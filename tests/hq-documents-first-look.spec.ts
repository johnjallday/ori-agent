import { expect, test } from '@playwright/test';
import { mkdir, readFile, realpath, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { freshGuidedHQ } from './helpers/fresh-guided-hq';
import { installLocalCdn } from './helpers/offline-cdn';
import { confirmReviewedCreation } from './helpers/confirm-reviewed-creation';
import { choiceInventory } from './helpers/workspace-choice-state';

// A split profile is essential: never authorize Documents under protected app data.
test('the manager offers a first look on Open; dismissal and return do not start work', async ({
  page,
  request
}, info) => {
  test.setTimeout(120_000);
  const sandbox = process.env.ORI_HQ_FIRST_VISIT_SANDBOX;
  test.skip(!sandbox || process.env.ORI_DEMO_SPLIT_PROFILE !== '1', 'requires fresh split sandbox');
  const base = await realpath(sandbox!);
  expect(base).toMatch(/\/ori-(?:demo|e2e)\.[^/]+$/);
  const home = join(base, 'home');
  const data = join(base, 'data');
  expect(
    (await (await request.get('/api/settings/workspace-root')).json()).default_workspace_root
  ).toBe(join(home, 'Ori Workspaces'));
  const hq = await freshGuidedHQ(request);
  const root = join(home, 'Documents');
  await mkdir(root, { mode: 0o750 });
  await writeFile(join(root, 'estimate.txt'), 'Synthetic estimate for cabinet order PO-4387.', {
    mode: 0o600
  });
  const before = await choiceInventory(request, data);
  const starts: string[] = [];
  const effects: string[] = [];
  page.on('request', r => {
    if (r.method() === 'GET') return;
    const path = new URL(r.url()).pathname;
    if (path.endsWith('/folder-manager-look')) starts.push(path);
    if (
      /organization-reviews|organization-file|\/file-search\/(sources|scan)|\/api\/chat$/.test(path)
    )
      effects.push(path);
  });
  await installLocalCdn(page);
  await page.goto(hq + '?mode=details');
  const suggestion = page.locator('[data-hq-firstvisit]');
  await suggestion.getByRole('button', { name: /Create a workspace from Documents/ }).click();
  await suggestion.getByRole('link', { name: 'Go to Home' }).click();
  await expect(page.locator('#cockpitCreateWorkspaceBtn')).toBeFocused();
  await page.locator('#cockpitCreateWorkspaceBtn').press('Enter');
  await expect(page.locator('#workspaceAnchorPath')).toHaveValue(root);
  await page.locator('#wizardNextBtn').click();
  await expect(page.locator('#workspaceRoleRoster img[src*="/research-archivist/"]')).toBeVisible();
  await page.locator('#wizardNextBtn').click();
  const review = page.locator('#workspaceCreationReviewPanel');
  await expect(review).toContainText('Opening the workspace does not start it');
  await confirmReviewedCreation(page);
  await review.getByRole('button', { name: 'Open workspace', exact: true }).click();
  const card = page.locator('#workspaceDocumentsLook');
  await expect(
    card.getByRole('heading', { name: 'Want help organizing Documents?' })
  ).toBeVisible();
  await expect(card).toContainText('Documents Manager');
  await expect(card.locator('img[src*="/research-archivist/"]')).toBeVisible();
  await expect(page.locator('#personalAssistantPanel')).toBeHidden();
  await card.screenshot({ path: info.outputPath('first-open.png') });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.emulateMedia({ reducedMotion: 'reduce', colorScheme: 'dark' });
  await card.screenshot({ path: info.outputPath('narrow-invite.png') });
  await card.getByRole('button', { name: 'Not now' }).press('Enter');
  const manual = card.getByRole('button', { name: 'Show Documents first look' });
  await expect(manual).toBeFocused();
  await page.reload();
  await expect(manual).toBeVisible();
  await manual.press('Enter');
  const action = card.getByRole('button', { name: 'Look around Documents' });
  await expect(action).toBeFocused();
  expect(starts).toEqual([]);
  expect(effects).toEqual([]);
  await action.press('Enter');
  const report = card.getByRole('button', { name: 'View report' });
  await expect(report).toBeVisible({ timeout: 60000 });
  expect(starts).toHaveLength(1);
  await page.reload();
  await expect(report).toBeVisible();
  await card.screenshot({ path: info.outputPath('narrow-return.png') });
  await page.setViewportSize({ width: 1280, height: 900 });
  await card.screenshot({ path: info.outputPath('desktop-return.png') });
  await report.click();
  const savedReport = page.locator('.folder-agent-card--report:visible');
  await expect(savedReport).toBeVisible();
  const suggestionButton = savedReport.getByRole('button', { name: 'Review filing suggestions' });
  await expect(suggestionButton).toBeVisible();
  await savedReport.screenshot({ path: info.outputPath('filing-recommendation.png') });
  await suggestionButton.click();
  const scope = savedReport.locator('.folder-agent-card__organization');
  await expect(scope).toContainText('Nothing has been read or sent.');
  await expect(scope.getByRole('checkbox', { name: 'estimate.txt' })).toBeChecked();
  await scope.screenshot({ path: info.outputPath('filing-read-boundary.png') });
  expect(starts).toHaveLength(1);
  expect(effects).toEqual([]);
  const after = await choiceInventory(request, data);
  expect(after.hires).toEqual(before.hires);
  expect(after.sources).toEqual(before.sources);
  expect(await readFile(join(root, 'estimate.txt'), 'utf8')).toBe(
    'Synthetic estimate for cabinet order PO-4387.'
  );
});
