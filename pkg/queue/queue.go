package queue

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/bradflaugher/middle-manager/pkg/agents"
	"github.com/bradflaugher/middle-manager/pkg/colors"
	"github.com/bradflaugher/middle-manager/pkg/config"
	"github.com/bradflaugher/middle-manager/pkg/gitops"
	"github.com/bradflaugher/middle-manager/pkg/loop"
	"github.com/bradflaugher/middle-manager/pkg/prompts"
	"github.com/bradflaugher/middle-manager/pkg/tui"
)

type IssueQueueRunner struct {
	cfg     *config.LoopConfig
	logPath string
	// baseStateDir is the top-level state dir captured once, before any per-issue
	// override. ResetIssueState derives each issue dir from this so they sit side
	// by side (issues/1, issues/2, …) instead of nesting under one another.
	baseStateDir string

	// ctx/cancel let Cancel() also abort a long-running collapse agent (worktree
	// mode), which runs outside any per-issue loop.
	ctx    context.Context
	cancel context.CancelFunc

	// mu guards current/canceled, which the monitor goroutine touches via Cancel
	// while Run() is mid-drain.
	mu       sync.Mutex
	current  *loop.MiddleManagerLoop // the in-flight issue's loop, for cancellation
	canceled bool                    // set by Cancel(); stops the drain advancing
}

func NewIssueQueueRunner(cfg *config.LoopConfig) (*IssueQueueRunner, error) {
	if cfg.IssueQueue == nil {
		return nil, fmt.Errorf("issue_queue config required")
	}
	baseStateDir := cfg.StatePath()
	logPath := filepath.Join(baseStateDir, "queue.log")
	ctx, cancel := context.WithCancel(context.Background())
	return &IssueQueueRunner{
		cfg:          cfg,
		logPath:      logPath,
		baseStateDir: baseStateDir,
		ctx:          ctx,
		cancel:       cancel,
	}, nil
}

// Cancel stops the drain: it aborts the in-flight issue's loop (terminating any
// running agent) and flags the runner so it won't advance to the next issue.
// Called from the monitor goroutine when the operator hits /quit or Ctrl+C.
func (r *IssueQueueRunner) Cancel() {
	r.mu.Lock()
	r.canceled = true
	cur := r.current
	r.mu.Unlock()
	if r.cancel != nil {
		r.cancel()
	}
	if cur != nil {
		cur.Cancel()
	}
}

