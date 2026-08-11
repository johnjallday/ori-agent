/*
 * Drives the Ticket-detail note picker and linked-note list
 * (tasks/prd-workspace-ticket-management.md FR-17, FR-18, FR-77).
 *
 *   node scripts/demo-tickets-notes-ui.mjs <studioId> [baseUrl] [outDir]
 */
import { chromium } from 'playwright';
import { mkdirSync, writeFileSync } from 'node:fs';
import { join, resolve } from 'node:path';

const studioId = process.argv[2];
if (!studioId) {
  console.error('usage: node scripts/demo-tickets-notes-ui.mjs <studioId> [baseUrl] [outDir]');
  process.exit(1);
}
const base = process.argv[3] || 'http://localhost:8931';
const outDir = resolve(process.argv[4] || 'tmp-ticket-notes-demo');
mkdirSync(outDir, { recursive: true });

const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1440, height: 1100 } });
const problems = [];
page.on('pageerror', e => problems.push('page error: ' + e.message));
page.on('response', r => {
  if (r.status() >= 400 && (r.url().includes('/tickets') || r.url().includes('/notes'))) {
    problems.push(`HTTP ${r.status()} ${r.request().method()} ${r.url()}`);
  }
});

const cdp = await page.context().newCDPSession(page);
const clearOverlays = () =>
  page.evaluate(() => {
    document.querySelectorAll('.modal.show').forEach(m => {
      m.classList.remove('show');
      m.style.display = 'none';
    });
    document.querySelectorAll('.modal-backdrop').forEach(el => el.remove());
    document.body.classList.remove('modal-open');
  });

async function shot(name) {
  await clearOverlays();
  await page.addStyleTag({
    content:
      '*,*::before,*::after{animation-duration:0s!important;transition-duration:0s!important}'
  });
  await page.waitForTimeout(150);
  const { data } = await cdp.send('Page.captureScreenshot', { format: 'png', fromSurface: true });
  const path = join(outDir, name + '.png');
  writeFileSync(path, Buffer.from(data, 'base64'));
  return path;
}

const settle = () => page.waitForTimeout(800);

const linkedNotes = () =>
  page.evaluate(() =>
    [...document.querySelectorAll('#hubTicketDetailBody .ticket-detail-note')].map(item => ({
      title: item.querySelector('.ticket-detail-note-link')?.textContent?.trim(),
      unlinkLabel: item.querySelector('.ticket-detail-note-unlink')?.getAttribute('aria-label')
    }))
  );

await page.goto(`${base}/workspaces/${studioId}`, { waitUntil: 'domcontentloaded' });
await page.waitForTimeout(2500);
await clearOverlays();
await page.click('[data-cmd-section="tickets"]');
await page.waitForSelector('#hubTicketsList', { state: 'visible', timeout: 8000 });
await settle();

console.log('→ open the first ticket');
await page.click('#hubTicketsList .ticket-card:first-child .ticket-card-open');
await page.waitForSelector('#hubTicketDetail:not([hidden])');
await page.waitForFunction(
  () => !/Loading/.test(document.getElementById('hubTicketDetailTitle')?.textContent || '')
);
await settle();

console.log('  linked notes before:', JSON.stringify(await linkedNotes()));
const pickerOptions = await page.evaluate(() =>
  [...document.querySelectorAll('.ticket-note-picker-select option')].map(o => o.textContent.trim())
);
console.log('  picker offers:', JSON.stringify(pickerOptions));
console.log('  screenshot:', await shot('01-detail-with-picker'));

const linkable = await page.evaluate(
  () =>
    [...document.querySelectorAll('.ticket-note-picker-select option')]
      .map(o => o.value)
      .filter(Boolean)[0] || ''
);

if (linkable) {
  console.log('→ link a note through the picker');
  await page.selectOption('.ticket-note-picker-select', linkable);
  await settle();
  console.log('  linked notes after:', JSON.stringify(await linkedNotes(), null, 2));
  console.log(
    '  live region:',
    await page.evaluate(() => document.getElementById('hubTicketsLiveRegion')?.textContent?.trim())
  );
  console.log('  screenshot:', await shot('02-note-linked'));

  console.log('→ the already-linked note is no longer offered');
  const after = await page.evaluate(() =>
    [...document.querySelectorAll('.ticket-note-picker-select option')].map(o => o.value)
  );
  console.log('  still offered:', after.includes(linkable) ? 'YES (bug)' : 'no');

  console.log('→ unlink it again; copy must say the note is kept');
  console.log(
    '  hint text:',
    await page.evaluate(
      () => document.querySelector('.ticket-detail-note-hint')?.textContent?.trim() || ''
    )
  );
  await page.click('.ticket-detail-note-unlink');
  await settle();
  console.log('  linked notes after unlink:', JSON.stringify(await linkedNotes()));
  console.log('  screenshot:', await shot('03-note-unlinked'));
} else {
  console.log('  (no linkable notes in this workspace; seed some first)');
}

console.log('\nproblems:', problems.length ? problems : 'none');
await browser.close();
