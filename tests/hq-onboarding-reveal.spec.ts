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

test('confirmed customized payload builds exactly one real HQ with reviewed Notes', async ({
  page,
  request
}) => {
  await page.goto('/');
  await page.waitForFunction(() => Boolean((window as any).PersonalAssistantHQCard?.show));
  await page.locator('#cockpitWelcome [data-home-welcome-action="primary"]').click();
  await expect(page.locator('#personalAssistantHQNotesPath')).toBeVisible();
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
  const builds: Record<string, unknown>[] = [];
  page.on('request', r => {
    if (r.method() === 'POST' && new URL(r.url()).pathname === '/api/personal-assistant/hq')
      builds.push(r.postDataJSON());
  });
  await page.locator('#personalAssistantHQBuild').click();
  await expect
    .poll(
      async () =>
        (await (await request.get('/api/personal-assistant')).json()).personal_assistant.state
    )
    .toBe('active');
  expect(builds).toHaveLength(1);
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
});
