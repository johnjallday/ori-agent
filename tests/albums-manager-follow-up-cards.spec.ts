import { expect, test, type APIRequestContext, type Page } from '@playwright/test';
import { mkdir, writeFile } from 'node:fs/promises';
import { join, resolve } from 'node:path';
import { installLocalCdn } from './helpers/offline-cdn';

// Albums Manager follow-up cards: after a music folder is set up, its Commander
// chat shows a short receipt and ONE follow-up at a time.
//
// Two kinds of evidence live in this file, named in each test title:
//   [real]       the real server, the real folder agent and the real REAPER reader
//                over five parseable REAPER songs and one Logic Pro bundle. No model
//                is configured, so the agent's look is skipped (free): there is no
//                agent-written ticket or goal here. Setting up installs the REAPER
//                plugin, which needs the network.
//   [controlled] the same real drawer and thread, but the folder agent's view is
//                answered from a copy of the real one with fields changed, so states
//                that cannot be timed (a stopped reading, an update-only offer, a
//                goal, a first ticket) are deterministic. Nothing here proves the
//                host produces those states; the Go and module tests do.
//
// Order matters and the tests are serial: a Home asks about its songs once, and a
// Learn answers it for every later folder joining that Home. The dismissal
// journey therefore runs before the learning one.
//
// ./scripts/e2e-fresh.sh --sandbox-env ORI_ALBUMS_FOLLOW_UP_SANDBOX tests/albums-manager-follow-up-cards.spec.ts -- --workers=1
const sandbox = process.env.ORI_ALBUMS_FOLLOW_UP_SANDBOX || '';
const endpoint = '/api/personal-assistant/folder-agent';
const evidence = resolve('tasks/evidence-albums-manager-follow-up-cards');

test.describe.configure({ mode: 'serial' });

type FolderView = {
  workspace_id: string;
  folder_name: string;
  route: string;
  state: string;
  setup?: { status?: string; receipt?: { home?: { name?: string } } };
  plan?: { steps: { handle: string; kind: string; chosen?: boolean }[]; selected?: string[] };
  learning?: { status?: string; done?: number; total?: number; line?: string };
  songs_offer?: unknown;
};

const rpp = (n: number) =>
  [
    '<REAPER_PROJECT 0.1 "7.27/macOS-arm64" 1760000000',
    `  TEMPO ${[96, 110, 124, 132, 140][n - 1]} 4 4`,
    '  MARKER 1 0 "Intro" 0 0 1',
    '  <TRACK\n    NAME "Drums"\n  >',
    '  <TRACK\n    NAME "Bass"\n  >',
    '>',
    ''
  ].join('\n');

async function writeAlbums(folder: string) {
  const root = join(sandbox, folder);
  for (let n = 1; n <= 5; n += 1) {
    await mkdir(join(root, `Album-${n}`), { recursive: true, mode: 0o750 });
    await writeFile(join(root, `Album-${n}`, 'Song.rpp'), rpp(n), { mode: 0o600 });
  }
  await mkdir(join(root, 'Logic Sketch', 'Sketch.logicx'), { recursive: true, mode: 0o750 });
  await writeFile(join(root, 'Logic Sketch', 'Sketch.logicx', 'ProjectData'), 'logic', {
    mode: 0o600
  });
  return root;
}

async function views(request: APIRequestContext): Promise<FolderView[]> {
  return (await (await request.get(endpoint)).json()).folder_agents;
}

async function seedAssistant(request: APIRequestContext) {
  await request.post('/api/onboarding/skip');
  // The Home hires agents, and a run with no model configured stops with
  // needs_model; a provider with no key cannot be set as the system model. So the
  // [real] journeys need a provider key in the server's environment: run them as
  // `with-keys ./scripts/e2e-fresh.sh …`. Without one they are SKIPPED here, which
  // is not a pass. (The Home's own agents run one short model turn when it is made.)
  const configured = await request.post('/api/settings/system-model', {
    data: { provider: 'claude', model: 'claude-sonnet-5-5' }
  });
  test.skip(
    !configured.ok(),
    `a Home needs a configured model; run under with-keys (${(await configured.text()).slice(0, 120)})`
  );
  const assistant = (await (await request.get('/api/personal-assistant')).json())
    .personal_assistant;
  const hire = await request.post('/api/personal-assistant/hire', {
    data: {
      request_id: 'albums-follow-up-hire',
      if_version: assistant.state_version || 0,
      display_name: 'Atlas',
      mandate: 'Help me with my songs.',
      focus_areas: ['plan_my_day']
    }
  });
  expect(hire.status(), await hire.text()).toBe(201);
  const hq = await request.post('/api/personal-assistant/hq', {
    data: {
      request_id: 'albums-follow-up-hq',
      if_version: (await hire.json()).personal_assistant.state_version,
      name: 'My HQ',
      timezone: 'UTC'
    }
  });
  expect(hq.status(), await hq.text()).toBe(201);
}

