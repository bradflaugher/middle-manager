# Generate & Execute — Iteration 1

You are the **Programmer** agent. Implement exactly **one** item from the plan.

## Mission
add feature XYZ

## Repository
`/root/middle-manager`

## Top priority task (do ONLY this)
Fix `_preprocess_argv` in `cli.py`: when expanding `mm quick …` or bare `mm …`, stop collecting mission text at the first `--flag` token and pass remaining args through for normal argparse (fixes polluted mission, wrong repo, and missing dry-run)

## Full plan for context
# fix_plan.md

## Feature

Ship the `mm quick` feature-mode workflow (discover → execute → verify) with correct `--dry-run` and `--repo` handling.

**Note:** The seeded mission string `add feature XYZ --dry-run --repo /root/bradflaugher.com` is corrupted — `_preprocess_argv` swallowed CLI flags into `--mission`. Real mission: `add feature XYZ` (README placeholder exercising the quick preset). Target repo for this run: `/root/middle-manager`.

## Files / modules

| File | Role |
|------|------|
| `middle_manager/cli.py` | `_preprocess_argv` — stop joining tokens at first `--flag` |
| `middle_manager/config.py` | `parse_args`, `--quick` / `--fresh` wiring |
| `middle_manager/presets.py` | `apply_quick_preset`, `reset_loop_state`, `seed_feature_plan` |
| `middle_manager/loop.py` | feature-mode seeding, `discover_feature` prompt, fresh reset |
| `middle_manager/prompts/discover_feature.md` | planner prompt (done) |
| `AGENT.md` | repo memory — missing, agents need build/test rules |

## Tasks

- [ ] Fix `_preprocess_argv` in `cli.py`: when expanding `mm quick …` or bare `mm …`, stop collecting mission text at the first `--flag` token and pass remaining args through for normal argparse (fixes polluted mission, wrong repo, and missing dry-run)
- [ ] Enable `--fresh` automatically for `mm quick` (README promises state reset; only bare shorthand sets it today)
- [ ] Create `AGENT.md` at repo root with middle-manager conventions (stdlib-only, `python mm.py` / `mm`, no pip deps, test via manual smoke commands)
- [ ] Verify `presets.py` integration end-to-end: `apply_quick_preset` sets mode/steps/agents, `seed_feature_plan` writes mission-only plan, `reset_loop_state` clears `.middle-manager/` on fresh runs
- [ ] Smoke-test dry-run path: `mm quick "add feature XYZ" --dry-run --repo /root/middle-manager` must print agent commands, set `dry_run=True`, `repo=/root/middle-manager`, mission=`add feature XYZ` (no flags in mission)

## Repository memory
(no AGENT.md or CLAUDE.md found — create one with repo rules)

## Previous errors (if any)


## Rules

1. **One item per loop.** Do not scope-creep into other fix_plan items.
2. Make the minimal correct change. Match existing code style.
3. Run relevant tests/build commands yourself before finishing.
4. If the task is already done, say so and update fix_plan.md to check it off.
5. Do not open PRs or merge — the loop handles git.

Ship the single task. Nothing else.