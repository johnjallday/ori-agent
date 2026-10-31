import { expect, test, type APIRequestContext, type Page } from '@playwright/test';
import { randomUUID } from 'node:crypto';
import { openMapReceiptDetails } from './helpers/confirm-reviewed-creation';

// Real exact-candidate owner/journal/role/layout acceptance. The Music wrapper
// must stage its immutable candidate in disposable HOME/data first. No model,
// live application or ordinary-create success is supplied by a route fixture.
test.skip(
  process.env.ORI_TEST_REVIEW_HOMES !== '1' && process.env.ORI_MUSIC_HOME_ACCEPTANCE !== '1',
  'requires isolated exact Music candidate'
);

async function snapshot(request: APIRequestContext) {
  const workspaces = await (await request.get('/api/workspaces')).json();
  const agents = await (await request.get('/api/agents')).json();
  return {
    ids: workspaces.folders.map((item: { id: string }) => item.id).sort(),
    agents: agents.agents.map((item: { name: string }) => item.name).sort(),
    layout: await (await request.get('/api/workspace-map/layout')).json()
  };
}

const map = process.env.ORI_TEST_HOME_MAP === '1';
const lost = process.env.ORI_TEST_HOME_LOST === '1';
const failedPosition = process.env.ORI_TEST_HOME_POSITION_FAILURE === '1';
const sourceChanged = process.env.ORI_TEST_HOME_SOURCE_CHANGE === '1';
const sourceAfter = process.env.ORI_TEST_HOME_SOURCE_AFTER === '1';
const originalCommits = sourceChanged ? 2 : 1;

async function openReview(page: Page, name?: string, resume = false) {
  await page.goto('/');
  if (map) {
    await page.getByRole('button', { name: 'Map', exact: true }).click();
    await page.locator('#cockpitCreateWorkspaceBtn').click();
    await page.locator('#workspaceCreatorKindGroup').check();
  } else {
    await page.getByRole('button', { name: 'Tree', exact: true }).click();
    await page.locator('[data-tree-new-group]').click();
  }
  const creator = page.locator('#addFolderModal');
  if (resume) {
    const review = creator.locator('#workspaceCreationReviewPanel');
    await expect(
      review.getByRole('heading', { name: 'Check confirmed Home', exact: true })
    ).toBeVisible();
    return review;
  }
  await creator
    .locator('.workspace-group-template-option', { hasText: 'Music Production Home' })
    .locator('input')
    .check();
  await creator.locator('#wizardNextBtn').click();
  if (name) await creator.locator('#folderNameInput').fill(name);
  await creator.locator('#wizardNextBtn').click();
  await expect(creator.locator('#wizardStep3')).toBeVisible();
  await creator.locator('#wizardNextBtn').click();
  const review = creator.locator('#workspaceCreationReviewPanel');
  await expect(review).toBeVisible();
  await expect(creator.locator('#workspaceReviewSummary')).toBeHidden();
  return review;
}

