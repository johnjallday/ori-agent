import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import { startJanitorBuildModel } from './helpers/janitor-build-model';
import {
  mkdtempSync,
  mkdirSync,
  writeFileSync,
  existsSync,
  readFileSync,
  realpathSync,
  utimesSync,
  renameSync
} from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

test.describe('attached creation with a scripted local model', () => {
  test.skip(
    process.env.ORI_TEST_BUILD_CREATION !== '1',
    'Requires the isolated scripted-model runtime documented in helpers/janitor-build-model.ts'
  );
  let stopModel: Awaited<ReturnType<typeof startJanitorBuildModel>> | undefined;
  test.beforeAll(async ({ request }) => {
    stopModel = await startJanitorBuildModel();
    const model = await request.post('/api/settings/system-model', {
      data: { provider: 'lmstudio', model: 'ori-synthetic-model' }
    });
    expect(model.ok(), await model.text()).toBeTruthy();
    let state = (await (await request.get('/api/personal-assistant')).json()).personal_assistant;
    if (state.state === 'needs_hire') {
      const hired = await request.post('/api/personal-assistant/hire', {
        data: {
          request_id: 'synthetic-creation-hire-' + Date.now(),
          if_version: state.state_version || 0,
          display_name: 'Synthetic Assistant',
          mandate: 'Help review synthetic folders.',
          focus_areas: ['plan_my_day']
        }
      });
      expect(hired.ok(), await hired.text()).toBeTruthy();
      state = (await (await request.get('/api/personal-assistant')).json()).personal_assistant;
    }
    if (state.state === 'needs_hq') {
      const hq = await request.post('/api/personal-assistant/hq', {
        data: {
          request_id: 'synthetic-creation-hq-' + Date.now(),
          if_version: state.state_version,
          name: 'Synthetic HQ',
          timezone: 'UTC'
        }
      });
      expect(hq.ok(), await hq.text()).toBeTruthy();
    }
    const availability = await request.get('/api/workspaces/build-sessions/availability');
    expect((await availability.json()).available).toBeTruthy();
  });
  test.afterAll(async () => {
    await stopModel?.();
  });

  async function attachFirst(
    page: Page,
    request: APIRequestContext,
    root: string,
    unsent = '',
    beforeAttach?: () => Promise<void>
  ) {
    const old = (
      await (
        await request.post('/api/workspaces/build-sessions', {
          data: { entry_point: 'home_cockpit_create' }
        })
      ).json()
    ).session;
    if (old?.status === 'open')
      await request.post(`/api/workspaces/build-sessions/${old.id}/abandon`, {
        data: { version: old.version }
      });
    await request.post('/api/onboarding/skip');
    await page.goto('/');
    await page.locator('#cockpitCreateWorkspaceBtn').click();
    const composer = page.locator('#workspaceBuildComposerInput');
    await expect(composer).toBeEnabled();
    if (unsent) await composer.fill(unsent);
    if (beforeAttach) await beforeAttach();
    await page
      .locator('#workspaceBuildPane')
      .getByRole('button', { name: 'Add folder', exact: true })
      .click();
    await expect(page.locator('#workspaceBuildPane')).toContainText(
      'shared with your configured model'
    );
    await page.getByPlaceholder('~/Projects/my-app').fill(root);
    await page.getByRole('button', { name: 'Use this path', exact: true }).click();
    return composer;
  }

  async function resources(request: APIRequestContext) {
    const workspaces = (await (await request.get('/api/workspaces')).json()).folders;
    const permissions = await Promise.all(
      workspaces.map(
        async (w: { id: string; directory_references: unknown; mcp_bindings: unknown }) => {
          const base = `/api/workspaces/${w.id}/file-janitor`;
          const status = (await (await request.get(base)).json()).status;
          // Readiness timestamps change on reads, not because automation ran.
          const { checked_at: _checkedAt, ...readiness } = status.readiness;
          return {
            id: w.id,
            references: w.directory_references,
            bindings: w.mcp_bindings,
            settings: status.settings,
            readiness,
            batches: await (await request.get(`${base}/batches`)).json(),
            history: await (await request.get(`${base}/history`)).json()
          };
        }
      )
    );
    return {
      workspaces: workspaces.map((w: { id: string }) => w.id).sort(),
      permissions: permissions.sort((a, b) => a.id.localeCompare(b.id)),
      plugins: await (await request.get('/api/plugins')).json(),
      agents: (await (await request.get('/api/agents')).json()).agents.sort(
        (a: { name: string }, b: { name: string }) => a.name.localeCompare(b.name)
      )
    };
  }

  async function currentBuild(request: APIRequestContext) {
    return (
      await (
        await request.post('/api/workspaces/build-sessions', {
          data: { entry_point: 'home_cockpit_create' }
        })
      ).json()
    ).session;
  }

  test('folder-first suggestion uses one draft-only operation even with create_now and unsent text', async ({
    page,
    request
  }, testInfo) => {
    stopModel!.scenario({ createNow: true });
    const before = await resources(request);
    const calls = stopModel!.calls();
    const root = inbox('Downloads');
    const turns: Record<string, any>[] = [];
    page.on('request', r => {
      if (r.url().endsWith('/turns')) turns.push(r.postDataJSON());
    });
    const composer = await attachFirst(page, request, root, 'This is still an unsent thought.');
    await expect(page.locator('#folderNameInput')).toHaveValue(/Synthetic Janitor/);
    await expect(page.locator('#workspaceBuildDraftText')).toContainText('Synthetic Janitor');
    await expect(composer).toHaveValue('This is still an unsent thought.');
    await expect.poll(() => stopModel!.calls()).toBe(calls + 1);
    expect(turns).toHaveLength(1);
    expect(turns[0].intent).toBe('suggest_folder');
    expect(turns[0].text).toBeUndefined();
    const build = await currentBuild(request);
    expect(build.turn_count).toBe(0);
    expect(build.first_request || '').toBe('');
    expect(build.create_now || false).toBe(false);
    expect(build.transcript.filter((e: { role: string }) => e.role === 'user')).toHaveLength(0);
    const replay = await request.post(`/api/workspaces/build-sessions/${build.id}/turns`, {
      data: turns[0]
    });
    expect(replay.ok(), await replay.text()).toBeTruthy();
    expect(stopModel!.calls()).toBe(calls + 1);
    expect(await resources(request)).toEqual(before);
    expect(existsSync(join(root, 'Filed'))).toBe(false);
    expect(readFileSync(join(root, 'invoice.pdf'), 'utf8')).toBe('synthetic source bytes');
    await page.screenshot({ path: testInfo.outputPath('folder-first-suggestion.png') });
    stopModel!.scenario();
  });

  test('folder-first preserves explicit earlier name and cleared description', async ({
    page,
    request
  }) => {
    const calls = stopModel!.calls(),
      before = await resources(request);
    await attachFirst(page, request, inbox(), 'Still unsent', async () => {
      await page.getByRole('button', { name: 'Set up manually', exact: true }).click();
      await page.locator('#wizardNextBtn').click();
      await page.locator('#folderNameInput').fill('My earlier name');
      await page.locator('#folderDescriptionInput').fill('A description I decided to clear');
      await page.locator('#folderDescriptionInput').fill('');
      await page.locator('#workspaceBuildWithBtn').click();
    });
    await expect.poll(async () => (await currentBuild(request)).proposal?.status).toBe('ready');
    await expect(page.locator('#workspaceBuildDraftText')).toContainText('My earlier name');
    await expect(page.locator('#folderDescriptionInput')).toHaveValue('');
    await expect(page.locator('#workspaceBuildComposerInput')).toHaveValue('Still unsent');
    const saved = await currentBuild(request);
    expect(saved.draft.name).toBe('My earlier name');
    expect(saved.draft.description || '').toBe('');
    expect(saved.edited_fields.name).toBeGreaterThan(0);
    expect(saved.edited_fields.description).toBeGreaterThan(0);
    expect(stopModel!.calls()).toBe(calls + 1);
    expect(await resources(request)).toEqual(before);
  });

  for (const mode of ['success', 'failure', 'ambiguous'] as const) {
    test(`folder-first ${mode} leaves a usable draft without creating`, async ({
      page,
      request
    }, testInfo) => {
      stopModel!.scenario({ mode });
      const root = inbox(mode === 'success' ? 'Downloads' : ''),
        before = await resources(request),
        calls = stopModel!.calls();
      await attachFirst(page, request, root);
      if (mode === 'success') {
        await expect(page.locator('#workspaceBuildDraftText')).toContainText('Synthetic Janitor');
        await expect(page.locator('#workspaceBuildComposerInput')).toHaveValue('');
      } else if (mode === 'failure') {
        await expect(
          page.getByRole('button', { name: 'Retry suggestion', exact: true })
        ).toBeVisible();
        await expect(page.locator('#workspaceBuildStatus')).toContainText('draft is kept');
      } else {
        await expect(
          page
            .locator('#workspaceBuildPane')
            .getByRole('button', { name: 'Organize files', exact: true })
        ).toBeVisible();
      }
      expect(stopModel!.calls()).toBe(calls + 1);
      expect(await resources(request)).toEqual(before);
      expect(existsSync(join(root, 'Filed'))).toBe(false);
      await page.screenshot({ path: testInfo.outputPath(`folder-first-${mode}.png`) });
      stopModel!.scenario();
    });
  }

  test('folder-first delayed reply cannot overwrite edits or a replacement folder', async ({
    page,
    request
  }, testInfo) => {
    stopModel!.scenario({ createNow: true, name: 'Stale synthetic suggestion' });
    const held = stopModel!.hold();
    const root = inbox(),
      replacement = inbox(),
      before = await resources(request),
      calls = stopModel!.calls();
    try {
      await attachFirst(page, request, root);
      await held.started;
      const build = await currentBuild(request);
      const edited = await request.patch(`/api/workspaces/build-sessions/${build.id}/draft`, {
        data: {
          version: build.version,
          draft: { ...build.draft, name: 'My explicit name' },
          edited_fields: ['name']
        }
      });
      expect(edited.ok(), await edited.text()).toBeTruthy();
      const current = (await edited.json()).session;
      const selected = await request.post('/api/folder-picker/select-path', {
        data: { path: replacement }
      });
      const attached = await request.post(`/api/workspaces/build-sessions/${build.id}/folder`, {
        data: {
          version: current.version,
          mode: 'selection',
          selection_token: (await selected.json()).selection_token
        }
      });
      expect(attached.ok(), await attached.text()).toBeTruthy();
      const replaced = (await attached.json()).session;
      held.release();
      await expect
        .poll(async () => (await currentBuild(request)).proposal.id)
        .toBe(replaced.proposal.id);
      await expect(page.locator('#workspaceBuildStatus')).not.toContainText(
        'Suggesting a workspace'
      );
      const after = await currentBuild(request);
      expect(after.draft.name).toBe('My explicit name');
      expect(after.folder.observation.id).toBe(replaced.folder.observation.id);
      expect(after.create_now || false).toBe(false);
      expect(stopModel!.calls()).toBe(calls + 1);
      expect(await resources(request)).toEqual(before);
      expect(existsSync(join(root, 'Filed')) || existsSync(join(replacement, 'Filed'))).toBe(false);
      await page.screenshot({ path: testInfo.outputPath('folder-first-stale-reply.png') });
    } finally {
      held.release();
      stopModel!.scenario();
    }
  });

  test('folder-first close and Resume preserve manual edits without reviving inference', async ({
    page,
    request
  }) => {
    stopModel!.scenario({ createNow: true, name: 'Late closed suggestion' });
    const held = stopModel!.hold();
    const calls = stopModel!.calls();
    try {
      await attachFirst(page, request, inbox(), 'Unsent composer buffer');
      await held.started;
      await page.getByRole('button', { name: 'Set up manually', exact: true }).click();
      await page.locator('#wizardNextBtn').click();
      await page.locator('#folderNameInput').fill('Keep my manual name');
      await page.getByRole('button', { name: 'Close create workspace', exact: true }).click();
      await expect(page.locator('#addFolderModal')).toBeHidden();
      held.release();
      await page.locator('#cockpitCreateWorkspaceBtn').click();
      await page
        .locator('#workspaceBuildPane')
        .getByRole('button', { name: 'Resume', exact: true })
        .click();
      await expect(page.locator('#folderNameInput')).toHaveValue('Keep my manual name');
      await expect(page.locator('#workspaceBuildComposerInput')).toHaveValue('');
      expect((await currentBuild(request)).create_now || false).toBe(false);
      expect(stopModel!.calls()).toBe(calls + 1);
    } finally {
      held.release();
      stopModel!.scenario();
    }
  });

  for (const monitoring of [false, true]) {
    test(`one attached folder creates the edited workspace and prepares review with monitoring ${monitoring ? 'on' : 'off'}`, async ({
      page,
      request
    }, testInfo) => {
      await page.setViewportSize({ width: 1280, height: 900 });
      const root = inbox();
      const resumed = await request.post('/api/workspaces/build-sessions', {
        data: { entry_point: 'home_cockpit_create' }
      });
      const oldBuild = (await resumed.json()).session;
      if (oldBuild?.status === 'open')
        await request.post(`/api/workspaces/build-sessions/${oldBuild.id}/abandon`, {
          data: { version: oldBuild.version }
        });
      await request.post('/api/onboarding/skip');
      await page.goto('/');
      await page.locator('#cockpitCreateWorkspaceBtn').click();
      const composer = page.locator('#workspaceBuildComposerInput');
      await expect(composer).toBeEnabled();
      await composer.fill('Prepare a File Janitor for my synthetic inbox.');
      await composer.press('Enter');
      await expect(page.locator('#folderNameInput')).toHaveValue(/Synthetic Janitor/);
      await expect(composer).toBeEnabled();
      await page
        .locator('#workspaceBuildPane')
        .getByRole('button', { name: 'Add folder', exact: true })
        .click();
      await page.getByPlaceholder('~/Projects/my-app').fill(root);
      await page.getByRole('button', { name: 'Use this path', exact: true }).click();
      await expect(page.locator('#workspaceBuildPane')).toContainText('Folder attached');
      const offer = page.locator('#workspaceBuildCreationReview');
      await expect(
        offer.getByRole('button', { name: 'Review & create', exact: true })
      ).toBeVisible();
      await offer.getByRole('button', { name: 'Review & create', exact: true }).click();
      await expect(
        offer.getByRole('button', { name: 'Create & prepare review', exact: true })
      ).toBeVisible();
      await expect(offer.getByRole('checkbox')).not.toBeChecked();
      expect(existsSync(join(root, 'Filed'))).toBeFalsy();
      await expect(offer).toContainText('Synthetic Curator');
      if (!monitoring) {
        await offer.getByRole('button', { name: 'Change folder', exact: true }).click();
        await expect(page.getByPlaceholder('~/Projects/my-app')).toBeVisible();
        await page
          .locator('#workspaceBuildPane')
          .getByRole('button', { name: 'Cancel', exact: true })
          .click();
        await page
          .locator('#workspaceBuildPane')
          .getByRole('button', { name: /^Remove folder / })
          .click();
        await expect(offer).not.toBeVisible();
        await page
          .locator('#workspaceBuildPane')
          .getByRole('button', { name: 'Add folder', exact: true })
          .click();
        await page.getByPlaceholder('~/Projects/my-app').fill(root);
        await page.getByRole('button', { name: 'Use this path', exact: true }).click();
        await offer.getByRole('button', { name: 'Review & create', exact: true }).click();
        await expect(
          offer.getByRole('button', { name: 'Create & prepare review', exact: true })
        ).toBeVisible();
      }
      await offer.getByRole('button', { name: 'Edit setup', exact: true }).click();
      await page.locator('#wizardStep4').getByRole('button', { name: 'Edit', exact: true }).click();
      const editedName = `Reviewed inbox ${monitoring ? 'watch' : 'manual'} ${Date.now()}`;
      await page.locator('#folderNameInput').fill(editedName);
      await page.locator('#workspaceBuildWithBtn').click();
      await offer.getByRole('button', { name: 'Review & create', exact: true }).click();
      await expect(offer).toContainText(editedName);
      if (monitoring) {
        await offer.getByRole('checkbox').check();
        await expect(offer).toContainText('Daily scan at');
      }
      await offer.getByText('Folder details', { exact: true }).click();
      await expect(offer).toContainText(root);
      await page.screenshot({ path: testInfo.outputPath('create-permission-review.png') });
      if (monitoring) {
        await page.setViewportSize({ width: 390, height: 844 });
        await offer
          .getByRole('button', { name: 'Create & prepare review', exact: true })
          .scrollIntoViewIfNeeded();
        expect((await offer.boundingBox())!.width).toBeLessThanOrEqual(390);
        await expect(
          offer.getByRole('button', { name: 'Change folder', exact: true })
        ).toBeVisible();
        await page.screenshot({ path: testInfo.outputPath('create-permission-narrow.png') });
      }
      let pickerCalls = 0;
      page.on('request', r => {
        if (r.url().includes('/api/folder-picker/')) pickerCalls++;
      });
      let lostResponse = false;
      if (!monitoring) {
        await page.route('**/api/workspaces/build-sessions/*/janitor-prepare', async route => {
          if (lostResponse) {
            await route.continue();
            return;
          }
          const realResponse = await route.fetch();
          expect(realResponse.ok(), await realResponse.text()).toBeTruthy();
          lostResponse = true;
          await route.abort('failed'); // Real server committed; discard transport response only.
        });
      }
      const confirm = offer.getByRole('button', { name: 'Create & prepare review', exact: true });
      await confirm.focus();
      await confirm.press('Enter');
      if (!monitoring) {
        await expect(
          offer.getByRole('button', { name: 'Retry preparation', exact: true })
        ).toBeVisible();
        expect(lostResponse).toBeTruthy();
        await page.screenshot({ path: testInfo.outputPath('lost-response-recovery.png') });
        await offer.getByRole('button', { name: 'Retry preparation', exact: true }).click();
      }
      await expect(page).toHaveURL(/panel=file-janitor/, { timeout: 20000 });
      await expect(page.locator('#fileJanitorConsoleBody')).toContainText('invoice.pdf');
      await expect(page.locator('#setupWizardDialog')).not.toHaveAttribute('open', '');
      expect(pickerCalls).toBe(0);
      const workspaces = (await (await request.get('/api/workspaces')).json()).folders;
      const created = workspaces.filter((ws: { name: string }) => ws.name === editedName);
      expect(created).toHaveLength(1);
      const status = (
        await (await request.get(`/api/workspaces/${created[0].id}/file-janitor`)).json()
      ).status;
      expect(status.settings.root_path).toBe(root);
      expect(status.settings.setup_decision.mode).toBe(monitoring ? 'monitoring' : 'manual');
      expect(Boolean(status.settings.paused)).toBe(!monitoring);
      if (monitoring) {
        for (const component of ['watcher', 'scheduler']) {
          expect(
            status.readiness.checks.find(
              (check: { component: string }) => check.component === component
            )?.status
          ).toBe('ok');
        }
      }
      expect(readFileSync(join(root, 'invoice.pdf'), 'utf8')).toBe('synthetic source bytes');
      await expect(page.locator('.fj-batch-loading')).not.toBeVisible();
      await page.screenshot({ path: testInfo.outputPath('created-initial-review.png') });
    });
  }
  test('a lost held selection refuses before creating resources and offers explicit repair', async ({
    page,
    request
  }, testInfo) => {
    const root = inbox();
    const resumed = await request.post('/api/workspaces/build-sessions', {
      data: { entry_point: 'home_cockpit_create' }
    });
    const oldBuild = (await resumed.json()).session;
    if (oldBuild?.status === 'open')
      await request.post(`/api/workspaces/build-sessions/${oldBuild.id}/abandon`, {
        data: { version: oldBuild.version }
      });
    await request.post('/api/onboarding/skip');
    await page.goto('/');
    await page.locator('#cockpitCreateWorkspaceBtn').click();
    const composer = page.locator('#workspaceBuildComposerInput');
    await expect(composer).toBeEnabled();
    await composer.fill('Prepare File Janitor for a temporary inbox.');
    await composer.press('Enter');
    await expect(page.locator('#folderNameInput')).toHaveValue(/Synthetic Janitor/);
    await expect(composer).toBeEnabled();
    const offer = page.locator('#workspaceBuildCreationReview');
    await expect(offer).not.toBeVisible();
    await page
      .locator('#workspaceBuildPane')
      .getByRole('button', { name: 'Add folder', exact: true })
      .click();
    await page.getByPlaceholder('~/Projects/my-app').fill(root);
    await page.getByRole('button', { name: 'Use this path', exact: true }).click();
    await offer.getByRole('button', { name: 'Review & create', exact: true }).click();
    await expect(
      offer.getByRole('button', { name: 'Create & prepare review', exact: true })
    ).toBeVisible();
    const before = (await (await request.get('/api/workspaces')).json()).folders
      .map((ws: { id: string }) => ws.id)
      .sort();
    renameSync(root, root + '-moved');
    await offer.getByRole('button', { name: 'Create & prepare review', exact: true }).click();
    await expect(offer).toContainText(/folder attached.*no longer available|attach.*again/i);
    const after = (await (await request.get('/api/workspaces')).json()).folders
      .map((ws: { id: string }) => ws.id)
      .sort();
    expect(after).toEqual(before);
    expect(existsSync(join(root + '-moved', 'Filed'))).toBeFalsy();
    await expect(offer.getByRole('button', { name: 'Edit setup', exact: true })).toBeVisible();
    await page.screenshot({ path: testInfo.outputPath('lost-selection-repair.png') });
  });
  test('explicit defer keeps the saved workspace/result and allows a separate build', async ({
    page,
    request
  }, testInfo) => {
    const root = inbox();
    const resumed = await request.post('/api/workspaces/build-sessions', {
      data: { entry_point: 'home_cockpit_create' }
    });
    const oldBuild = (await resumed.json()).session;
    if (oldBuild?.status === 'open')
      await request.post(`/api/workspaces/build-sessions/${oldBuild.id}/abandon`, {
        data: { version: oldBuild.version }
      });
    await request.post('/api/onboarding/skip');
    await page.goto('/');
    await page.locator('#cockpitCreateWorkspaceBtn').click();
    const composer = page.locator('#workspaceBuildComposerInput');
    await expect(composer).toBeEnabled();
    await composer.fill('Prepare a synthetic File Janitor.');
    await composer.press('Enter');
    await expect(page.locator('#folderNameInput')).toHaveValue(/Synthetic Janitor/);
    await expect(composer).toBeEnabled();
    await page
      .locator('#workspaceBuildPane')
      .getByRole('button', { name: 'Add folder', exact: true })
      .click();
    await page.getByPlaceholder('~/Projects/my-app').fill(root);
    await page.getByRole('button', { name: 'Use this path', exact: true }).click();
    const offer = page.locator('#workspaceBuildCreationReview');
    await offer.getByRole('button', { name: 'Review & create', exact: true }).click();
    await expect(
      offer.getByRole('button', { name: 'Create & prepare review', exact: true })
    ).toBeVisible();
    let accepted:
      | { workspace_id: string; folder_slug: string; preparation: { result: { batch_id: string } } }
      | undefined;
    let confirmation: { version: number; monitoring: boolean; review_revision: string } | undefined;
    let path = '';
    await page.route('**/api/workspaces/build-sessions/*/janitor-prepare', async route => {
      path = new URL(route.request().url()).pathname;
      confirmation = route.request().postDataJSON();
      const real = await route.fetch();
      expect(real.ok(), await real.text()).toBeTruthy();
      accepted = await real.json();
      await route.abort('failed');
    });
    await offer.getByRole('button', { name: 'Create & prepare review', exact: true }).click();
    await expect(
      offer.getByRole('button', { name: 'Defer preparation', exact: true })
    ).toBeVisible();
    await expect(offer.getByRole('link', { name: 'Open workspace', exact: true })).toHaveAttribute(
      'href',
      `/workspaces/${accepted!.folder_slug}?panel=file-janitor`
    );
    await page.screenshot({ path: testInfo.outputPath('explicit-defer.png') });
    await offer.getByRole('button', { name: 'Defer preparation', exact: true }).click();
    await expect(page.locator('#addFolderModal')).not.toBeVisible();
    const oldRetry = await request.post(path, { data: confirmation });
    expect(oldRetry.status()).toBe(409);
    expect((await oldRetry.json()).code).toBe('preparation_deferred');
    const status = (
      await (await request.get(`/api/workspaces/${accepted!.workspace_id}/file-janitor`)).json()
    ).status;
    expect(status.settings.setup_preparation).toBeUndefined();
    expect(status.settings.setup_decision.mode).toBe('manual');
    const batches = (
      await (
        await request.get(`/api/workspaces/${accepted!.workspace_id}/file-janitor/batches`)
      ).json()
    ).batches;
    expect(batches).toHaveLength(1);
    expect(batches[0].id).toBe(accepted!.preparation.result.batch_id);
    expect(readFileSync(join(root, 'invoice.pdf'), 'utf8')).toBe('synthetic source bytes');
    await page.goto(`/workspaces/${accepted!.folder_slug}?panel=file-janitor`);
    await expect(page.locator('#fileJanitorConsoleBody')).toContainText('invoice.pdf');
    await expect(page.locator('#setupWizardDialog')).not.toHaveAttribute('open', '');
    await page.screenshot({ path: testInfo.outputPath('deferred-saved-review.png') });
    await page.goto('/');
    await page.locator('#cockpitCreateWorkspaceBtn').click();
    await expect(composer).toBeEnabled();
    await expect(page.locator('#workspaceBuildCreationReview')).not.toBeVisible();
  });
});

