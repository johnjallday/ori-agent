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
                     ["demo", "--folder-response-baseline", "--files"],
                     ["demo", "--folder-response-baseline", "--reaper-source", "/reaper", "--music-source", "/music"],
                     ["demo", "--sources", "--placement"], ["demo", "--sources", "--portfolio"],
                     ["demo", "--files", "--sources"], ["demo", "--files", "--new-home"],
                     ["demo", "--accessibility", "--files"], ["demo", "--accessibility", "--portfolio"],
                     ["demo", "--integrated", "--sources"],
                     ["demo", "--integrated", "--reaper-source", "/reaper", "--music-source", "/music"],
                     ["demo", "--new-home", "--portfolio", "--reaper-source", "/reaper", "--music-source", "/music"]]:
            with patch("sys.argv", argv), contextlib.redirect_stderr(io.StringIO()):
                with self.assertRaises(SystemExit) as failure:
                    demo.main()
                self.assertEqual(failure.exception.code, 2)

    def test_every_plain_demo_names_an_existing_spec_and_its_own_sandbox_variable_and_log(self):
        runs = list(demo.PLAIN_DEMOS.values()) + [demo.DEFAULT_DEMO]
        for spec, variable, log in runs:
            self.assertTrue((demo.ROOT / spec).is_file(), spec)
            self.assertRegex(variable, r"^ORI_WORKSPACE_[A-Z]+_SANDBOX$")
            self.assertIn(variable, (demo.ROOT / spec).read_text())
            self.assertTrue(log.endswith(".log"))
        for column in range(3):
            self.assertEqual(len({run[column] for run in runs}), len(runs), "two runs share a spec, variable or log")

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

    def test_file_fixture_walks_listing_folder_file_and_quotes_only_what_was_read(self):
        listing = json.dumps({"status": "available", "attachments": [{"attachment_id": "a1", "name": "mix.wav", "readable": "not_supported"},
                                                                    {"attachment_id": "a2", "name": "art.md", "readable": "supported"}],
                              "linked_folders": [{"directory_id": "d0", "name": "Project"}, {"directory_id": "d1", "name": "Assets"},
                                                 {"directory_id": "d2", "name": "Archive"}], "project_file": {"name": "song.rpp"}})
        folder = json.dumps({"status": "available", "directory_id": "d1", "entries": [
            {"path": "lyrics", "kind": "folder"}, {"path": "lyrics/bridge.txt", "kind": "file"}, {"path": "long-log.txt", "kind": "file"}]})
        read = lambda where, text, key, **extra: json.dumps(dict({"status": "available", "content_read": True, "where": where,
                                                                  "content": text, "cite_as": key, "coverage": "full"}, **extra))
        lyrics = read("Linked folder “Assets” · lyrics/bridge.txt", "The bridge is in D minor.\nSecond line.", "[S1]")
        ask = "Summarize the lyrics file in Assets and the artwork attachment"
        self.assertEqual(demo.file_step(ask, [], True)["tool"], "assistant_workspace_files")
        # The folder the user named, not simply the first one linked.
        self.assertEqual(demo.file_step(ask, [listing], True)["arguments"], {"directory_id": "d1"})
        # No folder named: none is opened in its place; the attachment is still read.
        self.assertEqual(demo.file_step("Summarize the lyrics file", [listing], True)["arguments"], {"attachment_id": "a2"})
        self.assertEqual(demo.file_step(ask, [listing, folder], True)["arguments"], {"directory_id": "d1", "path": "lyrics/bridge.txt"})
        # The audio attachment is skipped by its listed readability, never opened.
        self.assertEqual(demo.file_step(ask, [listing, folder, lyrics], True)["arguments"], {"attachment_id": "a2"})
        art = read("Workspace attachment", "Artwork is due on the 12th.", "[S2]")
        answer = demo.file_step(ask, [listing, folder, lyrics, art], False)["answer"]
        self.assertIn("“The bridge is in D minor.” [S1]", answer)
        self.assertIn("From Workspace attachment: “Artwork is due on the 12th.” [S2]", answer)
        self.assertNotIn("Second line", answer)
        # A long file is continued once, and a partial read is said to be partial.
        part = read("Linked folder “Assets” · long-log.txt", "Log line 1\n", "[S1]", coverage="partial", start=0, end=40000, total=115000, next_offset=40000)
        long_ask = "Read the long log"
        self.assertEqual(demo.file_step(long_ask, [listing, folder, part], True)["arguments"],
                         {"directory_id": "d1", "path": "long-log.txt", "offset": 40000})
        more = read("Linked folder “Assets” · long-log.txt", "og line 9\n", "[S1]", coverage="partial", start=40000, end=58000, total=115000, next_offset=58000)
        long_answer = demo.file_step(long_ask, [listing, folder, part, more], False)["answer"]
        self.assertIn("“Log line 1” [S1] (only characters 1–58000 of 115000 were read, not the whole file)", long_answer)
        self.assertEqual(long_answer.count("[S1]"), 1)
        self.assertEqual(demo.file_step("Read the project file", [listing], True)["arguments"], {"project_file": True})
        # A refusal ends the walk and is reported; nothing is invented.
        refused = json.dumps({"status": "unavailable", "reason": "folder_not_linked_to_this_workspace", "content_read": False})
        self.assertIn("folder_not_linked_to_this_workspace", demo.file_step(ask, [listing, refused], True)["answer"])
        empty = json.dumps({"status": "empty", "attachments": [], "linked_folders": [], "content_read": False})
        self.assertIn("no matching file is readable in this workspace", demo.file_step(ask, [empty], True)["answer"])
        self.assertIsNone(demo.file_step("Hello there", [], True))

    def test_provider_audit_counts_requests_and_marker_indexes_without_storing_input(self):
        with tempfile.TemporaryDirectory(prefix="ori-awareness-provider.") as temp:
            state = Path(temp)
            demo.audit_provider_input(state, "an ordinary request")
            (state / "sentinels.json").write_text(json.dumps(["UNLINKED_BODY", "", "OTHER_BODY"]))
            demo.audit_provider_input(state, "a request that carries OTHER_BODY in a tool result")
            demo.audit_provider_input(state, "OTHER_BODY again")
            saved = (state / "provider-audit.json").read_text()
            self.assertEqual(json.loads(saved), {"requests": 3, "hits": [2]})
            self.assertNotIn("tool result", saved)
            self.assertNotIn("OTHER_BODY", saved)

    def test_server_log_audit_reports_marker_indexes_and_never_copies_a_line(self):
        with tempfile.TemporaryDirectory(prefix="ori-awareness-provider.") as temp:
            state = Path(temp)
            log = state / "server.log"
            log.write_text("started\nHome assistant workspace context | reader_calls=2\nfile uploaded | filename=a.md\n")
            self.assertEqual(demo.audit_server_log(log, state), {"lines": 3, "watched": 0, "diagnostics": 1, "hits": []})
            (state / "sentinels.json").write_text(json.dumps(["UNLINKED_BODY"]))
            (state / "log-watch.json").write_text(json.dumps(["READ_BODY", "", "/sandbox/Music/Assets"]))
            self.assertEqual(demo.audit_server_log(log, state)["hits"], [])
            with log.open("a") as more:
                more.write("reader failed | path=/sandbox/Music/Assets/lyrics/a.txt\n")
            audit = demo.audit_server_log(log, state)
            self.assertEqual((audit["watched"], audit["hits"]), (3, [2]))
            self.assertNotIn("Assets", json.dumps(audit))

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
