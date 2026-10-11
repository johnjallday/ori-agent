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
import sys
import tempfile
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.error import URLError
from urllib.request import urlopen

ROOT = Path(__file__).resolve().parent.parent
MODEL = "ori-workspace-fixture"

# The runs on plain wt demo, by flag: the spec, the variable that hands it the
# sandbox path, and the log kept with the evidence. No flag runs DEFAULT_DEMO.
PLAIN_DEMOS = {
    "discovery_interface": ("tests/personal-assistant-discovery-interface.spec.ts",
                            "ORI_DISCOVERY_INTERFACE_SANDBOX", "discovery-interface.log"),
    "discovery_continuity": ("tests/personal-assistant-discovery-continuity.spec.ts",
                             "ORI_DISCOVERY_CONTINUITY_SANDBOX", "discovery-continuity.log"),
    "discovery_research": ("tests/personal-assistant-discovery.spec.ts",
                           "ORI_DISCOVERY_RESEARCH_SANDBOX", "discovery-research.log"),
    "discovery_discussion": ("tests/personal-assistant-discovery-discussion.spec.ts",
                             "ORI_DISCOVERY_DISCUSSION_SANDBOX", "discovery-discussion.log"),
    "conversation_first": ("tests/personal-assistant-conversation-first.spec.ts",
                           "ORI_WORKSPACE_CONVERSATIONFIRST_SANDBOX", "conversation-first.log"),
    "folder_response": ("tests/personal-assistant-folder-response.spec.ts",
                        "ORI_WORKSPACE_FOLDERRESPONSE_SANDBOX", "folder-response.log"),
    "folder_response_baseline": ("tests/personal-assistant-folder-response-prototype.spec.ts",
                                 "ORI_WORKSPACE_FOLDERBASELINE_SANDBOX", "folder-response-baseline.log"),
    "placement": ("tests/personal-assistant-workspace-placement.spec.ts",
                  "ORI_WORKSPACE_PLACEMENT_SANDBOX", "group3-wt-demo-placement.log"),
    "sources": ("tests/personal-assistant-workspace-sources.spec.ts",
                "ORI_WORKSPACE_SOURCES_SANDBOX", "group4-wt-demo-sources.log"),
    "files": ("tests/personal-assistant-workspace-files.spec.ts",
              "ORI_WORKSPACE_FILES_SANDBOX", "group5-wt-demo-files.log"),
    "accessibility": ("tests/personal-assistant-workspace-accessibility.spec.ts",
                      "ORI_WORKSPACE_ACCESSIBILITY_SANDBOX", "group6-wt-demo-accessibility.log"),
    "integrated": ("tests/personal-assistant-workspace-integrated.spec.ts",
                   "ORI_WORKSPACE_INTEGRATED_SANDBOX", "group6-wt-demo-integrated.log"),
    "slow_reply": ("tests/personal-assistant-workspace-slow-reply.spec.ts",
                   "ORI_WORKSPACE_SLOW_SANDBOX", "group6-wt-demo-slow-reply.log"),
}
DEFAULT_DEMO = ("tests/personal-assistant-workspace-history.spec.ts",
                "ORI_WORKSPACE_HISTORY_SANDBOX", "group2-wt-demo.log")


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


