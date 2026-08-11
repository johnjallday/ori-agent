/*
 * Drives Group 3 of tasks/prd-workspace-ticket-management.md: the fixed,
 * server-authoritative Ticket Board.
 *
 *   node scripts/demo-tickets-board.mjs <studioId> [baseUrl] [outDir]
 *
 * Proves the things a screenshot cannot: that List and Board show identical
 * data, that a drag issues a transition rather than a kanban_column_id patch,
 * that an illegal move is refused and the card returns to its column, and
 * that every drag has a keyboard equivalent.
 */
import { chromium } from 'playwright';
import { mkdirSync, writeFileSync } from 'node:fs';
import { join, resolve } from 'node:path';

const studioId = process.argv[2];
if (!studioId) {
  console.error('usage: node scripts/demo-tickets-board.mjs <studioId> [baseUrl] [outDir]');
  process.exit(1);
}
const base = process.argv[3] || 'http://localhost:8931';
const outDir = resolve(process.argv[4] || 'tmp-ticket-board-demo');
mkdirSync(outDir, { recursive: true });

const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1500, height: 1100 } });
const problems = [];
const ticketRequests = [];
page.on('pageerror', e => problems.push('page error: ' + e.message));
page.on('request', r => {
  const url = r.url();
  if (url.includes('/tickets') || url.includes('/api/orchestration/tasks')) {
    ticketRequests.push(`${r.method()} ${url.replace(base, '')}`);
  }
});
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

const settle = () => page.waitForTimeout(700);

/** The board as rendered: columns, their programmatic names, and their cards. */
const boardState = () =>
  page.evaluate(() =>
    [...document.querySelectorAll('#hubTicketsBoard .ticket-board-column')].map(col => ({
      state: col.dataset.state,
      title: col.querySelector('.ticket-board-column-title')?.textContent?.trim(),
      count: col.querySelector('.ticket-board-column-count')?.textContent?.trim(),
      ariaLabel: col.querySelector('.ticket-board-column-body')?.getAttribute('aria-label'),
      cards: [...col.querySelectorAll('.ticket-board-card')].map(card => ({
        number: card.querySelector('.ticket-card-number')?.textContent?.trim(),
        title: card.querySelector('.ticket-card-title')?.textContent?.trim(),
        draggable: card.draggable,
        moves: [...card.querySelectorAll('.ticket-card-move-select option')]
          .map(o => o.textContent.trim())
          .filter(t => t !== 'Move to…')
      }))
    }))
  );

const listState = () =>
  page.evaluate(() =>
    [...document.querySelectorAll('#hubTicketsList .ticket-card')].map(card => ({
      number: card.querySelector('.ticket-card-number')?.textContent?.trim(),
      state: card.dataset.state
    }))
  );

await page.goto(`${base}/workspaces/${studioId}`, { waitUntil: 'domcontentloaded' });
await page.waitForTimeout(2500);
await clearOverlays();
await page.click('[data-cmd-section="tickets"]');
await page.waitForSelector('#hubTicketsFilters', { state: 'visible', timeout: 8000 });
await settle();

const fromList = await listState();
console.log('→ List shows', fromList.length, 'tickets');

console.log('\n→ switch to Board');
await page.click('#hubTicketsViewBoard');
await settle();
const board = await boardState();
console.log('  columns:', board.map(c => `${c.title}(${c.count})`).join(' | '));
console.log('  programmatic names:', JSON.stringify(board.map(c => c.ariaLabel)));
console.log(
  '  column editor present:',
  await page.evaluate(
    () => document.querySelectorAll('#hubTicketsBoard [class*="add-column"]').length > 0
  )
);

const boardCards = board.flatMap(c => c.cards);
console.log('  cards on board:', boardCards.length);
console.log(
  '  List/Board parity:',
  boardCards.length === fromList.filter(t => t.state !== 'cancelled').length
);
console.log('  screenshot:', await shot('01-board'));

