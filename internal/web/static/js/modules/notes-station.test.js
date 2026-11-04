import test from 'node:test';
import assert from 'node:assert/strict';

import {
  NOTES_ONE_LINER_CONNECTED,
  NOTES_ONE_LINER_CREATED,
  notesOneLiner,
  mergePeople,
  personMeta,
  previewHTML,
  checklistHTML,
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
  assert.equal(
    notesBadge(created({ written_by_ori: { count: 14 }, fill_in: { user_owned: 2 } }), NOW)
      .description,
    'set up by Ori · 14 written by Ori · 2 are yours now'
  );
  assert.equal(
    notesBadge(created({ written_by_ori: { count: 3 }, fill_in: { user_owned: 1 } }), NOW)
      .description,
    'set up by Ori · 3 written by Ori · 1 is yours now'
  );
  assert.equal(
    notesBadge(created({ written_by_ori: { count: 3 }, fill_in: { user_owned: 0 } }), NOW)
      .description,
    'set up by Ori · last look 2h'
  );
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

test('the panel says what the folder is under its title, by origin', () => {
  assert.equal(
    NOTES_ONE_LINER_CREATED,
    "Ori's notes about you, in a folder you own. Ori writes, you edit, and Ori never reads your own notes back."
  );
  assert.equal(
    NOTES_ONE_LINER_CONNECTED,
    'Your notes, read-only. Ori keeps the open items it found, never your notes.'
  );
  assert.equal(notesOneLiner(created()), NOTES_ONE_LINER_CREATED);
  assert.equal(notesOneLiner(vault()), NOTES_ONE_LINER_CONNECTED);
  assert.equal(notesOneLiner({ status: 'not_connected' }), '');

  const html = notesPanelHTML({
    secondBrain: created(),
    now: NOW,
    panel: { status: 'loading' }
  });
  assert.ok(
    html.includes(
      '</header><p class="ws-panel-one-liner" data-notes-one-liner>Ori&#39;s notes about you, in a folder you own.'
    ),
    html
  );
});

test('Open shows only on notes Ori wrote, and expands them inline as text', () => {
  const inbox = {
    notes: [
      { name: 'ori.md', kind: 'capture', title: 'Call Dana', owned_by_ori: true },
      { name: 'mine.md', kind: 'user', title: 'My own note', owned_by_ori: false }
    ],
    total: 2
  };
  const secondBrain = created({
    today_note: { date: '2026-11-04', state: 'ori', link: 'obsidian://open?path=%2Fx' },
    projects: [
      { name: 'Launch', rel: 'Projects/Launch', path: '/x', ori_note: true },
      { name: 'Album', rel: 'Projects/Album', path: '/y' }
    ]
  });
  const html = tab =>
    notesPanelHTML({
      secondBrain,
      now: NOW,
      panel: { status: 'ready', tab, inbox, look: { state: 'none' } }
    });
  const inboxView = html('inbox');
  assert.ok(inboxView.includes('data-cmd-modal-action="notes-preview" data-cmd-id="inbox:ori.md"'));
  assert.ok(!inboxView.includes('data-cmd-id="inbox:mine.md"'), "the user's note has no Open");
  assert.ok(inboxView.includes('data-cmd-id="daily:2026-11-04.md"'), "today's note when Ori's");
  const more = html('more');
  assert.ok(more.includes('data-cmd-id="project:Launch"'));
  assert.ok(!more.includes('data-cmd-id="project:Album"'), "the user's project has no Open");

  const opened = notesPanelHTML({
    secondBrain,
    now: NOW,
    panel: {
      status: 'ready',
      tab: 'inbox',
      inbox,
      look: { state: 'none' },
      preview: {
        key: 'inbox:ori.md',
        status: 'ready',
        note: {
          name: 'ori.md',
          title: 'Call Dana',
          body: '# Call Dana\n\n<b>about</b> the *tour*\n',
          truncated: true,
          link: 'obsidian://open?path=%2Fx%2FInbox%2Fori.md'
        }
      }
    }
  });
  assert.ok(opened.includes('<p class="ws-notes-preview-text"># Call Dana</p>'));
  assert.ok(opened.includes('&lt;b&gt;about&lt;/b&gt; the *tour*'), 'escaped, never rendered');
  assert.ok(opened.includes('Showing the start of this note.'));
  assert.ok(opened.includes('data-cmd-modal-action="notes-open-folder"'));
  assert.ok(opened.includes('aria-expanded="true"'));
  const refused = previewHTML(
    {
      key: 'project:Launch',
      status: 'error',
      message: 'That note is yours; open it in your editor.'
    },
    'project:Launch'
  );
  assert.ok(refused.includes('role="alert">That note is yours; open it in your editor.</p>'));
  assert.equal(previewHTML({ key: 'other' }, 'project:Launch'), '');
});

test('an empty Inbox of a folder Ori set up shows the first-week checklist', () => {
  const html = notesPanelHTML({
    secondBrain: created({ fill_in: { project_ask: 'skipped' } }),
    now: NOW,
    panel: {
      status: 'ready',
      tab: 'inbox',
      inbox: { notes: [], total: 0 },
      look: { state: 'none' },
      missions: [{ id: 'pa-save-first-journal', status: 'completed' }]
    }
  });
  assert.ok(html.includes('data-notes-checklist'));
  assert.ok(html.includes('data-cmd-modal-action="notes-focus-capture">Capture a note</button>'));
  assert.ok(
    html.includes(
      '<li class="ws-notes-checklist-row is-done" data-checklist-row="journal"><span class="ws-notes-check" aria-hidden="true">✓</span><a class="ws-notes-link" href="/?journal=1">'
    )
  );
  assert.ok(html.includes('href="/?quest=second-brain-project">Tell Ori your project'));
  const answered = checklistHTML({
    show: true,
    rows: [{ id: 'capture', label: 'Capture a note', done: false }]
  });
  assert.ok(!answered.includes('Tell Ori your project'));
  assert.equal(checklistHTML({ show: false, rows: [] }), '');
});

test('the header has Open folder for any connected folder', () => {
  for (const secondBrain of [created(), vault()]) {
    const html = notesPanelHTML({ secondBrain, now: NOW, panel: { status: 'loading' } });
    assert.ok(html.includes('data-cmd-modal-action="notes-open-folder">Open folder</button>'));
  }
  const none = notesPanelHTML({ secondBrain: { status: 'not_connected' }, panel: {} });
  assert.ok(!none.includes('notes-open-folder'));
});

test('People merges the look with the People notes on disk', () => {
  const merged = mergePeople(
    [{ name: 'Dana Lee', mentions: 3, link: '' }],
    [
      { name: 'dana lee', kind: 'seed', link: 'obsidian://open?path=%2Fd' },
      { name: 'Sam Ortiz', kind: 'person', link: 'obsidian://open?path=%2Fs' }
    ]
  );
  assert.deepEqual(
    merged.map(person => [person.name, personMeta(person)]),
    [
      ['Dana Lee', '3 mentions'],
      ['Sam Ortiz', 'A note in People']
    ]
  );
  assert.equal(merged[0].link, 'obsidian://open?path=%2Fd');
  const sections = notesSections({
    secondBrain: created(),
    look: { state: 'none' },
    inbox: { notes: [], total: 0 },
    people: { people: [{ name: 'Sam Ortiz', kind: 'seed' }] }
  });
  assert.equal(sections.find(section => section.key === 'people').count, 1);
  const empty = notesSections({ secondBrain: created(), look: { state: 'none' }, inbox: null });
  assert.equal(
    empty.find(section => section.key === 'people').empty,
    'No people in your notes yet. Approve a person on your Profile and Ori writes one.'
  );
  const connected = notesSections({ secondBrain: vault(), look: { state: 'none' } });
  assert.equal(
    connected.find(section => section.key === 'people').empty,
    'No people found in your notes yet.'
  );
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
          { name: 'b.md', kind: 'user', title: '', modified: hoursAgo(3) },
          { name: 'c.md', kind: 'follow_up', title: 'Renew the lease', modified: hoursAgo(4) }
        ]
      }
    }
  });
  assert.ok(html.includes('Call Dana'));
  assert.ok(html.includes('Captured'));
  assert.ok(html.includes('Follow-up'), 'a follow-up Ori copied is not labelled as the user’s');
  assert.ok(html.includes('Yours'));
  assert.ok(html.includes('b.md'));
  assert.ok(html.includes('Showing the newest 3 of 140.'));
  assert.ok(html.includes('Saved to Inbox as call-dana.'));
});

