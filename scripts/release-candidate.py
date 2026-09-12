#!/usr/bin/env python3
"""RC lifecycle. GitHub Actions owns authorization; all refs come from origin.

No checkout, local branch switch, force-update, or working-tree commit is used.
The only new commit is the candidate's VERSION bump, made with a private index.
"""

import argparse
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile

from rc_test_report import add_report_links, build_report, report_links


STABLE = re.compile(r"v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\Z")
RC = re.compile(r"(v(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*))-rc\.([1-9][0-9]*)\Z")
SHA = re.compile(r"[0-9a-f]{40}\Z")


class Refusal(Exception):
    pass


class NotReady(Refusal):
    """An ordinary scheduled hold, but a refusal for an explicit mutation."""


def run(*args, input=None, env=None):
    result = subprocess.run(args, input=input, text=True, capture_output=True, env=env, check=False)
    if result.returncode:
        raise Refusal(f"{' '.join(args)} failed:\n{result.stderr.strip()}")
    return result.stdout.strip()


def git(*args, **kwargs):
    return run("git", *args, **kwargs)


def gh(*args):
    return json.loads(run("gh", *args))


def output(**values):
    for key, value in values.items():
        if isinstance(value, bool):
            value = str(value).lower()
        if "\n" in str(value):
            raise Refusal("Invalid multiline workflow output")
        if os.environ.get("GITHUB_OUTPUT"):
            with open(os.environ["GITHUB_OUTPUT"], "a", encoding="utf-8") as target:
                target.write(f"{key}={value}\n")


def summary(text):
    print(text)
    if os.environ.get("GITHUB_STEP_SUMMARY"):
        with open(os.environ["GITHUB_STEP_SUMMARY"], "a", encoding="utf-8") as target:
            target.write(text + "\n")


def check_hold():
    if os.environ.get("AUTO_RELEASE_HOLD", "").lower() in {"1", "true", "yes", "on"}:
        raise NotReady("AUTO_RELEASE_HOLD is set")


