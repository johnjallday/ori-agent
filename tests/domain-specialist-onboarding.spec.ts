import { test, expect, APIRequestContext, Page } from '@playwright/test';
import { mkdirSync, rmSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';

// The domain-specialist offer, made on Home after the hire.
//
// Hiring stays one decision: the wizard asks about work in general and never
// mentions a domain. Once the assistant exists, Home offers help with the
// domain a detected app suggests. These cases cover the offer, both answers,
// the manual route, and every way detection can fail.

// One server, one relationship. `POST /api/onboarding/reset` reopens the
// wizard but deliberately does not delete the durable hire, so the cases run
// in order and the ones that need a hire come after it.
test.describe.configure({ mode: 'serial' });

const SHOTS = 'test-results/domain-specialist';

const musicOffer = {
  slug: 'music_production',
  display_name: 'music projects',
  specialist_name: 'Reaper Producer',
  offer_copy: {
    headline: 'I found REAPER on this Mac.',
    question: 'Want me to help with your music projects?',
    accept_label: 'Yes, help with my music',
    decline_label: 'No thanks',
    accepted_note:
      'Your assistant will keep an eye on your music projects and tell you what Reaper Producer has done.',
    manual_label: 'I work on music'
  },
  focus_areas: [
    { value: 'plan_my_day', label: 'Plan my studio day', selected: true },
    { value: 'track_songs_in_progress', label: 'Track songs in progress', selected: true },
    {
      value: 'chase_collaborator_handoffs',
      label: 'Chase collaborator handoffs',
      selected: true
    },
    { value: 'keep_release_dates_visible', label: 'Keep release and session dates visible' },
    { value: 'organize_project_files', label: 'Keep project files organized' },
    { value: 'something_else', label: 'Something else' }
  ],
  assignment_labels: [
    {
      type: 'priority',
      label: 'Song or project in progress',
      placeholder: 'Which track are you on?',
      add_label: 'Add a song or project'
    },
    {
      type: 'i_owe',
      label: 'Something I owe a collaborator',
      placeholder: 'What did you promise?',
      add_label: 'Add something I owe'
    },
    {
      type: 'waiting_on',
      label: 'Waiting on (mix, master, feature)',
      placeholder: 'What are you waiting for?',
      add_label: 'Add something I’m waiting on'
    },
    {
      type: 'fixed_commitment',
      label: 'Release or session date',
      placeholder: 'Release, session, or deadline to keep visible',
      add_label: 'Add a release or session date'
    }
  ],
  assignment_steps: [
    { index: 0, title: 'Songs in progress', legend: 'What are you working on right now?' },
    {
      index: 1,
      title: 'Owed and waiting',
      legend: 'What do you owe a collaborator—or what are you waiting on?'
    },
    { index: 2, title: 'Release and session dates', legend: 'Dates to keep visible' }
  ],
  suggested_template_id: 'reaper-song',
  suggestion: {
    title: 'Set up your studio workspace',
    body: 'The Reaper Song blueprint brings in Reaper Producer.',
    action_label: 'Create the studio workspace',
    action_route: '/?create=1&blueprint=reaper-song'
  },
  capability_order: ['projects', 'folders', 'calendar', 'email']
};

// stubDetection serves the scan deterministically, so an assertion never
// depends on what happens to be installed on the host running the suite.
async function stubDetection(page: Page, specialist: unknown) {
  await page.route('**/api/onboarding/detect', route =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        success: true,
        platform: 'darwin',
        apps: specialist ? [{ name: 'REAPER', path: '/Applications/REAPER.app' }] : [],
        specialist: specialist || undefined
      })
    })
  );
}

async function completeHire(page: Page, name: string) {
  await page.goto('/');
  await expect(page.locator('#onboardingModal')).toBeVisible();
  await page.locator('#onboardingUserName').fill('Jordan');
  await page.locator('#welcomeNextBtn').click();
  await page.locator('#continueWithoutModelBtn').click();
  await expect(page.locator('#onboardingPersonalAssistantHire')).toBeVisible();
  await page.locator('#pafAssistantName').fill(name);
  await page.locator('#pafHireNextBtn').click();
  await page.locator('#pafHireNextBtn').click();
  await page.locator('#pafHireConfirm').check();
  await page.locator('#pafHireBtn').click();
  await expect(page.locator('#onboardingModal')).toBeHidden();
}

const offer = (page: Page) => page.locator('#personalAssistantSpecialistOffer');

