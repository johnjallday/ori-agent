import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import { join } from 'node:path';
import {
  mkdirSync,
  readFileSync,
  readdirSync,
  realpathSync,
  renameSync,
  statSync,
  writeFileSync
} from 'node:fs';
import { installLocalCdn } from './helpers/offline-cdn';
import { goToDestination } from './helpers/home-destination';

const endpoint = '/api/personal-assistant/second-brain';
const evidence = process.env.MY_NOTES_EVIDENCE;
const sandbox = process.env.MY_NOTES_SANDBOX;

test.describe.configure({ mode: 'serial' });
test.skip(!sandbox, 'Use a fresh disposable wt demo; set MY_NOTES_SANDBOX to its printed root.');
test.beforeEach(async ({ baseURL, page, request }, info) => {
  expect(new URL(baseURL!).hostname).toBe('localhost');
  expect(readFileSync(join(sandbox!, '.ori-demo-owner'), 'utf8').trim()).toBe(
    `worktree=${realpathSync(process.cwd())}`
  );
  const root = await request.get('/api/settings/workspace-root');
  expect(root.ok(), await root.text()).toBeTruthy();
  const data = await root.json();
  if (data.confirmed) {
    expect(realpathSync(data.effective_workspace_root)).toBe(
      realpathSync(join(sandbox!, 'Ori Workspaces'))
    );
  } else {
    expect(info.title).toMatch(/^(Home Map and Tree|explicit notebook setup)/);
  }
  await installLocalCdn(page);
});

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

// Every notes request the page makes, as "METHOD /path?query" below the notes
// endpoint. Arrival may only read; anything else here is a regression.
function recordNotesRequests(page: Page) {
  const seen: string[] = [];
  page.on('request', req => {
    const url = new URL(req.url());
    if (url.pathname.startsWith(endpoint))
      seen.push(`${req.method()} ${url.pathname.slice(endpoint.length)}${url.search}`);
  });
  return seen;
}

const notebookBrowser = (page: Page) => page.getByRole('region', { name: 'Notebook browser' });
const folders = (page: Page) => notebookBrowser(page).getByRole('navigation', { name: 'Folders' });
const noteList = (page: Page) => notebookBrowser(page).locator('.my-notes-list');
const reader = (page: Page) => notebookBrowser(page).locator('.my-notes-reader');

async function listSettled(page: Page) {
  await expect(noteList(page).getByText('Loading names…')).toHaveCount(0);
  await expect(folders(page).getByText('Loading…')).toHaveCount(0);
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
  // Each Home destination in the Desk rail, and the view it leaves in the cockpit.
  for (const [destination, view] of [
    ['map', 'map'],
    ['files', 'tree']
  ] as const) {
    await goToDestination(page, destination);
    await expect(page.locator('#homeCockpit')).toHaveAttribute('data-view', view);
    await page.getByRole('link', { name: 'My Notes' }).click();
    await expect(page).toHaveURL(/\/my-notes$/);
    await expect(heading).toBeFocused();
    await page.reload();
    await expect(heading).toBeFocused();
    await page.goBack();
    await expect(page.locator('#homeCockpit')).toHaveAttribute('data-view', view);
  }
  expect(await notes(request)).toEqual(before);
});

