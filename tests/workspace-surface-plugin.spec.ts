import { test, expect, type APIRequestContext } from '@playwright/test';
import { cpSync, mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';

const PLUGIN_NAME = 'workspace-surface-demo';
const SURFACE_KEY = 'plugin:workspace-surface-demo:demo-tools:main';

async function removeExample(request: APIRequestContext) {
  await request.delete(`/api/plugins/${PLUGIN_NAME}`).catch(() => {});
}

async function createWorkspace(request: APIRequestContext) {
  const response = await request.post('/api/workspaces', {
    data: {
      name: `Workspace Surface Browser ${Date.now().toString(36)}`,
      description: 'Disposable Workspace Surface browser journey'
    }
  });
  expect(response.ok(), await response.text()).toBeTruthy();
  const payload = await response.json();
  const record = payload.folder || payload.workspace || payload;
  const agentName = `Surface Demo Manager ${Date.now().toString(36)}`;
  const agent = await request.post('/api/agents', {
    data: { name: agentName, type: 'orchestration' }
  });
  expect(agent.ok(), await agent.text()).toBeTruthy();
  const attached = await request.post(`/api/workspaces/${record.id}/agents`, {
    data: { agent_name: agentName }
  });
  expect(attached.ok(), await attached.text()).toBeTruthy();
  return {
    id: record.id as string,
    slug: (record.folder_slug || record.slug || record.id) as string,
    agentName
  };
}

test.describe.configure({ mode: 'serial' });

test('local plugin install drives a generic station, frame, state, confirmation, and invalidation', async ({
  page,
  request
}) => {
  await page.request.post('/api/onboarding/skip').catch(() => {});
  await removeExample(request);

  const tempRoot = mkdtempSync(path.join(tmpdir(), 'ori-workspace-surface-example-'));
  const source = path.join(tempRoot, 'workspace-surface-demo');
  cpSync(path.resolve('examples/plugins/workspace-surface-demo'), source, { recursive: true });
  const evidenceDir = process.env.ORI_SURFACE_EVIDENCE_DIR;
  if (evidenceDir) mkdirSync(evidenceDir, { recursive: true });
  const capture = async (name: string) => {
    if (evidenceDir) await page.screenshot({ path: path.join(evidenceDir, name), fullPage: true });
  };
  let workspace: Awaited<ReturnType<typeof createWorkspace>> | undefined;

  try {
    const preview = await request.post('/api/plugins/install', {
      data: { source, confirm: false }
    });
    expect(preview.ok(), await preview.text()).toBeTruthy();
    const trust = (await preview.json()).trust;
    expect(trust.SurfaceCapabilities).toContain('demo-tools — Surface Demo');
    expect(trust.Surfaces?.[0]?.EntryAsset).toBe('ui/index.html');

    const install = await request.post('/api/plugins/install', {
      data: { source, confirm: true }
    });
    expect(install.ok(), await install.text()).toBeTruthy();
    const installed = (await install.json()).plugin;
    expect(installed.enabled).toBeFalsy();

    const enable = await request.post(`/api/plugins/${PLUGIN_NAME}/enable`);
    expect(enable.ok(), await enable.text()).toBeTruthy();
    workspace = await createWorkspace(request);

    const catalog = await request.get(`/api/workspaces/${workspace.id}/surfaces`);
    expect(catalog.ok(), await catalog.text()).toBeTruthy();
    expect((await catalog.json()).surfaces).toHaveLength(1);

    await page.goto(
      `/workspaces/${encodeURIComponent(workspace.slug)}?surface=${encodeURIComponent(SURFACE_KEY)}`
    );
    await expect(page.locator('.workspace-surface-modal')).toBeVisible({ timeout: 20_000 });
    let frame = page.frameLocator('iframe.workspace-surface-frame');
    await expect(frame.getByText('Bridge ready')).toBeVisible({ timeout: 15_000 });
    await expect(frame.locator('[data-visit-count]')).toHaveText('1');

    await frame.locator('#demo-name').fill('Browser');
    await frame.getByRole('button', { name: 'Invoke broker' }).click();
    await expect(frame.locator('[data-result]')).toHaveText('Hello, Browser.');

    await frame.getByRole('button', { name: 'Test rejection' }).click();
    await expect(frame.locator('[data-rejection]')).toContainText('operation_unknown');

    page.once('dialog', dialog => dialog.accept());
    await frame.getByRole('button', { name: 'Request confirmation' }).click();
    await expect(frame.locator('[data-confirmation-result]')).toHaveText(
      'Approved and completed once.'
    );
    await capture('group2-01-installed-open-operated.png');

    await page.locator('.workspace-surface-close').click();
    await expect(page.locator('.workspace-surface-modal')).toBeHidden();
    const station = page.locator(`[data-cmd-hq-station="${SURFACE_KEY}"]`).first();
    await station.click();
    await expect(page.locator('.workspace-surface-modal')).toBeVisible({ timeout: 20_000 });
    frame = page.frameLocator('iframe.workspace-surface-frame');
    await expect(frame.locator('[data-visit-count]')).toHaveText('2', { timeout: 15_000 });
    await capture('group2-02-state-survives-frame-reload.png');

    const disable = await request.post(`/api/plugins/${PLUGIN_NAME}/disable`);
    expect(disable.ok(), await disable.text()).toBeTruthy();
    await frame.getByRole('button', { name: 'Invoke broker' }).click();
    await expect(frame.locator('[data-result]')).toContainText('session_unknown');
    await page.reload();
    await expect(page.locator('.workspace-surface-modal')).toBeHidden();
    await page.getByRole('button', { name: /^map$/i }).click();
    await expect(page.locator(`[data-cmd-hq-station="${SURFACE_KEY}"]`)).toHaveCount(0);
    await capture('group2-03-disabled.png');

    const reenable = await request.post(`/api/plugins/${PLUGIN_NAME}/enable`);
    expect(reenable.ok(), await reenable.text()).toBeTruthy();
    await page.reload();
    await page.getByRole('button', { name: /^map$/i }).click();
    const restoredStation = page.locator(`[data-cmd-hq-station="${SURFACE_KEY}"]`).first();
    await expect(restoredStation).toContainText('Surface Demo', { timeout: 20_000 });
    await restoredStation.click();
    await expect(page.locator('.workspace-surface-modal')).toBeVisible({ timeout: 20_000 });
    frame = page.frameLocator('iframe.workspace-surface-frame');
    await expect(frame.locator('[data-visit-count]')).toHaveText('3', { timeout: 15_000 });
    await capture('group2-04-reenabled.png');

    const manifestPath = path.join(source, '.ori-plugin', 'plugin.json');
    const manifest = JSON.parse(readFileSync(manifestPath, 'utf8'));
    const protectedOperation = manifest.services[0].operations.find(
      (operation: { id: string }) => operation.id === 'setting.validate'
    );
    protectedOperation.scopes = ['plugin_data_write'];
    writeFileSync(manifestPath, JSON.stringify(manifest, null, 2) + '\n');

    const updatePreview = await request.post(`/api/plugins/${PLUGIN_NAME}/update`, {
      data: { confirm: false }
    });
    expect(updatePreview.ok(), await updatePreview.text()).toBeTruthy();
    const updateDisclosure = await updatePreview.json();
    expect(updateDisclosure.changed).toBeTruthy();
    expect(updateDisclosure.trust.SymbolicScopes).toContain('plugin_data_write');

    const update = await request.post(`/api/plugins/${PLUGIN_NAME}/update`, {
      data: { confirm: true }
    });
    expect(update.ok(), await update.text()).toBeTruthy();
    await frame.getByRole('button', { name: 'Invoke broker' }).click();
    await expect(frame.locator('[data-result]')).toContainText('session_unknown');
    await capture('group2-05-trust-changing-update-invalidates-frame.png');

    await page.reload();
    await page.getByRole('button', { name: /^map$/i }).click();
    const updatedStation = page.locator(`[data-cmd-hq-station="${SURFACE_KEY}"]`).first();
    await expect(updatedStation).toContainText('Surface Demo', { timeout: 20_000 });
    await updatedStation.click();
    await expect(page.locator('.workspace-surface-modal')).toBeVisible({ timeout: 20_000 });
    frame = page.frameLocator('iframe.workspace-surface-frame');
    await expect(frame.locator('[data-visit-count]')).toHaveText('4', { timeout: 15_000 });

    const uninstall = await request.delete(`/api/plugins/${PLUGIN_NAME}`);
    expect(uninstall.ok(), await uninstall.text()).toBeTruthy();
    await frame.getByRole('button', { name: 'Invoke broker' }).click();
    await expect(frame.locator('[data-result]')).toContainText('session_unknown');
    await page.reload();
    await expect(page.locator('.workspace-surface-modal')).toBeHidden();
    await expect(page.locator(`[data-cmd-hq-station="${SURFACE_KEY}"]`)).toHaveCount(0);
    await capture('group2-06-uninstalled-while-open.png');
  } finally {
    await removeExample(request);
    if (workspace) await request.delete(`/api/workspaces/${workspace.id}`).catch(() => {});
    rmSync(tempRoot, { recursive: true, force: true });
  }
});
