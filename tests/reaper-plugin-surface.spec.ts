import { test, expect } from '@playwright/test';
import { mkdirSync, readFileSync } from 'node:fs';
import path from 'node:path';

const pluginPath = process.env.ORI_REAPER_PLUGIN_PATH;
const pluginName = 'reaper-plugin';
const templateID = 'plugin:reaper-plugin:reaper-song';
const surfaceKey = 'plugin:reaper-plugin:reaper-live-control:live-control';
const tidySurfaceKey = 'plugin:reaper-plugin:reaper-live-control:project-tidy';
const evidenceDir = process.env.ORI_REAPER_EVIDENCE_DIR;

async function cleanupAssistantTopology(
  request: import('@playwright/test').APIRequestContext,
  stationID: string,
  projectIDs: string[]
) {
  for (const projectID of projectIDs) {
    const current = await request
      .get(`/api/workspaces/${projectID}/assistant-program`)
      .then(response => response.json())
      .catch(() => null);
    if (current?.state_revision) {
      const reviewed = await request
        .post(`/api/workspaces/${stationID}/assistant-program/disconnect/review`, {
          data: { project_workspace_id: projectID, state_revision: current.state_revision }
        })
        .then(response => response.json())
        .catch(() => null);
      if (reviewed?.token)
        await request
          .post(`/api/workspaces/${stationID}/assistant-program/disconnect/commit`, {
            data: { token: reviewed.token, idempotency_key: `cleanup-${projectID}` }
          })
          .catch(() => {});
    }
    await request.delete(`/api/workspaces/${projectID}`).catch(() => {});
  }
  const home = await request
    .get(`/api/workspaces/${stationID}/assistant-program`)
    .then(response => response.json())
    .catch(() => null);
  if (home?.state_revision) {
    const reviewed = await request
      .post(`/api/workspaces/${stationID}/assistant-program/remove-home/review`, {
        data: { state_revision: home.state_revision }
      })
      .then(response => response.json())
      .catch(() => null);
    if (reviewed?.token)
      await request
        .post(`/api/workspaces/${stationID}/assistant-program/remove-home/commit`, {
          data: { token: reviewed.token }
        })
        .catch(() => {});
  }
  await request.delete(`/api/workspaces/${stationID}`).catch(() => {});
}

async function captureEvidence(page: import('@playwright/test').Page, name: string) {
  if (!evidenceDir) return;
  mkdirSync(evidenceDir, { recursive: true });
  await page.screenshot({ path: path.join(evidenceDir, name), fullPage: true });
}

async function createReviewedGroupedWorkspace(
  request: import('@playwright/test').APIRequestContext,
  data: Record<string, unknown>,
  idempotencyKey: string
) {
  let review = await request.post('/api/workspaces', {
    data: { ...data, group_composition: 'grouped', group_requirement_review: true }
  });
  expect(review.ok(), await review.text()).toBeTruthy();
  let reviewed = (await review.json()).group_requirement_review;
  if (reviewed?.state === 'home_creation_review_required') {
    const homeReview = await request.post('/api/workspaces/group-requirement/home/review', {
      data: { template_id: data.template_id }
    });
    const homeReviewText = await homeReview.text();
    expect(homeReview.ok(), homeReviewText).toBeTruthy();
    const homeReviewed = JSON.parse(homeReviewText).group_requirement_review;
    expect(homeReviewed?.state, homeReviewText).toBe('ready_grouped');
    expect(homeReviewed?.home_will_be_created, homeReviewText).toBeTruthy();
    const homeCommit = await request.post('/api/workspaces/group-requirement/home/commit', {
      data: {
        group_review_token: homeReviewed.review_token,
        idempotency_key: `${idempotencyKey}-home`,
        template_id: data.template_id
      }
    });
    const homeCommitText = await homeCommit.text();
    expect(homeCommit.ok(), homeCommitText).toBeTruthy();
    expect(JSON.parse(homeCommitText).group_requirement?.state, homeCommitText).toBe('home_ready');
    review = await request.post('/api/workspaces', {
      data: { ...data, group_composition: 'grouped', group_requirement_review: true }
    });
    expect(review.ok(), await review.text()).toBeTruthy();
    reviewed = (await review.json()).group_requirement_review;
  }
  expect(reviewed?.state).toBe('ready_grouped');
  expect(reviewed?.review_token).toBeTruthy();
  return request.post('/api/workspaces', {
    data: {
      ...data,
      group_composition: 'grouped',
      group_review_token: reviewed.review_token,
      idempotency_key: idempotencyKey
    }
  });
}

