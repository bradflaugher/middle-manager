package agents

import (
	"reflect"
	"testing"
)

func TestCleanAgentEnv(t *testing.T) {
	input := []string{
		"PATH=/usr/bin",
		"CLAUDECODE=1",
		"CLAUDE_CODE_ENTRYPOINT=/some/path",
		"USER=root",
		"CLAUDE_CODE_SSE_PORT=1234",
		"CLAUDE_AGENT_SDK_VERSION=1.0.0",
		"OTHER_VAR=value",
	}

	expected := []string{
		"PATH=/usr/bin",
		"USER=root",
		"OTHER_VAR=value",
	}

	result := cleanAgentEnv(input)

	if !reflect.DeepEqual(result, expected) {
		t.Errorf("cleanAgentEnv() = %v, want %v", result, expected)
	}
}

// TestBuildCommand pins the exact headless argv for each agent. These are the
// commands middle-manager actually shells out to; if a CLI's flags change, this
// is where it should break loudly.
func TestBuildCommand(t *testing.T) {
	const p = "do the thing"
	const dir = "/repo"

	cases := map[string][]string{
		"grok":     {"grok", "-p", p, "--always-approve", "--cwd", dir},
		"claude":   {"claude", "-p", p, "--dangerously-skip-permissions"},
		"opencode": {"opencode", "run", "--dangerously-skip-permissions", "--dir", dir, p},
		"codex":    {"codex", "exec", "--dangerously-bypass-approvals-and-sandbox", "-C", dir, p},
		"agy":      {"agy", "-p", p, "--dangerously-skip-permissions"},
		"crush":    {"crush", "run", "-c", dir, p},
	}

	for agent, want := range cases {
		run, err := BuildCommand(agent, p, dir, "", true, nil, "")
		if err != nil {
			t.Fatalf("%s: BuildCommand error: %v", agent, err)
		}
		if !reflect.DeepEqual(run.Command, want) {
			t.Errorf("%s argv = %v, want %v", agent, run.Command, want)
		}
	}
}

func TestBuildCommandModelAndNoYolo(t *testing.T) {
	run, err := BuildCommand("grok", "x", "/repo", "grok-4", false, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"grok", "-p", "x", "-m", "grok-4", "--cwd", "/repo"}
	if !reflect.DeepEqual(run.Command, want) {
		t.Errorf("argv = %v, want %v", run.Command, want)
	}
}

func TestBuildCommandUnknownAgent(t *testing.T) {
	if _, err := BuildCommand("bogus", "x", "/repo", "", true, nil, ""); err == nil {
		t.Error("expected error for unknown agent")
	}
}

func TestIsRandom(t *testing.T) {
	if !IsRandom(RandomAgent) {
		t.Error("RandomAgent must satisfy IsRandom")
	}
	for _, name := range AgentNames {
		if IsRandom(name) {
			t.Errorf("concrete agent %q must not be random", name)
		}
	}
	if IsRandom("") {
		t.Error("empty string is not random")
	}
}

func TestPickRandomAgentFrom(t *testing.T) {
	// Filters blanks and the sentinel; index wraps; empty pool -> "".
	pool := []string{"", "claude", RandomAgent, "grok"}
	// filtered = [claude, grok]
	if got := PickRandomAgentFrom(pool, 0); got != "claude" {
		t.Errorf("idx 0 = %q, want claude", got)
	}
	if got := PickRandomAgentFrom(pool, 1); got != "grok" {
		t.Errorf("idx 1 = %q, want grok", got)
	}
	if got := PickRandomAgentFrom(pool, 2); got != "claude" {
		t.Errorf("idx 2 (wrap) = %q, want claude", got)
	}
	if got := PickRandomAgentFrom(pool, -1); got != "grok" {
		t.Errorf("idx -1 (wrap) = %q, want grok", got)
	}
	if got := PickRandomAgentFrom([]string{RandomAgent, ""}, 0); got != "" {
		t.Errorf("pool with no concrete agents = %q, want empty", got)
	}
	if got := PickRandomAgentFrom(nil, 5); got != "" {
		t.Errorf("nil pool = %q, want empty", got)
	}
}

// PickRandomAgent must never panic and must always return either "" (nothing
// installed) or a real installed agent — never the sentinel.
func TestPickRandomAgentNeverReturnsSentinel(t *testing.T) {
	for i := 0; i < 50; i++ {
		got := PickRandomAgent(nil)
		if got == RandomAgent {
			t.Fatal("PickRandomAgent returned the sentinel")
		}
		if got != "" && !AgentAvailable(got, "") {
			t.Fatalf("PickRandomAgent returned %q which is not installed", got)
		}
	}
}

func TestWithRootSandbox(t *testing.T) {
	has := func(env []string, want string) bool {
		for _, e := range env {
			if e == want {
				return true
			}
		}
		return false
	}

	// Non-root: untouched.
	if got := withRootSandbox([]string{"PATH=/x"}, 1000); has(got, "IS_SANDBOX=1") {
		t.Error("non-root should not get IS_SANDBOX")
	}
	// Root: IS_SANDBOX=1 injected.
	if got := withRootSandbox([]string{"PATH=/x"}, 0); !has(got, "IS_SANDBOX=1") {
		t.Error("root should get IS_SANDBOX=1")
	}
	// Root with an explicit setting: respected, not overridden.
	got := withRootSandbox([]string{"IS_SANDBOX=0"}, 0)
	if has(got, "IS_SANDBOX=1") {
		t.Error("explicit IS_SANDBOX must be respected")
	}
}
