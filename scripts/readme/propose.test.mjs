import assert from 'node:assert/strict';
import { existsSync, mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import test from 'node:test';

import { extractReadmeLocalReferences, loadManifest, validateReadmeContent } from './manifest.mjs';
import { auditCandidate, createProposal, renderProposedREADME } from './propose.mjs';

const root = path.resolve(import.meta.dirname, '..', '..');

function runID() {
  return `proposal-test-${process.pid}-${Date.now()}`;
}

test('renders the four-scene WebP portfolio without rewriting unrelated README copy', async () => {
  const { manifest } = await loadManifest(root);
  const current = readFileSync(path.join(root, 'README.md'), 'utf8');
  const proposed = renderProposedREADME(current, manifest);

  assert.match(proposed, /docs\/images\/hero\.webp/);
  assert.match(proposed, /docs\/images\/action-center\.webp/);
  assert.match(proposed, /docs\/images\/workspace-map\.webp/);
  assert.match(proposed, /docs\/images\/workspace\.webp/);
  assert.doesNotMatch(proposed, /docs\/images\/(?:hero|action-center|onboarding|workspace)\.png/);
  assert.match(proposed, /\| Workspace Map \| Workspace Command \|/);
  for (const scene of manifest.screenshots) {
    assert.match(proposed, new RegExp(scene.caption.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')));
    assert.match(proposed, new RegExp(scene.alt_text.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')));
  }
  assert.match(proposed, /## 🚀 Quick start/);
  assert.match(proposed, /## 🤖 Providers/);
  assert.match(proposed, /## 📚 Documentation/);
  const errors = await validateReadmeContent(root, manifest, proposed, {
    virtualExistingPaths: manifest.screenshots.map((scene) => scene.output_path),
  });
  assert.deepEqual(errors, []);
});

test('writes a ready, ignored proposal, diff, and factual audit into one staging run', async t => {
  const id = runID();
  const fixtureRoot = mkdtempSync(path.join(tmpdir(), 'ori-readme-proposal-'));
  const { manifest } = await loadManifest(root);
  const currentReadme = readFileSync(path.join(root, 'README.md'));
  writeFileSync(path.join(fixtureRoot, 'README.md'), currentReadme);
  mkdirSync(path.join(fixtureRoot, 'docs'), { recursive: true });
  writeFileSync(
    path.join(fixtureRoot, 'docs', 'readme-screenshots.json'),
    readFileSync(path.join(root, 'docs', 'readme-screenshots.json')),
  );
  const evidencePaths = [
    'docs/INSTALLATION_MACOS.md',
    'docs/INSTALLATION_LINUX.md',
    'docs/INSTALLATION_WINDOWS.md',
    'docs/api/API_REFERENCE.md',
    'internal/llm/README.md',
    'internal/workspace/mission.go',
    'internal/workspace/scheduler.go',
    'internal/workspace/opportunities.go',
    'internal/skills/manager.go',
    'internal/mcp/registry.go',
    'scripts/build-server.sh',
    'docs/README.md',
    ...manifest.screenshots.flatMap((scene) => scene.owner_paths),
    ...extractReadmeLocalReferences(currentReadme.toString('utf8')).map((reference) => reference.target),
  ];
  for (const relativePath of evidencePaths) {
    const target = path.join(fixtureRoot, relativePath);
    mkdirSync(path.dirname(target), { recursive: true });
    if (!existsSync(target)) writeFileSync(target, '# fixture evidence\n');
  }
  const runDir = path.join(fixtureRoot, 'test-results', 'readme-refresh', id);
  mkdirSync(runDir, { recursive: true });
  t.after(() => rmSync(fixtureRoot, { recursive: true, force: true }));

  const result = await createProposal({ root: fixtureRoot, runID: id });
  assert.equal(result.status, 'ready', readFileSync(path.join(runDir, 'README.refresh-audit.json'), 'utf8'));
  assert.equal(result.checkpoint, 'Checkpoint 1: Apply staged refresh?');
  assert.deepEqual(result.exact_effects_after_approval.update, [
    'README.md',
    'docs/readme-screenshots.json',
    'docs/images/hero.webp',
    'docs/images/action-center.webp',
    'docs/images/workspace-map.webp',
    'docs/images/workspace.webp',
  ]);
  assert.equal(existsSync(path.join(runDir, 'README.proposed.md')), true);
  assert.equal(existsSync(path.join(runDir, 'README.proposed.diff')), true);
  assert.equal(existsSync(path.join(runDir, 'README.refresh-audit.json')), true);
  const audit = JSON.parse(readFileSync(path.join(runDir, 'README.refresh-audit.json'), 'utf8'));
  assert.equal(audit.status, 'ready');
  assert.deepEqual(audit.optional_suggestions, []);
  assert.equal(audit.required_changes.length, 1);
  assert.equal(readFileSync(path.join(runDir, 'README.proposed.diff'), 'utf8').includes('workspace-map.webp'), true);
});
