import test from 'node:test';
import assert from 'node:assert/strict';
import {
  savedCreationReference,
  retainCreationReference,
  releaseCreationReference,
  reviewCreationOperation,
  confirmCreationOperation,
  observeCreationOperation,
  abandonCreationOperation
} from './workspace-creation-operation.js';

const reference = { id: '6d8e342b-91fc-4629-b756-b69b99c3367f', digest: 'a'.repeat(64) };
const operation = state => ({ id: reference.id, review_digest: reference.digest, state });
const folder = {
  id: 'canonical-created-resource',
  name: 'Notebook',
  shared_data: {
    workspace_creation_operation_id: reference.id,
    workspace_creation_operation_digest: reference.digest
  }
};
const response = (body, ok = true) => ({ ok, json: async () => body });
function storage() {
  const values = new Map();
  return {
    getItem: key => values.get(key),
    setItem: (key, value) => values.set(key, value),
    removeItem: key => values.delete(key)
  };
}

test('browser storage retains only the exact opaque reference, never a material draft or assumed receipt', () => {
  const store = storage();
  assert.equal(savedCreationReference(store), null);
  retainCreationReference(store, { ...reference, name: 'Not retained', path: '/not-authority' });
  assert.deepEqual(savedCreationReference(store), reference);
  const other = { ...reference, id: '6d8e342b-91fc-4629-b756-b69b99c3367e' };
  assert.throws(() => retainCreationReference(store, other), /previous creation/);
  assert.equal(releaseCreationReference(store, other), false);
  assert.deepEqual(savedCreationReference(store), reference);
  assert.equal(releaseCreationReference(store, reference), true);
  assert.equal(savedCreationReference(store), null);
});

test('unreadable storage or reference blocks confirmation before any request', async () => {
  let calls = 0;
  const fetcher = async () => {
    calls++;
    return response({});
  };
  await assert.rejects(
    confirmCreationOperation(
      fetcher,
      {
        getItem() {
          throw new Error('Storage denied');
        }
      },
      reference
    ),
    /Storage denied/
  );
  await assert.rejects(
    confirmCreationOperation(fetcher, storage(), {
      id: 'not-an-operation',
      digest: reference.digest
    }),
    /invalid/
  );
  assert.equal(calls, 0);
  assert.throws(() => savedCreationReference({ getItem: () => '{corrupt' }), /unreadable/);
});

test('review submits the existing material request without normalizing away explicit vacancies', async () => {
  const material = { name: 'Notebook', role_staffing: [], existing_agent_names: [], blank: true };
  let sent;
  const received = await reviewCreationOperation(
    async (url, options) => {
      assert.equal(url, '/api/workspaces/create-operations');
      assert.equal(options.method, 'POST');
      sent = JSON.parse(options.body);
      return response({ operation: operation('reviewed') });
    },
    reference.id,
    material
  );
  assert.deepEqual(received, reference);
  assert.deepEqual(sent, { id: reference.id, request: material });
});

test('a refused status retains the exact diagnostic without another confirmation or automatic retry', async () => {
  const refusal = {
    error: 'Review the changed roles.',
    conflict: { type: 'team_readiness', fresh_plan: { revision: 'fresh' } }
  };
  let reads = 0;
  const result = await observeCreationOperation(async (_url, options) => {
    reads++;
    assert.equal(options.method, undefined);
    return response({ operation: operation('refused'), refusal });
  }, reference);
  assert.equal(reads, 1);
  assert.equal(result.state, 'refused');
  assert.equal(result.message, refusal.error);
  assert.deepEqual(result.refusal, refusal);
  assert.equal(result.result, null);
});

test('complete response requires the bound canonical operation and creation provenance, not HTTP success', async () => {
  const result = await confirmCreationOperation(
    async (_url, options) => {
      assert.equal(options.method, 'POST');
      assert.deepEqual(JSON.parse(options.body), { review_digest: reference.digest });
      return response({ creation_operation: operation('complete'), success: true, folder });
    },
    storage(),
    reference
  );
  assert.equal(result.state, 'complete');
  assert.equal(result.result.folder.id, folder.id);
});

test('a lost real-shaped confirmation response only reads status; it never resubmits', async () => {
  const calls = [];
  const result = await confirmCreationOperation(
    async (url, options = {}) => {
      calls.push({ url, method: options.method || 'GET' });
      if (options.method === 'POST') throw new Error('lost delivery');
      return response({ operation: operation('complete'), result: { success: true, folder } });
    },
    storage(),
    reference
  );
  assert.equal(result.state, 'complete');
  assert.deepEqual(
    calls.map(call => call.method),
    ['POST', 'GET']
  );
  assert.ok(calls.every(call => call.url.startsWith('/api/workspaces/create-operations/')));
});

test('malformed/foreign success and a reserved ID cannot unlock retry or become creation proof', async () => {
  for (const body of [
    {},
    { success: true, folder },
    { creation_operation: { ...operation('complete'), id: 'foreign' }, folder },
    { creation_operation: operation('complete'), folder: { id: 'reserved-id' } },
    { creation_operation: operation('complete'), success: true, folder: { ...folder, name: '' } },
    {
      creation_operation: operation('complete'),
      success: true,
      folder: { ...folder, agent_instances: 'malformed' }
    }
  ]) {
    let posts = 0;
    const result = await confirmCreationOperation(
      async (_url, options = {}) => {
        if (options.method === 'POST') {
          posts++;
          return response(body);
        }
        return response({ operation: operation('reviewed') });
      },
      storage(),
      reference
    );
    assert.equal(result.state, 'uncertain');
    assert.equal(result.result, undefined);
    assert.equal(posts, 1);
  }
});

test('partial and unreadable outcomes retain recovery identity and never execute through observation', async () => {
  const store = storage();
  const methods = [];
  const result = await confirmCreationOperation(
    async (_url, options = {}) => {
      methods.push(options.method || 'GET');
      return response(
        { creation_operation: operation('reconciliation_required'), error: 'Partial creation' },
        false
      );
    },
    store,
    reference
  );
  assert.equal(result.state, 'reconciliation_required');
  assert.equal(result.result, null);
  assert.deepEqual(savedCreationReference(store), reference);
  assert.deepEqual(methods, ['POST']);
  const observed = await observeCreationOperation(async (_url, options = {}) => {
    assert.equal(options.method, undefined);
    throw new Error('unreadable');
  }, reference);
  assert.equal(observed.state, 'uncertain');
  assert.deepEqual(savedCreationReference(store), reference);
});

test('abandonment requires the exact unaccepted server outcome and is never implicit continuation', async () => {
  const result = await abandonCreationOperation(async (url, options) => {
    assert.ok(url.endsWith('/abandon'));
    assert.equal(options.method, 'POST');
    assert.equal(options.body, '{}');
    return response({ operation: operation('abandoned') });
  }, reference);
  assert.equal(result.state, 'abandoned');
  await assert.rejects(
    abandonCreationOperation(async () => response({ error: 'Already accepted' }, false), reference),
    /Already accepted/
  );
});
