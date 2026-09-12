#!/usr/bin/env python3
"""RC reports: pinned evidence, honest results, safe Markdown and retryable uploads."""

import copy
import importlib.util
from pathlib import Path
import unittest
from unittest.mock import patch

import rc_test_report as reports


spec = importlib.util.spec_from_file_location("lifecycle_fixtures", Path(__file__).with_name("release-candidate.test.py"))
fixtures = importlib.util.module_from_spec(spec)
spec.loader.exec_module(fixtures)
rc = fixtures.rc
ROOT = Path(__file__).resolve().parent.parent


class ReportTests(fixtures.GitFixture):
    def change(self, path, subject):
        Path(path).parent.mkdir(parents=True, exist_ok=True)
        self.source = self.commit(subject, path)
        rc.git("push", "origin", "dev")

    def render(self, tag=None, previous="v1.2.3", repository="owner/ori"):
        return reports.build_report(rc.Repository(), tag or self.tag, previous, repository, rc.git)

    def test_exact_metadata_default_hold_and_no_worktree_or_ref_changes(self):
        self.prepare()
        before, status = self.refs(), rc.git("status", "--porcelain")
        name, text = self.render()
        self.assertEqual(name, "rc-test-report-v1.2.4-rc.1.md")
        self.assertIn(f"Exact candidate commit: `{self.candidate_sha}`", text)
        self.assertIn("Previous stable: `v1.2.3`", text)
        self.assertIn("- Decision: **HOLD**.", text)
        self.assertIn("automation does not parse this report", text)
        self.assertEqual(sum(line.startswith("| B") and line.endswith("| NOT RUN |")
                             for line in text.splitlines()), len(reports.BASELINE))
        self.assertIn("No PR reference in subject", text)  # VERSION-only commit is not lost.
        self.assertIn("[PR #10]", text)
        self.assertIn("Exact entry/navigation and setup: TODO", text)
        self.assertEqual(before, self.refs())
        self.assertEqual(status, rc.git("status", "--porcelain"))

    def test_moving_dev_dirty_checkout_and_later_prs_do_not_change_report(self):
        self.prepare()
        first = self.render()
        self.change("internal/database/later.go", "feat: must not enter candidate (#999)")
        Path("dirty-local").write_text("not candidate evidence")
        self.assertEqual(first, self.render())
        self.assertNotIn("#999", first[1])
        self.assertNotIn("later.go", first[1])
        self.assertNotIn("dirty-local", first[1])

    def test_risk_selection_uses_real_changed_paths_not_title(self):
        self.change("internal/database/migration999.go", "chore: harmless wording (#91)")
        self.change("internal/database/migration999_test.go", "test: persistence coverage (#92)")
        self.prepare()
        text = self.render()[1]
        self.assertIn("### R-DATA: Data, migrations and settings", text)
        self.assertIn("migration999.go", text)
        self.assertIn("migration999_test.go", text)
        self.assertIn("Changed test/guide references (not execution evidence)", text)
        self.assertIn("observable expected result", text)

    def test_test_only_change_does_not_claim_runtime_risk_or_completed_coverage(self):
        self.change("internal/database/migration999_test.go", "fix: critical auth migration (#93)")
        self.prepare()
        text = self.render()[1]
        self.assertNotIn("### R-DATA:", text)
        self.assertNotIn("### R-PERM:", text)
        self.assertIn("reviewer must classify", text)
        self.assertIn("Result / evidence / linked bug: NOT RUN", text)

    def test_rc2_contains_pinned_delta_and_resets_results(self):
        old_sha = self.prepare()
        old_text = self.render()[1]
        rc.git("checkout", "-qb", "release/v1.2.4", old_sha)
        Path("internal/workspace").mkdir(parents=True)
        fixed = self.commit("fix: specific release blocker (#94)", "internal/workspace/fix.go")
        rc.git("push", "origin", "release/v1.2.4")
        rc.prepare(rc.Repository(), "v1.2.4", fixed)
        name, text = self.render("v1.2.4-rc.2")
        self.assertEqual(name, "rc-test-report-v1.2.4-rc.2.md")
        self.assertIn(f"compare/{old_sha}...{fixed}", text)
        self.assertIn("Previous RC: `v1.2.4-rc.1`", text)
        self.assertIn("Previous RC PASS results and approval never carry over", text)
        self.assertIn("previous-STABLE fixture", text)
        self.assertIn("Decision: **HOLD**", text)
        self.assertNotIn("fix.go", old_text)
        self.assertIn("fix.go", text)
        with self.assertRaises(rc.Refusal):
            self.render("v1.2.4-rc.1")

    def test_merge_diff_includes_changes_from_first_parent(self):
        self.prepare()
        rc.git("checkout", "-qb", "fix", self.candidate_sha)
        Path("internal/auth").mkdir(parents=True)
        self.commit("fix permission boundary", "internal/auth/fix.go")
        rc.git("checkout", "-qb", "release/v1.2.4", self.candidate_sha)
        rc.git("merge", "--no-ff", "-m", "Merge pull request #95 from owner/fix", "fix")
        merged = rc.git("rev-parse", "HEAD")
        rc.git("push", "origin", "release/v1.2.4")
        rc.prepare(rc.Repository(), "v1.2.4", merged)
        text = self.render("v1.2.4-rc.2")[1]
        self.assertIn("[PR #95]", text)
        self.assertIn("internal/auth/fix.go", text)
        self.assertIn("### R-PERM:", text)
        # One merged entry, not a duplicate for its individual side-branch commit.
        self.assertNotIn("C12: <code>fix permission boundary", text)

    def test_metadata_and_filenames_are_inert_and_links_are_encoded(self):
        self.change("internal/web/<script>|@someone\nweird.js", "feat: <img src=x> | @someone [x] `thing` (#96)")
        self.prepare()
        text = self.render()[1]
        self.assertIn("&lt;img src=x&gt;", text)
        self.assertIn("&#124;", text)
        self.assertIn("&#64;someone", text)
        self.assertIn("&#92;u000a", text)
        self.assertNotIn("<script>", text)
        self.assertNotIn("<img src=x>", text)
        self.assertNotIn("@someone", text)

    def test_deleted_and_renamed_paths_remain_in_retest_evidence(self):
        self.change("internal/database/remove.go", "feat: candidate schema (#97)")
        self.prepare()
        rc.git("checkout", "-qb", "release/v1.2.4", self.candidate_sha)
        rc.git("rm", "internal/database/remove.go")
        rc.git("mv", "product", "renamed-product")
        rc.git("commit", "-qm", "fix: remove migration and rename file (#98)")
        fixed = rc.git("rev-parse", "HEAD")
        rc.git("push", "origin", "release/v1.2.4")
        rc.prepare(rc.Repository(), "v1.2.4", fixed)
        delta = self.render("v1.2.4-rc.2")[1].split("## Retest scope")[1]
        for path in ("internal/database/remove.go", "product", "renamed-product"):
            self.assertIn(reports.inline(path), delta)

    def test_invalid_or_unbounded_evidence_fails_closed(self):
        self.prepare()
        for baseline in ("v1.2.2", "dev", "v1.2.3\nready=true"):
            with self.assertRaises(ValueError):
                self.render(previous=baseline)
        for repository in ("owner/repo/extra", "../repo", "owner/repo\nBAD", "https://evil.invalid"):
            with self.assertRaises(ValueError):
                self.render(repository=repository)
        with patch.object(reports, "MAX_COMMITS", 1):
            with self.assertRaisesRegex(ValueError, "Too many"):
                self.render()
        with patch.object(reports, "MAX_PATHS", 1):
            with self.assertRaisesRegex(ValueError, "too large"):
                self.render()

    def release_transport(self, fail_after_upload=False):
        self.remote_release = {"isDraft": True, "isPrerelease": True, "body": "Original release notes", "assets": []}
        self.remote_assets = {}
        self.remote_writes = []
        original = rc.run
        def github(*args):
            self.assertEqual(args[:2], ("release", "view"))
            return copy.deepcopy(self.remote_release)
        def transport(*args, **kwargs):
            if args[0] != "gh":
                return original(*args, **kwargs)
            action = args[2]
            if action == "upload":
                self.assertNotIn("--clobber", args)
                file = Path(args[4])
                self.remote_assets[file.name] = file.read_bytes()
                self.remote_release["assets"].append({"name": file.name})
                self.remote_writes.append("upload")
                # Simulate a concurrent human edit outside the managed block.
                self.remote_release["body"] += "\nHuman note preserved."
                if fail_after_upload:
                    raise rc.Refusal("simulated lost response after upload")
            elif action == "download":
                file = Path(args[args.index("--dir") + 1]) / args[args.index("--pattern") + 1]
                file.write_bytes(self.remote_assets[file.name])
            elif action == "edit":
                self.assertEqual(args[4], "--notes-file")
                self.remote_release["body"] = Path(args[5]).read_text()
                self.remote_writes.append("notes")
            else:
                self.fail(f"unexpected outward-facing action: {args}")
            return ""
        self.enterContext(patch.object(rc, "gh", side_effect=github))
        self.enterContext(patch.object(rc, "run", side_effect=transport))

    def attach(self):
        rc.attach_test_report(rc.Repository(), self.tag, "v1.2.3", "owner/ori")

    def test_attach_is_idempotent_preserves_notes_and_never_publishes(self):
        self.prepare()
        self.release_transport()
        self.attach()
        self.assertEqual(self.remote_writes, ["upload", "notes"])
        self.assertIn("Original release notes", self.remote_release["body"])
        self.assertIn("Human note preserved.", self.remote_release["body"])
        self.assertIn("/rc-test-report-v1.2.4-rc.1.md", self.remote_release["body"])
        self.attach()
        self.assertEqual(self.remote_writes, ["upload", "notes"])
        self.assertTrue(self.remote_release["isDraft"])
        # A published retry is read-only when the exact evidence already exists.
        self.remote_release["isDraft"] = False
        self.attach()
        self.assertEqual(self.remote_writes, ["upload", "notes"])

    def test_partial_upload_retry_reuses_card_and_finishes_notes(self):
        self.prepare()
        self.release_transport(fail_after_upload=True)
        with self.assertRaisesRegex(rc.Refusal, "lost response"):
            self.attach()
        self.attach()
        self.assertEqual(self.remote_writes, ["upload", "notes"])

    def test_modified_results_or_notes_are_never_overwritten(self):
        self.prepare()
        self.release_transport()
        self.attach()
        name = f"rc-test-report-{self.tag}.md"
        self.remote_assets[name] += b"\nManual results: APPROVE\n"
        with self.assertRaisesRegex(rc.Refusal, "refusing to overwrite"):
            self.attach()
        self.assertEqual(self.remote_writes, ["upload", "notes"])
        self.remote_assets[name] = self.render()[1].encode()
        self.remote_release["body"] = self.remote_release["body"].replace("Development on dev continues", "Human edit: dev continues")
        with self.assertRaisesRegex(ValueError, "was edited"):
            self.attach()
        self.assertEqual(self.remote_writes, ["upload", "notes"])

    def test_hold_blocks_report_upload(self):
        self.prepare()
        self.release_transport()
        with patch.dict(fixtures.os.environ, {"AUTO_RELEASE_HOLD": "true"}):
            with self.assertRaisesRegex(rc.Refusal, "AUTO_RELEASE_HOLD"):
                self.attach()
        self.assertEqual(self.remote_writes, [])

    def test_published_rc_without_card_is_not_rewritten(self):
        self.prepare()
        self.release_transport()
        self.remote_release["isDraft"] = False
        with self.assertRaisesRegex(rc.Refusal, "Published RC"):
            self.attach()
        self.assertEqual(self.remote_writes, [])


