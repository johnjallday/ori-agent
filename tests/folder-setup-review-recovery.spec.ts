import { expect, test, type APIRequestContext, type Page, type TestInfo } from '@playwright/test';
import { randomUUID } from 'node:crypto';
import { basename, join } from 'node:path';
import { mkdirSync, readFileSync, realpathSync, writeFileSync } from 'node:fs';
import { chooseAttach } from './helpers/assistant-menus';
import { stageReviewedFolderPackages } from './helpers/reviewed-folder-packages';
import { watchFolderRequests, expectNoFolderExecution } from './helpers/folder-aware-collection';

// Real isolated host, no paid provider, plugin installation or setup execution.
// ./scripts/e2e-fresh.sh --sandbox-env ORI_RECOVERY_SANDBOX tests/folder-setup-review-recovery.spec.ts
const sandbox = process.env.ORI_RECOVERY_SANDBOX || '';
test.describe.configure({ mode: 'serial' });
test.setTimeout(90_000);

async function ok(response: Awaited<ReturnType<APIRequestContext['get']>>) {
  const text = await response.text();
  expect(response.ok(), `${response.url()}: ${text}`).toBeTruthy();
  return text ? JSON.parse(text) : {};
}

async function hire(request: APIRequestContext) {
  await ok(await request.post('/api/onboarding/skip'));
  await ok(await request.post('/api/settings/workspace-root', { data: { workspace_root: '' } }));
  let pa = (await ok(await request.get('/api/personal-assistant'))).personal_assistant;
  if (pa.state === 'needs_hire') {
    await ok(await request.post('/api/personal-assistant/hire', { data: {
      request_id: randomUUID(), if_version: pa.state_version || 0, display_name: 'Atlas',
      mandate: 'Keep my projects moving.', focus_areas: ['plan_my_day']
    } }));
    pa = (await ok(await request.get('/api/personal-assistant'))).personal_assistant;
  }
  if (!pa.hq_workspace_id)
    await ok(await request.post('/api/personal-assistant/hq', { data: {
      request_id: randomUUID(), if_version: pa.state_version, name: 'My HQ', timezone: 'UTC'
    } }));
}

function seed(name: string) {
  const root = join(sandbox, 'Music', name);
  mkdirSync(root, { recursive: true, mode: 0o750 });
  writeFileSync(join(root, 'song.rpp'), '<REAPER_PROJECT 0.1 7.0 1\n>\n', { mode: 0o600 });
  return root;
}

async function review(request: APIRequestContext, name: string) {
  const path = seed(name);
  const picked = await ok(await request.post('/api/folder-picker/select-path', { data: { path } }));
  const draft_id = randomUUID();
  const selected = await ok(await request.post('/api/home-assistant/folder-context/select', { data: {
    draft_id, revision: '', mode: 'selection', selection_token: picked.selection_token
  } }));
  return ok(await request.post('/api/home-assistant/folder-context/review', { data: {
    draft_id, revision: '', selection_id: selected.observation.id, candidate_id: 'candidate-0'
  } }));
}

async function attach(page: Page, name = 'Album-3') {
  await page.goto('/');
  await page.locator('#personalAssistantLauncher').click();
  await chooseAttach(page, '#personalAssistantFolderChip');
  await page.locator('#personalAssistantFolderChoices .folder-path-entry__input').fill(seed(name));
  await page.locator('#personalAssistantFolderChoices .folder-path-entry__use').click();
  await expect(page.locator('#personalAssistantActiveFolderName')).toHaveText(name);
}

async function screenshot(page: Page, info: TestInfo, name: string) {
  const dir = process.env.ORI_RECOVERY_EVIDENCE;
  if (dir) mkdirSync(dir, { recursive: true });
  await page.screenshot({ path: dir ? join(dir, `${name}.png`) : info.outputPath(`${name}.png`) });
}

