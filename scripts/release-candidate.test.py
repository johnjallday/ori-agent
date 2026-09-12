#!/usr/bin/env python3
"""Offline lifecycle regressions: real temporary Git remotes, mocked GitHub reads."""

import contextlib
import importlib.util
import io
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch
from urllib.parse import parse_qs, urlparse


ROOT = Path(__file__).resolve().parent.parent
spec = importlib.util.spec_from_file_location("release_candidate", ROOT / "scripts/release-candidate.py")
rc = importlib.util.module_from_spec(spec)
spec.loader.exec_module(rc)


class GitFixture(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="ori-rc-test-")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.original_cwd = Path.cwd()
        self.addCleanup(os.chdir, self.original_cwd)
        self.repo = self.root / "checkout"
        self.remote = self.root / "origin.git"
        subprocess.run(["git", "init", "--bare", "--quiet", str(self.remote)], check=True)
        subprocess.run(["git", "clone", "--quiet", str(self.remote), str(self.repo)], check=True,
                       capture_output=True)
        os.chdir(self.repo)
        for key, value in {"user.name": "Test", "user.email": "test@example.invalid",
                           "commit.gpgsign": "false", "tag.gpgsign": "false",
                           "core.hooksPath": str(self.root / "no-hooks")}.items():
            rc.git("config", key, value)
        rc.git("checkout", "-b", "main")
        Path("VERSION").write_text("v1.2.3\n")
        Path("product").write_text("stable\n")
        rc.git("add", "VERSION", "product")
        rc.git("commit", "-qm", "Initial stable")
        rc.git("tag", "v1.2.3")
        rc.git("push", "--quiet", "origin", "main", "v1.2.3")
        rc.git("checkout", "-qb", "dev")
        for number in range(1, 11):
            self.commit(f"feat: change (#{number})")
        rc.git("push", "--quiet", "origin", "dev")
        self.source = rc.git("rev-parse", "HEAD")
        self.releases = {"v1.2.3": {"isDraft": False, "isPrerelease": False}}
        self.runs = {}
        self.env = patch.dict(os.environ, {"GITHUB_OUTPUT": str(self.root / "output"),
                                          "GITHUB_STEP_SUMMARY": "", "AUTO_RELEASE_HOLD": "",
                                          "FORCE_RELEASE": "false", "RELEASE_MIN_PRS": "10"})
        self.env.start()
        self.addCleanup(self.env.stop)
        self.gh_patch = patch.object(rc, "gh", side_effect=self.github)
        self.gh_patch.start()
        self.addCleanup(self.gh_patch.stop)
        self.stdout = contextlib.redirect_stdout(io.StringIO())
        self.stdout.__enter__()
        self.addCleanup(self.stdout.__exit__, None, None, None)

    def github(self, *args):
        if args[:2] == ("release", "view"):
            tag = args[2]
            if tag not in self.releases:
                raise rc.Refusal(f"Release {tag} not found")
            return dict(self.releases[tag], tagName=tag, url=f"https://example.invalid/{tag}", body="")
        if args[0] == "api":
            url = args[-1]
            if "/releases?" in url:
                return [[{"tag_name": tag, "draft": release["isDraft"]} for tag, release in self.releases.items()]]
            workflow = url.split("/workflows/")[1].split("/")[0]
            sha = parse_qs(urlparse(url).query)["head_sha"][0]
            if (workflow, sha) in self.runs:
                return [{"workflow_runs": self.runs[workflow, sha]}]
            refs = rc.Repository()
            branches = [branch for branch, target in refs.branches.items() if target == sha]
            tags = [tag for tag, target in refs.tags.items() if target == sha]
            return [{"workflow_runs": [self.workflow_run(sha, branch) for branch in branches + tags]}]
        if args[:2] == ("pr", "list"):
            return [{"url": "https://example.invalid/merge-back"}]
        raise AssertionError(args)

    @staticmethod
    def workflow_run(sha, branch, conclusion="success", status="completed", number=1):
        return {"id": number, "head_sha": sha, "head_branch": branch, "status": status,
                "conclusion": conclusion, "event": "push", "html_url": "https://example.invalid/run"}

    def commit(self, subject, file="product"):
        with Path(file).open("a") as target:
            target.write(subject + "\n")
        rc.git("add", file)
        rc.git("commit", "-qm", subject)
        return rc.git("rev-parse", "HEAD")

    def refs(self):
        return rc.git("ls-remote", "origin")

    def outputs(self):
        return dict(line.split("=", 1) for line in (self.root / "output").read_text().splitlines())

    def prepare(self):
        rc.prepare(rc.Repository(), "v1.2.4", self.source)
        self.tag = "v1.2.4-rc.1"
        self.releases[self.tag] = {"isDraft": False, "isPrerelease": True}
        self.candidate_sha = rc.Repository().tags[self.tag]
        return self.candidate_sha


