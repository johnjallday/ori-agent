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
  mime = 'application/json',
  results,
  cancel,
  cancellation,
  managed,
  success
} = {}) {
  return {
    ok: status === 200,
    headers: { get: key => (key === 'Content-Type' ? mime : ready ? '1' : null) },
    json: async () => ({
      // Even a contradictory success flag is not enough.
      success: success ?? state === 'completed',
      cancel,
      operation: {
        id: operationID,
        state,
        intent,
        revision,
        schema_version: 1,
        blockers,
        hq_archive: archive,
        cancellation,
        restart: managed === undefined ? undefined : { managed },
        results: results ?? [
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

const pendingResults = [
  {
    id: 'settings',
    outcome: 'pending',
    checks: [{ name: 'preferences_default', outcome: 'pending' }]
  },
  {
    id: 'agents',
    outcome: 'pending',
    checks: [{ name: 'old_profiles_absent', outcome: 'pending' }]
  }
];
const offer = {
  available: true,
  revision: 3,
  token: 'process-token',
  headline: 'A folder you chose to keep when you reviewed this reset is now empty.',
  detail: 'Ori stopped before changing anything.',
  warning: 'Canceling does not restore anything that was removed outside Ori.'
};
const blockedWithOffer = (extra = {}) =>
  response({
    state: 'blocked',
    ready: false,
    intent: 'start_fresh',
    revision: 3,
    results: pendingResults,
    blockers: [{ message: 'Reset scope changed.', recovery: 'Preserve .ori-reset.' }],
    cancel: offer,
    ...extra
  });
const canceledOperation = ({ managed = false, ready = false, ...extra } = {}) =>
  response({
    state: 'canceled',
    ready,
    intent: 'start_fresh',
    revision: 4,
    results: pendingResults,
    cancellation: { from_revision: 3, prior_blocker: 'recovery_scope_changed' },
    success: false,
    managed,
    ...extra
  });
// The cancel POST's reply shares the harness's response queue.
const cancelReply = ({ status = 200, body } = {}) => ({
  ok: status === 200,
  headers: { get: () => 'application/json' },
  json: async () => {
    if (body instanceof Error) throw body;
    return body;
  }
});
const canceledBody = (managed = false) => ({
  canceled: true,
  operation: {
    id,
    state: 'canceled',
    intent: 'start_fresh',
    revision: 4,
    schema_version: 1,
    blockers: [],
    results: pendingResults,
    restart: { managed },
    cancellation: { from_revision: 3, prior_blocker: 'recovery_scope_changed' }
  }
});

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
    },
    async cancel() {
      element('reset-cancel-button').click();
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

test('an offered cancellation posts the exact revision with local proof and shows canceled, not completed', async () => {
  const h = harness([blockedWithOffer(), cancelReply({ body: canceledBody() })]);
  await h.idle();
  assert.equal(h.element('reset-cancel').hidden, false);
  assert.equal(h.element('reset-cancel-headline').textContent, offer.headline);
  assert.equal(h.element('reset-cancel-warning').textContent, offer.warning);
  assert.equal(h.element('reset-state-title').textContent, 'Reset needs attention');

  await h.cancel();
  const post = h.requests[1];
  assert.equal(post.url, `/api/reset/operations/${id}/cancel`);
  assert.equal(post.options.method, 'POST');
  assert.equal(post.options.mode, 'same-origin');
  assert.equal(post.options.redirect, 'error');
  assert.equal(post.options.headers['X-Requested-With'], 'XMLHttpRequest');
  assert.equal(post.options.headers['X-Ori-Reset-Token'], 'process-token');
  assert.deepEqual(
    JSON.parse(post.options.body),
    { revision: 3 },
    'no path, category or reason is ever sent'
  );
  assert.equal(h.element('reset-cancel').hidden, true);
  assert.equal(h.element('reset-state-title').textContent, 'Reset canceled');
  assert.match(
    h.element('reset-state-message').textContent,
    /Nothing was reset, removed or restored/
  );
  assert.doesNotMatch(h.element('reset-state-title').textContent, /complete/i);
  assert.equal(h.element('reset-results').hidden, true, 'pending categories are not results');
  assert.equal(
    h.element('reset-restart-instructions').hidden,
    false,
    'manual relaunch steps stay visible'
  );
  assert.equal(h.element('reset-enter').hidden, true, 'the recovery host is never an entry point');
  assert.deepEqual(h.applied, []);
});

test('a host that can reopen Ori says so only after the receipt is acknowledged', async () => {
  const h = harness([blockedWithOffer(), cancelReply({ body: canceledBody(true) })]);
  await h.idle();
  assert.notEqual(h.element('reset-state-title').textContent, 'Reopening Ori');
  await h.cancel();
  assert.equal(h.element('reset-state-title').textContent, 'Reopening Ori');
  assert.match(h.element('reset-state-message').textContent, /manual steps below/);
});

test('a refused or unconfirmed cancellation shows feedback, never success, and can be retried', async () => {
  const h = harness([
    blockedWithOffer(),
    cancelReply({
      status: 409,
      body: {
        canceled: false,
        code: 'not_cancelable',
        message: 'Ori can no longer show that this reset changed nothing.'
      }
    }),
    cancelReply({
      status: 503,
      body: { code: 'not_confirmed', message: 'Ori could not confirm that the reset was canceled.' }
    }),
    new Error('connection lost'),
    cancelReply({ body: new Error('not json') }),
    cancelReply({
      body: {
        canceled: true,
        operation: { id: 'another-operation', state: 'canceled', revision: 4 }
      }
    })
  ]);
  await h.idle();
  const attempts = [
    /can no longer show/,
    /could not confirm that the reset was canceled/,
    /Ori could not confirm that the reset was canceled\. Nothing is assumed/,
    /Ori could not confirm that the reset was canceled\. Nothing is assumed/,
    /Ori could not confirm that the reset was canceled\. Nothing is assumed/
  ];
  for (const expected of attempts) {
    await h.cancel();
    assert.equal(h.element('reset-cancel-feedback').hidden, false);
    assert.match(h.element('reset-cancel-feedback').textContent, expected);
    assert.equal(
      h.element('reset-cancel-button').disabled,
      false,
      'the button returns for a retry'
    );
    assert.equal(h.element('reset-cancel').hidden, false);
    assert.equal(
      h.element('reset-state-title').textContent,
      'Reset needs attention',
      'no success was claimed'
    );
  }
});

test('a second click while a cancellation is in flight sends one request', async () => {
  let release;
  const gate = new Promise(resolve => (release = resolve));
  const slow = {
    ok: true,
    headers: { get: () => 'application/json' },
    json: async () => (await gate, canceledBody())
  };
  const h = harness([blockedWithOffer(), slow]);
  await h.idle();
  h.element('reset-cancel-button').click();
  h.element('reset-cancel-button').click();
  h.element('reset-cancel-button').click();
  await h.idle();
  assert.equal(h.requests.filter(request => request.options.method === 'POST').length, 1);
  assert.equal(h.element('reset-cancel-button').disabled, true);
  release();
  await h.idle();
  assert.equal(h.element('reset-state-title').textContent, 'Reset canceled');
});

test('no cancel section for receipts the server did not offer it for', async () => {
  const h = harness([
    blockedWithOffer({ cancel: undefined }),
    blockedWithOffer({ cancel: { ...offer, available: false } }),
    response({ state: 'partial_failure', ready: false, revision: 5, cancel: offer }),
    response({ state: 'awaiting_restart', ready: false, revision: 6, cancel: offer })
  ]);
  await h.idle();
  for (let step = 0; step < 4; step += 1) {
    assert.equal(h.element('reset-cancel').hidden, true, `step ${step}`);
    if (step < 3) await h.tick();
  }
  await h.cancel(); // Nothing is offered, so a stray click sends nothing.
  assert.equal(h.requests.filter(request => request.options.method === 'POST').length, 0);
  assert.equal(h.element('reset-restart-instructions').hidden, false, 'recovery guidance stays');
});

test('a verified canceled receipt from the normal app offers Back to Ori and never the completed-reset path', async () => {
  const h = harness([canceledOperation({ ready: true }), canceledOperation({ ready: true })]);
  await h.idle();
  assert.equal(h.element('reset-state-title').textContent, 'Reset canceled');
  assert.match(h.element('reset-state-message').textContent, /did not run/);
  assert.equal(h.element('reset-enter').hidden, false);
  assert.equal(
    h.element('reset-enter').textContent,
    'Back to Ori',
    'not Continue setup, even though the intent is Start Fresh'
  );
  assert.deepEqual(h.applied, [], 'no first-run or browser cleanup meant for a completed reset');
  await h.enter();
  assert.equal(h.window.location.href, '/');
  assert.deepEqual(h.applied, []);
});

test('a canceled receipt is not ready without the normal app header or with contradictory evidence', async () => {
  const h = harness([
    canceledOperation({ ready: false }),
    canceledOperation({ ready: true, success: true }),
    canceledOperation({ ready: true, cancellation: undefined }),
    canceledOperation({ ready: true, cancellation: { from_revision: 9 } }),
    canceledOperation({ ready: true, blockers: [{ message: 'x', recovery: 'y' }] }),
    canceledOperation({
      ready: true,
      results: [{ id: 'settings', outcome: 'completed', checks: [] }]
    })
  ]);
  await h.idle();
  for (let check = 0; check < 6; check += 1) {
    assert.equal(h.element('reset-enter').hidden, true, `case ${check}`);
    if (check < 5) await h.tick();
  }
  assert.deepEqual(h.applied, []);
});
