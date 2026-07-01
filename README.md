![middle-manager demo](./docs/interface_demo.gif)

# middle-manager

Micromanaged multi-agent coding loop that orchestrates your favorite coding CLIs.

**Bring your own agents.** middle-manager chains **Claude Code**, **OpenAI Codex**, **Grok**, **OpenCode**, **Google Antigravity (agy)**, **Charm Crush** — plus **any headless CLI you declare in config** — into a tight 4-step software factory: it scopes the work, implements it, critiques and tests it, and lands one clean commit with a PR. Completely on autopilot, and watchable/steerable live from the built-in dashboard.

Each agent runs as its own CLI in plain headless mode, so it uses whatever login that tool already has — OAuth session or API key — with **no extra keys or adapters to configure**.

---

## Why

**Get the most out of every model — the ones you pay for and the ones you run for free.** Each step of the loop is just a coding CLI pointed at a model, so you can put the right model in the right seat:

- A **cheap or local model executes**, and a **stronger frontier model verifies** its work.
- A **big model plans**, while cheaper agents do the grunt work.
- Or rank your agents by strength once and let the **escalation ladder** start cheap and climb only when the cheap agent *verifiably* fails — the cascade pattern the multi-agent literature credits with 45–85% cost savings at ~95% retained quality.

**The orchestration itself is deterministic code, not another LLM.** Branching, committing, opening/merging PRs, closing issues, draining queues, enforcing budgets — all fixed logic. You're not paying an agent to babysit a queue, and an agent can't talk its way past a gate.

---

## How it works

```text
  ┌──────────────┐
  │   DISCOVER   │  Scope requirements & write implementation guidelines
  └──────────────┘
         │
         ▼
  ┌──────────────┐
  │   EXECUTE    │  Implement the changes
  └──────────────┘
         │
         ▼
  ┌──────────────┐
  │    VERIFY    │  Run tests & critique (a different agent, if you want)
  └──────────────┘
         │
         ├─ (Pass) ─► [ COMMIT ]  mm lands the commit, opens & links the PR
         │
         └─ (Fail) ─► Loop back with the verifier's findings — or escalate
```

Every handoff between steps is explicit: the planner's report feeds the programmer, the programmer's report and the **actual git change surface** feed the verifier, and a failed iteration feeds the verifier's concrete findings (plus the tree's uncommitted work) to the next attempt — never a blind retry.

**Nothing ships un-verified, and a verifier's word alone isn't enough:**