def file_step(prompt, results, tools_offered):
    """The same kind of stand-in for the file readers demo.

    It walks listing -> folder -> file exactly as Ori's readers allow, and its
    answer quotes only text a reader returned. Requests it recognizes: the
    lyrics file (plus one attachment), the long log (two parts) and the project
    file. Returns None for anything else.
    """
    words = prompt.lower()
    wants_project, wants_long, wants_lyrics = "project file" in words, "long log" in words, "lyrics" in words
    if not (wants_project or wants_long or wants_lyrics):
        return None

    def parsed(text):
        try:
            value = json.loads(text)
            return value if isinstance(value, dict) else {}
        except ValueError:
            return {}

    def call(name, **arguments):
        return {"tool": name, "arguments": arguments}

    have = [parsed(text) for text in results]
    listing = next((r for r in have if "linked_folders" in r or "attachments" in r), None)
    folder = next((r for r in have if "entries" in r), None)
    reads = [r for r in have if r.get("content_read")]
    refused = [str(r.get("reason")) for r in have if r.get("content_read") is False and r.get("reason")]
    if tools_offered and not refused:
        if listing is None:
            return call("assistant_workspace_files")
        if wants_project:
            if listing.get("project_file") and not reads:
                return call("assistant_workspace_file", project_file=True)
        else:
            # A workspace can link several folders (its own folder is one). Only
            # the one the user named is opened; an unnamed or no-longer-linked
            # folder is not replaced by a guess at another.
            named = next((f for f in listing.get("linked_folders") or []
                          if f.get("name") and str(f["name"]).lower() in words), None)
            if named and folder is None:
                return call("assistant_workspace_folder", directory_id=named["directory_id"])
            wanted = "log" if wants_long else "lyrics"
            entry = next((e for e in (folder or {}).get("entries", [])
                          if e.get("kind") == "file" and wanted in str(e.get("path", "")).lower()), None)
            file_reads = [r for r in reads if str(r.get("where", "")).startswith("Linked folder")]
            if entry and not file_reads:
                return call("assistant_workspace_file", directory_id=folder["directory_id"], path=entry["path"])
            if wants_long and len(file_reads) == 1 and "next_offset" in file_reads[0]:
                return call("assistant_workspace_file", directory_id=folder["directory_id"], path=entry["path"],
                            offset=file_reads[0]["next_offset"])
            attachment = next((a for a in listing.get("attachments", []) if a.get("readable") == "supported"), None)
            if wants_lyrics and attachment and not any(r.get("where") == "Workspace attachment" for r in reads):
                return call("assistant_workspace_file", attachment_id=attachment["attachment_id"])
    parts = []
    # Parts of one file share its source key: say once what was read of it, and
    # take coverage from the latest part, which reports the turn so far.
    by_source = {}
    for read in reads:
        by_source.setdefault(read["cite_as"], []).append(read)
    for key, group in by_source.items():
        first, latest = group[0], group[-1]
        line = first["content"].strip().split("\n")[0]
        part = "From " + first["where"] + ": “" + line + "” " + key
        if latest.get("coverage") == "partial":
            part += " (only characters " + str(min(r["start"] for r in group) + 1) + "–" + \
                    str(max(r["end"] for r in group)) + " of " + str(latest["total"]) + \
                    " were read, not the whole file)"
        parts.append(part + ".")
    if not reads:
        why = ", ".join(refused) if refused else ("no matching file is readable in this workspace" if listing else "readers unavailable")
        parts.append("I could not read a file for that (" + why + "). I am not describing a file I did not read.")
    return {"answer": " ".join(parts)}


_audit_lock = threading.Lock()


def audit_provider_input(state_dir, text):
    """Count provider requests and note which watched markers they carried.

    A spec lists markers that must never reach a model (for example the body of
    a file in a folder that was only attached) in sentinels.json. This records
    how many requests arrived and the index of any marker found. It never stores
    a request or a marker's surroundings.
    """
    with _audit_lock:
        audit = state_dir / "provider-audit.json"
        seen = json.loads(audit.read_text()) if audit.exists() else {"requests": 0, "hits": []}
        seen["requests"] += 1
        watched = state_dir / "sentinels.json"
        if watched.exists():
            for index, marker in enumerate(json.loads(watched.read_text())):
                if isinstance(marker, str) and marker and marker in text and index not in seen["hits"]:
                    seen["hits"].append(index)
        audit.write_text(json.dumps(seen))


def audit_server_log(log, state_dir):
    """Report which watched markers Ori's own log holds, without copying a line.

    Watched markers are the provider's sentinels plus whatever a spec lists in
    log-watch.json: bodies it had Ori read, and where a linked folder lives.
    Reading a source is allowed; writing it into a log is not. The count of
    workspace-context diagnostics lines shows the log was the real server's.
    """
    watched = []
    for name in ("sentinels.json", "log-watch.json"):
        try:
            watched += [marker for marker in json.loads((state_dir / name).read_text())
                        if isinstance(marker, str) and marker]
        except (OSError, ValueError):
            pass
    text = log.read_text(errors="replace")
    return {"lines": text.count("\n"), "watched": len(watched),
            "diagnostics": text.count("Home assistant workspace context"),
            "hits": [index for index, marker in enumerate(watched) if marker in text]}


