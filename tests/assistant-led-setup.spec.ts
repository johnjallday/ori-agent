import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import { existsSync, mkdtempSync, rmSync, statSync, utimesSync, writeFileSync } from 'node:fs';
import { homedir, tmpdir } from 'node:os';
import { join } from 'node:path';

/**
 * Real-domain assistant-led setup coverage.
 *
 * Run against its own fresh isolated sandbox, serially:
 *   ./scripts/smoke.sh serve 8951 assistant-led-setup-e2e
 *   ./scripts/e2e.sh --port 8951 tests/assistant-led-setup.spec.ts -- --workers=1
 *
 * The native OS dialog cannot be driven honestly in headless Playwright. This
 * spec uses the existing manual File Janitor API for that one folder grant,
 * then verifies that the assistant adopts the canonical grant. Foreground
 * native selection/cancellation/focus remains manual desktop evidence; no
 * fabricated selection token is used here.
 */

test.describe.configure({ mode: 'serial' });

let fixture = '';
let workspaceID = '';

async function setupProjection(request: APIRequestContext) {
  const response = await request.get('/api/personal-assistant/setup/file-janitor');
  expect(response.ok(), await response.text()).toBeTruthy();
  return (await response.json()).setup;
}

async function openAssistantSetup(page: Page) {
  await page.goto('/?quest=tidy-downloads');
  const later = page.locator('#skipOnboardingLink');
  if (await later.isVisible().catch(() => false)) await later.click();
  const card = page.locator('#assistantLedSetup');
  await expect(card).toBeVisible({ timeout: 20000 });
  return card;
}

test.afterAll(() => {
  if (fixture && fixture.startsWith(tmpdir()) && !fixture.startsWith(join(homedir(), 'Downloads'))) {
    rmSync(fixture, { recursive: true, force: true });
  }
});