async function openToday(page: Page) {
  const launcher = page.locator('#personalAssistantLauncher');
  await expect(launcher).toBeVisible();
  if (await page.locator('#personalAssistantPanel').isHidden()) await launcher.click();
  const todayTab = page.locator('#personalAssistantTodayTab');
  if ((await todayTab.getAttribute('aria-selected')) !== 'true') await todayTab.click();
  await expect(page.locator('#personalAssistantToday')).toBeVisible();
}

// buildHQ finishes setup the way the Map's Build My HQ form does. A fresh hire
// now lands in needs_hq, and the capability projection only reports sources for
// a relationship that is fully active.
async function buildHQ(page: Page) {
  const relationship = await page.request.get('/api/personal-assistant');
  const payload = await relationship.json();
  const paf = payload.personal_assistant || payload.data?.personal_assistant;
  const res = await page.request.post('/api/personal-assistant/hq', {
    data: {
      request_id: `hq-${Date.now()}`,
      if_version: paf.state_version,
      name: 'My HQ',
      timezone: 'UTC',
      schedule_days: ['mon', 'tue', 'wed', 'thu', 'fri'],
      schedule_time: '08:00'
    }
  });
  expect(res.ok(), await res.text()).toBeTruthy();
}

test('the hire wizard never mentions a domain', async ({ page }) => {
  await page.request.post('/api/onboarding/reset');
  await stubDetection(page, musicOffer);

  await page.goto('/');
  await expect(page.locator('#onboardingModal')).toBeVisible();
  await page.locator('#onboardingUserName').fill('Jordan');
  await page.locator('#welcomeNextBtn').click();
  await page.locator('#continueWithoutModelBtn').click();
  await page.locator('#pafAssistantName').fill('Atlas');
  await page.locator('#pafHireNextBtn').click();
  await expect(page.locator('#pafHireStepLabel')).toContainText('Hire step 2 of 3');

  // Hiring is one decision. The focus step is the shipped generic six and
  // nothing else.
  const labels = await page.locator('.paf-hire-focus-grid label').allInnerTexts();
  expect(labels.map(label => label.trim())).toEqual([
    'Plan my day',
    'Track commitments and follow-ups',
    'Prepare for meetings',
    'Keep projects moving',
    'Help with email',
    'Something else'
  ]);
  await expect(page.locator('#onboardingModal')).not.toContainText(/REAPER|music projects/i);
  await page.screenshot({ path: `${SHOTS}/01-hire-has-no-offer.png`, fullPage: true });
});

test('Home offers help with the detected domain once setup is finished', async ({ page }) => {
  await page.request.post('/api/onboarding/reset');
  await stubDetection(page, musicOffer);
  await completeHire(page, 'Atlas');

  // Straight after the hire, Home is running its guided HQ walkthrough. The
  // domain offer must not compete with the user's actual next step.
  await openToday(page);
  await expect(offer(page)).toBeHidden();
  await page.screenshot({ path: `${SHOTS}/02a-offer-waits-for-hq.png`, fullPage: true });

  await buildHQ(page);
  await page.goto('/');
  await openToday(page);
  await expect(offer(page)).toBeVisible();
  await expect(offer(page)).toHaveAttribute('data-decision', 'unanswered');
  await expect(page.locator('#personalAssistantSpecialistOfferHeadline')).toHaveText(
    'I found REAPER on this Mac.'
  );
  await expect(page.locator('#personalAssistantSpecialistOfferQuestion')).toHaveText(
    'Want me to help with your music projects?'
  );
  await expect(page.locator('#personalAssistantSpecialistAcceptBtn')).toHaveText(
    'Yes, help with my music'
  );
  await page.screenshot({ path: `${SHOTS}/02-home-offer.png`, fullPage: true });

  await page.locator('#personalAssistantSpecialistAcceptBtn').click();
  await expect(offer(page)).toHaveAttribute('data-decision', 'accepted');
  await expect(page.locator('#personalAssistantSpecialistOfferAccepted')).toBeVisible();
  await expect(page.locator('#personalAssistantSpecialistOfferActions')).toBeHidden();
  await page.screenshot({ path: `${SHOTS}/03-home-offer-accepted.png`, fullPage: true });

  // Accepting records the domain and reshapes the working agreement.
  const relationship = await page.request.get('/api/personal-assistant');
  const payload = await relationship.json();
  const paf = payload.personal_assistant || payload.data?.personal_assistant;
  expect(paf.specialist_slug).toBe('music_production');
  expect(paf.specialist_offer_state).toBe('accepted');
  expect(paf.focus_areas).toEqual([
    'plan_my_day',
    'track_songs_in_progress',
    'chase_collaborator_handoffs'
  ]);

  // The answer is durable, and the question is gone for good: the confirmation
  // is a one-time acknowledgement, not a banner that lives on Home forever.
  // What persists is the effect — focus areas, card order, the studio section.
  await page.goto('/');
  await openToday(page);
  await expect(offer(page)).toBeHidden();
  await expect(page.locator('#personalAssistantSpecialistManual')).toBeHidden();
});

