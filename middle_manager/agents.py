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
        yolo_flag="",  # crush run does not accept global -y flag in headless mode
        prompt_mode="arg",
        subcommand=("run",),
        model_flag="-m",
        cwd_flag="-c",
        notes="crush run PROMPT -c DIR (no YOLO flag needed for run command)",
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
        if spec.yolo_flag:
            cmd.append(spec.yolo_flag)

    cmd.extend(spec.subcommand)

    if yolo and spec.yolo_position in ("after_binary", "before_prompt"):
        if spec.yolo_flag:
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
        if spec.yolo_flag:
            cmd.append(spec.yolo_flag)

    # opencode/crush: yolo often works as trailing flag too
    if yolo and agent == "opencode" and spec.yolo_flag and spec.yolo_flag not in cmd:
        cmd.append(spec.yolo_flag)

    cmd.extend(extras)

    env = os.environ.copy()
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


def draw_status_block(
    agent_name: str,
    status_str: str,
    elapsed_str: str,
    cpu_str: str,
    active_procs: int,
    active_sockets: int,
    changed_files: list[str],
    last_printed_lines_cnt: int = 0,
    last_line: str = ""
) -> int:
    import sys
    from .colors import Colors
    lines = []
    lines.append(Colors.colored(f"  ┌── MONITORING {agent_name.upper()} ──────────────────────────────────────────────", Colors.MAGENTA))
    lines.append(Colors.colored(f"  │  Status:         {status_str}", Colors.CYAN))
    lines.append(Colors.colored(f"  │  Elapsed Time:  {elapsed_str}", Colors.CYAN))
    lines.append(Colors.colored(f"  │  CPU Usage:     {cpu_str}", Colors.CYAN))
    lines.append(Colors.colored(f"  │  Active Processes: {active_procs}", Colors.CYAN))
    lines.append(Colors.colored(f"  │  Active Sockets:   {active_sockets}", Colors.CYAN))

    if last_line:
        max_len = 62
        if len(last_line) > max_len:
            truncated = last_line[:max_len-3] + "..."
        else:
            truncated = last_line
        lines.append(Colors.colored(f"  │  Last Output:   \"{truncated}\"", Colors.YELLOW))

    if changed_files:
        lines.append(Colors.colored("  │  Files Changed:", Colors.CYAN))
        for f in changed_files[:5]:
            lines.append(Colors.colored(f"  │    - {f}", Colors.GREEN))
        if len(changed_files) > 5:
            lines.append(Colors.colored(f"  │    - ... and {len(changed_files) - 5} more", Colors.GREEN))
    else:
        lines.append(Colors.colored("  │  Files Changed: None yet", Colors.CYAN))
        
    lines.append(Colors.colored("  └──────────────────────────────────────────────────────────────────────────", Colors.MAGENTA))
    
    if sys.stdout.isatty() and last_printed_lines_cnt > 0:
        sys.stdout.write(f"\033[{last_printed_lines_cnt}A")
        
    for line in lines:
        if sys.stdout.isatty():
            sys.stdout.write("\033[K" + line + "\n")
        else:
            sys.stdout.write(line + "\n")
            
    sys.stdout.flush()
    return len(lines)


def read_available(stream) -> str:
    try:
        data = stream.read()
        return data if data is not None else ""
    except (BlockingIOError, TypeError):
        return ""
    except Exception:
        return ""


