import { chromium, expect, test, type Page, type Locator } from '@playwright/test';
import { existsSync, mkdirSync, mkdtempSync, realpathSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, sep } from 'node:path';
import {
  attachPlacementFolder as attach,
  placementJSON as json
} from './helpers/assisted-workspace-placement';

// A separate FRESH no-provider inbox sandbox. All application APIs are real.
// Browser zoom is Chrome's actual tab zoom, not CSS zoom, pinch or a smaller
// emulated viewport. The test-only extension lives in an owned temporary profile.
const sandbox = process.env.ORI_INBOX_SANDBOX || '';
const evidence = 'tasks/evidence-assisted-workspace-placement/group-4';
let axe = '';
test.skip(!sandbox, 'requires a fresh isolated inbox demo');
test.describe.configure({ mode: 'serial' });
test.setTimeout(90_000);
test.beforeAll(async ({ request }) => {
  expect(realpathSync(sandbox).startsWith(realpathSync(tmpdir()) + sep)).toBeTruthy();
  expect(sandbox).toContain('ori-inbox-demo-');
  mkdirSync(evidence, { recursive: true });
  expect(
    await (await request.get('/api/workspaces/build-sessions/availability')).json()
  ).toMatchObject({ available: false, reason: 'no_model' });
  const asset = await request.get('https://cdn.jsdelivr.net/npm/axe-core@4.10.3/axe.min.js', {
    timeout: 20_000
  });
  expect(asset.ok()).toBeTruthy();
  axe = await asset.text();
});

async function unobscured(control: Locator) {
  await control.focus();
  await expect(control).toBeFocused();
  await expect(control).toBeInViewport();
  expect(
    await control.evaluate(element => {
      const r = element.getBoundingClientRect();
      const hit = document.elementFromPoint(r.left + r.width / 2, r.top + r.height / 2);
      return element === hit || element.contains(hit);
    })
  ).toBeTruthy();
}

async function accessibility(page: Page) {
  await page.addScriptTag({ content: axe });
  const violations = await page.evaluate(async () => {
    const result = await (window as any).axe.run('#assistedWorkspacePlacementPanel', {
      runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'] }
    });
    return result.violations.map((item: any) => ({
      id: item.id,
      impact: item.impact,
      nodes: item.nodes.map((node: any) => node.target)
    }));
  });
  expect(violations).toEqual([]);
}

async function capture(page: Page, path: string, tabZoom: boolean) {
  if (!tabZoom) {
    await page.screenshot({ path });
    return;
  }
  // Playwright's CSS-sized clip crops a real tab-zoomed surface. Capture that
  // owned renderer without a clip, and verify that its physical width is whole.
  const session = await page.context().newCDPSession(page);
  try {
    const result = await session.send('Page.captureScreenshot', {
      format: 'png',
      captureBeyondViewport: false
    });
    const image = Buffer.from(result.data, 'base64');
    expect(image.readUInt32BE(16)).toBe(
      await page.evaluate(() => Math.round(innerWidth * devicePixelRatio))
    );
    writeFileSync(path, image, { mode: 0o600 });
  } finally {
    await session.detach();
  }
}

