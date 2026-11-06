import { expect, test } from '@playwright/test';
import { mkdirSync } from 'node:fs';
import { attachPlacementFolder, placementJSON } from './helpers/assisted-workspace-placement';

const evidence = 'tasks/evidence-reaper-folder-setup-wizard';

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
