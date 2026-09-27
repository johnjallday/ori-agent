import { expect, test, type APIRequestContext, type Page } from '@playwright/test';
import {
  existsSync,
  mkdirSync,
  readFileSync,
  readdirSync,
  realpathSync,
  writeFileSync
} from 'node:fs';
import { join, dirname } from 'node:path';
import { createHash } from 'node:crypto';

// A small exact-source acceptance for the current single-role REAPER blueprint.
// The older three-role contract has its own legacy fixture/spec; never substitute
// this candidate's roster for the installed blueprint's declaration.
const sandbox = process.env.ORI_MUSIC_REAPER_SANDBOX || '';
const order = process.env.ORI_MUSIC_REAPER_INSTALL_ORDER || '';
const musicPath = process.env.ORI_MUSIC_PLUGIN_PATH || '';
const reaperPath = process.env.ORI_REAPER_PLUGIN_PATH || '';
const firstPath = process.env.ORI_MUSIC_REAPER_FIRST_SNAPSHOT || '';
const finalPath = process.env.ORI_MUSIC_REAPER_FINAL_SNAPSHOT || '';
const restart = process.env.ORI_MUSIC_REAPER_RESTART_CHECK === '1';
const evidence = process.env.ORI_MUSIC_REAPER_EVIDENCE_DIR || '';
const receipt = join(sandbox, 'evidence', 'guidance-state.json');
const blueprintID = 'plugin:reaper-plugin:reaper-song';
test.skip(
  process.env.ORI_MUSIC_REAPER_ACCEPTANCE !== '1' || !sandbox || !musicPath || !reaperPath,
  'requires reaper-demo.sh test --suite guidance in disposable HOME'
);
test.describe.configure({ mode: 'serial' });
test.setTimeout(120_000);

async function json(response: Awaited<ReturnType<APIRequestContext['get']>>) {
  const body = await response.text();
  expect(response.ok(), body).toBeTruthy();
  return JSON.parse(body);
}
function stored(path: string) {
  return JSON.parse(readFileSync(path, 'utf8'));
}
function plugins(snapshot: any) {
  return snapshot.plugins.plugins || snapshot.plugins || [];
}
function workspaceFile(id: string) {
  const pending = [join(sandbox, 'workspace-staging')];
  while (pending.length) {
    const path = pending.pop()!;
    const file = join(path, 'workspace.json');
    if (existsSync(file) && stored(file).id === id) return { file, data: stored(file) };
    for (const entry of readdirSync(path, { withFileTypes: true }))
      if (entry.isDirectory()) pending.push(join(path, entry.name));
  }
  throw new Error(`missing sandbox workspace ${id}`);
}
async function screenshot(page: Page, label: string) {
  mkdirSync(evidence, { recursive: true });
  await page.screenshot({ path: join(evidence, `${order}-${label}.png`) });
}

