# Folder-first manual test

Run only on an isolated disposable server. **Never use a server started with your real `HOME`**: folder chips and Agents are resolved from that directory. `scripts/demo-server.sh` sets both `HOME` and `ORI_DATA_DIR` to a temporary sandbox and disables desktop opening. No model or provider credentials are needed for this walkthrough.

From the feature worktree, paste the entire block. It uses a free port, waits for the build **and** HTTP server, and stops only the server it started:

```bash
bash <<'BASH'
set -euo pipefail
port=8951
if lsof -nP -t -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1; then echo "Port $port is in use" >&2; exit 1; fi
log=$(mktemp /tmp/ori-folder-first-demo.XXXXXX)
shots=$(mktemp -d /tmp/ori-folder-first-shots.XXXXXX)
./scripts/demo-server.sh "$port" >"$log" 2>&1 &
server=$!
trap 'kill "$server" 2>/dev/null || true' EXIT
sandbox=''
for i in $(seq 1 120); do
  sandbox=$(awk -F= '/^SANDBOX=/{print $2; exit}' "$log")
  if [[ -n "$sandbox" && -d "$sandbox" ]] && curl -fsS -o /dev/null -m 2 "http://localhost:$port/agents" 2>/dev/null; then break; fi
  if ! kill -0 "$server" 2>/dev/null; then tail -25 "$log" >&2; exit 1; fi
  sleep 1
done
case "$sandbox" in /var/folders/*/ori-demo.*|/tmp/ori-demo.*) ;; *) echo "No disposable sandbox; see $log" >&2; exit 1;; esac
curl -fsS -o /dev/null "http://localhost:$port/agents" || { echo "Server not ready; see $log" >&2; exit 1; }
./scripts/smoke.sh showfolder "http://localhost:$port" seed "$sandbox"
./scripts/smoke.sh showfolder "http://localhost:$port" seed-corpus "$sandbox"
node scripts/demo-folder-first.mjs "http://localhost:$port" "$shots"
printf 'Screenshots: %s\nServer log: %s\n' "$shots" "$log"
BASH
```

The browser script drives a **fresh** sandbox, asserts the visible receipts and captures PNGs. Don't call the `hqcard` or `hq` smoke recipe _before_ the browser script; they change the initial hire state. The lower-level recipes are useful in a separate sandbox: `./scripts/smoke.sh showfolder http://localhost:8951 hqcard` leaves the setup card pending, `hq` provisions an HQ, and `today` prints the three Today sections and source health. `./scripts/smoke.sh showfolder http://localhost:8951 scan documents` inspects an offer after provisioning. The seed steps change only the sandbox path you give them.

Inspect screenshots at full size (especially `02c-home-action-opens-hq.png`, `03-hq-receipt.png`, `04b-home-action-opens-chooser.png`, `04d-inline-chooser-dark.png`, `04e-more-menu-dark.png`, `11b-more-menu-phone.png`, `04c-new-workspace-remains.png`, `05-four-missions.png`, `06a-avatar-digests-folder.png`, `08-project-receipt.png`, `09-corpus-receipt.png`, `11-today-phone.png`, and `12-today-degraded.png`). Check these behaviors:

