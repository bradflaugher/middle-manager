package prompts

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const DiscoverTemplate = `# Discover & Scope — Iteration {iteration}

You are the **Planner** agent. Your job is to analyze the issue and scope the required changes.

## Mission
{mission}

## Repository
` + "`" + `{repo}` + "`" + `

## GitHub Issue
- Number: {issue_number}
- Title: {issue_title}
- URL/ref: {issue}
- Body:
{issue_body}

## Repository Memory (AGENTS.md / CLAUDE.md)
{agent_memory}

## Orchestrator notes (learnings from previous runs)
{notes}

## Your job

1. **Scan the codebase** — use ripgrep, file reads, and critical thinking. Do not assume something is implemented just because a search returned nothing.
2. **Determine the scope of changes** — identify files/modules to touch, API requirements, and potential pitfalls.
3. **Do not implement fixes yet** — planning and discovery only. Do not modify any files.
4. Output a summary of your findings, files to modify, and execution guidelines for the next step. Your full output is handed verbatim to the programmer agent, so make it self-contained: exact file paths, function names, and acceptance criteria.`

const DiscoverFeatureTemplate = `# Feature Scoping — Iteration {iteration}

You are the **Planner** for a single feature request. Stay tight.

## Mission (the feature to build)
{mission}

## Repository
` + "`" + `{repo}` + "`" + `

## Repository memory
{agent_memory}

## Orchestrator notes (learnings from previous runs)
{notes}

## Your job

1. **Scope ONLY the mission above.** Do not go on a repo-wide bug hunt.
2. **Determine the scope of changes** — identify files to create or modify.
3. **Do not implement code** — planning and discovery only. Do not modify any files.
4. Output a summary of your findings, files to modify, and execution guidelines for the next step. Your full output is handed verbatim to the programmer agent, so make it self-contained: exact file paths, function names, and acceptance criteria.`

const ExecuteTemplate = `# Generate & Execute — Iteration {iteration}

You are the **Programmer** agent. Implement the feature or fix described in the mission.

## Mission
{mission}

## Repository
` + "`" + `{repo}` + "`" + `

## GitHub Issue (if applicable)
- Number: {issue_number}
- Title: {issue_title}
- Body:
{issue_body}

## Discovery Scoping Summary (if any)
{discover_output}

## Repository memory
{agent_memory}

## Orchestrator notes (learnings from previous runs)
{notes}

## Verifier feedback and previous errors (fix these first, if any)
{error_log}

## Rules

1. **Minimal correct change.** Match existing code style.
2. Run relevant tests/build commands yourself before finishing.
3. Do not commit, push, open PRs, or merge — the loop handles all git.
4. **Never create or modify agent-memory or orchestrator files** (AGENTS.md, CLAUDE.md, .middle-manager/, .cursorrules, and the like) unless the mission explicitly asks for it.

Ship the requested mission. Nothing else.`

const VerifyTemplate = `# Verifier & Backpressure — Iteration {iteration}

You are the **Critic**. Audit the work from the last execution step.

## Mission
{mission}

## Repository
` + "`" + `{repo}` + "`" + `

## GitHub Issue (if applicable)
- Number: {issue_number}
- Title: {issue_title}
- Body:
{issue_body}

## What the programmer agent reported doing
{execute_output}

## Actual working-tree change surface (from git)
{diff_summary}

## Previous verifier report (if any)
{test_output}

## Error log
{error_log}

## Repository memory
{agent_memory}

## Orchestrator notes (learnings from previous runs)
{notes}

## Evaluation criteria

1. Does the change actually address the mission?
2. Are there obvious bugs, security issues, or style violations?
3. Do tests pass? Run them yourself. If not, explain exactly what failed and how to fix it.
4. Cross-check: read the diff and relevant files — do not trust the builder's summary alone. If the change surface above includes unrelated files (especially AGENTS.md, CLAUDE.md, or orchestrator state), flag it and FAIL.

You are an auditor: run tests and builds, but do **not** modify source files, commit, or touch git state.

## Output format

` + "```" + `
VERDICT: PASS | FAIL
SUMMARY: ...
ISSUES:
- ...
` + "```" + `

If FAIL, make ISSUES concrete and actionable (file, symptom, suggested fix) — middle-manager feeds your full report to the next iteration's programmer automatically. Do not write any files.`

