![middle-manager logo](Logo.png)

# middle-manager

Micromanaged multi-agent coding loop that orchestrates your favorite coding CLIs.

**Bring your own agents.** middle-manager dynamically chains **Grok**, **Claude Code**, **OpenCode**, **OpenAI Codex**, and **Google Antigravity (agy)** into a tight 4-step software factory. It reads your codebase, scopes out requirements, executes fixes, critiques its own work, runs tests, commits, and opens PRs—completely on autopilot. *(Agents are auto-detected and configured automatically).*

---

## Install (One-Liner)

* **Go 1.25.0+** (requires Go compiler to compile the binary)
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
2. **Build and install the binary**:
   ```bash
   cd ~/.local/share/middle-manager
   go build -o ~/.local/bin/mm main.go
   ```
3. **Create the configuration directory**:
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

## Supported Agents

The loop dynamically resolves, configures, and orchestrates any installed coding agents on your `PATH`. The following agents are supported out of the box:

* **Grok**: Connected natively via stdio.
* **Claude Code**: Connected via the official `@agentclientprotocol/claude-agent-acp` adapter.
* **OpenCode**: Connected natively via stdio.
* **OpenAI Codex**: Connected via the community **`acp-adapter`** gateway (`acp-adapter --adapter codex`).
* **Google Antigravity (agy)**: Connected via the community Rust-based **`agy-acp`** adapter.

You can inspect the availability of your installed agents at any time by running:
```bash
mm agents
```

---

## Planless Loop Architecture

Starting in version 2.0, `middle-manager` operates under a **Planless** architecture. 
Rather than generating and writing task lists (`fix_plan.md`) to disk (which pollutes the working repository and causes gitignore conflicts), agents scope the necessary changes in memory, write guidelines to `discover_output.txt` (stored in your `.middle-manager` state directory), and execute/verify changes dynamically.

This streamlines loop execution, reduces latency, and allows simple code tasks and issue queue tickets to complete in **exactly one iteration**.

---

## Advanced CLI Usage (Quick Reference)

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
  │   DISCOVER   │  Grok repo requirements & compile scoping guidelines
  └──────────────┘
         │
         ▼
  ┌──────────────┐
  │   EXECUTE    │  Implement the changes
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

1. **Discover**: Scans codebase and active issues, determines the bounds and scope of changes, and writes implementation guidelines.
2. **Execute**: Implements the changes in the target workspace.
3. **Verify**: Reviews the changes, runs tests, and applies critical backpressure on failure.
4. **Commit**: Saves updates, registers context updates in repository memory (`AGENTS.md`), and submits pull requests (never merges directly).

---

## License

This project is licensed under the [MIT License](LICENSE).

