import assert from 'node:assert/strict';
import test from 'node:test';
import path from 'node:path';

import { loadManifest } from './manifest.mjs';
import { renderReport } from './report.mjs';

const repoRoot = path.resolve(import.meta.dirname, '..', '..');
const { manifest } = await loadManifest(repoRoot);

function encoding(prefix) {
  return {
    encoder: { package: 'sharp', version: '0.34.5' },
    images: manifest.screenshots.map((scene) => ({
      id: scene.id,
      width: 2880,
      height: 1800,
      bytes: 400000,
      sha256: `${prefix}-${scene.id}`,
    })),
  };
}

test('renders a current-versus-proposed report with same-environment checks', () => {
  const text = renderReport({
    manifest,
    run: {
      run_id: '20260717-example',
      source_commit: 'abc123',
      dirty_state: false,
      platform: 'darwin',
      architecture: 'arm64',
      node_version: 'v25.9.0',
      playwright_version: '1.57.0',
      chromium_version: '143.0.0',
      tracked_fingerprints: { before: { digest: 'same' }, after: { digest: 'same' } },
      acceptance_eligible: true,
    },
    first: encoding('same'),
    repeat: encoding('same'),
    runDir: path.join(repoRoot, 'test-results', 'readme-refresh', '20260717-example'),
  });
  assert.match(text, /README capture report/);
  assert.match(text, /\*\*PASS\*\*/);
  assert.match(text, /Current versus proposed/);
  assert.match(text, /hero/);
  assert.match(text, new RegExp(manifest.screenshots[0].alt_text));
  assert.match(text, /<img src=/);
});

test('marks a changed repeat checksum as a determinism failure', () => {
  const text = renderReport({
    manifest,
    run: {
      run_id: 'example',
      source_commit: 'abc',
      dirty_state: false,
      platform: 'darwin',
      architecture: 'arm64',
      node_version: 'v25',
      tracked_fingerprints: { before: { digest: 'same' }, after: { digest: 'same' } },
      acceptance_eligible: true,
    },
    first: encoding('first'),
    repeat: encoding('second'),
    runDir: path.join(repoRoot, 'test-results', 'readme-refresh', 'example'),
  });
  assert.match(text, /\*\*FAIL\*\*/);
});