1. Before hiring, the primary Home **Explore a folder** action is hidden; **New Workspace** remains available as the advanced path. After hiring, the Home action opens the _existing_ HQ setup card; the `/?quest=build-hq` Map walkthrough is still opt-in.
2. Confirm the suggested workspace directory before building My HQ. Expand the progress row ("N in progress · M done today"), then the **Setup receipt** in Done, to see the observed workspace, directory, and Daily Brief schedule; the completed HQ card is no longer in Needs you. On a reload, the workspace and schedule are reloaded from the server, but a past directory is not guessed from the current settings. Once active, Home opens the folder chooser. The empty Map and assistant launcher also link to the same chooser.
3. A ready drawer is one conversation with a pinned composer. Unrelated attention stays behind **Show**; expanding it retains its original cards and forms. **Add folder**, Home's **Explore a folder**, the mission's Start and `folder=show` open the same chooser. They do not fabricate a user message, scan automatically, or send a model request. First-time guidance points to Add folder; reset re-arms that guidance, not a conversation turn. Choose Documents: the existing portrait animation runs during the local request, then opens **Tree + Chat** with a bounded **Local snapshot · not sent**. Read the coverage/disclosure: only recorded names/relationships, kinds, counts and markers are available; file contents have not been read. Independent checks set discussion topics, not access or descendants. **Back to chat** preserves the attachment, focus and exact draft, returning to the compact preview. Send is the separate model-sharing action. Cancel or a failed replacement preserves the prior folder and draft. Reduced motion conveys the same status without animation. The visible board still has four missions.
4. Choose **Review setup**, explicitly select Thesis, then **Review selection**. Review saves a canonical event but creates no workspace and needs no model. The existing card names the candidate/type and proposed effects. **Keep chatting** closes it without a decline or learning; review again to proceed. **Adjust name** changes the workspace name, not the source-folder label. Confirm **Set up**. Blueprint, linked folder, roles, first task and route come from the persisted receipt, not preview guesses. Replay returns the same workspace and does not duplicate starter tasks. Replace the attachment with Desktop and explicitly review the whole-folder corpus candidate; its research blueprint and sources-index task appear when installed. Reload recovers receipts. Removing context does not undo completed setup. After restart/expiry, repick before new setup; saved observations alone are not a filesystem grant.
5. The drawer shows **Needs you**, then the progress row, which expands **Working on** and **Done** in place, with bounded seven-day results for both created projects. The assistant's name and the short next-check-in line under it stay legible; the header's **More** menu (the "…" button) retains Personal HQ, Working agreement, workspace memory, remembered-facts review, conditional interview, and Manage agents. Open it by keyboard, press Escape to close only the menu, and check the same controls at phone width. On a narrow viewport the labels remain readable and no section disappears. If one source is unavailable, the footer names it with a Retry button; it does not silently claim an empty day. Confirm the old standalone Today sections are not rendered.
6. Open **New Workspace** from Home after the assistant is active. The modal still works, and its **Import Folder** advanced option remains separate from the assistant's folder intake. Closing the modal does not dismiss the assistant relationship. The Map's empty-state create/import actions still open the same modal; the old map canvas create pad was removed before this feature.

7. Repeat review and Send from the drawer on **Settings**, not only Home. The same thread, card and receipts must render in place with one controller. In a disposable test, delete a conversation that has a pending review, then explicitly close that old review: it must become inert without a workspace/preference or source access, and a new conversation must be able to review another selection. A temporary read failure must not be treated as deletion. Never delete real conversations for this check.

Automated browser checks from fresh sandboxes:

```bash
./scripts/e2e-fresh.sh tests/personal-assistant-foundation.spec.ts tests/personal-assistant-foundation.a11y.spec.ts tests/personal-hq-daily-brief.spec.ts tests/starter-missions.spec.ts tests/folder-first-scene.spec.ts tests/domain-specialist-onboarding.spec.ts -- --workers=1
```

## Tree + Chat discussion focus

Repeat on Home and Settings using only synthetic files in the disposable HOME.
Successful new attachment opens the existing drawer's explorer, not a new
conversation. Desktop shows independently scrolling tree/chat panes; phone
starts on Tree with Tree/Chat controls and the same pinned composer. Expand a
recorded folder and independently check it and a child file: neither cascades.
Names/relationships must match the bounded local scan, never a model guess.
**Select all** checks all recorded topics, including collapsed children, when
these fit the eight-topic/byte/distinguishability limits. On larger/ambiguous
snapshots the label is **Select all · Whole folder**: it clears individual
checks, announces whole-folder focus and never silently selects only some
entries. Empty trees disable the control; summary-only history does not get one.
Open the bounded/partial disclosure for scan time, limits and omitted entries;
expanding cannot recover omitted/unscanned entries or read file contents.

Enter a Unicode/whitespace draft. Checks, expanding, Tree/Chat and Back to chat
must retain it exactly. Back to chat also retains attachment and focus; use
Tree + Chat to reopen locally. Only Send shares the bounded metadata with the
configured model. Focus is discussion emphasis, not a privacy filter, read
permission or selection of all descendants. Clear focus means whole-folder
discussion. At most eight distinguishable topics fit the bounded request; long
labels may require fewer, and ambiguous sanitized names cannot be checked.

Send, then change checks while the reply is pending. The sent user badge must
remain immutable; the new checks belong only to the next turn. Reload preserves
canonical sent badges and prose but clears next-turn checks and stays in ordinary
chat. Cancel/failed picker preserves the prior focus; successful replacement,
detach and New clear it. Legacy snapshots have summary-only history, no invented
tree. Saved canonical trees may be discussed without a rescan; setup/read rights
still require their separate live authority and review gates.

