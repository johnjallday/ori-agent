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
import { startLearningDemoControl } from './learning-missions-demo-control.mjs';

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

export async function startLearningProvider({ extended = false } = {}) {
  let calls = 0;
  let holdArmed = false,
    heldRelease;
  const releaseHeld = () => {
    holdArmed = false;
    heldRelease?.();
    heldRelease = undefined;
  };
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
        calls > (extended ? 14 : 5) ||
        body.tools?.length ||
        body.stream ||
        maxTokens !== 1200 ||
        body.messages?.length !== 2 ||
        body.messages[1].role !== 'user'
      )
        throw new Error('isolated call contract');
      const input = JSON.parse(body.messages[1].content);
      let content =
        'Welcome to the workshop. Bring your curiosity and one small idea to share. We will begin with introductions, then write a short draft together.';
      const keys = Object.keys(input).sort().join(',');
      if (
        keys === 'reviewed_input' &&
        typeof input.reviewed_input === 'string' &&
        Buffer.byteLength(input.reviewed_input) <= 4096
      ) {
        // Plain supplied-text execution, never inherited context.
      } else if (
        extended &&
        keys === 'original_result,reviewed_correction' &&
        typeof input.original_result === 'string' &&
        Buffer.byteLength(input.original_result) <= 65536 &&
        typeof input.reviewed_correction === 'string' &&
        Buffer.byteLength(input.reviewed_correction) <= 4096
      ) {
        content =
          'Welcome to the workshop at 10:00. Bring your curiosity and one small idea to share.';
      } else if (
        extended &&
        [
          'candidates,explicit_effort,explicit_focus,explicit_guidance,observations,practice_coverage,version',
          'candidates,explicit_effort,explicit_focus,explicit_guidance,local_practice_counts,observations,practice_coverage,version',
          'candidates,coaching_depth,explicit_effort,explicit_focus,explicit_guidance,max_coaching_hints,observations,practice_coverage,version',
          'candidates,coaching_depth,explicit_effort,explicit_focus,explicit_guidance,local_practice_counts,max_coaching_hints,observations,practice_coverage,version'
        ].includes(keys) &&
        (input.coaching_depth === undefined ||
          (['full', 'brief', 'basic'].includes(input.coaching_depth) &&
            Number.isSafeInteger(input.max_coaching_hints) &&
            input.max_coaching_hints >= 0 &&
            input.max_coaching_hints <= 4 &&
            (input.coaching_depth !== 'brief' || input.max_coaching_hints === 1) &&
            (input.coaching_depth !== 'basic' || input.max_coaching_hints === 0))) &&
        Object.entries(input.local_practice_counts || {}).every(
          ([key, count]) =>
            ['delegate', 'evaluate', 'improve', 'reuse'].includes(key) &&
            Number.isSafeInteger(count) &&
            count >= 0 &&
            count <= 10000
        ) &&
        input.candidates?.length > 0 &&
        input.candidates.length <= 8
      ) {
        const candidate = input.candidates[0];
        const profile =
          input.explicit_focus === 'Fictional garden club'
            ? 'garden'
            : input.explicit_focus === 'Fictional maker meetup'
              ? 'maker'
              : '';
        const coaching = [
          'Name the invitation audience in your supplied text',
          'Keep the draft within the supplied details',
          'Compare each detail before deciding whether to use the result'
        ];
        content = JSON.stringify({
          candidate_ref: candidate.candidate_ref,
          structure: candidate.structure,
          version: candidate.version,
          steps: candidate.steps,
          goal: profile
            ? profile === 'garden'
              ? 'Draft a plant-swap invitation'
              : 'Draft a maker-meetup invitation'
            : candidate.structure === 'delegate_inspect'
              ? 'Draft a workshop invitation'
              : 'Refine a workshop welcome',
          scope: profile
            ? 'Use only the event details you supply'
            : candidate.structure === 'delegate_inspect'
              ? 'Use only the workshop text you supply'
              : 'Use the selected welcome and the correction you supply',
          why: profile
            ? 'This matches your explicit fictional event focus'
            : 'You selected this workshop workspace and a reviewed result',
          hints: profile ? coaching.slice(0, input.max_coaching_hints) : [],
          ...(body.model === 'learning-invalid' ? { complete: true } : {})
        });
      } else throw new Error('data projection');
      if (holdArmed) {
        holdArmed = false;
        await new Promise(done => {
          heldRelease = done;
        });
      }
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
                content
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
    held: () => Boolean(heldRelease),
    holdNext: () => {
      if (holdArmed || heldRelease) throw new Error('One hold only');
      holdArmed = true;
    },
    releaseHeld,
    close: async () => {
      releaseHeld();
      server.closeAllConnections();
      await new Promise(done => server.close(done));
    }
  };
}

