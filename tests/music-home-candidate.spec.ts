import { expect, test, type APIRequestContext, type Page } from '@playwright/test';
import { mkdirSync } from 'node:fs';

// Exact-source acceptance for the locally staged Music Project Management
// candidate. Run only through scripts/music-home-demo.sh so HOME, ORI_DATA_DIR,
// plugin source, installed skill, and screenshots all stay in its sandbox.
const ENABLED = process.env.ORI_MUSIC_HOME_ACCEPTANCE === '1';
const SHOTS = process.env.ORI_MUSIC_HOME_EVIDENCE_DIR || 'test-results/music-home-candidate';
const PLUGIN_PATH = process.env.ORI_MUSIC_PLUGIN_PATH || '';
const REVISION = process.env.ORI_MUSIC_PLUGIN_REVISION || '';
const VERSION = process.env.ORI_MUSIC_PLUGIN_VERSION || '';
const RUN = Date.now().toString(36);
const GROUP_NAME = `Music Studio ${RUN}`;
const RENAMED_GROUP = `Renamed Music Studio ${RUN}`;
const MANAGER_NAME = 'Portfolio Manager';
let createdHomeID = '';
let createdHomeSlug = '';

interface WorkspaceSummary {
  id: string;
  name: string;
  kind: string;
  folder_slug: string;
  parent_id?: string;
}

interface GroupTemplate {
  id: string;
  revision: string;
  name: string;
  kind: string;
  availability: { state: string };
  provider: { kind: string; plugin_id: string; plugin_version: string };
  home_roles: Array<{
    label: string;
    required: boolean;
    primary?: boolean;
    skills?: string[];
  }>;
  project_roles_note?: string[];
  home?: { workspace_id?: string; name?: string; state?: string };
}

test.describe.configure({ mode: 'serial' });
test.skip(!ENABLED, 'requires scripts/music-home-demo.sh with an isolated staged candidate');

async function json(response: Awaited<ReturnType<APIRequestContext['get']>>) {
  const text = await response.text();
  expect(response.ok(), text).toBeTruthy();
  return JSON.parse(text);
}

async function workspaces(request: APIRequestContext): Promise<WorkspaceSummary[]> {
  return (await json(await request.get('/api/workspaces'))).folders || [];
}

async function musicTemplate(request: APIRequestContext): Promise<GroupTemplate> {
  const body = await json(await request.get('/api/workspaces/group-templates'));
  const matches = (body.group_templates || []).filter(
    (entry: GroupTemplate) => entry.provider?.plugin_id === 'music-project-management'
  );
  expect(matches, JSON.stringify(body.group_templates)).toHaveLength(1);
  return matches[0];
}

async function agentNames(request: APIRequestContext): Promise<string[]> {
  const body = await json(await request.get('/api/agents/dashboard/list?sort_by=name&order=asc'));
  return ((Array.isArray(body) ? body : body.agents) || []).map(
    (agent: { name: string }) => agent.name
  );
}

async function evidence(page: Page, name: string) {
  mkdirSync(SHOTS, { recursive: true });
  await page.screenshot({ path: `${SHOTS}/${name}.png` });
}

test('the exact candidate installs as one content-only Home provider', async ({ request }) => {
  expect(PLUGIN_PATH).toBeTruthy();
  expect(REVISION).toMatch(/^[a-f0-9]{40}$/);
  expect(VERSION).toMatch(/^\d+\.\d+\.\d+$/);

  const body = await json(await request.get('/api/plugins'));
  const plugins = Array.isArray(body) ? body : body.plugins || [];
  const music = plugins.find(
    (plugin: { name: string }) => plugin.name === 'music-project-management'
  );
  expect(music, JSON.stringify(plugins)).toMatchObject({
    name: 'music-project-management',
    version: VERSION,
    enabled: true,
    skills: ['music-project-management']
  });
  expect(music.source).toBe(PLUGIN_PATH);
  expect(plugins.some((plugin: { name: string }) => plugin.name === 'reaper-plugin')).toBe(false);

  const entry = await musicTemplate(request);
  expect(entry).toMatchObject({
    kind: 'managed_home',
    name: 'Music Production Home',
    provider: {
      kind: 'plugin',
      plugin_id: 'music-project-management',
      plugin_version: VERSION
    },
    availability: { state: 'creatable' }
  });
  expect(entry.project_roles_note || []).toEqual([]);
  expect(
    entry.home_roles.map(role => ({
      label: role.label,
      required: role.required,
      skills: role.skills || []
    }))
  ).toEqual([
    {
      label: 'Music Portfolio Manager',
      required: true,
      skills: ['music-project-management']
    },
    { label: 'Sample Library Manager', required: false, skills: [] }
  ]);
});

