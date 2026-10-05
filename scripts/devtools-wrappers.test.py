#!/usr/bin/env python3
"""Ori's ordinary wrapper gate: fake tool installations, no companion required."""
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parent.parent
ENTRIES = ("devops.sh", "herdr-devflow.sh", "away-dispatch.sh", "away-tick.sh",
           "away/install-herdr-telegram.sh", "away/install-pmset-helper.sh",
           "away/install-pmset-sudoers.sh", "away/pmset-helper.sh")


class WrapperTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="ori-wrapper-")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name).resolve()
        self.env = dict(os.environ, HOME=str(self.root), GIT_CONFIG_NOSYSTEM="1",
                        GIT_CONFIG_GLOBAL=os.devnull)
        for key in list(self.env):
            if key.startswith(("HERDR_DEVFLOW_", "ORI_DEVTOOLS_", "DEVOPS_")):
                self.env.pop(key)
        for key in ("GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE"):
            self.env.pop(key, None)
        self.ori = self.target("Ori with spaces")
        self.other = self.target("another Ori")
        self.tool = self.root / "fake tools"
        (self.tool / "scripts").mkdir(parents=True)
        (self.tool / "devtools-contract").write_text("ori-devtools-v1\n")
        (self.tool / "go.mod").write_text("module ori-devtools.local\n")
        (self.tool / "record.py").write_text('''import json, os, sys
print(json.dumps(dict(argv=sys.argv[1:], target=os.environ.get('HERDR_DEVFLOW_REPO_ROOT'),
                     tool=os.environ['ORI_DEVTOOLS_HOME'], stdin=sys.stdin.read())))
print('fixture stderr', file=sys.stderr)
raise SystemExit(int(os.environ.get('FIXTURE_EXIT', '0')))
''')
        for name in ENTRIES:
            (self.tool / "scripts" / name).parent.mkdir(parents=True, exist_ok=True)
            (self.tool / "scripts" / name).write_text('''#!/usr/bin/env bash
if [[ "${DEVOPS_SOURCE_ONLY:-}" == 1 || "${AWAY_DISPATCH_SOURCE_ONLY:-}" == 1 ]]; then
  fixture_sourced() { printf 'source functions remain in caller'; }
  return 37
fi
exec python3 "$ORI_DEVTOOLS_HOME/record.py" "$@"
''')
        (self.tool / "scripts/wt.sh").write_text('''function wt {
  cd "$1" || return
  printf 'navigated:%s\\n' "$PWD"
  return "${FIXTURE_EXIT:-0}"
}
''')
        self.env["ORI_DEVTOOLS_HOME"] = str(self.tool)

    def target(self, name):
        target = self.root / name
        (target / "scripts/lib").mkdir(parents=True)
        subprocess.run(["git", "init", "-q", str(target)], env=self.env, check=True)
        (target / "go.mod").write_text("module github.com/johnjallday/ori-agent\n")
        for entry in (*ENTRIES, "wt.sh", "lib/devtools-selector.sh",
                      "devtools-make.sh", "devtools-setup-skill.sh"):
            (target / "scripts" / entry).parent.mkdir(parents=True, exist_ok=True)
            shutil.copy(ROOT / "scripts" / entry, target / "scripts" / entry)
        return target

    def run_command(self, args, env=None, input="", cwd=None):
        return subprocess.run([str(arg) for arg in args], cwd=cwd or self.root,
                              env=dict(self.env, **(env or {})), input=input,
                              text=True, capture_output=True, timeout=10)

    def test_argv_stdin_target_and_child_exit_are_preserved(self):
        args = ["value with spaces", "$(touch not-executed)", "--", "", "quoted'\"text"]
        for entry in ENTRIES:
            for target in (self.ori, self.other):
                for code in (0, 20, 21, 37):
                    shell = "zsh" if entry == "away-tick.sh" else "bash"
                    result = self.run_command([shell, target / "scripts" / entry, *args],
                                              {"FIXTURE_EXIT": str(code)}, "line one\nline two\n")
                    self.assertEqual(result.returncode, code, result.stderr)
                    self.assertEqual(json.loads(result.stdout), dict(
                        argv=args, target=str(target), tool=str(self.tool), stdin="line one\nline two\n"))
                    self.assertEqual(result.stderr, "fixture stderr\n")
        self.assertFalse((self.root / "not-executed").exists())

    def test_missing_malformed_or_incompatible_source_never_falls_back(self):
        for selected in ("", "relative", str(self.root / "missing"), str(self.tool) + "\n", str(self.ori)):
            result = self.run_command(["bash", self.ori / "scripts/devops.sh", "help"],
                                      {"ORI_DEVTOOLS_HOME": selected})
            self.assertEqual(result.returncode, 2, result.stderr)
            self.assertEqual(result.stdout, "")
            self.assertIn("Ori devtools:", result.stderr)
        for marker in ("ori-devtools-v2\n", "ori-devtools-v1", "ori-devtools-v1\n\n"):
            (self.tool / "devtools-contract").write_text(marker)
            result = self.run_command(["bash", self.ori / "scripts/herdr-devflow.sh", "help"])
            self.assertEqual(result.returncode, 2, result.stderr)
        (self.tool / "devtools-contract").write_text("ori-devtools-v1\n")
        (self.tool / "scripts/herdr-devflow.sh").unlink()
        result = self.run_command(["bash", self.ori / "scripts/herdr-devflow.sh", "help"])
        self.assertEqual(result.returncode, 2, result.stderr)

    def test_target_conflict_and_unrelated_module_refuse_before_tool(self):
        for value in (str(self.other), "", "relative"):
            result = self.run_command(["bash", self.ori / "scripts/devops.sh", "new", "never post"],
                                      {"HERDR_DEVFLOW_REPO_ROOT": value})
            self.assertEqual(result.returncode, 2, result.stderr)
            self.assertEqual(result.stdout, "")
        (self.ori / "go.mod").write_text("module another.project\n")
        result = self.run_command(["bash", self.ori / "scripts/herdr-devflow.sh", "setup"])
        self.assertEqual(result.returncode, 2, result.stderr)
        self.assertEqual(result.stdout, "")

    def test_symlink_selection_and_matching_target_normalize(self):
        link = self.root / "tool link"
        link.symlink_to(self.tool, target_is_directory=True)
        target_link = self.root / "target link"
        target_link.symlink_to(self.ori, target_is_directory=True)
        result = self.run_command(["bash", target_link / "scripts/devops.sh", "all"],
                                  {"ORI_DEVTOOLS_HOME": str(link), "HERDR_DEVFLOW_REPO_ROOT": str(target_link)})
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(json.loads(result.stdout)["target"], str(self.ori))
        self.assertEqual(json.loads(result.stdout)["tool"], str(self.tool))

    def test_source_only_devops_preserves_functions_and_status(self):
        result = self.run_command(["bash", "-c",
                                  'DEVOPS_SOURCE_ONLY=1 source "$1"; code=$?; fixture_sourced; exit "$code"',
                                  "fixture", self.ori / "scripts/devops.sh"])
        self.assertEqual(result.returncode, 37, result.stderr)
        self.assertEqual(result.stdout, "source functions remain in caller")

    def test_away_source_only_and_sanitized_installed_tick(self):
        result = self.run_command(["bash", "-c",
                                  'AWAY_DISPATCH_SOURCE_ONLY=1 source "$1"; code=$?; fixture_sourced; exit "$code"',
                                  "fixture", self.ori / "scripts/away-dispatch.sh"])
        self.assertEqual(result.returncode, 37, result.stderr)
        self.assertIn("source functions remain", result.stdout)
        # Redirect only the fixed default in this disposable selector to the
        # fake installation. The real owner-selected path is never written.
        selector = self.ori / "scripts/lib/devtools-selector.sh"
        original = selector.read_text()
        self.assertEqual(original.count("/Users/jjdev/Projects/ori/devtools"), 1)
        selector.write_text(original.replace("/Users/jjdev/Projects/ori/devtools", str(self.tool)))
        result = self.run_command(["env", "-i", "HOME=" + str(self.root),
                                  "PATH=" + self.env["PATH"], "zsh", "-f",
                                  self.ori / "scripts/away-tick.sh"], cwd=self.root)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(json.loads(result.stdout)["target"], str(self.ori))
        self.assertEqual(json.loads(result.stdout)["tool"], str(self.tool))

    def test_setup_skill_resolution_is_read_only_and_refuses_missing_content(self):
        skill = self.tool / ".agents/skills/setup-herdr/SKILL.md"
        skill.parent.mkdir(parents=True)
        skill.write_text("# Fixture operating skill\n")
        entry = self.ori / "scripts/devtools-setup-skill.sh"
        result = self.run_command(["bash", entry])
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout, str(skill) + "\n")
        skill.unlink()
        result = self.run_command(["bash", entry])
        self.assertEqual(result.returncode, 2, result.stderr)
        self.assertEqual(result.stdout, "")
        self.assertIn("devtools-setup-skill.sh", (ROOT / ".agents/skills/setup-herdr/SKILL.md").read_text())

    def test_explicit_make_convenience_targets_do_not_build_in_ori(self):
        (self.tool / "Makefile").write_text("# fake companion\n")
        fake = self.root / "fake bin"
        fake.mkdir()
        make = fake / "make"
        make.write_text('#!/bin/sh\nexec python3 "$ORI_DEVTOOLS_HOME/record.py" "$@"\n')
        make.chmod(0o700)
        env = {"PATH": str(fake) + os.pathsep + self.env["PATH"], "FIXTURE_EXIT": "31"}
        entry = self.ori / "scripts/devtools-make.sh"
        for target in ("build", "test", "cross"):
            result = self.run_command(["bash", entry, target], env)
            self.assertEqual(result.returncode, 31, result.stderr)
            self.assertEqual(json.loads(result.stdout)["argv"], ["-C", str(self.tool), target])
        result = self.run_command(["bash", entry, "install"], env)
        self.assertEqual(result.returncode, 2, result.stderr)
        self.assertEqual(result.stdout, "")
        (self.tool / "Makefile").unlink()
        result = self.run_command(["bash", entry, "build"], env)
        self.assertEqual(result.returncode, 2, result.stderr)

    def test_sourced_wt_is_lazy_and_survives_function_only_snapshot(self):
        snapshot = self.root / "functions.zsh"
        saved = self.run_command(["zsh", "-f", "-c", 'source "$1" || exit; functions > "$2"',
                                 "fixture", self.ori / "scripts/wt.sh", snapshot],
                                 {"ORI_DEVTOOLS_HOME": str(self.root / "not-installed")})
        self.assertEqual(saved.returncode, 0, saved.stderr)
        result = self.run_command(["zsh", "-f", "-c",
                                  'source "$1"; wt "$2"; code=$?; print -r -- "caller:$PWD"; exit "$code"',
                                  "fixture", snapshot, self.other], {"FIXTURE_EXIT": "20"})
        self.assertEqual(result.returncode, 20, result.stderr)
        self.assertIn("caller:" + str(self.other), result.stdout)
        refused = self.run_command(["zsh", "-f", "-c",
                                   'source "$1"; wt anything; code=$?; print shell-alive; exit "$code"',
                                   "fixture", snapshot], {"ORI_DEVTOOLS_HOME": ""})
        self.assertEqual(refused.returncode, 2, refused.stderr)
        self.assertIn("shell-alive", refused.stdout)


if __name__ == "__main__":
    unittest.main(verbosity=2)
