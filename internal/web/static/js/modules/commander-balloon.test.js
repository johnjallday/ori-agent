import test from 'node:test';
import assert from 'node:assert/strict';

import {
  BALLOON_ANNOUNCE_GAP_MS,
  BALLOON_MAX_WIDTH,
  BALLOON_MIN_WIDTH,
  MAP_CAMERA_EVENT,
  anchorVisible,
  balloonWidth,
  commanderBalloon,
  dockEdge,
  firstFocusable,
  placeBalloon,
  placeLine,
  resetCommanderBalloons,
  shouldAnnounce
} from './commander-balloon.js';

// A fake DOM just big enough for the balloon: tree moves, focus, rects,
// events and class lists.
export function fakeDocument() {
  const doc = { activeElement: null };
  const make = tag => {
    const node = {
      tagName: String(tag).toUpperCase(),
      className: '',
      textContent: '',
      children: [],
      parentElement: null,
      dataset: {},
      style: {},
      attributes: {},
      listeners: {},
      hidden: false,
      disabled: false,
      scrollTop: 0,
      offsetWidth: 0,
      offsetHeight: 0,
      scrollHeight: 0,
      rect: { left: 0, top: 0, width: 0, height: 0 },
      get classList() {
        const list = () => node.className.split(' ').filter(Boolean);
        return {
          contains: name => list().includes(name),
          add: name => {
            if (!list().includes(name)) node.className = [...list(), name].join(' ');
          },
          remove: name => {
            node.className = list()
              .filter(n => n !== name)
              .join(' ');
          },
          toggle: (name, on) => {
            const want = on === undefined ? !list().includes(name) : on;
            if (want) node.classList.add(name);
            else node.classList.remove(name);
          }
        };
      },
      setAttribute(name, value) {
        node.attributes[name] = String(value);
      },
      getAttribute(name) {
        return node.attributes[name] ?? null;
      },
      addEventListener(type, fn) {
        (node.listeners[type] ||= []).push(fn);
      },
      dispatch(type, event = {}) {
        const evt = { type, target: node, preventDefault() {}, stopPropagation() {}, ...event };
        for (let at = node; at; at = at.parentElement)
          (at.listeners[type] || []).forEach(fn => fn(evt));
        return evt;
      },
      append(...kids) {
        kids.forEach(kid => {
          kid.remove();
          kid.parentElement = node;
          node.children.push(kid);
        });
      },
      remove() {
        const parent = node.parentElement;
        if (!parent) return;
        parent.children.splice(parent.children.indexOf(node), 1);
        node.parentElement = null;
        if (doc.activeElement && node.contains(doc.activeElement)) doc.activeElement = doc.body;
      },
      replaceChildren(...kids) {
        [...node.children].forEach(kid => kid.remove());
        node.append(...kids);
      },
      contains(other) {
        for (let at = other; at; at = at.parentElement) if (at === node) return true;
        return false;
      },
      all() {
        return node.children.flatMap(child => [child, ...child.all()]);
      },
      querySelectorAll(selector) {
        return node.all().filter(child => child.classList.contains(selector.replace(/^\./, '')));
      },
      querySelector(selector) {
        return node.querySelectorAll(selector)[0] || null;
      },
      getBoundingClientRect() {
        return {
          ...node.rect,
          right: node.rect.left + node.rect.width,
          bottom: node.rect.top + node.rect.height
        };
      },
      focus() {
        const prev = doc.activeElement;
        doc.activeElement = node;
        if (prev && prev !== node) prev.dispatch?.('focusout', { relatedTarget: node });
        node.dispatch('focusin', { relatedTarget: prev });
      }
    };
    return node;
  };
  doc.createElement = make;
  doc.body = make('body');
  doc.activeElement = doc.body;
  return doc;
}

const area = { left: 0, top: 0, width: 1000, height: 700 };

test('balloonWidth keeps 360–440 px and fits a narrow area', () => {
  assert.equal(balloonWidth(area), BALLOON_MAX_WIDTH);
  assert.equal(balloonWidth({ left: 0, top: 0, width: 400, height: 500 }), 384);
  assert.ok(balloonWidth({ left: 0, top: 0, width: 370, height: 500 }) < BALLOON_MIN_WIDTH);
});

