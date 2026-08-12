/*
 * Verifies the Group 6 consolidation items
 * (tasks/prd-workspace-ticket-management.md FR-81, FR-82, FR-84).
 *
 *   node scripts/check-ticket-consolidation.mjs <studioId> [baseUrl]
 *
 * Two things a screenshot cannot show:
 *   - "Open Backlog" no longer opens a SECOND editable backlog surface; it
 *     lands on the canonical Tickets destination.
 *   - The ⌘K palette finds tickets, labels them distinctly from notes, and
 *     deep-links on the stable id.
 */
import { chromium } from 'playwright';

const studioId = process.argv[2];
if (!studioId) {
  console.error('usage: node scripts/check-ticket-consolidation.mjs <studioId> [baseUrl]');
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

await page.goto(`${base}/workspaces/${studioId}`, { waitUntil: 'domcontentloaded' });
await page.waitForTimeout(3500);
await clearOverlays();

console.log('→ 6.7: "Open Backlog" must NOT open a second editable backlog surface');
const opener = page.locator('[data-cmd-open-backlog-drawer]').first();
console.log('  opener present:', (await opener.count()) > 0);
if ((await opener.count()) > 0) {
  await opener.click();
  await page.waitForTimeout(1600);
  console.log(
    '  ',
    JSON.stringify(
      await page.evaluate(() => ({
        legacyDrawerOpen: Boolean(window.workspaceCommand?.backlogDrawerOpen),
        ticketsModalOpen: Boolean(document.querySelector('.ws-cmd-modal:not([hidden])')),
        statSection: window.workspaceCommand?.statModalSection,
        activeChips: [...document.querySelectorAll('#hubTicketsFilters .ticket-filter-chip')]
          .filter(c => c.getAttribute('aria-pressed') === 'true')
          .map(c => c.textContent.trim()),
        ticketRows: document.querySelectorAll('#hubTicketsList .ticket-card').length
      }))
    )
  );
}

console.log('\n→ 6.7: "Add to Backlog" lands in the create form, already typing');
await page.goto(`${base}/workspaces/${studioId}`, { waitUntil: 'domcontentloaded' });
await page.waitForTimeout(3500);
await clearOverlays();
const addBtn = page.locator('[data-cmd-backlog-add]').first();
if ((await addBtn.count()) > 0) {
  await addBtn.click();
  await page.waitForTimeout(1600);
  console.log(
    '  ',
    JSON.stringify(
      await page.evaluate(() => ({
        focused: document.activeElement?.id || '',
        backlogPreselected:
          document.querySelector('input[name="hubTicketCreateState"][value="backlog"]')?.checked ||
          false
      }))
    )
  );
}

// 6.9 (search results labelling Tickets) is NOT checked here: the ⌘K palette
// is rendered only by base.tmpl, which only Home uses, and Home is not under
// /workspaces/{id} — so workspace-scoped ticket search has no surface to run
// on yet. See the checklist for why that item is blocked rather than done.
console.log('\n→ 6.9: skipped — the search palette does not render on this page');
console.log(
  '  palette present here:',
  await page.evaluate(() => Boolean(document.getElementById('searchPalette')))
);

console.log('\nproblems:', problems.length ? problems : 'none');
await browser.close();
