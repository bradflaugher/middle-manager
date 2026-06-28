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
    print(f"  [{Colors.colored('i', Colors.MAGENTA)}] interject instructions   [{Colors.colored('p', Colors.MAGENTA)}] print config   [{Colors.colored('!<cmd>', Colors.BOLD)}] run shell command")
    print()
    while True:
        choice = input("middle-manager> ").strip()
        if not choice:
            return "continue"
        
        # Check shell commands
        if choice.startswith("!"):
            import subprocess
            cmd_str = choice[1:].strip()
            if cmd_str:
                try:
                    subprocess.run(cmd_str, shell=True, cwd=str(cfg.repo))
                except Exception as e:
                    print(f"  Error running command: {e}")
            else:
                print("  No command specified. Usage: !<command>")
            continue
            
        choice_lower = choice.lower()
        if choice_lower in ("c", "continue"):
            return "continue"
        if choice_lower in ("s", "skip"):
            return "skip"
        if choice_lower in ("q", "quit", "exit"):
            return "quit"
        if choice_lower in ("a", "agents"):
            for row in list_agents_status(cfg.binary_overrides):
                mark = Colors.colored("✓", Colors.GREEN) if row["available"] == "yes" else Colors.colored("✗", Colors.RED)
                print(f"  {mark} {row['agent']:10} {row['binary']:20} yolo={row['yolo']}")
            continue
        if choice_lower in ("p", "config"):
            for name in ("discover", "execute", "verify", "commit"):
                sc = cfg.step_for(name)
                if not sc.enabled:
                    continue
                model = sc.model or "(default)"
                print(f"  {name:10} agent={sc.agent} model={model} args={sc.extra_args}")
            continue
        if choice_lower in ("i", "interject"):
            custom_text = input("Enter custom instruction to append to next step's prompt: ").strip()
            if custom_text:
                sc = cfg.step_for(step)
                sc.custom_interjection = custom_text
                print(f"  Added custom instruction to step '{step}': \"{custom_text}\"")
            continue
            
        print("  Unknown choice. Try c/s/q/a/p/i or !<cmd>")