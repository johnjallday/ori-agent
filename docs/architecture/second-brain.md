# Connect your second brain

The user's own notes — an Obsidian vault or any folder of Markdown — are
connected read-only to the Personal HQ as a source the personal assistant
reads. Right after the HQ is built, the assistant's first message asks for
them, and within a minute it comes back with one headline and one action: what
is open in the notes, and a tap that turns those open items into HQ follow-ups.

There is no project workspace for the notes, no agent with access to them, and
no tool over them in the assistant's chat. The look service is the only reader.

## Where it lives

| Piece | Code |
|---|---|
| Vault detection, bounded reader, extraction, the record's schema, the look, follow-ups, the headline prompt | `internal/secondbrain/` |
| The HQ sidecar store (knowledge-store discipline) and the service | `internal/personalassistant/second_brain_store.go`, `second_brain_codec.go`, `second_brain.go` |
| The learned fact | `internal/personalassistant/knowledge_second_brain.go`, `internal/server/personal_assistant_second_brain_learning.go` |
| Home Today rows | `internal/personalassistant/today_second_brain.go` |
| HTTP | `internal/personalassistanthttp/second_brain.go` |
| Composition | `internal/server/second_brain.go`, wired beside the folder digest in `builder_dailybrief.go`; missions and Today in `builder_progression.go` |
| The drawer turn | `internal/web/static/js/modules/personal-assistant-second-brain.js`, `templates/components/personal-assistant-second-brain.tmpl` |
| HQ map Notes station | `workspace-command.js` (`notesStation`) |
| Profile Sources row | `personal-hq-sources.js` (`notesSourceView`) |
| Fixtures and demo | `internal/testutil/vaultfixture/`, `scripts/second-brain-demo.sh` (`--create`, `--empty`), `scripts/second-brain-legacy.sh` (an older setup), `scripts/second-brain-age.sh` (a setup made days ago), `scripts/demo-inbox-folder.sh --recurring-names`; `scripts/second-brain-e2e.sh` runs the Playwright specs on their own sandboxes |

## Detection

Obsidian lists the vaults it has opened in its own config, read as metadata
only: `~/Library/Application Support/obsidian/obsidian.json` (macOS),
`%APPDATA%\obsidian\obsidian.json` (Windows), `~/.config/obsidian/obsidian.json`
(Linux). Every location derives from the server's own `HOME`, so a sandboxed
server only ever finds its sandbox's list. A missing, unparsable or oversized
(over 1 MB) file means "no vaults"; one malformed entry drops only that entry.

A vault is offered only when its path is itself a directory (not a symlink),
passes `folderpath.Check` (the typed-path rules: not a whole drive, not the
home folder, not inside Ori's data directory, no credential folder), and holds
a real `.obsidian` directory. Vaults are ordered by Obsidian's `ts`, most
recently used first, and named by Ori's own id (`v_` + a hash of the canonical
path); Obsidian's key never leaves the server. The result is cached for five
minutes. Nothing runs on a timer.

## Reading (bounded, read-only)

`secondbrain.Read` lists `.md` and `.markdown` files to depth 6, skipping
`.obsidian`, `.trash`, `.git`, `node_modules` and every hidden entry. Symbolic
links are counted and never followed. Bounds: 2,000 files listed, 300 opened
(most recently modified first), 256 KB per file (larger files are skipped and
counted, never read in part), 20 MB in total, 5 seconds. When a bound trips the
read keeps what it has and the headline says "(partial look)". Evicted iCloud
placeholders are counted and never opened, so a look never starts a download.

Every listing and every open goes through an `os.Root` pinned to the folder;
Unix opens add `O_NOFOLLOW|O_NONBLOCK`. Nothing outside the folder can be
reached, even through a link swapped in mid-read, and nothing is ever written.
`TestRead_NeverLeavesTheFolderNeverFollowsALinkNeverWrites` proves it against
a tree with links pointing outside and unreadable files, comparing a byte, mode
and mtime snapshot of the whole tree before and after.

The chooser's check that a picked folder holds notes (`HasNotes`, depth 3)
lists directories only and opens no file.

## Extraction

