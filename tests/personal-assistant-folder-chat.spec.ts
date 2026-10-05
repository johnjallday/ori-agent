import { test, expect, type APIRequestContext, type Page } from '@playwright/test';

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
};

async function installFixture(page: Page): Promise<Fixture> {
  const fixture: Fixture = {
    requests: [],
    mode: 'answer',
    scan: 'success',
    messages: [],
    revision: '',
    observation: null
  };
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
    const observation = observed(body.chip === 'desktop' ? 'Desktop' : 'Documents');
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
      ? observed(body.folder_context.selection_id.endsWith('Desktop') ? 'Desktop' : 'Documents')
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
        ...(observation ? { folder_context: { revision, observation } } : {})
      }
    });
  });
  await page.route('**/api/home-assistant/conversations/folder-chat-fixture', route =>
    route.fulfill({
      json: {
        conversation: { id: 'folder-chat-fixture', title: 'Folder discussion' },
        messages: fixture.messages,
        saved: [],
        folder_context: { revision: fixture.revision, observation: fixture.observation }
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

test.beforeAll(async ({ request }) => {
  await ensureAssistant(request);
});

test('browser fixture: Send carries exact references, followups deduplicate findings, reload restores one thread', async ({
  page
}) => {
  const fixture = await installFixture(page);
  await open(page);
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
  await page.locator('#personalAssistantLauncher').click();
  await expect(page.locator('#personalAssistantActiveFolderName')).toHaveText('Documents');
  await expect(page.locator('#homeAssistantConversation [data-folder-event-id]')).toHaveCount(1);
  await expect(page.locator('#homeAssistantConversation [data-message-id]')).toHaveCount(4);
  await page.locator('#personalAssistantInput').fill('Keep this draft');
  await page.locator('#personalAssistantConversationNew').click();
  await expect(page.locator('#personalAssistantActiveFolder')).toBeHidden();
  await expect(page.locator('#personalAssistantInput')).toHaveValue('Keep this draft');
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
