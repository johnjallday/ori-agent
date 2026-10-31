import { chromium, expect, test, type Locator, type Page } from '@playwright/test';
import { randomUUID } from 'node:crypto';
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { openMapReceiptDetails } from './helpers/confirm-reviewed-creation';
import { startJanitorBuildModel } from './helpers/janitor-build-model';

// Shared review accessibility on a real isolated branch server. Only the
// loopback model catalog is synthetic (for staging a multi-role team); all
// creation, receipts and layout are real. Browser zoom is Chrome's actual tab
// zoom from an owned temporary profile, not CSS zoom or a smaller viewport.
const evidence = 'tasks/evidence-unified-workspace-placement/final';
let axe = '';
test.describe.configure({ mode: 'serial' });
test.setTimeout(90_000);

test.beforeAll(async ({ request }) => {
  mkdirSync(evidence, { recursive: true });
  const skipped = await request.post('/api/onboarding/skip');
  expect(skipped.ok(), await skipped.text()).toBeTruthy();
  const asset = await request.get('https://cdn.jsdelivr.net/npm/axe-core@4.10.3/axe.min.js', {
    timeout: 20_000
  });
  expect(asset.ok()).toBeTruthy();
  axe = await asset.text();
});

async function accessibility(page: Page, selector = '#workspaceCreationReviewPanel') {
  await page.addScriptTag({ content: axe });
  const violations = await page.evaluate(async selector => {
    const result = await (window as any).axe.run(selector, {
      runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'] }
    });
    return result.violations.map((item: any) => ({
      id: item.id,
      impact: item.impact,
      nodes: item.nodes.map((node: any) => node.target)
    }));
  }, selector);
  expect(violations).toEqual([]);
}

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

async function workspaceCount(page: Page) {
  return (await (await page.request.get('/api/workspaces')).json()).folders.length;
}

async function noHorizontalOverflow(page: Page) {
  expect(
    await page.evaluate(() => {
      const body = document.querySelector('#addFolderModal .modal-body') as HTMLElement | null;
      return {
        page: document.documentElement.scrollWidth > window.innerWidth,
        dialog: body ? body.scrollWidth > body.clientWidth + 1 : true
      };
    })
  ).toEqual({ page: false, dialog: false });
}

async function openBlankReview(page: Page, name: string) {
  await page.goto('/');
  const create = page.locator('#cockpitCreateWorkspaceBtn');
  await create.focus();
  await create.press('Enter');
  await page.locator('.workspace-template-card', { hasText: 'Blank' }).first().click();
  await page.locator('#wizardNextBtn').click();
  await page.locator('#folderNameInput').fill(name);
  await page.locator('#wizardNextBtn').click();
  await page.getByRole('checkbox', { name: 'Create without agents' }).check();
  await page.locator('#wizardNextBtn').click();
  const review = page.locator('#workspaceCreationReviewPanel');
  await expect(review).toBeVisible();
  return review;
}

