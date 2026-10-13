// demo-folder-collection.mjs — drive one entry of the folder-of-projects
// journey in a real browser and print what a person would see.
//
// Run it through its wrapper, which makes the sandbox, seeds the folder and
// starts and stops the isolated server:
//
//   ./scripts/demo-folder-collection.sh
//
// Usage (by the wrapper):
//   node scripts/demo-folder-collection.mjs <base-url> <sandbox> <shots-dir> <phase>
//
// Phases:
//   composer  Add folder in the assistant's composer: the proposed group is on
//             screen before anything is sent; Send asks the real model, whose
//             reply is printed; Review group setup, Set up, the receipt.
//   home      Home's "Explore a folder": the same proposal as one offer, Adjust
//             (the group is renamed), Set up, the receipt. No model is asked.
//
// Each phase prints the card, the plan, the receipt and the folder's hash
// before and after, and fails when the folder changed, when the group and its
// six linked workspaces were not made, or on a page error.
import { chromium } from 'playwright';
import { createHash } from 'node:crypto';
import { lstatSync, mkdirSync, readFileSync, readdirSync, realpathSync } from 'node:fs';
import { join, relative } from 'node:path';

const PHASES = ['composer', 'home'];
const [base, sandbox, output, phase] = process.argv.slice(2);
if (!base || !sandbox || !output || !PHASES.includes(phase)) {
  console.error(
    `usage: node scripts/demo-folder-collection.mjs <base-url> <sandbox> <shots-dir> <${PHASES.join('|')}>`
  );
  process.exit(2);
}
mkdirSync(output, { recursive: true });

const MEMBERS = ['Album-1', 'Album-2', 'Album-3', 'Album-4', 'Album-5', 'Logic Sketch'];

// hashFolder digests every entry's relative name, kind and bytes.
function hashFolder(root) {
  const lines = [];
  const walk = dir => {
    for (const name of readdirSync(dir).sort()) {
      const full = join(dir, name);
      const rel = relative(root, full);
      if (lstatSync(full).isDirectory()) {
        lines.push(`d ${rel}`);
        walk(full);
      } else
        lines.push(`f ${rel} ${createHash('sha256').update(readFileSync(full)).digest('hex')}`);
    }
  };
  walk(root);
  return createHash('sha256').update(lines.join('\n')).digest('hex');
}

const browser = await chromium.launch();
const context = await browser.newContext({
  viewport: { width: 1280, height: 900 },
  baseURL: base
});
const page = await context.newPage();
const errors = [];
const asks = [];
page.on('pageerror', error => errors.push(error.message));
page.on('request', request => {
  if (/\/api\/home-assistant\/ask$/.test(new URL(request.url()).pathname)) asks.push(request.url());
});

const shot = name => page.screenshot({ path: join(output, `${phase}-${name}.png`) });
function expect(condition, message) {
  if (!condition) throw new Error(message);
}
async function api(method, path, data) {
  const response = await context.request.fetch(path, { method, data });
  const text = await response.text();
  expect(response.ok(), `${method} ${path}: ${response.status()} ${text.slice(0, 300)}`);
  return text ? JSON.parse(text) : {};
}
async function workspaces() {
  const listed = await api('GET', '/api/workspaces');
  return Array.isArray(listed) ? listed : listed.workspaces || [];
}

const card = page.locator('#personalAssistantFolderOffer');
// Each line is drawn as a marker, a name and an optional detail.
const planLines = async () =>
  (await card.locator('li').allInnerTexts()).map(line =>
    line
      .replace(/\s*\n\s*/g, ' · ')
      .replace(/^•\s*·\s*/, '')
      .trim()
  );
const actions = () =>
  card.locator('#personalAssistantFolderOfferActions').locator('button, a').allInnerTexts();

// setUp presses Set up and waits for the run's own end: its receipt.
async function setUp(groupName) {
  const started = Date.now();
  await card.getByRole('button', { name: 'Set up', exact: true }).click();
  const done = page.locator('.modal.show').first();
  await done.getByText(`Workspace · ${groupName} (group)`).waitFor({ timeout: 120000 });
  const seconds = ((Date.now() - started) / 1000).toFixed(1);
  const receipt = (await done.innerText()).replace(/\n{2,}/g, '\n');
  console.log(`the receipt (after ${seconds} s):\n${receipt}`);
  await shot('receipt');
  return receipt;
}

