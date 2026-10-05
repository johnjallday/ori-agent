import { test, expect } from '@playwright/test';
import { mkdir, writeFile, readFile } from 'node:fs/promises';
import { basename, join } from 'node:path';

// Findings-first baseline. Real host selection, review, Route/Ask and history;
// no configured model or plugin. Provider-input characterization lives beside
// the server in personal_assistant_workspace_context_test.go. The deliberate
// delayed confirmation below is a browser busy-state fixture, not a setup run.
test('baseline: canonical review survives chat; Set up is enabled until busy', async ({
  page,
  request
}, testInfo) => {
  const sandbox = process.env.ORI_WORKSPACE_AWARENESS_SANDBOX;
  test.skip(!sandbox, 'Use e2e-fresh.sh --sandbox-env ORI_WORKSPACE_AWARENESS_SANDBOX');
  expect(basename(sandbox!)).toMatch(/^ori-e2e\./);
  const folder = join(sandbox!, 'Documents', 'Album-5 fixture');
  await mkdir(folder, { recursive: true, mode: 0o750 });
  const source = join(folder, 'notes.txt');
  const sentinel = 'Metadata selection must not send this file body to a model.';
  await writeFile(source, sentinel, { mode: 0o600 });

  await request.post('/api/onboarding/skip');
  expect(
    (await request.post('/api/settings/workspace-root', { data: { workspace_root: '' } })).ok()
  ).toBeTruthy();
  let assistant = (await (await request.get('/api/personal-assistant')).json()).personal_assistant;
  const hired = await request.post('/api/personal-assistant/hire', {
    data: {
      request_id: 'awareness-baseline-hire',
      if_version: assistant.state_version || 0,
      display_name: 'Atlas',
      mandate: 'Help me plan.',
      focus_areas: ['plan_my_day']
    }
  });
  expect(hired.status(), await hired.text()).toBe(201);
  assistant = (await hired.json()).personal_assistant;
  const hq = await request.post('/api/personal-assistant/hq', {
    data: {
      request_id: 'awareness-baseline-hq',
      if_version: assistant.state_version,
      name: 'My HQ',
      timezone: 'UTC'
    }
  });
  expect(hq.status(), await hq.text()).toBe(201);
  expect(
    (await request.post('/api/settings/system-model', { data: { provider: '', model: '' } })).ok()
  ).toBeTruthy();

  const payloads: { path: string; body: unknown }[] = [];
  page.on('request', req => {
    const path = new URL(req.url()).pathname;
    if (req.method() === 'POST' && /^\/api\/home-assistant\/(route|ask)$/.test(path))
      payloads.push({ path, body: req.postDataJSON() });
  });
  await page.goto('/');
  await page.locator('#personalAssistantLauncher').click();
  await page.locator('#personalAssistantFolderChip').click();
  await page
    .locator('#personalAssistantFolderChoices')
    .getByRole('button', { name: 'Documents', exact: true })
    .click();
  await expect(page.locator('#personalAssistantFolderChip')).toBeEnabled();
  await page.getByRole('button', { name: 'Review workspace setup', exact: true }).click();
  await page
    .locator('#personalAssistantFolderSetupCandidate')
    .selectOption({ label: 'Album-5 fixture' });
  await page.getByRole('button', { name: 'Review selection', exact: true }).click();
  const card = page.locator('#homeAssistantConversation #personalAssistantFolderOffer');
  const setup = card.getByRole('button', { name: 'Set up', exact: true });
  await expect(card).toBeVisible();
  await expect(setup).toBeEnabled();
  const initialControl = await setup.evaluate(button => ({
    disabled: (button as HTMLButtonElement).disabled,
    opacity: getComputedStyle(button).opacity,
    classes: button.className
  }));
  const evidenceDir = join(process.cwd(), 'tasks', 'evidence-assistant-workspace-awareness');
  await mkdir(evidenceDir, { recursive: true, mode: 0o750 });
  await testInfo.attach('ready-control.png', {
    body: await card.screenshot({ path: join(evidenceDir, 'baseline-ready-control.png') }),
    contentType: 'image/png'
  });

  const current = await page.evaluate(() =>
    (window as any).PersonalAssistantFolderContext.current()
  );
  const before = await (
    await request.get(`/api/home-assistant/conversations/${current.conversationId}`)
  ).json();
  expect(before.folder_reviews[current.offerId].status).toBe('pending');
  await page.locator('#personalAssistantInput').fill('Add this to my workspace');
  await page.locator('#personalAssistantSend').click();
  await page.waitForFunction(() => !(window as any).OriAskRouting.getState().busy);
  expect(payloads.map(value => value.path)).toEqual([
    '/api/home-assistant/route',
    '/api/home-assistant/ask'
  ]);
  await expect(card).toBeVisible();
  await expect(setup).toBeEnabled();
  const after = await (
    await request.get(`/api/home-assistant/conversations/${current.conversationId}`)
  ).json();
  expect(after.folder_reviews[current.offerId].status).toBe('pending');
  expect(after.folder_context.offer_id).toBe(current.offerId);

  // Hold only the confirmation response. The host must not receive approval in
  // this characterization; the disabled state is caused by our in-flight click.
  let release: (() => Promise<void>) | undefined;
  await page.route('**/api/personal-assistant/folder-digest/offers/*/decide', route => {
    release = () =>
      route.fulfill({ status: 409, json: { error: 'Controlled busy-state probe; no setup ran.' } });
  });
  await setup.click();
  await expect(setup).toBeDisabled();
  await testInfo.attach('busy-control.png', {
    body: await card.screenshot({ path: join(evidenceDir, 'baseline-busy-control.png') }),
    contentType: 'image/png'
  });
  await expect.poll(() => Boolean(release)).toBe(true);
  await release!();
  await expect(setup).toBeEnabled();
  expect(await readFile(source, 'utf8')).toBe(sentinel);
  const evidence = JSON.stringify(
    {
      evidence: 'Real host, generic folder; no configured-model or compatible-plugin claim',
      originalFadedControl: 'NOT REPRODUCED; initial control enabled, delayed action disabled',
      initialControl,
      payloads,
      review: {
        status: before.folder_reviews[current.offerId].status,
        subject: before.folder_reviews[current.offerId].subject.name
      }
    },
    null,
    2
  );
  await writeFile(join(evidenceDir, 'browser-baseline.json'), evidence, { mode: 0o600 });
  await testInfo.attach('baseline.json', { body: evidence, contentType: 'application/json' });
});
