# Blueprint Intake

A manifest step kind is accepted only when `workspace.setupStepKindSpecs` defines its fixed reference scope and adapter requirement.
The project-template validator resolves that reference against declarations in the same manifest and rejects unknown kinds, references, and adapter keys.
At runtime the setup service reads the persisted workspace snapshot and resolves any adapter key through the compiled registry wired by `ServerBuilder.wireSetupWizard`.
