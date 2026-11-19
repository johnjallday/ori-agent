import test from 'node:test';
import assert from 'node:assert/strict';

import { commanderBalloon, resetCommanderBalloons } from './commander-balloon.js';
import { fakeDocument } from './commander-balloon.test.js';
import { folderAgentReportView } from './folder-agent-card.js';
import { memberCommanders } from './member-commanders.js';
import { memberWaits } from './member-folder-agents.js';

const songsOffer = {
  count: 5,
  noun: 'songs',
  ask: 'Want me to learn the 5 songs?',
  reads: 'I will read tempo and track names.',
  not_read: ''
};
const done = { status: 'done', lines: [] };

// A raw server view that waits (the songs offer), and ones that do not.
const waiting = (id, overrides = {}) => ({
  workspace_id: id,
  workspace_name: id,
  folder_name: id,
  agent_name: `${id} Manager`,
  state: 'set_up',
  setup: done,
  songs_offer: songsOffer,
  ...overrides
});
const working = id => ({
  workspace_id: id,
  workspace_name: id,
  folder_name: id,
  agent_name: `${id} Manager`,
  state: 'setting_up'
});
const stateOf = (...views) => ({ views, waits: memberWaits(views) });
const detail = (id, extra = {}) => ({
  workspaceId: id,
  placeId: id,
  placeKind: 'tile',
  ids: [id],
  count: 1,
  own: true,
  phase: 'songs',
  agent: `${id} Manager`,
  ...extra
});

// A group page's map over the balloon test file's fake DOM: a canvas the
// balloon attaches to, tiles it can anchor on, and marks the module writes
// aria-expanded on.
function page({ width = 1200, mode = 'map', home = false } = {}) {
  resetCommanderBalloons();
  const doc = fakeDocument();
  doc.body.dataset.workspaceViewMode = mode;
  const canvas = doc.createElement('div');
  canvas.rect = { left: 0, top: 0, width: 1000, height: 700 };
  doc.body.append(canvas);
  const tiles = {};
  const marks = [];
  canvas.querySelector = selector => {
    const id = /data-ws-id="([^"]+)"/.exec(selector)?.[1];
    return id && selector.startsWith('.ws-map-tile') ? tiles[id] || null : null;
  };
  // A group page's map root is found by the page's own selector; Home's page has
  // no such element, so a Home-style set must be given its root instead.
  doc.querySelector = home
    ? () => null
    : selector => (selector === '.ws-cmd-detachment-host .ws-map-canvas' ? canvas : null);
  const host = {
    querySelectorAll: selector =>
      selector === '[data-wait-mark]'
        ? marks
        : selector === '.ws-map-tile'
          ? Object.values(tiles)
          : []
  };
  const addTile = id => {
    const tile = doc.createElement('button');
    tile.className = 'ws-map-tile';
    tile.setAttribute('data-ws-id', id);
    tile.rect = { left: 400, top: 300, width: 150, height: 170 };
    canvas.append(tile);
    tiles[id] = tile;
    return tile;
  };
  const addMark = (id, count = 1) => {
    const mark = doc.createElement('button');
    mark.setAttribute('data-wait-mark', id);
    mark.setAttribute('data-wait-count', String(count));
    mark.removeAttribute = name => delete mark.attributes[name];
    marks.push(mark);
    canvas.append(mark);
    return mark;
  };
  // The map replaces a mark when it updates in place: the old element is gone.
  const dropMark = mark => {
    marks.splice(marks.indexOf(mark), 1);
    mark.remove();
  };
  const cards = [];
  const acts = [];
  const opened = [];
  const result = { value: null };
  const commanders = memberCommanders({
    host: () => host,
    doc,
    win: { innerWidth: width },
    reportView: folderAgentReportView,
    renderCard: (view, options) => {
      const card = doc.createElement('article');
      cards.push({ view, options, card });
      return card;
    },
    act: async (view, action, extra) => {
      acts.push({ view, action, extra });
      return result.value;
    },
    open: id => opened.push(id),
    face: name => `<span class="face">${name}</span>`,
    ...(home
      ? { root: () => canvas, showing: () => true, docEvents: [], keyPrefix: 'home-wait' }
      : {})
  });
  const balloonOf = id => commanderBalloon(`${home ? 'home-wait' : 'member'}:${id}`, { doc });
  return { doc, commanders, addTile, addMark, dropMark, balloonOf, cards, acts, opened, result };
}

