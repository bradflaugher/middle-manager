package gitops

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

type GitError struct {
	Dir      string
	Args     []string
	Stdout   string
	Stderr   string
	Code     int
	Original error
}

func (e *GitError) Error() string {
	cmd := "git " + strings.Join(e.Args, " ")
	detail := strings.TrimSpace(e.Stderr)
	if detail == "" {
		detail = strings.TrimSpace(e.Stdout)
	}
	if detail == "" && e.Original != nil {
		detail = e.Original.Error()
	}
	if detail == "" {
		detail = "unknown git error"
	}
	if e.Code >= 0 {
		return fmt.Sprintf("%s failed in %s (exit %d): %s", cmd, e.Dir, e.Code, detail)
	}
	return fmt.Sprintf("%s failed in %s: %s", cmd, e.Dir, detail)
}

func RunGit(repo string, args ...string) (string, string, int, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = repo
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	stdoutText := strings.TrimSpace(stdout.String())
	stderrText := strings.TrimSpace(stderr.String())
	code := 0
	if err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			code = exitError.ExitCode()
		} else {
			code = -1
		}
		err = &GitError{
			Dir:      repo,
			Args:     append([]string(nil), args...),
			Stdout:   stdoutText,
			Stderr:   stderrText,
			Code:     code,
			Original: err,
		}
	}
	return stdoutText, stderrText, code, err
}

func RepoIsGit(repo string) bool {
	stdout, _, code, err := RunGit(repo, "rev-parse", "--is-inside-work-tree")
	return err == nil && code == 0 && stdout == "true"
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
		if RefExists(repo, candidate) {
			return candidate
		}
	}
	// Fall back to the remote's default branch, but return the SHORT name (never
	// "origin/main"): callers check it out and pull it, and `git checkout
	// origin/main` would detach HEAD. ensureBranch handles the case where only
	// the remote-tracking ref exists locally.
	if originHead, _, code, err := RunGit(repo, "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD"); err == nil && code == 0 && originHead != "" {
		return strings.TrimPrefix(originHead, "origin/")
	}
	for _, candidate := range []string{"origin/dev", "origin/main", "origin/master"} {
		if RefExists(repo, candidate) {
			return strings.TrimPrefix(candidate, "origin/")
		}
	}
	if cb, err := CurrentBranch(repo); err == nil && cb != "" {
		return cb
	}
	return "main"
}

func RefExists(repo string, ref string) bool {
	if strings.TrimSpace(ref) == "" {
		return false
	}
	_, _, code, _ := RunGit(repo, "rev-parse", "--verify", ref+"^{commit}")
	return code == 0
}

func EnsureBranch(repo string, prefix string, iteration int, baseBranch string) (string, error) {
	branch := fmt.Sprintf("%s/loop-%d", prefix, iteration)
	return ensureBranch(repo, branch, baseBranch)
}

func EnsureIssueBranch(repo string, prefix string, issueNumber string, baseBranch string) (string, error) {
	branch := fmt.Sprintf("%s/issue-%s", prefix, issueNumber)
	return ensureBranch(repo, branch, baseBranch)
}

func ensureBranch(repo string, branch string, baseBranch string) (string, error) {
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

	if baseBranch != "" && !RefExists(repo, baseBranch) {
		// The base may exist only as a remote-tracking ref (fresh fetch, no local
		// branch yet) — branch off that rather than failing.
		if RefExists(repo, "origin/"+baseBranch) {
			baseBranch = "origin/" + baseBranch
		} else {
			return branch, fmt.Errorf("base branch %q was not found in %s; pass --base-branch or check out the intended base branch before running mm", baseBranch, repo)
		}
	}

	cmdArgs := []string{"checkout", "-b", branch}
	if baseBranch != "" {
		cmdArgs = append(cmdArgs, baseBranch)
	}
	_, _, _, err := RunGit(repo, cmdArgs...)
	return branch, err
}

func CommitAll(repo string, message string) bool {
	committed, _ := CommitAllWithError(repo, message)
	return committed
}