Per note, without a model: the title (frontmatter `title`, Logseq `title::`,
the first `# ` heading, or the file name); frontmatter or Logseq `tags` and
`status`; open items (`- [ ]`, `* [ ]`, `+ [ ]`, `N. [ ]`, `N) [ ]`, Logseq
`TODO`/`DOING`/`LATER`/`NOW`) with their text (at most 200 characters, read as
rendered: wikilinks become their alias or note name), line and date (Tasks
plugin `📅`, `due:`/`due::`, any ISO date except done/created dates, or "by
<Month> <day>"); done items; wikilinks. Fenced code is ignored.

Derived signals: open loops; decisions (a pending `status`, a line starting
"Decide" / "Need to decide" / "Decision", or a title naming a decision on a
note with open items; a closed status wins); stale notes (open items, untouched
for 60 days); people (wikilink targets that are notes under `People/` or
`Contacts/`, or tagged `person`, ranked by mentioning notes); active notes (7
days); daily notes (`YYYY-MM-DD.md`, Logseq `journals/YYYY_MM_DD.md`).

Lines that look like secrets (`sensitive.ContainsSecretLikeText`) are dropped
before anything else sees them.

## The look

`secondbrain.Looker.Run` reads, extracts, keeps at most 500 open items (within a
300 KB encoded budget), marks the suggested set — dated items first, soonest
due first; then items in decision notes; then items in notes edited this week;
at most 10, with the newest notes' items standing in when none apply — and
writes the headline.

The rules-only headline is "{N} open items across {M} notes, {K} with dates."
When a Default model is set, the headline is asked of it through the same path
as the Daily Brief and email triage, from the extracted signals only: counts,
the top 15 items, open decisions, stale notes, people and active notes. Every
string is folded to one line with angle brackets removed, and the whole block
sits between `NOTES-DATA` markers that the system prompt names as data, never
instructions. The answer is kept only when it is one plain line of at most 160
characters with no link, Markdown or question; otherwise the rules-only
headline stands, and the server logs which rule refused it (never the answer).
An "Also noticed" line names the stalest project and the person mentioned most.

A look always ends finished or failed, never waiting on the user. A failed read
is one sentence ("Can't find {name}. It may have been moved or renamed.") with
Choose again. A running look that this process is not running (a restart cut it
off) reads as failed.

## Storage

One record at `<HQ>/.ori/second-brain-v1.json`, through the knowledge store's
discipline: no-follow directory handle, its own lock, temp file + fsync +
rename, mode 0600, a 512 KB cap, schema and owner checks, unknown fields
refused. It holds the connected source (`id`, `kind`, `name`, `path`,
`connected_at`, `request_id`, `last_look`, and a running or failed `current`
look), the ask's state, and up to 32 request receipts. A look keeps only the
open-item lines (text, relative path, line, date, content hash, the Obsidian
link), the headline, the Also noticed line and the counts. Note contents are
never stored.

## The ask

The ask is the assistant's first message in the drawer after the relationship
becomes active (and once for an install whose HQ is already active), while
nothing is connected and the ask has never been shown. Its receipt
(`POST …/prompted`) is recorded only once the drawer is open while the ask
shows. It replaces the folder hand-over: while the second-brain record can be
read, the folder digest never offers its first prompt, so a declined ask is
never followed by it. The ask offers Connect (or Choose a folder), **Set one up
for me** (see below) and "Not now", which defers (Needs you offers it again after
7 days). "I don't keep notes" is no longer offered; the `none` decision stays in
the schema, so an install that already answered it keeps its declined ask and
skipped mission, and sees no new prompt. Reset Getting Started clears the ask
and the missions, never the connection.

A folder path written in the chat that holds `.obsidian` also offers
**Connect as my second brain** on its folder card, with its own unbound
selection token.

## Follow-ups

Accepting creates follow-ups in `internal/followup` with
`SourceRef{note_item, sha256(source id, relative path, normalised text)}`: the
line as the title, the note's path (and Obsidian link) as the detail, the date
as the due date (end of that day), decision items as needs-decision. The
domain's source dedup means a second acceptance or a later look creates no
duplicate. On a later look, an open follow-up whose line is ticked, marked
DONE or gone is completed with "Done in your notes." The notes are never edited.

## Learned fact

Connecting proposes and approves one `how_you_work` fact under source kind
`second_brain` ("You keep your notes in Obsidian, in the vault "{name}"." or "…
in a Markdown folder called {name}."). Its authority revalidates that the
folder is still the one connected, still exists, and still holds `.obsidian` or
Markdown notes. Disconnecting sends the fact back to review.

## Endpoints

All under `/api/personal-assistant/second-brain`, closed request bodies
(unknown fields refused, any key naming a path or folder refused), every route
— reads included — behind `connectionshttp.OriginGuard`. No endpoint but
`/api/folder-picker/select-path` accepts a path. `request_id` makes every
write idempotent.

| Method and route | Body | Result |
|---|---|---|
| `GET` | none | `{status: not_connected\|connected\|missing, detected[{id,name,parent,path}], source?, last_look?, ask{state, offer_again}, version}` |
| `POST /prompted` | `{request_id}` | status |
| `POST /ask` | `{decision: later\|none, request_id}` | status |
| `POST /connect` | `{detected_id}` or `{selection_token}`, `request_id`, optional `replace` | status and `look_id` |
| `POST /create` | `{selection_token, name, request_id}`: the token names the parent, `name` one folder name; `{request_id}` alone uses the default place | status and `look_id`; sets up a second brain (see below) |
| `POST /capture` | `{text, request_id}` | `capture` `{note, name, link?}`; one new Inbox note, second brain Ori set up only |
| `POST /fill-in` | `{decision: allow\|later, request_id}` | status; the consent card's answer, second brain Ori set up only (see "Filling in after setup") |
| `POST /project` | `{name, answer, request_id}`: `name` one folder name (no slash, not hidden), `answer` the user's own words, folded to one line and refused if empty or secret-like | `{project: {name, rel, link?}}`; writes `Projects/<name>/README.md` and records the question as answered ("The first week") |
| `POST /project/skip` | `{request_id}` | status; records the project question as skipped, once |
| `POST /week/dismiss` | `{request_id}` | status; records that the first-week recap row was put away |
| `GET /look` | none | `{state: running\|finished\|failed\|none, look_id, result?}` |
| `POST /look` | `{request_id}` | Look again (429 within 10 minutes of the last look) |
| `POST /look/{lookID}/follow-ups` | `{items: "suggested"}` or `{items: [id…]}`, `request_id` | `{created, skipped}` |
| `POST /look/{lookID}/dismiss` | `{request_id}` | takes a result off Needs you |
| `POST /disconnect` | `{request_id, if_version}` | status |

## A second brain Ori set up

Amendment for Issue #750. A user with no notes can answer the ask with **Set
one up for me**: Ori creates a Markdown folder the user owns, connects it, and
keeps two of its subfolders up to date. Everything above still holds for it;
this section adds what differs.

### Origin

Each source records `origin`: `connected` (the user's own folder, never written
to) or `created_by_ori`. A record from before the field reads as `connected`, so
a missing value can never grant a write. A connected vault cannot become
`created_by_ori`; granting that later would be a separate, consented change.

### The created layout

| Folder | Purpose | Ori writes? |
|---|---|---|
| `Inbox/` | Captures from chat, follow-ups, unsorted notes | Yes: a new note per capture or follow-up, and a tick |
| `Daily/` | One note per day, from the HQ Journal | Yes |
| `Projects/` | Things with an end date | Only `Projects/<name>/README.md`, one per workspace |
| `Areas/` | Ongoing responsibilities | No |
| `Resources/` | Reference material | No |
| `Archive/` | Finished projects and areas | No |
| `People/` | One note per person | Yes: `People/<name>.md` |
| `What Ori knows about you.md` | The approved facts and the working agreement | Yes |
| `README.md` | How this second brain works with Ori | Written once |
| `.obsidian/` | Minimal vault config so Obsidian opens it | Written once |

"Yes" always means the third write rule below: a new note, a note still exactly as
Ori wrote it, or a tick in such a note. The folder READMEs of `Projects/` and
`People/` are the user's from the moment they are written, and so is every other
README.

### Setting one up

By default the folder goes in the Workspace Directory as `<root>/Second Brain`,
next to `Agents/` and the workspaces, so it travels with the root. The card asks
one question with the place already named, and **Somewhere else…** leads to the
picker. It holds no `workspace.json`, the name is a reserved top-level slug, and
deleting a workspace or Start Fresh does not touch it. Before a Workspace
Directory is chosen the root is a staging folder, so `setup_default` is absent
and the card asks where. Changing the Workspace Directory later leaves the
folder where it is and it stays connected by its path.

`POST /create` with no selection token uses that place and the fixed name.
With a token it takes the parent from the folder picker (so it passes
`folderpath.Check`) and the new folder's name. `secondbrain.Creator`
makes `<parent>/<name>`, which must not exist or must be empty (a lone
`.DS_Store` counts as empty); it writes the layout above, one README per
top-level folder, the root README (it opens with "I'll keep Inbox, Daily,
People, a note per project and what I know about you up to date; everything
else is yours."), the facts note when there is a fact to write, and a minimal
`.obsidian/app.json`. A
failure removes exactly what the request made, and a later failure (the folder
rules on the finished path, the record write) does the same through
`Creation.Undo`. Setups are serialised and idempotent per `request_id`.

Seeding copies, once, what Ori already holds, each line through
`sensitive.ContainsSecretLikeText`: the confirmed, unclosed follow-ups (at most
200; unconfirmed candidates are left out) become `Inbox/<slug>.md` with a
`- [ ]` task line, so the extractor reads them back as open items; approved
facts (`how_you_work`, `routines`, `sources`) and the working agreement's summary
become `What Ori knows about you.md`, which the root README links instead of
carrying them; approved People entries (at most 100) become `People/<name>.md`.
People entries are one free-text sentence, so the name is the first run of up to
three capitalised words in it (`PersonFromSentence`); a sentence that names
nobody seeds nothing. Seeded follow-ups and people carry `ori: seed`; the facts
note carries `ori: facts` and is rendered exactly as the fill-in would render it,
so the first sync adopts it and says nothing.

The first look is rules-only and reports the seed ("Your second brain is set up:
N follow-ups in Inbox, M people."), or, when nothing was seeded, says it is empty
because Ori does not know the user yet and offers to be shown a folder. Under
either headline the card offers Downloads, Music, Documents and Desktop and
**Choose a folder…**; a chip attaches that folder to the assistant composer
exactly as the Add folder chooser does, and nothing is read until the user sends.
Items in seeded `Inbox/` notes, and in `follow_up` notes (below), are marked
`seeded`: they are never suggested and never added as follow-ups, because the
user already has them. The
learned fact reads "You keep your second brain in a folder Ori set up called
{name}." Only one notes folder is connected at a time, so setting one up while
one is connected is a conflict, not a replace.

### What Ori writes, and when

Three things write to a second brain Ori set up, always through `Writer`:

- **Journal → Daily (FR14).** Saving an HQ Journal still creates the Personal HQ
  workspace note, which is the canonical save. Then `JournalService` hands the
  same text to a `DailyMirror`, which writes `Daily/<YYYY-MM-DD>.md` with
  `ori: journal`. `POST /api/personal-hq/journal/save` answers with
  `daily_note` only when there is a second brain Ori set up: `mirrored`;
  `user_owned` (the day's note is the user's or was edited, so it was left
  alone; there is no "overwrite anyway"); or `failed` (logged; the save stands).
  The Journal toast says which.
- **Capture → Inbox (FR15).** `SecondBrainService.Capture` writes each note as
  its own file, `Inbox/<YYYYMMDD-HHMM>-<slug>.md` with `ori: capture`, through
  `Writer.Create`, which never replaces anything: a name already taken gets
  `-2`, `-3`, … The note is 1 to 4,000 characters and refused if it looks like a
  credential. It is idempotent per `request_id` (the receipt keeps the note's
  path). Two callers: the Notes card's capture box (`POST …/capture`) and the
  assistant's chat tool.
- **The chat tool `second_brain_capture`.** Offered only in the hired
  assistant's own conversation, and only while a second brain Ori set up is
  connected (`CaptureAvailable`), so a user with a connected vault never sees
  it. Its instructions require an explicit request in the current turn ("note
  this", "remember this in my notes") and forbid a bare yes, earlier turns,
  retrieved text, and the model's own judgment; the tool matches no phrase
  itself. One note per turn, one request id per turn, and the saved note's name
  is added under the reply whatever the model said. It writes only; no tool
  reads the notes.

`GET` adds `source.origin` and, for such a folder, `written_by_ori`
`{count, by_kind, last_at}`: the notes in Inbox, Daily, People, each project's
README and the facts note whose first lines carry Ori's marker (edited or not,
seeded ones included), found by a bounded listing that reads a note's first 160
bytes and keeps no content. The Notes station shows it ("set up by Ori · 5
written by Ori · last write 3h ago", or "· 2 are yours now" once the user has
taken notes over) and its card shows the capture box.

### Filling in after setup

Amendment for Issue #759. Setup copies what Ori holds once. After that, whenever
the user approves a fact or a person, confirms a follow-up, completes one,
creates a workspace or changes its goal, Ori copies the result into the folder as
a note it marks as its own. It never edits a note the user wrote or changed.

`secondbrain.Writer` accepts exactly these paths for a `created_by_ori` source
(`splitOwnedPath` is the one place the rule lives): `Inbox/<name>.md`,
`Daily/<name>.md`, `People/<name>.md`, `Projects/<name>/README.md` and
`What Ori knows about you.md`. Anything else is `ErrOutsideOwnedFolders` with no
filesystem call. The marker's kinds are `journal`, `capture`, `seed`, `facts`,
`person`, `project` and `follow_up`, defined once and read by every pattern.

| When this happens in HQ | Ori writes | Kind |
|---|---|---|
| A fact is approved, edited or forgotten (any route: Profile, Remember, the interview, a reviewed suggestion) | `What Ori knows about you.md`: a heading, a bullet per approved fact in `how_you_work`, `routines` and `sources`, the working agreement | `facts` |
| A `people` fact is approved | `People/<Name>.md`: the name, the sentence, "How Ori knows this" with the date; one note per person however many sentences name them | `person` |
| A follow-up is confirmed or added by hand | `Inbox/<slug>.md` with the same `- [ ]` line, date and "From:" line the seeds use; a title already taken at the next name gets a number | `follow_up` |
| A follow-up is completed | The `- [ ]` line becomes `- [x]`, only in a `follow_up` or `seed` note Ori wrote and the user never edited (`Writer.Tick`) | unchanged |
| A workspace is created, or its goal changes (`workspace.created`, `workspace.goal_charter_changed`) | `Projects/<Name>/README.md`: the heading, the goal, the anchored folder, the agents, and a link `/?workspace=<id>` | `project` |
| A Journal is saved | `Daily/<date>.md` (above) | `journal` |

Personal HQ is never a project (the knowledge binding names it, since a
workspace's own designation is only a projection), nor is a group. A follow-up
made from a line in the notes (`SourceRef{note_item}`) is never written back, or
the next look would find it again.

**One entry point.** `SecondBrainService.Sync(ctx, userID, reason)` compares what
HQ holds with a ledger and writes only what is new or changed, at most 40 notes a
run; a run that stops at the bound records `more_pending` and the next continues.
It runs, in the background and never blocking the change that triggered it, after
each trigger above, when the Notes panel opens and after Look again. Triggers that
land during a run share one more run. It never runs on a timer, and a second run
in a row writes nothing. If HQ's holdings cannot be read the sync stops: unknown
is not empty, so a passing failure cannot mark every note stale.

**The ledger** is `fill_in.mirrors[]` on the source: `[kind, id, rel, hash,
written_at, state]` with `state` `current`, `stale` or `user_owned`, at most 400
entries (1 facts note, 100 people, 200 follow-ups, 50 projects, plus headroom).
Each entry is an array rather than an object, with the source id and the body hash
shortened to 16 hex characters and the path held to 96 bytes: with named fields
400 entries do not fit beside a full look in the 512 KiB record, and
`TestSecondBrainCodec_EveryCapFullFitsTheRecord` proves the compact form does
(521,734 bytes with every cap full). Before it trusts an entry, a sync checks the
note on disk:

- A note the user **edited, moved or deleted** is `user_owned`, and Ori stops
  updating it and never retries. It is counted ("set up by Ori · 14 written by
  Ori · 2 are yours now"), not repaired.
- A note already at the name that is a `seed`, or exactly what the sync would
  write, is **adopted** with no write, so setup's notes are not rewritten or
  announced.
- A name taken by the user's own note is theirs (`user_owned`, no write); a
  follow-up note instead takes the next number.
- **Forgetting** deletes nothing. A person's note stays and goes `stale`; the
  facts note drops the line on its next refresh if it is still Ori's. A completed
  follow-up's note is ticked and its entry goes `stale`; a reopened one is opened
  again. When 200 follow-up entries are held, a closed one's entry is released for
  a new one (its note stays on disk).

**Consent.** A setup made after #759 is `allowed` from the start and its setup
sentence says so. A setup from before has no answer (`fill_in.state: pending`).
Until it is allowed the sync writes only `Inbox/` and `Daily/`; what it holds back
is remembered (`withheld`), and only then Needs you shows one card: "I can also
keep People, a note per project and what I know about you up to date in your
second brain. Yours are never touched." with **Let Ori write there** and **Not
now** (offered again after 7 days, as the ask is). `POST …/fill-in {decision:
allow|later, request_id}` is closed-body, origin-guarded and idempotent; allowing
starts a sync. An existing setup's own READMEs are never rewritten, since Ori
cannot prove they are unedited; the answer is the record of the change.

**What the user sees.** `GET` adds `fill_in {state, offer, project_ask, written,
user_owned, stale, last_at}` and `written_by_ori.by_kind`, and counts every place Ori keeps
notes. A sync that wrote something adds one Today Done row, "Ori added 2 people
and a project to your notes", collapsing every write since it was last seen,
linking to the Notes station and leaving after a day. A tick, or a refresh of a
person note that already existed, does not make a row. Three Tier 2 missions complete from what Ori
wrote, observed on the server: **Capture your first note**, **Save your first
Journal** and **Give Ori a project**.

### A project note becomes a workspace, on request

Nothing is created automatically (FR18). `GET` adds `projects[{name, rel, path}]`
for a second brain Ori set up: the folders directly under `Projects/` (at most
50, by name; links, hidden folders and files skipped; a bounded listing that
opens no note). Two things act on it, and both only open the form:

- The connection card lists each project with **Make a workspace**. It opens
  Create Workspace → **Use an existing folder** with the work folder filled in
  and the name set to the project's, already run through the read-only chooser
  check. The person still reviews and presses Create.
- In the assistant's conversation, a message about a workspace or agents that
  names a project gets a "Make <project> a workspace" action under the reply
  (the same navigate action the hire chip uses). It is not a tool, creates
  nothing, and appears only for a project the message names. The link is
  `/?create=1&second_brain_project=<name>`: it carries a name, never a path, and
  the page finds the folder in the second-brain status.

The result is an ordinary anchored workspace (see `anchored-folder-workspace.md`,
FR19): one read-only filesystem binding over exactly `Projects/<name>`, Ori's
own state in the sidecar, and nothing written into the notes. Agents there have
no access to the rest of the notes. `TestCreateAnchoredWorkspace_AProjectOfASecondBrainIsAnOrdinaryAnchoredWorkspace`
proves it against a real set-up folder.

### The first week

Amendment for Issue #759. The fill-in is the engine; the first week is four short
beats that feed it, each optional and each ending in a note the user can open.
The assistant drawer shows one second-brain card at a time: the consent card (for
a setup from before), then the project question.

1. **A folder to learn from.** A setup that seeded nothing says so, once ("Your
   second brain is empty because I don't know you yet. Show me a folder and I'll
   start filling it.") and offers the folders the server's own `/choices` list
   names, so a folder this machine does not have is never offered. The offer
   stays until a first look succeeds, through a reload or a failed attempt. Three
   Tier 2 missions follow from what Ori writes (**Capture your first note**,
   **Save your first Journal**, **Give Ori a project**); they are optional, locked
   behind **Connect your second brain**, and skipped, never failed, for a user who
   declined the ask or connected a vault Ori does not write to.
2. **Downloads teaches Ori.** After the user approves files for filing, the
   `approved_files` producer reads the File Janitor action journal's file *names*
   (the base name without its extension, for moves that are approved, applied and
   not undone, through the same gate as the filing-preference suggestions). A
   company or person whose name recurs in at least two of those names becomes one
   reviewable learning: "You deal with Acme (seen in 2 files you filed)." A name is
   a run of capitalised words joined by exactly one space, underscore or hyphen;
   digits never reach a learning, every line passes
   `sensitive.ContainsSecretLikeText`, and no file is opened. Learnings live in
   the `setup_batch` pool of reviewed knowledge (12 pending, no daily limit), so
   they neither use up nor wait behind the shared suggestions' 6 pending and 3 a
   day. Their evidence is `<workspaceId>:<actionId>`, the count follows the
   journal as more files are filed, and a rejected one is not proposed again. The
   first-batch report gains "I learned N things from this folder; approve them
   and they go into your second brain." with **Review what Ori learned**, which
   opens a card listing these and the Janitor's filing preferences
   (`GET /api/personal-assistant/setup-learning`; `POST …/setup-learning/dismiss
   {request_id}` turns the rest away for good). Approving or rejecting is the
   Profile page's own `POST /api/personal-assistant/knowledge/{id}/approve|reject
   {version, request_id}`: nothing is a fact until the user says so. An approved
   person then reaches the notes the ordinary way, as `People/<Name>.md`, through
   the sync.
3. **The current project.** Once the card slot is free (nothing else is waiting
   for an answer) in a second brain Ori set up and may write project notes, with no
   project under `Projects/` yet and the question neither answered nor skipped,
   the drawer asks "What's the main thing you're working on right now?" (the
   interview's `priority` question, at most 500 characters), offering a suggestion
   from the folder Ori was shown (`GET /api/personal-assistant/knowledge/interview`'s
   `suggestion {text, folder}`) as one button, and a typed answer otherwise. The answer is saved as
   an explicit fact (`POST /api/personal-assistant/knowledge/explicit`, not the
   interview's `save`, which would complete the Profile interview) and then
   written by `POST …/project` to `Projects/<name>/README.md`: the name as the
   heading, the user's words as the first paragraph, and "How Ori knows this". The
   name is the suggestion's folder name, or one Ori proposes from the answer and
   the user confirms; a name that is not one plain folder name is refused before
   anything is written. **Make it a workspace** opens the ordinary Create Workspace
   flow with the name filled in (`/?create=1&second_brain_project=<name>`), creates
   nothing until the user presses Create, and the workspace's README then replaces
   this one in place, a single file under `Projects/`. **Skip** is recorded once
   (`fill_in.project_ask: skipped`); the question never returns to the drawer and
   stays on the Profile page. Answering writes only where the setup allows project
   notes, and never over a README the user already has.
4. **The week.** Seven local days after `connected_at`, Today's Done section shows
   one row, kind `second_brain_week`: "This week your second brain gained 4 facts,
   2 people, 1 project and 5 captures.", linking to the Notes station, with
   **Dismiss**. The counts are the ledger's notes by kind (people, projects,
   follow-ups), the written-by-Ori listing's captures and journal entries, and the
   facts the facts note lists once Ori has written it; no note is opened. A quiet
   week says so plainly. The row is shown once: it appears in the seven days after
   it is due (a setup older than two weeks never gets a recap of its "first week"),
   stays for seven days after it was first shown, and is gone for good once
   dismissed (`fill_in.week_shown_at`, `fill_in.week_dismissed_at`,
   `POST …/week/dismiss {request_id}`). If the fact count cannot be read the row
   waits for the next load instead of showing a wrong number. It is only for a
   second brain Ori set up.

### The note marker

Every note Ori writes opens with exactly this frontmatter:

```yaml
---
ori: journal   # or capture, seed, facts, person, project, follow_up
ori_hash: <sha256 of everything after the closing fence>
---
```

`secondbrain.ReadMark` reports a note as `owned_by_ori` only when that block is
the whole frontmatter and the hash matches the body. Anything else is
`user_modified`: no marker, an edited body, or an added frontmatter key (an
Obsidian tag, say). Whatever Ori cannot prove it wrote counts as the user's.
"Written by Ori" counts every note still carrying the marker, edited or not.

### The write rules

`secondbrain.Writer` is the only way anything reaches the folder after creation.

1. **Only a `created_by_ori` source, and only the five paths in "Filling in after
   setup".** A path must be `<Inbox|Daily|People>/<visible name>.md`,
   `Projects/<valid folder name>/README.md` or `What Ori knows about you.md`; a
   folder's own `README.md` is never one. Any other source is `ErrNotOwned`; any
   other path is `ErrOutsideOwnedFolders`, with no filesystem call made.
2. **Only a new note, or one still exactly as Ori wrote it.** Anything else is
   `ErrUserModified` and the file is left alone. A symbolic link is never
   followed or replaced, including an owned folder, or a level of
   `Projects/<name>`, that is itself a link (`os.Root` alone would allow one that
   stays inside the folder). An evicted iCloud placeholder counts as the user's,
   because opening it would start a download.
3. **A tick is a replacement.** The one change to an existing note that is not a
   whole new version is one `- [ ]` line made `- [x]`, in a `follow_up` or `seed`
   note that passes rule 2. It goes through the same replace path, so the marker's
   hash is rewritten and the note stays Ori's; an edited note gets no write.

`Writer.Inspect` reads what is at a path without changing anything, recreating no
folder; the sync uses it to adopt a note and to see that one became the user's.

Writes pin the folder and the subfolder as `os.Root` handles, recreate a deleted
owned folder, and go through a temp file with fsync, mode 0644. A new
note is hard-linked into place so it cannot clobber one saved a moment earlier;
a filesystem without hard links re-checks and renames. A replacement re-checks
just before the rename, which leaves only a very small window on a re-saved
Journal note. Ori never deletes or renames a note; the only file it removes is
its own temp file. `TestWrite_StaysInsideOwnedFoldersNeverFollowsALinkNeverTouchesUserNotes`
proves all this against a tree of user notes and links, comparing a byte, mode
and mtime snapshot before and after.

Disconnecting removes the record exactly as for any source, and with it the
permission to write. The folder and everything in it stay on disk.

## The Notes panel

The Notes building on the HQ map opens a panel when a folder is connected
(`?station=second-brain` opens it too, which is where Today's "What's open in
{name}" row and its result card lead). With nothing connected, or a folder Ori cannot find, it opens
the drawer card as before; the ask, setup, consent and first look stay in the
drawer, and the panel's **Connection…** goes there too.

The panel is a window onto what the look and the Writer already do. It adds no
reader outside `internal/secondbrain`, no writer, and no stored note contents.

It has tabs rather than one long page, and it stays one station: its parts share a
folder and a look, and most would be empty as buildings of their own. **Inbox**
(Capture, the notes, Today) exists only for a second brain Ori set up. **Open loops**
holds triage and Look again. **People**, or **People & Projects** for a second brain
Ori set up, holds the rest. The panel opens on the first tab with something in it.

**Signals.** A look keeps up to 20 each of `people`, `stale` and `decisions`, as
names, relative paths, counts and dates, within an 80 KB budget beside the items'
300 KB. A decision's own line is not kept. A person's `note` is the person's own
note, which is what the extractor knows; it counts how many notes mention them,
not which mentions them most.

**Triage.** `POST …/look/{lookID}/items/{itemID}` takes `follow_up`, `done` or
`dismiss`, and records `acted_as` and `acted_at` on the item. No note is edited,
by any of them:

| Action | Now | On the next look |
| --- | --- | --- |
| `follow_up` | Makes the follow-up (same dedup as the bulk action); seeded items refuse it | Mark kept while the line stays open |
| `done` | Completes the item's follow-up with "Done from your notes view." if it has one; otherwise nothing but the mark | Mark kept and never suggested while the line stays open. Ticked or gone: the mark drops and the usual "Done in your notes." path applies |
| `dismiss` | Only the mark | Mark gone; the item may be suggested again |

A partial read that did not see an item's note keeps its `follow_up` or `done`
mark and item, so a mark never lapses because the bounds left a note out. The bulk
**Add N suggested** skips triaged items, counts them as skipped, and marks what it
adds as `follow_up`.

**Inbox and Today**, for a second brain Ori set up only. `GET …/inbox` lists the
Markdown notes directly under `Inbox/` through the pinned root, as `CountWritten`
does: links and hidden entries skipped, nothing followed, at most 2,000 entries
looked at, the newest 100 returned, and at most 4 KB read from each. Each folder's
README is the layout's and is left out. `kind` is `capture` or `seed` only when
that prefix proves the note is as Ori wrote it, so a note longer than 4 KB reads as
`user`. `title` is read for the listing and never stored. A listing is reused for
30 seconds and a capture drops it; it never runs on a timer. The status read's
`today_note` says whether `Daily/<date>.md` exists and whether its marker says it
is Ori's, by the server's local date, the same day the Journal proposes.

`TestListInbox_ListsNewestFirstWithKindAndTitleAndWritesNothing` and
`TestActOnItem_FollowUpDoneAndDismissLeaveTheNotesUntouched` prove all this against
a byte, mode and mtime snapshot.

## What is never done

Never, for any source:

- Following a link, or opening or writing a file outside the folder.
- Editing, deleting or renaming a note the user wrote or changed.
- Storing note contents, or sending more than the extracted signals to a model.
- Giving a project agent, the Journal agent, the Daily Brief or an MCP server a
  way to read the notes.
- Giving the assistant's chat a tool that reads the notes.
- Writing on a timer, running a look on a timer, or opening Obsidian itself
  (links are only links; `ORI_NO_DESKTOP_OPEN` stays honoured).
- Writing to a note the user wrote or changed, on any trigger: People, project
  READMEs, the facts note and ticks are all under the same rule as Inbox and
  Daily. Moving, renaming or deleting any note, Ori's own included.
- Reading the notes into chat, an agent, memory or the Daily Brief.

Never, for a connected source:

- Writing anything under the notes folder.