// Real-server acceptance. Only disposable local folders; no grant, settings,
// watcher, wizard or scan endpoint is stubbed. Native picker evidence is separate.
test.describe.configure({ mode: 'serial' });
const run = Date.now().toString(36);

function inbox(name = '') {
  let root = realpathSync(mkdtempSync(join(tmpdir(), 'ori-janitor-creation-')));
  if (name) {
    root = join(root, name);
    mkdirSync(root, { mode: 0o700 });
  }
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
    if (!monitoring) {
      const accepted = (
        await (
          await request.get(`/api/workspaces/${workspace.id}/file-janitor/setup-review`)
        ).json()
      ).review;
      const revoked = await request.post(`/api/workspaces/${workspace.id}/file-janitor/revoke`, {
        data: {}
      });
      expect(revoked.ok(), await revoked.text()).toBeTruthy();
      const stale = await request.post(
        `/api/workspaces/${workspace.id}/file-janitor/prepare-review`,
        {
          data: {
            reference_id: workspace.refs[0],
            monitoring: false,
            review_revision: accepted.review_revision
          }
        }
      );
      expect(stale.status()).toBe(409);
      const refs = (await (await request.get(`/api/workspaces/${workspace.id}/directories`)).json())
        .directories;
      expect(refs.some((ref: { id: string }) => ref.id === workspace.refs[0])).toBeTruthy();
      await page.reload();
      await expect(page.locator('#fileJanitorMount')).toContainText('revoked');
      await expect(
        page.getByRole('button', { name: 'Use attached Synthetic Inbox', exact: true })
      ).toHaveCount(0);
      expect(readFileSync(join(root, 'invoice.pdf'), 'utf8')).toBe('synthetic source bytes');
      await page.screenshot({ path: testInfo.outputPath('revoked-permission-repair.png') });
    }
  });
}