test('the post-hire surface leads with the domain and suggests its workspace', async ({ page }) => {
  const capabilities = await page.request.get('/api/personal-assistant/capabilities');
  const payload = await capabilities.json();
  const projection = payload.capabilities || payload.data?.capabilities;

  // Producer order: their own work before email and calendar.
  expect(projection.cards.map((card: { key: string }) => card.key)).toEqual([
    'projects',
    'folders',
    'calendar',
    'email'
  ]);
  expect(projection.suggestion.template_id).toBe('reaper-song');
  expect(projection.suggestion.action_route.startsWith('/')).toBe(true);

  await page.goto('/?personal-assistant=working-agreement');
  const suggestion = page.locator('[data-role="capability-suggestion"]');
  await expect(suggestion).toBeVisible();
  await expect(suggestion.locator('h4')).toHaveText('Set up your studio workspace');
  // Nothing here may imply the assistant can hand work to the specialist.
  await expect(suggestion).not.toContainText(/assign|delegate|tell it to|hand off/i);
  const cards = page.locator('#personalAssistantCapabilities .pa-capability');
  await expect(cards.first()).toHaveAttribute('data-role', 'capability-suggestion');
  await expect(cards.nth(1).locator('h4')).toContainText('Projects and workspaces');
  await page.screenshot({ path: `${SHOTS}/04-post-hire-capabilities.png`, fullPage: true });
});

test('the first assignment is re-worded for the domain without changing its item types', async ({
  page
}) => {
  // A fresh page load where no detection ran: the wording comes from the
  // domain recorded on the relationship.
  await page.goto('/?quest=plan-first-day');
  await expect(page.locator('#onboardingPersonalAssistantAssignment')).toBeVisible();

  await expect(page.locator('#pafAssignmentStepLabel')).toContainText('Songs in progress');
  await expect(page.locator('[data-paf-assignment-legend="0"]')).toHaveText(
    'What are you working on right now?'
  );
  await expect(page.locator('[data-paf-add-row="priority"]')).toHaveText('Add a song or project');
  const priorityRow = page.locator('[data-paf-assignment-row="priority"]').first();
  await expect(priorityRow.locator('label').first()).toContainText('Song or project in progress');
  await expect(priorityRow.locator('[data-field="title"]')).toHaveAttribute(
    'placeholder',
    'Which track are you on?'
  );
  await page.screenshot({ path: `${SHOTS}/05-producer-assignment.png`, fullPage: true });

  // The durable item types behind the re-worded steps are untouched.
  for (const type of ['priority', 'i_owe', 'waiting_on', 'fixed_commitment']) {
    await expect(page.locator(`[data-paf-assignment-row="${type}"]`)).toHaveCount(1);
  }

  await priorityRow.locator('[data-field="title"]').fill('Finish the bridge on Ivory');
  await page.locator('#pafPreviewAssignmentBtn').click();
  await expect(page.locator('[data-paf-assignment-legend="1"]')).toHaveText(
    'What do you owe a collaborator—or what are you waiting on?'
  );
  await page.locator('#pafPreviewAssignmentBtn').click();
  await expect(page.locator('[data-paf-assignment-legend="2"]')).toHaveText(
    'Dates to keep visible'
  );

  // The payload the server receives carries the unchanged types.
  const preview = page.waitForRequest(
    request =>
      request.url().includes('/api/personal-assistant/first-assignment/preview') &&
      request.method() === 'POST'
  );
  await page.locator('#pafPreviewAssignmentBtn').click();
  const body = JSON.parse((await preview).postData() || '{}');
  expect(body.rows.map((row: { type: string }) => row.type)).toEqual(['priority']);
});

