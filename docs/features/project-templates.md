# Project Templates

A **template** describes how a workspace starts. It is presented as a single **Template** picker in the create-workspace modal (there is no separate "starting point" vs. "project template" choice). One template may carry any combination of:

- a **name/description** prefill,
- a default **agent behavior** profile (`general` | `research` | `software_project`),
- **starter tasks** seeded into the new workspace and assigned to the entry agent (at most one may be marked `setup: true` — it auto-starts once, when the workspace is first opened),
- default **tools** (skills / MCP servers / plugins, applied if present),
- an **agent roster** — the agents created on the new workspace (entry agent + specialists), and
- a **project folder skeleton** — a Reaper song, a writing project, a code scaffold, anything, with an optional **project entry file** Ori can offer to open in the system default application.

A template that ships a skeleton scaffolds it into the workspace and records the result as the workspace's `project_path`. A **metadata-only** template (no files beyond `template.json`) contributes behavior/tasks/tools but creates no project folder. Either way, Ori contains no domain-specific code: a template is just a folder, and all domain specificity lives in template data.

## Why this design

Ori's capabilities come from MCP servers and skills, not built-in integrations. Project templates follow the same philosophy:

- **The app ships one mechanism** — copy a folder skeleton, substitute names, set `project_path`, and optionally remember one verified entry file.
- **Templates carry the domain** — a `reaper-song` template makes Ori useful for music; a `novel` template makes it useful for writing. Supporting a new domain requires zero changes to Ori.
- **Behavior stays in MCP/skills** — `project_entry` may identify a file and default the user's one-time **Open project after creation** choice, but it cannot name an application or perform domain work. Actions such as setting tempo, confirming that REAPER is ready, or editing a live session still belong to a domain MCP or skill.

## Where templates live

The library is a directory of folders. Resolution order:

1. `templates_root` in `settings.json`
2. `ORI_TEMPLATES_DIR` environment variable
3. `<data dir>/templates` (default; created on startup)

A set of **built-in** templates is materialized on first start — only when absent, so your edits are never overwritten. These are the five metadata-only starting points (`travels`, `daily-briefings`, `content-production`, `research-project`, `personal-ops`) plus the two folder-skeleton starters (`reaper-song`, `writing-project`). Built-ins are flagged `"builtin": true` and are **read-only** in the authoring UI — use **Duplicate to customize** to make an editable copy.

**Keeping built-ins current.** Each built-in carries a `builtin_version` (integer). When a release ships a newer version of a built-in's manifest, the next start refreshes that built-in's `template.json` in place so the change (e.g. an updated agent roster) reaches existing installs. **Only the manifest is refreshed** — scaffold seed files and everything else in the folder are left untouched, so hand-edited seed files survive. User templates and duplicates are never affected (they have no `builtin_version` and are not shipped starters).

## Authoring a template

Drop a folder into the templates directory. That's the whole registration process — it appears in the workspace-creation picker on next load.

```
templates/
  my-template/
    template.json          ← optional display metadata (not copied)
    {{name}}.rpp           ← {{name}} / {{date}} substituted in file & folder names
    assets/
```

```json
{
  "name": "My REAPER Template",
  "project_entry": {
    "relative_path": "{{name}}.rpp",
    "open_after_create_default": true
  }
}
```

- `template.json` carries **declarative metadata only** — never executable hooks, scripts, or post-create commands. Recognized keys: `name`, `description`, `tags`, `icon` (emoji shown on the picker card), `behavior_profile` (`general` | `research` | `software_project`), `starter_tasks` (`[{ "description", "details", "setup" }]`; at most one task may set `setup: true`), `tools` (`{ "skills", "mcp_servers", "plugins" }`), `agents` (agent roster — see below), `project_entry`, `builtin`, and `builtin_version` (shipped manifest revision for built-ins — see "Keeping built-ins current"). Unknown keys are preserved untouched.
- A folder containing **only** `template.json` is a valid metadata-only template (it scaffolds no files).
- `{{name}}` becomes the slugified project name; `{{date}}` becomes `YYYY-MM-DD`. Substitution applies to file and folder **names only**; file contents are byte-copied untouched, so binary files are always safe.
- Symlinks are skipped during instantiation (they would break portability or reach outside the template).
- Keep templates small: instantiation is synchronous, and seeds are meant to be starting points, not finished projects.

