# Settings reset: scope and lifecycle findings

Status: active implementation branch for `settings-reset-ux`. Replay Setup,
Reset Getting Started, and Reset Selected Data now use distinct verified paths.
Selective destructive reset is wired to the production process lease, shared
admission gate, bounded drain, staged journal, full-process relaunch, pre-store
application, startup suppression policy, durable status, and a minimal recovery
host. The legacy live-deletion handler is removed. **Start Fresh is now a
separate server-owned intent** with an enumerated installation-local owner
inventory, first-run verification, bounded browser cleanup, and an explicit
attach-existing-vault-package path. Selecting every narrow category never
implies Start Fresh.

> The numbered “in progress” sections below are retained implementation notes.
> Their milestone-specific delivery limits are superseded by the current status
> above and the final validation boundary at the end of this document.

## Evidence and isolation

The approved contract is four distinct intents: Replay Setup, Reset Getting
Started, Reset Selected Data, and Start Fresh. Selecting all categories does not
mean Start Fresh. Retained workspace/project and vault bytes must survive,
including nested/custom locations. A registration is not its backing folder.

The reusable fixture is `internal/testutil/resetfixture` ([usage and limits](../../internal/testutil/resetfixture/README.md)).
It owns fresh data/CWD/HOME/roots, memory secrets, retained sentinels, a loopback
listener, and deterministic background writers. It does not run the complete
production builder or authenticate externally. Reopen the same fixture paths to
verify persistence. Native Keychain, menubar UI and real-account behavior have
**not** been exercised.

Characterized so far:

- Real config/onboarding/agent/workspace/SQLite seeds reopen at the same paths.
- A paused real `config.Manager.Save` recreates deleted `settings.json` when
  released. Deleting the file does not invalidate the manager.
- Historical settings-only reset rejected missing confirmation but deleted a
  live fixture file. The real HTTP fixture now asserts `409 preview_required`
  and preservation. Original confinement cases now exercise the real planner;
  there is no test-only copy of the old deletion implementation.
- Fixture writes reject traversal, absolute paths and escaping symlinks; HTTP
  cannot attach to an inherited browser URL, proxy, redirect destination, or
  reuse its listener after close.
- Real `Server.Shutdown` leaves the session DB writable; a paused cached flush
  still succeeds after shutdown. Closing the actual HybridStore flushes cached
  metadata and makes the handle unusable; same-path reopening sees that flush.
- Real menubar `StartServer` / `runServer` / `StopServer` now execute twice in
  the same process and directory with real SQLite/cache and owned loopback HTTP.
  Both old DB handles remain open after stop until explicit test cleanup.
  Only runtime construction is injected through a package-private factory;
  production now uses `server.NewWithResetLease` + `srv.HTTPServer`. Native credential
  discovery, the broad builder and native menubar UI are not exercised.
- EventBus shutdown returns while an already-dispatched callback is paused;
  releasing it afterward still writes. Server shutdown leaves the actual usage
  writer able to persist a new record; explicit CostTracker.Close joins it.
- Emptying the global agent tree twice re-adopts legacy CWD profiles twice.
  Rebuilding a local workspace allowlist re-enables retained agent snapshots.
  A configured operator workspace root bypasses first-run staging consent.
- The real progression builder re-completes first day after an engine reset
  when the old relationship says its first assignment completed. Old app-state
  names also seed a fresh database profile.
- Clearing the MCP registry re-imports synthetic Codex config on every enabled
  startup; the existing explicit import-disable switch prevents it. Imported
  commands stay stopped, and the external config stays byte-identical.
- Initial characterization showed that normal Open migrated a seeded v11 schema
  and dropped DB-only vault content before returning. The 2.4 startup guard now
  refuses that Open without changing database/WAL bytes (see below). Detaching a
  current vault catalog row preserves its package byte-for-byte; a fresh store
  does not auto-attach it. Explicit test-only catalog reinsertion permits
  decryption with the generated password. This is not a shipped attachment API.
- A fake native runner proves the same relative settings filename addresses the
  same native account across two CWDs, and advertised availability can coexist
  with a locked read. No real native command or credential is used.
- Normal and race-enabled fixture/lifecycle checks passed on macOS/arm64. Windows symlink
  creation is explicitly skipped without privileges; no native Windows claim.

Commands:

```sh
go test ./internal/testutil/resetfixture -count=1
go test ./internal/settingshttp -run ResetFixture -count=1
go test -race ./internal/testutil/resetfixture ./internal/settingshttp -run 'Fixture|Writer' -count=1
go test -race ./internal/server ./internal/menubar ./internal/database ./internal/vault -run ResetLifecycle -count=1
go test ./internal/settingsreset -count=1
golangci-lint run --new-from-merge-base=origin/dev ./internal/testutil/resetfixture/... ./internal/settingshttp/...
gosec ./internal/testutil/resetfixture/...
```

## Location vocabulary and ownership rules

- **D**: activated `config.DefaultDataDir()`; default is per-user application
  support on macOS or `~/.ori-agent` elsewhere. `ORI_DATA_DIR` takes precedence.
- **C**: process CWD. `cmd/server.ensureDataDirectory` activates D before building
  stores; package/alternate-host callers must not be assumed to have C = D.
