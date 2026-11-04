import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const window = {};
vm.runInNewContext(readFileSync(new URL('./agent-edit-session.js', import.meta.url), 'utf8'), {
  window
});
const api = window.AgentEditSession;
// The module runs in its own realm; compare plain copies of what it returns.
const plain = value => JSON.parse(JSON.stringify(value));

const agent = (overrides = {}) => ({
  name: 'Scout',
  version: 'v1',
  role: 'general',
  model: 'gpt-5',
  provider: 'openai',
  temperature: 0.7,
  max_output_tokens: 0,
  reasoning_effort: '',
  system_prompt: 'Be brief.',
  metadata: { description: 'Finds things', tags: ['a'], favorite: false },
  ...overrides
});

// A fetch double that answers from a queue and records every request.
function fakeFetch(...answers) {
  const calls = [];
  const impl = async (url, init) => {
    calls.push({ url, body: JSON.parse(init.body) });
    const next = answers.shift();
    if (next instanceof Error) throw next;
    return {
      status: next.status,
      json: async () => {
        if (next.body === undefined) throw new SyntaxError('not json');
        return next.body;
      }
    };
  };
  impl.calls = calls;
  return impl;
}

test('a patch carries only the section’s changed fields and its version', () => {
  const s = api.createSession(agent());
  s.setDraft('prompt', { system_prompt: 'Be thorough.' });
  assert.deepEqual(plain(s.beginSubmit('prompt')), {
    system_prompt: 'Be thorough.',
    expected_version: 'v1'
  });

  const p = api.createSession(agent());
  p.setDraft('profile', {
    name: 'Scout',
    description: 'Finds more things',
    tags: ['a'],
    favorite: false
  });
  assert.deepEqual(plain(p.beginSubmit('profile')), {
    description: 'Finds more things',
    expected_version: 'v1'
  });
});

test('provider, model and reasoning travel together', () => {
  const s = api.createSession(agent());
  s.setDraft('configuration', { model: 'gpt-5-mini', llm_provider: 'openai' });
  const payload = s.beginSubmit('configuration');
  assert.deepEqual(plain(Object.keys(payload).sort()), [
    'expected_version',
    'llm_provider',
    'model',
    'reasoning_effort'
  ]);
  const t = api.createSession(agent());
  t.setDraft('configuration', { temperature: '0.2' });
  assert.deepEqual(plain(t.beginSubmit('configuration')), {
    temperature: 0.2,
    expected_version: 'v1'
  });
});

test('a blank number field leaves the saved value alone', () => {
  const s = api.createSession(agent({ max_output_tokens: 900 }));
  s.setDraft('configuration', { max_output_tokens: '', temperature: 'abc' });
  assert.equal(s.isDirty('configuration'), false);
});

test('saving needs a loaded version, a change, and no write in flight', () => {
  const noVersion = api.createSession(agent({ version: '' }));
  noVersion.setDraft('prompt', { system_prompt: 'x' });
  assert.throws(() => noVersion.beginSubmit('prompt'), /version is unknown/);

  const s = api.createSession(agent());
  assert.throws(() => s.beginSubmit('prompt'), /no changes/);
  s.setDraft('prompt', { system_prompt: 'x' });
  s.setDraft('configuration', { temperature: 0.1 });
  s.beginSubmit('prompt');
  assert.throws(() => s.beginSubmit('prompt'), /still saving/);
  assert.throws(() => s.beginSubmit('configuration'), /still saving/);
  assert.equal(s.pending(), 'prompt');

  const empty = api.createSession(agent());
  empty.setDraft('profile', { name: '  ' });
  assert.throws(() => empty.beginSubmit('profile'), /Name is required/);
});

