# PRD: Assistant-led setup — File Janitor first

**Status:** Draft for review; product scope confirmed, implementation not started.
**Feature slug:** `assistant-led-setup` (ad-hoc planning; no GitHub Issue attached).
**Evidence baseline:** repository revision `ba462199`; source inspection only, not a live usability test.
**Planning boundary:** This document specifies the product. It does not authorize implementation, create a task list, or start a worktree.

## 1. Introduction / overview

### Problem

Ori currently helps users learn how to configure a capability. A user who wants one folder organized is led through a mission, a workspace creator, name/team decisions, and a separate setup wizard before seeing useful output. The Assistant mostly tells them what to click.

The desired experience is different: the user chooses an outcome, reviews a bounded setup proposal, and lets the Assistant handle the configuration. The user intervenes for permissions, consequential choices, and blockers that genuinely require their action—not to advance through routine configuration screens.

**Product principle: automate configuration; ask for decisions and permissions.**

“Assistant-led” describes responsibility and presentation, not an unrestricted language-model agent. Ori's compiled, permission-checked services perform setup. The hired Assistant presents the proposal, reports verified progress, and asks for the decisions Ori cannot make on the user's behalf.

### Confirmed scope decisions

The user selected **1A, 2A, 3A**:

| Decision | Approved direction |
| --- | --- |
| First delivery | File Janitor end-to-end. Email and calendar are future extensions, not implementations in this release. |
| Existing onboarding | Keep “Meet your assistant.” Simplify File Janitor setup afterward; keep manual customization available. |
| Start boundary | The Assistant recommends a scoped plan. Nothing in that plan executes until the user clicks **Set up for me**. Subsequent permissions remain separate. |

The first delivery is a **bounded File Janitor setup flow**, not a universal automation framework or a replacement for all onboarding.

### Current behavior and existing foundations

The following facts were verified in the repository:

- `internal/web/static/js/modules/tidy-downloads-quest.js` opens the creator with File Janitor selected and points at Create. It does not create the workspace, choose a folder, approve automation, or call File Janitor's API.
- `internal/projecttemplates/starter/file-janitor/template.json` declares the File Curator, folder requirement, watcher/daily scan, and folder → automation → readiness → summary wizard.
- `internal/filejanitor/setup_adapter.go` keeps folder selection outside generic wizard actions and starts automation only after its own approval. Its readiness comes from File Janitor's domain service.
- `internal/filejanitorhttp/handler.go` checks workspace ownership. The wizard's folder grant passes `paused: true`, separating access from unattended scanning.
- `internal/filejanitor/service.go` creates the `Filed` destination during folder setup. Setup is not literally “no filesystem changes,” even though existing files are not moved.
- `internal/filejanitor/classifier_provider.go` uses no model in metadata-only mode. Setup and metadata classification need neither provider credentials nor model calls.
- `internal/web/static/js/modules/personal-assistant-panel.js` shows a hired identity before HQ exists, but disables normal Ask submission until the relationship is active/paused. A setup card cannot simply rely on that composer being enabled.
- `internal/setupwizard/service.go` owns the wizard lifecycle and first-ready hook. `internal/progression/quests.go` completes `pa-tidy-downloads` from wizard readiness, not from the browser or an agent's statement.

Existing contracts are reused, not replaced by a second implementation of permissions, classification, file review, or readiness.

### Terminology

- **Setup proposal:** a read-only description of the configuration the user is being asked to approve.
- **Setup run:** the durable record of one accepted proposal, its progress, and references to resources it creates or reuses.
- **Permission checkpoint:** an explicit user action granting a specific capability, such as access to one folder or ongoing monitoring.
- **Receipt:** a successful result from the service that performed an action. Only receipts and fresh canonical state justify a success claim.
- **First result:** a real pending review batch, or a verified scan result explaining why there is nothing eligible to review. It is not a claim that files have been organized.

## 2. Goals

1. Let a newly hired Assistant prepare File Janitor without requiring the user to understand workspaces, agents, blueprints, or setup-wizard navigation.
2. Keep the ordinary path in one persistent setup card, except for the native folder picker and the final file-review surface.
3. Reduce routine decisions to three distinct consent moments: approve the setup plan; choose/grant the folder; approve monitoring and the initial scan.
4. Preserve explicit, separate approval for every file-changing operation. Setup approval never approves filing or Trash.
5. Provide truthful progress, actionable blockers, and reliable resume after navigation, refresh, or a server restart.
6. Reach a useful first result without Personal HQ, an LLM, or a paid/provider-backed task run.
7. Preserve manual setup, existing workspaces, recorded permissions, and previously completed missions.

## 3. User stories

