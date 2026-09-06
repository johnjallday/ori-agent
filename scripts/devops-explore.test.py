#!/usr/bin/env python3
"""Hermetic Explore CLI, native-argv, evidence, and actual PTY menu tests."""

import importlib.util
import json
import os
from pathlib import Path
import pty
import select
import shlex
import shutil
import subprocess
import sys
import tempfile
import time
import unittest

ROOT = Path(__file__).resolve().parent.parent
SPEC = importlib.util.spec_from_file_location("evidence", ROOT / "scripts/lib/devops-explore-evidence.py")
EVIDENCE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(EVIDENCE)

NATIVE_FLAGS = """--tools --no-session --no-extensions --no-skills --no-prompt-templates
--no-themes --no-context-files --no-approve --offline --append-system-prompt
--print --thinking --model --safe-mode --restricted --strict-mcp-config
--mcp-config --allowedTools --permission-mode --no-session-persistence --effort"""


class ExploreTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="ori-explore-test-")
        self.addCleanup(self.temp.cleanup)
        self.home = Path(self.temp.name)
        self.repo = self.home / "repo with spaces"
        scripts = self.repo / "scripts"
        (scripts / "lib").mkdir(parents=True)
        shutil.copy(ROOT / "scripts/devops.sh", scripts)
        for name in ("devflow-common.sh", "devops-explore.sh", "devops-explore-launch.sh", "devops-explore-evidence.py"):
            shutil.copy(ROOT / "scripts/lib" / name, scripts / "lib")
        shutil.copytree(ROOT / "scripts/devops-prompts", scripts / "devops-prompts")
        self.script = scripts / "devops.sh"
        self.bin = self.home / "bin"
        self.bin.mkdir()
        # Minimal PATH proves print does not accidentally need gh, Python or a
        # native agent; launch tests install their explicit fake dependencies.
        for name in ("bash", "git", "dirname", "cat", "stty"):
            (self.bin / name).symlink_to(shutil.which(name))
        self.env = dict(os.environ, PATH=str(self.bin), HOME=str(self.home),
                        GIT_CONFIG_NOSYSTEM="1", GIT_CONFIG_GLOBAL=os.devnull,
                        EXPLORE_TEST_LOG=str(self.home / "calls.jsonl"), TERM="xterm-256color")
        for key in ("DEVOPS_SOURCE_ONLY", "GH_LOG", "GH_FAIL", "GH_CREATE_OUTPUT", "GIT_DIR", "GIT_WORK_TREE"):
            self.env.pop(key, None)
        self.git("init", "-q", "-b", "dev")
        self.git("-c", "user.name=Test", "-c", "user.email=test@example.invalid", "-c", "core.hooksPath=/dev/null",
                 "-c", "commit.gpgsign=false", "commit", "--allow-empty", "-qm", "fixture")

    def git(self, *args):
        return subprocess.run([str(self.bin / "git"), *args], cwd=self.repo,
                              env=self.env, check=True, capture_output=True, text=True)

    def cli(self, *args, input=""):
        return subprocess.run([str(self.bin / "bash"), str(self.script), "explore", *args],
                              cwd=self.home, env=self.env, input=input, text=True,
                              capture_output=True, timeout=15)

    def sourced(self, code, input=""):
        return subprocess.run([str(self.bin / "bash"), "-c",
                               "DEVOPS_SOURCE_ONLY=1 source " + shlex.quote(str(self.script)) + "; " + code],
                              env=self.env, input=input, text=True, capture_output=True, timeout=15)

    def calls(self):
        log = self.home / "calls.jsonl"
        return [json.loads(line) for line in log.read_text().splitlines()] if log.exists() else []

    def install_fakes(self):
        (self.bin / "python3").symlink_to(sys.executable)
        actor = self.bin / "actor"
        actor.write_text(f"#!{sys.executable}\n" + '''import json, os, sys
from pathlib import Path
name = Path(sys.argv[0]).name
args = sys.argv[1:]
with open(os.environ["EXPLORE_TEST_LOG"], "a") as log:
    log.write(json.dumps({"name": name, "args": args, "cwd": os.getcwd()}) + "\\n")
if name == "gh":
    if os.environ.get("FAKE_GH_FAIL"):
        print("fixture GitHub unavailable", file=sys.stderr)
        sys.exit(9)
    if "--template" in args:
        print("101\\tUseful task\\tbacklog, size:quick\\t2026-01-01")
    else:
        print('[{"number":101,"title":"Useful task","labels":["backlog","size:quick"]}]')
    sys.exit(0)
if "--help" in args:
    print(os.environ["FAKE_NATIVE_FLAGS"])
    sys.exit(0)
if "--list-models" in args:
    print("provider model\\nopenai-codex test-model")
    sys.exit(0)
print("Fixture advisor completed")
sys.exit(int(os.environ.get("FAKE_AGENT_EXIT", "0")))
''')
        actor.chmod(0o700)
        for name in ("pi", "claude", "gh"):
            (self.bin / name).symlink_to(actor)
        self.env["FAKE_NATIVE_FLAGS"] = NATIVE_FLAGS

    def test_catalog_and_every_print_need_no_external_dependencies(self):
        result = self.cli()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("[8] interview", result.stdout)
        for preset in ("next", "quick-win", "finish", "ux", "reliability", "workflow", "missing", "interview"):
            with self.subTest(preset=preset):
                result = self.cli(preset, "--print")
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertTrue(result.stdout.startswith("# Ori work exploration:"))
                self.assertIn("Stay read-only", result.stdout)
                self.assertIn("do not implement", result.stdout)
                self.assertEqual(result.stderr, "")
        self.assertEqual(self.calls(), [])

    def test_validation_precedes_any_native_or_evidence_call(self):
        self.install_fakes()
        invalid = [
            ["../common", "--print"], ["next", "finish"], ["next", "--context"],
            ["next", "--bogus"], ["next", "--print", "--print"],
            ["next", "--context", "a", "--context", "b"], ["next", "--context", "a" * 4001],
            ["next", "--context", "\x1b[2J"], ["next", "--context", "\rspoof"],
            ["next", "--kind", "codex", "--yes"], ["next", "--kind", "", "--yes"],
            ["next", "--model", "sonnet"], ["next", "--thinking", "high"],
            ["next", "--kind", "pi", "--thinking", ""],
            ["next", "--kind", "claude", "--thinking", "minimal"],
            ["next", "--kind", "pi", "--model", "--unsafe"],
            ["next", "--kind", "pi", "--model", ""],
            ["next", "--print", "--yes"], ["next", "--print", "--kind", "pi"],
            ["--print"], ["--context", "hello"], ["next"], ["next", "--yes"],
            ["next", "--kind", "pi"], ["interview", "--kind", "pi", "--yes"],
        ]
        for args in invalid:
            with self.subTest(args=args):
                result = self.cli(*args)
                self.assertEqual(result.returncode, 2, result.stdout + result.stderr)
        self.assertEqual(self.calls(), [])

    def test_context_is_literal_and_prompt_path_is_validated(self):
        marker = self.home / "must not exist"
        context = f'$(touch "{marker}"); `whoami` "quotes"\n@/etc/passwd --kind pi'
        result = self.cli("next", "--context", context, "--print")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn(context, result.stdout)
        self.assertFalse(marker.exists())
        (self.repo / "scripts/devops-prompts/next.md").unlink()
        result = self.cli("next", "--print")
        self.assertEqual(result.returncode, 1)
        self.assertIn("prompt files are missing", result.stderr)

    def test_menu_display_cancellation_and_eof_do_not_call_agents(self):
        self.install_fakes()
        for input in ("\n", "q\n", "2\n45 minutes\nd\n", "2\n", "2\n\nq\n", "2\n\n", "bad\n2\n\nd\n"):
            result = self.sourced("explore_menu", input=input)
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(self.calls(), [])

    def test_selector_uses_advisory_labels_without_leaking_planner_state(self):
        self.install_fakes()
        result = self.sourced('explore_is_interactive() { return 0; }; planner_kind_choice=original; '
                              'explore_menu; printf "\\noriginal=%s\\n" "$planner_kind_choice"',
                              input="2\n\nl\nc\ns\n3\nn\n")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("Choose the advisory agent", result.stdout)
        self.assertIn("Claude model for this advisory session", result.stdout)
        self.assertNotIn("recorded model on retry", result.stdout)
        self.assertIn("original=original", result.stdout)
        self.assertEqual(self.calls(), [])

    def test_both_agents_receive_safe_literal_argv_and_fresh_evidence(self):
        self.install_fakes()
        for kind in ("pi", "claude"):
            with self.subTest(kind=kind):
                result = self.cli("next", "--context", '$(never execute); @file', "--kind", kind,
                                  "--model", 'opaque/model with space;literal', "--thinking", "xhigh", "--yes")
                self.assertEqual(result.returncode, 0, result.stderr)
                launches = [call for call in self.calls() if call["name"] == kind and "--help" not in call["args"]]
                self.assertEqual(len(launches), 1)
                args = launches[0]["args"]
                self.assertEqual(Path(launches[0]["cwd"]).resolve(), self.repo.resolve())
                self.assertEqual(args[args.index("--model") + 1], 'opaque/model with space;literal')
                self.assertEqual(args[args.index("--") + 1], args[-1])
                self.assertIn('$(never execute); @file', args[-1])
                self.assertIn("captured_at", args[-1])
                self.assertIn("Useful task", args[-1])
                self.assertIn("--print", args)
                self.assertNotIn("--resume", args)
                self.assertNotIn("--continue", args)
                self.assertNotIn("--dangerously-skip-permissions", args)
                tools = args[args.index("--tools") + 1]
                if kind == "pi":
                    self.assertEqual(tools, "read,grep,find,ls")
                    self.assertIn("--no-session", args)
                    self.assertIn("--no-extensions", args)
                    self.assertIn("--no-approve", args)
                    self.assertEqual(args[args.index("--thinking") + 1], "xhigh")
                else:
                    self.assertEqual(tools, "Read,Glob,Grep")
                    self.assertIn("--safe-mode", args)
                    self.assertIn("--restricted", args)
                    self.assertIn("--no-session-persistence", args)
                    self.assertEqual(args[args.index("--effort") + 1], "xhigh")
        gh_calls = [call["args"][:2] for call in self.calls() if call["name"] == "gh"]
        self.assertEqual(gh_calls, [["issue", "list"], ["pr", "list"]] * 2)
        self.assertFalse((self.repo / "tasks").exists())
        self.assertEqual(self.git("branch", "--format=%(refname:short)").stdout.strip(), "dev")

    def test_native_failure_and_missing_capability_fail_closed(self):
        result = self.cli("next", "--kind", "pi", "--yes")
        self.assertEqual(result.returncode, 1)
        self.assertIn("requires the pi CLI", result.stderr)
        self.install_fakes()
        self.env["FAKE_NATIVE_FLAGS"] = "--tools --print"
        result = self.cli("next", "--kind", "pi", "--yes")
        self.assertEqual(result.returncode, 1)
        self.assertIn("refusing an unrestricted fallback", result.stderr)
        self.assertEqual(len(self.calls()), 1)  # only native help, no evidence
        self.env["FAKE_NATIVE_FLAGS"] = NATIVE_FLAGS
        self.env["FAKE_AGENT_EXIT"] = "17"
        self.env["FAKE_GH_FAIL"] = "1"
        result = self.cli("next", "--kind", "claude", "--yes")
        self.assertEqual(result.returncode, 17)
        self.assertIn("Advisor exited with status 17", result.stderr)
        args = self.calls()[-1]["args"]
        self.assertIn("fixture GitHub unavailable", args[-1])
        self.assertIn("unavailable", args[-1])
        self.assertIn("--print", result.stderr)

    def test_actual_line_repl_offers_explore_without_refreshing_issues(self):
        self.install_fakes()
        result = subprocess.run([str(self.bin / "bash"), str(self.script)], env=self.env,
                                input="e\n2\n45 minutes\nd\nq\n", text=True, capture_output=True, timeout=10)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("[e] Explore next work", result.stdout)
        self.assertIn("Prompt displayed", result.stdout)
        self.assertEqual([(call["name"], call["args"][:2]) for call in self.calls()], [("gh", ["issue", "list"])])

    def test_missing_python_and_interactive_launch_contract(self):
        self.install_fakes()
        (self.bin / "python3").unlink()
        result = self.cli("next", "--kind", "pi", "--yes")
        self.assertEqual(result.returncode, 1)
        self.assertIn("requires python3", result.stderr)
        self.assertEqual(self.calls(), [])
        (self.bin / "python3").symlink_to(sys.executable)
        for kind in ("pi", "claude"):
            result = self.sourced('explore_is_interactive() { return 0; }; '
                                  'explore_action interview --kind ' + kind + ' --yes')
            self.assertEqual(result.returncode, 0, result.stderr)
            args = self.calls()[-1]["args"]
            self.assertNotIn("--print", args)
            self.assertNotIn("--no-session-persistence", args)
            self.assertIn("Wait for the user's answers", args[-1])

    def test_pty_global_menu_preserves_empty_view_and_selection(self):
        # Actual terminal modes, run_picker dispatch and Explore UI; only the
        # dashboard's remote data loader is fixture-backed.
        for view, label in (("1", "All open issues"), ("2", "Needs my decision"),
                            ("3", "Backlog"), ("4", "Feature proposals"), ("5", "Ready to build")):
            for empty in (True, False):
                with self.subTest(empty=empty, view=view):
                    self.drive_picker(empty, view, label)

    def drive_picker(self, empty, view, label):
        master, slave = pty.openpty()
        labels = {"2": "needs-decision", "4": "feature-proposal, size:planned"}.get(view, "backlog, size:quick")
        issue_setup = "" if empty else f'''all_issue_numbers=(101 102); all_issue_titles=('First task' 'Second task');
all_issue_labels=('{labels}' '{labels}'); all_issue_updates=(2026-01-01 2026-01-01);'''
        code = 'DEVOPS_SOURCE_ONLY=1 source ' + shlex.quote(str(self.script)) + '; ' + '''
load_picker_index() {
  all_issue_numbers=(); all_issue_titles=(); all_issue_labels=(); all_issue_updates=();
  implementation_summary='Fixture dashboard'; picker_release_summary='Fixture release';
''' + issue_setup + '''
}
run_picker
'''
        process = subprocess.Popen([str(self.bin / "bash"), "-c", code], env=self.env,
                                   stdin=slave, stdout=slave, stderr=slave, start_new_session=True)
        os.close(slave)
        transcript = bytearray()
        cursor = 0

        def expect(text):
            nonlocal cursor
            wanted = text.encode()
            deadline = time.monotonic() + 8
            while wanted not in transcript[cursor:]:
                if time.monotonic() > deadline:
                    self.fail("PTY timed out waiting for " + text + "\n" + transcript.decode(errors="replace"))
                if select.select([master], [], [], 0.1)[0]:
                    try:
                        data = os.read(master, 65536)
                    except OSError:
                        data = b""
                    if not data:
                        self.fail("PTY exited waiting for " + text + "\n" + transcript.decode(errors="replace"))
                    transcript.extend(data)
            cursor = transcript.index(wanted, cursor) + len(wanted)

        try:
            expect("[e] Explore next work")
            os.write(master, view.encode())  # every-view action, not just Ready
            expect(label)
            if not empty:
                os.write(master, b"j")
                expect("Second task")
            os.write(master, b"e")
            expect("prompt> ")
            os.write(master, b"2\n")
            expect("context (optional")
            os.write(master, b"45 minutes; no frontend\n")
            expect("action> ")
            os.write(master, b"d\n")
            expect("Press Enter to return to the Issue picker.")
            os.write(master, b"\n")
            expect("[e] Explore next work")
            os.write(master, b"e")
            expect("prompt> ")
            os.write(master, b"q\n")
            expect("Press Enter to return to the Issue picker.")
            os.write(master, b"\n")
            expect("[e] Explore next work")
            os.write(master, b"q")
            self.assertEqual(process.wait(timeout=5), 0)
            rendered = transcript.decode(errors="replace")
            self.assertIn("45 minutes; no frontend", rendered)
            self.assertIn(label, rendered)
            if empty:
                self.assertIn("No matching open issues.", rendered)
            else:
                last_screen = rendered.rsplit("Ori DevOps", 1)[1]
                self.assertRegex(last_screen, "›.*#102.*Second task")
            demo = os.environ.get("EXPLORE_DEMO_TRANSCRIPT")
            if demo:
                with open(demo, "a") as output:
                    output.write("\n--- Fixture-backed real PTY; view=" + view + "; empty=" + str(empty) + " ---\n" + rendered)
        finally:
            if process.poll() is None:
                process.kill()
                process.wait()
            os.close(master)


