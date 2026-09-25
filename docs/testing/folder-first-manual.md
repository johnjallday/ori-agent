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

The browser script drives a **fresh** sandbox, asserts the visible receipts and captures PNGs. Don't call the `hqcard` or `hq` smoke recipe *before* the browser script; they change the initial hire state. The lower-level recipes are useful in a separate sandbox: `./scripts/smoke.sh showfolder http://localhost:8951 hqcard` leaves the setup card pending, `hq` provisions an HQ, and `today` prints the three Today sections and source health. `./scripts/smoke.sh showfolder http://localhost:8951 scan documents` inspects an offer after provisioning. The seed steps change only the sandbox path you give them.

Inspect screenshots at full size (especially `02c-home-action-opens-hq.png`, `03-hq-receipt.png`, `04b-home-action-opens-chooser.png`, `04d-inline-chooser-dark.png`, `04e-more-menu-dark.png`, `11b-more-menu-phone.png`, `04c-new-workspace-remains.png`, `05-four-missions.png`, `06a-avatar-digests-folder.png`, `08-project-receipt.png`, `09-corpus-receipt.png`, `11-today-phone.png`, and `12-today-degraded.png`). Check these behaviors:

1. Before hiring, the primary Home **Explore a folder** action is hidden; **New Workspace** remains available as the advanced path. After hiring, the Home action opens the *existing* HQ setup card; the `/?quest=build-hq` Map walkthrough is still opt-in.
2. Confirm the suggested workspace directory before building My HQ. Expand the **Setup receipt** in Done to see the observed workspace, directory, and Daily Brief schedule; the completed HQ card is no longer in Needs you. On a reload, the workspace and schedule are reloaded from the server, but a past directory is not guessed from the current settings. Once active, Home opens the folder chooser. The empty Map and assistant launcher also link to the same chooser.
3. Needs you comes first. The folder scene, chooser, and resulting offer are one card, already open whenever an active/paused assistant's Today panel is shown—there is no inline **Explore a folder** button. Other requests are under the collapsed, keyboard-operable **Also needs you** disclosure. The first-time hand-over line appears once after HQ is built and scrolls the open drawer to the chooser; reopening Today keeps the chooser visible but does not repeat that line. Select a folder: its icon moves toward the actual assistant portrait **while scanning**, then server-observed findings appear as badges and the chooser stays available to try another folder. Errors show no new findings. Expand **Why this suggestion?** for the reason. Reduced-motion users see the same information without movement. Reset/restart re-arms the first-time line. The visible board has four missions; retired HQ quest history is still loadable but is not a visible mission.
4. In the seeded Documents/Thesis folder, choose **Set up project** and confirm. The writing blueprint, linked folder, roles, first task, and route come from the *persisted workspace receipt*, not preview guesses. Repeating the exact Set up request returns the same workspace and does not duplicate starter tasks. In the seeded Desktop corpus, the research blueprint and sources-index task appear.
5. Today shows **Needs you**, **Working on**, and **Done**, with bounded seven-day results for both created projects. The heading and short next-check-in line stay legible; the **More** menu retains Personal HQ, Working agreement, workspace memory, remembered-facts review, conditional interview, and Manage agents. Open it by keyboard, press Escape to close only the menu, and check the same controls at phone width. On a narrow viewport the labels remain readable and no section disappears. If one source is unavailable, the footer names it with a Retry button; it does not silently claim an empty day. Confirm the old standalone Today sections are not rendered.
6. Open **New Workspace** from Home after the assistant is active. The modal still works, and its **Import Folder** advanced option remains separate from the assistant's folder intake. Closing the modal does not dismiss the assistant relationship. The Map's empty-state create/import actions still open the same modal; the old map canvas create pad was removed before this feature.

Automated browser checks from fresh sandboxes:

```bash
./scripts/e2e-fresh.sh tests/personal-assistant-foundation.spec.ts tests/personal-assistant-foundation.a11y.spec.ts tests/personal-hq-daily-brief.spec.ts tests/starter-missions.spec.ts tests/folder-first-scene.spec.ts tests/domain-specialist-onboarding.spec.ts -- --workers=1
```

Current `origin/dev` browser baseline: the unrelated Group creation tests in `home-workspace-cockpit.spec.ts` (2), old Ask Ori routing assertions in `ori-guide.spec.ts` (6), and Task Output/Settings directory cases in `smoke.spec.ts` (4) fail identically against `./scripts/e2e-fresh.sh --rev origin/dev`. Do not rewrite their behavior to make this feature appear green; compare the targeted spec against that baseline instead. The folder-first foundation, a11y, Daily Brief, missions, and specialist specs pass in fresh sandboxes.

Keep the demo sandbox only for inspection; remove it when finished. Do not save its local directory paths or state to tracked files.
