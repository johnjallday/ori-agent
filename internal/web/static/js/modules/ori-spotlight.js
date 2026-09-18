/*
 * ori-spotlight.js — Ori's blocking guide layer, for a first-run step that has
 * exactly one right click.
 *
 * Two presentations share one dimmed layer:
 *
 *   briefing   Ori in the centre of the screen with a mission card, over a
 *              page that is dimmed and inert. It asks one question: start the
 *              mission, or not now.
 *   spotlight  The page stays dimmed, with a hole cut over the one control the
 *              step names. A click in the hole reaches that control itself;
 *              a click anywhere else does nothing. Ori's callout sits beside
 *              the hole and says what to press.
 *
 * Starting a mission from its briefing opens the hole where the briefing was,
 * so the user sees Ori point at the first click.
 *
 * What it is not:
 *   - It never clicks, fills, selects or submits. The spotlit control is the
 *     user's to press. The hole is a clip-path on the dimmed layer, which also
 *     clips where that layer can be clicked.
 *   - It is never a trap. "Not now" and Escape always close it. Nothing is
 *     skipped or recorded here: the caller decides what "not now" means.
 *   - It is not a second pointer system. The ring and the hand on the control
 *     are Ori's own coachmark (OriGuide.markControl), which already re-anchors
 *     when the page re-renders the control and clears itself when the route
 *     changes.
 *
 * Classic scripts reach it through window.OriSpotlight.
 */

/* ---- geometry (pure) ------------------------------------------------------- */

const round = value => Math.round(value * 10) / 10;

/*
 * roundedRectPath is an SVG path for a rounded rectangle. Its command list is
 * the same for every size, a zero-sized one included, so the browser can
 * animate the hole opening from a point.
 */
export function roundedRectPath(x, y, width, height, radius) {
  const w = Math.max(0, width);
  const h = Math.max(0, height);
  const r = Math.max(0, Math.min(radius, w / 2, h / 2));
  const [x0, y0, x1, y1] = [round(x), round(y), round(x + w), round(y + h)];
  const rr = round(r);
  return (
    `M${round(x + r)} ${y0}H${round(x + w - r)}A${rr} ${rr} 0 0 1 ${x1} ${round(y + r)}` +
    `V${round(y + h - r)}A${rr} ${rr} 0 0 1 ${round(x + w - r)} ${y1}` +
    `H${round(x + r)}A${rr} ${rr} 0 0 1 ${x0} ${round(y + h - r)}` +
    `V${round(y + r)}A${rr} ${rr} 0 0 1 ${round(x + r)} ${y0}Z`
  );
}

// The padded box the hole is cut around, so Ori's ring on the control (an
// outline 3px outside it, pulsing out to 8px) shows inside the hole.
export function holeFor(rect, pad = 10) {
  return {
    x: rect.left - pad,
    y: rect.top - pad,
    width: rect.width + pad * 2,
    height: rect.height + pad * 2
  };
}

/*
 * scrimClipPath is the dimmed layer's clip-path: the whole viewport, minus the
 * hole (evenodd). With no hole it keeps a zero-sized one at the centre, which
 * is what the hole grows from when a briefing turns into a spotlight.
 */
export function scrimClipPath(viewport, hole, radius = 12) {
  const outer = `M0 0H${round(viewport.width)}V${round(viewport.height)}H0Z`;
  const inner = hole
    ? roundedRectPath(hole.x, hole.y, hole.width, hole.height, radius)
    : roundedRectPath(viewport.width / 2, viewport.height / 2, 0, 0, 0);
  return `path(evenodd, "${outer} ${inner}")`;
}

/*
 * placeCallout puts Ori's callout below the hole when it fits, above it when it
 * does not, centred on the hole and kept inside the viewport's gutters. `arrow`
 * is where the callout's pointer sits, measured from its left edge, so it
 * still aims at the control when the callout is pushed sideways.
 */
export function placeCallout(hole, viewport, size, { gap = 22, gutter = 16 } = {}) {
  const belowTop = hole.y + hole.height + gap;
  const fitsBelow = belowTop + size.height <= viewport.height - gutter;
  const top = fitsBelow ? belowTop : Math.max(gutter, hole.y - gap - size.height);
  const centre = hole.x + hole.width / 2;
  const maxLeft = Math.max(gutter, viewport.width - gutter - size.width);
  const left = Math.min(Math.max(gutter, centre - size.width / 2), maxLeft);
  const arrow = Math.min(Math.max(20, centre - left), Math.max(20, size.width - 20));
  return {
    left: Math.round(left),
    top: Math.round(top),
    side: fitsBelow ? 'below' : 'above',
    arrow: Math.round(arrow)
  };
}