class NotesAndWorkflowTests(unittest.TestCase):
    def test_metadata_cannot_create_markdown_links_or_images(self):
        import html
        value = '![image](https://example.invalid) [click](javascript:bad) ` **@all**'
        rendered = reports.inline(value)
        self.assertNotIn('![', rendered)
        self.assertNotIn('[click]', rendered)
        self.assertNotIn('`', rendered)
        self.assertNotIn('@all', rendered)
        self.assertEqual(html.unescape(rendered[6:-7]), value)

    def test_notes_block_is_idempotent_and_ambiguous_content_refuses(self):
        block = reports.report_links("owner/repo", "v1.2.4-rc.1", "a" * 40, "rc-test-report-v1.2.4-rc.1.md")
        body = reports.add_report_links("User's release notes", block)
        self.assertEqual(reports.add_report_links(body, block), body)
        for malformed in (body + block, "<!-- ori-rc-test-report -->", "<!-- /ori-rc-test-report -->",
                          body.replace("NOT RUN", "PASS")):
            with self.assertRaises(ValueError):
                reports.add_report_links(malformed, block)

    def test_workflow_attaches_before_publication_and_protocol_is_linked(self):
        workflow = (ROOT / ".github/workflows/release.yml").read_text()
        self.assertLess(workflow.index("attach-test-report --rc"), workflow.index("gh release edit \"$TAG\" --draft=false"))
        self.assertIn("if: needs.verify.outputs.prerelease == 'true'", workflow)
        self.assertIn('REPOSITORY: ${{ github.repository }}', workflow)
        self.assertIn('PREVIOUS_TAG: ${{ needs.verify.outputs.previous_tag }}', workflow)
        self.assertIn("RC_TEST_PROTOCOL.md", (ROOT / "docs/RELEASE_CHECKLIST.md").read_text())
        self.assertIn("completed and reviewed the test report", (ROOT / ".github/workflows/promote-release.yml").read_text())
        protocol = (ROOT / "docs/RC_TEST_PROTOCOL.md").read_text()
        for text in ("Fresh", "Upgrade", "NOT RUN", "HOLD", "ORI_DATA_DIR", "wt demo"):
            self.assertIn(text, protocol)


if __name__ == "__main__":
    unittest.main()
