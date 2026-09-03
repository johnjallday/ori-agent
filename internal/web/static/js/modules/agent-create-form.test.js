import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const source = readFileSync(new URL('./agent-create-form.js', import.meta.url), 'utf8');
const template = readFileSync(
  new URL('../../../templates/components/agent-create-form.tmpl', import.meta.url),
  'utf8'
);

globalThis.window = globalThis.window || {};
vm.runInThisContext(source, { filename: 'agent-create-form.js' });
const Form = globalThis.window.AgentCreateForm;

function classList() {
  return { toggle() {} };
}

function fakeHost(values) {
  const fields = new Map(
    Object.entries(values).map(([name, value]) => [
      name,
      {
        value,
        classList: classList(),
        setAttribute() {},
        removeAttribute() {}
      }
    ])
  );
  const model = fields.get('model');
  if (model) {
    model.selectedOptions = [
      {
        getAttribute(name) {
          return name === 'data-provider' ? values.provider || '' : '';
        }
      }
    ];
  }
  const errors = new Map();
  return {
    fields,
    errors,
    querySelector(selector) {
      const fieldMatch = selector.match(/^\[data-agent-create-field="(.+)"\]$/);
      if (fieldMatch) return fields.get(fieldMatch[1]) || null;
      const errorMatch = selector.match(/^\[data-agent-create-error="(.+)"\]$/);
      if (!errorMatch) return null;
      if (!errors.has(errorMatch[1])) {
        errors.set(errorMatch[1], { textContent: '', classList: classList() });
      }
      return errors.get(errorMatch[1]);
    }
  };
}

test('the inert shared core has one template ID and no preassigned field IDs', () => {
  assert.match(template, /id="agentCreateFormTemplate"/);
  assert.equal((template.match(/\sid="/g) || []).length, 1);
  for (const field of ['name', 'type', 'model', 'reasoningEffort', 'systemPrompt']) {
    assert.match(template, new RegExp(`data-agent-create-field="${field}"`));
  }
  assert.match(template, /data-agent-create-for="Name"/);
  assert.match(template, /data-agent-create-describedby="NameHelp NameError"/);
});

test('scoped IDs are deterministic and reject an empty prefix', () => {
  assert.equal(Form.scopedId('agent', 'Name'), 'agentName');
  assert.equal(Form.scopedId('workspaceAgentSetup', 'NameError'), 'workspaceAgentSetupNameError');
  assert.throws(() => Form.scopedId('***', 'Name'), /ID prefix/);
});

test('profiles expose only fields backed by their contracts', () => {
  assert.deepEqual(Form.profileFields('standalone'), [
    'name',
    'type',
    'model',
    'provider',
    'reasoningEffort',
    'systemPrompt'
  ]);
  assert.deepEqual(Form.profileFields('template'), [
    'name',
    'type',
    'model',
    'provider',
    'systemPrompt'
  ]);
});

test('name validation matches the Go create and override contract', () => {
  assert.equal(Form.validateName('Agent One_copy-2'), '');
  assert.match(Form.validateName('   '), /required/);
  assert.match(Form.validateName('a'.repeat(101)), /100/);
  assert.match(
    Form.validateName('agent.dot'),
    /letters, numbers, spaces, underscores, and hyphens/
  );
  assert.equal(
    Form.NAME_HELP,
    'Use 1–100 characters: letters, numbers, spaces, underscores, and hyphens.'
  );
});

test('unknown current model is preserved as an explicit choice', () => {
  const providers = [
    {
      name: 'openai',
      display_name: 'OpenAI',
      models: [{ value: 'gpt-5', label: 'GPT-5', type: 'general', provider: 'openai' }]
    }
  ];
  const known = Form.modelChoices(providers, 'gpt-5', 'openai');
  assert.equal(known.length, 1);
  assert.equal(known[0].current, undefined);

  const unknown = Form.modelChoices(providers, 'private-model', 'custom');
  assert.equal(unknown[0].value, 'private-model');
  assert.equal(unknown[0].provider, 'custom');
  assert.equal(unknown[0].current, true);
});

test('template extraction omits read-only reasoning and preserves long prompts', () => {
  const longPrompt = `  ${'x'.repeat(4100)}  `;
  const host = fakeHost({
    name: '  Blueprint Agent  ',
    type: 'general',
    model: 'private-model',
    provider: 'custom',
    reasoningEffort: 'high',
    systemPrompt: longPrompt
  });
  const result = Form.extract(host, 'template');
  assert.equal(result.valid, true);
  assert.deepEqual(result.values, {
    name: 'Blueprint Agent',
    type: 'general',
    model: 'private-model',
    provider: 'custom',
    systemPrompt: 'x'.repeat(4100)
  });
  assert.equal('reasoningEffort' in result.values, false);
});

test('standalone extraction validates the prompt cap and reports field errors', () => {
  const host = fakeHost({
    name: 'bad/name',
    type: 'tool-calling',
    model: 'gpt-5',
    provider: 'openai',
    reasoningEffort: 'medium',
    systemPrompt: 'x'.repeat(4001)
  });
  const result = Form.extract(host, 'standalone');
  assert.equal(result.valid, false);
  assert.deepEqual(Object.keys(result.errors).sort(), ['name', 'systemPrompt']);
  assert.equal(host.errors.get('name').textContent, result.errors.name);
  assert.equal(host.errors.get('systemPrompt').textContent, result.errors.systemPrompt);
});

test('Codex reasoning detection accepts provider or model identity', () => {
  assert.equal(Form.supportsCodexReasoning('codex', 'gpt-5'), true);
  assert.equal(Form.supportsCodexReasoning('openai', 'gpt-5-codex'), true);
  assert.equal(Form.supportsCodexReasoning('anthropic', 'claude-opus'), false);
});
