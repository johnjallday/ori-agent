// commander-balloon.js — the Commander speaks on the map (PRD
// commander-speaks-on-map). One balloon per map host and workspace: a
// collapsed speech line over the Commander's unit or tile, which expands into
// a screen-size balloon carrying the folder agent card (or a ticket) in the
// Commander's voice.
//
// The balloon is an overlay inside the map's viewport element, positioned from
// the anchor's screen rect, so it never scales with zoom. Maps re-mount often
// (Home several times in 100 ms), so every balloon lives in a module-level
// store keyed by host and workspace: the same DOM nodes move into the new
// layer after each mount, keeping what is expanded, the scroll position, what
// the owner typed and where focus was.

export const BALLOON_MIN_WIDTH = 360;
export const BALLOON_MAX_WIDTH = 440;
export const BALLOON_GAP = 14;
export const BALLOON_PAD = 8;
// Below this much room above or below, a balloon goes beside its anchor at a
// tighter width (never under BALLOON_TIGHT_WIDTH) rather than squashed.
export const BALLOON_USEFUL_HEIGHT = 240;
export const BALLOON_TIGHT_WIDTH = 300;
// Below this width no balloon expands: the host shows the full card (FR17).
export const BALLOON_NARROW_PX = 720;
// The collapsed line speaks at most once per Commander every 10 s (FR27).
export const BALLOON_ANNOUNCE_GAP_MS = 10000;
// The map's own camera event, dispatched on its viewport after each move.
export const MAP_CAMERA_EVENT = 'ori:map-camera';

const SIDES = ['above', 'below', 'right', 'left'];

function rectOf(value) {
  if (!value) return null;
  const left = Number(value.left) || 0;
  const top = Number(value.top) || 0;
  const width = Math.max(0, Number(value.width ?? (value.right ?? left) - left) || 0);
  const height = Math.max(0, Number(value.height ?? (value.bottom ?? top) - top) || 0);
  return { left, top, width, height, right: left + width, bottom: top + height };
}

const clamp = (value, low, high) => Math.min(Math.max(value, low), Math.max(low, high));

// balloonWidth keeps a readable width that fits the visible area (FR8).
export function balloonWidth(area, pad = BALLOON_PAD) {
  const room = Math.max(0, rectOf(area).width - pad * 2);
  return Math.min(BALLOON_MAX_WIDTH, room >= BALLOON_MIN_WIDTH ? room : Math.max(room, 0));
}

// anchorVisible says whether enough of the anchor is inside the area for a
// tail to point at it: its centre must be inside.
export function anchorVisible(anchor, area) {
  const a = rectOf(anchor);
  const v = rectOf(area);
  if (!a || !v) return false;
  const cx = a.left + a.width / 2;
  const cy = a.top + a.height / 2;
  return cx >= v.left && cx <= v.right && cy >= v.top && cy <= v.bottom;
}

// dockEdge is the visible area's edge nearest an off-screen anchor (FR11).
export function dockEdge(anchor, area) {
  const a = rectOf(anchor);
  const v = rectOf(area);
  const cx = a.left + a.width / 2;
  const cy = a.top + a.height / 2;
  const over = {
    left: v.left - cx,
    right: cx - v.right,
    top: v.top - cy,
    bottom: cy - v.bottom
  };
  return Object.keys(over).reduce((best, edge) => (over[edge] > over[best] ? edge : best), 'top');
}

