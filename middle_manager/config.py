"""Configuration loading and CLI argument parsing."""

from __future__ import annotations

import shutil

HAS_TMUX = shutil.which("tmux") is not None

import argparse
import json
from copy import deepcopy
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

from .agents import AGENT_NAMES

PACKAGE_DIR = Path(__file__).resolve().parent
DEFAULT_CONFIG_PATH = PACKAGE_DIR.parent / "config.default.json"


@dataclass
class IssueQueueConfig:
    label: str | None = None
    author: str | None = None
    state: str = "open"
    limit: int = 20
    close_on_success: bool = True
    close_comment: str = "Closed by middle-manager — fix verified and PR opened."


@dataclass
class StepConfig:
    agent: str
    model: str | None = None
    extra_args: list[str] = field(default_factory=list)
    prompt_file: str | None = None
    enabled: bool = True


@dataclass
class LoopConfig:
    repo: Path
    steps: int = 4
    max_iterations: int = 10
    yolo: bool = True
    dry_run: bool = False
    interactive: bool = False
    issue: str | None = None
    mode: str = "repair"  # repair | issue | queue | feature
    mission: str | None = None
    fresh: bool = False
    issue_queue: IssueQueueConfig | None = None
    branch_prefix: str = "mm"
    no_pr: bool = False
    no_merge: bool = True

    discover: StepConfig = field(default_factory=lambda: StepConfig("grok"))
    execute: StepConfig = field(default_factory=lambda: StepConfig("claude"))
    verify: StepConfig = field(default_factory=lambda: StepConfig("codex"))
    commit: StepConfig = field(default_factory=lambda: StepConfig("agy"))
    binary_overrides: dict[str, str] = field(default_factory=dict)
    state_dir: Path | None = None
    agent_memory_file: str = "AGENT.md"
    fix_unrelated_tests: bool = False
    stream_output: bool = False
    tmux: bool = HAS_TMUX
    batch_size: int = 1

    def step_for(self, name: str) -> StepConfig:
        return getattr(self, name)

    def active_steps(self) -> list[str]:
        names = ["discover", "execute", "verify", "commit"][: self.steps]
        return [n for n in names if self.step_for(n).enabled]

    def state_path(self) -> Path:
        base = self.state_dir or (self.repo / ".middle-manager")
        base.mkdir(parents=True, exist_ok=True)
        return base


DEFAULTS: dict[str, Any] = {
    "steps": 4,
    "max_iterations": 10,
    "yolo": True,
    "branch_prefix": "mm",
    "no_merge": True,

    "fix_unrelated_tests": False,
    "stream_output": False,
    "tmux": HAS_TMUX,
    "batch_size": 1,
    "discover": {"agent": "grok", "model": None, "extra_args": ["--check"]},
    "execute": {"agent": "claude", "model": "claude-sonnet-4-20250514"},
    "verify": {"agent": "codex", "model": "o4-mini"},
    "commit": {"agent": "agy", "model": None},
}


def load_json_config(path: Path | None) -> dict[str, Any]:
    if not path or not path.exists():
        return {}
    with path.open(encoding="utf-8") as f:
        return json.load(f)


STEP_KEYS = ("discover", "execute", "verify", "commit")


def merge_config(base: dict[str, Any], override: dict[str, Any]) -> dict[str, Any]:
    out = deepcopy(base)
    for key, value in override.items():
        if key in STEP_KEYS and isinstance(value, dict):
            # Step blocks replace wholesale — avoid leaking default models across agents.
            out[key] = deepcopy(value)
        elif isinstance(value, dict) and isinstance(out.get(key), dict):
            out[key] = {**out[key], **value}
        else:
            out[key] = value
    return out


def step_from_dict(data: dict[str, Any]) -> StepConfig:
    return StepConfig(
        agent=data.get("agent", "grok"),
        model=data.get("model"),
        extra_args=list(data.get("extra_args", [])),
        prompt_file=data.get("prompt_file"),
        enabled=data.get("enabled", True),
    )


