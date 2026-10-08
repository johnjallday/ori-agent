import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import { mkdir, writeFile, readFile } from 'node:fs/promises';
import { join, basename } from 'node:path';

// Browser controller/ordering fixtures, not proof of model grounding. Hire/HQ
// use real host routes. Folder selection/Route/Ask/history below are explicit
// browser fixtures; real provider-boundary and canonical storage assertions live
// in internal/server/personal_assistant_folder_turn_test.go. The sandbox demo
// additionally drives the actual host via a controlled local HTTP provider.
test.describe.configure({ mode: 'serial' });

async function ensureAssistant(request: APIRequestContext) {
  await request.post('/api/onboarding/skip');
  let assistant = (await (await request.get('/api/personal-assistant')).json()).personal_assistant;
  if (['active', 'paused'].includes(assistant.state)) return;
  const root = await request.post('/api/settings/workspace-root', { data: { workspace_root: '' } });
  expect(root.ok()).toBeTruthy();
  if (assistant.state !== 'needs_hq') {
    const hire = await request.post('/api/personal-assistant/hire', {
      data: {
        request_id: 'folder-chat-hire',
        if_version: assistant.state_version || 0,
        display_name: 'Atlas',
        mandate: 'Help me plan.',
        focus_areas: ['plan_my_day']
      }
    });
    expect(hire.status(), await hire.text()).toBe(201);
    assistant = (await hire.json()).personal_assistant;
  }
  const hq = await request.post('/api/personal-assistant/hq', {
    data: {
      request_id: 'folder-chat-hq',
      if_version: assistant.state_version,
      name: 'My HQ',
      timezone: 'UTC'
    }
  });
  expect(hq.status(), await hq.text()).toBe(201);
}

const observed = (folder: string) => ({
  version: 1,
  id: `selection-${folder}`,
  folder,
  scanned_at: '2026-10-05T10:00:00Z',
  files: 3,
  entries: 5,
  kinds: [{ name: '.md', count: 3 }],
  projects: [{ id: 'candidate-0', name: folder, files: 3 }],
  coverage: { max_depth: 3, max_entries: 5000, budget_seconds: 3, partial: false }
});

type Fixture = {
  requests: Record<string, any>[];
  mode: 'answer' | 'unavailable' | 'network';
  scan: 'success' | 'cancel' | 'error' | 'delay';
  releaseScan?: () => Promise<void>;
  messages: Record<string, any>[];
  revision: string;
  observation: ReturnType<typeof observed> | null;
  multi: boolean;
  reviewable?: string[];
};

