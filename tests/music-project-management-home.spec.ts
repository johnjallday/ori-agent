import { expect, test, type APIRequestContext, type Page } from '@playwright/test';
import { existsSync, mkdirSync, readdirSync, readFileSync } from 'node:fs';
import path from 'node:path';

// Exact-candidate acceptance for the reciprocal Music Project Management and
// REAPER split-ownership contract. Run only through scripts/reaper-demo.sh so
// both candidate exports, HOME, ORI_DATA_DIR, skills, projects, and screenshots
// stay inside one disposable sandbox. No test opens or controls REAPER.
const ENABLED = process.env.ORI_MUSIC_REAPER_ACCEPTANCE === '1';
const RESTART_CHECK = process.env.ORI_MUSIC_REAPER_RESTART_CHECK === '1';
const ORDER = process.env.ORI_MUSIC_REAPER_INSTALL_ORDER || '';
const SHOTS = process.env.ORI_MUSIC_REAPER_EVIDENCE_DIR || 'test-results/music-reaper';
const REAPER_PATH = process.env.ORI_REAPER_PLUGIN_PATH || '';
const REAPER_REVISION = process.env.ORI_REAPER_PLUGIN_REVISION || '';
const MUSIC_PATH = process.env.ORI_MUSIC_PLUGIN_PATH || '';
const MUSIC_REVISION = process.env.ORI_MUSIC_PLUGIN_REVISION || '';
const FIRST_SNAPSHOT = process.env.ORI_MUSIC_REAPER_FIRST_SNAPSHOT || '';
const FINAL_SNAPSHOT = process.env.ORI_MUSIC_REAPER_FINAL_SNAPSHOT || '';
const SANDBOX = process.env.ORI_MUSIC_REAPER_SANDBOX || '';
const RUN = Date.now().toString(36);
const HOME_NAME = `Music Production Home ${RUN}`;
const MANAGER_NAME = 'Portfolio Manager';
const SONG_BLUEPRINT = 'plugin:reaper-plugin:reaper-song';

test.describe.configure({ mode: 'serial' });
test.setTimeout(120_000);
test.skip(!ENABLED, 'requires scripts/reaper-demo.sh with isolated exact candidates');

interface WorkspaceSummary {
  id: string;
  name: string;
  kind: string;
  folder_slug: string;
  parent_id?: string;
}

interface PluginSummary {
  name: string;
  version: string;
  enabled: boolean;
  source: string;
}

interface Snapshot {
  plugins: { plugins?: PluginSummary[] } | PluginSummary[];
  project_templates: { templates?: Array<Record<string, any>> };
  group_templates: { group_templates?: Array<Record<string, any>> };
  workspaces: { folders?: WorkspaceSummary[] };
  // The Set up REAPER quest's read at the checkpoint, or its HTTP status when
  // the quest does not exist yet (REAPER not installed).
  reaper_quest?: { setup_journey?: Record<string, any>; status?: number };
}

const QUEST_ROOT = '/api/setup-quests/reaper-plugin/reaper_setup';

function questGroupStep(journey: Record<string, any> | undefined): Record<string, any> {
  const step = (journey?.steps || []).find(
    (candidate: { kind: string }) => candidate.kind === 'project_connect'
  );
  expect(step, 'the quest has a project_connect step').toBeTruthy();
  return step;
}

// expectMusicProviderMissing asserts the group screen's read names Music
// Project Management and offers its reviewed install.
function expectMusicProviderMissing(journey: Record<string, any> | undefined) {
  const step = questGroupStep(journey);
  expect(step).toMatchObject({
    status: 'blocked',
    reason_code: 'home_provider_missing',
    preparation: { exists: false, group_policy: 'required' },
    home_provider: {
      plugin_id: 'music-project-management',
      display_name: 'Music Project Management',
      reviewed: true,
      installed: false,
      enabled: false,
      template_id: SONG_BLUEPRINT,
      reason: 'plugin_install_required'
    }
  });
  expect(step.home_provider.actions).toContain('install_plugin');
  expect(step.home_provider.summary).toContain('comes from a separate plugin');
}

function newKey(): string {
  return `acceptance-${RUN}-${Math.random().toString(36).slice(2)}`;
}

let homeID = '';
const projectIDs: string[] = [];

async function json(response: Awaited<ReturnType<APIRequestContext['get']>>) {
  const text = await response.text();
  expect(response.ok(), text).toBeTruthy();
  return JSON.parse(text);
}

async function installCandidate(
  request: APIRequestContext,
  source: string,
  format = ''
): Promise<void> {
  const data: Record<string, unknown> = { source, confirm: true };
  if (format) data.format = format;
  await json(await request.post('/api/plugins/install', { data }));
}

async function enableCandidate(request: APIRequestContext, pluginID: string): Promise<void> {
  await json(await request.post(`/api/plugins/${pluginID}/enable`));
}

function snapshot(path: string): Snapshot {
  expect(path).toBeTruthy();
  return JSON.parse(readFileSync(path, 'utf8'));
}

function pluginList(value: Snapshot['plugins']): PluginSummary[] {
  return Array.isArray(value) ? value : value.plugins || [];
}

function persistedWorkspace(id: string): { data: any; file: string } {
  if (!SANDBOX) throw new Error('ORI_MUSIC_REAPER_SANDBOX is required');
  const pending = [path.join(SANDBOX, 'workspace-staging')];
  while (pending.length) {
    const current = pending.pop()!;
    const workspaceFile = path.join(current, 'workspace.json');
    if (existsSync(workspaceFile)) {
      const data = JSON.parse(readFileSync(workspaceFile, 'utf8'));
      if (data.id === id) return { data, file: workspaceFile };
    }
    for (const entry of readdirSync(current, { withFileTypes: true })) {
      if (entry.isDirectory()) pending.push(path.join(current, entry.name));
    }
  }
  throw new Error(`persisted workspace ${id} was not found in the isolated sandbox`);
}

