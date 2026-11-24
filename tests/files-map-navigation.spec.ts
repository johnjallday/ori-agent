import { test, expect, type Request } from '@playwright/test';
import { readFileSync, mkdirSync, writeFileSync, utimesSync, existsSync, chmodSync } from 'node:fs';
import { join } from 'node:path';
import { seedQuestionFolder } from './helpers/commander-question-fixture';
import { installLocalCdn } from './helpers/offline-cdn';
import { expectDestination, goToFiles, goToMap } from './helpers/home-destination';

const fixture = process.env.ORI_SEARCH_FIRST_FIXTURE;
const evidenceRoot = 'tasks/evidence-files-map-navigation';
test.skip(
  !fixture || process.env.ORI_SEARCH_FIRST_SCENARIO !== 'navigation',
  'requires a marked disposable profile'
);
test('distinguish duplicate copies by location and date before acting', async ({
  page,
  context,
  request,
  baseURL,
  browserName
}) => {
  const evidence = join(evidenceRoot, browserName);
  expect(new URL(baseURL!).hostname).toBe('localhost');
  expect(readFileSync(join(fixture!, '.search-first-fixture'), 'utf8')).toBe('synthetic-v1\n');
  expect(readFileSync(join(fixture!, '.search-first-test-url'), 'utf8').trim()).toBe(baseURL);
  // Exercise navigation on an established assistant fixture. First-run model
  // verification belongs to onboarding, not the filename query/controller.
  await installLocalCdn(page);
  // This navigation regression starts with independent search-only grants, as
  // on an upgraded installation. Create them before workspace onboarding;
  // established-profile workspace review has its own acceptance journeys.
  await page.goto('/search');
  for (const kind of ['Documents', 'Downloads']) {
    if (kind === 'Downloads') await page.getByRole('button', { name: /Add folder/ }).click();
    await page.getByRole('button', { name: kind, exact: true }).click();
    await page.getByRole('dialog').getByRole('button', { name: 'Allow search' }).click();
    await expect(page.locator('#sources li').filter({ hasText: kind })).toContainText('ready');
  }
  // The tall, empty-map welcome is where a stretched destination row was
  // most obvious. Capture it before creating the assistant fixture.
  expect((await request.post('/api/onboarding/skip')).ok()).toBeTruthy();
  const defaultSize = page.viewportSize()!;
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.goto('/?view=map');
  await expect(page.getByRole('heading', { name: 'Meet your assistant.' })).toBeVisible();
  await page.screenshot({ path: join(evidence, 'map-header-welcome.png') });
  await page.setViewportSize(defaultSize);
  await page.goto('about:blank');
  const music = await seedQuestionFolder(request, join(fixture!, 'home'), 'Map Albums', true);
  const other = await seedQuestionFolder(request, join(fixture!, 'home'), 'Other Albums', true);
  const deskRequests: string[] = [];
  const captureDeskRequest = (r: Request) => {
    const path = new URL(r.url()).pathname;
    if (path.startsWith('/api/')) deskRequests.push(path);
  };
  page.on('request', captureDeskRequest);
  const original = join(fixture!, 'home/Downloads/Saved/proposal.pdf');
  for (const [dir, stamp] of [
    ['Documents/Final', 1704067200],
    ['Downloads/Saved', 1735689600]
  ] as const) {
    const root = join(fixture!, 'home', dir);
    mkdirSync(root, { recursive: true });
    const copy = join(root, 'proposal.pdf');
    writeFileSync(copy, 'Synthetic', { flag: 'wx' });
    utimesSync(copy, stamp, stamp);
  }
  for (let n = 0; n < 27; n++)
    writeFileSync(join(fixture!, `home/Downloads/sample-${n}.unknown`), '', { flag: 'wx' });
  await page.goto('/search');
  // Files were added after fixture setup. Refresh, rather than granting again.
  await page.locator('#manage summary').click();
  for (const kind of ['Documents', 'Downloads']) {
    const row = page.locator('#sources li').filter({ hasText: kind });
    await row.getByRole('button', { name: /Refresh/ }).click();
    await expect(row).toContainText('ready');
  }
  await page.locator('#manage summary').click();
  const term = page.getByRole('searchbox');
  await term.fill('proposal');
  const rows = page.locator('#hits li');
  await expect(rows).toHaveCount(2);
  await expect(rows.first()).toContainText('Downloads / Saved');
  await expect(rows.last()).toContainText('Documents / Final');
  await rows.first().getByRole('button').click();
  await expect(page.locator('#selectedFile')).toContainText('Downloads / Saved');
  await page.screenshot({ path: join(evidence, 'copies.png') });
  await page.getByRole('button', { name: 'Search in Documents', exact: true }).click();
  await expect(page.locator('#title')).toHaveText('Documents');
  await term.fill('');
  await expect(rows).toHaveCount(4);
  await rows.filter({ hasText: 'proposal.pdf' }).getByRole('button').click();
  await page.locator('#manage summary').click();
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.screenshot({ path: join(evidence, 'documents-desk.png') });
  await page.getByRole('button', { name: 'Search in Downloads', exact: true }).click();
  await term.fill('proposal.pdf');
  await expect(rows).toHaveCount(1);
  await expect(page.locator('#selectedFile')).toBeHidden();
  await term.fill('sample-');
  await expect(rows).toHaveCount(25);
  await page.getByRole('button', { name: 'More results' }).click();
  await expect(rows).toHaveCount(27);
  await rows.last().getByRole('button').click();
  await expect(page.locator('#selectedFile button')).toHaveCount(1);
  await expect(page.locator('#selectedFile button')).toHaveText('Reveal in Finder');
  await page.locator('#selectedFile').scrollIntoViewIfNeeded();
  await page.screenshot({ path: join(evidence, 'scoped-last-page.png') });
  const last = await rows.last().getAttribute('data-id');
  page.off('request', captureDeskRequest);
  expect(deskRequests).toContain('/api/file-search/query');
  expect(deskRequests.filter(path => !path.startsWith('/api/file-search/'))).toEqual([]);
  const read = async (id: string) =>
    (await (await request.get(`/api/personal-assistant/folder-agent/${id}`)).json()).folder_agent;
  // Seed a pending-question record, not a simulated answer or a live model turn.
  // Only this marked, newly minted split profile is writable here. With a
  // degraded turn the real launcher correctly leads with the failure instead.
  for (const slug of ['map-albums', 'other-albums']) {
    const path = join(fixture!, 'data/workspace-staging', slug, '.ori/folder-agent-v1.json');
    const record = JSON.parse(readFileSync(path, 'utf8'));
    delete record.turn;
    writeFileSync(path, JSON.stringify(record));
  }
  await page.goto('/search');
  const mutations: string[] = [];
  page.on('request', r => {
    if (r.method() === 'POST') mutations.push(new URL(r.url()).pathname);
  });
  // Home's rail is the one switcher: Files is the merged tree and Map is the
  // map, both in the page. The standalone page is where search locations are
  // managed, reached from Files by "Manage search locations"; its own rail
  // links back to Files and Map. This journey walks between that page and Map.
  const onStandalone = async () => (await page.locator('#desk').count()) > 0;
  const toMap = async () => {
    if (await onStandalone()) {
      await page.getByRole('link', { name: 'Map', exact: true }).press('Enter');
      await expect(page).toHaveURL(/\/\?view=map$/);
    } else {
      await goToMap(page);
    }
    await expectDestination(page, 'map');
    // The rail marks the destination at once; the old Map marker only showed
    // once the map could be drawn, and this journey relied on that wait.
    await expect(page.locator('[data-ws-map-viewport]').first()).toBeVisible();
  };
  const toLocations = async () => {
    if (!(await onStandalone())) {
      await goToFiles(page);
      await expectDestination(page, 'files');
      await page.getByRole('link', { name: 'Manage search locations', exact: true }).press('Enter');
    }
    await expect(page).toHaveURL(/\/search$/);
    await expect(page.locator('#coverage')).not.toHaveText('');
  };
  // Home to the standalone page by address: the history traversal under test is
  // the browser's, not the rail's.
  const toLocationsByAddress = async () => {
    await page.goto('/search');
    await expect(page.locator('#coverage')).not.toHaveText('');
  };
  await expect(page.locator('#hits li.current')).toHaveAttribute('data-id', last!);
  await toMap();
  // Only the map should absorb spare height, never the destination controls.
  const originalSize = page.viewportSize()!;
  for (const size of [
    { width: 1440, height: 1000 },
    { width: 390, height: 844 }
  ]) {
    await page.setViewportSize(size);
    const picker = page.getByRole('navigation', { name: 'Destination', exact: true });
    // WebKit can momentarily expose both the rail and mobile picker while
    // applying the viewport change. Assert the layout settles to exactly one.
    await expect(picker).toHaveCount(1);
    await expect(picker).toBeVisible();
    await expect(page.locator('[data-ws-map-viewport]').first()).toBeVisible();
    const gap = await page.evaluate(() => {
      const shell = document.querySelector('.map-desk-shell')!.getBoundingClientRect();
      const mobile = document.querySelector('.desk-mobile')!.getBoundingClientRect();
      const rail = document.querySelector('.desk-rail')!.getBoundingClientRect();
      const cockpit = document.querySelector('#homeCockpit')!.getBoundingClientRect();
      return {
        above: cockpit.top - (mobile.height ? mobile.bottom : shell.top),
        left: cockpit.left - rail.right,
        rail: rail.width,
        bar: mobile.height
      };
    });
    expect(gap.above).toBeGreaterThanOrEqual(0);
    expect(gap.above).toBeLessThan(20);
    if (size.width > 700) {
      expect(gap.left).toBeGreaterThanOrEqual(0);
      expect(gap.rail).toBeGreaterThan(150);
      expect(gap.bar).toBe(0);
    } else {
      expect(gap.rail).toBe(0);
      expect(gap.bar).toBeGreaterThan(0);
      expect(gap.bar).toBeLessThan(70);
    }
    await page.screenshot({ path: join(evidence, `map-header-${size.width}.png`) });
  }
  await page.setViewportSize(originalSize);
  await toLocations();
  await expect(term).toHaveValue('sample-');
  await expect(page.locator('#hits li.current')).toHaveAttribute('data-id', last!);
  await page.getByRole('button', { name: 'Search in All locations', exact: true }).press('Enter');
  await expect(rows).toHaveCount(25);
  await page.locator('#more').click();
  await expect(rows).toHaveCount(27);
  await rows.last().getByRole('button').click();
  await toMap();
  await page.goBack();
  await expect(page.locator('#hits li.current')).toHaveAttribute('data-id', last!);
  await page.goForward();
  await toLocations();
  await term.fill('proposal.pdf');
  await expect(rows).toHaveCount(2);
  await rows.first().getByRole('button').click();
  const proposal = await rows.first().getAttribute('data-id');
  await toMap();
  await expectDestination(page, 'map');
  const camera = () => page.evaluate(() => (window as any).OriWorkspaceMap.getCamera());
  const viewport = page.locator('[data-ws-map-viewport]').first();
  const initialCamera = await camera();
  await viewport.press('ArrowRight');
  await viewport.press('+');
  const tile = page
    .locator(
      `.ws-map-tile[data-ws-id="${music.workspace_id}"], .ws-map-district-tag[data-ws-id="${music.workspace_id}"]`
    )
    .first();
  await tile.press('Space');
  const open = page.locator(`[data-cockpit-rail-open][data-workspace-id="${music.workspace_id}"]`);
  await expect(open).toBeVisible();
  await page.screenshot({ path: join(evidence, 'workspace-selected.png') });
  const panned = await camera();
  expect(panned).not.toEqual(initialCamera);
  await open.press('Enter');
  const launcher = page.locator('#personalAssistantLauncher');
  await expect(launcher.locator('[data-question]')).toBeVisible();
  expect(
    mutations.filter(p =>
      /\/file-search\/(begin|select|open|reveal|refresh|remove|preference)$|\/premise$/.test(p)
    )
  ).toEqual([]);
  await launcher.press('Enter');
  const answer = page.locator(
    `[data-folder-agent-chat-card][data-message-id="${music.chat.message_id}"] [data-folder-agent-action="premise_confirm"]`
  );
  await expect(answer).toBeVisible();
  await page.screenshot({ path: join(evidence, 'commander-question.png') });
  await answer.press('Enter');
  await expect(answer).toHaveCount(0);
  await expect(launcher.locator('[data-question]')).toHaveCount(0);
  expect((await read(other.workspace_id)).premise.state).toBe(other.premise.state);
  await page.getByRole('button', { name: 'Close personal assistant', exact: true }).click();
  await page.goBack();
  // The Map puts the selection back a moment after it draws; leaving before that
  // would save the empty selection over the one this journey is about.
  await expect(tile).toHaveClass(/is-selected/);
  if (await page.locator('#cockpitContextModal').isVisible()) await page.keyboard.press('Escape');
  await toLocations();
  await expect(term).toHaveValue('proposal.pdf');
  await expect(page.locator('#hits li.current')).toHaveAttribute('data-id', proposal!);
  await expect(term).toBeFocused();
  const disk = await page.evaluate(() =>
    JSON.stringify([localStorage, sessionStorage, history.state, location.href, window.name])
  );
  expect(disk).not.toContain('proposal.pdf');
  await page.screenshot({ path: join(evidence, 'restored-copy.png') });
  await toMap();
  await expect(tile).toHaveClass(/is-selected/);
  await expect.poll(camera).toEqual(panned);
  const second = await context.newPage();
  await second.goto('/search');
  await expect(second.locator('#term')).toHaveValue('');
  await second.locator('#manage summary').click();
  await second
    .locator('#sources li')
    .filter({ hasText: 'Downloads' })
    .getByRole('button', { name: 'Remove from search' })
    .click();
  await expect(second.locator('#sources')).not.toContainText('Downloads');
  await second.close();
  await toLocations();
  await expect(rows).toHaveCount(1);
  await expect(page.locator('#selectedFile')).toBeHidden();
  await expect(page.locator('#notice')).toContainText('no longer');
  expect(existsSync(original)).toBeTruthy();
  await page.screenshot({ path: join(evidence, 'revoked-copy.png') });
  // Return to the same workspace, not a cached cue or reply.
  await toMap();
  // Enter opens the selected workspace; wait for the selection to be back.
  await expect(tile).toHaveClass(/is-selected/);
  await tile.press('Enter');
  await expect(page).toHaveURL(new RegExp('/workspaces/map-albums'));
  if (await page.locator('#personalAssistantPanel').isVisible())
    await page.getByRole('button', { name: 'Close personal assistant', exact: true }).click();
  await expect(launcher).toBeVisible();
  await expect(launcher.locator('[data-question]')).toHaveCount(0);
  expect((await read(music.workspace_id)).premise.state).not.toBe('asked');
  await page.goBack();
  await toLocations();
  await expect(rows).toHaveCount(1);
  await rows.first().getByRole('button').click();
  await toMap();
  const refresher = await context.newPage();
  await refresher.goto('/search');
  await refresher.locator('#manage summary').click();
  const refreshed = refresher.waitForResponse(r => r.url().endsWith('/api/file-search/refresh'));
  await refresher.locator('#sources li').getByRole('button', { name: 'Refresh' }).click();
  expect((await refreshed).ok()).toBeTruthy();
  await expect(refresher.locator('#sources')).toContainText('ready');
  await refresher.close();
  await toLocations();
  await expect(rows).toHaveCount(1);
  await expect(page.locator('#selectedFile')).toBeHidden();
  await expect(page.locator('#notice')).toContainText('no longer');
  // Home-origin traversal is not always the explicit ?view=map link.
  await page.goto('/?home=1');
  // The main journey above uses Space; cover pointer selection on a fresh Home too.
  await tile.click();
  await expect(open).toBeVisible();
  await page.keyboard.press('Escape');
  await toLocationsByAddress();
  await page.goBack();
  await expect(tile).toHaveClass(/is-selected/);
  await toLocationsByAddress();
  const deleted = await request.delete(
    `/api/workspaces/${music.workspace_id}?confirm=true&delete_sessions=true`
  );
  expect(deleted.ok()).toBe(true);
  await toMap();
  await expectDestination(page, 'map');
  await expect(tile).toHaveCount(0);
  await expect(page.locator('.ws-map-tile.is-selected')).toHaveCount(0);
  await toLocations();
  // Deliberately deliver an obsolete successful read after a newer one.
  let release!: () => void;
  const wait = new Promise<void>(resolve => (release = resolve));
  let started!: () => void;
  const captured = new Promise<void>(resolve => (started = resolve));
  await page.route('**/api/file-search/query', async route => {
    if (route.request().postDataJSON().term !== 'Harbor') return route.continue();
    const response = await route.fetch();
    started();
    await wait;
    await route.fulfill({ response });
  });
  await term.fill('Harbor');
  await captured;
  await term.fill('Welcome');
  await expect(rows).toHaveCount(1);
  await expect(rows.first()).toContainText('Welcome');
  release();
  await expect(rows.first()).toContainText('Welcome');
  await page.route(
    '**/api/file-search/query',
    route => route.fulfill({ status: 503, body: 'Search unavailable.' }),
    { times: 1 }
  );
  await term.fill('proposal');
  await expect(page.locator('#retry')).toBeVisible();
  await expect(rows).toHaveCount(0);
  await expect(page.locator('#selectedFile')).toBeHidden();
  await page.locator('#retry').click();
  await expect(rows).toHaveCount(1);
  await page.setViewportSize({ width: 390, height: 844 });
  await term.focus();
  await expect(rows).toHaveCount(1);
  await term.press('ArrowDown');
  await expect(page.locator('#selectedFile')).toContainText('Documents / Final');
  await expect(page.getByRole('combobox', { name: 'Destination' })).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.screenshot({ path: join(evidence, 'narrow.png') });
  // The standalone page's picker leads to Home's two destinations.
  await page.getByRole('combobox', { name: 'Destination' }).selectOption('/?view=map');
  // Home's own picker offers the same two options on both of its destinations.
  const homePicker = page.locator('[data-map-picker]');
  await expect(homePicker).toBeVisible();
  await expect(homePicker.locator('option')).toHaveText(['Files', 'Map']);
  await expectDestination(page, 'map');
  await goToFiles(page);
  await expect(homePicker.locator('option')).toHaveText(['Files', 'Map']);
  await expectDestination(page, 'files');
  await page.getByRole('link', { name: 'Manage search locations', exact: true }).click();
  await expect(page).toHaveURL(/\/search$/);
  await expect(page.locator('#selectedFile')).toContainText('Documents / Final');
  await page.setViewportSize({ width: 1280, height: 900 });
  await page.evaluate(() => (document.documentElement.style.zoom = '200%'));
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await page.screenshot({ path: join(evidence, 'zoom.png') });
  const locked = join(fixture!, 'home/Documents/Locked');
  mkdirSync(locked, { mode: 0o700 });
  try {
    chmodSync(locked, 0);
    await page.locator('#manage summary').click();
    await page.locator('#sources').getByRole('button', { name: 'Refresh' }).click();
    await expect(page.locator('#sources')).toContainText('partial');
    await expect(page.locator('#coverage')).toContainText('Coverage incomplete');
  } finally {
    chmodSync(locked, 0o700);
  }
  await page.getByRole('button', { name: 'Start on Home instead' }).click();
  await page.waitForURL('**/?home=1');
  await page.goto('/');
  await expect(page.locator('.home-command-bridge')).toBeVisible();
  await page.goto('/search');
  await page.getByRole('button', { name: 'Make Files my starting page' }).click();
  await expect(page.getByRole('button', { name: 'Start on Home instead' })).toBeVisible();
  // Files as the starting page, with workspaces present, is the merged Files:
  // Home's tree with its one search box. The standalone page is still one click
  // away, from the Manage search locations link.
  await page.goto('/');
  await expect(page).toHaveURL(/\/\?view=tree$/);
  await expect(page.locator('#cockpitTree')).toBeVisible();
  await expect(page.locator('[data-cockpit-area-title]')).toHaveText('Files');
  await expectDestination(page, 'files');
  await page.getByRole('link', { name: 'Manage search locations', exact: true }).click();
  await expect(page.locator('#title')).toHaveText('All locations');
});