test('explicit notebook setup is reusable and arrival reads only the connection and two folders', async ({
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
  const reads = recordNotesRequests(page);
  await page.reload();
  await expect(
    page.getByRole('heading', { name: source.source.name, level: 2, exact: true })
  ).toBeVisible();
  // Arrival opens the notebook at Inbox, without a Browse step or settings.
  await expect(page.getByRole('button', { name: 'Browse notebook' })).toHaveCount(0);
  await expect(noteList(page).getByRole('heading', { name: 'Inbox', level: 3 })).toBeVisible();
  await expect(folders(page).getByRole('button', { name: 'Inbox', exact: true })).toHaveAttribute(
    'aria-current',
    'true'
  );
  await listSettled(page);
  await expect(page.locator('#personalAssistantPanel')).toBeHidden();
  await expect(page.getByRole('heading', { name: 'My Notes', level: 1 })).toBeFocused();
  await capture(page, '04-connected-created');
  // The connection, the root (for Folders) and Inbox: nothing recursive, no
  // note bodies, no writes and no look or index started by arriving.
  const ref = encodeURIComponent(source.browse_ref);
  expect([...reads].sort()).toEqual(
    ['GET ?view=connection', `GET /browse?ref=${ref}`, `GET /browse?ref=${ref}&part=Inbox`].sort()
  );
  expect(await notes(request)).toEqual(source);
  // The explicit Inbox destination still opens its view and capture box.
  await page.goto('/my-notes?view=inbox');
  await expect(page.getByRole('tab', { name: /Inbox/, selected: true })).toBeVisible();
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
  // My Notes opens on Inbox, where the HQ capture landed.
  await expect(noteList(page).getByRole('button', { name: /idea-from-hq/i })).toBeVisible();
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

test('a Workspace Note stays in workspace A and a personal capture stays in My Notes', async ({
  page,
  request
}) => {
  const create = async (name: string) => {
    const created = await post(request, '/api/workspaces', {
      name,
      blank: true,
      create_template_agents: true
    });
    const workspace = created.folder || created.workspace || created;
    expect(workspace.id).toBeTruthy();
    expect(workspace.folder_slug).toBeTruthy();
    return workspace;
  };
  const a = await create('Notes separation A');
  const b = await create('Notes separation B');
  const title = 'Plot notes exclusive to workspace A';
  const result = await post(request, `/api/workspaces/${a.id}/notes`, {
    name: title,
    content: 'A workspace-only observation.'
  });
  const id = result.note?.id || result.id;
  expect(id).toBeTruthy();
  const list = async (workspaceID: string) => {
    const response = await request.get(`/api/workspaces/${workspaceID}/notes`);
    expect(response.ok(), await response.text()).toBeTruthy();
    return (await response.json()).notes;
  };
  const beforeA = await list(a.id);
  const beforeB = await list(b.id);
  expect(beforeA.map(note => note.name)).toEqual([title]);
  expect(beforeB).toEqual([]);
  await page.goto(`/workspaces/${a.folder_slug}/notes/${id}`);
  await expect(page.locator('#noteNameInput')).toHaveValue(title);
  await capture(page, '11-workspace-note-a');
  await page.goto(`/workspaces/${b.folder_slug}/notes`);
  await expect(page.getByText(title, { exact: true })).toHaveCount(0);
  await page.goto('/');
  await page.getByRole('link', { name: 'My Notes' }).click();
  await expect(noteList(page).getByRole('heading', { name: 'Inbox', level: 3 })).toBeVisible();
  await page.goto('/my-notes?view=inbox');
  const text = 'A personal reminder, not a workspace document';
  const tools = page.locator('[data-my-notes-tools]');
  const inbox = tools.locator('[data-notes-section="inbox"]');
  await expect(inbox).not.toContainText(title);
  await tools.getByRole('textbox', { name: 'Quick note for Inbox' }).fill(text);
  await tools.getByRole('button', { name: 'Save to Inbox' }).click();
  await expect(inbox).toContainText(text);
  expect(await list(a.id)).toEqual(beforeA);
  expect(await list(b.id)).toEqual(beforeB);
  const personal = await notes(request);
  await page.setViewportSize({ width: 390, height: 760 });
  for (const theme of ['light', 'dark']) {
    await page.evaluate(value => localStorage.setItem('ori-theme', value), theme);
    await page.goto('/my-notes?view=inbox');
    await expect(page.locator('html')).toHaveAttribute('data-bs-theme', theme);
    await expect(inbox).toContainText(text);
    await expect(inbox).not.toContainText(title);
    expect(
      await page
        .locator('.my-notes-sheet')
        .evaluate(el => el.getBoundingClientRect().right <= innerWidth)
    ).toBe(true);
    await capture(page, `12-personal-inbox-${theme}-390`);
  }
  expect((await notes(request)).browse_ref).toEqual(personal.browse_ref);
});

test('a created notebook opens on Inbox; Projects → House reads the project note', async ({
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
  // A real project note Ori wrote, through the reviewed fill-in consent.
  await post(request, `${endpoint}/fill-in`, { decision: 'allow', request_id: 'browse-fill-in' });
  await post(request, `${endpoint}/project`, {
    name: 'House',
    answer: 'Renovate the kitchen before spring.',
    request_id: 'browse-house-project'
  });
  const inbox = join(root, 'Inbox');
  const nested = join(root, 'Projects', 'Research', 'Drafts');
  mkdirSync(nested, { recursive: true });
  writeFileSync(join(inbox, 'personal.md'), 'A personal note');
  writeFileSync(join(root, 'Projects', 'Research', 'scratch.md'), 'Private project notes');
  const before = snapshot(root);
  await page.goto('/my-notes');
  await expect(noteList(page).getByRole('heading', { name: 'Inbox', level: 3 })).toBeVisible();
  await noteList(page).getByRole('button', { name: file, exact: true }).click();
  await expect(reader(page).getByText(text)).toBeVisible();
  await expect(reader(page).getByText(`Inbox / ${file}`)).toBeVisible();
  await expect(noteList(page).getByRole('button', { name: file, exact: true })).toBeFocused();
  await capture(page, '05-inbox-preview');
  await noteList(page).getByRole('button', { name: 'personal.md', exact: true }).click();
  await expect(reader(page).getByText(/That note is yours/)).toBeVisible();
  // Ori sets its notebook up as a vault: the vault-relative link, never a path.
  expect(source.source.kind).toBe('obsidian');
  await expect(
    reader(page).getByRole('link', { name: 'Open personal.md in Obsidian' })
  ).toHaveAttribute(
    'href',
    `obsidian://open?vault=${encodeURIComponent(source.source.name)}&file=Inbox%2Fpersonal`
  );
  await reader(page).getByRole('button', { name: 'Open notebook folder' }).click();
  await expect(page.getByText('Folder handoff requested.')).toBeAttached();

  await folders(page).getByRole('button', { name: 'My Notes', exact: true }).click();
  for (const folder of ['Inbox', 'Daily', 'Projects', 'People', 'Areas', 'Resources', 'Archive']) {
    await expect(folders(page).getByRole('button', { name: folder, exact: true })).toBeVisible();
  }
  await expect(reader(page).getByRole('heading', { name: 'No note open' })).toBeVisible();
  await folders(page).getByRole('button', { name: 'Projects', exact: true }).click();
  await folders(page).getByRole('button', { name: 'House', exact: true }).click();
  await noteList(page).getByRole('button', { name: 'README.md', exact: true }).click();
  await expect(reader(page).getByRole('heading', { level: 3 })).not.toHaveText('No note open');
  await expect(reader(page).getByText('Renovate the kitchen before spring.')).toBeVisible();
  await expect(reader(page).getByText('Projects / House / README.md')).toBeVisible();
  await capture(page, '06-house-project-note');

  await folders(page).getByRole('button', { name: 'Research', exact: true }).click();
  await noteList(page).getByRole('button', { name: 'scratch.md', exact: true }).click();
  await expect(reader(page).getByText(/notes it wrote in Inbox, Daily/)).toBeVisible();
  await expect(reader(page).getByText('Private project notes')).toHaveCount(0);
  await capture(page, '06b-unsupported-note');
  await folders(page).getByRole('button', { name: 'Drafts', exact: true }).click();
  await expect(noteList(page).getByText('No folders or Markdown notes here.')).toBeVisible();
  expect(snapshot(root)).toEqual(before);

  renameSync(nested, nested + '-moved');
  await folders(page).getByRole('button', { name: 'Research', exact: true }).click();
  await expect(
    folders(page).getByRole('button', { name: 'Drafts-moved', exact: true })
  ).toBeVisible();
  renameSync(nested + '-moved', nested);
  await folders(page).getByRole('button', { name: 'Drafts-moved', exact: true }).click();
  await expect(noteList(page).getByRole('alert')).toContainText('moved or removed');

  writeFileSync(join(inbox, file), `${readFileSync(join(inbox, file), 'utf8')}\nMy edits.`);
  await folders(page).getByRole('button', { name: 'Inbox', exact: true }).click();
  await noteList(page).getByRole('button', { name: file, exact: true }).click();
  await expect(reader(page).getByText(/That note is yours/)).toBeVisible();
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
  const connected = await notes(request);
  const reads = recordNotesRequests(page);
  await page.goto('/my-notes');
  await expect(page.getByText('Connected folder · read-only for Ori')).toBeVisible();
  // A connected folder opens at its own root, with its own names.
  await expect(
    noteList(page).getByRole('heading', { name: 'Field Notebook', level: 3 })
  ).toBeVisible();
  await listSettled(page);
  expect(reads).toEqual([
    'GET ?view=connection',
    `GET /browse?ref=${encodeURIComponent(connected.browse_ref)}`
  ]);
  await expect(
    notebookBrowser(page).getByRole('button', { name: /^(Inbox|Projects|Save to Inbox)$/ })
  ).toHaveCount(0);
  await folders(page).getByRole('button', { name: 'Expeditions', exact: true }).click();
  await noteList(page).getByRole('button', { name: 'Canyon.md', exact: true }).click();
  await expect(
    reader(page).getByRole('link', { name: 'Open Canyon.md in Obsidian' })
  ).toHaveAttribute('href', /^obsidian:\/\/open/);
  await expect(reader(page).getByText(/connected folder/)).toBeVisible();
  await expect(reader(page).getByText('Not an Ori note.')).toHaveCount(0);
  await capture(page, '07-read-only-vault');
  expect(snapshot(root)).toEqual(before);
  expect(reads.filter(line => !line.startsWith('GET '))).toEqual([]);
  expect(reads.some(line => line.includes('/preview/'))).toBe(false);

  // UI-only error/partial cases follow the real fixture checks above.
  await page.route(
    `**${endpoint}/browse?*`,
    route => route.fulfill({ json: { directory: { entries: [], partial: true } } }),
    { times: 1 }
  );
  await folders(page).getByRole('button', { name: 'Field Notebook', exact: true }).click();
  await expect(
    noteList(page).getByText('Only some names could be listed.', { exact: false })
  ).toBeVisible();
  await expect(noteList(page).getByText('No folders or Markdown notes here.')).toHaveCount(0);
  await page.route(
    `**${endpoint}/browse?*`,
    route => route.fulfill({ status: 403, json: { error: 'Forbidden' } }),
    { times: 1 }
  );
  await folders(page).getByRole('button', { name: 'Field Notebook', exact: true }).click();
  await expect(noteList(page).getByRole('alert')).toContainText('Ori cannot list this folder');
  await expect(page.getByRole('button', { name: 'Open folder', exact: true })).toBeVisible();
});

test('a missing folder is distinct from disconnected and reuses the reviewed connect flow', async ({
  page,
  request
}) => {
  const source = await notes(request);
  const root = join(sandbox!, 'Documents', 'Field Notebook');
  expect(source.source.path).toBe(root);
  const moved = `${root}-moved`;
  const before = snapshot(root);
  renameSync(root, moved);
  try {
    const reads = recordNotesRequests(page);
    await page.goto('/my-notes');
    await expect(page.getByRole('heading', { name: 'Notes folder not found' })).toBeVisible();
    // An unavailable source offers repair and lists nothing.
    await expect(notebookBrowser(page)).toBeHidden();
    expect(reads).toEqual(['GET ?view=connection']);
    await capture(page, '13-missing-source');
    await page.getByRole('button', { name: 'Choose again…' }).click();
    const card = page.locator('#personalAssistantSecondBrain');
    await card.getByRole('button', { name: 'Choose again', exact: true }).click();
    await expect(card.getByRole('button', { name: 'Use this path' })).toBeVisible();
  } finally {
    renameSync(moved, root);
  }
  const restored = await notes(request);
  expect(restored.browse_ref).toBe(source.browse_ref);
  await post(request, `${endpoint}/disconnect`, {
    request_id: 'missing-test-disconnect',
    if_version: restored.version
  });
  await page.reload();
  await expect(page.getByRole('heading', { name: 'No notes folder connected' })).toBeVisible();
  await capture(page, '14-disconnected');
  await page.getByRole('button', { name: 'Set up notes…' }).click();
  const card = page.locator('#personalAssistantSecondBrain');
  await card.locator('[data-second-brain-existing] summary').click();
  await card.getByRole('button', { name: 'Read a folder I have…' }).click();
  await card.getByLabel('or paste a folder path').fill(root);
  await card.getByRole('button', { name: 'Use this path' }).click();
  const connect = card.getByRole('button', { name: 'Connect and look' });
  await expect(connect).toBeVisible();
  expect((await notes(request)).status).toBe('not_connected');
  await connect.click();
  await expect.poll(async () => (await notes(request)).status).toBe('connected');
  const after = await notes(request);
  expect(after.source.id).toBe(source.source.id);
  expect(after.browse_ref).not.toBe(source.browse_ref);
  expect(snapshot(root)).toEqual(before);
  await page.reload();
  await expect(page.getByRole('heading', { name: 'Field Notebook', level: 2 })).toBeVisible();
  await expect(
    noteList(page).getByRole('heading', { name: 'Field Notebook', level: 3 })
  ).toBeVisible();
  await capture(page, '15-reconnected');
});
