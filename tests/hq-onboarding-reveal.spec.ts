import { test, expect, type Page } from '@playwright/test';
import fs from 'node:fs';
import { installLocalCdn } from './helpers/offline-cdn';

// One durable journey per fresh server. A browser context is not a profile reset.
// ./scripts/e2e-fresh.sh tests/hq-onboarding-reveal.spec.ts -- --project=chromium --workers=1
test.describe.configure({ mode: 'serial' });
test.beforeEach(async ({ page }) => installLocalCdn(page));

async function accessible(page: Page, selector: string) {
  if (!(await page.evaluate(() => Boolean((window as any).axe))))
    await page.addScriptTag({ url: 'https://cdn.jsdelivr.net/npm/axe-core@4.10.3/axe.min.js' });
  expect(
    await page.evaluate(
      async scope =>
        (
          await (window as any).axe.run(scope, {
            runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa'] }
          })
        ).violations.map((v: any) => ({ id: v.id, targets: v.nodes.map((n: any) => n.target) })),
      selector
    )
  ).toEqual([]);
}

test('review and Customize retain consent without creating anything', async ({ page, request }) => {
  await request.post('/api/onboarding/skip');
  expect(
    (await request.post('/api/settings/workspace-root', { data: { workspace_root: '' } })).ok()
  ).toBeTruthy();
  const initial = (await (await request.get('/api/personal-assistant')).json()).personal_assistant;
  expect(initial.state, 'requires a fresh isolated server').toBe('needs_hire');
  const hire = await request.post('/api/personal-assistant/hire', {
    data: {
      request_id: 'hq-reveal-hire',
      if_version: initial.state_version ?? 0,
      display_name: 'Atlas',
      mandate: 'Keep my week moving.',
      focus_areas: ['plan_my_day']
    }
  });
  expect(hire.ok(), await hire.text()).toBeTruthy();
  const consequential: string[] = [];
  page.on('request', r => {
    if (
      r.method() === 'POST' &&
      /\/api\/(personal-assistant\/hq|personal-hq\/setup|personal-assistant\/second-brain\/(capture|setup))$/.test(
        new URL(r.url()).pathname
      )
    )
      consequential.push(r.url());
  });
  await page.goto('/');
  await page.waitForFunction(() => Boolean((window as any).PersonalAssistantHQCard?.show));
  await page.locator('#cockpitWelcome [data-home-welcome-action="primary"]').click();
  await expect(page.locator('#personalAssistantHQNotes')).toBeVisible();
  const path = await page.locator('#personalAssistantHQNotesPath').innerText();
  expect(fs.existsSync(path)).toBe(false);
  await expect(page.locator('#personalAssistantHQBuild')).toHaveText('Build my HQ');
  await expect(page.locator('#personalAssistantHQBuild')).toBeEnabled();
  fs.mkdirSync('tasks/evidence-hq-onboarding-reveal/group-1', { recursive: true });
  await page.setViewportSize({ width: 1440, height: 1000 });
  await accessible(page, '#personalAssistantHQCard');
  await page.locator('#personalAssistantHQHeadline').scrollIntoViewIfNeeded();
  await page.screenshot({
    path: 'tasks/evidence-hq-onboarding-reveal/group-1/normal-review.png',
    fullPage: true
  });
  await page.locator('#personalAssistantHQNotesOptions summary').click();
  await page.locator('#personalAssistantHQNotesInclude').uncheck();
  await page.locator('#personalAssistantHQName').fill('My quiet HQ');
  await page.locator('#personalAssistantHQAdjust').click();
  await expect(page.locator('#hqBuildModal')).toBeVisible();
  await expect(page.locator('#hqBuildName')).toHaveValue('My quiet HQ');
  await expect(page.locator('#hqBuildSubmitBtn')).toHaveText('Return to review');
  await page.locator('#hqBuildTimezone').fill('Pacific/Auckland');
  await page.locator('#hqBuildAdvancedToggle').click();
  for (const day of ['mon', 'tue', 'wed', 'thu', 'fri'])
    await page.locator(`#hqBuildAdvanced input[value="${day}"]`).uncheck();
  await page.locator('#hqBuildAdvanced input[value="sat"]').check();
  await page.locator('#hqBuildTime').fill('15:45');
  await page.locator('#hqBuildScope').selectOption('selected');
  await page.locator('#hqBuildIncludeFuture').uncheck();
  await page.locator('#hqBuildNotify').check();
  // Cancel keeps the draft too; it never confirms a build.
  await page.locator('#hqBuildModal').getByRole('button', { name: 'Cancel', exact: true }).click();
  await expect(page.locator('#hqBuildModal')).toBeHidden();
  await expect(page.locator('#personalAssistantHQAdjust')).toBeFocused();
  await expect(page.locator('#personalAssistantHQBriefSummary')).toHaveText(
    'sat · 15:45 · Pacific/Auckland'
  );
  await expect(page.locator('#personalAssistantHQScope')).toContainText(
    'future workspaces are not included'
  );
  await expect(page.locator('#personalAssistantHQNotesInclude')).not.toBeChecked();
  await page.locator('#personalAssistantHQAdjust').click();
  await expect(page.locator('#hqBuildTime')).toHaveValue('15:45');
  await expect(page.locator('#hqBuildNotify')).toBeChecked();
  const originalRoot = await page.locator('#hqBuildWorkspaceRoot').inputValue();
  await page.locator('#hqBuildWorkspaceRoot').fill('');
  await page.locator('#hqBuildSubmitBtn').click();
  await expect(page.locator('#hqBuildModal')).toBeHidden();
  await expect(page.locator('#personalAssistantHQBuild')).toBeDisabled();
  await page.locator('#personalAssistantHQChange').click();
  await expect(page.locator('#personalAssistantHQError')).toContainText(
    'Choose a workspace directory'
  );
  await page.screenshot({
    path: 'tasks/evidence-hq-onboarding-reveal/group-1/invalid-root.png',
    fullPage: true
  });
  await page.locator('#personalAssistantHQAdjust').click();
  await page.locator('#hqBuildWorkspaceRoot').fill(originalRoot);
  await page.locator('#hqBuildSubmitBtn').click();
  await expect(page.locator('#hqBuildModal')).toBeHidden();
  await expect(page.locator('#personalAssistantHQBuild')).toBeEnabled();
  await page.locator('#personalAssistantHQNotesInclude').check();
  await page.locator('#personalAssistantHQNotNow').click();
  await expect(page.locator('#personalAssistantHQPlan')).toBeHidden();
  await page.locator('#personalAssistantHQBuild').click();
  await expect(page.locator('#personalAssistantHQPlan')).toBeVisible();
  await expect(page.locator('#personalAssistantHQName')).toHaveValue('My quiet HQ');
  expect(consequential).toEqual([]);
  expect(fs.existsSync(path)).toBe(false);
  expect(
    (await (await request.get('/api/personal-assistant')).json()).personal_assistant.state
  ).toBe('needs_hq');
  fs.mkdirSync('tasks/evidence-hq-onboarding-reveal/group-1', { recursive: true });
  await page.locator('#personalAssistantHQHeadline').scrollIntoViewIfNeeded();
  await page.screenshot({
    path: 'tasks/evidence-hq-onboarding-reveal/group-1/custom-review-desktop.png',
    fullPage: true
  });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.locator('#personalAssistantHQHeadline').scrollIntoViewIfNeeded();
  await page.screenshot({
    path: 'tasks/evidence-hq-onboarding-reveal/group-1/custom-review-mobile.png',
    fullPage: true
  });
  expect(
    await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)
  ).toBeTruthy();
  // Controlled status fault: an existing read-only source replaces the reviewed
  // default immediately before submit. This must refuse, not create anything.
  await page.route('**/api/personal-assistant/second-brain', route =>
    route.fulfill({
      json: {
        second_brain: {
          status: 'connected',
          source: { path: '/fixture/user-owned-notes', name: 'My notes', origin: 'connected' }
        }
      }
    })
  );
  await page.locator('#personalAssistantHQBuild').click();
  await expect(page.locator('#personalAssistantHQError')).toContainText('notes setup changed');
  await expect(page.locator('#personalAssistantHQNotesMessage')).toContainText('never writes');
  await page.locator('#personalAssistantHQNotesMessage').scrollIntoViewIfNeeded();
  await page.screenshot({
    path: 'tasks/evidence-hq-onboarding-reveal/group-1/injected-readonly-refusal.png',
    fullPage: true
  });
  expect(consequential).toEqual([]);
  expect(fs.existsSync(path)).toBe(false);
});

