# Work vocabulary

Ori names a unit of work one way on every screen. The code may keep saying
`task` (the `workspace.Task` type, routes, JSON fields, element ids, CSS
classes); only what a person reads follows this table.

| Concept                                                                      | The one word                                               | Retired words                                       |
| ---------------------------------------------------------------------------- | ---------------------------------------------------------- | --------------------------------------------------- |
| A unit of work an agent does for you                                         | **Ticket**                                                 | Task, Quest, Objective, Job, Work item (as a label) |
| Tickets waiting to be picked up (a _state_ of tickets, and the list of them) | **Backlog** (state chip, filter, list title)               | Quest Board, "Backlog · Quest Board"                |
| The list of all tickets in a workspace                                       | **Tickets**                                                | Tasks, All Tasks, Objectives, Session Tickets       |
| One execution of a ticket                                                    | **Run**                                                    | —                                                   |
| Asking an agent to do work                                                   | **Give work**, "New ticket"                                | New task, Create task, Delegate a task              |
| Moving a backlog ticket into work                                            | **Start**                                                  | Accept Quest, Start Quest, Promote                  |
| Opening a ticket                                                             | **Open** / **Resolve** (needs input) / **Track** (working) | Open Quest, Resolve Quest, Track Quest              |

Backlog is kept on purpose: it is a lifecycle stage of a ticket
(`workspace.TaskStatusBacklog`), not a second name for the item. The item is a
ticket; backlog is where a ticket waits.

## Where the words live

- Web UI: `internal/web/static/js/modules/work-labels.js` — `WORK_LABELS`, the
  retired list `RETIRED_WORK_TERMS`, and `retiredWorkTermsIn()`. `hq-labels.js`
  takes its work words from it. Import the label instead of typing it.
- Server display text: `internal/workspace/worklabels.go` (`LabelTicket`,
  `LabelTickets`, `LabelBacklog`, `LabelRunYourFirstTicket`, …).

## Guards

- `work-labels.test.js` renders the Map belt, the Backlog and Tickets windows,
  ticket row actions, the agent sheet and the Tickets page header with fixture
  data and fails if a retired word appears in text, `aria-label`, `title` or
  `placeholder`.
- `internal/workspace/worklabels_test.go` scans the server's display-string
  sites (quest titles, result-group titles, notification and run-history
  titles, blocked-ticket questions) for "task".

## Not covered by this rule

- The setup checklist's own framing: "Mission 01", "Quests", "Quest complete"
  and the game economy (Craft, Harvest, Energy, XP, Tier).
- Home surface names (Needs you, Activity, Today), agent role words, and
  container words (Workspace, Group, HQ).
- Template-defined user setup quests under `internal/projecttemplates`, whose
  copy is digest-pinned into saved progress.
- API messages and error codes, log lines, model prompts, and the `tasks.md`
  file format.
