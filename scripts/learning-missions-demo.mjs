// Disposable production-server/browser fixture. Controls only provider replies;
// no application endpoint mocks, installed data, real credentials or paid calls.
import http from 'node:http';
import { spawn } from 'node:child_process';
import {
  mkdirSync,
  mkdtempSync,
  openSync,
  closeSync,
  writeFileSync,
  readFileSync,
  realpathSync
} from 'node:fs';
import { resolve, join, sep } from 'node:path';
import { pathToFileURL } from 'node:url';

export function verifyLearningSandbox(base, sandbox, root) {
  const url = new URL(base);
  if (
    url.protocol !== 'http:' ||
    url.hostname !== '127.0.0.1' ||
    !url.port ||
    url.pathname !== '/' ||
    url.search ||
    url.hash ||
    url.username ||
    url.password
  )
    throw new Error('A loopback demo URL is required');
  const path = realpathSync(resolve(sandbox));
  const canonicalRoot = realpathSync(root);
  if (
    !path.startsWith(resolve(canonicalRoot, 'tmp') + sep) ||
    readFileSync(join(path, '.ori-demo-owner'), 'utf8').trim() !== `worktree=${canonicalRoot}`
  )
    throw new Error('A sandbox owned by this worktree is required');
  return path;
}

export async function startLearningProvider() {
  let calls = 0;
  const server = http.createServer(async (request, response) => {
    response.setHeader('Content-Type', 'application/json');
    if (request.url === '/counts' && request.method === 'GET') {
      response.end(JSON.stringify({ calls }));
      return;
    }
    if (request.url !== '/v1/chat/completions' || request.method !== 'POST') {
      response.writeHead(404);
      response.end('{}');
      return;
    }
    try {
      let text = '';
      for await (const chunk of request) {
        text += chunk;
        if (Buffer.byteLength(text) > 32768) throw new Error('input cap');
      }
      const body = JSON.parse(text);
      calls++;
      const maxTokens = body.max_completion_tokens || body.max_tokens;
      if (
        calls > 5 ||
        body.tools?.length ||
        body.stream ||
        maxTokens !== 1200 ||
        body.messages?.length !== 2 ||
        body.messages[1].role !== 'user'
      )
        throw new Error('isolated call contract');
      const input = JSON.parse(body.messages[1].content);
      if (
        Object.keys(input).join(',') !== 'reviewed_input' ||
        Buffer.byteLength(input.reviewed_input) > 4096
      )
        throw new Error('data projection');
      response.end(
        JSON.stringify({
          id: `learning-demo-${calls}`,
          object: 'chat.completion',
          created: Math.floor(Date.now() / 1000),
          model: body.model,
          choices: [
            {
              index: 0,
              message: {
                role: 'assistant',
                content:
                  'Welcome to the workshop. Bring your curiosity and one small idea to share. We will begin with introductions, then write a short draft together.'
              },
              finish_reason: 'stop'
            }
          ],
          usage: { prompt_tokens: 80, completion_tokens: 35, total_tokens: 115 }
        })
      );
    } catch {
      response.writeHead(400);
      response.end(JSON.stringify({ error: { message: 'Controlled provider contract refused' } }));
    }
  });
  await new Promise((done, fail) => {
    server.once('error', fail);
    server.listen(0, '127.0.0.1', done);
  });
  const base = `http://127.0.0.1:${server.address().port}`;
  return {
    base,
    calls: () => calls,
    close: async () => {
      server.closeAllConnections();
      await new Promise(done => server.close(done));
    }
  };
}