// --- notes-station-panel (PRD notes-station-panel) ---------------------------

test('a line already a follow-up is handled, offers only Done, and is never suggested', async () => {
  const { isHandled } = await import('./notes-station.js');
  const accepted = item('made', { accepted: true, suggested: true });
  assert.equal(isHandled(accepted), true);
  assert.deepEqual(itemActions(accepted), ['done']);
  assert.equal(suggestedCount([accepted, item('new', { suggested: true })]), 1);
  const groups = openLoopGroups([accepted, item('open')], []);
  assert.deepEqual(
    groups.handled.map(entry => entry.id),
    ['made']
  );
  assert.deepEqual(
    groups.other.map(entry => entry.id),
    ['open']
  );
  const html = notesPanelHTML({
    secondBrain: vault(),
    panel: {
      status: 'ready',
      tab: 'loops',
      handledOpen: true,
      look: { state: 'finished', look_id: 'look-1', result: { items: [accepted, item('open')] } }
    },
    now: NOW
  });
  assert.match(
    html,
    /Item made<\/span><span class="ws-notes-item-meta">Notes\/made · In follow-ups/
  );
});

test('due dates read as chips: upcoming, today and overdue, by the local calendar day', async () => {
  const { dueChip } = await import('./notes-station.js');
  const now = new Date(2026, 10, 2, 9, 0, 0).getTime();
  assert.deepEqual(dueChip('2026-11-02', now), { label: 'Due today', tone: 'today' });
  assert.deepEqual(dueChip('2026-10-03', now), { label: 'Overdue · Oct 3', tone: 'overdue' });
  assert.deepEqual(dueChip('2026-12-25', now), { label: 'Due Dec 25', tone: 'upcoming' });
  assert.equal(dueChip('', now), null);
  assert.equal(dueChip('soon', now), null);
  assert.equal(dueChip('2026-13-01', now), null);
  const html = notesPanelHTML({
    secondBrain: vault(),
    panel: {
      status: 'ready',
      tab: 'loops',
      look: {
        state: 'finished',
        look_id: 'look-1',
        result: { items: [item('rent', { due: '2026-10-03' })] }
      }
    },
    now
  });
  assert.match(html, /<span class="ws-notes-due is-overdue">Overdue · Oct 3<\/span>/);
  assert.doesNotMatch(html, /due 2026-10-03/);
});

