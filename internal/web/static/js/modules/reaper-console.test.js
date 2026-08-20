import { test } from 'node:test';
import assert from 'node:assert/strict';

class FakeNode {
  constructor(tag, owner) {
    this.tagName = String(tag || '').toUpperCase();
    this.owner = owner;
    this.children = [];
    this.listeners = new Map();
    this.attributes = new Map();
    this.hidden = false;
    this.className = '';
    this.type = '';
    this._text = '';
    this._id = '';
    this.focused = false;
    this.classList = {
      add: value => {
        const classes = new Set(this.className.split(/\s+/).filter(Boolean));
        classes.add(value);
        this.className = Array.from(classes).join(' ');
      }
    };
  }
  set id(value) {
    this._id = value;
    if (value) this.owner.nodes.set(value, this);
  }
  get id() {
    return this._id;
  }
  set textContent(value) {
    this._text = String(value || '');
    this.children = [];
  }
  get textContent() {
    return this._text + this.children.map(child => child.textContent).join('');
  }
  appendChild(child) {
    this.children.push(child);
    return child;
  }
  setAttribute(name, value) {
    this.attributes.set(name, String(value));
  }
  addEventListener(type, listener) {
    this.listeners.set(type, listener);
  }
  focus() {
    this.focused = true;
  }
}

function makeDocument() {
  const listeners = new Map();
  const doc = {
    nodes: new Map(),
    visibilityState: 'visible',
    readyState: 'complete',
    activeElement: null,
    createElement(tag) {
      return new FakeNode(tag, doc);
    },
    getElementById(id) {
      return doc.nodes.get(id) || null;
    },
    addEventListener(type, listener) {
      listeners.set(type, listener);
    },
    dispatchEvent(event) {
      const listener = listeners.get(event.type);
      if (listener) listener(event);
      return true;
    },
    _dispatch(type) {
      const listener = listeners.get(type);
      if (listener) listener({ type });
    }
  };
  doc.body = new FakeNode('body', doc);
  return doc;
}

const originalWindow = globalThis.window;
const originalDocument = globalThis.document;
const originalFetch = globalThis.fetch;
const originalSetInterval = globalThis.setInterval;
const originalClearInterval = globalThis.clearInterval;
const originalCustomEvent = globalThis.CustomEvent;

const documentStub = makeDocument();
const timers = [];
globalThis.document = documentStub;
globalThis.window = {
  location: { pathname: '/workspaces/ws-reaper' },
  currentWorkspaceId: 'ws-reaper'
};
globalThis.fetch = async () => ({
  ok: true,
  json: async () => ({ connected: false, reason: 'reaper_unreachable', tracks: [] })
});
globalThis.setInterval = (callback, delay) => {
  const timer = { callback, delay, cleared: false };
  timers.push(timer);
  return timer;
};
globalThis.clearInterval = timer => {
  if (timer) timer.cleared = true;
};
globalThis.CustomEvent = class {
  constructor(type, options) {
    this.type = type;
    this.detail = options && options.detail;
  }
};

await import('./reaper-console.js');
const consolePanel = globalThis.window.ReaperConsole;

test('station state reports live project facts and honest offline reasons', () => {
  consolePanel._resetForTest();
  consolePanel.init('ws-reaper');
  assert.equal(consolePanel.stationState().value, 'Checking…');

  consolePanel._setState({
    applies: true,
    connected: true,
    project: 'Song',
    tempo: 120,
    play_state: 'playing',
    track_count: 3,
    tracks: []
  });
  assert.equal(consolePanel.stationState().value, '120 BPM · 3 tracks · playing');
  assert.equal(consolePanel.stationLabel(), 'REAPER · Song');
  assert.match(consolePanel.stationState().description, /Song/);

  consolePanel._setState({
    applies: true,
    connected: false,
    reason: 'web_remote_off',
    tracks: []
  });
  assert.equal(consolePanel.stationState().value, 'Web Remote off');
  assert.equal(consolePanel.stationState().tone, 'degraded');
});

test('polling runs no faster than five seconds and pauses off-map or in a hidden tab', () => {
  consolePanel._resetForTest();
  consolePanel.init('ws-reaper');
  documentStub.visibilityState = 'visible';
  consolePanel.setMapVisible(true);
  const timer = timers.at(-1);
  assert.ok(timer);
  assert.equal(timer.delay, 5000);
  assert.equal(consolePanel._polling(), true);

  consolePanel.setMapVisible(false);
  assert.equal(timer.cleared, true);
  assert.equal(consolePanel._polling(), false);

  consolePanel.setMapVisible(true);
  const visibleTimer = timers.at(-1);
  documentStub.visibilityState = 'hidden';
  documentStub._dispatch('visibilitychange');
  assert.equal(visibleTimer.cleared, true);
  assert.equal(consolePanel._polling(), false);
});

