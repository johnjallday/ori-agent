import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';

const captureConfig = readFileSync(
  new URL('../../playwright.readme.config.ts', import.meta.url),
  'utf8'
);
const appConfig = readFileSync(new URL('../../playwright.config.ts', import.meta.url), 'utf8');

test('README capture pins full-tile raster and font positioning without changing ordinary browser tests', () => {
  const args = captureConfig.match(/args:\s*\[([^\]]+)\]/)?.[1] || '';
  assert.match(args, /['"]--disable-font-subpixel-positioning['"]/);
  assert.match(args, /['"]--disable-partial-raster['"]/);
  assert.doesNotMatch(appConfig, /--disable-partial-raster|--disable-font-subpixel-positioning/);
});

test('README capture keeps one worker and no retries instead of retrying nondeterministic pixels to green', () => {
  assert.match(captureConfig, /workers:\s*1\b/);
  assert.match(captureConfig, /retries:\s*0\b/);
});