Controlled host/tree/focus validation (no vendor credentials):

```bash
ORI_DEMO_NO_CODEX=1 ORI_SKIP_CACHE_PRUNE=1 \
  python3 scripts/assistant-workspace-demo.py --folder-response \
  --folder-response-evidence-stage explorer --port 8931
```

This is host/privacy/persistence/layout evidence, not live-model, native picker,
native zoom or screen-reader evidence. Existing compact-summary, draft shortcuts
and explicit setup journeys remain below.

## Compact folder discussion

With an approved configured model, return **Back to chat** and Send **Explore
this folder** after the local preview. Expect one compact factual card: folder heading/date, bounded or partial look,
and **Attached folder: contents not read**. Recorded rows and markers are behind
**Scan details** by default; its nested Show more reveals only remaining recorded
rows, not omitted/unscanned folders. Scan details also contains precise time,
overlap-safe counts and coverage limits.
The interpretation should be short (normally two or three sentences / about
80 words for initial discovery), cautious about names/markers, and useful without
a setup pitch. Specific questions and requests for detail override that default.
Follow-ups should not restart the inventory. This is model guidance, not a hard
limit; saved prose is not rewritten.

The current saved answer offers **Discuss the collection/this folder** and,
when observed children exist, **Choose a project/folder…**. These prepare an
editable question, not Send, a rescan or setup. Choose explicitly; Cancel/Escape
returns to the trigger without changing attachment or draft. Try a non-empty
Unicode/whitespace draft: it must remain exact, gain focus, and say to send or
clear it first. Identical child names/markers must be disabled rather than
silently resolved to a target. Other observed children remain discussable even
if only one has a setup option.

Reload preserves one dated factual card per unchanged snapshot and at most one
eligible current choice strip, with saved-observation wording. Lost/expired selections permit only canonical historical
discussion; repick before inspection/new setup. Detach, replacement, New,
imported/legacy prose, failed/unsaved replies or trimmed-away eligible answers
must not revive old shortcuts. Long replies scroll normally with the composer
pinned. Check light/dark, phone and keyboard focus/Escape nesting; no nested
transcript frame or horizontal overflow. The editable textarea may scroll long
text; exploration uses a separate tree pane, not a second transcript.

When fresh setup is available, a compact host-backed proposal appears below
the discussion choices. **Choose setup scope** opens the one native chooser
beside that proposal when multiple scopes are offered. It starts empty and uses
a **Setting up** heading; discussion choices never preselect setup. A single
candidate may have an explicit **Review workspace setup** shortcut. The existing review card explains effects before confirmation; an existing
pending/running/stopped/completed review keeps its actual status/action even when
there are no new options. Direct composer **Review setup** (or **Review workspace setup**, depending on
the offered type) still works without a model, using a labelled attached-folder
handoff at the composer end of the same pane, not a fabricated assistant reply. Neither Send, conversational “yes”, reload nor opening either chooser
creates a workspace or grants file access. Historical/unavailable context,
unrelated pending review, content refusals and failed/unsaved answers have no new
setup suggestion.

The reproducible controlled-provider journey needs no vendor credentials:

```bash
python3 scripts/assistant-workspace-demo.py --folder-response --port 8931
# Preserve prior evidence when running final validation:
python3 scripts/assistant-workspace-demo.py --folder-response \
  --folder-response-evidence-stage final --port 8931
```

It drives real known-folder selection, Send, normal drafted follow-up and
canonical reload on Home/Settings, with only loopback provider prose scripted.
Album-like fixtures use Documents; document fixtures use Desktop. It checks
provider-call counts, unchanged snapshots/resources and unread content sentinels.
It is host/persistence/privacy/layout evidence, **not live-model quality**.

For the no-model canonical review journey, run:

```bash
./scripts/e2e-fresh.sh --sandbox-env ORI_FOLDER_CHAT_SANDBOX tests/personal-assistant-folder-chat.spec.ts -- --workers=1
```

That file distinguishes real-host setup from browser-fixtured model/controller
tests, including stale callbacks, lost historical authority, failed saves,
delayed replies, imported/trimmed history, literal long/hostile names and full
long prose. Chromium accessibility-tree names, native control semantics, focus,
contrast samples and reflow are checked without a native screen reader. It also
checks Home/Settings hydration, delayed-choice Escape/focus, light/dark phone
layouts and 200%-zoom-equivalent reflow (not native OS/browser zoom automation).

