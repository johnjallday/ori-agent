import { test, expect as baseExpect, type APIRequestContext } from '@playwright/test';
const expect = baseExpect.configure({ timeout: 60_000 });

// Run only against an isolated exact-candidate narrated sandbox. Successful
// setup and Home proof are real server operations, never response fixtures.
const song = process.env.MUSIC_HOME_PLACEMENT_SONG || '';
const evidence = 'tasks/evidence-music-home-map-placement/group-2';
const collection = process.env.MUSIC_HOME_PLACEMENT_COLLECTION || '';
const secondSong = process.env.MUSIC_HOME_PLACEMENT_SECOND_SONG || '';
test.describe.configure({ mode: 'serial' });

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
  for (let step = 0; step < 4; step++) await page.keyboard.press('ArrowDown');
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

test('existing Home Show and Move preserve later project anchors and canonical membership', async ({
  page,
  request
}) => {
  test.skip(!secondSong, 'requires the preceding single-song case and a second synthetic song');
  test.setTimeout(120_000);
  await page.setViewportSize({ width: 1440, height: 1000 });
  const before = await snapshot(request);
  const home = before.workspaces.find((w: any) => w.kind === 'group');
  expect(home).toBeTruthy();
  await page.goto(`/?view=map&show_on_map=${home.id}`);
  await page.waitForFunction(
    id => (window as any).OriWorkspaceMap?.getSelectedId() === id,
    home.id
  );
  expect((await snapshot(request)).positions).toEqual(before.positions);
  await expect(
    page.locator(`.ws-map-district[data-group-id="${home.id}"] .ws-map-district-name`)
  ).toContainText('Music Production Home');
  await page.screenshot({
    path: 'tasks/evidence-music-home-map-placement/group-3/show-empty-home.png'
  });
  await page.goto(`/?view=map&move=${home.id}`);
  await expect(page.locator('[data-map-live]')).toContainText(
    'Move is on for Music Production Home'
  );
  await page.keyboard.press('ArrowRight');
  await page.keyboard.press('Escape');
  expect((await snapshot(request)).positions).toEqual(before.positions);
  await page.goto(`/?view=map&move=${home.id}`);
  await expect(page.locator('[data-map-live]')).toContainText(
    'Move is on for Music Production Home'
  );
  await page.keyboard.press('ArrowRight');
  await page.keyboard.press('Enter');
  await expect
    .poll(async () => (await snapshot(request)).positions[home.id])
    .not.toEqual(before.positions[home.id]);
  const moved = await snapshot(request);
  await page.screenshot({
    path: 'tasks/evidence-music-home-map-placement/group-3/moved-empty-home.png'
  });
  await page.goto(`/workspaces/${home.folder_slug}/assistant`);
  const firstSuggestion = page.locator('#homeSongSuggestions [data-offer-id]').first();
  await expect(firstSuggestion).toBeVisible();
  const firstOfferID = await firstSuggestion.getAttribute('data-offer-id');
  await page
    .locator('#homeSongSuggestions')
    .getByRole('button', { name: 'Open Ori Music Demo & hire', exact: true })
    .click();
  await expect
    .poll(
      async () =>
        (
          await (
            await request.get(`/api/personal-assistant/folder-digest?offer_id=${firstOfferID}`)
          ).json()
        ).folder_digest.offer.status
    )
    .toBe('resolved');
  await expect
    .poll(
      async () =>
        (await snapshot(request)).workspaces.filter((w: any) => w.parent_id === home.id).length
    )
    .toBe(1);
  await page.goto('/');
  await page.waitForFunction(
    id => (window as any).OriWorkspaceMap?.getLayoutState()?.positions[id],
    home.id
  );
  const first = await snapshot(request);
  expect(first.positions[home.id]).toEqual(moved.positions[home.id]);
  const homeFrame = page.locator(`.ws-map-district[data-group-id="${home.id}"]`);
  expect(
    await homeFrame.evaluate(el => ({
      x: parseFloat((el as HTMLElement).style.left),
      y: parseFloat((el as HTMLElement).style.top)
    }))
  ).toEqual(moved.positions[home.id]);
  const child = first.workspaces.find((w: any) => w.parent_id === home.id);
  expect(child).toBeTruthy();
  const firstTile = page.locator(`.ws-map-tile[data-ws-id="${child.id}"]`);
  await expect(firstTile).toBeVisible();
  const firstAnchor = await firstTile.evaluate(el => ({
    x: (el as HTMLElement).style.left,
    y: (el as HTMLElement).style.top
  }));
  await page.goto('/?panel=today&folder=show');
  await page.locator('.folder-path-entry__input:visible').first().fill(secondSong);
  await page.getByRole('button', { name: 'Use this path', exact: true }).first().click();
  await page
    .getByRole('button', { name: /Go to Music Production Home/ })
    .first()
    .click();
  const secondSuggestion = page.locator('#homeSongSuggestions [data-offer-id]').first();
  await expect(secondSuggestion).toBeVisible();
  const secondOfferID = await secondSuggestion.getAttribute('data-offer-id');
  await page
    .locator('#homeSongSuggestions')
    .getByRole('button', { name: 'Open Second Song & hire', exact: true })
    .click();
  const readSecond = async () =>
    (
      await (
        await request.get(`/api/personal-assistant/folder-digest?offer_id=${secondOfferID}`)
      ).json()
    ).folder_digest.offer;
  await expect
    .poll(async () => {
      const offer = await readSecond();
      return offer.status === 'resolved' || offer.setup?.status === 'stopped';
    })
    .toBe(true);
  if ((await readSecond()).status !== 'resolved') {
    // The existing asynchronous library brief can advance the catalog revision
    // during Open. Exercise its explicit source-owned recovery, never treat a
    // vanished suggestion / early workspace as successful completed staffing.
    expect((await readSecond()).setup.stop_reason).toBe('failed');
    await page.goto('/');
    await page.getByRole('button', { name: 'Try again', exact: true }).click();
  }
  await expect.poll(async () => (await readSecond()).status).toBe('resolved');
  await expect
    .poll(
      async () =>
        (await snapshot(request)).workspaces.filter((w: any) => w.parent_id === home.id).length
    )
    .toBe(2);
  await page.goto('/');
  await expect(page.locator(`.ws-map-district[data-group-id="${home.id}"]`)).toBeVisible();
  const second = await snapshot(request);
  expect(second.positions[home.id]).toEqual(moved.positions[home.id]);
  expect(
    await homeFrame.evaluate(el => ({
      x: parseFloat((el as HTMLElement).style.left),
      y: parseFloat((el as HTMLElement).style.top)
    }))
  ).toEqual(moved.positions[home.id]);
  for (const [id, point] of Object.entries(first.positions))
    expect(second.positions[id]).toEqual(point);
  expect(second.workspaces.filter((w: any) => w.kind === 'group')).toHaveLength(1);
  expect(second.agents.length).toBe(first.agents.length);
  for (const project of second.workspaces.filter((w: any) => w.parent_id === home.id)) {
    const resource = await (await request.get(`/api/workspaces/${project.id}`)).json();
    expect(resource.agent_instances).toHaveLength(1);
  }
  expect(
    await firstTile.evaluate(el => ({
      x: (el as HTMLElement).style.left,
      y: (el as HTMLElement).style.top
    }))
  ).toEqual(firstAnchor);
  const closePanel = page.getByRole('button', { name: 'Close personal assistant', exact: true });
  if (await closePanel.isVisible()) await closePanel.click();
  await page.screenshot({
    path: 'tasks/evidence-music-home-map-placement/group-3/two-real-projects.png'
  });
  await page.reload();
  expect((await snapshot(request)).positions).toEqual(second.positions);
});

