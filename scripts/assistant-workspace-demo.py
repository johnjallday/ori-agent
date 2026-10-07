#!/usr/bin/env python3
"""Drive the real wt demo host with a loopback-only deterministic Ollama fixture.

No vendor model/authentication, plugin install, native execution or real user
state. The fixture proves browser/provider/persistence wiring, not model quality.
The process group and temporary state belong exclusively to this invocation.
"""

import argparse
import json
import os
from pathlib import Path
import re
import signal
import shutil
import subprocess
import tempfile
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.error import URLError
from urllib.request import urlopen

ROOT = Path(__file__).resolve().parent.parent
MODEL = "ori-workspace-fixture"


def workspace_projection(messages):
    """Only the current final user message can identify this fixture turn."""
    if not messages or messages[-1].get("role") != "user":
        raise ValueError("fixture requires the current user turn")
    match = re.search(r"<workspace_turn>(.*?)</workspace_turn>", messages[-1]["content"], re.S)
    if not match:
        raise ValueError("fixture requires the production workspace projection")
    projection = json.loads(match.group(1))
    if projection.get("status") != "available":
        raise ValueError("fixture scope unavailable")
    return projection


def provider_handler(state_dir):
    class Provider(BaseHTTPRequestHandler):
        def log_message(self, *_args):
            pass  # Never print prompt bodies, paths or credentials.

        def reply(self, status, value):
            body = json.dumps(value).encode()
            self.send_response(status)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

        def do_GET(self):
            if self.path == "/api/tags":
                self.reply(200, {"models": [{"name": MODEL}]})
            else:
                self.reply(200, {"fixture": "loopback-only; no vendor model"})

        def do_POST(self):
            if self.path != "/api/chat":
                self.reply(404, {"error": "fixture endpoint unavailable"})
                return
            try:
                size = int(self.headers.get("Content-Length", "0"))
                if not 0 < size <= 1_000_000:
                    raise ValueError("fixture input bound")
                request = json.loads(self.rfile.read(size))
                projection = workspace_projection(request.get("messages", []))
                subject = projection.get("subject") or {}
                if "Hold this workspace reply" in request["messages"][-1]["content"]:
                    # Metadata only; do not retain source bodies or model input.
                    accepted = {"subject_id": subject.get("id"), "subject_name": subject.get("name")}
                    (state_dir / "accepted.json").write_text(json.dumps(accepted))
                    deadline = time.monotonic() + 60
                    while not (state_dir / "release").exists():
                        if time.monotonic() >= deadline:
                            raise ValueError("fixture hold timed out")
                        time.sleep(0.02)
                self.reply(200, {
                    "model": MODEL,
                    "message": {"role": "assistant", "content":
                        "Fixture reply for " + subject.get("name", "App-wide") + ". Metadata only."},
                    "done": True,
                    "prompt_eval_count": 1,
                    "eval_count": 1,
                })
            except (ValueError, KeyError, TypeError):
                self.reply(400, {"error": "fixture requires a validated workspace turn"})

    return Provider


def wait_for_demo(process, log, port):
    deadline = time.monotonic() + 180
    while time.monotonic() < deadline:
        if process.poll() is not None:
            raise RuntimeError("wt demo exited before becoming ready")
        text = log.read_text()
        match = re.search(r"Demo sandbox: (\S+)", text)
        if match:
            sandbox = Path(match.group(1))
            if not sandbox.name.startswith("ori-demo."):
                raise RuntimeError("unexpected demo sandbox")
            try:
                with urlopen(f"http://127.0.0.1:{port}/api/personal-assistant", timeout=1) as response:
                    if response.status == 200:
                        return sandbox
            except (URLError, TimeoutError):
                pass
        time.sleep(0.1)
    raise RuntimeError("wt demo did not become ready")


def main():
    os.umask(0o077)
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--port", type=int, default=8954)
    args = parser.parse_args()
    if not 1024 <= args.port <= 65535:
        parser.error("port must be between 1024 and 65535")
    evidence = ROOT / "tasks/evidence-assistant-workspace-awareness"
    evidence.mkdir(parents=True, exist_ok=True, mode=0o750)
    with tempfile.TemporaryDirectory(prefix="ori-awareness-provider.") as temp:
        state = Path(temp)
        provider = ThreadingHTTPServer(("127.0.0.1", 0), provider_handler(state))
        thread = threading.Thread(target=provider.serve_forever, daemon=True)
        thread.start()
        # Child-only isolation. Do not read or alter user credential files.
        env = {key: value for key, value in os.environ.items()
               if not key.endswith("_API_KEY") and key not in {
                   "CODEX_HOME", "AGENT_STORE_PATH", "ORI_KEEP_DEMO_SANDBOX", "ORI_DEMO_OPEN"}}
        env.update(ORI_DEMO_NO_CODEX="1", ORI_DEMO_OPEN="0",
                   OLLAMA_BASE_URL=f"http://127.0.0.1:{provider.server_port}")
        log = evidence / "group2-wt-demo.log"
        process = None
        sandbox = None
        try:
            with log.open("w") as output:
                process = subprocess.Popen(
                    ["zsh", "-c", 'trap "" INT; source "$1"; wt demo "$2"', "--",
                     str(ROOT / "scripts/wt.sh"), str(args.port)],
                    cwd=ROOT, env=env, stdout=output, stderr=subprocess.STDOUT,
                    start_new_session=True,
                )
                sandbox = wait_for_demo(process, log, args.port)
                env.update(PLAYWRIGHT_BASE_URL=f"http://127.0.0.1:{args.port}",
                           ORI_WORKSPACE_HISTORY_SANDBOX=str(sandbox),
                           ORI_WORKSPACE_PROVIDER_FIXTURE=str(state))
                result = subprocess.run(
                    ["npx", "playwright", "test", "tests/personal-assistant-workspace-history.spec.ts",
                     "--workers=1"], cwd=ROOT, env=env, check=False,
                )
                return result.returncode
        finally:
            # Release only our hold, then stop only our process group. wt demo's
            # own finally block removes its sandbox; never remove an external HOME.
            (state / "release").touch()
            if process and process.poll() is None:
                os.killpg(process.pid, signal.SIGINT)
                try:
                    process.wait(timeout=15)
                except subprocess.TimeoutExpired:
                    os.killpg(process.pid, signal.SIGTERM)
                    process.wait(timeout=10)
            provider.shutdown()
            provider.server_close()
            thread.join(timeout=5)
            # A forced shell termination can skip zsh's always block. Restrict
            # fallback cleanup to this invocation's verified temporary sandbox.
            if sandbox and sandbox.exists():
                if sandbox.parent.resolve() != Path(tempfile.gettempdir()).resolve() or not sandbox.name.startswith("ori-demo."):
                    raise RuntimeError("refusing unexpected sandbox cleanup")
                shutil.rmtree(sandbox)


if __name__ == "__main__":
    raise SystemExit(main())