for (const variant of [
  { name: 'desktop-light', width: 1280, height: 900, theme: 'light', touch: false, reduced: false },
  {
    name: 'desktop-dark-reduced',
    width: 1280,
    height: 900,
    theme: 'dark',
    touch: false,
    reduced: true
  },
  { name: 'narrow-touch-dark', width: 390, height: 844, theme: 'dark', touch: true, reduced: true }
] as const) {
  test.describe(variant.name, () => {
    test.use({
      viewport: { width: variant.width, height: variant.height },
      hasTouch: variant.touch,
      colorScheme: variant.theme,
      reducedMotion: variant.reduced ? 'reduce' : 'no-preference'
    });
    test('labelled review edits by keyboard or tap, confirms once and keeps focus', async ({
      page
    }) => {
      await page.addInitScript(theme => localStorage.setItem('ori-theme', theme), variant.theme);
      const before = await workspaceCount(page);
      const review = await openBlankReview(
        page,
        `A11y ${variant.name} ${randomUUID().slice(0, 6)}`
      );
      await expect(page.locator('html')).toHaveAttribute('data-bs-theme', variant.theme);
      await accessibility(page);
      await noHorizontalOverflow(page);
      // Edit inline through the labelled native field; focus stays there.
      const goal = review.getByRole('textbox', { name: 'Goal', exact: true });
      await goal.focus();
      await page.keyboard.type(' Reviewed by keyboard.');
      await expect(goal).toBeFocused();
      const map = review.getByRole('button', { name: 'Place & create on map', exact: true });
      if (variant.width < 980) await expect(map).toBeDisabled();
      const automatic = review.getByRole('button', {
        name: 'Use automatic placement',
        exact: true
      });
      await unobscured(automatic);
      if (variant.touch) {
        await automatic.tap();
        await expect(automatic).toHaveAttribute('aria-pressed', 'true');
      } else {
        await automatic.focus();
        await page.keyboard.press('Enter');
      }
      const confirm = review.getByRole('button', { name: 'Create workspace', exact: true });
      await unobscured(confirm);
      await page.screenshot({ path: `${evidence}/a11y-${variant.name}-review.png` });
      expect(await workspaceCount(page)).toBe(before);
      if (variant.touch) await confirm.tap();
      else await confirm.press('Enter');
      await expect(
        review.getByRole('heading', { name: 'Workspace created', exact: true })
      ).toBeVisible();
      // The result announces into its labelled status region.
      await expect(review.getByRole('status')).toContainText('Workspace created');
      expect(await workspaceCount(page)).toBe(before + 1);
      await accessibility(page);
      await page.screenshot({ path: `${evidence}/a11y-${variant.name}-result.png` });
    });
  });
}

test('keyboard-only map placement returns with Escape and creates once with Enter', async ({
  page
}) => {
  await page.setViewportSize({ width: 1280, height: 900 });
  const before = await workspaceCount(page);
  const name = `Keyboard map ${randomUUID().slice(0, 6)}`;
  const review = await openBlankReview(page, name);
  const place = review.getByRole('button', { name: 'Place & create on map', exact: true });
  await place.focus();
  await page.keyboard.press('Enter');
  await expect(page.locator('#addFolderModal')).toBeHidden();
  const preview = page.locator('[data-ws-map-placement-preview]');
  await expect(preview).toBeVisible();
  // The map owns keyboard input during placement; Escape is a labelled return.
  await expect(page.locator('[data-ws-map-placement-confirm]')).toBeVisible();
  await page.locator('[data-ws-map-viewport]').focus();
  await page.keyboard.press('Escape');
  await expect(review).toBeVisible();
  await expect(review.getByRole('textbox', { name: 'Workspace name', exact: true })).toHaveValue(
    name
  );
  expect(await workspaceCount(page)).toBe(before);
  // Pressed immediately, while the editor may still be returning from the Map.
  await place.focus();
  await page.keyboard.press('Enter');
  await expect(preview).toBeVisible();
  await page.locator('[data-ws-map-viewport]').focus();
  for (let n = 0; n < 60 && /is-invalid/.test((await preview.getAttribute('class')) || ''); n++)
    await page.keyboard.press('ArrowDown');
  await page.keyboard.press('Enter');
  await openMapReceiptDetails(page, 'Workspace created');
  await expect(
    review.getByRole('heading', { name: 'Workspace created', exact: true })
  ).toBeVisible();
  await expect(review.locator('[data-placement-focus="status"]')).toBeFocused();
  expect(await workspaceCount(page)).toBe(before + 1);
});

test('long and markup-like names stay literal and readable at phone width', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.addInitScript(() => localStorage.setItem('ori-theme', 'dark'));
  const hostile = `<img src=x onerror="window.__unifiedReviewInjected=1"> ${'Longnameword'.repeat(8)}`;
  const review = await openBlankReview(page, hostile.slice(0, 120));
  await expect(review.getByRole('textbox', { name: 'Workspace name', exact: true })).toHaveValue(
    hostile.slice(0, 120)
  );
  expect(await review.locator('img, script').count()).toBe(0);
  await noHorizontalOverflow(page);
  await accessibility(page);
  expect(await page.evaluate(() => (window as any).__unifiedReviewInjected)).toBeUndefined();
  await page.screenshot({ path: `${evidence}/a11y-hostile-narrow.png` });
  await review.getByRole('button', { name: 'Cancel draft', exact: true }).click();
  await expect(page.locator('#addFolderModal')).toBeHidden();
});

