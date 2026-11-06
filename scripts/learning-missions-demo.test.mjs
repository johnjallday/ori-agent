import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, mkdirSync, writeFileSync, realpathSync, rmSync, symlinkSync } from 'node:fs';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import { verifyLearningSandbox, startLearningProvider } from './learning-missions-demo.mjs';

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
