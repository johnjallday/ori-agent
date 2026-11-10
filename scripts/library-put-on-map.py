#!/usr/bin/env python3
"""library-put-on-map.py — put a Home's ready shelf songs on the map against an
ISOLATED demo server, without a browser, through the same endpoints the
projects sheet uses, and print where each song workspace landed.

Usage:
  python3 -I scripts/library-put-on-map.py URL HOME_ID [NAME_PREFIX]

  URL          http://localhost:PORT of a demo server (wt demo, demo-server.sh)
  HOME_ID      the Home's workspace id (folder-agent-drive.py prints it as the
               receipt's Home)
  NAME_PREFIX  only entries whose name starts with it (e.g. "Album-")

Per entry, like "Put N on the map": read the entry, read its activation
eligibility, review with its first offered project file, commit. It never adds
an assistant. Output: one JSON object per entry with the commit status, the
new workspace id, its parent_id (the Home, or the folder workspace it nested
in) and the server's placement_note, if any.
"""
import json
import sys
import urllib.error
import urllib.request


def call(url, method="GET", body=None):
    data = None if body is None else json.dumps(body).encode()
    req = urllib.request.Request(url, data=data, method=method, headers={"Content-Type": "application/json"})
    try:
        with urllib.request.urlopen(req, timeout=120) as resp:
            return resp.status, json.loads(resp.read() or b"{}")
    except urllib.error.HTTPError as err:
        return err.code, json.loads(err.read() or b"{}")


def main():
    if len(sys.argv) < 3 or not sys.argv[1].startswith("http://localhost:"):
        print(__doc__, file=sys.stderr)
        sys.exit(2)
    base, home = sys.argv[1].rstrip("/"), sys.argv[2]
    prefix = sys.argv[3] if len(sys.argv) > 3 else ""
    lib = f"{base}/api/workspaces/{home}/assistant-program/library"
    status, page = call(f"{lib}/projects?sort=last_saved&direction=desc&page_size=25")
    if status != 200:
        sys.exit(f"library projects {status}: {page}")
    results = []
    for row in page.get("rows", []):
        name = str(row.get("name", ""))
        if (prefix and not name.startswith(prefix)) or row.get("connection") == "connected":
            continue
        entry = row["id"]
        _, detail = call(f"{lib}/projects/{entry}")
        _, eligibility = call(f"{lib}/projects/{entry}/activation")
        files = [f for f in eligibility.get("project_files", []) if f]
        if eligibility.get("state") not in ("review_available", "file_choice_required") or not files:
            results.append({"entry": name, "skipped": eligibility.get("state")})
            continue
        code, review = call(f"{lib}/projects/{entry}/activation/review", "POST", {
            "workspace_name": detail.get("row", {}).get("name", name),
            "project_file": files[0],
            "if_revision": detail.get("revision")})
        if code != 200:
            results.append({"entry": name, "review": code, "body": review})
            continue
        code, commit = call(f"{lib}/projects/{entry}/activation/commit", "POST", {
            "review_token": review.get("token"), "idempotency_key": "put-on-map-" + entry, "confirm": True})
        item = {"entry": name, "commit": code, "workspace_id": commit.get("workspace_id"),
                "placement_note": commit.get("placement_note", "")}
        if item["workspace_id"]:
            _, ws = call(f"{base}/api/workspaces/{item['workspace_id']}")
            item["parent_id"] = (ws.get("folder") or ws).get("parent_id", "")
        results.append(item)
    print(json.dumps(results, indent=2))


if __name__ == "__main__":
    main()
