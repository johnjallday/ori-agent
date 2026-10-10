import { test, expect, Page } from '@playwright/test';
import { installLocalCdn } from './helpers/offline-cdn';
import { mockHiredAssistant } from './helpers/hired-assistant';

test('cockpit work shortcut preserves canonical selection without a Help dependency', async ({
  page
}, testInfo) => {
  await prepare(page);
  await page.route('**/api/workspaces?tree=true', route =>
    route.fulfill({
      json: {
        folders: [
          {
            id: 'selected-uuid',
            folder_slug: 'route-slug',
            name: 'Selected project',
            kind: 'workspace',
            entry_agent_name: 'Commander',
            children: []
          }
        ]
      }
    })
  );
  let sent: any;
  await page.route('**/api/home-assistant/route', route => {
    sent = route.request().postDataJSON().context;
    return route.fulfill({
      json: {
        intent: 'app_introspection',
        route_mode: 'home_inline',
        target_surface: 'current',
        requires_creation: false
      }
    });
  });
  await page.route('**/api/home-assistant/ask', route =>
    route.fulfill({ json: { response: 'Fixture reply', actions: [] } })
  );
  await page.route('**/api/home-assistant/context', route =>
    route.fulfill({
      json: {
        version: 1,
        status: 'available',
        subject: { id: 'selected-uuid', name: 'Selected project', kind: 'project' }
      }
    })
  );
  await page.goto('/');
  await expect(page.locator('.ws-map-tile[data-ws-id="selected-uuid"]')).toBeVisible();
  await page.locator('.ws-map-tile[data-ws-id="selected-uuid"]').click();
  await page.locator('[data-cockpit-rail-ask]').click();
  await expect(page.locator('#personalAssistantPanel')).toBeVisible();
  await expect(page.locator('#oriGuidePanel')).toBeHidden();
  await page.waitForFunction(
    () => (window as any).PersonalAssistantConversation?.isLoading?.() !== true
  );
  // Work consumes the shared collector, not the compatibility Help collector.
  await page.evaluate(() => {
    (window as any).OriGuide._collectContext = () => {
      throw new Error('Work depended on Help');
    };
  });
  await page.locator('#personalAssistantInput').fill('Review the selected project');
  await page.locator('#personalAssistantSend').click();
  await expect.poll(() => sent?.context_version).toBe(1);
  expect(sent.selection_workspace_id).toBe('selected-uuid');
  expect(sent.workspace_id).toBe('selected-uuid');
  expect(sent.workspace_slug).toBe('');
  expect(sent.origin).toBe('personal_assistant_panel');
  await page.screenshot({ path: testInfo.outputPath('assistant-selected-workspace.png') });
});

// Fixture-backed work/status/history; real Help endpoint. No real provider calls.
async function prepare(page: Page) {
  await installLocalCdn(page);
  await mockHiredAssistant(page);
  await page.route('**/api/personal-assistant/today', route =>
    route.fulfill({
      json: {
        today: {
          state: 'active',
          relationship_state: 'active',
          display_name: 'Atlas',
          hq_workspace_id: 'spec-hq',
          model: { status: 'not_configured', available: false }
        }
      }
    })
  );
  await page.route('**/api/onboarding/status', route =>
    route.fulfill({ json: { completed: true, needs_onboarding: false } })
  );
  await page.route('**/api/home-assistant/conversations', route =>
    route.fulfill({ json: { conversations: [] } })
  );
  await page.route('**/api/home-assistant/context', route =>
    route.fulfill({ json: { version: 1, status: 'available' } })
  );
  await page.route('**/api/home-assistant/route', route =>
    route.fulfill({
      json: {
        intent: 'app_introspection',
        route_mode: 'home_inline',
        target_surface: 'current',
        requires_creation: false
      }
    })
  );
}

async function openAssistant(page: Page) {
  await page.locator('#personalAssistantLauncher').click();
  await expect(page.locator('#personalAssistantPanel')).toBeVisible();
  await page.waitForFunction(
    () => (window as any).PersonalAssistantConversation?.isLoading?.() !== true
  );
}

