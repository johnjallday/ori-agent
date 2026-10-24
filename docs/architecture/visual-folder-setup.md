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
- `folder-setup-visual.js` renders inert requirements, diagrams, consequences and milestones in
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

Requirements name each independently known integration/Home plugin, its role,
operation, reviewed source and available installed/target versions. Source and
purpose fields are bounded literal metadata, never paths, credentials or parsed
plan prose. **Not checked**, **unavailable** and **unverified** are distinct from
**not installed**. Only a complete `requirements_checked` set can say how many
downloads are needed or that nothing needs downloading. Enable needs no download;
an Ori plugin is not the desktop application, a model runtime or model weights.

A selected creation review may carry `FolderOfferView.Readiness` (or
`BuildFolderPlan.Readiness`) when a prerequisite failure prevents a complete
plan. Independently known rows survive another reader's failure. This projection
has no digest, confirmation authority or executable plan. Unbound/supporting
placement previews, attachment eligibility and model-free group recipes do not
acquire new prerequisite-release reads. Existing eligible review-on-attach remains
an explicit host-owned handoff; it does not install, create or call a model.

Milestones preserve saved runner order and actual waiting/working/done/failed
states. Saved requirements use the run's recorded visual metadata, never a new
plan's identity/model. Versions say installed-at-review or reviewed target;
legacy line-only identity stays generic. A named failed prerequisite retains
completed work, without inferring a network cause from a generic install stop. Task lines are separate from setup milestones. There is no percentage,
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
  **Open the full report** uses the existing server-authored `report_route`,
  not `ticket_route`: the ticket panel shows work/status, not the report body.
  A legacy ticket-only record retains **Open task**, without promising a full
  report. No new route, ticket field, result copy or execution action is added.
- Plain groups list their actual member workspaces and manager without inventing
  a first task. Partial member failure keeps completed resources visible.
- Home/library receipts list catalog entries without claiming every entry has a
  workspace. Recent-project shortcuts are one bounded library read for the active
  card, not a new setup poller. The existing canonical Home link becomes **Browse
  all …** when those shortcuts are available. Explicit **Open** uses the existing
  library API and its saved request identity/file-choice rules. Closing or
  switching targets suppresses late navigation; it does not undo accepted work.

## Applicable project model and explicit return

The model row concerns the project's actual agent, not the conversational model.
Resolution follows saved agent, resolved roster, then the new-agent system model.
A missing bound-agent configuration does not fall back to the system model;
a missing/unreadable blueprint is **not checked**, not proof of missing
configuration. **Configured** describes local settings, not connectivity or a
successful model execution. Applicable model facts are reread independently of
the 15-second release-facts cache.

A known missing configuration replaces confirmation with its compiled Settings
or agent-configuration route, including returning from Adjust without a one-card
run. That guided continuation keeps its own owner and is not eligible for a
pending-review recheck. The separate tab keeps the original folder,
conversation, exact draft and brief; blocked popup opening is reported. **Check
again** is an explicit observational read through the existing canonical review
request (`recheck: true`), guarded by owner/revision/generation and live selection.
An exact current review refreshes in place, without another history turn. A
one-request invalidation token and cache epochs fence stale release reads.
Returning, configuring or rechecking never installs, confirms, retries or starts
a first look. Accepted work keeps its recorded plan and recovery path.

Display metadata stays outside consent. A separate server-authored
`FolderSetupModelIntent` binds a known material model choice to the plan digest
and is checked before effects through the existing destination guard. A changed
model requires changed-plan consent; it cannot silently refresh an accepted run.
Legacy/pending-blueprint plans keep their existing validation and recovery gates.

