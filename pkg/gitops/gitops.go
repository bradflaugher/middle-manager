package gitops

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

func RunGit(repo string, args ...string) (string, string, int, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = repo
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	code := 0
	if err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			code = exitError.ExitCode()
		} else {
			code = -1
		}
	}
	return strings.TrimSpace(stdout.String()), strings.TrimSpace(stderr.String()), code, err
}

func RepoIsGit(repo string) bool {
	fi, err := os.Stat(filepath.Join(repo, ".git"))
	return err == nil && fi.IsDir()
}

func CurrentBranch(repo string) (string, error) {
	stdout, _, _, err := RunGit(repo, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", err
	}
	return stdout, nil
}

func HasChanges(repo string) bool {
	stdout, _, _, _ := RunGit(repo, "status", "--porcelain")
	return len(stdout) > 0
}

func DetectBaseBranch(repo string) string {
	for _, candidate := range []string{"dev", "main", "master"} {
		_, _, code, _ := RunGit(repo, "rev-parse", "--verify", candidate)
		if code == 0 {
			return candidate
		}
	}
	if cb, err := CurrentBranch(repo); err == nil && cb != "" {
		return cb
	}
	return "main"
}

func EnsureBranch(repo string, prefix string, iteration int, baseBranch string) (string, error) {
	branch := fmt.Sprintf("%s/loop-%d", prefix, iteration)
	branches, _, _, _ := RunGit(repo, "branch", "--list", branch)

	hasBranch := false
	for _, b := range strings.Split(branches, "\n") {
		b = strings.TrimSpace(b)
		b = strings.TrimPrefix(b, "*")
		b = strings.TrimSpace(b)
		if b == branch {
			hasBranch = true
			break
		}
	}

	if hasBranch {
		_, _, _, err := RunGit(repo, "checkout", branch)
		return branch, err
	}

	cmdArgs := []string{"checkout", "-b", branch}
	if baseBranch != "" {
		cmdArgs = append(cmdArgs, baseBranch)
	}
	_, _, _, err := RunGit(repo, cmdArgs...)
	return branch, err
}

func EnsureIssueBranch(repo string, prefix string, issueNumber string, baseBranch string) (string, error) {
	branch := fmt.Sprintf("%s/issue-%s", prefix, issueNumber)
	branches, _, _, _ := RunGit(repo, "branch", "--list", branch)

	hasBranch := false
	for _, b := range strings.Split(branches, "\n") {
		b = strings.TrimSpace(b)
		b = strings.TrimPrefix(b, "*")
		b = strings.TrimSpace(b)
		if b == branch {
			hasBranch = true
			break
		}
	}

	if hasBranch {
		_, _, _, err := RunGit(repo, "checkout", branch)
		return branch, err
	}

	cmdArgs := []string{"checkout", "-b", branch}
	if baseBranch != "" {
		cmdArgs = append(cmdArgs, baseBranch)
	}
	_, _, _, err := RunGit(repo, cmdArgs...)
	return branch, err
}

func CommitAll(repo string, message string) bool {
	if !HasChanges(repo) {
		return false
	}
	_, _, code, err := RunGit(repo, "add", "-A")
	if err != nil || code != 0 {
		return false
	}
	_, _, code, err = RunGit(repo, "commit", "-m", message)
	return err == nil && code == 0
}

func PushBranch(repo string, branch string, dryRun bool) {
	if dryRun {
		fmt.Printf("[dry-run] git push -u origin %s\n", branch)
		return
	}
	remotes, _, _, err := RunGit(repo, "remote")
	if err != nil || !strings.Contains(remotes, "origin") {
		fmt.Printf("[git] No 'origin' remote found, skipping push of branch '%s'.\n", branch)
		return
	}
	_, stderr, code, err := RunGit(repo, "push", "-u", "origin", branch)
	if err != nil || code != 0 {
		fmt.Printf("[git] Warning: Failed to push branch '%s' to origin: %s\n", branch, stderr)
	}
}

func GHAvailable() bool {
	_, err := exec.LookPath("gh")
	return err == nil
}

