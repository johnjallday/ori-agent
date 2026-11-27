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
  const managerURL = page.url();
  await panel
    .getByRole('article', { name: 'School/Archived History' })
    .getByRole('button', { name: 'Ignore this folder' })
    .click();
  await expect(panel).not.toContainText('Archived History');
  await discover();
  await expect(panel).toContainText('BIO 101/Syllabus.txt');
  await expect(panel).not.toContainText('Archived History');
  const review = panel.getByRole('button', { name: 'Review setup' });
  await review.click();
  await panel.screenshot({ path: join(evidence, 'review.png') });
  await panel.getByRole('button', { name: 'Cancel' }).click();
  const count = async () =>
    ((await (await request.get('/api/workspaces')).json()).workspaces || []).filter(
      (w: { name: string }) => w.name === 'BIO 101'
    ).length;
  const courseOpen = async () => {
    await review.click();
    const response = page.waitForResponse(
      r => r.url().endsWith('/documents-projects') && r.request().postDataJSON()?.action === 'open'
    );
    await panel.getByRole('button', { name: 'Open course workspace' }).click();
    const result = await response;
    expect(result.status()).toBe(200);
    await page.waitForURL('**/workspaces/bio-101?setup=1');
  };
  expect(await count()).toBe(0);
  const live = process.env.ORI_DISCOVERY_MODEL;
  if (live)
    expect(
      (
        await request.post('/api/settings/system-model', {
          data: { provider: 'claude', model: live }
        })
      ).ok()
    ).toBeTruthy();
  await courseOpen();
  await expect(page.locator('#setupWizardDialog')).toBeVisible();
  await page.goto(managerURL);
  await discover();
  await courseOpen();
  expect(await count()).toBe(1);
  const wizard = page.locator('#setupWizardDialog');
  const file = wizard.getByLabel('Choose your syllabus');
  const selected = join(school, 'BIO 101', 'Syllabus.txt');
  const uploads: string[] = [];
  page.on('request', r => {
    if (r.url().endsWith('/sources/files')) uploads.push(r.url());
  });
  await file.setInputFiles(selected);
  await expect(wizard.getByRole('button', { name: 'Read selected syllabus' })).toBeDisabled();
  await wizard.getByRole('button', { name: 'Not now', exact: true }).click();
  await expect(wizard).toContainText('No sources added yet.');
  expect(uploads).toEqual([]);
  if (!live) return;
  await file.setInputFiles(selected);
  await expect(wizard.locator('.blueprint-intake-consent-statement')).toContainText(
    /claude|anthropic/i
  );
  await wizard.screenshot({ path: join(evidence, 'consent.png') });
  await wizard.getByRole('checkbox', { name: 'I understand and agree.' }).check();
  await wizard.getByRole('button', { name: 'Read selected syllabus' }).click();
  await expect(wizard).toContainText('Parsed');
  await wizard.locator('#setupWizardPrimary').click();
  await wizard.getByRole('button', { name: 'Trust and enable' }).click();
  await wizard.getByRole('button', { name: 'Run intake' }).click();
  await expect(
    wizard
      .getByRole('heading', { name: 'Next stated deadline' })
      .or(wizard.locator('.blueprint-intake-run-error'))
  ).toBeVisible({ timeout: 120000 });
  await expect(wizard.locator('.blueprint-intake-run-error')).toHaveCount(0);
  await expect(wizard.locator('blockquote').first()).toContainText('December 14, 2027');
  await expect(wizard).toContainText('Dates marked TBD remain unknown.');
  await wizard.screenshot({ path: join(evidence, 'answer.png') });
});
