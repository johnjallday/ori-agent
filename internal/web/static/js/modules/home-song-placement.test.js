import test from 'node:test';
import assert from 'node:assert/strict';

import {
  createHomeSongPlacement,
  homeSongPlacementAvailable,
  homeSongPlacementCopy,
  openedHereInHome,
  PLACEMENT_MIN_WIDTH,
  POSITION_NOT_SAVED
} from './home-song-placement.js';

const view = { offerID: 'offer-5', song: 'Album-5', manager: 'Music Portfolio Manager' };
const shown = { getClientRects: () => [{}] };
const hidden = { getClientRects: () => [] };

function fakeMap({ scope = 'home-1', status = 'ready', readOnly = false, saved = true } = {}) {
  const calls = [];
  let spec = null;
  return {
    calls,
    spec: () => spec,
    getScopeGroupId: () => scope,
    getLayoutState: () => ({ status, readOnly }),
    beginPlacement(next) {
      spec = next;
      calls.push(['begin', next.token]);
      return true;
    },
    endPlacement(token) {
      calls.push(['end', token]);
      return true;
    },
    async commitPlacement(id, options) {
      calls.push(['commit', id, { ...options.candidate }, options.select]);
      return { saved };
    }
  };
}

function fakePanel() {
  const calls = [];
  return {
    calls,
    close: options => calls.push(['close', options]),
    open: (_trigger, options) => calls.push(['open', options])
  };
}

test('placement is offered only on the Home’s own drawn map, ready, writable and wide', () => {
  const map = fakeMap();
  const ok = { homeID: 'home-1', map, innerWidth: PLACEMENT_MIN_WIDTH, viewport: shown };
  assert.equal(PLACEMENT_MIN_WIDTH, 980);
  assert.equal(homeSongPlacementAvailable(ok), true);
  // Tree mode (the map is not drawn), narrow, not ready, read-only, another scope: automatic.
  for (const change of [
    { viewport: hidden },
    { viewport: null },
    { innerWidth: PLACEMENT_MIN_WIDTH - 1 },
    { map: fakeMap({ status: 'loading' }) },
    { map: fakeMap({ readOnly: true }) },
    { map: fakeMap({ scope: 'other-home' }) },
    { map: fakeMap({ scope: null }) },
    { map: null },
    { homeID: '' }
  ])
    assert.equal(homeSongPlacementAvailable({ ...ok, ...change }), false, JSON.stringify(change));
});

test('the banner copy names the song and the Home and offers automatic placement', () => {
  const copy = homeSongPlacementCopy(view, 'Music Production Home');
  assert.equal(
    copy.instruction,
    'Open Album-5 here with a click on open ground inside Music Production Home, or press Enter. Escape returns to the conversation.'
  );
  assert.equal(copy.confirmLabel, 'Open Album-5 here');
  assert.equal(copy.pendingLabel, 'Music Portfolio Manager is opening Album-5…');
  assert.equal(copy.backLabel, 'Back to the conversation');
  assert.equal(copy.secondaryLabel, 'Use automatic placement');
});

test('only a new child of this Home is positioned', () => {
  const outcome = {
    workspace_id: 'song-1',
    receipt: [{ kind: 'workspace', name: 'Album-5', operation: 'create' }]
  };
  const resource = { id: 'song-1', parent_id: 'home-1' };
  assert.equal(openedHereInHome(outcome, resource, 'home-1'), true);
  assert.equal(openedHereInHome({ workspace_id: 'song-1' }, resource, 'home-1'), true);
  assert.equal(openedHereInHome({ ...outcome, existing: true }, resource, 'home-1'), false);
  assert.equal(openedHereInHome(outcome, { ...resource, parent_id: 'elsewhere' }, 'home-1'), false);
  assert.equal(openedHereInHome(outcome, { ...resource, id: 'other' }, 'home-1'), false);
  assert.equal(
    openedHereInHome(
      { ...outcome, receipt: [{ kind: 'workspace', operation: 'reuse' }] },
      resource,
      'home-1'
    ),
    false
  );
  assert.equal(openedHereInHome({}, resource, 'home-1'), false);
});

function strategy({ map = fakeMap(), panel = fakePanel(), resource } = {}) {
  const reads = [];
  const focused = [];
  const placement = createHomeSongPlacement({
    map: () => map,
    panel,
    home: () => ({ id: 'home-1', name: 'Music Production Home' }),
    available: () => true,
    focusCard: next => focused.push(next.offerID),
    fetchImpl: async url => {
      reads.push(url);
      return {
        ok: true,
        json: async () => resource || { id: 'song-1', parent_id: 'home-1' }
      };
    }
  });
  return { map, panel, placement, reads, focused };
}

