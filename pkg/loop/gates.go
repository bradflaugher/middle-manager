package loop

import (
	"fmt"
	"os"
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

// scanForSecrets inspects what is about to ship — ADDED lines of the working
// diff plus the content of untracked files — for credential-shaped strings.
// Deletions are ignored (removing a secret must never block the commit).
func (l *MiddleManagerLoop) scanForSecrets() []secretHit {
	if !gitops.RepoIsGit(l.cfg.Repo) {
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

	diff, _, code, err := gitops.RunGit(l.cfg.Repo, "diff", "HEAD")
	if err == nil && code == 0 {
		current := ""
		for _, line := range strings.Split(diff, "\n") {
			if strings.HasPrefix(line, "+++ b/") {
				current = strings.TrimPrefix(line, "+++ b/")
				continue
			}
			if !strings.HasPrefix(line, "+") || strings.HasPrefix(line, "+++") {
				continue
			}
			for _, p := range secretPatterns {
				if p.re.MatchString(line) {
					record(current, p.Name)
				}
			}
		}
	}

	// Untracked files never appear in `diff HEAD` but WILL be swept up by the
	// commit — scan their whole content (bounded, text only).
	for _, entry := range gitops.StatusEntries(l.cfg.Repo) {
		if entry.Status != "??" {
			continue
		}
		file := entry.Path
		abs := filepath.Join(l.cfg.Repo, file)
		info, err := os.Stat(abs)
		if err != nil || info.IsDir() || info.Size() > 1<<20 {
			continue
		}
		b, err := os.ReadFile(abs)
		if err != nil || strings.ContainsRune(string(b[:min(len(b), 8000)]), '\x00') {
			continue // unreadable or binary
		}
		for _, p := range secretPatterns {
			if p.re.Match(b) {
				record(file, p.Name)
			}
		}
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
