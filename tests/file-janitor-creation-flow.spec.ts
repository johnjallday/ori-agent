import { test, expect, type APIRequestContext } from '@playwright/test';
import { startJanitorBuildModel } from './helpers/janitor-build-model';
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
  let stopModel: (() => Promise<void>) | undefined;
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
      const confirm = offer.getByRole('button', { name: 'Create & prepare review', exact: true });
      await confirm.focus();
      await confirm.press('Enter');
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
});

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
