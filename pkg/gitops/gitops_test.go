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