class Repository:
    def __init__(self):
        # Fetch failures are errors, never permission to use stale local refs.
        git("fetch", "--quiet", "--prune", "origin",
            "+refs/heads/*:refs/remotes/origin/*", "refs/tags/*:refs/tags/*")
        # Only remote tags count; local-only tags cannot authorize a release.
        self.tags = {}
        peeled = {}
        for line in git("ls-remote", "--tags", "origin").splitlines():
            sha, ref = line.split()
            tag = ref.removeprefix("refs/tags/")
            if tag.endswith("^{}"):
                peeled[tag[:-3]] = sha
            else:
                self.tags[tag] = sha
        self.tags.update(peeled)
        self.branches = {}
        for line in git("for-each-ref", "--format=%(refname:strip=3) %(objectname)",
                        "refs/remotes/origin").splitlines():
            branch, sha = line.split()
            self.branches[branch] = sha

    def ancestor(self, older, newer):
        result = subprocess.run(["git", "merge-base", "--is-ancestor", older, newer],
                                capture_output=True, text=True, check=False)
        if result.returncode not in (0, 1):
            raise Refusal(result.stderr)
        return result.returncode == 0

    def release(self, tag):
        return gh("release", "view", tag, "--json", "tagName,isDraft,isPrerelease,url,body")

    def latest_stable(self):
        tags = [tag for tag in self.tags if STABLE.fullmatch(tag)]
        if not tags:
            raise Refusal("No previous stable vX.Y.Z tag")
        return max(tags, key=lambda tag: tuple(map(int, tag[1:].split("."))))

    def previous_stable(self, version, sha):
        def key(value):
            return tuple(map(int, value[1:].split(".")))

        previous = [value for value in self.tags if STABLE.fullmatch(value) and key(value) < key(version)]
        if not previous:
            raise Refusal("Cannot determine the prior stable tag")
        tag = max(previous, key=key)
        if not self.ancestor(self.tags[tag], sha):
            raise Refusal("Previous stable tag is not an ancestor of this candidate")
        return tag

    def next_version(self):
        latest = self.latest_stable()
        major, minor, patch = map(int, latest[1:].split("."))
        return f"v{major}.{minor}.{patch + 1}"

    def active(self):
        active = []
        for branch, sha in self.branches.items():
            if not branch.startswith("release/"):
                continue
            version = branch.removeprefix("release/")
            if not STABLE.fullmatch(version):
                raise Refusal(f"Unrecognized release branch: {branch}; resolve it explicitly")
            if version not in self.tags:
                active.append(version)
                continue
            if self.tags[version] != sha:
                raise Refusal(f"{branch} changed after stable tagging; do not edit shipped branches")
            release = self.release(version)
            if release["isDraft"] or release["isPrerelease"]:
                raise Refusal(f"Stable publication of {version} is incomplete; retry its Release workflow")
        if len(active) > 1:
            raise Refusal(f"Multiple active candidates: {', '.join(active)}")
        return active

    def successful_workflow(self, workflow, sha, branch):
        # Match the event, exact SHA AND branch/tag. An unrelated green check or
        # an older successful attempt must not authorize this candidate.
        pages = gh("api", "--paginate", "--slurp",
                   f"repos/{{owner}}/{{repo}}/actions/workflows/{workflow}/runs"
                   f"?head_sha={sha}&event=push&per_page=100")
        runs = [item for page in pages for item in page["workflow_runs"]
                if item["head_sha"] == sha and item["head_branch"] == branch
                and item["event"] == "push"]
        if not runs:
            raise NotReady(f"No {workflow} push run for {branch}@{sha[:7]}")
        latest = max(runs, key=lambda item: item["id"])
        if latest["status"] != "completed" or latest["conclusion"] != "success":
            raise NotReady(f"{workflow} is not green for {branch}@{sha[:7]}: {latest['html_url']}")

    def candidates(self, version):
        return sorted((tag for tag in self.tags if RC.fullmatch(tag)
                       and RC.fullmatch(tag)[1] == version),
                      key=lambda tag: int(RC.fullmatch(tag)[2]))

    def candidate(self, tag):
        match = RC.fullmatch(tag)
        if not match or tag not in self.tags:
            raise Refusal("Supply an existing remote candidate tag, e.g. v0.0.112-rc.1")
        version = match[1]
        sha = self.tags[tag]
        branch = f"release/{version}"
        if self.branches.get(branch) != sha:
            raise Refusal(f"{tag} is not the HEAD of {branch}; build and test a new RC")
        if self.candidates(version)[-1] != tag:
            raise Refusal(f"{tag} has been superseded by a newer RC")
        if git("show", f"{sha}:VERSION") != version:
            raise Refusal(f"Candidate VERSION must be {version}")
        return version, sha, branch

    def promotion(self, tag):
        version, sha, branch = self.candidate(tag)
        active = self.active()
        if version in self.tags:
            if self.tags[version] != sha:
                raise Refusal(f"Stable tag {version} already points elsewhere")
            raise Refusal(f"{version} is already tagged; retry/inspect its Release workflow, not promotion")
        if active != [version] or version != self.next_version():
            raise Refusal("Candidate is not the single next release")
        if not self.ancestor(self.branches["main"], sha):
            raise Refusal("main diverged from the candidate; reconcile and test a new RC")
        release = self.release(tag)
        if release["isDraft"] or not release["isPrerelease"]:
            raise Refusal("Candidate must be a published GitHub prerelease")
        self.successful_workflow("ci.yml", sha, branch)
        self.successful_workflow("release.yml", sha, tag)
        return version, sha, branch


def evaluate(repo, candidate=""):
    output(ready=False)
    check_hold()
    active = repo.active()
    if candidate:
        if not STABLE.fullmatch(candidate) or active != [candidate]:
            raise Refusal("Requested version must be the single active release branch")
        sha = repo.branches[f"release/{candidate}"]
        repo.successful_workflow("ci.yml", sha, f"release/{candidate}")
        version = candidate
    else:
        if active:
            summary(f"HOLD — {active[0]} is being tested. dev remains open for new PRs.")
            return
        latest = repo.latest_stable()
        release = repo.release(latest)
        if release["isDraft"] or release["isPrerelease"]:
            raise Refusal(f"Stable release {latest} has not finished publishing")
        sha = repo.branches["dev"]
        if not repo.ancestor(repo.branches["main"], sha) or not repo.ancestor(repo.tags[latest], sha):
            summary("HOLD — merge the last release branch back into dev with a merge commit (not squash).")
            return
        subjects = git("log", f"{repo.tags[latest]}..{sha}", "--format=%s").splitlines()
        prs = [subject for subject in subjects if re.search(r"\(#[0-9]+\)$", subject)]
        minimum = int(os.environ.get("RELEASE_MIN_PRS", "10"))
        if minimum < 1:
            raise Refusal("RELEASE_MIN_PRS must be positive")
        if not subjects or (len(prs) < minimum and os.environ.get("FORCE_RELEASE") != "true"):
            summary(f"HOLD — {len(prs)}/{minimum} unreleased PRs; dev remains open.")
            return
        repo.successful_workflow("ci.yml", sha, "dev")
        version = repo.next_version()
        # PR titles are displayed as inert prose, never shell input.
        summary("Included PRs:\n" + "\n".join(f"- {subject}" for subject in prs))
    output(ready=True, version=version, sha=sha)
    summary(f"Prepare candidate for **{version}** from `{sha}` (no stable publication).")