func (r *IssueQueueRunner) Log(msg string, colorCode string) {
	timestamp := time.Now().Format("2006-01-02 15:04:05")
	rawLine := fmt.Sprintf("[%s] %s", timestamp, msg)

	display := rawLine
	if colorCode != "" {
		display = colors.Colored(rawLine, colorCode)
	}
	// When the monitor TUI owns the terminal, route through it — a raw
	// fmt.Println would punch through and corrupt the alt-screen. Without a
	// live monitor (stream/dry-run, or no TUI), print to stdout as before.
	if tui.GlobalProgram != nil {
		tui.NotifyTUIUpdate(display+"\n", false)
	} else {
		fmt.Println(display)
	}

	// Strip ANSI escape codes
	re := regexp.MustCompile(`\x1B(?:[@-Z\\-_]|\[[0-?]*[ -/]*[@-~])`)
	cleanLine := re.ReplaceAllString(rawLine, "") + "\n"

	_ = os.MkdirAll(filepath.Dir(r.logPath), 0755)
	f, err := os.OpenFile(r.logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err == nil {
		defer f.Close()
		_, _ = f.WriteString(cleanLine)
	}
}

func (r *IssueQueueRunner) ResetIssueState(issue map[string]string) {
	number := issue["number"]
	issueDir := filepath.Join(r.baseStateDir, "issues", number)
	_ = os.MkdirAll(issueDir, 0755)

	// Per-issue overrides on the shared config. Mission is cleared so the loop
	// re-derives an effective mission from THIS issue's title — otherwise issue N
	// would inherit issue N-1's derived mission.
	r.cfg.StateDir = issueDir
	r.cfg.Issue = number
	r.cfg.Mission = ""
}

func (r *IssueQueueRunner) Run() int {
	// Refuse to start on a dirty tree — the per-issue base checkout/reset would
	// otherwise clobber the operator's uncommitted work. (Once draining, a dirty
	// tree can only be mm's own leftovers from a failed issue, which we discard.)
	if gitops.RepoIsGit(r.cfg.Repo) && !r.cfg.DryRun && gitops.HasChanges(r.cfg.Repo) {
		r.Log(fmt.Sprintf("Working tree at %s has uncommitted changes — commit or stash them before draining a queue.", r.cfg.Repo), colors.Red)
		return 1
	}

	issues, err := gitops.ListIssues(r.cfg.Repo, r.cfg.IssueQueue.Label, r.cfg.IssueQueue.Author, r.cfg.IssueQueue.Limit, r.cfg.IssueQueue.State)
	if err != nil {
		r.Log(fmt.Sprintf("Could not list issues: %v", err), colors.Red)
		return 1
	}
	if len(issues) == 0 {
		r.Log("No matching issues in queue.", colors.Yellow)
		return 0
	}

	r.Log(fmt.Sprintf("Queue: %d issue(s) — label=%q author=%q", len(issues), r.cfg.IssueQueue.Label, r.cfg.IssueQueue.Author), colors.Cyan)

	// Worktree-collapse strategy (issue #1) develops each issue in isolation and
	// ships one consolidated PR instead of N possibly-conflicting ones.
	if r.cfg.Worktree {
		return r.drainWorktree(issues)
	}

	succeeded := 0
	failed := 0

	for idx, issue := range issues {
		// Bail before starting another issue if the operator quit the monitor
		// between issues (an in-flight quit is caught by the loop's own ctx).
		r.mu.Lock()
		canceled := r.canceled
		r.mu.Unlock()
		if canceled {
			break
		}

		number := issue["number"]
		r.Log(fmt.Sprintf("=== Queue %d/%d: Issue #%s — %s ===", idx+1, len(issues), number, issue["title"]), colors.Cyan+colors.Bold)
		tui.NotifyTUIQueue(idx+1, len(issues), fmt.Sprintf("#%s %s", number, issue["title"]))

		if gitops.RepoIsGit(r.cfg.Repo) && !r.cfg.DryRun {
			baseBranch := r.cfg.BaseBranch
			if baseBranch == "" {
				baseBranch = gitops.DetectBaseBranch(r.cfg.Repo)
			}
			// A prior issue that failed mid-loop can leave the tree dirty; those
			// edits would otherwise ride onto the next issue's branch and PR. This
			// repo is mm-managed, so discard them before switching base.
			if gitops.HasChanges(r.cfg.Repo) {
				r.Log("Discarding uncommitted leftovers from the previous task...", colors.Yellow)
				_, _, _, _ = gitops.RunGit(r.cfg.Repo, "reset", "--hard")
				_, _, _, _ = gitops.RunGit(r.cfg.Repo, "clean", "-fd")
			}
			r.Log(fmt.Sprintf("Checking out and pulling latest from base branch %q...", baseBranch), colors.Cyan)
			_, _, codeCheckout, _ := gitops.RunGit(r.cfg.Repo, "checkout", baseBranch)
			if codeCheckout != 0 {
				r.Log(fmt.Sprintf("⚠️ Failed to checkout branch %q", baseBranch), colors.Yellow)
			} else {
				_, _, codePull, _ := gitops.RunGit(r.cfg.Repo, "pull", "origin", baseBranch)
				if codePull != 0 {
					r.Log("⚠️ Failed to pull from origin", colors.Yellow)
				}
			}
		}

		r.ResetIssueState(issue)
		l := loop.NewMiddleManagerLoop(r.cfg)
		l.SetPrefetchedIssue(issue) // title/body already fetched; avoids a re-fetch failure window

		r.mu.Lock()
		r.current = l
		r.mu.Unlock()

		result, err := l.RunUntilComplete()

		// Cancel this issue's context so its background resource-tracking
		// goroutine stops; otherwise every finished issue keeps pushing stale
		// "running" status into the shared dashboard, fighting the live one.
		l.Cancel()
		r.mu.Lock()
		r.current = nil
		r.mu.Unlock()

		if err == nil && result.Success {
			succeeded++
			r.Log(fmt.Sprintf("Issue #%s done.", number), colors.Green)
			if r.cfg.IssueQueue.CloseOnSuccess {
				comment := r.cfg.IssueQueue.CloseComment
				if comment == "" {
					if result.PRURL != "" {
						comment = "Closed by middle-manager — fix verified and PR opened."
					} else {
						comment = "Closed by middle-manager — fix verified and pushed to a branch (no PR opened)."
					}
				}
				if result.PRURL != "" {
					comment = fmt.Sprintf("%s\n\nPR: %s", comment, result.PRURL)
				}
				gitops.CloseIssue(r.cfg.Repo, number, comment, r.cfg.DryRun)
			}
		} else {
			failed++
			reason := "unknown error"
			if err != nil {
				reason = err.Error()
			} else if result != nil {
				reason = result.Reason
			}
			r.Log(fmt.Sprintf("Issue #%s incomplete: %s", number, reason), colors.Red)
			if reason == "Stopped by user" {
				r.Log("Queue execution stopped by user request.", colors.Yellow)
				break
			}
			// Solo/wait-for-merge serializes the queue precisely so issues never
			// conflict. If this issue opened a PR that did NOT land, continuing
			// would branch the next issue off a base missing this PR — the very
			// conflict we're avoiding. Stop the drain instead of pressing on.
			if (r.cfg.IsSolo() || r.cfg.WaitForMerge) && result != nil && result.PRURL != "" {
				r.Log("Stopping drain: a PR was opened but did not merge. Resolve it, then re-run to continue.", colors.Yellow)
				break
			}
		}
	}

	r.Log(fmt.Sprintf("Queue finished: %d succeeded, %d incomplete.", succeeded, failed), colors.Green)
	if failed > 0 {
		return 1
	}
	return 0
}

// builtIssue records an issue whose per-issue loop succeeded in its worktree, so
// the collapse phase knows which branches to merge into the mega PR.
type builtIssue struct {
	number string
	title  string
	branch string
}

// drainWorktree runs the worktree-collapse strategy: freeze the base, develop
// each issue in its own worktree on its own branch (no per-issue PR), then merge
// the successful branches into one integration branch and open a single PR that
// closes every included issue. mm owns every commit; an agent only resolves
// merge conflicts.
func (r *IssueQueueRunner) drainWorktree(issues []map[string]string) int {
	repo := r.cfg.Repo
	if !gitops.RepoIsGit(repo) {
		r.Log("Worktree mode requires a git repository.", colors.Red)
		return 1
	}

	baseBranch := r.cfg.BaseBranch
	if baseBranch == "" {
		baseBranch = gitops.DetectBaseBranch(repo)
	}
	// Bring the base current, then FREEZE it: every issue branches off the same
	// commit so the later collapse is predictable.
	_, _, _, _ = gitops.RunGit(repo, "checkout", baseBranch)
	_, _, _, _ = gitops.RunGit(repo, "pull", "origin", baseBranch)
	baseSHA, err := gitops.RevParse(repo, baseBranch)
	if err != nil || baseSHA == "" {
		// The base may exist only as a remote-tracking ref (fresh clone, or an
		// ambiguous short name with multiple remotes) — fall back to origin/<base>
		// just like the non-worktree path's ensureBranch does.
		baseSHA, err = gitops.RevParse(repo, "origin/"+baseBranch)
	}
	if err != nil || baseSHA == "" {
		r.Log(fmt.Sprintf("Could not resolve base branch %q: %v", baseBranch, err), colors.Red)
		return 1
	}

	ts := time.Now().Format("20060102-150405")
	batchPrefix := "mm/batch-" + ts      // per-issue branches: mm/batch-<ts>/issue-N
	integrationBranch := "mm/mega-" + ts // integration branch (distinct name: no D/F conflict with the per-issue dir)
	worktreeRoot := filepath.Join(r.baseStateDir, "worktrees")
	// Clear any stale worktrees from a prior crashed run before we start.
	gitops.WorktreePrune(repo)
	_ = os.RemoveAll(worktreeRoot)
	_ = os.MkdirAll(worktreeRoot, 0755)
	gitops.WorktreePrune(repo)

	r.Log(fmt.Sprintf("🌳 Worktree mode: %d issue(s); base %q frozen at %s; batch %s", len(issues), baseBranch, shortSHA(baseSHA), ts), colors.Cyan+colors.Bold)

	var built []builtIssue
	for idx, issue := range issues {
		r.mu.Lock()
		canceled := r.canceled
		r.mu.Unlock()
		if canceled {
			break
		}

		number := issue["number"]
		branch := fmt.Sprintf("%s/issue-%s", batchPrefix, number)
		wtPath := filepath.Join(worktreeRoot, "issue-"+number)
		tui.NotifyTUIQueue(idx+1, len(issues), fmt.Sprintf("#%s %s", number, issue["title"]))
		r.Log(fmt.Sprintf("=== Worktree %d/%d: Issue #%s — %s ===", idx+1, len(issues), number, issue["title"]), colors.Cyan+colors.Bold)

		if err := gitops.WorktreeAddBranch(repo, wtPath, branch, baseSHA); err != nil {
			r.Log(fmt.Sprintf("⚠️ Could not create worktree for #%s: %v — skipping", number, err), colors.Yellow)
			continue
		}

		// Per-issue loop: operate INSIDE the worktree, on its pre-created branch,
		// with no per-issue PR (collapse opens the single PR). Fresh=false so the
		// loop doesn't reset/checkout base inside the worktree and lose the branch.
		issueCfg := *r.cfg
		issueCfg.Repo = wtPath
		issueCfg.Worktree = false
		issueCfg.Solo = false
		issueCfg.WaitForMerge = false
		issueCfg.NoPR = true
		issueCfg.Fresh = false
		issueCfg.Mode = "issue"
		issueCfg.Issue = number
		issueCfg.Mission = ""
		issueCfg.BranchPrefix = batchPrefix
		issueCfg.BaseBranch = baseSHA
		issueDir := filepath.Join(r.baseStateDir, "issues", number)
		_ = os.MkdirAll(issueDir, 0755)
		issueCfg.StateDir = issueDir

		l := loop.NewMiddleManagerLoop(&issueCfg)
		l.SetPrefetchedIssue(issue)
		r.mu.Lock()
		r.current = l
		r.mu.Unlock()

		result, lerr := l.RunUntilComplete()
		l.Cancel()
		r.mu.Lock()
		r.current = nil
		r.mu.Unlock()

		if lerr == nil && result != nil && result.Success {
			// Guard against a "successful" issue that committed nothing: its branch
			// still equals the frozen base, so merging it would contribute no code
			// yet still emit a "Closes #N" that closes the issue with no fix behind
			// it. Exclude such empty branches from the batch.
			if branchSHA, _ := gitops.RevParse(repo, branch); branchSHA == baseSHA {
				r.Log(fmt.Sprintf("Issue #%s produced no commit — excluded from the mega PR.", number), colors.Yellow)
			} else {
				r.Log(fmt.Sprintf("Issue #%s done in its worktree (branch %s).", number, branch), colors.Green)
				built = append(built, builtIssue{number: number, title: issue["title"], branch: branch})
			}
		} else {
			reason := "unknown error"
			if lerr != nil {
				reason = lerr.Error()
			} else if result != nil {
				reason = result.Reason
			}
			r.Log(fmt.Sprintf("Issue #%s incomplete: %s — excluded from the mega PR.", number, reason), colors.Red)
			if reason == "Stopped by user" {
				r.Log("Queue execution stopped by user request.", colors.Yellow)
				break
			}
		}
	}

	r.mu.Lock()
	canceled := r.canceled
	r.mu.Unlock()
	if canceled {
		r.Log("Canceled before collapse — leaving worktrees in place for inspection.", colors.Yellow)
		return 1
	}

	if len(built) == 0 {
		r.Log("No issues completed — nothing to collapse.", colors.Yellow)
		r.cleanupWorktrees(repo, worktreeRoot)
		return 1
	}

	code := r.collapse(repo, baseSHA, baseBranch, integrationBranch, worktreeRoot, built)
	r.cleanupWorktrees(repo, worktreeRoot)
	return code
}

// collapse merges every successfully-built issue branch into one integration
// branch and opens a single PR closing the issues that actually landed. Each
// merge is fail-closed: a conflict an agent can't cleanly resolve, or one that
// leaves markers/unmerged paths, is rolled back and the issue dropped.
func (r *IssueQueueRunner) collapse(repo, baseSHA, baseBranch, integrationBranch, worktreeRoot string, built []builtIssue) int {
	intPath := filepath.Join(worktreeRoot, "integration")
	if err := gitops.WorktreeAddBranch(repo, intPath, integrationBranch, baseSHA); err != nil {
		r.Log(fmt.Sprintf("Could not create integration worktree: %v", err), colors.Red)
		return 1
	}
	r.Log(fmt.Sprintf("🧬 Collapsing %d branch(es) into %s ...", len(built), integrationBranch), colors.Cyan+colors.Bold)

	var merged []builtIssue
	for _, b := range built {
		r.mu.Lock()
		canceled := r.canceled
		r.mu.Unlock()
		if canceled {
			break
		}

		prev, _ := gitops.RevParse(intPath, "HEAD")
		conflicted, upToDate, mErr := gitops.MergeNoCommit(intPath, b.branch)
		if mErr != nil {
			r.Log(fmt.Sprintf("⚠️ merge of #%s errored: %v — dropping.", b.number, mErr), colors.Yellow)
			gitops.AbortMergeAndReset(intPath, prev)
			continue
		}
		if upToDate {
			r.Log(fmt.Sprintf("Issue #%s already present in integration — counted as included.", b.number), colors.Dim)
			merged = append(merged, b)
			continue
		}
		if conflicted {
			files := gitops.UnmergedPaths(intPath)
			r.Log(fmt.Sprintf("Merge conflict on #%s in %d file(s) — invoking agent to resolve...", b.number, len(files)), colors.Yellow)
			if !r.resolveConflictWithAgent(intPath, b.branch, files) {
				r.Log(fmt.Sprintf("Agent could not resolve #%s — dropping from the mega PR.", b.number), colors.Yellow)
				gitops.AbortMergeAndReset(intPath, prev)
				continue
			}
			// mm validates the agent's resolution before trusting it.
			if len(gitops.UnmergedPaths(intPath)) > 0 || gitops.StagedHasConflictMarkers(intPath) {
				r.Log(fmt.Sprintf("Unresolved conflicts remain on #%s after the agent — dropping.", b.number), colors.Red)
				gitops.AbortMergeAndReset(intPath, prev)
				continue
			}
		}
		// If the conflict-resolution agent committed the merge itself (despite the
		// prompt asking it not to), MERGE_HEAD is already consumed and the tree is
		// clean — CommitMerge would fail "nothing to commit". Accept the agent's
		// commit (as long as it actually advanced HEAD) instead of resetting and
		// discarding correctly-resolved work.
		if !gitops.MergeInProgress(intPath) && !gitops.HasChanges(intPath) {
			if head, _ := gitops.RevParse(intPath, "HEAD"); head == prev {
				r.Log(fmt.Sprintf("Merge of #%s produced no commit (agent aborted/did nothing) — dropping.", b.number), colors.Yellow)
				gitops.AbortMergeAndReset(intPath, prev)
				continue
			}
			r.Log(fmt.Sprintf("✓ Merged #%s into %s (agent committed the resolution).", b.number, integrationBranch), colors.Green)
			merged = append(merged, b)
			continue
		}
		msg := fmt.Sprintf("Merge issue #%s: %s", b.number, b.title)
		if len(msg) > 72 {
			msg = msg[:72]
		}
		if err := gitops.CommitMerge(intPath, msg); err != nil {
			r.Log(fmt.Sprintf("Could not commit merge of #%s: %v — dropping.", b.number, err), colors.Red)
			gitops.AbortMergeAndReset(intPath, prev)
			continue
		}
		r.Log(fmt.Sprintf("✓ Merged #%s into %s.", b.number, integrationBranch), colors.Green)
		merged = append(merged, b)
	}

	if len(merged) == 0 {
		r.Log("Every branch failed to merge — no mega PR opened.", colors.Red)
		return 1
	}

	// An operator /quit during the merge loop must not still publish work: bail
	// before any push/PR/auto-merge side-effects.
	r.mu.Lock()
	canceled := r.canceled
	r.mu.Unlock()
	if canceled {
		r.Log("Canceled during collapse — not pushing or opening a PR.", colors.Yellow)
		return 1
	}

	if r.cfg.DryRun {
		r.Log(fmt.Sprintf("[dry-run] would push %s and open one PR closing %s", integrationBranch, issueRefList(merged)), colors.Cyan)
		return 0
	}

	// Push the integration branch (refs are shared, so push from the main repo)
	// and open ONE PR that closes every included issue. CI gates the merge.
	if err := gitops.PushBranch(repo, integrationBranch, false); err != nil {
		r.Log(fmt.Sprintf("Could not push %s: %v", integrationBranch, err), colors.Red)
		return 1
	}

	title := fmt.Sprintf("middle-manager: batch of %d issue(s)", len(merged))
	body := buildMegaPRBody(merged, !r.cfg.NoMerge)
	prURL, err := gitops.CreatePR(repo, title, body, integrationBranch, baseBranch, "", false)
	if err != nil {
		r.Log(fmt.Sprintf("Could not open mega PR (%s is pushed; open it manually): %v", integrationBranch, err), colors.Red)
		return 1
	}
	r.Log(fmt.Sprintf("🎉 Mega PR opened: %s (closes %s)", prURL, issueRefList(merged)), colors.Green+colors.Bold)

	if !r.cfg.NoMerge {
		if prNum := prNumberFromURL(prURL); prNum > 0 {
			if _, err := gitops.EnableAutoMerge(repo, prNum, "squash", true, false); err != nil {
				r.Log(fmt.Sprintf("⚠️ Could not enable auto-merge on the mega PR: %v", err), colors.Yellow)
			} else {
				r.Log("Auto-merge enabled on the mega PR.", colors.Green)
			}
		}
	}
	return 0
}

// resolveConflictWithAgent runs one agent in the integration worktree to resolve
// the listed conflicts. It returns true only when the agent exits cleanly; the
// caller still validates that no markers/unmerged paths remain before committing.
func (r *IssueQueueRunner) resolveConflictWithAgent(intPath, branch string, files []string) bool {
	agent := r.cfg.Execute.Agent
	bin := r.cfg.BinaryOverrides[agent]
	if agents.IsRandom(agent) || !agents.AgentAvailable(agent, bin) {
		agent = agents.PickRandomAgent(r.cfg.BinaryOverrides)
		if agent == "" {
			agent = agents.AutodetectAgent("execute", r.cfg.BinaryOverrides, "")
		}
		bin = r.cfg.BinaryOverrides[agent]
	}
	if agent == "" {
		r.Log("No installed agent available to resolve conflicts.", colors.Red)
		return false
	}

	template := prompts.LoadPrompt(r.cfg.Repo, "collapse")
	prompt := prompts.RenderPrompt(template, map[string]string{
		"repo":           intPath,
		"merge_branch":   branch,
		"conflict_files": strings.Join(files, "\n"),
		"agent_memory":   r.agentMemory(),
	})
	run, err := agents.BuildCommand(agent, prompt, intPath, "", r.cfg.Yolo, nil, bin)
	if err != nil {
		r.Log(fmt.Sprintf("Could not build conflict-resolution command: %v", err), colors.Red)
		return false
	}
	r.Log(fmt.Sprintf("Resolving conflicts with %s...", agent), colors.Cyan)
	onUpdate := func(text string, isThought bool) {
		if r.cfg.StreamOutput {
			os.Stdout.WriteString(text)
		} else {
			tui.NotifyTUIUpdate(text, isThought)
		}
	}
	_, code, err := agents.RunAgent(r.ctx, run, r.cfg.DryRun, "collapse", onUpdate)
	if err != nil || code != 0 {
		r.Log(fmt.Sprintf("Conflict-resolution agent failed (exit %d): %v", code, err), colors.Yellow)
		return false
	}
	return true
}

// agentMemory returns the repo's AGENTS.md / CLAUDE.md so the conflict-resolution
// agent has the same project rules a normal loop step would.
func (r *IssueQueueRunner) agentMemory() string {
	for _, name := range []string{r.cfg.AgentMemoryFile, "AGENTS.md", "CLAUDE.md"} {
		if name == "" {
			continue
		}
		if b, err := os.ReadFile(filepath.Join(r.cfg.Repo, name)); err == nil {
			return string(b)
		}
	}
	return "(no AGENTS.md or CLAUDE.md found)"
}

// cleanupWorktrees removes the per-issue and integration worktrees (their
// branches are preserved). Honors --keep-worktrees for debugging.
func (r *IssueQueueRunner) cleanupWorktrees(repo, worktreeRoot string) {
	if r.cfg.KeepWorktrees {
		r.Log(fmt.Sprintf("Keeping worktrees at %s (--keep-worktrees).", worktreeRoot), colors.Dim)
		return
	}
	if entries, err := os.ReadDir(worktreeRoot); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				_ = gitops.WorktreeRemove(repo, filepath.Join(worktreeRoot, e.Name()))
			}
		}
	}
	gitops.WorktreePrune(repo)
	_ = os.RemoveAll(worktreeRoot)
}

