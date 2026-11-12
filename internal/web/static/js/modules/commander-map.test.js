import test from 'node:test';
import assert from 'node:assert/strict';

import { folderAgentReportView } from './folder-agent-card.js';
import { commanderBalloon, resetCommanderBalloons } from './commander-balloon.js';
import { fakeDocument } from './commander-balloon.test.js';
import { speakingFolderAgents } from './home-workspace-cockpit.js';
import {
  commanderPresenter,
  homeCommanders,
  homeMapShowing,
  homeTileAnchor,
  MAP_MOUNTED_EVENT,
  revealHiredTile,
  tileIntoAreaDelta,
  visibleMapArea,
  clearScroll,
  lotAnchor,
  workspaceCommanderAnchor,
  workspaceMapRoot
} from './commander-map.js';

const raw = (overrides = {}) => ({
  workspace_id: 'ws-1',
  workspace_name: 'Albums',
  folder_name: 'Albums',
  agent_name: 'Albums Manager',
  state: 'reported',
  actions: ['set_up', 'keep', 'look_further'],
  ...overrides
});

// A map adapter double: the balloon records what it was told.
function fakeMap({ visible = true, narrow = false } = {}) {
  const calls = [];
  let changed = null;
  const balloon = {
    content: null,
    expanded: false,
    update(content) {
      calls.push(['update', content && content.line]);
      this.content = content;
      if (!content) this.expanded = false;
    },
    expand(opts = {}) {
      calls.push(['expand', opts.focus !== false]);
      this.expanded = true;
      return true;
    },
    collapse() {
      calls.push(['collapse']);
      this.expanded = false;
    }
  };
  return {
    calls,
    balloon,
    state: { visible, narrow },
    visible() {
      return this.state.visible;
    },
    narrow() {
      return this.state.narrow;
    },
    attach() {
      return this.state.visible;
    },
    reveal() {
      calls.push(['reveal']);
    },
    onChange(fn) {
      changed = fn;
      return () => (changed = null);
    },
    fire() {
      changed?.();
    }
  };
}

function fakeHost() {
  return {
    shown: null,
    replaceChildren(node) {
      this.shown = node;
    }
  };
}

const doc = {
  visibilityState: 'visible',
  createElement(tag) {
    const node = {
      tagName: tag.toUpperCase(),
      className: '',
      textContent: '',
      dataset: {},
      children: [],
      listeners: {},
      append(...kids) {
        node.children.push(...kids);
      },
      addEventListener(type, fn) {
        node.listeners[type] = fn;
      }
    };
    return node;
  }
};

function args(map, host, { view, previous = null, followed = false } = {}) {
  const v = view || folderAgentReportView(raw());
  return {
    view: v,
    raw: raw({ state: v.state }),
    previous,
    followed,
    error: '',
    opening: false,
    host,
    doc,
    card: compact => ({ kind: compact ? 'compact' : 'full' })
  };
}

test('on the Map tab the header card is one line and the balloon carries the report', () => {
  const map = fakeMap();
  const host = fakeHost();
  const presenter = commanderPresenter(map);
  assert.equal(presenter.present(args(map, host)), true);
  assert.equal(host.shown.children[0].textContent, 'Albums Manager has a plan for Albums');
  assert.equal(host.shown.children[1].textContent, 'Review');
  assert.equal(map.balloon.content.line, 'I have a plan for Albums · Review');
  assert.deepEqual(map.balloon.content.speaker, { name: 'Albums Manager', role: 'Commander' });
  assert.equal(map.balloon.content.body().kind, 'compact');
  // A report already waiting when the page opened: no expanding by itself.
  assert.equal(map.balloon.expanded, false);
  // Review expands the balloon and brings the map into view.
  host.shown.children[1].listeners.click();
  assert.deepEqual(map.calls.slice(-2), [['reveal'], ['expand', true]]);
});

// The chat box's answer to "do you hold the plan?" is asked on every draw.
const withChat = (map, host, held, options) => ({
  ...args(map, host, options),
  chatHolds: () => held.value
});

