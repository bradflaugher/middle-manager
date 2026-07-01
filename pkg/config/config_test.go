package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSoloFlagParsing(t *testing.T) {
	_, cfg, err := ParseArgs([]string{"--solo", "--label", "bug"})
	if err != nil {
		t.Fatalf("ParseArgs: %v", err)
	}
	if !cfg.Solo {
		t.Error("--solo did not set Solo")
	}
	if cfg.Steps != 1 {
		t.Errorf("Solo Steps = %d, want 1", cfg.Steps)
	}
	if cfg.Commit.Enabled {
		t.Error("Solo must disable the commit step")
	}
	if !cfg.WaitForMerge {
		t.Error("Solo must enable WaitForMerge")
	}
}

// `--steps 1` is an alias for solo mode.
func TestStepsOneIsSolo(t *testing.T) {
	_, cfg, err := ParseArgs([]string{"--steps", "1", "--label", "bug"})
	if err != nil {
		t.Fatalf("ParseArgs: %v", err)
	}
	if !cfg.Solo || !cfg.WaitForMerge || cfg.Commit.Enabled {
		t.Fatalf("--steps 1 should be solo: solo=%v wait=%v commit=%v", cfg.Solo, cfg.WaitForMerge, cfg.Commit.Enabled)
	}
}

func TestWorktreeFlagImpliesQueue(t *testing.T) {
	_, cfg, err := ParseArgs([]string{"--worktree", "--label", "bug"})
	if err != nil {
		t.Fatalf("ParseArgs: %v", err)
	}
	if !cfg.Worktree {
		t.Error("--worktree did not set Worktree")
	}
	if cfg.Mode != "queue" {
		t.Errorf("Mode = %q, want queue", cfg.Mode)
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("worktree+queue should validate, got: %v", err)
	}
}

func TestValidateMutualExclusion(t *testing.T) {
	cfg := NewDefaultConfig()
	cfg.Mode = "queue"
	cfg.Worktree = true
	cfg.Solo = true
	if err := cfg.Validate(); err == nil {
		t.Fatal("worktree + solo must be rejected")
	} else if !strings.Contains(err.Error(), "competing") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateWorktreeRequiresQueue(t *testing.T) {
	cfg := NewDefaultConfig()
	cfg.Mode = "feature"
	cfg.Worktree = true
	if err := cfg.Validate(); err == nil {
		t.Fatal("worktree outside queue mode must be rejected")
	}
}

func TestActiveStepsSolo(t *testing.T) {
	cfg := NewDefaultConfig()
	cfg.Solo = true
	got := cfg.ActiveSteps()
	if len(got) != 1 || got[0] != "solo" {
		t.Fatalf("solo ActiveSteps = %v, want [solo]", got)
	}
	// StepFor("solo") must reuse the Execute slot so the picker configures it.
	if cfg.StepFor("solo") != &cfg.Execute {
		t.Error("StepFor(solo) must alias the Execute step config")
	}
}

func TestMergeTimeoutDefault(t *testing.T) {
	if NewDefaultConfig().MergeTimeoutMinutes != 60 {
		t.Error("default merge timeout should be 60 minutes")
	}
	_, cfg, _ := ParseArgs([]string{"--solo", "--merge-timeout", "5", "--label", "x"})
	if cfg.MergeTimeoutMinutes != 5 {
		t.Errorf("--merge-timeout not parsed: %d", cfg.MergeTimeoutMinutes)
	}
}

func TestSoloFromJSONConfig(t *testing.T) {
	cfg := ConfigFromMap(map[string]interface{}{"solo": true}, "")
	if !cfg.Solo || cfg.Steps != 1 || !cfg.WaitForMerge {
		t.Fatalf("solo from JSON not applied: solo=%v steps=%d wait=%v", cfg.Solo, cfg.Steps, cfg.WaitForMerge)
	}
}

// Regression: a JSON config of {"steps":1} must normalize to full solo mode, not
// a half-set state that runs the solo step but opens no PR.
func TestStepsOneJSONNormalizesToSolo(t *testing.T) {
	cfg := ConfigFromMap(map[string]interface{}{"steps": float64(1)}, "")
	if !cfg.IsSolo() || !cfg.Solo || cfg.Steps != 1 || !cfg.WaitForMerge || cfg.Commit.Enabled {
		t.Fatalf("steps:1 JSON did not normalize to solo: solo=%v steps=%d wait=%v commit=%v",
			cfg.Solo, cfg.Steps, cfg.WaitForMerge, cfg.Commit.Enabled)
	}
}

// Regression: --solo --no-wait-merge must NOT strand a queue — solo forces the
// merge wait so serialization holds regardless of flag order.
func TestSoloForcesWaitDespiteNoWaitFlag(t *testing.T) {
	_, cfg, err := ParseArgs([]string{"--solo", "--no-wait-merge", "--label", "bug"})
	if err != nil {
		t.Fatalf("ParseArgs: %v", err)
	}
	if !cfg.WaitForMerge {
		t.Fatal("--solo must force WaitForMerge even when --no-wait-merge follows it")
	}
}

// Regression: --worktree with no issue-queue source must be rejected (it can't
// silently degrade to a plain single loop).
func TestValidateWorktreeNeedsQueueSource(t *testing.T) {
	_, cfg, _ := ParseArgs([]string{"--worktree"}) // no --label/--author
	if cfg.IssueQueue != nil {
		t.Fatal("bare --worktree should not allocate an issue queue")
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("--worktree without a queue source must be rejected")
	}
}

func TestIsSolo(t *testing.T) {
	if (&LoopConfig{Steps: 1}).IsSolo() != true {
		t.Error("Steps==1 must be solo")
	}
	if (&LoopConfig{Solo: true, Steps: 4}).IsSolo() != true {
		t.Error("Solo flag must be solo regardless of Steps")
	}
	if (&LoopConfig{Steps: 4}).IsSolo() != false {
		t.Error("4-step is not solo")
	}
}

// The default state dir must live OUTSIDE the repo (no pollution), be
// deterministic, and keep same-basename repos apart.
func TestDefaultStatePathOutsideRepo(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	repo := t.TempDir()

	got := DefaultStatePath(repo)
	if !strings.HasPrefix(got, filepath.Join(stateHome, "middle-manager")) {
		t.Fatalf("state path %q not under XDG_STATE_HOME", got)
	}
	if rel, err := filepath.Rel(repo, got); err == nil && !strings.HasPrefix(rel, "..") {
		t.Fatalf("state path %q is inside the repo %q", got, repo)
	}
	if got != DefaultStatePath(repo) {
		t.Fatal("state path is not deterministic")
	}

	other := filepath.Join(t.TempDir(), filepath.Base(repo))
	if err := os.MkdirAll(other, 0755); err != nil {
		t.Fatal(err)
	}
	if DefaultStatePath(other) == got {
		t.Fatal("two repos with the same basename must not share a state dir")
	}
}

// NotesPath must stay stable once pinned, even when the queue overrides
// StateDir per issue — otherwise cross-issue learnings fragment.
func TestNotesPathPinnedAcrossStateDirOverride(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	cfg := NewDefaultConfig()
	cfg.Repo = t.TempDir()

	notes := cfg.NotesPath()
	cfg.NotesFile = notes // what the queue runner does before per-issue overrides

	cfg.StateDir = t.TempDir() // per-issue override
	if cfg.NotesPath() != notes {
		t.Fatalf("notes moved with StateDir: %q != %q", cfg.NotesPath(), notes)
	}
}
