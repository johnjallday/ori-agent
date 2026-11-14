import { expect, test, type Page } from '@playwright/test';
import { mkdir, writeFile } from 'node:fs/promises';
import { join, resolve } from 'node:path';
import { installLocalCdn } from './helpers/offline-cdn';

const sandbox = process.env.ORI_HIRE_CARD_SANDBOX || '';
const evidence = resolve('tasks/evidence-folder-hire-card');
const endpoint = '/api/personal-assistant/folder-agent';
test.describe.configure({ mode: 'serial' });
test.use({ viewport: { width: 1440, height: 1200 } });

async function capture(page: Page, name: string) {
  await page.screenshot({ path: join(evidence, `${name}.png`), fullPage: true });
}

// Real sandbox, real picker, real single-request hire. No provider credentials.
test('folder hire picks a character, preserves it on creation, and offers generated faces', async ({
  page,
  request
}) => {
  test.skip(!sandbox, 'requires --sandbox-env ORI_HIRE_CARD_SANDBOX');
  test.setTimeout(120_000);
  await installLocalCdn(page);
  await mkdir(evidence, { recursive: true, mode: 0o750 });
  for (const name of ['Downloads', 'Desktop', 'Documents', 'Plain Folder', 'Sheet Folder'])
    await mkdir(join(sandbox, name), { recursive: true, mode: 0o750 });
  await writeFile(join(sandbox, 'Downloads', 'note.txt'), 'fixture', { mode: 0o600 });
  await request.post('/api/onboarding/skip');
  const current = (await (await request.get('/api/personal-assistant')).json()).personal_assistant;
  const hired = await request.post('/api/personal-assistant/hire', {
    data: {
      request_id: 'hire-card-assistant',
      if_version: current.state_version || 0,
      display_name: 'Atlas',
      mandate: 'Help with folders.',
      focus_areas: ['plan_my_day']
    }
  });
  expect(hired.status(), await hired.text()).toBe(201);
  const hq = await request.post('/api/personal-assistant/hq', {
    data: {
      request_id: 'hire-card-hq',
      if_version: (await hired.json()).personal_assistant.state_version,
      name: 'My HQ',
      timezone: 'UTC'
    }
  });
  expect(hq.status(), await hq.text()).toBe(201);

  async function feed(name: string) {
    await page.goto('/?panel=today&folder=show');
    const entry = page.locator('#personalAssistantFolderPathEntry');
    await entry.locator('input').fill(join(sandbox, name));
    await entry.getByRole('button').click();
    const card = page.locator('#personalAssistantFolderAgent');
    await expect(card.locator('.folder-agent-card__title')).toHaveText(`Hire an agent for ${name}`);
    return card;
  }
  const liveModel = process.env.ORI_HIRE_CARD_PROVIDER;
  if (liveModel) {
    const configured = await request.post('/api/settings/system-model', {
      data: {
        provider: liveModel,
        model: process.env.ORI_HIRE_CARD_MODEL || 'claude-sonnet-4-5'
      }
    });
    expect(configured.ok(), await configured.text()).toBeTruthy();
  }
  const card = await feed('Downloads');
  await expect(
    card.locator('.folder-agent-card__who-face [data-aa-requested="character"]')
  ).toBeVisible();
  await capture(page, 'face-downloads-suggested');
  await card.getByRole('button', { name: 'Change face', exact: true }).click();
  const picker = page.locator('#charPicker');
  await expect(picker).toBeVisible();
  await expect(picker.locator('#charPickerSearch')).toBeFocused();
  await picker.locator('[data-char-id="research-archivist"]').click();
  await capture(page, 'face-picker-selected');
  await picker.locator('#charPickerConfirm').click();
  await expect(picker).toHaveCount(0);
  const selected = card.locator('.folder-agent-card__who-face [data-aa-requested="character"]');
  await expect(selected).toBeVisible();
  await capture(page, 'face-downloads-chosen');
  let sent: any;
  page.on('request', req => {
    if (req.url().endsWith('/folder-agent/start')) sent = req.postDataJSON();
  });
  const startResponse = page.waitForResponse(response =>
    response.url().endsWith('/folder-agent/start')
  );
  await card.locator('[data-folder-agent-action="start"]').click();
  const started = await startResponse;
  expect(started.status(), await started.text()).toBe(201);
  await expect.poll(() => sent?.hire?.character).toBe('research-archivist');
  const list = async () => (await (await request.get('/api/agents/dashboard/list')).json()).agents;
  await expect
    .poll(
      async () =>
        (await list()).find((a: any) => a.name === 'Downloads Manager')?.appearance?.character
          ?.catalog_id
    )
    .toBe('research-archivist');
  await expect
    .poll(async () => {
      const response = await request.get('/api/agents/dashboard/list?sort_by=name&order=asc');
      return (await response.json()).agents.map((a: any) => a.name);
    })
    .toContain('Downloads Manager');
  if (liveModel) {
    const tileFace = page.locator('.ws-map-tile-face [data-aa-character="research-archivist"]');
    await expect(tileFace).toBeVisible({ timeout: 15_000 });
    await capture(page, 'face-map-looking');
    const cleared = await request.post('/api/settings/system-model', {
      data: { provider: '', model: '' }
    });
    expect(cleared.ok(), await cleared.text()).toBeTruthy();
  }
  await page.goto('/agents');
  const roster = page.locator('.roster-card[data-name="Downloads Manager"]');
  await expect(roster).toBeVisible();
  await expect(roster.locator('[data-aa-requested="character"]')).toBeVisible();
  await capture(page, 'face-agents-saved');
  await expect
    .poll(
      async () => {
        const views = (await (await request.get(endpoint)).json()).folder_agents;
        return views.find((v: any) => v.folder_name === 'Downloads')?.state;
      },
      { timeout: 60_000 }
    )
    .toBe('reported');

  const generated = await feed('Plain Folder');
  await generated.getByRole('button', { name: 'Change face', exact: true }).click();
  await page.locator('#charPickerSkip').click();
  await expect(
    generated.locator('.folder-agent-card__who-face [data-aa-requested="generated"]')
  ).toBeVisible();
  await capture(page, 'face-generated');
  const generatedResponse = page.waitForResponse(response =>
    response.url().endsWith('/folder-agent/start')
  );
  await generated.locator('[data-folder-agent-action="start"]').click();
  const generatedStarted = await generatedResponse;
  expect(generatedStarted.status(), await generatedStarted.text()).toBe(201);
  await expect.poll(() => sent?.hire?.character).toBe('');
  await expect
    .poll(
      async () =>
        (await list()).find((a: any) => a.name === 'Plain Folder Manager')?.appearance?.mode
    )
    .toBe('generated');
});