test('while the chat box holds the plan its host shows the full card and the balloon keeps only its line (visual-hire FR22, FR24)', () => {
  const map = fakeMap();
  const host = fakeHost();
  const held = { value: false };
  const presenter = commanderPresenter(map);
  const looking = folderAgentReportView(raw({ state: 'looking' }));
  const reported = folderAgentReportView(raw());

  // During the look the chat keeps its one line (FR16), as before.
  presenter.present(withChat(map, host, held, { view: looking, followed: true }));
  assert.equal(host.shown.className.includes('folder-agent-card--line'), true);

  // Starting the look opens the balloon by itself (the owner just started it).
  assert.equal(map.balloon.expanded, true);

  // The look ends with a plan and the chat box is open: it takes the whole card,
  // the balloon folds to its line with no plan in it and does not open again.
  held.value = true;
  const before = map.calls.length;
  presenter.present(
    withChat(map, host, held, { view: reported, previous: looking, followed: true })
  );
  assert.equal(host.shown.kind, 'full');
  assert.equal(map.balloon.expanded, false);
  assert.deepEqual(
    map.calls.slice(before).filter(([name]) => name === 'expand'),
    []
  );
  assert.equal(
    map.calls.slice(before).some(([name]) => name === 'collapse'),
    true
  );
  assert.equal(map.balloon.content.body, null);
  assert.equal(map.balloon.content.line, 'I have a plan for Albums · Review');
  // The arrival is announced once, by the balloon's own line.
  assert.ok(map.balloon.content.announce);
  assert.match(map.balloon.content.signature, /\|true$/);
});

test('closing the chat box gives the plan back to the balloon, and opening it takes it again (visual-hire FR23, FR24)', () => {
  const map = fakeMap();
  const host = fakeHost();
  const held = { value: true };
  const presenter = commanderPresenter(map);
  const looking = folderAgentReportView(raw({ state: 'looking' }));
  const reported = folderAgentReportView(raw());
  presenter.present(
    withChat(map, host, held, { view: reported, previous: looking, followed: true })
  );
  assert.equal(host.shown.kind, 'full');
  assert.equal(map.balloon.content.body, null);

  // The person closes the box: the chat's host has nothing to hold, the balloon
  // carries the plan again (its body returns) as it did before this feature.
  held.value = false;
  map.fire();
  assert.equal(typeof map.balloon.content.body, 'function');
  assert.equal(map.balloon.content.body().kind, 'compact');
  assert.equal(host.shown.className.includes('folder-agent-card--line'), true);

  // They open it again: the plan goes back into the chat and out of the balloon.
  held.value = true;
  map.fire();
  assert.equal(host.shown.kind, 'full');
  assert.equal(map.balloon.content.body, null);
});

test('a look that failed is held by the chat box the same way (visual-hire FR22)', () => {
  const map = fakeMap();
  const host = fakeHost();
  const held = { value: true };
  const presenter = commanderPresenter(map);
  const looking = folderAgentReportView(raw({ state: 'looking' }));
  const failed = folderAgentReportView(
    raw({ state: 'failed', failure: 'The agent could not finish looking around.' })
  );
  presenter.present(withChat(map, host, held, { view: failed, previous: looking, followed: true }));
  assert.equal(host.shown.kind, 'full');
  assert.equal(map.balloon.expanded, false);
  assert.equal(map.balloon.content.body, null);
});

test('a closed chat box leaves the plan to the balloon, which opens by itself as before (visual-hire FR23)', () => {
  const map = fakeMap();
  const host = fakeHost();
  const held = { value: false };
  const presenter = commanderPresenter(map);
  const looking = folderAgentReportView(raw({ state: 'looking' }));
  const reported = folderAgentReportView(raw());
  presenter.present(
    withChat(map, host, held, { view: reported, previous: looking, followed: true })
  );
  assert.equal(host.shown.className.includes('folder-agent-card--line'), true);
  assert.equal(map.balloon.content.body().kind, 'compact');
  assert.deepEqual(map.calls.at(-1), ['expand', false]);
  assert.equal(map.balloon.expanded, true);

  // The workspace page passes no chat at all and is unchanged.
  const page = fakeMap();
  const pageHost = fakeHost();
  commanderPresenter(page).present(
    args(page, pageHost, { view: reported, previous: looking, followed: true })
  );
  assert.equal(pageHost.shown.className.includes('folder-agent-card--line'), true);
  assert.equal(page.balloon.content.body().kind, 'compact');
  assert.equal(page.balloon.expanded, true);
});