// This test uses declarations from the *installed* blueprint, not a synthetic
// three-role expectation or a filename inferred from the song title.
test('paired candidates preserve order and create only the declared local project role', async ({
  page,
  request
}) => {
  test.skip(restart, 'the first run owns construction');
  const first = stored(firstPath);
  const final = stored(finalPath);
  const music = stored(join(musicPath, '.ori-plugin', 'plugin.json'));
  const reaper = stored(join(reaperPath, '.ori-plugin', 'plugin.json'));
  expect(['music-first', 'reaper-first']).toContain(order);
  expect(first.workspaces.folders).toEqual([]);
  expect(final.workspaces.folders).toEqual([]);
  expect(plugins(first)).toHaveLength(1);
  expect(plugins(first)[0].name).toBe(order === 'music-first' ? music.name : reaper.name);
  expect(
    plugins(final)
      .map((p: { name: string }) => p.name)
      .sort()
  ).toEqual(['music-project-management', 'reaper-plugin']);
  for (const candidate of [music, reaper]) {
    const installed = plugins(final).find((p: { name: string }) => p.name === candidate.name);
    expect(installed).toMatchObject({ version: candidate.version, enabled: true });
    expect(installed.source).toBe(candidate.name === music.name ? musicPath : reaperPath);
  }
  const template = final.project_templates.templates.find(
    (row: { id: string }) => row.id === blueprintID
  );
  expect(template).toMatchObject({
    readiness: { state: 'ready' },
    plugin_owner: { blueprint_version: 10, plugin_version: reaper.version }
  });
  const roles: Array<{ id: string; label: string }> = template.assistant_project.roles;
  expect(roles.map(role => ({ id: role.id, label: role.label }))).toEqual([
    { id: 'reaper-assistant', label: 'REAPER Assistant' }
  ]);
  expect(template.assistant_project.home).toMatchObject({
    provider_plugin_id: music.name,
    program_id: 'music-producer-assistant',
    min_home_version: 1,
    max_home_version: 1
  });
  await json(await request.post('/api/onboarding/skip'));
  await page.goto('/');
  await page.getByRole('button', { name: 'Tree', exact: true }).click();
  await page.locator('[data-tree-new-group]').click();
  const group = page.locator('#addFolderModal');
  const musicOption = group.locator('.workspace-group-template-option', {
    hasText: 'Music Production Home'
  });
  await musicOption.locator('input').check();
  await group.getByRole('button', { name: 'Continue →' }).click();
  await group.getByRole('button', { name: 'Continue →' }).click();
  await group.getByRole('button', { name: 'Review →' }).click();
  await screenshot(page, 'guidance-home-review');
  await group.locator('#createFolderBtn').click();
  await expect(group).toBeHidden();
  const homes = (await json(await request.get('/api/workspaces'))).folders.filter(
    (row: { kind: string }) => row.kind === 'group'
  );
  expect(homes).toHaveLength(1);
  const homeID = homes[0].id;
  const homeSummary = await json(await request.get(`/api/workspaces/${homeID}/assistant-program`));
  expect(homeSummary).toMatchObject({ is_station: true, home_provider_available: true });
  expect(homeSummary.roster.map((role: { role_id: string }) => role.role_id)).toEqual([
    'portfolio_manager'
  ]);
  // Initialize an empty Home library BEFORE normal single-project intake.
  // Later association must be an explicit review, not an initialization copy
  // or a best-effort creator observer silently mutating the catalog.
  const libraryBase = `/api/workspaces/${homeID}/assistant-program/library`;
  await page.goto(`/workspaces/${homes[0].folder_slug}/assistant`);
  const shelf = page.locator('#projectLibraryPanel');
  await expect(shelf).toBeVisible();
  await shelf.locator('#projectLibraryInitialize').click();
  await page
    .getByRole('dialog', { name: 'Start a Home project library?' })
    .getByRole('button', { name: 'Start library' })
    .click();
  await expect
    .poll(async () => (await json(await request.get(`${libraryBase}/roots`))).initialized)
    .toBe(true);
  expect((await json(await request.get(`${libraryBase}/projects`))).total).toBe(0);
  expect((await json(await request.get(`${libraryBase}/roots`))).total_roots).toBe(0);
  // Open the normal creator and select the installed REAPER template. The
  // declared single project role is staffed there, not inherited from Home.
  await page.goto('/');
  await page.waitForFunction(() => Boolean((window as any).sessionManager?.showAddWorkspaceModal));
  await page.evaluate(() => {
    (window as any).sessionManager.showAddWorkspaceModal({
      kind: 'workspace',
      entryPoint: 'paired_guidance'
    });
  });
  const creator = page.locator('#addFolderModal');
  await creator.locator(`#templatePicker [data-template-id="${blueprintID}"]`).click();
  await creator.locator('#wizardNextBtn').click();
  await creator.locator('#folderNameInput').fill('Paired Guidance Song');
  const open = creator.locator('#projectTemplateOpenAfterCreateToggle');
  if (await open.isChecked()) await open.uncheck();
  await creator.locator('#wizardNextBtn').click();
  const row = creator.locator('.ws-role-row', { hasText: roles[0].label });
  await expect(row).toHaveCount(1);
  await row.getByRole('button', { name: `Create an agent for ${roles[0].label}` }).click();
  await page.locator('[data-agent-create-field="name"]').fill('Song Assistant');
  await expect(page.locator('#toastContainer .toast.show')).toHaveCount(0, { timeout: 15_000 });
  await page.locator('#createAgentBtn').click();
  await expect(page.locator('#addAgentModal')).toBeHidden();
  await creator.locator('#wizardNextBtn').click();
  await screenshot(page, 'guidance-project-review');
  const review = page.waitForResponse(
    response =>
      new URL(response.url()).pathname === '/api/workspaces' &&
      response.request().method() === 'POST' &&
      response.request().postDataJSON()?.group_requirement_review === true
  );
  await creator.locator('#createFolderBtn').click();
  expect((await review).ok()).toBeTruthy();
  const commit = page.waitForResponse(
    response =>
      new URL(response.url()).pathname === '/api/workspaces' &&
      response.request().method() === 'POST' &&
      Boolean(response.request().postDataJSON()?.group_review_token)
  );
  await creator.locator('#createFolderBtn').click();
  const committed = await commit;
  expect(committed.ok(), await committed.text()).toBeTruthy();
  const created = await committed.json();
  const projectID = created.folder.id;
  const project = workspaceFile(projectID);
  expect(project.data.assistant_project_link).toMatchObject({
    station_workspace_id: homeID,
    home_provider: { plugin_version: music.version },
    project_provider: { plugin_version: reaper.version, blueprint_version: 10 }
  });
  expect(
    project.data.assistant_project_link.project_roles.map((role: { id: string }) => role.id)
  ).toEqual(roles.map(role => role.id));
  expect(project.data.agent_instances.map((agent: { role_id: string }) => agent.role_id)).toEqual(
    roles.map(role => role.id)
  );
  const file = join(
    dirname(project.file),
    project.data.project_path,
    project.data.shared_data.project_entry.relative_path
  );
  const sha256 = createHash('sha256').update(readFileSync(file)).digest('hex');
  const assistant = await json(await request.get(`/api/workspaces/${projectID}/assistant-program`));
  expect(assistant).toMatchObject({
    station_id: homeID,
    home_provider_available: true,
    project_provider_available: true
  });
  expect(assistant.roster.map((binding: { role_id: string }) => binding.role_id)).toEqual(
    roles.map(role => role.id)
  );
  expect((await json(await request.get(`${libraryBase}/projects`))).total).toBe(0);
  // The normal wizard navigates to the child to present its separate REAPER
  // setup journey, even with OS-open unchecked. Let navigation settle, then
  // leave without choosing a live-control mode or granting DAW access.
  await page.waitForURL(/\/workspaces\/paired-guidance-song(?:\/|\?|$)/);
  await page.waitForLoadState('domcontentloaded');
  await page.goto(`/workspaces/${homes[0].folder_slug}/assistant`);
  // A blueprint-scaffolded managed_workspace file is NOT an independently
  // picked, existing directory-reference source. Do not invent a discovery
  // grant or link-only catalog identity from this different child contract.
  // But the managed portfolio bridge must keep the Home readable while the
  // normal creator has appended the reciprocal child to Home membership.
  const homeAfterCreator = await json(
    await request.get(`/api/workspaces/${homeID}/assistant-program`)
  );
  expect(homeAfterCreator.projects.map((row: { id: string }) => row.id)).toEqual([projectID]);
  expect(homeAfterCreator.portfolio || []).toEqual([]);
  await expect(shelf).toBeVisible();
  await expect(shelf.locator('#projectLibraryCount')).toHaveText('0 of 0 projects');
  const pending = await json(await request.get(`${libraryBase}/linked-projects/pending`));
  expect(pending.total).toBe(0);
  await expect(page.locator('#projectLibraryPendingLinks')).toBeHidden();
  expect((await json(await request.get(`${libraryBase}/projects`))).total).toBe(0);
  expect((await json(await request.get(`${libraryBase}/roots`))).total_roots).toBe(0);
  expect(createHash('sha256').update(readFileSync(file)).digest('hex')).toBe(sha256);
  await screenshot(page, 'guidance-managed-child-home-remains-readable');
  writeFileSync(
    receipt,
    JSON.stringify({ homeID, projectID, sha256, file, roles: roles.map(role => role.id) }),
    { mode: 0o600 }
  );
});

