/*
 * Verifies the canonical Ticket deep-link contract
 * (tasks/prd-workspace-ticket-management.md FR-83, FR-84).
 *
 *   node scripts/check-ticket-deep-link.mjs <workspaceUrlWithTicketParam>
 *
 * A deep link has to actually LAND somewhere: the Tickets surface lives inside
 * a modal that is closed by default, so a link that merely told the module
 * which ticket to show would be a silent no-op.
 */
import { chromium } from 'playwright';

const url = process.argv[2];
if (!url) {
  console.error('usage: node scripts/check-ticket-deep-link.mjs <url>');
  process.exit(1);
}

const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } });
const problems = [];
page.on('pageerror', e => problems.push('page error: ' + e.message));

await page.goto(url, { waitUntil: 'domcontentloaded' });
await page.waitForTimeout(4000);

const landed = await page.evaluate(() => {
  const modal = document.querySelector('.ws-cmd-modal');
  const detail = document.getElementById('hubTicketDetail');
  return {
    ticketsModalOpen: Boolean(modal && !modal.hidden),
    modalTitle: modal?.querySelector('.ws-cmd-modal-title')?.textContent?.trim() || '',
    detailVisible: Boolean(detail && !detail.hidden),
    detailTitle: document.getElementById('hubTicketDetailTitle')?.textContent?.trim() || '',
    detailRows: [...document.querySelectorAll('#hubTicketDetailBody .ticket-detail-row')]
      .map(row => row.innerText.replace(/\s+/g, ' ').trim())
      .slice(0, 4)
  };
});

console.log(JSON.stringify(landed, null, 2));
console.log(
  'deep link landed on the ticket:',
  landed.ticketsModalOpen && landed.detailVisible && !/Loading|Could not/.test(landed.detailTitle)
    ? 'yes'
    : 'NO'
);
console.log('problems:', problems.length ? problems : 'none');
await browser.close();
