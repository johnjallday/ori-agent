import { test, expect, Page } from '@playwright/test';
import { startJanitorBuildModel } from './helpers/janitor-build-model';

/**
 * Acceptance for narrated folder setup (tasks/prd-narrated-folder-setup.md):
 * a recognized song folder is set up from both entries as a hire with a brief
 * (tasks/prd-folder-hire-brief.md), and the Map places it by itself, with Move.
 *
 * No folder turn is sent: attaching a recognized folder already gives the build
 * its host plan, the in-pane review names the workspace, and Hire and start
 * runs Home's real setup journey (the hired agent's first look then runs on the
 * sandbox's model). Run it against the
 * narrated demo sandbox, which installs the plugins and seeds the folders:
 *
 *   ./scripts/narrated-folder-demo.sh installed --reaper-source DIR --music-source DIR --port 8955
 *   ./scripts/smoke.sh showfolder http://localhost:8955 seed-song <sandbox> Documents "Second Song"
 *   ./scripts/e2e.sh --port 8955 --env NARRATED_SONG="<sandbox>/Documents/Ori Music Demo" \
 *     --env NARRATED_SECOND_SONG="<sandbox>/Documents/Second Song" tests/narrated-folder-setup.spec.ts -- --workers=1
 *
 * NARRATED_MODE=integration-missing runs the build half against the sandbox
 * mode of that name: the plan installs the integration first. The tests run
 * in order on one fresh sandbox (a folder card left waiting on Home refuses a
 * build's Set it up as review_pending, by design) and are skipped without the
 * folders.
 */

const SONG = process.env.NARRATED_SONG || '';
const SECOND_SONG = process.env.NARRATED_SECOND_SONG || '';
const MODE = process.env.NARRATED_MODE || 'installed';
const HOME = 'Music Production Home';

test.describe.configure({ mode: 'serial' });

type Workspace = {
  id: string;
  name: string;
  kind?: string;
  parent_id?: string;
  children?: Workspace[];
};

async function workspaces(page: Page): Promise<Workspace[]> {
  const body = await (await page.request.get('/api/workspaces')).json();
  const flat: Workspace[] = [];
  const walk = (list: Workspace[] = [], parent = '') =>
    list.forEach(item => {
      flat.push({ ...item, parent_id: item.parent_id || parent });
      walk(item.children, item.id);
    });
  walk(body.workspaces);
  return flat;
}

async function layoutPositions(page: Page): Promise<Record<string, { x: number; y: number }>> {
  const body = await (await page.request.get('/api/workspace-map/layout')).json();
  return body.layout?.positions || {};
}

// Nothing asks where a folder setup's result sits (folder-hire-brief FR22):
// the Map's placement controls stay away for a while after the run.
async function expectNoPlacementAsk(page: Page) {
  await page.waitForTimeout(3_000);
  await expect(page.locator('[data-ws-map-placement-controls]')).toHaveCount(0);
}

// Move (FR23) opens Home with the building selected and Move on; Escape keeps
// the automatic layout and writes nothing.
async function moveThenKeep(page: Page, href: string, name: string) {
  const before = await layoutPositions(page);
  await page.goto(href);
  await expect(page.locator('[data-map-live]')).toHaveText(
    new RegExp(`^Move is on for ${name}\\.`),
    {
      timeout: 30_000
    }
  );
  await expect(page.locator('[data-map-drag]').first()).toHaveAttribute('aria-pressed', 'true');
  await expect(page).not.toHaveURL(/move=/);
  // The keyboard is on the canvas: an arrow key starts the move at once.
  await page.keyboard.press('ArrowRight');
  await expect(page.locator('[data-map-live]')).toHaveText(/^(Moving from|Candidate) /);
  await page.keyboard.press('Escape');
  expect(await layoutPositions(page)).toEqual(before);
}

