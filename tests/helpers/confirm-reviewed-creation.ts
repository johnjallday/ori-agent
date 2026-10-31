import { expect, type Page } from '@playwright/test';

/** Only user gestures; installs no routes and fabricates no domain receipts. */
export async function confirmReviewedCreation(page: Page, open = false) {
  const review = page.locator('#workspaceCreationReviewPanel');
  if (await review.isVisible()) {
    await review.getByRole('button', { name: 'Use automatic placement', exact: true }).click();
    await review.getByRole('button', { name: 'Create workspace', exact: true }).click();
    if (open) await review.getByRole('button', { name: 'Open workspace', exact: true }).click();
  } else {
    await page.locator('#createFolderBtn').click();
  }
}

/**
 * The one final gesture for whichever owner the creator is using. The shared
 * review (ordinary, grouped project, ordinary Group or managed Home) is
 * confirmed through automatic placement and its verified result is awaited;
 * `open` then follows the result's own Open action. Guided/specialized owners
 * that still render their own button keep that button.
 */
export async function confirmCreatorFinalAction(page: Page, { open = false } = {}) {
  const review = page.locator('#workspaceCreationReviewPanel');
  if (!(await review.isVisible())) {
    await page.locator('#createFolderBtn').click();
    return;
  }
  await review.getByRole('button', { name: 'Use automatic placement', exact: true }).click();
  const confirm = review.locator('[data-placement-action="confirm"]');
  await expect(confirm).toBeEnabled();
  await confirm.click();
  await expect(
    review.getByRole('heading', { name: /^(Workspace created|Group created|Home ready)$/ })
  ).toBeVisible({ timeout: 20_000 });
  if (open) await review.locator('[data-placement-action="open"]').click();
}

/**
 * A Map placement reports on the Map, with nothing reopened over it. Details is
 * the explicit way to the owner's full receipt, which the caller then asserts.
 */
export async function openMapReceiptDetails(page: Page, title: string | RegExp) {
  const status = page.locator('[data-creation-map-status]');
  await expect(status).toBeVisible({ timeout: 30_000 });
  await expect(status).toContainText(title);
  await expect(page.locator('#addFolderModal')).toBeHidden();
  await expect(page.locator('.modal-backdrop')).toHaveCount(0);
  await status.getByRole('button', { name: /^(Details|Continue)$/ }).click();
  await expect(status).toBeHidden();
}

/** Follows the verified result's own Open action (after reading its reply). */
export async function openCreatorResult(page: Page) {
  const review = page.locator('#workspaceCreationReviewPanel');
  if (await review.isVisible()) await review.locator('[data-placement-action="open"]').click();
}
