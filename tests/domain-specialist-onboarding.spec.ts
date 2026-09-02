import { test, expect, Page } from '@playwright/test';

// The domain-specialist offer inside the canonical hire wizard.
//
// Two paths are covered against a real server: the offer a producer sees when
// REAPER is detected, and the byte-for-byte generic step everyone else sees.
// The generic case stubs the mapping empty rather than relying on the host
// machine having no creative app installed.

// One server, one relationship. `POST /api/onboarding/reset` reopens the
// wizard but deliberately does not delete the durable hire, so the case that
// actually completes a hire runs last and the file runs in order.
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

async function openHireFocusStep(page: Page) {
  await page.goto('/');
  await expect(page.locator('#onboardingModal')).toBeVisible();
  await page.locator('#onboardingUserName').fill('Jordan');
  await page.locator('#welcomeNextBtn').click();
  await page.locator('#continueWithoutModelBtn').click();
  await expect(page.locator('#onboardingPersonalAssistantHire')).toBeVisible();
  await page.locator('#pafAssistantName').fill('Atlas');
  await page.locator('#pafHireNextBtn').click();
  await expect(page.locator('#pafHireStepLabel')).toContainText('Hire step 2 of 3');
}

test.beforeEach(async ({ page }) => {
  // Every case starts from a not-yet-onboarded install.
  await page.request.post('/api/onboarding/reset');
});

test('a detected DAW offers help with the domain, not a quiz about the app', async ({ page }) => {
  // Serve the mapping and the match deterministically so the assertion does
  // not depend on what happens to be installed on the host.
  await page.route('**/api/onboarding/detect', route =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        success: true,
        platform: 'darwin',
        apps: [{ name: 'REAPER', path: '/Applications/REAPER.app' }],
        specialist: musicOffer
      })
    })
  );

  await openHireFocusStep(page);

  const offer = page.locator('#pafSpecialistOffer');
  await expect(offer).toBeVisible();
  await expect(offer).toHaveAttribute('data-decision', 'unanswered');
  await expect(page.locator('#pafSpecialistOfferHeadline')).toHaveText(
    'I found REAPER on this Mac.'
  );
  await expect(page.locator('#pafSpecialistOfferQuestion')).toHaveText(
    'Want me to help with your music projects?'
  );
  // The generic focus areas are still what is on screen until it is accepted.
  await expect(page.locator('[name="pafFocus"][value="plan_my_day"]')).toBeVisible();
  await page.screenshot({ path: `${SHOTS}/01-offer-detected.png`, fullPage: true });

  await page.locator('#pafSpecialistAcceptBtn').click();
  await expect(offer).toHaveAttribute('data-decision', 'accepted');
  await expect(page.locator('#pafSpecialistOfferAccepted')).toBeVisible();
  await expect(page.locator('#pafSpecialistOfferActions')).toBeHidden();
  // Accepting swaps in the domain's focus areas.
  await expect(page.locator('[name="pafFocus"][value="track_songs_in_progress"]')).toBeChecked();
  await expect(page.locator('[name="pafFocus"][value="prepare_for_meetings"]')).toHaveCount(0);
  await page.screenshot({ path: `${SHOTS}/02-offer-accepted.png`, fullPage: true });

  // Accepting must not gate anything: the rest of the hire still completes.
  await page.locator('#pafHireNextBtn').click();
  await expect(page.locator('#pafHireStepLabel')).toContainText('Hire step 3 of 3');
});

test('declining is one click, restores the generic focus areas, and is not re-asked', async ({
  page
}) => {
  await page.route('**/api/onboarding/detect', route =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ success: true, apps: [], specialist: musicOffer })
    })
  );

  await openHireFocusStep(page);
  await expect(page.locator('#pafSpecialistOffer')).toBeVisible();
  await page.locator('#pafSpecialistDeclineBtn').click();

  await expect(page.locator('#pafSpecialistOffer')).toBeHidden();
  for (const value of [
    'plan_my_day',
    'track_commitments_and_follow_ups',
    'prepare_for_meetings',
    'keep_projects_moving',
    'help_with_email',
    'something_else'
  ]) {
    await expect(page.locator(`[name="pafFocus"][value="${value}"]`)).toHaveCount(1);
  }
  await page.screenshot({ path: `${SHOTS}/03-offer-declined.png`, fullPage: true });

  // Leaving the step and coming back must not re-ask.
  await page.locator('#pafHireNextBtn').click();
  await page.locator('#pafHireBackBtn').click();
  await expect(page.locator('#pafHireStepLabel')).toContainText('Hire step 2 of 3');
  await expect(page.locator('#pafSpecialistOffer')).toBeHidden();
});

test('an empty mapping leaves the focus step exactly as it ships today', async ({ page }) => {
  await page.route('**/api/onboarding/detect', route =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ success: true, apps: [{ name: 'Safari' }], platform: 'darwin' })
    })
  );
  await page.route('**/api/onboarding/specialists', route =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ success: true, specialists: [] })
    })
  );

  await openHireFocusStep(page);

  await expect(page.locator('#pafSpecialistOffer')).toBeHidden();
  await expect(page.locator('#pafSpecialistManual')).toBeHidden();
  const labels = await page.locator('.paf-hire-focus-grid label').allInnerTexts();
  expect(labels.map(label => label.trim())).toEqual([
    'Plan my day',
    'Track commitments and follow-ups',
    'Prepare for meetings',
    'Keep projects moving',
    'Help with email',
    'Something else'
  ]);
  await page.screenshot({ path: `${SHOTS}/04-generic-no-mapping.png`, fullPage: true });
});

