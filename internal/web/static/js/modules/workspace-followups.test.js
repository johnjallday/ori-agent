import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  managementView,
  followUpActionFor,
  followUpIDFromSearch,
  focusManagementCard,
  renderManagementPanel,
  renderManagementCard,
  wireWorkspaceFollowUps,
  followUpSummary,
  followUpSummaryText,
  groupFollowUps,
  orderWithinGroup,
  dueChip,
  sourceLine,
  sourceLineText,
  safeNoteLink,
  defaultOpen,
  followUpsOpenKey,
  readFollowUpsOpen,
  writeFollowUpsOpen
} from './workspace-followups.js';
import { followUpView } from './personal-hq-onboarding.js';

// --- a tiny DOM stub: enough for createElement + append + click ------------
function fakeDoc() {
  const make = tag => ({
    tagName: tag,
    id: '',
    className: '',
    textContent: '',
    type: '',
    href: '',
    hidden: false,
    dataset: {},
    children: [],
    listeners: {},
    attributes: {},
    appendChild(c) {
      this.children.push(c);
      return c;
    },
    append(...cs) {
      cs.forEach(c => this.children.push(c));
    },
    addEventListener(ev, fn) {
      this.listeners[ev] = fn;
    },
    setAttribute(name, value) {
      this.attributes[name] = String(value);
    },
    scrollIntoView(options) {
      this.scrolledWith = options;
    },
    focus(options) {
      this.focusedWith = options;
    },
    set innerHTML(v) {
      if (v === '') this.children = [];
    },
    get innerHTML() {
      return '';
    }
  });
  return { createElement: make };
}

function walk(node, visit) {
  if (!node) return;
  visit(node);
  (node.children || []).forEach(c => walk(c, visit));
}

function findAll(node, predicate) {
  const out = [];
  walk(node, n => {
    if (predicate(n)) out.push(n);
  });
  return out;
}

const byClass = cls => n =>
  String(n.className || '')
    .split(/\s+/)
    .includes(cls);
const collectButtons = node => findAll(node, n => n.tagName === 'button');
const textOf = node => {
  let text = '';
  walk(node, n => {
    text += n.textContent || '';
  });
  return text;
};

function fakeStorage(initial = {}) {
  const data = { ...initial };
  return {
    data,
    getItem: key => (key in data ? data[key] : null),
    setItem: (key, value) => {
      data[key] = String(value);
    }
  };
}

// Monday 2 November 2026, mid-afternoon local time.
const NOW = new Date(2026, 10, 2, 15, 0, 0);
const endOfDay = (y, m, d) => new Date(y, m, d, 23, 59, 59).toISOString();

const raw = (id, over = {}) => ({
  id,
  title: `Item ${id}`,
  category: 'i_owe',
  status: 'active',
  source: { type: 'note_item' },
  ...over
});
const views = list => list.map(followUpView);

test('managementView maps items and gates visibility', () => {
  const empty = managementView([]);
  assert.equal(empty.show, false);
  assert.equal(empty.count, 0);

  const v = managementView([
    { id: 'f1', title: 'Reply to landlord', category: 'i_owe', status: 'active' },
    { id: 'f2', title: 'Waiting on quote', category: 'waiting_on', status: 'candidate' }
  ]);
  assert.equal(v.show, true);
  assert.equal(v.count, 2);
  assert.equal(v.items[0].title, 'Reply to landlord');
  assert.equal(v.items[1].isCandidate, true);
});

test('followUpIDFromSearch accepts only one exact bounded deep-link identity', () => {
  assert.equal(followUpIDFromSearch('?follow_up=follow-1'), 'follow-1');
  assert.equal(followUpIDFromSearch('?other=x&follow_up=follow_2'), 'follow_2');
  for (const search of [
    '',
    '?follow_up=',
    '?follow_up=one&follow_up=two',
    '?follow_up=../foreign',
    '?follow_up=%2Fforeign',
    `?follow_up=${'x'.repeat(201)}`
  ]) {
    assert.equal(followUpIDFromSearch(search), '', search);
  }
});

