import { test, expect } from '@playwright/test';
import { existsSync, readFileSync, writeFileSync, utimesSync } from 'node:fs';
import { join } from 'node:path';
import { installLocalCdn } from './helpers/offline-cdn';

// A fresh scripts/demo-inbox-folder.sh sandbox, no provider/model. This uses the
// current deterministic inbox-hire owner, NOT a declined legacy build proposal.
const sandbox = process.env.ORI_INBOX_SANDBOX || '';
test.skip(!sandbox, 'Requires the isolated demo-inbox-folder.sh runtime');
test.setTimeout(120_000);

test('Updates creation: attached folder, reviewed setup, quiet Home discovery, exposure and complete filing', async ({
  page,
  request
}, testInfo) => {
  await installLocalCdn(page);
  const root = join(sandbox, 'Desktop');
  const source = join(root, 'invoice.pdf');
  expect(existsSync(join(root, 'Filed'))).toBe(false);
  writeFileSync(source, 'synthetic source bytes', { mode: 0o600 });
  const old = new Date(Date.now() - 3600000);
  utimesSync(source, old, old);
  const before = (await (await request.get('/api/workspaces')).json()).folders;
  await page.goto('/');
  await page.waitForFunction(() => Boolean((window as any).PersonalAssistantPanel?.close));
  await page.evaluate(() => (window as any).PersonalAssistantPanel.close());
  await page.locator('#cockpitPrimaryActionBtn').click();
  await page.locator('.personal-assistant-panel__chip', { hasText: 'Desktop' }).first().click();
  const panel = page.locator('#personalAssistantPanel');
  await expect(panel.locator('.folder-visual__agent strong')).toHaveText('File Curator');
  await expect(panel.getByRole('checkbox', { name: /Also watch this folder/ })).not.toBeChecked();
  expect(existsSync(join(root, 'Filed'))).toBe(false);
  await page.getByRole('button', { name: 'Use automatic placement', exact: true }).click();
  const review = page.locator('#assistedWorkspacePlacementPanel');
  const confirm = review.getByRole('button', { name: 'Create workspace & scan', exact: true });
  await expect(confirm).toBeEnabled();
  expect((await (await request.get('/api/workspaces')).json()).folders.length).toBe(before.length);
  await confirm.click();
  await expect(review.getByRole('heading', { name: 'Workspace prepared' })).toBeVisible({
    timeout: 60_000
  });
  await review.getByRole('button', { name: 'Close', exact: true }).click();
  const created = (await (await request.get('/api/workspaces')).json()).folders.filter(
    (row: { id: string }) => !before.some((old: { id: string }) => old.id === row.id)
  );
  expect(created).toHaveLength(1);
  const id = created[0].id;
  const update = async () => {
    const response = await request.get(`/api/workspaces/${id}/updates`);
    expect(response.ok(), await response.text()).toBe(true);
    return response.json();
  };
  await expect
    .poll(async () => (await update()).welcome.unread_count, { timeout: 45_000 })
    .toBeGreaterThan(0);
  expect((await update()).review.pending_count).toBe(1);
  // The user already opened the Assistant to attach a folder. Delivery must not
  // replace that conversation with the welcome, nor count it as exposure.
  const saved = (await update()).welcome;
  await expect(
    panel.locator(
      `[data-session-id="${saved.session_id}"][data-message-id="${saved.messages[0].message_id}"]`
    )
  ).toHaveCount(0);
  const readiness = (await (await request.get(`/api/workspaces/${id}/file-janitor`)).json()).status;
  expect(readiness.settings.setup_decision.mode).toBe('manual');
  expect(readiness.settings.paused).toBe(true);
  expect(readFileSync(source, 'utf8')).toBe('synthetic source bytes');
  await page.locator('#personalAssistantClose').click();
  const operations: string[] = [];
  page.on('request', req => {
    const path = new URL(req.url()).pathname;
    if (
      req.method() !== 'GET' &&
      /\/file-janitor\/(?:setup|scan|prepare-review|decisions|preview|apply|undo)$|^\/api\/chat$|\/tasks\/execute$/.test(
        path
      )
    )
      operations.push(path);
  });
  await page.goto('/');
  await expect(panel).toBeHidden();
  await page.getByRole('button', { name: 'Show Updates', exact: true }).click();
  const row = page.locator(`[data-update-row="${id}"]`);
  await expect(row).toContainText('New message');
  await expect(row).toContainText('1 file to review');
  await page.screenshot({ path: testInfo.outputPath('created-home-updates.png'), fullPage: true });
  await row.locator('[data-workspace-update="welcome"]').click();
  await expect(panel).toBeVisible();
  await page.locator('#personalAssistantScroll').hover();
  await page.mouse.wheel(0, -1200);
  await expect.poll(async () => (await update()).welcome.unread_count).toBe(0);
  expect((await update()).review.pending_count).toBe(1);
  await page.screenshot({
    path: testInfo.outputPath('created-exposed-review-remains.png'),
    fullPage: true
  });
  await page.locator('#personalAssistantClose').click();
  await page
    .locator('#workspaceNextActionRegion')
    .getByRole('button', { name: 'Review files', exact: true })
    .click();
  await expect(page.locator('#fileJanitorConsoleBody')).toContainText('invoice.pdf');
  expect(operations).toEqual([]);
  const history = await (await request.get(`/api/sessions/${saved.session_id}`)).json();
  await page
    .locator('#fileJanitorConsoleBody .fj-row-item')
    .filter({ hasText: 'invoice.pdf' })
    .locator('.fj-select')
    .check();
  await page.locator('#fileJanitorApprove').click();
  await expect(page.locator('#fileJanitorConfirmApply')).toBeVisible();
  const applied = page.waitForResponse(
    response =>
      response.request().method() === 'POST' &&
      new URL(response.url()).pathname === `/api/workspaces/${id}/file-janitor/apply`
  );
  await page.locator('#fileJanitorConfirmApply').click();
  const appliedResponse = await applied;
  expect(appliedResponse.ok(), await appliedResponse.text()).toBe(true);
  await expect
    .poll(async () => {
      const review = (await update()).review;
      return review.state === 'ready' ? review.pending_count : null;
    })
    .toBe(0);
  expect(operations).toEqual([
    `/api/workspaces/${id}/file-janitor/preview`,
    `/api/workspaces/${id}/file-janitor/apply`
  ]);
  expect(readFileSync(join(root, 'Filed', 'Documents', 'invoice.pdf'), 'utf8')).toBe(
    'synthetic source bytes'
  );
  await page.goto('/');
  await page.getByRole('button', { name: 'Show Updates', exact: true }).click();
  await expect(page.locator(`[data-update-row="${id}"]`)).toHaveCount(0);
  expect((await update()).welcome.unread_count).toBe(0);
  expect((await (await request.get(`/api/sessions/${saved.session_id}`)).json()).messages).toEqual(
    history.messages
  );
  await page.screenshot({
    path: testInfo.outputPath('created-return-resolved.png'),
    fullPage: true
  });
  writeFileSync(
    testInfo.outputPath('created-update-operations.json'),
    JSON.stringify(
      {
        runtime: 'disposable inbox demo; no model configured',
        approved_setup: 'Create workspace & scan; monitoring off',
        navigation_execution: [],
        explicit_file_operations: operations,
        welcome_history_unchanged: true,
        unread_remaining: 0,
        pending_files: 0
      },
      null,
      2
    ),
    { mode: 0o600 }
  );
});
