import { test, expect, type Page } from '@playwright/test';
import { mkdirSync } from 'node:fs';
import { join } from 'node:path';
import { verifySavedFirstLook } from './helpers/folder-saved-report';

// Recheck only the read-only tail of a previously executed live journey. This
// supplements, never replaces, fresh review/staffing/model-execution evidence.
const sandbox = process.env.ORI_ONE_CARD_SANDBOX || '';
const offerID = process.env.ORI_ONE_CARD_REPORT_OFFER_ID || '';
const fixture = process.env.ORI_ONE_CARD_REPORT_FIXTURE || 'third';
const evidence = process.env.ORI_ONE_CARD_REPORT_EVIDENCE || '';
if (!['third', 'desktop'].includes(fixture)) throw new Error('Unknown saved-report source fixture');
const sourceFixture =
  fixture === 'desktop'
    ? { subject: 'Desktop', folder: 'Desktop', file: 'Session.rpp' }
    : undefined;
async function captureReport(page: Page, name: string) {
  if (!evidence) return;
  mkdirSync(evidence, { recursive: true, mode: 0o750 });
  if (await page.locator('#personalAssistantPanel').isVisible())
    await page.locator('#personalAssistantClose').click();
  const output = page.locator('#workspace-task-output');
  await expect(output).toBeVisible();
  // A full document capture includes the stored body below task metadata;
  // centering a clipped element can put it behind the existing sticky hero.
  // This is not an initial-viewport claim.
  await page.screenshot({ path: join(evidence, name), fullPage: true });
}
test.skip(!sandbox || !offerID, 'requires an explicit saved sandbox and report offer');
test('saved first report stays readable without re-announcement or another execution', async ({
  page,
  request
}) => {
  await verifySavedFirstLook(page, request, sandbox, offerID, undefined, sourceFixture);
  await captureReport(page, 'saved-report.png');
});

test('saved report navigation works when the assistant was already open', async ({
  page,
  request
}) => {
  test.setTimeout(60_000);
  await page.goto('/');
  await page.waitForFunction(() => Boolean((window as any).PersonalAssistantPanel?.open));
  await page.evaluate(() =>
    (window as any).PersonalAssistantPanel.open(
      document.getElementById('personalAssistantLauncher')
    )
  );
  await expect(page.locator('#personalAssistantPanel')).toBeVisible();
  await verifySavedFirstLook(page, request, sandbox, offerID, undefined, sourceFixture);
  await captureReport(page, 'saved-report-returning-drawer.png');
});
