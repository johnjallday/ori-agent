# Workspace navigation

The shared workspace header is intentionally small. **Overview** retains the
internal `details` preference and URL key; this is not a route migration.

## Destinations

| Previously prominent control                      | Current destination and owner                                                                |
| ------------------------------------------------- | -------------------------------------------------------------------------------------------- |
| Details                                           | Overview (`mode=details`)                                                                    |
| Large Tickets statistic / Map Tickets belt button | Tickets, with canonical server counts beside it                                              |
| Map Backlog button                                | Tickets → Backlog; old drawer links remain adapters                                          |
| Map Inventory / Stations buttons                  | Resources; installed operational stations still open their consoles                          |
| Separate global work buttons                      | One shared **Give work**, using the existing capture, draft and explicit start controls      |
| Three identity Edit buttons                       | Settings → General; name, description and tags retain their separate save APIs               |
| Goal, schedule, triggers, manager policy          | Settings → Goal & automation, with each existing owner's scope and consent                   |
| MCP, skills, plugins, capabilities, calendar      | Settings → Tools & connections; installation and runtime grants remain separate              |
| Notes, files, linked folders, Memory, sessions    | Resources → the corresponding existing editor/manager                                        |
| Successful setup/portability badges               | Settings → Advanced; failures, missing requirements and imported routines-off remain visible |
| Full agent deck                                   | Overview → Team → Manage team; existing selection, staffing and agent controls               |
| Group Build / Add existing                        | Add workspace → Create new / Add existing, through the locked creator or member picker       |
| Plans (More views)                                | None for a new workspace; see [Plans](#plans-retired-creation-supported-api) for an existing one |
| Canvas (retired)                                  | None: `/workspaces/{slug}/canvas` is a 404; see Overview, Tickets and Resources              |
| Program/profile/catalog links                     | Declaration-owned destinations; the profile jump stays on its owning assistant page          |

Dashboard appears only when configured. Personal HQ keeps its station-first body;
custom dashboards and plugin consoles retain their own DOM, session and bridge.
Generic workspace names do not confer HQ or program behavior.

There is no **More views** menu. Plans was its only item, so the menu went with
it.

## Plans: retired creation, supported API

Ori's own screens no longer start new structured Plans. A workspace with no Plans
has no Plans destination, no setup prompt for one and no creation form. To hand a
workspace work, use **Give work** or add a ticket; talking about work in chat
starts nothing, and chat answers several-step requests with advice that names
Give work.

A finished first assessment still suggests one next ticket. **Review suggested
ticket** opens `/workspaces/{slug}?next=assessment`: Give work opens once with the
suggestion typed in as plain text, nobody assigned and Start now off. Nothing is
saved unless you press **Give work**; cancelling, reloading, Back and Forward
create nothing. The old `/workspaces/{slug}/plans?next=assessment` bookmark is
redirected to the same place.

What is **retired** is the first-party creation UI. What is **supported** is
unchanged: the Plan API (including `POST …/plans`), every stored Plan, version,
approval, export and Ticket/Run link, the Plans collection
(`/workspaces/{slug}/plans`) and its detail pages. A workspace that has at least
one Plan, active or in History, shows a secondary **Existing plans** link under
**Tickets**; opening a Plan changes nothing, and review, approval, start, pause and
cancel keep their original gates. If that check cannot be read the link area says
so and offers **Try again** instead of hiding the link. In Settings the planning
controls are folded into **Existing plans**; saving settings writes every value
back exactly as loaded.

## Work, unknown data and attention

Overview leads with the existing [Next action](workspace-next-action.md), ongoing
work and meaningful configured goal state. Team and Resources are compact;
linked-workspace content appears only for groups. Optional unconfigured goal
controls live in Settings rather than becoming a missing-setup warning.

Unavailable is not empty. Roster, membership, resource and work read failures keep
an explicit unknown/unavailable state and an existing retry/repair destination.
A rendered zero must come from a successful canonical read. An idle agent's
**Ready to help** is not proof that a workspace has no queued work.

## Folders on a Home’s map

On a writable Music Production Home, **Map → + Add folder** opens the system folder picker. Hire the folder’s manager and review its plan, just as you would from **Explore a folder**. Choosing alone creates nothing. **Hire & build** creates the folder workspace and its manager inside this Home immediately, on both maps. **Set up** confirms the shelf and other steps you choose; it does not move the workspace to another Home. If you leave while the manager is looking, return to Map and press the pending folder’s name to reopen its card.

Press a folder’s name to select and frame its station without moving anything. The song count is what is on the map; **n to set up** opens that folder’s shelf projects. Projects from shelf-only roots remain on the Home-wide setup button. Four folders are shown by name; the rest are in **more folders**. Ordinary groups and Homes without a fed folder keep **Linked workspaces**. **Add workspace** and **Group projects by folder** are separate controls.

## Files inside a workspace

Open a workspace’s **Files** tab. **Folders on this Mac** (**Linked folders** on another device) lists the connected folders with their first level already open. In a group this includes members’ folders: if one lies inside another, expand it there rather than looking for a second copy at the top. **Stored in Ori** below holds the workspace’s own files and notes, and those of group members under their names. With no linked folders the usual **Files**, **Notes** and (for a group) **Linked workspaces** tree stays in place.

Inside a Home’s fed folder, select a folder marked **on the map** to open the song’s workspace or see its notes. **Not set up** means the shelf already knows about the project. On a writable Home, **Set up this project** opens its existing review sheet; nothing is set up until you confirm there. If the shelf can’t be read, you can still browse the folders. Move and Trash confirmations identify a selected song on the map and can be cancelled.

On the local Mac, selecting a linked text or source file shows its plain text in the preview pane (up to 512 KiB); common raster images preview up to 8 MiB. Selecting a file never launches an app. **Open in editor** opens source files such as `.py` as documents in TextEdit, not as programs. **Open in viewer** uses the system reader for supported images, PDFs and media. On the Mac running Ori, select an eligible `.rpp` project and choose **Open in default app** to request that exact original, even if another project is the workspace’s primary. The default application is chosen by macOS. Remote clients can browse but cannot launch it. If the folder is unavailable, reconnect it and retry; if the selection changed or expired, refresh Files and select it again. Browsing a linked folder does not copy its files or make it searchable.

**Select and organize originals.** Inside a linked folder, click a file or subfolder, ⌘/Ctrl-click to toggle siblings, or Shift-click for a range. Files and folders can be selected together. Drag the selection onto another folder in the same linked tree or choose **Move to…** from its right-click menu. The destination picker lists folders visible in Contents, including the linked root; expand a branch to make a deeper destination available. Review the destination and choose **Move here**. Ori won’t overwrite an existing name, copy across disks, or move a folder inside itself.

**Move to Trash.** Right-click the selection and choose **Move to Trash…**, or press Delete while a selected row is focused. Keyboard users can toggle with Ctrl/⌘-Space and open the row menu with Shift-F10. Confirm the paths once. A folder moves intact, **including all its contents**. If a move fails, Ori stops and reports how many it confirmed. The linked root itself cannot be moved or trashed here. There is no permanent-delete fallback.

If macOS refuses a Trash request, check which access Ori reports. For **folder access**, go to **System Settings → Privacy & Security → Files & Folders**, expand the app that launched Ori, and enable **Downloads Folder** if listed. A locked item or a read-only parent can also prevent a move; check **Get Info** in Finder. For **access to Trash itself**, enabling Downloads won’t help. Select the item in Finder and press **⌘-Delete** to avoid granting broader access. Ori also offers instructions for optional Full Disk Access for the launching app. Neither opening Settings nor refreshing a folder retries a move; select the item and confirm again.

**Color folders.** Right-click a linked folder and choose **Folder color…**, or select several sibling folders to color them together. Choose a named color or **Clear color**. The color stays with a subfolder moved within that link. Colors are saved in this browser’s Files view, not written to Finder tags or the originals.

## Files and Map

Home has one switcher, the rail on its left: **Files** and **Map**. Files is Home's tree (`/?view=tree`), with one **Search by name…** box and, beside the tree, a **Results** tab for what it finds; there is no separate Map/Tree toggle. Map is the workspace map. The rail shows for everyone, switching is in the page (no reload), and on a narrow screen it becomes the **Destination** picker. The global app bar and workspace controls remain on Map. Visiting either does not change your starting page or grant a search folder to a workspace.

Switch Files → Map → Files in the same tab and the search term, scope, selected result and loaded pages are still there; Map's camera and selection are recovered as before. After leaving the page, **Back** restores both on the Mac that runs Ori. The Commander reads the current question; following a destination never answers it. Workspace and ticket deep links keep their normal meaning.

On a local Mac with filename search available, searchable folders are managed in Files. Each linked folder's row says whether it is searchable, and its menu offers **Make searchable**, **Refresh index**, **Remove from search** and **Search in this folder**, as its state allows. Searchable folders with no workspace sit in a **Search-only folders** group at the foot of the tree, with **Connect to workspace**. **Add folder…** below the tree opens the same Explore card as the first-run page, and the button beside the Files heading chooses the starting page: **Make Files my starting page** or **Start on Map instead**. Once a workspace exists, `/search` opens Files; before that it is the first-run page, which also manages folders while setup is incomplete. On a workspace page, **Search _folder name_ in Files** opens Files scoped to that folder in the same tab. Removing the filename index leaves the workspace, its link, notes and tickets on Map and in Files; neither view watches Finder or regrants search. See [searching one folder](search-first.md) for consent, scope, selection and restoration limits.

## Home workspace panel

Select a workspace on Home's map to open its context panel. **Team** shows the
commander once, first with a text **Commander** badge, then the other agents.
Chosen characters and uploaded images retain their saved appearance; generated
agents keep the map's resident portrait. While agent records load, names remain
visible in stable empty slots. Unknown agent data is not presented as an empty team.

On a group's own **Map** tab, agent units use the shared avatar for all modes,
including the initials-based Generated avatar, rather than substituting a resident.
Chosen characters and workspace-scoped uploads remain authoritative. This does not
assign a character or change any saved appearance.

The Commander's balloon and the chat drawer (header, launcher and the line above
each message) take the Commander's face from the page, so they match its card: on
a group's page that is the group-map unit's avatar, on any other workspace page
the resident figure for a generated agent. Until the page has the agent's record,
and on Home, the drawer keeps its own face.

