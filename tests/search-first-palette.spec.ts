import { test, expect } from '@playwright/test';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { installLocalCdn } from './helpers/offline-cdn';
const fixture = process.env.ORI_SEARCH_FIRST_FIXTURE;
test.skip(
  !fixture || process.env.ORI_SEARCH_FIRST_SCENARIO !== 'established',
  'Requires the prior-version HQ fixture'
);
test('Files and Notes keep separate inputs, requests and history on established Home', async ({
  page,
  baseURL
}) => {
  expect(readFileSync(join(fixture!, '.search-first-test-url'), 'utf8').trim()).toBe(baseURL);
  await page.route('**/*', r =>
    new URL(r.request().url()).origin === baseURL ? r.continue() : r.abort()
  );
  await installLocalCdn(page);
  const noteRequests: string[] = [];
  page.on('request', r => {
    if (r.url().includes('/api/notes/search')) noteRequests.push(r.url());
  });
  await page.goto('/');
  await expect(page.locator('.home-command-bridge')).toBeVisible();
  await expect(page.getByRole('link', { name: 'Search local files' })).toBeVisible();
  if (process.env.ORI_SEARCH_EVIDENCE)
    await page.screenshot({ path: join(process.env.ORI_SEARCH_EVIDENCE, 'established-home.png') });
  await page.getByRole('link', { name: 'Search local files' }).click();
  await page.getByRole('button', { name: 'Documents', exact: true }).click();
  await expect(page.locator('#hits li').first()).toBeVisible();
  await page.goto('/');
  await expect(page.locator('.home-command-bridge')).toBeVisible();
  await page.keyboard.press('Control+k');
  const palette = page.locator('#searchPalette');
  await expect(palette).toBeVisible();
  await palette.getByRole('button', { name: 'Files', exact: true }).click();
  const field = palette.getByRole('searchbox');
  await field.fill('Harbor');
  await expect(palette.locator('[data-file-role="hits"] li')).toHaveCount(2);
  await field.press('ArrowDown');
  await expect(palette.locator('li.current')).toContainText('Final');
  expect(noteRequests.filter(u => u.includes('Harbor'))).toEqual([]);
  expect(await page.evaluate(() => localStorage.getItem('note.search.recent'))).toBeNull();
  await palette.getByRole('button', { name: 'Notes and headings' }).click();
  const input = palette.locator('#searchPaletteInput');
  await expect(input).toHaveValue('');
  await input.fill('Notebook');
  await expect
    .poll(() => noteRequests.filter(u => u.includes('Notebook')).length)
    .toBeGreaterThan(0);
  await input.press('Escape');
  await expect(palette).toBeHidden();
});
