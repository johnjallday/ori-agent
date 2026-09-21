# Blueprint Intake

A manifest step kind is accepted only when `workspace.setupStepKindSpecs` defines its fixed reference scope and adapter requirement.
The project-template validator resolves that reference against declarations in the same manifest and rejects unknown kinds, references, and adapter keys.
At runtime the setup service reads the persisted workspace snapshot and resolves any adapter key through the compiled registry wired by `ServerBuilder.wireSetupWizard`.

## Bundled skills

A user template may carry only `skills/<name>/SKILL.md`; import and duplicate discard every other entry below `skills/`. Declared bundled skills are size- and metadata-validated, copied into workspace provenance as inert exact text, and installed for the entry agent disabled and untrusted. The setup UI renders that exact text for review; a same-name content collision remains untouched until the user explicitly chooses the existing or bundled copy.

## Reviewed record application

The host-owned proposal schema supports tickets, one-line validated memory entries, bounded workspace notes, and single calendar events. Every kind stays inert until an exact proposal hash and selected item keys arrive through Apply. Each item is independent: one failure does not suppress later results. Memory uses the canonical `MEMORY.md` writer, notes use the workspace note store, and calendar events use Calendar Ops' confirmation store and its single confirmed mutation invocation path. When the workspace has no ready create-event mapping, calendar proposals remain visible but disabled as information.

## Source boundaries

Uploaded files are copied into the workspace. Public links are fetched only after provider-aware consent, through the shared SSRF policy: HTTP(S) only, no private or loopback host before or after redirects, DNS checked again at connection time, bounded response size and timeout. Ori stores the fetched page plus its extracted-text hash and text snapshot. Folder paths come only from scoped native-picker tokens; Ori reads no subfolders, resolves every immediate entry before containment checking, ignores escaping symlinks, and stores parsed text without copying the source file.

## Portable state

Intake sources, parsed-text snapshots, proposals, and the applied-item ledger live under the workspace folder's `blueprint-intake/` directory. The folder is the portable truth: this state moves with `workspace.json` and does not depend on a SQLite row or migration. Files use atomic replacement with private permissions. SQLite may index this state later for cross-workspace views, but it must not become the authority.
