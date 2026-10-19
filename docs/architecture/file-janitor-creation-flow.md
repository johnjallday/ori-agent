# Reviewed File Janitor creation and preparation

Status: implementation contract; acceptance evidence remains in the feature's
findings log. This document does not claim the complete journey is implemented.

## Boundary

Attaching a folder is not File Janitor consent. The host offers a separate review
of the current workspace draft/team, exact folder, metadata-only scope, creation
or reuse of `Filed/`, and optional monitoring. **Create & prepare review** (or
**Use attached …** for an existing workspace) authorizes that reviewed operation.
Model `ready`/`create_now`, viewing a card, linking, and generic wizard actions
cannot supply that authorization. Ordinary workspace creation keeps its existing
no-grant behavior.

No file content is read by preparation; only top-level metadata is scanned.
Every move/Trash still requires the existing file-action review. Build chat has a
different disclosure: its path-free folder metadata can reach the configured
model. Do not describe that conversation as necessarily on-device.

## Owners and selected seam

- `sessionhttp` owns exact draft/team validation and creation, reserved workspace
  identity, operation-stamped profile/workspace provenance, and build selection.
  The one-role default `CreateReviewedFileJanitor` request is **not** the edited
  draft. The new caller must enter the same normal creator with the real draft.
- `filejanitor` owns folder identity, ownership checks, grant markers, mode
  decision, monitoring and scan receipts. Its existing domain methods remain the
  only grant/scan authorities.
- Extract a narrow preparation function from the existing server
  `assistantSetupSessionAdapter.PrepareFirstReview`: reviewed monitoring (when
  selected) and one operation-aware scan. Both callers use that function; manual
  preparation does not need a hired Assistant, watcher, or scheduler. Do not add
  another journey engine or make the assistant's run table a second source of
  setup truth.
- `setupwizard` continues to derive readiness using the registered File Janitor
  adapter, including persisted `downloads_janitor` snapshots. The browser never
  marks steps complete or hides unfinished setup to simulate readiness.

## Review and receipt protocol

### Existing workspace

Closed owner-scoped API under `/api/workspaces/{id}/file-janitor` (also available
under the retained `downloads-janitor` alias):

- `GET /setup-review`: read-only projection with state, safe candidate reference
  IDs/names, disclosure version, effective schedule, current configuration and
  an opaque revision. A details field may show the full path **only to the
  authenticated owner**. No grants, operation claims, `Filed`, watcher or scan.
- `POST /setup-review`: explicit selection of `reference_id` and `monitoring`
  returns the current review. It is still read-only; supplying a reference is not
  consent. Unknown fields/foreign references are refused.
- `POST /prepare-review`: `{reference_id, monitoring, review_revision}` confirms
  exactly that review.
  The server claims or replays its stable operation before any domain effect.
- `POST /stop-preparation`: `{operation_id}` joins any already-started domain
  stage, pauses monitoring and releases only this operation's unfinished claim.
  It retains the workspace, adopted access, `Filed/` and saved results. It does
  not undo filesystem changes or clear an unrelated legacy grant claim.
- All three POST bodies are closed JSON with a 4 KiB bound; review reads and
  mutation replies are `no-store`.
- Retry uses the accepted operation identity and revalidates current authority;
  it does not reinterpret an old request as a new review or widen permissions.

Only canonical user-visible directory references outside the workspace's own
folder are candidates. A primary-directory flag, scaffold, suggested Downloads
path, or workspace name is not a candidate selection. Multiple candidates require
an explicit choice. Configured, revoked, unknown legacy-empty, conflicting and
unavailable states are distinct; existing configurations route to their existing
settings rather than being reprovisioned.

### Before creation

The build-scoped read/confirm pair binds the owner, build ID/version, exact
normalized create request and effective team plan, current blueprint digest,
held selection ID and canonical filesystem identity, privacy, mode, local daily
time/timezone, watcher events/debounce/exclusions, and disclosure version 1.
No consent field is added to the model-patch schema.

The build API is `POST /api/workspaces/build-sessions/{id}/janitor-review`
with `{version, monitoring}`, then `POST .../janitor-prepare` with
`{version, monitoring, review_revision}`. The first is read-only and returns the
actual draft summary, effective team, owner-only folder details, terms and opaque
revision. The second is explicit confirmation. Neither accepts paths, a replacement
draft, an operation ID or a reserved workspace ID. The server reads the canonical
BuildDraft and uses a server-only preflight hook in the normal create pipeline,
after all applicable gates and before the first agent/workspace write.

