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
HOLD = {"1", "true", "yes", "on"}
PENDING = {"requested", "queued", "waiting", "pending", "in_progress"}


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
    if os.environ.get("AUTO_RELEASE_HOLD", "").lower() in HOLD:
        raise NotReady("AUTO_RELEASE_HOLD is set")


def hold_active():
    """The brake as a workflow sees it: the runner's variable, or the repository variable behind it.

    None when the token cannot read repository variables (a personal token
    without Actions scope): unknown must not be reported as released.
    """
    if os.environ.get("AUTO_RELEASE_HOLD", "").lower() in HOLD:
        return True
    try:
        variables = gh("variable", "list", "--json", "name,value")
    except Refusal:
        return None
    return any(item["name"] == "AUTO_RELEASE_HOLD" and str(item["value"]).lower() in HOLD for item in variables)


class Repository:
    def __init__(self):
        # Fetch failures are errors, never permission to use stale local refs.
        # Tags are forced: actions/checkout on a tag push rewrites the pushed
        # annotated tag as a lightweight one, and local tags must match origin.
        git("fetch", "--quiet", "--prune", "origin",
            "+refs/heads/*:refs/remotes/origin/*", "+refs/tags/*:refs/tags/*")
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

    def workflow_state(self, workflow, sha, branch):
        """The latest push run of a workflow for this exact commit and branch/tag, or None."""
        # Match the event, exact SHA AND branch/tag. An unrelated green check or
        # an older successful attempt must not authorize this candidate.
        pages = gh("api", "--paginate", "--slurp",
                   f"repos/{{owner}}/{{repo}}/actions/workflows/{workflow}/runs"
                   f"?head_sha={sha}&event=push&per_page=100")
        runs = [item for page in pages for item in page["workflow_runs"]
                if item["head_sha"] == sha and item["head_branch"] == branch
                and item["event"] == "push"]
        if not runs:
            return None
        latest = max(runs, key=lambda item: item["id"])
        return {"status": latest["status"], "conclusion": latest["conclusion"], "url": latest["html_url"]}

    def successful_workflow(self, workflow, sha, branch):
        state = self.workflow_state(workflow, sha, branch)
        if state is None:
            raise NotReady(f"No {workflow} push run for {branch}@{sha[:7]}")
        if state["status"] != "completed" or state["conclusion"] != "success":
            raise NotReady(f"{workflow} is not green for {branch}@{sha[:7]}: {state['url']}")

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


def landed_pr(subject):
    """A squash merge ends in (#N); a merge commit names the PR and its branch."""
    if re.search(r"\(#[0-9]+\)$", subject):
        return True
    # The release merge-back has to be a merge commit, and it ships nothing new.
    merged = re.match(r"Merge pull request #[0-9]+ from [^/\s]+/(\S+)", subject)
    return bool(merged) and not merged[1].startswith("release/")


def landed(since, sha):
    """dev's own line since the stable tag: every subject, and the ones that are landed PRs."""
    # dev's own line holds one commit per landed PR, whichever merge button
    # was used; a merged branch's commits sit behind the second parent.
    subjects = git("log", "--first-parent", f"{since}..{sha}", "--format=%s").splitlines()
    return subjects, [subject for subject in subjects if landed_pr(subject)]


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
        subjects, prs = landed(repo.tags[latest], sha)
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
    # dev's required status checks make auto-merge wait for this PR's CI, and the
    # merge commit keeps release ancestry for the next cut. A conflict leaves the
    # PR open for a person instead of failing the stable publication.
    try:
        run("gh", "pr", "merge", url, "--merge", "--auto")
    except Refusal as error:
        summary(f"Merge-back PR: {url}\nAuto-merge could not be enabled ({error}); merge it with a merge commit by hand.")
        return
    summary(f"Merge-back PR: {url}\nAuto-merge (merge commit) is enabled; it lands once dev's checks pass. dev remains open.")


def pending_promotion(tag):
    """The Promote Release run for this tag still queued or awaiting approval, if any."""
    runs = gh("run", "list", "--workflow", "promote-release.yml", "--limit", "50",
              "--json", "status,displayTitle,url")
    for item in runs:
        if item["status"] in PENDING and f" {tag} " in f" {item['displayTitle']} ":
            return item
    return None


