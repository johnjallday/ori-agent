# Unified workspace review and placement

Status: implementation contract; acceptance evidence lives in the feature findings,
not in this document. This extends the interaction described in
[assisted workspace placement](assisted-workspace-placement.md), not its filesystem
or scan authority. Existing specialized protocols remain authoritative.

## Interaction and ownership

**Review/edit setup → Place & create on map → click/Enter creates.**
Automatic placement is a complete alternative: selecting it clears the manual
candidate but does not create; the full review retains an explicit final action.
There is no editor/backdrop or second confirmation panel while positioning.
Hover, pan, nudge, Back and Escape are inert. Selection-only map mode (`draft`)
is not final consent and must remain distinct from reviewed creation.

`sessions.js` remains the shared creator controller. Existing form setters,
CreateWorkspaceTeamDraft, role rosters and destination policy own material
choices; WorkspaceBuildSession remains the durable owner when applicable. A
renderer projects these owners and emits intentions. It never retains another
material draft, calls a model, constructs a request, grants permissions, creates
profiles, executes a plan or treats display text as authority.

### Bounded review projection

A projection identifies the editor generation, current material revision and
adapter kind, and contains display-only fields:

- Name, purpose/goal and blueprint identity (including unavailable/unknown states).
- Ordered role rows: effective name/settings, saved assignment versus staged
  creation, required/optional/host-filled status, vacancy and shared-Home scope.
  Zero roles is valid where the owner permits it. Never edit saved global profiles.
- Typed resource rows only when relevant: held folder/project/link, account
  prerequisite, new scaffold versus existing selected project. Paths are display
  data; opaque handles go only to their existing trusted setters.
- Complete blueprint-defined input values and labelled advanced-edit return
  actions. Description, tags, color and exact bounded brief remain available.
- Destination: standalone, exact existing group/Home, or separately reviewed new
  Home. State already-completed prerequisites explicitly; Cancel is not rollback.
- Separate immediate effects, later steps, permission/readiness facts and actual
  next action. Ordinary creation never claims to scan, connect accounts, execute
  starter tasks or verify a live application. File Janitor retains its full
  access/Filed/first metadata-review/monitoring disclosure.
- Owner-supplied final labels, including the effect of a map click, automatic
  confirmation, progress, partial result and recovery. No inferred executable
  plan, serialized authority or generic fallback when an adapter is unavailable.

Intent callbacks include edit-name/goal, edit-team/role, edit-input/advanced,
replace-resource, change-destination, place, automatic, confirm, back/cancel,
read-status, explicit owner continuation, open-result and retry-position-only.
Unavailable intentions are omitted or honestly disabled, not simulated controls.

## Freshness and lifecycle

Each async callback must retain its owner reference and editor generation.
Flush and validate material edits before obtaining review and again before
entering placement. At dispatch recheck owner/version, blueprint/team/readiness,
source/selection lifetime, destination and action consent. Dirty, stale, in-flight
or conflicting state cannot confirm. Late inference must respect user-edited
fields and cannot redirect a replacement editor. A changed-plan refusal returns
to full review; no silent refreshed-plan confirmation or slug-change retry.

States: editing → reviewed → positioning → dispatching → accepted → complete,
partial or uncertain. Before acceptance, Back resumes the exact draft and candidate;
Cancel abandons only that draft. After acceptance, closing is not undo and edits
cannot replace the accepted request. Read status never continues execution.
Explicit continuation, if supported, belongs solely to the original owner.

Map availability means a visible mounted current map, not merely a global API.
On Tree, narrow screens, actual 200% zoom or no-map routes the review remains
usable with automatic placement. Explicit view switching is safe only within the
same retained editor lifetime. Never navigate to another page while claiming an
in-memory draft survived. Reopen/refresh restores only canonical owner data;
selection snapshots and displayed paths cannot revive filesystem access.

Return focus to the review heading/status or exact labelled edit invoker. Native
controls, visible focus, keyboard/tap alternatives, restrained live announcements,
long/hostile text escaping, both themes and reduced motion are required. Inputs
own their key events; map Enter/Escape must not intercept focused controls.

## Ordinary creation operation boundary

The existing `POST /api/workspaces` pipeline remains the creation implementation.
Its group-policy operation and File Janitor preparation claims are not general
ordinary-create replay protection. Legacy callers keep their API; reviewed UI
ordinary creation uses a bounded durable wrapper around that same pipeline:

