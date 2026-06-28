![middle-manager logo](Logo.png)

# middle-manager

Micromanaged multi-agent coding loop that orchestrates your favorite coding CLIs.

**Bring your own agents.** middle-manager dynamically chains **Grok**, **Claude Code**, **Crush**, **Agy**, **Codex**, and **OpenCode** into a tight 4-step software factory. It reads your codebase, maps out a task list, executes fixes, critiques its own work, runs tests, commits, and opens PRs—completely on autopilot. *(Agents are auto-detected and configured automatically).*

---

## Install (One-Liner)

* **Pure Python 3.10+** (zero dependencies, no pip requirements)
* **Tmux (Highly Recommended)**: If `tmux` is installed, `middle-manager` automatically runs all agents inside background tmux sessions. This preserves their native pseudo-terminal (PTY) environment—enabling full colored TUIs, spinners, and interactive prompt choices—while keeping the main terminal clean.
* **Install command**:
  ```bash
  curl -fsSL https://raw.githubusercontent.com/bradflaugher/middle-manager/main/install.sh | bash
  ```

This installs `mm` to `~/.local/bin/mm` and clones the repo to `~/.local/share/middle-manager`.

<details>
<summary><b>Manual Installation & PATH Setup</b></summary>

### 1. Manual Installation
If you prefer to install manually without the automatic script:
1. **Clone the repository**:
   ```bash
   git clone https://github.com/bradflaugher/middle-manager.git ~/.local/share/middle-manager
   ```
2. **Create the wrapper executable** at `~/.local/bin/mm`:
   ```bash
   #!/usr/bin/env bash
   set -euo pipefail
   export PYTHONPATH="$HOME/.local/share/middle-manager:${PYTHONPATH:-}"
   exec python3 "$HOME/.local/share/middle-manager/mm.py" "$@"
   ```
3. **Make it executable**:
   ```bash
   chmod +x ~/.local/bin/mm
   ```
4. **Create the configuration directory**:
   ```bash
   mkdir -p ~/.config/middle-manager
   ```

### 2. Adding to PATH (if needed)
Make sure `~/.local/bin` is in your shell's `PATH`. If not, add this to your shell config (e.g., `~/.bashrc` or `~/.zshrc`):
```bash
export PATH="$HOME/.local/bin:$PATH"
```
Then reload your configuration:
```bash
source ~/.bashrc  # or ~/.zshrc
```
</details>

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

```text
  ┌──────────────┐
  │   DISCOVER   │  Compile plan/spec
  └──────────────┘
         │
         ▼
  ┌──────────────┐
  │   EXECUTE    │  Implement one task
  └──────────────┘
         │
         ▼
  ┌──────────────┐
  │    VERIFY    │  Test & critique
  └──────────────┘
         │
         ├─ (Pass) ─► [ COMMIT ] (PR + Memory)
         │
         └─ (Fail) ─► Loop back & retry
```

middle-manager executes steps in the following order:

1. **Discover**: Scans codebase/issues, scopes out tasks, and compiles the `fix_plan.md` list.
2. **Execute**: Implements **exactly one** item from the active task list.
3. **Verify**: Reviews the changes, runs tests, and applies critical backpressure on failure.
4. **Commit**: Saves updates, registers context updates in memory, and submits pull requests (never merges directly).


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
mm init --repo .                       # seed AGENTS.md + .middle-manager/
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

## License

This project is licensed under the [MIT License](LICENSE).

