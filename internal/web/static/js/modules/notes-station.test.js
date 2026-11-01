import test from 'node:test';
import assert from 'node:assert/strict';

import {
  firstNonEmptySection,
  itemActions,
  lookAgeLine,
  notesBadge,
  notesPanelAvailable,
  defaultNotesTab,
  nextNotesTab,
  notesPanelCount,
  notesTabs,
  notesPanelHTML,
  notesSections,
  openItemCount,
  openLoopGroups,
  safeLink,
  suggestedCount
} from './notes-station.js';

const NOW = Date.parse('2026-11-01T12:00:00Z');
const hoursAgo = hours => new Date(NOW - hours * 3600000).toISOString();

const vault = (fields = {}) => ({
  status: 'connected',
  source: { name: 'Second Brain', origin: 'connected', kind: 'obsidian' },
  last_look: { id: 'look-1', at: hoursAgo(2), open_items: 14, handled: 0 },
  ...fields
});

const created = (fields = {}) => ({
  status: 'connected',
  source: { name: 'My Brain', origin: 'created_by_ori', kind: 'obsidian' },
  last_look: { id: 'look-1', at: hoursAgo(2), open_items: 4, handled: 0 },
  inbox_count: 12,
  today_note: { date: '2026-11-01', state: 'none' },
  projects: [{ name: 'Launch', rel: 'Projects/Launch', path: '/x/Projects/Launch' }],
  ...fields
});

const item = (id, fields = {}) => ({
  id,
  text: 'Item ' + id,
  note: 'Notes/' + id + '.md',
  ...fields
});

test('the panel exists only for a connected folder', () => {
  assert.equal(notesPanelAvailable(vault()), true);
  assert.equal(notesPanelAvailable({ status: 'not_connected' }), false);
  assert.equal(notesPanelAvailable(vault({ status: 'missing' })), false);
  assert.equal(notesPanelAvailable(null), false);
});

test('the badge says what the panel will show', () => {
  assert.deepEqual(notesBadge(vault(), NOW), {
    value: '14 open',
    description: 'connected · last look 2h',
    tone: 'clear'
  });
  assert.deepEqual(notesBadge(created(), NOW), {
    value: '12 in Inbox',
    description: 'set up by Ori · last look 2h',
    tone: 'clear'
  });
  assert.deepEqual(notesBadge(vault({ status: 'missing' }), NOW), {
    value: "Can't find Second Brain",
    description: 'needs attention',
    tone: 'attention'
  });
  assert.equal(notesBadge({ status: 'not_connected' }, NOW).value, 'Not set up');
});

test('the badge counts what triage has handled, and an unread Inbox falls back to the name', () => {
  const handled = vault({ last_look: { at: hoursAgo(50), open_items: 14, handled: 3 } });
  assert.equal(notesBadge(handled, NOW).value, '11 open');
  assert.equal(notesBadge(handled, NOW).description, 'connected · last look 2d');
  assert.equal(notesBadge(vault({ last_look: undefined }), NOW).value, 'Second Brain');
  assert.equal(notesBadge(created({ inbox_count: undefined }), NOW).value, 'My Brain');
  assert.equal(openItemCount({ open_items: 1, handled: 5 }), 0);
});

test('items file once: dated soonest first, then decisions, stale, other, handled', () => {
  const groups = openLoopGroups(
    [
      item('later', { due: '2026-11-20', in_decision: true }),
      item('soon', { due: '2026-11-02' }),
      item('choice', { in_decision: true, note: 'Old.md' }),
      item('old', { note: 'Old.md' }),
      item('plain'),
      item('fin', { acted_as: 'done', due: '2026-11-01' }),
      { id: '', text: 'no id' },
      null
    ],
    [{ note: 'Old.md' }]
  );
  assert.deepEqual(
    groups.dated.map(entry => entry.id),
    ['soon', 'later']
  );
  assert.deepEqual(
    groups.decisions.map(entry => entry.id),
    ['choice']
  );
  assert.deepEqual(
    groups.stale.map(entry => entry.id),
    ['old']
  );
  assert.deepEqual(
    groups.other.map(entry => entry.id),
    ['plain']
  );
  assert.deepEqual(
    groups.handled.map(entry => entry.id),
    ['fin']
  );
});

