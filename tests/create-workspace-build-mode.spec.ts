import { test, expect, Page, Route } from '@playwright/test';
import { mockHiredAssistant, mockUnhiredAssistant } from './helpers/hired-assistant';
import { installLocalCdn } from './helpers/offline-cdn';

/**
 * E2E for "Build with your assistant" (tasks/prd-build-with-your-assistant.md):
 * Create Workspace with one active surface and one shared draft.
 *
 * The build-session endpoints are answered by a scripted fake in this file, so
 * no model runs; everything else — Home, the wizard and its setters, the team
 * plan, map placement, and the final Create — is the real server. The fake
 * keeps the server's contract: one open build, versions, server chip ids, form
 * edits said back, and a blueprint switch reported as `blueprint_changed`.
 *
 * Run against a fresh sandbox (a demoed one taints slug uniqueness):
 *   ./scripts/demo-server.sh 8931 "$TMPDIR/ori-e2e.<n>"
 *   ./scripts/e2e.sh tests/create-workspace-build-mode.spec.ts -- --workers=1
 */

type Choice = { id: string; label: string };
type Entry = {
  role: 'user' | 'assistant' | 'form';
  text: string;
  choices?: Choice[];
  chosen?: string;
};

// One scripted assistant turn: what it says, the draft fields it set, a team
// patch, the chips it asks with, and whether the user said "create it".
type Reply = {
  text: string;
  set?: Record<string, unknown>;
  team?: Record<string, unknown>;
  choices?: string[];
  createNow?: boolean;
};

type Session = {
  id: string;
  status: string;
  version: number;
  entry_point: string;
  assistant: { display_name: string; appearance: Record<string, unknown> };
  transcript: Entry[];
  draft: Record<string, unknown>;
  applied: string[];
  team_patch: Record<string, unknown> | null;
  team_state: Record<string, unknown> | null;
  pending_question: { text: string; choices: Choice[] } | null;
  create_now: boolean;
  furthest_step: number;
  turn_count: number;
};

type RequestBody = Record<string, any>;

type FakeBuild = {
  session: Session | null;
  creates: RequestBody[];
  turns: RequestBody[];
  drafts: RequestBody[];
  abandons: RequestBody[];
};

// The draft keys a turn can set, by the field name the session reports.
const FIELD_OF: Record<string, string> = {
  template_id: 'blueprint',
  blank: 'blueprint',
  name: 'name',
  description: 'description',
  blueprint_inputs: 'inputs',
  parent_id: 'parent',
  color: 'color',
  tags: 'tags'
};