func CommitAllWithError(repo string, message string) (bool, error) {
	if !HasChanges(repo) {
		return false, nil
	}
	_, _, code, err := RunGit(repo, "add", "-A")
	if err != nil || code != 0 {
		return false, err
	}
	_, _, code, err = RunGit(repo, "commit", "-m", message)
	if err != nil || code != 0 {
		return false, err
	}
	return true, nil
}

func PushBranch(repo string, branch string, dryRun bool) error {
	if dryRun {
		fmt.Printf("[dry-run] git push -u origin %s\n", branch)
		return nil
	}
	branch = strings.TrimSpace(branch)
	if branch == "" || branch == "HEAD" {
		return fmt.Errorf("cannot push detached or unknown branch %q", branch)
	}
	remotes, _, code, err := RunGit(repo, "remote")
	if err != nil || code != 0 {
		return fmt.Errorf("list git remotes: %w", err)
	}
	hasOrigin := false
	for _, remote := range strings.Split(remotes, "\n") {
		if strings.TrimSpace(remote) == "origin" {
			hasOrigin = true
			break
		}
	}
	if !hasOrigin {
		return fmt.Errorf("no 'origin' remote configured")
	}
	_, stderr, code, err := RunGit(repo, "push", "-u", "origin", branch)
	if err != nil || code != 0 {
		if err != nil {
			return err
		}
		return fmt.Errorf("git push -u origin %s failed: %s", branch, stderr)
	}
	return nil
}

func GHAvailable() bool {
	_, err := exec.LookPath("gh")
	return err == nil
}

