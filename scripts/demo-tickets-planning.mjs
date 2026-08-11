/*
 * Drives Group 2 of tasks/prd-workspace-ticket-management.md: Backlog-to-Ready
 * planning, filters, search, sort, the descendant roll-up, hierarchy in
 * detail, and the 14-day recent/archive boundary.
 *
 *   node scripts/demo-tickets-planning.mjs <studioId> [baseUrl] [outDir]
 *
 * Screenshots go through CDP with animations frozen: this app loads a blocked
 * Google Fonts stylesheet, so document.fonts.ready never settles and
 * Playwright's own screenshot path waits on it forever.
 */
import { chromium } from 'playwright';
import { mkdirSync, writeFileSync } from 'node:fs';
import { join, resolve } from 'node:path';

const studioId = process.argv[2];
if (!studioId) {
  console.error('usage: node scripts/demo-tickets-planning.mjs <studioId> [baseUrl] [outDir]');
  process.exit(1);
}
const base = process.argv[3] || 'http://localhost:8931';
const outDir = resolve(process.argv[4] || 'tmp-ticket-planning-demo');
mkdirSync(outDir, { recursive: true });

const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1440, height: 1100 } });
const problems = [];
page.on('pageerror', e => problems.push('page error: ' + e.message));
page.on('response', r => {
  if (r.status() >= 400 && r.url().includes('/tickets')) {
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
    document.body.style.removeProperty('overflow');
  });

async function shot(name) {
  await clearOverlays();
  await page.addStyleTag({
    content: '*,*::before,*::after{animation-duration:0s!important;transition-duration:0s!important}'
  });
  await page.waitForTimeout(150);
  const { data } = await cdp.send('Page.captureScreenshot', { format: 'png', fromSurface: true });
  const path = join(outDir, name + '.png');
  writeFileSync(path, Buffer.from(data, 'base64'));
  return path;
}

const rows = () =>
  page.evaluate(() =>
    [...document.querySelectorAll('#hubTicketsList .ticket-card')].map(card => ({
      number: card.querySelector('.ticket-card-number')?.textContent?.trim() || '',
      state: card.querySelector('.ticket-state-chip')?.textContent?.trim() || '',
      title: card.querySelector('.ticket-card-title')?.textContent?.trim() || '',
      owner: card.querySelector('.ticket-card-owner')?.textContent?.trim() || '',
      meta: card.querySelector('.ticket-card-meta')?.innerText?.replace(/\s+/g, ' ').trim() || ''
    }))
  );

const countLabel = () =>
  page.evaluate(() => document.getElementById('hubTicketsCount')?.textContent?.trim() || '');

async function settle() {
  await page.waitForTimeout(700);
}

await page.goto(`${base}/workspaces/${studioId}`, { waitUntil: 'domcontentloaded' });
await page.waitForTimeout(2500);
await clearOverlays();
await page.click('[data-cmd-section="tickets"]');
await page.waitForSelector('#hubTicketsFilters', { state: 'visible', timeout: 8000 });
await settle();
console.log('→ initial list:', JSON.stringify(await rows(), null, 2));
console.log('  count label:', await countLabel());
console.log('  screenshot:', await shot('01-list'));

console.log('\n→ filter: Backlog only');
await page.click('[data-ticket-state-filter="backlog"]');
await settle();
console.log('  rows:', JSON.stringify(await rows()));
console.log(
  '  chip aria-pressed:',
  await page.getAttribute('[data-ticket-state-filter="backlog"]', 'aria-pressed')
);
console.log('  screenshot:', await shot('02-filter-backlog'));

console.log('\n→ clear filters, then search');
await page.click('[data-ticket-state-filter="all"]');
await settle();
await page.fill('#hubTicketsSearch', 'flaky');
await settle();
console.log('  search rows:', JSON.stringify(await rows()));
console.log('  screenshot:', await shot('03-search'));

console.log('\n→ empty-result state distinguishes "no match" from "none yet"');
await page.fill('#hubTicketsSearch', 'zzz-nothing-matches');
await settle();
console.log(
  '  empty copy:',
  await page.evaluate(() => document.getElementById('hubTicketsEmpty')?.textContent?.trim())
);
await page.fill('#hubTicketsSearch', '');
await settle();

console.log('\n→ sort by priority');
await page.selectOption('#hubTicketsSort', 'priority');
await settle();
console.log('  rows:', JSON.stringify((await rows()).map(r => `${r.number} ${r.meta}`)));
await page.selectOption('#hubTicketsSort', '');
await settle();

console.log('\n→ descendant roll-up (owner badges)');
await page.check('#hubTicketsRollUp');
await settle();
const rolled = await rows();
console.log('  rows:', JSON.stringify(rolled, null, 2));
console.log('  owner badges present:', rolled.some(r => r.owner));
console.log('  screenshot:', await shot('04-rollup'));
await page.uncheck('#hubTicketsRollUp');
await settle();

console.log('\n→ archive filter (14-day boundary)');
await page.check('#hubTicketsArchive');
await settle();
console.log('  archived rows:', JSON.stringify(await rows()));
console.log('  screenshot:', await shot('05-archive'));
await page.uncheck('#hubTicketsArchive');
await settle();

console.log('\n→ open detail and promote Backlog to Ready');
const backlogCard = page.locator('#hubTicketsList .ticket-card[data-state="backlog"]').first();
await backlogCard.locator('.ticket-card-open').click();
await page.waitForSelector('#hubTicketDetail:not([hidden])');
await page.waitForFunction(
  () => !/Loading/.test(document.getElementById('hubTicketDetailTitle')?.textContent || '')
);
console.log(
  '  detail sections:',
  await page.evaluate(() =>
    [...document.querySelectorAll('#hubTicketDetailBody .ticket-detail-section-title')].map(h =>
      h.textContent.trim()
    )
  )
);
console.log(
  '  legal actions offered:',
  await page.evaluate(() =>
    [...document.querySelectorAll('#hubTicketDetailBody .ticket-action')].map(b =>
      b.textContent.trim()
    )
  )
);
console.log('  screenshot:', await shot('06-detail'));

await page.click('#hubTicketDetailBody [data-transition="ready"]');
await settle();
console.log(
  '  after promote — state:',
  await page.evaluate(
    () =>
      [...document.querySelectorAll('#hubTicketDetailBody .ticket-detail-row')]
        .map(r => r.innerText.replace(/\s+/g, ' ').trim())
        .find(t => t.startsWith('State')) || ''
  )
);
console.log(
  '  live region:',
  await page.evaluate(() => document.getElementById('hubTicketsLiveRegion')?.textContent?.trim())
);
console.log('  screenshot:', await shot('07-promoted'));

console.log('\n→ focus returns to the originating card on close');
const focusReturned = await page.evaluate(() => {
  const openerId = document.querySelector('#hubTicketDetail') ? true : false;
  document.getElementById('hubTicketDetailClose').click();
  const active = document.activeElement;
  return {
    hadDetail: openerId,
    focusedClass: active ? active.className : '',
    insideList: Boolean(active && active.closest && active.closest('#hubTicketsList'))
  };
});
console.log('  ', JSON.stringify(focusReturned));

console.log('\nproblems:', problems.length ? problems : 'none');
await browser.close();
