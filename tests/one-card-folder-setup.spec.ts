import { expect, test, type APIRequestContext, type Page } from '@playwright/test';
import { mkdirSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';

// One card, one click: a recognized project folder shows its whole plan, Set up
// runs it on the server, and the card ends on a receipt that opens the workspace
// and starts the agent's first task.
//
// Run it against the isolated demo sandbox (the folders are written into the
// server's HOME, which is the sandbox):
//
//   ./scripts/e2e.sh --env ORI_ONE_CARD_SANDBOX=<sandbox dir> tests/one-card-folder-setup.spec.ts
//
// It needs the network the first time (the reviewed REAPER integration and its
// Home provider are downloaded and installed by the run itself). The steps that
// create agents also need a model provider; without one they are skipped and the
// "needs a model" stop is the thing that is checked instead. No REAPER app is
// launched and no live control is granted.
const sandbox = process.env.ORI_ONE_CARD_SANDBOX || '';
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
  return card;
}

async function modelAvailable(request: APIRequestContext) {
  const response = await request.post('/api/settings/system-model', {
    data: { provider: 'codex', model: 'gpt-5.6-luna' }
  });
  return response.ok();
}

let modelReady = false;

test('a hired assistant with a recognized project folder shows the whole plan', async ({
  page,
  request
}) => {
  await request.post('/api/onboarding/skip');
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
  expect(text).toMatch(/first read-only task/);
  expect(text).not.toMatch(/\/Users\/|\/var\/|\/private\//);
  await expect(card.getByRole('button', { name: 'Set up', exact: true })).toBeVisible();
  await expect(card.getByRole('button', { name: 'Adjust…' })).toBeVisible();

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
  await card.getByRole('button', { name: 'Adjust…' }).click();
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
  await expect(resumed.getByRole('button', { name: 'Set up', exact: true })).toBeVisible();
  await expect(resumed.getByRole('button', { name: 'Continue setup' })).toBeVisible();
  await expect(resumed.locator('#personalAssistantFolderPlan li').first()).toBeVisible();
});

test('Set up without a model stops with one plain sentence and a way forward', async ({
  page,
  request
}) => {
  // Make sure no model is configured for this step.
  await request.post('/api/settings/system-model', { data: { provider: '', model: '' } });
  const offer = await scan(request, 'downloads');
  expect(offer.plan?.digest).toBeTruthy();

  const card = await openCard(page);
  await card.getByRole('button', { name: 'Set up', exact: true }).click();
  // Set up opens a pop-up that watches the run: no click per step, and it says
  // the assistant is working.
  const runModal = page.locator('#folderSetupRunModal');
  await expect(runModal).toBeVisible();
  await expect(runModal.locator('#folderSetupRunEyebrow')).toContainText(/working|paused/i);
  await expect(runModal.locator('#folderSetupRunBar')).toHaveAttribute('aria-valuenow', /\d+/);
  const stopped = runModal.locator('#folderSetupRunStatus');
  await expect(stopped).toContainText('needs a model', { timeout: 120_000 });
  await expect(runModal.locator('#folderSetupRunCount')).toContainText(/\d+ of \d+ steps finished/);
  await expect(runModal).not.toContainText(/needs_model|plan_changed|install_failed/);
  for (const name of ['Set up a model', 'Try again', 'Continue setup']) {
    await expect(runModal.getByRole('button', { name })).toBeVisible();
  }
  // Closing it does not stop anything: the card says the same on its own.
  await runModal.getByRole('button', { name: /^Close/ }).click();
  await expect(runModal).toBeHidden();
  const sentence = card.locator('#personalAssistantFolderOfferQuestion');
  await expect(sentence).toContainText('needs a model');
  await expect(sentence).toContainText(/\d+ of \d+ steps finished/);
  await expect(card).not.toContainText(/needs_model|plan_changed|install_failed/);
  for (const name of ['Set up a model', 'Try again', 'Continue setup']) {
    await expect(card.getByRole('button', { name })).toBeVisible();
  }
  // What finished stays finished; nothing was created without a model.
  await expect(
    card.locator('#personalAssistantFolderPlan li[data-state="done"]').first()
  ).toBeVisible();
  await expect(card.locator('#personalAssistantFolderReceipt')).toBeHidden();
  modelReady = await modelAvailable(request);
});

test('Try again after a model is set finishes the run on the same card, then Start first look starts the first task', async ({
  page,
  request
}) => {
  test.skip(!modelReady, 'no model provider is available on this machine');
  const card = await openCard(page);
  await card.getByRole('button', { name: 'Try again' }).click();
  const receipt = card.locator('#personalAssistantFolderReceipt li');
  await expect(receipt.first()).toBeVisible({ timeout: 180_000 });
  const rows = (await receipt.allInnerTexts()).join('\n');
  expect(rows).toMatch(/Workspace/);
  expect(rows).toMatch(/Agent/);
  // The song's agent is the Home's one shared assistant, not "<label> · <song>".
  expect(rows).toMatch(/REAPER Assistant[^\n·]*added/);
  expect(rows).toMatch(/First task/);
  expect(rows).toMatch(/Starts when you press Start first look/);

  const startCalls: string[] = [];
  page.on('response', async response => {
    if (
      response.url().endsWith('/folder-first-task/start') &&
      response.request().method() === 'POST'
    ) {
      startCalls.push(JSON.stringify(await response.json().catch(() => ({}))));
    }
  });
  // The pop-up that watched the run ends on the same receipt and Open link.
  const runModal = page.locator('#folderSetupRunModal');
  await expect(runModal).toBeVisible();
  await expect(runModal.locator('#folderSetupRunEyebrow')).toHaveText('All set');
  await expect(runModal.locator('#folderSetupRunBar')).toHaveAttribute('aria-valuenow', '100');
  // The receipt offers the click that spends tokens, and the workspace beside it.
  // Nothing has started yet: opening a page never starts the look.
  const receiptCard = page.locator('#personalAssistantFolderOffer');
  const start = receiptCard.getByRole('button', { name: 'Start first look' });
  await expect(start).toBeVisible();
  expect(startCalls).toEqual([]);
  const open = receiptCard.getByRole('link', { name: /^Open / });
  await expect(open).toBeVisible();
  const href = await open.getAttribute('href');
  expect(href).toMatch(/^\/workspaces\/[a-z0-9-]+$/);

  await start.click();
  await expect
    .poll(() => startCalls.some(call => call.includes('"started":true')), { timeout: 60_000 })
    .toBe(true);
  // The card follows the run on Home, with no page change.
  await expect(receiptCard.locator('#personalAssistantFolderReceipt')).toContainText(
    /Running…|Done/,
    {
      timeout: 60_000
    }
  );

  // Opening the workspace never starts it a second time, and shows the banner.
  const before = startCalls.length;
  await page.goto(href!);
  await page.waitForLoadState('load');
  await expect(page.locator('.modal.show')).toHaveCount(0);
  const banner = page.locator('#workspaceFirstTaskBanner');
  await expect(banner).toBeVisible();
  await expect(banner).toContainText(/first (task|look)/);
  expect(startCalls.length).toBe(before);
});

test('keyboard only: Set up runs from the focused button and the card announces its progress', async ({
  page,
  request
}) => {
  test.skip(!modelReady, 'no model provider is available on this machine');
  seedProject('Documents', 'Fourth');
  await scan(request, 'documents');
  const card = await openCard(page);
  // The status line is a polite live region, so progress is spoken, not just shown.
  await expect(card.locator('#personalAssistantFolderOfferNote')).toHaveAttribute(
    'aria-live',
    'polite'
  );
  const setUp = card.getByRole('button', { name: 'Set up', exact: true });
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
  // The pop-up's Open link is reachable and activatable without a mouse.
  const open = page.locator('#folderSetupRunModal').getByRole('link', { name: /^Open / });
  await open.focus();
  await expect(open).toBeFocused();
});
