import { test, expect } from '@playwright/test';
import { readFileSync, writeFileSync, existsSync } from 'node:fs';
import { join } from 'node:path';
// Run AFTER restarting the same marked search-first-onboarding fixture.
const fixture = process.env.ORI_SEARCH_FIRST_FIXTURE;
test.skip(
  !fixture || process.env.ORI_SEARCH_FIRST_SCENARIO !== 'return',
  'Requires the reopened search-first fixture'
);
test('manual refresh, two-tab removal, empty desk after return', async ({
  page,
  context,
  baseURL
}) => {
  expect(readFileSync(join(fixture!, '.search-first-fixture'), 'utf8')).toBe('synthetic-v1\n');
  expect(readFileSync(join(fixture!, '.search-first-test-url'), 'utf8').trim()).toBe(baseURL);
  expect(new URL(baseURL!).hostname).toBe('localhost');
  await page.goto('/');
  await expect(page.getByRole('heading', { name: 'Files', exact: true })).toBeVisible();
  const sources = page.locator('#sources');
  await expect(sources).toContainText('Documents');
  const term = page.getByRole('searchbox');
  const unverified = join(fixture!, 'home/Documents/Added after scan.txt');
  writeFileSync(unverified, 'Synthetic', { flag: 'wx' });
  await term.fill('Added after scan');
  await expect(page.locator('#coverage')).toHaveText('No matches in Documents');
  await sources.getByRole('button', { name: 'Refresh' }).click();
  await expect(page.locator('#hits')).toContainText('Added after scan');
  const otherFile = join(fixture!, 'home/Downloads/Harbor.txt');
  writeFileSync(otherFile, 'Synthetic', { flag: 'wx' });
  const downloads = page.getByRole('button', { name: 'Downloads', exact: true });
  await downloads.click();
  const consent = page.getByRole('dialog', { name: 'Allow Ori to search Downloads?' });
  await consent.getByRole('button', { name: 'Allow search' }).click();
  await term.fill('Harbor');
  await expect(page.locator('#hits li')).toHaveCount(3);
  const other = await context.newPage();
  await other.goto('/search');
  await other.getByRole('searchbox').fill('Harbor');
  await expect(other.locator('#hits li')).toHaveCount(3);
  const old = await page.evaluate(async () => {
    const res = await fetch('/api/file-search/query', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ term: 'Harbor' })
    });
    const j = await res.json();
    return j.hits.find(
      (h: { source: string }) =>
        h.source === j.sources.find((s: { label: string }) => s.label === 'Downloads').id
    );
  });
  await sources
    .locator('li')
    .filter({ hasText: 'Downloads' })
    .getByRole('button', { name: 'Remove from search' })
    .click();
  await expect(other.locator('#hits li')).toHaveCount(2);
  expect(existsSync(otherFile)).toBeTruthy();
  expect(
    await page.evaluate(
      async h =>
        (
          await fetch('/api/file-search/open', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ id: h.id, generation: h.generation })
          })
        ).status,
      old
    )
  ).toBe(409);
  await sources.getByRole('button', { name: 'Remove from search' }).click();
  await expect(other.locator('#emptyMap')).toHaveText('No folders mapped yet.');
  await page.goto('/');
  await expect(page.getByRole('heading', { name: 'Files', exact: true })).toBeVisible();
  await expect(page.locator('#sources li')).toHaveCount(0);
  if (process.env.ORI_SEARCH_EVIDENCE)
    await page.screenshot({ path: join(process.env.ORI_SEARCH_EVIDENCE, 'empty-return.png') });
});
