import { expect, test, type APIRequestContext, type Page } from '@playwright/test';
import { createHash } from 'node:crypto';
import {
  cpSync,
  lstatSync,
  mkdirSync,
  readFileSync,
  readdirSync,
  readlinkSync,
  rmSync,
  writeFileSync
} from 'node:fs';
import { basename, join, relative } from 'node:path';

// REAPER Studio against the EXACT committed REAPER and Music candidates, on a
// disposable Ori host. Run only through
//
//   ./scripts/reaper-demo.sh test --suite studio --reaper-source DIR \
//     --music-source DIR --install-order music-first|reaper-first
//   ./scripts/reaper-demo.sh test --suite studio --reaper-source DIR \
//     --music-source OLDER_DIR --music-upgrade-source DIR
//
// Real: the candidates' declarations, the REAPER candidate's own readers and
// service, the reviewed setup, catalog, Home summary and the song lookup tools
// called through chat. The readers inspect only the folders the harness seeds
// in the sandbox HOME (tests/fixtures/resource-studio/reaper-folders), and its
// reaper.ini names a Web Remote port nothing listens on, so the plugin list
// reads as REAPER closed. This is candidate evidence, not real-REAPER evidence
// (that is scripts/reaper-studio-reader-check.py) and not live-model judgment:
// the lookups are direct tool calls with no model.
const ENABLED =
  process.env.ORI_MUSIC_REAPER_ACCEPTANCE === '1' && process.env.ORI_STUDIO_CANDIDATE === '1';
const sandbox = process.env.ORI_MUSIC_REAPER_SANDBOX || '';
const reaperPath = process.env.ORI_REAPER_PLUGIN_PATH || '';
const musicPath = process.env.ORI_MUSIC_PLUGIN_PATH || '';
const upgradePath = process.env.ORI_STUDIO_UPGRADE_PATH || '';
const restartCheck = process.env.ORI_STUDIO_RESTART_CHECK === '1';
// Opt-in, never in CI: "provider/model" asks ONE real model a song question in
// chat and in a task (for example codex/gpt-6-astra with CODEX_HOME passed to
// the harness). Everything else in this file stays model-free.
const LIVE_MODEL = process.env.ORI_STUDIO_LIVE_MODEL || '';
const ORDER = process.env.ORI_MUSIC_REAPER_INSTALL_ORDER || 'music-first';
// An older REAPER commit (no Studio blueprint) is the compatibility run.
const reaperHasStudio =
  ENABLED &&
  Boolean(reaperPath) &&
  (manifestOf(reaperPath).blueprints || []).some(
    (blueprint: { id: string }) => blueprint.id === 'reaper-studio'
  );
const RUN = upgradePath ? 'upgrade' : reaperHasStudio ? ORDER : 'older-reaper';
// ORI_STUDIO_EVIDENCE_DIR keeps screenshots outside the sandbox, which the
// harness removes on exit.
const evidenceRoot =
  process.env.ORI_STUDIO_EVIDENCE_DIR ||
  process.env.ORI_MUSIC_REAPER_EVIDENCE_DIR ||
  'test-results/reaper-studio';
const SHOTS = join(evidenceRoot, `studio-${RUN}`);
const HEADERS = { 'X-Requested-With': 'XMLHttpRequest' };
const STUDIO_BLUEPRINT = 'plugin:reaper-plugin:reaper-studio';
const SONG_BLUEPRINT = 'plugin:reaper-plugin:reaper-song';
const RESOURCE = join(sandbox, 'Library', 'Application Support', 'REAPER');
const LIBRARY = join(sandbox, 'Ori Scripts', 'reaper');
const STATE = join(SHOTS, 'state.json');

type Saved = {
  homeID: string;
  homeSlug: string;
  studioID: string;
  studioSlug: string;
  songID: string;
  songAgent: string;
  revision: number;
  sources: Record<string, unknown>;
};
let saved: Saved = {
  homeID: '',
  homeSlug: '',
  studioID: '',
  studioSlug: '',
  songID: '',
  songAgent: '',
  revision: 0,
  sources: {}
};

test.describe.configure({ mode: 'serial' });
test.skip(!ENABLED, 'requires ./scripts/reaper-demo.sh test --suite studio');

async function json(response: Awaited<ReturnType<APIRequestContext['get']>>) {
  const text = await response.text();
  expect(response.ok(), `${response.url()} → ${response.status()} ${text}`).toBeTruthy();
  const body = text ? JSON.parse(text) : {};
  return body?.data ?? body;
}

function manifestOf(root: string) {
  return JSON.parse(readFileSync(join(root, '.ori-plugin', 'plugin.json'), 'utf8'));
}