An accepted build claim retains random operation/workspace IDs, accepted version,
review/root/terms digests, selected mode and acceptance time. An in-flight build
is frozen against all ordinary edits/turns; its current draft is the accepted
request until normal creation settles it. A created workspace's server-only
provenance binds the operation, linked reference, original capability install and
effective attached team/configuration digest. Canvas node IDs are excluded from
the team digest because the normal folder mirror canonicalizes them; role,
attachment identity, custom instructions and profile settings remain bound.
A lost response is reconciled from those markers, not current profile names.
If a partial core/profile write cannot prove completion, report reconciliation
required and do not run creation again. Once a domain preparation is accepted,
its own canonical receipts own retry and the initial result.

`POST .../janitor-defer` with the accepted `{version, monitoring, review_revision}`
settles an accepted build as abandoned,
retaining its claim/reserved identity so an old request cannot create again.
Where the exact domain operation can be proven it joins/stops that preparation;
it preserves later unrelated settings/monitoring decisions. A separate new build
is then allowed. A deferred claim cannot be retried or reused by ordinary create.

Resolve the unexpired hold and validate the normal create gates **before**
reserving/creating resources. The path-free observation cannot revive an expired
hold. Reserve one random workspace ID and stable operation IDs in the accepted
build record; persist the exact typed draft/team evidence there, not arbitrary
HTTP JSON. Lock/freeze that accepted build against late turns and form edits. Preparing
claims do not expire as ordinary open drafts or permit a second open build.
Normal unknown/settled-build permissiveness is not reviewed-create authority.

Concurrent submits join/replay the accepted operation, never allocate another
workspace ID. On lost response, observe the reserved ID and immutable operation
markers. A successful create changes a team action from create to reuse, so a
fresh name-based plan is not replay proof. A partial core/profile write with
unproven ownership stops for reconciliation rather than recreating by name.

## Ordered consequences and recovery

1. Enter the existing reset WorkGate and owner/build or workspace single-flight
   boundary. Re-read ownership, active lifecycle, installed capability and
   review evidence. Do not keep a storage transaction open across domain work.
2. For creation, reserve the claim; normal create/team pipeline persists the
   workspace and links the exact held selection. For existing setup, no workspace
   or profile creation occurs.
3. Persist the domain pending folder grant before `Filed/` or workspace-access
   consequences. Configure paused, reusing shared-resource ownership records.
   Revalidate the root identity and overlapping claims at the consequence
   boundary; a symlink swap/replacement is not the reviewed root.
4. Persist the explicit manual decision, or separately reviewed monitoring
   approval/activation. Manual leaves automation unapproved and inactive.
   Monitoring success requires actual watcher and scheduler health.
5. Run/replay the stable metadata-only scan operation. Batch and no-eligible
   outcomes are atomically receipted with scan state. Never derive result counts
   from the attachment observation or infer success from the latest batch.
6. Re-evaluate canonical wizard readiness and existing idempotent completion
   hooks. Land on the existing Review console using canonical workspace route
   identity and the actual result. Recovery links are emitted only for an
   owner/operation-proven canonical folder slug, never a reserved UUID or guessed
   draft name. Empty and settling results are legitimate.

After creation, failure preserves the exact workspace and earlier grants and
returns a bounded stage/domain code with the stable operation/workspace ID and
repair action. `monitoring_incomplete` distinguishes failed registration from
`initial_scan_incomplete`; `result_observation_incomplete` means a canonical scan
result exists but a remaining detail could not be confirmed. A constructed,
uncommitted scan result is not proof that a result was saved. It never sends the user through another create. Scan retry
cannot apply grants or change monitoring. Manual settling offers **Scan again**;
no hidden polling/rescan task. A historical result may be shown after later
settings changes, but those changes prevent any further old-authority work.

## Storage and compatibility

Selected settings schema: **2 → 3** in the existing domain settings file.

- `setup_decision`: optional typed `{mode, root_id, decided_at}`. Modes are
  `manual` and `monitoring`; missing/unknown/incomplete/root-mismatched decisions
  and decisions in an older/future settings schema never become approval. Explicit manual-ready requires configured current root,
  a matching manual decision, paused automation and no automation approval.
- `access_revoked_at`: a durable tombstone retained when settings are cleared.
  New explicit folder permission may clear it; mere reads/retries may not.
