import { expect, test, type APIRequestContext, type Page } from '@playwright/test';
import { mkdirSync } from 'node:fs';

// Acceptance for the Home profile card ("Your studio") against a staged Music
// Project Management candidate that declares `home_profile`. Run only through
//
//   ./scripts/music-home-demo.sh test --suite home-profile --source <candidate>
//
// which isolates HOME and ORI_DATA_DIR and supplies fixture applications: a
// REAPER.app in ORI_APPLICATIONS_DIR and a Logic Pro.app under the sandbox
// HOME, so the two-DAW path does not depend on what this Mac has installed.
//
// Not covered here, because they need companion releases that this suite's
// sandbox does not stage: listing templates (a REAPER plugin with
// `profile.read`), the one-card setup line and last screen, and the 0.1.1 to
// 0.2.0 Home upgrade.
const ENABLED =
  process.env.ORI_MUSIC_HOME_ACCEPTANCE === '1' && process.env.ORI_HOME_PROFILE_FIXTURES === '1';
const SHOTS = process.env.ORI_MUSIC_HOME_EVIDENCE_DIR || 'test-results/home-profile';
const HEADERS = { 'X-Requested-With': 'XMLHttpRequest' };
let homeID = '';
let homeSlug = '';

test.describe.configure({ mode: 'serial' });
test.skip(!ENABLED, 'requires scripts/music-home-demo.sh test --suite home-profile');

async function json(response: Awaited<ReturnType<APIRequestContext['get']>>) {
  const text = await response.text();
  expect(response.ok(), text).toBeTruthy();
  return JSON.parse(text);
}

const profileURL = (suffix = '') => `/api/workspaces/${homeID}/assistant-program/profile${suffix}`;

async function profile(request: APIRequestContext) {
  return json(await request.get(profileURL()));
}

async function openCard(page: Page) {
  await page.goto(`/workspaces/${homeSlug}/assistant`);
  const panel = page.locator('#homeProfilePanel');
  await expect(panel).toBeVisible();
  return panel;
}

async function evidence(page: Page, name: string) {
  mkdirSync(SHOTS, { recursive: true });
  await page.locator('#homeProfilePanel').screenshot({ path: `${SHOTS}/${name}.png` });
}

test('a new Home shows the declared card and detects nothing on load', async ({
  page,
  request
}) => {
  await request.post('/api/onboarding/skip', { headers: HEADERS });
  const templates = (await json(await request.get('/api/workspaces/group-templates')))
    .group_templates;
  const music = templates.filter(
    (entry: { provider?: { plugin_id: string } }) =>
      entry.provider?.plugin_id === 'music-project-management'
  );
  expect(music, JSON.stringify(templates)).toHaveLength(1);
  const body = {
    group_template_id: music[0].id,
    revision: music[0].revision,
    name: 'Music Production Home'
  };
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
        idempotency_key: `home-profile-${Date.now()}`
      },
      headers: HEADERS
    })
  );
  homeID = created.group_template.home_workspace_id;
  const folders = (await json(await request.get('/api/workspaces'))).folders;
  homeSlug = folders.find((folder: { id: string }) => folder.id === homeID).folder_slug;

  const card = await profile(request);
  expect(card, 'the staged candidate must declare home_profile').toMatchObject({
    available: true,
    read_only: false,
    revision: 0,
    profile: null
  });
  expect(card.fields.map((field: { kind: string }) => field.kind)).toEqual(
    expect.arrayContaining(['apps', 'main_app'])
  );

  const panel = await openCard(page);
  await expect(panel.locator('.assistant-program-panel-index')).toHaveText('05 / PROFILE');
  await expect(page.locator('#homeProfileTitle')).toHaveText(card.title);
  await expect(page.locator('#homeProfileIntro')).toHaveText(card.intro);
  await expect(page.locator('#homeProfileDetect')).toHaveText('Detect');
  await expect(panel).toContainText('Nothing has been looked for yet.');
  // Opening the page looked for nothing.
  expect((await profile(request)).profile).toBeNull();
  await evidence(page, '01-empty-card');
});

