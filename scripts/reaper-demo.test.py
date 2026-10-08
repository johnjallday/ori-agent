#!/usr/bin/env python3
"""Fake-only candidate staging: never build or edit a real companion checkout."""

import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest


class ReaperDemoSourcePreservation(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="ori-reaper-stage-test.")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.ori = self.root / "ori"
        self.source = self.root / "candidate"
        self.log = self.root / "build-root.txt"
        self.tmp = self.root / "tmp"
        self.tmp.mkdir()
        self.ori.mkdir()
        (self.ori / "scripts").mkdir()
        shutil.copyfile(Path(__file__).with_name("reaper-demo.sh"), self.ori / "scripts/reaper-demo.sh")
        (self.source / "scripts").mkdir(parents=True)
        (self.source / ".ori-plugin").mkdir()
        (self.source / ".ori-plugin/plugin.json").write_text('{"fixture":true}\n')
        self.write_script("with-local-artifact.sh", r'''#!/bin/sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
# A source-checkout invocation fails rather than relying on restoration.
[ "$root" != "$FIXTURE_SOURCE" ] || exit 91
printf '%s' "$root" > "$FIXTURE_BUILD_LOG"
# Deliberately mutate the export, proving even a mutating build helper is safe.
printf '\nfixture local artifact\n' >> "$root/.ori-plugin/plugin.json"
[ "${FIXTURE_FAIL_BUILD:-0}" != 1 ] || exit 55
mkdir -p "$root/artifacts"
printf '#!/bin/sh\nprintf "fixture-version\\n"\n' > "$root/artifacts/reaper-plugin-darwin-arm64"
chmod 0755 "$root/artifacts/reaper-plugin-darwin-arm64"
"$@"
''')
        self.write_script("verify-artifact.sh", r'''#!/bin/sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
[ "$root" != "$FIXTURE_SOURCE" ] || exit 92
[ -x "$root/artifacts/reaper-plugin-darwin-arm64" ]
''')
        for repo in (self.ori, self.source):
            self.git(repo, "init", "-q")
            self.git(repo, "add", ".")
            self.git(repo, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid",
                     "commit", "-qm", "inert fixture")
        self.before = (self.source / ".ori-plugin/plugin.json").read_bytes()
        self.revision = self.git(self.source, "rev-parse", "HEAD")

    def write_script(self, name, content):
        path = self.source / "scripts" / name
        path.write_text(content)
        path.chmod(0o755)

    def git(self, repo, *args):
        return subprocess.check_output(["git", "-C", str(repo), *args], text=True).strip()

    def run_demo(self, fail=False):
        env = dict(os.environ, HOME=str(self.root), TMPDIR=str(self.tmp),
                   FIXTURE_SOURCE=str(self.source), FIXTURE_BUILD_LOG=str(self.log),
                   FIXTURE_FAIL_BUILD="1" if fail else "0")
        # Do not inherit another candidate or paired installation selection.
        for key in ("ORI_REAPER_PLUGIN_SOURCE", "ORI_MUSIC_PLUGIN_SOURCE", "ORI_MUSIC_REAPER_INSTALL_ORDER"):
            env.pop(key, None)
        return subprocess.run(["bash", str(self.ori / "scripts/reaper-demo.sh"), "artifact",
                               "--reaper-source", str(self.source)], env=env, text=True,
                              capture_output=True, timeout=30)

    def assert_source_preserved(self):
        self.assertEqual((self.source / ".ori-plugin/plugin.json").read_bytes(), self.before)
        self.assertEqual(self.git(self.source, "rev-parse", "HEAD"), self.revision)
        self.assertEqual(self.git(self.source, "status", "--porcelain", "--untracked-files=all"), "")
        self.assertFalse((self.source / "artifacts").exists())
        exported = Path(self.log.read_text())
        self.assertNotEqual(exported, self.source)
        self.assertTrue(exported.name.startswith("ori-reaper-build."))
        self.assertFalse(exported.exists(), "disposable build export leaked")

    def test_artifact_build_uses_export_and_keeps_candidate_unchanged(self):
        result = self.run_demo()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("REAPER_PLUGIN_VERSION=fixture-version", result.stdout)
        self.assertTrue((self.ori / "reaper-plugin-darwin-arm64").is_file())
        self.assert_source_preserved()

    def test_failed_build_preserves_candidate_and_removes_export(self):
        result = self.run_demo(fail=True)
        self.assertEqual(result.returncode, 55, result.stdout + result.stderr)
        self.assertFalse((self.ori / "reaper-plugin-darwin-arm64").exists())
        self.assert_source_preserved()


if __name__ == "__main__":
    unittest.main()