test('answers are classified by structured codes, not wording', () => {
  const cases = [
    [409, { error: 'stale_agent_edit', code: 'stale_agent_edit', message: 'x' }, 'stale'],
    [
      409,
      { code: 'agent_changed_on_disk', error: 'This agent was changed on disk' },
      'changed_on_disk'
    ],
    [409, { error: 'shared_agent_edit_requires_confirmation' }, 'needs_confirmation'],
    [409, { error: 'attached_agent_rename_blocked' }, 'protected'],
    [409, { error: 'personal_assistant_profile_protected' }, 'protected'],
    [409, { code: 'workspace_owned_agent' }, 'read_only'],
    [409, { code: 'agent_name_taken' }, 'name_taken'],
    [409, { code: 'agent_rename_failed', definition_saved: true }, 'rename_failed'],
    [503, { code: 'agent_root_unavailable' }, 'unavailable'],
    [400, { error: 'invalid reasoning_effort' }, 'invalid'],
    [404, { error: 'Agent not found' }, 'missing'],
    [500, { error: 'Failed to update agent' }, 'error'],
    [200, { success: true, version: 'v2', agent: {} }, 'saved']
  ];
  for (const [status, body, kind] of cases) {
    assert.equal(api.classify(status, body).kind, kind, JSON.stringify(body));
  }
  // A success without a receipt, or one that is not JSON, may have landed.
  assert.equal(api.classify(200, { success: true }).kind, 'unconfirmed');
  assert.equal(api.classify(200, null, { json: false }).kind, 'unconfirmed');
  assert.equal(api.classify(502, null, { json: false }).kind, 'unconfirmed');
});

test('shared confirmation retries the same reviewed fields and version', async () => {
  const fetchImpl = fakeFetch(
    { status: 409, body: { error: 'shared_agent_edit_requires_confirmation', workspace_count: 2 } },
    { status: 409, body: { error: 'stale_agent_edit', code: 'stale_agent_edit' } }
  );
  const payload = { system_prompt: 'x', expected_version: 'v1' };
  const outcome = await api.submitPatch({
    fetchImpl,
    name: 'Scout',
    payload,
    confirmShared: async info => info.workspace_count === 2
  });
  assert.equal(outcome.kind, 'stale');
  assert.deepEqual(fetchImpl.calls[1].body, { ...payload, confirm_shared_edit: true });
  assert.equal(fetchImpl.calls[0].url, '/api/agents/Scout');
});

test('declining a shared edit sends nothing more', async () => {
  const fetchImpl = fakeFetch({
    status: 409,
    body: { error: 'shared_agent_edit_requires_confirmation' }
  });
  const outcome = await api.submitPatch({
    fetchImpl,
    name: 'Scout',
    payload: { system_prompt: 'x', expected_version: 'v1' },
    confirmShared: async () => false
  });
  assert.equal(outcome.kind, 'declined');
  assert.equal(fetchImpl.calls.length, 1);
});

test('a lost or unreadable answer is unconfirmed, never failed', async () => {
  for (const answer of [new TypeError('Failed to fetch'), { status: 200, body: undefined }]) {
    const outcome = await api.submitPatch({
      fetchImpl: fakeFetch(answer),
      name: 'Scout',
      payload: { system_prompt: 'x', expected_version: 'v1' },
      confirmShared: async () => true
    });
    assert.equal(outcome.kind, 'unconfirmed');
  }
});

test('the submitted draft survives conflicts, refusals and lost answers', () => {
  for (const kind of ['stale', 'changed_on_disk', 'invalid', 'protected', 'error', 'unconfirmed']) {
    const s = api.createSession(agent());
    s.setDraft('prompt', { system_prompt: 'Keep me' });
    s.beginSubmit('prompt');
    s.finishSubmit('prompt', { kind });
    assert.equal(s.draft('prompt').system_prompt, 'Keep me', kind);
    assert.equal(s.isDirty('prompt'), true, kind);
    assert.equal(s.pending(), null, kind);
  }
  const s = api.createSession(agent());
  s.setDraft('prompt', { system_prompt: 'Keep me' });
  s.beginSubmit('prompt');
  s.finishSubmit('prompt', { kind: 'unconfirmed' });
  assert.equal(s.state('prompt'), 'unconfirmed');
  assert.throws(() => s.beginSubmit('prompt'), /not confirmed/);
});

