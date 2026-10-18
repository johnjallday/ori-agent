import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  WorkspaceMemoryManager,
  defaultPromoteCategory,
  memoryEntryKind
} from './workspace-detail-memory.js';

// Minimal host: escapeHtml mirrors the page helper closely enough for assertions.
function makeManager() {
  const host = {
    workspaceId: 'ws1',
    escapeHtml: text =>
      String(text ?? '')
        .replace(/&/g, '&amp;')
        .replace(/</g, '&lt;')
        .replace(/>/g, '&gt;')
  };
  return new WorkspaceMemoryManager(host);
}

test('entryRowHtml renders type badge, text, and provenance', () => {
  const m = makeManager();
  const html = m.entryRowHtml(
    { type: 'watch', date: '2026-06-11', provenance: 'run:abc', text: 'baseline ~7 min' },
    0
  );
  assert.match(html, /workspace-detail-memory-badge-watch/);
  assert.match(html, /baseline ~7 min/);
  assert.match(html, /run:abc · 2026-06-11/);
  assert.match(html, /data-action="edit" data-index="0"/);
  assert.match(html, /data-action="delete" data-index="0"/);
});

test('entryRowHtml escapes hostile text', () => {
  const m = makeManager();
  const html = m.entryRowHtml({ type: 'fact', text: '<img src=x onerror=alert(1)>' }, 0);
  assert.ok(!html.includes('<img src=x'), 'raw HTML must be escaped');
  assert.match(html, /&lt;img/);
});

test('editRowHtml preselects the current type', () => {
  const m = makeManager();
  const html = m.editRowHtml({ type: 'decision', text: 'keep sqlite as cache' }, 2);
  assert.match(html, /<option value="decision" selected>decision<\/option>/);
  assert.match(html, /data-edit-text="2"/);
  assert.match(html, /value="keep sqlite as cache"/);
});

test('renderList shows the teaching empty state when there is nothing', () => {
  const m = makeManager();
  m.elements = { list: { innerHTML: '' } };
  m.entries = [];
  m.unstructured = [];
  m.renderList();
  assert.match(m.elements.list.innerHTML, /workspace-detail-memory-empty/);
  assert.match(m.elements.list.innerHTML, /No memory yet/);
});

test('renderList renders entries and an unstructured group', () => {
  const m = makeManager();
  m.elements = { list: { innerHTML: '' } };
  m.entries = [{ type: 'fact', date: '2026-06-01', provenance: 'user', text: 'alpha' }];
  m.unstructured = ['# Workspace Memory', 'hand note'];
  m.editingIndex = -1;
  m.renderList();
  assert.match(m.elements.list.innerHTML, /alpha/);
  assert.match(m.elements.list.innerHTML, /workspace-detail-memory-unstructured/);
  assert.match(m.elements.list.innerHTML, /hand note/);
});

test('renderList keeps approved assistant learnings read-only and evidence-linked', () => {
  const m = makeManager();
  m.elements = { list: { innerHTML: '' } };
  m.entries = [];
  m.unstructured = [];
  m.managedLearnings = [
    {
      id: 'learning-1',
      type: 'workflow',
      text: 'Keep the preflight concise.',
      confidence: 'high',
      evidence: [{ summary: 'Observed in song A', route: '/workspaces/song-a/task/task-1' }]
    }
  ];
  m.renderList();
  assert.match(m.elements.list.innerHTML, /Approved assistant learnings/);
  assert.match(m.elements.list.innerHTML, /Keep the preflight concise/);
  assert.match(m.elements.list.innerHTML, /href="\/workspaces\/song-a\/task\/task-1"/);
  assert.doesNotMatch(m.elements.list.innerHTML, /data-action="edit"/);
});

test('renderList switches the editing row into an edit form', () => {
  const m = makeManager();
  m.elements = { list: { innerHTML: '' } };
  m.entries = [
    { type: 'fact', text: 'first' },
    { type: 'fact', text: 'second' }
  ];
  m.editingIndex = 1;
  m.renderList();
  // Row 0 stays read-only, row 1 is an edit form.
  assert.match(m.elements.list.innerHTML, /data-action="edit" data-index="0"/);
  assert.match(m.elements.list.innerHTML, /data-edit-text="1"/);
});

