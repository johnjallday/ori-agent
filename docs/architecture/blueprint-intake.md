# Blueprint Intake

A manifest step kind is accepted only when `workspace.setupStepKindSpecs` defines its fixed reference scope and adapter requirement.
The project-template validator resolves that reference against declarations in the same manifest and rejects unknown kinds, references, and adapter keys.
At runtime the setup service reads the persisted workspace snapshot and resolves any adapter key through the compiled registry wired by `ServerBuilder.wireSetupWizard`.

## Bundled skills

A user template may carry only `skills/<name>/SKILL.md`; import and duplicate discard every other entry below `skills/`. Declared bundled skills are size- and metadata-validated, copied into workspace provenance as inert exact text, and installed for the entry agent disabled and untrusted. The setup UI renders that exact text for review; a same-name content collision remains untouched until the user explicitly chooses the existing or bundled copy.

## Portable state

Intake sources, parsed-text snapshots, proposals, and the applied-item ledger live under the workspace folder's `blueprint-intake/` directory. The folder is the portable truth: this state moves with `workspace.json` and does not depend on a SQLite row or migration. Files use atomic replacement with private permissions. SQLite may index this state later for cross-workspace views, but it must not become the authority.
