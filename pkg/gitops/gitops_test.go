package gitops_test

import (
	"testing"

	"github.com/bradflaugher/middle-manager/pkg/gitops"
)

func TestPlanIsComplete(t *testing.T) {
	tests := []struct {
		name     string
		plan     string
		expected bool
	}{
		{
			name:     "empty plan",
			plan:     "",
			expected: false,
		},
		{
			name:     "spaces only",
			plan:     "   \n  ",
			expected: false,
		},
		{
			name: "plan with pending tasks",
			plan: `# fix_plan.md
- [x] done task
- [ ] pending task
- [ ] another pending`,
			expected: false,
		},
		{
			name: "plan with all tasks done",
			plan: `# fix_plan.md
- [x] done task
- [x] another done task`,
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := gitops.PlanIsComplete(tt.plan)
			if actual != tt.expected {
				t.Errorf("expected %t, got %t", tt.expected, actual)
			}
		})
	}
}

func TestIsSafeToMerge(t *testing.T) {
	tests := []struct {
		name          string
		pr            gitops.PullRequest
		requireChecks bool
		wantSafe      bool
	}{
		{"clean approved", gitops.PullRequest{Mergeable: "MERGEABLE", MergeState: "CLEAN", ReviewDecision: "APPROVED", ChecksState: "passing"}, true, true},
		{"clean no checks", gitops.PullRequest{Mergeable: "MERGEABLE", MergeState: "CLEAN", ChecksState: "none"}, true, true},
		{"draft", gitops.PullRequest{IsDraft: true, MergeState: "CLEAN"}, true, false},
		{"conflicts", gitops.PullRequest{Mergeable: "CONFLICTING", MergeState: "DIRTY"}, true, false},
		{"changes requested", gitops.PullRequest{Mergeable: "MERGEABLE", MergeState: "BLOCKED", ReviewDecision: "CHANGES_REQUESTED"}, true, false},
		{"failing checks", gitops.PullRequest{Mergeable: "MERGEABLE", MergeState: "UNSTABLE", ChecksState: "failing"}, true, false},
		{"pending checks required", gitops.PullRequest{Mergeable: "MERGEABLE", MergeState: "UNSTABLE", ChecksState: "pending"}, true, false},
		{"pending checks not required", gitops.PullRequest{Mergeable: "MERGEABLE", MergeState: "CLEAN", ChecksState: "pending"}, false, true},
		{"behind base", gitops.PullRequest{Mergeable: "MERGEABLE", MergeState: "BEHIND", ChecksState: "passing"}, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, reason := tt.pr.IsSafeToMerge(tt.requireChecks)
			if got != tt.wantSafe {
				t.Errorf("IsSafeToMerge() = %v (%s), want %v", got, reason, tt.wantSafe)
			}
		})
	}
}