Previous folder-context evidence is indexed at
`tasks/evidence/assistant-chat-folder-context/final/README.md`, with its guide at
`tasks/test-guide-assistant-chat-folder-context.md`. The controlled folder-chat
capture uses the production HTTP provider adapter with a deterministic loopback
provider, **not a live LLM**. Live vendor-model and native-picker checks remain
separate and NOT RUN in that evidence. Conditional music-provider suites require
their documented isolated-provider prerequisites; skips are not integration proof.

Compare suspected baseline failures with `./scripts/e2e-fresh.sh --rev origin/dev <spec> -- --workers=1`, recording the revision and exact failure. Do not weaken unrelated assertions or reuse an old baseline claim after `origin/dev` changes. The feature checklist and `tasks/evidence/assistant-chat-folder-context/` hold the actual run logs and screenshots.

Current response-UX evidence and its post-implementation guide are local under
`tasks/evidence/assistant-folder-response-ux/` and
`tasks/test-guide-assistant-folder-response-ux.md`. Report live-model/native-picker
checks as NOT RUN unless separately exercised; do not count skips as passes.
Keep the demo sandbox only for inspection; remove it when finished. Do not save its local directory paths or state to tracked files.

## Conversation-first acceptance

See [the conversation-first contract](../architecture/assistant-conversation-first.md).
Use only synthetic folders; this controlled real-host/store journey requires no
vendor credentials or specialized plugins:

```bash
ORI_SKIP_CACHE_PRUNE=1 python3 scripts/assistant-workspace-demo.py \
  --conversation-first --port 8954 \
  --evidence-dir tasks/evidence/assistant-conversation-first/manual-check
```

The runner builds and launches `wt demo`, then checks ordinary chat and Tree +
Chat on Home, Settings and a real workspace-group page. Inspect geometry before
any corrective scroll or focus: one turn-local pending row; a complete short
reply and nearby actions; the beginning of a long reply; the pinned composer.
While a reply waits, intentionally scroll history up and type an exact draft.
Arrival must keep the visible row offset, keyboard focus, tree and outer page;
only explicit **New reply** reveals/focuses the accepted answer. Reload reveals
the newest exchange once; reopen retains intentional reading.

Today source failures remain behind the compact Show disclosure with Retry;
hiring/model/HQ prerequisites and request failures remain actionable. Local
folder evidence is folded without hiding contents-not-read, sharing, dated or
partial-coverage disclosures. Proposal art is decorative: type, proposed versus
existing state and effects are text. An unknown destination is not standalone;
a verified supporting link is not a new workspace. Collection/library cues do
not promise one workspace per observed folder.

Open the native setup chooser, cancel with Escape, and verify local focus return
and exact draft preservation. Selection alone must make no model/review/create
request. Explicit Review saves one canonical card; its own confirmation executes.
During a pending request, newer typing must not be overwritten or lose focus.
A focused obsolete choice remains explicitly inactive until blur, with no setup
authority. Verify canonical closed/blocked/completed states and receipt-derived
links, not name-built routes. The no-model confirmation suite above checks real
creation/reload and unchanged sources; loopback prose and browser prototypes are
not live specialized execution evidence.

Repeat with light/dark themes, 390px and 320px layouts, reduced motion and CSS
200%-equivalent reflow. Native browser zoom, picker interaction and human screen
reader operation are separate manual checks, NOT RUN unless actually exercised.
Feature evidence and the post-implementation guide stay ignored/local at
`tasks/evidence/assistant-conversation-first/final/` and
`tasks/test-guide-assistant-conversation-first.md`.

## Setup readiness and model return

On both Home and Settings, attach a synthetic project folder without sending a
chat message. A single recognized project immediately opens What you need,
including before its plugins supply a complete plan; no Review setup click is
needed. Multiple candidates start unselected and retain their review control in
Tree + Chat/compact preview; unsupported folders acquire no invented setup
action. Keep an exact Unicode/whitespace
draft and brief through every disclosure, cancellation and return.

The current review leads with each known plugin's name, purpose, operation,
versions and source disclosure. Check install/update/enable/ready independently;
unknown or failed checks must not claim zero downloads. Missing Home metadata may
require the separately opened existing Guided Setup. Requirements without a
complete plan cannot confirm setup. Plugins are not the REAPER desktop app or
model runtimes/weights. The smaller proposed outcome follows requirements and
all material consequences; Full setup details still contains the complete plan.

