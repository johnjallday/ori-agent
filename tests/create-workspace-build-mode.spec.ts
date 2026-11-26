import { test, expect, type Locator, type Page, type Route } from '@playwright/test';
import { mockHiredAssistant, mockUnhiredAssistant } from './helpers/hired-assistant';
import { installLocalCdn } from './helpers/offline-cdn';
import { checkNextActionReflow } from './helpers/next-action-reflow';
import { startJanitorBuildModel } from './helpers/janitor-build-model';
import { abandonOpenBuild, prepareFolderCreationRuntime } from './helpers/folder-first-runtime';
import { mkdtempSync, realpathSync, writeFileSync } from 'node:fs';
import { join, relative } from 'node:path';
import { tmpdir } from 'node:os';

/**
 * E2E for "Build with your assistant" (tasks/prd-build-with-your-assistant.md):
 * Create Workspace with one active surface and one shared draft.
 *
 * Presentation cases intercept the build session for precise UI timing and
 * failures. The newsletter placement and recovery cases use the real build,
 * team, and creation endpoints. Only the model is scripted on loopback. No
 * credentials or live inference are required.
 *
 * Run against a fresh sandbox (a demoed one taints slug uniqueness):
 *   LM_STUDIO_BASE_URL=http://127.0.0.1:8940/v1 ORI_DEMO_NO_CODEX=1 \
 *     zsh -c 'source scripts/wt.sh && wt demo'
 *   ./scripts/e2e.sh --env ORI_BUILD_DEMO_SANDBOX='<printed demo sandbox>' \
 *     tests/create-workspace-build-mode.spec.ts -- --workers=1
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
  why?: Session['why'];
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
  pending_question: { question: string; choices: Choice[] } | null;
  create_now: boolean;
  furthest_step: number;
  turn_count: number;
  why?: { section: string; text: string; blueprint_id?: string }[];
  edited_fields?: Record<string, number>;
  folder?: { observation: Record<string, unknown> } | null;
  link?: { observation: Record<string, unknown> } | null;
};

type RequestBody = Record<string, any>;

type FakeBuild = {
  session: Session | null;
  creates: RequestBody[];
  turns: RequestBody[];
  drafts: RequestBody[];
  abandons: RequestBody[];
  links: RequestBody[];
};

// What the fake "reads" for any public address: a page's own title and
// description, as the server's observer would return them.
const FAKE_LINK = {
  version: 1,
  id: 'spec-link-1',
  url: 'https://arxiv.org/abs/1706.03762',
  host: 'arxiv.org',
  kind: 'page',
  title: 'Attention Is All You Need',
  description: 'Abstract page for arXiv paper 1706.03762',
  readable: true,
  fetched_at: '2026-10-17T09:00:00Z'
};

// The folder the fake "scans" for any choice: the composer's path-free
// observation, as the server would return it.
const FAKE_OBSERVATION = {
  version: 1,
  id: 'spec-observation-1',
  folder: 'Elite School',
  scanned_at: '2026-10-12T09:00:00Z',
  entries: 44,
  files: 42,
  kinds: [{ name: 'py', count: 30 }],
  projects: [
    { id: 'candidate-0', name: 'Elite School', files: 42, marker: 'pyproject.toml', root: true }
  ],
  coverage: {
    max_depth: 3,
    max_entries: 5000,
    budget_seconds: 3,
    partial: false,
    skipped_links: 0,
    kinds_omitted: 0,
    projects_omitted: 0
  }
};

// The draft keys a turn can set, by the field name the session reports.
const FIELD_OF: Record<string, string> = {
  template_id: 'blueprint',
  blank: 'blueprint',
  name: 'name',
  description: 'description',
  mission: 'goal',
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
  const fake: FakeBuild = {
    session: null,
    creates: [],
    turns: [],
    drafts: [],
    abandons: [],
    links: []
  };
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
    session.pending_question = choices.length ? { question: next.text, choices } : null;
    session.applied = applied;
    if (next.why) session.why = next.why;
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
        options.available === false
          ? { available: false, reason: 'no_model' }
          : { available: true, folders: true, links: true }
      );
    }
    if (method === 'GET' && path === '/folder-choices') {
      return respond(route, 200, {
        available: true,
        chips: [
          { id: 'documents', label: 'Documents' },
          { id: 'desktop', label: 'Desktop' }
        ],
        picker_available: false,
        picker_note: 'Pick a folder from the list for now.'
      });
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
    const match = path.match(/^\/([^/]+)\/(turns|draft|abandon|link|link\/detach)$/);
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
    // Add link: one address, refused in the user's words when it is not
    // public; a refusal changes nothing on the build.
    if (match[2] === 'link') {
      fake.links.push(body);
      if (String(body.url || '').includes('127.0.0.1')) {
        return respond(route, 422, {
          error:
            'That address can’t be used here. Only public http or https links without a token or key in them are accepted.',
          code: 'link'
        });
      }
      session.link = { observation: { ...FAKE_LINK } };
      session.transcript.push({
        role: 'form',
        text: 'You attached a link to arxiv.org.'
      });
      session.applied = [];
      session.version += 1;
      return respond(route, 200, { session });
    }
    if (match[2] === 'link/detach') {
      if (session.link) {
        session.link = null;
        session.transcript.push({ role: 'form', text: 'You removed the attached link.' });
        session.version += 1;
      }
      return respond(route, 200, { session });
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
    for (const field of body.edited_fields || []) {
      session.edited_fields ||= {};
      session.edited_fields[field] = session.version + 1;
    }
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

async function openFromHome(
  page: Page,
  open = () => page.locator('#cockpitCreateWorkspaceBtn').click()
) {
  await page.request.post('/api/onboarding/skip').catch(() => {});
  await page.goto('/');
  await open();
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

async function choose(page: Page, label: string, keyboard = false) {
  const turn = page.waitForResponse(response => isTurn(response.url()));
  const button = pane(page).getByRole('button', { name: label });
  if (keyboard) await activateByTab(page, button);
  else await button.click();
  await settle(page, turn);
}

async function tabTo(page: Page, button: Locator) {
  for (
    let n = 0;
    n < 80 && !(await button.evaluate(node => node === document.activeElement));
    n++
  ) {
    await page.keyboard.press('Tab');
  }
  await expect(button).toBeFocused();
}

async function activateByTab(page: Page, button: Locator) {
  await tabTo(page, button);
  await page.keyboard.press('Enter');
}

// A sandbox that already holds workspaces may put the first candidate on
// occupied ground. Nudge from the keyboard so the candidate stays put on Enter.
async function confirmMapPosition(page: Page) {
  const preview = page.locator('[data-ws-map-placement-preview]');
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

async function manual(page: Page, keyboard = false) {
  const button = pane(page).getByRole('button', { name: 'Set up manually' });
  if (keyboard) await activateByTab(page, button);
  else await button.click();
  await expect(pane(page)).toBeHidden();
  const reviewTitle = page.locator('#workspaceCreationReviewTitle');
  await expect(
    (await currentStep(page)) === 4 && (await reviewTitle.isVisible())
      ? reviewTitle
      : page.locator(`#wizardStep${await currentStep(page)}Title`)
  ).toBeFocused();
}

async function assisted(page: Page) {
  await page.locator('#workspaceBuildWithBtn').click();
  const folder = await page.evaluate(() =>
    Boolean((window as any).sessionManager.workspaceBuild?.session?.folder)
  );
  await expect(folder ? page.locator('#workspaceBuildFolderReview') : pane(page)).toBeVisible();
}

// Isolated demo only: model metadata for the real final create, not a paid
// inference dependency. Build turns are still the explicit route fixture below.
let catalogModel: Awaited<ReturnType<typeof startJanitorBuildModel>>;
test.beforeAll(async ({ request }) => {
  catalogModel = await startJanitorBuildModel({ nonBuildReply: { items: [] } });
  // The real creation gate also needs a real build owner. A UI-only hired
  // assistant response cannot supply that server authority.
  await prepareFolderCreationRuntime(request);
});
test.afterAll(async () => {
  await catalogModel?.();
});

test.beforeEach(async ({ page }) => {
  await installLocalCdn(page);
  await page.route('**/api/onboarding/status', route =>
    route.fulfill({ json: { needs_onboarding: false, completed: true } })
  );
  await mockHiredAssistant(page);
});