func FetchIssue(repo string, issueRef string) map[string]string {
	if !GHAvailable() {
		return map[string]string{"number": issueRef, "title": "", "body": "", "url": issueRef}
	}
	re := regexp.MustCompile(`(\d+)$`)
	m := re.FindStringSubmatch(issueRef)
	number := issueRef
	if len(m) > 1 {
		number = m[1]
	}

	cmd := exec.Command("gh", "issue", "view", number, "--json", "number,title,body,url")
	cmd.Dir = repo
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return map[string]string{"number": number, "title": "", "body": stderr.String(), "url": issueRef}
	}

	var data struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
		Body   string `json:"body"`
		URL    string `json:"url"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &data); err != nil {
		return map[string]string{"number": number, "title": "", "body": stdout.String(), "url": issueRef}
	}

	return map[string]string{
		"number": fmt.Sprintf("%d", data.Number),
		"title":  data.Title,
		"body":   data.Body,
		"url":    data.URL,
	}
}

func ListIssues(repo string, label, author string, limit int, state string) []map[string]string {
	if !GHAvailable() {
		return nil
	}

	if limit <= 0 {
		limit = 20
	}
	if state == "" {
		state = "open"
	}

	args := []string{"issue", "list", "--state", state, "--json", "number,title,body,url,labels,author", "--limit", fmt.Sprintf("%d", limit)}
	if label != "" {
		args = append(args, "--label", label)
	}
	if author != "" {
		author = strings.TrimPrefix(author, "@")
		args = append(args, "--author", author)
	}

	cmd := exec.Command("gh", args...)
	cmd.Dir = repo
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return nil
	}

	type ghAuthor struct {
		Login string `json:"login"`
	}
	type ghIssue struct {
		Number int      `json:"number"`
		Title  string   `json:"title"`
		Body   string   `json:"body"`
		URL    string   `json:"url"`
		Author ghAuthor `json:"author"`
	}

	var items []ghIssue
	if err := json.Unmarshal(stdout.Bytes(), &items); err != nil {
		return nil
	}

	res := make([]map[string]string, 0, len(items))
	for _, item := range items {
		res = append(res, map[string]string{
			"number": fmt.Sprintf("%d", item.Number),
			"title":  item.Title,
			"body":   item.Body,
			"url":    item.URL,
			"author": item.Author.Login,
		})
	}
	return res
}

func CloseIssue(repo string, number string, comment string, dryRun bool) bool {
	if dryRun {
		fmt.Printf("[dry-run] gh issue close %s", number)
		if comment != "" {
			fmt.Printf(" --comment %q", comment)
		}
		fmt.Println()
		return true
	}
	if !GHAvailable() {
		fmt.Println("gh CLI not available; cannot close issue")
		return false
	}
	args := []string{"issue", "close", number}
	if comment != "" {
		args = append(args, "--comment", comment)
	}
	cmd := exec.Command("gh", args...)
	cmd.Dir = repo
	if err := cmd.Run(); err != nil {
		return false
	}
	return true
}

func CheckoutDefaultBranch(repo string) {
	for _, candidate := range []string{"main", "master"} {
		_, _, code, _ := RunGit(repo, "rev-parse", "--verify", candidate)
		if code == 0 {
			_, _, _, _ = RunGit(repo, "checkout", candidate)
			return
		}
	}
}

func CreatePR(repo string, title, body, branch, issueNumber string, dryRun bool) (string, error) {
	if dryRun {
		fmt.Printf("[dry-run] gh pr create --head %s --title %q\n", branch, title)
		return "", nil
	}
	if !GHAvailable() {
		return "", fmt.Errorf("gh CLI not available; skipping PR creation")
	}
	args := []string{"pr", "create", "--head", branch, "--title", title, "--body", body}
	if issueNumber != "" {
		// Verify if issue number is numeric
		isNumeric := true
		for _, r := range issueNumber {
			if r < '0' || r > '9' {
				isNumeric = false
				break
			}
		}
		if isNumeric {
			args = append(args, "--issue", issueNumber)
		}
	}
	cmd := exec.Command("gh", args...)
	cmd.Dir = repo
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s", strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

// PullRequest is a normalized view of an open PR for the merge feature.
type PullRequest struct {
	Number         int
	Title          string
	Author         string
	HeadRef        string
	URL            string
	IsDraft        bool
	Mergeable      string // MERGEABLE | CONFLICTING | UNKNOWN
	MergeState	   string // CLEAN | BLOCKED | BEHIND | DIRTY | UNSTABLE | ...
	ReviewDecision string // APPROVED | CHANGES_REQUESTED | REVIEW_REQUIRED | ""
	ChecksState    string // passing | pending | failing | none
}

// Mergeable reports whether a PR is safe to auto-merge under our conservative
// policy. requireChecks gates on CI being green (vs. merely not failing).
func (pr PullRequest) IsSafeToMerge(requireChecks bool) (bool, string) {
	if pr.IsDraft {
		return false, "draft"
	}
	if pr.Mergeable == "CONFLICTING" || pr.MergeState == "DIRTY" {
		return false, "merge conflicts"
	}
	if pr.ReviewDecision == "CHANGES_REQUESTED" {
		return false, "changes requested"
	}
	if pr.ChecksState == "failing" {
		return false, "checks failing"
	}
	if requireChecks {
		switch pr.ChecksState {
		case "pending":
			return false, "checks pending"
		case "failing":
			return false, "checks failing"
		}
		// "passing" and "none" are acceptable when requiring checks.
	}
	// mergeStateStatus is the most authoritative signal GitHub gives us.
	switch pr.MergeState {
	case "DIRTY":
		return false, "merge conflicts"
	case "BEHIND":
		return false, "branch behind base (needs update)"
	case "BLOCKED":
		// Blocked usually means required reviews/checks are missing.
		if pr.ReviewDecision == "APPROVED" && !requireChecks {
			return true, "approved"
		}
		return false, "blocked by branch protection"
	}
	return true, "mergeable"
}

// ListOpenPRs returns open PRs, optionally filtered by author login and label.
func ListOpenPRs(repo string, author string, label string, limit int) ([]PullRequest, error) {
	if !GHAvailable() {
		return nil, fmt.Errorf("gh CLI not available")
	}
	if limit <= 0 {
		limit = 30
	}
	args := []string{"pr", "list", "--state", "open", "--limit", fmt.Sprintf("%d", limit),
		"--json", "number,title,author,headRefName,url,isDraft,mergeable,mergeStateStatus,reviewDecision,statusCheckRollup"}
	if author != "" {
		args = append(args, "--author", strings.TrimPrefix(author, "@"))
	}
	if label != "" {
		args = append(args, "--label", label)
	}

	cmd := exec.Command("gh", args...)
	cmd.Dir = repo
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("gh pr list: %s", strings.TrimSpace(stderr.String()))
	}

	type ghPR struct {
		Number            int       `json:"number"`
		Title             string    `json:"title"`
		Author            struct{ Login string `json:"login"` } `json:"author"`
		HeadRefName       string    `json:"headRefName"`
		URL               string    `json:"url"`
		IsDraft           bool      `json:"isDraft"`
		Mergeable         string    `json:"mergeable"`
		MergeStateStatus  string    `json:"mergeStateStatus"`
		ReviewDecision    string    `json:"reviewDecision"`
		StatusCheckRollup []prCheck `json:"statusCheckRollup"`
	}

	var items []ghPR
	if err := json.Unmarshal(stdout.Bytes(), &items); err != nil {
		return nil, fmt.Errorf("parse gh pr list: %w", err)
	}

	prs := make([]PullRequest, 0, len(items))
	for _, it := range items {
		prs = append(prs, PullRequest{
			Number:         it.Number,
			Title:          it.Title,
			Author:         it.Author.Login,
			HeadRef:        it.HeadRefName,
			URL:            it.URL,
			IsDraft:        it.IsDraft,
			Mergeable:      it.Mergeable,
			MergeState:     it.MergeStateStatus,
			ReviewDecision: it.ReviewDecision,
			ChecksState:    rollupChecksState(it.StatusCheckRollup),
		})
	}
	return prs, nil
}

// prCheck is one entry of a PR's statusCheckRollup (GitHub Actions check runs
// use Status+Conclusion; legacy commit statuses use State).
type prCheck struct {
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	State      string `json:"state"`
}

func rollupChecksState(checks []prCheck) string {
	if len(checks) == 0 {
		return "none"
	}
	pending := false
	for _, c := range checks {
		// GitHub Actions style: Status COMPLETED + Conclusion.
		if c.Status != "" && c.Status != "COMPLETED" {
			pending = true
			continue
		}
		outcome := c.Conclusion
		if outcome == "" {
			outcome = c.State // commit-status contexts use State (SUCCESS/PENDING/FAILURE/ERROR)
		}
		switch strings.ToUpper(outcome) {
		case "SUCCESS", "NEUTRAL", "SKIPPED", "":
			// ok
		case "PENDING", "QUEUED", "IN_PROGRESS", "EXPECTED":
			pending = true
		default:
			return "failing" // FAILURE, ERROR, CANCELLED, TIMED_OUT, ACTION_REQUIRED, STARTUP_FAILURE
		}
	}
	if pending {
		return "pending"
	}
	return "passing"
}

// MergePR merges a PR via the gh CLI. It never uses --admin and never
// force-merges; method is one of squash|merge|rebase.
func MergePR(repo string, number int, method string, deleteBranch bool, dryRun bool) (string, error) {
	if method == "" {
		method = "squash"
	}
	methodFlag := "--squash"
	switch method {
	case "merge":
		methodFlag = "--merge"
	case "rebase":
		methodFlag = "--rebase"
	}
	args := []string{"pr", "merge", fmt.Sprintf("%d", number), methodFlag}
	if deleteBranch {
		args = append(args, "--delete-branch")
	}
	if dryRun {
		return fmt.Sprintf("[dry-run] gh %s", strings.Join(args, " ")), nil
	}
	if !GHAvailable() {
		return "", fmt.Errorf("gh CLI not available")
	}
	cmd := exec.Command("gh", args...)
	cmd.Dir = repo
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		return "", fmt.Errorf("%s", msg)
	}
	return strings.TrimSpace(stdout.String()), nil
}

func PlanIsComplete(planText string) bool {
	pending := false
	lines := strings.Split(planText, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "- [ ]") {
			pending = true
			break
		}
	}
	if !pending {
		return strings.TrimSpace(planText) != ""
	}
	return false
}
