import { test, expect } from '@playwright/test';

// Real-server keyboard/focus assertions. Run in its own isolated demo sandbox;
// these cases intentionally complete a fresh, optional interview with no rows.
test('reviewed interview stays keyboard-usable and an all-skipped review saves nothing', async ({
  page,
  request
}) => {
  const initial = await request.get('/api/personal-assistant/knowledge');
  if (initial.status() === 409) {
    const root = await request.post('/api/settings/workspace-root', {
      data: { workspace_root: '' }
    });
    expect(root.ok(), await root.text()).toBeTruthy();
    const hired = await request.post('/api/personal-assistant/hire', {
      data: {
        request_id: 'keyboard-hire',
        if_version: 0,
        display_name: 'Atlas',
        mandate: 'Help me plan.',
        focus_areas: ['plan_my_day']
      }
    });
    expect(hired.status(), await hired.text()).toBe(201);
    const relationship = (await hired.json()).personal_assistant;
    const hq = await request.post('/api/personal-assistant/hq', {
      data: {
        request_id: 'keyboard-hq',
        if_version: relationship.state_version,
        name: 'My HQ',
        timezone: 'UTC'
      }
    });
    expect(hq.status(), await hq.text()).toBe(201);
  }
  const before = await (await request.get('/api/personal-assistant/knowledge')).json();
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto('/profile');
  const shell = page.locator('#personalHQInterview');
  await expect(shell).toHaveAttribute('aria-busy', 'false');
  await expect(page.locator('#reviewedDossierSources')).toContainText('No durable app observation');
  const noSavedAppCheck = await request.post('/api/personal-assistant/knowledge/check-saved-apps');
  expect(noSavedAppCheck.ok(), await noSavedAppCheck.text()).toBeTruthy();
  const afterEmptyAppCheck = await (await request.get('/api/personal-assistant/knowledge')).json();
  expect(afterEmptyAppCheck.items).toHaveLength(before.items.length);
  expect(afterEmptyAppCheck.state_version).toBe(before.state_version);
  await expect(
    page.locator('#reviewedDossierSources').getByRole('link', { name: 'Tell Ori directly' })
  ).toHaveAttribute('href', '#personalHQInterview');
  // Mocked capability-only fixture: real profile/interview endpoints remain live.
  // A connected email/calendar source must remove setup nudges without claiming
  // the unimplemented #533/#534 learning producers are active.
  await page.route('**/api/personal-assistant/capabilities', route =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        capabilities: {
          cards: [
            {
              key: 'email',
              status: 'available',
              action_label: 'Review email connection',
              action_route: '/settings#google-account'
            },
            {
              key: 'calendar',
              status: 'available',
              action_label: 'Open Calendar Ops',
              action_route: '/'
            }
          ]
        }
      })
    })
  );
  await page.reload();
  await expect(page.locator('#reviewedDossierSources')).toContainText(
    'Reviewed memory suggestions from this source are not enabled here.'
  );
  await expect(
    page
      .locator('#reviewedDossierSources')
      .getByRole('link', { name: /Set up email|Set up Calendar/i })
  ).toHaveCount(0);
  await page.unroute('**/api/personal-assistant/capabilities');
  const wizard = page.getByRole('dialog', { name: 'Tell your assistant what matters' });
  // Deep link: the wizard opens in place instead of relying on scroll position.
  await page.goto('/profile#personalHQInterview');
  await page.reload(); // goto with only a new hash is not a page load
  await expect(wizard).toBeVisible();
  await expect(wizard.getByRole('textbox')).toBeFocused(); // fade-in done; Esc is ignored before
  await page.keyboard.press('Escape');
  await expect(wizard).toHaveCount(0);

  const start = page.getByRole('button', { name: 'Start interview' });
  await start.focus();
  await start.press('Enter');
  await expect(wizard).toBeVisible();
  await expect(wizard.getByRole('textbox', { name: /working on right now/i })).toBeFocused();
  // Each question shows an example answer, and it is read with the field.
  const hint = wizard.locator('.interview-wizard-hint');
  await expect(hint).toContainText('for example');
  expect(await wizard.getByRole('textbox').getAttribute('aria-describedby')).toContain(
    (await hint.getAttribute('id'))!
  );
  await expect(wizard.locator('[aria-current="step"]')).toContainText('Priority');
  await expect(wizard.locator('[aria-current="step"]')).toHaveCount(1);
  await expect(wizard.getByRole('status')).toHaveAttribute('aria-live', 'polite');
  // Esc closes the wizard and focus returns to the control that opened it.
  await page.keyboard.press('Escape');
  await expect(wizard).toHaveCount(0);
  await expect(start).toBeFocused();

  // Question 1's folder chooser. Mocked: this sandbox has no folders to show,
  // and the wizard's markup, not the scan, is what is checked here.
  await page.route('**/api/personal-assistant/folder-digest', route =>
    route.fulfill({
      json: {
        folder_digest: {
          offer: null,
          chips: [{ id: 'documents', label: 'Documents' }],
          picker_available: false,
          file_picker_available: false,
          picker_note: 'Pick a folder from the list for now.',
          paused: false,
          prompt_first_folder: false
        }
      }
    })
  );
  await page.route('**/api/personal-assistant/knowledge/interview/suggest', route =>
    route.fulfill({
      json: {
        suggestion: {
          text: 'Thesis, a LaTeX manuscript',
          folder: 'Thesis',
          alternates: [{ text: 'website, a Node.js package', folder: 'website' }]
        }
      }
    })
  );
  await start.press('Enter');
  await expect(wizard).toBeVisible();
  const box = wizard.getByRole('textbox', { name: /working on right now/i });
  await expect(box).toBeFocused();
  const chooser = wizard.getByRole('group', { name: 'Show me instead' });
  await expect(chooser).toBeVisible();
  // The chooser comes before the box, so Shift+Tab reaches its last button.
  await page.keyboard.press('Shift+Tab');
  const chip = chooser.getByRole('button', { name: 'Documents' });
  await expect(chip).toBeFocused();
  await chip.press('Enter');
  // The result is announced, focus lands in the box, and the caption that says
  // where the answer came from is part of the box's description.
  await expect(wizard.getByRole('status')).toHaveText('Filled in “Thesis, a LaTeX manuscript”.');
  await expect(box).toBeFocused();
  await expect(box).toHaveValue('Thesis, a LaTeX manuscript');
  const caption = wizard.locator('.interview-wizard-caption');
  await expect(caption).toContainText('From the folder you showed me');
  expect(await box.getAttribute('aria-describedby')).toContain((await caption.getAttribute('id'))!);
  const alternates = wizard.getByRole('group', { name: 'Also in this folder:' });
  await expect(
    alternates.getByRole('button', { name: 'website, a Node.js package' })
  ).toBeVisible();
  for (const part of [chooser, alternates]) {
    const partBounds = await part.boundingBox();
    expect(partBounds).toBeTruthy();
    expect(partBounds!.x + partBounds!.width).toBeLessThanOrEqual(390 + 1);
  }
  await page.unroute('**/api/personal-assistant/folder-digest');
  await page.unroute('**/api/personal-assistant/knowledge/interview/suggest');

  const answer = wizard.getByRole('textbox');
  await answer.fill('é'.repeat(251));
  await answer.press('Enter');
  const problem = wizard.locator('.interview-wizard-error');
  await expect(problem).toContainText('too long');
  await expect(problem).toHaveAttribute('role', 'alert');
  await expect(answer).toHaveAttribute('aria-invalid', 'true');
  const problemId = await problem.getAttribute('id');
  expect(await answer.getAttribute('aria-describedby')).toContain(problemId!);
  await expect(answer).toBeFocused();
  await expect(answer).toHaveValue('é'.repeat(251)); // never trimmed or changed
  await expect(wizard.locator('[aria-current="step"]')).toContainText('Priority');

  await answer.fill('');
  await wizard.getByRole('button', { name: 'Skip' }).click();
  await expect(wizard.locator('[aria-current="step"]')).toContainText('Working style');
  await expect(wizard.getByRole('textbox', { name: /communicate/i })).toBeFocused();
  await wizard.getByRole('button', { name: 'Skip' }).click();
  await expect(wizard.getByRole('textbox', { name: /person or recurring routine/i })).toBeFocused();
  await wizard.getByRole('button', { name: 'Skip' }).click();
  await expect(wizard.locator('[aria-current="step"]')).toContainText('Review');
  await expect(
    wizard.getByRole('heading', { name: 'Review exactly what will be saved' })
  ).toBeFocused();
  await expect(wizard).toContainText('All three answers skipped');
  const afterReview = await (await request.get('/api/personal-assistant/knowledge')).json();
  expect(afterReview.items).toHaveLength(before.items.length);
  const save = wizard.getByRole('button', { name: 'Finish without saving' });
  await save.focus();
  await save.press('Enter');
  await expect(wizard.getByRole('heading', { name: 'Finished. Nothing was saved.' })).toBeVisible();
  await wizard.getByRole('button', { name: 'Done' }).press('Enter');
  await expect(wizard).toHaveCount(0);
  await expect(page.locator('#personalHQInterviewStatus')).toContainText('complete');
  const completed = await (await request.get('/api/personal-assistant/knowledge')).json();
  expect(completed.items).toHaveLength(before.items.length);
  const factInput = page.locator('#personalHQFactText');
  await factInput.fill('two  spaces');
  await page.getByRole('button', { name: 'Save this fact' }).click();
  await expect(page.locator('#personalHQKnowledgeStatus')).toContainText('exact fact');
  await expect(factInput).toBeFocused();
  await expect(factInput).toHaveValue('two  spaces');
  await factInput.fill('é'.repeat(251));
  await page.getByRole('button', { name: 'Save this fact' }).click();
  await expect(page.locator('#personalHQKnowledgeStatus')).toContainText('500 UTF-8 bytes');
  await expect(factInput).toBeFocused();
  expect(
    (await (await request.get('/api/personal-assistant/knowledge')).json()).items
  ).toHaveLength(before.items.length);
  const longWord = 'thisisalongunbrokenwordfortestinglayout'.repeat(10);
  await factInput.fill(longWord);
  await page.getByRole('button', { name: 'Save this fact' }).click();
  const longFact = page.locator('#reviewedKnowledgeFacts .reviewed-knowledge-item');
  await expect(longFact).toContainText(longWord);
  const factBounds = await longFact.boundingBox();
  expect(factBounds).toBeTruthy();
  expect(factBounds!.x + factBounds!.width).toBeLessThanOrEqual(390 + 1);
  page.on('dialog', dialog => dialog.accept());
  await longFact.getByRole('button', { name: 'Forget', exact: true }).click();
  await expect(page.locator('#reviewedKnowledgeFacts')).not.toContainText(longWord);
  const bounds = await shell.boundingBox();
  expect(bounds).toBeTruthy();
  expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(390 + 1);
  await page.screenshot({ path: 'test-results/532-interview-keyboard-narrow.png', fullPage: true });
});

