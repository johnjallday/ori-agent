import { test, expect } from '@playwright/test';
import { installLocalCdn } from './helpers/offline-cdn';

// Fixture-backed native work replies, failed relationship read, real Help.
// Help never starts work; native work still owns progress and explicit review.
test('native work keeps one activity host outside Help after relationship failure', async ({
  page
}) => {
  await installLocalCdn(page);
  await page.route('**/api/onboarding/status', route =>
    route.fulfill({ json: { needs_onboarding: false, completed: true, skipped: true } })
  );
  await page.route('**/api/personal-assistant', route => route.fulfill({ status: 503, json: {} }));
  await page.route('**/api/progression', route =>
    route.fulfill({ json: { dismissed: true, missions: [] } })
  );
  let routes = 0;
  await page.route('**/api/home-assistant/route', route => {
    routes += 1;
    return route.fulfill({
      json: {
        intent: 'app_introspection',
        route_mode: 'home_inline',
        target_surface: 'current',
        requires_creation: false
      }
    });
  });
  let calls = 0;
  let finish: (() => void) | undefined;
  await page.route('**/api/home-assistant/ask', async route => {
    calls += 1;
    await new Promise<void>(resolve => {
      finish = resolve;
    });
    await route.fulfill({
      json: {
        response: 'Review this change first.',
        requires_confirmation: true,
        confirmation: {
          action_id: 'scope-review',
          action_type: 'remember',
          summary: 'Remember the reviewed fact?',
          arguments: { text: 'A fictional fixture fact' }
        }
      }
    });
  });
  await page.goto('/');
  await page.locator('#oriGuideLauncher').click();
  await page.locator('#oriGuideInput').fill('/note a fictional request');
  await page.locator('#oriGuideSend').click();
  await expect(page.locator('#oriGuideReply')).toHaveAttribute('data-status', 'answered');
  await expect(page.locator('#oriGuideReply')).toContainText('Personal Assistant');
  expect(routes).toBe(0);
  expect(calls).toBe(0);
  const activity = page.locator('#homeAssistantThinkingModal');
  await expect(
    page.locator('#personalAssistantActivityMount #homeAssistantThinkingModal')
  ).toHaveCount(1);
  await expect(page.locator('#oriGuidePanel #homeAssistantThinkingModal')).toHaveCount(0);

  // Exercise the retained public native-work contract, not a Help escalation.
  await page.evaluate(() => {
    void (window as any).OriAskRouting.submit('What needs my attention?', {
      context: { page_path: '/', surface: 'home', origin: 'workspace_surface' }
    });
  });
  await expect.poll(() => calls).toBe(1);
  expect(routes).toBe(1);
  await expect(page.locator('#personalAssistantPanel')).toBeVisible();
  await expect(page.locator('#personalAssistantPanelTitle')).toHaveText('Work activity');
  await expect(page.locator('#oriGuidePanel')).toBeHidden();
  await expect(activity).toHaveAttribute('data-home-assistant-panel-scope', 'personal-assistant');
  await expect(page.locator('#homeAssistantThinkingStatus')).toContainText('Reviewing');
  await page.locator('#oriGuideLauncher').click();
  finish!();
  await expect(page.locator('#oriGuidePanel')).toBeVisible();
  await expect(page.locator('#homeAssistantReopenBtn')).toHaveAttribute(
    'title',
    /^(Open|Reopen) work activity$/
  );
  await page.locator('#homeAssistantReopenBtn').click();
  await expect(page.locator('#personalAssistantPanel')).toBeVisible();
  await expect(page.locator('#oriGuidePanel')).toBeHidden();
  await expect
    .poll(() => page.evaluate(() => !!document.activeElement?.closest('#personalAssistantPanel')))
    .toBe(true);
  const actions = page.locator('#homeAssistantActions');
  await expect(actions.getByRole('button', { name: 'Confirm', exact: true })).toBeVisible();
  await expect(page.locator('#homeAssistantConversation')).toContainText(
    'Remember the reviewed fact?'
  );
  await actions.getByRole('button', { name: 'Cancel', exact: true }).click();
  await expect(page.locator('#homeAssistantConversation')).toContainText(
    'I will not make that change'
  );
  expect(calls).toBe(1);
  expect(routes).toBe(1);
});
