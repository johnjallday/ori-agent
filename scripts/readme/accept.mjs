#!/usr/bin/env node

import { createHash } from 'node:crypto';
import { execFileSync } from 'node:child_process';
import { copyFileSync, existsSync, readFileSync, rmSync, unlinkSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { pathToFileURL } from 'node:url';

import {
  REQUIRED_SCREENSHOT_IDS,
  loadManifest,
  readImageMetadata,
  resolveRepositoryPath,
  sha256,
  validateManifest,
  validateReadmeContent,
  validateRepository,
} from './manifest.mjs';

const RUN_PARENT = path.join('test-results', 'readme-refresh');
const BOOTSTRAP_BRANCH = 'feature/readme-steward';
const REFRESH_BRANCH_RE = /^docs\/readme-refresh-\d{4}-\d{2}$/;
const OBSOLETE_ASSETS = [
  'docs/images/hero.png',
  'docs/images/action-center.png',
  'docs/images/onboarding.png',
  'docs/images/workspace.png',
];

function fail(message) {
  throw new Error(message);
}

function parseArgs(argv) {
  const values = {};
  for (let index = 0; index < argv.length; index += 1) {
    const key = argv[index];
    if (!key?.startsWith('--')) fail(`Unexpected argument: ${key}`);
    if (key === '--approve') {
      values.approve = true;
      continue;
    }
    const value = argv[index + 1];
    if (value === undefined || value.startsWith('--')) fail(`Expected a value after ${key}.`);
    values[key.slice(2)] = value;
    index += 1;
  }
  return values;
}

function assertRunID(value) {
  if (!/^[A-Za-z0-9][A-Za-z0-9._-]{0,79}$/.test(value || '') || value.includes('..')) {
    fail(`Unsafe run ID: ${JSON.stringify(value)}`);
  }
  return value;
}

function digest(bytes) {
  return createHash('sha256').update(bytes).digest('hex');
}

function readJson(filePath, label) {
  if (!existsSync(filePath)) fail(`${label} is missing: ${filePath}`);
  try {
    return JSON.parse(readFileSync(filePath, 'utf8'));
  } catch (error) {
    fail(`${label} is not valid JSON: ${error.message}`);
  }
}

function runGit(root, args) {
  try {
    return execFileSync('git', args, { cwd: root, encoding: 'utf8' }).trim();
  } catch (error) {
    fail(`Git ${args.join(' ')} failed: ${error.stderr?.trim() || error.message}`);
  }
}

function branchGuard(root, manifest) {
  const branch = runGit(root, ['branch', '--show-current']);
  if (manifest.acceptance_state === 'bootstrap') {
    if (branch !== BOOTSTRAP_BRANCH) {
      fail(`Bootstrap acceptance is allowed only on ${BOOTSTRAP_BRANCH}, not ${branch || 'detached HEAD'}.`);
    }
    return { branch, mode: 'bootstrap' };
  }
  if (!REFRESH_BRANCH_RE.test(branch)) {
    fail(`Routine acceptance requires a docs/readme-refresh-YYYY-MM branch, not ${branch || 'detached HEAD'}.`);
  }
  return { branch, mode: 'routine' };
}

function assertCleanTrackedWorktree(root) {
  const status = runGit(root, ['status', '--porcelain', '--untracked-files=no']);
  if (status) fail('Acceptance requires a clean tracked worktree; commit or revert unrelated changes first.');
}

function stagedRunDirectory(root, runID) {
  const parent = path.resolve(root, RUN_PARENT);
  const runDir = path.resolve(parent, assertRunID(runID));
  if (path.dirname(runDir) !== parent || !existsSync(runDir)) {
    fail(`No staged README run exists for ${runID}.`);
  }
  return runDir;
}

function expectedSceneMetadata(manifest, runDir) {
  const first = readJson(path.join(runDir, 'proposed', 'image-metadata.json'), 'First encoding metadata');
  const repeat = readJson(path.join(runDir, 'proposed-repeat', 'image-metadata.json'), 'Repeat encoding metadata');
  const firstByID = new Map((first.images || []).map((image) => [image.id, image]));
  const repeatByID = new Map((repeat.images || []).map((image) => [image.id, image]));
  const results = [];
  for (const scene of manifest.screenshots) {
    const image = firstByID.get(scene.id);
    const second = repeatByID.get(scene.id);
    const stagedPath = path.join(runDir, 'proposed', `${scene.id}.webp`);
    if (!image || !second || !existsSync(stagedPath)) fail(`Staged output is incomplete for ${scene.id}.`);
    if (image.sha256 !== second.sha256) fail(`Staged repeat checksum differs for ${scene.id}.`);
    const file = readFileSync(stagedPath);
    const actualHash = digest(file);
    const actualBytes = file.byteLength;
    const expectedWidth = scene.viewport.width * scene.device_scale_factor;
    const expectedHeight = scene.viewport.height * scene.device_scale_factor;
    results.push({ scene, image, stagedPath, actualHash, actualBytes, expectedWidth, expectedHeight });
  }
  return { first, results };
}

function validatePrivacySidecars(manifest, runDir) {
  for (const scene of manifest.screenshots) {
    const sidecarPath = path.join(runDir, 'sidecars', `${scene.id}.json`);
    const sidecar = readJson(sidecarPath, `Visible-text sidecar for ${scene.id}`);
    if (sidecar.id !== scene.id || typeof sidecar.visible_text !== 'string') {
      fail(`Visible-text sidecar is incomplete for ${scene.id}.`);
    }
    for (const pattern of manifest.sensitive_patterns) {
      if (sidecar.visible_text.toLowerCase().includes(pattern.toLowerCase())) {
        fail(`Privacy scan failed for ${scene.id}: ${pattern}`);
      }
    }
  }
}

function validateReadmeWhitespace(proposedReadme) {
  const text = proposedReadme.toString('utf8');
  if (text.includes('\r')) fail('Staged README proposal must use LF line endings.');
  if (!text.endsWith('\n')) fail('Staged README proposal must end with one newline.');
  if (text.split('\n').some((line) => /[\t ]+$/.test(line))) {
    fail('Staged README proposal contains trailing whitespace.');
  }
}

async function validateStagedImages(manifest, metadata) {
  let totalBytes = 0;
  for (const item of metadata.results) {
    const image = await readImageMetadata(item.stagedPath);
    if (item.image.sha256 !== item.actualHash || item.image.bytes !== item.actualBytes) {
      fail(`Staged metadata does not match ${item.scene.id}.`);
    }
    if (image.format !== 'webp' || image.width !== item.expectedWidth || image.height !== item.expectedHeight) {
      fail(`Staged image ${item.scene.id} is not the required ${item.expectedWidth}x${item.expectedHeight} WebP.`);
    }
    if (item.actualBytes > 750 * 1024) fail(`Staged image ${item.scene.id} exceeds the 750 KB budget.`);
    totalBytes += item.actualBytes;
  }
  if (totalBytes > Math.floor(2.5 * 1024 * 1024)) fail('Staged screenshot portfolio exceeds the 2.5 MB budget.');
}

function validateRun(root, run, runDir, manifest) {
  const head = runGit(root, ['rev-parse', 'HEAD']);
  if (run.status !== 'succeeded') fail(`Run ${run.run_id || path.basename(runDir)} did not succeed.`);
  if (run.source_drift !== false || run.acceptance_eligible !== true) {
    fail('Staged run is not acceptance-eligible; recapture from a clean, unchanged source commit.');
  }
  if (run.source_commit !== head) {
    fail(`Staged run rendered ${run.source_commit}; current HEAD is ${head}. Recapture before acceptance.`);
  }
  if (!run.tracked_fingerprints?.before?.digest || run.tracked_fingerprints.before.digest !== run.tracked_fingerprints?.after?.digest) {
    fail('Tracked README contract changed during the staged capture.');
  }
  const passedIDs = new Set((run.scene_statuses || []).filter((scene) => scene.status === 'passed').map((scene) => scene.id));
  for (const id of REQUIRED_SCREENSHOT_IDS) {
    if (!passedIDs.has(id)) fail(`Staged run did not pass required scene ${id}.`);
  }
  if (!existsSync(path.join(runDir, 'comparison', 'README_CAPTURE_REPORT.md'))) {
    fail('Staged comparison report is missing.');
  }
  if (!existsSync(path.join(runDir, 'README.proposed.md')) || !existsSync(path.join(runDir, 'README.refresh-audit.json'))) {
    fail('Run is missing its staged README proposal or copy audit. Run make readme-propose first.');
  }
  if (manifest.acceptance_state === 'accepted' && !existsSync(path.join(runDir, 'README.proposed.diff'))) {
    fail('Routine acceptance requires a staged README diff.');
  }
}

function acceptedManifest(manifest, run, metadata, proposedReadme) {
  const result = structuredClone(manifest);
  result.acceptance_state = 'accepted';
  result.last_accepted_capture_commit = run.source_commit;
  result.accepted_readme_sha256 = digest(proposedReadme);
  result.accepted_environment = {
    platform: run.platform,
    architecture: run.architecture,
    node_version: run.node_version,
    playwright_version: run.playwright_version,
    chromium_version: run.chromium_version,
    encoder_version: run.encoder_version,
  };
  const byID = new Map(metadata.results.map((item) => [item.scene.id, item]));
  for (const scene of result.screenshots) {
    const item = byID.get(scene.id);
    scene.accepted_sha256 = item.actualHash;
    scene.accepted_bytes = item.actualBytes;
  }
  const errors = validateManifest(result);
  if (errors.length > 0) fail(`Accepted manifest would be invalid: ${errors.map((error) => error.code).join(', ')}`);
  return result;
}

function readSnapshot(filePath) {
  return existsSync(filePath) ? readFileSync(filePath) : null;
}

function restoreSnapshot(filePath, contents) {
  if (contents === null) {
    if (existsSync(filePath)) rmSync(filePath, { force: true });
    return;
  }
  writeFileSync(filePath, contents);
}

function findContentReferences(root, relativePath) {
  try {
    const output = execFileSync(
      'git',
      [
        'grep',
        '-n',
        '-I',
        '-F',
        '--',
        relativePath,
        '--',
        '.',
        ':(exclude)scripts/readme/**',
        ':(exclude)tests/**',
      ],
      { cwd: root, encoding: 'utf8' },
    ).trim();
    return output ? output.split('\n') : [];
  } catch (error) {
    if (error.status === 1) return [];
    fail(`Could not check references to ${relativePath}: ${error.stderr?.trim() || error.message}`);
  }
}

function trackedDiff(root, changedPaths) {
  return runGit(root, ['diff', '--', ...changedPaths]);
}

function currentOutputMatches(manifest, metadata, root, proposedReadme) {
  if (manifest.acceptance_state !== 'accepted' || !readFileSync(path.join(root, 'README.md')).equals(proposedReadme)) return false;
  return metadata.results.every((item) => {
    const target = resolveRepositoryPath(root, item.scene.output_path);
    return target && existsSync(target) && digest(readFileSync(target)) === item.actualHash && item.scene.accepted_sha256 === item.actualHash && item.scene.accepted_bytes === item.actualBytes;
  });
}

function cleanupNoopRun(root, runID, runDir, run) {
  const expectedRunDir = stagedRunDirectory(root, runID);
  if (runDir !== expectedRunDir) fail(`Refusing to clean an unexpected staged run: ${runDir}`);

  const sandbox = path.resolve(run.sandbox ?? '');
  const tempRoot = path.resolve(tmpdir());
  const safeSandbox = sandbox &&
    path.dirname(sandbox) === tempRoot &&
    path.basename(sandbox).startsWith('ori-readme-capture.');
  if (!safeSandbox) fail(`Refusing to clean an unsafe staged sandbox: ${JSON.stringify(run.sandbox)}`);

  rmSync(runDir, { recursive: true, force: false });
  if (existsSync(sandbox)) rmSync(sandbox, { recursive: true, force: false });
}

export async function inspectAcceptance({ root, runID }) {
  const { manifest } = await loadManifest(root);
  const branch = branchGuard(root, manifest);
  assertCleanTrackedWorktree(root);
  const runDir = stagedRunDirectory(root, runID);
  const run = readJson(path.join(runDir, 'run.json'), 'Run metadata');
  validateRun(root, run, runDir, manifest);
  const audit = readJson(path.join(runDir, 'README.refresh-audit.json'), 'README copy audit');
  if (audit.status !== 'ready') fail('Staged README copy audit is not ready for acceptance.');
  const proposedReadme = readFileSync(path.join(runDir, 'README.proposed.md'));
  const referenceErrors = await validateReadmeContent(root, manifest, proposedReadme.toString('utf8'), {
    virtualExistingPaths: manifest.screenshots.map((scene) => scene.output_path),
  });
  if (referenceErrors.length > 0) fail(`Staged README proposal failed reference validation: ${referenceErrors.map((error) => error.code).join(', ')}`);
  const metadata = expectedSceneMetadata(manifest, runDir);
  await validateStagedImages(manifest, metadata);
  validatePrivacySidecars(manifest, runDir);
  validateReadmeWhitespace(proposedReadme);
  return { branch, manifest, run, runDir, metadata, proposedReadme };
}

export async function applyAcceptance({ root, runID, approved = false, repositoryValidator = validateRepository }) {
  if (approved !== true) fail('Acceptance requires explicit approval after Checkpoint 1.');
  const inspection = await inspectAcceptance({ root, runID });
  const { manifest, run, runDir, metadata, proposedReadme } = inspection;
  if (currentOutputMatches(manifest, metadata, root, proposedReadme)) {
    cleanupNoopRun(root, runID, runDir, run);
    return { status: 'noop', run_id: runID, changed_paths: [], removed_assets: [], staging_cleaned: true };
  }
  const nextManifest = acceptedManifest(manifest, run, metadata, proposedReadme);
  const manifestPath = path.join(root, 'docs', 'readme-screenshots.json');
  const readmePath = path.join(root, 'README.md');
  const targetPaths = metadata.results.map((item) => resolveRepositoryPath(root, item.scene.output_path));
  const snapshots = new Map([...targetPaths, readmePath, manifestPath, ...OBSOLETE_ASSETS.map((file) => path.join(root, file))].map((filePath) => [filePath, readSnapshot(filePath)]));
  const removedAssets = [];
  try {
    for (const item of metadata.results) {
      copyFileSync(item.stagedPath, resolveRepositoryPath(root, item.scene.output_path));
    }
    writeFileSync(readmePath, proposedReadme);
    writeFileSync(manifestPath, `${JSON.stringify(nextManifest, null, 2)}\n`);
    for (const asset of OBSOLETE_ASSETS) {
      const absolute = path.join(root, asset);
      if (existsSync(absolute) && findContentReferences(root, asset).length === 0) {
        unlinkSync(absolute);
        removedAssets.push(asset);
      }
    }
    const report = await repositoryValidator(root);
    if (report.errors.length > 0) fail(`Post-acceptance validation failed: ${report.errors.map((error) => error.code).join(', ')}`);
  } catch (error) {
    for (const [filePath, contents] of snapshots) restoreSnapshot(filePath, contents);
    throw error;
  }
  const changedPaths = ['README.md', 'docs/readme-screenshots.json', ...metadata.results.map((item) => item.scene.output_path), ...removedAssets];
  return {
    status: 'applied',
    run_id: runID,
    changed_paths: changedPaths,
    removed_assets: removedAssets,
    post_acceptance_validation: 'passed',
    tracked_diff: trackedDiff(root, changedPaths),
    checkpoint: 'Checkpoint 2: Commit and open PR?',
    remote_actions_performed: false,
  };
}

async function main() {
  const values = parseArgs(process.argv.slice(2));
  if (!values.approve) fail('Acceptance requires explicit --approve after Checkpoint 1.');
  const result = await applyAcceptance({ root: process.cwd(), runID: values['run-id'], approved: values.approve === true });
  process.stdout.write(`${JSON.stringify(result, null, 2)}\n`);
}

if (process.argv[1] && pathToFileURL(path.resolve(process.argv[1])).href === import.meta.url) {
  main().catch((error) => {
    process.stderr.write(`README acceptance error: ${error.message}\n`);
    process.exitCode = 2;
  });
}