test('an activation shows the agent’s own folded line at its place, and focuses it', () => {
  const p = page();
  const tile = p.addTile('m1');
  const mark = p.addMark('m1');
  mark.focus();
  const took = p.commanders.show(detail('m1'), stateOf(waiting('m1')));
  assert.equal(took, true);
  const balloon = p.balloonOf('m1');
  assert.equal(balloon.nodes.lineText.textContent, 'm1 Manager asks to learn the songs · Answer');
  assert.equal(
    balloon.expanded,
    false,
    'the line comes first (D3); the card opens from its button'
  );
  assert.equal(balloon.pinLine, true);
  assert.equal(p.doc.activeElement, balloon.nodes.line, 'focus is on what it said');
  assert.equal(balloon.anchorElement(), tile, 'anchored on the place, not on the mark');
  assert.equal(p.commanders.shownId(), 'm1');
  assert.equal(mark.getAttribute('aria-expanded'), 'true');
  // The face the host draws is the one on the balloon.
  assert.match(balloon.nodes.lineFace.innerHTML || '', /m1 Manager/);
});

test('an activation the module cannot take is left to the map, which opens the member’s page', () => {
  const view = waiting('m1');
  const cases = {
    'more than one wait': [detail('m1', { count: 2, ids: ['m1', 'm2'] }), stateOf(view), {}],
    'no id': [detail('', { workspaceId: '' }), stateOf(view), {}],
    'no listed view': [detail('m1'), { views: [], waits: { m1: {} } }, {}],
    'no wait for it': [detail('m1'), { views: [view], waits: {} }, {}],
    'a narrow window': [detail('m1'), stateOf(view), { width: 500 }],
    'a page that is not on the Map tab': [detail('m1'), stateOf(view), { mode: 'details' }]
  };
  for (const [name, [args, state, options]] of Object.entries(cases)) {
    const p = page(options);
    p.addTile('m1');
    p.addMark('m1');
    // `mode` only changes what the balloon calls "showing"; the activation is
    // still taken when the map root exists, so that case is the one that opens.
    const took = p.commanders.show(args, state);
    if (name === 'a page that is not on the Map tab') {
      assert.equal(took, true, name);
      continue;
    }
    assert.equal(took, false, name);
    assert.equal(p.commanders.shownId(), '', name);
    assert.equal(p.balloonOf('m1').content, null, `${name}: nothing was created`);
  }
  // No map root on the page: nothing to speak from.
  const none = page();
  none.doc.querySelector = () => null;
  assert.equal(none.commanders.show(detail('m1'), stateOf(waiting('m1'))), false);
});

test('activating the same mark again closes the line, and another member replaces it', () => {
  const p = page();
  p.addTile('m1');
  p.addTile('m2');
  const m1 = p.addMark('m1');
  const m2 = p.addMark('m2');
  const state = stateOf(waiting('m1'), waiting('m2'));
  p.commanders.show(detail('m1'), state);
  assert.equal(m1.getAttribute('aria-expanded'), 'true');
  assert.equal(m2.getAttribute('aria-expanded'), 'false');

  // Another member: the first closes, the second speaks, one at a time.
  assert.equal(p.commanders.show(detail('m2'), state), true);
  assert.equal(p.commanders.shownId(), 'm2');
  assert.equal(p.balloonOf('m1').content, null);
  assert.ok(p.balloonOf('m2').content);
  assert.equal(m1.getAttribute('aria-expanded'), 'false');
  assert.equal(m2.getAttribute('aria-expanded'), 'true');

  // The same mark again toggles it off, and the activation is still taken. The
  // owner pressed the mark, so focus is on the mark; the toggle does not move it.
  m2.focus();
  assert.equal(p.commanders.show(detail('m2'), state), true);
  assert.equal(p.commanders.shownId(), '');
  assert.equal(p.balloonOf('m2').content, null);
  assert.equal(m2.getAttribute('aria-expanded'), 'false');
  assert.equal(p.doc.activeElement, m2, 'focus stayed on the mark that was pressed');
});

