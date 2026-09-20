# Assistant-led File Janitor setup contract

**Status:** Implemented on `feature/assistant-led-setup`; final repository gates and foreground native-picker evidence remain pending. This document records both the findings baseline and the delivered contract. Native selection, cancellation, and nested-focus claims remain explicitly NOT RUN where noted.

## Evidence baseline and preflight

### Source identity

- Canonical repository: `https://github.com/johnjallday/ori-agent.git`.
- Authorized checkout: `/Users/jjdev/Projects/ori/worktrees/assistant-led-setup` on `feature/assistant-led-setup`.
- Actual feature base and HEAD at preflight: `38a9394445e2572a82f0d69f793e1219ce627a25` (`feat(groups): support independent music production homes (#527)`).
- `git merge-base HEAD origin/dev`, the local `origin/dev` tracking ref, and a read-only `git ls-remote origin refs/heads/dev` all resolved to that same revision. There is therefore no source drift from the task list's reviewed revision.
- The PRD's older evidence baseline is `ba462199`. Six commits are present between that baseline and the implementation base: #521, #523, #524, #525, #526, and #527.
- The tracked checkout was clean before this findings document was created. Ignored local `.env`, dependency/build output, and `tasks/` artifacts were observed and left untouched. No shared or real user state was reset or repaired.

Repository guidance was re-read from `AGENTS.md` and `.agents/skills/task-planning/SKILL.md`. `docs/agents.md`, which older guidance referenced, is still absent at this revision; it is not treated as an implicit contract.

### Relevant drift since the PRD baseline

#### Root-agent storage (#521)

Agent definitions no longer have one uniform data-directory location:

- The built-in Assistant remains in the data-directory system store.
- User-owned agents normally live at `<workspace root>/Agents/<Name>/agent_settings.json`; runtime state remains under the data directory's `agent_state` tree.
- `store.CompositeStore` presents system, root, and trusted workspace-only agent copies through the ordinary `store.Store` interface. A workspace-only copy may be visible from `GetAgent`, but it is not a user-owned editable profile. New definitions require an available workspace root and can fail with `ErrAgentRootUnavailable`.
- A template still creates a user-owned root agent and attaches a separate workspace copy. Rollback may delete only definitions created by the failed request; it must never delete a reused root profile or a workspace-owned copy.
- Top-level workspace slug `Agents` is reserved because that folder now owns user agent definitions.

**Binding consequence:** assistant-led setup cannot use display-name lookup or generic `GetAgent` presence as proof that a File Curator is safe to reuse. Its reviewed plan must distinguish system/root ownership from workspace-only origin and bind the exact create/reuse decision. A missing root is a truthful setup blocker, not permission to fall back to a different store. The resulting workspace attachment must preserve the current root-agent/workspace-copy behavior.

#### Strict reviewed team creation

The current production creator has a strict path, but it remains opt-in rather than being implied by a template ID:

- `template_agent_review.version == 1`, a complete expectation list, and the server-generated plan revision are validated before any agent or workspace write.
- The revision covers effective template identity, model defaults, Assistant Program/group requirements, definitions, and create/reuse outcomes.
- Strict seeding rechecks every create/reuse expectation immediately before that operation. A mismatch returns a fresh plan. Failures roll back only `OwnedNames`; reused profiles remain untouched.
- Workspace persistence occurs after roster seeding. Current rollback tests cover core-store and folder-slug failures, while successful provisioning warnings intentionally retain created resources.
- The built-in File Janitor template is an ordinary template with one `File Curator`; it is not an Assistant Program/Assistant Project template. #527 broadened managed-team detection and effective plan derivation for independent Assistant homes, so assisted setup must use the current server-derived plan rather than reconstructing older assumptions.
- `CreateFromTemplate(ctx, name, templateID)` sends only `name` and `template_id` through the legacy production request. It neither supplies a strict team review nor establishes an assistant-setup operation owner. It can also seed starter tasks later in the creation pipeline.

**Binding consequence:** `CreateFromTemplate` is not the assistant-led commit contract. The assisted path must enter or extract the reviewed production creation seam with an exact current plan, strict create/reuse expectations, no automatic starter-task execution, and a durable operation identity that can reconcile partial ownership. Unknown or changed blueprint/team evidence fails closed.

#### Reviewed workspace deletion (#525)

Required group-contract workspaces now use a separate, one-time deletion review:

- The review binds owner, workspace, delete-sessions choice, host-derived Trash behavior, template/definition snapshot, and immutable creation-operation digest.
- Commit re-reads canonical folder-backed state, rejects changed topology or provenance, and consumes the receipt once.
- The folder store accepts only the exact reviewed operation digest and still rejects live Assistant Program membership or another protected Required descendant.
- Ordinary workspace deletion/Trash remains owned by the existing workspace lifecycle; this feature must not add a parallel delete or cleanup route.

File Janitor has no Required group contract, so this does not add a direct dependency. It does strengthen the lifecycle rule for this feature: a run whose workspace is archived, trashed, deleted, ownership-changed, or otherwise absent must invalidate/stop. It must not recreate that workspace from a stale setup approval. Any later destructive cleanup remains under the existing reviewed workspace lifecycle.

#### Independent Home and setup-journey changes (#527)

#527 added separately owned Assistant Home/project providers, provider-specific role sources, and current-plan derivation in creator and `setupjourney` staffing code. File Janitor does not declare those contracts, but these changes touch the shared creator/team-review seams. The feature must consume the effective built-in File Janitor plan after current template resolution and must not generalize its coordinator into an Assistant Program or generic setup graph.