function persistedProjectFile(workspaceFile: string, workspace: any): string {
  return path.join(
    path.dirname(workspaceFile),
    workspace.project_path,
    workspace.shared_data.project_entry.relative_path
  );
}

async function workspaces(request: APIRequestContext): Promise<WorkspaceSummary[]> {
  return (await json(await request.get('/api/workspaces'))).folders || [];
}

async function evidence(page: Page, name: string) {
  mkdirSync(SHOTS, { recursive: true });
  await page.screenshot({ path: `${SHOTS}/${ORDER}-${name}.png` });
}

async function createProjectRoleAgent(page: Page, label: string, name: string) {
  const row = page.locator('.ws-role-row', { hasText: label }).filter({
    has: page.getByRole('button', { name: `Create an agent for ${label}` })
  });
  await row.getByRole('button', { name: `Create an agent for ${label}` }).click();
  await expect(page.locator('#addAgentModal')).toBeVisible();
  await page.locator('[data-agent-create-field="name"]').fill(name);
  await expect(page.locator('#toastContainer .toast.show')).toHaveCount(0, { timeout: 15_000 });
  await page.locator('#createAgentBtn').click();
  await expect(page.locator('#addAgentModal')).toBeHidden();
  await expect(page.locator('#addFolderModal')).toBeVisible();
}

async function openWorkspaceCreator(page: Page, entryPoint: string) {
  await page.goto('/');
  await page.waitForFunction(() => Boolean((window as any).sessionManager?.showAddWorkspaceModal));
  await page.evaluate(value => {
    (window as any).sessionManager.showAddWorkspaceModal({
      kind: 'workspace',
      entryPoint: value
    });
  }, entryPoint);
  const creator = page.locator('#addFolderModal');
  await expect(creator).toBeVisible();
  return creator;
}

async function chooseTemplateAndDetails(
  page: Page,
  templateID: string,
  name: string,
  tempo: string
) {
  const creator = await openWorkspaceCreator(page, `music_reaper_${ORDER}`);
  await creator.locator(`#templatePicker [data-template-id="${templateID}"]`).click();
  await creator.locator('#wizardNextBtn').click();
  await expect(creator.locator('#wizardStep2')).toBeVisible();
  await creator.locator('#folderNameInput').fill(name);
  const tempoInput = creator.locator('#workspaceBlueprintInput-tempo');
  if (await tempoInput.count()) await tempoInput.fill(tempo);
  const openProject = creator.locator('#projectTemplateOpenAfterCreateToggle');
  if (await openProject.isChecked()) await openProject.uncheck();
  return creator;
}

async function createGroupedSong(page: Page, request: APIRequestContext, index: number) {
  const songName = `${index === 0 ? 'First' : 'Second'} Split Song ${RUN}`;
  const team = ['Producer', 'Mix Engineer', 'Songwriter'].map(label => [
    label,
    `${songName} ${label}`
  ]);
  const creator = await chooseTemplateAndDetails(page, SONG_BLUEPRINT, songName, `${118 + index}`);
  const destination = creator.locator('#workspaceGroupDestinationCard');
  await expect(destination).toContainText(HOME_NAME);
  await expect(destination).toContainText('Existing verified group');
  await expect(destination).toContainText('Plugin: music-project-management 0.1.0');
  if (index === 0) await evidence(page, '03-grouped-song-destination');

  await creator.locator('#wizardNextBtn').click();
  await expect(creator.locator('#wizardStep3')).toBeVisible();
  const homeScope = creator.locator('[data-scope="home"]');
  await expect(homeScope).toContainText(MANAGER_NAME);
  await expect(
    homeScope.getByRole('button', { name: /Create an agent|Assign an agent/ })
  ).toHaveCount(0);
  for (const [label, name] of team) await createProjectRoleAgent(page, label, name);

  await creator.locator('#wizardNextBtn').click();
  await expect(creator.locator('#workspaceReviewSummary')).toContainText('Reaper Song');
  await expect(creator.locator('#workspaceReviewSummary')).toContainText(
    `Tempo: ${118 + index} BPM`
  );
  await expect(creator.locator('#workspaceReviewSummary')).toContainText(
    `${MANAGER_NAME} · already staffed on the group`
  );
  if (index === 0) await evidence(page, '04-grouped-song-review');

  const reviewResponse = page.waitForResponse(
    response =>
      new URL(response.url()).pathname === '/api/workspaces' &&
      response.request().method() === 'POST' &&
      response.request().postDataJSON()?.group_requirement_review === true
  );
  await creator.locator('#createFolderBtn').click();
  expect((await reviewResponse).ok()).toBeTruthy();

  const commitResponse = page.waitForResponse(
    response =>
      new URL(response.url()).pathname === '/api/workspaces' &&
      response.request().method() === 'POST' &&
      Boolean(response.request().postDataJSON()?.group_review_token)
  );
  await creator.locator('#createFolderBtn').click();
  const committed = await commitResponse;
  expect(committed.ok(), await committed.text()).toBeTruthy();
  const body = await committed.json();
  expect(body.assistant_station_id).toBe(homeID);
  await page.waitForURL(url => /^\/workspaces\/[^/]+$/.test(url.pathname));

  const project = await json(await request.get(`/api/workspaces/${body.folder.id}`));
  expect(project.parent_id).toBe(homeID);
  expect(project.agent_instances.map((agent: { name: string }) => agent.name).sort()).toEqual(
    team.map(([, name]) => name).sort()
  );
  const assistant = await json(
    await request.get(`/api/workspaces/${body.folder.id}/assistant-program`)
  );
  expect(assistant).toMatchObject({
    available: true,
    station_id: homeID,
    project_id: body.folder.id,
    home_provider_available: true,
    project_provider_available: true,
    roster_scope: 'project'
  });
  expect(assistant.roster.map((binding: { role_id: string }) => binding.role_id).sort()).toEqual([
    'engineer',
    'producer',
    'songwriter'
  ]);

  const persisted = persistedWorkspace(project.id);
  expect(persisted.data.assistant_project_link).toMatchObject({
    station_workspace_id: homeID,
    home_provider: {
      plugin_id: 'music-project-management',
      plugin_version: '0.1.0',
      program_id: 'music-producer-assistant',
      home_schema_version: 1,
      home_version: 1,
      plugin_generation: 1
    },
    project_provider: {
      plugin_id: 'reaper-plugin',
      plugin_version: '0.8.0',
      blueprint_id: 'reaper-song',
      blueprint_version: 9,
      project_team_id: 'reaper-song-team',
      project_team_schema_version: 1,
      project_team_version: 1,
      plugin_generation: 1
    }
  });
  expect(
    persisted.data.assistant_project_link.project_roles.map((role: { id: string }) => role.id)
  ).toEqual(['producer', 'engineer', 'songwriter']);
  expect(persisted.data.shared_data.blueprint_inputs.values.tempo).toBe(`${118 + index}`);
  for (const evidence of [
    persisted.data.assistant_project_link.home_provider.declaration_digest,
    persisted.data.assistant_project_link.home_provider.component_fingerprint,
    persisted.data.assistant_project_link.project_provider.project_team_digest,
    persisted.data.assistant_project_link.project_provider.component_fingerprint
  ]) {
    expect(evidence).toMatch(/^[a-f0-9]{64}$/);
  }
  expect(readFileSync(persistedProjectFile(persisted.file, persisted.data), 'utf8')).toContain(
    `TEMPO ${118 + index} 4 4`
  );
  projectIDs.push(project.id);
}