## State and history

- Tickets retains `?ticket=<stable-id>` and `?tickets=<state[,state]>`, not a new
  `mode=tickets`. Selection and state filters remain owned by the ticket module.
  Back/Forward restores them without pushing another return entry; late reads
  cannot overwrite a newer query, selection or workspace. Same-ticket live edit
  drafts survive refresh/restore.
- Existing `panel=tasks&task=...`, Backlog, run and station arrivals still delegate
  to their owners. Full-ticket return targets retain workspace, selection and
  filter. A run link monitors an existing run; it is not start admission.
- Settings/Resources keep live editors outside disposable shared chrome. Refresh
  does not clone forms/iframes or discard drafts. Child editors suspend their
  parent; cancel/return resolves a live opener. Dirty and protected confirmations
  retain their existing guards.
- Scoped Map camera and selection survive view-only round trips in memory.
  Navigation does not move buildings or persist a new layout. Actual moves,
  placement, membership changes and creation still require their existing actions.
- Opening a menu does not create a workspace, install tools, approve operations or
  grant capabilities. File review remains in Filing.

### Configured first-goal boundary

**Approved exception:** the existing pending-first-goal loader may POST
`mission/first-open` when a configured pending goal is first opened. The product
owner explicitly chose to preserve this behavior. This branch does not change
execution admission or add another automatic run path. Subsequent navigation,
refresh and view changes must not duplicate that first run.

All other navigation guarantees above still apply. Opening a menu or configuration
section does not itself authorize a run, filing operation, grant or activation.
Validate this exception separately from no-goal navigation using the deterministic
goal fixture; its model output is not live-provider evidence.

## Verification and evidence

`playwright.workspace-ui.config.ts` covers Chromium/WebKit, both themes, narrow
and desktop widths, keyboard-only navigation, refresh/history, and a 720×450 CSS
viewport equivalent to 1440×900 at 200% zoom. Safari's default macOS mode uses
Option-Tab to include buttons and links. Native VoiceOver and browser-chrome zoom
still require a separate manual check.

`tests/workspace-surface-plugin.spec.ts` exercises the repository-owned fixture's
actual trust/grant/protected-operation, frame and lifecycle paths.
`tests/workspace-declared-navigation.spec.ts` exercises actual declared Home,
profile and resource Catalog routes. These are host integration evidence, not
certification of an external plugin, catalog scan, native DAW or paid model.

README capture photographs the real compact Team/Next action composition. A
changed capture contract does not accept new images: root README copy, accepted
screenshots and acceptance hashes remain under the separate approvals in
[README maintenance](README_MAINTENANCE.md).
