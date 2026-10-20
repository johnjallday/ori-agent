import { test, expect, Page } from '@playwright/test';

/**
 * Acceptance for narrated folder setup (tasks/prd-narrated-folder-setup.md):
 * a recognized song folder is set up from both entries and placed on the Map.
 *
 * Nothing here is faked and no model turn is sent: attaching a recognized
 * folder already gives the build its host plan, the in-pane review names the
 * workspace, and Set it up runs Home's real setup journey. Run it against the
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

async function placement(page: Page) {
  const controls = page.locator('[data-ws-map-placement-controls]');
  await expect(controls).toBeVisible({ timeout: 60_000 });
  return controls;
}

test('the build path recognizes the song, runs one Set it up, and places the new Home', async ({
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

  // Review or edit draft names the workspace; nothing else on the plan moves.
  // The open review stands in for the conversation, with its own Set it up.
  await page.click('#workspaceBuildEditDraftBtn');
  await expect(page.locator('#workspaceBuildPane')).toBeHidden();
  await page.fill('#workspaceBuildPlanName', 'Ori Music Demo');
  const setUp = page.locator('#workspaceBuildPlanReviewSetup');
  await expect(setUp).toBeEnabled({ timeout: 15_000 });
  await setUp.click();

  // The run finishes, the dialog closes, and the Map asks where the Home sits.
  await expect(page.locator('#addFolderModal')).toBeHidden({ timeout: 180_000 });
  const controls = await placement(page);
  await expect(controls).toContainText(`Place ${HOME}`);
  await expect(controls).toContainText(
    `Choose where ${HOME} sits. Ori Music Demo goes inside it. Click open ground to place; Esc keeps the automatic layout.`
  );
  await expect(page.locator('[data-ws-map-placement-district]')).toBeVisible();
  await page.keyboard.press('Enter');
  await expect(controls).toBeHidden({ timeout: 30_000 });
  await expect(page.locator('[data-map-live]')).toHaveText(
    new RegExp(`^${HOME} and Ori Music Demo placed at -?\\d+, -?\\d+\\.$`)
  );

  const all = await workspaces(page);
  const home = all.find(item => item.name === HOME && item.kind === 'group');
  const song = all.find(item => item.name === 'Ori Music Demo');
  expect(home, 'the Home was made').toBeTruthy();
  expect(song?.parent_id, 'the song went inside it').toBe(home?.id);
  expect(Object.keys(await layoutPositions(page))).toContain(song!.id);
});

test('Home’s Explore says the same lines, joins the existing Home, and Keep automatic layout writes nothing', async ({
  page
}) => {
  test.skip(!SECOND_SONG || MODE !== 'installed', 'NARRATED_SECOND_SONG on an installed sandbox');
  test.setTimeout(240_000);
  await page.goto('/?panel=today&folder=show');
  await page.locator('.folder-path-entry__input:visible').first().fill(SECOND_SONG);
  await page.locator('button:visible', { hasText: 'Use this path' }).first().click();
  // A first folder goes straight to Home's card; a later one is attached to
  // the conversation, whose Review setup asks which observed folder it is
  // about (the song itself). Both end on the same card.
  const reviewSetup = page.getByText('Review setup', { exact: true }).first();
  const attached = await reviewSetup.waitFor({ state: 'visible', timeout: 10_000 }).then(
    () => true,
    () => false
  );
  if (attached) {
    await reviewSetup.click();
    const scope = page
      .locator('select:visible', { has: page.locator('option', { hasText: 'Choose a folder' }) })
      .first();
    await scope.waitFor({ state: 'visible', timeout: 30_000 });
    await scope.selectOption({ index: 1 });
    await page.getByRole('button', { name: 'Review selection' }).first().click();
  }

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
  await page
    .locator('button:visible', { hasText: /^Set up$/ })
    .first()
    .click();

  // The run ends on its pop-up; nothing is asked while the drawer covers the Map.
  const runModal = page.locator('#folderSetupRunModal');
  await expect(runModal).toHaveAttribute('data-phase', 'done', { timeout: 180_000 });
  await runModal.locator('.btn-close, [data-bs-dismiss=modal]').first().click();
  await expect(page.locator('[data-ws-map-placement-controls]')).toHaveCount(0);
  await page.click('#personalAssistantClose');

  const controls = await placement(page);
  await expect(controls).toContainText(
    `Choose where Second Song sits in ${HOME}. Click open ground to place; Esc keeps the automatic layout.`
  );
  const before = await layoutPositions(page);
  await controls.getByRole('button', { name: 'Keep automatic layout' }).click();
  await expect(controls).toBeHidden();
  await expect(page.locator('[data-map-live]')).toHaveText(
    'Kept the automatic layout. Nothing was moved.'
  );

  const all = await workspaces(page);
  const home = all.find(item => item.name === HOME && item.kind === 'group');
  const second = all.find(item => item.name === 'Second Song');
  expect(second?.parent_id, 'the second song joined the existing Home').toBe(home?.id);
  expect(await layoutPositions(page)).toEqual(before);
});
