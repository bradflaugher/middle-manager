package loop

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bradflaugher/middle-manager/pkg/agents"
	"github.com/bradflaugher/middle-manager/pkg/config"
)

// The PR body's merge guidance must match the loop's actual merge behavior:
// auto-merge mode must NOT print the human-review warning (the contradiction we
// hit in production), and non-auto-merge mode must keep it.
func TestPRBodyMatchesMergeMode(t *testing.T) {
	autoMerge := prBody(3, true)
	if strings.Contains(autoMerge, "Do not merge without human review") {
		t.Errorf("auto-merge PR body must not warn against merging: %q", autoMerge)
	}
	if !strings.Contains(strings.ToLower(autoMerge), "auto-merge is enabled") {
		t.Errorf("auto-merge PR body should explain auto-merge: %q", autoMerge)
	}

	manual := prBody(3, false)
	if !strings.Contains(manual, "**Do not merge without human review.**") {
		t.Errorf("non-auto-merge PR body must keep the human-review note: %q", manual)
	}
	if strings.Contains(strings.ToLower(manual), "auto-merge is enabled") {
		t.Errorf("non-auto-merge PR body must not claim auto-merge: %q", manual)
	}
}

func TestParseVerifierUpdates(t *testing.T) {
	l := &MiddleManagerLoop{}
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"explicit pass", "SUMMARY: ok\nVERDICT: PASS\n", "PASS"},
		{"explicit fail", "VERDICT: FAIL\nISSUES: broken", "FAIL"},
		{"lowercase pass", "verdict: pass", "PASS"},
		{"no verdict line", "looks good to me, shipping it", "UNKNOWN"},
		{"both -> fail wins", "VERDICT: PASS\n...then actually VERDICT: FAIL", "FAIL"},
		{"empty", "", "UNKNOWN"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := l.ParseVerifierUpdates(c.in); got != c.want {
				t.Errorf("ParseVerifierUpdates(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// The step resolver must map the random sentinel to whatever was rolled for
// the iteration, and pass concrete agents through unchanged.
func TestResolveAgentRandom(t *testing.T) {
	l := &MiddleManagerLoop{cfg: config.NewDefaultConfig()}
	l.iterationAgent = "grok"

	if got, _, _ := l.resolveStepAgentModel("execute", &config.StepConfig{Agent: agents.RandomAgent}); got != "grok" {
		t.Errorf("random resolved to %q, want the iteration's grok", got)
	}
	if got, _, _ := l.resolveStepAgentModel("execute", &config.StepConfig{Agent: "claude"}); got != "claude" {
		t.Errorf("explicit agent changed to %q, want claude", got)
	}
	// No agent rolled (nothing installed) → random resolves to empty.
	l.iterationAgent = ""
	if got, _, _ := l.resolveStepAgentModel("execute", &config.StepConfig{Agent: agents.RandomAgent}); got != "" {
		t.Errorf("random with no roll = %q, want empty", got)
	}
}

// Escalation ladders: tier 0 is the configured base; every EscalateAfter
// failed iterations advance one rung, capped at the ladder top; the rung's
// agent+model replace the base pair.
func TestEscalationTiers(t *testing.T) {
	cfg := config.NewDefaultConfig()
	cfg.EscalateAfter = 2
	l := &MiddleManagerLoop{cfg: cfg}
	sc := &config.StepConfig{
		Agent: "opencode", Model: "cheap-model",
		Escalate: []config.AgentRef{{Agent: "claude", Model: "opus"}, {Agent: "codex"}},
	}

	check := func(failed int, wantAgent, wantModel string, wantTier int) {
		t.Helper()
		l.failedIters = failed
		agent, model, tier := l.resolveStepAgentModel("execute", sc)
		if agent != wantAgent || model != wantModel || tier != wantTier {
			t.Errorf("failedIters=%d → (%q, %q, %d), want (%q, %q, %d)",
				failed, agent, model, tier, wantAgent, wantModel, wantTier)
		}
	}
	check(0, "opencode", "cheap-model", 0)
	check(1, "opencode", "cheap-model", 0)
	check(2, "claude", "opus", 1)
	check(4, "codex", "", 2)
	check(99, "codex", "", 2) // capped at the ladder top

	// A step with no ladder never escalates.
	l.failedIters = 99
	if agent, _, tier := l.resolveStepAgentModel("execute", &config.StepConfig{Agent: "grok"}); agent != "grok" || tier != 0 {
		t.Errorf("ladder-less step escalated: agent=%q tier=%d", agent, tier)
	}
}

// With DistinctVerifier on, the verify step must resolve to a different agent
// than the executor whenever another agent is installed — the critic never
// grades its own homework.
func TestDistinctVerifier(t *testing.T) {
	cfg := config.NewDefaultConfig()
	cfg.DistinctVerifier = true
	cfg.Execute.Agent = "claude"
	cfg.Verify.Agent = "claude"
	// Hermetic install set: pin EVERY builtin to a nonexistent binary (overrides
	// do not fall back to PATH), then mark just claude+grok as installed.
	fakeInstalled := func(installed ...string) map[string]string {
		m := map[string]string{}
		for _, name := range agents.AgentNames {
			m[name] = "/nonexistent/mm-test-binary"
		}
		for _, name := range installed {
			m[name] = "/bin/sh"
		}
		return m
	}
	cfg.BinaryOverrides = fakeInstalled("claude", "grok")
	l := &MiddleManagerLoop{cfg: cfg}

	agent, _, _ := l.resolveStepAgentModel("verify", &cfg.Verify)
	if agent == "claude" {
		t.Fatalf("verifier = executor (%q); distinct verifier must swap", agent)
	}
	if agent != "grok" {
		t.Fatalf("verifier = %q, want the other installed agent (grok)", agent)
	}

	// Executor untouched, and a verify agent that already differs is kept.
	if got, _, _ := l.resolveStepAgentModel("execute", &cfg.Execute); got != "claude" {
		t.Errorf("execute agent changed to %q", got)
	}
	cfg.Verify.Agent = "grok"
	l2 := &MiddleManagerLoop{cfg: cfg}
	if got, _, _ := l2.resolveStepAgentModel("verify", &cfg.Verify); got != "grok" {
		t.Errorf("already-distinct verifier changed to %q", got)
	}

	// With only the executor installed, keep it rather than break the loop.
	cfg.Verify.Agent = "claude"
	cfg.BinaryOverrides = fakeInstalled("claude")
	l3 := &MiddleManagerLoop{cfg: cfg}
	if got, _, _ := l3.resolveStepAgentModel("verify", &cfg.Verify); got != "claude" {
		t.Errorf("single-agent fallback = %q, want claude", got)
	}
}

// The escalation handoff banner fires only for the working agents (execute /
// solo) at tier > 0, and names the predecessor when it differs.
func TestEscalationNotice(t *testing.T) {
	if got := escalationNotice("execute", 0, 2, "claude", ""); got != "" {
		t.Errorf("tier 0 must have no notice, got %q", got)
	}
	if got := escalationNotice("verify", 1, 2, "claude", "opencode"); got != "" {
		t.Errorf("verify must not get the notice (unbiased auditor), got %q", got)
	}
	got := escalationNotice("execute", 1, 2, "claude", "opencode")
	if !strings.Contains(got, "tier-1") || !strings.Contains(got, "CLAUDE") || !strings.Contains(got, "OPENCODE") {
		t.Errorf("notice missing tier/agent/predecessor: %q", got)
	}
	if !strings.Contains(got, "revert") {
		t.Errorf("notice must tell the agent it may revert prior work: %q", got)
	}
	// Unknown predecessor degrades to generic wording, never an empty name.
	got = escalationNotice("solo", 2, 3, "codex", "")
	if !strings.Contains(got, "a previous agent") {
		t.Errorf("missing generic predecessor wording: %q", got)
	}
}

// bumpTier (used when the stall detector finds headroom) must jump to the next
// escalation boundary so every ladder advances exactly one rung.
func TestBumpTierAndHeadroom(t *testing.T) {
	cfg := config.NewDefaultConfig()
	cfg.EscalateAfter = 3
	cfg.Execute.Escalate = []config.AgentRef{{Agent: "claude"}}
	l := &MiddleManagerLoop{cfg: cfg}

	l.failedIters = 1
	if !l.escalationHeadroom() {
		t.Fatal("expected headroom before the ladder top")
	}
	l.bumpTier()
	if got := l.tierFor(&cfg.Execute); got != 1 {
		t.Errorf("after bumpTier tier = %d, want 1", got)
	}
	if l.escalationHeadroom() {
		t.Error("no headroom expected at the ladder top")
	}
}

func TestPRNumberFromURL(t *testing.T) {
	cases := map[string]int{
		"https://github.com/o/r/pull/42": 42,
		"https://github.com/o/r/pull/7":  7,
		"":                               0,
		"not-a-url":                      0,
		"https://github.com/o/r/pull/x":  0,
	}
	for url, want := range cases {
		if got := prNumberFromURL(url); got != want {
			t.Errorf("prNumberFromURL(%q) = %d, want %d", url, got, want)
		}
	}
}

func initTestRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo := t.TempDir()
	cmd := exec.Command("git", "init")
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	return repo
}

// A custom state dir INSIDE the repo must be excluded via .git/info/exclude
// (local-only), never by editing the repo's tracked .gitignore.
func TestEnsureStateExcludedInRepoStateDir(t *testing.T) {
	repo := initTestRepo(t)
	cfg := config.NewDefaultConfig()
	cfg.Repo = repo
	cfg.StateDir = filepath.Join(repo, ".mm-state")
	l := NewMiddleManagerLoop(cfg)

	l.EnsureStateExcluded()

	b, err := os.ReadFile(filepath.Join(repo, ".git", "info", "exclude"))
	if err != nil || !strings.Contains(string(b), "/.mm-state/") {
		t.Fatalf("in-repo state dir not excluded: err=%v content=%q", err, b)
	}
	if _, err := os.Stat(filepath.Join(repo, ".gitignore")); !os.IsNotExist(err) {
		t.Fatal("mm must never create or edit the repo's .gitignore")
	}

	// Idempotent: a second run must not duplicate the entry.
	l.EnsureStateExcluded()
	b2, _ := os.ReadFile(filepath.Join(repo, ".git", "info", "exclude"))
	if strings.Count(string(b2), "/.mm-state/") != 1 {
		t.Fatalf("exclude entry duplicated: %q", b2)
	}
}

// With the default (out-of-repo) state dir there is nothing to exclude and no
// repo file may be touched.
func TestEnsureStateExcludedExternalStateDirIsNoop(t *testing.T) {
	repo := initTestRepo(t)
	cfg := config.NewDefaultConfig()
	cfg.Repo = repo
	cfg.StateDir = t.TempDir()
	l := NewMiddleManagerLoop(cfg)

	l.EnsureStateExcluded()

	if b, err := os.ReadFile(filepath.Join(repo, ".git", "info", "exclude")); err == nil &&
		strings.Contains(string(b), "middle-manager") {
		t.Fatalf("external state dir should not be written to exclude: %q", b)
	}
	if _, err := os.Stat(filepath.Join(repo, ".gitignore")); !os.IsNotExist(err) {
		t.Fatal("mm must never create the repo's .gitignore")
	}
}

// A fresh single run must never destroy a queue drain's per-issue history —
// the ledgers under issues/ are the drain's cost records.
func TestResetLoopStatePreservesIssueLedgers(t *testing.T) {
	cfg := config.NewDefaultConfig()
	cfg.Repo = t.TempDir()
	cfg.StateDir = t.TempDir()
	l := NewMiddleManagerLoop(cfg)

	ledger := filepath.Join(cfg.StateDir, "issues", "7", "ledger.jsonl")
	if err := os.MkdirAll(filepath.Dir(ledger), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ledger, []byte(`{"type":"step"}`+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	l.WriteText(filepath.Join(cfg.StateDir, "error_log.txt"), "stale")

	l.ResetLoopState()

	if _, err := os.Stat(ledger); err != nil {
		t.Fatal("ResetLoopState destroyed a drain's per-issue ledger")
	}
	if _, err := os.Stat(filepath.Join(cfg.StateDir, "error_log.txt")); !os.IsNotExist(err) {
		t.Error("ResetLoopState should still sweep the run's own state files")
	}
}

// TestFailClosedPolicy documents the commit gate: only an explicit PASS ships;
// FAIL and UNKNOWN both block. (The loop turns "verdict != PASS" into a
// loop-back; this asserts the decision the loop relies on.)
func TestFailClosedPolicy(t *testing.T) {
	commits := func(verdict string) bool { return verdict == "PASS" }
	if !commits("PASS") {
		t.Error("PASS must commit")
	}
	if commits("FAIL") {
		t.Error("FAIL must not commit")
	}
	if commits("UNKNOWN") {
		t.Error("UNKNOWN must not commit (fail closed)")
	}
}
