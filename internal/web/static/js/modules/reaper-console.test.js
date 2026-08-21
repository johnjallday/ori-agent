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
    child.parent = this;
    return child;
  }
  setAttribute(name, value) {
    this.attributes.set(name, String(value));
  }
  getAttribute(name) {
    return this.attributes.has(name) ? this.attributes.get(name) : null;
  }
  addEventListener(type, listener) {
    this.listeners.set(type, listener);
  }
  removeEventListener(type, listener) {
    if (this.listeners.get(type) === listener) this.listeners.delete(type);
  }
  focus() {
    this.focused = true;
  }
  dispatch(type, event) {
    const listener = this.listeners.get(type);
    if (listener) listener(event || { type });
  }
  // Minimal stand-in for real closest(): supports only the "[attr]" selector
  // shape this module actually uses, walking the appendChild parent chain.
  closest(selector) {
    const match = /^\[([\w-]+)\]$/.exec(selector);
    const attr = match ? match[1] : null;
    let node = this;
    while (node) {
      if (attr && node.attributes && node.attributes.has(attr)) return node;
      node = node.parent;
    }
    return null;
  }
}

// Walk the rendered tree for nodes matching a class, so a test can find the
// one strip control it wants without a real DOM.
function findAll(node, className, found = []) {
  if (!node) return found;
  if (
    String(node.className || '')
      .split(/\s+/)
      .includes(className)
  )
    found.push(node);
  (node.children || []).forEach(child => findAll(child, className, found));
  return found;
}

function findOne(node, className) {
  return findAll(node, className)[0] || null;
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
    removeEventListener(type, listener) {
      if (listeners.get(type) === listener) listeners.delete(type);
    },
    dispatchEvent(event) {
      const listener = listeners.get(event.type);
      if (listener) listener(event);
      return true;
    },
    // Drag hit-testing stands in for document.elementFromPoint + closest():
    // a test sets doc._elementFromPoint to a function returning a FakeNode
    // (or null) rather than simulating real pixel geometry.
    _elementFromPoint: null,
    elementFromPoint(x, y) {
      return typeof doc._elementFromPoint === 'function' ? doc._elementFromPoint(x, y) : null;
    },
    _dispatch(type) {
      const listener = listeners.get(type);
      if (listener) listener({ type });
    },
    _hasListener(type) {
      return listeners.has(type);
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
  assert.match(host.textContent, /Bass/);
  // Muted/soloed/armed now render as M/S/R toggle chips, not words.
  const chips = findAll(host, 'reaper-console-track-chip');
  const muteChip = chips.find(chip => chip.attributes.get('aria-label').startsWith('Mute track 1'));
  assert.equal(muteChip.attributes.get('aria-pressed'), 'true');
  const armChip = chips.find(chip =>
    chip.attributes.get('aria-label').startsWith('Record-arm track 2')
  );
  assert.equal(armChip.attributes.get('aria-pressed'), 'true');
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

test('Tier 2 actions still render an explicit confirmation before execution', async () => {
  consolePanel._resetForTest();
  consolePanel.init('ws-reaper');
  const record = {
    id: '1013',
    label: 'Record',
    description: 'Begin recording.',
    source: 'builtin',
    mutates: true,
    needs_confirmation: true
  };
  consolePanel._setActions([record]);
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
        action_id: '1013',
        applies: true,
        connected: true,
        project: 'Song',
        tempo: 120,
        play_state: 'recording',
        position: '1.1.00',
        track_count: 1,
        tracks: []
      })
    };
  };

  assert.equal(consolePanel._requestAction(record), true);
  assert.equal(calls, 0);
  const host = documentStub.getElementById('reaperConsole');
  assert.match(host.textContent, /Confirm project change/);
  assert.match(host.textContent, /Run Record/);
  assert.equal(await consolePanel._executeAction(record, true), true);
  assert.equal(calls, 1);
  assert.equal(consolePanel._toasts().length, 0);
  consolePanel.close();
});

test('a Tier 1 action fires a toast with Undo, and toasts stack', async () => {
  consolePanel._resetForTest();
  consolePanel.init('ws-reaper');
  const metronome = { id: '40364', label: 'Toggle metronome', needs_confirmation: false };
  const insert = { id: '40001', label: 'Insert new track', needs_confirmation: false };
  consolePanel._setActions([metronome, insert]);
  consolePanel._setState({ applies: true, connected: true, tracks: [] });
  consolePanel.open();
  globalThis.fetch = async path => ({
    ok: true,
    status: 200,
    json: async () => ({
      outcome: 'ok',
      action_id: /40364/.test(path) ? '40364' : '40001',
      applies: true,
      connected: true,
      tracks: [],
      undo: {
        summary: /40364/.test(path) ? 'Toggled the metronome' : 'Inserted a new track',
        command_id: '40029'
      }
    })
  });

  await consolePanel._executeAction(metronome, false);
  await consolePanel._executeAction(insert, false);
  assert.equal(consolePanel._toasts().length, 2);
  const host = documentStub.getElementById('reaperConsole');
  assert.match(host.textContent, /Toggled the metronome/);
  assert.match(host.textContent, /Inserted a new track/);
  consolePanel.close();
});

