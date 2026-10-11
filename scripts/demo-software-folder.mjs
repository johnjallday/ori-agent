// Drive the software-project folder card in a seeded demo sandbox and capture
// its states: a recognized project, a choice among several, and a folder that
// already has a workspace.
//
// Usage:
//   ./scripts/demo-server.sh 8947 "$TMPDIR/ori-software-demo"      (background)
//   ./scripts/software-folder-seed.sh "$TMPDIR/ori-software-demo" --ori
//   node scripts/demo-software-folder.mjs http://localhost:8947 "$TMPDIR/ori-software-demo" <shots-dir>
//
// The script reads the seeded folders only to hash them: every folder must be
// byte-for-byte the same after the card has looked at it and after a setup.
import { chromium } from 'playwright';
import { createHash } from 'node:crypto';
import { existsSync, lstatSync, mkdirSync, readFileSync, readdirSync, readlinkSync } from 'node:fs';
import { join, relative } from 'node:path';

const [base, sandbox, output] = process.argv.slice(2);
if (!base || !sandbox || !output)
  throw new Error('usage: demo-software-folder.mjs <base-url> <sandbox-dir> <shots-dir>');
mkdirSync(output, { recursive: true });

const oriCheckout = join(sandbox, 'Documents', 'ori-agent');
const documentsProject = existsSync(oriCheckout) ? oriCheckout : join(sandbox, 'Documents', 'widget');
const folders = {
  documents: documentsProject,
  api: join(sandbox, 'Desktop', 'api'),
  site: join(sandbox, 'Desktop', 'site'),
  scratchpad: join(sandbox, 'Downloads', 'scratchpad')
};
for (const [name, dir] of Object.entries(folders))
  if (!existsSync(dir)) throw new Error(`${name} is not seeded at ${dir}; run software-folder-seed.sh`);

// hashFolder digests every entry's relative name, kind and bytes.
function hashFolder(root) {
  const lines = [];
  const walk = dir => {
    for (const name of readdirSync(dir).sort()) {
      const full = join(dir, name);
      const rel = relative(root, full);
      const info = lstatSync(full);
      if (info.isSymbolicLink()) lines.push(`l ${rel} -> ${readlinkSync(full)}`);
      else if (info.isDirectory()) {
        lines.push(`d ${rel}`);
        walk(full);
      } else lines.push(`f ${rel} ${createHash('sha256').update(readFileSync(full)).digest('hex')}`);
    }
  };
  walk(root);
  return createHash('sha256').update(lines.join('\n')).digest('hex');
}
const hashes = () => Object.fromEntries(Object.entries(folders).map(([k, dir]) => [k, hashFolder(dir)]));

const browser = await chromium.launch();
const context = await browser.newContext({ viewport: { width: 1280, height: 900 }, baseURL: base });
const page = await context.newPage();
const errors = [];
page.on('pageerror', error => errors.push(error.message));

async function shot(name) {
  const path = join(output, `${name}.png`);
  await page.screenshot({ path });
  console.log(path);
}
async function api(method, path, data) {
  const response = await context.request.fetch(path, { method, data });
  const text = await response.text();
  if (!response.ok()) throw new Error(`${method} ${path}: ${response.status()} ${text}`);
  return text ? JSON.parse(text) : {};
}
async function workspaceCount() {
  const list = await api('GET', '/api/workspaces');
  const items = Array.isArray(list) ? list : list.workspaces || list.folders || list.data || [];
  if (!Array.isArray(items)) throw new Error(`unexpected workspace list: ${JSON.stringify(list).slice(0, 200)}`);
  return items.length;
}
// show opens the chooser the way Home's "Explore a folder" does and picks a chip.
async function show(chip) {
  await page.goto('/?panel=today&folder=show');
  const chooser = page.locator('#personalAssistantFolderChooser');
  await chooser.waitFor({ state: 'visible', timeout: 30000 });
  await chooser.getByRole('button', { name: chip, exact: true }).click();
  const card = page.locator('#personalAssistantFolderOffer');
  await card.waitFor({ state: 'visible', timeout: 30000 });
  await page.locator('#personalAssistantFolderOfferHeadline').waitFor({ state: 'visible' });
  await card.scrollIntoViewIfNeeded();
  return card;
}
const text = id => page.locator(id).innerText();
const buttons = card => card.locator('#personalAssistantFolderOfferActions').locator('button, a').allInnerTexts();
function expect(condition, message) {
  if (!condition) throw new Error(message);
}

