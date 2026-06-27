"""Interactive setup wizard — run `mm` with no args and get interrogated."""

from __future__ import annotations

import json
import os
import sys
from pathlib import Path

from .agents import AGENT_NAMES, autodetect_step_agents, list_agents_status
from .config import DEFAULTS, LoopConfig, StepConfig, config_from_dict, merge_config
from .git_ops import gh_available, list_issues, repo_is_git

CONFIG_HOME = Path(os.environ.get("XDG_CONFIG_HOME", Path.home() / ".config")) / "middle-manager"
LAST_CONFIG_PATH = CONFIG_HOME / "last.json"


def _tty() -> bool:
    return sys.stdin.isatty() and sys.stdout.isatty()


def _prompt(text: str, default: str = "", *, required: bool = True) -> str:
    suffix = f" [{default}]" if default else (" (optional)" if not required else "")
    while True:
        raw = input(f"{text}{suffix}: ").strip()
        if raw:
            return raw
        if default or not required:
            return default
        print("  (required — press Enter for default if one is shown)")


def _choose(text: str, options: list[tuple[str, str]], default_key: str) -> str:
    print(f"\n{text}")
    for key, label in options:
        mark = "*" if key == default_key else " "
        print(f"  {mark} [{key}] {label}")
    keys = {k for k, _ in options}
    while True:
        raw = input(f"Choice [{default_key}]: ").strip().lower() or default_key
        if raw in keys:
            return raw
        print(f"  Pick one of: {', '.join(sorted(keys))}")


def _yes_no(text: str, default: bool = True) -> bool:
    hint = "Y/n" if default else "y/N"
    raw = input(f"{text} ({hint}): ").strip().lower()
    if not raw:
        return default
    return raw in ("y", "yes", "1", "true")


def _expand_path(raw: str) -> Path:
    return Path(raw).expanduser().resolve()


def _show_agents(overrides: dict[str, str]) -> None:
    print("\n  Installed agents on this box:")
    for row in list_agents_status(overrides):
        mark = "✓" if row["available"] == "yes" else "✗"
        print(f"    {mark} {row['agent']}")


def _pick_agent(step: str, default: str, overrides: dict[str, str]) -> str:
    available = [n for n in AGENT_NAMES if any(r["agent"] == n and r["available"] == "yes" for r in list_agents_status(overrides))]
    if default not in available and available:
        default = available[0]
    print(f"\n  {step} agent (default: {default})")
    print(f"    available: {', '.join(available) or 'none — will dry-run or fail'}")
    raw = input(f"  Agent [{default}]: ").strip().lower()
    if not raw:
        return default
    if raw in AGENT_NAMES:
        return raw
    print(f"  Unknown agent, using {default}")
    return default


def load_last_config() -> dict:
    if LAST_CONFIG_PATH.exists():
        try:
            return json.loads(LAST_CONFIG_PATH.read_text(encoding="utf-8"))
        except (json.JSONDecodeError, OSError):
            pass
    return {}


def save_last_config(cfg: LoopConfig, extra: dict | None = None) -> None:
    CONFIG_HOME.mkdir(parents=True, exist_ok=True)
    data = {
        "repo": str(cfg.repo),
        "steps": cfg.steps,
        "max_iterations": cfg.max_iterations,
        "yolo": cfg.yolo,
        "test_command": cfg.test_command,
        "mission": cfg.mission,
        "mode": cfg.mode,
        "issue": cfg.issue,
        "issue_queue": _issue_queue_to_dict(cfg),
        "fix_unrelated_tests": cfg.fix_unrelated_tests,
        "discover": _step_to_dict(cfg.discover),
        "execute": _step_to_dict(cfg.execute),
        "verify": _step_to_dict(cfg.verify),
        "commit": _step_to_dict(cfg.commit),
    }
    if extra:
        data.update(extra)
    LAST_CONFIG_PATH.write_text(json.dumps(data, indent=2) + "\n", encoding="utf-8")


def _step_to_dict(sc: StepConfig) -> dict:
    return {"agent": sc.agent, "model": sc.model, "extra_args": sc.extra_args, "enabled": sc.enabled}