- **New user:** “I want the Assistant to prepare a folder-organizing capability, so I only decide what it may access and what may run automatically.”
- **Privacy-conscious user:** “Before approving, I want to know what Ori reads, whether anything leaves my device, and what it can change.”
- **Interrupted user:** “If I close the panel or restart Ori, I want to continue from the real saved state without creating another workspace or approving everything again.”
- **Existing user:** “If I already started File Janitor, I want the Assistant to finish that setup, not create a duplicate or overwrite my preferences.”
- **User facing a blocker:** “Tell me what succeeded, what failed, and the one action I can take to continue.”
- **Advanced user:** “Let me use the existing manual controls without maintaining a separate, conflicting setup state.”
- **User without a model configured:** “Let me see filing proposals now; configuring chat should not block deterministic folder setup.”

## 4. Numbered functional requirements

### A. Entry, eligibility, and recommendations

**FR-01 — Preserve the hire.** “Meet your assistant,” its hire confirmation, durable identity, mission ID, and completion/reward behavior remain unchanged. This feature does not build Personal HQ, change the Assistant's agreement, or activate other capabilities as a side effect.

**FR-02 — Recommend without taking over onboarding.** Offer File Janitor through its existing Home mission and a non-modal recommendation in the Personal Assistant surface. Suggested outcome copy is **Organize a folder** with **File Janitor** as the capability name. Do not claim the user's Downloads is messy: the recommendation is based on app setup state, not inspection of unapproved files. Do not introduce a new multi-capability goal-selection wizard.

**FR-03 — Respect competing flows and deferral.** Do not auto-open the setup card on hire, interrupt the existing HQ briefing, or layer a second modal over onboarding. **Not now** durably defers this recommendation using the existing mission deferral semantics where suitable; it never starts setup. The mission remains explicitly resumable. Routine refreshes do not re-open a deferred offer.

**FR-04 — Work after hire without HQ or a model.** A valid hired relationship in `needs_hq`, `provisioning_hq`, `active`, or `paused` can explicitly open this deterministic setup flow. This narrow setup eligibility must not enable normal Ask/task execution before HQ exists. A proactively paused relationship receives no new proactive setup prompt, but an explicit mission/open/resume action remains available. Invalid, missing, or unavailable relationship evidence must not invent an identity or initiate Assistant-led setup; existing manual setup stays available under its normal rules.

**FR-05 — One entry result.** The File Janitor mission's start/resume action and the Assistant's recommendation resolve to the same proposal or saved run. Preserve the existing `/?quest=tidy-downloads` entry as a compatible route into that result. Merely visiting a route, opening a card, or reading status never starts provisioning, opens the picker, grants access, scans, or enables monitoring.

### B. Proposal and authorization to configure

**FR-06 — Inspect only app state before consent.** Before proposing creation, read the user's owned File Janitor workspaces, known setup runs, authoritative setup status, and the effective built-in blueprint/team plan. Do not enumerate the user's filesystem, read a suggested folder, or call a model to make this proposal. A failed lookup is “status unavailable,” not evidence that nothing exists.

**FR-07 — Present a concrete proposal.** Above **Set up for me**, show:

- The outcome: prepare one folder for reviewed filing.
- Whether Ori will create a workspace or continue a specifically identified existing one.
- The proposed workspace name and File Curator creation/reuse summary.
- Metadata-only operation; file contents remain unread and setup/initial classification make no model calls.
- Folder permission and monitoring approval will be requested separately.
- Monitoring's default daily time and timezone, with the exact effective values shown again before activation.
- The initial scan prepares proposals only; actual moves and Trash require a later review.

Use the compiled blueprint's effective defaults, including the current 09:00 local catch-up schedule, rather than an independent second set of frontend defaults. The suggested `~/Downloads` path is a suggestion, not a selected or approved folder.

**FR-08 — Scope the initial approval.** **Set up for me** authorizes only the displayed workspace/specialist configuration and repeat-safe continuation of that configuration. It does not grant filesystem access, approve monitoring, execute arbitrary starter tasks, change global provider settings, install software, enable native CLI/MCP autonomy, or approve file operations. A subsequent permission cannot be inferred from this first click or from a generic chat reply.

**FR-09 — Review the effective team, not a generic promise.** Use the existing server-generated team/create-reuse review contract. Technical details may be collapsed, but disclose creation versus reuse, effective provider/model configuration, and that no model task runs during setup. Never silently attach a name-matching custom agent whose prompt/tools differ from the described File Curator. Resolve a collision with a reviewed new identity or an explicit reuse/customization decision. Do not modify a reused global profile.

**FR-10 — Reject a changed proposal.** Bind approval to the owner, File Janitor capability/blueprint identity, effective proposal revision, and creation/reuse decisions. If materially different resources, permissions, defaults, or effective team settings are needed before commit, present the updated proposal for confirmation. A stale approval must not authorize the new plan. Cosmetic progress updates need no new approval.

**FR-11 — Start once.** After approval, persist one setup run before initiating its first consequence. Repeated clicks, network retries, and another tab starting the same File Janitor setup must resolve to the existing run/result rather than create duplicate workspaces or agents. A fresh request identifier must not bypass detection of an already active run for this user and target. Record references to partial successes so recovery can identify them without searching by display name.