test('Create Workspace keeps the face picker above the sheet with independent focus and Escape', async ({
  page,
  request
}) => {
  test.skip(
    !sandbox || !process.env.ORI_HIRE_CARD_PROVIDER,
    'requires an isolated server with a configured provider'
  );
  await installLocalCdn(page);
  const model = await request.post('/api/settings/system-model', {
    data: {
      provider: process.env.ORI_HIRE_CARD_PROVIDER,
      model: process.env.ORI_HIRE_CARD_MODEL || 'claude-sonnet-4-5'
    }
  });
  expect(model.ok(), await model.text()).toBeTruthy();
  const available = await request.get('/api/workspaces/build-sessions/availability');
  expect((await available.json()).available).toBe(true);
  // The sheet is a real Bootstrap modal: it must not steal picker focus or Escape.
  await page.goto('/');
  const close = page.locator('#personalAssistantClose');
  if (await close.isVisible()) await close.click();
  await page.locator('#cockpitCreateWorkspaceBtn').click();
  const sheet = page.locator('#addFolderModal');
  await expect(sheet).toBeVisible();
  await sheet.getByRole('button', { name: 'Add folder', exact: true }).click();
  await sheet.locator('#workspaceBuildFolderPath').fill(join(sandbox, 'Sheet Folder'));
  await sheet.getByRole('button', { name: 'Use this path', exact: true }).click();
  const sheetCard = sheet.locator('#workspaceBuildFolderAgent');
  await expect(sheetCard.locator('.folder-agent-card__title')).toHaveText(
    'Hire an agent for Sheet Folder'
  );
  await sheetCard.getByRole('button', { name: 'Change face', exact: true }).click();
  const search = page.locator('#charPickerSearch');
  await expect(search).toBeFocused();
  await search.fill('research');
  await expect(search).toHaveValue('research');
  await capture(page, 'face-picker-above-sheet');
  await page.keyboard.press('Escape');
  await expect(page.locator('#charPicker')).toHaveCount(0);
  await expect(sheet).toBeVisible();
});