test.skip(
  !pluginPath,
  'set ORI_REAPER_PLUGIN_PATH to the locally built coordinated plugin checkout'
);

test('Create Workspace prepares the required Home before creating and staffing the project', async ({
  page,
  request
}) => {
  await request.post('/api/onboarding/skip').catch(() => {});
  await request.delete(`/api/plugins/${pluginName}`).catch(() => {});
  const install = await request.post('/api/plugins/install', {
    data: { source: pluginPath, confirm: true }
  });
  expect(install.ok(), await install.text()).toBeTruthy();
  const enable = await request.post(`/api/plugins/${pluginName}/enable`);
  expect(enable.ok(), await enable.text()).toBeTruthy();

  let workspaceID = '';
  let stationID = '';
  try {
    await page.goto('/workspaces');
    await page.evaluate(() => {
      const modal = document.getElementById('addFolderModal');
      // @ts-expect-error bootstrap is a page global
      window.bootstrap.Modal.getOrCreateInstance(modal).show();
    });
    await expect(page.locator('#addFolderModal')).toBeVisible();
    const reaperCard = page.locator('#templatePicker').getByRole('radio', {
      name: 'Reaper Song',
      exact: true
    });
    await expect(reaperCard).toBeVisible();
    await reaperCard.click();
    await page.locator('#wizardNextBtn').click();
    await expect(page.locator('#wizardStep2')).toBeVisible();
    const workspaceName = `Wizard REAPER ${Date.now().toString(36)}`;
    await page.locator('#folderNameInput').fill(workspaceName);
    const openProject = page.locator('#projectTemplateOpenAfterCreateToggle');
    if (await openProject.isChecked()) await openProject.uncheck();

    await page.locator('#wizardNextBtn').click();
    await expect(page.locator('#wizardStep3')).toBeVisible();
    await expect(page.locator('#wizardStep3Title')).toHaveText(
      'Staff your music production assistants'
    );
    await expect(page.locator('#workspaceAssistantProgramCreate')).toBeVisible();
    await expect(page.locator('#wizardStep3')).toContainText('Mix Engineer');
    await expect(page.locator('#wizardStep3')).toContainText('Songwriter');
    await expect(page.locator('#wizardStep3')).toContainText('Missing');

    await page.locator('#wizardNextBtn').click();
    await expect(page.locator('#wizardStep4')).toBeVisible();
    await expect(page.locator('#workspaceReviewSummary')).toContainText(
      'Required group: Music Production Home'
    );

    const homeResponsePromise = page.waitForResponse(
      response =>
        response.url().endsWith('/api/workspaces/group-requirement/home/commit') &&
        response.request().method() === 'POST'
    );
    page.once('dialog', async dialog => {
      expect(dialog.message()).toContain('must exist before this workspace can be created');
      expect(dialog.message()).toContain('creates only the empty group');
      await dialog.accept();
    });
    await page.locator('#createFolderBtn').click();
    const homeResponse = await homeResponsePromise;
    expect(homeResponse.ok(), await homeResponse.text()).toBeTruthy();
    const prepared = (await homeResponse.json()).group_requirement;
    expect(prepared.state).toBe('home_ready');
    expect(prepared.home_created).toBeTruthy();
    stationID = prepared.home_workspace_id;
    await expect(page.locator('#addFolderModal')).toBeVisible();
    await expect(page.locator('#workspaceReviewReadiness')).toContainText(
      'No workspace was created'
    );
    await expect(page.locator('#workspaceReviewReadiness')).toContainText(
      'Click Confirm create to create the workspace separately'
    );
    await expect(page.locator('#createFolderBtn')).toHaveText(`Confirm create “${workspaceName}”`);
    const homeOnly = await request.get('/api/workspaces').then(response => response.json());
    const homeOnlyRecords = Array.isArray(homeOnly.folders) ? homeOnly.folders : [];
    expect(homeOnlyRecords.some((item: { id?: string }) => item.id === stationID)).toBeTruthy();
    await expect(page.locator(`.ws-map-district[data-group-id="${stationID}"]`)).toBeAttached();
    expect(
      homeOnlyRecords.some((item: { name?: string }) => item.name === workspaceName)
    ).toBeFalsy();
    await captureEvidence(page, 'music-producer-01-home-before-project.png');

    // Cancelling after Home preparation closes the wizard but leaves only the
    // reviewed empty Home. Reopening must obtain another fresh project receipt.
    await page.locator('#addFolderModal .modal-footer [data-bs-dismiss="modal"]').click();
    await expect(page.locator('#addFolderModal')).toBeHidden();
    const afterCancel = await request.get('/api/workspaces').then(response => response.json());
    expect((afterCancel.folders || []).map((item: { id: string }) => item.id)).toEqual([stationID]);

    await page.evaluate(() => {
      const modal = document.getElementById('addFolderModal');
      // @ts-expect-error bootstrap is a page global
      window.bootstrap.Modal.getOrCreateInstance(modal).show();
    });
    await expect(page.locator('#addFolderModal')).toBeVisible();
    await page.locator('#templatePicker').getByRole('radio', { name: 'Reaper Song' }).click();
    await page.locator('#wizardNextBtn').click();
    await page.locator('#folderNameInput').fill(workspaceName);
    if (await openProject.isChecked()) await openProject.uncheck();
    await page.locator('#wizardNextBtn').click();
    await page.locator('#wizardNextBtn').click();
    await expect(page.locator('#wizardStep4')).toBeVisible();
    const freshReviewPromise = page.waitForResponse(
      response =>
        response.url().endsWith('/api/workspaces') &&
        response.request().method() === 'POST' &&
        response.request().postDataJSON()?.group_requirement_review === true
    );
    await page.locator('#createFolderBtn').click();
    const freshReview = await freshReviewPromise;
    expect(freshReview.ok(), await freshReview.text()).toBeTruthy();
    await expect(page.locator('#createFolderBtn')).toHaveText(`Confirm create “${workspaceName}”`);

    const createResponsePromise = page.waitForResponse(
      response =>
        response.url().endsWith('/api/workspaces') &&
        response.request().method() === 'POST' &&
        Boolean(response.request().postDataJSON()?.group_review_token)
    );
    const groupPlacementPromise = page.waitForRequest(request => {
      if (!request.url().endsWith('/api/workspace-map/layout') || request.method() !== 'PATCH') {
        return false;
      }
      return request
        .postDataJSON()
        ?.operations?.some((operation: { op?: string }) => operation.op === 'set_positions');
    });
    await page.locator('#createFolderBtn').click();
    const createResponse = await createResponsePromise;
    expect(createResponse.ok(), await createResponse.text()).toBeTruthy();
    const createPayload = createResponse.request().postDataJSON();
    expect(createPayload.create_required_home).toBeUndefined();
    expect(createPayload.template_agent_review).toBeUndefined();
    expect(createPayload.assistant_hire).toBeUndefined();
    const created = await createResponse.json();
    workspaceID = created.folder.id;
    const placementRequest = await groupPlacementPromise;
    const positions = placementRequest
      .postDataJSON()
      .operations.find((operation: { op?: string }) => operation.op === 'set_positions')?.positions;
    expect(positions?.[stationID]).toBeTruthy();
    expect(positions?.[workspaceID]).toBeTruthy();
    expect(positions[workspaceID].x).toBeGreaterThanOrEqual(positions[stationID].x);
    expect(positions[workspaceID].x).toBeLessThan(positions[stationID].x + 176);
    expect(positions[workspaceID].y).toBeGreaterThanOrEqual(positions[stationID].y);
    expect(positions[workspaceID].y).toBeLessThan(positions[stationID].y + 170);
    await page.waitForURL(`**/workspaces/${encodeURIComponent(created.folder.folder_slug)}`, {
      timeout: 20_000
    });

    const programResponse = await request.get(`/api/workspaces/${workspaceID}/assistant-program`);
    expect(programResponse.ok(), await programResponse.text()).toBeTruthy();
    const program = await programResponse.json();
    expect(program.station_id).toBe(stationID);
    const createdWorkspace = await request
      .get(`/api/workspaces/${workspaceID}`)
      .then(response => response.json());
    expect(createdWorkspace.parent_id).toBe(stationID);
    expect(createdWorkspace.group_requirement_status?.state).toBe('ready_grouped');

    await page.goto('/workspaces');
    const district = page.locator(`.ws-map-district[data-group-id="${stationID}"]`);
    const tile = page.locator(`.ws-map-tile[data-ws-id="${workspaceID}"]`);
    await expect(district).toBeVisible();
    await expect(tile).toBeVisible();
    const geometry = await page.evaluate(
      ({ homeID, childID }) => {
        const group = document.querySelector(`.ws-map-district[data-group-id="${homeID}"]`);
        const child = document.querySelector(`.ws-map-tile[data-ws-id="${childID}"]`);
        const value = (element: Element | null, property: string) =>
          Number.parseFloat((element as HTMLElement | null)?.style[property] || 'NaN');
        return {
          group: {
            x: value(group, 'left'),
            y: value(group, 'top'),
            width: value(group, 'width'),
            height: value(group, 'height')
          },
          child: { x: value(child, 'left'), y: value(child, 'top') }
        };
      },
      { homeID: stationID, childID: workspaceID }
    );
    expect(geometry.child.x).toBeGreaterThanOrEqual(geometry.group.x);
    expect(geometry.child.y).toBeGreaterThanOrEqual(geometry.group.y);
    expect(geometry.child.x).toBeLessThan(geometry.group.x + geometry.group.width);
    expect(geometry.child.y).toBeLessThan(geometry.group.y + geometry.group.height);
    await page.locator('[data-map-zoom-out]').click();
    await page.waitForTimeout(650); // let the Map's deliberate rise animation finish
    await captureEvidence(page, 'music-producer-02-group-aware-map.png');
  } finally {
    if (stationID)
      await cleanupAssistantTopology(request, stationID, workspaceID ? [workspaceID] : []);
    await request.delete(`/api/plugins/${pluginName}`).catch(() => {});
  }
});

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
  const capture = async (name: string) => captureEvidence(page, name);
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

  const create = await createReviewedGroupedWorkspace(
    request,
    {
      name: `Plugin REAPER ${Date.now().toString(36)}`,
      description: 'Disposable plugin-backed REAPER fixture',
      template_id: templateID,
      create_template_agents: true
    },
    `plugin-reaper-${Date.now().toString(36)}`
  );
  expect(create.ok(), await create.text()).toBeTruthy();
  const created = await create.json();
  const workspace = created.folder;
  expect(created.project_warning).toBeUndefined();
  // Shared roles belong to the assistant station, never duplicated into each
  // song workspace's local agent_instances array.
  expect(workspace.agent_instances || []).toHaveLength(0);
  const linkedAssistantWorkspaces: Array<{ id: string; folder_slug: string; name: string }> = [];
  let assistantStationID = '';
  const assistantAgentNames: string[] = [];

  try {
    const assistantBeforeResponse = await request.get(
      `/api/workspaces/${workspace.id}/assistant-program`
    );
    expect(assistantBeforeResponse.ok(), await assistantBeforeResponse.text()).toBeTruthy();
    const assistantBefore = await assistantBeforeResponse.json();
    expect(assistantBefore.available).toBeTruthy();
    expect(assistantBefore.stage_label).toBe('Helper');
    await page.goto(`/workspaces/${encodeURIComponent(workspace.folder_slug)}`);
    await expect(page.getByRole('link', { name: 'Open Music Production Home' })).toBeVisible({
      timeout: 15_000
    });
    await page.goto(`/workspaces/${encodeURIComponent(workspace.folder_slug)}/assistant`);

    let hired = assistantBefore;
    let producerName = String(assistantBefore.primary_name || '');
    await expect(page.getByRole('heading', { name: 'Music Production Home' })).toBeVisible({
      timeout: 15_000
    });
    await capture('music-producer-01-pre-hire.png');
    if (!hired.hired || !(hired.roster || []).length) {
      producerName = `Producer ${Date.now().toString(36)}`;
      const hire = await request.post(`/api/workspaces/${workspace.id}/assistant-program/hire`, {
        data: { name: producerName, version: assistantBefore.state_revision }
      });
      expect(hire.ok(), await hire.text()).toBeTruthy();
      hired = await hire.json();
    }
    expect(hired.primary_name).toBe(producerName);
    expect(hired.roster).toHaveLength(3);
    assistantStationID = hired.station_id;
    assistantAgentNames.push(
      producerName,
      ...hired.roster.map((item: { agent_name: string }) => item.agent_name)
    );
    expect(hired.stage_id).toBe('helper');
    expect(hired.level).toBe(1);

    const secondCreate = await createReviewedGroupedWorkspace(
      request,
      {
        name: `Second Plugin REAPER ${Date.now().toString(36)}`,
        description: 'Second disposable linked assistant fixture',
        template_id: templateID,
        create_template_agents: true
      },
      `second-plugin-reaper-${Date.now().toString(36)}`
    );
    expect(secondCreate.ok(), await secondCreate.text()).toBeTruthy();
    const secondCreated = await secondCreate.json();
    const secondAssistantWorkspace = secondCreated.folder as {
      id: string;
      folder_slug: string;
      name: string;
    };
    linkedAssistantWorkspaces.push(secondAssistantWorkspace);
    expect(secondCreated.assistant_station_id).toBe(hired.station_id);
    const secondProgram = await request.get(
      `/api/workspaces/${secondAssistantWorkspace.id}/assistant-program`
    );
    expect(secondProgram.ok(), await secondProgram.text()).toBeTruthy();
    const secondSummary = await secondProgram.json();
    expect(secondSummary.station_id).toBe(hired.station_id);
    expect(secondSummary.primary_name).toBe(producerName);
    expect(secondSummary.roster || []).toHaveLength(0);
    expect(secondSummary.roster_scope || 'project').toBe('project');

    await page.reload();
    await expect(page.getByRole('heading', { name: 'Music Production Home' })).toBeVisible({
      timeout: 15_000
    });
    await expect(page.getByText('Stage 1 — Helper')).toBeVisible();
    await expect(page.getByText('0 accepted completions · 5 until Collaborator')).toBeVisible();
    await expect(page.getByRole('heading', { name: /^Producer(?: |$)/ })).toBeVisible();
    await expect(page.getByRole('heading', { name: /^Mix Engineer(?: |$)/ })).toBeVisible();
    await expect(page.getByRole('heading', { name: /^Songwriter(?: |$)/ })).toBeVisible();
    await expect(page.getByRole('link', { name: secondAssistantWorkspace.name })).toBeVisible();
    await capture('music-producer-02-helper-home.png');

    const thirdCreate = await createReviewedGroupedWorkspace(
      request,
      {
        name: `Third Plugin REAPER ${Date.now().toString(36)}`,
        description: 'Third disposable linked assistant fixture',
        template_id: templateID,
        create_template_agents: true
      },
      `third-plugin-reaper-${Date.now().toString(36)}`
    );
    expect(thirdCreate.ok(), await thirdCreate.text()).toBeTruthy();
    const thirdCreated = await thirdCreate.json();
    linkedAssistantWorkspaces.push(thirdCreated.folder);
    expect(thirdCreated.assistant_station_id).toBe(hired.station_id);

    const progressionProjects = [
      workspace,
      secondAssistantWorkspace,
      thirdCreated.folder,
      workspace,
      secondAssistantWorkspace
    ];
    for (let index = 1; index <= progressionProjects.length; index += 1) {
      const createTask = await request.post('/api/orchestration/tasks', {
        data: {
          workspace_id: progressionProjects[index - 1].id,
          description: `Accepted producer collaboration ${index}`
        }
      });
      expect(createTask.ok(), await createTask.text()).toBeTruthy();
      const createdTask = (await createTask.json()).task;
      const completeTask = await request.post(
        `/api/orchestration/tasks/${encodeURIComponent(createdTask.id)}/complete`
      );
      expect(completeTask.ok(), await completeTask.text()).toBeTruthy();
    }
    const collaboratorResponse = await request.get(
      `/api/workspaces/${workspace.id}/assistant-program`
    );
    expect(collaboratorResponse.ok(), await collaboratorResponse.text()).toBeTruthy();
    const collaborator = await collaboratorResponse.json();
    expect(collaborator.stage_id).toBe('collaborator');
    expect(collaborator.level).toBe(2);
    expect(collaborator.accepted_tasks).toBe(5);
    expect(collaborator.promotion_pending).toBeTruthy();
    await page.reload();
    await expect(page.getByText('Stage 2 — Collaborator')).toBeVisible({ timeout: 15_000 });
    await expect(page.getByRole('button', { name: 'Acknowledge new stage' })).toBeVisible();
    await expect(page.getByRole('button', { name: 'Find suggestions' })).toBeEnabled();
    await capture('music-producer-03-collaborator-home.png');

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
    await expect(frame.locator('[data-action-result]')).toContainText(
      /ok|completed|failed|service_unavailable/i
    );
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

    const disable = await request.post(`/api/plugins/${pluginName}/disable`);
    expect(disable.ok(), await disable.text()).toBeTruthy();
    await page.goto(`/workspaces/${encodeURIComponent(workspace.folder_slug)}/assistant`);
    await expect(page.getByText('Contribution paused')).toBeVisible({ timeout: 15_000 });
    await expect(page.getByRole('button', { name: 'Reflect' })).toBeDisabled();
    await capture('music-producer-04-plugin-disabled.png');
    const reenable = await request.post(`/api/plugins/${pluginName}/enable`);
    expect(reenable.ok(), await reenable.text()).toBeTruthy();
    const restoredProgram = await request.get(`/api/workspaces/${workspace.id}/assistant-program`);
    expect(restoredProgram.ok(), await restoredProgram.text()).toBeTruthy();
    expect((await restoredProgram.json()).station_id).toBe(hired.station_id);
  } finally {
    if (assistantStationID)
      await cleanupAssistantTopology(request, assistantStationID, [
        workspace.id,
        ...linkedAssistantWorkspaces.map(item => item.id)
      ]);
    else await request.delete(`/api/workspaces/${workspace.id}`).catch(() => {});
    await request.delete(`/api/plugins/${pluginName}`).catch(() => {});
    for (const agentName of assistantAgentNames) {
      await request.delete(`/api/agents/${encodeURIComponent(agentName)}`).catch(() => {});
    }
    await request.delete(`/api/workspaces/${legacy.id}`).catch(() => {});
  }
});