// placeBalloon lays the expanded balloon beside its anchor within the visible
// area (all rects in one coordinate space). It tries above, below, right and
// left in that order and never overlaps the anchor; when nothing fits whole,
// the roomiest side wins and the balloon scrolls (maxHeight). An anchor
// outside the area docks the balloon at the nearest edge instead.
export function placeBalloon({ anchor, area, height, gap = BALLOON_GAP, pad = BALLOON_PAD }) {
  const v = rectOf(area);
  const a = rectOf(anchor);
  const width = balloonWidth(v, pad);
  const wanted = Math.max(0, Number(height) || 0);
  const inner = {
    left: v.left + pad,
    top: v.top + pad,
    right: v.right - pad,
    bottom: v.bottom - pad
  };
  if (!a || !anchorVisible(a, v)) {
    const edge = a ? dockEdge(a, v) : 'top';
    const maxHeight = Math.max(0, inner.bottom - inner.top);
    const h = Math.min(wanted, maxHeight);
    const cx = a ? a.left + a.width / 2 : v.left + v.width / 2;
    const cy = a ? a.top + a.height / 2 : v.top;
    let left = clamp(cx - width / 2, inner.left, inner.right - width);
    let top = clamp(cy - h / 2, inner.top, inner.bottom - h);
    if (edge === 'top') top = inner.top;
    if (edge === 'bottom') top = inner.bottom - h;
    if (edge === 'left') left = inner.left;
    if (edge === 'right') left = inner.right - width;
    // An anchor partly in view (half under the drawer) is not covered: the
    // docked balloon steps away from it along the edge when there is room.
    if (a) {
      const overlapsX = left < a.right && left + width > a.left;
      const overlapsY = top < a.bottom && top + h > a.top;
      if (overlapsX && overlapsY) {
        if (edge === 'left' || edge === 'right') {
          const before = a.left - gap - width;
          const after = a.right + gap;
          if (edge === 'right' && before >= inner.left) left = before;
          else if (edge === 'left' && after + width <= inner.right) left = after;
        } else {
          const above = a.top - gap - h;
          const below = a.bottom + gap;
          if (edge === 'bottom' && above >= inner.top) top = above;
          else if (edge === 'top' && below + h <= inner.bottom) top = below;
        }
      }
    }
    // A balloon at the bottom (or stepped above its anchor) hangs from its
    // bottom edge, so a taller body grows up, never past the fold.
    const hangs = edge === 'bottom' || (a && top + h <= a.top);
    return {
      side: 'dock',
      dock: edge,
      left,
      top,
      width,
      maxHeight: hangs ? top + h - inner.top : inner.bottom - top,
      bottomEdge: hangs ? top + h : null,
      tail: null
    };
  }
  const room = {
    above: a.top - gap - inner.top,
    below: inner.bottom - (a.bottom + gap),
    right: inner.right - (a.right + gap),
    left: a.left - gap - inner.left
  };
  const fitsAcross = side => (side === 'right' || side === 'left' ? room[side] >= width : true);
  const vertical = side => side === 'above' || side === 'below';
  const roomFor = side =>
    vertical(side) ? room[side] : fitsAcross(side) ? inner.bottom - inner.top : -1;
  let side = SIDES.find(s => fitsAcross(s) && roomFor(s) >= wanted);
  let w = width;
  if (!side) {
    side = SIDES.reduce((best, s) => (roomFor(s) > roomFor(best) ? s : best), 'above');
    // A short map (a narrow window): a sliver above or below is no place to
    // read a plan. Beside the anchor at a narrower width, full height, is.
    const across = room.right >= room.left ? 'right' : 'left';
    if (
      roomFor(side) < Math.min(wanted, BALLOON_USEFUL_HEIGHT) &&
      room[across] >= BALLOON_TIGHT_WIDTH
    ) {
      side = across;
      w = Math.min(width, room[across]);
    }
  }
  const maxHeight = Math.max(0, vertical(side) ? room[side] : inner.bottom - inner.top);
  const h = Math.min(wanted, maxHeight);
  const cx = a.left + a.width / 2;
  const cy = a.top + a.height / 2;
  let left;
  let top;
  if (vertical(side)) {
    left = clamp(cx - w / 2, inner.left, inner.right - w);
    top = side === 'above' ? a.top - gap - h : a.bottom + gap;
  } else {
    top = clamp(cy - h / 2, inner.top, inner.bottom - h);
    left = side === 'right' ? a.right + gap : a.left - gap - w;
  }
  const tail = vertical(side)
    ? { x: clamp(cx - left, 18, w - 18), y: side === 'above' ? h : 0 }
    : { x: side === 'right' ? 0 : w, y: clamp(cy - top, 18, Math.max(18, h - 18)) };
  // Above its anchor the balloon hangs from its bottom edge (a body that
  // grows never covers the anchor); elsewhere it never runs past the area's
  // bottom, whatever the measured height said.
  const above = side === 'above';
  return {
    side,
    dock: '',
    left,
    top,
    width: w,
    maxHeight: above ? maxHeight : Math.max(0, Math.min(maxHeight, inner.bottom - top)),
    bottomEdge: above ? a.top - gap : null,
    tail
  };
}

