# UI consistency — manual test guide

Branch: `feature/ui-consistency`. Groups 1–5 are committed through `d052a9662`.
This guide is a procedure, **not a broad native/assistive-technology certification**.
Scoped app-wide/browser checks, native Chrome zoom and the full current pinned
local gate passed; see `tasks/evidence-ui-consistency/group-6/curated/final-validation.json`.
PR delivery and human review/merge remain separate.

## Start an isolated, credential-free demo

Run from this worktree, using a free port (example8960, never historical8943):

```sh
mkdir -p .tmp/ui-consistency
sandbox="$(mktemp -d "$PWD/.tmp/ui-consistency/ori-demo.XXXXXXXX")"
env -i HOME="$HOME" PATH="$PATH" TMPDIR="${TMPDIR:-/tmp}" \
  GOWORK=off GOTOOLCHAIN=go1.26.0 \
  ORI_DISABLE_NATIVE_SECRET_STORE=1 ORI_DISABLE_EXTERNAL_MCP_IMPORT=true \
  ORI_DEMO_OPEN=0 ./scripts/demo-server.sh 8960 "$sandbox"
```

Keep that terminal running; manually open `http://localhost:8960`. The entrypoint
builds embedded assets, binds `.ori-demo-owner` to this worktree, and isolates
HOME/data. Rebuild after CSS/JS/template changes. Complete ordinary onboarding
inside this sandbox—do not hide overlays. Do not load credentials or a CLI home,
connect accounts/user folders, enable schedules, or start agents/workflows.

## Fixture prerequisites

- An empty demo proves only empty states. For owned workspace/Ticket/Note/Plan
  scenes, use the supported API fixtures in
  `tests/helpers/ui-consistency-workspace.ts` and their named feature specs.
  Layout projections are labelled; they do not prove successful persistence.
- Example scoped check, against the owned sandbox above:

  ```sh
  ./scripts/e2e.sh --port 8960 --env ORI_UI_SANDBOX="$sandbox" \
    tests/ui-consistency-workspace.spec.ts -- \
    --config=playwright.ui-consistency.config.ts --max-failures=1
  ```

  In the second terminal, set `sandbox` to the exact directory recorded by the
  launch above before using this command; do not create or adopt another root.
  For multiple named specs, `scripts/e2e.sh --each` starts a fresh browser per
  spec and the scoped config serializes engines. Do not blindly run seed,
  retirement, recovery, or preparation modes; their guards/state prerequisites
  are intentional. These tests normally retire only their verified own records.
- Real Team Home/Catalog consent/persistence uses the repository fixtures:
  `scripts/resource-studio-demo.sh test --port <free-port>`. Populated105-project
  library, upgrade/pause, and opaque-surface examples have separate owned datasets
  and opt-in specs/config. They prove host behavior, not installed/published
  packages, external DAWs/readers, native providers, or a model.
- Local retained roots/receipts in the findings are evidence, not portable
  dependencies or deletion authority. Resume only an owner-verified root using
  its recorded configuration; never adopt historical data or copy a real DB.

### Notes-station safety prerequisite

Use supported version-fenced Hire/HQ APIs without choosing a model. HQ setup
must explicitly send `schedule_enabled:false`; omission retains the existing
**enabled** default. This flag does **not** suppress the template help autostart.
Before opening HQ, validate the exact three newly created personal-ops template
records, complete nonsymlink inventory, and the never-run/unconsumed/unlinked
setup ticket's full readback. Retire **only that fresh setup ticket** through
the supported ticket DELETE API; preserve the two other records. A legacy DTO
can legitimately have version0—do not invent a revision. Do not fake consumption,
seed authorization tables, or briefly enable automation. The accepted journals
and `group-5/after/notes-station-validation.json` distinguish this preparation
from failed autostart probes. For the legacy suite, provide the fictional
`SECOND_BRAIN_VAULT`, local `AXE_CORE_FILE`, and `playwright.notes-station.config.ts`;
use a fresh fixture per engine so follow-up acceptance does not contaminate it.

## Golden paths and expected results

Use real populated IDs where required, not a missing-record shell.

1. **Home → workspace:** switch Map/Tree with actual controls, select a workspace,
   then follow its native link. Faces/art remain intact; selected state, drafts,
   navbar clearance and back/history remain correct.
2. **Workspace Overview/Map/Tickets:** switch each actual view and open
   Resources/Settings/Give work. Drafts survive incidental refresh; Escape/Cancel
   return to the actual opener. Create a manual ticket for **Me**, with Start now
   unchecked; edit/reload it. Board/list/detail agree and nothing runs.
3. **Notes:** open workspace Notes and focused `/notes/{id}`, edit fictional text,
   wait for real save feedback, reopen, follow a real Ticket link, and cancel the
   modal editor. Long content's last line and sidebar actions clear fixed UI.
