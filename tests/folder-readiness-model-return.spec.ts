import { test, expect, type APIRequestContext, type Locator } from '@playwright/test';
import { createServer, type Server } from 'node:http';
import { readFileSync, mkdirSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { chooseAttach } from './helpers/assistant-menus';
import { stageReviewedFolderPackages } from './helpers/reviewed-folder-packages';

// Real host + reviewed packages, explicitly controlled loopback provider. This
// is configuration/consent evidence, NOT a live model or report-quality test.
const sandbox = process.env.ORI_READINESS_MODEL_SANDBOX || '';
const port = Number(process.env.ORI_READINESS_MODEL_PORT || 0);
const evidence = process.env.ORI_READINESS_EVIDENCE || '';
test.skip(!sandbox || !port, 'requires an isolated provisioned sandbox and explicit loopback port');
test.describe.configure({ mode: 'serial' });
test.setTimeout(120_000);
let provider: Server;
let executions = 0;

async function revealModel(card: Locator) {
  const ready = card.locator('details.folder-requirements__ready');
  await expect(ready).toBeVisible();
  if (!(await ready.evaluate(node => (node as HTMLDetailsElement).open)))
    await ready.locator('summary').first().click();
  await card
    .getByRole('region', { name: 'Project agent model' })
    .scrollIntoViewIfNeeded({ timeout: 10_000 });
}

async function json(request: APIRequestContext, path: string) {
  const response = await request.get(path);
  expect(response.ok(), await response.text()).toBeTruthy();
  return response.json();
}

test.beforeAll(async ({ request }) => {
  expect((await json(request, '/api/settings/system-model')).configured).toBe(false);
  if (process.env.ORI_READINESS_MODEL_INSTALL === '1') await stageReviewedFolderPackages(request);
  expect((await json(request, '/api/plugins')).plugins.length).toBeGreaterThanOrEqual(2);
  const post = async (path: string, data: object) => {
    const response = await request.post(path, { data });
    expect(response.ok(), await response.text()).toBeTruthy();
    return response.json();
  };
  await post('/api/onboarding/skip', {});
  await post('/api/settings/workspace-root', { workspace_root: '' });
  let assistant = (await json(request, '/api/personal-assistant')).personal_assistant;
  if (assistant.state === 'needs_hire') {
    await post('/api/personal-assistant/hire', {
      request_id: 'model-readiness-hire',
      if_version: assistant.state_version || 0,
      display_name: 'Atlas',
      mandate: 'Help me plan.',
      focus_areas: ['plan_my_day']
    });
    assistant = (await json(request, '/api/personal-assistant')).personal_assistant;
  }
  if (assistant.state === 'needs_hq')
    await post('/api/personal-assistant/hq', {
      request_id: 'model-readiness-hq',
      if_version: assistant.state_version,
      name: 'My HQ',
      timezone: 'UTC'
    });
  for (const folder of ['Desktop', 'Documents']) {
    mkdirSync(join(sandbox, folder), { recursive: true, mode: 0o750 });
    writeFileSync(join(sandbox, folder, 'Session.rpp'), '<REAPER_PROJECT 0.1 "7.0"\n>\n', {
      mode: 0o600,
      flag: 'wx'
    });
  }
  provider = createServer((req, res) => {
    res.setHeader('Content-Type', 'application/json');
    if (req.method === 'GET') {
      res.end(
        JSON.stringify({
          object: 'list',
          data: ['readiness-control-a', 'readiness-control-b'].map(id => ({
            id,
            object: 'model',
            owned_by: 'controlled-fixture'
          }))
        })
      );
      return;
    }
    executions++;
    res.end(
      JSON.stringify({
        id: 'controlled-readiness',
        object: 'chat.completion',
        choices: [
          {
            index: 0,
            message: {
              role: 'assistant',
              content: 'Controlled configuration fixture only; no project analysis was performed.'
            },
            finish_reason: 'stop'
          }
        ],
        usage: { prompt_tokens: 1, completion_tokens: 1, total_tokens: 2 }
      })
    );
  });
  await new Promise<void>((resolve, reject) => {
    provider.once('error', reject);
    provider.listen(port, '127.0.0.1', resolve);
  });
  if (evidence) mkdirSync(evidence, { recursive: true, mode: 0o750 });
});

test.afterAll(async () => {
  if (provider) await new Promise<void>(resolve => provider.close(() => resolve()));
});

test('real model Settings return preserves the review and requires refreshed explicit consent', async ({
  page,
  request
}) => {
  const bytes = readFileSync(join(sandbox, 'Desktop', 'Session.rpp'));
  const workspaces = await json(request, '/api/workspaces');
  const effects: string[] = [];
  page.on('request', req => {
    if (
      req.method() !== 'GET' &&
      /\/offers\/[^/]+\/(setup|decide)$|\/plugins\/.*(install|enable|update)|\/tasks.*execute/.test(
        new URL(req.url()).pathname
      )
    )
      effects.push(req.url());
  });
  await page.goto('/?panel=today');
  await page.waitForFunction(() => Boolean((window as any).PersonalAssistantPanel?.open));
  await page.evaluate(() =>
    (window as any).PersonalAssistantPanel.open(
      document.getElementById('personalAssistantLauncher')
    )
  );
  const draft = '  Keep 🎼 this exact\ndraft  ';
  const brief = '  Keep 🎼 this exact\nbrief  ';
  await page.locator('#personalAssistantInput').fill(draft);
  await chooseAttach(page, '#personalAssistantFolderChip:enabled');
  await page
    .locator('#personalAssistantFolderChoices')
    .getByRole('button', { name: 'Desktop', exact: true })
    .click();
  const card = page.locator('#personalAssistantFolderOffer');
  await expect(card).toBeVisible({ timeout: 60_000 });
  await expect(card.getByRole('region', { name: 'Project agent model' })).toContainText(
    'Configuration needed'
  );
  await expect(card.locator('[data-folder-action="setup"]')).toHaveCount(0);
  await card.getByLabel('What should it do with it?').fill(brief);
  const original = await page.evaluate(() => (window as any).PersonalAssistantFolder.current());
  const history = await json(
    request,
    `/api/home-assistant/conversations/${original.conversation_id}`
  );

  // A blocked popup is visible and does not discard process-local context.
  await page.evaluate(() => {
    (window as any).__readinessOpen = window.open;
    window.open = () => null;
  });
  await card.getByRole('button', { name: 'Configure project model' }).click();
  await expect(card.getByRole('alert')).toContainText('tab was blocked');
  await page.evaluate(() => {
    window.open = (window as any).__readinessOpen;
    delete (window as any).__readinessOpen;
  });
  const popup = page.waitForEvent('popup');
  await card.getByRole('button', { name: 'Configure project model' }).click();
  const settings = await popup;
  await expect(settings).toHaveURL(/\/settings#system-model$/);
  await settings.locator('#systemModelProvider').selectOption('lmstudio');
  await settings.locator('#systemModelModel').selectOption('readiness-control-a');
  await settings.locator('#saveSystemModelBtn').click();
  await expect(settings.locator('#systemModelStatusText')).toHaveText('System Model Configured');
  await page.bringToFront();
  await expect(page.locator('#personalAssistantInput')).toHaveValue(draft);
  await expect(card.getByLabel('What should it do with it?')).toHaveValue(brief);
  expect(executions).toBe(0);
  expect(effects).toEqual([]);
  expect(await json(request, '/api/workspaces')).toEqual(workspaces);

  const refreshed = page.waitForResponse(
    response =>
      response.url().endsWith('/folder-context/review') &&
      response.request().postDataJSON()?.recheck === true
  );
  await card.getByRole('button', { name: 'Check again', exact: true }).click();
  expect((await refreshed).ok()).toBeTruthy();
  await revealModel(card);
  await expect(card.getByRole('region', { name: 'Project agent model' })).toContainText(
    'readiness-control-a'
  );
  await expect(card.getByRole('region', { name: 'Project agent model' })).toContainText(
    'Configured'
  );
  const current = await page.evaluate(() => (window as any).PersonalAssistantFolder.current());
  expect(current.id).toBe(original.id);
  expect(current.conversation_id).toBe(original.conversation_id);
  const after = await json(request, `/api/home-assistant/conversations/${current.conversation_id}`);
  expect(after.messages.length).toBe(history.messages.length);
  expect(after.folder_context.revision).toBe(history.folder_context.revision);
  // The refreshed conversation/history read can finish after the recheck.
  // Read the current disclosure and verify its focus/visibility, not the
  // scroll position of the earlier node before that hydration completed.
  await revealModel(card);
  const refreshedModel = card.getByRole('region', { name: 'Project agent model' });
  await refreshedModel.focus();
  await expect(refreshedModel).toBeFocused();
  await expect(refreshedModel).toBeInViewport();
  expect(current.plan.digest).not.toBe(original.plan.digest);
  await expect(card.getByLabel('What should it do with it?')).toHaveValue(brief);
  if (evidence)
    await page.screenshot({ path: join(evidence, 'group3-controlled-model-return.png') });

  // Deliberately submit the earlier consent after a material settings change.
  // The real endpoint, not a response fixture, must refuse it before effects.
  await settings.locator('#systemModelModel').selectOption('readiness-control-b');
  await settings.locator('#saveSystemModelBtn').click();
  await expect(settings.locator('#systemModelStatusDetails')).toContainText('readiness-control-b');
  const stale = await request.post(
    `/api/personal-assistant/folder-digest/offers/${current.id}/setup`,
    { data: { request_id: 'readiness-stale-consent', plan_digest: current.plan.digest, brief } }
  );
  expect(stale.status()).toBe(409);
  expect((await stale.json()).plan_changed).toBe(true);
  expect(await json(request, '/api/workspaces')).toEqual(workspaces);
  expect(executions).toBe(0);
  await page.bringToFront();
  await card.getByRole('button', { name: 'Check again', exact: true }).click();
  await expect(card.getByRole('region', { name: 'Project agent model' })).toContainText(
    'readiness-control-b'
  );
  await expect(card.getByLabel('What should it do with it?')).toHaveValue(brief);
  await expect(page.locator('#personalAssistantInput')).toHaveValue(draft);
  expect(effects).toEqual([]);
  expect(readFileSync(join(sandbox, 'Desktop', 'Session.rpp'))).toEqual(bytes);

  // Only this final, refreshed UI confirmation may create resources/start work.
  const accepted = page.waitForResponse(response => /\/offers\/[^/]+\/setup$/.test(response.url()));
  await card.locator('[data-folder-action="setup"]').click();
  expect((await accepted).ok()).toBeTruthy();
  await expect
    .poll(
      async () =>
        (await page.evaluate(() => (window as any).PersonalAssistantFolder.current()))?.status,
      { timeout: 60_000 }
    )
    .toBe('resolved');
  await expect(card.getByRole('button', { name: 'Check again', exact: true })).toHaveCount(0);
  expect(effects.filter(url => /\/setup$/.test(url))).toHaveLength(1);
  expect(readFileSync(join(sandbox, 'Desktop', 'Session.rpp'))).toEqual(bytes);
  if (evidence) await page.screenshot({ path: join(evidence, 'group3-controlled-confirmed.png') });
  await settings.close();
});

test('a real shared agent retains its own configured model after the default changes', async ({
  page,
  request
}) => {
  const changed = await request.post('/api/settings/system-model', {
    data: { provider: 'lmstudio', model: 'readiness-control-a' }
  });
  expect(changed.ok()).toBeTruthy();
  const workspaces = await json(request, '/api/workspaces');
  const effects: string[] = [];
  page.on('request', req => {
    if (
      req.method() !== 'GET' &&
      /\/offers\/[^/]+\/(setup|decide)$|\/tasks.*execute/.test(new URL(req.url()).pathname)
    )
      effects.push(req.url());
  });
  await page.goto('/settings');
  await page.waitForFunction(() => Boolean((window as any).PersonalAssistantPanel?.open));
  await page.evaluate(() =>
    (window as any).PersonalAssistantPanel.open(
      document.getElementById('personalAssistantLauncher')
    )
  );
  const draft = '  Bound 🎼 agent draft  ';
  await page.locator('#personalAssistantInput').fill(draft);
  await chooseAttach(page, '#personalAssistantFolderChip:enabled');
  await page
    .locator('#personalAssistantFolderChoices')
    .getByRole('button', { name: 'Documents', exact: true })
    .click();
  const card = page.locator('#personalAssistantFolderOffer');
  await expect
    .poll(
      async () =>
        page.evaluate(() => (window as any).PersonalAssistantFolder.current()?.plan?.hire?.mode),
      { timeout: 60_000 }
    )
    .toBe('bind');
  const current = await page.evaluate(() => (window as any).PersonalAssistantFolder.current());
  expect(current.plan.visual.model).toMatchObject({
    state: 'configured',
    settings: 'agent',
    name: 'readiness-control-b'
  });
  expect(current.plan.visual.model.agent_name).toBe(current.plan.hire.agent_name);
  await revealModel(card);
  const model = card.getByRole('region', { name: 'Project agent model' });
  await expect(model).toContainText('readiness-control-b');
  await expect(card.locator('[data-folder-action="setup"]')).toHaveText('Assign & set up');
  const ready = card.locator('details.folder-requirements__ready');
  if (!(await ready.evaluate(node => (node as HTMLDetailsElement).open)))
    await ready.locator('summary').first().click();
  await model.scrollIntoViewIfNeeded();
  if (evidence)
    await page.screenshot({ path: join(evidence, 'group3-real-bound-agent-model.png') });
  await expect(page.locator('#personalAssistantInput')).toHaveValue(draft);
  expect(effects).toEqual([]);
  expect(await json(request, '/api/workspaces')).toEqual(workspaces);
  await card.getByRole('button', { name: 'Keep chatting', exact: true }).click();
});
