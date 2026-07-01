package loop

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/bradflaugher/middle-manager/pkg/colors"
	"github.com/bradflaugher/middle-manager/pkg/gitops"
)

// Deterministic pre-commit gates: the LLM proposes, code disposes. A verifier
// PASS is necessary but not sufficient to ship — these checks run in Go, cost
// nothing, and cannot be sweet-talked.

// memoryFileNames are the agent-memory files mm injects into prompts and
// promises never to let agents modify. Guarded at the repo ROOT only —
// same-named files in subdirectories belong to the project, not to mm.
var memoryFileNames = map[string]bool{
	"AGENTS.md":    true,
	"CLAUDE.md":    true,
	"GEMINI.md":    true,
	".cursorrules": true,
}

// guardMemoryFiles reverts root-level memory-file edits the mission never
// asked for. Every prompt already forbids them, but a prompt is a request and
// this is a guarantee. Mentioning the file in the mission is the escape hatch.
func (l *MiddleManagerLoop) guardMemoryFiles() {
	if l.cfg.DryRun || !gitops.RepoIsGit(l.cfg.Repo) {
		return
	}
	mission := strings.ToLower(l.cfg.Mission)
	for _, entry := range gitops.StatusEntries(l.cfg.Repo) {
		status, file := entry.Status, entry.Path
		if strings.ContainsAny(file, "/\\") || !memoryFileNames[file] {
			continue
		}
		if mission != "" && strings.Contains(mission, strings.ToLower(file)) {
			continue // the operator explicitly asked for this file
		}
		abs := filepath.Join(l.cfg.Repo, file)
		if status == "??" {
			_ = os.Remove(abs)
		} else {
			// Restore the HEAD version (also unstages). A file staged as new has
			// no HEAD version — unstage it and remove the file instead.
			if _, _, code, _ := gitops.RunGit(l.cfg.Repo, "checkout", "HEAD", "--", file); code != 0 {
				_, _, _, _ = gitops.RunGit(l.cfg.Repo, "reset", "-q", "HEAD", "--", file)
				_ = os.Remove(abs)
			}
		}
		l.Log(fmt.Sprintf("🛡 Reverted unauthorized edit to %s — memory files are read-only to agents (mention the file in the mission to allow changes).", file), colors.Yellow)
	}
}

// secretPatterns are HIGH-CONFIDENCE credential shapes only. Anything fuzzier
// (generic "token = ..." matches) would drown real hits in fixture noise;
// --no-secret-scan is the escape hatch for repos whose fixtures still collide.
var secretPatterns = []struct {
	Name string
	re   *regexp.Regexp
}{
	{"AWS access key ID", regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`)},
	{"private key block", regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY( BLOCK)?-----`)},
	{"GitHub token", regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{36,}\b`)},
	{"Slack token", regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{10,}\b`)},
	{"Google API key", regexp.MustCompile(`\bAIza[0-9A-Za-z_\-]{35}\b`)},
	{"OpenAI-style secret key", regexp.MustCompile(`\bsk-[A-Za-z0-9_\-]{32,}\b`)},
}

type secretHit struct {
	File    string
	Pattern string
}

// changedCorpus collects what is about to ship, per file: the ADDED lines of
// the tracked diff, plus the full content of untracked files (which never
// appear in `diff HEAD` but WILL be swept up by the commit). Deletions are
// excluded on purpose — removing a secret must never block the commit.
func (l *MiddleManagerLoop) changedCorpus() map[string]string {
	corpus := map[string]string{}

	diff, _, code, err := gitops.RunGit(l.cfg.Repo, "diff", "HEAD")
	if err == nil && code == 0 {
		current := ""
		var b strings.Builder
		flush := func() {
			if current != "" && b.Len() > 0 {
				corpus[current] += b.String()
			}
			b.Reset()
		}
		for _, line := range strings.Split(diff, "\n") {
			if strings.HasPrefix(line, "+++ b/") {
				flush()
				current = strings.TrimPrefix(line, "+++ b/")
				continue
			}
			if !strings.HasPrefix(line, "+") || strings.HasPrefix(line, "+++") {
				continue
			}
			b.WriteString(strings.TrimPrefix(line, "+"))
			b.WriteByte('\n')
		}
		flush()
	}

	for _, entry := range gitops.StatusEntries(l.cfg.Repo) {
		if entry.Status != "??" {
			continue
		}
		abs := filepath.Join(l.cfg.Repo, entry.Path)
		info, err := os.Stat(abs)
		if err != nil || info.IsDir() || info.Size() > 1<<20 {
			continue
		}
		b, err := os.ReadFile(abs)
		if err != nil || strings.ContainsRune(string(b[:min(len(b), 8000)]), '\x00') {
			continue // unreadable or binary
		}
		corpus[entry.Path] = string(b)
	}
	return corpus
}

