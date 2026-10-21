#!/usr/bin/env python3
"""Read-only check of a REAPER plugin candidate's Studio catalog readers.

Usage:
  ./scripts/reaper-studio-reader-check.py --binary PATH --out DIR [--home DIR]

Runs the candidate binary's MCP service (`serve`) with HOME set to --home
(default: the current HOME), calls `studio.sources.read` once and pages
`studio.inventory.read` for every ready source, and snapshots the REAPER
resource folder and the Ori script library before and after: membership,
symlink targets and a sha256 of every regular file, never following a link
and never creating a path. It writes the answers, both snapshots and their
difference to --out and exits non-zero when a source changed.

The candidate decides what it reads; this script only observes. It starts no
REAPER, installs nothing and never runs a cataloged script.
"""

import argparse
import hashlib
import json
import os
import stat
import subprocess
import sys
from pathlib import Path

RESOURCE = Path("Library", "Application Support", "REAPER")
LIBRARY = Path("Ori Scripts", "reaper")
EXCHANGE = Path(".ori-reaper")
# REAPER itself rewrites these at exit; they are configuration, not sources.
REAPER_OWNED = {"reaper.ini", "reaper-fxtags.ini"}


def snapshot(root: Path) -> dict:
    """Membership and content of root without following links or creating it."""
    try:
        info = os.lstat(root)
    except FileNotFoundError:
        return {"exists": False}
    if stat.S_ISLNK(info.st_mode):
        return {"exists": True, "symlink": os.readlink(root)}
    entries = {}
    for directory, dirnames, filenames in os.walk(root, followlinks=False):
        dirnames.sort()
        for name in sorted(dirnames + filenames):
            path = Path(directory, name)
            relative = str(path.relative_to(root))
            item = os.lstat(path)
            if stat.S_ISLNK(item.st_mode):
                entries[relative] = {"link": os.readlink(path)}
            elif stat.S_ISDIR(item.st_mode):
                entries[relative] = {"dir": True}
            elif stat.S_ISREG(item.st_mode):
                digest = hashlib.sha256()
                with open(path, "rb") as handle:
                    for chunk in iter(lambda: handle.read(1 << 20), b""):
                        digest.update(chunk)
                entries[relative] = {"size": item.st_size, "sha256": digest.hexdigest()}
            else:
                entries[relative] = {"special": True}
    return {"exists": True, "entries": entries}


def snapshot_all(home: Path) -> dict:
    return {
        "resource": snapshot(home / RESOURCE),
        "library": snapshot(home / LIBRARY),
        "exchange": snapshot(home / EXCHANGE),
    }


def difference(before: dict, after: dict, ignore: set) -> list:
    changes = []
    if before.get("exists") != after.get("exists"):
        changes.append({"root": "created" if after.get("exists") else "removed"})
        return changes
    old, new = before.get("entries", {}), after.get("entries", {})
    for path in sorted(set(old) | set(new)):
        if Path(path).name in ignore and "/" not in path:
            continue
        if old.get(path) != new.get(path):
            changes.append({"path": path, "before": old.get(path), "after": new.get(path)})
    return changes