// made checks what the run left: one group of that name and a workspace for
// every folder, each linked to its own folder where it is, with no task.
async function made(groupName, root, before) {
  const all = await workspaces();
  const group = all.find(item => item.kind === 'group' && item.name === groupName);
  expect(group, `no group named ${groupName}`);
  const children = all.filter(item => item.parent_id === group.id);
  const linked = children
    .map(child => {
      const primary = String(child.shared_data?.primary_directory_id || '');
      const reference = (child.directory_references || []).find(item => item.id === primary);
      expect(reference, `${child.name} has no primary directory`);
      expect(!child.open_task_count, `${child.name} was given a task`);
      return reference.path;
    })
    .sort();
  const wanted = MEMBERS.map(name => join(realpathSync(root), name)).sort();
  expect(
    JSON.stringify(linked) === JSON.stringify(wanted),
    `the workspaces are linked to ${JSON.stringify(linked)}`
  );
  expect(
    all.length === before.workspaces + 7,
    `the setup made ${all.length - before.workspaces} workspaces, not 7`
  );
  const after = hashFolder(root);
  console.log(`folder hash before: ${before.hash}\nfolder hash after:  ${after}`);
  expect(after === before.hash, 'the folder changed');
  console.log(
    `made: group ${group.name} with ${children.length} workspaces: ${children.map(child => child.name).join(', ')}`
  );
}

async function composerPhase() {
  const root = join(sandbox, 'Downloads');
  const configured = await context.request.post('/api/settings/system-model', {
    data: { provider: 'codex', model: 'gpt-5.6-luna' }
  });
  expect(configured.ok(), `no model provider in this sandbox (${configured.status()})`);
  const before = { hash: hashFolder(root), workspaces: (await workspaces()).length };

  // 1. Add folder. The proposal is the server's own, shown before any reply.
  await page.goto('/');
  const panel = page.locator('#personalAssistantPanel');
  await page.locator('#personalAssistantLauncher').waitFor({ state: 'visible', timeout: 30000 });
  if (!(await panel.isVisible())) await page.locator('#personalAssistantLauncher').click();
  await page.locator('#personalAssistantFolderChip').click();
  const attached = page.waitForResponse(
    response =>
      response.request().method() === 'POST' &&
      /\/api\/home-assistant\/folder-context\/select$/.test(response.url()),
    { timeout: 60000 }
  );
  await page
    .locator('#personalAssistantFolderChoices')
    .getByRole('button', { name: 'Downloads', exact: true })
    .click();
  expect((await attached).ok(), 'the folder was not attached');
  const proposal = page.locator('#personalAssistantFolderStructure');
  await proposal.waitFor({ state: 'visible', timeout: 30000 });
  console.log(`the card, before anything is sent:\n${await proposal.innerText()}`);
  expect(asks.length === 0, 'the model was asked before Send');
  for (const name of MEMBERS)
    expect((await proposal.innerText()).includes(name), `the card does not list ${name}`);
  await shot('01-card-before-send');

  // 2. Send. The real model answers; it is told to lead with the proposal.
  await page.locator('#personalAssistantSend').click();
  await page
    .locator('[data-folder-setup-suggestion]')
    .first()
    .waitFor({ state: 'visible', timeout: 240000 });
  const message = page.locator('#homeAssistantConversation [data-message-role="assistant"]').last();
  // The message element also holds its scope label, its choices and the card.
  const reply = (await message.innerText()).split(/\n+App-wide\n/)[0].trim();
  console.log(`the model's reply:\n${reply.slice(0, 1500)}`);
  console.log(
    `the reply speaks of a group in its first 400 characters: ${/group/i.test(reply.slice(0, 400)) ? 'yes' : 'NO'}`
  );
  const choices = await page.locator('[data-folder-discussion] button').allInnerTexts();
  console.log('the choices under it:', JSON.stringify(choices));
  expect(
    choices.includes('Set up this structure') && choices.includes('Adjust…'),
    `the reply's choices: ${choices}`
  );
  await shot('02-reply');

  // 3. One review: everything Set up will do, and nothing made yet.
  await page.getByRole('button', { name: 'Set up this structure', exact: true }).last().click();
  await card.waitFor({ state: 'visible', timeout: 30000 });
  await card.getByRole('button', { name: 'Set up', exact: true }).waitFor({ timeout: 30000 });
  const plan = await planLines();
  console.log(`the plan:\n${plan.map(line => `  - ${line}`).join('\n')}`);
  expect(/^Creates group Downloads/.test(plan[0] || ''), `the plan starts with ${plan[0]}`);
  expect(
    plan.includes('Nothing is moved, copied or read'),
    'the plan does not say the folder is left alone'
  );
  expect(
    (await workspaces()).length === before.workspaces,
    'reviewing the setup created a workspace'
  );
  await card.scrollIntoViewIfNeeded();
  await shot('03-review');

  // 4. One run.
  const asked = asks.length;
  await setUp('Downloads');
  await made('Downloads', root, before);
  expect(asks.length === asked, 'the setup asked the model something');
  expect(errors.length === 0, `page errors: ${errors.join('; ')}`);
  console.log('OK (composer): a card before Send, one review, one run, folder unchanged');
}

