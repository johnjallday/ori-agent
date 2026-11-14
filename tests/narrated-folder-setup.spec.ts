import {
  test,
  expect,
  type APIRequestContext,
  type APIResponse,
  type Page
} from '@playwright/test';
import { randomUUID } from 'node:crypto';
import { mkdirSync, realpathSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, relative } from 'node:path';
import { startJanitorBuildModel } from './helpers/janitor-build-model';
import { abandonOpenBuild } from './helpers/folder-first-runtime';
import { installLocalCdn } from './helpers/offline-cdn';

/**
 * Fresh folders use the shared hire-first card, not the retired in-pane plan.
 * Hiring creates the folder's workspace; its report separately offers Set it up.
 * Retained music reviews still set up a Home library, and opening a song there
 * needs a separate explicit manager Open & hire. Both keep automatic placement and Move.
 *
 * Only model replies are scripted; use a disposable real-candidate sandbox.
 * Candidates must satisfy Ori's current reviewed floors (REAPER >=0.12.0,
 * Music >=0.5.0); --music-ref can select an existing release without switching
 * its checkout. An older installed candidate is correctly refused, not a pass.
 *
 *   LM_STUDIO_BASE_URL=http://127.0.0.1:8940/v1 LM_STUDIO_MODEL=ori-synthetic-model \
 *     NARRATED_DEMO_MODEL=lmstudio/ori-synthetic-model \
 *     ./scripts/narrated-folder-demo.sh installed --reaper-source DIR --music-source DIR --port 8955
 *   ./scripts/smoke.sh showfolder http://localhost:8955 seed-song <sandbox> Documents "Second Song"
 *   TZ=UTC ./scripts/e2e.sh --port 8955 --env NARRATED_SONG="<sandbox>/Documents/Ori Music Demo" \
 *     --env NARRATED_SECOND_SONG="<sandbox>/Documents/Second Song" tests/narrated-folder-setup.spec.ts -- --workers=1
 *
 * NARRATED_MODE=integration-missing also exercises installing the integration
 * from its reviewed release. Sources, stores, permissions and plugins are real;
 * no production settings or signed-in CLI provider are used.
 */
const SONG = process.env.NARRATED_SONG || '';
const SECOND_SONG = process.env.NARRATED_SECOND_SONG || '';
const MODE = process.env.NARRATED_MODE || 'installed';
const HOME = 'Music Production Home';

test.describe.configure({ mode: 'serial' });
test.use({ viewport: { width: 1280, height: 1000 }, actionTimeout: 15_000 });
test.skip(!SONG, 'NARRATED_SONG names a seeded song folder in a fresh isolated sandbox');

let model: Awaited<ReturnType<typeof startJanitorBuildModel>>;
test.beforeAll(async () => {
  // This suite creates retained-song fixtures beside SONG. Refuse personal
  // folders before any HTTP mutation or filesystem write, even if misconfigured.
  const song = realpathSync(SONG);
  expect(relative(realpathSync(tmpdir()), song)).toMatch(
    /^ori-demo\.narrated-[^/]+\/Documents\/[^/]+$/
  );
  if (SECOND_SONG) expect(dirname(realpathSync(SECOND_SONG))).toBe(dirname(song));
  model = await startJanitorBuildModel({
    nonBuildText:
      '## What it is\nA synthetic REAPER song project.\n## Plan\nConnect the REAPER tools [reaper-tools].'
  });
});
test.afterAll(async () => {
  await model?.();
});
test.beforeEach(async ({ page, request }) => {
  await installLocalCdn(page);
  await json(await request.post('/api/onboarding/skip'));
});

async function json(response: Pick<APIResponse, 'ok' | 'text' | 'json'>) {
  expect(response.ok(), await response.text()).toBeTruthy();
  return response.json();
}