test('Create Group reviews, staffs, replays, renames, and reuses one empty Home', async ({
  page,
  request
}) => {
  expect(await workspaces(request)).toEqual([]);
  const beforeAgents = await agentNames(request);
  const skipOnboarding = await request.post('/api/onboarding/skip');
  expect(skipOnboarding.ok(), await skipOnboarding.text()).toBeTruthy();

  let commitPayload: Record<string, unknown> | undefined;
  let staffPayload: Record<string, unknown> | undefined;
  page.on('request', outgoing => {
    const url = new URL(outgoing.url());
    if (outgoing.method() === 'POST' && url.pathname === '/api/workspaces/group-templates/commit') {
      commitPayload = outgoing.postDataJSON();
    }
    if (
      outgoing.method() === 'PUT' &&
      /\/api\/workspaces\/[^/]+\/roles\/portfolio_manager$/.test(url.pathname)
    ) {
      staffPayload = outgoing.postDataJSON();
    }
  });

  await page.goto('/');
  await page.getByRole('button', { name: 'Tree', exact: true }).click();
  await page.locator('[data-tree-new-group]').click();
  const creator = page.locator('#addFolderModal');
  await expect(creator).toBeVisible();
  await expect(creator.locator('#wizardStep1Title')).toHaveText('Choose a group blueprint');

  const music = creator.locator('.workspace-group-template-option', {
    hasText: 'Music Production Home'
  });
  await expect(music).toContainText(`Plugin: music-project-management ${VERSION}`);
  await expect(music).toContainText('Group roles: Music Portfolio Manager (required)');
  await expect(music).toContainText('Optional: Sample Library Manager');
  await expect(music).toContainText('Packaged skills: music-project-management');
  await expect(music).toContainText('enabled on a new agent only when you confirm its role setup');
  await expect(music).not.toContainText('Stays project-local');
  await evidence(page, '01-music-home-blueprint');

  await music.locator('input').check();
  await creator.getByRole('button', { name: 'Continue →' }).click();
  await expect(creator.locator('#workspaceGroupBlueprintRecap')).toContainText(
    'Music Production Home'
  );
  await creator.locator('#folderNameInput').fill(GROUP_NAME);
  await evidence(page, '02-music-home-details');
  await creator.getByRole('button', { name: 'Continue →' }).click();

  await expect(creator.locator('#wizardStep3Title')).toHaveText('Staff the group');
  const manager = creator.locator('.ws-role-row[data-role-id="portfolio_manager"]');
  await expect(manager).toHaveAttribute('data-state', 'filled');
  await expect(manager).toContainText('Music Portfolio Manager');
  await expect(manager).toContainText(MANAGER_NAME);
  await evidence(page, '03-music-home-staffing');

  await creator.getByRole('button', { name: 'Review →' }).click();
  const summary = creator.locator('#workspaceReviewSummary');
  await expect(summary).toContainText(
    `Template: Music Production Home · Plugin: music-project-management ${VERSION}`
  );
  await expect(summary).toContainText(
    'No project, team, schedule, tool access, or runtime setup is created'
  );
  await expect(summary).toContainText('music-project-management');
  await evidence(page, '04-music-home-review');

  await creator.locator('#createFolderBtn').click();
  await expect(creator).toBeHidden();
  await page.waitForURL(url => /^\/workspaces\/[^/]+$/.test(url.pathname));

  await expect.poll(async () => (await musicTemplate(request)).availability.state).toBe('reusable');
  const created = await workspaces(request);
  expect(created).toHaveLength(1);
  expect(created[0]).toMatchObject({ name: GROUP_NAME, kind: 'group' });
  expect(created[0].parent_id || '').toBe('');
  const homeID = created[0].id;
  createdHomeID = homeID;
  createdHomeSlug = created[0].folder_slug;
  const home = await json(await request.get(`/api/workspaces/${homeID}`));
  expect(home.agent_instances).toHaveLength(1);
  expect(home.agent_instances[0]).toMatchObject({
    name: MANAGER_NAME,
    role_id: 'portfolio_manager'
  });
  const assistant = await json(await request.get(`/api/workspaces/${homeID}/assistant-program`));
  expect(assistant).toMatchObject({
    available: true,
    is_station: true,
    hired: true,
    home_provider_available: true,
    primary_name: MANAGER_NAME
  });
  expect(assistant.projects || []).toEqual([]);
  expect(assistant.roster).toHaveLength(1);
  expect(home.scheduled_tasks || []).toEqual([]);
  expect(home.directory_references || []).toEqual([]);
  expect(home.mcp_bindings || []).toEqual([]);
  expect(home.installed_capabilities || []).toEqual([]);
  expect(home.project_path || '').toBe('');
  expect((await agentNames(request)).sort()).toEqual([...beforeAgents, MANAGER_NAME].sort());
  await expect(page.locator('.ws-cmd-group-template')).toContainText(
    'Ready — 1 of 1 required set up'
  );
  await page.waitForTimeout(750);
  await evidence(page, '05-music-home-ready');

  expect(commitPayload).toBeTruthy();
  const replay = await json(
    await request.post('/api/workspaces/group-templates/commit', { data: commitPayload })
  );
  expect(replay.group_template).toMatchObject({
    home_workspace_id: homeID,
    idempotent_replay: true,
    created_by_this_operation: true
  });
  expect((await workspaces(request)).map(workspace => workspace.id)).toEqual([homeID]);

  expect(staffPayload).toBeTruthy();
  const repeatedStaff = await request.put(`/api/workspaces/${homeID}/roles/portfolio_manager`, {
    data: staffPayload
  });
  expect(repeatedStaff.status()).toBe(409);
  const afterRepeatedStaff = await json(await request.get(`/api/workspaces/${homeID}`));
  expect(afterRepeatedStaff.agent_instances).toHaveLength(1);
  expect((await agentNames(request)).filter(name => name === MANAGER_NAME)).toHaveLength(1);

  await json(
    await request.post(`/api/workspaces/${homeID}/rename`, { data: { name: RENAMED_GROUP } })
  );
  await page.goto('/');
  await page.waitForFunction(() => Boolean((window as any).sessionManager?.showAddWorkspaceModal));
  await page.evaluate(() =>
    (window as any).sessionManager.showAddWorkspaceModal({
      kind: 'group',
      entryPoint: 'music_home_candidate'
    })
  );
  const reuseCreator = page.locator('#addFolderModal');
  const reusable = reuseCreator.locator('.workspace-group-template-option', {
    hasText: 'Music Production Home'
  });
  await expect(reusable).toContainText('Group exists');
  await expect(reusable).toContainText(`Reuses the existing group “${RENAMED_GROUP}”`);
  await reusable.locator('input').check();
  await reuseCreator.getByRole('button', { name: 'Continue →' }).click();
  await expect(reuseCreator.locator('#folderNameInput')).toHaveValue(RENAMED_GROUP);
  await expect(reuseCreator.locator('#folderNameInput')).toHaveJSProperty('readOnly', true);
  await reuseCreator.getByRole('button', { name: 'Continue →' }).click();
  await expect(
    reuseCreator.locator('.ws-role-row[data-role-id="portfolio_manager"]')
  ).toContainText(MANAGER_NAME);
  await reuseCreator.getByRole('button', { name: 'Review →' }).click();
  await expect(reuseCreator.locator('#createFolderBtn')).toHaveText(
    `Use existing group “${RENAMED_GROUP}”`
  );
  await evidence(page, '06-music-home-renamed-reuse');
  await reuseCreator.locator('#createFolderBtn').click();
  await expect(reuseCreator).toBeHidden();

  const finalWorkspaces = await workspaces(request);
  expect(finalWorkspaces).toHaveLength(1);
  expect(finalWorkspaces[0]).toMatchObject({ id: homeID, name: RENAMED_GROUP, kind: 'group' });
  createdHomeSlug = finalWorkspaces[0].folder_slug;
  const finalHome = await json(await request.get(`/api/workspaces/${homeID}`));
  expect(finalHome.agent_instances).toHaveLength(1);
  const finalAssistant = await json(
    await request.get(`/api/workspaces/${homeID}/assistant-program`)
  );
  expect(finalAssistant.projects || []).toEqual([]);
  expect(finalAssistant.roster).toHaveLength(1);
});

