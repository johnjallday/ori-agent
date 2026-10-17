import { test, expect, type APIRequestContext, type Page } from '@playwright/test';

// Browser controller fixtures for the composer's Add link. Hire/HQ use real
// host routes; link selection, Route, Ask and the build requests are explicit
// fixtures. The provider boundary, the held store and the prompt are covered
// by internal/agenthttp/personal_assistant_link_context_test.go, and the real
// hand-off into Create Workspace by the live demo.
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
        request_id: 'link-chat-hire',
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
      request_id: 'link-chat-hq',
      if_version: assistant.state_version,
      name: 'My HQ',
      timezone: 'UTC'
    }
  });
  expect(hq.status(), await hq.text()).toBe(201);
}

const repoLink = {
  version: 1,
  id: 'link-1',
  url: 'https://github.com/golang/go',
  host: 'github.com',
  kind: 'github_repo',
  title: 'golang/go',
  description: 'The Go programming language',
  readable: true,
  fetched_at: '2026-10-17T09:00:00Z',
  repo: {
    owner: 'golang',
    name: 'go',
    full_name: 'golang/go',
    language: 'Go',
    open_issues: 10354,
    reachable: 'yes'
  }
};

type Fixture = {
  requests: Record<string, any>[];
  ask: 'answer' | 'lost';
};

async function installFixture(page: Page): Promise<Fixture> {
  const fixture: Fixture = { requests: [], ask: 'answer' };
  await page.route('**/api/home-assistant/link-context/availability', route =>
    route.fulfill({ json: { available: true } })
  );
  await page.route('**/api/home-assistant/link-context/select', route => {
    const body = route.request().postDataJSON();
    fixture.requests.push({ stage: 'select', ...body });
    if (String(body.url).includes('127.0.0.1'))
      return route.fulfill({
        status: 422,
        json: {
          error:
            'That address can’t be used here. Only public http or https links without a token or key in them are accepted.',
          code: 'link'
        }
      });
    return route.fulfill({ json: { link_id: 'link-1', observation: repoLink } });
  });
  await page.route('**/api/home-assistant/link-context/detach', route => {
    fixture.requests.push({ stage: 'detach', ...route.request().postDataJSON() });
    return route.fulfill({ json: { detached: true } });
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
    if (fixture.ask === 'lost')
      return route.fulfill({
        json: {
          response: 'The attached link is no longer held. Nothing was sent.',
          intent: 'assistant_conversation',
          conversation: {
            id: body.conversation?.id || '',
            stored: false,
            error: 'link_context_unavailable'
          }
        }
      });
    return route.fulfill({
      json: {
        response: 'That is the Go repository: the language itself.',
        intent: 'assistant_conversation',
        conversation: {
          id: 'link-chat-fixture',
          title: 'A link',
          stored: true,
          user_message_id: 'u-1',
          assistant_message_id: 'a-1'
        },
        link_context: { link_id: 'link-1', observation: repoLink },
        actions: [
          {
            id: 'build-from-link',
            type: 'workspace_build_from_link',
            label: 'Set up a workspace from this link',
            arguments: { url: 'https://github.com/golang/go' }
          }
        ]
      }
    });
  });
  await page.route('**/api/home-assistant/conversations/link-chat-fixture', route =>
    route.fulfill({
      json: {
        conversation: { id: 'link-chat-fixture', title: 'A link' },
        messages: [
          { id: 'u-1', role: 'user', content: 'Tell me about this link.' },
          {
            id: 'a-1',
            role: 'assistant',
            content: 'That is the Go repository: the language itself.'
          }
        ],
        saved: []
      }
    })
  );
  await page.route('**/api/workspaces/build-sessions', route => {
    if (route.request().method() !== 'POST') return route.continue();
    fixture.requests.push({ stage: 'build', ...route.request().postDataJSON() });
    return route.fulfill({
      status: 201,
      json: { session: { id: 'build-1', version: 3, status: 'open', transcript: [] } }
    });
  });
  await page.route('**/api/workspaces/build-sessions/build-1/link', route => {
    fixture.requests.push({ stage: 'build-link', ...route.request().postDataJSON() });
    return route.fulfill({ json: { session: { id: 'build-1', version: 4, status: 'open' } } });
  });
  return fixture;
}

async function open(page: Page) {
  await page.goto('/');
  await expect(page.locator('#personalAssistantLauncher')).toBeVisible();
  await page.locator('#personalAssistantLauncher').click();
  await expect(page.locator('#personalAssistantLinkChip')).toBeVisible();
}

async function attach(page: Page, url: string) {
  await page.locator('#personalAssistantLinkChip').click();
  await expect(page.locator('#personalAssistantLinkField')).toBeVisible();
  await expect(page.locator('#personalAssistantLinkInput')).toBeFocused();
  await page.locator('#personalAssistantLinkInput').fill(url);
  await page.locator('#personalAssistantLinkInput').press('Enter');
}

