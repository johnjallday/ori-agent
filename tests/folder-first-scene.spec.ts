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
  await page.route('**/api/home-assistant/folder-context/choices', route =>
    route.fulfill({
      json: {
        chips: [{ id: 'documents', label: 'Documents' }],
        picker_available: false
      }
    })
  );
  const mutations: string[] = [];
  page.on('request', request => {
    const path = new URL(request.url()).pathname;
    // Today retains its existing read receipt; it is not a folder/setup action.
    if (request.method() === 'POST' && path !== '/api/personal-hq/brief/open') mutations.push(path);
  });
  let finishScan: (() => void) | undefined;
  let requestedChip = '';
  let failNext = false;
  await page.route('**/api/home-assistant/folder-context/select', async route => {
    requestedChip = route.request().postDataJSON().chip;
    if (failNext) {
      await route.fulfill({
        status: 503,
        contentType: 'application/json',
        body: '{"error":"unavailable","message":"Temporarily unavailable"}'
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
        observation: {
          version: 1,
          id: 'scene-selection',
          folder: 'Documents',
          scanned_at: '2026-10-05T10:00:00Z',
          files: 43,
          entries: 46,
          kinds: [
            { name: '.txt', count: 40 },
            { name: '.md', count: 3 }
          ],
          projects: ['Thesis', 'website', 'Album'].map((name, i) => ({
            id: `candidate-${i}`,
            name,
            files: 1
          })),
          coverage: { max_depth: 3, max_entries: 5000, budget_seconds: 3, partial: false }
        }
      })
    });
  });
  // Browser selection fixture: the link opens only local controls, not a
  // fabricated user/model exchange or a shared setup offer.
  await page.goto('/?panel=today&folder=show');
  const scene = page.locator('#personalAssistantFolderScene');
  const turn = page.locator('.personal-assistant-panel__scroll');
  const chooser = page.locator('#personalAssistantContextChooser');
  const chip = page.locator('#personalAssistantFolderChip');
  await expect(turn.locator('#personalAssistantContextChooser')).toBeVisible();
  await expect(page.locator('#personalAssistantFolderRequest')).toBeHidden();
  await expect(page.locator('#homeAssistantConversation [data-message-role]')).toHaveCount(0);
  await expect(chooser).toContainText('Add folder context');
  await expect(chooser).toContainText('Nothing goes to your configured model until Send');
  // The scene is the reply after a folder is chosen, not before.
  await expect(scene).toBeHidden();
  // Opening choices is not a scan, model turn, or setup decision.
  await expect(chip).toBeVisible();
  await expect(chip).toBeEnabled();
  expect(mutations).toEqual([]);
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
      top('personalAssistantContextChooser'),
      top('personalAssistantFolderChip'),
      top('personalAssistantInput')
    ];
  });
  expect(order).toEqual([...order].sort((a, b) => a - b));

  await expect(page.locator('#personalAssistantFolderShowBtn')).toHaveCount(0);
  await expect(chooser).toBeVisible();
  await page
    .locator('#personalAssistantFolderChoices')
    .getByRole('button', { name: 'Documents', exact: true })
    .click();
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
  await expect(scene).toBeHidden();
  await expect(chooser).toBeHidden();
  await expect(chip).toBeEnabled();
  const preview = page.locator('#personalAssistantFolderPreview');
  await expect(preview).toBeVisible();
  await expect(preview).toContainText('43 files observed');
  await expect(preview).toContainText('File contents have not been read');
  await preview.locator('summary').click();
  await expect(preview).toContainText('Thesis');
  await expect(preview).toContainText('website');
  await expect(preview).toContainText('Album');
  await expect(page.locator('#personalAssistantFolderOffer')).toBeHidden();
  expect(mutations).toEqual(['/api/home-assistant/folder-context/select']);

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
  await expect(chooser).toBeVisible();
  await page
    .locator('#personalAssistantFolderChoices')
    .getByRole('button', { name: 'Documents', exact: true })
    .click();
  await expect(scene).toBeHidden();
  await expect(preview).toContainText('43 files observed');
  await expect(page.locator('#personalAssistantContextStatus')).toHaveText(
    'Temporarily unavailable'
  );
  await page.setViewportSize({ width: 390, height: 800 });
  await expect(preview).toBeVisible();
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
  // No unsent folder survives a reload. Unrelated attention starts compact,
  // but its original decision and HQ receipt remain available behind Show.
  await page.reload();
  await page.locator('#personalAssistantLauncher').click();
  await expect(page.locator('#personalAssistantNeedsYou')).toBeHidden();
  await expect(page.locator('#personalAssistantFolder')).toBeHidden();
  await expect(summary).toBeVisible();
  await expect(summaryToggle).toHaveAttribute('aria-expanded', 'false');
  await summaryToggle.click();
  await expect(page.locator('#personalAssistantNeedsYou')).toBeVisible();
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
