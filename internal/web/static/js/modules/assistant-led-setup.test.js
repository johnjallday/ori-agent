import test from 'node:test';
import assert from 'node:assert/strict';

import {
  assistantSetupEligible,
  assistantSetupMilestoneView,
  assistantSetupMilestoneViews,
  assistantSetupModelLabel,
  assistantSetupRequest,
  safeAssistantSetupRoute
} from './assistant-led-setup.js';

test('assistant setup eligibility includes pre-HQ and explicit paused without making paused proactive', () => {
  for (const state of ['needs_hq', 'provisioning_hq', 'active']) {
    assert.equal(assistantSetupEligible(state), true, state);
    assert.equal(assistantSetupEligible(state, { explicit: true }), true, state);
  }
  assert.equal(assistantSetupEligible('paused'), false);
  assert.equal(assistantSetupEligible('paused', { explicit: true }), true);
  for (const state of ['needs_hire', 'hiring', 'repair_needed', 'unavailable', '']) {
    assert.equal(assistantSetupEligible(state), false, state);
    assert.equal(assistantSetupEligible(state, { explicit: true }), false, state);
  }
});

test('team copy reports exact create or reuse and model availability', () => {
  assert.equal(
    assistantSetupModelLabel({ action: 'create', name: 'File Curator', model: '' }),
    'Create File Curator · Configured; chat needs a model'
  );
  assert.equal(
    assistantSetupModelLabel({ action: 'reuse', name: 'My Curator', model: 'gpt-test' }),
    'Reuse My Curator · gpt-test'
  );
});

test('milestone view keys on the server id and maps every status to a label', () => {
  const view = assistantSetupMilestoneView({
    id: 'role:file-curator',
    kind: 'agent_role',
    name: 'Renamed Curator',
    status: 'created',
    needs_model: true,
    resource_id: 'instance-1'
  });
  assert.equal(view.key, 'role:file-curator');
  assert.equal(view.name, 'Renamed Curator');
  assert.equal(view.statusLabel, 'Created');
  assert.deepEqual(view.details, ['Configured; chat needs a model']);
  assert.equal(view.resourceID, 'instance-1');
  const labels = [
    'pending',
    'creating',
    'reusing',
    'created',
    'reused',
    'failed',
    'needs_review'
  ].map(status => assistantSetupMilestoneView({ id: 'workspace', status }).statusLabel);
  assert.deepEqual(labels, [
    'Waiting',
    'Creating',
    'Reusing',
    'Created',
    'Reused',
    'Failed',
    'Needs review'
  ]);
});

test('an unknown milestone status is never shown as success', () => {
  for (const status of ['done', 'ok', '', undefined, 'constructor', '__proto__']) {
    const view = assistantSetupMilestoneView({ id: 'workspace', status });
    assert.equal(view.status, 'needs_review', String(status));
    assert.equal(view.statusLabel, 'Needs review', String(status));
  }
});

test('milestone views are list-driven for several roles and keep long names intact', () => {
  const long = 'A very long reviewed role name '.repeat(12).trim();
  const views = assistantSetupMilestoneViews([
    { id: 'workspace', kind: 'workspace', name: 'File Janitor', status: 'created' },
    { id: 'role:one', kind: 'agent_role', name: long, status: 'reused' },
    { id: 'role:two', kind: 'agent_role', name: 'Second', status: 'pending' }
  ]);
  assert.deepEqual(
    views.map(view => view.key),
    ['workspace', 'role:one', 'role:two']
  );
  assert.equal(views[1].name, long);
  assert.equal(views[1].statusLabel, 'Reused');
  assert.deepEqual(assistantSetupMilestoneViews(undefined), []);
});

test('mutation requests carry only closed revisions and server-selected target', () => {
  const projection = {
    proposal: { revision: 'review', selected_workspace_id: 'workspace-1' },
    run: { id: 'run-1', revision: 4 }
  };
  assert.deepEqual(assistantSetupRequest('accept', projection), {
    url: '/api/personal-assistant/setup/file-janitor/accept',
    body: { proposal_revision: 'review', target_workspace_id: 'workspace-1' }
  });
  assert.deepEqual(assistantSetupRequest('finish_later', projection), {
    url: '/api/personal-assistant/setup/file-janitor/runs/run-1/defer',
    body: { if_version: 4 }
  });
  assert.deepEqual(assistantSetupRequest('manual_takeover', projection), {
    url: '/api/personal-assistant/setup/file-janitor/runs/run-1/defer',
    body: { if_version: 4 }
  });
  assert.deepEqual(assistantSetupRequest('resume', projection), {
    url: '/api/personal-assistant/setup/file-janitor/runs/run-1/resume',
    body: { if_version: 4 }
  });
  projection.monitoring_review = { revision: 'monitoring-review' };
  assert.deepEqual(assistantSetupRequest('prepare_review', projection), {
    url: '/api/personal-assistant/setup/file-janitor/runs/run-1/prepare-review',
    body: { if_version: 4, review_revision: 'monitoring-review' }
  });
  assert.equal(assistantSetupRequest('choose_folder', projection), null);
});

test('server routes are limited to workspace destinations', () => {
  assert.equal(
    safeAssistantSetupRoute('/workspaces/file-janitor?panel=file-janitor'),
    '/workspaces/file-janitor?panel=file-janitor'
  );
  assert.equal(
    safeAssistantSetupRoute('/workspaces/new?template=file-janitor'),
    '/workspaces/new?template=file-janitor'
  );
  assert.equal(safeAssistantSetupRoute('https://example.com'), '');
  assert.equal(safeAssistantSetupRoute('javascript:alert(1)'), '');
  assert.equal(safeAssistantSetupRoute('/workspaces/%2e%2e/settings'), '');
  assert.equal(safeAssistantSetupRoute('/workspaces/one#outside'), '');
  assert.equal(safeAssistantSetupRoute('/workspaces\\outside'), '');
});