test('collection library Opens keep the explicitly positioned Home and earlier child', async ({
  page,
  request
}) => {
  test.skip(
    process.env.MUSIC_HOME_PLACEMENT_LIBRARY !== '1',
    'requires an already prepared catalog-only collection sandbox'
  );
  test.setTimeout(120_000);
  await page.setViewportSize({ width: 1440, height: 1000 });
  const before = await snapshot(request);
  const home = before.workspaces.find((w: any) => w.kind === 'group');
  const priorChildren = before.workspaces.filter((w: any) => w.parent_id === home.id).length;
  await page.goto(`/?view=map&move=${home.id}`);
  await expect(page.locator('[data-map-live]')).toContainText('Move is on');
  await page.keyboard.press('ArrowRight');
  await page.keyboard.press('Enter');
  await expect.poll(async () => (await snapshot(request)).positions[home.id]).toBeTruthy();
  const positioned = await snapshot(request);
  const library = await (
    await request.get(`/api/workspaces/${home.id}/assistant-program/library/projects`)
  ).json();
  const unopened = library.rows.filter((entry: any) => entry.connection === 'catalog_only');
  expect(unopened.length).toBeGreaterThanOrEqual(2);
  let earlier: any = null;
  for (const entry of unopened.slice(0, 2)) {
    const priorIDs = new Set((await snapshot(request)).workspaces.map((w: any) => w.id));
    await page.goto(`/workspaces/${home.folder_slug}/assistant#projectLibraryPanel`);
    const openedResponse = page.waitForResponse(
      response =>
        response.url().endsWith(`/projects/${entry.id}/open`) &&
        response.request().method() === 'POST'
    );
    await page.locator(`#projectLibraryPanel [data-library-open="${entry.id}"]`).first().click();
    expect((await openedResponse).ok()).toBe(true);
    const created = (await snapshot(request)).workspaces.filter((w: any) => !priorIDs.has(w.id));
    expect(created).toHaveLength(1);
    const result = { workspace_id: created[0].id };
    const resource = await (await request.get(`/api/workspaces/${result.workspace_id}`)).json();
    expect(resource.parent_id).toBe(home.id);
    await page.goto('/');
    const frame = page.locator(`.ws-map-district[data-group-id="${home.id}"]`);
    await expect(frame).toBeVisible();
    expect(
      await frame.evaluate(el => ({
        x: parseFloat((el as HTMLElement).style.left),
        y: parseFloat((el as HTMLElement).style.top)
      }))
    ).toEqual(positioned.positions[home.id]);
    const after = await snapshot(request);
    for (const [id, point] of Object.entries(positioned.positions))
      expect(after.positions[id]).toEqual(point);
    if (earlier)
      expect(
        await page
          .locator(`.ws-map-tile[data-ws-id="${earlier.id}"]`)
          .evaluate(el => ({ x: (el as HTMLElement).style.left, y: (el as HTMLElement).style.top }))
      ).toEqual(earlier.anchor);
    earlier = {
      id: result.workspace_id,
      anchor: await page
        .locator(`.ws-map-tile[data-ws-id="${result.workspace_id}"]`)
        .evaluate(el => ({ x: (el as HTMLElement).style.left, y: (el as HTMLElement).style.top }))
    };
  }
  const after = await snapshot(request);
  expect(after.workspaces.filter((w: any) => w.parent_id === home.id)).toHaveLength(
    priorChildren + 2
  );
  expect(after.agents.length).toBeLessThanOrEqual(before.agents.length + 1);
  for (const agent of before.agents)
    expect(after.agents.some((a: any) => a.name === agent.name)).toBe(true);
  await page.screenshot({
    path: 'tasks/evidence-music-home-map-placement/group-3/library-two-projects.png'
  });
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