// Older builds can still hold an observation on Resume. Fresh picks now
// belong to the folder hiring card, exercised against the host below.
async function resumeRetainedFolderReview(page: Page, fake: FakeBuild) {
  await expect(page.locator('#workspaceBuildDraftSaveState')).toHaveText('Draft saved.');
  await page.getByRole('button', { name: 'Close create workspace' }).click();
  await expect(page.locator('#addFolderModal')).toBeHidden();
  fake.session!.folder = { observation: { ...FAKE_OBSERVATION } };
  await page.locator('#cockpitCreateWorkspaceBtn').click();
  await pane(page).getByRole('button', { name: 'Continue draft', exact: true }).click();
  const returned = page.locator('#workspaceBuildReturnToReview');
  const review = page.locator('#workspaceBuildFolderReview');
  await expect(
    returned.filter({ visible: true }).or(review.filter({ visible: true }))
  ).toBeVisible();
  if (!(await review.isVisible())) await returned.click();
}

test('retained folder review is one shared editable surface with optional conversation', async ({
  page
}, testInfo) => {
  await page.setViewportSize({ width: 1280, height: 900 });
  const fake = await fakeBuildServer(page, [
    {
      text: 'Here is a tentative workspace.',
      set: { blank: true, name: 'Research notes', description: 'Draft a research notebook.' },
      team: { mode: 'agentless' }
    },
    {
      text: 'I refined the description. Review before creating.',
      set: { description: 'My refined purpose' },
      createNow: true
    }
  ]);
  let creates = 0;
  page.on('request', request => {
    if (request.method() === 'POST' && new URL(request.url()).pathname === '/api/workspaces')
      creates++;
  });
  await openFromHome(page);
  await say(page, 'Make a research notebook with no agents.');
  await resumeRetainedFolderReview(page, fake);
  const review = page.locator('#workspaceBuildFolderReview');
  await expect(review).toBeVisible();
  await expect(pane(page)).toBeHidden();
  await expect(page.locator('#workspaceBuildDraftSummary')).toBeHidden();
  await expect(page.locator('#workspaceBuildCreationReview')).toBeHidden();
  await expect(page.locator('#workspaceBuildPlanName')).toHaveValue('Research notes');
  await expect(page.locator('#workspaceBuildFolderIdentity')).toContainText('Elite School');
  expect(fake.turns).toHaveLength(1);
  await page.locator('#workspaceBuildPlanName').fill('My edited workspace');
  await page.locator('#workspaceBuildFolderDetails summary').click();
  await page.locator('#workspaceBuildPlanGoal').fill('');
  await expect(page.locator('#folderNameInput')).toHaveValue('My edited workspace');
  await expect.poll(() => fake.session?.draft.name).toBe('My edited workspace');
  await page.getByRole('button', { name: 'Edit team', exact: true }).click();
  await expect(page.locator('#wizardStep3')).toBeVisible();
  await page.locator('#workspaceBuildWithBtn').click();
  await expect(review).toBeVisible();
  await expect(page.locator('#workspaceBuildPlanName')).toHaveValue('My edited workspace');
  await page.getByRole('button', { name: 'Refine with assistant', exact: true }).click();
  await composer(page).fill('Refine my purpose, but leave my name.');
  await pane(page).getByRole('button', { name: 'Add link', exact: true }).click();
  await page.locator('#workspaceBuildLinkInput').fill(FAKE_LINK.url);
  await page.locator('#workspaceBuildLinkInput').press('Enter');
  await expect(page.locator('#workspaceBuildLinkField')).toBeHidden();
  expect(fake.links).toHaveLength(1);
  expect(fake.turns).toHaveLength(1);
  await page.getByRole('button', { name: 'Return to workspace review', exact: true }).click();
  await expect(page.locator('#workspaceBuildFolderLink')).toContainText(FAKE_LINK.title);
  await page.getByRole('button', { name: 'Refine with assistant', exact: true }).click();
  await expect(composer(page)).toHaveValue('Refine my purpose, but leave my name.');
  const sent = page.waitForResponse(response => isTurn(response.url()));
  await composer(page).press('Enter');
  await settle(page, sent);
  expect(fake.turns).toHaveLength(2);
  expect(creates).toBe(0);
  await page.getByRole('button', { name: 'Return to workspace review', exact: true }).click();
  await expect(page.locator('#workspaceBuildPlanName')).toHaveValue('My edited workspace');
  await expect(page.locator('#workspaceBuildPlanDescription')).toHaveValue('My refined purpose');
  expect(fake.session?.folder?.observation.id).toBe(FAKE_OBSERVATION.id);
  expect(
    fake.drafts.some(
      draft => draft.edited_fields?.includes('name') && draft.draft.name === 'My edited workspace'
    )
  ).toBe(true);
  await expect(page.locator('#workspaceBuildPlanReviewSetup')).toBeEnabled();
  await page.locator('#workspaceBuildFolderDetails summary').click();
  await page.screenshot({ path: testInfo.outputPath('folder-review-shared-draft.png') });

  await page.locator('#workspaceBuildPlanName').fill('');
  await page.locator('#workspaceBuildFolderDetails summary').click();
  await page.locator('#workspaceBuildPlanDescription').fill('');
  await page.getByRole('button', { name: 'Change purpose', exact: true }).click();
  await cardByLabel(page, 'Code Project').click();
  await page.locator('#workspaceBuildWithBtn').click();
  await expect(page.locator('#workspaceBuildFolderPurpose')).toContainText('Code Project');
  await expect(page.locator('#workspaceBuildPlanName')).toHaveValue('');
  await expect(page.locator('#workspaceBuildPlanDescription')).toHaveValue('');
  expect(fake.turns).toHaveLength(2);
});