1. `POST /api/workspaces/create-operations` stores one immutable bounded material
   request with a caller UUID and a server digest binding the material JSON and
   resolved blueprint definition; this is inert review storage.
   Exact replay returns that record; changed payload or another owner is refused.
2. `POST /api/workspaces/create-operations/{id}/confirm` takes only that digest.
   It runs the normal create validations using the stored request. Immediately
   before the first domain effect, atomically claim reviewed → accepted and freeze
   a server-generated resource ID. No provider, assistant or HQ is required.
3. Concurrent/repeated confirms join the original record rather than rerun it.
   Definite pre-acceptance refusal ends that review; new terms require another
   explicit review and confirmation. Accepted failure remains reconciliation
   required. A reserved ID or existing same-name workspace proves nothing.
4. Completion records the exact successful response only after verifying its
   returned ID against the accepted ID and the canonical owned workspace. A
   malformed success is uncertainty, not completion. Warnings/partial readiness
   remain visible in the original response, not rewritten as operational success.
5. `GET /api/workspaces/create-operations/{id}` is owner-scoped, read-only
   observation. A durable complete receipt plus the canonical resource proves
   completion; a missing/deleted/foreign result is reconciliation required. An
   accepted operation interrupted before a complete receipt is never resubmitted
   automatically, including after a process restart. No status read seeds tasks,
   staffs agents, repairs setup or adopts by name. A provenance-verified existing
   workspace may be shown separately as partial evidence without upgrading an
   interrupted operation to complete. `POST .../{id}/abandon` can retire only an
   unaccepted review; acceptance racing abandonment is not undone.

Storage: SQLite migration **85 → 86** adds ordinary-create operation records
(identity, owner, material/source digest/request, frozen workspace ID, state and
outcome). Canonical workspace SharedData carries the operation ID/digest; a
reserved UUID, name or copied response without that provenance is not proof.
Existing workspaces/builds receive no inferred claims or consent. Build schema 5,
folder digest schema 2 and companion protocols/blueprint versions are unchanged.
Legacy create calls do not gain replay identity merely by supplying a group key.
Policy-aware group/Home and reviewed setup requests stay on their own protocols;
the wrapper refuses them rather than double-claim their effects.

The operation is a delivery/uncertainty journal, not a new general workflow engine.
An incomplete accepted operation exposes reconciliation rather than promising to
finish arbitrary partly executed effects. Browser recovery retains the operation
reference and reads it; it never retries ordinary creation on a lost reply.

### Attached ordinary build sources

An ordinary draft with a generic held folder or observed link retains its build
and existing create/link completion owners. Its operation request includes
`build_session_id` and exact `build_review_version`. The journal additionally
binds the canonical path-free source witness. Review and final validation require
an open current build with no setup/preparation claim, pending observer/inference
or recognized-plan authority; a missing/expired hold never becomes Blank.

Confirmation serializes that build's mutation/acceptance and rechecks under its
existing store lock before the first effect. SQL accepts at most one operation
for the same owner/build, even if another controller read an older sidecar or
another operation UUID was reviewed. Accepted journal records fence later edits
and plain-create bypass, independently of whether the build's invalidation or
completion write succeeded. A failed write after acceptance is uncertainty,
not permission to recreate. Build schema and folder hold lifetime are unchanged.

A rejected replacement may still advance an observer reservation version. The
client may read that same build and adopt only unchanged material choices and
both source identities, while its local editor/revision remains current. It
cannot re-pick, extend a hold, overwrite late edits or silently save over another
tab. Returning from source editing refreshes the same shared review, including
when a redundant draft sync clears without another write.

Recognized Code builds request bounded software evidence only through explicit
`POST /api/workspaces/build-sessions/{id}/software-review` (`mode: review`). The
existing software owner inspects the selected source and retains its evidence,
plan and final setup authority. Names-only `mode: adjust` uses that owner's
adjustment operation, without rereading project contents. Both are current-build
version fenced; neither accepts setup. The live pick moves to the existing
build-review target, without renewing its lifetime or deriving authority from a
path. Pending software review cancellation is not a saved decline. The read-only
`software_review` capability distinguishes this owner from other deferred-task
plans; an optional summary alone cannot select its names-only adjustment API.

