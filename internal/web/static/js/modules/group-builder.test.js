import test from 'node:test';
import assert from 'node:assert/strict';
import { groupBuildState } from './group-builder.js';

const fresh = () => ({
  receipts: {},
  steps: [
    {
      kind: 'project_connect',
      preparation: { exists: false },
      actions: [{ id: 'review_create_group' }]
    }
  ]
});

test('only a current canonical group action enables the guided builder', () => {
  assert.equal(groupBuildState(fresh()), 'create');
  const blocked = fresh();
  blocked.steps[0].actions = [];
  assert.equal(groupBuildState(blocked), 'unavailable');
  blocked.steps[0].preparation = null;
  assert.equal(groupBuildState(blocked), 'unavailable');
});
test('historical project or Home receipts never authorize a replacement group', () => {
  for (const field of ['home_workspace_id', 'project_workspace_id']) {
    const journey = fresh();
    journey.receipts[field] = 'historical-id';
    assert.equal(groupBuildState(journey), 'unavailable');
  }
});
test('existing canonical groups are reused and active operations cannot start a group', () => {
  const journey = fresh();
  journey.busy = {};
  assert.equal(groupBuildState(journey), 'busy');
  journey.steps[0].preparation.exists = true;
  assert.equal(groupBuildState(journey), 'existing');
});