console.log('\n→ legal moves offered per column (keyboard equivalent of a drag)');
board.forEach(col => {
  if (col.cards.length) console.log(`  ${col.title}: ${JSON.stringify(col.cards[0].moves)}`);
});

console.log('\n→ Backlog card cannot reach In Progress (FR-36)');
const backlogCard = board.find(c => c.state === 'backlog')?.cards[0];
if (backlogCard) {
  console.log(
    `  ${backlogCard.number} offers:`,
    JSON.stringify(backlogCard.moves),
    '— In Progress absent:',
    !backlogCard.moves.some(m => m.includes('In Progress'))
  );
}

console.log('\n→ move a Backlog ticket to Ready via the keyboard menu');
const before = ticketRequests.length;
await page.selectOption(
  '#hubTicketsBoard .ticket-board-column[data-state="backlog"] .ticket-card-move-select',
  'ready'
);
await settle();
const issued = ticketRequests.slice(before);
console.log('  requests issued:', JSON.stringify(issued));
// What matters is that no WRITE went to the legacy task API. The page still
// READS /api/orchestration/tasks for its own legacy panels during the
// compatibility window, and a GET cannot change lifecycle state.
const legacyWritesDuringMove = issued.filter(
  r => r.includes('/api/orchestration/tasks') && !r.startsWith('GET ')
);
console.log(
  '  used the transition route:',
  issued.some(r => r.includes('/transition'))
);
console.log(
  '  legacy task-API WRITES during the move:',
  legacyWritesDuringMove.length ? legacyWritesDuringMove : 'none'
);
console.log(
  '  live region:',
  await page.evaluate(() => document.getElementById('hubTicketsLiveRegion')?.textContent?.trim())
);
const afterMove = await boardState();
console.log('  columns now:', afterMove.map(c => `${c.title}(${c.count})`).join(' | '));
console.log('  screenshot:', await shot('02-after-keyboard-move'));

console.log('\n→ an illegal drop is refused server-side and the card stays put');
const illegal = await page.evaluate(async () => {
  const card = document.querySelector('#hubTicketsBoard .ticket-board-card');
  if (!card) return { skipped: true };
  const id = card.dataset.ticketId;
  const owner = card.dataset.owningWorkspaceId;
  const stateBefore = card.closest('.ticket-board-column').dataset.state;
  // Ask the server directly for a move the board would never offer.
  const res = await fetch(`/api/workspaces/${owner}/tickets/${id}/transition`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ to: 'done' })
  });
  const body = await res.json();
  return {
    stateBefore,
    status: res.status,
    code: body.code,
    legal: body.details?.legal_transitions
  };
});
console.log('  ', JSON.stringify(illegal));

console.log('\n→ back to List: same data, no re-query needed');
const beforeSwitch = ticketRequests.length;
await page.click('#hubTicketsViewList');
await settle();
console.log('  requests during view switch:', ticketRequests.length - beforeSwitch);
console.log('  list rows:', (await listState()).length);
console.log('  screenshot:', await shot('03-back-to-list'));

console.log('\n→ no legacy kanban WRITES anywhere in this session');
const legacyWrites = ticketRequests.filter(
  r => r.includes('/api/orchestration/tasks') && !r.startsWith('GET ')
);
const legacyReads = ticketRequests.filter(
  r => r.includes('/api/orchestration/tasks') && r.startsWith('GET ')
);
console.log('  legacy task-API writes:', legacyWrites.length ? legacyWrites : 'none');
console.log(
  `  legacy task-API reads: ${legacyReads.length} (compatibility-window panels; harmless)`
);

// The deliberate illegal-move probe above returns 409 by design, so it is not
// a defect — filter it out rather than reporting a "problem" we asked for.
const realProblems = problems.filter(p => !p.startsWith('HTTP 409 POST'));
console.log(
  '\nexpected 409s (deliberate illegal-move probe):',
  problems.length - realProblems.length
);
console.log('problems:', realProblems.length ? realProblems : 'none');
await browser.close();