test('placeBalloon prefers above and never covers the anchor', () => {
  const anchor = { left: 480, top: 500, width: 40, height: 40 };
  const p = placeBalloon({ anchor, area, height: 300 });
  assert.equal(p.side, 'above');
  assert.ok(p.top + 300 <= anchor.top, 'balloon ends above the anchor');
  assert.equal(p.width, BALLOON_MAX_WIDTH);
  assert.ok(p.tail.x > 0 && p.tail.x < p.width);
});

test('placeBalloon flips below, then beside, when there is no room above', () => {
  const nearTop = placeBalloon({
    anchor: { left: 480, top: 40, width: 40, height: 40 },
    area,
    height: 300
  });
  assert.equal(nearTop.side, 'below');
  assert.ok(nearTop.top >= 80);
  const tall = placeBalloon({
    anchor: { left: 40, top: 330, width: 40, height: 40 },
    area,
    height: 600
  });
  assert.equal(tall.side, 'right');
  assert.ok(tall.left >= 80, 'right of the anchor');
  const tallRight = placeBalloon({
    anchor: { left: 920, top: 330, width: 40, height: 40 },
    area,
    height: 600
  });
  assert.equal(tallRight.side, 'left');
  assert.ok(tallRight.left + tallRight.width <= 920);
});

test('placeBalloon scrolls on the roomiest side when nothing fits whole', () => {
  const small = { left: 0, top: 0, width: 460, height: 400 };
  const p = placeBalloon({
    anchor: { left: 210, top: 250, width: 40, height: 40 },
    area: small,
    height: 900
  });
  assert.equal(p.side, 'above');
  assert.ok(p.maxHeight < 900 && p.maxHeight > 0);
  assert.ok(p.top >= 0);
});

test('an anchor outside the visible area docks at the nearest edge (FR11)', () => {
  for (const [anchor, edge] of [
    [{ left: -300, top: 300, width: 40, height: 40 }, 'left'],
    [{ left: 1400, top: 300, width: 40, height: 40 }, 'right'],
    [{ left: 500, top: -200, width: 40, height: 40 }, 'top'],
    [{ left: 500, top: 900, width: 40, height: 40 }, 'bottom']
  ]) {
    assert.equal(anchorVisible(anchor, area), false);
    assert.equal(dockEdge(anchor, area), edge);
    const p = placeBalloon({ anchor, area, height: 300 });
    assert.equal(p.side, 'dock');
    assert.equal(p.dock, edge);
    assert.equal(p.tail, null);
    assert.ok(p.left >= 0 && p.left + p.width <= 1000 && p.top >= 0 && p.top + 300 <= 700);
  }
});

test('above its anchor a balloon hangs from its bottom edge; elsewhere it never passes the bottom', () => {
  const above = placeBalloon({
    anchor: { left: 480, top: 500, width: 40, height: 40 },
    area,
    height: 300
  });
  assert.equal(above.side, 'above');
  assert.equal(above.bottomEdge, 500 - 14, 'a taller body grows up, away from the anchor');
  const beside = placeBalloon({
    anchor: { left: 40, top: 600, width: 40, height: 40 },
    area,
    height: 650
  });
  assert.equal(beside.side, 'right');
  assert.equal(beside.bottomEdge, null);
  assert.ok(beside.top + beside.maxHeight <= 700 - 8, 'capped to the room below its top');
  const below = placeBalloon({
    anchor: { left: 480, top: 40, width: 40, height: 40 },
    area,
    height: 900
  });
  assert.notEqual(below.side, 'above');
  assert.ok(below.top + below.maxHeight <= 700 - 8);
});

test('on a short map the balloon goes beside its anchor, narrower, not squashed', () => {
  // 768-wide window: a 700 x 300 map area, the tile in the middle.
  const short = { left: 0, top: 300, width: 700, height: 300 };
  const p = placeBalloon({
    anchor: { left: 330, top: 420, width: 60, height: 60 },
    area: short,
    height: 600
  });
  assert.ok(p.side === 'right' || p.side === 'left');
  assert.ok(p.width >= 300 && p.width <= 440);
  assert.equal(p.maxHeight, 300 - 16, 'the full height of the area');
  assert.ok(p.left >= 0 && p.left + p.width <= 700);
  assert.ok(p.left >= 390 + 14 || p.left + p.width <= 330 - 14, 'beside, not over, the anchor');
});

