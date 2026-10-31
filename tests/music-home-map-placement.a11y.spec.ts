import { test, expect as baseExpect, type Locator, type Page } from '@playwright/test';
import { promises as fs } from 'node:fs';
import path from 'node:path';
import os from 'node:os';
import {
  musicHomeSnapshot as snapshot,
  musicHomeResourceCounts as resourceCounts,
  openMusicHomeReview
} from './helpers/music-home-placement';
import { openMapReceiptDetails } from './helpers/confirm-reviewed-creation';
import { withOwnedBrowserZoom } from './helpers/owned-browser-zoom';
const expect = baseExpect.configure({ timeout: 60_000 });

// One variant per freshly staged exact-candidate server. Creation and canonical
// proof are real; axe is a pinned accessibility asset, never a domain fixture.
const song = process.env.MUSIC_HOME_PLACEMENT_SONG || '';
const variant = process.env.MUSIC_HOME_PLACEMENT_A11Y || 'light';
const touch = variant === 'touch';
const theme = ['dark', 'touch', 'zoom'].includes(variant) ? 'dark' : 'light';
const manual = ['light', 'dark'].includes(variant);
const evidence = `tasks/evidence-music-home-map-placement/delivery/${variant}`;
let axe = '';
test.use({
  viewport: touch ? { width: 390, height: 844 } : { width: 1440, height: 1000 },
  hasTouch: touch
});
test.setTimeout(120_000);

test(`source Home ${variant}: accessible review, one real setup, no hidden map confirmation`, async ({
  page,
  request,
  baseURL
}) => {
  test.skip(!song, 'requires a fresh exact-candidate narrated sandbox');
  expect(['light', 'dark', 'touch', 'tree', 'zoom']).toContain(variant);
  await fs.mkdir(evidence, { recursive: true });
  const asset = await request.get('https://cdn.jsdelivr.net/npm/axe-core@4.10.3/axe.min.js', {
    timeout: 20_000
  });
  expect(asset.ok()).toBe(true);
  axe = await asset.text();
  if (variant === 'zoom') await withOwnedBrowserZoom(baseURL!, 2, exercise);
  else await exercise(page);
});

async function accessibility(page: Page, selector: string) {
  if (!(await page.evaluate(() => Boolean((window as any).axe))))
    await page.addScriptTag({ content: axe });
  const violations = await page.evaluate(async selector => {
    const result = await (window as any).axe.run(selector, {
      runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa'] }
    });
    return result.violations.map((item: any) => ({
      id: item.id,
      impact: item.impact,
      nodes: item.nodes.map((n: any) => n.target)
    }));
  }, selector);
  expect(violations).toEqual([]);
}

async function capture(page: Page, name: string) {
  const destination = `${evidence}/${name}.png`;
  if (variant !== 'zoom') return page.screenshot({ path: destination });
  // At real tab zoom Playwright's CSS-sized clip can crop the native screenshot.
  // Capture the actual visible browser surface without supplying a CSS clip.
  const session = await page.context().newCDPSession(page);
  try {
    const { data } = await session.send('Page.captureScreenshot', {
      format: 'png',
      captureBeyondViewport: false,
      fromSurface: true
    });
    await fs.writeFile(destination, Buffer.from(data, 'base64'), { mode: 0o600 });
  } finally {
    await session.detach();
  }
}

async function usable(control: Locator, key: 'Tab' | 'Shift+Tab' = 'Tab') {
  await control.scrollIntoViewIfNeeded();
  // Reach the action through the browser's real tab order, not focus() which
  // could hide a tabindex or focus-trap regression.
  for (let step = 0; step < 60; step++) {
    if (await control.evaluate(element => element === document.activeElement)) break;
    await control.page().keyboard.press(key);
  }
  await expect(control).toBeFocused();
  await expect(control).toBeInViewport();
  expect(
    await control.evaluate(element => {
      const rect = element.getBoundingClientRect();
      const hit = document.elementFromPoint(rect.left + rect.width / 2, rect.top + rect.height / 2);
      return element === hit || element.contains(hit);
    })
  ).toBe(true);
}

