import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';
import { setupQuestForTemplate, setupQuestURL } from './setup-quest-links.js';

const source = readFileSync(new URL('./templates-page.js', import.meta.url), 'utf8');
const pluginTemplate = {
  id: 'plugin:demo-plugin:project',
  name: 'Demo Project',
  setup_quest: 'demo_setup',
  plugin_owner: { plugin_id: 'demo-plugin', blueprint_id: 'project' }
};
const quest = {
  plugin_id: 'demo-plugin',
  id: 'demo_setup',
  template_id: pluginTemplate.id,
  title: 'Set up Demo',
  description: 'Prepare a group and project.',
  ownership: 'plugin'
};
quest.launch_url = setupQuestURL(quest);

function harness() {
  const nodes = new Map();
  const calls = [];
  const node = id => {
    if (!nodes.has(id))
      nodes.set(id, {
        hidden: false,
        disabled: false,
        textContent: '',
        innerHTML: '',
        style: {},
        children: [],
        querySelectorAll: () => [],
        removeAttribute(name) {
          delete this[name];
        },
        classList: { add() {}, remove() {} }
      });
    return nodes.get(id);
  };
  const dynamicControl = node('dynamic-control');
  const sandbox = {
    console,
    document: {
      readyState: 'loading',
      addEventListener() {},
      getElementById: node,
      querySelectorAll: () => [dynamicControl]
    },
    window: {
      confirm() {
        calls.push('confirm');
        return false;
      }
    },
    fetch: async (...args) => {
      calls.push(args);
      throw new Error('unexpected write');
    }
  };
  vm.createContext(sandbox);
  vm.runInContext(
    source +
      `\nglobalThis.subject = {
    tplState, tplQuests, tplFiles, tplRenderQuest, tplTemplateReadOnly, tplApplyReadOnly,
    tplSaveOverview, tplDelete, tplDuplicate, tplFileCreate, tplFileRename, tplFileDelete,
    tplEditorSave, tplToolsSave, tplAgentsSave
  };`,
    sandbox
  );
  const subject = sandbox.subject;
  subject.tplState.templates = [pluginTemplate];
  subject.tplState.selectedId = pluginTemplate.id;
  Object.assign(subject.tplQuests, {
    status: 'ready',
    items: [quest],
    resolve: setupQuestForTemplate
  });
  return { subject, node, calls };
}

test('Templates shows an inert plugin-owned quest and its canonical launch link', () => {
  const { subject, node, calls } = harness();
  subject.tplRenderQuest();
  assert.equal(node('tplSetupQuest').hidden, false);
  assert.equal(node('tplQuestOpen').hidden, false);
  assert.equal(node('tplQuestOpen').href, quest.launch_url);
  assert.equal(node('tplQuestHeading').textContent, quest.title);
  assert.match(node('tplQuestOwnership').textContent, /plugin-owned declaration · read-only/);
  assert.match(node('tplQuestStatus').textContent, /resumes your saved progress/);
  assert.deepEqual(calls, []);
  subject.tplQuests.items = [
    { ...quest, ownership: 'host_compatibility', title: '<img src=x onerror=alert(1)>' }
  ];
  subject.tplRenderQuest();
  assert.match(node('tplQuestOwnership').textContent, /Ori compatibility/);
  assert.equal(node('tplQuestHeading').textContent, '<img src=x onerror=alert(1)>');
  assert.equal(node('tplQuestHeading').innerHTML, '');
});

test('selection changes, missing references, and failed reads clear stale quest links', () => {
  const { subject, node } = harness();
  for (const status of ['loading', 'error']) {
    subject.tplQuests.status = 'ready';
    subject.tplRenderQuest();
    subject.tplQuests.status = status;
    subject.tplRenderQuest();
    assert.equal(node('tplQuestOpen').hidden, true);
    assert.equal(node('tplQuestOpen').href, undefined);
    assert.equal(node('tplQuestRetry').hidden, status !== 'error');
  }
  subject.tplQuests.status = 'ready';
  subject.tplQuests.items = [];
  subject.tplRenderQuest();
  assert.match(node('tplQuestStatus').textContent, /referenced setup quest is unavailable/);
  const local = { id: 'local-copy', name: 'Local', setup_quest: 'demo_setup' };
  subject.tplState.templates.push(local);
  subject.tplQuests.items = [quest];
  subject.tplState.selectedId = local.id;
  subject.tplRenderQuest();
  assert.equal(node('tplQuestOpen').hidden, true);
  assert.equal(node('tplQuestOpen').href, undefined);
  delete local.setup_quest;
  subject.tplRenderQuest();
  assert.match(node('tplQuestStatus').textContent, /does not have an available setup quest/);
  subject.tplState.selectedId = '';
  subject.tplRenderQuest();
  assert.equal(node('tplSetupQuest').hidden, true);
});

test('plugin-owned metadata and editors stay read-only without disabling user templates', () => {
  const { subject, node } = harness();
  subject.tplApplyReadOnly();
  assert.match(node('tplDetailBuiltinBadge').textContent, /Plugin-owned/);
  assert.equal(node('tplDetailBuiltinBadge').hidden, false);
  for (const id of [
    'tplSaveBtn',
    'tplDeleteBtn',
    'tplFileNewBtn',
    'tplEditorSaveBtn',
    'tplToolsSaveBtn',
    'tplAgentsSaveBtn',
    'tplDuplicateBtn',
    'dynamic-control'
  ]) {
    assert.equal(node(id).disabled, true, id);
  }
  assert.equal(node('tplDuplicateToCustomizeBtn').hidden, true);
  assert.equal(node('tplQuestOpen').disabled, false);
  subject.tplState.templates = [{ id: 'local', name: 'Local' }];
  subject.tplState.selectedId = 'local';
  subject.tplApplyReadOnly();
  for (const id of [
    'tplSaveBtn',
    'tplDeleteBtn',
    'tplFileNewBtn',
    'tplToolsSaveBtn',
    'tplAgentsSaveBtn',
    'dynamic-control'
  ]) {
    assert.equal(node(id).disabled, false, id);
  }
  assert.equal(node('tplDetailBuiltinBadge').hidden, true);
  assert.equal(subject.tplTemplateReadOnly({ id: 'builtin', builtin: true }), true);
  assert.equal(subject.tplTemplateReadOnly({ id: pluginTemplate.id }), true);
});

test('plugin-owned write handlers refuse even a stale or programmatic invocation', async () => {
  const { subject, calls } = harness();
  subject.tplFiles.selectedPath = 'template.json';
  subject.tplFiles.readOnly = false;
  await subject.tplSaveOverview();
  await subject.tplDelete();
  subject.tplDuplicate();
  subject.tplFileCreate('file');
  subject.tplFileRename();
  await subject.tplFileDelete();
  await subject.tplEditorSave();
  await subject.tplToolsSave();
  await subject.tplAgentsSave();
  assert.deepEqual(calls, []);
});