test('Today names the specialist that did the studio work and links straight to it', async ({
  page
}) => {
  // The projection itself is covered against real workspace records in
  // internal/personalassistant/today_studio_test.go — a task only reaches
  // "review" with a result by being executed, which needs a model the demo
  // sandbox deliberately does not have. This drives the rendering.
  await page.route('**/api/personal-assistant/today', route =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        today: {
          state: 'active',
          relationship_state: 'active',
          display_name: 'Atlas',
          hq_workspace_id: 'hq-1',
          model: { available: true, status: 'available' },
          brief: { health: { status: 'healthy_empty' }, items: [] },
          decisions: { health: { status: 'healthy_empty' }, items: [] },
          priorities: { health: { status: 'healthy_empty' }, items: [] },
          follow_ups: { health: { status: 'healthy_empty' }, items: [] },
          results: { health: { status: 'healthy_empty' }, items: [] },
          studio: {
            health: { status: 'available' },
            domain: 'music projects',
            specialist_name: 'Reaper Producer',
            workspace_name: 'Ivory',
            route: '/workspaces/ivory',
            items: [
              {
                id: 'studio-1',
                kind: 'studio_result',
                title: 'Bounced a rough mix of Ivory',
                attribution: 'Reaper Producer',
                route: '/workspaces/ivory?ticket=studio-1'
              },
              {
                id: 'studio-2',
                kind: 'studio_result',
                title: 'Tagged the session takes',
                attribution: 'Reaper Producer',
                route: '/workspaces/ivory?ticket=studio-2'
              }
            ]
          },
          links: { advanced: '/agents' },
          generated_at: new Date().toISOString()
        }
      })
    })
  );

  await page.goto('/');
  await openToday(page);
  const section = page.locator('#personalAssistantTodayStudioSection');
  await expect(section).toBeVisible();
  await expect(page.locator('#personalAssistantTodayStudioTitle')).toHaveText('From Ivory');

  const rows = page.locator('#personalAssistantTodayStudio li');
  await expect(rows).toHaveCount(2);
  await expect(rows.first()).toContainText('Bounced a rough mix of Ivory');
  await expect(rows.first().locator('.personal-assistant-today__attribution')).toHaveText(
    'Reaper Producer'
  );

  // The direct route to the specialist, offered plainly.
  const note = page.locator('#personalAssistantTodayStudioNote');
  await expect(note).toContainText('ask Reaper Producer directly');
  await expect(note.locator('a')).toHaveAttribute('href', '/workspaces/ivory');
  await expect(note.locator('a')).toHaveText('Open Ivory');
  // And nothing claiming the assistant can hand it work.
  await expect(section).not.toContainText(/delegate|assign|on your behalf|instead|workaround/i);
  await page.screenshot({ path: `${SHOTS}/06-today-studio-attribution.png`, fullPage: true });
});

// --- Specialist presence ------------------------------------------------

// The blueprint the REAPER plugin publishes, reduced to what presence needs:
// the workspace name, the template ID the mapping matches on, and the seeded
// specialist. The real blueprint's setup wizard and starter tasks are
// deliberately left out — this feature changes neither, and an auto-starting
// setup task would cover the surface being captured.
const REAPER_SONG_MANIFEST = {
  name: 'Reaper Song',
  description: 'Produce one song end to end.',
  tags: ['music'],
  agents: [
    {
      name: 'Reaper Producer',
      role: 'orchestrator',
      system_prompt: 'You own studio work for this song.'
    }
  ]
};

let installedTemplateDir = '';

async function installReaperSongBlueprint(request: APIRequestContext) {
  const res = await request.get('/api/project-templates');
  expect(res.ok(), await res.text()).toBeTruthy();
  const body = await res.json();
  const root = (body.templates_root || body.data?.templates_root) as string;
  expect(root, 'server did not report its templates root').toBeTruthy();
  installedTemplateDir = join(root, 'reaper-song');
  mkdirSync(installedTemplateDir, { recursive: true });
  writeFileSync(
    join(installedTemplateDir, 'template.json'),
    JSON.stringify(REAPER_SONG_MANIFEST, null, 2)
  );
}

test.afterAll(() => {
  if (installedTemplateDir) rmSync(installedTemplateDir, { recursive: true, force: true });
});

