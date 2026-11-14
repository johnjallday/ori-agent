import { expect, test } from '@playwright/test';
import { mkdir, readdir, readFile, writeFile } from 'node:fs/promises';
import { join, resolve } from 'node:path';

// Real sandbox HOME/Downloads, chooser, walk, report and built-in install.
// No model/provider or production policy bypass is used.
// ./scripts/e2e-fresh.sh --sandbox-env ORI_DOWNLOADS_SANDBOX tests/folder-agent-downloads-inbox.spec.ts
const sandbox = process.env.ORI_DOWNLOADS_SANDBOX || '';
const endpoint = '/api/personal-assistant/folder-agent';
const evidence = resolve('tasks/evidence-downloads-inbox-exception');

// Agent/CLI roster arrays come from maps; compare all values, not map order.
function stable(value: any): any {
  if (Array.isArray(value))
    return value.map(stable).sort((a, b) => JSON.stringify(a).localeCompare(JSON.stringify(b)));
  if (value && typeof value === 'object')
    return Object.fromEntries(Object.entries(value).map(([key, item]) => [key, stable(item)]));
  return value;
}

async function files(root: string): Promise<string[]> {
  const out: string[] = [];
  for (const item of await readdir(root, { withFileTypes: true })) {
    const path = join(root, item.name);
    if (item.isDirectory()) out.push(...(await files(path)));
    else out.push(path);
  }
  return out.sort();
}