test('long effective team wraps in the one narrow folder-review scroller', async ({
  page
}, testInfo) => {
  await page.addInitScript(() => localStorage.setItem('ori-theme', 'dark'));
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await page.setViewportSize({ width: 640, height: 450 });
  const names = [
    'Editorial planning and source verification lead for the synthetic project',
    'Publication copywriting and accessibility reviewer for the synthetic project'
  ];
  const fake = await fakeBuildServer(page, [
    {
      text: 'A draft with the requested team.',
      set: {
        template_id: 'content-production',
        name: 'Long team review',
        description: 'Review an editable team.'
      },
      team: {
        mode: 'staffed',
        roles: [
          { role_id: 'content-lead', mode: 'create', agent_name: names[0] },
          { role_id: 'brand-copywriter', mode: 'create', agent_name: names[1] }
        ]
      }
    }
  ]);
  await openFromHome(page);
  await say(page, 'Draft a content workspace with this team.');
  await resumeRetainedFolderReview(page, fake);
  const team = page.locator('#workspaceBuildFolderTeam');
  for (const name of names) await expect(team).toContainText(name);
  await page.getByRole('button', { name: 'Edit team', exact: true }).focus();
  const layout = await team.evaluate(node => {
    const body = node.closest('.workspace-build-folder-review__body')!;
    const box = document.activeElement!.getBoundingClientRect();
    const area = body.getBoundingClientRect();
    return {
      overflow: body.scrollWidth > body.clientWidth + 1,
      top: box.top,
      bottom: box.bottom,
      areaTop: area.top,
      areaBottom: area.bottom,
      nested: /scroll|auto/.test(getComputedStyle(node).overflowY)
    };
  });
  expect(layout.overflow).toBe(false);
  expect(layout.nested).toBe(false);
  expect(layout.top).toBeGreaterThanOrEqual(layout.areaTop);
  expect(layout.bottom).toBeLessThanOrEqual(layout.areaBottom);
  await expect(page.locator('#workspaceBuildPlanReviewSetup')).toBeInViewport();
  await page.screenshot({ path: testInfo.outputPath('folder-long-team-dark-640.png') });
});

test('slow first proposal exposes the applied draft, not a claim of creation', async ({
  page
}, testInfo) => {
  const wait = gate();
  const fake = await fakeBuildServer(
    page,
    [
      {
        text: 'I already created Newsletter Desk.',
        set: {
          template_id: 'content-production',
          name: 'Newsletter Desk',
          description: 'Turn research notes into a weekly newsletter.'
        },
        choices: ['Just a Content Lead'],
        createNow: true
      }
    ],
    { turnGate: () => wait.promise }
  );
  const material = async () => {
    const folders = (await (await page.request.get('/api/workspaces')).json()).folders;
    const agents = (await (await page.request.get('/api/agents')).json()).agents;
    return {
      workspaces: folders.map((w: RequestBody) => w.id).sort(),
      agents: agents.map((a: RequestBody) => a.name).sort(),
      runs: await Promise.all(
        folders.map(async ({ id }: RequestBody) => {
          const response = await page.request.get(`/api/workspaces/${id}/runs`);
          expect(response.ok()).toBe(true);
          return response.json();
        })
      )
    };
  };
  const before = await material();
  const writes: string[] = [];
  page.on('request', request => {
    const path = new URL(request.url()).pathname;
    if (
      request.method() === 'POST' &&
      /\/api\/workspaces(?:$|\/create-operations|\/[^/]+\/runs)/.test(path)
    )
      writes.push(path);
  });
  await openFromHome(page);
  const preview = page.locator('#workspaceBuildDraftSummary');
  await expect(preview).toContainText('Undecided');
  await composer(page).fill('Set up a workspace for my weekly newsletter.');
  const sent = page.waitForRequest(request => isTurn(request.url()));
  await pane(page).getByRole('button', { name: 'Send', exact: true }).click();
  try {
    await sent;
    await expect(preview).toContainText('Preparing a draft…');
    await expect(pane(page)).not.toContainText('Setting up…');
    await page.screenshot({ path: testInfo.outputPath('first-proposal-wait.png') });
  } finally {
    wait.release();
  }
  await expect(composer(page)).toBeFocused();
  await expect(preview).toContainText('Draft · Nothing created yet');
  await expect(preview).toContainText('Newsletter Desk');
  await expect(preview).toContainText('Content Production');
  await expect(preview).toContainText('Turn research notes into a weekly newsletter.');
  await expect(preview).toContainText('Next: answer the assistant’s question.');
  await expect(pane(page)).toContainText('I already created Newsletter Desk.');
  await expect(page.locator('#workspaceCreationReviewPanel')).toBeHidden();
  expect(fake.turns).toHaveLength(1);
  expect(writes).toEqual([]);
  expect(await material()).toEqual(before);
  await page.screenshot({ path: testInfo.outputPath('first-proposal.png') });
});

test('a fast partial first proposal leaves missing choices undecided', async ({ page }) => {
  const fake = await fakeBuildServer(page, [
    { text: 'What should go in it?', set: { name: 'Notebook' }, choices: ['Research', 'Recipes'] }
  ]);
  await openFromHome(page);
  await say(page, 'A place for notes');
  const preview = page.locator('#workspaceBuildDraftSummary');
  await expect(preview).toContainText('Notebook');
  await expect(page.locator('#workspaceBuildDraftBlueprint')).toHaveText('Undecided');
  await expect(page.locator('#workspaceBuildDraftPurpose')).toHaveText('Undecided');
  await expect.poll(() => fake.session?.draft.name).toBe('Notebook');
  expect(fake.session?.draft.blank).toBeUndefined();
});

const NEWSLETTER_PROMPT = 'Set up a workspace for my weekly newsletter.';
const NEWSLETTER_PURPOSE = 'Turn research notes into a weekly newsletter.';
const NEWSLETTER_REASON = 'Content Production fits a weekly editorial workflow.';
const NEWSLETTER_SOLO = 'Just a Content Lead';
const NEWSLETTER_PAIR = 'A Content Lead and a Brand Copywriter';

async function openRealNewsletterBuild(page: Page, name: string, keyboard = false) {
  await page.unroute(/\/api\/personal-assistant$/);
  await abandonOpenBuild(page.request);
  catalogModel.scenario({
    reply: {
      say: `I picked Content Production and named it ${name}.`,
      ask: {
        question: 'Who should staff it?',
        choices: [{ label: NEWSLETTER_PAIR }, { label: NEWSLETTER_SOLO }]
      },
      patch: {
        set: ['blueprint_id', 'name', 'description'],
        blueprint_id: 'content-production',
        name,
        description: NEWSLETTER_PURPOSE
      },
      why: [{ section: 'blueprint', text: NEWSLETTER_REASON }],
      ready: true,
      create_now: true // Even these flags cannot confirm the first turn.
    }
  });
  await openFromHome(
    page,
    keyboard ? () => activateByTab(page, page.locator('#cockpitCreateWorkspaceBtn')) : undefined
  );
}

function staffRealNewsletter(roles: { role_id: string; mode?: string; agent_name: string }[]) {
  catalogModel.scenario({
    reply: {
      say: 'Review the proposed team, or say “create it”.',
      patch: {
        set: ['team'],
        team: { mode: 'staffed', roles: roles.map(role => ({ mode: 'create', ...role })) }
      },
      ready: true
    }
  });
}

function proposeRealNewsletterReview() {
  catalogModel.scenario({
    reply: { say: 'Ready for you to review.', create_now: true, ready: true }
  });
}

function observeCreationTraffic(page: Page) {
  const reviews: RequestBody[] = [];
  const confirmations: { path: string; body: RequestBody }[] = [];
  const observations: string[] = [];
  const legacyWrites: string[] = [];
  page.on('request', request => {
    const path = new URL(request.url()).pathname;
    if (request.method() === 'GET' && /\/create-operations\/[^/]+$/.test(path))
      observations.push(path);
    if (request.method() !== 'POST') return;
    if (path === '/api/workspaces/create-operations') reviews.push(request.postDataJSON());
    else if (/\/create-operations\/[^/]+\/confirm$/.test(path))
      confirmations.push({ path, body: request.postDataJSON() });
    else if (/^\/api\/workspaces(?:$|\/[^/]+\/(?:runs|mission\/first-open)$)/.test(path))
      legacyWrites.push(path);
  });
  return { reviews, confirmations, observations, legacyWrites };
}

