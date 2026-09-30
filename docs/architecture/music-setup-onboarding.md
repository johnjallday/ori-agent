# Music setup onboarding: state and consent contract

Status: implementation contract for the `music-setup-onboarding` feature. It
freezes what each onboarding state means, who owns its truth, what the user may
do next, and what cancelling or losing it costs. It adds **no new authority**:
every consequence stays behind the existing, separately reviewed service
operation. Read alongside `project-library.md`, `independent-program-homes.md`
and `host-setup-quests.md`.

The journey this makes continuous:

> Choose Albums → review Home/library discovery → see discovered projects →
> select a song → review its exact file and connection → separately approve any
> project staffing → open the child or return to the same Home.

## Rules that hold in every state

1. **Navigation is not authority.** A query parameter, `sessionStorage` value,
   route, or opaque offer/entry ID only says *where to look*. It never carries a
   path, a root grant, a review token that authorizes a new action, or consent.
   The server re-derives owner, Home, provider, source identity, and committed
   outcome from canonical records on every read.
2. **No path crosses the browser.** Native-picked paths live in server process
   memory (`FolderDigestService.paths`, `pathselection.Store`, `Roots.pending`)
   until the existing explicit root/project approval. This feature does not
   lengthen the 30-minute windows or persist an unapproved path to avoid a
   legitimate re-pick after a restart.
3. **GET creates nothing.** Opening a page, reading a continuation projection, or
   restoring a tab never initializes a library, grants a root, scans, creates a
   child, staffs, changes mode, or changes Home membership.
4. **One review, one consequence, one cancel point.** Each existing operation
   keeps its own review and confirmation: initialize, root grant, scan, project
   connection, staffing, mode, association, integration install. A guided surface
   may *sequence* them but never merge them into one confirmation or prebuild a
   later review.
5. **Per-action revision rule.** `projectlibrary.Document.Revision` is a single
   counter incremented by every mutation, including inert reviews and scan
   start/finish. A commit requires `review.Revision == doc.Revision` and an
   unchanged provider revision. Therefore: obtain the *next* review only after
   `refresh()` has read the committed (or cancelled) revision of the previous
   step, and always refresh after a cancelled review because the review itself
   advanced the counter. A stale-revision `ErrConflict` is recovered by refreshing
   and re-reviewing, never by replaying the old token.
6. **Completed consequences are kept.** Cancellation, reload, restart, or a
   provider loss never rolls back, repeats, or duplicates a committed root,
   scan, child, link, profile, or association. Recovery reads the existing
   receipt (idempotency key, scan receipt, pending link) before proposing
   anything new.
7. **Truthful, independent outcomes.** "Discovered", "connected", "staffed",
   "in File-only mode", "model ready", and "live access ready" are separate facts
   with separate owners. A catalog-only record is never reported as linked or
   staffed; connecting a project never staffs a role.
8. **Source files are read-only.** No move, copy, rename, edit, or deletion of a
   user's project files at any step.

## State table

`Owner` is the canonical source of truth and receipt. `Target` is the exact
object the action affects. `Cancel` is what declining costs. `Recovery` is how
the journey resumes.

### S1. Selection still valid (collection chosen, nothing committed)

- **Owner:** `FolderDigestService.paths[offerID]` + offer sidecar (`FolderKey`,
  `RootIdentity`, never the path). Portfolio hand-off adds a 30-minute window from
  `ResolvedAt`; a scoped `pathselection` token adds its own 30 minutes.
- **Target:** the one selected collection directory, verified by directory
  identity before and after the scan.
- **Next explicit action:** "Create Music Home" (no Home) or "Add collection" to
  an existing Home (S2b). The already-valid selection is reused; no second native
  chooser opens.
- **Cancel:** nothing is granted; the offer stays decidable (Later/No).
- **Recovery:** see S0 for expiry/loss.

### S0. Selection lost, expired, replaced, canceled, or picker unavailable

Distinct outcomes, each with its own copy and the same safe next step (choose the
folder again). None implies a committed grant needs re-picking. Today every one of
these collapses into the single "The original selection expired or changed"
prompt; task 1.7 separates them. The "Detectable how" column names the evidence
that exists in code today; where a cause cannot be told apart from another with
current server errors (e.g. expiry versus restart both surface as
`ErrFolderPathLost`), 1.4/1.7 must add a distinguishing server signal rather than
guess in the browser.

