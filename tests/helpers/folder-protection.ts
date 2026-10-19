// Shared setup for the folder-protection specs: a real hired assistant with a
// Personal HQ, made through the same APIs Home uses. Idempotent per server.
import { expect, type Page } from '@playwright/test';

// A sandbox server was never onboarded and would open the welcome wizard over
// every page; that is unrelated to folder protection.
export async function skipOnboarding(page: Page): Promise<void> {
  await page.route('**/api/onboarding/status', route =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ needs_onboarding: false, completed: true })
    })
  );
}

// openNeedsYou opens the assistant's Today and its "Also needs you" queue,
// where the protection line sits beside the other backup items.
export async function openNeedsYou(page: Page): Promise<void> {
  await page.goto('/?panel=today');
  await expect(page.locator('#personalAssistantToday')).toBeVisible();
  const summary = page.locator('#personalAssistantSummaryToggle');
  await expect(summary).toBeVisible();
  if ((await summary.getAttribute('aria-expanded')) !== 'true') await summary.click();
  await expect(page.locator('#personalAssistantTodaySections')).toBeVisible();
  const queue = page.locator('#personalAssistantNeedsYouQueue');
  if (!(await queue.isVisible())) return;
  if ((await queue.getAttribute('open')) === null)
    await queue.locator('#personalAssistantNeedsYouQueueTitle').click();
}

export async function ensurePersonalHQ(page: Page): Promise<void> {
  const current = await (await page.request.get('/api/personal-assistant')).json();
  if (current.personal_assistant?.state === 'active') return;
  const rootSetting = await page.request.post('/api/settings/workspace-root', {
    data: { workspace_root: '' }
  });
  expect(rootSetting.status(), await rootSetting.text()).toBe(200);
  const hire = await page.request.post('/api/personal-assistant/hire', {
    data: {
      request_id: 'folder-protection-hire',
      if_version: 0,
      display_name: 'Atlas',
      mandate: 'Keep my priorities visible.',
      focus_areas: ['plan_my_day']
    }
  });
  expect(hire.status(), await hire.text()).toBe(201);
  const hq = await page.request.post('/api/personal-assistant/hq', {
    data: {
      request_id: 'folder-protection-hq',
      if_version: (await hire.json()).personal_assistant.state_version,
      name: 'My HQ',
      timezone: 'UTC',
      // What Home's Build My HQ sends: workspaces made later stay in the
      // saved scope, which Today and the protection line read.
      scope: 'all',
      include_future_workspaces: true
    }
  });
  expect(hq.status(), await hq.text()).toBe(201);
}