export function learningDemoEnvironment(
  sandbox,
  providerBase,
  extended = false,
  inherited = process.env
) {
  const env = { ...inherited };
  for (const key of [
    'OPENAI_API_KEY',
    'ANTHROPIC_API_KEY',
    'GEMINI_API_KEY',
    'GOOGLE_API_KEY',
    'CLAUDE_CODE_OAUTH_TOKEN',
    'ANTHROPIC_AUTH_TOKEN',
    'ORI_CLAUDE_CREDENTIALS_DIR',
    'AGENT_STORE_PATH',
    'WORKSPACE_DIR',
    'WORKFLOW_TEMPLATES_DIR',
    'ORI_DEV_SCRIPTED_TASK_RUNS',
    'ORI_LEARNING_DEMO_CONTROL',
    'ORI_LEARNING_DEMO_CONTROL_TOKEN',
    'ORI_TEMPLATES_DIR',
    'ORI_VAULT_DIR'
  ])
    delete env[key];
  const bin = join(sandbox, 'bin');
  const codexHome = join(sandbox, '.codex');
  mkdirSync(bin, { mode: 0o750 });
  mkdirSync(codexHome, { mode: 0o750 });
  // HOME alone does not isolate macOS Keychain or an inherited CODEX_HOME.
  writeFileSync(join(bin, 'security'), '#!/bin/sh\nexit 44\n', { mode: 0o750 });
  for (const name of ['codex', 'claude'])
    writeFileSync(
      join(bin, name),
      '#!/bin/sh\nprintf "%s\\n" "$*" >> "$ORI_LEARNING_CLI_LOG"\nexit 1\n',
      { mode: 0o750 }
    );
  writeFileSync(join(sandbox, 'cli-probes.log'), '', { mode: 0o600 });
  if (extended)
    writeFileSync(
      join(codexHome, 'auth.json'),
      JSON.stringify({ tokens: { access_token: 'fixture-not-a-real-token' } }),
      { mode: 0o600 }
    );
  Object.assign(env, {
    PATH: `${bin}:${env.PATH || '/usr/bin:/bin'}`,
    CODEX_HOME: codexHome,
    CLAUDE_CONFIG_DIR: join(sandbox, '.claude'),
    ORI_LEARNING_CLI_LOG: join(sandbox, 'cli-probes.log'),
    OPENAI_API_KEY: 'local-fixture-not-a-real-key',
    OPENAI_BASE_URL: `${providerBase}/v1/`,
    OLLAMA_BASE_URL: providerBase,
    LM_STUDIO_BASE_URL: providerBase,
    MLX_LM_BASE_URL: providerBase,
    ORI_DEMO_OPEN: '0',
    ORI_DEMO_NO_CODEX: '1'
  });
  return env;
}

