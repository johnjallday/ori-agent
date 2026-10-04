import { expect, test, type APIRequestContext, type Page, type Route } from '@playwright/test';
import { mkdirSync, utimesSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';

// The interview's folder feed: question 1 ("What's the main thing you're working
// on right now?") can be answered by showing the assistant a folder. The scan
// proposes wording; the user confirms or edits it, and nothing is saved before
// the wizard's Save.
//
// Run it in its own fresh sandbox (the folders are written into the server's
// HOME, which is the sandbox):
//
//   ./scripts/e2e-fresh.sh --sandbox-env ORI_INTERVIEW_FOLDER_SANDBOX tests/interview-folder-feed.spec.ts
//
// The native folder dialog is switched off in every sandbox, so the picker and
// file cases are mocked at the route. The mocked cases run first: the last test
// saves the interview, and a completed interview does not open again.
const sandbox = process.env.ORI_INTERVIEW_FOLDER_SANDBOX || '';
test.skip(!sandbox, 'requires ORI_INTERVIEW_FOLDER_SANDBOX (the demo sandbox directory)');
test.describe.configure({ mode: 'serial' });

const interviewApi = '/api/personal-assistant/knowledge/interview';
const folderApi = '/api/personal-assistant/folder-digest';
const thesis = 'Thesis, a LaTeX manuscript';

// The same trees `./scripts/smoke.sh showfolder <url> seed <sandbox>` writes:
// Documents holds three projects and 40 loose files, Downloads is a dump, and
// Desktop has two files.
function seedFolders() {
  const put = (rel: string, ageDays = 0) => {
    const path = join(sandbox, rel);
    mkdirSync(dirname(path), { recursive: true, mode: 0o750 });
    writeFileSync(path, 'fixture', { mode: 0o600 });
    const at = new Date(Date.now() - ageDays * 86_400_000);
    utimesSync(path, at, at);
  };
  const kinds = ['.pdf', '.png', '.zip', '.dmg', '.csv', '.txt'];
  [5, 5, 5, 4, 3, 3].forEach((count, kind) => {
    for (let i = 0; i < count; i += 1) put(`Downloads/download-${i}${kinds[kind]}`, 2 + i);
  });
  [10, 10, 8, 5, 4, 3].forEach((count, kind) => {
    for (let i = 0; i < count; i += 1) put(`Documents/doc-${i}${kinds[kind]}`, 3 + i);
  });
  put('Documents/Thesis/main.tex', 1);
  for (let i = 0; i < 5; i += 1) put(`Documents/Thesis/chapters/chapter-${i}.tex`, 1);
  put('Documents/website/package.json', 3);
  for (let i = 0; i < 4; i += 1) put(`Documents/website/src/index-${i}.js`, 3);
  put('Documents/Album/Song.rpp', 7);
  for (let i = 0; i < 3; i += 1) put(`Documents/Album/Media/take-${i}.wav`, 7);
  put('Desktop/todo.txt');
  put('Desktop/photo.png');
}

async function ensurePersonalHQ(request: APIRequestContext) {
  if ((await request.get(interviewApi)).ok()) return;
  const root = await request.post('/api/settings/workspace-root', {
    data: { workspace_root: '' }
  });
  expect(root.ok(), await root.text()).toBeTruthy();
  const hired = await request.post('/api/personal-assistant/hire', {
    data: {
      request_id: 'folder-feed-hire',
      if_version: 0,
      display_name: 'Atlas',
      mandate: 'Keep my priorities visible.',
      focus_areas: ['plan_my_day']
    }
  });
  expect(hired.status(), await hired.text()).toBe(201);
  const hq = await request.post('/api/personal-assistant/hq', {
    data: {
      request_id: 'folder-feed-hq',
      if_version: (await hired.json()).personal_assistant.state_version,
      name: 'My HQ',
      timezone: 'UTC'
    }
  });
  expect(hq.status(), await hq.text()).toBe(201);
}

async function openWizard(page: Page) {
  await page.goto('/profile');
  await expect(page.locator('#personalHQInterview')).toHaveAttribute('aria-busy', 'false');
  await page.locator('#personalHQInterviewStart').click();
  const wizard = page.getByRole('dialog', { name: 'Tell your assistant what matters' });
  await expect(wizard.getByRole('textbox')).toBeFocused(); // fade-in done
  return wizard;
}

// Reports the native dialog as available, which no sandbox can do for real.
async function pretendPickerAvailable(page: Page) {
  await page.route(`**${folderApi}`, async route => {
    const response = await route.fetch();
    const body = await response.json();
    body.folder_digest.picker_available = true;
    body.folder_digest.file_picker_available = true;
    body.folder_digest.picker_note = '';
    await route.fulfill({ response, json: body });
  });
}

// Answers every suggest request from the test; records what the browser sent.
async function mockSuggest(page: Page, answer: (route: Route) => Promise<void>) {
  const bodies: unknown[] = [];
  await page.route(`**${interviewApi}/suggest`, async route => {
    bodies.push(route.request().postDataJSON());
    await answer(route);
  });
  return bodies;
}

test.beforeAll(async ({ request }) => {
  seedFolders();
  await ensurePersonalHQ(request);
});

test('a cancelled folder dialog changes nothing', async ({ page }) => {
  await pretendPickerAvailable(page);
  let release = () => {};
  const held = new Promise<void>(resolve => {
    release = resolve;
  });
  const bodies = await mockSuggest(page, async route => {
    await held; // the dialog stays open until the test "cancels" it
    await route.fulfill({ json: { cancelled: true } });
  });
  const wizard = await openWizard(page);
  const chooser = wizard.getByRole('group', { name: 'Show me instead' });
  await wizard.getByRole('textbox').fill('Ship the portfolio site');

  await chooser.getByRole('button', { name: 'Pick a folder…' }).click();
  await expect(wizard.getByRole('status')).toHaveText('Choose a folder in the dialog…');
  // Nothing else can be started while the dialog is open.
  for (const button of await chooser.getByRole('button').all()) await expect(button).toBeDisabled();
  await expect(wizard.getByRole('button', { name: 'Next' })).toBeDisabled();
  release();

  await expect(wizard.getByRole('status')).toHaveText('');
  await expect(chooser.getByRole('button', { name: 'Pick a folder…' })).toBeEnabled();
  await expect(wizard.getByRole('textbox')).toHaveValue('Ship the portfolio site');
  await expect(wizard.locator('.interview-wizard-caption')).toHaveCount(0);
  await expect(wizard.locator('.interview-wizard-use')).toHaveCount(0);
  // The browser names a mode and nothing else: never a path.
  expect(bodies).toEqual([{ picker: true }]);
});

test('a picked file fills the answer with its project', async ({ page }) => {
  await pretendPickerAvailable(page);
  const bodies = await mockSuggest(page, route =>
    route.fulfill({ json: { suggestion: { text: 'Album', folder: 'Album' } } })
  );
  const wizard = await openWizard(page);
  await wizard.getByRole('button', { name: 'Pick a file…' }).click();
  await expect(wizard.getByRole('status')).toHaveText('Filled in “Album”.');
  await expect(wizard.getByRole('textbox')).toHaveValue('Album');
  await expect(wizard.getByRole('textbox')).toBeFocused();
  await expect(wizard.locator('.interview-wizard-caption')).toHaveText(
    "From the folder you showed me. Edit it if that's not quite right."
  );
  expect(bodies).toEqual([{ file: true }]);
});

test('a scan that is still running says so and changes nothing', async ({ page }) => {
  await pretendPickerAvailable(page);
  await mockSuggest(page, route =>
    route.fulfill({
      status: 409,
      json: {
        code: 'conflict',
        message: "I'm still looking at the last folder. Try again in a moment."
      }
    })
  );
  const wizard = await openWizard(page);
  await wizard.getByRole('textbox').fill('My own answer');
  await wizard.getByRole('button', { name: 'Documents' }).click();
  const status = wizard.getByRole('status');
  await expect(status).toHaveText("I'm still looking at the last folder. Try again in a moment.");
  await expect(status).toHaveClass(/is-warning/);
  await expect(wizard.getByRole('textbox')).toHaveValue('My own answer');
  await expect(wizard.locator('.interview-wizard-use')).toHaveCount(0);
  await expect(wizard.getByRole('button', { name: 'Documents' })).toBeEnabled();
});

test('a project already remembered is named in the hint and the box stays empty', async ({
  page
}) => {
  await page.route(`**${interviewApi}`, async route => {
    const response = await route.fetch();
    const body = await response.json();
    body.remembered_project = 'You are working on a project in the folder Thesis.';
    await route.fulfill({ response, json: body });
  });
  const wizard = await openWizard(page);
  const box = wizard.getByRole('textbox');
  await expect(box).toHaveValue('');
  const line = wizard.locator('#interview-wizard-remembered');
  await expect(line).toHaveText(
    'I already remember: “You are working on a project in the folder Thesis”. Add anything that matters more right now, or skip.'
  );
  expect(await box.getAttribute('aria-describedby')).toContain('interview-wizard-remembered');
  await expect(wizard.locator('.interview-wizard-caption')).toHaveCount(0);
});

test('showing a folder fills question 1, and only Save stores it', async ({ page, request }) => {
  const suggestBodies: unknown[] = [];
  page.on('request', sent => {
    if (sent.method() === 'POST' && sent.url().endsWith(`${interviewApi}/suggest`))
      suggestBodies.push(sent.postDataJSON());
  });
  const workspaceNames = async () => {
    const payload = await (await request.get('/api/workspaces')).json();
    const list = Array.isArray(payload) ? payload : payload.folders || payload.workspaces || [];
    return list.map((item: { name: string }) => item.name).sort();
  };
  const workspacesBefore = await workspaceNames();
  const wizard = await openWizard(page);
  const box = wizard.getByRole('textbox');
  const status = wizard.getByRole('status');
  const chooser = wizard.getByRole('group', { name: 'Show me instead' });

  await test.step('the chooser shows the seeded folders and what a scan does', async () => {
    await expect(chooser.getByRole('button')).toHaveText(['Downloads', 'Documents', 'Desktop']);
    await expect(chooser).toContainText(
      'I only look at file names and types. This also leaves a setup suggestion for the folder on Home.'
    );
    // No native dialog in a sandbox: the buttons are absent and the note says why.
    await expect(chooser.getByRole('button', { name: /Pick a/ })).toHaveCount(0);
    await expect(chooser).toContainText('the folder dialog is switched off in this session');
  });

  await test.step('Downloads is a dump: a plain message and an untouched box', async () => {
    await chooser.getByRole('button', { name: 'Downloads' }).click();
    await expect(status).toHaveText(
      "I couldn't tell what this folder is for. Type your answer instead."
    );
    await expect(box).toHaveValue('');
    await expect(wizard.locator('.interview-wizard-caption')).toHaveCount(0);
  });

  await test.step('Documents fills the box and says where the answer came from', async () => {
    await chooser.getByRole('button', { name: 'Documents' }).click();
    await expect(status).toHaveText(`Filled in “${thesis}”.`);
    await expect(box).toHaveValue(thesis);
    await expect(box).toBeFocused();
    const caption = wizard.locator('.interview-wizard-caption');
    await expect(caption).toHaveText(
      "From the folder you showed me. Edit it if that's not quite right."
    );
    expect(await box.getAttribute('aria-describedby')).toContain(
      (await caption.getAttribute('id'))!
    );
    await expect(
      wizard.getByRole('group', { name: 'Also in this folder:' }).getByRole('button')
    ).toHaveText(['website, a Node.js package', 'Album, a REAPER session']);
  });

  await test.step('editing the answer hides the caption', async () => {
    await box.fill('Finish the thesis by March');
    await expect(wizard.locator('.interview-wizard-caption')).toHaveCount(0);
    await expect(wizard.getByRole('group', { name: 'Also in this folder:' })).toHaveCount(0);
    expect(await box.getAttribute('aria-describedby')).not.toContain('interview-answer-caption');
  });

  await test.step('typed text is never overwritten; the suggestion waits as a button', async () => {
    await chooser.getByRole('button', { name: 'Documents' }).click();
    const use = wizard.getByRole('button', { name: `Use “${thesis}”` });
    await expect(use).toBeVisible();
    await expect(box).toHaveValue('Finish the thesis by March');
    await expect(status).toContainText('I kept what you typed');
    await use.click();
    await expect(box).toHaveValue(thesis);
    await expect(box).toBeFocused();
    await expect(use).toHaveCount(0);
    await expect(wizard.locator('.interview-wizard-caption')).toHaveCount(1);
  });

  await test.step('Review shows the answer under Personal HQ · Projects', async () => {
    await wizard.getByRole('button', { name: 'Next' }).click();
    await wizard.getByRole('button', { name: 'Next' }).click();
    await wizard.getByRole('button', { name: 'Next' }).click();
    const row = wizard.locator('.interview-wizard-review-row').first();
    await expect(row).toContainText(`“${thesis}”`);
    await expect(row).toContainText('Personal HQ · Projects');
    // Three scans so far, and still nothing remembered: only Save stores a fact.
    const before = await (await request.get('/api/personal-assistant/knowledge')).json();
    expect(
      before.items.filter((item: { category: string }) => item.category === 'projects')
    ).toEqual([]);
  });

  await test.step('Save stores the fact in the dossier', async () => {
    await wizard.getByRole('button', { name: 'Save 1 fact' }).click();
    await expect(wizard.getByRole('heading', { name: 'Saved 1 fact.' })).toBeVisible();
    await wizard.getByRole('button', { name: 'Done' }).click();
    await expect(wizard).toHaveCount(0);
    const after = await (await request.get('/api/personal-assistant/knowledge')).json();
    const projects = after.items.filter(
      (item: { category: string }) => item.category === 'projects'
    );
    expect(projects).toHaveLength(1);
    expect(projects[0]).toMatchObject({ text: thesis, state: 'approved', scope: 'personal_hq' });
    await expect(page.locator('#reviewedKnowledgeFacts')).toContainText(thesis);
  });

  await test.step('Home still has the folder offer, and no workspace was created', async () => {
    // Only chip ids ever left the browser.
    expect(suggestBodies).toEqual([
      { chip: 'downloads' },
      { chip: 'documents' },
      { chip: 'documents' }
    ]);
    const digest = (await (await request.get(folderApi)).json()).folder_digest;
    expect(digest.offer).toMatchObject({ status: 'pending', folder: 'Documents' });
    expect(digest.offer.subject.name).toBe('Thesis');
    expect(await workspaceNames()).toEqual(workspacesBefore);

    await page.goto('/');
    // The app polls, so the network never goes idle; wait for the panel instead.
    await page.waitForFunction(() => Boolean((window as any).PersonalAssistantPanel?.open));
    await page.evaluate(() =>
      (window as any).PersonalAssistantPanel?.open?.(
        document.getElementById('personalAssistantLauncher'),
        { view: 'today' }
      )
    );
    const card = page.locator('#personalAssistantFolderOffer');
    await expect(card).toBeVisible();
    await expect(card).toContainText('Thesis');
  });
});
