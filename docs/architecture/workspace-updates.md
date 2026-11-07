# Workspace messages and filing updates

Workspace updates are two independent saved facts: an **unread lead welcome**
and **files awaiting review**. They are not ticket attention, library proposals,
ordinary chat notifications, or a new work scheduler.

## Delivery and migration

`internal/workspaceupdates` owns bounded host bookkeeping. Successful eligible
creation explicitly enrolls the current owner/workspace/unique lead instance.
Delivery waits for the existing saved wizard-ready gate, or successful creation
when no wizard applies. The shared `Deliver` boundary also serves the existing
`POST /api/workspaces/{id}/lead-conversation/open` on-entry fallback.

The composer uses saved setup/assessment facts, not inference or a folder scan.
`EnsureLeadConversation` commits the ordinary chat message, suppression/delivery
receipt and exact update identity together. Failures roll back all three. There
is one introduction and at most one existing eligible later saved assessment;
user conversation and suppression rules still apply. Later batches do not append
repetitive welcomes or rewrite history.

Migration 088 adds `workspace_welcome_enrollments` and
`workspace_welcome_messages`. Revisions 1 and 2 identify those two possible host
messages, not arbitrary event cursors. Historical messages/receipts are preserved
without enrollment or unread backfill. Imported/legacy workspaces are not swept
into new conversations; an eligible legacy workspace without a receipt retains
its quiet on-entry fallback. Existing filing work remains discoverable.

Enrollment binds recovery to the original identity. Startup/coalesced hints and
a 30-second worker visit at most 32 enrollments per keyset pass. Recovery rereads
saved readiness and retries bookkeeping only—never provisioning, preparation,
scanning or model work. The reset WorkGate covers admission through commit;
shutdown joins the worker before closing stores. Both new tables are in reset's
application-record inventory, not portable workspace authority.

## Independent sources and authority

SQLite owns workspace identity/version/work; folder records supply canonical
setup and grant metadata. Ordinary folder scaffolding can advance independently;
ready-wizard mirrored writes remain version-fenced. Missing folder facts do not
mean setup is absent.

Welcome authority requires the exact current owner, unique lead instance, saved
session and message. Display names are not identities. Moved/deleted history,
replacement leads and foreign/trash/missing workspaces cannot adopt old receipts.

`filejanitor.ReadFilingReview` independently validates the current owner,
capability install, saved managed-root/grant/reference and batch. It uses
`LatestPendingBatch`, the shared scope verifier and `SummarizeBatch`, just like
Review. It does not require a Curator or live conversation, inspect file content,
probe the managed directory, or use welcome prose/cached counts as authority.
New scans save optional root/install provenance; legacy batches remain compatible
but a later explicit reinstall cannot adopt them.

Single-workspace summaries recheck primary ownership/version after both reads,
with one bounded retry for a benign version change. Aggregate reads select at
most 101 IDs, project at most 100, and recheck the page's current owner/lifecycle/
version in one bounded query. Transferred/deleted rows lose authority; an unreadable
still-owned row remains unavailable rather than disappearing as a false zero.

## HTTP contract

