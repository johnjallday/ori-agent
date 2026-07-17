#!/usr/bin/env node

import { createHash } from 'node:crypto';
import { existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import path from 'node:path';
import { pathToFileURL } from 'node:url';
import sharp from 'sharp';

import { loadManifest } from './manifest.mjs';

export const WEBP_OPTIONS = Object.freeze({
  quality: 82,
  effort: 6,
  smartSubsample: false,
  alphaQuality: 100,
  preset: 'text',
});

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

function sha256(bytes) {
  return createHash('sha256').update(bytes).digest('hex');
}

function resolveDirectory(value, label) {
  if (!value) fail(`${label} is required.`);
  const resolved = path.resolve(value);
  if (!existsSync(resolved)) fail(`${label} does not exist: ${resolved}`);
  return resolved;
}

export async function encodePortfolio({ manifestPath, sourceDir, outputDir }) {
  const manifestRoot = path.resolve(path.dirname(manifestPath), '..');
  const { manifest } = await loadManifest(manifestRoot, path.relative(manifestRoot, manifestPath));
  const rawDir = resolveDirectory(sourceDir, 'Source directory');
  const proposedDir = path.resolve(outputDir);
  mkdirSync(proposedDir, { recursive: true });
  const outputs = [];

  for (const scene of manifest.screenshots) {
    const input = path.join(rawDir, `${scene.id}.png`);
    const output = path.join(proposedDir, `${scene.id}.webp`);
    if (!existsSync(input)) fail(`Missing raw PNG for ${scene.id}: ${input}`);
    const rawMetadata = await sharp(input, { animated: false }).metadata();
    if (rawMetadata.width !== 2880 || rawMetadata.height !== 1800 || rawMetadata.format !== 'png') {
      fail(`Raw ${scene.id} must be a 2880x1800 PNG, received ${rawMetadata.format} ${rawMetadata.width}x${rawMetadata.height}.`);
    }
    await sharp(input, { animated: false, limitInputPixels: 2880 * 1800 })
      .rotate()
      .webp(WEBP_OPTIONS)
      .toFile(output);
    const bytes = readFileSync(output);
    const encodedMetadata = await sharp(output, { animated: false }).metadata();
    if (encodedMetadata.format !== 'webp' || encodedMetadata.width !== 2880 || encodedMetadata.height !== 1800) {
      fail(`Encoded ${scene.id} is not the required 2880x1800 WebP.`);
    }
    outputs.push({
      id: scene.id,
      input: path.relative(path.dirname(manifestPath), input),
      output: path.relative(path.dirname(manifestPath), output),
      sha256: sha256(bytes),
      bytes: bytes.byteLength,
      width: encodedMetadata.width,
      height: encodedMetadata.height,
      format: encodedMetadata.format,
    });
  }
  return {
    encoder: { package: 'sharp', version: sharp.versions?.sharp ?? null, options: WEBP_OPTIONS },
    images: outputs,
  };
}

async function main() {
  const values = parseArgs(process.argv.slice(2));
  const manifestPath = path.resolve(values.manifest ?? 'docs/readme-screenshots.json');
  const sourceDir = values['source-dir'];
  const outputDir = values['output-dir'];
  const result = await encodePortfolio({ manifestPath, sourceDir, outputDir });
  if (values['metadata-path']) {
    writeFileSync(path.resolve(values['metadata-path']), `${JSON.stringify(result, null, 2)}\n`);
  }
  process.stdout.write(`${JSON.stringify(result, null, 2)}\n`);
}

if (process.argv[1] && pathToFileURL(path.resolve(process.argv[1])).href === import.meta.url) {
  main().catch((error) => {
    process.stderr.write(`README WebP encoder error: ${error.message}\n`);
    process.exitCode = 2;
  });
}