New Workspace uses its own `GET …/build-sessions/{id}?recheck=1`, only for an
open, unaccepted build. An unavailable integration offers Check requirements
before model metadata exists; attachment itself does not acquire those additional
partial release reads. Its adapter updates readiness and authoritative folder
expiry, not wizard fields or the brief, and rejects changed session/version/local
revision/observation identity and accepted work. A lost hold requires re-pick. Failed/late reads disable stale confirmation. A canonical replacement
folder response retires the old check failure; it must not gate a model-free
replacement. It does not share the assistant's request, polling or action owner.

## Drafts and presentation

Briefs preserve whitespace and Unicode exactly. The UI counts Unicode code
points against the server's 300-rune limit; it does not truncate at a UTF-16
`maxlength` or trim on submission. Redraws preserve the draft and focus. New
reviews reconcile against their own owner/version without renewing consent.

The initial attachment is compact metadata evidence, not an empty destination
diagram. A single recognized project opens **What you need** immediately, including when
its plugins cannot yet provide a complete plan. Other candidates keep the one
Review setup control and explicit choice. The assistant adapter adds per-package
Install/Enable/Update controls to the inert requirement rows. Each first expands
the existing complete trust/source/version review inline, then requires its own
plugin-only confirmation. Integration install and enable remain distinct actions;
Home-provider confirmation discloses its existing install-and-enable behavior.
No new tab, workspace creation, staffing or inference is implied. The server
rechecks canonical ownership, live selection, unaccepted status and destination;
a package click cannot bypass changed consent or an existing Home's upgrade gate.
Missing-Home Guided Setup remains a legacy fallback where inline repair is not
available, never an override of lost selection or changed destination.
The separately confirmed guided action retains its existing owner: once its
adapter Commit returns, receipt/reconciliation finalization has a bounded
10-second context independent of a closed browser's request. Commit itself keeps
its original context; this neither repeats a mutation nor authorizes a cancelled
unclaimed request. Indeterminate effects stay reconciliation-required, and old
stranded claims are not migrated or reset by this change.

Requirements and complete material consequences precede a smaller proposed
outcome. An installation-labelled button still confirms the entire disclosed
setup, including resource creation, linking, shared staffing and first look; it
is not a second plugin-only approval. Full details remain available. Saved
recovery is revealed before the diagram on initial load/current conversation
return, without stealing focus or repeatedly overriding reader intent.

An offer card draws the diagram only from data it was given: a resolved receipt,
or a saved run or plan that carries `visual`. A run saved before the diagram
carried its own data has no `visual`, and the card then draws no diagram at all
(`folderDiagramVisible` in `personal-assistant-folder.js`). The renderer's
fallbacks ("Setup preview", "Not reviewed", "No destination proposed") exist for
the local-observation preview and the build pane, which pass their own evidence;
on a saved-run card they would read as state Ori does not know.

The renderer uses text nodes, existing building artwork and theme tokens, with
literal text summaries, non-color statuses and decision-first narrow layout.
Unknown identity uses a neutral fallback, not a fabricated portrait/name. Source
folders are kept in place; linking or explicit bounded software review does not
claim to move, copy or execute the source.

## Verification

`tests/visual-folder-agent-setup.spec.ts` labels injected presentation fixtures
and covers drawer/build parity, exact briefs, loading/late-response guards,
closed Home shortcuts, viewport reflow and actual 200% Chromium Page zoom in a
disposable profile. `tests/assistant-setup-readiness.spec.ts` exercises fresh
Home/Settings metadata and review without model or install effects. Its explicit
`ORI_READINESS_INSTALL=1` case additionally follows the two real inline package
review/confirmation controls (including Home-plugin-first order), stopping at
model configuration. Earlier Guided Setup evidence is recorded separately;
`tests/folder-readiness-model-return.spec.ts` uses real provisioned packages and
Settings with an expressly controlled loopback provider. Provisioned packages
do not prove fresh-card installation. The software and collection E2E scripts exercise real
routes/storage across their restart phases (scripted or no model). These are
not live REAPER/provider/native-picker evidence; those require their separately
provisioned sandbox checks. See the [manual guide](../testing/folder-first-manual.md).