async function fakeBuildServer(
  page: Page,
  script: Reply[],
  options: {
    available?: boolean;
    availabilityGate?: () => Promise<void>;
    turnGate?: () => Promise<void>;
    draftFailures?: number;
    turnFailures?: number;
  } = {}
): Promise<FakeBuild> {
  const fake: FakeBuild = { session: null, creates: [], turns: [], drafts: [], abandons: [] };
  let sequence = 0;
  const respond = (route: Route, status: number, body: unknown) =>
    route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) });

  const runTurn = (session: Session, userText: string) => {
    session.transcript.push({ role: 'user', text: userText });
    session.turn_count += 1;
    const next = script.shift() || { text: 'Anything else?' };
    const applied: string[] = [];
    for (const [key, value] of Object.entries(next.set || {})) {
      // A blueprint is one choice: the server sets blank and template_id together.
      if (key === 'template_id') delete session.draft.blank;
      if (key === 'blank') delete session.draft.template_id;
      session.draft[key] = value;
      const field = FIELD_OF[key];
      if (field && !applied.includes(field)) applied.push(field);
    }
    if (next.team) {
      session.team_patch = next.team;
      applied.push('team');
    }
    const choices = (next.choices || []).map((label, index) => ({
      id: `c${session.turn_count}-${index + 1}`,
      label
    }));
    session.transcript.push({
      role: 'assistant',
      text: next.text,
      ...(choices.length ? { choices } : {})
    });
    session.pending_question = choices.length ? { text: next.text, choices } : null;
    session.applied = applied;
    session.create_now = Boolean(next.createNow);
    session.version += 1;
  };

  await page.route(/\/api\/workspaces\/build-sessions(\/.*)?$/, async route => {
    const request = route.request();
    const method = request.method();
    const path = new URL(request.url()).pathname.replace('/api/workspaces/build-sessions', '');
    if (method === 'GET' && path === '/availability') {
      await options.availabilityGate?.();
      return respond(
        route,
        200,
        options.available === false ? { available: false, reason: 'no_model' } : { available: true }
      );
    }
    const body: RequestBody = request.postDataJSON() || {};
    if (method === 'POST' && path === '') {
      fake.creates.push(body);
      if (fake.session?.status === 'open') {
        return respond(route, 200, { session: fake.session, resumed: true });
      }
      sequence += 1;
      const session: Session = {
        id: `spec-build-${sequence}`,
        status: 'open',
        version: 1,
        entry_point: String(body.entry_point || ''),
        assistant: { display_name: 'Atlas', appearance: {} },
        transcript: [],
        draft: {},
        applied: [],
        team_patch: null,
        team_state: null,
        pending_question: null,
        create_now: false,
        furthest_step: 1,
        turn_count: 0
      };
      fake.session = session;
      if (body.first_message) runTurn(session, String(body.first_message));
      return respond(route, 201, { session });
    }
    const match = path.match(/^\/([^/]+)\/(turns|draft|abandon)$/);
    const session = fake.session;
    if (!match || !session || decodeURIComponent(match[1]) !== session.id) {
      return respond(route, 404, { error: 'Build session not found.', code: 'not_found' });
    }
    if (match[2] === 'abandon') {
      fake.abandons.push(body);
      session.status = 'abandoned';
      return respond(route, 200, { session });
    }
    if (Number(body.version) !== session.version) {
      return respond(route, 409, {
        error: 'The build changed since you last saw it.',
        code: 'version'
      });
    }
    if (match[2] === 'turns') {
      await options.turnGate?.();
      if (options.turnFailures && options.turnFailures-- > 0) {
        return respond(route, 503, { error: 'Injected turn failure' });
      }
      fake.turns.push(body);
      let text = String(body.text || '');
      if (body.choice_id) {
        const asked = [...session.transcript]
          .reverse()
          .find(entry => entry.role === 'assistant' && entry.choices?.length);
        const choice = asked?.choices?.find(candidate => candidate.id === body.choice_id);
        if (!asked || !choice) {
          return respond(route, 409, {
            error: 'That choice is no longer offered.',
            code: 'choice'
          });
        }
        asked.chosen = choice.id;
        text = choice.label;
      }
      runTurn(session, text);
      return respond(route, 200, { session });
    }
    // The draft is the form: a sync records it quietly; the user's own edit is
    // said back, and a new blueprint asks the assistant to re-staff.
    fake.drafts.push(body);
    if (options.draftFailures && options.draftFailures-- > 0) {
      return respond(route, 503, { error: 'Injected draft failure' });
    }
    const before = String(session.draft.template_id || '');
    session.draft = { ...(body.draft || {}) };
    if (body.team_state) session.team_state = body.team_state;
    if (body.step) session.furthest_step = Math.max(session.furthest_step, Number(body.step));
    const after = String(session.draft.template_id || '');
    const blueprintChanged = !body.sync && before !== after;
    if (blueprintChanged) {
      session.transcript.push({ role: 'form', text: `You switched the blueprint to ${after}` });
    }
    session.applied = [];
    session.create_now = false;
    session.version += 1;
    return respond(route, 200, {
      session,
      ...(blueprintChanged ? { blueprint_changed: true } : {})
    });
  });
  return fake;
}

const pane = (page: Page) => page.locator('#workspaceBuildPane');
const composer = (page: Page) => page.locator('#workspaceBuildComposerInput');

