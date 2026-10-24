import test from 'node:test';
import assert from 'node:assert/strict';
import { folderOfferView } from './personal-assistant-folder.js';

const { modelRequirementView, requirementsView } = globalThis.OriFolderSetupVisual;
const offer = {
  id: 'offer',
  conversation_id: 'conversation',
  candidate_id: 'candidate',
  verdict: 'project',
  status: 'pending',
  subject: { name: 'Session' },
  capability: { recognized: 'Project', setup_quest_id: 'install_ori_reaper' },
  plan: { digest: 'unchanged', lines: [{ kind: 'workspace', name: 'Creates a workspace' }] }
};

function withModel(model) {
  return { ...offer, plan: { ...offer.plan, visual: { model } } };
}

test('model configuration is not a connection and routes only to the applicable owner', () => {
  assert.equal(modelRequirementView(null), null);
  for (const model of [
    { state: 'not_checked' },
    { state: 'configured', name: 'alone' },
    { state: 'connected' }
  ]) {
    assert.equal(modelRequirementView(model).state, 'not_checked');
  }
  const configured = modelRequirementView({
    state: 'configured',
    provider: 'fixture',
    name: '<literal>',
    settings: 'system'
  });
  assert.equal(configured.status, 'Configured');
  assert.match(configured.detail, /connection not verified/);
  assert.equal(configured.settings, '/settings#system-model');
  const bound = modelRequirementView({
    state: 'missing',
    settings: 'agent',
    agent_name: '界 / literal'
  });
  assert.equal(bound.settings, `/agents/${encodeURIComponent('界 / literal')}`);
  assert.equal(
    modelRequirementView({ state: 'missing', settings: 'https://untrusted' }).settings,
    ''
  );
  assert.equal(
    modelRequirementView({ state: 'missing', settings: 'system' }, { saved: true }).settings,
    ''
  );
});

test('only known missing project configuration replaces setup with model configuration', () => {
  const missing = withModel({ state: 'missing', settings: 'system' });
  const before = JSON.stringify(missing);
  const view = folderOfferView(missing);
  assert.equal(view.actions[0].id, 'model');
  assert.equal(view.actions[0].newTab, true);
  assert.ok(!view.actions.some(action => action.oneCard));
  assert.ok(view.actions.some(action => action.recheck));
  assert.equal(JSON.stringify(missing), before);
  for (const model of [
    undefined,
    { state: 'not_checked' },
    { state: 'configured', provider: 'fixture', name: 'fixture' }
  ]) {
    assert.ok(folderOfferView(withModel(model)).actions.some(action => action.oneCard));
  }
  assert.equal(
    requirementsView({ offer: withModel({ state: 'not_checked' }) }).model.state,
    'not_checked'
  );
});

test('leaving Adjust cannot bypass a known missing model or recheck an accepted guided journey', () => {
  const guided = {
    ...withModel({ state: 'missing', settings: 'system' }),
    status: 'awaiting_outcome'
  };
  const view = folderOfferView(guided);
  assert.equal(view.actions[0].id, 'model');
  assert.ok(!view.actions.some(action => action.oneCard || action.recheck));
  assert.ok(view.actions.some(action => action.id === 'resume'));
});

test('selection/destination/run precedence remains ahead of model configuration', () => {
  const missing = withModel({ state: 'missing', settings: 'system' });
  const changed = folderOfferView({ ...missing, destination_status: 'changed' });
  assert.ok(
    !changed.actions.some(action => action.id === 'model' || action.recheck || action.oneCard)
  );
  const running = folderOfferView({
    ...missing,
    status: 'awaiting_outcome',
    plan: undefined,
    setup: { status: 'running', lines: [] }
  });
  assert.ok(
    !running.actions.some(action => action.recheck || action.id === 'model' || action.oneCard)
  );
});
