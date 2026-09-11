import { expect, test, type APIRequestContext } from '@playwright/test';
import { cpSync, mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';

const PLUGIN_NAME = 'workspace-group-fixture';
const TEMPLATE_ID = `plugin:${PLUGIN_NAME}:research-project`;
const RUN = Date.now().toString(36);
const HOME_COORDINATOR = `Fixture Portfolio Coordinator ${RUN}`;
const PROJECT_LEAD = `Fixture Research Lead ${RUN}`;
const PROJECT_LEAD_TWO = `Fixture Research Lead Two ${RUN}`;
const WRONG_HOME_AGENT = `Wrong Home Agent ${RUN}`;
const WRONG_PROJECT_AGENT = `Wrong Project Agent ${RUN}`;

let sourceRoot = '';
let homeID = '';
let projectID = '';
let secondProjectID = '';

async function responseJSON(response: Awaited<ReturnType<APIRequestContext['get']>>) {
  const text = await response.text();
  expect(response.ok(), text).toBeTruthy();
  return JSON.parse(text);
}

async function workspaces(request: APIRequestContext) {
  const body = await responseJSON(await request.get('/api/workspaces'));
  return Array.isArray(body.folders) ? body.folders : [];
}

async function agentNames(request: APIRequestContext) {
  const body = await responseJSON(
    await request.get('/api/agents/dashboard/list?sort_by=name&order=asc')
  );
  const records = Array.isArray(body) ? body : body.agents || [];
  return records.map((item: { name?: string }) => String(item.name || ''));
}

async function workspaceRoles(request: APIRequestContext, workspaceID: string) {
  const body = await responseJSON(await request.get(`/api/workspaces/${workspaceID}/roles`));
  return body.roles as {
    workspace_id: string;
    group_workspace_id?: string;
    roles: Array<{
      role_id: string;
      scope: 'home' | 'project';
      required: boolean;
      state: 'empty' | 'filled';
      read_only?: boolean;
      agent?: { name?: string };
    }>;
  };
}

async function cleanupTopology(request: APIRequestContext) {
  for (const [index, childID] of [projectID, secondProjectID].filter(Boolean).entries()) {
    const summary = await request
      .get(`/api/workspaces/${homeID}/assistant-program`)
      .then(response => response.json())
      .catch(() => null);
    if (homeID && summary?.state_revision) {
      const reviewed = await request
        .post(`/api/workspaces/${homeID}/assistant-program/disconnect/review`, {
          data: { project_workspace_id: childID, state_revision: summary.state_revision }
        })
        .then(response => response.json())
        .catch(() => null);
      if (reviewed?.token) {
        await request
          .post(`/api/workspaces/${homeID}/assistant-program/disconnect/commit`, {
            data: {
              token: reviewed.token,
              idempotency_key: `fixture-disconnect-${RUN}-${index}`
            }
          })
          .catch(() => {});
      }
    }
    await request.delete(`/api/workspaces/${childID}?confirm=true`).catch(() => {});
  }
  if (homeID) {
    const summary = await request
      .get(`/api/workspaces/${homeID}/assistant-program`)
      .then(response => response.json())
      .catch(() => null);
    if (summary?.state_revision) {
      const reviewed = await request
        .post(`/api/workspaces/${homeID}/assistant-program/remove-home/review`, {
          data: { state_revision: summary.state_revision }
        })
        .then(response => response.json())
        .catch(() => null);
      if (reviewed?.token) {
        await request
          .post(`/api/workspaces/${homeID}/assistant-program/remove-home/commit`, {
            data: { token: reviewed.token }
          })
          .catch(() => {});
      }
    }
    await request.delete(`/api/workspaces/${homeID}?confirm=true`).catch(() => {});
  }
  for (const name of [
    HOME_COORDINATOR,
    PROJECT_LEAD,
    PROJECT_LEAD_TWO,
    WRONG_HOME_AGENT,
    WRONG_PROJECT_AGENT
  ]) {
    await request.delete(`/api/agents/${encodeURIComponent(name)}`).catch(() => {});
  }
  await request.delete(`/api/plugins/${PLUGIN_NAME}`).catch(() => {});
}

test.describe.configure({ mode: 'serial' });

test.beforeAll(async ({ request }) => {
  const skipOnboarding = await request.post('/api/onboarding/skip');
  expect(skipOnboarding.ok(), await skipOnboarding.text()).toBeTruthy();
  const onboarding = await responseJSON(await request.get('/api/onboarding/status'));
  expect(onboarding).toMatchObject({ needs_onboarding: false, skipped: true });
  await request.delete(`/api/plugins/${PLUGIN_NAME}`).catch(() => {});
  sourceRoot = mkdtempSync(path.join(tmpdir(), 'ori-workspace-group-fixture-'));
  const source = path.join(sourceRoot, PLUGIN_NAME);
  cpSync(path.resolve('tests/fixtures/workspace-group-plugin'), source, { recursive: true });

  const install = await request.post('/api/plugins/install', {
    data: { source, confirm: true }
  });
  expect(install.ok(), await install.text()).toBeTruthy();
  const enable = await request.post(`/api/plugins/${PLUGIN_NAME}/enable`);
  expect(enable.ok(), await enable.text()).toBeTruthy();
});

test.afterAll(async ({ request }) => {
  await cleanupTopology(request);
  if (sourceRoot) rmSync(sourceRoot, { recursive: true, force: true });
});

test('passive lookup and Home review remain inert before the explicit Details commit', async ({
  request
}) => {
  const beforeWorkspaces = await workspaces(request);
  const beforeAgents = await agentNames(request);

  const plan = await responseJSON(
    await request.post('/api/workspaces/template-agent-plan', {
      data: { template_id: TEMPLATE_ID, group_composition: 'grouped' }
    })
  );
  expect(plan).toMatchObject({
    template_id: TEMPLATE_ID,
    assistant_program: {
      id: 'research-program',
      station_name: 'Research Program Home'
    }
  });
  expect(plan.assistant_program.roles).toEqual(
    expect.arrayContaining([
      expect.objectContaining({ id: 'portfolio_coordinator', scope: 'home', required: true }),
      expect.objectContaining({ id: 'project_lead', scope: 'project', required: true })
    ])
  );
  expect(await workspaces(request)).toHaveLength(beforeWorkspaces.length);
  expect(await agentNames(request)).toEqual(beforeAgents);

  const review = await responseJSON(
    await request.post('/api/workspaces/group-requirement/home/review', {
      data: { template_id: TEMPLATE_ID }
    })
  );
  expect(review.group_requirement_review).toMatchObject({
    state: 'ready_grouped',
    policy: 'required',
    selected_composition: 'grouped',
    home_name: 'Research Program Home',
    home_will_be_created: true
  });
  expect(review.group_requirement_review.review_token).toBeTruthy();
  expect(await workspaces(request)).toHaveLength(beforeWorkspaces.length);
  expect(await agentNames(request)).toEqual(beforeAgents);
  expect(
    (await workspaces(request)).some(
      (item: { name?: string }) => item.name === 'Research Program Home'
    )
  ).toBeFalsy();
});

test('first-time UI prepares and staffs the exact Home before one reviewed project creation', async ({
  page,
  request
}) => {
  const projectName = `Fixture Project ${RUN}`;
  const beforeWorkspaces = await workspaces(request);
  const beforeAgents = await agentNames(request);

  await page.goto('/workspaces');
  await page.evaluate(() => {
    const modal = document.getElementById('addFolderModal');
    // @ts-expect-error Bootstrap is a page global.
    window.bootstrap.Modal.getOrCreateInstance(modal).show();
  });
  const creator = page.locator('#addFolderModal');
  const destination = page.locator('#workspaceGroupDestinationCard');
  await expect(creator).toBeVisible();
  await page
    .locator('#templatePicker')
    .getByRole('radio', { name: 'Research Project', exact: true })
    .click();
  await expect(page.locator('#wizardStep1')).toContainText('Group role');
  await expect(page.locator('#wizardStep1')).toContainText('Project role');

  await page.locator('#wizardNextBtn').click();
  await expect(page.locator('#wizardStep2')).toBeVisible();
  await page.locator('#folderNameInput').fill(projectName);
  const openProject = page.locator('#projectTemplateOpenAfterCreateToggle');
  if (await openProject.isChecked()) await openProject.uncheck();

  await expect(destination).toContainText('Research Program Home');
  await expect(destination).toContainText('Proposed group · not created');
  await expect(destination).toContainText('0 of 1 required group role filled');
  await page.screenshot({
    path: 'tasks/screenshots/workspace-group-first-creation/synthetic-details-absent.png',
    fullPage: true
  });
  expect(await workspaces(request)).toHaveLength(beforeWorkspaces.length);
  expect(await agentNames(request)).toEqual(beforeAgents);

  await destination.getByRole('button', { name: 'Review Research Program Home setup' }).click();
  await expect(page.locator('#workspaceGroupHomeReview')).toBeVisible();
  await expect(page.locator('#workspaceGroupHomeReview')).toContainText(
    'does not create this project'
  );
  expect(await workspaces(request)).toHaveLength(beforeWorkspaces.length);

  const homeCommitPromise = page.waitForResponse(
    response =>
      response.url().endsWith('/api/workspaces/group-requirement/home/commit') &&
      response.request().method() === 'POST'
  );
  await page.locator('#workspaceGroupHomeConfirm').click();
  const homeCommit = await homeCommitPromise;
  expect(homeCommit.ok(), await homeCommit.text()).toBeTruthy();
  const prepared = (await homeCommit.json()).group_requirement;
  homeID = prepared.home_workspace_id;
  expect(prepared).toMatchObject({ state: 'home_ready', home_created: true });
  expect(homeID).toBeTruthy();
  expect(
    (await workspaces(request)).some((item: { id: string }) => item.id === homeID)
  ).toBeTruthy();
  expect(
    (await workspaces(request)).some((item: { name?: string }) => item.name === projectName)
  ).toBeFalsy();
  expect(await agentNames(request)).toEqual(beforeAgents);
  await page.screenshot({
    path: 'tasks/screenshots/workspace-group-first-creation/synthetic-details-empty-home.png',
    fullPage: true
  });

  await creator.locator('.modal-footer [data-bs-dismiss="modal"]').click();
  await expect(creator).toBeHidden();
  const afterGroupCancel = await workspaces(request);
  expect(afterGroupCancel).toHaveLength(beforeWorkspaces.length + 1);
  expect(afterGroupCancel.some((item: { id: string }) => item.id === homeID)).toBeTruthy();
  expect(afterGroupCancel.some((item: { name?: string }) => item.name === projectName)).toBeFalsy();
  await page.evaluate(() => {
    const modal = document.getElementById('addFolderModal');
    // @ts-expect-error Bootstrap is a page global.
    window.bootstrap.Modal.getOrCreateInstance(modal).show();
  });
  await page
    .locator('#templatePicker')
    .getByRole('radio', { name: 'Research Project', exact: true })
    .click();
  await page.locator('#wizardNextBtn').click();
  await page.locator('#folderNameInput').fill(projectName);
  if (await openProject.isChecked()) await openProject.uncheck();

  await expect(destination).toContainText('Existing verified group');
  await expect(destination).toContainText('0 of 1 required group role filled');
  await destination.getByRole('button', { name: 'Set up Portfolio Coordinator' }).click();
  await expect(page.locator('#addAgentModal')).toBeVisible();
  await page.locator('#addAgentModal .btn-close').click();
  await expect(page.locator('#addAgentModal')).toBeHidden();
  await expect(creator).toBeVisible();
  await expect(destination).toContainText('0 of 1 required group role filled');
  await expect(page.locator('#workspaceGroupDestinationTitle')).toBeFocused();
  const stagedBeforeHomeRole = await page.evaluate(() => {
    // @ts-expect-error CreateWorkspaceTeamDraft and sessionManager are page globals.
    return window.CreateWorkspaceTeamDraft.setRoleFill(
      window.sessionManager.teamDraft,
      'project_lead',
      { mode: 'create', name: 'Preserved Draft Lead' }
    );
  });
  expect(stagedBeforeHomeRole).toBeTruthy();
  await destination.getByRole('button', { name: 'Set up Portfolio Coordinator' }).click();
  await expect(page.locator('#addAgentModal')).toBeVisible();
  await expect(page.locator('#workspaceGroupRoleMode')).toBeVisible();
  await expect(page.locator('#agentCreateDraftContext')).toContainText('Home only');
  await expect(page.locator('#agentCreateDraftContextText')).toContainText(
    'does not create or change the project draft'
  );
  await expect(page.locator('#workspaceGroupRoleRosterPreview')).toContainText(
    'Portfolio Coordinator'
  );
  await expect(page.locator('[data-agent-create-field="name"]')).toBeFocused();
  await page.setViewportSize({ width: 390, height: 844 });
  const roleModalOverflow = await page
    .locator('#addAgentModal .modal-content')
    .evaluate(element => ({
      clientWidth: element.clientWidth,
      scrollWidth: element.scrollWidth
    }));
  expect(roleModalOverflow.scrollWidth).toBeLessThanOrEqual(roleModalOverflow.clientWidth + 1);
  await page.locator('[data-agent-create-field="name"]').fill(HOME_COORDINATOR);
  const homeRolePromise = page.waitForResponse(
    response =>
      response.url().endsWith(`/api/workspaces/${homeID}/roles/portfolio_coordinator`) &&
      response.request().method() === 'PUT'
  );
  await page.locator('#createAgentBtn').click();
  const homeRole = await homeRolePromise;
  expect(homeRole.ok(), await homeRole.text()).toBeTruthy();
  await expect(page.locator('#addAgentModal')).toBeHidden();
  await expect(creator).toBeVisible();
  await expect(destination).toContainText('1 of 1 required group role filled');
  await expect(destination).toContainText('Ready');
  await expect(page.locator('#workspaceGroupDestinationActions button')).toHaveCount(0);
  const exactHomeRoster = await workspaceRoles(request, homeID);
  expect(exactHomeRoster.roles).toEqual(
    expect.arrayContaining([
      expect.objectContaining({
        role_id: 'portfolio_coordinator',
        scope: 'home',
        state: 'filled',
        source: 'created',
        agent: expect.objectContaining({ name: HOME_COORDINATOR })
      })
    ])
  );
  await page.setViewportSize({ width: 1280, height: 900 });
  await page.screenshot({
    path: 'tasks/screenshots/workspace-group-first-creation/synthetic-details-staffed-home.png',
    fullPage: true
  });
  const preservedProjectFill = await page.evaluate(() => {
    // @ts-expect-error CreateWorkspaceTeamDraft and sessionManager are page globals.
    const api = window.CreateWorkspaceTeamDraft;
    const draft = window.sessionManager.teamDraft;
    const fill = api.getRoleFill(draft, 'project_lead');
    api.clearRoleFill(draft, 'project_lead');
    return fill;
  });
  expect(preservedProjectFill).toMatchObject({ mode: 'create', name: 'Preserved Draft Lead' });
  expect(
    (await workspaces(request)).some((item: { name?: string }) => item.name === projectName)
  ).toBeFalsy();

  await expect(page.locator('#folderNameInput')).toHaveValue(projectName);
  await page.locator('#wizardNextBtn').click();
  await expect(page.locator('#wizardStep3')).toBeVisible();
  await expect(page.locator('[data-scope="home"] .ws-role-roster__scope-title')).toHaveText(
    'Group coordination — Research Program Home'
  );
  await expect(page.locator('[data-scope="home"]')).toContainText('GROUP COORDINATOR');
  await expect(page.locator('[data-scope="project"] .ws-role-roster__scope-title')).toHaveText(
    `Team for ${projectName}`
  );
  await expect(page.locator('[data-scope="project"]')).toContainText('PROJECT LEAD');
  const projectRole = page.locator('.ws-role-row[data-role-id="project_lead"]');
  await expect(projectRole).toContainText('Research Lead');
  await expect(projectRole).toContainText('Missing');
  await projectRole.getByRole('button', { name: 'Create an agent for Research Lead' }).click();
  await expect(page.locator('#addAgentModal')).toBeVisible();
  await page.locator('[data-agent-create-field="name"]').fill(PROJECT_LEAD);
  await page.locator('#createAgentBtn').click();
  await expect(page.locator('#addAgentModal')).toBeHidden();
  await expect(creator).toBeVisible();
  await expect(projectRole).toContainText(PROJECT_LEAD);
  expect(await agentNames(request)).not.toContain(PROJECT_LEAD);
  await page.screenshot({
    path: 'tasks/screenshots/workspace-group-first-creation/synthetic-team-scopes.png',
    fullPage: true
  });

  await page.locator('#wizardNextBtn').click();
  await expect(page.locator('#wizardStep4')).toBeVisible();
  await expect(page.locator('#workspaceReviewSummary')).toContainText('Resulting hierarchy');
  await expect(page.locator('#workspaceReviewSummary')).toContainText('Research Program Home');
  await expect(page.locator('#workspaceReviewSummary')).toContainText(
    `${HOME_COORDINATOR} · already staffed on the group`
  );
  await expect(page.locator('#workspaceReviewSummary')).toContainText(
    'New project workspace · not created yet'
  );
  await expect(page.locator('#workspaceReviewSummary')).toContainText('Project team');
  await page.screenshot({
    path: 'tasks/screenshots/workspace-group-first-creation/synthetic-review-hierarchy.png',
    fullPage: true
  });
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(page.locator('#workspaceReviewSummary')).toBeVisible();
  const reviewOverflow = await page.locator('#addFolderModal .modal-content').evaluate(element => ({
    clientWidth: element.clientWidth,
    scrollWidth: element.scrollWidth
  }));
  expect(reviewOverflow.scrollWidth).toBeLessThanOrEqual(reviewOverflow.clientWidth + 1);
  await page.screenshot({
    path: 'tasks/screenshots/workspace-group-first-creation/synthetic-review-mobile.png',
    fullPage: true
  });
  await page.setViewportSize({ width: 1280, height: 900 });
  const placementReviewPromise = page.waitForResponse(
    response =>
      response.url().endsWith('/api/workspaces') &&
      response.request().method() === 'POST' &&
      response.request().postDataJSON()?.group_requirement_review === true
  );
  await page.locator('#createFolderBtn').click();
  const placementReview = await placementReviewPromise;
  expect(placementReview.ok(), await placementReview.text()).toBeTruthy();
  await expect(page.locator('#createFolderBtn')).toHaveText(`Confirm create “${projectName}”`);

  const createPromise = page.waitForResponse(
    response =>
      response.url().endsWith('/api/workspaces') &&
      response.request().method() === 'POST' &&
      Boolean(response.request().postDataJSON()?.group_review_token)
  );
  await page.locator('#createFolderBtn').click();
  const createResponse = await createPromise;
  expect(createResponse.ok(), await createResponse.text()).toBeTruthy();
  const createPayload = createResponse.request().postDataJSON();
  expect(createPayload.create_required_home).toBeUndefined();
  expect(createPayload.role_staffing).toEqual([
    expect.objectContaining({ role_id: 'project_lead', mode: 'create', name: PROJECT_LEAD })
  ]);
  const created = await createResponse.json();
  projectID = created.folder.id;
  expect(created.assistant_station_id).toBe(homeID);

  const home = await responseJSON(await request.get(`/api/workspaces/${homeID}`));
  const project = await responseJSON(await request.get(`/api/workspaces/${projectID}`));
  expect(home.agent_instances).toEqual([
    expect.objectContaining({
      name: HOME_COORDINATOR,
      role_id: 'portfolio_coordinator',
      role_source: 'created',
      entry_point: true
    })
  ]);
  expect(project).toMatchObject({ id: projectID, parent_id: homeID });
  expect(project.agent_instances).toEqual([
    expect.objectContaining({
      name: PROJECT_LEAD,
      role_id: 'project_lead',
      role_source: 'created',
      entry_point: true
    })
  ]);
  expect((await agentNames(request)).filter(name => name === HOME_COORDINATOR)).toHaveLength(1);
  expect((await agentNames(request)).filter(name => name === PROJECT_LEAD)).toHaveLength(1);

  const secondProjectName = `Field Notes Two ${RUN}`;
  const secondProjectLead = PROJECT_LEAD_TWO;
  await page.waitForURL(url => /^\/workspaces\/[^/]+$/.test(url.pathname));
  await page.goto('/workspaces');
  await expect(creator).toBeHidden();
  await page.evaluate(() => {
    const modal = document.getElementById('addFolderModal');
    if (!modal) throw new Error('Create Workspace modal is missing');
    // @ts-expect-error bootstrap is provided by the application page.
    window.bootstrap.Modal.getOrCreateInstance(modal).show();
  });
  await page
    .locator('#templatePicker')
    .getByRole('radio', { name: 'Research Project', exact: true })
    .click();
  await page.locator('#wizardNextBtn').click();
  await page.locator('#folderNameInput').fill(secondProjectName);
  if (await openProject.isChecked()) await openProject.uncheck();
  await expect(destination).toContainText('Existing verified group');
  await expect(destination).toContainText('1 of 1 required group role filled');
  await expect(page.locator('#workspaceGroupDestinationActions button')).toHaveCount(0);
  await page.locator('#wizardNextBtn').click();
  const secondProjectRole = page.locator('.ws-role-row[data-role-id="project_lead"]');
  await secondProjectRole
    .getByRole('button', { name: 'Create an agent for Research Lead' })
    .click();
  await page.locator('[data-agent-create-field="name"]').fill(secondProjectLead);
  await page.locator('#createAgentBtn').click();
  await expect(page.locator('#addAgentModal')).toBeHidden();
  await page.locator('#wizardNextBtn').click();
  const secondPlacementReviewPromise = page.waitForResponse(
    response =>
      response.url().endsWith('/api/workspaces') &&
      response.request().method() === 'POST' &&
      response.request().postDataJSON()?.group_requirement_review === true
  );
  await page.locator('#createFolderBtn').click();
  const secondPlacementReview = await secondPlacementReviewPromise;
  expect(secondPlacementReview.ok(), await secondPlacementReview.text()).toBeTruthy();
  const secondCreatePromise = page.waitForResponse(
    response =>
      response.url().endsWith('/api/workspaces') &&
      response.request().method() === 'POST' &&
      Boolean(response.request().postDataJSON()?.group_review_token)
  );
  await page.locator('#createFolderBtn').click();
  const secondCreate = await secondCreatePromise;
  expect(secondCreate.ok(), await secondCreate.text()).toBeTruthy();
  const secondCreated = await secondCreate.json();
  secondProjectID = secondCreated.folder.id;
  const secondProject = await responseJSON(
    await request.get(`/api/workspaces/${secondCreated.folder.id}`)
  );
  expect(secondProject).toMatchObject({
    id: secondCreated.folder.id,
    parent_id: homeID,
    agent_instances: [
      expect.objectContaining({
        name: secondProjectLead,
        role_id: 'project_lead',
        role_source: 'created',
        entry_point: true
      })
    ]
  });
  const reusedHome = await responseJSON(await request.get(`/api/workspaces/${homeID}`));
  expect(reusedHome.agent_instances).toEqual([
    expect.objectContaining({
      name: HOME_COORDINATOR,
      role_id: 'portfolio_coordinator',
      role_source: 'created',
      entry_point: true
    })
  ]);
  expect(
    (await workspaces(request)).filter(
      (item: { name?: string }) => item.name === 'Research Program Home'
    )
  ).toHaveLength(1);
  expect((await agentNames(request)).filter(name => name === HOME_COORDINATOR)).toHaveLength(1);
  expect((await agentNames(request)).filter(name => name === secondProjectLead)).toHaveLength(1);
});

test('a cleared Home coordinator can be explicitly reassigned without creating a project', async ({
  page,
  request
}) => {
  const beforeWorkspaces = await workspaces(request);
  const cleared = await request.delete(`/api/workspaces/${homeID}/roles/portfolio_coordinator`);
  expect(cleared.ok(), await cleared.text()).toBeTruthy();
  const emptyRoster = await workspaceRoles(request, homeID);
  expect(emptyRoster.roles.find(role => role.role_id === 'portfolio_coordinator')).toMatchObject({
    state: 'empty'
  });

  await page.goto('/workspaces');
  await page.evaluate(() => {
    const modal = document.getElementById('addFolderModal');
    if (!modal) throw new Error('Create Workspace modal is missing');
    // @ts-expect-error bootstrap is provided by the application page.
    window.bootstrap.Modal.getOrCreateInstance(modal).show();
  });
  await page
    .locator('#templatePicker')
    .getByRole('radio', { name: 'Research Project', exact: true })
    .click();
  await page.locator('#wizardNextBtn').click();
  const destination = page.locator('#workspaceGroupDestinationCard');
  await expect(destination).toContainText('0 of 1 required group role filled');
  await destination.getByRole('button', { name: 'Set up Portfolio Coordinator' }).click();
  await expect(page.locator('#addAgentModal')).toBeVisible();
  await page.locator('[name="workspace-group-role-fill-mode"][value="assign"]').check();
  const savedAgent = page.locator('#workspaceGroupRoleAgentSelect');
  await expect(savedAgent).toBeEnabled();
  await savedAgent.selectOption({ label: HOME_COORDINATOR });
  const assignPromise = page.waitForResponse(
    response =>
      response.url().endsWith(`/api/workspaces/${homeID}/roles/portfolio_coordinator`) &&
      response.request().method() === 'PUT'
  );
  await page.locator('#createAgentBtn').click();
  const assigned = await assignPromise;
  expect(assigned.ok(), await assigned.text()).toBeTruthy();
  expect(assigned.request().postDataJSON()).toEqual({
    mode: 'assign',
    name: HOME_COORDINATOR
  });
  await expect(page.locator('#addAgentModal')).toBeHidden();
  await expect(destination).toContainText('1 of 1 required group role filled');
  await expect(page.locator('#workspaceGroupDestinationActions button')).toHaveCount(0);
  expect(await workspaces(request)).toHaveLength(beforeWorkspaces.length);
  expect((await agentNames(request)).filter(name => name === HOME_COORDINATOR)).toHaveLength(1);
  const reassignedRoster = await workspaceRoles(request, homeID);
  expect(
    reassignedRoster.roles.find(role => role.role_id === 'portfolio_coordinator')
  ).toMatchObject({
    state: 'filled',
    source: 'assigned',
    agent: expect.objectContaining({ name: HOME_COORDINATOR })
  });
});

test('crafted inverse-scope role IDs cannot cross the exact Home/project boundary', async ({
  request
}) => {
  const homeBefore = await workspaceRoles(request, homeID);
  const projectBefore = await workspaceRoles(request, projectID);

  const stationAttack = await request.put(`/api/workspaces/${homeID}/roles/project_lead`, {
    data: { mode: 'create', name: WRONG_PROJECT_AGENT }
  });
  expect(stationAttack.status()).toBe(409);
  const childAttack = await request.put(
    `/api/workspaces/${projectID}/roles/portfolio_coordinator`,
    { data: { mode: 'create', name: WRONG_HOME_AGENT } }
  );
  expect(childAttack.status()).toBe(409);

  expect(await workspaceRoles(request, homeID)).toEqual(homeBefore);
  expect(await workspaceRoles(request, projectID)).toEqual(projectBefore);
  expect(await agentNames(request)).not.toContain(WRONG_PROJECT_AGENT);
  expect(await agentNames(request)).not.toContain(WRONG_HOME_AGENT);
});
