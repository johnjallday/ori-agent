import { test, expect } from '@playwright/test';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { basename, dirname, join } from 'node:path';
import {
  syntheticFolderFiles,
  folderContentSentinel
} from './fixtures/assistant-folder-response.js';

// Real Ori selection -> Route/Ask -> canonical persistence -> replay. Only the
// loopback provider's short prose is scripted: NOT vendor-model quality evidence.
// Run: python3 scripts/assistant-workspace-demo.py --folder-response --port 8931
// Native picker is not used; all synthetic files live in the owned demo HOME.
test('real host: compact metadata on Home/Settings, follow-up and canonical reload', async ({
  page,
  request
}) => {
  const sandbox = process.env.ORI_WORKSPACE_FOLDERRESPONSE_SANDBOX;
  const provider = process.env.ORI_WORKSPACE_PROVIDER_FIXTURE;
  test.skip(!sandbox || !provider, 'Use assistant-workspace-demo.py --folder-response');
  test.setTimeout(120_000);
  expect(basename(sandbox!)).toMatch(/^ori-demo\./);
  expect(basename(provider!)).toMatch(/^ori-awareness-provider\./);
  const evidence = join(process.cwd(), 'tasks/evidence/assistant-folder-response-ux/group-2');
  await mkdir(evidence, { recursive: true, mode: 0o750 });
  for (const [relative, content] of syntheticFolderFiles) {
    // Put projects directly under the known-folder chip for a multi-row scan.
    const path = join(
      sandbox!,
      relative
        .replace('Music/Album collection/', 'Documents/')
        .replace('Documents/Research papers/', 'Desktop/')
    );
    await mkdir(dirname(path), { recursive: true, mode: 0o750 });
    await writeFile(path, content, { mode: 0o600 });
  }
  await writeFile(join(provider!, 'sentinels.json'), JSON.stringify([folderContentSentinel]), {
    mode: 0o600
  });
  await request.post('/api/onboarding/skip');
  expect(
    (await request.post('/api/settings/workspace-root', { data: { workspace_root: '' } })).ok()
  ).toBeTruthy();
  expect(
    (
      await request.post('/api/settings/system-model', {
        data: { provider: 'ollama', model: 'ori-workspace-fixture' }
      })
    ).ok()
  ).toBeTruthy();
  let assistant = (await (await request.get('/api/personal-assistant')).json()).personal_assistant;
  const hire = await request.post('/api/personal-assistant/hire', {
    data: {
      request_id: 'folder-response-hire',
      if_version: assistant.state_version || 0,
      display_name: 'Atlas',
      mandate: 'Help me plan.',
      focus_areas: ['plan_my_day']
    }
  });
  expect(hire.status(), await hire.text()).toBe(201);
  assistant = (await hire.json()).personal_assistant;
  const hq = await request.post('/api/personal-assistant/hq', {
    data: {
      request_id: 'folder-response-hq',
      if_version: assistant.state_version,
      name: 'My HQ',
      timezone: 'UTC'
    }
  });
  expect(hq.status(), await hq.text()).toBe(201);
  const resourcesBefore = (await (await request.get('/api/workspaces')).json()).folders.length;
  const audit = async () =>
    JSON.parse(await readFile(join(provider!, 'provider-audit.json'), 'utf8'));
  const outcomes: unknown[] = [];
  await page.setViewportSize({ width: 1440, height: 900 });
  for (const [path, chip] of [
    ['/', 'Documents'],
    ['/settings', 'Desktop']
  ]) {
    await page.goto(path);
    await expect(page.locator('#personalAssistantLauncher')).toBeAttached();
    await page.waitForFunction(() => (window as any).PersonalAssistantPanel?._state.view.available);
    if (!(await page.locator('#personalAssistantPanel').isVisible()))
      await page.locator('#personalAssistantLauncher').click();
    await page.waitForFunction(() => Boolean((window as any).PersonalAssistantFolderContext));
    if (path !== '/') await page.locator('#personalAssistantConversationNew').click();
    await page.locator('#personalAssistantFolderChip').click();
    const selected = page.waitForResponse(response =>
      response.url().endsWith('/folder-context/select')
    );
    await page
      .locator('#personalAssistantFolderChoices')
      .getByRole('button', { name: chip, exact: true })
      .click();
    const observation = (await (await selected).json()).observation;
    const preview = page.locator('#personalAssistantFolderPreview');
    await expect(preview).toBeVisible();
    await expect(preview).toContainText('Local preview · not sent');
    await expect(preview.getByRole('heading')).toHaveText(observation.folder);
    await expect(preview).toContainText('Attached folder: contents not read.');
    await expect(page.locator('#personalAssistantFolderChip')).toBeEnabled();
    const ask = page.waitForResponse(response => response.url().endsWith('/home-assistant/ask'));
    await page.locator('#personalAssistantSend').click();
    const reply = await (await ask).json();
    expect(reply.conversation.stored).toBe(true);
    expect(reply.folder_context.observation).toEqual(observation);
    await page.waitForFunction(() => !(window as any).OriAskRouting.getState().busy);
    await expect(preview).toBeHidden();
    const card = page.locator('#homeAssistantConversation [data-folder-observation-id]');
    await expect(card).toHaveCount(1);
    await expect(card.getByRole('heading')).toHaveText(observation.folder);
    const children = observation.projects.filter((row: any) => !row.root);
    await expect(
      card.locator(':scope > .personal-assistant-folder-context__rows > li')
    ).toHaveCount(Math.min(3, children.length));
    const details = card.locator('.personal-assistant-folder-context__details');
    expect(await details.getAttribute('open')).toBeNull();
    const firstCounts = await audit();
    await card.getByText('Scan details', { exact: true }).click();
    await expect(details).toContainText('counts overlap');
    await expect(details).toContainText('not a complete tree');
    await expect(details).toContainText('5000 entries');
    await page
      .locator('#personalAssistantPanel')
      .screenshot({ path: join(evidence, `${chip.toLowerCase()}-details.png`) });
    await card.getByText('Scan details', { exact: true }).click();
    expect((await audit()).requests).toBe(firstCounts.requests);
    await page.locator('#personalAssistantScroll').evaluate(el => {
      el.scrollTop = el.scrollHeight;
    });
    await page
      .locator('#personalAssistantPanel')
      .screenshot({ path: join(evidence, `${chip.toLowerCase()}-initial.png`) });
    const next = page.waitForResponse(response => response.url().endsWith('/home-assistant/ask'));
    await page
      .locator('#personalAssistantInput')
      .fill('Just discuss the organization; do not set anything up.');
    await page.locator('#personalAssistantSend').click();
    const followup = await (await next).json();
    expect(followup.conversation.stored).toBe(true);
    await page.waitForFunction(() => !(window as any).OriAskRouting.getState().busy);
    await expect(card).toHaveCount(1);
    await page.locator('#personalAssistantScroll').evaluate(el => {
      el.scrollTop = el.scrollHeight;
    });
    await page
      .locator('#personalAssistantPanel')
      .screenshot({ path: join(evidence, `${chip.toLowerCase()}-followup.png`) });
    const id = reply.conversation.id;
    const history = await (await request.get(`/api/home-assistant/conversations/${id}`)).json();
    expect(history.messages.filter((row: any) => row.role === 'folder_context')).toHaveLength(2);
    expect(history.messages.filter((row: any) => row.role === 'assistant')).toHaveLength(2);
    expect(history.folder_context.observation).toEqual(observation);
    const beforeReload = (await audit()).requests;
    await page.reload();
    await expect(card).toHaveCount(1);
    await expect(card.getByRole('heading')).toHaveText(observation.folder);
    await expect(card).toContainText('Bounded look');
    await expect(
      page.locator('#homeAssistantConversation [data-message-role="assistant"]')
    ).toHaveCount(2);
    expect((await audit()).requests).toBe(beforeReload);
    expect((await audit()).hits).toEqual([]);
    await page.locator('#personalAssistantScroll').evaluate(el => {
      el.scrollTop = el.scrollHeight;
    });
    await page
      .locator('#personalAssistantPanel')
      .screenshot({ path: join(evidence, `${chip.toLowerCase()}-reloaded.png`) });
    outcomes.push({
      page: path,
      folder: observation.folder,
      observationCount: 1,
      canonicalFolderEvents: 2,
      savedAnswers: 2,
      unchangedSnapshotAfterReload: true,
      providerCallsOnReload: 0
    });
  }
  expect((await (await request.get('/api/workspaces')).json()).folders.length).toBe(
    resourcesBefore
  );
  const digest = (await (await request.get('/api/personal-assistant/folder-digest')).json())
    .folder_digest;
  expect(digest.offer).toBeFalsy();
  await page.setViewportSize({ width: 390, height: 844 });
  const panel = page.locator('#personalAssistantPanel');
  expect(await panel.evaluate(el => el.scrollWidth <= el.clientWidth)).toBeTruthy();
  await expect(page.locator('#personalAssistantInput')).toBeInViewport();
  await page.locator('#personalAssistantScroll').evaluate(el => {
    el.scrollTop = el.scrollHeight;
  });
  await panel.screenshot({ path: join(evidence, 'desktop-folder-phone.png') });
  await writeFile(
    join(evidence, 'real-host-results.json'),
    JSON.stringify(
      {
        evidence:
          'Real built Ori with scripted loopback Ollama provider; not live model/native picker',
        outcomes,
        provider: await audit(),
        resourcesUnchanged: true,
        setupOfferCreated: false
      },
      null,
      2
    ),
    { mode: 0o600 }
  );
});