### C. Existing setup and manual customization

**FR-12 — Reuse existing work honestly.** Resolve existing work by owned canonical capability/provenance and stable identifiers, not workspace or agent names. Apply the following rules:

| Existing state | Proposed behavior |
| --- | --- |
| One active Assistant-led run | Resume that exact run. |
| One supported unfinished File Janitor workspace, no run | Offer **Finish setting up [workspace]**; accepting binds the run to it and preserves completed steps. |
| One ready File Janitor workspace | Show current status and **Open File Janitor** or **Review proposed filing**; do not reprovision or start a scan merely because this card opened. |
| Several eligible workspaces | Ask the user which one they mean, before mutation; do not select the first or most recently listed arbitrarily. |
| No candidate | Offer creation from the canonical built-in File Janitor blueprint. |
| Missing/unsupported wizard snapshot or unreadable identity | Explain the limitation and offer the existing manual/repair route; do not guess a migration or silently create a replacement. |

Support the retired `downloads-janitor` identity through existing compatibility contracts when its wizard is supported. The new default does not bulk-migrate every capability-enabled workspace.

**FR-13 — Preserve manual control.** When no target workspace exists, **Customize / Set up manually** opens the existing File Janitor creator, with the blueprint selected, without starting a run or creating resources on its own. When a target workspace already exists—even before accepting a proposal—the manual action opens that same workspace's setup/settings, not a second creator. Switching an active run to manual setup first defers automatic continuation and reconciles any in-flight consequence, so two controllers cannot configure it concurrently. On explicit resume, the Assistant re-reads canonical state rather than overwriting manual changes or repeating already completed steps. User-made scope/privacy changes require review; they are not silently accepted or reversed.

### D. Automatic configuration

**FR-14 — Perform only the approved configuration.** Use the normal reviewed workspace-creation and File Janitor capability/provenance services to create the workspace, configure the reviewed File Curator, and record ordinary setup artifacts. Do not simulate UI clicks, invoke shell commands, or implement a second workspace seeder. Do not auto-start the blueprint's setup-help task or its first-batch discussion task. Never report “File Curator ready” merely because a profile row exists; distinguish configured identity from chat availability.

**FR-15 — No ceremonial Next buttons.** Within the approved scope, automatically advance through safe configuration, status checks, and resolved wizard steps. Pause only for an unmet permission, a real choice, an unsupported state, or an actionable failure. Generic wizard confirmation must not be used to auto-approve the automation step.

**FR-16 — Show actual progress.** The persistent card reports pending, running, waiting-for-you, failed, and completed actions with text as well as visual indicators. A completed row requires a canonical successful receipt or verified existing state. The workspace may appear on Home's existing map once it actually exists, with its true setup status; map placement is not another required choice. No fabricated percentage, forced animation delay, or “done” claim while only a proposal exists.

### E. Folder access

**FR-17 — Ask through the domain's grant surface.** Before **Choose folder**, explain that the selection grants File Janitor access to one folder's immediate files and creates or reuses its `Filed` destination. Use the existing native picker and owner-scoped File Janitor setup boundary. Choosing the folder is the folder-consent action, as in the existing wizard; no hidden filesystem path is supplied by an agent, URL, or generic setup action. The current picker/platform support boundary is unchanged. An unavailable picker produces an actionable unsupported/error state, not a shell/path-field workaround.

**FR-18 — State the exact access.** The folder permission disclosure must say:

- Ori lists names, types, sizes, and dates of files directly inside the chosen folder.
- Ori creates/reuses `Filed` inside that folder during setup.
- No file contents are read in this flow, and metadata-only classification does not send file data to a model.
- A later, separate per-file review may move approved files into `Filed/<category>` or send specifically approved items to system Trash; setup grants no approval for those operations.
- Ori never permanently deletes files through File Janitor and does not gain access to unrelated folders.

Display the resulting approved folder from canonical domain status. Do not say “nothing changed on disk” after a successful folder grant.

**FR-19 — Folder grant does not start monitoring.** A new folder grant uses the domain's paused setup behavior. Until the separate monitoring approval succeeds, no watcher or scheduled catch-up starts and the Assistant does not initiate the first scan. Picker cancellation creates no grant and is not reported as an error. Permission denial keeps the existing workspace/run resumable.

**FR-20 — Respect conflicts and changed roots.** Preserve the domain's exclusive-folder claim, canonical path/symlink checks, directory-reference ownership, and action-time validation. If another owned workspace already manages the folder, offer its authorized route or another folder choice; do not steal the claim, silently switch the target workspace, or remove the partial workspace. A later changed or revoked root invalidates pending continuation. Regrant/relink uses the existing explicit domain action, never an automatic repair.

### F. Monitoring and first result