class EvidenceTests(unittest.TestCase):
    def test_capture_timeout_and_output_limit_are_explicit(self):
        previous = EVIDENCE.TIMEOUT_SECONDS
        EVIDENCE.TIMEOUT_SECONDS = 0.05
        try:
            result = EVIDENCE.capture(ROOT, [sys.executable, "-c", "import time; time.sleep(10)"])
            self.assertEqual(result["status"], "timeout")
        finally:
            EVIDENCE.TIMEOUT_SECONDS = previous
        result = EVIDENCE.capture(ROOT, [sys.executable, "-c", 'print("x" * 200)'], limit=30)
        self.assertTrue(result["truncated"])
        self.assertEqual(len(result["output"]), 30)
        self.assertEqual(result["status"], "ok")

    def test_task_progress_uses_exact_feature_worktree_and_refuses_symlinks(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "tasks").mkdir()
            task = root / "tasks/tasks-101-102-example.md"
            task.write_text("- [x] 1.0 Done\n- [ ] 2.0 Next\n")
            raw = f"worktree {root}\0branch refs/heads/feature/101-102-example\0\0"
            result = EVIDENCE.task_progress(raw)["records"][0]
            self.assertEqual(result["checked"], 1)
            self.assertEqual(result["total"], 2)
            self.assertEqual(result["next_unchecked"], ["2.0 Next"])
            task.unlink()
            task.symlink_to(root / "other")
            result = EVIDENCE.task_progress(raw)["records"][0]
            self.assertEqual(result["status"], "unavailable")
            self.assertIn("symlinked", result["error"])


if __name__ == "__main__":
    unittest.main()
