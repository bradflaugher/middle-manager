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

### Quick Start (Wizard)

To run the interactive wizard and configure your loop step-by-step:
```bash
mm
```

---

### Advanced CLI Usage (Quick Reference)

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

## License

This project is licensed under the [MIT License](LICENSE).

