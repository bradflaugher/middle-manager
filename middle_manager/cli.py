"""CLI entry and subcommands."""

from __future__ import annotations

import json
import shutil
from pathlib import Path

from .agents import list_agents_status
from .config import DEFAULT_CONFIG_PATH, LoopConfig, parse_args
from .git_ops import repo_is_git
from .loop import MiddleManagerLoop


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
    print(f"State: {state}")
    for name in ("fix_plan.md", "error_log.txt", "verify_log.txt", "iteration.txt"):
        p = state / name
        print(f"  {name}: {'yes' if p.exists() else 'no'}")
    return 0


def main(argv: list[str] | None = None) -> int:
    args, cfg = parse_args(argv)

    if args.command == "agents":
        return cmd_agents(cfg)
    if args.command == "init":
        return cmd_init(cfg)
    if args.command == "status":
        return cmd_status(cfg)

    loop = MiddleManagerLoop(cfg)
    return loop.run()