async function workSearch(page: Page, text = 'draft the launch notes') {
  await page.locator('#oriGuideLauncher').click();
  await expect(page.locator('#oriGuidePanel')).toBeVisible();
  await page.locator('#oriGuideInput').fill(text);
  await page.locator('#oriGuideSend').click();
  await expect(page.getByRole('button', { name: 'Open Atlas', exact: true })).toBeVisible();
}

test('an explicit Help transition inserts an empty ready draft but only Send calls work', async ({
  page
}) => {
  await prepare(page);
  let routes = 0;
  let asks = 0;
  page.on('request', request => {
    if (/\/api\/home-assistant\/route$/.test(request.url())) routes += 1;
    if (/\/api\/home-assistant\/ask$/.test(request.url())) asks += 1;
  });
  await page.route('**/api/home-assistant/ask', route =>
    route.fulfill({ json: { response: 'Fixture reply', actions: [] } })
  );
  await page.goto('/');
  await openAssistant(page);
  await page.locator('#personalAssistantClose').click();
  await workSearch(page);
  expect(routes).toBe(0);
  expect(asks).toBe(0);
  await page.getByRole('button', { name: 'Open Atlas', exact: true }).click();
  await expect(page.locator('#oriGuidePanel')).toBeHidden();
  await expect(page.locator('#personalAssistantInput')).toHaveValue('draft the launch notes');
  expect(routes).toBe(0);
  expect(asks).toBe(0);
  await page.locator('#personalAssistantSend').click();
  await expect.poll(() => asks).toBe(1);
  expect(routes).toBe(1);
  await expect(
    page.locator('#personalAssistantActivityMount #homeAssistantThinkingModal')
  ).toHaveCount(1);
  await expect(page.locator('#oriGuidePanel #homeAssistantThinkingModal')).toHaveCount(0);
});

test('Help preserves a non-empty assistant draft and explains rejected insertion', async ({
  page
}, testInfo) => {
  await prepare(page);
  await page.goto('/');
  await openAssistant(page);
  await page.locator('#personalAssistantInput').fill('My exact unsent draft 🦊');
  await workSearch(page);
  await page.getByRole('button', { name: 'Open Atlas', exact: true }).click();
  await expect(page.locator('#oriGuidePanel')).toBeVisible();
  await expect(page.locator('#oriGuideReply')).toContainText('Send or clear your draft');
  await expect(page.locator('#oriGuideInput')).toHaveValue('draft the launch notes');
  await expect(page.locator('#personalAssistantInput')).toHaveValue('My exact unsent draft 🦊');
  await page.locator('#personalAssistantLauncher').click();
  await expect(page.locator('#oriGuidePanel')).toBeHidden();
  await expect(page.locator('#personalAssistantInput')).toHaveValue('My exact unsent draft 🦊');
  await page.screenshot({ path: testInfo.outputPath('assistant-preserved-draft.png') });
});

test('busy work and confirmation remain in the assistant while Help is read', async ({
  page
}, testInfo) => {
  await prepare(page);
  let asks = 0;
  let finish: (() => void) | undefined;
  await page.route('**/api/home-assistant/ask', async route => {
    asks += 1;
    await new Promise<void>(resolve => {
      finish = resolve;
    });
    await route.fulfill({
      json: {
        response: 'Review this change first.',
        requires_confirmation: true,
        confirmation: {
          action_id: 'scope-review',
          action_type: 'remember',
          summary: 'Remember the reviewed fact?',
          arguments: { text: 'A fictional fixture fact' }
        }
      }
    });
  });
  await page.goto('/');
  await openAssistant(page);
  await page.locator('#personalAssistantInput').fill('What needs my attention?');
  await page.locator('#personalAssistantSend').click();
  await expect.poll(() => asks).toBe(1);
  await workSearch(page, 'draft a new note');
  await expect(page.locator('#personalAssistantWorkStatus')).toContainText('Working');
  await page.getByRole('button', { name: 'Open Atlas', exact: true }).click();
  await expect(page.locator('#oriGuidePanel')).toBeVisible();
  await expect(page.locator('#oriGuideReply')).toContainText('Wait for the current reply');
  expect(asks).toBe(1);
  finish!();
  await expect(page.locator('#oriGuidePanel')).toBeVisible();
  await page.locator('#personalAssistantLauncher').click();
  const actions = page.locator('#homeAssistantActions');
  await expect(actions.getByRole('button', { name: 'Confirm', exact: true })).toBeVisible();
  await expect(page.locator('#homeAssistantThinkingModal')).toHaveAttribute(
    'data-home-assistant-panel-scope',
    'personal-assistant'
  );
  await actions.getByRole('button', { name: 'Confirm', exact: true }).scrollIntoViewIfNeeded();
  await page.screenshot({ path: testInfo.outputPath('assistant-fixture-confirmation.png') });
  await actions.getByRole('button', { name: 'Cancel', exact: true }).click();
  await expect(page.locator('#homeAssistantConversation')).toContainText(
    'I will not make that change'
  );
  expect(asks).toBe(1);
});