class Service:
    def __init__(self, binary: str, home: Path):
        env = dict(os.environ, HOME=str(home))
        self.process = subprocess.Popen(
            [binary, "serve"],
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            env=env,
            text=True,
        )
        self.next_id = 0

    def request(self, method: str, params: dict) -> dict:
        self.next_id += 1
        message = {"jsonrpc": "2.0", "id": self.next_id, "method": method, "params": params}
        self.process.stdin.write(json.dumps(message) + "\n")
        self.process.stdin.flush()
        while True:
            line = self.process.stdout.readline()
            if not line:
                raise RuntimeError(f"service exited during {method}")
            reply = json.loads(line)
            if reply.get("id") == self.next_id:
                if "error" in reply:
                    raise RuntimeError(f"{method}: {reply['error']}")
                return reply["result"]

    def notify(self, method: str) -> None:
        self.process.stdin.write(json.dumps({"jsonrpc": "2.0", "method": method}) + "\n")
        self.process.stdin.flush()

    def call(self, tool: str, arguments: dict) -> dict:
        envelope = {
            "protocol_version": 1,
            "operation_id": f"reader-check-{self.next_id + 1}",
            "context": {"workspace_id": "reader-check"},
            "input": arguments,
        }
        result = self.request("tools/call", {"name": tool, "arguments": envelope})
        if result.get("isError"):
            raise RuntimeError(f"{tool}: {result.get('content')}")
        if "structuredContent" in result:
            return result["structuredContent"]
        return json.loads(result["content"][0]["text"])

    def close(self) -> None:
        self.process.stdin.close()
        try:
            self.process.wait(timeout=10)
        except subprocess.TimeoutExpired:
            self.process.kill()


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--binary", required=True)
    parser.add_argument("--out", required=True)
    parser.add_argument("--home", default=os.environ.get("HOME", ""))
    args = parser.parse_args()
    home = Path(args.home)
    out = Path(args.out)
    out.mkdir(parents=True, exist_ok=True)

    before = snapshot_all(home)
    service = Service(args.binary, home)
    try:
        service.request(
            "initialize",
            {"protocolVersion": "2025-06-18", "capabilities": {}, "clientInfo": {"name": "reader-check", "version": "1"}},
        )
        service.notify("notifications/initialized")
        tools = {tool["name"] for tool in service.request("tools/list", {})["tools"]}
        missing = {"studio.sources.read", "studio.inventory.read"} - tools
        if missing:
            raise RuntimeError(f"the candidate does not serve {sorted(missing)}")
        sources = service.call("studio.sources.read", {"schema_version": 1})
        inventories = {}
        for source in sources["sources"]:
            pages, cursor = [], ""
            if source["state"] == "ready" or source["access"] == "live":
                while True:
                    page = service.call(
                        "studio.inventory.read",
                        {
                            "schema_version": 1,
                            "installation_id": sources["installation"]["id"],
                            "source_id": source["id"],
                            "cursor": cursor,
                            "limit": 256,
                        },
                    )
                    pages.append(page)
                    cursor = page.get("next_cursor", "")
                    if not cursor or len(pages) >= 1000:
                        break
            inventories[source["id"]] = pages
    finally:
        service.close()
    after = snapshot_all(home)

    changes = {
        "resource": difference(before["resource"], after["resource"], REAPER_OWNED),
        "library": difference(before["library"], after["library"], set()),
        "exchange": difference(before["exchange"], after["exchange"], set()),
    }
    summary = {"installation": sources["installation"], "sources": []}
    for source in sources["sources"]:
        pages = inventories[source["id"]]
        entries = [entry for page in pages for entry in page.get("entries", [])]
        skipped = {}
        for page in pages:
            for skip in page.get("coverage", {}).get("skipped", []):
                skipped[skip["reason"]] = skipped.get(skip["reason"], 0) + skip["count"]
        summary["sources"].append(
            {
                "id": source["id"],
                "location": source["location"],
                "state": source["state"],
                "inventory_state": pages[-1]["state"] if pages else None,
                "pages": len(pages),
                "entries": len(entries),
                "skipped": skipped,
                "kinds": sorted({entry["kind"] for entry in entries}),
                "sample_ids": [entry["native_id"] for entry in entries[:5]],
            }
        )
    summary["source_changes"] = len(changes["resource"]) + len(changes["library"])
    summary["exchange_changes"] = len(changes["exchange"])

    (out / "sources.json").write_text(json.dumps(sources, indent=2) + "\n")
    (out / "inventories.json").write_text(json.dumps(inventories, indent=2) + "\n")
    (out / "snapshot-before.json").write_text(json.dumps(before, indent=2) + "\n")
    (out / "snapshot-after.json").write_text(json.dumps(after, indent=2) + "\n")
    (out / "changes.json").write_text(json.dumps(changes, indent=2) + "\n")
    (out / "summary.json").write_text(json.dumps(summary, indent=2) + "\n")
    print(json.dumps(summary, indent=2))
    return 1 if summary["source_changes"] else 0


if __name__ == "__main__":
    sys.exit(main())