// The Commander's balloon carries the songs question, then the reading, in the
// host's words (learn-the-songs FR2, FR14), on the folder's own map.
test('the balloon asks to learn the songs, then follows the reading', () => {
  const finishedSetup = { status: 'done', lines: [] };
  const songsOffer = {
    count: 7,
    noun: 'songs',
    ask: 'Want me to learn the 7 songs?',
    reads:
      "I'll read tempo, length, track count, track names, markers and regions from each project file. Nothing is changed, and audio is not opened.",
    not_read: ''
  };
  const present = overrides => {
    const map = fakeMap();
    const host = fakeHost();
    const view = folderAgentReportView(
      raw({ state: 'set_up', actions: ['look_further'], setup: finishedSetup, ...overrides })
    );
    commanderPresenter(map).present(args(map, host, { view }));
    return { map, host };
  };

  const asking = present({ songs_offer: songsOffer });
  assert.equal(asking.map.balloon.content.line, 'Want me to learn the 7 songs? · Review');
  assert.equal(asking.host.shown.children[0].textContent, 'Albums Manager asks to learn the songs');
  assert.equal(asking.map.balloon.content.body().kind, 'compact');

  const reading = present({
    learning: { status: 'running', line: 'Learning Night Drive… (2 of 7)', done: 1, total: 7 }
  });
  assert.equal(reading.map.balloon.content.line, 'Learning Night Drive… (2 of 7)');
  assert.equal(reading.host.shown.children[0].textContent, 'Learning Night Drive… (2 of 7)');
});

test('other tabs and narrow screens keep the full card (FR14, FR17)', () => {
  const map = fakeMap({ visible: false });
  const host = fakeHost();
  const presenter = commanderPresenter(map);
  presenter.present(args(map, host));
  assert.equal(host.shown.kind, 'full');
  const narrow = fakeMap({ narrow: true });
  const narrowHost = fakeHost();
  commanderPresenter(narrow).present(args(narrow, narrowHost));
  assert.equal(narrowHost.shown.kind, 'full');
});

test('switching to another tab collapses the balloon and restores the card', () => {
  const map = fakeMap();
  const host = fakeHost();
  const presenter = commanderPresenter(map);
  presenter.present(args(map, host));
  map.balloon.expand();
  map.state.visible = false;
  map.fire();
  assert.equal(map.balloon.expanded, false);
  assert.equal(host.shown.kind, 'full');
  map.state.visible = true;
  map.fire();
  assert.equal(host.shown.className.includes('folder-agent-card--line'), true);
});

test('a report arriving while the owner is on the map opens the balloon without taking focus', () => {
  const map = fakeMap();
  const host = fakeHost();
  const presenter = commanderPresenter(map);
  const looking = folderAgentReportView(raw({ state: 'looking' }));
  presenter.present(args(map, host, { view: looking }));
  assert.equal(map.balloon.expanded, false);
  presenter.present(args(map, host, { previous: looking }));
  assert.equal(map.balloon.expanded, true);
  assert.deepEqual(
    map.calls.find(c => c[0] === 'expand'),
    ['expand', false]
  );
});

test('a kept folder or a missing report takes the balloon away (FR30)', () => {
  const map = fakeMap();
  const host = fakeHost();
  const presenter = commanderPresenter(map);
  presenter.present(args(map, host));
  presenter.present(args(map, host, { view: folderAgentReportView(raw({ state: 'kept' })) }));
  assert.equal(map.balloon.content, null);
  assert.equal(host.shown.kind, 'full');
  presenter.present(args(map, host));
  presenter.present(null);
  assert.equal(map.balloon.content, null);
});

test('the visible map area leaves out the drawer, open or closed (Home)', () => {
  const map = { left: 0, top: 60, width: 1200, height: 700 };
  assert.deepEqual(visibleMapArea(map, []), { left: 0, top: 60, width: 1200, height: 700 });
  // Drawer open on the right, covering 800–1200: the right side is cut (8 px gap).
  assert.deepEqual(visibleMapArea(map, [{ left: 800, top: 0, width: 400, height: 900 }]), {
    left: 0,
    top: 60,
    width: 792,
    height: 700
  });
  // A drawer on the left cuts the left side.
  assert.deepEqual(visibleMapArea(map, [{ left: -10, top: 0, width: 310, height: 900 }]), {
    left: 308,
    top: 60,
    width: 892,
    height: 700
  });
  // A drawer beside the map, not over it, changes nothing.
  assert.equal(visibleMapArea(map, [{ left: 1200, top: 0, width: 400, height: 900 }]).width, 1200);
  assert.equal(visibleMapArea(null, []), null);
});

