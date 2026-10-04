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
  // The link opens the drawer and starts the folder flow as a turn in the
  // conversation: the request, then the chooser as the assistant's reply.
  await page.goto('/?panel=today&folder=show');
  const scene = page.locator('#personalAssistantFolderScene');
  const turn = page.locator('#personalAssistantThread #personalAssistantFolder');
  const chooser = page.locator('#personalAssistantFolderChooser');
  const chip = page.locator('#personalAssistantFolderChip');
  await expect(turn.locator('#personalAssistantFolderChooser')).toBeVisible();
  await expect(page.locator('#personalAssistantFolderRequest')).toContainText('Explore a folder');
  await expect(chooser).toContainText('Which folder should I explore?');
  await expect(chooser).toContainText('A read-only peek. Nothing moves.');
  // The scene is the reply after a folder is chosen, not before.
  await expect(scene).toBeHidden();
  // One folder at a time: the chip waits while this one is being chosen.
  await expect(chip).toBeVisible();
  await expect(chip).toBeDisabled();
  await expect(page.locator('#personalAssistantHQCard')).toBeHidden();
  await expect(page.locator('#personalAssistantNeedsYouQueue')).toBeHidden();
  await expect(page.locator('#personalAssistantTodayTitle')).toHaveText('Today from Atlas');
  await expect(page.locator('#personalAssistantCheckIn')).toContainText('Next check-in ·');
  // The user started something, so the top of the drawer is one line.
  const summary = page.locator('#personalAssistantSummary');
  const summaryToggle = page.locator('#personalAssistantSummaryToggle');
  await expect(summary).toBeVisible();
  await expect(summaryToggle).toHaveAttribute('aria-expanded', 'false');
  await expect(page.locator('#personalAssistantTodaySections')).toBeHidden();
  const more = page.locator('#personalAssistantMore');
  const moreToggle = more.locator('summary');
  await expect(moreToggle).toBeVisible();
  await expect(page.locator('#personalAssistantTodayHQ')).toBeHidden();
  await moreToggle.click();
  await expect(page.locator('#personalAssistantTodayHQ')).toBeVisible();
  await expect(page.locator('#personalAssistantTodayMemory')).toHaveAttribute('href', /#memory$/);
  await expect(more.getByRole('link', { name: 'Review remembered facts' })).toHaveAttribute(
    'href',
    '/profile#personalHQKnowledge'
  );
  await expect(more.getByRole('link', { name: 'Manage agents' })).toHaveAttribute(
    'href',
    '/agents'
  );
  await moreToggle.press('Escape');
  await expect(more).not.toHaveAttribute('open', '');
  await expect(moreToggle).toBeFocused();
  await expect(page.locator('#personalAssistantPanel')).toBeVisible();
  await moreToggle.press('Enter');
  await expect(page.locator('#personalAssistantTodayHQ')).toBeVisible();
  await page.locator('#personalAssistantPanelTitle').click();
  await expect(more).not.toHaveAttribute('open', '');

  // Show brings back what the strip stands for, and Hide folds it again. The
  // HQ receipt is in Done, behind the progress row.
  await summaryToggle.click();
  await expect(summaryToggle).toHaveAttribute('aria-expanded', 'true');
  await expect(summaryToggle).toHaveText('Hide');
  await expect(page.locator('#personalAssistantTodaySections')).toBeVisible();
  await page.locator('#personalAssistantProgressRow').click();
  const hqReceipt = page.locator(
    '#personalAssistantDoneItems .personal-assistant-today__hq-receipt'
  );
  await expect(hqReceipt.locator('summary')).toBeVisible();
  await hqReceipt.locator('summary').click();
  await expect(hqReceipt.locator('li').first()).toContainText('Workspace');
  await expect(hqReceipt).toContainText('Daily Brief at');
  await summaryToggle.click();
  await expect(summaryToggle).toHaveText('Show');
  await expect(page.locator('#personalAssistantTodaySections')).toBeHidden();
  // The conversation comes after what needs the user, and the chip and the
  // composer after the conversation.
  const order = await page.evaluate(() => {
    const top = (id: string) => document.getElementById(id)!.getBoundingClientRect().top;
    return [
      top('personalAssistantSummary'),
      top('personalAssistantFolder'),
      top('personalAssistantFolderChip'),
      top('personalAssistantInput')
    ];
  });
  expect(order).toEqual([...order].sort((a, b) => a - b));

  await expect(page.locator('#personalAssistantFolderShowBtn')).toHaveCount(0);
  await expect(chooser).toBeVisible();
  await page.locator('#personalAssistantFolderChips button[data-chip="documents"]').click();
  await expect(scene).toBeVisible();
  await expect(scene).toHaveAttribute('data-phase', 'scanning');
  await expect(scene.locator('#personalAssistantFolderSceneAvatar .agent-avatar')).toBeVisible();
  await expect(scene.locator('.pa-folder__scene-art')).toHaveAttribute('aria-hidden', 'true');
  await expect(scene.locator('#personalAssistantFolderSceneLabel')).toHaveAttribute(
    'role',
    'status'
  );
  await expect(scene.locator('#personalAssistantFolderSceneLabel')).toHaveText(
    'Exploring Documents…'
  );
  expect(requestedChip).toBe('documents'); // A chip ID, never a filesystem path.
  await expect(chip).toBeDisabled(); // still exploring
  const motion = await scene
    .locator('.pa-folder__scene-folder')
    .evaluate(el => getComputedStyle(el).animationName);
  expect(motion).toContain('pa-folder-nibble');
  finishScan?.();
  await expect(scene).toHaveAttribute('data-phase', 'found');
  // The folder is chosen: the scene and the offer are the replies now, and
  // another folder can be asked for.
  await expect(chooser).toBeHidden();
  await expect(chip).toBeEnabled();
  await expect(scene.locator('#personalAssistantFolderSceneFinds')).toHaveText(
    '3 projects40 loose files'
  );
  await expect(turn.locator('#personalAssistantFolderOffer')).toBeVisible();
  await expect(
    page.locator('#personalAssistantNeedsYouCards #personalAssistantFolderOffer')
  ).toHaveCount(0);
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
  // Asking again reopens the chooser in the same turn.
  failNext = true;
  await chip.click();
  await expect(page.locator('#personalAssistantFolderTitle')).toHaveText(
    'Or explore another folder'
  );
  await page.locator('#personalAssistantFolderChips button[data-chip="documents"]').click();
  await expect(scene).toHaveAttribute('data-phase', 'error');
  await expect(scene.locator('#personalAssistantFolderSceneFinds')).toBeEmpty();
  await expect(page.locator('#personalAssistantFolderStatus')).toHaveText(
    'Temporarily unavailable'
  );
  await page.setViewportSize({ width: 390, height: 800 });
  await expect(scene).toBeVisible();
  await moreToggle.click();
  await expect(more.getByRole('link', { name: 'Workspace memory' })).toBeVisible();
  const widths = await page.evaluate(() => [
    document.documentElement.scrollWidth,
    window.innerWidth
  ]);
  expect(widths[0]).toBeLessThanOrEqual(widths[1] + 1);
  await page.route('**/api/personal-assistant/today', async route => {
    const response = await route.fetch();
    const payload = await response.json();
    payload.today.needs_you.items.push({
      kind: 'decision',
      title: 'Review a proposed change',
      detail: 'One decision is waiting for you.'
    });
    payload.today.interview_status = 'offered';
    await route.fulfill({ response, json: payload });
  });
  // A fresh page with nothing waiting: no folder turn, nothing folded, and the
  // chip ready.
  await page.reload();
  await page.locator('#personalAssistantLauncher').click();
  await expect(page.locator('#personalAssistantNeedsYou')).toBeVisible();
  await expect(page.locator('#personalAssistantFolder')).toBeHidden();
  await expect(summary).toBeHidden();
  await expect(chip).toBeEnabled();
  await moreToggle.click();
  await expect(page.locator('#personalAssistantTodayInterview')).toHaveAttribute(
    'href',
    '/profile#personalHQInterview'
  );
  await expect(page.locator('#personalAssistantTodayInterview')).toBeVisible();
  await moreToggle.click();
  const queue = page.locator('#personalAssistantNeedsYouQueue');
  await expect(page.locator('#personalAssistantNeedsYouQueueTitle')).toBeVisible();
  await expect(queue.locator('#personalAssistantNeedsYouItems')).toBeHidden();
  await page.locator('#personalAssistantNeedsYouQueueTitle').click();
  await expect(queue.locator('#personalAssistantNeedsYouItems')).toContainText(
    'Review a proposed change'
  );
  await expect(page.locator('#personalAssistantNeedsYouCount')).toHaveText('1');
  await page.locator('#personalAssistantProgressRow').click();
  await expect(hqReceipt.locator('summary')).toBeVisible();
  await expect(page.locator('#personalAssistantFolderShowBtn')).toHaveCount(0);
  await moreToggle.click();
  await more.getByRole('link', { name: 'Review remembered facts' }).click();
  await expect(page).toHaveURL(/\/profile#personalHQKnowledge$/);
});