async function run() {
  const root = process.cwd();
  mkdirSync(join(root, 'tmp'), { recursive: true, mode: 0o750 });
  const sandbox = mkdtempSync(join(root, 'tmp', 'ori-learning-demo-'));
  const final = process.argv.includes('--final');
  const group = final
    ? '4'
    : process.argv.includes('--group=6')
      ? '6'
      : process.argv.includes('--group=5')
        ? '5'
        : process.argv.includes('--group=4')
          ? '4'
          : process.argv.includes('--group=3')
            ? '3'
            : '2';
  const extended = group !== '2';
  const args = process.argv.slice(2);
  const browser =
    args.find(arg => arg.startsWith('--browser='))?.slice('--browser='.length) || 'chromium';
  if (
    args.some(
      arg =>
        ![
          '--group=3',
          '--group=4',
          '--group=5',
          '--group=6',
          '--final',
          '--browser=chromium',
          '--browser=firefox',
          '--browser=webkit'
        ].includes(arg)
    ) ||
    (final && args.some(arg => arg.startsWith('--group='))) ||
    args.filter(arg => arg.startsWith('--group=')).length > 1 ||
    args.filter(arg => arg.startsWith('--browser=')).length > 1 ||
    (browser !== 'chromium' && group !== '6')
  )
    throw new Error(
      'Usage: node scripts/learning-missions-demo.mjs [--final|--group=3|--group=4|--group=5|--group=6] [--browser=chromium|firefox|webkit (group 6 only)]'
    );
  if (group === '6' && !process.env.AXE_CORE_FILE)
    throw new Error('Group 6 requires AXE_CORE_FILE pointing to the pinned axe-core audit asset.');
  const evidence = resolve(
    root,
    `tasks/evidence-adaptive-learning-missions/${final ? 'final' : `group-${group}`}`,
    browser === 'chromium' ? '.' : browser
  );
  mkdirSync(evidence, { recursive: true, mode: 0o750 });
  const provider = await startLearningProvider({ extended });
  const portProbe = http.createServer();
  await new Promise(done => portProbe.listen(0, '127.0.0.1', done));
  const port = portProbe.address().port;
  await new Promise(done => portProbe.close(done));
  const base = `http://127.0.0.1:${port}`;
  const env = learningDemoEnvironment(sandbox, provider.base, extended);
  let child, stopped;
  const launch = () => {
    const log = openSync(join(sandbox, 'server.log'), 'a', 0o600);
    child = spawn('./scripts/demo-server.sh', [String(port), sandbox], {
      cwd: root,
      env,
      stdio: ['ignore', log, log]
    });
    closeSync(log);
    stopped = new Promise(done => {
      child.once('exit', done);
      child.once('error', done);
    });
  };
  const stop = async () => {
    if (!child) return;
    const owned = child;
    owned.kill('SIGTERM');
    const timer = setTimeout(() => owned.kill('SIGKILL'), 10000);
    await stopped;
    clearTimeout(timer);
  };
  launch();
  let testProcess;
  let cancelled = false;
  const cancel = () => {
    cancelled = true;
    child.kill('SIGTERM');
    testProcess?.kill('SIGTERM');
  };
  process.once('SIGINT', cancel);
  process.once('SIGTERM', cancel);
  const waitReady = async () => {
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
  };
  let control;
  try {
    await waitReady();
    if (group === '5')
      control = await startLearningDemoControl({
        provider,
        restart: async () => {
          verifyLearningSandbox(base, sandbox, root);
          await stop();
          if (cancelled) throw new Error('Cancelled');
          launch();
          await waitReady();
        }
      });
    const testEnv = {
      ...env,
      PLAYWRIGHT_BASE_URL: base,
      ORI_LEARNING_DEMO_SANDBOX: sandbox,
      ORI_LEARNING_DEMO_PROVIDER: provider.base,
      ORI_LEARNING_DEMO_EVIDENCE: evidence,
      ORI_LEARNING_DEMO_GROUP: group,
      ...(control
        ? {
            ORI_LEARNING_DEMO_CONTROL: control.base,
            ORI_LEARNING_DEMO_CONTROL_TOKEN: control.token
          }
        : {})
    };
    const test = spawn(
      process.execPath,
      [
        'node_modules/@playwright/test/cli.js',
        'test',
        group === '6'
          ? 'tests/adaptive-learning-layout.spec.ts'
          : 'tests/adaptive-learning-missions.spec.ts',
        ...(group === '6' ? ['--config=playwright.learning.config.ts'] : []),
        `--project=${browser}`,
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
    if (
      provider.calls() !==
      (group === '6' ? 1 : group === '5' ? 4 : group === '4' ? 14 : extended ? 6 : 1)
    )
      throw new Error(`Unexpected controlled calls: ${provider.calls()}`);
    if (
      extended &&
      readFileSync(join(sandbox, 'cli-probes.log'), 'utf8')
        .split('\n')
        .some(line => /(^|\s)exec(\s|$)/u.test(line))
    )
      throw new Error('A CLI execution was attempted');
    writeFileSync(
      join(evidence, 'controlled-provider-report.json'),
      JSON.stringify(
        {
          calls: provider.calls(),
          provider: 'local scripted fixture',
          browser,
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
    await control?.close();
    provider.releaseHeld();
    await stop();
    await provider.close();
  }
}

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  run().catch(error => {
    console.error(error.message);
    process.exitCode = 1;
  });
}
