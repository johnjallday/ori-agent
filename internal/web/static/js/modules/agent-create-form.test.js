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
// The form reads which models take a reasoning level from the shared module,
// loaded before it as in head.tmpl.
vm.runInThisContext(readFileSync(new URL('./reasoning-effort.js', import.meta.url), 'utf8'), {
  filename: 'reasoning-effort.js'
});
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
  for (const field of ['name', 'model', 'reasoningEffort', 'systemPrompt']) {
    assert.match(template, new RegExp(`data-agent-create-field="${field}"`));
  }
  assert.doesNotMatch(template, /data-agent-create-field="type"/);
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
    'model',
    'provider',
    'reasoningEffort',
    'systemPrompt'
  ]);
  assert.deepEqual(Form.profileFields('template'), [
    'name',
    'model',
    'provider',
    'reasoningEffort',
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
      models: [{ value: 'gpt-5', label: 'GPT-5', provider: 'openai' }]
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

test('template extraction preserves long prompts and drops a level the model cannot take', () => {
  const longPrompt = `  ${'x'.repeat(4100)}  `;
  const host = fakeHost({
    name: '  Blueprint Agent  ',
    model: 'private-model',
    provider: 'custom',
    reasoningEffort: 'high',
    systemPrompt: longPrompt
  });
  const result = Form.extract(host, 'template');
  assert.equal(result.valid, true);
  assert.deepEqual(result.values, {
    name: 'Blueprint Agent',
    model: 'private-model',
    provider: 'custom',
    reasoningEffort: '',
    systemPrompt: 'x'.repeat(4100)
  });
});

test('both profiles return a reasoning level only for Codex and Claude Code', () => {
  for (const profile of ['template', 'standalone']) {
    const extract = values => Form.extract(fakeHost({ name: 'Agent', ...values }), profile).values;
    assert.equal(
      extract({ provider: 'codex', model: 'gpt-5.6-sol', reasoningEffort: 'xhigh' })
        .reasoningEffort,
      'xhigh',
      profile
    );
    assert.equal(
      extract({ provider: 'claude_code', model: 'opus', reasoningEffort: 'max' }).reasoningEffort,
      'max',
      profile
    );
    assert.equal(
      extract({ provider: 'codex', model: 'gpt-5.6-sol', reasoningEffort: 'max' }).reasoningEffort,
      '',
      `${profile}: Codex has no max`
    );
    assert.equal(
      extract({ provider: 'claude', model: 'claude-opus-5', reasoningEffort: 'high' })
        .reasoningEffort,
      '',
      `${profile}: API providers take none`
    );
  }
  assert.equal(Form.supportsReasoning('claude_code', 'sonnet'), true);
  assert.equal(Form.supportsReasoning('openai', 'gpt-5-codex'), true);
  assert.equal(Form.supportsReasoning('claude', 'claude-opus-5'), false);
});

test('standalone extraction validates the prompt cap and reports field errors', () => {
  const host = fakeHost({
    name: 'bad/name',
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

test('a mount can omit the prompt: its section becomes a note and extraction never returns it', () => {
  assert.deepEqual(Form.mountedFields('template', ['systemPrompt', 'name']), [
    'name',
    'model',
    'provider',
    'reasoningEffort'
  ]);

  const replaced = [];
  const removed = [];
  const section = name => ({
    replaceWith(node) {
      replaced.push([name, node]);
    },
    remove() {
      removed.push(name);
    }
  });
  const root = {
    querySelector(selector) {
      const match = selector.match(/data-agent-create-section="(.+)"/);
      return match ? section(match[1]) : null;
    }
  };
  const previousDocument = globalThis.document;
  globalThis.document = {
    createElement: tag => ({
      tag,
      attributes: {},
      setAttribute(name, value) {
        this.attributes[name] = value;
      }
    })
  };
  try {
    const note = 'Instructions for this role come from Plugin: x 1.0 and are applied by Ori.';
    assert.deepEqual(Form.omitSections(root, ['systemPrompt', 'name'], `  ${note}  `), [
      'systemPrompt'
    ]);
    assert.equal(replaced.length, 1, 'only the prompt is omissible');
    assert.equal(replaced[0][1].textContent, note);
    assert.equal(replaced[0][1].attributes['data-agent-create-note'], 'systemPrompt');
    assert.deepEqual(Form.omitSections(root, ['systemPrompt']), ['systemPrompt']);
    assert.deepEqual(removed, ['systemPrompt'], 'without a note the section is simply removed');
  } finally {
    globalThis.document = previousDocument;
  }

  const host = fakeHost({ name: 'Studio Manager', model: 'm', provider: 'p', systemPrompt: 'x' });
  const result = Form.extract({ host, profile: 'template', omitFields: ['systemPrompt'] });
  assert.deepEqual(result.values, {
    name: 'Studio Manager',
    model: 'm',
    provider: 'p',
    reasoningEffort: ''
  });
});
