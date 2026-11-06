import { expect, test } from '@playwright/test';
import { mkdirSync } from 'node:fs';
import { attachPlacementFolder, placementJSON } from './helpers/assisted-workspace-placement';

import { musicHomeSnapshot, musicHomeResourceCounts } from './helpers/music-home-placement';

const evidence = 'tasks/evidence-reaper-folder-setup-wizard';

test('music wizard opens only on review, preserves the creator and closes without effects before automatic setup', async ({
  page,
  request
}) => {
  const song = process.env.MUSIC_HOME_PLACEMENT_SONG;
  test.skip(!song, 'requires a fresh isolated real-candidate music sandbox');
  test.setTimeout(120_000);
  mkdirSync(`${evidence}/wizard`, { recursive: true });
  const before = await musicHomeSnapshot(request);
  let setupPosts = 0;
  let inference = 0;
  page.on('request', request => {
    if (request.method() !== 'POST') return;
    if (/folder-digest\/offers\/[^/]+\/setup$/.test(request.url())) setupPosts++;
    if (/\/(turns|chat|completions|from-inbox)$/.test(request.url())) inference++;
  });
  await page.goto('/?panel=today&folder=show');
  await page.locator('.folder-path-entry__input:visible').first().fill(song!);
  await page.getByRole('button', { name: 'Use this path', exact: true }).first().click();
  const entry = page
    .locator('[data-source-setup-summary]')
    .getByRole('button', { name: 'Review setup', exact: true });
  await expect(entry).toBeVisible();
  const dialog = page.locator('#addFolderModal');
  await expect(dialog).toBeHidden();
  expect(await musicHomeSnapshot(request)).toEqual(before);
  // An explicit ordinary editor already using the shared shell must win. Its
  // original state survives even an external source-entry event while it is up.
  await page.evaluate(() =>
    (window as any).sessionManager.showAddWorkspaceModal({
      entryPoint: 'wizard-regression',
      name: 'Keep my draft'
    })
  );
  await expect(dialog).toBeVisible();
  await entry.dispatchEvent('click');
  await expect(dialog).not.toHaveAttribute('data-source-setup-owner');
  await expect(page.locator('[data-source-setup-summary]')).toContainText('choices are unchanged');
  expect(
    await page.evaluate(() => (window as any).sessionManager.workspaceCreatorContext.name)
  ).toBe('Keep my draft');
  await dialog.getByRole('button', { name: 'Close create workspace', exact: true }).click();
  await expect(dialog).toBeHidden();
  await entry.click();
  await expect(dialog).toHaveAttribute('data-source-setup-owner', 'music-folder');
  await expect(
    dialog.getByRole('heading', { name: 'Review music folder setup', exact: true })
  ).toBeFocused();
  const review = dialog.locator('[data-source-creation-review]');
  await expect(review).toBeVisible();
  await expect(page.locator('#personalAssistantPanel [data-source-creation-review]')).toHaveCount(
    0
  );
  await expect(review).toContainText('Ori Music Demo');
  await page.screenshot({ path: `${evidence}/wizard/review.png` });
  await review.getByRole('button', { name: 'Use automatic placement', exact: true }).click();
  await page.keyboard.press('Escape');
  await expect(dialog).toBeHidden();
  await expect(entry).toBeFocused();
  expect(setupPosts).toBe(0);
  expect(inference).toBe(0);
  expect(await musicHomeSnapshot(request)).toEqual(before);
  await entry.click();
  await expect(
    review.getByRole('button', { name: 'Use automatic placement', exact: true })
  ).toHaveAttribute('aria-pressed', 'true');
  // Repeated source entry is presentation only and cannot repeat confirmation.
  await entry.dispatchEvent('click');
  await expect(page.locator('.modal.show')).toHaveCount(1);
  await review.getByRole('button', { name: 'Set up Home', exact: true }).click();
  await expect(review.getByRole('heading', { name: 'Home ready', exact: true })).toBeVisible({
    timeout: 60_000
  });
  expect(setupPosts).toBe(1);
  expect(inference).toBe(0);
  const after = await musicHomeSnapshot(request);
  const homes = after.workspaces.filter((workspace: any) => workspace.kind === 'group');
  expect(homes).toHaveLength(1);
  expect(after.workspaces.length).toBe(before.workspaces.length + 1);
  expect(
    after.workspaces.filter((workspace: any) => workspace.parent_id === homes[0].id)
  ).toHaveLength(0);
  expect((await musicHomeResourceCounts(request, homes[0].id)).projects).toHaveLength(1);
  await page.screenshot({ path: `${evidence}/wizard/result.png` });
});

test('Downloads review uses the shared dialog without creating resources', async ({ page }) => {
  test.skip(!process.env.ORI_INBOX_SANDBOX, 'requires isolated demo-inbox-folder.sh state');
  mkdirSync(`${evidence}/baseline-downloads`, { recursive: true });
  const before = await placementJSON(page, '/api/workspaces');
  const agents = await placementJSON(page, '/api/agents');
  let setups = 0;
  page.on('request', request => {
    if (
      request.method() === 'POST' &&
      /\/(setup|janitor-prepare|confirm|turns)$/.test(request.url())
    )
      setups++;
  });
  await attachPlacementFolder(page, 'Downloads');
  await expect(page.locator('#addFolderModal')).toBeHidden();
  await page.getByRole('button', { name: 'Review setup', exact: true }).click();
  const dialog = page.locator('#addFolderModal');
  await expect(dialog).toBeVisible();
  const review = dialog.locator('#assistedWorkspacePlacementPanel');
  await expect(review.getByLabel('Workspace name')).toHaveValue('Downloads');
  await review.getByRole('button', { name: 'Use automatic placement', exact: true }).click();
  await expect(
    review.getByRole('button', { name: 'Create workspace & scan', exact: true })
  ).toBeEnabled();
  expect(await placementJSON(page, '/api/workspaces')).toEqual(before);
  expect(await placementJSON(page, '/api/agents')).toEqual(agents);
  expect(setups).toBe(0);
  await page.screenshot({ path: `${evidence}/baseline-downloads/review.png` });
  await review.getByRole('button', { name: 'Cancel draft', exact: true }).click();
  await expect(dialog).toBeHidden();
  expect(await placementJSON(page, '/api/workspaces')).toEqual(before);
  expect(await placementJSON(page, '/api/agents')).toEqual(agents);
  expect(setups).toBe(0);
});
