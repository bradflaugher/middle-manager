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
}

func NewIssueQueueRunner(cfg *config.LoopConfig) (*IssueQueueRunner, error) {
	if cfg.IssueQueue == nil {
		return nil, fmt.Errorf("issue_queue config required")
	}
	logPath := filepath.Join(cfg.StatePath(), "queue.log")
	return &IssueQueueRunner{
		cfg:     cfg,
		logPath: logPath,
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
	state := r.cfg.StatePath()
	number := issue["number"]
	issueDir := filepath.Join(state, "issues", number)
	_ = os.MkdirAll(issueDir, 0755)

	// Override config state
	r.cfg.StateDir = issueDir
	r.cfg.Issue = number
}

func (r *IssueQueueRunner) Run() int {
	issues := gitops.ListIssues(r.cfg.Repo, r.cfg.IssueQueue.Label, r.cfg.IssueQueue.Author, r.cfg.IssueQueue.Limit, r.cfg.IssueQueue.State)
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
		result, err := l.RunUntilComplete()

		if err == nil && result.Success {
			succeeded++
			r.Log(fmt.Sprintf("Issue #%s done.", number), colors.Green)
			if r.cfg.IssueQueue.CloseOnSuccess {
				comment := r.cfg.IssueQueue.CloseComment
				if comment == "" {
					comment = "Closed by middle-manager — fix verified and PR opened."
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
