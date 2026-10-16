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

Creating a workspace stores intent only (`GoalFirstRunPending`). An eligible
workspace gets one automatic attempt on first open, or on its first goal save.
`TaskScheduler` shares admission and a single in-flight slot across those starts,
Run now, explicitly enabled cadence and event triggers. HTTP starts return before
execution finishes; another start receives `409 goal_run_in_progress` and the UI
follows the existing run.

Before invocation, admission checks canonical workspace metadata, installation-
local execution permission, required setup/runtime/capabilities, the entry agent
and model, classified bindings and the goal's toolbox. A dismissed/opened wizard
is not ready. Missing or unreadable required setup fails closed; a workspace with
no declared setup requirement gains none. Setup status is evaluated outside store
mutations because it may persist regressions and fire setup bookkeeping hooks.

Code Project, ordinary copies retaining its software-project behavior profile,
and consent-gated folder assessment workspaces require **Run now** for goal
suggestions. Opening a page or saving text starts no model or project-content
read. **Start first look** starts only the separate assessment task, preserving
its recipe/tool restrictions. Neither action chains the other. An explicitly
enabled cadence remains a separate automation choice, not something a default
goal enables.

### Bounded attempts and recovery

The scheduler atomically spends deferred intent and persists `GoalFirstRunState`
before preparing execution. `starting` without an active run is interrupted or
ambiguous, not permission to replay. A pre-start `blocked` attempt leaves an
actionable reason on the goal card; repair setup/model/bindings/local activation,
then choose **Run now**. Reloading, polling, another tab or a setup completion
hook never retries a spent attempt. A per-page request latch also prevents a
pending → reload → pending POST loop after network/persistence errors.

Acceptance is persisted before execution and cancels any deferred start, including
when a manual or scheduled run wins first. Execution failures remain in run
history and require a deliberate retry (or a later explicitly enabled cadence).
Clearing the goal cancels unstarted intent; saving it again never reapplies a
template or resurrects an accepted first run. Lifecycle state round-trips through
folder metadata and the SQLite mission envelope. It is inert, portable history:
it never grants installation-local execution authority.

### Stock defaults and authoring

Every active stock template ships a short, outcome-focused `goal`, including
Code Project and Writing Project. Blank remains optional. The create modal
previews that default as literal text and prefills the existing Goal control;
an explicit replacement or deliberately empty value wins over the template.
Creation never starts a run or enables a cadence.

Templates → Overview exposes the default in a read-only field for built-ins and
source-owned templates. For built-ins, use **Duplicate** to make an ordinary
editable copy; plugin customization remains on its separate variant path.
Saving a nonempty goal trims it; clearing it removes the default. Omitted/null
API fields preserve it, and user-authored text is not limited to the stock-copy
budget. Source variants still inherit their source goal: `goal` is not a variant
override. Copies of Code Project retain its explicit-read-consent behavior.

### Template refresh and existing workspaces

Template defaults are creation-time input, not a live link. Refreshing a stock
manifest affects future creates only, never an existing workspace's goal,
deliberately empty goal, cadence, autonomy or first-run history. The existing
one-time bootstrap-goal migration is unchanged; there is no template-goal
backfill or adoption migration.

## Notifications

Notifications are a separate feature. The stored `NotificationPolicy` fields are
kept for compatibility, ignored by goal runs, and no longer shown in Goal
Settings.
