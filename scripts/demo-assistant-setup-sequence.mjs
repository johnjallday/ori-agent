/*
 * Drives the Assistant setup sequence (#529) against a running, ISOLATED demo
 * server (`wt demo`, or `./scripts/demo-server.sh <port>`) and saves screenshots.
 * Real UI and real endpoints; the only shortcut is skipping onboarding and
 * hiring the assistant through the API. Needs a FRESH sandbox: a hire cannot be
 * undone.
 *
 *   node scripts/demo-assistant-setup-sequence.mjs [baseUrl] [outDir]
 *
 * Defaults: http://localhost:8931 and $TMPDIR/assistant-setup-sequence.
 *
 * It presses "Set up for me", walks the read-only walkthrough (Create
 * Workspace, Create Agent, Receipt summary) with a screenshot per panel, then
 * prints the workspace and File Curator sequence beside the canonical resource
 * IDs and receipt times from the API, so the two can be compared by eye.
 */
import { chromium } from 'playwright';
import { mkdirSync } from 'node:fs';
import { join, resolve } from 'node:path';

const baseUrl = (process.argv[2] || 'http://localhost:8931').replace(/\/$/, '');
const outDir = resolve(
  process.argv[3] || join(process.env.TMPDIR || '.', 'assistant-setup-sequence')
);
mkdirSync(outDir, { recursive: true });

async function api(context, path, options) {
  const response = await context.request.fetch(`${baseUrl}${path}`, options);
  if (!response.ok()) throw new Error(`${path} -> ${response.status()} ${await response.text()}`);
  return response.json();
}

const browser = await chromium.launch();
const context = await browser.newContext({ viewport: { width: 1280, height: 800 } });
const page = await context.newPage();

try {
  await context.request.post(`${baseUrl}/api/onboarding/skip`);
  const relationship = (await api(context, '/api/personal-assistant')).personal_assistant;
  if (relationship.state !== 'needs_hire') {
    throw new Error(`needs a fresh sandbox (assistant state is ${relationship.state})`);
  }
  await api(context, '/api/personal-assistant/hire', {
    method: 'POST',
    data: {
      request_id: `demo-sequence-${Date.now()}`,
      if_version: relationship.state_version ?? 0,
      display_name: 'Ari',
      appearance: { mode: 'generated', generated: {} },
      mandate: 'Help me plan each day and keep my files organized.',
      focus_areas: ['plan_my_day', 'organize_project_files']
    }
  });

  await page.goto(`${baseUrl}/?quest=tidy-downloads`);
  const card = page.locator('#assistantLedSetup');
  await card.waitFor({ state: 'visible', timeout: 20000 });
  await page.screenshot({ path: join(outDir, '1-review.png') });

  await card.getByRole('button', { name: 'Set up for me' }).click();
  const dialog = page.locator('#assistantLedSetupDialog');
  await dialog.waitFor({ state: 'visible' });
  await page.locator('#assistantLedSetupDialogMode').getByText('Receipt walkthrough').waitFor();

  const panels = ['workspace', 'agents', 'receipt'];
  for (const [index, id] of panels.entries()) {
    await dialog.locator(`[data-panel-id="${id}"]`).waitFor({ state: 'visible', timeout: 10000 });
    await page.screenshot({ path: join(outDir, `${index + 2}-${id}.png`) });
  }
  await page.locator('#assistantLedSetupDialogContinue').click();
  await page.screenshot({ path: join(outDir, '5-continue-lands-on-choose-folder.png') });

  const setup = (await api(context, '/api/personal-assistant/setup/file-janitor')).setup;
  console.log('Sequence shown on the card / API receipts');
  console.table(
    setup.milestones.map(milestone => ({
      step: milestone.id,
      status: milestone.status,
      ownership: milestone.ownership,
      resource_id: milestone.resource_id,
      recorded_at: milestone.recorded_at
    }))
  );
  const shown = await page
    .locator('#assistantLedSetupMilestones li')
    .evaluateAll(items => items.map(item => item.dataset.resourceId));
  const same = JSON.stringify(shown) === JSON.stringify(setup.milestones.map(m => m.resource_id));
  console.log(`card IDs match the API: ${same}`);
  console.log(`screenshots: ${outDir}`);
  if (!same) process.exitCode = 1;
} finally {
  await browser.close();
}
