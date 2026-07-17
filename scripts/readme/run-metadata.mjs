#!/usr/bin/env node

import { createHash } from 'node:crypto';
import { execFileSync } from 'node:child_process';
import { existsSync, lstatSync, mkdirSync, readFileSync, rmSync, statSync, writeFileSync } from 'node:fs';
import { homedir, tmpdir } from 'node:os';
import path from 'node:path';

const RUN_PARENT = path.join('test-results', 'readme-refresh');
const TRACKED_PATHS = ['README.md', 'docs/images', 'docs/readme-screenshots.json'];

function fail(message) {
  throw new Error(message);
}

function parseArgs(argv) {
  const [command, ...rest] = argv;
  const values = {};
  for (let index = 0; index < rest.length; index += 2) {
    const key = rest[index];
    const value = rest[index + 1];
    if (!key?.startsWith('--') || value === undefined) {
      fail('Expected --key value arguments.');
    }
    values[key.slice(2)] = value;
  }
  return { command, values };
}

function sha256(value) {
  return createHash('sha256').update(value).digest('hex');
}

function stableJson(value) {
  if (Array.isArray(value)) {
    return `[${value.map(stableJson).join(',')}]`;
  }
  if (value && typeof value === 'object') {
    return `{${Object.keys(value)
      .sort()
      .map((key) => `${JSON.stringify(key)}:${stableJson(value[key])}`)
      .join(',')}}`;
  }
  return JSON.stringify(value);
}

function resolveRepoRoot(value) {
  if (!value) fail('A repository root is required.');
  const root = path.resolve(value);
  if (!existsSync(path.join(root, '.git')) || !existsSync(path.join(root, 'README.md'))) {
    fail(`Not an Ori repository root: ${root}`);
  }
  return root;
}

function assertRunID(runID) {
  if (!/^[A-Za-z0-9][A-Za-z0-9._-]{0,79}$/.test(runID) || runID.includes('..')) {
    fail(`Unsafe run ID: ${JSON.stringify(runID)}`);
  }
  return runID;
}

function expectedRunDir(repoRoot, runID) {
  const parent = path.resolve(repoRoot, RUN_PARENT);
  const runDir = path.resolve(parent, assertRunID(runID));
  if (path.dirname(runDir) !== parent) {
    fail(`Run directory escapes staging parent: ${runDir}`);
  }
  return runDir;
}

function assertRunDir(repoRoot, runDir, runID) {
  const expected = expectedRunDir(repoRoot, runID);
  if (path.resolve(runDir) !== expected) {
    fail(`Run directory must be exactly ${expected}.`);
  }
  return expected;
}

function git(repoRoot, args) {
  return execFileSync('git', args, { cwd: repoRoot, encoding: 'utf8' }).trim();
}

function gitState(repoRoot) {
  return {
    commit: git(repoRoot, ['rev-parse', 'HEAD']),
    dirty: git(repoRoot, ['status', '--porcelain', '--untracked-files=no']) !== '',
  };
}

function trackedFiles(repoRoot) {
  return git(repoRoot, ['ls-files', '--', ...TRACKED_PATHS])
    .split('\n')
    .filter(Boolean)
    .sort();
}

function trackedFingerprint(repoRoot) {
  const files = trackedFiles(repoRoot).map((relativePath) => {
    const absolutePath = path.resolve(repoRoot, relativePath);
    if (!absolutePath.startsWith(`${repoRoot}${path.sep}`)) {
      fail(`Tracked path escapes repository root: ${relativePath}`);
    }
    if (!existsSync(absolutePath)) {
      return { path: relativePath, sha256: null, bytes: null, state: 'missing' };
    }
    const stat = lstatSync(absolutePath);
    if (!stat.isFile()) {
      return { path: relativePath, sha256: null, bytes: null, state: 'not-file' };
    }
    const bytes = readFileSync(absolutePath);
    return { path: relativePath, sha256: sha256(bytes), bytes: bytes.byteLength, state: 'present' };
  });
  return { digest: sha256(stableJson(files)), files };
}

function readJson(filePath) {
  try {
    return JSON.parse(readFileSync(filePath, 'utf8'));
  } catch (error) {
    fail(`Could not read ${filePath}: ${error.message}`);
  }
}

function writeJson(filePath, value) {
  mkdirSync(path.dirname(filePath), { recursive: true });
  writeFileSync(filePath, `${JSON.stringify(value, null, 2)}\n`);
}

function packageVersion(relativePath) {
  try {
    return readJson(new URL(relativePath, import.meta.url)).version ?? null;
  } catch {
    return null;
  }
}

function playwrightVersion(repoRoot) {
  const packagePath = path.join(repoRoot, 'node_modules', '@playwright', 'test', 'package.json');
  return existsSync(packagePath) ? readJson(packagePath).version ?? null : null;
}

function readSceneStatuses(runDir) {
  const statusPath = path.join(runDir, 'scene-statuses.json');
  if (!existsSync(statusPath)) return [];
  const statuses = readJson(statusPath);
  if (!Array.isArray(statuses)) fail(`scene-statuses.json must be an array: ${statusPath}`);
  return statuses;
}

