import { test, expect, type APIRequestContext } from '@playwright/test';
import { cpSync, mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { installLocalCdn } from './helpers/offline-cdn';

const PLUGIN_NAME = 'workspace-surface-demo';
const SURFACE_KEY = 'plugin:workspace-surface-demo:demo-tools:main';

async function removeExample(request: APIRequestContext) {
  await request.delete(`/api/plugins/${PLUGIN_NAME}`).catch(() => {});
}

async function createWorkspace(request: APIRequestContext, templateId: string) {
  const response = await request.post('/api/workspaces', {
    data: {
      name: `Workspace Surface Browser ${Date.now().toString(36)}`,
      description: 'Disposable Workspace Surface browser journey',
      template_id: templateId,
      create_template_agents: true
    }
  });
  expect(response.ok(), await response.text()).toBeTruthy();
  const payload = await response.json();
  const record = payload.folder || payload.workspace || payload;
  expect(
    payload.project_warning,
    'plugin blueprint creation must not report normal-ready on partial setup'
  ).toBeUndefined();
  const loaded = await request.get(`/api/workspaces/${record.id}`);
  expect(loaded.ok(), await loaded.text()).toBeTruthy();
  const loadedPayload = await loaded.json();
  const workspace = loadedPayload.folder || loadedPayload.workspace || loadedPayload;
  const agent = workspace.agent_instances?.[0] || workspace.agents?.[0];
  expect(agent?.id, 'plugin blueprint should create its declared agent').toBeTruthy();
  return {
    id: record.id as string,
    slug: (record.folder_slug || record.slug || record.id) as string,
    agentId: agent.id as string
  };
}

test.describe.configure({ mode: 'serial' });

test('plugin blueprint drives capability setup, granted agent surface, and full lifecycle', async ({
  page,
  request
}) => {
  await installLocalCdn(page);
  await page.request.post('/api/onboarding/skip').catch(() => {});
  await removeExample(request);

  const tempRoot = mkdtempSync(path.join(tmpdir(), 'ori-workspace-surface-example-'));
  const source = path.join(tempRoot, 'workspace-surface-demo');
  cpSync(path.resolve('examples/plugins/workspace-surface-demo'), source, { recursive: true });
  // Augment only this disposable fixture copy with a template-owned dashboard.
  // The real scaffold/catalog/session/bridge path remains authoritative.
  const dashboardDir = path.join(source, 'blueprints/demo-workspace/project/dashboard');
  mkdirSync(dashboardDir, { recursive: true, mode: 0o750 });
  cpSync(
    path.resolve('docs/examples/workspace-dashboard/ori-bridge.js'),
    path.join(dashboardDir, 'ori-bridge.js')
  );
  writeFileSync(
    path.join(dashboardDir, 'index.html'),
    '<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Fixture dashboard</title><script src="ori-bridge.js" defer></script><script src="fixture.js" defer></script></head><body><h1>Fixture dashboard</h1><label>Dashboard draft <input id="draft"></label><p id="ready" role="status">Loading dashboard</p></body></html>',
    { mode: 0o600 }
  );
  writeFileSync(
    path.join(dashboardDir, 'fixture.js'),
    'Ori.whenReady().then(() => Ori.invoke("workspace.summary")).then(() => { document.getElementById("ready").textContent = "Dashboard bridge ready"; }).catch(error => { document.getElementById("ready").textContent = error.message; });',
    { mode: 0o600 }
  );
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
    const templateID = 'plugin:workspace-surface-demo:demo-workspace';
    const templates = await request.get('/api/project-templates');
    expect(templates.ok(), await templates.text()).toBeTruthy();
    expect(
      (await templates.json()).templates.some(
        (template: { id: string }) => template.id === templateID
      )
    ).toBeTruthy();

    workspace = await createWorkspace(request, templateID);
    const capabilityCatalog = await (
      await request.get(`/api/workspaces/${workspace.id}/capabilities`)
    ).json();
    const attachedCapability = capabilityCatalog.capabilities.find(
      (item: { definition: { id: string } }) => item.definition.id === 'demo-tools'
    );
    expect(attachedCapability.installed).toBeTruthy();
    expect(attachedCapability.record.owner.plugin_id).toBe(PLUGIN_NAME);

    const verify = await request.post(
      `/api/workspaces/${workspace.id}/runtime-capabilities/requirements/demo_runtime/verify`
    );
    expect(verify.ok(), await verify.text()).toBeTruthy();
    const grant = await request.post(
      `/api/workspaces/${workspace.id}/runtime-capabilities/requirements/demo_runtime/grants`,
      { data: { agent_instance_id: workspace.agentId } }
    );
    expect(grant.ok(), await grant.text()).toBeTruthy();
    await request.post(`/api/workspaces/${workspace.id}/setup-wizard/open`);
    const summary = await request.post(
      `/api/workspaces/${workspace.id}/setup-wizard/steps/summary/confirm`,
      { data: {} }
    );
    expect(summary.ok(), await summary.text()).toBeTruthy();
    const completeSetup = await request.post(
      `/api/workspaces/${workspace.id}/setup-wizard/complete`
    );
    expect(completeSetup.ok(), await completeSetup.text()).toBeTruthy();

    const catalog = await request.get(`/api/workspaces/${workspace.id}/surfaces`);
    expect(catalog.ok(), await catalog.text()).toBeTruthy();
    const declared = (await catalog.json()).surfaces;
    expect(declared.map((surface: { key: string }) => surface.key).sort()).toEqual(
      [SURFACE_KEY, 'user:ori.dashboard:dashboard:main'].sort()
    );

    await page.goto(
      `/workspaces/${encodeURIComponent(workspace.slug)}?mode=details&surface=${encodeURIComponent(SURFACE_KEY)}`
    );
    await expect(page.locator('.workspace-surface-modal')).toBeVisible({ timeout: 20_000 });
    const pluginFrame = () =>
      page.frameLocator('.workspace-surface-modal iframe.workspace-surface-frame');
    let frame = pluginFrame();
    await expect(frame.getByText('Bridge ready')).toBeVisible({ timeout: 15_000 });
    await expect(frame.locator('[data-visit-count]')).toHaveText('1');

    await frame.locator('#demo-name').fill('Browser');
    await frame.getByRole('button', { name: 'Invoke broker' }).click();
    await expect(frame.locator('[data-result]')).toHaveText('Hello, Browser.');

    await frame.getByRole('button', { name: 'Test rejection' }).click();
    await expect(frame.locator('[data-rejection]')).toContainText('operation_unknown');

    page.once('dialog', dialog => dialog.dismiss());
    await frame.getByRole('button', { name: 'Request confirmation' }).click();
    await expect(frame.locator('[data-confirmation-result]')).toContainText(
      /cancel|declin|denied/i
    );
    page.once('dialog', dialog => dialog.accept());
    await frame.getByRole('button', { name: 'Request confirmation' }).click();
    await expect(frame.locator('[data-confirmation-result]')).toHaveText(
      'Approved and completed once.'
    );
    await capture('group3-01-blueprint-setup-grant-operated.png');

    await page.locator('.workspace-surface-close').click();
    await expect(page.locator('.workspace-surface-modal')).toBeHidden();
    // Overview reaches the same operational console; refresh retains the live
    // iframe, bridge and unsaved console input rather than serializing its DOM.
    await page.mouse.wheel(0, 600);
    await page.locator(`.ws-cmd-capability-access [data-cmd-hq-station="${SURFACE_KEY}"]`).click();
    frame = pluginFrame();
    await expect(frame.locator('[data-visit-count]')).toHaveText('2');
    await frame.locator('#demo-name').fill('Keep console draft');
    const consoleNode = (await page
      .locator('.workspace-surface-modal iframe.workspace-surface-frame')
      .elementHandle())!;
    await page.evaluate(() => (window as any).workspaceDetail.loadWorkspace());
    expect(
      await page
        .locator('.workspace-surface-modal iframe.workspace-surface-frame')
        .evaluate((node, original) => node === original, consoleNode)
    ).toBe(true);
    await expect(frame.locator('#demo-name')).toHaveValue('Keep console draft');
    await capture('simplified-overview-console.png');
    await page.locator('.workspace-surface-close').click();

    await page.locator('[data-cmd-view-mode="dashboard"]').click();
    const inline = page.locator('[data-cmd-dashboard-host] iframe.workspace-surface-frame-inline');
    const dashboard = page.frameLocator('[data-cmd-dashboard-host] iframe.workspace-surface-frame-inline');
    await expect(dashboard.locator('#ready')).toHaveText('Dashboard bridge ready');
    await dashboard.locator('#draft').fill('Keep dashboard draft');
    const inlineNode = (await inline.elementHandle())!;
    await page.evaluate(() => (window as any).workspaceDetail.loadWorkspace());
    for (const mode of ['details', 'map', 'tickets', 'dashboard']) {
      await page.locator(`[data-cmd-view-mode="${mode}"]`).click();
      expect(await inline.evaluate((node, original) => node === original, inlineNode)).toBe(true);
    }
    await expect(dashboard.locator('#draft')).toHaveValue('Keep dashboard draft');
    await capture('simplified-dashboard-state.png');
    await page.getByRole('button', { name: /^map$/i }).click();
    const station = page.locator(`[data-cmd-hq-station="${SURFACE_KEY}"]`).first();
    await station.click();
    await expect(page.locator('.workspace-surface-modal')).toBeVisible({ timeout: 20_000 });
    frame = pluginFrame();
    await expect(frame.locator('[data-visit-count]')).toHaveText('3', { timeout: 15_000 });
    await capture('group3-02-state-survives-frame-reload.png');

    const disable = await request.post(`/api/plugins/${PLUGIN_NAME}/disable`);
    expect(disable.ok(), await disable.text()).toBeTruthy();
    const disabledCapabilities = await (
      await request.get(`/api/workspaces/${workspace.id}/capabilities`)
    ).json();
    const disabledRecord = disabledCapabilities.capabilities.find(
      (item: { definition: { id: string } }) => item.definition.id === 'demo-tools'
    );
    expect(disabledRecord.installed).toBeTruthy();
    expect(disabledRecord.available).toBeFalsy();
    await frame.getByRole('button', { name: 'Invoke broker' }).click();
    await expect(frame.locator('[data-result]')).toContainText('session_unknown');
    await page.reload();
    await expect(page.locator('.workspace-surface-modal')).toBeHidden();
    await page.getByRole('button', { name: /^map$/i }).click();
    await expect(page.locator(`[data-cmd-hq-station="${SURFACE_KEY}"]`)).toHaveCount(0);
    await capture('group3-03-disabled-station-removed.png');
    await page.goto(`/workspaces/${encodeURIComponent(workspace.slug)}?setup=1`);
    if (!(await page.locator('#setupWizardDialog').isVisible())) {
      await page.evaluate(() => {
        const setup = (
          window as typeof window & { SetupWizard?: { open?: (step?: string) => void } }
        ).SetupWizard;
        setup?.open?.('provider');
      });
    }
    await expect(page.locator('#setupWizardDialog')).toBeVisible({ timeout: 20_000 });
    await expect(page.locator('#setupWizardStepContent')).toContainText(
      "This workspace's capability provider is not available"
    );
    await capture('group3-04-disabled-provider-repair.png');
    await request.post(`/api/workspaces/${workspace.id}/setup-wizard/dismiss`);

    const reenable = await request.post(`/api/plugins/${PLUGIN_NAME}/enable`);
    expect(reenable.ok(), await reenable.text()).toBeTruthy();
    await page.goto(`/workspaces/${encodeURIComponent(workspace.slug)}`);
    await page.getByRole('button', { name: /^map$/i }).click();
    const restoredStation = page.locator(`[data-cmd-hq-station="${SURFACE_KEY}"]`).first();
    await expect(restoredStation).toContainText('Surface Demo', { timeout: 20_000 });
    await restoredStation.click();
    await expect(page.locator('.workspace-surface-modal')).toBeVisible({ timeout: 20_000 });
    frame = pluginFrame();
    await expect(frame.locator('[data-visit-count]')).toHaveText('4', { timeout: 15_000 });
    await capture('group3-05-reenabled.png');

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
    await capture('group3-06-trust-changing-update-invalidates-frame.png');

    // The surface owns its URL parameter: Command mode changes must preserve
    // it, so reload restores this open surface without a second station click.
    await page.reload();
    const updatedStation = page.locator(`[data-cmd-hq-station="${SURFACE_KEY}"]`).first();
    await expect(updatedStation).toContainText('Surface Demo', { timeout: 20_000 });
    await expect(page.locator('.workspace-surface-modal')).toBeVisible({ timeout: 20_000 });
    frame = pluginFrame();
    await expect(frame.locator('[data-visit-count]')).toHaveText('5', { timeout: 15_000 });

    const uninstall = await request.delete(`/api/plugins/${PLUGIN_NAME}`);
    expect(uninstall.ok(), await uninstall.text()).toBeTruthy();
    const removedCapabilities = await (
      await request.get(`/api/workspaces/${workspace.id}/capabilities`)
    ).json();
    const removedRecord = removedCapabilities.capabilities.find(
      (item: { definition: { id: string } }) => item.definition.id === 'demo-tools'
    );
    expect(removedRecord.installed).toBeTruthy();
    expect(removedRecord.available).toBeFalsy();
    await frame.getByRole('button', { name: 'Invoke broker' }).click();
    await expect(frame.locator('[data-result]')).toContainText('session_unknown');
    await page.reload();
    await expect(page.locator('.workspace-surface-modal')).toBeHidden();
    await expect(page.locator(`[data-cmd-hq-station="${SURFACE_KEY}"]`)).toHaveCount(0);
    await capture('group3-07-uninstalled-while-open.png');
  } finally {
    await removeExample(request);
    if (workspace) await request.delete(`/api/workspaces/${workspace.id}`).catch(() => {});
    rmSync(tempRoot, { recursive: true, force: true });
  }
});