const CommitTemplate = `# Loop Back & Commit — Iteration {iteration}

You are the **Ship** agent. Persist learnings and commit verified work.

## Repository
` + "`" + `{repo}` + "`" + `

## Completed mission
{mission}

## GitHub Issue (if applicable)
- Number: {issue_number}
- Title: {issue_title}
- Body:
{issue_body}

## Verifier report
{test_output}

## Working-tree change surface (from git)
{diff_summary}

## Repository memory
{agent_memory}

## Orchestrator notes (learnings from previous runs)
{notes}

## Your job

1. Append durable learnings from this run (build commands, gotchas, conventions discovered) to the orchestrator notes file at ` + "`" + `{notes_file}` + "`" + `. It lives OUTSIDE the repository on purpose — keep it short, deduplicated against what is already there, and factual. **Do NOT edit AGENTS.md or CLAUDE.md, and do not write orchestrator state into the repository.**
2. ` + "`" + `git add` + "`" + ` only files relevant to the mission. **Do not** ` + "`" + `git push --force` + "`" + `.
3. Commit with message: ` + "`" + `middle-manager: {mission}` + "`" + ` (truncate to 72 chars).

**Do not push and do not open a pull request.** middle-manager pushes the branch, opens the PR, and links the issue (` + "`" + `Closes #{issue_number}` + "`" + `) — all deterministically, right after this step, then merges according to the configured merge policy. Your only job is to land one clean commit (plus the notes update outside the repo).

If there is nothing to commit, say so and exit cleanly.`

const SoloTemplate = `# Solo Build — Iteration {iteration}

You are the **only** agent on this task. There is no separate planner, verifier, or
committer — you do everything end to end in this one step.

## Mission
{mission}

## Repository
` + "`" + `{repo}` + "`" + `

## GitHub Issue (if applicable)
- Number: {issue_number}
- Title: {issue_title}
- Body:
{issue_body}

## Repository memory (AGENTS.md / CLAUDE.md)
{agent_memory}

## Orchestrator notes (learnings from previous runs)
{notes}

## Previous errors (if any)
{error_log}

## Your job

1. **Scope** the change against the mission and the codebase (read before you edit).
2. **Implement** the minimal correct change. Match existing style.
3. **Run the tests / build yourself** and make them pass. Do NOT skip this — you are
   your own verifier, so the project's correctness rests entirely on this step.
4. **Self-review** the diff for bugs, security issues, and scope creep.
5. Do **not** commit, push, open a PR, or merge — middle-manager does all git/PR work
   deterministically right after this step (it commits, links ` + "`" + `Closes #{issue_number}` + "`" + `,
   opens one PR, and waits for it to merge). Your job is the working-tree change only.
6. **Never edit AGENTS.md or CLAUDE.md, and never write orchestrator state into the
   repo** unless the mission explicitly asks. If you learned something durable
   (build commands, gotchas), append it to ` + "`" + `{notes_file}` + "`" + ` — it lives outside the repo.

## Output format

End your response with EXACTLY one verdict line, as the very last line:

` + "```" + `
VERDICT: PASS
` + "```" + `

Emit ` + "`" + `VERDICT: PASS` + "`" + ` only if you implemented the mission AND the tests/build pass.
Otherwise emit ` + "`" + `VERDICT: FAIL` + "`" + ` followed by what is still broken. A missing or
ambiguous verdict is treated as FAIL — the work will not ship.`

const CollapseTemplate = `# Collapse / Merge-Conflict Resolution

You are resolving a Git **merge conflict** while middle-manager consolidates several
independently-developed issue branches into one integration branch.

## Repository (this is a git worktree on the integration branch)
` + "`" + `{repo}` + "`" + `

## Branch being merged
{merge_branch}

## Conflicted files (resolve every one)
{conflict_files}

## Repository memory (AGENTS.md / CLAUDE.md)
{agent_memory}

## Your job

1. Open each conflicted file and resolve it so it integrates BOTH sides' intent —
   keep both features working; do not blindly discard either side.
2. Remove every conflict marker (` + "`<<<<<<<`" + `, ` + "`=======`" + `, ` + "`>>>>>>>`" + `). Make sure
   the merged result still builds.
3. Stage your resolutions with ` + "`" + `git add` + "`" + ` on the resolved files.
4. **Do NOT run ` + "`" + `git commit` + "`" + `, ` + "`" + `git merge --continue` + "`" + `, ` + "`" + `git merge --abort` + "`" + `,
   ` + "`" + `git push` + "`" + `, or ` + "`" + `git reset` + "`" + `.** middle-manager verifies there are no remaining
   conflict markers and creates the merge commit itself.

When every conflicted file is resolved and staged, stop. If a file genuinely cannot
be reconciled, say so explicitly and leave it unstaged.`