All routes use the host's trusted current-user provider and `Cache-Control:
no-store`. Query/body owner claims never select authority.

| Route | Behavior |
| --- | --- |
| `GET /api/workspaces/{id}/updates` | Independent `welcome` and `review` projections plus canonical ID/slug/label and observation time. Inert: no delivery, acknowledgement or work. |
| `POST /api/workspaces/{id}/updates/seen` | Closed JSON `{session_id, message_id, revision}` acknowledges only that current delivered message. Server time, monotonic/idempotent; returns the current summary. |
| `GET /api/workspace-updates?after=<id>&limit=100` | Explicit ID-keyset page: `{items, has_more, next_after?}`; limit 1–100, default 100. `after` is navigation, never seen/delivery authority. |

Welcome states are `ready`, `not_applicable`, `unavailable`. A ready zero can mean
no receipt or a suppressed delivery; a missing previously delivered destination
is unavailable. Review additionally distinguishes `unconfigured`. Ready counts
include `pending_count` and `needs_review_count` with safe install/root/batch
identity and times. No filenames, absolute paths, content, grants, approval tokens
or raw errors are exposed by these projections. Unknown sources are not zero.

Malformed/unknown/duplicate query fields, unsupported methods, unknown seen body
fields and stale/forged identities are rejected. See the API reference for
response codes. A read failure may retain explicitly qualified last-known labels;
those labels cannot authorize an action.

## Presentation and exposure

The workspace's existing Next action owner retains ticket, repair/setup/consent,
running-work and completed-result priorities. A saved review supplies **Review
files**, with an independent **Ask [lead] · New message** when available. A missing
chat source must not hide valid review. **Source details** separates batch
completion, observation, delivery and exposure times. Safety copy remains visible
without opening the details.

Delivery/summary refresh never opens chat, selects a recipient/session, moves
focus/scroll or discards a draft. Explicit message navigation waits for recipient
readiness, revalidates exact saved identity and uses the existing busy/draft/New/
creation guards. Stale destinations report unavailability, never another thread.
Review navigation reopens current Review without stale preview/approval authority;
live selections and confirmations remain with the console's owner.

Only the matching open, visible, focused, settled, unblocked transcript can send
seen acknowledgements. Its saved message beginning and `min(height, 80px)` must
be inside the transcript viewport and intersect it horizontally. Hidden hydration,
scroll-away, another session, a modal, an active reply or a compact preview is not
exposure. This records visibility, not comprehension. Reading chat or opening
Review does not decide any files.

Home Updates, Map and Tree share `workspace-updates.js` and the same presenter.
Messages/files never inflate the existing ticket badge or library counts. Map
markers use the tile's existing pointer/menu owner (including Shift+F10), not
nested controls; Tree links participate in the row's keyboard order. Refresh
patches markers in place and preserves camera/drag/selection/menu/focus.

The in-memory cache retains at most 500 settled observations, with current-page
and pending-operation retention. There are no localStorage unread counters.
Continuation is explicit; partial/empty pages never claim whole-tree all-clear.
A hinted cached workspace outside the selected page becomes qualified last-known
without fetching other pages; an older pending ack cannot restore its authority.

Cross-tab BroadcastChannel messages contain only `{kind, workspace_id, source}`.
They request canonical rereads, never supply counts or authority. Visible active
Home rereads only its selected <=100-workspace page once per minute; a workspace
uses 30-second recovery. Focus/return/back-forward/source hints coalesce. Channels,
timers and subscriptions dispose/suspend with lifecycle; reset generations reject
old responses. Current runtime identity is local-user scoped; any future in-page
account-switch owner must reset this cache too.

## Validation and exclusions

Unit/HTTP/race suites cover migration, exact acknowledgements, owner/identity/root
fences, bounded queries and reset/drain. Browser specs are
`workspace-updates*.spec.ts`; use the isolated goal wrapper's `--lead-welcome`
mode, and its `--upgrade` mode for `workspace-updates-restart.spec.ts`. The latter
runs the same sandbox under different actual server PIDs. The folder-fed journey
runs in `workspace-updates-creation.spec.ts` against a fresh
`scripts/demo-inbox-folder.sh` sandbox (`ORI_INBOX_SANDBOX`), using the current
deterministic folder-hire path with no model configured. The older scripted
`file-janitor-creation-flow.spec.ts` has a separate explicitly enabled runtime;
its declined-offer fixture is not the current inbox-hire creation journey.

Accessibility checks exercise Home/Tree/card, Map keyboard navigation, both
themes, 320–1440px/touch and zoom-equivalent reflow. Map's decorative pseudo-grid
prevents axe resolving its background; opaque label foreground/background contrast
is checked separately. Browser fixtures are not VoiceOver/native-picker
certification. No push/email/sound, notification preferences, automatic approvals,
new folder permission, automatic work or external plugin dependency is introduced.
