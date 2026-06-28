"""Agent backends and command builders for coding CLIs."""

from __future__ import annotations

import os
import shutil
import subprocess
from dataclasses import dataclass, field
from pathlib import Path
from typing import Sequence

AGENT_NAMES = ("grok", "claude", "codex", "opencode", "agy")


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
    interactive: bool = False


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

    env = os.environ.copy()

    spec = AGENT_SPECS[agent]
    binary = binary_override or spec.binary
    cmd: list[str] = [binary]
    extras = list(extra_args or [])

    if yolo and spec.yolo_position == "before_subcommand":
        if spec.yolo_flag:
            cmd.append(spec.yolo_flag)

    cmd.extend(spec.subcommand)

    if yolo and spec.yolo_position in ("after_binary", "before_prompt"):
        if spec.yolo_flag:
            cmd.append(spec.yolo_flag)

    if spec.yolo_position == "extra" and yolo:
        cmd.extend(spec.extra_yolo)

    if interactive and agent in ("grok", "claude", "opencode", "agy"):
        if agent == "agy":
            cmd.extend(["--prompt-interactive", prompt])
        elif spec.prompt_mode == "arg":
            cmd.append(prompt)
        cmd.extend(extras)
        if model and spec.model_flag:
            cmd.extend([spec.model_flag, model])
        if spec.cwd_flag:
            cmd.extend([spec.cwd_flag, str(cwd)])
        return AgentRun(
            agent=agent,
            command=cmd,
            prompt=prompt,
            cwd=cwd,
            model=model,
            yolo=yolo,
            extra_args=list(extras),
            env=env,
            interactive=interactive,
        )

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
        if spec.yolo_flag:
            cmd.append(spec.yolo_flag)

    # opencode: yolo often works as trailing flag too
    if yolo and agent == "opencode" and spec.yolo_flag and spec.yolo_flag not in cmd:
        cmd.append(spec.yolo_flag)

    cmd.extend(extras)

    if agent == "claude" and yolo and spec.yolo_flag and spec.yolo_flag not in cmd:
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
        interactive=interactive,
    )


def get_process_tree_cpu_ticks(parent_pid: int) -> float | None:
    import os
    from pathlib import Path
    try:
        pids = []
        for p in Path("/proc").iterdir():
            if p.is_dir() and p.name.isdigit():
                pids.append(int(p.name))
        
        ppid_map = {}
        pid_stats = {}
        for pid in pids:
            try:
                stat_path = Path(f"/proc/{pid}/stat")
                content = stat_path.read_text(encoding="utf-8")
                rpar_idx = content.rfind(")")
                if rpar_idx == -1:
                    continue
                fields = content[rpar_idx + 2:].split()
                ppid = int(fields[1])  # PPID
                utime = int(fields[11])  # utime
                stime = int(fields[12])  # stime
                ppid_map[pid] = ppid
                pid_stats[pid] = utime + stime
            except Exception:
                pass
        
        descendants = {parent_pid}
        changed = True
        while changed:
            changed = False
            for pid, ppid in ppid_map.items():
                if ppid in descendants and pid not in descendants:
                    descendants.add(pid)
                    changed = True
                    
        total_ticks = 0
        for pid in descendants:
            total_ticks += pid_stats.get(pid, 0)
        return float(total_ticks)
    except Exception:
        return None


def get_process_tree_stats(parent_pid: int) -> tuple[int, int]:
    import os
    from pathlib import Path
    try:
        pids = []
        for p in Path("/proc").iterdir():
            if p.is_dir() and p.name.isdigit():
                pids.append(int(p.name))

        ppid_map = {}
        for pid in pids:
            try:
                stat_path = Path(f"/proc/{pid}/stat")
                content = stat_path.read_text(encoding="utf-8")
                rpar_idx = content.rfind(")")
                if rpar_idx == -1:
                    continue
                fields = content[rpar_idx + 2:].split()
                ppid = int(fields[1])
                ppid_map[pid] = ppid
            except Exception:
                pass

        descendants = {parent_pid}
        changed = True
        while changed:
            changed = False
            for pid, ppid in ppid_map.items():
                if ppid in descendants and pid not in descendants:
                    descendants.add(pid)
                    changed = True

        total_sockets = 0
        for pid in descendants:
            fd_dir = Path(f"/proc/{pid}/fd")
            if fd_dir.is_dir():
                try:
                    for fd_link in fd_dir.iterdir():
                        try:
                            target = os.readlink(fd_link)
                            if target.startswith("socket:["):
                                total_sockets += 1
                        except Exception:
                            pass
                except Exception:
                    pass
        return len(descendants), total_sockets
    except Exception:
        return 1, 0



