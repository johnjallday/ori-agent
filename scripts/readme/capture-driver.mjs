#!/usr/bin/env node

import { execFileSync, spawnSync } from 'node:child_process';
import { existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

function fail(message) {
  throw new Error(message);
}

function parseArgs(argv) {
  const values = {};
  for (let index = 0; index < argv.length; index += 2) {
    const key = argv[index];
    const value = argv[index + 1];
    if (!key?.startsWith('--') || value === undefined) fail('Expected --key value arguments.');
    values[key.slice(2)] = value;
  }
  return values;
}

function run(command, args, options) {
  const result = spawnSync(command, args, { cwd: options.cwd, env: options.env, encoding: 'utf8' });
  if (options.logPath) {
    writeFileSync(options.logPath, `${result.stdout || ''}\n${result.stderr || ''}`);
  }
  if (result.error) fail(`${command} failed to start: ${result.error.message}`);
  if (result.status !== 0) {
    process.stdout.write(result.stdout || '');
    process.stderr.write(result.stderr || '');
    fail(`${command} ${args.join(' ')} exited ${result.status}.`);
  }
  return result;
}

function imageMetadata(runDirectory, name) {
  const filePath = path.join(runDirectory, name, 'image-metadata.json');
  if (!existsSync(filePath)) fail(`Missing encoder metadata: ${filePath}`);
  return JSON.parse(readFileSync(filePath, 'utf8'));
}

function assertRepeatable(first, repeat) {
  const repeatByID = new Map(repeat.images.map((image) => [image.id, image]));
  for (const image of first.images) {
    const comparison = repeatByID.get(image.id);
    if (!comparison || comparison.sha256 !== image.sha256) {
      fail(`Same-environment WebP checksum mismatch for ${image.id}; inspect the staged run and recapture.`);
    }
  }
}

function chromiumVersion(repoRoot) {
  try {
    const executable = execFileSync(process.execPath, [
      '--input-type=module',
      '-e',
      "import { chromium } from 'playwright'; process.stdout.write(chromium.executablePath());",
    ], { cwd: repoRoot, encoding: 'utf8' }).trim();
    return execFileSync(executable, ['--version'], { encoding: 'utf8' }).trim();
  } catch {
    return 'unavailable';
  }
}

function scanSidecars(sidecarDirectory) {
  const forbidden = [/\/Users\//i, /\/home\//i, /\bsk-[a-z0-9]/i, /AKIA[0-9A-Z]{16}/, /BEGIN PRIVATE KEY/];
  for (const fileName of ['hero.json', 'action-center.json', 'workspace-map.json', 'workspace.json']) {
    const filePath = path.join(sidecarDirectory, fileName);
    if (!existsSync(filePath)) fail(`Missing visible-text sidecar: ${filePath}`);
    const text = readFileSync(filePath, 'utf8');
    for (const pattern of forbidden) {
      if (pattern.test(text)) fail(`Privacy scan failed for ${fileName}: ${pattern}`);
    }
  }
}

function captureAttempt({ repoRoot, baseURL, runDirectory, manifest, attempt, rawDirectory, sidecarDirectory }) {
  mkdirSync(rawDirectory, { recursive: true });
  mkdirSync(sidecarDirectory, { recursive: true });
  const cli = path.join(repoRoot, 'node_modules', '@playwright', 'test', 'cli.js');
  if (!existsSync(cli)) fail('Pinned @playwright/test is missing. Run npm ci before README capture.');
  const result = run(process.execPath, [cli, 'test', '--config', 'playwright.readme.config.ts'], {
    cwd: repoRoot,
    logPath: path.join(runDirectory, 'logs', `playwright-${attempt}.log`),
    env: {
      ...process.env,
      CI: '1',
      PLAYWRIGHT_BASE_URL: baseURL,
      README_CAPTURE_RUN_DIR: runDirectory,
      README_CAPTURE_RAW_DIR: rawDirectory,
      README_CAPTURE_SIDECAR_DIR: sidecarDirectory,
      README_CAPTURE_ATTEMPT: attempt,
      README_CAPTURE_PLAYWRIGHT_OUTPUT: path.join(runDirectory, `playwright-output-${attempt}`),
    },
  });
  return result;
}

async function main() {
  const values = parseArgs(process.argv.slice(2));
  const baseURL = values['base-url'];
  const runDirectory = path.resolve(values['run-dir'] ?? '');
  const manifest = path.resolve(values.manifest ?? '');
  if (!baseURL || !runDirectory || !existsSync(runDirectory) || !existsSync(manifest)) {
    fail('Capture driver requires an existing --run-dir, --manifest, and --base-url.');
  }
  const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..', '..');
  const raw = path.join(runDirectory, 'raw');
  const rawRepeat = path.join(runDirectory, 'raw-repeat');
  const proposed = path.join(runDirectory, 'proposed');
  const proposedRepeat = path.join(runDirectory, 'proposed-repeat');
  const sidecars = path.join(runDirectory, 'sidecars');

  captureAttempt({ repoRoot, baseURL, runDirectory, manifest, attempt: 'first', rawDirectory: raw, sidecarDirectory: sidecars });
  scanSidecars(sidecars);
  run(process.execPath, [
    path.join(repoRoot, 'scripts', 'readme', 'encode.mjs'),
    '--manifest', manifest,
    '--source-dir', raw,
    '--output-dir', proposed,
    '--metadata-path', path.join(proposed, 'image-metadata.json'),
  ], { cwd: repoRoot, env: process.env });

  captureAttempt({ repoRoot, baseURL, runDirectory, manifest, attempt: 'repeat', rawDirectory: rawRepeat, sidecarDirectory: path.join(sidecars, 'repeat') });
  run(process.execPath, [
    path.join(repoRoot, 'scripts', 'readme', 'encode.mjs'),
    '--manifest', manifest,
    '--source-dir', rawRepeat,
    '--output-dir', proposedRepeat,
    '--metadata-path', path.join(proposedRepeat, 'image-metadata.json'),
  ], { cwd: repoRoot, env: process.env });

  const first = imageMetadata(runDirectory, 'proposed');
  const repeat = imageMetadata(runDirectory, 'proposed-repeat');
  assertRepeatable(first, repeat);
  run(process.execPath, [
    path.join(repoRoot, 'scripts', 'readme', 'run-metadata.mjs'),
    'record-capture-details',
    '--run-dir', runDirectory,
    '--playwright-version', JSON.parse(readFileSync(path.join(repoRoot, 'node_modules', '@playwright', 'test', 'package.json'), 'utf8')).version,
    '--chromium-version', chromiumVersion(repoRoot),
    '--encoder-version', `${first.encoder.package}@${first.encoder.version}`,
  ], { cwd: repoRoot, env: process.env });
}

if (process.argv[1] && pathToFileURL(path.resolve(process.argv[1])).href === import.meta.url) {
  main().catch((error) => {
    process.stderr.write(`README capture driver error: ${error.message}\n`);
    process.exitCode = 2;
  });
}
