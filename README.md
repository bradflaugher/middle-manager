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
- Or rank your agents by strength once and let the **escalation ladder** start cheap and climb only when the cheap agent *verifiably* fails — so the expensive model is billed only for the work that actually needed it.

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
what to do → mission/issue/queue → loop shape → agents → options →
agent strength order → iteration budget → review & launch** (the strength
screen appears only when escalation is on). Every screen has a sensible
default, so mashing Enter gives you a working 4-step loop on `random` agents
with the quality levers (distinct verifier + escalation) already on.

Screens worth knowing about:

- **Agents** — the default for every seat is `random` (rainbow shimmer): each
  iteration rolls one installed agent and uses it for the whole iteration,
  spreading work across every CLI you're logged into. Press `c` to hand-pick
  agents per step instead.
- **Agent strength order** — appears when escalation is on. Rank your agents
  strongest-first (shift+↑/↓ to drag); escalation climbs your ranking, and the
  ranking is saved to `~/.config/middle-manager/config.json` so you only ever
  set it once.

Agents always run in their headless auto-approve mode — an unattended loop
cannot answer permission prompts, so there is nothing to configure there.
Every run starts fresh: stale plans from a previous mission are never reused.

## CLI Reference

| I want to… | Command |
|------------|---------|
| Add a feature | `mm quick "add feature XYZ"` (or just `mm "add feature XYZ"`) |
| Work one GitHub issue | `mm --issue 42` |
| Drain all bugs by a user | `mm --label bug --author @someuser --close-issues` |
| Good-first-issues sprint | `mm --label "good first issue" --issue-limit 10 --close-issues` |
| **No backlog? Audit the repo and file one** | `mm seed --count 10` (then drain `--label mm-todo`) |
| Fix the codebase generally (no issue needed) | `mm --mode repair` |
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
| See agents, state & the run ledger | `mm agents` · `mm status` |

While a loop runs, the monitor dashboard takes live commands: type a note to
steer the next step, or `/pause` `/resume` `/skip` `/quit`.

---

## The Software Factory

### Escalation ladders — mix big and small agents

The core bet: try the cheap configuration first, escalate only when a quality
check fails. Give any step an ordered ladder of `agent[:model]` rungs. The
step starts on its base agent; after every `--escalate-after N` failed
iterations (default 1) it climbs one rung:

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

A note on models: a `model` (anywhere — a step, a ladder rung) is passed to
the CLI's model flag **verbatim and unvalidated**; support depends on that
CLI, and some silently ignore the flag in headless mode. Omit it and each CLI
runs its own default — the recommended starting point.

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
time / retries / timeouts / **escalations**) **across a whole queue drain** —
each issue's ledger rolls up into one table, so a 50-issue drain answers
"where did my time go" in one command. The ESCAL column is the number the
playbook tunes on: how often the cheap seat actually needed rescuing.
Wall-clock per agent is your cost proxy: multiply by what each seat costs you
and the scoreboard is a bill.

### The playbook: where the strong models go (and where they don't)

Spend by **asymmetry of consequences**, not by vibes about which step feels
important:

| Seat | Deploy | Why |
|------|--------|-----|
| **verify** | Your strongest model. Always. | This is the gate everything rides on. A wrong FAIL costs you one retry (cheap). A wrong PASS ships broken code to a PR (expensive). When consequences are that lopsided, pay up. |
| **discover** | Strong. | The plan is the cheap executor's ceiling. A spec with exact file paths, function names, and acceptance criteria turns "needs a frontier model" into "any model can follow instructions." Skimp here and you'll pay for it in the execute seat instead, at a worse exchange rate. |
| **execute** | The cheapest thing that clears your verifier — with a ladder to your strongest. | This is where the volume is, so this is where the savings are. The ladder means being wrong about a model costs you one failed iteration, not a failed backlog. |
| **commit** | Anything fast. | It stages files and writes a commit message. Judgment-free. |
| **solo** | Strong only. | Solo collapses planner, executor, and verifier into one context — every argument above for splitting seats is an argument against putting a weak model here. Solo is a convenience mode, not a savings mode. |

**The strong default.** Drop this in `~/.config/middle-manager/config.json`
once and every run inherits it:

```json
{
  "discover": { "agent": "claude" },
  "execute":  { "agent": "opencode", "model": "<your-cheap-model>",
                "escalate": ["claude"] },
  "verify":   { "agent": "claude" },
  "distinct_verifier": true,
  "escalate_after": 2,
  "step_timeout_minutes": 60
}
```

Then drain the backlog with budgets on and read the bill after:

