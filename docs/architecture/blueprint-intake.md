# Blueprint Intake

A manifest step kind is accepted only when `workspace.setupStepKindSpecs` defines its fixed reference scope and adapter requirement.
The project-template validator resolves that reference against declarations in the same manifest and rejects unknown kinds, references, and adapter keys.
At runtime the setup service reads the persisted workspace snapshot and resolves any adapter key through the compiled registry wired by `ServerBuilder.wireSetupWizard`. The manifest never chooses behavior: it can select only host-defined declarations, while collection, parsing, consent, execution, validation, review, application, and automation stay compiled host code.

## Create Workspace inputs

Blueprint inputs may be bounded numbers/selects, one-line text, or HTTP(S) URLs. Only numbers and selects can be substituted into explicitly declared scaffold files. Text and link answers are stored with workspace provenance and never reach scaffolded files under any circumstances. A URL may name a URL-enabled intake key to prefill that intake step; it is not fetched until the user accepts the host-authored consent statement.

## Bundled skills

A user template may carry only `skills/<name>/SKILL.md`; import and duplicate discard every other entry below `skills/`. Declared bundled skills are size- and metadata-validated, copied into workspace provenance as inert exact text, and installed for the entry agent disabled and untrusted. The setup UI renders that exact text for review; a same-name content collision remains untouched until the user explicitly chooses the existing or bundled copy.

## Reviewed record application

The host-owned proposal schema supports tickets, one-line validated memory entries, bounded workspace notes, and single calendar events. Every kind stays inert until an exact proposal hash and selected item keys arrive through Apply. Each item is independent: one failure does not suppress later results. Memory uses the canonical `MEMORY.md` writer, notes use the workspace note store, and calendar events use Calendar Ops' confirmation store and its single confirmed mutation invocation path. When the workspace has no ready create-event mapping, calendar proposals remain visible but disabled as information.

## Source boundaries

Uploaded files are copied into the workspace. Public links are fetched only after provider-aware consent, through the shared SSRF policy: HTTP(S) only, no private or loopback host before or after redirects, DNS checked again at connection time, bounded response size and timeout. Ori stores the fetched page plus its extracted-text hash and text snapshot. Folder paths come only from scoped native-picker tokens; Ori reads no subfolders, resolves every immediate entry before containment checking, ignores escaping symlinks, and stores parsed text without copying the source file.

## Re-intake automation

An intake may reuse the template's declarative `automation_recipes` entry for its declared folder. The ordinary `automation_review` step records explicit approval before the host registers a fixed `blueprint_intake` domain-scan trigger. Manifests choose only events, debounce, and daily wall-clock time; they cannot choose executable behavior. The shared trigger service supplies watcher coalescing and rate limiting, while the re-intake consumer adds one-run-per-workspace/intake locking, one bounded follow-up, and reset work-gate admission. Daily catch-up also re-fetches consented links.

Refresh compares parsed-content hashes and sends only new or changed snapshots through the normal no-tools intake task path. Results are compared with the applied ledger as `new`, `changed`, `unchanged`, or `no_longer_found`; unchanged items are hidden by default and missing items are informational only. A refresh replaces the intake's older pending proposal rather than stacking. It never applies or deletes a canonical record. Applying a changed record updates its ledger-linked record; when the canonical record no longer matches the prior ledger value, review shows both values and requires an explicit keep-current/use-proposal choice.

## Portable state

Intake sources, parsed-text snapshots, proposals, and the applied-item ledger live under the workspace folder's `blueprint-intake/` directory. The folder is the portable truth: this state moves with `workspace.json` and does not depend on a SQLite row or migration. Files use atomic replacement with private permissions. SQLite may index this state later for cross-workspace views, but it must not become the authority.
