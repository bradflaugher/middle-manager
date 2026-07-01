package gitops_test

import (
	"io"
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

func TestAppendCloses(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		issueNumber string
		want        string
	}{
		{"numeric appends closes", "PR body.", "42", "PR body.\n\nCloses #42"},
		{"trims trailing newlines first", "PR body.\n\n", "7", "PR body.\n\nCloses #7"},
		{"empty body just closes", "", "13", "Closes #13"},
		{"empty issue is noop", "PR body.", "", "PR body."},
		{"non-numeric issue is noop", "PR body.", "feature/x", "PR body."},
		{"url ref is noop (caller passes numbers)", "PR body.", "#42", "PR body."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := gitops.AppendCloses(tt.body, tt.issueNumber); got != tt.want {
				t.Errorf("AppendCloses(%q, %q) = %q, want %q", tt.body, tt.issueNumber, got, tt.want)
			}
		})
	}
}

func TestRequiredBucketState(t *testing.T) {
	tests := []struct {
		name    string
		buckets []string
		want    string
	}{
		{"no required checks", nil, "none"},
		{"all pass", []string{"pass", "pass"}, "passing"},
		{"pass and skip", []string{"pass", "skipping"}, "passing"},
		{"any pending waits", []string{"pass", "pending"}, "pending"},
		{"any fail short-circuits", []string{"pass", "pending", "fail"}, "failing"},
		{"cancel counts as failing", []string{"cancel"}, "failing"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := gitops.RequiredBucketStateForTest(tt.buckets); got != tt.want {
				t.Errorf("requiredBucketState(%v) = %q, want %q", tt.buckets, got, tt.want)
			}
		})
	}
}