async function shot(page: Page, name: string) {
  mkdirSync(SHOTS, { recursive: true });
  await page.screenshot({ path: join(SHOTS, `${name}.png`), fullPage: true });
}

function save() {
  mkdirSync(SHOTS, { recursive: true });
  writeFileSync(STATE, JSON.stringify(saved, null, 2));
}

// Membership, link targets and content of the seeded REAPER folders. Nothing
// follows a link; REAPER is not running here, so reaper.ini is included too.
function sourceSnapshot(): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  for (const root of [RESOURCE, LIBRARY]) {
    const pending = [root];
    while (pending.length) {
      const current = pending.pop() as string;
      for (const entry of readdirSync(current).sort()) {
        const path = join(current, entry);
        const key = relative(sandbox, path);
        const info = lstatSync(path);
        if (info.isSymbolicLink()) out[key] = { link: readlinkSync(path) };
        else if (info.isDirectory()) {
          out[key] = 'dir';
          pending.push(path);
        } else out[key] = createHash('sha256').update(readFileSync(path)).digest('hex');
      }
    }
  }
  return out;
}

async function studioSummary(request: APIRequestContext) {
  const body = await json(
    await request.get(`/api/workspaces/${saved.homeID}/assistant-program/resource-workspaces`)
  );
  expect(body.resource_workspaces).toHaveLength(1);
  return body.resource_workspaces[0];
}

function counts(summary: any) {
  return Object.fromEntries(
    summary.catalog.categories.map((category: { id: string; present: number }) => [
      category.id,
      category.present
    ])
  );
}

async function openCatalog(page: Page) {
  await page.goto(`/workspaces/${saved.studioSlug}/catalog`);
  await expect(page.locator('#resourceCatalogContent')).toBeVisible();
}

async function openTab(page: Page, label: RegExp) {
  await page.locator('#resourceCatalogTabs').getByRole('tab', { name: label }).click();
}

// A linked song's agent calls a lookup tool directly through chat: the real
// tool registration and chain check, with no model in between.
async function lookup(
  request: APIRequestContext,
  workspaceID: string,
  tool: string,
  input: object
) {
  const reply = await json(
    await request.post('/api/chat', {
      data: {
        agent_name: saved.songAgent,
        route_context: { surface: 'workspace_detail', workspace_id: workspaceID },
        question: `/tool ${tool} ${JSON.stringify(input)}`
      },
      headers: HEADERS
    })
  );
  const text = String(reply.response || '');
  const start = text.indexOf('{"catalog_revision"');
  return {
    success: reply.success === true,
    text,
    answer: start >= 0 ? JSON.parse(text.slice(start, text.lastIndexOf('}') + 1)) : null
  };
}

async function createHome(request: APIRequestContext, name: string) {
  const templates = (await json(await request.get('/api/workspaces/group-templates')))
    .group_templates;
  const music = templates.filter(
    (entry: { provider?: { plugin_id: string } }) =>
      entry.provider?.plugin_id === 'music-project-management'
  );
  expect(music, JSON.stringify(templates)).toHaveLength(1);
  const body = { group_template_id: music[0].id, revision: music[0].revision, name };
  const review = (
    await json(
      await request.post('/api/workspaces/group-templates/review', { data: body, headers: HEADERS })
    )
  ).group_template_review;
  const created = await json(
    await request.post('/api/workspaces/group-templates/commit', {
      data: { ...body, group_review_token: review.review_token, idempotency_key: `home-${RUN}` },
      headers: HEADERS
    })
  );
  saved.homeID = created.group_template.home_workspace_id;
  const folders = (await json(await request.get('/api/workspaces'))).folders;
  saved.homeSlug = folders.find((folder: { id: string }) => folder.id === saved.homeID).folder_slug;
}

async function createAgentFor(page: Page, role: string, name: string) {
  const creator = page.locator('#addFolderModal');
  await creator.getByRole('button', { name: `Create an agent for ${role}` }).click();
  await expect(page.locator('#addAgentModal')).toBeVisible();
  await page.locator('[data-agent-create-field="name"]').fill(name);
  await expect(page.locator('#toastContainer .toast.show')).toHaveCount(0, { timeout: 15_000 });
  await page.locator('#createAgentBtn').click();
  await expect(page.locator('#addAgentModal')).toBeHidden();
  await expect(creator).toBeVisible();
}