- A change is only committed on an explicit `VERDICT: PASS` — a FAIL or a missing/garbled verdict **fails closed** and loops back. (The verifier agent runs your tests; mm itself never does.)
- After a PASS, **deterministic gates** run in Go: a secret scan blocks credential-shaped strings from ever being committed (with [gitleaks](https://github.com/gitleaks/gitleaks)' full ruleset automatically layered on when it's installed; `--no-secret-scan` to opt out), and unauthorized edits to `AGENTS.md`/`CLAUDE.md`/`.cursorrules` are auto-reverted unless your mission asked for them.
- The loop can't spin: an iteration that leaves the tree byte-identical **escalates the agent ladder** or stops; `--max-iterations`, per-step timeouts, and `--max-wall-minutes` are the hard outer bounds.
- Preflight checks (agents installed, `gh` authenticated when PRs are needed, writable state) run **before** any agent burns a token, and a per-repo lock stops two mm runs from fighting over one working tree.

---

## Install (One-Liner)

```bash
curl -fsSL https://raw.githubusercontent.com/bradflaugher/middle-manager/main/install.sh | bash
```

The installer downloads a **prebuilt binary** for your platform from the latest
[GitHub Release](https://github.com/bradflaugher/middle-manager/releases) — **no Go toolchain required**.
If no prebuilt binary is available it falls back to building from source (which
*does* need Go 1.25+). It installs `mm` to `~/.local/bin/mm`.

`mm` shells out to whichever agent CLIs you have installed (`claude`, `codex`,
`grok`, `opencode`, `agy`, `crush`, or your own) and to `git`/`gh` — install the ones you want.

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

## Quick Start (Wizard)

```bash
mm
```

The wizard (shown in the GIF above) walks you through: **repo → base branch →
what to do → mission/issue/queue → loop shape → agents → options → agent
strength order → iteration budget → review & launch**. Every screen has a
sensible default, so mashing Enter gives you a working 4-step loop on `random`
agents with the quality levers (distinct verifier + escalation) already on.

Two screens are worth knowing about:

- **Agents** — the default for every seat is `random` (rainbow shimmer): each
  iteration rolls one installed agent and uses it for the whole iteration,
  spreading work across every CLI you're logged into. Press `c` to hand-pick
  agents per step instead.
- **Agent strength order** — appears when escalation is on. Rank your agents
  strongest-first (shift+↑/↓ to drag); escalation climbs your ranking, and the
  ranking is saved to `~/.config/middle-manager/config.json` so you only ever
  set it once.

## CLI Reference

| I want to… | Command |
|------------|---------|
| Add a feature | `mm quick "add feature XYZ"` (or just `mm "add feature XYZ"`) |
| Work one GitHub issue | `mm --issue 42` |
| Drain all bugs by a user | `mm --label bug --author @someuser --close-issues` |
| Good-first-issues sprint | `mm --label "good first issue" --issue-limit 10 --close-issues` |
| Fix the codebase generally | `mm --mode repair` |
| **Solo:** one agent does it all, wait for the PR to merge | `mm --issue 42 --solo` |
| Solo, fully hands-off (mm merges the PR when green) | `mm --issue 42 --solo --merge` |
| **Solo queue:** drain issues one merged PR at a time | `mm --label bug --solo --close-issues` |
| **Worktree:** drain a queue into ONE mega PR | `mm --label bug --worktree --close-issues` |
| Roll a **random** installed agent each iteration | `mm "…" --execute-agent random` |
| **Cheap agent first, escalate on failure** | `mm --issue 42 --execute-agent opencode --execute-escalate "claude:opus,codex"` |
| Different agent audits the work | `mm --issue 42 --distinct-verifier` |
| Declare your strength ranking | `mm "…" --strength-order "claude,codex,opencode"` |
| Bound one agent invocation | `mm "…" --step-timeout 30` (per-step: `--execute-timeout 90`) |
| Bound the whole run / drain | `mm --label bug --max-wall-minutes 120` |
| Point at another repo | `mm quick "…" --repo ~/other-project` |
| Pause between steps | `mm quick "…" -i` |
| See agents, state & the run ledger | `mm agents` · `mm status` |

---

## The Software Factory

### Escalation ladders — mix big and small agents

Give any step an ordered ladder of `agent[:model]` rungs. The step starts on
its base agent; after every `--escalate-after N` failed iterations (default 1)
it climbs one rung:

```bash
# opencode tries first; claude-on-opus takes over if it verifiably fails; codex is the last resort
mm --issue 42 --execute-agent opencode --execute-escalate "claude:opus,codex"
```

Ladders work on every step (`--discover-escalate`, `--verify-escalate`, …) and
on the solo agent. The escalated agent gets a real **handoff**, not a cold
start: the verifier's findings, the tree's uncommitted change summary from the
failed attempt, and a banner naming its predecessor with instructions to
review/keep/revert that work rather than redo it blind.

In JSON config, ladders take strings or objects:

```json
{ "execute": { "agent": "opencode",
               "escalate": ["claude:opus", {"agent": "codex", "model": "gpt-5"}] } }
```

**You define what "stronger" means.** Your ranking — set on the wizard's
strength screen, via `"strength_order"` in config, or `--strength-order` —
drives both the wizard's ladder preset and the distinct-verifier pick. mm's
built-in ordering is only the fallback.

### Independent verifier

`--distinct-verifier` guarantees the verify step runs on a **different agent**
than the one that wrote the code (with `random` seats the verifier gets its
own roll). The verifier is prompted adversarially — *try to refute that this
change satisfies the mission* — and must list what it actually checked, so a
lazy PASS is visible. It's the cheapest known defense against self-review
rubber-stamping.

### Bring ANY agent

Declare any headless coding CLI in `~/.config/middle-manager/config.json`
(loaded on every run, then overlaid by `--config` and CLI flags):

```json
{
  "agents": {
    "aider": {
      "binary": "aider",
      "print_flag": "--message",
      "yolo_flags": ["--yes-always"],
      "model_flag": "--model"
    }
  }
}
```

Custom agents appear in `mm agents`, the wizard's pickers, `random` rolls, and
escalation ladders exactly like built-ins. Redefining a built-in name is
allowed on purpose — fix a flag mismatch the day an upstream CLI changes,
without waiting for an mm release.

### Robustness: timeouts, retries, budgets, gates

- **Per-step timeout** (`--step-timeout <min>`, default 60; `0` disables) — a
  hung CLI can never stall the factory; timeouts count as failed attempts.
- **Infrastructure vs task failures** — an agent CLI that exits nonzero
  (crash, rate limit, auth blip) gets one same-tier retry; escalation budget
  is reserved for *verified task failures*.
- **Budgets** — `--max-iterations` per task, `--max-wall-minutes` for the
  whole run **and** for a whole queue drain.
- **Deterministic gates** — preflight before the first token, the pre-commit
  secret scan and memory-file guard after every PASS, and a per-repo run lock.
- **Mechanical shortcuts** — an execute step that crashed leaving no changes
  skips the verifier entirely (nothing to audit, no tokens spent).

### The ledger: know where your time goes

Every step attempt is appended to `<state>/ledger.jsonl` — agent, model, tier,
attempt, duration, exit code, timeout flag — plus per-iteration verdicts and
run outcomes. `mm status` aggregates it into a per-agent scoreboard (steps /
time / retries / timeouts) **across a whole queue drain** — each issue's
ledger rolls up into one table, so a 50-issue drain answers "where did my
time/money go" in one command. Headless CLIs don't report token spend
uniformly, so wall-clock per agent is the cost proxy.

### Recipe: drain a big queue for cheap

The setup that squeezes a large issue backlog on a budget:

```bash
mm --label bug --issue-limit 50 --close-issues --merge \
   --discover-agent claude \
   --execute-agent opencode --execute-model opencode/deepseek-v4-flash-free \
   --execute-escalate "claude" --escalate-after 2 \
   --verify-agent claude --distinct-verifier \
   --max-wall-minutes 240
```

Why each seat is where it is:

- **Strong planner** (`discover`): the cheap executor succeeds in proportion
  to spec quality, not its own reasoning — a big model writing exact file
  paths, function names, and acceptance criteria is what makes the cheap seat
  viable at all.
- **Cheap executor with a ladder**: the cheap model gets `--escalate-after 2`
  attempts per issue (each retry already carries the verifier's findings);
  only a *verified* failure promotes the strong model, and each issue starts
  back at the cheap rung — you pay the big model only for the issues that
  actually need it.
- **Strong, distinct verifier**: verification is judgment-heavy and it is the
  gate everything rides on. Never let the executor grade its own homework.
- **Budgets on, ledger after**: `--max-wall-minutes` bounds the whole drain;
  `mm status` afterwards shows exactly how much work each agent absorbed and
  how often escalation fired — tune `--escalate-after` from that.

One honest caveat from live testing: mm passes each CLI's model flag
faithfully, **but cannot verify the CLI honored it** — some agents silently
ignore `--model` in headless mode (opencode hard-errors on an unknown model;
Antigravity accepts anything). Sanity-check your cheap seat with an invalid
model name once before trusting it.

### Why this design — the evidence

middle-manager's architecture follows what the multi-agent literature keeps
converging on, rather than inventing its own:

- **Cascade routing (cheap first, escalate on verified failure).**
  [RouteLLM](https://arxiv.org/abs/2406.18665) reports ~85% cost reduction
  while retaining ~95% of GPT-4-level quality by routing easy work to weak
  models; [FrugalGPT](https://arxiv.org/abs/2305.05176) reports up to ~98%
  cost reduction with LLM cascades that escalate only when a quality check
  fails. That is exactly mm's escalation ladder: the trigger is a verified
  FAIL, never vibes, and every retry adds information (verifier findings, the
  tree's change surface) or capability (a stronger rung) — never a blind
  identical retry.
- **Big model plans, cheap model executes.** Anthropic's own
  [model configuration](https://code.claude.com/docs/en/model-config) ships
  this split (`opusplan`: Opus plans, Sonnet executes) because planning and
  verification are judgment-heavy while well-specified execution is not.
- **Deterministic orchestration, not an LLM babysitter.**
  [Building Effective Agents](https://www.anthropic.com/research/building-effective-agents)
  (Anthropic): prefer simple, explicitly-sequenced workflows with programmatic
  gates between steps over LLM-driven orchestration. mm's loop shape, git
  ops, queue lifecycle, budgets, and gates are all plain code.
- **Coordination failures dominate, not model weakness.** The
  [MAST taxonomy](https://arxiv.org/abs/2503.13657) attributes the large
  majority of multi-agent failures to specification and inter-agent
  coordination — lost context, step repetition, missing termination — which
  is why mm's handoffs are explicit artifacts (plans, reports, diffs,
  verdicts), retries carry evidence, and every loop has hard termination
  bounds.
- **LLM judges are gameable; code is not.** LLM verifiers can be fooled by
  confident-sounding output (see
  [One Token to Fool LLM-as-a-Judge](https://arxiv.org/abs/2507.08794)), so
  mm treats a PASS as necessary but not sufficient: strict fail-closed
  verdict parsing, adversarial verifier framing with required CHECKED
  evidence, an independent (distinct-agent) critic, and deterministic Go
  gates (secret scan, memory-file guard, diff checks) after every PASS.
- **Multi-agent costs real money.** Anthropic's
  [multi-agent research system](https://www.anthropic.com/engineering/multi-agent-research-system)
  measured multi-agent runs at ~15× the tokens of single-agent chat — worth
  it only when the task justifies it. mm's answer is to make the spend
  visible (the ledger) and bounded (iteration, step-timeout, and wall-clock
  budgets) instead of pretending it isn't happening.

---

## Modes & Queue Strategies

### Loop shape: 4-step · 3-step · solo

- **4 steps** (default) — `discover → execute → verify → commit`, opens a PR.
- **3 steps** — `discover → execute → verify`, local commit, no PR agent.
- **1 step — solo** (`--solo`) — **one agent does everything**: scopes,
  implements, tests, self-reviews, and emits the `VERDICT`. mm still owns git
  deterministically (commit, one PR, `Closes #N`) and **waits for that PR to
  actually merge** before returning.

With `--merge`, mm arms GitHub auto-merge — and on repos where GitHub refuses
(no branch protection), **mm merges the PR itself** the moment it's green:
required checks passing, or no checks at all. It will never merge over red CI,
required or not. Without `--merge`, PRs wait for a human (or `mm merge`).

### Draining a GitHub issue queue

Filter issues with `--label` / `--author` / `--issue-limit`, then pick a strategy:

| Strategy | Flag | What you get |
|----------|------|--------------|
| Per-issue PRs (default) | _(none)_ | One PR per issue, opened back-to-back. Fast, but PRs can conflict. |
| Solo serialized | `--solo` | One agent per issue; mm waits for each PR to merge before the next issue. No conflicts, slower (you wait on CI per issue). |
| Worktree collapse | `--worktree` | Each issue is developed in its **own git worktree** off a frozen base, then mm merges the successful branches into one integration branch and opens a **single "mega" PR** that `Closes` every included issue. An agent resolves merge conflicts; mm validates and commits — a branch it can't cleanly merge is dropped, not force-shipped. |

`--solo` and `--worktree` are competing answers to "stop the conflicting-PR
pile-up" and are mutually exclusive. Solo's wait is bounded by
`--merge-timeout <minutes>` (default 60) — a PR that never goes green stops
the drain instead of hanging forever. Pass `--keep-worktrees` to keep the
scratch trees for inspection.

---

## Where things live

Everything mm writes stays **outside your repository**:

| What | Where |
|------|-------|
| Run state, prompts, outputs, ledger | `~/.local/state/middle-manager/<repo>-<hash>/` (respects `$XDG_STATE_HOME`; override with `--state-dir`) |
| Cross-run learnings (injected into every prompt) | `notes.md` in that state dir (override with `--notes-file`) |
| Your persistent config: custom agents, strength order, defaults | `~/.config/middle-manager/config.json` (respects `$XDG_CONFIG_HOME`) |
| Custom prompt overrides | `<state-dir>/prompts/*.md`, or committed in `<repo>/.middle-manager/prompts/*.md` |

mm never touches your `.gitignore` or `AGENTS.md`, and an agent's `git add -A`
can never sweep orchestrator state into a commit.

---

## License

This project is licensed under the [MIT License](LICENSE).
