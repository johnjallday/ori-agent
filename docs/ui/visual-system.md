# Ori visual system

A consistency pass, not a redesign. Preserve Ori's character artwork, map
identity, native navigation, drafts, permission gates and confirmation flows.
This guide describes the shared foundation; page migration and app-wide
acceptance are still in progress. A passing pilot is not whole-app certification.

## Owners

| Owner under `internal/web/static/` | Responsibility                                                                         |
| ---------------------------------- | -------------------------------------------------------------------------------------- |
| `css/variables.css`                | Shared palette, fonts, spacing, control metrics, radius, elevation and modal layers    |
| `css/components.css`               | Buttons, fields, tabs, badges, headers/toolbars, sections and Bootstrap modal surfaces |
| `css/common.css`                   | Keyboard focus, touch-target floors, form labels/help and existing feedback utilities  |
| `css/layout.css`                   | Body and page geometry                                                                 |
| Feature stylesheets                | Artwork, map geometry and page-specific layout; migrate deliberately with coverage     |

The shared `.ori-navbar` uses `layout.css` for its opaque frame/reflow and
`components.css` for links, badges and current states. `common.css` owns touch
floors and focus. Preserve the logo SVG, Fraunces brand text, native routes and
conditional Vaults controller; the default brand uses `--accent-text`, while
explicit caller brand colors remain supported. `navbar-offset.js` measures the
live height with its retained 76px minimum. Do not add per-page navbar offsets.
Settings holds an initial fragment through font and section hydration, stopping
on user intent; do not replace readiness/geometry tracking with a fixed timer.
Navbar automation also checks the 720×450 CSS viewport equivalent of 1440×900
at 200% zoom. Native browser zoom controls still need the manual app-wide pass.
Reduced motion uses zero transition duration: a positive global duration can
create transitions even on otherwise static elements during a theme switch.

The shared plugin trust disclosure remains owned by `js/modules/plugin-lifecycle.js`
and `css/workspace-construct.css`. It consumes these controls and semantic tokens;
permission warnings, commands and digests stay complete and readable. Styling must
never change the granted capability set or confirmation authority.

Keep the existing asset order. Remove competing primitive definitions instead of
adding a final override sheet or another `!important` chain. These styles apply
only to Ori documents: **never inject them into sandboxed plugin iframes**.

## Tokens and typography

- `--font-body`: Manrope, Segoe UI, sans-serif. Body: 16px / 1.5.
- `--font-display`: Fraunces, Georgia, serif. Reserve large display typography for
  appropriate headings, not routine forms.
- `--font-mono`: system monospace for code and technical identifiers.
- `--space-1/2/3/4/6/8/12`: 4/8/12/16/24/32/48px at the default root size.
- `--control-height-sm`, `--control-height`, `--control-height-lg`: 32/40/48px;
  buttons use 14px / 1.4 text. Heights are minima; wrapping may require more room.
- `--touch-target`: 44px. Narrow/coarse targets must remain at least 44×44px.
  Size checkbox/radio **labels**, not their glyphs. Do not resize ranges, colors
  or hidden controls through a blanket input rule.
- `--radius-sm` and `--radius-md`: 8px; `--radius-lg`: 12px;
  `--radius-xl`: 16px. Pills distinguish badges and filters from action buttons.
- Existing responsive and persisted `--ui-density`/`--ui-type` preferences remain
  available. Density must not defeat touch targets or focus visibility.

| Semantic token     | Light     | Dark      |
| ------------------ | --------- | --------- |
| `--bg-primary`     | `#f5f6f7` | `#0a0a0a` |
| `--bg-secondary`   | `#e8eaed` | `#1f2128` |
| `--surface-solid`  | `#f5f6f7` | `#1f2128` |
| `--text-primary`   | `#0a0a0a` | `#c9ccd1` |
| `--text-secondary` | `#1f2128` | `#b3b6bd` |
| `--text-muted`     | `#525257` | `#a6a8af` |
| `--control-border` | `#767d87` | `#737985` |
| `--focus-color`    | `#1f6b4e` | `#8df4be` |
| `--danger-text`    | `#a21d2b` | `#ffaeae` |

Primary fill is `--primary-color: #1f6b4e`, with
`--primary-dark: #155039` for hover/active and `--on-primary: #fff`.
Use paired `--success-text`, `--warning-text`, `--info-text` and `--accent-text`
for readable semantic foregrounds; legacy status swatches are not text pairs.
Select indicators use a theme-paired `--select-indicator` SVG. Native select
semantics remain intact; custom painting avoids WebKit's intrinsic menulist
height overriding the control minimum. Bootstrap inline code uses the normal
theme foreground through `--bs-code-color`; its default pink misses AA on shared
light surfaces. Single-line native selectors contain their closed-field option
overflow: WebKit can otherwise enlarge the document even when the field's own
box fits. This is field containment, never page overflow masking; multiple and
listbox selectors retain internal scrolling and native selection semantics.

