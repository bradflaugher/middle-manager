"""Main multi-agent coding loop."""

from __future__ import annotations

import subprocess
import time
from dataclasses import dataclass
from pathlib import Path

from .agents import agent_available, build_command, run_agent
from .config import LoopConfig, StepConfig
from .git_ops import (
    commit_all,
    create_pr,
    current_branch,
    ensure_branch,
    ensure_issue_branch,
    fetch_issue,
    has_changes,
    plan_is_complete,
    push_branch,
    repo_is_git,
)
from .interactive import pause
from .presets import reset_loop_state, seed_feature_plan
from .prompts import build_context, load_prompt, render_prompt


@dataclass
class LoopResult:
    success: bool
    reason: str = ""
    pr_url: str | None = None
    iterations: int = 0


class MiddleManagerLoop:
    def __init__(self, cfg: LoopConfig):
        import uuid
        self.run_id = uuid.uuid4().hex[:8]
        self.cfg = cfg
        self.state = cfg.state_path()
        self.fix_plan_path = self.state / "fix_plan.md"
        self.error_log_path = self.state / "error_log.txt"
        self.verify_log_path = self.state / "verify_log.txt"
        self.iteration_path = self.state / "iteration.txt"
        self.session_log = self.state / "session.log"
        self.last_pr_url: str | None = None

    def log(self, msg: str, color: str | None = None) -> None:
        from .colors import Colors
        raw_line = f"[{time.strftime('%Y-%m-%d %H:%M:%S')}] {msg}"
        if color:
            print_line = Colors.colored(raw_line, color)
        else:
            print_line = raw_line
        print(print_line)
        # Strip ANSI codes for the session log file
        import re
        ansi_escape = re.compile(r'\x1B(?:[@-Z\\-_]|\[[0-?]*[ -/]*[@-~])')
        clean_line = ansi_escape.sub('', raw_line) + "\n"
        with self.session_log.open("a", encoding="utf-8") as f:
            f.write(clean_line)

    def ensure_gitignore(self) -> None:
        if not repo_is_git(self.cfg.repo):
            return
        gitignore = self.cfg.repo / ".gitignore"
        rule = ".middle-manager/"
        if gitignore.exists():
            try:
                content = gitignore.read_text(encoding="utf-8")
                lines = [line.strip() for line in content.splitlines()]
                if rule not in lines and rule[:-1] not in lines:
                    with gitignore.open("a", encoding="utf-8") as f:
                        if content and not content.endswith("\n"):
                            f.write("\n")
                        f.write(f"\n# middle-manager state directory\n{rule}\n")
                    self.log(f"Added {rule} to .gitignore")
            except Exception as e:
                self.log(f"Warning: Could not update .gitignore: {e}")
        else:
            try:
                gitignore.write_text(f"# middle-manager state directory\n{rule}\n", encoding="utf-8")
                self.log(f"Created .gitignore and added {rule}")
            except Exception as e:
                self.log(f"Warning: Could not create .gitignore: {e}")

    def read_iteration(self) -> int:
        if self.iteration_path.exists():
            try:
                return max(1, int(self.iteration_path.read_text().strip()))
            except ValueError:
                pass
        return 1

    def write_iteration(self, n: int) -> None:
        self.iteration_path.write_text(str(n), encoding="utf-8")

    def read_text(self, path: Path, default: str = "") -> str:
        return path.read_text(encoding="utf-8") if path.exists() else default

    def write_text(self, path: Path, content: str) -> None:
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(content, encoding="utf-8")

    def top_plan_item(self) -> str:
        text = self.read_text(self.fix_plan_path)
        for line in text.splitlines():
            s = line.strip()
            if s.startswith("- [ ]"):
                return s[5:].strip()
        
        # Fallback to loose bullet points only if they are under a Tasks section
        in_tasks_section = False
        for line in text.splitlines():
            s = line.strip()
            if s.lower().startswith("## task"):
                in_tasks_section = True
                continue
            if s.startswith("#") and not s.lower().startswith("## task"):
                in_tasks_section = False
            if in_tasks_section and s.startswith("- ") and not s.startswith("- [x]") and not s.startswith("- [ ]"):
                return s[2:].strip()
        return "No actionable item in fix_plan.md — add `- [ ] task` lines."

    def top_plan_items(self, count: int = 1) -> list[str]:
        text = self.read_text(self.fix_plan_path)
        items = []
        for line in text.splitlines():
            s = line.strip()
            if s.startswith("- [ ]"):
                items.append(s[5:].strip())
                if len(items) >= count:
                    return items
        
        # Fallback to loose bullet points only if they are under a Tasks section
        in_tasks_section = False
        for line in text.splitlines():
            s = line.strip()
            if s.lower().startswith("## task"):
                in_tasks_section = True
                continue
            if s.startswith("#") and not s.lower().startswith("## task"):
                in_tasks_section = False
            if in_tasks_section and s.startswith("- ") and not s.startswith("- [x]") and not s.startswith("- [ ]"):
                item = s[2:].strip()
                if item not in items:
                    items.append(item)
                    if len(items) >= count:
                        return items
        return items

    def agent_memory(self) -> str:
        mem = self.cfg.repo / self.cfg.agent_memory_file
        if mem.exists():
            return mem.read_text(encoding="utf-8")
        claude_md = self.cfg.repo / "CLAUDE.md"
        if claude_md.exists():
            return claude_md.read_text(encoding="utf-8")
        return "(no AGENT.md or CLAUDE.md found — create one with repo rules)"

    def ensure_fix_plan_seed(self, issue_data: dict[str, str]) -> None:
        if self.cfg.mode == "feature" and self.cfg.mission:
            if self.cfg.fresh or not self.fix_plan_path.exists():
                seed_feature_plan(self.cfg, self.fix_plan_path)
            return
        if self.fix_plan_path.exists():
            return
        seed = "# fix_plan.md\n\n"
        if self.cfg.mission:
            seed += f"## Mission\n\n{self.cfg.mission}\n\n"
        if issue_data.get("title"):
            seed += f"## Issue #{issue_data['number']}: {issue_data['title']}\n\n"
            if issue_data.get("body"):
                seed += issue_data["body"] + "\n\n"
        task = self.cfg.mission or "Investigate and scope the top priority item"
        seed += f"## Tasks\n\n- [ ] {task}\n"
        self.write_text(self.fix_plan_path, seed)

    def prompt_for_step(self, step: str, iteration: int, issue_data: dict[str, str]) -> str:
        sc = self.cfg.step_for(step)
        if step == "discover" and self.cfg.mode == "feature":
            template_name = "discover_feature"
        else:
            template_name = sc.prompt_file or step
        template = load_prompt(template_name.replace(".md", ""))
        if step == "execute" and self.cfg.fix_unrelated_tests:
            rule_addition = "\n6. **Fix unrelated test failures:** If the test suite is failing due to unrelated test failures or environment-specific issues that block verification of your changes, you are allowed and encouraged to modify the test files or unrelated files directly to fix the test failures so that they pass.\n"
            template += rule_addition
        top_items = self.top_plan_items(self.cfg.batch_size)
        if len(top_items) == 1:
            top_item_str = top_items[0]
        elif len(top_items) > 1:
            top_item_str = "\n".join(f"- [ ] {item}" for item in top_items)
        else:
            top_item_str = "No actionable item in fix_plan.md — add `- [ ] task` lines."

        ctx = build_context(
            repo=self.cfg.repo,
            issue=self.cfg.issue or issue_data.get("url", ""),
            fix_plan=self.read_text(self.fix_plan_path),
            top_item=top_item_str,
            agent_memory=self.agent_memory(),
            test_output=self.read_text(self.error_log_path),
            error_log=self.read_text(self.error_log_path),
            iteration=iteration,
            mission=self.cfg.mission,
        )
        ctx["issue_title"] = issue_data.get("title", "")
        ctx["issue_body"] = issue_data.get("body", "")
        ctx["issue_number"] = issue_data.get("number", "")
        return render_prompt(template, **ctx)

    def run_step(self, step: str, iteration: int, issue_data: dict[str, str]) -> subprocess.CompletedProcess[str]:
        sc: StepConfig = self.cfg.step_for(step)
        if not sc.enabled:
            self.log(f"Skipping disabled step: {step}")
            return subprocess.CompletedProcess([], 0, "", "")

        binary = self.cfg.binary_overrides.get(sc.agent)
        if not agent_available(sc.agent, binary) and not self.cfg.dry_run:
            self.log(f"Agent {sc.agent} not found on PATH — skipping {step}")
            return subprocess.CompletedProcess([], 127, "", "")

        prompt = self.prompt_for_step(step, iteration, issue_data)
        prompt_file = self.state / f"{step}_prompt.md"
        self.write_text(prompt_file, prompt)

        from .colors import Colors
        run = build_command(
            sc.agent,
            prompt,
            cwd=self.cfg.repo,
            model=sc.model,
            yolo=self.cfg.yolo,
            extra_args=sc.extra_args,
            binary_override=binary,
            prompt_file=prompt_file if sc.agent != "agy" else None,
            interactive=self.cfg.interactive and step == "execute",
        )
        result = run_agent(run, dry_run=self.cfg.dry_run, stream=self.cfg.stream_output, step=step, tmux=self.cfg.tmux, run_id=self.run_id)
        
        # Write agent output to state directory for user inspectability/visibility
        output_file = self.state / f"{step}_output.txt"
        self.write_text(output_file, result.stdout)

        if result.returncode == 0:
            self.log(f"✅ Step {step.upper()} ({sc.agent.upper()}) finished successfully (exit code 0).", Colors.GREEN)
        else:
            self.log(f"❌ Step {step.upper()} ({sc.agent.upper()}) failed (exit code {result.returncode}).", Colors.RED)
        return result

    def maybe_commit_and_pr(self, iteration: int, issue_data: dict[str, str]) -> None:
        if self.cfg.steps < 4 or not self.cfg.step_for("commit").enabled:
            if has_changes(self.cfg.repo) and not self.cfg.dry_run:
                msg = f"middle-manager: iteration {iteration} — {self.top_plan_item()[:72]}"
                if commit_all(self.cfg.repo, msg):
                    self.log("Committed changes (3-step mode, no PR agent)")
            return

        result = self.run_step("commit", iteration, issue_data)
        if result.returncode != 0:
            self.log("Commit step failed; leaving working tree as-is")
            return

        if not repo_is_git(self.cfg.repo):
            return

        branch = current_branch(self.cfg.repo)
        if not self.cfg.no_pr:
            # Push branch to origin first to ensure PR creation succeeds
            push_branch(self.cfg.repo, branch, dry_run=self.cfg.dry_run)
            
            title = f"middle-manager: {self.top_plan_item()[:60]}"
            body = (
                f"Automated PR from middle-manager loop iteration {iteration}.\n\n"
                f"**Do not merge without human review.**\n\n"
                f"Plan: `{self.fix_plan_path}`"
            )
            url = create_pr(
                self.cfg.repo,
                title=title,
                body=body,
                branch=branch,
                issue_number=issue_data.get("number"),
                dry_run=self.cfg.dry_run,
            )
            if url:
                self.last_pr_url = url
                self.log(f"PR created: {url}")

    def _parse_verifier_updates(self, stdout: str) -> tuple[str, list[str]]:
        import re
        verdict = "UNKNOWN"
        m = re.search(r"VERDICT:\s*(PASS|FAIL)", stdout, re.IGNORECASE)
        if m:
            verdict = m.group(1).upper()
            
        updates = []
        lines = stdout.splitlines()
        in_updates = False
        for line in lines:
            stripped = line.strip()
            if not stripped:
                continue
            if re.match(r"(?:FIX[-_]PLAN[-_]UPDATES|PLAN[-_]UPDATES):", stripped, re.IGNORECASE):
                in_updates = True
                continue
            if in_updates:
                if stripped.startswith("-"):
                    task = stripped
                    if not task.startswith("- [ ]") and not task.startswith("- [x]"):
                        task = "- [ ] " + task[1:].strip()
                    updates.append(task)
                elif stripped.startswith("VERDICT:") or stripped.startswith("SUMMARY:") or stripped.startswith("ISSUES:"):
                    in_updates = False
                elif stripped.startswith("```"):
                    pass
                else:
                    if updates:
                        in_updates = False
        return verdict, updates

    def add_tasks_to_plan(self, new_tasks: list[str]) -> None:
        if not new_tasks:
            return
        text = self.read_text(self.fix_plan_path)
        lines = text.splitlines()
        
        tasks_index = -1
        for i, line in enumerate(lines):
            if line.strip().startswith("## Tasks") or line.strip().startswith("## Task"):
                tasks_index = i
                break
                
        if tasks_index != -1:
            insert_index = tasks_index + 1
            while insert_index < len(lines):
                line_strip = lines[insert_index].strip()
                if line_strip and not line_strip.startswith("-") and not line_strip.startswith("*") and not line_strip.startswith("#"):
                    break
                insert_index += 1
            
            for task in reversed(new_tasks):
                lines.insert(insert_index, task)
            self.log(f"Added {len(new_tasks)} task(s) suggested by verifier to fix_plan.md")
        else:
            lines.append("\n## Tasks")
            lines.extend(new_tasks)
            self.log(f"Appended {len(new_tasks)} task(s) suggested by verifier to end of fix_plan.md")
            
        self.write_text(self.fix_plan_path, "\n".join(lines) + "\n")

    def run_once(self, iteration: int) -> bool:
        """Run one full loop iteration. Returns False to stop the outer loop."""
        from .colors import Colors
        self.log(f"🔄 ==================== ITERATION {iteration} ====================", Colors.CYAN + Colors.BOLD)
        if repo_is_git(self.cfg.repo):
            self.log(f"Active Branch: {current_branch(self.cfg.repo)}", Colors.CYAN)

        issue_data = fetch_issue(self.cfg.repo, self.cfg.issue or "0")
        self.ensure_fix_plan_seed(issue_data)

        verifier_passed = True
        verifier_stdout = ""
        tasks_before = None

        for step in ("discover", "execute", "verify"):
            if step not in self.cfg.active_steps():
                continue
            if step == "execute":
                tasks_before = len([line for line in self.read_text(self.fix_plan_path).splitlines() if line.strip().startswith("- [ ]")])
            sc = self.cfg.step_for(step)
            self.log(f"⚡ [Step: {step.upper()}] Starting step with agent '{sc.agent.upper()}'...", Colors.CYAN + Colors.BOLD)
            if self.cfg.interactive:
                action = pause(self.cfg, step)
                if action == "quit":
                    return False
                if action == "skip":
                    self.log(f"Skipped step: {step}")
                    continue
            result = self.run_step(step, iteration, issue_data)
            if step == "verify":
                verifier_stdout = result.stdout
                if result.returncode != 0:
                    self.log("❌ Verifier reported CLI error/failure", Colors.RED)
                    verifier_passed = False

        if "verify" in self.cfg.active_steps() and verifier_passed:
            import re
            verdict = "UNKNOWN"
            m = re.search(r"VERDICT:\s*(PASS|FAIL)", verifier_stdout, re.IGNORECASE)
            if m:
                verdict = m.group(1).upper()
            
            self.log(f"🔍 Verifier Verdict: {verdict}", Colors.GREEN if verdict == "PASS" else Colors.RED)
            if verdict == "FAIL":
                verifier_passed = False
                self.log("⚠️ Verifier reported failure — will loop back", Colors.YELLOW)
                existing_err = self.read_text(self.error_log_path)
                header = f"\n=== VERIFIER FEEDBACK (Iteration {iteration}) ===\n"
                self.write_text(self.error_log_path, header + verifier_stdout + "\n" + existing_err)

            _, plan_updates = self._parse_verifier_updates(verifier_stdout)
            if plan_updates:
                self.add_tasks_to_plan(plan_updates)

        if not verifier_passed:
            return True

        if "commit" in self.cfg.active_steps():
            if repo_is_git(self.cfg.repo):
                if self.cfg.issue and self.cfg.issue.isdigit():
                    ensure_issue_branch(self.cfg.repo, self.cfg.branch_prefix, self.cfg.issue)
                else:
                    ensure_branch(self.cfg.repo, self.cfg.branch_prefix, iteration)
            self.maybe_commit_and_pr(iteration, issue_data)

        # Mark top items done if tests passed and we have a plan
        if tasks_before is not None:
            tasks_after = len([line for line in self.read_text(self.fix_plan_path).splitlines() if line.strip().startswith("- [ ]")])
            if tasks_after >= tasks_before:
                self._check_off_top_items(self.cfg.batch_size)
        else:
            self._check_off_top_items(self.cfg.batch_size)
        return True

    def is_complete(self) -> bool:
        return plan_is_complete(self.read_text(self.fix_plan_path))

    def _check_off_top_items(self, count: int) -> None:
        text = self.read_text(self.fix_plan_path)
        lines = text.splitlines()
        checked = 0
        for i, line in enumerate(lines):
            if line.strip().startswith("- [ ]"):
                lines[i] = line.replace("- [ ]", "- [x]", 1)
                checked += 1
                if checked >= count:
                    break
        if checked > 0:
            self.write_text(self.fix_plan_path, "\n".join(lines) + "\n")

    def run_until_complete(self) -> LoopResult:
        if not self.cfg.repo.exists():
            return LoopResult(False, f"Repo not found: {self.cfg.repo}")

        if self.cfg.fresh:
            reset_loop_state(self.cfg)

        self.ensure_gitignore()
        self.log(f"Target repo: {self.cfg.repo}")
        if repo_is_git(self.cfg.repo):
            self.log(f"Git branch:  {current_branch(self.cfg.repo)}")
        if self.cfg.mission:
            self.log(f"Mission: {self.cfg.mission[:80]}")
        self.log(f"Steps: {self.cfg.steps} ({', '.join(self.cfg.active_steps())})")
        self.log(f"YOLO: {self.cfg.yolo} | dry-run: {self.cfg.dry_run}")
        from .colors import Colors
        self.log("Press Ctrl+C to quit/exit the loop at any time.", Colors.YELLOW)
        if self.cfg.issue:
            self.log(f"Issue: {self.cfg.issue}")

        iteration = self.read_iteration()
        ran = 0
        for _ in range(self.cfg.max_iterations):
            if not self.run_once(iteration):
                return LoopResult(False, "Stopped by user", self.last_pr_url, ran)
            ran += 1
            iteration += 1
            self.write_iteration(iteration)
            if self.is_complete():
                self.log("Plan complete — all tasks checked off.")
                return LoopResult(True, "plan complete", self.last_pr_url, ran)

        return LoopResult(False, f"Max iterations ({self.cfg.max_iterations}) reached", self.last_pr_url, ran)

    def _build_interactive_command(self, agent: str, prompt: str) -> str:
        if agent == "grok":
            return f"grok --cwd {self.cfg.repo} -p \"{prompt}\""
        elif agent == "claude":
            return f"claude -p \"{prompt}\""
        elif agent == "crush":
            return f"crush run \"{prompt}\" -c {self.cfg.repo}"
        elif agent == "opencode":
            return f"opencode run \"{prompt}\" --dir {self.cfg.repo}"
        elif agent == "codex":
            return f"codex exec \"{prompt}\""
        elif agent == "agy":
            return f"agy --print \"{prompt}\""
        return f"{agent} -p \"{prompt}\""

    def run(self) -> int:
        from .colors import Colors
        try:
            result = self.run_until_complete()
            self.log("Loop finished.")
            if not result.success:
                branch_name = "unknown"
                if repo_is_git(self.cfg.repo):
                    try:
                        branch_name = current_branch(self.cfg.repo)
                    except Exception:
                        pass
                
                print()
                print(Colors.colored("┌────────────────────────────────────────────────────────┐", Colors.YELLOW + Colors.BOLD))
                print(Colors.colored("│                ⚠️  LOOP ABANDONED / FAILED             │", Colors.YELLOW + Colors.BOLD))
                print(Colors.colored("├────────────────────────────────────────────────────────┤", Colors.YELLOW + Colors.BOLD))
                print(Colors.colored(f"│  Reason: {result.reason:<46}│", Colors.YELLOW))
                print(Colors.colored(f"│  Branch: {branch_name:<46}│", Colors.YELLOW))
                print(Colors.colored("└────────────────────────────────────────────────────────┘", Colors.YELLOW + Colors.BOLD))
                print()
                
                sc = self.cfg.step_for("execute")
                err_log = self.read_text(self.error_log_path)
                err_summary = "See error_log.txt for details."
                if err_log:
                    lines = [l.strip() for l in err_log.splitlines() if l.strip()]
                    for line in lines:
                        if any(w in line.lower() for w in ("error", "fail", "exception", "timeout")):
                            err_summary = line[:60]
                            break
                    else:
                        if lines:
                            err_summary = lines[0][:60]
                
                prompt_msg = (
                    f"The last task '{self.top_plan_item()[:50]}' failed verification. "
                    f"Error: {err_summary}. Please debug and fix this issue."
                )
                
                agent_cmd = self._build_interactive_command(sc.agent, prompt_msg)
                
                print(Colors.colored("💡 To launch an interactive session with your programmer agent, run:", Colors.CYAN))
                print(Colors.colored(f"   {agent_cmd}", Colors.GREEN + Colors.BOLD))
                print()
                
            return 0 if result.success else 1
        except KeyboardInterrupt:
            self.log("⚠️  Loop interrupted by user (Ctrl+C).", Colors.YELLOW + Colors.BOLD)
            
            branch_name = "unknown"
            if repo_is_git(self.cfg.repo):
                try:
                    branch_name = current_branch(self.cfg.repo)
                except Exception:
                    pass
            
            print()
            print(Colors.colored("┌────────────────────────────────────────────────────────┐", Colors.YELLOW + Colors.BOLD))
            print(Colors.colored("│                ⚠️  LOOP INTERRUPTED (Ctrl+C)            │", Colors.YELLOW + Colors.BOLD))
            print(Colors.colored("├────────────────────────────────────────────────────────┤", Colors.YELLOW + Colors.BOLD))
            print(Colors.colored(f"│  Branch: {branch_name:<46}│", Colors.YELLOW))
            print(Colors.colored("└────────────────────────────────────────────────────────┘", Colors.YELLOW + Colors.BOLD))
            print()
            
            sc = self.cfg.step_for("execute")
            prompt_msg = (
                f"The task '{self.top_plan_item()[:50]}' was interrupted. "
                "Please resume working on it."
            )
            
            agent_cmd = self._build_interactive_command(sc.agent, prompt_msg)
                
            print(Colors.colored("💡 To launch an interactive session on this branch, run:", Colors.CYAN))
            print(Colors.colored(f"   {agent_cmd}", Colors.GREEN + Colors.BOLD))
            print()
            return 130