import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import { basename } from 'node:path';
import { installLocalCdn } from './helpers/offline-cdn';

// The recipient picker (tasks/prd-assistant-recipient-picker.md). On a
// workspace page the assistant drawer talks to that workspace's entry agent
// (its Commander), and the header lets the user switch to the hired Personal
// Assistant and back. Real host, real sessions and a real hire; /api/chat is
// answered in the browser, so no model is needed and what is asserted is the
// drawer's own requests. A hire cannot be undone, so the file runs in order
// against one fresh sandbox:
//   ./scripts/e2e-fresh.sh --sandbox-env ORI_RECIPIENT_PICKER_SANDBOX \
//     tests/personal-assistant-recipient.spec.ts -- --workers=1

test.describe.configure({ mode: 'serial' });

type Seeded = {
  commander: { id: string; slug: string };
  vacant: { id: string; slug: string };
  hq: { id: string; slug: string };
};
let seeded: Seeded;

async function ok(response: Awaited<ReturnType<APIRequestContext['post']>>) {
  expect(response.ok(), `${response.url()} → ${response.status()} ${await response.text()}`).toBe(
    true
  );
  return response.json();
}

async function workspaceRef(request: APIRequestContext, id: string) {
  const workspace = await (await request.get(`/api/workspaces/${id}`)).json();
  return { id, slug: String(workspace.folder_slug || workspace.slug || id) };
}

async function createWorkspace(request: APIRequestContext, data: Record<string, string>) {
  const created = await ok(await request.post('/api/workspaces', { data }));
  const id = String(created.id || created.workspace?.id || created.folder?.id || '');
  expect(id, JSON.stringify(created)).not.toBe('');
  return workspaceRef(request, id);
}

test.beforeAll(async ({ request }) => {
  const sandbox = process.env.ORI_RECIPIENT_PICKER_SANDBOX;
  test.skip(!sandbox, 'Use e2e-fresh.sh --sandbox-env ORI_RECIPIENT_PICKER_SANDBOX');
  expect(basename(sandbox!)).toMatch(/^ori-e2e\./);
  await request.post('/api/onboarding/skip');
  await ok(await request.post('/api/settings/workspace-root', { data: { workspace_root: '' } }));
  let assistant = (await (await request.get('/api/personal-assistant')).json()).personal_assistant;
  assistant = (
    await ok(
      await request.post('/api/personal-assistant/hire', {
        data: {
          request_id: 'recipient-picker-hire',
          if_version: assistant.state_version || 0,
          display_name: 'Atlas',
          mandate: 'Help me plan.',
          focus_areas: ['plan_my_day']
        }
      })
    )
  ).personal_assistant;
  await ok(
    await request.post('/api/personal-assistant/hq', {
      data: {
        request_id: 'recipient-picker-hq',
        if_version: assistant.state_version,
        name: 'My HQ',
        timezone: 'UTC'
      }
    })
  );
  assistant = (await (await request.get('/api/personal-assistant')).json()).personal_assistant;
  expect(assistant.state).toBe('active');
  await ok(
    await request.post('/api/agents', { data: { name: 'Mira', catalog_role: 'orchestrator' } })
  );
  seeded = {
    commander: await createWorkspace(request, {
      name: 'Product Launch',
      entry_agent_name: 'Mira'
    }),
    vacant: await createWorkspace(request, { name: 'Blank Space' }),
    hq: await workspaceRef(request, String(assistant.hq_workspace_id))
  };
  // The vacant workspace really has no Commander.
  const vacant = await (await request.get(`/api/workspaces/${seeded.vacant.id}`)).json();
  expect(String(vacant.entry_agent_name || '')).toBe('');
});

type Traffic = {
  sessions: Record<string, unknown>[];
  chats: { sessionId: string; body: Record<string, unknown> }[];
  personal: string[];
};

/** Records the drawer's requests and answers /api/chat without a model. */
async function watch(page: Page): Promise<Traffic> {
  const traffic: Traffic = { sessions: [], chats: [], personal: [] };
  page.on('request', req => {
    const path = new URL(req.url()).pathname;
    if (req.method() === 'POST' && path === '/api/sessions')
      traffic.sessions.push(req.postDataJSON());
    if (/^\/api\/home-assistant\/(route|ask)$/.test(path)) traffic.personal.push(path);
  });
  await page.route('**/api/chat', async route => {
    const req = route.request();
    traffic.chats.push({
      sessionId: String(req.headers()['x-session-id'] || ''),
      body: req.postDataJSON()
    });
    await route.fulfill({ json: { response: 'Fixture reply from Mira.' } });
  });
  return traffic;
}

/** Opens the drawer; after a reload it may already have reopened itself. */
async function openDrawer(page: Page) {
  const panel = page.locator('#personalAssistantPanel');
  const launcher = page.locator('#personalAssistantLauncher');
  await expect
    .poll(async () => (await panel.isVisible()) || (await launcher.isVisible()))
    .toBe(true);
  if (!(await panel.isVisible())) await launcher.click();
  await expect(panel).toBeVisible();
}

