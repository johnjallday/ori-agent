import { expect, type Page } from '@playwright/test';

const ADVANCE = /^(Continue|Confirm installation|Confirm update|Confirm enablement|Back to setup)$/;

// After a row's Install/Update/Enable button opened the wizard, gives every
// remaining consent the explicit click it needs until the summary closes. Each
// change downloads a real reviewed release into the disposable server's store.
export async function finishPluginSetupWizard(page: Page): Promise<void> {
  const wizard = page.locator('#pluginSetupWizard');
  await expect(wizard).toBeVisible();
  for (let step = 0; step < 10; step++) {
    const next = wizard.locator('footer button:enabled').filter({ hasText: ADVANCE });
    await expect(next).toHaveCount(1, { timeout: 120_000 });
    const label = await next.innerText();
    await next.click();
    if (label === 'Back to setup') {
      await expect(wizard).toBeHidden();
      return;
    }
  }
  throw new Error('The plugin setup wizard did not reach its summary');
}
