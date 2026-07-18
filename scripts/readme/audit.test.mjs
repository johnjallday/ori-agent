import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { execFileSync } from 'node:child_process';
import { existsSync, mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import test from 'node:test';

import { auditReadme } from './audit.mjs';

const screenshotIDs = ['hero', 'action-center', 'workspace-map', 'workspace'];

function digest(value) {
  return createHash('sha256').update(value).digest('hex');
}

function webP(width, height, variation = 0) {
  const buffer = Buffer.alloc(30 + variation);
  buffer.write('RIFF', 0, 'ascii');
  buffer.writeUInt32LE(buffer.length - 8, 4);
  buffer.write('WEBP', 8, 'ascii');
  buffer.write('VP8X', 12, 'ascii');
  buffer.writeUInt32LE(10, 16);
  const encodedWidth = width - 1;
  const encodedHeight = height - 1;
  buffer[24] = encodedWidth & 0xff;
  buffer[25] = (encodedWidth >>> 8) & 0xff;
  buffer[26] = (encodedWidth >>> 16) & 0xff;
  buffer[27] = encodedHeight & 0xff;
  buffer[28] = (encodedHeight >>> 8) & 0xff;
  buffer[29] = (encodedHeight >>> 16) & 0xff;
  return buffer;
}

function git(root, args) {
  return execFileSync('git', args, { cwd: root, encoding: 'utf8' }).trim();
}

function commit(root, message) {
  git(root, ['add', '.']);
  git(root, ['commit', '-m', message]);
  return git(root, ['rev-parse', 'HEAD']);
}

function acceptedManifest(sourceCommit, readme, images) {
  return {
    schema_version: 1,
    acceptance_state: 'accepted',
    last_accepted_capture_commit: sourceCommit,
    accepted_readme_sha256: digest(readme),
    accepted_environment: { platform: 'test' },
    sensitive_patterns: ['/Users/', '/home/'],
    screenshots: screenshotIDs.map((id, index) => ({
      id,
      output_path: `docs/images/${id}.webp`,
      route: id === 'hero' ? '/' : `/${id}`,
      scenario_id: 'audit-fixture',
      viewport: { width: 1440, height: 900 },
      device_scale_factor: 2,
      theme: 'dark',
      locale: 'en-US',
      timezone: 'UTC',
      required_visible_selectors: ['#fixture'],
      caption: `${id} caption`,
      alt_text: `${id} alt text`,
      display_width: index < 2 ? 820 : 420,
      owner_paths: [
        id === 'hero' ? 'internal/web/templates/components/dashboard.tmpl' : `internal/web/static/js/${id}.js`,
      ],
      accepted_sha256: digest(images.get(id)),
      accepted_bytes: images.get(id).length,
    })),
  };
}

function createFixture() {
  const root = mkdtempSync(path.join(tmpdir(), 'ori-readme-audit-'));
  mkdirSync(path.join(root, 'docs', 'images'), { recursive: true });
  writeFileSync(path.join(root, 'README.md'), '# Source README\n');
  writeFileSync(path.join(root, 'docs', 'readme-screenshots.json'), `${JSON.stringify({ acceptance_state: 'bootstrap' })}\n`);
  git(root, ['init', '--initial-branch', 'main']);
  git(root, ['config', 'user.email', 'readme-audit@example.com']);
  git(root, ['config', 'user.name', 'README Audit']);
  const sourceCommit = commit(root, 'feat: source product state');

  const images = new Map(screenshotIDs.map((id, index) => [id, webP(2880, 1800, index)]));
  const readme = screenshotIDs.map((id) => `![${id}](docs/images/${id}.webp)`).join('\n').concat('\n');
  for (const [id, bytes] of images) writeFileSync(path.join(root, 'docs', 'images', `${id}.webp`), bytes);
  writeFileSync(path.join(root, 'README.md'), readme);
  writeFileSync(path.join(root, 'docs', 'readme-screenshots.json'), `${JSON.stringify(acceptedManifest(sourceCommit, readme, images), null, 2)}\n`);
  commit(root, 'docs(readme): accept screenshot portfolio');
  return { root, images, readme, sourceCommit };
}

function cleanup(root) {
  rmSync(root, { recursive: true, force: true });
}

test('reports clean after an accepted docs commit without treating its own assets as product drift', async t => {
  const fixture = createFixture();
  t.after(() => cleanup(fixture.root));

  const report = await auditReadme(fixture.root);
  assert.equal(report.status, 'clean');
  assert.deepEqual(report.affected_paths, []);
  assert.equal(report.audited_range.from, fixture.sourceCommit);
  assert.deepEqual(report.validation_errors, []);
  assert.equal(git(fixture.root, ['status', '--porcelain']), '', 'a clean audit must not create a repository change');
});

test('reports UI and shared layout changes as drift with stable surface evidence', async t => {
  const fixture = createFixture();
  t.after(() => cleanup(fixture.root));
  const uiPath = path.join(fixture.root, 'internal', 'web', 'static', 'js', 'workspace.js');
  mkdirSync(path.dirname(uiPath), { recursive: true });
  writeFileSync(uiPath, 'export const view = "command";\n');
  commit(fixture.root, 'feat: update workspace command');
  const layoutPath = path.join(fixture.root, 'internal', 'web', 'templates', 'layout', 'head.tmpl');
  mkdirSync(path.dirname(layoutPath), { recursive: true });
  writeFileSync(layoutPath, '<title>Ori</title>\n');
  commit(fixture.root, 'feat: adjust shared layout');

  const report = await auditReadme(fixture.root);
  assert.equal(report.status, 'drift');
  assert.deepEqual(report.affected_paths, [
    'internal/web/static/js/workspace.js',
    'internal/web/templates/layout/head.tmpl',
  ]);
  assert.ok(report.affected_surfaces.includes('shared-ui-navigation-layout'));
  assert.equal(report.commits.length, 2);
  assert.equal(report.recommended_action, 'Ask README Steward to refresh the README.');
});

test('reports local copy and link integrity failures as drift without writing files', async t => {
  const fixture = createFixture();
  t.after(() => cleanup(fixture.root));
  writeFileSync(path.join(fixture.root, 'README.md'), '![broken](docs/missing.md)\n');

  const report = await auditReadme(fixture.root);
  assert.equal(report.status, 'drift');
  assert.ok(report.validation_errors.some((entry) => entry.code === 'readme.missing_reference'));
  assert.ok(report.working_tree_paths.includes('README.md'));
  assert.ok(existsSync(path.join(fixture.root, 'docs', 'readme-screenshots.json')));
});

test('fails explicitly for a missing accepted capture commit and shallow history', async t => {
  const fixture = createFixture();
  t.after(() => cleanup(fixture.root));
  const manifestPath = path.join(fixture.root, 'docs', 'readme-screenshots.json');
  const broken = JSON.parse(readFileSync(manifestPath, 'utf8'));
  broken.last_accepted_capture_commit = '0123456789abcdef0123456789abcdef01234567';
  writeFileSync(manifestPath, `${JSON.stringify(broken, null, 2)}\n`);
  const missing = await auditReadme(fixture.root);
  assert.equal(missing.status, 'error');
  assert.equal(missing.error.code, 'audit.missing_capture_commit');

  writeFileSync(manifestPath, `${JSON.stringify(acceptedManifest(fixture.sourceCommit, fixture.readme, fixture.images), null, 2)}\n`);
  const shallowRoot = mkdtempSync(path.join(tmpdir(), 'ori-readme-audit-shallow-'));
  t.after(() => cleanup(shallowRoot));
  execFileSync('git', ['clone', '--depth=1', `file://${fixture.root}`, shallowRoot], { encoding: 'utf8' });
  const shallow = await auditReadme(shallowRoot);
  assert.equal(shallow.status, 'error');
  assert.equal(shallow.error.code, 'audit.shallow_history');
});

test('keeps identical drift stable and recognizes new drift after a later accepted baseline', async t => {
  const fixture = createFixture();
  t.after(() => cleanup(fixture.root));
  const firstPath = path.join(fixture.root, 'internal', 'web', 'static', 'js', 'first.js');
  mkdirSync(path.dirname(firstPath), { recursive: true });
  writeFileSync(firstPath, 'export const first = true;\n');
  const firstProductCommit = commit(fixture.root, 'feat: first product drift');
  const first = await auditReadme(fixture.root);
  const repeated = await auditReadme(fixture.root);
  assert.deepEqual(repeated, first);
  assert.equal(first.status, 'drift');

  const manifestPath = path.join(fixture.root, 'docs', 'readme-screenshots.json');
  writeFileSync(
    manifestPath,
    `${JSON.stringify(acceptedManifest(firstProductCommit, fixture.readme, fixture.images), null, 2)}\n`,
  );
  commit(fixture.root, 'docs(readme): accept first product drift');
  const clean = await auditReadme(fixture.root);
  assert.equal(clean.status, 'clean');

  const nextPath = path.join(fixture.root, 'internal', 'web', 'static', 'css', 'next.css');
  mkdirSync(path.dirname(nextPath), { recursive: true });
  writeFileSync(nextPath, '.next { color: green; }\n');
  commit(fixture.root, 'feat: second product drift');
  const next = await auditReadme(fixture.root);
  assert.equal(next.status, 'drift');
  assert.deepEqual(next.affected_paths, ['internal/web/static/css/next.css']);
});