// LoadPrompt returns the prompt template for name. Custom overrides are looked
// up first in <stateRoot>/prompts/ (orchestrator-owned, outside the repo) and
// then in <repo>/.middle-manager/prompts/ (deliberately committed by the repo
// owner); otherwise the embedded default is used.
func LoadPrompt(repoPath, stateRoot, name string) string {
	candidates := []string{}
	if stateRoot != "" {
		candidates = append(candidates, filepath.Join(stateRoot, "prompts", name+".md"))
	}
	candidates = append(candidates, filepath.Join(repoPath, ".middle-manager", "prompts", name+".md"))
	for _, p := range candidates {
		if b, err := os.ReadFile(p); err == nil {
			return string(b)
		}
	}

	switch name {
	case "discover":
		return DiscoverTemplate
	case "discover_feature":
		return DiscoverFeatureTemplate
	case "execute":
		return ExecuteTemplate
	case "verify":
		return VerifyTemplate
	case "commit":
		return CommitTemplate
	case "solo":
		return SoloTemplate
	case "collapse":
		return CollapseTemplate
	default:
		return ""
	}
}

func RenderPrompt(template string, ctx map[string]string) string {
	res := template
	for k, v := range ctx {
		res = strings.ReplaceAll(res, "{"+k+"}", v)
	}
	return res
}

// clipMarker is inserted where injected context was truncated, so agents know
// they are seeing a window rather than silently missing content.
const clipMarker = "\n[... truncated by middle-manager ...]\n"

// Clip bounds a context section to roughly max bytes so one runaway agent
// transcript can't blow the next agent's prompt budget. keepEnd keeps the TAIL
// (agent outputs put their summary last); otherwise the HEAD is kept (the
// error log is newest-first). Cuts land on line boundaries where possible.
func Clip(s string, max int, keepEnd bool) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	if keepEnd {
		cut := s[len(s)-max:]
		if i := strings.IndexByte(cut, '\n'); i >= 0 && i < len(cut)-1 {
			cut = cut[i+1:]
		}
		return clipMarker + cut
	}
	cut := s[:max]
	if i := strings.LastIndexByte(cut, '\n'); i > 0 {
		cut = cut[:i]
	}
	return cut + clipMarker
}

// Context carries everything the step templates can reference. Prompt keys are
// stable (custom prompt files rely on them); new fields add keys, never rename.
type Context struct {
	Repo           string
	Issue          string
	DiscoverOutput string // planner's report, handed to the programmer
	ExecuteOutput  string // programmer's report, handed to the verifier
	AgentMemory    string // repo-owned AGENTS.md / CLAUDE.md (read-only)
	TestOutput     string // previous verifier report ({test_output} for compat)
	ErrorLog       string // newest-first verifier feedback / step errors
	DiffSummary    string // git status + diffstat of the working tree
	Notes          string // orchestrator notes content (cross-run learnings)
	NotesFile      string // absolute path agents may append learnings to
	StateDir       string // this run's state directory (outside the repo)
	Iteration      int
	Mission        string
}

// Per-section clip budgets (bytes). Generous enough to keep full context in the
// common case; tight enough that four huge sections can't sink a prompt.
const (
	clipAgentOutput = 16000
	clipMemory      = 12000
	clipErrorLog    = 16000
	clipNotes       = 8000
	clipDiff        = 8000
)

func BuildContext(c Context) map[string]string {
	mission := c.Mission
	if mission == "" {
		mission = "(no mission prompt — use repo context and issue body)"
	}
	issue := c.Issue
	if issue == "" {
		issue = "none"
	}
	notes := strings.TrimSpace(c.Notes)
	if notes == "" {
		notes = "(none yet)"
	}
	return map[string]string{
		"repo":            c.Repo,
		"issue":           issue,
		"discover_output": Clip(c.DiscoverOutput, clipAgentOutput, true),
		"execute_output":  Clip(c.ExecuteOutput, clipAgentOutput, true),
		"agent_memory":    Clip(c.AgentMemory, clipMemory, false),
		"test_output":     Clip(c.TestOutput, clipAgentOutput, true),
		"error_log":       Clip(c.ErrorLog, clipErrorLog, false),
		"diff_summary":    Clip(c.DiffSummary, clipDiff, false),
		"notes":           Clip(notes, clipNotes, true),
		"notes_file":      c.NotesFile,
		"state_dir":       c.StateDir,
		"iteration":       strconv.Itoa(c.Iteration),
		"mission":         mission,
	}
}