class LifecycleTests(GitFixture):
    def test_threshold_evaluates_without_changing_refs(self):
        before = self.refs()
        rc.evaluate(rc.Repository())
        self.assertEqual(self.outputs()["ready"], "true")
        self.assertEqual(self.outputs()["sha"], self.source)
        self.assertEqual(before, self.refs())

    def test_below_threshold_and_force_only_bypasses_cadence(self):
        with patch.dict(os.environ, {"RELEASE_MIN_PRS": "11"}):
            rc.evaluate(rc.Repository())
            self.assertEqual(self.outputs()["ready"], "false")
            with patch.dict(os.environ, {"FORCE_RELEASE": "true"}):
                rc.evaluate(rc.Repository())
                self.assertEqual(self.outputs()["ready"], "true")
                self.runs["ci.yml", self.source] = []
                with self.assertRaises(rc.Refusal):
                    rc.evaluate(rc.Repository())

    def test_dev_keeps_advancing_and_promotion_excludes_new_features(self):
        main_before = rc.Repository().branches["main"]
        sha = self.prepare()
        repo = rc.Repository()
        self.assertEqual(repo.branches["dev"], self.source)
        self.assertEqual(repo.branches["main"], main_before)
        self.assertEqual(rc.git("diff", "--name-only", self.source, sha), "VERSION")
        self.assertEqual(rc.git("show", f"{sha}:VERSION"), "v1.2.4")
        self.assertEqual(rc.git("branch", "--show-current"), "dev")
        self.assertEqual(rc.git("status", "--porcelain"), "")
        dev = self.commit("feat: later feature (#11)", "later-feature")
        rc.git("push", "origin", "dev")
        rc.evaluate(rc.Repository())
        self.assertEqual(self.outputs()["ready"], "false")
        rc.promote(rc.Repository(), self.tag, sha)
        repo = rc.Repository()
        self.assertEqual(repo.branches["main"], sha)
        self.assertEqual(repo.tags["v1.2.4"], sha)
        self.assertEqual(repo.branches["dev"], dev)
        self.assertNotIn("later-feature", rc.git("ls-tree", "--name-only", sha))

    def test_active_rc_holds_even_when_forced(self):
        self.prepare()
        before = self.refs()
        with patch.dict(os.environ, {"FORCE_RELEASE": "true"}):
            rc.evaluate(rc.Repository())
        self.assertEqual(self.outputs()["ready"], "false")
        self.assertEqual(before, self.refs())

    def test_repeated_prepare_reuses_existing_tag(self):
        sha = self.prepare()
        before = self.refs()
        rc.prepare(rc.Repository(), "v1.2.4", self.source)
        rc.prepare(rc.Repository(), "v1.2.4", sha)
        self.assertEqual(self.outputs()["tag"], self.tag)
        self.assertEqual(before, self.refs())

    def test_fixes_get_new_rc_and_old_candidate_cannot_be_promoted(self):
        old_sha = self.prepare()
        rc.git("checkout", "-qb", "release/v1.2.4", old_sha)
        fixed = self.commit("fix: release blocker", "fix")
        rc.git("push", "origin", "release/v1.2.4")
        with self.assertRaisesRegex(rc.Refusal, "not the HEAD"):
            rc.promote(rc.Repository(), self.tag, old_sha)
        rc.evaluate(rc.Repository(), "v1.2.4")
        self.assertEqual(self.outputs()["sha"], fixed)
        rc.prepare(rc.Repository(), "v1.2.4", fixed)
        repo = rc.Repository()
        self.assertEqual(repo.tags["v1.2.4-rc.2"], fixed)
        self.assertEqual(repo.tags[self.tag], old_sha)
        self.releases["v1.2.4-rc.2"] = {"isDraft": False, "isPrerelease": True}
        rc.promote(repo, "v1.2.4-rc.2", fixed)
        self.assertEqual(rc.Repository().branches["dev"], self.source)

    def test_promotion_requires_published_candidate_and_exact_green_runs(self):
        sha = self.prepare()
        before = self.refs()
        for state in ({"isDraft": True, "isPrerelease": True},
                      {"isDraft": False, "isPrerelease": False}):
            self.releases[self.tag] = state
            with self.assertRaises(rc.Refusal):
                rc.promote(rc.Repository(), self.tag, sha)
        self.releases[self.tag] = {"isDraft": False, "isPrerelease": True}
        for workflow, branch in (("ci.yml", "release/v1.2.4"), ("release.yml", self.tag)):
            for runs in ([], [self.workflow_run(sha, "unrelated")],
                         [self.workflow_run("f" * 40, branch)],
                         [self.workflow_run(sha, branch, "failure")],
                         [self.workflow_run(sha, branch, None, "in_progress")],
                         [self.workflow_run(sha, branch), self.workflow_run(sha, branch, "cancelled", number=2)]):
                with self.subTest(workflow=workflow, runs=runs):
                    self.runs[workflow, sha] = runs
                    with self.assertRaises(rc.Refusal):
                        rc.promote(rc.Repository(), self.tag, sha)
            self.runs.pop((workflow, sha))
        self.assertEqual(before, self.refs())

    def test_changed_approval_sha_and_main_divergence_refuse(self):
        sha = self.prepare()
        with self.assertRaisesRegex(rc.Refusal, "since the approval"):
            rc.promote(rc.Repository(), self.tag, "f" * 40)
        rc.git("checkout", "main")
        self.commit("hotfix on main")
        rc.git("push", "origin", "main")
        before = self.refs()
        with self.assertRaisesRegex(rc.Refusal, "main diverged"):
            rc.promote(rc.Repository(), self.tag, sha)
        self.assertEqual(before, self.refs())

    def test_atomic_promotion_does_not_partially_advance_main(self):
        sha = self.prepare()
        hook = self.remote / "hooks/pre-receive"
        hook.write_text("#!/bin/sh\nwhile read old new ref; do\n"
                        "  [ \"$ref\" != refs/tags/v1.2.4 ] || exit 1\ndone\n")
        hook.chmod(0o700)
        before = self.refs()
        with self.assertRaises(rc.Refusal):
            rc.promote(rc.Repository(), self.tag, sha)
        self.assertEqual(before, self.refs())
        hook.unlink()
        rc.promote(rc.Repository(), self.tag, sha)
        self.assertEqual(rc.Repository().branches["main"], sha)

    def test_merge_back_preserves_new_batch_and_is_required_before_next_cut(self):
        sha = self.prepare()
        for number in range(11, 21):
            self.commit(f"feat: next batch (#{number})")
        rc.git("push", "origin", "dev")
        rc.promote(rc.Repository(), self.tag, sha)
        # A tag without a successfully published stable release is not shipped.
        self.releases["v1.2.4"] = {"isDraft": True, "isPrerelease": False}
        with self.assertRaises(rc.Refusal):
            rc.evaluate(rc.Repository())
        self.releases["v1.2.4"] = {"isDraft": False, "isPrerelease": False}
        rc.evaluate(rc.Repository())
        self.assertEqual(self.outputs()["ready"], "false")
        rc.git("merge", "--no-ff", "-m", "Merge released v1.2.4", sha)
        rc.git("push", "origin", "dev")
        rc.evaluate(rc.Repository())
        self.assertEqual(self.outputs()["ready"], "true")
        self.assertEqual(self.outputs()["version"], "v1.2.5")

    def test_multiple_candidates_and_unknown_release_branches_refuse(self):
        self.prepare()
        for branch in ("release/v1.2.5", "release/unknown"):
            rc.git("push", "origin", f"{self.source}:refs/heads/{branch}")
            with self.assertRaises(rc.Refusal):
                rc.evaluate(rc.Repository())
            rc.git("push", "origin", f":refs/heads/{branch}")

    def test_local_only_tag_cannot_authorize_promotion(self):
        self.prepare()
        rc.git("tag", "v1.2.4-rc.99", self.candidate_sha)
        with self.assertRaisesRegex(rc.Refusal, "existing remote"):
            rc.promote(rc.Repository(), "v1.2.4-rc.99", self.candidate_sha)

    def test_build_requires_promotion_receipt_and_exact_checkout(self):
        sha = self.prepare()
        with self.assertRaisesRegex(rc.Refusal, "checkout"):
            rc.verify_build(rc.Repository(), self.tag)
        rc.git("checkout", "--detach", sha)
        self.releases[self.tag]["isDraft"] = True
        rc.verify_build(rc.Repository(), self.tag)
        self.releases[self.tag]["isDraft"] = False
        with self.assertRaisesRegex(rc.Refusal, "already published"):
            rc.verify_build(rc.Repository(), self.tag)
        rc.promote(rc.Repository(), self.tag, sha)
        rc.verify_build(rc.Repository(), "v1.2.4")
        self.assertEqual(self.outputs()["prerelease"], "false")
        self.assertEqual(self.outputs()["previous_tag"], "v1.2.3")

    def test_direct_stable_tag_without_receipt_is_rejected(self):
        sha = self.prepare()
        rc.git("tag", "v1.2.4", sha)
        rc.git("push", "origin", f"{sha}:refs/heads/main", "refs/tags/v1.2.4")
        rc.git("checkout", "--detach", sha)
        with self.assertRaisesRegex(rc.Refusal, "receipt"):
            rc.verify_build(rc.Repository(), "v1.2.4")

    def test_kill_switch_stops_mutations_even_with_force(self):
        sha = self.prepare()
        before = self.refs()
        with patch.dict(os.environ, {"AUTO_RELEASE_HOLD": "1", "FORCE_RELEASE": "true"}):
            for action in (lambda: rc.evaluate(rc.Repository()),
                           lambda: rc.prepare(rc.Repository(), "v1.2.4", sha),
                           lambda: rc.promote(rc.Repository(), self.tag, sha)):
                with self.assertRaisesRegex(rc.Refusal, "AUTO_RELEASE_HOLD"):
                    action()
        self.assertEqual(before, self.refs())

    def test_invalid_inputs_cannot_inject_commands(self):
        before = self.refs()
        for version in ("v1.2.4;touch owned", "../main", "v1.2.4\nready=true", "--help"):
            with self.assertRaises(rc.Refusal):
                rc.prepare(rc.Repository(), version, self.source)
            with self.assertRaises(rc.Refusal):
                rc.promote(rc.Repository(), version, self.source)
        self.assertEqual(before, self.refs())
        self.assertFalse(Path("owned").exists())

    def test_dirty_callers_index_and_worktree_are_never_packaged_or_changed(self):
        Path("VERSION").write_text("local-edit\n")
        Path("scratch").write_text("never package this\n")
        rc.git("add", "VERSION", "scratch")
        index = rc.git("write-tree")
        sha = self.prepare()
        self.assertEqual(rc.git("write-tree"), index)
        self.assertEqual(Path("VERSION").read_text(), "local-edit\n")
        self.assertNotIn("scratch", rc.git("ls-tree", "--name-only", sha))

    def test_dev_advance_between_evaluation_and_preparation_keeps_original_snapshot(self):
        later = self.commit("feat: after evaluation (#11)", "later")
        rc.git("push", "origin", "dev")
        self.runs["ci.yml", self.source] = [self.workflow_run(self.source, "dev")]
        sha = self.prepare()
        self.assertEqual(rc.git("show", "-s", "--format=%P", sha), self.source)
        self.assertEqual(rc.Repository().branches["dev"], later)
        self.assertNotIn("later", rc.git("ls-tree", "--name-only", sha))

    def test_initial_branch_and_tag_push_is_atomic_and_retryable(self):
        hook = self.remote / "hooks/pre-receive"
        hook.write_text("#!/bin/sh\nexit 1\n")
        hook.chmod(0o700)
        before = self.refs()
        with self.assertRaises(rc.Refusal):
            rc.prepare(rc.Repository(), "v1.2.4", self.source)
        self.assertEqual(before, self.refs())
        old_local_tag_sha = rc.git("rev-parse", "v1.2.4-rc.1^{commit}")
        hook.unlink()
        # A different wall clock must not cause a conflicting local-only tag.
        with patch.dict(os.environ, {"GIT_AUTHOR_DATE": "2000-01-01T00:00:00Z",
                                     "GIT_COMMITTER_DATE": "2000-01-01T00:00:00Z"}):
            self.prepare()
        self.assertEqual(self.candidate_sha, old_local_tag_sha)

    def test_main_advance_after_preview_is_rejected_atomically(self):
        sha = self.prepare()
        snapshot = rc.Repository()
        original = rc.tag_object
        def race(tag, target, message):
            ref = original(tag, target, message)
            new_main = rc.git("commit-tree", f"{snapshot.branches['main']}^{{tree}}",
                              "-p", snapshot.branches["main"], input="concurrent main update\n")
            rc.git("push", "origin", f"{new_main}:refs/heads/main")
            return ref
        with patch.object(rc, "tag_object", side_effect=race):
            with self.assertRaises(rc.Refusal):
                rc.promote(snapshot, self.tag, sha)
        self.assertNotIn("v1.2.4", rc.Repository().tags)
        self.assertNotEqual(rc.Repository().branches["main"], sha)

    def test_merge_back_pr_is_idempotent_and_not_a_dev_push(self):
        sha = self.prepare()
        rc.promote(rc.Repository(), self.tag, sha)
        self.releases["v1.2.4"] = {"isDraft": False, "isPrerelease": False}
        before = self.refs()
        repo = rc.Repository()
        with patch.object(rc, "run", wraps=rc.run) as runner:
            rc.sync_pr(repo, "v1.2.4")
            self.assertFalse(any(call.args[:3] == ("gh", "pr", "create") for call in runner.call_args_list))
        github = self.github
        def without_pr(*args):
            return [] if args[:2] == ("pr", "list") else github(*args)
        with patch.object(rc, "gh", side_effect=without_pr), patch.object(rc, "run", return_value="https://example.invalid/new") as runner:
            rc.sync_pr(repo, "v1.2.4")
            args = runner.call_args.args
            self.assertEqual(args[:7], ("gh", "pr", "create", "--base", "dev", "--head", "release/v1.2.4"))
            self.assertIn("NOT squash/rebase", args[-1])
        self.assertEqual(before, self.refs())

    def test_fetch_failure_never_uses_stale_remote_tracking_refs(self):
        repo = rc.Repository()
        self.assertIn("dev", repo.branches)
        rc.git("remote", "set-url", "origin", str(self.root / "missing.git"))
        with self.assertRaisesRegex(rc.Refusal, "fetch"):
            rc.Repository()