test('installation order preserves consequence-free provider boundaries', async ({ request }) => {
  expect(['music-first', 'reaper-first', 'reaper-only']).toContain(ORDER);
  expect(REAPER_PATH).toBeTruthy();
  expect(REAPER_REVISION).toMatch(/^[a-f0-9]{40}$/);
  const first = snapshot(FIRST_SNAPSHOT);
  const final = snapshot(FINAL_SNAPSHOT);
  expect(first.workspaces.folders || []).toEqual([]);
  expect(final.workspaces.folders || []).toEqual([]);

  const firstPlugins = pluginList(first.plugins);
  if (ORDER === 'music-first') {
    expect(firstPlugins).toHaveLength(1);
    expect(firstPlugins[0]).toMatchObject({
      name: 'music-project-management',
      version: '0.1.0',
      enabled: true
    });
    expect(firstPlugins[0].source).toBe(MUSIC_PATH);
    expect(
      first.project_templates.templates?.some(template => template.id === SONG_BLUEPRINT)
    ).toBe(false);
    const musicHome = first.group_templates.group_templates?.find(
      template => template.provider?.plugin_id === 'music-project-management'
    );
    expect(musicHome).toMatchObject({ availability: { state: 'creatable' } });
  } else {
    expect(firstPlugins).toHaveLength(1);
    expect(firstPlugins[0]).toMatchObject({
      name: 'reaper-plugin',
      version: '0.8.0',
      enabled: true
    });
    expect(firstPlugins[0].source).toBe(REAPER_PATH);
    const reaper = first.project_templates.templates?.find(
      template => template.id === SONG_BLUEPRINT
    );
    expect(reaper).toMatchObject({
      readiness: { state: 'action_required', reason: 'plugin_install_required' },
      assistant_project: {
        id: 'reaper-song-team',
        home: {
          provider_plugin_id: 'music-project-management',
          program_id: 'music-producer-assistant'
        }
      }
    });
    expect(
      first.group_templates.group_templates?.some(
        template => template.provider?.plugin_id === 'music-project-management'
      )
    ).toBe(false);
    // With REAPER alone, Set up REAPER's group screen names the missing Home
    // provider and offers its reviewed install instead of "could not be verified".
    expectMusicProviderMissing(first.reaper_quest?.setup_journey);
  }

  const finalPlugins = pluginList(final.plugins);
  if (ORDER === 'reaper-only') {
    expect(finalPlugins).toHaveLength(1);
    expect(finalPlugins[0]).toMatchObject({
      name: 'reaper-plugin',
      version: '0.8.0',
      enabled: true
    });
    expect(MUSIC_PATH).toBe('');
    expectMusicProviderMissing(final.reaper_quest?.setup_journey);
  } else {
    // Both providers are ready: the group screen offers Build Group.
    const group = questGroupStep(final.reaper_quest?.setup_journey);
    expect(group.home_provider).toBeUndefined();
    expect(group.preparation).toMatchObject({ exists: false, group_policy: 'required' });
    expect((group.actions || []).map((action: { id: string }) => action.id)).toContain(
      'review_create_group'
    );
    expect(MUSIC_REVISION).toMatch(/^[a-f0-9]{40}$/);
    expect(finalPlugins.map(plugin => plugin.name).sort()).toEqual([
      'music-project-management',
      'reaper-plugin'
    ]);
    const reaper = final.project_templates.templates?.find(
      template => template.id === SONG_BLUEPRINT
    );
    expect(reaper).toMatchObject({
      readiness: { state: 'ready' },
      user_setup_quest_eligibility: { eligible: true }
    });
  }

  const livePlugins = await json(await request.get('/api/plugins'));
  expect(
    pluginList(livePlugins)
      .map(plugin => plugin.name)
      .sort()
  ).toEqual(finalPlugins.map(plugin => plugin.name).sort());
});

