import { expect, test, type Locator, type Page } from '@playwright/test';
import { attachPlacementFolder, placementJSON } from './helpers/assisted-workspace-placement';

// Real server, real gestures. Runs against scripts/demo-inbox-folder.sh, whose
// sandbox has a hired assistant and seeded folders but no model provider.
test.skip(!process.env.ORI_INBOX_SANDBOX, 'Set ORI_INBOX_SANDBOX to the inbox demo sandbox.');
test.describe.configure({ mode: 'serial' });
test.use({ viewport: { width: 1440, height: 1000 } });

const UNSENT = 'Keep this unsent draft.';

async function domain(page: Page) {
  return {
    workspaces: (await placementJSON(page, '/api/workspaces')).folders.map((w: any) => w.id).sort(),
    agents: (await placementJSON(page, '/api/agents')).agents.map((a: any) => a.name),
    layout: await placementJSON(page, '/api/workspace-map/layout')
  };
}

async function openExpandedChat(page: Page) {
  await page.goto('/');
  await page.waitForFunction(() => (window as any).PersonalAssistantPanel?._state.view.available);
  await page.evaluate(() => (window as any).PersonalAssistantPanel.close());
  await page.locator('[data-cockpit-view="map"]').click();
  await page.locator('#personalAssistantLauncher').click();
  const expand = page.locator('#personalAssistantExpand');
  if ((await expand.getAttribute('aria-pressed')) !== 'true') await expand.click();
  await page.locator('#personalAssistantInput').fill(UNSENT);
}

