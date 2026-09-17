// Tests for operations-map-activity.js — the Operations map's working units,
// belt pulse, and parcels.
//
//   node --test internal/web/static/js/modules/operations-map-activity.test.js

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

import {
  BELT_WINDOW_FOR_TOOL,
  DWELL_MS,
  FINISH_MS,
  OperationsMapActivity,
  activityFromRealtime
} from './operations-map-activity.js';

// The real line table, so the bubbles say what the Home map says.
const linesSandbox = { window: {} };
vm.runInNewContext(
  readFileSync(new URL('./activity-bubble-lines.js', import.meta.url), 'utf8'),
  linesSandbox
);
const { lineFor } = linesSandbox.window.OriActivityBubbleLines;

// Just enough DOM: compound class/attribute selectors, descendants, and rects.
class Node {
  constructor(tag, { classes = [], attrs = {}, rect } = {}) {
    this.tagName = tag;
    this.classes = new Set(classes);
    this.attrs = { ...attrs };
    this.children = [];
    this.parentNode = null;
    this.style = {};
    this.textContent = '';
    this.rect = rect || { left: 0, top: 0, width: 0, height: 0 };
    this.offsetWidth = 1;
  }
  get className() {
    return [...this.classes].join(' ');
  }
  set className(value) {
    this.classes = new Set(String(value).split(/\s+/).filter(Boolean));
  }
  get classList() {
    return {
      add: c => this.classes.add(c),
      remove: c => this.classes.delete(c),
      contains: c => this.classes.has(c),
      toggle: (c, on) => (on ? this.classes.add(c) : this.classes.delete(c))
    };
  }
  setAttribute(name, value) {
    this.attrs[name] = String(value);
  }
  getAttribute(name) {
    return name in this.attrs ? this.attrs[name] : null;
  }
  appendChild(child) {
    child.parentNode = this;
    this.children.push(child);
    return child;
  }
  removeChild(child) {
    this.children = this.children.filter(entry => entry !== child);
    child.parentNode = null;
    return child;
  }
  replaceChild(fresh, old) {
    this.children[this.children.indexOf(old)] = fresh;
    fresh.parentNode = this;
    old.parentNode = null;
  }
  getBoundingClientRect() {
    return this.rect;
  }
  matches(selector) {
    const classes = [...selector.matchAll(/\.([\w-]+)/g)].map(m => m[1]);
    const attrs = [...selector.matchAll(/\[([\w-]+)(?:="([^"]*)")?\]/g)];
    if (!classes.length && !attrs.length) return false;
    return (
      classes.every(c => this.classes.has(c)) &&
      attrs.every(([, name, value]) =>
        value === undefined ? name in this.attrs : this.attrs[name] === value
      )
    );
  }
  querySelectorAll(selector) {
    const found = [];
    const walk = node =>
      node.children.forEach(child => {
        if (child.matches(selector)) found.push(child);
        walk(child);
      });
    walk(this);
    return found;
  }
  querySelector(selector) {
    return this.querySelectorAll(selector)[0] || null;
  }
}

const fakeDocument = () => ({
  body: new Node('body'),
  createElement: tag => new Node(tag)
});

// A unit card inside a field that is itself offset inside the world, so its
// place is the sum of both offsets.
function unit(name, { command = false, left = 0, field } = {}) {
  const el = new Node('button', {
    classes: ['ws-cmd-map-agent', ...(command ? ['is-command-node'] : [])],
    attrs: { 'data-cmd-map-select-agent': encodeURIComponent(name) }
  });
  Object.assign(el, {
    offsetLeft: left,
    offsetTop: 100,
    offsetWidth: 140,
    offsetHeight: 150,
    offsetParent: field
  });
  return el;
}

// One Operations map as the Command view renders it: a world with a Commander
// and a specialist, and the belt.
function renderMap({ agents = ['Theo', 'Ada'] } = {}) {
  const root = new Node('div', { classes: ['ws-cmd-opmap'] });
  const world = root.appendChild(
    new Node('section', {
      classes: ['ws-cmd-map-world'],
      rect: { left: 20, top: 10, width: 900, height: 600 }
    })
  );
  agents.forEach((name, index) =>
    world.appendChild(
      unit(name, {
        command: index === 0,
        left: 100 + index * 200,
        field: Object.assign(new Node('div'), {
          offsetLeft: 20,
          offsetTop: 30,
          offsetParent: world
        })
      })
    )
  );
  const belt = root.appendChild(new Node('nav', { classes: ['ws-cmd-map-belt'] }));
  ['objective', 'objectives', 'backlog', 'inventory', 'stations'].forEach(key =>
    belt.appendChild(
      new Node('button', {
        classes: ['ws-cmd-map-belt-btn'],
        attrs: { 'data-cmd-map-window': key }
      })
    )
  );
  return root;
}

function harness({ agents, snapshot = { running: [], parcels: [] }, status = 200 } = {}) {
  let now = 1_000_000;
  const timers = [];
  const fetches = [];
  const doc = fakeDocument();
  const state = { root: renderMap({ agents }), snapshot, status };
  const results = [];
  const cards = [];
  const layer = new OperationsMapActivity({
    root: () => state.root,
    workspaceId: () => 'ws-1',
    workspaceName: () => 'Research Lab',
    onOpenResult: payload => results.push(payload),
    fetchImpl: async url => {
      fetches.push(url);
      return {
        ok: state.status >= 200 && state.status < 300,
        status: state.status,
        json: async () => state.snapshot
      };
    },
    now: () => now,
    setTimeout: (fn, ms) => {
      const timer = { fn, at: now + ms, done: false };
      timers.push(timer);
      return timer;
    },
    clearTimeout: timer => {
      if (timer) timer.done = true;
    },
    lineFor,
    document: doc,
    resultCards: {
      open: options => {
        cards.push(options);
        return Promise.resolve({ parcel: { id: options.parcelId, kind: 'task', ref_id: 't1' } });
      }
    }
  });
  return {
    layer,
    state,
    fetches,
    results,
    cards,
    doc,
    unit: name =>
      state.root
        .querySelectorAll('.ws-cmd-map-agent')
        .find(el => el.getAttribute('data-cmd-map-select-agent') === encodeURIComponent(name)),
    world: () => state.root.querySelector('.ws-cmd-map-world'),
    belt: key =>
      state.root.querySelector('.ws-cmd-map-belt-btn[data-cmd-map-window="' + key + '"]'),
    rerender() {
      state.root = renderMap({ agents });
      layer.paint();
    },
    advance(ms) {
      now += ms;
      timers
        .filter(timer => !timer.done && timer.at <= now)
        .sort((a, b) => a.at - b.at)
        .forEach(timer => {
          timer.done = true;
          timer.fn();
        });
    }
  };
}

const realtime = (type, data) => ({ type, workspaceId: 'ws-1', data: { type, data } });
// Bubbles live in the world above the card, keyed by the agent.
const bubbleOf = el =>
  el.parentNode.querySelector(
    '[data-cmd-activity-agent="' +
      decodeURIComponent(el.getAttribute('data-cmd-map-select-agent')).toLowerCase() +
      '"]'
  );
const flush = () => new Promise(resolve => setImmediate(resolve));

test('realtime events are read through an allowlist', () => {
  const call = activityFromRealtime(
    realtime('task.tool_call', {
      task_id: 't1',
      agent: 'Theo',
      tool_name: 'web_search',
      arguments: { query: 'secret plans' }
    })
  );
  assert.deepEqual(call, {
    kind: 'task',
    task_id: 't1',
    agent_name: 'Theo',
    phase: 'step',
    step: { type: 'tool_call', tool_name: 'web_search' }
  });
  const result = activityFromRealtime(
    realtime('task.tool_result', { task_id: 't1', success: false, result: 'boom' })
  );
  assert.deepEqual(result.step, { type: 'tool_result', tool_name: '', ok: false });
  assert.equal(
    activityFromRealtime(realtime('task.completed', { task_id: 't1' })).outcome,
    'succeeded'
  );
  assert.equal(activityFromRealtime(realtime('task.created', { task_id: 't1' })), null);
  assert.equal(activityFromRealtime(realtime('task.started', {})), null, 'no task, no activity');
});

test('a started run steps its unit forward with a bubble, and finishing steps it back', () => {
  const h = harness();
  h.layer.handleRealtimeEvent(realtime('task.started', { task_id: 't1', agent: 'Ada' }));
  const ada = h.unit('Ada');
  assert.ok(ada.classList.contains('is-activity-working'));
  assert.equal(bubbleOf(ada).textContent, 'On it.');
  assert.equal(bubbleOf(ada).getAttribute('aria-hidden'), 'true');
  assert.equal(bubbleOf(ada).parentNode, h.world(), 'outside the card, which clips its contents');
  assert.equal(bubbleOf(ada).style.left, '390px', 'centred on the card');
  assert.equal(bubbleOf(ada).style.top, 30 + 100 - 10 + 'px', 'above the card after its step');
  assert.equal(h.unit('Theo').classList.contains('is-activity-working'), false);

  h.advance(DWELL_MS);
  h.layer.handleRealtimeEvent(realtime('task.completed', { task_id: 't1' }));
  assert.equal(
    bubbleOf(ada).textContent,
    'Done.',
    'the finish is named even without an agent field'
  );
  assert.ok(ada.classList.contains('is-activity-working'), 'still forward while Done. shows');

  h.advance(FINISH_MS);
  assert.equal(ada.classList.contains('is-activity-working'), false, 'stepped back');
  assert.equal(bubbleOf(ada), null);
  assert.deepEqual(h.fetches, ['/api/workspace-map/activity'], 'then its parcel is loaded');
});

test('lines keep the 2.5-second rule and the newest waiting line wins', () => {
  const h = harness();
  const say = (type, data) =>
    h.layer.handleRealtimeEvent(realtime(type, { task_id: 't1', agent: 'Theo', ...data }));
  say('task.started');
  h.advance(500);
  say('task.thinking');
  say('task.tool_call', { tool_name: 'web_search' });
  assert.equal(
    bubbleOf(h.unit('Theo')).textContent,
    'On it.',
    'not replaced before it was readable'
  );
  h.advance(DWELL_MS - 500);
  assert.equal(
    bubbleOf(h.unit('Theo')).textContent,
    'Looking it up…',
    'the thinking line was skipped'
  );
  say('task.tool_result', { tool_name: 'web_search', success: true });
  h.advance(DWELL_MS);
  assert.equal(
    bubbleOf(h.unit('Theo')).textContent,
    'Looking it up…',
    'a quiet step says nothing new'
  );
});

test('a re-render keeps the pose and brings the bubble back without popping it in again', () => {
  const h = harness();
  h.layer.handleRealtimeEvent(realtime('task.started', { task_id: 't1', agent: 'Theo' }));
  assert.equal(bubbleOf(h.unit('Theo')).classList.contains('is-settled'), false);
  h.rerender();
  const theo = h.unit('Theo');
  assert.ok(theo.classList.contains('is-activity-working'));
  assert.equal(bubbleOf(theo).textContent, 'On it.');
  assert.ok(bubbleOf(theo).classList.contains('is-settled'));
});

test('a blocked run shows the clay question that opens the task', () => {
  const h = harness();
  h.layer.handleRealtimeEvent(realtime('task.started', { task_id: 't9', agent: 'Ada' }));
  h.advance(DWELL_MS);
  h.layer.handleRealtimeEvent(realtime('task.blocked', { task_id: 't9' }));
  const ada = h.unit('Ada');
  const bubble = bubbleOf(ada);
  assert.equal(bubble.textContent, 'I need your input.');
  assert.ok(bubble.classList.contains('is-blocked'));
  assert.ok(ada.classList.contains('is-activity-blocked'));
  assert.equal(bubble.getAttribute('data-cmd-activity-open-task'), 't9');

  h.advance(DWELL_MS);
  h.layer.handleRealtimeEvent(realtime('task.resumed', { task_id: 't9' }));
  assert.equal(bubbleOf(ada).textContent, 'Back to it.');
  assert.equal(ada.classList.contains('is-activity-blocked'), false);
});

test('a tool call pulses the belt window that shows what it touched, and nothing else', () => {
  const h = harness();
  h.layer.handleRealtimeEvent(
    realtime('task.tool_call', { task_id: 't1', agent: 'Theo', tool_name: 'workspace_save_note' })
  );
  assert.ok(h.belt('inventory').classList.contains('is-activity-pulse'));
  h.advance(1200);
  assert.equal(h.belt('inventory').classList.contains('is-activity-pulse'), false, 'once');

  h.layer.handleRealtimeEvent(
    realtime('task.tool_call', { task_id: 't1', agent: 'Theo', tool_name: 'web_search' })
  );
  const pulsing = h.state.root.querySelectorAll('.ws-cmd-map-belt-btn.is-activity-pulse');
  assert.equal(pulsing.length, 0, 'no belt item matches a web search');
  assert.equal(BELT_WINDOW_FOR_TOOL.workspace_tasks, 'objectives');
});

const parcel = (overrides = {}) => ({
  id: 'p1',
  workspace_id: 'ws-1',
  kind: 'task',
  ref_id: 't1',
  title: 'Compare launch notes',
  agent_name: 'Ada',
  outcome: 'succeeded',
  produced_at: '2026-09-17T10:00:00Z',
  ...overrides
});

test("parcels land at the unit's feet, only for this workspace", async () => {
  const h = harness({
    snapshot: {
      running: [],
      parcels: [parcel(), parcel({ id: 'other', workspace_id: 'ws-2', agent_name: 'Ada' })]
    }
  });
  await h.layer.loadParcels();
  const drawn = h.world().querySelectorAll('[data-cmd-map-parcel]');
  assert.equal(drawn.length, 1);
  assert.equal(drawn[0].getAttribute('data-cmd-map-parcel'), 'p1');
  assert.match(drawn[0].getAttribute('aria-label'), /Result ready from Ada: Compare launch notes/);
  // Ada's card is 300px into a field 20px into the world: centre 320 + 70.
  assert.equal(drawn[0].style.left, '390px');
  assert.equal(drawn[0].style.top, 30 + 100 + 150 - 6 + 'px', 'at its feet');
  assert.equal(
    drawn[0].classList.contains('is-landing'),
    false,
    'what was there on load does not land'
  );

  h.rerender();
  assert.equal(h.world().querySelectorAll('[data-cmd-map-parcel]').length, 1, 'a render keeps it');
});

test('a parcel whose agent has left the map waits at the command post', async () => {
  const h = harness({
    snapshot: {
      running: [],
      parcels: [
        parcel({ id: 'gone', agent_name: 'Mira', outcome: 'failed' }),
        parcel({ id: 'mine', agent_name: 'Theo', produced_at: '2026-09-17T09:00:00Z' })
      ]
    }
  });
  await h.layer.loadParcels();
  const drawn = h.world().querySelectorAll('[data-cmd-map-parcel]');
  assert.equal(drawn.length, 1, "they share the Commander's spot");
  assert.equal(drawn[0].textContent, '+2');
  assert.equal(drawn[0].getAttribute('data-cmd-map-parcel'), 'gone', 'the newest opens first');
  assert.ok(drawn[0].classList.contains('is-attention'));
});

test('a new parcel lands once, and opening it removes it from the map', async () => {
  const h = harness();
  await h.layer.loadParcels();
  h.state.snapshot = { running: [], parcels: [parcel()] };
  await h.layer.loadParcels();
  const drawn = h.world().querySelector('[data-cmd-map-parcel]');
  assert.ok(drawn.classList.contains('is-landing'));
  h.rerender();
  assert.equal(
    h.world().querySelector('[data-cmd-map-parcel]').classList.contains('is-landing'),
    false
  );

  // A map taller than the window: the card centres in the part on screen.
  h.state.root.rect = { left: 30, top: 200, width: 800, height: 2000, bottom: 2200 };
  const previousWindow = globalThis.window;
  globalThis.window = { innerHeight: 900 };
  try {
    await h.layer.openParcel('p1');
  } finally {
    globalThis.window = previousWindow;
  }
  const host = h.cards[0].host;
  assert.equal(host.parentNode, h.doc.body);
  assert.deepEqual(
    [host.style.left, host.style.top, host.style.width, host.style.height],
    ['30px', '200px', '800px', '700px']
  );
  assert.equal(h.cards.length, 1);
  assert.equal(h.cards[0].parcelId, 'p1');
  assert.equal(h.cards[0].origin, h.unit('Ada'), 'focus returns to the unit, not the parcel');
  assert.equal(h.world().querySelector('[data-cmd-map-parcel]'), null);
  h.cards[0].onPrimary({ parcel: { kind: 'task', ref_id: 't1' } });
  assert.equal(h.results.length, 1);
});

test('a page opened mid-run shows that run, and a switched-off show stays quiet', async () => {
  const h = harness({
    snapshot: {
      running: [
        { kind: 'task', workspace_id: 'ws-1', agent_name: 'Theo', task_id: 't1', blocked: true },
        { kind: 'task', workspace_id: 'ws-2', agent_name: 'Ada', task_id: 't2', blocked: false }
      ],
      parcels: []
    }
  });
  await h.layer.loadParcels();
  assert.equal(bubbleOf(h.unit('Theo')).textContent, 'I need your input.');
  assert.equal(h.unit('Ada').classList.contains('is-activity-working'), false);

  const off = harness({ status: 404 });
  await off.layer.loadParcels();
  await off.layer.loadParcels();
  assert.equal(off.fetches.length, 1, 'a 404 is asked once');
});

test('a run with no agent still gets its parcel loaded', async () => {
  const h = harness();
  assert.equal(h.layer.handleRealtimeEvent(realtime('task.completed', { task_id: 'tx' })), false);
  h.advance(1000);
  await flush();
  assert.deepEqual(h.fetches, ['/api/workspace-map/activity']);
});

test('a deleted task clears its unit and its parcel', async () => {
  const h = harness({ snapshot: { running: [], parcels: [parcel({ agent_name: 'Theo' })] } });
  await h.layer.loadParcels();
  h.layer.handleRealtimeEvent(realtime('task.started', { task_id: 't1', agent: 'Theo' }));
  h.layer.handleRealtimeEvent(realtime('task.deleted', { task_id: 't1' }));
  assert.equal(h.unit('Theo').classList.contains('is-activity-working'), false);
  assert.equal(h.world().querySelector('[data-cmd-map-parcel]'), null);
});
