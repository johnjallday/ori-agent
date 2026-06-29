# Project Templates

A **template** describes how a workspace starts. It is presented as a single **Template** picker in the create-workspace modal (there is no separate "starting point" vs. "project template" choice). One template may carry any combination of:

- a **name/description** prefill,
- a default **agent behavior** profile (`general` | `research` | `software_project`),
- **starter tasks** seeded into the new workspace,
- default **tools** (skills / MCP servers / plugins, applied if present),
- an **onboarding** intake flow, and
- a **project folder skeleton** — a Reaper song, a writing project, a code scaffold, anything.

A template that ships a skeleton scaffolds it into the workspace and records the result as the workspace's `project_path`. A **metadata-only** template (no files beyond `template.json`) contributes behavior/tasks/tools but creates no project folder. Either way, Ori contains no domain-specific code: a template is just a folder, and all domain specificity lives in template data.

## Why this design

Ori's capabilities come from MCP servers and skills, not built-in integrations. Project templates follow the same philosophy:

- **The app ships one mechanism** — copy a folder skeleton, substitute names, set `project_path`.
- **Templates carry the domain** — a `reaper-song` template makes Ori useful for music; a `novel` template makes it useful for writing. Supporting a new domain requires zero changes to Ori.
- **Behavior stays in MCP/skills** — anything that should happen *after* instantiation ("open it in Reaper, set the tempo") belongs to a domain MCP or skill, never to the template manifest.

## Where templates live

The library is a directory of folders. Resolution order:

1. `templates_root` in `settings.json`
2. `ORI_TEMPLATES_DIR` environment variable
3. `<data dir>/templates` (default; created on startup)

A set of **built-in** templates is materialized on first start — only when absent, so your edits are never overwritten. These are the five metadata-only starting points (`travels`, `daily-briefings`, `content-production`, `research-project`, `personal-ops`) plus the two folder-skeleton starters (`reaper-song`, `writing-project`). Built-ins are flagged `"builtin": true` and are **read-only** in the authoring UI — use **Duplicate to customize** to make an editable copy.

## Authoring a template

Drop a folder into the templates directory. That's the whole registration process — it appears in the workspace-creation picker on next load.

```
templates/
  my-template/
    template.json          ← optional display metadata (not copied)
    {{name}}.rpp           ← {{name}} / {{date}} substituted in file & folder names
    assets/
```

- `template.json` carries **declarative metadata only** — never executable hooks, scripts, or post-create actions. Recognized keys: `name`, `description`, `tags`, `icon` (emoji shown on the picker card), `behavior_profile` (`general` | `research` | `software_project`), `starter_tasks` (`[{ "description", "details" }]`), `tools` (`{ "skills", "mcp_servers", "plugins" }`), `onboarding` (intake spec), and `builtin`. Unknown keys are preserved untouched.
- A folder containing **only** `template.json` is a valid metadata-only template (it scaffolds no files).
- `{{name}}` becomes the slugified project name; `{{date}}` becomes `YYYY-MM-DD`. Substitution applies to file and folder **names only**; file contents are byte-copied untouched, so binary files are always safe.
- Symlinks are skipped during instantiation (they would break portability or reach outside the template).
- Keep templates small: instantiation is synchronous, and seeds are meant to be starting points, not finished projects.

## Instantiation

Entry points:

- **Workspace creation** — the unified **Template** picker in the create-workspace modal: a card grid of built-ins plus a compact list of your own templates. Selecting one applies its prefill/behavior/tasks/tools and, if it has a skeleton, scaffolds the project (optionally set a project name; defaults to the workspace name). The "use any folder as a template" escape hatch lives under **Advanced**. A metadata-only template skips scaffolding entirely.
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

Because `project_path` is relative and stored in `workspace.json` (its canonical store — there is no SQLite column; reads hydrate from disk), the project travels with the workspace through renames, moves, grouping, and machine migrations.

## Managing the library

The filesystem is the primary management surface — but the app provides a full authoring UI over it:

- **`/templates` page** — the dedicated authoring surface. Per template: edit the Overview (name, description, tags, **icon**, **agent behavior**, **starter tasks**), browse/edit Files, configure Onboarding and Tools, **Duplicate**, **Reveal**, and **Delete**. Built-ins show a read-only badge and disable the mutating controls; **Duplicate to customize** makes an editable copy (the copy is never marked built-in). Mutating a built-in is also rejected server-side with `403` (defense in depth), so the read-only guarantee does not depend on the UI.
- **Manage modal** (reachable from the create-workspace modal and Settings → Project Templates): a lighter list/import/edit/delete/reveal veneer over the same library.
- **Settings → Project Templates**: configure `templates_root` (Browse/Save/Clear), open the library folder, and launch the authoring page. Changing the directory materializes the library there, including any absent built-ins.
- Deleting a built-in lasts until the next server start, which re-adds absent built-ins; duplicate-and-edit instead if you want one gone-but-different.

## Rules and guarantees

- One project per workspace (v1). The chat tool and API refuse when `project_path` is already set.
- Group workspaces cannot hold projects (their MCP roots are scoped to `files/` + `notes/`, so a sibling project would be invisible to group agents).
- Instantiation failures never break workspace creation: the workspace is created without a project and the response carries `project_warning`.
- A failed copy removes the partial project folder; reserved names (`files`, `notes`, `outputs`, `agents`, `sub-workspaces`, `tasks`, `sessions`) and path traversal are rejected.
- A `project.created` workspace event is published after successful instantiation.
