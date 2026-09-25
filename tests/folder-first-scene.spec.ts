import { test, expect } from '@playwright/test';

test('the assistant portrait explores only while a scan is in flight, then shows observed findings', async ({
  page,
  request
}) => {
  await request.post('/api/onboarding/skip');
  const relationship = (await (await request.get('/api/personal-assistant')).json())
    .personal_assistant;
  expect(relationship.state).toBe('needs_hire');
  const hire = await request.post('/api/personal-assistant/hire', {
    data: {
      request_id: 'scene-hire',
      if_version: relationship.state_version ?? 0,
      display_name: 'Atlas',
      mandate: 'Keep my projects moving.',
      focus_areas: ['plan_my_day']
    }
  });
  expect(hire.ok(), await hire.text()).toBeTruthy();
  const hired = (await (await request.get('/api/personal-assistant')).json()).personal_assistant;
  const hq = await request.post('/api/personal-assistant/hq', {
    data: { request_id: 'scene-hq', if_version: hired.state_version, name: 'My HQ' }
  });
  expect(hq.ok(), await hq.text()).toBeTruthy();

  await page.route('**/api/personal-assistant/folder-digest', route =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        folder_digest: {
          chips: [{ id: 'documents', label: 'Documents' }],
          picker_available: false,
          prompt_first_folder: false,
          offer: null
        }
      })
    })
  );
  let finishScan: (() => void) | undefined;
  let requestedChip = '';
  let failNext = false;
  await page.route('**/api/personal-assistant/folder-digest/scan', async route => {
    requestedChip = route.request().postDataJSON().chip;
    if (failNext) {
      await route.fulfill({
        status: 503,
        contentType: 'application/json',
        body: '{"error":"Temporarily unavailable"}'
      });
      return;
    }
    await new Promise<void>(resolve => {
      finishScan = resolve;
    });
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        offer: {
          id: 'scene-offer',
          status: 'pending',
          verdict: 'mixed',
          folder: 'Documents',
          subject: { name: 'Thesis' },
          projects_count: 3,
          loose_files: 40,
          reason: '3 projects and 40 loose files',
          create_available: true
        }
      })
    });
  });
  await page.goto('/?panel=today&folder=show');
  const scene = page.locator('#personalAssistantFolderScene');
  await expect(scene).toBeVisible();
  await expect(scene).toHaveAttribute('data-phase', 'choosing');
  await expect(scene.locator('#personalAssistantFolderSceneAvatar .agent-avatar')).toBeVisible();
  await expect(scene.locator('.pa-folder__scene-art')).toHaveAttribute('aria-hidden', 'true');
  await expect(scene.locator('#personalAssistantFolderSceneLabel')).toHaveAttribute(
    'role',
    'status'
  );
  await page.locator('#personalAssistantFolderChips button[data-chip="documents"]').click();
  await expect(scene).toHaveAttribute('data-phase', 'scanning');
  await expect(scene.locator('#personalAssistantFolderSceneLabel')).toHaveText(
    'Exploring Documents…'
  );
  expect(requestedChip).toBe('documents'); // A chip ID, never a filesystem path.
  const motion = await scene
    .locator('.pa-folder__scene-folder')
    .evaluate(el => getComputedStyle(el).animationName);
  expect(motion).toContain('pa-folder-nibble');
  finishScan?.();
  await expect(scene).toHaveAttribute('data-phase', 'found');
  await expect(page.locator('#personalAssistantTodayAllClear')).toBeHidden();
  await expect(scene.locator('#personalAssistantFolderSceneFinds')).toHaveText(
    '3 projects40 loose files'
  );
  await expect(page.locator('#personalAssistantFolderOffer')).toBeVisible();
  await expect(page.locator('#personalAssistantFolderOffer .pa-folder__why')).toBeVisible();
  await expect(page.locator('#personalAssistantFolderOfferReason')).toBeHidden();
  await page.locator('#personalAssistantFolderOffer .pa-folder__why summary').click();
  await expect(page.locator('#personalAssistantFolderOfferReason')).toBeVisible();

  await page.emulateMedia({ reducedMotion: 'reduce' });
  const reduced = await scene
    .locator('.pa-folder__scene-spark')
    .evaluate(el => getComputedStyle(el).animationName);
  expect(reduced).toBe('none');
  const transition = await scene
    .locator('.pa-folder__scene-folder')
    .evaluate(el => getComputedStyle(el).transitionProperty);
  expect(transition).toBe('none');
  failNext = true;
  await page.locator('#personalAssistantFolderShowBtn').click();
  await page.locator('#personalAssistantFolderChips button[data-chip="documents"]').click();
  await expect(scene).toHaveAttribute('data-phase', 'error');
  await expect(scene.locator('#personalAssistantFolderSceneFinds')).toBeEmpty();
  await expect(page.locator('#personalAssistantFolderStatus')).toHaveText(
    'Temporarily unavailable'
  );
  await page.setViewportSize({ width: 390, height: 800 });
  await expect(scene).toBeVisible();
  const widths = await page.evaluate(() => [
    document.documentElement.scrollWidth,
    window.innerWidth
  ]);
  expect(widths[0]).toBeLessThanOrEqual(widths[1] + 1);
});
