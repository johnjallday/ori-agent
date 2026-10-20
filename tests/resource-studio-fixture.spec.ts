import { expect, test, type APIRequestContext, type Page } from '@playwright/test';
import { mkdirSync } from 'node:fs';

// Host journey for a resource workspace (REAPER Studio) against the TEST
// FIXTURE plugins in tests/fixtures/resource-studio/. Run only through
//
//   ./scripts/resource-studio-demo.sh test [--port PORT]
//
// which isolates HOME and ORI_DATA_DIR and installs both fixtures through the
// real plugin API. The fixture REAPER service answers from fixed data and reads
// no folder, so this proves Ori's side of the contract only: it is not
// candidate or real-reader evidence (checklist 5.1/5.2).
const ENABLED = process.env.ORI_RESOURCE_STUDIO_FIXTURES === '1';
const SHOTS = 'test-results/resource-studio-fixture';
const HEADERS = { 'X-Requested-With': 'XMLHttpRequest' };
const STUDIO_BLUEPRINT = 'plugin:reaper-plugin:reaper-studio';
let homeID = '';
let homeSlug = '';
let studioID = '';
let studioSlug = '';
let createPayload: Record<string, unknown> = {};

test.describe.configure({ mode: 'serial' });
test.skip(!ENABLED, 'requires scripts/resource-studio-demo.sh test');

async function json(response: Awaited<ReturnType<APIRequestContext['get']>>) {
  const text = await response.text();
  expect(response.ok(), text).toBeTruthy();
  const body = JSON.parse(text);
  return body?.data ?? body;
}

async function evidence(page: Page, name: string) {
  mkdirSync(SHOTS, { recursive: true });
  await page.screenshot({ path: `${SHOTS}/${name}.png`, fullPage: true });
}

async function studioSummary(request: APIRequestContext) {
  const body = await json(
    await request.get(`/api/workspaces/${homeID}/assistant-program/resource-workspaces`)
  );
  expect(body.resource_workspaces).toHaveLength(1);
  return body.resource_workspaces[0];
}

async function openCatalog(page: Page) {
  await page.goto(`/workspaces/${studioSlug}/catalog`);
  await expect(page.locator('#resourceCatalogContent')).toBeVisible();
}

async function openTab(page: Page, label: RegExp) {
  await page.locator('#resourceCatalogTabs').getByRole('tab', { name: label }).click();
}

test('a fresh Home offers REAPER Studio and reads nothing', async ({ page, request }) => {
  const templates = (await json(await request.get('/api/workspaces/group-templates')))
    .group_templates;
  const music = templates.filter(
    (entry: { provider?: { plugin_id: string } }) =>
      entry.provider?.plugin_id === 'music-project-management'
  );
  expect(music, JSON.stringify(templates)).toHaveLength(1);
  const body = { group_template_id: music[0].id, revision: music[0].revision, name: 'Music Home' };
  const review = (
    await json(
      await request.post('/api/workspaces/group-templates/review', { data: body, headers: HEADERS })
    )
  ).group_template_review;
  const created = await json(
    await request.post('/api/workspaces/group-templates/commit', {
      data: {
        ...body,
        group_review_token: review.review_token,
        idempotency_key: `resource-studio-${Date.now()}`
      },
      headers: HEADERS
    })
  );
  homeID = created.group_template.home_workspace_id;
  const folders = (await json(await request.get('/api/workspaces'))).folders;
  homeSlug = folders.find((folder: { id: string }) => folder.id === homeID).folder_slug;

  expect(await studioSummary(request)).toMatchObject({
    state: 'not_created',
    attachment: { provider_plugin_id: 'reaper-plugin', label: 'REAPER Studio' }
  });
  await page.goto(`/workspaces/${homeSlug}/assistant`);
  const card = page.locator('#homeResourceWorkspaces');
  await expect(card).toBeVisible();
  await expect(card).toContainText('Nothing is read until you inspect it.');
  const setUp = card.getByRole('link', { name: 'Set up REAPER Studio' });
  const href = new URL(String(await setUp.getAttribute('href')), 'http://ori.local');
  expect(Object.fromEntries(href.searchParams)).toEqual({
    create: '1',
    blueprint: STUDIO_BLUEPRINT,
    parent: homeID
  });
  await card.screenshot({ path: `${SHOTS}/01-home-card.png` });
});

