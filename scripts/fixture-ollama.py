#!/usr/bin/env python3
"""fixture-ollama.py - a recording, Ollama-compatible fixture model.

Point a sandboxed Ori server at it with OLLAMA_BASE_URL and pick the `ollama`
provider with the model `fixture-model`. Every /api/chat request body is
appended to --log as one JSON line, so a demo can show exactly what reached
the model (for example, that the workspace goal is in the prompt).

Replies:
  * A goal (mission) run - any message containing "--- MISSION CONTEXT ---" -
    gets the contents of --mission-response, re-read on every request so a
    demo can swap the fixture between runs. Default: {"items": []}.
    --mission-delay holds that answer for N seconds so a demo can see a run
    in flight.
  * Anything else gets --chat-response (re-read likewise), or a short reply.

The fixture never calls tools. Stdlib only; stop it by PID.

Usage:
  scripts/fixture-ollama.py --port 11500 --log "$SANDBOX/fixture-requests.jsonl" \
      [--mission-response path] [--mission-delay seconds] [--chat-response path]
"""

import argparse
import json
import sys
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

MODEL = "fixture-model:latest"
MISSION_MARKER = "--- MISSION CONTEXT ---"
DEFAULT_CHAT_REPLY = "Fixture model reply: I read this workspace's context."
DEFAULT_MISSION_REPLY = '{"items": []}'


def read_text(path, fallback):
    if not path:
        return fallback
    try:
        with open(path, encoding="utf-8") as handle:
            return handle.read()
    except OSError:
        return fallback


class Fixture(BaseHTTPRequestHandler):
    server_version = "fixture-ollama/1"
    log_lock = threading.Lock()

    def log_message(self, fmt, *args):  # quiet access log
        sys.stderr.write("fixture-ollama: " + (fmt % args) + "\n")

    def send_json(self, payload, status=200):
        body = json.dumps(payload).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        if self.path.startswith("/api/tags"):
            self.send_json({"models": [{"name": MODEL, "model": MODEL, "size": 1,
                                        "details": {"family": "fixture", "parameter_size": "1B"}}]})
        elif self.path.startswith("/api/version"):
            self.send_json({"version": "0.0.0-fixture"})
        elif self.path in ("/", ""):
            self.send_response(200)
            self.end_headers()
            self.wfile.write(b"Ollama is running")
        else:
            self.send_json({"error": "not found"}, 404)

    def do_POST(self):
        length = int(self.headers.get("Content-Length") or 0)
        raw = self.rfile.read(length) if length else b"{}"
        try:
            request = json.loads(raw or b"{}")
        except json.JSONDecodeError:
            self.send_json({"error": "invalid json"}, 400)
            return
        if self.path.startswith("/api/show"):
            self.send_json({"model_info": {"general.architecture": "fixture", "fixture.context_length": 32768},
                            "capabilities": ["completion", "tools"]})
            return
        if not self.path.startswith("/api/chat"):
            self.send_json({"error": "not found"}, 404)
            return

        messages = request.get("messages") or []
        text = "\n".join(str(m.get("content") or "") for m in messages)
        mission = MISSION_MARKER in text
        reply = (read_text(self.server.mission_response, DEFAULT_MISSION_REPLY) if mission
                 else read_text(self.server.chat_response, DEFAULT_CHAT_REPLY)).strip()

        with Fixture.log_lock:
            with open(self.server.log_path, "a", encoding="utf-8") as handle:
                handle.write(json.dumps({"at": time.strftime("%Y-%m-%dT%H:%M:%S"), "kind": "mission" if mission else "chat",
                                         "model": request.get("model"), "stream": bool(request.get("stream")),
                                         "messages": messages}) + "\n")

        if mission and self.server.mission_delay > 0:
            time.sleep(self.server.mission_delay)

        done = {"model": request.get("model") or MODEL, "created_at": time.strftime("%Y-%m-%dT%H:%M:%SZ"),
                "done": True, "done_reason": "stop", "prompt_eval_count": 1, "eval_count": 1}
        if request.get("stream"):
            self.send_response(200)
            self.send_header("Content-Type", "application/x-ndjson")
            self.end_headers()
            chunk = dict(done, done=False, message={"role": "assistant", "content": reply})
            chunk.pop("done_reason", None)
            self.wfile.write((json.dumps(chunk) + "\n").encode("utf-8"))
            self.wfile.write((json.dumps(dict(done, message={"role": "assistant", "content": ""})) + "\n").encode("utf-8"))
            return
        self.send_json(dict(done, message={"role": "assistant", "content": reply}))


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--port", type=int, default=11500)
    parser.add_argument("--log", required=True, help="JSONL file that records every /api/chat request")
    parser.add_argument("--mission-response", default="", help="file whose contents answer goal (mission) runs")
    parser.add_argument("--mission-delay", type=float, default=0, help="seconds to hold each goal-run answer")
    parser.add_argument("--chat-response", default="", help="file whose contents answer everything else")
    args = parser.parse_args()

    server = ThreadingHTTPServer(("127.0.0.1", args.port), Fixture)
    server.log_path = args.log
    server.mission_response = args.mission_response
    server.mission_delay = args.mission_delay
    server.chat_response = args.chat_response
    print(f"FIXTURE_OLLAMA=http://127.0.0.1:{args.port} model={MODEL} log={args.log}", flush=True)
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        pass


if __name__ == "__main__":
    main()
