import { expect, test, type APIRequestContext, type Page } from '@playwright/test';
import { mkdirSync, renameSync } from 'node:fs';
import { join } from 'node:path';

// Acceptance for the Home profile card ("Your studio") against a staged Music
// Project Management candidate that declares `home_profile`. Run only through
//
//   ./scripts/music-home-demo.sh test --suite home-profile --source <candidate>
//
// which isolates HOME and ORI_DATA_DIR and supplies fixture applications: a
// REAPER.app in ORI_APPLICATIONS_DIR and a Logic Pro.app under the sandbox
// HOME, so the two-DAW path does not depend on what this Mac has installed.
//
// With `--reaper-source <candidate>` the sandbox also stages a REAPER plugin
// that offers `profile.read`, an application bundle with version metadata and
// a resource folder holding two project templates and one track template (plus
// files that are not templates), all under the sandbox HOME. The templates
// tests then drive the real operation through Ori's service runtime. Without
// it the templates row is checked in its "plugin missing" state only.
//
// Not covered here: the one-card setup line and last screen. The 0.1.1 to
// 0.2.0 Home upgrade is `--suite package-upgrade`.
const ENABLED =
  process.env.ORI_MUSIC_HOME_ACCEPTANCE === '1' && process.env.ORI_HOME_PROFILE_FIXTURES === '1';
const SHOTS = process.env.ORI_MUSIC_HOME_EVIDENCE_DIR || 'test-results/home-profile';
const HEADERS = { 'X-Requested-With': 'XMLHttpRequest' };
const REAPER_PATH = process.env.ORI_REAPER_PLUGIN_PATH || '';
const SANDBOX = process.env.ORI_MUSIC_HOME_SANDBOX || '';
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
  if (REAPER_PATH) {
    // The clean REAPER export, installed through the real plugin API. Nothing
    // about a Home's profile is read by installing it.
    await json(
      await request.post('/api/plugins/install', {
        headers: HEADERS,
        data: { source: REAPER_PATH, confirm: true }
      })
    );
    await json(await request.post('/api/plugins/reaper-plugin/enable', { headers: HEADERS }));
  }
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