async function reviewAndCancel(
  page: Page,
  name: string,
  map: boolean,
  touch = false,
  tabZoom = false
) {
  const before = await json(page, '/api/workspaces');
  const profiles = await json(page, '/api/agents');
  const inference: string[] = [];
  page.on('request', request => {
    if (request.method() === 'POST' && /\/(turns|ask)$/.test(new URL(request.url()).pathname))
      inference.push(request.url());
  });
  await attach(page, 'Desktop', map, !touch);
  const entry = page.getByRole('button', {
    name: map ? 'Place & review' : 'Use suggested setup',
    exact: true
  });
  if (touch) await entry.tap();
  else {
    await entry.focus();
    await entry.press('Enter');
  }
  const panel = page.locator('#assistedWorkspacePlacementPanel');
  await expect(panel).toBeVisible();
  if (map && (await page.evaluate(() => innerWidth >= 980))) {
    const canvas = page.locator('[data-ws-map-viewport]').filter({ visible: true }).first();
    await expect(canvas).toBeFocused();
    await canvas.press('ArrowRight');
    await canvas.press('Enter');
    await canvas.press('Tab');
    await expect(canvas).not.toBeFocused();
  } else {
    await expect(panel).not.toHaveClass(/assisted-placement-host--map/);
  }
  const confirm = panel.getByRole('button', { name: 'Create workspace & scan', exact: true });
  await expect(confirm).toBeEnabled({ timeout: 30_000 });
  const point = await page.evaluate(
    () => (window as any).sessionManager.workspaceBuild.placement.candidate
  );
  const field = panel.getByLabel('Workspace name');
  const longName = '<svg onload=window.__placementInjected=true> 업무 Ω ' + '긴이름'.repeat(25);
  await field.focus();
  await field.fill(longName);
  await field.press('ArrowLeft');
  expect(
    await page.evaluate(() => (window as any).sessionManager.workspaceBuild.placement.candidate)
  ).toEqual(point);
  await field.press('Tab');
  await expect(confirm).toBeEnabled({ timeout: 30_000 });
  const goal = panel.getByLabel('Goal', { exact: true });
  await expect(goal).toBeFocused();
  await goal.fill('매일 검토. <b>Plain text, not markup.</b>\nKeep this exact goal.');
  await goal.press('Tab');
  await expect(confirm).toBeEnabled({ timeout: 30_000 });
  await expect(
    panel.getByRole('button', { name: 'Browse setup cards', exact: true })
  ).toBeFocused();
  await expect(panel.locator('.assisted-placement__review')).toContainText(longName);
  await expect(panel.locator('.assisted-placement__review')).toContainText('Keep this exact goal.');
  expect(await page.evaluate(() => (window as any).__placementInjected === true)).toBe(false);
  expect(
    await panel.evaluate(element => element.scrollWidth <= element.clientWidth + 1)
  ).toBeTruthy();
  const box = await panel.boundingBox();
  const width = await page.evaluate(() => innerWidth);
  expect(box!.x).toBeGreaterThanOrEqual(0);
  expect(box!.x + box!.width).toBeLessThanOrEqual(width + 1);
  await unobscured(field);
  await capture(page, `${evidence}/a11y-${name}-slots.png`, tabZoom);
  for (const control of [
    field,
    goal,
    panel.getByRole('button', { name: 'Choose agent', exact: true }),
    panel.getByRole('button', { name: 'Choose folder', exact: true }),
    confirm
  ])
    await unobscured(control);
  await accessibility(page);
  await capture(page, `${evidence}/a11y-${name}-review.png`, tabZoom);
  expect((await json(page, '/api/workspaces')).folders).toEqual(before.folders);
  expect(await json(page, '/api/agents')).toEqual(profiles);
  expect(existsSync(join(sandbox, 'Desktop', 'Filed'))).toBe(false);
  const cancel = panel.getByRole('button', { name: 'Cancel draft', exact: true });
  if (touch) await cancel.tap();
  else {
    await cancel.focus();
    await cancel.press('Enter');
  }
  await expect(panel).toBeHidden();
  expect((await json(page, '/api/workspaces')).folders).toEqual(before.folders);
  expect(await json(page, '/api/agents')).toEqual(profiles);
  expect(inference).toEqual([]);
}

