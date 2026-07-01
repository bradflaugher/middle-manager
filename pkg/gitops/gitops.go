package gitops

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
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
	// Never let a headless drain hang on an interactive credential prompt: a
	// push against a remote with no cached credentials must fail loudly (and be
	// reported) rather than block the loop forever on a hidden tty question.
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
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

// StatusEntry is one `git status --porcelain` line, parsed. Status is the
// two-column XY code with spaces trimmed ("M", "??", "A", …); Path is the
// (rename-resolved, unquoted) file path relative to the repo root.
type StatusEntry struct {
	Status string
	Path   string
}

// StatusEntries parses `git status --porcelain` WITHOUT going through
// RunGit's whole-output TrimSpace, which silently eats the leading space of a
// first-line " M file" entry and shifts every field offset — a trap every
// hand-rolled parser of RunGit's status output has fallen into.
func StatusEntries(repo string) []StatusEntry {
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	var entries []StatusEntry
	for _, line := range strings.Split(string(out), "\n") {
		if len(line) <= 3 {
			continue
		}
		status := strings.TrimSpace(line[:2])
		path := strings.TrimSpace(line[3:])
		if idx := strings.LastIndex(path, " -> "); idx >= 0 {
			path = strings.TrimSpace(path[idx+4:])
		}
		path = strings.Trim(path, `"`)
		if status == "" || path == "" {
			continue
		}
		entries = append(entries, StatusEntry{Status: status, Path: path})
	}
	return entries
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
		if _, _, _, err := RunGit(repo, "checkout", branch); err != nil {
			return branch, err
		}
		// A leftover branch from an earlier run can be stale: created off an old
		// base commit but holding no work of its own (the run failed before
		// committing anything). Fast-forward it onto the current base so a retry
		// doesn't build — and open a PR — on an outdated base, which is a classic
		// source of surprise merge conflicts. Branches with their own commits are
		// left untouched (that is resumable work).
		fastForwardStaleBranch(repo, branch, baseBranch)
		return branch, nil
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

// fastForwardStaleBranch resets branch to baseBranch when — and only when —
// doing so cannot lose anything: the branch must be a strict ancestor of the
// base (no unique commits) and the working tree must be clean. Any doubt means
// no reset.
func fastForwardStaleBranch(repo, branch, baseBranch string) {
	base := strings.TrimSpace(baseBranch)
	if base == "" {
		return
	}
	if !RefExists(repo, base) {
		if !RefExists(repo, "origin/"+base) {
			return
		}
		base = "origin/" + base
	}
	branchSHA, err1 := RevParse(repo, branch)
	baseSHA, err2 := RevParse(repo, base)
	if err1 != nil || err2 != nil || branchSHA == baseSHA {
		return
	}
	if _, _, code, _ := RunGit(repo, "merge-base", "--is-ancestor", branch, base); code != 0 {
		return // branch has its own commits — resumable work, keep it
	}
	if HasChanges(repo) {
		return // never hard-reset under uncommitted work
	}
	_, _, _, _ = RunGit(repo, "reset", "--hard", baseSHA)
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
	_, _, code, err = gitCommit(repo, "commit", "-m", message)
	if err != nil || code != 0 {
		return false, err
	}
	return true, nil
}

// gitCommit runs a git commit command, retrying once with a synthetic identity
// when the host has no user.name/user.email configured — a fresh box must not
// abandon an entire verified iteration over missing git identity.
func gitCommit(repo string, args ...string) (string, string, int, error) {
	stdout, stderr, code, err := RunGit(repo, args...)
	if code != 0 && missingIdentity(stderr) {
		fallback := append([]string{
			"-c", "user.name=middle-manager",
			"-c", "user.email=middle-manager@localhost",
		}, args...)
		return RunGit(repo, fallback...)
	}
	return stdout, stderr, code, err
}

func missingIdentity(stderr string) bool {
	s := strings.ToLower(stderr)
	return strings.Contains(s, "please tell me who you are") ||
		strings.Contains(s, "unable to auto-detect email address") ||
		strings.Contains(s, "empty ident name")
}

// PullFFOnly brings branch current from origin without ever creating a merge
// commit in the operator's repo: a diverged local base fails cleanly (for the
// caller to report) instead of leaving a surprise merge commit or a conflicted
// tree for the drain to trip over.
func PullFFOnly(repo, branch string) error {
	_, stderr, code, err := RunGit(repo, "pull", "--ff-only", "origin", branch)
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("git pull --ff-only origin %s failed: %s", branch, stderr)
	}
	return nil
}

