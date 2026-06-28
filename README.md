![middle-manager logo](interface_demo.gif)

# middle-manager

Micromanaged multi-agent coding loop that orchestrates your favorite coding CLIs.

**Bring your own agents.** middle-manager dynamically chains **Grok**, **Claude Code**, **OpenCode**, **OpenAI Codex**, **Google Antigravity (agy)**, and **Charm Crush** into a tight 4-step software factory. It reads your codebase, scopes out requirements, executes fixes, critiques its own work, runs tests, commits, and opens PRs—completely on autopilot. *(Agents are auto-detected and configured automatically).*

Each agent runs as its own CLI in plain headless mode, so it uses whatever login that tool already has—OAuth session or API key—with **no extra keys or adapters to configure**. And because it's *micromanaged*, you can watch every step live and steer it mid-run.

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

### Watch it work — and steer it

Run a loop without `--stream-output` and you get a live TUI: an animated `DISCOVER → EXECUTE → VERIFY → COMMIT` pipeline, a dashboard (branch · current step · agent · elapsed), a resource panel (CPU sparkline · processes · sockets), and the agent's output streaming in real time.

Because it's *micromanaged*, you steer it **between steps** from the input box:

- a typed note is **queued and folded into the next step's prompt** — it can't change the step that's running right now (each step is a one-shot agent CLI), but the loop pauses briefly to show your note landing on the next one.
- `/pause` · `/resume` · `/skip` take effect at the next step boundary.
- `/quit` aborts immediately — it kills the running agent's process group.

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
| Merge ready open PRs | `mm merge` |
| Merge PRs by one author | `mm merge --merge-author @someuser` |
| Preview a merge run | `mm merge --dry-run` |
| Point at another repo | `mm quick "…" --repo ~/other-project` |
| Pause between steps | `mm quick "…" -i` |
| Use a config file | `mm --config examples/quick-feature.json --repo .` |

State lives in `<repo>/.middle-manager/`. Issue queue state is per-issue under `.middle-manager/issues/<number>/`.

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
4. **Commit**: Saves updates, registers context updates in repository memory (`AGENTS.md`), and submits pull requests for review (it never auto-merges — see Merge Mode).

A change is only committed on an explicit `VERDICT: PASS` from the verifier — a `FAIL` or a missing/garbled verdict **fails closed** and loops back rather than shipping unverified work. (The verifier agent runs the tests; middle-manager never runs them for you.) The loop also stops itself early if it stalls: if an iteration produces the same diff and the same verifier feedback as the last one, it bails instead of burning iterations.

---

## Merge Mode

The loop opens PRs and leaves them for a human — it never auto-merges. When you're ready to ship the green ones, that's a separate, explicit command:

```bash
mm merge                      # merge every ready open PR
mm merge --merge-author @me   # only PRs by a given author
mm merge --merge-pr 42        # just one specific PR
mm merge --dry-run            # preview what would merge, change nothing
```

A PR is merged only if it's mergeable (no conflicts), not a draft, has no requested changes, and — unless you pass `--no-require-checks` — has green CI. It uses `gh pr merge` under the hood: never a force-merge, never `--admin`.

---

## License

This project is licensed under the [MIT License](LICENSE).