try {
  await api('POST', '/api/onboarding/skip');
  await api('POST', '/api/settings/workspace-root', { workspace_root: '' });
  let relationship = (await api('GET', '/api/personal-assistant')).personal_assistant;
  if (relationship.state === 'needs_hire') {
    await api('POST', '/api/personal-assistant/hire', {
      request_id: 'software-demo-hire',
      if_version: relationship.state_version ?? 0,
      display_name: 'Atlas',
      mandate: 'Keep my projects moving.',
      focus_areas: ['plan_my_day']
    });
    relationship = (await api('GET', '/api/personal-assistant')).personal_assistant;
    await api('POST', '/api/personal-assistant/hq', {
      request_id: 'software-demo-hq',
      if_version: relationship.state_version,
      name: 'My HQ',
      timezone: 'UTC'
    });
  }
  const before = { hashes: hashes(), workspaces: await workspaceCount() };

  // 1. One recognized project: the Ori checkout (or the widget fixture).
  let card = await show('Documents');
  const recognized = {
    headline: await text('#personalAssistantFolderOfferHeadline'),
    note: await text('#personalAssistantFolderOfferCapability'),
    buttons: await buttons(card)
  };
  console.log('recognized:', JSON.stringify(recognized));
  expect(/looks like a/.test(recognized.headline), `not recognized: ${recognized.headline}`);
  expect(/Nothing in it was opened\./.test(recognized.note), `no recognition note: ${recognized.note}`);
  if (documentsProject === oriCheckout)
    expect(/linked Git checkout/.test(recognized.note), `the Ori worktree was not recognized as one: ${recognized.note}`);
  await shot('01-recognized-project');

  // 2. Two projects side by side: a choice, and no setup until it is made.
  card = await show('Desktop');
  const choice = {
    headline: await text('#personalAssistantFolderOfferHeadline'),
    question: await text('#personalAssistantFolderOfferQuestion'),
    buttons: await buttons(card)
  };
  console.log('choice:', JSON.stringify(choice));
  expect(/holds 2 software projects/.test(choice.headline), `no choice: ${choice.headline}`);
  expect(!choice.buttons.some(label => /^Set up/.test(label)), `setup offered before a choice: ${choice.buttons}`);
  await shot('02-choose-which-project');
  await card.getByRole('button', { name: /^site/ }).click();
  await page.waitForFunction(
    () => /site looks like/.test(document.getElementById('personalAssistantFolderOfferHeadline')?.textContent || '')
  );
  console.log('chosen:', await text('#personalAssistantFolderOfferHeadline'), await buttons(card));
  await shot('03-chosen-project');

  expect((await workspaceCount()) === before.workspaces, 'showing folders created a workspace');
  expect(JSON.stringify(hashes()) === JSON.stringify(before.hashes), 'showing folders changed one of them');

  // 3. An unrelated project with no Git, set up through the existing reviewed create.
  card = await show('Downloads');
  console.log('unrelated:', await text('#personalAssistantFolderOfferHeadline'), '|', await text('#personalAssistantFolderOfferCapability'));
  await shot('04-unrelated-project');
  await card.getByRole('button', { name: 'Set up', exact: true }).click();
  await page.locator('#personalAssistantFolderReceipt li').first().waitFor({ state: 'visible', timeout: 60000 });
  console.log('receipt:', (await page.locator('#personalAssistantFolderReceipt li').allInnerTexts()).join(' | '));
  await shot('05-set-up-receipt');
  expect((await workspaceCount()) === before.workspaces + 1, 'the setup did not create exactly one workspace');

  // 4. The same folder again: the workspace that covers it, and no second setup.
  card = await show('Downloads');
  const existing = {
    headline: await text('#personalAssistantFolderOfferHeadline'),
    question: await text('#personalAssistantFolderOfferQuestion'),
    buttons: await buttons(card)
  };
  console.log('existing:', JSON.stringify(existing));
  expect(/already has a workspace/.test(existing.headline), `existing workspace not shown: ${existing.headline}`);
  expect(existing.buttons.some(label => /^Open /.test(label)), `no Open control: ${existing.buttons}`);
  expect(!existing.buttons.some(label => /^Set up/.test(label)), `a second setup was offered: ${existing.buttons}`);
  await shot('06-existing-workspace');
  await page.setViewportSize({ width: 390, height: 844 });
  await card.scrollIntoViewIfNeeded();
  const overflow = await card.evaluate(el => el.scrollWidth - el.clientWidth);
  expect(overflow <= 1, `the card scrolls sideways at phone width (${overflow}px)`);
  await shot('07-existing-workspace-phone');

  expect((await workspaceCount()) === before.workspaces + 1, 'showing the folder again created a workspace');
  expect(JSON.stringify(hashes()) === JSON.stringify(before.hashes), 'a seeded folder changed during the demo');
  expect(errors.length === 0, `page errors: ${errors.join('; ')}`);
  console.log('OK: folders unchanged, one workspace created, no page errors');
} finally {
  await browser.close();
}