| Cause | Detectable how | Message says |
|---|---|---|
| Chooser canceled | picker returns cancel | nothing changed; choose again when ready |
| Picker unavailable | `ErrUnavailable` from picker | this environment cannot open a native chooser |
| Expired | `PortfolioRoot` window from `ResolvedAt`, or the scoped token TTL, elapsed | the selection expired after 30 minutes |
| Lost to restart | offer sidecar persists but `paths[offerID]` is absent | Ori restarted, which clears unapproved selections |
| Replaced directory | directory identity mismatch | the folder at that path changed |
| Provider lost | provider revision/state changed | the Music package changed; review it again |
| Stale review | `ErrConflict` | Home changed while you were reviewing; refresh |
| Home unavailable | verified Home lookup fails | the Home could not be read |

- **Cancel/Recovery:** re-picking preserves the existing Home and every committed
  library result. A tab reload alone is **not** a lost selection: while
  `paths[offerID]` and the offer are valid, the continuation is offered again.

### S2a. Home created, library not initialized

- **Owner:** the Home workspace + reviewed Home provider; library document absent.
- **Target:** the exact owner-scoped Home key/provider.
- **Next action:** "Review library setup" → initialize review (`ReviewInitialize`).
  Existing-Home initialization discloses saved-note / exact-link migration effects.
- **Cancel:** library stays absent; Home unchanged; offer still resumable.
- **Recovery:** continuation projection (below) points back to this step; the
  guided surface does not auto-initialize on load.

### S2b. Existing Music Home, another collection

- **Owner:** `reviewedHomeExists` (owner + plugin + program) and the Home library.
- **Decision:** the suppression that hides the portfolio card for an existing Home
  stays (no duplicate Home). Instead the folder offer gains a separate reviewed
  **"Add this collection to <Home>"** continuation bound to the *exact* existing
  Home, using the same server-held selection. It does not create a Home, root, or
  scan by being offered.
- **Implemented as:** the scan keeps the collection evidence and sets
  `portfolio.existing_home` when the Home can be re-read canonically
  (`reviewedExistingHome`: owner-scoped station + provider identity + group row;
  no creation-time proof, no release resolution). If it cannot be read the offer
  falls back to the plain suggestion. The card asks "Add this collection to your
  Music Production Home?" and says nothing is installed or created. Confirming
  calls `POST …/offers/{id}/existing-home` with only a `request_id`;
  `ResolveExistingHome` re-reads the Home itself (the browser names none) and
  records it as the outcome (`existing: true`). The just-created-Home route
  `ResolvePortfolio` refuses such an offer outright, so the two never cross. From
  there `PortfolioRoot`, `pick-offer` and the continuation projection work
  unchanged.
- **Next action:** if the same root is already approved → offer that root's scan
  review; else → root review for the new collection. `pick-offer` reports
  `existing_root_id` (via `Roots.ApprovedRootFor`, the same active-root rule
  `Commit` refuses a duplicate by) so the guided flow goes straight to that root's
  scan review instead of a root review whose commit would be refused.
- **Cancel:** no root, no scan; the generic "set up a workspace" option remains
  available and unchanged.
- **Recovery:** a lost selection follows S0; an unreadable Home falls back to the
  existing plain offer (fail closed).

### S3. Root approved, not scanned

- **Owner:** library document root + its commit receipt.
- **Target:** the exact committed root ID and its identity.
- **Next action:** scan review (`scans/review`) obtained *after* refreshing the
  post-commit revision.
- **Cancel:** the committed root is kept; the user resumes at the scan review
  without another folder pick.
- **Recovery:** on reload, read the root and any existing scan receipt; an
  uncertain scan reply is reconciled from the exact scan receipt before offering a
  fresh scan.

**Guided presentation (implemented).** The Home page carries one *setup card*
directly under the hero, computed by `setupNextStep` from the library's own
state: not initialized → "Review library setup"; initialized with no usable root
→ "Review folder connection" (names the carried collection when known); a
connected root with no scan → "Scan <folder> once"; only unfinished scans →
"Review scan again"; provider read-only → an explanation with no action. A root
with a finished (complete or partial) scan is an established Home and shows no
card. The card only names the next step; its button opens that step's own review,
so no consequence is merged or pre-confirmed. While it shows, the hero compacts
(name, stage and level remain). A `#projectLibraryPanel` arrival focuses the card
(else the shelf heading) once, including on an uninitialized Home, and never again
on later refreshes. The sequence after a root grant goes straight to the scan
review of that root — the separate "Scan the folder now?" question is removed
because the scan review is itself the disclosure and cancel point; cancelling it
keeps the root and the card offers the scan again with no new folder pick. Each
next review is requested only after `refresh()` has read the previous step's
committed revision.