**FR-21 — Ask once for the described monitoring.** After a valid folder grant, show the exact folder, watcher behavior, actual settling/debounce policy, `Filed` exclusion, daily local time/timezone, and the initial metadata-only scan. Use **Start monitoring and prepare review** as the meaningful approval. Existing approved monitoring is not re-approved gratuitously, but a user-paused workspace is never resumed automatically. When adopting an unfinished setup with already approved monitoring, offer **Prepare first review** if an initial assisted scan has not been authorized; do not infer that new scan approval from opening/resuming the run. If a pending batch already supplies the result, link to it without requesting another scan. If the schedule/timezone changes before this approval commits, refresh the disclosure. Do not silently use a guessed timezone when one cannot be resolved; ask for it.

**FR-22 — Honor declining monitoring.** Offer **Not now** without pressure. Leave the folder grant and configuration saved, unattended scanning off, and the run awaiting that decision. Do not mark the default setup complete. Existing manual on-demand scanning remains available under its own controls; creating a new scan-only onboarding mode is not part of this release.

**FR-23 — Verify activation before claiming it.** Use the existing automation approval and watcher/scheduler lifecycle. “Monitoring active” requires fresh domain evidence; a saved approval with a failed watcher must say exactly that and offer retry. Retry preserves the saved approval when its scope is unchanged. Never turn on other automations to compensate for a failure.

**FR-24 — Prepare a real first result.** After approved activation and setup readiness, automatically request one metadata-only initial scan through File Janitor's existing scanner. Reconcile any batch a concurrently running watcher/scan has already produced. Use existing eligibility, settling, and candidate deduplication rules; do not bypass them to make onboarding appear successful. Recovery must not issue a second onboarding scan simply because the first response was lost: persist/reconcile the initial-scan attempt and result. A user may separately request another scan through the existing explicit control.

**FR-25 — Keep privacy conservative.** This assisted default never turns on content inspection or invokes an LLM for setup/classification. No provider key is required. A newly configured File Curator can be shown as **Configured; chat needs a model** without blocking monitoring. Existing content-enabled/privacy-customized workspaces must not be silently reset to defaults or scanned under a falsely advertised metadata-only plan: show their actual state and route further setup through explicit manual review.

**FR-26 — Distinguish first-result outcomes.** Display one of these verified results:

| Outcome | Required meaning and next action |
| --- | --- |
| Pending review exists | **Your first batch is ready to review. Nothing has moved.** Show the current pending count and **Review proposed filing**. |
| Successful scan, no eligible new items | **Monitoring is active. No new files are ready to review.** Show the scan time. Do not claim the folder is empty unless that fact is actually known. |
| Files still settling or otherwise excluded | Explain the bounded known reason, if available, and that future normal scans will reconsider eligible items. Do not manufacture a batch or shorten safety waits. |
| Initial scan failed | **Monitoring is configured; the first scan needs attention** only if monitoring is in fact healthy. Preserve setup success, show the failure separately, and offer an appropriate retry. |

Opening the final review uses the existing workspace File Janitor console. A second file-review interface inside the setup card is out of scope.

**FR-27 — Separate milestones.** The existing mission completes from the canonical wizard's first-ready event, preserving its ID and one-time reward. The Assistant-led run reaches its first-result milestone only after FR-26 has a successful batch/no-eligible-items outcome. A completed mission does not hide an initial-scan failure. Browser claims and generated assistant text cannot complete either milestone.

**FR-28 — Setup never approves files.** No setup action selects review candidates, records move/Trash decisions, acquires action-approval tokens, applies file operations, or automatically undoes actions. Preserve the existing empty initial selection, exact file-operation review, stale-approval checks, history, and user-triggered undo. Changes caused by a separately authorized review in another tab must be reported from real results, not attributed to setup.

### G. Persistence, interruption, and recovery

**FR-29 — One durable source for progress.** Store owner, stable run identity, target capability/blueprint revision, approved proposal revision, canonical workspace/agent references, step receipts, pending reason, version, bounded retry state, and timestamps. Keep paths in the existing domain grant/settings records, not copied into generic progress or analytics. Refresh/open reconstructs status from the run plus fresh domain facts. Local storage and the chat transcript are not authority.

**FR-30 — Resume without duplicate effects.** Navigation, closing the panel, browser refresh, and server restart must preserve the accepted run and its approvals. Use stable operation identifiers and version checks for committing steps. Reconcile ambiguous outcomes before retrying. If safe reconciliation is impossible, stop with a truthful partial-result message rather than create a second workspace or guess success. Canonical state changed from another tab must be reflected before further mutation.

**FR-31 — Distinguish close, defer, and stop.** Closing the card hides presentation only; already approved server-side work may finish and cannot cross an unmet permission checkpoint. State this while work is running. **Finish later** stops launching further setup steps after any already accepted in-flight action safely settles and records a resumable deferral. It does not revoke folder access, delete the workspace, or stop monitoring already approved and activated; disclose that state and offer **Pause monitoring** separately. **Resume setup** continues the same approved plan after revalidation.

