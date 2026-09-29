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

1. On the Home map choose **Import Folder** and enter the copied folder's path.
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

Resetting **Conversation & app records** keeps your workspace folders, and each
kept folder still holds its private copy. Nothing comes back on its own: import
a kept folder to restore it, or delete the folder to erase that copy.

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
