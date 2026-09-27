import { expect, test, type APIRequestContext, type Page } from '@playwright/test';
import { existsSync, mkdirSync, readFileSync, readdirSync, writeFileSync } from 'node:fs';
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
  writeFileSync(
    receipt,
    JSON.stringify({ homeID, projectID, sha256, file, roles: roles.map(role => role.id) }),
    { mode: 0o600 }
  );
});

test('restart preserves exact candidate identities and the one declared project role', async ({
  request
}) => {
  test.skip(!restart, 'run by reaper-demo.sh after its controlled restart');
  const saved = stored(receipt);
  const homes = (await json(await request.get('/api/workspaces'))).folders;
  expect(homes.filter((row: { kind: string }) => row.kind === 'group')).toHaveLength(1);
  expect(homes.filter((row: { parent_id: string }) => row.parent_id === saved.homeID)).toHaveLength(
    1
  );
  const home = await json(await request.get(`/api/workspaces/${saved.homeID}/assistant-program`));
  expect(home).toMatchObject({ home_provider_available: true, is_station: true });
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
});