test('toast Undo posts REAPER global undo and dismisses the toast', async () => {
  consolePanel._resetForTest();
  consolePanel.init('ws-reaper');
  consolePanel._setState({ applies: true, connected: true, tracks: [] });
  const toast = consolePanel._addToast('Renamed ‘Track 3’ to ‘Kick’', {
    summary: 'Renamed',
    command_id: '40029'
  });
  const requests = [];
  globalThis.fetch = async (path, options) => {
    requests.push({ path, options });
    return {
      ok: true,
      status: 200,
      json: async () => ({ outcome: 'ok', applies: true, connected: true, tracks: [] })
    };
  };
  await consolePanel._undoFromToast(toast.id);
  // The undo call, plus the forced immediate state re-read (requirement 4.1.7).
  assert.ok(requests.length >= 1);
  assert.match(requests[0].path, /actions\/40029\/run$/);
  assert.deepEqual(JSON.parse(requests[0].options.body), { confirmed: true });
  assert.equal(consolePanel._toasts().length, 0);
});

test('Undo and Redo never produce a toast of their own', async () => {
  consolePanel._resetForTest();
  consolePanel.init('ws-reaper');
  const undoAction = { id: '40029', label: 'Undo', needs_confirmation: false };
  consolePanel._setActions([undoAction]);
  consolePanel._setState({ applies: true, connected: true, tracks: [] });
  consolePanel.open();
  globalThis.fetch = async () => ({
    ok: true,
    status: 200,
    json: async () => ({
      outcome: 'ok',
      action_id: '40029',
      applies: true,
      connected: true,
      tracks: []
    })
  });
  await consolePanel._executeAction(undoAction, false);
  assert.equal(consolePanel._toasts().length, 0);
  consolePanel.close();
});

