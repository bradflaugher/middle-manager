![middle-manager logo](./docs/interface_demo.gif)

# middle-manager

Micromanaged multi-agent coding loop that orchestrates your favorite coding CLIs.

**Bring your own agents.** middle-manager dynamically chains **Grok**, **Claude Code**, **OpenCode**, **OpenAI Codex**, **Google Antigravity (agy)**, and **Charm Crush** into a tight 4-step software factory. It reads your codebase, scopes out requirements, executes fixes, critiques its own work, runs tests, commits, and opens PRs—completely on autopilot. *(Agents are auto-detected and configured automatically).*

Each agent runs as its own CLI in plain headless mode, so it uses whatever login that tool already has—OAuth session or API key—with **no extra keys or adapters to configure**. And because it's *micromanaged*, you can watch every step live and steer it mid-run.

---

## But Why?

**Get the most out of every model — the ones you pay for and the ones you run for free.** Got a subscription you want to burn down, or a local model sitting idle? Put it on the grunt work in a loop and have a *better* model check it. middle-manager lets you assign a different model to each step and run them in the order that fits your budget and trust:

- A **local or open-source model executes** for free, and a **stronger frontier model verifies** its work.
- A **big, expensive model does only the planning and execution**, while cheaper agents handle the rest.
- Whatever split you want — each step is just a coding CLI pointed at a model, dropped into the **discover → execute → verify → commit** order. The right model in the right seat.

You set it up by configuring each coding agent to use the model you want, then assigning those agents to steps.

**Closing issues is deterministic, not babysat.** Draining a queue, opening PRs, and closing issues runs as fixed, scripted logic — you're not paying an agent to sit and watch a queue. That's faster and cheaper than asking an LLM to mind the lifecycle.

---

## Install (One-Liner)

```bash
curl -fsSL https://raw.githubusercontent.com/bradflaugher/middle-manager/main/install.sh | bash
```

