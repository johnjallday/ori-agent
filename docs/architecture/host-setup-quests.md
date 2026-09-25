# Host-owned setup quests

Status: **Implemented** on `feature/455-goal-first-gmail-setup-quest`
(2026-09-15). This document records the contract for host-owned setup quests.
Sections 2–7 were written as the plan in the Group 1 spike. Where the
implementation differed, the drift register in §1.3 wins: D11–D14 record the
Group 2–4 refinements and their demo evidence. Line references in §1 point at
the base commit `48392740`. Line references in later sections are approximate
after implementation, so use the named function instead.

### Folder-fed capability entry point (current)

The post-hire installed-app specialist offer is retired. `POST /api/onboarding/detect`
remains for generic onboarding app/profile reads but never matches a specialist,
and Home never calls it to produce an offer. A person must feed a folder or a
single file to the Folder Digest before a capability card can appear. The host
capability table in `internal/folderdigest/tables.go` selects the reviewed
integration. A REAPER `.rpp` project confirmation opens the host/plugin quest;
its `project_connect` step owns workspace creation and a canonical ready quest
receipt verifies the resulting project and the shown folder. A collection of at
least five immediate audio project folders instead offers the reviewed Music
Project Management Home provider. Its release disclosure, independent
Home-only Group Template creator, and checked provenance are the resolution
path. An existing Home suppresses the collection offer. Neither path installs
a native music application, nor does a folder scan create a workspace. Existing
accepted specialist relationships and their setup reporting remain readable.

What shipped, in one place:

| Area | Where |
| --- | --- |
| Account-link shape and step kinds | `internal/specialist/setup_journey.go` |
| Host quest data (`email_ops_setup`) | `internal/hostquests/` |
| Host source, catalog, identity, status read | `internal/setupjourney/quests.go`, `host_quest_catalog.go` |
| Routes and the non-creating status handler | `internal/setupjourneyhttp/quests.go`, `internal/server/routes.go` |
| Readers and the reviewed mailbox link | `internal/server/email_ops_quest_adapters.go`, `email_ops_quest_mailbox.go` |
| Capability card routing | `internal/personalassistant/capabilities.go` |
| Journey UI for the new kinds | `internal/web/static/js/modules/setup-journey.js`, `setup-journey-account-steps.js` |
| Creator team lock | `workspace-creator-state.js`, `sessions.js` |
| Home resume card | `email-setup-quest-card.js`, `components/dashboard.tmpl` |
| Adversarial and browser coverage | `internal/server/email_ops_quest_adversarial_test.go`, `tests/email-setup-quest.spec.ts` |