/*
 * placeBeside puts Ori's callout next to a region (a form, for instance) rather
 * than under a control: to its left when there is room, else to its right,
 * level with the control it points at. It returns null when neither side has
 * room, so the caller can present the step some other way.
 */
export function placeBeside(anchor, target, viewport, size, { gap = 18, gutter = 16 } = {}) {
  let left;
  let side;
  if (anchor.left - gap - size.width >= gutter) {
    left = anchor.left - gap - size.width;
    side = 'left';
  } else if (anchor.left + anchor.width + gap + size.width <= viewport.width - gutter) {
    left = anchor.left + anchor.width + gap;
    side = 'right';
  } else {
    return null;
  }
  const level = target.top + Math.min(target.height, 44) / 2;
  const maxTop = Math.max(gutter, viewport.height - gutter - size.height);
  const top = Math.min(Math.max(gutter, level - 36), maxTop);
  const arrow = Math.min(Math.max(20, level - top), Math.max(20, size.height - 20));
  return { left: Math.round(left), top: Math.round(top), side, arrow: Math.round(arrow) };
}

/* ---- DOM ------------------------------------------------------------------- */

const ROOT_ID = 'oriSpotlight';
const PORTRAIT_SRC = '/characters/ori-guide/static.svg';
// How long a step waits for the control it names to mount, in frames.
const TARGET_WAIT_FRAMES = 90;

let current = null;

function doc() {
  return globalThis.document;
}

function win() {
  return globalThis.window;
}

function el(tag, className, text) {
  const node = doc().createElement(tag);
  if (className) node.className = className;
  if (text !== undefined && text !== null) node.textContent = String(text);
  return node;
}

function portrait(size) {
  const img = el('img', 'ori-spotlight__portrait');
  img.src = PORTRAIT_SRC;
  img.alt = '';
  img.width = size;
  img.height = size;
  img.decoding = 'async';
  return img;
}

function reducedMotion() {
  try {
    return !!win().matchMedia?.('(prefers-reduced-motion: reduce)').matches;
  } catch (_) {
    return false;
  }
}

function viewport() {
  const d = doc().documentElement;
  return {
    width: win().innerWidth || d.clientWidth,
    height: win().innerHeight || d.clientHeight
  };
}

function resolveTarget(key) {
  const registry = win().OriGuideCoachmarks;
  if (!registry || typeof registry.resolve !== 'function') return null;
  return registry.resolve(key, win().location.pathname || '/', doc());
}

// The page behind a briefing is inert: no click, no Tab, no screen reader
// reaching past the card. A spotlight leaves the page alone, because the
// control in the hole has to stay reachable.
function makePageInert() {
  const restored = [];
  for (const child of Array.from(doc().body.children)) {
    if (child.id === ROOT_ID || child.inert || child.tagName === 'SCRIPT') continue;
    child.inert = true;
    restored.push(child);
  }
  return () => {
    for (const child of restored) child.inert = false;
  };
}

function ensureRoot() {
  if (current) return current;
  const root = el('div', 'ori-spotlight');
  root.id = ROOT_ID;
  if (reducedMotion()) root.classList.add('is-still');
  const scrim = el('div', 'ori-spotlight__scrim');
  scrim.setAttribute('aria-hidden', 'true');
  // A click on the dimmed page is a click on nothing. Say so, gently: the
  // callout nudges toward the control that is the way on.
  scrim.addEventListener('click', () => nudge());
  const live = el('p', 'visually-hidden');
  live.setAttribute('aria-live', 'polite');
  root.append(scrim, live);
  doc().body.appendChild(root);

  current = {
    root,
    scrim,
    live,
    mode: '',
    card: null,
    callout: null,
    frame: 0,
    restoreInert: null,
    onLater: null,
    key: '',
    target: null,
    anchor: '',
    size: null,
    lastClip: '',
    lastPlace: ''
  };
  doc().addEventListener('keydown', onKeydown, true);
  scrim.style.clipPath = scrimClipPath(viewport(), null);
  return current;
}

