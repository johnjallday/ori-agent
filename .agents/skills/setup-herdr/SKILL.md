---
name: setup-herdr
description: Configure, diagnose, and operate Ori's Herdr devflow bridge through the selected external Ori devtools. Use for bridge setup, managed agents, continuations, standalone macOS wake support, and Overnight Runs.
---

# Set Up Herdr — delegated operating skill

Keep the target Ori checkout distinct from tool source and installed runtime.
Read Ori's `AGENTS.md` and `docs/devtools.md` first. Resolve the exact trusted
operating skill with the Ori-owned read-only entrypoint:

```bash
bash scripts/devtools-setup-skill.sh
```

Read the returned absolute `SKILL.md` **in full**, then follow its referenced
source documentation and authorization boundaries. The selector honors explicit
`ORI_DEVTOOLS_HOME`, otherwise the single approved durable source location; it
never searches, downloads, installs, or falls back. Missing/incompatible source
or skill is a refusal: ask the user to restore/select reviewed source, not an
excuse to perform setup from remembered commands.

The companion owns operating instructions and assets. Ori retains configuration,
plans, its canonical task-planning skill, and product mechanics. Resolving this
skill authorizes no agent launch, runtime refresh, service change, wake scheduling
or global skill installation. Installed runtime health remains unverified until
separately inspected; source selection is not activation.