async function domainResources(page: Page) {
  const get = async (endpoint: string) => {
    const response = await page.request.get(endpoint);
    expect(response.ok(), `${endpoint} should be readable`).toBe(true);
    return response.json();
  };
  return {
    workspaces: (await get('/api/workspaces')).folders.map((item: RequestBody) => item.id).sort(),
    agents: (await get('/api/agents')).agents.map((item: RequestBody) => item.name).sort()
  };
}

test('real newsletter review and map bind only the latest material draft to confirmation', async ({
  page
}, testInfo) => {
  const firstName = 'Newsletter Desk';
  const updatedName = 'Friday Newsletter Desk';
  const lead = 'Friday Content Lead';
  const traffic = observeCreationTraffic(page);
  await page.addInitScript(() => localStorage.setItem('ori-theme', 'light'));
  await openRealNewsletterBuild(page, firstName);
  const before = await domainResources(page);
  const summary = page.locator('#workspaceBuildDraftSummary');
  const review = page.locator('#workspaceCreationReviewPanel');
  const held = catalogModel.hold();
  await composer(page).fill(NEWSLETTER_PROMPT);
  const proposed = page.waitForResponse(response => isTurn(response.url()));
  await composer(page).press('Enter');
  try {
    await held.started;
    await expect(summary).toContainText('Preparing a draft…');
    await expect(summary).toContainText('Nothing created yet');
    await page.screenshot({ path: testInfo.outputPath('desktop-waiting.png') });
    expect(await domainResources(page)).toEqual(before);
  } finally {
    held.release();
  }
  await settle(page, proposed);
  await expect(summary).toContainText(NEWSLETTER_REASON);
  await expect(summary).toContainText('Standalone workspace');
  await expect(summary).toContainText('Content Lead: Required · choose an agent');
  await expect(summary).toContainText('Next: answer the assistant’s question.');
  await expect(review).toBeHidden();
  await page.screenshot({ path: testInfo.outputPath('desktop-proposal.png') });
  staffRealNewsletter([{ role_id: 'content-lead', agent_name: lead }]);
  await choose(page, NEWSLETTER_SOLO);
  await expect(summary).toContainText(`Will create ${lead} · required`);
  await expect(summary).toContainText('3 optional roles left empty.');
  await expect(review).toBeHidden();
  await page.screenshot({ path: testInfo.outputPath('desktop-team.png') });
  await test.step('reach review without confirming', async () => {
    proposeRealNewsletterReview();
    await say(page, 'create it');
    await expect(summary).toBeHidden();
    await expect(review).toContainText(`Review ${firstName}`);
    await expect(review).toContainText('Review · Nothing created yet');
    await expect(review).toContainText(NEWSLETTER_PURPOSE);
    await expect(review).toContainText('Standalone workspace');
    await expect(review).toContainText(`Will create ${lead} · required`);
    await expect(page.locator('#createFolderBtn')).toBeHidden();
    expect(traffic.reviews).toHaveLength(0);
    expect(traffic.confirmations).toHaveLength(0);
    await review.getByRole('button', { name: 'Use automatic placement' }).click();
    await expect(
      review.getByRole('button', { name: 'Create workspace', exact: true })
    ).toBeEnabled();
    expect(await domainResources(page)).toEqual(before);
    expect(traffic.confirmations).toHaveLength(0);
  });
  const place = review.getByRole('button', { name: 'Place & create on map', exact: true });
  await place.click();
  const mapDraft = page.locator('[data-ws-map-placement-preview]');
  await expect(mapDraft).toBeVisible();
  expect(traffic.reviews).toHaveLength(1);
  expect(await domainResources(page)).toEqual(before);
  await page.locator('[data-ws-map-viewport]').focus();
  await page.keyboard.press('Escape');
  await expect(page.locator('#workspaceCreationReviewTitle')).toBeFocused();
  await expect(mapDraft).toBeHidden();
  expect(traffic.confirmations).toHaveLength(0);
  expect(await domainResources(page)).toEqual(before);
  await page.screenshot({ path: testInfo.outputPath('review-returned.png') });

  const name = review.getByLabel('Workspace name', { exact: true });
  await name.fill(updatedName);
  await name.press('Tab');
  await expect(review).toContainText(`Review ${updatedName}`);
  await page.screenshot({ path: testInfo.outputPath('review-edited.png') });
  await place.click();
  await expect(mapDraft).toBeVisible();
  expect(traffic.reviews).toHaveLength(2);
  const material = traffic.reviews[1];
  expect(material.id).not.toBe(traffic.reviews[0].id);
  expect(material.request).toMatchObject({
    name: updatedName,
    template_id: 'content-production',
    description: NEWSLETTER_PURPOSE,
    build_session_id: expect.any(String),
    build_review_version: expect.any(Number),
    role_staffing: [{ role_id: 'content-lead', mode: 'create', name: lead }]
  });
  expect(material.request.parent_id || '').toBe('');
  const operationPath = `/api/workspaces/create-operations/${material.id}`;
  const reviewed = await (await page.request.get(operationPath)).json();
  const retired = await (
    await page.request.get(`/api/workspaces/create-operations/${traffic.reviews[0].id}`)
  ).json();
  expect(reviewed.operation.state).toBe('reviewed');
  expect(retired.operation.state).toBe('abandoned');
  expect(await domainResources(page)).toEqual(before);
  await confirmMapPosition(page);
  const result = page.getByRole('region', { name: 'Workspace setup status' });
  await expect(result).toContainText(updatedName);
  expect(traffic.legacyWrites).toEqual([]);
  expect(traffic.confirmations).toEqual([
    { path: `${operationPath}/confirm`, body: { review_digest: reviewed.operation.review_digest } }
  ]);
  const confirmed = await (await page.request.get(operationPath)).json();
  const id = confirmed.result.folder.id;
  const after = await domainResources(page);
  expect(after.workspaces.filter((item: string) => !before.workspaces.includes(item))).toEqual([
    id
  ]);
  expect(after.agents.filter((item: string) => !before.agents.includes(item))).toEqual([lead]);
  await expect(result).toContainText(`${updatedName} created`);
  await expect(result).toContainText(`Assigned team: ${lead}`);
  await expect(result).toContainText('Map position saved');
  await expect(result).toContainText('2 starter tickets saved for later');
  await page.screenshot({ path: testInfo.outputPath('review-created.png') });
  await result.getByRole('button', { name: `Open ${updatedName}`, exact: true }).click();
  await expectSavedNewsletter(page, confirmed.result.folder, [lead]);
  expect(traffic.legacyWrites).toEqual([]);
  await page.screenshot({ path: testInfo.outputPath('desktop-open.png') });
});

async function expectSavedNewsletter(page: Page, folder: RequestBody, roster: string[]) {
  await expect(page).toHaveURL(new RegExp(`/workspaces/${folder.folder_slug}$`));
  const detail = await (await page.request.get(`/api/workspaces/${folder.id}`)).json();
  const team = await (await page.request.get(`/api/workspaces/${folder.id}/agents`)).json();
  const response = await page.request.get(`/api/workspaces/${folder.id}/runs`);
  expect(response.ok()).toBe(true);
  const runs = await response.json();
  expect('runs' in runs).toBe(true);
  expect(detail.name).toBe(folder.name);
  expect(detail.mission_state.mission_execution_count || 0).toBe(0);
  expect(team.agents.map((agent: RequestBody) => agent.name).sort()).toEqual([...roster].sort());
  expect(runs.runs || []).toEqual([]);
  const mission = await (await page.request.get(`/api/workspaces/${folder.id}/mission`)).json();
  expect(mission.first_run_pending).not.toBe(true);
  const provenance = page.locator('.ws-cmd-build-summary');
  await activateByTab(page, provenance.locator('summary'));
  await expect(provenance).toContainText(NEWSLETTER_PROMPT);
  await expect(provenance).toContainText(NEWSLETTER_REASON);
  for (const name of roster) await expect(provenance).toContainText(name);
}

