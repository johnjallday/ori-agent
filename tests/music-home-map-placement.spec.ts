import { test, expect as baseExpect } from '@playwright/test';
import {
  musicHomeSnapshot as snapshot,
  musicHomeResourceCounts as resourceCounts,
  openMusicHomeReview
} from './helpers/music-home-placement';
import { promises as fs } from 'node:fs';
import path from 'node:path';
import os from 'node:os';
const expect = baseExpect.configure({ timeout: 60_000 });

// Run only against an isolated exact-candidate narrated sandbox. Successful
// setup and Home proof are real server operations, never response fixtures.
const song = process.env.MUSIC_HOME_PLACEMENT_SONG || '';
const fault = process.env.MUSIC_HOME_PLACEMENT_FAULT || '';
const evidence = fault
  ? `tasks/evidence-music-home-map-placement/group-4/${fault}`
  : 'tasks/evidence-music-home-map-placement/group-2';
const collection = process.env.MUSIC_HOME_PLACEMENT_COLLECTION || '';
const secondSong = process.env.MUSIC_HOME_PLACEMENT_SECOND_SONG || '';
test.describe.configure({ mode: 'serial' });
let restoreFault: (() => Promise<void>) | null = null;
test.afterEach(async () => {
  if (restoreFault) {
    await restoreFault();
    restoreFault = null;
  }
});

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
  let allPosts = 0;
  let positionWrites = 0;
  let faultInjected = false;
  let staleRead: any;
  let staleSelection = '';
  let stalePoint = { x: 0, y: 0 };
  let secondTab: any;
  let secondReview: any;
  let secondPosts = 0;
  let secondWrites = 0;
  let competingSnapshot: any;
  if (fault === 'stale') {
    const priorIDs = new Set(before.workspaces.map((w: any) => w.id));
    const newHomeRead = (url: string) =>
      /\/api\/workspaces\/[0-9a-f-]{36}$/.test(url) &&
      !priorIDs.has(new URL(url).pathname.split('/').pop());
    staleRead = page.waitForResponse(response => newHomeRead(response.url()));
    await page.route(/\/api\/workspaces\/[0-9a-f-]{36}$/, async route => {
      if (!faultInjected && newHomeRead(route.request().url())) {
        faultInjected = true;
        const newer = await page.evaluate(point => {
          const map = (window as any).OriWorkspaceMap;
          map.suspendDraftPlacement();
          (window as any).PersonalAssistantPanel?.close?.({ restoreFocus: false });
          const started = map.beginPlacement({
            token: 'newer-review',
            mode: 'draft',
            preview: { kind: 'group', name: 'A newer review' },
            initialCandidate: { x: point.x + 380, y: point.y - 190 }
          });
          return { started, selected: map.getSelectedId() };
        }, stalePoint);
        expect(newer.started).toBe(true);
        staleSelection = newer.selected;
      }
      await route.continue(); // delayed real read; no successful response is fabricated
    });
  }
  if (fault === 'accept')
    await page.route(/folder-digest\/offers\/[^/]+\/setup$/, async route => {
      const response = await route.fetch();
      expect(response.ok()).toBe(true); // real accepted operation; only its delivery is lost
      faultInjected = true;
      await route.abort('failed');
    });
  if (['layout', 'ack', 'occupied', 'deleted-home'].includes(fault))
    await page.route('**/api/workspace-map/layout', async route => {
      const request = route.request();
      const positions =
        request.method() === 'PATCH' &&
        request.postDataJSON()?.operations?.some((op: any) => op.op === 'set_positions');
      if (positions && !faultInjected) {
        faultInjected = true;
        if (fault === 'ack') {
          expect((await route.fetch()).ok()).toBe(true); // canonical layout committed, reply lost
          await route.abort('failed');
        } else
          await route.fulfill({
            status: 503,
            contentType: 'application/json',
            body: '{"error":"injected layout failure"}'
          });
      } else await route.continue();
    });
  page.on('request', r => {
    if (r.method() === 'POST') allPosts++;
    if (
      r.method() === 'PATCH' &&
      /\/workspace-map\/layout$/.test(r.url()) &&
      r.postDataJSON()?.operations?.some((op: any) => op.op === 'set_positions')
    )
      positionWrites++;
    if (r.method() === 'POST' && /folder-digest\/offers\/[^/]+\/setup$/.test(r.url())) setupPosts++;
    if (r.method() === 'POST' && /\/(turns|chat|completions)$/.test(r.url())) inferencePosts++;
  });
  const review = await openMusicHomeReview(page, song);
  await expect(review).toContainText('does not open its workspace');
  if (fault === 'two-tabs') {
    const source = (await (await request.get('/api/personal-assistant/folder-digest')).json())
      .folder_digest.offer;
    secondTab = await page.context().newPage();
    await secondTab.setViewportSize({ width: 1440, height: 1000 });
    secondTab.on('request', (r: any) => {
      if (r.method() === 'POST' && /folder-digest\/offers\/[^/]+\/setup$/.test(r.url()))
        secondPosts++;
      if (
        r.method() === 'PATCH' &&
        /\/workspace-map\/layout$/.test(r.url()) &&
        r.postDataJSON()?.operations?.some((op: any) => op.op === 'set_positions')
      )
        secondWrites++;
    });
    await secondTab.goto('/?panel=today&folder=show');
    if (source.conversation_id)
      await secondTab.evaluate(
        (id: string) => (window as any).PersonalAssistantConversation.resume(id),
        source.conversation_id
      );
    const launch = secondTab.getByRole('button', {
      name: /^(Set up|Prepare) Music Production Home$/
    });
    const reopen = secondTab.getByRole('button', {
      name: 'Open Ori Music Demo review',
      exact: true
    });
    await expect(launch.or(reopen)).toBeVisible();
    if (await reopen.isVisible()) await reopen.click();
    await launch.click();
    secondReview = secondTab.locator('[data-source-creation-review]:visible');
    await secondReview.getByRole('button', { name: 'Place & set up on map', exact: true }).click();
    for (let step = 0; step < 6; step++) await secondTab.keyboard.press('ArrowLeft');
    expect(secondPosts).toBe(0);
  }
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
  stalePoint = candidate;
  if (fault === 'competing-home') {
    const other = await page.context().newPage();
    await other.setViewportSize({ width: 1440, height: 1000 });
    await other.goto('/');
    const close = other.getByRole('button', { name: 'Close personal assistant', exact: true });
    if (await close.isVisible()) await close.click();
    await other.locator('#cockpitCreateWorkspaceBtn').click();
    await other.getByRole('button', { name: 'Set up manually', exact: true }).click();
    await other.locator('#workspaceCreatorKindGroup').check();
    const creator = other.locator('#addFolderModal');
    await creator
      .locator('.workspace-group-template-option', { hasText: 'Music Production Home' })
      .locator('input')
      .check();
    await creator.locator('#wizardNextBtn').click();
    await creator.locator('#wizardNextBtn').click();
    await expect(creator.locator('#wizardStep3')).toBeVisible();
    await creator.locator('#wizardNextBtn').click();
    const otherReview = creator.locator('#workspaceCreationReviewPanel');
    await otherReview.getByRole('button', { name: 'Use automatic placement', exact: true }).click();
    await otherReview.locator('[data-placement-action="confirm"]').click();
    await expect(
      otherReview.getByRole('heading', { name: 'Home ready', exact: true })
    ).toBeVisible();
    const home = (await snapshot(request)).workspaces.find((w: any) => w.kind === 'group');
    await other.goto(`/?view=map&move=${home.id}`);
    await expect(other.locator('[data-map-live]')).toContainText('Move is on');
    await other.locator('[data-ws-map-viewport]').focus();
    for (let step = 0; step < 8; step++) await other.keyboard.press('ArrowDown');
    await other.keyboard.press('Enter');
    await expect.poll(async () => (await snapshot(request)).positions[home.id]).toBeTruthy();
    competingSnapshot = await snapshot(request);
    await other.close();
    faultInjected = true;
  }
  if (fault === 'disabled-provider') {
    expect((await request.post('/api/plugins/music-project-management/disable')).ok()).toBe(true);
    competingSnapshot = before;
    faultInjected = true;
  }
  if (fault === 'missing-model') {
    expect(
      (await request.post('/api/settings/system-model', { data: { provider: '', model: '' } })).ok()
    ).toBe(true);
    competingSnapshot = before;
    faultInjected = true;
  }
  if (fault === 'partial') {
    const sandbox = await fs.realpath(path.resolve(song, '../..'));
    const temporaryRoot = await fs.realpath(os.tmpdir());
    expect(sandbox.startsWith(`${temporaryRoot}/ori-demo.`)).toBe(true);
    expect(await fs.readFile(path.join(sandbox, '.ori-demo-owner'), 'utf8')).toContain(
      `worktree=${process.cwd()}`
    );
    const agentDir = await fs.realpath(path.join(sandbox, 'Ori Workspaces', 'Agents'));
    expect(agentDir.startsWith(`${sandbox}/`)).toBe(true);
    const mode = (await fs.stat(agentDir)).mode & 0o777;
    restoreFault = () => fs.chmod(agentDir, mode);
    await fs.chmod(agentDir, 0o550); // fail real staffing after the real Home commit
    faultInjected = true;
  }
  await page.locator('[data-ws-map-viewport]').focus();
  if (process.env.MUSIC_HOME_PLACEMENT_CONFIRM === 'pointer') {
    const box = await preview.boundingBox();
    expect(box).not.toBeNull();
    const hovering = await snapshot(request);
    await page.mouse.move(box!.x + 1, box!.y + 1);
    expect(await snapshot(request)).toEqual(hovering);
    expect(setupPosts).toBe(0);
    // A real background click, not just the toolbar's pointer alternative.
    await page.mouse.click(box!.x + 1, box!.y + 1);
  } else {
    await page.keyboard.press('Enter');
    await page.keyboard.press('Enter');
  }
  if (['competing-home', 'disabled-provider', 'missing-model'].includes(fault)) {
    const sourcePanel = page.locator('#personalAssistantPanel');
    await expect(sourcePanel).toBeVisible();
    await expect(sourcePanel).toContainText(
      /changed|unavailable|Review the current setup|system model/i
    );
    expect(await snapshot(request)).toEqual(competingSnapshot);
    expect(setupPosts).toBe(1);
    expect(positionWrites).toBe(0);
    expect(faultInjected).toBe(true);
    await page.screenshot({ path: `${evidence}/changed-review.png` });
    await page.reload();
    expect((await snapshot(request)).positions).toEqual(competingSnapshot.positions);
    expect(positionWrites).toBe(0);
    return;
  }
  if (fault === 'stale') {
    await (await staleRead).finished();
    await page.evaluate(
      () => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)))
    );
    await expect(page.locator('.ws-map-placement-preview')).toContainText('A newer review');
    await expect(page.locator('[data-ws-map-viewport]')).toBeFocused();
    expect(await page.evaluate(() => (window as any).OriWorkspaceMap.getSelectedId())).toBe(
      staleSelection
    );
    const current = await snapshot(request);
    const homes = current.workspaces.filter((w: any) => w.kind === 'group');
    expect(homes).toHaveLength(1);
    expect(current.workspaces.filter((w: any) => w.parent_id === homes[0].id)).toHaveLength(0);
    expect(current.positions).toEqual(before.positions);
    expect(setupPosts).toBe(1);
    expect(positionWrites).toBe(0);
    expect(faultInjected).toBe(true);
    await page.screenshot({ path: `${evidence}/newer-editor-preserved.png` });
    expect(
      await page.evaluate(() => (window as any).OriWorkspaceMap.endPlacement('newer-review'))
    ).toBe(true);
    await page.reload();
    await expect(page.locator(`.ws-map-district[data-group-id="${homes[0].id}"]`)).toBeVisible();
    expect((await snapshot(request)).positions).toEqual(before.positions);
    expect(positionWrites).toBe(0);
    return;
  }
  if (fault === 'partial') {
    const readOffer = async () =>
      (await (await request.get('/api/personal-assistant/folder-digest')).json()).folder_digest
        .offer;
    await expect.poll(async () => (await readOffer())?.setup?.status).toBe('stopped');
    const offer = await readOffer();
    expect(offer.setup.stop_reason).toBe('failed');
    expect(offer.home_result).toMatchObject({
      state: 'partial',
      created_by_this_operation: true,
      layout_subject_new: false
    });
    const partial = await snapshot(request);
    expect(partial.workspaces.length).toBe(before.workspaces.length + 1);
    expect(
      partial.workspaces.filter((w: any) => w.parent_id === offer.home_result.home_id)
    ).toHaveLength(0);
    // Staffing can leave its newly allocated agent visible before its durable
    // role assignment fails. Preserve it honestly; observation must not add more.
    expect(partial.agents.length).toBeGreaterThanOrEqual(before.agents.length);
    expect(partial.agents.length).toBeLessThanOrEqual(before.agents.length + 1);
    expect(partial.positions).toEqual(before.positions);
    await expect(page.getByRole('button', { name: 'Try again', exact: true })).toBeVisible();
    await expect(
      page.getByRole('button', { name: 'Retry position only', exact: true })
    ).toHaveCount(0);
    await page.screenshot({ path: `${evidence}/partial-home.png` });
    await readOffer();
    await page.reload();
    await expect(page.getByRole('button', { name: 'Try again', exact: true })).toBeVisible();
    const observed = await snapshot(request);
    expect(observed.positions).toEqual(before.positions);
    expect(observed.workspaces.length).toBe(partial.workspaces.length);
    expect(observed.agents.length).toBe(partial.agents.length);
    expect(setupPosts).toBe(1);
    expect(positionWrites).toBe(0);
    expect(faultInjected).toBe(true);
    return;
  }
  if (fault === 'accept') {
    await expect(review).toContainText('Confirmation delivery is unknown');
    await expect(page.locator('#personalAssistantFolderOfferNote')).toBeHidden();
    await expect(page.locator('#personalAssistantFolderReviewStatus')).toBeHidden();
    await page.screenshot({ path: `${evidence}/unknown-confirmation.png` });
    await review.getByRole('button', { name: 'Read saved status', exact: true }).click();
  }
  await expect(review.getByRole('heading', { name: 'Home ready', exact: true })).toBeVisible({
    timeout: 60_000
  });
  let after = await snapshot(request);
  if (fault === 'two-tabs') {
    const home = after.workspaces.find((w: any) => w.kind === 'group');
    const domainBefore = await resourceCounts(request, home.id);
    const original = (await (await request.get('/api/personal-assistant/folder-digest')).json())
      .folder_digest.offer;
    await secondTab.keyboard.press('Enter');
    await expect(
      secondReview.getByRole('heading', { name: 'Home ready', exact: true })
    ).toBeVisible();
    await expect(secondReview).toContainText('Existing Home kept in place');
    expect(secondPosts).toBe(1);
    expect(secondWrites).toBe(0);
    expect(await resourceCounts(request, home.id)).toEqual(domainBefore);
    expect((await snapshot(request)).positions).toEqual(after.positions);
    const observed = (
      await (
        await request.get(`/api/personal-assistant/folder-digest?offer_id=${original.id}`)
      ).json()
    ).folder_digest.offer;
    expect(observed.setup).toEqual(original.setup);
    expect(observed.home_result).toEqual(original.home_result);
    await secondTab.screenshot({ path: `${evidence}/second-tab-observes.png` });
    await secondTab.close();
    faultInjected = true;
  }
  if (fault === 'deleted-home') {
    const original = after.workspaces.find((w: any) => w.kind === 'group');
    const summary = await (
      await request.get(`/api/workspaces/${original.id}/assistant-program`)
    ).json();
    const removal = await request.post(
      `/api/workspaces/${original.id}/assistant-program/remove-home/review`,
      {
        data: { state_revision: summary.state_revision }
      }
    );
    expect(removal.ok(), await removal.text()).toBe(true);
    const removed = await request.post(
      `/api/workspaces/${original.id}/assistant-program/remove-home/commit`,
      {
        data: { token: (await removal.json()).token }
      }
    );
    expect(removed.ok(), await removed.text()).toBe(true);
    await expect
      .poll(async () => (await snapshot(request)).workspaces.some((w: any) => w.id === original.id))
      .toBe(false);
    const rosterResponse = await request.post('/api/workspaces/template-agent-plan', {
      data: { group_roster: true, group_name: original.name }
    });
    expect(rosterResponse.ok(), await rosterResponse.text()).toBe(true);
    const roster = await rosterResponse.json();
    const manager = roster.agents[0];
    const decoy = await request.post('/api/workspaces', {
      data: {
        name: original.name,
        kind: 'group',
        group_roster: true,
        create_template_agents: true,
        template_agent_review: {
          version: 1,
          plan_revision: roster.revision,
          expectations: [{ index: 0, name: manager.name, action: manager.action }]
        }
      }
    });
    expect(decoy.ok(), await decoy.text()).toBe(true);
    const afterRemoval = await snapshot(request);
    await review.getByRole('button', { name: 'Retry position only', exact: true }).click();
    await expect(review).toContainText('no longer has verified placement authority');
    await expect(review.getByRole('heading', { name: 'Home ready', exact: true })).toBeHidden();
    await expect(
      review.getByRole('button', { name: 'Retry position only', exact: true })
    ).toBeHidden();
    await review.getByRole('button', { name: 'Read saved status', exact: true }).click();
    await expect(review).toContainText('original result could not be verified');
    expect(await snapshot(request)).toEqual(afterRemoval);
    expect(setupPosts).toBe(1);
    expect(positionWrites).toBe(1);
    await page.screenshot({ path: `${evidence}/deleted-home-not-replaced.png` });
    await page.reload();
    expect(await snapshot(request)).toEqual(afterRemoval);
    expect(setupPosts).toBe(1);
    expect(positionWrites).toBe(1);
    return;
  }
  if (['accept', 'layout', 'ack', 'occupied'].includes(fault)) {
    const home = after.workspaces.find((w: any) => w.kind === 'group');
    await expect(review).toContainText('position not saved');
    await page.screenshot({ path: `${evidence}/position-failed.png` });
    const domainBefore = await resourceCounts(request, home.id);
    const postsBefore = allPosts;
    expect(domainBefore.roots).toHaveLength(1);
    expect(domainBefore.projects).toHaveLength(1);
    if (fault === 'occupied') {
      const response = await request.patch('/api/workspace-map/layout', {
        data: {
          operations: [{ op: 'set_positions', positions: { [before.workspaces[0].id]: candidate } }]
        }
      });
      expect(response.ok()).toBe(true);
    }
    await review.getByRole('button', { name: 'Retry position only', exact: true }).click();
    if (fault === 'occupied') {
      await expect(review).toContainText('requested point is not available');
      await expect(review.getByRole('button', { name: 'Move group', exact: true })).toBeVisible();
      expect(positionWrites).toBe(1);
    } else {
      await expect(review).toContainText('requested position is saved');
      expect(positionWrites).toBe(fault === 'layout' ? 2 : 1);
    }
    expect(await resourceCounts(request, home.id)).toEqual(domainBefore);
    expect(allPosts).toBe(postsBefore);
    after = await snapshot(request);
    await page.screenshot({ path: `${evidence}/position-repair.png` });
  } else await expect(review).not.toContainText('not saved');
  if (fault) expect(faultInjected).toBe(true);
  await page.screenshot({ path: `${evidence}/home-ready.png` });
  const homes = after.workspaces.filter((w: any) => w.kind === 'group');
  expect(homes).toHaveLength(1);
  const home = homes[0];
  expect(after.workspaces.filter((w: any) => w.parent_id === home.id)).toHaveLength(0);
  expect(after.workspaces.length).toBe(before.workspaces.length + 1);
  if (fault === 'occupied') expect(after.positions[home.id]).toBeUndefined();
  else expect(after.positions[home.id]).toEqual(candidate);
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
  const writesBeforeReload = positionWrites;
  await page.reload();
  await expect(page.locator(`.ws-map-district[data-group-id="${home.id}"]`)).toBeVisible();
  expect((await snapshot(request)).positions).toEqual(after.positions);
  expect(positionWrites).toBe(writesBeforeReload);
});

