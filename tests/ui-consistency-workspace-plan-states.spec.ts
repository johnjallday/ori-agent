import { expect, test } from '@playwright/test';
import { capture, contrast, preparePage, reachable } from './helpers/ui-consistency';
import {
  createWorkspaceSeed,
  freezeWorkspaceSeed,
  retireWorkspaceSeed,
  type WorkspaceSeed
} from './helpers/ui-consistency-workspace';
import {
  guardWorkspaceReadTour,
  workspaceMetadataReads
} from './fixtures/ui-consistency-workspace';

let seed: WorkspaceSeed | undefined;
let check: (() => Promise<void>) | undefined;
test.afterEach(async ({ page, request }) => {
  test.setTimeout(120_000);
  await page.goto('about:blank');
  try {
    if (check) await check();
  } finally {
    if (seed) await retireWorkspaceSeed(request, seed);
    seed = undefined;
    check = undefined;
  }
});

for (const theme of ['light', 'dark'] as const)
  for (const width of [320, 1440]) {
    test(`Plans empty loading error and neutral create draft ${theme} ${width}`, async ({
      page,
      request
    }, info) => {
      seed = await createWorkspaceSeed(request);
      await freezeWorkspaceSeed(request, seed);
      const base = `/api/workspaces/${seed.workspace.id}`;
      const settings = await (await request.get(`${base}/settings`)).json();
      await preparePage(page, theme, width);
      await workspaceMetadataReads(page);
      check = await guardWorkspaceReadTour(page, [seed]);
      // Presentation only: do not enable structured/automatic planning on the
      // real workspace merely to photograph the creation affordance.
      await page.route(`**${base}/settings`, route => {
        expect(route.request().method()).toBe('GET');
        return route.fulfill({
          json: {
            ...settings,
            settings: {
              ...settings.settings,
              planning: { ...settings.settings?.planning, enabled: true }
            }
          }
        });
      });
      let release!: () => void;
      const gate = new Promise<void>(resolve => {
        release = resolve;
      });
      let fail = false;
      let first = true;
      await page.route(`**${base}/plans?scope=active`, async route => {
        expect(route.request().method()).toBe('GET');
        if (first) {
          first = false;
          await gate;
        }
        if (fail)
          return route.fulfill({ status: 503, json: { message: 'Fictional local read refusal' } });
        return route.continue();
      });
      const index = `/workspaces/${seed.workspace.folder_slug}/plans`;
      try {
        await page.goto(index, { waitUntil: 'domcontentloaded' });
        await expect(page.locator('#plan-status')).toContainText('Loading');
        await capture(
          page,
          info,
          'workspace-plans-loading',
          {
            workspaceId: seed.workspace.id,
            fixture: 'Deferred genuine empty list GET; enabled-policy presentation only'
          },
          { fullPage: false }
        );
      } finally {
        release();
      }
      await expect(page.locator('#plan-active-empty')).toBeVisible();
      await expect(page.locator('#plan-history-section')).toBeHidden();
      await expect(page.locator('#plan-create-panel')).toBeVisible();
      const field = page.locator('#plan-create-request');
      await expect(field).toHaveValue('');
      expect(await field.evaluate(node => node.matches(':user-invalid'))).toBe(false);
      await page.locator('#plan-create-form button[type="submit"]').click();
      await expect(page.locator('#plan-status')).toContainText('Describe what you want');
      expect((await contrast(page.locator('#plan-status'))).ratio).toBeGreaterThanOrEqual(4.5);
      await field.fill('Unsent fictional outline. Do not generate, approve or start work.');
      await page.locator('#plan-refresh').click();
      await expect(page.locator('#plan-status')).toBeHidden();
      await expect(field).toHaveValue(
        'Unsent fictional outline. Do not generate, approve or start work.'
      );
      await capture(
        page,
        info,
        'workspace-plans-empty-create-draft',
        {
          workspaceId: seed.workspace.id,
          fixture:
            'Real empty collection; policy GET projection only; blank submission rejected locally'
        },
        { fullPage: false }
      );
      fail = true;
      await page.locator('#plan-refresh').click();
      await expect(page.locator('#plan-status')).toContainText('Fictional local read refusal');
      expect((await contrast(page.locator('#plan-status'))).ratio).toBeGreaterThanOrEqual(4.5);
      await expect(field).toHaveValue(
        'Unsent fictional outline. Do not generate, approve or start work.'
      );
      await reachable(page.locator('#plan-refresh'), width <= 768 ? 44 : 40);
      await capture(
        page,
        info,
        'workspace-plans-read-error',
        {
          workspaceId: seed.workspace.id,
          fixture: 'Named GET-only 503; no successful mutation mocks'
        },
        { fullPage: false }
      );
      await page.goto(`${index}/plan_deliberately_missing`);
      await expect(page.locator('#plan-missing')).toBeVisible();
      await expect(page.locator('#plan-content')).toBeHidden();
      await expect(page.locator('#plan-loading')).toBeHidden();
      await expect(page.locator('#plan-status-badge')).toHaveText(/Unavailable/);
      await expect(page.locator('#plan-detail-status')).toContainText('not found');
      await check();
    });
  }