test('the footer says what changed, the counts, and what Ori keeps', async () => {
  const { lookCountsLine, sinceLookLine, NOTES_PRIVACY_LINE } = await import('./notes-station.js');
  const result = {
    headline: '12 open items across 6 notes, 4 with dates.',
    finished_at: hoursAgo(3),
    new_since: 2,
    done_since: 1,
    counts: { notes: 40, open_items: 12, notes_with_open: 6, dated: 4, partial: true },
    items: [item('a', { in_decision: true }), item('b', { in_decision: true }), item('c')]
  };
  assert.equal(sinceLookLine(result), 'Since your last look: 2 new, 1 done.');
  assert.equal(sinceLookLine({ new_since: 0, done_since: 0 }), '');
  assert.equal(
    lookCountsLine(result),
    '12 open items in 6 notes · 4 with dates · 2 in decisions · read 40 notes (partial look)'
  );
  assert.equal(
    lookCountsLine({ counts: { notes: 1, open_items: 1, notes_with_open: 1, dated: 0 } }),
    '1 open item in 1 note · 0 with dates · read 1 note'
  );
  const html = notesPanelHTML({
    secondBrain: vault(),
    panel: {
      status: 'ready',
      tab: 'loops',
      look: { state: 'finished', look_id: 'look-1', result }
    },
    now: NOW
  });
  assert.match(html, /Since your last look: 2 new, 1 done\./);
  assert.match(html, /read 40 notes \(partial look\) · last look 3 hours ago/);
  assert.ok(html.includes(NOTES_PRIVACY_LINE));
  const empty = notesPanelHTML({
    secondBrain: vault(),
    panel: { status: 'ready', tab: 'loops', look: { state: 'none' } },
    now: NOW
  });
  assert.ok(!empty.includes(NOTES_PRIVACY_LINE), 'no look, nothing kept to speak of');
});