function onKeydown(event) {
  if (!current) return;
  // A callout sits beside a form the user is working in, and Escape there
  // belongs to the form.
  if (event.key === 'Escape' && current.mode !== 'callout') {
    event.preventDefault();
    event.stopPropagation();
    later();
    return;
  }
  // The briefing is a modal dialog: Tab stays on its buttons.
  if (event.key === 'Tab' && current.mode === 'briefing' && current.card) {
    const buttons = Array.from(current.card.querySelectorAll('button'));
    if (!buttons.length) return;
    const first = buttons[0];
    const last = buttons[buttons.length - 1];
    const active = doc().activeElement;
    if (event.shiftKey && (active === first || !current.card.contains(active))) {
      event.preventDefault();
      last.focus();
    } else if (!event.shiftKey && (active === last || !current.card.contains(active))) {
      event.preventDefault();
      first.focus();
    }
  }
}

function nudge() {
  const target = current && (current.callout || current.card);
  if (!target || reducedMotion()) return;
  target.classList.remove('is-nudged');
  // Restart the animation on a repeat click.
  void target.offsetWidth;
  target.classList.add('is-nudged');
}

function later() {
  const onLater = current && current.onLater;
  close();
  if (typeof onLater === 'function') onLater();
}

function stopTracking() {
  if (current && current.frame) {
    win().cancelAnimationFrame?.(current.frame);
    current.frame = 0;
  }
}

/*
 * close removes the layer and Ori's mark. Safe to call when nothing is up.
 */
export function close() {
  if (!current) return;
  stopTracking();
  doc().removeEventListener('keydown', onKeydown, true);
  if (current.restoreInert) current.restoreInert();
  if (current.key) win().OriGuide?.clearControlMark?.();
  current.root.remove();
  current = null;
}

export function isOpen() {
  return !!current;
}

/*
 * showBriefing puts Ori in the centre with one mission.
 *
 *   guideName, greeting   who is speaking, and what they say first
 *   kicker, title, why     the mission card
 *   steps                  the clicks ahead, as short labels
 *   reward, unlocks        what finishing it gives (either may be empty)
 *   note                   one reassurance line under the buttons
 *   startLabel, laterLabel the two choices
 *   onStart, onLater       called after the choice; onStart may open a
 *                          spotlight, which keeps the dimmed layer up
 *
 * Every string is set as text, never markup: names are user data.
 */
export function showBriefing(opts = {}) {
  const layer = ensureRoot();
  layer.mode = 'briefing';
  layer.root.dataset.mode = 'briefing';
  layer.onLater = opts.onLater || null;
  layer.callout?.remove();
  layer.callout = null;
  if (!layer.restoreInert) layer.restoreInert = makePageInert();

  const card = el('section', 'ori-spotlight__briefing');
  card.setAttribute('role', 'dialog');
  card.setAttribute('aria-modal', 'true');
  card.setAttribute('aria-labelledby', 'oriBriefingGreeting');
  card.setAttribute('aria-describedby', 'oriBriefingWhy');

  const who = el('div', 'ori-spotlight__who');
  who.append(portrait(72));
  const ident = el('div', 'ori-spotlight__ident');
  ident.append(
    el('p', 'ori-spotlight__name', opts.guideName || 'Ori'),
    el('p', 'ori-spotlight__role', 'App Guide')
  );
  who.append(ident);

  const greeting = el('h2', 'ori-spotlight__greeting', opts.greeting || '');
  greeting.id = 'oriBriefingGreeting';

  const mission = el('div', 'ori-spotlight__mission');
  const head = el('div', 'ori-spotlight__mission-head');
  head.append(el('span', 'ori-spotlight__kicker', opts.kicker || ''));
  if (opts.reward) head.append(el('span', 'ori-spotlight__reward', opts.reward));
  const title = el('h3', 'ori-spotlight__mission-title', opts.title || '');
  const why = el('p', 'ori-spotlight__why', opts.why || '');
  why.id = 'oriBriefingWhy';
  mission.append(head, title, why);

  const steps = Array.isArray(opts.steps) ? opts.steps : [];
  if (steps.length) {
    const list = el('ol', 'ori-spotlight__steps');
    list.setAttribute('aria-label', `${steps.length} steps`);
    steps.forEach((label, index) => {
      const item = el('li', 'ori-spotlight__step');
      item.append(el('span', 'ori-spotlight__step-n', index + 1), el('span', '', label));
      list.append(item);
    });
    mission.append(list);
  }
  if (opts.unlocks) mission.append(el('p', 'ori-spotlight__unlocks', opts.unlocks));

  const actions = el('div', 'ori-spotlight__actions');
  const start = el('button', 'ori-spotlight__start', opts.startLabel || 'Start mission');
  start.type = 'button';
  start.dataset.oriSpotlight = 'start';
  const notNow = el('button', 'ori-spotlight__later', opts.laterLabel || 'Not now');
  notNow.type = 'button';
  notNow.dataset.oriSpotlight = 'later';
  actions.append(start, notNow);

  card.append(who, greeting, mission, actions);
  if (opts.note) card.append(el('p', 'ori-spotlight__note', opts.note));

  start.addEventListener('click', () => {
    if (typeof opts.onStart === 'function') opts.onStart();
    else close();
  });
  notNow.addEventListener('click', () => later());

  layer.card?.remove();
  layer.card = card;
  layer.root.append(card);
  start.focus();
  return true;
}

