import { expect, test } from '@playwright/test';
import { readFile } from 'node:fs/promises';

// Component evidence only: synthetic catalog, no server state or model calls.
test('logical collections page exact membership, escape names and recover from stale reads', async ({
  page
}, info) => {
  const files: Record<string, string> = {
    '/collections.js': await readFile('internal/web/static/js/modules/home-collections.js', 'utf8'),
    '/workspace-arrangement.js': await readFile(
      'internal/web/static/js/modules/workspace-arrangement.js',
      'utf8'
    ),
    '/style.css': await readFile('internal/web/static/css/workspace-arrangement.css', 'utf8')
  };
  await page.route('**/*', async route => {
    const url = new URL(route.request().url());
    if (url.origin !== 'http://collections.test') return route.abort();
    if (files[url.pathname])
      return route.fulfill({
        contentType: url.pathname.endsWith('.css') ? 'text/css' : 'text/javascript',
        body: files[url.pathname]
      });
    if (url.pathname !== '/') return route.abort();
    return route.fulfill({
      contentType: 'text/html',
      body: `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><link rel="stylesheet" href="/style.css"><title>Collection component fixture</title></head><body><main style="max-width:720px;margin:auto;padding:12px"><h1>Logical collections</h1><p>Component fixture, not live application state.</p><div id="catalog"></div></main><script type="module">import {mountHomeCollections} from '/collections.js'; window.mountCollections=mountHomeCollections;</script></body></html>`
    });
  });
  await page.goto('http://collections.test');
  await page.waitForFunction(() => Boolean((window as any).mountCollections));
  await page.evaluate(async () => {
    const host = window as any;
    host.calls = [];
    host.fail = false;
    const pin = 'a'.repeat(64);
    const cards = Array.from({ length: 21 }, (_, i) => ({
      id: `collection-${i}`,
      name: i ? `Client ${i}` : '<img onerror=alert(1)>',
      revision: 1,
      members: 101
    }));
    host.browser = host.mountCollections(document.getElementById('catalog'), {
      isCurrent: () => true,
      request: async (path: string) => {
        host.calls.push(path);
        if (host.fail) throw new Error('private source path');
        const url = new URL(path, location.origin);
        const offset = Number(url.searchParams.get('offset') || 0);
        if (url.pathname === '/collections')
          return {
            context_pin: pin,
            revision: 3,
            offset,
            total: 21,
            collections: cards.slice(offset, offset + 20),
            ...(offset === 0 ? { next_offset: 20 } : {})
          };
        const id = url.pathname.split('/').pop();
        const collection = cards.find(c => c.id === id);
        return {
          context_pin: pin,
          revision: 3,
          offset,
          total: 101,
          collection,
          members: Array.from({ length: Math.min(50, 101 - offset) }, (_, i) => ({
            id: `entry-${offset + i}`,
            name: '<script>' + 'long'.repeat(80),
            format: 'reaper',
            last_observed_availability: 'revoked_source'
          })),
          ...(offset < 100 ? { next_offset: offset + 50 } : {})
        };
      }
    });
    await host.browser.refresh();
  });
  await expect(page.locator('.home-collections__cards > li')).toHaveCount(20);
  await page.getByRole('button', { name: 'Next page', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Saved collections', exact: true })).toBeFocused();
  await expect(page.locator('.home-collections__cards > li')).toHaveCount(1);
  await page.getByRole('button', { name: 'Previous page', exact: true }).click();
  await page
    .getByRole('button', { name: 'View members: <img onerror=alert(1)>', exact: true })
    .click();
  await expect(page.locator('.home-collections__members > li')).toHaveCount(50);
  await expect(page.locator('#catalog img, #catalog script')).toHaveCount(0);
  await page.getByRole('button', { name: 'Next page', exact: true }).click();
  await expect(page.getByText('51–100 of 101 exact members.', { exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Next page', exact: true }).click();
  await expect(page.getByText('101–101 of 101 exact members.', { exact: true })).toBeVisible();
  await page.setViewportSize({ width: 390, height: 844 });
  expect(await page.locator('#catalog').evaluate(el => el.scrollWidth <= el.clientWidth)).toBe(
    true
  );
  await page.screenshot({ path: info.outputPath('collection-members-narrow.png'), fullPage: true });
  await page.evaluate(() => {
    (window as any).fail = true;
  });
  await page.getByRole('button', { name: 'Previous page', exact: true }).click();
  await expect(page.getByRole('status')).toContainText('Refresh collections to read');
  await expect(page.locator('.home-collections__members')).toHaveCount(0);
  const calls = await page.evaluate(() => (window as any).calls.length);
  await expect(page.locator('#catalog')).not.toContainText('private source path');
  expect(await page.evaluate(() => (window as any).calls.length)).toBe(calls);
  await page.evaluate(() => {
    (window as any).fail = false;
  });
  await page.getByRole('button', { name: 'Refresh collections', exact: true }).click();
  await expect(page.locator('.home-collections__cards > li')).toHaveCount(20);
});
