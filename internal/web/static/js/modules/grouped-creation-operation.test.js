import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  groupedCreationReference,
  savedGroupedCreationReference,
  retainGroupedCreationReference,
  releaseGroupedCreationReference,
  observeGroupedCreation
} from './grouped-creation-operation.js';

const reference = {
  group_review_token: 'original-review',
  idempotency_key: 'original-operation',
  input_digest: 'a'.repeat(64)
};
const complete = () => ({
  group_requirement_operation: {
    state: 'complete',
    workspace_id: 'canonical-child',
    home_workspace_id: 'canonical-home',
    operation_digest: 'b'.repeat(64),
    layout_subject_new: true,
    requires_composite_layout: false
  },
  folder: {
    id: 'canonical-child',
    kind: 'workspace',
    status: 'active',
    parent_id: 'canonical-home',
    name: 'Actual canonical name'
  }
});
const storage = () => {
  const values = new Map();
  return {
    getItem: key => values.get(key) || null,
    setItem: (key, value) => values.set(key, value),
    removeItem: key => values.delete(key)
  };
};

test('grouped status sends only the original lookup witness to its read-only owner', async () => {
  const calls = [];
  const result = await observeGroupedCreation(
    async (url, options) => {
      calls.push({ url, options });
      return { status: 200, json: async () => complete() };
    },
    { ...reference, name: 'Never sent as authority', path: '/not-authority', confirm: true }
  );
  assert.equal(result.state, 'complete');
  assert.equal(result.result.id, 'canonical-child');
  assert.equal(calls.length, 1);
  assert.equal(calls[0].url, '/api/workspaces/group-requirements/observe');
  assert.deepEqual(JSON.parse(calls[0].options.body), reference);
  assert.equal(result.proof.home_workspace_id, 'canonical-home');
});

test('HTTP success, names or unrelated resource shapes never substitute for canonical proof', async () => {
  for (const corrupt of [
    body => {
      delete body.group_requirement_operation.operation_digest;
    },
    body => {
      body.folder.id = 'same-name-unrelated-child';
    },
    body => {
      body.folder.kind = 'group';
    },
    body => {
      delete body.folder.kind;
    },
    body => {
      body.folder.status = 'trashed';
    },
    body => {
      body.group_requirement_operation.layout_subject_new = false;
    }
  ]) {
    const body = complete();
    corrupt(body);
    let calls = 0;
    const result = await observeGroupedCreation(async () => {
      calls++;
      return { status: 200, json: async () => body };
    }, reference);
    assert.equal(result.state, 'outcome_unknown');
    assert.equal(result.result, undefined);
    assert.equal(calls, 1, 'no automatic retry/creation/repair');
  }
});

test('partial and missing grouped operations remain observational and expose no complete resource', async () => {
  for (const status of [202, 404, 503]) {
    const result = await observeGroupedCreation(
      async () => ({
        status,
        json: async () => ({
          group_requirement_operation: {
            state: 'reconcile_required',
            summary: 'Read resumed nothing.'
          }
        })
      }),
      reference
    );
    assert.equal(result.state, 'reconcile_required');
    assert.equal(result.result, undefined);
    assert.match(result.message, /resumed nothing/);
  }
});

test('grouped lookup storage cannot replace or erase a different retained operation', () => {
  const store = storage();
  retainGroupedCreationReference(store, reference, { candidate: { x: 76, y: 228 } });
  assert.deepEqual(savedGroupedCreationReference(store), reference);
  const another = { ...reference, idempotency_key: 'another-operation' };
  assert.throws(() => retainGroupedCreationReference(store, another), /Another grouped creation/);
  releaseGroupedCreationReference(store, another);
  assert.deepEqual(savedGroupedCreationReference(store), reference);
  releaseGroupedCreationReference(store, reference);
  assert.equal(savedGroupedCreationReference(store), null);
  assert.throws(
    () => groupedCreationReference({ ...reference, input_digest: 'not-a-digest' }),
    /unreadable/
  );
});