function isTurn(url: string) {
  return /\/api\/workspaces\/build-sessions\/[^/]+\/turns$/.test(new URL(url).pathname);
}

function cardByLabel(page: Page, label: string) {
  return page.locator('#templatePicker .workspace-template-card').filter({
    has: page.locator('.workspace-template-card-label', { hasText: new RegExp(`^${label}$`) })
  });
}

async function openFromHome(page: Page) {
  await page.request.post('/api/onboarding/skip').catch(() => {});
  await page.goto('/');
  await page.locator('#cockpitCreateWorkspaceBtn').click();
  await expect(page.locator('#addFolderModal')).toBeVisible();
}

// A turn is over when its response is in and the composer is enabled again.
async function settle(page: Page, turn: Promise<unknown>) {
  await turn;
  await expect(composer(page)).toBeEnabled();
}

async function say(page: Page, text: string) {
  await expect(composer(page)).toBeEnabled();
  await composer(page).fill(text);
  const turn = page.waitForResponse(response => isTurn(response.url()));
  await composer(page).press('Enter');
  await settle(page, turn);
}

async function choose(page: Page, label: string) {
  const turn = page.waitForResponse(response => isTurn(response.url()));
  await pane(page).getByRole('button', { name: label }).click();
  await settle(page, turn);
}

// Home's create ends on the map. A sandbox that already holds workspaces may
// put the first candidate on occupied ground, so nudge until it is open.
async function placeOnMap(page: Page) {
  const preview = page.locator('[data-ws-map-placement-preview]');
  await expect(preview).toBeVisible();
  const occupied = async () => /\bis-invalid\b/.test((await preview.getAttribute('class')) || '');
  await page.locator('[data-ws-map-viewport]').focus();
  for (let nudge = 0; nudge < 60 && (await occupied()); nudge += 1) {
    await page.keyboard.press('ArrowDown');
  }
  expect(await occupied(), 'open ground was found for the new workspace').toBe(false);
  // Confirm from the keyboard: moving the pointer to the button would move
  // the candidate with it.
  await page.keyboard.press('Enter');
  await expect(preview).toBeHidden();
}

async function currentStep(page: Page) {
  return page.evaluate(() => (window as any).sessionManager.wizardStep);
}

async function manual(page: Page) {
  await pane(page).getByRole('button', { name: 'Set up manually' }).click();
  await expect(pane(page)).toBeHidden();
  await expect(page.locator(`#wizardStep${await currentStep(page)}Title`)).toBeFocused();
}

async function assisted(page: Page) {
  await page.locator('#workspaceBuildWithBtn').click();
  await expect(pane(page)).toBeVisible();
}

test.beforeEach(async ({ page }) => {
  await installLocalCdn(page);
  await page.route('**/api/onboarding/status', route => route.fulfill({ json: { needs_onboarding: false, completed: true } }));
  await mockHiredAssistant(page);
});

