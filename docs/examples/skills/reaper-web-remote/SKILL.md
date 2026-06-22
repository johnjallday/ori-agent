---
name: reaper-web-remote
description: Control a running REAPER instance from a CLI agent (Codex/Claude Code) via REAPER's Web Remote HTTP interface, using only the agent's built-in shell. Use when asked to inspect or change REAPER's transport, project, tempo, tracks, or to run registered ReaScripts.
---

# REAPER via Web Remote (shell, no MCP)

You drive REAPER over its **Web Remote** HTTP interface using your **shell** (curl).
This works inside the workspace sandbox because localhost network is enabled — you
do NOT need an MCP tool or app-automation permission.

## 1. Find the Web Remote port
REAPER's port is in its config. On macOS:

    grep -i '^csurf_' "$HOME/Library/Application Support/REAPER/reaper.ini"

Look for a line like `csurf_0=HTTP 0 2307 ...` — the number (e.g. `2307`) is the port.
If not found, the common default is `2307`. Call it `$PORT`.

## 2. Confirm REAPER is reachable

    curl -s -m 5 "http://127.0.0.1:$PORT/_/TRANSPORT"

A tab-separated `TRANSPORT\t<playstate>\t<pos_sec>\t<repeat>\t<pos_str>\t<pos_beats>`
line means REAPER is up and the Web Remote is live.

## 3. Run REAPER actions / registered ReaScripts
Trigger any REAPER **action command ID** (or a ReaScript that has been registered
as an action and thus has a command ID):

    curl -s -m 5 "http://127.0.0.1:$PORT/_/<COMMAND_ID>"

Chain commands with `;`:  `.../_/40044;1016`  (play, then stop).

Useful built-ins: `40023` New project, `40022` Save project as, `1007` Play,
`1016` Stop, `40044` Play/stop. Registered ReaScripts appear in REAPER's Action
List with `_RS…`/custom IDs; use the catalog the workspace provides.

## 4. Create/configure a project at a specific tempo
Web Remote cannot set an arbitrary BPM directly, so two reliable options:

- **File-based (no live REAPER needed):** write a minimal project file in the
  workspace and set its tempo line, e.g. a `.RPP` containing `  TEMPO 123 4 4`,
  then (optionally) load it into the running REAPER via a registered "open
  project" ReaScript.
- **Script-based (live REAPER):** use a registered ReaScript that sets the tempo
  (e.g. one that calls `reaper.SetCurrentBPM(0, bpm, true)`); pass the desired
  value by writing it to an agreed temp file the script reads, then trigger the
  script's command ID via the curl in step 3.

## Rules
- Always discover `$PORT` first; never hardcode unless step 1 fails.
- Prefer reads (`/_/TRANSPORT`, status) before any state-changing action.
- Do not launch or quit REAPER and do not use app automation — only Web Remote + files.
