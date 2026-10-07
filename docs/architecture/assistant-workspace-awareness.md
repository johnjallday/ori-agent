# Personal Assistant workspace awareness: investigation contract

**Status: findings plus in-progress host implementation; not shipped behavior.**
The full feature includes context-following conversations, setup continuity,
notes/tasks, and permitted files. A context badge alone is not the delivery.
Implementation proceeds in that order with one writer. This document records
non-sensitive decisions; local runs/screenshots and exact source inventory are
in `tasks/findings-assistant-workspace-awareness.md` and its evidence directory.

## Characterized baseline

The original `TestAssistantWorkspaceContext_BaselinePendingReviewMissingFromModel`
characterized production Route/Ask, relationship, file/session stores, metadata
observation, and review. Its current successor is
`TestAssistantWorkspaceContext_CurrentWorkspaceWithPendingReview`. It creates a generic group with Album-1, a second project, and an
unlinked Album-5 folder. Only the configured model is an inspecting stand-in.

- A live pending review is in canonical conversation hydration, with a digest.
- The accepted folder turn retains its offer but supplies `[]` for *new* setup
  options. No existing review summary or current page workspace reaches the
  model. The existing prompt says an empty list means no suggested review.
- Navigation references do not move conversation ownership out of Personal HQ.
- Metadata selection does not deliver a sentinel file body or absolute path.
- No creation/confirmation runs on Send or hydration. Model input has no native
  execution workspace, directory, MCP server or execution scope.

Group 2 has replaced the current-workspace absence assertion with the validated
projection. The pending-review summary remains unimplemented in group 3: the
empty new-options list is still not a canonical review summary.

The real-host browser fixture in
`tests/personal-assistant-workspace-awareness.spec.ts` captures Route/Ask payloads
and the pending card without a model. Set up is initially **enabled**; holding a
confirmation response makes it disabled/faded, then it re-enables. This proves
one busy-state path, **not** the original screenshot's cause or specialized
REAPER setup. The busy text uses the observation root (Documents) rather than
selected project (Album-5); correct that mismatch with the review presentation.

`tests/personal-assistant-workspace-baseline.spec.ts` separately stages the exact
compatible Music 0.1.1 and REAPER 0.9.0 candidates in disposable HOME/data. The
host creates a genuine Music-owned Home, a physically grouped Album-1 fixture
(not a staffed/project-linked child), and a pending recognized Album-5 review.
On `/workspaces/{slug}/assistant`, the primary action is **enabled**, opacity 1,
but paper-colored text has a transparent background: `--hc-signal` is absent.
`home-command.css` defines that token only on the Home dashboard body, while
`.pa-specialist-offer .btn-primary` consumes it on all drawer hosts. This
reproduces faded styling, not a disabled/expired permission gate; the original
uncaptured screenshot is not asserted to have the identical condition.

The original standalone `workspace-assistant.tmpl` omitted shared DX utilities,
so Send failed locally before Route/Ask. Group 2 now loads that dependency exactly
once. The exact-candidate browser reaches real Route/Ask with canonical ID/slug
references and the review/source/workspace state unchanged. Shared drawer primary
tokens remain a group-3 fix. No vendor model, confirmation, project runtime or
live REAPER runs in this specialized check.

## Current group-2 implementation

- `assistantcontext` supplies data-only overview and bounded turn-attribution
  DTOs. `POST /api/home-assistant/context` refreshes canonical metadata without
  loading profile/memory, scanning a directory, calling a model or storing a turn.
- Route validates panel references; Ask independently freezes canonical location
  and subject IDs at acceptance. Ordinary project questions remain hired-assistant
  conversations; explicit specialist/execution intents retain their existing
  gates. Workspace browsing never sets native CLI execution fields.
- Per-turn panel registries recheck user/relationship and pinned workspace ownership
  before each read. The existing home metadata tools receive owner/subject-filtered
  stores; separate discovery includes groups and preserves labeled project/group
  totals. Full notes/tasks/files and validated evidence citations are still pending.