test('Downloads keeps project evidence, offers only Janitor and explores a separately chosen folder', async ({
  page,
  request
}) => {
  test.skip(!sandbox, 'requires the isolated server HOME');
  test.setTimeout(120_000);
  const root = join(sandbox, 'Downloads');
  async function seed(rel: string, content = 'fixture') {
    const path = join(root, rel);
    await mkdir(join(path, '..'), { recursive: true, mode: 0o750 });
    await writeFile(path, content, { mode: 0o600 });
  }
  for (let n = 1; n <= 7; n++) await seed(`Software/Project-${n}/package.json`, '{}');
  for (let n = 1; n <= 5; n++) await seed(`Music/Song-${n}/Song.rpp`, '<REAPER_PROJECT\n>\n');
  for (let n = 1; n <= 3; n++) await seed(`Writing/Draft-${n}/outline.md`);
  const kinds = ['pdf', 'png', 'zip', 'dmg', 'csv', 'txt'];
  for (let n = 0; n < 204; n++) await seed(`loose-${n}.${kinds[n % kinds.length]}`);
  for (let n = 1; n <= 36; n++)
    await mkdir(join(root, `Empty-${n}`), { recursive: true, mode: 0o750 });
  const beforeFiles = await files(root);
  const beforeContents = await Promise.all(beforeFiles.map(path => readFile(path, 'utf8')));
  await mkdir(evidence, { recursive: true, mode: 0o750 });

  await request.post('/api/onboarding/skip');
  let assistant = (await (await request.get('/api/personal-assistant')).json()).personal_assistant;
  const hire = await request.post('/api/personal-assistant/hire', {
    data: {
      request_id: 'downloads-hire',
      if_version: assistant.state_version || 0,
      display_name: 'Atlas',
      mandate: 'Help me understand my folders.',
      focus_areas: ['plan_my_day']
    }
  });
  expect(hire.status(), await hire.text()).toBe(201);
  assistant = (await hire.json()).personal_assistant;
  const hq = await request.post('/api/personal-assistant/hq', {
    data: {
      request_id: 'downloads-hq',
      if_version: assistant.state_version,
      name: 'My HQ',
      timezone: 'UTC'
    }
  });
  expect(hq.status(), await hq.text()).toBe(201);

  const mutations: string[] = [];
  page.on('request', req => {
    if (req.method() === 'POST') mutations.push(req.url());
  });
  await page.goto('/?panel=today&folder=show');
  const entry = page.locator('#personalAssistantFolderPathEntry');
  await expect(entry).toBeVisible();
  await entry.locator('input').fill(root);
  await entry.getByRole('button').click();
  const host = page.locator('#personalAssistantFolderAgent');
  await expect(host).toContainText('Hire an agent for Downloads');
  await host.locator('[data-folder-agent-action="start"]').click();
  await expect(host.getByRole('button', { name: 'Show on map' })).toBeVisible();
  await host.getByRole('button', { name: 'Show on map' }).click();
  const report = page
    .locator('.folder-agent-card--report')
    .filter({ hasText: 'Downloads stays an inbox' });
  await expect(report).toBeVisible({ timeout: 30_000 });
  await expect(report.locator('[data-step]')).toHaveCount(1);
  await expect(report).toContainText('Add File Janitor to Downloads');
  await expect(report.locator('.folder-agent-card__facts')).toContainText('7 software projects');
  await expect(report.locator('.folder-agent-card__facts')).toContainText('5 REAPER songs');
  await expect(report.locator('.folder-agent-card__facts')).toContainText('3 manuscripts');
  await page.screenshot({ path: join(evidence, 'fresh-downloads.png'), fullPage: true });

  const list = (await (await request.get(endpoint)).json()).folder_agents;
  expect(list).toHaveLength(1);
  const id = list[0].workspace_id;
  const get = async () => (await (await request.get(`${endpoint}/${id}`)).json()).folder_agent;
  const view = await get();
  expect(view.plan.steps.map((s: { kind: string }) => s.kind)).toEqual(['file_curator']);
  expect(view.plan.selected).toEqual(['file-curator']);
  expect(view.model_sentence).toContain('could not look itself');
  expect(view.facts_line).toContain('204 loose files');
  expect(view.facts_line).toContain('36 empty folders');
  // Details uses the same production card outside the map balloon, making
  // the complete plan and its consent note visible together.
  await page.goto(`${view.route}?view=details`);
  const closeAssistant = page.getByRole('button', {
    name: 'Close personal assistant',
    exact: true
  });
  if (await closeAssistant.isVisible()) await closeAssistant.click();
  await page.getByRole('button', { name: 'Open in chat', exact: true }).click();
  await expect(report).toBeVisible();
  await page.screenshot({ path: join(evidence, 'fresh-downloads.png'), fullPage: true });
  const workspacesBefore = await (await request.get('/api/workspaces')).json();
  const agentNamesBefore = await (await request.get('/api/agents')).json();
  for (const selected of [
    ['group'],
    ['home:music_project_management'],
    ['reaper-tools'],
    ['file-curator', 'integration:ori_reaper']
  ]) {
    const refused = await request.post(`${endpoint}/${id}/setup`, {
      data: { plan_digest: view.plan.digest, selected }
    });
    expect(refused.status(), await refused.text()).toBe(409);
    expect((await refused.json()).plan_changed).toBe(true);
  }
  expect(await (await request.get('/api/workspaces')).json()).toEqual(workspacesBefore);
  expect(stable(await (await request.get('/api/agents')).json())).toEqual(stable(agentNamesBefore));

  // Keyboard-native link uses the existing chooser. Escape creates nothing.
  const explore = report.getByRole('link', { name: 'Explore a project folder…' });
  await explore.focus();
  await expect(explore).toBeFocused();
  await page.keyboard.press('Enter');
  await expect(entry).toBeVisible();
  await page.keyboard.press('Escape');
  await expect
    .poll(() => page.evaluate(() => document.activeElement?.id))
    .toMatch(/^(personalAssistant(AttachMenu|Input|Launcher)|cockpitPrimaryActionBtn)$/);
  expect((await (await request.get(endpoint)).json()).folder_agents).toHaveLength(1);
  // Explicit selection is a second onboarding run, not reanchoring Downloads.
  await page.goto('/?panel=today&folder=show');
  await entry.locator('input').fill(join(root, 'Music'));
  await entry.getByRole('button').click();
  await expect(host).toContainText('Hire an agent for Music');
  expect((await (await request.get(endpoint)).json()).folder_agents).toHaveLength(1);
  await host.locator('[data-folder-agent-action="start"]').click();
  await expect
    .poll(async () => {
      const views = (await (await request.get(endpoint)).json()).folder_agents;
      return views.find((v: { folder_name: string }) => v.folder_name === 'Music')?.state;
    })
    .toBe('reported');
  const musicRoute = (await (await request.get(endpoint)).json()).folder_agents.find(
    (v: { folder_name: string }) => v.folder_name === 'Music'
  ).route;
  await page.goto(musicRoute);
  const openMusicChat = page.getByRole('button', { name: 'Open in chat', exact: true });
  const musicPremise = page.getByRole('button', { name: 'Manage them as songs', exact: true });
  await expect(openMusicChat.or(musicPremise).first()).toBeVisible();
  if (await openMusicChat.isVisible()) await openMusicChat.click({ timeout: 10_000 });
  await musicPremise.click({ timeout: 10_000 });
  await expect(page.locator('.folder-agent-card--report:visible')).toContainText(
    'Music Production Home'
  );
  expect((await get()).plan.selected).toEqual(['file-curator']);
  const all = (await (await request.get(endpoint)).json()).folder_agents;
  expect(all).toHaveLength(2);
  const music = all.find((v: { folder_name: string }) => v.folder_name === 'Music');
  expect(music.plan.steps.some((s: { kind: string }) => s.kind === 'home')).toBe(true);
  expect(music.plan.steps.some((s: { kind: string }) => s.kind === 'group')).toBe(true);
  expect(music.policy_note).toBeUndefined();
  await expect(page.locator('.folder-agent-card--report:visible')).toContainText(
    'Music Production Home'
  );
  await page.screenshot({ path: join(evidence, 'separate-music-folder.png'), fullPage: true });

  // Seed an actual version-1 pending sidecar, as a pre-change server could
  // have left it. GET must project current eligibility without rewriting it.
  const records = (await files(join(sandbox, 'workspace-staging'))).filter(path =>
    path.endsWith('/.ori/folder-agent-v1.json')
  );
  let recordPath = '';
  for (const path of records)
    if (JSON.parse(await readFile(path, 'utf8')).workspace_id === id) recordPath = path;
  expect(recordPath).not.toBe('');
  const legacy = JSON.parse(await readFile(recordPath, 'utf8'));
  expect(legacy.facts.members).toHaveLength(15);
  expect(legacy.facts.root_loose_files).toBe(204);
  expect(legacy.facts.empty_folders).toHaveLength(36);
  expect(legacy.facts.unopened || []).toHaveLength(0); // this modest fixture fits the budget
  legacy.candidates = music.candidates;
  legacy.stage = 'home';
  legacy.sections = {
    plan: 'Create [music-production-home], connect [reaper-tools] and make [group].',
    plan_steps: ['music-production-home', 'reaper-tools', 'group'],
    suggested_goal: 'Release all songs',
    next: 'Work on the songs',
    source: 'first_look_model',
    model: 'fixture-old-model'
  };
  const legacyBytes = JSON.stringify(legacy);
  await writeFile(recordPath, legacyBytes, { mode: 0o600 });
  const reopened = await get();
  expect(reopened.plan.steps.map((s: { kind: string }) => s.kind)).toEqual(['file_curator']);
  expect(reopened.plan.words).toBeUndefined();
  expect(reopened.suggested_goal).toBeUndefined();
  expect(await readFile(recordPath, 'utf8')).toBe(legacyBytes);
  const stale = await request.post(`${endpoint}/${id}/setup`, {
    data: {
      plan_digest: 'pre-policy-home-plan',
      selected: ['music-production-home', 'reaper-tools']
    }
  });
  expect(stale.status()).toBe(409);
  expect((await stale.json()).folder_agent.plan.selected).toEqual(['file-curator']);
  await page.goto(`${view.route}?view=details`);
  if (await closeAssistant.isVisible()) await closeAssistant.click();
  await page.getByRole('button', { name: 'Open in chat', exact: true }).click();
  await expect(report).toBeVisible();
  await expect(report).not.toContainText('Release all songs');
  await page.screenshot({ path: join(evidence, 'legacy-refreshed.png'), fullPage: true });
  await page.addScriptTag({ url: 'https://cdn.jsdelivr.net/npm/axe-core@4.10.3/axe.min.js' });
  // Changed-surface layout/theme/reflow and semantic checks, not a claim of
  // native dialog or screen-reader certification.
  for (const [theme, width, zoom] of [
    ['light', 1280, 1],
    ['dark', 1280, 1],
    ['dark', 390, 1],
    ['light', 1280, 2]
  ] as const) {
    await page.setViewportSize({ width, height: 900 });
    await page.evaluate(
      ({ theme, zoom }) => {
        document.documentElement.setAttribute('data-bs-theme', theme);
        document.documentElement.setAttribute('data-theme', theme);
        document.documentElement.style.zoom = String(zoom);
      },
      { theme, zoom }
    );
    await expect(report.getByRole('link', { name: 'Explore a project folder…' })).toBeVisible();
    expect(await report.evaluate(el => el.scrollWidth <= el.clientWidth + 1)).toBe(true);
    expect(await report.getByRole('checkbox').count()).toBe(1);
    await report.getByRole('checkbox').focus();
    await expect(report.getByRole('checkbox')).toBeFocused();
    await page.keyboard.press('Space');
    await expect(report.getByRole('button', { name: 'Set it up', exact: true })).toBeDisabled();
    await expect(report.getByRole('checkbox')).toBeFocused();
    await page.keyboard.press('Space');
    await expect(report.getByRole('button', { name: 'Set it up', exact: true })).toBeEnabled();
    await expect(report.getByRole('checkbox')).toBeFocused();
    const violations = await page.evaluate(async () => {
      const result = await (window as any).axe.run(
        { include: [['.folder-agent-card__explore']] },
        {
          runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa'] }
        }
      );
      return result.violations.map((v: { id: string }) => v.id);
    });
    expect(violations).toEqual([]);
    await page.screenshot({
      path: join(evidence, `report-${theme}-${width}-${zoom}x.png`),
      fullPage: true
    });
  }
  await page.evaluate(() => {
    document.documentElement.style.zoom = '';
  });
  await page.setViewportSize({ width: 1280, height: 900 });

  // Confirm only the allowed Downloads setup through the same API the card uses.
  const structure = (data: any) =>
    stable(
      data.folders.map((w: any) => ({
        id: w.id,
        shape: w.shape,
        parent_id: w.parent_id,
        mission: w.mission
      }))
    );
  const structureBefore = structure(await (await request.get('/api/workspaces')).json());
  const install = await request.post(`${endpoint}/${id}/setup`, {
    data: { plan_digest: view.plan.digest, selected: ['file-curator'] }
  });
  expect(install.ok(), await install.text()).toBeTruthy();
  await expect.poll(async () => (await get()).setup?.status).toBe('done');
  const done = await get();
  expect(done.setup.receipt?.home).toBeUndefined();
  expect(done.setup.receipt?.group).not.toBe(true);
  expect(done.goal_offer).toBeUndefined();
  expect(done.next_ticket).toBeUndefined();
  const janitor = await request.get(`/api/workspaces/${id}/file-janitor`);
  expect(janitor.ok(), await janitor.text()).toBeTruthy();
  const state = await janitor.json();
  expect(JSON.stringify(state)).not.toContain('last_completed_at');
  const protection = await (
    await request.get(`/api/workspaces/${id}/file-janitor/protection`)
  ).json();
  expect(protection.state).toBe('unavailable'); // install is not a folder grant
  expect(structure(await (await request.get('/api/workspaces')).json())).toEqual(structureBefore);
  await page.goto(`${view.route}?view=details`);
  if (await closeAssistant.isVisible()) await closeAssistant.click();
  await page.locator('#fileJanitorCardOpen').click();
  const permissions = page.getByRole('region', { name: 'Review File Janitor permissions' });
  await expect(permissions).toContainText('Use your attached folder');
  await expect(permissions).toContainText('No file contents are opened');
  await expect(permissions).toContainText(
    'Every move into Filed/ or action to Trash needs your separate approval'
  );
  await expect(permissions.getByRole('checkbox')).not.toBeChecked();
  await page.screenshot({ path: join(evidence, 'janitor-needs-grant.png'), fullPage: true });
  await page.getByRole('button', { name: 'Close File Janitor', exact: true }).click();
  await page.locator('#fileJanitorCardOpen').click();
  await expect(permissions.getByRole('checkbox')).not.toBeChecked();
  expect(
    (await (await request.get(`/api/workspaces/${id}/file-janitor/protection`)).json()).state
  ).toBe('unavailable');
  expect(
    mutations.filter(url => /file-janitor\/(scan|preview|apply|setup|prepare-review)/.test(url))
  ).toEqual([]);
  // This separate explicit confirmation discloses Filed/ creation and one
  // metadata-only scan. It still grants no moves, Trash or monitoring.
  await permissions.getByRole('button', { name: 'Use attached Downloads', exact: true }).click();
  await expect(permissions).toBeHidden();
  const configured = (await (await request.get(`/api/workspaces/${id}/file-janitor`)).json())
    .status;
  expect(configured.settings.setup_decision.mode).toBe('manual');
  expect(configured.settings.paused).toBe(true);
  expect(configured.settings.content_mode).toBe('metadata_only');
  const granted = await (await request.get(`/api/workspaces/${id}/file-janitor/protection`)).json();
  expect(granted.state).toBe('not_covered');
  expect(granted.last_completed_at).toBeUndefined();
  expect(mutations.filter(url => /file-janitor\/(apply|preview)/.test(url))).toEqual([]);
  expect(mutations.filter(url => /file-janitor\/prepare-review/.test(url))).toHaveLength(1);
  expect(await files(root)).toEqual(beforeFiles);
  expect(await Promise.all(beforeFiles.map(path => readFile(path, 'utf8')))).toEqual(
    beforeContents
  );
});