test('the seeded specialist renders with the one shared workspace portrait', async ({
  page,
  request
}) => {
  await installReaperSongBlueprint(request);
  const created = await request.post('/api/workspaces', {
    data: {
      name: `Ivory ${Date.now()}`,
      description: '',
      template_id: 'reaper-song',
      create_template_agents: true
    }
  });
  expect(created.ok(), await created.text()).toBeTruthy();
  const body = await created.json();
  const slug = (body.folder?.folder_slug ||
    body.workspace?.folder_slug ||
    body.folder?.slug) as string;
  expect(slug, `no folder slug in ${JSON.stringify(body)}`).toBeTruthy();

  await page.goto(`/workspaces/${slug}`);
  // The shared renderer's own element. There must be exactly one portrait
  // system, so this asserts the specialist uses it — not that some portrait
  // happens to be on screen.
  const portrait = page.locator('.ws-map-av', { hasText: 'Reaper Producer' }).first();
  await expect(portrait).toBeVisible();
  // The workspace page fades in and re-mounts its agent list, so anything
  // holding a node across that swap detaches. Settle first, then act.
  await page.waitForTimeout(900);
  await expect(portrait.locator('.ws-map-av-figure')).toHaveCount(1);
  await expect(portrait.locator('.ws-map-av-label')).toHaveText('Reaper Producer');
  await page.screenshot({ path: `${SHOTS}/07-specialist-portrait.png`, fullPage: true });
  await portrait.screenshot({ path: `${SHOTS}/07b-specialist-portrait-closeup.png` });
});

// --- Declining, the manual route, and every way detection can fail ------
//
// These need an unanswered relationship, so they run last against a server
// whose relationship is reset between them by the harness that starts it.

// There is one relationship per server and the accept case above answered it
// for good, which is the point of the feature. These two cases re-open the
// question at the client boundary: the relationship read is served as
// unanswered and the answer write is accepted without being applied. The
// durable side of declining is covered in
// internal/personalassistant/specialist_offer_test.go.
async function reopenTheOffer(page: Page) {
  await page.route('**/api/personal-assistant', async route => {
    if (route.request().method() !== 'GET') return route.continue();
    const response = await route.fetch();
    const payload = await response.json();
    const paf = payload.personal_assistant || payload.data?.personal_assistant || {};
    paf.specialist_offer_state = '';
    paf.specialist_slug = '';
    return route.fulfill({ response, json: payload });
  });
  await page.route('**/api/personal-assistant/specialist', route =>
    route.fulfill({ status: 200, contentType: 'application/json', body: '{"success":true}' })
  );
}

test('declining is one click and is never asked again', async ({ page }) => {
  await stubDetection(page, musicOffer);
  await reopenTheOffer(page);

  await page.goto('/');
  await openToday(page);
  await expect(offer(page)).toBeVisible();
  await page.locator('#personalAssistantSpecialistDeclineBtn').click();
  await expect(offer(page)).toBeHidden();
  // The manual route goes with it: the question has been answered.
  await expect(page.locator('#personalAssistantSpecialistManual')).toBeHidden();
  await page.screenshot({ path: `${SHOTS}/08-offer-declined.png`, fullPage: true });
});

test('a domain that was not detected is still reachable by hand', async ({ page }) => {
  await stubDetection(page, null);
  await reopenTheOffer(page);

  await page.goto('/');
  await openToday(page);
  await expect(offer(page)).toBeHidden();
  const manual = page
    .locator('#personalAssistantSpecialistManual button[data-specialist-manual]')
    .first();
  await expect(manual).toBeVisible();
  await expect(manual).toHaveText('I work on music');
  await page.screenshot({ path: `${SHOTS}/09-manual-path.png`, fullPage: true });
});

test('a scan that never answers leaves Home fully usable', async ({ page }) => {
  // A scan that hangs for the whole 30s ceiling is the pathological case. It
  // must degrade to Home as it is today, not to a stuck page.
  await page.route('**/api/onboarding/detect', async () => {
    await new Promise(resolve => setTimeout(resolve, 30_000));
  });

  await page.goto('/');
  await openToday(page);
  await expect(offer(page)).toBeHidden();
  // Home's own content is there and interactive.
  await expect(page.locator('#personalAssistantTodaySections')).toBeVisible();
  await page.screenshot({ path: `${SHOTS}/10-slow-scan.png`, fullPage: true });
});

test('a failing scan is never an error the user sees', async ({ page }) => {
  for (const failure of [{ status: 500 }, { status: 404 }] as const) {
    await page.route('**/api/onboarding/detect', route =>
      route.fulfill({ status: failure.status, contentType: 'application/json', body: '{}' })
    );
    await page.goto('/');
    await openToday(page);
    await expect(offer(page)).toBeHidden();
    await expect(page.locator('#personalAssistantToday')).not.toContainText(
      /could not|failed|error/i
    );
    await page.unroute('**/api/onboarding/detect');
  }
});