test('triage actions: seeded has no Follow up, a follow-up can still be finished', () => {
  assert.deepEqual(itemActions(item('a')), ['follow_up', 'done', 'dismiss']);
  assert.deepEqual(itemActions(item('a', { seeded: true })), ['done', 'dismiss']);
  assert.deepEqual(itemActions(item('a', { acted_as: 'follow_up' })), ['done']);
  assert.deepEqual(itemActions(item('a', { acted_as: 'done' })), []);
  assert.deepEqual(itemActions(item('a', { acted_as: 'dismissed' })), []);
});

test('the bulk action counts only what it would still add', () => {
  assert.equal(
    suggestedCount([
      item('a', { suggested: true }),
      item('b', { suggested: true, acted_as: 'dismissed' }),
      item('c', { suggested: true, seeded: true }),
      item('d')
    ]),
    1
  );
});

test('a look a day old says so, a fresh one stays quiet', () => {
  assert.equal(lookAgeLine(hoursAgo(2), NOW), '');
  assert.equal(lookAgeLine(hoursAgo(72), NOW), 'Looked 3 days ago — Look again?');
  assert.equal(lookAgeLine('', NOW), '');
});

test('a connected vault has no Inbox, Today, Capture or Projects', () => {
  const sections = notesSections({ secondBrain: vault(), look: { state: 'finished', result: {} } });
  assert.deepEqual(
    sections.map(section => section.key),
    ['loops', 'people']
  );
});

test('a folder Ori set up has every section in the fixed order', () => {
  const sections = notesSections({
    secondBrain: created(),
    look: { state: 'finished', result: {} },
    inbox: { notes: [], total: 0 }
  });
  assert.deepEqual(
    sections.map(section => section.key),
    ['capture', 'inbox', 'today', 'loops', 'people', 'projects']
  );
});

test('the panel opens on the first section with something in it, never on Capture', () => {
  const look = { state: 'finished', result: { items: [item('a')] } };
  const withInbox = notesSections({
    secondBrain: created(),
    look,
    inbox: { notes: [{ name: 'a.md' }], total: 1 }
  });
  assert.equal(firstNonEmptySection(withInbox), 'inbox');
  const emptyInbox = notesSections({
    secondBrain: created(),
    look,
    inbox: { notes: [], total: 0 }
  });
  assert.equal(firstNonEmptySection(emptyInbox), 'loops');
  assert.equal(firstNonEmptySection(notesSections({ secondBrain: vault(), look })), 'loops');
  assert.equal(firstNonEmptySection(notesSections({ secondBrain: vault() })), '');
});

test('the count chip is Inbox for a folder Ori set up and open items for a vault', () => {
  const look = {
    state: 'finished',
    result: { items: [item('a'), item('b', { acted_as: 'done' })] }
  };
  assert.equal(notesPanelCount(notesSections({ secondBrain: vault(), look })), 1);
  assert.equal(
    notesPanelCount(
      notesSections({ secondBrain: created(), look, inbox: { notes: [], total: 12 } })
    ),
    12
  );
  assert.equal(notesPanelCount(notesSections({ secondBrain: created(), look })), '—');
});

test('the panel escapes what notes say and links only to Obsidian', () => {
  assert.equal(safeLink('javascript:alert(1)'), '');
  assert.equal(safeLink('obsidian://open?vault=a'), 'obsidian://open?vault=a');
  const view = tab =>
    notesPanelHTML({
      secondBrain: vault(),
      now: NOW,
      panel: {
        status: 'ready',
        tab,
        look: {
          state: 'finished',
          look_id: 'look-1',
          result: {
            items: [
              item('x', { text: '<img src=x onerror=alert(1)>', link: 'javascript:alert(1)' })
            ],
            people: [{ name: '<b>Dana</b>', mentions: 2, link: 'obsidian://open?vault=a&file=b' }]
          }
        }
      }
    });
  const loops = view('loops');
  assert.ok(!loops.includes('<img'));
  assert.ok(!loops.includes('javascript:'));
  assert.ok(loops.includes('&lt;img'));
  const people = view('more');
  assert.ok(!people.includes('<b>Dana'));
  assert.ok(people.includes('href="obsidian://open?vault=a&amp;file=b"'));
  assert.ok(people.includes('2 mentions'));
});

