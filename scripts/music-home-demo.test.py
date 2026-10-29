#!/usr/bin/env python3
"""Fake-only exact-ref export contracts; never start Ori or install a plugin."""

import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest


class MusicCandidateRefExport(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="ori-music-ref-test.")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.ori = self.root / "ori"
        self.music = self.root / "music"
        self.reaper = self.root / "reaper"
        self.shims = self.root / "shims"
        self.shims.mkdir()
        (self.root / "tmp").mkdir()
        (self.ori / "scripts").mkdir(parents=True)
        for name in ("music-home-demo.sh", "reaper-demo.sh"):
            shutil.copyfile(Path(__file__).with_name(name), self.ori / "scripts" / name)
        self.init_repo(self.ori)
        (self.music / ".ori-plugin").mkdir(parents=True)
        (self.music / "scripts").mkdir()
        (self.music / "skills/music-project-management").mkdir(parents=True)
        self.script(self.music / "scripts/validate-package.py", '''#!/usr/bin/env python3
import os
from pathlib import Path
import sys
root = Path(sys.argv[1])
assert root.resolve() != Path(os.environ['FIXTURE_MUSIC_SOURCE']).resolve()
print('Validated disposable fixture ' + (root / 'skills/music-project-management/SKILL.md').read_text().strip())
''')
        self.write_music("0.2.0", "old skill")
        self.init_repo(self.music)
        self.old = self.git(self.music, "rev-parse", "HEAD")
        self.write_music("0.3.0", "reviewed skill")
        self.commit(self.music)
        self.reviewed = self.git(self.music, "rev-parse", "HEAD")
        self.git(self.music, "tag", "-a", "reviewed-candidate", "-m", "fixture annotated tag")
        self.git(self.music, "checkout", "--detach", "-q", self.old)
        (self.reaper / ".ori-plugin").mkdir(parents=True)
        (self.reaper / "scripts").mkdir()
        (self.reaper / ".ori-plugin/plugin.json").write_text(json.dumps({"services": [{"artifacts": [{}]}]}))
        self.script(self.reaper / "scripts/with-local-artifact.sh", '''#!/bin/sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
[ "$root" != "$FIXTURE_REAPER_SOURCE" ] || exit 91
mkdir -p "$root/artifacts"
printf '#!/bin/sh\nprintf "fixture-version\\n"\n' > "$root/artifacts/reaper-plugin-darwin-arm64"
chmod 0755 "$root/artifacts/reaper-plugin-darwin-arm64"
"$@"
''')
        self.script(self.reaper / "scripts/verify-artifact.sh", "#!/bin/sh\nexit 0\n")
        self.init_repo(self.reaper)
        self.reaper_head = self.git(self.reaper, "rev-parse", "HEAD")
        # Stop at the explicit build boundary, after export/validation and
        # before any application server or plugin-install API can run.
        self.script(self.shims / "go", '#!/bin/sh\nprintf reached > "$FIXTURE_BUILD_BARRIER"\nexit 73\n')
        self.script(self.shims / "curl", "#!/bin/sh\nexit 7\n")

    def script(self, path, content):
        path.write_text(content)
        path.chmod(0o755)

    def git(self, repo, *args):
        return subprocess.check_output(["git", "-C", str(repo), *args], text=True).strip()

    def init_repo(self, repo):
        self.git(repo, "init", "-q")
        self.git(repo, "config", "user.name", "Fixture")
        self.git(repo, "config", "user.email", "fixture@example.invalid")
        self.commit(repo)

    def commit(self, repo):
        self.git(repo, "add", ".")
        self.git(repo, "commit", "-qm", "inert fixture")

    def write_music(self, version, skill):
        (self.music / ".ori-plugin/plugin.json").write_text(json.dumps({"version": version}))
        (self.music / "skills/music-project-management/SKILL.md").write_text(skill + "\n")

    def run_demo(self, paired, ref):
        sandbox = self.root / ("paired" if paired else "standalone")
        barrier = self.root / "build-barrier"
        env = dict(os.environ, HOME=str(self.root), TMPDIR=str(self.root / "tmp"),
                   PATH=str(self.shims) + os.pathsep + os.environ["PATH"],
                   FIXTURE_MUSIC_SOURCE=str(self.music), FIXTURE_REAPER_SOURCE=str(self.reaper),
                   FIXTURE_BUILD_BARRIER=str(barrier))
        for key in ("ORI_MUSIC_PLUGIN_SOURCE", "ORI_REAPER_PLUGIN_SOURCE", "ORI_MUSIC_REAPER_INSTALL_ORDER"):
            env.pop(key, None)
        if paired:
            args = ["reaper-demo.sh", "serve", "--reaper-source", str(self.reaper), "--music-source", str(self.music)]
            ref_flag = "--music-ref"
        else:
            args = ["music-home-demo.sh", "serve", "--source", str(self.music)]
            ref_flag = "--source-ref"
        if ref is not None:
            args += [ref_flag, ref]
        args += ["--sandbox", str(sandbox)]
        result = subprocess.run(["bash", str(self.ori / "scripts" / args[0]), *args[1:]],
                                env=env, text=True, capture_output=True, timeout=30)
        return result, sandbox, barrier

    def assert_unchanged(self):
        self.assertEqual(self.git(self.music, "rev-parse", "HEAD"), self.old)
        self.assertEqual(self.git(self.music, "status", "--porcelain", "--untracked-files=all"), "")
        self.assertEqual(self.git(self.reaper, "rev-parse", "HEAD"), self.reaper_head)
        self.assertEqual(self.git(self.reaper, "status", "--porcelain", "--untracked-files=all"), "")

    def test_exact_tag_export_and_default_head_preserve_both_sources(self):
        for paired in (False, True):
            for ref, expected, version in (("reviewed-candidate", self.reviewed, "0.3.0"), (None, self.old, "0.2.0")):
                with self.subTest(paired=paired, ref=ref):
                    result, sandbox, barrier = self.run_demo(paired, ref)
                    self.assertEqual(result.returncode, 73, result.stdout + result.stderr)
                    self.assertTrue(barrier.is_file())
                    evidence = sandbox / "evidence"
                    prefix = "music-candidate" if paired else "candidate"
                    self.assertEqual((evidence / (prefix + "-revision.txt")).read_text().strip(), expected)
                    self.assertEqual((evidence / (prefix + "-tree.txt")).read_text().strip(), self.git(self.music, "rev-parse", expected + "^{tree}"))
                    export = sandbox / "plugin-source/music-project-management"
                    self.assertEqual(json.loads((export / ".ori-plugin/plugin.json").read_text())["version"], version)
                    self.assert_unchanged()

    def test_unknown_or_option_like_ref_fails_before_build_or_sandbox(self):
        for paired, ref in ((False, "missing-commit"), (True, "--not-an-option")):
            with self.subTest(paired=paired):
                result, sandbox, barrier = self.run_demo(paired, ref)
                self.assertEqual(result.returncode, 2, result.stdout + result.stderr)
                self.assertIn("does not resolve", result.stderr)
                self.assertFalse(barrier.exists())
                self.assertFalse(sandbox.exists())
                self.assert_unchanged()

    def test_explicit_ref_does_not_bypass_dirty_source_refusal(self):
        (self.music / "untracked.txt").write_text("uncommitted source remains untouched\n")
        for paired in (False, True):
            result, sandbox, barrier = self.run_demo(paired, "reviewed-candidate")
            self.assertEqual(result.returncode, 2, result.stdout + result.stderr)
            self.assertIn("must be clean", result.stderr)
            self.assertFalse(barrier.exists())
            self.assertFalse(sandbox.exists())
        self.assertEqual((self.music / "untracked.txt").read_text(), "uncommitted source remains untouched\n")
        self.assertEqual(self.git(self.music, "rev-parse", "HEAD"), self.old)


if __name__ == "__main__":
    unittest.main()
