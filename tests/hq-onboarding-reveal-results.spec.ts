import { test, expect, type Page } from '@playwright/test';
import fs from 'node:fs';
import path from 'node:path';
import { installLocalCdn } from './helpers/offline-cdn';

// Separate fresh server. Real HQ/Notes/write first; subsequent status/layout
// fixtures are explicitly injected projections, not claimed backend failures.
test.describe.configure({ mode: 'serial' });
test.beforeEach(async ({ page }) => installLocalCdn(page));
const evidence = 'tasks/evidence-hq-onboarding-reveal/group-4';
let workspaceId = '';
let workspaceRoute = '';
let notes: any;

async function restoreResult(page: Page) {
  await page.addInitScript(id => {
    if (sessionStorage.getItem('hq-result-fixture-seeded')) return;
    sessionStorage.setItem('hq-result-fixture-seeded', '1');
    localStorage.setItem('ori.hq.firstAction.v1', JSON.stringify({ workspaceId: id }));
    sessionStorage.setItem('ori.hq.compactResult.v1', id);
  }, workspaceId);
  await page.goto('/');
  await expect(page.locator('#personalHQResultHost')).toBeVisible();
  await expect(page.locator('#personalAssistantPanel')).toBeHidden();
}

test('real note: Back, changed-source refusal, repeat submit, Add another and reload draft', async ({
  page,
  request
}) => {
  fs.mkdirSync(evidence, { recursive: true });
  await request.post('/api/onboarding/skip');
  expect(
    (await request.post('/api/settings/workspace-root', { data: { workspace_root: '' } })).ok()
  ).toBeTruthy();
  const initial = (await (await request.get('/api/personal-assistant')).json()).personal_assistant;
  expect(initial.state).toBe('needs_hire');
  const hired = await request.post('/api/personal-assistant/hire', {
    data: {
      request_id: 'hq-results-hire',
      if_version: initial.state_version ?? 0,
      display_name: 'Atlas thoughtful companion for a busy week',
      mandate: 'Keep my week moving.',
      focus_areas: ['plan_my_day']
    }
  });
  expect(hired.ok(), await hired.text()).toBeTruthy();
  await page.goto('/');
  await page.locator('#cockpitWelcome [data-home-welcome-action="primary"]').click();
  await page
    .locator('#personalAssistantHQName')
    .fill('A quiet home base for projects and everything worth remembering');
  await page.locator('#personalAssistantHQBuild').click();
  await expect(page.locator('#personalHQResultHost')).toBeVisible();
  const hq = (await (await request.get('/api/personal-hq/status')).json()).status;
  workspaceId = hq.workspace.id;
  workspaceRoute = `/workspaces/${encodeURIComponent(hq.workspace.folder_slug)}`;
  notes = await page.evaluate(
    async () => (await (await fetch('/api/personal-assistant/second-brain')).json()).second_brain
  );
  expect(notes.source.origin).toBe('created_by_ori');
  const inbox = path.join(notes.source.path, 'Inbox');
  const before = fs.readdirSync(inbox);
  const captures: any[] = [];
  page.on('request', r => {
    if (r.url().endsWith('/second-brain/capture')) captures.push(r.postDataJSON());
  });
  const note = page.locator('#personalAssistantHQFirstNote');
  const text = page.locator('#personalAssistantHQNoteText');
  await note.click();
  await text.fill('Keep this draft while I check the connection.');
  await page.locator('#personalAssistantHQNoteBack').click();
  await note.click();
  await expect(text).toHaveValue('Keep this draft while I check the connection.');
  // Injected changed source between editor-open and save; no capture is sent.
  await page.route('**/api/personal-assistant/second-brain', route =>
    route.fulfill({
      json: {
        second_brain: { ...notes, source: { ...notes.source, id: 'injected-different-source' } }
      }
    })
  );
  await page.locator('#personalAssistantHQNoteSave').click();
  await expect(page.locator('#personalAssistantHQNoteError')).toContainText('connection changed');
  await expect(page.locator('#personalAssistantHQNoteSave')).toBeDisabled();
  expect(captures).toHaveLength(0);
  expect(fs.readdirSync(inbox)).toEqual(before);
  await page.screenshot({ path: `${evidence}/injected-source-change.png` });
  await page.unroute('**/api/personal-assistant/second-brain');
  await page.locator('#personalAssistantHQNoteBack').click();
  await note.click();
  // Two native form submissions in one turn must still be one HTTP capture.
  await page.locator('#personalAssistantHQNoteForm').evaluate(form => {
    (form as HTMLFormElement).requestSubmit();
    (form as HTMLFormElement).requestSubmit();
  });
  await expect(page.locator('#personalAssistantHQNoteSavedTitle')).toContainText('Saved to Inbox');
  expect(captures).toHaveLength(1);
  const added = fs.readdirSync(inbox).filter(name => !before.includes(name));
  expect(added).toHaveLength(1);
  expect(fs.readFileSync(path.join(inbox, added[0]), 'utf8')).toContain(
    'Keep this draft while I check the connection.'
  );
  await page.locator('#personalAssistantHQNoteAnother').click();
  await expect(text).toHaveValue('');
  await text.fill('An unsaved second idea.');
  await page.reload();
  await expect(page.locator('#personalHQResultHost')).toBeVisible();
  await expect(page.locator('.is-assembling')).toHaveCount(0);
  await note.click();
  await expect(text).toHaveValue('An unsaved second idea.');
  expect(captures).toHaveLength(1);
  expect(fs.readdirSync(inbox).filter(name => !before.includes(name))).toHaveLength(1);
});

