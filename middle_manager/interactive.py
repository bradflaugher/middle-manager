"""Interactive menu for stepping through the loop."""

from __future__ import annotations

from .config import LoopConfig
from .agents import list_agents_status


def pause(cfg: LoopConfig, step: str) -> str:
    print("\n" + "=" * 60)
    print(f"  INTERACTIVE — before step: {step}")
    print("=" * 60)
    print(f"  Repo: {cfg.repo}")
    print(f"  Steps: {cfg.steps} | YOLO: {cfg.yolo} | dry-run: {cfg.dry_run}")
    print()
    print("  [c] continue   [s] skip next step   [q] quit   [a] show agents")
    print("  [p] print config")
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
                mark = "✓" if row["available"] == "yes" else "✗"
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