**Resilience (implemented).** A failed re-read keeps a previously loaded library on
screen and says the current check is unavailable; a first-load failure still hides
it. An uncertain scan commit (no answer, or a 5xx) is replayed once with the same
review token and idempotency key, which the server treats as a replay and never as
a second scan; if that also fails the browser re-reads the Home and reports a scan
only when the root's recorded last scan differs from the one before the review.

### S4. Scan complete or partial

- **Owner:** saved scan session + digest counts (`digest.go`, `query.go`).
- **Target:** the scan's own coverage record.
- **Implemented counts:** the persisted scan digest carries `projects`, `new`,
  `connected` (songs already holding an exact project link), `activatable` (can be
  set up), `needs_file_choice` (the part of `activatable` whose folder holds several
  candidate files), `unsupported_format`, `unavailable`, and `coverage`. Each song
  is in exactly one of connected / can be set up / unsupported, and a file choice
  is a subset of "can be set up", so the parts never exceed the projects found;
  digests stored before these fields read them as zero and stay valid.
- **Next action:** browse/search rows, select a song. Counts distinguish *found
  catalog records*, *verified connected workspaces*, *needs file choice / other
  blockers*, *unsupported formats*, and *coverage* (complete vs partial). A pending
  association is not double-counted; an installed integration does not imply every
  row is ready.
- **Cancel:** n/a (read state). A failed refresh keeps the last populated catalog
  and labels the current check unavailable; it never claims saved data vanished.
- **Recovery:** saved sessions, paging, filters, and last-observed labels persist.

### S5. Catalog-only (unsupported format, or no compatible provider)

- **Owner:** the catalog entry (`projectlibrary` query) and the provider-eligibility
  read (`ActivationInspector.Eligibility`).
- **Target:** one entry; no child exists.
- **Next action:** planning/notes actions for unsupported formats. For a supported
  format with no installed provider: the integration install continuation (S8).
- **Cancel:** entry stays catalog-only. It is never described as linked or staffed.
- **Recovery:** eligibility is recomputed live on every open; revoked/unavailable
  sources, unsupported formats, ambiguous providers, unsupported platforms, and
  stale Home providers stay distinct blockers.

### S6. Song selected → exact file and connection review

- **Owner:** `ActivationService` (review `Review`/`Commit`) over the canonical
  `projectconnection.Service`. Review receipt lives 10 minutes.
- **Target:** one entry → one folder → one authoritative `.rpp` file → one child
  workspace/link. A folder with `Song.rpp` and `Alternate.rpp` is one candidate
  with an explicit file choice; no newest-file guess, no duplicate workspace.
- **Next action:** "Review project setup" → confirm connect. A single observed
  file is pre-filled but still disclosed.
- **Cancel:** no child, link, or mode change.
- **Recovery:** the deterministic child ID makes a repeated commit idempotent; an
  uncertain reply resumes from the exact receipt.

### S7. Child connected, not staffed

- **Owner:** the child workspace + reciprocal Home link (`projectconnection`) and,
  for the normal-intake path, the quest run (`project_connect` committed).
- **Target:** the exact child only. Home roster, siblings, saved root profiles are
  untouched.
- **Decision (creator bridge, feeds 4.1):** a connection-only commit must not
  require an agent choice it cannot canonically staff. The bridge (which today
  posts only `{if_revision, idempotency_key, review_token, input}` and drops any
  team fills) defers staffing to the single later staffing form and states exactly
  what is and is not committed. Ordinary blueprint creation, strict reviewed team
  seeding, saved-agent binding, and standalone/non-music creators keep their
  current contracts.
- **Decision (continuation binding, feeds 4.2):** the library activation `runID`
  is a random ID used for the deterministic child scope; it is **not** a
  setup-quest binding. Continuing from a library-created child to staffing requires
  a host-verified binding: owner, installed declaration/provider, child/mirror
  identity, selected file/mode, and the observed creation receipt are re-read and
  checked before any quest/staffing scope opens. A browser-supplied child or run ID
  never marks a quest step complete, replays a creator, widens a resolver, or
  rebinds old role snapshots. Only the minimal recoverable host binding/receipt is
  persisted.
- **Next action:** staffing review for the installed blueprint's required
  project-local roles (canonical create/bind profile choices), then separate
  confirmation.
- **Cancel:** creates no profile, binding, or grant; the child stays connected and
  unstaffed and is honestly labelled so.
- **Recovery:** resumed from receipts after reload/restart. Storage loss cannot
  recover unconfirmed permission, recreate a child, or duplicate a profile.

