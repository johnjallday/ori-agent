import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import { join } from 'node:path';
import { mkdirSync, readFileSync, readdirSync, renameSync, statSync, writeFileSync } from 'node:fs';
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

test('HQ Overview saves to the personal Inbox and its Map station navigates', async ({
  page,
  request
}) => {
  const status = await request.get('/api/personal-assistant/today');
  expect(status.ok()).toBeTruthy();
  const data = await status.json();
  const slug = data.today?.hq_workspace_slug;
  expect(slug).toBeTruthy();
  const base = `/workspaces/${slug}`;
  const workspaceNotesURL = `/api/workspaces/${data.today.hq_workspace_id}/notes`;
  const workspaceNotes = async () => {
    const response = await request.get(workspaceNotesURL);
    expect(response.ok(), await response.text()).toBeTruthy();
    return (await response.json()).notes;
  };
  const before = await workspaceNotes();
  await page.goto(base + '?mode=details');
  const capturePanel = page.locator('.ws-hq-capture');
  const draft = capturePanel.getByLabel('Save to My Notes → Inbox');
  await expect(draft).toBeVisible();
  const text = 'Idea from HQ about next week';
  await draft.fill(text);
  // Force a background status refresh/repaint: it must not clear the field.
  await page.evaluate(() =>
    document.dispatchEvent(new CustomEvent('personal-assistant:second-brain'))
  );
  await expect(draft).toHaveValue(text);
  await expect(draft).toBeFocused();
  await page.route(
    `**${endpoint}/capture`,
    route => route.fulfill({ status: 500, json: { error: 'Try the save again.' } }),
    { times: 1 }
  );
  await draft.press('Enter');
  await expect(capturePanel.getByRole('alert')).toHaveText('Try the save again.');
  await expect(draft).toHaveValue(text);
  await draft.press('Enter');
  await expect(capturePanel.getByRole('status')).toHaveText('Saved to My Notes → Inbox.');
  expect(await workspaceNotes()).toEqual(before);
  expect(await capturePanel.getByRole('tab').count()).toBe(0);
  await capture(page, '08-hq-quick-capture');
  await capturePanel.getByRole('link', { name: 'Today’s journal' }).click();
  const journal = page.locator('.hq-journal-editor');
  await expect(journal).toBeVisible();
  await journal.fill('Reviewed the field notebook and planned the week.');
  await capture(page, '09-journal');
  await page.getByRole('button', { name: 'Save journal' }).click();
  await expect(page.locator('.hq-journal-launch')).toBeVisible();
  await expect.poll(workspaceNotes).toHaveLength(before.length + 1);
  const source = await notes(request);
  const daily = join(sandbox!, 'Ori Workspaces', 'My Notes', 'Daily');
  expect(source.source.path).toBe(join(daily, '..'));
  expect(snapshot(daily).join('\n')).toContain('Reviewed the field notebook and planned the week.');
  await page.goBack();
  await capturePanel.getByRole('link', { name: 'Open My Notes' }).click();
  await page.getByRole('button', { name: 'Inbox', exact: true }).click();
  await expect(page.locator('[data-notes-section="inbox"]')).toContainText(/idea from hq/i);
  await capture(page, '10-hq-note-in-personal-inbox');
  await page.goto(base + '?mode=map');
  await page
    .locator('[data-cmd-hq-station="second-brain"]')
    .filter({ visible: true })
    .first()
    .click();
  await expect(page).toHaveURL(/\/my-notes$/);
  await page.goBack();
  await expect(page).toHaveURL(new RegExp(`/workspaces/${slug}.*mode=map`));
  await page.goto(base + '?mode=map&station=second-brain');
  await expect(page).toHaveURL(/\/my-notes$/);
});

function snapshot(root: string): string[] {
  return readdirSync(root, { withFileTypes: true })
    .sort((a, b) => a.name.localeCompare(b.name))
    .flatMap(entry => {
      const path = join(root, entry.name);
      if (entry.isDirectory()) return [path, ...snapshot(path)];
      return [`${path}: ${statSync(path).mtimeMs} ${readFileSync(path, 'utf8')}`];
    });
}

