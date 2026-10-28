import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import {
  mkdtempSync,
  realpathSync,
  writeFileSync,
  readFileSync,
  readdirSync,
  utimesSync
} from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { randomUUID } from 'node:crypto';
import { startJanitorBuildModel } from './helpers/janitor-build-model';
import {
  prepareFolderCreationRuntime,
  abandonOpenBuild,
  attachFirst
} from './helpers/folder-first-runtime';

// Real host/storage/selection/creation acceptance. Only model responses are
// synthetic. The link observer reads a public page's bounded metadata, not a
// virtual receipt or an SSRF exemption. Use an isolated loopback-model demo.
async function snapshot(request: APIRequestContext) {
  const workspaces = (await (await request.get('/api/workspaces')).json()).folders;
  const agents = (await (await request.get('/api/agents')).json()).agents;
  return {
    workspaces: workspaces.map((value: { id: string }) => value.id).sort(),
    agents: agents.map((value: { name: string }) => value.name).sort()
  };
}

async function declineAutomaticSetup(request: APIRequestContext, root: string) {
  const picked = await request.post('/api/folder-picker/select-path', { data: { path: root } });
  expect(picked.ok(), await picked.text()).toBeTruthy();
  const selected = await request.post('/api/personal-assistant/folder-digest/scan', {
    data: { selection_token: (await picked.json()).selection_token }
  });
  expect(selected.ok(), await selected.text()).toBeTruthy();
  const offer = (await selected.json()).offer;
  expect(offer?.id).toBeTruthy();
  const declined = await request.post(
    `/api/personal-assistant/folder-digest/offers/${offer.id}/decide`,
    { data: { decision: 'no', request_id: randomUUID() } }
  );
  expect(declined.ok(), await declined.text()).toBeTruthy();
}

async function automatic(page: Page) {
  const review = page.locator('#workspaceCreationReviewPanel');
  await review.getByRole('button', { name: 'Use automatic placement', exact: true }).click();
  const confirm = review.getByRole('button', { name: 'Create workspace', exact: true });
  await expect(confirm)
    .toBeEnabled({ timeout: 5000 })
    .catch(async error => {
      const state = await page.evaluate(() => {
        const manager = (window as any).sessionManager;
        const build = manager.workspaceBuild;
        return {
          draftUser: build?.draftUser,
          inFlight: Boolean(build?.draftInFlight),
          timer: Boolean(manager.workspaceBuildTimer),
          busy: build?.busy,
          paused: Boolean(build?.pausePromise),
          pending: manager.workspaceBuildAttachmentPending(build),
          problem: manager.ordinaryBuildReviewProblem()
        };
      });
      throw new Error(`${error}\nRead-only build state: ${JSON.stringify(state)}`);
    });
  await confirm.click();
  await expect(
    review.getByRole('heading', { name: 'Workspace created', exact: true })
  ).toBeVisible();
  return page.evaluate(
    () => (window as any).sessionManager.workspaceCreatorContext.ordinaryReview.result.id as string
  );
}

async function captureSourceReview(
  page: Page,
  name: string,
  review = page.locator('#workspaceCreationReviewPanel')
) {
  await review.getByRole('heading').first().scrollIntoViewIfNeeded();
  await page.screenshot({
    path: `tasks/evidence-unified-workspace-placement/group-3/${name}-review.png`,
    timeout: 10000
  });
  await review
    .locator('.assisted-placement__slots')
    .evaluate(element => element.scrollIntoView({ block: 'start' }));
  await page.screenshot({
    path: `tasks/evidence-unified-workspace-placement/group-3/${name}-resources.png`,
    timeout: 10000
  });
  await review
    .getByRole('button', { name: 'Use automatic placement', exact: true })
    .scrollIntoViewIfNeeded();
  await page.screenshot({
    path: `tasks/evidence-unified-workspace-placement/group-3/${name}-actions.png`,
    timeout: 10000
  });
}

