import { test } from 'node:test';
import assert from 'node:assert/strict';

import {
  consumeGroupTemplateLanding,
  fetchGroupTemplateStatus,
  GROUP_TEMPLATE_LANDING_KEY,
  groupTemplateIntegrationFact,
  groupTemplateProviderLabel,
  groupTemplateRolePromptNote,
  groupTemplateTeamFact
} from './group-template-status.js';

const status = (
  team,
  provider = { kind: 'plugin', plugin_id: 'fixture', plugin_version: '1.0.0' }
) => ({
  kind: 'managed_home',
  provider,
  team
});

test('coordinator copy reports only verified progress', () => {
  const roles = [{ role_id: 'coordinator', label: 'Portfolio Coordinator', state: 'empty' }];
  const incomplete = groupTemplateTeamFact(
    status({
      state: 'incomplete',
      required_home_roles: { verification: 'verified', required: 1, filled: 0, roles }
    })
  );
  assert.equal(incomplete.copy, 'Incomplete — 0 of 1 required set up (Portfolio Coordinator)');
  assert.equal(incomplete.missing.length, 1);

  const ready = groupTemplateTeamFact(
    status({
      state: 'ready',
      required_home_roles: {
        verification: 'verified',
        required: 1,
        filled: 1,
        roles: [{ ...roles[0], state: 'filled' }]
      }
    })
  );
  assert.equal(ready.copy, 'Ready — 1 of 1 required set up');
  assert.equal(ready.className, 'is-ready');

  // Unverifiable evidence is never presented as a count or as Ready.
  for (const team of [
    { state: 'ready', required_home_roles: { verification: 'unavailable' } },
    { state: 'unverified' },
    undefined
  ]) {
    const fact = groupTemplateTeamFact(status(team));
    assert.equal(fact.copy, 'Could not be verified');
    assert.equal(fact.verified, false);
  }
  assert.equal(
    groupTemplateTeamFact(status({ state: 'migration_required' })).copy,
    'Needs its guided migration'
  );
});

test('integration and provider copy never invent availability', () => {
  assert.deepEqual(groupTemplateIntegrationFact({ state: 'available' }), {
    copy: 'Available',
    className: 'is-ready'
  });
  assert.equal(
    groupTemplateIntegrationFact({ state: 'unavailable', reason: 'plugin_enable_required' }).copy,
    'Unavailable — its plugin is disabled'
  );
  assert.equal(
    groupTemplateIntegrationFact({ state: 'unavailable', reason: 'new' }).copy,
    'Unavailable'
  );
  assert.deepEqual(groupTemplateIntegrationFact(null), {
    copy: 'Could not be checked',
    className: 'is-unknown'
  });
  assert.equal(groupTemplateProviderLabel(status({})), 'Plugin: fixture 1.0.0');
  assert.equal(groupTemplateProviderLabel(status({}, { kind: 'user_template' })), 'Your template');
  assert.equal(groupTemplateProviderLabel(null), '');
});

test('an unreadable status resolves to null rather than an empty group', async () => {
  const original = globalThis.fetch;
  try {
    const urls = [];
    globalThis.fetch = async url => {
      urls.push(url);
      if (url.includes('broken')) throw new Error('offline');
      if (url.includes('missing')) return { ok: false, json: async () => ({}) };
      return { ok: true, json: async () => ({ group_template: { kind: 'managed_home' } }) };
    };
    assert.equal(await fetchGroupTemplateStatus(''), null);
    assert.equal(await fetchGroupTemplateStatus('broken'), null);
    assert.equal(await fetchGroupTemplateStatus('missing'), null);
    assert.deepEqual(await fetchGroupTemplateStatus('home/1'), { kind: 'managed_home' });
    assert.equal(urls.at(-1), '/api/workspaces/home%2F1/group-template');
  } finally {
    globalThis.fetch = original;
  }
});

test('a program Home role prompt note names the template source', () => {
  assert.equal(
    groupTemplateRolePromptNote(status({})),
    'Instructions for this role come from Plugin: fixture 1.0.0 and are applied by Ori.'
  );
  assert.equal(
    groupTemplateRolePromptNote(null),
    'Instructions for this role come from its template and are applied by Ori.'
  );
});

function memoryStorage(entries = {}) {
  const values = new Map(Object.entries(entries));
  return {
    values,
    getItem: key => (values.has(key) ? values.get(key) : null),
    setItem: (key, value) => values.set(key, String(value)),
    removeItem: key => values.delete(key)
  };
}

test('the creator landing notice is read once, only on its own group, only while fresh', () => {
  const now = 1_000_000;
  const notice = {
    workspace_id: 'home-1',
    created_at: now - 1000,
    tone: 'warning',
    title: 'Group created',
    message: 'Lab is ready. Portfolio Coordinator was not staffed: Nope.',
    role_id: 'coordinator',
    error: 'Nope.'
  };
  const storage = memoryStorage({ [GROUP_TEMPLATE_LANDING_KEY]: JSON.stringify(notice) });
  assert.deepEqual(consumeGroupTemplateLanding(storage, 'home-1', now), {
    tone: 'warning',
    title: 'Group created',
    message: notice.message,
    roleId: 'coordinator',
    error: 'Nope.'
  });
  assert.equal(storage.values.size, 0, 'consumed');
  assert.equal(consumeGroupTemplateLanding(storage, 'home-1', now), null, 'never twice');

  const elsewhere = memoryStorage({ [GROUP_TEMPLATE_LANDING_KEY]: JSON.stringify(notice) });
  assert.equal(consumeGroupTemplateLanding(elsewhere, 'home-2', now), null);
  assert.equal(elsewhere.values.size, 0, 'a notice for another group is discarded, not kept');

  const stale = memoryStorage({
    [GROUP_TEMPLATE_LANDING_KEY]: JSON.stringify({ ...notice, created_at: now - 10 * 60 * 1000 })
  });
  assert.equal(consumeGroupTemplateLanding(stale, 'home-1', now), null);

  const garbage = memoryStorage({ [GROUP_TEMPLATE_LANDING_KEY]: '{not json' });
  assert.equal(consumeGroupTemplateLanding(garbage, 'home-1', now), null);
  assert.equal(consumeGroupTemplateLanding(null, 'home-1', now), null);
  const throwing = {
    getItem() {
      throw new Error('blocked');
    }
  };
  assert.equal(consumeGroupTemplateLanding(throwing, 'home-1', now), null);
});