test("the visible area keeps clear of the assistant's launcher and stays above the fold", () => {
  const map = { left: 30, top: 130, width: 1220, height: 800 };
  // The launcher in the bottom-right corner (1018–1258 × 806–880): cutting
  // the bottom keeps more room than cutting the right side.
  const launcher = { left: 1018, top: 806, width: 240, height: 74 };
  const area = visibleMapArea(map, [launcher], { left: 0, top: 0, width: 1280, height: 900 });
  assert.equal(area.top, 130);
  assert.equal(area.top + area.height, 798, 'ends above the launcher');
  assert.equal(area.left + area.width, 1250);
  // A window shorter than the map: the area ends at the fold.
  const short = visibleMapArea(map, [], { left: 0, top: 0, width: 1024, height: 800 });
  assert.equal(short.top + short.height, 800);
  assert.equal(short.left + short.width, 1024);
});

test('bringing the card or the map into view clears the sticky navbar', () => {
  // 390 px: a 169 px navbar; the card's top at 96 sits under it.
  assert.equal(clearScroll({ top: 96, height: 1100 }, 169, 844, { top: true }), 96 - 181);
  // A card far down the page lands with its top just below the navbar.
  assert.equal(clearScroll({ top: 2100, height: 1100 }, 169, 844, { top: true }), 2100 - 181);
  // A map that fits and is already in view does not move.
  assert.equal(clearScroll({ top: 300, height: 400 }, 117, 900), 0);
  // One that ends below the fold rises until its bottom is in view.
  assert.equal(clearScroll({ top: 400, height: 400 }, 117, 700), 400 + 400 - 688);
  // One taller than the room left lines its top up under the navbar.
  assert.equal(clearScroll({ top: 400, height: 900 }, 117, 700), 400 - 129);
  assert.equal(clearScroll(null, 0, 0), 0);
});

test("Home's anchor is the district header once the folder is a district, else its tile", () => {
  const header = { id: 'header' };
  const tile = { id: 'tile' };
  const root = found => ({
    querySelector: selector => found.find(([s]) => s === selector)?.[1] || null
  });
  assert.equal(
    homeTileAnchor(
      root([
        ['.ws-map-district[data-group-id="ws-1"] .ws-map-district-header', header],
        ['.ws-map-tile[data-ws-id="ws-1"]', tile]
      ]),
      'ws-1'
    ),
    header
  );
  assert.equal(homeTileAnchor(root([['.ws-map-tile[data-ws-id="ws-1"]', tile]]), 'ws-1'), tile);
  assert.equal(homeTileAnchor(root([]), 'ws-1'), null);
  assert.equal(homeTileAnchor(root([]), 'bad"id'), null, 'an unsafe id never reaches a selector');
});

test('a folder district inside its Home speaks from its own header, not the Home’s (nest-songs FR13)', async () => {
  // The real map markup: the Home's district, then Albums drawn inside it.
  const { readFileSync } = await import('node:fs');
  const vm = await import('node:vm');
  const window = {};
  vm.runInNewContext(readFileSync(new URL('./workspace-map.js', import.meta.url), 'utf8'), {
    window,
    document: {}
  });
  const layout = window.OriWorkspaceMap.computeWorldLayout(
    [
      { id: 'home', kind: 'group', name: 'Music Production Home' },
      {
        id: 'albums',
        kind: 'group',
        parent_id: 'home',
        name: 'Albums',
        entry_agent_name: 'Albums Manager'
      },
      { id: 'album-1', parent_id: 'albums', name: 'Album-1' }
    ],
    {}
  );
  const albums = layout.districts.find(d => d.id === 'albums');
  assert.equal(albums.parentId, 'home', 'Albums is a district inside the Home');
  const header = { id: 'albums-header' };
  const homeHeader = { id: 'home-header' };
  const root = {
    querySelector: selector =>
      ({
        '.ws-map-district[data-group-id="albums"] .ws-map-district-header': header,
        '.ws-map-district[data-group-id="home"] .ws-map-district-header': homeHeader
      })[selector] || null
  };
  assert.equal(homeTileAnchor(root, 'albums'), header);
});