**Implemented (S6/S7).** *Connection-only creator.* The setup quest's workspace
creator opens with `connectionOnly` and `stayAfterCreate`. `connectionOnly` makes
`usesTeamRosterCreator()` false, which is the one switch every Team gate reads, so
the wizard has no Team step, collects no agent choice, and sends none (the journey
commit never staffed a role anyway: the Team choice was a pure gate whose result
`SetupWorkspaceCreator.submit` dropped). Its steps are `[2, 4]` (Details, Review;
the quest preselects the blueprint), numbered by the wizard's own chrome, and the
Review step states that nothing is created for the team now. Ordinary blueprint
creation, reviewed team seeding and standalone flows read the same switch as
false and are unchanged (the paired suite's ordinary-creator test is the guard).
`stayAfterCreate` keeps the person on the page: the journey reopens once the
creator has closed, on the project's **team step** (the view "Manage Team and
Extras" opens), so a connection is followed directly by the staffing review, never
by a detour through the child and Today. Staffing remains its own review and
confirmation; nothing about the connection stages it.

*The child's roster.* `GET /api/workspaces/{child}/roles` used to list the
**Home's** roles for a linked child (the station declaration) and none of its own.
It now lists the child's snapshotted project roles (project scope only) and only
its own bindings, matching the assistant summary; the Home page, a joint station
and a link with no snapshot are unchanged. This is the canonical roster the child
page's team form (`PUT /api/workspaces/{child}/roles/{role}`, no quest run needed)
reads.

*Library-created children (no persistence, no migration).* The library's
activation `runID` is a random id used only for the deterministic child scope, so
it is not a setup-quest binding, and this feature adds none: nothing is fabricated
into a run from a browser child id, no creator is replayed, no resolver is widened.
The connected dialog resolves the child's real page and mode from one canonical
workspace read. A child that has chosen a mode gets a "Set up the project team"
link that requests its team form by role (opened once, only for an empty,
writable role); a child that has not (the library does not select a mode) is
linked to its page, which opens its own mode wizard first — File-only is the
starting option — and the wizard's end returns to the page, whose "Fill the
primary role" banner opens the role's own form (nothing auto-opens it; the form
names the role and "this workspace only" and creates nothing until submitted).
Workspace pages are routed by folder slug:
`/workspaces/<id>` is a 404, and every library link to a child used the id until
this was fixed, so no link is offered when the slug cannot be resolved.

### S8. Integration missing or disabled for a selected song

- **Owner:** the reviewed integration registry (`reviewedintegration`) and the host
  install quest/adapters.
- **Target:** the exact owner/Home/catalog entry (and queue position when queued),
  carried as a bounded host-projected return binding.
- **Next action:** reviewed install/enable with source, version, artifact/trust,
  and platform disclosure, shown in the current guided context. The install quest's
  generic follow-on must not connect the song by a second creator.
- **Cancel or failure:** return to the *same* song, re-read provider/root/file/
  ownership/Home state, then offer a fresh project review. Installing creates no
  child, restores no revoked root, and launches neither REAPER nor live control.
- **Recovery:** tampered or foreign return targets fail closed; provider removal
  or replacement mid-review invalidates old reviews; existing Home/package
  replacement guards stay enforced.

**Implemented (S5/S8).** Eligibility can only say `project_provider_unavailable`;
it cannot tell "not installed" from "installed but disabled" and knows nothing of
platform. So the host adds an inert `integration_offer` to the activation read,
computed server-side only when (a) the state is `project_provider_unavailable`
(every earlier blocker — revoked or unavailable source, Home provider, ambiguous
provider, folder owner, existing link — returns first and never gets an offer),
(b) the observed format maps, through the one host-owned discovery table
(`folderdigest.IntegrationKeyForProjectFormat`), to a reviewed integration (a format
no reviewed integration supports maps to nothing), (c) that registry entry is
release-ready, and (d) this platform is in its `SupportedPlatforms`. The offer
carries the registry key, the generated install quest ID and a display name,
never a path.

The Home page has no setup modal, so the song's dialog sends the person to the
existing deep link for the reviewed install quest and records a **navigation
hint** (`library-return.js`, per-tab `sessionStorage`): Home ID, opaque song ID,
quest ID and the Home's own page path, valid for one hour and re-validated on
every read (a query, hash, foreign origin, dot segment or other route is "no
hint"). The hint never installs, connects or grants anything. In the quest, the
summary's follow-on returns to the song **only while the open quest is the one the
hint names**, instead of opening the plugin's own project creator (a second,
unrelated way to create a workspace for that song). On the Home, the panel
consumes the hint once for this exact Home, re-reads the song from the server, and
reopens it with fresh eligibility. No child is created, no revoked root is
restored, and nothing launches a project application or live control. A queued
song that needs the integration pauses the queue in place (same song, no
progress, no held review token) and starts the same review.