test('Detect lists both DAWs as hints and one pick fills the main DAW', async ({
  page,
  request
}) => {
  const panel = await openCard(page);
  const status = page.locator('#homeProfileStatus');
  await page.locator('#homeProfileDetect').click();
  await expect(status).toHaveText('Found REAPER and Logic Pro.');
  await expect(page.locator('#homeProfileDetect')).toHaveText('Detect again');
  const reaper = panel.locator('li[data-app-id="reaper"]');
  const logic = panel.locator('li[data-app-id="logic-pro"]');
  await expect(reaper.locator('.home-profile-badge')).toHaveText('Detected');
  await expect(logic.locator('.home-profile-badge')).toHaveText('Detected');
  await expect(panel).toContainText('Not chosen. REAPER and Logic Pro were found. Pick one.');
  let card = await profile(request);
  expect(card.profile.main_app).toBeUndefined();
  await evidence(page, '02-two-daws-detected');

  await page.locator('#homeProfileMainApp').selectOption({ label: 'REAPER' });
  await expect(status).toHaveText('Saved.');
  await expect(panel.locator('.home-profile-main .home-profile-badge')).toHaveText('Chosen by you');
  await reaper.getByRole('button', { name: 'Confirm' }).click();
  await expect(status).toHaveText('Confirmed.');
  await expect(reaper.locator('.home-profile-badge')).toHaveText('Confirmed');
  await expect(reaper.getByRole('button', { name: 'Confirm' })).toHaveCount(0);
  card = await profile(request);
  expect(card.profile.main_app).toMatchObject({ id: 'reaper', source: 'owner' });
  expect(card.profile.apps[0].confirmed_at).toBeTruthy();
  await evidence(page, '03-picked-and-confirmed');

  await logic.getByRole('button', { name: 'Not mine' }).click();
  await expect(status).toHaveText('Hidden. Agents no longer see it.');
  await expect(logic).toHaveCount(0);
  await expect(panel).toContainText('Not yours: Logic Pro');
  await panel.getByRole('button', { name: 'Show again' }).click();
  await expect(status).toHaveText('Shown again as a hint.');
  await expect(logic.locator('.home-profile-badge')).toHaveText('Detected');

  // Detecting again keeps what the owner said.
  await page.locator('#homeProfileDetect').click();
  await expect(status).toHaveText('Found REAPER and Logic Pro.');
  await expect(reaper.locator('.home-profile-badge')).toHaveText('Confirmed');
  expect((await profile(request)).profile.main_app).toMatchObject({
    id: 'reaper',
    source: 'owner'
  });
});

test('new-song defaults are bounded, saved and shown as the owner’s', async ({ page, request }) => {
  const panel = await openCard(page);
  const status = page.locator('#homeProfileStatus');
  const row = panel.locator('section[data-kind="defaults"]');
  await page.locator('#homeProfileTempo').fill('300');
  await row.getByRole('button', { name: 'Save' }).click();
  await expect(status).toHaveText('Tempo is a whole number from 40 to 240 BPM.');
  expect((await profile(request)).profile.defaults).toBeUndefined();

  await page.locator('#homeProfileTempo').fill('96');
  await page.locator('#homeProfileSampleRate').selectOption({ label: '48 kHz' });
  await page.locator('#homeProfileBitDepth').selectOption({ label: '24-bit' });
  // Another row's action redraws the whole card; what was typed here and not
  // saved yet is still in the fields afterwards, and still not stored.
  await page.locator('#homeProfileDetect').click();
  await expect(status).toHaveText('Found REAPER and Logic Pro.');
  await expect(page.locator('#homeProfileTempo')).toHaveValue('96');
  await expect(page.locator('#homeProfileSampleRate')).toHaveValue('48000');
  await expect(page.locator('#homeProfileBitDepth')).toHaveValue('24');
  expect((await profile(request)).profile.defaults).toBeUndefined();
  await row.getByRole('button', { name: 'Save' }).click();
  await expect(status).toHaveText('Defaults saved.');
  await expect(row).toContainText('96 BPM, 48 kHz, 24-bit. Set by you');
  expect((await profile(request)).profile.defaults).toMatchObject({
    tempo_bpm: 96,
    sample_rate_hz: 48000,
    bit_depth: 24,
    source: 'owner'
  });
  await evidence(page, '04-defaults');
});

