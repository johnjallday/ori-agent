---
name: refresh-readme
description: Audit, review, refresh, update, or regenerate Ori's root README.md and its product screenshots through the README Steward workflow. Use for README copy drift, stale product imagery, screenshot recapture, or a monthly README review.
---

# README Steward

Read `docs/README_MAINTENANCE.md` and `docs/readme-screenshots.json` before acting. Treat their commands, screenshot contract, and approval boundaries as authoritative. Do not recreate capture, Playwright, encoding, or acceptance logic.

## Preflight

1. Report the current worktree, branch, and tracked Git status.
2. Confirm the repository folder is the agent's workspace-local filesystem scope, Node dependencies and Chromium are available, and the `refresh-readme` skill is enabled and trusted for the README Steward agent.
3. Stop with the precise missing setup item if any prerequisite, workspace permission, or required browser/build dependency is unavailable. Leave tracked files unchanged.

## Audit mode

Run `make readme-audit` and `make readme-check`. Treat Audit as read-only: do not create a worktree, start a server, create staging output, change tracked files, commit, push, call GitHub, or make external network calls.

Report the audited range, relevant UI drift, contract/link results, and whether a Refresh is recommended. A bootstrap `readme-check` failure is expected until the initial accepted WebP portfolio exists; label it clearly rather than trying to apply assets.

## Refresh mode

1. For a routine monthly refresh, start from clean `dev` with `bash scripts/readme-new-refresh-worktree.sh YYYY-MM`; work only in the printed `docs/readme-refresh-YYYY-MM` worktree. The initial portfolio may use the documented `feature/readme-steward` bootstrap exception.
2. Run `make readme-capture`, then `make readme-propose RUN_ID=<run-id>`. Review the report, proposed README diff, audit, sidecars, and the full-resolution and README-width images.
3. Reject loading/error states, clipping, private data, inconsistent themes, or misleading product states. Use only the running Ori UI and its fictional fixtures—never generated, fabricated, retouched, composited, or presentation-only screenshots.
4. Present the staged paths, validations (including determinism and privacy), exact file effects, and `Checkpoint 1: Apply staged refresh?`. Stop until the user explicitly approves.
5. After Checkpoint 1 approval, run `make readme-accept RUN_ID=<run-id> APPROVE=1`. Present its validation result and tracked diff, then stop at `Checkpoint 2: Commit and open PR?`.
6. Only after separate Checkpoint 2 approval, create the focused documentation commit and run `wt pr`. Never merge automatically.

## Run summary

Return: mode; audited commit range; relevant UI changes; current worktree and branch; staged artifact paths; proposed README edits; validation, determinism, and privacy status; cleanup status; and the next required approval. State explicitly when no tracked files or remote state changed.