def run_command_monitored(
    command: list[str] | str,
    *,
    cwd: Path,
    env: dict[str, str] | None = None,
    timeout: int | None = None,
    stream: bool = False,
    label: str = "COMMAND",
    dry_run: bool = False,
) -> subprocess.CompletedProcess[str]:
    import sys
    import time
    import fcntl
    import os
    from .colors import Colors
    
    cmd_list = [command] if isinstance(command, str) else command
    cmd_str = " ".join(_quote(a) for a in cmd_list) if isinstance(command, list) else command
    
    dry_prefix = Colors.colored("[DRY RUN] ", Colors.YELLOW) if dry_run else ""
    
    header = f"┌── {dry_prefix}RUNNING {label} ──────────────────────────────────────────────────"
    print(Colors.colored(header, Colors.MAGENTA + Colors.BOLD))
    print(Colors.colored(f"│ Cwd:     {cwd}", Colors.CYAN))
    print(Colors.colored(f"│ Command: {cmd_str}", Colors.CYAN))
    print(Colors.colored("└" + "─" * 78, Colors.MAGENTA + Colors.BOLD))

    if dry_run:
        return subprocess.CompletedProcess(cmd_list, 0, stdout="", stderr="")

    if stream:
        print(Colors.colored(f"  ┌── {label} Output ──────────────────────────────────────────────────────────", Colors.MAGENTA))

        proc = subprocess.Popen(
            command,
            cwd=cwd,
            env=env,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            text=True,
            bufsize=1,
            shell=isinstance(command, str),
        )
        output_lines: list[str] = []
        assert proc.stdout is not None
        
        start_of_line = True
        try:
            for line in proc.stdout:
                output_lines.append(line)
                for char in line:
                    if start_of_line:
                        sys.stdout.write(Colors.colored("  │ ", Colors.MAGENTA))
                        start_of_line = False
                    sys.stdout.write(char)
                    if char == "\n":
                        start_of_line = True
                sys.stdout.flush()
        except KeyboardInterrupt:
            try:
                proc.terminate()
                proc.wait(timeout=2)
            except Exception:
                try:
                    proc.kill()
                except Exception:
                    pass
            raise

        proc.wait(timeout=timeout)
        try:
            proc.stdout.close()
        except Exception:
            pass
        if not start_of_line:
            print()
        print(Colors.colored(f"  └── End of {label} Output ────────────────────────────────────────────────────", Colors.MAGENTA))
        return subprocess.CompletedProcess(
            cmd_list,
            proc.returncode or 0,
            stdout="".join(output_lines),
            stderr="",
        )
    else:
        # Monitoring mode (default)
        proc = subprocess.Popen(
            command,
            cwd=cwd,
            env=env,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            text=True,
            bufsize=1,
            shell=isinstance(command, str),
        )
        
        # Make stdout non-blocking
        fd = proc.stdout.fileno()
        fl = fcntl.fcntl(fd, fcntl.F_GETFL)
        fcntl.fcntl(fd, fcntl.F_SETFL, fl | os.O_NONBLOCK)
        
        output_parts: list[str] = []
        start_time = time.time()
        last_cpu_time = start_time
        last_ticks = get_process_tree_cpu_ticks(proc.pid)
        cpu_percent = 0.0
        
        last_git_check = 0.0
        changed_files: list[str] = []
        
        last_printed_lines = 0
        step_counter = 0
        
        SPINNER = ["⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"]
        
        # Track for non-TTY logging
        last_log_time = start_time
        known_changed_files = set()
        
        if not sys.stdout.isatty():
            print(Colors.colored(f"  ┌── MONITORING {label} ──────────────────────────────────────────────", Colors.MAGENTA))
        
        try:
            while True:
                poll_status = proc.poll()
                is_running = (poll_status is None)
                
                chunk = read_available(proc.stdout)
                if chunk:
                    output_parts.append(chunk)
                    
                if not is_running:
                    final_chunk = read_available(proc.stdout)
                    if final_chunk:
                        output_parts.append(final_chunk)
                    break
                    
                now = time.time()
                
                # CPU check
                if now - last_cpu_time >= 0.5:
                    cpu_val, last_ticks, last_cpu_time = calculate_cpu_percent(proc.pid, last_ticks, last_cpu_time)
                    if cpu_val is not None:
                        cpu_percent = cpu_val
                        
                # Git Status check
                if now - last_git_check >= 1.5:
                    changed_files = get_changed_files_with_status(cwd)
                    last_git_check = now
                    
                elapsed = now - start_time
                mins, secs = divmod(int(elapsed), 60)
                elapsed_str = f"{mins:02d}:{secs:02d}"
                
                accumulated = "".join(output_parts)
                out_lines = len(accumulated.splitlines())
                out_bytes = len(accumulated.encode("utf-8", errors="ignore"))
                if out_bytes < 1024:
                    size_str = f"{out_bytes} B"
                elif out_bytes < 1024 * 1024:
                    size_str = f"{out_bytes / 1024:.1f} KB"
                else:
                    size_str = f"{out_bytes / (1024 * 1024):.1f} MB"
                    
                cpu_str = f"{cpu_percent:.1f}%"
                
                if sys.stdout.isatty():
                    spinner_char = SPINNER[step_counter % len(SPINNER)]
                    status_line = f"{spinner_char} RUNNING..."
                    
                    last_line = ""
                    for line in reversed(accumulated.splitlines()):
                        cleaned = line.strip()
                        if cleaned:
                            last_line = cleaned
                            break

                    active_procs, active_sockets = get_process_tree_stats(proc.pid)

                    last_printed_lines = draw_status_block(
                        agent_name=label,
                        status_str=status_line,
                        elapsed_str=elapsed_str,
                        cpu_str=cpu_str,
                        active_procs=active_procs,
                        active_sockets=active_sockets,
                        changed_files=changed_files,
                        last_printed_lines_cnt=last_printed_lines,
                        last_line=last_line
                    )
                else:
                    # Non-TTY logic
                    new_files_set = set(changed_files)
                    added_changes = new_files_set - known_changed_files
                    for f in added_changes:
                        print(Colors.colored(f"  │ [{elapsed_str}] File changed: {f}", Colors.GREEN))
                    known_changed_files = new_files_set
                    
                    if now - last_log_time >= 10.0:
                        active_procs, active_sockets = get_process_tree_stats(proc.pid)
                        print(Colors.colored(f"  │ [{elapsed_str}] CPU: {cpu_percent:.1f}% | Procs: {active_procs} | Sockets: {active_sockets}", Colors.CYAN))
                        last_log_time = now
                        
                time.sleep(0.1)
                step_counter += 1
                
        except KeyboardInterrupt:
            try:
                proc.terminate()
                proc.wait(timeout=2)
            except Exception:
                try:
                    proc.kill()
                except Exception:
                    pass
            raise
            
        proc.wait(timeout=timeout)
        try:
            proc.stdout.close()
        except Exception:
            pass
        
        # Final stats
        elapsed = time.time() - start_time
        mins, secs = divmod(int(elapsed), 60)
        elapsed_str = f"{mins:02d}:{secs:02d}"
        
        changed_files = get_changed_files_with_status(cwd)
        accumulated = "".join(output_parts)
        out_lines = len(accumulated.splitlines())
        out_bytes = len(accumulated.encode("utf-8", errors="ignore"))
        if out_bytes < 1024:
            size_str = f"{out_bytes} B"
        elif out_bytes < 1024 * 1024:
            size_str = f"{out_bytes / 1024:.1f} KB"
        else:
            size_str = f"{out_bytes / (1024 * 1024):.1f} MB"
            
        status_str = "✅ COMPLETED" if proc.returncode == 0 else f"❌ FAILED (exit code {proc.returncode})"
        
        if sys.stdout.isatty():
            last_line = ""
            for line in reversed(accumulated.splitlines()):
                cleaned = line.strip()
                if cleaned:
                    last_line = cleaned
                    break

            draw_status_block(
                agent_name=label,
                status_str=status_str,
                elapsed_str=elapsed_str,
                cpu_str="0.0% (stopped)",
                active_procs=0,
                active_sockets=0,
                changed_files=changed_files,
                last_printed_lines_cnt=last_printed_lines,
                last_line=last_line
            )
        else:
            print(Colors.colored(f"  │ [{elapsed_str}] Final status: {status_str}", Colors.CYAN))
            if changed_files:
                print(Colors.colored("  │ Final changed files:", Colors.CYAN))
                for f in changed_files:
                    print(Colors.colored(f"  │   - {f}", Colors.GREEN))
            print(Colors.colored(f"  └── End of {label} Run ──────────────────────────────────────────────────────", Colors.MAGENTA))
            
        return subprocess.CompletedProcess(
            cmd_list,
            proc.returncode or 0,
            stdout="".join(output_parts),
            stderr="",
        )


def run_agent(run: AgentRun, *, dry_run: bool = False, stream: bool = True, step: str | None = None) -> subprocess.CompletedProcess[str]:
    label = f"{step.upper()} STEP ({run.agent.upper()})" if step else f"AGENT: {run.agent.upper()}"
    return run_command_monitored(
        command=run.command,
        cwd=run.cwd,
        env=run.env,
        timeout=run.timeout,
        stream=stream,
        label=label,
        dry_run=dry_run,
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