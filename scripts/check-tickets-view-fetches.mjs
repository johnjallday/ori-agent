/*
 * Counts ticket-list fetches per user action, so the Tickets view mode cannot
 * quietly become chatty.
 *
 *   node scripts/check-tickets-view-fetches.mjs <studioId> [baseUrl]
 *
 * The view is shown and hidden by render(), and render() runs on every
 * workspace refresh — so an unguarded "load on render" turns one board move
 * into a fetch storm and throws away the user's scroll and filters each time.
 * This measures the actual counts rather than trusting the guard.
 */
import { chromium } from 'playwright';

const studioId = process.argv[2];
if (!studioId) {
  console.error('usage: node scripts/check-tickets-view-fetches.mjs <studioId> [baseUrl]');
  process.exit(1);
}
const base = process.argv[3] || 'http://localhost:8933';

const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1500, height: 1040 } });
const problems = [];
page.on('pageerror', e => problems.push('page error: ' + e.message));

let seen = [];
page.on('request', req => {
  const url = new URL(req.url());
  if (req.method() === 'GET' && /\/tickets$/.test(url.pathname)) {
    seen.push(url.pathname + url.search);
  }
});
const measure = async (label, fn) => {
  seen = [];
  await fn();
  await page.waitForTimeout(1600);
  console.log(`  ${label}: ${seen.length} ticket-list fetches`);
  if (process.env.TRACE) seen.forEach(u => console.log('      ' + u));
  return seen.length;
};

await page.goto(`${base}/workspaces/${studioId}`, { waitUntil: 'domcontentloaded' });
await page.waitForTimeout(3200);
await page.evaluate(() => {
  document.querySelectorAll('.modal.show').forEach(m => {
    m.classList.remove('show');
    m.style.display = 'none';
  });
  document.querySelectorAll('.modal-backdrop').forEach(el => el.remove());
  document.body.classList.remove('modal-open');
});

const counts = {};
counts.enter = await measure('entering Tickets', async () => {
  await page.click('[data-cmd-view-mode="tickets"]');
  await page.waitForSelector('#hubTicketsList .ticket-card', { timeout: 8000 });
});
counts.idle = await measure('sitting in Tickets while the page refreshes', () =>
  page.evaluate(() => window.workspaceCommand?.render())
);
counts.toBoard = await measure('switching List → Board', () => page.click('#hubTicketsViewBoard'));
counts.leave = await measure('leaving to Details', () =>
  page.click('[data-cmd-view-mode="details"]')
);
counts.reenter = await measure('re-entering Tickets', () =>
  page.click('[data-cmd-view-mode="tickets"]')
);

const verdicts = {
  'entering fetches exactly once': counts.enter === 1,
  'an unrelated re-render fetches nothing': counts.idle === 0,
  'the List/Board toggle fetches nothing': counts.toBoard === 0,
  // Leaving costs one request by design, and it is not the list: returning to
  // Details refreshes the header's ticket-count tile, which is suppressed
  // while the view itself is showing the count.
  'leaving refreshes the count tile once': counts.leave === 1,
  're-entering refetches (never shows stale data)': counts.reenter === 1
};
console.log('\n' + JSON.stringify(verdicts, null, 2));
console.log('all checks pass:', Object.values(verdicts).every(Boolean) ? 'yes' : 'NO');
console.log('problems:', problems.length ? problems : 'none');
await browser.close();