def get_changed_files_with_status(repo: Path) -> list[str]:
    import subprocess
    from pathlib import Path
    from .git_ops import repo_is_git
    try:
        if repo_is_git(repo):
            proc = subprocess.run(
                ["git", "status", "--porcelain"],
                cwd=repo,
                capture_output=True,
                text=True,
                check=False
            )
            if proc.returncode == 0:
                files = []
                for line in proc.stdout.splitlines():
                    if len(line) > 3:
                        status = line[:2].strip()
                        filename = line[3:].strip()
                        if " -> " in filename:
                            filename = filename.split(" -> ")[-1].strip()
                        
                        if status == "M":
                            status_desc = "modified"
                        elif status in ("A", "??"):
                            status_desc = "new"
                        elif status == "D":
                            status_desc = "deleted"
                        elif status == "R":
                            status_desc = "renamed"
                        else:
                            status_desc = "changed"
                            
                        files.append(f"{filename} ({status_desc})")
                return files
    except Exception:
        pass
    return []


def calculate_cpu_percent(pid: int, last_ticks: float | None, last_time: float) -> tuple[float | None, float | None, float]:
    import os
    import time
    current_time = time.time()
    dt = current_time - last_time
    if dt <= 0:
        return None, last_ticks, last_time
    
    current_ticks = get_process_tree_cpu_ticks(pid)
    if current_ticks is None or last_ticks is None:
        return 0.0, current_ticks, current_time
    
    d_ticks = current_ticks - last_ticks
    try:
        clk_tck = os.sysconf(os.sysconf_names['SC_CLK_TCK'])
    except Exception:
        clk_tck = 100
        
    cpu_percent = (d_ticks / (clk_tck * dt)) * 100.0
    return cpu_percent, current_ticks, current_time


def strip_ansi(text: str) -> str:
    import re
    ansi_escape = re.compile(r'\x1B(?:\][^\x07\x1b]*(?:\x07|\x1b\\)|\[[0-?]*[ -/]*[@-~]|[@-Z\\-_])')
    return ansi_escape.sub('', text)


def format_cmd_for_display(command: list[str] | str) -> str:
    import shlex
    if isinstance(command, str):
        if "\n" in command or len(command) > 150:
            lines = command.splitlines()
            first_line = lines[0] if lines else ""
            if len(first_line) > 120:
                first_line = first_line[:120] + "..."
            return f"{first_line} ... [truncated prompt]"
        return command

    formatted_args = []
    for arg in command:
        if "\n" in arg or len(arg) > 150:
            lines = arg.splitlines()
            first_line = lines[0] if lines else ""
            if len(first_line) > 120:
                first_line = first_line[:120] + "..."
            formatted_args.append(f"\"{first_line}... [truncated prompt]\"")
        else:
            formatted_args.append(shlex.quote(arg))
    return " ".join(formatted_args)


def get_acp_command(agent: str, binary_override: str | None = None) -> list[str]:
    import shutil
    binary = binary_override
    if agent == "grok":
        bin_name = binary or "grok"
        if not shutil.which(bin_name) and shutil.which("agent"):
            bin_name = "agent"
        return [bin_name, "agent", "stdio"]
    elif agent == "opencode":
        return [binary or "opencode", "acp"]
    elif agent == "claude":
        bin_name = binary or "claude"
        if shutil.which(bin_name):
            return [bin_name, "agent", "stdio"]
        return ["npx", "-y", "@agentclientprotocol/claude-agent-acp"]
    elif agent == "codex":
        return [binary or "codex", "app-server"]
    elif agent == "agy":
        if binary and binary != "agy" and shutil.which(binary):
            return [binary, "--acp"]
        import sys
        from pathlib import Path
        adapter_path = str(Path(__file__).parent / "agy_acp.py")
        return [sys.executable, adapter_path]
    return [binary or agent]


