import { test, expect } from '@playwright/test';
import { mkdirSync } from 'node:fs';
import { join } from 'node:path';
import { talkToPersonalAssistant } from './helpers/assistant-recipient';
import { installLocalCdn } from './helpers/offline-cdn';

// Mutates settings only in an explicitly opted-in disposable wt demo server.
// No provider inference is requested by this suite.
test.skip(process.env.ORI_DEFAULT_MODEL_DEMO !== '1', 'requires an isolated wt demo server');
test.describe.configure({ mode: 'serial' });
const evidence = process.env.ORI_DEFAULT_MODEL_EVIDENCE || '';

test('Default model recovery keeps the old anchor and save/clear contract', async ({ page, request }) => {
  expect((await request.post('/api/onboarding/skip', { data: {} })).ok()).toBeTruthy();
  expect((await request.post('/api/settings/system-model', {
    data: { provider: '', model: '' }
  })).ok()).toBeTruthy();
  if (evidence) mkdirSync(evidence, { recursive: true });

  await page.goto('/agents/create');
  if (await page.getByRole('tab', { name: 'Advanced', exact: true }).isVisible()) {
    await page.getByRole('tab', { name: 'Advanced', exact: true }).click();
  }
  await page.locator('label[for="createConfigModeAuto"]').click();
  await expect(page.locator('#createLlmWarningMessage')).toContainText('Default model');
  if (evidence) await page.screenshot({ path: join(evidence, '01-setup-recovery.png') });
  await page.locator('#createLlmWarningSettingsLink').click();
  await expect(page).toHaveURL(/\/settings#system-model$/);
  await expect(page.locator('#system-model h2')).toBeInViewport();
  await expect(page.locator('#systemModelStatusText')).toHaveText('Default model not configured');
  await expect(page.locator('#system-model')).toContainText(
    "Used by your Assistant and Ori's AI features. Individual agents can use their own models."
  );
  await page.locator('#system-model h2').scrollIntoViewIfNeeded();
  if (evidence) await page.screenshot({ path: join(evidence, '02-settings-unconfigured.png') });

  await page.locator('#systemModelProvider').selectOption('ollama');
  await expect(page.locator('#systemModelModel')).toBeEnabled();
  const model = await page.locator('#systemModelModel option').evaluateAll(options =>
    options.map(option => (option as HTMLOptionElement).value).find(Boolean)
  );
  expect(model, 'demo needs one locally listed Ollama model; no inference is run').toBeTruthy();
  await page.locator('#systemModelModel').selectOption(model!);
  await page.locator('#saveSystemModelBtn').click();
  await expect(page.locator('#systemModelStatusText')).toHaveText('Default model configured');
  const saved = await (await request.get('/api/settings/system-model')).json();
  expect(saved).toMatchObject({ provider: 'ollama', model, configured: true });
  if (evidence) await page.screenshot({ path: join(evidence, '03-settings-configured.png') });
  await page.reload();
  await expect(page.locator('#systemModelModel')).toHaveValue(model!);

  page.once('dialog', dialog => dialog.accept());
  await page.locator('#clearSystemModelBtn').click();
  await expect(page.locator('#systemModelStatusText')).toHaveText('Default model not configured');
  expect(await (await request.get('/api/settings/system-model')).json()).toMatchObject({
    provider: '', model: '', configured: false
  });
});

test('real metadata follows recipients; links open the effective editors', async ({ page, request }) => {
  test.setTimeout(60_000);
  await installLocalCdn(page);
  const post = async (path: string, data: object) => {
    const response = await request.post(path, { data });
    expect(response.ok(), await response.text()).toBeTruthy();
    return response.json();
  };
  await post('/api/settings/workspace-root', { workspace_root: '' });
  let assistant = (await (await request.get('/api/personal-assistant')).json()).personal_assistant;
  if (assistant.state === 'needs_hire') {
    assistant = (await post('/api/personal-assistant/hire', {
      request_id: 'model-clarity-hire', if_version: assistant.state_version || 0,
      display_name: 'Atlas', mandate: 'Help me plan.', focus_areas: ['plan_my_day']
    })).personal_assistant;
  }
  if (assistant.state === 'needs_hq') {
    await post('/api/personal-assistant/hq', {
      request_id: 'model-clarity-hq', if_version: assistant.state_version,
      name: 'My HQ', timezone: 'UTC'
    });
  }
  await post('/api/settings/system-model', { provider: 'ollama', model: 'assistant-choice' });
  const name = `Model Guide ${Date.now()}`;
  await post('/api/agents', { name, catalog_role: 'orchestrator' });
  expect((await request.patch(`/api/agents/${encodeURIComponent(name)}`, {
    data: { model: 'global-choice', llm_provider: 'ollama' }
  })).ok()).toBeTruthy();
  const workspace = await post('/api/workspaces', { name: `Model project ${Date.now()}`, entry_agent_name: name });
  const id = String(workspace.id || workspace.workspace?.id || workspace.folder?.id);
  const details = await (await request.get(`/api/workspaces/${id}`)).json();
  const slug = details.folder_slug || id;
  const before = await (await request.get(`/api/sessions?folder_id=${id}`)).json();
  await page.goto(`/workspaces/${slug}?mode=map`);
  await page.waitForFunction(() => Boolean((window as any).PersonalAssistantPanel?.open));
  await page.evaluate(() => (window as any).PersonalAssistantPanel.open(document.getElementById('personalAssistantLauncher')));
  await expect(page.locator('#personalAssistantModelText')).toHaveText('Model: ollama / global-choice');
  await expect(page.locator('#personalAssistantModelSettings')).toHaveAttribute('href', `/workspaces/${slug}?editAgentModel=${encodeURIComponent(name).replace(/%20/g, '+')}`);
  expect(await (await request.get(`/api/sessions?folder_id=${id}`)).json()).toEqual(before);
  if (evidence) await page.screenshot({ path: join(evidence, '04-entry-model.png') });

  // Controlled chat response: this verifies UI snapshot timing, not execution.
  let releaseChat = () => {};
  const gate = new Promise<void>(resolve => { releaseChat = resolve; });
  let sent = false;
  await page.route('**/api/chat', async route => {
    sent = true;
    await gate;
    await route.fulfill({ json: { response: 'Controlled delayed reply.' } });
  });
  await page.locator('#personalAssistantInput').fill('Say hello.');
  await page.locator('#personalAssistantSend').click();
  await expect.poll(() => sent).toBe(true);
  // Workspace activation has materialized its own copy. Edit that effective
  // definition, not the independent global agent of the same name.
  const updatedCopy = await request.patch(`/api/workspaces/${id}/agents/${encodeURIComponent(name)}`, {
    data: { model: 'after-turn-choice', llm_provider: 'ollama' }
  });
  expect(updatedCopy.ok(), await updatedCopy.text()).toBeTruthy();
  expect((await (await request.get(`/api/agents/${encodeURIComponent(name)}/detail`)).json()).model).toBe('global-choice');
  await page.evaluate(() => (window as any).EventBus.emit('systemModel:changed', {}));
  await expect(page.locator('#personalAssistantModelText')).toHaveText('Model: ollama / global-choice');
  releaseChat();
  await expect(page.locator('#personalAssistantModelText')).toHaveText('Model: ollama / after-turn-choice');
  const sessions = await (await request.get(`/api/sessions?folder_id=${id}`)).json();
  expect(sessions.sessions).toHaveLength(1);
  await page.reload();
  await expect(page.locator('#personalAssistantModelText')).toHaveText('Model: ollama / after-turn-choice');
  await expect.poll(() => page.evaluate(() => (window as any).PersonalAssistantRecipient?.sessionId?.())).toBe(sessions.sessions[0].id);

  await talkToPersonalAssistant(page);
  await expect(page.locator('#personalAssistantModelText')).toHaveText('Model: ollama / assistant-choice');
  await expect(page.locator('#personalAssistantModelSettings')).toHaveAttribute('href', '/settings#system-model');
  if (evidence) await page.screenshot({ path: join(evidence, '05-personal-model.png') });
  // A late metadata response for a previous recipient must not land.
  let releaseMetadata = () => {};
  const metadataGate = new Promise<void>(resolve => { releaseMetadata = resolve; });
  let requested = false;
  await page.route('**/api/chat/model?**', async route => {
    requested = true;
    await metadataGate;
    await route.fulfill({ json: { state: 'configured', source: 'agent', provider: 'ollama', model: 'stale-entry', settings_href: `/workspaces/${slug}?editAgentModel=wrong` } });
  });
  await page.locator('#personalAssistantRecipientButton').click();
  await page.locator('#personalAssistantRecipientMenu').getByRole('menuitemradio', { name: /Commander/ }).click();
  await expect.poll(() => requested).toBe(true);
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(page.locator('#personalAssistantModelText')).toHaveText('Model: checking…');
  if (evidence) await page.screenshot({ path: join(evidence, '07-mobile-loading-fixture.png') });
  await talkToPersonalAssistant(page);
  releaseMetadata();
  await expect(page.locator('#personalAssistantModelText')).toHaveText('Model: ollama / assistant-choice');
  await page.unroute('**/api/chat/model?**');
  await page.locator('#personalAssistantModelSettings').click();
  await expect(page).toHaveURL(/\/settings#system-model$/);
  await expect(page.locator('#system-model h2')).toBeInViewport();
  await post('/api/settings/system-model', { provider: '', model: '' });
  await page.evaluate(() => {
    (window as any).PersonalAssistantPanel.open(document.getElementById('personalAssistantLauncher'));
    window.dispatchEvent(new Event('focus'));
  });
  await expect(page.locator('#personalAssistantModelText')).toHaveText('Model: not configured');
  if (evidence) await page.screenshot({ path: join(evidence, '08-mobile-unconfigured.png') });
  await post('/api/settings/system-model', { provider: 'ollama', model: 'assistant-choice' });
  await page.setViewportSize({ width: 1280, height: 720 });

  await page.goto(`/workspaces/${slug}?editAgentModel=${encodeURIComponent(name)}`);
  await expect(page.getByText(`Change Model for ${name}`, { exact: true })).toBeVisible();
  await expect(page.getByText('after-turn-choice via ollama', { exact: true })).toBeVisible();
  if (evidence) await page.screenshot({ path: join(evidence, '06-workspace-model-editor.png') });
});
