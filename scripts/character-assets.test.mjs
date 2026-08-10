/*
 * Raster half of the map-ready character asset contract.
 *
 *   npm run test:character-assets
 *
 * Rasterizes every catalog asset at its native size and proves the artwork is
 * genuinely background-free: transparent artboard, clear safe perimeter, correct
 * native dimensions. The structural half — baked halos that never touch an
 * edge, and native viewBox — lives in internal/web/character_assets_test.go.
 *
 * The fixture suite runs first and on purpose: the production sweep is only
 * worth anything if the inspector has been shown to fail the exact background
 * idioms this feature is removing.
 */
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync, existsSync } from 'node:fs';
import { join, dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

import { ASSET_CONTRACT, inspectAsset, formatFindings } from './lib/character-asset-contract.mjs';

const repoRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const charDir = join(repoRoot, 'internal/web/static/characters');
const catalogPath = join(repoRoot, 'internal/charactercatalog/catalog.json');

const svg = (size, body) =>
  Buffer.from(
    `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 ${size} ${size}" width="${size}" height="${size}">${body}</svg>`
  );

const codes = findings => findings.map(f => f.code).sort();

// ---------------------------------------------------------------------------
// Fixtures: the inspector must earn the right to judge production art.
// ---------------------------------------------------------------------------

test('fixture: transparent art well inside the safe perimeter passes', async () => {
  const { findings, painted } = await inspectAsset(
    svg(48, '<circle cx="24" cy="26" r="14" fill="#bd7c24"/>'),
    'sprite'
  );
  assert.deepEqual(
    findings,
    [],
    `expected a clean pass, got:\n${formatFindings('fixture', 'sprite', findings)}`
  );
  assert.ok(painted < ASSET_CONTRACT.sprite.maxPaintedFraction);
});

test('fixture: a full-artboard card fails on both perimeter and coverage', async () => {
  const { findings } = await inspectAsset(
    svg(48, '<rect width="48" height="48" fill="#f0dcb8"/>'),
    'sprite'
  );
  assert.deepEqual(codes(findings), ['coverage', 'perimeter']);
});

test('fixture: an inscribed circular disc fails even though its corners are clear', async () => {
  // The disc idiom every current sprite uses. Corner-only sampling would call
  // this transparent, which is exactly why the check sweeps the whole perimeter.
  const disc = svg(48, '<circle cx="24" cy="24" r="24" fill="#f0dcb8"/>');
  const { findings } = await inspectAsset(disc, 'sprite');
  assert.deepEqual(codes(findings), ['coverage', 'perimeter']);

  const { data } = await (await import('sharp'))
    .default(disc)
    .ensureAlpha()
    .raw()
    .toBuffer({ resolveWithObject: true });
  assert.equal(data[3], 0, 'the disc fixture should leave the top-left corner transparent');
});

test('fixture: a small shape touching the edge fails the perimeter only', async () => {
  // Not a background — a prop or ear that reaches the artboard edge and would
  // clip. Coverage stays low, so the two findings are independent.
  const { findings } = await inspectAsset(
    svg(48, '<rect x="20" y="44" width="8" height="4" fill="#3b2a12"/>'),
    'sprite'
  );
  assert.deepEqual(codes(findings), ['perimeter']);
});

test('fixture: the portrait variant applies the 160px contract', async () => {
  const { findings } = await inspectAsset(
    svg(48, '<circle cx="24" cy="26" r="10" fill="#bd7c24"/>'),
    'portrait'
  );
  assert.ok(
    findings.some(f => f.code === 'dimensions'),
    'a 48px file offered as a portrait must fail the dimensions rule'
  );
});

test('fixture: malformed and missing assets are reported, not thrown', async () => {
  const malformed = await inspectAsset(Buffer.from('<svg><this is not markup'), 'sprite');
  assert.deepEqual(codes(malformed.findings), ['unreadable']);

  const missing = await inspectAsset(join(charDir, 'no-such-character/sprite.svg'), 'sprite');
  assert.deepEqual(codes(missing.findings), ['unreadable']);
});

test('fixture: an unknown variant is a programming error, not a finding', async () => {
  await assert.rejects(() => inspectAsset(svg(48, ''), 'banner'), /unknown variant/);
});

// ---------------------------------------------------------------------------
// Production sweep. This ran behind a temporary migration ratchet while the 27
// assets were converted one family at a time; the ratchet is gone because the
// list reached empty, so the contract now applies unconditionally.
// ---------------------------------------------------------------------------

const catalog = JSON.parse(readFileSync(catalogPath, 'utf8'));

const assets = catalog.characters.flatMap(ch =>
  Object.keys(ASSET_CONTRACT).map(variant => ({
    id: ch.id,
    variant,
    key: `${ch.id}/${variant}`,
    // The catalog path is repo-relative from the static root.
    path: join(repoRoot, 'internal/web/static', ch.assets[variant])
  }))
);

test('every catalog asset is present on disk', () => {
  const missing = assets.filter(a => !existsSync(a.path)).map(a => a.key);
  assert.deepEqual(
    missing,
    [],
    `catalog references assets that do not exist: ${missing.join(', ')}`
  );
});

test('catalog assets meet the transparent map-ready contract', async () => {
  const violations = [];

  for (const asset of assets) {
    const { findings } = await inspectAsset(asset.path, asset.variant);
    if (findings.length > 0) {
      violations.push(formatFindings(asset.id, asset.variant, findings));
    }
  }

  assert.deepEqual(
    violations,
    [],
    'character assets break the transparent map-ready contract:\n' + violations.join('\n')
  );
});
