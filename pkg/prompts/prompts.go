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

## Test output
{test_output}

## Repository memory
{agent_memory}

## Your job

1. Update ` + "`" + `{agent_memory}` + "`" + ` (AGENTS.md or CLAUDE.md) with anything the next loop iteration must remember (build commands, gotchas, conventions discovered).
2. ` + "`" + `git add` + "`" + ` only relevant files. **Do not** ` + "`" + `git push --force` + "`" + ` or merge PRs.
3. Commit with message: ` + "`" + `middle-manager: {mission}` + "`" + ` (truncate to 72 chars).
4. Push the current branch to origin.
5. Create a PR with ` + "`" + `gh pr create` + "`" + ` if ` + "`" + `gh` + "`" + ` is available. Link issue {issue_number} if set.
6. **Never merge the PR.** Human review required.

If there is nothing to commit, say so and exit cleanly.`

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
