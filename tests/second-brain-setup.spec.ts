import {
  existsSync,
  mkdirSync,
  readdirSync,
  readFileSync,
  realpathSync,
  renameSync,
  writeFileSync
} from 'node:fs';
import { join } from 'node:path';

import { test, expect, type APIRequestContext, type Page } from '@playwright/test';

// Set one up for me, in the browser, against the sandbox that
//   ./scripts/second-brain-demo.sh --create 8947
// prints. Its READY line carries DOCUMENTS, FOLLOW_UPS and PEOPLE:
//
//   PLAYWRIGHT_BASE_URL=http://localhost:8947 SECOND_BRAIN_DOCUMENTS="<DOCUMENTS>" \
//     SECOND_BRAIN_FOLLOW_UPS=3 SECOND_BRAIN_PEOPLE=2 \
//     npx playwright test tests/second-brain-setup.spec.ts --project=chromium --workers=1
//
// The sandbox has an empty Documents, no vault list, three open follow-ups and
// two approved People entries. The native folder dialog is never driven: the
// parent is pasted into the same path field the dialog's answer fills. The
// cases share one sandbox, so they run in order; each makes a differently
// named folder, so a re-run needs no cleanup beyond the disconnect it does.

// The server records the canonical path (on macOS /var is /private/var).
const documents = process.env.SECOND_BRAIN_DOCUMENTS
  ? realpathSync(process.env.SECOND_BRAIN_DOCUMENTS)
  : '';
const followUps = Number(process.env.SECOND_BRAIN_FOLLOW_UPS || 0);
const people = Number(process.env.SECOND_BRAIN_PEOPLE || 0);

test.describe.configure({ mode: 'serial' });
test.skip(
  !documents,
  'Run against scripts/second-brain-demo.sh --create: set PLAYWRIGHT_BASE_URL and SECOND_BRAIN_DOCUMENTS from its READY line.'
);

const ENDPOINT = '/api/personal-assistant/second-brain';
const stamp = Date.now();

type SecondBrain = {
  status: string;
  version: number;
  ask: { state: string };
  source?: { name: string; path: string; origin: string; open_link?: string };
  written_by_ori?: { count: number; last_at?: string };
  projects?: { name: string; rel: string; path: string }[];
};

async function secondBrain(request: APIRequestContext): Promise<SecondBrain> {
  const response = await request.get(ENDPOINT);
  expect(response.ok(), await response.text()).toBeTruthy();
  return (await response.json()).second_brain;
}

async function disconnect(request: APIRequestContext) {
  const current = await secondBrain(request);
  if (current.status === 'not_connected') return;
  const response = await request.post(`${ENDPOINT}/disconnect`, {
    data: { request_id: `spec-disconnect-${Date.now()}`, if_version: current.version }
  });
  expect(response.ok(), await response.text()).toBeTruthy();
}

async function resetGettingStarted(request: APIRequestContext) {
  const response = await request.post('/api/progression/reset', { data: {} });
  expect(response.ok(), await response.text()).toBeTruthy();
  await expect.poll(async () => (await secondBrain(request)).ask.state).toBe('pending');
}

async function openDrawer(page: Page) {
  await page.goto('/');
  await page.waitForFunction(() =>
    Boolean(
      (
        window as unknown as {
          PersonalAssistantPanel?: { _state?: { personalAssistant?: unknown } };
        }
      ).PersonalAssistantPanel?._state?.personalAssistant
    )
  );
  const open = await page.evaluate(() =>
    Boolean(
      (
        window as unknown as { PersonalAssistantPanel?: { isOpen?: () => boolean } }
      ).PersonalAssistantPanel?.isOpen?.()
    )
  );
  if (!open) await page.locator('#personalAssistantLauncher').click();
  await expect(page.locator('#personalAssistantPanel')).toBeVisible();
}

const card = (page: Page) => page.locator('#personalAssistantSecondBrain');
const action = (page: Page, id: string) => card(page).locator(`[data-second-brain-action="${id}"]`);

// startSetup takes the ask's Set one up for me to the confirmation step with
// the sandbox's Documents as the parent.
async function startSetup(page: Page, request: APIRequestContext) {
  await disconnect(request);
  await resetGettingStarted(request);
  await openDrawer(page);
  await action(page, 'setup').click();
  await action(page, 'elsewhere').click();
  await expect(card(page)).toContainText('Where should I set up your second brain?');
  await card(page).getByLabel('or paste a folder path').fill(documents);
  await card(page).getByRole('button', { name: 'Use this path' }).click();
  await expect(card(page)).toContainText('Set up "Second Brain" in Documents?');
  await expect(card(page)).toContainText(
    "I'll keep Inbox, Daily, People, a note per project and what I know about you up to date; everything else is yours."
  );
}

