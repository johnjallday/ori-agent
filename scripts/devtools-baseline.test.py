#!/usr/bin/env python3
"""Pre-extraction contracts; no worktree creation, live agents or installations.

Run from any directory with Python 3, Go, bash and zsh installed. Go builds only
into the temporary fixture. This characterizes the legacy path, NOT candidate
compatibility. Keep it separate from the future paired integration harness.
"""

import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parent.parent


class LegacyBoundaryTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.temp = tempfile.TemporaryDirectory(prefix="ori-devtools-baseline-")
        cls.addClassCleanup(cls.temp.cleanup)
        cls.root = Path(cls.temp.name).resolve()
        cls.home = cls.root / "home"
        cls.home.mkdir()
        # Resolve caches before isolating HOME; do not download a second module
        # cache or silently depend on an ambient Go workspace during the build.
        go_env = dict(os.environ, GOWORK="off", GOFLAGS="-mod=readonly")
        caches = json.loads(subprocess.check_output(
            ["go", "env", "-json", "GOCACHE", "GOPATH"], env=go_env, cwd=ROOT))
        cls.env = dict(go_env, **caches, HOME=str(cls.home),
                       GIT_CONFIG_NOSYSTEM="1", GIT_CONFIG_GLOBAL=os.devnull,
                       HERDR_DEVFLOW_HOME=str(cls.root / "runtime"))
        for key in list(cls.env):
            if key.startswith("HERDR_DEVFLOW_") and key != "HERDR_DEVFLOW_HOME":
                cls.env.pop(key)
        for key in ("GIT_DIR", "GIT_WORK_TREE", "DEVOPS_SOURCE_ONLY"):
            cls.env.pop(key, None)
        cls.helper = cls.root / "compiled helper"
        subprocess.run(["go", "build", "-o", str(cls.helper),
                        "./tools/herdr-devflow/cmd/herdr-devflow"],
                       cwd=ROOT, env=cls.env, check=True, timeout=120)

    def setUp(self):
        self.fixture = self.root / self._testMethodName
        self.fixture.mkdir()

    def run_cli(self, args, env=None, cwd=None, input=""):
        return subprocess.run([str(arg) for arg in args], cwd=cwd or self.fixture,
                              env=dict(self.env, **(env or {})), input=input,
                              text=True, capture_output=True, timeout=90)

    def recorder(self, path):
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text("#!" + sys.executable + "\n" +
                        "import json, os, sys\n"
                        "print(json.dumps({'args': sys.argv[1:], 'cwd': os.getcwd(), "
                        "'stdin': sys.stdin.read()}))\n"
                        "print('fixture stderr', file=sys.stderr)\n"
                        "sys.exit(23)\n")
        path.chmod(0o700)
        return path

    def test_real_source_and_compiled_helper_modes(self):
        launcher = ROOT / "scripts/herdr-devflow.sh"
        source = self.run_cli(["bash", launcher, "help"],
                              {"HERDR_DEVFLOW_USE_SOURCE": "1"})
        compiled = self.run_cli(["bash", launcher, "help"],
                                {"HERDR_DEVFLOW_BINARY": str(self.helper)})
        self.assertEqual(source.returncode, 0, source.stderr)
        self.assertEqual(compiled.returncode, 0, compiled.stderr)
        self.assertEqual(source.stdout, compiled.stdout)
        self.assertIn("Ori Herdr Devflow bridge", source.stdout)
        for mode in ({"HERDR_DEVFLOW_USE_SOURCE": "1"},
                     {"HERDR_DEVFLOW_BINARY": str(self.helper)}):
            failed = self.run_cli(["bash", launcher, "not-a-command"], mode)
            # go run returns 1 and reports the program's status 2 on stderr;
            # prebuilt execution propagates 2 directly. Preserve this evidence.
            self.assertEqual(failed.returncode, 1 if "HERDR_DEVFLOW_USE_SOURCE" in mode else 2)
            self.assertIn('unknown command "not-a-command"', failed.stderr)

    def test_launcher_argument_and_failure_contract(self):
        target = self.fixture / "Ori target with spaces"
        scripts = target / "scripts"
        scripts.mkdir(parents=True)
        launcher = scripts / "herdr-devflow.sh"
        shutil.copyfile(ROOT / "scripts/herdr-devflow.sh", launcher)
        fake = self.recorder(self.fixture / "fake helper")
        payload = ["cleanup", "--worktree", str(target), "literal; $(not-a-command)"]
        result = self.run_cli(["bash", launcher, *payload],
                              {"HERDR_DEVFLOW_BINARY": str(fake)}, input="stdin bytes\n")
        self.assertEqual(result.returncode, 23)
        record = json.loads(result.stdout)
        self.assertEqual(record["args"], ["--repo-root", str(target), *payload])
        self.assertEqual(record["stdin"], "stdin bytes\n")
        self.assertEqual(result.stderr, "fixture stderr\n")
        fake_go = self.recorder(self.fixture / "fake bin" / "go")
        result = self.run_cli(["bash", launcher, *payload], {
            "HERDR_DEVFLOW_USE_SOURCE": "1", "HERDR_DEVFLOW_BINARY": str(fake),
            "PATH": str(fake_go.parent) + os.pathsep + self.env["PATH"],
        })
        self.assertEqual(result.returncode, 23)
        record = json.loads(result.stdout)
        self.assertEqual(record["args"], ["run", "./tools/herdr-devflow/cmd/herdr-devflow",
                                          "--repo-root", str(target), *payload])
        self.assertEqual(record["cwd"], str(target))

    def test_sourced_wt_re_resolves_target_and_refuses_missing_helper(self):
        roots = [self.fixture / "Ori one", self.fixture / "Ori two"]
        for target in roots:
            self.recorder(target / "scripts" / "recorder")
            (target / "scripts" / "herdr-devflow.sh").write_text(
                '#!/bin/bash\nexec "$(dirname "$0")/recorder" "$@"\n')
        # Git evidence is fake. No actual worktrees are created or removed.
        code = '''source "$1"
function git { [[ "$*" == "rev-parse --show-toplevel" ]] || return 91; print -r -- "$PWD"; }
cd "$2" || exit
wt_devflow config agent-defaults --tsv
first=$?
cd "$3" || exit
wt_devflow config agent-defaults --tsv
second=$?
[[ $first == 23 && $second == 23 ]]
'''
        result = self.run_cli(["zsh", "-f", "-c", code, "baseline",
                               ROOT / "scripts/wt.sh", *roots])
        self.assertEqual(result.returncode, 0, result.stderr)
        records = [json.loads(line) for line in result.stdout.splitlines()]
        self.assertEqual([record["cwd"] for record in records], list(map(str, roots)))
        toolbox = self.fixture / "toolbox without Ori helper"
        toolbox.mkdir()
        failure = self.run_cli(["zsh", "-f", "-c",
                               'source "$1"; function git { print -r -- "$PWD"; }; wt_devflow cleanup',
                               "baseline", ROOT / "scripts/wt.sh"], cwd=toolbox)
        self.assertEqual(failure.returncode, 1)
        self.assertIn("Ori devflow helper not found:", failure.stdout)
        # This is only legacy missing-helper refusal, not new-tool identity
        # validation: a copied toolbox containing that helper would be unsafe.

    def test_installed_plugin_uses_stable_runtime_without_shell_startup(self):
        runtime = self.fixture / "stable runtime with spaces"
        plugin = runtime / "plugin" / "plugin.sh"
        plugin.parent.mkdir(parents=True)
        shutil.copyfile(ROOT / "tools/herdr-devflow/plugin.sh", plugin)
        self.recorder(runtime / "bin" / "herdr-devflow")
        result = subprocess.run(["/bin/sh", str(plugin), "plugin", "actions"],
                                cwd=self.fixture, env={"HOME": str(self.home), "PATH": "/usr/bin:/bin"},
                                input="", text=True, capture_output=True, timeout=10)
        self.assertEqual(result.returncode, 23)
        self.assertEqual(json.loads(result.stdout)["args"],
                         ["--home", str(runtime), "plugin", "actions"])
        dispatcher = (ROOT / "tools/herdr-devflow/launchd/com.ori.herdr-devflow.plist.tmpl").read_text()
        self.assertIn("{{HELPER_PATH}}", dispatcher)
        self.assertIn("<string>dispatch</string>", dispatcher)
        self.assertIn("<string>{{RUNTIME_ROOT}}</string>", dispatcher)


if __name__ == "__main__":
    unittest.main(verbosity=2)
