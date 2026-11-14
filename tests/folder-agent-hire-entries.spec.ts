import { expect, test, type Page } from '@playwright/test';
import { mkdir, writeFile } from 'node:fs/promises';
import { join, resolve } from 'node:path';
import { installLocalCdn } from './helpers/offline-cdn';

const sandbox = process.env.ORI_HIRE_ENTRIES_SANDBOX || '';
const evidence = resolve('tasks/evidence-folder-hire-card');
const endpoint = '/api/personal-assistant/folder-agent';
test.use({ viewport: { width: 1440, height: 1200 } });

async function capture(page: Page, name: string) {
  await page.screenshot({ path: join(evidence, `${name}.png`), fullPage: true });
}

// These are real host flows in a disposable HOME; no model or external app runs.
test('a workspace project-holder hire uses the same card; phone widths keep the picker usable', async ({
  page,
  request
}) => {
  test.skip(!sandbox, 'requires --sandbox-env ORI_HIRE_ENTRIES_SANDBOX');
  test.setTimeout(120_000);
  await installLocalCdn(page);
  await mkdir(evidence, { recursive: true, mode: 0o750 });
  for (const name of ['Downloads', 'Desktop', 'Documents', 'Narrow Folder'])
    await mkdir(join(sandbox, name), { recursive: true, mode: 0o750 });
  for (let n = 0; n < 7; n++) {
    const folder = join(sandbox, 'Documents', 'Projects', `Project-${n}`);
    await mkdir(folder, { recursive: true, mode: 0o750 });
    await writeFile(join(folder, 'go.mod'), `module example.test/project${n}\ngo 1.26\n`, {
      mode: 0o600
    });
  }
  await request.post('/api/onboarding/skip');
  const assistant = (await (await request.get('/api/personal-assistant')).json())
    .personal_assistant;
  const hired = await request.post('/api/personal-assistant/hire', {
    data: {
      request_id: 'hire-entries-assistant',
      if_version: assistant.state_version || 0,
      display_name: 'Atlas',
      mandate: 'Help with folders.',
      focus_areas: ['plan_my_day']
    }
  });
  expect(hired.status(), await hired.text()).toBe(201);
  const hq = await request.post('/api/personal-assistant/hq', {
    data: {
      request_id: 'hire-entries-hq',
      if_version: (await hired.json()).personal_assistant.state_version,
      name: 'My HQ',
      timezone: 'UTC'
    }
  });
  expect(hq.status(), await hq.text()).toBe(201);
  for (const name of [
    'Albums Manager for recordings and reviews',
    'Albums Manager for recordings and releases',
    'Birch',
    'Cedar',
    'Dove',
    'Elm',
    'Fir'
  ]) {
    const response = await request.post('/api/agents', {
      data: { name, catalog_role: 'orchestrator' }
    });
    expect(response.ok(), await response.text()).toBeTruthy();
  }
  const views = async () => (await (await request.get(endpoint)).json()).folder_agents;
  async function feed(folder: string) {
    await page.goto('/?panel=today&folder=show');
    const entry = page.locator('#personalAssistantFolderPathEntry');
    await entry.locator('input').fill(join(sandbox, folder));
    await entry.getByRole('button').click();
    const card = page.locator('#personalAssistantFolderAgent');
    await expect(card.locator('.folder-agent-card__title')).toHaveText(
      `Hire an agent for ${folder}`
    );
    return card;
  }
  const parentCard = await feed('Documents');
  await expect(parentCard.locator('.folder-agent-card__who-customize')).not.toHaveAttribute(
    'open',
    ''
  );
  await expect(parentCard.locator('.folder-agent-card__who-reuse')).not.toHaveAttribute('open', '');
  let response = page.waitForResponse(r => r.url().endsWith('/folder-agent/start'));
  await parentCard.locator('[data-folder-agent-action="start"]').click();
  expect((await response).status()).toBe(201);
  await expect
    .poll(async () => (await views()).find((v: any) => v.folder_name === 'Documents')?.state)
    .toBe('reported');
  const parent = (await views()).find((v: any) => v.folder_name === 'Documents');
  expect(parent.project_holders.rows).toHaveLength(1);
  await page.goto(parent.route);
  const close = page.locator('#personalAssistantClose');
  if (await close.isVisible()) await close.click();
  await page.getByRole('button', { name: 'Open in chat', exact: true }).click();
  const report = page.locator('.folder-agent-card--report:visible');
  const holders = report.locator('.folder-agent-card__project-holders');
  await holders.getByRole('button', { name: 'Set up Projects', exact: true }).click();
  const child = holders.locator('[data-folder-agent-card="choose"]');
  await expect(child.locator('.folder-agent-card__title')).toHaveText('Hire an agent for Projects');
  await expect(child.locator('[data-folder-agent-action="start"]')).toHaveText('Hire & build');
  await expect(child.locator('.folder-agent-card__who-reuse')).not.toHaveAttribute('open', '');
  await child.locator('.folder-agent-card__who-customize-toggle').click();
  await child.locator('.folder-agent-card__who-change-face').click();
  await page.locator('[data-char-id="operations-keeper"]').click();
  await page.locator('#charPickerConfirm').click();
  await expect(child.locator('[data-aa-character="operations-keeper"]')).toBeVisible();
  await capture(page, 'entry-workspace-child-hire');
  response = page.waitForResponse(r => r.url().endsWith('/folder-agent/start'));
  await child.locator('[data-folder-agent-action="start"]').click();
  const started = await response;
  expect(started.status(), await started.text()).toBe(201);
  await expect
    .poll(async () => (await views()).some((v: any) => v.folder_name === 'Projects'))
    .toBe(true);
  const records = (await (await request.get('/api/agents/dashboard/list')).json()).agents;
  expect(
    records.find((a: any) => a.name === 'Projects Manager')?.appearance?.character?.catalog_id
  ).toBe('operations-keeper');
  await capture(page, 'entry-workspace-child-receipt');

  for (const width of [760, 390]) {
    await page.setViewportSize({ width, height: 1000 });
    for (const theme of ['light', 'dark']) {
      const card = await feed('Narrow Folder');
      await page.evaluate(theme => {
        document.documentElement.setAttribute('data-bs-theme', theme);
        document.documentElement.setAttribute('data-theme', theme);
      }, theme);
      expect(await card.evaluate(el => el.scrollWidth <= el.clientWidth + 1)).toBe(true);
      await card.locator('.folder-agent-card__who-heading').click();
      expect(await card.evaluate(el => el.scrollWidth <= el.clientWidth + 1)).toBe(true);
      await card.locator('.folder-agent-card__who-face').click();
      const picker = page.locator('#charPicker');
      await expect(picker).toBeVisible();
      const search = picker.locator('#charPickerSearch');
      await expect(search).toBeFocused();
      await search.fill('research');
      await expect(search).toHaveValue('research');
      expect(await picker.evaluate(el => el.scrollWidth <= el.clientWidth + 1)).toBe(true);
      await capture(page, `entry-picker-${width}-${theme}`);
      await page.keyboard.press('Escape');
      await expect(picker).toHaveCount(0);
      await expect(card).toBeVisible();
      await card.locator('[data-folder-agent-action="start"]').scrollIntoViewIfNeeded();
      await capture(page, `entry-card-${width}-${theme}`);
    }
  }
});
