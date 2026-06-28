package gitops_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

func TestRunGitErrorIncludesStderr(t *testing.T) {
	repo := initGitRepo(t)

	_, _, code, err := gitops.RunGit(repo, "checkout", "does-not-exist")
	if err == nil {
		t.Fatal("expected checkout to fail")
	}
	if code == 0 {
		t.Fatal("expected non-zero exit code")
	}
	msg := err.Error()
	if !strings.Contains(msg, "git checkout does-not-exist failed") {
		t.Fatalf("error %q did not include the git command", msg)
	}
	if !strings.Contains(msg, "does-not-exist") {
		t.Fatalf("error %q did not include stderr detail", msg)
	}
}

func TestRepoIsGitAcceptsWorktreeGitFile(t *testing.T) {
	repo := initGitRepo(t)
	worktree := filepath.Join(t.TempDir(), "worktree")

	runGitRaw(t, repo, "worktree", "add", "--detach", worktree, "HEAD")

	if !gitops.RepoIsGit(worktree) {
		t.Fatal("worktree with .git file should be treated as a git repo")
	}
}

func TestEnsureBranchInvalidBaseExplainsCause(t *testing.T) {
	repo := initGitRepo(t)

	_, err := gitops.EnsureBranch(repo, "middle-manager", 1, "missing-base")
	if err == nil {
		t.Fatal("expected invalid base branch to fail")
	}
	if !strings.Contains(err.Error(), `base branch "missing-base" was not found`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPushBranchNoOriginReturnsError(t *testing.T) {
	repo := initGitRepo(t)
	branch, err := gitops.CurrentBranch(repo)
	if err != nil {
		t.Fatal(err)
	}

	err = gitops.PushBranch(repo, branch, false)
	if err == nil {
		t.Fatal("expected push without origin remote to fail")
	}
	if !strings.Contains(err.Error(), "no 'origin' remote configured") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func initGitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo := t.TempDir()
	runGitRaw(t, repo, "init")
	runGitRaw(t, repo, "config", "user.email", "test@example.com")
	runGitRaw(t, repo, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("test\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runGitRaw(t, repo, "add", "README.md")
	runGitRaw(t, repo, "commit", "-m", "initial commit")
	return repo
}

func runGitRaw(t *testing.T, repo string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = repo
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, string(out))
	}
}
