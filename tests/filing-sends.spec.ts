import { test, expect, type Page, type Locator } from '@playwright/test';
import { filingWorkspace } from './helpers/filing-workspace';
import {
  existsSync,
  mkdtempSync,
  mkdirSync,
  realpathSync,
  utimesSync,
  writeFileSync
} from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

// Filing sends files to another workspace (tasks/prd-filing-sends-to-workspace.md).
// Real APIs and saved state, credential-free and model-free: two workspaces with
// Filing on synthetic OS-temp folders, never the user's own. The journey drives the
// connect card, the Leaving card, one press, the receipt, the receiving side's
// strip and Undo this send, then Undo all; the access checks cover the new controls
// in both themes, at 390 px and with reduced motion (FR 52, 53).
test.describe.configure({ mode: 'serial', timeout: 180_000 });

const evidence = 'tasks/evidence-filing-sends-to-workspace';
const old = new Date(Date.now() - 2 * 24 * 60 * 60 * 1000);

function syntheticFolder(name: string, files: string[]) {
  const temp = realpathSync(tmpdir());
  const parent = realpathSync(mkdtempSync(join(temp, 'ori-sends-')));
  if (!parent.startsWith(temp) || parent === temp) throw new Error('Non-temporary fixture');
  const root = join(parent, name);
  mkdirSync(root, { mode: 0o700 });
  for (const file of files) {
    writeFileSync(join(root, file), 'synthetic ' + file, { mode: 0o600 });
    utimesSync(join(root, file), old, old);
  }
  return root;
}

async function openFiling(page: Page, slug: string, tab: 'review' | 'settings') {
  await page.route('**/api/onboarding/status', route =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ needs_onboarding: false, completed: true })
    })
  );
  const url = `/workspaces/${encodeURIComponent(slug)}?view=details&panel=file-janitor&tab=${tab}`;
  await page.goto(url);
  const console = page.locator('#fileJanitorConsole');
  await expect(console).toBeVisible({ timeout: 20_000 });
  const nudge = page.locator('#workspace-detail-task-confirm-modal');
  if (await nudge.isVisible()) {
    await console.getByRole('button', { name: 'Close Filing', exact: true }).click();
    await nudge.getByRole('button', { name: 'Not now', exact: true }).click();
    await page.goto(url);
    await expect(console).toBeVisible();
  }
  return console;
}

// At least 44 px tall (FR 52).
async function tallEnough(control: Locator) {
  // Loading the plan replaces the first picture; measure the settled control.
  await expect
    .poll(async () => (await control.boundingBox())?.height ?? 0)
    .toBeGreaterThanOrEqual(43.5);
}

const BANNED =
  /\b(?:linked|batch(?:es)?|candidates?|proposals?|exact(?:ly)?|explicitly|drafts?)\b/i;

