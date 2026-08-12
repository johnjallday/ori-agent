/*
 * Verifies the Note page's linked-tickets panel
 * (tasks/prd-workspace-ticket-management.md FR-75, FR-78).
 *
 *   node scripts/check-note-ticket-panel.mjs <noteUrl>
 *
 * The load-bearing assertion is the last one: the panel must contain no
 * buttons. A note gains no execution controls by being referenced — it is
 * knowledge, not work.
 */
import { chromium } from 'playwright';

const url = process.argv[2];
if (!url) {
  console.error('usage: node scripts/check-note-ticket-panel.mjs <noteUrl>');
  process.exit(1);
}

const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1280, height: 900 } });
const problems = [];
page.on('pageerror', e => problems.push('page error: ' + e.message));

await page.goto(url, { waitUntil: 'domcontentloaded' });
await page.waitForTimeout(3000);

const panel = await page.evaluate(() => {
  const section = document.getElementById('notePageTickets');
  return {
    exists: Boolean(section),
    hidden: section ? section.hidden : null,
    title: section?.querySelector('.note-page-tickets-title')?.textContent?.trim(),
    rows: [...(section?.querySelectorAll('.note-page-ticket') || [])].map(li => ({
      text: li.innerText.replace(/\s+/g, ' ').trim(),
      href: li.querySelector('a')?.getAttribute('href'),
      chip: li.querySelector('.ticket-state-chip')?.textContent?.trim()
    })),
    buttonsInPanel: [...(section?.querySelectorAll('button') || [])].length
  };
});

console.log(JSON.stringify(panel, null, 2));
console.log(
  'read-only (no execution controls on the note):',
  panel.buttonsInPanel === 0 ? 'yes' : `NO — ${panel.buttonsInPanel} button(s)`
);
console.log('problems:', problems.length ? problems : 'none');
await browser.close();
