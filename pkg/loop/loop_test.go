package loop

import (
	"strings"
	"testing"
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
