package loop

import (
	"strings"
	"testing"
)

func TestParseSeedIssues(t *testing.T) {
	out := `
I scanned the repo. Here are my proposals:

===ISSUE===
TITLE: Fix nil deref in queue drain
PRIORITY: P1
SIZE: S
BODY:
**Context:** crash when the queue is empty.

**Acceptance criteria:**
- [ ] go test ./... passes
===END===

some commentary the parser must ignore

===ISSUE===
TITLE: Add coverage for the merge path
PRIORITY: p2 (meaningful improvement)
SIZE: xl — should probably be split
BODY:
Body text here.
===END===

===ISSUE===
TITLE: Malformed — no body follows
===END===

===ISSUE===
TITLE: No priority or size given
BODY:
Still valid; tags are optional.
===END===

## Summary
Filed 3, skipped 1 duplicate. (Parser must not treat this as an issue.)
`
	issues := ParseSeedIssues(out)
	if len(issues) != 3 {
		t.Fatalf("parsed %d issues, want 3: %+v", len(issues), issues)
	}
	if issues[0].Title != "Fix nil deref in queue drain" || issues[0].Priority != "P1" || issues[0].Size != "S" {
		t.Errorf("issue 0 = %+v", issues[0])
	}
	if !strings.Contains(issues[0].Body, "Acceptance criteria") {
		t.Errorf("issue 0 body lost content: %q", issues[0].Body)
	}
	// Lowercase priority normalizes; a size token with trailing prose still parses.
	if issues[1].Priority != "P2" || issues[1].Size != "XL" {
		t.Errorf("issue 1 tags = %q/%q, want P2/XL", issues[1].Priority, issues[1].Size)
	}
	// Missing tags degrade to empty, never invented.
	if issues[2].Priority != "" || issues[2].Size != "" {
		t.Errorf("issue 2 tags = %q/%q, want empty", issues[2].Priority, issues[2].Size)
	}

	if got := ParseSeedIssues("no blocks at all"); got != nil {
		t.Errorf("garbage input produced issues: %+v", got)
	}
}