test('memoryEntryKind tells reviewed, copied and ordinary entries apart', () => {
  assert.equal(memoryEntryKind({ provenance: 'ori-hq:item:rev' }), 'managed');
  assert.equal(memoryEntryKind({ provenance: 'agent:Scout', managed: true }), 'managed');
  assert.equal(memoryEntryKind({ provenance: 'global:item-1' }), 'global_copy');
  assert.equal(memoryEntryKind({ provenance: 'user' }), 'ordinary');
  assert.equal(defaultPromoteCategory('feedback'), 'how_you_work');
  assert.equal(defaultPromoteCategory('fact'), 'projects');
});

test('an ordinary entry offers Promote to global when global memory is available', () => {
  const m = makeManager();
  m.globalMemory = { available: true, review_href: '/profile#personalHQKnowledge' };
  const html = m.entryRowHtml({ type: 'fact', provenance: 'user', text: 'alpha' }, 3);
  assert.match(html, /data-action="promote" data-index="3">Promote to global/);
  assert.doesNotMatch(html, /aria-disabled/);
});

test('Promote is explained, not hidden, when there is no Personal HQ, and absent in the HQ', () => {
  const m = makeManager();
  m.globalMemory = {
    available: false,
    message: 'Global memory needs a personal assistant with a Personal HQ. Set one up from Home.',
    setup_href: '/'
  };
  const html = m.entryRowHtml({ type: 'fact', provenance: 'user', text: 'alpha' }, 0);
  assert.match(html, /aria-disabled="true"/);
  assert.match(html, /title="Global memory needs a personal assistant with a Personal HQ/);
  m.elements = { list: { innerHTML: '' } };
  m.entries = [{ type: 'fact', provenance: 'user', text: 'alpha' }];
  m.handleListClick({
    target: { closest: () => ({ dataset: { action: 'promote', index: '0' } }) }
  });
  assert.equal(m.promotingIndex, -1);
  assert.match(m.elements.list.innerHTML, /workspace-detail-memory-notice/);
  assert.match(m.elements.list.innerHTML, /Set one up from Home/);
  assert.match(m.elements.list.innerHTML, /href="\/"/);

  m.globalMemory = { available: true, is_personal_hq: true };
  assert.doesNotMatch(
    m.entryRowHtml({ type: 'fact', provenance: 'user', text: 'alpha' }, 0),
    /Promote/
  );
});

test('the promote form edits the wording and defaults the category by type', () => {
  const m = makeManager();
  m.globalMemory = { available: true };
  m.elements = { list: { innerHTML: '' } };
  m.entries = [
    { type: 'feedback', provenance: 'user', text: 'Prefers short replies' },
    { type: 'fact', provenance: 'user', text: 'Masters at -14 LUFS' }
  ];
  m.handleListClick({
    target: { closest: () => ({ dataset: { action: 'promote', index: '0' } }) }
  });
  assert.equal(m.promotingIndex, 0);
  const html = m.elements.list.innerHTML;
  assert.match(html, /data-promote-text="0" maxlength="500" value="Prefers short replies"/);
  assert.match(html, /<option value="how_you_work" selected>How you work<\/option>/);
  assert.match(html, /data-action="promote-confirm" data-index="0"/);
  assert.match(html, /The entry stays in this workspace/);
  assert.match(m.promoteRowHtml(m.entries[1], 1), /<option value="projects" selected>Projects/);
});

test('reviewed global-memory lines are read-only with a Profile link', () => {
  const m = makeManager();
  m.globalMemory = { available: true, is_personal_hq: true, review_href: '//evil.example' };
  const html = m.entryRowHtml(
    { type: 'fact', provenance: 'ori-hq:item:rev', managed: true, text: 'Reviewed fact' },
    0
  );
  assert.match(html, /Global memory \(reviewed\)/);
  assert.match(html, /href="\/profile#personalHQKnowledge"/);
  assert.doesNotMatch(html, /data-action="edit"|data-action="delete"|data-action="promote"/);
});

test('every authoritative payload reaches the Memory station hook', () => {
  const m = makeManager();
  const seen = [];
  m.applyPayload({ entries: [{ type: 'fact', text: 'a' }] });
  m.onPayload = payload => seen.push(payload.entries.length);
  m.applyPayload({
    entries: [
      { type: 'fact', text: 'a' },
      { type: 'watch', text: 'b' }
    ]
  });
  assert.deepEqual(seen, [2]);
  assert.equal(m.entries.length, 2);
});

// The pane is one DOM node moved between hosts (the Memory station's modal
// mounts it), so its listeners are bound once to the node and survive a move;
// the modal's mount path loads it directly rather than through Bootstrap's
// shown.bs.tab lazy-load.
test('the add form and list delegation work after the pane is mounted elsewhere', async () => {
  const originalDocument = globalThis.document;
  const originalFetch = globalThis.fetch;
  const originalWindow = globalThis.window;
  globalThis.window = {};
  const listeners = {};
  const element = id => ({
    id,
    value: '',
    innerHTML: '',
    addEventListener(type, listener) {
      listeners[id + ':' + type] = listener;
    }
  });
  const elements = Object.fromEntries(
    [
      'workspace-detail-config-memory-tab',
      'workspace-detail-memory-meta',
      'workspace-detail-memory-list',
      'workspace-detail-memory-add-form',
      'workspace-detail-memory-add-type',
      'workspace-detail-memory-add-text'
    ].map(id => [id, element(id)])
  );
  globalThis.document = { getElementById: id => elements[id] || null };
  const requests = [];
  globalThis.fetch = async (url, options) => {
    requests.push([options?.method || 'GET', url, options?.body || '']);
    return {
      ok: true,
      json: async () => ({
        entries: [{ type: 'fact', text: 'first', provenance: 'user' }],
        raw_size: 10,
        char_budget: 8000
      })
    };
  };
  try {
    const m = makeManager();
    m.bindEvents();
    // The mount path: load without any tab event.
    await m.load();
    assert.match(elements['workspace-detail-memory-list'].innerHTML, /first/);

    elements['workspace-detail-memory-add-text'].value = 'second';
    elements['workspace-detail-memory-add-type'].value = 'watch';
    listeners['workspace-detail-memory-add-form:submit']({ preventDefault() {} });
    await new Promise(resolve => setTimeout(resolve, 0));
    assert.deepEqual(requests[1], [
      'POST',
      '/api/workspaces/ws1/memory/entries',
      JSON.stringify({ text: 'second', type: 'watch' })
    ]);

    listeners['workspace-detail-memory-list:click']({
      target: { closest: () => ({ dataset: { action: 'edit', index: '0' } }) }
    });
    assert.equal(m.editingIndex, 0);
    assert.match(elements['workspace-detail-memory-list'].innerHTML, /data-edit-text="0"/);
  } finally {
    globalThis.document = originalDocument;
    globalThis.fetch = originalFetch;
    globalThis.window = originalWindow;
  }
});

// In the Memory station's modal, a re-rendered list must not drop focus to
// <body>: that would also take Escape and the focus trap with it.
test('focus stays in the pane when the list re-renders under it', () => {
  const originalDocument = globalThis.document;
  const body = { id: 'body' };
  const focused = [];
  const control = selector => ({ selector, focus: () => focused.push(selector) });
  globalThis.document = { body, activeElement: body };
  try {
    const m = makeManager();
    m.elements = {
      list: { innerHTML: '', querySelector: selector => control(selector) },
      addText: control('add-text')
    };
    m.entries = [{ type: 'fact', provenance: 'user', text: 'alpha' }];
    m.globalMemory = { available: false, message: 'Global memory needs a Personal HQ.' };
    const click = (action, index) =>
      m.handleListClick({
        target: { closest: () => ({ dataset: { action, index: String(index) } }) }
      });

    click('promote', 0);
    assert.equal(focused.pop(), '[data-promote-text="0"]');
    click('edit', 0);
    assert.equal(focused.pop(), '[data-edit-text="0"]');
    click('cancel', 0);
    assert.equal(focused.pop(), '[data-action="edit"][data-index="0"]');

    // Nothing matches: the add input takes focus.
    m.elements.list.querySelector = () => null;
    click('promote-cancel', 0);
    assert.equal(focused.pop(), 'add-text');

    // Focus the user moved elsewhere is left alone.
    globalThis.document.activeElement = { id: 'somewhere-else' };
    click('edit', 0);
    assert.equal(focused.length, 0);
  } finally {
    globalThis.document = originalDocument;
  }
});

test('a copied-from-global entry stays editable but cannot be promoted again', () => {
  const m = makeManager();
  m.globalMemory = { available: true };
  const html = m.entryRowHtml(
    { type: 'fact', date: '2026-10-17', provenance: 'global:item-1', text: 'Copied' },
    0
  );
  assert.match(html, /data-action="edit"/);
  assert.match(html, /data-action="delete"/);
  assert.doesNotMatch(html, /data-action="promote"/);
  assert.match(html, /copied from global · 2026-10-17/);
  assert.match(html, /is-global-copy/);
});
