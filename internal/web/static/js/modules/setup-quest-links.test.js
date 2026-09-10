import test from 'node:test';
import assert from 'node:assert/strict';
import {
  ASSISTANT_SETUP_ROOT,
  setupQuestAPIRoot,
  userTemplateSetupQuestAPIRoot,
  setupJourneyAPIRoot,
  setupQuestURL,
  setupQuestForTemplate,
  loadSetupQuests
} from './setup-quest-links.js';

const quest = {
  plugin_id: 'demo-plugin',
  id: 'demo_setup',
  template_id: 'plugin:demo-plugin:project'
};
const userQuest = {
  source: 'user_template',
  template_id: 'local-project',
  attachment_id: 'uqatt_0123456789abcdef01234567',
  id: 'quest_0123456789abcdef01234567'
};

test('quest routes are compiled from exact IDs, not plugin URLs or arbitrary paths', () => {
  assert.equal(
    setupQuestAPIRoot(quest.plugin_id, quest.id),
    '/api/setup-quests/demo-plugin/demo_setup'
  );
  assert.equal(
    setupQuestURL({ ...quest, launch_url: 'https://untrusted.test' }),
    '/?setup=quest&plugin=demo-plugin&quest=demo_setup'
  );
  assert.equal(setupJourneyAPIRoot({ journey: quest }), '/api/setup-quests/demo-plugin/demo_setup');
  assert.equal(setupJourneyAPIRoot({ journey: { id: 'legacy_setup' } }), ASSISTANT_SETUP_ROOT);
  for (const bad of [
    '',
    null,
    undefined,
    123,
    [],
    'other/quest',
    '../plugin',
    'plugin%2fother',
    ' plugin',
    'plugin\n',
    'Plugin',
    'plugin?owner=other',
    'a'.repeat(65)
  ]) {
    assert.throws(() => setupQuestAPIRoot(bad, quest.id));
    assert.throws(() => setupQuestAPIRoot(quest.plugin_id, bad));
  }
  assert.throws(() => setupJourneyAPIRoot({ journey: { plugin_id: '../other', id: quest.id } }));
});

test('user template routes use source-aware attachment identity without a plugin owner', () => {
  assert.equal(
    userTemplateSetupQuestAPIRoot(userQuest.template_id, userQuest.attachment_id),
    '/api/user-template-setup-quests/local-project/uqatt_0123456789abcdef01234567'
  );
  assert.equal(
    setupJourneyAPIRoot({ journey: userQuest }),
    '/api/user-template-setup-quests/local-project/uqatt_0123456789abcdef01234567'
  );
  assert.equal(
    setupQuestURL(userQuest),
    '/?setup=quest&source=user_template&template=local-project&attachment=uqatt_0123456789abcdef01234567'
  );
  const template = {
    id: userQuest.template_id,
    user_setup_quest: { attachment_id: userQuest.attachment_id }
  };
  assert.equal(setupQuestForTemplate(template, [userQuest]), userQuest);
  assert.equal(
    setupQuestForTemplate(template, [
      { ...userQuest, attachment_id: 'uqatt_aaaaaaaaaaaaaaaaaaaaaaaa' }
    ]),
    null
  );
  assert.throws(() => userTemplateSetupQuestAPIRoot('../local', userQuest.attachment_id));
  assert.throws(() =>
    userTemplateSetupQuestAPIRoot(userQuest.template_id, 'quest_0123456789abcdef01234567')
  );
});

test('template lookup matches exact ownership, optional legacy reference, and refuses ambiguity', () => {
  const template = {
    id: 'placeholder',
    plugin_owner: { plugin_id: 'demo-plugin', blueprint_id: 'project' },
    setup_quest: 'demo_setup'
  };
  assert.equal(setupQuestForTemplate(template, [quest]), quest);
  assert.equal(setupQuestForTemplate({ id: quest.template_id }, [quest]), quest);
  assert.equal(setupQuestForTemplate({ ...template, setup_quest: 'missing' }, [quest]), null);
  assert.equal(setupQuestForTemplate({ id: 'user-copy', setup_quest: quest.id }, [quest]), null);
  assert.equal(setupQuestForTemplate(template, [{ ...quest, plugin_id: 'other-plugin' }]), null);
  assert.equal(
    setupQuestForTemplate(template, [{ ...quest, template_id: 'plugin:other:project' }]),
    null
  );
  assert.equal(setupQuestForTemplate(template, [quest, quest]), null);
  assert.equal(setupQuestForTemplate(template, [null]), null);
  assert.equal(setupQuestForTemplate(null, [quest]), null);
});

test('catalog discovery makes one read and generates its own safe launch URL', async t => {
  const calls = [];
  t.mock.method(globalThis, 'fetch', async (url, options) => {
    calls.push({ url, options });
    return {
      ok: true,
      json: async () => ({ quests: [{ ...quest, launch_url: 'javascript:alert(1)' }] })
    };
  });
  const result = await loadSetupQuests();
  assert.equal(calls.length, 1);
  assert.equal(calls[0].url, '/api/setup-quests');
  assert.equal(calls[0].options.method, undefined);
  assert.equal(calls[0].options.body, undefined);
  assert.equal(result[0].launch_url, '/?setup=quest&plugin=demo-plugin&quest=demo_setup');
});

test('catalog failures and malformed entries never synthesize a specialist fallback', async t => {
  const fetch = t.mock.method(globalThis, 'fetch');
  for (const response of [
    { ok: false },
    { ok: true, json: async () => ({}) },
    { ok: true, json: async () => ({ quests: [{ ...quest, plugin_id: '../other' }] }) }
  ]) {
    fetch.mock.mockImplementation(async () => response);
    await assert.rejects(loadSetupQuests());
  }
});
