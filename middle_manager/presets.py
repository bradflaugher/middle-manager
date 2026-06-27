"""One-shot presets for common workflows."""

from __future__ import annotations

import shutil
from pathlib import Path

from .agents import autodetect_step_agents
from .config import LoopConfig


def apply_quick_preset(cfg: LoopConfig) -> None:
    """3-agent stack tuned for 'add feature XYZ' style missions."""
    cfg.steps = 3
    cfg.commit.enabled = False
    cfg.mode = "feature"
    if cfg.max_iterations == 10:  # default — tighten for single features
        cfg.max_iterations = 5

    detected = autodetect_step_agents(cfg.binary_overrides)
    for step in ("discover", "execute", "verify"):
        sc = cfg.step_for(step)
        sc.agent = detected[step]
        sc.model = None
        sc.extra_args = ["--check"] if step in ("discover", "verify") and sc.agent == "grok" else []


def reset_loop_state(cfg: LoopConfig) -> None:
    """Clear per-run state so a new mission starts clean."""
    state = cfg.state_path()
    for name in (
        "fix_plan.md",
        "iteration.txt",
        "error_log.txt",
        "verify_log.txt",
        "discover_prompt.md",
        "execute_prompt.md",
        "verify_prompt.md",
        "session.log",
    ):
        path = state / name
        if path.exists():
            path.unlink()
    issues_dir = state / "issues"
    if issues_dir.exists():
        shutil.rmtree(issues_dir)


def seed_feature_plan(cfg: LoopConfig, path: Path) -> None:
    """Write a fix_plan with the mission as the top task."""
    mission = (cfg.mission or "").strip()
    if not mission:
        return
    body = (
        "# fix_plan.md\n\n"
        f"## Feature\n\n{mission}\n\n"
        "## Tasks\n\n"
        f"- [ ] {mission}\n"
    )
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(body, encoding="utf-8")