test('the Home’s workspace page links straight to the card', async ({ page, request }) => {
  const card = await profile(request);
  // The card lives on the Home's own page, below four other panels. From the
  // workspace page there is a link named by the package's title that lands on
  // it, so nobody has to know the address.
  await page.goto(`/workspaces/${homeSlug}`);
  const link = page.locator('.ws-cmd-view-switch [data-home-profile-entry]');
  await expect(link).toHaveText(card.title);
  await expect(link).toHaveCount(1);
  mkdirSync(SHOTS, { recursive: true });
  await page.screenshot({ path: `${SHOTS}/00-workspace-page-link.png` });
  await link.click();
  await page.waitForURL(
    url => url.pathname === `/workspaces/${homeSlug}/assistant` && url.hash === '#homeProfilePanel'
  );
  const panel = page.locator('#homeProfilePanel');
  await expect(panel).toBeVisible();
  await expect(page.locator('#homeProfileTitle')).toBeInViewport();
  await expect(page.locator('#homeProfileTitle')).toBeFocused();
  // Arriving looked for nothing either.
  expect((await profile(request)).profile).toBeNull();
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
  if (REAPER_PATH) {
    // Detect asked the staged plugin for the version only: REAPER's row shows
    // a release number, and no template was named or stored.
    expect(card.facts_operation).toBe(true);
    expect(card.profile.apps[0]).toMatchObject({ id: 'reaper' });
    expect(card.profile.apps[0].version).toMatch(/^\d+\.\d+$/);
    await expect(reaper.locator('.home-profile-app-version')).toHaveText(
      card.profile.apps[0].version
    );
    expect(card.profile.templates).toBeUndefined();
  }
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

test('templates are listed only after the owner reviews, and Forget takes them back', async ({
  page,
  request
}) => {
  test.skip(REAPER_PATH === '', 'needs --reaper-source: a project plugin that offers profile.read');
  const panel = await openCard(page);
  const status = page.locator('#homeProfileStatus');
  const row = panel.locator('section[data-kind="templates"]');
  const notRead = 'Not read. Review to let Ori list the templates folder of REAPER.';

  // Nothing was read by installing the plugin, creating the Home, detecting or
  // loading this page.
  let card = await profile(request);
  expect(card.templates).toMatchObject({ state: 'not_read', app_name: 'REAPER' });
  expect(card.templates.consented ?? false).toBe(false);
  expect(card.profile.templates).toBeUndefined();
  await expect(row).toContainText(notRead);
  await expect(row.getByRole('button')).toHaveText(['Review']);

  // The review names the application and the two folders, and reads nothing.
  await row.getByRole('button', { name: 'Review' }).click();
  const dialog = page.locator('dialog.home-profile-dialog');
  await expect(dialog.getByRole('heading', { level: 2 })).toHaveText('Read REAPER templates?');
  await expect(dialog.locator('li')).toHaveText(['ProjectTemplates', 'TrackTemplates']);
  await expect(dialog).toContainText(
    'Ori will list the names of the files in ProjectTemplates and TrackTemplates inside your REAPER settings folder. It opens no template and changes nothing.'
  );
  await dialog.getByRole('button', { name: 'Not now' }).click();
  await expect(dialog).toHaveCount(0);
  await expect(status).toHaveText('Nothing was read.');
  expect((await profile(request)).profile.templates).toBeUndefined();

  // Only the dialog's own button reads.
  await row.getByRole('button', { name: 'Review' }).click();
  await dialog.getByRole('button', { name: 'Read templates' }).click();
  await expect(status).toHaveText('Listed 3 templates.');
  await expect(row).toContainText('2 project templates and 1 track template, read');
  await expect(row.locator('.home-profile-templates')).toHaveText([
    'Project Band Session, Vocal Comp',
    'Track Drum Bus'
  ]);
  await expect(row.getByRole('button')).toHaveText(['Read again', 'Forget']);
  card = await profile(request);
  expect(card.templates).toMatchObject({ state: 'listed', consented: true });
  const stored = card.profile.templates;
  expect(stored.consent).toMatchObject({ source: 'home_review' });
  expect(stored.consent.revoked_at).toBeUndefined();
  expect(stored.app_id).toBe('reaper');
  // Names only: the backup file, the subfolder's template and the text file
  // are not templates, and nothing stored is or contains a path.
  expect(
    stored.items.map((item: { kind: string; file: string }) => `${item.kind}:${item.file}`)
  ).toEqual([
    'project:Band Session.RPP',
    'project:Vocal Comp.RPP',
    'track:Drum Bus.RTrackTemplate'
  ]);
  // The fixture keeps one template in a subfolder. Its name is not listed, and
  // the list says it is not everything rather than passing over it in silence.
  expect(stored.truncated).toBe(true);
  await expect(row).toContainText('The folders may hold more than are listed here.');
  const serialized = JSON.stringify(stored);
  expect(serialized).not.toContain('/');
  if (SANDBOX) expect(serialized).not.toContain(SANDBOX);
  await evidence(page, '06-templates-listed');

  // Reading again under the consent already given asks nothing new.
  await row.getByRole('button', { name: 'Read again' }).click();
  await expect(status).toHaveText('Listed 3 templates.');
  await expect(dialog).toHaveCount(0);
  const again = (await profile(request)).profile.templates;
  expect(again.consent.granted_at).toBe(stored.consent.granted_at);

  // Forget clears the names and takes the agreement back in one step.
  await row.getByRole('button', { name: 'Forget' }).click();
  await expect(status).toHaveText('Forgotten. Nothing is read until you review it again.');
  await expect(row).toContainText(notRead);
  await expect(row.locator('.home-profile-templates')).toHaveCount(0);
  await expect(row.getByRole('button')).toHaveText(['Review']);
  card = await profile(request);
  expect(card.templates).toMatchObject({ state: 'not_read' });
  expect(card.templates.consented ?? false).toBe(false);
  expect(card.profile.templates.items).toBeUndefined();
  expect(card.profile.templates.consent.revoked_at).toBeTruthy();
  await evidence(page, '07-templates-forgotten');

  // An application that has no templates folders at all (it never saved a
  // template) is said as that, not as folders in which nothing was found.
  if (SANDBOX) {
    const resource = join(SANDBOX, 'Library', 'Application Support', 'REAPER');
    renameSync(resource, `${resource}.away`);
    try {
      await row.getByRole('button', { name: 'Review' }).click();
      await dialog.getByRole('button', { name: 'Read templates' }).click();
      await expect(row).toContainText('REAPER has no templates folders yet, read');
      await expect(row).toContainText('Save a template in REAPER, then press Read again.');
      await expect(row).not.toContainText('No templates were found');
      await expect(row.getByRole('button')).toHaveText(['Read again', 'Forget']);
      expect((await profile(request)).profile.templates).toMatchObject({
        empty_reason: 'no_folders'
      });
      await evidence(page, '08-no-templates-folders');
    } finally {
      renameSync(`${resource}.away`, resource);
    }
    // Once the folders exist, Read again lists them and the reason is gone.
    await row.getByRole('button', { name: 'Read again' }).click();
    await expect(status).toHaveText('Listed 3 templates.');
    expect((await profile(request)).profile.templates.empty_reason).toBeUndefined();
    await row.getByRole('button', { name: 'Forget' }).click();
    await expect(status).toHaveText('Forgotten. Nothing is read until you review it again.');
  }

  // A review that no longer describes the Home reads nothing: after a new
  // consent, the review obtained before it is refused.
  const post = (suffix: string, data: Record<string, unknown>) =>
    request.post(profileURL(suffix), { data, headers: HEADERS });
  const review = await json(await post('/templates/review', { request_id: `r-${Date.now()}` }));
  await json(
    await post('/templates/commit', { request_id: `c1-${Date.now()}`, review_id: review.review_id })
  );
  const stale = await post('/templates/commit', {
    request_id: `c2-${Date.now()}`,
    review_id: review.review_id
  });
  expect(stale.status()).toBe(409);
  expect((await stale.json()).code).toBe('home_profile_changed');
  expect((await profile(request)).profile.templates.items).toHaveLength(3);
});

test('templates are never read on load and say what is missing', async ({ page, request }) => {
  test.skip(REAPER_PATH !== '', 'a project plugin is staged; see the templates test above');
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