```bash
mm --label bug --issue-limit 50 --close-issues --merge --max-wall-minutes 240
mm status   # per-agent scoreboard for the whole drain: time, retries, escalations
```

Two habits that keep it honest:

- **Sanity-check your cheap seat once.** mm passes a configured model to each
  CLI's model flag faithfully but cannot verify the CLI honored it — some
  agents silently ignore the flag in headless mode. After your first drain,
  check the provider dashboard: if the "cheap" seat billed like the default
  model, the CLI ignored you — pick a different CLI for that seat instead of
  fighting it.
- **Let issues carry their own acceptance criteria.** The verifier checks the
  mission; a mission that says "make X better" gets you an unfalsifiable PASS.
  Issues that state what must be true when done are what make the cheap
  executor's PASSes trustworthy.

### Our assumptions — and what would change our mind

The playbook above is not eternal truth; it rests on four assumptions. Each
one comes with the signal — visible in **your own ledger** (`mm status`) —
that it has stopped holding for *your* repo, which matters more than what was
true for anyone else's:

1. **Most backlog issues are routine, and cheap models clear routine work.**
   *Watch:* the escalation rate. If the strong rung is taking over on more
   than roughly a third of your issues, the cheap tier is adding a failed
   iteration of cost in front of most work instead of absorbing it — promote
   your base executor (or make the planner stronger) and re-measure.
2. **Frontier models cost enough more that routing matters at all.** *Watch:*
   your providers' pricing. The playbook assumes an order-of-magnitude gap
   between your cheap and strong seats. If that gap collapses — cheap models
   get priced up, strong ones get priced down, or your subscription makes the
   strong model effectively flat-rate — skip the ladder and put the strong
   model in every seat; simplicity beats routing when routing saves nothing.
3. **Plan quality substitutes for executor strength.** We validated this the
   fun way: on our test repo, a bottom-tier free model solved tasks it
   provably could not solve unaided whenever the plan came from a strong
   model — and failed the moment the planner was weak too. *Watch:* if
   escalation keeps firing even with a strong planner, your tasks aren't
   spec-limited, they're reasoning-limited — this playbook's savings don't
   apply to that backlog.
4. **Verification is cheaper than execution.** The verifier reads and runs;
   the executor writes. *Watch:* if verify time rivals execute time in your
   scoreboard, your missions are too vague to check cheaply — fix the issue
   descriptions before touching the model assignments.

**When to re-evaluate:** whenever a provider changes prices, whenever a new
model tier ships, and any time the scoreboard surprises you. The re-evaluation
is cheap: re-run a slice of the same backlog with a different seating and
compare `mm status` tables. The ledger exists precisely so this is a
five-minute decision based on your data instead of a debate.

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

### No backlog? Seed one

The factory needs issues to drain — `mm seed` writes them for you:

```bash
mm seed --count 10                      # audit the repo, file up to 10 issues
mm seed "focus on test coverage" --count 5
mm seed --dry-run                       # preview without filing anything
mm --label mm-todo --close-issues --merge   # then drain what it filed
```

The strongest available agent deeply audits the repository — grounding itself
in your stack, stated invariants, and build/test commands, and skimming
existing issues to avoid duplicates — then proposes real, actionable issues
(no nitpicks; it errs toward *not* filing). **The agent proposes; mm files
deterministically**, labeling each issue with your drain label plus a
**priority** (`P0`–`P3`) and a **t-shirt size** (`XS`–`XL`, perceived
difficulty for one agent). That makes budget-shaped drains trivial:

```bash
mm --label P1 --close-issues --merge          # bugs first
mm --label XS --solo --close-issues --merge   # knock out the trivial tail cheaply
```

Every filed issue is self-contained — context, exact file:line locations,
evidence, a proposed direction, and mechanically checkable acceptance
criteria referencing the repo's real build/test commands — because the agent
that fixes it will arrive with no memory of the audit.

**Writing issues by hand?** `mm init` installs
`.github/ISSUE_TEMPLATE/mm-task.md` with the same structure. The one rule
that matters: acceptance criteria must be *mechanically checkable* (a command
to run, a fact about a file, a count). "Make X better" gets you an
unfalsifiable PASS; the verifier is only as strong as the criteria you give
it.

### No issues at all? Repair mode

`mm --mode repair` skips the backlog entirely: the discover step runs as an
**auditor** that hunts for the single highest-value, smallest-scope concrete
defect (failing tests, real bugs, docs that lie about the code) and scopes it
with acceptance criteria — then the normal execute → verify → commit pipeline
ships the fix. Give it a focus with `--mission "…"` or let it use its
judgment. Repair is "fix one thing well," seed-then-drain is "work through
many things" — use repair for a quick win, seed for a campaign.

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
