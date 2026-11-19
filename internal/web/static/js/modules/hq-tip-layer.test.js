import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  CLOSE_GRACE_MS,
  HOVER_OPEN_MS,
  MARK_ATTR,
  PART_ATTR,
  TIP_ID,
  TRIGGER_ATTR,
  createHqTipLayer
} from './hq-tip-layer.js';

// A hand-rolled DOM: just what the layer reads, plus spies on focus()/blur().
let focusCalls = 0;

function matcher(selector) {
  const attr = selector.match(/^\[([\w-]+)(?:="([^"]*)")?\]$/);
  if (attr)
    return node =>
      node.hasAttribute(attr[1]) &&
      (attr[2] === undefined || node.getAttribute(attr[1]) === attr[2]);
  if (selector === 'nav.navbar')
    return node => node.tagName === 'NAV' && node.className.includes('navbar');
  throw new Error('unsupported selector ' + selector);
}

class FakeElement {
  constructor(tagName = 'div') {
    this.tagName = tagName.toUpperCase();
    this.attributes = new Map();
    this.children = [];
    this.parentNode = null;
    this.className = '';
    this.id = '';
    this.hidden = false;
    this.style = {};
    this.textContent = '';
    this.innerHTML = '';
    this.rect = { left: 0, top: 0, right: 0, bottom: 0, width: 0, height: 0 };
  }
  setAttribute(name, value) {
    this.attributes.set(name, String(value));
  }
  getAttribute(name) {
    return this.attributes.has(name) ? this.attributes.get(name) : null;
  }
  hasAttribute(name) {
    return this.attributes.has(name);
  }
  removeAttribute(name) {
    this.attributes.delete(name);
  }
  appendChild(child) {
    child.parentNode = this;
    this.children.push(child);
    return child;
  }
  remove() {
    if (!this.parentNode) return;
    this.parentNode.children = this.parentNode.children.filter(node => node !== this);
    this.parentNode = null;
  }
  contains(node) {
    for (let at = node; at; at = at.parentNode) if (at === this) return true;
    return false;
  }
  closest(selector) {
    const test = matcher(selector);
    for (let at = this; at; at = at.parentNode) if (test(at)) return at;
    return null;
  }
  querySelector(selector) {
    const test = matcher(selector);
    const walk = node => {
      for (const child of node.children) {
        if (test(child)) return child;
        const found = walk(child);
        if (found) return found;
      }
      return null;
    };
    return walk(this);
  }
  getBoundingClientRect() {
    return this.rect;
  }
  focus() {
    focusCalls += 1;
  }
  blur() {
    focusCalls += 1;
  }
  set innerHTML(value) {
    this._html = value;
  }
  get innerHTML() {
    return this._html || '';
  }
}

function setup({ vw = 390, vh = 800, scope = 'overview', tipWidth = 300, tipHeight = 60 } = {}) {
  focusCalls = 0;
  const listeners = [];
  const body = new FakeElement('body');
  const doc = {
    body,
    documentElement: { clientWidth: vw },
    activeElement: null,
    createElement: tag => {
      const node = new FakeElement(tag);
      if (tag === 'div')
        node.rect = {
          left: 0,
          top: 0,
          right: tipWidth,
          bottom: tipHeight,
          width: tipWidth,
          height: tipHeight
        };
      return node;
    },
    addEventListener: (type, fn, capture) =>
      listeners.push({ target: 'doc', type, fn, capture: Boolean(capture) }),
    removeEventListener: (type, fn, capture) => {
      const at = listeners.findIndex(
        l => l.target === 'doc' && l.type === type && l.fn === fn && l.capture === Boolean(capture)
      );
      if (at >= 0) listeners.splice(at, 1);
    },
    querySelector: selector => body.querySelector(selector)
  };
  const win = {
    innerWidth: vw,
    innerHeight: vh,
    addEventListener: (type, fn, capture) =>
      listeners.push({ target: 'win', type, fn, capture: Boolean(capture) }),
    removeEventListener: (type, fn, capture) => {
      const at = listeners.findIndex(
        l => l.target === 'win' && l.type === type && l.fn === fn && l.capture === Boolean(capture)
      );
      if (at >= 0) listeners.splice(at, 1);
    }
  };
  // Manual clock.
  let now = 0;
  let nextId = 1;
  const timers = new Map();
  const clock = {
    setTimeout: (fn, ms) => {
      const id = nextId++;
      timers.set(id, { fn, at: now + ms });
      return id;
    },
    clearTimeout: id => timers.delete(id),
    tick(ms) {
      const end = now + ms;
      for (;;) {
        const due = [...timers.entries()]
          .filter(([, t]) => t.at <= end)
          .sort((a, b) => a[1].at - b[1].at)[0];
        if (!due) break;
        timers.delete(due[0]);
        now = due[1].at;
        due[1].fn();
      }
      now = end;
    }
  };

  const root = new FakeElement('main');
  body.appendChild(root);
  const state = {
    scope,
    shown: [],
    rootReads: 0,
    known: {
      'daily-brief': 'Brief sentence.',
      memory: 'Memory sentence.',
      notes: 'Notes sentence.'
    }
  };

  function addPart(key, rect, { trigger = '', withMark = true } = {}) {
    const card = new FakeElement('button');
    card.setAttribute(PART_ATTR, key);
    if (trigger) card.setAttribute(TRIGGER_ATTR, trigger);
    card.rect = rect;
    const child = new FakeElement('span');
    card.appendChild(child);
    root.appendChild(card);
    let mark = null;
    if (withMark) {
      mark = new FakeElement('button');
      mark.setAttribute(MARK_ATTR, key);
      mark.setAttribute('aria-expanded', 'false');
      mark.rect = {
        left: rect.right - 44,
        top: rect.bottom - 44,
        right: rect.right,
        bottom: rect.bottom,
        width: 44,
        height: 44
      };
      root.appendChild(mark);
    }
    return { card, child, mark };
  }

  const layer = createHqTipLayer({
    document: doc,
    window: win,
    root: () => {
      state.rootReads += 1;
      return root;
    },
    scope: () => state.scope,
    content: key =>
      state.known[key]
        ? { sentence: state.known[key], name: 'Atlas', faceHTML: '<i>face</i>' }
        : null,
    onShown: key => state.shown.push(key),
    isKeyboardFocus: node => node.keyboardFocus !== false,
    setTimeout: clock.setTimeout,
    clearTimeout: clock.clearTimeout,
    requestAnimationFrame: cb => clock.setTimeout(cb, 16)
  });
  const tip = body.children.find(node => node.id === TIP_ID);
  const fire = (type, event = {}) => {
    const e = {
      type,
      defaultPrevented: false,
      stopped: false,
      preventDefault() {
        this.defaultPrevented = true;
      },
      stopPropagation() {
        this.stopped = true;
      },
      ...event
    };
    for (const l of [...listeners]) if (l.target === 'doc' && l.type === type) l.fn(e);
    return e;
  };
  const count = type => listeners.filter(l => l.type === type).length;
  return { layer, doc, win, body, root, tip, state, clock, fire, count, addPart, listeners };
}

const CARD = { left: 20, top: 100, right: 220, bottom: 190, width: 200, height: 90 };
const mouse = { pointerType: 'mouse' };

test('show and hide: the tip carries its words, the part and its ? carry ARIA, onShown fires once', () => {
  const t = setup();
  const { card, mark } = t.addPart('daily-brief', CARD);
  assert.equal(t.tip.getAttribute('role'), 'tooltip');
  assert.equal(t.tip.hidden, true, 'closed until shown, so aria-controls always resolves');

  assert.equal(t.layer.show('daily-brief', 'tap'), true);
  assert.equal(t.tip.hidden, false);
  assert.equal(t.layer.isOpen(), true);
  assert.equal(t.layer.currentKey(), 'daily-brief');
  const [head, text] = t.tip.children;
  assert.equal(text.textContent, 'Brief sentence.');
  assert.equal(head.children[1].textContent, 'Atlas');
  assert.equal(head.children[0].innerHTML, '<i>face</i>');
  assert.equal(card.getAttribute('aria-describedby'), TIP_ID);
  assert.equal(mark.getAttribute('aria-describedby'), TIP_ID);
  assert.equal(mark.getAttribute('aria-expanded'), 'true');
  assert.deepEqual(t.state.shown, ['daily-brief']);

  // showing the visible key again fires nothing
  t.layer.show('daily-brief', 'tap');
  assert.deepEqual(t.state.shown, ['daily-brief']);

  t.layer.hide();
  assert.equal(t.tip.hidden, true);
  assert.equal(card.hasAttribute('aria-describedby'), false);
  assert.equal(mark.hasAttribute('aria-describedby'), false);
  assert.equal(mark.getAttribute('aria-expanded'), 'false');
  assert.equal(t.layer.isOpen(), false);
});

test('other aria-describedby tokens survive a tip coming and going', () => {
  const t = setup();
  const { card } = t.addPart('daily-brief', CARD);
  card.setAttribute('aria-describedby', 'other');
  t.layer.show('daily-brief', 'tap');
  assert.equal(card.getAttribute('aria-describedby'), 'other ' + TIP_ID);
  t.layer.hide();
  assert.equal(card.getAttribute('aria-describedby'), 'other');
});

test('the tip stays inside the screen with a 16px gutter at 390px', () => {
  const t = setup({ vw: 390 });
  // A ? near the right edge and one near the left edge.
  const right = t.addPart('daily-brief', {
    left: 190,
    top: 100,
    right: 380,
    bottom: 190,
    width: 190,
    height: 90
  });
  t.layer.show('daily-brief', 'tap');
  const rightLeft = parseInt(t.tip.style.left, 10);
  assert.ok(rightLeft >= 16 && rightLeft <= 390 - 300 - 16, 'right edge: ' + rightLeft);
  t.layer.hide();
  right.card.remove();
  right.mark.remove();
  t.addPart('memory', { left: 0, top: 100, right: 60, bottom: 160, width: 60, height: 60 });
  t.layer.show('memory', 'tap');
  assert.equal(parseInt(t.tip.style.left, 10), 16);
  assert.equal(t.tip.getAttribute('data-placement'), 'below');
});

test('with no room below the tip goes above, and below the navbar', () => {
  const t = setup({ vh: 300, tipHeight: 60 });
  t.addPart('daily-brief', { left: 20, top: 200, right: 220, bottom: 290, width: 200, height: 90 });
  t.layer.show('daily-brief', 'tap');
  assert.equal(t.tip.getAttribute('data-placement'), 'above');
  assert.ok(parseInt(t.tip.style.top, 10) < 200);
});

test('a replaced anchor keeps the tip: ARIA moves to the new nodes and onShown fires once', () => {
  const t = setup();
  const first = t.addPart('daily-brief', CARD);
  t.layer.show('daily-brief', 'tap');
  // The page rebuilds its container.
  first.card.remove();
  first.mark.remove();
  const second = t.addPart('daily-brief', CARD);
  t.layer.refresh();
  assert.equal(t.layer.isOpen(), true);
  assert.equal(second.card.getAttribute('aria-describedby'), TIP_ID);
  assert.equal(second.mark.getAttribute('aria-describedby'), TIP_ID);
  assert.equal(second.mark.getAttribute('aria-expanded'), 'true');
  assert.deepEqual(t.state.shown, ['daily-brief']);
});

test('a removed anchor, a changed scope or a part with no words closes the tip', () => {
  let t = setup();
  let part = t.addPart('daily-brief', CARD);
  t.layer.show('daily-brief', 'tap');
  part.card.remove();
  part.mark.remove();
  t.layer.refresh();
  assert.equal(t.layer.isOpen(), false);
  assert.equal(t.tip.hidden, true);

  t = setup();
  t.addPart('daily-brief', CARD);
  t.layer.show('daily-brief', 'tap');
  t.state.scope = 'flow';
  t.layer.refresh();
  assert.equal(t.layer.isOpen(), false, 'a view switch closes it');

  t = setup();
  t.addPart('daily-brief', CARD);
  t.layer.show('daily-brief', 'tap');
  t.state.scope = '';
  t.layer.refresh();
  assert.equal(t.layer.isOpen(), false, 'a finished first visit closes it');

  t = setup();
  t.addPart('daily-brief', CARD);
  t.state.scope = '';
  assert.equal(t.layer.show('daily-brief', 'tap'), false, 'nothing opens with no scope');
  t.state.scope = 'overview';
  delete t.state.known['daily-brief'];
  assert.equal(t.layer.show('daily-brief', 'tap'), false, 'no sentence, no tip');
  assert.equal(t.layer.show('Bad Key!', 'tap'), false);
});

test('refresh with no tip open reads nothing from the page', () => {
  const t = setup();
  t.addPart('daily-brief', CARD);
  const before = t.state.rootReads;
  t.layer.refresh();
  t.layer.refresh();
  assert.equal(t.state.rootReads, before);
});

test('the layer never moves focus, on any path', () => {
  const t = setup();
  const { card, child, mark } = t.addPart('daily-brief', CARD);
  const other = t.addPart('memory', {
    left: 240,
    top: 100,
    right: 380,
    bottom: 190,
    width: 140,
    height: 90
  });
  // hover, then leave and let the grace pass
  t.fire('pointerover', { ...mouse, target: child });
  t.clock.tick(HOVER_OPEN_MS);
  assert.equal(t.layer.isOpen(), true);
  t.fire('pointerout', { ...mouse, target: child, relatedTarget: t.body });
  t.clock.tick(CLOSE_GRACE_MS);
  assert.equal(t.layer.isOpen(), false);
  // keyboard focus
  t.doc.activeElement = mark;
  t.fire('focusin', { target: mark });
  assert.equal(t.layer.isOpen(), true);
  t.fire('focusout', { target: mark, relatedTarget: other.mark });
  t.doc.activeElement = other.mark;
  t.fire('focusin', { target: other.mark });
  assert.equal(t.layer.currentKey(), 'memory', 'the next part swaps the tip');
  // tap, activation, refresh, escape, hide
  t.fire('click', { target: mark });
  t.layer.refresh();
  t.fire('keydown', { key: 'Escape' });
  t.fire('click', { target: mark });
  t.fire('click', { target: card });
  t.layer.show('daily-brief', 'tap');
  t.layer.hide();
  assert.equal(focusCalls, 0, 'zero focus() or blur() calls');
});

test('Escape closes only the tip; with no tip open the event is left alone', () => {
  const t = setup();
  t.addPart('daily-brief', CARD);
  assert.equal(t.count('keydown'), 0, 'no keydown listener while closed');
  const idle = t.fire('keydown', { key: 'Escape' });
  assert.equal(idle.defaultPrevented, false);
  assert.equal(idle.stopped, false);

  t.layer.show('daily-brief', 'tap');
  assert.equal(t.count('keydown'), 1);
  const spent = t.fire('keydown', { key: 'Escape', defaultPrevented: true });
  assert.equal(t.layer.isOpen(), true, 'an owner earlier in the capture phase keeps priority');
  assert.equal(spent.stopped, false);
  const other = t.fire('keydown', { key: 'Enter' });
  assert.equal(other.defaultPrevented, false);
  assert.equal(t.layer.isOpen(), true);
  const composing = t.fire('keydown', { key: 'Escape', isComposing: true });
  assert.equal(composing.defaultPrevented, false);

  const escape = t.fire('keydown', { key: 'Escape' });
  assert.equal(escape.defaultPrevented, true);
  assert.equal(escape.stopped, true);
  assert.equal(t.layer.isOpen(), false);
  assert.equal(t.count('keydown'), 0, 'the listener goes with the tip');
});

test('hover opens after a moment, never from touch, and the pointer can cross onto the tip', () => {
  const t = setup();
  const { card } = t.addPart('daily-brief', CARD);
  t.fire('pointerover', { pointerType: 'touch', target: card });
  t.clock.tick(1000);
  assert.equal(t.layer.isOpen(), false, 'a touch hover emulation opens nothing');

  t.fire('pointerover', { ...mouse, target: card });
  t.clock.tick(HOVER_OPEN_MS - 1);
  assert.equal(t.layer.isOpen(), false);
  t.clock.tick(1);
  assert.equal(t.layer.isOpen(), true);
  assert.deepEqual(t.state.shown, ['daily-brief']);

  // Leave the card for the tip within the grace period: it stays.
  t.fire('pointerout', { ...mouse, target: card, relatedTarget: t.body });
  t.clock.tick(CLOSE_GRACE_MS - 10);
  t.fire('pointerover', { ...mouse, target: t.tip.children[1] });
  t.clock.tick(1000);
  assert.equal(t.layer.isOpen(), true, 'the tip is hoverable');
  // Leaving the tip for the page closes it.
  t.fire('pointerout', { ...mouse, target: t.tip.children[1], relatedTarget: t.body });
  t.clock.tick(CLOSE_GRACE_MS);
  assert.equal(t.layer.isOpen(), false);
});

test('a pointer that leaves before the wait ends opens nothing; crossing children does not restart it', () => {
  const t = setup();
  const { card, child } = t.addPart('daily-brief', CARD);
  t.fire('pointerover', { ...mouse, target: card });
  t.clock.tick(200);
  t.fire('pointerover', { ...mouse, target: child });
  t.fire('pointerout', { ...mouse, target: card, relatedTarget: child });
  t.clock.tick(100);
  assert.equal(t.layer.isOpen(), true, 'the wait was not restarted by the child');
  t.layer.hide();

  t.fire('pointerover', { ...mouse, target: card });
  t.clock.tick(100);
  t.fire('pointerout', { ...mouse, target: card, relatedTarget: t.body });
  t.clock.tick(1000);
  assert.equal(t.layer.isOpen(), false);
});

test('moving to another part swaps the tip at once', () => {
  const t = setup();
  const a = t.addPart('daily-brief', CARD);
  const b = t.addPart('memory', {
    left: 240,
    top: 100,
    right: 380,
    bottom: 190,
    width: 140,
    height: 90
  });
  t.layer.show('daily-brief', 'tap');
  t.fire('pointerover', { ...mouse, target: b.card });
  assert.equal(t.layer.currentKey(), 'memory');
  assert.deepEqual(t.state.shown, ['daily-brief', 'memory']);
  assert.equal(a.card.hasAttribute('aria-describedby'), false);
  assert.equal(b.card.getAttribute('aria-describedby'), TIP_ID);
});

test('keyboard focus opens at once; a pointer-driven focus does not', () => {
  const t = setup();
  const { card, mark } = t.addPart('daily-brief', CARD);
  card.keyboardFocus = false;
  t.fire('focusin', { target: card });
  assert.equal(t.layer.isOpen(), false, 'a click that moves focus opens nothing');
  mark.keyboardFocus = true;
  t.fire('focusin', { target: mark });
  assert.equal(t.layer.isOpen(), true);
  // Leaving focus closes it after the grace, unless focus is still on the part.
  t.doc.activeElement = t.body;
  t.fire('focusout', { target: mark, relatedTarget: t.body });
  t.clock.tick(CLOSE_GRACE_MS);
  assert.equal(t.layer.isOpen(), false);
});

test('a focused node that a repaint replaced keeps the tip', () => {
  const t = setup();
  const first = t.addPart('daily-brief', CARD);
  t.doc.activeElement = first.mark;
  t.fire('focusin', { target: first.mark });
  assert.equal(t.layer.isOpen(), true);
  // Repaint: the focused node is removed (focusout with no relatedTarget) and
  // the page puts focus on its replacement before the grace ends.
  first.card.remove();
  first.mark.remove();
  const second = t.addPart('daily-brief', CARD);
  t.fire('focusout', { target: first.mark, relatedTarget: null });
  t.doc.activeElement = second.mark;
  t.layer.refresh();
  t.clock.tick(CLOSE_GRACE_MS * 3);
  assert.equal(t.layer.isOpen(), true);
});

test('Escape dismisses a key: focus restored by a repaint does not bring it back', () => {
  const t = setup();
  const { mark } = t.addPart('daily-brief', CARD);
  t.doc.activeElement = mark;
  t.fire('focusin', { target: mark });
  t.fire('keydown', { key: 'Escape' });
  assert.equal(t.layer.isOpen(), false);
  // the repaint hands focus back to the same ?
  t.fire('focusin', { target: mark });
  assert.equal(t.layer.isOpen(), false, 'still dismissed');
  // focus elsewhere clears the dismissal
  const other = t.addPart('memory', {
    left: 240,
    top: 100,
    right: 380,
    bottom: 190,
    width: 140,
    height: 90
  });
  other.mark.keyboardFocus = false;
  t.fire('focusin', { target: other.mark });
  t.fire('focusin', { target: mark });
  assert.equal(t.layer.isOpen(), true);
});

test('a tap pins the tip; a second tap, a tap elsewhere or Escape closes it', () => {
  const t = setup();
  const { card, mark } = t.addPart('daily-brief', CARD);
  t.fire('click', { target: mark });
  assert.equal(t.layer.isOpen(), true);
  assert.deepEqual(t.state.shown, ['daily-brief']);
  // pinned: pointer and focus leaving do not close it
  t.fire('pointerout', { ...mouse, target: mark, relatedTarget: t.body });
  t.clock.tick(CLOSE_GRACE_MS * 4);
  assert.equal(t.layer.isOpen(), true);
  // a second tap on the same ? closes it and keeps it quiet
  t.fire('click', { target: mark });
  assert.equal(t.layer.isOpen(), false);
  t.fire('pointerover', { ...mouse, target: mark });
  t.clock.tick(HOVER_OPEN_MS);
  assert.equal(t.layer.isOpen(), false, 'dismissed until the pointer leaves');
  t.fire('pointerout', { ...mouse, target: mark, relatedTarget: t.body });
  t.fire('pointerover', { ...mouse, target: mark });
  t.clock.tick(HOVER_OPEN_MS);
  assert.equal(t.layer.isOpen(), true, 'back after the pointer left and returned');
  t.layer.hide();

  // tap elsewhere
  t.fire('click', { target: mark });
  t.fire('pointerdown', { target: t.body });
  assert.equal(t.layer.isOpen(), false);
  // pressing the tip, the part or the ? does not close it
  t.fire('click', { target: mark });
  t.fire('pointerdown', { target: t.tip.children[1] });
  t.fire('pointerdown', { target: mark });
  t.fire('pointerdown', { target: card });
  assert.equal(t.layer.isOpen(), true);
});

test('a pinned tip does not outlive keyboard focus that moved on', () => {
  const t = setup();
  const { mark } = t.addPart('daily-brief', CARD);
  t.doc.activeElement = mark;
  t.fire('click', { target: mark });
  t.fire('focusout', { target: mark, relatedTarget: null });
  t.clock.tick(CLOSE_GRACE_MS * 3);
  assert.equal(t.layer.isOpen(), true, 'a window blur or a removed node is not a Tab');
  const away = new FakeElement('a');
  t.root.appendChild(away);
  t.doc.activeElement = away;
  t.fire('focusout', { target: mark, relatedTarget: away });
  t.clock.tick(CLOSE_GRACE_MS);
  assert.equal(t.layer.isOpen(), false);
});

test('using the part closes the tip and keeps it quiet; using something else closes it too', () => {
  const t = setup();
  const { card, child } = t.addPart('daily-brief', CARD);
  t.layer.show('daily-brief', 'hover', 'part');
  t.fire('click', { target: child });
  assert.equal(t.layer.isOpen(), false);
  t.fire('pointerover', { ...mouse, target: card });
  t.clock.tick(HOVER_OPEN_MS);
  assert.equal(t.layer.isOpen(), false);

  t.fire('pointerout', { ...mouse, target: card, relatedTarget: t.body });
  t.layer.show('daily-brief', 'hover', 'part');
  t.fire('click', { target: t.body });
  assert.equal(t.layer.isOpen(), false, 'a click anywhere else closes it');
});

test('a part that only its ? triggers ignores hover and focus on the part itself', () => {
  const t = setup();
  const { card, child, mark } = t.addPart(
    'notes',
    { left: 20, top: 100, right: 380, bottom: 300, width: 360, height: 200 },
    { trigger: 'mark' }
  );
  t.fire('pointerover', { ...mouse, target: child });
  t.clock.tick(HOVER_OPEN_MS);
  assert.equal(t.layer.isOpen(), false);
  t.fire('focusin', { target: card });
  assert.equal(t.layer.isOpen(), false);
  t.fire('pointerover', { ...mouse, target: mark });
  t.clock.tick(HOVER_OPEN_MS);
  assert.equal(t.layer.isOpen(), true);
  // the pointer wandering into the panel does not keep the tip alive
  t.fire('pointerout', { ...mouse, target: mark, relatedTarget: child });
  t.clock.tick(CLOSE_GRACE_MS);
  assert.equal(t.layer.isOpen(), false);
});

test('onShown that throws cannot break the tip', () => {
  const t = setup();
  t.addPart('daily-brief', CARD);
  const layer = createHqTipLayer({
    document: t.doc,
    window: t.win,
    root: () => t.root,
    scope: () => 'overview',
    content: () => ({ sentence: 'x' }),
    onShown: () => {
      throw new Error('store is full');
    },
    setTimeout: t.clock.setTimeout,
    clearTimeout: t.clock.clearTimeout
  });
  assert.equal(layer.show('daily-brief', 'tap'), true);
  assert.equal(layer.isOpen(), true);
  layer.destroy();
});

test('onHidden says which tip closed and why; it is not fired for nothing, or on destroy', () => {
  const t = setup();
  t.addPart('daily-brief', CARD);
  const hidden = [];
  const layer = createHqTipLayer({
    document: t.doc,
    window: t.win,
    root: () => t.root,
    scope: () => 'overview',
    content: () => ({ sentence: 'x' }),
    onHidden: (key, reason) => {
      hidden.push([key, reason]);
      throw new Error('a throwing callback must not break the tip');
    },
    setTimeout: t.clock.setTimeout,
    clearTimeout: t.clock.clearTimeout
  });
  layer.hide('escape');
  assert.deepEqual(hidden, [], 'nothing was open');
  layer.show('daily-brief', 'tap');
  layer.hide('escape');
  assert.deepEqual(hidden, [['daily-brief', 'escape']]);
  assert.equal(layer.isOpen(), false);
  layer.show('daily-brief', 'tap');
  layer.destroy();
  assert.equal(hidden.length, 1, 'destroy is not a close the page needs to hear about');
});

test('destroy removes the element and every listener; a layer with no DOM is inert', () => {
  const t = setup();
  t.addPart('daily-brief', CARD);
  t.layer.show('daily-brief', 'tap');
  t.layer.destroy();
  assert.equal(t.body.children.includes(t.tip), false);
  assert.equal(t.listeners.length, 0);
  assert.equal(t.layer.isOpen(), false);
  assert.equal(t.layer.show('daily-brief', 'tap'), false);

  const inert = createHqTipLayer({ document: {}, window: {} });
  assert.equal(inert.show('daily-brief'), false);
  assert.equal(inert.isOpen(), false);
  assert.equal(inert.currentKey(), '');
  inert.refresh();
  inert.hide();
  inert.destroy();
});
