package gitops_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bradflaugher/middle-manager/pkg/gitops"
)

// writeCommit writes content to <dir>/name and commits it in that worktree.
func writeCommit(t *testing.T, dir, name, content, msg string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	runGitRaw(t, dir, "add", "-A")
	runGitRaw(t, dir, "commit", "-m", msg)
}

// TestWorktreeCleanCollapse exercises the happy path of issue #1's collapse:
// two issue branches that touch DIFFERENT files merge cleanly into one
// integration branch, and the result contains both changes.
func TestWorktreeCleanCollapse(t *testing.T) {
	repo := initGitRepo(t)
	base, err := gitops.CurrentBranch(repo)
	if err != nil {
		t.Fatal(err)
	}
	baseSHA, err := gitops.RevParse(repo, base)
	if err != nil || baseSHA == "" {
		t.Fatalf("RevParse base: %v", err)
	}

	root := t.TempDir()
	wt1 := filepath.Join(root, "issue-1")
	wt2 := filepath.Join(root, "issue-2")
	intp := filepath.Join(root, "integration")

	if err := gitops.WorktreeAddBranch(repo, wt1, "mm/batch/issue-1", baseSHA); err != nil {
		t.Fatalf("worktree 1: %v", err)
	}
	writeCommit(t, wt1, "a.txt", "from issue 1\n", "issue 1 work")

	if err := gitops.WorktreeAddBranch(repo, wt2, "mm/batch/issue-2", baseSHA); err != nil {
		t.Fatalf("worktree 2: %v", err)
	}
	writeCommit(t, wt2, "b.txt", "from issue 2\n", "issue 2 work")

	if err := gitops.WorktreeAddBranch(repo, intp, "mm/mega", baseSHA); err != nil {
		t.Fatalf("integration worktree: %v", err)
	}

	for _, br := range []string{"mm/batch/issue-1", "mm/batch/issue-2"} {
		conflicted, upToDate, err := gitops.MergeNoCommit(intp, br)
		if err != nil {
			t.Fatalf("merge %s: %v", br, err)
		}
		if conflicted {
			t.Fatalf("merge %s unexpectedly conflicted", br)
		}
		if upToDate {
			t.Fatalf("merge %s reported up-to-date", br)
		}
		if err := gitops.CommitMerge(intp, "merge "+br); err != nil {
			t.Fatalf("commit merge %s: %v", br, err)
		}
	}

	// Both files must be present in the integration worktree.
	for _, f := range []string{"a.txt", "b.txt"} {
		if _, err := os.Stat(filepath.Join(intp, f)); err != nil {
			t.Errorf("integration missing %s: %v", f, err)
		}
	}

	// WorktreeRemove must cleanly tear a worktree down (branch is preserved).
	if err := gitops.WorktreeRemove(repo, wt1); err != nil {
		t.Errorf("WorktreeRemove: %v", err)
	}
	if !gitops.RefExists(repo, "mm/batch/issue-1") {
		t.Error("removing a worktree must NOT delete its branch")
	}
}

// TestWorktreeConflictAbortRollsBack proves a conflicting branch can be cleanly
// dropped: AbortMergeAndReset restores the integration branch to exactly its
// pre-merge state, leaving no unmerged paths behind.
func TestWorktreeConflictAbortRollsBack(t *testing.T) {
	repo := initGitRepo(t)
	base, _ := gitops.CurrentBranch(repo)
	baseSHA, _ := gitops.RevParse(repo, base)

	root := t.TempDir()
	wt1 := filepath.Join(root, "i1")
	wt2 := filepath.Join(root, "i2")
	intp := filepath.Join(root, "int")

	// Both branches edit the SAME file differently → guaranteed conflict.
	if err := gitops.WorktreeAddBranch(repo, wt1, "b/1", baseSHA); err != nil {
		t.Fatal(err)
	}
	writeCommit(t, wt1, "README.md", "ONE\n", "one")
	if err := gitops.WorktreeAddBranch(repo, wt2, "b/2", baseSHA); err != nil {
		t.Fatal(err)
	}
	writeCommit(t, wt2, "README.md", "TWO\n", "two")

	if err := gitops.WorktreeAddBranch(repo, intp, "b/mega", baseSHA); err != nil {
		t.Fatal(err)
	}

	// First branch merges cleanly.
	if _, _, err := gitops.MergeNoCommit(intp, "b/1"); err != nil {
		t.Fatalf("merge b/1: %v", err)
	}
	if err := gitops.CommitMerge(intp, "merge b/1"); err != nil {
		t.Fatalf("commit b/1: %v", err)
	}
	afterFirst, _ := gitops.RevParse(intp, "HEAD")

	// Second branch conflicts.
	conflicted, _, err := gitops.MergeNoCommit(intp, "b/2")
	if err != nil {
		t.Fatalf("merge b/2: %v", err)
	}
	if !conflicted {
		t.Fatal("expected a conflict merging b/2 over b/1")
	}
	if len(gitops.UnmergedPaths(intp)) == 0 {
		t.Fatal("expected unmerged paths during the conflict")
	}

	// Drop it: abort + reset to the pre-merge SHA.
	gitops.AbortMergeAndReset(intp, afterFirst)
	if got := gitops.UnmergedPaths(intp); len(got) != 0 {
		t.Fatalf("unmerged paths remain after abort: %v", got)
	}
	if head, _ := gitops.RevParse(intp, "HEAD"); head != afterFirst {
		t.Fatalf("HEAD = %s after abort, want %s", head, afterFirst)
	}
	if gitops.HasChanges(intp) {
		t.Fatal("integration tree must be clean after abort")
	}
}