test('injected Notes outcome matrix never grants writes from a historical receipt', async ({
  page
}) => {
  let projection: any = notes;
  const writes: string[] = [];
  page.on('request', r => {
    if (
      ['POST', 'PUT', 'PATCH', 'DELETE'].includes(r.method()) &&
      /second-brain|personal-assistant\/hq|\/chat|\/scan|\/layout/.test(r.url())
    )
      writes.push(r.url());
  });
  await page.route('**/api/personal-assistant/second-brain', route =>
    projection === null
      ? route.fulfill({ status: 503, json: { error: 'Injected status unavailable' } })
      : route.fulfill({ json: { second_brain: projection } })
  );
  await restoreResult(page);
  const cases = [
    {
      name: 'readonly',
      value: { ...notes, source: { ...notes.source, origin: 'connected' } },
      copy: 'never writes to them',
      open: true
    },
    {
      name: 'missing',
      value: { ...notes, status: 'missing' },
      copy: 'connection needs attention',
      open: true
    },
    {
      name: 'stopped',
      value: {
        ...notes,
        setup: {
          status: 'stopped',
          message: 'Another folder is connected. Existing files are unchanged.'
        }
      },
      copy: 'Another folder',
      open: true
    },
    {
      name: 'pending',
      value: { ...notes, source: null, status: 'not_connected', setup: { status: 'pending' } },
      copy: 'still being set up',
      retry: true
    },
    {
      name: 'failed',
      value: {
        ...notes,
        source: null,
        status: 'not_connected',
        setup: { status: 'failed', message: 'The reviewed folder is occupied. Your HQ is ready.' }
      },
      copy: 'folder is occupied',
      retry: true
    },
    {
      name: 'omitted',
      value: { ...notes, source: null, setup: null, status: 'not_connected' },
      copy: 'set up notes later',
      setup: true
    },
    { name: 'unavailable', value: null, copy: 'status is unavailable', open: true }
  ];
  for (const item of cases) {
    projection = item.value;
    await page.reload();
    const host = page.locator('#personalHQResultHost');
    await expect(host).toBeVisible();
    await expect(host).toContainText(item.copy);
    await expect(page.locator('#personalAssistantHQFirstNote')).toBeHidden();
    await expect(page.locator('#personalAssistantHQFirstExplore')).toHaveAttribute(
      'href',
      workspaceRoute + '?mode=details'
    );
    await expect(page.locator('#personalAssistantHQFirstOpenNotes')).toBeVisible({
      visible: !!item.open
    });
    await expect(page.locator('#personalAssistantHQFirstSetupNotes')).toBeVisible({
      visible: !!item.setup
    });
    await expect(page.locator('#personalAssistantHQNotesRetry')).toBeVisible({
      visible: !!item.retry
    });
    await expect(page.locator('.is-assembling')).toHaveCount(0);
    await page.screenshot({ path: `${evidence}/injected-${item.name}.png` });
  }
  expect(writes).toEqual([]);
});