// setUpFolder feeds the folder, lets the skipped look report, then sets it up
// with the Home step (and what it needs) chosen, and waits for the run to end.
async function setUpFolder(page: Page, request: APIRequestContext, folder: string) {
  const root = await writeAlbums(folder);
  await page.goto('/?panel=today&folder=show');
  const entry = page.locator('#personalAssistantFolderPathEntry');
  await expect(entry).toBeVisible();
  await entry.locator('input').fill(root);
  await entry.getByRole('button').click();
  const card = page.locator('#personalAssistantFolderAgent');
  await expect(card).toContainText(`Hire an agent for ${folder}`);
  await card.locator('[data-folder-agent-action="start"]').click();
  const get = async () => (await views(request)).find(v => v.folder_name === folder);
  await expect.poll(async () => (await get())?.state, { timeout: 30_000 }).toBe('reported');
  let view = (await get())!;
  // The owner says what the folder is before anything can be set up.
  const confirmed = await request.post(`${endpoint}/${view.workspace_id}/premise`, {
    data: { answer: 'confirmed' }
  });
  expect(confirmed.ok(), await confirmed.text()).toBe(true);
  view = (await get())!;
  // The Home and what it needs, never the group (the two exclude each other).
  const picks = (view.plan?.steps || [])
    .filter(step => step.kind === 'home' || step.kind === 'integration')
    .map(step => step.handle);
  expect(picks, 'the plan must offer the Home step').toContain('music-production-home');
  const planned = await request.post(`${endpoint}/${view.workspace_id}/plan`, {
    data: { selected: picks }
  });
  expect(planned.ok(), await planned.text()).toBe(true);
  const plan = (await planned.json()).plan;
  const run = await request.post(`${endpoint}/${view.workspace_id}/setup`, {
    data: { plan_digest: plan.digest, selected: plan.selected, hire_faces: {} }
  });
  expect(run.ok(), await run.text()).toBe(true);
  await expect
    .poll(async () => (await get())?.setup?.status, { timeout: 240_000, intervals: [2000] })
    .toBe('done');
  const done = (await get())!;
  expect(
    done.setup?.receipt?.home?.name,
    'the run must have placed the folder in a Home'
  ).toBeTruthy();
  return done;
}

// openThread opens the folder's workspace and its Commander chat, where the
// report's cards are drawn under the report message.
async function openThread(page: Page, view: FolderView) {
  await page.goto(`${view.route}?view=details`);
  const closeAssistant = page.getByRole('button', {
    name: 'Close personal assistant',
    exact: true
  });
  if (await closeAssistant.isVisible()) await closeAssistant.click();
  await page.getByRole('button', { name: 'Open in chat', exact: true }).click();
  const thread = page.locator('#homeAssistantConversation');
  await expect(thread.locator('[data-folder-agent-chat-part="receipt"]')).toBeVisible();
  return thread;
}

// writes records every request that could change something, so a test can say
// exactly what an action sent (reads and the drawer's own "seen" note excluded).
function writes(page: Page) {
  const sent: { url: string; body: string }[] = [];
  page.on('request', request => {
    const url = request.url();
    if (request.method() === 'POST' && url.includes('/folder-agent/')) {
      sent.push({ url: url.split('/folder-agent/')[1], body: request.postData() || '' });
    }
  });
  return {
    all: () => sent.filter(item => !item.url.endsWith('/chat/seen')),
    to: (suffix: string) => sent.filter(item => item.url.endsWith(suffix))
  };
}

const steps = (thread: ReturnType<Page['locator']>) => thread.locator('[data-chat-step]');

const views_: { albums?: FolderView; singles?: FolderView } = {};