- `setup_preparation`: one bounded domain-owned accepted operation with `id`,
  `owner_user_id`, `reference_id`, `review_revision`, `binding_revision`,
  `capability_revision`, permission-bearing `access_revision`, `monitoring`, typed
  `terms`, and `accepted_at`. `scan_dispatched_at` records dispatch intent and
  `scan_receipt_digest` pins an observed receipt. Both are refusal-only fences:
  neither proves completion, contributes counts, nor replaces a canonical result.
  Completion stays in existing grant/decision/scan receipts, not duplicated stage flags. Paths stay in the existing domain pending grant/settings and workspace
  directory store, never in generic coordinator receipts or model payloads.
- Existing scan-state receipt retention remains bounded. A missing, corrupt,
  changed or pruned receipt after dispatch is unresolved, not permission to scan
  again. Only the in-memory attempt which just persisted dispatch intent may
  start its first scan without a receipt; restart cannot reconstruct that proof.
  An interruption between dispatch intent and result publication therefore
  requires explicit fresh Scan now, not a guessed initial retry. Fresh manual
  scanning closes an unconfirmed preparation without changing saved access,
  mode or monitoring; completed retained operations still replay their receipt.

Build sidecar: **1 → 2** for `BuildPreparationClaim` and the frozen `preparing`
status. Decode legacy v1 without fabricating consent, then write v2. Keep the
existing filename and Windows implementation in sync. Retain at most the current
bounded number of builds; retain only stable outcome identity after completion,
not a transcript/path/token. Missing/unknown claims cannot trigger create replay.
No new database/reset tables are needed for workspace/build-owned records.

Capability definition stays **1**: installs and ownership contracts are unchanged.
Built-in File Janitor copy version advances **4 → 5** with UI/disclosures;
legacy alias identity stays intact. Wizard schema/step IDs stay unchanged: the
adapter recognizes a real manual decision for existing `automation_review` and
readiness steps. No bulk rewrite of snapshots or user-authored templates. Existing
approved/paused/running states preserve their existing meanings. A new manual
choice must never fabricate `automation_approved_at` or say “Paused by you.”

| Canonical state | Setup meaning | Automation |
| --- | --- | --- |
| No configured root | Needs folder permission | None |
| Folder configured; no decision/approval; paused | Unfinished legacy/manual wizard | Inactive |
| Current explicit manual decision | Ready · Manual scans | Unapproved, inactive |
| Approved and healthy | Ready · Watching | Active |
| Approved then user-paused | Ready · Paused by you | Inactive |
| Approved but registration failed | Needs attention | Never claim Watching |
| Revoked/legacy-empty-uncertain | Explicit repair/new folder permission | None |

## Invalidation and lifecycle

Bind each consequential continuation to exact owner/workspace/capability install,
root generation and filesystem identity, settings revision, mode/privacy,
schedule/recipe and disclosure version. Folder/draft/team/blueprint changes stale
unaccepted reviews. Manual pause/edit/relink/revoke clears continuation authority.
Initial reviews include the current permission snapshot. At continuation and
inside canonical workspace-access writes, both primary and folder views must
match either that original snapshot or the canonical metadata-only resources
already established by this operation. Changed/disabled/removed bindings or
ownership associations are not silently repaired, including after a partial
workspace commit. Such a preparation exposes Stop, not an enabled Retry.
Known revocation remains distinguishable even if an adopted link survives.
An older empty/corrupt record, orphaned scan state, or unexplained owned-access
resources are uncertain: require explicit folder repair rather
than granting from a surviving reference. Archive/delete/ownership change,
capability removal/reinstall and reset cannot be undone by stale pages or retries.

Reset admission fences finite work before reads/mutations; no browser timer or
startup sweep continues preparation. Retained detached workspace files after
reset are inert evidence, not permission. User-owned `Filed/` and adopted links
are never deleted as compensation. Safe errors contain bounded codes and exact
repair actions, not OS paths, selection tokens, filenames or raw store errors.

## Validation boundary

Package tests use temporary roots and operation/replay fault injection. Browser
acceptance must use real create/grant/settings/scan APIs; model and chooser
responses may be scripted and labelled. Real native choose/cancel/focus is a
separate supported-desktop check, never inferred from `selected:false` when
`reason:dialog_off`. Both themes, keyboard, narrow layout, zoom and reduced motion
remain delivery requirements. Tests and receipts are not evidence that the whole
journey or a foreground picker was exercised.
