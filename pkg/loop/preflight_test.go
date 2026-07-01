package loop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bradflaugher/middle-manager/pkg/agents"
	"github.com/bradflaugher/middle-manager/pkg/config"
)

// hermeticOverrides pins every builtin to a nonexistent binary, then marks the
// listed agents installed — preflight results must not depend on the host.
func hermeticOverrides(installed ...string) map[string]string {
	m := map[string]string{}
	for _, name := range agents.AgentNames {
		m[name] = "/nonexistent/mm-test-binary"
	}
	for _, name := range installed {
		m[name] = "/bin/sh"
	}
	return m
}

func TestPreflightNoAgentsIsFatal(t *testing.T) {
	cfg := config.NewDefaultConfig()
	cfg.Repo = t.TempDir()
	cfg.StateDir = t.TempDir()
	cfg.NoPR = true
	cfg.BinaryOverrides = hermeticOverrides()

	if _, fatal := Preflight(cfg); fatal == nil || !strings.Contains(fatal.Error(), "no agent CLIs") {
		t.Fatalf("want fatal no-agents error, got %v", fatal)
	}
}

func TestPreflightWarnsOnUnknownAndMissingAgents(t *testing.T) {
	cfg := config.NewDefaultConfig()
	cfg.Repo = t.TempDir()
	cfg.StateDir = t.TempDir()
	cfg.NoPR = true
	cfg.BinaryOverrides = hermeticOverrides("claude")
	cfg.Execute.Agent = "claude"
	cfg.Execute.Escalate = []config.AgentRef{{Agent: "nonsense-agent"}}
	cfg.Verify.Agent = "grok" // known but not installed

	warnings, fatal := Preflight(cfg)
	if fatal != nil {
		t.Fatalf("unexpected fatal: %v", fatal)
	}
	joined := strings.Join(warnings, "\n")
	if !strings.Contains(joined, `unknown agent "nonsense-agent"`) {
		t.Errorf("missing unknown-agent warning: %q", joined)
	}
	if !strings.Contains(joined, `"grok" is not installed`) {
		t.Errorf("missing not-installed warning: %q", joined)
	}
}

func TestPreflightWarnsOnDirtyTree(t *testing.T) {
	repo := initTestRepo(t)
	gitIn(t, repo, "config", "user.email", "t@t")
	gitIn(t, repo, "config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(repo, "wip.txt"), []byte("uncommitted"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := config.NewDefaultConfig()
	cfg.Repo = repo
	cfg.StateDir = t.TempDir()
	cfg.NoPR = true
	cfg.BinaryOverrides = hermeticOverrides("claude")

	warnings, fatal := Preflight(cfg)
	if fatal != nil {
		t.Fatalf("unexpected fatal: %v", fatal)
	}
	if !strings.Contains(strings.Join(warnings, "\n"), "uncommitted changes") {
		t.Errorf("missing dirty-tree warning: %q", warnings)
	}
}

func TestAcquireRepoLock(t *testing.T) {
	state := t.TempDir()

	release, err := AcquireRepoLock(state)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	// A second live-PID lock in another state dir is fine; the SAME dir is not.
	// (Same process re-acquiring is allowed — pid matches — so simulate a live
	// foreign holder with our own pid replaced check via a foreign live pid: use
	// pid 1, which is always alive on Linux.)
	lockPath := filepath.Join(state, "mm.lock")
	if err := os.WriteFile(lockPath, []byte("1"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := AcquireRepoLock(state); err == nil {
		t.Fatal("lock held by a live pid must refuse a second acquire")
	}

	// A stale lock (dead pid) is taken over.
	if err := os.WriteFile(lockPath, []byte("99999999"), 0644); err != nil {
		t.Fatal(err)
	}
	release2, err := AcquireRepoLock(state)
	if err != nil {
		t.Fatalf("stale lock not taken over: %v", err)
	}
	release2()
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Error("release did not remove the lockfile")
	}
	release() // idempotent-ish cleanup; file already gone
}