test('a docked balloon steps aside from an anchor half under the drawer', () => {
  // Visible area 0–612; the Commander spans 520–720, its centre (620) hidden.
  const visible = { left: 0, top: 0, width: 612, height: 700 };
  const anchor = { left: 520, top: 300, width: 200, height: 200 };
  const p = placeBalloon({ anchor, area: visible, height: 300 });
  assert.equal(p.side, 'dock');
  assert.equal(p.dock, 'right');
  assert.ok(p.left + p.width <= anchor.left, 'ends before the visible part of the anchor');
});

test('a new phase starts the balloon body at its top; the same phase keeps its scroll', () => {
  resetCommanderBalloons();
  const doc = fakeDocument();
  const { root, unit } = mapRoot(doc);
  const balloon = commanderBalloon('ws:scroll', { doc, now: () => 0, setTimer: () => 1 });
  balloon.attach(root, { anchor: () => unit, narrow: () => false });
  balloon.update({ ...reportContent(doc), scrollKey: 'report|report' });
  balloon.expand();
  balloon.nodes.body.scrollTop = 200;
  balloon.nodes.body.dispatch('scroll');
  balloon.update({ ...reportContent(doc, 'plan, ticked'), scrollKey: 'report|report' });
  assert.equal(balloon.nodes.body.scrollTop, 200, 'same phase: kept');
  balloon.update({ ...reportContent(doc, 'Setting up'), scrollKey: 'report|running' });
  assert.equal(balloon.nodes.body.scrollTop, 0, 'the run starts at its headline');
});

test('the drawer-narrowed area keeps the balloon out from under the drawer', () => {
  const visible = { left: 0, top: 0, width: 600, height: 700 }; // drawer covers 600–1000
  const p = placeBalloon({
    anchor: { left: 520, top: 400, width: 40, height: 40 },
    area: visible,
    height: 300
  });
  assert.ok(p.left + p.width <= 600);
  const hidden = placeBalloon({
    anchor: { left: 800, top: 400, width: 40, height: 40 },
    area: visible,
    height: 300
  });
  assert.equal(hidden.side, 'dock');
  assert.equal(hidden.dock, 'right');
  assert.ok(hidden.left + hidden.width <= 600);
});

test('placeLine sits over the anchor, below when the top is near', () => {
  const over = placeLine({
    anchor: { left: 480, top: 300, width: 40, height: 40 },
    area,
    width: 200,
    height: 24
  });
  assert.equal(over.below, false);
  assert.ok(over.top + 24 <= 300);
  const under = placeLine({
    anchor: { left: 480, top: 10, width: 40, height: 40 },
    area,
    width: 200,
    height: 24
  });
  assert.equal(under.below, true);
  assert.equal(
    placeLine({
      anchor: { left: -400, top: 10, width: 40, height: 40 },
      area,
      width: 200,
      height: 24
    }),
    null
  );
});

test('shouldAnnounce speaks a new line at most once per 10 s', () => {
  assert.deepEqual(shouldAnnounce('A', { text: '', at: null }, 1000), { now: true, wait: 0 });
  assert.deepEqual(shouldAnnounce('A', { text: 'A', at: 1000 }, 2000), { now: false, wait: 0 });
  assert.deepEqual(shouldAnnounce('B', { text: 'A', at: 1000 }, 4000), { now: false, wait: 7000 });
  assert.deepEqual(shouldAnnounce('B', { text: 'A', at: 1000 }, 1000 + BALLOON_ANNOUNCE_GAP_MS), {
    now: true,
    wait: 0
  });
});

function mapRoot(doc) {
  const root = doc.createElement('div');
  root.rect = { left: 0, top: 0, width: 1000, height: 700 };
  doc.body.append(root);
  const unit = doc.createElement('button');
  unit.className = 'ws-map-unit is-commander';
  unit.rect = { left: 480, top: 500, width: 40, height: 40 };
  root.append(unit);
  return { root, unit };
}

function reportContent(doc, line = 'I have a plan for Albums · Review') {
  return {
    speaker: { name: 'Albums Manager', role: 'Commander' },
    line,
    signature: line,
    body: d => {
      const card = d.createElement('article');
      const goal = d.createElement('input');
      goal.className = 'goal';
      const setUp = d.createElement('button');
      setUp.className = 'set-up';
      card.append(goal, setUp);
      return card;
    }
  };
}

