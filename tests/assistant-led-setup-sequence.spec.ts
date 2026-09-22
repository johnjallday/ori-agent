import { test, expect, type APIRequestContext, type Page } from '@playwright/test';

/**
 * Real-domain Assistant setup sequence (#529): two tabs, one run.
 *
 * Needs its own fresh isolated sandbox, serially:
 *   ./scripts/e2e-fresh.sh tests/assistant-led-setup-sequence.spec.ts -- --workers=1
 */

test.describe.configure({ mode: 'serial' });

async function setupProjection(request: APIRequestContext) {
  const response = await request.get('/api/personal-assistant/setup/file-janitor');
  expect(response.ok(), await response.text()).toBeTruthy();
  return (await response.json()).setup;
}

async function openCard(page: Page) {
  await page.goto('/?quest=tidy-downloads');
  const later = page.locator('#skipOnboardingLink');
  if (await later.isVisible().catch(() => false)) await later.click();
  const card = page.locator('#assistantLedSetup');
  await expect(card).toBeVisible({ timeout: 20000 });
  return card;
}

function listOf(payload: any, key: string): any[] {
  const value = payload?.[key] ?? payload?.data?.[key] ?? payload;
  return Array.isArray(value) ? value : Object.values(value || {});
}

async function counts(request: APIRequestContext) {
  const workspaces = listOf(await (await request.get('/api/workspaces')).json(), 'workspaces');
  const agents = listOf(await (await request.get('/api/agents')).json(), 'agents');
  return {
    workspaces: workspaces.filter(item => item?.name === 'File Janitor').length,
    curators: agents.filter(item => (item?.name ?? item) === 'File Curator').length
  };
}

async function renderedMilestones(page: Page) {
  return page
    .locator('#assistantLedSetupMilestones li')
    .evaluateAll(items =>
      items.map(item => [
        (item as HTMLElement).dataset.milestoneId,
        (item as HTMLElement).dataset.status,
        (item as HTMLElement).dataset.resourceId
      ])
    );
}

test('two tabs share one run, one workspace, and one File Curator', async ({
  browser,
  request
}) => {
  await request.post('/api/onboarding/skip');
  const relationship = (await (await request.get('/api/personal-assistant')).json())
    .personal_assistant;
  test.skip(relationship?.state !== 'needs_hire', 'this spec requires its own fresh sandbox');
  const hire = await request.post('/api/personal-assistant/hire', {
    data: {
      request_id: `assistant-setup-sequence-${Date.now()}`,
      if_version: relationship.state_version ?? 0,
      display_name: 'Ari',
      appearance: { mode: 'generated', generated: {} },
      mandate: 'Help me plan each day and keep my files organized.',
      focus_areas: ['plan_my_day', 'organize_project_files']
    }
  });
  expect(hire.ok(), await hire.text()).toBeTruthy();

  const tabA = await (await browser.newContext()).newPage();
  const tabB = await (await browser.newContext()).newPage();
  const writes: string[] = [];
  for (const tab of [tabA, tabB]) {
    tab.on('request', outgoing => {
      if (outgoing.method() !== 'GET')
        writes.push(`${outgoing.method()} ${new URL(outgoing.url()).pathname}`);
    });
  }
  const cardA = await openCard(tabA);
  const cardB = await openCard(tabB);
  await expect(cardA.getByRole('button', { name: 'Set up for me' })).toBeEnabled();
  await expect(cardB.getByRole('button', { name: 'Set up for me' })).toBeEnabled();

  // Tab A accepts. Preparation takes milliseconds, so what it sees is a
  // labelled receipt walkthrough, never "Creating".
  await cardA.getByRole('button', { name: 'Set up for me' }).click();
  const dialogA = tabA.locator('#assistantLedSetupDialog');
  await expect(dialogA).toBeVisible();
  await expect(dialogA).toContainText('Assistant setup · read-only');
  await expect(tabA.locator('#assistantLedSetupDialogMode')).toContainText('Receipt walkthrough', {
    timeout: 20000
  });
  await expect(dialogA).not.toContainText('Creating');
  expect(await dialogA.locator('form, input, textarea, select, [type="submit"]').count()).toBe(0);
  const accepted = await setupProjection(request);
  expect(accepted.milestones.map((m: any) => [m.id, m.status])).toEqual([
    ['workspace', 'created'],
    ['role:file-curator', 'created']
  ]);

  // Tab B still shows the old proposal and accepts the same review: it replays
  // the same run and opens the same receipts as a walkthrough.
  await cardB.getByRole('button', { name: 'Set up for me' }).click();
  await expect(tabB.locator('#assistantLedSetupDialogMode')).toContainText('Receipt walkthrough', {
    timeout: 20000
  });
  const replayed = await setupProjection(request);
  expect(replayed.run.id).toBe(accepted.run.id);
  expect(replayed.run.revision).toBe(accepted.run.revision);
  const workspaceOperation = replayed.operations.find((op: any) => op.kind === 'workspace');
  expect(workspaceOperation.status).toBe('succeeded');
  expect(workspaceOperation.attempt_count).toBe(1);
  await tabB.locator('#assistantLedSetupDialogClose').click();
  expect(await renderedMilestones(tabB)).toEqual(
    replayed.milestones.map((m: any) => [m.id, m.status, m.resource_id])
  );
  expect(await counts(request)).toEqual({ workspaces: 1, curators: 1 });

  // A reload rebuilds the same receipts, and replaying the walkthrough writes nothing.
  await tabA.reload();
  const reloaded = await openCard(tabA);
  await expect(reloaded.locator('#assistantLedSetupMilestones li')).toHaveCount(2);
  expect(await renderedMilestones(tabA)).toEqual(
    replayed.milestones.map((m: any) => [m.id, m.status, m.resource_id])
  );
  const writesBeforeReplay = writes.length;
  await reloaded.getByRole('button', { name: 'View setup walkthrough' }).click();
  await expect(tabA.locator('#assistantLedSetupDialogMode')).toContainText('Receipt walkthrough');
  await tabA.keyboard.press('Escape');
  await expect(tabA.locator('#assistantLedSetupDialog')).toBeHidden();
  await expect(reloaded.getByRole('button', { name: 'View setup walkthrough' })).toBeFocused();
  expect(writes.slice(writesBeforeReplay)).toEqual([]);
  expect(writes.filter(write => write.includes('/setup/file-janitor/accept'))).toHaveLength(2);
  expect(writes.filter(write => write.includes('folder-picker'))).toEqual([]);
  expect(await counts(request)).toEqual({ workspaces: 1, curators: 1 });
});
