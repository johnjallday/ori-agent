import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { mkdtemp, mkdir, rm, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { REQUIRED_SCREENSHOT_IDS, extractReadmeLocalReferences, validateManifest, validateRepository } from './manifest.mjs';

function webP(width, height) {
  const buffer = Buffer.alloc(30);
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

function digest(value) {
  return createHash('sha256').update(value).digest('hex');
}

async function fixture() {
  const root = await mkdtemp(path.join(os.tmpdir(), 'ori-readme-manifest-'));
  await mkdir(path.join(root, 'docs', 'images'), { recursive: true });
  const readme = [
    '# Fixture',
    '![Hero](docs/images/hero.webp)',
    '![Action](docs/images/action-center.webp)',
    '<img src="docs/images/workspace-map.webp" alt="Map">',
    '<img src="docs/images/workspace.webp" alt="Workspace">',
    '[Guide](docs/guide.md)'
  ].join('\n');
  await writeFile(path.join(root, 'README.md'), readme);
  await writeFile(path.join(root, 'docs', 'guide.md'), '# Guide\n');

  const screenshots = [];
  for (const [index, id] of REQUIRED_SCREENSHOT_IDS.entries()) {
    const outputPath = `docs/images/${id === 'workspace-map' ? 'workspace-map' : id}.webp`;
    const image = webP(2880, 1800);
    await writeFile(path.join(root, outputPath), image);
    screenshots.push({
      id,
      output_path: outputPath,
      route: id === 'hero' ? '/' : `/${id}`,
      scenario_id: 'fixture',
      viewport: { width: 1440, height: 900 },
      device_scale_factor: 2,
      theme: 'dark',
      locale: 'en-US',
      timezone: 'UTC',
      required_visible_selectors: ['#fixture'],
      caption: `${id} caption`,
      alt_text: `${id} alt text`,
      display_width: index < 2 ? 820 : 420,
      owner_paths: ['internal/web/static/js/fixture.js'],
      accepted_sha256: digest(image),
      accepted_bytes: image.length
    });
  }
  const manifest = {
    schema_version: 1,
    acceptance_state: 'accepted',
    last_accepted_capture_commit: '0123456789abcdef0123456789abcdef01234567',
    accepted_readme_sha256: digest(readme),
    accepted_environment: { platform: 'test', arch: 'test', node_version: 'test', chromium_version: 'test', encoder: 'test' },
    sensitive_patterns: ['/Users/', '/home/'],
    screenshots
  };
  await writeFile(path.join(root, 'docs', 'readme-screenshots.json'), `${JSON.stringify(manifest, null, 2)}\n`);
  return { root, manifest };
}

test('validates an accepted four-scene README portfolio', async t => {
  const { root } = await fixture();
  t.after(() => rm(root, { recursive: true, force: true }));
  const report = await validateRepository(root);
  assert.deepEqual(report.errors, []);
});

test('rejects duplicate, unsafe, missing, and unexpected screenshot IDs', async () => {
  const { manifest } = await fixture();
  manifest.screenshots[1].id = 'hero';
  manifest.screenshots[2].output_path = '../escape.webp';
  manifest.screenshots.pop();
  manifest.screenshots.push({ ...manifest.screenshots[0], id: 'surprise', output_path: 'docs/images/surprise.webp' });
  const codes = new Set(validateManifest(manifest).map(error => error.code));
  assert.ok(codes.has('manifest.duplicate_id'));
  assert.ok(codes.has('manifest.invalid_output_path'));
  assert.ok(codes.has('manifest.missing_required_id'));
  assert.ok(codes.has('manifest.unexpected_id'));
});

test('flags local README links, staging references, and unmanifested product images', async t => {
  const { root } = await fixture();
  t.after(() => rm(root, { recursive: true, force: true }));
  await writeFile(path.join(root, 'docs', 'images', 'legacy.png'), 'legacy');
  await writeFile(path.join(root, 'README.md'), [
    '![Hero](docs/images/hero.webp)',
    '![Legacy](docs/images/legacy.png)',
    '![Preview](test-results/readme-refresh/run/hero.webp)',
    '[Missing](docs/missing.md)'
  ].join('\n'));
  const report = await validateRepository(root);
  const codes = new Set(report.errors.map(error => error.code));
  assert.ok(codes.has('readme.unmanifested_product_image'));
  assert.ok(codes.has('readme.staging_reference'));
  assert.ok(codes.has('readme.missing_reference'));
  assert.ok(codes.has('readme.missing_manifest_image'));
});

test('detects accepted checksum, format, dimension, and byte-budget failures', async t => {
  const { root, manifest } = await fixture();
  t.after(() => rm(root, { recursive: true, force: true }));
  const hero = manifest.screenshots[0];
  await writeFile(path.join(root, hero.output_path), webP(1440, 900));
  hero.accepted_bytes = 1;
  hero.accepted_sha256 = '0'.repeat(64);
  await writeFile(path.join(root, 'docs', 'readme-screenshots.json'), `${JSON.stringify(manifest, null, 2)}\n`);
  const report = await validateRepository(root);
  const codes = new Set(report.errors.map(error => error.code));
  assert.ok(codes.has('image.invalid_dimensions'));
  assert.ok(codes.has('image.accepted_size_mismatch'));
  assert.ok(codes.has('image.accepted_checksum_mismatch'));
});

test('extracts Markdown and HTML local references without treating external URLs as local files', () => {
  const refs = extractReadmeLocalReferences([
    '![Image](docs/images/hero.webp)',
    '[Guide](docs/guide.md#heading)',
    '<img src="docs/images/workspace.webp">',
    '<a href="docs/README.md">Docs</a>',
    '[Release](https://example.com/release)',
    '[Mail](mailto:team@example.com)'
  ].join('\n'));
  assert.deepEqual(refs.map(reference => reference.target), [
    'docs/images/hero.webp',
    'docs/guide.md',
    'docs/images/workspace.webp',
    'docs/README.md'
  ]);
});