// Create Workspace for one song under the Home. The Home's coordinator is
// staffed on the first child, whichever kind it is.
async function createSong(page: Page, request: APIRequestContext, name: string) {
  await page.goto('/');
  await page.waitForFunction(() => Boolean((window as any).sessionManager?.showAddWorkspaceModal));
  await page.evaluate(() =>
    (window as any).sessionManager.showAddWorkspaceModal({
      kind: 'workspace',
      entryPoint: 'studio_candidate_song'
    })
  );
  const creator = page.locator('#addFolderModal');
  await expect(creator).toBeVisible();
  await creator.locator(`#templatePicker [data-template-id="${SONG_BLUEPRINT}"]`).click();
  await creator.locator('#wizardNextBtn').click();
  await expect(creator.locator('#wizardStep2')).toBeVisible();
  await creator.locator('#folderNameInput').fill(name);
  const tempo = creator.locator('#workspaceBlueprintInput-tempo');
  if (await tempo.count()) await tempo.fill('120');
  const openProject = creator.locator('#projectTemplateOpenAfterCreateToggle');
  if ((await openProject.count()) && (await openProject.isChecked())) await openProject.uncheck();
  await expect(creator.locator('#workspaceGroupDestinationCard')).toContainText(
    'Existing verified group'
  );
  await creator.locator('#wizardNextBtn').click();
  await expect(creator.locator('#wizardStep3')).toBeVisible();
  const coordinator = creator.getByRole('button', {
    name: 'Set up Music Portfolio Manager',
    exact: true
  });
  if (await coordinator.count()) {
    await coordinator.click();
    await page.getByRole('button', { name: 'Create and fill group role' }).click();
    await expect(creator).toBeVisible();
  }
  saved.songAgent = `${name} REAPER Assistant`;
  await createAgentFor(page, 'REAPER Assistant', saved.songAgent);
  await creator.locator('#wizardNextBtn').click();
  await expect(creator.locator('#workspaceReviewSummary')).toContainText('Reaper Song');
  const commit = page.waitForResponse(
    response =>
      new URL(response.url()).pathname === '/api/workspaces' &&
      response.request().method() === 'POST' &&
      Boolean(response.request().postDataJSON()?.group_review_token)
  );
  await creator.locator('#createFolderBtn').click();
  const committed = await commit;
  expect(committed.ok(), await committed.text()).toBeTruthy();
  saved.songID = (await committed.json()).folder.id;
  await page.waitForURL(url => /^\/workspaces\/[^/]+$/.test(url.pathname));
  const song = await json(await request.get(`/api/workspaces/${saved.songID}`));
  expect(song.parent_id).toBe(saved.homeID);
}

async function setUpStudio(page: Page, request: APIRequestContext) {
  await page.goto(`/workspaces/${saved.homeSlug}/assistant`);
  await page
    .locator('#homeResourceWorkspaces')
    .getByRole('link', { name: 'Set up REAPER Studio' })
    .click();
  const creator = page.locator('#addFolderModal');
  await expect(creator).toBeVisible();
  await expect(creator).toContainText('REAPER Studio');
  await creator.locator('#wizardNextBtn').click();
  await expect(creator.locator('#workspaceGroupDestinationCard')).toContainText(
    'Existing verified group'
  );
  await creator.locator('#wizardNextBtn').click();
  await expect(creator.locator('#wizardStep3')).toBeVisible();
  const coordinator = creator.getByRole('button', {
    name: 'Set up Music Portfolio Manager',
    exact: true
  });
  if (await coordinator.count()) {
    await coordinator.click();
    await page.getByRole('button', { name: 'Create and fill group role' }).click();
    await expect(creator).toBeVisible();
  }
  await createAgentFor(page, 'REAPER Studio Assistant', `Studio Assistant ${RUN}`);
  await creator.locator('#wizardNextBtn').click();
  await expect(creator.locator('#workspaceReviewSummary')).toContainText('REAPER Studio');
  await shot(page, '03-studio-review');
  const commit = page.waitForResponse(
    response =>
      new URL(response.url()).pathname === '/api/workspaces' &&
      response.request().method() === 'POST' &&
      Boolean(response.request().postDataJSON()?.group_review_token)
  );
  await creator.locator('#createFolderBtn').click();
  const committed = await commit;
  expect(committed.ok(), await committed.text()).toBeTruthy();
  const payload = committed.request().postDataJSON();
  saved.studioID = (await committed.json()).folder.id;
  await page.waitForURL(url => /^\/workspaces\/[^/]+$/.test(url.pathname));
  const studio = await json(await request.get(`/api/workspaces/${saved.studioID}`));
  expect(studio.parent_id).toBe(saved.homeID);
  saved.studioSlug = studio.folder_slug;
  save();
  return payload;
}

