import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const source = readFileSync(new URL('./reset-status.js', import.meta.url), 'utf8');
const id = 'reviewed-operation';

function response({
  state = 'completed',
  ready = true,
  intent = 'selected_data',
  operationID = id,
  revision = 3,
  verified = true,
  blockers = [],
  archive,
  status = 200,
  mime = 'application/json'
} = {}) {
  return {
    ok: status === 200,
    headers: { get: key => (key === 'Content-Type' ? mime : ready ? '1' : null) },
    json: async () => ({
      success: state === 'completed', // Even a contradictory success flag is not enough.
      operation: {
        id: operationID,
        state,
        intent,
        revision,
        schema_version: 1,
        blockers,
        hq_archive: archive,
        results: [
          {
            id: 'setup_steps',
            outcome: 'completed',
            checks: [{ name: 'setup_incomplete', outcome: verified ? 'completed' : 'failed' }]
          }
        ]
      }
    })
  };
}

function harness(responses) {
  const elements = {};
  const node = () => ({
    children: [],
    get childElementCount() {
      return this.children.length;
    },
    append(...items) {
      this.children.push(...items);
    },
    prepend(item) {
      this.children.unshift(item);
    },
    replaceChildren(...items) {
      this.children = items;
    },
    addEventListener(name, fn) {
      this[name] = fn;
    }
  });
  const element = key => (elements[key] ??= node());
  const requests = [];
  const applied = [];
  const timers = new Map();
  const delays = [];
  const window = {
    location: { href: `/reset/status?operation=${id}` },
    OriResetGeneration: { apply: value => applied.push(value) },
    addEventListener(name, fn) {
      this[name] = fn;
    }
  };
  element('reset-enter').hidden = true;
  Object.assign(element('reset-operation-id'), { value: id, dataset: { revision: 2 } });
  element('reset-state-title').textContent = 'Restart required';
  vm.runInNewContext(source, {
    window,
    document: { getElementById: element, createElement: node },
    fetch: async (url, options) => {
      requests.push({ url, options });
      const next = responses.shift();
      if (next instanceof Error) throw next;
      return next;
    },
    AbortSignal,
    setTimeout(fn, delay) {
      delays.push(delay);
      timers.set(delays.length, fn);
      return delays.length;
    },
    clearTimeout(number) {
      timers.delete(number);
    }
  });
  const idle = () => new Promise(resolve => setImmediate(resolve));
  return {
    element,
    window,
    requests,
    applied,
    delays,
    idle,
    async tick() {
      assert.equal(timers.size, 1);
      [...timers.values()][0]();
      await idle();
    },
    async manual() {
      element('reset-check').submit({ preventDefault() {} });
      await idle();
    },
    async enter() {
      element('reset-enter').click();
      await idle();
    }
  };
}

test('downtime keeps the last confirmed state; the same normal app receipt reconnects without navigation', async () => {
  const h = harness([new Error('offline'), new Error('booting'), response(), response()]);
  await h.idle();
  assert.match(h.element('reset-connection').textContent, /Waiting to reconnect/);
  assert.equal(h.element('reset-state-title').textContent, 'Restart required');
  await h.tick();
  assert(h.delays[1] > h.delays[0]);
  await h.manual();
  assert.equal(h.element('reset-state-title').textContent, 'Reset completed');
  assert.equal(h.element('reset-enter').textContent, 'Back to Ori');
  assert.equal(h.window.location.href, `/reset/status?operation=${id}`);
  assert.deepEqual(h.applied, []);
  await h.enter();
  assert.equal(h.window.location.href, '/');
  for (const { url, options } of h.requests) {
    assert.equal(url, `/api/reset/operations/${id}`);
    assert.equal(options.mode, 'same-origin');
    assert.equal(options.redirect, 'error');
    assert.equal(options.cache, 'no-store');
    assert.equal(options.method, undefined);
  }
});

test('recovery-only, unrelated, superseded, and contradictory results never offer entry', async () => {
  const h = harness([
    response({ ready: false }),
    response({ operationID: 'other-operation' }),
    response({ revision: 2 }),
    response({ status: 404 }),
    response({ verified: false }),
    response({ mime: 'text/html' })
  ]);
  await h.idle();
  for (let check = 0; check < 6; check += 1) {
    assert.equal(h.element('reset-enter').hidden, true);
    if (check < 5) await h.tick();
  }
  assert.deepEqual(h.applied, []);
});

test('only verified Start Fresh invokes browser cleanup and offers Continue setup', async () => {
  const h = harness([
    response({ intent: 'start_fresh', ready: false }),
    response({ intent: 'start_fresh' }),
    response({ status: 404 })
  ]);
  await h.idle();
  assert.deepEqual(h.applied, []);
  await h.tick();
  assert.equal(h.element('reset-enter').textContent, 'Continue setup');
  assert.deepEqual(h.applied, [id]);
  await h.enter();
  assert.equal(h.element('reset-enter').hidden, true);
  assert.equal(h.window.location.href, `/reset/status?operation=${id}`);
});

test('archive outcome is independent of reset readiness and shows the exact reviewed path', async () => {
  const destination = '/archive/my-hq<literal>';
  const h = harness([
    response({
      state: 'blocked',
      archive: { outcome: 'failed', destination, message: 'Keep both locations.' }
    }),
    response({
      state: 'partial_failure',
      revision: 4,
      archive: {
        outcome: 'completed',
        destination,
        message: 'Review the folder with Import Folder.'
      }
    }),
    response({
      revision: 5,
      archive: { outcome: 'pending', destination, message: 'Not verified.' }
    }),
    response({ revision: 6, archive: { outcome: 'completed', destination, message: 'Intact.' } })
  ]);
  await h.idle();
  assert.equal(h.element('reset-hq-archive-label').textContent, 'Reviewed destination:');
  assert.equal(h.element('reset-hq-archive-path').textContent, destination);
  assert.equal(h.element('reset-hq-archive-message').textContent, 'Keep both locations.');
  await h.tick();
  assert.equal(h.element('reset-hq-archive-label').textContent, 'Archived at:');
  assert.match(h.element('reset-hq-archive-message').textContent, /Import Folder/);
  assert.equal(h.element('reset-enter').hidden, true, 'archive success is not reset success');
  await h.tick();
  assert.equal(h.element('reset-enter').hidden, true, 'unverified archive cannot claim readiness');
  await h.tick();
  assert.equal(h.element('reset-enter').hidden, false);
});

test('durable partial failure surfaces its blocker literally and does not offer entry', async () => {
  const h = harness([
    response({
      state: 'partial_failure',
      blockers: [{ message: '<script>', recovery: 'Keep the receipt.' }]
    })
  ]);
  await h.idle();
  assert.equal(h.element('reset-enter').hidden, true);
  assert.equal(h.element('reset-state-title').textContent, 'Reset needs attention');
  assert.equal(h.element('reset-blockers').hidden, false);
  assert.equal(h.element('reset-blocker-list').children[0].children[0].textContent, '<script>');
});
