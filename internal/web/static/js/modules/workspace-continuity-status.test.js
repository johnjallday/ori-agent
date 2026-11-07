import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

async function harness() {
  const nodes = new Map();
  const get = id => {
    if (!nodes.has(id))
      nodes.set(id, {
        hidden: true,
        dataset: {},
        textContent: '',
        listeners: {},
        addEventListener(name, fn) {
          this.listeners[name] = fn;
        },
        setAttribute() {},
        focus() {},
        close() {}
      });
    return nodes.get(id);
  };
  let payload = { imported: true, background_allowed: false, attachment_version: 3 };
  let failed = false;
  const writes = [];
  const doc = {
    readyState: 'complete',
    body: { dataset: { workspaceId: 'w' }, appendChild() {} },
    getElementById: get,
    querySelector: () => ({ appendChild() {} }),
    dispatchEvent() {}
  };
  const context = vm.createContext({
    document: doc,
    console,
    setTimeout() {},
    CustomEvent: class {},
    fetch: async (_path, options) => {
      if (options.method === 'POST') writes.push(JSON.parse(options.body));
      return { ok: !failed, json: async () => (failed ? {} : { continuity: payload }) };
    },
    window: {
      WorkspaceContinuity: {
        describeStatus: status => ({
          state: 'ready',
          label: 'Ready',
          detail: 'saved',
          imported: status?.imported,
          backgroundAllowed: status?.background_allowed,
          canActivate: status?.imported && !status?.background_allowed
        })
      }
    }
  });
  vm.runInContext(
    readFileSync(new URL('./workspace-continuity-status.js', import.meta.url), 'utf8'),
    context
  );
  await new Promise(resolve => setImmediate(resolve));
  return {
    get,
    api: () => context.window.WorkspaceContinuityStatus,
    payload: value => {
      payload = value;
    },
    fail: () => {
      failed = true;
    },
    writes
  };
}

test('background portability reads preserve same-version consent, invalidate changed versions, and perform no write', async () => {
  const h = await harness();
  h.get('workspaceContinuityActivateBtn').listeners.click();
  assert.equal(h.get('workspaceContinuityConfirm').hidden, false);
  await h.api().refresh();
  assert.equal(h.get('workspaceContinuityConfirm').hidden, false);
  h.payload({ imported: true, background_allowed: false, attachment_version: 4 });
  await h.api().refresh();
  assert.equal(h.get('workspaceContinuityConfirm').hidden, true);
  await h.get('workspaceContinuityConfirmBtn').listeners.click();
  assert.equal(h.writes.length, 0, 'a stale confirmation cannot activate');
});

test('a failed status read replaces stale readiness and revokes pending consent', async () => {
  const h = await harness();
  h.get('workspaceContinuityActivateBtn').listeners.click();
  h.fail();
  await h.api().refresh();
  assert.equal(h.api().view.state, 'unavailable');
  assert.match(h.get('workspaceContinuityChip').textContent, /unavailable/);
  assert.equal(h.get('workspaceContinuityConfirm').hidden, true);
  assert.equal(h.writes.length, 0);
});