function listFiles(root: string): string[] {
  const files: string[] = [];
  const walk = [root];
  while (walk.length) {
    const dir = walk.pop() as string;
    for (const child of readdirSync(dir, { withFileTypes: true })) {
      const childPath = join(dir, child.name);
      if (child.isDirectory()) walk.push(childPath);
      else files.push(relative(root, childPath));
    }
  }
  return files;
}

// The Studio's folder and its linked project folder hold the candidate's
// skeleton and Ori's own files only.
function studioFiles(): string[] {
  const skip = new Set(['Library', 'Ori Scripts', 'outside', 'plugin-source', 'plugin-upgrade']);
  const pending = [sandbox];
  while (pending.length) {
    const current = pending.pop() as string;
    for (const entry of readdirSync(current, { withFileTypes: true })) {
      const path = join(current, entry.name);
      if (entry.isFile() && entry.name === 'workspace.json') {
        const data = JSON.parse(readFileSync(path, 'utf8'));
        if (data.id !== saved.studioID) continue;
        const files = listFiles(current);
        if (data.project_path) {
          const project = join(current, data.project_path);
          files.push(...listFiles(project).map(file => join(data.project_path, file)));
        }
        return [...new Set(files)].sort();
      }
      if (entry.isDirectory() && !skip.has(entry.name) && !entry.name.startsWith('.git'))
        pending.push(path);
    }
  }
  throw new Error(`the Studio ${saved.studioID} has no workspace.json in the sandbox`);
}

test('an older REAPER plugin leaves the Studio to an update', async ({ page, request }) => {
  test.skip(reaperHasStudio || Boolean(upgradePath) || restartCheck, 'older REAPER commit only');
  expect(basename(sandbox)).toMatch(/^ori-reaper-demo\./);
  await json(await request.post('/api/onboarding/skip', { headers: HEADERS }));
  const plugins = (await json(await request.get('/api/plugins'))).plugins || [];
  expect(plugins.find((row: any) => row.name === 'reaper-plugin')).toMatchObject({
    enabled: true,
    source: reaperPath
  });
  saved.sources = sourceSnapshot();
  await createHome(request, `Music Home ${RUN}`);
  expect(await studioSummary(request)).toMatchObject({ state: 'provider_incompatible' });
  await page.goto(`/workspaces/${saved.homeSlug}/assistant`);
  const card = page.locator('#homeResourceWorkspaces');
  await expect(card).toContainText('Update the plugin that provides REAPER Studio to set it up.');
  await expect(card.getByRole('link', { name: 'Set up REAPER Studio' })).toHaveCount(0);
  await card.screenshot({ path: join(SHOTS, '01-older-reaper-card.png') });
  expect(sourceSnapshot()).toEqual(saved.sources);
});