4. **Plans:** inspect Active/History, actual draft/review/approved records, and
   bounded comparison. Duplicate/autosave/reload an owned draft. Genuine concurrent
   saves retain both versions for Keep mine/Discard and restore Save focus after
   resolution. Under-review content is read-only; an approved working draft may
   remain editable without altering its immutable reviewed version or effects.
   Never approve arbitrary work for photography or start execution.
5. **Agents:** collection search/filter/inspector, create draft, character/model
   picker Cancel, and real section-local metadata edit/save/cancel. Workspace
   agent identity stays workspace-owned; specialized Claude/Codex availability
   labels are not proof of a live CLI connection. Do not configure a provider
   merely to complete a screenshot.
6. **Utilities:** Settings/Profile real local save/cancel and theme/density;
   Skills import preview; Plugins trust/update Cancel; fictional Vault
   unlock/record flow; MCP disclosure; Models long labels; Workflows editing
   without execution; Usage/Review/Action Center long details and dialogs.
7. **Resource hosts:** populated Team Home roster, Catalog tabs/selected entry and
   canceled drafts; real populated library search/selection/unsent session form;
   upgrade-review Cancel and provider-paused metadata. Opaque example frame
   loads its own CSS/bridge; Close returns to its actual station door. Ori styles
   do not leak into the plugin document. Do not install into a user plugin store.
8. **Notes station:** real rules headline and read-only source statement; native
   handled disclosure, visible Open folder/Look again, actual single/bulk follow-up
   readback, forbidden private-note preview, and truthful rate-limit response.
   Source vault bytes remain unchanged, no model is configured, schedules remain
   disabled, and run-list API stays empty.

## Layout, keyboard and negative checks

- Light/dark ×320/390/768/1440 CSS pixels, documented900px height; also
  default/compact/roomy density. Normal checkbox/radio glyphs sit inside44px
  labelled narrow targets. Static sections do not lift; actions have clear roles.
- Keep the assistant visible. Scroll long regions and hit-test complete controls,
  including their corners. Wide tables/Canvas pan only inside labelled bounded
  regions; ordinary content must not overflow sideways.
- Use real Tab/Shift-Tab (macOS WebKit may require Option-Tab), Enter, arrows and
  Escape. No hover-only controls, lost drafts, hidden focused action, incorrect
  modal trapping, or focus return to a removed/wrong opener.
- Loading/error/missing ownership stays truthful, never empty/success. Failed
  saves/reviews preserve drafts and immutable authority; read-only agents do not
  become global editable profiles. Merely opening panels starts nothing.
- Run fallback and separately `--env ORI_UI_FONTS=real`; loaded Manrope/Fraunces
  faces must be proved, not inferred from `document.fonts.ready`. A failed font
  fetch is not a passing intended-font result. Check reduced motion and enabled
  text/control/focus contrast in both themes.

## Native zoom and remaining certification limits

Native Chromium200% is now exercised through the real **Appearance → Page zoom**
preference in an owned, non-incognito temporary browser profile:

```sh
./scripts/e2e.sh --port 8960 --env ORI_UI_SANDBOX="$sandbox" \
  --env ORI_UI_NATIVE_ZOOM=1 --env ORI_UI_EVIDENCE=after \
  --env ORI_UI_EVIDENCE_GROUP=group-6 tests/native-ui-consistency.spec.ts -- \
  --config=playwright.native-ui.config.ts --max-failures=1
```

Repeat with `--env ORI_UI_FONTS=real`. The test proves outer width1440 stays
unchanged while content width1440→720, DPR1→2, compositor native zoom2, CSS zoom1
and visual scale1. Its actual content height becomes406 (Chrome furniture uses
part of the900px outer height), not the synthetic450px equivalent. Four complete
cases cover eight routes each, both themes and font modes, with real owned
workspace definitions, keyboard focus/hits and Give work dismissal. The first-use
walkthrough stays visible beside its target. No shared browser profile is used.
Native photographs read the complete compositor without a clip: Playwright's
ordinary screenshot clip crops native-zoom windows. No rescaling or retouching
is applied. The test restores100% before closing its profile.

Earlier page-key and incognito settings probes failed and are not acceptance.
The720×450 CSS viewport remains reflow evidence, not native zoom. Native Safari/
WebKit browser-chrome zoom, OS select popups and assistive-technology behavior
are **not certified** by these scoped checks; record separate human observations
for those environments, never relabel their NOT RUN evidence as passed.

## Stop and delivery boundaries

From another terminal: `./scripts/stop-demo-server.sh 8960`. Stop only this owned
server. Preserve unexpected fixtures/journals; no blanket removal. Full
`make ci-local`, required disposable README capture/contract checks and the final
review remain separate gates. Never accept root README imagery/hashes, merge,
publish, enable auto-merge, or claim PR/release readiness from this guide.
