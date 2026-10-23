# Visual folder setup: one display seam, existing authority

The folder review, saved setup progress, recovery and receipt stay on one card.
**Full setup details** retains the complete plan/receipt. There is no nested
folder-run modal. The advanced specialist journey remains a separate explicit
**Adjust** / **Continue setup** route.

## Owners

- `personal-assistant-folder.js` retains the drawer/Home offer, request identity,
  draft, polling and response-generation guards. Conversation hydration and a
  pending folder-context review disable mutations against the previous review.
  Re-picking from a Home attention card first restores that offer's conversation;
  a late restore cannot redirect another target.
- `sessions.js` and `create-workspace-build-pane.js` retain the New Workspace
  session, form setters, resume/manual mode, confirmation and run handoff. Their
  build-scoped IDs are not copies of the drawer's IDs. Only the active review or
  pane owns an enabled setup control.
- `folder-setup-visual.js` renders inert diagrams, consequences and milestones in
  either surface. It performs no fetches, confirmations, polling or persistence.
  It creates no setup authority or inferred resource membership.

See [conversation context](assistant-chat-folder-context.md),
[build sessions](workspace-build-sessions.md) and the
[specialist journey contract](specialist-setup-journey-contract.md) for the
underlying permission, consent and lifetime rules.

## Facts and unknowns

`FolderSetupVisual` is a bounded, cloned, server-authored projection carried by
review/build plans and saved runs. Entity and plugin operations, known versions,
placement and applicable stages are display facts. Private intent is not exported.
The projection is excluded from consent digests and is not accepted as setup
input. An old or partial record remains neutral, rather than guessed from prose,
a folder name, a configured plugin, or the plan's expected outcome.

Milestones preserve saved runner order and actual waiting/working/done/failed
states. Task lines are separate from setup milestones. There is no percentage,
elapsed-time progression, forced ordering, or repainting unfinished optional work
as done. A finished runner without its verified outcome is still awaiting the
saved result.

Canonical receipt readers add optional `operation` (create/reuse), `assigned`
(observed workspace membership), `model` (recorded agent configuration) and
`placement` (observed resource placement). These are snapshots, not connectivity
checks. Only an agent row with `assigned: true` confirms membership. A planned
hire, a profile, a saved agent definition or completed staffing milestone does
not. Missing legacy fields remain unreported. Receipt child counts count
workspace rows, not the group's agent rows.

- Projects link to the actual workspace/task/report routes. Setup-ready and
  first-look-ready are independent; a failed look can coexist with a ready
  workspace. Reading a report or opening a suggestion never starts that work.
- Plain groups list their actual member workspaces and manager without inventing
  a first task. Partial member failure keeps completed resources visible.
- Home/library receipts list catalog entries without claiming every entry has a
  workspace. Recent-project shortcuts are one bounded library read for the active
  card, not a new setup poller. The existing canonical Home link becomes **Browse
  all …** when those shortcuts are available. Explicit **Open** uses the existing
  library API and its saved request identity/file-choice rules. Closing or
  switching targets suppresses late navigation; it does not undo accepted work.

## Drafts and presentation

Briefs preserve whitespace and Unicode exactly. The UI counts Unicode code
points against the server's 300-rune limit; it does not truncate at a UTF-16
`maxlength` or trim on submission. Redraws preserve the draft and focus. New
reviews reconcile against their own owner/version without renewing consent.

The renderer uses text nodes, existing building artwork and theme tokens, with
literal text summaries, non-color statuses and decision-first narrow layout.
Unknown identity uses a neutral fallback, not a fabricated portrait/name. Source
folders are kept in place; linking or explicit bounded software review does not
claim to move, copy or execute the source.

## Verification

`tests/visual-folder-agent-setup.spec.ts` labels injected presentation fixtures
and covers drawer/build parity, exact briefs, loading/late-response guards and
closed Home shortcuts. The software and collection E2E scripts exercise real
routes/storage across their restart phases (scripted or no model). These are
not live REAPER/provider/native-picker evidence; those require their separately
provisioned sandbox checks. See the [manual guide](../testing/folder-first-manual.md).
