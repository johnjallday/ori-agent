/*
 * Drives the song-facts demo checkpoints (tasks/tasks-song-facts-collection-brief.md)
 * against a running isolated demo server, headless, and saves screenshots.
 *
 *   node scripts/demo-song-facts.mjs <baseUrl> <outDir> card <chip>
 *       scan the chip, open its card, screenshot it, print the plan lines
 *   node scripts/demo-song-facts.mjs <baseUrl> <outDir> setup <chip>
 *       press Set up on the chip's card, wait for the pop-up's last screen,
 *       screenshot it, print its song rows and the Home library link
 *   node scripts/demo-song-facts.mjs <baseUrl> <outDir> library <path> [name] [light|dark] [WxH]
 *       open the Home library, screenshot it, print "Recently saved" and the rows
 *
 * Prepare the sandbox first: ./scripts/smoke.sh showfolder <url> hq, … model, and
 * … seed-portfolio <sandbox> <count> <chip>. Prints console errors and failed
 * requests, so a page that renders but is quietly broken does not pass.
 */
import { chromium } from 'playwright';
import { mkdirSync } from 'node:fs';
import { join, resolve } from 'node:path';

const [baseUrl, outDir, step, ...args] = process.argv.slice(2);
if (!baseUrl || !outDir || !step) {
  console.error('usage: node scripts/demo-song-facts.mjs <baseUrl> <outDir> card|setup|library …');
  process.exit(1);
}
mkdirSync(resolve(outDir), { recursive: true });
const shot = name => join(resolve(outDir), `${name}.png`);

async function api(path, body) {
  const response = await fetch(`${baseUrl}${path}`, {
    method: body ? 'POST' : 'GET',
    headers: { 'Content-Type': 'application/json' },
    body: body ? JSON.stringify(body) : undefined
  });
  const text = await response.text();
  if (!response.ok) throw new Error(`${path}: ${response.status} ${text}`);
  return text ? JSON.parse(text) : {};
}

async function openCard(page) {
  await page.goto(`${baseUrl}/`, { waitUntil: 'domcontentloaded' });
  await page.waitForFunction(() => Boolean(window.PersonalAssistantPanel?.open));
  await page.evaluate(() =>
    window.PersonalAssistantPanel.open(document.getElementById('personalAssistantLauncher'), {
      view: 'today'
    })
  );
  const card = page.locator('#personalAssistantFolderOffer');
  await card.waitFor({ state: 'visible', timeout: 30_000 });
  await card.scrollIntoViewIfNeeded();
  return card;
}

const [width, height] = (args.find(arg => /^\d+x\d+$/.test(arg)) || '1440x900')
  .split('x')
  .map(Number);
const scheme = args.includes('dark') ? 'dark' : 'light';
const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width, height }, colorScheme: scheme });
await page.addInitScript(theme => window.localStorage.setItem('ori-theme', theme), scheme);
const problems = [];

// The Home library at path, its rows loaded.
async function openLibrary(path) {
  await page.goto(`${baseUrl}${path}`, { waitUntil: 'domcontentloaded' });
  await page.locator('#projectLibraryRows tr').first().waitFor({ timeout: 60_000 });
  await page.waitForTimeout(1200);
}

// Prints "Recently saved", the switch, and every row's muted line.
async function printLibrary() {
  const lines = selector =>
    page
      .locator(selector)
      .evaluateAll(items =>
        items.map(
          item =>
            `${item.querySelector('h4, strong')?.textContent} — ${item.querySelector('small.project-library-saved, h4 + small')?.textContent || ''}`
        )
      );
  console.log('Recently saved:');
  (await lines('#projectLibraryRecentCards article')).forEach(row => console.log(`  ${row}`));
  const songDetails = page.locator('#projectLibrarySongDetails');
  if (await songDetails.isVisible()) {
    const on = await page.locator('#projectLibrarySongDetailsSwitch').isChecked();
    console.log(
      `Switch: ${on ? 'on' : 'off'} — ${await page.locator('#projectLibrarySongDetailsNote').textContent()}`
    );
  } else {
    console.log('Switch: not shown');
  }
  console.log('Library rows:');
  (await lines('#projectLibraryRows tr')).forEach(row => console.log(`  ${row}`));
}

