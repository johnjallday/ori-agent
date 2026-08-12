/*
 * Verifies the count shortcuts into the canonical Tickets destination
 * (tasks/prd-workspace-ticket-management.md FR-65, FR-80, FR-81).
 *
 *   node scripts/check-ticket-count-shortcut.mjs <studioId> [baseUrl]
 *
 * Checks both routes into the filtered destination: the in-page Backlog panel
 * shortcut, and the `?tickets=<state>` deep link.
 */
import { chromium } from 'playwright';

const studioId = process.argv[2];
if (!studioId) {
  console.error('usage: node scripts/check-ticket-count-shortcut.mjs <studioId> [baseUrl]');
  process.exit(1);
}
const base = process.argv[3] || 'http://localhost:8931';

const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } });
const problems = [];
page.on('pageerror', e => problems.push('page error: ' + e.message));

const clearOverlays = () =>
  page.evaluate(() => {
    document.querySelectorAll('.modal.show').forEach(m => {
      m.classList.remove('show');
      m.style.display = 'none';
    });
    document.querySelectorAll('.modal-backdrop').forEach(el => el.remove());
    document.body.classList.remove('modal-open');
  });

const shown = () =>
  page.evaluate(() => ({
    ticketsViewActive: Boolean(
      document.querySelector('#workspace-detail-tickets-surface:not([hidden])')
    ),
    activeChips: [...document.querySelectorAll('#hubTicketsFilters .ticket-filter-chip')]
      .filter(chip => chip.getAttribute('aria-pressed') === 'true')
      .map(chip => chip.textContent.trim()),
    rows: [...document.querySelectorAll('#hubTicketsList .ticket-card')].map(card => ({
      number: card.querySelector('.ticket-card-number')?.textContent?.trim(),
      state: card.dataset.state
    }))
  }));

console.log('→ route 1: the in-page Backlog panel shortcut');
await page.goto(`${base}/workspaces/${studioId}`, { waitUntil: 'domcontentloaded' });
await page.waitForTimeout(3000);
await clearOverlays();

const shortcut = page.locator('[data-cmd-open-tickets="backlog"]').first();
console.log('  shortcut present:', (await shortcut.count()) > 0);
if ((await shortcut.count()) > 0) {
  await shortcut.click();
  await page.waitForTimeout(1500);
  console.log('  ', JSON.stringify(await shown()));
}

console.log('\n→ route 2: the ?tickets=<state> deep link');
await page.goto(`${base}/workspaces/${studioId}?tickets=backlog`, {
  waitUntil: 'domcontentloaded'
});
await page.waitForTimeout(3500);
await clearOverlays();
const viaLink = await shown();
console.log('  ', JSON.stringify(viaLink));
console.log(
  '  filtered to Backlog only:',
  viaLink.activeChips.length === 1 &&
    viaLink.activeChips[0] === 'Backlog' &&
    viaLink.rows.every(row => row.state === 'backlog')
    ? 'yes'
    : 'NO'
);

console.log('\nproblems:', problems.length ? problems : 'none');
await browser.close();
