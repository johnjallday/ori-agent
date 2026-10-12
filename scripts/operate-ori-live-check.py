#!/usr/bin/env python3
"""Ask a real model, on an isolated demo host, to operate Ori. Times each turn.

    wt demo 8963                      # in another terminal; note "Demo sandbox:"
    ./scripts/operate-ori-live-check.py --sandbox <that path> [--port 8963]
        [--provider codex] [--model <name>]

The browser acceptance runs use a deterministic stand-in for the model. This
check is the other half: whether a real model, given Ori's actual skill text
and tool definitions, calls the operation tools for an explicit request and
leaves them alone for a question. It sends four assistant turns and prints,
for each, how long it took and what Ori recorded. It starts no task: preparing
a draft writes nothing, and nothing here saves or starts one.

It refuses to run against anything but a loopback demo host whose sandbox
directory is named ori-demo.*, because it hires an assistant and creates a
workspace there. With the codex provider each turn runs the Codex CLI on your
own login and leaves a session log in your Codex home.

Exit status: 0 when every turn did what was expected, 1 otherwise, 2 for a
usage or setup problem.
"""

import argparse
import json
import sys
import time
from pathlib import Path
from urllib.error import HTTPError, URLError
from urllib.request import Request, urlopen

WORKSPACE = "Album-1 live check"
TURN_TIMEOUT = 240


def call(base, method, path, body=None, timeout=60):
    data = json.dumps(body).encode() if body is not None else None
    request = Request(base + path, data=data, method=method,
                      headers={"Content-Type": "application/json", "Accept": "application/json"})
    try:
        with urlopen(request, timeout=timeout) as response:
            text = response.read().decode()
            return response.status, json.loads(text) if text else {}
    except HTTPError as error:
        text = error.read().decode()
        try:
            return error.code, json.loads(text)
        except ValueError:
            return error.code, {"error": text[:200]}


def must(status, body, what, ok=(200, 201)):
    if status not in ok:
        print(f"setup failed: {what}: {status} {json.dumps(body)[:300]}", file=sys.stderr)
        raise SystemExit(2)
    return body


def seed(base, provider, model):
    call(base, "POST", "/api/onboarding/skip")
    must(*call(base, "POST", "/api/settings/workspace-root", {"workspace_root": ""}), "workspace root")
    must(*call(base, "POST", "/api/settings/system-model", {"provider": provider, "model": model}),
         f"system model {provider}/{model}")
    assistant = must(*call(base, "GET", "/api/personal-assistant"), "relationship")["personal_assistant"]
    if assistant.get("status") not in ("active", "paused"):
        hired = must(*call(base, "POST", "/api/personal-assistant/hire", {
            "request_id": "live-check-hire", "if_version": assistant.get("state_version") or 0,
            "display_name": "Atlas", "mandate": "Help me plan.", "focus_areas": ["plan_my_day"]}), "hire")
        assistant = hired["personal_assistant"]
        must(*call(base, "POST", "/api/personal-assistant/hq", {
            "request_id": "live-check-hq", "if_version": assistant["state_version"],
            "name": "My HQ", "timezone": "UTC"}), "HQ")
    folders = must(*call(base, "GET", "/api/workspaces"), "workspaces")["folders"]
    workspace = next((row for row in folders if row["name"] == WORKSPACE), None)
    if not workspace:
        must(*call(base, "POST", "/api/workspaces", {"name": WORKSPACE}), "create workspace")
        folders = must(*call(base, "GET", "/api/workspaces"), "workspaces")["folders"]
        workspace = next(row for row in folders if row["name"] == WORKSPACE)
        for agent in ("Mixer", "Engineer"):
            call(base, "POST", "/api/agents", {"name": agent, "model": "gpt-4o-mini"})
            must(*call(base, "POST", "/api/orchestration/workspace/agents",
                       {"workspace_id": workspace["id"], "agent_name": agent}), f"add {agent}")
    capability = must(*call(base, "GET", "/api/home-assistant/operations/capability"), "capability")
    return workspace, capability


