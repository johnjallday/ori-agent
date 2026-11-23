import { test, expect } from '@playwright/test';
import { readFileSync, mkdirSync, writeFileSync, utimesSync, existsSync } from 'node:fs';
import { join } from 'node:path';

const fixture = process.env.ORI_SEARCH_FIRST_FIXTURE;
const evidence = 'tasks/evidence-files-map-navigation';
test.skip(
  !fixture || process.env.ORI_SEARCH_FIRST_SCENARIO !== 'navigation',
  'requires a marked disposable profile'
);
test('distinguish duplicate copies by location and date before acting', async ({
  page,
  baseURL
}) => {
  expect(new URL(baseURL!).hostname).toBe('localhost');
  expect(readFileSync(join(fixture!, '.search-first-fixture'), 'utf8')).toBe('synthetic-v1\n');
  expect(readFileSync(join(fixture!, '.search-first-test-url'), 'utf8').trim()).toBe(baseURL);
  const original = join(fixture!, 'home/Downloads/Saved/proposal.pdf');
  for (const [dir, stamp] of [
    ['Documents/Final', 1704067200],
    ['Downloads/Saved', 1735689600]
  ] as const) {
    const root = join(fixture!, 'home', dir);
    mkdirSync(root, { recursive: true });
    const copy = join(root, 'proposal.pdf');
    writeFileSync(copy, 'Synthetic', { flag: 'wx' });
    utimesSync(copy, stamp, stamp);
  }
  for (let n = 0; n < 27; n++)
    writeFileSync(join(fixture!, `home/Downloads/sample-${n}.unknown`), '', { flag: 'wx' });
  await page.goto('/search');
  for (const kind of ['Documents', 'Downloads']) {
    await page.getByRole('button', { name: kind, exact: true }).click();
    await page.getByRole('dialog').getByRole('button', { name: 'Allow search' }).click();
    await expect(page.locator('#sources li').filter({ hasText: kind })).toContainText('ready');
  }
  const term = page.getByRole('searchbox');
  await term.fill('proposal');
  const rows = page.locator('#hits li');
  await expect(rows).toHaveCount(2);
  await expect(rows.first()).toContainText('Downloads / Saved');
  await expect(rows.last()).toContainText('Documents / Final');
  await rows.first().getByRole('button').click();
  await expect(page.locator('#selectedFile')).toContainText('Downloads / Saved');
  await page.screenshot({ path: join(evidence, 'copies.png') });
  await page.locator('#scope').selectOption({ label: 'Downloads' });
  await expect(rows).toHaveCount(1);
  await expect(page.locator('#selectedFile')).toBeHidden();
  await term.fill('sample-');
  await expect(rows).toHaveCount(25);
  await page.getByRole('button', { name: 'More results' }).click();
  await expect(rows).toHaveCount(27);
  await rows.last().getByRole('button').click();
  await expect(page.locator('#selectedFile button')).toHaveCount(1);
  await expect(page.locator('#selectedFile button')).toHaveText('Reveal in Finder');
  await page.locator('#selectedFile').scrollIntoViewIfNeeded();
  await page.screenshot({ path: join(evidence, 'scoped-last-page.png') });
  await page
    .locator('#sources li')
    .filter({ hasText: 'Downloads' })
    .getByRole('button', { name: 'Remove from search' })
    .click();
  await expect(rows).toHaveCount(0);
  await expect(page.locator('#selectedFile')).toBeHidden();
  expect(existsSync(original)).toBeTruthy();
});