test('result/editor layouts: themes, narrow widths, long details, reduced keyboard viewport', async ({
  page
}) => {
  await restoreResult(page);
  await page.addScriptTag({ url: 'https://cdn.jsdelivr.net/npm/axe-core@4.10.3/axe.min.js' });
  const accessible = async () =>
    expect(
      await page.evaluate(async () =>
        (
          await (window as any).axe.run('#personalHQResultHost', {
            runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa'] }
          })
        ).violations.map((v: any) => ({ id: v.id, targets: v.nodes.map((n: any) => n.target) }))
      )
    ).toEqual([]);
  for (const [width, height] of [
    [1440, 1000],
    [820, 900],
    [390, 844],
    [320, 740]
  ]) {
    await page.setViewportSize({ width, height });
    for (const theme of ['light', 'dark']) {
      if ((await page.locator('html').getAttribute('data-bs-theme')) !== theme)
        await page.locator('#darkModeToggle').click();
      // Explicit user-like scrolling for this restored-result layout fixture;
      // reload itself must not move the camera or start the reveal.
      await page.locator('[data-ws-map-viewport]').evaluate(n => {
        const nav = document.querySelector('.navbar')?.getBoundingClientRect().height || 0;
        window.scrollBy({ top: n.getBoundingClientRect().top - nav - 12, behavior: 'instant' });
      });
      await page.waitForTimeout(350); // Let the existing theme transition finish for captures.
      const host = page.locator('#personalHQResultHost');
      await expect(host).toBeVisible();
      expect(await host.evaluate(n => n.scrollWidth <= n.clientWidth + 1)).toBe(true);
      await accessible();
      await page.screenshot({ path: `${evidence}/result-${width}-${theme}.png` });
      await page.locator('#personalAssistantHQDetails summary').click();
      await expect(page.locator('#personalAssistantHQReceipt')).toBeVisible();
      expect(await host.evaluate(n => n.scrollWidth <= n.clientWidth + 1)).toBe(true);
      await page.locator('#personalAssistantHQDetails summary').click();
      await page.locator('#personalAssistantHQFirstNote').click();
      await page.locator('#personalAssistantHQNoteText').fill('A draft that stays in this tab.');
      const save = page.locator('#personalAssistantHQNoteSave');
      await expect(save).toBeInViewport();
      expect(
        await save.evaluate(n => {
          const r = n.getBoundingClientRect();
          return n.contains(document.elementFromPoint(r.x + r.width / 2, r.y + r.height / 2));
        })
      ).toBe(true);
      await accessible();
      await page.screenshot({ path: `${evidence}/editor-${width}-${theme}.png` });
      await page.locator('#personalAssistantHQNoteBack').click();
    }
  }
  // Browser resize models a keyboard-reduced layout viewport, not a physical
  // iOS keyboard. visualViewport handling also accounts for overlay keyboards.
  await page.setViewportSize({ width: 390, height: 480 });
  await page.locator('#personalAssistantHQFirstNote').click();
  await expect(page.locator('#personalAssistantHQNoteText')).toBeFocused();
  await expect(page.locator('#personalAssistantHQNoteSave')).toBeInViewport();
  await page.screenshot({ path: `${evidence}/editor-keyboard-viewport.png` });
  // Separately inject an overlay-style visualViewport: unlike resize, the
  // layout viewport remains 844px tall. This exercises the real resize handler,
  // not a claim that a physical iOS/Android keyboard was opened.
  await page.setViewportSize({ width: 390, height: 844 });
  await page.evaluate(() => {
    const actual = window.visualViewport!;
    Object.defineProperty(window, 'visualViewport', {
      configurable: true,
      value: {
        width: 390,
        height: 480,
        offsetTop: 0,
        offsetLeft: 0,
        scale: 1
      }
    });
    actual.dispatchEvent(new Event('resize'));
  });
  const bounds = await page.locator('#personalHQResultHost').boundingBox();
  const nav = await page.locator('.navbar').boundingBox();
  const save = await page.locator('#personalAssistantHQNoteSave').boundingBox();
  expect(bounds!.y).toBeGreaterThanOrEqual(nav!.y + nav!.height);
  expect(save!.y + save!.height).toBeLessThanOrEqual(480);
  // Bounding boxes alone miss clipping by the scrolling card or sticky footer.
  // Check all four edge midpoints simultaneously, without scrolling (rounded
  // corners intentionally don't belong to the control's hit target).
  for (const id of [
    'personalAssistantHQNoteText',
    'personalAssistantHQNoteSave',
    'personalAssistantHQNoteBack',
    'personalAssistantHQFirstExplore'
  ]) {
    expect(
      await page.locator(`#${id}`).evaluate(el => {
        const b = el.getBoundingClientRect();
        return [
          [b.left + b.width / 2, b.top + 2],
          [b.left + b.width / 2, b.bottom - 2],
          [b.left + 2, b.top + b.height / 2],
          [b.right - 2, b.top + b.height / 2]
        ].every(([x, y]) => el.contains(document.elementFromPoint(x, y)));
      }),
      `${id} is fully visible in the injected overlay viewport`
    ).toBe(true);
  }
  await expect(page.locator('#personalAssistantHQNoteText')).toBeFocused();
  await page.screenshot({ path: `${evidence}/editor-injected-overlay-viewport.png` });
});

