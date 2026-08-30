import test from 'node:test';
import assert from 'node:assert/strict';

import { AssistantProgramPage } from './assistant-program.js';

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
