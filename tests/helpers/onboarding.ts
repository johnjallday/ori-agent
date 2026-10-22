// Keep first-run onboarding and a new workspace's first-visit prompts out of
// a spec's way, the way a user would clear them.
//
// Why: a sandbox server was never onboarded and would open the welcome wizard
// over every page; a brand-new workspace without agents offers to hire a
// Commander, and hiring one starts a first task behind a confirmation. Neither
// is what the spec is about.
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
 * clearBlockingModals declines the "Hire a Commander" offer and closes the
 * first-task confirmation if either is showing on a workspace page.
 */
export async function clearBlockingModals(page: Page): Promise<void> {
  const gate = page.locator('#workspace-detail-task-confirm-modal');
  const notNow = gate.getByRole('button', { name: 'Not now' });
  if (await notNow.isVisible().catch(() => false)) {
    await notNow.click();
    await expect(gate).toBeHidden({ timeout: 15000 });
  }
  if (await gate.isVisible().catch(() => false)) {
    await page.locator('#workspace-detail-task-confirm-close').click();
    await expect(gate).toBeHidden({ timeout: 15000 });
  }
}