test('People & Projects offers Make a workspace', () => {
  const html = notesPanelHTML({
    secondBrain: created(),
    now: NOW,
    panel: { status: 'ready', tab: 'more', inbox: { notes: [], total: 0 }, look: { state: 'none' } }
  });
  assert.ok(html.includes('/?create=1&amp;second_brain_project=Launch'));
});

test('the panel offers triage per item, the bulk action, and says partial and failed', () => {
  const html = notesPanelHTML({
    secondBrain: vault(),
    now: NOW,
    panel: {
      status: 'ready',
      look: {
        state: 'finished',
        look_id: 'look-1',
        result: {
          headline: '2 open items',
          also_noticed: 'Distribution has waited two weeks.',
          finished_at: hoursAgo(72),
          counts: { notes: 3, open_items: 2, partial: true },
          items: [
            item('a', { suggested: true }),
            item('b', { seeded: true }),
            item('c', { acted_as: 'dismissed' })
          ]
        }
      }
    }
  });
  assert.ok(html.includes('data-cmd-modal-action="notes-item-follow_up" data-cmd-id="a"'));
  assert.ok(!html.includes('data-cmd-modal-action="notes-item-follow_up" data-cmd-id="b"'));
  assert.ok(html.includes('Add 1 suggested as follow-ups'));
  assert.ok(html.includes('1 handled'));
  assert.ok(html.includes('Dismissed'));
  assert.ok(html.includes('(partial look)'));
  assert.ok(html.includes('Also noticed: Distribution has waited two weeks.'));
  assert.ok(html.includes('Looked 3 days ago — Look again?'));
  assert.ok(html.includes('notes-connection'));

  const failed = notesPanelHTML({
    secondBrain: vault(),
    panel: { status: 'ready', look: { state: 'failed', result: { failure: "Can't find it." } } }
  });
  assert.ok(failed.includes('Can&#39;t find it.'));
  assert.ok(failed.includes('notes-look-again'));
});

test('a folder Ori set up has three views and a vault has two', () => {
  const look = { state: 'finished', result: { items: [item('a')] } };
  const madeSections = notesSections({
    secondBrain: created(),
    look,
    inbox: { notes: [{ name: 'a.md' }], total: 12 }
  });
  assert.deepEqual(
    notesTabs(madeSections).map(tab => [tab.key, tab.title, tab.count]),
    [
      ['inbox', 'Inbox', 12],
      ['loops', 'Open loops', 1],
      ['more', 'People & Projects', 1]
    ]
  );
  assert.deepEqual(
    notesTabs(madeSections)[0].sections.map(section => section.key),
    ['capture', 'inbox', 'today']
  );
  assert.deepEqual(
    notesTabs(notesSections({ secondBrain: vault(), look })).map(tab => [tab.key, tab.title]),
    [
      ['loops', 'Open loops'],
      ['more', 'People']
    ]
  );
});

test('the panel opens on the view with something in it', () => {
  const look = { state: 'finished', result: { items: [item('a')] } };
  const inbox = total => ({ notes: total ? [{ name: 'a.md' }] : [], total });
  const none = created({ today_note: { state: 'none' } });
  assert.equal(
    defaultNotesTab(notesSections({ secondBrain: none, look, inbox: inbox(1) })),
    'inbox'
  );
  assert.equal(
    defaultNotesTab(notesSections({ secondBrain: none, look, inbox: inbox(0) })),
    'loops'
  );
  // Nothing anywhere: the Inbox view, because its capture box is the way to start.
  assert.equal(
    defaultNotesTab(
      notesSections({
        secondBrain: created({ today_note: { state: 'none' }, projects: [] }),
        look: { state: 'none' },
        inbox: inbox(0)
      })
    ),
    'inbox'
  );
  assert.equal(defaultNotesTab(notesSections({ secondBrain: vault(), look })), 'loops');
});