**FR-32 — Retry technical failures, not permissions.** Permit at most two automatic retries per repeat-safe step after its initial attempt, with bounded backoff. Read current state before retrying a write with an uncertain outcome. Permission denial, user cancellation, ownership changes, conflicts, unsupported configuration, missing identity, and changed consent are never automatic-retry candidates. Once the budget is exhausted, show **Retry** and **Finish later**, not an infinite spinner. Never auto-open/reopen a native permission dialog.

**FR-33 — Explain partial success.** A failure message identifies what succeeded, the failed operation, and the safest next action. Preserve saved resources and history. Never say “nothing was created” after partial creation, “files organized” after a scan, or “monitoring active” from approval alone. Existing owned-resource rollback may clean up a failed atomic creation, but the new flow cannot remove reused resources or treat cancellation as permission for destructive cleanup.

**FR-34 — Respect later user changes.** Folder revocation, a manual monitoring pause, capability removal, workspace deletion/archive, reset, and changed ownership must stop or invalidate automatic continuation as appropriate. A stale run may not recreate deleted resources, regrant access, resume paused monitoring, or restart work after reset. Explicit recovery requires fresh canonical validation and, where scope changed, fresh user approval. After completion, the card is a live status projection; it does not become a perpetual repair agent.

### H. Presentation, control, and accessibility

**FR-35 — One persistent card, one voice.** Reuse the Personal Assistant panel for the setup card and Home's existing mission for discovery/resume. The same saved run is available through the launcher across authenticated app pages. Keep the card accessible even when ordinary Ask is disabled before HQ. Use the hired Assistant's actual name/avatar, but describe service actions honestly: **Ori is configuring File Janitor**, not **File Curator approved your folder**. Ori Guide stays read-only and must not be given setup mutation tools.

**FR-36 — One primary action at a time.** Show a compact checklist and the next actionable blocker. Workspace/team details, receipts, and advanced configuration are progressively disclosed. Use specific labels such as **Choose folder**, **Start monitoring and prepare review**, **Retry watcher**, and **Review proposed filing**, not repeated **Continue** buttons. Do not require the user to open Agents, the workspace creator, the map, or Settings on the ordinary path.

**FR-37 — Preserve accessibility and control.** All actions are keyboard-operable with visible focus and accessible names. Announce important asynchronous state changes through a polite live region; do not announce every poll. Status must not rely on color or animation. Honor reduced motion. A card refresh must not steal focus, reset an active choice, or move a button while the user is activating it. Keep permission text and the relevant action together on narrow screens. Close/Escape and nested-picker focus restoration must remain predictable; an in-flight grant cannot be falsely presented as canceled.

**FR-38 — Provide operational exits.** At the appropriate saved state, show authorized routes/controls for **Open workspace**, **Pause monitoring**, **Manage access**, and **History**. Scope permission controls to this workspace; do not offer a misleading single switch that pauses the entire Assistant or every workspace. Use existing pause/revoke/history services and their confirmations. A failed status read labels cached facts as stale and disables consequential actions until revalidated.

## 5. Non-goals / out of scope

- Rewriting Mission 01, automatically hiring an Assistant, automatically building HQ, or changing the Assistant's working agreement/focus areas.
- Implementing Assistant-led email, calendar, Personal HQ, plugin installation, REAPER, arbitrary blueprints, or other capability setups.
- A universal agent planner, dynamic setup graph/schema, plugin-author setup SDK, or a new general-purpose task runtime.
- An LLM that clicks through the UI, runs setup shell commands, bypasses permissions, or receives new file-changing tools.
- A general natural-language setup-intent classifier. The mission and structured Assistant card are the supported entry points for this first delivery; free-text routing can be considered later.
- Reading file contents, configuring providers, changing global models, or broadening existing local/native-MCP autonomy.
- Autonomous filing, automatic Trash, automatic undo, classification changes, relaxed settling rules, or a redesigned file-review console.
- New remote/native folder-picker implementations or relaxed platform support. Preserve existing manual routes where supported; report unsupported picker environments honestly.
- A new scan-only onboarding mode, multiple-folder batch setup, or a UI to orchestrate several simultaneous capability setups.
- Bulk conversion of existing user/custom/plugin blueprints or automatic migration of unsupported legacy setup records.
- A new telemetry backend, external analytics service, outbound notifications, or production rollout changes.

## 6. Design considerations

### Default journey

1. The user completes the existing hire, or returns with an already hired Assistant.
2. On Home, the user sees the File Janitor outcome recommendation without a forced modal or interruption to another mission.
3. Opening it shows the concrete proposal. **Set up for me** is the first mutation boundary.
4. Ori creates/configures the approved internal resources; the card shows verified progress.
5. The card asks for folder access through the existing native picker and domain grant.
6. The card asks to activate the disclosed monitoring and initial scan.
7. Ori verifies readiness and prepares the first result, then links to the existing review surface.
8. The user separately selects and approves any actual file changes there.

