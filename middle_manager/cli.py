"""CLI entry and subcommands."""

from __future__ import annotations

import os
import sys
from pathlib import Path

from .agents import list_agents_status
from .config import LoopConfig, parse_args
from .git_ops import repo_is_git
from .issue_queue import IssueQueueRunner
from .loop import MiddleManagerLoop
from .wizard import run_wizard

_COMMANDS = frozenset({
    "run", "quick", "agents", "init", "status", "issues", "install-path",
})


def _split_mission_tail(argv: list[str]) -> tuple[str, list[str]]:
    """Pull leading mission words off argv; leave flags intact."""
    mission_parts: list[str] = []
    for i, arg in enumerate(argv):
        if arg.startswith("-"):
            return " ".join(mission_parts).strip(), argv[i:]
        mission_parts.append(arg)
    return " ".join(mission_parts).strip(), []


def _preprocess_argv(argv: list[str]) -> list[str]:
    """Shorthand: mm quick \"add X\"  or  mm \"add feature XYZ\" """
    if not argv:
        return argv
    if argv[0] == "quick":
        mission, tail = _split_mission_tail(argv[1:])
        out = ["quick", "--no-wizard"]
        if mission:
            out.extend(["--mission", mission])
        return out + tail
    if argv[0] not in _COMMANDS and not argv[0].startswith("-"):
        mission, tail = _split_mission_tail(argv)
        out = ["quick", "--fresh", "--no-wizard"]
        if mission:
            out.extend(["--mission", mission])
        return out + tail
    return argv


def _should_wizard(args, argv: list[str] | None) -> bool:
    if args.no_wizard:
        return False
    if args.wizard:
        return True
    if getattr(args, "quick", False):
        return False
    # No subcommand and no significant CLI flags → wizard
    if argv is None:
        argv = sys.argv[1:]
    if argv and argv[0] in ("agents", "init", "status", "issues", "install-path", "run", "--help", "-h"):
        if argv[0] in ("run",) or argv[0].startswith("-"):
            pass  # fall through to flag check
        else:
            return False
    flags = {
        "--repo", "-C", "--config", "--issue", "--mission", "-m", "--mode",
        "--label", "--author", "--dry-run", "--no-wizard", "--steps",
        "--discover-agent", "--execute-agent", "--verify-agent", "--commit-agent",
        "--quick", "-q", "--fresh",
    }
    if any(a in flags or a.split("=")[0] in flags for a in argv):
        return False
    return sys.stdin.isatty()


def cmd_agents(cfg: LoopConfig) -> int:
    from .colors import Colors
    rows = list_agents_status(cfg.binary_overrides)
    print(Colors.colored(f"{'AGENT':<10} {'AVAILABLE':<10} {'BINARY':<24} YOLO FLAG", Colors.CYAN + Colors.BOLD))
    print(Colors.colored("-" * 72, Colors.CYAN))
    for row in rows:
        agent_pad = Colors.colored(row['agent'].ljust(10), Colors.BOLD)
        avail_color = Colors.GREEN if row['available'] == 'yes' else Colors.RED
        avail_pad = Colors.colored(row['available'].ljust(10), avail_color)
        print(f"{agent_pad} {avail_pad} {row['binary']:<24} {row['yolo']}")
        if row["notes"]:
            print(Colors.colored(f"           {row['notes']}", Colors.YELLOW))
    return 0


def cmd_init(cfg: LoopConfig) -> int:
    from .colors import Colors
    state = cfg.state_path()
    templates = ["fix_plan.md", "AGENT.md"]
    for name in templates:
        dest = state / name if name == "fix_plan.md" else cfg.repo / name
        if dest.exists():
            print(f"exists: {dest}")
            continue
        if name == "fix_plan.md":
            dest.write_text("# fix_plan.md\n\n- [ ] Add your first task here\n", encoding="utf-8")
        else:
            dest.write_text(
                "# AGENT.md\n\nRepository memory for middle-manager loops.\n"
                "Add build commands, conventions, and things agents keep forgetting.\n",
                encoding="utf-8",
            )
        print(Colors.colored(f"created: {dest}", Colors.GREEN))
    print(f"State dir: {state}")
    return 0