def version_commit(sha, version):
    # Do not touch the caller's real index, local dev branch or checkout.
    with tempfile.TemporaryDirectory(prefix="ori-rc-index-") as temp:
        # A retry after a rejected push must recreate the same metadata commit,
        # even when it runs later and a local-only RC tag already exists.
        date = git("show", "-s", "--format=%cI", sha)
        env = dict(os.environ, GIT_INDEX_FILE=str(Path(temp) / "index"),
                   GIT_AUTHOR_DATE=date, GIT_COMMITTER_DATE=date)
        git("read-tree", sha, env=env)
        blob = git("hash-object", "-w", "--stdin", input=version + "\n")
        git("update-index", "--add", "--cacheinfo", f"100644,{blob},VERSION", env=env)
        tree = git("write-tree", env=env)
        return git("commit-tree", tree, "-p", sha, env=env,
                   input=f"chore: prepare {version} release candidate\n")


def tag_object(tag, sha, message):
    # Never replace a tag, even a local one left by a failed network push.
    existing = git("tag", "--list", tag)
    if existing:
        if git("rev-parse", f"refs/tags/{tag}^{{commit}}") != sha:
            raise Refusal(f"Local tag {tag} conflicts; inspect it before retrying")
    else:
        git("tag", "-a", tag, sha, "-m", message)
    return f"refs/tags/{tag}"


def prepare(repo, version, sha):
    check_hold()
    if not STABLE.fullmatch(version) or not SHA.fullmatch(sha):
        raise Refusal("Invalid version or full commit SHA")
    active = repo.active()
    if version != repo.next_version() or (active and active != [version]):
        raise Refusal("Only the single next version may be prepared")
    branch = f"release/{version}"
    creating = branch not in repo.branches
    if creating:
        if not repo.ancestor(sha, repo.branches["dev"]):
            raise Refusal("Validated source is no longer on dev")
        if not repo.ancestor(repo.branches["main"], sha):
            raise Refusal("Merge main back into dev before preparing another release")
        repo.successful_workflow("ci.yml", sha, "dev")
        sha = version_commit(sha, version)
    else:
        head = repo.branches[branch]
        if sha != head:
            # Retry after the atomic first push succeeded but its runner lost
            # the response. Only accept our exact VERSION-only child commit.
            parents = git("show", "-s", "--format=%P", head)
            changed = git("diff", "--name-only", sha, head)
            if parents != sha or changed != "VERSION":
                raise Refusal("Release branch advanced; evaluate the candidate again")
            sha = head
        if git("show", f"{sha}:VERSION") != version:
            raise Refusal(f"Release branch VERSION must remain {version}")
        tags = repo.candidates(version)
        if tags and repo.tags[tags[-1]] == sha:
            summary(f"Candidate already exists: {tags[-1]}. Re-run failed Release jobs if needed.")
            output(tag=tags[-1], sha=sha)
            return
        repo.successful_workflow("ci.yml", sha, branch)
    tags = repo.candidates(version)
    number = int(RC.fullmatch(tags[-1])[2]) + 1 if tags else 1
    tag = f"{version}-rc.{number}"
    ref = tag_object(tag, sha, f"Release candidate {tag}")
    args = ["push", "--atomic", "origin", f"{ref}:{ref}"]
    if creating:
        # Creation-only lease: never overwrite a branch another run created.
        args += [f"--force-with-lease=refs/heads/{branch}:", f"{sha}:refs/heads/{branch}"]
    git(*args)
    output(tag=tag, sha=sha)
    summary(f"Candidate **{tag}** queued from `{sha}` on `{branch}`.\n"
            "Installers will appear as a prerelease after package checks pass. dev is unchanged.")


