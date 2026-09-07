# Settings reset: scope and lifecycle findings

Status: findings-first baseline and chosen contract for `settings-reset-ux`.
**This document specifies the implementation; it does not claim the reset
behavior is shipped.** Shared payload types live in
`internal/settingsreset/types.go`; product wiring follows in checklist group 2.

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
- Real settings-only reset rejects missing confirmation, deletes the confirmed
  fixture file, and preserves unselected files plus every retained sentinel.
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
  production still uses `server.New` + `srv.HTTPServer`. Native credential
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
- Running real migrations from a seeded v11 schema drops DB-only vault content
  and its empty-path catalog row before Open returns. Conversely, detaching a
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
| CLI execution logs — EventLogger's configured root + `cli_agent_tasks/<task>/events.json`; ephemeral `ori-workspace-run-*` / schema/config temporary files | CLI task goroutines/event cache, run lifecycle; generated native-MCP config store | F clears known app-owned records/temp artifacts only after cancellation/join; preserve project edits and other apps' CLI config. Builder currently passes A (a file path) to EventLogger: do not invent a default directory | Active runs, retained project effects, actual log root/unavailable persistence | No remaining admitted jobs; exact owned temp paths cleaned, no glob deletion in system temp |
| Native-CLI MCP configurations — `D/cli-mcp/<workspace>.mcp.json`, `CODEX_HOME/ori-ws-<workspace>.config.toml` — `llm.CLIMCPConfigStore` | Regenerated on CLI invocation from workspace bindings | F/R detach removes exact owned generated configs where ownership is proven; preserve Codex `auth.json`, `config.toml`, models cache and other profiles. Shared-name/root ambiguity blocks cleanup, never erase CODEX_HOME | Generated workspace config names and effective roots, without embedded values | No stale generated profile invoked; all external auth/config sentinels survive |
| Mac wake — config's last scheduled event and shared `<UserConfigDir>/ori/wake/wake-candidates.json`/lock (`ORI_WAKE_DIR` override supported) | macwake owner + other processes including Herdr | F clears this installation's settings/jobs; preserve other sources' candidates/system state. Outstanding shared/programmed wake with unproven safe scoped cleanup is a **blocker**; no privileged restart/wake automation added by reset | Owned task wake vs other-source candidates; manual recovery needed | Fake OS runner proves no unrelated cancellation; native behavior must be separately labelled |
| Environment, `.env`, external CLI auth, third-party accounts, app binary/update metadata | Startup dotenv/PATH expansion; auth discovery can refresh external credentials | Preserve. Never revoke tokens or edit environment/config owned externally; suppress startup discovery/refresh during reset verification | Credential-source disclosure; operator-enforced roots can block F | Synthetic env/auth sentinels unchanged; no external network/auth calls |
| Unknown files and reset receipt | Unknown owners; receipt writer is separate | Preserve unknown content; unclassified active stores block F. Receipt lives outside enumerated wipe targets with bounded, secret-free results, retained until verification | Preserved/unclassified counts, blocker reasons and receipt operation ID | No root-wide removal; interrupted operation remains recoverable |

### Shared database domains

`database.Open` always migrates. The migration inventory includes:

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
instruction. `internal/database/reset_legacy_test.go` characterizes the actual
v11 → current migration on an explicit temporary SQLite path (without config,
provider or credential initialization).

### Secret ownership hazard

`createConfigManager("settings.json")` supplies that **relative string** to
`NewDefaultSecretStoreForNamespace`; namespace normalization hashes the string,
not its canonical absolute path. Two roots using this call can share native
accounts. Fallback storage, by contrast, resolves through `ORI_DATA_DIR`.
Do not claim absolute-root isolation merely because filesystem paths differ.
A reset must use the attached namespace and establish ownership of legacy
accounts; shared/ambiguous legacy native credentials are a preflight blocker
until a safe migration/ownership policy is settled. Never enumerate/delete all
`ori-agent` accounts. Backend `Status()` currently advertises availability based
on discovery, not a verified unlocked read; failures cannot mean “absent.”

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
  calls HTTP shutdown *before* background shutdown, ignores the HTTP error,
  clears pointers, and stays in the same process. Process exit cannot justify
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
- `cmd/server` loads dotenv before directory activation and expands PATH. A
  sanitized parent environment is not proof an arbitrary production executable
  will remain isolated. Dedicated full-server tests must control those seams.

### Writer trace and host-specific consequences

| Owner | Observed stop/start behavior | Reset consequence |
| --- | --- | --- |
| Task/step executors | Stop closes channel, cancels tracked jobs and waits; cancellation still writes final task status/results. Start also reconciles old assigned/in-progress tasks | Block confirmation while active work exists; stop new dispatch before staging. Finalization must finish before the apply boundary, never wipe underneath it |
| Task scheduler | Immediate initial poll; Stop closes channel and waits | Disable new dispatch while pending; restart must not load retained schedules |
| Daily Brief scheduler | Immediate Tick; Stop joins its loop, but Tick uses a background context | No bounded live replacement claim; process relaunch is mandatory |
| Directory sync / session watcher | Directory sync joins its loops, then closes watcher; watcher owns additional event/debounce goroutines | Explicit teardown required; no filesystem removal while watched |
| Trigger service | Start restores pending fires from sidecars; Close explicitly lets in-flight dispatches finish independently | Retained sidecars stay inert; process boundary, not Close alone, prevents late writes |
| EventBus / location | Subscribers run in separate goroutines; shutdown/cancel does not join every callback/detection | Receipt acceptance is not proof of quiescence; no live destructive apply |
| Chat / CLI | Chat derives timeout from request context; CLI tracks per-job cancellation but has no global join in Server.Shutdown | Gate new work and refuse active/unowned child processes; never equate browser disconnect with completion |
| MCP registry | StopAll exists but Server.Shutdown does not call it | Running external services must be stopped and ownership verified, or block staging; do not assume parent exit kills arbitrary child processes |
| Menubar shell settings | `cmd/menubar` reads a separate onboarding manager before Controller construction; chooses C from HOME, independently of ORI_DATA_DIR | Apply must precede shell manager construction too. Same-process server restart cannot qualify; inconsistent C/D must converge explicitly or block |

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
- States remain distinct: `awaiting_restart`, `applying`, `verifying`,
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
  current UI can do it. Add a narrow, validated attach-existing-file operation
  in group 4: inspect an explicitly chosen package without rewriting it, verify
  metadata/password, then register it. Existing encrypted-bundle Import and
  Relink of an existing row are not equivalent. Lock caches and remove app
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

## Validation limits / next implementation boundary (1.7)

The above lifecycle is chosen because tests falsified the simpler live-replace
and same-process-restart assumptions. The new types have serialization tests,
not a working coordinator yet. No reset API has been rewired and no native
restart or credential cleanup has run. Full production-builder shutdown and
native macOS menubar/Keychain journeys, Windows/Linux locking/backends, browser
accessibility/recovery, and applied-reset postconditions remain group 2–5 gates.

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