test('browse a created notebook on demand, with guarded preview and folder fallback', async ({
  page,
  request
}) => {
  const source = await notes(request);
  const root = join(sandbox!, 'Ori Workspaces', 'My Notes');
  expect(source.source.path).toBe(root);
  const text = 'A fresh thought, saved for later.';
  const captureBody = await post(request, `${endpoint}/capture`, {
    text,
    request_id: 'browse-preview-capture'
  });
  const file = captureBody.capture.note.split('/').at(-1);
  const inbox = join(root, 'Inbox');
  const nested = join(root, 'Projects', 'Research', 'Drafts');
  mkdirSync(nested, { recursive: true });
  writeFileSync(join(inbox, 'personal.md'), 'A personal note');
  writeFileSync(join(root, 'Projects', 'Research', 'scratch.md'), 'Private project notes');
  const before = snapshot(root);
  await page.goto('/my-notes');
  await page.getByRole('button', { name: 'Browse notebook' }).click();
  const browser = page.getByRole('region', { name: 'Browse notebook' });
  await expect(browser.getByRole('heading', { name: 'Inbox' })).toBeFocused();
  await browser.getByRole('button', { name: `Check Ori preview for ${file}` }).click();
  await expect(browser.getByText(text)).toBeVisible();
  await capture(page, '05-inbox-preview');
  await browser.getByRole('button', { name: 'Check Ori preview for personal.md' }).click();
  await expect(browser.getByText(/That note is yours/)).toBeVisible();
  await browser.getByRole('button', { name: 'Open notebook folder' }).click();
  await expect(page.getByText('Folder handoff requested.')).toBeAttached();
  await browser.getByRole('navigation').getByRole('button', { name: 'My Notes' }).click();
  for (const folder of ['Inbox', 'Daily', 'Projects', 'People', 'Areas', 'Resources', 'Archive']) {
    await expect(browser.getByRole('button', { name: folder, exact: true })).toBeVisible();
  }
  await browser.getByRole('button', { name: 'Projects', exact: true }).click();
  await browser.getByRole('button', { name: 'Research', exact: true }).click();
  await expect(browser.getByText('scratch.md')).toBeVisible();
  await expect(browser.getByRole('button', { name: /preview/ })).toHaveCount(0);
  await capture(page, '06-nested-project');
  await browser.getByRole('button', { name: 'Drafts', exact: true }).click();
  await expect(browser.getByText('No folders or Markdown notes here.')).toBeVisible();
  expect(snapshot(root)).toEqual(before);
  renameSync(nested, nested + '-moved');
  await browser
    .getByRole('navigation')
    .getByRole('button', { name: 'Research', exact: true })
    .click();
  await expect(browser.getByRole('button', { name: 'Drafts-moved', exact: true })).toBeVisible();
  renameSync(nested + '-moved', nested);
  await browser.getByRole('button', { name: 'Drafts-moved', exact: true }).click();
  await expect(browser.getByRole('alert')).toContainText('moved or removed');
  writeFileSync(join(inbox, file), `${readFileSync(join(inbox, file), 'utf8')}\nMy edits.`);
  await page.getByRole('button', { name: 'Browse notebook' }).click();
  await browser.getByRole('button', { name: `Check Ori preview for ${file}` }).click();
  await expect(browser.getByText(/That note is yours/)).toBeVisible();
});

test('browse a connected vault with its own layout without scanning or writing', async ({
  page,
  request
}) => {
  const source = await notes(request);
  await post(request, `${endpoint}/disconnect`, {
    request_id: 'browse-test-disconnect',
    if_version: source.version
  });
  const root = join(sandbox!, 'Documents', 'Field Notebook');
  mkdirSync(join(root, '.obsidian'), { recursive: true });
  mkdirSync(join(root, 'Expeditions'));
  writeFileSync(join(root, 'Expeditions', 'Canyon.md'), 'Not an Ori note.');
  const pick = await post(request, '/api/folder-picker/select-path', { path: root });
  await post(request, `${endpoint}/connect`, {
    selection_token: pick.selection_token,
    request_id: 'browse-test-vault'
  });
  const before = snapshot(root);
  await page.goto('/my-notes');
  await expect(page.getByText('Connected folder · read-only for Ori')).toBeVisible();
  await page.getByRole('button', { name: 'Browse notebook' }).click();
  const browser = page.getByRole('region', { name: 'Browse notebook' });
  await expect(browser.getByRole('button', { name: /Inbox|Projects|Save/ })).toHaveCount(0);
  await browser.getByRole('button', { name: 'Expeditions' }).click();
  await expect(browser.getByRole('link', { name: 'Open Canyon.md in Obsidian' })).toHaveAttribute(
    'href',
    /^obsidian:\/\/open/
  );
  await expect(browser.getByRole('button', { name: /preview/ })).toHaveCount(0);
  await capture(page, '07-read-only-vault');
  expect(snapshot(root)).toEqual(before);
});