The installer downloads a **prebuilt binary** for your platform from the latest
[GitHub Release](https://github.com/bradflaugher/middle-manager/releases) — **no Go toolchain required**.
If no prebuilt binary is available it falls back to building from source (which
*does* need Go 1.25+). It installs `mm` to `~/.local/bin/mm`.

`mm` shells out to whichever agent CLIs you have installed (`grok`, `claude`,
`opencode`, `codex`, `agy`, `crush`) and to `git`/`gh` — install the ones you want.

<details>
<summary><b>Other ways to install</b></summary>

### Download a binary directly (no Go)
Grab the asset for your OS/arch from the
[Releases page](https://github.com/bradflaugher/middle-manager/releases), then:
```bash
chmod +x mm_linux_amd64 && mv mm_linux_amd64 ~/.local/bin/mm
```

### Build from source (needs Go 1.25+)
Install Go from [go.dev/doc/install](https://go.dev/doc/install), then:
```bash
git clone https://github.com/bradflaugher/middle-manager.git
cd middle-manager
go build -o ~/.local/bin/mm .
```

### Cut a release (maintainers)
Releases (and their prebuilt binaries) are produced by the `Release` GitHub
Action when you push a version tag:
```bash
git tag v0.2.0 && git push origin v0.2.0
```

### PATH
Make sure `~/.local/bin` is on your `PATH`:
```bash
export PATH="$HOME/.local/bin:$PATH"   # add to ~/.bashrc or ~/.zshrc
```
</details>

### Quick Start (Wizard)

To run the interactive wizard and configure your loop step-by-step:
```bash
mm
```


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
| **Solo:** one agent does it all, wait for the PR to merge | `mm --issue 42 --solo` |
| **Solo queue:** drain issues one merged PR at a time | `mm --label bug --solo --close-issues` |
| **Worktree:** drain a queue into ONE mega PR | `mm --label bug --worktree --close-issues` |
| Roll a **random** installed agent each iteration | `mm "…" --execute-agent random` |
| **Cheap agent first, escalate on failure** | `mm --issue 42 --execute-agent opencode --execute-escalate "claude:opus"` |
| Different agent audits the work | `mm --issue 42 --distinct-verifier` |
| Bound one agent invocation (minutes) | `mm "…" --step-timeout 30` (per-step: `--execute-timeout 90`) |
| Bound the whole run (minutes) | `mm --label bug --max-wall-minutes 120` |
| Point at another repo | `mm quick "…" --repo ~/other-project` |
| Pause between steps | `mm quick "…" -i` |

State lives **outside your repo** in `~/.local/state/middle-manager/<repo>-<hash>/`
(respects `$XDG_STATE_HOME`; override with `--state-dir`), so mm never touches your
`.gitignore` and an agent's `git add -A` can never sweep orchestrator state into a
commit. Issue queue state is per-issue under `issues/<number>/` inside that dir.
Cross-run learnings accumulate in `notes.md` there (override with `--notes-file`)
and are injected into every agent prompt — mm never writes to your `AGENTS.md`.
Custom prompt overrides are read from `<state-dir>/prompts/*.md` or, if you prefer
to commit them, `<repo>/.middle-manager/prompts/*.md`.

---

## The Software Factory: mixing big and small agents

middle-manager is built around the routing pattern the multi-agent literature
keeps converging on: **try the cheap configuration first, escalate only when a
quality check fails** (reported 45–85% cost savings at ~95% retained quality).

### Escalation ladders

Give any step an ordered ladder of `agent[:model]` rungs. The step starts on
its base agent; after every `--escalate-after N` failed iterations (default 1
— a *failed iteration* means the verifier said FAIL, or failed closed) it
climbs one rung:

```bash
# opencode tries first; claude-on-opus takes over if it verifiably fails; codex is the last resort
mm --issue 42 --execute-agent opencode --execute-escalate "claude:opus,codex"
```

Ladders work on every step (`--discover-escalate`, `--verify-escalate`, …) and
on the solo agent (solo shares the execute slot). Escalation is keyed on
**verified failure, not vibes**: every retry ships the verifier's concrete
findings to the next attempt, and the stall detector (identical diff + identical
feedback) now **forces the next rung instead of giving up** while ladder
headroom remains — retrying identically is the one guaranteed waste of tokens.

In JSON config, ladders take strings or objects:

```json
{ "execute": { "agent": "opencode",
               "escalate": ["claude:opus", {"agent": "codex", "model": "gpt-5"}] } }
```

### Independent verifier

`--distinct-verifier` guarantees the verify step runs on a **different agent**
than the one that wrote the code (with `random` steps the verifier gets its own
roll). A fresh process on a different model is the cheapest known defense
against self-review rubber-stamping. The verifier is also prompted
adversarially and must list what it actually checked — a bare "PASS" with no
evidence reads as suspicious.

### Bring ANY agent (custom agent definitions)

The built-in roster is just a starting point. Declare any headless coding CLI
in your persistent config at `~/.config/middle-manager/config.json` (loaded on
every run, then overlaid by `--config` and CLI flags):

```json
{
  "agents": {
    "aider": {
      "binary": "aider",
      "print_flag": "--message",
      "yolo_flags": ["--yes-always"],
      "model_flag": "--model",
      "notes": "aider --message PROMPT --yes-always"
    }
  }
}
```

Custom agents appear in `mm agents`, the wizard's pickers, `random` rolls, and
escalation ladders exactly like built-ins. Redefining a built-in name is
allowed on purpose — it lets you fix a flag mismatch the day an upstream CLI
changes, without waiting for an mm release.

### Robustness: timeouts, retries, budgets

- **Per-step timeout** (`--step-timeout <min>`, default 60; per-step
  `--execute-timeout <min>`; `0` disables) — a hung CLI can never stall the
  factory. Timeouts count as failed attempts and feed the escalation ladder.
- **Infrastructure vs task failures** — an agent CLI that exits nonzero
  (crash, rate limit, auth blip) gets one same-tier retry before anything
  else; escalation budget is reserved for *verified task failures*.
- **Run budget** (`--max-wall-minutes <min>`) — hard wall-clock ceiling for a
  whole run, with a structured stop reason instead of an open-ended burn.
- **Cheap mechanical gates** — an execute step that crashed leaving no
  working-tree changes skips the verifier entirely (nothing to audit, no
  tokens spent) and loops back with the error output in context.

### The ledger: know where your time goes

Every step attempt is appended to `<state>/ledger.jsonl` — agent, model, tier,
attempt, duration, exit code, timeout flag — plus per-iteration verdicts and
the run outcome. `mm status` aggregates it into a per-agent scoreboard
(steps / time / retries / timeouts). Plain headless CLIs don't report token
spend uniformly, so wall-clock per agent is the cost proxy.

---

## Modes & Queue Strategies

middle-manager has three ways to shape a run. Pick by what you're optimizing for.

### 🎲 Random agent (the new default)

In the wizard the default agent for every step is **`random`** (shown with a
rainbow shimmer). At the start of **each iteration** it rolls one installed agent
and uses it for that whole iteration — so the agent varies issue-to-issue and
retry-to-retry, spreading work across every CLI you have logged in. Set it
per-step on the CLI too: `--execute-agent random`. With nothing installed it
fails closed with a clear message instead of guessing.

The wizard's options screen also carries the two factory quality levers —
**distinct verifier** and **escalate to a stronger agent on repeated failure**
(a one-rung ladder to the strongest installed agent) — both defaulting ON when
you have two or more agents installed. The review screen shows the resulting
ladder before launch.

### Loop shape: 4-step · 3-step · solo

- **4 steps** (default) — `discover → execute → verify → commit`, opens a PR.
- **3 steps** — `discover → execute → verify`, local commit, no PR agent.
- **1 step — solo** (`--solo`) — **one agent does everything**: it scopes,
  implements, runs the tests, self-reviews, and emits the `VERDICT`. mm still
  owns git deterministically (commit, one PR, `Closes #N`, auto-merge) and then
  **waits for that PR to actually merge** before returning. On its own it's a
  hands-off "ship this issue" button; across a queue it **serializes** work so
  each issue branches off a base that already contains the previous PR — no pile
  of conflicting PRs.

### Draining a GitHub issue queue

Filter issues with `--label` / `--author` / `--issue-limit`, then choose a strategy:

| Strategy | Flag | What you get |
|----------|------|--------------|
| Per-issue PRs (default) | _(none)_ | One PR per issue, opened back-to-back. Fast, but PRs can conflict. |
| Solo serialized | `--solo` | One agent per issue; mm waits for each PR to merge before the next issue. No conflicts, slower (you wait on CI per issue). |
| Worktree collapse | `--worktree` | Each issue is developed in its **own git worktree** off a frozen base, then mm merges the successful branches into one integration branch and opens a **single "mega" PR** that `Closes` every included issue. An agent resolves any merge conflicts (mm validates and commits; a branch it can't cleanly merge is dropped, not force-shipped). |

`--solo` and `--worktree` are competing answers to "stop the conflicting-PR
pile-up" and are mutually exclusive. Solo's wait is bounded by
`--merge-timeout <minutes>` (default 60) — a PR that never goes green (failed
required check, branch-protection review, conflict) stops the drain instead of
hanging forever. Worktree keeps its scratch trees under `worktrees/` in the
out-of-repo state dir; pass `--keep-worktrees` to leave them for inspection.

---

## The Loop

```text
  ┌──────────────┐
  │   DISCOVER   │  Scope requirements & compile guidelines
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
4. **Commit**: Appends durable learnings to the orchestrator notes file (outside your repo — `AGENTS.md` is read-only to mm) and lands one clean commit; middle-manager itself pushes, opens the PR, and links the issue.

A change is only committed on an explicit `VERDICT: PASS` from the verifier — a `FAIL` or a missing/garbled verdict **fails closed** and loops back rather than shipping unverified work. (The verifier agent runs the tests; middle-manager never runs them for you.) The loop also stops itself early if it stalls: if an iteration produces the same diff and the same verifier feedback as the last one, it bails instead of burning iterations.

---

## License

This project is licensed under the [MIT License](LICENSE).