test('[real] the setup runs and the question draws alone under a collapsed receipt', async ({
  page,
  request
}) => {
  test.skip(!sandbox, 'requires the isolated server HOME');
  test.setTimeout(360_000);
  await mkdir(evidence, { recursive: true, mode: 0o750 });
  await installLocalCdn(page);
  await seedAssistant(request);
  views_.albums = await setUpFolder(page, request, 'Albums');
  const sent = writes(page);
  const thread = await openThread(page, views_.albums);

  const receipt = thread.locator('[data-folder-agent-chat-part="receipt"]');
  await expect(receipt).toContainText('Albums is set up in your new');
  const toggle = receipt.getByRole('button', { name: 'View setup details' });
  await expect(toggle).toHaveAttribute('aria-expanded', 'false');
  await expect(steps(thread)).toHaveCount(1);
  const question = thread.locator('[data-chat-step="question"]');
  await expect(question.getByRole('heading')).toHaveText('Learn the 5 REAPER songs?');
  await expect(question).toContainText('Nothing is changed, and audio is not opened.');
  await expect(question).toContainText("1 Logic Pro song can't be read this way.");
  await expect(question.getByRole('button')).toHaveText(['Learn 5 songs', 'Not now']);
  // Nothing but the question: no ticket, no goal, no Look further while it is open.
  await expect(thread.getByText('First ticket')).toHaveCount(0);
  await expect(thread.getByText('Start this ticket')).toHaveCount(0);
  await expect(thread.getByRole('button', { name: 'Look further' })).toHaveCount(0);
  // Opening the details, restoring the view and redrawing send nothing.
  await toggle.click();
  await expect(toggle).toHaveAttribute('aria-expanded', 'true');
  await expect(receipt).toContainText('Music Production Home');
  await page.reload();
  const again = await openThread(page, views_.albums);
  await expect(again.locator('[data-chat-step="question"]')).toBeVisible();
  await expect(again.locator('[data-chat-step]')).toHaveCount(1);
  expect(sent.all(), 'a write was sent before any answer').toEqual([]);
  await page.screenshot({ path: join(evidence, 'e2e-01-receipt-and-question.png') });
});

test('[real] Not now sends one dismiss answer, reads nothing and moves on at once; a reload resumes', async ({
  page,
  request
}) => {
  test.skip(!sandbox || !views_.albums, 'requires the first journey');
  await installLocalCdn(page);
  const sent = writes(page);
  const thread = await openThread(page, views_.albums!);
  const question = thread.locator('[data-chat-step="question"]');
  await expect(question).toBeVisible();
  // Keyboard only.
  await question.getByRole('button', { name: 'Not now' }).focus();
  await page.keyboard.press('Enter');
  await expect(thread.locator('[data-chat-step="question"]')).toHaveCount(0);
  expect(sent.to('/songs')).toHaveLength(1);
  expect(JSON.parse(sent.to('/songs')[0].body)).toEqual({ answer: 'dismiss' });
  expect(sent.all()).toHaveLength(1);
  // The next card is the next eligible decision, straight away: the agent's
  // goal when its look wrote one (it comes before the ticket), else its first
  // ticket, else the one quiet Look further. No learning card, nothing was read.
  await expect(thread.locator('[data-chat-step="learning"]')).toHaveCount(0);
  const next = thread.locator(
    '[data-chat-step="goal"], [data-chat-step="ticket"], [data-chat-step="further"]'
  );
  await expect(next).toHaveCount(1);
  // Focus followed the answer to the new card (the pressed button is gone).
  await expect(page.locator(':focus')).toHaveAttribute('data-chat-target', '');
  await expect(next).toContainText(await page.locator(':focus').innerText());
  const after = (await views(request)).find(v => v.folder_name === 'Albums')!;
  expect(after.learning).toBeUndefined();
  expect(after.songs_offer).toBeUndefined();
  await page.screenshot({ path: join(evidence, 'e2e-02-after-not-now.png') });

  // Reload: resumes from the saved answer, asks nothing, sends nothing.
  await page.reload();
  const again = await openThread(page, views_.albums!);
  await expect(again.locator('[data-chat-step="question"]')).toHaveCount(0);
  await expect(
    again.locator('[data-chat-step="goal"], [data-chat-step="ticket"], [data-chat-step="further"]')
  ).toBeVisible();
  expect(sent.to('/songs')).toHaveLength(1);
});

