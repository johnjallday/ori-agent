# Workspace goal runs

A workspace's goal (`Workspace.Mission`) is what its agents work toward: one
sentence, or a short **charter** (below). It is set at create (explicit goal →
template `goal` → legacy bootstrap goal) or in Goal Settings, and it is injected
into every workspace prompt: the chat snapshot, the protected task header
(`GoalPromptLine`), Plans and the Home summary.

## The goal charter

A goal is a charter when its text has at least one of five part headings, as a
`#`/`##`/`###` line or a whole-line `**Part**`, case-insensitive, in any order
and any subset (`workspace.ParseGoalCharter`). Text before the first heading
belongs to Outcome; an unknown heading stays text of the part above it. The
parts and their meanings live in `internal/workspace/goal_charter.go` only
(`GoalCharterParts`):

| Part       | Meaning                                                             |
| ---------- | ------------------------------------------------------------------- |
| Outcome    | What this workspace exists to produce or keep true.                 |
| Maintain   | What the agent keeps in good order on its own initiative.           |
| Build      | What the agent should work towards that does not exist yet.         |
| Watch      | What the agent looks for and reports when it changes.               |
| Boundaries | What the agent must not do or touch, and what needs the user's say. |

- The charter is text: no workspace field, no SQLite column, no migration. A
  blueprint whose goal is written with the headings gets the shape for free.
  The starter blueprints' goals are still single sentences; rewriting them as
  charters is a separate content change.
- **One-line readers show the Outcome line** (`GoalOutcomeLine`): the first line
  of a charter's Outcome (or, without one, its first line of text), flattened
  and bounded at 500 bytes. A plain goal's line is exactly what it was before
  charters. Task prompts, builder plans, the assistant's workspace and Home
  snapshots, the folder summary, the software assessment intent and the toolbox
  goal brief summary read it; the mission GET returns it as `goal_outcome`.
