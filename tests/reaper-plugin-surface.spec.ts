import { test, expect } from '@playwright/test';
import { mkdirSync } from 'node:fs';
import path from 'node:path';

const pluginPath = process.env.ORI_REAPER_PLUGIN_PATH;
const pluginName = 'reaper-plugin';
const templateID = 'plugin:reaper-plugin:reaper-song';
const surfaceKey = 'plugin:reaper-plugin:reaper-live-control:live-control';

test.skip(
  !pluginPath,
  'set ORI_REAPER_PLUGIN_PATH to the locally built coordinated plugin checkout'
);

test('plugin-backed Reaper Song reaches generic setup, surface, action, script, and agent declarations', async ({
  page,
  request
}) => {
  await request.delete(`/api/plugins/${pluginName}`).catch(() => {});
  const evidenceDir = process.env.ORI_REAPER_EVIDENCE_DIR;
  if (evidenceDir) mkdirSync(evidenceDir, { recursive: true });
  const capture = async (name: string) => {
    if (evidenceDir) await page.screenshot({ path: path.join(evidenceDir, name), fullPage: true });
  };
  const legacyRequests: string[] = [];
  const frameErrors: string[] = [];
  page.on('pageerror', error => frameErrors.push(error.message));
  page.on('request', request => {
    if (/\/api\/workspaces\/[^/]+\/reaper(?:\/|$|-setup)/.test(request.url())) {
      legacyRequests.push(request.url());
    }
  });

  const install = await request.post('/api/plugins/install', {
    data: { source: pluginPath, confirm: true }
  });
  expect(install.ok(), await install.text()).toBeTruthy();
  const enable = await request.post(`/api/plugins/${pluginName}/enable`);
  expect(enable.ok(), await enable.text()).toBeTruthy();

  const templates = await request.get('/api/project-templates');
  expect(templates.ok(), await templates.text()).toBeTruthy();
  expect(
    (await templates.json()).templates.some((item: { id: string }) => item.id === templateID)
  ).toBeTruthy();

  const create = await request.post('/api/workspaces', {
    data: {
      name: `Plugin REAPER ${Date.now().toString(36)}`,
      description: 'Disposable plugin-backed REAPER fixture',
      template_id: templateID,
      create_template_agents: true
    }
  });
  expect(create.ok(), await create.text()).toBeTruthy();
  const created = await create.json();
  const workspace = created.folder;
  expect(created.project_warning).toBeUndefined();

  try {
    const setup = await request.get(`/api/workspaces/${workspace.id}/setup-wizard`);
    expect(setup.ok(), await setup.text()).toBeTruthy();
    const setupPayload = (await setup.json()).setup;
    expect(setupPayload.applicable).toBeTruthy();
    expect(
      setupPayload.steps.some(
        (step: { runtime_requirement_key?: string }) =>
          step.runtime_requirement_key === 'reaper_live_control'
      )
    ).toBeTruthy();
    await request.post(`/api/workspaces/${workspace.id}/setup-wizard/open`);
    await request.post(`/api/workspaces/${workspace.id}/setup-wizard/dismiss`);

    const catalog = await request.get(`/api/workspaces/${workspace.id}/surfaces`);
    expect(catalog.ok(), await catalog.text()).toBeTruthy();
    const surfaces = (await catalog.json()).surfaces;
    expect(surfaces).toHaveLength(1);
    expect(surfaces[0].key).toBe(surfaceKey);

    await page.goto(
      `/workspaces/${encodeURIComponent(workspace.folder_slug)}?surface=${encodeURIComponent(surfaceKey)}`
    );
    await expect(page.locator('.workspace-surface-modal')).toBeVisible({ timeout: 20_000 });
    const frame = page.frameLocator('iframe.workspace-surface-frame');
    await expect(frame.getByRole('heading', { name: 'Live Control' })).toBeVisible({
      timeout: 15_000
    });
    await expect(frame.locator('[data-status]')).not.toHaveText('Connecting', { timeout: 15_000 });
    await expect(frame.locator('[data-actions] .action').first()).toBeVisible({ timeout: 15_000 });
    await expect(frame.locator('[data-scripts]')).toBeVisible();
    await capture('group4-01-live-control.png');

    await frame.getByRole('button', { name: 'Play', exact: true }).click();
    await expect(frame.locator('[data-action-result]')).not.toHaveText('No action run.');
    await frame.getByRole('button', { name: 'Stop', exact: true }).click();
    await capture('group4-02-transport-action.png');

    const frameURL = await page.locator('iframe.workspace-surface-frame').getAttribute('src');
    const appResponse = await request.get(String(frameURL).replace(/index\.html$/, 'app.js'));
    expect(await appResponse.text()).toContain('data-script-form');
    const scriptStamp = Date.now().toString(36);
    const scriptFilename = `ori-plugin-demo-${scriptStamp}.lua`;
    const scriptName = `Ori plugin demo ${scriptStamp}`;
    await frame.locator('#script-filename').fill(scriptFilename);
    await frame.locator('#script-name').fill(scriptName);
    await frame.locator('#script-code').fill("reaper.ShowConsoleMsg('Ori plugin script OK\\n')");
    page.once('dialog', dialog => dialog.accept());
    await frame.locator('[data-script-form]').evaluate(form => {
      form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
    });
    expect(frameErrors).toEqual([]);
    await expect(frame.locator('[data-script-result]')).toContainText(
      'saved to the global library'
    );
    const scriptRow = frame.locator('.script-row').filter({ hasText: scriptName });
    await expect(scriptRow).toBeVisible();
    page.once('dialog', dialog => dialog.accept());
    await scriptRow.getByRole('button', { name: 'Run' }).click();
    await expect(frame.locator('[data-action-result]')).toContainText(/ok|completed|failed/i);
    await capture('group4-03-script-test-save.png');
    page.once('dialog', dialog => dialog.accept());
    await scriptRow.getByRole('button', { name: 'Delete' }).click();
    await expect(frame.locator('[data-script-result]')).toContainText('deleted');

    await frame.locator('#draft-code').fill("reaper.ShowConsoleMsg('Ori plugin fixture')");
    await frame.getByRole('button', { name: 'Validate' }).click();
    await expect(frame.locator('[data-draft-result]')).toContainText('within the runner limit');

    const plugin = (await (await request.get('/api/plugins')).json()).plugins.find(
      (item: { name: string }) => item.name === pluginName
    );
    expect(plugin.workspace_surfaces.capabilities[0].agent_operations).toContain('state.read');
    expect(plugin.workspace_surfaces.capabilities[0].agent_operations).toContain('draft.run');
    expect(legacyRequests).toEqual([]);
  } finally {
    await request.delete(`/api/plugins/${pluginName}`).catch(() => {});
    await request.delete(`/api/workspaces/${workspace.id}`).catch(() => {});
  }
});
