"""Prompt template loading and rendering."""

from __future__ import annotations

from pathlib import Path

PACKAGE_DIR = Path(__file__).resolve().parent
PROMPTS_DIR = PACKAGE_DIR / "prompts"


def load_prompt(name: str, overrides: dict[str, Path] | None = None) -> str:
    if overrides and name in overrides:
        return overrides[name].read_text(encoding="utf-8")
    path = PROMPTS_DIR / f"{name}.md"
    if not path.exists():
        raise FileNotFoundError(f"Prompt template not found: {path}")
    return path.read_text(encoding="utf-8")


def render_prompt(template: str, **kwargs: str) -> str:
    try:
        return template.format(**kwargs)
    except KeyError as exc:
        missing = str(exc).strip("'")
        return template.replace("{" + missing + "}", "")


def build_context(
    *,
    repo: Path,
    issue: str | None,
    fix_plan: str,
    top_item: str,
    agent_memory: str,
    test_output: str,
    error_log: str,
    iteration: int,
) -> dict[str, str]:
    return {
        "repo": str(repo),
        "issue": issue or "none",
        "fix_plan": fix_plan,
        "top_item": top_item,
        "agent_memory": agent_memory,
        "test_output": test_output,
        "error_log": error_log,
        "iteration": str(iteration),
    }