// Keep first-run onboarding and a new workspace's first-visit prompts out of
// a spec's way, the way a user would clear them.
//
// Why: a sandbox server was never onboarded and would open the welcome wizard
// over every page; a brand-new workspace without agents asks for a Commander,
// and creating one starts a first task behind a confirmation. Neither is what
// the spec is about.
import { expect, type Page } from '@playwright/test';

/**
 * skipOnboarding answers GET /api/onboarding/status as already completed.
 * Call it before page.goto().
 */
export async function skipOnboarding(page: Page): Promise<void> {
  await page.route('**/api/onboarding/status', route =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ needs_onboarding: false, completed: true })
    })
  );
}

/**
 * clearBlockingModals dismisses the "Create Commander" nudge and the first-task
 * confirmation if either is showing on a workspace page.
 */
export async function clearBlockingModals(page: Page): Promise<void> {
  const nudge = page.getByRole('button', { name: 'Create Commander' });
  if (await nudge.isVisible().catch(() => false)) {
    await nudge.click();
    await expect(nudge).toBeHidden({ timeout: 15000 });
  }
  const gate = page.locator('#workspace-detail-task-confirm-modal');
  if (await gate.isVisible().catch(() => false)) {
    await page.locator('#workspace-detail-task-confirm-close').click();
    await expect(gate).toBeHidden({ timeout: 15000 });
  }
}