async def run_agent_acp(
    agent: str,
    prompt: str,
    cwd: Path,
    *,
    model: str | None = None,
    env: dict[str, str] | None = None,
    extra_args: list[str] | None = None,
    binary_override: str | None = None,
    step: str | None = None,
) -> str:
    import asyncio
    from acp import Client, connect_to_agent, text_block, PROTOCOL_VERSION, RequestError
    from acp.schema import (
        ClientCapabilities,
        Implementation,
        PermissionOption,
        RequestPermissionResponse,
        AllowedOutcome,
        AgentMessageChunk,
        AgentThoughtChunk,
    )
    from rich.console import Console
    from typing import Any

    console = Console()

    # Determine command
    cmd = get_acp_command(agent, binary_override)
    if model:
        if agent == "grok":
            cmd.extend(["-m", model])
        elif agent == "opencode":
            cmd.extend(["-m", model])
        elif agent == "agy":
            cmd.extend(["--model", model])

    label = f"{step.upper()} STEP ({agent.upper()})" if step else f"AGENT: {agent.upper()}"
    console.print(f"[bold cyan]Connecting to {label} via ACP...[/bold cyan]")
    console.print(f"   [cyan]Cwd:     {cwd}[/cyan]")
    console.print(f"   [cyan]Command: {' '.join(cmd)}[/cyan]\n")

    # Spawn agent process
    proc = await asyncio.create_subprocess_exec(
        cmd[0],
        *cmd[1:],
        stdin=asyncio.subprocess.PIPE,
        stdout=asyncio.subprocess.PIPE,
        stderr=None,
        cwd=str(cwd),
        env=env,
    )

    if proc.stdin is None or proc.stdout is None:
        raise RuntimeError("Agent process does not expose stdio pipes")

    accumulated_text = []

    class MiddleManagerACPClient(Client):
        def __init__(self):
            super().__init__()
            self.terminals = {}

        async def request_permission(
            self, options: list[PermissionOption], session_id: str, tool_call: Any, **kwargs: Any
        ) -> RequestPermissionResponse:
            desc = getattr(tool_call, "description", "")
            if not desc:
                desc = f"tool '{getattr(tool_call, 'name', 'unknown')}'"
            console.print(f"\n[bold green]⚡ [ACP] Auto-approving permission: {desc}[/bold green]")
            selected_option = options[0].option_id
            return RequestPermissionResponse(
                outcome=AllowedOutcome(
                    outcome="selected",
                    optionId=selected_option
                )
            )

        async def session_update(
            self,
            session_id: str,
            update: Any,
            **kwargs: Any,
        ) -> None:
            if isinstance(update, AgentThoughtChunk):
                console.print(update.thought, style="italic dim", end="")
            elif isinstance(update, AgentMessageChunk):
                content = update.content
                text = ""
                if hasattr(content, "text"):
                    text = content.text
                elif hasattr(content, "uri"):
                    text = content.uri
                else:
                    text = str(content)
                console.print(text, style="green", end="")
                accumulated_text.append(text)
            elif hasattr(update, "type") and update.type == "tool_call_start":
                console.print(f"\n[cyan]🔨 Running tool: {getattr(update, 'name', 'unknown')}[/cyan]")

        async def read_text_file(
            self, path: str, session_id: str, limit: int | None = None, line: int | None = None, **kwargs: Any
        ) -> ReadTextFileResponse:
            from acp.schema import ReadTextFileResponse
            p = Path(path)
            if not p.is_absolute():
                p = Path(cwd) / p
            
            try:
                rel_path = p.relative_to(Path(cwd))
            except Exception:
                rel_path = p
            console.print(f"   [dim]📖 [ACP] Reading file: {rel_path}[/dim]")
            
            if not p.exists():
                return ReadTextFileResponse(content="")
                
            lines = p.read_text(encoding="utf-8", errors="ignore").splitlines()
            start = (line - 1) if line is not None else 0
            if start < 0:
                start = 0
            if limit is not None:
                lines = lines[start : start + limit]
            else:
                lines = lines[start :]
            return ReadTextFileResponse(content="\n".join(lines))

        async def write_text_file(
            self, content: str, path: str, session_id: str, **kwargs: Any
        ) -> WriteTextFileResponse | None:
            from acp.schema import WriteTextFileResponse
            p = Path(path)
            if not p.is_absolute():
                p = Path(cwd) / p
            
            try:
                rel_path = p.relative_to(Path(cwd))
            except Exception:
                rel_path = p
            console.print(f"   [bold yellow]✏️  [ACP] Writing file: {rel_path}[/bold yellow]")
            
            p.parent.mkdir(parents=True, exist_ok=True)
            p.write_text(content, encoding="utf-8")
            return WriteTextFileResponse()

        async def create_terminal(
            self,
            command: str,
            session_id: str,
            args: list[str] | None = None,
            cwd: str | None = None,
            env: list[Any] | None = None,
            **kwargs: Any,
        ) -> CreateTerminalResponse:
            from acp.schema import CreateTerminalResponse
            
            console.print(f"   [bold blue]🖥️  [ACP] Executing terminal command: {command}[/bold blue]")
            
            terminal_id = f"term_{len(self.terminals) + 1}"
            
            env_dict = None
            if env:
                env_dict = {item.name: item.value for item in env if hasattr(item, 'name')}
            
            target_cwd = cwd or str(cwd)
            
            # Execute shell command asynchronously
            proc = await asyncio.create_subprocess_shell(
                command,
                stdout=asyncio.subprocess.PIPE,
                stderr=asyncio.subprocess.STDOUT,
                cwd=target_cwd,
                env=env_dict
            )
            
            term_info = {
                "proc": proc,
                "output": bytearray(),
                "exit_code": None
            }
            self.terminals[terminal_id] = term_info
            
            async def read_stdout():
                try:
                    while True:
                        chunk = await proc.stdout.read(65536)
                        if not chunk:
                            break
                        term_info["output"].extend(chunk)
                except Exception:
                    pass
                finally:
                    code = await proc.wait()
                    term_info["exit_code"] = code
            
            asyncio.create_task(read_stdout())
            return CreateTerminalResponse(terminalId=terminal_id)

        async def terminal_output(
            self, session_id: str, terminal_id: str, **kwargs: Any
        ) -> TerminalOutputResponse:
            from acp.schema import TerminalOutputResponse, TerminalExitStatus
            term_info = self.terminals.get(terminal_id)
            if not term_info:
                raise RequestError.invalid_params(f"Terminal {terminal_id} not found")
                
            out_str = term_info["output"].decode("utf-8", errors="ignore")
            
            exit_status = None
            if term_info["exit_code"] is not None:
                exit_status = TerminalExitStatus(exitCode=term_info["exit_code"])
                
            return TerminalOutputResponse(
                output=out_str,
                truncated=False,
                exitStatus=exit_status
            )

        async def wait_for_terminal_exit(
            self, session_id: str, terminal_id: str, **kwargs: Any
        ) -> WaitForTerminalExitResponse:
            from acp.schema import WaitForTerminalExitResponse, TerminalExitStatus
            term_info = self.terminals.get(terminal_id)
            if not term_info:
                raise RequestError.invalid_params(f"Terminal {terminal_id} not found")
                
            while term_info["exit_code"] is None:
                await asyncio.sleep(0.1)
                
            return WaitForTerminalExitResponse(
                exitStatus=TerminalExitStatus(exitCode=term_info["exit_code"])
            )

        async def release_terminal(self, session_id: str, terminal_id: str, **kwargs: Any) -> Any:
            term_info = self.terminals.get(terminal_id)
            if term_info and term_info["exit_code"] is None:
                try:
                    term_info["proc"].terminate()
                except Exception:
                    pass
            return None

        async def kill_terminal(self, session_id: str, terminal_id: str, **kwargs: Any) -> Any:
            term_info = self.terminals.get(terminal_id)
            if term_info and term_info["exit_code"] is None:
                try:
                    term_info["proc"].kill()
                except Exception:
                    pass
            return None

        async def ext_method(self, method: str, params: dict) -> dict:
            raise RequestError.method_not_found(method)

        async def ext_notification(self, method: str, params: dict) -> None:
            raise RequestError.method_not_found(method)

    client_impl = MiddleManagerACPClient()
    conn = connect_to_agent(client_impl, proc.stdin, proc.stdout)

    await conn.initialize(
        protocol_version=PROTOCOL_VERSION,
        client_capabilities=ClientCapabilities(),
        client_info=Implementation(name="middle-manager", title="Middle Manager", version="0.1.0"),
    )

    session = await conn.new_session(mcp_servers=[], cwd=str(cwd))

    try:
        await conn.prompt(
            session_id=session.session_id,
            prompt=[text_block(prompt)],
        )
    finally:
        if proc.returncode is None:
            try:
                proc.terminate()
                await proc.wait()
            except Exception:
                pass

    console.print()
    return "".join(accumulated_text)