test('three mounts within 100 ms keep one expanded balloon with its content and focus', () => {
  resetCommanderBalloons();
  const doc = fakeDocument();
  let { root, unit } = mapRoot(doc);
  const balloon = commanderBalloon('ws:ws-1', { doc, now: () => 0, setTimer: () => 1 });
  balloon.attach(root, { anchor: () => unit, narrow: () => false });
  balloon.update(reportContent(doc));
  assert.equal(balloon.expand(), true);
  const goal = balloon.nodes.body.querySelector('.goal');
  goal.value = 'Finish the mix';
  goal.focus();
  balloon.nodes.body.scrollTop = 120;
  balloon.nodes.body.dispatch('scroll');
  for (let i = 0; i < 3; i += 1) {
    root.remove(); // the map re-mounts: the old container is gone
    ({ root, unit } = mapRoot(doc));
    assert.equal(doc.activeElement, doc.body, 'focus fell to the body with the old map');
    balloon.attach(root, { anchor: () => unit });
    balloon.update(reportContent(doc));
  }
  const layers = root.querySelectorAll('.commander-balloon-layer');
  assert.equal(layers.length, 1);
  assert.equal(root.querySelectorAll('.commander-balloon').length, 1);
  assert.equal(balloon.expanded, true);
  assert.equal(balloon.nodes.box.hidden, false);
  assert.equal(balloon.nodes.body.querySelector('.goal'), goal, 'same body, same typed goal');
  assert.equal(goal.value, 'Finish the mix');
  assert.equal(doc.activeElement, goal, 'focus came back to the goal');
  assert.equal(balloon.nodes.body.scrollTop, 120);
});

test('expanding focuses the first control, Escape collapses back to the unit', () => {
  resetCommanderBalloons();
  const doc = fakeDocument();
  const { root, unit } = mapRoot(doc);
  const balloon = commanderBalloon('ws:ws-2', { doc, now: () => 0, setTimer: () => 1 });
  balloon.attach(root, { anchor: () => unit, narrow: () => false });
  balloon.update(reportContent(doc));
  assert.equal(balloon.nodes.box.getAttribute('role'), 'dialog');
  assert.ok(balloon.nodes.box.getAttribute('aria-labelledby'));
  balloon.nodes.line.dispatch('click');
  assert.equal(balloon.expanded, true);
  assert.equal(doc.activeElement, balloon.nodes.body.querySelector('.goal'));
  balloon.nodes.box.dispatch('keydown', { key: 'Escape' });
  assert.equal(balloon.expanded, false);
  assert.equal(doc.activeElement, unit);
  assert.equal(balloon.nodes.box.hidden, true);
  assert.equal(balloon.nodes.line.hidden, false);
});

test('Enter on the focused Commander unit expands its balloon', () => {
  resetCommanderBalloons();
  const doc = fakeDocument();
  const { root, unit } = mapRoot(doc);
  const balloon = commanderBalloon('ws:ws-3', { doc, now: () => 0, setTimer: () => 1 });
  balloon.attach(root, { anchor: () => unit, narrow: () => false });
  balloon.update(reportContent(doc));
  unit.dispatch('keydown', { key: 'Enter' });
  assert.equal(balloon.expanded, true);
});

test('one balloon expanded per map: expanding one collapses the other', () => {
  resetCommanderBalloons();
  const doc = fakeDocument();
  const { root, unit } = mapRoot(doc);
  const a = commanderBalloon('home:a', { doc, now: () => 0, setTimer: () => 1 });
  const b = commanderBalloon('home:b', { doc, now: () => 0, setTimer: () => 1 });
  a.attach(root, { anchor: () => unit, narrow: () => false }).update(reportContent(doc));
  b.attach(root, { anchor: () => unit, narrow: () => false }).update(
    reportContent(doc, 'Albums is set up')
  );
  a.expand();
  b.expand();
  assert.equal(a.expanded, false);
  assert.equal(b.expanded, true);
  assert.equal(root.querySelectorAll('.commander-balloon-layer').length, 1);
});

