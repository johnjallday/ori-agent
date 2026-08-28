import { test, expect } from '@playwright/test';
import { chmodSync, mkdirSync, readFileSync, statSync, writeFileSync } from 'node:fs';
import path from 'node:path';

const pluginPath = process.env.ORI_REAPER_PLUGIN_PATH;
const pluginName = 'reaper-plugin';
const templateID = 'plugin:reaper-plugin:reaper-song';
const tidySurfaceKey = 'plugin:reaper-plugin:reaper-live-control:project-tidy';

test.skip(!pluginPath, 'set ORI_REAPER_PLUGIN_PATH to the coordinated plugin worktree');

function writeProposal(
  projectRoot: string,
  projectEntry: string,
  id: string,
  createdAt: string,
  line: string
) {
  const tidyRoot = path.join(projectRoot, 'tidy');
  mkdirSync(tidyRoot, { recursive: true, mode: 0o750 });
  const reason = 'conventions: vocals = rgb(55, 126, 230)';
  const itemID = `color-${id}`;
  const plan = {
    schema_version: 1,
    plan_id: id,
    inspected_project: {
      name: path.basename(projectEntry),
      path: projectEntry,
      project_change_count: 1
    },
    items: [
      {
        id: itemID,
        verb: 'set_track_color',
        target: { track_guid: '{DB7F23BB-38B6-3844-8A87-22FD017EE05B}' },
        payload: { color: { red: 55, green: 126, blue: 230 } },
        reason
      }
    ]
  };
  const record = {
    schema_version: 1,
    proposal_id: id,
    created_at: createdAt,
    status: 'open',
    already_tidy: false,
    plan_file: `plan-${id}.json`,
    summary_file: `proposal-${id}.md`,
    items: [{ item_id: itemID, line, reason }]
  };
  const files: Array<[string, string]> = [
    [`plan-${id}.json`, JSON.stringify(plan, null, 2) + '\n'],
    [`proposal-${id}.md`, `# REAPER tidy-up proposal\n\n- [ ] ${line}\n`],
    [`proposal-${id}.state.json`, JSON.stringify(record, null, 2) + '\n']
  ];
  for (const [name, contents] of files) {
    const target = path.join(tidyRoot, name);
    writeFileSync(target, contents, { mode: 0o600 });
    chmodSync(target, 0o600);
  }
}

test('project-entry tidy panel reviews, filters, dismisses, and supersedes fixture proposals', async ({
  page,
  request
}) => {
  await request.delete(`/api/plugins/${pluginName}`).catch(() => {});
  const install = await request.post('/api/plugins/install', {
    data: { source: pluginPath, confirm: true }
  });
  expect(install.ok(), await install.text()).toBeTruthy();
  expect((await request.post(`/api/plugins/${pluginName}/enable`)).ok()).toBeTruthy();

  const create = await request.post('/api/workspaces', {
    data: {
      name: `Tidy Surface ${Date.now().toString(36)}`,
      description: 'Disposable Project Tidy browser fixture',
      template_id: templateID,
      create_template_agents: true
    }
  });
  expect(create.ok(), await create.text()).toBeTruthy();
  const workspace = (await create.json()).folder;
  const primary = workspace.directory_references.find(
    (item: { id: string }) => item.id === workspace.shared_data.primary_directory_id
  );
  const projectRoot = primary.path as string;
  const projectEntry = path.join(projectRoot, `${path.basename(projectRoot)}.rpp`);
  const projectMtime = statSync(projectEntry).mtimeMs;
  expect(readFileSync(path.join(projectRoot, 'conventions.md'), 'utf8')).toContain(
    'ori-reaper-tidy-conventions:1'
  );

  writeProposal(
    projectRoot,
    projectEntry,
    'proposal-001',
    '2026-08-28T12:00:00Z',
    'Track "Vox 2" — no color → rgb(55, 126, 230) — conventions: vocals are blue'
  );

  try {
    await page.goto(`/workspaces/${encodeURIComponent(workspace.folder_slug)}`);
    const action = page.locator(`[data-cmd-project-action="${tidySurfaceKey}"]`);
    await expect(action).toBeVisible({ timeout: 20_000 });
    await expect(action).toBeDisabled();
    await expect(page.locator(`[data-cmd-project-setup="${tidySurfaceKey}"]`)).toBeVisible();

    await page.route(/\/api\/workspaces\/[^/]+\/surfaces$/, async route => {
      const response = await route.fetch();
      const payload = await response.json();
      payload.surfaces = payload.surfaces.map((surface: { key: string; status: object }) =>
        surface.key === tidySurfaceKey
          ? {
              ...surface,
              status: {
                state: 'ready',
                value: '1 proposal',
                description: 'A tidy-up proposal is ready to review.'
              }
            }
          : surface
      );
      await route.fulfill({ response, json: payload });
    });
    await page.reload();
    await expect(page.locator(`[data-cmd-project-action="${tidySurfaceKey}"]`)).toContainText(
      '1 proposal'
    );
    const evidenceDir = process.env.ORI_REAPER_EVIDENCE_DIR;
    if (evidenceDir) {
      mkdirSync(evidenceDir, { recursive: true });
      await page.screenshot({
        path: path.join(evidenceDir, 'group3-02-project-entry-badge.png'),
        fullPage: true
      });
    }

    await page.goto(
      `/workspaces/${encodeURIComponent(workspace.folder_slug)}?surface=${encodeURIComponent(tidySurfaceKey)}`
    );
    await expect(page.locator('.workspace-surface-modal')).toBeVisible({ timeout: 20_000 });
    const frame = page.frameLocator('iframe.workspace-surface-frame');
    await expect(frame.getByRole('heading', { name: 'Project Tidy' })).toBeVisible({
      timeout: 15_000
    });
    await expect(frame.locator('.proposal-item')).toHaveCount(1, { timeout: 15_000 });
    await expect(frame.getByRole('button', { name: 'Apply selected' })).toBeEnabled();
    await expect(frame.locator('body')).not.toContainText('track_guid');

    if (evidenceDir) {
      mkdirSync(evidenceDir, { recursive: true });
      await page.screenshot({
        path: path.join(evidenceDir, 'group3-02-project-tidy-panel.png'),
        fullPage: true
      });
    }

    await frame.locator('.proposal-item input').uncheck();
    await expect(frame.getByRole('button', { name: 'Apply selected' })).toBeDisabled();
    await frame.getByRole('button', { name: 'Dismiss' }).click();
    await expect(frame.locator('[data-outcome]')).toContainText('REAPER was not changed');

    writeProposal(
      projectRoot,
      projectEntry,
      'proposal-002',
      '2026-08-28T12:01:00Z',
      'Marker 4 — "chorus" → "Chorus" — conventions: Title Case'
    );
    await expect(frame.locator('.proposal-item')).toContainText('Marker 4', { timeout: 10_000 });
    await expect(frame.getByRole('button', { name: 'Apply selected' })).toBeEnabled();
    if (evidenceDir) {
      await page.screenshot({
        path: path.join(evidenceDir, 'group3-03-project-tidy-superseded.png'),
        fullPage: true
      });
    }
    expect(statSync(projectEntry).mtimeMs).toBe(projectMtime);
  } finally {
    await request.delete(`/api/plugins/${pluginName}`).catch(() => {});
    await request.delete(`/api/workspaces/${workspace.id}`).catch(() => {});
  }
});
