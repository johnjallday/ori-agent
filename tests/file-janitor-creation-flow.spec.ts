import { test, expect, type APIRequestContext } from '@playwright/test';
import {
  mkdtempSync,
  writeFileSync,
  existsSync,
  readFileSync,
  realpathSync,
  utimesSync
} from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

// Real-server acceptance. Only disposable local folders; no grant, settings,
// watcher, wizard or scan endpoint is stubbed. Native picker evidence is separate.
test.describe.configure({ mode: 'serial' });
const run = Date.now().toString(36);

function inbox() {
  const root = realpathSync(mkdtempSync(join(tmpdir(), 'ori-janitor-creation-')));
  const file = join(root, 'invoice.pdf');
  writeFileSync(file, 'synthetic source bytes', { mode: 0o600 });
  const old = new Date(Date.now() - 3600000);
  utimesSync(file, old, old);
  return root;
}

async function createLinked(request: APIRequestContext, label: string, roots: string[]) {
  const response = await request.post('/api/workspaces', {
    data: { name: `Janitor ${label} ${run}`, template_id: 'file-janitor' }
  });
  expect(response.ok(), await response.text()).toBeTruthy();
  const workspace = (await response.json()).folder;
  const refs: string[] = [];
  for (const root of roots) {
    const linked = await request.post(`/api/workspaces/${workspace.id}/directories`, {
      data: { name: 'Synthetic Inbox', path: root }
    });
    expect(linked.ok(), await linked.text()).toBeTruthy();
    refs.push((await linked.json()).directory.id);
  }
  return { id: workspace.id, slug: workspace.folder_slug, refs };
}

for (const monitoring of [false, true]) {
  test(`existing linked folder prepares a real first review with monitoring ${monitoring ? 'on' : 'off'}`, async ({
    page,
    request
  }, testInfo) => {
    await page.setViewportSize({ width: 1280, height: 900 });
    const root = inbox();
    const workspace = await createLinked(request, monitoring ? 'watch' : 'manual', [root]);
    let pickerCalls = 0;
    page.on('request', request => {
      if (request.url().includes('/api/folder-picker/')) pickerCalls++;
    });
    await page.goto(`/workspaces/${workspace.slug}?panel=file-janitor`);
    const review = page.locator('#setupWizardDialog[open] .fj-setup-review');
    await expect(review).toBeVisible();
    const disclosureBounds = await review.locator('.fj-disclosure').boundingBox();
    expect(disclosureBounds?.width).toBeGreaterThan(300);
    expect(disclosureBounds?.height).toBeLessThan(200);
    await expect(review.getByRole('checkbox')).not.toBeChecked();
    expect(existsSync(join(root, 'Filed'))).toBeFalsy();
    await review.getByText('Folder details', { exact: true }).click();
    await expect(review.locator('.fj-setup-path')).toHaveText(root);
    if (monitoring) {
      await review.getByRole('checkbox').check();
      await expect(review.getByRole('checkbox')).toBeChecked();
      await expect(review).toContainText('Daily scan at');
    }
    await page.screenshot({ path: testInfo.outputPath('permission-review.png') });
    await review.getByRole('button', { name: 'Use attached Synthetic Inbox', exact: true }).click();
    await expect(page.locator('#setupWizardDialog')).not.toHaveAttribute('open', '');
    await expect(page.locator('#fileJanitorConsole')).toBeVisible();
    await expect(page.locator('#fileJanitorConsoleBody')).toContainText('invoice.pdf');
    await expect(page.locator('.fj-batch-loading')).not.toBeVisible();
    await page.screenshot({ path: testInfo.outputPath('initial-review.png') });
    expect(pickerCalls).toBe(0);
    expect(readFileSync(join(root, 'invoice.pdf'), 'utf8')).toBe('synthetic source bytes');
    expect(existsSync(join(root, 'Filed'))).toBeTruthy();
    const statusResponse = await request.get(`/api/workspaces/${workspace.id}/file-janitor`);
    const status = (await statusResponse.json()).status;
    expect(status.settings.root_path).toBe(root);
    expect(Boolean(status.settings.paused)).toBe(!monitoring);
    expect(status.settings.setup_decision.mode).toBe(monitoring ? 'monitoring' : 'manual');
    const watcher = status.readiness.checks.find(
      (check: { component: string }) => check.component === 'watcher'
    );
    expect(watcher.status).toBe(monitoring ? 'ok' : 'pending');
    const batchResponse = await request.get(`/api/workspaces/${workspace.id}/file-janitor/batches`);
    const before = await batchResponse.json();
    expect(before.batches).toHaveLength(1);
    await page.reload();
    await expect(page.locator('#fileJanitorConsoleBody')).toContainText('invoice.pdf');
    await expect(page.locator('#setupWizardDialog')).not.toHaveAttribute('open', '');
    const after = await (
      await request.get(`/api/workspaces/${workspace.id}/file-janitor/batches`)
    ).json();
    expect(after.batches.map((batch: { id: string }) => batch.id)).toEqual(
      before.batches.map((batch: { id: string }) => batch.id)
    );
  });
}

test('same-named attachments require an explicit reference choice', async ({
  page,
  request
}, testInfo) => {
  await page.setViewportSize({ width: 1280, height: 900 });
  const roots = [inbox(), inbox()];
  const workspace = await createLinked(request, 'choose', roots);
  await page.goto(`/workspaces/${workspace.slug}?panel=file-janitor`);
  const review = page.locator('#setupWizardDialog[open] .fj-setup-review');
  await expect(review).toBeVisible();
  await expect(
    review.getByRole('button', { name: 'Use attached Synthetic Inbox', exact: true })
  ).toBeDisabled();
  await page.screenshot({ path: testInfo.outputPath('choose-attached-folder.png') });
  await review.getByRole('combobox', { name: 'Attached folder' }).selectOption(workspace.refs[1]);
  await expect(
    review.getByRole('button', { name: 'Use attached Synthetic Inbox', exact: true })
  ).toBeEnabled();
  await review.getByRole('button', { name: 'Use attached Synthetic Inbox', exact: true }).click();
  await expect(page.locator('#fileJanitorConsoleBody')).toContainText('invoice.pdf');
  expect(existsSync(join(roots[0], 'Filed'))).toBeFalsy();
  const status = (await (await request.get(`/api/workspaces/${workspace.id}/file-janitor`)).json())
    .status;
  expect(status.settings.root_path).toBe(roots[1]);
  expect(status.settings.directory_reference_id).toBe(workspace.refs[1]);
});
