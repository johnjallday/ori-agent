import { test, expect } from '@playwright/test';

// Browser-fixtured service replies exercise the actual universal Ask controller
// when the personal relationship cannot be read. No model or mutation is run.
test('shared Ask keeps its progress and explicit confirmation outside the personal drawer', async ({
  page
}) => {
  await page.route('**/api/onboarding/status', route =>
    route.fulfill({
      json: { needs_onboarding: false, completed: true, skipped: true }
    })
  );
  await page.route('**/api/personal-assistant', route => route.fulfill({ status: 503, json: {} }));
  await page.route('**/api/progression', route =>
    route.fulfill({ json: { dismissed: true, missions: [] } })
  );
  await page.route('**/api/ori-guide', route =>
    route.fulfill({
      json: { status: 'unknown', answer: 'This request needs the work controller.', actions: [] }
    })
  );
  await page.route('**/api/home-assistant/route', route =>
    route.fulfill({
      json: {
        intent: 'app_introspection',
        route_mode: 'home_inline',
        target_surface: 'current',
        requires_creation: false
      }
    })
  );
  let calls = 0;
  let finish: (() => void) | undefined;
  await page.route('**/api/home-assistant/ask', async route => {
    calls++;
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
          arguments: { text: 'A fixture fact' }
        }
      }
    });
  });
  await page.goto('/');
  await page.locator('#oriGuideMapTrigger').click();
  await expect(page.locator('#oriGuideReply')).toHaveAttribute('data-status', 'unknown');
  await page.locator('#oriGuideInput').fill('What needs my attention?');
  await page.locator('#oriGuideSend').click();
  await expect.poll(() => calls).toBe(1);
  const activity = page.locator('#homeAssistantThinkingModal');
  await expect(activity).toHaveAttribute('data-home-assistant-panel-scope', 'universal');
  await expect(activity).toBeVisible();
  await expect(page.locator('#homeAssistantThinkingStatus')).toContainText('Reviewing');
  await expect(page.locator('#homeAssistantThinkingModalLabel')).toBeVisible();
  finish!();
  const actions = page.locator('#homeAssistantActions');
  await expect(actions.getByRole('button', { name: 'Confirm', exact: true })).toBeVisible();
  await expect(page.locator('.home-assistant-conversation-section-header')).toBeVisible();
  await expect(page.locator('#homeAssistantConversation')).toContainText(
    'Remember the reviewed fact?'
  );
  await actions.getByRole('button', { name: 'Cancel', exact: true }).click();
  await expect(page.locator('#homeAssistantConversation')).toContainText(
    'I will not make that change'
  );
  expect(calls).toBe(1);
});