// FetchIssue resolves issue metadata for the given ref. On any failure it
// returns the best-effort number with EMPTY title/body plus a non-nil error —
// it never launders gh stderr into the body, which would otherwise be fed to the
// agents as if it were the issue description. Callers should treat a non-nil
// error as "issue context unavailable".
func FetchIssue(repo string, issueRef string) (map[string]string, error) {
	if strings.TrimSpace(issueRef) == "" {
		return map[string]string{"number": "", "title": "", "body": "", "url": ""}, nil
	}
	re := regexp.MustCompile(`(\d+)$`)
	m := re.FindStringSubmatch(issueRef)
	number := issueRef
	if len(m) > 1 {
		number = m[1]
	}

	if !GHAvailable() {
		return map[string]string{"number": number, "title": "", "body": "", "url": issueRef}, fmt.Errorf("gh CLI not available")
	}

	cmd := exec.Command("gh", "issue", "view", number, "--json", "number,title,body,url")
	cmd.Dir = repo
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return map[string]string{"number": number, "title": "", "body": "", "url": issueRef},
			fmt.Errorf("gh issue view %s: %s", number, strings.TrimSpace(stderr.String()))
	}

	var data struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
		Body   string `json:"body"`
		URL    string `json:"url"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &data); err != nil {
		return map[string]string{"number": number, "title": "", "body": "", "url": issueRef},
			fmt.Errorf("parse gh issue view %s: %w", number, err)
	}

	return map[string]string{
		"number": fmt.Sprintf("%d", data.Number),
		"title":  data.Title,
		"body":   data.Body,
		"url":    data.URL,
	}, nil
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

// CheckoutDefaultBranch checks out the configured base branch when given one,
// falling back to the usual dev/main/master candidates. Without this it would
// blindly land on main/master even when the repo's base is e.g. "dev".
func CheckoutDefaultBranch(repo string, baseBranch string) {
	candidates := []string{}
	if b := strings.TrimPrefix(strings.TrimSpace(baseBranch), "origin/"); b != "" {
		candidates = append(candidates, b)
	}
	candidates = append(candidates, "dev", "main", "master")
	for _, candidate := range candidates {
		_, _, code, _ := RunGit(repo, "rev-parse", "--verify", candidate)
		if code == 0 {
			_, _, _, _ = RunGit(repo, "checkout", candidate)
			return
		}
	}
}

// CreatePR opens a PR for branch via gh. When issueNumber is a positive integer
// a "Closes #N" line is appended to the body so GitHub links and auto-closes the
// issue once the PR merges — `gh pr create` has NO --issue flag, so this is the
// only supported linking mechanism. When baseBranch is set the PR targets it
// explicitly instead of relying on the repository's default branch.
func CreatePR(repo string, title, body, branch, baseBranch, issueNumber string, dryRun bool) (string, error) {
	body = AppendCloses(body, issueNumber)
	args := []string{"pr", "create", "--head", branch, "--title", title, "--body", body}
	if base := strings.TrimPrefix(strings.TrimSpace(baseBranch), "origin/"); base != "" {
		args = append(args, "--base", base)
	}
	if dryRun {
		fmt.Printf("[dry-run] gh %s\n", strings.Join(args, " "))
		return "", nil
	}
	if !GHAvailable() {
		return "", fmt.Errorf("gh CLI not available; skipping PR creation")
	}
	cmd := exec.Command("gh", args...)
	cmd.Dir = repo
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = strings.TrimSpace(stdout.String())
		}
		return "", fmt.Errorf("%s", detail)
	}
	return strings.TrimSpace(stdout.String()), nil
}

// AppendCloses appends a GitHub "Closes #N" auto-link line to a PR body when
// issueNumber is a positive integer, so merging the PR closes the issue. It is a
// no-op for empty/non-numeric refs.
func AppendCloses(body, issueNumber string) string {
	if !isNumeric(issueNumber) {
		return body
	}
	body = strings.TrimRight(body, "\n")
	if body != "" {
		body += "\n\n"
	}
	return body + fmt.Sprintf("Closes #%s", issueNumber)
}

func isNumeric(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// PullRequest is a normalized view of an open PR for the merge feature.
type PullRequest struct {
	Number         int
	Title          string
	Author         string
	HeadRef        string
	BaseRef        string
	URL            string
	IsDraft        bool
	Mergeable      string // MERGEABLE | CONFLICTING | UNKNOWN
	MergeState     string // CLEAN | BLOCKED | BEHIND | DIRTY | UNSTABLE | ...
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
		"--json", "number,title,author,headRefName,baseRefName,url,isDraft,mergeable,mergeStateStatus,reviewDecision,statusCheckRollup"}
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
		Number int    `json:"number"`
		Title  string `json:"title"`
		Author struct {
			Login string `json:"login"`
		} `json:"author"`
		HeadRefName       string    `json:"headRefName"`
		BaseRefName       string    `json:"baseRefName"`
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
			BaseRef:        it.BaseRefName,
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

// RequiredChecksState reports the combined state of ONLY the branch-protection
// required status checks for a PR: "passing", "pending", "failing", or "none"
// (the repo defines no required checks / none are reported yet). It lets
// `mm merge` ignore non-blocking checks and merge as soon as the required ones
// are green — GitHub itself then reports such a PR as UNSTABLE (still mergeable).
func RequiredChecksState(repo string, prNumber int) string {
	if !GHAvailable() {
		return "none"
	}
	cmd := exec.Command("gh", "pr", "checks", fmt.Sprintf("%d", prNumber), "--required", "--json", "bucket")
	cmd.Dir = repo
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	// gh exits non-zero when checks are pending/failing (and when there are no
	// required checks), so the exit code is not a reliable signal — parse the
	// JSON instead. Empty/unparseable output means "no required checks".
	_ = cmd.Run()
	out := strings.TrimSpace(stdout.String())
	if out == "" {
		return "none"
	}
	var checks []struct {
		Bucket string `json:"bucket"`
	}
	if err := json.Unmarshal([]byte(out), &checks); err != nil {
		return "none"
	}
	buckets := make([]string, 0, len(checks))
	for _, c := range checks {
		buckets = append(buckets, c.Bucket)
	}
	return requiredBucketState(buckets)
}

// requiredBucketState reduces gh's per-check `bucket` values to a single state.
// Buckets are one of pass|fail|pending|skipping|cancel.
func requiredBucketState(buckets []string) string {
	if len(buckets) == 0 {
		return "none"
	}
	pending := false
	for _, b := range buckets {
		switch strings.ToLower(b) {
		case "fail", "cancel":
			return "failing"
		case "pending":
			pending = true
		}
		// "pass" and "skipping" count as satisfied.
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

// EnableAutoMerge configures auto-merge on a PR via the gh CLI.
func EnableAutoMerge(repo string, number int, method string, deleteBranch bool, dryRun bool) (string, error) {
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
	args := []string{"pr", "merge", fmt.Sprintf("%d", number), methodFlag, "--auto"}
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
