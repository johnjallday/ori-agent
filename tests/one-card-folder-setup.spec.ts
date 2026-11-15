import { expect, test, type APIRequestContext, type Locator, type Page } from '@playwright/test';
import { createHash, randomUUID } from 'node:crypto';
import { mkdirSync, readdirSync, readFileSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { createServer, type Server } from 'node:http';
import { revealFolderSetupDetails } from './helpers/folder-setup-card';
import { verifySavedFirstLook } from './helpers/folder-saved-report';
import { stageReviewedFolderPackages } from './helpers/reviewed-folder-packages';
import { openFolderAttach, chooseAttach } from './helpers/assistant-menus';
import {
  chooseRetainedHomeTeam,
  openRetainedMusicDialog,
  retainedMusicOptions,
  resumeRetainedMusicReview,
  returnToRetainedMusicReview,
  openRetainedSongFromHome
} from './helpers/retained-music-review';
import { installLocalCdn } from './helpers/offline-cdn';

// Retained source reviews keep one canonical owner through explicit Home-team
// choices, Home setup, and the manager's separate project Open. New folder feeds
// use the hire-first card (covered independently by folder-agent-explore).
// The optional summary still needs its own explicit workspace consent.
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
const adjustPhase = phase === 'adjust';
const alternateModel =
  process.env.ORI_ONE_CARD_ALTERNATE_MODEL ||
  (process.env.ORI_ONE_CARD_SCRIPTED_PORT ? 'one-card-fixture-alternate' : '');
const setupConfirmation = /^Return to setup review$/;

async function expectPrimary(card: Locator) {
  await expect(
    card.getByRole('button', {
      name: setupConfirmation,
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
if (sandbox && !['no-model', 'provider', 'adjust'].includes(phase))
  throw new Error('Unknown one-card phase');
test.skip(!sandbox, 'requires ORI_ONE_CARD_SANDBOX (the demo sandbox directory)');
test.describe.configure({ mode: 'serial' });
test.setTimeout(240_000);

const folderApi = '/api/personal-assistant/folder-digest';
test.beforeEach(async ({ page }) => installLocalCdn(page));

async function ok(response: Awaited<ReturnType<APIRequestContext['get']>>) {
  const text = await response.text();
  expect(response.ok(), text).toBeTruthy();
  return text ? JSON.parse(text) : {};
}

// Optional deterministic runner; ordinary manual/live invocations are unchanged.
// The wrapper explicitly supplies this port and isolates every server/package.
let scriptedModel: Server | undefined;
test.beforeAll(async ({ request }) => {
  const port = Number(process.env.ORI_ONE_CARD_SCRIPTED_PORT || 0);
  if (!port) return;
  scriptedModel = createServer(async (req, res) => {
    res.setHeader('Content-Type', 'application/json');
    if (req.method === 'GET')
      return res.end(
        JSON.stringify({
          data: ['one-card-fixture', alternateModel]
            .filter(Boolean)
            .map(id => ({ id, object: 'model' }))
        })
      );
    let data = '';
    for await (const chunk of req) data += chunk;
    const body = JSON.parse(data);
    const brief = body.messages?.some((message: any) =>
      String(message.content).startsWith('You write a concise daily brief')
    );
    const followup = body.messages?.some((message: any) =>
      String(message.content).includes('Discuss the next step without starting work')
    );
    const content = brief
      ? JSON.stringify({ opening_summary: 'Unrelated scripted Daily Brief fixture.' })
      : followup
        ? 'We can discuss the next step. No additional task was started.'
        : '## Saved session\nThe supplied session fixture declares no tracks. No audio was analyzed or history inferred.\n\nNext: Review the saved session before recording.';
    res.end(
      JSON.stringify({
        id: 'one-card-fixture',
        model: body.model,
        choices: [{ index: 0, finish_reason: 'stop', message: { role: 'assistant', content } }]
      })
    );
  });
  await new Promise<void>((resolve, reject) => {
    scriptedModel!.once('error', reject);
    scriptedModel!.listen(port, '127.0.0.1', resolve);
  });
  await stageReviewedFolderPackages(request);
});
test.afterAll(async () => {
  if (scriptedModel) await new Promise<void>(resolve => scriptedModel!.close(() => resolve()));
});

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

let scopedOfferID = '';
let scopedConversationID = '';
async function scan(
  page: Page,
  _request: APIRequestContext,
  chip: string,
  { chooseTeam = true, draft = '' } = {}
) {
  await page.goto('/?panel=today');
  await page.waitForFunction(() => Boolean((window as any).PersonalAssistantPanel?.open));
  await page.evaluate(() =>
    (window as any).PersonalAssistantPanel.open(
      document.getElementById('personalAssistantLauncher')
    )
  );
  if (draft) await page.locator('#personalAssistantInput').fill(draft);
  await openFolderAttach(page);
  const selected = page.waitForResponse(
    response =>
      response.request().method() === 'POST' &&
      new URL(response.url()).pathname === '/api/home-assistant/folder-context/select'
  );
  await page
    .locator('#personalAssistantFolderChoices')
    .getByRole('button', { name: chip[0].toUpperCase() + chip.slice(1), exact: true })
    .click();
  const selectionResponse = await selected;
  expect(selectionResponse.ok()).toBeTruthy();
  const selection = await selectionResponse.json();
  const expected = chip === 'downloads' ? 'Third' : chip === 'documents' ? 'Fourth' : 'Desktop';
  // The real selection says whether it is opening a single canonical review.
  // Do not race that request with a second chooser that will be removed by it.
  if (!selection.hire_candidate) {
    await chooseAttach(page, '#personalAssistantReviewFolderSetup');
    await expect(page.locator('#personalAssistantFolderSetupChoices')).toBeVisible();
    const label = await page
      .locator('#personalAssistantFolderSetupCandidate option')
      .evaluateAll(
        (nodes, name) =>
          nodes.map(node => node.textContent || '').find(text => text.startsWith(name)) || '',
        expected
      );
    expect(label).not.toBe('');
    await page.locator('#personalAssistantFolderSetupCandidate').selectOption({ label });
    await page.locator('#personalAssistantFolderSetupReview').click({ timeout: 10_000 });
  }
  await page.waitForFunction(
    name => {
      const current = (window as any).PersonalAssistantFolder.current();
      return current?.subject?.name === name && Boolean(current.id);
    },
    expected,
    { timeout: 30_000 }
  );
  await openRetainedMusicDialog(page);
  if (chooseTeam)
    await chooseRetainedHomeTeam(page, {
      provider: process.env.ORI_ONE_CARD_PROVIDER || '',
      model: process.env.ORI_ONE_CARD_MODEL || ''
    });
  const offer = await page.evaluate(() => (window as any).PersonalAssistantFolder.current());
  scopedOfferID = offer.id;
  scopedConversationID = offer.conversation_id;
  return offer;
}

async function openCard(page: Page) {
  if (!page.url().includes('panel=today'))
    await resumeRetainedMusicReview(page, scopedConversationID);
  // The app polls, so the network never goes idle; wait for the panel instead.
  await page.waitForFunction(() => Boolean((window as any).PersonalAssistantPanel?.open));
  await page.evaluate(() =>
    (window as any).PersonalAssistantPanel?.open?.(
      document.getElementById('personalAssistantLauncher')
    )
  );
  const card = await retainedMusicOptions(page);
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

test('a retained source reviews its Home half and requires an explicit team before a digest', async ({
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

  const before = await workspaceIDs(request);
  const offer = await scan(page, request, 'desktop', { chooseTeam: false });
  expect(offer.home_staffing.state).toBe('needs_choice');
  expect(offer.plan.digest).toBe('');
  const card = await openCard(page);
  await expect(
    card.getByRole('button', { name: 'Choose the Home team first', exact: true })
  ).toBeDisabled();
  await chooseRetainedHomeTeam(page, {
    provider: process.env.ORI_ONE_CARD_PROVIDER || '',
    model: process.env.ORI_ONE_CARD_MODEL || ''
  });
  const ready = await page.evaluate(() => (window as any).PersonalAssistantFolder.current());
  expect(ready.plan.digest).toMatch(/^[0-9a-f]{64}$/);
  expect(ready.home_staffing.choices[0]).toMatchObject({
    mode: 'create',
    provider: process.env.ORI_ONE_CARD_PROVIDER,
    model: process.env.ORI_ONE_CARD_MODEL
  });
  const lines = card.locator('#personalAssistantFolderPlan li');
  await expect(lines.first()).toBeVisible();
  const text = (await lines.allInnerTexts()).join('\n');
  expect(text).toContain('Creates your Music Production Home');
  expect(text).toContain('Staff the Home with your reviewed team');
  expect(text).toContain("Lists Desktop in the Home's library");
  expect(text).toContain('then suggests opening Desktop');
  // The source shows this press's Home effects, not a promise to open the song.
  expect(text).not.toMatch(
    /File-only mode|optional read-only summary|\/Users\/|\/var\/|\/private\//
  );
  expect(ready.plan.lines.some((line: any) => line.kind === 'handoff')).toBe(true);
  await expectPrimary(card);
  const review = await returnToRetainedMusicReview(page);
  await expect(review).toContainText('does not open its workspace or start its ticket');
  await expect(
    review.getByRole('textbox', { name: 'Later project goal', exact: true })
  ).toHaveValue(/\S/);
  await page.setViewportSize({ width: 390, height: 900 });
  expect(await review.evaluate(el => el.scrollWidth - el.clientWidth)).toBeLessThanOrEqual(1);
  expect(
    await page.evaluate(() => document.documentElement.scrollWidth - innerWidth)
  ).toBeLessThanOrEqual(1);
  expect(await workspaceIDs(request)).toEqual(before);
  await retainedMusicOptions(page);
  await card.getByRole('button', { name: 'Keep chatting', exact: true }).click();
  await expect(card).toBeHidden();
});

test('Adjust… opens today’s step-by-step journey instead of running the plan', async ({
  page,
  request
}) => {
  test.skip(!adjustPhase, 'dedicated fresh Adjust phase preserves its unfinished journey');
  const pluginNames = async () =>
    ((await ok(await request.get('/api/plugins'))).plugins || [])
      .map((row: { name: string }) => row.name)
      .sort();
  await scan(page, request, 'desktop');
  const before = await pluginNames();
  const card = await openCard(page);
  await card.getByRole('button', { name: 'Adjust…', exact: true }).click();
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
  // Its unfinished journey is intentionally preserved. This phase has its
  // own sandbox, so later tests never bypass that pending review's ownership.
});

test('a missing Default model leaves a staged Home agent unconfirmable without a paused run', async ({
  page,
  request
}) => {
  test.skip(providerPhase || adjustPhase, 'no-model phase only');
  const before = await workspaceIDs(request);
  const source = join(sandbox, 'Downloads', 'Third');
  const original = sourceSnapshot(source);
  const offer = await scan(page, request, 'downloads', { chooseTeam: false });
  expect(offer.plan.digest).toBe('');
  expect(offer.home_staffing.state).toBe('needs_choice');
  expect(offer.setup).toBeFalsy();
  const writes: string[] = [];
  page.on(
    'request',
    r => r.method() === 'POST' && /\/setup$/.test(r.url()) && writes.push(r.url())
  );
  const card = await openCard(page);
  await card
    .getByRole('button', { name: 'Create an agent for Music Portfolio Manager', exact: true })
    .click();
  const form = card.locator('.folder-home-staffing__editor');
  await form.locator('[data-agent-create-field="name"]').fill('Unconfigured Portfolio Manager');
  await expect(form.locator('[data-agent-create-field="model"]')).toHaveValue('');
  const rejected = page.waitForResponse(
    response =>
      response.request().method() === 'POST' && /\/offers\/[^/]+\/adjust$/.test(response.url())
  );
  await form.getByRole('button', { name: 'Use for this role', exact: true }).click();
  const draftReply = await rejected;
  expect(draftReply.ok(), await draftReply.text()).toBe(true);
  const invalid = await page.evaluate(() => (window as any).PersonalAssistantFolder.current());
  expect(invalid.home_staffing.state).toBe('stale');
  expect(invalid.home_staffing.choice_problems.portfolio_manager).toMatch(/model/i);
  expect(invalid.plan.digest).toBe('');
  await expect(
    card.getByRole('button', { name: 'Choose the Home team first', exact: true })
  ).toBeDisabled();
  await expect(card.locator('[aria-label="Saved setup progress"]')).toHaveCount(0);
  await page.getByRole('button', { name: 'Close setup review', exact: true }).click();
  await page.locator('#personalAssistantClose').click();
  await page.locator('#personalAssistantLauncher').click();
  await openCard(page);
  const stillPending = await page.evaluate(() => (window as any).PersonalAssistantFolder.current());
  expect(stillPending.setup).toBeFalsy();
  expect(stillPending.plan.digest).toBe('');
  expect(writes).toEqual([]);
  expect(await workspaceIDs(request)).toEqual(before);
  expect(sourceSnapshot(source)).toEqual(original);
  await expect(card.locator('#personalAssistantFolderReceipt')).toBeHidden();
});

test('reviewed Home model edits invalidate consent; unrelated Default changes do not rewrite it', async ({
  page,
  request
}) => {
  test.skip(providerPhase || adjustPhase, 'no-model phase only');
  test.skip(
    !process.env.ORI_ONE_CARD_PROVIDER && !process.env.ORI_ONE_CARD_MODEL,
    'explicit model needed to exercise changed consent'
  );
  expect(
    alternateModel,
    'provide an explicit alternate model for the model-edit consent check'
  ).not.toBe('');
  const before = await workspaceIDs(request);
  const source = join(sandbox, 'Downloads', 'Third');
  const original = sourceSnapshot(source);
  const pending = (
    await ok(await request.get(`${folderApi}?offer_id=${encodeURIComponent(scopedOfferID)}`))
  ).folder_digest.offer;
  expect(pending.status).toBe('pending');
  expect(pending.setup).toBeFalsy();
  await openCard(page);
  await chooseRetainedHomeTeam(page, {
    provider: process.env.ORI_ONE_CARD_PROVIDER || '',
    model: process.env.ORI_ONE_CARD_MODEL || ''
  });
  const staged = await page.evaluate(() => (window as any).PersonalAssistantFolder.current());
  const oldDigest = staged.plan.digest;
  expect(oldDigest).toMatch(/^[0-9a-f]{64}$/);
  expect(await selectExplicitModel(request)).toBe(true);
  const unchanged = (await ok(await request.get(`${folderApi}?offer_id=${pending.id}`)))
    .folder_digest.offer;
  expect(unchanged.plan.digest).toBe(oldDigest);
  expect(unchanged.home_staffing.choices[0].model).toBe(process.env.ORI_ONE_CARD_MODEL);
  const editable = await openCard(page);
  const clear = editable.getByRole('button', {
    name: 'Clear Music Portfolio Manager',
    exact: true
  });
  await clear.click();
  // Clear is asynchronous: do not let the next helper reuse the old ready
  // choice before the source-owned response has actually replaced it.
  await expect(clear).toHaveCount(0);
  await expect
    .poll(() =>
      page.evaluate(() => {
        const team = (window as any).PersonalAssistantFolder.current()?.home_staffing;
        return { state: team?.state, choices: team?.choices || [] };
      })
    )
    .toEqual({ state: 'needs_choice', choices: [] });
  await chooseRetainedHomeTeam(page, {
    provider: process.env.ORI_ONE_CARD_PROVIDER || '',
    model: alternateModel
  });
  const changed = await page.evaluate(() => (window as any).PersonalAssistantFolder.current());
  expect(changed.home_staffing.choices[0].model).toBe(alternateModel);
  expect(changed.plan.digest).not.toBe(oldDigest);
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
  await expect(card).toBeVisible();
  const refreshed = (
    await ok(await request.get(`${folderApi}?offer_id=${encodeURIComponent(scopedOfferID)}`))
  ).folder_digest.offer;
  expect(refreshed.plan.digest).not.toBe(oldDigest);
  expect(refreshed.setup).toBeFalsy();
  expect(sourceSnapshot(source)).toEqual(original);
});

test('Home setup and the manager Open end ready; the workspace separately starts its summary', async ({
  page,
  request
}, testInfo) => {
  test.skip(!providerPhase, 'fresh provider phase only');
  test.setTimeout(reportTimeout + 60_000);
  const source = join(sandbox, 'Downloads', 'Third');
  const original = sourceSnapshot(source);
  const exactDraft = '  Unsent 界 draft\n keep exact  ';
  await scan(page, request, 'downloads', { draft: exactDraft });
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
  const conversationId = await page.evaluate(() =>
    (window as any).PersonalAssistantConversation.currentId()
  );
  const composer = page.locator('#personalAssistantInput');
  const brief =
    'Summarize the permitted saved REAPER session. Report missing track data honestly and suggest one next step; do not run or change anything.';
  const review = await returnToRetainedMusicReview(page);
  const goal = review.getByRole('textbox', { name: 'Later project goal', exact: true });
  await goal.fill(brief);
  await goal.press('Tab');
  await review.getByRole('button', { name: 'Use automatic placement', exact: true }).click();
  await review.getByRole('button', { name: 'Set up Home', exact: true }).click();
  await expect(review.getByRole('heading', { name: 'Home ready', exact: true })).toBeVisible({
    timeout: 120_000
  });
  const homes = (await ok(await request.get('/api/workspaces'))).folders.filter(
    (item: any) => item.kind === 'group'
  );
  expect(homes).toHaveLength(1);
  const home = homes[0];
  expect(
    (await ok(await request.get('/api/workspaces'))).folders.filter(
      (item: any) => item.parent_id === home.id
    )
  ).toHaveLength(0);
  expect(startCalls).toEqual([]);
  await page.getByRole('button', { name: 'Close setup review', exact: true }).click();
  await openRetainedSongFromHome(page, home, 'Third', brief);
  await resumeRetainedMusicReview(page, conversationId);
  await openCard(page);
  await expect(card.locator('#personalAssistantFolderOfferHeadline')).toContainText('is ready', {
    timeout: 180_000
  });
  // The retained requirements editor deliberately opened Setup details;
  // reopening its saved source keeps that disclosure state. Exercise the
  // compact result explicitly rather than assuming a navigation resets it.
  const savedDetails = card.locator('#personalAssistantFolderDetails');
  if ((await savedDetails.getAttribute('open')) !== null)
    await savedDetails.locator('summary').first().click();
  await expect(card.locator('#personalAssistantFolderReceipt')).toBeHidden();
  await expect(composer).toHaveValue(exactDraft);
  await expect(composer).toHaveAttribute('placeholder', 'What would you like to do next?');
  await revealFolderSetupDetails(card);
  const receipt = card.locator('#personalAssistantFolderReceipt li');
  await expect(receipt.first()).toBeVisible();
  const rows = (await receipt.allTextContents()).join('\n');
  expect(rows).toMatch(/Workspace/);
  expect(rows).toMatch(/Agent/);
  // The song's agent is the Home's one shared assistant, not "<label> · <song>".
  expect(rows).toMatch(/REAPER Assistant[^\n·]*added/);
  expect(rows).toMatch(/First ticket/);

  await expect(card.locator('.folder-visual')).toContainText('Assigned here');
  await expect(page.locator('#folderSetupRunModal')).toHaveCount(0);
  const receiptCard = page.locator('#personalAssistantFolderOffer');
  await expect(card.locator('#personalAssistantFolderOfferHeadline')).toContainText('is ready');
  await expect(card.locator('#personalAssistantFolderOptionalTask')).toBeHidden();
  await expect(page.locator('#personalAssistantActiveFolder')).toBeHidden();
  await expect(card.getByRole('button', { name: 'Start summary', exact: true })).toHaveCount(0);
  expect(
    await page.evaluate(() => (window as any).PersonalAssistantFolderContext.request())
  ).toBeNull();
  expect(startCalls).toEqual([]);
  const open = receiptCard
    .locator('a[href^="/workspaces/"]')
    .filter({ hasText: /^Open / })
    .filter({ hasNotText: /report|assessment|Home/ })
    .first();
  await expect(open).toBeVisible();
  const href = await open.getAttribute('href');
  expect(href).toMatch(/^\/workspaces\/[a-z0-9-]+$/);
  // The completed drawer has no task upsell. Optional work is started only
  // through the unchanged workspace banner and its explicit scope/cost review.
  await Promise.all([page.waitForURL(url => url.pathname === href), open.click()]);
  await page.waitForFunction(
    () =>
      (window as any).workspaceDetail?.initialListsLoaded &&
      (window as any).PersonalAssistantPanel?.open,
    undefined,
    { timeout: 15_000 }
  );
  const panel = page.locator('#personalAssistantPanel');
  if (await panel.isVisible()) await page.locator('#personalAssistantClose').click();
  await expect(panel).toBeHidden();
  const banner = page.locator('#workspaceFirstTaskBanner');
  await expect(banner).toContainText('Configuration only; connection not verified');
  await expect(banner).toContainText('Each explicit attempt uses model tokens');
  await expect(banner.getByRole('button', { name: 'Start summary', exact: true })).toBeVisible();
  expect(startCalls).toEqual([]);
  await banner.getByRole('button', { name: 'Start summary', exact: true }).click();
  await expect(banner.getByRole('link', { name: 'Open summary', exact: true })).toBeVisible({
    timeout: reportTimeout
  });
  await page.goto('/?panel=today');
  await page.waitForFunction(
    () =>
      Boolean((window as any).PersonalAssistantConversation) &&
      !(window as any).PersonalAssistantConversation.isLoading()
  );
  await page.evaluate(
    id => (window as any).PersonalAssistantConversation.resume(id),
    conversationId
  );
  if (!(await page.locator('#personalAssistantPanel').isVisible()))
    await page.locator('#personalAssistantLauncher').click();
  await openCard(page);
  await expect(card.getByRole('link', { name: 'Open summary', exact: true })).toBeVisible({
    timeout: reportTimeout
  });
  await expect(page.locator('#personalAssistantActiveFolder')).toBeHidden();
  await page.setViewportSize({ width: 1440, height: 1100 });
  const details = card.locator('#personalAssistantFolderDetails');
  if ((await details.getAttribute('open')) !== null)
    await details.locator('summary').first().click();
  await card.locator('#personalAssistantFolderOfferHeadline').scrollIntoViewIfNeeded();
  await expect(card.locator('#personalAssistantFolderTaskDetails')).not.toHaveAttribute('open', '');
  await expect(card.locator('#personalAssistantFolderTaskLabel')).toBeHidden();
  await page.screenshot({ path: testInfo.outputPath('live-ready-report.png') });
  const evidence = process.env.ORI_ONE_CARD_EVIDENCE_DIR;
  if (evidence) {
    mkdirSync(evidence, { recursive: true, mode: 0o750 });
    await page.screenshot({ path: join(evidence, '05-real-compact-summary.png') });
  }
  await page.getByRole('button', { name: 'Close setup review', exact: true }).click();
  const asks: { url: string; body: any }[] = [];
  page.on('request', req => {
    if (
      req.method() === 'POST' &&
      /\/home-assistant\/(ask|route)$/.test(new URL(req.url()).pathname)
    )
      asks.push({ url: req.url(), body: req.postDataJSON() });
  });
  await composer.fill('  \n\t ');
  await page.locator('#personalAssistantSend').click();
  expect(asks).toEqual([]);
  await composer.fill('Discuss the next step without starting work');
  await page.locator('#personalAssistantSend').click();
  await expect(page.locator('#homeAssistantConversation')).toContainText(
    'No additional task was started.'
  );
  expect(asks.filter(row => row.url.endsWith('/ask'))).toHaveLength(1);
  expect(asks.every(row => row.body.folder_context === undefined)).toBe(true);
  await expect(page.locator('#personalAssistantActiveFolder')).toBeHidden();
  if (evidence) await page.screenshot({ path: join(evidence, '05-real-typed-followup.png') });

  // Closing/reopening preserves the result. The shared read-only tail checks
  // workspace/report navigation without repeating expensive live inference.
  const before = startCalls.length;
  const ids = await workspaceIDs(request);
  await page.locator('#personalAssistantClose').click();
  await page.locator('#personalAssistantLauncher').click();
  expect(await workspaceIDs(request)).toEqual(ids);
  expect(sourceSnapshot(source)).toEqual(original);
  await openCard(page);
  const savedOfferID = await page.evaluate(
    () => (window as any).PersonalAssistantFolder.current()?.id
  );
  const reportHref = await card
    .getByRole('link', { name: 'Open summary', exact: true })
    .getAttribute('href');
  expect(reportHref).toBe(
    await verifySavedFirstLook(
      page,
      request,
      sandbox,
      savedOfferID,
      href!,
      undefined,
      conversationId
    )
  );
  expect(startCalls.length).toBe(before);
  if (evidence) {
    if (await page.locator('#personalAssistantPanel').isVisible())
      await page.locator('#personalAssistantClose').click();
    await page.locator('#workspace-task-output').scrollIntoViewIfNeeded();
    await page.screenshot({ path: join(evidence, '05-real-report-reloaded.png') });
  }
});

test('keyboard: the manager Opens a later song with the same shared assistant', async ({
  page,
  request
}) => {
  test.skip(!providerPhase, 'fresh provider phase only');
  const source = seedProject('Documents', 'Fourth');
  const original = sourceSnapshot(source);
  const reviewed = await scan(page, request, 'documents');
  expect(reviewed.plan.hire).toMatchObject({ mode: 'bind', agent_name: 'REAPER Assistant' });
  const homes = (await ok(await request.get('/api/workspaces'))).folders.filter(
    (item: any) => item.kind === 'group'
  );
  expect(homes).toHaveLength(1);
  const opened = await openRetainedSongFromHome(page, homes[0], 'Fourth', undefined, true);
  await expect(page.locator('#personalAssistantHomeOffer [aria-live="polite"]')).toHaveCount(1);
  await expect(opened).toContainText('REAPER Assistant');
  const agents = await ok(await request.get('/api/agents/dashboard/list'));
  const names = (Array.isArray(agents) ? agents : agents.agents || []).map(
    (agent: { name: string }) => agent.name
  );
  expect(names.filter((name: string) => name.startsWith('REAPER Assistant'))).toEqual([
    'REAPER Assistant'
  ]);
  const open = opened.getByRole('link', { name: 'Open Fourth', exact: true });
  await open.focus();
  await expect(open).toBeFocused();
  const canonical = (await ok(await request.get(`${folderApi}?offer_id=${reviewed.id}`)))
    .folder_digest.offer;
  expect(canonical.status).toBe('resolved');
  expect(
    canonical.outcome.receipt.some(
      (row: any) => row.kind === 'agent' && row.assigned && /REAPER Assistant/.test(row.name)
    )
  ).toBe(true);
  expect(sourceSnapshot(source)).toEqual(original);
});
