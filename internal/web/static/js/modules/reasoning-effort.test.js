import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

function load() {
  const window = {};
  const source = readFileSync(new URL('./reasoning-effort.js', import.meta.url), 'utf8');
  vm.runInNewContext(source, { window }, { filename: 'reasoning-effort.js' });
  return window.OriReasoningEffort;
}

const asData = value => JSON.parse(JSON.stringify(value));

function fakeSelect() {
  const doc = {
    createElement: tag => ({ tag, value: '', textContent: '', selected: false })
  };
  return {
    ownerDocument: doc,
    options: [],
    value: 'stale',
    replaceChildren() {
      this.options = [];
    },
    appendChild(option) {
      this.options.push(option);
    }
  };
}

test('only Codex and Claude Code take a reasoning level', () => {
  const api = load();
  assert.deepEqual(asData(api.levels('codex', 'gpt-5.6-sol')), ['low', 'medium', 'high', 'xhigh']);
  assert.deepEqual(asData(api.levels('openai', 'gpt-5-codex')), ['low', 'medium', 'high', 'xhigh']);
  assert.deepEqual(asData(api.levels('claude_code', 'opus')), [
    'low',
    'medium',
    'high',
    'xhigh',
    'max'
  ]);
  for (const [provider, model] of [
    ['claude', 'claude-opus-5'],
    ['openai', 'gpt-5.6-sol'],
    ['ollama', 'llama3.2'],
    ['', '']
  ]) {
    assert.equal(api.supports(provider, model), false, `${provider}/${model}`);
    assert.deepEqual(asData(api.options(provider, model)), []);
    assert.equal(api.helpText(provider, model), '');
  }
});

test('levels normalize per provider, with a Codex-only default', () => {
  const api = load();
  assert.equal(api.normalize('claude_code', 'opus', ' MAX '), 'max');
  assert.equal(api.normalize('codex', 'gpt-5.6-sol', 'max'), '');
  assert.equal(api.normalize('claude', 'claude-opus-5', 'high'), '');
  assert.equal(api.defaultFor('codex', 'gpt-5.6-sol'), 'medium');
  assert.equal(api.defaultFor('claude_code', 'opus'), '');

  assert.deepEqual(
    asData(api.options('codex', 'x')).map(option => option.label),
    ['Low', 'Medium (Recommended)', 'High', 'Extra High']
  );
  assert.deepEqual(asData(api.options('claude_code', 'opus')), [
    { value: '', label: 'Claude Code default' },
    { value: 'low', label: 'Low' },
    { value: 'medium', label: 'Medium' },
    { value: 'high', label: 'High' },
    { value: 'xhigh', label: 'Extra High' },
    { value: 'max', label: 'Max' }
  ]);
});

test('a select keeps an accepted choice across providers and falls back otherwise', () => {
  const api = load();
  const select = fakeSelect();

  assert.equal(api.syncSelect(select, 'claude_code', 'opus', 'max'), 'max');
  assert.equal(select.value, 'max');
  assert.equal(select.options.length, 6);

  // Codex has no max: its default is chosen instead.
  assert.equal(api.syncSelect(select, 'codex', 'gpt-5.6-sol', 'max'), 'medium');
  assert.equal(select.options.length, 4);
  assert.equal(api.syncSelect(select, 'codex', 'gpt-5.6-sol', 'high'), 'high');

  // Claude Code's default is "no flag", kept as an explicit empty choice.
  assert.equal(api.syncSelect(select, 'claude_code', 'opus', ''), '');
  assert.equal(api.syncSelect(select, 'claude_code', 'opus', 'nonsense'), '');
  assert.equal(api.syncSelect(select, 'claude_code', 'opus', 'high'), 'high');

  // A provider without levels empties the select.
  assert.equal(api.syncSelect(select, 'claude', 'claude-opus-5', 'high'), '');
  assert.equal(select.options.length, 0);
});