- Provider capability controls tool advertisement. Snapshot-only paths keep the
  validated overview and explain the lack of brokered readers; no provider switch
  or native-MCP workaround is used.
- Migration 77 adds optional `messages.turn_context_json`. The canonical atomic
  writer saves user/answer attribution with optional folder observation, checks
  session owner, relationship version and workspace ownership inside the same
  transaction, and retains the existing folder-revision CAS/cache invalidation.
  Generic message/JSON/continuity inputs cannot author this field. Hydrated context
  is historical data, never new authority; missing old attribution remains unknown.
- The drawer's live context line refreshes on opening and navigation. Per-reply
  labels use accepted server attribution, not the current page. Tab-local draft
  and drawer-open state survive full navigation; attachment/review identities
  remain independently canonical.

Real-host browser evidence covers A→B before Send, in-page back/forward,
navigation during a held real Ask response, draft retention, reopening and
app-wide clearing. The complete group-2 demo additionally uses `wt demo` and the
production Ollama adapter against a loopback-only deterministic provider. It
holds actual generation at the provider while navigation changes A→B, then
reloads canonical saved user/answer attribution, the same pending review and
unsent draft. Legacy rows remain `Earlier workspace unknown`. App Home group
selection is a browsing reference without changing legacy execution targets.
Run `python3 scripts/assistant-workspace-demo.py --port 8954`; fixture-only tests
are `python3 scripts/assistant-workspace-demo.test.py`. Both child server and
sandbox are cleaned; vendor credentials and Codex home are not passed through.
The server/provider fixture separately verifies current pinned reads and
revocation during generation. These are deterministic host/adapter checks,
not configured-vendor-model proof. Review projection/placement and substantive
source readers remain the following groups' work.

## Four separate scopes and the read principal

1. **Owner:** current server-resolved user, hired relationship/profile, state
   version and designated HQ. Canonical Session remains owned by HQ/profile.
2. **Location:** active page workspace; explicit Home map/tree selection only
   when the page is app Home. IDs and slugs are different reference types.
3. **Subject:** uniquely resolved explicit user target, otherwise location;
   an attachment names the folder observation independently. A subject change
   affects this turn only. Ambiguous/conflicting action intent needs one choice.
4. **Destination:** a reviewed operation's canonical workspace/Home and effects;
   never inferred from a later page, history, same display name or model prose.

Panel reads act **for the requesting user through the hired relationship**, not
as the project's entry agent. Host resolution uses `LocalUserProvider` (currently
`local`) and canonical `OwnerUserID`. Native-created workspaces explicitly set
`local`; existing owner normalization treats empty legacy ownership as local.
A nonempty different owner, missing/inactive workspace, broken relationship,
foreign selected task/source or disagreeing references fails closed. Do not
manufacture a workspace-local instance or copy its credentials/tools.

This is a new narrow *host read adapter*, not reuse of the workspace chat's
principal. In the local app, ordinary notes/tasks/attachments and purpose-empty
linked directories are already user-visible sources; a user-requested panel
read may use those existing source contracts after user/workspace validation.
Opening/navigating is not an instruction to read contents. User-selected metadata
observations are not such sources. A future non-local principal needs an explicit
access implementation; it must not reuse the local-owner default.

| Source | Canonical lookup and authority | Required exclusions/refresh |
| --- | --- | --- |
| Notes | Session note store; exact `WorkspaceID` after GetNote, not title/preview alone | Duplicate names clarify; moved/deleted IDs fail; current bytes/time each read |
| Tickets/tasks | Workspace/TicketService, exact subject workspace and task ID | Detail/result/error are read-only; no schedule/execute/state change |
| Ordinary attachment | Current workspace attachment ID plus workspace-owned relative file | Legacy OriginalPath/file:// is metadata, not an arbitrary file grant; never follow it |
| User-visible directory | Fresh exact DirectoryReference ID with WorkspaceID match and empty Purpose | Never derive root from request/history; removed/replaced reference fails; no inheritance |
| Managed knowledge | Existing relationship-bound HomeSection and eligible memory renderer | No borrowed project-agent ID; paused/pending/rejected/forgotten/revoked entries excluded; raw MEMORY.md/sidecars cannot bypass |
| Exact project entry | Fresh typed ProjectEntryLocator and contained approved managed root or purpose-empty DirectoryReference | No live capability/MCP; sample/library roots and capability-internal Purpose excluded; exact entry only |

