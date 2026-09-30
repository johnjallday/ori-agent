import test from 'node:test';
import assert from 'node:assert/strict';

import {
  AssistantProgramPage,
  assistantProviderUnavailableMessage,
  providerUpgradeAgentLine,
  providerUpgradeCard,
  requestedUpgradeReview
} from './assistant-program.js';

test('split provider availability names only the affected owner', () => {
  assert.match(
    assistantProviderUnavailableMessage({
      home_provider_available: false,
      project_provider_available: true
    }),
    /Home provider.*project-local/s
  );
  assert.match(
    assistantProviderUnavailableMessage({
      home_provider_available: true,
      project_provider_available: false
    }),
    /project provider.*Home remains available/s
  );
  assert.equal(
    assistantProviderUnavailableMessage({}, { disabled_message: 'Provider unavailable.' }),
    'Provider unavailable.'
  );
});

test('assistant page keeps UUID APIs separate from slug navigation', async () => {
  const requests = [];
  const page = new AssistantProgramPage({
    workspaceId: 'workspace-uuid',
    workspaceSlug: 'neon-song',
    fetchImpl: async (url, options) => {
      requests.push({ url, options });
      return { ok: true, json: async () => ({ available: true, hired: false }) };
    }
  });

  assert.equal(page.workspaceURL(), '/workspaces/neon-song');
  assert.equal(page.apiURL('/hire'), '/api/workspaces/workspace-uuid/assistant-program/hire');
  await page.request('/hire', { method: 'POST', body: '{}' });
  assert.equal(requests[0].url, '/api/workspaces/workspace-uuid/assistant-program/hire');
  assert.equal(requests[0].options.method, 'POST');
  assert.equal(requests[0].options.headers['Content-Type'], 'application/json');
});

test('completed Home removal returns to the workspace map instead of the deleted Home', async () => {
  const requests = [];
  const destinations = [];
  const page = new AssistantProgramPage({
    workspaceId: 'home-uuid',
    workspaceSlug: 'music-production-home',
    fetchImpl: async (url, options) => {
      requests.push({ url, options });
      return { ok: true, json: async () => ({ success: true }) };
    },
    navigateImpl: url => destinations.push(url)
  });

  await page.commitHomeRemoval('review-token');

  assert.equal(requests[0].url, '/api/workspaces/home-uuid/assistant-program/remove-home/commit');
  assert.equal(requests[0].options.method, 'POST');
  assert.deepEqual(JSON.parse(requests[0].options.body), { token: 'review-token' });
  assert.deepEqual(destinations, ['/']);
});

test('assistant route presents the declaration-named optional team home', () => {
  const page = new AssistantProgramPage({ workspaceId: 'workspace-uuid', workspaceSlug: 'song' });
  assert.equal(
    page.homeName({ declaration: { station_name: 'Producer Home' }, primary_name: 'June' }),
    'Producer Home'
  );
  assert.equal(page.homeName({}), 'Team Home');
});

test('assistant page surfaces server conflict messages', async () => {
  const page = new AssistantProgramPage({
    workspaceId: 'workspace-uuid',
    workspaceSlug: 'neon-song',
    fetchImpl: async () => ({
      ok: false,
      status: 409,
      json: async () => ({ error: 'Assistant program changed; reload and try again' })
    })
  });
  await assert.rejects(() => page.request('/hire', { method: 'POST' }), /reload and try again/);
});

test('assistant page surfaces coded upgrade refusals with their operation', async () => {
  const page = new AssistantProgramPage({
    workspaceId: 'workspace-uuid',
    workspaceSlug: 'music-home',
    fetchImpl: async () => ({
      ok: false,
      status: 409,
      json: async () => ({
        code: 'home_upgrade_reconcile_required',
        message: 'The upgrade stopped partway and needs attention.',
        details: { id: 'op-1', status: 'reconcile_required' }
      })
    })
  });
  await assert.rejects(
    () => page.request('/provider-upgrade/commit', { method: 'POST', body: '{}' }),
    error =>
      /stopped partway/.test(error.message) &&
      error.code === 'home_upgrade_reconcile_required' &&
      error.details.id === 'op-1'
  );
});

test('package upgrade card offers a review only for an available release', () => {
  assert.equal(providerUpgradeCard(null).hidden, true);
  assert.equal(
    providerUpgradeCard({ plugin_id: 'music', installed_version: '0.1.1', available: false })
      .hidden,
    true
  );
  const available = providerUpgradeCard({
    plugin_id: 'music-project-management',
    installed_version: '0.1.0',
    available_version: '0.1.1',
    available: true
  });
  assert.equal(available.state, 'available');
  assert.equal(available.reviewable, true);
  assert.match(available.title, /music-project-management 0\.1\.1 is available/);
  assert.match(available.text, /uses 0\.1\.0.*Plugins page cannot update it/s);

  const attention = providerUpgradeCard({
    available: true,
    operation: {
      id: 'op-7',
      status: 'reconcile_required',
      to_version: '0.1.1',
      reason: 'The plugin replacement stopped partway.'
    }
  });
  assert.equal(attention.state, 'reconcile_required');
  assert.equal(attention.reviewable, false);
  assert.match(attention.text, /operation op-7.*stopped partway.*Nothing is chosen/s);

  assert.equal(
    providerUpgradeCard({ operation: { status: 'replaced', to_version: '0.1.1' } }).state,
    'running'
  );
});

test('package upgrade result counts updated and kept agents', () => {
  const card = providerUpgradeCard(null, {
    status: 'succeeded',
    to_version: '0.1.1',
    agents: [
      { agent_name: 'Portfolio', profile: 'replace', home_copy: 'replace' },
      { agent_name: 'Librarian', profile: 'keep', home_copy: 'replace' },
      { agent_name: 'Scout', profile: 'keep', home_copy: 'keep' }
    ]
  });
  assert.equal(card.state, 'succeeded');
  assert.equal(card.title, 'Upgraded to 0.1.1');
  assert.match(card.text, /1 staffed agent got the new guidance; 2 kept their edited prompt/);
  assert.match(card.text, /Approved library folders stay approved/);
  assert.equal(
    providerUpgradeAgentLine({ profile: 'missing', home_copy: 'missing' }),
    'has no saved profile; nothing to update'
  );
});

test('the Plugins page hand-off opens the upgrade review only when asked', () => {
  assert.equal(requestedUpgradeReview('?upgrade=review'), true);
  assert.equal(requestedUpgradeReview('?folder_offer_id=x&upgrade=review'), true);
  assert.equal(requestedUpgradeReview('?upgrade=commit'), false);
  assert.equal(requestedUpgradeReview(''), false);
});