test('golden path: three turns and no form clicks create the workspace the build drafted', async ({
  page
}) => {
  const name = `Newsletter Desk ${Date.now().toString(36)}`;
  const fake = await fakeBuildServer(page, [
    {
      text: `I picked Content Production and named it ${name}. Who should staff it?`,
      set: {
        template_id: 'content-production',
        name,
        description: 'Turns my research notes into a weekly newsletter.'
      },
      choices: ['A Content Lead and a Brand Copywriter', 'Just a Content Lead']
    },
    {
      text: 'Done — a new Content Lead and a new Brand Copywriter. Check the review, or say “create it”.',
      team: {
        mode: 'staffed',
        roles: [
          { role_id: 'content-lead', mode: 'create', agent_name: 'Content Lead' },
          { role_id: 'brand-copywriter', mode: 'create', agent_name: 'Brand Copywriter' }
        ]
      }
    },
    { text: 'Creating it now.', createNow: true }
  ]);
  const created: RequestBody[] = [];
  page.on('request', request => {
    if (request.method() === 'POST' && new URL(request.url()).pathname === '/api/workspaces') {
      created.push(request.postDataJSON());
    }
  });

  await openFromHome(page);
  await expect(pane(page)).toBeVisible();
  await expect(pane(page)).toContainText('What should this workspace do?');
  await expect(composer(page)).toBeFocused();
  expect(fake.creates[0]).toMatchObject({ entry_point: 'home_cockpit_create' });

  await say(page, 'A workspace that turns my research notes into a weekly newsletter');
  await expect(page.locator('#folderNameInput')).toHaveValue(name);
  await expect(page.locator('#folderDescriptionInput')).toHaveValue(
    'Turns my research notes into a weekly newsletter.'
  );
  await expect(page.locator('[data-build-chosen="name"]')).toHaveText('Chosen by Atlas');
  await expect(composer(page)).toBeFocused();

  await choose(page, 'A Content Lead and a Brand Copywriter');
  await expect(pane(page)).toContainText('a new Content Lead and a new Brand Copywriter');

  await say(page, 'create it');
  await placeOnMap(page);
  await expect.poll(() => created.length).toBe(1);

  expect(fake.turns).toHaveLength(3);
  const payload = created[0];
  expect(payload.build_session_id).toBe(fake.session?.id);
  expect(payload.template_id).toBe('content-production');
  expect(payload.name).toBe(name);
  // A fresh sandbox creates both agents; one that already holds agents by
  // these names assigns them instead (the team draft never duplicates one).
  const staffing = (payload.role_staffing || []).map((role: RequestBody) => [
    role.role_id,
    role.name
  ]);
  expect(staffing).toEqual(
    expect.arrayContaining([
      ['content-lead', 'Content Lead'],
      ['brand-copywriter', 'Brand Copywriter']
    ])
  );
  for (const role of payload.role_staffing || []) {
    expect(['create', 'assign']).toContain(role.mode);
  }

  // The real server created it from that request.
  await expect
    .poll(async () => {
      const response = await page.request.get('/api/workspaces');
      const body = await response.json();
      const list = Array.isArray(body) ? body : body.workspaces || [];
      return list.some((workspace: RequestBody) => workspace.name === name);
    })
    .toBe(true);
});

test('a blueprint switched on the form is said back and the assistant re-staffs', async ({
  page
}) => {
  const name = `Field Notes ${Date.now().toString(36)}`;
  const fake = await fakeBuildServer(page, [
    {
      text: `I picked Content Production and named it ${name}.`,
      set: { template_id: 'content-production', name, description: 'Weekly field notes.' }
    },
    { text: 'You switched to Research Project — I’ll staff it for research instead.' }
  ]);

  await openFromHome(page);
  await say(page, 'A place for my weekly field notes');
  await expect(page.locator('#folderNameInput')).toHaveValue(name);

  // The user deliberately reveals the shared form, then picks another card.
  await manual(page);
  while ((await currentStep(page)) > 1) await page.locator('#wizardBackBtn').click();
  await expect(cardByLabel(page, 'Content Production')).toContainText('Chosen by Atlas');
  const autoTurn = page.waitForRequest(
    request => request.method() === 'POST' && isTurn(request.url())
  );
  await cardByLabel(page, 'Research Project').click();
  await expect.poll(() => fake.drafts.some(draft => draft.draft?.template_id === 'research-project')).toBe(true);
  expect(fake.turns).toHaveLength(1); // Manual mode does not run hidden assistant work.
  await assisted(page);

  const turn = await autoTurn;
  expect(turn.postDataJSON().text).toBe('(I changed the blueprint)');
  const edit = fake.drafts.find(
    draft => !draft.sync && draft.draft?.template_id === 'research-project'
  );
  expect(edit, 'the switch reaches the build as the user’s own edit').toBeTruthy();
  await expect(composer(page)).toBeEnabled();
  await expect(pane(page).locator('.workspace-build-entry--form')).toContainText(
    'research-project'
  );
  await expect(pane(page)).toContainText('I’ll staff it for research instead');
  await expect(cardByLabel(page, 'Content Production')).not.toContainText('Chosen by Atlas');
});

