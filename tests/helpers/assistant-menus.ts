import { expect, type Locator, type Page } from '@playwright/test';

// The drawer keeps some controls inside menus (assistant-drawer-redesign):
// Add folder, Add link, Tree + Chat and Review setup in the composer's "+"
// menu; New and Continue… in the header's conversation menu; Watch Ori in
// "…". The controls keep their ids, so a spec opens the menu first and then
// uses the control as before.

async function openMenu(page: Page, button: string, menu: string): Promise<void> {
  const trigger = page.locator(button);
  if ((await trigger.getAttribute('aria-expanded')) !== 'true') await trigger.click();
  await expect(page.locator(menu)).toBeVisible();
}

/** Opens the composer's "+" menu. */
export async function openAttachMenu(page: Page): Promise<void> {
  await openMenu(page, '#personalAssistantAttachMenu', '#personalAssistantAttachItems');
}

/** A control of the "+" menu (e.g. '#personalAssistantFolderChip'), with the menu open. */
export async function attachItem(page: Page, selector: string): Promise<Locator> {
  await openAttachMenu(page);
  return page.locator(selector);
}

/** Opens the "+" menu and chooses one of its controls. */
export async function chooseAttach(page: Page, selector: string): Promise<void> {
  await (await attachItem(page, selector)).click();
}

/** Opens the header's conversation menu. */
export async function openConversationMenu(page: Page): Promise<void> {
  await openMenu(page, '#personalAssistantConversationMenu', '#personalAssistantConversationItems');
}

/** A control of the conversation menu (e.g. '#personalAssistantConversationNew'), with the menu open. */
export async function conversationItem(page: Page, selector: string): Promise<Locator> {
  await openConversationMenu(page);
  return page.locator(selector);
}

/** Opens the conversation menu and chooses New, Continue… or the entry agent's New. */
export async function chooseConversation(page: Page, selector: string): Promise<void> {
  await (await conversationItem(page, selector)).click();
}

/** Opens the header's "…" menu, where Watch Ori is. */
export async function openMoreMenu(page: Page): Promise<void> {
  const more = page.locator('#personalAssistantMore');
  if (!(await more.evaluate(node => (node as HTMLDetailsElement).open)))
    await more.locator('summary').click();
  await expect(more.locator('.personal-assistant-panel__links')).toBeVisible();
}
