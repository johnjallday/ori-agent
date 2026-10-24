import { expect, test, type APIRequestContext, type Locator, type Page } from '@playwright/test';
import { createHash, randomUUID } from 'node:crypto';
import { mkdirSync, readdirSync, readFileSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { revealFolderSetupDetails } from './helpers/folder-setup-card';
import { verifySavedFirstLook } from './helpers/folder-saved-report';

// One card, one click: a recognized project folder shows its whole plan as a
// hire, Hire & start runs it on the server, and the same card ends on a receipt.
// The authorized first look is separate; opening the workspace does not start it.
//
// Run it against the isolated demo sandbox (the folders are written into the
// server's HOME, which is the sandbox):
//
//   ./scripts/e2e.sh --env ORI_ONE_CARD_SANDBOX=<sandbox dir> --env ORI_ONE_CARD_PHASE=no-model tests/one-card-folder-setup.spec.ts
//
// Run again with ORI_ONE_CARD_PHASE=provider on a DIFFERENT fresh sandbox.
// Both phases are required for delivery. Configure an explicit provider/model
// for both: the no-model phase verifies the known-missing configuration gate
// and refuses an old model digest without execution; the provider phase reviews
// its intended model before the hire. Neither silently refreshes accepted work.
//
// It needs the reviewed REAPER integration and its Home provider to be available.
// This suite requires a complete plan: install/enable the reviewed packages
// through the offered guided setup first when the scan is unavailable. Record
// fresh-card installation separately; preinstallation is not that evidence.
// The steps that
// create agents also need explicit ORI_ONE_CARD_PROVIDER and ORI_ONE_CARD_MODEL;
// without them only the no-model path is exercised. Never select a signed-in
// CLI implicitly. An explicitly selected but refused model fails the run. No REAPER app is
// launched and no live control is granted.
const sandbox = process.env.ORI_ONE_CARD_SANDBOX || '';
const phase = process.env.ORI_ONE_CARD_PHASE || 'no-model';
const providerPhase = phase === 'provider';
const setupConfirmation =
  /^(?:(?:Hire|Assign) & start|(?:Install|Update|Enable) .+ & set up|Set (?:up|it up))$/;

async function expectPrimary(card: Locator) {
  await expect(
    card.getByRole('button', {
      name: providerPhase ? setupConfirmation : 'Configure project model',
      exact: true
    })
  ).toBeVisible();
}
// Slow local inference may take several tool rounds. Extend only the explicit
// live-report wait, never the required finished-report assertion.
const reportTimeout = Number(process.env.ORI_ONE_CARD_REPORT_TIMEOUT_MS || 180_000);
if (!Number.isSafeInteger(reportTimeout) || reportTimeout < 1000 || reportTimeout > 900_000) {
  throw new Error('ORI_ONE_CARD_REPORT_TIMEOUT_MS must be 1000–900000');
}
if (sandbox && !['no-model', 'provider'].includes(phase)) throw new Error('Unknown one-card phase');
test.skip(!sandbox, 'requires ORI_ONE_CARD_SANDBOX (the demo sandbox directory)');
test.describe.configure({ mode: 'serial' });
test.setTimeout(240_000);

const folderApi = '/api/personal-assistant/folder-digest';

async function ok(response: Awaited<ReturnType<APIRequestContext['get']>>) {
  const text = await response.text();
  expect(response.ok(), text).toBeTruthy();
  return text ? JSON.parse(text) : {};
}

function seedProject(chip: 'Desktop' | 'Documents' | 'Downloads', name: string) {
  const folder = chip === 'Desktop' ? join(sandbox, chip) : join(sandbox, chip, name);
  mkdirSync(folder, { recursive: true, mode: 0o750 });
  writeFileSync(join(folder, `${name}.rpp`), '<REAPER_PROJECT 0.1 "7.0"\n>\n', { mode: 0o600 });
  return folder;
}

function sourceSnapshot(folder: string): Record<string, string> {
  const entries: Record<string, string> = {};
  const visit = (relative = '') => {
    for (const entry of readdirSync(join(folder, relative), { withFileTypes: true })) {
      const name = join(relative, entry.name);
      expect(entry.isSymbolicLink(), 'no new source symlinks').toBe(false);
      if (entry.isDirectory()) {
        entries[`${name}/`] = 'directory';
        visit(name);
      } else
        entries[name] = createHash('sha256')
          .update(readFileSync(join(folder, name)))
          .digest('hex');
    }
  };
  visit();
  return entries;
}

async function workspaceIDs(request: APIRequestContext) {
  const listed = await ok(await request.get('/api/workspaces'));
  const rows = Array.isArray(listed) ? listed : listed.workspaces || listed.folders || [];
  return rows.map((row: { id: string }) => row.id).sort();
}

async function scan(request: APIRequestContext, chip: string) {
  const payload = await ok(await request.post(`${folderApi}/scan`, { data: { chip } }));
  return payload.offer;
}

async function openCard(page: Page) {
  await page.goto('/');
  // The app polls, so the network never goes idle; wait for the panel instead.
  await page.waitForFunction(() => Boolean((window as any).PersonalAssistantPanel?.open));
  await page.evaluate(() =>
    (window as any).PersonalAssistantPanel?.open?.(
      document.getElementById('personalAssistantLauncher')
    )
  );
  const card = page.locator('#personalAssistantFolderOffer');
  await expect(card).toBeVisible();
  await card.scrollIntoViewIfNeeded();
  await revealFolderSetupDetails(card);
  return card;
}

async function selectExplicitModel(request: APIRequestContext) {
  const provider = process.env.ORI_ONE_CARD_PROVIDER || '';
  const model = process.env.ORI_ONE_CARD_MODEL || '';
  if (!provider && !model) return false;
  expect(provider, 'explicit provider required').not.toBe('');
  expect(model, 'explicit model required').not.toBe('');
  await ok(await request.post('/api/settings/system-model', { data: { provider, model } }));
  return true;
}

test('a hired assistant with a recognized project folder shows the whole plan', async ({
  page,
  request
}) => {
  await request.post('/api/onboarding/skip');
  await ok(await request.post('/api/settings/workspace-root', { data: { workspace_root: '' } }));
  if (providerPhase)
    expect(await selectExplicitModel(request), 'provider phase requires explicit model').toBe(true);
  const relationship = (await ok(await request.get('/api/personal-assistant'))).personal_assistant;
  if (relationship.state === 'needs_hire') {
    await ok(
      await request.post('/api/personal-assistant/hire', {
        data: {
          request_id: 'one-card-hire',
          if_version: relationship.state_version ?? 0,
          display_name: 'Atlas',
          mandate: 'Keep my projects moving.',
          focus_areas: ['plan_my_day']
        }
      })
    );
    const hired = (await ok(await request.get('/api/personal-assistant'))).personal_assistant;
    await ok(
      await request.post('/api/personal-assistant/hq', {
        data: { request_id: 'one-card-hq', if_version: hired.state_version, name: 'My HQ' }
      })
    );
  }
  seedProject('Desktop', 'Session');
  seedProject('Downloads', 'Third');

  const offer = await scan(request, 'desktop');
  expect(offer.plan?.lines?.length).toBeGreaterThanOrEqual(6);
  expect(offer.plan.digest).toMatch(/^[0-9a-f]{64}$/);

  const card = await openCard(page);
  const lines = card.locator('#personalAssistantFolderPlan li');
  await expect(lines.first()).toBeVisible();
  const text = (await lines.allInnerTexts()).join('\n');
  // Every consequence is on the card, and none is a filesystem path.
  expect(text).toMatch(/File-only mode/);
  expect(text).toMatch(/where it is/);
  expect(text).toMatch(/agents/);
  expect(text).toMatch(/first read-only look, started right away/);
  expect(text).not.toMatch(/\/Users\/|\/var\/|\/private\//);
  // The plan is a hire: who is hired for the folder, one labelled question that
  // starts from a default brief, and the lines under their heading.
  await expect(card.locator('#personalAssistantFolderOfferHeadline')).toHaveText(
    /^(Hire|Assign) .+ (for|to) Desktop$/
  );
  await expect(card.getByLabel('What should it do with it?')).toHaveValue(/\S/);
  await expect(card.locator('#personalAssistantFolderPlanHeadingText')).toHaveText(
    'Also, in the background'
  );
  expect(offer.plan.visual?.model?.state).toBe(providerPhase ? 'configured' : 'missing');
  await expectPrimary(card);
  await expect(card.getByRole('button', { name: 'Not now', exact: true })).toBeVisible();
  await expect(card.getByRole('button', { name: 'Adjust', exact: true })).toBeVisible();

  // Phone width: the plan wraps instead of scrolling the page sideways.
  await page.setViewportSize({ width: 390, height: 900 });
  await expect(card).toBeVisible();
  const overflow = await card.evaluate(el => el.scrollWidth - el.clientWidth);
  expect(overflow).toBeLessThanOrEqual(1);
  const pageOverflow = await page.evaluate(
    () => document.documentElement.scrollWidth - document.documentElement.clientWidth
  );
  expect(pageOverflow).toBeLessThanOrEqual(1);
});

test('Adjust… opens today’s step-by-step journey instead of running the plan', async ({
  page,
  request
}) => {
  const pluginNames = async () =>
    ((await ok(await request.get('/api/plugins'))).plugins || [])
      .map((row: { name: string }) => row.name)
      .sort();
  await scan(request, 'desktop');
  const before = await pluginNames();
  const card = await openCard(page);
  await card.getByRole('button', { name: 'Adjust', exact: true }).click();
  const modal = page.locator('#specialistSetupJourneyModal');
  await expect(modal).toBeVisible();
  await expect(page.locator('#specialistSetupJourneyTitle')).not.toBeEmpty();
  // Nothing was run: pressing Adjust… installed and enabled nothing, and the card
  // shows no run in progress.
  expect(await pluginNames()).toEqual(before);
  await expect(page.locator('#personalAssistantFolderPlan[data-mode="running"]')).toHaveCount(0);

  // Leaving the journey does not lose the one-click plan: the resumed card offers
  // Set up beside Continue setup.
  await page.locator('#specialistSetupJourneyClose').click();
  await expect(modal).toBeHidden();
  await page.reload();
  const resumed = await openCard(page);
  await expect(resumed).toContainText('Project setup has not finished');
  await expectPrimary(resumed);
  await expect(resumed.getByRole('button', { name: 'Continue setup' })).toBeVisible();
  await expect(resumed.locator('#personalAssistantFolderPlan li').first()).toBeVisible();
});

test('a known missing project model blocks confirmation without manufacturing a paused run', async ({
  page,
  request
}) => {
  test.skip(providerPhase, 'no-model phase only');
  const before = await workspaceIDs(request);
  const source = join(sandbox, 'Downloads', 'Third');
  const original = sourceSnapshot(source);
  const offer = await scan(request, 'downloads');
  expect(offer.plan?.digest).toBeTruthy();
  expect(offer.plan.visual?.model?.state).toBe('missing');
  expect(offer.setup).toBeFalsy();
  const writes: string[] = [];
  page.on(
    'request',
    r => r.method() === 'POST' && /\/setup$/.test(r.url()) && writes.push(r.url())
  );
  const card = await openCard(page);
  await expectPrimary(card);
  await expect(card.getByRole('region', { name: 'Project agent model' })).toContainText(
    'Configuration needed'
  );
  await expect(card.getByRole('button', { name: setupConfirmation })).toHaveCount(0);
  await expect(card.locator('[aria-label="Saved setup progress"]')).toHaveCount(0);
  await page.locator('#personalAssistantClose').click();
  await page.locator('#personalAssistantLauncher').click();
  await expectPrimary(card);
  expect(writes).toEqual([]);
  expect(await workspaceIDs(request)).toEqual(before);
  expect(sourceSnapshot(source)).toEqual(original);
  await expect(card.locator('#personalAssistantFolderReceipt')).toBeHidden();
});

test('a configured model invalidates the old review digest before effects', async ({
  page,
  request
}) => {
  test.skip(providerPhase, 'no-model phase only');
  test.skip(
    !process.env.ORI_ONE_CARD_PROVIDER && !process.env.ORI_ONE_CARD_MODEL,
    'explicit model needed to exercise changed consent'
  );
  const before = await workspaceIDs(request);
  const source = join(sandbox, 'Downloads', 'Third');
  const original = sourceSnapshot(source);
  const pending = (await ok(await request.get(folderApi))).folder_digest.offer;
  expect(pending.status).toBe('pending');
  expect(pending.setup).toBeFalsy();
  const oldDigest = pending.plan.digest;
  expect(await selectExplicitModel(request)).toBe(true);
  const refused = await request.post(
    `${folderApi}/offers/${encodeURIComponent(pending.id)}/setup`,
    {
      data: { request_id: randomUUID(), plan_digest: oldDigest }
    }
  );
  expect(refused.status()).toBe(409);
  expect((await refused.json()).plan_changed).toBe(true);
  expect(await workspaceIDs(request)).toEqual(before);
  const card = await openCard(page);
  await expect(card.getByRole('region', { name: 'Project agent model' })).toContainText(
    'Configured'
  );
  await expect(card.getByRole('button', { name: setupConfirmation })).toBeVisible();
  const refreshed = (await ok(await request.get(folderApi))).folder_digest.offer;
  expect(refreshed.plan.digest).not.toBe(oldDigest);
  expect(refreshed.setup).toBeFalsy();
  expect(sourceSnapshot(source)).toEqual(original);
});

test('a hire with the reviewed model finishes on the same card and produces the first report', async ({
  page,
  request
}, testInfo) => {
  test.skip(!providerPhase, 'fresh provider phase only');
  test.setTimeout(reportTimeout + 60_000);
  const source = join(sandbox, 'Downloads', 'Third');
  const original = sourceSnapshot(source);
  await scan(request, 'downloads');
  const startCalls: string[] = [];
  page.on('response', async response => {
    if (
      response.url().endsWith('/folder-first-task/start') &&
      response.request().method() === 'POST'
    ) {
      startCalls.push(JSON.stringify(await response.json().catch(() => ({}))));
    }
  });
  const card = await openCard(page);
  await card
    .locator('#personalAssistantFolderBrief')
    .fill(
      'Read the primary REAPER file once, using the directory ID returned by workspace_directories, not its name. Summarize only its actual contents in a concise final answer; reuse those findings rather than calling the same tools again.'
    );
  await card.getByRole('button', { name: 'Hire & start', exact: true }).click();
  const receipt = card.locator('#personalAssistantFolderReceipt li');
  await expect(receipt.first()).toBeVisible({ timeout: 180_000 });
  const rows = (await receipt.allInnerTexts()).join('\n');
  expect(rows).toMatch(/Workspace/);
  expect(rows).toMatch(/Agent/);
  // The song's agent is the Home's one shared assistant, not "<label> · <song>".
  expect(rows).toMatch(/REAPER Assistant[^\n·]*added/);
  expect(rows).toMatch(/First task/);

  await expect(card.locator('.folder-visual')).toContainText('Confirmed workspace member');
  await expect(page.locator('#folderSetupRunModal')).toHaveCount(0);
  // The hire's consent started the look on the server; the page never asked to.
  const receiptCard = page.locator('#personalAssistantFolderOffer');
  await expect(receiptCard.locator('#personalAssistantFolderPlan')).toContainText(
    'Reading the project, read-only'
  );
  await expect(receiptCard.getByRole('button', { name: 'Start first look' })).toHaveCount(0);
  expect(startCalls).toEqual([]);
  const open = receiptCard
    .locator('a[href^="/workspaces/"]')
    .filter({ hasText: /^Open / })
    .filter({ hasNotText: /report|assessment|Home/ })
    .first();
  await expect(open).toBeVisible();
  const href = await open.getAttribute('href');
  expect(href).toMatch(/^\/workspaces\/[a-z0-9-]+$/);
  // The card follows the look on Home, with no page change.
  await expect(receiptCard.locator('#personalAssistantFolderReceipt')).toContainText(
    /Running…|Done|Did not finish/,
    {
      timeout: 60_000
    }
  );

  // Actual provider-backed acceptance needs a finished report, not merely a
  // configured model or a ready workspace with a failed first look.
  await expect(card.getByRole('link', { name: 'Open the full report', exact: true })).toBeVisible({
    timeout: reportTimeout
  });
  await page.setViewportSize({ width: 1440, height: 1100 });
  const details = card.locator('#personalAssistantFolderDetails');
  if ((await details.getAttribute('open')) !== null)
    await details.locator('summary').first().click();
  await card.locator('.folder-visual').scrollIntoViewIfNeeded();
  await page.screenshot({ path: testInfo.outputPath('live-ready-report.png') });

  // Closing/reopening preserves the result. The shared read-only tail checks
  // workspace/report navigation without repeating expensive live inference.
  const before = startCalls.length;
  const ids = await workspaceIDs(request);
  await page.locator('#personalAssistantClose').click();
  await page.locator('#personalAssistantLauncher').click();
  expect(await workspaceIDs(request)).toEqual(ids);
  expect(sourceSnapshot(source)).toEqual(original);
  const savedOfferID = await page.evaluate(
    () => (window as any).PersonalAssistantFolder.current()?.id
  );
  const reportHref = await card
    .getByRole('link', { name: 'Open the full report', exact: true })
    .getAttribute('href');
  expect(reportHref).toBe(await verifySavedFirstLook(page, request, sandbox, savedOfferID, href!));
  expect(startCalls.length).toBe(before);
});

test('keyboard only: Assign & start runs from the focused button and the card announces its progress', async ({
  page,
  request
}) => {
  test.skip(!providerPhase, 'fresh provider phase only');
  const source = seedProject('Documents', 'Fourth');
  const original = sourceSnapshot(source);
  await scan(request, 'documents');
  const card = await openCard(page);
  // A later song in the same Home is assigned the Home's one shared assistant.
  await expect(card.locator('#personalAssistantFolderOfferHeadline')).toHaveText(
    'Assign REAPER Assistant to Fourth'
  );
  // The status line is a polite live region, so progress is spoken, not just shown.
  await expect(card.locator('#personalAssistantFolderOfferNote')).toHaveAttribute(
    'aria-live',
    'polite'
  );
  const setUp = card.getByRole('button', { name: 'Assign & start', exact: true });
  await setUp.focus();
  await expect(setUp).toBeFocused();
  await page.keyboard.press('Enter');
  const receipt = card.locator('#personalAssistantFolderReceipt li');
  await expect(receipt.first()).toBeVisible({ timeout: 180_000 });
  // A later song in the same Home joins the assistant the first one added.
  expect((await receipt.allInnerTexts()).join('\n')).toMatch(/REAPER Assistant[^\n·]*joined/);
  const agents = await ok(await request.get('/api/agents/dashboard/list'));
  const names = (Array.isArray(agents) ? agents : agents.agents || []).map(
    (agent: { name: string }) => agent.name
  );
  expect(names.filter((name: string) => name.startsWith('REAPER Assistant'))).toEqual([
    'REAPER Assistant'
  ]);
  // The actual workspace link remains keyboard reachable in the same card.
  const open = card.getByRole('link', { name: 'Open Fourth', exact: true });
  await open.focus();
  await expect(open).toBeFocused();
  expect(sourceSnapshot(source)).toEqual(original);
});
