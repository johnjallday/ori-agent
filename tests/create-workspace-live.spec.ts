import { test, expect } from '@playwright/test';
import { installLocalCdn } from './helpers/offline-cdn';

// Explicit opt-in only, against a fresh wt demo sandbox. This hires an assistant,
// creates its HQ, and uses the selected provider for real build turns. No tasks run.
// Never point it at personal workspace data or production.
test('live guided creation preserves the draft, resumes, and creates the reviewed team', async ({ page, request }, testInfo) => {
  test.skip(process.env.ORI_LIVE_BUILD_DEMO !== '1', 'Requires an explicitly permitted isolated live-provider demo');
  test.setTimeout(180_000);
  const provider = process.env.ORI_LIVE_BUILD_PROVIDER;
  const model = process.env.ORI_LIVE_BUILD_MODEL;
  expect(provider).toBeTruthy();
  expect(model).toBeTruthy();
  const stamp = Date.now().toString(36);
  const name = `Live Guided ${stamp}`;
  const lead = `Live Lead ${stamp}`;
  const writer = `Live Writer ${stamp}`;
  const json = async (response: Awaited<ReturnType<typeof request.get>>) => {
    expect(response.ok(), await response.text()).toBeTruthy();
    return response.json();
  };
  await installLocalCdn(page);
  await request.post('/api/onboarding/skip');
  await json(await request.post('/api/settings/system-model', { data: { provider, model } }));
  const before = (await json(await request.get('/api/personal-assistant'))).personal_assistant;
  expect(before.state, 'Use a fresh disposable demo').toBe('needs_hire');
  let assistant = (await json(await request.post('/api/personal-assistant/hire', { data: {
    request_id: `live-hire-${stamp}`, if_version: before.state_version || 0,
    display_name: `Live Assistant ${stamp}`, mandate: 'Help with this disposable workspace creation demo only.', focus_areas: ['plan_my_day']
  } }))).personal_assistant;
  assistant = (await json(await request.post('/api/personal-assistant/hq', { data: {
    request_id: `live-hq-${stamp}`, if_version: assistant.state_version, name: `Live HQ ${stamp}`, timezone: 'UTC'
  } }))).personal_assistant;
  expect((await json(await request.get('/api/workspaces/build-sessions/availability'))).available).toBe(true);
  await page.goto('/');
  await page.locator('#cockpitCreateWorkspaceBtn').click();
  const pane = page.locator('#workspaceBuildPane');
  const input = page.locator('#workspaceBuildComposerInput');
  await expect(pane).toBeVisible();
  const transcript: unknown[] = [];
  const say = async (text: string) => {
    await expect(input).toBeEnabled({ timeout: 60_000 });
    await input.fill(text);
    const response = page.waitForResponse(r => /\/build-sessions\/[^/]+\/turns$/.test(new URL(r.url()).pathname), { timeout: 90_000 });
    await input.press('Enter');
    const reply = await json(await response);
    transcript.push(reply);
    await expect(input).toBeEnabled({ timeout: 30_000 });
    await expect(pane).not.toContainText('couldn’t reach my model');
    return reply.session;
  };
  let session = await say(`Create a Content Production workspace named "${name}" for a weekly newsletter. Use the content-production blueprint. Staff content-lead with a NEW agent named "${lead}" and brand-copywriter with a NEW agent named "${writer}". Both should use provider ${provider} and model ${model}. Do not create the workspace yet; prepare the complete draft and team for my review.`);
  await expect(page.locator('#folderNameInput')).toHaveValue(name);
  if (!(await page.evaluate(() => (window as any).sessionManager.teamView()?.canContinueFromTeam))) {
    session = await say(`Please fill both required roles now: content-lead with NEW "${lead}" and brand-copywriter with NEW "${writer}", provider ${provider}, model ${model}. Prepare for review, do not create yet.`);
  }
  await page.screenshot({ path: testInfo.outputPath('creation-live-assisted.png'), fullPage: true });
  await pane.getByRole('button', { name: 'Set up manually' }).click();
  await expect(pane).toBeHidden();
  const draftBefore = await page.evaluate(() => (window as any).sessionManager.collectCreatePayload().payload);
  expect(draftBefore.role_staffing).toEqual(expect.arrayContaining([
    expect.objectContaining({ role_id: 'content-lead', name: lead }),
    expect.objectContaining({ role_id: 'brand-copywriter', name: writer })
  ]));
  while (await page.locator('#wizardNextBtn').isVisible()) await page.locator('#wizardNextBtn').click();
  await expect(page.locator('#wizardStep4')).toBeVisible();
  await page.screenshot({ path: testInfo.outputPath('creation-live-review.png'), fullPage: true });
  await page.locator('#workspaceBuildWithBtn').click();
  await page.locator('#addFolderModal').getByRole('button', { name: 'Cancel', exact: true }).click();
  await expect(page.locator('#addFolderModal')).toBeHidden();
  await page.locator('#cockpitCreateWorkspaceBtn').click();
  await expect(pane).toContainText(`Resume building ${name}?`);
  await pane.getByRole('button', { name: 'Resume', exact: true }).click();
  await expect(page.locator('#folderNameInput')).toHaveValue(name);
  await expect.poll(() => page.evaluate(() => (window as any).sessionManager.wizardStep)).toBe(4);
  const createdResponse = page.waitForResponse(r => new URL(r.url()).pathname === '/api/workspaces' && r.request().method() === 'POST', { timeout: 90_000 });
  session = await say('Create it now, with exactly the draft and team I just reviewed.');
  const preview = page.locator('[data-ws-map-placement-preview]');
  await expect(preview).toBeVisible();
  await page.locator('[data-ws-map-viewport]').focus();
  for (let n = 0; n < 80 && /\bis-invalid\b/.test(await preview.getAttribute('class') || ''); n++) await page.keyboard.press('ArrowDown');
  await page.keyboard.press('Enter');
  const created = await json(await createdResponse);
  expect(created.folder.name).toBe(name);
  const agents = await json(await request.get(`/api/workspaces/${created.folder.id}/agents`));
  expect(agents.agents.map((agent: any) => ({ name: agent.name, provider: agent.provider, model: agent.model }))).toEqual(expect.arrayContaining([
    { name: lead, provider, model }, { name: writer, provider, model }
  ]));
  await testInfo.attach('live-guided-evidence', { body: JSON.stringify({ provider, model, sessionID: session.id, created, agents, transcript }, null, 2), contentType: 'application/json' });
  await page.goto(`/workspaces/${created.folder.folder_slug}`);
  await page.screenshot({ path: testInfo.outputPath('creation-live-workspace.png'), fullPage: true });
});