// placeLine sets the collapsed speech line centred over its anchor, inside
// the area; below the anchor when there is no room above.
export function placeLine({ anchor, area, width, height, gap = 12, pad = BALLOON_PAD }) {
  const a = rectOf(anchor);
  const v = rectOf(area);
  if (!a || !anchorVisible(a, v)) return null;
  const w = Math.min(Number(width) || 0, Math.max(0, v.width - pad * 2));
  const h = Number(height) || 0;
  const left = clamp(a.left + a.width / 2 - w / 2, v.left + pad, v.right - pad - w);
  const above = a.top - gap - h >= v.top + pad;
  return { left, top: above ? a.top - gap - h : a.bottom + gap, below: !above };
}

// shouldAnnounce applies FR27: a new line is announced when it differs from
// the last one spoken and the gap has passed; otherwise it waits for wait ms.
export function shouldAnnounce(text, last, now, gap = BALLOON_ANNOUNCE_GAP_MS) {
  if (!text || text === last.text) return { now: false, wait: 0 };
  if (last.at == null) return { now: true, wait: 0 };
  const since = now - last.at;
  if (since >= gap) return { now: true, wait: 0 };
  return { now: false, wait: gap - since };
}

function initialsOf(name) {
  const words = String(name || '')
    .trim()
    .split(/\s+/)
    .filter(Boolean);
  return (words[0]?.[0] || '?').toUpperCase() + (words[1]?.[0] || '').toUpperCase();
}

// firstFocusable walks node's subtree for the first control a keyboard user
// can reach (FR26: expanding moves focus to it).
export function firstFocusable(node) {
  for (const child of Array.from(node?.children || [])) {
    if (child.hidden) continue;
    const tag = String(child.tagName || '').toUpperCase();
    const control = ['BUTTON', 'INPUT', 'SELECT', 'TEXTAREA'].includes(tag) && !child.disabled;
    const link = tag === 'A' && Boolean(child.href || child.getAttribute?.('href'));
    if (control || link) return child;
    const inner = firstFocusable(child);
    if (inner) return inner;
  }
  return null;
}

const store = new Map();
let nextId = 0;

// commanderBalloon returns the one balloon for key (host and workspace),
// creating it on first use. Options apply on creation only.
export function commanderBalloon(key, options = {}) {
  let balloon = store.get(key);
  if (!balloon) {
    balloon = new CommanderBalloon(key, options);
    store.set(key, balloon);
  }
  return balloon;
}

// commanderBalloons lists the live balloons, for a host that keeps several.
export function commanderBalloons(prefix = '') {
  return [...store.values()].filter(b => b.key.startsWith(prefix));
}

// resetCommanderBalloons empties the store (tests, logout).
export function resetCommanderBalloons() {
  [...store.values()].forEach(b => b.remove());
  store.clear();
}

class CommanderBalloon {
  constructor(key, { doc, now, setTimer, clearTimer, onExpandChange } = {}) {
    this.key = key;
    this.doc = doc || (typeof document === 'undefined' ? null : document);
    this.now = now || (() => Date.now());
    this.setTimer = setTimer || ((fn, ms) => setTimeout(fn, ms));
    this.clearTimer = clearTimer || (id => clearTimeout(id));
    this.onExpandChange = onExpandChange || null;
    this.id = `commander-balloon-${++nextId}`;
    this.expanded = false;
    this.content = null;
    this.signature = '';
    this.root = null;
    this.layer = null;
    this.anchorOf = () => null;
    this.areaOf = null;
    this.narrowOf = null;
    this.onShow = null;
    this.onNarrow = null;
    this.held = null;
    this.scrollTop = 0;
    this.focusInside = false;
    this.lastFocused = null;
    this.spoken = { text: '', at: null };
    this.announceTimer = null;
    this.removed = false;
    this.nodes = null;
  }