test('a long look lists its first 30 items and offers Show all', async () => {
  const { MAX_ITEMS_SHOWN } = await import('./notes-station.js');
  const items = Array.from({ length: 34 }, (_, index) => item('n' + index));
  const look = { state: 'finished', look_id: 'look-1', result: { items } };
  const rows = html => (html.match(/<li class="ws-notes-item"/g) || []).length;
  const short = notesPanelHTML({
    secondBrain: vault(),
    panel: { status: 'ready', tab: 'loops', look },
    now: NOW
  });
  assert.equal(rows(short), MAX_ITEMS_SHOWN);
  assert.match(short, /data-cmd-modal-action="notes-show-all">Show all 34<\/button>/);
  const all = notesPanelHTML({
    secondBrain: vault(),
    panel: { status: 'ready', tab: 'loops', look, showAll: true },
    now: NOW
  });
  assert.equal(rows(all), 34);
  assert.doesNotMatch(all, /notes-show-all/);
  const few = notesPanelHTML({
    secondBrain: vault(),
    panel: {
      status: 'ready',
      tab: 'loops',
      look: { state: 'finished', look_id: 'look-1', result: { items: items.slice(0, 30) } }
    },
    now: NOW
  });
  assert.doesNotMatch(few, /notes-show-all/);
});

test('Look again too soon says when Ori looked and how long to wait', async () => {
  const { lookTooSoonLine, LOOK_TOO_SOON_LINE } = await import('./notes-station.js');
  const now = NOW;
  const fourMinutesAgo = new Date(now - 4 * 60000).toISOString();
  assert.equal(
    lookTooSoonLine({ retryAfterSeconds: 361, lastLookAt: fourMinutesAgo, now }),
    'Looked 4 minutes ago. Try again in 7 minutes.'
  );
  assert.equal(
    lookTooSoonLine({ lastLookAt: fourMinutesAgo, now }),
    'Looked 4 minutes ago. Try again in 6 minutes.'
  );
  assert.equal(lookTooSoonLine({ retryAfterSeconds: 30, now }), 'Try again in 1 minute.');
  assert.equal(lookTooSoonLine({ now }), LOOK_TOO_SOON_LINE);
  assert.equal(
    lookTooSoonLine({ lastLookAt: new Date(now - 11 * 60000).toISOString(), now }),
    LOOK_TOO_SOON_LINE
  );
});

test('the badge turns to attention when the notes deserve another look, and says why', async () => {
  const { notesNudge } = await import('./notes-station.js');
  const stale = vault({ nudge: { stale: true, days: 8, came_due: 0 } });
  assert.deepEqual(notesBadge(stale, NOW), {
    value: 'looked 8d ago',
    description: '14 open · looked 8d ago',
    tone: 'attention'
  });
  const due = vault({ nudge: { stale: false, days: 0, came_due: 2 } });
  assert.deepEqual(notesBadge(due, NOW), {
    value: '2 came due',
    description: '14 open · 2 came due',
    tone: 'attention'
  });
  const both = vault({ nudge: { stale: true, days: 9, came_due: 1 } });
  assert.deepEqual(notesBadge(both, NOW), {
    value: '1 came due',
    description: '14 open · 1 came due · looked 9d ago',
    tone: 'attention'
  });
  // Dismissed or fresh: the server sends no nudge, and the badge is calm.
  assert.deepEqual(notesBadge(vault(), NOW), {
    value: '14 open',
    description: 'connected · last look 2h',
    tone: 'clear'
  });
  assert.equal(notesNudge(vault({ nudge: { stale: false, came_due: 0 } })), null);
  const setUp = notesBadge(created({ nudge: { stale: true, days: 8, came_due: 0 } }), NOW);
  assert.deepEqual(setUp, {
    value: '12 in Inbox',
    description: 'set up by Ori · last look 2h · looked 8d ago',
    tone: 'attention'
  });
  // A missing folder keeps its own words.
  assert.equal(
    notesBadge(vault({ status: 'missing', nudge: { stale: true, days: 8 } }), NOW).value,
    "Can't find Second Brain"
  );
});