for (const touch of [false, true])
  test(`${touch ? 'Touch' : 'Keyboard'} can open, back out and Explore without any note write`, async ({
    browser,
    baseURL
  }) => {
    const context = await browser.newContext({
      baseURL,
      viewport: { width: 390, height: 844 },
      hasTouch: touch,
      isMobile: touch
    });
    try {
      const page = await context.newPage();
      await installLocalCdn(page);
      const writes: string[] = [];
      page.on('request', r => {
        if (
          r.method() === 'POST' &&
          /second-brain\/(capture|connect|setup)|personal-assistant\/hq/.test(r.url())
        )
          writes.push(r.url());
      });
      await restoreResult(page);
      const note = page.locator('#personalAssistantHQFirstNote');
      await expect(note).toBeVisible();
      if (touch) await note.tap();
      else {
        for (let i = 0; i < 40 && !(await note.evaluate(n => n === document.activeElement)); i++)
          await page.keyboard.press('Tab');
        await expect(note).toBeFocused();
        await page.keyboard.press('Enter');
      }
      const text = page.locator('#personalAssistantHQNoteText');
      await expect(text).toBeFocused();
      await text.pressSequentially('An optional unsaved draft.');
      if (touch) {
        await page.locator('#personalAssistantHQNoteBack').tap();
        await page.locator('#personalAssistantHQFirstExplore').tap();
      } else {
        await page.keyboard.press('Tab');
        await expect(page.locator('#personalAssistantHQNoteSave')).toBeFocused();
        await page.keyboard.press('Tab');
        await expect(page.locator('#personalAssistantHQNoteBack')).toBeFocused();
        await page.keyboard.press('Enter');
        await expect(note).toBeFocused();
        await page.keyboard.press('Tab');
        await expect(page.locator('#personalAssistantHQFirstExplore')).toBeFocused();
        await page.keyboard.press('Enter');
      }
      await page.waitForURL(url => url.pathname === workspaceRoute);
      expect(writes).toEqual([]);
      expect(await page.evaluate(() => localStorage.getItem('ori.hq.firstAction.v1'))).toBeNull();
    } finally {
      await context.close();
    }
  });
