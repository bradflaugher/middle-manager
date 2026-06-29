package queue

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/bradflaugher/middle-manager/pkg/colors"
	"github.com/bradflaugher/middle-manager/pkg/config"
	"github.com/bradflaugher/middle-manager/pkg/gitops"
	"github.com/bradflaugher/middle-manager/pkg/loop"
)

type IssueQueueRunner struct {
	cfg     *config.LoopConfig
	logPath string
	// baseStateDir is the top-level state dir captured once, before any per-issue
	// override. ResetIssueState derives each issue dir from this so they sit side
	// by side (issues/1, issues/2, …) instead of nesting under one another.
	baseStateDir string
}

func NewIssueQueueRunner(cfg *config.LoopConfig) (*IssueQueueRunner, error) {
	if cfg.IssueQueue == nil {
		return nil, fmt.Errorf("issue_queue config required")
	}
	baseStateDir := cfg.StatePath()
	logPath := filepath.Join(baseStateDir, "queue.log")
	return &IssueQueueRunner{
		cfg:          cfg,
		logPath:      logPath,
		baseStateDir: baseStateDir,
	}, nil
}

func (r *IssueQueueRunner) Log(msg string, colorCode string) {
	timestamp := time.Now().Format("2006-01-02 15:04:05")
	rawLine := fmt.Sprintf("[%s] %s", timestamp, msg)

	if colorCode != "" {
		fmt.Println(colors.Colored(rawLine, colorCode))
	} else {
		fmt.Println(rawLine)
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

	succeeded := 0
	failed := 0

	for idx, issue := range issues {
		number := issue["number"]
		r.Log(fmt.Sprintf("=== Queue %d/%d: Issue #%s — %s ===", idx+1, len(issues), number, issue["title"]), colors.Cyan+colors.Bold)

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
		result, err := l.RunUntilComplete()

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
		}
	}

	r.Log(fmt.Sprintf("Queue finished: %d succeeded, %d incomplete.", succeeded, failed), colors.Green)
	if failed > 0 {
		return 1
	}
	return 0
}
