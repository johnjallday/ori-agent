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


def current_turn(messages):
    """The current user message and the reader results Ori returned after it.

    History never identifies the turn: the request must end with the user's
    message or with a tool result that followed it.
    """
    if not messages or messages[-1].get("role") not in ("user", "tool"):
        raise ValueError("fixture requires the current user turn")
    index = max(i for i, message in enumerate(messages) if message.get("role") == "user")
    following = messages[index + 1:]
    if any(message.get("role") not in ("assistant", "tool") for message in following):
        raise ValueError("fixture requires one current turn")
    return messages[index], [m.get("content", "") for m in following if m.get("role") == "tool"]


def workspace_projection(messages):
    """Only the current user message can identify this fixture turn."""
    user, _results = current_turn(messages)
    match = re.search(r"<workspace_turn>(.*?)</workspace_turn>", user["content"], re.S)
    if not match:
        raise ValueError("fixture requires the production workspace projection")
    projection = json.loads(match.group(1))
    if projection.get("status") != "available":
        raise ValueError("fixture scope unavailable")
    return projection


def reader_step(prompt, results, tools_offered):
    """A deterministic stand-in for a tool-using model, for the sources demo.

    It decides only from the user's own words and from what Ori's readers
    returned in this turn. It holds no workspace data, so anything it says about
    a note or a task came through a real reader. Returns a tool call, an answer,
    or None when the request is not one of the demo's reading requests.
    """
    words = prompt.lower()

    def parsed(text):
        try:
            value = json.loads(text)
            return value if isinstance(value, dict) else {}
        except ValueError:
            return {}

    def call(name, **arguments):
        return {"tool": name, "arguments": arguments}

    have = [parsed(text) for text in results]
    if "missing plan" in words:
        if not have and tools_offered:
            return call("assistant_workspace_note", title="Missing plan")
        reason = have[0].get("reason", "unavailable") if have else "readers unavailable"
        return {"answer": "I could not read a note titled Missing plan (" + str(reason) + "). "
                          "Nothing was read, so I am not saying this workspace has no such plan."}
    if "release note" not in words:
        return None
    notes = next((r for r in have if "notes" in r), None)
    note = next((r for r in have if r.get("content_read") and "note_id" in r), None)
    tasks = next((r for r in have if "tasks" in r), None)
    task = next((r for r in have if r.get("content_read") and "task_id" in r), None)
    reasons = [str(r.get("reason", "")) for r in have]
    if tools_offered:
        if not have:
            return call("assistant_workspace_notes")
        if notes and not note and not any(r.startswith(("note_", "several_", "evidence_")) for r in reasons):
            listed = next((n for n in notes.get("notes", []) if "release" in str(n.get("title", "")).lower()), None)
            if listed:
                return call("assistant_workspace_note", note_id=listed["note_id"])
        if tasks is None and "workspace_unavailable" not in reasons:
            return call("assistant_workspace_tasks")
        if tasks and not task and not any(r.startswith("task_") for r in reasons):
            open_task = next((t for t in tasks.get("tasks", []) if t.get("state") not in ("done", "cancelled")), None)
            if open_task:
                return call("assistant_workspace_task", task_id=open_task["task_id"])
    parts = []
    if note:
        parts.append("Recorded in your note “" + note["title"] + "”: " +
                     note["content"].split(".")[0].strip() + ". " + note["cite_as"])
    else:
        parts.append("I could not read a release note here, so I am not relying on one.")
    if task:
        parts.append("Recorded: the task “" + task["title"] + "” is " + task["state_label"] + " " + task["cite_as"] + ".")
        parts.append("My suggestion, not a recorded fact: work on “" + task["title"] + "” next.")
    # A marker for something that was never read; Ori must remove it.
    parts.append("Not read this turn: [S9].")
    return {"answer": " ".join(parts)}


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
                user, results = current_turn(request["messages"])
                # Only the user's own words choose a demo; the overview Ori
                # appends (which lists note titles) never does.
                step = reader_step(user["content"].split("\n\n##", 1)[0], results, bool(request.get("tools")))
                if step:
                    message = {"role": "assistant", "content": step.get("answer", "")}
                    if "tool" in step:
                        message["tool_calls"] = [{"function": {"name": step["tool"], "arguments": step["arguments"]}}]
                    self.reply(200, {"model": MODEL, "message": message, "done": True,
                                     "prompt_eval_count": 1, "eval_count": 1})
                    return
                if "Hold this workspace reply" in user["content"]:
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
    parser.add_argument("--reaper-source")
    parser.add_argument("--music-source")
    parser.add_argument("--new-home", action="store_true", help="Confirm a declared new Home with exact staged candidates")
    parser.add_argument("--portfolio", action="store_true", help="Confirm a collection and library in a declared new Home")
    parser.add_argument("--placement", action="store_true",
                        help="wt demo: named review, blocked refresh, confirmed setup and project-local choice")
    parser.add_argument("--sources", action="store_true",
                        help="wt demo: note and task readers, checked sources, changed records and a missing note")
    args = parser.parse_args()
    if args.placement and args.sources:
        parser.error("choose one wt demo: --placement or --sources")
    if (args.placement or args.sources) and (args.reaper_source or args.music_source or args.new_home or args.portfolio):
        parser.error("--placement and --sources run on plain wt demo, without companion candidates")
    if bool(args.reaper_source) != bool(args.music_source):
        parser.error("candidate setup needs both exact companion sources")
    if (args.new_home or args.portfolio) and not args.reaper_source:
        parser.error("new-Home/portfolio acceptance needs both exact companion sources")
    if args.new_home and args.portfolio:
        parser.error("choose new-Home project or portfolio acceptance, not both")
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
        candidate = bool(args.reaper_source)
        if candidate:
            env.update(ORI_WORKSPACE_SETUP_ACCEPTANCE="1", ORI_WORKSPACE_PROVIDER_FIXTURE=str(state))
        env.pop("ORI_WORKSPACE_COLLECTION_ACCEPTANCE", None)
        if args.new_home or args.portfolio:
            env["ORI_WORKSPACE_NEW_HOME_ACCEPTANCE"] = "1"
            if args.portfolio:
                env["ORI_WORKSPACE_COLLECTION_ACCEPTANCE"] = "1"
        else:
            env.pop("ORI_WORKSPACE_NEW_HOME_ACCEPTANCE", None)
        log = evidence / ("group3-portfolio-candidate.log" if args.portfolio else
                          "group3-new-home-candidate.log" if args.new_home else
                          "group3-confirmed-candidate.log" if candidate else
                          "group3-wt-demo-placement.log" if args.placement else
                          "group4-wt-demo-sources.log" if args.sources else "group2-wt-demo.log")
        spec, sandbox_env = (("tests/personal-assistant-workspace-placement.spec.ts", "ORI_WORKSPACE_PLACEMENT_SANDBOX")
                             if args.placement else
                             ("tests/personal-assistant-workspace-sources.spec.ts", "ORI_WORKSPACE_SOURCES_SANDBOX")
                             if args.sources else
                             ("tests/personal-assistant-workspace-history.spec.ts", "ORI_WORKSPACE_HISTORY_SANDBOX"))
        process = None
        sandbox = None
        try:
            with log.open("w") as output:
                command = ([str(ROOT / "scripts/reaper-demo.sh"), "test", "--suite", "awareness",
                            "--reaper-source", args.reaper_source, "--music-source", args.music_source,
                            "--port", str(args.port)] if candidate else
                           ["zsh", "-c", 'trap "" INT; source "$1"; wt demo "$2"', "--",
                            str(ROOT / "scripts/wt.sh"), str(args.port)])
                process = subprocess.Popen(
                    command,
                    cwd=ROOT, env=env, stdout=output, stderr=subprocess.STDOUT,
                    start_new_session=True,
                )
                if candidate:
                    return process.wait()
                sandbox = wait_for_demo(process, log, args.port)
                env.update(PLAYWRIGHT_BASE_URL=f"http://127.0.0.1:{args.port}",
                           ORI_WORKSPACE_PROVIDER_FIXTURE=str(state))
                env[sandbox_env] = str(sandbox)
                result = subprocess.run(
                    ["npx", "playwright", "test", spec, "--workers=1"], cwd=ROOT, env=env, check=False,
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