test('Home project shelf initializes by review without a scan or a model', async ({
  page,
  request
}) => {
  expect(createdHomeID).toBeTruthy();
  await page.goto(`/workspaces/${encodeURIComponent(createdHomeSlug)}/assistant`);
  const shelf = page.locator('#projectLibraryPanel');
  await expect(shelf).toBeVisible();
  await expect(shelf.locator('#projectLibraryStatus')).toContainText('No library yet');
  await evidence(page, '07-project-library-before-initialization');
  await shelf.locator('#projectLibraryInitialize').click();
  const review = page.getByRole('dialog', { name: 'Start a Home project library?' });
  await expect(review).toContainText('Saved user notes are copied once');
  await review.getByRole('button', { name: 'Start library' }).click();
  await expect(shelf.locator('#projectLibraryContent')).toBeVisible();
  await expect(shelf.locator('#projectLibraryTitle')).toBeFocused();
  await expect(shelf.locator('#projectLibraryStatus')).toContainText(
    'native folder picker is unavailable'
  );
  const roots = await json(
    await request.get(`/api/workspaces/${createdHomeID}/assistant-program/library/roots`)
  );
  expect(roots).toMatchObject({ initialized: true, total_roots: 0, provider_read_only: false });
  const projects = await json(
    await request.get(`/api/workspaces/${createdHomeID}/assistant-program/library/projects`)
  );
  expect(projects).toMatchObject({ total: 0 });
  await evidence(page, '08-project-library-empty');
  await expect(shelf.locator('#projectLibraryAdd')).toBeDisabled();
  expect(
    (
      await json(
        await request.get(`/api/workspaces/${createdHomeID}/assistant-program/library/roots`)
      )
    ).total_roots
  ).toBe(0);
  await evidence(page, '09-project-library-picker-unavailable');

  // The real Home shelf still has a usable manual search with no model or
  // native picker, including keyboard submission on a phone-sized viewport.
  await page.setViewportSize({ width: 390, height: 844 });
  await shelf.scrollIntoViewIfNeeded();
  const search = shelf.getByRole('searchbox', { name: 'Search projects' });
  await search.fill('no such project');
  await search.press('Enter');
  await expect(shelf.locator('#projectLibraryStatus')).toContainText('No projects match');
  await expect(shelf.locator('#projectLibraryCount')).toContainText('0 of 0 projects');
  const overflow = await page.evaluate(
    () => document.documentElement.scrollWidth - document.documentElement.clientWidth
  );
  expect(overflow, 'phone-sized Home should not scroll sideways').toBeLessThanOrEqual(1);
  for (const select of await shelf.locator('#projectLibrarySearchForm select').all()) {
    const bounds = await select.boundingBox();
    expect(
      bounds?.width || 0,
      'mobile filter labels should not be truncated into narrow columns'
    ).toBeGreaterThan(250);
  }
  await evidence(page, '09a-project-library-mobile-search');
  await page.setViewportSize({ width: 1280, height: 900 });

  const disable = await request.post('/api/plugins/music-project-management/disable', {
    headers: { 'X-Requested-With': 'XMLHttpRequest' }
  });
  expect(disable.ok(), await disable.text()).toBeTruthy();
  await page.reload();
  const saved = await json(
    await request.get(`/api/workspaces/${createdHomeID}/assistant-program/library/roots`)
  );
  expect(saved).toMatchObject({ initialized: true, total_roots: 0, provider_read_only: true });
  await expect(page.locator('#projectLibraryPanel')).toBeVisible();
  await expect(page.locator('#projectLibraryAdd')).toBeHidden();
  await expect(page.locator('#projectLibraryStatus')).toContainText('Saved records are readable');
  await page.locator('#projectLibraryPanel').scrollIntoViewIfNeeded();
  await evidence(page, '10-project-library-provider-read-only');

  // Re-enabling the same installation is not a new provider identity.
  const reenable = await request.post('/api/plugins/music-project-management/enable', {
    headers: { 'X-Requested-With': 'XMLHttpRequest' }
  });
  expect(reenable.ok(), await reenable.text()).toBeTruthy();
  const reenabled = await json(
    await request.get(`/api/workspaces/${createdHomeID}/assistant-program/library/roots`)
  );
  expect(reenabled).toMatchObject({ initialized: true, total_roots: 0, provider_read_only: false });

  // Uninstall/reinstall deliberately uses the *same* clean package inside the
  // disposable sandbox: content fingerprints can collide across install epochs.
  const uninstall = await request.delete('/api/plugins/music-project-management', {
    headers: { 'X-Requested-With': 'XMLHttpRequest' }
  });
  expect(uninstall.ok(), await uninstall.text()).toBeTruthy();
  const removed = await json(
    await request.get(`/api/workspaces/${createdHomeID}/assistant-program/library/roots`)
  );
  expect(removed).toMatchObject({ initialized: true, total_roots: 0, provider_read_only: true });
  const reinstall = await request.post('/api/plugins/install', {
    headers: { 'X-Requested-With': 'XMLHttpRequest' },
    data: { source: PLUGIN_PATH, format: 'claude', confirm: true }
  });
  expect(reinstall.ok(), await reinstall.text()).toBeTruthy();
  const enableAgain = await request.post('/api/plugins/music-project-management/enable', {
    headers: { 'X-Requested-With': 'XMLHttpRequest' }
  });
  expect(enableAgain.ok(), await enableAgain.text()).toBeTruthy();
  const newlyInstalled = await json(
    await request.get(`/api/workspaces/${createdHomeID}/assistant-program/library/roots`)
  );
  expect(newlyInstalled).toMatchObject({
    initialized: true,
    total_roots: 0,
    provider_read_only: true
  });
  await page.reload();
  await expect(page.locator('#projectLibraryPanel')).toBeVisible();
  await expect(page.locator('#projectLibraryAdd')).toBeHidden();
  await page.locator('#projectLibraryPanel').scrollIntoViewIfNeeded();
  await evidence(page, '11-project-library-reinstall-still-read-only');
});