test('Escape or the close control closes the balloon and returns focus to the mark', () => {
  const p = page();
  p.addTile('m1');
  const mark = p.addMark('m1');
  p.commanders.show(detail('m1'), stateOf(waiting('m1')));
  const balloon = p.balloonOf('m1');
  assert.equal(balloon.nodes.close.getAttribute('aria-label'), 'Close');
  balloon.nodes.line.dispatch('keydown', { key: 'Escape' });
  assert.equal(p.commanders.shownId(), '');
  assert.equal(balloon.content, null);
  assert.equal(p.doc.activeElement, mark, 'focus returned to the mark');
  assert.equal(mark.getAttribute('aria-expanded'), 'false');

  // Expanded: Escape inside the card, and the close control, do the same.
  p.commanders.show(detail('m1'), stateOf(waiting('m1')));
  const again = p.balloonOf('m1');
  again.expand();
  assert.equal(again.expanded, true);
  again.nodes.box.dispatch('keydown', { key: 'Escape' });
  assert.equal(p.commanders.shownId(), '');
  assert.equal(p.doc.activeElement, mark);
  p.commanders.show(detail('m1'), stateOf(waiting('m1')));
  p.balloonOf('m1').nodes.close.dispatch('click');
  assert.equal(p.commanders.shownId(), '');
});

test('the mark is found again by its place when it was re-created while the line was open', () => {
  const p = page();
  const tile = p.addTile('m1');
  const first = p.addMark('m1');
  p.commanders.show(detail('m1'), stateOf(waiting('m1')));
  // An in-place update of the map replaced the mark element.
  p.dropMark(first);
  const second = p.addMark('m1');
  p.commanders.syncMarks();
  assert.equal(second.getAttribute('aria-expanded'), 'true', 'the new mark is told it is open');
  p.balloonOf('m1').nodes.line.dispatch('keydown', { key: 'Escape' });
  assert.equal(p.doc.activeElement, second, 'focus goes to the new mark, not the stale one');
  // With no mark at all (the wait is about to clear) it falls back to the tile.
  p.commanders.show(detail('m1'), stateOf(waiting('m1')));
  p.dropMark(second);
  p.balloonOf('m1').nodes.line.dispatch('keydown', { key: 'Escape' });
  assert.equal(p.doc.activeElement, tile);
});