test('a press closes the drawer and enters placement; nothing runs before the click', async () => {
  const { map, panel, placement } = strategy();
  const runs = [];
  assert.equal(placement.wants(view), true);
  await placement.begin(view, { run: async options => runs.push(options) });
  assert.deepEqual(panel.calls[0], ['close', { restoreFocus: false }]);
  const spec = map.spec();
  assert.equal(spec.mode, 'reviewed-setup');
  assert.deepEqual(spec.preview, {
    name: 'Album-5',
    kind: 'workspace',
    parentID: 'home-1',
    withinParent: true
  });
  assert.equal(spec.actionCopy.secondaryLabel, 'Use automatic placement');
  assert.equal(runs.length, 0, 'no press before the click or Enter');
});

test('Escape returns to the conversation with nothing run', async () => {
  const { map, panel, placement, focused } = strategy();
  const runs = [];
  await placement.begin(view, { run: async options => runs.push(options) });
  map.spec().onBack('token', 'back');
  assert.equal(runs.length, 0);
  assert.deepEqual(panel.calls.at(-1), ['open', { focus: false, preserveConversation: true }]);
  assert.deepEqual(focused, ['offer-5']);
});

test('Use automatic placement runs the open at once, with no position to save', async () => {
  const { map, panel, placement } = strategy();
  const runs = [];
  await placement.begin(view, { run: async options => (runs.push(options), true) });
  map.spec().onSecondary('token');
  await new Promise(resolve => setImmediate(resolve));
  assert.deepEqual(runs, [{}]);
  assert.equal(panel.calls.at(-1)[0], 'open');
  assert.ok(
    map.calls.some(call => call[0] === 'end'),
    'the session is ended'
  );
});

test('confirming runs the press once, reopens the drawer, then saves the point once after verification', async () => {
  const { map, panel, placement, reads } = strategy();
  let afterVerified = null;
  let release;
  const running = new Promise(resolve => (release = resolve));
  let runs = 0;
  await placement.begin(view, {
    run: async options => {
      runs += 1;
      afterVerified = options.afterVerified;
      await running;
      return true;
    }
  });
  assert.equal(map.spec().onConfirm('token', { x: 900, y: 1000 }), true);
  // A second confirmation (double click) never runs a second press.
  map.spec().onConfirm('token', { x: 900, y: 1000 });
  assert.equal(runs, 1);
  // The drawer is back on the running card while the run proceeds (FR13).
  assert.deepEqual(panel.calls.at(-1), ['open', { focus: false, preserveConversation: true }]);
  const note = await afterVerified({
    workspace_id: 'song-1',
    receipt: [{ kind: 'workspace', operation: 'create' }]
  });
  assert.equal(note, '');
  assert.deepEqual(reads, ['/api/workspaces/song-1']);
  const commit = map.calls.find(call => call[0] === 'commit');
  assert.deepEqual(commit, ['commit', 'song-1', { x: 900, y: 1000 }, true]);
  release();
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(map.calls.filter(call => call[0] === 'commit').length, 1);
});

test('a failed position save is still a success with the Move note; an unproven child is not positioned', async () => {
  const failing = strategy({ map: fakeMap({ saved: false }) });
  let afterVerified;
  await failing.placement.begin(view, {
    run: async options => ((afterVerified = options.afterVerified), true)
  });
  failing.map.spec().onConfirm('t', { x: 1, y: 2 });
  assert.equal(await afterVerified({ workspace_id: 'song-1' }), POSITION_NOT_SAVED);
  assert.equal(POSITION_NOT_SAVED, 'Its position was not saved; use Move.');

  const elsewhere = strategy({ resource: { id: 'song-1', parent_id: 'another-home' } });
  await elsewhere.placement.begin(view, {
    run: async options => ((afterVerified = options.afterVerified), true)
  });
  elsewhere.map.spec().onConfirm('t', { x: 1, y: 2 });
  assert.equal(await afterVerified({ workspace_id: 'song-1' }), POSITION_NOT_SAVED);
  assert.equal(
    elsewhere.map.calls.some(call => call[0] === 'commit'),
    false,
    'a point never moves a workspace this run did not make here (FR16)'
  );
});

test('a map that refuses the session takes the automatic path', async () => {
  const map = fakeMap();
  map.beginPlacement = () => false;
  const { placement } = strategy({ map });
  const runs = [];
  await placement.begin(view, { run: async options => (runs.push(options), true) });
  assert.deepEqual(runs, [{}]);
});
