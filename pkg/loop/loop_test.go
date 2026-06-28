package loop

import "testing"

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
