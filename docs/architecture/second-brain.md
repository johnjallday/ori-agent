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
| Fixtures and demo | `internal/testutil/vaultfixture/`, `scripts/second-brain-demo.sh` |

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
| `Inbox/` | Captures from chat, seeded follow-ups, unsorted notes | Yes |
| `Daily/` | One note per day, from the HQ Journal | Yes |
| `Projects/` | Things with an end date | No |
| `Areas/` | Ongoing responsibilities | No |
| `Resources/` | Reference material | No |
| `Archive/` | Finished projects and areas | No |
| `People/` | One note per person | Seeded once, then no |
| `README.md` | How this second brain works with Ori | Written once |
| `.obsidian/` | Minimal vault config so Obsidian opens it | Written once |

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
top-level folder, the root README (it opens with "I'll keep Inbox and Daily up
to date; everything else is yours."), and a minimal `.obsidian/app.json`. A
failure removes exactly what the request made, and a later failure (the folder
rules on the finished path, the record write) does the same through
`Creation.Undo`. Setups are serialised and idempotent per `request_id`.

Seeding copies, once, what Ori already holds, each line through
`sensitive.ContainsSecretLikeText`: the confirmed, unclosed follow-ups (at most
200; unconfirmed candidates are left out) become `Inbox/<slug>.md` with a
`- [ ]` task line, so the extractor reads them back as open items; approved
facts and the working agreement's summary become the README's "What Ori knows
about you"; approved People entries (at most 100) become `People/<name>.md`.
People entries are one free-text sentence, so the name is the first run of up to
three capitalised words in it (`PersonFromSentence`); a sentence that names
nobody seeds nothing. Seeded notes carry `ori: seed`.

The first look is rules-only and reports the seed ("Your second brain is set up:
N follow-ups in Inbox, M people."), or says the folder is ready when nothing was
seeded. Items in seeded `Inbox/` notes are marked `seeded`: they are never
suggested and never added as follow-ups, because the user already has them. The
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
`{count, last_at}`: the notes in Inbox and Daily whose first lines carry Ori's
marker (edited or not, seeded ones included), found by a bounded listing that
reads a note's first 160 bytes and keeps no content. The Notes station shows it
("set up by Ori · 5 written by Ori · last write 3h ago") and its card shows the
capture box.

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

### The note marker

Every note Ori writes to `Inbox/` or `Daily/` opens with exactly this frontmatter:

```yaml
---
ori: journal   # or capture, seed
ori_hash: <sha256 of everything after the closing fence>
---
```

`secondbrain.ReadMark` reports a note as `owned_by_ori` only when that block is
the whole frontmatter and the hash matches the body. Anything else is
`user_modified`: no marker, an edited body, or an added frontmatter key (an
Obsidian tag, say). Whatever Ori cannot prove it wrote counts as the user's.
"Written by Ori" counts every note still carrying the marker, edited or not.

### The two write rules

`secondbrain.Writer` is the only way anything reaches the folder after creation.

1. **Only a `created_by_ori` source, and only directly in `Inbox/` or `Daily/`.**
   A path must be `<Inbox|Daily>/<visible name>.md`. Any other source is
   `ErrNotOwned`; any other path is `ErrOutsideOwnedFolders`, with no filesystem
   call made.
2. **Only a new note, or one still exactly as Ori wrote it.** Anything else is
   `ErrUserModified` and the file is left alone. A symbolic link is never
   followed or replaced, including an `Inbox` or `Daily` that is itself a link
   (`os.Root` alone would allow one that stays inside the folder). An evicted
   iCloud placeholder counts as the user's, because opening it would start a
   download.

Writes pin the folder and the subfolder as `os.Root` handles, recreate a deleted
`Inbox/` or `Daily/`, and go through a temp file with fsync, mode 0644. A new
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

Never, for a connected source:

- Writing anything under the notes folder.
