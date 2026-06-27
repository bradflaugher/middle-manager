# ⚡️ middle-manager

Unsupervised multi-agent coding loop that orchestrates your favorite coding CLIs. 

**Bring your own agents.** `middle-manager` dynamically chains **Grok**, **Claude Code**, **Crush**, **Agy**, **Codex**, and **OpenCode** into a tight 4-step software factory. It reads your codebase, maps out a task list, executes fixes, critiques its own work, runs tests, commits, and opens PRs—completely on autopilot.

---

## 🚀 Key Features

- **Dynamic Agent Picking**: No configuration boilerplate. It automatically scans your system (`grok`, `crush`, `agy`, etc.) and routes each step of the pipeline to the best installed tool.
- **Critic Backpressure**: Real verifier logic. It parses the critique, writes feedback loops, and injects new corrective tasks into the plan on failure, preventing buggy commits.
- **Git Native**: Autonomously branches, stages, commits, and opens pull requests (but never merges without human approval).
- **Zero Dependencies**: Pure Python standard library. Fast, lightweight, and sandbox-friendly.

---

## 📸 Terminal Preview

Here is what it looks like when running:

```ansi
$ mm "add stripe checkout to pricing page"

Target repo: /root/my-project
Steps: 3 (discover, execute, verify)
YOLO: True

=== Iteration 1 ===
▶ grok: grok -p PROMPT --yolo --cwd /root/my-project --check
discover finished.

Current Plan:
  [ ] Install and configure stripe SDK
  [ ] Create checkout endpoint in api/checkout.py
  [ ] Add payment button to billing UI

▶ grok: grok -p PROMPT --yolo --cwd /root/my-project
execute finished.

▶ grok: grok -p PROMPT --yolo --cwd /root/my-project
verify finished.
Verifier verdict: PASS

✓ Tests passed.
✓ Committed changes: middle-manager: Install and configure stripe SDK.
```

---

## 📦 Install (One-Liner)

```bash
curl -fsSL https://raw.githubusercontent.com/bradflaugher/middle-manager/main/install.sh | bash
```

This installs `mm` to `~/.local/bin/mm` and clones the repo to `~/.local/share/middle-manager`.

Make sure to add the bin directory to your `PATH`:
```bash
export PATH="$HOME/.local/bin:$PATH"
```

---

## 🕹️ Quick Start

### 1. Build a new feature fast
Point the manager at your repo, tell it what to build, and let it rip:
```bash
cd ~/my-project
mm "add a dark mode toggle to the settings screen"
```
*What happens:* It autodetects your installed CLIs, plans the feature in `.middle-manager/fix_plan.md`, implements one subtask at a time, runs tests, commits, and loops until complete.

### 2. General codebase repair
Let it hunt down bugs, failing tests, and outdated code:
```bash
mm --mode repair --test-command "npm test"
```

### 3. Burn down your GitHub issues
Provide issue labels/authors, and it will batch-checkout branch-by-branch, resolve the issue, and open PRs:
```bash
mm --author @dependabot --close-issues --issue-limit 5
```

---

## 🔄 The Loop

```
┌─────────────┐    ┌─────────────┐    ┌─────────────┐    ┌─────────────┐
│  DISCOVER   │───▶│   EXECUTE   │───▶│   VERIFY    │───▶│   COMMIT    │
│  plan/spec  │    │  one item   │    │   critic    │    │ PR + memory │
│   (grok)    │    │  (claude)   │    │  (crush)    │    │   (agy)     │
└─────────────┘    └─────────────┘    └─────────────┘    └─────────────┘
       ▲                                      │
       └──────── tests fail / verifier fail ──┘
```

| Step | Job | Default Priority |
| :--- | :--- | :--- |
| **1. Discover** | Scans repo + issues, maintains `fix_plan.md` task checklist | `grok` → `claude` → `crush` → `opencode` → `agy` |
| **2. Execute** | Implements exactly one task from `fix_plan.md` | `claude` → `grok` → `opencode` → `crush` → `agy` |
| **3. Verify** | Audits diff, checks tests, outputs `VERDICT: PASS/FAIL` | `codex` → `grok` → `claude` → `opencode` → `crush` |
| **4. Commit** | Updates memory files (`AGENT.md`), commits, pushes, opens PR | `agy` → `grok` → `claude` → `opencode` → `crush` |

---

## 🛠️ Commands

| Command | Description |
| :--- | :--- |
| `mm` | Starts the interactive configuration wizard |
| `mm quick "..."` | Starts a 3-agent feature loop (discover → execute → verify) |
| `mm "..."` | Shorthand for `mm quick "..."` |
| `mm status` | Shows current checklist, progress, and logs |
| `mm agents` | Lists detected agents, binary locations, and status |
| `mm init` | Seeds `AGENT.md` memory file and state directories |

---

## 🎛️ Advanced Customization

### Override Agents & Models
```bash
mm --discover-agent grok --discover-model grok-3 \
   --execute-agent claude --execute-model claude-3-5-sonnet \
   --test-command "pytest"
```

### Run with a JSON config
Save your settings and run them easily:
```bash
mm --config examples/quick-feature.json
```

---

## 📜 Rules of the Road

1. **Keep tasks small**: The cleaner the steps, the higher the success rate.
2. **Review before merging**: The commit agent creates PRs; you decide when they land.
3. **Write repository memory**: Maintain `AGENT.md` at your repo root. This is the persistent long-term memory where agents check for conventions, build commands, and rules.
4. **`fix_plan.md` is the source of truth**: The loop reads the top `- [ ]` task from this checklist.
5. **Tests are backpressure**: Test failures are automatically captured and fed back to the discovery phase.

---

## 🧱 Architecture

```
middle_manager/
  agents.py      # CLI command builders and YOLO settings
  cli.py         # Entry point and subcommand routing
  colors.py      # ANSI terminal formatting
  config.py      # Argument parsing, agent autodetection, and defaults
  git_ops.py     # Subprocess-based git and gh helpers
  interactive.py # Interactive pause menu handler (-i)
  issue_queue.py # Batch processor for GitHub issues
  loop.py        # Pipeline workflow engine
  prompts/       # Prompt templates (discover, execute, verify, commit)
mm.py            # Executable runner script
```

---

MIT. No warranty. Use with `--yolo` at your own discretion.