test('below 720 px no balloon expands and the host is asked for the card', () => {
  resetCommanderBalloons();
  const doc = fakeDocument();
  const { root, unit } = mapRoot(doc);
  root.rect = { left: 0, top: 0, width: 390, height: 700 };
  let asked = 0;
  const balloon = commanderBalloon('ws:narrow', { doc, now: () => 0, setTimer: () => 1 });
  balloon.attach(root, { anchor: () => unit, onNarrow: () => asked++ });
  balloon.update(reportContent(doc));
  assert.equal(balloon.expand(), false);
  assert.equal(asked, 1);
  assert.equal(balloon.expanded, false);
});

test('the map camera event re-measures; a hold waits for the slide to end', async () => {
  resetCommanderBalloons();
  const doc = fakeDocument();
  const { root, unit } = mapRoot(doc);
  const balloon = commanderBalloon('ws:cam', { doc, now: () => 0, setTimer: () => 1 });
  balloon.nodes = null;
  balloon.attach(root, { anchor: () => unit, narrow: () => false }).update(reportContent(doc));
  balloon.nodes.line.offsetWidth = 200;
  balloon.nodes.line.offsetHeight = 24;
  balloon.paint();
  const before = balloon.nodes.line.style.left;
  unit.rect = { ...unit.rect, left: 200 };
  root.dispatch(MAP_CAMERA_EVENT);
  assert.notEqual(balloon.nodes.line.style.left, before, 'moved with the camera');
  let release;
  balloon.holdUntil(new Promise(resolve => (release = resolve)));
  const held = balloon.nodes.line.style.left;
  unit.rect = { ...unit.rect, left: 700 };
  root.dispatch(MAP_CAMERA_EVENT);
  assert.equal(balloon.nodes.line.style.left, held, 'still during the slide');
  release();
  await new Promise(resolve => setImmediate(resolve));
  assert.notEqual(balloon.nodes.line.style.left, held, 'moved once after the slide');
});

test('the collapsed line speaks politely at most once per 10 s', () => {
  resetCommanderBalloons();
  const doc = fakeDocument();
  const { root, unit } = mapRoot(doc);
  let clock = 0;
  const timers = [];
  const balloon = commanderBalloon('ws:live', {
    doc,
    now: () => clock,
    setTimer: (fn, ms) => timers.push({ fn, ms })
  });
  balloon.attach(root, { anchor: () => unit, narrow: () => false });
  balloon.update({
    ...reportContent(doc, 'Looking in Album-1 · 3 folders so far'),
    announce: 'Looking around'
  });
  assert.equal(balloon.nodes.live.getAttribute('aria-live'), 'polite');
  assert.equal(balloon.nodes.live.textContent, 'Looking around');
  clock = 2000;
  balloon.update({
    ...reportContent(doc, 'Thinking it through…'),
    announce: 'Thinking it through…'
  });
  assert.equal(balloon.nodes.live.textContent, 'Looking around', 'held back inside the gap');
  assert.equal(timers.length, 1);
  assert.equal(timers[0].ms, 8000);
  clock = 10000;
  timers[0].fn();
  assert.equal(balloon.nodes.live.textContent, 'Thinking it through…');
});

test('a balloon whose report disappears collapses and removes itself (FR30)', () => {
  resetCommanderBalloons();
  const doc = fakeDocument();
  const { root, unit } = mapRoot(doc);
  const balloon = commanderBalloon('ws:gone', { doc, now: () => 0, setTimer: () => 1 });
  balloon.attach(root, { anchor: () => unit, narrow: () => false }).update(reportContent(doc));
  balloon.expand();
  balloon.update(null);
  assert.equal(balloon.expanded, false);
  assert.equal(balloon.nodes.box.hidden, true);
  assert.equal(balloon.nodes.line.hidden, true);
  assert.equal(balloon.nodes.body.children.length, 0, 'no stale controls');
  assert.equal(doc.activeElement, unit);
  assert.equal(balloon.expand(), false);
});

test('firstFocusable skips hidden and disabled controls', () => {
  const doc = fakeDocument();
  const wrap = doc.createElement('div');
  const off = doc.createElement('button');
  off.disabled = true;
  const gone = doc.createElement('input');
  gone.hidden = true;
  const inner = doc.createElement('div');
  const link = doc.createElement('a');
  link.setAttribute('href', '/x');
  inner.append(link);
  wrap.append(off, gone, inner);
  assert.equal(firstFocusable(wrap), link);
});
