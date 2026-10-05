#!/usr/bin/env python3
"""Ori adapter behavior without a tool clone, real builds, agents or secrets."""
import json
import os
from pathlib import Path
import shutil
import stat
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parent.parent


class ProjectTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="ori-project-adapter-")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name).resolve()
        self.dev = self.root / "ori-agent-dev"
        self.feature = self.root / "feature with spaces"
        self.home = self.root / "home"
        self.home.mkdir()
        self.env = dict(os.environ, HOME=str(self.home), TMPDIR=str(self.root),
                        GIT_CONFIG_NOSYSTEM="1", GIT_CONFIG_GLOBAL=os.devnull,
                        GIT_AUTHOR_NAME="Fixture", GIT_AUTHOR_EMAIL="fixture@example.invalid",
                        GIT_COMMITTER_NAME="Fixture", GIT_COMMITTER_EMAIL="fixture@example.invalid",
                        ADAPTER_RECORD=str(self.root / "server.json"), ADAPTER_EVENTS=str(self.root / "events"))
        for key in list(self.env):
            if key.startswith(("HERDR_DEVFLOW_", "ORI_DEVTOOLS_", "ORI_DEMO_", "ORI_KEEP_DEMO_")):
                self.env.pop(key)
        for key in ("GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "CODEX_HOME", "NO_BROWSER"):
            self.env.pop(key, None)
        self.command(["git", "init", "-q", "-b", "dev", self.dev])
        (self.dev / "go.mod").write_text("module github.com/johnjallday/ori-agent\n")
        (self.dev / ".gitignore").write_text("/.env\n/.claude/\n/bin/\n")
        (self.dev / "scripts/lib").mkdir(parents=True)
        for name in ("devtools-project.sh", "lib/devtools-selector.sh", "lib/devtools-project.zsh"):
            shutil.copy(ROOT / "scripts" / name, self.dev / "scripts" / name)
        (self.dev / "scripts/ci-local.sh").write_text("# fixture target CI marker\n")
        (self.dev / "scripts/build-folder-picker.sh").write_text(
            'printf "picker:%s\\n" "$PWD" >> "$ADAPTER_EVENTS"\nexit 33\n')
        (self.dev / "package.json").write_text('{}\n')
        self.command(["git", "add", "."], self.dev)
        self.command(["git", "commit", "-qm", "fixture"], self.dev)
        self.command(["git", "worktree", "add", "-qb", "feature/demo", self.feature], self.dev)
        fake = self.root / "fake bin"
        fake.mkdir()
        self.executable(fake / "npm", '#!/bin/sh\nprintf "npm:%s:%s\\n" "$PWD" "$*" >> "$ADAPTER_EVENTS"\nexit 31\n')
        self.executable(fake / "make", '#!/bin/sh\nprintf "make:%s:%s\\n" "$PWD" "$*" >> "$ADAPTER_EVENTS"\nexit "${MAKE_EXIT:-0}"\n')
        self.executable(fake / "go", '''#!/bin/sh
printf 'go:%s:%s\\n' "$PWD" "$*" >> "$ADAPTER_EVENTS"
[ "${BUILD_FAIL:-0}" = 0 ] || exit 12
mkdir -p bin
cp "$FAKE_SERVER" bin/ori-agent
''')
        server = self.root / "fake-server"
        self.executable(server, '''#!/usr/bin/env python3
import json, os
from pathlib import Path
Path(os.environ['ADAPTER_RECORD']).write_text(json.dumps(dict(cwd=os.getcwd(),
    HOME=os.environ['HOME'], ORI_DATA_DIR=os.environ['ORI_DATA_DIR'], PORT=os.environ['PORT'],
    NO_BROWSER=os.environ.get('NO_BROWSER'), ORI_NO_DESKTOP_OPEN=os.environ.get('ORI_NO_DESKTOP_OPEN'),
    CODEX_HOME=os.environ.get('CODEX_HOME'))))
raise SystemExit(int(os.environ.get('SERVER_EXIT', '0')))
''')
        self.env.update(PATH=str(fake) + os.pathsep + self.env["PATH"], FAKE_SERVER=str(server))

    def executable(self, path, contents):
        path.write_text(contents)
        path.chmod(0o700)

    def command(self, args, cwd=None, env=None, status=0):
        result = subprocess.run([str(arg) for arg in args], cwd=cwd or self.root,
                                env=dict(self.env, **(env or {})), input="", text=True,
                                capture_output=True, timeout=30)
        self.assertEqual(result.returncode, status, f"{args}\n{result.stdout}\n{result.stderr}")
        return result

    def adapter(self, *args, env=None, status=0):
        return self.command(["zsh", "-f", self.feature / "scripts/devtools-project.sh", "v1", *args],
                            env=env, status=status)

    def test_provision_preserves_modes_deny_floor_nonoverwrite_and_best_effort(self):
        secret = self.dev / ".env"
        secret.write_text("INVENTED_TEST_SECRET=not-real\n")
        secret.chmod(0o600)
        self.adapter("provision", self.dev, "acceptEdits")
        target_secret = self.feature / ".env"
        self.assertEqual(target_secret.read_bytes(), secret.read_bytes())
        self.assertEqual(stat.S_IMODE(target_secret.stat().st_mode), 0o600)
        profile = self.feature / ".claude/settings.local.json"
        settings = json.loads(profile.read_text())["permissions"]
        self.assertEqual(settings["defaultMode"], "acceptEdits")
        self.assertEqual(settings["deny"], ["Bash(rm -rf:*)", "Bash(git push --force:*)",
                         "Bash(git push -f:*)", "Bash(./scripts/release.sh:*)", "Read(**/.env)", "Read(**/*secret*)"])
        original_profile = profile.read_bytes()
        target_secret.write_text("keep-existing\n")
        self.adapter("provision", self.dev, "bypassPermissions")
        self.assertEqual(profile.read_bytes(), original_profile)
        self.assertEqual(target_secret.read_text(), "keep-existing\n")
        events = (self.root / "events").read_text()
        self.assertIn("picker:" + str(self.feature), events)
        self.assertIn("npm:" + str(self.feature) + ":install", events)
        # Both helpers failed; legacy provisioning is still best-effort.
        self.assertNotIn("go:", events)

    def test_provision_copy_failure_warns_without_overwriting_and_foreign_repo_refuses(self):
        (self.dev / ".env").write_text("INVENTED_ONLY=1\n")
        self.executable(self.root / "fake bin/cp", "#!/bin/sh\nexit 9\n")
        result = self.adapter("provision", self.dev, "acceptEdits")
        self.assertIn("Warning: could not copy .env", result.stdout)
        self.assertFalse((self.feature / ".env").exists())
        foreign = self.root / "foreign"
        self.command(["git", "init", "-q", foreign])
        before = (self.root / "events").read_bytes()
        self.adapter("provision", foreign, "acceptEdits", status=2)
        self.adapter("provision", self.dev, 'bad"mode', status=2)
        self.assertEqual((self.root / "events").read_bytes(), before)

    def test_demo_default_browser_opt_in_codex_opt_out_and_exit_cleanup(self):
        codex = self.home / ".codex"
        codex.mkdir()
        for extra, port in (({}, "8931"), ({"ORI_DEMO_OPEN": "1", "NO_BROWSER": "1"}, "9111"),
                            ({"ORI_DEMO_NO_CODEX": "1", "CODEX_HOME": str(codex)}, "9112")):
            self.adapter("demo", port, env=dict(extra, SERVER_EXIT="23"), status=23)
            record = json.loads((self.root / "server.json").read_text())
            sandbox = Path(record["HOME"])
            self.assertEqual(record["cwd"], str(sandbox))
            self.assertEqual(record["ORI_DATA_DIR"], str(sandbox))
            self.assertEqual(record["PORT"], port)
            self.assertEqual(record["ORI_NO_DESKTOP_OPEN"], "1")
            self.assertEqual(record["NO_BROWSER"], None if extra.get("ORI_DEMO_OPEN") else "1")
            self.assertEqual(record["CODEX_HOME"], None if extra.get("ORI_DEMO_NO_CODEX") else str(codex))
            self.assertFalse(sandbox.exists(), "demo did not clean its own sandbox")
        self.assertTrue(codex.is_dir())
        self.assertIn(f"go:{self.feature}:build -o bin/ori-agent ./cmd/server", (self.root / "events").read_text())

    def test_demo_build_failure_and_keep_sandbox(self):
        self.adapter("demo", env={"BUILD_FAIL": "1"}, status=1)
        self.assertFalse((self.root / "server.json").exists())
        self.assertEqual(list(self.root.glob("ori-demo.*")), [])
        self.adapter("demo", env={"ORI_KEEP_DEMO_SANDBOX": "1"})
        record = json.loads((self.root / "server.json").read_text())
        self.assertTrue(Path(record["HOME"]).is_dir())
        self.assertEqual(record["PORT"], "8931")

    def test_pre_pr_uses_target_and_propagates_failure_or_missing_gate(self):
        self.adapter("pre-pr", env={"MAKE_EXIT": "17"}, status=17)
        events = (self.root / "events").read_bytes()
        self.assertIn(f"make:{self.feature}:ci-local".encode(), events)
        (self.feature / "scripts/ci-local.sh").unlink()
        self.adapter("pre-pr", status=2)
        self.assertEqual((self.root / "events").read_bytes(), events)
        self.adapter("pre-pr", "unexpected", status=2)


if __name__ == "__main__":
    unittest.main(verbosity=2)