for (const interrupted of [false, true]) {
  test(`real compact newsletter: ${interrupted ? 'double activation and read-only recovery' : 'keyboard-only confirmation'}`, async ({
    page
  }, testInfo) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await page.addInitScript(() => localStorage.setItem('ori-theme', 'dark'));
    await page.emulateMedia({ reducedMotion: 'reduce' });
    const day = interrupted ? 'Sunday' : 'Saturday';
    const firstName = `Weekend Newsletter Desk (${day})`;
    const name = `${day} Newsletter Desk`;
    const lead = `Saved ${day} Editor`;
    const copywriter = `${day} Copywriter`;
    const saved = await page.request.post('/api/agents', {
      data: {
        name: lead,
        provider: 'lmstudio',
        model: 'ori-synthetic-model',
        system_prompt: 'Synthetic publishing lead.'
      }
    });
    expect(saved.ok(), await saved.text()).toBe(true);
    const traffic = observeCreationTraffic(page);
    await openRealNewsletterBuild(page, firstName, true);
    const before = await domainResources(page);
    await say(page, NEWSLETTER_PROMPT);
    const choicesVisible = await pane(page)
      .getByRole('button', { name: NEWSLETTER_PAIR })
      .evaluate(pair => {
        const scroller = pair.closest('.workspace-build-pane__transcript')!.getBoundingClientRect();
        const choices = Array.from(pair.parentElement!.querySelectorAll('button'));
        return choices.every(button => {
          const rect = button.getBoundingClientRect();
          return rect.top >= scroller.top - 1 && rect.bottom <= scroller.bottom + 1;
        });
      });
    expect(choicesVisible, 'both staffing choices fit in the visible transcript').toBe(true);
    await page.screenshot({ path: testInfo.outputPath('compact-proposal.png') });
    staffRealNewsletter([
      { role_id: 'content-lead', agent_name: lead, mode: 'assign' },
      { role_id: 'brand-copywriter', agent_name: copywriter }
    ]);
    await choose(page, NEWSLETTER_PAIR, true);
    const summary = page.locator('#workspaceBuildDraftSummary');
    await expect(summary).toContainText(`Will assign ${lead} · required`);
    await expect(summary).toContainText(`Will create ${copywriter}`);
    await expect(summary).toContainText('Draft · Nothing created yet');
    const layout = await summary.evaluate(root => ({
      overflow: root.scrollWidth - root.clientWidth,
      bottom: root.getBoundingClientRect().bottom,
      height: innerHeight,
      animation: getComputedStyle(root).animationName
    }));
    expect(layout.overflow).toBeLessThanOrEqual(1);
    expect(layout.bottom).toBeLessThanOrEqual(layout.height);
    expect(layout.animation).toBe('none');
    // An inert re-render cannot re-announce the same applied changes or move focus.
    await expect(composer(page)).toBeFocused();
    const repeated = await summary.evaluate(root => {
      const observer = new MutationObserver(() => {});
      observer.observe(root.querySelector('[aria-live="polite"]')!, {
        subtree: true,
        childList: true,
        characterData: true
      });
      (window as any).sessionManager.syncWorkspaceBuildPresentation();
      const count = observer.takeRecords().length;
      observer.disconnect();
      return count;
    });
    expect(repeated).toBe(0);
    await expect(composer(page)).toBeFocused();
    await page.screenshot({ path: testInfo.outputPath('compact-team.png') });

    await manual(page, true);
    const review = page.locator('#workspaceCreationReviewPanel');
    await activateByTab(
      page,
      review.getByRole('button', { name: 'Edit details & advanced settings' })
    );
    await tabTo(page, page.locator('#folderNameInput'));
    await page.keyboard.press('ControlOrMeta+a');
    await page.keyboard.insertText(name);
    await activateByTab(page, page.locator('#workspaceBuildWithBtn'));
    await expect(summary).toContainText('You edited: name');
    await expect(summary).toContainText('Draft saved.');
    await activateByTab(page, page.getByRole('button', { name: 'Close create workspace' }));
    await expect(pane(page)).toBeHidden();
    await activateByTab(page, page.locator('#cockpitCreateWorkspaceBtn'));
    await activateByTab(
      page,
      pane(page).getByRole('button', { name: 'Continue draft', exact: true })
    );
    await expect(composer(page)).toBeFocused();
    await expect(summary).toContainText(name);
    await expect(summary).toContainText(`Will assign ${lead}`);
    await expect(summary).toContainText(NEWSLETTER_REASON);
    await expect(page.locator('#workspaceBuildDraftChange')).toBeHidden();
    await page.screenshot({ path: testInfo.outputPath('compact-resumed.png') });

    proposeRealNewsletterReview();
    await say(page, 'create it');
    await expect(summary).toBeHidden();
    await expect(review).toContainText(`Review ${name}`);
    await activateByTab(page, review.getByRole('button', { name: 'Use automatic placement' }));
    const confirm = review.getByRole('button', { name: 'Create workspace', exact: true });
    await expect(confirm).toBeEnabled();
    expect(await domainResources(page)).toEqual(before);
    expect(traffic.confirmations).toEqual([]);
    await page.screenshot({ path: testInfo.outputPath('compact-review.png') });
    if (!interrupted) {
      await activateByTab(page, confirm);
      await expect(review).toContainText(`${name} created`);
      expect(traffic.confirmations).toHaveLength(1);
      expect(traffic.reviews).toHaveLength(1);
      const verified = await (
        await page.request.get(`/api/workspaces/create-operations/${traffic.reviews[0].id}`)
      ).json();
      await activateByTab(page, review.getByRole('button', { name: `Open ${name}`, exact: true }));
      await expectSavedNewsletter(page, verified.result.folder, [lead, copywriter]);
      expect(traffic.legacyWrites).toEqual([]);
      return;
    }
    let unreadable = true;
    await page.route(/\/api\/workspaces\/create-operations\/[^/]+$/, route =>
      unreadable && route.request().method() === 'GET'
        ? route.fulfill({ contentType: 'application/json', body: '{' })
        : route.continue()
    );
    const lost = gate();
    await page.route('**/api/workspaces/create-operations/*/confirm', async route => {
      try {
        const response = await route.fetch(); // The real creation commits.
        expect(response.status()).toBe(201);
      } finally {
        lost.release();
        await route.abort('failed'); // Only the response is lost.
      }
    });
    await confirm.dblclick();
    await lost.promise;
    await expect(
      review.getByRole('heading', { name: 'Workspace status unavailable' })
    ).toBeVisible();
    await expect(review.getByRole('button', { name: 'Check status' })).toBeVisible();
    await expect(page.locator('#workspaceBuildWithBtn')).toBeHidden();
    await expect(review.getByRole('button', { name: `Open ${name}` })).toHaveCount(0);
    await page.screenshot({ path: testInfo.outputPath('compact-uncertain.png') });
    expect(traffic.reviews).toHaveLength(1);
    expect(traffic.reviews[0].request).toMatchObject({
      name,
      template_id: 'content-production',
      role_staffing: [
        { role_id: 'content-lead', mode: 'assign', name: lead },
        { role_id: 'brand-copywriter', mode: 'create', name: copywriter }
      ]
    });
    expect(traffic.confirmations).toHaveLength(1);
    const operationPath = `/api/workspaces/create-operations/${traffic.reviews[0].id}`;
    const committed = await (await page.request.get(operationPath)).json();
    expect(committed.operation.state).toBe('complete');
    expect(traffic.confirmations[0]).toEqual({
      path: `${operationPath}/confirm`,
      body: { review_digest: committed.operation.review_digest }
    });
    const after = await domainResources(page);
    expect(after.workspaces.filter((id: string) => !before.workspaces.includes(id))).toEqual([
      committed.result.folder.id
    ]);
    expect(after.agents.filter((agent: string) => !before.agents.includes(agent))).toEqual([
      copywriter
    ]);

    const calls = catalogModel.calls();
    await page.reload();
    await activateByTab(page, page.locator('#cockpitCreateWorkspaceBtn'));
    await expect(review.getByRole('button', { name: 'Check status' })).toBeVisible();
    await expect(pane(page)).toBeHidden();
    await expect(page.locator('#createFolderBtn')).toBeHidden();
    await expect(review).not.toContainText(`${name} created`);
    unreadable = false;
    await activateByTab(page, review.getByRole('button', { name: 'Check status' }));
    await expect(review).toContainText(`${name} created`);
    await expect(review).toContainText('Standalone workspace');
    await expect(review).toContainText(lead);
    await expect(review).toContainText(copywriter);
    expect(catalogModel.calls()).toBe(calls);
    expect(traffic.observations.length).toBeGreaterThanOrEqual(2);
    expect([...new Set(traffic.observations)]).toEqual([operationPath]);
    expect(traffic.confirmations).toHaveLength(1);
    expect(traffic.reviews).toHaveLength(1);
    expect(await domainResources(page)).toEqual(after);
    await page.screenshot({ path: testInfo.outputPath('compact-recovered.png') });
    await activateByTab(page, review.getByRole('button', { name: `Open ${name}`, exact: true }));
    await expectSavedNewsletter(page, committed.result.folder, [lead, copywriter]);
    expect(traffic.legacyWrites).toEqual([]);
    await page.screenshot({ path: testInfo.outputPath('compact-open.png') });
  });
}