def trace_reader_turn(state_dir, results, offered, step):
    """Append one sanitized line per request that used, or could use, a reader.

    It records which reader the stand-in chose and, for each result Ori
    returned, only its status, reason, whether content was read and how many
    rows it listed. No content, title, name or path is written.
    """
    if not results and not (step and "tool" in step):
        return
    outcomes = []
    for text in results:
        try:
            value = json.loads(text)
        except ValueError:
            value = None
        if not isinstance(value, dict):
            outcomes.append({"status": "tool_error"})
            continue
        outcome = {"status": value.get("status"), "content_read": value.get("content_read")}
        if value.get("reason"):
            outcome["reason"] = value["reason"]
        for rows in ("notes", "tasks", "attachments", "linked_folders", "entries"):
            if isinstance(value.get(rows), list):
                outcome[rows] = len(value[rows])
        outcomes.append(outcome)
    line = {"readers_offered": offered, "results": outcomes,
            "chose": step.get("tool", "answer") if step else "plain_reply"}
    with _audit_lock:
        with (state_dir / "provider-trace.jsonl").open("a") as trace:
            trace.write(json.dumps(line) + "\n")


def folder_response_step(content):
    """Scripted metadata interpretation, not live-model concision evidence."""
    match = re.search(r"<folder_observation>(.*?)</folder_observation>", content, re.S)
    if not match:
        return None
    observation = json.loads(match.group(1))
    if observation.get("version", 1) != 1 or not isinstance(observation.get("folder"), str):
        raise ValueError("fixture requires typed folder metadata")
    return {"answer": "Fixture interpretation: The names and markers suggest projects alongside shared material. "
                      "They do not establish contents or progress. What would you like to work out first?"}


def conversation_reply(prompt):
    """Explicit synthetic scenario only; never infer it from workspace metadata."""
    if prompt in {
        "Should we build a community platform for musicians?",
        "No, I don't want to develop anyone's talent. Let's compare community ideas.",
        "Could Ori help people build communities?",
        "Is there a Telegram skill?",
    }:
        return ("Deterministic discussion fixture: compare community options, not talent coaching. "
                "No external catalog or document was read and no setup was performed.")
    if prompt.startswith("Conversation fixture: long answer"):
        return "Beginning of the long fixture reply.\n\n" + "\n\n".join(
            f"Section {index + 1}. This is synthetic discussion, not a setup plan. "
            "Consider one next step at a time. No source contents were read and no resources were created."
            for index in range(20)
        ) + "\n\nEnd of the long fixture reply."
    return None


def evidence_directory(value):
    """Keep an explicit override inside this worktree's ignored evidence root."""
    path = (ROOT / value).resolve()
    root = (ROOT / "tasks/evidence").resolve()
    if not root.is_relative_to(ROOT.resolve()) or not path.is_relative_to(root):
        raise argparse.ArgumentTypeError("evidence directory must be inside this worktree's tasks/evidence")
    return path


def browser_spec(value):
    """An explicit regression file, not a glob or an external script."""
    path = (ROOT / value).resolve()
    if not path.is_relative_to(ROOT / 'tests') or not path.is_file() or not path.name.endswith('.spec.ts'):
        raise argparse.ArgumentTypeError('extra spec must be an existing worktree-local tests/*.spec.ts file')
    return str(path.relative_to(ROOT))