For settled software builds, `setup_result` reads the existing source journal's
exact decided, resolved build/resource outcome. It is not a new persisted build
receipt or execution endpoint. New-resource positioning additionally verifies
the actual workspace's source marker; reused outcomes never authorize moving
that resource. The existing map controller dispatches the exact source owner's
setup action, then saves geometry once after canonical completion. Reading a
result cannot repeat that position attempt; repair is explicitly layout-only.

Recognized software/project plans, source offer/run commits and File Janitor
preparation do not acquire ordinary creation authority through this adapter.
Their separate confirmation and observation protocols still apply. In particular,
`from-inbox` admission has not been generalized. The same inert review can be
mounted inside a current Home/conversation source card. Its offer/thread/digest,
name, brief and monitoring choices remain bound to that card's existing setup
owner; it does not manufacture a build or source-transfer request. Returning from
map restores that exact conversation rather than hydrating a replacement entry
conversation. Its result verifies the canonical outcome and resource before
saving geometry. Source receipt, attachment consumption, task controls and
continuation remain with the existing controller.

File Janitor's shared result and accepted/uncertain review retain the original
explicit defer operation. A lost response is observed, not resubmitted; an actual
accepted preparation failure can continue only the same saved preparation after
explicit repair. Neither reading its status nor closing its presentation starts
another scan or changes monitoring. Unstarted software assessment/summary work
remains in the workspace and requires its separate Start action.

## Destination and layout policy

Coordinates are geometry, never membership, access or staffing. Parent/group/Home
comes only from the reviewed canonical owner. A locked group-page parent wins
over map origin; cancelling district Build clears its legacy origin. Do not infer
membership from a rectangle or move a workspace into a nearby group.

Layout subjects must match the actual newly created resources:

| Outcome | Preview / permitted position writes |
| --- | --- |
| Standalone workspace | One workspace footprint / exact returned workspace ID |
| Child of existing group/Home | Child footprint / new child only; parent unchanged |
| Ordinary new Group | Group frame / returned group ID; explicit member approval separate |
| New Home plus project | Reviewed composite footprint, with named new IDs and relative geometry; reject unsupported composition rather than preview one building |
| Reused Home plus new child | New child only; never move/rename/restaff reused Home |
| Catalog/library-only records | No invented workspace or placement; show actual catalog outcome |
| Import or reuse only | Real Import / Use existing verb; no mandatory placement or “created” result |

If an owner creates several resources in separate prerequisite confirmations,
review states what already exists. Position only the newly created subject of the
current action unless a specific composite layout was explicitly reviewed. A
composite cannot be supported by moving unrelated existing members. Invalid or
unsupported geometry returns to review/automatic placement, not a silent nearby
point. Keep existing server-owned required-Home/group policy intact.

Only canonical proof of creation enables exact `commitPlacement` against the
current collision rules. Domain creation and positioning are separate results.
A failed save leaves resources intact and discloses automatic-layout fallback.
Explicit repair targets only the proven ID(s) and requested geometry, rechecks
collisions and never repeats create, reparent, staffing, grants, first tasks,
scans or application opening. Late layout callbacks cannot reset a newer map
session or its Move mode. Read/history navigation never saves geometry.

## Adapter and recovery matrix

| Adapter / entries | Sole final owner / proof | Return/recovery boundary |
| --- | --- | --- |
| Blank, built-in/user/plugin ordinary declarations; Home/hub/map/deep link | existing ordinary create via immutable operation / canonical receipt | same draft before accept; operation observation afterward |
| Assistant ordinary build | build setters + ordinary operation / actual created build/resource | exact build version; no create_now bypass or render-time inference |
| Inbox File Janitor | janitor-review / janitor-prepare claim and linked scan receipt | retain exact source transfer, hold TTL, consumed attachment and empty-scan receipt |
| Generic folder/link / software / recognized setup | source run or build plan owner, selected by actual proposal | retain source owner unless exact durable transfer is specifically implemented; no relaxation of from-inbox restrictions |
| Ordinary Group / selected members | reviewed Group Manager or setup-journey group commit / returned group ID | member approval is separate, not geometry |
| Managed Home / required Home | group-template or requirement review/commit / created versus reused identity | prerequisite survives cancellation of later child |
| Music/REAPER / connection-only quests | existing Home, project connection and journey owners / canonical resources | no ordinary fallback, no implicit launch/live control; paired candidate acceptance required |
| Import/continuity/HQ | existing import/adoption/designation owners / their receipts | truthful reuse/import, no forced placement, no bootstrap redesign |