test('a domain that was not detected is still reachable manually', async ({ page }) => {
  await page.route('**/api/onboarding/detect', route =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ success: true, apps: [{ name: 'Safari' }] })
    })
  );

  await openHireFocusStep(page);

  await expect(page.locator('#pafSpecialistOffer')).toBeHidden();
  const manual = page.locator('#pafSpecialistManual button[data-paf-specialist-manual]').first();
  await expect(manual).toBeVisible();
  await expect(manual).toHaveText('I work on music');
  await page.screenshot({ path: `${SHOTS}/05-manual-path.png`, fullPage: true });

  await manual.click();
  await expect(page.locator('[name="pafFocus"][value="track_songs_in_progress"]')).toBeChecked();
  await expect(page.locator('#pafSpecialistManual')).toBeHidden();
});

test('a detection scan that never answers leaves the wizard fully usable', async ({ page }) => {
  // A scan that hangs for the whole 30s ceiling is the pathological case. It
  // must degrade to the generic path, not to a frozen wizard.
  await page.route('**/api/onboarding/detect', async () => {
    await new Promise(resolve => setTimeout(resolve, 30_000));
  });

  await openHireFocusStep(page);

  await expect(page.locator('#pafSpecialistOffer')).toBeHidden();
  await expect(page.locator('[name="pafFocus"][value="plan_my_day"]')).toBeVisible();
  await page.locator('[name="pafFocus"][value="help_with_email"]').check();
  await page.locator('#pafHireNextBtn').click();
  await expect(page.locator('#pafHireStepLabel')).toContainText('Hire step 3 of 3');
  await page.screenshot({ path: `${SHOTS}/06-slow-scan-generic.png`, fullPage: true });
});

// Last: this is the only case that completes a durable hire, and the hire is
// not undone by an onboarding reset.
test('the first assignment is re-worded for the domain without changing its item types', async ({
  page
}) => {
  await page.route('**/api/onboarding/detect', route =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ success: true, apps: [], specialist: musicOffer })
    })
  );

  await openHireFocusStep(page);
  await page.locator('#pafSpecialistAcceptBtn').click();
  await page.locator('#pafHireNextBtn').click();
  await page.locator('#pafHireConfirm').check();
  await page.locator('#pafHireBtn').click();

  // Hiring lands on Home; the quest is opened from there, in a fresh page
  // load where nothing was detected — the wording comes from the persisted
  // slug via the mapping.
  await expect(page.locator('#onboardingModal')).toBeHidden();
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
  await page.screenshot({ path: `${SHOTS}/07-producer-assignment-step-1.png`, fullPage: true });

  // The durable item types behind the re-worded steps are untouched.
  for (const type of ['priority', 'i_owe', 'waiting_on', 'fixed_commitment']) {
    await expect(page.locator(`[data-paf-assignment-row="${type}"]`)).toHaveCount(1);
  }

  await priorityRow.locator('[data-field="title"]').fill('Finish the bridge on Ivory');
  await page.locator('#pafPreviewAssignmentBtn').click();
  await expect(page.locator('[data-paf-assignment-legend="1"]')).toHaveText(
    'What do you owe a collaborator—or what are you waiting on?'
  );
  await expect(page.locator('[data-paf-add-row="waiting_on"]')).toHaveText(
    'Add something I’m waiting on'
  );
  await page.screenshot({ path: `${SHOTS}/08-producer-assignment-step-2.png`, fullPage: true });

  await page.locator('#pafPreviewAssignmentBtn').click();
  await expect(page.locator('#pafAssignmentStepLabel')).toContainText('Release and session dates');
  await expect(page.locator('[data-paf-assignment-legend="2"]')).toHaveText(
    'Dates to keep visible'
  );
  await expect(page.locator('[data-paf-add-row="fixed_commitment"]')).toHaveText(
    'Add a release or session date'
  );
  await page.screenshot({ path: `${SHOTS}/09-producer-assignment-step-3.png`, fullPage: true });

  // The payload the server receives carries the unchanged types.
  const preview = page.waitForRequest(
    request =>
      request.url().includes('/api/personal-assistant/first-assignment/preview') &&
      request.method() === 'POST'
  );
  await page.locator('#pafPreviewAssignmentBtn').click();
  const body = JSON.parse((await preview).postData() || '{}');
  expect(body.rows.map((row: { type: string }) => row.type)).toEqual(['priority']);

  // The accepted domain also survives the round trip to the relationship read,
  // which is what every post-hire surface reads.
  const relationship = await page.request.get('/api/personal-assistant');
  const payload = await relationship.json();
  expect((payload.personal_assistant || payload.data?.personal_assistant).specialist_slug).toBe(
    'music_production'
  );
});

test('the post-hire surface leads with the domain and suggests its workspace', async ({ page }) => {
  // Runs after the producer hire above, so the relationship already carries
  // the accepted slug.
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
  await page.screenshot({ path: `${SHOTS}/10-post-hire-capabilities.png`, fullPage: true });
});