async function installFixture(page: Page): Promise<Fixture> {
  const fixture: Fixture = {
    requests: [],
    mode: 'answer',
    scan: 'success',
    messages: [],
    revision: '',
    observation: null,
    multi: false
  };
  const observationFor = (folder: string) => ({
    ...observed(folder),
    ...(fixture.multi
      ? {
          projects: [
            { id: 'candidate-0', name: folder, files: 3, root: true },
            { id: 'candidate-1', name: 'Project A', files: 1 },
            { id: 'candidate-2', name: 'Project B', files: 1 }
          ]
        }
      : {})
  });
  const suggestion = () =>
    fixture.observation
      ? {
          conversation_id: 'folder-chat-fixture',
          revision: fixture.revision,
          observation_id: fixture.observation.id,
          message_id: fixture.messages.at(-1)?.id,
          options: fixture.observation.projects
            .filter(project => !fixture.reviewable || fixture.reviewable.includes(project.id))
            .map(project => ({
              candidate_id: project.id,
              workspace_type: 'Blank workspace'
            }))
        }
      : null;
  await page.route('**/api/home-assistant/folder-context/choices', route =>
    route.fulfill({
      json: {
        chips: [
          { id: 'documents', label: 'Documents' },
          { id: 'desktop', label: 'Desktop' }
        ],
        picker_available: true
      }
    })
  );
  await page.route('**/api/home-assistant/folder-context/select', async route => {
    const body = route.request().postDataJSON();
    if (fixture.scan === 'cancel') return route.fulfill({ json: { cancelled: true } });
    if (fixture.scan === 'error')
      return route.fulfill({
        status: 409,
        json: { message: 'Fixture access denied. Previous folder unchanged.' }
      });
    const observation = observationFor(body.chip === 'desktop' ? 'Desktop' : 'Documents');
    const reply = () => route.fulfill({ json: { observation, revision: fixture.revision } });
    if (fixture.scan === 'delay') {
      fixture.releaseScan = reply;
      return;
    }
    await reply();
  });
  await page.route('**/api/home-assistant/folder-context/detach', route => {
    fixture.observation = null;
    fixture.revision += '-detached';
    return route.fulfill({ json: { revision: fixture.revision } });
  });
  await page.route('**/api/home-assistant/route', route => {
    fixture.requests.push({ stage: 'route', ...route.request().postDataJSON() });
    return route.fulfill({
      json: {
        intent: 'assistant_conversation',
        route_mode: 'home_inline',
        target_surface: 'current'
      }
    });
  });
  await page.route('**/api/home-assistant/ask', route => {
    const body = route.request().postDataJSON();
    fixture.requests.push({ stage: 'ask', ...body });
    if (fixture.mode === 'network') return route.abort('failed');
    if (fixture.mode === 'unavailable')
      return route.fulfill({
        json: {
          response: 'The configured model is unavailable.',
          model_unavailable: true,
          conversation: { id: body.conversation?.id || '', stored: false }
        }
      });
    const number = fixture.messages.length;
    const observation = body.folder_context
      ? observationFor(
          body.folder_context.selection_id.endsWith('Desktop') ? 'Desktop' : 'Documents'
        )
      : null;
    const revision = `event-${number}`;
    if (observation)
      fixture.messages.push({
        id: revision,
        role: 'folder_context',
        folder_context: { version: 1, observation }
      });
    fixture.messages.push(
      { id: `u-${number}`, role: 'user', content: body.prompt },
      {
        id: `a-${number}`,
        role: 'assistant',
        content:
          'Fixture reply: discuss the observed structure and your goal. File contents have not been read.'
      }
    );
    fixture.revision = revision;
    fixture.observation = observation;
    return route.fulfill({
      json: {
        response: fixture.messages.at(-1)?.content,
        intent: 'assistant_conversation',
        conversation: {
          id: 'folder-chat-fixture',
          title: 'Folder discussion',
          stored: true,
          user_message_id: `u-${number}`,
          assistant_message_id: `a-${number}`
        },
        ...(observation
          ? { folder_context: { revision, observation }, folder_setup_suggestion: suggestion() }
          : {})
      }
    });
  });
  await page.route('**/api/home-assistant/conversations/folder-chat-fixture', route =>
    route.fulfill({
      json: {
        conversation: { id: 'folder-chat-fixture', title: 'Folder discussion' },
        messages: fixture.messages,
        saved: [],
        folder_context: { revision: fixture.revision, observation: fixture.observation },
        folder_setup_suggestion: suggestion()
      }
    })
  );
  return fixture;
}

async function open(page: Page, path = '/') {
  await page.goto(path);
  await expect(page.locator('#personalAssistantLauncher')).toBeVisible();
  await page.locator('#personalAssistantLauncher').click();
  await expect(page.locator('#personalAssistantFolderChip')).toBeVisible();
}
// The drawer's open state now survives a reload or navigation in the same tab,
// so only open it when it is closed.
async function reopen(page: Page) {
  await expect(page.locator('#personalAssistantLauncher')).toBeAttached();
  await page.waitForFunction(() => Boolean((window as any).PersonalAssistantFolderContext));
  if (!(await page.locator('#personalAssistantPanel').isVisible()))
    await page.locator('#personalAssistantLauncher').click();
  await expect(page.locator('#personalAssistantFolderChip')).toBeVisible();
}
async function choose(page: Page, name = 'Documents') {
  await page.locator('#personalAssistantFolderChip').click();
  await page
    .locator('#personalAssistantFolderChoices')
    .getByRole('button', { name, exact: true })
    .click();
  await expect(page.locator('#personalAssistantFolderChip')).toBeEnabled();
}
async function say(page: Page, prompt: string) {
  await page.locator('#personalAssistantInput').fill(prompt);
  await page.locator('#personalAssistantSend').click();
  await page.waitForFunction(() => !(window as any).OriAskRouting.getState().busy);
}

