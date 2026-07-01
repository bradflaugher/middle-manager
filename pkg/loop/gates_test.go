package loop

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bradflaugher/middle-manager/pkg/config"
)

func gitIn(t *testing.T, repo string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// newGateLoop builds a loop over a fresh committed repo with one tracked file.
func newGateLoop(t *testing.T) (*MiddleManagerLoop, string) {
	t.Helper()
	repo := initTestRepo(t)
	gitIn(t, repo, "config", "user.email", "t@t")
	gitIn(t, repo, "config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("# rules\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "app.py"), []byte("print('hi')\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "add", "-A")
	gitIn(t, repo, "commit", "-m", "init")

	cfg := config.NewDefaultConfig()
	cfg.Repo = repo
	cfg.StateDir = t.TempDir()
	return NewMiddleManagerLoop(cfg), repo
}

// Root-level memory-file edits the mission never asked for are reverted; a
// mission that names the file allows them; project files are untouched.
func TestGuardMemoryFiles(t *testing.T) {
	l, repo := newGateLoop(t)

	// Agent "helpfully" edits AGENTS.md, creates CLAUDE.md, and edits app.py.
	if err := os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("# hijacked\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "CLAUDE.md"), []byte("agent memory\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "app.py"), []byte("print('bye')\n"), 0644); err != nil {
		t.Fatal(err)
	}

	l.guardMemoryFiles()

	if b, _ := os.ReadFile(filepath.Join(repo, "AGENTS.md")); string(b) != "# rules\n" {
		t.Errorf("AGENTS.md edit not reverted: %q", b)
	}
	if _, err := os.Stat(filepath.Join(repo, "CLAUDE.md")); !os.IsNotExist(err) {
		t.Error("untracked CLAUDE.md not removed")
	}
	if b, _ := os.ReadFile(filepath.Join(repo, "app.py")); string(b) != "print('bye')\n" {
		t.Errorf("project file must not be touched by the guard: %q", b)
	}

	// Escape hatch: the mission names the file.
	l.cfg.Mission = "rewrite AGENTS.md with better build instructions"
	if err := os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("# improved\n"), 0644); err != nil {
		t.Fatal(err)
	}
	l.guardMemoryFiles()
	if b, _ := os.ReadFile(filepath.Join(repo, "AGENTS.md")); string(b) != "# improved\n" {
		t.Errorf("mission-sanctioned AGENTS.md edit was reverted: %q", b)
	}
}

// Subdirectory files with memory names belong to the project — never guarded.
func TestGuardMemoryFilesRootOnly(t *testing.T) {
	l, repo := newGateLoop(t)
	sub := filepath.Join(repo, "pkg")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "AGENTS.md"), []byte("subdir doc\n"), 0644); err != nil {
		t.Fatal(err)
	}
	l.guardMemoryFiles()
	if _, err := os.Stat(filepath.Join(sub, "AGENTS.md")); err != nil {
		t.Error("subdirectory AGENTS.md must not be touched")
	}
}

// The scan catches credential shapes in the tracked diff's ADDED lines and in
// untracked files, and ignores deletions (removing a secret must not block).
func TestScanForSecrets(t *testing.T) {
	l, repo := newGateLoop(t)

	if hits := l.scanForSecrets(); len(hits) != 0 {
		t.Fatalf("clean tree produced hits: %+v", hits)
	}

	// Tracked file gains a GitHub token; a new untracked file holds an AWS key.
	if err := os.WriteFile(filepath.Join(repo, "app.py"),
		[]byte("TOKEN = 'ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789'\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "deploy.env"),
		[]byte("AWS_KEY=AKIAIOSFODNN7EXAMPLE\n"), 0644); err != nil {
		t.Fatal(err)
	}

	hits := l.scanForSecrets()
	if len(hits) != 2 {
		t.Fatalf("hits = %+v, want GitHub token in app.py and AWS key in deploy.env", hits)
	}
	found := map[string]string{}
	for _, h := range hits {
		found[h.File] = h.Pattern
	}
	if !strings.Contains(found["app.py"], "GitHub") || !strings.Contains(found["deploy.env"], "AWS") {
		t.Errorf("wrong attribution: %+v", hits)
	}

	// Commit the secret, then remove it: the deletion-only diff must not hit.
	gitIn(t, repo, "add", "-A")
	gitIn(t, repo, "commit", "-m", "oops")
	if err := os.WriteFile(filepath.Join(repo, "app.py"), []byte("TOKEN = load_from_env()\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(repo, "deploy.env")); err != nil {
		t.Fatal(err)
	}
	if hits := l.scanForSecrets(); len(hits) != 0 {
		t.Errorf("removing secrets must not be blocked, got %+v", hits)
	}
}

// The combined gate fails the iteration on a hit, records it in the error log
// for the next attempt, and honors the --no-secret-scan escape hatch.
func TestEnforcePreCommitGates(t *testing.T) {
	l, repo := newGateLoop(t)
	if !l.enforcePreCommitGates() {
		t.Fatal("clean tree must pass the gates")
	}

	if err := os.WriteFile(filepath.Join(repo, "creds.txt"),
		[]byte("-----BEGIN RSA PRIVATE KEY-----\nabc\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if l.enforcePreCommitGates() {
		t.Fatal("a private key block must fail the gates")
	}
	if log := l.ReadText(l.errorLogPath, ""); !strings.Contains(log, "SECRET SCAN") || !strings.Contains(log, "creds.txt") {
		t.Errorf("gate findings not fed to the error log: %q", log)
	}

	l.cfg.NoSecretScan = true
	if !l.enforcePreCommitGates() {
		t.Error("--no-secret-scan must bypass the scan")
	}
}