test('Set up REAPER builds the split Home through its own routes, once', async ({ request }) => {
  test.skip(
    ORDER !== 'reaper-first',
    'quest Build Group acceptance runs in the reaper-first order'
  );
  await json(await request.post('/api/onboarding/skip'));
  expect(await workspaces(request)).toEqual([]);

  let journey = (await json(await request.get(QUEST_ROOT))).setup_journey;
  journey = (
    await json(
      await request.post(`${QUEST_ROOT}/open`, {
        data: { if_revision: journey.state_revision, idempotency_key: newKey() }
      })
    )
  ).setup_journey;
  const ready = questGroupStep(journey);
  expect(ready.home_provider).toBeUndefined();
  expect(ready.preparation).toMatchObject({ exists: false, name: 'Music Production Home' });
  expect(ready.actions.map((action: { id: string }) => action.id)).toEqual(['review_create_group']);

  const run = `${QUEST_ROOT}/runs/${encodeURIComponent(journey.run_id)}`;
  const questHomeName = `Quest Music Home ${RUN}`;
  const reviewed = await json(
    await request.post(`${run}/actions/review_create_group`, {
      data: {
        if_revision: journey.state_revision,
        idempotency_key: newKey(),
        input: { name: questHomeName }
      }
    })
  );
  expect(reviewed.review).toMatchObject({
    commit_action: 'create_group',
    group: { name: questHomeName, exists: false }
  });
  const commit = {
    if_revision: reviewed.setup_journey.state_revision,
    idempotency_key: newKey(),
    review_token: reviewed.review.token,
    input: { name: questHomeName }
  };
  journey = (await json(await request.post(`${run}/actions/create_group`, { data: commit })))
    .setup_journey;
  const built = questGroupStep(journey);
  expect(built.preparation).toMatchObject({ exists: true, name: questHomeName });
  const questHomeID = built.preparation.group_id;
  expect(questHomeID).toBeTruthy();

  const created = await workspaces(request);
  expect(created).toHaveLength(1);
  expect(created[0]).toMatchObject({ id: questHomeID, name: questHomeName, kind: 'group' });
  const persisted = persistedWorkspace(questHomeID).data.assistant_program_state;
  expect(persisted).toMatchObject({
    key: {
      owner_user_id: 'local',
      plugin_id: 'music-project-management',
      program_id: 'music-producer-assistant'
    },
    home_provider: {
      plugin_id: 'music-project-management',
      plugin_version: '0.1.0',
      program_id: 'music-producer-assistant',
      home_schema_version: 1,
      home_version: 1,
      plugin_generation: 1
    }
  });
  expect(persisted.home_provider.declaration_digest).toMatch(/^[a-f0-9]{64}$/);
  expect(persisted.home_provider.component_fingerprint).toMatch(/^[a-f0-9]{64}$/);
  expect(
    persisted.declaration.roles.every((role: { scope: string }) => role.scope === 'home')
  ).toBe(true);

  // Replaying the same confirmed request, or reviewing a new one, never builds
  // a second Home.
  const replay = await request.post(`${run}/actions/create_group`, { data: commit });
  expect(replay.status()).toBeLessThan(500);
  const again = await request.post(`${run}/actions/review_create_group`, {
    data: {
      if_revision: journey.state_revision,
      idempotency_key: newKey(),
      input: { name: `${questHomeName} Again` }
    }
  });
  expect(again.ok()).toBe(false);
  expect(await workspaces(request)).toHaveLength(1);
  const reread = questGroupStep((await json(await request.get(run))).setup_journey);
  expect(reread.preparation).toMatchObject({ exists: true, group_id: questHomeID });

  // Remove the quest-built Home so the Group Template acceptance below starts
  // from the same empty sandbox it always has.
  // An Assistant Home is removed through its own reviewed removal first.
  const summary = await json(await request.get(`/api/workspaces/${questHomeID}/assistant-program`));
  const removal = await json(
    await request.post(`/api/workspaces/${questHomeID}/assistant-program/remove-home/review`, {
      data: { state_revision: summary.state_revision }
    })
  );
  await json(
    await request.post(`/api/workspaces/${questHomeID}/assistant-program/remove-home/commit`, {
      data: { token: removal.token }
    })
  );
  // The reviewed removal may already have moved the Home to the Trash.
  if ((await workspaces(request)).some(workspace => workspace.id === questHomeID)) {
    const removed = await request.delete(`/api/workspaces/${questHomeID}?confirm=true`);
    expect(removed.ok(), await removed.text()).toBeTruthy();
  }
  await expect.poll(async () => (await workspaces(request)).length).toBe(0);
});

