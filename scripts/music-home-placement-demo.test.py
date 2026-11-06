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
printf 'RELEASES=%s MODE=%s\n' "$ORI_INTEGRATION_RELEASES_API" "$1" >> "$TRACE"
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
        self.assertIn(f"--output\n{self.sandbox.resolve()}/evidence/placement-results", calls)

    def test_collection_runs_two_executed_subsets_without_irrelevant_skips(self):
        result = self.run_case("collection")
        self.assertEqual(result.returncode, 0, result.stderr)
        calls = self.trace.read_text()
        self.assertEqual(calls.count("e2e.sh\n"), 2)
        self.assertEqual(calls.count("seed-song\n"), 5)
        self.assertIn("collection automatic", calls)
        self.assertIn("collection library", calls)
        self.assertIn("MUSIC_HOME_PLACEMENT_LIBRARY=1", calls)

    def test_collection_map_runs_real_setup_before_library_opens(self):
        result = self.run_case("collection-map")
        self.assertEqual(result.returncode, 0, result.stderr)
        calls = self.trace.read_text()
        self.assertIn("MUSIC_HOME_PLACEMENT_COLLECTION_MAP=1", calls)
        self.assertIn("collection map setup", calls)
        self.assertIn("collection library", calls)
        self.assertEqual(calls.count("seed-song\n"), 5)

    def test_staffing_sets_up_a_consenting_home_before_the_staffing_spec(self):
        result = self.run_case("staffing")
        self.assertEqual(result.returncode, 0, result.stderr)
        calls = self.trace.read_text()
        self.assertEqual(calls.count("seed-song\n"), 5)
        self.assertIn("Album-1", calls)
        self.assertEqual(calls.count("e2e.sh\n"), 2)
        setup, staffing = calls.split("e2e.sh\n")[1:3]
        self.assertIn("collection automatic setup", setup)
        self.assertNotIn("LIBRARY_SETUP_STAFFING", setup)
        self.assertIn("LIBRARY_SETUP_STAFFING=1", staffing)
        self.assertIn("tests/library-setup-staffing.spec.ts", staffing)
        self.assertNotIn("MUSIC_HOME_PLACEMENT_FAULT", calls)
        self.assertIn("URL=http://127.0.0.1:1/v1 KEY=disposable-no-inference", calls)

    def test_staffing_repair_runs_only_the_repair_journey(self):
        result = self.run_case("staffing-repair")
        self.assertEqual(result.returncode, 0, result.stderr)
        calls = self.trace.read_text()
        self.assertEqual(calls.count("seed-song\n"), 5)
        setup, repair = calls.split("e2e.sh\n")[1:3]
        self.assertIn("collection automatic setup", setup)
        self.assertIn("LIBRARY_SETUP_STAFFING=repair", repair)
        self.assertNotIn("LIBRARY_SETUP_STAFFING=1", repair)
        self.assertIn("-g\nrepair existing\n", repair)

    def test_staffing_recovery_runs_only_the_recovery_journeys(self):
        result = self.run_case("staffing-recovery")
        self.assertEqual(result.returncode, 0, result.stderr)
        calls = self.trace.read_text()
        self.assertEqual(calls.count("seed-song\n"), 5)
        setup, recovery = calls.split("e2e.sh\n")[1:3]
        self.assertNotIn("LIBRARY_SETUP_STAFFING", setup)
        self.assertIn("LIBRARY_SETUP_STAFFING=recovery", recovery)
        self.assertIn("-g\nrecovery:\n", recovery)
        self.assertNotIn("MUSIC_HOME_PLACEMENT_FAULT", calls)

    def test_a11y_case_is_explicit_and_discards_ambient_fault(self):
        self.env["MUSIC_HOME_PLACEMENT_FAULT"] = "occupied"
        self.env["MUSIC_HOME_PLACEMENT_COLLECTION_MAP"] = "1"
        self.stub("e2e.sh", 'printf "%s\\n" "AMBIENT=${MUSIC_HOME_PLACEMENT_FAULT:-none} MAP=${MUSIC_HOME_PLACEMENT_COLLECTION_MAP:-none}" "$@" >> "$TRACE"\n')
        result = self.run_case("a11y-touch")
        self.assertEqual(result.returncode, 0, result.stderr)
        calls = self.trace.read_text()
        self.assertIn("AMBIENT=none MAP=none", calls)
        self.assertIn("MUSIC_HOME_PLACEMENT_A11Y=touch", calls)
        self.assertIn("tests/music-home-map-placement.a11y.spec.ts", calls)

    def test_chat_runs_home_setup_then_the_manager_hand_off(self):
        result = self.run_case("chat")
        self.assertEqual(result.returncode, 0, result.stderr)
        calls = self.trace.read_text()
        self.assertEqual(calls.count("e2e.sh\n"), 1)
        self.assertIn("tests/music-home-map-placement.spec.ts", calls)
        self.assertIn("-g\nsingle-song\n", calls)
        self.assertNotIn("MUSIC_HOME_PLACEMENT_FAULT", calls)
        self.assertIn(f"MUSIC_HOME_PLACEMENT_SONG={self.sandbox.resolve()}/Documents/Ori Music Demo", calls)

    def test_chat_choice_gives_the_song_a_second_project_file(self):
        song = self.sandbox / "Documents" / "Ori Music Demo"
        song.mkdir(parents=True)
        (song / "Ori Music Demo.rpp").write_text("<REAPER_PROJECT>\n")
        result = self.run_case("chat-choice")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue((song / "Ori Music Demo (mix).rpp").is_file())
        calls = self.trace.read_text()
        self.assertIn("MUSIC_HOME_PLACEMENT_TWO_FILES=1", calls)
        self.assertIn("-g\nsingle-song\n", calls)

    def test_chat_a11y_sets_up_the_home_before_the_a11y_hand_off(self):
        result = self.run_case("chat-a11y")
        self.assertEqual(result.returncode, 0, result.stderr)
        calls = self.trace.read_text()
        self.assertEqual(calls.count("e2e.sh\n"), 2)
        setup, a11y = calls.split("e2e.sh\n")[1:3]
        self.assertIn("tests/music-home-map-placement.spec.ts", setup)
        self.assertIn("single-song reviewed", setup)
        self.assertNotIn("MUSIC_HOME_PLACEMENT_A11Y", setup)
        self.assertIn("MUSIC_HOME_PLACEMENT_A11Y=chat", a11y)
        self.assertIn("tests/music-home-map-placement.a11y.spec.ts", a11y)
        self.assertIn("manager hand-off", a11y)

    def test_wizard_runs_only_the_source_dialog_journey(self):
        result = self.run_case("wizard")
        self.assertEqual(result.returncode, 0, result.stderr)
        calls = self.trace.read_text()
        self.assertEqual(calls.count("e2e.sh\n"), 1)
        self.assertIn("tests/reaper-folder-setup-wizard.spec.ts", calls)
        self.assertIn("-g\nmusic wizard\n", calls)
        self.assertNotIn("MUSIC_HOME_PLACEMENT_FAULT", calls)
        self.assertIn("URL=http://127.0.0.1:1/v1 KEY=disposable-no-inference", calls)

    def test_wizard_choices_and_guided_handoff_are_explicit_cases(self):
        song = self.sandbox / "Documents" / "Ori Music Demo"
        song.mkdir(parents=True)
        (song / "Ori Music Demo.rpp").write_text("<REAPER_PROJECT>\n")
        result = self.run_case("wizard-choice")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue((song / "Ori Music Demo (mix).rpp").is_file())
        self.assertIn("MUSIC_HOME_PLACEMENT_TWO_FILES=1", self.trace.read_text())
        self.trace.unlink()
        result = self.run_case("wizard-quest")
        self.assertEqual(result.returncode, 0, result.stderr)
        calls = self.trace.read_text()
        self.assertIn("-g\nmusic guided\n", calls)
        self.assertNotIn("MUSIC_HOME_PLACEMENT_TWO_FILES", calls)

    def test_wizard_response_cases_reach_the_browser_not_an_unused_argument_array(self):
        for case in ["wizard-close", "wizard-close-unknown"]:
            with self.subTest(case=case):
                result = self.run_case(case)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertIn(f"MUSIC_HOME_WIZARD_RESPONSE={case}", self.trace.read_text())
                self.trace.unlink()
        self.env["MUSIC_HOME_WIZARD_RESPONSE"] = "wizard-close-unknown"
        result = self.run_case("wizard")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertNotIn("MUSIC_HOME_WIZARD_RESPONSE", self.trace.read_text())

    def test_missing_integration_uses_published_floor_and_its_own_sandbox(self):
        self.sandbox = self.root / "ori-demo.narrated-integration-missing-fixture"
        self.env["FIXTURE_SANDBOX"] = str(self.sandbox)
        result = self.run_case("wizard-integration-missing")
        self.assertEqual(result.returncode, 0, result.stderr)
        calls = self.trace.read_text()
        self.assertIn("RELEASES=http://127.0.0.1:1 MODE=integration-missing", calls)
        self.assertIn("MUSIC_HOME_WIZARD_PREREQUISITE=integration", calls)
        self.assertIn("-g\nmusic prerequisites\n", calls)

    def test_missing_provider_uninstalls_only_the_sandbox_package_before_review(self):
        self.stub("curl", 'printf "curl\\n%s\\n" "$@" >> "$TRACE"\n')
        self.env["PATH"] = str(self.scripts) + os.pathsep + self.env["PATH"]
        result = self.run_case("wizard-provider-missing")
        self.assertEqual(result.returncode, 0, result.stderr)
        calls = self.trace.read_text()
        self.assertIn("http://localhost:9998/api/plugins/music-project-management", calls)
        self.assertIn("MUSIC_HOME_WIZARD_PREREQUISITE=provider", calls)
        self.assertIn("RELEASES=http://127.0.0.1:1 MODE=installed", calls)
        self.assertLess(calls.index("curl\n"), calls.index("e2e.sh\n"))

    def test_bad_case_launches_nothing(self):
        result = self.run_case("not-a-case")
        self.assertEqual(result.returncode, 2)
        self.assertFalse(self.trace.exists())


if __name__ == "__main__":
    unittest.main()