test.describe('fresh Home', () => {
  test.skip(!reaperHasStudio || Boolean(upgradePath) || restartCheck, 'fresh-Home run only');

  test('the exact candidates are installed and enabled', async ({ request }) => {
    expect(basename(sandbox)).toMatch(/^ori-reaper-demo\./);
    // A fresh profile opens onboarding over every page; this suite is not about it.
    await json(await request.post('/api/onboarding/skip', { headers: HEADERS }));
    if (LIVE_MODEL) {
      // Agents staffed later default to the system model.
      const split = LIVE_MODEL.indexOf('/');
      await json(
        await request.post('/api/settings/system-model', {
          data: { provider: LIVE_MODEL.slice(0, split), model: LIVE_MODEL.slice(split + 1) },
          headers: HEADERS
        })
      );
    }
    const plugins = (await json(await request.get('/api/plugins'))).plugins || [];
    const music = plugins.find((row: any) => row.name === 'music-project-management');
    const reaper = plugins.find((row: any) => row.name === 'reaper-plugin');
    expect(music).toMatchObject({
      enabled: true,
      source: musicPath,
      version: manifestOf(musicPath).version
    });
    expect(reaper).toMatchObject({
      enabled: true,
      source: reaperPath,
      version: manifestOf(reaperPath).version
    });
    saved.sources = sourceSnapshot();
    save();
  });

  test('a fresh Home offers REAPER Studio and reads nothing', async ({ page, request }) => {
    await createHome(request, `Music Home ${RUN}`);
    expect(await studioSummary(request)).toMatchObject({
      state: 'not_created',
      attachment: { provider_plugin_id: 'reaper-plugin', label: 'REAPER Studio' }
    });
    await page.goto(`/workspaces/${saved.homeSlug}/assistant`);
    const card = page.locator('#homeResourceWorkspaces');
    await expect(card).toContainText('Nothing is read until you inspect it.');
    await expect(card.getByRole('link', { name: 'Set up REAPER Studio' })).toBeVisible();
    await card.screenshot({ path: join(SHOTS, '01-home-card.png') });
    expect(sourceSnapshot()).toEqual(saved.sources);
  });

  test('a song and then the one Studio are created under the Home', async ({ page, request }) => {
    test.setTimeout(180_000);
    await createSong(page, request, `Song ${RUN}`);
    const payload = await setUpStudio(page, request);
    expect(await studioSummary(request)).toMatchObject({
      state: 'available',
      workspace: { id: saved.studioID }
    });
    // Only the candidate's skeleton and Ori's own files: no project, no song tasks.
    const files = studioFiles();
    expect(files.filter(file => /\.rpp$/i.test(file))).toEqual([]);
    expect(files).toContain('workspace.json');
    // The Studio is not a song; the song still is.
    const program = await json(
      await request.get(`/api/workspaces/${saved.homeID}/assistant-program`)
    );
    expect(JSON.stringify(program.projects || [])).not.toContain(saved.studioID);
    expect(JSON.stringify(program.portfolio || [])).not.toContain(saved.studioID);
    expect(JSON.stringify(program.projects || [])).toContain(saved.songID);
    // A second Studio is refused, and a stale Set up link opens this one.
    const { group_review_token: _token, idempotency_key: _key, ...again } = payload;
    const replay = await request.post('/api/workspaces', {
      data: { ...again, idempotency_key: `second-${RUN}` },
      headers: HEADERS
    });
    expect(replay.status(), await replay.text()).toBe(409);
    await page.goto(
      `/?create=1&blueprint=${encodeURIComponent(STUDIO_BLUEPRINT)}&parent=${saved.homeID}`
    );
    await page.waitForURL(url => url.pathname === `/workspaces/${saved.studioSlug}`);
    // Before any approval the song is not offered a lookup, and nothing was read.
    const early = await lookup(request, saved.songID, 'resource_catalog_search', {
      query: 'vocal'
    });
    expect(early.success).toBe(false);
    expect(early.text).toContain("Tool 'resource_catalog_search' not found");
    expect(
      (await json(await request.get(`/api/workspaces/${saved.studioID}/resource-catalog`))).revision
    ).toBe(0);
    expect(sourceSnapshot()).toEqual(saved.sources);
  });

  test('the candidate readers inspect the approved folders', async ({ page, request }) => {
    test.setTimeout(120_000);
    await openCatalog(page);
    await page
      .locator('#resourceCatalogActions')
      .getByRole('button', { name: 'Inspect resources' })
      .click();
    const dialog = page.locator('#resourceCatalogReview');
    await expect(dialog.locator('.resource-catalog-review-source')).toHaveCount(5);
    await expect(dialog).toContainText('~/Library/Application Support/REAPER/ProjectTemplates');
    await expect(dialog).toContainText('~/Ori Scripts/reaper');
    await expect(dialog).toContainText('Needs the application open');
    await dialog.screenshot({ path: join(SHOTS, '04-review.png') });
    await page.locator('#resourceCatalogReviewCommit').click();
    await expect(dialog).toBeHidden();
    await expect(
      page.locator('#resourceCatalogActions').getByRole('button', { name: 'Refresh' })
    ).toBeVisible({ timeout: 60_000 });

    const sources = page.locator('#resourceCatalogSources');
    await expect(sources.locator('li', { hasText: 'Project templates' })).toContainText(
      '3 found · 1 skipped: 1 hidden'
    );
    await expect(sources.locator('li', { hasText: 'Track templates' })).toContainText('2 found');
    await expect(sources.locator('li', { hasText: 'REAPER Scripts' })).toContainText(
      '3 found · 2 skipped: 1 registered from outside the folder, 1 symlinked'
    );
    await expect(sources.locator('li', { hasText: 'Ori script library' })).toContainText('1 found');

    await openTab(page, /Templates/);
    const list = page.locator('#resourceCatalogList');
    await expect(list.locator('.resource-catalog-row')).toHaveCount(5);
    await expect(list).toContainText('Project template · Project templates · Band/Vocals.RPP');
    await expect(list).toContainText('Project template · Project templates · Vocals.RPP');
    await shot(page, '05-templates');

    await openTab(page, /Scripts/);
    await expect(list.locator('.resource-catalog-row')).toHaveCount(4);
    await list.locator('.resource-catalog-row', { hasText: 'Normalize items' }).click();
    const detail = page.locator('#resourceCatalogDetail');
    await expect(detail).toContainText('Normalize the selected items');
    await expect(detail).toContainText('Lua');
    await expect(detail).toContainText(
      'Listed means seen during inspection. It does not mean licensed, loaded, working or tested.'
    );
    await shot(page, '06-script-detail');
    await list.locator('.resource-catalog-row', { hasText: 'Tidy markers' }).click();
    await expect(detail).toContainText('Rename markers to their section names');

    await openTab(page, /Audio Plugins/);
    await expect(list.locator('.resource-catalog-row')).toHaveCount(0);
    await expect(page.locator('#resourceCatalogEmpty')).toHaveText(
      'Nothing could be read. REAPER plugin list: Needs the application open. Refresh once that changes.'
    );
    await shot(page, '07-audio-closed');

    const summary = await studioSummary(request);
    expect(counts(summary)).toEqual({ templates: 5, scripts: 4, audio_plugins: 0 });
    saved.revision = summary.catalog.revision;
    save();
    expect(sourceSnapshot()).toEqual(saved.sources);
  });

  test('notes persist and the song finds them, read-only and cited', async ({ page, request }) => {
    await openCatalog(page);
    await openTab(page, /Templates/);
    await page
      .locator('#resourceCatalogList .resource-catalog-row', { hasText: 'Band/Vocals.RPP' })
      .click();
    const detail = page.locator('#resourceCatalogDetail');
    await detail.getByLabel('Favorite').check();
    await detail.getByLabel('Tags, separated by commas').fill('lead, band');
    await detail.getByLabel('Note').fill('Main band vocal setup.');
    await detail.getByRole('button', { name: 'Save notes' }).click();
    await expect(detail).toContainText('Saved.');
    await page.reload();
    await openTab(page, /Templates/);
    await page.locator('#resourceCatalogSearch').fill('band vocal');
    await page.locator('#resourceCatalogSearch').press('Enter');
    await expect(page.locator('#resourceCatalogList .resource-catalog-row')).toHaveCount(1);
    await shot(page, '08-notes');

    const found = await lookup(request, saved.songID, 'resource_catalog_search', {
      query: 'band vocal'
    });
    expect(found.success, found.text).toBe(true);
    expect(found.answer.results).toHaveLength(1);
    const result = found.answer.results[0];
    expect(result).toMatchObject({
      name: 'Vocals',
      favorite: true,
      tags: ['lead', 'band'],
      studio_url: expect.stringMatching(
        new RegExp(`^/workspaces/${saved.studioSlug}/catalog\\?entry=`)
      )
    });
    // No path, native identity or file content ever reaches the song.
    expect(found.text).not.toContain(sandbox);
    expect(found.text).not.toContain('Band/Vocals.RPP');
    expect(found.text).not.toContain('REAPER_PROJECT');
    const detailed = await lookup(request, saved.songID, 'resource_catalog_detail', {
      entry_id: result.entry_id
    });
    expect(detailed.success, detailed.text).toBe(true);
    expect(detailed.text).toContain('Main band vocal setup.');
    expect(detailed.text).not.toContain('Band vocals');
    // The Home itself is not a linked song: it gets no lookup.
    const fromHome = await lookup(request, saved.homeID, 'resource_catalog_search', {
      query: 'vocal'
    });
    expect(fromHome.success).toBe(false);
    expect(await studioSummary(request)).toMatchObject({
      catalog: { revision: saved.revision }
    });
  });

  test('a live model answers a song question from the catalog, in chat and in a task', async ({
    request
  }) => {
    test.skip(!LIVE_MODEL, 'opt-in: ORI_STUDIO_LIVE_MODEL=provider/model');
    test.setTimeout(900_000);
    const citation = `/workspaces/${saved.studioSlug}/catalog?entry=`;
    const question =
      'Which vocal project templates do I have in my REAPER Studio, and which one is my favorite? Give me its Studio link.';
    let started = Date.now();
    const reply = await json(
      await request.post('/api/chat', {
        data: {
          agent_name: saved.songAgent,
          route_context: { surface: 'workspace_detail', workspace_id: saved.songID },
          question
        },
        headers: HEADERS,
        timeout: 300_000
      })
    );
    const chat = { question, ms: Date.now() - started, response: String(reply.response || '') };

    started = Date.now();
    const description =
      'Look up which track templates my REAPER Studio has and list them with their Studio links. Change nothing.';
    const created = await json(
      await request.post('/api/orchestration/tasks', {
        data: { workspace_id: saved.songID, description, priority: 2 },
        headers: HEADERS
      })
    );
    const taskID = created.task.id;
    await json(
      await request.post('/api/orchestration/tasks/execute', {
        data: { task_id: taskID },
        headers: HEADERS
      })
    );
    let task: any = {};
    await expect
      .poll(
        async () => {
          const body = await json(
            await request.get(`/api/orchestration/tasks?workspace_id=${saved.songID}`)
          );
          task = (Array.isArray(body) ? body : body.tasks).find((t: any) => t.id === taskID) || {};
          return task.status;
        },
        { timeout: 600_000, intervals: [3_000] }
      )
      .toMatch(/^(completed|failed|cancelled|timeout)$/);
    writeFileSync(
      join(SHOTS, 'live-model.json'),
      JSON.stringify(
        {
          model: LIVE_MODEL,
          chat,
          task: {
            description,
            ms: Date.now() - started,
            status: task.status,
            result: task.result,
            error: task.error
          }
        },
        null,
        2
      )
    );
    // Cited, bounded and read-only: the judgment itself is read from the file.
    expect(chat.response).toContain(citation);
    expect(chat.response).not.toContain(sandbox);
    expect(task.status, task.error).toBe('completed');
    expect(String(task.result)).toContain(citation);
    expect(String(task.result)).not.toContain(sandbox);
  });

  test('withdrawal and a switched-off provider stop the lookups, not the owner view', async ({
    page,
    request
  }) => {
    await openCatalog(page);
    const source = page.locator('#resourceCatalogSources li', { hasText: 'Track templates' });
    await source.getByRole('button', { name: 'Withdraw access' }).click();
    await page.locator('#resourceCatalogWithdrawCommit').click();
    await expect(source).toContainText('Access withdrawn');
    expect(counts(await studioSummary(request)).templates).toBe(3);
    const withdrawn = await lookup(request, saved.songID, 'resource_catalog_search', {
      query: 'Kit bus'
    });
    expect(withdrawn.answer?.results || []).toHaveLength(0);
    await shot(page, '09-withdrawn');

    await json(await request.post('/api/plugins/reaper-plugin/disable', { headers: HEADERS }));
    try {
      expect(await studioSummary(request)).toMatchObject({ state: 'provider_disabled' });
      const off = await lookup(request, saved.songID, 'resource_catalog_search', {
        query: 'vocal'
      });
      expect(off.success).toBe(false);
      await openCatalog(page);
      await expect(page.locator('#resourceCatalogProvider')).toContainText('switched off');
      await openTab(page, /Templates/);
      await expect(page.locator('#resourceCatalogList')).toContainText('★ Vocals');
      await shot(page, '10-provider-off');
    } finally {
      await json(await request.post('/api/plugins/reaper-plugin/enable', { headers: HEADERS }));
    }
    const back = await lookup(request, saved.songID, 'resource_catalog_search', { query: 'vocal' });
    expect(back.success, back.text).toBe(true);
    save();
  });

  test('narrow and dark layouts, and the folders were never changed', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await openCatalog(page);
    const overflow = await page.evaluate(
      () => document.documentElement.scrollWidth - document.documentElement.clientWidth
    );
    expect(overflow).toBeLessThanOrEqual(0);
    await shot(page, '11-narrow');
    await page.setViewportSize({ width: 1280, height: 900 });
    await page.locator('#darkModeToggle').click();
    await expect(page.locator('html')).toHaveAttribute('data-bs-theme', 'dark');
    await shot(page, '12-dark');
    await page.locator('#darkModeToggle').click();
    expect(sourceSnapshot()).toEqual(saved.sources);
  });
});

