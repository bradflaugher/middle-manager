// Package merge implements middle-manager's "merge" mode: it drains the
// repository's open pull requests, merging only the ones that are demonstrably
// safe (no conflicts, no requested changes, green checks). It never force-merges
// and never uses gh's --admin escape hatch.
package merge

import (
	"fmt"
	"strings"

	"github.com/bradflaugher/middle-manager/pkg/config"
	"github.com/bradflaugher/middle-manager/pkg/gitops"
	"github.com/bradflaugher/middle-manager/pkg/tui"
)

type Runner struct {
	cfg *config.LoopConfig
}

func NewRunner(cfg *config.LoopConfig) *Runner {
	return &Runner{cfg: cfg}
}

type Outcome struct {
	PR      gitops.PullRequest
	Merged  bool
	Skipped bool
	Reason  string
}

// Run lists open PRs, merges the safe ones, and renders a summary. Returns a
// process exit code (0 = nothing failed).
func (r *Runner) Run() int {
	prs, err := gitops.ListOpenPRs(r.cfg.Repo, r.cfg.MergeAuthor, r.cfg.MergeLabel, r.cfg.MergeLimit)
	if err != nil {
		fmt.Println(tui.RenderError(fmt.Sprintf("Could not list PRs: %v", err)))
		return 1
	}

	// --merge-pr N narrows to a single PR.
	if r.cfg.MergePRNumber > 0 {
		var only []gitops.PullRequest
		for _, pr := range prs {
			if pr.Number == r.cfg.MergePRNumber {
				only = append(only, pr)
			}
		}
		prs = only
	}

	fmt.Println(tui.RenderMergeHeader(r.cfg.Repo, r.cfg.MergeAuthor, r.cfg.MergeLabel, r.cfg.MergeRequireChecks, r.cfg.DryRun))

	if len(prs) == 0 {
		if r.cfg.MergePRNumber > 0 {
			fmt.Println(tui.RenderInfo(fmt.Sprintf("PR #%d is not open / does not match the filter.", r.cfg.MergePRNumber)))
		} else {
			fmt.Println(tui.RenderInfo("No open PRs match the filter. Nothing to merge."))
		}
		return 0
	}

	var outcomes []Outcome
	merged, skipped, failed := 0, 0, 0

	for _, pr := range prs {
		safe, reason := pr.IsSafeToMerge(r.cfg.MergeRequireChecks)
		if !safe {
			skipped++
			outcomes = append(outcomes, Outcome{PR: pr, Skipped: true, Reason: reason})
			continue
		}
		out, mErr := gitops.MergePR(r.cfg.Repo, pr.Number, r.cfg.MergeMethod, r.cfg.MergeDeleteBranch, r.cfg.DryRun)
		if mErr != nil {
			failed++
			outcomes = append(outcomes, Outcome{PR: pr, Reason: firstLine(mErr.Error())})
			continue
		}
		merged++
		detail := r.cfg.MergeMethod
		if r.cfg.DryRun {
			detail = "dry-run"
		}
		_ = out
		outcomes = append(outcomes, Outcome{PR: pr, Merged: true, Reason: detail})
	}

	fmt.Println(tui.RenderMergeTable(toRows(outcomes)))
	fmt.Println(tui.RenderMergeSummary(merged, skipped, failed, r.cfg.DryRun))

	if failed > 0 {
		return 1
	}
	return 0
}

func toRows(outcomes []Outcome) []tui.MergeRow {
	rows := make([]tui.MergeRow, 0, len(outcomes))
	for _, o := range outcomes {
		status := "skip"
		if o.Merged {
			status = "merged"
		} else if !o.Skipped {
			status = "failed"
		}
		rows = append(rows, tui.MergeRow{
			Number: o.PR.Number,
			Title:  o.PR.Title,
			Author: o.PR.Author,
			Status: status,
			Reason: o.Reason,
		})
	}
	return rows
}

func firstLine(s string) string {
	return strings.TrimSpace(strings.SplitN(s, "\n", 2)[0])
}
