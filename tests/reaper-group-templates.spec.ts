import { expect, test, type APIRequestContext, type Page } from '@playwright/test';
import { mkdirSync } from 'node:fs';

// Exact-source acceptance for Group Templates with the published REAPER plugin
// 0.5.2 (commit 9fde099d, blueprint reaper-song v6). Opt-in: run it only against
// a disposable server whose isolated plugin store already has that exact
// release installed and enabled, e.g.
//   POST /api/plugins/install {"source":"https://github.com/johnjallday/reaper-plugin#sha=9fde099d2f911d70aa8381c920359bf579466d1d","confirm":true}
// No live REAPER, personal folders or user plugin store are involved.
const ENABLED = process.env.ORI_REAPER_052_ACCEPTANCE === '1';
const SHOTS = process.env.ORI_REAPER_DEMO_EVIDENCE_DIR || 'test-results/reaper-group-templates';
const RUN = Date.now().toString(36);
const GROUP_NAME = `My Studio ${RUN}`;
const COORDINATOR = `Studio Portfolio Manager ${RUN}`;
const SONG_BLUEPRINT = 'plugin:reaper-plugin:reaper-song';
const TEMPLATE_META = 'Template: Music Production Home · Plugin: reaper-plugin 0.5.2';

test.describe.configure({ mode: 'serial' });
test.skip(!ENABLED, 'requires an isolated server with reaper-plugin 0.5.2 installed');

let homeID = '';

async function json(response: Awaited<ReturnType<APIRequestContext['get']>>) {
  const text = await response.text();
  expect(response.ok(), text).toBeTruthy();
  return JSON.parse(text);
}

async function folders(request: APIRequestContext) {
  return (await json(await request.get('/api/workspaces'))).folders as Array<{
    id: string;
    name: string;
    kind: string;
    parent_id?: string;
    folder_slug: string;
  }>;
}

async function agentNames(request: APIRequestContext) {
  const body = await json(await request.get('/api/agents/dashboard/list?sort_by=name&order=asc'));
  return ((Array.isArray(body) ? body : body.agents) || []).map(
    (agent: { name: string }) => agent.name
  );
}

async function musicTemplate(request: APIRequestContext) {
  const body = await json(await request.get('/api/workspaces/group-templates'));
  const entry = body.group_templates.find(
    (template: { provider?: { plugin_id?: string } }) =>
      template.provider?.plugin_id === 'reaper-plugin'
  );
  expect(entry, JSON.stringify(body.group_templates)).toBeTruthy();
  return entry;
}

async function evidence(page: Page, name: string) {
  mkdirSync(SHOTS, { recursive: true });
  await page.screenshot({ path: `${SHOTS}/${name}.png` });
}

// Waits until the role form is fully open in its project-draft mode; a submit
// during Bootstrap's opening transition would not close it.
async function createProjectRoleAgent(page: Page, label: string, name: string) {
  const row = page.locator('.ws-role-row', { hasText: label }).filter({
    has: page.getByRole('button', { name: `Create an agent for ${label}` })
  });
  await row.getByRole('button', { name: `Create an agent for ${label}` }).click();
  await expect(page.locator('#addAgentModal')).toBeVisible();
  await expect(page.locator('[data-agent-create-field="name"]')).toBeFocused();
  await page.locator('[data-agent-create-field="name"]').fill(name);
  await page.locator('#createAgentBtn').click();
  await expect(page.locator('#addAgentModal')).toBeHidden();
  await expect(page.locator('#addFolderModal')).toBeVisible();
}

test('the installed source is the exact published 0.5.2 and offers Music Production Home', async ({
  request
}) => {
  const plugins = await json(await request.get('/api/plugins'));
  const list = Array.isArray(plugins) ? plugins : plugins.plugins || [];
  const reaper = list.find((plugin: { name: string }) => plugin.name === 'reaper-plugin');
  expect(reaper, JSON.stringify(list)).toMatchObject({ version: '0.5.2', enabled: true });
  expect(String(reaper.source)).toContain('#sha=9fde099d2f911d70aa8381c920359bf579466d1d');

  const entry = await musicTemplate(request);
  expect(entry).toMatchObject({
    kind: 'managed_home',
    name: 'Music Production Home',
    proposed_group_name: 'Music Production Home',
    provider: { kind: 'plugin', plugin_id: 'reaper-plugin', plugin_version: '0.5.2' },
    availability: { state: 'creatable' },
    project_roles_note: ['Producer', 'Mix Engineer', 'Songwriter']
  });
  expect(
    entry.home_roles.map((role: { label: string; required: boolean }) => [
      role.label,
      role.required
    ])
  ).toEqual([
    ['Music Portfolio Manager', true],
    ['Sample Library Manager', false]
  ]);
});