- **A**: authoritative agent index path; `AGENT_STORE_PATH` overrides
  `D/agents.json`. Individual agents live alongside it under `agents/` (including
  the store's supported index-inside-agents layout).
- **W**: folder store's actual root. Configured `workspace_root`, approved
  conventional `~/Ori Workspaces`, or `WORKSPACE_DIR` can apply. A fresh,
  unconfirmed install uses `D/workspace-staging` instead of adopting user files.
- **V**: vault catalog's resolved backing files, not just the configured root
  for *new* vaults. Managed default is `D/vaults`; individual custom paths and
  legacy layouts can coexist.
- **T**: project templates root from settings, `ORI_TEMPLATES_DIR`, or
  `D/templates`. Legacy workflow templates use `WORKFLOW_TEMPLATES_DIR` or
  `C/workflow_templates` independently.

Resolve targets from active owners **once** for preview/confirmation, not from
browser paths or reconstructed defaults. Resolve missing suffixes through their
existing parents, preserve the existing separator-aware confinement checks,
and reject symlink escapes. An overlapping retained root/file either splits the
explicit removal plan safely or blocks it. Never recursively remove D, W or V.
Unknown files are preserved and disclosed; an unclassified *active owner* blocks
completion rather than being silently omitted. Inaccessible counts are unknown,
not zero. New records may arrive after a count; material target/dependency
changes invalidate confirmation.

## Persistence scope matrix

Policies below describe the required target, not today's four-checkbox behavior.
**S** = selected Settings/API keys; **A** = selected Agents; **R** = selected
conversation/app records (legacy `sessions` request); **O** = legacy selected
onboarding/replay. **F** = Start Fresh. Other narrow intents preserve each row
unless stated. “Remove” always means through the safely stopped owner/apply
boundary, never while a handle or writer is live.

| Category / location / owner | Cache or writer | Reset policy and retained dependency | Preview facts | Verification |
| --- | --- | --- | --- | --- |
| Global config `C/settings.json` — `config.Manager`, builder configuration | Mutex-protected cached settings; settings/mac-wake/root callbacks save | S/F reset config and saved provider/search keys. Retain W/V files and external credentials. S must disclose root changes and dependencies, not adopt folders accidentally | File present/readable; root settings; externally sourced credentials as booleans, never values | Fresh manager reads canonical settings; later save/restart cannot restore previous config |
| Supplemental config `C/model_categories.json`, `C/locations.json` — model category store, location manager | Cached categories/zones; location loop | F removes app-owned configuration; narrow S preserves unless explicitly included by reviewed category definitions | Category/zone counts or unavailable; actual paths | Reopened owners have defaults/no old zones or overrides |
| App state `C/app_state.json` — onboarding manager | Cached app state; profile mirroring, event-driven progression saves | O/Replay reset only `Onboarding`. Getting Started uses engine reset only. F clears names, inferred profile, preferences, assistant progress and quests as well | Setup completion, quest counts, identity/profile present (avoid unnecessary personal content) | Owner reads canonical defaults and DB profile agrees after relaunch |
| Provider/search secrets — attached `vault.SecretStore` plus legacy plaintext settings | Config secret overlay and provider/client caches | S/F delete exact keys `openai_api_key`, `anthropic_api_key`, `gemini_api_key`, `brave_api_key`. **Preserve `vault_dek` and other namespaces**. Locked/unavailable/ambiguous ownership blocks or yields explicit partial outcome | Backend kind, key presence if safely knowable, unknown otherwise; external env/CLI availability | Exact-key absence in each confirmed backend; unrelated namespace/key equality; rebuilt providers |
| Secret backends — macOS service `ori-agent`, account `<namespace>:<key>`; Linux service/namespace/account attributes; `D/vault_secrets.json` passphrase fallback | Native command runner or cached fallback map | Scope by runtime namespace/backend, never blanket service deletion or whole fallback-file removal. F must classify inactive legacy copies too; ambiguity blocks. Windows native backend currently unsupported | Backend read/write/lock status, no key values | Fake runner exact calls and failure states; fallback reopened with retained encryption material |
| Global agents — A, sibling `agents/<name>/agent_settings.json`, `skills_state.json`, agent/repo skills | `store.fileStore` map; atomic saves; workspace snapshot restoration | A/F remove owned profiles, agent-owned skills/state and compatibility index. Preserve personal/external skills and retained workspace snapshots; do not restore them without intent | Agent count, actual index/agent tree; related history and snapshots preserved | Fresh store has no previous profiles; later save/relaunch cannot hydrate detached snapshots |
| Legacy global agents — `C/agents/` (nested settings or flat JSON), CWD `agents.json` projection | `migrateLegacyAgentStore` copies CWD agents when destination empty; `writeAgentsJSON` writes CWD | F classifies both active and discovered legacy targets; block ownership ambiguity. A must include or suppress restoration from reviewed legacy sources | Legacy copy exists; migration/import warning | Emptying destination cannot cause legacy re-adoption |
| Shared SQLite — actual `session.HybridStore.DB().Path()`, usually `D/sessions.db` plus WAL/SHM | LRU session cache, periodic flush/cleanup, many domain writers | R/F removes confirmed app records, not “chat database only.” Catalog removal detaches vaults; legacy DB-only vault content is a blocker. No unlink with live DB handles | Domain counts, collateral identity/HQ/notes/plans/jobs; unknown domains block | Reopen same DB path, verify domain postconditions and retained-vault reopen |
| Workspace registrations/index — shared DB workspace rows; `W/index.db` — composed SyncStore, folder FileStore | FileStore metadata/index handle; bidirectional reconciliation, AgentSnapshotStore | R/F detach app registrations, do not delete W or mutate retained folder contents. W index is colocated cache: preserve with W while detached, do not treat it as authoritative consent | Registered and retained folder counts/paths; cached references; root approval | New runtime uses inert/staging root; no automatic DB/index re-adoption |
| Retained workspace/project folders — `workspace.json`, `agents/*/config.json`, tasks/schedules/MCP/skills/toolbox/shared data, files/notes/output, memory and backlog Markdown | SyncStore writes; task/step/scheduler, directory sync, Markdown sync, reflection; migration/backfill | **Preserve** bytes for all intents, including F. Remove only app registrations/allowances; stop writers before detach. Explicit import/root selection is required for reattachment | Exact protected folders; archived jobs/integration settings remain in retained files but are inactive | File-tree hashes unchanged through reset/relaunch; no old jobs run or agents reappear |
| Workspace sidecars — `triggers.json`; `.ori/assistant-program-learnings-v1.json`; `downloads-janitor/settings.json` and `scan-state.json`; custom dashboards; setup wizard state in workspace data | Trigger cron/filewatch service, capability/setup handlers, janitor and reflection | Preserve with retained folders; F detaches and deactivates, never run “uninstall” hooks that change retained content | Sidecar presence and active automation counts, no content scan of user documents | Sidecars unchanged; runtime has no active registrations/jobs until explicit reattachment |
| Workspace allowlist — `D/workspace_allowlist.json` | Cached allowlist and startup `BackfillLocalWorkspacesIntoAllowlist` | F clears Ori import permission; R detach invalidates affected entries. Deleting it alone is insufficient: startup backfill must not recreate it | Approved imports, physical root/adoption policy | Same-root relaunch leaves detached snapshots unhydrated |
| Session uploads — `C/session_files/<session>/manifest.json` and uploads; permission audit logs when configured there | Sessionfiles store + filewatcher, upload endpoints | R/F remove enumerated app-owned uploads after watcher/requests stop; linked external documents stay preserved | File/session counts, bytes if available, retained references and overlap blockers | Files absent; watcher closed; no repopulation; external/nested sentinels unchanged |
| Vault catalog — `vaults` table in shared DB | Vault store and handlers | R/F detach catalog registrations; retain backing files and crypto dependencies. Inspect raw legacy schema before migrations, not a filtered `ListVaults` response | All raw rows and missing/custom/legacy file references; locked/unreadable reported | No registered old vault after restart; explicit reattachment can decrypt retained data |
| Vault packages — `V/<id>.orivault/vault.db` (and discovered legacy files) | Vault store caches DEKs; package DB handles opened by vault APIs | **Preserve whole package**, including wrapped key, encrypted records/folders/attachments, grants and audit. F must prevent retained grants/tokens becoming active without reattachment; do not invoke normal disconnect paths that mutate vault records | Package location/availability, catalog linkage, retained credential disclaimer | Same-file hashes plus decrypt/reopen after explicit attachment; no automatic integration activation |
| Connections — `D/connections/google.json`, `consent.json` — connections store/consent log | Mutex stores, OAuth callbacks, in-memory pending flow/state | F removes Ori-owned account metadata/consent and pending callbacks; preserves vault token bytes and provider account, no external revocation. R collateral vault detach must not falsely claim integrations are connected | Connection/grant status and dependent workspaces; retained token location, not values | No active connection/refresh after restart; persisted vault bytes unchanged |
| MCP global registrations — `C/mcp_registry.json` — config manager/registry | Server processes, clients, OAuth global hooks; external import on builder startup | F clears owned registrations/runtime clients and requires explicit re-enable/import; keep external Claude/Codex config and vault credentials | Server counts, enabled/imported origin, dependent plugins/workspaces | No old servers start or re-import; built-in inert definitions may return |
| MCP browser catalog — `C/mcp_search_sources.json`, `C/mcp_search_cache.json` | Cached source list/TTL fetch results | F removes custom sources and cache; curated built-in default allowed | Custom source/cache counts or unreadable | New catalog contains canonical built-in source, no old custom cache |
| Plugins — `D/plugins/installed.json`, `marketplaces.json`, `src/`, `state/<plugin hash>/<workspace hash>.json` | Manager, source update checker, workspace service processes/state locks | F removes owned installed registrations/marketplaces/state and managed clones; preserve linked third-party source folders and personal skill files. Stop services, no arbitrary uninstall hook. Ambiguous legacy install root/overlap blocks deletion | Installed/enabled counts, clone versus link ownership, state namespaces and dependent MCP | No enabled old plugin/state or background process; external sources unchanged |
| Project templates T; legacy `workflow_templates/*.json`, `custom/*.json` | Project library operations/startup starter materialization; workflow manager caches | F removes enumerated Ori-owned library/templates and custom workflow configuration; built-in starters may return. External/custom root ownership or overlap not proven means **block**, not recursive erase or silent exclusion | Owned/custom template counts, configured/legacy roots, preserved project copies | Canonical library recreated without custom templates; instantiated projects unchanged |
| Personal/repository/external skills — `~/.agents/skills`, repo `.agents/skills`, Claude/Codex directories | Skills manager/external cache, plugin skill registration | Preserve non-installation-owned skill contents; F clears Ori enablement/auto-discovery permissions, not files used by another harness | Sources and retained roots, own enablement registrations | External trees unchanged; prior implicit access not active |
| Usage/activity — `~/.ori-agent/usage_data/usage_records.json`, `C/activity_logs/<agent>.jsonl` | CostTracker cache/background save; ActivityLogger | F removes owned usage/activity records; shared HOME usage location requires ownership check/blocker if another installation writes there | Counts, exact roots and shared-location warning | No old usage/activity after close/reopen or late flush |
| CLI execution logs — EventLogger's configured root + `cli_agent_tasks/<task>/events.json`; generated native-MCP configuration | CLI task goroutines/event cache, run lifecycle; generated native-MCP config store | F clears the exact installation-owned event/config directories only after cancellation/join; preserve project edits and external CLI authentication/configuration | Active runs, retained project effects, external `ori-ws-*.config.toml` profiles | No remaining admitted jobs; exact owned paths cleaned, no glob deletion in system temp |
| Native-CLI MCP configurations — `D/cli-mcp/<workspace>.mcp.json`, `CODEX_HOME/ori-ws-<workspace>.config.toml` — `llm.CLIMCPConfigStore` | Regenerated on CLI invocation from workspace bindings | F/R detach removes exact owned generated configs where ownership is proven; preserve Codex `auth.json`, `config.toml`, models cache and other profiles. Shared-name/root ambiguity blocks cleanup, never erase CODEX_HOME | Generated workspace config names and effective roots, without embedded values | No stale generated profile invoked; all external auth/config sentinels survive |
| Mac wake — config's last scheduled event and shared `<UserConfigDir>/ori/wake/wake-candidates.json`/lock (`ORI_WAKE_DIR` override supported) | macwake owner + other processes including Herdr | F clears this installation's settings/jobs; preserve other sources' candidates/system state. Outstanding shared/programmed wake with unproven safe scoped cleanup is a **blocker**; no privileged restart/wake automation added by reset | Owned task wake vs other-source candidates; manual recovery needed | Fake OS runner proves no unrelated cancellation; native behavior must be separately labelled |
| Environment, `.env`, external CLI auth, third-party accounts, app binary/update metadata | Startup dotenv/PATH expansion; auth discovery can refresh external credentials | Preserve. Never revoke tokens or edit environment/config owned externally; suppress startup discovery/refresh during reset verification | Credential-source disclosure; operator-enforced roots can block F | Synthetic env/auth sentinels unchanged; no external network/auth calls |
| Unknown files and reset receipt | Unknown owners; receipt writer is separate | Preserve unknown content; unclassified active stores block F. Receipt lives outside enumerated wipe targets with bounded, secret-free results, retained until verification | Preserved/unclassified counts, blocker reasons and receipt operation ID | No root-wide removal; interrupted operation remains recoverable |

### Shared database domains

`database.Open` migrates after its retained-vault safety guard allows startup;
preview must still use the read-only inspector, not that constructor. The
migration inventory includes:

- `users` (canonical local profile, preferences and HQ designation), `workspaces`
  (rich workspace JSON including tasks, schedules and setup/integration state).
- `sessions`, `messages`, `session_tags`, `tool_calls`, `session_tasks`,
  `scheduled_task_reminders`, `smart_input_overrides`.
- `workspace_notes`, `note_links`, `note_tags`, `note_headings`, and note search
  indexes/virtual-table internals where present (`sessions_fts`,
  `workspace_notes_fts`, `note_headings_fts` and their SQLite-owned shadows).
- `review_issues`, `review_runs`, `session_review_status`.
- `workspace_runs`, `workspace_run_trace`, `workspace_run_artifacts`,
  `home_assistant_intake_traces`.
- `workspace_map_layouts`, `workspace_map_positions`,
  `workspace_map_group_presentations`.
- `personal_assistant_state`, `personal_assistant_assignment`,
  `personal_hq_followup`, `calendar_meeting_prep`.
- `daily_brief_config`, `daily_brief_revision`, `daily_brief_generation_claim`,
  `daily_brief_notification`.
- `workspace_plans`, versions, clarifications, approvals, task/run links,
  activity, draft snapshots, execution slots/queue/generations, reconciliations
  (all `workspace_plan_*` tables).
- Specialist setup journey runs and bounded operation, declaration-migration,
  and review receipts (all `setup_journey_*` tables).
- Sample Library state, source registrations, catalog facts, curation and copy
  provenance, plus bounded review/operation receipts (all `sample_library_*`
  tables). The source and copied files remain workspace-owned backing bytes and
  are not database reset targets.
- Agent Map layout and position records (`agent_map_layouts`,
  `agent_map_positions`).
- `vaults` catalog. Older schemas additionally contain `vault_records`,
  `vault_record_attachments`, `vault_folders`, `vault_grants`,
  `vault_audit_events`, and crypto material in the old vault row.
- `schema_migrations` and SQLite infrastructure are not user history; a reset
  may retain/recreate canonical schema rather than claiming zero tables.

Use actual schema/domain checks for preview and verification, not a hardcoded
chat count. A database-only reset affects profile, HQ, notes, jobs and connections'
vault registrations even when agents/settings remain. Unknown/new domains must
be classified before full completion.

**Legacy hazard:** migrations 012/013 drop the old vault-content tables/key
columns and remove empty-path catalog rows. They do not first export DB-only
content. The normal vault list filters missing/legacy rows. Therefore reset
preflight must inspect before `database.Open`/migration can destroy that evidence;
a legacy content-bearing database must block with a preserve/export/recovery
instruction. `internal/database/reset_legacy_test.go` originally reproduced this
loss and now asserts the fixed pre-migration refusal using the same real v11
schema, including encryption material present only in a live WAL. Empty legacy
layouts still upgrade. No config/provider/credential initialization is involved.

### Secret ownership hazard

`createConfigManager("settings.json")` supplies that **relative string** to
`NewDefaultSecretStoreForNamespace`; namespace normalization hashes the string,
not its canonical absolute path. Two roots using this call can share native
accounts. Fallback storage, by contrast, resolves through `ORI_DATA_DIR`.
Do not claim absolute-root isolation merely because filesystem paths differ.
A reset must use the attached namespace and establish ownership of legacy
accounts. Credential preflight now rejects a relative config/namespace before
calling the backend, so today's production `settings.json` namespace remains an
explicit `credential_namespace_ambiguous` blocker until a safe migration policy
is implemented. Never enumerate/delete all `ori-agent` accounts. For an
absolute, explicitly attached owner, preflight checks only the four exact
provider/search slots and maps locked, unavailable, read-only and failed reads
to blockers; failures never mean “absent.”

## Browser inventory (explicit allowlist, never origin-wide clear)

These are candidate category-bound cleanup keys. Concrete implementation must
share them with tests and confirm category outcome before removal. `prefix*`
means only that exact Ori-owned prefix plus its identifier, never `ori*` or
all keys. Other browsers/devices are outside browser-local cleanup.

| Storage family (source modules under `internal/web/static/js`) | Keys / exact families | Scope |
| --- | --- | --- |
| Theme/density/voice/settings, `themeManager`, `settings-page`, `voice-input`, layout head | `ori-theme`, `ori-ui-density`, `voiceSettings`, `note.openBehavior` | F; settings category only when explicitly reviewed as preferences |
| Chat input/layout, `app`, `resizer`, `sessions` | `enterToSend`, `planBeforeAction`, `chatPanelWidth`, `sidebarWidth`, `sessionSidebarCollapsed`, `sessionSidebarWidth` | F/preferences; not data-only cleanup |
| Workspace presentation, hub/command/canvas | `oriWorkspaceHubLauncherView`, `oriWorkspaceCommandViewMode`, legacy `oriWorkspaceDetailView`, `canvas-bg-color`; session keys `oriWorkspaceHubOverviewExpanded`, `oriWorkspaceHubLauncherTab` | F/preferences |
| Notes presentation | `note.leftRail.tab`, `note.toc.collapsed`, `note.aiAssist.collapsed` | F/preferences |
| Conversation caches/selections, `app`, `sessions`, `dashboard` | `ori_chat_session_*`, `ori_chat_assistant`, `ori_chat_assistant_*` (including slug variant), `activeSessionId`, session `activeSessionId_*`; `ori.homeAssistant.recentSessions` in local/session storage | R/F |
| Workspace references, sync and directory explorer | `oriWorkspaceSyncDismissed`, `oriWorkspaceHubSelectedId` (session), `currentWorkspaceId` (session), `sessionFolderCollapsed`, `workspace-directory-explorer:*`, `ori-workspace-command-agent:*` | R/F; agent references also A where affected |
| Note references/search | `note.tabs`, `note.tabs.workspace.*`, `note.search.recent` | R/F |
| Agent selection/evolution announcements | `ori.roster.selectedAgent`, `ori.evolution.lastStage.*` | A/F; quest reset does not erase assistant evolution |
| Vault reference/tab | `ori-selected-vault-id`, `ori-vault-active-tab` | F or actual vault-catalog detach, not merely settings selected |
| Onboarding operation IDs | `ori.personalAssistantHireRequestId`, `ori.personalAssistantHQRequestId`, `ori.personalAssistantApplyRequestId.*` | F. Replay must not accidentally replay old provisioning; do not remove an unresolved domain operation ID as if it completed |
| Per-workspace UI handoffs (session storage) | `oriSetupWizardResume:*`, `oriProjectOpenNotice:*`, `workspace-detail-entry-agent-prompt-dismissed:*`, `workspace-detail-task-assist-specialist`, `ori.homeAssistant.pendingWorkspacePrompt`, `ori-guide-handoff` | R/F; F clears stale provisioning/workspace intent |
| Automation preference and tab mechanics | `ori.homeAssistant.automationMode` (local/session); `ori-keyboard-nav-persistent` (session); `oriTabId` (session) | F clears preferences; preserve `oriTabId` as non-user tab identity for operation coordination |
| Generic `settings.js` toggle writer | DOM `data-setting` names, not an unrestricted deletion prefix | Preserve unknown keys; only named owned controls may join the preferences allowlist |
| Reset recovery | New bounded operation ID/receipt reference | Preserve through pending/partial/unknown/restart states and verified result display |

No IndexedDB/service-worker cache reset surface was found in the current app JS
scan. Unknown origin keys, cookies and opaque-origin plugin data are not blanket
reset targets. Stale in-memory tabs still need a generation/admission check;
localStorage cleanup alone cannot stop them writing old state back.

## Lifecycle observations (1.3)

- `Server.Shutdown` stops plugin source checks, plan auto runner, workflow,
  plugin workspace services, session filewatcher, folder picker and gateway.
  It does **not** close the session HybridStore/SQLite or folder index, and does
  not stop the location manager/cost-tracker loop here. The retained session
  handle and post-shutdown write are now characterized in
  `internal/server/reset_lifecycle_test.go`; EventBus and usage writes are pinned
  in `internal/server/reset_writers_test.go`.
- `HybridStore.Close` stops its loop channel, performs a final cache flush, then
  `database.DB.Close` attempts a WAL checkpoint and closes SQLite. Calling it
  after destructive removal is too late; joining periodic writers is separate
  from closing a stop channel. Tests must pin the actual interleavings.
- Standalone shutdown calls background shutdown *before* HTTP shutdown. Menubar
  calls HTTP shutdown *before* background shutdown and stays in the same process.
  It previously ignored HTTP shutdown errors and discarded the runtime; the
  host-admission slice now retains it on failure and requires Stop retry/Quit. Process exit cannot justify
  the menubar sequence's persistence safety. `internal/menubar/reset_lifecycle_test.go`
  exercises real start/stop over two instances with injected safe construction.
  The native menubar UI and full production builder remain unverified.
- Some services start during builder construction (location, HybridStore loops,
  session watcher, triggers). `HTTPServer.BaseContext` also starts `s.Start` in
  a goroutine after listening. A reset apply boundary must precede all of these,
  not only `Server.Start`.
- Startup folder reconciliation, local allowlist backfill, agent migration and
  snapshot restore can resurrect detached state. `WORKSPACE_DIR` authorizes
  maintenance independently of `workspace_root_confirmed` and is therefore a
  likely F blocker, not an environment variable reset may silently rewrite.
- Progression engine backfill is one-time, but builder separately completes the
  first-day quest whenever an old first assignment is complete. The bypass of
  intentional Getting Started reset is pinned in `reset_startup_test.go`.
- The menubar C/D split above is now corrected: its shell honors ORI_DATA_DIR,
  canonicalizes the physical root and publishes it before constructing settings.
  Both supported launchers now acquire process ownership before constructors (see
  the 2.4 ownership slice below); this alone does not enable destructive reset.
- `cmd/server` loads dotenv before directory activation and expands PATH. A
  sanitized parent environment is not proof an arbitrary production executable
  will remain isolated. Dedicated full-server tests must control those seams.

### Writer trace and host-specific consequences

| Owner | Observed stop/start behavior | Reset consequence |
| --- | --- | --- |
| Task/step executors | Stop closes channel, cancels tracked jobs and waits; cancellation still writes final task status/results. Start also reconciles old assigned/in-progress tasks | Block confirmation while active work exists; stop new dispatch before staging. Finalization must finish before the apply boundary, never wipe underneath it |
| Task scheduler | Immediate initial poll; Stop closes channel and waits | Disable new dispatch while pending; restart must not load retained schedules |
| Daily Brief scheduler | Immediate Tick; Stop joins its loop, but Tick uses a background context. Service, HTTP handoff, scheduler ticks and revision-ready callback now share host admission | Admission covers generation/final persistence without cancelling it; process relaunch remains mandatory because this is not complete host draining |
| Workspace Runs / automatic Plans | HTTP and Plan launchers detach work; final-approval Runs retain an environment after ExecuteRun returns | Child handoff is now registered before response/return, with permits through final status/Plan writes and approval-environment teardown. Persisted in-progress/awaiting state must still be inspected after relaunch |
| Directory sync / session watcher | Directory sync joins its loops, then closes watcher; watcher owns additional event/debounce goroutines | Explicit teardown required; no filesystem removal while watched |
| Trigger service | Start restores pending fires from sidecars; Close explicitly lets in-flight dispatches finish independently | Retained sidecars stay inert; process boundary, not Close alone, prevents late writes |
| EventBus / location | Subscribers run in separate goroutines; shutdown/cancel does not join every callback/detection | Receipt acceptance is not proof of quiescence; no live destructive apply |
| Chat / CLI | Chat derives timeout from request context; CLI tracks per-job cancellation but has no global join in Server.Shutdown | Gate new work and refuse active/unowned child processes; never equate browser disconnect with completion |
| MCP registry | StopAll exists but Server.Shutdown does not call it | Running external services must be stopped and ownership verified, or block staging; do not assume parent exit kills arbitrary child processes |
| Menubar shell settings | A separate onboarding manager precedes Controller construction. C/D now converge through ORI_DATA_DIR and shell mutations share the host admission gate | Apply must still precede shell manager construction. Same-process restart cannot qualify; changed C/D or recovery evidence blocks construction |

## Chosen lifecycle (1.4)

**Destructive reset is staged and applied on a full process relaunch, before
normal persistence constructors. Live SQLite/file replacement and managed
restart are not supported in this increment.** Replay Setup and Getting
Started stay authoritative in-memory operations with no restart when no
destructive operation is pending.

1. Resolve canonical runtime ownership at each supported host entry point,
   before constructors, migrations, external import or profile seeding. Acquire
   an installation process-lifetime lock under `D/.ori-reset/` before opening
   stores. Hold it through menubar server stop/start; only process exit ends
   ownership. A second process cannot apply while the old owner lives. A port,
   browser health check or PID alone is not ownership. Alternate entry points
   without this contract report reset unavailable, rather than guessing.
2. Preview is read-only: normalized intent/categories, authoritative locations,
   dependencies, safe counts or unknowns, active-work status, protected roots,
   credential provenance and blockers. Do not open a legacy DB with the normal
   migrating constructor. No provider request, token refresh, permission prompt,
   external discovery mutation or secret contents belong in preview.
3. Execute validates exact typed confirmation, preview lifetime/binding,
   request identity and current ownership/dependencies before mutation. Refuse
   active chat/tasks/CLI runs, unknown child-process ownership or an uninspectable
   writer instead of promising cancellation preserves the user's work. A job
   arriving after preview makes admission fail/review again.
4. Serialize admission for the installation, gate normal mutation and new
   background dispatch, and stop/drain known writers. Route-level gating alone
   is insufficient for timers/events. Persist the staged plan and operation
   receipt atomically before replying 202. No destructive files/keys change in
   the accepting process. A stop/drain failure is recorded, not a successful
   wipe; all subsequent ordinary mutation remains fenced while unresolved.
5. Show persistent `awaiting_restart`. Standalone: stop this Ori process and
   relaunch the same installation. Menubar: **Quit the menubar application and
   relaunch it**; its Stop/Start Server controls are insufficient. No automatic
   OS restart, admin prompt, timed reload, or “success” toast substitutes for it.
6. The new process acquires ownership, loads a bounded, versioned receipt, and
   verifies that its process identity differs from the accepting identity.
   Re-resolve/revalidate trusted targets, legacy schema, protected overlaps and
   backend ownership before applying; never execute arbitrary paths recovered
   from a malformed or edited receipt. Missing/corrupt/unsupported receipt or
   unverifiable ownership fails closed into recovery, before startup services.
7. Apply only explicit category targets at this pre-start point; persist each
   category's durable result and verification boundary. Do not remove SQLite
   files with active handles. Do not broaden a failed category to root deletion.
   Cross-filesystem/native-secret operations are not a transaction: there is
   **no rollback promise**. Already-completed categories are not replayed.
8. Recreate canonical stores/defaults with import/dispatch suppressed, then
   verify named postconditions for this operation/generation. Only verified
   completion enables normal app work and the explicit navigation action. A
   partial/blocked/interrupted operation keeps recovery/status available and
   ordinary mutation fenced until its unresolved categories are safely handled.

The process lock must also detect/refuse conflicting supported instances using
the same installation. A legacy/alternate process which cannot establish the
ownership contract is unsupported for reset: instruct the operator to stop all
such instances rather than killing them or relying on a free port. Process exit
alone is not sufficient proof for untracked external children; those are
preflight blockers until their teardown is established.

The private receipt is `D/.ori-reset/operation.json`, excluded from every reset
category. Use owner-only permissions and atomic replacement; bounded size
(initial cap 64 KiB), one active operation and a bounded last-result/replay
record, no transcripts, key values or raw provider errors. Oversized plans fail
preflight instead of writing an unbounded deletion manifest. Keep the minimal
retention/import policy independently at `D/.ori-reset/policy.json` so retiring
a result never re-enables old imports. Neither file is general job storage.

## Shared API and outcome contract (1.5)

`internal/settingsreset/types.go` defines public, versioned payloads. Runtime
resolved targets/process identities and the private apply journal are separate,
non-client-authoritative data. Public removed/retained locations are display
facts only.

- Intents: `selected_data`, `start_fresh`, `replay_setup`, `getting_started`.
- Existing selection families map to `settings`, `agents`, `app_records`,
  `setup_steps`. Start Fresh additionally includes `identity_progress`,
  `app_configuration`, `integrations`, `templates`, `activity`, `runtime_cache`.
  Category definitions own preview, application, dependencies and checks; a
  missing owner is a blocker, not a silent omission. Full intent cannot be
  reconstructed from selected checkboxes.
- A preview contains its opaque ID, expiry (initially 10 minutes), scope digest,
  selected/dependent categories, facts (`count:null` when unknown), retained
  paths/credentials, blockers and restart mode. Bind the digest to intent,
  targets, ownership, schema/dependencies and retention policy. Counts describe
  a moment and do not freeze new data; changed identity/root/schema/dependencies
  require a new review. Bound the in-memory preview set and never evict an
  admitted operation's durable recovery state to make room.
- `POST /api/reset` accepts only `preview_id`, `request_id`, `confirmation` for
  the new contract. Retain the XMLHttpRequest header, 1 KiB body limit and exact
  `RESET`; reject unknown fields. It cannot take a client filesystem path or
  accept extra categories after confirmation. The server derives the entire
  plan from its held preview and revalidates it.
- Return 202 with the durable operation for staged acceptance, 409 for changed
  scope/conflicting operation/stale confirmation, 400 for malformed or empty
  options, 404/410 for an unknown/retired operation, and explicit unavailable
  backend/blocker errors without mutation. A persisted partial outcome is a
  readable operation result, not hidden in an overall success boolean.
- `GET /api/reset/operations/{id}` is authoritative recovery for lost responses,
  multiple tabs and restart polling. Availability/health alone is not evidence
  of success. Reading status cannot resume/retry/apply work.
- Same request ID + same admitted preview returns the same operation. Reusing
  either for another plan fails; a consumed/expired preview never starts a new
  wipe. A second tab sees the active operation and cannot overlap it. Request
  ID changes do not bypass installation admission or broaden a consumed plan.
- Retry first previews the unresolved, retryable categories of the same
  operation (`retry_operation_id`), revalidates their scope and obtains fresh
  confirmation. A retry updates that operation/revision and cannot repeat
  completed/preserved categories. Unknown/interrupted categories must first
  reconcile postconditions; never blindly infer whether their deletion ran.
- States remain distinct: `preparing` (durable admission/draining),
  `awaiting_restart`, `applying`, `verifying`,
  `completed`, `partial_failure`, `blocked`, `interrupted`. Each category has
  `pending`, `completed`, `failed`, `skipped`, `preserved` or `unknown` outcome,
  named verification checks, safe errors and an explicit retryability flag.
  Persist an applying boundary before effects; a crash with ambiguous effects
  recovers as interrupted/unknown unless checks prove a specific outcome.
- Legacy `settings`, `agents`, `sessions`, `onboarding` booleans normalize only
  into their original selection families. An old direct execute without a
  reviewed preview returns `409 preview_required`; it does **not** silently
  become Start Fresh or keep an unsafe no-preview deletion path. Preserve
  familiar response fields during UI migration: `success` means wholly verified
  completion, `reset_items` lists only verified completed categories, and
  `requires_restart` remains true while pending. Old preview calls without a
  selection get a validation error, never an executable all-data plan.
- Browser-local cleanup is a separately reported local outcome after its server
  category is verified. It must not erase the operation receipt while pending,
  or represent other devices as cleaned. A stale tab/generation cannot mutate
  pre-reset state simply because its localStorage was not yet cleared.

## Preservation and restoration decisions (1.6)

- **Legacy DB-only vaults:** inspect raw schema/tables/rows read-only before
  migration. Non-empty legacy content or only-copy crypto material blocks with
  an instruction to preserve the database and recover/export it using a
  compatible version. Do not “repair” by running current migrations. Unknown
  future schema or unreadable vault evidence also blocks.
- **Current vaults:** retain whole packages and encryption keys; detach only
  catalog/registration state. The characterization establishes that wrapped
  package keys suffice after explicit catalog reattachment, not that the
  attachment API now accepts only an explicitly selected `.orivault` package,
  validates its existing SQLite schema and sole metadata identity, registers it
  without moving/deleting the package, and leaves password proof to the existing
  Unlock flow. Existing encrypted-bundle Import and Relink of an existing row
  are not equivalent. Lock caches and remove app
  bindings without calling disconnect APIs that mutate retained vault records.
- **Secret scope:** use only a positively owned attached backend namespace and
  exact provider/search keys. Preserve `vault_dek` even if it appears obsolete;
  classify fallback/legacy plaintext copies separately. The shared relative
  namespace is a blocker when relevant keys exist or their ownership/presence
  cannot be established. Do not automatically copy-then-delete shared entries.
  Recovery explains that the four shared provider/search entries need explicit
  migration/removal with other Ori installations stopped; vault encryption keys
  and external CLI auth are excluded. A new canonical namespace alone is not
  evidence that old credentials were erased. Native backend failure stays
  unavailable/locked, never “not found.”
- **Roots/overlaps:** never recursively erase a root shared with retained
  workspace/vault content, including a custom root inside agents, session
  uploads, templates or D itself. Protect exact descendants or block. Preserve
  unknown files, linked plugin sources, personal skills, external templates and
  external auth unless positive Ori ownership places them in the reviewed
  removal set. Configured external ownership ambiguity blocks full completion.
- **Selective settings:** preserve root/registration metadata needed to keep
  *unselected* workspaces/vaults usable; explicitly list that exception in
  preview. Resetting API keys/preferences must not accidentally choose and
  adopt another conventional workspace directory. Clearing app records instead
  detaches affected registrations and explicitly gates their future import.
- **Restoration policy:** after F, resolve an inert staging root and disable
  external MCP/CLI imports, profile/quest backfill and legacy agent adoption
  until explicit user reattachment/configuration. Do not silently change
  environment variables. `WORKSPACE_DIR` that forces adoption is a preflight
  blocker. Other operator roots that force reconnection get the same treatment.
  Apply the policy to live rescan/root-selection as well as boot. A selected
  Agents reset must suppress implicit workspace/legacy rehydration of removed
  profiles while leaving retained workspace bytes unchanged.
- **Identity/progress:** F resets both app-state and relational identity plus
  global agents, so startup cannot seed one old copy from another. Canonical Ori
  defaults/local profile/built-in records may be recreated. Getting Started
  needs an explicit reset marker respected by the builder's separate first-day
  reconciliation, in addition to the engine's consumed-backfill semantics.
  Replay resets only setup steps and cannot overwrite retained identity or
  duplicate provisioning through old browser request IDs.
- **Shared/OS surfaces:** other apps' CLI authentication, third-party accounts,
  user environment and unrelated wake candidates remain untouched. No provider
  revocation or privileged wake cancellation is part of reset. An outstanding
  owned/shared wake or external child which cannot be safely scoped is an
  explicit preflight blocker with manual recovery. A default path is not proof
  of exclusive ownership of shared HOME usage or legacy stores.

## Read-only preview implementation (2.3)

`GET /api/reset/preview?intent=selected_data&category=agents&category=app_records`
accepts repeated `category` parameters. Legacy boolean query options normalize
only to their original families; mixed forms, duplicate/unknown keys, missing
intent on the new form, empty selections, arbitrary paths and oversized queries
are rejected before reading owners. Start Fresh accepts only
`intent=start_fresh` with no client category list; the server supplies its fixed
category set and exact owner inventory. Responses are `Cache-Control: no-store`
and inspection has a five-second context.

- The builder supplies its real config, agent, setup, session DB/upload, folder,
  allowlist and vault owners after construction. Narrow read-only location
  methods expose the same paths those owners use. Split CWD/data roots produce
  actual locations and confinement blockers, not substituted default paths.
- Database inspection uses a read transaction on the existing connection: no
  normal constructor, migration, checkpoint, key read, vault-file open, provider
  request or external auth discovery. All 60 classified record tables are
  counted; known FTS infrastructure is explicit. Unknown domains, unsupported
  schemas, legacy vault evidence and uninspectable catalog rows block. The
  v11 regression proves inspection and the new startup guard both preserve
  legacy content instead of letting normal Open erase the evidence.
- Raw catalog paths are resolved by the vault owner, including whole packages
  outside the current creation root. Protected overlaps, hard links, symlink
  escapes/dangling links and invalid path encodings block. App-record preview
  also lists collateral root-consent/import-permission changes and their actual
  files. Upload-root counts explicitly include preserved files and are bounded;
  linked/uninspectable contents are unknown, never reported as zero.
- Confirmation references are server-held, defensively copied, bounded to 32
  previews/10 minutes and 64 KiB per plan. The scope digest binds resolved
  targets, protected roots, database location/schema and readiness. Ordinary
  record-count changes do not freeze the dataset, but changed owners, schema,
  consent, overlaps or blockers require a new review. This validation is **not
  operation admission** and does not yet consume a token or persist a receipt.
- Saved provider/search key preflight is scoped to `openai_api_key`,
  `anthropic_api_key`, `gemini_api_key` and `brave_api_key` through the attached
  config owner. It reports only a distinct-slot count, never values, and treats
  locked/unavailable/read-only/failed inspection as unknown plus a blocker.
  Relative shared namespaces block before backend access. `vault_dek`, unrelated
  keys/namespaces and environment/CLI credentials are explicitly retained;
  external provider environment presence is disclosed only as booleans/counts.
- Production readiness remains `lifecycle_unavailable` until the real process
  lease/coordinator is attached. Unit fixtures may supply a read-only readiness
  callback; there is no test fault endpoint or mock-backed production apply.

Preview planner/API/builder/legacy-inspection checks pass normally and under
`-race`. Tests compare application file contents before/after preview (excluding
SQLite SHM read-lock coordination only), verify real mixed known/unknown counts,
legacy query normalization, cache bounds/expiry and scope changes. Ratcheted
lint is clean; gosec reports zero findings in settingsreset and the new database
inspection file (the database package's old G301 in `db.go` is unchanged).
The old live-deletion POST path has now been removed. Preview/admission safety
still cannot be used as evidence that destructive application or post-relaunch
verification works.

## Startup/legacy error safeguards (2.4, in progress)

`database.Open` now probes existing files using SQLite **mode=ro before opening
a writable connection**, then rechecks before storage pragmas or migrations.
This covers callers outside the server builder too. Fixed metadata and EXISTS
queries detect legacy content, non-empty key columns and unclassified vault
columns without loading secret values. Refusal returns
`ErrRetainedVaultMigration` with preserve/export guidance. The read-only probe
matters: closing a refused *writable* connection could otherwise implicitly
checkpoint a crash-left WAL. Tests prove repeated refusal preserves database
bytes, live-WAL keys and an offline DB+WAL copy (including URI-reserved filename
characters), while empty v11 layouts still upgrade. Future-schema errors no
longer suggest blindly resetting the database. This is not an atomic ownership
lease against other writers; process-lifetime ownership remains required for
staged reset.

The product no longer contains the live file-deletion/cache-clear helpers.
Legacy requests now assert review refusal and unchanged selected/cached data,
not success from a copied historical algorithm. Original root, sibling, traversal
and symlink cases now exercise the real planner. Actual mixed category/owner
failure assertions remain required on pre-start application before delivery;
request-refusal and result-projection tests do not prove staged application.

### Process ownership and metadata boundary (2.4, in progress)

`internal/resetstate` is a small pre-store package with no config, credential,
SQLite, provider, or service constructors. Both `cmd/server` and `cmd/menubar`
call `BeforeStores` before persistence initialization; standalone does so before
port inspection/takeover too. Menubar respects explicit ORI_DATA_DIR and gives
its shell settings manager and server the same canonical physical root.

- Darwin/Linux use nonblocking exclusive `flock` on the stable
  `D/.ori-reset/process.lock` inode. Alias paths contend for the same owner;
  another installation remains independent. The lock file is never unlinked.
  Exclusive file creation avoids a reproduced concurrent O_CREAT/APFS race.
- The lease is pinned in process-owned storage, including through garbage
  collection. `Close` refuses a pinned lease. Neither a shutdown return nor
  menubar Stop/Start releases it: only process exit does. This is necessary
  while known shutdown paths still leave cached writers and SQLite handles alive.
- Root-relative `os.Root` operations, physical-identity rechecks, effective-owner
  checks, private modes (0700 directory/0600 files), regular-file/single-link
  checks and symlink refusal protect metadata. Replaced ownership directories or
  lock inodes fail closed. This is a cooperating-process boundary, not an OS
  sandbox or proof that legacy instances/untracked children stopped.
- Only `operation.json` and independent `policy.json` can be read/replaced, with
  a 64 KiB bound per record. Missing and empty files differ. Writes use exclusive
  temporary files, file sync, rename, then directory sync. Parent-directory sync
  also covers a creator losing the acquisition race. Failure after rename is
  explicitly uncertain, not rollback; orphan temp files remain recovery evidence.
  Payload schema validation and operation admission still belong to the pending
  coordinator, not this filesystem layer.
- Until that coordinator can interpret/apply/verify a journal, **any** private
  state besides the lock prevents normal constructors. Corrupt, empty, future,
  policy-only and orphan records are never silently ignored or deleted. Current
  recovery is a startup refusal with preserve/compatible-version guidance, not
  a shipped operation-status UI or a claimed successful reset.
- Other platforms, including Windows, currently leave destructive reset
  unsupported. Ordinary startup is allowed only without prior reset metadata;
  they refuse an existing reset directory rather than ignoring pending work or
  import policy. Native Windows locking/ACLs/durable replacement remain work.

Owned subprocess tests run the real standalone/menubar `main` refusal paths.
Private construction seams fail safely if ordering regresses, before any port
inspection, shell manager, broad builder, systray, browser or native credential
command. A separate pure-package child holds ownership through GC and an early
Close attempt, exits without deferred unlock, and allows reacquisition only
then. The real controller's two-stop/start SQLite fixture also proves its outer
lease remains held while those old handles remain writable.

These checks pass under `-race` on macOS/arm64, including repeated concurrent
acquisition and process-exit tests. Darwin/Linux code and unsupported Windows
code compile; Linux/Windows native execution has not been performed. Ratcheted
lint reports zero issues; gosec reports zero in resetstate and seven pre-existing
browser/AppleScript subprocess findings in unchanged launcher functions. The
menubar race-test linker emits an LC_DYSYMTAB warning but the tests pass. No real
credential backend, external authentication or native menubar UI was exercised.

The planner now uses the metadata package's reserved directory constant, but
production still reports `lifecycle_unavailable`: a lease alone is not safe
admission, writer draining, pre-start application or restart verification. The
old POST is now refused; the replay regression uses the admission coordinator
and owned HTTP rather than the historical deletion helpers.

### Durable admission and recovery API (2.4 / 2.6, in progress)

`settingsreset.Coordinator.Stage` serializes on the installation lease, including
across separately constructed coordinators. It validates the held preview and
its installation, budgets the complete journal envelope before fencing, calls
an atomic `Lifecycle.TryFence`, and revalidates scope before draining. Successful
fencing is irreversible within the running process. It records `preparing`
before `Drain`, then durably records `awaiting_restart` or a safe `blocked` result.
No category is applied in this process and every category/check remains pending.

- A preview allocates **separate** `id` and `operation_id` values before POST, so
  a lost POST response can be recovered with a read-only operation lookup.
- A repeated request/preview pair returns the same durable operation/revision
  without preview expiry checks, writer draining or record rewriting. Reusing
  either for another pair conflicts and returns the active operation. Only one
  operation is currently supported: result retirement and explicit unresolved-
  category retry remain unimplemented, never an implicit second wipe.
- The private journal checks schema/version, canonical encoding, selection,
  target kinds/confinement, installation identity and coherent admission states.
  Duplicate/unknown/case-variant fields, unsupported states, arbitrary target
  kinds, truncation and even manual reformatting fail closed. Recovered targets
  are evidence only: pre-start apply must independently resolve owners before
  treating any path as executable.
- Panics after fencing and persistence uncertainty latch admission closed across
  coordinator reconstruction. A known receipt disappearing cannot become a fresh
  operation in the same process. A prior process's `preparing` state is projected
  as `interrupted` without rewriting/retrying it. Raw drain/provider errors are
  never persisted. A bounded failure message explains recovery instead.
- `POST /api/reset` requires JSON, XMLHttpRequest, exact RESET and only the three
  execute fields. Duplicate/unknown/case-variant/mixed fields, null booleans,
  trailing data and bodies exceeding 1 KiB are rejected. Legacy booleans receive
  `409 preview_required`; missing runtime lifecycle receives 503. The handler
  uses a bounded independent admission context after valid confirmation, so
  closing the browser is not cancellation or permission to unfreeze work.
- `GET /api/reset/operations/{id}` is registered and read-only. Both APIs use
  no-store responses. 202 means staged acceptance, **not** success. Compatibility
  fields report only named verified category checks; a restart, partial result,
  unverified check or contradictory completion state cannot imply success.

Owned HTTP tests now send a real preview, two accepted POSTs and a status GET,
assert one drain/revision and unchanged selected/retained bytes. Other tests
cover concurrent coordinators, busy work arriving after preview, material scope
changes during fencing, status while drain is paused, drain failure, panic,
failed persistence, missing/corrupt/future receipts and journal-envelope overflow.
The original replay regression is green on this real admitted path, not made
vacuous by only rejecting legacy requests. Old live-deletion expectations are
replaced with actual compatibility-preservation behavior; original confinement
cases are ported to the real planner, with no copied historical deletion helper.

Full settingsreset/settingshttp/resetstate tests pass under `-race`; scoped
server route/preview/lifecycle tests, nine transitional JS tests and ratcheted
lint pass. Gosec reports no findings in new reset code; two existing G301 findings
remain in unchanged `settingshttp/handlers.go`. Route golden coverage now includes
preview and operation recovery.

**Delivery limit:** no production `Lifecycle` is attached yet. The real builder
still reports lifecycle_unavailable; startup still refuses all receipt/policy/
orphan evidence before constructors. There is no pre-start category applier,
relaunch recovery HTTP host, import-suppression policy consumer or retry engine.
The current browser's legacy request therefore receives preview_required, not a
working reset. No full-server/native/browser reset completion is claimed.

### Cooperating runtime admission (2.4, in progress)

`resetstate.WorkGate` now arbitrates instrumented work against an irreversible
in-process fence under one mutex. Admission refuses active work **without
cancelling it or partially fencing the runtime**. Permits are idempotently
released after final owner writes, not merely after a provider returns. There
is no Unfence method. An unwired gate is explicitly unknown, not evidence of an
idle runtime; existing non-reset callers retain ordinary behavior.

The production builder shares one gate across this initial set of entry points:

| Covered entry | Permit boundary |
| --- | --- |
| Build / Server.Start | Synchronous construction and startup, including task boot reconciliation; this does not account for every service started there |
| Ordinary HTTP | Through handler return, including GET handlers, which are not assumed read-only; detached work needs its own permit before the response |
| Task polling/execution | Before owner discovery and claims; child registration before dispatch; through final status, result storage and event publication |
| Step polling/workflows | Before dependency/status changes and claims; through task results and workflow completion rollup |
| Workspace orchestrator | Mission planning, synchronous task execution, sequential execution and its own mission-to-goroutine handoff |
| Task scheduler | Before workspace/schedule/wake inspection and changes; mission/reflection child permits registered before the poll returns |
| CLI executor and CLI HTTP create | Before adapter/provider discovery; through usage/event persistence; HTTP registers detached work before acknowledging 202 |
| EventBus publication/subscribers | Before history/filter processing; each callback registers before Publish returns and retains admission through its save/panic recovery |

The reset APIs use a separate, explicitly registered control mux inside the
existing security/recovery/CORS chain. They do not count themselves as ordinary
work, and read-only operation recovery/replay stays reachable after fencing.
This is not a broad `/api/reset*` path exemption into the main router. Fenced
ordinary requests return no-store 503 reset_pending, including page requests.
A dedicated recovery page must exist before enabling this in the product.

Tests exercise real owned HTTP preview → concurrent paused config save → busy
refusal → normal save completion → accepted admission → fenced ordinary methods
→ GET recovery and replay. They assert one drain, unchanged selected setup bytes
and retained sentinels. Real temporary file-store tests pause task, step and
orchestrator final writes, prove admission stays busy without cancelling their
contexts, and reopen the same files to verify normal completion. Manual task
HTTP, first-open, assist/review reruns, parent sequences and legacy scheduler-node
triggers register children before acknowledgment and retain them through final
workspace saves, including failed saves. CLI HTTP similarly uses a fake
provider/adapter and an empty executable PATH. Event tests prove that an
already-dispatched save remains tracked even after the existing non-joining
EventBus.Shutdown returns. Failed task claims release context, running slot and
permit. Gate entry/fence races, duplicate release, cancellation and panic cleanup
are covered.

Daily Brief generation now enters before claims and holds through revision,
current-pointer, notification callback and final claim status; HTTP registers its
detached child before 202, and each scheduler tick enters before owner reads.
Workspace Run HTTP similarly transfers before Created, and run execution owns
environment preparation, runner, artifacts, validation and final status. A
final-approval run retains its permit after ExecuteRun returns; approve, reject
or stop releases it only after environment teardown. Automatic Plan launch does
the same for its loop, synchronous Task/Run dispatches and final Plan status.
CostTracker now transfers each TrackUsage permit to the exact asynchronous
snapshot that includes it; coalesced signals retain every permit, Close joins the
writer, and closed/fenced trackers reject before changing memory. Session cache
flush and retention-cleanup ticks also enter the host gate; HybridStore Close
joins both loops before its final flush and database close. Location detection,
manual/zone mutation and emitted callbacks are admitted before OS detection or
state changes, callbacks receive child permits before launch, and Stop joins the
detection and callback workers. Directory-sync polling and fsnotify events enter
before workspace reads, OS watch changes or EventBus publication. Plugin update
checks enter before registry/source/Git access, Stop joins active checks without
cancelling them, and fenced invalidation cannot change its cache. Trigger
ownership now spans webhook acknowledgement, an open fixed debounce window,
durable pending-fire merge/claim, synchronous mission/task/domain dispatch and
final fire-history persistence. Claim uncertainty retains its permit instead of
stranding unowned durable work. Trigger file-watch events and validation enter
before store/stat/watch changes. File Janitor domain handoff acquires its scan
child before trigger dispatch returns; coalesced follow-ups retain that ownership,
scheduler callbacks enter first, and Stop joins scans. Home Assistant task
confirmation transfers a child permit before acknowledgement. Gateway channels
are registered with a lifetime permit before launch; inbound/outbound dispatch
enters the same gate, and the console channel no longer hides its input loop in
an unowned second goroutine. Global MCP servers and workspace-surface service
processes acquire lifetime permits before construction; calls and asynchronous
connect starts are admitted before launch, health/status work remains covered,
and only a verified successful stop (or concrete local ExitError) releases
ownership. Ambiguous stops retain retryable process evidence and the permit.
Lifetime owners are now counted separately from finite operations: an idle
service does not masquerade as active user work, so an atomic fence can reject
new calls before a future `Lifecycle.Drain` stops it. A finite admitted call
still refuses fencing without cancellation, and a failed/ambiguous stop leaves
an owner count that must fail drain. This primitive is not itself that lifecycle.
The Plan lifecycle service now gates creation, generation, retention, editing,
review decisions, approvals and revisions before model/store access; hosted
materialization/execution remains covered by ordinary HTTP admission or the
Automatic Plan parent permit. The legacy workspace manual-task route, Calendar
Meeting Prep and review jobs transfer permits before acknowledgement/run-ID
return and retain them through final task/note/review persistence; request
cancellation no longer erases accepted work. Delayed chat shutdown and menubar
status callbacks are registered before launch. Failed final writes are not
projected as persisted success. These in-memory
permits do not recover ownership after process death: durable in-progress and
awaiting-approval rows remain mandatory pre-start recovery evidence.

**This is still partial instrumentation, not a production Lifecycle.** Supported
hosts share the lease's gate across runtimes and shell writers (see below).
Direct Plan execution helper use outside hosted HTTP/Automatic Plan wiring,
remaining external children and any newly discovered callback writers still
need their own admission
and drain/ownership coverage. A callback
that spawns detached work must transfer a permit again; tracking its parent is
not enough. Existing shutdown/SQLite/child-ownership findings are not overturned
by an idle gate. The builder still supplies no CheckLifecycle/coordinator, and
BeforeStores still refuses all recovery evidence before constructors. Pre-start
apply, recovery serving after relaunch, import policy and verification remain
unimplemented. No production, native-child or browser reset completion is claimed.

Validation on macOS/arm64: repeated focused race tests pass; complete
`-race -short` workspace, CLI, CLI HTTP, settingsreset, settingshttp, resetstate,
orchestration HTTP, Daily Brief/HTTP, Workspace Run and Workspace Plan,
LLM/CostTracker, session, location, directory-sync, trigger, File Janitor,
gateway, Home action, MCP/workspace-surface and plugin-update suites pass.
Scoped server
admission/host/preview/lifecycle/route-golden checks and both launcher builds
pass. Ratcheted lint reports zero issues. The latest scoped
LLM/session/location/plugin/workspace/trigger/File Janitor/Plan/Run/orchestration/
Daily Brief/server gosec reports 113 existing
findings and zero overlap with branch-added/changed Go lines. Full delivery gates
remain pending.

### Host-lifetime gate and constructor boundary (2.4, in progress)

The process `Lease` now owns the `WorkGate`; neither a new server builder nor a
new menubar Controller can replace that gate when using the same lease. Both
launchers forward the lease explicitly. Alternate callers retain their existing
constructors, but receive no destructive reset capability.

- `Lease.EnterRuntime` acquires construction admission before touching owners,
  checks clean recovery metadata, physical CWD/data-root identity and in-memory
  uncertainty/expected-receipt latches, and pins ownership before returning.
  Root mismatch has explicit relaunch/configuration guidance, not an instruction
  to delete state. Releasing a construction permit never releases the lease.
- Builder and background startup use that boundary. A failed/panicking hosted
  build latches uncertainty because earlier phases may already have writers or
  children. Newly discovered root/recovery errors at background startup block
  ordinary HTTP too. An already-fenced late Start does **not** incorrectly turn
  a valid pending receipt into uncertain admission.
- Lease uncertainty now rejects new instrumented work even if fencing itself
  panicked. Existing permits remain counted and contexts are not cancelled;
  neither this emergency closure nor an idle gate proves complete draining.
- Menubar transfers its construction permit to the constructor goroutine,
  rather than releasing it when the Start caller times out. A timeout cannot
  admit another build or pretend Stop joined the constructor. Start/Stop
  generations prevent old waiters/serve errors overwriting a newer lifecycle.
- A failed HTTP shutdown retains the runtime, reports the failure, and leaves
  Stop retry available. It does not cancel background work or advertise Stopped
  while requests remain active. Ports/runtime pointers are read under the
  controller lock. Existing final SQLite/background close gaps remain separate.
- The separate shell settings manager shares the lease gate. Menu start holds
  root/host admission **before** port preflight/takeover, and autostart/port
  changes hold a permit across their native action/dialog and final save.
  Setter guards prevent a retained shell cache writing after the fence. Quit
  and Stop remain available; neither clears reset admission.

Owned subprocess tests cover actual standalone lease handoff, repeated runtime
construction, pinned GC/Close/exit ownership, real menubar Stop/Start with old
SQLite writes still counted, initialization timeout, HTTP shutdown timeout and
retry, second-controller fence refusal and shell persistence preservation.
Constructor/preflight/dialog seams make regressions fail safely before native
credentials, provider discovery, port takeover, AppleScript or systray. The
server tests use an instance-local first-phase constructor refusal, not a broad
builder or production fault endpoint. Menubar menu and constructor guards are
exercised headlessly; native UI/LaunchAgent/Keychain behavior is unverified.

This closes host-lifetime sharing, **not** the production reset gate. Remaining
writer/child coverage, joined draining, pre-start apply, relaunch recovery,
verification and the unresolved shared-namespace migration policy still block
CheckLifecycle/coordinator wiring.
All receipt/policy/orphan evidence continues to block normal startup; there is
still no applied-reset or browser completion claim.

Validation: repeated focused race checks plus full `-race -short` resetstate,
settingsreset, settingshttp, menubar and both launcher suites pass on
macOS/arm64. Scoped server host/admission/preview/lifecycle/route-golden tests,
both launcher builds and ratcheted lint pass (zero lint issues). Scoped host
security scanning reports 14 existing findings, none overlapping added/changed
Go lines; resetstate has zero findings. Linux/amd64 and Windows/amd64 resetstate
cross-compilation passes, not native execution. The existing macOS menubar
LC_DYSYMTAB linker warning remains; no native UI or credential validation was run.

## Validation limits / next implementation boundary (1.7)

The above lifecycle is chosen because tests falsified the simpler live-replace
and same-process-restart assumptions. Preview and startup guards are implemented,
and supported launchers now hold a process-lifetime lease. Admission/status and
its journal are implemented. Exact-key credential inspection/deletion now exists
for absolute, explicitly attached owners and preserves `vault_dek`, unrelated
slots and external sources; fake-store tests cover locked and partial failure.
Production selective lifecycle wiring, canonical namespace migration, scoped
category application, unresolved-category retry, and same-operation verification
are implemented. Test-owned same-sandbox demos staged both app-record reset and the full Start
Fresh intent through the production HTTP lifecycle, fully stopped the owned
server, relaunched through `BeforeStores`, and read completed durable results.
The Start Fresh run exercised all nine categories, preserved workspace/vault
checksums, kept the vault detached on relaunch, then attached and unlocked that
same package through the production API. The demos explicitly removed inherited
provider-key variables and forced the encrypted fallback secret store with a
synthetic passphrase; they did not invoke Keychain or an external account.
Headless Playwright screenshot capture was attempted again, but Chromium still
failed its macOS Mach rendezvous, so screenshot evidence remains pending.

Start Fresh's supplemental inventory/application and retained-vault attachment
are implemented and covered by same-installation fixture tests, including
byte-preserved vault decryption after explicit attachment. Still pending are the
full production-builder demo/screenshots, native macOS menubar/Keychain journey,
Windows/Linux native locking/backend execution, and complete browser/e2e/
accessibility gates. Native and external authentication behavior is not
represented as validated.

Group 1 validation passed: full settingshttp/settingsreset/resetfixture/database/
vault package tests; scoped server/menubar/database/vault lifecycle tests under
`-race`; menubar same-process test under `-race -count=5`; ratcheted lint across
all touched packages (0 issues); and gosec on resetfixture/settingsreset (0
issues). Scoped menubar gosec reports four pre-existing findings in unchanged
`launchagent.go` (G204 twice, G301, G306), none in the changed controller seam.
No production fault hook, real credential value or machine-specific absolute
path is checked in. Full repository and browser gates remain pending.

Characterization tests intentionally pin current unsafe behavior and must be
updated to the desired regression assertion when the corresponding fix lands;
do not preserve a bug merely to keep its finding test green. Group 2 must wire
host ownership/pre-start apply and persistent outcomes together before any new
UI is considered delivered.