async function sessionsIn(request: APIRequestContext, workspaceId: string) {
  const list = await (await request.get(`/api/sessions?folder_id=${workspaceId}`)).json();
  return (list.sessions || []) as { id: string; agent_name: string }[];
}

test('a workspace with a Commander opens as its entry agent and sends to it', async ({
  page,
  request
}) => {
  await installLocalCdn(page);
  const traffic = await watch(page);
  await page.goto(`/workspaces/${seeded.commander.slug}?mode=map`);
  const launcher = page.locator('#personalAssistantLauncher');
  await expect(page.locator('#personalAssistantLauncherName')).toHaveText('Mira');
  await expect(page.locator('#personalAssistantLauncherRole')).toHaveText(
    'Commander · Product Launch'
  );
  await expect(launcher).not.toContainText('Atlas');
  await openDrawer(page);
  await expect(page.locator('#personalAssistantPanelTitle')).toHaveText('Mira');
  await expect(page.locator('#personalAssistantCheckIn')).toHaveText('Commander · Product Launch');
  await expect(page.locator('#personalAssistantInput')).toHaveAttribute(
    'placeholder',
    'Ask Mira about this workspace… (/task creates a task)'
  );
  await expect(page.locator('#personalAssistantFolderChip')).toBeHidden();
  await expect(page.locator('#personalAssistantEntrySessionTitle')).toHaveText('New conversation');

  await page.locator('#personalAssistantInput').fill('What can you do in this workspace?');
  await page.locator('#personalAssistantSend').click();
  const reply = page.locator('#homeAssistantConversation [data-message-author="Mira"]');
  await expect(reply).toContainText('Fixture reply from Mira.');
  expect(traffic.sessions).toHaveLength(1);
  expect(traffic.sessions[0]).toMatchObject({
    folder_id: seeded.commander.id,
    agent_name: 'Mira'
  });
  const created = await sessionsIn(request, seeded.commander.id);
  expect(created.map(session => session.agent_name)).toEqual(['Mira']);
  expect(traffic.chats).toHaveLength(1);
  expect(traffic.chats[0].sessionId).toBe(created[0].id);
  expect(traffic.chats[0].body).toMatchObject({
    question: 'What can you do in this workspace?',
    agent_name: 'Mira'
  });
  expect(traffic.chats[0].body.conversation).toBeUndefined();
  expect(traffic.personal).toEqual([]);
  await expect(page.locator('#chatPanel')).toHaveAttribute('aria-hidden', 'true');
  await expect(page.locator('#personalAssistantPanelStatus')).not.toContainText('Atlas');

  // A follow-up stays in the same session.
  await page.locator('#personalAssistantInput').fill('Name one thing to do first.');
  await page.locator('#personalAssistantSend').click();
  await expect(reply).toHaveCount(2);
  expect(traffic.sessions).toHaveLength(1);
  expect(traffic.chats[1].sessionId).toBe(created[0].id);

  // Details mode of the same workspace names the same agent.
  await page.goto(`/workspaces/${seeded.commander.slug}?mode=details`);
  await expect(page.locator('#personalAssistantLauncherName')).toHaveText('Mira');
});

test('switching to the Personal Assistant keeps the draft byte for byte', async ({ page }) => {
  await installLocalCdn(page);
  const traffic = await watch(page);
  await page.goto(`/workspaces/${seeded.commander.slug}?mode=map`);
  await openDrawer(page);
  await expect(page.locator('#personalAssistantPanelTitle')).toHaveText('Mira');
  const draft = '  half-typed REAPER question → “reverb” 🎛️\nsecond line ';
  const input = page.locator('#personalAssistantInput');
  await input.fill(draft);
  const button = page.locator('#personalAssistantRecipientButton');
  await expect(button).toBeVisible();
  await expect(button).toHaveAttribute('aria-haspopup', 'menu');
  await button.click();
  const menu = page.locator('#personalAssistantRecipientMenu');
  await expect(menu.getByRole('menuitemradio')).toHaveCount(2);
  await expect(menu.getByRole('menuitemradio').first()).toHaveAttribute('aria-checked', 'true');
  await menu.getByRole('menuitemradio', { name: /Atlas/ }).click();
  await expect(menu).toBeHidden();
  await expect(page.locator('#personalAssistantPanelTitle')).toHaveText('Atlas');
  await expect(page.locator('#personalAssistantLauncherName')).toHaveText('Atlas');
  expect(await input.inputValue()).toBe(draft);
  await expect(page.locator('#personalAssistantEntrySessionBar')).toBeHidden();
  await expect(page.locator('#homeAssistantConversation [data-message-author="Mira"]')).toHaveCount(
    0
  );

  // The pick lasts for this workspace in this tab, through a reload.
  await page.reload();
  await expect(page.locator('#personalAssistantLauncherName')).toHaveText('Atlas');

  // And back, from the keyboard alone.
  await openDrawer(page);
  await button.focus();
  await page.keyboard.press('Enter');
  await expect(menu).toBeVisible();
  await page.keyboard.press('Home');
  await page.keyboard.press('Enter');
  await expect(page.locator('#personalAssistantPanelTitle')).toHaveText('Mira');
  await expect(button).toBeFocused();
  // Escape closes only the menu.
  await page.keyboard.press('Enter');
  await expect(menu).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(menu).toBeHidden();
  await expect(page.locator('#personalAssistantPanel')).toBeVisible();
  // Switching sent nothing and created nothing.
  expect(traffic.sessions).toEqual([]);
  expect(traffic.chats).toEqual([]);
});