for (const settling of [false, true]) {
  test(`manual preparation honestly lands on ${settling ? 'settling files' : 'an empty folder'} and never silently rescans`, async ({
    page,
    request
  }, testInfo) => {
    const root = realpathSync(mkdtempSync(join(tmpdir(), 'ori-janitor-empty-')));
    if (settling)
      writeFileSync(join(root, 'fresh.pdf'), 'synthetic recent source', { mode: 0o600 });
    const workspace = await createLinked(request, settling ? 'settling' : 'empty', [root]);
    await page.goto(`/workspaces/${workspace.slug}?panel=file-janitor`);
    const review = page.locator('#setupWizardDialog[open] .fj-setup-review');
    await review.getByRole('button', { name: 'Use attached Synthetic Inbox', exact: true }).click();
    await expect(page.locator('#setupWizardDialog')).not.toHaveAttribute('open', '');
    await expect(page.locator('#fileJanitorConsoleBody')).toContainText('Initial scan finished');
    await expect(page.locator('#fileJanitorConsoleBody')).toContainText('No eligible files');
    await expect(page.locator('#fileJanitorConsoleBody')).not.toContainText(
      'Not scanned in this session'
    );
    const statusReview = (
      await (await request.get(`/api/workspaces/${workspace.id}/file-janitor/setup-review`)).json()
    ).review;
    expect(statusReview.state).toBe('prepared');
    expect(statusReview.result.outcome).toBe('no_eligible');
    expect(statusReview.result.batch_id || '').toBe('');
    await page.reload();
    await expect(page.locator('#fileJanitorConsoleBody')).toContainText('Initial scan finished');
    await expect(page.locator('#fileJanitorConsoleBody')).not.toContainText(
      'Not scanned in this session'
    );
    await expect(page.locator('#setupWizardDialog')).not.toHaveAttribute('open', '');
    await expect(page.locator('.fj-batch-loading')).not.toBeVisible();
    await page.screenshot({
      path: testInfo.outputPath(settling ? 'initial-settling.png' : 'initial-empty.png')
    });
    const replay = await request.post(
      `/api/workspaces/${workspace.id}/file-janitor/prepare-review`,
      {
        data: {
          reference_id: workspace.refs[0],
          monitoring: false,
          review_revision: statusReview.review_revision
        }
      }
    );
    expect(replay.ok(), await replay.text()).toBeTruthy();
    expect((await replay.json()).preparation.result.completed_at).toBe(
      statusReview.result.completed_at
    );
    const batches = (
      await (await request.get(`/api/workspaces/${workspace.id}/file-janitor/batches`)).json()
    ).batches;
    expect(batches).toHaveLength(0);
    if (!settling) {
      const file = join(root, 'later.pdf');
      writeFileSync(file, 'synthetic later source', { mode: 0o600 });
      const old = new Date(Date.now() - 3600000);
      utimesSync(file, old, old);
      let scans = 0;
      page.on('request', req => {
        if (req.url().endsWith('/file-janitor/scan')) scans++;
      });
      await page.waitForTimeout(300);
      expect(scans).toBe(0);
      await page.locator('#fileJanitorScan').click();
      await expect(page.locator('#fileJanitorConsoleBody')).toContainText('later.pdf');
      expect(scans).toBe(1);
      expect(new URL(page.url()).searchParams.has('preparation')).toBeFalsy();
    }
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