// UI-only failure fixture; real crash recovery is exercised with a restarted
// KnowledgeStore in internal/personalassistant/knowledge_forget_test.go.
test('interrupted Forget offers a focusable server-owned recovery, not old plaintext', async ({
  page,
  request
}) => {
  const live = await (await request.get('/api/personal-assistant/knowledge')).json();
  let finished = false;
  const preparedID = '31f21d7a-e4af-4490-bc83-eb4af5e78eb7';
  await page.route('**/api/personal-assistant/knowledge', route =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        ...live,
        items: finished
          ? []
          : [
              {
                id: preparedID,
                version: 2,
                state: 'needs_review',
                category: 'projects',
                source_kind: 'explicit',
                scope: 'personal_hq',
                review_unavailable: 'operation_pending',
                can_resume_forget: true
              }
            ]
      })
    })
  );
  await page.route(`**/api/personal-assistant/knowledge/${preparedID}/resume-forget`, route => {
    expect(route.request().method()).toBe('POST');
    expect(route.request().postData() || '').toBe('');
    finished = true;
    return route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: '{"status":"resumed"}'
    });
  });
  await page.goto('/profile');
  await expect(page.locator('#reviewedKnowledgeQueue')).not.toContainText('secret-or-stale-text');
  const resume = page.getByRole('button', { name: 'Finish interrupted Forget' });
  await expect(resume).toBeVisible();
  await resume.focus();
  await resume.press('Enter');
  await expect(resume).toHaveCount(0);
  expect(finished).toBe(true);
});