test('a confirmed folder offer connects one existing file, then separately reviews its Home association', async ({
  page,
  request
}) => {
  test.skip(restart, 'the first run owns construction');
  const saved = stored(receipt);
  const documents = join(sandbox, 'Documents');
  mkdirSync(documents, { recursive: true, mode: 0o750 });
  const song = join(documents, 'Existing Song.rpp');
  writeFileSync(song, '<REAPER_PROJECT 0.1 "7.0" 1234\n>\n', { mode: 0o600 });
  const sourceHash = createHash('sha256').update(readFileSync(song)).digest('hex');
  const initial = (await json(await request.get('/api/personal-assistant'))).personal_assistant;
  await json(
    await request.post('/api/personal-assistant/hire', {
      data: {
        request_id: 'paired-existing-file-hire',
        if_version: initial.state_version,
        display_name: 'Atlas',
        mandate: 'Help review my projects.',
        focus_areas: ['plan_my_day']
      }
    })
  );
  const hired = (await json(await request.get('/api/personal-assistant'))).personal_assistant;
  await json(
    await request.post('/api/personal-assistant/hq', {
      data: {
        request_id: 'paired-existing-file-hq',
        if_version: hired.state_version,
        name: 'My HQ'
      }
    })
  );
  const scanned = await json(
    await request.post('/api/personal-assistant/folder-digest/scan', {
      data: { chip: 'documents' }
    })
  );
  expect(scanned.offer).toMatchObject({
    status: 'pending',
    capability: { setup_source: 'plugin' }
  });
  await page.goto('/?panel=today&folder=show');
  const offerCard = page.locator('#personalAssistantFolderOffer');
  await expect(offerCard).toContainText('Documents looks like a REAPER project');
  await offerCard.locator('[data-folder-action="setup"]').click();
  const quest = page.locator('#specialistSetupJourneyModal');
  await expect(quest).toBeVisible();
  await expect(quest).toContainText('Create New Workspace');
  await quest.getByRole('button', { name: 'Create New Workspace', exact: true }).click();
  const creator = page.locator('#addFolderModal');
  await expect(creator).toBeVisible();
  expect(await creator.locator('#workspaceJourneyReview').count()).toBe(1);
  await expect(creator.locator('#workspaceJourneyProjectChoice select').last()).toHaveValue(
    'existing_project'
  );
  await creator.locator('#folderNameInput').fill('Existing Documents Song');
  await creator.locator('#wizardNextBtn').click();
  const projectRole = creator.locator('.ws-role-row[data-role-id="reaper-assistant"]');
  await expect(projectRole).toContainText('REAPER Assistant');
  await projectRole.getByRole('button', { name: 'Create an agent for REAPER Assistant' }).click();
  await page.locator('[data-agent-create-field="name"]').fill('Existing Song Assistant');
  await page.locator('#createAgentBtn').click();
  await expect(page.locator('#addAgentModal')).toBeHidden();
  await creator.locator('#wizardNextBtn').click();
  await expect(creator.locator('#workspaceJourneyReview')).toContainText(
    'Project file: Existing Song.rpp'
  );
  await expect(creator.locator('#workspaceJourneyReview')).toContainText('File-only mode');
  const open = creator.locator('#projectTemplateOpenAfterCreateToggle');
  if (await open.isChecked()) await open.uncheck();
  await screenshot(page, 'guidance-existing-file-creator-review');
  const projectCommit = page.waitForResponse(
    response =>
      response.url().includes('/actions/connect_existing_project') &&
      response.request().method() === 'POST'
  );
  await creator.locator('#createFolderBtn').click();
  const committed = await projectCommit;
  expect(committed.ok(), await committed.text()).toBeTruthy();
  const journey = (await committed.json()).setup_journey;
  const existingID = journey.receipts.project_workspace_id;
  expect(existingID).toBeTruthy();
  await page.waitForURL(/\/workspaces\/existing-documents-song(?:\/|\?|$)/);
  const existing = workspaceFile(existingID).data;
  expect(existing.shared_data.project_entry).toMatchObject({
    kind: 'directory_reference',
    relative_path: 'Existing Song.rpp'
  });
  expect(existing.directory_references).toEqual(
    expect.arrayContaining([expect.objectContaining({ path: realpathSync(documents) })])
  );
  expect(existing.assistant_project_link.station_workspace_id).toBe(saved.homeID);
  // The wizard's agent choice is not itself a reviewed staffing commit for
  // this independently connected child. Its own staffing remains separate.
  expect(existing.agent_instances || []).toEqual([]);
  const unassociatedHome = await json(
    await request.get(`/api/workspaces/${saved.homeID}/assistant-program`)
  );
  expect(unassociatedHome.projects.map((row: { id: string }) => row.id).sort()).toEqual(
    [saved.projectID, existingID].sort()
  );
  expect(unassociatedHome.portfolio || []).toEqual([]);
  expect(unassociatedHome.roster.map((role: { role_id: string }) => role.role_id)).toEqual([
    'portfolio_manager'
  ]);
  expect(createHash('sha256').update(readFileSync(song)).digest('hex')).toBe(sourceHash);
  const base = `/api/workspaces/${saved.homeID}/assistant-program/library`;
  const rootsBefore = await json(await request.get(`${base}/roots`));
  expect(rootsBefore).toMatchObject({ initialized: true, total_roots: 0 });
  expect((await json(await request.get(`${base}/projects`))).total).toBe(0);
  const pendingBefore = await json(await request.get(`${base}/linked-projects/pending`));
  expect(pendingBefore.rows).toEqual([
    { workspace_id: existingID, name: 'Existing Documents Song' }
  ]);
  // The normal creator navigated straight to its child. Return through the
  // durable offer receipt, not a browser-guessed Home slug or a scan grant.
  const offerView = (await json(await request.get('/api/personal-assistant/folder-digest')))
    .folder_digest.offer;
  const homeRoute = `/workspaces/${(await json(await request.get('/api/workspaces'))).folders.find((row: { id: string }) => row.id === saved.homeID).folder_slug}/assistant#projectLibraryPanel`;
  expect(offerView).toMatchObject({
    id: scanned.offer.id,
    status: 'resolved',
    outcome: { workspace_id: existingID, home_route: homeRoute }
  });
  await page.goto('/?panel=today&folder=show');
  const followUp = page.locator('#personalAssistantFolderOffer');
  await expect(followUp).toContainText("Here's what I set up:");
  const homeLink = followUp.getByRole('link', { name: 'Review this link in the Home library' });
  await expect(homeLink).toHaveAttribute('href', homeRoute);
  await homeLink.scrollIntoViewIfNeeded();
  await screenshot(page, 'guidance-existing-file-home-navigation');
  await homeLink.click();
  await page.waitForURL(url => url.pathname + url.hash === homeRoute);
  const shelf = page.locator('#projectLibraryPanel');
  await expect(shelf.locator('#projectLibraryPendingLinks')).toBeVisible();
  await shelf
    .getByRole('button', { name: 'Review Existing Documents Song for this shelf' })
    .click();
  const association = page.getByRole('dialog', {
    name: 'Add Existing Documents Song to this shelf?'
  });
  await expect(association).toContainText('link-only metadata without a discovery folder grant');
  expect((await json(await request.get(`${base}/projects`))).total).toBe(0);
  await screenshot(page, 'guidance-existing-file-association-review');
  await association.getByRole('button', { name: 'Cancel' }).click();
  await expect(shelf.locator('#projectLibraryPendingLinks')).toBeVisible();
  expect((await json(await request.get(`${base}/projects`))).total).toBe(0);
  await shelf
    .getByRole('button', { name: 'Review Existing Documents Song for this shelf' })
    .click();
  await page
    .getByRole('dialog', { name: 'Add Existing Documents Song to this shelf?' })
    .getByRole('button', { name: 'Add linked project' })
    .click();
  await expect(shelf.locator('#projectLibraryCount')).toHaveText('1 of 1 projects');
  expect((await json(await request.get(`${base}/linked-projects/pending`))).total).toBe(0);
  const catalog = await json(await request.get(`${base}/projects`));
  expect(catalog.rows).toEqual([
    expect.objectContaining({ name: 'Existing Documents Song', connection: 'connected' })
  ]);
  expect((await json(await request.get(`${base}/roots`))).total_roots).toBe(0);
  expect(createHash('sha256').update(readFileSync(song)).digest('hex')).toBe(sourceHash);
  await screenshot(page, 'guidance-existing-file-associated-home');
  writeFileSync(
    receipt,
    JSON.stringify({ ...saved, existingID, song, sourceHash, offerHomeRoute: homeRoute }),
    {
      mode: 0o600
    }
  );
});