test('Tree → Create Group → Blueprint builds one unstaffed Music Production group', async ({
  page,
  request
}) => {
  const beforeFolders = await folders(request);
  const beforeAgents = await agentNames(request);
  await page.goto('/');
  await page.getByRole('button', { name: 'Tree', exact: true }).click();
  await page.locator('[data-tree-new-group]').click();
  const creator = page.locator('#addFolderModal');
  await expect(creator).toBeVisible();
  await expect(creator.locator('#wizardStep1Title')).toHaveText('Choose a group blueprint');
  const music = creator.locator('.workspace-group-template-option', {
    hasText: 'Music Production Home'
  });
  await expect(music).toContainText('Plugin: reaper-plugin 0.5.2');
  await expect(music).toContainText('Set up after: Music Portfolio Manager (required)');
  await expect(music).toContainText('Stays project-local: Producer, Mix Engineer, Songwriter');
  await evidence(page, '01-create-group-blueprint-step');

  await music.locator('input').check();
  await expect(creator.locator('#wizardStep1')).toBeVisible();
  await creator.getByRole('button', { name: 'Continue →' }).click();
  await expect(creator.locator('#workspaceGroupBlueprintRecap')).toContainText(
    'Music Production Home'
  );
  await expect(creator.locator('#folderNameInput')).toHaveValue('Music Production Home');
  await creator.locator('#folderNameInput').fill(GROUP_NAME);
  await creator.getByRole('button', { name: 'Review →' }).click();
  const summary = creator.locator('#workspaceReviewSummary');
  await expect(summary).toContainText(TEMPLATE_META);
  await expect(summary).toContainText('Required, set up after: Music Portfolio Manager');
  await expect(creator.locator('[data-group-template-review-status]')).toContainText(
    'Only this group will be created'
  );
  await evidence(page, '02-create-group-review');
  await creator.getByRole('button', { name: `Create group “${GROUP_NAME}” only` }).click();
  await expect(creator).toBeHidden();

  const after = await folders(request);
  const created = after.filter(folder => !beforeFolders.some(item => item.id === folder.id));
  expect(created).toHaveLength(1);
  expect(created[0]).toMatchObject({ name: GROUP_NAME, kind: 'group' });
  expect(created[0].parent_id || '').toBe('');
  homeID = created[0].id;
  expect(await agentNames(request)).toEqual(beforeAgents);
  expect(await musicTemplate(request)).toMatchObject({
    availability: { state: 'reusable' },
    home: { state: 'exists', workspace_id: homeID, name: GROUP_NAME }
  });
});

test('the group page sets up its Music Portfolio Manager', async ({ page, request }) => {
  const home = (await folders(request)).find(folder => folder.id === homeID)!;
  await page.goto(`/workspaces/${encodeURIComponent(home.folder_slug)}`);
  const strip = page.locator('.ws-cmd-group-template');
  await expect(strip).toContainText('Music Production Home');
  await expect(strip).toContainText('Plugin: reaper-plugin 0.5.2');
  await expect(strip).toContainText(
    'Incomplete — 0 of 1 required set up (Music Portfolio Manager)'
  );
  await expect(strip).toContainText('Available');
  await evidence(page, '03-group-incomplete');
  await strip.getByRole('button', { name: 'Set up Music Portfolio Manager' }).click();
  await expect(page.locator('#addAgentModal')).toBeVisible();
  await expect(page.locator('[data-agent-create-field="name"]')).toBeFocused();
  await page.locator('[data-agent-create-field="name"]').fill(COORDINATOR);
  await page.locator('#createAgentBtn').click();
  await expect(page.locator('#addAgentModal')).toBeHidden();
  await expect(strip).toContainText('Ready — 1 of 1 required set up');
  await evidence(page, '04-group-ready');
  const status = await json(await request.get(`/api/workspaces/${homeID}/group-template`));
  expect(status.group_template).toMatchObject({
    kind: 'managed_home',
    team: { state: 'ready' },
    integration: { state: 'available' }
  });
});

