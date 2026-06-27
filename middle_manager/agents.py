"""Agent backends and command builders for coding CLIs."""

from __future__ import annotations

import os
import shutil
import subprocess
from dataclasses import dataclass, field
from pathlib import Path
from typing import Sequence

AGENT_NAMES = ("grok", "claude", "codex", "crush", "opencode", "agy")


@dataclass(frozen=True)
class AgentSpec:
    name: str
    binary: str
    yolo_flag: str
    yolo_position: str = "before_prompt"  # before_prompt | after_binary | extra
    prompt_mode: str = "arg"  # arg | stdin | print_flag
    print_flag: str | None = None
    model_flag: str | None = "-m"
    cwd_flag: str | None = "--cwd"
    prompt_file_flag: str | None = None
    subcommand: tuple[str, ...] = ()
    extra_yolo: tuple[str, ...] = ()
    notes: str = ""


# YOLO / permission-skipping flags per agent (verified against upstream docs where possible).
AGENT_SPECS: dict[str, AgentSpec] = {
    "grok": AgentSpec(
        name="grok",
        binary="grok",
        yolo_flag="--yolo",
        prompt_mode="arg",
        print_flag="-p",
        model_flag="-m",
        cwd_flag="--cwd",
        prompt_file_flag="--prompt-file",
        extra_yolo=("--always-approve",),  # alias; grok accepts both
        notes="Headless: grok -p PROMPT --yolo --cwd DIR. Alias: --always-approve",
    ),
    "claude": AgentSpec(
        name="claude",
        binary="claude",
        yolo_flag="--dangerously-skip-permissions",
        prompt_mode="arg",
        print_flag="-p",
        model_flag="--model",
        cwd_flag=None,
        notes="Run from target repo cwd. Also: --permission-mode bypassPermissions",
    ),
    "codex": AgentSpec(
        name="codex",
        binary="codex",
        yolo_flag="--yolo",
        subcommand=("exec",),
        prompt_mode="arg",
        model_flag="-m",
        cwd_flag=None,
        notes="OpenAI Codex CLI: codex exec PROMPT --yolo. Also: --full-auto",
    ),
    "crush": AgentSpec(
        name="crush",
        binary="crush",
        yolo_flag="-y",
        yolo_position="before_subcommand",
        prompt_mode="arg",
        subcommand=("run",),
        model_flag="-m",
        cwd_flag="-c",
        notes="Global flag before subcommand: crush -y run PROMPT -c DIR",
    ),
    "opencode": AgentSpec(
        name="opencode",
        binary="opencode",
        yolo_flag="--dangerously-skip-permissions",
        subcommand=("run",),
        prompt_mode="arg",
        model_flag="-m",
        cwd_flag="--dir",
        notes="opencode run PROMPT --dangerously-skip-permissions --dir DIR",
    ),
    "agy": AgentSpec(
        name="agy",
        binary="agy",
        yolo_flag="--dangerously-skip-permissions",
        prompt_mode="print_flag",
        print_flag="--print",
        model_flag="--model",
        cwd_flag=None,
        notes="agy --print PROMPT --dangerously-skip-permissions",
    ),
}


@dataclass
class AgentRun:
    agent: str
    command: list[str]
    prompt: str
    cwd: Path
    model: str | None = None
    yolo: bool = True
    extra_args: list[str] = field(default_factory=list)
    env: dict[str, str] = field(default_factory=dict)
    timeout: int | None = None


def resolve_binary(name: str, override: str | None = None) -> str | None:
    if override:
        return override if shutil.which(override) or Path(override).exists() else None
    spec = AGENT_SPECS.get(name)
    if not spec:
        return None
    return spec.binary if shutil.which(spec.binary) else None


def agent_available(name: str, binary_override: str | None = None) -> bool:
    return resolve_binary(name, binary_override) is not None