test('two REAPER projects share one independently staffed Home and one manager', async ({
  page,
  request
}) => {
  test.skip(ORDER === 'reaper-only', 'combined-provider acceptance only');
  await json(await request.post('/api/onboarding/skip'));
  expect(await workspaces(request)).toEqual([]);

  await page.goto('/');
  await page.getByRole('button', { name: 'Tree', exact: true }).click();
  await page.locator('[data-tree-new-group]').click();
  const creator = page.locator('#addFolderModal');
  const music = creator.locator('.workspace-group-template-option', {
    hasText: 'Music Production Home'
  });
  await expect(music).toContainText('Plugin: music-project-management 0.1.0');
  await music.locator('input').check();
  await creator.getByRole('button', { name: 'Continue →' }).click();
  await creator.locator('#folderNameInput').fill(HOME_NAME);
  await creator.getByRole('button', { name: 'Continue →' }).click();
  await expect(creator.locator('.ws-role-row[data-role-id="portfolio_manager"]')).toContainText(
    MANAGER_NAME
  );
  await creator.getByRole('button', { name: 'Review →' }).click();
  await evidence(page, '01-independent-home-review');
  await creator.locator('#createFolderBtn').click();
  await expect(creator).toBeHidden();
  await page.waitForURL(url => /^\/workspaces\/[^/]+$/.test(url.pathname));

  const created = await workspaces(request);
  expect(created).toHaveLength(1);
  expect(created[0]).toMatchObject({ name: HOME_NAME, kind: 'group' });
  homeID = created[0].id;
  const homeBeforeProjects = await json(await request.get(`/api/workspaces/${homeID}`));
  expect(homeBeforeProjects.agent_instances).toHaveLength(1);
  expect(homeBeforeProjects.agent_instances[0]).toMatchObject({
    name: MANAGER_NAME,
    role_id: 'portfolio_manager'
  });
  const persistedHomeBefore = persistedWorkspace(homeID).data;
  expect(persistedHomeBefore.assistant_program_state).toMatchObject({
    key: {
      owner_user_id: 'local',
      plugin_id: 'music-project-management',
      program_id: 'music-producer-assistant'
    },
    home_provider: {
      plugin_id: 'music-project-management',
      plugin_version: '0.1.0',
      program_id: 'music-producer-assistant',
      home_schema_version: 1,
      home_version: 1,
      plugin_generation: 1
    },
    group_template: {
      program_home_owner: {
        plugin_id: 'music-project-management',
        plugin_version: '0.1.0',
        program_id: 'music-producer-assistant'
      }
    }
  });
  expect(
    persistedHomeBefore.assistant_program_state.declaration.roles.every(
      (role: { scope: string }) => role.scope === 'home'
    )
  ).toBe(true);
  await evidence(page, '02-independent-home-ready');

  await createGroupedSong(page, request, 0);
  await createGroupedSong(page, request, 1);
  expect(projectIDs).toHaveLength(2);

  const home = await json(await request.get(`/api/workspaces/${homeID}`));
  expect(home.agent_instances).toHaveLength(1);
  expect(home.agent_instances[0]).toMatchObject({ name: MANAGER_NAME });
  const assistant = await json(await request.get(`/api/workspaces/${homeID}/assistant-program`));
  expect(assistant).toMatchObject({
    available: true,
    is_station: true,
    hired: true,
    home_provider_available: true,
    project_provider_available: true,
    primary_name: MANAGER_NAME
  });
  expect(assistant.projects.map((project: { id: string }) => project.id).sort()).toEqual(
    [...projectIDs].sort()
  );
  expect(assistant.roster).toHaveLength(1);
  const persistedHomeAfter = persistedWorkspace(homeID).data;
  expect(persistedHomeAfter.assistant_program_state.linked_project_ids.sort()).toEqual(
    [...projectIDs].sort()
  );
  expect(persistedHomeAfter.agent_instances).toHaveLength(1);

  await page.goto(`/workspaces/${encodeURIComponent(created[0].folder_slug)}`);
  await page.waitForTimeout(500);
  await evidence(page, '05-shared-home-two-projects');
});

test('a reviewed Home handoff creates one inert child-owned Ticket', async ({ request }) => {
  test.skip(ORDER === 'reaper-only', 'combined-provider acceptance only');
  expect(homeID).toBeTruthy();
  expect(projectIDs).toHaveLength(2);
  const portfolio = await json(
    await request.get(`/api/workspaces/${homeID}/assistant-program/portfolio`)
  );
  const project = portfolio.projects.find(
    (entry: { project_workspace_id: string }) => entry.project_workspace_id === projectIDs[0]
  );
  expect(project).toBeTruthy();
  const linkID = project.link_id;
  const input = {
    link_id: linkID,
    title: `Review split delivery ${RUN}`,
    description: 'Check the child-owned deliverable without starting work.',
    state: 'backlog'
  };
  const review = await json(
    await request.post(`/api/workspaces/${homeID}/assistant-program/handoffs/review`, {
      data: input
    })
  );
  expect(review.token).toBeTruthy();
  expect(review.handoff).toMatchObject({
    link_id: linkID,
    project_workspace_id: projectIDs[0],
    state: 'backlog',
    assignment: 'Unassigned in the child for explicit project-team triage',
    authority_boundary:
      'Creates one child-owned Ticket only; the Home receives no child tools or project access.'
  });
  const receipt = await json(
    await request.post(`/api/workspaces/${homeID}/assistant-program/handoffs/commit`, {
      data: {
        review_token: review.token,
        idempotency_key: `music-reaper-handoff-${RUN}`,
        title: input.title,
        description: input.description,
        state: input.state
      }
    })
  );
  expect(receipt).toMatchObject({ project_workspace_id: projectIDs[0], link_id: linkID });
  const ticket = await json(
    await request.get(`/api/workspaces/${projectIDs[0]}/tickets/${receipt.ticket_id}`)
  );
  expect(ticket).toMatchObject({
    id: receipt.ticket_id,
    owning_workspace_id: projectIDs[0],
    title: input.title,
    state: 'backlog',
    source: 'assistant'
  });
  expect(ticket.current_run_id || '').toBe('');
  expect(ticket.assignee || '').toBe('');
  expect(ticket.required_capabilities || []).toEqual([]);
  expect(ticket.schedule_enabled || false).toBe(false);
  const homeTickets = await json(await request.get(`/api/workspaces/${homeID}/tickets`));
  expect((homeTickets.tickets || []).some((item: { id: string }) => item.id === ticket.id)).toBe(
    false
  );
});