The project agent's model is separate from chat configuration. An unreadable
blueprint says not checked; known missing configuration offers the applicable
Settings/agent route instead of confirmation. Block the popup once, then allow
it. Cancelling or saving Settings and returning must preserve the folder, exact
draft/brief and canonical review without installing or executing anything.
Explicit Check again rereads current facts. Configured is not Connected. A
changed material model requires a fresh confirmation; accepted runs keep their
recorded consent and recovery. On New Workspace, verify the same facts under its
own session/name/brief, not the assistant's owner. A failed or late recheck must
not enable stale confirmation or gate a newly attached model-free folder.

After confirmation, waiting/working/done/failed rows describe saved milestones,
not download bytes or percentages. A named failed prerequisite retains completed
work. Restart/expiry/changed-selection reasons outrank retry; on reopening, the
reason and re-pick action must be visible without scrolling past a diagram.
Reopening/reading a saved result does not execute setup or a first look again.

Fresh metadata/review checks (no provider credentials or plugin installation):

```bash
env -u CODEX_HOME -u OPENAI_API_KEY -u ANTHROPIC_API_KEY -u GEMINI_API_KEY \
  -u ORI_REVIEWED_HOME_PROVIDER_DEV_SOURCE -u ORI_REVIEWED_INTEGRATION_DEV_SOURCE \
  ./scripts/e2e-fresh.sh --sandbox-env ORI_READINESS_SANDBOX \
  tests/assistant-setup-readiness.spec.ts -- --project=chromium --workers=1
```

To additionally exercise real reviewed integration and Home-provider installation,
add `--env ORI_READINESS_INSTALL=1` to that fresh runner. It uses Install on each
requirement card, reads the full inline source/version/trust disclosure, and
explicitly confirms only that plugin. Cancel or Escape before confirming and
check focus/draft retention. The Home provider can be installed first. REAPER
installation leaves it disabled until a separate reviewed Enable confirmation;
the Home provider's existing installer discloses installation and enablement
together. Successful explicit repair refreshes readiness automatically, but never
confirms workspace setup, configures a model or starts an agent. Record the
packages actually selected by review. This opt-in case is separate from the complete provider-phase
receipt/report acceptance below; the default metadata run skips it.

For a continuous fresh inline → real Settings → explicit setup → saved live
report check, run this same spec with `ORI_READINESS_INSTALL=1` and the paired,
explicitly authorized `ORI_READINESS_LIVE_PROVIDER` / `ORI_READINESS_LIVE_MODEL`.
It keeps the same canonical review, selection, exact draft and brief through
configuration/recheck, then confirms setup only after showing the configured
project model. Existing Settings may bootstrap the **Ask Ori system assistant**;
that is separate from project staffing and is not an inline-package effect.
`ORI_READINESS_REPORT_TIMEOUT_MS` accepts 1000–900000 (default 180000). Use a
fresh `demo-server.sh` sandbox and `e2e.sh` so a browser timeout does not invoke
the fresh runner's automatic server shutdown while accepted inference is running.
Keep the store and inspect its task before any further action; never repeat an
accepted setup just to obtain a green browser result.

A CLI run must not inherit unapproved native MCP servers, hooks or user rules.
Use a separately isolated invocation/configuration without editing shared CLI
settings or changing workspace native-tool permissions, and record the actual
CLI version, authentication method and per-run policy. A policy-only test shim
must forward model responses unchanged and must not silently substitute a model;
its evidence is live inference under that policy, not the default shared profile.
Never put credentials in evidence files.

Presentation fixtures in `visual-folder-agent-setup.spec.ts`, controlled
model-return tests and preinstalled packages are not fresh installation or live
report evidence. The visual suite includes actual 200% Chromium Page zoom in a
disposable browser profile, verifying unchanged outer width and halved content
width in addition to the separate viewport-reflow matrix. It does not establish
native folder selection/drop/cancel or human screen-reader operation. For native
folder checks, launch `scripts/demo-server.sh --native-picker <port> <sandbox>`;
the browser still does not open automatically. Use an unlocked desktop and only
the synthetic folder, cancel a replacement, and record which native drop gesture
was actually exercised. Never unlock the machine, grant OS permissions or change
system preferences on the user's behalf. Keep any unexercised native/live checks
explicitly NOT RUN; DOM/accessibility assertions do not prove human screen-reader
operation.