type Workspace = {
  id: string;
  name: string;
  kind?: string;
  parent_id?: string;
  folder_slug?: string;
};
async function workspaces(request: APIRequestContext): Promise<Workspace[]> {
  const body = await json(await request.get('/api/workspaces'));
  expect(
    Array.isArray(body.folders),
    'read the actual workspace list, never a vacuous empty snapshot'
  ).toBe(true);
  return body.folders;
}
async function layoutPositions(page: Page): Promise<Record<string, { x: number; y: number }>> {
  return (await json(await page.request.get('/api/workspace-map/layout'))).layout?.positions || {};
}
async function expectNoPlacementAsk(page: Page) {
  await page.waitForTimeout(3_000);
  await expect(page.locator('[data-ws-map-placement-controls]')).toHaveCount(0);
}
async function moveThenKeep(page: Page, id: string, name: string) {
  const before = await layoutPositions(page);
  await page.goto(`/?move=${encodeURIComponent(id)}`);
  await expect(page.locator('[data-map-live]')).toHaveText(
    new RegExp(`^Move is on for ${name}\\.`),
    { timeout: 30_000 }
  );
  await expect(page.locator('[data-map-drag]').first()).toHaveAttribute('aria-pressed', 'true');
  await expect(page).not.toHaveURL(/move=/);
  await page.locator('[data-ws-map-viewport]').press('ArrowRight');
  await expect(page.locator('[data-map-live]')).toHaveText(/^(Moving from|Candidate) /);
  await page.locator('[data-ws-map-viewport]').press('Escape');
  expect(await layoutPositions(page)).toEqual(before);
}

async function attach(page: Page, entry: 'sheet' | 'home', path: string) {
  if (entry === 'sheet') {
    await abandonOpenBuild(page.request);
    await page.goto('/');
    await page.locator('#cockpitCreateWorkspaceBtn').click();
    await page
      .locator('#workspaceBuildPane')
      .getByRole('button', { name: 'Add folder', exact: true })
      .click();
    await page.getByPlaceholder('~/Projects/my-app').fill(path);
    await page.getByRole('button', { name: 'Use this path', exact: true }).click();
  } else {
    await page.goto('/?panel=today&folder=show');
    await page.locator('.folder-path-entry__input:visible').first().fill(path);
    await page.getByRole('button', { name: 'Use this path', exact: true }).first().click();
  }
  const card = page.locator('[data-folder-agent-card="choose"]');
  await expect(card).toBeVisible();
  await expect(card).toContainText('Nothing is built until you hire');
  return card;
}

async function hireAndSetUp(page: Page, entry: 'sheet' | 'home', path: string, brief: string) {
  const before = await workspaces(page.request);
  const positions = await layoutPositions(page);
  const card = await attach(page, entry, path);
  await card.locator('.folder-agent-card__brief').fill(brief);
  const accepted = page.waitForResponse(
    response =>
      response.url().endsWith('/folder-agent/start') && response.request().method() === 'POST'
  );
  await card.getByRole('button', { name: 'Hire & build', exact: true }).click();
  const created = (await json(await accepted)).folder_agent;
  expect((await workspaces(page.request)).length).toBe(before.length + 1);
  const read = async () =>
    (
      await json(
        await page.request.get(`/api/personal-assistant/folder-agent/${created.workspace_id}`)
      )
    ).folder_agent;
  await expect.poll(async () => (await read()).state, { timeout: 60_000 }).toBe('reported');
  const reported = await read();
  expect(reported.brief).toBe(brief);
  expect(reported.plan.source).toBe('agent');
  expect(reported.plan.selected).toContain('reaper-tools');
  expect(reported.plan.steps.some((step: any) => step.kind === 'home')).toBe(false);
  // A single root project is not a portfolio. Installing its tools must not
  // silently make a Home or open an additional song workspace.
  expect(
    (await workspaces(page.request)).filter(item => item.kind === 'group' && item.name === HOME)
  ).toEqual(before.filter(item => item.kind === 'group' && item.name === HOME));
  await page.goto(reported.route);
  const close = page.getByRole('button', { name: 'Close personal assistant', exact: true });
  if (await close.isVisible()) await close.click();
  await page.getByRole('button', { name: 'Open in chat', exact: true }).click();
  const report = page.locator('.folder-agent-card--report:visible');
  await expect(report).toContainText('REAPER');
  const setup = report.locator('[data-folder-agent-action="set_up"]');
  await expect(setup).toHaveText(/^(Install \d+ & set up|Set it up)$/);
  await setup.click();
  await expect
    .poll(async () => (await read()).setup?.status || '', { timeout: 180_000 })
    .toMatch(/^(done|stopped)$/);
  const done = await read();
  expect(done.setup, JSON.stringify(done.setup)).toMatchObject({ status: 'done' });
  expect(
    done.setup.lines.some(
      (line: any) =>
        line.kind === 'workspace' && line.step === 'reaper-tools' && line.state === 'done'
    )
  ).toBe(true);
  expect((await workspaces(page.request)).length).toBe(before.length + 1);
  expect(await layoutPositions(page)).toEqual(positions);
  await page.goto('/');
  await expectNoPlacementAsk(page);
  return done;
}

