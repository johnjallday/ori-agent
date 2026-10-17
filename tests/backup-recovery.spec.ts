import { test, expect, type Page } from '@playwright/test';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { installLocalCdn } from './helpers/offline-cdn';

/** Prototype browser checks of the actual compiled console assets. All HTTP
 * data below is invented; no server, provider, Vault or drive is contacted.
 * These do NOT replace creator/domain/live-provider acceptance (2.11/5.3). */
const origin = 'http://127.0.0.1:8931';
const root = resolve(import.meta.dirname, '..');
async function prototype(page: Page, configured = false) {
  const mutations: Array<{ path: string; body: Record<string, unknown> }> = [];
  const status = {
    generation: configured ? 'invented-generation' : '',
    revision: configured ? 1 : 0,
    sequence: configured ? 1 : 0,
    server_time: '2026-10-17T00:00:00Z',
    connection: configured ? 'configured' : 'not_configured',
    runtime_available: true,
    monitoring: 'manual_only',
    destination: configured ? 'present' : 'not_checked',
    snapshot_count: configured ? 0 : null,
    last_successful_read: configured ? '2026-10-17T00:00:00Z' : null
  };
  await page.route(origin + '/**', async route => {
    const url = new URL(route.request().url());
    if (url.pathname === '/fixture')
      return route.fulfill({
        contentType: 'text/html',
        body: `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Backup console prototype</title><meta name="viewport" content="width=device-width,initial-scale=1"><link rel="stylesheet" href="/css/backup-recovery.css"><style>body{font-family:system-ui;margin:24px;background:#f5f7f6} @media(prefers-color-scheme:dark){body{background:#152126;--text-primary:#e6eeee;--text-secondary:#b0c2c7;--border-color:#40535b;--bg-secondary:#1c2b31;--surface-1:#1c2b31}}</style></head><body><main><h1>Prototype demo · invented evidence</h1><div id="backupRecoveryMount"></div></main><script>window.currentWorkspaceId='invented-workspace';window.WorkspaceCapabilities={isInstalled:()=>true,load:async()=>{},registerOpenHandler:()=>{}};</script><script defer src="/js/modules/backup-recovery-console.js"></script></body></html>`
      });
    if (url.pathname.startsWith('/css/'))
      return route.fulfill({
        contentType: 'text/css',
        body: readFileSync(resolve(root, 'internal/web/static/css/backup-recovery.css'), 'utf8')
      });
    if (url.pathname.startsWith('/js/'))
      return route.fulfill({
        contentType: 'application/javascript',
        body: readFileSync(
          resolve(root, 'internal/web/static/js/modules/backup-recovery-console.js'),
          'utf8'
        )
      });
    const path = url.pathname.replace('/api/workspaces/invented-workspace/backup-recovery', '');
    const body = route.request().postDataJSON() || {};
    if (route.request().method() !== 'GET') mutations.push({ path, body });
    let data: Record<string, unknown> = {};
    if (path === '/settings')
      data = {
        settings: {
          generation: status.generation,
          revision: status.revision,
          configured: status.connection === 'configured',
          picker_available: true,
          source_paths: configured ? ['/Invented/Project'] : []
        }
      };
    else if (path === '/guide')
      data = {
        guide: {
          boundary: 'External execution only. This is invented prototype evidence.',
          prerequisites: ['Official restic 0.19.1; configure outside Ori.'],
          steps: [],
          sources: []
        }
      };
    else if (path === '/history') data = { history: { items: [], next_cursor: 0 } };
    else if (path === '/selection')
      data = { selection: { selected: true, token: 'opaque-' + body.kind } };
    else if (path === '/credential')
      data = {
        credential: {
          vault_id: body.vault_id,
          record_id: body.record_id,
          record_revision: 'a'.repeat(64)
        }
      };
    else if (path === '/review')
      data = {
        review: {
          generation: status.generation,
          revision: status.revision,
          review_token: 'invented-review',
          expires_at: '2026-10-17T00:05:00Z',
          source_paths: ['/Invented/Project'],
          volume_path: '/Volumes/Invented',
          repository_path: '/Volumes/Invented/Repository'
        }
      };
    else if (path === '/connect') {
      status.connection = 'configured';
      status.generation = 'invented-generation';
      status.revision = 1;
      status.snapshot_count = 0;
      status.last_successful_read = status.server_time;
      data = {
        operation: {
          generation: status.generation,
          revision: 1,
          state: 'complete',
          outcome: 'read_ok'
        }
      };
    } else data = { status };
    return route.fulfill({
      contentType: 'application/json',
      body: JSON.stringify({ success: true, ...data })
    });
  });
  await page.goto(origin + '/fixture');
  await expect(page.getByRole('button', { name: 'Open backup evidence' })).toBeVisible();
  return mutations;
}

