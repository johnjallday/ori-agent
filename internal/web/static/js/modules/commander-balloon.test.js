import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

import {
  BALLOON_ANNOUNCE_GAP_MS,
  BALLOON_MAX_WIDTH,
  BALLOON_MIN_WIDTH,
  MAP_CAMERA_EVENT,
  anchorVisible,
  balloonWidth,
  commanderBalloon,
  dockEdge,
  dockLine,
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

test('dockLine keeps a pinned line at the map edge nearest its anchor, with no tail', () => {
  const left = dockLine({
    anchor: { left: -400, top: 300, width: 40, height: 40 },
    area,
    width: 200,
    height: 24
  });
  assert.deepEqual(left, { left: 8, top: 308, below: false, docked: true });
  const below = dockLine({
    anchor: { left: 480, top: 900, width: 40, height: 40 },
    area,
    width: 200,
    height: 24
  });
  assert.equal(below.top, 700 - 8 - 24);
  assert.equal(below.left, 400);
  assert.equal(dockLine({ anchor: null, area, width: 200, height: 24 }), null);
});

test('a pinned line stays on the map when its anchor leaves the view; an unpinned one goes', () => {
  resetCommanderBalloons();
  const doc = fakeDocument();
  const { root, unit } = mapRoot(doc);
  const pinned = commanderBalloon('ws:pinned', { doc, now: () => 0, setTimer: () => 1 });
  pinned.attach(root, { anchor: () => unit, narrow: () => false, pinLine: true });
  pinned.update(reportContent(doc));
  assert.equal(pinned.nodes.line.classList.contains('is-pinned'), true);
  unit.rect = { left: 1400, top: 500, width: 40, height: 40 };
  pinned.reposition();
  assert.equal(pinned.nodes.line.hidden, false);
  assert.equal(pinned.nodes.line.classList.contains('is-docked'), true);
  // Back in view: over its anchor again, with its tail.
  unit.rect = { left: 480, top: 500, width: 40, height: 40 };
  pinned.reposition();
  assert.equal(pinned.nodes.line.classList.contains('is-docked'), false);

  const plain = commanderBalloon('ws:plain', { doc, now: () => 0, setTimer: () => 1 });
  plain.attach(root, { anchor: () => unit, narrow: () => false });
  plain.update(reportContent(doc));
  unit.rect = { left: 1400, top: 500, width: 40, height: 40 };
  plain.reposition();
  assert.equal(plain.nodes.line.hidden, true);
  assert.equal(plain.nodes.line.classList.contains('is-pinned'), false);
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

// FR24, FR26: the header and the folded line draw the Commander's face (the
// markup its card draws), initials only when the host has no face to give.
test("the balloon's header and folded line show the Commander's face, else initials", () => {
  resetCommanderBalloons();
  const doc = fakeDocument();
  const { root, unit } = mapRoot(doc);
  const balloon = commanderBalloon('ws:face', { doc, now: () => 0, setTimer: () => 1 });
  balloon.attach(root, { anchor: () => unit, narrow: () => false });
  const slots = () => [balloon.nodes.lineFace, balloon.nodes.face];
  const speaking = face => ({
    ...reportContent(doc),
    speaker: { name: 'Albums Manager', role: 'Commander', ...(face === undefined ? {} : { face }) }
  });

  // No face from the host: initials, as before.
  balloon.update(speaking());
  for (const slot of slots()) {
    assert.equal(slot.textContent, 'AM');
    assert.equal(slot.classList.contains('has-avatar'), false);
  }

  // The host's face fills both slots.
  const squirrel = '<span class="agent-avatar agent-avatar--character"></span>';
  balloon.update(speaking(squirrel));
  for (const slot of slots()) {
    assert.equal(slot.innerHTML, squirrel);
    assert.equal(slot.classList.contains('has-avatar'), true);
  }

  // The same face again is not written again, so a redraw cannot make it flicker.
  slots().forEach(slot => (slot.innerHTML = 'untouched'));
  balloon.update(speaking(squirrel));
  slots().forEach(slot => assert.equal(slot.innerHTML, 'untouched'));

  // A different face replaces it.
  const robot = '<span class="agent-avatar agent-avatar--generated"></span>';
  balloon.update(speaking(robot));
  slots().forEach(slot => assert.equal(slot.innerHTML, robot));

  // The face goes away (an empty string counts as none): initials come back.
  balloon.update(speaking(''));
  for (const slot of slots()) {
    assert.equal(slot.textContent, 'AM');
    assert.equal(slot.classList.contains('has-avatar'), false);
  }
});

// FR24 to FR27, demo finding 6.4: the card draws the Commander's figure with a
// name plate and a star over it. In a 20 to 54 px slot those covered the face (and
// the start of the agent's name in the chat), so every slot that holds a face keeps
// them off and crops the figure from its own size. A DOM test cannot see layout, so
// the stylesheet's contract is pinned: the slots that hold a face, what is hidden,
// and where each slot gets its size.
test('the small face slots keep the figure free of the card’s plate and star', () => {
  const mapCSS = readFileSync(new URL('../../css/workspace-map.css', import.meta.url), 'utf8');
  const guideCSS = readFileSync(new URL('../../css/ori-guide.css', import.meta.url), 'utf8');
  const slots = [
    '.commander-balloon-face',
    '.personal-assistant-launcher__avatar',
    '.personal-assistant-panel__avatar',
    '.personal-assistant-message__author-avatar'
  ];
  // Prettier wraps long selectors; match on the sheet with its whitespace collapsed.
  const flat = mapCSS.replace(/\s+/g, ' ');
  const rule = selectorTail => {
    const found = [
      ...flat.matchAll(
        new RegExp(
          ':is\\(([^)]*)\\)\\s*' + selectorTail.replace(/[.()>]/g, '\\$&') + '\\s*\\{([^}]*)\\}',
          'g'
        )
      )
    ];
    assert.equal(found.length, 1, `one shared rule for ${selectorTail}`);
    return found[0];
  };
  const card = rule('.ws-map-av');
  for (const slot of slots) {
    assert.ok(card[1].includes(slot), `${slot} is covered by the compact figure rule`);
  }
  assert.match(card[2], /width:\s*var\(--face-size/);
  assert.match(card[2], /height:\s*var\(--face-size/);
  assert.match(card[2], /overflow:\s*hidden/);
  assert.match(card[2], /clip-path:\s*none/, 'the card’s notched corner is not cut from a face');
  assert.match(card[2], /border-radius:\s*var\(--face-radius/);

  const hidden = rule('.ws-map-av :is(.ws-map-av-label, .ws-map-av-commander-mark)');
  assert.match(hidden[2], /display:\s*none/);
  assert.match(rule('.ws-map-av-figure')[2], /calc\(var\(--face-size/);
  assert.match(rule('.ws-map-av > .agent-avatar')[2], /--aa-size:\s*var\(--face-size/);

  // Each slot sets the size it offers: the balloon in the map's sheet, the
  // drawer's three in the guide's.
  assert.match(
    mapCSS,
    /\.commander-balloon-face\.has-avatar\s*\{[^}]*--face-size:\s*26px;[^}]*--face-radius:\s*50%/
  );
  assert.match(
    guideCSS,
    /\.personal-assistant-message__author-avatar\s*\{[^}]*--face-size:\s*20px;[^}]*--face-radius:\s*6px/
  );
  assert.match(
    guideCSS,
    /\.personal-assistant-launcher__avatar,\s*\.personal-assistant-panel__avatar\s*\{[^}]*--face-size:\s*54px;[^}]*--face-radius:\s*12px/
  );
  // The crown is the map's light-theme brown in the drawer too, not the gold fallback.
  const mapKeeper = mapCSS.match(/--ws-keeper:\s*(#9b641b);/);
  assert.ok(mapKeeper, 'the map sets its light-theme keeper colour');
  assert.match(
    guideCSS,
    new RegExp(
      '\\[data-bs-theme="light"\\][^{]*\\.personal-assistant-message__author-avatar[^{]*\\{[^}]*--ws-keeper:\\s*' +
        mapKeeper[1]
    )
  );
});

// FR31, demo finding 7.1b: opening the balloon scrolled its first control into
// view, so a card taller than the balloon (the folder question: sentence, names,
// outcome, safety line, then the buttons) opened with the question sentence
// scrolled out of sight above the buttons. Opening never scrolls the body now.
test('opening a balloon whose first control is below the fold keeps the question in view', () => {
  resetCommanderBalloons();
  const doc = fakeDocument();
  const { root, unit } = mapRoot(doc);
  const balloon = commanderBalloon('ws:tall', { doc, now: () => 0, setTimer: () => 1 });
  balloon.attach(root, { anchor: () => unit, narrow: () => false });
  balloon.update(reportContent(doc));
  const body = balloon.nodes.body;
  const goal = body.querySelector('.goal');
  let scrolled = 0;
  goal.scrollIntoView = () => (scrolled += 1);
  body.scrollIntoView = () => (scrolled += 1);

  assert.equal(body.getAttribute('tabindex'), '-1', 'the body can take script focus');
  assert.equal(body.getAttribute('role'), 'group');
  assert.match(body.getAttribute('aria-labelledby'), /-title$/);

  // The body shows 100 px (top 100); the control sits at 250: not in view.
  body.rect = { left: 0, top: 100, width: 360, height: 100 };
  goal.rect = { left: 0, top: 250, width: 200, height: 44 };
  body.scrollTop = 0;
  assert.equal(balloon.expand(), true);
  assert.equal(doc.activeElement, body, 'focus waits on the body, not on a control no one can see');
  assert.equal(body.scrollTop, 0, 'the question is still at the top');
  assert.equal(scrolled, 0, 'nothing was scrolled into view');
  assert.equal(balloon.outOfView(goal), true);

  // Closing and opening again, and the "and N more" step, behave the same.
  balloon.collapse({ returnFocus: false });
  assert.equal(balloon.expand(), true);
  assert.equal(doc.activeElement, body);
  assert.equal(body.scrollTop, 0);

  // A control that is in view takes focus, as before, still without scrolling.
  goal.rect = { left: 0, top: 120, width: 200, height: 44 };
  balloon.collapse({ returnFocus: false });
  assert.equal(balloon.expand(), true);
  assert.equal(doc.activeElement, goal);
  assert.equal(balloon.outOfView(goal), false);
  assert.equal(scrolled, 0);

  // A control cut by the body's lower edge counts as out of view; one wholly
  // inside, or a body with no height yet, does not.
  goal.rect = { left: 0, top: 180, width: 200, height: 44 };
  assert.equal(balloon.outOfView(goal), true);
  goal.rect = { left: 0, top: 156, width: 200, height: 44 };
  assert.equal(balloon.outOfView(goal), false);
  body.rect = { left: 0, top: 100, width: 360, height: 0 };
  goal.rect = { left: 0, top: 900, width: 200, height: 44 };
  assert.equal(balloon.outOfView(goal), false, 'no layout yet: cannot tell, so focus the control');
  assert.equal(balloon.outOfView(null), false);
  assert.equal(balloon.outOfView(body), false);
});

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

// A host that dismisses (the member balloon on a group page) closes the balloon
// outright: its line exists only while the mark that opened it does.
test('with onDismiss, Escape in the box, on the line and the close control dismiss instead of folding', () => {
  resetCommanderBalloons();
  const doc = fakeDocument();
  const { root, unit } = mapRoot(doc);
  const balloon = commanderBalloon('member:dismiss', { doc, now: () => 0, setTimer: () => 1 });
  const dismissed = [];
  balloon.attach(root, {
    anchor: () => unit,
    narrow: () => false,
    onDismiss: () => dismissed.push('dismissed')
  });
  balloon.update(reportContent(doc));
  assert.equal(balloon.nodes.close.getAttribute('aria-label'), 'Close');
  balloon.expand();
  assert.equal(balloon.expanded, true);
  const focused = doc.activeElement;

  balloon.nodes.box.dispatch('keydown', { key: 'Escape' });
  assert.equal(dismissed.length, 1, 'Escape in the box');
  assert.equal(balloon.expanded, true, 'the balloon did not fold by itself: the host decides');
  balloon.nodes.close.dispatch('click');
  assert.equal(dismissed.length, 2, 'the close control');
  assert.equal(balloon.expanded, true);
  balloon.nodes.line.dispatch('keydown', { key: 'Escape' });
  assert.equal(dismissed.length, 3, 'Escape on the folded line');
  assert.equal(doc.activeElement, focused, 'dismissing does not move focus; the host does');

  // Any other key on the line does nothing.
  balloon.nodes.line.dispatch('keydown', { key: 'a' });
  assert.equal(dismissed.length, 3);

  // A later attach that says nothing about onDismiss keeps it.
  balloon.attach(root, { anchor: () => unit });
  balloon.nodes.box.dispatch('keydown', { key: 'Escape' });
  assert.equal(dismissed.length, 4);
  // A host that takes it away puts the old behaviour back.
  balloon.attach(root, { onDismiss: null });
  assert.equal(balloon.nodes.close.getAttribute('aria-label'), 'Collapse');
  balloon.nodes.box.dispatch('keydown', { key: 'Escape' });
  assert.equal(balloon.expanded, false, 'folded, as every other balloon');
  assert.equal(dismissed.length, 4);
});

test('a balloon with no onDismiss folds on Escape and ignores Escape on its line, as it always did', () => {
  resetCommanderBalloons();
  const doc = fakeDocument();
  const { root, unit } = mapRoot(doc);
  const balloon = commanderBalloon('home:plain', { doc, now: () => 0, setTimer: () => 1 });
  balloon.attach(root, { anchor: () => unit, narrow: () => false });
  balloon.update(reportContent(doc));
  assert.equal(balloon.nodes.close.getAttribute('aria-label'), 'Collapse');
  balloon.expand();
  balloon.nodes.line.dispatch('keydown', { key: 'Escape' });
  assert.equal(balloon.expanded, true, 'Escape on the line does nothing');
  balloon.nodes.box.dispatch('keydown', { key: 'Escape' });
  assert.equal(balloon.expanded, false, 'Escape in the box folds');
  balloon.expand();
  balloon.nodes.close.dispatch('click');
  assert.equal(balloon.expanded, false, 'the close control folds');
});

test('focusLine puts focus on the folded line, and says false when there is nothing to focus', () => {
  resetCommanderBalloons();
  const doc = fakeDocument();
  const { root, unit } = mapRoot(doc);
  const balloon = commanderBalloon('member:focus', { doc, now: () => 0, setTimer: () => 1 });
  balloon.attach(root, { anchor: () => unit, narrow: () => false });
  assert.equal(balloon.focusLine(), false, 'no content yet');
  balloon.update(reportContent(doc));
  assert.equal(balloon.focusLine(), true);
  assert.equal(doc.activeElement, balloon.nodes.line);
  assert.equal(balloon.expanded, false, 'focusing the line does not open it');
  balloon.remove();
  assert.equal(balloon.focusLine(), false, 'a removed balloon has no line');
});
