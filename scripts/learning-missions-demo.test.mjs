import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  mkdtempSync,
  mkdirSync,
  writeFileSync,
  readFileSync,
  existsSync,
  realpathSync,
  rmSync,
  symlinkSync
} from 'node:fs';
import { spawnSync } from 'node:child_process';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import { startLearningDemoControl } from './learning-missions-demo-control.mjs';
import {
  verifyLearningSandbox,
  startLearningProvider,
  learningDemoEnvironment
} from './learning-missions-demo.mjs';

test('loopback fixture controls require their private token and never accept a command or body', async t => {
  const provider = await startLearningProvider();
  let restarts = 0;
  const control = await startLearningDemoControl({
    provider,
    restart: async () => {
      restarts++;
    }
  });
  t.after(async () => {
    provider.releaseHeld();
    await control.close();
    await provider.close();
  });
  const headers = { 'X-Ori-Demo-Control': control.token };
  assert.equal((await fetch(control.base + '/restart', { method: 'POST' })).status, 403);
  assert.equal((await fetch(control.base + '/command', { method: 'POST', headers })).status, 404);
  assert.equal(
    (await fetch(control.base + '/restart', { method: 'POST', headers, body: '{}' })).status,
    409
  );
  assert.equal(restarts, 0);
  assert.equal((await fetch(control.base + '/hold', { method: 'POST', headers })).status, 200);
  const pending = fetch(provider.base + '/v1/chat/completions', {
    method: 'POST',
    body: JSON.stringify({
      max_tokens: 1200,
      messages: [
        { role: 'system', content: 'Host rules' },
        { role: 'user', content: JSON.stringify({ reviewed_input: 'Synthetic text' }) }
      ]
    })
  });
  for (let n = 0; n < 100 && !provider.held(); n++) await new Promise(done => setTimeout(done, 10));
  assert.equal(provider.held(), true);
  assert.equal((await fetch(control.base + '/restart', { method: 'POST', headers })).status, 409);
  assert.equal(restarts, 0);
  assert.equal((await fetch(control.base + '/release', { method: 'POST', headers })).status, 200);
  assert.equal((await pending).status, 200);
  assert.equal(provider.calls(), 1);
  assert.equal((await fetch(control.base + '/restart', { method: 'POST', headers })).status, 200);
  assert.equal(restarts, 1);
});

test('learning fixture refuses remote URLs, unowned roots and symlink escapes', t => {
  const root = realpathSync(mkdtempSync(join(tmpdir(), 'learning-helper-')));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const sandbox = join(root, 'tmp', 'demo');
  mkdirSync(sandbox, { recursive: true, mode: 0o750 });
  writeFileSync(join(sandbox, '.ori-demo-owner'), `worktree=${root}\n`, { mode: 0o600 });
  assert.equal(verifyLearningSandbox('http://127.0.0.1:1234', sandbox, root), sandbox);
  for (const base of [
    'http://example.com:1234',
    'http://127.0.0.1',
    'http://user@127.0.0.1:1234',
    'http://127.0.0.1:1234/path',
    'http://127.0.0.1:1234/?query'
  ]) {
    assert.throws(() => verifyLearningSandbox(base, sandbox, root));
  }
  writeFileSync(join(sandbox, '.ori-demo-owner'), 'worktree=/other', { mode: 0o600 });
  assert.throws(() => verifyLearningSandbox('http://127.0.0.1:1234', sandbox, root));
  const outside = join(root, 'outside');
  mkdirSync(outside);
  writeFileSync(join(outside, '.ori-demo-owner'), `worktree=${root}`, { mode: 0o600 });
  const link = join(root, 'tmp', 'link');
  symlinkSync(outside, link);
  assert.throws(() => verifyLearningSandbox('http://127.0.0.1:1234', link, root));
});