test('prototype: opening all tabs and reloading stored evidence never starts a check', async ({
  page
}) => {
  const mutations = await prototype(page, true);
  await page.getByRole('button', { name: 'Open backup evidence' }).click();
  const dialog = page.locator('#backupRecoveryConsole');
  await expect(dialog).toBeVisible();
  await expect(dialog).toContainText('None observed');
  for (const tab of ['History', 'Recovery', 'Settings', 'Overview'])
    await dialog.getByRole('tab', { name: tab, exact: true }).click();
  expect(mutations).toHaveLength(0);
  await expect(dialog).not.toContainText('all files protected');
  await page.keyboard.press('Escape');
  await expect(dialog).not.toBeVisible();
  await expect(page.getByRole('button', { name: 'Open backup evidence' })).toBeFocused();
});

test('prototype: explicit choice → review → separate unchecked consent → one read', async ({
  page
}) => {
  const mutations = await prototype(page);
  await page.getByRole('button', { name: 'Open backup evidence' }).click();
  const dialog = page.locator('#backupRecoveryConsole');
  await dialog.getByRole('tab', { name: 'Settings', exact: true }).click();
  for (const label of [
    'Choose source folder',
    'Choose external volume root',
    'Choose existing repository'
  ])
    await dialog.getByRole('button', { name: label, exact: true }).click();
  await dialog.getByLabel('Selected Vault ID — not a password').fill('invented-vault');
  await dialog.getByLabel('Selected workspace secret record ID').fill('invented-record');
  await dialog.getByRole('button', { name: 'Describe selected reference' }).click();
  await dialog.getByRole('button', { name: 'Review metadata access', exact: true }).click();
  const consent = dialog.getByLabel(
    'Allow one bounded metadata read of this exact selection. Do not run a backup or restore.',
    { exact: true }
  );
  await expect(consent).not.toBeChecked();
  await expect(dialog.getByRole('button', { name: 'Approve one metadata read' })).toBeDisabled();
  expect(mutations.filter(call => call.path === '/connect')).toHaveLength(0);
  await consent.check();
  await dialog.getByRole('button', { name: 'Approve one metadata read' }).click();
  await expect(dialog).toContainText('Metadata observed. This is not proof');
  expect(mutations.filter(call => call.path === '/connect')).toHaveLength(1);
  for (const mutation of mutations)
    expect(JSON.stringify(mutation.body)).not.toContain('/Volumes/');
});

test('prototype: narrow dark view keeps tabs and paths reachable without horizontal overflow', async ({
  page
}) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.emulateMedia({ colorScheme: 'dark', reducedMotion: 'reduce' });
  await prototype(page, true);
  await page.getByRole('button', { name: 'Open backup evidence' }).click();
  const dialog = page.locator('#backupRecoveryConsole');
  await dialog.getByRole('tab', { name: 'Overview', exact: true }).focus();
  await page.keyboard.press('ArrowRight');
  await expect(dialog.getByRole('tab', { name: 'History', exact: true })).toHaveAttribute(
    'aria-selected',
    'true'
  );
  const bounds = await dialog.boundingBox();
  expect(bounds).not.toBeNull();
  expect(bounds!.width).toBeLessThanOrEqual(390);
  expect(await dialog.evaluate(node => node.scrollWidth <= node.clientWidth)).toBeTruthy();
  await page.screenshot({
    path: test.info().outputPath('prototype-backup-narrow-dark.png'),
    fullPage: true
  });
});

/** Real current server/creator/wizard/domain/console. Only CDN assets are served
 * locally; no product API is intercepted. Run on an isolated missing-tool demo.
 * Native choices and real provider acceptance are separately labelled in 2.11. */