// UI-only fixture. Restarted-store approval/edit tests exercise the canonical
// recovery implementation; this verifies the bodyless keyboard-facing action.
test('interrupted review offers a focusable recovery without exposing its pending value', async ({
  page,
  request
}) => {
  const live = await (await request.get('/api/personal-assistant/knowledge')).json();
  const id = '51f21d7a-e4af-4490-bc83-eb4af5e78eb7';
  let finished = false;
  await page.route('**/api/personal-assistant/knowledge', route =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        ...live,
        items: finished
          ? []
          : [
              {
                id,
                version: 1,
                state: 'candidate',
                category: 'projects',
                source_kind: 'saved_app',
                scope: 'personal_hq',
                review_unavailable: 'operation_pending',
                can_resume_operation: true
              }
            ]
      })
    })
  );
  await page.route(`**/api/personal-assistant/knowledge/${id}/resume-operation`, route => {
    expect(route.request().method()).toBe('POST');
    expect(route.request().postData() || '').toBe('');
    finished = true;
    return route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: '{"status":"resumed"}'
    });
  });
  await page.goto('/profile');
  await expect(page.locator('#reviewedKnowledgeQueue')).not.toContainText('private-pending-value');
  const resume = page.getByRole('button', { name: 'Finish interrupted review' });
  await expect(resume).toBeVisible();
  await resume.focus();
  await resume.press('Enter');
  await expect(resume).toHaveCount(0);
  expect(finished).toBe(true);
});

