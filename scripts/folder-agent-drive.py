#!/usr/bin/env python3
"""folder-agent-drive.py — drive one folder-agent run against an ISOLATED demo
server, without a browser, and print what the Home's library says afterwards.

Usage:
  python3 -I scripts/folder-agent-drive.py URL FOLDER_PATH [--steps h1,h2] [--plan]
      [--provider claude] [--model claude-sonnet-5-5]

  URL          http://localhost:PORT of a demo server (wt demo, demo-server.sh)
  FOLDER_PATH  a folder under $TMPDIR (a sandbox fixture); refused otherwise
  --steps      the step handles to tick (comma separated); omitted = the plan
               the card shows (the agent's or Ori's default)
  --plan       print the offered steps and the plan, then stop before Set it up
  --workspace  the folder workspace of an earlier --plan run: set it up without
               a second look (FOLDER_PATH is still checked)

It hires "<folder> Manager" on the given model (the look is a real model turn),
waits for the report, sets it up, waits for the run to finish, then prints the
run's lines in order, the receipt's Home, the view's route, and the Home's
library summary (digest.activatable, digest.setup_note, ready_proposals).
"""
import json
import os
import sys
import time
import urllib.error
import urllib.request


def die(msg):
    print(msg, file=sys.stderr)
    sys.exit(2)


def call(url, method="GET", body=None):
    data = None if body is None else json.dumps(body).encode()
    req = urllib.request.Request(url, data=data, method=method, headers={"Content-Type": "application/json"})
    try:
        with urllib.request.urlopen(req, timeout=120) as resp:
            return resp.status, json.loads(resp.read() or b"{}")
    except urllib.error.HTTPError as err:
        try:
            return err.code, json.loads(err.read() or b"{}")
        except ValueError:
            return err.code, {}


def main(argv):
    args, opts = [], {"--steps": None, "--provider": "claude", "--model": "claude-sonnet-5-5", "--workspace": None}
    plan_only = False
    i = 0
    while i < len(argv):
        a = argv[i]
        if a == "--plan":
            plan_only = True
        elif a in opts:
            i += 1
            opts[a] = argv[i]
        else:
            args.append(a)
        i += 1
    if len(args) != 2 or not args[0].startswith("http://localhost:"):
        die(__doc__)
    base, folder = args[0].rstrip("/"), os.path.realpath(args[1])
    tmp = os.path.realpath(os.environ.get("TMPDIR", "/tmp"))
    if not folder.startswith(tmp + os.sep) or not os.path.isdir(folder):
        die("refusing: %s is not a folder under $TMPDIR" % folder)
    api = base + "/api/personal-assistant/folder-agent"

    if opts["--workspace"]:
        # A folder already started (e.g. after --plan): no second look.
        _, got = call(api + "/" + opts["--workspace"])
        view = got.get("folder_agent") or {}
        if not view.get("workspace_id"):
            die("no folder agent on workspace %s" % opts["--workspace"])
    else:
        status, picked = call(base + "/api/folder-picker/select-path", "POST", {"path": folder})
        if status != 200 or not picked.get("selection_token"):
            die("select-path %s: %s" % (status, picked))
        status, chosen = call(api + "/choose", "POST", {"selection_token": picked["selection_token"]})
        card = chosen.get("card") or {}
        if status != 200 or not card.get("choice_token"):
            die("choose %s: %s" % (status, chosen))
        name = os.path.basename(folder) + " Manager"
        status, started = call(api + "/start", "POST", {
            "choice_token": card["choice_token"], "brief": "",
            "hire": {"name": name, "provider": opts["--provider"], "model": opts["--model"]}})
        view = started.get("folder_agent") or {}
        if status != 201 or not view.get("workspace_id"):
            die("start %s: %s" % (status, started))
    ws = view["workspace_id"]
    print("folder workspace:", ws, view.get("workspace_name"))

    deadline = time.time() + 180
    while view.get("state") not in ("reported", "kept", "failed", "set_up"):
        if time.time() > deadline:
            die("no report after 180 s (state %s)" % view.get("state"))
        time.sleep(2)
        _, got = call(api + "/" + ws)
        view = got.get("folder_agent") or view
    if view.get("state") == "failed":
        die("look failed: %s" % view.get("failure"))
    plan = view.get("plan") or {}
    print("offered steps:", ", ".join("%s%s" % (s["handle"], "*" if s.get("chosen") else "") for s in plan.get("steps", [])))
    if opts["--steps"] is not None:
        selected = [h for h in opts["--steps"].split(",") if h]
        status, got = call(api + "/" + ws + "/plan", "POST", {"selected": selected})
        if status != 200:
            die("plan %s: %s" % (status, got))
        plan = got["plan"]
    else:
        selected = None
    print("plan lines (run order):")
    for line in plan.get("lines", []):
        print("  -", line.get("name"))
    if plan_only:
        return
    body = {"plan_digest": plan["digest"]}
    if selected is not None:
        body["selected"] = selected
    status, got = call(api + "/" + ws + "/setup", "POST", body)
    if status != 200:
        die("setup %s: %s" % (status, got))
    deadline = time.time() + 300
    while True:
        _, got = call(api + "/" + ws)
        view = got.get("folder_agent") or {}
        setup = view.get("setup") or {}
        if setup.get("status") in ("done", "stopped"):
            break
        if time.time() > deadline:
            die("run did not finish in 300 s")
        time.sleep(1)
    print("run:", setup.get("status"), setup.get("stop_reason") or "")
    for line in setup.get("lines", []):
        print("  [%s] %s%s" % (line.get("state"), line.get("name"), (" — " + line["progress"]) if line.get("progress") else ""))
    print("route:", view.get("route"))
    home = (setup.get("receipt") or {}).get("home") or {}
    print("receipt home:", json.dumps(home))
    if home.get("id"):
        _, summary = call(base + "/api/workspaces/%s/assistant-program/library/summary" % home["id"])
        summary = summary.get("summary", summary)
        digest = summary.get("digest") or {}
        print("library summary: activatable=%s setup_note=%s projects=%s needs_file_choice=%s unsupported=%s ready_proposals=%s" % (
            digest.get("activatable"), digest.get("setup_note"), digest.get("projects"),
            digest.get("needs_file_choice"), digest.get("unsupported_format"), summary.get("ready_proposals")))
    _, plugins = call(base + "/api/plugins")
    listed = plugins if isinstance(plugins, list) else plugins.get("plugins") or []
    print("plugins:", ", ".join(str(p.get("name") or p.get("id")) for p in listed))


if __name__ == "__main__":
    main(sys.argv[1:])
