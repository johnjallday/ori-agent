import { test, expect, type APIRequestContext } from '@playwright/test';

// Run only against an isolated exact-candidate narrated sandbox. Successful
// setup and Home proof are real server operations, never response fixtures.
const song = process.env.MUSIC_HOME_PLACEMENT_SONG || '';
const evidence = 'tasks/evidence-music-home-map-placement/group-2';
const collection = process.env.MUSIC_HOME_PLACEMENT_COLLECTION || '';

async function snapshot(request: APIRequestContext) {
  const body = await (await request.get('/api/workspaces')).json();
  const agents = await (await request.get('/api/agents')).json();
  const layout = await (await request.get('/api/workspace-map/layout')).json();
  return {
    workspaces: body.workspaces.sort((a: any, b: any) => a.id.localeCompare(b.id)),
    agents: agents.agents.sort((a: any, b: any) => a.name.localeCompare(b.name)),
    positions: layout.layout.positions || {}
  };
}

test('single-song reviewed empty Home is positioned before any project Open', async ({
  page,
  request
}) => {
  test.skip(
    !song,
    'MUSIC_HOME_PLACEMENT_SONG must name a synthetic song in a fresh narrated sandbox'
  );
  test.setTimeout(120_000);
  await page.setViewportSize({ width: 1440, height: 1000 });
  const before = await snapshot(request);
  expect(before.workspaces.filter((w: any) => w.kind === 'group')).toHaveLength(0);
  let setupPosts = 0;
  let inferencePosts = 0;
  page.on('request', r => {
    if (r.method() === 'POST' && /folder-digest\/offers\/[^/]+\/setup$/.test(r.url())) setupPosts++;
    if (r.method() === 'POST' && /\/(turns|chat|completions)$/.test(r.url())) inferencePosts++;
  });
  await page.goto('/?panel=today&folder=show');
  await page.locator('.folder-path-entry__input:visible').first().fill(song);
  await page.getByRole('button', { name: 'Use this path', exact: true }).first().click();
  const existingReview = page.getByRole('button', {
    name: 'Open Ori Music Demo review',
    exact: true
  });
  const first = page.getByRole('button', { name: /^(Set up|Prepare) Music Production Home$/ });
  await expect(first.or(existingReview)).toBeVisible({ timeout: 30_000 });
  if (await existingReview.isVisible()) await existingReview.click();
  await expect(first).toBeVisible({ timeout: 30_000 });
  await first.click();
  const review = page.locator('[data-source-creation-review]:visible');
  await expect(review).toBeVisible();
  await expect(review).toContainText('does not open its workspace');
  await review
    .getByRole('button', { name: 'Place & set up on map', exact: true })
    .scrollIntoViewIfNeeded();
  await page.screenshot({ path: `${evidence}/review.png` });
  expect(await snapshot(request)).toEqual(before);
  await review.getByRole('button', { name: 'Place & set up on map', exact: true }).click();
  const preview = page.locator('.ws-map-placement-preview');
  await expect(preview).toBeVisible();
  await page.screenshot({ path: `${evidence}/empty-frame-preview.png` });
  expect(await snapshot(request)).toEqual(before);
  expect(setupPosts).toBe(0);
  await page.keyboard.press('Escape');
  await expect(review).toBeVisible();
  expect(await snapshot(request)).toEqual(before);
  await review.getByRole('button', { name: 'Use automatic placement', exact: true }).click();
  expect(setupPosts).toBe(0);
  expect(await snapshot(request)).toEqual(before);
  await review.getByRole('button', { name: 'Place & set up on map', exact: true }).click();
  await page.keyboard.press('ArrowRight');
  const candidate = await preview.evaluate(element => ({
    x: Number.parseFloat((element as HTMLElement).style.left),
    y: Number.parseFloat((element as HTMLElement).style.top)
  }));
  if (process.env.MUSIC_HOME_PLACEMENT_CONFIRM === 'pointer')
    await page.locator('[data-ws-map-placement-confirm]').click();
  else await page.keyboard.press('Enter');
  await expect(review.getByRole('heading', { name: 'Home ready', exact: true })).toBeVisible({
    timeout: 60_000
  });
  await expect(review).not.toContainText('not saved');
  await page.screenshot({ path: `${evidence}/home-ready.png` });
  const after = await snapshot(request);
  const homes = after.workspaces.filter((w: any) => w.kind === 'group');
  expect(homes).toHaveLength(1);
  const home = homes[0];
  expect(after.workspaces.filter((w: any) => w.parent_id === home.id)).toHaveLength(0);
  expect(after.workspaces.length).toBe(before.workspaces.length + 1);
  expect(after.positions[home.id]).toEqual(candidate);
  expect(setupPosts).toBe(1);
  expect(inferencePosts).toBe(0);
  const digest = await (await request.get('/api/personal-assistant/folder-digest')).json();
  const offer = digest.folder_digest.offer;
  expect(offer.setup.stop_reason).toBe('handoff_ready');
  expect(offer.home_result).toMatchObject({
    home_id: home.id,
    state: 'ready',
    layout_subject_new: true
  });
  await page.locator('#personalAssistantClose').click();
  await expect(page.locator(`.ws-map-district[data-group-id="${home.id}"]`)).toBeVisible();
  await page.screenshot({ path: `${evidence}/positioned-empty-home.png` });
  await page.reload();
  expect((await snapshot(request)).positions).toEqual(after.positions);
});