test('followUpActionFor maps kinds to the shared mutation endpoints', () => {
  assert.equal(followUpActionFor('complete').url, '/api/personal-hq/followups/complete');
  assert.equal(followUpActionFor('dismiss').url, '/api/personal-hq/followups/dismiss');
  assert.equal(followUpActionFor('snooze').url, '/api/personal-hq/followups/snooze');
  assert.equal(followUpActionFor('confirm').url, '/api/personal-hq/followups/confirm');
  assert.equal(followUpActionFor('nope'), null);
});

// --- pure view-model --------------------------------------------------------

test('followUpSummary counts open, decisions, this week, overdue and candidates', () => {
  const items = views([
    raw('a', { due_at: endOfDay(2026, 10, 2) }), // today → this week
    raw('b', { due_at: endOfDay(2026, 10, 8) }), // 6 days out → this week
    raw('c', { due_at: endOfDay(2026, 10, 9) }), // 7 days out → neither
    raw('d', { due_at: endOfDay(2026, 9, 30) }), // past → overdue
    raw('e', { category: 'needs_decision' }),
    raw('f', { status: 'candidate' }),
    raw('g', {
      status: 'snoozed',
      snoozed_until: endOfDay(2026, 10, 3),
      due_at: endOfDay(2026, 9, 1)
    }),
    raw('h', { status: 'reopened' }),
    raw('i', { status: 'completed', due_at: endOfDay(2026, 9, 1) })
  ]);
  assert.deepEqual(followUpSummary(items, NOW), {
    open: 8,
    decision: 1,
    dueThisWeek: 2,
    overdue: 1,
    toConfirm: 1
  });
});

test('followUpSummaryText omits zero parts and agrees in number', () => {
  assert.equal(
    followUpSummaryText({ open: 8, decision: 2, dueThisWeek: 3, overdue: 1, toConfirm: 1 }),
    '8 open · 2 need a decision · 3 due this week · 1 overdue · 1 to confirm'
  );
  assert.equal(
    followUpSummaryText({ open: 2, decision: 1, dueThisWeek: 0, overdue: 0, toConfirm: 0 }),
    '2 open · 1 needs a decision'
  );
  assert.equal(
    followUpSummaryText({ open: 0, decision: 0, dueThisWeek: 0, overdue: 0, toConfirm: 0 }),
    ''
  );
});

test('groupFollowUps groups by source, largest first, ties in the fixed order', () => {
  const items = views([
    raw('n1'),
    raw('e1', { source: { type: 'email_thread' } }),
    raw('m1', { source: { type: 'manual' } }),
    raw('n2'),
    raw('j1', { source: { type: 'journal' } }),
    raw('x1', { source: { type: 'personal_assistant_first_assignment' } }),
    raw('e2', { source: { type: 'email_thread' } }),
    raw('u1', { source: null })
  ]);
  const groups = groupFollowUps(items, NOW);
  assert.deepEqual(
    groups.map(g => [g.key, g.label, g.items.length]),
    [
      ['note_item', 'From your notes', 2],
      ['email_thread', 'From email', 2],
      ['other', 'Other', 2],
      ['journal', 'From your journal', 1],
      ['manual', 'Added by you', 1]
    ]
  );
  assert.deepEqual(groupFollowUps([], NOW), []);
});

