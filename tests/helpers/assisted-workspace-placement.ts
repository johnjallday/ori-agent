import { expect, type Page } from '@playwright/test';
import { openFolderAttach } from './assistant-menus';

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
  // These journeys review a folder attached to a conversation. The Files
  // header hires a folder agent instead; it isn't the placement entry.
  await page.goto(map ? '/?view=map' : '/');
  const launcher = page.locator('#personalAssistantLauncher');
  if (!(await page.locator('#personalAssistantPanel').isVisible())) {
    if (keyboard) await launcher.press('Enter');
    else await launcher.click();
  }
  await openFolderAttach(page);
  const chip = page
    .locator('#personalAssistantContextChooser')
    .getByRole('button', { name: folder, exact: true });
  if (keyboard) {
    await chip.focus();
    await chip.press('Enter');
  } else await chip.click();
  await expect(page.getByRole('button', { name: 'Review setup', exact: true })).toBeVisible();
}