test('closing mid-build and reopening offers Resume, which restores the form and the step', async ({
  page
}) => {
  const name = `Course Notes ${Date.now().toString(36)}`;
  const fake = await fakeBuildServer(page, [
    {
      text: `I picked Research Project and named it ${name}. Who should lead it?`,
      set: { template_id: 'research-project', name, description: 'Notes for my course.' },
      choices: ['Create a Research Lead']
    }
  ]);

  await openFromHome(page);
  await say(page, 'Notes for my course');
  await expect(page.locator('#folderNameInput')).toHaveValue(name);
  const step = await currentStep(page);
  expect(step).toBeGreaterThan(1);

  await page.locator('#addFolderModal').getByRole('button', { name: 'Cancel' }).click();
  await expect(page.locator('#addFolderModal')).toBeHidden();
  expect(fake.abandons, 'closing keeps the build').toHaveLength(0);

  await page.locator('#cockpitCreateWorkspaceBtn').click();
  await expect(page.locator('#addFolderModal')).toBeVisible();
  await expect(pane(page)).toContainText(`Resume building ${name}?`);
  expect(fake.creates.at(-1)).toMatchObject({ entry_point: 'home_cockpit_create' });

  await pane(page).getByRole('button', { name: 'Resume' }).click();
  await expect(page.locator('#folderNameInput')).toHaveValue(name);
  await expect(page.locator(`#wizardStep${step}`)).toBeHidden();
  await expect.poll(() => currentStep(page)).toBe(step);
  await expect(pane(page)).toContainText('Notes for my course');
  await expect(pane(page).getByRole('button', { name: 'Create a Research Lead' })).toBeEnabled();
  await expect(composer(page)).toBeFocused();
});

test('without a hired assistant the dialog is the manual wizard and nothing is built', async ({
  page
}) => {
  await mockUnhiredAssistant(page);
  const fake = await fakeBuildServer(page, []);
  const buildRequests: string[] = [];
  page.on('request', request => {
    if (request.url().includes('/build-sessions')) {
      buildRequests.push(`${request.method()} ${new URL(request.url()).pathname}`);
    }
  });

  await openFromHome(page);
  await expect(cardByLabel(page, 'Blank')).toBeVisible();
  await expect(pane(page)).toBeHidden();
  await expect(page.locator('#workspaceBuildWithBtn')).toBeHidden();
  expect(fake.creates).toHaveLength(0);
  expect(
    buildRequests.every(line => line === 'GET /api/workspaces/build-sessions/availability')
  ).toBe(true);

  await cardByLabel(page, 'Blank').click();
  await page.locator('#wizardNextBtn').click();
  await expect(page.locator('#wizardStep2')).toBeVisible();
  await page.locator('#folderNameInput').fill('Manual Notes');
  await expect(page.locator('#folderNameInput')).toHaveValue('Manual Notes');
  await expect(page.locator('[data-build-chosen]')).toHaveCount(0);
});

test('with no model for the assistant the dialog stays manual', async ({ page }) => {
  const fake = await fakeBuildServer(page, [], { available: false });
  await openFromHome(page);
  await expect(cardByLabel(page, 'Blank')).toBeVisible();
  await expect(pane(page)).toBeHidden();
  expect(fake.creates).toHaveLength(0);
});