/*
 * showSpotlight dims the page around one registered control and puts Ori's
 * callout beside it.
 *
 *   coachmark      the control's key in OriGuideCoachmarks
 *   index, total   "Step n of total"
 *   title, body    what to press, and why
 *   note           optional reassurance line
 *   laterLabel     the way out ("Not now")
 *   onLater        called when the user leaves
 *   focus          false to mark without moving focus
 *
 * It waits a bounded time for a control that is about to mount. It returns a
 * promise of whether the control was found: when it was not, nothing is shown
 * and the caller falls back to its own presentation.
 */
export function showSpotlight(opts = {}) {
  return waitAndPresent(opts, 'spotlight');
}

/*
 * showCallout is the same callout without the dimmed page, for a step where
 * the user works in several fields at once. It sits beside `anchor` (a CSS
 * selector for the region, a form for instance; the control itself when
 * omitted), level with the control, and may offer `choices` ({id, label}),
 * reported through `onChoice(id)`. It resolves false, showing nothing, when the
 * control is missing or there is no room beside the region.
 */
export function showCallout(opts = {}) {
  return waitAndPresent(opts, 'callout');
}

function waitAndPresent(opts, mode) {
  const key = String(opts.coachmark || '');
  return new Promise(resolve => {
    let frames = 0;
    const attempt = () => {
      const target = key ? resolveTarget(key) : null;
      if (target && (mode !== 'callout' || roomBeside(target, opts.anchor))) {
        present(target, key, opts, mode);
        resolve(true);
        return;
      }
      frames += 1;
      if (frames >= TARGET_WAIT_FRAMES || typeof win().requestAnimationFrame !== 'function') {
        resolve(false);
        return;
      }
      win().requestAnimationFrame(attempt);
    };
    attempt();
  });
}

function anchorFor(target, anchor) {
  const region = anchor ? doc().querySelector(anchor) : null;
  return region || target;
}

// Whether a callout of the usual width fits beside the region. A phone-width
// sheet that fills the screen has no beside.
function roomBeside(target, anchor) {
  const size = { width: Math.min(340, viewport().width - 32), height: 160 };
  return !!placeBeside(
    anchorFor(target, anchor).getBoundingClientRect(),
    target.getBoundingClientRect(),
    viewport(),
    size
  );
}

