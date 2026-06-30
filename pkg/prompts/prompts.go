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

## Your job

1. **Scan the codebase** — use ripgrep, file reads, and critical thinking. Do not assume something is implemented just because a search returned nothing.
2. **Determine the scope of changes** — identify files/modules to touch, API requirements, and potential pitfalls.
3. **Do not implement fixes yet** — planning and discovery only.
4. Output a summary of your findings, files to modify, and execution guidelines for the next step.`

const DiscoverFeatureTemplate = `# Feature Scoping — Iteration {iteration}

You are the **Planner** for a single feature request. Stay tight.

## Mission (the feature to build)
{mission}

## Repository
` + "`" + `{repo}` + "`" + `

## Repository memory
{agent_memory}

## Your job

1. **Scope ONLY the mission above.** Do not go on a repo-wide bug hunt.
2. **Determine the scope of changes** — identify files to create or modify.
3. **Do not implement code** — planning and discovery only.
4. Output a summary of your findings, files to modify, and execution guidelines for the next step.`

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

## Previous errors (if any)
{error_log}

## Rules

1. **Minimal correct change.** Match existing code style.
2. Run relevant tests/build commands yourself before finishing.
3. Do not open PRs or merge — the loop handles git.

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

## Test / build output
{test_output}

## Error log
{error_log}

## Repository memory
{agent_memory}

## Evaluation criteria

1. Does the change actually address the mission?
2. Are there obvious bugs, security issues, or style violations?
3. Do tests pass? If not, explain exactly what failed and how to fix it.
4. Cross-check: read the diff and relevant files — do not trust the builder's summary alone.

## Output format

` + "```" + `
VERDICT: PASS | FAIL
SUMMARY: ...
ISSUES:
- ...
` + "```" + `

If FAIL, append concrete fix instructions to ` + "`" + `.middle-manager/error_log.txt` + "`" + ` for the next loop.`

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

## Test output
{test_output}

## Repository memory
{agent_memory}

## Your job

1. Update ` + "`" + `{agent_memory}` + "`" + ` (AGENTS.md or CLAUDE.md) with anything the next loop iteration must remember (build commands, gotchas, conventions discovered).
2. ` + "`" + `git add` + "`" + ` only relevant files. **Do not** ` + "`" + `git push --force` + "`" + `.
3. Commit with message: ` + "`" + `middle-manager: {mission}` + "`" + ` (truncate to 72 chars).

**Do not push and do not open a pull request.** middle-manager pushes the branch, opens the PR, and links the issue (` + "`" + `Closes #{issue_number}` + "`" + `) — all deterministically, right after this step, then merges according to the configured merge policy. Your only job is to land one clean commit (plus the memory update).

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

// LoadPrompt reads custom prompt file if it exists, otherwise returns the default embedded one.
func LoadPrompt(repoPath, name string) string {
	customPath := filepath.Join(repoPath, ".middle-manager", "prompts", name+".md")
	if b, err := os.ReadFile(customPath); err == nil {
		return string(b)
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

func BuildContext(repoPath, issue, discoverOutput, agentMemory, testOutput, errorLog string, iteration int, mission string) map[string]string {
	if mission == "" {
		mission = "(no mission prompt — use repo context and issue body)"
	}
	if issue == "" {
		issue = "none"
	}
	return map[string]string{
		"repo":            repoPath,
		"issue":           issue,
		"discover_output": discoverOutput,
		"agent_memory":    agentMemory,
		"test_output":     testOutput,
		"error_log":       errorLog,
		"iteration":       strconv.Itoa(iteration),
		"mission":         mission,
	}
}