test('a link attached in the composer rides the turn and its reply offers the build', async ({
  page,
  request
}) => {
  await ensureAssistant(request);
  const fixture = await installFixture(page);
  await open(page);
  await expect(page.locator('#personalAssistantLinkDisclosure')).toContainText(
    'title and description once'
  );
  await attach(page, 'https://github.com/golang/go');
  await expect(page.locator('#personalAssistantActiveLinkName')).toHaveText('golang/go');
  await expect(page.locator('#personalAssistantLinkSummary')).toHaveText(
    'GitHub repository · Go · 10,354 open issues'
  );
  await expect(page.locator('#personalAssistantLinkField')).toBeHidden();
  await expect(page.locator('#personalAssistantLinkSendHint')).toBeVisible();
  await expect(page.locator('#personalAssistantRemoveLink')).toHaveAttribute(
    'aria-label',
    'Remove link golang/go'
  );

  // An empty Send asks about the link; the turn names the held link only.
  await page.locator('#personalAssistantInput').fill('');
  await page.locator('#personalAssistantSend').click();
  await expect(page.getByText('That is the Go repository: the language itself.')).toBeVisible();
  const route = fixture.requests.find(item => item.stage === 'route');
  const ask = fixture.requests.find(item => item.stage === 'ask');
  expect(route?.link_context).toEqual({ link_id: 'link-1' });
  expect(route?.folder_context).toBeUndefined();
  expect(ask?.prompt).toBe('Tell me about this link.');
  expect(ask?.link_context).toEqual({ link_id: 'link-1' });

  // The build hand-off: start a build, attach the same address, open it.
  await page.evaluate(() => {
    const manager = (window as any).sessionManager;
    (window as any).__opened = [];
    manager.showAddWorkspaceModal = (options: unknown) => (window as any).__opened.push(options);
  });
  await page.getByRole('button', { name: 'Set up a workspace from this link' }).click();
  await expect.poll(() => page.evaluate(() => (window as any).__opened.length)).toBe(1);
  expect(await page.evaluate(() => (window as any).__opened[0])).toEqual({
    entryPoint: 'personal_assistant_ask',
    buildResume: true
  });
  expect(fixture.requests.filter(item => item.stage === 'build')).toEqual([
    { stage: 'build', entry_point: 'personal_assistant_ask' }
  ]);
  expect(fixture.requests.filter(item => item.stage === 'build-link')).toEqual([
    { stage: 'build-link', url: 'https://github.com/golang/go', version: 3 }
  ]);
});

test('a refused address keeps what was typed, and Escape closes only the field', async ({
  page,
  request
}) => {
  await ensureAssistant(request);
  await installFixture(page);
  await open(page);
  await attach(page, 'http://127.0.0.1:8931/');
  await expect(page.locator('#personalAssistantLinkStatus')).toHaveText(
    'That address can’t be used here. Only public http or https links without a token or key in them are accepted.'
  );
  await expect(page.locator('#personalAssistantLinkInput')).toHaveValue('http://127.0.0.1:8931/');
  await expect(page.locator('#personalAssistantActiveLink')).toBeHidden();
  await page.locator('#personalAssistantLinkInput').press('Escape');
  await expect(page.locator('#personalAssistantLinkField')).toBeHidden();
  await expect(page.locator('#personalAssistantPanel')).toBeVisible();
  await expect(page.locator('#personalAssistantLinkChip')).toBeFocused();
});

test('a link the server let go sends nothing and gives the message back', async ({
  page,
  request
}) => {
  await ensureAssistant(request);
  const fixture = await installFixture(page);
  fixture.ask = 'lost';
  await open(page);
  await attach(page, 'https://github.com/golang/go');
  await expect(page.locator('#personalAssistantActiveLink')).toBeVisible();
  await page.locator('#personalAssistantInput').fill('what is this repo?');
  await page.locator('#personalAssistantSend').click();
  await expect(page.locator('#personalAssistantInput')).toHaveValue('what is this repo?');
  await expect(page.locator('#personalAssistantActiveLink')).toBeHidden();
  await expect(page.locator('#personalAssistantLinkStatus')).toContainText('Attach it again');
});

test('removing the link detaches it, and a new conversation starts without one', async ({
  page,
  request
}) => {
  await ensureAssistant(request);
  const fixture = await installFixture(page);
  await open(page);
  await attach(page, 'https://github.com/golang/go');
  await page.locator('#personalAssistantRemoveLink').click();
  await expect(page.locator('#personalAssistantActiveLink')).toBeHidden();
  await expect(page.locator('#personalAssistantLinkChip')).toBeFocused();
  expect(fixture.requests.filter(item => item.stage === 'detach')).toEqual([
    { stage: 'detach', link_id: 'link-1' }
  ]);

  await attach(page, 'https://github.com/golang/go');
  await expect(page.locator('#personalAssistantActiveLink')).toBeVisible();
  await page.evaluate(() => (window as any).PersonalAssistantLink.reset());
  await expect(page.locator('#personalAssistantActiveLink')).toBeHidden();
  expect(fixture.requests.filter(item => item.stage === 'detach')).toHaveLength(2);
});
