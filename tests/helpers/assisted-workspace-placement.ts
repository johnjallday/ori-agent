import { expect, type Page } from '@playwright/test';

export async function placementJSON(page: Page, path: string) {
  const response = await page.request.get(path);
  expect(response.ok(), path).toBeTruthy();
  const body = await response.json();
  // Keep complete snapshots; roster map iteration order is not a change.
  if (path === '/api/agents') body.agents.sort((a: any, b: any) => a.name.localeCompare(b.name));
  return body;
}

export async function attachPlacementFolder(
  page: Page,
  folder: string,
  map = false,
  keyboard = false
) {
  await page.goto('/');
  await page.waitForFunction(() => Boolean((window as any).PersonalAssistantPanel?.close));
  await page.evaluate(() => (window as any).PersonalAssistantPanel.close());
  if (map) await page.locator('[data-cockpit-view="map"]').click();
  const trigger = page.locator('#cockpitPrimaryActionBtn');
  if (keyboard) {
    await trigger.focus();
    await trigger.press('Enter');
  } else await trigger.click();
  const chip = page.locator('.personal-assistant-panel__chip', { hasText: folder }).first();
  if (keyboard) {
    await chip.focus();
    await chip.press('Enter');
  } else await chip.click();
  await expect(page.getByRole('button', { name: 'Review setup', exact: true })).toBeVisible();
}