def request_promotion(repo, tag):
    """Ask for the human approval once an RC has every automated check green.

    Nothing is published here: the dispatched Promote Release run stops at the
    `release` environment review, which a person approves.
    """
    version = RC.fullmatch(tag)[1]
    sha = repo.tags[tag]
    try:
        repo.successful_workflow("ci.yml", sha, f"release/{version}")
        repo.successful_workflow("release.yml", sha, tag)
    except NotReady as error:
        summary(f"Not yet: {error}")
        return
    # Every other promotion precondition; failing one here is a real inconsistency.
    repo.promotion(tag)
    if pending_promotion(tag):
        summary(f"Promotion of {tag} is already awaiting approval.")
        return
    run("gh", "workflow", "run", "promote-release.yml", "--ref", "main",
        "-f", f"rc_tag={tag}", "-f", "confirm_tested=true")
    summary(f"Promotion of **{tag}** requested. Approve the `release` environment review in Actions "
            f"to publish {version}; nothing is published until then.")


def merge_sync_prs(repo, sha):
    """Land the release watcher's dev→release sync PR once dev CI is green for that exact commit."""
    active = repo.active()
    if not active:
        summary("Nothing to do: no active release branch.")
        return
    branch = f"release/{active[0]}"
    if not repo.ancestor(sha, repo.branches["dev"]):
        summary(f"Nothing to do: {sha[:7]} is not on dev.")
        return
    merged = []
    for pr in gh("pr", "list", "--state", "open", "--base", branch, "--json", "number,url,headRefName,headRefOid"):
        if pr.get("headRefName", "").startswith("release-sync/") and pr.get("headRefOid") == sha:
            # GH_TOKEN is RELEASE_PAT, so this merge's push runs CI on the release branch.
            run("gh", "pr", "merge", str(pr["number"]), "--merge")
            merged.append(pr["url"])
    if merged:
        summary(f"Merged into {branch} with a merge commit: {', '.join(merged)}. Its CI run re-cuts the candidate.")
    else:
        summary(f"Nothing to do: no release-sync PR into {branch} at {sha[:7]}.")


def react(repo, workflow, event, branch, sha, conclusion):
    """Act on a finished CI or Release run: re-cut, land a sync PR or request promotion.

    Failures are left alone; the release watcher routine investigates those.
    """
    check_hold()
    if conclusion != "success" or event != "push":
        summary(f"Nothing to do: {workflow} ({event}) on {branch} ended {conclusion}.")
        return
    if not SHA.fullmatch(sha):
        raise Refusal("Invalid full commit SHA")
    if workflow == "CI" and branch == "dev":
        merge_sync_prs(repo, sha)
    elif workflow == "CI" and branch.startswith("release/"):
        version = branch.removeprefix("release/")
        if not STABLE.fullmatch(version):
            raise Refusal(f"Unrecognized release branch: {branch}")
        if repo.branches.get(branch) != sha:
            summary(f"Nothing to do: {branch} has moved past {sha[:7]}; its newer run decides.")
        elif version in repo.tags:
            summary(f"Nothing to do: {version} is already stable.")
        else:
            tags = repo.candidates(version)
            if tags and repo.tags[tags[-1]] == sha:
                request_promotion(repo, tags[-1])
            else:
                prepare(repo, version, sha)
    elif workflow == "Release" and RC.fullmatch(branch):
        if repo.tags.get(branch) != sha:
            raise Refusal(f"{branch} no longer points at the built commit")
        request_promotion(repo, branch)
    else:
        summary(f"Nothing to do: {workflow} on {branch}.")


def conclusion(state):
    """One word for a workflow_state: its conclusion, else its status, else missing."""
    if state is None:
        return "missing"
    return state["conclusion"] if state["status"] == "completed" else state["status"]


def failed(state):
    """A finished run that did not succeed; waiting or missing runs are not failures."""
    return state is not None and state["status"] == "completed" and state["conclusion"] != "success"