  // build makes the balloon's nodes once; they move between layers.
  build() {
    if (this.nodes || !this.doc) return this.nodes;
    const doc = this.doc;
    const make = (tag, className, text) => {
      const node = doc.createElement(tag);
      if (className) node.className = className;
      if (text != null) node.textContent = text;
      return node;
    };
    const line = make('button', 'commander-balloon-line cozy-frame');
    line.type = 'button';
    line.dataset.commanderBalloonLine = this.key;
    const lineFace = make('span', 'commander-balloon-face');
    lineFace.setAttribute('aria-hidden', 'true');
    const lineText = make('span', 'commander-balloon-line-text');
    line.append(lineFace, lineText);
    line.addEventListener('click', () => this.expand());

    const live = make('p', 'commander-balloon-live visually-hidden');
    live.setAttribute('role', 'status');
    live.setAttribute('aria-live', 'polite');

    const box = make('section', 'commander-balloon cozy-frame');
    box.hidden = true;
    box.setAttribute('role', 'dialog');
    box.setAttribute('aria-modal', 'false');
    box.setAttribute('aria-labelledby', `${this.id}-title`);
    box.dataset.commanderBalloon = this.key;
    const header = make('header', 'commander-balloon__header');
    const face = make('span', 'commander-balloon-face commander-balloon__face');
    face.setAttribute('aria-hidden', 'true');
    const title = make('h3', 'commander-balloon__title');
    title.id = `${this.id}-title`;
    const name = make('span', 'commander-balloon__name');
    const role = make('span', 'commander-balloon__role');
    title.append(name, role);
    const headline = make('p', 'commander-balloon__headline');
    const close = make('button', 'commander-balloon__close', '✕');
    close.type = 'button';
    close.setAttribute('aria-label', 'Collapse');
    close.addEventListener('click', () => this.collapse());
    const heading = make('div', 'commander-balloon__heading');
    heading.append(title, headline);
    header.append(face, heading, close);
    const dock = make('div', 'commander-balloon__dock');
    dock.hidden = true;
    const arrow = make('span', 'commander-balloon__arrow');
    arrow.setAttribute('aria-hidden', 'true');
    const show = make('button', 'btn btn-sm commander-balloon__show', 'Show');
    show.type = 'button';
    show.addEventListener('click', () => this.onShow?.());
    dock.append(arrow, show);
    const body = make('div', 'commander-balloon__body');
    body.addEventListener('scroll', () => {
      this.scrollTop = body.scrollTop;
    });
    const more = make('button', 'commander-balloon__more');
    more.type = 'button';
    more.hidden = true;
    more.addEventListener('click', () => this.content?.onMore?.());
    const tail = make('span', 'commander-balloon__tail');
    tail.setAttribute('aria-hidden', 'true');
    box.append(header, dock, body, more, tail);
    box.addEventListener('keydown', event => {
      if (event.key === 'Escape') {
        event.preventDefault?.();
        event.stopPropagation?.();
        this.collapse();
      }
    });
    box.addEventListener('focusin', event => {
      this.focusInside = true;
      this.lastFocused = event.target;
    });
    box.addEventListener('focusout', event => {
      // A node torn out by a re-mount loses focus with no new target; only a
      // move to somewhere else on the page counts as leaving.
      if (event.relatedTarget && !box.contains?.(event.relatedTarget)) this.focusInside = false;
    });
    this.nodes = {
      line,
      lineFace,
      lineText,
      live,
      box,
      face,
      name,
      role,
      headline,
      close,
      dock,
      show,
      body,
      more,
      tail
    };
    return this.nodes;
  }

