import assert from 'node:assert/strict';
import { spawn, spawnSync } from 'node:child_process';
import { existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from 'node:fs';
import net from 'node:net';
import { tmpdir } from 'node:os';
import path from 'node:path';
import test from 'node:test';

const repoRoot = path.resolve(import.meta.dirname, '..', '..');
const stagingParent = path.join(repoRoot, 'test-results', 'readme-refresh');
const sourceWasDirty = spawnSync('git', ['status', '--porcelain', '--untracked-files=no'], {
  cwd: repoRoot,
  encoding: 'utf8',
}).stdout.trim() !== '';
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
const playwrightExecutable = spawnSync(
  process.execPath,
  ['--input-type=module', '-e', 'import { chromium } from "playwright"; process.stdout.write(chromium.executablePath());'],
  { cwd: repoRoot, encoding: 'utf8' },
).stdout.trim();
let playwrightCachePath = playwrightExecutable;
while (path.basename(playwrightCachePath) !== path.parse(playwrightCachePath).root && !path.basename(playwrightCachePath).startsWith('chromium-')) {
  playwrightCachePath = path.dirname(playwrightCachePath);
}
if (!path.basename(playwrightCachePath).startsWith('chromium-') || !existsSync(path.dirname(playwrightCachePath))) {
  throw new Error('Could not resolve the installed Playwright Chromium cache for README runtime tests.');
}
const playwrightBrowsersPath = path.dirname(playwrightCachePath);
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

function createProtectedRoots(outside) {
  const roots = {
    home: path.join(outside, 'home'),
    data: path.join(outside, 'ori-data'),
    workspace: path.join(outside, 'workspace-root'),
    vault: path.join(outside, 'vault'),
    plugins: path.join(outside, 'plugins'),
  };
  for (const location of Object.values(roots)) {
    mkdirSync(location, { recursive: true });
    writeFileSync(path.join(location, 'sentinel.txt'), 'do not modify\n');
  }
  return roots;
}

function assertProtectedRootsUnchanged(roots) {
  for (const [name, location] of Object.entries(roots)) {
    assert.equal(readFileSync(path.join(location, 'sentinel.txt'), 'utf8'), 'do not modify\n', `${name} sentinel must remain unchanged`);
    assert.deepEqual(readdirSync(location).sort(), ['sentinel.txt'], `${name} location must not receive capture files`);
  }
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
    const protectedRoots = createProtectedRoots(outside);
    const sentinel = path.join(outside, 'sentinel.txt');
    const id = runID('success');
    const runDir = path.join(stagingParent, id);
    writeFileSync(sentinel, 'do not modify\n');
    const result = runCapture(id, 'tests/fixtures/readme-capture-driver.mjs', {
      ...process.env,
      HOME: protectedRoots.home,
      ORI_DATA_DIR: protectedRoots.data,
      ORI_WORKSPACE_ROOT: protectedRoots.workspace,
      ORI_VAULT_DIR: protectedRoots.vault,
      ORI_PLUGIN_DIR: protectedRoots.plugins,
      OPENAI_API_KEY: 'sentinel-openai-key',
      ANTHROPIC_API_KEY: 'sentinel-anthropic-key',
      AWS_ACCESS_KEY_ID: 'sentinel-aws-key',
      README_CAPTURE_GOROOT: goRoot,
      README_CAPTURE_GOMODCACHE: goModuleCache,
      PLAYWRIGHT_BROWSERS_PATH: playwrightBrowsersPath,
      README_CAPTURE_TEST_MODE: '1',
      README_CAPTURE_TEST_SERVER_BINARY: fixtureServer,
    });

    try {
      assert.equal(result.status, 0, `${result.stderr}\n${result.stdout}`);
      assert.equal(readFileSync(sentinel, 'utf8'), 'do not modify\n');
      assertProtectedRootsUnchanged(protectedRoots);

      const metadata = JSON.parse(readFileSync(path.join(runDir, 'run.json'), 'utf8'));
      const probe = JSON.parse(readFileSync(path.join(runDir, 'sidecars', 'runtime-probe.json'), 'utf8'));
      assert.equal(metadata.status, 'succeeded');
      assert.equal(metadata.source_drift, false);
      assert.equal(metadata.tracked_fingerprints.before.digest, metadata.tracked_fingerprints.after.digest);
      assert.equal(metadata.scene_statuses[0].id, 'runtime-probe');
      assert.equal(probe.health.status, 'ok');
      assert.equal(probe.credential_present, false, 'fixture driver must receive the scrubbed environment');
      if (sourceWasDirty) {
        assert.equal(metadata.acceptance_eligible, false, 'a dirty source tree cannot be accepted later');
        assert.match(metadata.acceptance_blockers.join('\n'), /dirty at capture start/);
      } else {
        assert.equal(metadata.acceptance_eligible, true, 'a clean source tree should preserve acceptance eligibility');
        assert.doesNotMatch(metadata.acceptance_blockers.join('\n'), /dirty at capture start/);
      }
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
  const protectedRoots = createProtectedRoots(outside);
  const id = runID('failure');
  const runDir = path.join(stagingParent, id);
  const unrelated = spawn(process.execPath, ['-e', 'setInterval(() => {}, 1000)'], { stdio: 'ignore' });
  const result = runCapture(id, 'tests/fixtures/readme-failing-driver.mjs', {
    ...process.env,
    HOME: protectedRoots.home,
    ORI_DATA_DIR: protectedRoots.data,
    ORI_WORKSPACE_ROOT: protectedRoots.workspace,
    ORI_VAULT_DIR: protectedRoots.vault,
    ORI_PLUGIN_DIR: protectedRoots.plugins,
    OPENAI_API_KEY: 'sentinel-openai-key',
    README_CAPTURE_GOROOT: goRoot,
    README_CAPTURE_GOMODCACHE: goModuleCache,
    PLAYWRIGHT_BROWSERS_PATH: playwrightBrowsersPath,
    README_CAPTURE_TEST_MODE: '1',
    README_CAPTURE_TEST_SERVER_BINARY: fixtureServer,
  });

  try {
    assert.equal(result.status, 2, `${result.stderr}\n${result.stdout}`);
    assert.equal(unrelated.exitCode, null, 'exact-PID cleanup must not affect an unrelated process');
    assert.equal(existsSync(path.join(runDir, 'logs', 'server.log')), true, 'failure must preserve server diagnostics');
    assert.match(result.stderr, /scene=runtime-probe rule=fixture-failure/);
    assert.match(result.stderr, /Safe retry: bash scripts\/readme-refresh\.sh cleanup --run-id/);
    assertProtectedRootsUnchanged(protectedRoots);
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