test('sync never opens a balloon; it draws the shown one fresh and closes it when the wait is gone', () => {
  const p = page();
  p.addTile('m1');
  p.addTile('m2');
  const m1 = p.addMark('m1');
  const m2 = p.addMark('m2');
  const focus = p.doc.activeElement;
  // A wait that appears on its own adds nothing but the marks' own state.
  p.commanders.sync(stateOf(waiting('m1'), waiting('m2')));
  assert.equal(p.commanders.shownId(), '');
  assert.equal(p.balloonOf('m1').content, null);
  assert.equal(p.balloonOf('m2').content, null);
  assert.equal(p.doc.activeElement, focus, 'focus did not move (D4)');
  assert.equal(m1.getAttribute('aria-expanded'), 'false', 'a mark says it is closed');
  assert.equal(m2.getAttribute('aria-expanded'), 'false');

  p.commanders.show(detail('m1'), stateOf(waiting('m1')));
  // The agent's words changed: the shown balloon is drawn from the fresh view.
  p.commanders.sync(
    stateOf(
      waiting('m1', { songs_offer: { ...songsOffer, noun: 'tracks', ask: 'Learn 5 tracks?' } })
    )
  );
  assert.match(p.balloonOf('m1').nodes.lineText.textContent, /asks to learn the tracks/);
  assert.equal(p.commanders.shownId(), 'm1');
  // It stopped waiting (it went to work): the balloon goes and the mark says closed.
  p.commanders.sync(stateOf(working('m1')));
  assert.equal(p.commanders.shownId(), '');
  assert.equal(p.balloonOf('m1').content, null);
  assert.equal(m1.getAttribute('aria-expanded'), 'false');
  // It vanished from the listing altogether.
  p.commanders.show(detail('m1'), stateOf(waiting('m1')));
  p.commanders.sync({ views: [], waits: {} });
  assert.equal(p.commanders.shownId(), '');
});

test('a mark that stands for several waits carries no aria-expanded; one wait carries true or false', () => {
  const p = page();
  p.addTile('m1');
  const one = p.addMark('m1', 1);
  const many = p.addMark('albums', 3);
  many.setAttribute('aria-expanded', 'true');
  p.commanders.syncMarks();
  assert.equal(one.getAttribute('aria-expanded'), 'false');
  assert.equal(many.getAttribute('aria-expanded'), null);
  // After a map mount redrew the marks, syncMarks puts the state back.
  p.commanders.show(detail('m1'), stateOf(waiting('m1')));
  one.removeAttribute('aria-expanded');
  p.commanders.syncMarks();
  assert.equal(one.getAttribute('aria-expanded'), 'true');
});

test('the card’s action posts through act, follows to the next wait, and closes when the agent works', async () => {
  const p = page();
  p.addTile('m1');
  const mark = p.addMark('m1');
  p.commanders.show(detail('m1'), stateOf(waiting('m1')));
  p.balloonOf('m1').expand();
  const card = () => p.cards.at(-1).options;
  assert.equal(card().compact, true);
  assert.equal(card().opening, false);

  // An action that leaves another question: the balloon follows to it.
  const button = { disabled: false };
  p.result.value = {
    view: waiting('m1', { songs_offer: undefined, goal_offer: { goal: 'Finish the album' } })
  };
  await card().onAction('learn_songs', button, { x: 1 });
  assert.equal(button.disabled, true, 'the pressed button is disabled while it posts');
  assert.equal(p.acts.length, 1);
  assert.equal(p.acts[0].action, 'learn_songs');
  assert.deepEqual(p.acts[0].extra, { x: 1 });
  assert.equal(p.acts[0].view.agent, 'm1 Manager');
  assert.equal(p.commanders.shownId(), 'm1', 'it still waits: on the goal now');
  assert.match(p.balloonOf('m1').nodes.lineText.textContent, /suggests a goal/);

  // An error stays on the card, with the same view.
  p.result.value = { error: 'That did not go through.', view: null };
  await card().onAction('use_goal', null, {});
  assert.equal(p.commanders.shownId(), 'm1');
  assert.equal(card().error, 'That did not go through.');

  // The answer sends the agent to work: the balloon closes. Focus was inside the
  // card, so it returns to the mark.
  p.balloonOf('m1').focusFirst();
  p.balloonOf('m1').nodes.box.dispatch('focusin', { target: p.balloonOf('m1').nodes.body });
  p.result.value = { view: working('m1') };
  await card().onAction('use_goal', null, {});
  assert.equal(p.commanders.shownId(), '');
  assert.equal(p.balloonOf('m1').content, null);
  assert.equal(p.doc.activeElement, mark, 'focus was inside, so it returns to the mark');
  assert.equal(mark.getAttribute('aria-expanded'), 'false');
});