const digestPath = () => join(sandbox, 'Ori Workspaces', 'my-hq', '.ori', 'folder-digest-v1.json');
const storedDigest = () => readFileSync(digestPath(), 'utf8');

async function state(request: APIRequestContext) {
  return {
    digest: storedDigest(),
    workspaces: await ok(await request.get('/api/workspaces')),
    conversations: (await ok(await request.get('/api/home-assistant/conversations'))).conversations.length
  };
}

test.beforeAll(async ({ request }) => {
  expect(sandbox, 'requires --sandbox-env ORI_RECOVERY_SANDBOX; never use the real HOME').toBeTruthy();
  expect(basename(realpathSync(sandbox))).toMatch(/^ori-(e2e|demo)\./);
  await hire(request);
  // Prepare published reviewed packages in this disposable host only, before
  // the request/state guard. Navigation itself must never install anything.
  const plugins = (await ok(await request.get('/api/plugins'))).plugins || [];
  if (!plugins.some((plugin: any) => plugin.name === 'reaper-plugin'))
    await stageReviewedFolderPackages(request);
});

test('Album-3 returns from Album-5 to its normal visual review, preserving the draft', async ({ page, request }, info) => {
  const blocking = await review(request, 'Album-5');
  const before = await state(request);
  const seen = watchFolderRequests(page);
  await attach(page);
  const notice = page.locator('#personalAssistantFolderStructure');
  await expect(notice).toContainText('Album-5 · Waiting for review');
  const draft = 'Keep this exact Album-3 draft unsent 🎼';
  await page.locator('#personalAssistantInput').fill(draft);
  const selection = await page.evaluate(() => (window as any).PersonalAssistantFolderContext.current().observation.id);
  await screenshot(page, info, 'blocked');
  expect(await state(request)).toEqual(before);
  await notice.getByRole('button', { name: 'Open Album-5 review', exact: true }).click();
  await expect(page.locator('#personalAssistantFolderReturn')).toBeVisible();
  const card = page.locator('#personalAssistantFolderOffer');
  await expect(card).toContainText('Album-5');
  expect(await page.evaluate(() => (window as any).PersonalAssistantConversation.currentId())).toBe(blocking.conversation.id);
  expect(await state(request)).toEqual(before);
  await screenshot(page, info, 'opened');
  await card.getByRole('button', { name: 'Keep chatting', exact: true }).click();
  await expect(card).toBeHidden();
  const closed = await state(request);
  await page.locator('#personalAssistantFolderReturn').click();
  await expect(page.locator('#personalAssistantFolderReturn')).toBeHidden();
  await expect(page.locator('#personalAssistantInput')).toHaveValue(draft);
  await expect(page.getByRole('button', { name: 'Review workspace setup', exact: true })).toBeVisible();
  expect(await page.evaluate(() => (window as any).PersonalAssistantFolderContext.current().observation.id)).toBe(selection);
  expect(await state(request)).toEqual(closed);
  await screenshot(page, info, 'returned');
  await page.getByRole('button', { name: 'Review workspace setup', exact: true }).click();
  await expect(card).toBeVisible();
  await expect(card).toContainText('Album-3');
  const details = card.locator('#personalAssistantFolderDetails');
  if (!(await details.evaluate(node => (node as HTMLDetailsElement).open)))
    await details.getByText('Full setup details', { exact: true }).click();
  await expect(card.locator('.folder-visual')).toBeVisible();
  await card.locator('.folder-visual').scrollIntoViewIfNeeded();
  await screenshot(page, info, 'new-recommendation');
  expectNoFolderExecution(seen);
  expect(seen.posts.filter(item => item.path.endsWith('/folder-context/select'))).toHaveLength(1);
  expect(seen.posts.filter(item => /\/scan|\/build-sessions/.test(item.path))).toEqual([]);
  // Retire this review using its real control, keeping later cases independent.
  await card.getByRole('button', { name: 'Keep chatting', exact: true }).click();
  await expect(card).toBeHidden();
});