test('three scripted turns reach review with the effective pair and no creation', async ({
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
      text: 'I already assigned a Content Editor. Check the review, or say “create it”.',
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
  const traffic = observeCreationTraffic(page);
  const teamPlan = gate();
  await page.route('**/api/workspaces/template-agent-plan', async route => {
    if (route.request().postDataJSON()?.template_id === 'content-production')
      await teamPlan.promise;
    await route.continue();
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

  const draftTeam = page.locator('#workspaceBuildDraftTeam');
  try {
    await expect(draftTeam).toHaveText('Checking team options…');
  } finally {
    teamPlan.release();
  }
  await expect(draftTeam).toContainText('Content Lead: Required · choose an agent');
  await choose(page, 'A Content Lead and a Brand Copywriter');
  await expect(draftTeam).toContainText('Content Lead · required');
  await expect(draftTeam).toContainText('Brand Copywriter');
  await expect(draftTeam).not.toContainText('Content Editor');
  await expect(draftTeam).toContainText('2 optional roles left empty.');

  await say(page, 'create it');
  const review = page.locator('#workspaceCreationReviewPanel');
  await expect(review).toContainText(`Review ${name}`);
  await expect(review).toContainText('Will create Content Lead');
  await expect(review).toContainText('Will create Brand Copywriter');
  expect(fake.turns).toHaveLength(3);
  expect(traffic.reviews).toEqual([]);
  expect(traffic.confirmations).toEqual([]);
  expect(traffic.legacyWrites).toEqual([]);
});

test('a folder pick keeps the existing hire review and no ordinary draft preview', async ({
  page
}, testInfo) => {
  // Seed only the launched demo's recognized project, never an arbitrary root.
  const configured = process.env.ORI_BUILD_DEMO_SANDBOX;
  test.skip(
    !configured,
    'Set ORI_BUILD_DEMO_SANDBOX to the printed wt demo sandbox for the real folder check.'
  );
  const sandbox = realpathSync(configured!);
  expect(relative(realpathSync(tmpdir()), sandbox)).toMatch(/^ori-demo\.[^/]+$/);
  const response = await page.request.get('/api/settings/workspace-root');
  expect(response.ok()).toBe(true);
  const workspaceRoot = await response.json();
  // An unconfirmed demo uses its sandbox's staging directory until setup.
  const root = workspaceRoot.effective_workspace_root || workspaceRoot.default_workspace_root;
  expect(relative(sandbox, root)).not.toMatch(/^\.\.(?:\/|$)/);
  const project = mkdtempSync(join(sandbox, 'Synthetic Widget '));
  writeFileSync(join(project, 'go.mod'), 'module example.invalid/widget\n', { mode: 0o600 });
  writeFileSync(join(project, 'main.go'), 'package main\n\nfunc main() {}\n', { mode: 0o600 });

  await page.unroute(/\/api\/personal-assistant$/);
  await abandonOpenBuild(page.request);
  const before = await domainResources(page);
  const calls = catalogModel.calls();
  await openFromHome(page);
  const add = pane(page).getByRole('button', { name: 'Add folder', exact: true });
  await add.click();
  const chooser = page.locator('#workspaceBuildFolderChooser');
  await expect(chooser).toContainText('File contents are not read');
  await page.keyboard.press('Escape');
  await expect(chooser).toBeHidden();
  await expect(add).toBeFocused();
  await add.click();
  await chooser.getByPlaceholder('~/Projects/my-app').fill(project);
  await chooser.getByRole('button', { name: 'Use this path', exact: true }).click();
  const card = page.locator('#workspaceBuildFolderAgent');
  await expect(card).toContainText('Synthetic Widget');
  await expect(card).toContainText('Nothing is built until you hire.');
  await expect(card.getByRole('button', { name: 'Hire & build', exact: true })).toBeVisible();
  await expect(page.locator('#workspaceBuildFolderReview')).toBeHidden();
  await expect(page.locator('#workspaceBuildDraftSummary')).toBeHidden();
  await expect(pane(page)).toBeHidden();
  expect(await domainResources(page)).toEqual(before);
  expect(catalogModel.calls()).toBe(calls);
  await page.screenshot({ path: testInfo.outputPath('recognized-folder-review.png') });
});

test('a link attached in the pane shows on the chip, the draft line, the turn, the goal, and Review', async ({
  page
}) => {
  const goal = 'Maintain a cited synthesis of Attention Is All You Need.';
  const fake = await fakeBuildServer(page, [
    {
      text: 'This is a paper. I picked Research Project and named it Attention Notes.',
      set: {
        template_id: 'research-project',
        name: 'Attention Notes',
        description: 'Reads and summarizes the attention paper.',
        mission: goal
      }
    }
  ]);
  await openFromHome(page);
  await expect(pane(page)).toBeVisible();
  const addLink = pane(page).getByRole('button', { name: 'Add link' });
  const field = page.locator('#workspaceBuildLinkField');
  const input = page.locator('#workspaceBuildLinkInput');
  await expect(page.locator('#workspaceBuildLink')).toBeHidden();

  await addLink.click();
  await expect(field).toBeVisible();
  await expect(field).toContainText('title and description once');
  await expect(input).toBeFocused();
  // Escape closes only the field: the dialog stays, and focus returns to Add link.
  await page.keyboard.press('Escape');
  await expect(field).toBeHidden();
  await expect(page.locator('#addFolderModal')).toBeVisible();
  await expect(addLink).toBeFocused();

  // A refused address is said under the field, which keeps what was typed.
  await addLink.click();
  await input.fill('http://127.0.0.1:8931/');
  await input.press('Enter');
  await expect(page.locator('#workspaceBuildLinkStatus')).toContainText(
    'Only public http or https links'
  );
  await expect(input).toHaveValue('http://127.0.0.1:8931/');
  await expect(page.locator('#workspaceBuildLink')).toBeHidden();

  const attached = page.waitForResponse(
    response =>
      /\/api\/workspaces\/build-sessions\/[^/]+\/link$/.test(new URL(response.url()).pathname) &&
      response.status() === 200
  );
  await input.fill('https://arxiv.org/abs/1706.03762');
  await page.locator('#workspaceBuildLinkField').getByRole('button', { name: 'Attach' }).click();
  await attached;
  await expect(field).toBeHidden();
  await expect(page.locator('#workspaceBuildLinkName')).toHaveText('Attention Is All You Need');
  await expect(page.locator('#workspaceBuildLinkSummary')).toHaveText('Page · arxiv.org');
  await expect(page.locator('#workspaceBuildDraftText')).toContainText(
    'Link: Attention Is All You Need'
  );
  await expect(pane(page)).toContainText('Link attached');
  expect(fake.links.map(body => body.url)).toEqual([
    'http://127.0.0.1:8931/',
    'https://arxiv.org/abs/1706.03762'
  ]);

  // An empty Send asks for the link itself; the reply fills the form and its goal.
  await expect(composer(page)).toBeEnabled();
  const turn = page.waitForResponse(response => isTurn(response.url()));
  await composer(page).press('Enter');
  await settle(page, turn);
  expect(fake.turns.at(-1)?.text).toBe('Set it up from the attached link.');
  await expect(page.locator('#folderNameInput')).toHaveValue('Attention Notes');
  await expect(page.locator('#folderGoalInput')).toHaveValue(goal);

  // Review says what Create will do with the link.
  await expect(page.locator('[data-build-link-receipt]')).toHaveCount(1);
  await expect(page.locator('[data-build-link-receipt]')).toContainText(
    'Attention Is All You Need · Page'
  );
  await expect(page.locator('[data-build-link-receipt]')).toContainText('Sources note');

  const detached = page.waitForResponse(response =>
    /\/link\/detach$/.test(new URL(response.url()).pathname)
  );
  await page
    .locator('#workspaceBuildLink')
    .getByRole('button', { name: /Remove link/ })
    .click();
  await detached;
  await expect(page.locator('#workspaceBuildLink')).toBeHidden();
  await expect(page.locator('#workspaceBuildDraftText')).toBeHidden();
  await expect(page.locator('#workspaceBuildDraftPreview')).toBeVisible();
  await expect(page.locator('#workspaceBuildDraftPreview')).not.toContainText(
    'Attention Is All You Need'
  );
  await expect(page.locator('[data-build-link-receipt]')).toHaveCount(0);
});

test('a blueprint switched on the form is said back and the assistant re-staffs', async ({
  page
}) => {
  const name = `Field Notes ${Date.now().toString(36)}`;
  const fake = await fakeBuildServer(page, [
    {
      text: `I picked Content Production and named it ${name}.`,
      set: { template_id: 'content-production', name, description: 'Weekly field notes.' },
      why: [
        {
          section: 'blueprint',
          blueprint_id: 'content-production',
          text: 'A repeatable editorial workflow.'
        }
      ]
    },
    { text: 'You switched to Research Project — I’ll staff it for research instead.' }
  ]);

  await openFromHome(page);
  await say(page, 'A place for my weekly field notes');
  await expect(page.locator('#folderNameInput')).toHaveValue(name);
  await expect(page.locator('#workspaceBuildDraftWhy')).toContainText(
    'A repeatable editorial workflow.'
  );

  // The user deliberately reveals the shared form, then picks another card.
  await manual(page);
  while ((await currentStep(page)) > 1) await page.locator('#wizardBackBtn').click();
  await expect(cardByLabel(page, 'Content Production')).toContainText('Chosen by Atlas');
  const autoTurn = page.waitForRequest(
    request => request.method() === 'POST' && isTurn(request.url())
  );
  await cardByLabel(page, 'Research Project').click();
  await expect
    .poll(() => fake.drafts.some(draft => draft.draft?.template_id === 'research-project'))
    .toBe(true);
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
  await expect(page.locator('#workspaceBuildDraftBlueprint')).toHaveText('Research Project');
  await expect(page.locator('#workspaceBuildDraftWhy')).toBeHidden();
});

test('clearing a proposed role updates the draft and survives Resume', async ({ page }) => {
  const fake = await fakeBuildServer(page, [
    {
      text: 'A proposed editorial team.',
      set: {
        template_id: 'content-production',
        name: 'Clearable Desk',
        description: 'Plan the newsletter.'
      },
      team: {
        mode: 'staffed',
        roles: [{ role_id: 'content-lead', mode: 'create', agent_name: 'Proposed Lead' }]
      }
    }
  ]);
  await openFromHome(page);
  await say(page, 'An editorial workspace with a lead');
  const team = page.locator('#workspaceBuildDraftTeam');
  await expect(team).toContainText('Will create Proposed Lead');
  await manual(page);
  if ((await currentStep(page)) === 4)
    await page
      .locator('#workspaceCreationReviewPanel')
      .getByRole('button', { name: 'Edit team', exact: true })
      .first()
      .click();
  await page
    .locator('#workspaceRoleRoster')
    .getByRole('button', { name: 'Clear Content Lead', exact: true })
    .click();
  await assisted(page);
  await expect(team).toContainText('Content Lead: Required · choose an agent');
  await expect(team).not.toContainText('Proposed Lead');
  await expect(page.locator('#workspaceBuildDraftSaveState')).toHaveText('Draft saved.');
  expect(fake.session?.draft.role_staffing).toEqual([]);
  await page.getByRole('button', { name: 'Close create workspace' }).click();
  await expect(pane(page)).toBeHidden();
  await page.locator('#cockpitCreateWorkspaceBtn').click();
  await pane(page).getByRole('button', { name: 'Continue draft', exact: true }).click();
  await expect(team).toContainText('Content Lead: Required · choose an agent');
  await expect(team).not.toContainText('Proposed Lead');
  await expect(page.locator('#workspaceBuildDraftChange')).toBeHidden();
});

test('reopening an untouched assistant build skips the draft prompt', async ({ page }) => {
  await page.unroute(/\/api\/personal-assistant$/);
  await abandonOpenBuild(page.request);
  const openBuild = () =>
    page.waitForResponse(
      r => r.request().method() === 'POST' && r.url().endsWith('/api/workspaces/build-sessions')
    );
  const first = openBuild();
  const defaultsSaved = page.waitForResponse(
    r => r.request().method() === 'PATCH' && /\/build-sessions\/[^/]+\/draft$/.test(r.url())
  );
  await openFromHome(page);
  const { session } = await (await first).json();
  expect((await defaultsSaved).ok()).toBe(true);
  const calls = catalogModel.totalCalls();
  await page.getByRole('button', { name: 'Close create workspace' }).click();
  await expect(page.locator('#addFolderModal')).toBeHidden();
  const again = openBuild();
  await page.locator('#cockpitCreateWorkspaceBtn').click();
  const reopened = await (await again).json();
  expect(reopened.session.id).toBe(session.id);
  expect(reopened.resumed).not.toBe(true);
  await expect(composer(page)).toBeEnabled();
  await expect(pane(page).getByRole('button', { name: 'Continue draft' })).toHaveCount(0);
  expect(catalogModel.totalCalls()).toBe(calls);
});

test('closing mid-build and reopening offers Continue draft, which restores the form and the step', async ({
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
  await expect(pane(page)).toContainText(`Continue the draft for “${name}”?`);
  expect(fake.creates.at(-1)).toMatchObject({ entry_point: 'home_cockpit_create' });

  await pane(page).getByRole('button', { name: 'Continue draft' }).click();
  await expect(page.locator('#folderNameInput')).toHaveValue(name);
  await expect(page.locator(`#wizardStep${step}`)).toBeHidden();
  await expect.poll(() => currentStep(page)).toBe(step);
  await expect(pane(page)).toContainText('Notes for my course');
  await expect(page.locator('#workspaceBuildDraftChange')).toBeHidden();
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
  await expect(page.locator('#workspaceBuildDraftSummary')).toBeHidden();
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
  const promise = new Promise<void>(resolve => {
    release = resolve;
  });
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
  await expect
    .poll(() => fake.drafts.some(draft => draft.draft.name === 'My manual draft'))
    .toBe(true);
  await manual(page);
  await expect(page.locator('#folderNameInput')).toHaveValue('My manual draft');
  expect(await currentStep(page)).toBe(2);
});

test('late scripted turn keeps newer manual values and cannot create or reopen assisted setup', async ({
  page
}) => {
  const wait = gate();
  let hold = false;
  const fake = await fakeBuildServer(
    page,
    [
      {
        text: 'Here is your draft.',
        set: { template_id: 'content-production', name: 'Original draft' }
      },
      { text: 'Here is the late reply.', set: { name: 'Late overwritten name' }, createNow: true }
    ],
    { turnGate: () => (hold ? wait.promise : Promise.resolve()) }
  );
  let creates = 0;
  page.on('request', request => {
    if (request.method() === 'POST' && new URL(request.url()).pathname === '/api/workspaces')
      creates++;
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
  await expect
    .poll(() => fake.drafts.some(draft => draft.draft.name === 'Newer manual name'))
    .toBe(true);
  await expect(page.locator('#folderNameInput')).toHaveValue('Newer manual name');
  await expect(page.locator('#folderNameInput')).toBeFocused();
  await expect(pane(page)).toBeHidden();
  expect(creates).toBe(0);
  await assisted(page);
  await expect(pane(page)).toContainText('Your manual draft was kept');
  await expect(page.locator('#workspaceBuildDraftName')).toHaveText('Newer manual name');
  await expect(page.locator('#workspaceBuildDraftChange')).toContainText('You edited: name');
  await expect(page.locator('#folderNameInput')).toHaveValue('Newer manual name');
  expect(fake.abandons).toHaveLength(0);
});

test('scripted turn failure offers Retry and preserves the populated shared draft', async ({
  page
}) => {
  const options = { turnFailures: 0 };
  const fake = await fakeBuildServer(
    page,
    [
      { text: 'Draft ready.', set: { blank: true, name: 'Kept draft' } },
      { text: 'Recovered without losing your draft.' }
    ],
    options
  );
  await openFromHome(page);
  await say(page, 'A blank workspace');
  options.turnFailures = 1;
  await say(page, 'Continue');
  await expect(pane(page)).toContainText('couldn’t reach my model');
  await expect(page.locator('#workspaceBuildDraftNext')).toContainText(
    'Try again or set up manually'
  );
  await expect(page.locator('#folderNameInput')).toHaveValue('Kept draft');
  await pane(page).getByRole('button', { name: 'Try again' }).click();
  await expect(pane(page)).toContainText('Recovered without losing your draft');
  expect(fake.turns).toHaveLength(2);
});

test('failed draft sync stays local, blocks new turns, and recovers through Retry', async ({
  page
}) => {
  const options = { draftFailures: 0 };
  const fake = await fakeBuildServer(page, [], options);
  await openFromHome(page);
  await expect(composer(page)).toBeEnabled();
  options.draftFailures = 1;
  await manual(page);
  await page.locator('#wizardNextBtn').click();
  await page.locator('#folderNameInput').fill('Unsaved shared draft');
  await expect(page.locator('#workspaceBuildModeStatus')).toContainText('Draft not saved');
  expect(fake.turns).toHaveLength(0);
  await assisted(page);
  await expect(page.locator('#workspaceBuildDraftSaveState')).toContainText(
    'Not saved. Your edits are kept here'
  );
  await expect(page.locator('#workspaceBuildDraftName')).toHaveText('Unsaved shared draft');
  await pane(page).getByRole('button', { name: 'Try again' }).click();
  await expect(page.locator('#workspaceBuildDraftSaveState')).toHaveText('Draft saved.');
  await expect.poll(() => fake.session?.draft.name).toBe('Unsaved shared draft');
  await expect(page.locator('#folderNameInput')).toHaveValue('Unsaved shared draft');
  expect(fake.turns).toHaveLength(0);
});

for (const theme of ['light', 'dark']) {
  test(`one active creation mode, keyboard switch and readable narrow layout (${theme})`, async ({
    page
  }, testInfo) => {
    await page.addInitScript(value => localStorage.setItem('ori-theme', value), theme);
    await page.emulateMedia({ reducedMotion: 'reduce' });
    await page.setViewportSize({ width: 390, height: 844 });
    await fakeBuildServer(page, [
      { text: 'We can review this draft together.', set: { blank: true, name: 'Shared draft' } }
    ]);
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
      const result = await (window as any).axe.run('#addFolderModal', {
        runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa'] }
      });
      return {
        violations: result.violations,
        contrast: result.passes.find((rule: any) => rule.id === 'color-contrast')?.nodes.length || 0
      };
    });
    expect(scan.violations).toEqual([]);
    expect(scan.contrast).toBeGreaterThan(0);
    expect(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth + 1)).toBe(
      false
    );
    await page.screenshot({
      path: testInfo.outputPath(`creation-assisted-${theme}.png`),
      fullPage: true
    });
    await checkNextActionReflow(page, '#addFolderModal');
    await page.screenshot({
      path: testInfo.outputPath(`creation-assisted-zoom-${theme}.png`),
      fullPage: true
    });
    await switcher.focus();
    await page.keyboard.press('Enter');
    await expect(pane(page)).toBeHidden();
    await expect(page.locator(`#wizardStep${await currentStep(page)}Title`)).toBeFocused();
    await expect(page.locator('.workspace-create-modal-body')).toBeVisible();
    await checkNextActionReflow(page, '#addFolderModal');
    await page.locator('#folderNameInput').scrollIntoViewIfNeeded();
    await expect(page.locator('#folderNameInput')).toBeVisible();
    await expect(page.locator('#folderNameInput')).toHaveValue('Shared draft');
    await page.screenshot({
      path: testInfo.outputPath(`creation-manual-zoom-${theme}.png`),
      fullPage: true
    });
    await page.evaluate(() => {
      document.documentElement.style.zoom = '';
    });
    await page.setViewportSize({ width: 390, height: 844 });
    await page.screenshot({
      path: testInfo.outputPath(`creation-manual-${theme}.png`),
      fullPage: true
    });
    await assisted(page);
    await expect(composer(page)).toBeFocused();
    await expect(page.locator('#folderNameInput')).toHaveValue('Shared draft');
  });
}
