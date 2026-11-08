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

/**
 * Opens the conversation's attach chooser (a folder attached to the turn).
 * Add folder in "+" now starts an agent on a folder instead
 * (folder-agent-explore.spec.ts); the attach flow stays for a pasted path's
 * card and Re-pick, so specs of that flow open it directly.
 */
export async function openFolderAttach(page: Page): Promise<void> {
  await page.waitForFunction(() => Boolean((window as any).PersonalAssistantFolderContext?.open));
  // open() does nothing while a selection or a reply is still settling, as a
  // click on a busy control would; ask again until the chooser shows.
  const chooser = page.locator('#personalAssistantContextChooser');
  await expect
    .poll(
      async () => {
        if (await chooser.isVisible()) return true;
        await page.evaluate(() => (window as any).PersonalAssistantFolderContext.open());
        return chooser.isVisible();
      },
      { timeout: 15_000 }
    )
    .toBe(true);
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