Home children may supply bounded owner-checked project summaries; this does not
recursively read child notes, files, memory, runtime state or credentials. Folder
linking is a separate reviewed mutation. Where a source is not independently
eligible, offer linking/setup or direct file supply; do not read and then seek
permission or silently create a grant. No new companion permission API is needed
for these host-owned user-visible sources.

Refresh relationship/scope at Send and every tool read; check again before
persisting/confirming. Freeze location/subject by canonical ID for the turn.
Fresh reads may reject revoked scope, but must never replace it with the current
page or HQ. Cancellation stops new reads; a losing revision/save must not report
stored attribution or redirect into a replacement Session.

## Provider boundary

`assistant_workspace_contract_test.go` executes real Codex/Claude Code adapters
against controlled executables in temporary HOME. Existing vendor conversion
and Codex protocol tests cover request/response shape. These are deterministic
adapter checks, **not real vendor responses**.

- OpenAI/Claude/Gemini and local HTTP providers declare host tool support.
  Expose only the explicit read allowlist where the selected adapter supports it.
- Codex supports Ori-brokered calls: tools are prompt/schema data; Ori validates
  and executes calls. Native MCP is separate.
- Claude Code declares `SupportsTools=false`. Keep a validated overview and
  review controls, explain deeper-read limitations, and send no unavailable
  tool definitions. Its text-only posture uses tools empty / dontAsk.
- Both CLI adapters treat **nonempty ChatRequest.WorkspaceID as native execution
  opt-in**, even without MCP servers. Keep WorkspaceID, WorkspaceDir, MCPServers
  and ExecutionScope empty on this panel. Display/attribution IDs belong in the
  separate context DTO. Do not change providers, dual gates or models as a fix.
- `runModel` now makes advertisement capability-aware while preserving empty
  native-execution fields. Keep four tool rounds and final tool-free synthesis.

Configured-model checks require a separately selected authorized sandbox model;
credential presence alone is not a decision to change the application's model.
Live-model evidence remains separate from inspecting providers and browser mocks.

## Minimal context/evidence/persistence seam (proposed)

Use a small data-only package (e.g. `internal/assistantcontext`) shared by HTTP
and Session adapters. Do not import HTTP handlers into chat/workspace packages.

- Versioned browser references: page path/surface, explicit workspace ID versus
  slug, selected task. Browser names/descriptions/permissions are ignored.
- Server context: typed location and subject (ID, safe name, kind, breadcrumb,
  record version, updated/read times), source availability and bounded counts;
  separate relationship owner and review destination remain server-controlled.
- Availability: available, empty, unavailable, denied, unsupported, partial,
  historical; unavailable never means empty. Record safe reason codes and
  duration/count/truncation only, never source bodies or unrestricted paths.
- Review projection: canonical offer lookup after conversation validation;
  subject/operation/destination/effects/status/blocker/valid control labels.
  Do not send executable references/digests to the model. Failed state lookup
  is state unavailable, not no review. History cannot reactivate an old offer.
- Bind destination identity, relationship and compatibility/version witnesses
  into review validation/digest, not just display lines. Renames and material
  effects need refreshed disclosure; navigation alone does not retarget review.
  Creating a Home retains the exact reviewed creation intent and records the
  resulting parent identity in its canonical receipt.
- Evidence ledger per turn: server-issued reference keys, exact workspace/source
  identity, safe label, current version/hash/update/read time, preview/full/
  partial coverage, ranges and continuation. Model citations resolve only against
  evidence actually delivered for that turn. URL/path prose is never a citation.
- Source links reuse `/notes/{id}`, workspace slug + Ticket query, and contained
  file routes where safe. Directory content currently has API/Files-explorer
  surfaces; use a narrowly authorized source detail if no safe deep-link exists.
  Historical references are labels/evidence, not grants to fetch current bodies.

