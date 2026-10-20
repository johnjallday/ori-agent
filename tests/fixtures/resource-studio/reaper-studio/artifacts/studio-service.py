#!/usr/bin/env python3
"""Test-fixture MCP stdio service for the REAPER Studio catalog contract.

It answers studio.sources.read and studio.inventory.read from fixed data so
Ori's host can be exercised end to end. It reads no folder, talks to no
REAPER and changes nothing. Pages are cut by the request's cursor and limit.
"""

import json
import sys

INSTALLATION = {
    "id": "inst_fixture_studio_01",
    "label": "REAPER 7.28 (fixture)",
    "layout": "per_user",
    "app_installed": True,
    "app_version": "7.28",
}

SOURCES = [
    {"id": "project_templates", "category": "templates", "label": "Project templates",
     "location": "~/Library/Application Support/REAPER/ProjectTemplates", "access": "file", "state": "ready"},
    {"id": "track_templates", "category": "templates", "label": "Track templates",
     "location": "~/Library/Application Support/REAPER/TrackTemplates", "access": "file", "state": "ready"},
    {"id": "reaper_scripts", "category": "scripts", "label": "REAPER Scripts",
     "location": "~/Library/Application Support/REAPER/Scripts", "access": "file", "state": "ready"},
    {"id": "ori_scripts", "category": "scripts", "label": "Ori script library",
     "location": "~/Ori Scripts/reaper", "access": "file", "state": "absent",
     "detail": "There is no Ori script library on this computer yet."},
    {"id": "audio_plugins", "category": "audio_plugins", "label": "REAPER plugin list",
     "location": "REAPER application", "access": "live", "state": "app_closed",
     "detail": "Open REAPER to read its plugin list."},
]


def entry(native_id, kind, name, facts=None, description=None):
    item = {"native_id": native_id, "kind": kind, "name": name, "facts": facts or []}
    if description:
        item["description"] = {"text": description, "source": "Script header"}
    return item


ENTRIES = {
    "project_templates": [
        entry("Vocals.RPP", "project_template", "Vocals"),
        entry("Band/Vocals.RPP", "project_template", "Vocals"),
        entry("Band/Live tracking.RPP", "project_template", "Live tracking"),
    ],
    "track_templates": [
        entry("Lead vocal chain.RTrackTemplate", "track_template", "Lead vocal chain"),
    ],
    "reaper_scripts": [
        entry("Cockos/Lyrics.lua", "script", "Lyrics",
              [{"id": "language", "value": "Lua"}, {"id": "registration", "value": "Yes"}],
              "Shows the lyrics of the current item."),
        entry("ReaTeam Scripts/Items/Split at grid.eel", "script", "Split at grid",
              [{"id": "language", "value": "EEL"}, {"id": "package", "value": "ReaTeam Scripts"}]),
        entry("Tools/Render stems.py", "script", "Render stems", [{"id": "language", "value": "Python"}]),
    ],
}


def sources_read(_payload):
    return {"schema_version": 1, "installation": INSTALLATION, "sources": SOURCES}


def inventory_read(payload):
    source_id = str(payload.get("source_id", ""))
    answer = {
        "schema_version": 1,
        "installation_id": str(payload.get("installation_id", "")),
        "source_id": source_id,
        "state": "ok",
        "entries": [],
        "next_cursor": "",
        "coverage": {"visited": 0, "skipped": []},
    }
    if answer["installation_id"] != INSTALLATION["id"]:
        answer["state"] = "installation_changed"
        return answer
    source = next((item for item in SOURCES if item["id"] == source_id), None)
    if source is None or source["state"] != "ready":
        answer["state"] = source["state"] if source else "unsupported"
        return answer
    items = ENTRIES.get(source_id, [])
    cursor = str(payload.get("cursor", ""))
    start = int(cursor[3:]) if cursor.startswith("at-") and cursor[3:].isdigit() else 0
    limit = max(1, min(int(payload.get("limit", 1)), 256))
    page = items[start:start + limit]
    answer["entries"] = page
    answer["coverage"]["visited"] = len(page)
    if source_id == "reaper_scripts" and start == 0:
        answer["coverage"]["skipped"] = [{"reason": "external_registration", "count": 1}]
    if start + limit < len(items):
        answer["next_cursor"] = "at-" + str(start + limit)
    return answer


TOOLS = {"studio.sources.read": sources_read, "studio.inventory.read": inventory_read}


def send(message):
    sys.stdout.write(json.dumps(message, separators=(",", ":")) + "\n")
    sys.stdout.flush()


def handle(request):
    method = request.get("method")
    request_id = request.get("id")
    if request_id is None:
        return
    if method == "initialize":
        send({"jsonrpc": "2.0", "id": request_id, "result": {
            "protocolVersion": "2025-03-26",
            "capabilities": {"tools": {"listChanged": False}},
            "serverInfo": {"name": "reaper-studio-fixture", "version": "0.11.0-fixture"},
        }})
    elif method == "ping":
        send({"jsonrpc": "2.0", "id": request_id, "result": {}})
    elif method == "tools/list":
        tools = [{"name": name, "description": name, "inputSchema": {}, "outputSchema": {}} for name in TOOLS]
        send({"jsonrpc": "2.0", "id": request_id, "result": {"tools": tools}})
    elif method == "tools/call":
        params = request.get("params") or {}
        arguments = params.get("arguments") or {}
        payload = arguments.get("input") if isinstance(arguments.get("input"), dict) else arguments
        handler = TOOLS.get(str(params.get("name", "")))
        if handler is None:
            send({"jsonrpc": "2.0", "id": request_id, "result": {
                "content": [{"type": "text", "text": "unknown tool"}], "structuredContent": {}, "isError": True}})
            return
        output = handler(payload)
        send({"jsonrpc": "2.0", "id": request_id, "result": {
            "content": [{"type": "text", "text": json.dumps(output, separators=(",", ":"))}],
            "structuredContent": output,
            "isError": False,
        }})
    else:
        send({"jsonrpc": "2.0", "id": request_id, "error": {"code": -32601, "message": "method not found"}})


def main():
    for line in sys.stdin:
        try:
            message = json.loads(line)
            if isinstance(message, dict):
                handle(message)
        except Exception:
            continue


if __name__ == "__main__":
    main()
