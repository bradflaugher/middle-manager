package loop

import (
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

// resolveAgent must map the random sentinel to whatever was rolled for the
// iteration, and pass concrete agents through unchanged.
func TestResolveAgentRandom(t *testing.T) {
	l := &MiddleManagerLoop{cfg: config.NewDefaultConfig()}
	l.iterationAgent = "grok"

	if got := l.resolveAgent(&config.StepConfig{Agent: agents.RandomAgent}); got != "grok" {
		t.Errorf("random resolved to %q, want the iteration's grok", got)
	}
	if got := l.resolveAgent(&config.StepConfig{Agent: "claude"}); got != "claude" {
		t.Errorf("explicit agent changed to %q, want claude", got)
	}
	// No agent rolled (nothing installed) → random resolves to empty.
	l.iterationAgent = ""
	if got := l.resolveAgent(&config.StepConfig{Agent: agents.RandomAgent}); got != "" {
		t.Errorf("random with no roll = %q, want empty", got)
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
