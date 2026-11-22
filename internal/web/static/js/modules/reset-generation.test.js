import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const source = readFileSync(new URL('./reset-generation.js', import.meta.url), 'utf8');
const generation = 'ori.reset.generation';

function storage(values = {}) {
  const map = new Map(Object.entries(values));
  return {
    get length() {
      return map.size;
    },
    key: index => [...map.keys()][index],
    getItem: key => map.get(key) ?? null,
    setItem: (key, value) => map.set(key, value),
    removeItem: key => map.delete(key)
  };
}

function tab({ local = storage(), session = storage(), status = false, disabled = false } = {}) {
  const events = {};
  let reloads = 0;
  const window = {
    get localStorage() {
      if (disabled) throw new Error('blocked');
      return local;
    },
    sessionStorage: session,
    addEventListener: (name, fn) => {
      events[name] = fn;
    },
    location: { pathname: status ? '/reset/status' : '/', reload: () => reloads++ }
  };
  vm.runInNewContext(source, { window }, { filename: 'reset-generation.js' });
  return { events, reloads: () => reloads, apply: window.OriResetGeneration?.apply };
}

test('a verified Start Fresh generation clears stale tab selection and reloads once', () => {
  const local = storage({ [generation]: 'old-operation' });
  const session = storage({
    'ori.reset.tab_generation': 'old-operation',
    currentWorkspaceId: 'stale',
    oriTabId: 'tab'
  });
  const h = tab({ local, session });
  h.events.storage({ key: 'unrelated', newValue: 'new-operation' });
  h.events.storage({ key: generation, newValue: 'old-operation' });
  assert.equal(h.reloads(), 0);
  local.setItem(generation, 'new-operation');
  h.events.storage({ key: generation, newValue: 'new-operation' });
  h.events.pageshow({ persisted: true });
  assert.equal(h.reloads(), 1);
  assert.equal(session.getItem('currentWorkspaceId'), null);
  assert.equal(session.getItem('oriTabId'), 'tab');
});

test('duplicate status tabs do not erase preferences set after the same generation', () => {
  const local = storage({
    'ori-theme': 'dark',
    unrelated: 'keep',
    'ori.reset.operation_id': 'fresh-op'
  });
  const session1 = storage({ 'oriSetupWizardResume:stale': 'stale' });
  const session2 = storage({ currentWorkspaceId: 'stale', unrelated: 'keep' });
  const first = tab({ local, session: session1, status: true });
  const second = tab({ local, session: session2, status: true });
  first.apply('fresh-op');
  assert.equal(local.getItem('ori-theme'), null);
  assert.equal(session1.getItem('oriSetupWizardResume:stale'), null);
  local.setItem('ori-theme', 'light');
  second.apply('fresh-op');
  first.apply('fresh-op');
  assert.equal(local.getItem('ori-theme'), 'light');
  assert.equal(local.getItem(generation), 'fresh-op');
  assert.equal(local.getItem('ori.reset.operation_id'), 'fresh-op');
  assert.equal(local.getItem('unrelated'), 'keep');
  assert.equal(session2.getItem('unrelated'), 'keep');
  assert.equal(session2.getItem('currentWorkspaceId'), null);
  assert.deepEqual(first.events, {});
});

test('unavailable local storage does not prevent tab cleanup', () => {
  const session = storage({ currentWorkspaceId: 'stale' });
  const h = tab({ session, status: true, disabled: true });
  h.apply('new-generation');
  assert.equal(session.getItem('currentWorkspaceId'), null);
});
