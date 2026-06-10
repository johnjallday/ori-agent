# Project Templates

Project templates let a workspace start with a ready-made **project folder** — a Reaper song, a writing project, a code scaffold, anything — without Ori containing any domain-specific code. A template is just a folder; instantiating it copies the folder's contents into the workspace and records the result as the workspace's `project_path`.

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

Two starter templates (`reaper-song`, `writing-project`) are materialized on first start — only when absent, so your edits are never overwritten.

## Authoring a template

Drop a folder into the templates directory. That's the whole registration process — it appears in the workspace-creation picker on next load.

```
templates/
  my-template/
    template.json          ← optional display metadata (not copied)
    {{name}}.rpp           ← {{name}} / {{date}} substituted in file & folder names
    assets/
```

- `template.json` may set `"name"` and `"description"` for the picker. It carries **metadata only** — no hooks, scripts, or post-create actions, by design.
- `{{name}}` becomes the slugified project name; `{{date}}` becomes `YYYY-MM-DD`. Substitution applies to file and folder **names only**; file contents are byte-copied untouched, so binary files are always safe.
- Symlinks are skipped during instantiation (they would break portability or reach outside the template).
- Keep templates small: instantiation is synchronous, and seeds are meant to be starting points, not finished projects.

## Instantiation

Entry points:

- **Workspace creation** — the "Project (optional)" card in the create-workspace modal: pick a library template or any folder on disk ("Choose folder…"), optionally set a project name (defaults to the workspace name).
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

## Rules and guarantees

- One project per workspace (v1). The chat tool and API refuse when `project_path` is already set.
- Group workspaces cannot hold projects (their MCP roots are scoped to `files/` + `notes/`, so a sibling project would be invisible to group agents).
- Instantiation failures never break workspace creation: the workspace is created without a project and the response carries `project_warning`.
- A failed copy removes the partial project folder; reserved names (`files`, `notes`, `outputs`, `agents`, `sub-workspaces`, `tasks`, `sessions`) and path traversal are rejected.
- A `project.created` workspace event is published after successful instantiation.