const nameField = (page: Page) => card(page).locator('[data-second-brain-name]');

test('the ask offers to set one up when no vault is found', async ({ page, request }) => {
  await disconnect(request);
  await resetGettingStarted(request);
  await openDrawer(page);
  await expect(card(page)).toContainText(
    'Do you keep a second brain? Point me at it, or I can set one up for you.'
  );
  await expect(card(page).locator('[data-second-brain-action]')).toHaveText([
    'Choose folder…',
    'Set one up for me',
    'Not now'
  ]);
});

test('set one up in one click: the Workspace Directory, no picker, and Somewhere else comes back', async ({
  page,
  request
}) => {
  await disconnect(request);
  await resetGettingStarted(request);
  const place = (await secondBrain(request)) as SecondBrain & {
    setup_default?: { parent_name: string; path: string };
  };
  expect(place.setup_default?.path.endsWith('/Second Brain')).toBe(true);
  await openDrawer(page);
  await action(page, 'setup').click();
  await expect(card(page)).toContainText(
    `Set up your second brain in ${place.setup_default?.parent_name}?`
  );
  await expect(card(page)).toContainText(place.setup_default?.path as string);
  await action(page, 'elsewhere').click();
  await expect(card(page)).toContainText('Where should I set up your second brain?');
  await card(page).getByRole('button', { name: 'Back' }).click();
  await action(page, 'create').click();
  // The server records the canonical path (on macOS /var is /private/var).
  await expect
    .poll(async () => (await secondBrain(request)).source?.path, { timeout: 25_000 })
    .toBe(join(realpathSync(join(place.setup_default?.path as string, '..')), 'Second Brain'));
  await expect(card(page)).not.toContainText('Where should I set up');
  await disconnect(request);
  // The folder stays after a disconnect; move it so later cases start clean.
  renameSync(place.setup_default?.path as string, `${place.setup_default?.path} ${stamp}`);
});

test('a default place that is already taken says so and asks where, changing nothing', async ({
  page,
  request
}) => {
  await disconnect(request);
  await resetGettingStarted(request);
  const place = (await secondBrain(request)) as SecondBrain & {
    setup_default?: { path: string };
  };
  const taken = place.setup_default?.path as string;
  mkdirSync(taken);
  writeFileSync(join(taken, 'mine.md'), 'mine');
  await openDrawer(page);
  await action(page, 'setup').click();
  await action(page, 'create').click();
  await expect(card(page)).toContainText('already has something in it');
  await expect(card(page)).toContainText('Where should I set up your second brain?');
  expect(readdirSync(taken)).toEqual(['mine.md']);
  expect((await secondBrain(request)).status).toBe('not_connected');
  renameSync(taken, `${taken} taken ${stamp}`);
});

test('set one up: parent, name, confirm; the folder is made, seeded, connected and looked at', async ({
  page,
  request
}) => {
  await startSetup(page, request);
  const name = `Spec Brain ${stamp}`;
  await nameField(page).fill(name);
  await action(page, 'create').click();

  // The first look reports the seed: exactly what the sandbox holds.
  await expect(card(page).locator('.pa-specialist-offer__headline')).toHaveText(
    `Your second brain is set up: ${followUps} follow-ups in Inbox, ${people} people.`,
    { timeout: 75_000 }
  );
  // The seeded items are the user's follow-ups already: nothing to add.
  await expect(action(page, 'add')).toHaveCount(0);
  const link = card(page).getByRole('link', { name: 'Open in Obsidian' });
  await expect(link).toHaveAttribute('href', /^obsidian:\/\/open\?path=/);

  const brain = join(documents, name);
  const current = await secondBrain(request);
  expect(current.status).toBe('connected');
  expect(current.source).toMatchObject({ name, path: brain, origin: 'created_by_ori' });
  expect(existsSync(join(brain, '.obsidian', 'app.json'))).toBe(true);
  expect(readFileSync(join(brain, 'README.md'), 'utf8')).toMatch(
    /^I'll keep Inbox, Daily, People, a note per project and what I know about you up to date; everything else is yours\./
  );
  for (const folder of ['Inbox', 'Daily', 'Projects', 'Areas', 'Resources', 'Archive', 'People']) {
    expect(existsSync(join(brain, folder, 'README.md')), folder).toBe(true);
  }
  const inbox = readdirSync(join(brain, 'Inbox')).filter(file => file !== 'README.md');
  const peopleNotes = readdirSync(join(brain, 'People')).filter(file => file !== 'README.md');
  expect(inbox).toHaveLength(followUps);
  expect(peopleNotes).toHaveLength(people);
  expect(peopleNotes).toContain('Dana Lee.md');
  const seeded = readFileSync(join(brain, 'Inbox', inbox[0]), 'utf8');
  expect(seeded).toMatch(/^---\nori: seed\nori_hash: [0-9a-f]{64}\n---\n/);
  expect(seeded).toContain('- [ ] ');

  // The look says the same, and marks the items as seeded.
  const look = (await (await request.get(`${ENDPOINT}/look`)).json()).look;
  expect(look.result.items.filter((item: { seeded?: boolean }) => item.seeded)).toHaveLength(
    followUps
  );
  expect(look.result.items.some((item: { suggested?: boolean }) => item.suggested)).toBe(false);
});

