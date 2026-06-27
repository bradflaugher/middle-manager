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
from .prompts import build_context, load_prompt, render_prompt


@dataclass
class LoopResult:
    success: bool
    reason: str = ""
    pr_url: str | None = None
    iterations: int = 0


class MiddleManagerLoop:
    def __init__(self, cfg: LoopConfig):
        self.cfg = cfg
        self.state = cfg.state_path()
        self.fix_plan_path = self.state / "fix_plan.md"
        self.error_log_path = self.state / "error_log.txt"
        self.verify_log_path = self.state / "verify_log.txt"
        self.iteration_path = self.state / "iteration.txt"
        self.session_log = self.state / "session.log"
        self.last_pr_url: str | None = None

    def log(self, msg: str) -> None:
        line = f"[{time.strftime('%Y-%m-%d %H:%M:%S')}] {msg}\n"
        print(line, end="")
        with self.session_log.open("a", encoding="utf-8") as f:
            f.write(line)

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
            if s.startswith("- ") and not s.startswith("- [x]"):
                return s[2:].strip()
        return "No actionable item in fix_plan.md — add `- [ ] task` lines."

    def agent_memory(self) -> str:
        mem = self.cfg.repo / self.cfg.agent_memory_file
        if mem.exists():
            return mem.read_text(encoding="utf-8")
        claude_md = self.cfg.repo / "CLAUDE.md"
        if claude_md.exists():
            return claude_md.read_text(encoding="utf-8")
        return "(no AGENT.md or CLAUDE.md found — create one with repo rules)"

    def ensure_fix_plan_seed(self, issue_data: dict[str, str]) -> None:
        if self.fix_plan_path.exists():
            return
        seed = "# fix_plan.md\n\n"
        if issue_data.get("title"):
            seed += f"## Issue #{issue_data['number']}: {issue_data['title']}\n\n"
            if issue_data.get("body"):
                seed += issue_data["body"] + "\n\n"
        seed += "## Tasks\n\n- [ ] Investigate and scope the top priority item\n"
        self.write_text(self.fix_plan_path, seed)

    def run_tests(self) -> tuple[bool, str]:
        cmd = self.cfg.test_command
        if not cmd:
            return True, "(no test_command configured)"
        if self.cfg.dry_run:
            return True, f"[dry-run] would run: {cmd}"
        proc = subprocess.run(cmd, cwd=self.cfg.repo, shell=True, capture_output=True, text=True)
        out = (proc.stdout or "") + (proc.stderr or "")
        self.write_text(self.error_log_path, out)
        return proc.returncode == 0, out

    def prompt_for_step(self, step: str, iteration: int, issue_data: dict[str, str]) -> str:
        sc = self.cfg.step_for(step)
        template_name = sc.prompt_file or step
        template = load_prompt(template_name.replace(".md", ""))
        ctx = build_context(
            repo=self.cfg.repo,
            issue=self.cfg.issue or issue_data.get("url", ""),
            fix_plan=self.read_text(self.fix_plan_path),
            top_item=self.top_plan_item(),
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

    def run_step(self, step: str, iteration: int, issue_data: dict[str, str]) -> int:
        sc: StepConfig = self.cfg.step_for(step)
        if not sc.enabled:
            self.log(f"Skipping disabled step: {step}")
            return 0

        binary = self.cfg.binary_overrides.get(sc.agent)
        if not agent_available(sc.agent, binary) and not self.cfg.dry_run:
            self.log(f"Agent {sc.agent} not found on PATH — skipping {step}")
            return 127

        prompt = self.prompt_for_step(step, iteration, issue_data)
        prompt_file = self.state / f"{step}_prompt.md"
        self.write_text(prompt_file, prompt)

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
        result = run_agent(run, dry_run=self.cfg.dry_run)
        self.log(f"{step} finished with exit code {result.returncode}")
        return result.returncode

    def maybe_commit_and_pr(self, iteration: int, issue_data: dict[str, str]) -> None:
        if self.cfg.steps < 4 or not self.cfg.step_for("commit").enabled:
            if has_changes(self.cfg.repo) and not self.cfg.dry_run:
                msg = f"middle-manager: iteration {iteration} — {self.top_plan_item()[:72]}"
                if commit_all(self.cfg.repo, msg):
                    self.log("Committed changes (3-step mode, no PR agent)")
            return

        code = self.run_step("commit", iteration, issue_data)
        if code != 0:
            self.log("Commit step failed; leaving working tree as-is")
            return

        if not repo_is_git(self.cfg.repo):
            return

        branch = current_branch(self.cfg.repo)
        if not self.cfg.no_pr:
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

    def run_once(self, iteration: int) -> bool:
        """Run one full loop iteration. Returns False to stop the outer loop."""
        self.log(f"=== Iteration {iteration} ===")

        issue_data = fetch_issue(self.cfg.repo, self.cfg.issue or "0")
        self.ensure_fix_plan_seed(issue_data)

        for step in ("discover", "execute", "verify"):
            if step not in self.cfg.active_steps():
                continue
            if self.cfg.interactive:
                action = pause(self.cfg, step)
                if action == "quit":
                    return False
                if action == "skip":
                    self.log(f"Skipped step: {step}")
                    continue
            code = self.run_step(step, iteration, issue_data)
            if code != 0 and step == "verify":
                self.log("Verifier reported failure — will loop back")

        ok, test_out = self.run_tests()
        self.write_text(self.verify_log_path, test_out)
        if not ok:
            self.log("Tests failed — error_log updated for next discover pass")
            return True

        if "commit" in self.cfg.active_steps():
            if repo_is_git(self.cfg.repo):
                if self.cfg.issue and self.cfg.issue.isdigit():
                    ensure_issue_branch(self.cfg.repo, self.cfg.branch_prefix, self.cfg.issue)
                else:
                    ensure_branch(self.cfg.repo, self.cfg.branch_prefix, iteration)
            self.maybe_commit_and_pr(iteration, issue_data)

        # Mark top item done if tests passed and we have a plan
        self._check_off_top_item()
        return True

    def is_complete(self) -> bool:
        return plan_is_complete(self.read_text(self.fix_plan_path))

    def _check_off_top_item(self) -> None:
        text = self.read_text(self.fix_plan_path)
        lines = text.splitlines()
        changed = False
        for i, line in enumerate(lines):
            if line.strip().startswith("- [ ]"):
                lines[i] = line.replace("- [ ]", "- [x]", 1)
                changed = True
                break
        if changed:
            self.write_text(self.fix_plan_path, "\n".join(lines) + "\n")

    def run_until_complete(self) -> LoopResult:
        if not self.cfg.repo.exists():
            return LoopResult(False, f"Repo not found: {self.cfg.repo}")

        self.log(f"Target repo: {self.cfg.repo}")
        if self.cfg.mission:
            self.log(f"Mission: {self.cfg.mission[:80]}")
        self.log(f"Steps: {self.cfg.steps} ({', '.join(self.cfg.active_steps())})")
        self.log(f"YOLO: {self.cfg.yolo} | dry-run: {self.cfg.dry_run}")
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

    def run(self) -> int:
        result = self.run_until_complete()
        self.log("Loop finished.")
        return 0 if result.success else 1