import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import { randomUUID } from 'node:crypto';

// Real-server recovery matrix for the ordinary operation owner. Only reply
// delivery or a layout write is interrupted; no creation, receipt or layout
// success is fabricated. Run against an isolated branch server.
async function workspaceIDs(request: APIRequestContext) {
  const body = await (await request.get('/api/workspaces')).json();
  return body.folders.map((workspace: { id: string }) => workspace.id).sort();
}

async function openBlankReview(page: Page, name: string) {
  await page.goto('/');
  await page.locator('#cockpitCreateWorkspaceBtn').click();
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

test.beforeEach(async ({ request }) => {
  const skipped = await request.post('/api/onboarding/skip');
  expect(skipped.ok(), await skipped.text()).toBeTruthy();
});

test('a second tab cannot compete with a confirmed operation; it reads the original instead', async ({
  context,
  request
}) => {
  const before = await workspaceIDs(request);
  const first = await context.newPage();
  const second = await context.newPage();
  const firstName = `First tab ${randomUUID().slice(0, 8)}`;
  const firstReview = await openBlankReview(first, firstName);
  // The second tab reaches its own review before the first one confirms.
  const secondReview = await openBlankReview(second, `Second tab ${randomUUID().slice(0, 8)}`);

  let confirms = 0;
  await context.route('**/api/workspaces/create-operations/*/confirm', async route => {
    confirms++;
    if (confirms === 1) {
      // The host accepts and completes the first confirmation; its reply is lost.
      await route.fetch();
      await route.abort('failed');
      return;
    }
    await route.continue();
  });
  await firstReview.getByRole('button', { name: 'Use automatic placement', exact: true }).click();
  await firstReview.getByRole('button', { name: 'Create workspace', exact: true }).click();
  await expect(
    firstReview.getByRole('heading', { name: 'Workspace created', exact: true })
  ).toBeVisible();

  await secondReview.getByRole('button', { name: 'Use automatic placement', exact: true }).click();
  await secondReview.getByRole('button', { name: 'Create workspace', exact: true }).click();
  // Nothing was sent for the second draft: the original is read instead.
  await expect(
    secondReview.getByRole('heading', { name: 'Workspace created', exact: true })
  ).toBeVisible();
  await expect(secondReview).toContainText(firstName);
  expect(confirms).toBe(1);
  const after = await workspaceIDs(request);
  expect(after).toHaveLength(before.length + 1);

  // Closing the original result releases it; a new confirmation then works.
  await secondReview.getByRole('button', { name: 'Close', exact: true }).click();
  const thirdName = `After release ${randomUUID().slice(0, 8)}`;
  const third = await openBlankReview(second, thirdName);
  await third.getByRole('button', { name: 'Use automatic placement', exact: true }).click();
  await third.getByRole('button', { name: 'Create workspace', exact: true }).click();
  await expect(
    third.getByRole('heading', { name: 'Workspace created', exact: true })
  ).toBeVisible();
  await expect(third).toContainText(thirdName);
  expect(await workspaceIDs(request)).toHaveLength(before.length + 2);
});

test('a double-clicked final confirmation creates exactly once', async ({ page, request }) => {
  const before = await workspaceIDs(request);
  let confirms = 0;
  await page.route('**/api/workspaces/create-operations/*/confirm', async route => {
    confirms++;
    await route.continue();
  });
  const review = await openBlankReview(page, `Double click ${randomUUID().slice(0, 8)}`);
  await review.getByRole('button', { name: 'Use automatic placement', exact: true }).click();
  await review.getByRole('button', { name: 'Create workspace', exact: true }).dblclick();
  await expect(
    review.getByRole('heading', { name: 'Workspace created', exact: true })
  ).toBeVisible();
  expect(confirms).toBe(1);
  expect(await workspaceIDs(request)).toHaveLength(before.length + 1);
});

test('a failed position save keeps the workspace and repairs only its layout', async ({
  page,
  request
}) => {
  const before = await workspaceIDs(request);
  const review = await openBlankReview(page, `Position repair ${randomUUID().slice(0, 8)}`);
  let refused = 0;
  await page.route('**/api/workspace-map/layout', async route => {
    if (route.request().method() === 'PATCH' && refused === 0) {
      refused++;
      await route.fulfill({ status: 503, json: { error: 'layout unavailable' } });
      return;
    }
    await route.continue();
  });
  await review.getByRole('button', { name: 'Place & create on map', exact: true }).click();
  const preview = page.locator('[data-ws-map-placement-preview]');
  await expect(preview).toBeVisible();
  await page.locator('[data-ws-map-viewport]').focus();
  for (let n = 0; n < 60 && /is-invalid/.test((await preview.getAttribute('class')) || ''); n++)
    await page.keyboard.press('ArrowDown');
  const candidate = await preview.evaluate(element => ({
    x: Number.parseFloat((element as HTMLElement).style.left),
    y: Number.parseFloat((element as HTMLElement).style.top)
  }));
  await page.keyboard.press('Enter');
  await expect(
    review.getByRole('heading', { name: 'Workspace created', exact: true })
  ).toBeVisible();
  const retry = review.getByRole('button', { name: 'Retry position only', exact: true });
  await expect(retry).toBeVisible();
  const created = (await workspaceIDs(request)).filter((id: string) => !before.includes(id));
  expect(created).toHaveLength(1);
  let layout = (await (await request.get('/api/workspace-map/layout')).json()).layout;
  expect(layout.positions[created[0]]).toBeUndefined();

  let confirms = 0;
  await page.route('**/api/workspaces/create-operations/*/confirm', async route => {
    confirms++;
    await route.continue();
  });
  await retry.click();
  await expect(review).toContainText('Only layout changed');
  layout = (await (await request.get('/api/workspace-map/layout')).json()).layout;
  expect(layout.positions[created[0]]).toEqual(candidate);
  expect(confirms).toBe(0);
  expect(await workspaceIDs(request)).toHaveLength(before.length + 1);
});