def config_from_dict(data: dict[str, Any], repo: Path) -> LoopConfig:
    return LoopConfig(
        repo=repo.resolve(),
        steps=int(data.get("steps", 4)),
        max_iterations=int(data.get("max_iterations", 10)),
        yolo=bool(data.get("yolo", True)),
        branch_prefix=str(data.get("branch_prefix", "mm")),
        no_merge=bool(data.get("no_merge", True)),

        discover=step_from_dict(data.get("discover", DEFAULTS["discover"])),
        execute=step_from_dict(data.get("execute", DEFAULTS["execute"])),
        verify=step_from_dict(data.get("verify", DEFAULTS["verify"])),
        commit=step_from_dict(data.get("commit", DEFAULTS["commit"])),
        binary_overrides=dict(data.get("binary_overrides", {})),
        agent_memory_file=str(data.get("agent_memory_file", "AGENT.md")),
        fix_unrelated_tests=bool(data.get("fix_unrelated_tests", False)),
        stream_output=bool(data.get("stream_output", False)),
        tmux=bool(data.get("tmux", HAS_TMUX)),
        batch_size=int(data.get("batch_size", 1)),
    )


def build_parser() -> argparse.ArgumentParser:
    p = argparse.ArgumentParser(
        prog="middle-manager",
        description="Multi-agent YOLO coding loop for GitHub issues and codebase repair.",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="""
Examples:
  mm quick "add feature XYZ"              # simplest: 3 agents + your prompt
  mm "add dark mode toggle"               # same thing (shorthand)
  mm -m "add feature XYZ" --steps 3 --no-wizard
  mm --repo ~/bradflaugher.com --dry-run
  mm agents
        """,
    )
    p.add_argument(
        "command",
        nargs="?",
        default=None,
        choices=["run", "quick", "agents", "init", "status", "issues", "install-path"],
    )
    p.add_argument("prompt", nargs="*", help="Mission text (with quick command or bare mm \"...\")")
    p.add_argument("--repo", "-C", type=Path, default=Path.cwd(), help="Target repository")
    p.add_argument("--config", type=Path, help="JSON config file")
    p.add_argument("--steps", type=int, choices=[3, 4], help="3-step (no commit agent) or 4-step loop")
    p.add_argument("--max-iterations", type=int, help="Max loop iterations")
    p.add_argument("--issue", help="GitHub issue number or URL to focus on")
    p.add_argument("--mission", "-m", help="Mission prompt injected into agent context")
    p.add_argument("--mode", choices=["repair", "issue", "queue", "feature"], help="Run mode")
    p.add_argument("--quick", "-q", action="store_true", help="3-agent feature preset (discover→execute→verify)")
    p.add_argument("--fresh", action="store_true", help="Reset .middle-manager state for a new mission")
    p.add_argument("--label", help="Issue queue: filter by label")
    p.add_argument("--author", help="Issue queue: filter by author (@user)")
    p.add_argument("--issue-limit", type=int, default=None, help="Issue queue: max issues")
    p.add_argument("--close-issues", action="store_true", help="Close issues when queue item succeeds")
    p.add_argument("--no-close-issues", action="store_true", help="Do not close issues after success")
    p.add_argument("--wizard", action="store_true", help="Force interactive wizard")
    p.add_argument("--no-wizard", action="store_true", help="Skip wizard even with no args")
    p.add_argument("--yolo", dest="yolo", action="store_true", default=None, help="Enable YOLO mode (default)")
    p.add_argument("--no-yolo", dest="yolo", action="store_false", help="Disable auto-approve flags")
    p.add_argument("--dry-run", action="store_true", help="Print commands without executing agents")
    p.add_argument("--interactive", "-i", action="store_true", help="Interactive menu between steps")

    p.add_argument("--branch-prefix", default=None)
    p.add_argument("--no-pr", action="store_true", help="Skip PR creation")
    p.add_argument("--state-dir", type=Path)
    p.add_argument("--fix-unrelated-tests", action="store_true", help="Allow agents to modify tests or other files to fix unrelated failures.")
    p.add_argument("--stream-output", action="store_true", help="Stream raw agent stdout/stderr to console instead of using the monitor")
    p.add_argument("--tmux", dest="tmux", action="store_true", default=None, help="Run agent commands inside a tmux session (default: True if tmux is installed)")
    p.add_argument("--no-tmux", dest="tmux", action="store_false", help="Disable running agent commands inside a tmux session")
    p.add_argument("--batch-size", type=int, default=None, help="Number of tasks to execute in a single iteration")

    for step in ("discover", "execute", "verify", "commit"):
        p.add_argument(f"--{step}-agent", choices=AGENT_NAMES)
        p.add_argument(f"--{step}-model")
        p.add_argument(f"--{step}-args", help=f"Extra CLI args for {step} (comma-separated)")

    p.add_argument("--binary", action="append", metavar="AGENT=PATH", help="Override agent binary path")
    return p


