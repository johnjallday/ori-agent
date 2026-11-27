import { expect, test } from '@playwright/test';
import { mkdir, writeFile, realpath } from 'node:fs/promises';
import { join } from 'node:path';
import { freshGuidedHQ } from './helpers/fresh-guided-hq';
import { installLocalCdn } from './helpers/offline-cdn';
import { confirmReviewedCreation } from './helpers/confirm-reviewed-creation';

const evidence = 'tasks/evidence-documents-project-discovery';

test('find a BIO 101 lab deadline from Documents', async ({ page, request }) => {
  const sandbox = process.env.ORI_DISCOVERY_SANDBOX;
  test.skip(
    !sandbox || process.env.ORI_DEMO_SPLIT_PROFILE !== '1',
    'requires a fresh split profile'
  );
  test.setTimeout(180000);
  const home = join(await realpath(sandbox!), 'home');
  expect(
    (await (await request.get('/api/settings/workspace-root')).json()).default_workspace_root
  ).toBe(join(home, 'Ori Workspaces'));
  const hq = await freshGuidedHQ(request);
  const school = join(home, 'Documents', 'School');
  for (const name of ['BIO 101', 'Archived History']) {
    await mkdir(join(school, name, 'Labs'), { recursive: true });
    await writeFile(
      join(school, name, 'Syllabus.txt'),
      name === 'BIO 101'
        ? 'BIO 101 syllabus\nLab report due December 14, 2027.\nFinal presentation: TBD.\n'
        : 'Historical syllabus.'
    );
  }
  await writeFile(join(school, 'Shopping.txt'), 'Unrelated groceries.');
  await installLocalCdn(page);
  await page.goto(hq + '?mode=details');
  const suggestion = page.locator('[data-hq-firstvisit]');
  await suggestion.getByRole('button', { name: /Create a workspace from Documents/ }).click();
  await suggestion.getByRole('link', { name: 'Go to Home' }).click();
  await expect(page.locator('#cockpitCreateWorkspaceBtn')).toBeFocused();
  await page.locator('#cockpitCreateWorkspaceBtn').press('Enter');
  await expect(page.locator('#workspaceAnchorPath')).toHaveValue(join(home, 'Documents'));
  await page.locator('#wizardNextBtn').click();
  await page.locator('#wizardNextBtn').click();
  await confirmReviewedCreation(page);
  await page.locator('#toastContainer').getByRole('button', { name: 'Open workspace' }).click();
  const manager = page.locator('#workspaceDocumentsLook');
  await expect(manager).toContainText('Documents Manager');
  const discover = async () => {
    await manager.getByRole('button', { name: 'Find projects in this folder' }).click();
    await manager
      .getByLabel('Choose a folder in Documents')
      .selectOption({ label: 'Documents/School' });
    await manager.getByRole('button', { name: 'Look for projects' }).click();
  };
  await discover();
  const panel = manager.getByRole('region', { name: 'Project discovery' });
  await expect(panel).toContainText('BIO 101/Syllabus.txt');
  await expect(panel).toContainText('Archived History/Labs/');
  await expect(panel).not.toContainText('Shopping');
  await panel.screenshot({ path: join(evidence, 'suggestions.png') });
});