async function finishManualReview(page: Page) {
  const review = page.locator('#workspaceCreationReviewPanel');
  for (let step = 0; step < 4 && !(await review.isVisible()); step++) {
    await page.locator('#wizardNextBtn').click();
  }
  await expect(review).toBeVisible();
  return review;
}

test.describe('ordinary attached-source review on a real isolated host', () => {
  test.skip(
    process.env.ORI_TEST_REVIEW_SOURCES !== '1',
    'requires isolated LM_STUDIO_BASE_URL=http://127.0.0.1:8940/v1; no paid provider'
  );
  let model: Awaited<ReturnType<typeof startJanitorBuildModel>>;
  test.beforeAll(async ({ request }) => {
    model = await startJanitorBuildModel();
    model.scenario({ mode: 'ordinary', createNow: true });
    await prepareFolderCreationRuntime(request);
  });
  test.afterAll(async () => {
    await model?.();
  });

  test('an explicitly declined folder remains usable as a generic source and retains edits across cancelled replacement and map Back', async ({
    page,
    request
  }) => {
    const root = realpathSync(mkdtempSync(join(tmpdir(), 'ori-source-review-')));
    // Explicitly decline automatic setup for this synthetic source through its
    // real owner first. Manual attachment is still allowed; it does not revive
    // the declined specialized proposal or invent a recognition response.
    writeFileSync(join(root, 'Set.als'), 'Synthetic source bytes; no application is launched.\n', {
      mode: 0o600
    });
    const bytes = readFileSync(join(root, 'Set.als'));
    const files = readdirSync(root);
    await declineAutomaticSetup(request, root);
    const before = await snapshot(request),
      calls = model.calls();
    await attachFirst(page, request, root);
    await expect(page.locator('#workspaceBuildPlanReviewSetup')).toHaveText(
      'Review & place workspace'
    );
    await expect(page.locator('#workspaceBuildPlanReviewSetup')).toBeEnabled();
    await page.locator('#workspaceBuildPlanReviewSetup').click();
    const review = await finishManualReview(page);
    const name = `Generic source review ${randomUUID().slice(0, 8)}`;
    const brief = 'Keep my edited folder brief exact. 日本語';
    await review.locator('[data-placement-field="name"]').fill(name);
    await review.locator('[data-placement-field="name"]').blur();
    await review.locator('[data-placement-field="mission"]').fill(brief);
    await review.locator('[data-placement-field="mission"]').blur();
    await expect(
      review.getByRole('region', { name: 'Attached folder', exact: true })
    ).toContainText(root);
    expect(await snapshot(request)).toEqual(before);
    await review.getByRole('button', { name: 'Change folder', exact: true }).click();
    await page
      .locator('#workspaceBuildFolderChooser')
      .getByRole('button', { name: 'Cancel', exact: true })
      .click();
    await expect(review).toBeVisible();
    await expect(review.locator('[data-placement-field="mission"]')).toHaveValue(brief);
    await expect(
      review.getByRole('region', { name: 'Attached folder', exact: true })
    ).toContainText(root);
    await page.locator('#addFolderModal .modal-body').evaluate(element => {
      element.scrollTop = 0;
    });
    await page.screenshot({
      path: 'tasks/evidence-unified-workspace-placement/group-3/generic-folder-review.png'
    });
    await captureSourceReview(page, 'generic-folder');
    await review.getByRole('button', { name: 'Place & create on map', exact: true }).click();
    await expect(page.locator('[data-ws-map-placement-preview]')).toBeVisible();
    await expect(page.locator('#addFolderModal')).toBeHidden();
    await page.screenshot({
      path: 'tasks/evidence-unified-workspace-placement/group-3/generic-folder-map.png'
    });
    expect(await snapshot(request)).toEqual(before);
    await page.locator('[data-ws-map-viewport]').focus();
    await page.keyboard.press('Escape');
    await expect(review).toBeVisible();
    await expect(review.locator('[data-placement-field="name"]')).toHaveValue(name);
    expect(await snapshot(request)).toEqual(before);
    const id = await automatic(page);
    await review.screenshot({
      path: 'tasks/evidence-unified-workspace-placement/group-3/generic-folder-result.png'
    });
    const canonical = await (await request.get(`/api/workspaces/${id}`)).json();
    expect(canonical.name).toBe(name);
    expect(canonical.mission_state.mission).toBe(brief);
    expect(canonical.mission_state.mission_execution_count || 0).toBe(0);
    expect(await snapshot(request)).toEqual({
      workspaces: [...before.workspaces, id].sort(),
      agents: before.agents
    });
    const dirs = (await (await request.get(`/api/workspaces/${id}/directories`)).json())
      .directories;
    expect(dirs.filter((directory: { path: string }) => directory.path === root)).toHaveLength(1);
    expect(readFileSync(join(root, 'Set.als'))).toEqual(bytes);
    expect(readdirSync(root)).toEqual(files);
    expect(model.calls()).toBe(calls + 1);
  });

  test('an explicitly selected File Janitor draft shares review but retains its own preparation and scan authority', async ({
    page,
    request
  }) => {
    const root = realpathSync(mkdtempSync(join(tmpdir(), 'ori-build-janitor-review-')));
    const file = join(root, 'invoice.pdf');
    writeFileSync(file, 'synthetic source bytes', { mode: 0o600 });
    const old = new Date(Date.now() - 3600000);
    utimesSync(file, old, old);
    // A genuine Home dump offer (rather than an unanswered tiny-folder result)
    // is the independently declined prerequisite for this generic draft.
    for (let index = 0; index < 24; index++) {
      const extension = ['txt', 'csv', 'png', 'zip', 'docx', 'pdf'][index % 6];
      const extra = join(root, `fixture-${index}.${extension}`);
      writeFileSync(extra, 'synthetic source bytes', { mode: 0o600 });
      utimesSync(extra, old, old);
    }
    const originals = readdirSync(root);
    await declineAutomaticSetup(request, root);
    const before = await snapshot(request),
      calls = model.calls();
    model.scenario({ mode: 'success', createNow: true });
    try {
      await attachFirst(page, request, root);
      const review = page.locator('#assistedWorkspacePlacementPanel');
      await expect(review).toBeVisible();
      await expect(review).toContainText('Filed/');
      await expect(review).toContainText('No file contents');
      expect(await snapshot(request)).toEqual(before);
      const team = await page.evaluate(() =>
        (window as any).sessionManager.workspaceBuild.janitorReview.team.map(
          (member: { name: string }) => member.name
        )
      );
      expect(team).toHaveLength(1);
      const confirm = review.getByRole('button', { name: 'Create workspace & scan', exact: true });
      await expect(confirm).toBeEnabled();
      await confirm.click();
      await expect(
        review.getByRole('heading', { name: 'Workspace prepared', exact: true })
      ).toBeVisible({ timeout: 20000 });
      await expect(review).toContainText('25 files in the first filing review');
      const id = await page.evaluate(
        () => (window as any).sessionManager.workspaceBuild.placement.workspaceID
      );
      expect(await snapshot(request)).toEqual({
        workspaces: [...before.workspaces, id].sort(),
        agents: [...before.agents, ...team].sort()
      });
      const status = (await (await request.get(`/api/workspaces/${id}/file-janitor`)).json())
        .status;
      expect(status.settings.root_path).toBe(root);
      expect(status.settings.setup_decision.mode).toBe('manual');
      expect(status.settings.paused).toBeTruthy();
      for (const original of originals)
        expect(readFileSync(join(root, original), 'utf8')).toBe('synthetic source bytes');
      expect(readdirSync(root).sort()).toEqual([...originals, 'Filed'].sort());
      expect(model.calls()).toBe(calls + 1);
    } finally {
      model.scenario({ mode: 'ordinary', createNow: true });
    }
  });

  test('a recognized software plan uses its fixed team and existing setup owner with an optional unstarted first task', async ({
    page,
    request
  }) => {
    const root = realpathSync(mkdtempSync(join(tmpdir(), 'ori-software-review-')));
    const bytes = 'module example.org/synthetic-review\n\ngo 1.24\n';
    writeFileSync(join(root, 'go.mod'), bytes, { mode: 0o600 });
    await abandonOpenBuild(request);
    const before = await snapshot(request);
    const calls = model.calls();
    await attachFirst(page, request, root);
    const evidenceReview = page.locator('#workspaceBuildPlanReviewSetup');
    await expect(evidenceReview).toHaveText('Review software setup');
    expect(await snapshot(request)).toEqual(before);
    expect(model.calls()).toBe(calls);
    await evidenceReview.click();
    const review = page.locator('#workspaceBuildSetupReview');
    await expect(review)
      .toBeVisible()
      .catch(async error => {
        const state = await page.evaluate(() => {
          const session = (window as any).sessionManager.workspaceBuild?.session;
          return {
            status: session?.status,
            plan: session?.plan,
            recognition: session?.folder?.recognition,
            message: (window as any).sessionManager.workspaceBuild?.readinessMessage
          };
        });
        throw new Error(`${error}\nRead-only setup state: ${JSON.stringify(state)}`);
      });
    await expect(review).toContainText('Development Assistant');
    await expect(review).toContainText('first task is prepared, not started');
    await expect(review.getByRole('button', { name: 'Change blueprint', exact: true })).toHaveCount(
      0
    );
    expect(await snapshot(request)).toEqual(before);
    expect(model.calls()).toBe(calls);
    const name = `Reviewed software ${randomUUID().slice(0, 8)}`;
    const brief = 'Preserve the exact software brief. 日本語';
    await review.locator('[data-placement-field="name"]').fill(name);
    await review.locator('[data-placement-field="name"]').blur();
    await expect
      .poll(() =>
        page.evaluate(
          () => (window as any).sessionManager.workspaceBuild.session.plan.visual.entity.name
        )
      )
      .toBe(name);
    await review.locator('[data-placement-field="mission"]').fill(brief);
    await review.locator('[data-placement-field="mission"]').blur();
    await review.getByRole('button', { name: 'Use automatic placement', exact: true }).click();
    const { buildID, offerID } = await page.evaluate(() => {
      const s = (window as any).sessionManager.workspaceBuild.session;
      return { buildID: s.id as string, offerID: s.folder.recognition.offer_id as string };
    });
    const confirm = review.getByRole('button', { name: 'Create & prepare workspace', exact: true });
    await expect(confirm)
      .toBeEnabled()
      .catch(async error => {
        const state = await page.evaluate(() => {
          const m = (window as any).sessionManager,
            b = m.workspaceBuild;
          return {
            pressable: m.workspaceBuildSetupPressable(),
            nameCurrent: m.workspaceBuildSetupNameCurrent(),
            name: (document.getElementById('folderNameInput') as HTMLInputElement)?.value,
            entity: b.session.plan.visual?.entity,
            draftUser: b.draftUser,
            draftConflict: b.draftConflict,
            draftError: b.draftError,
            busy: b.busy,
            readinessBusy: b.readinessBusy,
            readinessStale: b.readinessStale,
            model: m.workspaceBuildModelView(),
            expires: b.session.folder_expires_at,
            status: b.session.status
          };
        });
        throw new Error(`${error}\nRead-only final state: ${JSON.stringify(state)}`);
      });
    await captureSourceReview(page, 'software', review);
    await confirm.click();
    await expect
      .poll(
        async () =>
          (await (await request.get(`/api/workspaces/build-sessions/${buildID}`)).json()).session
            .status
      )
      .toBe('created');
    const session = (await (await request.get(`/api/workspaces/build-sessions/${buildID}`)).json())
      .session;
    const after = await snapshot(request);
    expect(after.workspaces.filter((id: string) => !before.workspaces.includes(id))).toEqual([
      session.created_workspace_id
    ]);
    const canonical = await request.get(`/api/workspaces/${session.created_workspace_id}`);
    expect(canonical.ok(), await canonical.text()).toBeTruthy();
    const ws = await canonical.json();
    expect(session.setup_result.id).toBe(offerID);
    expect(session.setup_result.outcome.workspace_id).toBe(session.created_workspace_id);
    expect(ws.shared_data.folder_digest_offer_id).toBe(offerID);
    await expect(
      review.getByRole('heading', { name: 'Workspace created', exact: true })
    ).toBeVisible();
    await review.screenshot({
      path: 'tasks/evidence-unified-workspace-placement/group-3/software-result.png'
    });
    expect(ws.name).toBe(name);
    expect(ws.mission_state.mission).toBe(brief);
    expect(ws.agent_instances).toHaveLength(1);
    expect(ws.tasks).toHaveLength(1);
    expect(ws.tasks[0].status).toBe('pending');
    expect(readFileSync(join(root, 'go.mod'), 'utf8')).toBe(bytes);
    expect(readdirSync(root)).toEqual(['go.mod']);
    expect(model.calls()).toBe(calls);
  });

  for (const failLayout of [false, true])
    test(`recognized software map confirmation uses the same source owner and Escape preserves its reviewed draft${failLayout ? ' with layout-only repair' : ''}`, async ({
      page,
      request
    }) => {
      const root = realpathSync(mkdtempSync(join(tmpdir(), 'ori-software-map-')));
      const bytes = 'module example.org/map-proof\n\ngo 1.24\n';
      writeFileSync(join(root, 'go.mod'), bytes, { mode: 0o600 });
      const before = await snapshot(request),
        calls = model.calls();
      await attachFirst(page, request, root);
      await page.locator('#workspaceBuildPlanReviewSetup').click();
      const review = page.locator('#workspaceBuildSetupReview');
      await expect(review).toBeVisible();
      await review.getByRole('button', { name: 'Place & create on map', exact: true }).click();
      await expect(page.locator('#addFolderModal')).toBeHidden();
      await expect(page.locator('[data-ws-map-placement-preview]')).toBeVisible();
      expect(await snapshot(request)).toEqual(before);
      await page.keyboard.press('Escape');
      await expect(review).toBeVisible();
      expect(await snapshot(request)).toEqual(before);
      await review.getByRole('button', { name: 'Place & create on map', exact: true }).click();
      await expect(page.locator('#addFolderModal')).toBeHidden();
      const place = page.locator('[data-ws-map-placement-confirm]');
      const canvas = page.locator('[data-ws-map-viewport]').filter({ visible: true }).first();
      await canvas.focus();
      // Other real journeys leave buildings on the map. Use the actual keyboard
      // position setter; do not bypass or relax the collision gate for this case.
      for (let nudge = 0; nudge < 30 && !(await place.isEnabled()); nudge++)
        await canvas.press('ArrowDown');
      await expect(place).toBeEnabled();
      if (!failLayout)
        await page.screenshot({
          path: 'tasks/evidence-unified-workspace-placement/group-3/software-map.png'
        });
      let setups = 0;
      page.on('request', r => {
        if (
          r.method() === 'POST' &&
          /\/build-sessions\/[^/]+\/setup$/.test(new URL(r.url()).pathname)
        )
          setups++;
      });
      if (failLayout)
        await page.route('**/api/workspace-map/layout', route =>
          route.request().method() === 'PATCH'
            ? route.fulfill({
                status: 503,
                contentType: 'application/json',
                body: '{"error":"Synthetic layout outage"}'
              })
            : route.continue()
        );
      await place.click();
      await expect(
        review.getByRole('heading', { name: 'Workspace created', exact: true })
      ).toBeVisible({ timeout: 15000 });
      const result = await page.evaluate(() => {
        const s = (window as any).sessionManager.workspaceBuild.setupReviewUI;
        return {
          id: s.result.id as string,
          failed: s.positionFailed,
          point: s.mapState.candidate,
          positionResult: s.positionResult
        };
      });
      if (failLayout)
        await review.screenshot({
          path: 'tasks/evidence-unified-workspace-placement/group-3/software-position-failure.png'
        });
      expect(result.failed).toBe(failLayout);
      expect(result.point).toBeTruthy();
      expect(setups).toBe(1);
      const after = await snapshot(request);
      expect(after.workspaces.filter((id: string) => !before.workspaces.includes(id))).toEqual([
        result.id
      ]);
      const ws = await (await request.get(`/api/workspaces/${result.id}`)).json();
      expect(ws.tasks).toHaveLength(1);
      expect(ws.tasks[0].status).toBe('pending');
      if (failLayout) {
        await page.unroute('**/api/workspace-map/layout');
        await review.getByRole('button', { name: 'Retry position only', exact: true }).click();
        await expect(
          review.getByRole('button', { name: 'Retry position only', exact: true })
        ).toHaveCount(0);
        expect(await snapshot(request)).toEqual(after);
        expect(setups).toBe(1);
      }
      expect(readFileSync(join(root, 'go.mod'), 'utf8')).toBe(bytes);
      expect(model.calls()).toBe(calls);
    });

  test('changed software evidence refuses final setup before any resources and requires new review', async ({
    page,
    request
  }) => {
    const root = realpathSync(mkdtempSync(join(tmpdir(), 'ori-software-changed-')));
    writeFileSync(join(root, 'go.mod'), 'module example.org/old\n\ngo 1.24\n', { mode: 0o600 });
    await attachFirst(page, request, root);
    await page.locator('#workspaceBuildPlanReviewSetup').click();
    const review = page.locator('#workspaceBuildSetupReview');
    await expect(review).toBeVisible();
    const before = await snapshot(request),
      calls = model.calls();
    writeFileSync(join(root, 'go.mod'), 'module example.org/changed\n\ngo 1.24\n', { mode: 0o600 });
    await review.getByRole('button', { name: 'Use automatic placement', exact: true }).click();
    const confirmation = page.waitForResponse(response =>
      /\/build-sessions\/[^/]+\/setup$/.test(new URL(response.url()).pathname)
    );
    await review.getByRole('button', { name: 'Create & prepare workspace', exact: true }).click();
    const refused = await confirmation;
    expect(refused.status()).toBe(409);
    expect(await snapshot(request)).toEqual(before);
    expect(model.calls()).toBe(calls);
    await expect(page.locator('#addFolderModal')).toBeVisible();
    await page.waitForTimeout(300);
    expect(await snapshot(request)).toEqual(before);
    await abandonOpenBuild(request);
  });

  test('software lost real setup delivery observes its canonical source result without repeating setup', async ({
    page,
    request
  }) => {
    const root = realpathSync(mkdtempSync(join(tmpdir(), 'ori-software-lost-')));
    writeFileSync(join(root, 'go.mod'), 'module example.org/lost-proof\n\ngo 1.24\n', {
      mode: 0o600
    });
    await attachFirst(page, request, root);
    await page.locator('#workspaceBuildPlanReviewSetup').click();
    const review = page.locator('#workspaceBuildSetupReview');
    await expect(review).toBeVisible();
    const before = await snapshot(request),
      calls = model.calls();
    let dispatches = 0;
    await page.route('**/api/workspaces/build-sessions/*/setup', async route => {
      dispatches++;
      const accepted = await route.fetch();
      expect(accepted.ok(), await accepted.text()).toBeTruthy();
      await route.abort('failed');
    });
    await review.getByRole('button', { name: 'Use automatic placement', exact: true }).click();
    await review.getByRole('button', { name: 'Create & prepare workspace', exact: true }).click();
    await expect(
      review.getByRole('heading', { name: 'Workspace created', exact: true })
    ).toBeVisible({ timeout: 15000 });
    expect(dispatches).toBe(1);
    const buildID = await page.evaluate(
      () => (window as any).sessionManager.workspaceBuild.session.id as string
    );
    const original = (await (await request.get(`/api/workspaces/build-sessions/${buildID}`)).json())
      .session;
    expect(original.setup_result.outcome.workspace_id).toBe(original.created_workspace_id);
    const after = await snapshot(request);
    expect(after.workspaces.filter((id: string) => !before.workspaces.includes(id))).toEqual([
      original.created_workspace_id
    ]);
    for (let read = 0; read < 2; read++)
      expect(
        (await (await request.get(`/api/workspaces/build-sessions/${buildID}`)).json()).session
          .created_workspace_id
      ).toBe(original.created_workspace_id);
    expect(await snapshot(request)).toEqual(after);
    expect(dispatches).toBe(1);
    expect(model.calls()).toBe(calls);
  });

  for (const onMap of [false, true])
    test(`the Home software offer retains its source owner behind shared ${onMap ? 'map' : 'automatic'} review`, async ({
      page,
      request
    }) => {
      const root = realpathSync(mkdtempSync(join(tmpdir(), 'ori-home-source-')));
      const bytes = 'module example.org/home-source\n\ngo 1.24\n';
      writeFileSync(join(root, 'go.mod'), bytes, { mode: 0o600 });
      const picked = await request.post('/api/folder-picker/select-path', { data: { path: root } });
      expect(picked.ok()).toBeTruthy();
      const scanned = await request.post('/api/personal-assistant/folder-digest/scan', {
        data: { selection_token: (await picked.json()).selection_token }
      });
      expect(scanned.ok(), await scanned.text()).toBeTruthy();
      const offer = (await scanned.json()).offer;
      const reviewed = await request.post(
        `/api/personal-assistant/folder-digest/offers/${offer.id}/review`,
        { data: { request_id: randomUUID() } }
      );
      expect(reviewed.ok(), await reviewed.text()).toBeTruthy();
      const before = await snapshot(request),
        calls = model.calls();
      await request.post('/api/onboarding/skip');
      await page.goto('/');
      await page.waitForFunction(() =>
        Boolean((window as any).PersonalAssistantFolder?.showHomeReview)
      );
      await page.evaluate(async id => {
        (window as any).PersonalAssistantPanel.open(
          document.getElementById('personalAssistantLauncher')
        );
        await (window as any).PersonalAssistantFolder.showHomeReview(id);
      }, offer.id);
      const showNeeds = page.getByRole('button', { name: 'Show', exact: true });
      if (await showNeeds.isVisible()) await showNeeds.click();
      await page.locator('[data-folder-action="setup"]').filter({ visible: true }).first().click();
      const review = page.locator('[data-source-creation-review]');
      await expect(review).toBeVisible();
      expect(await snapshot(request)).toEqual(before);
      if (onMap) {
        await review.getByRole('button', { name: 'Place & create on map', exact: true }).click();
        await expect(page.locator('#personalAssistantPanel')).toBeHidden();
        const place = page.locator('[data-ws-map-placement-confirm]');
        const canvas = page.locator('[data-ws-map-viewport]').filter({ visible: true }).first();
        await canvas.focus();
        for (let nudge = 0; nudge < 40 && !(await place.isEnabled()); nudge++)
          await canvas.press('ArrowDown');
        await expect(place).toBeEnabled();
        expect(await snapshot(request)).toEqual(before);
        await place.click();
      } else {
        await review.getByRole('button', { name: 'Use automatic placement', exact: true }).click();
        await review
          .getByRole('button', { name: 'Create & prepare workspace', exact: true })
          .click();
      }
      await expect(
        review.getByRole('heading', { name: 'Workspace created', exact: true })
      ).toBeVisible({ timeout: 15000 });
      const current = await page.evaluate(() => (window as any).PersonalAssistantFolder.current());
      const after = await snapshot(request);
      expect(after.workspaces.filter((id: string) => !before.workspaces.includes(id))).toEqual([
        current.outcome.workspace_id
      ]);
      expect(readFileSync(join(root, 'go.mod'), 'utf8')).toBe(bytes);
      expect(model.calls()).toBe(calls);
    });

  test('a real page link keeps its observed identity, refuses private replacement and creates only after reviewed confirmation', async ({
    page,
    request
  }) => {
    await abandonOpenBuild(request);
    const before = await snapshot(request),
      calls = model.calls();
    await request.post('/api/onboarding/skip');
    await page.goto('/');
    await page.locator('#cockpitCreateWorkspaceBtn').click();
    const pane = page.locator('#workspaceBuildPane');
    await pane.getByRole('button', { name: 'Add link', exact: true }).click();
    await page.locator('#workspaceBuildLinkInput').fill('https://example.org/');
    await page
      .locator('#workspaceBuildLinkField')
      .getByRole('button', { name: 'Attach', exact: true })
      .click();
    await expect(page.locator('#workspaceBuildLinkName')).toBeVisible();
    await page
      .locator('#workspaceBuildComposerInput')
      .fill('Create an agentless notebook from this page.');
    const turn = page.waitForResponse(response =>
      /\/build-sessions\/[^/]+\/turns$/.test(new URL(response.url()).pathname)
    );
    await pane.getByRole('button', { name: 'Send', exact: true }).click();
    expect((await turn).ok()).toBeTruthy();
    await expect(page.locator('#folderNameInput')).toHaveValue(/Synthetic/);
    await expect(page.locator('#workspaceBuildEditDraftBtn')).toBeEnabled();
    await page.locator('#workspaceBuildEditDraftBtn').click();
    const review = await finishManualReview(page);
    const row = review.getByRole('region', { name: 'Attached link', exact: true });
    const name = `Linked notebook ${randomUUID().slice(0, 8)}`;
    const brief = 'Keep a notebook from this reviewed page when I choose.';
    await review.locator('[data-placement-field="name"]').fill(name);
    await review.locator('[data-placement-field="name"]').blur();
    await review.locator('[data-placement-field="mission"]').fill(brief);
    await review.locator('[data-placement-field="mission"]').blur();
    await expect(row).toContainText('https://example.org/');
    expect(await snapshot(request)).toEqual(before);
    await review.getByRole('button', { name: 'Edit link', exact: true }).click();
    await page.locator('#workspaceBuildLinkInput').fill('http://127.0.0.1/private');
    await page
      .locator('#workspaceBuildLinkField')
      .getByRole('button', { name: 'Attach', exact: true })
      .click();
    await expect(page.locator('#workspaceBuildLinkStatus')).toContainText('public');
    await page
      .locator('#workspaceBuildLinkField')
      .getByRole('button', { name: 'Cancel', exact: true })
      .click();
    await page.locator('#workspaceBuildEditDraftBtn').click();
    await finishManualReview(page);
    await expect(row).toContainText('https://example.org/');
    await page.locator('#addFolderModal .modal-body').evaluate(element => {
      element.scrollTop = 0;
    });
    await page.screenshot({
      path: 'tasks/evidence-unified-workspace-placement/group-3/link-review.png'
    });
    await captureSourceReview(page, 'link');
    let dispatched = 0;
    await page.route('**/api/workspaces/create-operations/*/confirm', async route => {
      dispatched++;
      // Lose delivery only after the real host completed its real operation.
      const actual = await route.fetch();
      expect(actual.status()).toBe(201);
      await route.abort('failed');
    });
    const id = await automatic(page);
    expect(dispatched).toBe(1);
    await review.screenshot({
      path: 'tasks/evidence-unified-workspace-placement/group-3/link-result.png'
    });
    const notes = await (await request.get(`/api/workspaces/${id}/notes`)).json();
    expect(JSON.stringify(notes)).toContain('Sources');
    const canonical = await (await request.get(`/api/workspaces/${id}`)).json();
    expect(canonical.name).toBe(name);
    expect(canonical.mission_state.mission).toBe(brief);
    expect(canonical.mission_state.mission_execution_count || 0).toBe(0);
    const after = await snapshot(request);
    expect(after).toEqual({ workspaces: [...before.workspaces, id].sort(), agents: before.agents });
    const operationID = canonical.shared_data.workspace_creation_operation_id;
    for (let read = 0; read < 3; read++) {
      const receipt = await (
        await request.get(`/api/workspaces/create-operations/${operationID}`)
      ).json();
      expect(receipt.operation.state).toBe('complete');
      expect(receipt.result.folder.id).toBe(id);
    }
    await page.reload();
    expect(await snapshot(request)).toEqual(after);
    expect(await (await request.get(`/api/workspaces/${id}/notes`)).json()).toEqual(notes);
    expect(model.calls()).toBe(calls + 1);
  });
});
