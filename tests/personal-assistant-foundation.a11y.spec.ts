import { test, expect, Page } from '@playwright/test';

async function contrastRatio(page: Page, selector: string) {
  return page.locator(selector).evaluate(element => {
    const rgb = (value: string) => {
      const channels = (value.match(/[\d.]+/g) || []).slice(0, 3).map(Number);
      return value.startsWith('color(srgb') ? channels.map(channel => channel * 255) : channels;
    };
    const luminance = (channels: number[]) => {
      const linear = channels.map(channel => {
        const value = channel / 255;
        return value <= 0.03928 ? value / 12.92 : ((value + 0.055) / 1.055) ** 2.4;
      });
      return 0.2126 * linear[0] + 0.7152 * linear[1] + 0.0722 * linear[2];
    };
    const foreground = rgb(getComputedStyle(element).color);
    let current: Element | null = element;
    let background = [255, 255, 255];
    while (current) {
      const style = getComputedStyle(current);
      if (!style.backgroundColor.endsWith(', 0)') && style.backgroundColor !== 'transparent') {
        background = rgb(style.backgroundColor);
        break;
      }
      current = current.parentElement;
    }
    const [lighter, darker] = [luminance(foreground), luminance(background)].sort((a, b) => b - a);
    return (lighter + 0.05) / (darker + 0.05);
  });
}

async function mockCompletedOnboarding(page: Page) {
  await page.route('**/api/onboarding/status', route =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        needs_onboarding: false,
        completed: true,
        current_step: 4,
        steps_completed: ['done']
      })
    })
  );
}

async function mockAssistantState(
  page: Page,
  relationshipState: 'active' | 'paused' | 'repair_needed' = 'active',
  todayState = relationshipState,
  modelAvailable = true
) {
  await page.route(/\/api\/personal-assistant$/, route =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        personal_assistant: {
          state: relationshipState,
          state_version: 7,
          assistant_id: 'assistant-stable',
          display_name: 'Atlas',
          hq_workspace_id: 'hq-1',
          mandate: 'Keep commitments visible.',
          focus_areas: ['plan_my_day'],
          daily_brief: {
            timezone: 'UTC',
            schedule_days: ['mon', 'wed', 'fri'],
            schedule_time: '08:00',
            schedule_enabled: true,
            scope: 'all',
            include_future_workspaces: true,
            notify_on_ready: false,
            config_revision: 3
          },
          availability: {
            model: {
              status: modelAvailable ? 'available' : 'not_configured',
              available: modelAvailable
            }
          }
        }
      })
    })
  );
  await page.route('**/api/personal-assistant/today', route =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        today: {
          state: todayState,
          relationship_state: relationshipState,
          display_name: 'Atlas',
          hq_workspace_id: 'hq-1',
          hq_workspace_slug: 'personal-hq',
          model: {
            status: modelAvailable ? 'available' : 'not_configured',
            available: modelAvailable
          },
          brief: { health: { status: 'healthy_empty' }, items: [] },
          decisions: { health: { status: 'healthy_empty' }, items: [] },
          priorities: { health: { status: 'healthy_empty' }, items: [] },
          follow_ups: { health: { status: 'healthy_empty' }, items: [] },
          results: { health: { status: 'healthy_empty' }, items: [] },
          links: {
            personal_hq: '/workspaces/personal-hq',
            working_agreement: '/?personal-assistant=working-agreement',
            memory: '/workspaces/personal-hq#memory',
            advanced: '/agents'
          }
        }
      })
    })
  );
  await page.route('**/api/personal-assistant/capabilities', route =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ capabilities: { state: relationshipState, cards: [] } })
    })
  );
}