Add optional bounded `turn_context_json` to **canonical Messages** via the
numbered database migration mechanism (implemented by migration 77). Keep
Go Message metadata out of generic JSON input. A dedicated internal append seam
must atomically store user/answer attribution after owner/revision validation;
extend existing folder-turn transaction rather than adding an independent write.
Invalidate hybrid caches on success/failure, preserve order, FTS/retention and
session deletion. Generic/imported input cannot author fresh metadata. Imports
may retain only explicitly historical projections; old text-only turns show
unknown scope. Relationship/HQ replacement never upgrades history into authority.
Store bounded references/versions, not retrieved source bodies or a second memory
store. Saved answers may quote evidence under existing conversation behavior.

Readers should share small canonical data/parser functions, **not** Tools() or
runtime prompts (those include writes, delegation, agent management and auto-save
instructions). Per-reader errors degrade that source only. Secrets, managed
knowledge files and source instructions remain filtered untrusted data. Existing
path-returning helpers alone do not prove race-safe open: group 5 must validate
root/reference/file identity at actual open/read and test substitutions.

## Budgets and latency

Retain PRD defaults: 12,000 Unicode characters for overview, five previews per
category, 64,000 aggregate evidence characters including overview, 40,000 per
file chunk, existing parser byte limits, 500 directory entries/three levels,
four tool rounds. Enforce before provider input, across all calls in a round.
Any truncation/continuation carries precise coverage. The initial metadata
registry bounds previews and tool-result accumulation; the complete cross-reader
ledger including all initial snapshot evidence remains a group-4 requirement. A directory listing's
readability check currently sniffs file bytes; do not copy it into panel-open or
greeting discovery and claim no content was read.

The baseline has **no viewed-workspace preparation path**, so its overhead is
zero by omission, not a meaningful success against the 250 ms target. Compare
new overview preparation to a warmed canonical-read fixture (group with two
projects, selected project containing 100 tasks); measure 200 samples, report
p50/p95 separately from model, parser and tool work. New overview fixtures add
representative note/file counts without content reads. No broad performance
refactor or budget revision is authorized by an unmeasured assumption.

The current non-race warm fixture (same 100 tasks, 200 samples, one warm-up
excluded) measured canonical project+parent p50 **178.625 µs**, p95 **321.333 µs**;
full bounded resolver/projection p50 **616.292 µs**, p95 **964.209 µs**. This is
local context preparation only, excluding provider, profile/memory, deeper reads
and parse latency. Concurrent race-instrumented checks are not this performance
comparison. The provisional 250 ms p95 target is met for this fixture; later
reader/large-workspace measurements are still required.

## Companion finding

Host-only source implementation is sufficient: reviewed REAPER blueprint v10
uses typed project entry + `.rpp` existing/new connection, requiring Music-owned
Home schema/version 1. Music Home schema 1 reciprocally allows exact REAPER
project-team schema/version 1. `projectconnection.RecordAttachedProject` creates
a purpose-empty workspace-owned directory reference and typed exact locator;
`ResolveProjectEntry` rechecks identity/containment without executing the plugin.
No blueprint, manifest, SDK, permission, version-floor or release change is
identified for this feature. Do not mistake a similarly named old checkout for
that reviewed contract.

Source-protected staging uses `scripts/reaper-demo.sh` with explicit source
arguments. Its build helper now runs only in a temporary Git export, not the
companion checkout; fake-only success/failure preservation tests run with
`python3 scripts/reaper-demo.test.py`. The exact REAPER artifact-only build also
passes locally without changing the external source. This is not installation
or published-release verification.

The exact candidates have now been installed/enabled **only in disposable
baseline state**, with a real Music Home and recognized REAPER review. This does
not prove the user's installed version, project-entry reading, completed setup,
real vendor-model behavior, live REAPER or release verification. Tasks 3.5, 5.5
and 6.3 still require their distinct integration/acceptance cases. No companion
source edit, real-environment installation, publication or floor change occurred.
