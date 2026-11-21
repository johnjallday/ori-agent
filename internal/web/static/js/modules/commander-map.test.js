import test from 'node:test';
import assert from 'node:assert/strict';

import { folderAgentReportView } from './folder-agent-card.js';
import { commanderBalloon, resetCommanderBalloons } from './commander-balloon.js';
import { fakeDocument } from './commander-balloon.test.js';
import { speakingFolderAgents } from './home-workspace-cockpit.js';
import {
  commanderPresenter,
  homeCommanderMap,
  homeCommanders,
  homeMapShowing,
  homeTileAnchor,
  MAP_MOUNTED_EVENT,
  placeCommanderMap,
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

test('only the workspace Filing map suppresses its completed receipt while waiting for access', () => {
  const map = fakeMap({ narrow: true });
  map.filingStation = true;
  map.showing = () => map.state.visible;
  const host = fakeHost();
  const presenter = commanderPresenter(map, { hostLine: false });
  const completed = folderAgentReportView(
    raw({
      state: 'set_up',
      setup: { status: 'done' },
      filing_permission_required: true
    })
  );
  presenter.present(args(map, host, { view: completed, followed: true }));
  assert.equal(host.hidden, true);
  assert.equal(map.balloon.content, null);

  map.state.visible = false;
  map.fire();
  assert.equal(host.hidden, false, 'Details keeps its completed receipt');
  assert.match(map.balloon.content.line, /See what I made/);

  map.state.visible = true;
  map.state.narrow = false;
  map.fire();
  assert.equal(map.balloon.content, null);
  presenter.present(args(map, host, { view: { ...completed, filingPermissionRequired: false } }));
  assert.match(map.balloon.content.line, /See what I made/);
  assert.equal(map.balloon.expanded, false);

  const home = fakeMap();
  commanderPresenter(home).present(args(home, fakeHost(), { view: completed }));
  assert.match(home.balloon.content.line, /See what I made/);
});

test('on the Map tab the header card is one line and the balloon carries the report', () => {
  const map = fakeMap();
  const host = fakeHost();
  const presenter = commanderPresenter(map);
  assert.equal(presenter.present(args(map, host)), true);
  assert.equal(host.shown.children[0].textContent, 'Albums Manager has a plan for Albums');
  assert.equal(host.shown.children[1].textContent, 'Review');
  assert.equal(map.balloon.content.line, 'Albums Manager has a plan for Albums · Review');
  assert.deepEqual(map.balloon.content.speaker, {
    name: 'Albums Manager',
    role: 'Commander',
    face: ''
  });
  assert.equal(map.balloon.content.body().kind, 'compact');
  // A report already waiting when the page opened: no expanding by itself.
  assert.equal(map.balloon.expanded, false);
  // Review expands the balloon and brings the map into view.
  host.shown.children[1].listeners.click();
  assert.deepEqual(map.calls.slice(-2), [['reveal'], ['expand', true]]);
});

// The page's Next action strip speaks for the same folder agent: the presenter
// hands it each view the page already fetched (no second request), and gives it
// the line's own Review for a report that has no message in the chat.
test('the presenter hands each view to the page and reviews as the line does (folder-question-said-once FR15)', () => {
  const map = fakeMap();
  const host = fakeHost();
  const seen = [];
  let reviewed = 0;
  const presenter = commanderPresenter(map, {
    onView: entry => seen.push(entry),
    onReview: () => (reviewed += 1)
  });
  // Nothing to review before a view.
  assert.equal(presenter.review(), false);

  const first = args(map, host);
  presenter.present(first);
  assert.equal(seen.length, 1);
  assert.equal(seen[0].view, first.view);
  assert.equal(seen[0].raw, first.raw);
  // Each later view goes the same way, and a presenter told "nothing" says so.
  presenter.present(args(map, host, { previous: first.view }));
  assert.equal(seen.length, 2);
  presenter.present(null);
  assert.equal(seen.at(-1), null);

  // Review, on the map: the line's Review button does exactly this.
  presenter.present(first);
  const before = map.calls.length;
  assert.equal(presenter.review(), true);
  assert.deepEqual(map.calls.slice(before), [['reveal'], ['expand', true]]);
  assert.equal(reviewed, 1);
  host.shown.children[1].listeners.click();
  assert.deepEqual(map.calls.slice(-2), [['reveal'], ['expand', true]]);
  assert.equal(reviewed, 2);

  // Off the map there is no balloon to open: the full card the host shows is
  // brought into view instead, and the balloon is left alone.
  map.state.visible = false;
  const offMap = map.calls.length;
  assert.equal(presenter.review(), true);
  assert.equal(map.calls.slice(offMap).filter(c => c[0] === 'expand').length, 0);
  assert.equal(reviewed, 2);
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
  assert.equal(map.balloon.content.line, 'Albums Manager has a plan for Albums · Review');
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
  assert.equal(asking.map.balloon.content.line, 'Albums Manager asks to learn the songs · Answer');
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

test('a host that keeps no line says nothing on the map and comes back with the full card elsewhere (map-above-the-fold)', () => {
  const map = fakeMap();
  const host = { ...fakeHost(), hidden: false };
  const presenter = commanderPresenter(map, { hostLine: false });
  assert.equal(presenter.present(args(map, host)), true);
  // On the map the Commander's line is the status: the host is empty and hidden.
  assert.equal(host.shown, undefined);
  assert.equal(host.hidden, true);
  assert.equal(map.balloon.content.line, 'Albums Manager has a plan for Albums · Review');
  // The page unhides the host before each draw; a redraw hides it again.
  host.hidden = false;
  map.fire();
  assert.equal(host.hidden, true);
  // Another tab, or a screen too narrow for a balloon: the full card, shown.
  map.state.visible = false;
  map.fire();
  assert.equal(host.shown.kind, 'full');
  assert.equal(host.hidden, false);
  map.state.visible = true;
  map.state.narrow = true;
  map.fire();
  assert.equal(host.shown.kind, 'full');
  assert.equal(host.hidden, false);
});

test('a report posted to the conversation is one line off the map, and nothing while the chat shows it (Plan B)', () => {
  const map = fakeMap({ visible: false });
  const host = { ...fakeHost(), hidden: false };
  let held = false;
  const opened = [];
  const presenter = commanderPresenter(map, {
    hostLine: false,
    chatHolds: () => held,
    chatLine: { open: (ref, button) => opened.push([ref, button]) }
  });
  const posted = {
    ...args(map, host),
    raw: raw({ state: 'reported', chat: { session_id: 's-1', message_id: 'm-1', unread: true } })
  };
  presenter.present(posted);
  // Overview: one line that opens the report in the chat, not a second card.
  assert.equal(host.hidden, false);
  assert.equal(host.shown.children[0].textContent, 'Albums Manager has a plan for Albums');
  assert.equal(host.shown.children[1].textContent, 'Open in chat');
  host.shown.children[1].listeners.click();
  assert.deepEqual(opened[0][0], { sessionID: 's-1', messageID: 'm-1', unread: true, lead: '' });
  // The open chat shows it: the page says nothing, the balloon carries no body.
  held = true;
  map.fire();
  assert.equal(host.hidden, true);
  assert.equal(map.balloon.content.body, null);
  // On the Map with the chat showing it: the balloon folds to its line.
  map.state.visible = true;
  map.balloon.expand();
  map.fire();
  assert.equal(map.balloon.expanded, false);
  assert.equal(host.hidden, true);
  // The chat closes: the balloon carries the plan again; the page stays quiet.
  held = false;
  map.fire();
  assert.equal(typeof map.balloon.content.body, 'function');
  assert.equal(host.hidden, true);
});

test('a kept report the chat shows is not drawn again on the page', () => {
  const map = fakeMap({ visible: false });
  const host = { ...fakeHost(), hidden: false };
  let held = true;
  const presenter = commanderPresenter(map, {
    hostLine: false,
    chatHolds: () => held,
    chatLine: { open: () => {} }
  });
  const kept = folderAgentReportView(raw({ state: 'kept' }));
  presenter.present({
    ...args(map, host, { view: kept }),
    raw: raw({ state: 'kept', chat: { session_id: 's-1', message_id: 'm-1' } })
  });
  assert.equal(host.hidden, true);
  held = false;
  map.fire();
  assert.equal(host.shown.kind, 'full');
  assert.equal(host.hidden, false);
});

test('a report that was never posted keeps the full card off the map', () => {
  const map = fakeMap({ visible: false });
  const host = { ...fakeHost(), hidden: false };
  commanderPresenter(map, { hostLine: false, chatLine: { open: () => {} } }).present(
    args(map, host)
  );
  assert.equal(host.shown.kind, 'full');
});

test('the report names the ticket it narrates while it has something to say', () => {
  const map = fakeMap();
  const host = fakeHost();
  const heard = [];
  const presenter = commanderPresenter(map, { hostLine: false, onNarrated: id => heard.push(id) });
  const withTicket = (state, ticket) => ({
    ...args(map, host, { view: folderAgentReportView(raw({ state })) }),
    raw: raw({ state, ticket_id: ticket })
  });
  presenter.present(withTicket('looking', 't-look'));
  presenter.present(withTicket('reported', 't-look'));
  assert.deepEqual(heard, ['t-look'], 'told once, not on every draw');
  // A kept folder has nothing to say: the ticket is a ticket like any other again.
  presenter.present(withTicket('kept', 't-look'));
  assert.deepEqual(heard, ['t-look', '']);
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
  // The owner pressed Hire & build: followed, but the tile is not drawn yet.
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
  assert.equal(a.content.line, 'Albums Manager has a plan for Albums · Review');
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

// FR24, FR27: the balloon speaks with the Commander's own face on both maps,
// the one its card or tile draws, and draws again when the face arrives late.
test("the balloon speaks with the Commander's face and says so again when the face arrives", () => {
  const map = fakeMap();
  const host = fakeHost();
  const faces = new Map();
  const presenter = commanderPresenter(map, { face: name => faces.get(name) });
  // No face yet (the record has not loaded): the speaker carries none, and the
  // balloon falls back to initials.
  presenter.present(args(map, host));
  assert.equal(map.balloon.content.speaker.face, '');
  // The face arrives: a redraw gives it to the balloon.
  faces.set('Albums Manager', '<span class="agent-avatar agent-avatar--character"></span>');
  presenter.retick();
  assert.equal(
    map.balloon.content.speaker.face,
    '<span class="agent-avatar agent-avatar--character"></span>'
  );

  // A provider that fails, or answers with something else, never breaks the balloon.
  for (const bad of [
    () => {
      throw new Error('renderer not loaded');
    },
    () => null,
    () => ({ not: 'markup' }),
    () => 42
  ]) {
    const broken = commanderPresenter(fakeMap(), { face: bad });
    const brokenMap = fakeMap();
    const again = commanderPresenter(brokenMap, { face: bad });
    again.present(args(brokenMap, fakeHost()));
    assert.equal(brokenMap.balloon.content.speaker.face, '');
    broken.dispose();
  }
  // No provider at all (a host that has no face) is the same.
  const bareMap = fakeMap();
  commanderPresenter(bareMap).present(args(bareMap, fakeHost()));
  assert.equal(bareMap.balloon.content.speaker.face, '');
});

test("Home's balloons speak with the tile's face and repaint when it arrives (FR27)", () => {
  resetCommanderBalloons();
  const doc = fakeDocument();
  const faces = new Map();
  const asked = [];
  const commanders = homeCommanders({
    doc,
    win: {},
    reportView: folderAgentReportView,
    renderCard: () => doc.createElement('article'),
    act: async () => null,
    face: name => {
      asked.push(name);
      return faces.get(name);
    }
  });
  commanders.sync(speakingFolderAgents([raw({ workspace_id: 'ws-a' })]), '');
  const balloon = commanderBalloon('home:ws-a');
  assert.equal(balloon.content.speaker.face, '');
  assert.ok(asked.includes('Albums Manager'));
  faces.set('Albums Manager', '<b class="tile-face"></b>');
  commanders.repaint();
  assert.equal(balloon.content.speaker.face, '<b class="tile-face"></b>');
  commanders.sync([], '');
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
  assert.equal(
    commanderBalloon('home:ws-a').content.line,
    'Albums Manager has a plan for Albums · Review'
  );
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
  assert.equal(
    commanderBalloon('home:ws-a').content.line,
    'Albums Manager has a plan for Albums · Review'
  );
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

test('a building tile pans on arrival without waiting for its assembly to settle', async () => {
  const h = hiredTileHarness({
    tile: { left: 900, top: 300, width: 120, height: 100 },
    drawer: DRAWER,
    drawn: false
  });
  h.win.OriWorkspaceMap.isAssembling = id => id === 'ws-1';
  h.win.OriWorkspaceMap.whenMotionSettled = () => new Promise(() => {});
  const result = revealHiredTile('ws-1', { doc: h.doc, win: h.win });
  h.mount();
  assert.equal(await result, true);
  assert.deepEqual(h.calls, [['panBy', 564, -100]]);
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

// ---------------------------------------------------------------------------
// One adapter for any map root (waiting-agents-on-parent-map)
// ---------------------------------------------------------------------------

// A map root that answers only the selectors a test names and records every
// selector it was asked, so a test can see what the adapter looked for.
function selectorRoot(found = {}) {
  const asked = [];
  return {
    asked,
    querySelector: selector => {
      asked.push(selector);
      return found[selector] || null;
    }
  };
}

test('the adapter anchors a place on any root: a tile, or a district header, and never a mark', () => {
  const tile = { id: 'tile', closest: () => null };
  const header = { id: 'header', closest: () => null };
  const onTile = selectorRoot({ '.ws-map-tile[data-ws-id="m1"]': tile });
  const onHeader = selectorRoot({
    '.ws-map-district[data-group-id="albums"] .ws-map-district-header': header
  });
  let state = { showing: true };
  const adapter = (id, root) =>
    placeCommanderMap(id, {
      key: `member:${id}`,
      root: () => root,
      showing: () => state.showing,
      doc: { querySelector: () => null },
      win: { innerWidth: 1200 }
    });
  assert.equal(adapter('m1', onTile).visible(), true, 'a tile on a non-Home root');
  assert.equal(adapter('albums', onHeader).visible(), true, 'a district header on a non-Home root');
  assert.equal(adapter('ghost', selectorRoot()).visible(), false, 'nothing drawn for it');
  // The only selectors it ever asks for are the tile's and the district header's:
  // the wait mark has neither, so a balloon is never anchored to a mark that the
  // map re-creates on every update.
  const asked = [...onTile.asked, ...onHeader.asked];
  assert.ok(asked.length > 0);
  asked.forEach(selector => assert.doesNotMatch(selector, /wait|mark/));
  // A map that is not showing is not a place to speak from.
  state = { showing: false };
  assert.equal(adapter('m1', onTile).visible(), false);
});

test('a place filtered out of the map is not visible, and viewShowing replaces showing for visible', () => {
  const filtered = { closest: () => ({ classList: { contains: c => c === 'is-filtered-out' } }) };
  const root = selectorRoot({ '.ws-map-tile[data-ws-id="m1"]': filtered });
  const base = { key: 'k', root: () => root, doc: {}, win: {} };
  assert.equal(placeCommanderMap('m1', { ...base, showing: () => true }).visible(), false);
  // viewShowing is the gate `visible` asks (Home's Tree view); showing is not.
  const plain = { closest: () => null };
  const open = selectorRoot({ '.ws-map-tile[data-ws-id="m1"]': plain });
  const gated = placeCommanderMap('m1', {
    ...base,
    root: () => open,
    showing: () => false,
    viewShowing: () => true
  });
  assert.equal(gated.visible(), true);
  assert.equal(gated.showing(), false);
  const hidden = placeCommanderMap('m1', {
    ...base,
    root: () => open,
    showing: () => true,
    viewShowing: () => false
  });
  assert.equal(hidden.visible(), false);
});

test('key, pinLine, onDismiss and onNarrow reach the balloon; the presenter’s onNarrow is the default', () => {
  resetCommanderBalloons();
  const balloonDoc = fakeDocument();
  const rootEl = balloonDoc.createElement('div');
  rootEl.rect = { left: 0, top: 0, width: 1000, height: 700 };
  balloonDoc.body.append(rootEl);
  const own = () => 'own';
  const dismiss = () => 'dismiss';
  const adapter = placeCommanderMap('m1', {
    key: 'member:m1',
    root: () => rootEl,
    showing: () => true,
    pinLine: true,
    onDismiss: dismiss,
    onNarrow: own,
    doc: balloonDoc,
    win: { innerWidth: 1200 }
  });
  const presenterNarrow = () => 'presenter';
  assert.equal(adapter.attach({ onNarrow: presenterNarrow }), true);
  assert.equal(adapter.balloon.key, 'member:m1');
  assert.equal(adapter.balloon.pinLine, true);
  assert.equal(adapter.balloon.onDismiss, dismiss);
  assert.equal(adapter.balloon.onNarrow, own, 'the adapter’s own onNarrow wins');
  assert.equal(adapter.balloon.nodes.close.getAttribute('aria-label'), 'Close');

  // Without its own onNarrow and onDismiss the adapter changes nothing: the
  // presenter's onNarrow is used and the balloon folds as it always did.
  const plain = placeCommanderMap('m2', {
    key: 'home:m2',
    root: () => rootEl,
    showing: () => true,
    doc: balloonDoc,
    win: { innerWidth: 1200 }
  });
  plain.attach({ onNarrow: presenterNarrow });
  assert.equal(plain.balloon.onNarrow, presenterNarrow);
  assert.equal(plain.balloon.onDismiss, null);
  assert.equal(plain.balloon.pinLine, false);
  assert.equal(plain.balloon.nodes.close.getAttribute('aria-label'), 'Collapse');
  // No root, nothing to attach to.
  assert.equal(
    placeCommanderMap('m3', {
      key: 'k3',
      root: () => null,
      showing: () => true,
      doc: balloonDoc
    }).attach(),
    false
  );
});

test('Home’s adapter is the same adapter with Home’s own key, root, and Tree-view rule', () => {
  resetCommanderBalloons();
  const view = { value: 'map' };
  const cockpit = { present: true };
  const tile = { closest: () => null };
  const canvas = selectorRoot({ '.ws-map-tile[data-ws-id="ws-1"]': tile });
  const homeDoc = {
    querySelector: selector => (selector === '#cockpitMap .ws-map-canvas' ? canvas : null),
    getElementById: id => {
      if (id === 'cockpitMap') return cockpit.present ? {} : null;
      if (id === 'homeCockpit') return { dataset: { view: view.value } };
      return null;
    },
    createElement: () => ({})
  };
  const adapter = homeCommanderMap('ws-1', { doc: homeDoc, win: { innerWidth: 1200 } });
  assert.equal(adapter.balloon.key, 'home:ws-1');
  assert.equal(adapter.showing(), true);
  assert.equal(adapter.visible(), true);
  // The Tree view hides it, as it always did.
  view.value = 'tree';
  assert.equal(adapter.showing(), false);
  assert.equal(adapter.visible(), false);
  // No cockpit map on the page: not showing; visible still reads the map's own
  // canvas, as before.
  view.value = 'map';
  cockpit.present = false;
  assert.equal(adapter.showing(), false);
  assert.equal(adapter.visible(), true);
});
