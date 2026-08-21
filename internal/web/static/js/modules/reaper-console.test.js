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
  dispatch(type, event) {
    const listener = this.listeners.get(type);
    if (listener) listener(event || { type });
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