## Reviewed REAPER setup acceptance

`tests/one-card-folder-setup.spec.ts` uses two **fresh disposable stores**:
`ORI_ONE_CARD_PHASE=no-model` checks no-model readiness/recovery and changed consent;
`ORI_ONE_CARD_PHASE=provider` checks actual new/bound staffing and a finished
first report. Both phases are required. Supply `ORI_ONE_CARD_SANDBOX` and an
explicit `ORI_ONE_CARD_PROVIDER` / `ORI_ONE_CARD_MODEL` through `scripts/e2e.sh`.
Suppress ambient credentials except the explicitly authorized provider's
credential/login context. A signed-in Codex CLI alone is not selection or consent. Slow local inference can explicitly set
`ORI_ONE_CARD_REPORT_TIMEOUT_MS=900000` (maximum 15 minutes; default 3 minutes).
This only extends the report wait; a finished report is still required. The
provider phase enters an explicit read-only brief through the normal review field;
it does not supply a report, a tool response, or altered execution permissions.
A timeout does not cancel the server task. Inspect its state before repeating anything, and
use a new provider-phase store rather than replaying setup into a used store.

Confirm the sandbox workspace root. If the initial offer has no plan, follow its
existing guided setup to install/enable the reviewed integration and Home provider;
record the actual versions and which UI confirmation authorized each installation.
If the installed blueprint establishes missing model configuration, confirmation
must remain blocked; do not force a setup POST to manufacture a paused run. A
pending/unreadable blueprint is a different case and may reach the existing
model-repair stop after installation. Changing a model after approval does not authorize a
silent retry: **Review changed setup** opens the existing guided continuation.
The provider phase instead reviews its intended model before confirming the hire.

Follow **Open the full report** / **Read the full report** directly to the stored
task output, not only a ticket's status. Legacy ticket-only data offers **Open task**.
Reloading that view must preserve workspace/task IDs, execution counts and source
bytes; it must not run the task or
its suggestion. A successful HTTP response containing only ticket status is not
proof that a report is readable. After slow inference or a browser interruption,
`tests/one-card-saved-report.spec.ts` can recheck this read-only tail with explicit
`ORI_ONE_CARD_SANDBOX` and `ORI_ONE_CARD_REPORT_OFFER_ID`. It shares the provider
suite's assertions, never provisions or retries work, and supplements rather than
replaces fresh review/staffing and live-execution evidence. The default source
fixture is `Downloads/Third`; set `ORI_ONE_CARD_REPORT_FIXTURE=desktop` for the
fresh-inline `Desktop/Session.rpp` journey. Optional
`ORI_ONE_CARD_REPORT_EVIDENCE` captures the actual report body after navigation,
not an initial viewport. The checks cover a fresh page and an already-open
drawer, close that drawer normally before using Tickets behind it, and compare
all report body paragraphs/code lines without treating Markdown fence language
tags as visible content. A finished task is not
re-announced in the workspace's first-task banner on a new visit.

A listed model is not proof of usable execution. For local models, verify the
**loaded** context capacity, not only the advertised maximum. Ori's
`LM_STUDIO_CONTEXT_WINDOW` sets its prompt budget; it does not resize LM Studio.
Do not reconfigure a shared model server without authorization. A ready workspace
with a waiting/failed look does not satisfy the finished-report check. Native
picker selection/cancel and native browser zoom remain separate evidence.

## Software project folder

A folder that holds a software project is reviewed, set up as a Code Project
workspace bound to that exact directory, and given one read-only first
assessment authorized by the reviewed hire. Nothing is installed and nothing
is written into the project.

Automated, in a real browser, with a scripted loopback provider (no live model):

```bash
./scripts/e2e-software-folder.sh
```

