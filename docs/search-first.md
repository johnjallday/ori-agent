# Search one folder on this Mac

On a new local Mac installation, open Ori in the browser. Choose **Documents**, **Downloads**, or **Choose another folder…** to make a folder searchable. Choosing your first folder also makes Search your starting page. **Not now** goes to Home without confirming a workspace directory or completing Welcome. Canceling a folder dialog makes no choice at all.

If you've already set up Ori, use **Search local files** on Home. Visiting Search does not change your starting page. The desk offers an explicit starting-page preference, and **Home** takes you back without changing it. Search doesn't need a provider key, model, assistant, or Personal HQ.

## What the folder map means

The map lists **search sources**, not workspaces or folders an agent can read. Ori indexes names, relative locations, modification dates and local identity metadata. It doesn't extract text, move files, hand their names to a model, or store your queries in note-search history. Directory access is limited to the selected local APFS folder. Private Ori roots, system folders, whole volumes and overlapping sources are refused. System privacy controls can still refuse a selection.

- Type a filename or part of one. When two names match, use the source, relative parent and date to tell them apart. Notes and headings are a separate scope in the search palette.
- Use the arrow keys to select a row. **Enter** requests the displayed reader for a supported document or media file; **Shift+Enter** requests Finder. Unsupported files offer Reveal rather than a launch association. These are explicit desktop actions, not permission for an agent to read the file. A moved, replaced or inaccessible result can no longer be opened from the old row.
- Results arrive in batches. **Partial** means some entries couldn't be mapped, not that there are no more files. Hidden/tooling directories, app bundles, symlinks, other mounts and flagged cloud placeholders are excluded. There are caps on traversal time, depth, entries and index size.
- There is no watcher. Returning to Search shows the saved index; use **Refresh** to map again. Refresh starts a new generation, so deleted or previously indexed files aren't counted as newly verified. If the original root is unavailable, reconnect it and refresh, or remove it and explicitly choose a new source.
- **Remove from search** forgets that grant and its current index. It does not change originals, other mapped folders, notes, macOS privacy settings or historical backups. Removing the last folder leaves an empty search desk. Resetting ordinary workspace setup does not create or revoke grants; deleting app records clears all search sources and results. Start Fresh resets the starting page as well.

The implementation uses fixed system document readers, not “your default app.” The headless demo disables all desktop effects; it cannot verify Finder, a reader, or an OS privacy prompt. Native acceptance requires a separate foreground Mac test account with synthetic files.