test('folder-first attachment offers a hire without a build turn or resource write', async ({
  page,
  request
}, testInfo) => {
  const before = await workspaces(request);
  const roster = async () =>
    (await json(await request.get('/api/agents'))).agents.sort((a: any, b: any) =>
      a.name.localeCompare(b.name)
    );
  const agents = await roster();
  let turns = 0,
    starts = 0;
  page.on('request', r => {
    if (r.method() !== 'POST') return;
    if (r.url().endsWith('/turns')) turns++;
    if (r.url().endsWith('/folder-agent/start')) starts++;
  });
  const card = await attach(page, 'sheet', SONG);
  await expect(card).toContainText('Ori Music Demo');
  await expect(card.getByRole('button', { name: 'Hire & build', exact: true })).toBeEnabled();
  await expect(page.locator('#workspaceBuildFolderReview')).toBeHidden();
  expect(await workspaces(request)).toEqual(before);
  expect(await roster()).toEqual(agents);
  expect(turns).toBe(0);
  expect(starts).toBe(0);
  expect(model.calls()).toBe(0);
  await page.screenshot({ path: testInfo.outputPath('folder-first-hire-review.png') });
  await page.getByRole('button', { name: 'Close create workspace', exact: true }).click();
  await abandonOpenBuild(request);
});

test('Create Workspace hires with the exact brief; only the report confirms tools and Move cancels cleanly', async ({
  page
}, testInfo) => {
  test.setTimeout(240_000);
  const done = await hireAndSetUp(page, 'sheet', SONG, 'Mix the chorus first.');
  await page.screenshot({ path: testInfo.outputPath('sheet-hired-and-connected.png') });
  await moveThenKeep(page, done.workspace_id, 'Ori Music Demo');
});

test('Home Explore uses the same hire and explicit setup without implicitly making a Home', async ({
  page
}, testInfo) => {
  test.skip(!SECOND_SONG || MODE !== 'installed', 'NARRATED_SECOND_SONG on an installed sandbox');
  test.setTimeout(240_000);
  const done = await hireAndSetUp(
    page,
    'home',
    SECOND_SONG,
    'Arrange the second song without changing files.'
  );
  await page.screenshot({ path: testInfo.outputPath('home-hired-and-connected.png') });
  await moveThenKeep(page, done.workspace_id, 'Second Song');
});

