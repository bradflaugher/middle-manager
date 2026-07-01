package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMain isolates the whole package from the operator's real persistent
// config (~/.config/middle-manager/config.json), which ParseArgs now auto-loads.
func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "mm-config-test-*")
	if err == nil {
		os.Setenv("XDG_CONFIG_HOME", tmp)
	}
	code := m.Run()
	if tmp != "" {
		os.RemoveAll(tmp)
	}
	os.Exit(code)
}

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

// Escalation ladders parse from CLI ("agent:model,agent"), keeping models
// intact even when they contain colons (ollama-style tags).
func TestEscalateFlagParsing(t *testing.T) {
	_, cfg, err := ParseArgs([]string{"--execute-agent", "opencode", "--execute-escalate", "claude:opus, codex", "--execute-timeout", "30"})
	if err != nil {
		t.Fatalf("ParseArgs: %v", err)
	}
	want := []AgentRef{{Agent: "claude", Model: "opus"}, {Agent: "codex"}}
	if len(cfg.Execute.Escalate) != 2 || cfg.Execute.Escalate[0] != want[0] || cfg.Execute.Escalate[1] != want[1] {
		t.Fatalf("Escalate = %+v, want %+v", cfg.Execute.Escalate, want)
	}
	if cfg.Execute.TimeoutMinutes != 30 {
		t.Errorf("per-step timeout = %d, want 30", cfg.Execute.TimeoutMinutes)
	}

	if ref := ParseAgentRef("opencode:ollama/qwen2.5:14b"); ref.Model != "ollama/qwen2.5:14b" {
		t.Errorf("colon-bearing model mangled: %+v", ref)
	}
}

func TestFactoryFlagParsing(t *testing.T) {
	_, cfg, err := ParseArgs([]string{"--step-timeout", "0", "--escalate-after", "2", "--distinct-verifier", "--max-wall-minutes", "90"})
	if err != nil {
		t.Fatalf("ParseArgs: %v", err)
	}
	if cfg.StepTimeoutMinutes != 0 {
		t.Errorf("--step-timeout 0 should disable the global timeout, got %d", cfg.StepTimeoutMinutes)
	}
	if cfg.EscalateAfter != 2 || !cfg.DistinctVerifier || cfg.MaxWallMinutes != 90 {
		t.Errorf("factory flags not applied: after=%d distinct=%v wall=%d", cfg.EscalateAfter, cfg.DistinctVerifier, cfg.MaxWallMinutes)
	}
	if def := NewDefaultConfig(); def.StepTimeoutMinutes != 60 || def.EscalateAfter != 1 {
		t.Errorf("defaults: timeout=%d after=%d, want 60/1", def.StepTimeoutMinutes, def.EscalateAfter)
	}
}

// JSON configs accept ladders as strings, string lists, or object lists, and
// declare custom agents under "agents".
func TestJSONFactoryConfig(t *testing.T) {
	cfg := ConfigFromMap(map[string]interface{}{
		"step_timeout_minutes": float64(15),
		"distinct_verifier":    true,
		"execute": map[string]interface{}{
			"agent":    "aider",
			"escalate": []interface{}{"claude:opus", map[string]interface{}{"agent": "codex", "model": "gpt-5"}},
		},
		"verify": map[string]interface{}{
			"agent":    "claude",
			"escalate": "claude:opus",
			"enabled":  true,
		},
		"agents": map[string]interface{}{
			"aider": map[string]interface{}{
				"binary":     "aider",
				"print_flag": "--message",
				"yolo_flags": []interface{}{"--yes-always"},
				"model_flag": "--model",
			},
			"noname": map[string]interface{}{},
		},
	}, "")

	if cfg.StepTimeoutMinutes != 15 || !cfg.DistinctVerifier {
		t.Errorf("globals not applied: timeout=%d distinct=%v", cfg.StepTimeoutMinutes, cfg.DistinctVerifier)
	}
	if len(cfg.Execute.Escalate) != 2 ||
		cfg.Execute.Escalate[0] != (AgentRef{Agent: "claude", Model: "opus"}) ||
		cfg.Execute.Escalate[1] != (AgentRef{Agent: "codex", Model: "gpt-5"}) {
		t.Errorf("execute ladder = %+v", cfg.Execute.Escalate)
	}
	if len(cfg.Verify.Escalate) != 1 || cfg.Verify.Escalate[0].Model != "opus" {
		t.Errorf("verify ladder (string form) = %+v", cfg.Verify.Escalate)
	}
	aider, ok := cfg.CustomAgents["aider"]
	if !ok || aider.PrintFlag != "--message" || len(aider.YoloFlags) != 1 {
		t.Errorf("custom agent not parsed: %+v", aider)
	}
	if cfg.CustomAgents["noname"].Binary != "noname" {
		t.Error("custom agent with no binary should default to its name")
	}
}

