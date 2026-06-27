"""Git and GitHub helpers (stdlib + subprocess only)."""

from __future__ import annotations

import json
import re
import subprocess
from pathlib import Path


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


def ensure_branch(repo: Path, prefix: str, iteration: int) -> str:
    branch = f"{prefix}/loop-{iteration}"
    branches = run_git(repo, "branch", "--list", branch, check=False).stdout
    if branch in branches or f"* {branch}" in branches:
        run_git(repo, "checkout", branch)
    else:
        run_git(repo, "checkout", "-b", branch)
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
    run_git(repo, "push", "-u", "origin", branch)


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