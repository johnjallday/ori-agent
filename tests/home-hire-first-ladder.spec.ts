import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import { installLocalCdn } from './helpers/offline-cdn';

// Home's one primary action follows the assistant relationship, end to end in a
// real browser against the real server and store (home-hire-first-actions
// FR26): "Meet your assistant" before the hire, "Build My HQ" after it, and
// "Hire an agent" once the HQ exists. New Workspace stays beside it as the
// secondary and opens the Create Workspace modal in every state.
//
// A hire is durable and cannot be undone, so the ladder needs a server of its
// own that has never hired:
//
//   ./scripts/e2e-fresh.sh tests/home-hire-first-ladder.spec.ts
//
// No model is configured and none is needed: every step is the server's own.
test.describe.configure({ mode: 'serial' });
test.beforeEach(async ({ page }) => installLocalCdn(page));

async function ok(response: Awaited<ReturnType<APIRequestContext['get']>>) {
  const text = await response.text();
  expect(response.ok(), `${response.url()}: ${text}`).toBeTruthy();
  return text ? JSON.parse(text) : {};
}

async function relationship(request: APIRequestContext) {
  return (await ok(await request.get('/api/personal-assistant'))).personal_assistant;
}

// expectPrimary loads Home and checks the creation group in one ladder state:
// the primary first with its label, New Workspace second as the secondary, and
// no create affordance on the Map to tell a different story.
async function expectPrimary(page: Page, label: string) {
  await page.goto('/');
  const primary = page.locator('#cockpitPrimaryActionBtn');
  await expect(primary).toBeVisible();
  await expect(primary).toHaveText(label);
  await expect(primary).toHaveClass(/modern-btn-primary/);
  const create = page.locator('#cockpitCreateWorkspaceBtn');
  await expect(create).toBeVisible();
  await expect(create).toHaveClass(/modern-btn-secondary/);
  const order = await page
    .locator('.cockpit-area-actions__create > button')
    .evaluateAll(buttons =>
      buttons.filter(button => !(button as HTMLElement).hidden).map(button => button.id)
    );
  expect(order).toEqual(['cockpitPrimaryActionBtn', 'cockpitCreateWorkspaceBtn']);
  await expect(page.locator('#cockpitMap')).toBeVisible();
  await expect(
    page.locator('#cockpitMap [data-ws-map-create], #cockpitMap .ws-map-pad')
  ).toHaveCount(0);
  return primary;
}

// New Workspace opens the same Create Workspace modal in every state.
async function expectNewWorkspaceOpens(page: Page) {
  await page.locator('#cockpitCreateWorkspaceBtn').click();
  const modal = page.locator('#addFolderModal');
  await expect(modal).toBeVisible();
  await expect(modal).toHaveAttribute('data-import-mode', 'false');
  expect(await page.evaluate(() => (window as any).sessionManager?.importEntryPoint)).toBe(
    'home_cockpit_create'
  );
  await modal.locator('[data-bs-dismiss="modal"]').first().click();
  await expect(modal).toBeHidden();
}

// The drawer's card and chooser open through the deep link's own guard, which
// waits for their controllers; a press before they mount does nothing.
async function waitForDrawerControllers(page: Page) {
  await page.waitForFunction(() =>
    Boolean(
      (window as any).PersonalAssistantHQCard?.show &&
      (window as any).PersonalAssistantFolder?.openChooser
    )
  );
}

test.describe('Home leads with the next hire-first step', () => {
  test.beforeAll(async ({ request }) => {
    await request.post('/api/onboarding/skip');
    await ok(await request.post('/api/settings/workspace-root', { data: { workspace_root: '' } }));
    const initial = await relationship(request);
    test.skip(
      initial.state !== 'needs_hire',
      `needs a server that has never hired (found "${initial.state}"); run it with ./scripts/e2e-fresh.sh`
    );
  });

  test('before the hire: "Meet your assistant" starts Mission 01', async ({ page }) => {
    const primary = await expectPrimary(page, 'Meet your assistant');
    await expectNewWorkspaceOpens(page);
    await primary.click();
    await page.waitForURL(url => url.pathname === '/' && url.search === '?quest=meet-assistant');
    const callout = page.locator('#oriSpotlight .ori-spotlight__callout');
    await expect(callout.locator('.ori-spotlight__callout-title')).toHaveText('Click Agents');
  });

  test('after the hire: "Build My HQ" opens the HQ card', async ({ page, request }) => {
    const initial = await relationship(request);
    await ok(
      await request.post('/api/personal-assistant/hire', {
        data: {
          request_id: 'hire-first-ladder-hire',
          if_version: initial.state_version ?? 0,
          display_name: 'Atlas',
          mandate: 'Keep my projects moving.',
          focus_areas: ['plan_my_day']
        }
      })
    );
    expect((await relationship(request)).state).toBe('needs_hq');

    const primary = await expectPrimary(page, 'Build My HQ');
    await expectNewWorkspaceOpens(page);
    await waitForDrawerControllers(page);
    await primary.click();
    await expect(page.locator('#personalAssistantPanel')).toBeVisible();
    await expect(page.locator('#personalAssistantHQCard')).toBeVisible();
  });

  test('after the HQ: "Hire an agent" opens the folder chooser', async ({ page, request }) => {
    const hired = await relationship(request);
    await ok(
      await request.post('/api/personal-assistant/hq', {
        data: {
          request_id: 'hire-first-ladder-hq',
          if_version: hired.state_version,
          name: 'My HQ',
          timezone: 'UTC'
        }
      })
    );
    expect((await relationship(request)).state).toBe('active');

    const primary = await expectPrimary(page, 'Hire an agent');
    await expectNewWorkspaceOpens(page);
    await waitForDrawerControllers(page);
    await primary.click();
    await expect(page.locator('#personalAssistantPanel')).toBeVisible();
    await expect(page.locator('#personalAssistantContextChooser')).toBeVisible();
    await expect(
      page.locator('#personalAssistantFolderChoices').getByRole('button').first()
    ).toBeVisible();
  });

  test('at phone width both header buttons fit without horizontal scroll', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await expectPrimary(page, 'Hire an agent');
    const overflow = await page.evaluate(
      () => document.documentElement.scrollWidth - window.innerWidth
    );
    expect(overflow).toBeLessThanOrEqual(1);
  });
});