test('templates are never read on load and say what is missing', async ({ page, request }) => {
  const panel = await openCard(page);
  const row = panel.locator('section[data-kind="templates"]');
  const card = await profile(request);
  // This sandbox stages no project plugin, so nothing can be listed.
  expect(card.facts_operation).toBe(false);
  expect(card.templates.state).toBe('plugin_missing');
  expect(card.profile.templates).toBeUndefined();
  await expect(row).toContainText('Install the REAPER plugin to read templates.');
  await expect(row.getByRole('button')).toHaveCount(0);
  const refused = await request.post(profileURL('/templates/review'), {
    data: { request_id: `review-${Date.now()}` },
    headers: HEADERS
  });
  expect(refused.status()).toBe(409);
  expect((await refused.json()).code).toBe('plugin_operation_unavailable');
});

test('the routes are the owner’s Home only and refuse stale or invalid saves', async ({
  request
}) => {
  const card = await profile(request);
  const stale = await request.post(profileURL('/fields'), {
    data: { request_id: `stale-${Date.now()}`, if_revision: 0, main_app: 'logic-pro' },
    headers: HEADERS
  });
  expect(stale.status()).toBe(409);
  expect((await stale.json()).code).toBe('home_profile_changed');
  for (const body of [
    { request_id: `a-${Date.now()}`, if_revision: card.revision, main_app: 'ableton-live' },
    { request_id: `b-${Date.now()}`, if_revision: card.revision, defaults: { tempo_bpm: 20 } },
    { request_id: `c-${Date.now()}`, if_revision: card.revision, templates: {} },
    { if_revision: card.revision, main_app: 'reaper' }
  ]) {
    const response = await request.post(profileURL('/fields'), { data: body, headers: HEADERS });
    expect(response.status(), JSON.stringify(body)).toBe(400);
  }
  expect((await request.get('/api/workspaces/not-a-home/assistant-program/profile')).status()).toBe(
    404
  );
  expect((await profile(request)).revision).toBe(card.revision);

  // A repeated request_id replays instead of writing again.
  const requestID = `replay-${Date.now()}`;
  const body = { request_id: requestID, if_revision: card.revision, confirm_apps: ['logic-pro'] };
  const first = await json(
    await request.post(profileURL('/fields'), { data: body, headers: HEADERS })
  );
  const again = await json(
    await request.post(profileURL('/fields'), { data: body, headers: HEADERS })
  );
  expect(first.replayed).toBeUndefined();
  expect(again.replayed).toBe(true);
  expect(again.revision).toBe(first.revision);
});

test('a read-only Home shows every value and disables every control', async ({ page, request }) => {
  const disable = await request.post('/api/plugins/music-project-management/disable', {
    headers: HEADERS
  });
  expect(disable.ok(), await disable.text()).toBeTruthy();
  try {
    const card = await profile(request);
    expect(card).toMatchObject({ available: true, read_only: true });
    expect(card.profile.main_app).toMatchObject({ id: 'reaper' });
    const panel = await openCard(page);
    await expect(page.locator('#homeProfileReadOnly')).toHaveText(
      'Saved values are readable; the Home provider is unavailable for changes.'
    );
    await expect(panel.locator('li[data-app-id="reaper"]')).toContainText('REAPER');
    for (const control of await panel.locator('button, select, input').all()) {
      await expect(control).toBeDisabled();
    }
    const refused = await request.post(profileURL('/detect'), {
      data: { request_id: `ro-${Date.now()}` },
      headers: HEADERS
    });
    expect(refused.status()).toBe(409);
    expect((await refused.json()).code).toBe('home_read_only');
    await evidence(page, '05-read-only');
  } finally {
    const enable = await request.post('/api/plugins/music-project-management/enable', {
      headers: HEADERS
    });
    expect(enable.ok(), await enable.text()).toBeTruthy();
  }
  expect((await profile(request)).read_only).toBe(false);
});