The release-candidate commits (#523/#524) and map light-mode fix (#526) do not change the File Janitor setup authority. The Home work in #527 may affect later panel-placement characterization and will be rechecked there.

### Companion dependency revalidation

**Assessment: not required.** The implementation source needed by the approved scope is in this repository at the base above:

- File Janitor blueprint: `internal/projecttemplates/starter/file-janitor/template.json` (built-in version 2).
- Compiled capability and legacy aliases: `internal/workspacecapability/builtin_file_janitor.go` (definition version 1).
- Setup/runtime/scanner and HTTP boundaries: `internal/filejanitor/` and `internal/filejanitorhttp/`.
- Native helper source: `cmd/folder-picker/`.
- Helper build entry point: `scripts/build-folder-picker.sh`, which derives `cmd/folder-picker` from that script's own repository root.

Generated Wails bindings, ignored binaries/app bundles, and any installed helper are validation artifacts, not editable companion source. No plugin, shared SDK, reviewed pin, external provider, or separately authorized worktree is required.

The live spike found Wails CLI `v2.15.0` already installed and ran `./scripts/build-folder-picker.sh` from this feature checkout. The command reported its frontend directory as this checkout's `cmd/folder-picker/frontend`, built a self-signed universal macOS app, and copied it to this checkout's ignored `bin/ori-folder-picker.app`. The copied executable and the build output were byte-identical (`sha256 fa79df95c91d212cb1071c41512549b20bdfe03af4f72535f900e8892d3c5e17`), contain arm64 and x86_64 slices, and declare bundle ID `com.wails.ori-folder-picker`. The build caused no tracked source or mode change.

If later discovery finds required source outside this repository, this assessment becomes `unresolved`; only dependent native/live proof may continue after a separate authorized companion handoff. Installed software will not be edited as a shortcut.

## Existing manual baseline and shared-panel findings

### Isolated environment and native helper

The baseline ran on macOS 26.3.1 (Darwin arm64) from this feature checkout. `scripts/demo-server.sh` built the current working tree and served it on port 8765 with both `HOME` and `ORI_DATA_DIR` redirected to a disposable `ori-assistant-led-setup-manual.*` sandbox. Every selected/test folder lived under that sandbox; no real Downloads/Desktop, user agent store, plugin store, or app data was read or changed.

Native-helper observations are deliberately split:

| Observation | Result |
|---|---|
| Current-checkout helper build | **PASS.** Wails built the repository-owned universal app described above. No installed plugin/source copy was involved. |
| Helper launch/control API | **PASS.** The server launched that exact app from this checkout; PID command path resolved under this checkout, bundle ID was `com.wails.ori-folder-picker`, and `GET 127.0.0.1:21547/health` returned `{"running":true}`. |
| Helper unavailable/fallback | **PASS.** With only the two ignored checkout build artifacts temporarily moved aside and no control listener running, `POST /api/folder-picker/select-path` returned HTTP 404 and `Folder picker app not found. Please build it with: ./scripts/build-folder-picker.sh`. The existing File Janitor console rendered the same actionable error. |
| Native selection success | **NOT RUN.** The coding-agent process could launch the helper/control listener but could not bring its AppKit panel into the foreground. System Events reported no helper window; no user selection was fabricated. |
| Native cancellation | **NOT RUN.** For the same reason, an actual Cancel action could not be delivered. A selection request waited for the helper and returned the server's 90-second timeout error. Killing/quitting the helper is not cancellation evidence and is not counted as such. |

This limitation blocks native success/cancel proof only. It does not authorize a typed-path fallback in the assisted card and does not block package/unit/read-only implementation work. The final supported-desktop demo must repeat real selection and cancellation from an interactive foreground session; a mocked picker may cover browser focus/error rendering but cannot close this evidence gap.

### Manual File Janitor path

A real built-in `file-janitor` blueprint workspace and a separate in-place install were created only in the sandbox. The blueprint path produced the following canonical facts:

1. Before folder consent, Setup Wizard reported `folder` active and automation/readiness pending; File Janitor reported `metadata_only` and no root.
2. `POST /api/workspaces/{id}/file-janitor/setup` with `paused:true` created/reused `Filed`, persisted the directory/root IDs, left `automation_approved_at` zero, and reported watcher/scheduler pending with “Paused by you.” The two synthetic source files were untouched.
3. The wizard then exposed the separate automation disclosure. Confirming `steps/automation/confirm` recorded a nonzero approval time, started one healthy watcher, and reported watcher/scheduler `ok`; folder approval alone did not do this.
4. A manual metadata scan created one pending batch with two unselected candidates (`invoice.pdf` → Documents and `photo.jpg` → Images), classified by `metadata`. It reported `Filed` as ineligible and moved/trashed nothing.
5. The canonical deep link `/workspaces/assistant-setup-blueprint-matrix?panel=file-janitor` opened the existing console directly to its visible Review batch with the same two pending candidates. No second reviewer is needed.

Measured local API durations from a separate fresh one-file sandbox fixture were: reviewed template workspace creation 0.010 s, paused folder grant 0.014 s, automation confirmation/readiness 0.010 s, metadata scan 0.005 s, and review load 0.001 s. These are one-machine service timings, not user-time or production performance claims; they exclude creator interaction, native picker/OS time, download settling, and human review.

From an already selected File Janitor blueprint, the existing manual path requires at least three post-creation user decisions before a useful review: native folder selection/grant, automation approval, and a separate **Scan now**. Creating the workspace itself remains the creator's reviewed decision and route. The observed app-surface sequence is creator → workspace wizard → File Janitor review console (one page navigation to the workspace and one in-page handoff); native selection is a nested OS surface. The assisted target still has three consent moments overall—reviewed setup, folder selection, monitoring plus initial scan—but removes the separate manual scan action and avoids a required app-page navigation before reporting the first result.

Sanitized existing-behavior evidence is stored at:

- `tasks/screenshots/assistant-led-setup/existing-manual-file-janitor-review.png` — real sandbox batch, cropped to exclude the raw local path;
- `tasks/screenshots/assistant-led-setup/existing-native-picker-unavailable.png` — real server/helper-unavailable error;
- `tasks/screenshots/assistant-led-setup/existing-pre-hq-personal-assistant-panel.png` — mocked relationship state solely to expose the real shared panel layout; it is placement evidence, not delivered setup evidence.

### Shared Personal Assistant panel characterization

At a 375×812 viewport, the real drawer was exercised on Home and `/agents` with mocked `needs_hq`, `active`, and `paused` relationship reads. In all six cases the panel was 375 px wide with no horizontal overflow and retained the real Assistant name. Home alone rendered Today/Ask tabs; non-Home pages are Ask-only. `needs_hq` kept the composer disabled and exposed the existing **Build Personal HQ** briefing/link. `active` and `paused` kept direct Ask available; paused displayed its proactive-pause statement. No model was configured in any case.

Focus behavior also constrains the mount: Home opening focuses its selected tab, active/paused Ask focuses the composer, and a disabled pre-HQ non-Home composer cannot receive focus. The setup card therefore needs its own explicit-entry focus target and must not repurpose the composer. The card controller must save the exact **Choose folder** trigger before opening the native picker and restore it after success, cancellation, or error if it remains connected. Live nested-picker focus restoration is **NOT RUN** until the foreground native test above is available.

These findings reject a Today-only mount: `personal-assistant-today.tmpl` is not rendered on other app pages. The conditional shared-template placement selected below preserves the HQ briefing, pre-HQ eligibility, ordinary Ask gating, and one stable card ID.

## Selected architecture: narrow assistant setup coordinator

### Why `setupjourney` is not the owner

The coordinator will be a narrow `internal/assistantsetup` package rather than another `setupjourney` run kind.

`setupjourney` is the right authority for declaration-backed specialist programs: its identity, staffing, first-project, child-run, and live-setup semantics are rooted in an Assistant Home/project declaration. File Janitor is different in every material respect:

- it is a built-in Workspace Capability on an ordinary project workspace, not an Assistant Program or Assistant Project;
- the flow must work for a hired assistant in `needs_hq`, before a station/Home exists;
- the human decisions are a reviewed workspace/team proposal, a local-folder grant, and a separate monitoring approval, not role staffing or project-child setup;
- its canonical partial state already lives in the workspace creator, Setup Wizard, and File Janitor services.

Forcing this flow into `setupjourney` would require a fake program/declaration identity, would exclude the required pre-HQ case, and would make two domains authoritative for File Janitor readiness. The new package therefore owns only cross-domain sequencing, idempotency, and receipts. It delegates every resource mutation and health judgment to the existing authority.

The selected dependency direction is:

```text
personalassistanthttp/assistant_setup (current-user boundary)
        |
        v
assistantsetup.Coordinator + assistantsetup.Store
        |-- Personal Assistant relationship reader
        |-- File Janitor workspace candidate resolver
        |-- reviewed sessionhttp workspace/team creator
        |-- setupwizard.Service / File Janitor setup adapter
        |-- filejanitor.Service + Automation + scan service
        `-- existing workspace lifecycle for open/delete/Trash behavior
```

`internal/setupwizard` remains the workspace-local setup-progress authority for the generic wizard and its seeded setup task. The coordinator advances/rechecks that service instead of inventing equivalent readiness. `internal/filejanitor` remains authoritative for the selected root, separate automation approval, watcher/schedule health, metadata-only scanning, batches, and review routing. The assistant-setup store is authoritative only for the accepted cross-domain run, its current step, operation claims, and ownership receipts.

### Package, server, HTTP, and browser boundaries

The least invasive implementation boundary is:

| Boundary | Responsibility |
|---|---|
| `internal/assistantsetup` | Typed proposal/run state, coordinator, SQLite store interface/implementation, transition validation, operation claims, and safe projections. No HTTP, template loading, filesystem paths, or direct workspace/agent persistence. |
| `internal/personalassistanthttp/assistant_setup.go` | Owner-scoped JSON handlers under `/api/personal-assistant/setup/file-janitor`; reuses the existing Personal Assistant handler's current-user provider and never accepts `owner_user_id`. A separate HTTP package would duplicate that boundary and is rejected. |
| `internal/server/assistant_setup.go` | Dependency composition and narrow adapters over the canonical workspace store, Personal Assistant service, setup wizard, File Janitor domain, automation, scanner, and session workspace creator. |
| `internal/sessionhttp` | One exported in-process reviewed-creation facade. It consumes an exact server-generated team plan/revision and server-owned operation descriptor, then reuses the normal production creation pipeline. The legacy `CreateFromTemplate` method is not used. |
| `internal/filejanitor` and `internal/filejanitorhttp` | Small operation-aware additions only where needed to stamp folder/automation/scan receipts, accept a trusted native-selection receipt for the assisted grant, and run the initial scan. They continue to own all paths and File Janitor behavior. |
| `internal/web/static/js/modules/assistant-led-setup.js` | The reviewed preview/consent/retry controller. It listens to the existing `personal-assistant:status` event, but applies its own explicit setup eligibility instead of weakening ordinary Ask gating. |

The shared-card template has one DOM instance per page. On Home it is included in `personal-assistant-today.tmpl` immediately after `personalAssistantTodayBanner`, so the existing needs-HQ briefing remains first. On non-Home pages the same template is included in `ori-guide.tmpl` inside `personalAssistantAskPanel`, after `personalAssistantPanelStatus` and before work activity. It is not placed only in the Today partial (which is Home-only), and it is not placed inside or used to enable the disabled pre-HQ composer. Explicit mission/deep-link entry may focus the card's heading or current action; ordinary launcher opening keeps existing tab/composer focus behavior. The card never auto-opens over onboarding or the HQ quest.

The route prefix is capability-specific on purpose. This is the first proof, not a generic manifest runner: clients cannot submit arbitrary template IDs, agent definitions, workspace names, operation kinds, or paths through coordinator endpoints. The only assisted path value travels through File Janitor's existing owner-scoped folder-confirmation boundary as an opaque native-selection receipt; legacy manual setup retains its current direct path request. The assisted grant is always committed with automation paused. Looking at a proposal is read-only.

### Relationship and target-workspace resolution

A proposal is available only when the server can prove one current Personal Assistant relationship for the authenticated owner:

- `needs_hq`, `provisioning_hq`, and `active` permit an explicit setup request;
- `paused` permits the same explicit request but never a proactive/background start;
- `needs_hire`, `hiring`, `repair_needed`, missing, or unavailable relationships fail closed with a safe recovery state.

No Personal HQ ID is required or synthesized. The relationship's stable assistant ID is bound into the accepted run only as identity evidence; the assistant profile, chat, and HQ are not mutated.

The server adapter resolves candidate workspaces from canonical workspace metadata, not names or browser state. A compatible candidate must be active, owned by the current user (with the existing local-user treatment for legacy blank owners), a normal project workspace rather than a group/Home, and canonically carry the `file-janitor` installed capability. File Janitor's own applicability/status service supplies the readiness facts. Template provenance and legacy aliases may corroborate the result but never substitute for the installed capability.

Resolution is deterministic:

- no compatible candidate: propose creation from the current built-in `file-janitor` blueprint;
- exactly one: propose adopting/resuming that workspace without changing its name, team, folder, or monitoring merely by previewing it;
- more than one: report an ambiguity and require an explicit candidate selection in a newly revised proposal; never choose by recency, display name, or partial setup state.

An accepted run binds the selected workspace ID or the `create` decision. If that workspace later becomes archived, trashed, deleted, owner-mismatched, loses the capability, or otherwise ceases to be the reviewed target, the run stops for reconciliation; it never silently creates a replacement.

### Reviewed creation seam

For a create proposal, the coordinator asks the session creator for the current effective File Janitor team plan and renders the exact `File Curator` create/reuse decision, provider/model/configuration, warnings, permission effects, and plan revision. Acceptance stores that typed snapshot. Commit re-resolves the blueprint and team immediately before the first write and uses strict `team_intent`/`role_staffing` validation; drift returns a fresh proposal and requires review again.

The in-process facade also carries a server-only operation descriptor. The descriptor reserves the workspace ID before creation and stamps the operation ID/review digest into portable template provenance in the same creation path that persists the workspace. It is not a JSON field available on `POST /api/workspaces`. This closes the otherwise unobservable crash window between creating a workspace and recording which run owns it. Created versus reused agent outcomes are recorded separately; a reused root profile is never operation-owned. Missing root-agent storage remains a blocker.

The built-in Setup Wizard snapshot is retained. Its presence already suppresses setup-task auto-execution. As the assisted flow commits the folder and monitoring decisions it rechecks/advances the same Setup Wizard service, and final readiness completes the wizard-owned setup task without running an agent. There is therefore one deterministic setup, not a second automatic starter-task flow.

### Durable store and state ownership

`assistantsetup.SQLiteStore` uses the existing application SQLite database and owns three normalized tables:

1. `assistant_setup_runs` — one accepted cross-domain run. A partial unique constraint permits at most one non-terminal run for `(owner_user_id, capability_id)`.
2. `assistant_setup_operations` — one server-claimed operation for each run step (`workspace`, `folder_grant`, `monitoring`, `initial_scan`, and later `reset`). A unique `(run_id, kind)` constraint makes retries replay the same claim.
3. `assistant_setup_resources` — bounded receipts for resources observed, adopted, created, or updated by an operation. A unique `(operation_id, resource_kind, resource_id)` constraint prevents duplicate ownership records.

A proposal GET does not write. Accepting a freshly recomputed proposal transactionally creates or resumes the one run and its workspace operation claim. Every later mutation first claims/loads its operation, calls the canonical domain once, observes the canonical result, and then records the receipt. On restart, the run is the single journey-resume point; canonical domains remain the proof that a claimed resource actually exists. Disagreement becomes `reconcile_required`, never guessed success.

Run rows may contain only:

- server-generated run ID, owner user ID, stable assistant ID, capability ID, run status/current step, monotonic revision, and timestamps;
- accepted proposal revision, blueprint ID/version/definition digest, team-plan revision, and the validated typed team-review snapshot;
- target mode (`create` or `adopt`) and target workspace ID;
- safe last-error code, failed step, and retry/reconciliation timestamps.

Operation/resource receipts may contain only:

- server-generated operation ID, run ID, operation kind/status, attempt count, idempotency/review digest, expected run revision, and timestamps;
- resource kind, stable opaque resource ID, ownership (`adopted`, `created`, or `updated`), and a bounded version/digest used to prove a safe retry/reset;
- workspace ID; workspace agent-instance ID; a created root profile's immutable assistant-setup provenance ID/store origin/config digest (never its editable display name as recovery identity); directory-reference ID and root-generation ID; watcher trigger ID and schedule identity; initial scan batch ID; and safe outcome/error codes. A reused profile is represented only by the reviewed team digest plus its attached workspace-instance ID and `adopted` ownership; if no stable attachment/provenance can prove it, reconciliation stops.

These tables never contain an absolute folder path, filename/candidate list, file contents, chat text, prompt output, credential/token, raw OS error, or arbitrary request/response JSON. The accepted team snapshot is a validated schema, not an untyped payload. The canonical File Janitor settings remain the only durable owner of the approved absolute root. Its existing owner-scoped status may show that approved folder back to the same user, but the coordinator receipt stores only directory/root IDs.

The assistant-setup API exposes the corresponding safe projection: run and operation IDs/statuses/revisions, target workspace ID/name/safe route, reviewed team summary, fixed disclosure/effects copy, readiness summaries, safe codes, and the resulting batch ID/review route. Mutation requests carry only the current run/proposal revision plus the one explicit decision for that step. Owner identity, template/team configuration, operation identity, and resource ownership are always server-derived.

## HTTP contract

### Routes and closed request bodies

All coordinator routes are registered on the existing Personal Assistant handler and derive the owner from its `userprofile.UserProvider`. Unknown or foreign run/workspace IDs are indistinguishable `not_found` responses. JSON bodies use bounded decoding, reject unknown fields, and contain no owner, template ID, agent definition, workspace name, path, operation kind, completion flag, or arbitrary action name.

| Method and route | Request | Consequence |
|---|---|---|
| `GET /api/personal-assistant/setup/file-janitor` | Optional own `target_workspace_id` query only, used to render one choice from an ambiguous candidate list. | Read and recompute proposal/current-run projection. It creates no run/resource, claims no operation, opens no picker, and starts no task/automation/scan. |
| `POST /api/personal-assistant/setup/file-janitor/accept` | `{"proposal_revision":"<opaque>","target_workspace_id":"<optional own ID>"}` | Re-resolve the exact plan, transactionally create/resume one accepted run and workspace operation, then perform only reviewed creation/reuse. It stops at folder permission. |
| `POST /api/personal-assistant/setup/file-janitor/defer` | `{"proposal_revision":"<opaque>"}` | Before a run, durably defer the existing File Janitor recommendation/mission. It creates no assistant-setup run. |
| `POST /api/personal-assistant/setup/file-janitor/runs/{runID}/folder-intent` | `{"if_version":N}` | Claim/reload the folder operation and return a short-lived opaque setup token. It does not open the picker or grant a folder. |
| `POST /api/personal-assistant/setup/file-janitor/runs/{runID}/prepare-review` | `{"if_version":N,"review_revision":"<opaque>"}` | Commit the separately reviewed monitoring approval; after healthy registration, run/reconcile the one metadata-only initial-scan operation. It never performs file actions. |
| `POST /api/personal-assistant/setup/file-janitor/runs/{runID}/defer` | `{"if_version":N}` | **Finish later**: stop launching later steps after admitted work settles. Existing access or approved monitoring is not revoked/paused. |
| `POST /api/personal-assistant/setup/file-janitor/runs/{runID}/resume` | `{"if_version":N}` | Re-read canonical state and resume the same authorization only where still valid. It never opens a picker or silently unpauses monitoring. |
A failed monitoring/scan operation is retried explicitly by submitting the freshly rendered `prepare-review` decision again; no generic retry endpoint can bypass that review. Verified transient failures may also consume the bounded automatic retry budget while the exact coordinator-owned rollback marker and authority digest remain current. **Continue manually** first uses the same versioned run `defer` endpoint, then the browser opens the server-projected route for that exact workspace. Before a run exists, the projection supplies the ordinary blueprint creator route without a coordinator mutation.

**Close** has no endpoint and changes no server state. Picker cancellation also has no coordinator mutation: the folder operation remains an awaiting-user claim and the same run stays at folder permission.

The assisted folder commit extends the existing owner-scoped `POST /api/workspaces/{workspaceID}/file-janitor/setup` request with a closed alternative shape:

```json
{
  "selection_token": "opaque native-picker receipt",
  "assistant_setup_token": "opaque folder-operation receipt",
  "paused": true
}
```

`filejanitorhttp` resolves the one-time selection token server-side, verifies the setup token binds the authenticated owner, exact run/workspace/operation/revision, refuses `paused:false`, and invokes the operation-aware File Janitor grant. It then records only stable resulting IDs through an injected narrow receipt interface. The existing manual shape (`path`, optional time/timezone/paused) remains available to the existing console/wizard and does not acquire coordinator authority. Assisted clients may not send `path`; manual clients may not forge an `assistant_setup_token`. A same-root lost response is recovered from the File Janitor pending/final operation marker, not by reusing an expired picker receipt.

### Projection schema

Every successful coordinator response wraps one `setup` projection. Fields are typed and additive only within a versioned schema:

- `schema_version`, `capability_id`, `view_state`, `stale`, and a bounded `status_message`;
- `relationship`: Assistant stable ID/display name/state and setup eligibility only—no prompt/model secret;
- optional `proposal`: opaque revision; `create|adopt|choose` mode; blueprint ID/version/digest; exact workspace candidate IDs/names/routes; normalized team-plan revision and each role's create/reuse/configured-model status; fixed metadata/folder/monitoring/file-review disclosures;
- optional `run`: run ID/revision/lifecycle/current step, target workspace ID/name/route, operation rows with kind/status/attempt count/safe timestamps, saved-success summaries, deferral/failure code, and whether work is in flight;
- optional `health`: fresh/stale canonical File Janitor readiness, paused/monitoring/privacy summaries, local schedule/timezone, bounded first-result counts/time, exact batch ID, and safe review/settings/history routes;
- `actions`: a server-derived closed list from `accept`, `defer_recommendation`, `choose_target`, `choose_folder`, `prepare_review`, `finish_later`, `resume`, `manual`, `manual_takeover`, `open_workspace`, `review_batch`, `pause_monitoring`, `manage_access`, and `history`. Each has only ID, label, enabled, and optional reason/route.

The browser renders these values as text, never HTML, and does not infer permission from a label. A write returns HTTP 200 for replay/adoption and 201 only when acceptance creates a new run; both return the same projection shape. HTTP success is not itself a domain receipt—the projection identifies the observed canonical receipt.

### Safe failures

Errors use the existing structured API envelope with a stable code, user-safe message, and optional `retryable`/fresh `setup` projection. Raw paths, filenames, tokens, store errors, provider details, and foreign IDs are never returned.

| Code | HTTP | Meaning/allowed response |
|---|---:|---|
| `invalid_request` | 400 | Malformed/unknown/oversized body or invalid opaque revision format. |
| `not_found` | 404 | Run/target missing or not owned; reveals no foreign existence. |
| `assistant_not_ready` | 409 | Not hired/hiring; use canonical hire route. |
| `assistant_repair_required` | 409 | Relationship identity cannot be trusted; no setup mutation. |
| `ambiguous_target` | 409 | A selection is required; fresh candidates are returned by the safe projection. |
| `unsupported_target` | 409 | Capability/wizard/provenance/privacy cannot be safely automated; manual/repair route only. |
| `stale_proposal` / `stale_run` / `stale_monitoring_review` | 409 | Canonical revision changed; return fresh projection and require review where consent changed. |
| `invalid_action` | 409 | The requested route is not allowed at the run's current step. |
| `team_conflict` | 409 | Current create/reuse plan differs or a custom-name collision exists; no write from the stale plan. |
| `folder_conflict` / `folder_changed` | 409 | File Janitor root ownership/identity check refused; choose/repair explicitly. |
| `privacy_review_required` | 409 | Current mode is not the reviewed metadata-only mode; no reset or scan. |
| `monitoring_registration_failed` | 503 | Approval may be saved but fresh watcher health failed; retry registration only when still bound. |
| `initial_scan_failed` | 503 | Setup may be ready but the operation has a verified failure; retry only within policy. |
| `reconcile_required` | 409 | Consequence outcome/ownership cannot be proved; never automatic retry. |
| `no_longer_available` | 409 | Target/relationship/capability was removed or invalidated; never recreate from stale consent. |
| `work_fenced` | 503 | Reset/shutdown admission refused before mutation. |
| `assistant_setup_unavailable` | 503 | A required authoritative read/store is unavailable; never interpret as absence. |

Native picker launch retains its existing 404 actionable unsupported response. A real native Cancel remains HTTP 200 with `success:true, selected:false` and is not converted to an error by the card.

## State and consent contract

### Persisted versus derived state

Run lifecycle and live capability health remain separate columns/facts:

| Persisted run lifecycle | Meaning |
|---|---|
| `active` | Accepted and allowed to advance only through already reviewed, non-permission work. |
| `deferred` | Finish later/manual takeover stopped new launches; canonical resources remain. |
| `first_result` | This run has an operation-specific batch or `no_eligible` receipt. Historical completion is immutable. |
| `reconcile_required` | A consequential result or ownership marker cannot be proved. No automatic work. |
| `invalidated` | Relationship/target/capability lifecycle ended this authorization. No recreation. |

`current_step` is one of `workspace`, `folder`, `monitoring`, `initial_scan`, or `result`. Operations are `awaiting_user`, `claimed`, `running`, `succeeded`, `failed`, or `unresolved`. User-visible `Proposed`, `Setting up`, `Needs your permission`, `Needs your decision`, `Needs attention`, `Saved for later`, `Preparing review`, `First review ready`, `No new files to review`, and `No longer available` are derived from those facts plus fresh canonical health. A historical `first_result` run may therefore show monitoring paused or needs attention without re-running setup.

### Consent bindings

| Consent | Bound evidence | Consequences authorized | Explicitly not authorized | Becomes stale when |
|---|---|---|---|---|
| **Set up for me** | Owner/Assistant ID, proposal revision, create/adopt target, blueprint version/digest, exact strict team plan/revision and create/reuse decisions | One reviewed workspace/team prepare/reuse operation; safe wizard/readiness reconciliation up to folder checkpoint | Picker/folder access, `Filed`, monitoring, scans, tasks/agent runs, model calls, file actions | Relationship/owner/target/blueprint/team decision changes |
| **Choose folder** | User-triggered native selection token plus run/workspace/folder-operation token | Canonical root grant, directory/MCP associations, and disclosed create/reuse of `Filed`, with `paused:true` | Watcher, daily scan, assisted initial scan, content read/model, move/Trash | Token expires; target/root claim changes; run/version/owner changes |
| **Start monitoring and prepare review** | Run/version and review revision over root generation, File Janitor settings revision, metadata-only mode, watcher recipe, local schedule, and timezone | File Janitor automation approval, exact watcher registration, scheduler readiness, then one operation-aware metadata-only initial scan | File approval/move/Trash, content inspection/model, other automations, unpausing a manually paused adopted setup without explicit review | Root/privacy/settings/schedule/timezone/recipe/owner/run changes or user pauses/revokes before commit |
| **Existing file-review confirmation** | Existing File Janitor preview/approval token and selected candidate fingerprints | Only the exact reviewed move/Trash plan | Setup authority or future file operations | Existing File Janitor approval rules say so |

Folder and monitoring decisions are never combined. A ready adopted workspace is read-only on open; existing automation approval can be adopted, but a distinct Prepare first review decision is still required when this run has no scan authorization. A manually paused workspace stays paused on generic resume.

## Crash, replay, lifecycle, and reset contract

### Operation protocol and lock order

Every consequential step follows the same protocol:

1. Acquire the process-wide `resetstate.WorkGate` permit before reading a mutable owner, opening a picker consequence, or launching detached work.
2. In one short assistant-setup SQLite transaction, compare the run revision, validate the allowed step, and create/load the unique operation claim. Commit before calling another domain.
3. Call exactly one canonical domain owner. Never hold an assistant-setup SQL transaction while waiting on an agent store, workspace store, filesystem, trigger store, Setup Wizard adapter, or scan.
4. Observe the canonical result by stable ID/marker and expected version. Then record the resource receipt and advance the run with a compare-and-swap on its revision.
5. If the final coordinator write or HTTP response is lost, replay starts at observation, not at mutation.

The lock/admission order is therefore `WorkGate permit -> assistantsetup claim transaction -> domain lock/write -> assistantsetup receipt transaction`. A domain may take its own workspace/File Janitor/trigger locks, but it must never call back into an open assistant-setup transaction. Two tabs racing the same step load the same operation claim; only one receives the claimed run revision, and both eventually project the same observed result.

The coordinator's retry scheduler is a server-owned background service. The runner owns only its cancel/join lifecycle; every consequential retry enters the shared finite `WorkGate` inside `PrepareReview` before revalidation or mutation. `Close` cancels future polling and joins the runner. `Server.shutdownBackground` stops it before File Janitor automation. Reset fencing therefore refuses a retry before mutation or waits for an already admitted finite operation, while normal shutdown joins the polling loop.

Retries use the operation row's persisted attempt count and expected run revision. The initial attempt plus at most two automatic retries is the total automatic budget. Only a verified transient failure or an exact idempotent claim is retryable. Native-picker cancellation/denial, a conflict, changed owner/identity/scope/consent, stale review, or an unresolved consequence never schedules a retry. No timer may open the native picker. A deferred, invalidated, or revision-changed run makes an old timer a no-op.

### Domain atomicity and required additions

The current domains do not have one cross-store transaction, so recovery is based on small atomic writes plus operation markers:

| Boundary | Atomic fact available today | Required operation proof and recovery |
|---|---|---|
| Proposal acceptance | One SQLite transaction can enforce one active run. | Store the accepted revision, exact target decision, reserved workspace ID (for create), typed team snapshot, and workspace operation claim before any profile/workspace write. A lost accept response returns this run. |
| Agent creation | Each root-agent definition is an atomic file write, but a roster and workspace are not atomic together. | Extend `store.CreateAgentConfig` with bounded provenance tags so a newly created profile is stamped with run/operation IDs in the same create write. The claim already names the exact expected profile/config digest. On replay, an exact same-operation marker/config is a prior create result; an unmarked or mismatched same-name profile is a collision and stops. Assigned/reused profiles are receipts with `adopted` ownership and are never mutated or rolled back. |
| Workspace persistence/provisioning | The production creator seeds profiles, creates the core workspace, then creates the folder, scaffolding, tasks, provenance, and capability state. Folder/provisioning warnings can occur after the core row exists. | Preallocate the random workspace ID in the operation claim and pass it through a server-only reviewed creator descriptor. That exact ID is sufficient to find a core-only partial without a name search. The completed folder workspace receives typed assistant-setup creation provenance alongside normal template provenance. Reconcile the exact ID: finish missing creator-owned folder/provenance/capability work when inputs still match, or use the creator's bounded rollback for an operation-owned incomplete create. Never issue a second create with a new ID. |
| Folder grant | `settings.json`, each workspace update, and `Filed` creation are individually repeat-safe, but `ConfirmSetup` currently creates `Filed` and workspace access before the settings write; they are not one transaction. | Add an operation-aware File Janitor grant. After validation/conflict checks, atomically persist a domain-owned pending grant containing operation ID, canonical root, fresh root generation, expected settings revision, and pre-existing-resource facts **inside File Janitor state**, not the coordinator. Only then create/reuse `Filed` and update workspace access. Stamp the directory-reference/MCP capability-resource associations with the operation ID, then atomically finalize settings with their IDs. Replay uses this pending record and exact root; absence before the pending write means no grant consequence and returns to `Choose folder`, never auto-opens the picker. |
| Monitoring approval/registration | The reviewed settings write atomically verifies root/privacy/schedule/timezone and the reviewed paused/approved state, records approval while paused with the run/operation/review/authority marker, and only then performs a separately revalidated activation. `EnsureWatcher` is a later idempotent trigger-store write. The daily scheduler is one global loop reading settings, not a per-workspace scheduled-job row. | The review digest binds owner/run/revision, root generation, metadata-only privacy, stable settings digest, watch events/debounce/exclusions, local time/timezone, and current automation state. File Janitor advances the marker through approval, activation, and failed-paused rollback; every manual settings/pause/folder action clears it. The trigger remains owned by File Janitor rather than receiving a second authority marker. Fresh watcher and scheduler health are required before scanning. A failed new activation is paused; monitoring that was already approved and active is preserved. Scheduler success is fresh global-loop readiness plus matching settings, not a fabricated schedule resource. |
| Initial scan | `UpdateScanState` holds one workspace lock and atomically writes observations, candidates, and a batch. Today a successful zero-eligible scan writes observations but no batch or operation receipt. | Add a bounded `ScanOperationReceipt` collection to `scan-state.json` and an operation-aware metadata-only scan method. Under the same scan-state update, first replay an existing receipt or pending batch, then validate root generation/privacy, scan, and atomically write either `(batch_id, counts, completed_at)` or `(no_eligible, bounded exclusion/settling counts, completed_at)`. A batch and its receipt can never diverge. Failures before the atomic write have no scan result and are safely retryable if scope is unchanged. |
| Wizard readiness/completion | Wizard progress is an atomic workspace update. Its existing task/mission completion hooks run afterward and are idempotent, but are not in the same write. | Recheck the canonical wizard after folder/monitoring actions. When it first becomes ready, the finalizer may replay the existing idempotent setup-task and Mission 02 consumers until their canonical evidence is observable; wizard-ready alone is not a receipt that those cross-store effects ran. Initial-scan failure remains separate from wizard completion. |
| Coordinator/final HTTP acknowledgment | Each assistant-setup receipt/run advance is atomic, but later than its domain write. | Every success response is reconstructed from the domain marker plus the stored operation. If the domain succeeded and the coordinator acknowledgment did not, observation completes the receipt. If the coordinator committed and the HTTP response was lost, the client gets the same run revision/result. Browser state and generated text never acknowledge a step. |

The implemented monitoring review uses deterministic digests of the authority-bearing File Janitor fields rather than timestamp precision: one digest binds root, privacy, schedule/timezone, and watcher recipe; the displayed review additionally binds pause/approval and observed active state. A path-free File Janitor receipt binds the exact run, operation, review, authority digest, and coordinator-owned transition. This lets a timed retry distinguish its own failed-paused rollback from a user's later pause; all generic settings, folder, approval, and pause paths clear the receipt. A folder change/revoke also issues a new `RootID`; operation-aware folder grants retain their bounded pending/final operation marker. The folder path appears only in the File Janitor domain's settings/pending-grant record because that domain must finish or explain the grant; it never enters assistant-setup SQLite, assisted responses, generic logs, URLs, events, or diagnostics.

`Filed` is an external user-folder side effect explicitly disclosed at folder consent. It is never removed automatically during reconciliation, revocation, workspace deletion, or app reset: without placing private marker files in the user's folder, Ori cannot prove an empty `Filed` was not adopted or used by the user after creation. The operation receipt records whether setup observed or created it solely for truthful reporting.

### Initial-scan replay proof

The initial-scan operation is the one place where a normal domain return value is insufficient: `created == false` currently means a successful scan with no batch, but it leaves no operation-specific evidence.

The new scan receipt is keyed by the server operation ID and stores only root ID, privacy/settings revision digest, source, start/completion times, outcome, batch ID when present, and bounded counts/reason codes. It contains no filenames or paths. It is persisted in the same atomic `scan-state.json` replacement as settling observations and any batch/candidates.

Recovery is deterministic:

- matching receipt with batch: load that exact batch; never infer from “latest”;
- matching `no_eligible` receipt: report the stored completion time/counts; never invent an empty batch or claim the folder was empty;
- no receipt and operation attempt known to have started: because the domain writes receipt and scan effects atomically, there was no successful scan-state consequence; retry is allowed only while root ID, metadata-only mode, settings revision/review digest, ownership, and run authorization still match;
- receipt missing after the bounded receipt collection has rolled past the operation's age/high-water mark: outcome is unresolved and automatic replay stops;
- mismatched root/privacy/review or malformed state: `reconcile_required`; do not scan.

Before enumerating, the operation-aware scan checks the current scan state for a pending batch on the current root. If one already supplies the promised result (including one produced by a watcher that won the scan lock), it records an `adopted_batch` receipt and does not scan again. Otherwise all scans still serialize through the existing scan-state lock, eligibility, settling, and active-fingerprint deduplication. The operation method refuses any content-enabled mode, so no classifier provider is called even if a model exists.

### Safe unresolved-outcome stop

`reconcile_required` is not a generic retry state. It is mandatory when an expected stable resource exists but its operation marker, owner, version, or reviewed configuration cannot be proved; when a persisted record is unreadable; or when both completion and absence cannot be established from the atomic domain contract. The card names saved successes and offers a bounded repair/manual route. It does not delete, recreate, attach by name, grant again, unpause, or scan.

In particular:

- an exact reserved workspace ID with partial creator state is handled only by the reviewed creator's reconcile/rollback seam;
- a same-name agent without the exact operation tags is unrelated, even if its visible configuration looks similar;
- a directory reference/trigger with a different or absent marker is adopted only when the reviewed existing-state path explicitly established that fact before mutation; it is never retroactively claimed after a crash;
- a File Janitor domain pending grant is the sole authority for resuming a lost folder response—the generic run never reconstructs a path;
- an absent scan receipt is replayable only under the atomic/high-water proof above.

### Manual changes and lifecycle invalidation

Every mutation re-reads the relationship, canonical folder-backed workspace, capability, wizard, File Janitor settings, and relevant operation marker. The following state changes supersede old coordinator intent:

| Observed change | Required behavior |
|---|---|
| Assistant missing, replaced, hiring, or `repair_needed` | Invalidate/freeze the run. Do not mutate or clean up resources. A paused relationship allows only an explicit user resume, never proactive/timer continuation. |
| Target owner changed, workspace archived/trashed/deleted, or exact target unreadable | Mark `no_longer_available`/`reconcile_required` as appropriate. Never create a replacement from the stale proposal. |
| File Janitor capability removed or wizard/provenance becomes unsupported | Stop and route to existing manual/repair controls. Never reinstall or synthesize a wizard from the run. |
| Root revoked | Invalidate folder/monitoring/scan continuation. Keep the workspace/run history and require a fresh picker action; never regrant from a stored generic receipt. |
| Root generation or canonical/symlink identity changed | Stale every monitoring/scan review bound to the old root. Preserve domain history; require explicit regrant/review. |
| Schedule, timezone, watcher recipe, settings revision, or monitoring approval changed before commit | Refresh the monitoring disclosure and require review. Do not overwrite the manual change. |
| User pauses monitoring after approval | Preserve pause and stop later assisted scan/continuation. Never translate `Resume setup` into unpause; offer the existing scoped resume/scan controls explicitly. |
| Content mode/provider/consent differs from metadata-only | Do not reset it and do not run the assisted scan. Show actual privacy and route to manual review. |
| Manual wizard/domain work completes a step | Adopt the verified fact as `adopted`; do not repeat it or claim ownership. A changed scope still needs a fresh review. |
| A pending batch appears from ordinary watcher/manual work | Link/adopt it when it belongs to the current root; do not initiate another onboarding scan. |
| `Finish later` while an action is in flight | Let only that admitted action settle and receipt it, then stop before the next step. `Close` changes no server state. |
| Normal shutdown | Stop retry dispatch, join coordinator work, then stop/join File Janitor automation. Persisted claims remain resumable; no shutdown callback invents success. |
| App reset fence or completed reset | Fence new work and refuse reset while a finite operation is active. Drain both services before apply. After apply, no old timer or retained workspace file may recreate an erased run. |

A later change after first-result completion affects live health only; it does not rewrite historical setup/scan receipts or turn the completed card into an autonomous repair process.

### Global reset inventory

This feature adds no assistant-specific destructive reset endpoint. Ori's existing reviewed Start Fresh lifecycle is the reset authority.

All three assistant-setup tables must be added to `database.ResetRecordTables` in the same migration that creates them; otherwise reset inspection correctly reports an unclassified database domain and refuses. Reset deletes the run/operation/resource rows with the other app records in one database transaction. Workspace/project backing folders—including File Janitor settings, batches, trigger files, and the user-approved external `Filed` directory—remain retained/detached under the existing reset policy. Because the run is gone and workspace reattachment is disabled by that policy, retained operation markers are inert evidence, not authority to continue.

The coordinator receives the same process-wide gate as File Janitor automation. Reset order is: atomically fence finite work; stop/join assistant retry dispatch; stop/join File Janitor watcher/daily scans; stop other owners and close stores through `serverResetLifecycle`; then apply the existing pre-start reset plan. A reset refusal changes no run state. A staged/uncertain reset leaves the process fenced, so no assistant setup continuation can race recovery.

### Resource ownership rule

Operation identity must reach each resource that could otherwise be mistaken for unrelated user state:

- the operation claim reserves a newly created workspace ID before creation, and the completed folder workspace carries assistant-setup creation provenance with run ID, workspace-operation ID, and review digest;
- newly created agents carry the operation markers in the atomic create, while reused profiles are marked `adopted` and never deletable by this run;
- folder grants carry their operation/root IDs in File Janitor pending/final state and workspace associations; monitoring carries a path-free operation/review/authority receipt in File Janitor settings while the coordinator owns the bounded retry record, without copying the path into coordinator storage;
- the initial batch or successful zero-result receipt is atomically keyed to the scan operation ID.

Reconciliation or creator rollback may act only when both the coordinator claim and canonical resource marker/stable reserved identity match the same operation and expected version. Adopted resources and any resource changed by another flow are left untouched. Ordinary workspace deletion, File Janitor revoke/relink, pause, capability removal, and app reset remain owned by their existing reviewed lifecycles.

## Spike conclusions and implementation bindings

The PRD's four implementation-discovery questions are resolved:

1. **Durable orchestration:** use the narrow `internal/assistantsetup` coordinator and the three normalized SQLite tables above; do not extend declaration-backed `setupjourney` and do not build a generic graph/runtime.
2. **Creation/scan recovery:** reserve/stamp stable operation identity at each canonical resource boundary, add a reviewed in-process `sessionhttp` creator seam, add File Janitor pending grant/monitoring markers, and store an operation-specific scan receipt atomically with scan state—including `no_eligible`.
3. **Panel integration:** render one shared card instance after the existing Home Today/HQ banner and, on non-Home pages, after the Ask status. Preserve ordinary Ask's `active|paused` gate and use separate setup eligibility for `needs_hq|provisioning_hq|active`, with explicit-only access while paused.
4. **Native picker:** this checkout builds the existing universal macOS helper and the unavailable path is verified. Interactive success, cancellation, and nested focus are explicitly **NOT RUN** in the coding-agent environment and remain required desktop demo evidence; no new picker/platform or typed-path fallback is authorized.

No new characterization helper is needed before implementation because existing tests already pin the critical current contracts: `internal/filejanitor/agent_free_golden_path_test.go` and `privacy_test.go` cover no-agent/no-model operation; `internal/sessionhttp/template_agent_review_test.go`, `workspace_create_rollback_test.go`, and `template_agents_root_test.go` cover strict current creation/ownership; `internal/filejanitor/reset_admission_test.go` and `internal/server/reset_lifecycle_test.go` cover work admission/drain.

Bounded validation at the spike gate passed:

- `go test ./internal/filejanitor -run 'Test(GoldenPath_WorksWithNoAgentInTheWorkspace|GoldenPath_SurvivesAgentsBeingDeleted|MetadataOnlyMode_ReadsNoContentAndCallsNoModel|ResetAdmissionJanitorScanHandoffAndStopJoin|ResetAdmissionJanitorRefusesBeforeCallbacksOrMutation)$' -count=1` — 5 selected cases passed.
- `go test ./internal/sessionhttp -run 'Test(TemplateAgentPlanRevisionIsDeterministicAndDefinitionSensitive|ValidateTemplateAgentReviewRequiresExactCompleteExpectations|SeedTemplateAgentsStrictCreatesCompleteRoster|CreateWorkspaceWithReviewedTemplateAgentIsAtomic|StaleTemplateAgentReviewReturnsFreshPlanBeforeWrites|StrictTemplateAgentFailureAfterReuseNeverDeletesSavedDefinition|StrictTemplateAgentFailureRollsBackEarlierDefinitions|SeededTemplateAgentsAreRootAgentsAndAttachAsWorkspaceCopies|CreateWorkspaceRepeatSubmissionDoesNotDuplicateAgents)$' -count=1` — 9 selected cases passed.
- `go test ./internal/server -run 'Test(ResetLifecycleShutdownLeavesSessionStoreWritable|ResetLifecycleRefusesFiniteWorkWithoutCancellation|ResetLifecycleDrainFlushesFencedSessionBeforeClose|ResetLifecycleDrainRejectsUnreleasedLifetimeOwner|ResetAdmissionHTTPBusyThenStagedRecoveryStaysReachable)$' -count=1` — 5 selected cases passed.
- `node --test internal/web/static/js/modules/personal-assistant-panel.test.js internal/web/static/js/modules/personal-assistant-home.test.js` — 26 tests passed.
- The disposable live API/browser exercises above verified paused grant, separate automation, metadata review routing, six panel page/state combinations, narrow width, and actual helper-unavailable rendering. Interactive picker success/cancel/focus remain the only Group 1 live unknown and are bounded as described above.

These results leave concrete service boundaries for later groups and no architecture blocker. The native evidence gap blocks only claims that require a foreground picker; it is not an approval shortcut and cannot be replaced by direct path entry in the assisted flow.

Implementation file bindings are therefore final: `internal/assistantsetup/`, `internal/personalassistanthttp/assistant_setup.go`, `internal/server/assistant_setup.go`, operation-aware changes in existing `sessionhttp`/`filejanitor` owners, and one `assistant-led-setup.js` plus shared card template/style. The optional `file-janitor-setup-controls.js` extraction is rejected unless implementation proves two real consumers need identical explicit-workspace controls; the first implementation must not create it speculatively.
