import { test, expect, Page } from '@playwright/test';
import { installLocalCdn } from './helpers/offline-cdn';

async function shell(page: Page) {
  await installLocalCdn(page);
  await page.route('**/api/onboarding/status', route =>
    route.fulfill({ json: { completed: true, needs_onboarding: false } })
  );
  await page.route('**/api/home-assistant/conversations', route =>
    route.fulfill({ json: { conversations: [] } })
  );
}

const states = [
  ['needs_hire', 'Meet your assistant', '/?quest=meet-assistant'],
  ['hiring', 'Resume meeting your assistant', '/?quest=meet-assistant'],
  ['needs_hq', 'Build Personal HQ', '/?quest=build-hq'],
  ['provisioning_hq', 'Resume Personal HQ setup', '/?quest=build-hq'],
  ['repair_needed', 'Repair personal assistant', '/agents?quest=meet-assistant'],
  ['active', 'Open Atlas', ''],
  ['paused', 'Open Atlas', '']
];
for (const [state, label, href] of states) {
  test(`authoritative ${state} offers only its existing transition, without work`, async ({
    page
  }, testInfo) => {
    await shell(page);
    await page.route('**/api/personal-assistant', route =>
      route.fulfill({
        json: {
          personal_assistant: {
            state,
            display_name: 'Atlas',
            assistant_id: 'fixture-assistant',
            hq_workspace_id: 'fixture-hq',
            availability: { model: { available: false, status: 'not_configured' } }
          }
        }
      })
    );
    let workCalls = 0;
    page.on('request', request => {
      if (/\/api\/home-assistant\/(route|ask)$/.test(request.url())) workCalls += 1;
    });
    await page.goto('/');
    await page.locator('#oriGuideLauncher').click();
    await page.locator('#oriGuideInput').fill('draft the launch notes');
    await page.locator('#oriGuideSend').click();
    const result = page.locator('#oriGuideReply');
    if (href) {
      await expect(result.getByRole('link', { name: label, exact: true })).toHaveAttribute(
        'href',
        href
      );
      await expect(
        page.locator('#oriGuideWalkthroughs').getByRole('link', { name: label, exact: true })
      ).toHaveAttribute('href', href);
      await expect(result.getByRole('button', { name: /Open Atlas/ })).toHaveCount(0);
    } else {
      await expect(result.getByRole('button', { name: label, exact: true })).toBeVisible();
      await expect(page.locator('#oriGuideWalkthroughs a')).toHaveCount(0);
      // Open the confirmed identity directly; conversation loading is independently gated.
      await page.locator('#personalAssistantLauncher').click();
      await expect(page.locator('#personalAssistantModelSetup')).toBeVisible();
      await expect(page.locator('#personalAssistantModelSetup')).toHaveAttribute(
        'href',
        '/settings#system-model'
      );
      await expect(page.locator('#personalAssistantInput')).toBeEnabled();
    }
    expect(workCalls).toBe(0);
    if (state === 'needs_hq' || state === 'repair_needed')
      await page.screenshot({ path: testInfo.outputPath(`help-${state}-fixture.png`) });
  });
}

test('a failed relationship offers retry, not an invented hire or cached identity', async ({
  page
}, testInfo) => {
  await shell(page);
  let recovered = false;
  await page.route('**/api/personal-assistant', route =>
    recovered
      ? route.fulfill({
          json: {
            personal_assistant: {
              state: 'active',
              display_name: 'Atlas',
              assistant_id: 'fixture-assistant',
              hq_workspace_id: 'fixture-hq'
            }
          }
        })
      : route.fulfill({ status: 503, json: {} })
  );
  let workCalls = 0;
  page.on('request', request => {
    if (/\/api\/home-assistant\/(route|ask)$/.test(request.url())) workCalls += 1;
  });
  await page.goto('/');
  await page.locator('#oriGuideLauncher').click();
  await page.locator('#oriGuideInput').fill('draft the launch notes');
  await page.locator('#oriGuideSend').click();
  await expect(
    page.getByRole('button', { name: 'Retry assistant status', exact: true })
  ).toBeVisible();
  await expect(page.locator('#oriGuideWalkthroughs a')).toHaveCount(0);
  await expect(page.locator('#oriGuideReply')).not.toContainText('Open Atlas');
  await page.screenshot({ path: testInfo.outputPath('help-unavailable-relationship.png') });
  recovered = true;
  await page.getByRole('button', { name: 'Retry assistant status', exact: true }).click();
  await expect(page.getByRole('button', { name: 'Open Atlas', exact: true })).toBeVisible();
  await expect(page.locator('#oriGuidePanel')).toBeVisible();
  await expect(page.locator('#personalAssistantPanel')).toBeHidden();
  await expect(page.locator('#personalAssistantInput')).toHaveValue('');
  await expect(page.locator('#oriGuideInput')).toHaveValue('draft the launch notes');
  expect(workCalls).toBe(0);
});

test('loading or malformed status cannot offer a named assistant or start work', async ({
  page
}) => {
  await shell(page);
  let release: (() => void) | undefined;
  const gate = new Promise<void>(resolve => {
    release = resolve;
  });
  await page.route('**/api/personal-assistant', async route => {
    await gate;
    await route.fulfill({
      json: { personal_assistant: { state: 'unrecognized', display_name: 'Unconfirmed name' } }
    });
  });
  let workCalls = 0;
  page.on('request', request => {
    if (/\/api\/home-assistant\/(route|ask)$/.test(request.url())) workCalls += 1;
  });
  await page.goto('/', { waitUntil: 'domcontentloaded' });
  await page.locator('#oriGuideLauncher').click();
  await page.locator('#oriGuideInput').fill('draft the launch notes');
  await page.locator('#oriGuideSend').click();
  await expect(
    page.getByRole('button', { name: 'Retry assistant status', exact: true })
  ).toBeVisible();
  await expect(page.locator('#oriGuideWalkthroughs a')).toHaveCount(0);
  release!();
  await expect(page.locator('#oriGuideWalkthroughs')).toContainText('could not be checked');
  await expect(page.locator('#oriGuideReply')).not.toContainText('Unconfirmed name');
  expect(workCalls).toBe(0);
});