  // attach puts the balloon into root (the map's non-scaling viewport) and
  // re-measures. anchor() returns the anchor element or its client rect;
  // visibleArea() the part of the map the owner can see, in client
  // coordinates (default: the root). Call it after every mount.
  attach(root, { anchor, visibleArea, narrow, onShow, onNarrow } = {}) {
    if (!root || this.removed) return this;
    const nodes = this.build();
    if (!nodes) return this;
    if (anchor) this.anchorOf = typeof anchor === 'function' ? anchor : () => anchor;
    if (visibleArea !== undefined) this.areaOf = visibleArea;
    if (narrow !== undefined) this.narrowOf = narrow;
    if (onShow !== undefined) this.onShow = onShow;
    if (onNarrow !== undefined) this.onNarrow = onNarrow;
    const moved = this.root !== root || !this.layer || this.layer.parentElement !== root;
    if (moved) {
      const reattach = Boolean(this.layer);
      this.root = root;
      let layer =
        Array.from(root.children || []).find(child =>
          String(child.className || '')
            .split(' ')
            .includes('commander-balloon-layer')
        ) || null;
      if (!layer) {
        layer = this.doc.createElement('div');
        layer.className = 'commander-balloon-layer';
        root.append(layer);
      }
      this.layer = layer;
      // Moving into a new layer would replay the entrance animation on every
      // re-mount; what the owner already sees stays still.
      if (reattach) {
        nodes.line.classList?.add('is-settled');
        nodes.box.classList?.add('is-settled');
      }
      layer.append(nodes.line, nodes.box, nodes.live);
      if (!root.__commanderBalloonKeys) {
        root.__commanderBalloonKeys = true;
        root.addEventListener(MAP_CAMERA_EVENT, () => {
          commanderBalloons()
            .filter(b => b.root === root)
            .forEach(b => b.reposition());
        });
        root.addEventListener('keydown', event => this.unitKey(event, root));
      }
      nodes.body.scrollTop = this.scrollTop;
      this.restoreFocus();
    }
    this.paint();
    return this;
  }

  // unitKey: Enter on the focused anchor (unit or tile) expands its balloon.
  unitKey(event, root) {
    if (event.key !== 'Enter') return;
    const target = event.target;
    const balloon = commanderBalloons().find(b => b.root === root && b.anchorElement() === target);
    if (!balloon || !balloon.content || balloon.expanded) return;
    event.preventDefault?.();
    event.stopPropagation?.();
    balloon.expand();
  }

  restoreFocus() {
    if (!this.expanded || !this.focusInside || !this.lastFocused) return;
    const active = this.doc?.activeElement;
    if (active && active !== this.doc.body && this.nodes.box.contains?.(active)) return;
    if (active && active !== this.doc.body && active !== this.lastFocused) return;
    if (this.nodes.box.contains?.(this.lastFocused))
      this.lastFocused.focus?.({ preventScroll: true });
  }

  // anchorInView says whether the anchor's centre is in the visible area:
  // the balloon would point at it rather than dock.
  anchorInView() {
    if (!this.root) return false;
    const value = this.anchorOf?.();
    if (!value) return false;
    const rect =
      typeof value.getBoundingClientRect === 'function' ? value.getBoundingClientRect() : value;
    const areaValue = typeof this.areaOf === 'function' ? this.areaOf() : this.areaOf;
    return anchorVisible(rect, areaValue || this.rootRect());
  }

  anchorElement() {
    const value = this.anchorOf?.();
    return value && typeof value.getBoundingClientRect === 'function' ? value : null;
  }