### Representative copy / wireframes

These are illustrative layouts. Counts, names, folder labels, and completion marks must be rendered from current server state.

```text
Organize a folder
File Janitor setup proposal · From your Assistant

I can create File Janitor and configure its File Curator.
You choose the folder and approve monitoring separately.
It uses names and file metadata only. Nothing moves without review.

Workspace: File Janitor       [Details]
File contents: Not read

[Set up for me]  [Customize / Set up manually]  [Not now]
```

```text
File Janitor · Needs your permission

✓ Workspace created
✓ File Curator configured
→ Choose the folder Ori may access
○ Approve monitoring and initial scan
○ Prepare your first review

Ori will list this folder's immediate files and create/reuse Filed
inside it. No file contents are read; no existing files are moved.

[Choose folder]  [Finish later]
```

```text
File Janitor · First review ready

12 files are waiting for your review. Nothing has moved.
Monitoring: Active · Daily catch-up: 09:00 America/Los_Angeles
Folder: Downloads · File contents: Not read

[Review proposed filing]
Open workspace · Pause monitoring · Manage access · History
```

### State vocabulary

The setup-run lifecycle and the capability's live health must be separate facts. For example, “setup completed” can coexist with a later paused watcher; it is not a reason to restart it.

| User-visible state | Meaning | Primary action |
| --- | --- | --- |
| Proposed | No approved run or provisioning yet | Set up for me |
| Setting up | Approved, bounded internal work running | No extra confirmation; Finish later available |
| Needs your permission | Specific grant/automation approval missing | The named permission action |
| Needs your decision | Ambiguous workspace, changed proposal, or customization | Review the specific choice |
| Needs attention | A technical step failed or authoritative state is unavailable | Specific retry/repair, when safe |
| Saved for later | No new setup steps launching | Resume setup |
| Preparing review | Setup ready; initial scan in progress | No ceremonial Next |
| First review ready | A real pending batch exists | Review proposed filing |
| No new files to review | Successful scan with no eligible new proposals | Open File Janitor |
| No longer available | The target was removed or cannot be safely resolved | Explain; never silently recreate |

### Consent summary

| User action | Grants | Does not grant |
| --- | --- | --- |
| Set up for me | Reviewed internal creation/configuration | Folder access, scans, monitoring, file operations, content inspection |
| Choose/grant folder | Domain-scoped metadata access and disclosed `Filed` creation | Monitoring, initial scan by this flow, file-operation approval |
| Start monitoring and prepare review | Disclosed watcher, daily catch-up, initial metadata scan | Move/Trash approval, content inspection, unrelated automations |
| File review confirmation | Exact reviewed operation on explicitly selected files | Approval of other files or future batches |

### Relationship to existing flows

- Keep Mission 02/HQ available and unchanged; it is not a hidden prerequisite for this pilot.
- Change only the File Janitor mission's entry/presentation to the new default. Keep its durable ID and evidence-based completion.
- The manual wizard and Assistant card are two views/controllers over the same canonical setup facts, not two permission ledgers.
- Do not add an always-visible setup dashboard row to Map-first Home. The existing mission and Assistant launcher provide discovery and continuity.
- The preferred first delivery uses a focused card within the current panel, not another top-level page or a second chat persona.

## 7. Technical constraints and integrations

### Existing seams to reuse

| Concern | Verified source | Required integration |
| --- | --- | --- |
| Hired identity and HQ gating | `internal/personalassistant/service.go`; `internal/web/static/js/modules/personal-assistant-panel.js` | Separate narrow setup eligibility from ordinary Ask availability; preserve existing identity checks. |
| Mission discovery and completion | `internal/progression/quests.go`; `internal/web/static/js/modules/tidy-downloads-quest.js`; `docs/architecture/personal-assistant-foundation-contract.md` | Route File Janitor to one saved run; keep first-ready completion/reward behavior. |
| Canonical blueprint and defaults | `internal/projecttemplates/starter/file-janitor/template.json` | Validate the effective built-in identity and snapshot; do not execute author text or arbitrary blueprint IDs. |
| Reviewed creation | `internal/sessionhttp/workspace_handler.go`; `template_agents.go`; `template_agent_review.go`; `workspace_team_readiness.go` in the same package | Preserve strict team review, current create/reuse decisions, ownership, provenance, rollback, and actual creation results. |
| Wizard authority | `internal/setupwizard/service.go`; `internal/setupwizardhttp/`; `internal/filejanitor/setup_adapter.go` | Reuse readiness and confirmed domain operations; reconcile completed steps and trigger the normal completion hook. |
| Folder selection and grant | `internal/workspace/http_handlers_launcher.go`; `internal/filejanitorhttp/handler.go`; `internal/filejanitor/service.go` | Keep user-driven picker and owner-scoped domain grant, explicit paused setup, `Filed` disclosure, and root claims. |
| Automatic scan and privacy | `internal/filejanitor/scan_service.go`; `classifier_provider.go`; `settings_service.go` in the same package | Reuse metadata-only classification, domain scans, consent boundaries, and canonical settings. |
| Existing review/settings/history | `internal/web/static/js/modules/file-janitor-console.js`; `internal/filejanitorhttp/batches.go` | Handoff to the existing review console; preserve per-file action approval and undo. |
| Shared presentation | `internal/web/static/js/modules/personal-assistant-home.js`; `personal-assistant-panel.js` in the same directory; Home/launcher templates | One resumable card with structured actions, not an LLM-generated sequence of commands. |

