import { expect, test } from '@playwright/test';
import { readFile } from 'node:fs/promises';

// Component-only populated picker: synthetic responses, no host catalog/model.
for (const operation of [0, 1]) {
  test(`manual ${operation ? 'activation selection' : 'collection'} picker pages, retains choices and never applies dirty intent`, async ({
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
      if (url.origin !== 'https://candidates.test') return route.abort();
      if (files[url.pathname])
        return route.fulfill({ contentType: 'text/javascript', body: files[url.pathname] });
      if (url.pathname !== '/') return route.abort();
      return route.fulfill({
        contentType: 'text/html',
        body: `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Saved member picker component</title><style>body{font:16px system-ui;margin:16px}label{display:block}button{margin:4px}section{overflow-wrap:anywhere}input[type=search]{max-width:100%;box-sizing:border-box}</style></head><body><h1>Saved member picker</h1><p>Synthetic component fixture, not live catalog state.</p><div id="review"></div><script type="module">import {mountCollectionReview} from '/home-collection-review.js';window.mountReview=mountCollectionReview;</script></body></html>`
      });
    });
    await page.goto('https://candidates.test');
    await page.waitForFunction(() => Boolean((window as any).mountReview));
    await page.evaluate(() => {
      const host = window as any;
      host.calls = [];
      const review = {
        arrangement_id: 'arrangement-a',
        review_token: 'review-a',
        catalog_revision: 7,
        expires_at: '2050-01-01T00:00:00Z',
        goal: 'Keep these catalog-only',
        rationale: 'Owner selection',
        unchanged: 1,
        needs_review: [],
        unknowns: [],
        coverage: { note: 'Saved metadata only' },
        operation: 0,
        offset: 0,
        total: 0,
        members: [],
        effects: [
          {
            kind: 'create_collection',
            collection_id: 'collection-a',
            before_name: '',
            after_name: 'Client references',
            before_members: 0,
            after_members: 0
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
          host.calls.push({ path, options });
          if (path.endsWith('/collection-review')) return review;
          if (path.endsWith('/collection-correction')) throw new Error('uncertain saved response');
          const url = new URL(path, location.origin);
          const offset = Number(url.searchParams.get('offset'));
          if (url.pathname.endsWith('/collection-review/review-a')) {
            return url.searchParams.get('operation') === '1'
              ? {
                  ...review,
                  operation: 1,
                  total: 1,
                  members: [
                    {
                      id: 'selected-song',
                      name: 'Selected song',
                      availability: 'available',
                      before: false,
                      after: true
                    }
                  ]
                }
              : review;
          }
          return {
            arrangement_id: 'arrangement-a',
            review_token: 'review-a',
            catalog_revision: 7,
            operation: Number(url.searchParams.get('operation')),
            offset,
            query: url.searchParams.get('query'),
            total: 21,
            members: Array.from({ length: Math.min(20, 21 - offset) }, (_, i) => ({
              id: `entry-${offset + i}`,
              name: `<img> Client ${offset + i}`,
              availability: 'revoked_source'
            })),
            ...(offset === 0 ? { next_offset: 20 } : {})
          };
        }
      });
    });
    await page.getByRole('button', { name: 'Review collection metadata', exact: true }).click();
    await expect(
      page.getByText('Each project still needs its own exact file choice', { exact: false })
    ).toBeVisible();
    await page.getByLabel('Collection effect', { exact: true }).selectOption('1');
    await expect(page.getByText('Add: Selected song · available', { exact: true })).toBeVisible();
    await page.getByLabel('Collection effect', { exact: true }).selectOption('0');
    if (operation === 1)
      await page.getByLabel('Collection effect', { exact: true }).selectOption('1');
    await page.getByText('Correct this proposal', { exact: true }).click();
    if (operation === 1) {
      await expect(
        page.getByText('this correction cannot approve their creation.', { exact: false })
      ).toBeVisible();
      await page.getByRole('checkbox', { name: 'Exclude Selected song', exact: true }).check();
    }
    expect(await page.evaluate(() => (window as any).calls.length)).toBe(3 + operation);
    await page.getByRole('button', { name: 'Find saved members', exact: true }).click();
    const picker = page.getByRole('region', { name: 'Add saved catalog members' });
    await expect(picker.getByRole('checkbox')).toHaveCount(20);
    await picker.getByRole('checkbox').first().check();
    await picker.getByRole('button', { name: 'Next candidates', exact: true }).click();
    await expect(picker.getByRole('status')).toBeFocused();
    await expect(picker.getByRole('checkbox')).toHaveCount(1);
    await picker.getByRole('checkbox').check();
    await expect(
      picker.getByRole('list', { name: 'Unsaved member additions' }).getByRole('listitem')
    ).toHaveCount(2);
    await expect(picker.locator('img')).toHaveCount(0);
    await expect(
      page.getByRole('button', {
        name: 'Save metadata and prepare activation reviews',
        exact: true
      })
    ).toHaveAttribute('aria-disabled', 'true');
    await page.setViewportSize({ width: 390, height: 844 });
    expect(await picker.evaluate(el => el.scrollWidth <= el.clientWidth)).toBe(true);
    await page.screenshot({
      path: info.outputPath('saved-member-additions-narrow.png'),
      fullPage: true
    });
    await page.getByRole('button', { name: 'Save correction — not apply', exact: true }).click();
    await expect(
      page.getByText('Correction outcome could not be confirmed.', { exact: false })
    ).toBeVisible();
    const calls = await page.evaluate(() => (window as any).calls);
    expect(calls).toHaveLength(6 + operation);
    const correction = JSON.parse(calls[5 + operation].options.body);
    expect(correction.operation).toBe(operation);
    expect(correction.add_ids).toEqual(['entry-0', 'entry-20']);
    expect(correction.exclude_ids).toEqual(operation ? ['selected-song'] : []);
    expect(calls.some((call: any) => call.path.endsWith('/collection-commit'))).toBe(false);
    await expect(
      picker.getByRole('list', { name: 'Unsaved member additions' }).getByRole('listitem')
    ).toHaveCount(2);
  });
}