function init(values) {
  const repoRoot = resolveRepoRoot(values['repo-root']);
  const runID = assertRunID(values['run-id']);
  const runDir = assertRunDir(repoRoot, values['run-dir'], runID);
  const sandbox = path.resolve(values.sandbox ?? '');
  if (!sandbox || sandbox === path.parse(sandbox).root || sandbox === repoRoot || sandbox === homedir()) {
    fail(`Unsafe sandbox path: ${JSON.stringify(values.sandbox)}`);
  }
  const state = gitState(repoRoot);
  const metadata = {
    schema_version: 1,
    run_id: runID,
    source_commit: state.commit,
    dirty_state: state.dirty,
    started_at: new Date().toISOString(),
    ended_at: null,
    status: 'running',
    platform: process.platform,
    architecture: process.arch,
    node_version: process.version,
    playwright_version: playwrightVersion(repoRoot),
    chromium_version: null,
    encoder_version: null,
    port: Number(values.port),
    server_pid: null,
    sandbox,
    log_path: values['log-path'] ?? null,
    driver: values.driver ?? null,
    scene_statuses: [],
    tracked_fingerprints: { before: trackedFingerprint(repoRoot), after: null },
    source_drift: null,
    source_drift_details: [],
    acceptance_eligible: false,
    acceptance_blockers: state.dirty ? ['source worktree is dirty at capture start'] : [],
  };
  writeJson(path.join(runDir, 'run.json'), metadata);
}

function updatePid(values) {
  const runPath = path.resolve(values['run-dir'] ?? '');
  const metadataPath = path.join(runPath, 'run.json');
  const metadata = readJson(metadataPath);
  const pid = Number(values.pid);
  if (!Number.isInteger(pid) || pid <= 1) fail(`Invalid server PID: ${values.pid}`);
  metadata.server_pid = pid;
  writeJson(metadataPath, metadata);
}

function finalize(values) {
  const repoRoot = resolveRepoRoot(values['repo-root']);
  const runID = assertRunID(values['run-id']);
  const runDir = assertRunDir(repoRoot, values['run-dir'], runID);
  const metadataPath = path.join(runDir, 'run.json');
  const metadata = readJson(metadataPath);
  const state = gitState(repoRoot);
  const after = trackedFingerprint(repoRoot);
  const changes = [];
  if (metadata.source_commit !== state.commit) {
    changes.push(`source commit changed from ${metadata.source_commit} to ${state.commit}`);
  }
  if (metadata.tracked_fingerprints.before.digest !== after.digest) {
    changes.push('README contract tracked-file fingerprint changed during capture');
  }
  if (metadata.dirty_state !== state.dirty) {
    changes.push(`Git dirty state changed from ${metadata.dirty_state} to ${state.dirty}`);
  }
  const acceptanceBlockers = [...(metadata.dirty_state ? ['source worktree is dirty at capture start'] : [])];
  if (state.dirty) acceptanceBlockers.push('source worktree is dirty at capture end');
  acceptanceBlockers.push(...changes);
  metadata.ended_at = new Date().toISOString();
  metadata.status = values.status === 'succeeded' && changes.length === 0 ? 'succeeded' : 'failed';
  metadata.final_commit = state.commit;
  metadata.final_dirty_state = state.dirty;
  metadata.tracked_fingerprints.after = after;
  metadata.scene_statuses = readSceneStatuses(runDir);
  metadata.source_drift = changes.length > 0;
  metadata.source_drift_details = changes;
  metadata.acceptance_eligible = metadata.status === 'succeeded' && acceptanceBlockers.length === 0;
  metadata.acceptance_blockers = [...new Set(acceptanceBlockers)];
  writeJson(metadataPath, metadata);
}

function safeSandboxPath(value) {
  const sandbox = path.resolve(value ?? '');
  const tempRoot = path.resolve(tmpdir());
  if (
    !sandbox ||
    sandbox === path.parse(sandbox).root ||
    sandbox === tempRoot ||
    sandbox === homedir() ||
    path.dirname(sandbox) !== tempRoot ||
    !path.basename(sandbox).startsWith('ori-readme-capture.')
  ) {
    fail(`Refusing unsafe sandbox cleanup target: ${JSON.stringify(value)}`);
  }
  return sandbox;
}

function cleanup(values) {
  const repoRoot = resolveRepoRoot(values['repo-root']);
  const runID = assertRunID(values['run-id']);
  const runDir = expectedRunDir(repoRoot, runID);
  const metadataPath = path.join(runDir, 'run.json');
  if (!existsSync(metadataPath)) fail(`No staged run metadata exists for ${runID}.`);
  const metadata = readJson(metadataPath);
  const sandbox = safeSandboxPath(metadata.sandbox);
  if (existsSync(runDir)) rmSync(runDir, { recursive: true, force: false });
  if (existsSync(sandbox)) rmSync(sandbox, { recursive: true, force: false });
}

try {
  const { command, values } = parseArgs(process.argv.slice(2));
  if (command === 'init') init(values);
  else if (command === 'record-pid') updatePid(values);
  else if (command === 'finalize') finalize(values);
  else if (command === 'cleanup') cleanup(values);
  else fail(`Unknown command: ${command ?? '(missing)'}`);
} catch (error) {
  process.stderr.write(`README run metadata error: ${error.message}\n`);
  process.exitCode = 2;
}