test('an opening the rules call for waits until the tile is drawn', () => {
  const map = fakeMap({ visible: false });
  const host = fakeHost();
  const presenter = commanderPresenter(map, { action: 'Show on map' });
  const looking = folderAgentReportView(raw({ state: 'looking' }));
  // The owner pressed Hire & start: followed, but the tile is not drawn yet.
  presenter.present(args(map, host, { view: looking, followed: true }));
  assert.equal(map.balloon.expanded, false);
  assert.equal(host.shown.kind, 'full');
  map.state.visible = true;
  map.fire();
  assert.equal(map.balloon.expanded, true, 'opened once the tile arrived');
  assert.equal(host.shown.children[1].textContent, 'Show on map');
});

test("Home's other Commanders speak from their tiles; the drawer's own is left to the drawer", async () => {
  resetCommanderBalloons();
  const doc = fakeDocument();
  const posted = [];
  const commanders = homeCommanders({
    doc,
    win: {},
    reportView: folderAgentReportView,
    renderCard: (view, options) => {
      const card = doc.createElement('article');
      const button = doc.createElement('button');
      button.addEventListener('click', () => options.onAction('set_up', button, { selected: [] }));
      card.append(button);
      return card;
    },
    act: async (view, action) => {
      posted.push([view.workspaceID, action]);
      return {
        view: raw({
          workspace_id: view.workspaceID,
          state: 'setting_up',
          setup: { status: 'running', lines: [] }
        })
      };
    }
  });
  const views = speakingFolderAgents([
    raw({ workspace_id: 'ws-a', workspace_name: 'Albums' }),
    raw({
      workspace_id: 'ws-b',
      workspace_name: 'Singles',
      folder_name: 'Singles',
      state: 'looking'
    }),
    raw({ workspace_id: 'ws-c', state: 'set_up', setup: { status: 'done', lines: [] } }),
    raw({ workspace_id: 'ws-d', state: 'kept' })
  ]);
  assert.deepEqual(
    views.map(v => v.workspace_id),
    ['ws-a', 'ws-b'],
    'old receipts and kept folders stay quiet'
  );
  commanders.sync(views, 'ws-b');
  assert.deepEqual(commanders.ids(), ['ws-a']);
  const a = commanderBalloon('home:ws-a');
  assert.equal(a.content.line, 'I have a plan for Albums · Review');
  assert.equal(a.expanded, false, 'never opens by itself for an agent not followed');
  // An action from its balloon posts through the host and redraws.
  a.content.body(doc).children[0].dispatch('click');
  await new Promise(resolve => setImmediate(resolve));
  assert.deepEqual(posted, [['ws-a', 'set_up']]);
  assert.match(a.content.line, /^Setting up Albums/);
  // Gone from the listing: its balloon goes too (FR30).
  commanders.sync([], '');
  assert.deepEqual(commanders.ids(), []);
  assert.equal(a.content, null);
});

test('a balloon whose run Home follows out says where it is going', () => {
  resetCommanderBalloons();
  const doc = fakeDocument();
  const drawn = [];
  const commanders = homeCommanders({
    doc,
    win: {},
    reportView: folderAgentReportView,
    renderCard: (view, options) => {
      drawn.push(options.opening);
      return doc.createElement('article');
    },
    act: async () => null
  });
  const done = raw({ workspace_id: 'ws-a', state: 'set_up', setup: { status: 'done', lines: [] } });
  commanders.sync([done], '', new Map([['ws-a', 'Opening Music Production Home…']]));
  const balloon = commanderBalloon('home:ws-a');
  assert.equal(balloon.content.line, 'Opening Music Production Home…');
  balloon.content.body(doc);
  assert.equal(drawn.at(-1), true, 'the card draws its own Opening line');
  // Without an opening, the same balloon keeps its own speech.
  commanders.sync([raw({ workspace_id: 'ws-a' })], '');
  assert.equal(commanderBalloon('home:ws-a').content.line, 'I have a plan for Albums · Review');
});