test('closing while the request runs leaves nothing to draw, and focus elsewhere is not pulled back', async () => {
  const p = page();
  p.addTile('m1');
  const mark = p.addMark('m1');
  p.commanders.show(detail('m1'), stateOf(waiting('m1')));
  p.result.value = { view: waiting('m1') };
  // The action is posted, then the balloon closes before the server answers.
  const posting = p.cards.at(-1).options.onAction('learn_songs', null, {});
  p.commanders.closeAll();
  await posting;
  assert.equal(p.acts.length, 1, 'the action was posted');
  assert.equal(p.commanders.shownId(), '');
  assert.equal(p.balloonOf('m1').content, null, 'the answer drew nothing after it closed');
  assert.notEqual(p.doc.activeElement, mark, 'focus was not pulled to the mark');

  // An answer that sends the agent to work while focus is OUTSIDE the card does
  // not move focus.
  const q = page();
  q.addTile('m1');
  const outside = q.addMark('other');
  q.commanders.show(detail('m1'), stateOf(waiting('m1')));
  outside.focus();
  q.result.value = { view: working('m1') };
  await q.cards.at(-1).options.onAction('learn_songs', null, {});
  assert.equal(q.commanders.shownId(), '');
  assert.equal(q.doc.activeElement, outside, 'focus stayed where the owner put it');
});

test('closeAll takes the balloon away with the map, and a narrow press on the line opens the member’s page', () => {
  const p = page();
  p.addTile('m1');
  const mark = p.addMark('m1');
  p.commanders.show(detail('m1'), stateOf(waiting('m1')));
  p.commanders.closeAll();
  assert.equal(p.commanders.shownId(), '');
  assert.equal(p.balloonOf('m1').content, null);
  assert.equal(mark.getAttribute('aria-expanded'), 'false');
  p.commanders.closeAll();

  // The window narrowed after the line was shown: pressing it cannot open a
  // balloon, so it opens the member's own page, where the full card is.
  const narrow = page({ width: 1200 });
  narrow.addTile('m1');
  narrow.addMark('m1');
  narrow.commanders.show(detail('m1'), stateOf(waiting('m1')));
  const balloon = narrow.balloonOf('m1');
  balloon.narrowOf = () => true;
  balloon.nodes.line.dispatch('click');
  assert.deepEqual(narrow.opened, ['m1']);
  assert.equal(balloon.expanded, false);
});

test('Home’s map is served by the same balloon through its own root, showing check and key', () => {
  const p = page({ home: true });
  const tile = p.addTile('m1');
  const mark = p.addMark('m1');
  // The page has no group-page root at all: the root Home gave is the one used.
  assert.equal(p.doc.querySelector('.ws-cmd-detachment-host .ws-map-canvas'), null);
  assert.equal(p.commanders.show(detail('m1'), stateOf(waiting('m1'))), true);
  const balloon = p.balloonOf('m1');
  assert.equal(balloon.key, 'home-wait:m1', 'its own key, apart from Home’s `home:<id>` balloons');
  assert.equal(balloon.nodes.lineText.textContent, 'm1 Manager asks to learn the songs · Answer');
  assert.equal(balloon.expanded, false);
  assert.equal(balloon.anchorElement(), tile);
  assert.equal(mark.getAttribute('aria-expanded'), 'true');
  // Opening Home's own balloon on the same root folds this one; two are never
  // expanded at once.
  balloon.expand();
  const other = commanderBalloon('home:m2', { doc: p.doc });
  other.attach(balloon.root, { anchor: () => tile, narrow: () => false });
  other.update({
    speaker: { name: 'm2 Manager', role: 'Commander' },
    line: 'm2 has a plan · Review',
    signature: 'x',
    body: () => p.doc.createElement('article')
  });
  other.expand();
  assert.equal(other.expanded, true);
  assert.equal(balloon.expanded, false, 'one expanded balloon per map root');
  // Dismissing still closes it and puts focus back on the mark.
  balloon.nodes.line.dispatch('keydown', { key: 'Escape' });
  assert.equal(p.commanders.shownId(), '');
  assert.equal(p.doc.activeElement, mark);
});

