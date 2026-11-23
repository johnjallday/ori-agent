# Moving a workspace to another computer

A workspace folder can carry its saved work to another Ori installation: its
conversations and their uploaded files, follow-ups, Daily Brief history, notes,
memory and — for a Personal HQ — your assistant and its working agreement. You
copy one folder; the other computer reviews it and asks before restoring
anything.

## 1. Check the folder is ready

Open the workspace. The chip in its header shows whether its latest work is in
the folder:

| Chip | Meaning |
| --- | --- |
| **Ready to move as of …** | Copy the folder now. |
| **Saving latest work** | Ori is adding your latest changes. Wait a few seconds. |
| **Preparing** | The first copy is being made. |
| **Unavailable** | Open the chip for the reason (for example a link the folder cannot carry). |

Click the chip for details, or **Prepare now** to refresh immediately.

## 2. Copy the whole folder

Copy the workspace folder from your Workspace Directory, including its hidden
`.ori` folder, to the other computer (a drive, a sync service, anything that
copies files). Copy nothing else: the other computer does not need this
computer's database, settings or upload folder.

The folder contains private conversations, follow-ups, briefs and uploaded
files. Treat every copy as personal data — Ori cannot erase a copy you have
made somewhere else. If your Workspace Directory is inside a git repository,
add `.ori/continuity/` to its `.gitignore` unless you mean to commit that
private history.

## 3. Import it on the other computer

1. On Home, switch to **Tree** and choose **Import Folder**. Enter the copied folder's path.
2. Ori reviews the folder without changing anything and shows what it holds
   and when it was saved.
3. Choose:
   - **Import and continue** — this computer has no assistant yet: your
     assistant comes back with its working agreement, paused. Resume it when
     you are ready.
   - **Import workspace only** — the workspace and its history, without making
     its assistant yours here. This is the only choice when this computer
     already has an assistant; that assistant is left exactly as it is, and
     the incoming one stays with the folder so it can move on later.
4. A report lists what was restored and what to set up here.

If the folder changed after you reviewed it, Ori asks you to review it again.
If an import is interrupted, the report offers **Retry**; nothing is
duplicated. Importing the same copy twice changes nothing.

## What stays on each computer

- **Not copied:** API keys, connected accounts, MCP and tool connections,
  permissions and model choices. Set these up on the new computer.
- **Background routines start off.** Schedules, missions, triggers, first-open
  briefs and follow-up reminders do not run for an imported workspace until
  you open its chip and choose **Turn on background routines…**. Turning them
  on does not stop them on the other computer — avoid running the same
  routines in both places.
- You can read and use everything by hand right away: open old
  conversations, continue them, complete follow-ups, read old Daily Briefs.
- Files that were linked from elsewhere on the old computer (rather than
  uploaded) show as unavailable.

When the workspace is not this computer's Personal HQ, its old Daily Briefs
appear on the workspace page under **Daily Briefs**; new briefs come from the
Personal HQ here.

## After resetting Ori

**Reset Selected Data** with **Conversation & app records** selected, and
**Start Fresh**, leave workspace folders in place by default. Ori does not
restore retained checkpoints automatically. Use **Import Folder** to review
and restore one explicitly.

If your current Personal HQ is eligible, you can check **Archive my old
Personal HQ** in the reset review. Ori shows the source and the proposed
archive location before you confirm. On full relaunch, Ori moves just that
folder, intact, outside the Workspace Directory. The reset result gives its
exact location under Ori’s data directory (`hq-archive-<operation-id>-<folder>`).
It does not move other workspaces or vault files.

The archive option is for a directly managed, designated Personal HQ on a
supported local filesystem. Detached folders, linked or external workspaces,
unsafe paths, and cross-volume moves are not eligible. If Ori cannot verify
that it can make the approved move without overwriting anything, the reset
stops and displays recovery guidance.

The archive preserves files that were already there, including any portable
checkpoint. It does not save changes that existed only in Ori’s database. If
you need the latest work, **Prepare now** and wait for **Ready to move** before
resetting. Review an archived folder with **Import Folder** just like a copied
workspace. Nothing is reconnected by default; restoring the assistant is an
explicit choice and it starts paused. An existing assistant is not replaced.

If a retained folder blocks **Build my HQ**, you can confirm the suggested
folder name without re-hiring or changing the HQ name, Daily Brief or Notes.
You can dismiss the choice and return to the saved setup later. **Resume setup**
tries the already approved folder; it never accepts a suggestion for you.

## Older folders

A folder from a version of Ori without this feature (no `.ori/continuity`
checkpoint) imports as an ordinary folder: its workspace files come across, but
its conversations, follow-ups, Daily Briefs and uploaded files are not in it —
the import review says so. If it was a Personal HQ and this computer has no
assistant yet, the review offers **Continue with <name> as my personal
assistant** (checked). Keep it checked to bring that assistant back, paused. The
older copy never saved the working agreement, so focus and the brief schedule
are new choices you make here; background routines start off as with any
import.
