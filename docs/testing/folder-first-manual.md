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
this folder** after the local preview. Expect one factual card: folder heading/date, bounded or partial look,
**Attached folder: contents not read**, and at most three non-root observed rows.
Show more reveals only remaining recorded rows, not omitted/unscanned folders.
Scan details contains precise time, overlap-safe counts and coverage limits.
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

When fresh setup is available, secondary **Optional: review setup** appears
below the discussion choices. Click it and separately choose the whole folder
or project when multiple scopes are offered. Discussion choices never preselect
setup. The existing review card explains effects before confirmation; an existing
pending/running/stopped/completed review keeps its actual status/action even when
there are no new options. Direct composer **Review setup** still works without a
model. Neither Send, conversational “yes”, reload nor opening either chooser
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
