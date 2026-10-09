import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import { mkdir, writeFile, readFile } from 'node:fs/promises';
import { join, basename } from 'node:path';
import { folderResponseFixtures } from './fixtures/assistant-folder-response.js';

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
  mode: 'answer' | 'unavailable' | 'network' | 'unsaved' | 'delay';
  releaseReply?: () => void;
  reply?: string;
  scenario?: any;
  authority?: string;
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
    ...(fixture.scenario || {}),
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
  await page.route('**/api/home-assistant/ask', async route => {
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
    if (fixture.mode === 'unsaved')
      return route.fulfill({
        json: {
          response: 'Fixture reply: not saved.',
          conversation: {
            id: body.conversation?.id || '',
            stored: false,
            error: 'folder_context_save_failed'
          }
        }
      });
    if (fixture.mode === 'delay')
      await new Promise<void>(resolve => {
        fixture.releaseReply = resolve;
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
          fixture.reply ||
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
        folder_context: {
          revision: fixture.revision,
          observation: fixture.observation,
          ...(fixture.authority ? { authority: fixture.authority, historical: true } : {})
        },
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

test('browser fixture: current choices survive cancellation but reject stale callbacks, detached/imported and trimmed history', async ({
  page
}) => {
  const fixture = await installFixture(page);
  await open(page);
  await choose(page);
  await say(page, 'Discuss only');
  const strip = page.locator('[data-folder-discussion]');
  const input = page.locator('#personalAssistantInput');
  await expect(strip).toHaveCount(1);
  await page.evaluate(() => {
    (window as any).retiredFolderChoice = document.querySelector('[data-folder-discussion] button');
  });
  fixture.scan = 'cancel';
  await choose(page, 'Desktop');
  await expect(strip).toHaveCount(1);
  await input.fill('');
  await page.evaluate(() => (window as any).retiredFolderChoice.click());
  await expect(input).toHaveValue('');
  const evidence = join(process.cwd(), 'tasks/evidence/assistant-folder-response-ux/group-4');
  await mkdir(evidence, { recursive: true, mode: 0o750 });
  await page
    .locator('#personalAssistantPanel')
    .screenshot({ path: join(evidence, 'browser-fixture-retired-callback-rejected.png') });
  await strip.getByRole('button').first().click();
  await expect(input).not.toHaveValue('');
  await input.fill('');
  fixture.authority = 'lost';
  const before = fixture.requests.length;
  await page.reload();
  await reopen(page);
  await expect(strip.getByRole('button').first()).toHaveText('Discuss saved observations');
  await expect(page.locator('[data-folder-event-id]')).toContainText('historical');
  await expect(page.locator('[data-folder-setup-suggestion]')).toHaveCount(0);
  await page.locator('[data-folder-event-id]').scrollIntoViewIfNeeded();
  await page
    .locator('#personalAssistantPanel')
    .screenshot({ path: join(evidence, 'browser-fixture-lost-historical-snapshot.png') });
  await strip.getByRole('button').first().click();
  expect(fixture.requests.length).toBe(before);
  await page.locator('#personalAssistantSend').click();
  await page.waitForFunction(() => !(window as any).OriAskRouting.getState().busy);
  const latest = fixture.requests.filter(row => row.stage === 'ask').at(-1)!;
  expect(latest.folder_context.historical).toBe(true);
  expect(latest.folder_context.reference).toBeUndefined();
  await page.locator('#personalAssistantRemoveFolder').click();
  await expect(strip).toHaveCount(0);
  await expect(page.locator('[data-message-role="assistant"]').last()).toContainText(
    'Fixture reply'
  );
  fixture.messages.at(-1)!.imported = true;
  await page.reload();
  await reopen(page);
  await expect(strip).toHaveCount(0);
  await expect(page.locator('[data-message-role="assistant"]').last()).toContainText(
    'Fixture reply'
  );
  fixture.messages.at(-1)!.imported = false;
  for (let index = 0; index < 70; index++)
    fixture.messages.push({
      id: `legacy-${index}`,
      role: index % 2 ? 'assistant' : 'user',
      content: 'Legacy prose remains unchanged.'
    });
  await page.reload();
  await reopen(page);
  await expect(strip).toHaveCount(0);
  await expect(page.locator('[data-message-role="assistant"]').last()).toContainText(
    'Legacy prose remains unchanged.'
  );
  await expect(page.locator('#personalAssistantConversationNote')).toContainText('most recent');
  await input.fill('New draft');
  await page.locator('#personalAssistantConversationNew').click();
  await expect(strip).toHaveCount(0);
  await expect(input).toHaveValue('New draft');
});

test('browser fixture: failed save and delayed reply preserve intent and newer typing without duplicate Send', async ({
  page
}) => {
  const fixture = await installFixture(page);
  await open(page);
  await choose(page);
  fixture.mode = 'unsaved';
  await say(page, 'Intended question');
  await expect(page.locator('#personalAssistantInput')).toHaveValue('Intended question');
  await expect(page.locator('#personalAssistantFolderPreview')).toBeVisible();
  await expect(page.locator('[data-folder-discussion]')).toHaveCount(0);
  fixture.mode = 'answer';
  await say(page, 'Discuss only');
  fixture.mode = 'delay';
  await page.locator('#personalAssistantInput').fill('A delayed question');
  await page.locator('#personalAssistantSend').click();
  await expect.poll(() => Boolean(fixture.releaseReply)).toBe(true);
  await expect(page.locator('[data-folder-discussion]')).toHaveCount(0);
  const before = fixture.requests.filter(row => row.stage === 'ask').length;
  await page.locator('#personalAssistantInput').fill('Keep my new 🎼 typing exactly  ');
  await page.locator('#personalAssistantSend').click();
  await page.locator('#personalAssistantConversationNew').click();
  expect(await page.evaluate(() => (window as any).PersonalAssistantConversation.currentId())).toBe(
    'folder-chat-fixture'
  );
  expect(fixture.requests.filter(row => row.stage === 'ask').length).toBe(before);
  fixture.releaseReply!();
  await page.waitForFunction(() => !(window as any).OriAskRouting.getState().busy);
  const whole = page.locator('[data-folder-discussion]').getByRole('button').first();
  await whole.click();
  await whole.click();
  await expect(page.locator('#personalAssistantInput')).toHaveValue(
    'Keep my new 🎼 typing exactly  '
  );
  expect(fixture.requests.filter(row => row.stage === 'ask').length).toBe(before);
});

for (const theme of ['light', 'dark']) {
  test(`browser fixture: ${theme} explorer focus, limits, keyboard and snapshot retirement`, async ({
    page
  }) => {
    await page.addInitScript(value => localStorage.setItem('ori-theme', value), theme);
    const fixture = await installFixture(page);
    fixture.scenario = {
      tree: {
        omitted: 14,
        nodes: Array.from({ length: 10 }, (_, index) => ({
          id: `entry-${index}`,
          name: index === 0 ? '<img onerror=alert(1)> 🎼' : `Topic ${index}`,
          kind: 'file'
        }))
      },
      coverage: {
        max_depth: 3,
        max_entries: 5000,
        budget_seconds: 3,
        partial: true,
        partial_reason: 'entries'
      }
    };
    await open(page, '/settings');
    const input = page.locator('#personalAssistantInput');
    await input.fill('  Preserve exact 🎼 text\n');
    await choose(page);
    const pane = page.locator('#personalAssistantFolderExplorer');
    await expect(pane).toBeVisible();
    await expect(pane).toContainText('Partial snapshot · 10 entries · 14 omitted');
    await expect(pane.locator('img, script')).toHaveCount(0);
    const checks = pane.locator('input[type="checkbox"]');
    await checks.first().focus();
    await page.keyboard.press('Space');
    await expect(checks.first()).toBeChecked();
    for (let index = 1; index < 8; index++) await checks.nth(index).check();
    // This activation is deliberately refused; check() requires success.
    await checks.nth(8).click();
    await expect(checks.nth(8)).not.toBeChecked();
    await expect(page.locator('#personalAssistantExplorerNotice')).toContainText('up to 8');
    await expect(input).toHaveValue('  Preserve exact 🎼 text\n');
    expect(fixture.requests).toHaveLength(0);
    await page.evaluate(() => {
      (window as any).retiredTreeCheck = document.querySelector('[data-tree-focus="entry-0"]');
    });
    fixture.scan = 'cancel';
    await choose(page, 'Desktop');
    await expect(checks.first()).toBeChecked();
    await page.evaluate(() => {
      const check = (window as any).retiredTreeCheck;
      check.checked = false;
      check.dispatchEvent(new Event('change'));
    });
    await expect(checks.first()).toBeChecked();
    fixture.scan = 'success';
    fixture.mode = 'delay';
    await input.fill('Discuss selected metadata');
    await page.locator('#personalAssistantSend').click();
    await expect.poll(() => Boolean(fixture.releaseReply)).toBe(true);
    await checks.first().uncheck();
    await input.fill('  Newer exact text 🎼  ');
    fixture.releaseReply!();
    await page.waitForFunction(() => !(window as any).OriAskRouting.getState().busy);
    await expect(checks.first()).not.toBeChecked();
    await expect(input).toHaveValue('  Newer exact text 🎼  ');
    expect(
      fixture.requests.find(row => row.stage === 'ask')!.folder_context.focus_ids
    ).toHaveLength(8);
    await page.setViewportSize({ width: 390, height: 844 });
    await page.locator('#personalAssistantExplorerTreeTab').click();
    await checks.nth(2).focus();
    await expect(checks.nth(2)).toBeInViewport({ ratio: 1 });
    await page.keyboard.press('Tab');
    await expect(checks.nth(3)).toBeFocused();
    await page.locator('#personalAssistantFolderFocusClear').click();
    await expect(input).toBeFocused();
    await expect(pane.locator('input:checked')).toHaveCount(0);
    await page.locator('#personalAssistantExplorerBack').click();
    await page.locator('#personalAssistantConversationNew').click();
    await expect(pane).toBeHidden();
    await expect(input).toHaveValue('  Newer exact text 🎼  ');
    fixture.scenario = { tree: { nodes: [], omitted: 0 } };
    await choose(page);
    await expect(pane).toContainText('No visible entries recorded');
    await expect(pane.locator('input')).toHaveCount(0);
    await page.locator('#personalAssistantExplorerBack').click();
    fixture.scenario = {
      tree: {
        omitted: 0,
        nodes: [
          { id: 'entry-0', name: 'Same name', kind: 'folder' },
          { id: 'entry-1', name: 'Same name', kind: 'folder' }
        ]
      }
    };
    await choose(page, 'Desktop');
    await expect(pane.locator('input').first()).toBeDisabled();
    await expect(pane.locator('input').last()).toBeDisabled();
    await expect(pane).toContainText('indistinguishable name');
    await page.locator('#personalAssistantExplorerBack').click();
    fixture.scenario = { tree: null };
    await choose(page);
    await expect(pane).toBeHidden();
    await expect(page.locator('#personalAssistantExploreAttachedFolder')).toBeHidden();
    await page.evaluate(() => (window as any).PersonalAssistantFolderContext.explore());
    await expect(pane).toContainText('Saved folder summaries only');
    await expect(pane.locator('input')).toHaveCount(0);
    await page.locator('#personalAssistantRemoveFolder').click();
    await expect(pane).toBeHidden();
    await expect(input).toHaveValue('  Newer exact text 🎼  ');
  });
}

for (const theme of ['light', 'dark']) {
  test(`browser fixture: ${theme} bounded/hostile matrix, keyboard choices and accessible reflow`, async ({
    page
  }) => {
    await page.addInitScript(value => localStorage.setItem('ori-theme', value), theme);
    await page.emulateMedia({ reducedMotion: 'reduce' });
    const fixture = await installFixture(page);
    fixture.reviewable = [];
    await open(page, '/settings');
    const evidence = join(process.cwd(), 'tasks/evidence/assistant-folder-response-ux/group-4');
    await mkdir(evidence, { recursive: true, mode: 0o750 });
    const accessibility: any[] = [];
    const ax = await page.context().newCDPSession(page);
    for (const scenario of [
      folderResponseFixtures.rootOnly,
      folderResponseFixtures.empty,
      folderResponseFixtures.partial,
      folderResponseFixtures.hostile
    ]) {
      fixture.scenario = scenario;
      fixture.reply =
        scenario === folderResponseFixtures.hostile
          ? 'Full literal prose <b>not HTML</b> 🎼. '.repeat(400).trim()
          : 'Fixture: names suggest a collection. Contents are not known. What matters to you?';
      if (await page.evaluate(() => (window as any).PersonalAssistantConversation.currentId()))
        await page.locator('#personalAssistantConversationNew').click();
      await choose(page);
      await say(page, 'Just discuss');
      const card = page.locator('[data-folder-event-id]');
      const strip = page.locator('[data-folder-discussion]');
      await expect(card.getByRole('heading')).toHaveText(scenario.folder);
      await expect(card).toContainText(/[Bb]ounded look/);
      await expect(card).toContainText('contents not read');
      await expect(card.locator('img, b, script')).toHaveCount(0);
      const tree = await ax.send('Accessibility.getFullAXTree');
      const group = tree.nodes.find(
        node => node.role?.value === 'group' && node.name?.value === 'Folder conversation choices'
      );
      expect(group).toBeTruthy();
      const names = (group?.childIds || [])
        .map(id => tree.nodes.find(node => node.nodeId === id))
        .filter(node => node?.role?.value === 'button')
        .map(node => node!.name?.value);
      expect(names.length).toBeGreaterThan(0);
      expect(names.every(name => typeof name === 'string' && name.length > 0)).toBe(true);
      expect(
        await page
          .locator('[data-message-role="assistant"]')
          .last()
          .evaluate(el => el.firstElementChild?.firstChild?.textContent)
      ).toBe(fixture.reply);
      const details = card.getByText('Scan details', { exact: true });
      await details.focus();
      await details.press('Enter');
      await expect(card).toContainText('counts overlap');
      await details.press('Space');
      const before = fixture.requests.length;
      const whole = strip.getByRole('button').first();
      await whole.focus();
      expect(
        await whole.evaluate(el => {
          const style = getComputedStyle(el);
          return (
            (style.outlineStyle !== 'none' && style.outlineWidth !== '0px') ||
            style.boxShadow !== 'none'
          );
        })
      ).toBe(true);
      const contrasts = await card.evaluate(el => {
        const parse = (value: string) => {
          const numbers = value.match(/[\d.]+/g)!.map(Number);
          return [numbers[0], numbers[1], numbers[2]]
            .map(x => (value.startsWith('color(') ? x * 255 : x))
            .concat(numbers[3] ?? 1);
        };
        const luminance = (channels: number[]) =>
          channels
            .slice(0, 3)
            .map(x => x / 255)
            .map(x => (x <= 0.04045 ? x / 12.92 : ((x + 0.055) / 1.055) ** 2.4))
            .reduce((sum, x, index) => sum + x * [0.2126, 0.7152, 0.0722][index], 0);
        return Array.from(el.querySelectorAll('summary')).map(control => {
          const ancestors: Element[] = [];
          for (let parent: Element | null = control; parent; parent = parent.parentElement)
            ancestors.unshift(parent);
          let bg = [255, 255, 255];
          for (const parent of ancestors) {
            const color = parse(getComputedStyle(parent).backgroundColor);
            bg = bg.map((x, index) => color[index] * color[3] + x * (1 - color[3]));
          }
          const foreground = parse(getComputedStyle(control).color);
          const light = luminance(foreground),
            dark = luminance(bg);
          return {
            label: control.textContent,
            ratio: (Math.max(light, dark) + 0.05) / (Math.min(light, dark) + 0.05)
          };
        });
      });
      expect(contrasts.every(sample => sample.ratio >= 4.5)).toBe(true);
      accessibility.push({
        snapshot: scenario.id,
        axButtonNames: names,
        contrasts,
        focusVisible: true,
        reducedMotion: await page.evaluate(
          () => matchMedia('(prefers-reduced-motion: reduce)').matches
        )
      });
      await whole.press('Enter');
      await expect(page.locator('#personalAssistantInput')).toBeFocused();
      const draft = await page.locator('#personalAssistantInput').inputValue();
      expect(draft).not.toContain(scenario.id);
      expect(draft).toContain(scenario.folder);
      await whole.press('Enter');
      await expect(page.locator('#personalAssistantInput')).toHaveValue(draft);
      expect(fixture.requests.length).toBe(before);
      const children = scenario.projects.filter((row: any) => !row.root);
      await expect(strip.getByRole('button')).toHaveCount(children.length ? 2 : 1);
      if (children.length) {
        const chooseDiscussion = strip.getByRole('button').nth(1);
        await chooseDiscussion.focus();
        await chooseDiscussion.press('Enter');
        const select = page.locator('#personalAssistantFolderDiscussionCandidate');
        await expect(select).toBeFocused();
        await expect(select).toHaveValue('');
        await expect(select.locator('option')).toHaveCount(children.length + 1);
        await expect(
          page.getByRole('combobox', { name: 'Which observed folder would you like to discuss?' })
        ).toBeVisible();
        await page.locator('#personalAssistantFolderDiscussionCancel').press('Enter');
        await expect(chooseDiscussion).toBeFocused();
        await expect(page.locator('#personalAssistantInput')).toHaveValue(draft);
        await chooseDiscussion.press('Enter');
        await page.keyboard.press('Escape');
        await expect(chooseDiscussion).toBeFocused();
        await expect(page.locator('#personalAssistantPanel')).toBeVisible();
      }
      await page.locator('#personalAssistantInput').fill('');
      for (const [width, height, zoom] of [
        [1440, 900, 1],
        [390, 844, 1],
        [780, 1688, 2]
      ]) {
        await page.setViewportSize({ width, height });
        await page.evaluate(value => {
          document.body.style.zoom = String(value);
        }, zoom);
        await whole.focus();
        await expect
          .poll(() =>
            whole.evaluate(el => {
              const active = el.getBoundingClientRect();
              const viewport = document
                .getElementById('personalAssistantScroll')!
                .getBoundingClientRect();
              return active.top >= viewport.top && active.bottom <= viewport.bottom;
            })
          )
          .toBe(true);
        const layout = await page.locator('#personalAssistantPanel').evaluate(panel => {
          const input = document.getElementById('personalAssistantInput')!.getBoundingClientRect();
          const active = document.activeElement!.getBoundingClientRect();
          const bounds = panel.getBoundingClientRect();
          const nested = Array.from(panel.querySelectorAll('*')).filter(
            el =>
              el.id !== 'personalAssistantScroll' &&
              // The pinned editable textarea may scroll long text/placeholder
              // at reflow. It is not a nested transcript or metadata viewport.
              el.id !== 'personalAssistantInput' &&
              el.getClientRects().length > 0 &&
              ['auto', 'scroll'].includes(getComputedStyle(el).overflowY) &&
              el.scrollHeight > el.clientHeight
          );
          return {
            overflow: panel.scrollWidth > panel.clientWidth + 1,
            nested: nested.length,
            inputVisible: input.top >= bounds.top && input.bottom <= bounds.bottom,
            focusedVisible: active.top >= bounds.top && active.bottom <= bounds.bottom,
            targets: Array.from(
              panel.querySelectorAll(
                '[data-folder-discussion] button, [data-folder-event-id] summary'
              )
            ).every(
              el =>
                el.getBoundingClientRect().height >= 44 * Number(document.body.style.zoom || 1) - 1
            )
          };
        });
        const scrollDiagnostics = await page.locator('#personalAssistantPanel').evaluate(panel =>
          Array.from(panel.querySelectorAll('*'))
            .filter(
              el =>
                ['auto', 'scroll'].includes(getComputedStyle(el).overflowY) &&
                el.scrollHeight > el.clientHeight
            )
            .map(el => ({
              id: el.id,
              tag: el.tagName,
              visible: Boolean(el.getClientRects().length),
              scroll: el.scrollHeight,
              client: el.clientHeight
            }))
        );
        expect(layout, JSON.stringify(scrollDiagnostics)).toEqual({
          overflow: false,
          nested: 0,
          inputVisible: true,
          focusedVisible: true,
          targets: true
        });
        await page.locator('#personalAssistantPanel').screenshot({
          path: join(evidence, `${theme}-${scenario.id}-${width}px-zoom-${zoom}.png`)
        });
        if (children.length) {
          const chooseDiscussion = strip.getByRole('button').nth(1);
          await whole.press('Tab');
          await expect(chooseDiscussion).toBeFocused();
          await chooseDiscussion.press('Enter');
          await expect(page.locator('#personalAssistantFolderDiscussionCandidate')).toBeFocused();
          await page.locator('#personalAssistantPanel').screenshot({
            path: join(evidence, `${theme}-${scenario.id}-${width}px-local-chooser.png`)
          });
          await page.keyboard.press('Escape');
          await expect(chooseDiscussion).toBeFocused();
          await expect(page.locator('#personalAssistantInput')).toHaveValue('');
          expect(fixture.requests.length).toBe(before);
        }
      }
      await page
        .locator('#personalAssistantPanel')
        .screenshot({ path: join(evidence, `${theme}-${scenario.id}-reflow.png`) });
      await page.evaluate(() => {
        document.body.style.zoom = '1';
      });
      await page.setViewportSize({ width: 1440, height: 900 });
    }
    await expect(page.locator('#personalAssistantPanelStatus')).toHaveAttribute(
      'aria-live',
      'polite'
    );
    await writeFile(
      join(evidence, `${theme}-accessibility.json`),
      JSON.stringify(accessibility, null, 2),
      { mode: 0o600 }
    );
    await ax.detach();
  });
}