def research_step(user, results, tools_offered):
    """Fixture decisions only; public reads still run through production owners."""
    content = user["content"]
    reference = re.search(r"<research_reference>(.*?)</research_reference>", content, re.S)
    if reference:
        result = json.loads(reference.group(1))
        candidates = result.get("candidates", [])
        names = [c.get("name", "Candidate") + (" [" + c["receipt"]["key"] + "]" if c.get("receipt", {}).get("key") else "") for c in candidates]
        return {"answer": "Deterministic research comparison fixture: " + str(result.get("availability", "unavailable")) + ". " + "; ".join(names) + ". Listings and bounded documents are not operational verification; grants, configuration and undeclared dependencies remain unknown. No installation ran."}
    if results:
        result = json.loads(results[-1])
        if result.get("status") == "review_required":
            return {"answer": "Deterministic research fixture: review the exact public lookup below. Nothing has been sent or installed."}
        candidates = result.get("candidates", [])
        return {"answer": "Deterministic cached-catalog fixture: " + "; ".join(c.get("name", "Candidate") + " [" + c.get("receipt", {}).get("key", "S999") + "]" for c in candidates) + ". Metadata only, not a tested integration."}
    if tools_offered and "public document" in content.lower():
        target = re.search(r"https?://[^\s<>]+", content)
        if target:
            return {"tool": "assistant_propose_research_lookup", "arguments": {"operation": "public_document", "url": target.group(0)}}
    if tools_offered and "telegram" in content.lower():
        return {"tool": "assistant_propose_research_lookup", "arguments": {"operation": "skills_catalog", "query": "Telegram community management"}}
    if tools_offered and "installed capability metadata" in content.lower():
        return {"tool": "assistant_installed_capabilities", "arguments": {"query": ""}}
    if tools_offered and "cached mcp" in content.lower():
        return {"tool": "assistant_mcp_catalog", "arguments": {"query": "" if "two cached mcp" in content.lower() else "filesystem"}}
    return None


def continuity_summary(request):
    """Fixture selection only: exact known sentences from host-owned sources."""
    if not any(m.get("role") == "system" and "Select a compact conversation recap" in m.get("content", "")
               for m in request.get("messages", [])):
        return None
    if request.get("tools"):
        raise ValueError("summary fixture must be tool-free")
    data = json.loads(request["messages"][-1]["content"])
    items = list((data.get("prior") or {}).get("items", []))
    wanted = [("user_goal", "My goal is community membership."),
              ("user_correction", "No, I do not want to develop anyone's talent."),
              ("user_correction", "No, I want recurring membership only, not one-off sales or talent coaching.")]
    for source in data.get("sources", []):
        if source.get("role") != "user" or source.get("imported"):
            continue
        for kind, quote in wanted:
            if quote in source.get("content", "") and not any(item.get("quote") == quote for item in items):
                items.append({"kind": kind, "message_id": source["id"], "quote": quote})
    return json.dumps({"version": 1, "items": items[:16]})


def continuity_answer(state_dir, messages):
    """Record bounded-input facts, not a fabricated pleasant-answer quality score."""
    text = "\n".join(m.get("content", "") for m in messages)
    recap_roles = [m.get("role") for m in messages if "<conversation_reference>" in m.get("content", "")]
    outcome = {"recap_present": bool(recap_roles), "recap_user_role": all(role == "user" for role in recap_roles),
               "early_correction_present": "No, I do not want to develop anyone's talent." in text,
               "latest_correction_present": "No, I want recurring membership only, not one-off sales or talent coaching." in text,
               "history_runes": sum(len(m.get("content", "")) for m in messages[1:-1])}
    (state_dir / "continuity-input.json").write_text(json.dumps(outcome))
    topic = ("community membership is present in the actual bounded input, not talent coaching. "
             if outcome["early_correction_present"] else "no early community correction is present in this input. ")
    history = "Historical references are bounded and not current source truth. " if outcome["recap_present"] else "No historical recap is being used. "
    return {"answer": "Deterministic continuity fixture: " + topic + history +
                      "No setup or remembered fact was saved. This is wiring evidence, not a reasoning-quality claim."}


def stop_owned_process(process):
    if process and process.poll() is None:
        os.killpg(process.pid, signal.SIGINT)
        try:
            process.wait(timeout=15)
        except subprocess.TimeoutExpired:
            os.killpg(process.pid, signal.SIGTERM)
            process.wait(timeout=10)