class EntryPointTests(unittest.TestCase):
    def test_confirmation_and_dispatch_are_exact_without_local_git_mutation(self):
        with tempfile.TemporaryDirectory(prefix="ori-rc-cli-") as temp:
            root = Path(temp)
            log = root / "calls"
            gh = root / "gh"
            gh.write_text('#!/bin/sh\nprintf "%s\\n" "$@" >> "$CALL_LOG"\n')
            gh.chmod(0o700)
            env = dict(os.environ, PATH=f"{root}{os.pathsep}{os.environ['PATH']}", CALL_LOG=str(log))
            def call(*args, script="release.sh"):
                return subprocess.run(["bash", str(ROOT / "scripts" / script), *args],
                                      stdin=subprocess.DEVNULL, capture_output=True, text=True, env=env)
            for args in (("promote", "v1.2.4-rc.1"), ("v1.2.4", "--yes"),
                         ("promote", "v1.2.4", "--yes"), ("candidate", "--skip-checks", "--yes"),
                         ("promote", "v1.2.4-rc.1;touch owned", "--yes")):
                self.assertNotEqual(call(*args).returncode, 0, args)
                self.assertFalse(log.exists())
            self.assertEqual(call("candidate", "--yes").returncode, 0)
            self.assertEqual(log.read_text().splitlines(), ["workflow", "run", "auto-release.yml", "--ref", "main",
                                                           "-f", "force=false", "-f", "candidate="])
            log.unlink()
            result = call("v1.2.4-rc.2", "--yes", script="create-release.sh")
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(log.read_text().splitlines(), ["workflow", "run", "promote-release.yml", "--ref", "main",
                                                           "-f", "rc_tag=v1.2.4-rc.2", "-f", "confirm_tested=true"])

    def test_workflow_contracts(self):
        auto = (ROOT / ".github/workflows/auto-release.yml").read_text()
        promote = (ROOT / ".github/workflows/promote-release.yml").read_text()
        release = (ROOT / ".github/workflows/release.yml").read_text()
        smoke = (ROOT / ".github/workflows/smoke-tests.yml").read_text()
        self.assertNotIn("HEAD:main", auto)
        self.assertNotIn("environment: release", auto)
        self.assertNotIn("schedule:", promote)
        self.assertIn("inputs.confirm_tested", promote)
        self.assertIn("environment: release", promote)
        self.assertIn('promote --rc "$RC_TAG" --sha "$SHA"', promote)
        self.assertIn("needs: [verify, smoke]", release)
        self.assertIn("draft: true", (ROOT / ".goreleaser.yaml").read_text())
        self.assertNotIn("softprops/action-gh-release", release)
        self.assertNotIn("continue-on-error", smoke)
        self.assertNotIn("--snapshot", smoke)
        self.assertIn("gh release download", smoke)
        self.assertIn("macos-15-intel", smoke)
        self.assertIn("macos-15", smoke)
        self.assertIn("head_branch", (ROOT / "scripts/release-candidate.py").read_text())
        self.assertIn("$ProductVersion", (ROOT / "build/windows/create-msi.ps1").read_text())


if __name__ == "__main__":
    unittest.main()