test('no-model and pre-HQ setup reaches one real unselected review without moving files', async ({
  page,
  request
}) => {
  await request.post('/api/onboarding/skip');
  const relationshipResponse = await request.get('/api/personal-assistant');
  const relationship = (await relationshipResponse.json()).personal_assistant;
  test.skip(relationship?.state !== 'needs_hire', 'this spec requires its own fresh sandbox');

  const hire = await request.post('/api/personal-assistant/hire', {
    data: {
      request_id: `assistant-led-hire-${Date.now()}`,
      if_version: relationship.state_version ?? 0,
      display_name: 'Ari',
      appearance: { mode: 'generated', generated: {} },
      mandate: 'Help me plan each day and keep my files organized.',
      focus_areas: ['plan_my_day', 'organize_project_files']
    }
  });
  expect(hire.ok(), await hire.text()).toBeTruthy();

  const browserWrites: string[] = [];
  page.on('request', outgoing => {
    if (outgoing.method() === 'GET') return;
    const path = new URL(outgoing.url()).pathname;
    if (/provider|completion|chat|tasks\/(?:start|execute)|approv|apply|trash|move/i.test(path)) {
      browserWrites.push(`${outgoing.method()} ${path}`);
    }
    if (path.includes('/personal-assistant/setup/') && /(?:root_)?path/i.test(outgoing.postData() || '')) {
      browserWrites.push(`raw-path ${path}`);
    }
  });

  let card = await openAssistantSetup(page);
  await expect(card).toContainText('Set up for me');
  await expect(card).toContainText('does not read file contents');
  await card.getByRole('button', { name: 'Set up for me' }).click();
  await expect(card).toContainText('Choose the folder to tidy', { timeout: 20000 });

  const permission = await setupProjection(request);
  expect(permission.view_state).toBe('needs_permission');
  expect(permission.relationship.state).toBe('needs_hq');
  expect(permission.run.team_role.model_configured).toBe(false);
  workspaceID = permission.run.target_workspace_id;

  // #529: the sequence shown on the card and in the walkthrough is the server's
  // receipts, with canonical IDs equal to the API projection.
  expect(
    permission.milestones.map((milestone: any) => [
      milestone.id,
      milestone.status,
      milestone.ownership
    ])
  ).toEqual([
    ['workspace', 'created', 'created'],
    ['role:file-curator', 'created', 'created']
  ]);
  expect(permission.milestones[0].resource_id).toBe(workspaceID);
  await expect(page.locator('#assistantLedSetupDialogMode')).toContainText('Receipt walkthrough');
  const apiIDs = permission.milestones.map((milestone: any) => milestone.resource_id);
  const shownIDs = await page
    .locator('#assistantLedSetupMilestones li')
    .evaluateAll(items => items.map(item => (item as HTMLElement).dataset.resourceId));
  expect(shownIDs).toEqual(apiIDs);
  await page.locator('#assistantLedSetupDialogClose').click();
  await page.reload();
  card = await openAssistantSetup(page);
  const afterReload = await page
    .locator('#assistantLedSetupMilestones li')
    .evaluateAll(items => items.map(item => (item as HTMLElement).dataset.resourceId));
  expect(afterReload).toEqual(apiIDs);
  await card.getByRole('button', { name: 'View setup walkthrough' }).click();
  await expect(page.locator('#assistantLedSetupDialogMode')).toContainText('Receipt walkthrough');
  await page.keyboard.press('Escape');
  const replayed = await setupProjection(request);
  expect(replayed.run.id).toBe(permission.run.id);
  expect(replayed.run.revision).toBe(permission.run.revision);
  const workspaceOps = replayed.operations.filter(
    (operation: any) => operation.kind === 'workspace'
  );
  expect(workspaceOps).toHaveLength(1);
  expect(workspaceOps[0].attempt_count).toBe(1);

  // #528 boundaries are unchanged by the presentation: no root, no automation
  // approval, no scan, and the native picker was never opened.
  const janitor = await request.get(`/api/workspaces/${workspaceID}/file-janitor`);
  expect(janitor.ok(), await janitor.text()).toBeTruthy();
  const status = await janitor.json();
  expect(status.settings?.root_id ?? '').toBe('');
  expect(status.settings?.automation_approved_at ?? '').toBe('');
  expect(replayed.health?.root_generation_id ?? '').toBe('');
  expect(replayed.operations.some((operation: any) => operation.kind === 'initial_scan')).toBe(
    false
  );
  expect(browserWrites.some(write => write.includes('folder-picker'))).toBe(false);

  fixture = mkdtempSync(join(tmpdir(), 'assistant-led-setup-'));
  expect(fixture.startsWith(join(homedir(), 'Downloads'))).toBeFalsy();
  const report = join(fixture, 'report.pdf');
  const photo = join(fixture, 'photo.jpg');
  writeFileSync(report, 'fixture report');
  writeFileSync(photo, 'fixture photo');
  const settled = new Date(Date.now() - 60 * 60 * 1000);
  utimesSync(report, settled, settled);
  utimesSync(photo, settled, settled);
  const before = [statSync(report).size, statSync(photo).size];

  // Deliberate test bridge described above: this is the unchanged manual API,
  // not an assisted raw-path request and not evidence for the native picker.
  const grant = await request.post(`/api/workspaces/${workspaceID}/file-janitor/setup`, {
    data: { path: fixture, paused: true }
  });
  expect(grant.ok(), await grant.text()).toBeTruthy();
  expect(existsSync(join(fixture, 'Filed'))).toBeTruthy();

  await page.reload();
  card = await openAssistantSetup(page);
  await expect(card).toContainText('monitoring is still off');
  await expect(card).toContainText('create and rename events');
  await expect(card).toContainText('metadata only');
  await expect(card).toContainText('cannot move or Trash files');
  await card.getByRole('button', { name: 'Start monitoring and prepare review' }).click();

  await expect(card).toContainText('proposals are ready to review', { timeout: 20000 });
  await expect(card).toContainText('Nothing has moved');
  expect(statSync(report).size).toBe(before[0]);
  expect(statSync(photo).size).toBe(before[1]);
  expect(browserWrites).toEqual([]);

  await card.getByRole('link', { name: 'Review proposed filing' }).click();
  await expect(page).toHaveURL(/panel=file-janitor.*batch_id=/);
  const console = page.locator('#fileJanitorConsole');
  await expect(console).toBeVisible({ timeout: 20000 });
  await expect(console.locator('.fj-select:checked')).toHaveCount(0);
  await expect(console).toContainText('2 remaining of 2 candidates');
});

test('refreshing the delivered result reuses the same run and scan receipt', async ({ request }) => {
  test.skip(!workspaceID, 'requires the fresh-sandbox golden path');
  const first = await setupProjection(request);
  const second = await setupProjection(request);
  expect(second.run.id).toBe(first.run.id);
  expect(second.run.revision).toBe(first.run.revision);
  expect(second.first_result.batch_id).toBe(first.first_result.batch_id);
  const scans = second.operations.filter((operation: any) => operation.kind === 'initial_scan');
  expect(scans).toHaveLength(1);
  expect(scans[0].attempt_count).toBe(1);
});
