import test from 'node:test';
import assert from 'node:assert/strict';

import { fetchWorkspaceProtection, protectionFactView } from './workspace-protection.js';

test('the Protection fact is read from the workspace API and fails to nothing', async () => {
  const calls = [];
  const fact = { state: 'not_covered', sentence: 'Not in any backup Ori observes' };
  const got = await fetchWorkspaceProtection('ws 1', async (url, init) => {
    calls.push({ url, init });
    return { ok: true, json: async () => ({ protection: fact }) };
  });
  assert.deepEqual(got, fact);
  assert.equal(calls[0].url, '/api/workspaces/ws%201/protection');
  assert.equal(calls[0].init.method, undefined, 'a plain GET');
  assert.equal(await fetchWorkspaceProtection('', async () => assert.fail('no id, no read')), null);
  assert.equal(await fetchWorkspaceProtection('ws', async () => ({ ok: false })), null);
  assert.equal(
    await fetchWorkspaceProtection('ws', async () => ({
      ok: true,
      json: async () => ({ protection: null })
    })),
    null
  );
  assert.equal(
    await fetchWorkspaceProtection('ws', async () => {
      throw new Error('offline');
    }),
    null
  );
});

test('only the three server states with a sentence are shown, and only local routes link', () => {
  assert.deepEqual(
    protectionFactView({
      state: 'covered',
      sentence: 'Protected by Time Machine in My Backups, last completed backup 2 d ago',
      route: '/workspaces/my-backups?panel=backup-recovery',
      route_label: 'Open Backup & Recovery'
    }),
    {
      state: 'covered',
      sentence: 'Protected by Time Machine in My Backups, last completed backup 2 d ago',
      route: '/workspaces/my-backups?panel=backup-recovery',
      routeLabel: 'Open Backup & Recovery'
    }
  );
  assert.equal(protectionFactView({ state: 'verified', sentence: 'Backed up' }), null);
  assert.equal(protectionFactView({ state: 'covered', sentence: '  ' }), null);
  for (const route of [
    '//evil.example',
    'https://evil.example',
    'javascript:alert(1)',
    '/\\evil'
  ]) {
    const view = protectionFactView({ state: 'unknown', sentence: 'Protection unknown', route });
    assert.equal(view.route, '', route);
    assert.equal(view.routeLabel, '');
  }
});
