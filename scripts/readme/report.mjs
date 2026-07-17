#!/usr/bin/env node

import { existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import path from 'node:path';
import { pathToFileURL } from 'node:url';

import { loadManifest } from './manifest.mjs';

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

function readJson(filePath, label) {
  if (!existsSync(filePath)) fail(`${label} is missing: ${filePath}`);
  return JSON.parse(readFileSync(filePath, 'utf8'));
}

function escapeHTML(value) {
  return String(value ?? '')
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
    .replaceAll('"', '&quot;');
}

function relativeFromReport(runDir, target) {
  return path.relative(path.join(runDir, 'comparison'), target).split(path.sep).join('/');
}

export function renderReport({ manifest, run, first, repeat, runDir }) {
  const repeatByID = new Map(repeat.images.map((image) => [image.id, image]));
  const firstByID = new Map(first.images.map((image) => [image.id, image]));
  const determinism = manifest.screenshots.map((scene) => {
    const primary = firstByID.get(scene.id);
    const second = repeatByID.get(scene.id);
    return { id: scene.id, match: primary?.sha256 === second?.sha256, primary, second };
  });
  const determinismOK = determinism.every((item) => item.match);
  const rows = manifest.screenshots
    .map((scene) => {
      const image = firstByID.get(scene.id);
      const output = path.join(runDir, 'proposed', `${scene.id}.webp`);
      const current = path.join(path.resolve(runDir, '..', '..', '..'), scene.output_path);
      return `<section>\n<h2>${escapeHTML(scene.id)}</h2>\n<p>${escapeHTML(scene.caption)} — ${escapeHTML(scene.alt_text)}</p>\n<table><tr><th>Current</th><th>Proposed</th></tr><tr><td><img src="${escapeHTML(relativeFromReport(runDir, current))}" width="${scene.display_width}" alt="Current ${escapeHTML(scene.alt_text)}"></td><td><img src="${escapeHTML(relativeFromReport(runDir, output))}" width="${scene.display_width}" alt="Proposed ${escapeHTML(scene.alt_text)}"></td></tr></table>\n<p><code>${image.width}x${image.height}</code> · <code>${image.bytes} bytes</code> · <code>${image.sha256}</code></p>\n</section>`;
    })
    .join('\n');
  const checkRows = determinism
    .map((item) => `| ${item.id} | ${item.match ? 'PASS' : 'FAIL'} | ${item.primary?.sha256 ?? 'missing'} | ${item.second?.sha256 ?? 'missing'} |`)
    .join('\n');
  return `# README capture report\n\n- Run: \`${run.run_id}\`\n- Capture source commit: \`${run.source_commit}\`\n- Source dirty at capture: \`${run.dirty_state}\`\n- Platform: \`${run.platform}/${run.architecture}\`\n- Node: \`${run.node_version}\`\n- Playwright: \`${run.playwright_version ?? 'not recorded'}\`\n- Chromium: \`${run.chromium_version ?? 'not recorded'}\`\n- Encoder: \`${first.encoder.package}@${first.encoder.version ?? 'unknown'}\`\n- Tracked README contract unchanged: \`${run.tracked_fingerprints.before.digest === run.tracked_fingerprints.after.digest}\`\n- Acceptance eligible: \`${run.acceptance_eligible}\`\n\n## Same-environment determinism\n\n| Scene | Result | First SHA-256 | Repeat SHA-256 |\n| --- | --- | --- | --- |\n${checkRows}\n\n**${determinismOK ? 'PASS' : 'FAIL'}** — checksums are compared only between two captures from this same run/environment.\n\n## Current versus proposed\n\n${rows}\n\n## Next action\n\nInspect the proposed images and this report. This command did not modify \`README.md\`, \`docs/images/\`, or accepted manifest metadata.\n`;
}

async function main() {
  const values = parseArgs(process.argv.slice(2));
  const runDir = path.resolve(values['run-dir'] ?? '');
  if (!runDir || !existsSync(runDir)) fail('A valid --run-dir is required.');
  const manifestPath = path.resolve(values.manifest ?? 'docs/readme-screenshots.json');
  const manifestRoot = path.resolve(path.dirname(manifestPath), '..');
  const { manifest } = await loadManifest(manifestRoot, path.relative(manifestRoot, manifestPath));
  const run = readJson(path.join(runDir, 'run.json'), 'Run metadata');
  const first = readJson(path.join(runDir, 'proposed', 'image-metadata.json'), 'First encoding metadata');
  const repeat = readJson(path.join(runDir, 'proposed-repeat', 'image-metadata.json'), 'Repeat encoding metadata');
  const output = path.join(runDir, 'comparison', 'README_CAPTURE_REPORT.md');
  mkdirSync(path.dirname(output), { recursive: true });
  writeFileSync(output, renderReport({ manifest, run, first, repeat, runDir }));
  process.stdout.write(`${output}\n`);
}

if (process.argv[1] && pathToFileURL(path.resolve(process.argv[1])).href === import.meta.url) {
  main().catch((error) => {
    process.stderr.write(`README comparison report error: ${error.message}\n`);
    process.exitCode = 2;
  });
}