test('Action Center renders assistant suggestion provenance safely at desktop and narrow widths', async ({
  page
}) => {
  await page.route('**/api/action-center/opportunities?**', async route => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        total: 1,
        items: [
          {
            id: 'assistant-opportunity-1',
            workspace_id: 'workspace-target',
            workspace_slug: 'song-target',
            workspace_name: 'Song Target',
            source_type: 'assistant_suggestion',
            source_id: 'suggestion-1',
            source_label: 'June <script>alert(1)</script>',
            source_url: '/workspaces/song-target/assistant',
            title: 'Consider the approved preflight pattern',
            summary: 'The same reviewed preference appeared across three linked projects.',
            evidence:
              'Song A — accepted checklist\nSong B — accepted checklist\nSong C — accepted checklist',
            priority: 'medium',
            confidence: 'high',
            status: 'new',
            updated_at: new Date().toISOString()
          }
        ]
      })
    });
  });
  await page.goto('/action-center?workspace=workspace-target');
  await expect(page.getByText('Assistant suggestion')).toBeVisible({ timeout: 15_000 });
  await expect(page.getByText('June <script>alert(1)</script>')).toBeVisible();
  await expect(page.getByText('high confidence')).toBeVisible();
  await page.getByText('Evidence', { exact: true }).click();
  await expect(page.getByText('Song C — accepted checklist')).toBeVisible();
  await expect(page.getByRole('link', { name: 'Open source' })).toHaveAttribute(
    'href',
    '/workspaces/song-target/assistant'
  );
  await captureEvidence(page, 'music-producer-05-action-center-suggestion.png');

  await page.setViewportSize({ width: 390, height: 844 });
  await expect(page.getByRole('button', { name: 'Add to Backlog' })).toBeVisible();
  await captureEvidence(page, 'music-producer-06-action-center-narrow.png');
});