test('orderWithinGroup: dated (overdue first, soonest next, snoozed by snooze end), then decisions, candidates, recent', () => {
  const items = views([
    raw('recent-old', { updated_at: '2026-10-01T00:00:00Z' }),
    raw('candidate', { status: 'candidate' }),
    raw('due-later', { due_at: endOfDay(2026, 10, 20) }),
    raw('decision', { category: 'needs_decision' }),
    raw('snoozed', { status: 'snoozed', snoozed_until: endOfDay(2026, 10, 4) }),
    raw('overdue', { due_at: endOfDay(2026, 9, 28) }),
    raw('due-soon', { due_at: endOfDay(2026, 10, 3) }),
    raw('recent-new', { updated_at: '2026-11-01T00:00:00Z' })
  ]);
  assert.deepEqual(
    orderWithinGroup(items, NOW).map(v => v.id),
    [
      'overdue',
      'due-soon',
      'snoozed',
      'due-later',
      'decision',
      'candidate',
      'recent-new',
      'recent-old'
    ]
  );
});

test('dueChip reads today, overdue, future and snoozed dates', () => {
  const chip = over => dueChip(followUpView(raw('x', over)), NOW);
  assert.deepEqual(chip({ due_at: endOfDay(2026, 10, 2) }), {
    text: 'Due today',
    tone: 'attention'
  });
  assert.deepEqual(chip({ due_at: endOfDay(2026, 9, 30) }), {
    text: 'Overdue · Oct 30',
    tone: 'attention'
  });
  assert.deepEqual(chip({ due_at: endOfDay(2026, 10, 14) }), {
    text: 'Due Nov 14',
    tone: 'neutral'
  });
  assert.deepEqual(
    chip({ status: 'snoozed', snoozed_until: endOfDay(2026, 10, 5), due_at: endOfDay(2026, 9, 1) }),
    { text: 'Snoozed until Nov 5', tone: 'neutral' }
  );
  assert.equal(chip({}), null);
  assert.equal(chip({ due_at: 'not a date' }), null);
});

test('sourceLine names the note and links it only for obsidian://open?', () => {
  const note = over =>
    sourceLine(
      followUpView(
        raw('n', {
          detail: 'Projects/Album Release.md\nOpen in Obsidian: obsidian://open?vault=Studio',
          presentation: { source_label: 'Studio', source_name: 'Album Release', ...over }
        })
      )
    );
  assert.deepEqual(note({ link: 'obsidian://open?vault=Studio&file=Album%20Release.md' }), {
    name: 'Album Release',
    label: 'Studio',
    href: 'obsidian://open?vault=Studio&file=Album%20Release.md'
  });
  assert.equal(sourceLineText(note({})), 'Album Release · Studio');
  assert.equal(note({ link: '' }).href, '');
  assert.equal(note({ link: 'javascript:alert(1)' }).href, '');
  assert.equal(note({ link: 'obsidian://new?vault=Studio' }).href, '');
  assert.equal(note({ link: 'https://example.com' }).href, '');

  // Without a presentation the client never falls back to parsing the detail.
  const bare = sourceLine(
    followUpView(raw('n2', { detail: 'Inbox.md\nOpen in Obsidian: obsidian://open?vault=X' }))
  );
  assert.deepEqual(bare, { name: '', label: 'Notes', href: '' });
});

test('sourceLine for email, journal, manual and other sources', () => {
  const line = over => sourceLine(followUpView(raw('x', over)));
  assert.equal(
    sourceLineText(
      line({
        source: { type: 'email_thread' },
        counterparty: 'Alex',
        presentation: { source_label: 'Email Ops', source_name: 'Alex Rivera', link: '' }
      })
    ),
    'Alex Rivera · Email Ops'
  );
  assert.equal(
    sourceLineText(line({ source: { type: 'email_thread' }, counterparty: 'Alex' })),
    'Alex · Email Ops'
  );
  assert.equal(sourceLineText(line({ source: { type: 'journal' } })), 'Journal');
  assert.equal(line({ source: { type: 'manual' } }), null);
  assert.equal(line({ source: { type: 'something_else' } }), null);
  assert.equal(safeNoteLink(' obsidian://open?path=%2Fx '), 'obsidian://open?path=%2Fx');
});