  // update sets what the Commander says. content: {speaker: {name, role,
  // initials}, line, announce, headline, signature, body(doc) → Element,
  // more: {label}, onMore}. A null content removes the balloon (FR30).
  update(content) {
    if (!content) {
      this.remove();
      return this;
    }
    this.removed = false;
    const nodes = this.build();
    if (!nodes) return this;
    const previous = this.signature;
    this.content = content;
    const speaker = content.speaker || {};
    const initials = speaker.initials || initialsOf(speaker.name);
    nodes.lineFace.textContent = initials;
    nodes.face.textContent = initials;
    nodes.lineText.textContent = content.line || '';
    nodes.line.setAttribute('aria-label', `${speaker.name || 'Commander'}: ${content.line || ''}`);
    nodes.line.setAttribute('aria-expanded', this.expanded ? 'true' : 'false');
    nodes.name.textContent = speaker.name || '';
    nodes.role.textContent = speaker.role ? ` · ${speaker.role}` : '';
    nodes.headline.textContent = content.headline || '';
    nodes.headline.hidden = !content.headline;
    nodes.more.textContent = content.more?.label || '';
    nodes.more.hidden = !content.more?.label;
    const signature = String(content.signature ?? content.line ?? '');
    const scrollKey = String(content.scrollKey ?? '');
    if (scrollKey !== this.scrollKey) {
      this.scrollKey = scrollKey;
      this.scrollTop = 0;
    }
    if (signature !== previous || !nodes.body.children?.length) {
      this.signature = signature;
      const active = this.doc?.activeElement;
      const hadFocus = Boolean(active && nodes.body.contains?.(active));
      const next = typeof content.body === 'function' ? content.body(this.doc) : null;
      if (next) nodes.body.replaceChildren(next);
      else nodes.body.replaceChildren();
      nodes.body.scrollTop = this.scrollTop;
      if (hadFocus && this.expanded) this.focusFirst();
    }
    this.speak(content.announce ?? content.line ?? '');
    this.paint();
    return this;
  }

  // speak puts the line in the polite live region, at most once per gap.
  speak(text) {
    const clean = String(text || '').trim();
    const decision = shouldAnnounce(clean, this.spoken, this.now());
    if (decision.now) {
      this.spoken = { text: clean, at: this.now() };
      if (this.nodes) this.nodes.live.textContent = clean;
      return;
    }
    if (decision.wait > 0) {
      this.pendingSpeech = clean;
      if (this.announceTimer) return;
      this.announceTimer = this.setTimer(() => {
        this.announceTimer = null;
        const pending = this.pendingSpeech;
        this.pendingSpeech = '';
        if (pending && !this.removed) this.speak(pending);
      }, decision.wait);
    }
  }

  isNarrow() {
    if (typeof this.narrowOf === 'function') return Boolean(this.narrowOf());
    const width =
      Number(this.root?.getBoundingClientRect?.().width) ||
      Number(typeof window === 'undefined' ? 0 : window.innerWidth) ||
      0;
    return width > 0 && width < BALLOON_NARROW_PX;
  }

  // expand opens the balloon (one per map: the others on the same root
  // collapse). Below 720 px it asks the host for the full card instead.
  expand({ focus = true } = {}) {
    if (!this.content || this.removed) return false;
    if (this.isNarrow()) {
      this.onNarrow?.();
      return false;
    }
    commanderBalloons()
      .filter(b => b !== this && b.expanded && b.root === this.root)
      .forEach(b => b.collapse({ returnFocus: false }));
    const was = this.expanded;
    this.expanded = true;
    if (!was) this.nodes?.box.classList?.remove('is-settled');
    this.paint();
    if (focus) this.focusFirst();
    if (!was) this.onExpandChange?.(true, this);
    return true;
  }

  focusFirst() {
    const nodes = this.nodes;
    if (!nodes) return;
    const first =
      (!nodes.dock.hidden && nodes.show) || firstFocusable(nodes.body) || nodes.close || null;
    first?.focus?.({ preventScroll: true });
    // Inside the balloon's own scroll, the focused control comes into view.
    first?.scrollIntoView?.({ block: 'nearest' });
  }

  // collapse closes the balloon; returnFocus sends focus back to the unit.
  collapse({ returnFocus = true } = {}) {
    if (!this.expanded) return;
    this.expanded = false;
    this.focusInside = false;
    this.nodes?.line.classList?.add('is-settled');
    this.paint();
    if (returnFocus) {
      const anchor = this.anchorElement();
      (anchor || this.nodes?.line)?.focus?.({ preventScroll: true });
    }
    this.onExpandChange?.(false, this);
  }

  // holdUntil keeps the balloon still until promise settles (the placement
  // slide from #798), then re-measures once (FR12).
  holdUntil(promise) {
    const token = {};
    this.held = token;
    Promise.resolve(promise)
      .catch(() => {})
      .then(() => {
        if (this.held !== token) return;
        this.held = null;
        this.paint();
      });
    return this;
  }

  reposition() {
    if (!this.held) this.paint();
  }

