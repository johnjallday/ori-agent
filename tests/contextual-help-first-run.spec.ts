import { test, expect } from '@playwright/test';
import { installLocalCdn } from './helpers/offline-cdn';

test('real pre-hire, no-model Help and meet-assistant deferral remain independent', async ({
  page
}, testInfo) => {
  await installLocalCdn(page);
  // Only the isolated wt-demo sandbox is used. No hire, model, or user reset.
  await page.request.post('/api/onboarding/skip');
  const before = (await (await page.request.get('/api/personal-assistant')).json())
    .personal_assistant;
  expect(before.state).toBe('needs_hire');
  expect(before.availability.model.available).toBe(false);
  let workCalls = 0;
  let hires = 0;
  page.on('request', request => {
    if (/\/api\/home-assistant\/(route|ask)$/.test(request.url())) workCalls += 1;
    if (/\/api\/personal-assistant\/hire$/.test(request.url())) hires += 1;
  });
  await page.goto('/');
  await page.locator('#oriGuideLauncher').click();
  await expect(page.locator('#oriGuideAbout')).toContainText(/workspace map/i);
  await page.locator('#oriGuideInput').fill('Model setup');
  await page.locator('#oriGuideSend').click();
  await expect(page.locator('#oriGuideReply')).toHaveAttribute('data-topic', 'model-setup');
  await expect(page.locator('#oriGuideReply')).toContainText(
    'Help and fixed walkthroughs work without a model'
  );
  await page.screenshot({ path: testInfo.outputPath('help-real-prehire-no-model.png') });
  await page
    .locator('#oriGuideWalkthroughs')
    .getByRole('link', { name: 'Meet your assistant', exact: true })
    .click();
  // Meeting the assistant is direct: the walkthrough link opens the
  // personal-assistant configuration itself, with no spotlight to click through.
  const modal = page.locator('#addAgentModal');
  await page.waitForURL(url => url.pathname === '/agents');
  await expect(modal).toBeVisible();
  await expect(modal).toHaveClass(/is-assistant-hire/);
  await expect(page.locator('#oriSpotlight')).toHaveCount(0);
  await page.screenshot({ path: testInfo.outputPath('help-real-meet-walkthrough.png') });
  // Deferring is just closing it.
  await page.locator('#cancelAgentBtn').click();
  await expect(modal).toBeHidden();
  await page.locator('#oriGuideLauncher').click();
  await expect(page.locator('#oriGuideTitle')).toHaveText('Help');
  await expect(page.locator('#oriGuideQuestPortrait')).toBeHidden();
  await expect(page.locator('#oriGuideForm')).toBeVisible();
  // Explicitly resume the same eligible mission; Help itself never advances it.
  await page
    .locator('#oriGuideWalkthroughs')
    .getByRole('link', { name: 'Meet your assistant', exact: true })
    .click();
  await page.waitForURL(url => url.pathname === '/agents');
  await expect(modal).toBeVisible();
  await expect(page.locator('#cr-name')).toBeFocused();
  await page.locator('#cancelAgentBtn').click();
  const after = (await (await page.request.get('/api/personal-assistant')).json())
    .personal_assistant;
  expect(after.state).toBe('needs_hire');
  expect(workCalls).toBe(0);
  expect(hires).toBe(0);
});

test('a failed refreshed relationship blocks new drafts without dropping an existing draft', async ({
  page
}) => {
  await installLocalCdn(page);
  await page.route('**/api/onboarding/status', route =>
    route.fulfill({ json: { completed: true, needs_onboarding: false } })
  );
  await page.route('**/api/home-assistant/conversations', route =>
    route.fulfill({ json: { conversations: [] } })
  );
  let failed = false;
  await page.route('**/api/personal-assistant', route =>
    failed
      ? route.fulfill({ status: 503, json: {} })
      : route.fulfill({
          json: {
            personal_assistant: {
              state: 'active',
              display_name: 'Atlas',
              assistant_id: 'fixture-id',
              hq_workspace_id: 'fixture-hq'
            }
          }
        })
  );
  await page.goto('/');
  await page.locator('#personalAssistantLauncher').click();
  await expect(page.locator('#personalAssistantInput')).toBeEnabled();
  await page.locator('#personalAssistantInput').fill('Retain this draft through the failed read');
  failed = true;
  await page.evaluate(() => (window as any).PersonalAssistantPanel.refresh());
  await expect(page.locator('#personalAssistantInput')).toBeDisabled();
  await expect(page.locator('#personalAssistantInput')).toHaveValue(
    'Retain this draft through the failed read'
  );
  await page.locator('#oriGuideLauncher').click();
  await page.locator('#oriGuideInput').fill('draft the launch notes');
  await page.locator('#oriGuideSend').click();
  await expect(
    page.getByRole('button', { name: 'Retry assistant status', exact: true })
  ).toBeVisible();
  await expect(page.locator('#oriGuideReply')).not.toContainText('Open Atlas');
  await expect(page.locator('#personalAssistantInput')).toHaveValue(
    'Retain this draft through the failed read'
  );
});

test('an old relationship refresh cannot overwrite a newer confirmed identity', async ({
  page
}) => {
  await installLocalCdn(page);
  await page.route('**/api/onboarding/status', route =>
    route.fulfill({ json: { completed: true, needs_onboarding: false } })
  );
  await page.route('**/api/personal-assistant', route =>
    route.fulfill({
      json: {
        personal_assistant: { state: 'active', display_name: 'Atlas', assistant_id: 'fixture-id' }
      }
    })
  );
  await page.goto('/');
  await page.locator('#personalAssistantLauncher').click();
  let release: (() => void) | undefined;
  let held = false;
  const gate = new Promise<void>(resolve => {
    release = resolve;
  });
  await page.route('**/api/personal-assistant', async route => {
    held = true;
    await gate;
    await route.fulfill({
      json: {
        personal_assistant: {
          state: 'paused',
          display_name: 'Old name',
          assistant_id: 'fixture-id'
        }
      }
    });
  });
  const oldRead = page.evaluate(() => (window as any).PersonalAssistantPanel.refresh());
  await expect.poll(() => held).toBe(true);
  await page.evaluate(() =>
    (window as any).PersonalAssistantPanel.applyPersonalAssistant({
      state: 'active',
      display_name: 'New confirmed name',
      assistant_id: 'fixture-id'
    })
  );
  release!();
  await oldRead;
  await expect(page.locator('#personalAssistantPanelTitle')).toHaveText('New confirmed name');
});