def describe(report):
    """One plain line: what happens next, and whether a workflow, a script or a person does it."""
    if "problem" in report:
        return f"Inspect by hand: {report['problem']}"
    prefix = "HOLD is set; nothing automatic runs. " if report["hold"] else ""
    if report["hold"] is None:
        prefix = "Hold unknown (this token cannot read repository variables). "
    dev, release = report["dev"], report["release"]
    if release is None:
        if not dev["merged_back"]:
            where = report["merge_back_pr"] or "no PR is open"
            return prefix + f"Merge {report['latest_stable']} back into dev with a merge commit ({where})."
        count = f"{dev['unreleased_prs']}/{dev['minimum_prs']} PRs toward {report['next_version']}"
        if failed(dev["ci"]):
            # Red dev CI blocks the cut whatever the count; it is the one thing a person or ci-triage must act on.
            return prefix + f"dev CI failed for {dev['sha'][:7]} ({dev['ci']['url']}); run ci-triage. {count}."
        if dev["unreleased_prs"] < dev["minimum_prs"]:
            return prefix + f"{count}; the daily gate cuts it at the minimum, or ./scripts/release.sh candidate --force."
        if conclusion(dev["ci"]) != "success":
            return prefix + f"dev CI is {conclusion(dev['ci'])} for {dev['sha'][:7]}; the gate waits for it."
        return prefix + (f"Ready: the daily gate cuts {report['next_version']} from dev {dev['sha'][:7]}, "
                         "or ./scripts/release.sh candidate.")
    suffix = f" Sync PR waiting for green dev CI: {', '.join(release['sync_prs'])}." if release["sync_prs"] else ""
    tag = release["latest_candidate"]
    if not release["candidate_at_head"]:
        if conclusion(release["ci"]) == "success":
            return prefix + (f"{release['branch']} is green at {release['sha'][:7]} and untagged; react tags the "
                             f"next RC, or ./scripts/release.sh candidate {release['version']}.") + suffix
        if failed(release["ci"]):
            return prefix + (f"{release['branch']} head {release['sha'][:7]} is untagged and its CI failed "
                             f"({release['ci']['url']}); run ci-triage.") + suffix
        return prefix + (f"{release['branch']} head {release['sha'][:7]} is untagged and its CI is "
                         f"{conclusion(release['ci'])}; it is tagged once green.") + suffix
    promotion = release["promotion"]
    if promotion["state"] == "awaiting_approval":
        return prefix + (f"Approve the release environment on the Promote Release run for {tag}: "
                         f"{promotion.get('url') or 'see Actions'}.") + suffix
    if promotion["state"] == "ready":
        return prefix + (f"{tag} is ready: react dispatches Promote Release, or ./scripts/release.sh promote {tag}; "
                         "then approve the release environment.") + suffix
    return prefix + f"{tag} is not promotable yet: {promotion['reason']}" + suffix


def status(repo):
    """Where the lifecycle stands, as JSON for people and agents. Reads only; never a hold or refusal."""
    latest = repo.latest_stable()
    dev = repo.branches["dev"]
    _, prs = landed(repo.tags[latest], dev)
    report = {
        "hold": hold_active(),
        "latest_stable": latest,
        "next_version": repo.next_version(),
        "dev": {
            "sha": dev,
            "ci": repo.workflow_state("ci.yml", dev, "dev"),
            "unreleased_prs": len(prs),
            "minimum_prs": int(os.environ.get("RELEASE_MIN_PRS", "10")),
            "merged_back": repo.ancestor(repo.tags[latest], dev) and repo.ancestor(repo.branches["main"], dev),
        },
        "merge_back_pr": None,
        "release": None,
    }
    if not report["dev"]["merged_back"]:
        open_prs = gh("pr", "list", "--state", "open", "--base", "dev", "--head", f"release/{latest}", "--json", "url")
        report["merge_back_pr"] = open_prs[0]["url"] if open_prs else None
    try:
        active = repo.active()
    except Refusal as error:
        # The gate would refuse here; the status says why instead of hiding the rest.
        report["problem"] = str(error)
        active = []
    if active:
        version = active[0]
        branch = f"release/{version}"
        sha = repo.branches[branch]
        tags = repo.candidates(version)
        tag = tags[-1] if tags else None
        release = {
            "version": version, "branch": branch, "sha": sha,
            "ci": repo.workflow_state("ci.yml", sha, branch),
            "candidates": tags, "latest_candidate": tag,
            "candidate_at_head": bool(tag) and repo.tags[tag] == sha,
            "prerelease": None, "release_workflow": None, "promotion": None,
            "sync_prs": [pr["url"] for pr in gh("pr", "list", "--state", "open", "--base", branch,
                                                 "--json", "url,headRefName")
                         if pr.get("headRefName", "").startswith("release-sync/")],
        }
        if tag:
            try:
                published = repo.release(tag)
                release["prerelease"] = ("draft" if published["isDraft"]
                                         else "published" if published["isPrerelease"] else "not_prerelease")
            except Refusal:
                release["prerelease"] = "missing"
            release["release_workflow"] = repo.workflow_state("release.yml", repo.tags[tag], tag)
            pending = pending_promotion(tag)
            if pending:
                release["promotion"] = {"state": "awaiting_approval", "url": pending.get("url")}
            else:
                try:
                    repo.promotion(tag)
                    release["promotion"] = {"state": "ready"}
                except Refusal as error:
                    release["promotion"] = {"state": "blocked", "reason": str(error)}
        report["release"] = release
    report["next"] = describe(report)
    print(json.dumps(report, indent=2))
    return report


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
    react_parser = commands.add_parser("react")
    for name in ("workflow", "event", "branch", "sha", "conclusion"):
        react_parser.add_argument(f"--{name}", required=True)
    commands.add_parser("status")
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
        elif args.command == "react":
            react(repo, args.workflow, args.event, args.branch, args.sha, args.conclusion)
        elif args.command == "status":
            status(repo)
    except NotReady as error:
        if args.command in ("evaluate", "react"):
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
