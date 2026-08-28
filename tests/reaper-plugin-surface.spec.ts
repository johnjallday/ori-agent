import { test, expect } from '@playwright/test';
import { mkdirSync, readFileSync } from 'node:fs';
import path from 'node:path';

const pluginPath = process.env.ORI_REAPER_PLUGIN_PATH;
const pluginName = 'reaper-plugin';
const templateID = 'plugin:reaper-plugin:reaper-song';
const surfaceKey = 'plugin:reaper-plugin:reaper-live-control:live-control';
const tidySurfaceKey = 'plugin:reaper-plugin:reaper-live-control:project-tidy';

test.skip(
  !pluginPath,
  'set ORI_REAPER_PLUGIN_PATH to the locally built coordinated plugin checkout'
);

test('plugin-backed Reaper Song reaches generic setup, surface, action, script, and agent declarations', async ({
  page,
  request
}) => {
  await request.delete(`/api/plugins/${pluginName}`).catch(() => {});
  const legacyCreate = await request.post('/api/workspaces', {
    data: {
      name: `Legacy REAPER ${Date.now().toString(36)}`,
      description: 'Disposable pre-plugin legacy fixture',
      template_id: 'writing-project',
      create_template_agents: false
    }
  });
  expect(legacyCreate.ok(), await legacyCreate.text()).toBeTruthy();
  const legacy = (await legacyCreate.json()).folder;
  const legacyPrimary = legacy.directory_references.find(
    (item: { id: string }) => item.id === legacy.shared_data.primary_directory_id
  );
  const legacyProject = path.join(legacyPrimary.path, 'outline.md');
  const legacyProjectBytes = readFileSync(legacyProject);
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
    const legacyAfterInstall = await request.get(`/api/workspaces/${legacy.id}`);
    expect(legacyAfterInstall.ok(), await legacyAfterInstall.text()).toBeTruthy();
    expect(
      (await legacyAfterInstall.json()).installed_capabilities?.some(
        (item: { owner?: { plugin_id?: string } }) => item.owner?.plugin_id === pluginName
      ) || false
    ).toBeFalsy();
    expect(readFileSync(legacyProject)).toEqual(legacyProjectBytes);
    const legacyCatalog = await request.get(`/api/workspaces/${legacy.id}/surfaces`);
    expect(legacyCatalog.ok(), await legacyCatalog.text()).toBeTruthy();
    expect((await legacyCatalog.json()).surfaces).toEqual([]);
    await page.goto(`/workspaces/${encodeURIComponent(legacy.folder_slug)}`);
    await expect(page.getByRole('heading', { name: legacy.name }).first()).toBeVisible({
      timeout: 15_000
    });
    await capture('group5-00-legacy-no-auto-attachment.png');

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
    expect(surfaces).toHaveLength(2);
    expect(surfaces.map((surface: { key: string }) => surface.key)).toEqual(
      expect.arrayContaining([surfaceKey, tidySurfaceKey])
    );
    const tidySurface = surfaces.find((surface: { key: string }) => surface.key === tidySurfaceKey);
    expect(tidySurface.placement).toBe('project_entry');
    expect(tidySurface.features.create_task).toBeTruthy();
    expect(tidySurface.status.state).toBe('disabled');

    const primary = workspace.directory_references.find(
      (item: { id: string }) => item.id === workspace.shared_data.primary_directory_id
    );
    expect(readFileSync(path.join(primary.path, 'conventions.md'), 'utf8')).toContain(
      'ori-reaper-tidy-conventions:1'
    );

    await page.goto(`/workspaces/${encodeURIComponent(workspace.folder_slug)}`);
    const tidyAction = page.locator(`[data-cmd-project-action="${tidySurfaceKey}"]`);
    await expect(tidyAction).toBeVisible({ timeout: 15_000 });
    await expect(tidyAction).toBeDisabled();
    await expect(tidyAction).toHaveAttribute('title', /required live-control mode/i);
    await expect(page.locator(`[data-cmd-project-setup="${tidySurfaceKey}"]`)).toBeVisible();
    await capture('group3-01-project-entry-disabled.png');

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
    const firstChip = frame.locator('[data-prompt-chips] button').first();
    await expect(firstChip).toBeVisible();
    const chipText = await firstChip.textContent();
    await firstChip.click();
    await expect(frame.locator('[data-ask-input]')).toHaveValue(chipText || '');
    await capture('group4-01-live-control.png');

    await frame.getByRole('button', { name: 'Play', exact: true }).click();
    await expect(frame.locator('[data-action-result]')).toContainText('Play');
    await frame.getByRole('button', { name: 'Stop', exact: true }).click();
    await expect(frame.locator('[data-action-result]')).toContainText('Stop');
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

    if (process.env.ORI_REAPER_LIVE === '1') {
      const opened = await request.post(
        `/api/workspaces/${workspace.id}/surfaces/${encodeURIComponent(surfaceKey)}/sessions`
      );
      expect(opened.ok(), await opened.text()).toBeTruthy();
      const session = (await opened.json()).session as string;
      const invoke = async (operation_id: string, input: object, confirm = false) => {
        const first = await request.post('/api/workspace-surfaces/operations', {
          data: { session, operation_id, input }
        });
        if (!confirm) {
          expect(first.ok(), `${operation_id}: ${await first.text()}`).toBeTruthy();
          return (await first.json()).output;
        }
        expect(first.status(), `${operation_id}: ${await first.text()}`).toBe(409);
        const confirmation_id = (await first.json()).confirmation_id as string;
        const approved = await request.post('/api/workspace-surfaces/confirmations', {
          data: { session, confirmation_id }
        });
        expect(approved.ok(), await approved.text()).toBeTruthy();
        const confirmation_token = (await approved.json()).confirmation_token as string;
        const completed = await request.post('/api/workspace-surfaces/operations', {
          data: { session, operation_id, input, confirmation_token }
        });
        expect(completed.ok(), `${operation_id}: ${await completed.text()}`).toBeTruthy();
        return (await completed.json()).output;
      };

      let state = await invoke('state.read', {});
      for (let attempt = 0; attempt < 10 && state.tracks.length === 0; attempt += 1) {
        await page.waitForTimeout(250);
        state = await invoke('state.read', {});
      }
      expect(state.connected).toBeTruthy();
      expect(state.tracks.length).toBeGreaterThan(0);
      expect(JSON.stringify(state)).not.toMatch(/\.ori-reaper|inbox\.lua|\/Users\//i);
      expect(JSON.stringify(state)).not.toMatch(/"(?:port|endpoint|runner_root|receipt_path)"/i);
      const liveTrackRow = frame.locator('[data-track-index]').last();
      await liveTrackRow
        .locator('select[data-track-operation="color"]')
        .selectOption({ label: 'Gold' });
      await expect(frame.locator('[data-action-result]')).toContainText(/applied/i);
      await frame.getByRole('button', { name: 'Undo edit' }).click();
      await expect(frame.locator('[data-action-result]')).toContainText(/undone/i);
      const track = state.tracks[state.tracks.length - 1];
      const renamed = `Ori broker parity ${Date.now().toString(36)}`;
      expect(
        (
          await invoke('tracks.edit', {
            edit: {
              operation: 'rename',
              index: track.index,
              expected_name: track.name,
              new_name: renamed
            }
          })
        ).outcome
      ).toBe('applied');
      expect((await invoke('tracks.undo', {})).outcome).toBe('undone');
      const guarded = await invoke('tracks.edit', {
        edit: {
          operation: 'mute',
          index: track.index,
          expected_name: `${track.name}-stale`,
          new_bool: !track.muted
        }
      });
      expect(guarded.outcome).not.toBe('applied');

      const inertPlan = await invoke('plans.propose', {
        edits: [
          {
            operation: 'mute',
            index: track.index,
            expected_name: track.name,
            new_bool: !track.muted
          }
        ]
      });
      expect(inertPlan.outcome).toBe('pending');
      expect((await invoke('plans.current', {})).plan.id).toBe(inertPlan.plan.id);
      await expect(frame.locator('[data-plan-panel]')).toBeVisible({ timeout: 5_000 });
      await capture('group5-02-live-plan-review.png');
      await frame.getByRole('button', { name: 'Cancel plan' }).click();
      await expect(frame.locator('[data-plan-panel]')).toBeHidden();
      expect((await invoke('plans.current', {})).outcome).toBe('none');

      const planName = `Ori plan parity ${Date.now().toString(36)}`;
      const livePlan = await invoke('plans.propose', {
        edits: [
          {
            operation: 'rename',
            index: track.index,
            expected_name: track.name,
            new_name: planName
          }
        ]
      });
      expect((await invoke('plans.apply', { plan_id: livePlan.plan.id }, true)).outcome).toBe(
        'applied'
      );
      await invoke('actions.run_safe', { action_id: '40029' });

      const proposalFilename = `ori-broker-parity-${Date.now().toString(36)}.lua`;
      const proposal = await invoke('proposals.propose', {
        filename: proposalFilename,
        name: 'Ori broker parity',
        description: 'Disposable live broker proposal',
        code: 'local _ = reaper.CountTracks(0)',
        needs_confirmation: true
      });
      expect(proposal.outcome).toBe('pending');
      await expect(frame.locator('[data-proposal-panel]')).toBeVisible({ timeout: 5_000 });
      await expect(frame.locator('[data-proposal-code]')).toContainText('CountTracks');
      await capture('group5-03-live-script-proposal.png');
      expect(
        (await invoke('proposals.test', { proposal_id: proposal.proposal.id }, true)).proposal
          .tested
      ).toBeTruthy();
      expect(
        (await invoke('proposals.save', { proposal_id: proposal.proposal.id }, true)).outcome
      ).toBe('saved');
      await invoke('scripts.delete', { id: proposalFilename }, true);

      const discarded = await invoke('proposals.propose', {
        filename: `ori-broker-discard-${Date.now().toString(36)}.lua`,
        name: 'Discarded broker proposal',
        code: 'return 1'
      });
      expect(
        (await invoke('proposals.discard', { proposal_id: discarded.proposal.id })).outcome
      ).toBe('discarded');

      const pinState = await request.post('/api/workspace-surfaces/state', {
        data: { session, action: 'get', key: 'pinned_actions' }
      });
      expect(pinState.ok(), await pinState.text()).toBeTruthy();
      const currentPins = await pinState.json();
      const emptied = await request.post('/api/workspace-surfaces/state', {
        data: {
          session,
          action: 'set',
          key: 'pinned_actions',
          schema_version: 1,
          expected_revision: currentPins.revision,
          value: { ids: [] }
        }
      });
      expect(emptied.ok(), await emptied.text()).toBeTruthy();
      expect((await emptied.json()).value.ids).toEqual([]);
      await page.reload();
      await expect(page.locator('.workspace-surface-modal')).toBeVisible({ timeout: 20_000 });
      await expect(
        page.frameLocator('iframe.workspace-surface-frame').locator('[data-pinned-actions] .action')
      ).toHaveCount(0);
      await capture('group5-01-live-parity-controls.png');

      const askIntent = await request.post('/api/workspace-surfaces/intents', {
        data: { session, type: 'ask_ori', context: 'Review the live disposable track state.' }
      });
      expect(askIntent.ok(), await askIntent.text()).toBeTruthy();
      expect((await askIntent.json()).required_capabilities).toContain('reaper_live_control');
      const setupIntent = await request.post('/api/workspace-surfaces/intents', {
        data: { session, type: 'open_setup' }
      });
      expect(setupIntent.ok(), await setupIntent.text()).toBeTruthy();
      expect((await setupIntent.json()).provider_id).toBe('plugin:reaper-plugin:reaper-runtime');
      await request.delete('/api/workspace-surfaces/sessions', { data: { session } });
    }

    const plugin = (await (await request.get('/api/plugins')).json()).plugins.find(
      (item: { name: string }) => item.name === pluginName
    );
    expect(plugin.workspace_surfaces.capabilities[0].agent_operations).toContain('state.read');
    expect(plugin.workspace_surfaces.capabilities[0].agent_operations).toContain('draft.run');
    expect(legacyRequests).toEqual([]);
  } finally {
    await request.delete(`/api/plugins/${pluginName}`).catch(() => {});
    await request.delete(`/api/workspaces/${workspace.id}`).catch(() => {});
    await request.delete(`/api/workspaces/${legacy.id}`).catch(() => {});
  }
});