## Controls and states

Reports opt into `.ui-metric-grid`/`.ui-metric` for quiet numeric summaries,
`.ui-number` for tabular numerals, and `.ui-data-table` for genuinely wide tables.
The table wrapper needs a descriptive accessible name, `role="region"` and
`tabindex="0"`; the table still needs column/row headers and a caption. Keep
horizontal scrolling inside that bounded region, never on the page toolbar.
These classes do not style other tables or turn static summaries into actions.

Bootstrap `.btn` and `.modern-btn` are compatibility names for the same family,
using Bootstrap's `--bs-btn-*` state variables. Bootstrap retains responsibility
for toggles, dropdowns and tab behavior.

| Intent      | Bootstrap                                 | Modern alias                                         |
| ----------- | ----------------------------------------- | ---------------------------------------------------- |
| Primary     | `.btn.btn-primary`                        | `.modern-btn.modern-btn-primary`                     |
| Secondary   | `.btn.btn-secondary` or outline variants  | `.modern-btn.modern-btn-secondary`                   |
| Quiet       | `.btn.btn-link`                           | `.modern-btn.modern-btn-quiet` / `.modern-btn-ghost` |
| Destructive | `.btn.btn-danger` / `.btn-outline-danger` | `.modern-btn.modern-btn-danger`                      |
| Size        | `.btn-sm` / `.btn-lg`                     | `.modern-btn-sm` / `.modern-btn-lg`                  |

Success aliases primary; retain the action's meaningful name. Use a single
primary action per context. Do not turn every row action into a primary button.
Keep sentence case, accessible names and native anchors for navigation.

Fields share `.form-control`, `.form-select`, `.modern-input` and `.form-input`
metrics. Their small/large aliases remain supported. Labels belong in
`.form-label`; `.form-helper` or `.ui-field-help` uses 13px text. Associate help
and errors using `aria-describedby`; CSS alone cannot make feedback accessible.

```html
<label for="example-name" class="form-label required">Name</label>
<input id="example-name" class="form-control" required aria-describedby="example-name-help" />
<small id="example-name-help" class="form-helper">A short, recognizable name.</small>
```

Untouched required fields stay neutral. Validation presentation supports
`:user-invalid`, `.is-invalid`, `[aria-invalid="true"]` and Bootstrap's
`.was-validated` container. Controllers still own validation timing, error text,
focus and persistence. Read-only fields remain readable/selectable, not falsely
presented as disabled.

Keyboard focus is a 2px opaque theme ring with 2px offset. Disabled native
controls retain their semantics. `aria-disabled` alone does **not** prevent a
keyboard action; prefer native `disabled`, or enforce the state in the controller.
`aria-busy="true"` provides a progress cursor. Existing `.btn-loading` /
`.modern-btn-loading` retain the label's geometry and paint a spinner in the
button foreground. Controllers own accessible pending labels, busy/disabled
state and success/error feedback; CSS never fakes success.

The shared Appearance editor and character picker consume the same button,
field and filter primitives in every host. Feature CSS owns portrait/preview
geometry, not another control family. Character selection uses a native modal
`<dialog>` so an underlying Bootstrap modal cannot cover it or reclaim focus.
Preserve the parent draft and the choose/cancel/skip result contract; restore the
actual invoker, including its replacement after a staged redraw. Keep the gallery
and keyboard-focusable preview bounded; card rows must fit their complete art
and description rather than shrinking to a touch-floor height. The wide agent
detail host may arrange source cards in responsive columns without imposing that
geometry on narrow modal hosts. File inputs consume the canonical field metrics,
including the narrow target floor.

Hire/Create uses one form surface with quiet sections rather than cards inside
cards. Untouched fields remain neutral and controller validation remains live.
Metadata loading/errors are not empty or successful creation. A fixture-backed
model picker or staged portrait choice does not prove a saved model or a hire.

## Page structure and surfaces

```html
<header class="ui-page-header">
  <div>
    <h1>Page title</h1>
    <p>A brief description.</p>
  </div>
  <div class="ui-actions">
    <button class="modern-btn modern-btn-primary">Create item</button>
  </div>
</header>
<div class="ui-toolbar" role="search" aria-label="Filter items">
  <label class="visually-hidden" for="example-search">Search items</label>
  <input id="example-search" class="form-control" type="search" />
</div>
<section class="ui-section ui-section--quiet" aria-labelledby="example-heading">
  <h2 id="example-heading">Items</h2>
</section>
```

