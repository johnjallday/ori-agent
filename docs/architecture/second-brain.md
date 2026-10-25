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
never followed by it. "Not now" defers (Needs you offers it again after 7 days);
"I don't keep notes" declines and skips the mission. Reset Getting Started
clears the ask and the missions, never the connection.

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
| `GET /look` | none | `{state: running\|finished\|failed\|none, look_id, result?}` |
| `POST /look` | `{request_id}` | Look again (429 within 10 minutes of the last look) |
| `POST /look/{lookID}/follow-ups` | `{items: "suggested"}` or `{items: [id…]}`, `request_id` | `{created, skipped}` |
| `POST /look/{lookID}/dismiss` | `{request_id}` | takes a result off Needs you |
| `POST /disconnect` | `{request_id, if_version}` | status |

## What is never done

- Writing anything under the notes folder, following a link, or opening a file
  outside it.
- Storing note contents, or sending more than the extracted signals to a model.
- Giving the assistant's chat, a project agent, the Journal agent or the Daily
  Brief a way to read the notes.
- Running a look on a timer, or opening Obsidian itself (links are only links;
  `ORI_NO_DESKTOP_OPEN` stays honoured).
