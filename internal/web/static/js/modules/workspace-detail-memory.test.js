import { test } from 'node:test';
import assert from 'node:assert/strict';
import { WorkspaceMemoryManager } from './workspace-detail-memory.js';

// Minimal host: escapeHtml mirrors the page helper closely enough for assertions.
function makeManager() {
  const host = {
    workspaceId: 'ws1',
    escapeHtml: text => String(text ?? '').replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
  };
  return new WorkspaceMemoryManager(host);
}

test('entryRowHtml renders type badge, text, and provenance', () => {
  const m = makeManager();
  const html = m.entryRowHtml({ type: 'watch', date: '2026-06-11', provenance: 'run:abc', text: 'baseline ~7 min' }, 0);
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
