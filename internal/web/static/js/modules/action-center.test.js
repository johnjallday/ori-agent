import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const source = readFileSync(new URL('./action-center.js', import.meta.url), 'utf8');

function makeEl(overrides = {}) {
  return {
    textContent: '',
    innerHTML: '',
    style: {},
    value: '',
    disabled: false,
    appendChild() {},
    addEventListener() {},
    ...overrides
  };
}

// Loads the module with a minimal fake DOM. Deliberately omits
// '#action-center-list' so init()'s auto-wiring/reload() no-ops on load —
// tests drive the exported window.ActionCenter surface directly instead.
function loadActionCenter(overrides = {}) {
  const elements = {
    '#action-center-status': makeEl(),
    '#action-center-status-filter': makeEl({ value: '' }),
    '#action-center-sort': makeEl({ value: 'priority' }),
    '#action-center-filter-banner': makeEl(),
    ...(overrides.elements || {})
  };
  const document = {
    readyState: 'complete',
    addEventListener() {},
    querySelector: sel => elements[sel] || null,
    querySelectorAll: () => []
  };
  const window = { location: { search: overrides.search || '' } };
  const context = {
    console,
    window,
    document,
    URLSearchParams,
    fetch: overrides.fetch || (async () => ({ ok: true, json: async () => ({}) })),
    bootstrap: undefined
  };
  vm.runInNewContext(source, context, { filename: 'action-center.js' });
  return { api: window.ActionCenter, elements };
}

test('rowHTML shows Add to Backlog for a non-planned finding (FR26)', () => {
  const { api } = loadActionCenter();
  const html = api.rowHTML({
    id: 'o1',
    workspace_id: 'ws-1',
    title: 'Brand voice drift',
    status: 'new'
  });
  assert.match(html, /data-action="add-to-backlog"/);
  assert.match(html, />Add to Backlog</);
  assert.ok(!html.includes('View in Backlog'), 'not yet planned, so no linked-item shortcut');
});

test("rowHTML shows a View in Backlog deep link once planned, using Group 5's panel=backlog contract (FR26, 29, 59)", () => {
  const { api } = loadActionCenter();
  const html = api.rowHTML({
    id: 'o1',
    workspace_id: 'ws-1',
    title: 'Brand voice drift',
    status: 'planned',
    linked_task_id: 'task-9',
    linked_workspace_id: 'ws-1'
  });
  assert.ok(
    !html.includes('data-action="add-to-backlog"'),
    'no duplicate-capture affordance once planned'
  );
  assert.match(html, /href="\/workspaces\/ws-1\?panel=backlog&task=task-9"/);
  assert.match(html, />View in Backlog</);
});

test('statusChip labels "planned" distinctly and mutes it like resolved/dismissed', () => {
  const { api } = loadActionCenter();
  assert.match(api.statusChip('planned'), />Planned</);
  assert.match(api.statusChip('planned'), /opacity: 0\.6/);
  assert.match(api.statusChip('new'), /opacity: 1/);
});

test('handleAddToBacklog shows a pending state on the clicked button while the request is in flight', async () => {
  let resolveFetch;
  const pending = new Promise(resolve => {
    resolveFetch = resolve;
  });
  const { api, elements } = loadActionCenter({
    fetch: async () => {
      await pending;
      return {
        ok: true,
        json: async () => ({ status: 'planned', item: { id: 'task-9', workspace_id: 'ws-1' } })
      };
    }
  });
  const button = makeEl({ textContent: 'Add to Backlog' });

  const done = api.handleAddToBacklog('ws-1', 'o1', button);
  assert.equal(button.disabled, true, 'button disabled while in flight');
  assert.equal(button.textContent, 'Adding…');

  resolveFetch();
  await done;
  assert.equal(elements['#action-center-status'].innerHTML.includes('Added to backlog.'), true);
  assert.match(
    elements['#action-center-status'].innerHTML,
    /href="\/workspaces\/ws-1\?panel=backlog&task=task-9"/
  );
  assert.match(elements['#action-center-status'].innerHTML, />Open item</);
});

test('handleAddToBacklog re-enables the button and shows an error status on failure', async () => {
  const { api, elements } = loadActionCenter({
    fetch: async () => ({ ok: false, json: async () => ({ message: 'workspace is trashed' }) })
  });
  const button = makeEl({ textContent: 'Add to Backlog' });

  await api.handleAddToBacklog('ws-1', 'o1', button);

  assert.equal(button.disabled, false, 'button re-enabled after failure');
  assert.equal(button.textContent, 'Add to Backlog', 'label restored after failure');
  assert.match(
    elements['#action-center-status'].textContent,
    /Add to Backlog failed: workspace is trashed/
  );
});

test('handleAddToBacklog posts to the add-to-backlog endpoint, not resolve/dismiss', async () => {
  const calls = [];
  const { api } = loadActionCenter({
    fetch: async (url, options) => {
      calls.push({ url, method: options?.method });
      return { ok: true, json: async () => ({ item: { id: 'task-9', workspace_id: 'ws-1' } }) };
    }
  });

  await api.handleAddToBacklog('ws-1', 'o1', makeEl());

  // The first call is the mutation itself; reload()'s own GET refresh follows.
  assert.equal(calls[0].url, '/api/action-center/opportunities/ws-1/o1/add-to-backlog');
  assert.equal(calls[0].method, 'POST');
});
