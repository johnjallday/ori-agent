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
