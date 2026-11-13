import { test, expect } from '@playwright/test';
import fs from 'node:fs';
import { installLocalCdn } from './helpers/offline-cdn';

// One durable journey per fresh server. A browser context is not a profile reset.
// ./scripts/e2e-fresh.sh tests/hq-onboarding-reveal.spec.ts -- --project=chromium --workers=1
test.describe.configure({ mode: 'serial' });
test.beforeEach(async ({ page }) => installLocalCdn(page));

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
  const reopen = process.env.ORI_HQ_REVEAL_REOPEN === '1';
  const reduced = process.env.ORI_HQ_REVEAL_REDUCED === '1';
  if (reduced) await page.emulateMedia({ reducedMotion: 'reduce' });
  await page.goto('/');
  await page.locator('[data-cockpit-view="tree"]').click();
  await page.waitForFunction(() => Boolean((window as any).PersonalAssistantHQCard?.show));
  await page.locator('#personalAssistantLauncher').click();
  await expect(page.locator('#personalAssistantHQCard')).toBeVisible();
  if (await page.locator('#personalAssistantHQPlan').isHidden())
    await page.locator('#personalAssistantHQBuild').click();
  await expect(page.locator('#personalAssistantHQNotesPath')).toBeVisible();
  await page.locator('#personalAssistantExpand').click();
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
  await page.route('**/api/personal-assistant/hq', async route => {
    const response = await route.fetch();
    await new Promise(resolve => setTimeout(resolve, 900)); // injected response delay, real write
    await route.fulfill({ response });
  });
  const builds: Record<string, unknown>[] = [];
  const forbidden: string[] = [];
  page.on('request', r => {
    if (
      r.method() !== 'GET' &&
      /\/api\/(workspace-map\/layout|chat|ori-guide|.*\/scan)/.test(new URL(r.url()).pathname)
    )
      forbidden.push(r.url());
    if (r.method() === 'POST' && new URL(r.url()).pathname === '/api/personal-assistant/hq')
      builds.push(r.postDataJSON());
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
  fs.mkdirSync('tasks/evidence-hq-onboarding-reveal/group-2', { recursive: true });
  await page.screenshot({
    path: `tasks/evidence-hq-onboarding-reveal/group-2/pending${reopen ? '-reopened' : ''}.png`,
    fullPage: true
  });
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
    await expect(page.locator('#personalAssistantExpand')).toHaveAttribute('aria-pressed', 'true');
    await expect(page.locator('[data-creation-map-status]')).toBeHidden();
    await page.screenshot({
      path: 'tasks/evidence-hq-onboarding-reveal/group-2/explicit-reopen.png',
      fullPage: true
    });
    return;
  }
  await expect(page.locator('[data-creation-map-status]')).toContainText('Your HQ is ready');
  await expect(page.locator('[data-creation-map-status] .agent-avatar')).toBeVisible();
  await expect(page.locator('#personalAssistantPanel')).toBeHidden();
  fs.mkdirSync('tasks/evidence-hq-onboarding-reveal/group-3', { recursive: true });
  const art = page.locator('.ws-map-tile svg[data-building-variant="hq"]');
  await expect(art.locator('[data-hq-part]')).toHaveCount(4);
  if (!reduced) {
    await expect(art).toHaveClass(/is-assembling/);
    await page.screenshot({
      path: 'tasks/evidence-hq-onboarding-reveal/group-3/assembly.png',
      fullPage: true
    });
    if (process.env.ORI_HQ_REVEAL_SKIP === '1') {
      await page.getByRole('button', { name: 'Skip animation' }).click();
    } else if (process.env.ORI_HQ_REVEAL_INTERRUPT === '1') {
      await page.locator('[data-cockpit-view="tree"]').click();
      await expect(page.locator('[data-creation-map-status]')).toBeHidden();
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
    path: `tasks/evidence-hq-onboarding-reveal/group-3/${reduced ? 'reduced' : 'final'}.png`,
    fullPage: true
  });
  const hq = page.locator('.ws-map-tile').filter({ has: page.locator('.ws-map-tile-hq-badge') });
  await expect(hq).toBeVisible();
  await expect
    .poll(() =>
      hq.evaluate(el => {
        const box = el.getBoundingClientRect();
        return el.contains(
          document.elementFromPoint(box.x + box.width / 2, box.y + box.height / 2)
        );
      })
    )
    .toBe(true);
  await page.screenshot({
    path: 'tasks/evidence-hq-onboarding-reveal/group-2/ready.png',
    fullPage: true
  });
  await page.reload();
  await page.waitForFunction(() => Boolean((window as any).PersonalAssistantPanel?.isOpen));
  await expect(page.locator('#personalAssistantPanel')).toBeHidden();
  await expect(page.locator('[data-creation-map-status]')).toHaveCount(0);
});