test('a target that already has content is refused with one sentence and nothing changes', async ({
  page,
  request
}) => {
  await startSetup(page, request);
  const name = `Spec Brain ${stamp}`; // made by the previous case
  const before = readdirSync(join(documents, name)).sort();
  await nameField(page).fill(name);
  await action(page, 'create').click();
  await expect(card(page)).toContainText(
    `${name} already has something in it. Choose another name or location.`
  );
  await expect(nameField(page)).toBeVisible();
  expect((await secondBrain(request)).status).toBe('not_connected');
  expect(readdirSync(join(documents, name)).sort()).toEqual(before);
});

test('a name with a slash is refused before anything is sent', async ({ page, request }) => {
  await startSetup(page, request);
  let created = false;
  page.on('request', sent => {
    if (sent.url().endsWith(`${ENDPOINT}/create`)) created = true;
  });
  await nameField(page).fill('../escape');
  await action(page, 'create').click();
  await expect(card(page)).toContainText('Choose a simple folder name, without slashes.');
  expect(created).toBe(false);
});

// setUpViaApi sets a second brain up the way the drawer does (a parent from
// the picker's typed-path answer, then /create) and waits for its first look.
async function setUpViaApi(request: APIRequestContext, name: string) {
  await disconnect(request);
  const picked = await request.post('/api/folder-picker/select-path', {
    data: { path: documents }
  });
  const token = (await picked.json()).selection_token;
  expect(token, await picked.text()).toBeTruthy();
  const created = await request.post(`${ENDPOINT}/create`, {
    data: { selection_token: token, name, request_id: `spec-create-${name.replace(/\W+/g, '-')}` }
  });
  expect(created.ok(), await created.text()).toBeTruthy();
  await expect
    .poll(async () => (await (await request.get(`${ENDPOINT}/look`)).json()).look.state, {
      timeout: 75_000
    })
    .toBe('finished');
  return join(documents, name);
}

async function writtenCount(request: APIRequestContext): Promise<number> {
  return (await secondBrain(request)).written_by_ori?.count ?? -1;
}