### Architecture constraints

1. **Keep the coordinator small.** A File Janitor-specific coordinator may track the accepted proposal and advance repeat-safe steps. It must call the existing domain owners rather than redefine their permissions/readiness. Do not build a generic blueprint execution engine for hypothetical future capabilities.
2. **Do not mistake existing helpers for a complete contract.** `CreateFromTemplate` currently sends only a name and template ID through the production create path; it is not, by itself, the reviewed, idempotent proposal-commit boundary required here. The existing create path can also tolerate unresolved template cases. The assisted flow must fail closed when its promised canonical blueprint cannot be resolved.
3. **Use hydrated canonical workspace reads.** Wizard snapshots/provenance are not reliably present in every list/read projection. Resolve candidates through sources that actually carry the File Janitor capability and wizard records. An absent projection field is not proof of absence.
4. **Keep reads non-consequential.** New recommendation/status reads do not allocate runs or provision resources. Existing wizard status evaluation may reconcile its own readiness bookkeeping; that does not authorize configuration, access, automation, or scans.
5. **Enforce scope on the server.** Every action validates user ownership, expected run/proposal version, applicable capability, and the specific allowed step. Do not trust client-supplied owner IDs, completion flags, arbitrary action names, filenames, paths embedded in chat, or a model's approval claims. Native selection remains in the domain's folder-grant path, not a generic coordinator argument.
6. **Close crash windows.** Persist/reconcile action ownership and result references across workspace provisioning, folder grants, activation, and the initial scan. Reuse canonical operation identifiers where available and add only the bounded idempotency needed for this flow. A missing HTTP response is not permission to repeat an untracked mutation.
7. **Respect lifecycle boundaries.** New work must participate in existing application shutdown/reset admission and workspace/capability lifecycle handling. Reset or revocation must not leave a coordinator able to resume stale approved work later.
8. **Keep sensitive data where it belongs.** The card may render the approved folder from domain status for its authorized user. Generic logs, events, URLs, proposal hashes' explanatory payloads, and diagnostics must not expose raw paths, filenames, content, tokens, or credentials. Treat displayed filenames/blueprint descriptions as untrusted text.
9. **No new companion release expected.** The verified File Janitor blueprint, setup adapter, runtime, picker integration, and UI live in this repository. No external plugin/SDK change is currently indicated. Exact native-picker platform packaging behavior was not exercised; do not infer new platform support or require a companion release without further evidence.

### Acceptance and regression scenarios

| ID | Scenario | Required result |
| --- | --- | --- |
| AC-01 | Fresh hire; no HQ and no model | Structured setup is available; ordinary Ask remains gated; default setup/scan completes without an LLM call. |
| AC-02 | Open/reload recommendation or visit its deep link; choose Not now | No workspace, agent, run, grant, watcher, schedule, or scan is created by the read; deferral does not nag on reload. |
| AC-03 | Accept valid proposal with a settled test folder | One reviewed workspace/team; explicit folder grant; explicit monitoring approval; server-verified readiness; real review batch; no file move/Trash. |
| AC-04 | Double-click, two tabs, lost creation response, restart after partial provision | Same run and canonical workspace/agent references; no duplicate effects; ambiguous recovery stops safely. |
| AC-05 | Cancel picker; deny access; picker unavailable | No fabricated grant; no monitor/scan; saved workspace retained; bounded next action. |
| AC-06 | Grant folder but decline monitoring | `Filed` side effect disclosed and verified; access saved; watcher/schedule off; no assisted initial scan; resumable permission checkpoint. |
| AC-07 | Approve monitoring; watcher registration fails | Approval retained; active claim withheld; retry fixes only the failed activation without regranting access. |
| AC-08 | Empty, ineligible-only, and still-settling folders | Honest no-new-proposals/settling result; no artificial batch, forced safety bypass, or claim that all files are organized. |
| AC-09 | Initial scan fails after setup readiness | Mission evidence remains correct; first-result failure stays visible; no duplicate readiness reward or reset of valid grants. |
| AC-10 | One existing partial, one ready/paused, several candidates, legacy supported wizard | Respect the FR-12 matrix; preserve manual pause/settings/history; require a choice for ambiguity. |
| AC-11 | Same-name custom agent or stale team/blueprint revision | No silent reuse or changed plan execution; reviewed resolution required before the affected write. |
| AC-12 | Folder already claimed; wrong owner; directory reference/symlink changed | Domain safety checks reject; no widened access or leaked foreign resource details. |
| AC-13 | Finish later/close at each stage, then refresh/restart/resume | Distinct documented semantics; already approved monitoring reported accurately; no duplicate or unapproved continuation. |
| AC-14 | Manual setup progresses in another tab | Card re-reads canonical state; completed steps are not repeated; changed scope requires review. |
| AC-15 | Revoke access, pause monitoring, remove capability, delete/archive workspace, or reset | Pending continuation stops; no resource resurrection, silent regrant, or unpause. |
| AC-16 | Content-enabled existing workspace or hostile filename/author text | Actual privacy shown; no silently altered settings, model call, new permission, or instruction execution by this flow. |
| AC-17 | Transient failure versus permission failure | No more than two automatic safe retries; permission/cancel/conflict never loops; retry state survives reload. |
| AC-18 | Keyboard-only, screen reader, narrow viewport, reduced motion, nested picker | Every decision reachable; focus restored; truthful live status; no color-only state or required motion. |
| AC-19 | Existing manual creator/wizard, HQ/hire, and file review | Existing flows remain usable; no new auto-started setup task; file selections/approvals/history/undo unchanged. |
| AC-20 | Status service unavailable or cached ready state becomes stale | Unavailable/stale shown explicitly; no guessed success or consequential action on stale authority. |

