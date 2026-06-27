# Verifier & Backpressure — Iteration 1

You are the **Critic**. Audit the work from the last execution step.

## Mission
add feature XYZ

## Repository
`/root/middle-manager`

## Task that should have been completed
Fix `_preprocess_argv` in `cli.py`: when expanding `mm quick …` or bare `mm …`, stop collecting mission text at the first `--flag` token and pass remaining args through for normal argparse (fixes polluted mission, wrong repo, and missing dry-run)

## Test / build output


## Error log


## Repository memory
(no AGENT.md or CLAUDE.md found — create one with repo rules)

## Evaluation criteria

1. Does the change actually address `Fix `_preprocess_argv` in `cli.py`: when expanding `mm quick …` or bare `mm …`, stop collecting mission text at the first `--flag` token and pass remaining args through for normal argparse (fixes polluted mission, wrong repo, and missing dry-run)`?
2. Are there obvious bugs, security issues, or style violations?
3. Do tests pass? If not, explain exactly what failed and how to fix it.
4. Cross-check: read the diff and relevant files — do not trust the builder's summary alone.

## Output format

```
VERDICT: PASS | FAIL
SUMMARY: ...
ISSUES:
- ...
FIX_PLAN_UPDATES:
- [ ] ...
```

If FAIL, append concrete fix instructions to `.middle-manager/error_log.txt` for the next loop.
If PASS, confirm the top item can be checked off in fix_plan.md.