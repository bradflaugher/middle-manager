![middle-manager logo](Logo.jpg)

# middle-manager

Unsupervised multi-agent coding loop that orchestrates your favorite coding CLIs. **Pure Python 3.10+ — no external requirements or pip dependencies.**

**Bring your own agents.** middle-manager dynamically chains **Grok**, **Claude Code**, **Crush**, **Agy**, **Codex**, and **OpenCode** into a tight 4-step software factory. It reads your codebase, maps out a task list, executes fixes, critiques its own work, runs tests, commits, and opens PRs—completely on autopilot.

---

## Install (One-Liner)

```bash
curl -fsSL https://raw.githubusercontent.com/bradflaugher/middle-manager/main/install.sh | bash
```

This installs `mm` to `~/.local/bin/mm` and clones the repo to `~/.local/share/middle-manager`.

Make sure to add the bin directory to your `PATH`:
```bash
export PATH="$HOME/.local/bin:$PATH"
```

### Quick reference

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

---

## The Loop

```
┌─────────────┐    ┌─────────────┐    ┌─────────────┐    ┌─────────────┐
│  DISCOVER   │───▶│   EXECUTE   │───▶│   VERIFY    │───▶│   COMMIT    │
│  plan/spec  │    │  one item   │    │   critic    │    │ PR + memory │
│   (grok)    │    │  (claude)   │    │  (crush)    │    │   (agy)     │
└─────────────┘    └─────────────┘    └─────────────┘    └─────────────┘
       ▲                                      │
       └──────── tests fail / verifier fail ──┘
```

| Step | Default agent | Job |
|------|---------------|-----|
| 1. Discover | Grok | Scan repo + issues, maintain `fix_plan.md` |
| 2. Execute | Claude Code | Implement **exactly one** plan item |
| 3. Verify | Codex | Critic / backpressure on tests + diff |
| 4. Commit | Agy | Update AGENT.md, commit, push, open PR (**never merge**) |

---

## Cookbook — copy/paste recipes

<details>
<summary><b>Interactive wizard (recommended)</b></summary>

If you prefer interactive prompts instead of specifying CLI flags, run `mm` with no arguments. It will walk you through setting up your loop step-by-step:

```bash
mm                    # walks through repo → mode → mission → agents → start
mm --wizard           # force the wizard even if other flags are provided
```

Your last chosen configuration options are saved to `~/.config/middle-manager/last.json` to make running subsequent loops extremely fast.
</details>

<details>
<summary><b>Add a feature fast (most common)</b></summary>

Three autodetected agents, your prompt, fresh state every run:

```bash
cd ~/your-project

mm quick "add dark mode toggle to settings"
mm "add Stripe checkout to the pricing page"     # shorthand — identical
mm quick "add user avatars" --dry-run            # preview agent commands first
```

What happens: discover scopes the feature into subtasks → execute implements one item per loop → verify audits + runs tests. State resets each quick run. No wizard, no config file.

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
</details>

<details>
<summary><b>Chug through GitHub issues</b></summary>

Requires `gh` authenticated in the repo (`gh auth login`).

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
</details>

<details>
<summary><b>Single GitHub issue</b></summary>

```bash
mm --issue 42
mm --issue 42 --mission "fix without refactoring anything else"
mm --issue https://github.com/you/repo/issues/42 --steps 4
```
</details>

<details>
<summary><b>Fix whatever's broken (no specific feature)</b></summary>

Repo-wide discovery — finds failing tests, doc drift, missing CI, etc.:

```bash
mm --mode repair
mm --mode repair --mission "focus on Playwright failures only"
mm --mode repair --test-command "npm run test:ci" --max-iterations 5
```
</details>

<details>
<summary><b>Inspect before you YOLO</b></summary>

```bash
mm agents                              # what's installed on this machine
mm init --repo .                       # seed AGENT.md + .middle-manager/
mm status --repo .                     # fix_plan, logs, iteration count
mm quick "add feature X" --dry-run     # print agent commands, run nothing
```
</details>

<details>
<summary><b>Per-step configuration</b></summary>

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

#### Example: only grok installed (no claude/codex)

```bash
mm quick "add resume link to index.html" \
  --repo ~/bradflaugher.com \
  --discover-agent grok \
  --execute-agent grok \
  --verify-agent grok \
  --test-command "npm test"
```
</details>

---

## Agent YOLO flags

middle-manager passes the right permission-skipping flag per CLI when `--yolo` is on (default):

| Agent | Binary | YOLO flag | Headless invocation |
|-------|--------|-----------|---------------------|
| **[Grok](https://docs.x.ai/docs/grok-cli)** | `grok` | `--yolo` (alias: `--always-approve`) | `grok -p PROMPT --yolo --cwd DIR` |
| **[Claude Code](https://code.claude.com)** | `claude` | `--dangerously-skip-permissions` | `claude -p PROMPT --dangerously-skip-permissions` |
| **[Codex](https://developers.openai.com/codex/cli)** | `codex` | `--yolo` | `codex exec PROMPT --yolo` |
| **[Crush](https://github.com/charmbracelet/crush)** | `crush` | None | `crush run PROMPT -c DIR` |
| **[OpenCode](https://opencode.ai)** | `opencode` | `--dangerously-skip-permissions` | `opencode run PROMPT --dangerously-skip-permissions --dir DIR` |
| **[Agy](https://antigravity.google/docs/cli-install)** | `agy` | `--dangerously-skip-permissions` | `agy --print PROMPT --dangerously-skip-permissions` |

Not all agents are installed on every box. `mm agents` shows what you have. Override with `--binary claude=/path/to/claude`.

---

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

---

MIT. No warranty.