test('defaultOpen opens for overdue, due within a week, decisions and candidates only', () => {
  const opens = over => defaultOpen(views([raw('x', over)]), NOW);
  assert.equal(opens({}), false);
  assert.equal(opens({ due_at: endOfDay(2026, 10, 20) }), false);
  assert.equal(opens({ due_at: endOfDay(2026, 10, 8) }), true);
  assert.equal(opens({ due_at: endOfDay(2026, 9, 1) }), true);
  assert.equal(opens({ category: 'needs_decision' }), true);
  assert.equal(opens({ status: 'candidate' }), true);
  assert.equal(opens({ status: 'snoozed', snoozed_until: endOfDay(2026, 10, 3) }), false);
  assert.equal(defaultOpen([], NOW), false);
});

test('collapse memory reads and writes per workspace and forgets when storage refuses', () => {
  const storage = fakeStorage();
  assert.equal(followUpsOpenKey('ws-1'), 'ori:followups-open:ws-1');
  assert.equal(readFollowUpsOpen(storage, 'ws-1'), null);
  writeFollowUpsOpen(storage, 'ws-1', true);
  assert.equal(storage.data['ori:followups-open:ws-1'], 'true');
  assert.equal(readFollowUpsOpen(storage, 'ws-1'), true);
  writeFollowUpsOpen(storage, 'ws-1', false);
  assert.equal(readFollowUpsOpen(storage, 'ws-1'), false);
  assert.equal(readFollowUpsOpen(storage, 'ws-2'), null);

  const refusing = {
    getItem() {
      throw new Error('denied');
    },
    setItem() {
      throw new Error('denied');
    }
  };
  assert.equal(readFollowUpsOpen(refusing, 'ws-1'), null);
  assert.doesNotThrow(() => writeFollowUpsOpen(refusing, 'ws-1', true));
  assert.equal(readFollowUpsOpen(null, 'ws-1'), null);
});

// --- rendering ----------------------------------------------------------------

test('renderManagementCard wires Done to a complete action; candidates get Track/Dismiss', () => {
  const doc = fakeDoc();
  const fired = [];
  const active = renderManagementCard(
    doc,
    { id: 'f1', title: 'X', category: 'You owe', isCandidate: false },
    (kind, v) => fired.push([kind, v.id])
  );
  const btns = collectButtons(active);
  assert.deepEqual(
    btns.map(b => b.textContent),
    ['Done', 'Snooze 1 day', 'Make a ticket']
  );
  btns[0].listeners.click();
  assert.deepEqual(fired, [['complete', 'f1']]);

  const cand = renderManagementCard(
    doc,
    { id: 'f2', title: 'Y', category: 'Waiting on', isCandidate: true },
    () => {}
  );
  assert.deepEqual(
    collectButtons(cand).map(b => b.textContent),
    ['Track this', 'Not a follow-up']
  );
});

