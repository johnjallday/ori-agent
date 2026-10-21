import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import { startJanitorBuildModel } from './helpers/janitor-build-model';
import {
  prepareFolderCreationRuntime,
  attachFirst,
  resources,
  currentBuild,
  inbox
} from './helpers/folder-first-runtime';
import {
  mkdtempSync,
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
    await prepareFolderCreationRuntime(request);
  });
  test.afterAll(async () => {
    await stopModel?.();
  });

  async function editDraftTeam(page: Page, name: string) {
    await page.getByRole('button', { name: 'Edit team', exact: true }).click();
    const roster = page.locator('#workspaceRoleRoster');
    const clear = roster.getByRole('button', { name: 'Clear File Curator', exact: true });
    if (await clear.isVisible()) await clear.click();
    await roster
      .getByRole('button', { name: 'Create an agent for File Curator', exact: true })
      .click();
    const agent = page.locator('#addAgentModal');
    await expect(agent).toBeVisible();
    await agent.locator('[data-agent-create-field=name]').fill(name);
    await agent.locator('[data-agent-create-field=model]').selectOption('ori-synthetic-model');
    await agent.getByRole('button', { name: 'Add to team', exact: true }).click();
    await expect(agent).toBeHidden();
    await page.locator('#workspaceBuildWithBtn').click();
  }

  test('an ordinary folder suggestion creates only the edited notebook and its folder link', async ({
    page,
    request
  }, testInfo) => {
    stopModel!.scenario({ mode: 'ordinary', createNow: true });
    try {
      const root = inbox('Notebook source');
      const before = await resources(request),
        calls = stopModel!.calls();
      let preparations = 0;
      page.on('request', request => {
        if (/\/janitor-(review|prepare)$/.test(request.url())) preparations++;
      });
      await attachFirst(page, request, root);
      const primary = page.locator('#workspaceBuildPlanReviewSetup');
      await expect(primary).toHaveText('Create workspace');
      await expect(primary).toBeEnabled();
      const name = `Reviewed notebook ${Date.now()}`;
      await page.locator('#workspaceBuildPlanName').fill(name);
      await expect.poll(async () => (await currentBuild(request)).draft.name).toBe(name);
      await expect(primary).toBeEnabled();
      await expect(page.locator('#workspaceBuildCreationReview')).toBeHidden();
      expect(await resources(request)).toEqual(before);
      const build = await currentBuild(request);
      const response = page.waitForResponse(
        r => r.request().method() === 'POST' && new URL(r.url()).pathname === '/api/workspaces'
      );
      await page.screenshot({ path: testInfo.outputPath('folder-ordinary-review.png') });
      await primary.click();
      const preview = page.locator('[data-ws-map-placement-preview]');
      await expect(preview).toBeVisible();
      await page.locator('[data-ws-map-viewport]').focus();
      for (
        let n = 0;
        n < 60 && /\bis-invalid\b/.test((await preview.getAttribute('class')) || '');
        n++
      )
        await page.keyboard.press('ArrowDown');
      await page.keyboard.press('Enter');
      const result = await response;
      expect(result.ok(), await result.text()).toBeTruthy();
      await expect(preview).toBeHidden();
      const created = (await result.json()).folder;
      expect(created.name).toBe(name);
      const final = (await (await request.get(`/api/workspaces/build-sessions/${build.id}`)).json())
        .session;
      expect(final.status).toBe('created');
      const after = await resources(request);
      expect(after.workspaces).toHaveLength(before.workspaces.length + 1);
      expect(after.agents).toEqual(before.agents);
      const linked = (await (await request.get(`/api/workspaces/${created.id}/directories`)).json())
        .directories;
      // Ordinary creation also owns its native workspace directory. The
      // external source is linked once, not imported into that directory.
      expect(linked).toHaveLength(2);
      expect(linked.filter((reference: { path: string }) => reference.path === root)).toHaveLength(
        1
      );
      const janitor = after.permissions.find(item => item.id === created.id)!;
      expect(janitor.readiness.state).toBe('setup_required');
      expect(janitor.batches.total).toBe(0);
      expect(janitor.settings.automation_approved_at || '').toMatch(/^$|^0001-/);
      expect(preparations).toBe(0);
      expect(stopModel!.calls()).toBe(calls + 1);
      expect(existsSync(join(root, 'Filed'))).toBe(false);
      expect(readFileSync(join(root, 'invoice.pdf'), 'utf8')).toBe('synthetic source bytes');
    } finally {
      stopModel!.scenario();
    }
  });

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
        await expect(page.locator('#workspaceBuildFolderReview')).toBeVisible();
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
            .locator('#workspaceBuildFolderReview')
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
      // A still-focused composer may keep refinement open; switch explicitly
      // in that case without submitting its buffer.
      const returnToReview = page.getByRole('button', {
        name: 'Return to workspace review',
        exact: true
      });
      if (await returnToReview.isVisible()) await returnToReview.click();
      await page.locator('#workspaceBuildPlanName').fill('Keep my manual name');
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

  test('folder review keeps a staged team edit and the held folder through manual setup', async ({
    page,
    request
  }, testInfo) => {
    await page.setViewportSize({ width: 1280, height: 900 });
    const before = await resources(request);
    await attachFirst(page, request, inbox('Downloads'));
    await expect(page.locator('#workspaceBuildFolderTeam')).toContainText('Synthetic Curator');
    const folder = (await currentBuild(request)).folder.observation.id;
    await page.locator('#workspaceBuildPlanName').fill('Reviewed folder draft');
    await editDraftTeam(page, 'Reviewed Curator');
    await expect(page.locator('#workspaceBuildFolderReview')).toBeVisible();
    await expect(page.locator('#workspaceBuildPlanName')).toHaveValue('Reviewed folder draft');
    await expect(page.locator('#workspaceBuildFolderTeam')).toContainText('Reviewed Curator');
    await expect
      .poll(async () => (await currentBuild(request)).draft.role_staffing?.[0]?.name)
      .toBe('Reviewed Curator');
    expect((await currentBuild(request)).folder.observation.id).toBe(folder);
    await expect(page.locator('#workspaceBuildPlanReviewSetup')).toHaveText('Create & scan folder');
    await expect(page.locator('#workspaceBuildPlanReviewSetup')).toBeEnabled();
    expect(await resources(request)).toEqual(before);
    // The mode button can leave the pointer over the toast (which pauses its
    // expiry). Move away before waiting for a clean screenshot.
    await page.mouse.move(20, 880);
    await page
      .getByText('Reviewed Curator added to the workspace draft.', { exact: true })
      .waitFor({ state: 'hidden', timeout: 10_000 });
    await page.screenshot({ path: testInfo.outputPath('folder-review-edited-team.png') });
  });

  for (const monitoring of [false, true]) {
    test(`one attached folder creates the edited workspace and prepares review with monitoring ${monitoring ? 'on' : 'off'}`, async ({
      page,
      request
    }, testInfo) => {
      await page.setViewportSize({ width: 1280, height: 900 });
      const root = inbox('Downloads');
      const before = await resources(request);
      const modelCalls = stopModel!.calls();
      await attachFirst(page, request, root);
      const offer = page.locator('#workspaceBuildCreationReview');
      const confirm = page.locator('#workspaceBuildPlanReviewSetup');
      await expect(confirm).toHaveText('Create & scan folder');
      await expect(confirm).toBeEnabled();
      await expect(offer.getByRole('checkbox')).not.toBeChecked();
      const original = await currentBuild(request);
      const staleReview = (
        await (
          await request.post(`/api/workspaces/build-sessions/${original.id}/janitor-review`, {
            data: { version: original.version, monitoring: false }
          })
        ).json()
      ).review;
      if (monitoring) {
        const unreviewedMode = await request.post(
          `/api/workspaces/build-sessions/${original.id}/janitor-prepare`,
          {
            data: {
              version: staleReview.version,
              review_revision: staleReview.review_revision,
              monitoring: true
            }
          }
        );
        expect(unreviewedMode.status()).toBe(409);
        expect((await unreviewedMode.json()).code).toBe('review_changed');
      }
      const editedName = `Reviewed inbox ${monitoring ? 'watch' : 'manual'} ${Date.now()}`;
      const editedAgent = `Reviewed Curator ${Date.now()}`;
      await page.locator('#workspaceBuildPlanName').fill(editedName);
      if (!monitoring) await editDraftTeam(page, editedAgent);
      if (monitoring) {
        await offer.getByRole('checkbox').check();
        await expect(offer).toContainText('Daily scan at');
      }
      await expect(confirm).toHaveText('Create & scan folder');
      await expect(confirm).toBeEnabled();
      await expect(page.locator('#workspaceBuildPlanName')).toHaveValue(editedName);
      if (!monitoring)
        await expect(page.locator('#workspaceBuildFolderTeam')).toContainText(editedAgent);
      expect((await currentBuild(request)).folder.observation.id).toBe(
        original.folder.observation.id
      );
      // The old name/team/version cannot be confirmed after these edits.
      const stale = await request.post(
        `/api/workspaces/build-sessions/${original.id}/janitor-prepare`,
        {
          data: {
            version: staleReview.version,
            monitoring: false,
            review_revision: staleReview.review_revision
          }
        }
      );
      expect(stale.status()).toBe(409);
      expect(await resources(request)).toEqual(before);
      expect(existsSync(join(root, 'Filed'))).toBeFalsy();
      expect(stopModel!.calls() - modelCalls).toBe(1);
      await offer.getByText('Folder details', { exact: true }).click();
      await expect(offer).toContainText(root);
      await expect(offer).toContainText('No file contents are opened');
      await page.screenshot({ path: testInfo.outputPath('create-permission-review.png') });
      if (monitoring) {
        await page.setViewportSize({ width: 390, height: 844 });
        expect((await offer.boundingBox())!.width).toBeLessThanOrEqual(390);
        await expect(confirm).toBeInViewport();
        await page.screenshot({ path: testInfo.outputPath('create-permission-narrow.png') });
      }
      let pickerCalls = 0,
        confirmations = 0;
      page.on('request', r => {
        if (r.url().includes('/api/folder-picker/')) pickerCalls++;
        if (r.url().endsWith('/janitor-prepare')) confirmations++;
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
      await confirm.focus();
      await page.keyboard.down('Enter');
      await page.keyboard.down('Enter');
      await page.keyboard.up('Enter');
      if (!monitoring) {
        await expect(confirm).toHaveText('Retry preparation');
        await expect(confirm).toBeEnabled();
        expect(lostResponse).toBeTruthy();
        await expect(page.locator('#workspaceBuildPlanName')).toBeHidden();
        await page.screenshot({ path: testInfo.outputPath('lost-response-recovery.png') });
        await confirm.click();
      }
      await expect(page).toHaveURL(/panel=file-janitor/, { timeout: 20000 });
      await expect(page.locator('#fileJanitorConsoleBody')).toContainText('invoice.pdf');
      await expect(page.locator('#setupWizardDialog')).not.toHaveAttribute('open', '');
      expect(pickerCalls).toBe(0);
      expect(confirmations).toBe(monitoring ? 1 : 2); // one initial admission, plus explicit recovery only
      const workspaces = (await (await request.get('/api/workspaces')).json()).folders;
      const created = workspaces.filter((ws: { name: string }) => ws.name === editedName);
      expect(created).toHaveLength(1);
      if (!monitoring) expect(created[0].agents).toContain(editedAgent);
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
  test('an accepted preparation failure resumes the same workspace and scan after repair', async ({
    page,
    request
  }, testInfo) => {
    await page.setViewportSize({ width: 1280, height: 900 });
    const root = inbox('Downloads');
    const before = await resources(request);
    const calls = stopModel!.calls();
    await attachFirst(page, request, root);
    const confirm = page.locator('#workspaceBuildPlanReviewSetup');
    await expect(confirm).toHaveText('Create & scan folder');
    await expect(confirm).toBeEnabled();
    const build = await currentBuild(request);
    // A real filesystem conflict after the read-only review. Do not mock the
    // grant, creation, preparation or scan endpoints.
    writeFileSync(join(root, 'Filed'), 'synthetic filing-root blocker', { mode: 0o600 });
    const response = page.waitForResponse(r => r.url().endsWith('/janitor-prepare'));
    await confirm.click();
    const failed = await response;
    expect(failed.status()).toBe(409);
    const failure = await failed.json();
    const workspaceID = failure.details.workspace_id;
    expect(workspaceID).toBeTruthy();
    await expect(confirm).toHaveText('Retry preparation');
    await expect(confirm).toBeEnabled();
    await expect(page.locator('#workspaceBuildCreationReview')).toContainText(/Filed|filing/i);
    const accepted = (
      await (await request.get(`/api/workspaces/build-sessions/${build.id}`)).json()
    ).session;
    expect(accepted.preparation.workspace_id).toBe(workspaceID);
    expect((await resources(request)).workspaces).toEqual(
      [...before.workspaces, workspaceID].sort()
    );
    await page.screenshot({ path: testInfo.outputPath('accepted-preparation-failure.png') });
    renameSync(join(root, 'Filed'), join(root, 'kept-blocker.txt'));
    await confirm.click();
    await expect(page).toHaveURL(/panel=file-janitor/, { timeout: 20_000 });
    await expect(page.locator('#fileJanitorConsoleBody')).toContainText('invoice.pdf');
    const after = (await (await request.get(`/api/workspaces/build-sessions/${build.id}`)).json())
      .session;
    expect(after.preparation.id).toBe(accepted.preparation.id);
    expect(after.preparation.workspace_id).toBe(workspaceID);
    const batches = (
      await (await request.get(`/api/workspaces/${workspaceID}/file-janitor/batches`)).json()
    ).batches;
    expect(batches).toHaveLength(1);
    expect((await resources(request)).workspaces).toEqual(
      [...before.workspaces, workspaceID].sort()
    );
    expect(stopModel!.calls() - calls).toBe(1);
    expect(readFileSync(join(root, 'invoice.pdf'), 'utf8')).toBe('synthetic source bytes');
    await page.screenshot({ path: testInfo.outputPath('accepted-preparation-resumed.png') });
  });

  test('a late automatic reply cannot change an explicitly created and prepared workspace', async ({
    page,
    request
  }) => {
    test.setTimeout(60_000);
    const root = inbox('Downloads');
    stopModel!.scenario({ createNow: true, name: 'Late suggestion must not return' });
    const held = stopModel!.hold();
    let responded = false;
    let replied!: () => void;
    const replyFinished = new Promise<void>(resolve => {
      replied = resolve;
    });
    await page.route('**/api/workspaces/build-sessions/*/turns', async route => {
      try {
        const real = await route.fetch({ timeout: 60_000 });
        responded = true;
        await route.fulfill({ response: real }).catch(() => {}); // old page may have navigated
      } finally {
        replied();
      }
    });
    try {
      await attachFirst(page, request, root);
      await held.started;
      await page.getByRole('button', { name: 'Change purpose', exact: true }).click();
      await page
        .locator('#templatePicker .workspace-template-card')
        .filter({
          has: page.locator('.workspace-template-card-label', { hasText: /^File Janitor$/ })
        })
        .click();
      await page.locator('#workspaceBuildWithBtn').click();
      const name = `Manual delayed inbox ${Date.now()}`;
      await page.locator('#workspaceBuildPlanName').fill(name);
      await editDraftTeam(page, `Manual delayed curator ${Date.now()}`);
      const confirm = page.locator('#workspaceBuildPlanReviewSetup');
      await expect(confirm).toHaveText('Create & scan folder');
      await expect(confirm).toBeEnabled();
      const build = await currentBuild(request);
      await confirm.click();
      await expect(page).toHaveURL(/panel=file-janitor/, { timeout: 20_000 });
      const accepted = (
        await (await request.get(`/api/workspaces/build-sessions/${build.id}`)).json()
      ).session;
      expect(accepted.draft.name).toBe(name);
      expect(accepted.preparation.workspace_id).toBeTruthy();
      const beforeLate = await resources(request);
      held.release();
      await replyFinished;
      expect(responded).toBe(true);
      const after = (await (await request.get(`/api/workspaces/build-sessions/${build.id}`)).json())
        .session;
      expect(after.draft).toEqual(accepted.draft);
      expect(after.preparation).toEqual(accepted.preparation);
      expect((await resources(request)).workspaces).toEqual(beforeLate.workspaces);
      expect((await resources(request)).agents).toEqual(beforeLate.agents);
      expect(readFileSync(join(root, 'invoice.pdf'), 'utf8')).toBe('synthetic source bytes');
    } finally {
      held.release();
      stopModel!.scenario();
    }
  });

  test('a lost held selection refuses before creating resources and offers explicit repair', async ({
    page,
    request
  }, testInfo) => {
    const root = inbox();
    await attachFirst(page, request, root);
    const offer = page.locator('#workspaceBuildCreationReview');
    const confirm = page.locator('#workspaceBuildPlanReviewSetup');
    await expect(confirm).toHaveText('Create & scan folder');
    await expect(confirm).toBeEnabled();
    const before = (await (await request.get('/api/workspaces')).json()).folders
      .map((ws: { id: string }) => ws.id)
      .sort();
    renameSync(root, root + '-moved');
    await confirm.click();
    await expect(offer).toContainText(/folder attached.*no longer available|attach.*again/i);
    const after = (await (await request.get('/api/workspaces')).json()).folders
      .map((ws: { id: string }) => ws.id)
      .sort();
    expect(after).toEqual(before);
    expect(existsSync(join(root + '-moved', 'Filed'))).toBeFalsy();
    await expect(page.locator('#workspaceBuildFolderChange')).toBeVisible();
    await expect(page.locator('#workspaceBuildPlanName')).toBeVisible();
    await page.screenshot({ path: testInfo.outputPath('lost-selection-repair.png') });
  });
  test('explicit defer keeps the saved workspace/result and allows a separate build', async ({
    page,
    request
  }, testInfo) => {
    const root = inbox();
    await attachFirst(page, request, root);
    const confirm = page.locator('#workspaceBuildPlanReviewSetup');
    await expect(confirm).toHaveText('Create & scan folder');
    await expect(confirm).toBeEnabled();
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
    await confirm.click();
    await expect(
      page.getByRole('button', { name: 'Defer preparation', exact: true })
    ).toBeVisible();
    await expect(page.getByRole('link', { name: 'Open workspace', exact: true })).toHaveAttribute(
      'href',
      `/workspaces/${accepted!.folder_slug}?panel=file-janitor`
    );
    await page.screenshot({ path: testInfo.outputPath('explicit-defer.png') });
    await page.getByRole('button', { name: 'Defer preparation', exact: true }).click();
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
    await expect(page.locator('#workspaceBuildComposerInput')).toBeEnabled();
    await expect(page.locator('#workspaceBuildCreationReview')).not.toBeVisible();
  });
});

// Real-server acceptance. Only disposable local folders; no grant, settings,
// watcher, wizard or scan endpoint is stubbed. Native picker evidence is separate.
test.describe.configure({ mode: 'serial' });
const run = Date.now().toString(36);

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