async function reachOrdinaryReview(page: Page, name: string) {
  // The expanded drawer legitimately covers this button; keyboard reaches it.
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

// The map's controls and open ground answer real hit tests, not visibility.
async function expectClearMap(page: Page) {
  await expect(page.locator('[data-ws-map-placement-preview]')).toBeVisible();
  await expect(page.locator('#personalAssistantPanel')).toBeHidden();
  await expect(page.locator('#addFolderModal')).toBeHidden();
  await expect(page.locator('.modal-backdrop')).toHaveCount(0);
  const hit = await page.evaluate(() => {
    const controls = document.querySelector('[data-ws-map-placement-controls]')!;
    const box = controls.getBoundingClientRect();
    const onControls = document.elementFromPoint(
      box.left + box.width / 2,
      box.top + box.height / 2
    );
    const viewport = document.querySelector('[data-ws-map-viewport]')!;
    const v = viewport.getBoundingClientRect();
    const onGround = document.elementFromPoint(v.right - 40, v.bottom - 40);
    return { controls: controls.contains(onControls), ground: viewport.contains(onGround) };
  });
  expect(hit).toEqual({ controls: true, ground: true });
}

async function placeButton(review: Locator) {
  return review.getByRole('button', { name: 'Place & create on map', exact: true });
}

test('ordinary creation clears an open expanded chat and Escape restores both', async ({
  page
}) => {
  await openExpandedChat(page);
  const review = await reachOrdinaryReview(page, 'Transition ordinary (not created)');
  await expect(page.locator('#personalAssistantPanel')).toBeVisible();
  const before = await domain(page);

  await (await placeButton(review)).click();
  await expectClearMap(page);
  // Stepping aside is not a close: the tab still remembers the drawer as open.
  expect(await page.evaluate(() => sessionStorage.getItem('ori.personalAssistant.panelOpen'))).toBe(
    '1'
  );

  await page.locator('[data-ws-map-viewport]').filter({ visible: true }).first().focus();
  await page.keyboard.press('Escape');
  await expect(review).toBeVisible();
  await expect(page.locator('#personalAssistantPanel')).toBeVisible();
  await expect(page.locator('#personalAssistantInput')).toHaveValue(UNSENT);
  await expect(page.locator('#personalAssistantExpand')).toHaveAttribute('aria-pressed', 'true');
  await expect(page.locator('#folderNameInput')).toHaveValue('Transition ordinary (not created)');

  // Immediate re-entry works, and Back left nothing behind.
  await (await placeButton(review)).click();
  await expectClearMap(page);
  await page.locator('[data-ws-map-placement-back]').click();
  await expect(review).toBeVisible();
  expect(await domain(page)).toEqual(before);
});

test('reopening chat during placement leaves placement first, never stacked', async ({ page }) => {
  await openExpandedChat(page);
  const review = await reachOrdinaryReview(page, 'Transition reopen (not created)');
  const before = await domain(page);
  await (await placeButton(review)).click();
  await expectClearMap(page);

  await page.evaluate(() =>
    (window as any).PersonalAssistantPanel.open(
      document.getElementById('personalAssistantLauncher')
    )
  );
  await expect(page.locator('[data-ws-map-placement-preview]')).toHaveCount(0);
  await expect(review).toBeVisible();
  await expect(page.locator('#personalAssistantInput')).toHaveValue(UNSENT);
  expect(await domain(page)).toEqual(before);
});

test('a background reveal cannot reopen chat over placement', async ({ page }) => {
  await openExpandedChat(page);
  const review = await reachOrdinaryReview(page, 'Transition reveal (not created)');
  await (await placeButton(review)).click();
  await expectClearMap(page);
  const revealed = await page.evaluate(() =>
    (window as any).PersonalAssistantPanel.openActivity(null)
  );
  expect(revealed).toBe(false);
  await expectClearMap(page);
  await page.locator('[data-ws-map-placement-back]').click();
  await expect(review).toBeVisible();
});

test('holding Enter from the Place button cannot create', async ({ page }) => {
  await openExpandedChat(page);
  const review = await reachOrdinaryReview(page, 'Transition enter (not created)');
  const before = await domain(page);
  await (await placeButton(review)).click();
  await expectClearMap(page);
  await page.locator('[data-ws-map-viewport]').filter({ visible: true }).first().focus();
  await page.evaluate(() =>
    document.querySelector('[data-ws-map-viewport]:not([hidden])')?.dispatchEvent(
      new KeyboardEvent('keydown', {
        key: 'Enter',
        repeat: true,
        bubbles: true,
        cancelable: true
      })
    )
  );
  await expectClearMap(page);
  expect(await domain(page)).toEqual(before);
  await page.locator('[data-ws-map-placement-back]').click();
  await expect(review).toBeVisible();
});

test('a placed workspace reports on the Map, one accepted gesture creating one workspace', async ({
  page
}) => {
  await openExpandedChat(page);
  const name = `Transition placed ${Date.now()}`;
  const review = await reachOrdinaryReview(page, name);
  const before = await domain(page);

  await (await placeButton(review)).click();
  await expectClearMap(page);
  await page.locator('[data-ws-map-viewport]').filter({ visible: true }).first().focus();
  await page.keyboard.press('Enter');

  const status = page.locator('[data-creation-map-status]');
  await expect(status).toBeVisible({ timeout: 30000 });
  await expect(status).toContainText(name);
  // The Map stays unobstructed: nothing comes back on its own.
  await expect(page.locator('#addFolderModal')).toBeHidden();
  await expect(page.locator('#personalAssistantPanel')).toBeHidden();
  await expect(page.locator('.modal-backdrop')).toHaveCount(0);

  const after = await domain(page);
  const created = after.workspaces.filter((id: string) => !before.workspaces.includes(id));
  expect(created).toHaveLength(1);
  expect(after.layout.layout.positions[created[0]]).toBeTruthy();

  // Details is the existing full receipt; closing it leaves the workspace alone.
  await status.getByRole('button', { name: 'Details', exact: true }).click();
  await expect(page.locator('#workspaceCreationReviewPanel')).toBeVisible();
  await expect(status).toBeHidden();
  await page.locator('#addFolderModal .btn-close').first().click();
  await expect(page.locator('#addFolderModal')).toBeHidden();
  expect((await domain(page)).workspaces).toEqual(after.workspaces);

  // The explicit launcher still brings the very same conversation back.
  await page.locator('#personalAssistantLauncher').click();
  await expect(page.locator('#personalAssistantPanel')).toBeVisible();
  await expect(page.locator('#personalAssistantInput')).toHaveValue(UNSENT);
});

async function placeAndConfirm(page: Page, name: string) {
  const review = await reachOrdinaryReview(page, name);
  await (await placeButton(review)).click();
  await expectClearMap(page);
  await page.locator('[data-ws-map-viewport]').filter({ visible: true }).first().focus();
  await page.keyboard.press('Enter');
}

test('a slow create stays locked on the Map; repeats, Escape and reopening chat change nothing', async ({
  page
}) => {
  await openExpandedChat(page);
  const name = `Transition slow ${Date.now()}`;
  const before = await domain(page);
  // The real response, only later: nothing about the operation is fabricated.
  let release = () => {};
  const held = new Promise<void>(resolve => (release = resolve));
  await page.route('**/api/workspaces/create-operations/*/confirm', async route => {
    await held;
    await route.continue();
  });
  await placeAndConfirm(page, name);
  const confirm = page.locator('[data-ws-map-placement-confirm]');
  await expect(confirm).toBeDisabled();
  await page.keyboard.press('Enter');
  await page.keyboard.press('Escape');
  await expect(page.locator('[data-ws-map-placement-preview]')).toBeVisible();
  await expect(page.locator('#addFolderModal')).toBeHidden();

  // Asking for chat while it runs shows chat; it is not sent back to its review.
  await page.evaluate(() =>
    (window as any).PersonalAssistantPanel.open(
      document.getElementById('personalAssistantLauncher')
    )
  );
  await expect(page.locator('#personalAssistantPanel')).toBeVisible();
  await expect(page.locator('#personalAssistantInput')).toHaveValue(UNSENT);
  await expect(page.locator('#addFolderModal')).toBeHidden();

  release();
  const status = page.locator('[data-creation-map-status]');
  await expect(status).toContainText(name, { timeout: 30000 });
  const after = await domain(page);
  expect(after.workspaces.filter((id: string) => !before.workspaces.includes(id))).toHaveLength(1);
  await page.unroute('**/api/workspaces/create-operations/*/confirm');
});

test('a failed layout write reports on the Map and Retry repeats only the position', async ({
  page
}) => {
  await openExpandedChat(page);
  const name = `Transition layout ${Date.now()}`;
  const before = await domain(page);
  let fail = true;
  await page.route('**/api/workspace-map/layout', route =>
    route.request().method() === 'PATCH' && fail
      ? route.fulfill({
          status: 503,
          contentType: 'application/json',
          body: '{"error":"Layout outage"}'
        })
      : route.continue()
  );
  await placeAndConfirm(page, name);
  const status = page.locator('[data-creation-map-status]');
  await expect(status).toBeVisible({ timeout: 30000 });
  await expect(status).toHaveAttribute('data-tone', 'warning');
  await expect(status).toContainText('position was not saved');
  await expect(page.locator('#addFolderModal')).toBeHidden();
  const created = (await domain(page)).workspaces.filter(
    (id: string) => !before.workspaces.includes(id)
  );
  expect(created).toHaveLength(1);

  fail = false;
  await status.getByRole('button', { name: 'Retry position', exact: true }).click();
  await expect(status).toHaveAttribute('data-tone', 'success');
  await expect(status.getByRole('button', { name: 'Retry position', exact: true })).toHaveCount(0);
  const after = await domain(page);
  expect(after.layout.layout.positions[created[0]]).toBeTruthy();
  expect(after.workspaces).toEqual([...before.workspaces, created[0]].sort());
  expect(after.agents).toEqual(before.agents);
  await page.unroute('**/api/workspace-map/layout');
});

test('leaving the Map during placement returns the review and chat with nothing created', async ({
  page
}) => {
  await openExpandedChat(page);
  const review = await reachOrdinaryReview(page, 'Transition view change (not created)');
  const before = await domain(page);
  await (await placeButton(review)).click();
  await expectClearMap(page);
  await page.locator('[data-cockpit-view="tree"]').click();
  await expect(review).toBeVisible();
  await expect(page.locator('#personalAssistantPanel')).toBeVisible();
  await expect(page.locator('#personalAssistantInput')).toHaveValue(UNSENT);
  await expect(page.locator('[data-ws-map-placement-preview]')).toHaveCount(0);
  expect(await domain(page)).toEqual(before);
});

test('a receipt survives a resize and Dismiss leaves an ordinary closed drawer', async ({
  page
}) => {
  await openExpandedChat(page);
  const name = `Transition resize ${Date.now()}`;
  await placeAndConfirm(page, name);
  const status = page.locator('[data-creation-map-status]');
  await expect(status).toBeVisible({ timeout: 30000 });
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(status).toBeVisible();
  const box = await status.boundingBox();
  expect(box!.x).toBeGreaterThanOrEqual(0);
  expect(box!.x + box!.width).toBeLessThanOrEqual(390);
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);
  await page.setViewportSize({ width: 1440, height: 1000 });

  await status.getByRole('button', { name: 'Dismiss', exact: true }).click();
  await expect(status).toBeHidden();
  await expect(page.locator('#personalAssistantPanel')).toBeHidden();
  // Nothing is blocking background reveals any more, and the tab keeps its choice.
  expect(await page.evaluate(() => (window as any).OriCreationTransition.blocksReveal())).toBe(
    false
  );
  await page.locator('#personalAssistantLauncher').click();
  await expect(page.locator('#personalAssistantInput')).toHaveValue(UNSENT);
});

test('the inbox review keeps chat behind it until the user chooses the Map', async ({ page }) => {
  await attachPlacementFolder(page, 'Downloads', true);
  await page.locator('#personalAssistantInput').fill(UNSENT);
  await page.getByRole('button', { name: 'Review setup', exact: true }).click();
  const review = page.locator('#assistedWorkspacePlacementPanel');
  await expect(review).toBeVisible();
  await expect(page.locator('#personalAssistantPanel')).toBeVisible();
  const before = await domain(page);

  await review.getByRole('button', { name: 'Place & create on map', exact: true }).click();
  await expect(page.locator('[data-ws-map-placement-preview]')).toBeVisible();
  await expect(page.locator('#personalAssistantPanel')).toBeHidden();
  await expect(page.locator('.modal-backdrop')).toHaveCount(0);

  await page.locator('[data-ws-map-viewport]').filter({ visible: true }).first().focus();
  await page.keyboard.press('Escape');
  await expect(review).toBeVisible();
  await expect(page.locator('#personalAssistantPanel')).toBeVisible();
  await expect(page.locator('#personalAssistantInput')).toHaveValue(UNSENT);
  expect(await domain(page)).toEqual(before);
});