test('the assistant’s create confirmation opens the build with the request as its first turn', async ({
  page
}) => {
  const ask = 'Could you set up a newsletter workspace for me?';
  const fake = await fakeBuildServer(page, [
    {
      text: 'I picked Content Production for your newsletter.',
      set: { template_id: 'content-production' }
    }
  ]);
  // Route the sentence to the inline Home path, where /ask proposes the build.
  await page.route('**/api/home-assistant/route', route =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ intent: 'app_introspection' })
    })
  );
  const asks: RequestBody[] = [];
  await page.route('**/api/home-assistant/ask', route => {
    asks.push(route.request().postDataJSON());
    return route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        response: '',
        requires_confirmation: true,
        confirmation: {
          action_id: 'spec-build-action',
          action_type: 'build_workspace',
          summary: 'Build this workspace with Atlas?',
          arguments: { first_message: ask }
        }
      })
    });
  });

  await page.request.post('/api/onboarding/skip').catch(() => {});
  await page.goto('/');
  await page.locator('#personalAssistantLauncher').click();
  await page.locator('#personalAssistantInput').fill(ask);
  await page.locator('#personalAssistantSend').click();
  await page.getByRole('button', { name: 'Confirm', exact: true }).click();

  await expect(page.locator('#addFolderModal')).toBeVisible();
  await expect(pane(page)).toContainText(ask);
  await expect(pane(page)).toContainText('I picked Content Production for your newsletter.');
  expect(fake.creates[0]).toMatchObject({
    entry_point: 'personal_assistant_ask',
    first_message: ask
  });
  expect(asks, 'Confirm opens the build; it never re-asks the server to run it').toHaveLength(1);
});

function gate() {
  let release!: () => void;
  const promise = new Promise<void>(resolve => { release = resolve; });
  return { promise, release };
}

test('slow availability keeps a manual draft active until an explicit switch', async ({ page }) => {
  const wait = gate();
  const fake = await fakeBuildServer(page, [], { availabilityGate: () => wait.promise });
  await openFromHome(page);
  await cardByLabel(page, 'Blank').click();
  await page.locator('#wizardNextBtn').click();
  await page.locator('#folderNameInput').fill('My manual draft');
  wait.release();
  await expect(page.locator('#workspaceBuildWithBtn')).toBeVisible();
  await expect(pane(page)).toBeHidden();
  await expect(page.locator('#folderNameInput')).toBeFocused();
  expect(fake.creates).toHaveLength(0);
  await assisted(page);
  await expect.poll(() => fake.drafts.some(draft => draft.draft.name === 'My manual draft')).toBe(true);
  await manual(page);
  await expect(page.locator('#folderNameInput')).toHaveValue('My manual draft');
  expect(await currentStep(page)).toBe(2);
});

test('late scripted turn keeps newer manual values and cannot create or reopen assisted setup', async ({ page }) => {
  const wait = gate();
  let hold = false;
  const fake = await fakeBuildServer(page, [
    { text: 'Here is your draft.', set: { template_id: 'content-production', name: 'Original draft' } },
    { text: 'Here is the late reply.', set: { name: 'Late overwritten name' }, createNow: true }
  ], { turnGate: () => hold ? wait.promise : Promise.resolve() });
  let creates = 0;
  page.on('request', request => {
    if (request.method() === 'POST' && new URL(request.url()).pathname === '/api/workspaces') creates++;
  });
  await openFromHome(page);
  await say(page, 'A content workspace');
  hold = true;
  await composer(page).fill('Rename and create it');
  const sent = page.waitForRequest(request => isTurn(request.url()));
  await composer(page).press('Enter');
  await sent;
  await manual(page);
  while ((await currentStep(page)) > 2) await page.locator('#wizardBackBtn').click();
  await page.locator('#folderNameInput').fill('Newer manual name');
  wait.release();
  await expect.poll(() => fake.drafts.some(draft => draft.draft.name === 'Newer manual name')).toBe(true);
  await expect(page.locator('#folderNameInput')).toHaveValue('Newer manual name');
  await expect(page.locator('#folderNameInput')).toBeFocused();
  await expect(pane(page)).toBeHidden();
  expect(creates).toBe(0);
  await assisted(page);
  await expect(pane(page)).toContainText('Your manual draft was kept');
  await expect(page.locator('#folderNameInput')).toHaveValue('Newer manual name');
  expect(fake.abandons).toHaveLength(0);
});