test('collection automatic setup finishes a Home library without project buildings or a map', async ({
  page,
  request
}) => {
  test.skip(
    !collection,
    'MUSIC_HOME_PLACEMENT_COLLECTION names a synthetic collection in a fresh candidate sandbox'
  );
  test.setTimeout(120_000);
  await page.setViewportSize({ width: 720, height: 900 });
  const before = await snapshot(request);
  expect(before.workspaces.filter((w: any) => w.kind === 'group')).toHaveLength(0);
  await page.goto('/?panel=today&folder=show');
  await page.locator('.folder-path-entry__input:visible').first().fill(collection);
  await page.getByRole('button', { name: 'Use this path', exact: true }).first().click();
  await page
    .getByRole('button', { name: 'Review library setup', exact: true })
    .click({ timeout: 15_000 });
  await page.getByRole('button', { name: 'Set up', exact: true }).click({ timeout: 15_000 });
  const review = page.locator('[data-source-creation-review]:visible');
  await expect(review).toBeVisible();
  await expect(
    review.getByRole('button', { name: 'Place & set up on map', exact: true })
  ).toBeDisabled();
  await review.getByRole('button', { name: 'Use automatic placement', exact: true }).click();
  expect(await snapshot(request)).toEqual(before);
  await page.screenshot({ path: `${evidence}/collection-automatic-review.png` });
  const acceptedResponse = page.waitForResponse(r =>
    /folder-digest\/offers\/[^/]+\/setup$/.test(r.url())
  );
  await review.getByRole('button', { name: 'Set up Home', exact: true }).click();
  const accepted = await (await acceptedResponse).json();
  await expect(review.getByRole('heading', { name: 'Home ready', exact: true })).toBeVisible({
    timeout: 60_000
  });
  const after = await snapshot(request);
  const homes = after.workspaces.filter((w: any) => w.kind === 'group');
  expect(homes).toHaveLength(1);
  expect(after.workspaces.length).toBe(before.workspaces.length + 1);
  expect(after.workspaces.filter((w: any) => w.parent_id === homes[0].id)).toHaveLength(0);
  expect(after.positions).toEqual(before.positions);
  const digest = await (
    await request.get(`/api/personal-assistant/folder-digest?offer_id=${accepted.offer.id}`)
  ).json();
  expect(digest.folder_digest.offer.status).toBe('resolved');
  expect(digest.folder_digest.offer.home_result).toMatchObject({
    state: 'ready',
    home_id: homes[0].id
  });
  await page.screenshot({ path: `${evidence}/collection-home-ready.png` });
});