def _issue_queue_to_dict(cfg: LoopConfig) -> dict | None:
    if not cfg.issue_queue:
        return None
    q = cfg.issue_queue
    return {
        "label": q.label,
        "author": q.author,
        "state": q.state,
        "limit": q.limit,
        "close_on_success": q.close_on_success,
    }


def run_wizard(argv_repo: Path | None = None, mission: str | None = None) -> LoopConfig | None:
    if not _tty():
        return None

    from .colors import Colors
    print()
    print(Colors.colored("  ╔══════════════════════════════════════════════════════════╗", Colors.CYAN))
    print(Colors.colored("  ║  middle-manager — unsupervised multi-agent loop          ║", Colors.CYAN + Colors.BOLD))
    print(Colors.colored("  ║  not for you. YOLO on. use claude if this feels wrong.   ║", Colors.CYAN))
    print(Colors.colored("  ║", Colors.CYAN) + Colors.colored("  Ctrl+C to quit at any time if you started by accident.  ", Colors.YELLOW) + Colors.colored("║", Colors.CYAN))
    print(Colors.colored("  ╚══════════════════════════════════════════════════════════╝", Colors.CYAN))
    print()

    last = load_last_config()
    default_repo = str(last.get("repo", os.getcwd()))
    if argv_repo:
        default_repo = str(argv_repo.resolve())

    repo_raw = _prompt("Repository path", default_repo)
    repo = _expand_path(repo_raw)
    if not repo.exists():
        print(Colors.colored(f"  ✗ Path does not exist: {repo}", Colors.RED))
        return None
    if not repo_is_git(repo):
        print(Colors.colored(f"  ⚠ {repo} is not a git repo — continuing anyway", Colors.YELLOW))

    if mission:
        mode = "feature"
        print(f"\n  Mission: {mission}")
    else:
        mode = _choose(
            "What are we doing?",
            [
                ("feature", "Build something new (e.g. \"add feature XYZ\") — recommended"),
                ("repair", "Discover & fix problems in the codebase"),
                ("issue", "Work a single GitHub issue"),
                ("queue", "Batch loop: drain a filtered queue of GitHub issues"),
            ],
            default_key=last.get("mode", "feature"),
        )

        mission_default = last.get("mission", "")
        print("\n  Mission prompt — what should the agents build or fix?")
        if mode == "feature":
            print("  Example: add dark mode toggle to settings page")
            mission = _prompt("Mission", mission_default, required=True)
        else:
            print("  Leave blank for no extra guidance.")
            mission = _prompt("Mission", mission_default, required=False)

    issue: str | None = None
    issue_queue: dict | None = None

    if mode == "issue":
        if not gh_available():
            print("  ⚠ gh CLI not found — issue title/body won't be fetched")
        issue = _prompt("Issue number or URL", last.get("issue", ""))
    elif mode == "queue":
        if not gh_available():
            print("  ✗ gh CLI required for issue queue mode")
            return None
        print("\n  Issue filter (leave blank to skip a filter):")
        label = _prompt("  Label", (last.get("issue_queue") or {}).get("label", ""), required=False)
        author = _prompt("  Author (@user)", (last.get("issue_queue") or {}).get("author", ""), required=False)
        limit_s = _prompt("  Max issues", str((last.get("issue_queue") or {}).get("limit", 20)))
        try:
            limit = max(1, int(limit_s))
        except ValueError:
            limit = 20
        close_on_success = _yes_no("Close issues automatically when fixed?", default=True)
        issue_queue = {
            "label": label or None,
            "author": author or None,
            "state": "open",
            "limit": limit,
            "close_on_success": close_on_success,
        }
        # Preview matching issues
        from .config import IssueQueueConfig

        preview = list_issues(repo, IssueQueueConfig(**issue_queue))
        print(f"\n  Found {len(preview)} matching open issue(s)")
        for row in preview[:5]:
            print(f"    #{row['number']} {row['title'][:60]}")
        if len(preview) > 5:
            print(f"    ... and {len(preview) - 5} more")
        if not preview:
            if not _yes_no("No issues match — continue anyway?", default=False):
                return None

    overrides: dict[str, str] = {}
    _show_agents(overrides)
    detected = autodetect_step_agents(overrides)
    print("\n  Autodetected agent defaults:")
    for step, agent in detected.items():
        print(f"    {step:10} → {agent}")

    if _yes_no("Customize agents per step?", default=False):
        for step in ("discover", "execute", "verify", "commit"):
            detected[step] = _pick_agent(step, detected[step], overrides)

    if mode == "feature":
        steps = 3
        print("\n  Using 3-agent stack: discover → execute → verify")
    else:
        steps = 4
        if _yes_no("Use 4-step loop (discover→execute→verify→commit)?", default=True):
            steps = 4
        else:
            steps = 3

    yolo = _yes_no("YOLO mode (auto-approve agent permissions)?", default=True)
    dry_run = _yes_no("Dry-run (print commands only)?", default=False)
    pause_steps = _yes_no("Pause before each step?", default=False)
    fix_unrelated = _yes_no("Allow agents to fix unrelated test failures?", default=last.get("fix_unrelated_tests", False))

    test_default = last.get("test_command") or "npm test"
    test_command = _prompt("Test command for backpressure", test_default)
    max_iter_s = _prompt("Max iterations per issue/task", str(last.get("max_iterations", 10)))
    try:
        max_iterations = max(1, int(max_iter_s))
    except ValueError:
        max_iterations = 10

    no_pr = not _yes_no("Open PRs when commit step succeeds?", default=True)

    # Build config
    data = merge_config(DEFAULTS, {})
    data["steps"] = steps
    data["max_iterations"] = max_iterations
    data["yolo"] = yolo
    data["test_command"] = test_command or None
    data["mode"] = mode
    data["mission"] = mission or None
    for step in ("discover", "execute", "verify", "commit"):
        data[step] = {"agent": detected[step], "model": None, "extra_args": []}
        if step == "discover" and detected["discover"] == "grok":
            data[step]["extra_args"] = ["--check"]
        if step == "verify" and detected["verify"] == "grok":
            data[step]["extra_args"] = ["--check"]

    cfg = config_from_dict(data, repo)
    cfg.dry_run = dry_run
    cfg.interactive = pause_steps
    cfg.issue = issue
    cfg.no_pr = no_pr
    cfg.fix_unrelated_tests = fix_unrelated
    cfg.mode = mode
    cfg.mission = mission or None
    if mode == "feature" and mission:
        cfg.fresh = True
        from .presets import apply_quick_preset

        apply_quick_preset(cfg)
        for step in ("discover", "execute", "verify"):
            data[step]["agent"] = cfg.step_for(step).agent

    if issue_queue:
        from .config import IssueQueueConfig

        cfg.issue_queue = IssueQueueConfig(**issue_queue)

    if steps == 3:
        cfg.commit.enabled = False

    print(Colors.colored("\n  ── Summary ──", Colors.CYAN + Colors.BOLD))
    print(f"  Repo:     {cfg.repo}")
    print(f"  Mode:     {cfg.mode}")
    if cfg.mission:
        print(f"  Mission:  {cfg.mission[:72]}{'...' if len(cfg.mission) > 72 else ''}")
    print(f"  Steps:    {cfg.steps} ({', '.join(cfg.active_steps())})")
    for step in cfg.active_steps():
        sc = cfg.step_for(step)
        print(f"    {step:10} {sc.agent}")
    print(f"  YOLO:     {Colors.colored(str(cfg.yolo), Colors.GREEN if cfg.yolo else Colors.YELLOW)} | dry-run: {Colors.colored(str(cfg.dry_run), Colors.YELLOW if cfg.dry_run else Colors.GREEN)}")
    print(f"  Fix unrelated tests: {Colors.colored(str(cfg.fix_unrelated_tests), Colors.GREEN if cfg.fix_unrelated_tests else Colors.YELLOW)}")
    if cfg.issue_queue:
        q = cfg.issue_queue
        parts = [f"state={q.state}", f"limit={q.limit}"]
        if q.label:
            parts.append(f"label={q.label}")
        if q.author:
            parts.append(f"author={q.author}")
        print(f"  Queue:    {', '.join(parts)} | close={q.close_on_success}")
    elif cfg.issue:
        print(f"  Issue:    {cfg.issue}")

    if not _yes_no(Colors.colored("\nStart the loop?", Colors.BOLD + Colors.CYAN), default=True):
        print("  Aborted.")
        return None

    save_last_config(cfg)
    return cfg