test('provider removal and exact reinstall preserve independent data and authority', async ({
  page,
  request
}) => {
  test.skip(ORDER === 'reaper-only', 'combined-provider lifecycle acceptance only');
  expect(homeID).toBeTruthy();
  expect(projectIDs).toHaveLength(2);

  const beforeWorkspaces = await workspaces(request);
  const homeSummary = beforeWorkspaces.find(workspace => workspace.id === homeID)!;
  const persistedHomeBefore = persistedWorkspace(homeID).data;
  const persistedProjectsBefore = projectIDs.map(id => persistedWorkspace(id));
  const linksBefore = persistedProjectsBefore.map(project =>
    JSON.stringify(project.data.assistant_project_link)
  );
  const projectFilesBefore = persistedProjectsBefore.map(project =>
    readFileSync(persistedProjectFile(project.file, project.data), 'utf8')
  );
  const ticketsBefore = await json(await request.get(`/api/workspaces/${projectIDs[0]}/tickets`));
  const portfolio = await json(
    await request.get(`/api/workspaces/${homeID}/assistant-program/portfolio`)
  );
  const firstLink = portfolio.projects.find(
    (entry: { project_workspace_id: string }) => entry.project_workspace_id === projectIDs[0]
  );
  expect(firstLink).toBeTruthy();

  await json(await request.post('/api/plugins/reaper-plugin/disable'));
  let homeAssistant = await json(await request.get(`/api/workspaces/${homeID}/assistant-program`));
  expect(homeAssistant).toMatchObject({ home_provider_available: true });
  let projectAssistant = await json(
    await request.get(`/api/workspaces/${projectIDs[0]}/assistant-program`)
  );
  expect(projectAssistant).toMatchObject({
    home_provider_available: true,
    project_provider_available: false
  });
  const blockedWithoutProject = await request.post(
    `/api/workspaces/${homeID}/assistant-program/handoffs/review`,
    {
      data: {
        link_id: firstLink.link_id,
        title: `Blocked without project provider ${RUN}`,
        state: 'backlog'
      }
    }
  );
  expect(blockedWithoutProject.status(), await blockedWithoutProject.text()).toBe(409);
  await page.goto(`/workspaces/${encodeURIComponent(homeSummary.folder_slug)}`);
  await page.waitForTimeout(500);
  await evidence(page, '06-reaper-disabled-home-preserved');

  const removeReaper = await request.delete('/api/plugins/reaper-plugin');
  expect(removeReaper.ok(), await removeReaper.text()).toBeTruthy();
  expect(
    pluginList(await json(await request.get('/api/plugins'))).map(plugin => plugin.name)
  ).toEqual(['music-project-management']);
  expect((await workspaces(request)).map(workspace => workspace.id).sort()).toEqual(
    beforeWorkspaces.map(workspace => workspace.id).sort()
  );
  projectAssistant = await json(
    await request.get(`/api/workspaces/${projectIDs[0]}/assistant-program`)
  );
  expect(projectAssistant).toMatchObject({
    home_provider_available: true,
    project_provider_available: false
  });

  await installCandidate(request, REAPER_PATH);
  await enableCandidate(request, 'reaper-plugin');
  projectAssistant = await json(
    await request.get(`/api/workspaces/${projectIDs[0]}/assistant-program`)
  );
  expect(projectAssistant).toMatchObject({
    home_provider_available: true,
    project_provider_available: true
  });

  await json(await request.post('/api/plugins/music-project-management/disable'));
  homeAssistant = await json(await request.get(`/api/workspaces/${homeID}/assistant-program`));
  expect(homeAssistant).toMatchObject({ home_provider_available: false });
  projectAssistant = await json(
    await request.get(`/api/workspaces/${projectIDs[0]}/assistant-program`)
  );
  expect(projectAssistant).toMatchObject({
    home_provider_available: false,
    project_provider_available: true
  });
  const blockedWithoutHome = await request.post(
    `/api/workspaces/${homeID}/assistant-program/handoffs/review`,
    {
      data: {
        link_id: firstLink.link_id,
        title: `Blocked without Home provider ${RUN}`,
        state: 'backlog'
      }
    }
  );
  expect(blockedWithoutHome.status(), await blockedWithoutHome.text()).toBe(409);
  await page.reload();
  await page.waitForTimeout(500);
  await evidence(page, '07-music-disabled-projects-preserved');

  const removeMusic = await request.delete('/api/plugins/music-project-management');
  expect(removeMusic.ok(), await removeMusic.text()).toBeTruthy();
  expect(
    pluginList(await json(await request.get('/api/plugins'))).map(plugin => plugin.name)
  ).toEqual(['reaper-plugin']);
  expect((await workspaces(request)).map(workspace => workspace.id).sort()).toEqual(
    beforeWorkspaces.map(workspace => workspace.id).sort()
  );
  homeAssistant = await json(await request.get(`/api/workspaces/${homeID}/assistant-program`));
  expect(homeAssistant).toMatchObject({ home_provider_available: false });
  projectAssistant = await json(
    await request.get(`/api/workspaces/${projectIDs[0]}/assistant-program`)
  );
  expect(projectAssistant).toMatchObject({
    home_provider_available: false,
    project_provider_available: true
  });

  await installCandidate(request, MUSIC_PATH, 'claude');
  await enableCandidate(request, 'music-project-management');
  homeAssistant = await json(await request.get(`/api/workspaces/${homeID}/assistant-program`));
  expect(homeAssistant).toMatchObject({
    home_provider_available: true,
    project_provider_available: true,
    primary_name: MANAGER_NAME
  });
  expect(homeAssistant.projects.map((project: { id: string }) => project.id).sort()).toEqual(
    [...projectIDs].sort()
  );
  expect(homeAssistant.roster).toHaveLength(1);

  const afterWorkspaces = await workspaces(request);
  expect(afterWorkspaces.map(workspace => workspace.id).sort()).toEqual(
    beforeWorkspaces.map(workspace => workspace.id).sort()
  );
  const persistedHomeAfter = persistedWorkspace(homeID).data;
  expect(persistedHomeAfter.assistant_program_state.key).toEqual(
    persistedHomeBefore.assistant_program_state.key
  );
  expect(persistedHomeAfter.assistant_program_state.linked_project_ids.sort()).toEqual(
    [...projectIDs].sort()
  );
  expect(persistedHomeAfter.agent_instances).toHaveLength(1);
  for (const [index, id] of projectIDs.entries()) {
    const persisted = persistedWorkspace(id);
    expect(JSON.stringify(persisted.data.assistant_project_link)).toBe(linksBefore[index]);
    expect(readFileSync(persistedProjectFile(persisted.file, persisted.data), 'utf8')).toBe(
      projectFilesBefore[index]
    );
  }
  const ticketsAfter = await json(await request.get(`/api/workspaces/${projectIDs[0]}/tickets`));
  expect((ticketsAfter.tickets || []).map((ticket: { id: string }) => ticket.id).sort()).toEqual(
    (ticketsBefore.tickets || []).map((ticket: { id: string }) => ticket.id).sort()
  );
  await page.reload();
  await page.waitForTimeout(500);
  await evidence(page, '08-exact-reinstall-restored-availability');
});

