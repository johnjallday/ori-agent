#!/usr/bin/env python3
"""Tiny MCP stdio service for the copyable Workspace Surface example.

It deliberately uses only Python's standard library and has no filesystem or
network behavior. Ori supplies authoritative workspace context to operations;
this example never accepts a workspace path or identifier from the frame.
"""

import json
import sys
from datetime import datetime, timezone


def tool(name, description, input_schema):
    return {
        "name": name,
        "description": description,
        "inputSchema": input_schema,
        "outputSchema": {},
    }


EMPTY_INPUT = {
    "type": "object",
    "properties": {},
    "required": [],
    "additionalProperties": False,
}

degraded = False

TOOLS = [
    tool("status.read", "Return bounded station status.", EMPTY_INPUT),
    tool(
        "greeting.create",
        "Create a harmless bounded greeting.",
        {
            "type": "object",
            "properties": {"name": {"type": "string", "maxLength": 80}},
            "required": ["name"],
            "additionalProperties": False,
        },
    ),
    tool(
        "setting.validate",
        "Demonstrate a host-confirmed switch between ready and degraded state.",
        {
            "type": "object",
            "properties": {"enabled": {"type": "boolean"}},
            "required": ["enabled"],
            "additionalProperties": False,
        },
    ),
    tool("runtime.prerequisites", "Check harmless demo prerequisites.", EMPTY_INPUT),
    tool("runtime.readiness", "Check durable demo readiness.", EMPTY_INPUT),
    tool("runtime.live_status", "Check current demo availability.", EMPTY_INPUT),
    tool("runtime.verify", "Verify the demo provider.", EMPTY_INPUT),
    tool("runtime.repair", "Repair the intentional degraded demo state.", EMPTY_INPUT),
]


def send(message):
    sys.stdout.write(json.dumps(message, separators=(",", ":")) + "\n")
    sys.stdout.flush()


def successful(request_id, result):
    send({"jsonrpc": "2.0", "id": request_id, "result": result})


def failed(request_id, code, message):
    send({"jsonrpc": "2.0", "id": request_id, "error": {"code": code, "message": message}})


def call_tool(name, arguments):
    global degraded
    # Ori's broker wraps frame input with authoritative context. Domain tools
    # read only the bounded `input`; they must not accept caller-selected roots.
    payload = arguments.get("input") if isinstance(arguments.get("input"), dict) else arguments
    if name == "status.read":
        output = {
            "state": "degraded" if degraded else "ready",
            "value": "Intentional degraded state" if degraded else "Example service ready",
            "description": "Use Repair in setup to restore the demo." if degraded else "The copyable MCP stdio example is responding.",
            "checked_at": datetime.now(timezone.utc).isoformat().replace("+00:00", "Z"),
        }
    elif name == "greeting.create":
        requested = str(payload.get("name", "")).strip()
        if not requested or len(requested) > 80:
            raise ValueError("name must contain 1 to 80 characters")
        output = {"message": "Hello, " + requested + "."}
    elif name == "setting.validate":
        if not isinstance(payload.get("enabled"), bool):
            raise ValueError("enabled must be boolean")
        degraded = not payload["enabled"]
        output = {"accepted": True}
    elif name in ("runtime.prerequisites", "runtime.readiness"):
        output = {
            "ready": not degraded,
            "summary": "Demo provider is ready." if not degraded else "The intentional demo degradation needs repair.",
        }
    elif name == "runtime.live_status":
        output = {
            "available": not degraded,
            "summary": "Demo provider is online." if not degraded else "Demo provider is intentionally degraded.",
        }
    elif name == "runtime.verify":
        output = {
            "verified": not degraded,
            "summary": "Demo provider verified." if not degraded else "Repair the demo provider before verification.",
        }
    elif name == "runtime.repair":
        degraded = False
        output = {"repaired": True, "summary": "Demo provider repaired."}
    else:
        raise KeyError("unknown tool")
    return {
        "content": [{"type": "text", "text": json.dumps(output, separators=(",", ":"))}],
        "structuredContent": output,
        "isError": False,
    }


def handle(request):
    method = request.get("method")
    request_id = request.get("id")
    if request_id is None:
        return
    if method == "initialize":
        successful(
            request_id,
            {
                "protocolVersion": "2025-03-26",
                "capabilities": {"tools": {"listChanged": False}},
                "serverInfo": {"name": "workspace-surface-demo", "version": "0.1.0"},
            },
        )
    elif method == "ping":
        successful(request_id, {})
    elif method == "tools/list":
        successful(request_id, {"tools": TOOLS})
    elif method == "tools/call":
        params = request.get("params") or {}
        try:
            successful(request_id, call_tool(str(params.get("name", "")), params.get("arguments") or {}))
        except (KeyError, ValueError) as error:
            successful(
                request_id,
                {
                    "content": [{"type": "text", "text": str(error)}],
                    "structuredContent": {},
                    "isError": True,
                },
            )
    else:
        failed(request_id, -32601, "method not found")


def main():
    for line in sys.stdin:
        try:
            message = json.loads(line)
            if isinstance(message, dict):
                handle(message)
        except Exception:
            # Keep protocol diagnostics generic: never echo malformed input,
            # environment values, local paths, or stack traces to the client.
            continue


if __name__ == "__main__":
    main()