test('a balloon handed to the drawer is released, not cleared', () => {
  resetCommanderBalloons();
  const doc = fakeDocument();
  const commanders = homeCommanders({
    doc,
    win: {},
    reportView: folderAgentReportView,
    renderCard: () => doc.createElement('article'),
    act: async () => null
  });
  commanders.sync([raw({ workspace_id: 'ws-a' })], '');
  commanders.sync([raw({ workspace_id: 'ws-a' })], 'ws-a');
  assert.deepEqual(commanders.ids(), []);
  assert.equal(commanderBalloon('home:ws-a').content.line, 'I have a plan for Albums · Review');
});

test('the Commander anchor: command node, group Commander unit, else the lot', () => {
  const node = { id: 'node' };
  const unit = { id: 'unit' };
  const root = found => ({
    querySelector: selector => found[selector] || null,
    getBoundingClientRect: () => ({ left: 100, top: 50, width: 600, height: 400 })
  });
  assert.equal(workspaceCommanderAnchor(root({ '.ws-cmd-map-agent.is-command-node': node })), node);
  assert.equal(workspaceCommanderAnchor(root({ '.ws-map-unit.is-commander': unit })), unit);
  assert.deepEqual(workspaceCommanderAnchor(root({})), {
    left: 380,
    top: 98,
    width: 40,
    height: 40
  });
  assert.deepEqual(lotAnchor(root({})), { left: 380, top: 98, width: 40, height: 40 });
  assert.equal(workspaceCommanderAnchor(null), null);
  const canvas = { id: 'canvas' };
  const opmap = { id: 'opmap' };
  assert.equal(
    workspaceMapRoot({ querySelector: s => (s.includes('detachment') ? canvas : opmap) }),
    canvas
  );
  assert.equal(
    workspaceMapRoot({ querySelector: s => (s.includes('detachment') ? null : opmap) }),
    opmap
  );
});

test("Home's map is showing only when its map is on the page and the Tree view is not (visual-hire FR15, FR21)", () => {
  assert.equal(homeMapShowing({ hasMap: true, view: 'map' }), true);
  assert.equal(homeMapShowing({ hasMap: true, view: '' }), true);
  assert.equal(homeMapShowing({ hasMap: true, view: 'tree' }), false);
  // The workspace page, Needs you and any page without Home's map.
  assert.equal(homeMapShowing({ hasMap: false, view: 'map' }), false);
  assert.equal(homeMapShowing({ hasMap: 'yes' }), false);
  assert.equal(homeMapShowing(), false);
});

test('a tile is panned only when it is not wholly in the visible map area, to the area centre (FR17)', () => {
  // The map clipped to the left of a 640px drawer.
  const area = { left: 0, top: 100, width: 792, height: 700 };
  const tile = (left, top) => ({ left, top, width: 120, height: 100 });
  assert.equal(tileIntoAreaDelta(tile(200, 300), area), null);
  // The edges are inside.
  assert.equal(tileIntoAreaDelta({ left: 0, top: 100, width: 792, height: 700 }, area), null);
  // Under the drawer, and half under it: the move that centres the tile.
  assert.deepEqual(tileIntoAreaDelta(tile(900, 300), area), { dx: 564, dy: -100 });
  assert.deepEqual(tileIntoAreaDelta(tile(760, 300), area), { dx: 424, dy: -100 });
  // Above the area as well as beside it.
  assert.deepEqual(tileIntoAreaDelta(tile(200, 20), area), { dx: -136, dy: -380 });
  // Nothing to measure, nothing to move.
  assert.equal(tileIntoAreaDelta(null, area), null);
  assert.equal(tileIntoAreaDelta(tile(200, 300), null), null);
  assert.equal(tileIntoAreaDelta(tile(200, 300), { left: 0, top: 0, width: 0, height: 0 }), null);
});