test('[real] Learn 5 songs sends one learn answer, the reader reads the songs and the result is the actual count', async ({
  page,
  request
}) => {
  test.skip(!sandbox || !views_.albums, 'requires the first journey');
  test.setTimeout(360_000);
  await installLocalCdn(page);
  // A second folder joins the same Home: a Not now on Albums did not answer for it.
  views_.singles = await setUpFolder(page, request, 'Singles');
  const sent = writes(page);
  const thread = await openThread(page, views_.singles);
  const question = thread.locator('[data-chat-step="question"]');
  await expect(question.getByRole('heading')).toHaveText('Learn the 5 REAPER songs?');
  await question.getByRole('button', { name: 'Learn 5 songs' }).focus();
  await page.keyboard.press('Enter');
  // Pressed twice in a row: the second goes nowhere.
  await page.keyboard.press('Enter');
  await expect
    .poll(
      async () => (await views(request)).find(v => v.folder_name === 'Singles')?.learning?.status,
      {
        timeout: 60_000
      }
    )
    .toBe('done');
  const learned = (await views(request)).find(v => v.folder_name === 'Singles')!;
  expect(learned.learning?.done).toBe(5);
  expect(sent.to('/songs')).toHaveLength(1);
  expect(JSON.parse(sent.to('/songs')[0].body)).toEqual({ answer: 'learn' });
  const result = thread.locator('[data-chat-step="learning"]');
  await expect(result).toContainText('Learned 5');
  await expect(result).toContainText("1 Logic Pro song can't be read this way.");
  await expect(thread.locator('[data-chat-step="question"]')).toHaveCount(0);
  await page.screenshot({ path: join(evidence, 'e2e-03-learned-result.png') });
  // Reload: the saved result again, and nothing is asked or sent.
  await page.reload();
  const again = await openThread(page, views_.singles);
  await expect(again.locator('[data-chat-step="learning"]')).toContainText('Learned 5');
  expect(sent.to('/songs')).toHaveLength(1);
});

// ---- Controlled states: the real thread, a changed view ----

async function control(page: Page, view: FolderView, change: (copy: any) => any) {
  const real = (await (await page.request.get(`${endpoint}/${view.workspace_id}`)).json())
    .folder_agent;
  const current: { value: any } = { value: change(structuredClone(real)) };
  await page.route(`**${endpoint}/${view.workspace_id}`, route =>
    route.request().method() === 'GET'
      ? route.fulfill({ json: { folder_agent: current.value } })
      : route.fallback()
  );
  return {
    set: (next: any) => {
      current.value = next;
    },
    real
  };
}

const ticket = {
  id: 't-1',
  title: 'Open Album-1 and write a short note on its tracks.',
  started: false
};

test('[controlled] a goal comes before the ticket; using it sends one goal answer and never starts the ticket', async ({
  page
}) => {
  test.skip(!sandbox || !views_.singles, 'requires the learning journey');
  await installLocalCdn(page);
  const sent = writes(page);
  const view = views_.singles!;
  const goal = { goal: 'Keep these songs moving toward release.', max_runes: 300 };
  const withGoal = (copy: any) => ({
    ...copy,
    goal_offer: goal,
    next_ticket: ticket,
    actions: ['start_next', 'look_further']
  });
  const controlled = await control(page, view, withGoal);
  await page.route(`**${endpoint}/${view.workspace_id}/goal`, async route => {
    controlled.set({ ...withGoal(controlled.real), goal_offer: undefined });
    await route.fulfill({
      json: { folder_agent: { ...withGoal(controlled.real), goal_offer: undefined } }
    });
  });
  const thread = await openThread(page, view);
  const kinds = thread.locator('[data-chat-step]');
  await expect(kinds.filter({ hasText: 'A goal for this place' })).toBeVisible();
  await expect(thread.getByText('First ticket')).toHaveCount(0);
  await expect(thread.getByRole('button', { name: 'Start this ticket' })).toHaveCount(0);
  await thread.getByRole('button', { name: 'Use this goal' }).click();
  await expect(thread.getByText('First ticket')).toBeVisible();
  expect(sent.to('/goal')).toHaveLength(1);
  expect(JSON.parse(sent.to('/goal')[0].body)).toEqual({ answer: 'use' });
  expect(sent.to('/start-next')).toHaveLength(0);
  await page.screenshot({ path: join(evidence, 'e2e-04-goal-then-ticket.png') });
});

