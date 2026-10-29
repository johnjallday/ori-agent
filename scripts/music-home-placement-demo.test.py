#!/usr/bin/env python3
"""Fake-only contracts for fresh-sandbox Music Home acceptance orchestration."""
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

SCRIPT = Path(__file__).with_name("music-home-placement-demo.sh")


class PlacementDemoContract(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="ori-home-placement-wrapper-")
        self.root = Path(self.temp.name)
        self.scripts = self.root / "scripts"
        self.scripts.mkdir()
        shutil.copy2(SCRIPT, self.scripts / SCRIPT.name)
        self.trace = self.root / "calls"
        self.sandbox = self.root / "ori-demo.narrated-installed-fixture with spaces"
        self.env = {**os.environ, "TMPDIR": str(self.root), "TRACE": str(self.trace), "FIXTURE_SANDBOX": str(self.sandbox)}
        self.stub("narrated-folder-demo.sh", '''
mkdir -p "$FIXTURE_SANDBOX"
printf 'worktree=%s\n' "$PWD" > "$FIXTURE_SANDBOX/.ori-demo-owner"
printf 'MODEL=%s URL=%s KEY=%s\n' "$NARRATED_DEMO_MODEL" "$OPENAI_BASE_URL" "$OPENAI_API_KEY" >> "$TRACE"
printf 'READY SANDBOX=%s ORI_URL=http://localhost:9998 SONG=synthetic MODE=installed\n' "$FIXTURE_SANDBOX"
trap 'exit 0' TERM INT
while true; do sleep 1; done
''')
        for name in ("smoke.sh", "e2e.sh", "stop-demo-server.sh"):
            self.stub(name, 'printf "%s\\n" "' + name + '" "$@" >> "$TRACE"\n')

    def tearDown(self):
        self.temp.cleanup()

    def stub(self, name, body):
        target = self.scripts / name
        target.write_text("#!/usr/bin/env bash\nset -eu\n" + body)
        target.chmod(0o750)

    def run_case(self, case):
        return subprocess.run(["bash", str(self.scripts / SCRIPT.name), "--case", case,
                               "--port", "9998", "--reaper-source", "/candidate reaper",
                               "--music-source", "/candidate music", "--music-ref", "v0.3.0"],
                              env=self.env, text=True, capture_output=True, timeout=15)

    def test_fault_is_explicit_and_model_is_loopback_only(self):
        result = self.run_case("ack")
        self.assertEqual(result.returncode, 0, result.stderr)
        calls = self.trace.read_text()
        self.assertIn("URL=http://127.0.0.1:1/v1 KEY=disposable-no-inference", calls)
        self.assertIn("MUSIC_HOME_PLACEMENT_FAULT=ack", calls)
        self.assertIn(f"MUSIC_HOME_PLACEMENT_SONG={self.sandbox.resolve()}/Documents/Ori Music Demo", calls)
        self.assertIn("stop-demo-server.sh\n9998", calls)

    def test_collection_runs_two_executed_subsets_without_irrelevant_skips(self):
        result = self.run_case("collection")
        self.assertEqual(result.returncode, 0, result.stderr)
        calls = self.trace.read_text()
        self.assertEqual(calls.count("e2e.sh\n"), 2)
        self.assertEqual(calls.count("seed-song\n"), 5)
        self.assertIn("collection automatic", calls)
        self.assertIn("collection library", calls)
        self.assertIn("MUSIC_HOME_PLACEMENT_LIBRARY=1", calls)

    def test_bad_case_launches_nothing(self):
        result = self.run_case("not-a-case")
        self.assertEqual(result.returncode, 2)
        self.assertFalse(self.trace.exists())


if __name__ == "__main__":
    unittest.main()