async function noOverflow(page: Page) {
  expect(
    await page.evaluate(() => {
      const panel = document.querySelector('#personalAssistantPanel') as HTMLElement;
      return {
        page: document.documentElement.scrollWidth > innerWidth + 1,
        panel: panel.scrollWidth > panel.clientWidth + 1
      };
    })
  ).toEqual({ page: false, panel: false });
}

async function exercise(page: Page) {
  await page.addInitScript(theme => localStorage.setItem('ori-theme', theme), theme);
  await page.emulateMedia({
    colorScheme: theme,
    reducedMotion: theme === 'dark' ? 'reduce' : 'no-preference'
  });
  let selectedSong = song;
  if (touch) {
    // A real hostile/long source name, not substituted successful response data.
    const sandbox = await fs.realpath(path.resolve(song, '../..'));
    const temporaryRoot = await fs.realpath(os.tmpdir());
    expect(sandbox.startsWith(`${temporaryRoot}/ori-demo.narrated-installed-`)).toBe(true);
    expect(await fs.readFile(path.join(sandbox, '.ori-demo-owner'), 'utf8')).toContain(
      `worktree=${process.cwd()}`
    );
    const hostile = '<img src=x onerror=window.__homeInjected=1> ' + 'LongProjectName'.repeat(5);
    selectedSong = path.join(path.dirname(song), hostile);
    await fs.rename(song, selectedSong);
  }
  const before = await snapshot(page.request);
  expect(before.workspaces.filter((w: any) => w.kind === 'group')).toHaveLength(0);
  let setups = 0,
    writes = 0;
  page.on('request', request => {
    if (request.method() === 'POST' && /folder-digest\/offers\/[^/]+\/setup$/.test(request.url()))
      setups++;
    if (
      request.method() === 'PATCH' &&
      /\/workspace-map\/layout$/.test(request.url()) &&
      request.postDataJSON()?.operations?.some((op: any) => op.op === 'set_positions')
    )
      writes++;
  });
  const review = await openMusicHomeReview(page, selectedSong, variant === 'tree');
  await expect(page.locator('html')).toHaveAttribute('data-bs-theme', theme);
  const homeName = review.getByRole('textbox', { name: 'Group name', exact: true });
  await expect(homeName).toHaveAttribute('readonly', '');
  await expect(homeName).toHaveValue('Music Production Home');
  await expect(
    review.getByRole('textbox', { name: 'Later project goal', exact: true })
  ).toHaveAttribute('readonly', '');
  await expect(
    review.getByRole('heading', { name: 'Later project · REAPER Assistant', exact: true })
  ).toHaveCount(1);
  await expect(review).toContainText('not by this Home setup');
  if (touch) {
    await expect(review).toContainText(path.basename(selectedSong));
    expect(await review.locator('img[src="x"], script').count()).toBe(0);
    expect(await page.evaluate(() => (window as any).__homeInjected)).toBeUndefined();
  }
  await homeName.scrollIntoViewIfNeeded();
  await capture(page, 'identity');
  await review
    .getByRole('heading', { name: 'Later project · REAPER Assistant', exact: true })
    .scrollIntoViewIfNeeded();
  await capture(page, 'later-team');
  await noOverflow(page);
  await accessibility(page, '[data-source-creation-review]');
  const place = review.getByRole('button', { name: 'Place & set up on map', exact: true });
  let candidate: { x: number; y: number } | undefined;
  if (manual) {
    await usable(place);
    await capture(page, 'review');
    await place.press('Enter');
    const canvas = page.locator('[data-ws-map-viewport]');
    await expect(canvas).toBeFocused();
    const frame = page.locator('[data-ws-map-placement-preview]');
    await expect(frame).toHaveClass(/ws-map-placement-preview--group/);
    await canvas.press('ArrowRight');
    for (let step = 0; step < 4; step++) await canvas.press('ArrowDown');
    candidate = await frame.evaluate(element => ({
      x: parseFloat((element as HTMLElement).style.left),
      y: parseFloat((element as HTMLElement).style.top)
    }));
    await accessibility(page, '[data-ws-map-placement-controls]');
    await capture(page, 'preview');
    expect(await snapshot(page.request)).toEqual(before);
    await canvas.press('Escape');
    await expect(review).toBeVisible();
    await expect(review.locator(':focus')).toHaveCount(1);
    await usable(place);
    await place.press('Enter');
    expect(
      await frame.evaluate(element => ({
        x: parseFloat((element as HTMLElement).style.left),
        y: parseFloat((element as HTMLElement).style.top)
      }))
    ).toEqual(candidate);
    expect(setups).toBe(0);
    expect(writes).toBe(0);
    await canvas.press('Enter');
    await openMapReceiptDetails(page, /Home (ready|kept)/);
  } else {
    await expect(place).toBeDisabled();
    if (variant === 'tree')
      await expect(page.getByRole('button', { name: 'Tree', exact: true })).toHaveAttribute(
        'aria-pressed',
        'true'
      );
    const automatic = review.getByRole('button', { name: 'Use automatic placement', exact: true });
    await usable(automatic);
    if (touch) await automatic.tap();
    else await automatic.press('Enter');
    expect(await snapshot(page.request)).toEqual(before);
    expect(setups).toBe(0);
    const confirm = review.getByRole('button', { name: 'Set up Home', exact: true });
    await usable(confirm);
    await capture(page, 'review');
    if (touch) await confirm.tap();
    else await confirm.press('Enter');
  }
  await expect(review.getByRole('heading', { name: 'Home ready', exact: true })).toBeVisible();
  await expect(review.getByRole('status')).toContainText(
    'Library entries are not project buildings'
  );
  await expect(review.locator(':focus')).toHaveCount(1);
  await noOverflow(page);
  await accessibility(page, '[data-source-creation-review]');
  const after = await snapshot(page.request);
  const homes = after.workspaces.filter((w: any) => w.kind === 'group');
  expect(homes).toHaveLength(1);
  const home = homes[0];
  expect(after.workspaces.filter((w: any) => w.parent_id === home.id)).toHaveLength(0);
  expect(after.agents).toHaveLength(before.agents.length + 1);
  expect(setups).toBe(1);
  expect(writes).toBe(manual ? 1 : 0);
  if (manual) expect(after.positions[home.id]).toEqual(candidate);
  else expect(after.positions).toEqual(before.positions);
  await capture(page, 'home-ready');
  const resources = await resourceCounts(page.request, home.id);
  const show = review.getByRole('button', { name: 'Show on map', exact: true });
  await usable(show);
  if (touch) await show.tap();
  else await show.press('Enter');
  const district = page.locator(`.ws-map-district[data-group-id="${home.id}"]`);
  await expect(page.locator('#personalAssistantPanel')).toBeHidden();
  await expect(district).toBeVisible();
  await expect
    .poll(() => page.evaluate(() => (window as any).OriWorkspaceMap.getSelectedId()))
    .toBe(home.id);
  await accessibility(page, `.ws-map-district[data-group-id="${home.id}"]`);
  await capture(page, 'empty-home');
  expect((await snapshot(page.request)).positions).toEqual(after.positions);
  await page.reload();
  expect(await resourceCounts(page.request, home.id)).toEqual(resources);
  expect((await snapshot(page.request)).positions).toEqual(after.positions);
  expect(setups).toBe(1);
}

