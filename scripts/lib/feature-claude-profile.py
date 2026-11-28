"""Provision Ori's local Claude profile; never create or modify a plugin worktree."""
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile

PLUGINS = {
    "music-project-management": Path("music-project-management"),
    "reaper-plugin": Path("ori/plugins/reaper-plugin"),
}
DENY = ["Bash(rm -rf:*)", "Bash(git push --force:*)", "Bash(git push -f:*)",
        "Bash(./scripts/release.sh:*)", "Read(**/.env)", "Read(**/*secret*)"]


def git(root, *args):
    return subprocess.check_output(["git", "-C", root, *args], text=True).removesuffix("\n")


def plugin_tree(root, feature):
    return root.with_name(root.name + "-worktrees") / feature


def prepared(root, feature):
    """Resolve only the conventional linked tree, registered on the matching branch."""
    tree = plugin_tree(root, feature)
    if (root.resolve() != root or tree.resolve() != tree or not (root / ".git").is_dir()
            or (root / ".git").is_symlink()
            or not (tree / ".git").is_file() or (tree / ".git").is_symlink()):
        return False
    branch = "refs/heads/feature/" + feature
    # Directory names alone cannot identify a repository, a released candidate,
    # or a copied .git file pointing at a different worktree's admin directory.
    repo = "johnjallday/" + root.name
    origins = {prefix + repo + suffix for prefix in ("https://github.com/", "git@github.com:")
               for suffix in ("", ".git")}
    if (git(root, "remote", "get-url", "origin") not in origins
            or Path(git(tree, "rev-parse", "--path-format=absolute", "--git-common-dir")) != root / ".git"
            or git(tree, "symbolic-ref", "--quiet", "HEAD") != branch):
        return False
    entries = git(root, "worktree", "list", "--porcelain", "-z").split("\0\0")
    return any(entry.startswith(f"worktree {tree}\0") and "branch " + branch in entry.split("\0")
               for entry in entries)


def paired_dirs(target, dev):
    feature = target.name
    if not dev or not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9_-]*", feature):
        return []
    plan = Path(dev) / "tasks" / f"tasks-{feature}.md"
    if not plan.is_file() or plan.resolve() != plan:
        return []
    text = plan.read_text()
    # Only consume the exact metadata header, never paths, Issue text, task
    # descriptions, or quoted examples. Legacy plans and starters grant nothing.
    if "ori-devflow: planning-starter" in text:
        return []
    lines = [line for line in text.splitlines() if line.strip()][:2]
    if len(lines) < 2 or not lines[0].startswith("# ") or not lines[1].startswith("Paired plugins:"):
        return []
    match = re.fullmatch(r"Paired plugins: `([a-z-]+(?:, [a-z-]+)*)`", lines[1])
    names = match[1].split(", ") if match else []
    if not names or set(names) - PLUGINS.keys() or len(set(names)) != len(names):
        raise ValueError("invalid Paired plugins header; name each supported plugin once, not a path")
    if not (target / ".git").is_file() or git(target, "symbolic-ref", "--quiet", "HEAD") != "refs/heads/feature/" + feature:
        return []
    dirs = []
    for name in names:
        root = Path.home() / "Projects" / PLUGINS[name]
        try:
            ok = prepared(root, feature)
        except subprocess.CalledProcessError:
            ok = False
        if not ok:
            print(f"No Claude grant for {name}: prepare its registered feature/{feature} worktree first.", file=sys.stderr)
            continue
        dirs.append(str(plugin_tree(root, feature)))
    return dirs


def provision(target, dev, mode):
    profile = target / ".claude/settings.local.json"
    if profile.parent.is_symlink() or profile.is_symlink():
        raise ValueError("refusing a symlinked Claude profile or directory")
    dirs = paired_dirs(target, dev) if mode == "acceptEdits" else []
    if profile.exists():
        settings = json.loads(profile.read_text())
        perms = settings.setdefault("permissions", {})
        if dirs and perms.get("defaultMode") == "bypassPermissions":
            raise ValueError("refusing to add plugin worktrees in bypassPermissions mode")
        existing = perms.get("additionalDirectories", [])
        if not isinstance(existing, list) or not all(isinstance(p, str) for p in existing):
            raise ValueError("invalid permissions.additionalDirectories in existing local profile")
        managed = {str(plugin_tree(Path.home() / "Projects" / root, target.name)) for root in PLUGINS.values()}
        keep = [d for d in existing if d not in managed or d in dirs]
        updated = keep + [d for d in dirs if d not in keep]
        if updated == existing:
            return
        perms["additionalDirectories"] = updated
    else:
        settings = {"permissions": {"defaultMode": mode, "deny": DENY}}
        if dirs:
            settings["permissions"]["additionalDirectories"] = dirs
    profile.parent.mkdir(mode=0o750, exist_ok=True)
    temp = tempfile.NamedTemporaryFile(mode="w", dir=profile.parent, delete=False)
    try:
        with temp:
            temp.write(json.dumps(settings, indent=2) + "\n")
        os.replace(temp.name, profile)
    finally:
        Path(temp.name).unlink(missing_ok=True)
    for directory in dirs:
        print(f"Claude paired plugin worktree: {directory}")


if __name__ == "__main__":
    try:
        provision(Path(sys.argv[1]), sys.argv[2], sys.argv[3])
    except (OSError, ValueError, AttributeError, subprocess.CalledProcessError) as exc:
        sys.exit(f"Cannot provision feature Claude profile: {exc}")