// DiffSummary is a reviewer-oriented snapshot of the working tree: porcelain
// status (which includes untracked files a plain diff misses) plus a diffstat
// against HEAD. Injected into verifier prompts so the critic audits the actual
// change surface instead of trusting the builder's summary.
func DiffSummary(repo string) string {
	if !RepoIsGit(repo) {
		return ""
	}
	status, _, _, _ := RunGit(repo, "status", "--porcelain")
	stat, _, _, _ := RunGit(repo, "diff", "--stat", "HEAD")
	var b strings.Builder
	if status != "" {
		b.WriteString("git status --porcelain:\n" + status + "\n")
	}
	if stat != "" {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString("git diff --stat HEAD:\n" + stat + "\n")
	}
	if b.Len() == 0 {
		return "(working tree clean — no uncommitted changes)"
	}
	return b.String()
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

// runGH executes gh non-interactively in repo and returns trimmed
// stdout/stderr. GH_PROMPT_DISABLED keeps gh from ever blocking an unattended
// run on an interactive question; the update notifier is suppressed so its
// banner can't leak into parsed output.
func runGH(repo string, args ...string) (string, string, error) {
	cmd := exec.Command("gh", args...)
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), "GH_PROMPT_DISABLED=1", "GH_NO_UPDATE_NOTIFIER=1")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return strings.TrimSpace(stdout.String()), strings.TrimSpace(stderr.String()), err
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

	stdout, stderr, err := runGH(repo, "issue", "view", number, "--json", "number,title,body,url")
	if err != nil {
		return map[string]string{"number": number, "title": "", "body": "", "url": issueRef},
			fmt.Errorf("gh issue view %s: %s", number, stderr)
	}

	var data struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
		Body   string `json:"body"`
		URL    string `json:"url"`
	}
	if err := json.Unmarshal([]byte(stdout), &data); err != nil {
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

// ListIssues returns the matching issues, or a non-nil error if the gh query
// itself failed. The error lets the queue distinguish "no matching issues" from
// "gh broke" (auth/network/rate-limit) instead of silently treating a failure as
// an empty queue.
func ListIssues(repo string, label, author string, limit int, state string) ([]map[string]string, error) {
	if !GHAvailable() {
		return nil, fmt.Errorf("gh CLI not available")
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

	stdout, stderr, err := runGH(repo, args...)
	if err != nil {
		return nil, fmt.Errorf("gh issue list: %s", stderr)
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
	if err := json.Unmarshal([]byte(stdout), &items); err != nil {
		return nil, fmt.Errorf("parse gh issue list: %w", err)
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
	return res, nil
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
	if _, _, err := runGH(repo, args...); err != nil {
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
	stdout, stderr, err := runGH(repo, args...)
	if err != nil {
		detail := stderr
		if detail == "" {
			detail = stdout
		}
		return "", fmt.Errorf("%s", detail)
	}
	return stdout, nil
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

	stdout, stderr, err := runGH(repo, args...)
	if err != nil {
		return nil, fmt.Errorf("gh pr list: %s", stderr)
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
	if err := json.Unmarshal([]byte(stdout), &items); err != nil {
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
	// gh exits non-zero when checks are pending/failing (and when there are no
	// required checks), so the exit code is not a reliable signal — parse the
	// JSON instead. Empty/unparseable output means "no required checks".
	out, _, _ := runGH(repo, "pr", "checks", fmt.Sprintf("%d", prNumber), "--required", "--json", "bucket")
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
	stdout, stderr, err := runGH(repo, args...)
	if err != nil {
		msg := stderr
		if msg == "" {
			msg = stdout
		}
		return "", fmt.Errorf("%s", msg)
	}
	return stdout, nil
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
	stdout, stderr, err := runGH(repo, args...)
	if err != nil {
		msg := stderr
		if msg == "" {
			msg = stdout
		}
		return "", fmt.Errorf("%s", msg)
	}
	return stdout, nil
}

// ---------------------------------------------------------------------------
// Worktree-collapse mode (issue #1) — each issue is developed in its own git
// worktree, then the branches are merged into one integration branch for a
// single "mega" PR. mm owns every commit; an agent only resolves conflicts.
// ---------------------------------------------------------------------------

// RevParse resolves a ref (branch, tag, SHA) to a full commit SHA. Used to
// freeze the base at drain start so every worktree branches off the SAME commit
// and the later collapse merges are predictable.
func RevParse(repo, ref string) (string, error) {
	sha, _, _, err := RunGit(repo, "rev-parse", ref)
	if err != nil {
		return "", err
	}
	return sha, nil
}

// WorktreeAddBranch creates branch at startPoint and checks it out in a fresh
// worktree at path. The branch must not already be checked out elsewhere.
func WorktreeAddBranch(repo, path, branch, startPoint string) error {
	_, stderr, code, err := RunGit(repo, "worktree", "add", "-b", branch, path, startPoint)
	if err != nil || code != 0 {
		if err != nil {
			return err
		}
		return fmt.Errorf("git worktree add -b %s failed: %s", branch, stderr)
	}
	return nil
}

// WorktreeRemove force-removes a worktree (force so a dirty leftover tree from a
// failed issue still gets cleaned). Branches it held are preserved.
func WorktreeRemove(repo, path string) error {
	_, _, _, err := RunGit(repo, "worktree", "remove", "--force", path)
	return err
}

// WorktreePrune drops administrative records for worktrees whose directories are
// already gone, so a re-run doesn't trip over stale entries.
func WorktreePrune(repo string) {
	_, _, _, _ = RunGit(repo, "worktree", "prune")
}

// MergeNoCommit merges branch into the current worktree WITHOUT committing
// (--no-ff so every issue stays an identifiable, revertable unit). It returns
// conflicted=true when the merge left unmerged paths for an agent to resolve,
// and upToDate=true when branch was already an ancestor (nothing to merge).
func MergeNoCommit(repo, branch string) (conflicted bool, upToDate bool, err error) {
	stdout, _, code, mergeErr := RunGit(repo, "merge", "--no-ff", "--no-commit", branch)
	if code == 0 {
		// Clean merge stopped before commit, OR nothing to do. Distinguish via
		// MERGE_HEAD: absent means "already up to date".
		if !RefExists(repo, "MERGE_HEAD") {
			return false, true, nil
		}
		if strings.Contains(stdout, "Already up to date") {
			return false, true, nil
		}
		return false, false, nil
	}
	// Non-zero with unmerged paths is the conflict case (an agent will resolve).
	if len(UnmergedPaths(repo)) > 0 {
		return true, false, nil
	}
	return false, false, mergeErr
}

// MergeInProgress reports whether a merge is mid-flight (MERGE_HEAD present) —
// i.e. mm still owes the merge commit. False once the merge is committed or
// aborted (e.g. if a conflict-resolution agent committed it itself).
func MergeInProgress(repo string) bool {
	return RefExists(repo, "MERGE_HEAD")
}

// UnmergedPaths lists files still in conflict (diff-filter=U).
func UnmergedPaths(repo string) []string {
	stdout, _, _, _ := RunGit(repo, "diff", "--name-only", "--diff-filter=U")
	if strings.TrimSpace(stdout) == "" {
		return nil
	}
	return strings.Split(stdout, "\n")
}

// StagedHasConflictMarkers reports whether the staged content still contains
// leftover conflict markers — a fail-closed check before mm commits an
// agent-resolved merge, so a half-resolved tree never lands in the mega PR.
func StagedHasConflictMarkers(repo string) bool {
	// `git diff --cached --check` exits non-zero when staged content has conflict
	// markers (it also flags whitespace; we only treat marker hits as fatal).
	stdout, _, code, _ := RunGit(repo, "diff", "--cached", "--check")
	if code == 0 {
		return false
	}
	return strings.Contains(stdout, "leftover conflict marker")
}

// CommitMerge stages everything and creates the (merge) commit. With MERGE_HEAD
// present git records it as a merge commit; mm always owns this commit so a
// flaky agent can't decide history.
func CommitMerge(repo, message string) error {
	if _, _, code, err := RunGit(repo, "add", "-A"); err != nil || code != 0 {
		if err != nil {
			return err
		}
		return fmt.Errorf("git add failed before merge commit")
	}
	_, stderr, code, err := gitCommit(repo, "commit", "--no-edit", "-m", message)
	if err != nil || code != 0 {
		if err != nil {
			return err
		}
		return fmt.Errorf("git commit (merge) failed: %s", stderr)
	}
	return nil
}

// AbortMergeAndReset backs out an in-progress/failed merge and hard-resets to
// prevSHA, then cleans untracked leftovers — so a dropped issue leaves the
// integration branch exactly as it was before the attempt.
func AbortMergeAndReset(repo, prevSHA string) {
	_, _, _, _ = RunGit(repo, "merge", "--abort")
	if prevSHA != "" {
		_, _, _, _ = RunGit(repo, "reset", "--hard", prevSHA)
	}
	_, _, _, _ = RunGit(repo, "clean", "-fd")
}

// ---------------------------------------------------------------------------
// Single-PR status + bounded wait-for-merge (issue #2, solo serialization)
// ---------------------------------------------------------------------------

// PRStatus is a point-in-time view of one PR used by the wait-for-merge loop.
type PRStatus struct {
	State            string // OPEN | MERGED | CLOSED
	Merged           bool   // derived: State == MERGED
	MergeStateStatus string // CLEAN | BLOCKED | BEHIND | DIRTY | UNSTABLE | UNKNOWN
	Mergeable        string // MERGEABLE | CONFLICTING | UNKNOWN
	ChecksState      string // passing | pending | failing | none
	AutoMergeEnabled bool
}

// GetPRStatus fetches the authoritative state of a single PR via gh's JSON API
// (never by scraping CLI text). NOTE: `gh pr view --json` has no "merged" field —
// a merged PR reports state=="MERGED" (and a non-empty mergedAt), so we derive
// Merged from those rather than requesting an invalid field (which gh rejects).
func GetPRStatus(repo string, number int) (*PRStatus, error) {
	if !GHAvailable() {
		return nil, fmt.Errorf("gh CLI not available")
	}
	stdout, stderr, err := runGH(repo, "pr", "view", fmt.Sprintf("%d", number),
		"--json", "state,mergedAt,mergeStateStatus,mergeable,statusCheckRollup,autoMergeRequest")
	if err != nil {
		return nil, fmt.Errorf("gh pr view %d: %s", number, stderr)
	}
	var data struct {
		State             string    `json:"state"`
		MergedAt          string    `json:"mergedAt"`
		MergeStateStatus  string    `json:"mergeStateStatus"`
		Mergeable         string    `json:"mergeable"`
		StatusCheckRollup []prCheck `json:"statusCheckRollup"`
		AutoMergeRequest  *struct {
			EnabledAt string `json:"enabledAt"`
		} `json:"autoMergeRequest"`
	}
	if err := json.Unmarshal([]byte(stdout), &data); err != nil {
		return nil, fmt.Errorf("parse gh pr view %d: %w", number, err)
	}
	state := strings.ToUpper(data.State)
	return &PRStatus{
		State:            state,
		Merged:           state == "MERGED" || data.MergedAt != "",
		MergeStateStatus: strings.ToUpper(data.MergeStateStatus),
		Mergeable:        strings.ToUpper(data.Mergeable),
		ChecksState:      rollupChecksState(data.StatusCheckRollup),
		AutoMergeEnabled: data.AutoMergeRequest != nil,
	}, nil
}

// DisableAutoMerge cancels a previously-enabled auto-merge so an aborted PR
// can't silently land later, after the drain has already moved on.
func DisableAutoMerge(repo string, number int) {
	if !GHAvailable() {
		return
	}
	_, _, _ = runGH(repo, "pr", "merge", fmt.Sprintf("%d", number), "--disable-auto")
}

// WaitForPRMerge blocks until PR number merges, a terminal/blocked state is
// detected, the timeout elapses, or ctx is cancelled. It NEVER waits forever.
// merged=true only on an actual merge; otherwise reason explains why it stopped.
// Polling backs off (start→max) so a long CI wait doesn't hammer the gh API.
func WaitForPRMerge(ctx context.Context, repo string, number int, timeout time.Duration, log func(string)) (merged bool, reason string) {
	logf := func(format string, a ...interface{}) {
		if log != nil {
			log(fmt.Sprintf(format, a...))
		}
	}
	deadline := time.Now().Add(timeout)
	interval := 15 * time.Second
	const maxInterval = 60 * time.Second

	for {
		st, err := GetPRStatus(repo, number)
		if err != nil {
			// Transient gh failure: don't abort the drain on a single blip, just
			// retry until the deadline.
			logf("⚠️ could not read PR #%d status: %v (will retry)", number, err)
		} else {
			switch {
			case st.Merged || st.State == "MERGED":
				return true, "merged"
			case st.State == "CLOSED":
				return false, "PR was closed without merging"
			case st.Mergeable == "CONFLICTING" || st.MergeStateStatus == "DIRTY":
				return false, "PR has merge conflicts (auto-merge cannot complete)"
			case st.ChecksState == "failing" && RequiredChecksState(repo, number) == "failing":
				// Only a REQUIRED failing check actually blocks GitHub auto-merge.
				// A red optional/advisory check leaves the PR UNSTABLE-but-mergeable,
				// so don't cancel a merge GitHub will still complete.
				return false, "a required check is failing (auto-merge cannot complete)"
			default:
				logf("⏳ PR #%d not merged yet (state=%s merge=%s checks=%s) — waiting…",
					number, st.State, st.MergeStateStatus, st.ChecksState)
			}
		}

		if time.Now().After(deadline) {
			return false, fmt.Sprintf("timed out after %s waiting for PR #%d to merge", timeout, number)
		}

		// Sleep with cancellation, then widen the interval up to the cap.
		select {
		case <-ctx.Done():
			return false, "canceled"
		case <-time.After(interval):
		}
		if interval < maxInterval {
			interval += 15 * time.Second
			if interval > maxInterval {
				interval = maxInterval
			}
		}
	}
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
