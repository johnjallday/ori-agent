import { test, expect } from '@playwright/test';

const proposal = {
  schema_version: 1,
  capability_id: 'file-janitor',
  view_state: 'proposed',
  stale: false,
  status_message: 'Review the exact workspace and File Curator setup before anything is created.',
  relationship: {
    assistant_id: 'assistant-a11y',
    display_name: 'Ari',
    state: 'needs_hq',
    eligible: true,
    proactive: true
  },
  proposal: {
    revision: 'review-a11y',
    mode: 'create',
    blueprint_id: 'file-janitor',
    blueprint_version: 2,
    blueprint_digest: 'blueprint-a11y',
    team_plan_revision: 'team-a11y',
    team: [
      {
        role_id: 'file-curator',
        name: 'File Curator',
        action: 'create',
        model_configured: false,
        config_digest: 'role-a11y'
      }
    ],
    targets: [],
    metadata_disclosure:
      'File Janitor classifies files from names, types, sizes, and dates only. It does not read file contents or send them to a model.',
    folder_disclosure:
      'You will choose one folder next. Granting it allows Ori to list immediate-child metadata and create or reuse Filed inside that folder; it does not start monitoring.',
    monitoring_disclosure:
      'Monitoring is a separate decision. Ori waits five minutes and runs a daily catch-up at 09:00 local time.',
    file_review_disclosure:
      'Every move or send-to-Trash still requires your separate approval in File Janitor review.',
    default_schedule: '09:00',
    timezone: 'local time',
    no_automatic_tasks: true,
    no_file_actions: true
  },
  actions: [
    { id: 'accept', label: 'Set up for me', enabled: true },
    { id: 'defer_recommendation', label: 'Not now', enabled: true },
    {
      id: 'manual',
      label: 'Customize manually',
      enabled: true,
      route: '/workspaces/new?template=file-janitor'
    }
  ]
};

async function openMockedSetup(page: any) {
  await page.route('**/api/onboarding/status', route =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ needs_onboarding: false, completed: true, skipped: true })
    })
  );
  await page.route('**/api/personal-assistant', route =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        personal_assistant: {
          state: 'needs_hq',
          assistant_id: 'assistant-a11y',
          display_name: 'Ari',
          state_version: 2
        }
      })
    })
  );
  await page.route('**/api/personal-assistant/setup/file-janitor**', route =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ setup: proposal })
    })
  );
  await page.goto('/?quest=tidy-downloads');
  const card = page.locator('#assistantLedSetup');
  await expect(card).toBeVisible({ timeout: 15000 });
  return card;
}

test('setup review has labelled structure, live status, and keyboard-reachable decisions', async ({
  page
}) => {
  await page.emulateMedia({ reducedMotion: 'reduce' });
  const card = await openMockedSetup(page);
  await expect(card).toHaveAttribute('aria-labelledby', 'assistantLedSetupTitle');
  await expect(page.locator('#assistantLedSetupStatus')).toHaveAttribute('aria-live', 'polite');
  await expect(card.getByRole('heading', { name: 'File Janitor' })).toBeVisible();
  await expect(card.getByRole('button', { name: 'Set up for me' })).toBeEnabled();
  await expect(card.getByRole('button', { name: 'Not now' })).toBeEnabled();
  await expect(card.getByRole('link', { name: 'Customize manually' })).toHaveAttribute(
    'href',
    '/workspaces/new?template=file-janitor'
  );

  await card.getByRole('button', { name: 'Set up for me' }).focus();
  await expect(card.getByRole('button', { name: 'Set up for me' })).toBeFocused();
  await page.keyboard.press('Tab');
  await expect(card.getByRole('button', { name: 'Not now' })).toBeFocused();
  await page.keyboard.press('Tab');
  await expect(card.getByRole('link', { name: 'Customize manually' })).toBeFocused();
});

test('the card remains usable at narrow width without page-level horizontal scrolling', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  const card = await openMockedSetup(page);
  await expect(card).toContainText('does not read file contents');
  await expect(card).toContainText('separate approval');
  const widths = await page.evaluate(() => ({
    viewport: window.innerWidth,
    page: document.documentElement.scrollWidth
  }));
  expect(widths.page).toBeLessThanOrEqual(widths.viewport + 1);
});
