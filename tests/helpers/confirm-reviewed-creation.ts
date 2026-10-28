import type { Page } from '@playwright/test';

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
