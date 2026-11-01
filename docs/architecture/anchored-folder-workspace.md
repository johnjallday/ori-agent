# Anchored folder workspaces

Ori distinguishes where work lives from where its own state lives.

| Shape | Work folder | Ori state | Creation |
| --- | --- | --- | --- |
| Managed | Workspace Directory / slug | Same folder | Ordinary New Workspace, templates and groups |
| Anchored | An existing external folder | Workspace Directory / slug (the sidecar) | New Workspace → Use an existing folder; new reviewed File Janitor folder hires |
| Imported | An existing folder | Inside that folder | Advanced Import Folder |

These are derived shapes, not three workspace kinds. Groups remain managed.
Existing Downloads/Janitor workspaces are **not migrated**. Software-project,
song and portfolio setup profiles retain their existing behavior.

## Portable identity and local presentation

The canonical `workspace.json` stores one stable reference ID:

```json
{
  "shared_data": {
    "anchor_directory_reference_id": "folder-id",
    "primary_directory_id": "folder-id"
  },
  "directory_references": [
    { "id": "folder-id", "path": "/Users/example/Documents/Client" }
  ]
}
```

The anchor is also the primary directory. Its path exists only on the reference.
SharedData round-trips through the SQLite cache and disk reload without a schema
column. `shape` is computed as `anchored`, `imported` (the existing folder-import
marker), or `managed`; it is never a second persisted flag. Lightweight SQLite
lists read only the relevant shape markers, not all workspace SharedData.

Detail responses additionally project `anchor`, `sidecar_path`, `home_directory`
and the anchor reference's `missing` status. These describe this machine and are
not saved into portable state. `~` shortening is display-only and applies only
to the current user's exact home prefix. An unconfirmed Workspace Directory may
use staging; the UI reports the real sidecar location rather than inventing a
path under `~/Ori Workspaces`.

## What the sidecar contains

The ordinary slug is unchanged. `workspace.json`, agent state, notes, memory,
backlog and capability state stay in the sidecar. Creation does **not** scaffold
`files/` or `notes/`, link the sidecar as a work folder, or grant filesystem access
to it. Notes and Ori-owned uploads create their directories lazily. A later
`files/` directory is still Ori state, never an automatic agent filesystem root.

Files lists the external **Anchored folder** and any explicitly linked extra
references. The header separately names **Ori's notes and settings**.

## Explicit access, never inferred access

A normal anchored creation writes one enabled `filesystem` binding, alias
`anchor-folder`, over exactly the anchor. Its default side effect is `read` and
its allowlist is explicit:

- `list_directory`, `list_directory_with_sizes`, `directory_tree`
- `search_files`, `get_file_info`, `list_allowed_directories`
- `read_file`, `read_text_file`, `read_media_file`, `read_multiple_files`

No write, edit, create, move, delete or copy tool is included. Users can explicitly
add other bindings through Add Tool. Removing or disabling the default does not
restore access: runtime synthesis, frontend synthesis, Janitor's legacy access
pin and folder-rebase scaffolding all exclude anchored workspaces.

For a new File Janitor hire, its metadata-only `downloads_janitor_root` grant
**replaces** the creation default in the same workspace update that records
capability ownership. The stable binding ID survives. The anchor reference is
shared, so uninstall cannot delete it. Janitor's existing four-tool allowlist,
forbidden-content/mutation policy, consent and filing destination are unchanged.
Its recorded reference remains excluded from generic `AdoptedFolders` advice.
An unrelated explicit user grant is not silently deleted or repointed.

Pinned Toolbox selections are intersected with the binding's current allowlist.
A recipe pinned before Janitor narrowed a grant cannot retain content-reading
operations afterward.

## Creation and ownership

The [API reference](../api/API_REFERENCE.md#anchored-workspaces) documents
`anchor_path` and the read-only chooser check. An anchor must be a real,
non-symlink directory outside the Workspace Directory; the one exception is a
folder inside `<root>/Second Brain/Projects/`, which holds the user's notes rather
than a managed workspace. Existing `workspace.json`
and exact ownership conflicts are refused. The final check and sidecar write use
the project-attachment commit lock; failures roll back newly created resources
without touching the external folder.

Nested anchors are allowed. The chooser names the closest parent anchor's
workspace and offers Open it, without blocking creation or inheriting its access.

Reviewed inbox creation passes the server-held root, not a guessed browser path.
Accepted build drafts reuse that same reference. Existing/adopted workspaces and
assistant setup with no selected folder are not converted. Blueprint agents,
capabilities and tools still install; an explicit anchor skips the blueprint's
second managed project-folder skeleton.

## Lifecycle and recovery

- Rename and group nesting move only the sidecar. Anchor IDs, paths and bindings
  do not rebase, and managed grants are never appended.
- A missing anchor shows **Folder not found** in the header and Files. Nothing
  is recreated or removed. Relink uses the existing folder picker/typed-path
  dialog and a validated anchor-specific update with an expected reference/path.
- Relink retains the default binding's ID, allowlist and enabled/removed state.
  Other user-authored grants keep their own roots. Generic directory PUT cannot
  bypass the anchor ownership check.
- A File Janitor-owned anchor relink uses its existing lifecycle: stop watching,
  invalidate pending approvals, retain history, update the same shared reference
  and scoped binding, and require a fresh monitoring decision. Changing only the
  capability's folder remains distinct from moving the workspace anchor.
- Trash, permanent deletion and restoration operate only on the sidecar, even
  when the anchor is unavailable. Confirmation explicitly says the external
  folder is not touched.
- No general workspace-level Reveal action exists. Project-entry Open Project
  actions are unrelated and unchanged. Native folder dialogs continue to honor
  `ORI_NO_DESKTOP_OPEN`; typed selection works without desktop launches.

## Backup boundary

Copying, syncing or backing up the sidecar covers **Ori's state only**. It does
not copy or protect the anchor's files. Back up that folder independently with
your chosen backup application; see [Backup & Recovery](../backup-and-recovery.md).
File Janitor filing is still not a backup.
