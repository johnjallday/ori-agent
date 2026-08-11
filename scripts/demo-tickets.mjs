/*
 * Drives the canonical Tickets destination on a running demo server, for the
 * Group-1 Demo: checkpoint of tasks/prd-workspace-ticket-management.md.
 *
 *   node scripts/demo-tickets.mjs <studioId> [baseUrl] [outDir]
 *
 * It exercises the surface a user actually reaches: open the workspace page,
 * open Tickets from the Command view, create one Backlog and one Ready Ticket
 * through the form (explicit capture choice, FR-19), read the rendered numbers
 * and state chips back out of the DOM, and open server-backed detail.
 *
 * Screenshots go through CDP rather than page.screenshot(): this app loads a
 * blocked Google Fonts stylesheet, so document.fonts.ready never settles and
 * Playwright's screenshot path waits on it forever.
 */
import { chromium } from 'playwright';
import { mkdirSync, writeFileSync } from 'node:fs';
import { join, resolve } from 'node:path';

const studioId = process.argv[2];
if (!studioId) {
  console.error('usage: node scripts/demo-tickets.mjs <studioId> [baseUrl] [outDir]');
  process.exit(1);
}
const base = process.argv[3] || 'http://localhost:8931';
const outDir = resolve(process.argv[4] || 'tmp-ticket-demo');
mkdirSync(outDir, { recursive: true });

const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1440, height: 980 } });
const problems = [];
page.on('pageerror', error => problems.push('page error: ' + error.message));
page.on('console', msg => {
  if (msg.type() === 'error') problems.push('console error: ' + msg.text());
});
// Record which requests actually failed, so a generic "404 (Not Found)"
// console line can be attributed to a URL instead of guessed at.
page.on('response', res => {
  if (res.status() >= 400) problems.push(`HTTP ${res.status()} ${res.url()}`);
});

const cdp = await page.context().newCDPSession(page);

/**
 * Clears Bootstrap modal leftovers. The workspace page auto-opens an
 * onboarding modal and a setup-task confirm modal; hiding them leaves their
 * backdrops behind, which dim everything underneath and make the capture
 * useless. Unrelated to Tickets — this is demo-harness hygiene.
 */
const clearOverlays = () =>
  page.evaluate(() => {
    document.querySelectorAll('.modal.show').forEach(modal => {
      modal.classList.remove('show');
      modal.style.display = 'none';
      modal.setAttribute('aria-hidden', 'true');
    });
    document.querySelectorAll('.modal-backdrop, .offcanvas-backdrop').forEach(el => el.remove());
    document.body.classList.remove('modal-open');
    document.body.style.removeProperty('overflow');
    document.body.style.removeProperty('padding-right');
  });

/**
 * Freeze CSS transitions/animations. The Command view's modal fades in, and a
 * CDP capture taken mid-transition composites a half-faded panel over the
 * backdrop — which looks like a rendering defect but is only the screenshot
 * catching an in-flight animation.
 */
const freezeAnimations = () =>
  page.addStyleTag({
    content: `*, *::before, *::after {
      animation-duration: 0s !important;
      animation-delay: 0s !important;
      transition-duration: 0s !important;
      transition-delay: 0s !important;
    }`
  });

async function shot(name) {
  await clearOverlays();
  await freezeAnimations();
  await page.waitForTimeout(150);
  const { data } = await cdp.send('Page.captureScreenshot', {
    format: 'png',
    fromSurface: true,
    captureBeyondViewport: false
  });
  const path = join(outDir, name + '.png');
  writeFileSync(path, Buffer.from(data, 'base64'));
  return path;
}

/** Whatever the Tickets list currently shows, straight from the rendered DOM. */
const renderedTickets = () =>
  page.evaluate(() =>
    [...document.querySelectorAll('#hubTicketsList .ticket-card')].map(card => ({
      number: card.querySelector('.ticket-card-number')?.textContent?.trim() || '',
      state: card.querySelector('.ticket-state-chip')?.textContent?.trim() || '',
      title: card.querySelector('.ticket-card-title')?.textContent?.trim() || '',
      stateAttr: card.getAttribute('data-state') || ''
    }))
  );

async function createTicket(title, state) {
  const before = await page.locator('#hubTicketsList .ticket-card').count();
  await page.fill('#hubTicketCreateTitle', title);
  await page.check(`input[name="hubTicketCreateState"][value="${state}"]`);
  await page.click('.hub-ticket-create-submit');
  await page.waitForFunction(
    expected => document.querySelectorAll('#hubTicketsList .ticket-card').length > expected,
    before,
    { timeout: 8000 }
  );
}