test('Set up creates the one Studio under the Home', async ({ page, request }) => {
  await page.goto(`/workspaces/${homeSlug}/assistant`);
  await page
    .locator('#homeResourceWorkspaces')
    .getByRole('link', { name: 'Set up REAPER Studio' })
    .click();
  const creator = page.locator('#addFolderModal');
  await expect(creator).toBeVisible();
  await expect(creator).toContainText('REAPER Studio');
  await creator.locator('#wizardNextBtn').click();
  const destination = creator.locator('#workspaceGroupDestinationCard');
  await expect(destination).toContainText('Music Home');
  await expect(destination).toContainText('Existing verified group');
  await creator.locator('#wizardNextBtn').click();
  await expect(creator.locator('#wizardStep3')).toBeVisible();

  // The Home's required coordinator and the Studio's one role.
  await creator
    .getByRole('button', { name: 'Set up Music Portfolio Manager', exact: true })
    .click();
  await page.getByRole('button', { name: 'Create and fill group role' }).click();
  await expect(page.locator('#addFolderModal')).toBeVisible();
  const role = 'REAPER Studio Assistant';
  await creator.getByRole('button', { name: `Create an agent for ${role}` }).click();
  await expect(page.locator('#addAgentModal')).toBeVisible();
  await page.locator('[data-agent-create-field="name"]').fill(`${role} ${Date.now()}`);
  await page.locator('#createAgentBtn').click();
  await expect(page.locator('#addAgentModal')).toBeHidden();

  await creator.locator('#wizardNextBtn').click();
  await expect(creator.locator('#workspaceReviewSummary')).toContainText('REAPER Studio');
  await evidence(page, '02-create-review');
  const commit = page.waitForResponse(
    response =>
      new URL(response.url()).pathname === '/api/workspaces' &&
      response.request().method() === 'POST' &&
      Boolean(response.request().postDataJSON()?.group_review_token)
  );
  await creator.locator('#createFolderBtn').click();
  const committed = await commit;
  expect(committed.ok(), await committed.text()).toBeTruthy();
  createPayload = committed.request().postDataJSON();
  const created = await committed.json();
  studioID = created.folder.id;
  await page.waitForURL(url => /^\/workspaces\/[^/]+$/.test(url.pathname));

  const studio = await json(await request.get(`/api/workspaces/${studioID}`));
  expect(studio.parent_id).toBe(homeID);
  studioSlug = studio.folder_slug;
  expect(await studioSummary(request)).toMatchObject({
    state: 'available',
    workspace: { id: studioID }
  });
  // The Studio is not a song: the Home's projects and portfolio leave it out.
  const program = await json(await request.get(`/api/workspaces/${homeID}/assistant-program`));
  expect(JSON.stringify(program.projects || [])).not.toContain(studioID);
  expect(JSON.stringify(program.portfolio || [])).not.toContain(studioID);

  // A second request for the same Home is answered with the existing Studio.
  // The team plan names the existing Studio, and a stale Set up link opens
  // it instead of asking for a team for a second one.
  const plan = await json(
    await request.post('/api/workspaces/template-agent-plan', {
      data: { template_id: STUDIO_BLUEPRINT },
      headers: HEADERS
    })
  );
  expect(plan.group_requirement).toMatchObject({
    state: 'resource_workspace_exists',
    existing_workspace_id: studioID
  });
  await page.goto(`/?create=1&blueprint=${encodeURIComponent(STUDIO_BLUEPRINT)}&parent=${homeID}`);
  await page.waitForURL(url => url.pathname === `/workspaces/${studioSlug}`);
  // The create request itself is refused the same way.
  const { group_review_token: _token, idempotency_key: _key, ...again } = createPayload;
  const replay = await request.post('/api/workspaces', {
    data: { ...again, idempotency_key: `second-${Date.now()}` },
    headers: HEADERS
  });
  expect(replay.status(), await replay.text()).toBe(409);

  await page.goto(`/workspaces/${homeSlug}/assistant`);
  const card = page.locator('#homeResourceWorkspaces');
  await expect(card.getByRole('link', { name: 'Open REAPER Studio' })).toHaveAttribute(
    'href',
    `/workspaces/${studioSlug}/catalog`
  );
  await expect(card.getByRole('link', { name: 'Set up REAPER Studio' })).toHaveCount(0);
  await expect(card).toContainText('Templates: Not inspected');
  await card.screenshot({ path: `${SHOTS}/03-home-card-created.png` });
});