test('[controlled] a first ticket is started once even when pressed twice, and is then shown as started', async ({
  page
}) => {
  test.skip(!sandbox || !views_.singles, 'requires the learning journey');
  await installLocalCdn(page);
  const sent = writes(page);
  const view = views_.singles!;
  // The real view still carries a goal offer (the goal test above intercepted its
  // answer), which would come first; this test is about the ticket.
  const open = (copy: any) => ({
    ...copy,
    goal_offer: undefined,
    next_ticket: ticket,
    actions: ['start_next', 'look_further']
  });
  const controlled = await control(page, view, open);
  await page.route(`**${endpoint}/${view.workspace_id}/start-next`, async route => {
    const started = {
      ...controlled.real,
      goal_offer: undefined,
      next_ticket: { ...ticket, started: true },
      actions: ['look_further']
    };
    controlled.set(started);
    await route.fulfill({ json: { folder_agent: started } });
  });
  const thread = await openThread(page, view);
  const start = thread.getByRole('button', { name: 'Start this ticket' });
  await start.dblclick();
  await expect(thread.getByRole('heading', { name: 'Ticket started' })).toBeVisible();
  expect(sent.to('/start-next')).toHaveLength(1);
  await expect(thread.getByRole('button', { name: 'Start this ticket' })).toHaveCount(0);
  await expect(thread.getByRole('button', { name: 'Look further' })).toBeVisible();
  // Reopened from the saved report: still started, no second start.
  await page.reload();
  const again = await openThread(page, view);
  await expect(again.getByRole('heading', { name: 'Ticket started' })).toBeVisible();
  expect(sent.to('/start-next')).toHaveLength(1);
});

test('[controlled] a stopped reading and an update-only offer stay reachable beside an available ticket', async ({
  page
}) => {
  test.skip(!sandbox || !views_.singles, 'requires the learning journey');
  await installLocalCdn(page);
  const sent = writes(page);
  const view = views_.singles!;
  const stopped = (copy: any) => ({
    ...copy,
    goal_offer: undefined,
    next_ticket: ticket,
    actions: ['start_next', 'look_further'],
    learning: {
      status: 'stopped',
      line: 'Learning stopped.',
      done: 2,
      total: 5,
      reason: 'No model is set for the app assistant.',
      action: 'retry'
    }
  });
  const controlled = await control(page, view, stopped);
  let thread = await openThread(page, view);
  // One primary prompt (the ticket). The recovery is behind a disclosure.
  await expect(thread.getByRole('button', { name: 'Start this ticket' })).toBeVisible();
  await expect(thread.getByRole('button', { name: 'Try again' })).toHaveCount(0);
  const details = thread.getByRole('button', { name: 'Learning details' });
  await details.click();
  await expect(thread.getByRole('button', { name: 'Try again' })).toBeVisible();
  await expect(thread.getByText('Learning stopped.')).toBeVisible();
  await page.screenshot({ path: join(evidence, 'e2e-05-stopped-details.png') });
  // The same, with only an update to offer.
  controlled.set({
    ...controlled.real,
    goal_offer: undefined,
    learning: undefined,
    next_ticket: ticket,
    actions: ['start_next', 'look_further'],
    songs_offer: {
      count: 5,
      noun: 'songs',
      format: 'REAPER',
      ask: 'Update REAPER to read the 5 songs.',
      update: 'REAPER'
    }
  });
  await page.reload();
  thread = await openThread(page, view);
  await expect(thread.getByRole('button', { name: 'Start this ticket' })).toBeVisible();
  await thread.getByRole('button', { name: 'Plugin update' }).click();
  await expect(thread.getByRole('link', { name: 'Update' })).toHaveAttribute('href', '/plugins');
  expect(sent.all(), 'looking at a notice sent something').toEqual([]);
});

