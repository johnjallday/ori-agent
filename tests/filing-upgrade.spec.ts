import { expect, test } from '@playwright/test';
import { mkdir, readFile, realpath, utimes, writeFile } from 'node:fs/promises';
import { basename, join, resolve } from 'node:path';
import { installLocalCdn } from './helpers/offline-cdn';

const sandbox = process.env.ORI_FILING_UPGRADE_SANDBOX || '';
const phase = process.env.ORI_FILING_UPGRADE_PHASE || '';
const evidence = resolve('tasks/evidence-filing-first-run/upgrade');

test('a configured dev Downloads workspace survives the Filing rename without asking again', async ({
  page,
  request
}) => {
  test.skip(
    !sandbox || !['seed', 'verify'].includes(phase),
    'requires an isolated two-build upgrade sandbox'
  );
  test.setTimeout(90_000);
  expect(basename(await realpath(sandbox)), 'disposable demo HOME').toMatch(/^ori-(e2e|demo)[.-]/);
  expect(await readFile(join(sandbox, '.ori-demo-owner'), 'utf8')).toBe(
    `worktree=${await realpath(process.cwd())}\n`
  );
  await installLocalCdn(page);
  await request.post('/api/onboarding/skip');
  const receiptPath = join(sandbox, 'filing-upgrade-receipt.json');
  const root = join(sandbox, 'Downloads');
  if (phase === 'seed') {
    await mkdir(root, { recursive: true, mode: 0o750 });
    const path = join(root, 'upgrade-invoice.pdf');
    await writeFile(path, 'synthetic upgrade fixture', { mode: 0o600 });
    const old = new Date(Date.now() - 3_600_000);
    await utimes(path, old, old);
    const created = await request.post('/api/workspaces', {
      data: {
        name: 'File Janitor — Downloads',
        template_id: 'file-janitor',
        create_template_agents: true
      }
    });
    expect(created.ok(), await created.text()).toBeTruthy();
    const workspace = (await created.json()).folder;
    const configured = await request.post(`/api/workspaces/${workspace.id}/file-janitor/setup`, {
      data: { path: root, paused: true }
    });
    expect(configured.ok(), await configured.text()).toBeTruthy();
    await page.goto(`/workspaces/${workspace.folder_slug}?view=details`);
    await expect(page.locator('#setupWizardDialog')).toBeVisible();
    await expect(page.locator('#setupWizardStepTitle')).toHaveText('Review what runs on its own');
    await page.locator('#setupWizardPrimary').click();
    await expect(page.locator('#setupWizardStepTitle')).toHaveText('Check everything is working');
    await page.locator('#setupWizardPrimary').click();
    await expect(page.locator('#setupWizardStepTitle')).toHaveText('File Janitor is ready');
    await page.locator('#setupWizardPrimary').click();
    await expect(page.locator('#setupWizardDialog')).toBeHidden();
    const settings = (
      await (await request.get(`/api/workspaces/${workspace.id}/file-janitor`)).json()
    ).status.settings;
    await writeFile(receiptPath, JSON.stringify({ workspace, settings }), { mode: 0o600 });
    return;
  }
  const saved = JSON.parse(await readFile(receiptPath, 'utf8'));
  const workspace = saved.workspace;
  const settings = (
    await (await request.get(`/api/workspaces/${workspace.id}/file-janitor`)).json()
  ).status.settings;
  for (const key of [
    'root_path',
    'root_id',
    'directory_reference_id',
    'setup_completed_at',
    'automation_approved_at'
  ])
    expect(settings[key], `preserve ${key}`).toBe(saved.settings[key]);
  const freshWorkspace = await (await request.get(`/api/workspaces/${workspace.id}`)).json();
  expect(freshWorkspace.name).toBe('File Janitor — Downloads');
  const grants: string[] = [];
  page.on('request', req => {
    if (
      req.method() === 'POST' &&
      /file-janitor\/(setup|prepare-review|setup-review)$/.test(req.url())
    )
      grants.push(req.url());
  });
  await page.goto(`/workspaces/${workspace.folder_slug}?view=details`);
  await expect(page.locator('#fileJanitorMount')).toBeVisible();
  await expect(page.locator('#fileJanitorMount .fj-card-title')).toHaveText('Filing');
  await expect(page.locator('#setupWizardDialog')).toBeHidden();
  await expect
    .poll(() => page.evaluate(() => (window as any).SetupWizard.getStatus()?.state))
    .toBe('ready');
  await expect(page.locator('#workspaceNextAction')).not.toHaveText(
    /Review permission|Set up filing/
  );
  await page.locator('#fileJanitorCardOpen').click();
  await expect(page.getByRole('dialog', { name: 'Filing', exact: true })).toBeVisible();
  await expect(page.getByRole('region', { name: 'Review Filing permissions' })).toHaveCount(0);
  expect(grants).toEqual([]);
  const scanned = await request.post(`/api/workspaces/${workspace.id}/file-janitor/scan`, {
    data: {}
  });
  expect(scanned.ok(), await scanned.text()).toBeTruthy();
  await page.evaluate(() => (window as any).FileJanitorConsole.refresh());
  await page.locator('#fileJanitorView-all').click();
  await expect(page.locator('#fileJanitorConsoleBody')).toContainText('upgrade-invoice.pdf');
  expect(await readFile(join(root, 'upgrade-invoice.pdf'), 'utf8')).toBe(
    'synthetic upgrade fixture'
  );
  await mkdir(evidence, { recursive: true, mode: 0o750 });
  await page.screenshot({
    path: join(evidence, 'existing-downloads-still-ready.png'),
    fullPage: true
  });
});
