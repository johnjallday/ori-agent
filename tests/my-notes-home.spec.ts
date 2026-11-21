import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import { join } from 'node:path';
import { installLocalCdn } from './helpers/offline-cdn';

const endpoint = '/api/personal-assistant/second-brain';
const evidence = process.env.MY_NOTES_EVIDENCE;
const sandbox = process.env.MY_NOTES_SANDBOX;

test.describe.configure({ mode: 'serial' });
test.skip(!sandbox, 'Use a fresh disposable wt demo; set MY_NOTES_SANDBOX to its printed root.');
test.beforeEach(async ({ page }) => installLocalCdn(page));

async function capture(page: Page, name: string) {
  if (evidence) await page.screenshot({ path: join(evidence, `${name}.png`), fullPage: true });
}

async function post(request: APIRequestContext, path: string, data: unknown) {
  const response = await request.post(path, { data });
  expect(response.ok(), `${path}: ${await response.text()}`).toBeTruthy();
  return response.json();
}

async function notes(request: APIRequestContext) {
  const response = await request.get(`${endpoint}?view=connection`);
  expect(response.ok(), await response.text()).toBeTruthy();
  return (await response.json()).second_brain;
}

test('Home Map and Tree link to an independent, refreshable My Notes page', async ({
  page,
  request
}) => {
  await post(request, '/api/onboarding/complete', {});
  const before = await notes(request);
  expect(before.setup_required).toBe('assistant');
  await page.goto('/my-notes');
  const heading = page.getByRole('heading', { name: 'My Notes', exact: true });
  await expect(heading).toBeFocused();
  await expect(page.getByRole('heading', { name: 'Meet your assistant first' })).toBeVisible();
  await capture(page, '01-setup-needed');
  await page.goto('/');
  for (const mode of ['Map', 'Tree']) {
    await page.getByRole('button', { name: mode, exact: true }).click();
    await expect(page.locator('#homeCockpit')).toHaveAttribute('data-view', mode.toLowerCase());
    await page.getByRole('link', { name: 'My Notes' }).click();
    await expect(page).toHaveURL(/\/my-notes$/);
    await expect(heading).toBeFocused();
    await page.reload();
    await expect(heading).toBeFocused();
    await page.goBack();
    await expect(page.locator('#homeCockpit')).toHaveAttribute('data-view', mode.toLowerCase());
  }
  expect(await notes(request)).toEqual(before);
});

test('explicit notebook setup is reusable and page entry only reads the connection', async ({
  page,
  request
}) => {
  await post(request, '/api/settings/workspace-root', {
    workspace_root: join(sandbox!, 'Ori Workspaces')
  });
  await post(request, '/api/personal-assistant/hire', {
    request_id: 'notes-test-hire',
    if_version: 0,
    display_name: 'Atlas',
    mandate: 'Keep my projects moving and remember what I am working on.',
    focus_areas: ['keep_projects_moving']
  });
  await page.goto('/my-notes');
  await expect(page.getByRole('heading', { name: 'Build My HQ first' })).toBeVisible();
  await capture(page, '02-hq-required');
  const assistant = await request.get('/api/personal-assistant');
  expect(assistant.ok()).toBeTruthy();
  const state = await assistant.json();
  await post(request, '/api/personal-assistant/hq', {
    request_id: 'notes-test-hq',
    if_version: state.personal_assistant?.state_version ?? 2,
    name: 'Personal HQ',
    timezone: 'Asia/Seoul',
    schedule_days: ['mon'],
    schedule_time: '08:00',
    scope: 'all',
    include_future_workspaces: true,
    notify_on_ready: false
  });
  await page.reload();
  await expect(page.getByRole('heading', { name: 'No notes folder connected' })).toBeVisible();
  await capture(page, '03-no-folder');
  await page.getByRole('button', { name: 'Set up notes…' }).click();
  const card = page.locator('#personalAssistantSecondBrain');
  await expect(card.getByRole('button', { name: 'Set up my notes' })).toBeFocused();
  expect((await notes(request)).status).toBe('not_connected');
  await card.getByRole('button', { name: 'Set up my notes' }).click();
  await card.locator('[data-second-brain-action="create"]').click();
  await expect.poll(async () => (await notes(request)).status).toBe('connected');
  const source = await notes(request);
  expect(source.source.origin).toBe('created_by_ori');
  const reads: string[] = [];
  page.on('request', req => {
    if (req.url().includes(endpoint))
      reads.push(`${req.method()} ${new URL(req.url()).search || new URL(req.url()).pathname}`);
  });
  await page.reload();
  await expect(
    page.getByRole('heading', { name: source.source.name, level: 2, exact: true })
  ).toBeVisible();
  await expect(page.locator('#personalAssistantPanel')).toBeHidden();
  await capture(page, '04-connected-created');
  expect(reads).toEqual(['GET ?view=connection']);
  expect(await notes(request)).toEqual(source);
  await page.getByRole('button', { name: 'Inbox', exact: true }).click();
  await expect(page.getByRole('tab', { name: /Inbox/ })).toBeFocused();
  await expect(page.getByRole('button', { name: 'Save to Inbox' })).toBeVisible();
});
