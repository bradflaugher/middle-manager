# middle-manager

A multi-agent coding loop that probably should be fired.

**If you are a normal human:** close this tab and use [Claude Code](https://code.claude.com). Seriously. One agent. One subscription. It works. Leave Brad alone.

**If you are a true nerd / CTO / agent polygamist:** welcome. This is a pure-Python (stdlib only), configurable, YOLO-bash-driven loop that chains modern coding CLIs through a 3- or 4-step pipeline to chew through GitHub issues or auto-discover and repair codebase problems.

Inspired by the Ralph Wiggum "pipe prompts into a while loop" technique, Open SWE's planner/builder split, and the Karpathy "verify before you ship" backpressure method — but turned up to 11.

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
| 1. Discover | `grok` | Scan repo + issues, maintain `fix_plan.md` |
| 2. Execute | `claude` | Implement **exactly one** plan item |
| 3. Verify | `codex` | Critic / backpressure on tests + diff |
| 4. Commit | `agy` | Update AGENT.md, commit, push, open PR (**never merge**) |

Use `--steps 3` to skip the commit agent (git steps run inline instead).

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

## Quick start

**Interactive (recommended):** just run `mm` — it walks you through repo, agents, mission prompt, and mode.

```bash
mm                           # interactive wizard → loop
mm agents                    # see what's installed
mm init --repo ~/your-project
mm --repo ~/your-project --dry-run
mm --repo ~/your-project --issue 42
mm --label bug --author @dependabot --close-issues   # issue queue mode
```

Non-interactive:

```bash
python mm.py --repo ~/your-project --mission "fix all the maps tests" --dry-run
python mm.py issues --repo ~/project --label enhancement --close-issues
```

State lives in `<repo>/.middle-manager/` (`fix_plan.md`, logs, iteration counter). Issue queue state is per-issue under `.middle-manager/issues/<number>/`.

## Interactive wizard

Running `mm` with no arguments starts the wizard:

1. **Repository** — path to your git repo (defaults to cwd)
2. **Mode** — codebase repair, single issue, or filtered issue queue
3. **Mission prompt** — free-text goals injected into every agent step
4. **Agents** — autodetected from what's on your PATH; customize per step if you want
5. **Options** — 3/4 steps, YOLO, dry-run, test command, PRs, close issues

Last choices are saved to `~/.config/middle-manager/last.json`.

## Issue queue (batch mode)

Drain open GitHub issues matching a filter, one at a time:

```bash
mm --repo ~/myapp --label bug --author @someuser --close-issues
mm issues --repo ~/myapp --label "good first issue" --issue-limit 10
```

Or pick **queue** in the interactive wizard. For each issue middle-manager:

1. Checks out `mm/issue-<number>`
2. Seeds a per-issue `fix_plan.md` from the issue body
3. Runs the full discover → execute → verify → commit loop
4. Closes the issue on success (unless `--no-close-issues`)

Requires `gh` CLI authenticated against the repo.

## Agent YOLO flags

middle-manager passes the right permission-skipping flag per CLI when `--yolo` is on (default):

| Agent | Binary | YOLO flag | Headless invocation |
|-------|--------|-----------|---------------------|
| **grok** | `grok` | `--yolo` (alias: `--always-approve`) | `grok -p PROMPT --yolo --cwd DIR` |
| **claude** | `claude` | `--dangerously-skip-permissions` | `claude -p PROMPT --dangerously-skip-permissions` |
| **codex** | `codex` | `--yolo` | `codex exec PROMPT --yolo` |
| **crush** | `crush` | `-y` / `--yolo` (global, before `run`) | `crush -y run PROMPT -c DIR` |
| **opencode** | `opencode` | `--dangerously-skip-permissions` | `opencode run PROMPT --dangerously-skip-permissions --dir DIR` |
| **agy** | `agy` | `--dangerously-skip-permissions` | `agy --print PROMPT --dangerously-skip-permissions` |

Not all agents are installed on every box. That's fine — `python mm.py agents` shows what's available. Override binaries with `--binary claude=/path/to/claude`.

## Per-step configuration

Override agents, models, and extra CLI args per step:

```bash
python mm.py --repo ~/bradflaugher.com \
  --discover-agent grok --discover-model grok-3 \
  --execute-agent claude --execute-model claude-sonnet-4-20250514 \
  --verify-agent grok --verify-args "--check,--effort,high" \
  --commit-agent agy \
  --test-command "npm test" \
  --max-iterations 5
```

Or use a JSON config:

```bash
python mm.py --config my-loop.json --repo ~/project
```

See `config.default.json` for the full schema.

### Example: bradflaugher.com on this box

Grok + Crush + Agy are installed here; Claude and Codex are not. A workable local config:

```bash
python mm.py --repo ~/bradflaugher.com \
  --discover-agent grok \
  --execute-agent grok \
  --verify-agent grok --verify-args "--check" \
  --commit-agent agy \
  --test-command "npm test" \
  --dry-run
```

Drop `--dry-run` when you're ready to let the agents actually run.

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
| `python mm.py` | Run the loop (default) |
| `python mm.py agents` | Show installed agents + YOLO flags |
| `python mm.py init --repo PATH` | Seed `.middle-manager/` and AGENT.md |
| `python mm.py status --repo PATH` | Show loop state |

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
  interactive.py # CTO sanity pause button
  prompts/       # discover, execute, verify, commit
mm.py            # entry point
config.default.json
```

## For the love of god

Most people should use Claude. This repo exists because some of us run Grok for discovery, Claude for building, Codex for verification, Crush because it's glamorous, OpenCode because it's hip, and Agy because why not — all in one unhinged bash loop.

If that sentence made you tired: **use Claude**. We're not mad. We're just tired too.

---

MIT. PRs welcome but Brad might merge them with another agent loop out of spite.