def cmd_status(cfg: LoopConfig) -> int:
    from .colors import Colors
    state = cfg.state_path()
    print(Colors.colored(f"Repo:  {cfg.repo}", Colors.BOLD))
    print(f"Git:   {'yes' if repo_is_git(cfg.repo) else 'no'}")
    print(f"Mode:  {cfg.mode}")
    print(f"State: {state}")
    print()
    
    plan_path = state / "fix_plan.md"
    if plan_path.exists():
        print(Colors.colored("Current Plan:", Colors.BOLD + Colors.CYAN))
        plan_text = plan_path.read_text(encoding="utf-8")
        has_tasks = False
        for line in plan_text.splitlines():
            stripped = line.strip()
            if stripped.startswith("- [ ]"):
                task = stripped[5:].strip()
                print(f"  [ ] {Colors.colored(task, Colors.YELLOW)}")
                has_tasks = True
            elif stripped.startswith("- [x]"):
                task = stripped[5:].strip()
                print(f"  [x] {Colors.colored(task, Colors.GREEN)}")
                has_tasks = True
            elif stripped.startswith("- ") and not stripped.startswith("- ["):
                task = stripped[2:].strip()
                print(f"  [ ] {Colors.colored(task, Colors.YELLOW)}")
                has_tasks = True
        if not has_tasks:
            print("  (no active tasks in fix_plan.md)")
        print()
    else:
        print("Plan:  No fix_plan.md found (run 'mm init' or start a loop)")
        print()
        
    print(Colors.colored("Logs & State Files:", Colors.BOLD + Colors.CYAN))
    for name in ("error_log.txt", "verify_log.txt", "iteration.txt", "queue.log"):
        p = state / name
        status = Colors.colored("exists", Colors.GREEN) if p.exists() else Colors.colored("missing", Colors.YELLOW)
        print(f"  {name:<16}: {status}")
    return 0


def cmd_install_path() -> int:
    bin_dir = Path.home() / ".local" / "bin"
    install_dir = Path.home() / ".local" / "share" / "middle-manager"
    print(f'export PATH="{bin_dir}:$PATH"')
    print(f"# mm installed at {install_dir}")
    return 0


def cmd_issues(cfg: LoopConfig) -> int:
    if not cfg.issue_queue:
        print("Issue queue requires --label, --author, and/or --mode queue")
        return 1
    cfg.mode = "queue"
    return IssueQueueRunner(cfg).run()


def main(argv: list[str] | None = None) -> int:
    try:
        raw_argv = list(argv) if argv is not None else sys.argv[1:]
        raw_argv = _preprocess_argv(raw_argv)

        if raw_argv and raw_argv[0] == "install-path":
            return cmd_install_path()

        args, cfg = parse_args(raw_argv)

        if args.command == "install-path":
            return cmd_install_path()
        if args.command == "agents":
            return cmd_agents(cfg)
        if args.command == "init":
            return cmd_init(cfg)
        if args.command == "status":
            return cmd_status(cfg)
        if args.command == "issues":
            return cmd_issues(cfg)

        if _should_wizard(args, raw_argv):
            wizard_cfg = run_wizard(cfg.repo)
            if wizard_cfg is None:
                return 1
            cfg = wizard_cfg

        if cfg.mode == "queue" and cfg.issue_queue:
            return IssueQueueRunner(cfg).run()

        if (getattr(args, "quick", False) or cfg.mode == "feature") and not cfg.mission:
            print("Quick/feature mode needs a mission. Examples:")
            print('  mm quick "add feature XYZ"')
            print('  mm "add dark mode toggle"')
            return 1

        loop = MiddleManagerLoop(cfg)
        return loop.run()
    except KeyboardInterrupt:
        from .colors import Colors
        print()
        print(Colors.colored("┌────────────────────────────────────────────────────────┐", Colors.YELLOW + Colors.BOLD))
        print(Colors.colored("│               👋  MIDDLE MANAGER GOODBYE                │", Colors.YELLOW + Colors.BOLD))
        print(Colors.colored("├────────────────────────────────────────────────────────┤", Colors.YELLOW + Colors.BOLD))
        print(Colors.colored("│  Wizard/Loop exited by user. No changes were forced.   │", Colors.YELLOW))
        print(Colors.colored("└────────────────────────────────────────────────────────┘", Colors.YELLOW + Colors.BOLD))
        print()
        return 130