test('live server: reviewed one-advisor creation and missing-tool setup work without a model', async ({
  page,
  request
}, testInfo) => {
  test.setTimeout(90000);
  await installLocalCdn(page);
  expect((await request.post('/api/onboarding/skip')).ok()).toBeTruthy();
  await page.goto('/workspaces');
  await page
    .locator('#launcherCreateWorkspaceBtn:visible, #cockpitCreateWorkspaceBtn:visible')
    .first()
    .click();
  const creator = page.locator('#addFolderModal');
  await expect(creator).toBeVisible();
  await creator.locator('[data-template-id="backup-and-recovery"]').first().click();
  await page.locator('#wizardNextBtn').click();
  const name = 'Backup browser ' + Date.now().toString(36);
  await page.locator('#folderNameInput').fill(name);
  await page.locator('#wizardNextBtn').click();
  const advisor = page.locator('#workspaceRoleRoster [data-role-id="backup-advisor"]');
  await expect(advisor).toBeVisible();
  await advisor.getByRole('button', { name: /^Create an agent for / }).click();
  await expect(page.locator('#addAgentModal')).toBeVisible();
  await page.locator('#createAgentBtn').click();
  await expect(page.locator('#addAgentModal')).toBeHidden();
  await expect(advisor).not.toContainText('Required · Missing');
  await page.locator('#wizardNextBtn').click();
  await expect(page.locator('#wizardStep4')).toBeVisible();
  await expect(page.locator('#workspaceSetupPreview')).toContainText(
    'Review the metadata connection'
  );
  await page.screenshot({
    path: testInfo.outputPath('live-no-model-create-review.png'),
    fullPage: true
  });
  const created = page.waitForResponse(
    response =>
      response.request().method() === 'POST' &&
      new URL(response.url()).pathname === '/api/workspaces'
  );
  await page.locator('#createFolderBtn').click();
  // Map creation has one final explicit placement confirmation after Review.
  await page.getByRole('button', { name: 'Create workspace here', exact: true }).click();
  const response = await created;
  expect(response.status(), await response.text()).toBe(201);
  const body = await response.json();
  const workspace = body.folder || body.workspace;
  expect(workspace.id).toBeTruthy();
  const id = workspace.id as string;
  const path = '/workspaces/' + encodeURIComponent(workspace.folder_slug || id);
  await page.goto(path + '?mode=details');
  const wizard = page.locator('#setupWizardDialog');
  if (!(await wizard.isVisible().catch(() => false)))
    await page.locator('#setupWizardBannerAction').click();
  await expect(wizard).toBeVisible();
  await expect(page.locator('#setupWizardStepTitle')).toHaveText('Review the metadata connection');
  await expect(wizard).toContainText('unavailable');
  await page.screenshot({
    path: testInfo.outputPath('live-missing-tool-wizard.png'),
    fullPage: true
  });
  await page.keyboard.press('Escape');
  await expect(wizard).toBeHidden();
  const mutations: string[] = [];
  page.on('request', request => {
    if (request.method() !== 'GET' && new URL(request.url()).pathname.includes('/backup-recovery'))
      mutations.push(request.url());
  });
  const card = page.locator('#backupRecoveryMount');
  await expect(card).toBeVisible();
  await card.getByRole('button', { name: 'Open backup evidence' }).click();
  const console = page.locator('#backupRecoveryConsole');
  await expect(console).toBeVisible();
  await expect(console).toContainText('Reader unavailable');
  await expect(console.getByRole('button', { name: 'Check metadata now' })).toBeDisabled();
  await console.getByRole('tab', { name: 'Settings', exact: true }).click();
  await console.getByText('External setup guide · no model required', { exact: true }).click();
  await expect(console).toContainText('0.19.1');
  await expect(console).toContainText('outside Ori');
  await page.screenshot({
    path: testInfo.outputPath('live-missing-tool-settings-guide.png'),
    fullPage: true
  });
  for (const tab of ['History', 'Recovery', 'Overview'])
    await console.getByRole('tab', { name: tab, exact: true }).click();
  expect(mutations).toEqual([]);
  const status = await request.get('/api/workspaces/' + id + '/backup-recovery');
  expect(status.ok(), await status.text()).toBeTruthy();
  expect((await status.json()).status).toMatchObject({
    connection: 'not_configured',
    runtime_available: false,
    monitoring: 'manual_only'
  });
  const stored = await request.get('/api/workspaces/' + id + '/setup-wizard');
  expect(stored.ok(), await stored.text()).toBeTruthy();
  expect(JSON.stringify(await stored.json())).not.toContain('"state":"complete"');
  await console.getByRole('button', { name: 'Close', exact: true }).click();
  await page.goto(path + '?mode=details&panel=backup-recovery&tab=recovery');
  await expect(console).toBeVisible();
  await expect(console.getByRole('tab', { name: 'Recovery', exact: true })).toHaveAttribute(
    'aria-selected',
    'true'
  );
  await page.screenshot({
    path: testInfo.outputPath('live-model-free-recovery.png'),
    fullPage: true
  });
});