  rootRect() {
    return rectOf(this.root?.getBoundingClientRect?.() || { left: 0, top: 0, width: 0, height: 0 });
  }

  // paint positions the line or the balloon in the root's coordinates.
  paint() {
    const nodes = this.nodes;
    if (!nodes || !this.root || this.held) return;
    const hasContent = Boolean(this.content) && !this.removed;
    const origin = this.rootRect();
    const anchorValue = this.anchorOf?.();
    const anchorRect = anchorValue
      ? rectOf(
          typeof anchorValue.getBoundingClientRect === 'function'
            ? anchorValue.getBoundingClientRect()
            : anchorValue
        )
      : null;
    const areaValue = typeof this.areaOf === 'function' ? this.areaOf() : this.areaOf;
    const area = rectOf(areaValue) || origin;
    const local = (r, x, y) => ({ ...r, left: x - origin.left, top: y - origin.top });
    nodes.line.setAttribute('aria-expanded', this.expanded ? 'true' : 'false');
    nodes.box.hidden = !(hasContent && this.expanded);
    if (!hasContent) {
      nodes.line.hidden = true;
      return;
    }
    if (!this.expanded) {
      // Measured shown: a hidden line has no size and would land on its anchor.
      nodes.line.hidden = false;
      const width = Number(nodes.line.offsetWidth) || 220;
      const height = Number(nodes.line.offsetHeight) || 24;
      const spot = placeLine({ anchor: anchorRect, area, width, height });
      nodes.line.hidden = !spot;
      if (spot) {
        const at = local(spot, spot.left, spot.top);
        nodes.line.style.left = `${Math.round(at.left)}px`;
        nodes.line.style.top = `${Math.round(at.top)}px`;
        nodes.line.classList?.toggle('is-below', Boolean(spot.below));
      }
      return;
    }
    nodes.line.hidden = true;
    // Its natural height: the frame plus all of the body, scrolled or not, so
    // the max-height of the last placement never shrinks the next one (and
    // the body keeps its scroll position).
    const frame = Number(nodes.box.offsetHeight) - Number(nodes.body.clientHeight);
    const natural = (frame > 0 ? frame : 0) + Number(nodes.body.scrollHeight);
    const height = natural || Number(nodes.box.scrollHeight) || 320;
    const place = placeBalloon({ anchor: anchorRect, area, height });
    const at = local(place, place.left, place.top);
    nodes.box.style.left = `${Math.round(at.left)}px`;
    if (place.bottomEdge != null) {
      nodes.box.style.top = 'auto';
      nodes.box.style.bottom = `${Math.round(origin.top + origin.height - place.bottomEdge)}px`;
    } else {
      nodes.box.style.bottom = 'auto';
      nodes.box.style.top = `${Math.round(at.top)}px`;
    }
    nodes.box.style.width = `${Math.round(place.width)}px`;
    nodes.box.style.maxHeight = `${Math.round(place.maxHeight)}px`;
    nodes.box.dataset.side = place.side;
    nodes.box.dataset.dock = place.dock || '';
    nodes.dock.hidden = place.side !== 'dock';
    nodes.tail.hidden = !place.tail;
    if (place.tail) {
      nodes.tail.style.left = `${Math.round(place.tail.x)}px`;
      // Above its anchor the tail sits on the bottom edge, wherever it is.
      nodes.tail.style.top = place.side === 'above' ? '100%' : `${Math.round(place.tail.y)}px`;
    }
  }

  // remove collapses and takes the balloon off the map (FR30). The store
  // keeps the entry, so a later update brings the same balloon back.
  remove() {
    const hadFocus = this.expanded && this.focusInside;
    this.removed = true;
    this.expanded = false;
    this.content = null;
    this.signature = '';
    this.focusInside = false;
    if (this.announceTimer) {
      this.clearTimer(this.announceTimer);
      this.announceTimer = null;
    }
    if (this.nodes) {
      this.nodes.body.replaceChildren();
      this.nodes.box.hidden = true;
      this.nodes.line.hidden = true;
      this.nodes.live.textContent = '';
    }
    if (hadFocus) this.anchorElement()?.focus?.({ preventScroll: true });
  }
}
