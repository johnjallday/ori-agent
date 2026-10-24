import test from 'node:test';
import assert from 'node:assert/strict';
import { createFolderPrerequisites } from './folder-prerequisites.js';

function fixture() {
  let owner = 'generation:offer';
  const calls = [],
    responses = [];
  let changes = 0,
    refreshes = 0,
    sequence = 0;
  const controller = createFolderPrerequisites({
    key: () => owner,
    id: () => `request-${++sequence}`,
    request: async body => {
      calls.push(structuredClone(body));
      const response = responses.shift();
      return typeof response === 'function' ? response() : response;
    },
    changed: () => changes++,
    refreshed: () => refreshes++
  });
  controller.sync(true);
  return {
    controller,
    calls,
    responses,
    setOwner: value => {
      owner = value;
    },
    changes: () => changes,
    refreshes: () => refreshes
  };
}
const reviewed = () => ({
  ok: true,
  status: 200,
  data: {
    prerequisite: {
      kind: 'integration',
      run_id: 'canonical-run',
      revision: 7,
      review_token: 'durable-token',
      action: 'install',
      name: 'Literal <plugin>',
      version: '1.0'
    }
  }
});

test('a card review never installs, confirms, refreshes or chooses another requirement', async () => {
  const f = fixture();
  f.responses.push(reviewed());
  await f.controller.review('integration');
  assert.deepEqual(f.calls, [{ kind: 'integration', request_id: 'request-1' }]);
  assert.equal(f.refreshes(), 0);
  assert.equal(f.controller.active(), true);
  f.controller.cancel();
  assert.equal(f.controller.active(), false);
  assert.equal(f.calls.length, 1);
});

test('explicit confirmation uses the saved canonical envelope then refreshes only readiness', async () => {
  const f = fixture();
  f.responses.push(reviewed(), {
    ok: true,
    status: 200,
    data: { prerequisite: { completed: true } }
  });
  await f.controller.review('integration');
  await f.controller.confirm();
  assert.deepEqual(f.calls[1], {
    kind: 'integration',
    confirm: true,
    request_id: 'request-2',
    run_id: 'canonical-run',
    revision: 7,
    review_token: 'durable-token',
    action: 'install'
  });
  assert.equal(f.refreshes(), 1);
  assert.equal(f.controller.active(), false);
});

test('an indeterminate response retains the exact request ID and consumed review for explicit replay', async () => {
  const f = fixture();
  f.responses.push(
    reviewed(),
    () => {
      throw new Error('lost response');
    },
    { ok: true, status: 200, data: { prerequisite: { completed: true } } }
  );
  await f.controller.review('integration');
  await f.controller.confirm();
  assert.equal(f.controller.view().error, 'lost response');
  f.controller.cancel();
  assert.equal(f.controller.active(), true);
  await f.controller.review('provider');
  assert.equal(f.calls.length, 2);
  await f.controller.confirm();
  assert.deepEqual(f.calls[2], f.calls[1]);
  assert.equal(f.refreshes(), 1);
});

test('a reached server refusal cannot silently re-review or confirm changed consent', async () => {
  const f = fixture();
  f.responses.push(reviewed(), {
    ok: false,
    status: 409,
    data: { error: 'Review the changed package.' }
  });
  await f.controller.review('integration');
  await f.controller.confirm();
  await f.controller.confirm();
  assert.equal(f.calls.length, 2);
  assert.equal(f.controller.view().pending, null);
  assert.equal(f.controller.view().review, null);
  assert.equal(f.refreshes(), 1); // observational refresh only, no new consent
});

test('a delayed disclosure cannot replace another owner or an accepted review', async () => {
  const f = fixture();
  let resolve;
  f.responses.push(
    () =>
      new Promise(done => {
        resolve = done;
      })
  );
  const pending = f.controller.review('integration');
  f.setOwner('new:offer');
  f.controller.sync(true);
  resolve(reviewed());
  await pending;
  assert.equal(f.controller.view().review, null);
  assert.equal(f.controller.active(), false);
  f.controller.sync(false);
  await f.controller.review('provider');
  await f.controller.confirm();
  assert.equal(f.calls.length, 1);
});

test('a completed old-owner request cannot refresh or confirm a newer review', async () => {
  const f = fixture();
  let resolve;
  f.responses.push(
    reviewed(),
    () =>
      new Promise(done => {
        resolve = done;
      })
  );
  await f.controller.review('integration');
  const pending = f.controller.confirm();
  f.setOwner('other:offer');
  f.controller.sync(true);
  resolve({ ok: true, status: 200, data: { prerequisite: { completed: true } } });
  await pending;
  assert.equal(f.refreshes(), 0);
  assert.equal(f.controller.active(), false);
});

test('provider confirmation contains only the host-reviewed version and disclosure witness', async () => {
  const f = fixture();
  f.responses.push(
    {
      ok: true,
      status: 200,
      data: { prerequisite: { kind: 'provider', version: '0.4.0', digest: 'a'.repeat(64) } }
    },
    { ok: true, status: 200, data: { prerequisite: { completed: true } } }
  );
  await f.controller.review('provider');
  await f.controller.confirm();
  assert.deepEqual(f.calls[1], {
    kind: 'provider',
    confirm: true,
    request_id: 'request-2',
    digest: 'a'.repeat(64),
    reviewed_version: '0.4.0'
  });
});

test('structured canonical API guidance survives both preview and confirmation failures', async () => {
  const failure = {
    ok: false,
    status: 409,
    data: {
      code: 'review_stale',
      message: 'This review expired. Review the current package again.'
    }
  };
  const preview = fixture();
  preview.responses.push(failure);
  await preview.controller.review('integration');
  assert.equal(preview.controller.view().error, failure.data.message);
  const confirm = fixture();
  confirm.responses.push(reviewed(), failure);
  await confirm.controller.review('integration');
  await confirm.controller.confirm();
  assert.equal(confirm.controller.view().error, failure.data.message);
  assert.equal(confirm.calls.length, 2);
});

test('malformed or mismatched disclosures do not gain confirmation authority', async () => {
  for (const response of [
    { ok: false, status: 503 },
    { ok: true, status: 200, data: { prerequisite: { kind: 'provider' } } }
  ]) {
    const f = fixture();
    f.responses.push(response);
    await f.controller.review('integration');
    await f.controller.confirm();
    assert.equal(f.calls.length, 1);
    assert.equal(f.controller.view().review, null);
  }
});
