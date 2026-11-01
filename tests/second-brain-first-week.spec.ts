import { existsSync, readdirSync, readFileSync, realpathSync, writeFileSync } from 'node:fs';
import { execFileSync } from 'node:child_process';
import { join, resolve } from 'node:path';

import { test, expect, type APIRequestContext, type Page } from '@playwright/test';

// The first week with a second brain, in the browser (Issue #759): the offer of a
// folder, the project question, the fill-in that writes approved people and
// follow-ups, the consent card for an older setup, the notes Ori leaves alone, and
// the recap row seven days later.
//
// Two sandboxes, because the cases need different machines.
//
// 1. Everything but the Downloads case runs against
//      ./scripts/second-brain-demo.sh --create 8947
//    whose READY line carries SANDBOX and DOCUMENTS:
//
//      PLAYWRIGHT_BASE_URL=http://localhost:8947 SECOND_BRAIN_SANDBOX="<SANDBOX>" \
//        SECOND_BRAIN_DOCUMENTS="<DOCUMENTS>" SECOND_BRAIN_FOLLOW_UPS=3 SECOND_BRAIN_PEOPLE=2 \
//        npx playwright test tests/second-brain-first-week.spec.ts --project=chromium --workers=1
//
//    (--empty with SECOND_BRAIN_FOLLOW_UPS=0 SECOND_BRAIN_PEOPLE=0 drives the
//    empty-seed headline instead of the seeded one.) The cases share the sandbox
//    and run in order; each makes a differently named folder.
//
// 2. The Downloads case ("approving files teaches Ori") runs against
//      ./scripts/demo-inbox-folder.sh --port 8957 --recurring-names
//    with FIRST_WEEK_DOWNLOADS_URL=http://localhost:8957 in addition.
//
// The native folder dialog is never driven, and the real home folder is never
// touched: both sandboxes redirect HOME.

const documents = process.env.SECOND_BRAIN_DOCUMENTS
  ? realpathSync(process.env.SECOND_BRAIN_DOCUMENTS)
  : '';
const sandbox = process.env.SECOND_BRAIN_SANDBOX || '';
const followUps = Number(process.env.SECOND_BRAIN_FOLLOW_UPS || 0);
const people = Number(process.env.SECOND_BRAIN_PEOPLE || 0);
const downloadsURL = process.env.FIRST_WEEK_DOWNLOADS_URL || '';

const ENDPOINT = '/api/personal-assistant/second-brain';
const KNOWLEDGE = '/api/personal-assistant/knowledge';
const stamp = Date.now();
// A person's name is its capitalised words, so a name that must be new to this run
// takes a letters-only tag from the stamp instead of a number.
const tag = `K${String(stamp % 1_000_000)
  .split('')
  .map(digit => 'abcdefghij'[Number(digit)])
  .join('')}`;