test('New forgets the session and the next message starts another', async ({ page, request }) => {
  await installLocalCdn(page);
  const traffic = await watch(page);
  await page.goto(`/workspaces/${seeded.commander.slug}?mode=map`);
  await openDrawer(page);
  await expect(page.locator('#personalAssistantEntrySessionTitle')).toHaveText(
    'What can you do in this workspace?'
  );
  await page.locator('#personalAssistantEntrySessionNew').click();
  await expect(page.locator('#personalAssistantEntrySessionTitle')).toHaveText('New conversation');
  expect(await sessionsIn(request, seeded.commander.id)).toHaveLength(1);
  await page.locator('#personalAssistantInput').fill('Hello again.');
  await page.locator('#personalAssistantSend').click();
  await expect(
    page.locator('#homeAssistantConversation [data-message-author="Mira"]')
  ).toContainText('Fixture reply from Mira.');
  expect(traffic.sessions).toHaveLength(1);
  const sessions = await sessionsIn(request, seeded.commander.id);
  expect(sessions).toHaveLength(2);
  expect(sessions.every(session => session.agent_name === 'Mira')).toBe(true);
});

test('a vacant Commander defaults to the Personal Assistant with a disabled row', async ({
  page
}) => {
  await installLocalCdn(page);
  // The workspace's own page opens a mandatory "Create a Commander" dialog
  // (it predates the picker), so the drawer is checked on its canvas, which
  // is the same workspace page for the recipient rules (FR2, FR4).
  await page.goto(`/workspaces/${seeded.vacant.slug}/canvas`);
  await expect(page.locator('#personalAssistantLauncherName')).toHaveText('Atlas');
  await openDrawer(page);
  await expect(page.locator('#personalAssistantPanelTitle')).toHaveText('Atlas');
  await page.locator('#personalAssistantRecipientButton').click();
  const menu = page.locator('#personalAssistantRecipientMenu');
  const entry = menu.getByRole('menuitemradio').first();
  await expect(entry).toHaveAttribute('aria-disabled', 'true');
  await expect(entry).toContainText('No Commander yet');
  await expect(menu.getByRole('menuitem', { name: 'Fill the role' })).toHaveAttribute(
    'href',
    `/workspaces/${encodeURIComponent(seeded.vacant.slug)}?mode=details`
  );
  // Playwright will not click an aria-disabled item; a person can, and is told why.
  await entry.dispatchEvent('click');
  await expect(page.locator('#personalAssistantPanelStatus')).toHaveText(
    'This workspace has no Commander yet. Fill the role to talk to one.'
  );
  await expect(page.locator('#personalAssistantPanelTitle')).toHaveText('Atlas');
});

test('Home and the Personal HQ show no picker', async ({ page }) => {
  await installLocalCdn(page);
  for (const path of ['/', `/workspaces/${seeded.hq.slug}?mode=map`]) {
    await page.goto(path);
    await expect(page.locator('#personalAssistantLauncherName')).toHaveText('Atlas');
    await openDrawer(page);
    await expect(page.locator('#personalAssistantPanelTitle')).toHaveText('Atlas');
    await expect(page.locator('#personalAssistantRecipientButton')).toBeHidden();
    await expect(page.locator('#personalAssistantRecipientMenu')).toBeHidden();
  }
});

test('the open picker has no WCAG A/AA violations', async ({ page }) => {
  await installLocalCdn(page);
  await page.goto(`/workspaces/${seeded.commander.slug}?mode=map`);
  await openDrawer(page);
  await page.locator('#personalAssistantRecipientButton').click();
  await expect(page.locator('#personalAssistantRecipientMenu')).toBeVisible();
  await page.addScriptTag({ url: 'https://cdn.jsdelivr.net/npm/axe-core@4.10.3/axe.min.js' });
  const violations = await page.evaluate(
    async () =>
      (
        await (window as any).axe.run(document.getElementById('personalAssistantPanel'), {
          runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa'] }
        })
      ).violations
  );
  expect(violations, JSON.stringify(violations, null, 2)).toEqual([]);
});