// The persistent operator config auto-loads and CLI flags still win over it.
func TestDefaultConfigFileAutoLoads(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	dir := filepath.Join(configHome, "middle-manager")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	body := `{"escalate_after": 3, "max_iterations": 7, "agents": {"myagent": {"binary": "/usr/bin/myagent"}}}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}

	_, cfg, err := ParseArgs([]string{"--max-iterations", "4"})
	if err != nil {
		t.Fatalf("ParseArgs: %v", err)
	}
	if cfg.EscalateAfter != 3 {
		t.Errorf("persistent config not loaded: EscalateAfter=%d", cfg.EscalateAfter)
	}
	if cfg.MaxIterations != 4 {
		t.Errorf("CLI flag must override persistent config: MaxIterations=%d", cfg.MaxIterations)
	}
	if cfg.CustomAgents["myagent"].Binary != "/usr/bin/myagent" {
		t.Errorf("custom agent from persistent config missing: %+v", cfg.CustomAgents)
	}
}

// strength_order parses from JSON (list or comma string) and the CLI, and
// SaveStrengthOrder persists it without clobbering other config keys.
func TestStrengthOrder(t *testing.T) {
	cfg := ConfigFromMap(map[string]interface{}{
		"strength_order": []interface{}{"codex", " claude "},
	}, "")
	if len(cfg.StrengthOrder) != 2 || cfg.StrengthOrder[0] != "codex" || cfg.StrengthOrder[1] != "claude" {
		t.Errorf("JSON list strength order = %v", cfg.StrengthOrder)
	}
	cfg = ConfigFromMap(map[string]interface{}{"strength_order": "grok, opencode"}, "")
	if len(cfg.StrengthOrder) != 2 || cfg.StrengthOrder[1] != "opencode" {
		t.Errorf("JSON string strength order = %v", cfg.StrengthOrder)
	}

	_, cfg2, err := ParseArgs([]string{"--strength-order", "claude,codex"})
	if err != nil {
		t.Fatalf("ParseArgs: %v", err)
	}
	if len(cfg2.StrengthOrder) != 2 || cfg2.StrengthOrder[0] != "claude" {
		t.Errorf("--strength-order = %v", cfg2.StrengthOrder)
	}
}

func TestSaveStrengthOrderPreservesConfig(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	dir := filepath.Join(configHome, "middle-manager")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	existing := `{"escalate_after": 3, "agents": {"aider": {"binary": "aider"}}}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(existing), 0644); err != nil {
		t.Fatal(err)
	}

	if err := SaveStrengthOrder([]string{"codex", "claude"}); err != nil {
		t.Fatalf("SaveStrengthOrder: %v", err)
	}

	_, cfg, err := ParseArgs(nil)
	if err != nil {
		t.Fatalf("ParseArgs: %v", err)
	}
	if len(cfg.StrengthOrder) != 2 || cfg.StrengthOrder[0] != "codex" {
		t.Errorf("saved strength order not loaded: %v", cfg.StrengthOrder)
	}
	if cfg.EscalateAfter != 3 || cfg.CustomAgents["aider"].Binary != "aider" {
		t.Errorf("pre-existing config keys clobbered: after=%d agents=%+v", cfg.EscalateAfter, cfg.CustomAgents)
	}

	// Saving with no pre-existing file creates it.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := SaveStrengthOrder([]string{"claude"}); err != nil {
		t.Fatalf("SaveStrengthOrder (fresh): %v", err)
	}
	_, cfg3, _ := ParseArgs(nil)
	if len(cfg3.StrengthOrder) != 1 || cfg3.StrengthOrder[0] != "claude" {
		t.Errorf("fresh save not loaded: %v", cfg3.StrengthOrder)
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
