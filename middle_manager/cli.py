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


def _should_wizard(args, argv: list[str] | None) -> bool:
    if args.no_wizard:
        return False
    if args.wizard:
        return True
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
    }
    if any(a in flags or a.split("=")[0] in flags for a in argv):
        return False
    return sys.stdin.isatty()


def cmd_agents(cfg: LoopConfig) -> int:
    rows = list_agents_status(cfg.binary_overrides)
    print(f"{'AGENT':<10} {'AVAILABLE':<10} {'BINARY':<24} YOLO FLAG")
    print("-" * 72)
    for row in rows:
        print(f"{row['agent']:<10} {row['available']:<10} {row['binary']:<24} {row['yolo']}")
        if row["notes"]:
            print(f"           {row['notes']}")
    return 0


def cmd_init(cfg: LoopConfig) -> int:
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
        print(f"created: {dest}")
    print(f"State dir: {state}")
    return 0


def cmd_status(cfg: LoopConfig) -> int:
    state = cfg.state_path()
    print(f"Repo: {cfg.repo}")
    print(f"Git: {'yes' if repo_is_git(cfg.repo) else 'no'}")
    print(f"Mode: {cfg.mode}")
    print(f"State: {state}")
    for name in ("fix_plan.md", "error_log.txt", "verify_log.txt", "iteration.txt", "queue.log"):
        p = state / name
        print(f"  {name}: {'yes' if p.exists() else 'no'}")
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
    raw_argv = list(argv) if argv is not None else sys.argv[1:]

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
        wizard_cfg = run_wizard(cfg.repo if cfg.repo != Path.cwd() else None)
        if wizard_cfg is None:
            return 1
        cfg = wizard_cfg

    if cfg.mode == "queue" and cfg.issue_queue:
        return IssueQueueRunner(cfg).run()

    loop = MiddleManagerLoop(cfg)
    return loop.run()