Headers wrap title/description and actions; filters occupy a separate toolbar.
`.ui-grid` offers wrapping content columns without a fixed minimum wider than a
phone. Do not conceal overflow to make a test pass.

`.card`, `.modern-card` and `.ui-section` are flat, opaque sections without hover
lift or default shadow. `.ui-section--quiet` removes the box. Prefer whitespace
and dividers to cards inside cards. `.ui-card-link` explicitly opts into an
interactive affordance; use a native link/button or a properly implemented
accessible control. Never add `role`/`tabindex` to a static section merely for
visual treatment. Shared display primitives preserve `[hidden]`.

Bootstrap dialog surfaces use the overlay radius and `--shadow-xl`, a
`calc(100dvh - 2rem)` content maximum, a scrollable body and stable footer.
`--app-modal-layer: 20200` and `--app-modal-backdrop-layer: 20190` sit above the
shared assistant. Earlier page notifications stay below that backdrop at
`--app-toast-layer: 20180`. Notifications created while a Bootstrap modal is
open mount in a bounded, in-flow area before its body, so errors remain visible
without covering footer actions. `toast.js` owns notification lifetime;
`common.css` owns its layout. Bootstrap owns focus trapping and dismissal;
programmatic openers must restore focus. Native/custom dialogs keep their own hosts and
lifecycle. Page-local geometry and inline compatibility exceptions require
explicit coverage before removal.

### Gallery and fixed-furniture clearance

The Agents gallery keeps cozy paper, portrait rings and catalog tints as artwork.
Its routine controls, inspector tabs, fields and slot badge use shared aliases;
role text and selection frames use semantic foregrounds, never arbitrary catalog
colours. Native custom overlays retain their controllers: the narrow inspector
inerts exterior siblings, preserves earlier inert state, traps focus only while
modal, and restores its opener. Native Filters and bulk dialogs keep their own
lifecycle and cancellation authority.

`agents-roster.js` measures the desktop inspector's actual top and the visible
assistant launcher's footprint, including portrait/font changes. The pane and
bulk bar reserve that space without hiding the assistant or guessing its width.
Check whole-control intersection as well as center-point hit testing: a partially
covered action can otherwise pass. Shared coachmarks reveal with native nearest
scrolling, focus without a preliminary scroll, and suppress smooth reveal for
reduced motion; first-use guidance is not a layout-test bypass.

## Compatibility and verification

`themeManager` and `data-bs-theme` remain authoritative; `.dark-mode` is a legacy
alias. Existing token aliases are `--bg-color → --bg-primary`,
`--text-color → --text-primary`, `--card-bg → --bg-secondary`, and
`--muted-text → --text-muted`.

Keep artwork, intentionally pannable maps/canvases and bounded wide tables.
Their toolbars and dialog actions must still fit and receive input. Preserve
hidden state, IDs/data hooks, routes, backend parameter names and plugin trust.
Reduced motion must suppress decorative movement; static sections never lift.

Relevant guards are `internal/web/visual_system_test.go`,
`tests/ui-consistency-components.spec.ts` and
`tests/ui-consistency-defects.spec.ts`. Verify real pages, not only a component
showcase: actual loaded states, element bounds, hit testing, focus and persistence.
Measure normal text at ≥4.5:1, qualifying large text at ≥3:1, essential boundaries
and focus at ≥3:1 against actual backgrounds. Disabled states are exempt from
those enabled-control ratios, not from recognizable semantics.

Run the opt-in suites through `scripts/test-ui-consistency.sh` against an owned
isolated demo. It starts no server and runs one spec per fresh browser process,
sequentially, without adding retries. This bounds the context count: the local
macOS WebKit run reproducibly stalled before navigation in its 64th context.
Use `--utilities` for the utility checkpoint and forward normal e2e options;
arguments after `--` go to Playwright. Utility mutations additionally require
`ORI_DISABLE_NATIVE_SECRET_STORE=1` on the demo server; HOME isolation alone does
not disable native credential storage.

```sh
./scripts/test-ui-consistency.sh --utilities --port 8943 \
  --env ORI_UI_SANDBOX="$SANDBOX" --env ORI_UI_EVIDENCE=after
./scripts/test-ui-consistency.sh --utilities --port 8943 \
  --env ORI_UI_SANDBOX="$SANDBOX" --env ORI_UI_EVIDENCE=after --env ORI_UI_FONTS=real
./scripts/test-ui-consistency.test.sh # fake-only runner contracts
```

Offline/fallback screenshots and intended-font verification are separate evidence.
Prove loaded Manrope/Fraunces **and computed families**; `document.fonts.ready`
alone is insufficient. Full page/theme/viewport/browser acceptance and the
repository gate are required before delivery.
