# Plugins

Ori can install **Claude Code-** and **Codex-compatible plugins** — bundles that
package MCP servers and skills (and, in those ecosystems, commands/agents/hooks).
A plugin is a **packaging layer over Ori's existing MCP + skills primitives**, not a
return of the old gRPC plugin system: installing one registers its MCP servers in
Ori's MCP registry and drops its skills into a scanned skills directory.

## Installing a plugin

**From the UI** — open **Plugins** in the sidebar:
- Paste a **local path** or **git URL** under *Install a plugin*, click **Preview**,
  review what it will register, then **Confirm install**.
- Or **add a marketplace** (git URL / local folder) and install a listed plugin by name.

**From the API:**
```
# Preview (no changes) — returns the trust disclosure
POST /api/plugins/install     {"source": "<path|git>", "confirm": false}
# Install
POST /api/plugins/install     {"source": "<path|git>", "confirm": true}
GET  /api/plugins                                  # list installed
POST /api/plugins/{name}/enable | /disable         # per-workspace binding state
DELETE /api/plugins/{name}                         # uninstall (removes all components)

# Marketplaces
GET  /api/plugins/marketplaces
POST /api/plugins/marketplaces            {"source": "<path|git>"}
POST /api/plugins/marketplaces/install    {"marketplace": "...", "plugin": "...", "confirm": true}
```

## Trust

Before anything is registered, install shows a **disclosure**: the exact commands each
MCP server will run, the skills it adds, any unsupported components, and warnings (e.g. a
missing binary). Nothing is registered until you confirm; declining makes **no changes**.

## Supported components

| Component | Status |
|-----------|--------|
| MCP servers (`.mcp.json`) | ✅ registered |
| Skills (`skills/<name>/SKILL.md`) | ✅ installed (discovered by Ori) |
| Slash commands (`commands/`) | ⏸ recognized, **skipped + reported** (not yet registered) |
| Agents (`agents/`) | ⏸ recognized, skipped + reported |
| Hooks (`hooks/`) | ⏸ recognized, skipped + reported |
| Codex app connectors (`.app.json`) | ⏸ metadata only |

Unsupported components never fail an install — they're listed in the trust disclosure and
on the plugin's entry.

## Plugin layout

**Claude Code:**
```
my-plugin/
├── .claude-plugin/plugin.json     # name (required), version, description, …
├── .mcp.json                      # { "<server>": { "command": "${CLAUDE_PLUGIN_ROOT}/bin/x", "args": [] } }
└── skills/<name>/SKILL.md
```

**Codex** (also supports a versioned `<plugin>/<version>/` layout):
```
my-plugin/
├── .codex-plugin/plugin.json      # name, version, "mcpServers": "./.mcp.json", "skills": "./skills/", "interface": {…}
├── .mcp.json                      # { "mcpServers": { "<server>": { "command": "./bin/x", "cwd": "." } } }
└── skills/<name>/SKILL.md
```

Ori normalizes both formats into one internal descriptor. Notable differences it handles:
the `.mcp.json` shape (Codex wraps servers under `mcpServers`; Claude uses a bare keyed
map), and the plugin-root model (Claude's `${CLAUDE_PLUGIN_ROOT}` vs. Codex relative
command + `cwd`, which Ori resolves to an absolute command).

## Binary delivery

Ori runs whatever command a plugin's `.mcp.json` specifies; it does **not** build the
binary. If the resolved command is missing or not executable, the plugin still installs but
the server is marked **"unavailable — binary missing"** until you provide the binary
(prebuilt, `go build`, or a downloaded release) and re-enable it.

## Component ownership & uninstall

Plugin-registered MCP servers and skills are **owned by the plugin** (managed via
install/uninstall, not edited directly). Uninstall removes every component the plugin
registered, recorded at install time, leaving no orphans.
