import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import test from 'node:test';
import sharp from 'sharp';

import { encodePortfolio } from './encode.mjs';

const repoRoot = path.resolve(import.meta.dirname, '..', '..');
const manifestPath = path.join(repoRoot, 'docs', 'readme-screenshots.json');
const sceneIDs = ['hero', 'action-center', 'workspace-map', 'workspace'];

function digest(filePath) {
  return createHash('sha256').update(readFileSync(filePath)).digest('hex');
}

async function writePNG(directory, id, color) {
  await sharp({
    create: { width: 2880, height: 1800, channels: 3, background: color },
  })
    .png({ compressionLevel: 9, palette: false })
    .toFile(path.join(directory, `${id}.png`));
}

test('encodes the four-scene portfolio as deterministic 2880x1800 WebP files', async () => {
  const root = mkdtempSync(path.join(tmpdir(), 'readme-encode-test-'));
  const raw = path.join(root, 'raw');
  const first = path.join(root, 'first');
  const second = path.join(root, 'second');
  try {
    mkdirSync(raw);
    for (const [index, id] of sceneIDs.entries()) {
      await writePNG(raw, id, { r: 30 + index * 20, g: 50, b: 70 });
    }
    const firstResult = await encodePortfolio({ manifestPath, sourceDir: raw, outputDir: first });
    const secondResult = await encodePortfolio({ manifestPath, sourceDir: raw, outputDir: second });
    assert.equal(firstResult.encoder.package, 'sharp');
    assert.equal(firstResult.encoder.version, sharp.versions.sharp);
    assert.deepEqual(
      firstResult.images.map((image) => image.sha256),
      secondResult.images.map((image) => image.sha256),
      'the pinned encoder must be repeatable on one platform',
    );
    for (const image of firstResult.images) {
      assert.equal(image.format, 'webp');
      assert.equal(image.width, 2880);
      assert.equal(image.height, 1800);
      assert.equal(image.sha256, digest(path.join(first, `${image.id}.webp`)));
      assert.equal(image.bytes > 0, true);
    }
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test('fails before encoding an absent or incorrectly sized raw scene', async () => {
  const root = mkdtempSync(path.join(tmpdir(), 'readme-encode-error-'));
  const raw = path.join(root, 'raw');
  const output = path.join(root, 'output');
  try {
    await assert.rejects(
      encodePortfolio({ manifestPath, sourceDir: raw, outputDir: output }),
      /Source directory does not exist/,
    );
    writeFileSync(path.join(root, 'placeholder'), '');
    await sharp({ create: { width: 1, height: 1, channels: 3, background: '#000' } })
      .png()
      .toFile(path.join(root, 'hero.png'));
    const prepared = path.join(root, 'prepared');
    mkdirSync(prepared);
    await sharp({ create: { width: 1, height: 1, channels: 3, background: '#000' } })
      .png()
      .toFile(path.join(prepared, 'hero.png'));
    await assert.rejects(
      encodePortfolio({ manifestPath, sourceDir: prepared, outputDir: output }),
      /Raw hero must be a 2880x1800 PNG/,
    );
    assert.equal(existsSync(path.join(output, 'hero.webp')), false);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});