def provider_handler(state_dir, folder_response=False, discovery_research=False, discovery_continuity=False):
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
                raw = self.rfile.read(size)
                audit_provider_input(state_dir, raw.decode("utf-8", "replace"))
                request = json.loads(raw)
                if request.get("messages") and request["messages"][0].get("content", "").startswith("The current user explicitly requests an editable workspace proposal"):
                    history = request["messages"][1:-1]
                    body = "\n".join(message.get("content", "") for message in history)
                    (state_dir / "workspace-proposal-input.json").write_text(json.dumps({
                        "tool_free": not request.get("tools"),
                        "correction_present": "not talent coaching" in body,
                        "membership_present": "membership" in body,
                        "early_correction_present": "No, I do not want to develop anyone's talent." in body,
                        "latest_correction_present": "No, I want recurring membership only, not one-off sales or talent coaching." in body,
                        "history_runes": len(body),
                    }))
                    proposal = {"name": "Fictional Membership Pilot", "description": "Goal: recurring community membership, not talent coaching. Proposed starter work: identify the audience, compare membership workflows, and outline a small pilot. Telegram compatibility remains unknown; no integration is installed."}
                    self.reply(200, {"model": MODEL, "message": {"role": "assistant", "content": json.dumps(proposal)}, "done": True,
                                     "prompt_eval_count": 1, "eval_count": 1})
                    return
                summary = continuity_summary(request) if discovery_continuity else None
                if summary is not None:
                    self.reply(200, {"model": MODEL, "message": {"role": "assistant", "content": summary}, "done": True,
                                     "prompt_eval_count": 1, "eval_count": 1})
                    return
                projection = workspace_projection(request.get("messages", []))
                subject = projection.get("subject") or {}
                user, results = current_turn(request["messages"])
                # Only the user's own words choose a demo; the overview Ori
                # appends (which lists note titles) never does.
                own_words, offered = user["content"].split("\n\n##", 1)[0], bool(request.get("tools"))
                if own_words == "Suggest a useful next step for our membership plan.":
                    system = request["messages"][0].get("content", "")
                    (state_dir / "workspace-offer-input.json").write_text(json.dumps({
                        "optional_review_allowed": "may offer an optional workspace review even if" in system,
                        "decline_respected": "respect a decline" in system,
                    }))
                    self.reply(200, {"model": MODEL, "message": {"role": "assistant", "content": "Deterministic discussion fixture: a workspace could organize the agreed membership pilot. Would you like a workspace proposal? Nothing is created or installed."}, "done": True,
                                     "prompt_eval_count": 1, "eval_count": 1})
                    return
                step = ((research_step(user, results, offered) if discovery_research or discovery_continuity else None) or
                        (continuity_answer(state_dir, request["messages"]) if discovery_continuity else None) or
                        (folder_response_step(user["content"]) if folder_response else None) or
                        reader_step(own_words, results, offered) or file_step(own_words, results, offered))
                trace_reader_turn(state_dir, results, offered, step)
                # Hold generation, including folder replies, rather than mocking
                # the browser's HTTP response. Only our private state releases it.
                hold_next = state_dir / "hold-next"
                held = hold_next.exists()
                if held:
                    hold_next.unlink()
                if held or "Hold this workspace reply" in own_words:
                    accepted = {"subject_id": subject.get("id"), "subject_name": subject.get("name")}
                    (state_dir / "accepted.json").write_text(json.dumps(accepted))
                    deadline = time.monotonic() + 60
                    while not (state_dir / "release").exists():
                        if time.monotonic() >= deadline:
                            raise ValueError("fixture hold timed out")
                        time.sleep(0.02)
                scenario = None if discovery_research or discovery_continuity else conversation_reply(own_words)
                if scenario:
                    step = {"answer": scenario}
                if step:
                    message = {"role": "assistant", "content": step.get("answer", "")}
                    if "tool" in step:
                        message["tool_calls"] = [{"function": {"name": step["tool"], "arguments": step["arguments"]}}]
                    self.reply(200, {"model": MODEL, "message": message, "done": True,
                                     "prompt_eval_count": 1, "eval_count": 1})
                    return
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
    parser.add_argument("--extra-spec", action="append", type=browser_spec, default=[],
                        help="Also run an explicit worktree-local browser regression against this same isolated host")
    parser.add_argument("--evidence-dir", type=evidence_directory,
                        help="Keep runner logs and compatible captures inside this worktree's tasks/evidence")
    parser.add_argument("--discovery-interface", action="store_true",
                        help="wt demo: Help/draft transitions, candidate navigation and exact reviewed lookup, no integration install")
    parser.add_argument("--discovery-continuity", action="store_true",
                        help="wt demo: bounded long thread, exact recap and same-sandbox server restart, deterministic model only")
    parser.add_argument("--discovery-research", action="store_true",
                        help="wt demo: exact reviewed public catalog/document smoke, actual source owners, no install or live model")
    parser.add_argument("--discovery-discussion", action="store_true",
                        help="wt demo: exploratory discussion, same canonical thread and an unconfirmed setup review")
    parser.add_argument("--conversation-first", action="store_true",
                        help="wt demo: chronology, passive reply geometry, delayed generation and inline setup")
    parser.add_argument("--reaper-source")
    parser.add_argument("--music-source")
    parser.add_argument("--new-home", action="store_true", help="Confirm a declared new Home with exact staged candidates")
    parser.add_argument("--portfolio", action="store_true", help="Confirm a collection and library in a declared new Home")
    parser.add_argument("--placement", action="store_true",
                        help="wt demo: named review, blocked refresh, confirmed setup and project-local choice")
    parser.add_argument("--sources", action="store_true",
                        help="wt demo: note and task readers, checked sources, changed records and a missing note")
    parser.add_argument("--files", action="store_true",
                        help="wt demo: linked-file and attachment readers, partial reads, an unlinked folder and revoked access")
    parser.add_argument("--accessibility", action="store_true",
                        help="wt demo: keyboard use, context announcements, sources, focus return and narrow layouts")
    parser.add_argument("--integrated", action="store_true",
                        help="wt demo: one conversation from setup review through notes, files, a delayed reply, "
                             "removed access and confirmation")
    parser.add_argument("--slow-reply", action="store_true",
                        help="wt demo: a reply held longer than 30 seconds still arrives, and a failed request "
                             "is named after the hired assistant")
    parser.add_argument("--folder-response", action="store_true",
                        help="wt demo: compact real folder Send/replay with a scripted loopback reply")
    parser.add_argument("--folder-response-evidence-stage", choices=["group-3", "group-4", "final", "explorer"],
                        help="Keep folder-response captures in a distinct local evidence stage")
    parser.add_argument("--folder-response-baseline", action="store_true",
                        help="wt demo: built drawer controller baseline vs standalone synthetic prototype; no model")
    args = parser.parse_args()
    if args.extra_spec and (args.reaper_source or args.music_source):
        parser.error('--extra-spec supports only plain isolated host demos, not companion candidate runs')
    if args.folder_response_evidence_stage and not args.folder_response:
        parser.error("--folder-response-evidence-stage requires --folder-response")
    plain = ["--" + name.replace("_", "-") for name in PLAIN_DEMOS if getattr(args, name)]
    if len(plain) > 1:
        parser.error("choose one wt demo: " + " or ".join(plain))
    if plain and (args.reaper_source or args.music_source or args.new_home or args.portfolio):
        parser.error(plain[0] + " runs on plain wt demo, without companion candidates")
    if bool(args.reaper_source) != bool(args.music_source):
        parser.error("candidate setup needs both exact companion sources")
    if (args.new_home or args.portfolio) and not args.reaper_source:
        parser.error("new-Home/portfolio acceptance needs both exact companion sources")
    if args.new_home and args.portfolio:
        parser.error("choose new-Home project or portfolio acceptance, not both")
    if not 1024 <= args.port <= 65535:
        parser.error("port must be between 1024 and 65535")
    evidence = args.evidence_dir or (ROOT / "tasks/evidence/assistant-conversation-first/baseline" if args.conversation_first else
                ROOT / "tasks/evidence/assistant-folder-response-ux/group-1" if args.folder_response_baseline else
                ROOT / "tasks/evidence/assistant-folder-response-ux" / (args.folder_response_evidence_stage or "group-3") if args.folder_response else
                ROOT / "tasks/evidence-assistant-workspace-awareness")
    evidence.mkdir(parents=True, exist_ok=True, mode=0o750)
    with tempfile.TemporaryDirectory(prefix="ori-awareness-provider.") as temp:
        state = Path(temp)
        provider = ThreadingHTTPServer(("127.0.0.1", 0), provider_handler(state, folder_response=args.folder_response, discovery_research=args.discovery_research or args.discovery_interface, discovery_continuity=args.discovery_continuity))
        thread = threading.Thread(target=provider.serve_forever, daemon=True)
        thread.start()
        # Child-only isolation. Do not read or alter user credential files.
        env = {key: value for key, value in os.environ.items()
               if not key.endswith("_API_KEY") and key not in {
                   "CODEX_HOME", "AGENT_STORE_PATH", "ORI_KEEP_DEMO_SANDBOX", "ORI_DEMO_OPEN"}}
        env.update(ORI_DEMO_NO_CODEX="1", ORI_DEMO_OPEN="0",
                   ORI_ASSISTANT_EVIDENCE_DIR=str(evidence),
                   OLLAMA_BASE_URL=f"http://127.0.0.1:{provider.server_port}")
        if args.discovery_continuity:
            env.update(ORI_KEEP_DEMO_SANDBOX="1", ORI_DISCOVERY_CONTINUITY_PHASE="seed")
        if args.folder_response:
            env["ORI_FOLDER_RESPONSE_EVIDENCE_DIR"] = str(evidence)
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
        spec, sandbox_env, plain_log = PLAIN_DEMOS[plain[0][2:].replace("-", "_")] if plain else DEFAULT_DEMO
        log = evidence / ("group3-portfolio-candidate.log" if args.portfolio else
                          "group3-new-home-candidate.log" if args.new_home else
                          "group3-confirmed-candidate.log" if candidate else plain_log)
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
                    ["npx", "playwright", "test", spec, *args.extra_spec, "--workers=1"], cwd=ROOT, env=env, check=False,
                )
                if args.discovery_continuity and result.returncode == 0:
                    # Preserve only this invocation's verified wt sandbox, stop
                    # its owned process group, then restart the built production
                    # host with the checked-in isolation entrypoint and same data.
                    stop_owned_process(process)
                    process = subprocess.Popen([str(ROOT / "scripts/demo-server.sh"), str(args.port), str(sandbox)],
                                               cwd=ROOT, env=env, stdout=output, stderr=subprocess.STDOUT,
                                               start_new_session=True)
                    wait_for_demo(process, log, args.port)
                    env["ORI_DISCOVERY_CONTINUITY_PHASE"] = "reopen"
                    result = subprocess.run(["npx", "playwright", "test", spec, *args.extra_spec, "--workers=1"],
                                            cwd=ROOT, env=env, check=False)
                output.flush()
                audit = audit_server_log(log, state)
                (evidence / (log.stem + "-server-log-audit.json")).write_text(json.dumps(audit))
                if audit["hits"]:
                    print("server log holds watched marker(s):", audit["hits"], file=sys.stderr)
                    return 1
                return result.returncode
        finally:
            # Release only our hold, then stop only our process group. Ordinary
            # wt runs clean themselves; a restart run preserves only this
            # invocation's verified sandbox for the guarded cleanup below.
            (state / "release").touch()
            stop_owned_process(process)
            provider.shutdown()
            provider.server_close()
            thread.join(timeout=5)
            # Keep the provider's sanitized records beside the run's log: which
            # readers were chosen and what status each returned, and whether a
            # watched marker reached the provider. Neither holds content.
            for record in ("provider-trace.jsonl", "provider-audit.json"):
                if (state / record).exists():
                    shutil.copyfile(state / record, evidence / (log.stem + "-" + record))
            # A forced shell termination can skip zsh's always block. Restrict
            # fallback cleanup to this invocation's verified temporary sandbox.
            if sandbox and sandbox.exists():
                if sandbox.parent.resolve() != Path(tempfile.gettempdir()).resolve() or not sandbox.name.startswith("ori-demo."):
                    raise RuntimeError("refusing unexpected sandbox cleanup")
                shutil.rmtree(sandbox)


if __name__ == "__main__":
    raise SystemExit(main())