test('[controlled] a report with no Home receipt keeps the single card', async ({ page }) => {
  test.skip(!sandbox || !views_.singles, 'requires the learning journey');
  await installLocalCdn(page);
  const view = views_.singles!;
  await control(page, view, copy => ({
    ...copy,
    setup: { ...copy.setup, receipt: { installed: ['Ori REAPER Plugin 0.12.0'] } },
    landing: undefined,
    songs_offer: undefined,
    learning: undefined
  }));
  await page.goto(`${view.route}?view=details`);
  const closeAssistant = page.getByRole('button', {
    name: 'Close personal assistant',
    exact: true
  });
  if (await closeAssistant.isVisible()) await closeAssistant.click();
  await page.getByRole('button', { name: 'Open in chat', exact: true }).click();
  const thread = page.locator('#homeAssistantConversation');
  await expect(thread.locator('.folder-agent-card--report')).toBeVisible();
  await expect(thread.locator('[data-folder-agent-chat-part]')).toHaveCount(0);
});

test('[controlled] a reload in the middle of the reading resumes it, sends nothing, and it finishes', async ({
  page
}) => {
  test.skip(!sandbox || !views_.singles, 'requires the learning journey');
  await installLocalCdn(page);
  const sent = writes(page);
  const view = views_.singles!;
  const reading = (done: number) => ({
    status: 'running',
    line: `REAPER Assistant is reading Album-${done + 1}… (${done + 1} of 5)`,
    done,
    total: 5
  });
  const midRead = (copy: any, done = 2) => ({
    ...copy,
    goal_offer: undefined,
    songs_offer: undefined,
    next_ticket: undefined,
    actions: ['look_further'],
    learning: reading(done)
  });
  const controlled = await control(page, view, copy => midRead(copy));
  let thread = await openThread(page, view);
  const learning = thread.locator('[data-chat-step="learning"]');
  await expect(learning).toContainText('reading Album-3… (3 of 5)');
  await expect(learning.locator('progress')).toHaveJSProperty('value', 2);
  // Reloaded while it reads: it comes back from the saved state, still reading.
  await page.reload();
  thread = await openThread(page, view);
  await expect(thread.locator('[data-chat-step="learning"]')).toContainText(
    'reading Album-3… (3 of 5)'
  );
  // The page keeps following it: the next view moves the progress, then it ends.
  controlled.set(midRead(controlled.real, 4));
  await expect(thread.locator('[data-chat-step="learning"]')).toContainText('(5 of 5)', {
    timeout: 15_000
  });
  controlled.set({
    ...midRead(controlled.real),
    learning: { status: 'done', line: 'Learned 5 songs.', done: 5, total: 5 }
  });
  await expect(thread.locator('[data-chat-step="learning"]')).toContainText('Learned 5 songs.', {
    timeout: 15_000
  });
  // Nothing about opening, reloading or following it sent an answer.
  expect(sent.all(), 'restoring a reading sent something').toEqual([]);
  await page.screenshot({ path: join(evidence, 'e2e-06-resumed-reading.png') });
});

test('[controlled] receipt, result and ticket together do not scroll the drawer header out of view at 504 px', async ({
  page
}) => {
  test.skip(!sandbox || !views_.singles, 'requires the learning journey');
  // The width where a hidden live region once stretched the panel's scroll area.
  await page.setViewportSize({ width: 504, height: 861 });
  await installLocalCdn(page);
  const view = views_.singles!;
  await control(page, view, copy => ({
    ...copy,
    goal_offer: undefined,
    songs_offer: undefined,
    next_ticket: ticket,
    actions: ['start_next', 'look_further'],
    learning: { status: 'done', line: 'Learned 5 songs.', done: 5, total: 5 }
  }));
  const thread = await openThread(page, view);
  await expect(thread.locator('[data-chat-step="ticket"]')).toBeVisible();
  const panel = page.locator('#personalAssistantPanel');
  await expect
    .poll(() => panel.evaluate(el => el.scrollHeight - el.clientHeight), {
      message: 'the drawer panel has scrollable overflow that hides its header'
    })
    .toBeLessThanOrEqual(1);
  expect(await panel.evaluate(el => el.scrollTop)).toBe(0);
  const header = page.locator('#personalAssistantPanel .personal-assistant-panel__header');
  const [headerTop, panelTop] = await Promise.all([
    header.evaluate(el => el.getBoundingClientRect().top),
    panel.evaluate(el => el.getBoundingClientRect().top)
  ]);
  expect(headerTop).toBeGreaterThanOrEqual(panelTop);
  await page.screenshot({ path: join(evidence, 'e2e-07-tall-thread-504.png') });
});
