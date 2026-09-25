import { test } from 'node:test';
import assert from 'node:assert/strict';
import { hqCardView, hqReceiptRows } from './personal-assistant-hq-card.js';

const assistant = state => ({ state, display_name: '<Atlas>' });
const root = { workspace_root: '/workspaces', confirmed: true };

test('HQ proposal is visible only while building HQ, or while showing its receipt', () => {
  assert.equal(hqCardView(assistant('needs_hire'), root).visible, false);
  assert.equal(hqCardView(assistant('needs_hq'), root).visible, true);
  assert.equal(hqCardView(assistant('provisioning_hq'), root).building, true);
  assert.equal(hqCardView(assistant('active'), root).visible, false);
  assert.equal(hqCardView(assistant('active'), root, { receipt: [{}] }).visible, true);
});

test('Build needs the same confirmed root as the HQ form; a failed build can retry', () => {
  const missing = hqCardView(assistant('needs_hq'), { workspace_root: '/suggested' });
  assert.equal(missing.canBuild, false);
  assert.match(missing.status, /Confirm this directory/);
  assert.equal(hqCardView(assistant('needs_hq'), root).canBuild, true);
  assert.equal(hqCardView(assistant('provisioning_hq'), root).canBuild, false);
  assert.equal(hqCardView(assistant('provisioning_hq'), root, { failed: true }).canBuild, true);
});

test('Not now collapses to a one-line Build My HQ action', () => {
  const view = hqCardView(assistant('needs_hq'), root, { collapsed: true });
  assert.equal(view.collapsed, true);
  assert.equal(view.visible, true);
  assert.equal(view.name, '<Atlas>', 'the DOM must insert the name as text, not markup');
});

test('HQ receipt uses only canonical workspace facts, schedule and selected directory', () => {
  assert.deepEqual(
    hqReceiptRows(
      { valid: true, workspace: { name: 'My HQ', folder_slug: 'my hq' } },
      { time: '08:00' },
      '/workspaces'
    ),
    [
      { kind: 'workspace', name: 'My HQ', detail: '', route: '/workspaces/my%20hq' },
      { kind: 'schedule', name: 'Daily Brief at 08:00 on weekdays', detail: '' },
      { kind: 'directory', name: '/workspaces', detail: '' }
    ]
  );
  assert.deepEqual(hqReceiptRows({ valid: false }, {}, ''), []);
});