test('approved sources are inspected once and browsed by category', async ({ page, request }) => {
  await openCatalog(page);
  await expect(page.locator('#resourceCatalogEmpty')).toHaveText(
    'Not inspected yet. Nothing is read until you review its sources.'
  );
  const catalogURL = `/api/workspaces/${studioID}/resource-catalog`;
  expect((await json(await request.get(catalogURL))).revision).toBe(0);

  await page
    .locator('#resourceCatalogActions')
    .getByRole('button', { name: 'Inspect resources' })
    .click();
  const dialog = page.locator('#resourceCatalogReview');
  await expect(dialog).toBeVisible();
  await expect(dialog.locator('.resource-catalog-review-source')).toHaveCount(5);
  await expect(dialog).toContainText('There is no Ori script library on this computer yet.');
  await expect(dialog).toContainText('REAPER application · Needs the application open');
  await dialog.screenshot({ path: `${SHOTS}/04-review.png` });
  await page.locator('#resourceCatalogReviewCommit').click();
  await expect(dialog).toBeHidden();
  await expect(
    page.locator('#resourceCatalogActions').getByRole('button', { name: 'Refresh' })
  ).toBeVisible({
    timeout: 30_000
  });

  await openTab(page, /Templates/);
  const rows = page.locator('#resourceCatalogList .resource-catalog-row');
  await expect(rows).toHaveCount(4);
  // The two "Vocals" project templates are told apart by their identity.
  await expect(page.locator('#resourceCatalogList')).toContainText(
    'Project template · Project templates · Band/Vocals.RPP'
  );
  await expect(page.locator('#resourceCatalogList')).toContainText(
    'Project template · Project templates · Vocals.RPP'
  );
  await expect(page.locator('#resourceCatalogSources')).toContainText(
    '3 found · 1 skipped: 1 registered from outside the folder'
  );
  await evidence(page, '05-templates');

  await openTab(page, /Scripts/);
  await expect(rows).toHaveCount(3);
  await expect(page.locator('#resourceCatalogList')).toContainText('Script · REAPER Scripts · Lua');
  await openTab(page, /Audio Plugins/);
  await expect(rows).toHaveCount(0);
  await expect(page.locator('#resourceCatalogEmpty')).toHaveText(
    'Nothing could be read. REAPER plugin list: Needs the application open. Refresh once that changes.'
  );
  await expect(page.locator('#resourceCatalogEmpty')).not.toContainText(/install/i);
  await evidence(page, '06-audio-plugins');

  const summary = await studioSummary(request);
  const counts = Object.fromEntries(
    summary.catalog.categories.map((category: { id: string; present: number }) => [
      category.id,
      category.present
    ])
  );
  expect(counts).toEqual({ templates: 4, scripts: 3, audio_plugins: 0 });
});