test('no strip controls render while REAPER is disconnected', () => {
  consolePanel._resetForTest();
  consolePanel.init('ws-reaper');
  consolePanel._setActions([]);
  consolePanel._setScripts([]);
  consolePanel._setState({
    applies: true,
    connected: false,
    reason: 'reaper_unreachable',
    tracks: []
  });
  consolePanel.open();

  const host = documentStub.getElementById('reaperConsole');
  // The offline panel, not a track list — even a track_editing_available:true
  // left over from a prior connected state must not leak controls through.
  assert.equal(findAll(host, 'reaper-console-track').length, 0);
  assert.equal(findAll(host, 'reaper-console-track-grip').length, 0);
  assert.equal(findAll(host, 'reaper-console-track-chip').length, 0);
  assert.equal(findAll(host, 'reaper-console-track-swatch').length, 0);
  assert.match(host.textContent, /REAPER not running/);
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

test('agent script proposals show readable Lua, real draft errors, and global save scope', () => {
  consolePanel._resetForTest();
  consolePanel.init('ws-reaper');
  consolePanel._setActions([]);
  consolePanel._setScripts([]);
  const proposal = {
    id: 'proposal-1',
    filename: 'layout.lua',
    name: 'Build layout',
    description: 'Adds the standard track layout.',
    needs_confirmation: true,
    code: 'reaper.NoSuchFunction()\n',
    tested_successfully: false,
    last_run: { outcome: 'error', error_text: 'attempt to call nil value' }
  };
  consolePanel._setProposals([proposal]);
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
  const host = documentStub.getElementById('reaperConsole');
  assert.match(host.textContent, /Script drafts/);
  assert.match(host.textContent, /reaper.NoSuchFunction/);
  assert.match(host.textContent, /attempt to call nil value/);
  assert.match(host.textContent, /Untested/);

  consolePanel._requestProposalSave(proposal);
  assert.match(host.textContent, /available in every REAPER workspace on this Mac/);
  assert.match(host.textContent, /This draft is untested/);
  assert.match(host.textContent, /Save for every workspace/);
  consolePanel.close();
});

test('a draft run surfaces the runner error instead of claiming success', async () => {
  consolePanel._resetForTest();
  consolePanel.init('ws-reaper');
  const proposal = {
    id: 'proposal-2',
    filename: 'broken.lua',
    name: 'Broken draft',
    description: 'Test error reporting.',
    needs_confirmation: true,
    code: 'error("broken")',
    tested_successfully: false
  };
  consolePanel._setActions([]);
  consolePanel._setScripts([]);
  consolePanel._setProposals([proposal]);
  globalThis.fetch = async () => ({
    ok: false,
    status: 502,
    json: async () => ({
      proposal_id: 'proposal-2',
      outcome: 'error',
      error_text: 'runner exploded on line 1',
      tested_successfully: false
    })
  });
  assert.equal(await consolePanel._runProposal(proposal, true), false);
  consolePanel._setState({ applies: true, connected: true, tracks: [] });
  consolePanel.open();
  const host = documentStub.getElementById('reaperConsole');
  assert.match(host.textContent, /runner exploded on line 1/);
  assert.doesNotMatch(host.textContent, /ran successfully as a draft/);
  consolePanel.close();
});

test('the shared custom script library lists metadata and run controls', () => {
  consolePanel._resetForTest();
  consolePanel.init('ws-reaper');
  consolePanel._setActions([
    {
      id: 'custom:band.lua',
      label: 'Add band tracks',
      description: 'Adds the standard band layout.',
      source: 'custom',
      mutates: true,
      needs_confirmation: true
    }
  ]);
  consolePanel._setScripts([
    {
      id: 'custom:band.lua',
      filename: 'band.lua',
      name: 'Add band tracks',
      description: 'Adds the standard band layout.',
      needs_confirmation: true,
      metadata_valid: true
    },
    {
      id: 'custom:legacy.lua',
      filename: 'legacy.lua',
      name: 'legacy.lua',
      needs_confirmation: true,
      metadata_valid: false
    }
  ]);
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
  const host = documentStub.getElementById('reaperConsole');
  assert.match(host.textContent, /Script library/);
  assert.match(host.textContent, /Add band tracks/);
  assert.match(host.textContent, /Review run/);
  assert.match(host.textContent, /Metadata missing or malformed/);
  consolePanel.close();
});

test('registered scripts and the raw command escape hatch share the action surface', () => {
  consolePanel._resetForTest();
  consolePanel.init('ws-reaper');
  consolePanel._setActions([
    {
      id: '_RSdeadBEEF',
      label: 'Add arrangement markers',
      description: 'Registered ReaScript: Add arrangement markers',
      source: 'registered',
      mutates: true,
      needs_confirmation: true
    }
  ]);
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
  const host = documentStub.getElementById('reaperConsole');
  assert.match(host.textContent, /Add arrangement markers/);
  assert.match(host.textContent, /Raw command ID/);
  assert.match(host.textContent, /Decimal or _RS hexadecimal IDs always require confirmation/);

  consolePanel._requestAction({
    id: '40001',
    label: 'Raw command 40001',
    source: 'raw',
    mutates: true,
    needs_confirmation: true
  });
  assert.match(host.textContent, /Confirm project change/);
  assert.match(host.textContent, /Run Raw command 40001/);
  consolePanel.close();
});

function liveTrackState(tracks) {
  return {
    applies: true,
    connected: true,
    project: 'Song',
    tempo: 120,
    time_signature: '4/4',
    play_state: 'stopped',
    position: '1.1.00',
    track_editing_available: true,
    track_count: tracks.length,
    tracks
  };
}

test('clicking a track name opens an inline editor that commits on Enter', async () => {
  consolePanel._resetForTest();
  consolePanel.init('ws-reaper');
  consolePanel._setActions([]);
  consolePanel._setScripts([]);
  consolePanel._setState(liveTrackState([{ index: 1, name: 'Drums' }]));
  consolePanel.open();

  const host = documentStub.getElementById('reaperConsole');
  const nameButton = findOne(host, 'reaper-console-track-name');
  assert.ok(nameButton, 'the strip name should be a control');
  nameButton.dispatch('click');
  assert.equal(consolePanel._editingIndex(), 1);

  const input = findOne(host, 'reaper-console-track-name-input');
  assert.ok(input);
  assert.equal(input.value, 'Drums');
  assert.equal(input.focused, true);

  const requests = [];
  globalThis.fetch = async (path, options) => {
    requests.push({ path, options });
    return {
      ok: true,
      status: 200,
      json: async () => ({
        ...liveTrackState([{ index: 1, name: 'Kick' }]),
        outcome: 'ok',
        undo: { summary: 'Renamed ‘Drums’ to ‘Kick’' }
      })
    };
  };
  input.value = 'Kick';
  input.dispatch('keydown', { key: 'Enter' });
  await new Promise(resolve => setImmediate(resolve));

  const rename = requests.find(request => /tracks\/1\/rename$/.test(request.path));
  assert.ok(rename, 'Enter should post the rename');
  assert.deepEqual(JSON.parse(rename.options.body), { name: 'Kick', expected_name: 'Drums' });
  assert.equal(consolePanel._toasts().length, 1);
  assert.match(consolePanel._toasts()[0].message, /Renamed/);
  consolePanel.close();
});

test('Escape cancels an inline rename without calling the server', () => {
  consolePanel._resetForTest();
  consolePanel.init('ws-reaper');
  consolePanel._setActions([]);
  consolePanel._setScripts([]);
  consolePanel._setState(liveTrackState([{ index: 1, name: 'Drums' }]));
  consolePanel.open();

  const host = documentStub.getElementById('reaperConsole');
  let calls = 0;
  globalThis.fetch = async () => {
    calls += 1;
    return { ok: true, status: 200, json: async () => ({}) };
  };
  consolePanel._beginTrackRename(1);
  const input = findOne(host, 'reaper-console-track-name-input');
  input.value = 'Kick';
  input.dispatch('keydown', { key: 'Escape' });
  assert.equal(consolePanel._editingIndex(), 0);
  assert.equal(calls, 0);
  consolePanel.close();
});

test('an empty name is rejected client-side with no server call', () => {
  consolePanel._resetForTest();
  consolePanel.init('ws-reaper');
  consolePanel._setActions([]);
  consolePanel._setScripts([]);
  consolePanel._setState(liveTrackState([{ index: 1, name: 'Drums' }]));
  consolePanel.open();

  const host = documentStub.getElementById('reaperConsole');
  let calls = 0;
  globalThis.fetch = async () => {
    calls += 1;
    return { ok: true, status: 200, json: async () => ({}) };
  };
  consolePanel._beginTrackRename(1);
  const input = findOne(host, 'reaper-console-track-name-input');
  input.value = '   ';
  input.dispatch('keydown', { key: 'Enter' });
  assert.equal(calls, 0);
  assert.match(host.textContent, /A track name cannot be empty/);
  consolePanel.close();
});

test('a failed rename reverts the optimistic update and shows the guard notice', async () => {
  consolePanel._resetForTest();
  consolePanel.init('ws-reaper');
  consolePanel._setActions([]);
  consolePanel._setScripts([]);
  consolePanel._setState(liveTrackState([{ index: 1, name: 'Drums' }]));
  consolePanel.open();

  globalThis.fetch = async (path, options) => {
    // The forced state re-read still succeeds; only the edit is refused.
    if (!options || options.method !== 'POST') {
      return {
        ok: true,
        status: 200,
        json: async () => liveTrackState([{ index: 1, name: 'Drums' }])
      };
    }
    return {
      ok: false,
      status: 409,
      json: async () => ({
        ...liveTrackState([{ index: 1, name: 'Drums' }]),
        outcome: 'error',
        code: 'track_list_changed',
        error_reason: 'The track list changed — refreshed.'
      })
    };
  };

  assert.equal(await consolePanel._renameTrack(1, 'Drums', 'Kick'), false);
  // The optimistic value is gone and the strip reports what happened.
  assert.equal(consolePanel._pendingEdit(), null);
  assert.equal(consolePanel._stripNotice().text, 'The track list changed — refreshed.');
  const host = documentStub.getElementById('reaperConsole');
  assert.match(host.textContent, /The track list changed/);
  assert.match(host.textContent, /Drums/);
  assert.equal(consolePanel._toasts().length, 0);
  consolePanel.close();
});

test('a track-edit toast undoes through the specific inverse endpoint', async () => {
  consolePanel._resetForTest();
  consolePanel.init('ws-reaper');
  consolePanel._setState(liveTrackState([{ index: 1, name: 'Kick' }]));
  const toast = consolePanel._addToast('Renamed ‘Drums’ to ‘Kick’', { kind: 'track_edit' });

  const requests = [];
  globalThis.fetch = async (path, options) => {
    requests.push({ path, options });
    return {
      ok: true,
      status: 200,
      json: async () => ({ ...liveTrackState([{ index: 1, name: 'Drums' }]), outcome: 'ok' })
    };
  };
  await consolePanel._undoFromToast(toast.id);
  // The specific inverse, never REAPER's global undo command.
  assert.match(requests[0].path, /tracks\/undo$/);
  assert.ok(!requests.some(request => /actions\/40029\/run$/.test(request.path)));
  assert.equal(consolePanel._toasts().length, 0);
});

test('strips render read-only when the runner is unavailable', () => {
  consolePanel._resetForTest();
  consolePanel.init('ws-reaper');
  consolePanel._setActions([]);
  consolePanel._setScripts([]);
  const state = liveTrackState([{ index: 1, name: 'Drums' }]);
  state.track_editing_available = false;
  consolePanel._setState(state);
  consolePanel.open();

  const host = documentStub.getElementById('reaperConsole');
  assert.match(host.textContent, /Track editing needs the Ori REAPER runner/);
  assert.match(host.textContent, /read-only until it is installed/);
  // No name control renders, so there is no dead thing to click.
  assert.equal(findAll(host, 'reaper-console-track-name').length, 1);
  assert.equal(findOne(host, 'reaper-console-track-name').tagName, 'STRONG');
  consolePanel.close();
});

test('the M/S/R chips reflect current state and toggle with a click', async () => {
  consolePanel._resetForTest();
  consolePanel.init('ws-reaper');
  consolePanel._setActions([]);
  consolePanel._setScripts([]);
  consolePanel._setState(
    liveTrackState([{ index: 1, name: 'Drums', muted: false, soloed: false, armed: false }])
  );
  consolePanel.open();

  const host = documentStub.getElementById('reaperConsole');
  const chips = findAll(host, 'reaper-console-track-chip');
  assert.equal(chips.length, 3);
  const muteChip = chips.find(chip => chip.attributes.get('aria-label').startsWith('Mute'));
  assert.equal(muteChip.attributes.get('aria-pressed'), 'false');

  const requests = [];
  globalThis.fetch = async (path, options) => {
    requests.push({ path, options });
    return {
      ok: true,
      status: 200,
      json: async () => ({
        ...liveTrackState([{ index: 1, name: 'Drums', muted: true }]),
        outcome: 'ok',
        undo: { summary: 'Muted ‘Drums’' }
      })
    };
  };
  muteChip.dispatch('click');
  await new Promise(resolve => setImmediate(resolve));

  const mute = requests.find(request => /tracks\/1\/mute$/.test(request.path));
  assert.ok(mute, 'clicking the M chip should post to the mute endpoint');
  assert.deepEqual(JSON.parse(mute.options.body), { value: true, expected_name: 'Drums' });
  assert.equal(consolePanel._toasts().length, 1);
  assert.match(consolePanel._toasts()[0].message, /Muted/);
  consolePanel.close();
});

test('disconnect mid-edit reverts optimistically and says plainly nothing was applied — every operation', async () => {
  consolePanel._resetForTest();
  consolePanel.init('ws-reaper');
  consolePanel._setActions([]);
  consolePanel._setScripts([]);
  consolePanel.open();

  // The same runTrackEdit path serves all four operations; a genuine network
  // failure (not a 409) exercises the catch branch identically for each.
  globalThis.fetch = async () => {
    throw new Error('network unreachable');
  };

  assert.equal(await consolePanel._setTrackColor(1, 'Drums', 0), false);
  assert.equal(consolePanel._pendingEdit(), null);
  assert.match(consolePanel._stripNotice().text, /Nothing was applied/);

  assert.equal(await consolePanel._setTrackToggle('mute', 1, 'Drums', true), false);
  assert.equal(consolePanel._pendingEdit(), null);
  assert.match(consolePanel._stripNotice().text, /Nothing was applied/);

  assert.equal(await consolePanel._moveTrack(1, 'Drums', 2), false);
  assert.equal(consolePanel._pendingEdit(), null);
  assert.match(consolePanel._stripNotice().text, /Nothing was applied/);

  // Each call above leaves its own fire-and-forget forced refresh() in
  // flight against this same throwing mock. Swap in a benign mock so a stray
  // late render can only ever land as harmless, then drain it before the
  // next test starts.
  globalThis.fetch = async () => ({ ok: true, status: 200, json: async () => liveTrackState([]) });
  await new Promise(resolve => setImmediate(resolve));
  consolePanel.close();
});

test('a disabled chip on a read-only strip does not post', () => {
  consolePanel._resetForTest();
  consolePanel.init('ws-reaper');
  consolePanel._setActions([]);
  consolePanel._setScripts([]);
  const state = liveTrackState([{ index: 1, name: 'Drums' }]);
  state.track_editing_available = false;
  consolePanel._setState(state);
  consolePanel.open();

  let calls = 0;
  globalThis.fetch = async () => {
    calls += 1;
    return { ok: true, status: 200, json: async () => ({}) };
  };
  const host = documentStub.getElementById('reaperConsole');
  const chip = findOne(host, 'reaper-console-track-chip');
  assert.equal(chip.type, 'button');
  chip.dispatch('click');
  assert.equal(calls, 0);
  consolePanel.close();
});

test('strip controls carry the correct accessible roles and states', () => {
  consolePanel._resetForTest();
  consolePanel.init('ws-reaper');
  consolePanel._setActions([]);
  consolePanel._setScripts([]);
  consolePanel._setState(
    liveTrackState([{ index: 1, name: 'Kick', muted: true, soloed: false, armed: false, color: 0 }])
  );
  consolePanel.open();

  const host = documentStub.getElementById('reaperConsole');

  // Every strip control is a real <button> — keyboard-operable by default,
  // Enter/Space activate it, and it participates in normal tab order.
  for (const className of [
    'reaper-console-track-grip',
    'reaper-console-track-move',
    'reaper-console-track-swatch',
    'reaper-console-track-chip'
  ]) {
    for (const node of findAll(host, className)) {
      assert.equal(node.tagName, 'BUTTON', `${className} should be a <button>`);
    }
  }

  const muteChip = findAll(host, 'reaper-console-track-chip').find(chip =>
    chip.attributes.get('aria-label').startsWith('Mute')
  );
  assert.equal(muteChip.attributes.get('aria-pressed'), 'true');

  const swatch = findOne(host, 'reaper-console-track-swatch');
  assert.equal(swatch.attributes.get('aria-haspopup'), 'true');
  assert.equal(swatch.attributes.get('aria-expanded'), 'false');
  swatch.dispatch('click');
  assert.equal(
    findOne(
      documentStub.getElementById('reaperConsole'),
      'reaper-console-track-swatch'
    ).attributes.get('aria-expanded'),
    'true'
  );
  assert.equal(
    findOne(
      documentStub.getElementById('reaperConsole'),
      'reaper-console-color-popover'
    ).attributes.get('role'),
    'menu'
  );
  consolePanel.close();
});

test('the color swatch opens a palette with a fixed set plus No color', () => {
  consolePanel._resetForTest();
  consolePanel.init('ws-reaper');
  consolePanel._setActions([]);
  consolePanel._setScripts([]);
  consolePanel._setState(liveTrackState([{ index: 1, name: 'Drums', color: 0 }]));
  consolePanel.open();

  const host = documentStub.getElementById('reaperConsole');
  const swatch = findOne(host, 'reaper-console-track-swatch');
  assert.ok(swatch);
  assert.equal(consolePanel._openPalette(), 0);
  swatch.dispatch('click');
  assert.equal(consolePanel._openPalette(), 1);

  const popover = findOne(host, 'reaper-console-color-popover');
  assert.ok(popover, 'the palette should render once open');
  const options = findAll(host, 'reaper-console-color-option');
  // A fixed swatch set plus "No color" (PRD open question 1).
  assert.ok(options.length > 1);
  assert.match(host.textContent, /No color/);
  consolePanel.close();
});

test('picking a swatch applies the color and closes the palette', async () => {
  consolePanel._resetForTest();
  consolePanel.init('ws-reaper');
  consolePanel._setActions([]);
  consolePanel._setScripts([]);
  consolePanel._setState(liveTrackState([{ index: 1, name: 'Drums', color: 0 }]));
  consolePanel.open();
  const host = documentStub.getElementById('reaperConsole');
  findOne(host, 'reaper-console-track-swatch').dispatch('click');

  const requests = [];
  globalThis.fetch = async (path, options) => {
    requests.push({ path, options });
    return {
      ok: true,
      status: 200,
      json: async () => ({
        ...liveTrackState([{ index: 1, name: 'Drums', color: 33488896 }]),
        outcome: 'ok',
        undo: { summary: 'Recolored ‘Drums’' }
      })
    };
  };
  const swatchOption = findAll(
    documentStub.getElementById('reaperConsole'),
    'reaper-console-color-option'
  ).find(option => !option.className.includes('is-none'));
  swatchOption.dispatch('click');
  await new Promise(resolve => setImmediate(resolve));

  const colorCall = requests.find(request => /tracks\/1\/color$/.test(request.path));
  assert.ok(colorCall, 'picking a swatch should post to the color endpoint');
  assert.equal(consolePanel._openPalette(), 0, 'the palette should close after picking');
  assert.equal(consolePanel._toasts().length, 1);
  consolePanel.close();
});

test('clicking outside the palette closes it without applying anything', () => {
  consolePanel._resetForTest();
  consolePanel.init('ws-reaper');
  consolePanel._setActions([]);
  consolePanel._setScripts([]);
  consolePanel._setState(liveTrackState([{ index: 1, name: 'Drums', color: 0 }]));
  consolePanel.open();
  const host = documentStub.getElementById('reaperConsole');
  findOne(host, 'reaper-console-track-swatch').dispatch('click');
  assert.equal(consolePanel._openPalette(), 1);

  let calls = 0;
  globalThis.fetch = async () => {
    calls += 1;
    return { ok: true, status: 200, json: async () => ({}) };
  };
  const backdrop = findOne(
    documentStub.getElementById('reaperConsole'),
    'reaper-console-color-backdrop'
  );
  assert.ok(backdrop);
  backdrop.dispatch('click');
  assert.equal(consolePanel._openPalette(), 0);
  assert.equal(calls, 0);
  consolePanel.close();
});

test('Escape closes the color palette', () => {
  consolePanel._resetForTest();
  consolePanel.init('ws-reaper');
  consolePanel._setActions([]);
  consolePanel._setScripts([]);
  consolePanel._setState(liveTrackState([{ index: 1, name: 'Drums', color: 0 }]));
  consolePanel.open();
  const host = documentStub.getElementById('reaperConsole');
  findOne(host, 'reaper-console-track-swatch').dispatch('click');
  assert.equal(consolePanel._openPalette(), 1);

  const popover = findOne(
    documentStub.getElementById('reaperConsole'),
    'reaper-console-color-popover'
  );
  popover.dispatch('keydown', { key: 'Escape' });
  assert.equal(consolePanel._openPalette(), 0);
  consolePanel.close();
});

function threeTrackState() {
  return liveTrackState([
    { index: 1, name: 'Kick' },
    { index: 2, name: 'Bass' },
    { index: 3, name: 'Guitar' }
  ]);
}

test('dragging the grip posts a move to the target index and clears the drop indicator', async () => {
  consolePanel._resetForTest();
  consolePanel.init('ws-reaper');
  consolePanel._setActions([]);
  consolePanel._setScripts([]);
  consolePanel._setState(threeTrackState());
  consolePanel.open();

  const host = documentStub.getElementById('reaperConsole');
  const grips = findAll(host, 'reaper-console-track-grip');
  assert.equal(grips.length, 3);
  grips[0].dispatch('pointerdown', { preventDefault: () => {} });
  assert.deepEqual(consolePanel._dragState(), {
    sourceIndex: 1,
    sourceName: 'Kick',
    targetIndex: 1
  });
  assert.ok(
    findOne(documentStub.getElementById('reaperConsole'), 'reaper-console-track-drop-indicator')
  );

  consolePanel._dragOverIndex(3);
  assert.equal(consolePanel._dragState().targetIndex, 3);

  const requests = [];
  globalThis.fetch = async (path, options) => {
    requests.push({ path, options });
    return {
      ok: true,
      status: 200,
      json: async () => ({ ...threeTrackState(), outcome: 'ok', undo: { summary: 'Moved ‘Kick’' } })
    };
  };
  await consolePanel._endDrag();

  const move = requests.find(request => /tracks\/1\/move$/.test(request.path));
  assert.ok(move, 'ending the drag should post to the move endpoint');
  assert.deepEqual(JSON.parse(move.options.body), { new_index: 3, expected_name: 'Kick' });
  assert.equal(consolePanel._dragState(), null);
  consolePanel.close();
});

test('dropping back on the source position sends no request', async () => {
  consolePanel._resetForTest();
  consolePanel.init('ws-reaper');
  consolePanel._setActions([]);
  consolePanel._setScripts([]);
  consolePanel._setState(threeTrackState());
  consolePanel.open();

  consolePanel._beginDrag(2, 'Bass');
  let calls = 0;
  globalThis.fetch = async () => {
    calls += 1;
    return { ok: true, status: 200, json: async () => ({}) };
  };
  await consolePanel._endDrag();
  assert.equal(calls, 0);
  assert.equal(consolePanel._dragState(), null);
  consolePanel.close();
});

test('Escape cancels an in-progress drag without applying anything', () => {
  consolePanel._resetForTest();
  consolePanel.init('ws-reaper');
  consolePanel._setActions([]);
  consolePanel._setScripts([]);
  consolePanel._setState(threeTrackState());
  consolePanel.open();

  consolePanel._beginDrag(1, 'Kick');
  consolePanel._dragOverIndex(2);
  assert.ok(
    documentStub._hasListener('keydown'),
    'a drag should register its own keydown listener'
  );
  let calls = 0;
  globalThis.fetch = async () => {
    calls += 1;
    return { ok: true, status: 200, json: async () => ({}) };
  };
  documentStub.dispatchEvent({ type: 'keydown', key: 'Escape' });
  assert.equal(consolePanel._dragState(), null);
  assert.equal(calls, 0, 'cancelling a drag must not touch REAPER');
  consolePanel.close();
});

test('pointer movement resolves the drop target through elementFromPoint', () => {
  consolePanel._resetForTest();
  consolePanel.init('ws-reaper');
  consolePanel._setActions([]);
  consolePanel._setScripts([]);
  consolePanel._setState(threeTrackState());
  consolePanel.open();

  const host = documentStub.getElementById('reaperConsole');
  const grips = findAll(host, 'reaper-console-track-grip');
  grips[0].dispatch('pointerdown', { preventDefault: () => {} });
  assert.equal(consolePanel._dragState().targetIndex, 1);

  const stripUnderPointer = findAll(
    documentStub.getElementById('reaperConsole'),
    'reaper-console-track'
  )[2];
  documentStub._elementFromPoint = () => stripUnderPointer;
  documentStub.dispatchEvent({ type: 'pointermove', clientX: 10, clientY: 400 });
  assert.equal(consolePanel._dragState().targetIndex, 3);
  documentStub._elementFromPoint = null;

  consolePanel._cancelDrag();
  consolePanel.close();
});

test('move up and move down buttons are a keyboard-accessible equivalent to dragging', async () => {
  consolePanel._resetForTest();
  consolePanel.init('ws-reaper');
  consolePanel._setActions([]);
  consolePanel._setScripts([]);
  consolePanel._setState(threeTrackState());
  consolePanel.open();

  const host = documentStub.getElementById('reaperConsole');
  const downButtons = findAll(host, 'reaper-console-track-move').filter(node =>
    node.className.includes('is-down')
  );
  // Track 1 (Kick) can move down; the last track cannot.
  const requests = [];
  globalThis.fetch = async (path, options) => {
    requests.push({ path, options });
    return { ok: true, status: 200, json: async () => ({ ...threeTrackState(), outcome: 'ok' }) };
  };
  downButtons[0].dispatch('click');
  await new Promise(resolve => setImmediate(resolve));
  const move = requests.find(request => /tracks\/1\/move$/.test(request.path));
  assert.ok(move);
  assert.deepEqual(JSON.parse(move.options.body), { new_index: 2, expected_name: 'Kick' });

  const lastDown = downButtons[downButtons.length - 1];
  assert.equal(lastDown.disabled, true, 'the last track cannot move further down');
  consolePanel.close();
});

test('a state change mid-drag is recorded but does not rebuild the panel', async () => {
  consolePanel._resetForTest();
  consolePanel.init('ws-reaper');
  consolePanel._setActions([]);
  consolePanel._setScripts([]);
  consolePanel._setState(threeTrackState());
  consolePanel.open();

  consolePanel._beginDrag(1, 'Kick');
  const bodyBeforeChange = findOne(
    documentStub.getElementById('reaperConsole'),
    'reaper-console-body'
  );

  const changed = threeTrackState();
  changed.position = '5.1.00';
  globalThis.fetch = async () => ({ ok: true, status: 200, json: async () => changed });
  await consolePanel.refresh();

  const bodyAfterChange = findOne(
    documentStub.getElementById('reaperConsole'),
    'reaper-console-body'
  );
  assert.equal(bodyAfterChange, bodyBeforeChange, 'the panel must not rebuild while dragging');

  // Dropping resumes normal rendering.
  await consolePanel._endDrag();
  const bodyAfterDrop = findOne(
    documentStub.getElementById('reaperConsole'),
    'reaper-console-body'
  );
  assert.notEqual(bodyAfterDrop, bodyBeforeChange);
  consolePanel.close();
});

function samplePlan() {
  return {
    id: 'plan_1',
    edits: [
      { index: 1, expected_name: 'Track 1', operation: 'rename', new_name: 'Kick' },
      { index: 2, expected_name: 'Track 2', operation: 'rename', new_name: 'Snare' },
      { index: 3, expected_name: 'Bass', operation: 'color', new_color: 0x1ff0000 },
      { index: 4, expected_name: 'Guitar', operation: 'mute', new_bool: true },
      { index: 5, expected_name: 'Synth', operation: 'move', new_index: 1 }
    ]
  };
}

test('the plan card groups edits by operation and shows old to new', () => {
  consolePanel._resetForTest();
  consolePanel.init('ws-reaper');
  consolePanel._setActions([]);
  consolePanel._setScripts([]);
  consolePanel._setState(threeTrackState());
  consolePanel._setPendingPlan(samplePlan());
  consolePanel.open();

  const host = documentStub.getElementById('reaperConsole');
  assert.match(host.textContent, /Proposed changes/);
  assert.match(host.textContent, /Rename 2 tracks/);
  assert.match(host.textContent, /Track 1.*Kick/);
  assert.match(host.textContent, /Track 2.*Snare/);
  assert.match(host.textContent, /Color 1 track/);
  assert.match(host.textContent, /Mute 1 track/);
  assert.match(host.textContent, /Move 1 track/);
  assert.match(host.textContent, /Synth.*position 1/);
  consolePanel.close();
});

test('Apply posts confirmed:true and fires one toast with a global-undo Undo', async () => {
  consolePanel._resetForTest();
  consolePanel.init('ws-reaper');
  consolePanel._setActions([]);
  consolePanel._setScripts([]);
  consolePanel._setState(threeTrackState());
  consolePanel._setPendingPlan(samplePlan());
  consolePanel.open();

  const requests = [];
  globalThis.fetch = async (path, options) => {
    requests.push({ path, options });
    return {
      ok: true,
      status: 200,
      json: async () => ({
        ...threeTrackState(),
        outcome: 'ok',
        applied_count: 5,
        undo: { summary: 'Applied 5 changes', command_id: '40029' }
      })
    };
  };
  await consolePanel._applyPlan();

  const apply = requests.find(request => /track-plan\/apply$/.test(request.path));
  assert.ok(apply);
  assert.deepEqual(JSON.parse(apply.options.body), { confirmed: true });
  assert.equal(consolePanel._pendingPlan(), null, 'the plan clears once applied');
  assert.equal(consolePanel._toasts().length, 1);
  assert.match(consolePanel._toasts()[0].message, /Applied 5 changes/);

  // The plan's Undo fires REAPER's global undo, not a specific inverse.
  const undoRequests = [];
  globalThis.fetch = async (path, options) => {
    undoRequests.push({ path, options });
    return {
      ok: true,
      status: 200,
      json: async () => ({ outcome: 'ok', applies: true, connected: true, tracks: [] })
    };
  };
  await consolePanel._undoFromToast(consolePanel._toasts()[0].id);
  assert.match(undoRequests[0].path, /actions\/40029\/run$/);
  consolePanel.close();
});

test('Cancel discards the plan with no REAPER contact', async () => {
  consolePanel._resetForTest();
  consolePanel.init('ws-reaper');
  consolePanel._setActions([]);
  consolePanel._setScripts([]);
  consolePanel._setState(threeTrackState());
  consolePanel._setPendingPlan(samplePlan());
  consolePanel.open();

  const requests = [];
  globalThis.fetch = async (path, options) => {
    requests.push({ path, options });
    return { ok: true, status: 200, json: async () => ({}) };
  };
  await consolePanel._cancelPlan();

  assert.equal(requests.length, 1);
  assert.equal(requests[0].options.method, 'DELETE');
  assert.equal(consolePanel._pendingPlan(), null);
  assert.equal(consolePanel._toasts().length, 0, 'cancelling must not toast');
  consolePanel.close();
});

test('a guard failure on apply keeps the plan pending and shows the notice', async () => {
  consolePanel._resetForTest();
  consolePanel.init('ws-reaper');
  consolePanel._setActions([]);
  consolePanel._setScripts([]);
  consolePanel._setState(threeTrackState());
  consolePanel._setPendingPlan(samplePlan());
  consolePanel.open();

  globalThis.fetch = async (path, options) => {
    // Only the apply call fails the guard; the forced state re-read after it
    // still succeeds, matching what refresh() would see from a live server.
    if (options && options.method === 'POST') {
      return {
        ok: false,
        status: 409,
        json: async () => ({
          ...threeTrackState(),
          outcome: 'error',
          code: 'plan_guard_failed',
          error_reason: 'The track list changed — nothing was applied.',
          failed_indices: [2]
        })
      };
    }
    return { ok: true, status: 200, json: async () => threeTrackState() };
  };
  assert.equal(await consolePanel._applyPlan(), false);
  assert.equal(consolePanel._toasts().length, 0);
  const host = documentStub.getElementById('reaperConsole');
  assert.match(host.textContent, /nothing was applied/);
  consolePanel.close();
});

test('a network failure during apply reverts and says plainly nothing was applied', async () => {
  consolePanel._resetForTest();
  consolePanel.init('ws-reaper');
  consolePanel._setActions([]);
  consolePanel._setScripts([]);
  consolePanel._setState(threeTrackState());
  consolePanel._setPendingPlan(samplePlan());
  consolePanel.open();

  globalThis.fetch = async () => {
    throw new Error('network unreachable');
  };
  assert.equal(await consolePanel._applyPlan(), false);
  assert.equal(consolePanel._toasts().length, 0);
  // The plan stays pending — a disconnect is not the same as a refusal, but
  // either way nothing was applied and the console must say so plainly. Read
  // the notice state directly: applyPlan's own forced refresh() races in the
  // background against this same throwing mock, so which panel is on screen
  // by the time this assertion runs is nondeterministic — the underlying
  // notice text is not.
  assert.ok(consolePanel._pendingPlan(), 'the plan should still be there to retry');
  assert.match(consolePanel._stripNotice().text, /Nothing was applied/);

  // applyPlan's own fire-and-forget forced refresh() is still in flight
  // against this same throwing mock. Swap in a benign mock so a stray late
  // render can only ever land as harmless, then drain it.
  globalThis.fetch = async () => ({ ok: true, status: 200, json: async () => threeTrackState() });
  await new Promise(resolve => setImmediate(resolve));
  consolePanel.close();
});

test('the poll drops to one second while the console is open', () => {
  consolePanel._resetForTest();
  consolePanel.init('ws-reaper');
  documentStub.visibilityState = 'visible';
  consolePanel.setMapVisible(true);
  assert.equal(consolePanel._pollIntervalMs(), 5000);

  consolePanel._setState(liveTrackState([]));
  consolePanel.open();
  assert.equal(consolePanel._pollIntervalMs(), 1000);
  assert.equal(timers.at(-1).delay, 1000);

  consolePanel.close();
  assert.equal(consolePanel._pollIntervalMs(), 5000);
  assert.equal(timers.at(-1).delay, 5000);

  // Still visibility-aware at the faster cadence.
  consolePanel.open();
  documentStub.visibilityState = 'hidden';
  documentStub._dispatch('visibilitychange');
  assert.equal(consolePanel._polling(), false);
  documentStub.visibilityState = 'visible';
  consolePanel.close();
});

test('a re-render keeps the console body scrolled where the user left it', () => {
  consolePanel._resetForTest();
  consolePanel.init('ws-reaper');
  consolePanel._setActions([]);
  consolePanel._setScripts([]);
  consolePanel._setState(liveTrackState([{ index: 1, name: 'Drums' }]));
  consolePanel.open();

  const host = documentStub.getElementById('reaperConsole');
  const body = findOne(host, 'reaper-console-body');
  assert.ok(body);
  // The user scrolls down to the Tracks section.
  body.scrollTop = 420;

  // The transport position ticks, forcing a re-render at the 1s cadence.
  const moved = liveTrackState([{ index: 1, name: 'Drums' }]);
  moved.position = '2.1.00';
  consolePanel._setState(moved);

  const rebuilt = findOne(documentStub.getElementById('reaperConsole'), 'reaper-console-body');
  assert.notEqual(rebuilt, body, 'the panel should have been rebuilt');
  assert.equal(rebuilt.scrollTop, 420, 'the scroll offset should survive the re-render');
  consolePanel.close();
});

test('an unchanged poll tick does not rebuild the console', async () => {
  consolePanel._resetForTest();
  consolePanel.init('ws-reaper');
  consolePanel._setActions([]);
  consolePanel._setScripts([]);
  const state = liveTrackState([{ index: 1, name: 'Drums' }]);
  consolePanel._setState(state);
  consolePanel.open();

  const before = findOne(documentStub.getElementById('reaperConsole'), 'reaper-console-body');
  globalThis.fetch = async () => ({ ok: true, status: 200, json: async () => state });
  await consolePanel.refresh();
  const after = findOne(documentStub.getElementById('reaperConsole'), 'reaper-console-body');
  assert.equal(after, before, 'an identical state must not rebuild the panel');
  consolePanel.close();
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
