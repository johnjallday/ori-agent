# Blueprint Intake

A manifest step kind is accepted only when `workspace.setupStepKindSpecs` defines its fixed reference scope and adapter requirement.
The project-template validator resolves that reference against declarations in the same manifest and rejects unknown kinds, references, and adapter keys.
At runtime the setup service reads the persisted workspace snapshot and resolves any adapter key through the compiled registry wired by `ServerBuilder.wireSetupWizard`.

## Portable state

Intake sources, parsed-text snapshots, proposals, and the applied-item ledger live under the workspace folder's `blueprint-intake/` directory. The folder is the portable truth: this state moves with `workspace.json` and does not depend on a SQLite row or migration. Files use atomic replacement with private permissions. SQLite may index this state later for cross-workspace views, but it must not become the authority.