- The goal save (`PUT`/`PATCH /api/workspaces/{id}/mission`) drops untouched
  hint lines (`_<meaning>_`, written by the editor's **Use the charter shape**)
  and refuses a goal over `WorkspaceGoalMaxRunes` (4,000) with 400 "Keep the
  goal within 4,000 characters."
- The mission GET also returns `goal_parts` (present parts, `[]` for a plain
  goal) and `goal_charter_shape` (the five names with their hints), so the page
  never duplicates the copy.
- **Brief vs. charter:** the hire card's brief, the build-session goal and the
  folder brief stay one line of at most 300 runes
  (`personalassistant.BriefMaxRunes`). The brief becomes the goal at hire; it
  grows into a charter through the suggested charter (below), not by typing one
  into the hire card.

A folder setup presented as a hire (**Hire and start**, on Home's card, the
assistant's folder card and the Create Workspace build) asks one question,
"What should it do with it?". The answer, the **brief**, is the new workspace's
goal (at most 300 characters, line breaks folded; an empty answer takes the
plan's `brief_default`, the blueprint's goal or one per folder shape). The
setup writes it with `GoalFirstRunPending` like any created goal and starts no
goal run; the hired agent's first look is told it as the user's intent.

## What a goal run does

A goal run reads the workspace and answers with one JSON object: first an
`assessment`, then an `items` array of `task` suggestions and `finding`s
(`ParseMissionRunOutput`). Its prompt carries the goal as a `## Goal` section:
a charter's present parts, each under `### <Part> — <meaning>`, or a plain goal
with the note that it has no parts yet.

- **Assessment.** Every run (baseline, cadence, manual, event, first look) is
  asked to write it first: short plain text, at most about 1,500 characters, one
  paragraph per part the goal has (`Part: …`), or one paragraph against a plain
  goal, saying where the workspace stands today with evidence. Items follow from
  it; `items` may be empty. The record keeps `assessment` (paragraphs kept,
  bounded at `MissionAssessmentMax` = 1,500 runes) and `assessment_parts` (the
  goal's parts when the run started). An answer without one (the older
  `{"items": [...]}` shape) still parses `ok`. The Goal card shows the newest
  successful run's assessment (**Latest assessment**) and run history keeps each
  one under a collapsed **Assessment**; a run that assessed and stored nothing
  reports "Assessed the workspace; nothing to suggest right now."
- **Suggested charter.** When the goal is plain, a hire's first look and any run
  with `CycleOrdinal` ≤ 1 may also return `suggested_charter`: the five parts
  written from the brief and what the run found. It is bounded at 4,000 runes on
  whole lines and kept only when it parses as a charter with Outcome
  (`suggested_charter` on the record). The Goal card shows it with **Use** (saves
  it as the goal) and **Edit** (opens the Goal modal with it) while the goal is
  still plain. Later runs never ask; the hire report, Needs you and the Backlog
  ignore it. The first look's `summary` and `Next:` line are unchanged.

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

## Discussing an assessment with the lead agent

In the workspace's existing chat/drawer, ask the lead agent **“What should I
focus on in this workspace?”** The runtime offers `workspace_goal_assessment`
only to the exact resolved local entry-agent instance and rechecks that identity
on each call. Specialists, a global namesake and ambiguous/replaced instances do
not receive it. It uses the existing conversation surface.

The read returns the current full goal, the newest successful saved assessment,
its start/finish times and goal match (`current`, `changed`, or `unknown`), the
latest recorded attempt's success, and a bounded snapshot of current open and
recently finished top-level work. It reuses `MissionWorkSnapshot`: at most five
open items with their total and five recently finished items, with bounded
previews. Current task states must take precedence over an older assessment's
claims. If more evidence is needed, the agent uses the existing authorized
workspace readers; this does not expand folder access.

The conversational guidance asks for findings → one recommendation with a reason
→ a concrete offer to discuss/review. It discourages unnecessary hiring/setup,
repeating finished work, and assessment pitches on greetings or unrelated
requests. An existing relevant goal suggestion may link to the canonical
Backlog; there is no new execution button or approval route. The guidance also
applies when an authored agent prompt uses workspace variables rather than the
generic runtime snapshot.

These are stored observations, not a fresh review: `read_at` dates the read, not
the assessment. A newer failed attempt never replaces a successful assessment;
a missing assessment is stated, not invented. A matching goal is not proof that
the workspace is unchanged. New assessments retain their goal digest even when
no charter is proposed; older undigested records remain `unknown`, with no
migration or inferred freshness. Suggested charters and raw failure output are
not returned by this reader.

On successful reads, the host appends a short source line to the conversational
reply, before persisting it in the existing session. It uses the exact returned
metadata: assessment completion/start time (or explicitly unavailable), goal
match, task-snapshot read time and any latest failed review. This line is not
model-authored and does not substitute today's read time for an older review.
Only the actual native reader can supply it; same-named tools, failed reads and
model-authored JSON cannot mint one. One line covers the last successful read in
a turn. Unrelated/no-tool replies and structured tool rendering are unchanged.
The model still owns the recommendation and must distinguish historical claims
from current facts; the source line does not verify the prose's correctness.

The reader has no mutation or run-start dependency and is classified read-only.
Asking for advice starts only the normal chat turn, not a goal run, task, hire,
install, note save or standing permission. Advisory guidance explicitly overrides
the generic suggestion to save useful generated content automatically. Existing
chat tools and their authorization boundaries are otherwise unchanged; this is
not a new sandbox or a general autonomous execution policy. Actions still use
their existing review/start controls. No new background review, notification
policy or automatic charter adoption is introduced.

### The lead speaks first

On the first eligible workspace open, the drawer delivers a host-written
introduction in an ordinary lead-agent session. It brings in the saved review's
findings, one highest-priority unfinished suggestion from that same review, its
reason/first step and source dates. Completed, active, cancelled, dismissed and
older-review suggestions are not presented as new starts. Changed or unknown
goal matches are qualified instead of recommending obsolete work. This initial
message uses the saved model assessment, not a second automatic model call.
Subsequent questions use normal lead chat and the evidence reader above. The
stored introduction stays assistant-authored. For Claude's user-first wire
contract, its adapter quotes an assistant-only opening as historical context
inside the first actual user turn; it never inserts a dummy user request or
promotes that prose to system instructions.

A capability-associated File Curator lead instead uses File Janitor's saved
filing state, not the presence of a goal assessment. Its welcome offers two or
three numbered discussion choices: review the pending batch, discuss uncertain
items only when some are still pending, and understand the filing rules. It
recommends starting with the proposed filing. With no batch it offers the
existing **Scan now** control; missing setup or unavailable evidence gets an
honest setup/status choice, never invented findings or a Goal settings detour.
The reader checks the current owner, Curator association and saved read grant,
returns counts/dates only, and never probes the managed folder, scans, or changes
filing state. Batch completion and record-read times stay distinct. Choosing a
topic is not consent for a scan or file action. These choices complete the
one-time welcome rather than waiting for a separate goal review. An unanswered
older generic welcome may receive them once; user-led chats remain untouched.

`POST /api/workspaces/{workspaceID}/lead-conversation/open` is authenticated to
the workspace owner and exact local lead, fenced against workspace version
changes. It has no model/run/task dependency. Migration 87 adds a local delivery
receipt, with workspace-delete cascade and factory-reset inventory. The receipt
survives session deletion; imports do not carry permission or delivery state.
Transactions deduplicate concurrent tabs and combine a ready assessment with
the introduction, or append it at most once later. A failed review (or a
completed review with no assessment) gets an explicit non-retrying fallback.
With no review yet, the lead says so and asks about the goal rather than
inventing findings or silently starting a review. Existing first-open admission,
manual-only consent and setup gates are unchanged.

The drawer waits for workspace/setup readiness and a resolved workspace lead.
It does not replace a draft, an active turn, a new-conversation choice or another
recipient. Existing lead sessions suppress the introduction. Once a user has
spoken, the delayed unsolicited assessment is suppressed too. Reloads, another
tab, moved/deleted sessions and replacement leads do not recreate the greeting.
Closing the drawer keeps later delivery quiet. A bounded poll and the existing
goal-state/setup events observe readiness; no persistent background worker is
added. HQ/groups and assistant-home programs retain their own entry flows.

`./scripts/e2e-goal-suggestions.sh --lead-welcome` checks the real browser,
creation, first-open review, persisted conversation and follow-up using only the
isolated fixture model. Node tests cover UI deferral and stale-response guards;
Go tests cover identity, version/owner fences and transactional delivery.

## Preconditions

A precondition is a host-observed fact that an unmet step must come before
any other work (`MissionPromptInputs.Preconditions`, `MissionPrecondition`).
Each has a `Kind`, the host's own `Statement` of the fact, a `SuggestedTitle`
and `SuggestedFirstStep`, a `Why`, and an `Action` id for the ticket's button.
Everything is keyed by string ids, so a new kind adds one filler in the mission
bridge and one entry in the shelf's action map.

- When the list is non-empty, `BuildMissionSystemPrompt` adds a
  `## Preconditions` section just before the output contract. It states each
  fact and its task, and asks for those tasks before any other work, with the
  suggested title exactly, listed first (and named as `next` when the answer
  has one). Without preconditions the prompt is unchanged.
- The one kind today is `backup_protection`, for a File Janitor workspace (one
  with a File Curator companion). The bridge reads the managed folder's stored
  protection fact through the same reader as the review console and the
  curator's chat (`fileCuratorProtection`). The owner is the workspace's, or the
  local user's, and the fact counts only when its `WorkspaceID` matches. A fact
  that is `not_covered`, `unknown` or `configured` (a configuration is not a
  completed backup) yields "Back up <workspace> before the first filing batch"
  with the first step "Press Back up and approve one metadata read."
  `covered`, `unavailable`, a failed read, no reader or another workspace kind
  yields none. So does a task that is already open or was dismissed as Not
  useful. A hired agent's first look never carries preconditions. The read is
  stored metadata only; no provider is probed and no file is read.
- When the answer is stored, a kept ticket whose normalized title matches a
  precondition is tagged under `goal_suggestion` with `precondition`, `action`
  and `source: "model"`. A precondition the answer left out, whose title is not
  open or dismissed, is stored by the host as one ticket with the same keys and
  `source: "host"`, high priority, counted in the run record and logged at info.
  So is one whose run failed or answered unreadably: the fact is the host's own
  and needs no model. This is compiled code storing a suggestion, which every
  autonomy level allows.
- The run's precondition tickets are moved to the top of the Backlog order
  (`BacklogService.Reorder`; order is a rank, not priority). On the Backlog
  shelf, a ticket whose action is `check_protection` leads its action row with
  **Back up**. That button reads `GET
/api/workspaces/{id}/file-janitor/protection` and opens the chooser or the
  Backup & Recovery console with no review-return trip. A `covered` fact
  replaces the button with "Covered · evidence observed <local time>". Nothing
  closes the ticket automatically.

## When a run starts

Creating a workspace stores intent only (`GoalFirstRunPending`). Creation, an
inbox folder hire, the Setup Wizard and a precondition start no run of their
own; the first-open admission below is unchanged by them. An eligible
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
its recipe/tool restrictions. Neither action chains the other. **Hire and start**
is the consent for exactly one such look: the setup starts it once when it
finishes (create or bind the agent, create the workspace, link the folder
read-only, run the first look read-only, under Watch) and covers no goal run,
cadence or write into the folder. A look can be stopped and started again by
hand. When it finishes, its suggested next task waits under the assistant's
Needs you; **Start this** creates and starts it as a Ready task assigned to the
same agent, held to the workspace's autonomy gate (context
`autonomy_gate: workspace`, Watch by default), and **Not now** adds its title to
`DismissedSuggestions`. An explicitly enabled cadence remains a separate
automation choice, not something a default goal enables.

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

Notifications are a separate feature. Goal runs have no notification policy:
the old `notification_policy` field was never read, and has been removed. A
workspace that still stores it loads normally and drops it on its next save.
