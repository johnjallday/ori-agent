import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { execFileSync } from 'node:child_process';
import { existsSync, mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import test from 'node:test';

import { applyAcceptance, inspectAcceptance } from './accept.mjs';

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

function imagePath(id) {
  return `docs/images/${id}.webp`;
}

function candidateReadme({ protectOnboarding = false } = {}) {
  return [
    '# README fixture',
    ...screenshotIDs.map((id) => `![${id}](${imagePath(id)})`),
    protectOnboarding ? '[Keep onboarding fixture](docs/images/onboarding.png)' : '',
  ].filter(Boolean).join('\n').concat('\n');
}

function manifest({ acceptanceState = 'bootstrap', readme = null, images = new Map() } = {}) {
  return {
    schema_version: 1,
    acceptance_state: acceptanceState,
    last_accepted_capture_commit: acceptanceState === 'accepted' ? '0123456789abcdef0123456789abcdef01234567' : null,
    accepted_readme_sha256: acceptanceState === 'accepted' ? digest(readme) : null,
    accepted_environment: acceptanceState === 'accepted' ? { platform: 'test' } : null,
    sensitive_patterns: ['/Users/', '/home/'],
    screenshots: screenshotIDs.map((id, index) => {
      const bytes = images.get(id);
      return {
        id,
        output_path: imagePath(id),
        route: id === 'hero' ? '/' : `/${id}`,
        scenario_id: 'acceptance-fixture',
        viewport: { width: 1440, height: 900 },
        device_scale_factor: 2,
        theme: 'dark',
        locale: 'en-US',
        timezone: 'UTC',
        required_visible_selectors: ['#fixture'],
        caption: `${id} caption`,
        alt_text: `${id} alt text`,
        display_width: index < 2 ? 820 : 420,
        owner_paths: ['README.md'],
        accepted_sha256: acceptanceState === 'accepted' ? digest(bytes) : null,
        accepted_bytes: acceptanceState === 'accepted' ? bytes.length : null,
      };
    }),
  };
}

function git(root, args) {
  return execFileSync('git', args, { cwd: root, encoding: 'utf8' }).trim();
}

function createFixture({ branch = 'feature/readme-steward', accepted = false } = {}) {
  const root = mkdtempSync(path.join(tmpdir(), 'ori-readme-accept-'));
  const originalReadme = [
    '# Original fixture',
    '![Hero](docs/images/hero.png)',
    '![Action](docs/images/action-center.png)',
    '![Onboarding](docs/images/onboarding.png)',
    '![Workspace](docs/images/workspace.png)',
  ].join('\n');
  const acceptedReadme = candidateReadme();
  const images = new Map(screenshotIDs.map((id, index) => [id, webP(2880, 1800, index)]));

  mkdirSync(path.join(root, 'docs', 'images'), { recursive: true });
  writeFileSync(path.join(root, 'README.md'), accepted ? acceptedReadme : originalReadme);
  for (const oldAsset of ['hero.png', 'action-center.png', 'onboarding.png', 'workspace.png']) {
    writeFileSync(path.join(root, 'docs', 'images', oldAsset), `old-${oldAsset}\n`);
  }
  if (accepted) {
    for (const [id, bytes] of images) writeFileSync(path.join(root, imagePath(id)), bytes);
  }
  writeFileSync(
    path.join(root, 'docs', 'readme-screenshots.json'),
    `${JSON.stringify(manifest({
      acceptanceState: accepted ? 'accepted' : 'bootstrap',
      readme: acceptedReadme,
      images,
    }), null, 2)}\n`,
  );

  git(root, ['init', '--initial-branch', branch]);
  git(root, ['config', 'user.email', 'readme-test@example.com']);
  git(root, ['config', 'user.name', 'README Test']);
  git(root, ['add', '.']);
  git(root, ['commit', '-m', 'test: create README acceptance fixture']);
  return { root, images, originalReadme, acceptedReadme };
}

function stageRun(root, images, { runID = 'acceptance-fixture', readme = candidateReadme(), sourceCommit = git(root, ['rev-parse', 'HEAD']), status = 'succeeded', sceneStatuses = screenshotIDs.map((id) => ({ id, status: 'passed' })) } = {}) {
  const runDir = path.join(root, 'test-results', 'readme-refresh', runID);
  const sandbox = mkdtempSync(path.join(tmpdir(), 'ori-readme-capture.accept-test-'));
  mkdirSync(path.join(runDir, 'proposed'), { recursive: true });
  mkdirSync(path.join(runDir, 'proposed-repeat'), { recursive: true });
  mkdirSync(path.join(runDir, 'comparison'), { recursive: true });
  mkdirSync(path.join(runDir, 'sidecars'), { recursive: true });
  const metadata = { images: [] };
  for (const [id, bytes] of images) {
    writeFileSync(path.join(runDir, 'proposed', `${id}.webp`), bytes);
    writeFileSync(path.join(runDir, 'proposed-repeat', `${id}.webp`), bytes);
    metadata.images.push({ id, sha256: digest(bytes), bytes: bytes.length });
  }
  writeFileSync(path.join(runDir, 'proposed', 'image-metadata.json'), `${JSON.stringify(metadata, null, 2)}\n`);
  writeFileSync(path.join(runDir, 'proposed-repeat', 'image-metadata.json'), `${JSON.stringify(metadata, null, 2)}\n`);
  writeFileSync(path.join(runDir, 'comparison', 'README_CAPTURE_REPORT.md'), '# fixture report\n');
  for (const id of screenshotIDs) {
    writeFileSync(path.join(runDir, 'sidecars', `${id}.json`), `${JSON.stringify({ id, visible_text: 'fictional fixture only' })}\n`);
  }
  writeFileSync(path.join(runDir, 'README.proposed.md'), readme);
  writeFileSync(path.join(runDir, 'README.proposed.diff'), 'fixture diff\n');
  writeFileSync(path.join(runDir, 'README.refresh-audit.json'), `${JSON.stringify({ status: 'ready' })}\n`);
  writeFileSync(path.join(runDir, 'run.json'), `${JSON.stringify({
    run_id: runID,
    status,
    source_commit: sourceCommit,
    source_drift: false,
    acceptance_eligible: true,
    tracked_fingerprints: { before: { digest: 'same' }, after: { digest: 'same' } },
    scene_statuses: sceneStatuses,
    platform: 'test',
    architecture: 'test',
    node_version: process.version,
    playwright_version: 'test',
    chromium_version: 'test',
    encoder_version: 'test',
    sandbox,
  }, null, 2)}\n`);
  return { runDir, sandbox };
}

function cleanupFixture(root) {
  rmSync(root, { recursive: true, force: true });
}

test('requires Checkpoint 1 approval and rejects unsafe, stale, failed, and incomplete runs without mutation', async t => {
  const fixture = createFixture();
  t.after(() => cleanupFixture(fixture.root));
  const staged = stageRun(fixture.root, fixture.images);
  t.after(() => rmSync(staged.sandbox, { recursive: true, force: true }));
  const originalManifest = readFileSync(path.join(fixture.root, 'docs', 'readme-screenshots.json'));

  await assert.rejects(() => applyAcceptance({ root: fixture.root, runID: 'acceptance-fixture' }), /explicit approval/);
  assert.equal(readFileSync(path.join(fixture.root, 'README.md'), 'utf8'), fixture.originalReadme);
  assert.deepEqual(readFileSync(path.join(fixture.root, 'docs', 'readme-screenshots.json')), originalManifest);
  assert.equal(existsSync(staged.runDir), true, 'pre-approval must preserve the staged review artifacts');

  await assert.rejects(() => inspectAcceptance({ root: fixture.root, runID: '../escape' }), /Unsafe run ID/);
  const runPath = path.join(staged.runDir, 'run.json');
  const run = JSON.parse(readFileSync(runPath, 'utf8'));
  run.source_commit = '0123456789abcdef0123456789abcdef01234567';
  writeFileSync(runPath, `${JSON.stringify(run, null, 2)}\n`);
  await assert.rejects(() => applyAcceptance({ root: fixture.root, runID: 'acceptance-fixture', approved: true }), /current HEAD/);

  run.source_commit = git(fixture.root, ['rev-parse', 'HEAD']);
  run.status = 'failed';
  writeFileSync(runPath, `${JSON.stringify(run, null, 2)}\n`);
  await assert.rejects(() => applyAcceptance({ root: fixture.root, runID: 'acceptance-fixture', approved: true }), /did not succeed/);

  run.status = 'succeeded';
  run.scene_statuses = [{ id: 'hero', status: 'passed' }];
  writeFileSync(runPath, `${JSON.stringify(run, null, 2)}\n`);
  await assert.rejects(() => applyAcceptance({ root: fixture.root, runID: 'acceptance-fixture', approved: true }), /required scene action-center/);
  assert.equal(readFileSync(path.join(fixture.root, 'README.md'), 'utf8'), fixture.originalReadme);
});

test('rejects the bootstrap run outside its dedicated feature branch', async t => {
  const fixture = createFixture({ branch: 'dev' });
  t.after(() => cleanupFixture(fixture.root));
  const staged = stageRun(fixture.root, fixture.images);
  t.after(() => rmSync(staged.sandbox, { recursive: true, force: true }));

  await assert.rejects(() => applyAcceptance({ root: fixture.root, runID: 'acceptance-fixture', approved: true }), /Bootstrap acceptance is allowed only/);
  assert.equal(readFileSync(path.join(fixture.root, 'README.md'), 'utf8'), fixture.originalReadme);
});

test('rejects a private sidecar and invalid staged README whitespace before copying files', async t => {
  const fixture = createFixture();
  t.after(() => cleanupFixture(fixture.root));
  const staged = stageRun(fixture.root, fixture.images);
  t.after(() => rmSync(staged.sandbox, { recursive: true, force: true }));
  const heroSidecar = path.join(staged.runDir, 'sidecars', 'hero.json');
  writeFileSync(heroSidecar, `${JSON.stringify({ id: 'hero', visible_text: '/Users/private/fixture' })}\n`);

  await assert.rejects(() => applyAcceptance({ root: fixture.root, runID: 'acceptance-fixture', approved: true }), /Privacy scan failed/);
  assert.equal(readFileSync(path.join(fixture.root, 'README.md'), 'utf8'), fixture.originalReadme);

  writeFileSync(heroSidecar, `${JSON.stringify({ id: 'hero', visible_text: 'fictional fixture only' })}\n`);
  writeFileSync(path.join(staged.runDir, 'README.proposed.md'), candidateReadme().trimEnd());
  await assert.rejects(() => applyAcceptance({ root: fixture.root, runID: 'acceptance-fixture', approved: true }), /must end with one newline/);
  assert.equal(readFileSync(path.join(fixture.root, 'README.md'), 'utf8'), fixture.originalReadme);
});

test('rolls back a failed post-copy validation and permits a safe retry from the same staged run', async t => {
  const fixture = createFixture();
  t.after(() => cleanupFixture(fixture.root));
  const staged = stageRun(fixture.root, fixture.images, { readme: candidateReadme({ protectOnboarding: true }) });
  t.after(() => rmSync(staged.sandbox, { recursive: true, force: true }));
  const originalManifest = readFileSync(path.join(fixture.root, 'docs', 'readme-screenshots.json'));
  let validatorSawAppliedFiles = false;

  await assert.rejects(
    () => applyAcceptance({
      root: fixture.root,
      runID: 'acceptance-fixture',
      approved: true,
      repositoryValidator: async () => {
        validatorSawAppliedFiles = readFileSync(path.join(fixture.root, 'README.md'), 'utf8').includes('workspace-map.webp');
        return { errors: [{ code: 'test.forced_validation_failure' }] };
      },
    }),
    /Post-acceptance validation failed/,
  );
  assert.equal(validatorSawAppliedFiles, true, 'the injected validator proves rollback covers post-copy validation failures');
  assert.equal(readFileSync(path.join(fixture.root, 'README.md'), 'utf8'), fixture.originalReadme);
  assert.deepEqual(readFileSync(path.join(fixture.root, 'docs', 'readme-screenshots.json')), originalManifest);
  assert.equal(existsSync(path.join(fixture.root, 'docs', 'images', 'hero.webp')), false);
  assert.equal(existsSync(path.join(fixture.root, 'docs', 'images', 'hero.png')), true);
  assert.equal(existsSync(staged.runDir), true, 'a failed acceptance must retain artifacts for a safe retry');

  const result = await applyAcceptance({ root: fixture.root, runID: 'acceptance-fixture', approved: true });
  assert.equal(result.status, 'applied');
  assert.equal(result.post_acceptance_validation, 'passed');
  assert.equal(result.checkpoint, 'Checkpoint 2: Commit and open PR?');
  assert.equal(result.remote_actions_performed, false);
  assert.match(result.tracked_diff, /README\.md/);
  assert.deepEqual(result.removed_assets.sort(), ['docs/images/action-center.png', 'docs/images/hero.png', 'docs/images/workspace.png']);
  assert.equal(existsSync(path.join(fixture.root, 'docs', 'images', 'onboarding.png')), true, 'a remaining repository reference protects an obsolete image');
  assert.equal(readFileSync(path.join(fixture.root, 'README.md'), 'utf8'), candidateReadme({ protectOnboarding: true }));
  assert.equal(git(fixture.root, ['rev-parse', 'HEAD']).length, 40, 'Checkpoint 1 does not create a commit or contact GitHub');
});

test('identical accepted outputs are a no-op and safely clean their staging run', async t => {
  const fixture = createFixture({ branch: 'docs/readme-refresh-2026-07', accepted: true });
  t.after(() => cleanupFixture(fixture.root));
  const staged = stageRun(fixture.root, fixture.images, { readme: fixture.acceptedReadme });
  const beforeHead = git(fixture.root, ['rev-parse', 'HEAD']);
  const beforeManifest = readFileSync(path.join(fixture.root, 'docs', 'readme-screenshots.json'));

  const result = await applyAcceptance({ root: fixture.root, runID: 'acceptance-fixture', approved: true });
  assert.deepEqual(result, {
    status: 'noop',
    run_id: 'acceptance-fixture',
    changed_paths: [],
    removed_assets: [],
    staging_cleaned: true,
  });
  assert.equal(existsSync(staged.runDir), false);
  assert.equal(existsSync(staged.sandbox), false);
  assert.equal(git(fixture.root, ['rev-parse', 'HEAD']), beforeHead);
  assert.deepEqual(readFileSync(path.join(fixture.root, 'docs', 'readme-screenshots.json')), beforeManifest);
});