Use package/module tests for authorization, transitions, and repeated requests, plus a real end-to-end browser demonstration in disposable state. Relevant existing browser coverage includes `tests/file-janitor.spec.ts`, `tests/file-janitor.a11y.spec.ts`, `tests/blueprint-setup-wizards.spec.ts`, `tests/workspace-team-readiness.spec.ts`, and `tests/personal-assistant-foundation.spec.ts`. Stub-only screenshots do not prove that folder grants, monitoring, or scan receipts work.

## 8. Success metrics

### Release acceptance targets

- **Three meaningful approval moments** on the fresh default path, excluding native OS interactions and later file review: setup plan, folder grant, monitoring/initial scan. No additional name/team/Next/Finish decisions unless a real conflict requires one.
- **No required app-page navigation before the first-result card** on the supported default path, other than the native folder picker. Opening the final review console is an intentional handoff.
- **Zero unapproved folder grants, monitoring activations, content reads, model calls, or file operations** in the acceptance suite.
- **Zero duplicate workspaces/agents or successful initial-scan effects caused by replay** in the concurrency/restart/lost-response scenarios. Retrying a verified failed attempt is distinct from repeating a successful or unresolved one.
- **Every blocked state has a truthful next action or an explicit unavailable explanation.** No indefinite spinner or false completion.
- **All accepted grants and canonical resource references survive interruption**, subject to revocation/reset, with no second approval for unchanged completed steps.

### Evaluation after implementation

Compare the current manual path and the assisted path with the same disposable test folder and environment:

1. Time from opening the proposal to first useful result, separating active setup time from user waiting/OS permissions and normal settling delays.
2. Number of user decisions and app-surface changes before the first result.
3. Where users defer or abandon setup: proposal, folder permission, monitoring, or technical failure.
4. Resume success rate and time to recover from a simulated failed step.
5. User comprehension: can they explain what Ori may read, whether monitoring is running, and whether files have moved?

No baseline timings, production conversion rates, or success percentages have been measured in this planning session. Gather a baseline before making an improvement claim. Use local test observations and bounded existing diagnostics; do not add an external analytics dependency. Any recorded events contain stage/outcome/timing codes only, not personal paths or filenames.

## 9. Open questions

The product choices needed to draft this PRD are resolved. The following are bounded implementation-discovery questions, not permission to expand the release:

1. **Durable orchestration seam:** Can the existing setup-journey persistence support this small flow without imposing unrelated plugin/project steps, or is a File Janitor-specific run record smaller and safer? Choose based on the required receipts, restart reconciliation, and permission boundaries—not on a goal of building a universal system.
2. **Reviewed creation and scan crash recovery:** Which existing operation identifiers can be reused, and where are additional bounded idempotency records required to prove AC-04? Resolve before wiring automatic creation/first scan.
3. **Panel integration:** Where can the shared setup card mount so it remains visible before HQ without weakening Ask availability or conflicting with Today/HQ briefing focus? Verify in the actual Home/launcher layout before finalizing UI structure.
4. **Native picker support:** Which existing supported desktop environments will be exercised in the delivery demo? Preserve unsupported-state behavior; a new platform picker is not an implicit dependency.

### Future extensions, explicitly deferred

Once File Janitor validates this interaction pattern, separately scope Assistant-led email/calendar setup and, if justified, shared proposal/permission-card presentation. Each capability must define its own access, external side effects, credential needs, and recovery contract. This PRD neither authorizes those integrations nor requires an extensibility framework in advance.