test('restart preserves exact candidate identities, links, roles, and files', async ({
  page,
  request
}) => {
  test.skip(!RESTART_CHECK, 'run by scripts/reaper-demo.sh after its controlled restart');
  const plugins = pluginList(await json(await request.get('/api/plugins')));
  expect(plugins.every(plugin => plugin.enabled)).toBe(true);
  const all = await workspaces(request);

  if (ORDER === 'reaper-only') {
    expect(plugins.map(plugin => plugin.name)).toEqual(['reaper-plugin']);
    expect(all).toHaveLength(1);
    const persisted = persistedWorkspace(all[0].id);
    expect(persisted.data.assistant_program_state).toBeUndefined();
    expect(persisted.data.assistant_project_link).toBeUndefined();
    expect(readFileSync(persistedProjectFile(persisted.file, persisted.data), 'utf8')).toContain(
      'TEMPO 127 4 4'
    );
    const assistant = await json(
      await request.get(`/api/workspaces/${all[0].id}/assistant-program`)
    );
    expect(assistant).toMatchObject({ available: false, project_id: all[0].id });
    await page.goto(`/workspaces/${encodeURIComponent(all[0].folder_slug)}`);
    await page.waitForTimeout(500);
    await evidence(page, 'restart-standalone-preserved');
    return;
  }

  expect(plugins.map(plugin => plugin.name).sort()).toEqual([
    'music-project-management',
    'reaper-plugin'
  ]);
  const home = all.find(workspace => workspace.kind === 'group');
  expect(home).toBeTruthy();
  const projects = all.filter(workspace => workspace.parent_id === home!.id);
  expect(projects).toHaveLength(2);
  const persistedHome = persistedWorkspace(home!.id).data;
  expect(persistedHome.agent_instances).toHaveLength(1);
  expect(persistedHome.agent_instances[0]).toMatchObject({
    name: MANAGER_NAME,
    role_id: 'portfolio_manager'
  });
  expect(persistedHome.assistant_program_state.key).toMatchObject({
    owner_user_id: 'local',
    plugin_id: 'music-project-management',
    program_id: 'music-producer-assistant'
  });
  expect(persistedHome.assistant_program_state.linked_project_ids.sort()).toEqual(
    projects.map(project => project.id).sort()
  );
  const homeAssistant = await json(
    await request.get(`/api/workspaces/${home!.id}/assistant-program`)
  );
  expect(homeAssistant).toMatchObject({
    available: true,
    hired: true,
    home_provider_available: true,
    primary_name: MANAGER_NAME
  });
  expect(homeAssistant.projects).toHaveLength(2);
  expect(homeAssistant.roster).toHaveLength(1);

  let handoffTickets = 0;
  for (const project of projects) {
    const persisted = persistedWorkspace(project.id);
    expect(persisted.data.assistant_project_link).toMatchObject({
      station_workspace_id: home!.id,
      home_provider: {
        plugin_id: 'music-project-management',
        plugin_version: '0.1.0',
        program_id: 'music-producer-assistant'
      },
      project_provider: {
        plugin_id: 'reaper-plugin',
        plugin_version: '0.8.0',
        blueprint_id: 'reaper-song',
        blueprint_version: 9,
        project_team_id: 'reaper-song-team'
      }
    });
    expect(persisted.data.agent_instances).toHaveLength(3);
    expect(readFileSync(persistedProjectFile(persisted.file, persisted.data), 'utf8')).toMatch(
      /TEMPO (118|119) 4 4/
    );
    const assistant = await json(
      await request.get(`/api/workspaces/${project.id}/assistant-program`)
    );
    expect(assistant).toMatchObject({
      home_provider_available: true,
      project_provider_available: true,
      roster_scope: 'project'
    });
    const tickets = await json(await request.get(`/api/workspaces/${project.id}/tickets`));
    handoffTickets += (tickets.tickets || []).filter(
      (ticket: { source: string }) => ticket.source === 'assistant'
    ).length;
  }
  expect(handoffTickets).toBe(1);
  await page.goto(`/workspaces/${encodeURIComponent(home!.folder_slug)}`);
  await page.waitForTimeout(500);
  await evidence(page, 'restart-combined-state-preserved');
});