Source of truth for requirements: `tasks/prd-455-goal-first-gmail-setup-quest.md`
(Issue #455). Requirement IDs (`FR n`) below refer to that PRD.

---

## 1. Seam recheck (task 1.1)

Feature checkout: branch `feature/455-goal-first-gmail-setup-quest` at
`48392740` (= `origin/dev` head, 2026-09-14). The PRD was written against
`c0b47199`; one PR (#486, group blueprints in Create Group) landed in between
and is in this branch's base.

### 1.1 Seams the PRD §7.1 names, re-verified

| Concern | PRD reference | Actual at `48392740` | Status |
| --- | --- | --- | --- |
| Closed kind enum | `internal/specialist/setup_journey.go:25-46,151-176` | `SetupJourneyRequiredSteps = 5` (`:25`); five `SetupStep*` consts (`:33-37`); fixed `setupJourneyStepOrder` (`:40-46`); count check (`:154-156`); positional kind check (`:174-176`). No `WorkspaceLaunch` field rule by kind: it is optional for every declaration (`:186-195`). | unchanged |
| `actionDefinitionsByKind` | `internal/setupjourney/actions.go:133-320` | `:133-178`; five kind keys. `NewReaderRegistry` (`:234-252`) requires `len(readers) == len(actionDefinitionsByKind)` and rejects unknown kinds. `validCanonicalRead` (`:270-306`) pins each typed projection to its kind and validates action IDs against the kind's definitions. `validResultForKind` (`:308-327`) has `default: false`. `NormalizeActionID` (`:330-340`) scans every kind. | unchanged |
| `ReadScope` | — | `:183-202`; has `QuestSource`, `UserTemplateID`, no `Shape`. | unchanged |
| `scopeForRun` | `service.go:590-611` | `:590-611`; sets `QuestSource`/`UserTemplateID` only when `root.SpecialistSlug == "user_template_quest"`. | unchanged |
| `applyCanonicalRead` | `service.go:613-636` | `:613-636`; three kind cases, `ProjectWorkspaceID` written by `project_connect`. | unchanged |
| `baseProjection` | — | `:778-802`; `Journey.Source`/`TemplateID` set only for `user_template_quest`. `Read` (`:253-272`) takes the user-template mutation lock only for that source; `read` (`:319-323`) also stamps `Source`/`TemplateID`/`AttachmentID`. | unchanged |
| Quest sources | `quests.go` | `QuestSource` has two values (`:17-20`); `validQuestKey` (`:346-359`) `default: false`; `normalizeQuestKey` (`:361-371`); `questRelationshipID` (`:373-377`); `ForQuest` (`:399-413`); `ForUserTemplateQuest` (`:415-443`, resolves the quest ID from the catalog list); `questDeclaration` (`:445-486`) switch with fail-closed `default`, then `FindQuestRoot`. | unchanged |
| `FindQuestRoot` | `quest_store.go` | `:12-55`; **two** branches: `user_template` (exact relationship + slug) and an `else` that serves plugin **and** legacy roots. A host key must get its own branch so it never falls into the `else`. | unchanged |
| Summary reader | `builder_setup_journey.go:24-40` | `readSetupSummary` (`:27-39`) offers `open_project`/`open_live_setup` whenever `ProjectWorkspaceID != ""` and Home actions whenever `HomeWorkspaceID != ""`. Must become shape-aware (FR 10). Catalogs are combined at `:228-240`. | unchanged |
| HTTP scoping | `setupjourneyhttp/quests.go` | `questService` interface (`:19-23`: `ListQuests`, `ForQuest`, `ForUserTemplateQuest`); `ScopeQuest` (`:56-74`); `ScopeUserTemplateQuest` (`:78-96`). `Handler.Service` (`handler.go:25-31`) is read/open/dismiss/child/mutate only; a non-creating status read needs a new handler method. | unchanged |
| Routes | `routes.go:1063-1099` | `registerSetupJourneyRoutes` now at `:1070-1104` (shifted +7). Golden files: `internal/server/testdata/route_paths.txt` and `route_table.golden` both enumerate the quest families. | line shift only |
| Readiness authority | `email_readiness.go` | `Evaluate` (`:81-147`): connection (`evaluateConnection`, `:151-219`: load → identity → Gmail grant enabled → grant healthy → vault preflight) → binding (`:95-111`) → credential (`:113-146`). `evaluateConnection` is unexported but the new readers live in `internal/server`, so no export helper is needed. | unchanged |
| Email Ops resolution | `workspace/email_ops.go` | `ResolveEmailOpsWorkspace(src EmailOpsWorkspaceSource, userID)` (`:27-59`); its doc requires a provenance-hydrated `Get`. The daily brief passes `b.workspaceFileStore` (`builder_handlers.go:1188-1194`). | unchanged |
| Inbox name gate | `mailbox_access.go:98-107` | `isInboxAgent` (`:100-108`) consults `personalhq.V1Roster` for slug `inbox` first, then the literal `"Inbox"`; `provision.go:57-62` says the Inbox specialist moved out of the roster and the literal name is the rule. Effective check is still a display-name compare. | roster indirection, same effect |
| Capability card | `capabilities.go:144-170` | `emailCapabilityCard(ctx, reader, hqID)` (`:144-167`); default action "Set up email" → `/settings#google-account`; the server reader (`personal_assistant_capabilities.go`) evaluates the **HQ** workspace. `CapabilityService.workspaces` is the sync store (`builder_dailybrief.go:279-281`). | unchanged |
| Creator entry | `sessions.js:10730-10760,709-766` | `showAddWorkspaceModal(options)` (`:10830`), `beginWorkspaceCreatorContext` (`:10795-10827`) with keys `entryPoint`, `blueprint`, `postCreateAction`, `mapOrigin`, `selection`, `onCreated`, `guided`, `drafts`; show listener (`:716-773`) preselects the blueprint card. | unchanged, but see D2/D3 |
| Journey UI | `setup-journey.js:1587-1712` | root-route selection (`:1653-1660`), URL parsing (`:1752-1767`). | unchanged |
| Link helpers | `setup-quest-links.js` | `setupJourneyAPIRoot` (`:35-43`), `setupQuestURL` (`:45-59`), `setupQuestForTemplate` (`:63-89`). | unchanged |
| Plugin/user validators | `plugin/setup_quest.go`, `projecttemplates/user_setup_quest.go` | Both rely solely on `specialist.NormalizeSetupJourney` plus `WorkspaceLaunch != nil` (`setup_quest.go:30-33`; `user_setup_quest.go:170-176`, `342-353`). `NewUserSetupQuest` hard-codes the five kinds (`:328-341`). Widening the normalizer silently loosens `parseUserSetupQuest` and `validateSetupQuests` unless FR 4 adds an explicit shape check. | confirmed risk |
| Compatibility fixture | "REAPER compatibility JSON" | `internal/specialist/compatibility/reaper-setup.json`, embedded by `legacy_setup.go` (`//go:embed compatibility/*.json`). | located |
| Reader-registry test sites | 11 | 11 test call sites + 1 production: `setupjourney/service_test.go:81,110,117,306`, `presentation_test.go:111`, `integration_replacement_test.go:169`, `quest_compatibility_test.go:68`, `integration_adapter_test.go:345,413`, `setupjourneyhttp/quests_test.go:55`, `server/personal_assistant_setup_reporting_test.go:58`. Each builds its own `readers` map; there is no shared helper. | count confirmed |
| Events | `specialistevents` | `events.go:16-33` names; `:41-47` outcomes. No account-link event exists. | as expected |
| Email Ops manifest | `starter/email-ops/template.json` | `builtin_version: 4`; no `setup_quest`; `setup_wizard` steps `mailbox` (`account_link`), `readiness`, `summary` in the **workspace wizard** kind vocabulary. | as expected |
| `SetupQuestID` consumers | — | Parsed at `templates.go:647`; cleared only for variants (`:682`, `template_variant.go:233`) and standalone/grouped derivations (`group_requirements.go:271,299`; `grouprequirements/service.go:660`). `group_requirements.go:365` projects `pre_workspace_setup` from it for grouped composition only. A built-in's own reference is never cleared. | FR 17 safe |
| Absent files | — | `internal/hostquests/`, `internal/server/email_ops_quest_adapters.go`, this document, `docs/agents.md` all absent before this task. | as expected |

### 1.2 Merge state of the coordinated Issues (checked via `gh`, 2026-09-14)

| Issue | State | Labels | Effect |
| --- | --- | --- | --- |
| #440 Inbox authorization by display name | **open** | `backlog`, `size:quick`, `bundled` | FR 25 rename lock stays. |
| #441 email capability card reports "not configured" | **open** | `backlog`, `size:quick`, `bundled` | FR 40 applies to the current `capabilities.go`. |
| #445 resolve the Inbox specialist by role everywhere | **open** | `feature-proposal`, `size:planned` | No re-application needed; PR body still names it. |
| #486 group blueprints in Create Group | **merged** 2026-09-14T23:59Z | — | In this branch's base. Introduced no new creator option keys beyond `guided`/`mode: 'guided'` (see D2). |

No merged PR in the last 100 references #440, #441, or #445.

### 1.3 Drift register

Each entry: what the PRD assumed, what the code does, and the decision this
implementation takes. Decisions marked *(owner: task)* are executed there.

**D1 — Builder order makes lazy resolution unnecessary.** PRD §7.2 worried that
the readiness evaluator and mailbox linker are wired after the setup-journey
readers. Actual order in `builder.go`: `initializeWorkspaceStore` (Phase 18,
`:541`) ends by calling `wireMailboxRuntime` (`builder_workflow.go:421`), which
sets `b.emailReadiness` (`builder_handlers.go:1171`), the mailbox linker
(`:1178`), and `b.gmailSink.lifecycle` (`:1152`). `initializeSetupJourney` runs
inside `initializeDailyBrief` (Phase 22.6, `builder_dailybrief.go:289`), after
all of that. Decision: capture `b.emailReadiness`, `b.connStore`,
`b.vaultStore`, `b.gmailSink`, the linker, `b.setupWizardService`, and
`b.workspaceFileStore` directly at wiring time, with nil guards that degrade the
account readers to `owner_unavailable`. `wireMailboxRuntime` returns early when
the workspace store or vault store is nil (`:1141`), so nil is a real case.
*(owner: 1.5 confirms, 3.4 implements)*

**D2 — The creator's `guided` option is already taken.** `group-builder.js:279-296`
passes `guided: { state, submit, review, error, groupTemplateId }` together with
`mode: 'guided'`, and `workspace-creator-state.js:19,78-79` defines that mode as
a fixed-kind Group creator without a roster. `sessions.js:7081-7083, 8186,
8429-8433, 9201-9207` all gate on `mode === 'guided' && guided`, so passing
`guided` alone (as FR 24 wrote) would be functionally inert but semantically
overloaded and fragile. Decision: the host quest passes a distinct creator
option (working name `teamLock: { agentNames: ['Inbox'], reason }`) threaded
through `createCreatorContext` in both `workspace-creator-state.js:47-72` and
the `sessions.js:10805-10825` fallback, consumed only by the Team step's rename
control; `mode` stays `ordinary`, `entryPoint` is `host_setup_quest`. Final key
name is fixed in task 1.6. *(owner: 1.6 decides, 2.8 implements)*

**D3 — Workspace creation never reports back and navigates away.**
`onCreated` fires only from `finishOrdinaryGroupCreate` (`sessions.js:8541-8552`).
The workspace path has no callback and, unless the create is map-origin
(`:9918-9952`), navigates to `/workspaces/<slug>` on success (`:9955-9958`),
which would leave the quest modal behind. FR 24 needs two bounded changes in
`sessions.js`: invoke `creatorContext.onCreated({ folder, workspaceId })` on the
workspace path, and add a stay-on-page branch (when the context requests it)
that refreshes surfaces the way the map-origin branch does (`loadFolders`,
`WorkspaceHub.loadWorkspaces`, `OriHomeCockpit.refreshQuietly`) instead of
navigating. The journey additionally re-reads on the modal's `hidden.bs.modal`
so a dismissed or failed create still reconciles. *(owner: 2.8)*

**D4 — `isInboxAgent` reads the roster first.** `mailbox_access.go:100-108`
now loops `personalhq.V1Roster` for slug `inbox` before the literal compare;
the roster no longer contains Inbox (`provision.go:57-62`), so the literal
`"Inbox"` compare is the live rule. FR 25's lock is still required. No change
to the PRD's intent.

**D5 — FR 40 must not reuse the capability service's workspace read.**
`CapabilityService.workspaces` is the SQLite-primary sync store, and
`workspaceCapabilityCards` reads `ws.TemplateProvenance` off `ListActive()`
(`capabilities.go:219-222`), which the sync store does not hydrate (see
`ResolveEmailOpsWorkspace`'s doc comment and the daily brief's
`emailOpsSource`, `builder_handlers.go:1188-1194`). Decision: the "no Email Ops
workspace" branch resolves through a provenance-hydrated source supplied by the
server adapter (`personal_assistant_capabilities.go`), not through
`s.workspaces`. The pre-existing calendar/domain detection on the sync store is
out of scope. *(owner: 4.1)*

**D6 — Two different "guided setup" actions exist in the picker.** The
`open_guided_setup` action in `sessions.js:6909-6986` is the group-requirement
action (`grouprequirements.ActionOpenGuidedSetup`) routed to
`ProjectTemplateCard.openSelectedGuidedSetup`. The template-level "Open Guided
Setup" link (`project-templates-manage.js:824`, `templates-page.js:351-411`) is
the one FR 41 extends, via `setupQuestForTemplate`. The former is untouched.

**D7 — Kind-name overlap across two enums.** The workspace Setup Wizard already
has `workspace.SetupStepKind` values `account_link` and `readiness`
(`email-ops/template.json:56-67`). The journey kind `account_link`
(`specialist.SetupStepKind`) is a different Go type in a different package; no
code collision, but every doc and test name must say which vocabulary it means.
The PRD's choice of no journey `readiness` kind (§6.5) also avoids a second
overlap.

**D8 — `email-ops` `builtin_version` is 4.** FR 17 bumps it to 5. The manifest
has no `group_requirement`, so the `pre_workspace_setup` projection
(`group_requirements.go:365`) is unaffected. *(owner: 2.5 verifies the library
refresh in the demo)*

**D9 — Host quests need no mutation lock and carry their own ID.** Unlike
`ForUserTemplateQuest`, which derives the quest ID from the catalog list and
whose `Read` takes a library lock (`service.go:254-270`), a host quest is an
immutable embedded declaration addressed by ID. `ForHostQuest(ctx, userID,
questID)` follows `ForQuest`'s shape and `Read` takes no lock. *(owner: 1.3)*

**D10 — `NewReaderRegistry` has no shared test helper.** All 11 test call sites
build their own `readers` map; 4 of them size it from
`len(actionDefinitionsByKind)` and fill it by iterating the map, so they adapt
automatically; the rest enumerate kinds by hand and must add the three new
kinds mechanically. The exact list is in task 1.2.

**D11 — Refinements found while implementing Group 2 (supersede §4 where they differ).**

- *Receipt lag.* During reconciliation `scopeForRun` reads a root run's
  `ProjectWorkspaceID` from the persisted root row, so a receipt produced by
  one step reaches later steps only on the *next* read. Specialist journeys
  already live with that lag. For the account-link shape `scopeForRun` now
  reads the fresh candidate's receipt, so steps 2–3 see the workspace step 1
  resolved on the same read. The specialist path is unchanged.
- *`Linked` does not imply an address.* A binding whose vault account vanished
  is linked but has no readable email, so `validAccountLinkProjection`
  requires only `Ready ⇒ Linked && AccountEmail != ""`.
- *No `CanonicalReceiptID` on `account_link`.* §5.2's final decision is
  enforced by `validResultForKind`: an account-link result carries no ID at
  all, and an account-connect result carries none either.
- *Workspace route is optional.* A slug that would need escaping is omitted
  and the client resolves the workspace by ID; the ID and label stay paired.

**D12 — The Team step staffs roles, and Inbox is optional (found in the Group 2
demo).** Since the role-vacancy work the creator's Team step renders each
blueprint agent as a role with Create, Assign, and Clear, and an ordinary
blueprint's non-first roles are optional and start empty. A workspace created
that way can have no agent named Inbox, which leaves mail reading unusable
(`isInboxAgent`) while the readiness evaluator still reports the mailbox as
linked. The per-agent setup form §6.2 planned to lock is not on this path.
Decisions, implemented:

- The quest's creator options add `stageBlueprintRoles: true`. Once the plan and
  saved roster are ready, `stageGuidedBlueprintRoles` stages every project role
  as a Create fill under its declared name, or Assign when a saved agent
  already has that name. It only edits the draft; creation still needs the
  user's confirmation.
- `teamLock` covers all three role paths for the locked label: the Create form's
  name field is read-only with the reason, saving another name is refused,
  Assign of a differently named agent is refused, and Clear is refused with a
  warning toast. Postmaster stays fully editable. The per-agent setup form
  keeps the same lock for blueprints that still use it.
- The stay-on-page behaviour is a context option, `stayAfterCreate`, rather
  than a check on `entryPoint`, so other guided callers can reuse it.
- Known limitation: a workspace created from the picker outside the quest can
  still leave Inbox empty. Step 1 completes for it, because FR 23 resolves the
  workspace only. Surfacing a missing Inbox is left to #440/#445, which own the
  Inbox role rule.

Demo evidence (provider-less sandbox, headless Chromium, 2026-09-15): Templates
shows "Ori built-in · read-only" with the host launch link; status is
`exists:false` and the database holds no journey rows before the first open;
the quest opens on step 1; Review your team opens the creator on Team with the
name "Email Ops" and both roles staged; Clear on Inbox is refused with the lock
reason; Create goes through `POST /api/workspaces`, the page stays on Home, and
the quest reopens with step 1 complete and the workspace receipt; after a
server restart the roster is Postmaster and Inbox and the workspace is in
`workspace_allowlist.json`; the 400px layout holds. The in-browser Chrome
extension could not capture screenshots in this session, so the demo ran
through `scripts/demo-455-email-quest.mjs`.

**D13 — Group 3 refinements (supersede §4.3 and §5.4 where they differ).**

- *Unfinished is not blocked.* Step 2 follows the readiness evaluator's own
  split (`emailStepReadiness`): "connect Google" and "enable Gmail" are
  unfinished first-time setup, so the step is active with its two navigation
  actions and no reason code. Only a missing OAuth client, a grant that needs
  reconnecting, and a vault repair block the step. The reason codes
  `account_connection_required` and `account_capability_not_enabled` were
  therefore never emitted and are removed from the closed set.
- *Consequence observed means Ready.* `ConsequenceObserved` receives only the
  step read, so it cannot recompute the binding-to-credential equality §5.4
  described. It returns true when the link step is complete, which is exactly
  the evaluator's Ready. The link is idempotent and readiness is the
  consequence the user asked for, so an interrupted commit reconciles to
  `already_current`. A Go test proves it without a second link.
- *Stale review.* A receipt that no longer names the user's Email Ops
  workspace, a changed connection or credential, or a binding written in
  between (the workspace's own CTA) all surface as `review_stale` through the
  owner digest or `ErrConflict`. After a CTA link the next read shows the step
  complete with one binding.
- *Ready shows its summary.* A ready run has no current step, and the shared
  modal fell back to the first step, hiding the summary actions. The fallback
  now picks the final step. Specialist journeys never reached that branch.
- *Summary copy.* The summary description no longer repeats that triage needs
  a model; the separate note appears only when the summary offers model
  settings.
- The mailbox linker is now built whenever the workspace and vault stores
  exist and is stashed as `b.mailboxLinker`; the Personal HQ handler receives
  the same instance as before.

Demo evidence for Group 3 (provider-less sandbox, headless Chromium,
2026-09-15). What ran against the real server: step 2 with no OAuth client
shows "Google sign-in isn't configured on this Ori server yet" as blocked;
Open Google Account opens `/settings#google-account` in a new tab with the
quest still open; Check again re-reads. With a seeded connection file
(identity and a healthy Gmail grant, no vault) step 2 names "Repair vault" and
opens `/settings#google-account?gc_action=repair`; loading Settings then
revalidated the seeded credential and Check again correctly showed "needs
reconnecting". What was mocked: the link review card and the ready summary were
rendered from fixture responses in the server's exact shape, because no live
Google account or vault was used (creating a vault was avoided so the demo
could not touch the real macOS keychain). The commit itself (sink → linker →
wizard confirm, one read/search binding, replay without re-linking, stale
review, secret scan, parity with workspace email status) is proven by the Go
integration tests in `internal/server/email_ops_quest_mailbox_test.go`. The
server log shows no model request.

**D14 — Group 4: where the resume card lives and what closing means.**
- *Mount.* FR 48 places the card "below the first mission in the quest-log
  area". Since the PRD was written, Home's quest log moved into the on-demand
  Quests flyout (`#cockpitQuestsFlyout`). The card mounts there, directly
  after `#questLog`, so it appears only when the user opens Quests. It never
  auto-opens the flyout, the quest, or a coachmark.
- *Closing a host quest only hides it.* The shared modal's close button and
  Escape recorded the journey's persisted dismissal ("Saving your place…").
  For the host quest that made FR 44 unreachable: leaving the quest
  unfinished always set `dismissed: true`, so the card could never show.
  Closing a `source: "host"` journey now hides the modal without a mutation.
  The card's "Not now" is the one explicit dismissal, and any Open clears it
  (FR 46). Plugin and user-template journeys keep close-as-dismiss unchanged.
  Nothing else reads the host run's `dismissed` flag. `JourneyDismissed` is emitted for the
  host quest only on "Not now".
- *Parameter isolation.* The Build-HQ walkthrough starts only on
  `?quest=build-hq`, and the setup journey opens only on `?setup=quest`. JS
  tests pin both directions.
- *Pre-existing Home failures.* Two `tests/home-workspace-cockpit.spec.ts`
  Group-creation tests fail at the Group Manager setup gate added by #486. They
  fail identically on the base commit `48392740` built separately, and three
  serial tests after them do not run. The other 47 tests pass.

Demo evidence for Group 4 (fresh provider-less sandbox, headless Chromium,
2026-09-15). What ran against the real server: hire and Build HQ through the
assistant API made the capability cards active. The Email card reads "Set up
email" and links to the quest URL, and clicking it opened the quest. Before
that, the Quests flyout showed no card and the status read reported no root.
After closing on step 1, the root was `in_progress` with `dismissed: false`,
and the flyout showed "Set up Email Ops · Step 1 of 4 · Review your Email Ops
team". At 400px both Resume and Not now were reachable, with nothing floating
over them. "Not now" set `dismissed: true` and hid the card across a reload.
"Open Guided Setup" on Templates cleared it, and closing again brought the card
back. Resume opened the quest in place. What was mocked: completion. The status
response was replaced with a ready run and with a regressed run that has
`first_completed_at`, and neither showed the card, because finishing live needs
a Google account and vault. The same rules are unit-tested in
`email-setup-quest-card.test.js`.

**Final validation (Group 5, 2026-09-15).**

Final demo on a fresh build and sandbox, headless Chromium, at 1440px and
400px. Live against the real server: hire and HQ, the capability card, every
resume-card rule, Templates, the creator Team step with the Inbox lock, and
workspace creation. Also live: step 2 blocked with no OAuth client, and step 2
naming **Repair vault** with a seeded connection. A server restart kept the
quest at step 2 and the workspace roster as Postmaster and Inbox. Mocked: the
link review, the ready summary, and completed-quest card states. No live Google
account or vault was used, so no mail binding existed to check across the
restart. The reviewed link, its binding, replay, and every refusal path are
proven by the Go tests instead.

Gates on the branch:

| Gate | Result |
| --- | --- |
| `make test` | Pass |
| `make test-js` | Pass |
| `make lint-new`, `make vet`, `gofmt` on changed files | Clean |
| `npm run lint`, `npm run format:check` | Clean |
| `make readme-check` | Pass |
| Scoped `gosec` over changed packages | 4 findings, all in files this branch does not touch (`server/initialization.go`, `projecttemplates/starter.go`) |
| `tests/email-setup-quest.spec.ts` | 6 of 6 at 1280px and 400px, stable over repeated runs |
| 21 affected Playwright specs: 204 passed, 17 failed, 14 skipped or not run | 13 failures reproduce identically on base `48392740`. 4 more pass on a fresh branch server and fail only after other specs leave state behind, such as a hired assistant whose dock covers 390px controls, or under parallel load |

`internal/sessionhttp` is untouched. `scripts/demo-server.sh --rev <commit>`
now serves any commit from its own sandbox for baseline comparisons.

### 1.4 Not drift, but worth stating

- `gh` fails under the sandbox with a keychain TLS error; the merge-state check
  above ran unsandboxed. Go builds and tests will also run unsandboxed per the
  repository's smoke-testing notes.
- `docs/agents.md`, referenced by `CLAUDE.md`, is absent; `AGENTS.md` is the
  authority.
- The `personal-hq-quest.js` deep link checks `?quest=build-hq` only
  (`:26-27,58`); the host quest URL always carries `setup=quest`, so the two
  cannot collide (FR 21).

---

## 2. Shape contract (task 1.2)

Decided against `internal/specialist/setup_journey.go` at `48392740`.

### 2.1 Vocabulary

`specialist.SetupStepKind` gains exactly three values (FR 1):

| Const | Value |
| --- | --- |
| `SetupStepWorkspaceCreate` | `workspace_create` |
| `SetupStepAccountConnect` | `account_connect` |
| `SetupStepAccountLink` | `account_link` |

A new `SetupJourneyShape` string type names a compiled ordered kind sequence:

| Const | Value | Sequence |
| --- | --- | --- |
| `SetupJourneyShapeSpecialist` | `specialist` | `integration_install`, `project_connect`, `workspace_setup`, `assistant_program_staffing`, `summary` |
| `SetupJourneyShapeAccountLink` | `account_link` | `workspace_create`, `account_connect`, `account_link`, `summary` |

`setupJourneyStepOrder` keeps its name and content (the specialist sequence);
`setup_journey_test.go:160` and the fuzz test index it. The account-link
sequence is a sibling array. `SetupJourneyRequiredSteps` stays `5` and keeps
meaning the specialist shape's count (FR 3); `builder_setup_journey.go:48`
uses it only as a map capacity hint and `user_setup_quest.go:311-325` as the
five-step authoring rule, both of which stay correct.

Exported helpers:

- `SetupJourneyShapeSteps(shape) []SetupStepKind` returns a copy of the
  sequence (empty for an unknown shape).
- `(*SetupJourney).Shape() SetupJourneyShape` computes the shape from the
  step kinds of a normalized declaration and returns `""` for anything else.
  It is a method, not a field: nothing is added to the JSON encoding, which is
  what makes FR 3's byte-identical guarantee hold by construction.
- `SetupStepKinds() []SetupStepKind` returns the union of both sequences in a
  fixed order (eight kinds). The reader-registry parity test and the
  `actionDefinitionsByKind` coverage test use it so "every compiled kind" is
  defined once.

There is no authorable `shape` field (FR 2). `ParseSetupJourney` keeps
`DisallowUnknownFields`, so a `"shape"` key is rejected as an unknown field.

### 2.2 Inference

`NormalizeSetupJourney` normalizes each step's kind first (trim + lower, as
today), then:

1. Chooses the *candidate* shape from the first step's normalized kind:
   `workspace_create` selects `account_link`; anything else, including an
   empty step list, selects `specialist`. The candidate only decides which
   expected sequence and which error text apply.
2. Requires `len(steps) == len(candidate sequence)`.
3. Requires `steps[i].Kind == candidate[i]` for every position.

A declaration is accepted only when its kinds equal one shape position by
position (FR 2). Because the two sequences differ in first kind and in length,
no declaration can match both, and the candidate rule never masks a valid
declaration of the other shape.

Error text stays in the existing style. For the specialist candidate every
message is unchanged, so every rejection an existing test asserts by presence
still fires with the same text:

| Failure | Message |
| --- | --- |
| Wrong count, specialist candidate | `setup journey must contain exactly 5 steps` (unchanged) |
| Wrong count, account-link candidate | `setup journey must contain exactly 4 steps for the account_link shape` |
| Wrong kind at position *i* | `setup journey step %d kind must be %q` (unchanged; expected kind comes from the candidate sequence) |
| Duplicate step ID | `setup journey step id %q is duplicated` (unchanged) |

The existing specialist tests only assert error presence, apart from
`ParseSetupJourney`'s `unknown field`, `trailing`, and `exceeds` substrings,
which are untouched.

### 2.3 Field rules by shape (FR 5)

Validation order becomes: identity and display text → step kinds and shape →
shape-conditional references → per-step display text → `workspace_launch`.

| Field | `specialist` | `account_link` |
| --- | --- | --- |
| `integration_key` | required stable ID (unchanged) | must be empty: `setup journey integration_key must be empty for the account_link shape` |
| `expected_blueprint_id` | required stable ID (unchanged) | required stable ID (same grammar and message as today) |
| `expected_assistant_program_id` | required stable ID (unchanged) | must be empty: `setup journey expected_assistant_program_id must be empty for the account_link shape` |
| `workspace_launch` | optional (unchanged) | must be absent: `setup journey workspace_launch is not allowed for the account_link shape` |

"Must be a built-in template ID" is not checked in `internal/specialist`:
`projecttemplates` already imports `specialist`, so the normalizer cannot
consult the built-in catalog without an import cycle. The membership rule
lives in `internal/hostquests` (init-time) and in the FR 17 cross-reference
test, which together fail startup and CI for a host declaration whose
`expected_blueprint_id` is not a built-in.

Why `workspace_launch` must be absent: `setup-journey.js:287-294` renders the
four-screen launch UI whenever `journey.journey.workspace_launch` is present
and otherwise renders the plain step rail (`:295-366`). Forbidding the field
for the new shape is what keeps the account-link journey on the rail without
a JS-side shape switch.

### 2.4 Guarantee for existing declarations (FR 3)

Every declaration that normalized before this change normalizes to the same
value after it, because for the specialist candidate the checks, their order
relative to each other, and the struct they produce are unchanged, and no
field is added to `SetupJourney`. Evidence added in Group 2:

- `TestNormalizeSetupJourneyDeclaresNoNewFields`: reflects over
  `SetupJourney`'s JSON tags and asserts the exact tag set the contract
  document lists, so a future field cannot slip into the encoding unnoticed.
- `TestCompatibilityDeclarationNormalizesToItself`: parses
  `compatibility/reaper-setup.json`, re-marshals the normalized value, and
  asserts equality with the marshaled raw decode of the same bytes (the
  fixture is already in normalized form). `TestBuiltInSetupJourneyContract`
  continues to pin its identity and kinds.
- The account-link shape gets the mirror of `validSetupJourney()`
  (`validAccountLinkSetupJourney()`) and a table of rejection cases: five
  steps in the new shape, four steps in the specialist shape, `integration_key`
  set, `expected_assistant_program_id` set, `workspace_launch` present,
  reordered kinds, a specialist kind inserted, duplicated `summary`, and the
  literal `"shape"` key through `ParseSetupJourney`.
- `FuzzParseSetupJourneyFailsClosed` seeds both shapes and asserts that any
  accepted declaration has `Shape() != ""`, its kinds equal that shape's
  sequence, and the shape-conditional field rules hold.

### 2.5 FR 4: the plugin and user validators name the shape explicitly

Both validators today rely on `NormalizeSetupJourney` plus
`WorkspaceLaunch != nil`. That incidental check would already reject an
account-link declaration (which cannot carry `workspace_launch`), but FR 4
makes the rule explicit so the guarantee does not depend on a UI-motivated
field:

- `internal/plugin/setup_quest.go:30-33`: reject when
  `normalized.Shape() != specialist.SetupJourneyShapeSpecialist`.
- `internal/projecttemplates/user_setup_quest.go:170-176` (`parseUserSetupQuest`)
  and `:348-353` (`NewUserSetupQuest`): same check. `NewUserSetupQuest` already
  hard-codes the five kinds at `:328-341`.
- `internal/setupjourney/quests.go:148-152` and `:277-289` (the plugin and
  user-template catalogs) keep their `WorkspaceLaunch == nil` rejection; no
  change is needed there because both feed only specialist-shaped
  declarations, and the shape check upstream now guarantees it.
- `internal/plugin/schema/setup-quest-v1.schema.json` is unchanged; a test
  asserts `steps.minItems == steps.maxItems == 5` and the five `kind` consts,
  in order, so a schema edit cannot ride along silently.

### 2.5.1 Addendum: the install split supersedes the specialist shape

After the setup-quest install split, the `specialist` row in §2.1 no longer
exists. The compiled shapes are `integration_install` (`integration_install`,
`summary`), `project_setup` (`project_connect`, `workspace_setup`,
`assistant_program_staffing`, `summary`) and `account_link` (unchanged).
Candidate selection in §2.2 is by first kind: `integration_install`,
`project_connect` or `workspace_create`. The §2.3 and §2.5 rules that name
`specialist` now name `project_setup` for plugin and user validators.

The host quest source gains a second catalog. Ori generates one
`integration_install` quest per reviewed integration, `install_<key>`, beside
the embedded `account_link` quests. `ForHostQuest` accepts both shapes; the
embedded catalog still accepts only `account_link`. The host route family adds
`POST /api/host-setup-quests/{questID}/restart` for **Start over** on an
incompatible root. It adds no column, and database reset inspection in §3.6 is
unchanged. See
[specialist contract §3.5](specialist-setup-journey-contract.md#35-addendum-install-split-setup_quests_v2).

### 2.6 Every test that enumerates kinds or builds a reader registry

`NewReaderRegistry` has 11 test call sites and one production site. Those
that iterate `actionDefinitionsByKind` adapt on their own once the map has
eight keys; those that list kinds by hand must add the three new kinds. The
new kinds' stub readers return the zero `CanonicalStepRead`, which the
reconciler never consults for a specialist-shaped declaration because it
reads only the declared steps.

| Site | Builds readers by | Change |
| --- | --- | --- |
| `internal/setupjourney/service_test.go:63-86` `readerRegistryStub` | iterating the map | none (reads for undeclared kinds are simply absent from `reads`) |
| `service_test.go:102-120` `TestReaderRegistryRequiresExactlyTheClosedV1Kinds` | iterating the map | add a case that deletes `SetupStepWorkspaceCreate` to prove the new kinds are mandatory; keep the extra-kind case |
| `service_test.go:292-306` | iterating the map | none |
| `presentation_test.go:77-111` | iterating the map | none |
| `integration_replacement_test.go:162-170` | iterating the map | none |
| `quest_compatibility_test.go:48-68` | iterating the map (`default: Complete: true`) | none |
| `integration_adapter_test.go:335-345` and `:405-413` | iterating the map | none |
| `internal/setupjourneyhttp/quests_test.go:49-54` | hand-listed five kinds | add the three kinds |
| `internal/server/personal_assistant_setup_reporting_test.go:32-57` | hand-listed five kinds | add the three kinds (they fall to the existing `default`) |
| `internal/server/builder_setup_journey.go:48-56` (production) | hand-listed | Group 2 registers `workspace_create` and marks the two account readers `owner_unavailable`; Group 3 replaces them |

Other kind enumerations:

| File | What it enumerates | Change |
| --- | --- | --- |
| `internal/specialist/setup_journey_test.go` | `validSetupJourney()` five kinds; `setupJourneyStepOrder` | add the account-link fixture and cases (§2.4) |
| `internal/specialist/setup_journey_fuzz_test.go` | five kinds, `setupJourneyStepOrder` | extend per §2.4 |
| `internal/setupjourney/service_test.go:52-61` `defaultCanonicalReads()` | five specialist reads | unchanged; add `accountLinkCanonicalReads()` for the new-shape reconcile tests |
| `internal/setupjourney/quests_test.go`, `project_recovery_test.go`, `workspace_launch_test.go` | specialist kinds through `defaultCanonicalReads`/`readerRegistryStub` | none |
| `internal/projecttemplates/user_setup_quest_test.go:255` | one wrong-kind draft case | none; add an account-link-shape rejection (FR 4) |
| `internal/plugin/setup_quest_test.go` | normalizes the compat entry | add an account-link-shape rejection (FR 4) |
| `internal/workspace/setup_wizard_test.go` | the **wizard** kind enum | unrelated (D7) |
| `internal/web/static/js/modules/templates-page-user-quest.test.js` | five kinds in the user authoring editor | none (editor stays five-step) |
| `internal/web/static/js/modules/setup-journey.js` `:100,301,336,370-371,413-414,577,677,788,800,948,1105,1515` | specialist kinds by string | none; the new kinds dispatch to the sibling renderer before these branches run |

Documentation that promises "exactly five": `specialist-setup-journey-contract.md:335-337` and `:425-428`, `plugin-setup-quests.md:33,73-74`. Task 5.3 restates them as "the specialist shape, the only one plugins and user templates may author" and adds the shapes addendum.

## 3. Host source, identity, routes (task 1.3)

### 3.1 Key and relationship digest (FR 13)

A host key is `QuestKey{Source: "host", ID: <quest id>}` with `PluginID`,
`TemplateID`, and `AttachmentID` empty. `validQuestKey` gains a
`QuestSourceHost` case: `validateStableID(key.ID) && key.PluginID == "" &&
key.TemplateID == "" && key.AttachmentID == ""`. `normalizeQuestKey` is
unchanged. `questRelationshipID` is unchanged: its identity string becomes
`host::::<id>` (source, then three empty segments, then the ID), which cannot
equal a plugin (`plugin:<plugin>:::<id>`) or user-template
(`user_template::<template>:<attachment>:<id>`) identity, so the digest is
disjoint by construction.

### 3.2 Root identity (FR 14)

| Column | Value |
| --- | --- |
| `owner_user_id` | the current user |
| `relationship_id` | `quest:<digest>` from §3.1 |
| `specialist_slug` | `host_quest` |
| `journey_id` | the declaration ID (`email_ops_setup`) |

`host_quest` passes `validateStableID` (`normalizeRootIdentity`,
`store.go:904-916`) and the 64-byte column check. The partial unique index
`idx_setup_journey_run_root_identity(owner_user_id, relationship_id,
specialist_slug, journey_id) WHERE run_kind = 'root'` (`migrations.go:2145-2147`)
gives exactly one root per user per host quest, and `CreateOrGetRoot` uses
`INSERT OR IGNORE` then a get (`store.go:119-139`), so concurrent first reads
converge on one row. The host root uses the plain `CreateOrGetRoot` path;
`CreateOrGetUserTemplateRoot` and its binding claim are user-template-only.

`FindQuestRoot` gets an explicit host branch:

```sql
SELECT <runColumns> FROM setup_journey_run
WHERE run_kind = 'root' AND owner_user_id = ? AND journey_id = ?
  AND relationship_id = ? AND specialist_slug = 'host_quest'
ORDER BY created_at LIMIT 2
```

and the current `else` (plugin + legacy) becomes an explicit
`QuestSourcePlugin` case with any other source returning `ErrInvalid`, so a
host key can never match a `plugin_quest`, `user_template_quest`, or
assistant-owned (`music_production` and friends) root, and no plugin key can
match a host root. `questDeclaration` gains a `QuestSourceHost` case: the
declaration must have `OwnerPluginID == ""`, `identity.SpecialistSlug =
"host_quest"`, no `Binding`; the fail-closed `default` stays.

`ForHostQuest(ctx, userID, questID)` mirrors `ForQuest`: build the key,
`validQuestKey` + `validateCanonicalRef(userID)`, copy the service with
`quest = &key`, and call `questDeclaration` so an unknown ID fails with
`journey_unavailable` (HTTP 404) before any handler runs. `Read` takes no
mutation lock for the host source (D9).

Projection and event stamping follow the existing `user_template_quest`
branches: `baseProjection` (`service.go:797-800`), `read` (`:319-323`),
`runEventFields` (`events.go:31-34`), and `scopeForRun` (`:602-605`) each gain
a `host_quest` branch that sets `Source = QuestSourceHost` and `TemplateID =
declaration.ExpectedBlueprintID`. `scopeForRun` additionally sets
`ReadScope.Shape = declaration.Shape()` for every run, so readers of every
source can branch on shape (FR 10).

### 3.3 Catalog (FR 15–16)

`internal/hostquests` is data-only and imports only `internal/specialist`:

- `email-ops-setup.json` embedded via `//go:embed *.json`, parsed once in
  `init()` with `specialist.ParseSetupJourney`. A parse or normalization
  error, a duplicate ID, a non-empty `OwnerPluginID`, or a declaration whose
  `Shape()` is `""` panics with `invalid host setup quest <file>: <err>`, the
  same failure class as `mustNormalizeRegistry` for built-in specialists.
- `All() []specialist.SetupJourney` and `Get(id) (specialist.SetupJourney, bool)`
  return deep copies (via `specialist.NormalizeSetupJourney` of the stored
  value, which already copies steps and launch copy).
- `EmailOpsSetupQuestID = "email_ops_setup"` and
  `EmailOpsSetupQuestURL = "/?setup=quest&source=host&quest=email_ops_setup"`.

The package does not import `projecttemplates`, because the FR 17
cross-reference test lives beside the built-in templates and imports
`hostquests`; keeping the data package leaf-level avoids the cycle. Built-in
membership of `expected_blueprint_id` is therefore enforced by that test (and
by the demo), not at `hostquests` init.

`setupjourney.NewHostQuestCatalog(declarations []specialist.SetupJourney)
QuestCatalog` (in `host_quest_catalog.go`) holds normalized copies keyed by
ID. `List` returns, sorted by ID, `QuestSummary{QuestKey{Source: host, ID},
Title, Description, TemplateID: ExpectedBlueprintID, Ownership: "host"}`; the
`TemplateID` carries no `plugin:` prefix (FR 16). `Lookup` requires
`key.Source == host` and an exact ID; `DefinitionDigest`/`ExecutionDigest`
stay empty (only the user-template branch validates them). Neither method
touches the store, a plugin, or the filesystem. The builder appends it to the
combined catalog (`builder_setup_journey.go:228-240`), so
`GET /api/setup-quests` lists it (FR 20).

### 3.4 Routes (FR 18)

`ScopeHostQuest(next)` in `setupjourneyhttp/quests.go` reads
`r.PathValue("questID")`, calls `ForHostQuest`, and delegates exactly like
`ScopeQuest`. The `questService` interface (`:19-23`) gains `ForHostQuest`.
Registered in `registerSetupJourneyRoutes`:

```text
GET  /api/host-setup-quests/{questID}                       GetRoot
GET  /api/host-setup-quests/{questID}/status                Status   (new handler)
GET  /api/host-setup-quests/{questID}/runs/{runID}          GetRun
POST /api/host-setup-quests/{questID}/open                  OpenRoot
POST /api/host-setup-quests/{questID}/dismiss               DismissRoot
POST /api/host-setup-quests/{questID}/runs/{runID}/open     OpenRun
POST /api/host-setup-quests/{questID}/runs/{runID}/dismiss  DismissRun
POST /api/host-setup-quests/{questID}/runs/{runID}/actions/{actionID}  Mutate
```

`children` and `preparation` are deliberately not registered:

- `CreateOrResumeChild` requires the root's summary to offer
  `connect_another_project` (`presentation.go:127`), which the account-link
  summary reader never offers (FR 10), so the route could only ever answer
  `action_unavailable`.
- `CheckPreparation` (`workspace_launch.go:161`) is the four-screen launch's
  group-preparation read and depends on `workspace_launch`, which the shape
  forbids (§2.3).

Bodies, `if_revision`, idempotency keys, review tokens, size limits, and
`forbiddenControlFields` (`handler.go:351-357`) are the existing ones; the
host family adds no body field. Golden files: add the host paths to
`internal/server/testdata/route_paths.txt` (`/api/host-setup-quests/x`,
`/status`, `/open`, `/dismiss`, `/runs/x`, `/runs/x/open`, `/runs/x/dismiss`,
`/runs/x/actions/x`, plus `/children` and `/runs/x/preparation` to pin them as
not registered) and regenerate `route_table.golden` with
`go test ./internal/server -run TestGoldenRouteTable -update-golden`. As with
`/api/setup-quests/x/x`, an unknown ID answers 404 and the golden records it as
`not-found`; that is the scoping wrapper working, not a missing route.

### 3.5 The non-creating status read (FR 19, FR 45)

`Service.Status(ctx, userID) (*JourneyProjection, bool, error)` on a
quest-scoped service:

1. `s.quest == nil` → `journey_unavailable`.
2. `questDeclaration` (catalog lookup only; writes nothing).
3. `s.store.FindQuestRoot(ctx, userID, key, "")`: `ErrNotFound` → `(nil,
   false, nil)`; `ErrConflict` → `journey_unavailable`.
4. Root found → return `s.Read(ctx, userID, "")` with `exists = true`.

Step 4 reaches `CreateOrGetRoot`, but with the root present the
`INSERT OR IGNORE` affects zero rows and the existing row is returned; the
reconcile that follows is the framework's ordinary read-time reconciliation
(the same thing `GET …/{questID}` does). No root is inserted by `Status`, and
for a user with no root the only statement executed is the `SELECT` in
`FindQuestRoot`. The write-count test in Group 5 pins both facts.

`Handler.Status` (`GET …/status`): `RequireMethod GET`, `noQuery`,
`currentUser`, then a capability check on the service
(`interface{ Status(context.Context, string) (*setupjourney.JourneyProjection, bool, error) }`,
the pattern `CheckPreparation` uses at `handler.go:115-121`), failing with
`owner_unavailable` when absent. Responses:

```json
{ "exists": false }
{ "exists": true, "setup_journey": { ...JourneyProjection } }
```

Home's card uses only this endpoint (FR 44–45).

### 3.6 No migration, no reset change

Columns consumed by a host run: `owner_user_id`, `relationship_id`,
`specialist_slug` (`host_quest`, 10 bytes ≤ 64), `journey_id`,
`declaration_*_version`, `state_revision`, `lifecycle_state`,
`current_step_id`, `step_states_json` (four steps, well under 8192 bytes),
`dismissed`, the three timestamps, and `project_workspace_id` as the workspace
receipt (FR 9; ≤ 128). `integration_plugin_id`, `integration_version`,
`home_workspace_id`, and `selected_mode_id` stay empty. No column, constraint,
or index changes; migration 54 (`migrations.go:2103-2240`) is sufficient.

Reset inspection (`internal/database/reset_inspection.go:30-32`) classifies
the four `setup_journey_*` tables by name, not by `specialist_slug`, so host
rows fall into the existing setup-progress category with no change. The
user-template binding tables are not used by host quests.

## 4. Receipts, projections, reason codes (task 1.4)

### 4.1 Receipts (FR 9, OQ6)

The only durable receipt the account-link shape writes is the Email Ops
workspace ID, in the run's existing `project_workspace_id` column via
`CanonicalResult.ProjectWorkspaceID`. `applyCanonicalRead` gains a
`SetupStepWorkspaceCreate` case identical to the `project_connect` branch's
project half (`service.go:628-630`): copy `ProjectWorkspaceID` when non-empty.
Because host runs are always roots, `scopeForRun` (`:606-609`) already feeds
`root.ProjectWorkspaceID` back into `ReadScope.ProjectWorkspaceID`, which the
`account_connect`, `account_link`, and `summary` readers use to find the
workspace without re-resolving it.

`validResultForKind` gains:

| Kind | Allowed non-empty fields | Everything else must be empty |
| --- | --- | --- |
| `workspace_create` | `ProjectWorkspaceID`, `OwnerRevisions` | `ChildRunID`, `IntegrationPluginID`, `IntegrationVersion`, `HomeWorkspaceID`, `SelectedModeID` |
| `account_connect` | `OwnerRevisions` | all IDs |
| `account_link` | `OwnerRevisions`, `CanonicalReceiptID` | all other IDs |

`normalizeCanonicalResult` (`types.go:513-541`) already applies
`validateCanonicalRef` to every ID, which rejects secret-like text
(`sensitive.ContainsSecretLikeText`) and caps length at 128 bytes; workspace
IDs pass. `account_link`'s commit result uses `CanonicalReceiptID` for the
binding ID (an opaque `mcpb_*`-style workspace binding ID, not a credential
reference) only if the linker exposes it; otherwise the result carries no ID
and `ConsequenceObserved` re-reads the evaluator (§5). No account ID, email
address, credential reference, vault ID, or token is ever placed in
`CanonicalResult`, an operation receipt, or a review receipt (FR 9, FR 52).

Owner revisions: `CanonicalOwner` gains no new value. `account_link` reports
`OwnerWorkspace` (binding revision) only; the connection is not a
`CanonicalOwner` and its state is bound into the review digest instead (§5).

### 4.2 Typed projections (FR 11)

All three are response-only: they live on `CanonicalStepRead`, are cloned into
`StepProjection` by `projectionFromRun` (`service.go:743-776`), and are never
serialized into a journey row (the same rule `IntegrationProjection`
documents). New file `internal/setupjourney/account_projection.go`.

```go
type WorkspaceCreateProjection struct {
    TemplateTitle  string `json:"template_title"`
    WorkspaceID    string `json:"workspace_id,omitempty"`
    WorkspaceLabel string `json:"workspace_label,omitempty"`
    WorkspaceRoute string `json:"workspace_route,omitempty"`
}
type AccountConnectProjection struct {
    Configured    bool   `json:"configured"`
    IdentityEmail string `json:"identity_email,omitempty"`
    GmailHealth   string `json:"gmail_health"`
    ActionLabel   string `json:"action_label,omitempty"`
    ActionURL     string `json:"action_url,omitempty"`
}
type AccountLinkProjection struct {
    WorkspaceLabel string `json:"workspace_label"`
    AccountEmail   string `json:"account_email,omitempty"`
    Linked         bool   `json:"linked"`
    Ready          bool   `json:"ready"`
}
```

Validators (same shape as `validWorkspaceSetupProjection`,
`workspace_setup_adapter.go:243-259`), applied inside `validCanonicalRead`
with the kind pin ("a projection may appear only on its own kind's read"):

- `validWorkspaceCreateProjection`: `TemplateTitle` non-blank, ≤ 120 bytes,
  no control characters; `WorkspaceID` empty or `validateCanonicalRef`;
  `WorkspaceLabel` ≤ 120 bytes, no control characters; `WorkspaceRoute` empty
  or a relative path starting with `/workspaces/` whose remainder is a stable
  ID (`^[a-z0-9][a-z0-9_-]{0,63}$`); `WorkspaceID`, `WorkspaceLabel`, and
  `WorkspaceRoute` are all set or all empty.
- `validAccountConnectProjection`: `GmailHealth` ∈ {`unconfigured`,
  `not_connected`, `not_enabled`, `unhealthy`, `vault_unavailable`,
  `healthy`}; `IdentityEmail` empty or a bounded address (≤ 254 bytes, exactly
  one `@`, no whitespace or control characters); `ActionLabel` ≤ 60 bytes;
  `ActionURL` empty or a relative `/settings…` path (no scheme, no `//`, no
  whitespace); `Configured == false` implies `GmailHealth == "unconfigured"`;
  `GmailHealth == "healthy"` implies `Configured` and `IdentityEmail != ""`.
- `validAccountLinkProjection`: `WorkspaceLabel` non-blank, ≤ 120 bytes;
  `AccountEmail` as above; `Ready` implies `Linked`; `Linked` implies
  `AccountEmail != ""`.

Clones are value copies (no slices). `CanonicalStepRead`, `StepProjection`,
and `ReviewProjection`/`ActionReviewMaterial` gain the three pointer fields
(`WorkspaceCreate`, `AccountConnect`, `AccountLink`); `ReaderRegistry.read`
(`actions.go:262-266`) clones them like the existing four.

The email address appears in responses only, on `account_connect` (identity)
and `account_link` (linked account), mirroring what
`GET /api/workspaces/{id}/email/status` already returns
(`EmailReadiness.EmailAddress`). It is never digested into a receipt; the
review's owner digest binds the connection **subject**, not the address
(§5.3).

### 4.3 Reason codes and guidance (FR 8)

New `ReasonCode`s, added to `validReasonCodes` and `safeGuidance`:

| ReasonCode | Guidance (compiled, no path/address/vault/account) |
| --- | --- |
| `workspace_required` | "Create the Email Ops workspace to continue." |
| `account_connection_not_configured` | "Google sign-in isn't configured on this Ori server yet. Ask whoever runs it to set up the Google connection, then check again." |
| `account_connection_required` | "Connect your Google account in Settings, then choose Check again." |
| `account_capability_not_enabled` | "Enable Gmail on your connected Google account, then choose Check again." |
| `account_reconnect_required` | "Reconnect Gmail in Settings, then choose Check again." |
| `account_vault_repair_required` | "Repair the vault that holds your email credentials in Settings, then choose Check again." |
| `mailbox_link_required` | "Review and confirm linking the connected account to this workspace." |
| `mailbox_account_unavailable` | "The linked email account is no longer available. Reconnect it in Settings, then check again." |

Mapping table (`emailOpsQuestReason` in `internal/server`, with a test that
enumerates every `workspace.BlockedReason*` × action pair the evaluator can
emit, from `email_readiness.go:81-219`):

| Evaluator reason | Evaluator action | Step | Journey reason |
| --- | --- | --- | --- |
| (no OAuth client: `connections.ResolveOAuthClientChecked` source `none`, or verdict invalid) | — | `account_connect` | `account_connection_not_configured` |
| `connection_required` | `connect_google` | `account_connect` | `account_connection_required` |
| `capability_not_enabled` | `enable_gmail` | `account_connect` | `account_capability_not_enabled` |
| `reconnect_required` | `reconnect_gmail` (connection-level, `:185-193`) | `account_connect` | `account_reconnect_required` |
| `vault_repair_required` | `repair_vault` | `account_connect` | `account_vault_repair_required` |
| `not_linked_to_workspace` | `link_account` | `account_link` | pending, not blocked: actions `review_mailbox_link` |
| `account_unavailable` | `link_account` (`:113-133`) | `account_link` | `mailbox_account_unavailable` |
| `reconnect_required` | `reconnect_gmail` (binding-level, `:134-144`) | `account_link` | `account_reconnect_required` |
| workspace missing (resolver finds none) | — | `workspace_create` | pending, not blocked: action `review_team` |
| workspace deleted after receipt | — | `workspace_create` | `workspace_required` |

The not-configured check runs first in the `account_connect` reader because
`evaluateConnection` reports a missing OAuth client as plain
`connection_required` (`:165-173`): the connection file has no verified
identity either way, and the distinction the PRD wants (FR 29) exists only in
`ResolveOAuthClientChecked` (`connections/oauth_client.go:56-66`). The
projection's `Configured` and `GmailHealth: "unconfigured"` carry the same
fact to the UI.

`account_connect` is **blocked** (not pending) in every unmet case because the
quest cannot complete it; the two navigation actions stay available on a
blocked step (`projectionFromRun` publishes actions for the current step
regardless of status, `service.go:766-768`). `account_link` is **pending**
with `review_mailbox_link` when the only gap is the link itself, which is the
one case the quest can resolve.

### 4.4 Response-only versus persisted

| Value | Where it lives | Persisted? |
| --- | --- | --- |
| Email Ops workspace ID | `Run.ProjectWorkspaceID`, `ResourceProjection.ProjectWorkspaceID` | yes (existing column) |
| Step status and reason code | `Run.StepStates` | yes (existing JSON column) |
| `WorkspaceCreateProjection`, `AccountConnectProjection`, `AccountLinkProjection` | `CanonicalStepRead` → `StepProjection` | no |
| `ReviewProjection.AccountLink` | action response | no (only its digest is stored, as today) |
| Identity/account email, Gmail health, action label/URL | projections above | no |
| Binding ID | `CanonicalResult.CanonicalReceiptID` (only if the linker returns it) | operation receipt only; not a credential |
| Credential ref, vault ID, tokens, subject | never enters `setupjourney` | no |

## 5. Link path trace (task 1.5)

### 5.1 Readiness verdict order (FR 27, FR 30)

`emailReadinessEvaluator.Evaluate(ctx, workspaceID)` (`email_readiness.go:81-147`):

1. `evaluateConnection` (`:151-219`), workspace-independent, first unmet wins:
   connection file loads → `HasVerifiedIdentity()` (subject present) → Gmail
   grant exists and not `HealthNotEnabled` → grant `HealthHealthy` → vault
   preflight `VaultOutcomeReady`. Reasons: `connection_required`,
   `capability_not_enabled`, `reconnect_required`, `vault_repair_required`.
2. Workspace loads and `emailBindingFor(ws)` finds an enabled native-email
   binding naming an account with read access → else
   `not_linked_to_workspace` / `link_account`.
3. The bound account resolves in the vault and holds an access or refresh
   token → else `account_unavailable` / `link_account`, or
   `reconnect_required` / `reconnect_gmail` (binding-level, `:134-144`).
4. `Ready` with `AccountID` and `EmailAddress`.

`evaluateConnection` returns `nil` when no connection store is wired, so
readers must treat a nil `b.connStore` as `owner_unavailable` rather than
"connected". A missing OAuth client is indistinguishable from "not connected"
here; §4.3 resolves that with `connections.ResolveOAuthClientChecked` first.

The `account_connect` reader calls `evaluateConnection` directly (same
package). The `account_link` reader calls `Evaluate(ws.ID)`; because the
framework only reads step 3 after step 2 completes, a connection-level verdict
there means the connection regressed between reads and maps to the same
`account_*` reason codes.

### 5.2 Commit sequence for `link_mailbox` (FR 32)

Inputs come from the server, never the body (FR 49):

| Input | Source |
| --- | --- |
| workspace ID | `ReadScope.ProjectWorkspaceID` (the step-1 receipt), re-validated by `ResolveEmailOpsWorkspace` against the folder store before commit |
| user ID | `ReadScope.OwnerUserID` |
| `credentialRef`, `vaultID` | `conn := b.connStore.Load()`; `g, ok := conn.Grant(connections.ProductGmail)`; `g.CredentialRef`, `conn.VaultID` (exactly what `connectionshttp.gmailLink` passes, `handler.go:315-325`) |

Steps, in order:

1. `accountID, err := b.gmailSink.LinkGmailToWorkspace(ctx, g.CredentialRef, conn.VaultID, workspaceID)`
   (`connection_gmail_sink.go:107-125`). It verifies the vault record resolves
   and returns its ID; it creates nothing and requests no scope, so it is
   idempotent by construction. `connectionshttp.ErrCredentialMissing` (vault
   record gone) fails the operation with journey reason
   `account_reconnect_required`; any other error → `operation_failed`.
2. `status, err := linker.LinkWorkspaceMailbox(ctx, userID, workspaceID, accountID)`
   (`personalhq_email.go:237-243` → `linkMailboxToWorkspace`, `:134-169`):
   owner check (empty owner allowed), account must exist and be unscoped or
   scoped to this workspace, then upsert the binding
   `{ID: existing-or-new, ServerName: "gmail", RuntimeKind: native_email,
   Enabled: true, Config: {account_id, allowed_actions: ["read","search"]}}`
   and `Save(ws)`. The existing binding ID is reused, which is what makes the
   concurrent CTA/wizard link converge on one binding (FR 34). Send is never
   in `allowed_actions`.
3. `b.setupWizardService.Confirm(ctx, workspaceID, "mailbox", setupwizard.StepAction{Type: setupwizard.ActionConfirm})`
   (`setupwizard/service.go:243-345`; note the signature takes the workspace
   **ID**, not the workspace). The email adapter's `Evaluate` now reports
   `Ready`, so `alreadySatisfied` short-circuits `adapter.Confirm` and
   `refresh` records the step as acknowledged. This call is bookkeeping for
   the wizard's own progress record: a failure here (for example the adapter
   is not registered because `b.emailReadiness` was nil, `email_setup_adapter.go:103-110`)
   is logged and does **not** fail the commit, because the binding is the
   consequence and the wizard re-derives the step from the same evaluator on
   its next evaluation. The parity test asserts the wizard reports the mailbox
   step `Ready` after the quest commit either way.
4. Return an empty `CanonicalResult` with result code `applied`. The result
   carries no account ID and no binding ID: the linker does not return the
   binding ID, and the account ID is the credential reference, which FR 52
   keeps out of journey tables. A replay with the same idempotency key returns
   the stored receipt; an interrupted claim that `ConsequenceObserved` settles
   finalizes as `already_current`.

`InputDigest(link_mailbox, raw)` requires the empty object (the
`decodeEmptyActionInput` helper used by the workspace-setup adapter,
`workspace_setup_adapter.go:45`) and returns `Digest("account_link:link_mailbox:v1")`.
`PrepareCommit` repeats `Review`'s reads and returns the same material so the
service's digest comparison detects drift.

### 5.3 Review material (FR 31)

`Review(ctx, scope, review_mailbox_link, raw)` reads: the Email Ops workspace
(label from the folder store), `conn` and its Gmail grant, and the current
binding. It returns `ActionReviewMaterial{CommitAction: link_mailbox,
InputDigest, OwnerRevisionDigest, DisclosureDigest, AccountLink:
&AccountLinkProjection{WorkspaceLabel, AccountEmail: conn.Email, Linked,
Ready}}`. The response `ReviewProjection.AccountLink` also carries the
disclosure copy through the step description; the projection itself has no
free-text field.

- `OwnerRevisionDigest = Digest("account_link_owner:v1|" + conn.Subject + "|" + string(grant.Health) + "|" + grant.CredentialRef + "|" + workspaceID + "|" + bindingRevision)`,
  where `bindingRevision` is `Digest(binding.ID + "|" + account_id + "|" + joined allowed_actions + "|" + enabled)` or `"none"`. A changed account
  (subject), a grant that turned unhealthy, a swapped credential, a different
  workspace, or a binding written by the CTA between review and commit all
  change the digest, and the service reports `review_stale`. Only the SHA-256
  is stored (the existing `setup_journey_review_receipt.owner_revision_digest`
  column); the credential reference itself never enters a row.
- `DisclosureDigest = Digest("account_link_disclosure:v1|" + workspaceLabel + "|" + conn.Email + "|" + disclosureCopy)`,
  binding the exact text the user consented to.

### 5.4 `ConsequenceObserved(link_mailbox, read)` (FR 33)

True when the settled read is `Complete` (evaluator `Ready`) **and** the
workspace's binding `account_id` equals the connection's Gmail grant
`CredentialRef`. The reader carries that comparison in
`AccountLinkProjection.Linked` only for the binding's presence; the adapter
recomputes the equality from the same read inputs so a legacy per-workspace
account that happens to be Ready does not settle an interrupted quest commit
as `already_current`.

The reconciler's recovery predicate (`service.go:438`,
`recoverable := kind == project_connect || action == select_file_only_mode`)
is widened to include `ActionLinkMailbox`. The comment there warns against
generalizing to staffing or plugin actions because their consequences are
ambiguous; the mailbox link is the file-only case's twin: one idempotent
upsert whose presence is directly observable. The widening is one clause with
a test that an interrupted `link_mailbox` claim reconciles to
`already_current` without a second link call.

### 5.5 Dependencies and wiring point (D1 confirmed)

| Dependency | Builder field | Exists at Phase 22.6? | Nil case |
| --- | --- | --- | --- |
| readiness evaluator | `b.emailReadiness` | yes (`wireMailboxRuntime`, `:1171`) | nil when workspace store or vault store is nil → account readers `owner_unavailable` |
| connection store | `b.connStore` | yes (Phase 17) | nil → `account_connect` `owner_unavailable` |
| vault store (account resolver) | `b.vaultStore` | yes (Phase 17, `:323`) | nil → same |
| Gmail credential sink | `b.gmailSink` | yes (Phase 17, `:369`) | nil → `link_mailbox` adapter not registered; step 3 offers no commit |
| mailbox linker | **not retained today** (`linker` is local, `:1178`, and built only when the HQ handler and service exist) | — | Group 3 stashes it as `b.mailboxLinker`, constructing it before the HQ `if` since its workspace-scoped methods tolerate a nil HQ service (`ownedWorkspace`, `:71-88`) |
| setup wizard service | `b.setupWizardService` | yes (`wireSetupWizard`, Phase 18) | nil → skip the confirm (non-fatal) |
| provenance-hydrated workspace source | `b.workspaceFileStore` | yes (Phase 18) | nil → `workspace_create` `owner_unavailable` |
| model availability | `b.configManager`, `b.llmFactory` | yes (Phases 4, 5) | nil → summary offers `open_model_settings` |

Decision: direct capture at `initializeSetupJourney` time in a
`newEmailOpsQuestAdapters(deps)` constructor in
`internal/server/email_ops_quest_adapters.go`, with every reader returning
`owner_unavailable` when its dependency is nil, and `SetActionAdapter(
SetupStepAccountLink, …)` called only when the sink, linker, connection store,
and evaluator are all present. No lazy closures. The one builder change
outside the setup-journey files is the `b.mailboxLinker` stash in
`wireMailboxRuntime`.

## 6. FR 26 evidence and creator context keys (task 1.6)

### 6.1 Provider-less creation, verified live (2026-09-14)

Environment: branch build (`make build` at `48392740` + planning docs),
launched by hand with the smoke recipe (`HOME` and `ORI_DATA_DIR` pointed at
`$TMPDIR/smoke-455-<pid>`, started from inside that directory, port 8957; no
`OPENAI_API_KEY`/`ANTHROPIC_API_KEY`, no Google client). Two sandbox facts
worth keeping: the seatbelt denies the listen bind and the server reports it
as "Port is in use by another process | processes=unknown", and it also
filters `curl` to `localhost`, so the server and its probes ran unsandboxed.
Port 8931 (the `wt demo` default) was genuinely held by another session's
server at the time.

| Step | Request | Observed |
| --- | --- | --- |
| Plan | `POST /api/workspaces/template-agent-plan` `{"template_id":"email-ops"}` | `system_model_configured: false`; agents `Postmaster` (entry, `create`) and `Inbox` (`create`); `revision` present |
| Create | `POST /api/workspaces` `{"name":"Email Ops","template_id":"email-ops","create_template_agents":true,"template_agent_review":{"version":1,"plan_revision":<revision>,"expectations":[{"index":0,"name":"Postmaster","action":"create"},{"index":1,"name":"Inbox","action":"create"}]}}` | 200; `folder.folder_slug: "email-ops"`; `agent_instances` = Postmaster (entry) + Inbox |
| Allowlist | `workspace_allowlist.json` at the data root | contains the new workspace ID |
| Provenance | `workspace-staging/email-ops/workspace.json` | `template_provenance.template_id: "email-ops"`, `builtin: true` |
| Restart | kill by PID, relaunch from the same sandbox | `GET /api/workspaces/{id}` still lists Postmaster and Inbox |
| Readiness | `GET /api/workspaces/{id}/email/status` | `setup.ready: false`, `reason: connection_required`, `action: connect_google`, `action_url: /settings#google-account` |
| Catalog baseline | `GET /api/setup-quests` | one entry: the REAPER `host_compatibility` quest; nothing for Email Ops yet |

FR 26 holds for creation. Finishing the quest to `ready` without a model is
still to be shown in Group 3's demo (it depends only on the evaluator).

The workspace creation payload the guided creator already sends is exactly
the one above (`sessions.js:9348-9358`), so the quest needs no new request
field; the review's `plan_revision` comes from the creator's own plan fetch
(`:4468-4478`).

### 6.2 Creator context keys (for Group 2)

`showAddWorkspaceModal(options)` (`sessions.js:10830`) → `beginWorkspaceCreatorContext`
(`:10795-10827`) → `WorkspaceCreatorState.createCreatorContext`
(`workspace-creator-state.js:47-72`). Keys the context carries today:

| Key | Type | Meaning |
| --- | --- | --- |
| `mode` | `ordinary` \| `import` \| `guided` \| `specialist` \| `selected-members` | derived by `modeFor`; `guided` is the group-preparation dialog (D2) |
| `kind`, `fixedKind` | `workspace` \| `group` | fixed kind disables the toggle |
| `entryPoint` | string | analytics/behaviour tag; defaults `workspace_hub_create` |
| `blueprint` | string | preselected blueprint card (`preselectBlueprintCard`, `:764-766`) |
| `postCreateAction` | string | e.g. `designate_personal_hq` |
| `mapOrigin` | bool | return to map instead of navigating |
| `selection` | `{ids, names}` | selected-members grouping |
| `invoker` | Element | focus return |
| `onCreated` | function | fired only by `finishOrdinaryGroupCreate` today (D3) |
| `guided` | object | group preparation state (D2) |
| `drafts`, `review`, `submitting`, `knownCreatedGroup` | internal | wizard drafts |

Decisions for FR 24–25:

- The quest opens the creator with
  `showAddWorkspaceModal({ blueprint: 'email-ops', entryPoint: 'host_setup_quest', teamLock: { agentNames: ['Inbox'], reason: 'Mail access is granted to the agent named Inbox.' }, onCreated })`
  and then `goToWizardStep(2)` (Team) once the blueprint card is preselected.
  `teamLock` is a new, single-purpose option threaded through both context
  builders; `guided` stays untouched.
- `sessions.js` gains two bounded changes on the workspace-create success path
  (`:9897-9959`): invoke `creatorContext.onCreated({ folder, workspaceId })`
  when present, and when `entryPoint === 'host_setup_quest'` skip the
  `/workspaces/<slug>` navigation and instead refresh surfaces the way the
  map-origin branch does. Everything else on that path (seed notes, hire,
  post-create action, toasts) is unchanged.
- The Team step's rename control is disabled, with the lock reason shown
  inline, only for names listed in `teamLock.agentNames`; other agents and all
  other customization stay editable. The exact control is identified in 2.8
  while editing `create-workspace-team-draft.js`.

## 7. Open-question resolutions, acceptance matrix, file ownership (task 1.7)

### 7.1 PRD §9 open questions

| OQ | Resolution | Evidence |
| --- | --- | --- |
| OQ1 kind names | `workspace_create`, `account_connect`, `account_link` (§2.1). The `account_*` names are generic so a later Calendar Ops quest can reuse them; the workspace-wizard enum already uses `account_link` for its own step kind (D7), which is fine across packages. | `specialist.SetupStepKind` is the only enum this feature extends |
| OQ2 triage route | `start_inbox_triage` navigates to `/workspaces/<slug>?panel=tasks`. | `workspace-url-state.js:42,95` reads `panel`; `routes_test.go:339` canonicalizes `…?panel=tasks` |
| OQ3 model settings | `open_model_settings` navigates to `/settings#system-model`. | the navbar's System Model indicator links there (`navbar.tmpl:79`); `settings.tmpl:511-533` hosts the provider/model selects |
| OQ4 `builtin_version` bump | Bumping `email-ops` from 4 to 5 refreshes only the library copy's `template.json` (and shipped dashboard) on the next start; skeleton files and user edits are untouched, existing workspaces keep the provenance `Version` recorded at creation, and no upgrade prompt exists anywhere in `workspace`/`sessionhttp`. Safe. | `starter.go:60-114` (`EnsureLibrary` → `refreshBuiltinManifest`), `template_provenance.go:45` |
| OQ5 dismiss unification | One persisted dismiss. The modal's "Do this later" posts `…/dismiss` with `mutationBody()` (`if_revision`, `idempotency_key`), and the Home card's "Not now" posts the same body to `POST /api/host-setup-quests/email_ops_setup/dismiss` using the revision from the status read. Reopening from any entry point clears it through the existing `Open` semantics. | `setup-journey.js:1616-1640` |
| OQ6 receipt column | `project_workspace_id` is the target-workspace receipt for v1; no migration (§3.6, §4.1). Revisit only if a third shape needs two workspaces. | `migrations.go:2124` |

### 7.2 Contracts that must not be weakened (stop conditions checked)

- **Plugin and user quests stay five-step.** §2.5 adds an explicit specialist-shape check to both validators; the `WorkspaceLaunch != nil` rule they already apply would reject the new shape anyway. No loosening is required by any part of this design.
- **Reviewed-integration trust path.** The host catalog never consults `reviewedintegration.All()`, `plugin.Manager`, or an installed plugin; `installedQuestCatalog` is untouched; `FindQuestRoot`'s plugin branch becomes explicit rather than broader (§3.2).
- **Reads never mutate.** `Status`, `Read`, `Overview`, and the catalog list execute no link, unlink, OAuth start, wizard confirm, or workspace write. The `workspace_create` reader resolves through the folder store and never creates a workspace; creation happens only through `POST /api/workspaces` from the creator (§6). The only writes on a read path are the framework's own root create-or-get on the first authorized quest read (FR 39) and its read-time reconciliation, both pre-existing.

No stop condition is triggered; implementation may proceed.

### 7.3 Acceptance matrix (verbatim from the task list; evidence column is the plan)

| Outcome | Required observable evidence |
| --- | --- |
| Contract widened safely | `NormalizeSetupJourney` accepts both shapes; every pre-existing fixture (REAPER compatibility JSON, plugin/user quests, `specialist_test.go`) normalizes to the same bytes; plugin and user validators reject the `account_link` shape; the JSON schema file is unchanged. |
| Quest is discoverable and inert until opened | `/api/setup-quests` lists `email_ops_setup` with `source: host`; Templates and the picker show "Open Guided Setup" for the built-in Email Ops card; listing writes no journey rows. |
| Step 1 reuses the creator | `review_team` opens the shared modal on Team with Email Ops preselected; Inbox rename is disabled with a reason; creating goes through `POST /api/workspaces` and the workspace appears in `workspace_allowlist.json`; the journey re-reads and completes step 1 with the workspace receipt. |
| Step 2 is navigation-only | No OAuth request originates from the quest; Settings opens in a new tab; "Check again" re-reads; a server with no OAuth client shows the not-configured copy; blocked reasons map from the evaluator table. |
| Step 3 is an explicit link | Review shows workspace, account email, and disclosure; commit runs sink → linker → wizard confirm; one binding with `read`/`search`; replay returns `already_current`; stale review → `review_stale`; the workspace's wizard shows its mailbox step passed. |
| Ready means ready | Parity test: quest `ready` ⇔ `GET /api/workspaces/{id}/email/status` `setup.ready` for every evaluator verdict; regression flips to `needs_attention` with the right repair. |
| No model needed | Provider-less `wt demo` sandbox reaches `ready`; zero requests to model endpoints counted in the browser spec; summary hides `start_inbox_triage` and shows the model note. |
| Quiet Home card | Home for a never-started user issues zero journey writes and renders no card; an in-progress quest renders one card; "Not now" persists dismiss and hides it; reopening from Templates clears dismiss; a completed quest never shows the card again. |
| Entry points are user-triggered | No auto-open on Home load, hire, onboarding, or app detection; capability card routes to the quest only when no Email Ops workspace exists. |
| Isolation and secrets | Cross-user run IDs and foreign review tokens fail closed; `setup_journey_*` rows contain no token, credential ref, vault ID, or address after the golden path. |
| Existing journeys untouched | Specialist, plugin, and user quest Go suites and browser specs pass without behavioural edits; specialist summary actions are byte-identical. |
| CI budget | `internal/sessionhttp` test count unchanged; Linux `-race` leg time not materially longer. |

### 7.4 File ownership

One writer (the integration owner) for everything; the split below is about
which group touches what, not about parallelism.

| Area | Files | Group |
| --- | --- | --- |
| Shape contract | `internal/specialist/setup_journey.go`, `setup_journey_test.go`, `setup_journey_fuzz_test.go` | 2 |
| FR 4 guards | `internal/plugin/setup_quest.go` (+test), `internal/projecttemplates/user_setup_quest.go` (+test), schema-unchanged test | 2 |
| Journey framework | `internal/setupjourney/actions.go`, `service.go`, `types.go`, `account_projection.go` (new), `quests.go`, `quest_store.go`, `host_quest_catalog.go` (new), `presentation.go`, `events.go` + tests | 2 (readers/registry/source), 3 (link event, recovery predicate) |
| Host data | `internal/hostquests/hostquests.go`, `email-ops-setup.json`, `hostquests_test.go` (new) | 2 |
| Template cross-reference | `internal/projecttemplates/starter/email-ops/template.json`, `templates.go` (comment), `host_setup_quest_reference_test.go` (new, external test package) | 2 |
| HTTP | `internal/setupjourneyhttp/quests.go`, `handler.go` (`Status`), `quests_test.go`; `internal/server/routes.go`, `testdata/route_paths.txt`, `testdata/route_table.golden` | 2 |
| Server readers/adapters | `internal/server/email_ops_quest_adapters.go` (+test, new), `builder_setup_journey.go` (registration, shape-aware summary), `builder_handlers.go` (`b.mailboxLinker` stash), `builder.go` (field), `email_ops_quest_parity_test.go` (new), `setup_summary_test.go` | 2 (workspace_create + summary + stubs), 3 (account readers, link adapter, parity) |
| Events | `internal/specialistevents/events.go` (`AccountLinkOutcome`) | 3 |
| Capability card | `internal/personalassistant/capabilities.go` (+test), `internal/server/personal_assistant_capabilities.go` | 4 |
| Web: links and journey | `setup-quest-links.js` (+test), `setup-journey.js` (+test), `setup-journey-account-steps.js` (+test, new), `setup-journey.css` | 2 (step 1, links, dispatch), 3 (steps 2–3, summary) |
| Web: creator | `sessions.js` (+test), `workspace-creator-state.js`, `create-workspace-team-draft.js` (+test) | 2 |
| Web: templates | `templates-page.js`, `project-templates-manage.js`, their tests | 2 |
| Web: Home card | `email-setup-quest-card.js` (+test, new), `dashboard.tmpl`, `base.tmpl`, `dashboard.css`, `templates_test.go` | 4 |
| Browser spec | `tests/email-setup-quest.spec.ts` (new) | 5 |
| Docs | this file; `docs/plugin-setup-quests.md`; `docs/architecture/specialist-setup-journey-contract.md`; `tasks/test-guide-455-goal-first-gmail-setup-quest.md` | 1 (this), 5 (rest) |

Never touched: `internal/sessionhttp/*_test.go` (CI timeout cliff),
`internal/plugin/schema/setup-quest-v1.schema.json`,
`internal/specialist/compatibility/reaper-setup.json`, the REAPER journey
renderers in `setup-journey.js`, and `internal/reviewedintegration`.
