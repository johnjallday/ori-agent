import { chromium, expect, type Page } from '@playwright/test';
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

// Actual tab zoom, not CSS zoom or viewport emulation. Both the extension and
// browser profile are owned disposable files; no installed browser is touched.
export async function withOwnedBrowserZoom(
  baseURL: string,
  zoom: number,
  run: (page: Page) => Promise<void>
) {
  const directory = mkdtempSync(join(tmpdir(), 'ori-home-review-zoom-'));
  let browser: Awaited<ReturnType<typeof chromium.launchPersistentContext>> | undefined;
  try {
    const extension = join(directory, 'extension');
    mkdirSync(extension, { mode: 0o700 });
    writeFileSync(
      join(extension, 'manifest.json'),
      JSON.stringify({
        manifest_version: 3,
        name: 'Owned test tab zoom',
        version: '1.0',
        permissions: ['tabs'],
        background: { service_worker: 'background.js' }
      }),
      { mode: 0o600 }
    );
    writeFileSync(
      join(extension, 'background.js'),
      'chrome.tabs.onUpdated.addListener(() => {});',
      {
        mode: 0o600
      }
    );
    browser = await chromium.launchPersistentContext(join(directory, 'profile'), {
      channel: 'chromium',
      headless: true,
      viewport: null,
      deviceScaleFactor: undefined,
      baseURL,
      ignoreDefaultArgs: ['--disable-extensions'],
      args: [
        `--disable-extensions-except=${extension}`,
        `--load-extension=${extension}`,
        '--window-size=1440,1000'
      ]
    });
    const page = browser.pages()[0] || (await browser.newPage());
    await page.goto('/');
    const before = await page.evaluate(() => ({ width: innerWidth, dpr: devicePixelRatio }));
    const worker = browser.serviceWorkers()[0] || (await browser.waitForEvent('serviceworker'));
    const actual = await worker.evaluate(
      async ({ origin, zoom }) => {
        const api = (globalThis as any).chrome;
        const tabs = await api.tabs.query({});
        const tab = tabs.find((item: any) => item.url?.startsWith(origin));
        if (!tab?.id) throw new Error('The owned test tab was not found');
        await api.tabs.setZoom(tab.id, zoom);
        return api.tabs.getZoom(tab.id);
      },
      { origin: baseURL, zoom }
    );
    expect(actual).toBe(zoom);
    await expect.poll(() => page.evaluate(() => innerWidth)).toBe(Math.floor(before.width / zoom));
    console.log('ACTUAL_BROWSER_ZOOM', JSON.stringify({ zoom: actual, before }));
    await run(page);
  } finally {
    try {
      await browser?.close();
    } finally {
      rmSync(directory, { recursive: true, force: true });
    }
  }
}