func shortSHA(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

func prNumberFromURL(prURL string) int {
	parts := strings.Split(strings.TrimSpace(prURL), "/")
	if len(parts) == 0 {
		return 0
	}
	n := 0
	for _, c := range parts[len(parts)-1] {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// issueRefList renders "#1, #2, #3" for logging.
func issueRefList(items []builtIssue) string {
	refs := make([]string, 0, len(items))
	for _, b := range items {
		refs = append(refs, "#"+b.number)
	}
	return strings.Join(refs, ", ")
}

// buildMegaPRBody builds the consolidated PR description, including one
// "Closes #N" line per included issue so merging the single PR closes them all.
func buildMegaPRBody(merged []builtIssue, autoMerge bool) string {
	var b strings.Builder
	b.WriteString("Automated batch PR from middle-manager worktree-collapse mode.\n\n")
	b.WriteString("Each issue was developed in its own git worktree and verified independently, then merged into one integration branch:\n\n")
	for _, it := range merged {
		b.WriteString(fmt.Sprintf("- #%s %s\n", it.number, it.title))
	}
	if autoMerge {
		b.WriteString("\n_Auto-merge is enabled — GitHub will merge once required status checks pass (CI gates the combined result)._\n")
	} else {
		b.WriteString("\n**Do not merge without human review.**\n")
	}
	b.WriteString("\n")
	for _, it := range merged {
		b.WriteString(fmt.Sprintf("Closes #%s\n", it.number))
	}
	return b.String()
}
