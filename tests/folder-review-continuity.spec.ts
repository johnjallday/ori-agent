import { test, expect, type APIRequestContext } from '@playwright/test';
import { mkdir, readFile, realpath, rename, stat, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { chooseAttach, chooseConversation } from './helpers/assistant-menus';
import { revealFolderSetupDetails } from './helpers/folder-setup-card';

// Explicit phases against the same disposable wt demo sandbox: seed/reopen,
// repick, home-seed/home-reopen, then build-seed/build-reopen. Restart only the
// owned server between each seed/reopen pair. No user folders or real models
// are used. Normal smoke runs skip this isolated lifecycle acceptance.
const sandbox = process.env.ORI_FOLDER_REVIEW_CONTINUITY_SANDBOX;
const phase = process.env.ORI_FOLDER_REVIEW_CONTINUITY_PHASE;
const evidence = resolve('tasks/evidence-folder-review-continuity');

async function checkedSandbox(request: APIRequestContext) {
  const root = await realpath(sandbox!);
  expect(root.startsWith(`${await realpath(tmpdir())}/ori-demo.`)).toBe(true);
  expect(await readFile(join(root, '.ori-demo-owner'), 'utf8')).toBe(
    `worktree=${await realpath('.')}\n`
  );
  const response = await request.get('/api/settings/workspace-root');
  expect(response.ok()).toBe(true);
  const roots = await response.json();
  expect(
    (roots.effective_workspace_root || roots.default_workspace_root).startsWith(`${root}/`)
  ).toBe(true);
  await mkdir(evidence, { recursive: true });
  return root;
}

test('an opened folder review survives a server restart without a new pick', async ({
  page,
  request
}) => {
  test.skip(
    !sandbox || !['seed', 'reopen'].includes(phase || ''),
    'requires an isolated demo and explicit phase'
  );
  test.setTimeout(300_000);
  page.setDefaultTimeout(20_000);
  await page.setViewportSize({ width: 1440, height: 1000 });
  const temporaryRoot = await realpath(tmpdir());
  expect((await realpath(sandbox!)).startsWith(`${temporaryRoot}/ori-demo.`)).toBe(true);
  expect(await readFile(join(sandbox!, '.ori-demo-owner'), 'utf8')).toBe(
    `worktree=${await realpath('.')}\n`
  );
  await mkdir(evidence, { recursive: true });
  const stateFile = join(sandbox!, '.folder-review-continuity-state.json');
  const carried = await readFile(stateFile, 'utf8')
    .then(JSON.parse)
    .catch(() => null);
  const api = async (method: string, path: string, data?: unknown) => {
    const response = await request.fetch(path, { method, data });
    expect(response.ok(), `${method} ${path}: ${await response.text()}`).toBeTruthy();
    return response.json();
  };
  const roots = await api('GET', '/api/settings/workspace-root');
  expect(
    (roots.effective_workspace_root || roots.default_workspace_root).startsWith(
      `${await realpath(sandbox!)}/`
    ),
    'server must belong to the supplied sandbox'
  ).toBe(true);
  if (phase === 'seed' && !carried) {
    await api('POST', '/api/onboarding/skip');
    await api('POST', '/api/settings/workspace-root', { workspace_root: '' });
    let assistant = (await api('GET', '/api/personal-assistant')).personal_assistant;
    if (assistant.state === 'needs_hire') {
      assistant = (
        await api('POST', '/api/personal-assistant/hire', {
          request_id: 'folder-continuity-hire',
          if_version: assistant.state_version || 0,
          display_name: 'Atlas',
          mandate: 'Help me plan.',
          focus_areas: ['plan_my_day']
        })
      ).personal_assistant;
      await api('POST', '/api/personal-assistant/hq', {
        request_id: 'folder-continuity-hq',
        if_version: assistant.state_version,
        name: 'My HQ',
        timezone: 'UTC'
      });
    }
  }
  await page.goto('/');
  await page.waitForFunction(() => (window as any).PersonalAssistantPanel?._state.view.available);
  if (!(await page.locator('#personalAssistantPanel').isVisible())) {
    await page.locator('#personalAssistantLauncher').click();
  }
  let state: { conversation: string; offer: string };
  if (phase === 'seed' && !carried) {
    await chooseAttach(page, '#personalAssistantFolderChip');
    const chooser = page.locator('#personalAssistantFolderChoices');
    await chooser.getByLabel('or paste a folder path').fill(join(sandbox!, 'Albums', 'Album-5'));
    const reviewed = page.waitForResponse(
      response =>
        response.url().endsWith('/folder-context/review') && response.request().method() === 'POST'
    );
    await chooser.getByRole('button', { name: 'Use this path', exact: true }).click();
    const response = await reviewed;
    expect(response.ok(), await response.text()).toBeTruthy();
    const body = await response.json();
    state = { conversation: body.conversation.id, offer: body.folder_context.offer_id };
    expect(state.offer).toBeTruthy();
    await writeFile(stateFile, JSON.stringify(state), { mode: 0o600 });
  } else {
    expect(carried, 'seed the sandbox before reopening').toBeTruthy();
    state = carried;
    await chooseConversation(page, '#personalAssistantConversationContinue');
    await page.locator(`button[data-conversation-id="${state.conversation}"]`).click();
  }
  const card = page.locator('#personalAssistantFolderOffer');
  await expect(card).toBeVisible();
  await expect(card).toContainText('Album-5');
  await expect(card.getByRole('button', { name: 'Pick it again', exact: true })).toHaveCount(0);
  if (phase === 'seed') {
    // These reviewed packages install only inside the disposable server's
    // plugin store. Each download uses its real disclosure/confirmation UI.
    for (const name of ['REAPER', 'Music Project Management']) {
      const install = card.getByRole('button', { name: `Install ${name}`, exact: true });
      if (await install.count()) {
        await install.click();
        const confirm = card.getByRole('button', { name: 'Confirm installation', exact: true });
        await expect(confirm).toBeEnabled({ timeout: 30_000 });
        await confirm.click();
        await expect(install).toHaveCount(0, { timeout: 120_000 });
      }
    }
  }
  // Explicit Review again must also work using the restored authority, while
  // preserving the original dated observation and offer reference.
  const recheck = card.getByRole('button', { name: 'Check again', exact: true });
  if (await recheck.count()) {
    const refreshed = page.waitForResponse(
      response =>
        response.url().endsWith('/folder-context/review') && response.request().method() === 'POST'
    );
    await recheck.click();
    const response = await refreshed;
    expect(response.ok(), await response.text()).toBeTruthy();
    if (phase === 'reopen') {
      const fresh = await response.json();
      expect(fresh.folder_context.historical).toBe(true);
      expect(fresh.folder_context.offer_id).toBe(state.offer);
    }
  }
  const stored = await api('GET', `/api/home-assistant/conversations/${state.conversation}`);
  const offer = stored.folder_reviews[state.offer];
  expect(offer.id).toBe(state.offer);
  expect(offer.needs_pick).not.toBe(true);
  expect(stored.folder_review_context.status).toBe('awaiting_confirmation');
  expect(stored.folder_review_context.historical).not.toBe(true);
  const setupButton = card
    .getByRole('button', { name: /set up|hire and start|^Add .* to /i })
    .first();
  await setupButton.scrollIntoViewIfNeeded();
  await page.screenshot({ path: join(evidence, `conversation-${phase}.png`) });
  // A complete plan depends on the reviewed REAPER/Music packages. Do not call
  // their absence a passed Set up demo; the screenshot still records continuity.
  expect(offer.plan, 'reviewed integration packages must make Set up reachable').toBeTruthy();
  if (phase === 'reopen') {
    const button = setupButton;
    await expect(button).toBeEnabled();
    await button.click();
    await card.getByRole('button', { name: 'Use automatic placement', exact: true }).click();
    const confirm = card.locator('[data-placement-action="confirm"]');
    await expect(confirm).toBeEnabled();
    const started = page.waitForResponse(
      response => response.url().endsWith('/setup') && response.request().method() === 'POST'
    );
    await confirm.click();
    const response = await started;
    expect(response.ok(), await response.text()).toBeTruthy();
    await expect
      .poll(async () => {
        const current = await api('GET', `/api/home-assistant/conversations/${state.conversation}`);
        return current.folder_reviews[state.offer]?.setup?.status;
      })
      .toBe('stopped');
    const statusButton = card.getByRole('button', { name: 'Read saved status', exact: true });
    if (await statusButton.count()) await statusButton.click();
    await page.screenshot({ path: join(evidence, 'conversation-run-started.png') });
  }
});

test('re-picking the renamed original keeps its review, name, brief and stopped run', async ({
  page,
  request
}) => {
  test.skip(!sandbox || phase !== 'repick', 'requires the verified demo and explicit repick phase');
  test.setTimeout(120_000);
  page.setDefaultTimeout(20_000);
  await page.setViewportSize({ width: 1440, height: 1000 });
  const root = await checkedSandbox(request);
  const stateFile = join(root, '.folder-review-continuity-state.json');
  const state = JSON.parse(await readFile(stateFile, 'utf8'));
  const get = async (path: string) => {
    const response = await request.get(path);
    expect(response.ok(), await response.text()).toBe(true);
    return response.json();
  };
  const conversationURL = `/api/home-assistant/conversations/${encodeURIComponent(state.conversation)}`;
  const before = await get(conversationURL);
  const original = before.folder_reviews[state.offer];
  expect(original.id).toBe(state.offer);
  let pickedPath = state.folderPath || join(root, 'Albums', 'Album-5');
  expect((await realpath(pickedPath)).startsWith(`${root}/Albums/`)).toBe(true);
  if (!original.needs_pick) {
    const target = join(
      root,
      'Albums',
      pickedPath.endsWith('/Album-5') ? 'Album-5 renamed' : 'Album-5'
    );
    expect(
      await stat(target)
        .then(() => true)
        .catch(error => {
          expect(error.code).toBe('ENOENT');
          return false;
        }),
      'never replace an existing fixture'
    ).toBe(false);
    await rename(pickedPath, target);
    pickedPath = target;
    await writeFile(stateFile, JSON.stringify({ ...state, folderPath: pickedPath }), {
      mode: 0o600
    });
  } else {
    expect(original.needs_pick_reason, 'only resume an interrupted rename demo').toBe('moved');
  }
  await page.goto('/');
  await page.waitForFunction(() => (window as any).PersonalAssistantPanel?._state.view.available);
  if (!(await page.locator('#personalAssistantPanel').isVisible()))
    await page.locator('#personalAssistantLauncher').click();
  await chooseConversation(page, '#personalAssistantConversationContinue');
  await page.locator(`button[data-conversation-id="${state.conversation}"]`).click();
  const card = page.locator('#personalAssistantFolderOffer');
  await expect(card).toContainText('has moved or been renamed');
  await page.screenshot({ path: join(evidence, 'conversation-moved.png') });
  let reviewCalls = 0;
  page.on('request', req => {
    if (req.method() === 'POST' && req.url().endsWith('/folder-context/review')) reviewCalls++;
  });
  await card.getByRole('button', { name: 'Pick it again', exact: true }).click();
  const chooser = page.locator('#personalAssistantFolderChoices');
  await chooser.getByLabel('or paste a folder path').fill(pickedPath);
  const selected = page.waitForResponse(
    response =>
      response.url().endsWith('/folder-context/select') && response.request().method() === 'POST'
  );
  await chooser.getByRole('button', { name: 'Use this path', exact: true }).click();
  const response = await selected;
  expect(response.ok(), await response.text()).toBe(true);
  expect((await response.json()).resumed_review).toBeTruthy();
  await expect(card.getByRole('button', { name: 'Pick it again', exact: true })).toHaveCount(0);
  await expect(card.getByRole('button', { name: 'Continue setup', exact: true })).toBeVisible();
  const after = await get(conversationURL);
  const recovered = after.folder_reviews[state.offer];
  expect(after.folder_context.offer_id).toBe(state.offer);
  expect(recovered.needs_pick).not.toBe(true);
  expect(recovered.subject.name).toBe(original.subject.name);
  expect(recovered.scanned_at).toBe(original.scanned_at);
  expect(recovered.setup.brief).toBe(original.setup.brief);
  expect(recovered.setup.lines).toEqual(original.setup.lines);
  expect(after.messages.length).toBe(before.messages.length);
  expect(reviewCalls).toBe(0);
  const digest = await get('/api/personal-assistant/folder-digest');
  expect(digest.folder_digest.offer.id).toBe(state.offer);
  expect(digest.folder_digest.offer.setup.brief).toBe(original.setup.brief);
  await expect(page.locator('#personalAssistantFolderReviewStatus')).not.toContainText(
    'has moved or been renamed'
  );
  await page.screenshot({ path: join(evidence, 'conversation-repicked.png') });
});

test('a Home-owned review keeps its folder after restart', async ({ page, request }) => {
  test.skip(
    !sandbox || !['home-seed', 'home-reopen'].includes(phase || ''),
    'requires an explicit isolated Home phase'
  );
  test.setTimeout(120_000);
  page.setDefaultTimeout(20_000);
  await page.setViewportSize({ width: 1440, height: 1000 });
  const root = await checkedSandbox(request);
  const stateFile = join(root, '.folder-review-owners-state.json');
  const state = await readFile(stateFile, 'utf8')
    .then(JSON.parse)
    .catch(error => {
      if (error.code === 'ENOENT') return {};
      throw error;
    });
  const api = async (method: string, path: string, data?: unknown) => {
    const response = await request.fetch(path, { method, data });
    expect(response.ok(), `${path}: ${await response.text()}`).toBe(true);
    return response.json();
  };
  if (phase === 'home-seed') {
    // Seed an ordinary Home scan through its public, trusted selection API.
    // Adjust/continuation below use the actual confirmation UI, not a store edit.
    const selected = await api('POST', '/api/folder-picker/select-path', {
      path: join(root, 'Albums', 'Album-1')
    });
    const scanned = await api('POST', '/api/personal-assistant/folder-digest/scan', {
      selection_token: selected.selection_token
    });
    state.homeOffer = scanned.offer.id;
    expect(scanned.offer.conversation_id).toBeFalsy();
    await writeFile(stateFile, JSON.stringify(state), { mode: 0o600 });
  }
  expect(state.homeOffer).toBeTruthy();
  await page.goto('/');
  await page.waitForFunction(() => (window as any).PersonalAssistantPanel?._state.view.available);
  if (!(await page.locator('#personalAssistantPanel').isVisible()))
    await page.locator('#personalAssistantLauncher').click();
  const card = page.locator('#personalAssistantFolderOffer');
  await expect(card).toHaveAttribute('data-status', /pending|awaiting_outcome/);
  const strip = page.locator('#personalAssistantTodayStripToggle');
  if ((await strip.getAttribute('aria-expanded')) === 'false') await strip.click();
  await expect(card).toBeVisible();
  await expect(card).toContainText('Album-1');
  await expect(card.getByRole('button', { name: 'Pick it again', exact: true })).toHaveCount(0);
  await revealFolderSetupDetails(card);
  if (phase === 'home-seed') {
    await card.getByRole('button', { name: /^Adjust(?:…)?$/ }).click();
    const modal = page.locator('#specialistSetupJourneyModal');
    await expect(modal).toBeVisible();
    await page.locator('#specialistSetupJourneyClose').click();
    await expect(modal).toBeHidden();
  }
  const digest = await api(
    'GET',
    `/api/personal-assistant/folder-digest?offer_id=${encodeURIComponent(state.homeOffer)}`
  );
  expect(digest.folder_digest.offer.id).toBe(state.homeOffer);
  expect(digest.folder_digest.offer.conversation_id).toBeFalsy();
  expect(digest.folder_digest.offer.needs_pick).not.toBe(true);
  await expect(card.getByRole('button', { name: 'Continue setup', exact: true })).toBeVisible();
  await card.scrollIntoViewIfNeeded();
  await page.setViewportSize({ width: 1440, height: 1200 });
  const details = card.locator('#personalAssistantFolderDetails');
  if ((await details.getAttribute('open')) !== null)
    await details.locator('summary').first().click();
  await card.scrollIntoViewIfNeeded();
  await page.screenshot({ path: join(evidence, `${phase}.png`) });
});

test('the build pane reopens its own folder and name after restart', async ({ page, request }) => {
  test.skip(
    !sandbox || !['build-seed', 'build-reopen'].includes(phase || ''),
    'requires an explicit isolated build phase'
  );
  // The caller must start the verified demo with the existing no-inference
  // recipe: invented OpenAI credential and OPENAI_BASE_URL=http://127.0.0.1:1/v1.
  expect(process.env.ORI_FOLDER_REVIEW_CONTINUITY_OFFLINE_MODEL).toBe('1');
  test.setTimeout(120_000);
  page.setDefaultTimeout(20_000);
  await page.setViewportSize({ width: 1440, height: 1100 });
  const root = await checkedSandbox(request);
  const stateFile = join(root, '.folder-review-owners-state.json');
  const state = JSON.parse(await readFile(stateFile, 'utf8'));
  let modelTurns = 0;
  await page.route('**/api/workspaces/build-sessions/*/turns', async route => {
    modelTurns++;
    await route.abort(); // never send an accidental inference request
  });
  const api = async (path: string) => {
    const response = await request.get(path);
    expect(response.ok(), await response.text()).toBe(true);
    return response.json();
  };
  if (phase === 'build-seed') {
    // Only configuration, through its real UI. The endpoint cannot infer.
    await page.goto('/settings#system-model');
    await page.locator('#systemModelProvider').selectOption('openai');
    await page.locator('#systemModelModel').selectOption('gpt-5-mini');
    await page.locator('#saveSystemModelBtn').click();
    await expect(page.locator('#systemModelStatusText')).toHaveText('Default model configured');
  }
  await page.goto('/');
  const opening = page.waitForResponse(
    response =>
      response.url().endsWith('/api/workspaces/build-sessions') &&
      response.request().method() === 'POST'
  );
  await page.locator('#cockpitCreateWorkspaceBtn').click();
  const opened = await opening;
  expect(opened.ok(), await opened.text()).toBe(true);
  const initial = await opened.json();
  const pane = page.locator('#workspaceBuildPane');
  await expect(pane).toBeVisible();
  if (initial.resumed) await pane.getByRole('button', { name: 'Resume', exact: true }).click();
  if (phase === 'build-seed') {
    if (initial.session.folder) await page.locator('#workspaceBuildFolderChange').click();
    else await page.locator('[data-build-control="add-folder"]').click();
    await page.locator('#workspaceBuildFolderPath').fill(join(root, 'Albums', 'Album-2'));
    const attaching = page.waitForResponse(
      response =>
        response.url().endsWith(`/build-sessions/${initial.session.id}/folder`) &&
        response.request().method() === 'POST'
    );
    await page.locator('[data-build-control="folder-path"]').click();
    const attached = await attaching;
    expect(attached.ok(), await attached.text()).toBe(true);
    const body = await attached.json();
    state.build = body.session.id;
    state.buildObservation = body.session.folder.observation.id;
    state.buildName = 'Kept Album-2 review';
    await expect(page.locator('#workspaceBuildFolderReview')).toBeVisible();
    await page.locator('#workspaceBuildPlanName').fill(state.buildName);
    await page.locator('#workspaceBuildPlanName').press('Tab');
    await writeFile(stateFile, JSON.stringify(state), { mode: 0o600 });
  } else {
    expect(initial.session.id).toBe(state.build);
    expect(initial.session.folder.observation.id).toBe(state.buildObservation);
    expect(initial.session.draft.name).toBe(state.buildName);
  }
  const review = page.locator('#workspaceBuildFolderReview');
  await expect(review).toBeVisible();
  await expect(page.locator('#workspaceBuildPlanName')).toHaveValue(state.buildName);
  await expect(page.locator('#workspaceBuildFolderIdentity')).toContainText('Album-2');
  await expect(review).not.toContainText(/expired|Pick it again|no longer has/);
  await page.screenshot({ path: join(evidence, `${phase}.png`) });
  // Closing pauses and flushes the real draft; it does not discard the review.
  await page
    .locator('#workspaceBuildFolderReviewFooter')
    .getByRole('button', { name: 'Cancel', exact: true })
    .click();
  await expect(page.locator('#addFolderModal')).toBeHidden();
  const saved = await api(`/api/workspaces/build-sessions/${state.build}`);
  expect(saved.session.draft.name).toBe(state.buildName);
  expect(saved.session.folder.observation.id).toBe(state.buildObservation);
  expect(saved.session.folder_expires_at).toBeUndefined();
  expect(saved.session.folder_path).toBeUndefined();
  expect(saved.session.folder_needs_pick_reason).toBeUndefined();
  expect(JSON.stringify(saved)).not.toContain(join(root, 'Albums'));
  expect(modelTurns).toBe(0);
});