### S9. Pending shelf association (normal-intake child linked, not on the Home shelf)

- **Owner:** `link_association` / `portfolio_bridge` exact pending link.
- **Target:** one linked child and one Home shelf entry; link-only, no discovery
  grant, no scan.
- **Next action:** optional guided association review after staffing.
- **Cancel:** adds no entry. Continuing setup never silently associates, grants a
  root, or creates another child.
- **Recovery:** existing exact-link/source matching and pending-link
  reconciliation. A managed scaffold, foreign/broken link, changed selected file,
  or divergent mirror stays refused under existing policy.

### S10. Mode, model, and live readiness

- **Owner:** workspace mode/`workspace_setup` (File-only is the starting mode),
  model availability projection, and the separate optional live-access setup.
- **Next action:** none required for file-only work. Live access remains a
  distinct, later, separately gated setup.
- **Cancel/Recovery:** a previously confirmed mode is preserved on resume, never
  reset. Missing model credentials or a non-running DAW never erase the catalog,
  block manual library use, or force live setup; they are labelled as their own
  states.

### S11. Provider unavailable / read-only Home

- **Owner:** Home provider state + `Document` provider revision.
- **Next action:** the Home stays readable; consequence actions are disabled with
  the specific reason.
- **Cancel/Recovery:** re-read on the next open; no action runs against a stale
  provider revision.

## Owner-scoped continuation projection (feeds 1.4)

A bounded read that answers *"what can this owner continue?"* from canonical
evidence only: the valid offer (or its committed outcome) and committed root
receipts. It returns opaque IDs and states (S0–S3), never a path or the raw library
document, and grants nothing. It rechecks owner, Home key/provider, source
identity/TTL, and committed outcome on every call. A foreign, malformed, or
missing offer ID yields "nothing to continue", not an error that reveals another
owner's data. Ambiguous evidence (more than one possible continuation) is listed,
never guessed.

Implemented as `FolderDigestService.PortfolioContinuations` behind
`GET /api/personal-assistant/folder-digest/continuations?home_id=`. It shares one
classifier (`portfolioSource`) with `PortfolioRoot`, so "ready" can never disagree
with what `pick-offer` will accept, and the S0 reasons are produced server-side:
`expired` (window elapsed, checked first), `lost` (path not held: restart), and
`changed` (directory missing or no longer the same). Decision: it does **not** call
`VerifiedHome`. That check resolves the newest reviewed release, which is both
heavier than a page-load read and wrong for a continuation (a Home pinned to an
older reviewed release would look unavailable). Owner, Home identity and provider
were verified when the offer resolved and are stored in its outcome; the projection
matches that outcome exactly, and the library's `roots/pick-offer` plus root
review/commit re-verify the Home, provider and folder identity before any
consequence. Committed-root awareness ("this root is already approved") belongs to
task 1.6, not this projection.

## Explicitly untouched

No Music/REAPER manifest, blueprint, packaged prompt, schema/version, reviewed
pin, or release change; no live runtime or DAW launch; no real user-state
installation. Old children with missing role provenance keep using the existing
separate reviewed repair path; this feature never silently rebinds or repairs
them.

## Evidence

Findings behind this table are recorded in
`tasks/tasks-music-setup-onboarding.md` under "Spike findings (task 1.2)".
Fake-picker/chip and headless-browser results are labelled as such; the real
native chooser, model, and live-DAW behaviour are recorded separately and remain
NOT RUN until exercised.

### What the journey spec proves (and does not)

`tests/music-setup-onboarding.spec.ts`, run by
`scripts/music-home-demo.sh test --suite onboarding --provider reviewed`, drives
the whole path in a disposable HOME on **published** releases: chip-chosen
collection → setup-first Home → root and scan reviews → per-song blockers (a
format with no reviewed integration is offered none) → leave the reviewed REAPER
install without confirming (nothing installed) → confirm install, then enable (two
separate reviews; installed is not enabled) → "Back to your song" → exact file
choice → one connected child → the child's mode wizard → the role's own form. It
hashes every source project file before and after and counts workspaces.

Not proven by it: the native macOS folder dialog (chip only), a REAPER launch or
live control, any model call (no credentials are part of the fixture), the
paired local-candidate install (the guidance suite in both install orders covers
that, separately), and screen-reader behaviour. The `portfolio` (local
candidate) suite's refusal assertion for a same-name local provider expects the
text "Home provider is unavailable" but the server answers "The workspace could
not be linked to the folder right now" on that path; the handler and provider
preview code are untouched by this branch, so treat it as pre-existing until
shown otherwise.