// A page double for revealHiredTile: Home's map canvas with one tile whose rect
// the test sets, an open drawer (or none), and the map's camera calls recorded.
function hiredTileHarness({ tile, drawer = null, drawn = true, panResult = true }) {
  const calls = [];
  const listeners = {};
  let present = drawn;
  const node = { getBoundingClientRect: () => tile };
  const canvas = {
    querySelector: selector =>
      present && selector === '.ws-map-tile[data-ws-id="ws-1"]' ? node : null,
    getBoundingClientRect: () => ({ left: 0, top: 100, width: 1400, height: 700 }),
    closest: () => ({ querySelectorAll: () => [] })
  };
  const panel = drawer && { hidden: false, getBoundingClientRect: () => drawer };
  const doc = {
    querySelector: selector => (selector === '#cockpitMap .ws-map-canvas' ? canvas : null),
    getElementById: id => (id === 'personalAssistantPanel' ? panel : null),
    addEventListener: (type, fn) => {
      listeners[type] = fn;
    },
    removeEventListener: type => {
      delete listeners[type];
    }
  };
  const win = {
    innerWidth: 1440,
    innerHeight: 900,
    PersonalAssistantPanel: { isOpen: () => Boolean(drawer) },
    OriWorkspaceMap: {
      whenMotionSettled: ms => {
        calls.push(['settled', ms]);
        return Promise.resolve();
      },
      panBy: (dx, dy) => {
        calls.push(['panBy', dx, dy]);
        return panResult;
      },
      reveal: () => calls.push(['reveal']),
      fitAll: () => calls.push(['fitAll']),
      framePlaced: () => calls.push(['framePlaced'])
    },
    requestAnimationFrame: run => run()
  };
  return {
    doc,
    win,
    calls,
    listeners,
    mount() {
      present = true;
      listeners[MAP_MOUNTED_EVENT]?.();
    }
  };
}
const DRAWER = { left: 800, top: 0, width: 640, height: 900 };
const camera = h => h.calls.filter(([name]) => name !== 'settled').map(([name]) => name);

test('a hired tile under the drawer is panned into the map beside it; one in view is left alone (FR17)', async () => {
  const under = hiredTileHarness({
    tile: { left: 900, top: 300, width: 120, height: 100 },
    drawer: DRAWER
  });
  assert.equal(await revealHiredTile('ws-1', { doc: under.doc, win: under.win }), true);
  // The map had its motion settled first, then moved once, by the centring delta
  // inside the part of the map left of the drawer (8px gap).
  assert.deepEqual(under.calls, [
    ['settled', 1000],
    ['panBy', 564, -100]
  ]);

  const seen = hiredTileHarness({
    tile: { left: 200, top: 300, width: 120, height: 100 },
    drawer: DRAWER
  });
  assert.equal(await revealHiredTile('ws-1', { doc: seen.doc, win: seen.win }), true);
  assert.deepEqual(camera(seen), []);

  // A map that cannot move (not ready) says so.
  const stuck = hiredTileHarness({
    tile: { left: 900, top: 300, width: 120, height: 100 },
    drawer: DRAWER,
    panResult: false
  });
  assert.equal(await revealHiredTile('ws-1', { doc: stuck.doc, win: stuck.win }), false);
});

test('revealing a hired tile never selects it, refits the map or frames it, whatever the page does (FR17)', async () => {
  for (const tile of [
    { left: 900, top: 300, width: 120, height: 100 },
    { left: 200, top: 300, width: 120, height: 100 }
  ]) {
    const h = hiredTileHarness({ tile, drawer: DRAWER });
    await revealHiredTile('ws-1', { doc: h.doc, win: h.win });
    assert.deepEqual(
      camera(h).filter(name => name !== 'panBy'),
      []
    );
  }
});

test('a hired tile that is not drawn yet is waited for until the map mounts, then given up on', async () => {
  const late = hiredTileHarness({
    tile: { left: 900, top: 300, width: 120, height: 100 },
    drawer: DRAWER,
    drawn: false
  });
  const arriving = revealHiredTile('ws-1', { doc: late.doc, win: late.win, waitMs: 1000 });
  assert.ok(late.listeners[MAP_MOUNTED_EVENT], 'it listens for the map to mount');
  late.mount();
  assert.equal(await arriving, true);
  assert.deepEqual(camera(late), ['panBy']);
  assert.equal(late.listeners[MAP_MOUNTED_EVENT], undefined, 'and stops listening');

  const never = hiredTileHarness({
    tile: { left: 900, top: 300, width: 120, height: 100 },
    drawer: DRAWER,
    drawn: false
  });
  assert.equal(
    await revealHiredTile('ws-1', { doc: never.doc, win: never.win, waitMs: 20 }),
    false
  );
  assert.deepEqual(never.calls, []);
  assert.equal(never.listeners[MAP_MOUNTED_EVENT], undefined);
});