test('a real validation refusal restores the same review without creating HQ', async ({
  page,
  request
}) => {
  await page.goto('/');
  await page.waitForFunction(() => Boolean((window as any).PersonalAssistantHQCard?.show));
  await page.locator('#cockpitWelcome [data-home-welcome-action="primary"]').click();
  await page.locator('#personalAssistantHQAdjust').click();
  await page.locator('#hqBuildTimezone').fill('not/a/timezone');
  await page.locator('#hqBuildSubmitBtn').click();
  await expect(page.locator('#hqBuildModal')).toBeHidden();
  await page.locator('#personalAssistantHQBuild').click();
  await expect(page.locator('#personalAssistantHQError')).toContainText('daily rhythm');
  await expect(page.locator('#personalAssistantPanel')).toBeVisible();
  await expect(page.locator('#personalAssistantHQBriefSummary')).toContainText('not/a/timezone');
  expect(
    (await (await request.get('/api/personal-assistant')).json()).personal_assistant.state
  ).toBe('needs_hq');
  fs.mkdirSync('tasks/evidence-hq-onboarding-reveal/group-2', { recursive: true });
  await page.screenshot({
    path: 'tasks/evidence-hq-onboarding-reveal/group-2/refused.png',
    fullPage: true
  });
});

test('confirmed customized payload builds exactly one real HQ with reviewed Notes', async ({
  page,
  request
}) => {
  const mobile = process.env.ORI_HQ_REVEAL_MOBILE === '1';
  await page.setViewportSize(mobile ? { width: 390, height: 844 } : { width: 1440, height: 1000 });
  const reopen = process.env.ORI_HQ_REVEAL_REOPEN === '1';
  const lost = process.env.ORI_HQ_REVEAL_LOST === '1';
  const stale = process.env.ORI_HQ_REVEAL_STALE === '1';
  const unknown = process.env.ORI_HQ_REVEAL_UNKNOWN === '1';
  const noMap = process.env.ORI_HQ_REVEAL_NO_MAP === '1';
  const noStorage = process.env.ORI_HQ_REVEAL_NO_STORAGE === '1';
  if (noStorage)
    await page.addInitScript(() => {
      for (const method of ['getItem', 'setItem', 'removeItem'] as const)
        Storage.prototype[method] = () => {
          throw new DOMException('Injected storage refusal', 'SecurityError');
        };
    });
  const reduced = process.env.ORI_HQ_REVEAL_REDUCED === '1';
  const capture = (group: number, name: string) =>
    `tasks/evidence-hq-onboarding-reveal/group-${group}/${test.info().project.name}-${mobile ? '390' : 'desktop'}${unknown ? '-uncertain' : stale ? '-stale-view' : lost ? '-lost-response' : noMap ? '-renderer-failure' : noStorage ? '-no-storage' : ''}-${name}.png`;
  if (reduced) await page.emulateMedia({ reducedMotion: 'reduce' });
  await page.goto('/');
  await page.locator('[data-cockpit-view="tree"]').click();
  await page.waitForFunction(() => Boolean((window as any).PersonalAssistantHQCard?.show));
  await page.locator('#personalAssistantLauncher').click();
  await expect(page.locator('#personalAssistantHQCard')).toBeVisible();
  if (await page.locator('#personalAssistantHQPlan').isHidden())
    await page.locator('#personalAssistantHQBuild').click();
  await expect(page.locator('#personalAssistantHQNotesPath')).toBeVisible();
  if (!mobile) await page.locator('#personalAssistantExpand').click();
  const notesPath = await page.locator('#personalAssistantHQNotesPath').innerText();
  await page.locator('#personalAssistantHQAdjust').click();
  await page.locator('#hqBuildName').fill('Quiet HQ');
  await page.locator('#hqBuildTimezone').fill('Pacific/Auckland');
  await page.locator('#hqBuildAdvancedToggle').click();
  for (const day of ['mon', 'tue', 'wed', 'thu', 'fri'])
    await page.locator(`#hqBuildAdvanced input[value="${day}"]`).uncheck();
  await page.locator('#hqBuildAdvanced input[value="sat"]').check();
  await page.locator('#hqBuildTime').fill('15:45');
  await page.locator('#hqBuildScope').selectOption('selected');
  await page.locator('#hqBuildIncludeFuture').uncheck();
  await page.locator('#hqBuildNotify').check();
  await page.locator('#hqBuildSubmitBtn').click();
  await expect(page.locator('#hqBuildModal')).toBeHidden();
  let projectionDown = false;
  await page.route(/\/api\/personal-assistant$/, route =>
    projectionDown && unknown
      ? route.fulfill({
          status: 503,
          json: { error: 'Injected relationship projection unavailable' }
        })
      : route.continue()
  );
  await page.route('**/api/personal-hq/status', route =>
    projectionDown && stale
      ? route.fulfill({ status: 503, json: { error: 'Injected HQ projection unavailable' } })
      : route.continue()
  );
  let releaseResponse!: () => void;
  const responseGate = new Promise<void>(resolve => {
    releaseResponse = resolve;
  });
  await page.route('**/api/personal-assistant/hq', async route => {
    const response = await route.fetch();
    projectionDown = stale || unknown;
    await responseGate; // injected response hold, never a delay before the real write
    if (lost || unknown)
      await route.abort('connectionreset'); // real server wrote; only its response is lost
    else await route.fulfill({ response });
  });
  const builds: Record<string, unknown>[] = [];
  const captures: Record<string, unknown>[] = [];
  const forbidden: string[] = [];
  page.on('request', r => {
    if (
      r.method() !== 'GET' &&
      /\/api\/(workspace-map\/layout|chat|ori-guide|.*\/scan|personal-assistant\/hire|personal-assistant\/second-brain\/(connect|setup)|home-assistant\/folder-context\/(select|review)|agents)/.test(
        new URL(r.url()).pathname
      )
    )
      forbidden.push(r.url());
    if (r.method() === 'POST' && new URL(r.url()).pathname === '/api/personal-assistant/hq')
      builds.push(r.postDataJSON());
    if (r.method() === 'POST' && r.url().endsWith('/second-brain/capture'))
      captures.push(r.postDataJSON());
  });
  if (noMap)
    await page.evaluate(() => {
      (window as any).OriWorkspaceMap.frameHQCreation = () => {
        throw new Error('Injected map renderer failure');
      };
    });
  await page.locator('#personalAssistantHQBuild').click();
  await expect(page.locator('[data-creation-map-status]')).toContainText('Setting up your HQ…');
  await expect(page.locator('#personalAssistantPanel')).toBeHidden();
  await expect(page.locator('.modal-backdrop')).toHaveCount(0);
  await expect(page.locator('#homeCockpit')).toHaveAttribute('data-view', 'map');
  await expect(page.locator('#cockpitWelcome')).toBeHidden();
  if (reopen) {
    await page.locator('#personalAssistantLauncher').click();
    await expect(page.locator('#personalAssistantPanel')).toBeVisible();
  }
  if (!reopen) await accessible(page, '[data-creation-map-status]');
  fs.mkdirSync('tasks/evidence-hq-onboarding-reveal/group-2', { recursive: true });
  await page.screenshot({
    path: capture(2, `pending${reopen ? '-reopened' : ''}`),
    fullPage: false
  });
  releaseResponse();
  await expect
    .poll(
      async () =>
        (await (await request.get('/api/personal-assistant')).json()).personal_assistant.state
    )
    .toBe('active');
  expect(builds).toHaveLength(1);
  expect(forbidden).toEqual([]);
  expect(new URL(page.url()).searchParams.get('view')).toBe('tree');
  expect(builds[0]).toMatchObject({
    name: 'Quiet HQ',
    timezone: 'Pacific/Auckland',
    schedule_days: ['sat'],
    schedule_time: '15:45',
    scope: 'selected',
    include_future_workspaces: false,
    notify_on_ready: true,
    notes: { include: true, witness: notesPath }
  });
  expect(builds[0].request_id).toBeTruthy();
  await expect.poll(() => fs.existsSync(`${notesPath}/Inbox/README.md`)).toBe(true);
  if (reopen) {
    await page.waitForTimeout(1500); // allow injected response + canonical refresh to finish
    await expect(page.locator('#personalAssistantPanel')).toBeVisible();
    await expect(page.locator('#personalAssistantExpand')).toHaveAttribute(
      'aria-pressed',
      mobile ? 'false' : 'true'
    );
    await expect(page.locator('[data-creation-map-status]')).toBeHidden();
    await page.screenshot({
      path: capture(2, 'explicit-reopen'),
      fullPage: false
    });
    return;
  }
  if (stale || unknown) {
    const status = page.locator('[data-creation-map-status]');
    await expect(status).toContainText(stale ? 'view needs refreshing' : 'Setup needs checking');
    await expect(page.locator('#personalHQResultHost')).toBeHidden();
    await expect(page.locator('.is-assembling')).toHaveCount(0);
    await expect(status.getByRole('button', { name: /Build|Retry same request/ })).toHaveCount(0);
    projectionDown = false;
    await status
      .getByRole('button', { name: stale ? 'Refresh view' : 'Check setup status', exact: true })
      .click();
    expect(builds).toHaveLength(1);
  }
  await expect(page.locator('#personalHQResultHost')).toContainText('Your HQ is ready');
  expect(captures).toHaveLength(0);
  await expect(page.locator('#personalHQResultHost .agent-avatar')).toBeVisible();
  await expect(page.locator('#personalAssistantPanel')).toBeHidden();
  fs.mkdirSync('tasks/evidence-hq-onboarding-reveal/group-3', { recursive: true });
  const art = page.locator('.ws-map-tile svg[data-building-variant="hq"]');
  await expect(art.locator('[data-hq-part]')).toHaveCount(4);
  if (!reduced && !noMap) {
    await expect(art).toHaveClass(/is-assembling/);
    await page.screenshot({
      path: capture(3, 'assembly'),
      fullPage: false
    });
    if (process.env.ORI_HQ_REVEAL_ESCAPE === '1') {
      await page.keyboard.press('Escape');
      await expect(page.locator('#personalHQResultHost')).toBeHidden();
      await expect(page.locator('.is-assembling')).toHaveCount(0);
      await expect(page.locator('#personalAssistantPanel')).toBeHidden();
      expect(builds).toHaveLength(1);
      return;
    } else if (process.env.ORI_HQ_REVEAL_SKIP === '1') {
      await page.getByRole('button', { name: 'Skip animation' }).click();
    } else if (process.env.ORI_HQ_REVEAL_INTERRUPT === '1') {
      await page.locator('[data-cockpit-view="tree"]').click();
      await expect(page.locator('[data-creation-map-status]')).toBeHidden();
      await expect(page.locator('#personalHQResultHost')).toBeHidden();
      await expect(page.locator('.is-assembling')).toHaveCount(0);
      return;
    } else {
      // Controlled ordinary status remount: it may inherit elapsed motion, not
      // acquire a second reveal or another camera/focus operation.
      await page.evaluate(() => (window as any).PersonalHQOnboarding.refreshHQStatus());
    }
  }
  await expect(art).not.toHaveClass(/is-assembling/);
  await page.screenshot({
    path: capture(3, reduced ? 'reduced' : 'final'),
    fullPage: false
  });
  const hq = page.locator('.ws-map-tile').filter({ has: page.locator('.ws-map-tile-hq-badge') });
  await expect(hq).toBeVisible();
  if (mobile) {
    const bounds = await hq.boundingBox();
    const canvas = await page.locator('[data-ws-map-viewport]').boundingBox();
    const result = await page.locator('#personalHQResultHost').boundingBox();
    expect(bounds!.y).toBeGreaterThanOrEqual(canvas!.y);
    expect(bounds!.y + bounds!.height).toBeLessThanOrEqual(result!.y);
  }
  await expect
    .poll(
      () =>
        hq.evaluate(el => {
          const box = el.getBoundingClientRect();
          return el.contains(
            document.elementFromPoint(box.x + box.width / 2, box.y + box.height / 2)
          );
        }),
      {
        message: JSON.stringify(
          await hq.evaluate(el => ({
            tile: el.getBoundingClientRect(),
            result: document.getElementById('personalHQResultHost')?.getBoundingClientRect(),
            canvas: document.querySelector('[data-ws-map-viewport]')?.getBoundingClientRect()
          }))
        )
      }
    )
    .toBe(true);
  await page.screenshot({
    path: capture(2, 'ready'),
    fullPage: false
  });
  fs.mkdirSync('tasks/evidence-hq-onboarding-reveal/group-4', { recursive: true });
  await page.locator('#personalAssistantHQFirstNote').click();
  await expect(page.locator('#personalAssistantHQNoteText')).toBeFocused();
  await page
    .locator('#personalAssistantHQNoteText')
    .fill('A real first note from the HQ reveal sandbox.');
  await page.locator('#personalAssistantHQNoteBack').click();
  await page.locator('#personalAssistantHQFirstNote').click();
  await expect(page.locator('#personalAssistantHQNoteText')).toHaveValue(
    'A real first note from the HQ reveal sandbox.'
  );
  await page.screenshot({
    path: capture(4, 'editor'),
    fullPage: false
  });
  expect(captures).toHaveLength(0);
  const savedResponse = page.waitForResponse(
    r => r.request().method() === 'POST' && r.url().endsWith('/second-brain/capture')
  );
  await page.locator('#personalAssistantHQNoteSave').click();
  const saved = (await (await savedResponse).json()).capture;
  expect(captures).toHaveLength(1);
  expect(forbidden).toEqual([]);
  await expect(page.locator('#personalAssistantHQNoteSavedTitle')).toContainText('Saved to Inbox');
  expect(fs.readFileSync(`${notesPath}/${saved.note}`, 'utf8')).toContain(
    'A real first note from the HQ reveal sandbox.'
  );
  await page.screenshot({
    path: capture(4, 'saved'),
    fullPage: false
  });
  const route = await page.locator('#personalAssistantHQFirstExplore').getAttribute('href');
  expect(route).toMatch(/^\/workspaces\//);
  await page.locator('#personalAssistantHQFirstExplore').click();
  await page.waitForURL(url => url.pathname + url.search === route);
  expect(new URL(page.url()).searchParams.get('mode')).toBe('details');
  await page.goto('/');
  await page.waitForFunction(() => Boolean((window as any).PersonalAssistantPanel?.isOpen));
  await expect(page.locator('#personalAssistantPanel')).toBeHidden();
  await expect(page.locator('[data-creation-map-status]')).toHaveCount(0);
  await expect(page.locator('#personalHQResultHost')).toBeHidden();
  await expect(page.locator('.is-assembling')).toHaveCount(0);
});
