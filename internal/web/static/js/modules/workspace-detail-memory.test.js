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
