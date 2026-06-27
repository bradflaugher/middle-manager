"""Interactive menu for stepping through the loop."""

from __future__ import annotations

from .config import LoopConfig
from .agents import list_agents_status


def pause(cfg: LoopConfig, step: str) -> str:
    from .colors import Colors
    print("\n" + Colors.colored("=" * 60, Colors.CYAN))
    print(Colors.colored(f"  INTERACTIVE — before step: {step}", Colors.BOLD + Colors.CYAN))
    print(Colors.colored("=" * 60, Colors.CYAN))
    print(f"  Repo: {cfg.repo}")
    print(f"  Steps: {cfg.steps} | YOLO: {cfg.yolo} | dry-run: {cfg.dry_run}")
    print()
    print(f"  [{Colors.colored('c', Colors.GREEN)}] continue   [{Colors.colored('s', Colors.YELLOW)}] skip next step   [{Colors.colored('q', Colors.RED)}] quit   [{Colors.colored('a', Colors.CYAN)}] show agents")
    print(f"  [{Colors.colored('p', Colors.MAGENTA)}] print config")
    print()
    while True:
        choice = input("middle-manager> ").strip().lower() or "c"
        if choice in ("c", "continue", ""):
            return "continue"
        if choice in ("s", "skip"):
            return "skip"
        if choice in ("q", "quit", "exit"):
            return "quit"
        if choice in ("a", "agents"):
            for row in list_agents_status(cfg.binary_overrides):
                mark = Colors.colored("✓", Colors.GREEN) if row["available"] == "yes" else Colors.colored("✗", Colors.RED)
                print(f"  {mark} {row['agent']:10} {row['binary']:20} yolo={row['yolo']}")
            continue
        if choice in ("p", "config"):
            for name in ("discover", "execute", "verify", "commit"):
                sc = cfg.step_for(name)
                if not sc.enabled:
                    continue
                model = sc.model or "(default)"
                print(f"  {name:10} agent={sc.agent} model={model} args={sc.extra_args}")
            continue
        print("  Unknown choice. Try c/s/q/a/p")