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
| Plans / Canvas                                    | More views, using their native full-page links                                               |
| Program/profile/catalog links                     | Declaration-owned destinations; the profile jump stays on its owning assistant page          |

Dashboard appears only when configured. Personal HQ keeps its station-first body;
custom dashboards and plugin consoles retain their own DOM, session and bridge.
Generic workspace names do not confer HQ or program behavior.

## Work, unknown data and attention

Overview leads with the existing [Next action](workspace-next-action.md), ongoing
work and meaningful configured goal state. Team and Resources are compact;
linked-workspace content appears only for groups. Optional unconfigured goal
controls live in Settings rather than becoming a missing-setup warning.

Unavailable is not empty. Roster, membership, resource and work read failures keep
an explicit unknown/unavailable state and an existing retry/repair destination.
A rendered zero must come from a successful canonical read. An idle agent's
**Ready to help** is not proof that a workspace has no queued work.

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
  grant capabilities. Filing review remains in File Janitor.

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
