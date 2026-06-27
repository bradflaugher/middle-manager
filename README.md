# middle-manager

Unsupervised multi-agent coding loop. YOLO on by default.

**Not for you.** Use [Claude Code](https://code.claude.com). One agent. Stop reading.

**Agent polygamists:** pure Python, no dependencies, chains [Grok](https://docs.x.ai/docs/grok-cli) / [Claude Code](https://code.claude.com) / [Codex](https://developers.openai.com/codex/cli) / [Crush](https://github.com/charmbracelet/crush) / [OpenCode](https://opencode.ai) / [Agy](https://antigravity.google/docs/cli-install) through a 4-step pipeline. It edits your repo, runs shell commands without permission prompts, pushes branches, opens PRs, and closes GitHub issues. You point it at a mission and leave.

```bash
mm quick "add feature XYZ"          # 3 agents, your prompt, no hand-holding
mm --author @someuser --close-issues   # drain their issue queue, auto-close on success
```

Specialized roles, tight loops, unsupervised execution. Plan, build, verify, ship — repeat until done or something breaks.

## The loop

```
┌─────────────┐    ┌─────────────┐    ┌─────────────┐    ┌─────────────┐
│  DISCOVER   │───▶│   EXECUTE   │───▶│   VERIFY    │───▶│   COMMIT    │
│  plan/spec  │    │  one item   │    │   critic    │    │ PR + memory │
│  (grok)     │    │  (claude)   │    │  (codex)    │    │   (agy)     │
└─────────────┘    └─────────────┘    └─────────────┘    └─────────────┘
       ▲                                      │
       └──────── tests fail / new issues ─────┘
```

| Step | Default agent | Job |
|------|---------------|-----|
| 1. Discover | [Grok](https://docs.x.ai/docs/grok-cli) | Scan repo + issues, maintain `fix_plan.md` |
| 2. Execute | [Claude Code](https://code.claude.com) | Implement **exactly one** plan item |
| 3. Verify | [Codex](https://developers.openai.com/codex/cli) | Critic / backpressure on tests + diff |
| 4. Commit | [Agy](https://antigravity.google/docs/cli-install) | Update AGENT.md, commit, push, open PR (**never merge**) |

Use `--steps 3` to skip the commit agent (git steps run inline instead).

**Supported CLIs:** [Grok](https://docs.x.ai/docs/grok-cli) · [Claude Code](https://code.claude.com) · [Codex](https://developers.openai.com/codex/cli) · [Crush](https://github.com/charmbracelet/crush) · [OpenCode](https://opencode.ai) · [Agy](https://antigravity.google/docs/cli-install) (Google Antigravity)

## Install (one-liner)

```bash
curl -fsSL https://raw.githubusercontent.com/bradflaugher/middle-manager/main/install.sh | bash
```

Then run `mm` from anywhere. The installer puts it in `~/.local/bin/mm` and clones to `~/.local/share/middle-manager`.

Add to PATH if needed:

```bash
export PATH="$HOME/.local/bin:$PATH"
# or: mm install-path
```

## Cookbook — copy/paste recipes

### Add a feature fast (most common)

Three autodetected agents, your prompt, fresh state every run:

```bash
cd ~/your-project

mm quick "add dark mode toggle to settings"
mm "add Stripe checkout to the pricing page"     # shorthand — identical
mm quick "add user avatars" --dry-run            # preview agent commands first
```

What happens: **discover** scopes the feature into subtasks → **execute** implements one item per loop → **verify** audits + runs tests. State resets each `quick` run. No wizard, no config file.

```bash
# same thing, explicit flags
mm --steps 3 --mode feature --fresh -m "add OAuth login" --no-wizard

# pick your own agents (when you have them installed)
mm quick "add webhook handler" \
  --discover-agent grok \
  --execute-agent claude \
  --verify-agent codex \
  --test-command "npm test"
```

More feature examples:

```bash
mm quick "add /health endpoint that returns JSON status"
mm quick "add pagination to the search results page"
mm quick "add email validation to the signup form"
mm quick "refactor auth middleware to use JWT" --max-iterations 8
mm quick "add Playwright test for the checkout flow" --test-command "npx playwright test"
```

### Chug through GitHub issues

Requires [`gh`](https://cli.github.com/) authenticated in the repo (`gh auth login`).

**By submitter** — burn down everything `@someuser` filed:

```bash
cd ~/your-project

mm --author @someuser --close-issues
mm issues --repo . --author dependabot --close-issues --issue-limit 20
mm --author @bradflaugher --close-issues --no-pr    # fix + close, skip PR step
```

**By label:**

```bash
mm --label bug --close-issues
mm --label "good first issue" --issue-limit 10 --close-issues
mm --label enhancement --author @intern --close-issues
```

**Label + author combo** — e.g. dependabot bugs only:

```bash
mm --label bug --author dependabot --close-issues --issue-limit 50
mm issues --repo ~/myapp --label security --author @renovate-bot --close-issues
```

**Don't auto-close** — open PRs but leave issues open for human review:

```bash
mm --author @someuser --no-close-issues
mm --label bug --no-close-issues --steps 4   # full loop with commit agent + PR
```

**Preview the queue** without running agents:

```bash
gh issue list --author @someuser --state open --json number,title
gh issue list --label bug --author dependabot --state open
mm --author @someuser --dry-run --issue-limit 3
```

For each issue, middle-manager:

1. Checks out `mm/issue-<number>`
2. Seeds `fix_plan.md` from the issue body
3. Runs discover → execute → verify → (commit)
4. Closes the issue on success (unless `--no-close-issues`)

Per-issue state: `.middle-manager/issues/<number>/`

### Single GitHub issue

```bash
mm --issue 42
mm --issue 42 --mission "fix without refactoring anything else"
mm --issue https://github.com/you/repo/issues/42 --steps 4
```

### Fix whatever's broken (no specific feature)

Repo-wide discovery — finds failing tests, doc drift, missing CI, etc.:

```bash
mm --mode repair
mm --mode repair --mission "focus on Playwright failures only"
mm --mode repair --test-command "npm run test:ci" --max-iterations 5
```

### Interactive wizard

When you want prompts instead of flags:

```bash
mm                    # walks through repo → mode → mission → agents → go
mm --wizard           # force wizard even with other flags
```

Wizard defaults to **feature** mode + 3-step stack. Last choices saved to `~/.config/middle-manager/last.json`.

### Inspect before you YOLO

```bash
mm agents                              # what's installed on this machine
mm init --repo .                       # seed AGENT.md + .middle-manager/
mm status --repo .                     # fix_plan, logs, iteration count
mm quick "add feature X" --dry-run     # print agent commands, run nothing
```

## Quick reference

| I want to… | Command |
|------------|---------|
| Add a feature | `mm quick "add feature XYZ"` |
| Shorthand feature | `mm "add feature XYZ"` |
| One GitHub issue | `mm --issue 42` |
| All issues by user | `mm --author @someuser --close-issues` |
| All bugs by user | `mm --label bug --author @someuser --close-issues` |
| Good-first-issues sprint | `mm --label "good first issue" --issue-limit 10 --close-issues` |
| Fix the codebase generally | `mm --mode repair` |
| Point at another repo | `mm quick "…" --repo ~/other-project` |
| Pause between steps | `mm quick "…" -i` |
| Use a config file | `mm --config examples/quick-feature.json --repo .` |

State lives in `<repo>/.middle-manager/`. Issue queue state is per-issue under `.middle-manager/issues/<number>/`.

## Agent YOLO flags

middle-manager passes the right permission-skipping flag per CLI when `--yolo` is on (default):

| Agent | Binary | YOLO flag | Headless invocation |
|-------|--------|-----------|---------------------|
| **[Grok](https://docs.x.ai/docs/grok-cli)** | `grok` | `--yolo` (alias: `--always-approve`) | `grok -p PROMPT --yolo --cwd DIR` |
| **[Claude Code](https://code.claude.com)** | `claude` | `--dangerously-skip-permissions` | `claude -p PROMPT --dangerously-skip-permissions` |
| **[Codex](https://developers.openai.com/codex/cli)** | `codex` | `--yolo` | `codex exec PROMPT --yolo` |
| **[Crush](https://github.com/charmbracelet/crush)** | `crush` | `-y` / `--yolo` (global, before `run`) | `crush -y run PROMPT -c DIR` |
| **[OpenCode](https://opencode.ai)** | `opencode` | `--dangerously-skip-permissions` | `opencode run PROMPT --dangerously-skip-permissions --dir DIR` |
| **[Agy](https://antigravity.google/docs/cli-install)** | `agy` | `--dangerously-skip-permissions` | `agy --print PROMPT --dangerously-skip-permissions` |

Not all agents are installed on every box. `mm agents` shows what you have. Override with `--binary claude=/path/to/claude`.

## Per-step configuration

Override agents, models, and extra CLI args per step:

```bash
mm --repo ~/bradflaugher.com \
  --discover-agent grok --discover-model grok-3 \
  --execute-agent claude --execute-model claude-sonnet-4-20250514 \
  --verify-agent grok --verify-args "--check,--effort,high" \
  --commit-agent agy \
  --test-command "npm test" \
  --max-iterations 5
```

Or use a JSON config:

```bash
mm --config examples/quick-feature.json --repo ~/project
mm --config examples/bradflaugher.com.json --repo ~/bradflaugher.com --dry-run
```

See `config.default.json` for the full schema.

### Example: only grok installed (no claude/codex)

```bash
mm quick "add resume link to index.html" \
  --repo ~/bradflaugher.com \
  --discover-agent grok \
  --execute-agent grok \
  --verify-agent grok \
  --test-command "npm test"
```

## Interactive mode

`-i` / `--interactive` pauses before each step:

```
middle-manager> c    # continue
middle-manager> s    # skip step
middle-manager> a    # list agent availability
middle-manager> p    # print step config
middle-manager> q    # quit
```

## Commands

| Command | Description |
|---------|-------------|
| `mm` | Interactive wizard → loop |
| `mm quick "…"` | 3-agent feature preset |
| `mm "…"` | Shorthand for `mm quick "…"` |
| `mm agents` | Show installed agents + YOLO flags |
| `mm init --repo PATH` | Seed `.middle-manager/` and AGENT.md |
| `mm status --repo PATH` | Show loop state |
| `mm issues --author @user` | Issue queue batch mode |
| `mm install-path` | Print PATH export for installer |

## Rules of the road

1. **One item per loop iteration.** Cramming the context window makes everything worse.
2. **Don't merge PRs.** The commit step opens PRs; humans merge (or don't).
3. **Maintain AGENT.md.** Agents are ghosts — repo memory is how they learn.
4. **fix_plan.md is the source of truth.** Discover writes it; execute reads the top `- [ ]` item.
5. **Tests are backpressure.** `test_command` runs after verify; failures feed the next discover pass.

## Architecture

Pure Python 3.10+. No pip dependencies. Subprocesses to agent CLIs. Prompt templates in `middle_manager/prompts/`.

```
middle_manager/
  agents.py      # CLI command builders per agent
  config.py      # defaults + argparse
  loop.py        # the while-loop
  git_ops.py     # git/gh helpers
  interactive.py # pause before each step (-i)
  prompts/       # discover, execute, verify, commit
mm.py            # entry point
config.default.json
```

## Not for you

One agent is the correct number of agents. This repo is for people who run [Grok](https://docs.x.ai/docs/grok-cli) to plan, [Claude](https://code.claude.com) to build, [Codex](https://developers.openai.com/codex/cli) to verify, and [Crush](https://github.com/charmbracelet/crush) / [OpenCode](https://opencode.ai) / [Agy](https://antigravity.google/docs/cli-install) because they have a problem.

You enabled `--yolo`. That was your choice.

---

MIT. No warranty.