test('the owner’s notes persist and are searchable', async ({ page }) => {
  await openCatalog(page);
  await openTab(page, /Templates/);
  await page
    .locator('#resourceCatalogList .resource-catalog-row', { hasText: 'Band/Vocals.RPP' })
    .click();
  const detail = page.locator('#resourceCatalogDetail');
  await expect(detail).toContainText('Identity: Band/Vocals.RPP');
  await expect(detail).toContainText(
    'Listed means seen during inspection. It does not mean licensed, loaded, working or tested.'
  );
  await expect(
    page.locator('#resourceCatalogList .resource-catalog-row[aria-current="true"]')
  ).toContainText('Band/Vocals.RPP');
  await detail.getByLabel('Favorite').check();
  await detail.getByLabel('Tags, separated by commas').fill('lead, band');
  await detail.getByLabel('Note').fill('Main band vocal setup.');
  await detail.getByRole('button', { name: 'Save notes' }).click();
  await expect(detail).toContainText('Saved.');

  await page.reload();
  await expect(page.locator('#resourceCatalogContent')).toBeVisible();
  await openTab(page, /Templates/);
  await page.locator('#resourceCatalogSearch').fill('band vocal');
  await page.locator('#resourceCatalogSearch').press('Enter');
  await expect(page.locator('#resourceCatalogList .resource-catalog-row')).toHaveCount(1);
  await expect(page.locator('#resourceCatalogList')).toContainText('★ Vocals');
  await evidence(page, '07-notes-search');
});

test('withdrawing one source keeps its history and drops it from the Home', async ({
  page,
  request
}) => {
  await openCatalog(page);
  const source = page.locator('#resourceCatalogSources li', { hasText: 'Track templates' });
  await source.getByRole('button', { name: 'Withdraw access' }).click();
  const dialog = page.locator('#resourceCatalogWithdraw');
  await expect(dialog).toContainText('Nothing in the folder changes.');
  await page.locator('#resourceCatalogWithdrawCommit').click();
  await expect(dialog).toBeHidden();
  await expect(source).toContainText('Access withdrawn');
  await openTab(page, /Templates/);
  await expect(
    page.locator('#resourceCatalogList .resource-catalog-row', { hasText: 'Lead vocal chain' })
  ).toContainText('Access withdrawn');
  await evidence(page, '08-withdrawn');
  const templates = (await studioSummary(request)).catalog.categories.find(
    (category: { id: string }) => category.id === 'templates'
  );
  expect(templates.present).toBe(3);
});

test('switching the provider off keeps the catalog and notes readable', async ({
  page,
  request
}) => {
  await json(await request.post('/api/plugins/reaper-plugin/disable', { headers: HEADERS }));
  try {
    expect(await studioSummary(request)).toMatchObject({ state: 'provider_disabled' });
    await openCatalog(page);
    await expect(page.locator('#resourceCatalogProvider')).toContainText('switched off');
    await expect(page.locator('#resourceCatalogActions button')).toHaveCount(0);
    await openTab(page, /Templates/);
    await expect(page.locator('#resourceCatalogList')).toContainText('★ Vocals');
    await evidence(page, '09-provider-off');
  } finally {
    await json(await request.post('/api/plugins/reaper-plugin/enable', { headers: HEADERS }));
  }
  await openCatalog(page);
  await expect(
    page.locator('#resourceCatalogActions').getByRole('button', { name: 'Refresh' })
  ).toBeVisible();
});

test('the catalog fits a narrow screen and follows the dark theme', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await openCatalog(page);
  const overflow = await page.evaluate(
    () => document.documentElement.scrollWidth - document.documentElement.clientWidth
  );
  expect(overflow).toBeLessThanOrEqual(0);
  const box = await page.locator('#resourceCatalogFavorites').boundingBox();
  expect(box?.width).toBeLessThan(24);
  await evidence(page, '10-narrow');
  await page.setViewportSize({ width: 1280, height: 900 });
  await page.locator('#darkModeToggle').click();
  await expect(page.locator('html')).toHaveAttribute('data-bs-theme', 'dark');
  await evidence(page, '11-dark');
  await page.locator('#darkModeToggle').click();
});
