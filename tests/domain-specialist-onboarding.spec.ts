import { test, expect } from '@playwright/test';

// This was the installed-app specialist-card suite. The detector remains
// available for silent installed/not-installed copy and onboarding profiles,
// but Home must not run it to solicit a separate capability confirmation.
test('Home never mounts the retired installed-app offer', async ({ page, request }) => {
  await request.post('/api/onboarding/skip');
  const relationship = (await (await request.get('/api/personal-assistant')).json())
    .personal_assistant;
  if (relationship.state === 'needs_hire') {
    const hire = await request.post('/api/personal-assistant/hire', {
      data: {
        request_id: 'retired-card-hire',
        if_version: relationship.state_version,
        display_name: 'Atlas',
        mandate: 'Keep my projects moving.',
        focus_areas: ['plan_my_day']
      }
    });
    expect(hire.ok(), await hire.text()).toBeTruthy();
  }
  const hired = (await (await request.get('/api/personal-assistant')).json()).personal_assistant;
  if (hired.state === 'needs_hq') {
    const hq = await request.post('/api/personal-assistant/hq', {
      data: { request_id: 'retired-card-hq', if_version: hired.state_version, name: 'My HQ' }
    });
    expect(hq.ok(), await hq.text()).toBeTruthy();
  }
  let offerDetectionCalls = 0;
  await page.route('**/api/onboarding/detect', route => {
    offerDetectionCalls++;
    return route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        success: true,
        apps: [{ name: 'REAPER' }],
        specialist: { slug: 'music_production' }
      })
    });
  });
  await page.goto('/?panel=today');
  await expect(page.locator('#personalAssistantToday')).toBeVisible();
  await expect(page.locator('#personalAssistantFolder')).toBeVisible();
  await expect(page.locator('#personalAssistantSpecialistOffer')).toHaveCount(0);
  await expect(page.locator('#personalAssistantSpecialistManual')).toHaveCount(0);
  expect(offerDetectionCalls).toBe(0);
});