async function run() {
  const root = process.cwd();
  mkdirSync(join(root, 'tmp'), { recursive: true, mode: 0o750 });
  const sandbox = mkdtempSync(join(root, 'tmp', 'ori-learning-demo-'));
  const evidence = resolve(root, 'tasks/evidence-adaptive-learning-missions/group-2');
  mkdirSync(evidence, { recursive: true, mode: 0o750 });
  const provider = await startLearningProvider();
  const portProbe = http.createServer();
  await new Promise(done => portProbe.listen(0, '127.0.0.1', done));
  const port = portProbe.address().port;
  await new Promise(done => portProbe.close(done));
  const base = `http://127.0.0.1:${port}`;
  const env = { ...process.env };
  for (const key of [
    'OPENAI_API_KEY',
    'ANTHROPIC_API_KEY',
    'GEMINI_API_KEY',
    'GOOGLE_API_KEY',
    'ORI_DEV_SCRIPTED_TASK_RUNS',
    'ORI_TEMPLATES_DIR',
    'ORI_VAULT_DIR'
  ])
    delete env[key];
  Object.assign(env, {
    OPENAI_API_KEY: 'local-fixture-not-a-real-key',
    OPENAI_BASE_URL: `${provider.base}/v1/`,
    ORI_DEMO_OPEN: '0',
    ORI_DEMO_NO_CODEX: '1'
  });
  const log = openSync(join(sandbox, 'server.log'), 'w', 0o600);
  const child = spawn('./scripts/demo-server.sh', [String(port), sandbox], {
    cwd: root,
    env,
    stdio: ['ignore', log, log]
  });
  closeSync(log);
  const stopped = new Promise(done => {
    child.once('exit', done);
    child.once('error', done);
  });
  let testProcess;
  let cancelled = false;
  const cancel = () => {
    cancelled = true;
    child.kill('SIGTERM');
    testProcess?.kill('SIGTERM');
  };
  process.once('SIGINT', cancel);
  process.once('SIGTERM', cancel);
  try {
    const deadline = Date.now() + 180000;
    let ready = false;
    while (!cancelled && Date.now() < deadline && child.exitCode === null) {
      try {
        const response = await fetch(`${base}/api/learning-missions`);
        if (response.ok) {
          ready = true;
          break;
        }
      } catch {
        /* build/start pending */
      }
      await new Promise(done => setTimeout(done, 300));
    }
    if (!ready) throw new Error(`Demo did not become ready; inspect ${sandbox}/server.log`);
    verifyLearningSandbox(base, sandbox, root);
    const testEnv = {
      ...env,
      PLAYWRIGHT_BASE_URL: base,
      ORI_LEARNING_DEMO_SANDBOX: sandbox,
      ORI_LEARNING_DEMO_PROVIDER: provider.base,
      ORI_LEARNING_DEMO_EVIDENCE: evidence
    };
    const test = spawn(
      process.execPath,
      [
        'node_modules/@playwright/test/cli.js',
        'test',
        'tests/adaptive-learning-missions.spec.ts',
        '--project=chromium',
        '--workers=1',
        '--retries=0'
      ],
      { cwd: root, env: testEnv, stdio: 'inherit' }
    );
    testProcess = test;
    const code = await new Promise((done, fail) => {
      test.once('exit', done);
      test.once('error', fail);
    });
    if (code !== 0)
      throw new Error(`Browser checks failed (${code}); sandbox retained: ${sandbox}`);
    if (provider.calls() !== 1) throw new Error(`Unexpected controlled calls: ${provider.calls()}`);
    writeFileSync(
      join(evidence, 'controlled-provider-report.json'),
      JSON.stringify(
        {
          calls: provider.calls(),
          provider: 'local scripted fixture',
          live_quality: 'NOT RUN',
          sandbox,
          base
        },
        null,
        2
      ) + '\n',
      { mode: 0o600 }
    );
    console.log(`Evidence: ${evidence}\nSandbox retained: ${sandbox}`);
  } finally {
    process.removeListener('SIGINT', cancel);
    process.removeListener('SIGTERM', cancel);
    child.kill('SIGTERM');
    const timer = setTimeout(() => child.kill('SIGKILL'), 10000);
    await stopped;
    clearTimeout(timer);
    await provider.close();
  }
}

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  run().catch(error => {
    console.error(error.message);
    process.exitCode = 1;
  });
}