test('the console overlay renders current project and track state in place', () => {
  consolePanel._resetForTest();
  consolePanel.init('ws-reaper');
  documentStub.visibilityState = 'visible';
  consolePanel._setState({
    applies: true,
    connected: true,
    project: 'Reaper Songasd',
    tempo: 120,
    time_signature: '4/4',
    play_state: 'stopped',
    position: '1.1.00',
    track_count: 2,
    tracks: [
      { index: 1, name: 'Drums', muted: true, soloed: false, armed: false },
      { index: 2, name: 'Bass', muted: false, soloed: false, armed: true }
    ]
  });
  assert.equal(consolePanel.open({ trigger: new FakeNode('button', documentStub) }), true);
  const host = documentStub.getElementById('reaperConsole');
  assert.equal(host.hidden, false);
  assert.match(host.textContent, /Reaper Songasd/);
  assert.match(host.textContent, /120 BPM/);
  assert.match(host.textContent, /Drums/);
  assert.match(host.textContent, /Muted/);
  assert.match(host.textContent, /Bass/);
  assert.match(host.textContent, /Armed/);
  consolePanel.close();
});

test('transport actions run one click, surface outcomes, and update live state', async () => {
  consolePanel._resetForTest();
  consolePanel.init('ws-reaper');
  const play = {
    id: '1007',
    label: 'Play',
    description: 'Start playback.',
    source: 'builtin',
    mutates: false,
    needs_confirmation: false
  };
  consolePanel._setActions([play]);
  consolePanel._setState({
    applies: true,
    connected: true,
    project: 'Song',
    tempo: 120,
    time_signature: '4/4',
    play_state: 'stopped',
    position: '1.1.00',
    track_count: 0,
    tracks: []
  });
  consolePanel.open();
  const requests = [];
  globalThis.fetch = async (path, options) => {
    requests.push({ path, options });
    return {
      ok: true,
      status: 200,
      json: async () => ({
        outcome: 'ok',
        action_id: '1007',
        applies: true,
        connected: true,
        project: 'Song',
        tempo: 120,
        time_signature: '4/4',
        play_state: 'playing',
        position: '1.1.00',
        track_count: 0,
        tracks: []
      })
    };
  };

  assert.equal(await consolePanel._executeAction(play, false), true);
  assert.equal(requests.length, 1);
  assert.match(requests[0].path, /actions\/1007\/run$/);
  assert.deepEqual(JSON.parse(requests[0].options.body), { confirmed: false });
  assert.equal(consolePanel._state().play_state, 'playing');
  assert.equal(consolePanel._lastRun().outcome, 'ok');
  const host = documentStub.getElementById('reaperConsole');
  assert.match(host.textContent, /Play completed in REAPER/);
  consolePanel.close();
});

test('mutating actions render an explicit confirmation before execution', async () => {
  consolePanel._resetForTest();
  consolePanel.init('ws-reaper');
  const insert = {
    id: '40001',
    label: 'Insert new track',
    description: 'Add a track.',
    source: 'builtin',
    mutates: true,
    needs_confirmation: true
  };
  consolePanel._setActions([insert]);
  consolePanel._setState({
    applies: true,
    connected: true,
    project: 'Song',
    tempo: 120,
    play_state: 'stopped',
    position: '1.1.00',
    track_count: 0,
    tracks: []
  });
  consolePanel.open();
  let calls = 0;
  globalThis.fetch = async (_path, options) => {
    calls += 1;
    assert.deepEqual(JSON.parse(options.body), { confirmed: true });
    return {
      ok: true,
      status: 200,
      json: async () => ({
        outcome: 'ok',
        action_id: '40001',
        applies: true,
        connected: true,
        project: 'Song',
        tempo: 120,
        play_state: 'stopped',
        position: '1.1.00',
        track_count: 1,
        tracks: []
      })
    };
  };

  assert.equal(consolePanel._requestAction(insert), true);
  assert.equal(calls, 0);
  const host = documentStub.getElementById('reaperConsole');
  assert.match(host.textContent, /Confirm project change/);
  assert.match(host.textContent, /Run Insert new track/);
  assert.equal(await consolePanel._executeAction(insert, true), true);
  assert.equal(calls, 1);
  consolePanel.close();
});

test('a failed REAPER run is never rendered as success', async () => {
  consolePanel._resetForTest();
  consolePanel.init('ws-reaper');
  const stop = { id: '1016', label: 'Stop', needs_confirmation: false };
  consolePanel._setActions([stop]);
  globalThis.fetch = async () => ({
    ok: false,
    status: 409,
    json: async () => ({
      outcome: 'error',
      error_reason: 'REAPER is not connected. Nothing was run.',
      connected: false,
      reason: 'reaper_unreachable',
      tracks: []
    })
  });
  assert.equal(await consolePanel._executeAction(stop, false), false);
  assert.equal(consolePanel._lastRun().outcome, 'error');
  assert.match(consolePanel._lastRun().reason, /Nothing was run/);
});

test('Fix setup routes to the existing reaper_live_control wizard step', () => {
  const opened = [];
  globalThis.window.SetupWizard = {
    getStatus: () => ({
      steps: [
        { id: 'other', kind: 'runtime_readiness', runtime_requirement_key: 'other' },
        {
          id: 'live-control',
          kind: 'runtime_readiness',
          runtime_requirement_key: 'reaper_live_control'
        }
      ]
    }),
    open: id => opened.push(id)
  };
  consolePanel._openSetupFix();
  assert.deepEqual(opened, ['live-control']);
});

test.after(() => {
  consolePanel._resetForTest();
  globalThis.window = originalWindow;
  globalThis.document = originalDocument;
  globalThis.fetch = originalFetch;
  globalThis.setInterval = originalSetInterval;
  globalThis.clearInterval = originalClearInterval;
  globalThis.CustomEvent = originalCustomEvent;
});