A replaced/expired selection retains material edits and requires a trusted re-pick;
failed/cancelled replacement retains the prior valid selection. Repaint never
rescans or extends a hold. Partial setup exposes its real owner and next action.
Opening old receipts consumes no newer attachment and starts no work. Concurrent
builds and foreign/stale sources fail closed with explicit conflict recovery.

## Ordinary declaration and entry coverage

The declaration catalog is not a list of interchangeable creation authorities.
`workspace_creation_review_catalog_test.go` reads every shipped starter's actual
role declarations and verifies inert plan/review behavior; the JS review/team
contracts cover required, optional, automatic, saved, shared and read-only rows.
The domain-neutral trusted plugin fixture additionally proves an exact source
change refusal before resources exist. These contracts do not claim live plugin
compatibility.

| Declaration family | Ordinary coverage / real acceptance boundary |
| --- | --- |
| Blank | No-HQ/no-model real map and automatic creation; agentless requires the existing explicit choice |
| `research-project` | Real saved/new multi-role mix, unchanged saved definition, two new profiles, no first-work execution |
| `content-production` | Real final creation after scripted build turns; the scripted build service itself is UI-route coverage, not build-store acceptance |
| `code-project`, `writing-project` | Real edited-goal creation without running first work; server goal contracts retain pending/read-consent semantics |
| `calendar-ops` | Real Tree automatic creation; optional vacancy, pending tasks, scoped workspace-files binding and not-started setup |
| `email-ops`, `github-ops`, `backup-and-recovery` | Catalog/preview contracts project their own setup steps; Calendar exercises the shared real setup-result observation adapter, not those external connections |
| `personal-ops`, `daily-briefings`, `travels` | Shipped declaration/role and goal-inheritance contracts; ordinary task/goal effect families exercised above |
| `file-janitor`, retired `downloads-janitor` | Declarations covered; source-backed filing retains the separate preparation owner and group-1 real acceptance; the retired alias is not reintroduced into listings |
| User catalog, advanced folder, direct user-blueprint link | Real typed values and invalid-range refusal, return from advanced editing, exact scaffold bytes and unchanged source bytes |
| Neutral plugin declaration | Real server contract tests, including exact changed-source refusal; no installation/live application claim |

| Ordinary entry | Reachability / final action coverage |
| --- | --- |
| Home New Workspace | Real manual multi-role and source-free assistant success/failure-to-manual completion |
| Workspaces hub URL | `/workspaces` currently redirects to Home; no claim that its dormant page is reachable |
| Home Tree / non-map surface | Real Calendar automatic completion; hidden map cannot capture the draft |
| Canvas Build | Real exact-coordinate final Enter with pre-consent resource/layout invariance and reload assertions |
| Direct blueprint URL | Exact generation-fenced selection; real custom declaration and unavailable-link explicit replacement, never silent Blank |
| Source-free assistant finish | Real host/build-store cases with a loopback scripted model; proposals never supply final consent and observation does not infer |
| Folder/link/setup, Group, district, managed Home | Not ordinary adapters; remain separate integration/acceptance milestones |

Result setup hints first prove operation/resource identity and then read the
existing setup-wizard status. They do not mark setup ready. A busy accepted
result disables navigation until its current owner finishes; an explicit native
open preference runs only after direct confirmed creation, never status/reload
or layout repair. Desktop launch and native picker acceptance are separate from
the sandboxed host-request assertions.

## Verification boundary

Inventory every declaration and entry; test projections declaration-first and
exercise every distinct owner through a real server. Fixtures prove rendering
and routing, not domain effects, native dialogs or live applications. Required
NOT RUN acceptance blocks PR readiness. Count resources before review/placement,
after one final gesture, after duplicate/lost-reply/reload recovery and after
layout-only repair. Check exact receipts, unchanged global profiles/source bytes,
no render/place model calls, actual membership and actual immediate/later work.

Music/REAPER source compatibility, sandbox staging and published rollout are
separate evidence. No version/floor bump, installation or companion write is
implied by this host presentation contract.
