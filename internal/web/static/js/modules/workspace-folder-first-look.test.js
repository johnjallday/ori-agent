import assert from 'node:assert/strict';
import test from 'node:test';
import { renderDocumentsLook } from './workspace-folder-first-look.js';

function fixture(t, draft = {}) {
  const elements = new Map();
  const get = name => {
    if (!elements.has(name))
      elements.set(name, {
        focus() {
          this.focused = true;
        }
      });
    return elements.get(name);
  };
  const values = new Map(),
    calls = [];
  let reloads = 0,
    reply = null;
  for (const [name, value] of Object.entries({
    localStorage: {
      getItem: key => values.get(key),
      setItem: (key, v) => values.set(key, v),
      removeItem: key => values.delete(key)
    },
    location: {
      reload() {
        reloads++;
      }
    },
    fetch: async (path, opts) => {
      calls.push([path, opts]);
      if (opts.method === 'POST') throw Error('lost reply');
      return { status: reply ? 200 : 204, json: async () => reply };
    }
  })) {
    const previous = Object.getOwnPropertyDescriptor(globalThis, name);
    Object.defineProperty(globalThis, name, { value, configurable: true });
    t.after(() => {
      if (previous) Object.defineProperty(globalThis, name, previous);
      else delete globalThis[name];
    });
  }
  const d = {
    workspace_id: 'ws',
    owner: 'me',
    manager: { name: 'Renamed manager' },
    brief: 'Metadata only',
    look_link: 'verified',
    ...draft
  };
  const paint = (changes = {}) =>
    renderDocumentsLook({ querySelector: get }, {}, { ...d, ...changes }, () => true);
  paint();
  return {
    get: k => get(`[data-documents-${k}]`),
    calls,
    paint,
    reloadCount: () => reloads,
    setReply: r => (reply = r)
  };
}

test('dismissal is scoped by owner and workspace, never hides a saved look, and does not send a request', t => {
  const f = fixture(t);
  f.get('dismiss').onclick();
  f.paint();
  assert.equal(f.get('action').textContent, 'Show Documents first look');
  f.paint({ owner: 'other' });
  assert.equal(f.get('action').textContent, 'Look around Documents');
  f.paint({ look_state: 'looking' });
  assert.equal(f.get('action').textContent, 'View progress');
  assert.equal(f.get('manager').textContent, 'Renamed manager');
  assert.equal(f.calls.length, 0);
});
test('legacy verification sends only the reviewed token and recovers a lost reply by reading', async t => {
  const f = fixture(t, { verify_folder: 'token', look_link: undefined });
  assert.equal(f.get('action').textContent, 'Verify Documents folder');
  f.setReply({ workspace_id: 'ws', look_link: 'verified' });
  await f.get('action').onclick();
  assert.deepEqual(
    f.calls.map(([path]) => path.split('/').at(-1)),
    ['verify-documents-folder', 'folder-manager-draft']
  );
  assert.deepEqual(JSON.parse(f.calls[0][1].body), { verification: 'token' });
  assert.equal(f.reloadCount(), 1);
});
test('an uncertain start never resubmits and leaves a status action', async t => {
  const f = fixture(t);
  await f.get('action').onclick();
  assert.equal(f.get('action').textContent, 'Reload status');
  await f.get('action').onclick();
  assert.equal(f.calls.length, 2);
  assert.equal(f.reloadCount(), 1);
});