def task_count(base, workspace):
    body = must(*call(base, "GET", "/api/orchestration/tasks?workspace_id=" + workspace["id"]), "tasks")
    return len(body.get("tasks") or [])


def turn(base, prompt, conversation):
    context = {"context_version": 1, "origin": "personal_assistant_panel", "surface": "home", "page_path": "/"}
    started = time.monotonic()
    status, route = call(base, "POST", "/api/home-assistant/route", {"prompt": prompt, "context": context})
    body = {"prompt": prompt, "intent": route.get("intent") or "assistant_conversation", "context": context,
            "conversation": {"id": conversation} if conversation else {},
            "operation": {"tab_id": "live-check-tab", "watch": True}}
    status, reply = call(base, "POST", "/api/home-assistant/ask", body, timeout=TURN_TIMEOUT)
    return status, route, reply, time.monotonic() - started


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--sandbox", required=True, help='the "Demo sandbox:" path wt demo printed')
    parser.add_argument("--port", type=int, default=8963)
    parser.add_argument("--provider", default="codex")
    parser.add_argument("--model", default="")
    args = parser.parse_args()
    sandbox = Path(args.sandbox)
    if not sandbox.is_dir() or not sandbox.name.startswith("ori-demo."):
        print("--sandbox must be an existing wt demo sandbox (ori-demo.*)", file=sys.stderr)
        return 2
    if not args.model:
        print("--model is required (the model your app uses for the assistant)", file=sys.stderr)
        return 2
    base = f"http://127.0.0.1:{args.port}"
    try:
        workspace, capability = seed(base, args.provider, args.model)
    except URLError as error:
        print(f"no demo host on {base}: {error.reason}", file=sys.stderr)
        return 2
    print(f"host {base}  model {args.provider}/{args.model}  operations: {json.dumps(capability)}")
    if not capability.get("available"):
        print("this provider cannot run Ori's operation tools here; nothing was asked", file=sys.stderr)
        return 1

    before = task_count(base, workspace)
    # (prompt, the kind of operation an explicit request should record, or None)
    turns = [
        (f"Open the {WORKSPACE} workspace and select Mixer", "navigation"),
        (f"Create a task for Engineer in the {WORKSPACE} workspace to bounce the stems", "task"),
        ("How do I open a workspace?", None),
        ("Should we give Mixer a task at some point?", None),
    ]
    failures, conversation, rows = 0, "", []
    for prompt, want in turns:
        try:
            status, route, reply, seconds = turn(base, prompt, conversation)
        except (URLError, TimeoutError) as error:
            print(f"FAIL  {prompt!r}: no answer ({error})")
            failures += 1
            continue
        conversation = (reply.get("conversation") or {}).get("id") or conversation
        operation = reply.get("operation") or {}
        got = operation.get("kind")
        ok = status == 200 and got == want and not reply.get("requires_confirmation")
        detail = {"seconds": round(seconds, 1), "route": route.get("intent"), "operation": got,
                  "status": operation.get("status"), "error": reply.get("operation_error") or reply.get("error")}
        if got == "task":
            draft = operation.get("draft") or {}
            detail["draft_agent"] = draft.get("agent_name")
            detail["draft_title"] = draft.get("title")
            ok = ok and draft.get("agent_name") == "Engineer" and operation.get("status") == "prepared"
        if got == "navigation":
            actions = [command.get("action") for command in operation.get("commands") or []]
            detail["commands"] = actions
            ok = ok and actions == ["open_workspace", "select_agent"]
        failures += 0 if ok else 1
        rows.append({"prompt": prompt, "ok": ok, **detail})
        print(("PASS  " if ok else "FAIL  ") + json.dumps({"prompt": prompt, **detail}))
        print("      reply: " + " ".join(str(reply.get("response") or "").split())[:300])

    after = task_count(base, workspace)
    wrote_nothing = after == before
    print(("PASS  " if wrote_nothing else "FAIL  ") + f"tasks in the workspace: {before} before, {after} after")
    slowest = max((row["seconds"] for row in rows), default=0)
    print(f"slowest turn: {slowest}s")
    return 0 if failures == 0 and wrote_nothing else 1


if __name__ == "__main__":
    raise SystemExit(main())