test.describe('Personal Assistant Foundation accessibility', () => {
  test.beforeEach(async ({ page }) => {
    await page.emulateMedia({ reducedMotion: 'reduce' });
  });

  test('active Home, assistant dialog, and working agreement are keyboard operable', async ({
    page
  }) => {
    await mockCompletedOnboarding(page);
    await mockAssistantState(page);
    await page.goto('/');

    const launcher = page.getByRole('button', { name: /Atlas Personal Assistant/i });
    await expect(launcher).toBeVisible();
    await launcher.focus();
    await page.keyboard.press('Enter');
    const assistantDialog = page.getByRole('dialog', { name: 'Atlas' });
    await expect(assistantDialog).toBeVisible();
    await expect(page.locator('#personalAssistantInput')).toBeFocused();
    await expect(page.locator('#personalAssistantPanelStatus')).toHaveAttribute(
      'aria-live',
      'polite'
    );
    expect(await contrastRatio(page, '#personalAssistantLauncherName')).toBeGreaterThanOrEqual(4.5);
    expect(await contrastRatio(page, '#personalAssistantTodayBanner')).toBeGreaterThanOrEqual(4.5);
    await page.keyboard.press('Escape');
    await expect(assistantDialog).toBeHidden();
    await expect(launcher).toBeFocused();

    const agreementLink = page.getByRole('link', { name: 'Working agreement' });
    await agreementLink.focus();
    await page.keyboard.press('Enter');
    const agreement = page.getByRole('dialog', { name: 'How your assistant works with you' });
    await expect(agreement).toBeVisible();
    await expect(page.getByRole('button', { name: 'Close working agreement' })).toBeFocused();
    await expect(page.getByLabel('Mandate')).toBeVisible();
    await expect(page.getByLabel('Brief scope')).toBeVisible();
    await expect(page.locator('#personalAssistantContinuityStatus')).toHaveAttribute(
      'aria-live',
      'polite'
    );
    await page.keyboard.press('Escape');
    await expect(agreement).toBeHidden();
    await expect(agreementLink).toBeFocused();
  });

  for (const scenario of [
    {
      name: 'paused',
      relationship: 'paused' as const,
      today: 'paused',
      model: true,
      copy: /Paused/i
    },
    {
      name: 'partial',
      relationship: 'active' as const,
      today: 'partial',
      model: true,
      copy: /partial|unavailable|current/i
    },
    {
      name: 'no model',
      relationship: 'active' as const,
      today: 'model_unavailable',
      model: false,
      copy: /model|deterministic/i
    },
    {
      name: 'repair',
      relationship: 'repair_needed' as const,
      today: 'repair_needed',
      model: true,
      copy: /repair/i
    }
  ]) {
    test(`${scenario.name} state is stated in text and never color alone`, async ({ page }) => {
      await mockCompletedOnboarding(page);
      await mockAssistantState(page, scenario.relationship, scenario.today, scenario.model);
      await page.goto('/');
      await expect(page.locator('#personalAssistantToday')).toBeVisible();
      await expect(page.locator('#personalAssistantToday')).toContainText(scenario.copy);
      const overflow = await page.evaluate(
        () => document.documentElement.scrollWidth > document.documentElement.clientWidth
      );
      expect(overflow).toBe(false);
    });
  }

  test('no-model onboarding exposes named, associated hire and preview controls', async ({
    page
  }) => {
    await page.route('**/api/onboarding/status', route =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          needs_onboarding: true,
          completed: false,
          current_step: 0,
          steps_completed: [],
          timezone: 'UTC'
        })
      })
    );
    await page.route('**/api/providers', route =>
      route.fulfill({ status: 200, contentType: 'application/json', body: '{"providers":[]}' })
    );
    await page.route(/\/api\/personal-assistant$/, route =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: '{"personal_assistant":{"state":"needs_hire","state_version":1,"availability":{"model":{"status":"not_configured","available":false}}}}'
      })
    );
    await page.goto('/');
    await expect(page.locator('#onboardingModal')).toBeVisible();
    await expect(page.getByLabel('Your name')).toBeVisible();
    await expect(page.locator('#pafAssistantName')).toHaveAttribute('maxlength', '100');
    await expect(page.locator('#pafHireConfirm')).toHaveAttribute('type', 'checkbox');
    await expect(page.locator('#pafAssignmentStatus')).toHaveAttribute('aria-live', 'polite');
  });
});