test('renderManagementCard shows the due chip and a linked source line, never the raw link', () => {
  const doc = fakeDoc();
  const card = renderManagementCard(
    doc,
    followUpView(
      raw('n1', {
        due_at: endOfDay(2026, 10, 2),
        detail:
          'Projects/Album Release.md\nOpen in Obsidian: obsidian://open?vault=Studio&file=A.md',
        presentation: {
          source_label: 'Studio',
          source_name: 'Album Release',
          link: 'obsidian://open?vault=Studio&file=A.md'
        }
      })
    ),
    () => {},
    NOW
  );
  const due = findAll(card, byClass('workspace-followup-due'))[0];
  assert.equal(due.textContent, 'Due today');
  assert.match(due.className, /is-attention/);
  const link = findAll(card, n => n.tagName === 'a')[0];
  assert.equal(link.textContent, 'Album Release');
  assert.equal(link.href, 'obsidian://open?vault=Studio&file=A.md');
  assert.doesNotMatch(textOf(card), /obsidian:\/\//);
  assert.doesNotMatch(textOf(card), /Open in Obsidian/);
  assert.match(textOf(card), /Album Release · Studio/);

  const manual = renderManagementCard(
    doc,
    followUpView(raw('m1', { source: { type: 'manual' }, detail: 'Back porch' })),
    () => {},
    NOW
  );
  assert.equal(findAll(manual, byClass('workspace-followup-source')).length, 0);
  assert.equal(findAll(manual, byClass('workspace-followup-due')).length, 0);
  assert.match(textOf(manual), /Back porch/);
});

test('focusManagementCard highlights and focuses only an exact rendered card', () => {
  const doc = fakeDoc();
  const card = doc.createElement('div');
  card.className = 'hq-followup-card';
  assert.equal(focusManagementCard(card, 'Waiting for agreement'), true);
  assert.match(card.className, /is-deep-linked/);
  assert.equal(card.tabIndex, -1);
  assert.equal(card.attributes['aria-label'], 'Selected follow-up: Waiting for agreement');
  assert.deepEqual(card.scrolledWith, { block: 'center' });
  assert.deepEqual(card.focusedWith, { preventScroll: true });
  // A second call (after the mount moved) does not stack the class.
  focusManagementCard(card, 'Waiting for agreement');
  assert.equal(card.className.match(/is-deep-linked/g).length, 1);
  assert.equal(focusManagementCard(null), false);
});

test('renderManagementPanel hides when empty and renders nothing (FR5)', () => {
  const doc = fakeDoc();
  const mount = doc.createElement('div');
  renderManagementPanel(doc, mount, managementView([]), () => {});
  assert.equal(mount.hidden, true);
  assert.equal(mount.children.length, 0);
});

test('renderManagementPanel: accessible header, summary, collapse toggle, chips and groups', () => {
  const doc = fakeDoc();
  const mount = doc.createElement('div');
  const toggles = [];
  const filters = [];
  const view = managementView([
    raw('n1', { due_at: endOfDay(2026, 10, 4) }),
    raw('n2'),
    raw('e1', { source: { type: 'email_thread' }, counterparty: 'Alex' })
  ]);
  renderManagementPanel(doc, mount, view, () => {}, '', {
    now: NOW,
    open: false,
    onToggle: open => toggles.push(open),
    onFilter: key => filters.push(key)
  });
  assert.equal(mount.hidden, false);
  const section = mount.children[0];
  assert.equal(section.tagName, 'section');
  assert.equal(section.attributes['aria-labelledby'], 'workspaceFollowupsTitle');
  const title = findAll(section, n => n.tagName === 'h2')[0];
  assert.equal(title.id, 'workspaceFollowupsTitle');
  assert.equal(title.textContent, 'Follow-ups');
  assert.equal(
    findAll(section, byClass('workspace-followups-summary'))[0].textContent,
    '3 open · 1 due this week'
  );

  const toggle = findAll(section, byClass('workspace-followups-toggle'))[0];
  const body = findAll(section, byClass('workspace-followups-body'))[0];
  assert.equal(toggle.attributes['aria-expanded'], 'false');
  assert.equal(toggle.attributes['aria-controls'], body.id);
  assert.equal(body.hidden, true);
  toggle.listeners.click();
  assert.equal(toggle.attributes['aria-expanded'], 'true');
  assert.equal(body.hidden, false);
  assert.deepEqual(toggles, [true]);

  const groupTitles = findAll(section, n => n.tagName === 'h3').map(textOf);
  assert.deepEqual(groupTitles, ['From your notes 2', 'From email 1']);

  const chips = findAll(section, byClass('workspace-followups-chip'));
  assert.deepEqual(
    chips.map(c => c.textContent),
    ['All 3', 'From your notes 2', 'From email 1']
  );
  assert.deepEqual(
    chips.map(c => c.attributes['aria-pressed']),
    ['true', 'false', 'false']
  );
  chips[2].listeners.click();
  assert.deepEqual(
    chips.map(c => c.attributes['aria-pressed']),
    ['false', 'false', 'true']
  );
  const groups = findAll(section, byClass('workspace-followups-group'));
  assert.deepEqual(
    groups.map(g => [g.dataset.group, g.hidden]),
    [
      ['note_item', true],
      ['email_thread', false]
    ]
  );
  assert.deepEqual(filters, ['email_thread']);

  // Reading order: toggle, then chips, then each card's controls.
  const order = findAll(section, n => n.tagName === 'button' || n.tagName === 'a');
  assert.equal(order[0], toggle);
  assert.deepEqual(order.slice(1, 4), chips);
});

test('a chip click on a collapsed panel opens it', () => {
  const doc = fakeDoc();
  const mount = doc.createElement('div');
  renderManagementPanel(doc, mount, managementView([raw('n1')]), () => {}, '', {
    now: NOW,
    open: false
  });
  const body = findAll(mount, byClass('workspace-followups-body'))[0];
  assert.equal(body.hidden, true);
  findAll(mount, byClass('workspace-followups-chip'))[0].listeners.click();
  assert.equal(body.hidden, false);
});

test('wireWorkspaceFollowUps focuses an exact owner-local deep link without mutating, opening the list', async () => {
  const doc = fakeDoc();
  const mount = doc.createElement('div');
  const posted = [];
  const storage = fakeStorage({ 'ori:followups-open:eo-1': 'false' });
  const controller = await wireWorkspaceFollowUps({
    doc,
    workspaceId: 'eo-1',
    mount,
    storage,
    now: () => NOW,
    locationSearch: '?follow_up=f2',
    fetchImpl: async () => ({
      ok: true,
      json: async () => ({
        followups: [
          { id: 'f1', title: 'First', category: 'i_owe', status: 'active' },
          { id: 'f2', title: 'Exact record', category: 'waiting_on', status: 'active' }
        ]
      })
    }),
    postImpl: async (...args) => posted.push(args)
  });
  const cards = findAll(mount, byClass('hq-followup-card'));
  const f1 = cards.find(c => c.dataset.followupId === 'f1');
  const f2 = cards.find(c => c.dataset.followupId === 'f2');
  assert.doesNotMatch(f1.className, /is-deep-linked/);
  assert.match(f2.className, /is-deep-linked/);
  assert.equal(f2.focusedWith.preventScroll, true);
  assert.equal(findAll(mount, byClass('workspace-followups-body'))[0].hidden, false);
  assert.deepEqual(posted, []);
  // The deep link does not overwrite the remembered choice.
  assert.equal(storage.data['ori:followups-open:eo-1'], 'false');

  // The card was focused while the mount had no box (the stub has no
  // getClientRects), so refocus() scrolls it into view once the mount has
  // moved, and only once.
  f2.scrolledWith = null;
  assert.equal(controller.refocus(), true);
  assert.deepEqual(f2.scrolledWith, { block: 'center' });
  f2.scrolledWith = null;
  assert.equal(controller.refocus(), false);
  assert.equal(f2.scrolledWith, null);

  const untouched = doc.createElement('div');
  const none = await wireWorkspaceFollowUps({
    doc,
    workspaceId: 'eo-1',
    mount: untouched,
    storage: fakeStorage(),
    now: () => NOW,
    locationSearch: '?follow_up=missing',
    fetchImpl: async () => ({
      ok: true,
      json: async () => ({
        followups: [{ id: 'f1', title: 'First', category: 'i_owe', status: 'active' }]
      })
    }),
    postImpl: async (...args) => posted.push(args)
  });
  assert.doesNotMatch(
    findAll(untouched, byClass('hq-followup-card'))[0].className,
    /is-deep-linked/
  );
  assert.equal(none.refocus(), false);
  assert.deepEqual(posted, []);
});

test('wireWorkspaceFollowUps: a remembered choice wins over the default, and a toggle is remembered', async () => {
  const doc = fakeDoc();
  const payload = {
    followups: [{ id: 'f1', title: 'Decide', category: 'needs_decision', status: 'active' }]
  };
  const fetchImpl = async () => ({ ok: true, json: async () => payload });

  // Default: a decision opens the list.
  const opened = doc.createElement('div');
  await wireWorkspaceFollowUps({
    doc,
    workspaceId: 'w',
    mount: opened,
    fetchImpl,
    storage: fakeStorage(),
    now: () => NOW
  });
  assert.equal(findAll(opened, byClass('workspace-followups-body'))[0].hidden, false);

  // Remembered "closed" wins.
  const storage = fakeStorage({ 'ori:followups-open:w': 'false' });
  const closed = doc.createElement('div');
  await wireWorkspaceFollowUps({
    doc,
    workspaceId: 'w',
    mount: closed,
    fetchImpl,
    storage,
    now: () => NOW
  });
  assert.equal(findAll(closed, byClass('workspace-followups-body'))[0].hidden, true);

  findAll(closed, byClass('workspace-followups-toggle'))[0].listeners.click();
  assert.equal(storage.data['ori:followups-open:w'], 'true');
});

test('wireWorkspaceFollowUps loads the workspace list and re-fetches after a mutation', async () => {
  const doc = fakeDoc();
  const mount = doc.createElement('div');
  const fetched = [];
  const posted = [];
  let payload = {
    followups: [{ id: 'f1', title: 'Reply to landlord', category: 'i_owe', status: 'active' }]
  };
  const fetchImpl = async url => {
    fetched.push(url);
    return { ok: true, json: async () => payload };
  };
  const postImpl = async (url, body) => {
    posted.push([url, body.id]);
    payload = { followups: [] }; // completing empties the list
    return {};
  };

  await wireWorkspaceFollowUps({
    doc,
    workspaceId: 'eo-1',
    mount,
    fetchImpl,
    postImpl,
    storage: fakeStorage(),
    now: () => NOW
  });
  assert.equal(fetched[0], '/api/workspaces/eo-1/followups');
  assert.equal(mount.hidden, false);

  // Click "Done" → posts complete, then re-fetches (now empty → panel hides).
  const doneBtn = collectButtons(mount).find(b => b.textContent === 'Done');
  await doneBtn.listeners.click();
  assert.deepEqual(posted, [['/api/personal-hq/followups/complete', 'f1']]);
  assert.equal(fetched.length, 2);
  assert.equal(mount.hidden, true);
});

test('wireWorkspaceFollowUps keeps the chosen filter across a re-fetch', async () => {
  const doc = fakeDoc();
  const mount = doc.createElement('div');
  let payload = {
    followups: [
      raw('n1'),
      raw('e1', { source: { type: 'email_thread' } }),
      raw('e2', { source: { type: 'email_thread' } })
    ]
  };
  const fetchImpl = async () => ({ ok: true, json: async () => payload });
  const postImpl = async () => {
    payload = { followups: payload.followups.filter(f => f.id !== 'e1') };
  };
  await wireWorkspaceFollowUps({
    doc,
    workspaceId: 'w',
    mount,
    fetchImpl,
    postImpl,
    storage: fakeStorage(),
    now: () => NOW
  });
  const emailChip = findAll(mount, byClass('workspace-followups-chip')).find(
    c => c.dataset.filter === 'email_thread'
  );
  emailChip.listeners.click();
  const e1Done = collectButtons(
    findAll(mount, byClass('hq-followup-card')).find(c => c.dataset.followupId === 'e1')
  ).find(b => b.textContent === 'Done');
  await e1Done.listeners.click();
  const pressed = findAll(mount, byClass('workspace-followups-chip')).find(
    c => c.attributes['aria-pressed'] === 'true'
  );
  assert.equal(pressed.dataset.filter, 'email_thread');
});