test('folder-first recognized plan needs no model or setup action on attachment', async ({
  page,
  request
}, testInfo) => {
  test.skip(!SONG, 'NARRATED_SONG names a seeded song folder in the sandbox');
  const model = await startJanitorBuildModel();
  await page.setViewportSize({ width: 1280, height: 900 });
  try {
    const before = await workspaces(page);
    let turns = 0;
    page.on('request', r => {
      if (r.url().endsWith('/turns')) turns++;
    });
    await request.post('/api/onboarding/skip');
    await page.goto('/');
    await page.locator('#cockpitCreateWorkspaceBtn').click();
    await page.locator('[data-build-control=add-folder]').click();
    await page.fill('#workspaceBuildFolderPath', SONG);
    await page.locator('[data-build-control=folder-path]').click();
    await expect(page.locator('.workspace-build-folder__recognition')).toContainText(
      'Recognized: REAPER project'
    );
    await expect(page.locator('#workspaceBuildFolderReview')).toBeVisible();
    await expect(page.locator('#workspaceBuildPlanReviewLines')).toContainText(
      `Creates Home “${HOME}”`
    );
    await expect(page.locator('#workspaceBuildFolderTeamEdit')).toBeHidden();
    await expect(page.locator('#workspaceBuildFolderPurpose')).toContainText('fixed by this plan');
    expect(await workspaces(page)).toEqual(before);
    expect(turns).toBe(0);
    expect(model.calls()).toBe(0);
    await page.screenshot({ path: testInfo.outputPath('folder-first-recognized-plan.png') });
    const build = (
      await (
        await request.post('/api/workspaces/build-sessions', {
          data: { entry_point: 'home_cockpit_create' }
        })
      ).json()
    ).session;
    expect(build.proposal).toBeUndefined();
    expect(build.setup_run_id || '').toBe('');
    await request.post(`/api/workspaces/build-sessions/${build.id}/abandon`, {
      data: { version: build.version }
    });
  } finally {
    await model();
  }
});

test('the build path recognizes the song, hires with a brief, and the Map places it itself', async ({
  page
}) => {
  test.skip(!SONG, 'NARRATED_SONG names a seeded song folder in the sandbox');
  test.setTimeout(240_000);
  await page.goto('/');
  await page.click('#cockpitCreateWorkspaceBtn');
  await expect(page.locator('#workspaceBuildPane')).toBeVisible();
  // A build left open on the sandbox is asked about first; this one starts over.
  const startOver = page.locator('[data-build-choice=start_over]:not([disabled])');
  if (
    await startOver.waitFor({ timeout: 3_000 }).then(
      () => true,
      () => false
    )
  )
    await startOver.click();

  await page.locator('[data-build-control=add-folder]').click();
  await page.fill('#workspaceBuildFolderPath', SONG);
  await page.locator('[data-build-control=folder-path]').click();
  await expect(page.locator('.workspace-build-folder__recognition')).toHaveText(
    /^Recognized: REAPER project · \d+ session files?$/
  );
  const needs =
    MODE === 'integration-missing'
      ? 'Needs: REAPER integration (install) · Music Project Management (installed)'
      : 'Needs: REAPER integration (installed) · Music Project Management (installed)';
  await expect(page.locator('#workspaceBuildPane')).toContainText(needs);
  await expect(page.locator('#workspaceBuildPlan')).toContainText(`Creates Home “${HOME}”`);
  await expect(page.locator('#workspaceBuildDraftTitle')).toHaveText('Plan so far');

  // Attachment opens the shared review directly. Naming the workspace does
  // not change the host-owned plan, team or placement.
  await expect(page.locator('#workspaceBuildFolderReview')).toBeVisible();
  await expect(page.locator('#workspaceBuildPane')).toBeHidden();
  await page.fill('#workspaceBuildPlanName', 'Ori Music Demo');
  // The plan is a hire: titled by it, with the one question.
  await expect(page.locator('#workspaceBuildPlanReviewTitle')).toHaveText(
    /^(Hire|Assign) .+ (for|to) Ori Music Demo$/
  );
  const brief = page.locator('#workspaceBuildPlanReviewBrief');
  await expect(brief).toHaveValue(/\S/);
  await brief.fill('Mix the chorus first.');
  const setUp = page.locator('#workspaceBuildPlanReviewSetup');
  await expect(setUp).toHaveText('Hire and start');
  await expect(setUp).toBeEnabled({ timeout: 15_000 });
  const positions = await layoutPositions(page);
  await setUp.click();

  // The run finishes and the dialog closes; the Map's automatic layout places
  // the Home and the song, and nothing asks or writes a position.
  await expect(page.locator('#addFolderModal')).toBeHidden({ timeout: 180_000 });
  await expectNoPlacementAsk(page);

  const all = await workspaces(page);
  const home = all.find(item => item.name === HOME && item.kind === 'group');
  const song = all.find(item => item.name === 'Ori Music Demo');
  expect(home, 'the Home was made').toBeTruthy();
  expect(song?.parent_id, 'the song went inside it').toBe(home?.id);
  expect(await layoutPositions(page)).toEqual(positions);
  const goal = await (await page.request.get(`/api/workspaces/${song!.id}`)).json();
  expect(JSON.stringify(goal)).toContain('Mix the chorus first.');
  await moveThenKeep(page, `/?move=${encodeURIComponent(song!.id)}`, 'Ori Music Demo');
});

