import { expect, type Locator } from '@playwright/test';

// Open the one disclosure through its real control, without changing setup state.
export async function revealFolderSetupDetails(card: Locator) {
  const details = card.locator('#personalAssistantFolderDetails');
  await expect(details).toBeVisible();
  if ((await details.getAttribute('open')) === null) {
    await details.locator('summary').first().click();
  }
}