test('a stale answer becomes a conflict only when the edited fields moved', () => {
  const s = api.createSession(agent());
  s.setDraft('prompt', { system_prompt: 'Mine' });
  s.beginSubmit('prompt');
  s.finishSubmit('prompt', { kind: 'stale' });
  assert.equal(s.state('prompt'), 'stale');
  s.observe(agent({ version: 'v2', metadata: { description: 'Elsewhere' } }));
  assert.equal(s.state('prompt'), 'dirty');
  assert.equal(s.version('prompt'), 'v2');

  const t = api.createSession(agent());
  t.setDraft('prompt', { system_prompt: 'Mine' });
  t.beginSubmit('prompt');
  t.finishSubmit('prompt', { kind: 'changed_on_disk' });
  t.observe(agent({ version: 'v2', system_prompt: 'Edited on disk' }));
  assert.equal(t.state('prompt'), 'conflict');
  assert.equal(t.version('prompt'), 'v1');
});

test('a confirmed save adopts the receipt and keeps input typed after Save', () => {
  const s = api.createSession(agent());
  s.setDraft('prompt', { system_prompt: 'First' });
  s.beginSubmit('prompt');
  s.setDraft('prompt', { system_prompt: 'First, then more' });
  s.finishSubmit('prompt', {
    kind: 'saved',
    version: 'v2',
    receipt: { version: 'v2', agent: agent({ system_prompt: 'First', version: 'v2' }) }
  });
  assert.equal(s.version('prompt'), 'v2');
  assert.equal(s.baseline('prompt').system_prompt, 'First');
  assert.equal(s.draft('prompt').system_prompt, 'First, then more');
  assert.equal(s.state('prompt'), 'dirty');

  const clean = api.createSession(agent());
  clean.setDraft('prompt', { system_prompt: 'Only' });
  clean.beginSubmit('prompt');
  clean.finishSubmit('prompt', {
    kind: 'saved',
    version: 'v2',
    receipt: { agent: agent({ system_prompt: 'Only' }) }
  });
  assert.equal(clean.draft('prompt'), null);
  assert.equal(clean.state('prompt'), 'saved');
});

test('another section’s save advances an unaffected draft, conflicts an affected one', () => {
  const s = api.createSession(agent());
  s.setDraft('prompt', { system_prompt: 'Draft prompt' });
  s.setDraft('profile', { description: 'Draft description' });

  // Configuration saved elsewhere on the page: prompt/profile fields unchanged.
  const afterConfig = agent({ version: 'v2', temperature: 0.1 });
  assert.deepEqual(plain(s.observe(afterConfig)), {
    profile: 'unchanged',
    configuration: 'refreshed',
    prompt: 'unchanged'
  });
  assert.equal(s.version('prompt'), 'v2');
  assert.equal(s.version('profile'), 'v2');
  assert.equal(s.draft('prompt').system_prompt, 'Draft prompt');

  // Another client changed the prompt itself: the draft is kept for review.
  const result = s.observe(agent({ version: 'v3', temperature: 0.1, system_prompt: 'Theirs' }));
  assert.equal(result.prompt, 'conflict');
  assert.equal(s.state('prompt'), 'conflict');
  assert.equal(s.version('prompt'), 'v2');
  assert.equal(s.draft('prompt').system_prompt, 'Draft prompt');
  assert.equal(s.latest('prompt').system_prompt, 'Theirs');
});

test('a non-overlapping outside change rebases the untouched field into the draft', () => {
  const s = api.createSession(agent());
  s.setDraft('profile', {
    name: 'Scout',
    description: 'Mine',
    tags: ['a'],
    favorite: false
  });
  const out = s.observe(
    agent({ version: 'v2', metadata: { description: 'Finds things', tags: ['a', 'b'] } })
  );
  assert.equal(out.profile, 'rebased');
  assert.deepEqual(plain(s.draft('profile').tags), ['a', 'b']);
  assert.deepEqual(plain(s.beginSubmit('profile')), {
    description: 'Mine',
    expected_version: 'v2'
  });
});
