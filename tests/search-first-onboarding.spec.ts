import { test, expect } from '@playwright/test';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';

// Dedicated fresh split profile only. Never exercise desktop actions in the
// operator's running app. Caller supplies a fixture minted by the helper.
const fixture = process.env.ORI_SEARCH_FIRST_FIXTURE;
const scenario = process.env.ORI_SEARCH_FIRST_SCENARIO;
test.skip(!fixture || !scenario, 'Requires an isolated search-first fixture');
test('search before workspace setup', async ({ page, baseURL }) => {
  expect(readFileSync(join(fixture!, '.search-first-fixture'), 'utf8')).toBe('synthetic-v1\n');
  expect(readFileSync(join(fixture!, '.search-first-test-url'), 'utf8').trim()).toBe(baseURL);
  expect(new URL(baseURL!).hostname).toBe('localhost');
  const calls: string[] = [];
  const external: string[] = [];
  const errors: string[] = [];
  page.on('pageerror', e => errors.push(e.message));
  await page.route('**/*', r => {
    const u = new URL(r.request().url());
    if (u.origin !== baseURL) {
      external.push(u.origin);
      return r.abort();
    }
    calls.push(u.pathname);
    return r.continue();
  });
  const shot = async (name: string) => {
    if (process.env.ORI_SEARCH_EVIDENCE)
      await page.screenshot({
        path: join(process.env.ORI_SEARCH_EVIDENCE, `${name}.png`),
        fullPage: true
      });
  };
  await page.goto('/');
  await expect(page.getByRole('heading', { name: 'Start with one folder.' })).toBeVisible();
  await expect(page.getByText('No folders mapped yet.')).toBeVisible();
  await shot('first-open');
  expect(calls.filter(p => /\/(begin|select)$/.test(p))).toEqual([]);
  if (scenario === 'skip') {
    await page.getByRole('button', { name: 'Not now' }).click();
    await expect(page.getByRole('heading', { name: 'Welcome to Ori.' })).toBeVisible();
    await page.goto('/');
    await expect(page.getByRole('link', { name: 'Start or resume workspace setup' })).toBeVisible();
    await shot('deferred-home');
    await page.getByRole('link', { name: 'Search local files' }).click();
    await expect(page.getByRole('button', { name: 'Make Search my starting page' })).toBeVisible();
    await page.goto('/');
    await expect(page.getByRole('heading', { name: 'Welcome to Ori.' })).toBeVisible();
  } else {
    // This cancellation is an adapter seam; it is not a real Finder interaction.
    await page.route(
      '**/api/file-search/select',
      r => r.fulfill({ contentType: 'application/json', body: 'null' }),
      { times: 1 }
    );
    const custom = page.getByRole('button', { name: 'Choose another folder…' });
    await custom.click();
    await expect(page.getByText('No folder chosen.')).toBeVisible();
    await expect(custom).toBeFocused();
    await shot('cancel-adapter');
    const start = performance.now();
    await page.getByRole('button', { name: 'Documents', exact: true }).click();
    await page.locator('#hits li').first().waitFor();
    console.log(
      'First visible filename (ms):',
      Math.round(performance.now() - start),
      'source:',
      await page.locator('#sources li span').first().textContent()
    );
    const input = page.getByRole('searchbox');
    await expect(input).toBeFocused();
    await input.fill('Harbor');
    await expect(page.locator('#hits li')).toHaveCount(2);
    await expect(page.locator('#hits li').first()).toContainText('Final');
    await input.press('ArrowDown');
    await expect(page.locator('#hits li.current')).toContainText('Final');
    await shot('mapped');
    const intended = await page.locator('#hits li.current').getAttribute('data-id');
    let action: unknown;
    await page.route(
      '**/api/file-search/open',
      r => {
        action = r.request().postDataJSON();
        return r.fulfill({ status: 503, body: 'Document opening is unavailable.' });
      },
      { times: 1 }
    );
    await input.press('Enter');
    await expect(page.locator('#notice')).toHaveText('Document opening is unavailable.');
    expect(action).toEqual({ id: intended, generation: 1 });
    await shot('desktop-unavailable-adapter');
    await input.fill('Harbo');
    await expect(page.locator('#hits li')).toHaveCount(2);
    await page.setViewportSize({ width: 390, height: 844 });
    await page.emulateMedia({ colorScheme: 'dark', reducedMotion: 'reduce' });
    await shot('narrow-dark');
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)
    ).toBeTruthy();
    await page.setViewportSize({ width: 1280, height: 900 });
    await page.evaluate(() => (document.documentElement.style.zoom = '200%'));
    await shot('zoom-200');
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)
    ).toBeTruthy();
    const timings: number[] = [];
    for (let i = 0; i < 10; i++) {
      const hit = i % 2 === 0 ? 'Welcome' : 'Harbor';
      const ts = performance.now();
      await input.fill(hit);
      await page.waitForFunction(
        q => document.querySelector('#hits strong')?.textContent?.includes(q),
        hit,
        { polling: 'raf' }
      );
      timings.push(performance.now() - ts);
    }
    console.log(
      'Keypress-to-visible sample p95 (ms, ten fills):',
      Math.round(timings.sort((a, b) => a - b)[9])
    );
    await page.goto('/');
    await expect(page.getByRole('heading', { name: 'Your search desk.' })).toBeVisible();
    await expect(page.locator('#sources')).toContainText('Documents');
    await page.getByRole('link', { name: 'Home', exact: true }).click();
    await expect(page.getByRole('heading', { name: 'Welcome to Ori.' })).toBeVisible();
  }
  expect(
    calls.filter(
      p =>
        p.startsWith('/api/workspaces') ||
        p.startsWith('/api/onboarding') ||
        p.includes('assistant') ||
        p.includes('/api/chat')
    )
  ).toEqual([]);
  expect(external).toEqual([]);
  expect(errors).toEqual([]);
});