It starts one isolated demo server, runs the suite's first phase
(`tests/software-project-folder-setup.spec.ts`), stops and starts the server on
the same sandbox, and runs the second phase, so a paused setup surviving a
restart is checked for real. A third phase, on a server and sandbox of its
own, attaches a checkout through the assistant composer's **Add folder** and
sends it: the reply proposes that one folder as a workspace, and one click
reaches its card. A fourth, independent `reports` phase creates two unread stored
reports through actual setup/task execution, then checks Home/drawer handoff,
**Start this** and **Not now** using the same cases as `folder-hire-brief.spec.ts`.
The suite writes its own inert project folders
(Git metadata is plain text; no git command runs) and hashes them before and
after. Its one stand-in is labelled in the spec as the FIXTURE PROVIDER: a
local HTTP server that answers the model API, which the server's own `lmstudio`
provider is pointed at (`LM_STUDIO_BASE_URL`). The start route, the dispatch,
the evidence, the isolation and every screen are the product's own; only the
model's words are canned, and the spec reads back exactly what was sent to it.
Run directly through `scripts/e2e.sh`, the spec skips unless
`ORI_SOFTWARE_SANDBOX`, `ORI_SOFTWARE_FIXTURE_PORT` and `ORI_SOFTWARE_PHASE`
(`first`, `restarted`, `conversation` or `reports`) are set.

The shared [visual setup contract](../architecture/visual-folder-setup.md) keeps
review, saved milestones, recovery and the canonical result on one card. Use
**Full setup details** for the complete plan/receipt. Create/bind plans use
**Hire & start** / **Assign & start**; unknown staffing retains a generic setup
action. Setup completion is separate from the first look and its report.

With an explicitly intended real model, the whole journey in one command:

```bash
./scripts/demo-software-folder.sh               # a small unrelated project
./scripts/demo-software-folder.sh --ori         # a copy of this repository, as a linked worktree
./scripts/demo-software-folder.sh --add-folder  # the composer's Add folder instead of Home
```

`--add-folder` is a shorter journey of its own: a checkout whose marker is at
the root of the picked folder is attached in the composer and sent to the real
model, the reply's proposal is followed to the project's card, and its review
and Set up finish in the conversation. The reply is printed in the sandbox's
`add-folder.log`.

It starts an isolated demo server on a new sandbox, seeds the folders, and runs
the walkthrough's phases in order, restarting the server where the journey
needs it: `setup` (pauses with no model), restart, `resume` (re-pick/review if
folder authority was lost), `assess-interrupt` (observes the first look already
authorized by setup), restart, `assess` (Try again uses the same failed task;
a finished look is only read), `repeat`. A look that finished before interruption
is reported as **NOT RUN** for that interruption case, never rerun to manufacture
it. The report, workspace banner and Plans hand-off are followed. Like `wt demo`,
the server is given your Codex home (`$CODEX_HOME`, else `~/.codex`): confirming
the hire and explicitly retrying its look can make real model calls. Do not use
this live path as an implicit signed-in-provider fallback. The report is printed
in the sandbox's `assess.log`, and screenshots go to its `shots` folder.

To drive one phase by hand, start `scripts/demo-server.sh <port> <sandbox>`
yourself (export `CODEX_HOME` for a model: the demo server is otherwise fully
isolated and has no provider), seed with `scripts/software-folder-seed.sh
<sandbox> [--ori]`, and run `node scripts/demo-software-folder.mjs
http://localhost:<port> <sandbox> <shots-dir> <phase>`. `recognize` and `review`
each need a fresh seeded sandbox.

Check by eye:

1. Before Set up, the card names the workspace, the folder (linked where it
   is, read-only), the one assistant and its model, the planning-first mode and
   the first assessment, and says what was read and that no command was run.
2. The reviewed hire discloses its first look; accepting it may start that look
   on the server. Reopening the card/workspace must not start it again. A model
   configuration is not a verified connection. Observe pending/running/finished/
   failed look state independently of the ready workspace.
3. While it runs there is nothing to press twice. A run that does not finish
   says why in one sentence, offers Try again only when nothing has to be
   repaired first, and never shows a raw provider error or a path.
4. A finished assessment names its sources, calls build and test commands
   "declared, not run", and ends with Ori's own note of which files it was
   based on. **Open the assessment** opens the task's page, where the whole
   report is shown.
5. **Plan the suggested task** opens the workspace's Plans page with the
   suggestion in the box. Nothing is created until Create plan is pressed.
6. The project folder is byte-for-byte unchanged afterwards.
7. Through **Add folder**, a folder that is itself one project is proposed by
   name as one workspace. It never asks which of its subfolders to set up, and
   the line under the conversation agrees with the card (it does not say setup
   is unavailable while the card offers a review).

Evidence and the post-implementation guide stay ignored/local at
`tasks/evidence/software-project-folder-setup/` and
`tasks/test-guide-software-project-folder-setup.md`.