func TestCreatePRDryRunHasBaseAndCloses(t *testing.T) {
	// Dry-run prints the exact gh command it WOULD run. Capture it and assert the
	// invalid --issue flag is gone, --base is passed, and the issue is linked via
	// the body instead.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = w
	_, prErr := gitops.CreatePR("/tmp", "title", "body", "mm/issue-42", "dev", "42", true)
	_ = w.Close()
	os.Stdout = orig
	if prErr != nil {
		t.Fatalf("dry-run CreatePR returned error: %v", prErr)
	}
	out, _ := io.ReadAll(r)
	got := string(out)
	if strings.Contains(got, "--issue") {
		t.Errorf("dry-run command still uses the invalid --issue flag: %s", got)
	}
	if !strings.Contains(got, "--base dev") {
		t.Errorf("dry-run command missing --base dev: %s", got)
	}
	if !strings.Contains(got, "Closes #42") {
		t.Errorf("dry-run command missing 'Closes #42' issue link in body: %s", got)
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

// A leftover issue branch with NO unique commits must fast-forward to the
// moved base on re-ensure, so retries never build on a stale base.
func TestEnsureIssueBranchFastForwardsStaleBranch(t *testing.T) {
	repo := initGitRepo(t)
	base, err := gitops.CurrentBranch(repo)
	if err != nil {
		t.Fatal(err)
	}

	branch, err := gitops.EnsureIssueBranch(repo, "mm", "7", base)
	if err != nil {
		t.Fatal(err)
	}

	runGitRaw(t, repo, "checkout", base)
	writeCommit(t, repo, "advance.txt", "x\n", "advance base")
	newBase, _ := gitops.RevParse(repo, base)

	if _, err := gitops.EnsureIssueBranch(repo, "mm", "7", base); err != nil {
		t.Fatal(err)
	}
	if got, _ := gitops.RevParse(repo, branch); got != newBase {
		t.Fatalf("stale branch not fast-forwarded: %s != %s", got, newBase)
	}
}

// A branch that already holds its own commits is resumable work and must be
// left exactly where it was.
func TestEnsureIssueBranchKeepsBranchWithOwnCommits(t *testing.T) {
	repo := initGitRepo(t)
	base, _ := gitops.CurrentBranch(repo)

	if _, err := gitops.EnsureIssueBranch(repo, "mm", "8", base); err != nil {
		t.Fatal(err)
	}
	writeCommit(t, repo, "work.txt", "wip\n", "issue work")
	workSHA, _ := gitops.RevParse(repo, "mm/issue-8")

	runGitRaw(t, repo, "checkout", base)
	writeCommit(t, repo, "advance.txt", "x\n", "advance base")

	if _, err := gitops.EnsureIssueBranch(repo, "mm", "8", base); err != nil {
		t.Fatal(err)
	}
	if got, _ := gitops.RevParse(repo, "mm/issue-8"); got != workSHA {
		t.Fatalf("branch with its own commits was reset: %s != %s", got, workSHA)
	}
}

// Never hard-reset under uncommitted work: EnsureIssueBranch is also called
// right before the commit step, when the agent's changes are still uncommitted.
func TestEnsureIssueBranchNoResetUnderDirtyTree(t *testing.T) {
	repo := initGitRepo(t)
	base, _ := gitops.CurrentBranch(repo)

	if _, err := gitops.EnsureIssueBranch(repo, "mm", "9", base); err != nil {
		t.Fatal(err)
	}
	staleSHA, _ := gitops.RevParse(repo, "mm/issue-9")

	runGitRaw(t, repo, "checkout", base)
	writeCommit(t, repo, "advance.txt", "x\n", "advance base")
	runGitRaw(t, repo, "checkout", "mm/issue-9")

	dirty := filepath.Join(repo, "uncommitted.txt")
	if err := os.WriteFile(dirty, []byte("agent work in flight"), 0644); err != nil {
		t.Fatal(err)
	}

	if _, err := gitops.EnsureIssueBranch(repo, "mm", "9", base); err != nil {
		t.Fatal(err)
	}
	if got, _ := gitops.RevParse(repo, "mm/issue-9"); got != staleSHA {
		t.Fatal("branch was reset despite a dirty working tree")
	}
	if _, err := os.Stat(dirty); err != nil {
		t.Fatal("uncommitted work was destroyed")
	}
}

// A box with no git identity must still be able to commit verified work — the
// commit falls back to a synthetic identity instead of dropping the iteration.
func TestCommitAllFallsBackWhenIdentityMissing(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	repo := initGitRepo(t)
	runGitRaw(t, repo, "config", "--unset", "user.email")
	runGitRaw(t, repo, "config", "--unset", "user.name")

	if err := os.WriteFile(filepath.Join(repo, "f.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	committed, err := gitops.CommitAllWithError(repo, "identity fallback test")
	if err != nil || !committed {
		t.Fatalf("commit without identity failed: committed=%v err=%v", committed, err)
	}
}

// DiffSummary must surface untracked files (which `git diff` misses) so the
// verifier sees the full change surface.
func TestDiffSummaryIncludesUntracked(t *testing.T) {
	repo := initGitRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "brand-new.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	sum := gitops.DiffSummary(repo)
	if !strings.Contains(sum, "brand-new.txt") {
		t.Fatalf("untracked file missing from diff summary: %q", sum)
	}
}

// Regression: the FIRST porcelain line of an " M file" entry must keep its
// path intact. Parsers that fed RunGit's TrimSpace'd output through fixed
// offsets read "GENTS.md" out of " M AGENTS.md" whenever it came first.
func TestStatusEntriesFirstLineNotMangled(t *testing.T) {
	repo := initGitRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("v1"), 0644); err != nil {
		t.Fatal(err)
	}
	runGitRaw(t, repo, "add", "-A")
	runGitRaw(t, repo, "commit", "-m", "init")
	if err := os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("v2"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "new.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	entries := gitops.StatusEntries(repo)
	if len(entries) != 2 {
		t.Fatalf("entries = %+v, want 2", entries)
	}
	got := map[string]string{}
	for _, e := range entries {
		got[e.Path] = e.Status
	}
	if got["AGENTS.md"] != "M" {
		t.Errorf("modified first entry mangled: %+v", entries)
	}
	if got["new.txt"] != "??" {
		t.Errorf("untracked entry wrong: %+v", entries)
	}
}