def build_command(
    agent: str,
    prompt: str,
    *,
    cwd: Path,
    model: str | None = None,
    yolo: bool = True,
    extra_args: Sequence[str] | None = None,
    binary_override: str | None = None,
    prompt_file: Path | None = None,
    interactive: bool = False,
) -> AgentRun:
    if agent not in AGENT_SPECS:
        raise ValueError(f"Unknown agent {agent!r}. Choose from: {', '.join(AGENT_NAMES)}")

    spec = AGENT_SPECS[agent]
    binary = binary_override or spec.binary
    cmd: list[str] = [binary]
    extras = list(extra_args or [])

    if yolo and spec.yolo_position == "before_subcommand":
        cmd.append(spec.yolo_flag)

    cmd.extend(spec.subcommand)

    if yolo and spec.yolo_position in ("after_binary", "before_prompt"):
        cmd.append(spec.yolo_flag)

    if spec.yolo_position == "extra" and yolo:
        cmd.extend(spec.extra_yolo)

    if interactive and agent in ("grok", "claude", "crush", "opencode", "agy"):
        if prompt_file and spec.prompt_file_flag:
            cmd.extend([spec.prompt_file_flag, str(prompt_file)])
        elif agent == "agy":
            cmd.extend(["--prompt-interactive", prompt])
        elif spec.prompt_mode == "arg":
            cmd.append(prompt)
        cmd.extend(extras)
        if model and spec.model_flag:
            cmd.extend([spec.model_flag, model])
        if spec.cwd_flag:
            cmd.extend([spec.cwd_flag, str(cwd)])
        return AgentRun(agent=agent, command=cmd, prompt=prompt, cwd=cwd, model=model, yolo=yolo, extra_args=list(extras))

    use_prompt_file = prompt_file and spec.prompt_file_flag and spec.prompt_mode != "print_flag"

    if use_prompt_file:
        cmd.extend([spec.prompt_file_flag, str(prompt_file)])
    elif spec.prompt_mode == "print_flag" and spec.print_flag:
        cmd.extend([spec.print_flag, prompt])
    elif spec.prompt_mode == "arg":
        if spec.print_flag:
            cmd.append(spec.print_flag)
        cmd.append(prompt)

    if model and spec.model_flag:
        cmd.extend([spec.model_flag, model])

    if spec.cwd_flag:
        cmd.extend([spec.cwd_flag, str(cwd)])

    if yolo and spec.yolo_position not in ("before_subcommand", "after_binary", "before_prompt", "extra"):
        cmd.append(spec.yolo_flag)

    # opencode/crush: yolo often works as trailing flag too
    if yolo and agent == "opencode" and spec.yolo_flag not in cmd:
        cmd.append(spec.yolo_flag)

    cmd.extend(extras)

    env = os.environ.copy()
    if agent == "claude" and yolo and spec.yolo_flag not in cmd:
        cmd.append(spec.yolo_flag)

    return AgentRun(
        agent=agent,
        command=cmd,
        prompt=prompt,
        cwd=cwd,
        model=model,
        yolo=yolo,
        extra_args=list(extras),
        env=env,
    )


def run_agent(run: AgentRun, *, dry_run: bool = False, stream: bool = True) -> subprocess.CompletedProcess[str]:
    display = " ".join(_quote(a) for a in run.command)
    print(f"\n{'[dry-run] ' if dry_run else ''}▶ {run.agent}: {display}\n")

    if dry_run:
        return subprocess.CompletedProcess(run.command, 0, stdout="", stderr="")

    proc = subprocess.Popen(
        run.command,
        cwd=run.cwd,
        env=run.env,
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
        text=True,
        bufsize=1,
    )
    output_lines: list[str] = []
    assert proc.stdout is not None
    for line in proc.stdout:
        output_lines.append(line)
        if stream:
            print(line, end="", flush=True)
    proc.wait(timeout=run.timeout)
    return subprocess.CompletedProcess(
        run.command,
        proc.returncode or 0,
        stdout="".join(output_lines),
        stderr="",
    )


def _quote(arg: str) -> str:
    if not arg or any(c in arg for c in " \t\"'$\\"):
        return '"' + arg.replace("\\", "\\\\").replace('"', '\\"') + '"'
    return arg


def list_agents_status(binary_overrides: dict[str, str] | None = None) -> list[dict[str, str]]:
    overrides = binary_overrides or {}
    rows = []
    for name in AGENT_NAMES:
        spec = AGENT_SPECS[name]
        path = resolve_binary(name, overrides.get(name))
        rows.append(
            {
                "agent": name,
                "binary": path or spec.binary,
                "available": "yes" if path else "no",
                "yolo": spec.yolo_flag,
                "notes": spec.notes,
            }
        )
    return rows


# Preference order per step when autodetecting installed agents.
STEP_AGENT_PRIORITY: dict[str, tuple[str, ...]] = {
    "discover": ("grok", "claude", "crush", "opencode", "agy", "codex"),
    "execute": ("claude", "grok", "opencode", "crush", "agy", "codex"),
    "verify": ("codex", "grok", "claude", "opencode", "crush", "agy"),
    "commit": ("agy", "grok", "claude", "opencode", "crush", "codex"),
}


def available_agents(binary_overrides: dict[str, str] | None = None) -> list[str]:
    overrides = binary_overrides or {}
    return [name for name in AGENT_NAMES if agent_available(name, overrides.get(name))]


def autodetect_agent(
    step: str,
    binary_overrides: dict[str, str] | None = None,
    fallback: str = "grok",
) -> str:
    overrides = binary_overrides or {}
    for name in STEP_AGENT_PRIORITY.get(step, AGENT_NAMES):
        if agent_available(name, overrides.get(name)):
            return name
    return fallback


def autodetect_step_agents(binary_overrides: dict[str, str] | None = None) -> dict[str, str]:
    return {step: autodetect_agent(step, binary_overrides) for step in STEP_AGENT_PRIORITY}