async function homePhase() {
  const root = join(sandbox, 'Music');
  const before = { hash: hashFolder(root), workspaces: (await workspaces()).length };

  // 1. Explore a folder. One offer about the folder itself.
  await page.goto('/?panel=today&folder=show');
  const chooser = page.locator('#personalAssistantFolderChooser');
  await chooser.waitFor({ state: 'visible', timeout: 30000 });
  const scanned = page.waitForResponse(
    response =>
      response.request().method() === 'POST' && /\/folder-digest\/scan$/.test(response.url()),
    { timeout: 60000 }
  );
  await chooser.getByRole('button', { name: 'Music', exact: true }).click();
  expect((await scanned).ok(), 'the folder was not looked at');
  await card.waitFor({ state: 'visible', timeout: 30000 });
  await card.scrollIntoViewIfNeeded();
  const shown = {
    headline: await page.locator('#personalAssistantFolderOfferHeadline').innerText(),
    question: await page.locator('#personalAssistantFolderOfferQuestion').innerText(),
    actions: await actions()
  };
  console.log('the card:', JSON.stringify(shown, null, 1));
  console.log(`the plan:\n${(await planLines()).map(line => `  - ${line}`).join('\n')}`);
  expect(
    shown.actions.join(',') === 'Set up,Adjust…,No thanks,Later',
    `the card's actions: ${shown.actions}`
  );
  await shot('01-card');

  // 2. Adjust: the group is renamed in the card and the plan follows.
  await card.getByRole('button', { name: 'Adjust…', exact: true }).click();
  const sheet = card.locator('[data-folder-adjust]').first();
  await sheet.waitFor({ state: 'visible', timeout: 15000 });
  await shot('02-adjust');
  await sheet.locator('input[type=text], input:not([type])').first().fill('Studio');
  await sheet.getByRole('button', { name: 'Done', exact: true }).click();
  await card.getByText('Creates group Studio').waitFor({ timeout: 30000 });
  console.log(`the plan after Adjust starts: ${(await planLines())[0]}`);
  expect(
    (await workspaces()).length === before.workspaces,
    'adjusting the proposal created a workspace'
  );

  // 3. One run.
  await setUp('Studio');
  await made('Studio', root, before);
  expect(asks.length === 0, 'a model was asked');
  expect(errors.length === 0, `page errors: ${errors.join('; ')}`);
  console.log('OK (home): one offer, adjusted, one run, folder unchanged, no model asked');
}

try {
  await api('POST', '/api/onboarding/skip').catch(() => {});
  await api('POST', '/api/settings/workspace-root', { workspace_root: '' });
  let relationship = (await api('GET', '/api/personal-assistant')).personal_assistant;
  if (relationship.state === 'needs_hire') {
    await api('POST', '/api/personal-assistant/hire', {
      request_id: 'collection-demo-hire',
      if_version: relationship.state_version ?? 0,
      display_name: 'Atlas',
      mandate: 'Keep my projects moving.',
      focus_areas: ['plan_my_day']
    });
    relationship = (await api('GET', '/api/personal-assistant')).personal_assistant;
    await api('POST', '/api/personal-assistant/hq', {
      request_id: 'collection-demo-hq',
      if_version: relationship.state_version,
      name: 'My HQ',
      timezone: 'UTC'
    });
  }
  if (phase === 'composer') await composerPhase();
  else await homePhase();
} finally {
  await browser.close();
}
