# Workspace goal runs

A workspace's goal (`Workspace.Mission`) is the one sentence its agents work
toward. It is set at create (explicit goal → template `goal` → legacy bootstrap
goal) or in Goal Settings, and it is injected into every workspace prompt: the
chat snapshot, the protected task header (`GoalPromptLine`), Plans and the Home
summary.

## What a goal run does

A goal run reads the workspace and answers with one JSON object whose `items`
array holds `task` suggestions and `finding`s (`ParseMissionRunOutput`).

- Suggestions become **Backlog tickets** with `source_type` `goal_suggestion`
  and their why, evidence, first step, agent, effort and confidence under the
  ticket context key `goal_suggestion`. Findings go to the Action Center. One run
  stores at most five of each; anything already open, recently finished or
  dismissed is skipped (`SelectMissionSuggestions`).
- Storing is an application-side effect of parsing the answer, not a model tool
  call, so it happens under Watch and Propose alike. Autonomy only decides what
  the agent may do **while it looks**: Watch reads only; Propose may also write
  notes and drafts inside the workspace. An unset level is Watch.
- Every run appends a `MissionRunRecord` (counts, or the error and the tail of an
  unreadable answer) shown in Goal Settings run history.
- **Not useful** on a suggested ticket, or deleting one any other way, adds its
  normalized title to `Workspace.DismissedSuggestions`, which later runs are told
  never to suggest again.

## When a run starts

`TaskScheduler.StartMissionRun` is the one entry point for cadence, Run now, the
first goal save and the first open of a workspace created with a goal
(`GoalFirstRunPending`). It claims one in-flight slot per workspace (a second
start is `409 goal_run_in_progress`), refuses with a stored reason when there is
no agent, model or toolbox (`412`, shown on the goal card), and executes detached
from the request; the UI follows the run until its record lands.

## Notifications

Notifications are a separate feature. The stored `NotificationPolicy` fields are
kept for compatibility, ignored by goal runs, and no longer shown in Goal
Settings.