test('a General Group review is labelled and accessible before any creation', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 900 });
  const before = await workspaceCount(page);
  await page.goto('/');
  await page.locator('#cockpitCreateWorkspaceBtn').click();
  await page.locator('#workspaceCreatorKindGroup').check();
  await page.locator('#wizardNextBtn').click();
  await page.locator('#folderNameInput').fill(`A11y group ${randomUUID().slice(0, 6)}`);
  await page.locator('#folderDescriptionInput').fill('Accessible group purpose');
  await page.locator('#wizardNextBtn').click();
  await page.locator('#workspaceTeamRoster [data-team-agent-setup]').click();
  await page.locator('#createAgentBtn').click();
  await expect(page.locator('#addAgentModal')).toBeHidden();
  await page.locator('#wizardNextBtn').click();
  const review = page.locator('#workspaceCreationReviewPanel');
  await expect(review).toContainText('Group Manager');
  await accessibility(page);
  await page.screenshot({ path: `${evidence}/a11y-group-review.png` });
  await review.getByRole('button', { name: 'Cancel draft', exact: true }).click();
  expect(await workspaceCount(page)).toBe(before);
});

test.describe('multi-role review with the loopback model catalog', () => {
  let stopModel: Awaited<ReturnType<typeof startJanitorBuildModel>> | undefined;
  test.beforeAll(async ({ request }) => {
    stopModel = await startJanitorBuildModel();
    const model = await request.post('/api/settings/system-model', {
      data: { provider: 'lmstudio', model: 'ori-synthetic-model' }
    });
    expect(model.ok(), await model.text()).toBeTruthy();
  });
  test.afterAll(async () => {
    await stopModel?.();
  });
  test('staged multi-role team is reachable and accessible in the shared review', async ({
    page
  }) => {
    await page.setViewportSize({ width: 1280, height: 900 });
    const before = await workspaceCount(page);
    await page.goto('/');
    await page.locator('#cockpitCreateWorkspaceBtn').click();
    await page.locator('.workspace-template-card', { hasText: 'Research' }).first().click();
    await page.locator('#wizardNextBtn').click();
    await page.locator('#folderNameInput').fill(`A11y research ${randomUUID().slice(0, 6)}`);
    await page.locator('#wizardNextBtn').click();
    const roster = page.locator('#workspaceRoleRoster');
    for (let n = 0; n < 6; n++) {
      const create = roster.getByRole('button', { name: /^Create an agent for / }).first();
      if (!(await create.isVisible())) break;
      await create.click();
      const form = page.locator('#addAgentModal');
      await form.locator('[data-agent-create-field="model"]').selectOption('ori-synthetic-model');
      await form.getByRole('button', { name: 'Add to team', exact: true }).click();
      await expect(form).toBeHidden();
    }
    await page.locator('#wizardNextBtn').click();
    const review = page.locator('#workspaceCreationReviewPanel');
    await expect(review).toBeVisible();
    await expect(review.locator('.assisted-placement__slots')).toBeVisible();
    await accessibility(page);
    await page.screenshot({ path: `${evidence}/a11y-multi-role-review.png`, fullPage: true });
    await review.getByRole('button', { name: 'Cancel draft', exact: true }).click();
    expect(await workspaceCount(page)).toBe(before);
  });
});

test('actual Chrome 200 percent zoom keeps the shared review complete and operable', async ({
  baseURL
}) => {
  const directory = mkdtempSync(join(tmpdir(), 'ori-unified-review-zoom-'));
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
    const count = await workspaceCount(page);
    const review = await openBlankReview(page, `Zoomed ${randomUUID().slice(0, 6)}`);
    await noHorizontalOverflow(page);
    await accessibility(page);
    const automatic = review.getByRole('button', { name: 'Use automatic placement', exact: true });
    await automatic.focus();
    await page.keyboard.press('Enter');
    const confirm = review.getByRole('button', { name: 'Create workspace', exact: true });
    await unobscured(confirm);
    await confirm.press('Enter');
    await expect(
      review.getByRole('heading', { name: 'Workspace created', exact: true })
    ).toBeVisible();
    expect(await workspaceCount(page)).toBe(count + 1);
    console.log('ACTUAL_BROWSER_ZOOM', JSON.stringify({ zoom, before }));
  } finally {
    await browser?.close();
    rmSync(directory, { recursive: true, force: true });
  }
});