test('retained music reviews still create one Home library; explicit manager Opens join it and preserve each brief', async ({
  page,
  request
}, testInfo) => {
  test.skip(MODE !== 'installed', 'retained Home acceptance uses both installed real candidates');
  test.setTimeout(240_000);
  let home: Workspace | undefined;
  let setupPosts = 0;
  page.on('request', outgoing => {
    if (outgoing.method() === 'POST' && /folder-digest\/offers\/[^/]+\/setup$/.test(outgoing.url()))
      setupPosts++;
  });
  const caseID = randomUUID().slice(0, 8);
  for (let index = 1; index <= 2; index++) {
    // A retained source review is an existing owner, not a new folder-feed UI.
    // Seed it through the real metadata-only selection/review APIs, then resume
    // its canonical conversation. No fake plan or completion is injected.
    const name = `Retained Narrated Song ${index} ${caseID}`;
    const path = join(dirname(SONG), name);
    mkdirSync(path, { recursive: true, mode: 0o750 });
    writeFileSync(join(path, 'Song.rpp'), '<REAPER_PROJECT 0.1 7.0 1\n>\n', { mode: 0o600 });
    const picked = await json(
      await request.post('/api/folder-picker/select-path', { data: { path } })
    );
    const draft_id = randomUUID();
    const selected = await json(
      await request.post('/api/home-assistant/folder-context/select', {
        data: { draft_id, revision: '', mode: 'selection', selection_token: picked.selection_token }
      })
    );
    expect(selected.hire_candidate).toBeTruthy();
    const reviewed = await json(
      await request.post('/api/home-assistant/folder-context/review', {
        data: {
          draft_id,
          revision: '',
          selection_id: selected.observation.id,
          candidate_id: selected.hire_candidate
        }
      })
    );
    expect(reviewed.conversation.id).toBeTruthy();
    const before = await workspaces(request);
    const positions = await layoutPositions(page);
    await page.goto('/');
    await page.waitForFunction(
      () =>
        (window as any).PersonalAssistantConversation?._state.hydrated &&
        !(window as any).PersonalAssistantConversation.isLoading()
    );
    if (!(await page.locator('#personalAssistantPanel').isVisible()))
      await page.locator('#personalAssistantLauncher').click();
    await expect(page.locator('#personalAssistantPanel')).toBeVisible();
    await page.waitForFunction(() => !(window as any).PersonalAssistantConversation.isLoading());
    const resumed = await page.evaluate(
      id => (window as any).PersonalAssistantConversation.resume(id),
      reviewed.conversation.id
    );
    expect(resumed).toBe(true);
    expect(
      await page.evaluate(() => (window as any).PersonalAssistantConversation.currentId())
    ).toBe(reviewed.conversation.id);
    const reviewEntry = page
      .locator('[data-source-setup-summary]')
      .getByRole('button', { name: 'Review setup', exact: true });
    await expect(reviewEntry).toBeVisible();
    await reviewEntry.click();
    const dialog = page.locator('#addFolderModal[data-source-setup-owner="music-folder"]');
    await expect(dialog).toBeVisible();
    const brief = `Preserve retained arrangement ${index}.`;
    if (index === 1) {
      // A new Home's required role is an explicit staged choice, not implicit
      // permission to hire. The optional sample-library role stays unfilled.
      await dialog
        .getByRole('button', { name: 'Create an agent for Music Portfolio Manager', exact: true })
        .click();
      const form = dialog.locator('.folder-home-staffing__editor');
      await expect(form).toBeVisible();
      await form
        .locator('[data-agent-create-field="name"]')
        .fill(`Narrated Portfolio Manager ${caseID}`);
      await form.locator('[data-agent-create-field="model"]').selectOption('ori-synthetic-model');
      const useRole = form.getByRole('button', { name: 'Use for this role', exact: true });
      await expect(useRole).toBeEnabled();
      await useRole.click();
      await expect(form).toBeHidden();
      expect(await workspaces(request)).toEqual(before);
      const returnToReview = dialog.getByRole('button', {
        name: 'Return to setup review',
        exact: true
      });
      await expect(returnToReview).toBeEnabled();
      await returnToReview.click();
    }
    if (index === 1) {
      const review = dialog.locator('[data-source-creation-review]');
      await expect(review).toContainText(name);
      const laterGoal = review.getByRole('textbox', { name: 'Later project goal', exact: true });
      await laterGoal.fill(brief);
      await laterGoal.press('Tab'); // Commit the source edit before selecting placement.
      const automatic = review.getByRole('button', {
        name: 'Use automatic placement',
        exact: true
      });
      await automatic.click();
      await expect(automatic).toHaveAttribute('aria-pressed', 'true');
      await review.getByRole('button', { name: 'Set up Home', exact: true }).click();
      await expect(review.getByRole('heading', { name: 'Home ready', exact: true })).toBeVisible({
        timeout: 120_000
      });
      const after = await workspaces(request);
      const homes = after.filter(item => item.kind === 'group' && item.name === HOME);
      expect(homes).toHaveLength(1);
      if (home) expect(homes[0].id).toBe(home.id);
      home = homes[0];
      expect(after.length).toBe(before.length + (index === 1 ? 1 : 0));
      expect(after.filter(item => item.parent_id === home!.id)).toHaveLength(index - 1);
      expect(await layoutPositions(page)).toEqual(positions);
      await dialog.getByRole('button', { name: 'Close setup review', exact: true }).click();
      const library = await json(
        await request.get(`/api/workspaces/${home.id}/assistant-program/library/projects`)
      );
      const entry = library.rows.find((row: any) => row.name === name);
      expect(entry?.connection).toBe('catalog_only');
      // The manager's Open & hire owns the deferred brief and staffing. A bare
      // library Open only makes a project scaffold and is not this consent.
      await page.goto(`/workspaces/${home.folder_slug}?mode=map&panel=assistant`);
    } else {
      // With an existing Home, the retained source sends the user straight to
      // its manager; it must not offer another Home setup or repeat staffing.
      await expect(dialog.locator('[data-source-creation-review]')).toHaveCount(0);
      expect(await workspaces(request)).toEqual(before);
      expect(setupPosts).toBe(2);
      await dialog.getByRole('button', { name: `Go to ${HOME}`, exact: true }).click();
    }
    const offer = page
      .locator('#personalAssistantHomeOffer .pa-home-offer')
      .filter({ hasText: name });
    await expect(offer).toBeVisible();
    if (index === 1) await expect(offer.locator('textarea')).toHaveValue(brief);
    else {
      await offer.locator('textarea').fill(brief);
      await offer.locator('textarea').press('Tab');
    }
    await page.waitForFunction(
      () => (window as any).OriWorkspaceMap?.getLayoutState?.().status === 'ready'
    );
    await offer.getByRole('button', { name: `Open ${name} & hire`, exact: true }).click();
    const placement = page.locator('[data-ws-map-placement-controls]');
    await expect(placement).toBeVisible();
    const openResponse = page.waitForResponse(
      response =>
        /folder-digest\/offers\/[^/]+\/setup$/.test(response.url()) &&
        response.request().method() === 'POST' &&
        response.request().postDataJSON()?.open === true
    );
    await placement.getByRole('button', { name: 'Use automatic placement', exact: true }).click();
    const opened = await openResponse;
    expect(opened.ok(), opened.ok() ? '' : await opened.text()).toBe(true);
    await expect(offer).toHaveAttribute('data-state', 'opened', { timeout: 120_000 });
    expect(setupPosts).toBe(index === 1 ? 2 : 3);
    const projects = (await workspaces(request)).filter(item => item.parent_id === home!.id);
    expect(projects).toHaveLength(index);
    const song = projects.find(item => item.name === name);
    expect(song).toBeTruthy();
    const resource = await json(await request.get(`/api/workspaces/${song!.id}`));
    expect(resource.agent_instances).toHaveLength(1);
    expect(JSON.stringify(resource)).toContain(brief);
    expect(await layoutPositions(page)).toEqual(positions);
    await page.goto('/');
    await expectNoPlacementAsk(page);
    await page.screenshot({ path: testInfo.outputPath(`retained-home-song-${index}.png`) });
    await moveThenKeep(page, song!.id, name);
  }
});