test('real host: local review, Keep chatting, adjusted setup and canonical receipt without a model', async ({
  page
}) => {
  const sandbox = process.env.ORI_FOLDER_CHAT_SANDBOX;
  test.skip(
    !sandbox,
    'Run e2e-fresh.sh --sandbox-env ORI_FOLDER_CHAT_SANDBOX for real disposable-folder coverage'
  );
  expect(basename(sandbox!)).toMatch(/^ori-e2e\./);
  const source = join(sandbox!, 'Documents', 'Chosen');
  await mkdir(source, { recursive: true, mode: 0o750 });
  const file = join(source, 'fixture-notes.txt');
  await writeFile(file, 'Source stays unchanged.', { mode: 0o600 });
  // A second folder for the later reviews: Chosen will already be a project.
  await mkdir(join(sandbox!, 'Documents', 'Later'), { recursive: true, mode: 0o750 });
  await writeFile(join(sandbox!, 'Documents', 'Later', 'later.txt'), 'Later.', { mode: 0o600 });
  const posts: string[] = [];
  page.on('request', request => {
    if (request.method() === 'POST') posts.push(new URL(request.url()).pathname);
  });
  await open(page);
  await choose(page);
  await page.getByRole('button', { name: 'Review workspace setup', exact: true }).click();
  await expect(page.locator('#personalAssistantFolderSetupCandidate')).toHaveValue('');
  await page.locator('#personalAssistantFolderSetupCandidate').selectOption({ label: 'Chosen' });
  await page.getByRole('button', { name: 'Review selection', exact: true }).click();
  const card = page.locator('#homeAssistantConversation #personalAssistantFolderOffer');
  await expect(card).toBeVisible();
  await expect(card).toContainText('Chosen');
  await expect(page.locator('[data-folder-discussion]')).toHaveCount(0);
  const evidence = process.env.ORI_FOLDER_RESPONSE_EVIDENCE_DIR;
  if (evidence) {
    await mkdir(evidence, { recursive: true, mode: 0o750 });
    await page
      .locator('#personalAssistantPanel')
      .screenshot({ path: join(evidence, 'real-canonical-optional-review.png') });
  }
  await card.getByRole('button', { name: 'Keep chatting', exact: true }).click();
  await expect(card).toHaveCount(0);
  if (evidence)
    await page
      .locator('#personalAssistantPanel')
      .screenshot({ path: join(evidence, 'real-canonical-review-cancelled.png') });
  expect(posts.some(path => path.endsWith('/decide'))).toBe(false);
  await page.getByRole('button', { name: 'Review workspace setup', exact: true }).click();
  await page.locator('#personalAssistantFolderSetupCandidate').selectOption({ label: 'Chosen' });
  await page.getByRole('button', { name: 'Review selection', exact: true }).click();
  await expect(card).toBeVisible();
  await card.getByRole('button', { name: 'Adjust name', exact: true }).click();
  const workspaceName = `Next step fixture ${test.info().repeatEachIndex}`;
  await card.getByLabel('Workspace name').fill(workspaceName);
  await card.getByRole('button', { name: 'Set up', exact: true }).click();
  await expect(page.locator('#personalAssistantFolderReceipt')).toBeVisible();
  await expect(page.locator('#personalAssistantFolderReceipt')).toContainText(workspaceName);
  const link = card.getByRole('link', { name: /Open/ }).first();
  const href = await link.getAttribute('href');
  expect(href).toMatch(/^\/workspaces\//);
  await page.reload();
  await reopen(page);
  await expect(card.getByRole('link', { name: /Open/ }).first()).toHaveAttribute('href', href!);
  await page.locator('#personalAssistantRemoveFolder').click();
  await expect(
    page.locator('#homeAssistantConversation').getByRole('link', { name: /Open/ }).first()
  ).toHaveAttribute('href', href!);
  await page.goto('/settings');
  await reopen(page);
  await expect(
    page.locator('#homeAssistantConversation').getByRole('link', { name: /Open/ }).first()
  ).toHaveAttribute('href', href!);
  await expect(page.locator('script[src="/js/modules/dashboard.js"]')).toHaveCount(1);
  await expect(page.locator('#personalAssistantFolderOffer')).toHaveCount(1);
  // The folder that just became a project is pointed at from a new conversation,
  // not reviewed a second time.
  await page.locator('#personalAssistantConversationNew').click();
  await choose(page);
  await page.getByRole('button', { name: 'Review workspace setup', exact: true }).click();
  await page.locator('#personalAssistantFolderSetupCandidate').selectOption({ label: 'Chosen' });
  await page.getByRole('button', { name: 'Review selection', exact: true }).click();
  const existing = page.getByRole('region', { name: 'Existing project' });
  await expect(existing).toContainText(`already set up as the project “${workspaceName}”`);
  await expect(existing.getByRole('link', { name: `Open ${workspaceName}` })).toHaveAttribute(
    'href',
    href!
  );
  await expect(card).toHaveCount(0);
  await existing.getByRole('button', { name: 'Keep chatting', exact: true }).click();
  // An orphaned pending review must fail closed without blocking all future
  // reviews. Closing it edits only its owned sidecar, never the missing Session.
  async function nextReview() {
    await page.locator('#personalAssistantConversationNew').click();
    await choose(page);
    await page.getByRole('button', { name: 'Review workspace setup', exact: true }).click();
    await page.locator('#personalAssistantFolderSetupCandidate').selectOption({ label: 'Later' });
    await page.getByRole('button', { name: 'Review selection', exact: true }).click();
    await expect(card).toBeVisible();
  }
  await nextReview();
  const orphan = await page.evaluate(() =>
    (window as any).PersonalAssistantConversation.currentId()
  );
  const removed = await page.request.delete(`/api/sessions/${orphan}`);
  expect(removed.ok(), await removed.text()).toBeTruthy();
  await card.getByRole('button', { name: 'Keep chatting', exact: true }).click();
  await expect(page.locator('#personalAssistantConversationNote')).toContainText(
    'old setup review is closed'
  );
  await expect(card).toContainText('This setup review is closed');
  await expect(card.getByRole('button')).toHaveCount(0);
  await nextReview();
  await card.getByRole('button', { name: 'Keep chatting', exact: true }).click();
  await expect(card).toHaveCount(0);
  expect(posts.filter(path => path.endsWith('/decide'))).toHaveLength(1);
  expect(
    posts.some(path => ['/api/home-assistant/ask', '/api/home-assistant/route'].includes(path))
  ).toBe(false);
  expect(await readFile(file, 'utf8')).toBe('Source stays unchanged.');
});

test.beforeAll(async ({ request }) => {
  await ensureAssistant(request);
});

for (const path of ['/', '/settings']) {
  test(`browser fixture on ${path}: Send carries exact references, followups deduplicate findings, reload restores one thread`, async ({
    page
  }) => {
    const fixture = await installFixture(page);
    await open(page, path);
    await choose(page);
    expect(fixture.requests).toHaveLength(0);
    await expect(page.locator('#personalAssistantFolderPreview')).toContainText('Local preview');
    await expect(page.locator('#personalAssistantFolderSendHint')).toBeVisible();
    await say(page, '');
    let asks = fixture.requests.filter(request => request.stage === 'ask');
    expect(asks).toHaveLength(1);
    expect(asks[0].prompt).toBe('Explore this folder');
    expect(asks[0].folder_context.selection_id).toBe('selection-Documents');
    expect(asks[0].folder_context.draft_id).toBeTruthy();
    expect(Object.keys(asks[0].folder_context).sort()).toEqual([
      'draft_id',
      'revision',
      'selection_id'
    ]);
    await say(page, 'What goal should I focus on?');
    asks = fixture.requests.filter(request => request.stage === 'ask');
    expect(asks[1].conversation.id).toBe('folder-chat-fixture');
    expect(asks[1].folder_context).toEqual({
      selection_id: 'selection-Documents',
      revision: 'event-0'
    });
    await expect(page.locator('#homeAssistantConversation [data-folder-event-id]')).toHaveCount(1);
    await expect(page.locator('#homeAssistantConversation [data-message-id]')).toHaveCount(4);
    const roles = await page
      .locator('#homeAssistantConversation > [data-message-role]')
      .evaluateAll(rows => rows.map(row => (row as HTMLElement).dataset.messageRole));
    expect(roles).toEqual(['folder_context', 'user', 'assistant', 'user', 'assistant']);
    await page.reload();
    await reopen(page);
    await expect(page.locator('#personalAssistantActiveFolderName')).toHaveText('Documents');
    await expect(page.locator('#homeAssistantConversation [data-folder-event-id]')).toHaveCount(1);
    await expect(page.locator('#homeAssistantConversation [data-message-id]')).toHaveCount(4);
    await page.locator('#personalAssistantInput').fill('Keep this draft');
    await page.locator('#personalAssistantConversationNew').click();
    await expect(page.locator('#personalAssistantActiveFolder')).toBeHidden();
    await expect(page.locator('#personalAssistantInput')).toHaveValue('Keep this draft');
  });
}

test('browser fixture: suggested setup follows the reply, restores on reload and requires explicit scope', async ({
  page,
  request
}) => {
  await ensureAssistant(request);
  const fixture = await installFixture(page);
  fixture.multi = true;
  // Even if only the whole-folder setup is available, do not silently choose
  // it when the observation contains multiple scopes.
  fixture.reviewable = ['candidate-0'];
  const reviews: Record<string, any>[] = [];
  await page.route('**/api/home-assistant/folder-context/review', route => {
    reviews.push(route.request().postDataJSON());
    return route.fulfill({
      status: 409,
      json: { message: 'Fixture: selection expired. Pick again.' }
    });
  });
  await open(page, '/settings');
  await choose(page);
  const handoff = page.locator('#homeAssistantConversation [data-folder-setup-suggestion]');
  await expect(handoff).toHaveCount(0);
  await say(page, 'Explore this folder');
  await expect(handoff).toHaveCount(1);
  const requestsBefore = fixture.requests.length;
  const discussion = page.locator('[data-folder-discussion]');
  await expect(discussion).toHaveCount(1);
  const chooseDiscussion = discussion.getByRole('button', { name: 'Choose a folder…' });
  await chooseDiscussion.click();
  const discussionCandidate = page.locator('#personalAssistantFolderDiscussionCandidate');
  await expect(discussionCandidate.locator('option')).toHaveCount(3);
  await expect(discussionCandidate).toContainText('Project A');
  await expect(discussionCandidate).toContainText('Project B');
  await page.keyboard.press('Escape');
  await expect(chooseDiscussion).toBeFocused();
  expect(fixture.requests.length).toBe(requestsBefore);
  await say(page, 'I want to organize the whole collection');
  await expect(handoff).toHaveCount(1);
  await expect(page.locator('[data-message-id="a-0"] [data-folder-setup-suggestion]')).toHaveCount(
    0
  );
  await page.reload();
  await reopen(page);
  await expect(handoff).toHaveCount(1);
  await page.locator('#personalAssistantInput').fill('Keep this draft');
  const button = handoff.getByRole('button', { name: 'Optional: review setup' });
  expect(
    await button.evaluate(element => {
      const row = element.closest('[data-message-id]');
      const bubble = row?.firstElementChild;
      const text = bubble?.firstChild;
      if (!text || !bubble?.contains(element)) return false;
      const range = document.createRange();
      range.selectNode(text);
      return (
        element.getBoundingClientRect().top >= range.getBoundingClientRect().bottom &&
        element.getBoundingClientRect().height >= 44
      );
    })
  ).toBe(true);
  await button.focus();
  await page.keyboard.press('Enter');
  const candidate = page.locator('#personalAssistantFolderSetupCandidate');
  await expect(candidate).toBeFocused();
  await expect(candidate).toHaveValue('');
  expect(reviews).toHaveLength(0);
  await page.keyboard.press('Escape');
  await expect(button).toBeFocused();
  await button.click();
  await candidate.selectOption('candidate-0');
  await page.locator('#personalAssistantFolderSetupReview').click();
  await expect.poll(() => reviews.length).toBe(1);
  expect(reviews[0]).toMatchObject({
    conversation_id: 'folder-chat-fixture',
    revision: fixture.revision,
    selection_id: 'selection-Documents',
    candidate_id: 'candidate-0'
  });
  await expect(page.locator('#personalAssistantContextStatus')).toContainText('selection expired');
  await expect(page.locator('#personalAssistantInput')).toHaveValue('Keep this draft');
  expect(fixture.requests.filter(request => request.stage === 'ask')).toHaveLength(2);
  await choose(page, 'Desktop');
  await expect(handoff).toHaveCount(0);
});

test('browser fixture: non-Home cancel/error/replacement retains drafts and late scan cannot follow New', async ({
  page
}) => {
  const fixture = await installFixture(page);
  await open(page, '/settings');
  await page.locator('#personalAssistantInput').fill('An unsent question');
  await choose(page);
  fixture.scan = 'cancel';
  await choose(page, 'Desktop');
  await expect(page.locator('#personalAssistantActiveFolderName')).toHaveText('Documents');
  fixture.scan = 'error';
  await choose(page, 'Desktop');
  await expect(page.locator('#personalAssistantActiveFolderName')).toHaveText('Documents');
  await expect(page.locator('#personalAssistantInput')).toHaveValue('An unsent question');
  fixture.scan = 'success';
  await choose(page, 'Desktop');
  await expect(page.locator('#personalAssistantActiveFolderName')).toHaveText('Desktop');
  await page.locator('#personalAssistantRemoveFolder').click();
  await expect(page.locator('#personalAssistantActiveFolder')).toBeHidden();
  await expect(page.locator('#personalAssistantFolderChip')).toBeFocused();
  fixture.scan = 'delay';
  await page.locator('#personalAssistantFolderChip').click();
  await page
    .locator('#personalAssistantFolderChoices')
    .getByRole('button', { name: 'Documents', exact: true })
    .click();
  await expect(page.locator('#personalAssistantFolderChip')).toBeDisabled();
  await page.locator('#personalAssistantConversationNew').click();
  await expect.poll(() => Boolean(fixture.releaseScan)).toBe(true);
  await fixture.releaseScan?.();
  await expect(page.locator('#personalAssistantActiveFolder')).toBeHidden();
  await expect(page.locator('#personalAssistantInput')).toHaveValue('An unsent question');
  expect(fixture.requests).toHaveLength(0);
  expect(await page.locator('#personalAssistantFolderChip').count()).toBe(1);
});

for (const theme of ['light', 'dark']) {
  test(`browser fixture: ${theme} keyboard, narrow layout and zoom keep one thread and composer`, async ({
    page
  }) => {
    await page.addInitScript(value => localStorage.setItem('ori-theme', value), theme);
    await page.emulateMedia({ reducedMotion: 'reduce' });
    const fixture = await installFixture(page);
    await open(page, '/settings');
    const chip = page.locator('#personalAssistantFolderChip');
    let releaseChoices: (() => Promise<void>) | undefined;
    await page.route(
      '**/api/home-assistant/folder-context/choices',
      route => {
        releaseChoices = () =>
          route.fulfill({ json: { chips: [{ id: 'documents', label: 'Documents' }] } });
      },
      { times: 1 }
    );
    await chip.focus();
    await chip.press('Enter');
    await expect(page.locator('#personalAssistantFolderChooserCancel')).toBeFocused();
    await page.keyboard.press('Escape');
    await expect(chip).toBeFocused();
    await expect.poll(() => Boolean(releaseChoices)).toBe(true);
    await releaseChoices!();
    await expect(page.locator('#personalAssistantContextChooser')).toBeHidden();
    await expect(chip).toBeFocused();
    await expect(page.locator('#personalAssistantPanel')).toBeVisible();
    await chip.press('Enter');
    await page
      .locator('#personalAssistantFolderChoices')
      .getByRole('button', { name: 'Documents', exact: true })
      .press('Enter');
    await expect(chip).toBeFocused();
    expect(fixture.requests).toHaveLength(0);
    await expect(page.locator('#personalAssistantFolderPreview')).toContainText(
      'Attached folder: contents not read.'
    );
    await page.locator('#personalAssistantRemoveFolder').press('Enter');
    await expect(chip).toBeFocused();
    await choose(page);
    await say(page, 'Discuss this long goal without reading contents. '.repeat(20));
    const actions = page
      .locator('#homeAssistantConversation .personal-assistant-message__toggle')
      .first();
    await actions.press('Enter');
    await page.keyboard.press('Escape');
    await expect(actions).toBeFocused();
    for (const [width, height, zoom] of [
      [1440, 900, 1],
      [390, 844, 1],
      [720, 450, 2]
    ]) {
      // Half the desktop CSS viewport exercises 200%-zoom-equivalent reflow,
      // not an OS/browser zoom automation claim.
      await page.setViewportSize({ width, height });
      const layout = await page.evaluate(() => {
        const input = document.getElementById('personalAssistantInput')!.getBoundingClientRect();
        const panel = document.getElementById('personalAssistantPanel')!;
        const bounds = panel.getBoundingClientRect();
        const log = document.getElementById('homeAssistantConversation')!;
        return {
          inputVisible: input.top >= 0 && input.bottom <= innerHeight + 1,
          // Settings already overflows at phone width with the drawer closed;
          // scope this contract to the changed assistant surface.
          overflow:
            panel.scrollWidth > panel.clientWidth + 1 ||
            bounds.left < -1 ||
            bounds.right > innerWidth + 1,
          nestedScroll: ['auto', 'scroll'].includes(getComputedStyle(log).overflowY),
          mounts: document.querySelectorAll('#homeAssistantConversation').length
        };
      });
      expect(layout, `${theme} ${width}px zoom=${zoom}`).toEqual({
        inputVisible: true,
        overflow: false,
        nestedScroll: false,
        mounts: 1
      });
    }
    for (let i = 0; i < 3; i++) {
      await page.locator('#personalAssistantClose').press('Enter');
      await page.locator('#personalAssistantLauncher').press('Enter');
    }
    await expect(page.locator('#homeAssistantConversation [data-folder-event-id]')).toHaveCount(1);
    await expect(page.locator('#personalAssistantFolderOffer')).toHaveCount(1);
    expect(fixture.requests.filter(request => request.stage === 'ask')).toHaveLength(1);
  });
}

test('browser fixture: model/network failure keeps exact intended folder and never auto-retries', async ({
  page
}) => {
  const fixture = await installFixture(page);
  await open(page);
  await choose(page);
  fixture.mode = 'unavailable';
  await say(page, 'Keep my question');
  await expect(page.locator('#personalAssistantInput')).toHaveValue('Keep my question');
  await expect(page.locator('#personalAssistantActiveFolderName')).toHaveText('Documents');
  await expect(page.locator('#personalAssistantFolderSendHint')).toBeHidden();
  const initial = fixture.requests.find(request => request.stage === 'ask');
  fixture.mode = 'network';
  await say(page, 'Keep my question');
  await expect(page.locator('#personalAssistantInput')).toHaveValue('Keep my question');
  await expect(page.locator('#personalAssistantConversationNote')).toContainText(
    'may have been saved'
  );
  const asks = fixture.requests.filter(request => request.stage === 'ask');
  expect(asks).toHaveLength(2);
  expect(asks[1].folder_context).toEqual(initial?.folder_context);
  await page.waitForTimeout(150);
  expect(fixture.requests.filter(request => request.stage === 'ask')).toHaveLength(2);
});
