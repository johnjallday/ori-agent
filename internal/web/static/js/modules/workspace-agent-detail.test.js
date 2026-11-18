import { test } from 'node:test';
import assert from 'node:assert/strict';
import { WorkspaceAgentDetailPage } from './workspace-agent-detail.js';

function pageFixture(t) {
  const page = new WorkspaceAgentDetailPage('workspace/uuid', 'Local Manager');
  const nodes = new Map();
  let focused;
  page.el = id => {
    if (!nodes.has(id)) {
      const classes = new Set();
      nodes.set(id, {
        value: '',
        textContent: '',
        innerHTML: '',
        disabled: false,
        addEventListener() {},
        classes,
        classList: {
          add: (...names) => names.forEach(name => classes.add(name)),
          remove: (...names) => names.forEach(name => classes.delete(name)),
          toggle(name, force) {
            const enabled = force ?? !classes.has(name);
            if (enabled) classes.add(name);
            else classes.delete(name);
          }
        },
        focus() {
          focused = id;
        },
        style: { setProperty() {} }
      });
    }
    return nodes.get(id);
  };
  page.promptEditable = true;
  page.currentPrompt = 'Original workspace prompt';
  t.mock.method(globalThis, 'fetch', () => assert.fail('unexpected request'));
  return { page, nodes, focused: () => focused };
}

test('cancel restores the actual Edit door and a reopened editor uses the saved local prompt', t => {
  const { page, focused } = pageFixture(t);
  page.enterEditMode();
  assert.equal(focused(), 'wad-prompt-editor');
  page.el('wad-prompt-editor').value = 'Unsent draft';
  page.exitEditMode();
  assert.equal(focused(), 'wad-prompt-edit');
  assert.equal(page.el('wad-prompt-editor').classes.has('d-none'), true);
  page.enterEditMode();
  assert.equal(page.el('wad-prompt-editor').value, 'Original workspace prompt');
});

test('unavailable workspace-local prompt data is explicitly read-only while refinement remains readable', async t => {
  const { page } = pageFixture(t);
  page.fetchBasePrompt = async () => null;
  page.fetchEffectivePrompt = async () => ({
    refinement: 'Reviewed workspace context',
    note: 'Context only'
  });
  await page.loadPrompt();
  assert.equal(page.promptEditable, false);
  assert.match(page.el('wad-prompt-permission').textContent, /Read-only here/);
  assert.equal(page.el('wad-prompt-edit').classes.has('d-none'), true);
  assert.equal(page.el('wad-refinement').textContent, 'Reviewed workspace context');
});

test('a read-only workspace profile neither enters editing nor sends a save', async t => {
  const { page, focused } = pageFixture(t);
  page.promptEditable = false;
  page.enterEditMode();
  await page.savePrompt();
  assert.equal(focused(), undefined);
});

test('saving keeps the exact workspace-local endpoint and accepted field, restores focus and announces readback', async t => {
  const { page, focused } = pageFixture(t);
  page.enterEditMode();
  page.el('wad-prompt-editor').value = ' Reviewed local prompt ';
  let calls = 0;
  t.mock.method(globalThis, 'fetch', async (url, options) => {
    calls++;
    assert.equal(url, '/api/workspaces/workspace%2Fuuid/agents/Local%20Manager/system-prompt');
    assert.equal(options.method, 'PATCH');
    assert.deepEqual(JSON.parse(options.body), { system_prompt: ' Reviewed local prompt ' });
    return { ok: true, json: async () => ({ system_prompt: 'Reviewed local prompt' }) };
  });
  await page.savePrompt();
  assert.equal(calls, 1);
  assert.equal(page.currentPrompt, 'Reviewed local prompt');
  assert.equal(focused(), 'wad-prompt-edit');
  assert.equal(page.el('wad-prompt-status').textContent, 'Saved.');
  assert.equal(page.el('wad-prompt-save').disabled, false);
});

test('a refused local save keeps the draft and offers Cancel without a library fallback', async t => {
  const { page } = pageFixture(t);
  t.mock.method(console, 'error', () => {});
  page.enterEditMode();
  page.el('wad-prompt-editor').value = 'Keep this refused draft';
  t.mock.method(globalThis, 'fetch', async () => ({
    ok: false,
    status: 403,
    json: async () => ({ error: 'Read only' })
  }));
  await page.savePrompt();
  assert.equal(page.el('wad-prompt-editor').value, 'Keep this refused draft');
  assert.equal(page.el('wad-prompt-editor').classes.has('d-none'), false);
  assert.equal(page.el('wad-prompt-status').classes.has('is-error'), true);
  assert.equal(page.el('wad-prompt-cancel').disabled, false);
  assert.equal(page.currentPrompt, 'Original workspace prompt');
});

test('late canonical portrait repaint never rebuilds the focused prompt draft', t => {
  const { page, focused } = pageFixture(t);
  const previous = globalThis.window;
  t.after(() => {
    if (previous === undefined) delete globalThis.window;
    else globalThis.window = previous;
  });
  globalThis.window = { AgentAvatar: { markup: () => '<svg data-canonical-portrait></svg>' } };
  page.enterEditMode();
  page.el('wad-prompt-editor').value = 'Still typing';
  page.renderIdentity({ name: 'Local Manager', role: 'coordinator', provider: 'fictional' });
  assert.match(page.el('wad-avatar').innerHTML, /data-canonical-portrait/);
  assert.equal(page.el('wad-prompt-editor').value, 'Still typing');
  assert.equal(focused(), 'wad-prompt-editor');
});

test('disabled binding labels retain an explicit accessible state and escape untrusted names', t => {
  const { page } = pageFixture(t);
  page.renderChips(
    'bindings',
    'count',
    [{ name: '<script>', enabled: false }],
    item => ({ label: item.name, enabled: item.enabled }),
    'Empty'
  );
  assert.match(page.el('bindings').innerHTML, /modern-badge is-disabled/);
  assert.match(page.el('bindings').innerHTML, /visually-hidden.*Disabled:/);
  assert.match(page.el('bindings').innerHTML, /&lt;script&gt;/);
});