async function panelShot(name) {
  const file = shot(`${name}-${scheme}-${width}`);
  await page.locator('#projectLibraryPanel').screenshot({ path: file });
  console.log(`saved ${file}`);
}

const named = (value, fallback) =>
  value && !/^(light|dark|on|off|\d+x\d+)$/.test(value) ? value : fallback;
page.on(
  'console',
  message => message.type() === 'error' && problems.push(`console: ${message.text()}`)
);
page.on('requestfailed', request => problems.push(`request failed: ${request.url()}`));
page.on(
  'response',
  response =>
    response.status() >= 400 && problems.push(`HTTP ${response.status()}: ${response.url()}`)
);

try {
  if (step === 'card') {
    const chip = (args[0] || 'desktop').toLowerCase();
    const { offer } = await api('/api/personal-assistant/folder-digest/scan', { chip });
    if (!offer?.portfolio) throw new Error(`the ${chip} scan made no collection offer`);
    for (const line of offer.plan?.lines || [])
      console.log(`  [${line.kind}] ${line.name} — ${line.detail || ''}`);
    const card = await openCard(page);
    await page.waitForTimeout(600);
    await card.screenshot({ path: shot(`card-${chip}`) });
    console.log(`offer ${offer.id}; saved ${shot(`card-${chip}`)}`);
  } else if (step === 'setup') {
    const chip = (args[0] || 'desktop').toLowerCase();
    const card = await openCard(page);
    await card.getByRole('button', { name: 'Set up', exact: true }).click();
    const modal = page.locator('#folderSetupRunModal');
    await modal.waitFor({ state: 'visible' });
    await page
      .locator('#folderSetupRunTitle', { hasText: 'Pick a song to start with' })
      .waitFor({ timeout: 300_000 });
    await page.waitForTimeout(500);
    await modal.locator('.modal-content').screenshot({ path: shot(`setup-${chip}`) });
    const rows = await page
      .locator('#folderSetupRunSongs li')
      .evaluateAll(items =>
        items.map(
          item =>
            `${item.querySelector('strong')?.textContent} — ${item.querySelector('small')?.textContent}`
        )
      );
    rows.forEach(row => console.log(`  ${row}`));
    const browse = await modal.getByRole('link', { name: /^Browse all/ }).getAttribute('href');
    console.log(`library ${browse}; saved ${shot(`setup-${chip}`)}`);
  } else if (step === 'library') {
    await openLibrary(args[0]);
    await printLibrary();
    await panelShot(named(args[1], 'library'));
  } else if (step === 'switch') {
    // switch <path> on|off [name]: set the song-details switch and wait for its status.
    const want = args.includes('on');
    await openLibrary(args[0]);
    const toggle = page.locator('#projectLibrarySongDetailsSwitch');
    if ((await toggle.isChecked()) !== want) {
      await toggle.click();
      await page
        .locator('#projectLibraryStatus', {
          hasText: want ? 'Song details are on' : 'Song details cleared'
        })
        .waitFor({ timeout: 30_000 });
    }
    console.log(`Status: ${await page.locator('#projectLibraryStatus').textContent()}`);
    await printLibrary();
    await panelShot(named(args[2], want ? 'switch-on' : 'switch-off'));
  } else if (step === 'rescan') {
    // rescan <path> [name]: review and commit one scan of the first folder.
    await openLibrary(args[0]);
    await page
      .locator('#projectLibraryRoots article.project-library-root')
      .first()
      .getByRole('button', { name: 'Review scan' })
      .click();
    await page.getByRole('button', { name: 'Scan metadata' }).click();
    await page.waitForTimeout(4000);
    await openLibrary(args[0]);
    await printLibrary();
    await panelShot(named(args[1], 'rescan'));
  } else {
    throw new Error(`unknown step ${step}`);
  }
} finally {
  await browser.close();
}
if (problems.length) {
  console.log(`${problems.length} problem(s):`);
  for (const problem of [...new Set(problems)].slice(0, 12)) console.log(`  ${problem}`);
}