test('demo masks native credential discovery, inherited stores and local providers', t => {
  const root = realpathSync(mkdtempSync(join(tmpdir(), 'learning-environment-')));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  for (const extended of [false, true]) {
    const sandbox = join(root, String(extended));
    mkdirSync(sandbox);
    const env = learningDemoEnvironment(sandbox, 'http://127.0.0.1:1234', extended, {
      PATH: '/usr/bin:/bin',
      CODEX_HOME: '/real/codex',
      AGENT_STORE_PATH: '/real/agents',
      WORKSPACE_DIR: '/real/work',
      ORI_CLAUDE_CREDENTIALS_DIR: '/real/claude',
      ANTHROPIC_API_KEY: 'not-for-demo',
      OPENAI_API_KEY: 'not-for-demo'
    });
    assert.equal(env.CODEX_HOME, join(sandbox, '.codex'));
    for (const key of [
      'AGENT_STORE_PATH',
      'WORKSPACE_DIR',
      'ORI_CLAUDE_CREDENTIALS_DIR',
      'ANTHROPIC_API_KEY'
    ])
      assert.equal(env[key], undefined);
    assert.equal(env.OPENAI_BASE_URL, 'http://127.0.0.1:1234/v1/');
    assert.equal(env.OLLAMA_BASE_URL, 'http://127.0.0.1:1234');
    assert.equal(spawnSync('security', ['find-generic-password'], { env }).status, 44);
    assert.equal(spawnSync('codex', ['--version'], { env }).status, 1);
    assert.equal(existsSync(join(env.CODEX_HOME, 'auth.json')), extended);
    if (extended)
      assert.deepEqual(JSON.parse(readFileSync(join(env.CODEX_HOME, 'auth.json'), 'utf8')), {
        tokens: { access_token: 'fixture-not-a-real-token' }
      });
  }
});

test('controlled provider accepts only the bounded data-only call shape', async t => {
  const provider = await startLearningProvider();
  t.after(() => provider.close());
  const body = {
    model: 'gpt-4o-mini',
    max_completion_tokens: 1200,
    messages: [
      { role: 'system', content: 'Host rules' },
      { role: 'user', content: JSON.stringify({ reviewed_input: 'Synthetic text' }) }
    ]
  };
  const send = data =>
    fetch(provider.base + '/v1/chat/completions', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(data)
    });
  assert.equal((await send(body)).status, 200);
  assert.equal((await send({ ...body, tools: [{ type: 'function' }] })).status, 400);
  assert.equal((await send({ ...body, max_completion_tokens: 4000 })).status, 400);
  assert.equal((await fetch(provider.base + '/api/learning-missions')).status, 404);
  assert.equal(provider.calls(), 3);
});

test('extended fixture controls proposal and revision responses without accepting arbitrary context', async t => {
  const provider = await startLearningProvider({ extended: true });
  t.after(() => provider.close());
  const send = (input, model = 'fixture') =>
    fetch(provider.base + '/v1/chat/completions', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        model,
        max_tokens: 1200,
        messages: [
          { role: 'system', content: 'Host rules' },
          { role: 'user', content: JSON.stringify(input) }
        ]
      })
    });
  const context = {
    version: 1,
    explicit_effort: 'small',
    explicit_focus: '',
    explicit_guidance: 'guided',
    practice_coverage: 'unknown',
    observations: [],
    candidates: [
      {
        candidate_ref: 'opaque',
        structure: 'improve_result',
        version: 1,
        steps: ['selected', 'correction', 'run', 'compare']
      }
    ]
  };
  const proposal = await (await send(context)).json();
  assert.equal(JSON.parse(proposal.choices[0].message.content).candidate_ref, 'opaque');
  const invalid = await (await send(context, 'learning-invalid')).json();
  assert.equal(JSON.parse(invalid.choices[0].message.content).complete, true);
  const revision = await (
    await send({ original_result: 'Old welcome', reviewed_correction: 'Add a time' })
  ).json();
  assert.match(revision.choices[0].message.content, /10:00/);
  assert.equal((await send({ ...context, file_contents: 'not permitted' })).status, 400);
  assert.equal(
    (
      await send({
        ...context,
        practice_coverage: 'local_review_records_only',
        local_practice_counts: { delegate: 1, evaluate: 1 }
      })
    ).status,
    200
  );
  assert.equal(
    (await send({ ...context, local_practice_counts: { result_text: 'not permitted' } })).status,
    400
  );
  for (const [depth, limit] of [
    ['full', 3],
    ['brief', 1]
  ]) {
    const profile = await (
      await send({
        ...context,
        explicit_focus: 'Fictional maker meetup',
        coaching_depth: depth,
        max_coaching_hints: limit
      })
    ).json();
    const offered = JSON.parse(profile.choices[0].message.content);
    assert.equal(offered.goal, 'Draft a maker-meetup invitation');
    assert.equal(offered.hints.length, limit);
  }
  assert.equal(provider.calls(), 8);
});