def parse_args(argv: list[str] | None = None) -> tuple[argparse.Namespace, LoopConfig]:
    parser = build_parser()
    argv = list(argv) if argv is not None else None
    args = parser.parse_args(argv)

    if args.command == "quick":
        args.quick = True
        args.command = "run"
        if not args.no_wizard:
            args.no_wizard = True
        if not args.fresh:
            args.fresh = True
    if args.command is None:
        args.command = "run"

    file_cfg = merge_config(DEFAULTS, load_json_config(DEFAULT_CONFIG_PATH))
    if args.config:
        file_cfg = merge_config(file_cfg, load_json_config(args.config))

    cfg = config_from_dict(file_cfg, args.repo)

    if args.steps is not None:
        cfg.steps = args.steps
        if args.steps == 3:
            cfg.commit.enabled = False
    if args.max_iterations is not None:
        cfg.max_iterations = args.max_iterations
    if args.issue:
        cfg.issue = args.issue
        cfg.mode = "issue"
    if args.mode:
        cfg.mode = args.mode
    if getattr(args, "prompt", None):
        prompt_text = " ".join(args.prompt).strip()
        if prompt_text and not args.mission:
            args.mission = prompt_text
    if args.mission:
        cfg.mission = args.mission
    if args.quick or args.command == "quick":
        from .presets import apply_quick_preset

        apply_quick_preset(cfg)
        if not cfg.mission and getattr(args, "prompt", None):
            cfg.mission = " ".join(args.prompt).strip() or None
    if args.fresh:
        cfg.fresh = True
    if args.label or args.author or args.issue_limit is not None:
        cfg.mode = "queue"
        cfg.issue_queue = cfg.issue_queue or IssueQueueConfig()
        if args.label:
            cfg.issue_queue.label = args.label
        if args.author:
            cfg.issue_queue.author = args.author
        if args.issue_limit is not None:
            cfg.issue_queue.limit = args.issue_limit
    if args.close_issues:
        if cfg.issue_queue is None:
            cfg.issue_queue = IssueQueueConfig()
        cfg.issue_queue.close_on_success = True
    if args.no_close_issues:
        if cfg.issue_queue is None:
            cfg.issue_queue = IssueQueueConfig()
        cfg.issue_queue.close_on_success = False
    if args.yolo is not None:
        cfg.yolo = args.yolo
    if args.dry_run:
        cfg.dry_run = True
    if args.interactive:
        cfg.interactive = True

    if args.branch_prefix:
        cfg.branch_prefix = args.branch_prefix
    if args.no_pr:
        cfg.no_pr = True
    if args.state_dir:
        cfg.state_dir = args.state_dir
    if args.fix_unrelated_tests:
        cfg.fix_unrelated_tests = True
    if args.stream_output:
        cfg.stream_output = True
    if args.tmux is not None:
        cfg.tmux = args.tmux
    if args.batch_size is not None:
        cfg.batch_size = args.batch_size

    for step in ("discover", "execute", "verify", "commit"):
        agent = getattr(args, f"{step}_agent")
        model = getattr(args, f"{step}_model")
        extra = getattr(args, f"{step}_args")
        sc: StepConfig = cfg.step_for(step)
        if agent:
            sc.agent = agent
        if model:
            sc.model = model
        if extra:
            sc.extra_args.extend(_split_args(extra))

    if args.binary:
        for item in args.binary:
            if "=" not in item:
                parser.error(f"--binary expects AGENT=PATH, got {item!r}")
            name, path = item.split("=", 1)
            cfg.binary_overrides[name] = path

    # Autodetect step agents if defaults are missing and no explicit CLI flags were provided
    from .agents import agent_available, autodetect_agent
    for step in ("discover", "execute", "verify", "commit"):
        cmd_arg_agent = getattr(args, f"{step}_agent")
        sc = cfg.step_for(step)
        if not cmd_arg_agent:
            binary = cfg.binary_overrides.get(sc.agent)
            if not agent_available(sc.agent, binary):
                detected = autodetect_agent(step, cfg.binary_overrides, fallback=sc.agent)
                if detected != sc.agent:
                    sc.agent = detected
                    sc.model = None  # Clear default model from different agent

    return args, cfg


def _split_args(value: str) -> list[str]:
    return [part.strip() for part in value.split(",") if part.strip()]
