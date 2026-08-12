/*
 * Verifies that Tickets is a real third VIEW MODE beside Details and Map
 * (tasks/prd-workspace-ticket-management.md FR-63, FR-64), not a modal.
 *
 *   node scripts/check-tickets-view-mode.mjs <studioId> [baseUrl]
 *
 * The thing worth proving is not that a click ran a handler — it is that the
 * surface the user sees actually changed, in both directions. A view mode that
 * shows Tickets but leaves the Details body underneath, or that cannot be
 * switched back out of, would pass a handler-level check and fail a user.
 */
import { chromium } from 'playwright';

const studioId = process.argv[2];
if (!studioId) {
  console.error('usage: node scripts/check-tickets-view-mode.mjs <studioId> [baseUrl]');
  process.exit(1);
}
const base = process.argv[3] || 'http://localhost:8933';
const shot = process.argv[4] || '';

const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1500, height: 1040 } });
const problems = [];
page.on('pageerror', e => problems.push('page error: ' + e.message));

const state = () =>
  page.evaluate(() => {
    const surface = document.getElementById('workspace-detail-tickets-surface');
    const tabs = [...document.querySelectorAll('[data-cmd-view-mode]')];
    const rect = surface?.getBoundingClientRect();
    return {
      tabs: tabs.map(t => t.getAttribute('data-cmd-view-mode')),
      activeTab: tabs
        .find(t => t.classList.contains('is-active'))
        ?.getAttribute('data-cmd-view-mode'),
      viewMode: window.workspaceCommand?.viewMode,
      ticketsVisible: Boolean(surface && !surface.hidden && rect && rect.width > 0),
      ticketsWidth: Math.round(rect?.width || 0),
      // Tickets lives OUTSIDE the command container, which is what makes it
      // immune to the innerHTML rebuild that forces the Board to be relocated.
      outsideCommandContainer: Boolean(
        surface && !document.getElementById('workspaceCommandView')?.contains(surface)
      ),
      detailsBodyPresent: Boolean(document.querySelector('#workspaceCommandView .ws-cmd-layout')),
      mapPresent: Boolean(document.querySelector('#workspaceCommandView .ws-cmd-map')),
      ticketRows: document.querySelectorAll('#hubTicketsList .ticket-card').length,
      countLabel: document.getElementById('hubTicketsCount')?.textContent?.trim() || ''
    };
  });

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

console.log('→ on arrival (Details):', JSON.stringify(await state(), null, 2));

await page.click('[data-cmd-view-mode="tickets"]');
await page.waitForSelector('#hubTicketsList .ticket-card', { timeout: 8000 });
await page.waitForTimeout(900);
const onTickets = await state();
console.log('\n→ after switching to Tickets:', JSON.stringify(onTickets, null, 2));

await page.click('[data-cmd-view-mode="details"]');
await page.waitForTimeout(900);
const backOnDetails = await state();
console.log('\n→ after switching back to Details:', JSON.stringify(backOnDetails, null, 2));

// Re-entry has to reload, not show a stale render.
await page.click('[data-cmd-view-mode="tickets"]');
await page.waitForTimeout(1200);
const reentry = await state();

const verdicts = {
  'three view modes offered': onTickets.tabs.join(',') === 'details,map,tickets',
  'Tickets shows the ticket list full width': onTickets.ticketsVisible && onTickets.ticketRows > 0,
  'Tickets is not nested in the rebuilt container': onTickets.outsideCommandContainer,
  'Details body is not rendered underneath Tickets': !onTickets.detailsBodyPresent,
  'switching back restores Details':
    backOnDetails.detailsBodyPresent && !backOnDetails.ticketsVisible,
  're-entering Tickets still renders rows': reentry.ticketsVisible && reentry.ticketRows > 0
};
console.log('\n' + JSON.stringify(verdicts, null, 2));
console.log('all checks pass:', Object.values(verdicts).every(Boolean) ? 'yes' : 'NO');

if (shot) {
  await page.addStyleTag({
    content: '*,*::before,*::after{animation:none!important;transition:none!important}'
  });
  await page.waitForTimeout(400);
  const cdp = await page.context().newCDPSession(page);
  const { data } = await cdp.send('Page.captureScreenshot', { format: 'png', fromSurface: true });
  const { writeFileSync } = await import('node:fs');
  writeFileSync(shot, Buffer.from(data, 'base64'));
  console.log('screenshot:', shot);
}

console.log('problems:', problems.length ? problems : 'none');
await browser.close();