for (const variant of [
  {
    name: 'desktop-light',
    theme: 'light',
    width: 1440,
    height: 1000,
    map: true,
    touch: false,
    reduced: false
  },
  {
    name: 'desktop-dark',
    theme: 'dark',
    width: 1440,
    height: 1000,
    map: true,
    touch: false,
    reduced: false
  },
  {
    name: 'narrow-reduced',
    theme: 'light',
    width: 390,
    height: 844,
    map: true,
    touch: false,
    reduced: true
  },
  {
    name: 'narrow-touch-dark',
    theme: 'dark',
    width: 390,
    height: 844,
    map: false,
    touch: true,
    reduced: true
  }
] as const) {
  test.describe(variant.name, () => {
    test.use({
      viewport: { width: variant.width, height: variant.height },
      hasTouch: variant.touch,
      colorScheme: variant.theme,
      reducedMotion: variant.reduced ? 'reduce' : 'no-preference'
    });
    test('labelled keyboard/tap controls keep choices visible and remain inert', async ({
      page
    }) => {
      await page.addInitScript(theme => localStorage.setItem('ori-theme', theme), variant.theme);
      await reviewAndCancel(page, variant.name, variant.map, variant.touch);
      await expect(page.locator('html')).toHaveAttribute('data-bs-theme', variant.theme);
    });
  });
}

test('actual Chrome 200 percent zoom keeps the complete reviewed controls accessible', async ({
  baseURL
}) => {
  const directory = mkdtempSync(join(tmpdir(), 'ori-placement-browser-zoom-'));
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
  writeFileSync(join(extension, 'background.js'), 'chrome.tabs.onUpdated.addListener(() => {});', {
    mode: 0o600
  });
  let browser: Awaited<ReturnType<typeof chromium.launchPersistentContext>> | undefined;
  try {
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
    await page.addInitScript(() => localStorage.setItem('ori-theme', 'dark'));
    await page.goto('/');
    const before = await page.evaluate(() => ({ width: innerWidth, dpr: devicePixelRatio }));
    const worker = browser.serviceWorkers()[0] || (await browser.waitForEvent('serviceworker'));
    const zoom = await worker.evaluate(async origin => {
      const api = (globalThis as any).chrome;
      const tabs = await api.tabs.query({});
      const tab = tabs.find((item: any) => item.url?.startsWith(origin));
      if (!tab?.id) throw new Error('The owned test tab was not found');
      await api.tabs.setZoom(tab.id, 2);
      return api.tabs.getZoom(tab.id);
    }, baseURL!);
    expect(zoom).toBe(2);
    await expect.poll(() => page.evaluate(() => innerWidth)).toBe(Math.floor(before.width / 2));
    const after = await page.evaluate(() => ({ width: innerWidth, dpr: devicePixelRatio }));
    expect(after.dpr).toBe(before.dpr * 2);
    console.log('ACTUAL_BROWSER_ZOOM', JSON.stringify({ zoom, before, after }));
    await reviewAndCancel(page, 'actual-200-percent-dark', true, false, true);
  } finally {
    await browser?.close();
    rmSync(directory, { recursive: true, force: true });
  }
});

test('narrow keyboard-only suggested setup confirms once without dragging', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  const before = await json(page, '/api/workspaces');
  await attach(page, 'Desktop', false, true);
  const entry = page.getByRole('button', { name: 'Use suggested setup', exact: true });
  await entry.focus();
  await entry.press('Enter');
  const panel = page.locator('#assistedWorkspacePlacementPanel');
  const confirm = panel.getByRole('button', { name: 'Create workspace & scan', exact: true });
  await expect(confirm).toBeEnabled({ timeout: 30_000 });
  await unobscured(confirm);
  await confirm.press('Enter');
  await expect(panel.getByRole('heading', { name: 'Workspace prepared' })).toBeVisible({
    timeout: 30_000
  });
  await expect(panel.getByRole('status')).toBeFocused();
  await expect(panel).toContainText('Assigned: File Curator');
  await expect(panel).toContainText('first scan found no eligible files');
  expect((await json(page, '/api/workspaces')).folders.length).toBe(before.folders.length + 1);
  await accessibility(page);
  await page.screenshot({ path: `${evidence}/a11y-narrow-keyboard-result.png`, fullPage: true });
});
