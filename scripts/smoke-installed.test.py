#!/usr/bin/env python3
"""Exercise the installed-server probe without starting Ori or using real data."""

import contextlib
import importlib.util
import io
import os
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import patch


spec = importlib.util.spec_from_file_location("smoke_installed", Path(__file__).with_name("smoke-installed.py"))
smoke = importlib.util.module_from_spec(spec)
spec.loader.exec_module(smoke)

SERVER = '''
import http.server, json, os, pathlib, sys
mode, record = sys.argv[1:3]
port = int(sys.argv[-1].split('=')[1])
pathlib.Path(record).write_text(json.dumps(dict(cwd=os.getcwd(), pid=os.getpid(), env=dict(os.environ))))
if mode == 'exit':
    raise SystemExit(9)
class Handler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(200)
        self.end_headers()
        body = {'status': 'bad' if mode == 'unhealthy' else 'ok'} if self.path == '/health' else {'version': '0.0.1' if mode == 'wrong' else '1.2.4-rc.1'}
        self.wfile.write(json.dumps(body).encode())
    def log_message(self, *args):
        pass
http.server.HTTPServer(('127.0.0.1', port), Handler).serve_forever()
'''


class ProbeTests(unittest.TestCase):
    def test_success_and_failures_cleanup_and_isolate_state(self):
        import json
        for mode in ("healthy", "unhealthy", "wrong", "exit"):
            with self.subTest(mode=mode), tempfile.TemporaryDirectory(prefix="ori-probe-test-") as temp:
                script = Path(temp) / "server.py"
                script.write_text(SERVER)
                record = Path(temp) / "record.json"
                command = [sys.executable, str(script), mode, str(record)]
                with patch.dict(os.environ, {"GH_TOKEN": "must-not-leak", "OPENAI_API_KEY": "must-not-leak",
                                             "ORI_DATA_DIR": "/never-use-this-live-profile"}), \
                        contextlib.redirect_stdout(io.StringIO()), contextlib.redirect_stderr(io.StringIO()):
                    if mode == "healthy":
                        smoke.probe(command, "v1.2.4-rc.1", timeout=3)
                    else:
                        with self.assertRaises(RuntimeError):
                            smoke.probe(command, "v1.2.4-rc.1", timeout=3)
                child = json.loads(record.read_text())
                self.assertFalse(Path(child["cwd"]).exists(), "temporary profile not cleaned")
                self.assertNotIn("GH_TOKEN", child["env"])
                self.assertNotIn("OPENAI_API_KEY", child["env"])
                self.assertNotEqual(child["env"]["ORI_DATA_DIR"], "/never-use-this-live-profile")
                if os.name != "nt":
                    with self.assertRaises(ProcessLookupError):
                        os.kill(child["pid"], 0)


if __name__ == "__main__":
    unittest.main()