test('Home Cmd/Ctrl+J focuses work without changing a draft or hijacking an editing field', async ({
  page
}) => {
  await prepare(page);
  await page.goto('/');
  const shortcut = `${process.platform === 'darwin' ? 'Meta' : 'Control'}+j`;
  await page.keyboard.press(shortcut);
  await expect(page.locator('#personalAssistantPanel')).toBeVisible();
  await expect(page.locator('#personalAssistantInput')).toBeFocused();
  await page.locator('#personalAssistantInput').fill('Keep this shortcut draft');
  await page.locator('#oriGuideLauncher').click();
  await page.locator('#oriGuideInput').fill('agent');
  await page.keyboard.press(shortcut);
  await expect(page.locator('#oriGuidePanel')).toBeVisible();
  await page.locator('#oriGuideClose').click();
  await page.keyboard.press(shortcut);
  await expect(page.locator('#personalAssistantInput')).toBeFocused();
  await expect(page.locator('#personalAssistantInput')).toHaveValue('Keep this shortcut draft');
});

test('legacy handoff survives reload and rejected review, then becomes an unsent draft', async ({
  page
}) => {
  await prepare(page);
  let workCalls = 0;
  page.on('request', request => {
    if (/\/api\/home-assistant\/(route|ask)$/.test(request.url())) workCalls += 1;
  });
  await page.addInitScript(() => {
    if (sessionStorage.getItem('help-recovery-fixture')) return;
    sessionStorage.setItem('help-recovery-fixture', '1');
    sessionStorage.setItem('ori-guide-handoff', 'Recovered unsent request');
    sessionStorage.setItem('ori.personalAssistant.panelDraft', 'Existing newer draft');
  });
  await page.goto('/');
  await openAssistant(page);
  await expect(page.locator('#personalAssistantRecoveredHandoffText')).toHaveText(
    'Recovered unsent request'
  );
  await page.locator('#personalAssistantRecoverHandoff').click();
  await expect(page.locator('#personalAssistantInput')).toHaveValue('Existing newer draft');
  expect(await page.evaluate(() => sessionStorage.getItem('ori-guide-handoff'))).toBe(
    'Recovered unsent request'
  );
  await page.reload();
  await expect(page.locator('#personalAssistantPanel')).toBeVisible();
  await page.waitForFunction(
    () => (window as any).PersonalAssistantConversation?.isLoading?.() !== true
  );
  await expect(page.locator('#personalAssistantRecoveredHandoff')).toBeVisible();
  await page.locator('#personalAssistantInput').fill('');
  await page.locator('#personalAssistantRecoverHandoff').click();
  await expect(page.locator('#personalAssistantInput')).toHaveValue('Recovered unsent request');
  expect(await page.evaluate(() => sessionStorage.getItem('ori-guide-handoff'))).toBeNull();
  await expect(page.locator('#personalAssistantRecoveredHandoff')).toBeHidden();
  expect(workCalls).toBe(0);
});