function present(target, key, opts, mode) {
  const layer = ensureRoot();
  const wasBriefing = layer.mode === 'briefing';
  const wasCallout = layer.mode === 'callout';
  layer.mode = mode;
  layer.root.dataset.mode = mode;
  layer.onLater = opts.onLater || null;
  layer.key = key;
  layer.target = target;
  layer.anchor = opts.anchor || '';
  // A callout leaves the page alone; a spotlight dims it around the control.
  layer.scrim.hidden = mode === 'callout';
  // The page must be reachable again: the control is the way on.
  if (layer.restoreInert) {
    layer.restoreInert();
    layer.restoreInert = null;
  }
  layer.card?.remove();
  layer.card = null;

  const callout = el('section', 'ori-spotlight__callout');
  callout.setAttribute('role', 'dialog');
  callout.setAttribute('aria-modal', 'false');
  callout.setAttribute('aria-labelledby', 'oriSpotlightTitle');

  const head = el('div', 'ori-spotlight__callout-head');
  head.append(portrait(36));
  const meta = el('div', 'ori-spotlight__callout-meta');
  meta.append(
    el('span', 'ori-spotlight__callout-name', opts.guideName || 'Ori'),
    el('span', 'ori-spotlight__callout-step', `Step ${opts.index} of ${opts.total}`)
  );
  head.append(meta);
  const notNow = el('button', 'ori-spotlight__callout-later', opts.laterLabel || 'Not now');
  notNow.type = 'button';
  notNow.dataset.oriSpotlight = 'later';
  notNow.addEventListener('click', () => later());
  head.append(notNow);

  const title = el('h2', 'ori-spotlight__callout-title', opts.title || '');
  title.id = 'oriSpotlightTitle';
  callout.append(head, title);
  if (opts.body) callout.append(el('p', 'ori-spotlight__callout-body', opts.body));
  const choices = Array.isArray(opts.choices) ? opts.choices : [];
  if (choices.length) {
    const row = el('div', 'ori-spotlight__choices');
    for (const choice of choices) {
      const id = String(choice?.id || '').trim();
      const label = String(choice?.label || '').trim();
      if (!id || !label) continue;
      const button = el('button', 'ori-spotlight__choice', label);
      button.type = 'button';
      button.dataset.oriSpotlightChoice = id;
      button.addEventListener('click', () => opts.onChoice?.(id));
      row.append(button);
    }
    callout.append(row);
  }
  if (opts.note) callout.append(el('p', 'ori-spotlight__callout-note', opts.note));
  const arrow = el('span', 'ori-spotlight__arrow');
  arrow.setAttribute('aria-hidden', 'true');
  callout.append(arrow);

  layer.callout?.remove();
  layer.callout = callout;
  layer.root.append(callout);
  if (wasBriefing) callout.classList.add('is-from-briefing');
  // Moving from one field to the next is the same conversation: no fade.
  if (wasCallout) callout.classList.add('is-continuing');
  if (mode === 'spotlight') {
    // The hole animates open once. Left on, the transition would lag the hole
    // behind the control on every later resize.
    const root = layer.root;
    root.classList.add('is-opening');
    win().setTimeout?.(() => root.classList.remove('is-opening'), 480);
  }

  // The step, read once, without moving focus off the control.
  layer.live.textContent = [
    `Step ${opts.index} of ${opts.total}.`,
    opts.title || '',
    opts.body || ''
  ]
    .join(' ')
    .trim();

  win().OriGuide?.markControl?.(key, { awaitTarget: true, focus: opts.focus !== false });
  if (opts.focus !== false && !win().OriGuide?.markControl) target.focus?.();

  stopTracking();
  layer.lastClip = '';
  layer.lastPlace = '';
  layer.size = null;
  track();
}

// Keeps the hole and the callout on the control for as long as the layer is
// up. A frame loop, like Ori's pointer: layout settles, fonts load, forms
// scroll and the page re-renders without any event that says so.
function track() {
  if (!current || (current.mode !== 'spotlight' && current.mode !== 'callout')) return;
  let target = current.target;
  if (!target || !doc().contains(target)) {
    target = resolveTarget(current.key);
    current.target = target;
  }
  const callout = current.callout;
  if (target) {
    const rect = target.getBoundingClientRect();
    const view = viewport();
    let place = null;
    // Measured while shown: a hidden callout measures 0 × 0, which would fit
    // anywhere and flicker it back on.
    if (callout && !callout.hidden) {
      current.size = { width: callout.offsetWidth, height: callout.offsetHeight };
    }
    const size = callout ? current.size : null;
    if (current.mode === 'spotlight') {
      const hole = holeFor(rect);
      const clip = scrimClipPath(view, hole);
      if (clip !== current.lastClip) {
        current.scrim.style.clipPath = clip;
        current.lastClip = clip;
      }
      if (size) place = placeCallout(hole, view, size);
    } else if (size) {
      const anchor = anchorFor(target, current.anchor).getBoundingClientRect();
      place = placeBeside(anchor, rect, view, size);
    }
    if (callout) {
      // No room beside the form any more (a window made narrow): step aside
      // rather than cover the field being pointed at.
      callout.hidden = !place;
      const signature = place ? `${place.left},${place.top},${place.side},${place.arrow}` : '';
      if (place && signature !== current.lastPlace) {
        callout.style.left = `${place.left}px`;
        callout.style.top = `${place.top}px`;
        callout.dataset.side = place.side;
        callout.style.setProperty('--ori-spotlight-arrow', `${place.arrow}px`);
      }
      current.lastPlace = signature;
    }
  }
  current.frame = win().requestAnimationFrame?.(track) || 0;
}

if (typeof window !== 'undefined') {
  window.OriSpotlight = Object.freeze({
    showBriefing,
    showSpotlight,
    showCallout,
    close,
    isOpen
  });
}
