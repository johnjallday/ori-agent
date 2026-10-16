import { expect, test } from '@playwright/test';
import { readFile } from 'node:fs/promises';

// Component-only intent changes: synthetic responses, no host catalog or model.
test('intent changes are keyboard-reachable, fit narrow screens and send one typed revision', async ({
  page
}, info) => {
  const files: Record<string, string> = {};
  for (const name of [
    'home-collection-review',
    'home-collection-candidates',
    'workspace-arrangement'
  ])
    files[`/${name}.js`] = await readFile(`internal/web/static/js/modules/${name}.js`, 'utf8');
  await page.route('**/*', async route => {
    const url = new URL(route.request().url());
    if (url.origin !== 'https://intent.test') return route.abort();
    if (files[url.pathname])
      return route.fulfill({ contentType: 'text/javascript', body: files[url.pathname] });
    if (url.pathname !== '/') return route.abort();
    return route.fulfill({
      contentType: 'text/html',
      body: `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Proposal intent component</title><style>body{font:16px system-ui;margin:16px}label{display:block}button{margin:4px}section,details{overflow-wrap:anywhere}.form-select{display:block;width:100%;max-width:100%}</style></head><body><h1>Proposal intent</h1><p>Synthetic component fixture, not live catalog state.</p><div id="review"></div><script type="module">import {mountCollectionReview} from '/home-collection-review.js';window.mountReview=mountCollectionReview;</script></body></html>`
    });
  });
  await page.goto('https://intent.test');
  await page.waitForFunction(() => Boolean((window as any).mountReview));
  await page.evaluate(() => {
    const host = window as any;
    host.calls = [];
    const review = {
      arrangement_id: 'arrangement-a',
      review_token: 'review-a',
      catalog_revision: 7,
      expires_at: '2050-01-01T00:00:00Z',
      goal: 'Keep experiments catalog-only; separate the client songs',
      rationale: 'Owner goal',
      unchanged: 3,
      needs_review: [{ reason: 'Which of the <b>two</b> mixes is authoritative?', count: 2 }],
      unknowns: [],
      coverage: { note: 'Saved metadata only' },
      operation: 1,
      offset: 0,
      total: 1,
      members: [
        {
          id: 'client-song',
          name: 'Client song',
          availability: 'available',
          before: false,
          after: true
        }
      ],
      effects: [
        {
          kind: 'create_collection',
          collection_id: 'collection-a',
          before_name: '',
          after_name: 'Client songs',
          before_members: 0,
          after_members: 1
        },
        {
          kind: 'select_activation',
          collection_id: '',
          queue_id: 'queue-a',
          before_name: '',
          after_name: 'Projects awaiting separate activation review',
          before_members: 0,
          after_members: 1
        }
      ]
    };
    host.mountReview(document.getElementById('review'), {
      ref: { id: 'arrangement-a', revision: 1, catalog_revision: 6 },
      isCurrent: () => true,
      reload: async () => {},
      request: async (path: string, options: any) => {
        host.calls.push({ path, body: options?.body ? JSON.parse(options.body) : null });
        if (path.endsWith('/collection-review')) return review;
        throw new Error('uncertain saved response');
      }
    });
  });
  await page.getByRole('button', { name: 'Review collection metadata', exact: true }).click();
  const summary = page.getByText('Change what this proposal intends', { exact: true });
  await summary.focus();
  await page.keyboard.press('Enter');
  for (const name of [
    'Keep these projects catalog-only instead',
    'Keep question 1 items unchanged',
    'Dismiss question 1',
    'Release 3 explicitly unchanged items'
  ])
    await expect(page.getByRole('button', { name, exact: true })).toBeVisible();
  // A collection-to-activation conversion is not offered while one exists.
  await expect(
    page.getByRole('button', { name: 'Also select catalog-only members for activation reviews' })
  ).toHaveCount(0);
  await expect(page.locator('b')).toHaveCount(0);
  await page.screenshot({ path: info.outputPath('intent-changes-desktop.png'), fullPage: true });
  await page.setViewportSize({ width: 390, height: 844 });
  const wide = await page.evaluate(() =>
    [...document.querySelectorAll('body *')]
      .filter(el => el.getBoundingClientRect().right > innerWidth + 1)
      .map(el => `${el.tagName}.${el.className}`)
  );
  expect(wide).toEqual([]);
  await page.screenshot({ path: info.outputPath('intent-changes-narrow.png'), fullPage: true });
  const convert = page.getByRole('button', { name: 'Keep these projects catalog-only instead' });
  await convert.focus();
  await page.keyboard.press('Enter');
  await expect(
    page.getByText('Correction outcome could not be confirmed.', { exact: false })
  ).toBeVisible();
  const calls = await page.evaluate(() => (window as any).calls);
  expect(calls).toHaveLength(2);
  expect(calls[1]).toEqual({
    path: '/arrangements/arrangement-a/collection-correction',
    body: { review_token: 'review-a', operation: 1, convert: 'catalog_only' }
  });
  // An uncertain outcome is never retried, and nothing is applied.
  await expect(convert).toHaveAttribute('aria-disabled', 'true');
  await convert.dispatchEvent('click');
  expect(await page.evaluate(() => (window as any).calls.length)).toBe(2);
  expect(calls.some((call: any) => call.path.endsWith('/collection-commit'))).toBe(false);
});