for (const [index, song] of ['First Song', 'Second Song'].entries()) {
  test(`${song} joins the group with its own project team`, async ({ page, request }) => {
    const songName = `${song} ${RUN}`;
    const team = ['Producer', 'Mix Engineer', 'Songwriter'].map(label => [
      label,
      `${song} ${label} ${RUN}`
    ]);
    const homeBefore = await json(await request.get(`/api/workspaces/${homeID}`));
    await page.goto('/');
    await page.waitForFunction(() =>
      Boolean((window as any).sessionManager?.showAddWorkspaceModal)
    );
    await page.evaluate(() =>
      (window as any).sessionManager.showAddWorkspaceModal({
        kind: 'workspace',
        entryPoint: 'reaper_052'
      })
    );
    const creator = page.locator('#addFolderModal');
    await creator.locator(`#templatePicker [data-template-id="${SONG_BLUEPRINT}"]`).click();
    await creator.locator('#wizardNextBtn').click();
    await creator.locator('#folderNameInput').fill(songName);
    const openProject = creator.locator('#projectTemplateOpenAfterCreateToggle');
    if (await openProject.isChecked()) await openProject.uncheck();
    const destination = creator.locator('#workspaceGroupDestinationCard');
    await expect(destination).toContainText(GROUP_NAME);
    await expect(destination).toContainText('Existing verified group');
    await expect(destination).toContainText(TEMPLATE_META);
    await expect(destination).toContainText('1 of 1 required group role filled');
    if (index === 0) await evidence(page, '05-song-destination');

    await creator.locator('#wizardNextBtn').click();
    await expect(creator.locator('#wizardStep3')).toBeVisible();
    const homeScope = creator.locator('[data-scope="home"]');
    await expect(homeScope).toContainText(COORDINATOR);
    await expect(
      homeScope.getByRole('button', { name: /Create an agent|Assign an agent/ })
    ).toHaveCount(0);
    for (const [label, name] of team) await createProjectRoleAgent(page, label, name);

    await creator.locator('#wizardNextBtn').click();
    const review = creator.locator('#workspaceReviewSummary');
    await expect(review).toContainText(TEMPLATE_META);
    await expect(review).toContainText(`${COORDINATOR} · already staffed on the group`);
    if (index === 0) await evidence(page, '06-song-review');
    const placement = page.waitForResponse(
      response =>
        new URL(response.url()).pathname === '/api/workspaces' &&
        response.request().method() === 'POST' &&
        response.request().postDataJSON()?.group_requirement_review === true
    );
    await creator.locator('#createFolderBtn').click();
    expect((await placement).ok()).toBeTruthy();
    const commit = page.waitForResponse(
      response =>
        new URL(response.url()).pathname === '/api/workspaces' &&
        response.request().method() === 'POST' &&
        Boolean(response.request().postDataJSON()?.group_review_token)
    );
    await creator.locator('#createFolderBtn').click();
    const committed = await commit;
    expect(committed.ok(), await committed.text()).toBeTruthy();
    const body = await committed.json();
    expect(body.assistant_station_id).toBe(homeID);
    await page.waitForURL(url => /^\/workspaces\/[^/]+$/.test(url.pathname));

    const project = await json(await request.get(`/api/workspaces/${body.folder.id}`));
    expect(project.parent_id).toBe(homeID);
    const instances = project.agent_instances as Array<{ name: string; entry_point?: boolean }>;
    expect(instances.map(instance => instance.name).sort()).toEqual(
      team.map(([, name]) => name).sort()
    );
    expect(
      instances.filter(instance => instance.entry_point).map(instance => instance.name)
    ).toEqual([team[0][1]]);
    expect(instances.some(instance => instance.name === COORDINATOR)).toBe(false);
    const homeAfter = await json(await request.get(`/api/workspaces/${homeID}`));
    expect(homeAfter.agent_instances).toEqual(homeBefore.agent_instances);
    expect((await agentNames(request)).filter((name: string) => name === COORDINATOR)).toHaveLength(
      1
    );
  });
}

test('Create Group reuses the renamed Music Production group unchanged', async ({
  page,
  request
}) => {
  const renamed = `Renamed Studio ${RUN}`;
  await json(await request.post(`/api/workspaces/${homeID}/rename`, { data: { name: renamed } }));
  const beforeFolders = await folders(request);
  await page.goto('/');
  await page.waitForFunction(() => Boolean((window as any).sessionManager?.showAddWorkspaceModal));
  await page.evaluate(() =>
    (window as any).sessionManager.showAddWorkspaceModal({
      kind: 'group',
      entryPoint: 'reaper_052'
    })
  );
  const creator = page.locator('#addFolderModal');
  const music = creator.locator('.workspace-group-template-option', {
    hasText: 'Music Production Home'
  });
  await expect(music).toContainText('Group exists');
  await expect(music).toContainText(`Reuses the existing group “${renamed}” unchanged.`);
  await music.locator('input').check();
  await creator.getByRole('button', { name: 'Continue →' }).click();
  await expect(creator.locator('#folderNameInput')).toHaveValue(renamed);
  await expect(creator.locator('#folderNameInput')).toHaveJSProperty('readOnly', true);
  await creator.getByRole('button', { name: 'Review →' }).click();
  await expect(creator.locator('#createFolderBtn')).toHaveText(`Use existing group “${renamed}”`);
  await evidence(page, '07-reuse-renamed');
  await creator.locator('#createFolderBtn').click();
  await expect(creator).toBeHidden();
  expect((await folders(request)).map(folder => folder.id).sort()).toEqual(
    beforeFolders.map(folder => folder.id).sort()
  );
});