test('saving a Journal copies it to Daily, refreshes it, and leaves an edited note alone', async ({
  request
}) => {
  const brain = await setUpViaApi(request, `Journal Brain ${stamp}`);
  const day = '2031-02-03';
  const daily = join(brain, 'Daily', `${day}.md`);
  const before = await writtenCount(request);
  const save = async (content: string) => {
    const response = await request.post('/api/personal-hq/journal/save', {
      data: { local_date: day, content }
    });
    expect(response.ok(), await response.text()).toBeTruthy();
    return response.json();
  };

  expect((await save('# End of day\n\nShipped the mix.')).daily_note).toBe('mirrored');
  const first = readFileSync(daily, 'utf8');
  expect(first).toMatch(/^---\nori: journal\nori_hash: [0-9a-f]{64}\n---\n# End of day/);
  expect(first).toContain('Shipped the mix.');
  expect(await writtenCount(request)).toBe(before + 1);

  // Saved again with the note untouched: refreshed.
  expect((await save('# End of day\n\nShipped the mix and the video.')).daily_note).toBe(
    'mirrored'
  );
  expect(readFileSync(daily, 'utf8')).toContain('and the video.');

  // The user edits it; the next save leaves it alone and says so.
  const edited = `${readFileSync(daily, 'utf8')}\nMy own thought.\n`;
  writeFileSync(daily, edited);
  const third = await save('# End of day\n\nA newer draft.');
  expect(third.daily_note).toBe('user_owned');
  expect(third.note_id).toBeTruthy();
  expect(readFileSync(daily, 'utf8')).toBe(edited);
});

test('the connection card shows what Ori wrote and takes a note into Inbox', async ({
  page,
  request
}) => {
  const brain = await setUpViaApi(request, `Capture Brain ${stamp}`);
  const before = await writtenCount(request);
  await openDrawer(page);
  await page.evaluate(() =>
    (
      window as unknown as { PersonalAssistantSecondBrain: { openManage: () => Promise<void> } }
    ).PersonalAssistantSecondBrain.openManage()
  );
  await expect(card(page)).toContainText(`Connected · Capture Brain ${stamp}`);
  await expect(card(page)).toContainText(`Written by Ori: ${before} notes`);
  await expect(card(page).getByRole('link', { name: 'Open in Obsidian' })).toBeVisible();

  // An empty note is refused in the box, before anything is sent.
  await action(page, 'capture').click();
  await expect(card(page)).toContainText('Write the note first.');
  expect(await writtenCount(request)).toBe(before);

  const box = card(page).locator('[data-second-brain-capture]');
  // The box fills the card rather than shrinking to its label.
  const boxWidth = (await box.boundingBox())?.width ?? 0;
  const areaWidth =
    (await card(page).locator('.pa-second-brain__capture').boundingBox())?.width ?? 1;
  expect(boxWidth).toBeGreaterThan(areaWidth - 4);
  await box.fill('Spec note one');
  await action(page, 'capture').click();
  await expect(card(page)).toContainText('Saved to Inbox as');
  await expect.poll(() => writtenCount(request)).toBe(before + 1);
  await expect(card(page)).toContainText(`Written by Ori: ${before + 1} notes`);
  // The same words again are a second note, never an overwrite.
  await box.fill('Spec note one');
  await action(page, 'capture').click();
  await expect.poll(() => writtenCount(request)).toBe(before + 2);

  const notes = readdirSync(join(brain, 'Inbox')).filter(file => file.includes('spec-note-one'));
  expect(notes).toHaveLength(2);
  const note = readFileSync(join(brain, 'Inbox', notes.sort()[0]), 'utf8');
  expect(note).toMatch(
    /^---\nori: capture\nori_hash: [0-9a-f]{64}\n---\nSpec note one\n\nCaptured by Ori on \d{4}-\d{2}-\d{2}\./
  );
});

test('a project note is offered as a workspace on request and nothing is created until Create', async ({
  page,
  request
}) => {
  const brain = await setUpViaApi(request, `Project Brain ${stamp}`);
  mkdirSync(join(brain, 'Projects', 'Launch'), { recursive: true });
  writeFileSync(
    join(brain, 'Projects', 'Launch', 'Plan.md'),
    '# Launch plan\n\n- [ ] Book the venue\n'
  );
  const before = await (await request.get('/api/workspaces')).json();

  await openDrawer(page);
  await page.evaluate(() =>
    (
      window as unknown as { PersonalAssistantSecondBrain: { openManage: () => Promise<void> } }
    ).PersonalAssistantSecondBrain.openManage()
  );
  const project = card(page).getByRole('button', { name: 'Make Launch a workspace' });
  await expect(project).toBeVisible();
  await project.click();

  // The form opens in anchored mode, on that folder, with the project's name.
  await expect(page.locator('#addFolderModal')).toBeVisible();
  await expect(page.locator('#workspaceFolderModeAnchored')).toBeChecked();
  await expect(page.locator('#workspaceAnchorPath')).toHaveValue(join(brain, 'Projects', 'Launch'));
  await expect(page.locator('#folderNameInput')).toHaveValue('Launch');
  expect(await (await request.get('/api/workspaces')).json()).toEqual(before);
  // Nothing was written into the notes.
  expect(readdirSync(join(brain, 'Projects', 'Launch'))).toEqual(['Plan.md']);
});

test('a capture that looks like a secret is refused and nothing is written', async ({
  request
}) => {
  const brain = await setUpViaApi(request, `Secret Brain ${stamp}`);
  const before = readdirSync(join(brain, 'Inbox')).sort();
  const response = await request.post(`${ENDPOINT}/capture`, {
    data: {
      text: 'my key is sk-ant-api03-abcdefghijklmnopqrstuvwxyz0123456789',
      request_id: `spec-secret-${stamp}`
    }
  });
  expect(response.status()).toBe(400);
  expect(await response.text()).not.toContain('sk-ant');
  expect(readdirSync(join(brain, 'Inbox')).sort()).toEqual(before);
});

test('disconnecting leaves the folder and everything in it', async ({ request }) => {
  const name = `Spec Brain ${stamp}`;
  const brain = join(documents, name);
  const before = readdirSync(join(brain, 'Inbox')).sort();
  await disconnect(request);
  expect((await secondBrain(request)).status).toBe('not_connected');
  expect(readdirSync(join(brain, 'Inbox')).sort()).toEqual(before);
  expect(existsSync(join(brain, 'README.md'))).toBe(true);
});
