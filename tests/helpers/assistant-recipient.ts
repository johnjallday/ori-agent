// Address the assistant drawer to the hired Personal Assistant.
//
// Why: on a workspace page whose workspace has a Commander (for example a
// group created with its template agents), the drawer opens as that
// Commander (tasks/prd-assistant-recipient-picker.md). Specs about the
// Personal Assistant's own turns on such a page pick it first, through the
// same menu a person uses. Call it with the drawer open.
import { expect, type Page } from '@playwright/test';

export async function talkToPersonalAssistant(page: Page): Promise<void> {
  // Wait until the page has decided who the drawer talks to.
  await page.waitForFunction(() => {
    const current = (window as any).PersonalAssistantRecipient?.current?.();
    return Boolean(current) && current.loading !== true;
  });
  const kind = await page.evaluate(
    () => (window as any).PersonalAssistantRecipient.current().kind as string
  );
  if (kind !== 'workspace_entry') return;
  await page.locator('#personalAssistantRecipientButton').click();
  await page
    .locator('#personalAssistantRecipientMenu')
    .getByRole('menuitemradio', { name: /Personal Assistant/ })
    .click();
  await expect(page.locator('#personalAssistantPanel')).toHaveAttribute(
    'data-recipient',
    'personal'
  );
}
