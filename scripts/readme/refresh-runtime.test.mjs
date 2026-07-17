import assert from 'node:assert/strict';
import { spawn, spawnSync } from 'node:child_process';
import { existsSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import net from 'node:net';
import { tmpdir } from 'node:os';
import path from 'node:path';
import test from 'node:test';

const repoRoot = path.resolve(import.meta.dirname, '..', '..');
const stagingParent = path.join(repoRoot, 'test-results', 'readme-refresh');
const loopbackAvailable = await new Promise((resolve) => {
  const probe = net.createServer();
  probe.once('error', () => resolve(false));
  probe.listen({ host: '127.0.0.1', port: 0 }, () => {
    probe.close(() => resolve(true));
  });
});
const loopbackSkip = loopbackAvailable
  ? false
  : 'Local loopback binding is unavailable in this execution sandbox.';
const goRoot = spawnSync('go', ['env', 'GOROOT'], { encoding: 'utf8' }).stdout.trim();
const goModuleCache = spawnSync('go', ['env', 'GOMODCACHE'], { encoding: 'utf8' }).stdout.trim();
const fixtureRoot = sandboxRoot('server');
const fixtureServer = path.join(fixtureRoot, 'readme-fixture-server');
const fixtureBuild = spawnSync(
  path.join(goRoot, 'bin', 'go'),
  ['build', '-o', fixtureServer, 'tests/fixtures/readme-fixture-server.go'],
  {
    cwd: repoRoot,
    encoding: 'utf8',
    env: { ...process.env, GOROOT: goRoot, GOTOOLCHAIN: 'local', GOMODCACHE: goModuleCache },
  },
);
if (fixtureBuild.status !== 0) {
  throw new Error(`Could not build README lifecycle fixture server: ${fixtureBuild.stderr}`);
}

function runID(label) {
  return `runtime-${label}-${process.pid}-${Date.now()}`;
}

function sandboxRoot(label) {
  return mkdtempSync(path.join(tmpdir(), `readme-runtime-${label}-`));
}

function runCapture(id, driver, environment) {
  return spawnSync(
    'bash',
    ['scripts/readme-refresh.sh', 'capture', '--run-id', id, '--driver', driver],
    {
      cwd: repoRoot,
      env: environment,
      encoding: 'utf8',
      timeout: 120_000,
    },
  );
}

function cleanupRun(id) {
  return spawnSync('bash', ['scripts/readme-refresh.sh', 'cleanup', '--run-id', id], {
    cwd: repoRoot,
    encoding: 'utf8',
    timeout: 20_000,
  });
}

test(
  'capture uses an isolated sandbox, clears credentials, and preserves its staging contract',
  { timeout: 150_000, skip: loopbackSkip },
  () => {
    const outside = sandboxRoot('outside');
    const home = path.join(outside, 'home');
    const data = path.join(outside, 'ori-data');
    const sentinel = path.join(outside, 'sentinel.txt');
    const id = runID('success');
    const runDir = path.join(stagingParent, id);
    writeFileSync(sentinel, 'do not modify\n');
    const result = runCapture(id, 'tests/fixtures/readme-capture-driver.mjs', {
      ...process.env,
      HOME: home,
      ORI_DATA_DIR: data,
      OPENAI_API_KEY: 'sentinel-openai-key',
      ANTHROPIC_API_KEY: 'sentinel-anthropic-key',
      AWS_ACCESS_KEY_ID: 'sentinel-aws-key',
      README_CAPTURE_GOROOT: goRoot,
      README_CAPTURE_GOMODCACHE: goModuleCache,
      README_CAPTURE_TEST_MODE: '1',
      README_CAPTURE_TEST_SERVER_BINARY: fixtureServer,
    });

    try {
      assert.equal(result.status, 0, `${result.stderr}\n${result.stdout}`);
      assert.equal(readFileSync(sentinel, 'utf8'), 'do not modify\n');
      assert.equal(existsSync(home), false, 'the capture process must not write the caller HOME');
      assert.equal(existsSync(data), false, 'the capture process must not write the caller ORI_DATA_DIR');

      const metadata = JSON.parse(readFileSync(path.join(runDir, 'run.json'), 'utf8'));
      const probe = JSON.parse(readFileSync(path.join(runDir, 'sidecars', 'runtime-probe.json'), 'utf8'));
      assert.equal(metadata.status, 'succeeded');
      assert.equal(metadata.source_drift, false);
      assert.equal(metadata.tracked_fingerprints.before.digest, metadata.tracked_fingerprints.after.digest);
      assert.equal(metadata.scene_statuses[0].id, 'runtime-probe');
      assert.equal(probe.health.status, 'ok');
      assert.equal(probe.credential_present, false, 'fixture driver must receive the scrubbed environment');
      assert.equal(metadata.acceptance_eligible, false, 'a dirty source tree cannot be accepted later');
      assert.match(metadata.acceptance_blockers.join('\n'), /dirty at capture start/);
      assert.equal(existsSync(metadata.sandbox), true, 'review artifacts keep the temporary sandbox until explicit cleanup');
    } finally {
      if (existsSync(path.join(runDir, 'run.json'))) {
        const cleanup = cleanupRun(id);
        assert.equal(cleanup.status, 0, cleanup.stderr);
      }
      assert.equal(existsSync(runDir), false);
      rmSync(outside, { recursive: true, force: true });
    }
  },
);

test('failure preserves diagnostics and does not stop an unrelated process', { timeout: 150_000, skip: loopbackSkip }, async () => {
  const outside = sandboxRoot('failure');
  const id = runID('failure');
  const runDir = path.join(stagingParent, id);
  const unrelated = spawn(process.execPath, ['-e', 'setInterval(() => {}, 1000)'], { stdio: 'ignore' });
  const result = runCapture(id, 'tests/fixtures/readme-failing-driver.mjs', {
    ...process.env,
    HOME: path.join(outside, 'home'),
    ORI_DATA_DIR: path.join(outside, 'ori-data'),
    OPENAI_API_KEY: 'sentinel-openai-key',
    README_CAPTURE_GOROOT: goRoot,
    README_CAPTURE_GOMODCACHE: goModuleCache,
    README_CAPTURE_TEST_MODE: '1',
    README_CAPTURE_TEST_SERVER_BINARY: fixtureServer,
  });

  try {
    assert.equal(result.status, 7, `${result.stderr}\n${result.stdout}`);
    assert.equal(unrelated.exitCode, null, 'exact-PID cleanup must not affect an unrelated process');
    assert.equal(existsSync(path.join(runDir, 'logs', 'server.log')), true, 'failure must preserve server diagnostics');
    const metadata = JSON.parse(readFileSync(path.join(runDir, 'run.json'), 'utf8'));
    assert.equal(metadata.status, 'failed');
    assert.equal(metadata.server_pid > 1, true);
  } finally {
    unrelated.kill();
    if (existsSync(path.join(runDir, 'run.json'))) {
      const cleanup = cleanupRun(id);
      assert.equal(cleanup.status, 0, cleanup.stderr);
    }
    rmSync(outside, { recursive: true, force: true });
  }
});

test.after(() => {
  rmSync(fixtureRoot, { recursive: true, force: true });
});
