import { expect, test, type APIRequestContext, type Page } from '@playwright/test';
import { cpSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';

// Real-server acceptance for Group Templates against a domain-neutral fixture.
// Each run installs a uniquely named copy of the fixture plugin, so its program
// key — and therefore its canonical Home — is fresh even on a reused server.
const RUN = Date.now().toString(36);
const PLUGIN_NAME = `group-templates-fixture-${RUN}`;
const GROUP_NAME = `Lab Portfolio ${RUN}`;

let sourceRoot = '';

type GroupTemplate = {
  id: string;
  kind: string;
  revision: string;
  name: string;
  provider?: { kind: string; plugin_id?: string };
  availability?: { state: string; actions?: string[] };
  home?: { state: string; workspace_id?: string; name?: string };
  required_home_roles?: { verification: string; filled?: number; missing?: number };
};

async function json(response: Awaited<ReturnType<APIRequestContext['get']>>) {
  const text = await response.text();
  expect(response.ok(), text).toBeTruthy();
  return JSON.parse(text);
}

async function workspaceIDs(request: APIRequestContext) {
  const body = await json(await request.get('/api/workspaces'));
  return (Array.isArray(body.folders) ? body.folders : []).map(
    (folder: { id: string }) => folder.id
  );
}

async function agentNames(request: APIRequestContext) {
  const body = await json(await request.get('/api/agents/dashboard/list?sort_by=name&order=asc'));
  const records = Array.isArray(body) ? body : body.agents || [];
  return records.map((item: { name?: string }) => String(item.name || '')).sort();
}

async function fixtureTemplate(request: APIRequestContext): Promise<GroupTemplate> {
  const body = await json(await request.get('/api/workspaces/group-templates'));
  const templates: GroupTemplate[] = body.group_templates || [];
  expect(templates[0]).toMatchObject({ id: 'general', kind: 'ordinary_group' });
  const entry = templates.find(
    template => template.kind === 'managed_home' && template.provider?.plugin_id === PLUGIN_NAME
  );
  expect(entry, JSON.stringify(templates)).toBeTruthy();
  return entry as GroupTemplate;
}

async function openGroupCreator(page: Page) {
  await page.goto('/');
  await page.waitForFunction(() => Boolean((window as any).sessionManager?.showAddWorkspaceModal));
  await page.evaluate(() =>
    (window as any).sessionManager.showAddWorkspaceModal({
      kind: 'group',
      entryPoint: 'group_templates_spec'
    })
  );
  await expect(page.locator('#addFolderModal')).toBeVisible();
  await expect(page.locator('#workspaceCreatorKindGroup')).toBeChecked();
}

async function chooseTemplate(page: Page, id: string) {
  const option = page.locator(
    `#workspaceGroupTemplateOptions [data-group-template-id="${id}"] input`
  );
  await expect(option).toBeEnabled();
  await option.check();
  await expect(option).toBeChecked();
}

test.describe.configure({ mode: 'serial' });

test.beforeAll(async ({ request }) => {
  await json(await request.post('/api/onboarding/skip'));
  sourceRoot = mkdtempSync(path.join(tmpdir(), 'ori-group-templates-fixture-'));
  const source = path.join(sourceRoot, PLUGIN_NAME);
  cpSync(path.resolve('tests/fixtures/workspace-group-plugin'), source, { recursive: true });
  for (const manifest of ['.ori-plugin/plugin.json', '.claude-plugin/plugin.json']) {
    const file = path.join(source, manifest);
    const parsed = JSON.parse(readFileSync(file, 'utf8'));
    parsed.name = PLUGIN_NAME;
    // Capability definitions are owner-exclusive host-wide; keep this copy's
    // marker distinct from any other enabled fixture on a shared test server.
    for (const capability of parsed.capabilities || []) {
      capability.id = `${capability.id}-${RUN}`;
    }
    writeFileSync(file, `${JSON.stringify(parsed, null, 2)}\n`);
  }
  await json(await request.post('/api/plugins/install', { data: { source, confirm: true } }));
  await json(await request.post(`/api/plugins/${PLUGIN_NAME}/enable`));
});

test.afterAll(async ({ request }) => {
  await request.delete(`/api/plugins/${PLUGIN_NAME}`).catch(() => {});
  if (sourceRoot) rmSync(sourceRoot, { recursive: true, force: true });
});

test('browsing and review are inert, and forged or mixed payloads are refused', async ({
  request
}) => {
  const beforeWorkspaces = await workspaceIDs(request);
  const beforeAgents = await agentNames(request);

  const entry = await fixtureTemplate(request);
  expect(entry).toMatchObject({
    name: 'Research Program Home',
    availability: { state: 'creatable' },
    home: { state: 'absent' },
    required_home_roles: { verification: 'group_absent', filled: 0, missing: 1 }
  });

  const selection = { group_template_id: entry.id, revision: entry.revision, name: GROUP_NAME };
  for (const forged of [
    { ...selection, template_id: `plugin:${PLUGIN_NAME}:research-project` },
    { ...selection, parent_id: beforeWorkspaces[0] || 'group' },
    { ...selection, owner_user_id: 'someone-else' },
    { ...selection, role_staffing: [] },
    { group_template_id: 'general', revision: 'none', name: GROUP_NAME }
  ]) {
    const response = await request.post('/api/workspaces/group-templates/review', { data: forged });
    expect(response.status(), JSON.stringify(forged)).toBe(400);
  }
  const stale = await request.post('/api/workspaces/group-templates/review', {
    data: { ...selection, revision: 'stale' }
  });
  expect(stale.status()).toBe(409);

  const review = await json(
    await request.post('/api/workspaces/group-templates/review', { data: selection })
  );
  expect(review.group_template_review).toMatchObject({ reuse: false, home_name: GROUP_NAME });
  expect(review.group_template_review.review_token).toBeTruthy();

  expect(await workspaceIDs(request)).toEqual(beforeWorkspaces);
  expect(await agentNames(request)).toEqual(beforeAgents);
});

test('a person creates a named group from its template, then reuses it unchanged', async ({
  page,
  request
}) => {
  const beforeWorkspaces = await workspaceIDs(request);
  const beforeAgents = await agentNames(request);
  const entry = await fixtureTemplate(request);

  await openGroupCreator(page);
  await chooseTemplate(page, entry.id);
  await expect(page.locator('#workspaceGroupDetailsNotice')).toContainText(
    'Only the group is created.'
  );
  await expect(page.locator('#wizardNextBtn')).toHaveText('Review →');
  await page.locator('#folderNameInput').fill(GROUP_NAME);
  await page.locator('#wizardNextBtn').click();

  await expect(page.locator('[data-group-template-review-status]')).toContainText(
    'Only this group will be created'
  );
  const create = page.locator('#createFolderBtn');
  await expect(create).toHaveText(`Create group “${GROUP_NAME}” only`);
  await expect(create).toBeEnabled();
  await create.click();
  await expect(page.locator('#addFolderModal')).toBeHidden();

  const created = await fixtureTemplate(request);
  expect(created).toMatchObject({
    availability: { state: 'reusable' },
    home: { state: 'exists', name: GROUP_NAME },
    required_home_roles: { verification: 'verified', filled: 0, missing: 1 }
  });
  const homeID = String(created.home?.workspace_id || '');
  const afterCreate = await workspaceIDs(request);
  expect(afterCreate.filter((id: string) => !beforeWorkspaces.includes(id))).toEqual([homeID]);
  expect(await agentNames(request)).toEqual(beforeAgents);
  const home = await json(await request.get(`/api/workspaces/${homeID}`));
  const folder = home.folder || home.workspace || home;
  expect(folder).toMatchObject({ kind: 'group' });
  expect(folder.parent_id || '').toBe('');

  await openGroupCreator(page);
  await chooseTemplate(page, entry.id);
  const name = page.locator('#folderNameInput');
  await expect(name).toHaveValue(GROUP_NAME);
  await expect(name).toHaveJSProperty('readOnly', true);
  await page.locator('#wizardNextBtn').click();
  await expect(page.locator('[data-group-template-review-status]')).toContainText(
    'reused unchanged'
  );
  await expect(create).toHaveText(`Use existing group “${GROUP_NAME}”`);
  await create.click();
  await expect(page.locator('#addFolderModal')).toBeHidden();

  expect(await workspaceIDs(request)).toEqual(afterCreate);
  expect(await agentNames(request)).toEqual(beforeAgents);
});

test('General keeps its reviewed Group Manager roster and creates nothing when closed', async ({
  page,
  request
}) => {
  const beforeWorkspaces = await workspaceIDs(request);
  await openGroupCreator(page);
  await expect(
    page.locator('#workspaceGroupTemplateOptions [data-group-template-id="general"] input')
  ).toBeChecked();
  await page.locator('#folderNameInput').fill(`Ordinary Group ${RUN}`);
  await page.locator('#wizardNextBtn').click();
  await expect(page.locator('#wizardStep3')).toBeVisible();
  await expect(page.locator('#workspaceTeamHeading')).toHaveText('Group roster');
  await expect(page.locator('[data-workspace-creator-step-name="3"]')).toHaveText('Group roster');
  await page.keyboard.press('Escape');
  await expect(page.locator('#addFolderModal')).toBeHidden();
  expect(await workspaceIDs(request)).toEqual(beforeWorkspaces);
});

test('the template chooser and review fit a phone-width viewport', async ({
  page,
  request
}, testInfo) => {
  await page.setViewportSize({ width: 400, height: 860 });
  const entry = await fixtureTemplate(request);
  await openGroupCreator(page);
  await chooseTemplate(page, entry.id);
  const choice = page.locator('#workspaceGroupTemplateChoice');
  await choice.scrollIntoViewIfNeeded();
  const overflow = await page.evaluate(() => {
    const body = document.querySelector('#addFolderModal .modal-body') as HTMLElement | null;
    const options = [
      ...document.querySelectorAll('.workspace-group-template-option')
    ] as HTMLElement[];
    return {
      page: document.documentElement.scrollWidth > window.innerWidth,
      dialog: body ? body.scrollWidth > body.clientWidth : true,
      option: options.some(option => option.scrollWidth > option.clientWidth)
    };
  });
  expect(overflow).toEqual({ page: false, dialog: false, option: false });
  await page.screenshot({ path: testInfo.outputPath('group-template-chooser-mobile.png') });
  await page.locator('#wizardNextBtn').click();
  await expect(page.locator('[data-group-template-review-status]')).toBeVisible();
  await page.screenshot({ path: testInfo.outputPath('group-template-review-mobile.png') });
  await page.keyboard.press('Escape');
  await expect(page.locator('#addFolderModal')).toBeHidden();
});