test('REAPER alone creates only an explicit Home-free standalone variant', async ({
  page,
  request
}) => {
  test.skip(ORDER !== 'reaper-only', 'standalone acceptance runs in its own REAPER-only sandbox');
  await json(await request.post('/api/onboarding/skip'));
  expect(await workspaces(request)).toEqual([]);

  const variantResponse = await request.post(
    `/api/project-templates/${encodeURIComponent(SONG_BLUEPRINT)}/variants`,
    {
      data: {
        name: `Standalone Reaper Song ${RUN}`,
        group_requirement: {
          schema_version: 2,
          policy: 'none',
          assistant_project_id: 'reaper-song-team'
        }
      }
    }
  );
  const variant = await json(variantResponse);
  expect(variant.template).toMatchObject({
    group_requirement: {
      schema_version: 2,
      policy: 'none',
      assistant_project_id: 'reaper-song-team'
    }
  });

  const projectName = `Standalone Song ${RUN}`;
  const creator = await chooseTemplateAndDetails(page, variant.template.id, projectName, '127');
  await expect(creator.locator('#workspaceGroupDestinationCard')).toContainText(
    'Standalone workspace'
  );
  await expect(creator.locator('#workspaceGroupDestinationCard')).toContainText(
    'No program Home or membership'
  );
  await creator.locator('#wizardNextBtn').click();
  await expect(creator.locator('#wizardStep3')).toBeVisible();
  const team = ['Producer', 'Mix Engineer', 'Songwriter'].map(label => [
    label,
    `${projectName} ${label}`
  ]);
  for (const [label, name] of team) await createProjectRoleAgent(page, label, name);
  await creator.locator('#wizardNextBtn').click();
  await expect(creator.locator('#workspaceReviewSummary')).toContainText(
    'No Home or Assistant Program membership'
  );
  await expect(creator.locator('#workspaceReviewSummary')).toContainText('Tempo: 127 BPM');
  await evidence(page, '01-standalone-review');

  const reviewResponse = page.waitForResponse(
    response =>
      new URL(response.url()).pathname === '/api/workspaces' &&
      response.request().method() === 'POST' &&
      response.request().postDataJSON()?.group_requirement_review === true
  );
  await creator.locator('#createFolderBtn').click();
  expect((await reviewResponse).ok()).toBeTruthy();

  const commitResponse = page.waitForResponse(
    response =>
      new URL(response.url()).pathname === '/api/workspaces' &&
      response.request().method() === 'POST' &&
      Boolean(response.request().postDataJSON()?.group_review_token)
  );
  await creator.locator('#createFolderBtn').click();
  const committed = await commitResponse;
  expect(committed.ok(), await committed.text()).toBeTruthy();
  const body = await committed.json();
  await page.waitForURL(url => /^\/workspaces\/[^/]+$/.test(url.pathname));

  const all = await workspaces(request);
  expect(all).toHaveLength(1);
  expect(all[0]).toMatchObject({ id: body.folder.id, name: projectName, kind: 'workspace' });
  expect(all[0].parent_id || '').toBe('');
  const project = await json(await request.get(`/api/workspaces/${body.folder.id}`));
  const assistant = await json(
    await request.get(`/api/workspaces/${body.folder.id}/assistant-program`)
  );
  expect(assistant).toMatchObject({ available: false, project_id: body.folder.id });
  expect(assistant.activation_needed || false).toBe(false);
  expect(project.agent_instances.map((agent: { name: string }) => agent.name).sort()).toEqual(
    team.map(([, name]) => name).sort()
  );
  const persisted = persistedWorkspace(body.folder.id);
  expect(persisted.data.parent_id || '').toBe('');
  expect(persisted.data.assistant_program_state).toBeUndefined();
  expect(persisted.data.assistant_project_link).toBeUndefined();
  expect(persisted.data.shared_data.blueprint_inputs.values).toMatchObject({
    tempo: '127',
    time_signature: '4 4'
  });
  expect(persisted.data.template_provenance.group_requirement).toMatchObject({
    policy: 'none',
    selected_composition: 'standalone',
    project_provider: {
      plugin_id: 'reaper-plugin',
      plugin_version: '0.8.0',
      blueprint_id: 'reaper-song',
      blueprint_version: 9,
      project_team_id: 'reaper-song-team'
    }
  });
  expect(persisted.data.template_provenance.group_requirement.home_provider).toBeUndefined();
  expect(
    persisted.data.template_provenance.group_requirement.standalone_roles.map(
      (role: { role_id: string }) => role.role_id
    )
  ).toEqual(['producer', 'engineer', 'songwriter']);
  expect(readFileSync(persistedProjectFile(persisted.file, persisted.data), 'utf8')).toContain(
    'TEMPO 127 4 4'
  );
  const plugins = pluginList(await json(await request.get('/api/plugins')));
  expect(plugins.map(plugin => plugin.name)).toEqual(['reaper-plugin']);
  await page.waitForTimeout(500);
  await evidence(page, '02-standalone-ready');
});