test('restart preserves exact candidate identities for both children and the reviewed association', async ({
  page,
  request
}) => {
  test.skip(!restart, 'run by reaper-demo.sh after its controlled restart');
  const saved = stored(receipt);
  const homes = (await json(await request.get('/api/workspaces'))).folders;
  expect(homes.filter((row: { kind: string }) => row.kind === 'group')).toHaveLength(1);
  expect(homes.filter((row: { parent_id: string }) => row.parent_id === saved.homeID)).toHaveLength(
    2
  );
  const home = await json(await request.get(`/api/workspaces/${saved.homeID}/assistant-program`));
  expect(home).toMatchObject({ home_provider_available: true, is_station: true });
  expect(home.projects.map((row: { id: string }) => row.id).sort()).toEqual(
    [saved.projectID, saved.existingID].sort()
  );
  expect(
    (home.portfolio || []).map((row: { project_workspace_id: string }) => row.project_workspace_id)
  ).toEqual([saved.existingID]);
  const roots = await json(
    await request.get(`/api/workspaces/${saved.homeID}/assistant-program/library/roots`)
  );
  expect(roots).toMatchObject({ initialized: true, total_roots: 0 });
  expect(
    (
      await json(
        await request.get(`/api/workspaces/${saved.homeID}/assistant-program/library/projects`)
      )
    ).total
  ).toBe(1);
  expect(
    (
      await json(
        await request.get(
          `/api/workspaces/${saved.homeID}/assistant-program/library/linked-projects/pending`
        )
      )
    ).total
  ).toBe(0);
  const project = await json(
    await request.get(`/api/workspaces/${saved.projectID}/assistant-program`)
  );
  expect(project).toMatchObject({
    station_id: saved.homeID,
    home_provider_available: true,
    project_provider_available: true
  });
  expect(project.roster.map((binding: { role_id: string }) => binding.role_id)).toEqual(
    saved.roles
  );
  expect(createHash('sha256').update(readFileSync(saved.file)).digest('hex')).toBe(saved.sha256);
  const imported = await json(
    await request.get(`/api/workspaces/${saved.existingID}/assistant-program`)
  );
  expect(imported).toMatchObject({ station_id: saved.homeID, project_provider_available: true });
  expect(workspaceFile(saved.existingID).data.agent_instances || []).toEqual([]);
  expect(createHash('sha256').update(readFileSync(saved.song)).digest('hex')).toBe(
    saved.sourceHash
  );
  const resumedOffer = (await json(await request.get('/api/personal-assistant/folder-digest')))
    .folder_digest.offer;
  expect(resumedOffer).toMatchObject({
    status: 'resolved',
    outcome: { workspace_id: saved.existingID, home_route: saved.offerHomeRoute }
  });
  await page.goto('/?panel=today&folder=show');
  const homeLink = page
    .locator('#personalAssistantFolderOffer')
    .getByRole('link', { name: 'Review this link in the Home library' });
  await expect(homeLink).toHaveAttribute('href', saved.offerHomeRoute);
  await homeLink.click();
  await page.waitForURL(url => url.pathname + url.hash === saved.offerHomeRoute);
  await expect(page.locator('#projectLibraryCount')).toHaveText('1 of 1 projects');
  expect(
    (
      await json(
        await request.get(
          `/api/workspaces/${saved.homeID}/assistant-program/library/linked-projects/pending`
        )
      )
    ).total
  ).toBe(0);
});
