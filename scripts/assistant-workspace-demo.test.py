#!/usr/bin/env python3
"""Fake-only tests for the isolated workspace-history demo provider."""
import importlib.util
import json
from pathlib import Path
import tempfile
import threading
import unittest
from concurrent.futures import ThreadPoolExecutor
from http.server import ThreadingHTTPServer
from urllib.error import HTTPError
from urllib.request import Request, urlopen

spec = importlib.util.spec_from_file_location("demo", Path(__file__).with_name("assistant-workspace-demo.py"))
demo = importlib.util.module_from_spec(spec)
spec.loader.exec_module(demo)


def message(name="A", status="available"):
    return {"role": "user", "content": "Hold this workspace reply\n<workspace_turn>" + json.dumps({
        "status": status, "subject": {"id": "a", "name": name}
    }) + "</workspace_turn>"}


class DemoProviderTests(unittest.TestCase):
    def test_candidate_acceptance_requires_an_explicit_exact_source_pair(self):
        from unittest.mock import patch
        import contextlib
        import io
        for argv in [["demo", "--new-home"], ["demo", "--portfolio"],
                     ["demo", "--reaper-source", "/candidate"],
                     ["demo", "--placement", "--new-home"],
                     ["demo", "--placement", "--reaper-source", "/reaper", "--music-source", "/music"],
                     ["demo", "--sources", "--placement"], ["demo", "--sources", "--portfolio"],
                     ["demo", "--new-home", "--portfolio", "--reaper-source", "/reaper", "--music-source", "/music"]]:
            with patch("sys.argv", argv), contextlib.redirect_stderr(io.StringIO()):
                with self.assertRaises(SystemExit) as failure:
                    demo.main()
                self.assertEqual(failure.exception.code, 2)

    def test_current_user_projection_not_history(self):
        projection = demo.workspace_projection([message("Old"), message("Current")])
        self.assertEqual(projection["subject"]["name"], "Current")
        for messages in [[], [{"role": "user", "content": "Hello"}], [message(status="denied")],
                         [message(), {"role": "assistant", "content": "Old scope"}]]:
            with self.assertRaises(ValueError):
                demo.workspace_projection(messages)

    def test_reader_fixture_says_only_what_a_reader_returned(self):
        ask = "Based on my release notes and open tasks, what next?"
        listing = json.dumps({"status": "available", "notes": [{"note_id": "n1", "title": "Release notes"}]})
        note = json.dumps({"status": "available", "content_read": True, "note_id": "n1", "title": "Release notes",
                           "content": "Master the single first. Then artwork.", "cite_as": "[S1]"})
        tasks = json.dumps({"status": "available", "tasks": [{"task_id": "t0", "title": "Done", "state": "done"},
                                                             {"task_id": "t1", "title": "Master", "state": "ready"}]})
        task = json.dumps({"status": "available", "content_read": True, "task_id": "t1", "title": "Master",
                           "state_label": "Ready", "cite_as": "[S2]"})
        # One reader per round, each chosen from what the last one returned.
        self.assertEqual(demo.reader_step(ask, [], True), {"tool": "assistant_workspace_notes", "arguments": {}})
        self.assertEqual(demo.reader_step(ask, [listing], True)["arguments"], {"note_id": "n1"})
        self.assertEqual(demo.reader_step(ask, [listing, note], True)["tool"], "assistant_workspace_tasks")
        self.assertEqual(demo.reader_step(ask, [listing, note, tasks], True)["arguments"], {"task_id": "t1"})
        answer = demo.reader_step(ask, [listing, note, tasks, task], False)["answer"]
        for expected in ["Master the single first. [S1]", "“Master” is Ready [S2]", "not a recorded fact", "[S9]"]:
            self.assertIn(expected, answer)
        # No reader, or a refused read, never becomes an invented fact.
        self.assertIn("could not read a release note", demo.reader_step(ask, [], False)["answer"])
        refused = json.dumps({"status": "unavailable", "reason": "note_not_found_in_this_workspace"})
        self.assertIn("note_not_found_in_this_workspace", demo.reader_step("Read the note Missing plan", [refused], True)["answer"])
        self.assertEqual(demo.reader_step("Read the note Missing plan", [], True)["arguments"], {"title": "Missing plan"})
        # A greeting, or a note title Ori listed in its overview, starts no reading.
        self.assertIsNone(demo.reader_step("Hello there", [], True))
        turn = [message("Old"), {"role": "user", "content": "Hi\n\n## Context\nRelease notes<workspace_turn>" +
                                 json.dumps({"status": "available"}) + "</workspace_turn>"}]
        user, results = demo.current_turn(turn + [{"role": "assistant", "content": ""}, {"role": "tool", "content": "{}"}])
        self.assertEqual((user["content"].split("\n\n##", 1)[0], results), ("Hi", ["{}"]))
        with self.assertRaises(ValueError):
            demo.current_turn(turn + [{"role": "tool", "content": "{}"}, {"role": "system", "content": "x"}, {"role": "tool", "content": "{}"}])

    def test_real_http_fixture_hold_and_safe_failure(self):
        with tempfile.TemporaryDirectory(prefix="ori-awareness-provider.") as temp:
            state = Path(temp)
            server = ThreadingHTTPServer(("127.0.0.1", 0), demo.provider_handler(state))
            thread = threading.Thread(target=server.serve_forever, daemon=True)
            thread.start()
            base = f"http://127.0.0.1:{server.server_port}"
            try:
                with urlopen(base + "/api/tags", timeout=2) as response:
                    self.assertEqual(json.load(response)["models"][0]["name"], demo.MODEL)
                invalid = Request(base + "/api/chat", data=json.dumps({"messages": []}).encode())
                with self.assertRaises(HTTPError) as failure:
                    urlopen(invalid, timeout=2)
                self.assertEqual(failure.exception.code, 400)
                failure.exception.close()
                request = Request(base + "/api/chat", data=json.dumps({"messages": [message()]}).encode())
                accepted = threading.Event()
                original = Path.write_text

                def signal_metadata(path, *args, **kwargs):
                    result = original(path, *args, **kwargs)
                    if path == state / "accepted.json":
                        accepted.set()
                    return result

                from unittest.mock import patch
                with patch.object(Path, "write_text", signal_metadata), ThreadPoolExecutor(max_workers=1) as executor:
                    future = executor.submit(urlopen, request, timeout=3)
                    self.assertTrue(accepted.wait(timeout=2))
                    self.assertFalse(future.done(), "generation is held, not the browser response")
                    self.assertEqual(json.loads((state / "accepted.json").read_text()), {
                        "subject_id": "a", "subject_name": "A"
                    })
                    (state / "release").touch()
                    with future.result(timeout=2) as response:
                        self.assertEqual(json.load(response)["message"]["content"],
                                         "Fixture reply for A. Metadata only.")
            finally:
                (state / "release").touch()
                server.shutdown()
                server.server_close()
                thread.join(timeout=2)


if __name__ == "__main__":
    unittest.main()