test.describe('Filing sends files to another workspace', () => {
  let downloads: Awaited<ReturnType<typeof filingWorkspace>>;
  let documents: Awaited<ReturnType<typeof filingWorkspace>>;

  test.beforeEach(async ({ request }) => {
    downloads = await filingWorkspace(
      request,
      'Downloads',
      syntheticFolder('Downloads', ['lease.pdf', 'invoice.pdf', 'photo.png'])
    );
    documents = await filingWorkspace(request, 'Documents', syntheticFolder('Documents', []));
  });

  test('connect, file, receipt, the receiving side, Undo this send, Undo all', async ({
    page,
    request
  }) => {
    // Connect from the sender's Settings (FR 4 to 6).
    let console = await openFiling(page, downloads.slug, 'settings');
    await console.getByRole('button', { name: 'Connect a workspace' }).click();
    const card = console.locator('#fileJanitorConnectCard');
    await expect(card).toContainText('What it never allows');
    await expect(card).not.toContainText(BANNED);
    const receiverChoice = card.locator('#fileJanitorConnectReceiver');
    if (await receiverChoice.count()) await receiverChoice.selectOption(documents.id);
    await card.getByRole('checkbox', { name: 'Documents' }).check();
    await card.getByRole('button', { name: /^Allow sending to / }).click();
    await expect(console.locator('[data-section="sends-to"]')).toContainText('On ·');
    await page.screenshot({ path: `${evidence}/6.2-01-connected.png`, fullPage: true });

    // Scan, then the picture: Leaving above the folder, ticked on open (FR 20 to 22).
    const scan = await request.post(`${downloads.base}/scan`);
    expect(scan.ok(), await scan.text()).toBeTruthy();
    console = await openFiling(page, downloads.slug, 'review');
    await expect(console.locator('#fileJanitorLeavingTitle')).toHaveText(/^Leaving /);
    const tick = console.locator('[id^="fileJanitorLeaving-connection-"]');
    await expect(tick).toBeChecked();
    await expect(console.locator('.fj-decision-counts')).toContainText('2 to ');
    await page.screenshot({ path: `${evidence}/6.2-02-leaving.png`, fullPage: true });

    // One press files both (FR 29 to 31).
    const press = console.locator('#fileJanitorApprove');
    await expect(press).toHaveText('File 3 files');
    await press.click();
    const receipt = console.locator('#fileJanitorReceipt');
    // A fresh workspace's first Filed move starts its filesystem server, which
    // can take tens of seconds; the receipt is what is checked, not the speed.
    await expect(receipt).toContainText('2 went to ', { timeout: 90_000 });
    await expect(receipt.getByRole('link', { name: /^Open / })).toBeVisible();
    await page.screenshot({ path: `${evidence}/6.2-03-receipt.png`, fullPage: true });
    expect(existsSync(join(documents.root, 'lease.pdf'))).toBe(true);
    expect(existsSync(join(documents.root, 'invoice.pdf'))).toBe(true);
    expect(existsSync(join(downloads.root, 'Filed', 'Images', 'photo.png'))).toBe(true);

    // The receiving side shows what arrived, and Undo this send returns only those.
    console = await openFiling(page, documents.slug, 'review');
    const strip = console.locator('#fileJanitorArrivalsHost');
    await expect(strip).toContainText('2 files arrived from ');
    await expect(strip).toContainText('from ');
    await page.screenshot({ path: `${evidence}/6.2-04-arrived.png`, fullPage: true });
    await strip.getByRole('button', { name: 'Undo this send' }).click();
    await expect(strip).toContainText('2 files are back in ');
    expect(existsSync(join(downloads.root, 'lease.pdf'))).toBe(true);
    expect(existsSync(join(documents.root, 'lease.pdf'))).toBe(false);
    expect(existsSync(join(downloads.root, 'Filed', 'Images', 'photo.png'))).toBe(true);

    // Undo all on the sender brings back the Filed file too (FR 33).
    const filings = await request.get(`${downloads.base}/filings?limit=1`);
    const filingID = (await filings.json()).filings[0].filing_id;
    const undo = await request.post(`${downloads.base}/filings/${filingID}/undo`);
    expect(undo.ok(), await undo.text()).toBeTruthy();
    expect(existsSync(join(downloads.root, 'photo.png'))).toBe(true);
  });

  test('the new controls are labelled, tall enough and keyboard reachable, in both themes, at 390 px, with reduced motion', async ({
    page,
    request
  }) => {
    const connect = await request.post(`${downloads.base}/connections`, {
      data: { receiver_workspace_id: documents.id, kinds: ['documents'] }
    });
    expect(connect.ok(), await connect.text()).toBeTruthy();
    expect((await request.post(`${downloads.base}/scan`)).ok()).toBeTruthy();

    for (const theme of ['light', 'dark'] as const) {
      await page.emulateMedia({ colorScheme: theme, reducedMotion: 'reduce' });
      await page.addInitScript(value => window.localStorage.setItem('ori-theme', value), theme);
      await page.setViewportSize({ width: 390, height: 844 });

      const console = await openFiling(page, downloads.slug, 'review');
      const tick = console.locator('[id^="fileJanitorLeaving-connection-"]');
      await expect(tick).toHaveAccessibleName(/^Send \d+ files? to the .+ workspace$/);
      await tallEnough(console.locator('.fj-leaving-card .fj-folder-tick').first());
      const keep = console.getByRole('button', { name: /^Keep these in .+ instead$/ });
      await tallEnough(keep);
      // Reachable by Tab: focus moves to it without a pointer.
      await keep.focus();
      await expect(keep).toBeFocused();
      const width = await page.evaluate(() => document.documentElement.scrollWidth);
      expect(width, 'nothing overflows at 390 px').toBeLessThanOrEqual(392);
      await page.screenshot({
        path: `${evidence}/6.2-05-leaving-${theme}-390.png`,
        fullPage: true
      });

      const settings = await openFiling(page, downloads.slug, 'settings');
      const off = settings.getByRole('button', { name: /^Turn off sending to / });
      await expect(off).toBeVisible();
      await tallEnough(off);
      await tallEnough(settings.locator('#fileJanitorConnectStart'));
    }
  });
});