def run_agent(
    run: AgentRun,
    *,
    dry_run: bool = False,
    stream: bool = True,
    step: str | None = None,
    tmux: bool = False,
    run_id: str | None = None,
) -> subprocess.CompletedProcess[str]:
    import asyncio
    import sys
    if dry_run:
        print(f"[DRY RUN] Would run agent {run.agent} with prompt: {run.prompt}")
        return subprocess.CompletedProcess(run.command, 0, stdout="", stderr="")

    try:
        binary_override = run.command[0] if run.command else None
        stdout = asyncio.run(
            run_agent_acp(
                run.agent,
                run.prompt,
                run.cwd,
                model=run.model,
                env=run.env,
                extra_args=run.extra_args,
                binary_override=binary_override,
                step=step,
            )
        )
        return subprocess.CompletedProcess(run.command, 0, stdout=stdout, stderr="")
    except Exception as e:
        print(f"Error running agent via ACP: {e}", file=sys.stderr)
        import traceback
        traceback.print_exc()
        return subprocess.CompletedProcess(run.command, 1, stdout="", stderr=str(e))


def _quote(arg: str) -> str:
    import shlex
    return shlex.quote(arg)


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


STEP_AGENT_PRIORITY: dict[str, tuple[str, ...]] = {
    "discover": ("grok", "claude", "opencode", "agy", "codex"),
    "execute": ("claude", "grok", "opencode", "agy", "codex"),
    "verify": ("codex", "grok", "claude", "opencode", "agy"),
    "commit": ("agy", "grok", "claude", "opencode", "codex"),
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
    overrides = binary_overrides or {}
    installed = [name for name in AGENT_NAMES if agent_available(name, overrides.get(name))]
    
    if not installed:
        return {
            "discover": "grok",
            "execute": "claude",
            "verify": "codex",
            "commit": "agy"
        }
        
    assigned = {}
    steps = ["discover", "execute", "verify", "commit"]
    
    for step in steps:
        priority_list = STEP_AGENT_PRIORITY.get(step, AGENT_NAMES)
        chosen = None
        # First pass: try to pick an installed agent that has NOT been assigned to any other step yet
        for name in priority_list:
            if name in installed and name not in assigned.values():
                chosen = name
                break
        
        # Second pass: pick the highest priority installed agent regardless of duplicate assignment
        if not chosen:
            for name in priority_list:
                if name in installed:
                    chosen = name
                    break
                    
        # Third pass: absolute fallback
        if not chosen:
            chosen = priority_list[0]
            
        assigned[step] = chosen
        
    return assigned