// The demo found it on Home: open the line, dismiss it, collapse the group (the map
// is re-mounted), press the mark: no line appeared and nothing took focus, until the
// next mount event. A dismissed balloon is kept, ignores attach() while it is
// removed, and the presenter attaches BEFORE it updates, so the second life began in
// the old, detached canvas.
test('a line shown again after the map was re-mounted lands in the map that is on the page', () => {
  const p = page();
  p.addTile('m1');
  p.addMark('m1');
  const state = stateOf(waiting('m1'));
  assert.equal(p.commanders.show(detail('m1'), state), true);
  const oldRoot = p.balloonOf('m1').root;
  assert.ok(oldRoot);
  // Dismissed (the balloon is kept, as removed).
  p.balloonOf('m1').nodes.line.dispatch('keydown', { key: 'Escape' });
  assert.equal(p.commanders.shownId(), '');
  assert.equal(p.balloonOf('m1').content, null);

  // The map is re-mounted: a new canvas replaces the old one, with the same places.
  const fresh = p.doc.createElement('div');
  fresh.rect = { left: 0, top: 0, width: 1000, height: 700 };
  fresh.querySelector = oldRoot.querySelector;
  p.doc.body.append(fresh);
  oldRoot.remove();
  p.doc.querySelector = selector =>
    selector === '.ws-cmd-detachment-host .ws-map-canvas' ? fresh : null;

  assert.equal(p.commanders.show(detail('m1'), state), true);
  const balloon = p.balloonOf('m1');
  assert.equal(balloon.root, fresh, 'the balloon hangs in the canvas that is on the page');
  assert.equal(balloon.nodes.line.parentElement.parentElement, fresh, 'its line is in that canvas');
  assert.equal(p.doc.activeElement, balloon.nodes.line, 'and has the focus');
  assert.equal(balloon.nodes.line.hidden, false);
});

test('following to the next question keeps focus in the card the owner was in, and leaves it alone otherwise', async () => {
  const p = page();
  p.addTile('m1');
  const mark = p.addMark('m1');
  const outside = p.addMark('other');
  const next = () =>
    waiting('m1', { songs_offer: undefined, goal_offer: { goal: 'Finish the album' } });

  // Focus is inside the card when the answer is pressed: the card is replaced by
  // the next question's, so focus must land in the new card, not fall to the page.
  p.commanders.show(detail('m1'), stateOf(waiting('m1')));
  let balloon = p.balloonOf('m1');
  balloon.expand();
  balloon.focusFirst();
  assert.ok(balloon.nodes.box.contains(p.doc.activeElement), 'focus starts inside the card');
  p.result.value = { view: next() };
  await p.cards.at(-1).options.onAction('learn_songs', null, {});
  assert.equal(p.commanders.shownId(), 'm1', 'it follows: the goal is the next question');
  assert.match(balloon.nodes.lineText.textContent, /suggests a goal/);
  assert.ok(
    balloon.nodes.box.contains(p.doc.activeElement),
    'focus went into the next card, not to the page'
  );

  // Focus was moved elsewhere while the request ran: it is not pulled back.
  p.commanders.closeAll();
  p.commanders.show(detail('m1'), stateOf(waiting('m1')));
  balloon = p.balloonOf('m1');
  balloon.expand();
  balloon.focusFirst();
  outside.focus();
  p.result.value = { view: next() };
  await p.cards.at(-1).options.onAction('learn_songs', null, {});
  assert.equal(p.commanders.shownId(), 'm1');
  assert.equal(p.doc.activeElement, outside, 'focus stayed where the owner put it');
  assert.ok(mark);
});