def promote(repo, tag, expected_sha):
    check_hold()
    version, sha, _ = repo.promotion(tag)
    if sha != expected_sha:
        raise Refusal("Candidate changed since the approval preview")
    ref = tag_object(version, sha, f"Release {version}\n\nApproved candidate: {tag}\nCandidate commit: {sha}")
    git("push", "--atomic", "origin",
        f"--force-with-lease=refs/heads/main:{repo.branches['main']}",
        f"{sha}:refs/heads/main", f"{ref}:{ref}")
    summary(f"Stable build **{version}** queued from approved **{tag}** (`{sha}`).\n"
            "Publication still waits for the stable installers to pass smoke tests. dev is unchanged.")


def verify_build(repo, tag):
    check_hold()
    if RC.fullmatch(tag):
        version, sha, _ = repo.candidate(tag)
    elif STABLE.fullmatch(tag) and tag in repo.tags:
        version, sha = tag, repo.tags[tag]
        if repo.branches.get("main") != sha:
            raise Refusal("Stable tag must point at main")
        message = git("for-each-ref", "--format=%(contents)", f"refs/tags/{tag}")
        matches = re.findall(r"^Approved candidate: (\S+)$", message, re.MULTILINE)
        if len(matches) != 1:
            raise Refusal("Stable tag lacks an explicit RC promotion receipt; use Promote Release")
        candidate = matches[0]
        candidate_version, candidate_sha, branch = repo.candidate(candidate)
        if (candidate_version, candidate_sha) != (version, sha):
            raise Refusal("Stable tag does not match the approved candidate")
        release = repo.release(candidate)
        if release["isDraft"] or not release["isPrerelease"]:
            raise Refusal("Approved RC is not a published prerelease")
        repo.successful_workflow("release.yml", sha, candidate)
        repo.successful_workflow("ci.yml", sha, branch)
    else:
        raise Refusal("Unsupported tag; expected vX.Y.Z-rc.N or an approved stable tag")
    if git("rev-parse", "HEAD") != sha:
        raise Refusal("Build checkout does not match the exact release tag")
    # Stable and its approved RC share a commit. GoReleaser's implicit previous
    # tag could therefore be that RC and produce empty stable release notes.
    # A full workflow rerun must not let GoReleaser replace notes/assets after
    # people have begun recording results. Retry only failed jobs after publish.
    pages = gh("api", "--paginate", "--slurp", "repos/{owner}/{repo}/releases?per_page=100")
    if any(release["tag_name"] == tag and not release["draft"] for page in pages for release in page):
        raise Refusal("Release is already published; do not rebuild it. Retry only failed jobs, preserving evidence")
    output(version=tag, prerelease=bool(RC.fullmatch(tag)), previous_tag=repo.previous_stable(version, sha))


def attach_test_report(repo, tag, previous_tag, repository):
    """Actions-only write: attach an immutable blank card, never test results."""
    check_hold()
    name, text = build_report(repo, tag, previous_tag, repository, git)
    block = report_links(repository, tag, repo.tags[tag], name)
    release = gh("release", "view", tag, "--json", "isDraft,isPrerelease,assets,body")
    if not release["isPrerelease"]:
        raise Refusal("Test reports attach only to release candidates")
    notes = add_report_links(release["body"], block)
    matches = [asset for asset in release["assets"] if asset["name"] == name]
    if len(matches) > 1:
        raise Refusal("Duplicate RC report assets; inspect before retrying")
    if not release["isDraft"] and (not matches or notes != release["body"]):
        raise Refusal("Published RC is missing report evidence; do not rewrite it automatically")

    with tempfile.TemporaryDirectory(prefix="ori-rc-report-") as temp:
        path = Path(temp) / name
        if matches:
            # An earlier attempt may have uploaded the card before losing its
            # runner. Reuse identical evidence; never clobber an edited report.
            run("gh", "release", "download", tag, "--pattern", name, "--dir", temp)
            if path.read_bytes() != text.encode("utf-8"):
                raise Refusal("Existing RC report differs; refusing to overwrite it or manual results")
        else:
            path.touch(mode=0o600, exist_ok=False)
            path.write_text(text, encoding="utf-8")
            run("gh", "release", "upload", tag, str(path))
        if notes != release["body"]:
            # Preserve unrelated note edits made while the asset uploaded.
            current = gh("release", "view", tag, "--json", "isDraft,body")
            if not current["isDraft"]:
                raise Refusal("RC was published while attaching the report; inspect its notes manually")
            notes = add_report_links(current["body"], block)
            if notes != current["body"]:
                notes_path = Path(temp) / "notes.md"
                notes_path.touch(mode=0o600, exist_ok=False)
                notes_path.write_text(notes, encoding="utf-8")
                run("gh", "release", "edit", tag, "--notes-file", str(notes_path))
    summary(f"RC test card attached: **{name}**. Manual results are NOT RUN; decision defaults to HOLD.")


