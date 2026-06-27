"""Batch processor for filtered GitHub issue queues."""

from __future__ import annotations

import time
from pathlib import Path

from .config import LoopConfig
from .git_ops import checkout_default_branch, close_issue, list_issues, repo_is_git
from .loop import MiddleManagerLoop


class IssueQueueRunner:
    def __init__(self, cfg: LoopConfig):
        if not cfg.issue_queue:
            raise ValueError("issue_queue config required")
        self.cfg = cfg
        self.queue = cfg.issue_queue
        self.log_path = cfg.state_path() / "queue.log"

    def log(self, msg: str) -> None:
        line = f"[{time.strftime('%Y-%m-%d %H:%M:%S')}] {msg}\n"
        print(line, end="")
        self.log_path.parent.mkdir(parents=True, exist_ok=True)
        with self.log_path.open("a", encoding="utf-8") as f:
            f.write(line)

    def reset_issue_state(self, issue: dict[str, str]) -> None:
        """Fresh fix_plan and iteration counter per issue."""
        state = self.cfg.state_path()
        number = issue["number"]
        issue_dir = state / "issues" / number
        issue_dir.mkdir(parents=True, exist_ok=True)

        # Per-issue fix plan seed
        plan = issue_dir / "fix_plan.md"
        seed = f"# fix_plan.md — Issue #{number}\n\n"
        seed += f"## {issue['title']}\n\n"
        if issue.get("body"):
            seed += issue["body"] + "\n\n"
        seed += "## Tasks\n\n"
        seed += f"- [ ] Resolve issue #{number}: {issue['title']}\n"
        plan.write_text(seed, encoding="utf-8")

        # Point loop at per-issue state
        self.cfg.state_dir = issue_dir
        self.cfg.issue = number
        self.cfg.max_iterations = self.cfg.max_iterations  # per-issue budget

    def run(self) -> int:
        issues = list_issues(self.cfg.repo, self.queue)
        if not issues:
            self.log("No matching issues in queue.")
            return 0

        self.log(f"Queue: {len(issues)} issue(s) — label={self.queue.label!r} author={self.queue.author!r}")

        succeeded = 0
        failed = 0

        for idx, issue in enumerate(issues, start=1):
            number = issue["number"]
            self.log(f"=== Queue {idx}/{len(issues)}: Issue #{number} — {issue['title'][:60]} ===")

            if repo_is_git(self.cfg.repo) and not self.cfg.dry_run:
                checkout_default_branch(self.cfg.repo)

            self.reset_issue_state(issue)
            loop = MiddleManagerLoop(self.cfg)
            result = loop.run_until_complete()

            if result.success:
                succeeded += 1
                self.log(f"Issue #{number} done.")
                if self.queue.close_on_success:
                    comment = self.queue.close_comment
                    if result.pr_url:
                        comment = f"{comment}\n\nPR: {result.pr_url}"
                    close_issue(
                        self.cfg.repo,
                        number,
                        comment=comment,
                        dry_run=self.cfg.dry_run,
                    )
            else:
                failed += 1
                self.log(f"Issue #{number} incomplete: {result.reason}")

        self.log(f"Queue finished: {succeeded} succeeded, {failed} incomplete.")
        return 0 if failed == 0 else 1