test('restart keeps the catalog, notes and lookups; detaching then stops them', async ({
  page,
  request
}) => {
  test.skip(
    !restartCheck || !reaperHasStudio,
    'run by the harness after a controlled restart of a Studio run'
  );
  saved = JSON.parse(readFileSync(STATE, 'utf8'));
  const summary = await studioSummary(request);
  expect(summary.catalog.revision).toBe(saved.revision);
  expect(counts(summary)).toEqual({ templates: 3, scripts: 4, audio_plugins: 0 });
  await openCatalog(page);
  await openTab(page, /Templates/);
  await expect(page.locator('#resourceCatalogList')).toContainText('★ Vocals');
  const found = await lookup(request, saved.songID, 'resource_catalog_search', {
    query: 'band vocal'
  });
  expect(found.success, found.text).toBe(true);
  expect(found.answer.results[0]).toMatchObject({ favorite: true });
  await shot(page, '13-after-restart');

  // The Home's reviewed disconnect detaches the Studio: songs lose the lookup,
  // the Studio keeps its catalog and notes, and Set up would make a new one.
  const program = await json(
    await request.get(`/api/workspaces/${saved.homeID}/assistant-program`)
  );
  const review = await json(
    await request.post(`/api/workspaces/${saved.homeID}/assistant-program/disconnect/review`, {
      data: { project_workspace_id: saved.studioID, state_revision: program.state_revision },
      headers: HEADERS
    })
  );
  await json(
    await request.post(`/api/workspaces/${saved.homeID}/assistant-program/disconnect/commit`, {
      data: { token: review.token, idempotency_key: `detach-${RUN}` },
      headers: HEADERS
    })
  );
  const detached = await lookup(request, saved.songID, 'resource_catalog_search', {
    query: 'vocal'
  });
  expect(detached.success).toBe(false);
  expect(await studioSummary(request)).toMatchObject({ state: 'not_created' });
  await openCatalog(page);
  await expect(page.locator('#resourceCatalogProvider')).toContainText('no longer attached');
  await openTab(page, /Templates/);
  await expect(page.locator('#resourceCatalogList')).toContainText('★ Vocals');
  await shot(page, '14-detached');
  expect(sourceSnapshot()).toEqual(saved.sources);
});