def sync_pr(repo, version):
    if not STABLE.fullmatch(version) or version not in repo.tags:
        raise Refusal("Expected a published stable version")
    release = repo.release(version)
    branch = f"release/{version}"
    if release["isDraft"] or release["isPrerelease"] or repo.branches.get(branch) != repo.tags[version]:
        raise Refusal("Stable publication/branch is incomplete")
    if repo.ancestor(repo.tags[version], repo.branches["dev"]):
        summary(f"{version} is already merged back into dev.")
        return
    existing = gh("pr", "list", "--state", "open", "--base", "dev", "--head", branch, "--json", "url")
    if existing:
        summary(f"Merge-back PR: {existing[0]['url']}")
        return
    url = run("gh", "pr", "create", "--base", "dev", "--head", branch,
              "--title", f"chore(release): merge {version} back into dev",
              "--body", f"Stable {version} has shipped. Bring its VERSION bump and stabilization fixes back to dev.\n\n"
              "**Use Create a merge commit, NOT squash/rebase.** Release ancestry is required by the next RC gate. "
              "Resolve conflicts without dropping newer dev work. New feature PRs can continue throughout.\n\n"
              "After CI passes and this PR is merge-committed, the release branch can be deleted. Keep all release tags.")
    summary(f"Merge-back PR: {url}\nUse a merge commit, not squash/rebase. dev remains open.")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    evaluate_parser = commands.add_parser("evaluate")
    evaluate_parser.add_argument("--candidate", default="")
    prepare_parser = commands.add_parser("prepare")
    prepare_parser.add_argument("--version", required=True)
    prepare_parser.add_argument("--sha", required=True)
    check_parser = commands.add_parser("check-promotion")
    check_parser.add_argument("--rc", required=True)
    promote_parser = commands.add_parser("promote")
    promote_parser.add_argument("--rc", required=True)
    promote_parser.add_argument("--sha", required=True)
    verify_parser = commands.add_parser("verify-build")
    verify_parser.add_argument("--tag", required=True)
    sync_parser = commands.add_parser("sync-pr")
    sync_parser.add_argument("--version", required=True)
    report_parser = commands.add_parser("attach-test-report")
    report_parser.add_argument("--rc", required=True)
    report_parser.add_argument("--previous", required=True)
    report_parser.add_argument("--repository", required=True)
    args = parser.parse_args()
    try:
        repo = Repository()
        if args.command == "evaluate":
            evaluate(repo, args.candidate)
        elif args.command == "prepare":
            prepare(repo, args.version, args.sha)
        elif args.command == "check-promotion":
            check_hold()
            version, sha, _ = repo.promotion(args.rc)
            output(version=version, sha=sha)
            summary(f"Approve **{args.rc}** → **{version}**, exact commit `{sha}`. Later dev changes are excluded.")
        elif args.command == "promote":
            promote(repo, args.rc, args.sha)
        elif args.command == "verify-build":
            verify_build(repo, args.tag)
        elif args.command == "sync-pr":
            sync_pr(repo, args.version)
        elif args.command == "attach-test-report":
            attach_test_report(repo, args.rc, args.previous, args.repository)
    except NotReady as error:
        if args.command == "evaluate":
            summary(f"HOLD — {error}")
            return 0
        summary(f"REFUSED — {error}")
        return 1
    except (Refusal, ValueError, KeyError) as error:
        summary(f"REFUSED — {error}")
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