// A failed refresh must mark old source labels stale without eating a draft.
test('failed dossier read labels prior source status stale and preserves unsaved wording', async ({
  page
}) => {
  await page.goto('/profile');
  const sources = page.locator('#reviewedDossierSources');
  await expect(sources).toContainText('Saved apps');
  const draft = page.locator('#personalHQFactText');
  await draft.fill('Keep this exact unsaved draft');
  await page.route('**/api/personal-assistant/knowledge', route =>
    route.fulfill({
      status: 503,
      contentType: 'application/json',
      body: '{"error":"temporarily unavailable"}'
    })
  );
  await page.evaluate(() =>
    document.dispatchEvent(new Event('personal-assistant-knowledge-changed'))
  );
  await expect(sources.locator('.reviewed-dossier-stale')).toContainText('may be stale');
  await expect(page.locator('#personalHQKnowledgeStatus')).toHaveClass(/is-warning/);
  await expect(draft).toHaveValue('Keep this exact unsaved draft');
  await page.unroute('**/api/personal-assistant/knowledge');
  await page.evaluate(() =>
    document.dispatchEvent(new Event('personal-assistant-knowledge-changed'))
  );
  await expect(sources.locator('.reviewed-dossier-stale')).toHaveCount(0);
  await expect(draft).toHaveValue('Keep this exact unsaved draft');
});