// The Home's manager offers the waiting song in the drawer (home-manager-chat-
// handoff). Runs on a sandbox whose Home the plain single-song case just set up
// (scripts/music-home-placement-demo.sh --case chat-a11y).
test('manager hand-off: keyboard-only offer, brief and placement in both themes', async ({
  page,
  request
}) => {
  test.skip(!song || variant !== 'chat', 'MUSIC_HOME_PLACEMENT_A11Y=chat after the Home setup');
  const chatEvidence = 'tasks/evidence-home-manager-chat-handoff/a11y';
  await fs.mkdir(chatEvidence, { recursive: true });
  if (!axe) {
    const asset = await request.get('https://cdn.jsdelivr.net/npm/axe-core@4.10.3/axe.min.js', {
      timeout: 20_000
    });
    expect(asset.ok()).toBe(true);
    axe = await asset.text();
  }
  const home = (await snapshot(request)).workspaces.find((w: any) => w.kind === 'group');
  expect(home).toBeTruthy();
  let setups = 0;
  page.on('request', r => {
    if (r.method() === 'POST' && /folder-digest\/offers\/[^/]+\/setup$/.test(r.url())) setups++;
  });
  const landing = `/workspaces/${home.folder_slug}?mode=map&panel=assistant`;
  const offers = page.locator('#personalAssistantHomeOffer');
  const offer = offers.locator('.pa-home-offer').first();
  const live = offers.locator('[role="status"][aria-live="polite"]').last();
  // Dark with reduced motion, then light: focus lands on the manager's message.
  for (const scheme of ['dark', 'light'] as const) {
    await page.addInitScript(theme => localStorage.setItem('ori-theme', theme), scheme);
    await page.emulateMedia({
      colorScheme: scheme,
      reducedMotion: scheme === 'dark' ? 'reduce' : 'no-preference'
    });
    await page.goto(landing);
    await expect(page.locator('html')).toHaveAttribute('data-bs-theme', scheme);
    await expect(offer).toBeVisible();
    await expect(offer).toBeFocused();
    // This feature's surfaces: the manager's offer and the composer it changes.
    // (The drawer header's Model Settings link has a pre-existing target-size
    // finding outside this feature.)
    await accessibility(page, '#personalAssistantHomeOffer');
    await accessibility(page, '#personalAssistantForm');
    await noOverflow(page);
    await page.screenshot({ path: `${chatEvidence}/offer-${scheme}.png` });
  }
  // A reply becomes the brief, from the keyboard, and is announced.
  const input = page.locator('#personalAssistantInput');
  await usable(input);
  await page.keyboard.type('bounce stems and print a mix note');
  const send = page.locator('#personalAssistantSend');
  await expect(send).toHaveText('Use as brief');
  await usable(send);
  await page.keyboard.press('Enter');
  await expect(offer.locator('textarea')).toHaveValue('bounce stems and print a mix note');
  await expect(live).toContainText('Brief saved: bounce stems and print a mix note');
  // Placement from the keyboard: start is announced, a point outside the Home is
  // refused aloud, Escape returns focus to the offer, Enter inside opens it.
  const press = offers.getByRole('button', { name: 'Open Ori Music Demo & hire', exact: true });
  // From Send the card sits just above, back up the drawer's tab order.
  await usable(press, 'Shift+Tab');
  await page.keyboard.press('Enter');
  await expect(page.locator('[data-map-live]').first()).toContainText(
    'Placing Ori Music Demo inside Music Production Home'
  );
  const instruction = page.locator('[data-ws-map-placement-instruction]');
  await expect(instruction).toHaveAttribute('aria-live', 'polite');
  for (let step = 0; step < 40; step++) await page.keyboard.press('Shift+ArrowLeft');
  await expect(instruction).toContainText('That point is outside Music Production Home.');
  await page.keyboard.press('Escape');
  await expect(offer).toBeFocused();
  expect(setups).toBe(0);
  await usable(press);
  await page.keyboard.press('Enter');
  await expect(instruction).not.toContainText('outside');
  await page.keyboard.press('Enter');
  await expect(offers).toContainText('Ori Music Demo is open', { timeout: 90_000 });
  await expect(live).toContainText('Ori Music Demo is open');
  expect(setups).toBe(1);
  await accessibility(page, '#personalAssistantHomeOffer');
  await page.screenshot({ path: `${chatEvidence}/receipt-light.png` });
});