type SecondBrain = {
  status: string;
  version: number;
  ask: { state: string };
  source?: { name: string; path: string; origin: string; connected_at: string };
  written_by_ori?: { count: number; by_kind?: Record<string, number> };
  fill_in?: { state: string; offer: boolean; project_ask?: string; written: number };
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

// setUpViaApi sets a second brain up the way the drawer does (a parent from the
// picker's typed-path answer, then /create) and waits for its first look.
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

// approvePerson saves a sentence about a person the way the Profile page does.
let saved = 0;
async function saveFact(request: APIRequestContext, category: string, text: string) {
  const interview = await request.get(`${KNOWLEDGE}/interview`);
  expect(interview.ok(), await interview.text()).toBeTruthy();
  const response = await request.post(`${KNOWLEDGE}/explicit`, {
    data: {
      request_id: `spec-fact-${stamp}-${(saved += 1)}`,
      state_version: (await interview.json()).state_version,
      category,
      text
    }
  });
  expect(response.ok(), await response.text()).toBeTruthy();
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
  // A card that waits for an answer sits under Needs you, which starts folded.
  const strip = page.locator('#personalAssistantTodayStripToggle');
  if ((await strip.count()) && (await strip.getAttribute('aria-expanded')) === 'false') {
    await strip.click();
  }
}

const card = (page: Page) => page.locator('#personalAssistantSecondBrain');
const action = (page: Page, id: string) => card(page).locator(`[data-second-brain-action="${id}"]`);
const noteFiles = (folder: string) =>
  existsSync(folder) ? readdirSync(folder).filter(file => file !== 'README.md') : [];
const waitFor = (path: string, timeout = 30_000) =>
  expect.poll(() => existsSync(path), { timeout }).toBe(true);

// The next card waits its turn: the one slot shows the first look's result until
// the user puts it away, and then asks what is owed next, consent first.
async function waitForCard(page: Page, text: string) {
  const wanted = card(page).getByText(text);
  await expect
    .poll(
      async () => {
        if (await wanted.isVisible()) return true;
        const later = card(page).getByRole('button', { name: 'Not now' });
        if (await later.count()) await later.first().click();
        return false;
      },
      { timeout: 30_000 }
    )
    .toBe(true);
}
const waitForProjectQuestion = (page: Page) =>
  waitForCard(page, "What's the main thing you're working on right now?");

test.describe('the first week, in a second brain Ori set up', () => {
  test.describe.configure({ mode: 'serial' });
  test.skip(
    !documents || !sandbox,
    'Run against scripts/second-brain-demo.sh --create: set PLAYWRIGHT_BASE_URL, SECOND_BRAIN_SANDBOX and SECOND_BRAIN_DOCUMENTS from its READY line.'
  );

  test('the first look says what was seeded, or that it is empty, and offers a folder to show Ori', async ({
    page,
    request
  }) => {
    // The offer follows the setup made in this conversation, so it is set up
    // through the drawer: the ask, a folder pasted into the path field, a name.
    await disconnect(request);
    const reset = await request.post('/api/progression/reset', { data: {} });
    expect(reset.ok(), await reset.text()).toBeTruthy();
    await expect.poll(async () => (await secondBrain(request)).ask.state).toBe('pending');
    await openDrawer(page);
    await action(page, 'setup').click();
    await action(page, 'elsewhere').click();
    await card(page).getByLabel('or paste a folder path').fill(documents);
    await card(page).getByRole('button', { name: 'Use this path' }).click();
    await card(page).locator('[data-second-brain-name]').fill(`Offer Brain ${stamp}`);
    await action(page, 'create').click();
    const headline = card(page).locator('.pa-specialist-offer__headline');
    if (followUps + people === 0) {
      await expect(headline).toHaveText(
        "Your second brain is empty because I don't know you yet. Show me a folder and I'll start filling it.",
        { timeout: 75_000 }
      );
    } else {
      // What the sandbox seeded, or more: an earlier run's approved people are
      // part of what Ori knows by now.
      await expect(headline).toHaveText(
        /^Your second brain is set up: \d+ follow-ups? in Inbox, \d+ (?:people|person)\.$/,
        { timeout: 75_000 }
      );
      const counts = (await headline.innerText()).match(/set up: (\d+) .* Inbox, (\d+) /);
      expect(Number(counts?.[1])).toBeGreaterThanOrEqual(followUps);
      expect(Number(counts?.[2])).toBeGreaterThanOrEqual(people);
    }
    // The same offer sits under either headline: the folders this machine has,
    // Downloads first when it has one, and the chooser.
    // The usual folders are asked of the server after the headline is drawn.
    const buttons = async () =>
      (await card(page).getByRole('button').allInnerTexts()).map(text => text.trim());
    await expect.poll(buttons, { timeout: 20_000 }).toContain('Choose a folder…');
    const usual = (await buttons()).filter(text =>
      ['Downloads', 'Music', 'Documents', 'Desktop'].includes(text)
    );
    if (usual.includes('Downloads')) expect(usual[0]).toBe('Downloads');
  });

  test('the project question writes the answer as a note, refuses a bad name, and opens Create Workspace without making one', async ({
    page,
    request
  }) => {
    const brain = await setUpViaApi(request, `Project Brain ${stamp}`);
    expect((await secondBrain(request)).fill_in?.state).toBe('allowed');
    await openDrawer(page);
    await waitForProjectQuestion(page);

    await card(page).locator('textarea[data-project-answer]').fill('Finishing the Launch album');
    await card(page).getByRole('button', { name: 'Save', exact: true }).click();
    await expect(card(page)).toContainText("I'll call it Launch Album. OK?");

    // A name with a slash is refused in one sentence, before anything is sent.
    let wrote = false;
    page.on('request', sent => {
      if (sent.url().endsWith(`${ENDPOINT}/project`)) wrote = true;
    });
    const name = card(page).locator('input[data-project-name]');
    await name.fill('a/b');
    await card(page).getByRole('button', { name: 'Save', exact: true }).click();
    await expect(card(page)).toContainText('Choose a simple folder name, without slashes.');
    expect(wrote).toBe(false);
    expect(noteFiles(join(brain, 'Projects'))).toEqual([]);

    const project = `Launch ${stamp}`;
    await name.fill(project);
    await card(page).getByRole('button', { name: 'Save', exact: true }).click();
    await expect(card(page)).toContainText(`Added ${project} to your notes.`);

    const readme = join(brain, 'Projects', project, 'README.md');
    const written = readFileSync(readme, 'utf8');
    expect(written).toMatch(/^---\nori: project\nori_hash: [0-9a-f]{64}\n---\n/);
    expect(written).toContain(`# ${project}\n\nFinishing the Launch album\n`);
    expect(readdirSync(join(brain, 'Projects', project))).toEqual(['README.md']);
    const current = await secondBrain(request);
    expect(current.fill_in?.project_ask).toBe('answered');
    expect(current.projects?.map(item => item.name)).toContain(project);

    // Make it a workspace opens the ordinary form with the folder and name filled in.
    await action(page, 'workspace').click();
    // On Home the page opens the form where it is; elsewhere the link carries the name.
    await expect(page.locator('.modal.show').first()).toBeVisible();
    await expect(page.locator('#workspaceAnchorPath')).toHaveValue(
      new RegExp(`/Projects/${project}$`)
    );
    await expect(page.locator('#workspaceFolderModeAnchored')).toBeChecked();
    // Nothing is made until the person presses Create.
    const workspaces = await (await request.get('/api/workspaces')).json();
    const list: { name: string }[] = Array.isArray(workspaces) ? workspaces : workspaces.workspaces;
    expect(list.map(item => item.name)).not.toContain(project);

    // The question does not come back.
    await openDrawer(page);
    await expect(card(page)).not.toContainText("What's the main thing");
  });

  test('Skip is recorded once, the question never returns, and the Profile interview still has it', async ({
    page,
    request
  }) => {
    await setUpViaApi(request, `Skip Brain ${stamp}`);
    await openDrawer(page);
    await waitForProjectQuestion(page);
    await card(page).getByRole('button', { name: 'Skip', exact: true }).click();
    await expect
      .poll(async () => (await secondBrain(request)).fill_in?.project_ask)
      .toBe('skipped');

    await openDrawer(page);
    await expect(card(page)).not.toContainText("What's the main thing");
    // The Profile page's interview is a separate surface and is untouched.
    const interview = await request.get(`${KNOWLEDGE}/interview`);
    expect(interview.ok()).toBeTruthy();
    expect(JSON.stringify(await interview.json())).toContain('priority');
  });

  test('an approved person becomes a note, a note the user edited is left alone, and a follow-up is ticked', async ({
    request
  }) => {
    const brain = await setUpViaApi(request, `Fill Brain ${stamp}`);
    const first = `Dana ${tag}`;
    await saveFact(request, 'people', `${first} is my distributor.`);
    const danaNote = join(brain, 'People', `${first}.md`);
    await waitFor(danaNote);
    expect(readFileSync(danaNote, 'utf8')).toMatch(
      /^---\nori: person\nori_hash: [0-9a-f]{64}\n---\n/
    );

    // The user takes the note over by editing it; a later sync never touches it.
    writeFileSync(danaNote, `${readFileSync(danaNote, 'utf8')}\nMine now.\n`);
    const mine = readFileSync(danaNote);
    const second = `Sam ${tag}`;
    await saveFact(request, 'people', `${second} is my mastering engineer.`);
    await waitFor(join(brain, 'People', `${second}.md`));
    expect(readFileSync(danaNote).equals(mine)).toBe(true);
    await expect
      .poll(async () => (await secondBrain(request)).written_by_ori?.by_kind?.person ?? 0)
      .toBeGreaterThanOrEqual(1);

    // A follow-up confirmed by hand lands in Inbox with an open line, and
    // completing it ticks that line.
    const title = `Call the pressing plant ${stamp}`;
    const created = await request.post('/api/personal-hq/followups', {
      data: { category: 'i_owe', direction: 'i_owe', title }
    });
    expect(created.ok(), await created.text()).toBeTruthy();
    const id = (await created.json()).followup.id as string;
    const inbox = join(brain, 'Inbox');
    const findNote = () =>
      noteFiles(inbox).find(file => readFileSync(join(inbox, file), 'utf8').includes(title));
    await expect.poll(findNote, { timeout: 30_000 }).toBeTruthy();
    const noteName = findNote() as string;
    expect(readFileSync(join(inbox, noteName), 'utf8')).toContain(`- [ ] ${title}`);
    const done = await request.post('/api/personal-hq/followups/complete', { data: { id } });
    expect(done.ok(), await done.text()).toBeTruthy();
    await expect
      .poll(() => readFileSync(join(inbox, noteName), 'utf8'), { timeout: 30_000 })
      .toContain(`- [x] ${title}`);
  });

  test('a setup from before asks once, writes nothing outside Inbox and Daily until allowed, then keeps the notes', async ({
    page,
    request
  }) => {
    const brain = await setUpViaApi(request, `Legacy Brain ${stamp}`);
    execFileSync(resolve('scripts/second-brain-legacy.sh'), [sandbox]);
    expect((await secondBrain(request)).fill_in?.state).toBe('pending');

    const person = `Quinn ${tag}`;
    await saveFact(request, 'people', `${person} is my label contact.`);
    await expect.poll(async () => (await secondBrain(request)).fill_in?.offer).toBe(true);
    // Nothing was written to People for want of an answer.
    expect(existsSync(join(brain, 'People', `${person}.md`))).toBe(false);

    await openDrawer(page);
    await waitForCard(
      page,
      'I can also keep People, a note per project and what I know about you up to date in your second brain.'
    );
    await expect(card(page)).toContainText('Yours are never touched.');
    await card(page).getByRole('button', { name: 'Let Ori write there' }).click();
    await waitFor(join(brain, 'People', `${person}.md`));
    const current = await secondBrain(request);
    expect(current.fill_in).toMatchObject({ state: 'allowed', offer: false });
  });

  test('seven days after setup Today recaps the week once, and a dismissal is remembered', async ({
    page,
    request
  }) => {
    await setUpViaApi(request, `Week Brain ${stamp}`);
    const rows = async () =>
      ((await (await request.get('/api/personal-assistant/today')).json()).today?.done?.items ??
        []) as { kind: string; title: string; actions?: string[]; route: string }[];
    expect((await rows()).filter(row => row.kind === 'second_brain_week')).toHaveLength(0);

    execFileSync(resolve('scripts/second-brain-age.sh'), [sandbox, '8']);
    const [recap] = (await rows()).filter(row => row.kind === 'second_brain_week');
    expect(recap?.title).toMatch(
      /^This week your second brain gained .+\.$|^Your second brain had a quiet week/
    );
    expect(recap.actions).toEqual(['dismiss']);
    expect(recap.route).toContain('station=second-brain');

    // Today lives in the assistant drawer; its Done list is behind the "done today" row.
    const showDone = async () => {
      await openDrawer(page);
      const progress = page.locator('#personalAssistantProgressRow');
      if ((await progress.getAttribute('aria-expanded')) === 'false') await progress.click();
    };
    await showDone();
    const row = page.getByText(recap.title, { exact: true });
    await expect(row).toBeVisible();
    await page.locator('[data-second-brain-week-action="dismiss"]').click();
    await expect(row).toHaveCount(0);

    // It is recorded: a fresh request and a reload both leave it gone.
    expect((await rows()).filter(item => item.kind === 'second_brain_week')).toHaveLength(0);
    await showDone();
    await expect(page.getByText(recap.title, { exact: true })).toHaveCount(0);
  });
});

test.describe('the first Downloads batch teaches Ori', () => {
  test.describe.configure({ mode: 'serial' });
  test.skip(
    !downloadsURL,
    'Run against scripts/demo-inbox-folder.sh --recurring-names and set FIRST_WEEK_DOWNLOADS_URL.'
  );
  test.use({ baseURL: downloadsURL || undefined });

  const files = [
    'Acme_Invoice_0412.pdf',
    'Chase_Statement_Jan.pdf',
    'Chase_Statement_Feb.pdf',
    'Chase_Statement_balance-4521.88_Mar.pdf',
    'Dana_Lee_Contract.pdf',
    'Dana_Lee_Proposal.docx'
  ];

  test('approving files for filing offers the names that recur, and an approved one becomes a People note', async ({
    page,
    request
  }) => {
    // It prepares a workspace and scans a folder, which takes longer than a click.
    test.setTimeout(240_000);
    // The second brain is set up in its default place, in the sandbox's own
    // Workspace Directory.
    const created = await request.post(`${ENDPOINT}/create`, {
      data: { request_id: `spec-downloads-${stamp}` }
    });
    expect(created.ok(), await created.text()).toBeTruthy();
    const brain = (await secondBrain(request)).source?.path as string;
    expect(brain).toBeTruthy();

    // Show Ori Downloads from Home; it prepares the review and the first batch.
    await page.goto('/');
    await page.waitForFunction(() =>
      Boolean((window as unknown as { PersonalAssistantPanel?: unknown }).PersonalAssistantPanel)
    );
    await page.evaluate(() =>
      (
        window as unknown as { PersonalAssistantPanel: { close: () => void } }
      ).PersonalAssistantPanel.close()
    );
    await page.locator('#cockpitPrimaryActionBtn').click();
    await page.locator('.personal-assistant-panel__chip', { hasText: 'Downloads' }).first().click();
    await page
      .getByRole('button', { name: 'Use automatic placement', exact: true })
      .first()
      .click();
    const placement = page.locator('#assistedWorkspacePlacementPanel');
    await placement.getByRole('button', { name: 'Create workspace & scan', exact: true }).click();
    await placement
      .getByRole('heading', { name: 'Workspace prepared' })
      .waitFor({ timeout: 90_000 });
    await placement.getByRole('button', { name: 'Close', exact: true }).click();

    // Review the first batch and approve the six files whose names recur.
    await page.goto('/');
    await page.waitForFunction(() =>
      Boolean(
        (window as unknown as { PersonalAssistantPanel?: { open?: unknown } })
          .PersonalAssistantPanel?.open
      )
    );
    await page.evaluate(() =>
      (
        window as unknown as { PersonalAssistantPanel: { open: (el: Element | null) => void } }
      ).PersonalAssistantPanel.open(document.getElementById('personalAssistantLauncher'))
    );
    const strip = page.locator('#personalAssistantTodayStripToggle');
    if ((await strip.count()) && (await strip.getAttribute('aria-expanded')) === 'false') {
      await strip.click();
    }
    await page
      .locator('#personalAssistantNeedsYouReports article[data-look-id]')
      .first()
      .getByRole('link', { name: 'Review batch' })
      .click();
    for (const file of files) {
      await page.getByRole('checkbox', { name: `Select ${file}`, exact: true }).check();
    }
    await page.locator('#fileJanitorApprove').click();
    await page.locator('#fileJanitorConfirmApply').click();

    // The names that recur are offered as learnings; nothing is a fact yet.
    const learning = async () =>
      (await (await request.get('/api/personal-assistant/setup-learning')).json()).learning as {
        items: { id: string; version: number; text: string }[];
      };
    await expect
      .poll(async () => (await learning()).items.map(item => item.text), { timeout: 60_000 })
      .toEqual(expect.arrayContaining([expect.stringContaining('Dana Lee')]));
    expect(existsSync(join(brain, 'People', 'Dana Lee.md'))).toBe(false);
    const names = (await learning()).items.map(item => item.text).join('\n');
    expect(names).not.toContain('4521');
    expect(names).not.toContain('Acme');

    // Approving one is the Profile page's own approve, and the sync writes the note.
    const dana = (await learning()).items.find(item => item.text.includes('Dana Lee'));
    const approved = await request.post(`${KNOWLEDGE}/${dana?.id}/approve`, {
      data: { version: dana?.version, request_id: `spec-approve-${stamp}` }
    });
    expect(approved.ok(), await approved.text()).toBeTruthy();
    await waitFor(join(brain, 'People', 'Dana Lee.md'));
    expect(readFileSync(join(brain, 'People', 'Dana Lee.md'), 'utf8')).toMatch(
      /^---\nori: person\n/
    );
  });
});
