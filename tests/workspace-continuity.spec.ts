import { test, expect, type Page } from '@playwright/test';

/**
 * Portable workspace continuity — the real, unmocked directory-only journey on
 * an isolated destination server.
 *
 * It needs a copied workspace folder whose checkpoint was prepared on a
 * different isolated server:
 *
 *   ./scripts/demo-server.sh 8931 "$TMPDIR/ori-cont-source"
 *   ./scripts/continuity-demo-seed.sh http://localhost:8931 "$TMPDIR/ori-cont-source" "$TMPDIR/ori-cont-transfer"
 *   ./scripts/demo-server.sh 8932 "$TMPDIR/ori-cont-dest"
 *   ./scripts/e2e.sh --port 8932 --env CONTINUITY_FOLDER=<printed folder> tests/workspace-continuity.spec.ts
 *
 * The destination must be fresh (no assistant hired). Nothing is mocked: the
 * review, import, restored views and status all come from the server.
 */
const folder = process.env.CONTINUITY_FOLDER || '';
// An older copy: the same folder without its .ori/continuity checkpoint, e.g.
//   rsync -a --exclude '.ori/continuity' <folder>/ "$TMPDIR/ori-cont-legacy/garden-hq/"
const legacyFolder = process.env.LEGACY_CONTINUITY_FOLDER || '';

async function appears(locator: ReturnType<Page['locator']>, timeout: number): Promise<boolean> {
  return locator
    .waitFor({ state: 'visible', timeout })
    .then(() => true)
    .catch(() => false);
}

// A fresh destination first asks for a name and a model; this journey needs
// neither a model nor the assistant mission.
async function finishFirstRun(page: Page): Promise<void> {
  const name = page.getByPlaceholder(/Your name/);
  if (await appears(name, 10000)) {
    await name.fill('Continuity Tester');
    await page.getByRole('button', { name: 'Continue', exact: true }).click();
    await page.getByText('Continue without a model').click();
  }
  const notNow = page.getByRole('button', { name: 'Not now' });
  if (await appears(notNow, 5000)) await notNow.click();
}

test.describe('Portable workspace continuity', () => {
  test.skip(!folder, 'set CONTINUITY_FOLDER to a prepared, copied workspace folder');

  test('reviews, imports and continues a copied Personal HQ with its history', async ({ page }) => {
    await page.goto('/');
    await finishFirstRun(page);

    await page.getByRole('button', { name: 'Import Folder' }).first().click();
    const path = page.getByRole('textbox', { name: 'Folder path' });
    await path.fill(folder);
    await path.press('Tab');

    // Review: a named region with the choice, the disclosure and routines-off.
    const review = page.getByRole('region', { name: /Continue with/ });
    await expect(review).toBeVisible({ timeout: 15000 });
    await expect(review).toContainText('conversation');
    await expect(review).toContainText('Background routines stay off');
    await expect(review).toContainText('Treat copies of it as personal data');
    // The ordinary Import Folder action never runs over a checkpoint folder.
    await expect(page.locator('#createFolderBtn')).toBeDisabled();
    await expect(review.getByRole('button', { name: 'Import workspace only' })).toBeVisible();

    await review.getByRole('button', { name: 'Import and continue' }).click();
    const result = page.getByRole('region', { name: 'Workspace restored' });
    await expect(result).toBeVisible({ timeout: 30000 });
    await expect(result).toContainText('Follow-ups');
    await expect(result).toContainText('Conversations: 1');
    await expect(result).toContainText('starts paused');

    await result.getByRole('button', { name: 'Open workspace' }).click();
    await expect(page).toHaveURL(/\/workspaces\//);
    await expect(page.locator('#workspaceFollowupMount')).toContainText(
      'Order compost for the spring beds'
    );

    // Status: a real button naming its dialog, announcing portability. It
    // survives the Command view re-rendering the header it lives in.
    const chip = page.getByRole('button', { name: /^Portability:/ });
    await expect(chip).toBeVisible({ timeout: 20000 });
    await expect(chip).toContainText('Imported · routines off');
    await chip.click();
    const dialog = page.getByRole('dialog', { name: 'Moving this workspace' });
    await expect(dialog).toBeVisible();
    await expect(dialog).toContainText('Copies you make elsewhere cannot be erased remotely');
    // Choosing to turn routines on asks again, warning about the other copy;
    // leaving without confirming changes nothing.
    await dialog.getByRole('button', { name: 'Turn on background routines…' }).click();
    await expect(dialog).toContainText('does not stop them on the other computer');
    await expect(dialog.getByRole('button', { name: 'Turn on routines here' })).toBeFocused();
    await page.keyboard.press('Escape');
    await expect(dialog).toBeHidden();

    // Routines are still off: the server says so, not only the chip.
    const status = await page.evaluate(async () => {
      const id = document.body.dataset.workspaceId;
      const response = await fetch(`/api/workspaces/${id}/continuity`);
      return (await response.json()).continuity;
    });
    expect(status.imported).toBe(true);
    expect(status.background_allowed).toBe(false);
  });
});

test.describe('Older copies without a checkpoint', () => {
  test.skip(!legacyFolder, 'set LEGACY_CONTINUITY_FOLDER to a checkpoint-less copied HQ folder');

  test('explains what an older HQ copy lacks and continues with its assistant when chosen', async ({
    page
  }) => {
    await page.goto('/');
    await finishFirstRun(page);

    await page.getByRole('button', { name: 'Import Folder' }).first().click();
    const path = page.getByRole('textbox', { name: 'Folder path' });
    await path.fill(legacyFolder);
    await path.press('Tab');

    const notice = page.getByRole('region', { name: 'Older copy' });
    await expect(notice).toBeVisible({ timeout: 15000 });
    await expect(notice).toContainText(
      'conversations, follow-ups, Daily Briefs and uploaded files are not in it'
    );
    const choice = notice.getByRole('checkbox', {
      name: 'Continue with Ada as my personal assistant'
    });
    await expect(choice).toBeChecked();
    // An older copy uses the ordinary import button (setup review, then import).
    const action = page.locator('#createFolderBtn');
    await expect(action).toBeEnabled();
    await action.click();
    await expect(action).toHaveText('Import Folder', { timeout: 30000 });
    await action.click();
    await expect(page.getByText('Ada is your personal assistant again')).toBeVisible({
      timeout: 30000
    });

    const assistant = await page.evaluate(async () => {
      const response = await fetch('/api/personal-assistant');
      return (await response.json()).personal_assistant;
    });
    expect(assistant.state).toBe('paused');
    expect(assistant.display_name).toBe('Ada');
  });
});