test('an existing Home with a song upgrades through review, then offers the Studio', async ({
  page,
  request
}) => {
  test.skip(!upgradePath || restartCheck, 'run with --music-upgrade-source');
  test.setTimeout(240_000);
  expect(basename(sandbox)).toMatch(/^ori-reaper-demo\./);
  expect(upgradePath.startsWith(sandbox)).toBe(true);
  const before = manifestOf(musicPath).version;
  const after = manifestOf(upgradePath).version;
  expect(after).not.toBe(before);
  await json(await request.post('/api/onboarding/skip', { headers: HEADERS }));
  saved.sources = sourceSnapshot();

  // The older Home has no resource attachment: no card, nothing to set up.
  await createHome(request, 'Existing Home');
  await createSong(page, request, 'Existing Song');
  const song = await json(await request.get(`/api/workspaces/${saved.songID}`));
  const summaryBefore = await json(
    await request.get(`/api/workspaces/${saved.homeID}/assistant-program/resource-workspaces`)
  );
  expect(summaryBefore.resource_workspaces || []).toHaveLength(0);
  await page.goto(`/workspaces/${saved.homeSlug}/assistant`);
  await expect(page.locator('#homeResourceWorkspaces')).toBeHidden();

  // A developer replaces the installed export with the candidate in place.
  for (const entry of readdirSync(musicPath)) rmSync(join(musicPath, entry), { recursive: true });
  cpSync(upgradePath, musicPath, { recursive: true });
  await page.goto('/plugins');
  const update = page.locator(
    '[data-plugin-action="update"][data-plugin-name="music-project-management"]'
  );
  await expect(update).toBeVisible({ timeout: 30_000 });
  await update.click();
  const toast = page.locator('.toast', { hasText: 'A Home uses this plugin' });
  await expect(toast).toBeVisible();
  await toast.getByRole('button', { name: 'Open Home' }).click();
  const dialog = page.locator('dialog.assistant-program-action-dialog');
  await expect(dialog.getByRole('heading', { level: 2 })).toHaveText(
    `Upgrade music-project-management to ${after}?`,
    { timeout: 30_000 }
  );
  await expect(dialog).toContainText('REAPER Studio');
  await dialog.screenshot({ path: join(SHOTS, '01-upgrade-review.png') });
  await dialog.getByRole('button', { name: 'Upgrade Home' }).click();
  await expect(dialog).toBeHidden({ timeout: 60_000 });
  await expect(page.locator('#assistantProgramUpgrade')).toHaveAttribute('data-state', 'succeeded');

  // The upgrade offers the Studio and creates or reads nothing; the song is untouched.
  expect(await studioSummary(request)).toMatchObject({ state: 'not_created' });
  const songAfter = await json(await request.get(`/api/workspaces/${saved.songID}`));
  expect(songAfter.parent_id).toBe(saved.homeID);
  expect(songAfter.agent_instances.map((agent: any) => agent.name)).toEqual(
    song.agent_instances.map((agent: any) => agent.name)
  );
  await page.goto(`/workspaces/${saved.homeSlug}/assistant`);
  const card = page.locator('#homeResourceWorkspaces');
  await expect(card.getByRole('link', { name: 'Set up REAPER Studio' })).toBeVisible();
  await card.screenshot({ path: join(SHOTS, '02-upgraded-home-card.png') });

  await setUpStudio(page, request);
  expect(await studioSummary(request)).toMatchObject({ state: 'available' });
  const program = await json(
    await request.get(`/api/workspaces/${saved.homeID}/assistant-program`)
  );
  expect(JSON.stringify(program.projects || [])).toContain(saved.songID);
  expect(JSON.stringify(program.projects || [])).not.toContain(saved.studioID);
  expect(
    (await json(await request.get(`/api/workspaces/${saved.studioID}/resource-catalog`))).revision
  ).toBe(0);
  expect(sourceSnapshot()).toEqual(saved.sources);
});