test('arrow keys wrap around the views', () => {
  const tabs = [{ key: 'inbox' }, { key: 'loops' }, { key: 'more' }];
  assert.equal(nextNotesTab(tabs, 'inbox', 1), 'loops');
  assert.equal(nextNotesTab(tabs, 'more', 1), 'inbox');
  assert.equal(nextNotesTab(tabs, 'inbox', -1), 'more');
});

test('only the chosen view renders, and the footer belongs to Open loops', () => {
  const html = tab =>
    notesPanelHTML({
      secondBrain: created(),
      now: NOW,
      panel: {
        status: 'ready',
        tab,
        inbox: { notes: [], total: 0 },
        look: { state: 'finished', look_id: 'look-1', result: { items: [item('a')] } }
      }
    });
  assert.ok(html('inbox').includes('data-notes-section="capture"'));
  assert.ok(!html('inbox').includes('data-notes-section="loops"'));
  assert.ok(!html('inbox').includes('data-notes-section="footer"'));
  assert.ok(html('loops').includes('data-notes-section="footer"'));
  assert.ok(!html('loops').includes('data-notes-section="capture"'));
  assert.ok(html('more').includes('data-notes-section="projects"'));
  assert.ok(html('loops').includes('id="notes-tab-loops" class="ws-cmd-system-tab is-active"'));
  assert.ok(html('loops').includes('aria-labelledby="notes-tab-loops"'));
  // A view this folder lacks falls back to the first rather than an empty panel.
  assert.ok(html('gone').includes('data-notes-section="capture"'));
});

test('an expanded handled list stays expanded through a repaint', () => {
  const html = handledOpen =>
    notesPanelHTML({
      secondBrain: vault(),
      panel: {
        status: 'ready',
        handledOpen,
        look: { state: 'finished', result: { items: [item('c', { acted_as: 'follow_up' })] } }
      }
    });
  assert.ok(html(true).includes('<details class="ws-notes-handled" open>'));
  assert.ok(html(false).includes('<details class="ws-notes-handled">'));
});

test('Today follows the state, and None leads into the Journal', () => {
  const html = today =>
    notesPanelHTML({
      secondBrain: created({ today_note: today }),
      panel: { status: 'ready', inbox: { notes: [], total: 0 }, look: { state: 'none' } }
    });
  assert.ok(html({ state: 'none' }).includes('href="/?journal=1"'));
  const ori = html({ state: 'ori', link: 'obsidian://open?path=x' });
  assert.ok(ori.includes('Today&#39;s note is saved.'));
  assert.ok(ori.includes('href="obsidian://open?path=x"'));
  assert.ok(
    html({ state: 'user', link: 'obsidian://open?path=x' }).includes('Today&#39;s note is yours.')
  );
});

test('Inbox rows say whose they are, and a bound says it was hit', () => {
  const html = notesPanelHTML({
    secondBrain: created(),
    now: NOW,
    panel: {
      status: 'ready',
      look: { state: 'none' },
      captureReceipt: 'Saved to Inbox as call-dana.',
      inbox: {
        total: 140,
        partial: true,
        notes: [
          {
            name: 'a.md',
            kind: 'capture',
            title: 'Call Dana',
            modified: hoursAgo(1),
            link: 'obsidian://open?path=a'
          },
          { name: 'b.md', kind: 'user', title: '', modified: hoursAgo(3) }
        ]
      }
    }
  });
  assert.ok(html.includes('Call Dana'));
  assert.ok(html.includes('Captured'));
  assert.ok(html.includes('Yours'));
  assert.ok(html.includes('b.md'));
  assert.ok(html.includes('Showing the newest 2 of 140.'));
  assert.ok(html.includes('Saved to Inbox as call-dana.'));
});