test('single-song chat hand-off: the manager offers the song and it stands where clicked inside the Home', async ({
  page,
  request
}) => {
  test.skip(
    !song || !!secondSong || !!fault,
    'runs after the plain single-song case; the later case opens this song itself'
  );
  test.setTimeout(180_000);
  await page.setViewportSize({ width: 1440, height: 1000 });
  const chatEvidence = 'tasks/evidence-home-manager-chat-handoff/e2e';
  const before = await snapshot(request);
  const home = before.workspaces.find((w: any) => w.kind === 'group');
  expect(home).toBeTruthy();
  let setupPosts = 0;
  let modelPosts = 0;
  let sessionPosts = 0;
  page.on('request', r => {
    if (r.method() !== 'POST') return;
    if (/folder-digest\/offers\/[^/]+\/setup$/.test(r.url())) setupPosts++;
    if (/\/(turns|chat|completions)$/.test(r.url())) modelPosts++;
    if (/\/api\/sessions$/.test(r.url())) sessionPosts++;
  });
  await page.goto(`/workspaces/${home.folder_slug}?mode=map&panel=assistant`);
  const offers = page.locator('#personalAssistantHomeOffer');
  const offer = offers.locator('.pa-home-offer').first();
  await expect(offer).toBeVisible();
  await expect(offer).toContainText(
    'I listed Ori Music Demo in Music Production Home. Want me to open it and hire the REAPER Assistant?'
  );
  await expect(offer.locator('.personal-assistant-message__author')).toContainText(
    'Portfolio Manager'
  );
  // The drawer's request is consumed once; Map mode stays.
  await expect(page).toHaveURL(new RegExp(`/workspaces/${home.folder_slug}\\?mode=map$`));
  const input = page.locator('#personalAssistantInput');
  const send = page.locator('#personalAssistantSend');
  await expect(send).toHaveText('Use as brief');
  await expect(input).toHaveAttribute(
    'placeholder',
    /what the REAPER Assistant should do with Ori Music Demo/
  );
  const brief = 'clean up the drums and bounce stems';
  await input.fill(brief);
  await send.click();
  await expect(offer.locator('textarea')).toHaveValue(brief);
  await expect(page.locator('[data-message-role="user"]').last()).toContainText(brief);
  await expect(input).toHaveValue('');
  await page.screenshot({ path: `${chatEvidence}/offer-with-brief.png` });
  expect({ setupPosts, sessionPosts, modelPosts }).toEqual({
    setupPosts: 0,
    sessionPosts: 0,
    modelPosts: 0
  });

  const press = () =>
    offers.getByRole('button', { name: 'Open Ori Music Demo & hire', exact: true }).click();
  const preview = page.locator('.ws-map-placement-preview');
  const instruction = page.locator('[data-ws-map-placement-instruction]');
  // Escape is back to the conversation: the card and its brief stay; nothing ran.
  await press();
  await expect(preview).toBeVisible();
  await expect(instruction).toContainText(
    'Open Ori Music Demo here with a click on open ground inside Music Production Home'
  );
  // The banner's copy keeps a readable width beside its three actions.
  const copy = page.locator('.ws-map-placement-copy');
  expect((await copy.boundingBox())?.width ?? 0).toBeGreaterThan(200);
  // The Home's Detachment toolbar steps aside while the banner is up.
  await expect(page.locator('.ws-cmd-map-group-bar')).toBeHidden();
  await page.screenshot({ path: `${chatEvidence}/placement-banner.png` });
  await page.keyboard.press('Escape');
  await expect(offer.locator('textarea')).toHaveValue(brief);
  expect(setupPosts).toBe(0);
  // A point outside the Home is not a candidate.
  await press();
  await expect(preview).toBeVisible();
  const canvas = page.locator('[data-ws-map-viewport]:visible').first();
  const box = await canvas.boundingBox();
  if (!box) throw new Error('the Home map is not drawn');
  await page.mouse.move(box.x + 12, box.y + box.height - 12);
  await expect(instruction).toContainText('That point is outside Music Production Home.');
  await page.keyboard.press('Enter');
  expect(setupPosts).toBe(0);
  await page.screenshot({ path: `${chatEvidence}/outside-refused.png` });
  await page.keyboard.press('Escape');
  // The suggested point inside the Home, confirmed by keyboard.
  await press();
  await expect(preview).toBeVisible();
  await expect(instruction).not.toContainText('outside');
  const candidate = await preview.evaluate(element => ({
    x: Number.parseFloat((element as HTMLElement).style.left),
    y: Number.parseFloat((element as HTMLElement).style.top)
  }));
  await page.keyboard.press('Enter');
  await expect.poll(() => setupPosts).toBe(1);
  // The drawer is back on the running card, then the manager's receipt.
  await expect(page.locator('#personalAssistantPanel')).toBeVisible();
  await expect(offers).toContainText('Portfolio Manager opened it', { timeout: 90_000 });
  await expect(offers).toContainText('Ori Music Demo is open');
  await page.screenshot({ path: `${chatEvidence}/receipt.png` });
  const after = await snapshot(request);
  const child = after.workspaces.find((w: any) => w.parent_id === home.id);
  expect(child).toBeTruthy();
  await expect(
    offers.getByRole('link', { name: 'Open Ori Music Demo', exact: true })
  ).toHaveAttribute('href', `/workspaces/${child.folder_slug}`);
  await expect.poll(async () => (await snapshot(request)).positions[child.id]).toEqual(candidate);
  const resource = await (await request.get(`/api/workspaces/${child.id}`)).json();
  expect(resource.parent_id).toBe(home.id);
  expect(JSON.stringify(resource)).toContain(brief);
  await expect(page.locator(`.ws-map-tile[data-ws-id="${child.id}"]`)).toHaveClass(/is-selected/);
  expect(setupPosts).toBe(1);
  expect(sessionPosts).toBe(0);
  expect(modelPosts).toBe(0);
  // The Home page's card no longer offers the opened song.
  await page.goto(`/workspaces/${home.folder_slug}/assistant`);
  await expect(page.locator('#homeSongSuggestions [data-offer-id]')).toHaveCount(0);
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
  // Go to lands on the Home's map with its manager offering the song in the
  // drawer; this case keeps automatic placement from the banner.
  await expect(page).toHaveURL(new RegExp(`/workspaces/${home.folder_slug}\\?mode=map$`));
  const secondSuggestion = page.locator('#personalAssistantHomeOffer [data-offer-id]').first();
  await expect(secondSuggestion).toBeVisible();
  const secondOfferID = await secondSuggestion.getAttribute('data-offer-id');
  await page
    .locator('#personalAssistantHomeOffer')
    .getByRole('button', { name: 'Open Second Song & hire', exact: true })
    .click();
  await page.getByRole('button', { name: 'Use automatic placement', exact: true }).click();
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
  // An automatic district can sit beside HQ; one step right is not necessarily
  // a free site. Deliberately move into open ground rather than bypass collision.
  for (let step = 0; step < 16; step++)
    await page.locator('[data-ws-map-viewport]').press('ArrowUp');
  await page.locator('[data-ws-map-viewport]').press('Enter');
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

const collectionOnMap = process.env.MUSIC_HOME_PLACEMENT_COLLECTION_MAP === '1';
test(`collection ${collectionOnMap ? 'map' : 'automatic'} setup finishes a Home library without project buildings`, async ({
  page,
  request
}) => {
  test.skip(
    !collection,
    'MUSIC_HOME_PLACEMENT_COLLECTION names a synthetic collection in a fresh candidate sandbox'
  );
  test.setTimeout(120_000);
  await page.setViewportSize({ width: collectionOnMap ? 1440 : 720, height: 1000 });
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
  await expect(review.getByRole('textbox', { name: 'Group name', exact: true })).toHaveValue(
    'Music Production Home'
  );
  await expect(review.getByRole('textbox', { name: 'Purpose', exact: true })).toHaveCount(0);
  const place = review.getByRole('button', { name: 'Place & set up on map', exact: true });
  let candidate: { x: number; y: number } | undefined;
  if (collectionOnMap) {
    await place.click();
    const canvas = page.locator('[data-ws-map-viewport]');
    for (let step = 0; step < 4; step++) await canvas.press('ArrowDown');
    candidate = await page.locator('[data-ws-map-placement-preview]').evaluate(element => ({
      x: parseFloat((element as HTMLElement).style.left),
      y: parseFloat((element as HTMLElement).style.top)
    }));
  } else {
    await expect(place).toBeDisabled();
    await review.getByRole('button', { name: 'Use automatic placement', exact: true }).click();
  }
  expect(await snapshot(request)).toEqual(before);
  await page.screenshot({
    path: `${evidence}/collection-${collectionOnMap ? 'map' : 'automatic'}-review.png`
  });
  const acceptedResponse = page.waitForResponse(r =>
    /folder-digest\/offers\/[^/]+\/setup$/.test(r.url())
  );
  if (collectionOnMap) await page.locator('[data-ws-map-viewport]').press('Enter');
  else await review.getByRole('button', { name: 'Set up Home', exact: true }).click();
  const accepted = await (await acceptedResponse).json();
  await expect(review.getByRole('heading', { name: 'Home ready', exact: true })).toBeVisible({
    timeout: 60_000
  });
  const after = await snapshot(request);
  const homes = after.workspaces.filter((w: any) => w.kind === 'group');
  expect(homes).toHaveLength(1);
  expect(after.workspaces.length).toBe(before.workspaces.length + 1);
  expect(after.workspaces.filter((w: any) => w.parent_id === homes[0].id)).toHaveLength(0);
  if (collectionOnMap) expect(after.positions[homes[0].id]).toEqual(candidate);
  else expect(after.positions).toEqual(before.positions);
  expect((await resourceCounts(request, homes[0].id)).projects).toHaveLength(5);
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