`project_entry` rules:

- `relative_path` is relative to the generated project folder, uses `/` separators, and must name a regular, non-symlink scaffold file. Absolute paths, drive letters, traversal, malformed tokens, and symlinked components are rejected.
- Only `{{name}}` and `{{date}}` are supported, using the same values as scaffold filename substitution. The literal source file must exist in the template (for example, `{{name}}.rpp`).
- `open_after_create_default` defaults the Create Workspace checkbox; `false` still means the template has an entry and shows the checkbox unchecked. Omitting `project_entry` means there is no launch action.
- A hand-edited invalid entry produces a template warning and is omitted from normalized API output. The authoring API rejects an invalid update with `400`. Entry verification problems during instantiation are non-fatal: the project remains created and the response carries `project_warning`.
- The `/templates` **Overview** tab exposes **Project entry file** and **Open project after creation by default** controls. Leaving the path blank removes the entry.

## Agents

A template can declare an `agents` roster that is seeded onto the workspace when it is created. The roster is an **ordered** array:

- the **first agent is the entry agent** — the one required agent every workspace needs. Seeding it satisfies that requirement, so the "create an entry agent" prompt does not appear.
- the **rest are specialist sub-agents** the entry agent delegates to.

A template with **no** `agents` block behaves exactly as before: the workspace starts without agents and prompts for an entry agent.

```json
"agents": [
  {
    "name": "Research Lead",
    "role": "orchestrator",
    "type": "general",
    "model": "",
    "system_prompt": "You are the research lead for this workspace…",
    "tools": { "skills": ["citations"], "mcp_servers": ["web-search"] }
  },
  { "name": "Source Scout", "role": "researcher", "system_prompt": "You find and vet sources…" }
]
```

Per-agent fields (all optional except `name`):

- `name` — the agent's display name (required).
- `role` — `orchestrator` | `researcher` | `analyzer` | `synthesizer` | `validator` | `specialist` | `general` (unknown values fall back to the default).
- `type` — `tool-calling` | `general` | `research` (unknown values fall back to the default).
- `model` — omit to use the workspace/global default model.
- `system_prompt` — the agent's instructions.
- `tools` — per-agent `skills` (enabled on the agent) and `mcp_servers` (bound at the workspace level, since MCP has no per-agent scope), applied if present.

Rules:

- **Reuse on name match.** If an agent with that name already exists, it is **reused as-is** — the template's prompt/model/tools for that entry are ignored, so editing a reused agent affects every workspace that shares it. Only unmatched names create a new agent.
- The roster is **capped at 10**; blank-named and duplicate entries are dropped.
- If the entry agent (first) fails to create, the workspace is left agent-less and the prompt fires; a specialist that fails is skipped with a non-blocking notice.

Authoring: edit the roster in the **Agents** tab of the `/templates` page — add/remove agents, drag to reorder (drag to the top to set the entry agent). Built-in templates are read-only there.

## Instantiation

Entry points:

- **Workspace creation** — the unified **Template** picker in the create-workspace modal: a card grid of built-ins plus a compact list of your own templates. Selecting one applies its prefill/behavior/tasks/tools, seeds its agent roster (if any), and, if it has a skeleton, scaffolds the project (optionally set a project name; defaults to the workspace name). The "use any folder as a template" escape hatch lives under **Advanced**. A metadata-only template skips scaffolding entirely.
- **Chat** — agents in a workspace can call `workspace_project_templates` and `workspace_create_project`.
- **API** — `POST /api/workspaces` with `template_id` or `template_path` (+ optional `project_name`); see the [API reference](../api/API_REFERENCE.md#project-templates).

The project folder is created **inside the workspace folder**, as a sibling of `files/` and `notes/`:

```
song-x/                  ← workspace folder
  workspace.json           project_path: "song-x"
  files/                 ← workspace file storage
  notes/                 ← workspace notes
  song-x/                ← the project (from the template)
    song-x.rpp
```

This placement matters: the workspace's auto-provisioned filesystem MCP is rooted at the workspace folder, so agents can read and edit the project immediately — while the attachment reconcile that runs on file-tree loads only walks `files/`, keeping potentially large project media out of that scan.

Because `project_path` is relative and stored in `workspace.json` (its canonical store — there is no SQLite column; reads hydrate from disk), the project travels with the workspace through renames, moves, grouping, and machine migrations. When an entry verifies successfully, its resolved portable path is stored relative to that project as `shared_data.project_entry_path`; session metadata is only a best-effort mirror.

For a library template with a valid entry, Create Workspace shows **Open project after creation** and initializes it from `open_after_create_default`. The user can opt in or out before creating. An opted-in create sends one bodyless request to the fixed local open endpoint, then navigates to the new workspace. Opening failure never rolls back workspace/project creation; the destination shows a one-time warning and the persistent **Open Project** command remains available for retry. Ordinary workspace loads, reloads, chat-created projects, and adding a project to an existing workspace do not launch an application.

**Open Project** asks the operating system to use the file type's default application. Acceptance means only that the OS request was issued; it does not prove that a domain application finished loading the file or that an automation interface such as REAPER Web Remote is ready. Domain skills must verify live state before claiming live changes.

## Project-entry non-goals

- Binding a template to a named application, executable, bundle path, command, arguments, environment variables, or shell hook.
- Running arbitrary post-create scripts or launching on ordinary page load, reload, server start, chat tool use, or background workspace creation.
- Treating an OS-open response as application readiness or bypassing the confirmation/safety behavior of a domain skill.
- Persisting absolute machine paths. Both `project_path` and `shared_data.project_entry_path` remain portable relative paths.

## Managing the library

The filesystem is the primary management surface — but the app provides a full authoring UI over it:

- **`/templates` page** — the dedicated authoring surface. Per template: edit the Overview (name, description, tags, **icon**, **agent behavior**, **project entry**, **starter tasks**), browse/edit Files, configure Tools and Agents, **Duplicate**, **Reveal**, and **Delete**. Built-ins show a read-only badge and disable the mutating controls; **Duplicate to customize** makes an editable copy (the copy is never marked built-in). Mutating a built-in is also rejected server-side with `403` (defense in depth), so the read-only guarantee does not depend on the UI.
- **Manage modal** (reachable from the create-workspace modal and Settings → Project Templates): a lighter list/import/edit/delete/reveal veneer over the same library.
- **Settings → Project Templates**: configure `templates_root` (Browse/Save/Clear), open the library folder, and launch the authoring page. Changing the directory materializes the library there, including any absent built-ins.
- Deleting a built-in lasts until the next server start, which re-adds absent built-ins; duplicate-and-edit instead if you want one gone-but-different.

## Rules and guarantees

- One project per workspace (v1). The chat tool and API refuse when `project_path` is already set.
- Group workspaces cannot hold projects (their MCP roots are scoped to `files/` + `notes/`, so a sibling project would be invisible to group agents).
- Instantiation failures never break workspace creation: the workspace is created without a project and the response carries `project_warning`.
- Entry-only failures never remove a successfully scaffolded project: `project_warning` explains the missing entry and no `project_entry_path` is persisted.
- The project-open endpoint accepts no caller path or request body, is limited to direct loopback requests, and revalidates the persisted workspace/project paths, containment, file type, and every symlink boundary immediately before invoking the system opener.
- A failed copy removes the partial project folder; reserved names (`files`, `notes`, `outputs`, `agents`, `sub-workspaces`, `tasks`, `sessions`) and path traversal are rejected.
- A `project.created` workspace event is published after successful instantiation.