test(`managed Home ${map ? 'map frame' : 'automatic'}${lost ? ' with lost real delivery' : failedPosition ? ' with failed position' : sourceChanged ? ' with changed source' : sourceAfter ? ' with unavailable source recovery' : ''} review creates/staffs once, reload only observes, and reuse preserves its identity`, async ({
  page,
  request
}) => {
  expect((await snapshot(request)).ids).toHaveLength(0);
  expect((await request.post('/api/onboarding/skip')).ok()).toBeTruthy();
  const before = await snapshot(request);
  let commits = 0,
    staffing = 0,
    ordinary = 0;
  page.on('request', outgoing => {
    const path = new URL(outgoing.url()).pathname;
    if (outgoing.method() === 'POST' && path === '/api/workspaces/group-templates/commit')
      commits++;
    if (outgoing.method() === 'PUT' && /\/roles\//.test(path)) staffing++;
    if (outgoing.method() === 'POST' && path === '/api/workspaces') ordinary++;
  });
  if (lost) {
    await page.route(
      '**/api/workspaces/group-templates/commit',
      async route => {
        const actual = await route.fetch();
        expect(actual.ok(), await actual.text()).toBeTruthy();
        // The original owner really committed. Only delivery to this page is
        // lost; no successful receipt or canonical resource is manufactured.
        expect((await actual.json()).group_template.home_workspace_id).toBeTruthy();
        await route.abort('failed');
      },
      { times: 1 }
    );
  }
  if (failedPosition) {
    let injected = false;
    await page.route('**/api/workspace-map/layout', async route => {
      if (route.request().method() !== 'PATCH' || injected) return route.continue();
      if (
        !route
          .request()
          .postDataJSON()
          ?.operations?.some((operation: { op: string }) => operation.op === 'set_positions')
      )
        return route.continue();
      injected = true;
      await route.fulfill({
        status: 503,
        contentType: 'application/json',
        body: JSON.stringify({ error: 'Injected position-write failure' })
      });
    });
  }
  const name = `Reviewed Home ${randomUUID().slice(0, 8)}`;
  let review = await openReview(page, name);
  await expect(review).toContainText('Music Portfolio Manager');
  await expect(review).toContainText('Sample Library Manager');
  await expect(review).toContainText(
    'No project, scan, schedule, task, tool access or application opening'
  );
  await expect(page.locator('#createFolderBtn')).toBeHidden();
  // The old creator/proposal entry may reveal review, never act as consent.
  await page.evaluate(() => (window as any).sessionManager.createFolder());
  expect(await snapshot(request)).toEqual(before);
  let candidate: { x: number; y: number } | null = null;
  if (map) {
    const place = review.getByRole('button', { name: 'Place & create on map', exact: true });
    await expect(place).toBeEnabled();
    await place.click();
    await expect(page.locator('#addFolderModal')).toBeHidden();
    await expect(page.locator('.ws-map-placement-preview--group')).toBeVisible();
    expect(await snapshot(request)).toEqual(before);
    await page.keyboard.press('Escape');
    await expect(review.locator('[data-placement-field="name"]')).toHaveValue(name);
    await page.waitForFunction(
      () => (window as any).sessionManager.workspaceMapPlacement?.phase === 'review'
    );
    await place.click();
    await expect(page.locator('#addFolderModal')).toBeHidden();
    const frame = page.locator('.ws-map-placement-preview--group');
    await expect(frame).toBeVisible();
    await expect(frame).toHaveCSS('width', '176px');
    await expect(frame).toHaveCSS('height', '170px');
    const canvas = page.locator('[data-ws-map-viewport]').filter({ visible: true }).first();
    const confirm = page.locator('[data-ws-map-placement-confirm]');
    await canvas.focus();
    for (let n = 0; n < 30 && !(await confirm.isEnabled()); n++) await canvas.press('ArrowDown');
    await expect(confirm).toHaveText('Create & staff Home here');
    await expect(confirm).toBeEnabled();
    candidate = await frame.evaluate(element => ({
      x: Number.parseFloat((element as HTMLElement).style.left),
      y: Number.parseFloat((element as HTMLElement).style.top)
    }));
    if (sourceChanged) {
      expect(
        (await request.post('/api/plugins/music-project-management/disable')).ok()
      ).toBeTruthy();
      await canvas.press('Enter');
      await expect(review).toBeVisible();
      await expect(review.locator('[data-placement-field="name"]')).toHaveValue(name);
      await expect(
        review.getByRole('button', { name: 'Refresh review', exact: true })
      ).toBeVisible();
      await expect(place).toBeDisabled();
      expect(commits).toBe(1);
      expect(staffing).toBe(0);
      expect(await snapshot(request)).toEqual(before);
      expect(
        (await request.post('/api/plugins/music-project-management/enable')).ok()
      ).toBeTruthy();
      await review.getByRole('button', { name: 'Refresh review', exact: true }).click();
      await expect(place).toBeEnabled();
      expect(await snapshot(request)).toEqual(before);
      await place.click();
      await expect(page.locator('#addFolderModal')).toBeHidden();
      await canvas.focus();
      await expect(confirm).toBeEnabled();
      candidate = await frame.evaluate(element => ({
        x: Number.parseFloat((element as HTMLElement).style.left),
        y: Number.parseFloat((element as HTMLElement).style.top)
      }));
    }
    await canvas.press('Enter');
    // The accepted Home reports on the Map; Details is its full receipt.
    await openMapReceiptDetails(page, 'Home');
  } else {
    await review.getByRole('button', { name: 'Use automatic placement', exact: true }).click();
    await review.locator('[data-placement-action="confirm"]').click();
  }
  if (lost) {
    await expect(
      review.getByRole('heading', { name: 'Check confirmed Home', exact: true })
    ).toBeVisible();
    await expect(
      review.getByRole('button', { name: 'Read saved status', exact: true })
    ).toBeEnabled();
    expect(commits).toBe(1);
    expect(staffing).toBe(0);
    await review.getByRole('button', { name: 'Read saved status', exact: true }).click();
  }
  await expect(review.getByRole('heading', { name: 'Home ready', exact: true })).toBeVisible();
  await expect(review.getByRole('button', { name: 'Open group', exact: true })).toBeEnabled();
  if (map && (lost || failedPosition)) {
    const retry = review.getByRole('button', { name: 'Retry position only', exact: true });
    await expect(retry).toBeVisible();
    const beforeRepair = await snapshot(request);
    await retry.click();
    await expect(retry).toBeHidden();
    const afterRepair = await snapshot(request);
    expect(afterRepair.ids).toEqual(beforeRepair.ids);
    expect(afterRepair.agents).toEqual(beforeRepair.agents);
  }
  expect(commits).toBe(originalCommits);
  expect(staffing).toBe(lost ? 0 : 1);
  expect(ordinary).toBe(0);
  const after = await snapshot(request);
  expect(after.ids).toHaveLength(1);
  expect(after.agents.filter((item: string) => !before.agents.includes(item))).toEqual(
    lost ? [] : ['Portfolio Manager']
  );
  const homeID = after.ids[0];
  if (map) expect(after.layout.layout.positions[homeID]).toEqual(candidate);
  else expect(after.layout).toEqual(before.layout);
  const home = await (await request.get(`/api/workspaces/${homeID}`)).json();
  expect(home).toMatchObject({ id: homeID, name, kind: 'group' });
  expect(home.agent_instances || []).toHaveLength(lost ? 0 : 1);
  if (!lost)
    expect(home.agent_instances[0]).toMatchObject({
      name: 'Portfolio Manager',
      role_id: 'portfolio_manager'
    });
  expect(home.tasks || []).toHaveLength(0);
  expect(home.scheduled_tasks || []).toHaveLength(0);
  expect(home.assistant_program.state.key.plugin_id).toBe('music-project-management');
  expect(home.assistant_program.state.linked_project_ids || []).toHaveLength(0);
  if (sourceAfter)
    expect((await request.post('/api/plugins/music-project-management/disable')).ok()).toBeTruthy();
  await page.reload();
  review = await openReview(page, undefined, true);
  await expect(
    review.getByRole('heading', { name: 'Check confirmed Home', exact: true })
  ).toBeVisible();
  expect(commits).toBe(originalCommits);
  expect(staffing).toBe(lost ? 0 : 1);
  await review.getByRole('button', { name: 'Read saved status', exact: true }).click();
  await expect(review.getByRole('heading', { name: 'Home ready', exact: true })).toBeVisible();
  await expect(review).toContainText('Reading status did not resume staffing');
  expect(await snapshot(request)).toEqual(after);
  expect(commits).toBe(originalCommits);
  expect(staffing).toBe(lost ? 0 : 1);
  if (sourceAfter)
    expect((await request.post('/api/plugins/music-project-management/enable')).ok()).toBeTruthy();
  await review.getByRole('button', { name: 'Close', exact: true }).click();
  await expect(page.locator('#addFolderModal')).toBeHidden();
  review = await openReview(page);
  await expect(review.locator('[data-placement-field="name"]')).toHaveAttribute('readonly', '');
  await expect(review).toContainText('Name, members and position are preserved');
  await expect(
    review.getByRole('button', { name: 'Place & create on map', exact: true })
  ).toBeDisabled();
  await review.locator('[data-placement-action="confirm"]').click();
  await expect(review.getByRole('heading', { name: 'Home ready', exact: true })).toBeVisible();
  expect(commits).toBe(originalCommits + 1);
  expect(staffing).toBe(1);
  expect(ordinary).toBe(0);
  const final = await snapshot(request);
  expect(final.ids).toEqual(after.ids);
  expect(final.layout).toEqual(after.layout);
  expect(final.agents.filter((item: string) => !before.agents.includes(item))).toEqual([
    'Portfolio Manager'
  ]);
  if (!lost) expect(final).toEqual(after);
});