test('Home’s Explore says the same lines, joins the existing Home, and Move keeps the automatic layout', async ({
  page
}) => {
  test.skip(!SECOND_SONG || MODE !== 'installed', 'NARRATED_SECOND_SONG on an installed sandbox');
  test.setTimeout(240_000);
  await page.goto('/?panel=today&folder=show');
  await page.locator('.folder-path-entry__input:visible').first().fill(SECOND_SONG);
  await page.locator('button:visible', { hasText: 'Use this path' }).first().click();
  // A first folder goes straight to Home's card; a later one is attached to
  // the conversation, whose attach opens the song's own review at once
  // (hire_candidate). Both end on the same card, with no chooser.
  await expect(
    page.locator('select:visible', { has: page.locator('option', { hasText: 'Choose a folder' }) })
  ).toHaveCount(0);

  // The card speaks the build pane's lines, in the drawer or, for a first
  // folder, on the mission card.
  await expect(
    page
      .locator(
        '#personalAssistantFolderOfferRecognition:visible, [data-role=first-mission-offer-recognition]:visible'
      )
      .first()
  ).toHaveText('Recognized: REAPER project', { timeout: 30_000 });
  await expect(
    page
      .locator(
        '#personalAssistantFolderOfferNeeds:visible, [data-role=first-mission-offer-needs]:visible'
      )
      .first()
  ).toHaveText('Needs: REAPER integration (installed) · Music Project Management (installed)');
  // The card is a hire: its one question starts from the shape's default.
  await expect(page.getByLabel('What should it do with it?').first()).toHaveValue(/\S/);
  await page
    .locator('button:visible', { hasText: /^Hire and start$/ })
    .first()
    .click();

  // The run ends on its pop-up, which says where the building sits, with
  // Move; nothing asks where to place it, before or after the drawer closes.
  const before = await layoutPositions(page);
  const runModal = page.locator('#folderSetupRunModal');
  await expect(runModal).toHaveAttribute('data-phase', 'done', { timeout: 180_000 });
  const onMap = runModal.locator('#folderSetupRunMap');
  await expect(onMap).toContainText(`building sits inside ${HOME}.`);
  const move = await onMap.getByRole('link', { name: 'Move' }).getAttribute('href');
  expect(move).toMatch(/^\/\?move=/);
  await runModal.locator('.btn-close, [data-bs-dismiss=modal]').first().click();
  await page.click('#personalAssistantClose');
  await expectNoPlacementAsk(page);

  const all = await workspaces(page);
  const home = all.find(item => item.name === HOME && item.kind === 'group');
  const second = all.find(item => item.name === 'Second Song');
  expect(second?.parent_id, 'the second song joined the existing Home').toBe(home?.id);
  expect(move).toBe(`/?move=${encodeURIComponent(second!.id)}`);
  expect(await layoutPositions(page)).toEqual(before);
  await moveThenKeep(page, move!, 'Second Song');
});