console.log(`→ opening ${base}/workspaces/${studioId}`);
await page.goto(`${base}/workspaces/${studioId}`, { waitUntil: 'domcontentloaded' });
await page.waitForTimeout(2500);
console.log('  screenshot:', await shot('01-workspace-page'));

// A fresh workspace auto-starts its setup task, whose autonomy-gate modal
// covers the page and swallows pointer events. Unrelated to Tickets, but it
// has to go before anything can be clicked.
const dismissed = await page.evaluate(() => {
  const open = [...document.querySelectorAll('.modal.show')];
  open.forEach(modal => {
    modal.classList.remove('show');
    modal.style.display = 'none';
    modal.setAttribute('aria-hidden', 'true');
  });
  document.querySelectorAll('.modal-backdrop').forEach(el => el.remove());
  document.body.classList.remove('modal-open');
  document.body.style.removeProperty('overflow');
  return open.map(m => m.id || '(unnamed)');
});
if (dismissed.length) console.log('→ dismissed pre-existing modals:', dismissed.join(', '));

// The Tickets stat tile is the entry point into the canonical destination.
const tile = page.locator('[data-cmd-section="tickets"]');
const tileCount = await tile.count();
console.log(`→ Tickets stat tile present: ${tileCount > 0}`);
if (tileCount === 0) {
  console.error('FAIL: no Tickets entry point rendered in the Command view');
  console.log('problems:', problems);
  await browser.close();
  process.exit(1);
}
console.log('  tile label:', (await tile.first().innerText()).replace(/\s+/g, ' ').trim());

await tile.first().click();
await page.waitForSelector('#hubTicketCreateForm', { state: 'visible', timeout: 8000 });
await page.waitForTimeout(600);
console.log('  screenshot:', await shot('02-tickets-open'));
console.log('→ tickets already present:', JSON.stringify(await renderedTickets()));

// FR-19: creating without choosing Backlog/Ready must be refused by the form.
const submitBlocked = await page.evaluate(() => {
  const form = document.getElementById('hubTicketCreateForm');
  const title = document.getElementById('hubTicketCreateTitle');
  title.value = 'no state chosen';
  return !form.checkValidity();
});
console.log(`→ create without an explicit Backlog/Ready choice is blocked: ${submitBlocked}`);

const before = (await renderedTickets()).length;
console.log('→ creating a Backlog ticket through the form');
await createTicket('Demo: captured from the Tickets form', 'backlog');
console.log('→ creating a Ready ticket through the form');
await createTicket('Demo: committed from the Tickets form', 'ready');

const after = await renderedTickets();
console.log('→ rendered tickets:', JSON.stringify(after, null, 2));
console.log('  screenshot:', await shot('03-tickets-created'));

const created = after.length - before;
console.log(`→ created ${created} ticket(s) through the UI (expected 2)`);

// Numbers must render, and states must be real labels rather than color alone.
const missingNumber = after.filter(t => !/^#\d+$/.test(t.number));
const missingState = after.filter(t => !t.state);
console.log(`→ every card shows a ticket number: ${missingNumber.length === 0}`);
console.log(`→ every card shows a text state label: ${missingState.length === 0}`);

console.log('→ opening server-backed detail for the first ticket');
await page.click('#hubTicketsList .ticket-card:first-child .ticket-card-open');
await page.waitForSelector('#hubTicketDetail:not([hidden])', { timeout: 8000 });
await page.waitForFunction(
  () => !/Loading/.test(document.getElementById('hubTicketDetailTitle')?.textContent || ''),
  undefined,
  { timeout: 8000 }
);
const detail = await page.evaluate(() => ({
  title: document.getElementById('hubTicketDetailTitle')?.textContent?.trim() || '',
  rows: [...document.querySelectorAll('#hubTicketDetailBody .ticket-detail-row')].map(row =>
    row.innerText.replace(/\s+/g, ' ').trim()
  ),
  history: [...document.querySelectorAll('#hubTicketDetailBody .ticket-detail-history li')].map(li =>
    li.textContent.trim()
  )
}));
console.log('→ detail:', JSON.stringify(detail, null, 2));
console.log('  screenshot:', await shot('04-ticket-detail'));

console.log('\nproblems:', problems.length ? problems : 'none');
await browser.close();
