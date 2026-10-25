# Workspace next action

Ordinary workspaces show the same **Next action** in Map and Details. The Map,
stations, agent controls, Memory, and **All Tasks** remain available. Group maps,
Personal HQ, Tickets, and dashboards keep their existing journeys.

## How recommendations are chosen

The shared resolver uses canonical task presentation and File Janitor's public,
identity-bound action summary. It does not identify capabilities or starter tasks
by their names, hide tasks, or change their completion state.

Priority, highest first:

1. Tasks needing attention.
2. File Janitor repair, setup, or consent.
3. Files awaiting review.
4. Running tasks, then an active file scan.
5. Other actionable tasks, then unknown task states.
6. Confirmed task results or filing receipts.
7. Known manual, paused, watching, or idle state.

Running work stays visible alongside a higher-priority recommendation. Task ties
use the existing task ordering. Results compare recorded completion times, never
invented activity timestamps; filing results come from unfiltered History,
independently of the console's current filter.

Loading and unavailable sources are explicit. A failure in one source does not
hide usable actions from another. **Retry workspace status** only rereads state.
An idle agent is **Ready to help**; queued work is still **Waiting**. Zero
specialists and empty Memory are not missing-setup conditions.

## File review remains in File Janitor

**Review files** opens the existing console. Setup, consent, repair and result
links also open their existing destinations there. Opening guidance does not
start a task, scan, preview, move, or Trash operation. The console may read state
and mark its existing map parcels seen.

- **Scan now** is an explicit console/card action.
- Move/Trash choices and a same-batch confirmation survive refresh and view
  changes. A changed workspace, folder or batch invalidates old authority.
- Filing receipts distinguish applied, failed and changed-since-preview files.
  A mixed outcome is not reported as complete success.
- **Undo move** appears only where the existing History owner permits it.
- Old guidance buttons recheck the current recommendation before dispatching.

Keyboard focus returns to the opener's live replacement. If a read temporarily
removes the primary, focus stays in the Next action region until it settles.
Escape cannot dismiss a pending confirmation; Back/reopen retains its owner.
Stable status announcements avoid repeating unchanged recommendations on polls.

## Validation and safe reproduction

Use a disposable server, never a normal server pointed at your own workspace
root or Downloads:

```sh
# Run in zsh from this feature worktree.
source scripts/wt.sh
ORI_DEMO_NO_CODEX=1 wt demo 8947
```

In another terminal:

```sh
./scripts/e2e.sh --port 8947 tests/file-janitor.spec.ts -- --workers=1 -g 'next action follows'
./scripts/e2e.sh --port 8947 tests/file-janitor.a11y.spec.ts tests/workspace-detail.a11y.spec.js -- --workers=1
```

The filing journey uses synthetic temporary files and performs an explicit scan,
partial apply and Undo. It also checks that navigation performs no operations.
Layout tests cover Map/Details, both themes, 320/390/768/1280px, long task names,
keyboard focus and zoom-equivalent reflow. HQ designation and queued/long task
states in those tests are labelled display/response fixtures, not live agent or
brief execution. The example-plugin regression installs only into demo storage.

These checks are not native folder-picker, VoiceOver, or live-user-folder
certification. Backup recommendations and their return/dismissal flow are a
separate feature.