test('scripted turn failure offers Retry and preserves the populated shared draft', async ({ page }) => {
  const options = { turnFailures: 0 };
  const fake = await fakeBuildServer(page, [
    { text: 'Draft ready.', set: { blank: true, name: 'Kept draft' } },
    { text: 'Recovered without losing your draft.' }
  ], options);
  await openFromHome(page);
  await say(page, 'A blank workspace');
  options.turnFailures = 1;
  await say(page, 'Continue');
  await expect(pane(page)).toContainText('couldn’t reach my model');
  await expect(page.locator('#folderNameInput')).toHaveValue('Kept draft');
  await pane(page).getByRole('button', { name: 'Try again' }).click();
  await expect(pane(page)).toContainText('Recovered without losing your draft');
  expect(fake.turns).toHaveLength(2);
});

test('failed draft sync stays local, blocks new turns, and recovers through Retry', async ({ page }) => {
  const options = { draftFailures: 0 };
  const fake = await fakeBuildServer(page, [] , options);
  await openFromHome(page);
  await expect(composer(page)).toBeEnabled();
  options.draftFailures = 1;
  await manual(page);
  await page.locator('#wizardNextBtn').click();
  await page.locator('#folderNameInput').fill('Unsaved shared draft');
  await expect(page.locator('#workspaceBuildModeStatus')).toContainText('Draft not saved');
  expect(fake.turns).toHaveLength(0);
  await assisted(page);
  await expect(page.locator('#workspaceBuildModeStatus')).not.toContainText('Draft not saved');
  await expect.poll(() => fake.session?.draft.name).toBe('Unsaved shared draft');
  await expect(page.locator('#folderNameInput')).toHaveValue('Unsaved shared draft');
  expect(fake.turns).toHaveLength(0);
});

for (const theme of ['light', 'dark']) {
  test(`one active creation mode, keyboard switch and readable narrow layout (${theme})`, async ({ page }, testInfo) => {
    await page.addInitScript(value => localStorage.setItem('ori-theme', value), theme);
    await page.emulateMedia({ reducedMotion: 'reduce' });
    await page.setViewportSize({ width: 390, height: 844 });
    await fakeBuildServer(page, [{ text: 'We can review this draft together.', set: { blank: true, name: 'Shared draft' } }]);
    await openFromHome(page);
    await say(page, 'A simple workspace');
    await expect(page.locator('.workspace-create-modal-body')).toBeHidden();
    await expect(page.locator('#wizardNextBtn')).toBeHidden();
    await expect(page.locator('#workspaceBuildDraftSummary')).toContainText('Shared draft');
    const switcher = pane(page).getByRole('button', { name: 'Set up manually' });
    await switcher.focus();
    await expect(switcher).toHaveCSS('outline-style', 'solid');
    await page.addScriptTag({ url: 'https://cdn.jsdelivr.net/npm/axe-core@4.10.3/axe.min.js' });
    const scan = await page.evaluate(async () => {
      const result = await (window as any).axe.run('#addFolderModal', { runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa'] } });
      return { violations: result.violations, contrast: result.passes.find((rule: any) => rule.id === 'color-contrast')?.nodes.length || 0 };
    });
    expect(scan.violations).toEqual([]);
    expect(scan.contrast).toBeGreaterThan(0);
    expect(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth + 1)).toBe(false);
    await page.screenshot({ path: testInfo.outputPath(`creation-assisted-${theme}.png`), fullPage: true });
    await page.keyboard.press('Enter');
    await expect(pane(page)).toBeHidden();
    await expect(page.locator(`#wizardStep${await currentStep(page)}Title`)).toBeFocused();
    await expect(page.locator('.workspace-create-modal-body')).toBeVisible();
    await page.screenshot({ path: testInfo.outputPath(`creation-manual-${theme}.png`), fullPage: true });
    await assisted(page);
    await expect(composer(page)).toBeFocused();
    await expect(page.locator('#folderNameInput')).toHaveValue('Shared draft');
  });
}
