"""Git and GitHub helpers (stdlib + subprocess only)."""

from __future__ import annotations

import json
import re
import subprocess
from pathlib import Path

from .config import IssueQueueConfig


def run_git(repo: Path, *args: str, check: bool = True) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        ["git", *args],
        cwd=repo,
        capture_output=True,
        text=True,
        check=check,
    )


def repo_is_git(repo: Path) -> bool:
    return (repo / ".git").exists()


def current_branch(repo: Path) -> str:
    return run_git(repo, "rev-parse", "--abbrev-ref", "HEAD").stdout.strip()


def has_changes(repo: Path) -> bool:
    return bool(run_git(repo, "status", "--porcelain").stdout.strip())


def detect_base_branch(repo: Path) -> str:
    for candidate in ("dev", "main", "master"):
        proc = run_git(repo, "rev-parse", "--verify", candidate, check=False)
        if proc.returncode == 0:
            return candidate
    try:
        return current_branch(repo)
    except Exception:
        return "main"


def ensure_branch(repo: Path, prefix: str, iteration: int, base_branch: str | None = None) -> str:
    branch = f"{prefix}/loop-{iteration}"
    branches = run_git(repo, "branch", "--list", branch, check=False).stdout
    if branch in branches or f"* {branch}" in branches:
        run_git(repo, "checkout", branch)
    else:
        cmd = ["checkout", "-b", branch]
        if base_branch:
            cmd.append(base_branch)
        run_git(repo, *cmd)
    return branch


def commit_all(repo: Path, message: str) -> bool:
    if not has_changes(repo):
        return False
    run_git(repo, "add", "-A")
    run_git(repo, "commit", "-m", message)
    return True


def push_branch(repo: Path, branch: str, *, dry_run: bool = False) -> None:
    if dry_run:
        print(f"[dry-run] git push -u origin {branch}")
        return
    try:
        res = subprocess.run(
            ["git", "remote"], cwd=str(repo), capture_output=True, text=True, check=True
        )
        remotes = res.stdout.splitlines()
        if "origin" not in remotes:
            print(f"[git] No 'origin' remote found, skipping push of branch '{branch}'.")
            return
        run_git(repo, "push", "-u", "origin", branch)
    except Exception as e:
        print(f"[git] Warning: Failed to push branch '{branch}' to origin: {e}")


def gh_available() -> bool:
    try:
        subprocess.run(["gh", "--version"], capture_output=True, check=True)
        return True
    except (FileNotFoundError, subprocess.CalledProcessError):
        return False


def fetch_issue(repo: Path, issue_ref: str) -> dict[str, str]:
    if not gh_available():
        return {"number": issue_ref, "title": "", "body": "", "url": issue_ref}
    m = re.search(r"(\d+)$", issue_ref)
    number = m.group(1) if m else issue_ref
    proc = subprocess.run(
        ["gh", "issue", "view", number, "--json", "number,title,body,url"],
        cwd=repo,
        capture_output=True,
        text=True,
    )
    if proc.returncode != 0:
        return {"number": number, "title": "", "body": proc.stderr, "url": issue_ref}
    data = json.loads(proc.stdout)
    return {
        "number": str(data.get("number", number)),
        "title": data.get("title", ""),
        "body": data.get("body", ""),
        "url": data.get("url", issue_ref),
    }


def list_issues(repo: Path, queue: IssueQueueConfig) -> list[dict[str, str]]:
    """List GitHub issues matching label/author filters via gh CLI."""
    if not gh_available():
        return []

    args = [
        "gh",
        "issue",
        "list",
        "--state",
        queue.state,
        "--json",
        "number,title,body,url,labels,author",
        "--limit",
        str(queue.limit),
    ]
    if queue.label:
        args.extend(["--label", queue.label])
    if queue.author:
        author = queue.author.lstrip("@")
        args.extend(["--author", author])

    proc = subprocess.run(args, cwd=repo, capture_output=True, text=True)
    if proc.returncode != 0:
        print(proc.stderr or proc.stdout)
        return []

    items = json.loads(proc.stdout or "[]")
    rows: list[dict[str, str]] = []
    for item in items:
        rows.append(
            {
                "number": str(item.get("number", "")),
                "title": item.get("title", ""),
                "body": item.get("body", "") or "",
                "url": item.get("url", ""),
                "author": (item.get("author") or {}).get("login", ""),
            }
        )
    return rows


def close_issue(
    repo: Path,
    number: str,
    *,
    comment: str | None = None,
    dry_run: bool = False,
) -> bool:
    if dry_run:
        print(f"[dry-run] gh issue close {number}" + (f" --comment {comment!r}" if comment else ""))
        return True
    if not gh_available():
        print("gh CLI not available; cannot close issue")
        return False
    args = ["gh", "issue", "close", number]
    if comment:
        args.extend(["--comment", comment])
    proc = subprocess.run(args, cwd=repo, capture_output=True, text=True)
    if proc.returncode != 0:
        print(proc.stderr or proc.stdout)
        return False
    return True


def ensure_issue_branch(repo: Path, prefix: str, issue_number: str, base_branch: str | None = None) -> str:
    branch = f"{prefix}/issue-{issue_number}"
    branches = run_git(repo, "branch", "--list", branch, check=False).stdout
    if branch in branches or f"* {branch}" in branches:
        run_git(repo, "checkout", branch)
    else:
        cmd = ["checkout", "-b", branch]
        if base_branch:
            cmd.append(base_branch)
        run_git(repo, *cmd)
    return branch


def checkout_default_branch(repo: Path) -> None:
    for candidate in ("main", "master"):
        proc = run_git(repo, "rev-parse", "--verify", candidate, check=False)
        if proc.returncode == 0:
            run_git(repo, "checkout", candidate)
            return


def plan_is_complete(plan_text: str) -> bool:
    pending = False
    for line in plan_text.splitlines():
        s = line.strip()
        if s.startswith("- [ ]"):
            pending = True
            break
    if not pending:
        # No unchecked items — done if there's at least one checked or any content
        return bool(plan_text.strip())
    return False


def create_pr(
    repo: Path,
    *,
    title: str,
    body: str,
    branch: str,
    issue_number: str | None,
    dry_run: bool = False,
) -> str | None:
    if dry_run:
        print(f"[dry-run] gh pr create --head {branch} --title {title!r}")
        return None
    if not gh_available():
        print("gh CLI not available; skipping PR creation")
        return None
    args = ["gh", "pr", "create", "--head", branch, "--title", title, "--body", body]
    if issue_number and issue_number.isdigit():
        args.extend(["--issue", issue_number])
    proc = subprocess.run(args, cwd=repo, capture_output=True, text=True)
    if proc.returncode != 0:
        print(proc.stderr)
        return None
    return proc.stdout.strip()