// TestMergeInProgressTransitions backs the collapse fix for "the agent committed
// the merge itself": MergeInProgress is true during a conflict and false once the
// merge is committed — so collapse can detect an agent-committed merge (no
// MERGE_HEAD, clean tree, HEAD advanced) instead of resetting and losing it.
func TestMergeInProgressTransitions(t *testing.T) {
	repo := initGitRepo(t)
	base, _ := gitops.CurrentBranch(repo)
	baseSHA, _ := gitops.RevParse(repo, base)

	root := t.TempDir()
	wt1 := filepath.Join(root, "i1")
	wt2 := filepath.Join(root, "i2")
	intp := filepath.Join(root, "int")
	if err := gitops.WorktreeAddBranch(repo, wt1, "c/1", baseSHA); err != nil {
		t.Fatal(err)
	}
	writeCommit(t, wt1, "README.md", "ALPHA\n", "alpha")
	if err := gitops.WorktreeAddBranch(repo, wt2, "c/2", baseSHA); err != nil {
		t.Fatal(err)
	}
	writeCommit(t, wt2, "README.md", "BETA\n", "beta")
	if err := gitops.WorktreeAddBranch(repo, intp, "c/mega", baseSHA); err != nil {
		t.Fatal(err)
	}

	if _, _, err := gitops.MergeNoCommit(intp, "c/1"); err != nil {
		t.Fatal(err)
	}
	if err := gitops.CommitMerge(intp, "merge c/1"); err != nil {
		t.Fatal(err)
	}
	prev, _ := gitops.RevParse(intp, "HEAD")

	// Conflict on c/2 → merge is in progress.
	conflicted, _, _ := gitops.MergeNoCommit(intp, "c/2")
	if !conflicted {
		t.Fatal("expected conflict")
	}
	if !gitops.MergeInProgress(intp) {
		t.Fatal("MergeInProgress should be true mid-conflict")
	}

	// Simulate an agent that resolves AND commits the merge itself.
	if err := os.WriteFile(filepath.Join(intp, "README.md"), []byte("ALPHA+BETA\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runGitRaw(t, intp, "add", "-A")
	runGitRaw(t, intp, "commit", "--no-edit", "-m", "agent resolved")

	// Collapse's acceptance condition: no merge in progress, clean tree, HEAD moved.
	if gitops.MergeInProgress(intp) {
		t.Fatal("MergeInProgress should be false after the agent committed")
	}
	if gitops.HasChanges(intp) {
		t.Fatal("tree should be clean after the agent committed")
	}
	if head, _ := gitops.RevParse(intp, "HEAD"); head == prev {
		t.Fatal("HEAD should have advanced past the pre-merge SHA")
	}
}

// TestMergeNoCommitUpToDate: merging an ancestor branch is a no-op the collapse
// loop must recognize (so it counts the issue as included without a commit).
func TestMergeNoCommitUpToDate(t *testing.T) {
	repo := initGitRepo(t)
	base, _ := gitops.CurrentBranch(repo)
	baseSHA, _ := gitops.RevParse(repo, base)

	root := t.TempDir()
	intp := filepath.Join(root, "int")
	if err := gitops.WorktreeAddBranch(repo, intp, "b/mega2", baseSHA); err != nil {
		t.Fatal(err)
	}
	// Merging the base (already an ancestor) must report up-to-date, not conflict.
	conflicted, upToDate, err := gitops.MergeNoCommit(intp, baseSHA)
	if err != nil {
		t.Fatalf("merge base: %v", err)
	}
	if conflicted {
		t.Fatal("merging an ancestor must not conflict")
	}
	if !upToDate {
		t.Fatal("merging an ancestor must report up-to-date")
	}
}