// scanForSecrets inspects the about-to-ship corpus for credential-shaped
// strings. The builtin high-confidence patterns always run; when `gitleaks`
// is installed its full ruleset runs too (see gitleaksScan) and findings are
// merged. Fail-open on scanner malfunction, fail-closed on findings.
func (l *MiddleManagerLoop) scanForSecrets() []secretHit {
	if !gitops.RepoIsGit(l.cfg.Repo) {
		return nil
	}
	corpus := l.changedCorpus()
	if len(corpus) == 0 {
		return nil
	}

	var hits []secretHit
	seen := map[string]bool{}
	record := func(file, pattern string) {
		key := file + "\x00" + pattern
		if !seen[key] {
			seen[key] = true
			hits = append(hits, secretHit{File: file, Pattern: pattern})
		}
	}

	for file, content := range corpus {
		for _, p := range secretPatterns {
			if p.re.MatchString(content) {
				record(file, p.Name)
			}
		}
	}
	for _, h := range l.gitleaksScan(corpus) {
		record(h.File, h.Pattern)
	}
	return hits
}

// gitleaksScan runs gitleaks — when it is installed — over a staged copy of
// the corpus, so its full community ruleset backs the builtin patterns. The
// staging keeps two properties the obvious `gitleaks dir <repo>` would lose:
// only NEW content is scanned (a pre-existing secret elsewhere in the repo
// must not block an unrelated change), and file attribution stays exact.
func (l *MiddleManagerLoop) gitleaksScan(corpus map[string]string) []secretHit {
	bin, err := exec.LookPath("gitleaks")
	if err != nil {
		return nil
	}

	stage := filepath.Join(l.state, "secretscan")
	_ = os.RemoveAll(stage)
	defer os.RemoveAll(stage)
	for file, content := range corpus {
		dst := filepath.Join(stage, filepath.FromSlash(file))
		if !strings.HasPrefix(dst, stage+string(filepath.Separator)) {
			continue // a hostile "../" path must not escape the staging dir
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
			continue
		}
		_ = os.WriteFile(dst, []byte(content), 0600)
	}

	report := filepath.Join(l.state, "gitleaks.json")
	defer os.Remove(report)
	// `gitleaks dir` (v8.19+), falling back to the older `detect --no-git`.
	// Exit 0 = clean, exit 1 = leaks found; anything else is a tool error and
	// the builtin patterns remain the backstop.
	run := func(args ...string) (int, string) {
		cmd := exec.Command(bin, args...)
		var stderr strings.Builder
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				return exitErr.ExitCode(), stderr.String()
			}
			return -1, stderr.String()
		}
		return 0, stderr.String()
	}
	code, stderr := run("dir", stage, "--no-banner", "--report-format", "json", "--report-path", report)
	if code != 0 && code != 1 && (strings.Contains(stderr, "unknown command") || strings.Contains(stderr, "unknown flag")) {
		code, _ = run("detect", "--no-git", "--source", stage, "--no-banner", "--report-format", "json", "--report-path", report)
	}
	if code != 0 && code != 1 {
		l.Log("⚠️ gitleaks is installed but errored — falling back to builtin secret patterns only.", colors.Yellow)
		return nil
	}

	b, err := os.ReadFile(report)
	if err != nil {
		return nil
	}
	var findings []struct {
		RuleID string `json:"RuleID"`
		File   string `json:"File"`
	}
	if err := json.Unmarshal(b, &findings); err != nil {
		return nil
	}
	var hits []secretHit
	for _, f := range findings {
		file := f.File
		if rel, err := filepath.Rel(stage, file); err == nil && !strings.HasPrefix(rel, "..") {
			file = filepath.ToSlash(rel)
		}
		hits = append(hits, secretHit{File: file, Pattern: "gitleaks:" + f.RuleID})
	}
	return hits
}

// enforcePreCommitGates runs the deterministic gates after a verifier PASS and
// before any commit. It returns false (fail the iteration, loop back with the
// findings in the error log) when secret-shaped content is about to ship.
func (l *MiddleManagerLoop) enforcePreCommitGates() bool {
	if l.cfg.DryRun || !gitops.RepoIsGit(l.cfg.Repo) {
		return true
	}
	l.guardMemoryFiles()
	if l.cfg.NoSecretScan {
		return true
	}
	hits := l.scanForSecrets()
	if len(hits) == 0 {
		return true
	}
	var b strings.Builder
	b.WriteString("=== SECRET SCAN BLOCKED THE COMMIT ===\n")
	b.WriteString("Credential-shaped strings were found in the changes. Remove them and load the values from the environment or a secret manager instead. Findings:\n")
	for _, h := range hits {
		b.WriteString(fmt.Sprintf("- %s: looks like a %s\n", h.File, h.Pattern))
		l.Log(fmt.Sprintf("🛡 %s: %s-shaped string — refusing to commit.", h.File, h.Pattern), colors.Red+colors.Bold)
	}
	l.Log("Secret scan failed the iteration (override with --no-secret-scan if these are known fixtures).", colors.Yellow)
	existing := l.ReadText(l.errorLogPath, "")
	l.WriteText(l.errorLogPath, b.String()+"\n"+existing)
	l.appendLedger(map[string]interface{}{"type": "gate", "gate": "secret_scan", "blocked": true, "hits": len(hits)})
	return false
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
