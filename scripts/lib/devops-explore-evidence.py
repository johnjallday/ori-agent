#!/usr/bin/env python3
"""Bounded, read-only evidence for advisors that deliberately have no shell tool.

Only fixed argv below are executed. No prompt/context text is a command input.
Output is JSON so terminal controls and evidence delimiters remain inert text.
"""

import json
import os
from pathlib import Path
import re
import signal
import subprocess
import sys
from datetime import datetime, timezone


TIMEOUT_SECONDS = 10
SOURCE_LIMIT = 4096
GIT = ["git", "--no-optional-locks", "-c", "core.fsmonitor=false"]


def capture(root, argv, limit=SOURCE_LIMIT):
    env = dict(os.environ, GH_PROMPT_DISABLED="1", GH_PAGER="cat", GIT_TERMINAL_PROMPT="0")
    try:
        # A separate group lets timeout kill descendants as well as the CLI.
        process = subprocess.Popen(
            argv, cwd=root, env=env, stdin=subprocess.DEVNULL,
            stdout=subprocess.PIPE, stderr=subprocess.STDOUT, start_new_session=True,
        )
    except OSError as error:
        return {"status": "unavailable", "error": str(error)}
    timed_out = False
    try:
        output, _ = process.communicate(timeout=TIMEOUT_SECONDS)
    except subprocess.TimeoutExpired:
        timed_out = True
        try:
            os.killpg(process.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
        output, _ = process.communicate()
    return {
        "status": "timeout" if timed_out else ("ok" if process.returncode == 0 else "unavailable"),
        "exit_code": process.returncode,
        "truncated": len(output) > limit,
        "output": output[:limit].decode("utf-8", errors="replace"),
    }


def task_progress(worktree_output):
    """Use exact checked-out branch identities, not stale copied dev plans."""
    records = []
    for record in worktree_output.split("\0\0"):
        fields = record.split("\0")
        path = next((field[9:] for field in fields if field.startswith("worktree ")), "")
        branch = next((field[7:] for field in fields if field.startswith("branch ")), "")
        if not path or not branch.startswith("refs/heads/"):
            continue
        name = branch[len("refs/heads/"):]
        if "/" not in name:
            continue  # dev/main are not authoritative feature implementations.
        slug = name.split("/", 1)[1]
        if "/" in slug or slug in ("", ".", ".."):
            records.append({"worktree": path, "status": "unsupported task-list identity"})
            continue
        task = Path(path) / "tasks" / ("tasks-" + slug + ".md")
        item = {"worktree": path, "branch": name, "task_file": str(task)}
        try:
            if task.is_symlink() or task.parent.is_symlink():
                raise OSError("symlinked task lists are not read by the snapshot collector")
            with task.open("rb") as source:
                raw = source.read(16385)
            text = raw[:16384].decode("utf-8", errors="replace")
            checks = re.findall(r"^\s*- \[([ xX])\] (.+)$", text, re.MULTILINE)
            item.update(
                status="ok", truncated=len(raw) > 16384,
                checked=sum(mark.lower() == "x" for mark, _ in checks), total=len(checks),
                next_unchecked=[label[:180] for mark, label in checks if mark == " "][:4],
            )
        except OSError as error:
            item.update(status="unavailable", error=str(error))
        records.append(item)
        if len(records) == 12:
            break
    return {
        "limit": "First 12 feature worktrees; counts include parent and child checkboxes. Inspect full task lists for detail.",
        "records": records,
        "agent_state": "Not collected. A branch/worktree is in-flight evidence, not proof an agent is running or idle.",
    }


def collect(root):
    worktrees = capture(root, GIT + ["worktree", "list", "--porcelain", "-z"])
    return {
        "captured_at": datetime.now(timezone.utc).isoformat(),
        "repository": str(root),
        "limits": "Bounded snapshot, not exhaustive. Each command has a 10s timeout. Truncated output is partial evidence, never a zero count.",
        "checkout_status": capture(root, GIT + ["status", "--short", "--branch"]),
        "worktrees": worktrees,
        "local_branches": capture(root, GIT + ["for-each-ref", "--count=100", "--sort=-committerdate", "--format=%(refname:short)", "refs/heads"]),
        "recent_changes": capture(root, GIT + ["log", "-12", "--format=%h %s", "--name-only"]),
        "active_tasks": task_progress(worktrees.get("output", "")) if worktrees["status"] == "ok" else {"status": "unavailable"},
        "open_issues": capture(root, [
            "gh", "issue", "list", "--state", "open", "--limit", "100",
            "--json", "number,title,labels,updatedAt,url,body",
            "--jq", '[.[] | {number,title:(.title[:160]),labels:[.labels[].name],updatedAt,url,body:(.body[:1200])}]',
        ], limit=10000),
        "open_dev_prs": capture(root, [
            "gh", "pr", "list", "--base", "dev", "--state", "open", "--limit", "40",
            "--json", "number,title,headRefName,isDraft,reviewDecision,url",
        ], limit=6000),
        "issue_scope": "At most 100 open Issues, bodies clipped to 1200 characters; comments are not collected. Labels are evidence, not planning authorization. PRs: at most 40 open targeting dev.",
    }


if __name__ == "__main__":
    if len(sys.argv) == 3 and sys.argv[1] == "--agent-help" and sys.argv[2] in ("claude", "pi"):
        help_result = capture(Path.cwd(), [sys.argv[2], "--help"], limit=40000)
        if help_result["status"] != "ok" or help_result.get("truncated"):
            print("Cannot verify native advisor options: " + json.dumps(help_result), file=sys.stderr)
            sys.exit(1)
        print(help_result["output"])
        sys.exit(0)
    if len(sys.argv) != 2 or not Path(sys.argv[1]).is_dir():
        sys.exit("usage: devops-explore-evidence.py <repository-directory>")
    print(json.dumps(collect(Path(sys.argv[1])), ensure_ascii=True, indent=2))
