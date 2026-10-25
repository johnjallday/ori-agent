import { test, expect } from '@playwright/test';
import { mkdirSync } from 'node:fs';
import { join } from 'node:path';
import { installLocalCdn } from './helpers/offline-cdn';

test.skip(process.env.ORI_DEFAULT_MODEL_DEMO !== '1', 'requires an isolated wt demo server');
const evidence = process.env.ORI_DEFAULT_MODEL_EVIDENCE || '';

test('linked editors keep saved profiles separate and report real shared outcomes', async ({
  page,
  request
}) => {
  test.setTimeout(120_000);
  await installLocalCdn(page);
  if (evidence) mkdirSync(evidence, { recursive: true });
  const post = async (path: string, data: object) => {
    const response = await request.post(path, { data });
    expect(response.ok(), await response.text()).toBeTruthy();
    return response.json();
  };
  await post('/api/onboarding/skip', {});
  await post('/api/settings/workspace-root', { workspace_root: '' });
  let assistant = (await (await request.get('/api/personal-assistant')).json()).personal_assistant;
  if (assistant.state === 'needs_hire') {
    assistant = (
      await post('/api/personal-assistant/hire', {
        request_id: 'model-editor-hire',
        if_version: assistant.state_version || 0,
        display_name: 'Atlas',
        mandate: 'Help me plan.',
        focus_areas: ['plan_my_day']
      })
    ).personal_assistant;
  }
  if (assistant.state === 'needs_hq') {
    assistant = (
      await post('/api/personal-assistant/hq', {
        request_id: 'model-editor-hq',
        if_version: assistant.state_version,
        name: 'My HQ',
        timezone: 'UTC'
      })
    ).personal_assistant;
  }
  const name = assistant.global_agent_profile_name;
  const agentPath = `/api/agents/${encodeURIComponent(name)}`;
  const profile = async () => (await request.get(`${agentPath}/detail`)).json();
  const settings = async () => (await request.get('/api/settings/system-model')).json();
  expect(
    (
      await request.patch(agentPath, {
        data: { model: 'saved-profile-alias', llm_provider: 'ollama' }
      })
    ).ok()
  ).toBeTruthy();
  await post('/api/settings/system-model', { provider: 'ollama', model: 'conversation-alias' });

  await page.goto(`/agents/${encodeURIComponent(name)}?tab=configuration`);
  await expect(page.locator('#agentModelLabel')).toHaveText('Saved model');
  await expect(page.locator('#agentModel')).toHaveText('saved-profile-alias');
  await expect(page.locator('#agentConversationModel')).toHaveText(
    'Conversation uses Default model: ollama / conversation-alias.'
  );
  await page.locator('#editConfigBtn').click();
  await expect(page.locator('#editModel')).toHaveValue('saved-profile-alias');
  await expect(page.locator('#editSystemModelHint')).toContainText("Ori's AI features");
  if (evidence)
    await page.locator('#agentConfigurationSection').screenshot({
      path: join(evidence, '09-linked-saved-vs-conversation.png'),
      animations: 'disabled'
    });
  await page.locator('#editTemperature').fill('0.7');
  await post('/api/settings/system-model', { provider: 'ollama', model: 'another-tab-choice' });
  await page.evaluate(() => window.dispatchEvent(new Event('focus')));
  await expect(page.locator('#editConversationModel')).toContainText('another-tab-choice');
  await expect(page.locator('#editModel')).toHaveValue('saved-profile-alias');
  await expect(page.locator('#editTemperature')).toHaveValue('0.7');
  await page.locator('#editProviderFilter').selectOption('ollama');
  const model = await page
    .locator('#editModel option')
    .evaluateAll(options =>
      options
        .map(option => (option as HTMLOptionElement).value)
        .find(value => value && value !== 'saved-profile-alias')
    );
  expect(model, 'needs a listed local model; no inference is requested').toBeTruthy();
  await page.locator('#editModel').selectOption(model!);
  await page.locator('#saveConfigBtn').click();
  await expect(page.locator('#configEditStatus')).toContainText(
    `Conversation uses Default model: ollama / ${model}.`
  );
  expect(await settings()).toMatchObject({ provider: 'ollama', model });
  expect(await profile()).toMatchObject({ model, provider: 'ollama' });
  await page.evaluate(() =>
    (window as any).PersonalAssistantPanel.open(
      document.getElementById('personalAssistantLauncher')
    )
  );
  await expect(page.locator('#personalAssistantModelText')).toHaveText(`Model: ollama / ${model}`);
  await page.evaluate(() => (window as any).PersonalAssistantPanel.close());

  // The shared session editor lives in the Home/chat layout, not agent detail.
  await page.goto('/');
  await page.waitForFunction(() => Boolean((window as any).sessionManager?.showEditAgentModal));
  await page.evaluate(name => (window as any).sessionManager.showEditAgentModal(name), name);
  await expect(page.locator('#editAgentModelLabel')).toHaveText('Saved model');
  await expect(page.locator('#editAgentModelSelect')).toHaveValue(model!);
  await expect(page.locator('#editAgentSystemModelHint')).toContainText("Ori's AI features");
  await page.locator('#editAgentSaveBtn').click();
  await expect(page.locator('#editAgentModal')).not.toBeVisible();
  expect(await settings()).toMatchObject({ provider: 'ollama', model });

  // Controlled input fixture, real server refusal: select a provider that is
  // not registered. The profile saves; the existing shared mirror rejects it.
  await page.evaluate(name => (window as any).sessionManager.showEditAgentModal(name), name);
  await expect(page.locator('#editAgentSaveBtn')).toBeEnabled();
  await expect(page.locator('#editAgentModelSelect')).toHaveValue(model!);
  await page.locator('#editAgentModelSelect').evaluate((select: HTMLSelectElement) => {
    const option = new Option('Controlled unavailable profile', 'unavailable-profile', true, true);
    option.dataset.provider = 'gemini';
    select.add(option);
    select.dispatchEvent(new Event('change', { bubbles: true }));
  });
  await page.locator('#editAgentSaveBtn').click();
  await expect(page.locator('#editAgentError')).toContainText(
    'Agent saved, but the Default model update was not confirmed.'
  );
  await expect(page.locator('#editAgentConversationModel')).toContainText(`ollama / ${model}`);
  await expect(page.locator('#editAgentModelSelect')).toHaveValue('unavailable-profile');
  expect(await profile()).toMatchObject({
    model: 'unavailable-profile',
    provider: 'gemini'
  });
  expect(await settings()).toMatchObject({ provider: 'ollama', model });
  await page.locator('#editAgentError').scrollIntoViewIfNeeded();
  if (evidence)
    await page.screenshot({
      path: join(evidence, '10-rejected-mirror-controlled-input.png'),
      animations: 'disabled'
    });
  await page.locator('#editAgentModal .btn-close').click();
  expect(
    (await request.patch(agentPath, { data: { model, llm_provider: 'ollama' } })).ok()
  ).toBeTruthy();

  const independent = `Independent ${Date.now()}`;
  await post('/api/agents', { name: independent, catalog_role: 'orchestrator' });
  await page.goto(`/agents/${encodeURIComponent(independent)}?tab=configuration`);
  await expect(page.locator('#agentModelLabel')).toHaveText('Model');
  await expect(page.locator('#agentModelSystemNote')).toBeHidden();
  await page.locator('#editConfigBtn').click();
  await expect(page.locator('#editSystemModelHint')).toBeHidden();
  await page.locator('#editProviderFilter').selectOption('ollama');
  await page.locator('#editModel').selectOption(model!);
  await page.locator('#saveConfigBtn').click();
  await expect(page.locator('#configEditStatus')).toContainText(
    'Configuration updated successfully'
  );
  expect(await settings()).toMatchObject({ provider: 'ollama', model });
  await page.goto('/');
  await page.waitForFunction(() => Boolean((window as any).sessionManager?.showEditAgentModal));
  await page.evaluate(name => (window as any).sessionManager.showEditAgentModal(name), independent);
  await expect(page.locator('#editAgentModelSelect')).toHaveValue(model!);
  await expect(page.locator('#editAgentSystemModelHint')).toBeHidden();
  await expect(page.locator('#editAgentConversationModel')).toBeHidden();
  await page.locator('#editAgentModelSelect').scrollIntoViewIfNeeded();
  if (evidence)
    await page.screenshot({
      path: join(evidence, '11-independent-editor.png'),
      animations: 'disabled'
    });
  await page.locator('#editAgentModal .btn-close').click();

  await post('/api/settings/system-model', { provider: '', model: '' });
  await page.goto(`/agents/${encodeURIComponent(name)}?tab=configuration`);
  await expect(page.locator('#agentModel')).toHaveText(model!);
  await expect(page.locator('#agentConversationModel')).toHaveText(
    'Conversation uses Default model: not configured.'
  );
  await page.evaluate(() =>
    (window as any).PersonalAssistantPanel.open(
      document.getElementById('personalAssistantLauncher')
    )
  );
  await expect(page.locator('#personalAssistantModelText')).toHaveText('Model: not configured');
  if (evidence)
    await page.screenshot({ path: join(evidence, '12-cleared-default-keeps-profile.png') });
  await post('/api/settings/system-model', { provider: 'ollama', model });
});
