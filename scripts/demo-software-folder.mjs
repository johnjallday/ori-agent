// Drive the software-project folder card in a seeded demo sandbox and capture
// its states. Phases; recognize, review and setup each start on a fresh seeded
// sandbox, and resume continues the sandbox setup left behind:
//
//   recognize  a recognized project and a choice among several
//   review     Review setup: what was read, the plan, prerequisites, adjusting
//              names, a missing model, a replaced folder, and a music project
//              whose review still lists its integration
//   setup      Set up with no model chosen: the run pauses before it creates
//              anything
//   resume     after the server was restarted on the same sandbox: the paused
//              setup is still there; a model is chosen, Continue finishes it,
//              and the receipt is what was saved
//   assess-interrupt  observes the look authorized by setup, without starting
//              it again. If still running, the server can be stopped under it;
//              a look already finished is reported, never rerun to force a demo
//   assess     after the server was restarted: a run the server lost did not
//              finish and says so; Try again (or Start assessment, when it was
//              never started) runs the same task with the real model; its
//              result is followed into the workspace and its suggestion onto
//              the Plans page, where nothing is created
//   repeat     the same folder shown again opens that workspace, and a sibling
//              worktree is a different project
//   add-folder on a fresh seeded sandbox of its own: a checkout is attached
//              through the composer's Add folder and sent to the real model;
//              the reply proposes that one folder as a workspace, one click
//              reaches its card, and its review and Set up finish as on Home
//
// Usage:
//   ./scripts/demo-server.sh 8947 "$TMPDIR/ori-software-demo"      (background)
//   ./scripts/software-folder-seed.sh "$TMPDIR/ori-software-demo" --ori
//   node scripts/demo-software-folder.mjs http://localhost:8947 "$TMPDIR/ori-software-demo" <shots-dir> [phase]
//
// For resume: run setup, stop the server (./scripts/stop-demo-server.sh 8947),
// start it again on the same sandbox, then run resume with the same shots dir.
// assess-interrupt, assess and repeat continue that sandbox in that order, with
// the server stopped and started again between assess-interrupt and assess.
// Choosing the model (resume) and both assessment phases need a signed-in model
// provider: start those servers with CODEX_HOME set to a signed-in Codex home
// (demo-server.sh is otherwise fully isolated and has no provider). The two
// resume can start a real model call; assess retries only an unfinished look.
//
// The script reads the seeded folders only to hash them: every folder it did
// not replace itself must be byte-for-byte the same at the end.
import { chromium } from 'playwright';
import { createHash } from 'node:crypto';
import {
  existsSync,
  lstatSync,
  mkdirSync,
  readFileSync,
  readdirSync,
  readlinkSync,
  renameSync,
  rmSync,
  writeFileSync
} from 'node:fs';
import { dirname, join, relative } from 'node:path';

const PHASES = [
  'recognize',
  'review',
  'setup',
  'resume',
  'assess-interrupt',
  'assess',
  'repeat',
  'add-folder'
];
// The phases that continue the sandbox an earlier phase left behind.
const CONTINUES = ['resume', 'assess-interrupt', 'assess', 'repeat'];
const [base, sandbox, output, phase = 'review'] = process.argv.slice(2);
if (!base || !sandbox || !output || !PHASES.includes(phase))
  throw new Error(
    `usage: demo-software-folder.mjs <base-url> <sandbox-dir> <shots-dir> [${PHASES.join('|')}]`
  );
mkdirSync(output, { recursive: true });
// What setup saw before it changed anything, for resume to compare against.
const carried = join(output, 'setup-state.json');

const oriCheckout = join(sandbox, 'Documents', 'ori-agent');
const isOri = existsSync(oriCheckout);
const documentsProject = isOri ? oriCheckout : join(sandbox, 'Documents', 'widget');
const projectName = isOri ? 'ori-agent' : 'widget';
const folders = {
  documents: documentsProject,
  api: join(sandbox, 'Desktop', 'api'),
  site: join(sandbox, 'Desktop', 'site'),
  scratchpad: join(sandbox, 'Downloads', 'scratchpad')
};
for (const [name, dir] of Object.entries(folders))
  if (!existsSync(dir))
    throw new Error(`${name} is not seeded at ${dir}; run software-folder-seed.sh`);

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
      } else
        lines.push(`f ${rel} ${createHash('sha256').update(readFileSync(full)).digest('hex')}`);
    }
  };
  walk(root);
  return createHash('sha256').update(lines.join('\n')).digest('hex');
}
const hashes = (names = Object.keys(folders)) =>
  Object.fromEntries(names.map(name => [name, hashFolder(folders[name])]));

const browser = await chromium.launch();
const context = await browser.newContext({
  viewport: { width: 1280, height: 900 },
  baseURL: base
});
const page = await context.newPage();
const errors = [];
page.on('pageerror', error => errors.push(error.message));

async function shot(name) {
  const path = join(output, `${name}.png`);
  await page.screenshot({ path });
  console.log(path);
}
// phoneShot captures the same state at phone width. What is shown must wrap
// inside its own box instead of scrolling sideways.
async function phoneShot(name, locator) {
  await page.setViewportSize({ width: 390, height: 844 });
  await locator.scrollIntoViewIfNeeded();
  const overflow = await locator.evaluate(el => el.scrollWidth - el.clientWidth);
  if (overflow > 1) throw new Error(`${name} scrolls sideways at phone width (${overflow}px)`);
  await shot(`${name}-phone`);
  await page.setViewportSize({ width: 1280, height: 900 });
  await locator.scrollIntoViewIfNeeded();
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
  if (!Array.isArray(items))
    throw new Error(`unexpected workspace list: ${JSON.stringify(list).slice(0, 200)}`);
  return items.length;
}
const currentOffer = async () =>
  (await api('GET', '/api/personal-assistant/folder-digest')).folder_digest?.offer || null;

const card = page.locator('#personalAssistantFolderOffer');
// show opens the chooser the way Home's "Explore a folder" does and picks a chip.
async function show(chip) {
  await page.goto('/?panel=today&folder=show');
  const chooser = page.locator('#personalAssistantFolderChooser');
  await chooser.waitFor({ state: 'visible', timeout: 30000 });
  // The card of an earlier folder may still be on screen; what this look found
  // is on it only once the scan has answered.
  const scanned = page.waitForResponse(
    response =>
      response.request().method() === 'POST' && /\/folder-digest\/scan$/.test(response.url()),
    { timeout: 60000 }
  );
  await chooser.getByRole('button', { name: chip, exact: true }).click();
  await scanned;
  await card.waitFor({ state: 'visible', timeout: 30000 });
  await page.locator('#personalAssistantFolderOfferHeadline').waitFor({ state: 'visible' });
  await card.scrollIntoViewIfNeeded();
}
// comeBack reloads Home the way a person returning later sees it: the card is
// still there, inside "Needs you", which starts collapsed.
async function comeBack() {
  await page.goto('/?panel=today');
  const attention = page.locator('#personalAssistantSummaryToggle');
  await attention.waitFor({ state: 'visible', timeout: 30000 });
  if ((await attention.getAttribute('aria-expanded')) === 'false') await attention.click();
  await card.waitFor({ state: 'visible', timeout: 30000 });
  await page.locator('#personalAssistantFolderOfferHeadline').waitFor({ state: 'visible' });
  await card.scrollIntoViewIfNeeded();
  await fullDetails();
}
async function fullDetails() {
  const details = card.locator('#personalAssistantFolderDetails');
  if ((await details.getAttribute('open')) === null)
    await details.locator('summary').first().click();
}
const text = id => page.locator(id).innerText();
const list = id => page.locator(`${id} li`).allInnerTexts();
const buttons = () =>
  card.locator('#personalAssistantFolderOfferActions').locator('button, a').allInnerTexts();
const press = name => card.getByRole('button', { name, exact: true }).click();
const isSetupAction = name => /^(Set up|Hire & start|Assign & start)$/.test(name);
async function confirmSetup() {
  const label = (await buttons())[0];
  expect(isSetupAction(label), `no reviewed setup action: ${label}`);
  await press(label);
}
const focused = () => page.evaluate(() => document.activeElement?.dataset?.folderAction || '');
function expect(condition, message) {
  if (!condition) throw new Error(message);
}
// review presses Review setup (or Review again) and waits for what was read.
async function review(label = 'Review setup') {
  await fullDetails();
  await press(label);
  await page.locator('#personalAssistantFolderEvidence li').first().waitFor({ timeout: 30000 });
  await page.waitForFunction(
    () => !document.querySelector('#personalAssistantFolderOfferActions button[disabled]')
  );
}

async function recognize() {
  // 1. One recognized project: the Ori checkout (or the widget fixture).
  await show('Documents');
  const recognized = {
    headline: await text('#personalAssistantFolderOfferHeadline'),
    question: await text('#personalAssistantFolderOfferQuestion'),
    note: await text('#personalAssistantFolderOfferCapability'),
    buttons: await buttons()
  };
  console.log('recognized:', JSON.stringify(recognized));
  expect(/looks like a/.test(recognized.headline), `not recognized: ${recognized.headline}`);
  expect(/Nothing in it was opened\./.test(recognized.note), `no note: ${recognized.note}`);
  expect(/Review setup reads its README/.test(recognized.question), recognized.question);
  expect(recognized.buttons[0] === 'Review setup', `first action: ${recognized.buttons}`);
  expect(
    !recognized.buttons.includes('Set up'),
    `a bare Set up was offered: ${recognized.buttons}`
  );
  if (isOri)
    expect(/linked Git checkout/.test(recognized.note), `not a worktree: ${recognized.note}`);
  await shot('01-recognized-project');

  // 2. Two projects side by side: a choice, and no review or setup until it is made.
  await show('Desktop');
  const choice = {
    headline: await text('#personalAssistantFolderOfferHeadline'),
    question: await text('#personalAssistantFolderOfferQuestion'),
    buttons: await buttons()
  };
  console.log('choice:', JSON.stringify(choice));
  // With the Ori seed, a sibling worktree of the Documents checkout sits here too.
  expect(
    new RegExp(`holds ${isOri ? 3 : 2} software projects`).test(choice.headline),
    `no choice: ${choice.headline}`
  );
  expect(
    !choice.buttons.some(label => /^(Set up|Review setup)/.test(label)),
    `an action was offered before a choice: ${choice.buttons}`
  );
  await shot('02-choose-which-project');
  await card.getByRole('button', { name: /^site/ }).click();
  await page.waitForFunction(() =>
    /site looks like/.test(
      document.getElementById('personalAssistantFolderOfferHeadline')?.textContent || ''
    )
  );
  console.log('chosen:', await text('#personalAssistantFolderOfferHeadline'), await buttons());
  await shot('03-chosen-project');
}

async function reviewPhase() {
  // 3. Review setup on the recognized project: what was read, the plan, and
  //    what the setup needs. Nothing is created.
  await show('Documents');
  await review();
  const offer = await currentOffer();
  const reviewed = {
    headline: await text('#personalAssistantFolderOfferHeadline'),
    question: await text('#personalAssistantFolderOfferQuestion'),
    evidence: await list('#personalAssistantFolderEvidence'),
    plan: await list('#personalAssistantFolderPlan'),
    kinds: await page
      .locator('#personalAssistantFolderPlan li')
      .evaluateAll(items => items.map(item => item.dataset.kind)),
    prerequisites: await list('#personalAssistantFolderPrerequisites'),
    buttons: await buttons()
  };
  console.log('reviewed:', JSON.stringify(reviewed, null, 1));
  expect(reviewed.headline === `Here is what I found in ${projectName}.`, reviewed.headline);
  expect(
    reviewed.kinds.join(',') === 'workspace,folder,agents,mode,task',
    `plan: ${reviewed.kinds}`
  );
  expect(
    reviewed.evidence.some(line => /^Built with .*Go/.test(line)),
    `no stack: ${reviewed.evidence}`
  );
  expect(
    reviewed.evidence.some(line => /^It declares .*None of these was run\./.test(line)),
    `no declared commands: ${reviewed.evidence}`
  );
  if (isOri) {
    expect(
      reviewed.evidence.some(line => /^Its instructions .*AGENTS\.md/.test(line)),
      `Ori's own instructions were not found: ${reviewed.evidence}`
    );
    expect(
      reviewed.evidence.some(line =>
        /A linked Git worktree .*github\.com\/johnjallday\/ori-agent\.git/.test(line)
      ),
      `Ori's worktree and remote were not read: ${reviewed.evidence}`
    );
  }
  expect(
    reviewed.prerequisites.some(line => /^Folder access · Ready/.test(line)),
    `folder prerequisite: ${reviewed.prerequisites}`
  );
  expect(
    reviewed.prerequisites.some(line => /^GitHub · Not set \(optional\)/.test(line)),
    `GitHub prerequisite: ${reviewed.prerequisites}`
  );
  const available = offer?.software?.setup_available === true;
  expect(
    reviewed.buttons.some(isSetupAction) === available,
    `Setup shown=${reviewed.buttons.some(isSetupAction)} but setup_available=${available}`
  );
  await shot('10-reviewed-setup');

  // 4. A missing model is said in words, with the way to fix it.
  const modelRow = reviewed.prerequisites.find(line => /^Assistant's model/.test(line)) || '';
  console.log('model prerequisite:', modelRow);
  if (/· Needed/.test(modelRow)) {
    const link = page.locator('#personalAssistantFolderPrerequisites a', {
      hasText: 'Set up a model'
    });
    expect((await link.getAttribute('href')) === '/settings#system-model', 'no model link');
    expect(
      /has no model yet/.test(reviewed.plan.join(' ')),
      `plan hides the gap: ${reviewed.plan}`
    );
    await page.locator('#personalAssistantFolderPrerequisites').scrollIntoViewIfNeeded();
    await shot('11-missing-model');
    // Recover: configure a model the way Settings does, then come back.
    const configured = await context.request.post('/api/settings/system-model', {
      data: { provider: 'codex', model: 'gpt-5.6-luna' }
    });
    if (configured.ok()) {
      await comeBack();
      await page.locator('#personalAssistantFolderPrerequisites li').first().waitFor();
      const after = await list('#personalAssistantFolderPrerequisites');
      console.log('after a model was set:', JSON.stringify(after));
      expect(
        after.some(line => /^Assistant's model · Set, not checked/.test(line)),
        `model not picked up: ${after}`
      );
      await card.scrollIntoViewIfNeeded();
      await shot('12-model-configured');
    } else {
      console.log(
        `model recovery NOT SHOWN: no model provider in this sandbox (${configured.status()})`
      );
    }
  } else {
    console.log('missing-model state NOT SHOWN: a model was already configured');
  }

  // 5. Adjust names: the plan follows, focus returns, and a refused name keeps
  //    what was typed.
  const before = await list('#personalAssistantFolderPlan');
  await press('Adjust names…');
  const nameField = page.locator('#personalAssistantFolderWorkspaceName');
  const assistantField = page.locator('#personalAssistantFolderAssistantName');
  await nameField.waitFor({ state: 'visible' });
  expect(
    await nameField.evaluate(el => el === document.activeElement),
    'focus did not move to the name'
  );
  expect(
    (await nameField.inputValue()) === projectName,
    `name field: ${await nameField.inputValue()}`
  );
  await nameField.fill('bad/name');
  await press('Save names');
  await page.locator('#personalAssistantFolderOfferError').waitFor({ state: 'visible' });
  expect((await nameField.inputValue()) === 'bad/name', 'the refused name was thrown away');
  console.log('refused name:', await text('#personalAssistantFolderOfferError'));
  await nameField.fill('Project notebook');
  await assistantField.fill('Dev Pat');
  await shot('13-adjust-names');
  await press('Save names');
  await nameField.waitFor({ state: 'hidden' });
  const renamed = await list('#personalAssistantFolderPlan');
  console.log('renamed plan:', JSON.stringify(renamed.slice(0, 3)));
  expect(/named Project notebook/.test(renamed[0]), `workspace name: ${renamed[0]}`);
  expect(/Dev Pat/.test(renamed[2]), `assistant name: ${renamed[2]}`);
  expect(renamed[1] === before[1], 'the linked folder changed with the names');
  expect((await focused()) === 'adjust', `focus after saving: ${await focused()}`);
  await press('Adjust names…');
  await press('Cancel');
  expect((await focused()) === 'adjust', `focus after cancel: ${await focused()}`);
  await shot('14-names-saved');

  // 6. Phone width: the review wraps instead of scrolling sideways.
  await page.setViewportSize({ width: 390, height: 844 });
  await card.scrollIntoViewIfNeeded();
  const overflow = await card.evaluate(el => el.scrollWidth - el.clientWidth);
  expect(overflow <= 1, `the review scrolls sideways at phone width (${overflow}px)`);
  await shot('15-review-phone');
  await page.setViewportSize({ width: 1280, height: 900 });

  // 7. A folder replaced after its review is not the folder that was reviewed.
  await show('Downloads');
  await review();
  expect(
    (await list('#personalAssistantFolderPlan')).length === 5,
    'no plan for the small project'
  );
  // The old folder is moved away and deleted only once the new one exists: a
  // filesystem may give a new directory the inode number of one just deleted,
  // and the replacement would then pass for the original.
  const aside = join(sandbox, '.replaced-scratchpad');
  rmSync(aside, { recursive: true, force: true });
  renameSync(folders.scratchpad, aside);
  mkdirSync(folders.scratchpad, { mode: 0o750 });
  writeFileSync(join(folders.scratchpad, 'package.json'), '{ "name": "scratchpad" }\n', {
    mode: 0o600
  });
  rmSync(aside, { recursive: true });
  await comeBack();
  const stale = {
    question: await text('#personalAssistantFolderOfferQuestion'),
    plan: await list('#personalAssistantFolderPlan'),
    buttons: await buttons()
  };
  console.log('replaced folder:', JSON.stringify(stale));
  expect(
    /not the one you showed me/.test(stale.question),
    `no stale explanation: ${stale.question}`
  );
  expect(stale.plan.length === 0, 'a replaced folder kept its plan');
  expect(stale.buttons[0] === 'Pick it again', `stale actions: ${stale.buttons}`);
  await card.scrollIntoViewIfNeeded();
  await shot('16-replaced-folder');
  await press('Pick it again');
  const chooser = page.locator('#personalAssistantFolderChooser');
  await chooser.waitFor({ state: 'visible' });
  await chooser.getByRole('button', { name: 'Downloads', exact: true }).click();
  await page.waitForFunction(() =>
    /Review setup reads its README/.test(
      document.getElementById('personalAssistantFolderOfferQuestion')?.textContent || ''
    )
  );
  await review();
  expect((await list('#personalAssistantFolderPlan')).length === 5, 'no plan after picking again');
  await shot('17-picked-again');

  // 8. A music project's review is untouched: it still lists its integration.
  rmSync(folders.scratchpad, { recursive: true });
  mkdirSync(join(sandbox, 'Downloads', 'Song'), { mode: 0o750 });
  writeFileSync(join(sandbox, 'Downloads', 'Song', 'Song.rpp'), '<REAPER_PROJECT 0.1 "7.0"\n>\n', {
    mode: 0o600
  });
  await show('Downloads');
  const music = await currentOffer();
  const musicKinds = (music?.plan?.lines || []).map(line => line.kind);
  if (musicKinds.length) {
    expect(
      musicKinds[0] === 'integration',
      `a music plan lost its integration line: ${musicKinds}`
    );
    expect(!music.software, 'a music project was described as software');
    console.log('music plan:', JSON.stringify(music.plan.lines.map(line => line.name)));
    await page.locator('#personalAssistantFolderPlan li').first().waitFor();
    await shot('18-music-review-keeps-integration');
  } else {
    console.log(
      `music plan NOT SHOWN: ${music?.setup_unavailable_reason || 'no plan'} (the reviewed integration could not be read in this sandbox); software=${Boolean(music?.software)}`
    );
    expect(!music?.software, 'a music project was described as software');
  }
}

// One surface for saved progress and the canonical receipt; no run modal.
async function setupCardState() {
  return {
    title: await text('#personalAssistantFolderOfferHeadline'),
    status: await text('#personalAssistantFolderOfferQuestion'),
    steps: await card.locator('[aria-label="Saved setup progress"] li').allInnerTexts(),
    receipt: await list('#personalAssistantFolderReceipt'),
    buttons: await buttons()
  };
}
async function developmentAssistants() {
  const listed = await api('GET', '/api/agents');
  const agents = Array.isArray(listed) ? listed : listed.agents || listed.data || [];
  return agents.filter(
    agent => (typeof agent === 'string' ? agent : agent?.name) === 'Development Assistant'
  ).length;
}

async function setupPhase(before) {
  // 9. Set up with no model chosen. The run pauses before it creates anything
  //    and says what it needs.
  await show('Documents');
  await review();
  const reviewed = await list('#personalAssistantFolderPlan');
  expect(reviewed.length === 5, `no plan to confirm: ${reviewed}`);
  expect(/has no model yet/.test(reviewed[2]), `the plan hides the missing model: ${reviewed[2]}`);
  await confirmSetup();
  await page.waitForFunction(
    () =>
      /has no model yet/.test(
        document.getElementById('personalAssistantFolderOfferQuestion')?.textContent || ''
      ),
    null,
    { timeout: 30000 }
  );
  const paused = await setupCardState();
  console.log('paused setup:', JSON.stringify(paused, null, 1));
  expect(
    paused.steps.length > 0 && paused.steps.every(line => /Waiting|Did not finish/.test(line)),
    `unexpected progress: ${paused.steps}`
  );
  expect(/has no model yet/.test(paused.status), `no reason given: ${paused.status}`);
  expect(
    paused.buttons.join(',') === 'Set up a model,Continue',
    `paused actions: ${paused.buttons}`
  );
  expect((await workspaceCount()) === before.workspaces, 'a paused setup created a workspace');
  expect((await developmentAssistants()) === 0, 'a paused setup created the assistant');
  await shot('20-setup-paused-for-a-model');
  writeFileSync(
    carried,
    JSON.stringify({
      hashes: before.hashes,
      workspaces: before.workspaces,
      offer: (await currentOffer())?.id
    }),
    { mode: 0o600 }
  );
}

async function resumePhase(untouched) {
  expect(existsSync(carried), `run the setup phase first: ${carried} is missing`);
  const before = JSON.parse(readFileSync(carried, 'utf8'));

  // 10. The server was restarted. The paused setup is still on Home.
  await comeBack();
  const afterRestart = {
    headline: await text('#personalAssistantFolderOfferHeadline'),
    question: await text('#personalAssistantFolderOfferQuestion'),
    buttons: await buttons()
  };
  console.log('after a restart:', JSON.stringify(afterRestart));
  expect((await currentOffer())?.id === before.offer, 'the paused offer was replaced');
  expect(
    /has no model yet/.test(afterRestart.question),
    `the pause was lost: ${afterRestart.question}`
  );
  expect(
    afterRestart.buttons.join(',') === 'Set up a model,Continue',
    `actions after a restart: ${afterRestart.buttons}`
  );
  expect((await workspaceCount()) === before.workspaces, 'a restart created a workspace');
  await shot('21-paused-setup-after-restart');

  // 11. Choose a model the way Settings does, then Continue.
  const configured = await context.request.post('/api/settings/system-model', {
    data: { provider: 'codex', model: 'gpt-5.6-luna' }
  });
  expect(configured.ok(), `no model provider in this sandbox (${configured.status()})`);
  await press('Continue');
  await page.waitForFunction(
    () =>
      /is ready/.test(
        document.getElementById('personalAssistantFolderOfferHeadline')?.textContent || ''
      ) ||
      [...document.querySelectorAll('#personalAssistantFolderOfferActions button')].some(
        button => button.textContent === 'Pick it again'
      ),
    null,
    { timeout: 60000 }
  );
  if (await card.getByRole('button', { name: 'Pick it again', exact: true }).isVisible()) {
    await press('Pick it again');
    const scanned = page.waitForResponse(
      response =>
        response.request().method() === 'POST' && /\/folder-digest\/scan$/.test(response.url())
    );
    await page
      .locator('#personalAssistantFolderChooser')
      .getByRole('button', { name: 'Documents', exact: true })
      .click();
    await scanned;
    expect((await currentOffer())?.id === before.offer, 're-picking replaced the saved setup');
    const reviewLabel = (await buttons()).find(label =>
      /^(Review setup|Review again)$/.test(label)
    );
    if (reviewLabel) await review(reviewLabel);
    if ((await buttons()).includes('Continue')) await press('Continue');
    else await confirmSetup();
  }
  await page.waitForFunction(
    () =>
      /is ready/.test(
        document.getElementById('personalAssistantFolderOfferHeadline')?.textContent || ''
      ),
    null,
    { timeout: 60000 }
  );
  const done = await setupCardState();
  console.log('finished setup:', JSON.stringify(done, null, 1));
  // A finished setup ends on its receipt, read back from what was saved; the
  // run's own step list is gone with the run.
  expect(/is ready/.test(done.title), `setup did not resolve: ${done.title}`);
  const receipt = done.receipt.join(' | ').replace(/\n/g, ' ');
  for (const wanted of [
    new RegExp(`${projectName} \\(created\\)`),
    /linked where it is, read-only/,
    /Code Project \(planning first; approved work waits for you/,
    /Development Assistant \(development assistant, added\)/,
    // Task state is separate from setup; do not guess whether the look finished.
    /Assess this project and suggest a first task/
  ])
    expect(wanted.test(receipt), `receipt lacks ${wanted}: ${receipt}`);
  expect(!/Ready to start/.test(receipt), `the receipt promises a start: ${receipt}`);
  expect(
    (await workspaceCount()) === before.workspaces + 1,
    'the setup did not make one workspace'
  );
  expect((await developmentAssistants()) === 1, 'the setup did not make exactly one assistant');
  await shot('22-setup-receipt');

  // 12. The same card, with the separately recorded authorized first look.
  expect(
    (await page.locator('#folderSetupRunModal').count()) === 0,
    'a duplicate run modal remains'
  );
  await card.scrollIntoViewIfNeeded();
  const resolved = await currentOffer();
  console.log('first assessment:', JSON.stringify(resolved?.first_task));
  expect(resolved?.status === 'resolved', `the offer is ${resolved?.status}`);
  expect(
    Boolean(resolved?.outcome?.first_look && resolved?.first_task?.task_id),
    'no canonical first look was recorded'
  );
  await shot('23-card-receipt');

  writeFileSync(
    carried,
    JSON.stringify({
      ...before,
      resolved: resolved.id,
      workspace: resolved.first_task.workspace_id,
      task: resolved.first_task.task_id
    }),
    { mode: 0o600 }
  );
  await settled(before, untouched);
  console.log('OK (resume): one workspace, one assistant, folders unchanged, no page errors');
}

// settled is what every phase after setup leaves true: one workspace and one
// assistant more than before, and every seeded folder byte-for-byte the same.
async function settled(before, untouched) {
  expect(
    (await workspaceCount()) === before.workspaces + 1,
    'the workspaces changed after the setup'
  );
  expect((await developmentAssistants()) === 1, 'there is not exactly one assistant');
  expect(
    JSON.stringify(hashes(untouched)) === JSON.stringify(before.hashes),
    'a seeded folder changed'
  );
  expect(errors.length === 0, `page errors: ${errors.join('; ')}`);
}

// The set-up offer's first task, read the way its card reads it.
async function firstTask(offerID) {
  const digest = await api(
    'GET',
    `/api/personal-assistant/folder-digest?offer_id=${encodeURIComponent(offerID)}`
  );
  return digest.folder_digest?.offer?.first_task || null;
}
// The workspace's tasks: an assessment is one task however often it is run.
async function taskIDs(workspaceID) {
  const listed = await api(
    'GET',
    `/api/orchestration/tasks?workspace_id=${encodeURIComponent(workspaceID)}`
  );
  const tasks = Array.isArray(listed) ? listed : listed.tasks || listed.data || [];
  return tasks.map(task => task.id).sort();
}
// What the receipt card says about the assessment right now.
async function assessmentCard() {
  return {
    receipt: (await list('#personalAssistantFolderReceipt')).map(line => line.replace(/\n/g, ' ')),
    readiness: (await list('#personalAssistantFolderPrerequisites')).map(line =>
      line.replace(/\n/g, ' ')
    ),
    note: await text('#personalAssistantFolderOfferNote'),
    result: await text('#personalAssistantFolderFirstLookResult'),
    buttons: await buttons()
  };
}
// Waits until the card's first task is in one of the given states.
async function waitForLook(offerID, states, timeout) {
  const deadline = Date.now() + timeout;
  for (;;) {
    const look = await firstTask(offerID);
    if (look && states.includes(look.state)) return look;
    if (Date.now() > deadline)
      throw new Error(`the assessment is ${look?.state} after ${timeout / 1000}s, not ${states}`);
    await page.waitForTimeout(1500);
  }
}
// Home refreshes what its assistant drawer shows with a POST that only reads
// (no model turn, nothing saved). Every other request that is not a GET could
// start a run or change something, and is recorded.
const DISPLAY_READS = new Set(['/api/home-assistant/context']);
function watchWrites() {
  const writes = [];
  page.on('request', request => {
    const path = new URL(request.url()).pathname;
    if (request.method() !== 'GET' && !DISPLAY_READS.has(path))
      writes.push(`${request.method()} ${path}`);
  });
  return writes;
}

async function assessInterruptPhase(untouched) {
  expect(existsSync(carried), `run setup and resume first: ${carried} is missing`);
  const before = JSON.parse(readFileSync(carried, 'utf8'));
  expect(before.resolved && before.task, 'run the resume phase first');
  const tasksBefore = await taskIDs(before.workspace);

  // The hire's consent already authorized this task. Reopening is read-only.
  const writes = watchWrites();
  await comeBack();
  const look = await waitForLook(before.resolved, ['running', 'finished', 'failed'], 30000);
  expect(look.task_id === before.task, 'the look was replaced');
  expect(writes.length === 0, `reopening the receipt sent: ${writes}`);
  await shot('30-assessment-observed');
  await phoneShot('30-assessment-observed', card);
  if (look.state !== 'running') {
    console.log(
      `NOT RUN (in-flight interruption): the first look is already ${look.state}; it will not be rerun just for this demo.`
    );
    await settled(before, untouched);
    return;
  }
  await page.waitForFunction(
    () =>
      ![...document.querySelectorAll('#personalAssistantFolderOfferActions button')].some(button =>
        /Start assessment|Starting/.test(button.textContent)
      ),
    null,
    { timeout: 30000 }
  );
  const running = await assessmentCard();
  console.log('assessment running:', JSON.stringify(running));
  expect(/Nothing is being run/.test(running.note), `running note: ${running.note}`);
  expect(
    !running.buttons.some(name => /Start assessment|Try again/.test(name)),
    `a second start is offered while it runs: ${running.buttons}`
  );
  expect(look.task_id === before.task, 'the run is another task');
  const sent = writes.filter(write => /folder-first-task\/start$|tasks\/execute$/.test(write));
  expect(sent.length === 0, `reopening started the look again: ${sent}`);
  expect(
    JSON.stringify(await taskIDs(before.workspace)) === JSON.stringify(tasksBefore),
    'starting the assessment made another task'
  );
  await card.scrollIntoViewIfNeeded();
  await shot('31-assessment-running');
  await settled(before, untouched);
  console.log(
    'Observed the existing first look running, one task, no repeated start. Stop the server now; confirm interruption from its saved state after restart.'
  );
}

async function assessPhase(untouched) {
  expect(existsSync(carried), `run setup and resume first: ${carried} is missing`);
  const before = JSON.parse(readFileSync(carried, 'utf8'));
  expect(before.resolved && before.task, 'run the resume phase first');
  const tasksBefore = await taskIDs(before.workspace);

  // 17. After a restart the assessment is where it was left: a run the server
  //     lost did not finish, and says so in a sentence. Coming back runs
  //     nothing, and nothing polls it into running again.
  const writes = watchWrites();
  await comeBack();
  let waiting = await firstTask(before.resolved);
  if (waiting?.state === 'running')
    waiting = await waitForLook(before.resolved, ['finished', 'failed'], 180000);
  const left = await assessmentCard();
  console.log('assessment after a restart:', JSON.stringify({ ...left, look: waiting }, null, 1));
  // The model the assistant is saved with, as the server reads it.
  console.log(
    'model:',
    (waiting.readiness || []).find(row => row.id === 'model')?.detail || 'not reported'
  );
  expect(waiting.task_id === before.task, 'the assessment is another task after a restart');
  const retry = waiting.state === 'failed';
  // A finished assessment is read, never run again by this script.
  const ran = waiting.state !== 'finished';
  expect(
    retry || waiting.state === 'seeded' || !ran,
    `the assessment is ${waiting.state}; this phase needs one that is waiting, did not finish, or is finished`
  );
  if (ran) await runAssessment(before, { writes, waiting, left, retry, tasksBefore });
  else console.log('the assessment is already finished: reading it, not running it again');
  const done = await firstTask(before.resolved);
  expect(done.state === 'finished', `the assessment did not finish: ${done.message}`);
  expect(done.task_id === before.task, 'the finished run is another task');
  const sent = writes.filter(write => /folder-first-task\/start$|tasks\/execute$/.test(write));
  expect(sent.length === (ran ? 1 : 0), `runs asked for: ${sent}`);

  await readAssessment(before, { writes, done, tasksBefore });
  await settled(before, untouched);
  const how = !ran
    ? 'no run (it was already finished)'
    : `one run of one task (${retry ? 'a retry of the run that did not finish' : 'its first start'}), a real model call`;
  console.log(`OK (assess): ${how}, nothing created by the hand-off, folders unchanged`);
}

// runAssessment presses the one button the card offers and follows the run to
// its end. Initial setup may already have authorized a look; never start it twice.
async function runAssessment(before, { writes, waiting, left, retry, tasksBefore }) {
  if (retry) {
    expect(waiting.can_retry === true, `it cannot be tried again: ${waiting.reason}`);
    expect(Boolean(waiting.message), 'the run that did not finish has no sentence');
    expect(
      !/codex|exit status|stderr|\/Users\/|\/private\//i.test(waiting.message),
      `the sentence shows a raw error: ${waiting.message}`
    );
    expect(
      left.receipt.some(line => /Did not finish/.test(line)),
      `the receipt's task row: ${left.receipt}`
    );
  }
  const label = retry ? 'Try again' : 'Start assessment';
  expect(left.buttons[0] === label, `first action: ${left.buttons}`);
  await page.waitForTimeout(6000);
  expect(writes.length === 0, `coming back sent: ${writes}`);
  expect(
    (await firstTask(before.resolved)).state === waiting.state,
    'the assessment moved without a click'
  );
  await shot(retry ? '32-assessment-did-not-finish' : '32-assessment-after-restart');

  // 18. One click runs it: the same task, with the real model. While it runs
  //     the card says so and offers nothing to press twice.
  await press(label);
  // A run that is tried again reads "failed" until the new run is picked up,
  // so "failed" here would be the old run, not this one's outcome.
  await waitForLook(before.resolved, ['running', 'finished'], 60000);
  const running = await assessmentCard();
  console.log('assessment running:', JSON.stringify(running));
  if ((await firstTask(before.resolved)).state === 'running') {
    expect(/Nothing is being run/.test(running.note), `running note: ${running.note}`);
    expect(
      !running.buttons.some(name => /Start assessment|Try again/.test(name)),
      `a second start is offered while it runs: ${running.buttons}`
    );
    await shot('33-assessment-running');
  }
  const ended = await waitForLook(before.resolved, ['finished', 'failed'], 420000);
  expect(ended.state === 'finished', `the assessment did not finish: ${ended.message}`);
  console.log(`run: started ${ended.started_at}, finished ${ended.finished_at}`);
  expect(
    JSON.stringify(await taskIDs(before.workspace)) === JSON.stringify(tasksBefore),
    'running the assessment made another task'
  );
}

// readAssessment follows a finished assessment from Home to its report, to the
// workspace's banner, and to the Plans page. It only reads.
async function readAssessment(before, { writes, done, tasksBefore }) {
  // 19. The result on Home: one line of it, the suggestion, and the two links.
  await page.waitForFunction(
    () =>
      [...document.querySelectorAll('#personalAssistantFolderOfferActions a')].some(
        link => link.textContent.trim() === 'Open the assessment'
      ),
    null,
    { timeout: 30000 }
  );
  const finished = await assessmentCard();
  console.log('assessment finished:', JSON.stringify({ ...finished, look: done }, null, 1));
  const task = await api('GET', `/api/orchestration/tasks?id=${encodeURIComponent(before.task)}`);
  console.log(`--- the assessment (${task.result.length} characters) ---\n${task.result}\n---`);
  expect(Boolean(done.next_request), 'the assessment suggested no next task');
  expect(
    finished.buttons.join(',') ===
      `Open the assessment,Plan the suggested task,Open ${projectName}`,
    `actions on a finished assessment: ${finished.buttons}`
  );
  expect(
    finished.result.includes(done.next_request),
    `the card does not show the suggestion: ${finished.result}`
  );
  expect(/^Next task: /m.test(task.result), 'the assessment has no Next task line');
  expect(
    /What this is based on/.test(task.result),
    'the assessment does not say what it was based on'
  );
  expect(
    !/\b(tests? (pass|passed|are passing)|CI is green|build succeeds)\b/i.test(task.result),
    'the assessment claims something was run'
  );
  if (isOri)
    expect(
      /worktree/i.test(task.result),
      "Ori's own worktree rules were not recognized from its instructions"
    );
  await card.scrollIntoViewIfNeeded();
  await shot('34-assessment-finished');
  await phoneShot('34-assessment-finished', card);

  // 20. Open the assessment: the task's own page, where the whole report can
  //     be read. (A ticket's detail lists the work and its runs, not this.)
  const followWrites = writes.length;
  await card.getByRole('link', { name: 'Open the assessment', exact: true }).click();
  await page.waitForURL(url => /\/workspaces\/[^/]+\/task\/[^/]+$/.test(url.pathname), {
    timeout: 30000
  });
  const report = page.getByText('What this is based on', { exact: false }).first();
  await report.waitFor({ state: 'visible', timeout: 30000 });
  await closeAssistantDrawer();
  const reportPage = {
    url: new URL(page.url()).pathname,
    shows: {
      purpose: await page.getByText('What it is', { exact: true }).first().isVisible(),
      declared: await page
        .getByText(/Build and test \(declared, not run\)/)
        .first()
        .isVisible(),
      next: await page
        .getByText(/^Next task: /)
        .first()
        .isVisible()
    }
  };
  console.log('report page:', JSON.stringify(reportPage));
  expect(
    reportPage.url === `/workspaces/${projectName}/task/${before.task}`,
    `the report opened ${reportPage.url}`
  );
  expect(
    reportPage.shows.purpose && reportPage.shows.declared && reportPage.shows.next,
    `the report page does not show the assessment: ${JSON.stringify(reportPage.shows)}`
  );
  await shot('35-assessment-report');
  await report.scrollIntoViewIfNeeded();
  await shot('36-assessment-report-basis');

  // 21. The workspace itself: its banner hands on the same suggestion.
  await page.goto(`/workspaces/${projectName}`);
  const banner = page.locator('#workspaceFirstTaskBanner');
  await banner.waitFor({ state: 'visible', timeout: 30000 });
  await page.waitForFunction(
    () => document.getElementById('workspaceFirstTaskBanner')?.dataset.phase === 'done',
    null,
    { timeout: 30000 }
  );
  await closeAssistantDrawer();
  const bannerSays = {
    text: (await banner.locator('.first-task-banner__text').innerText()).replace(/\n/g, ' '),
    controls: await banner.locator('button, a').allInnerTexts(),
    report: await banner
      .getByRole('link', { name: 'Open the assessment', exact: true })
      .getAttribute('href')
  };
  console.log('workspace banner:', JSON.stringify(bannerSays));
  expect(
    bannerSays.text.includes(done.next_request),
    `the banner's suggestion differs from the card's: ${bannerSays.text}`
  );
  expect(
    bannerSays.controls.join(',') === 'Open the assessment,Plan the suggested task,Dismiss',
    `banner controls: ${bannerSays.controls}`
  );
  expect(bannerSays.report === reportPage.url, `the banner's report link: ${bannerSays.report}`);
  await shot('37-workspace-banner');
  await phoneShot('37-workspace-banner', banner);

  // 22. Plan the suggested task: the Plans page with the suggestion in its box.
  //     Nothing is created by arriving, and nothing is written into the project.
  await banner.getByRole('link', { name: 'Plan the suggested task', exact: true }).click();
  const box = page.locator('#plan-create-request');
  await box.waitFor({ state: 'visible', timeout: 30000 });
  await page.waitForFunction(
    () => (document.getElementById('plan-create-request')?.value || '').length > 0,
    null,
    { timeout: 30000 }
  );
  const planning = {
    url: new URL(page.url()).pathname + new URL(page.url()).search,
    box: await box.inputValue(),
    status: await text('#plan-status')
  };
  console.log('plans page:', JSON.stringify(planning));
  expect(planning.box.startsWith(done.next_request), `the box: ${planning.box}`);
  expect(
    /Suggested by the first assessment of/.test(planning.box),
    `the box does not say where the request came from: ${planning.box}`
  );
  expect(
    /Nothing is created until you do/.test(planning.status),
    `plans status: ${planning.status}`
  );
  expect(!/next=assessment/.test(planning.url), `the address still asks for it: ${planning.url}`);
  const plans = await api('GET', `/api/workspaces/${encodeURIComponent(before.workspace)}/plans`);
  expect((plans.plans || []).length === 0, 'arriving on the Plans page created a plan');
  // Opening a workspace page makes its own open-time requests (activating the
  // workspace on this computer, and asking whether a blueprint setup task is
  // waiting). None of them may run a task, send a message, or create a plan or
  // a ticket; the unchanged task list below is the proof that none did.
  const followed = writes.slice(followWrites);
  console.log('requests while following the assessment:', JSON.stringify(followed));
  const acted = followed.filter(write =>
    /folder-first-task\/start$|\/tasks\/execute$|\/plans(\/|$)|\/tickets(\/|$)|\/api\/chat|\/orchestration\/tasks$/.test(
      write
    )
  );
  expect(acted.length === 0, `following the suggestion sent: ${acted}`);
  await shot('38-suggestion-on-plans-page');
  await phoneShot('38-suggestion-on-plans-page', page.locator('#plan-create-panel'));

  expect(
    JSON.stringify(await taskIDs(before.workspace)) === JSON.stringify(tasksBefore),
    'following the suggestion made a task'
  );
  expect(
    (await firstTask(before.resolved)).finished_at === done.finished_at,
    'following the suggestion ran the assessment again'
  );
}

// The assistant's drawer stays open across pages when it was open on Home. A
// person reading the page behind it closes it; so does this.
async function closeAssistantDrawer() {
  const close = page.locator('#personalAssistantClose');
  if (await close.isVisible().catch(() => false)) {
    await close.click();
    await page.locator('#personalAssistantPanel').waitFor({ state: 'hidden', timeout: 10000 });
  }
}

async function repeatPhase(untouched) {
  expect(existsSync(carried), `run setup and resume first: ${carried} is missing`);
  const before = JSON.parse(readFileSync(carried, 'utf8'));

  // 13. The same folder shown again opens that workspace; nothing new is offered.
  await show('Documents');
  const again = {
    headline: await text('#personalAssistantFolderOfferHeadline'),
    question: await text('#personalAssistantFolderOfferQuestion'),
    buttons: await buttons()
  };
  console.log('same folder again:', JSON.stringify(again));
  expect(
    /already has a workspace/.test(again.headline),
    `not recognized as set up: ${again.headline}`
  );
  expect(again.buttons[0] === `Open ${projectName}`, `first action: ${again.buttons}`);
  expect(
    !again.buttons.some(label => /^(Set up|Review setup)/.test(label)),
    `a second setup was offered: ${again.buttons}`
  );
  await shot('24-same-folder-again');

  // 14. A sibling worktree of the same repository is another folder: it is not
  //     the workspace above, and its review plans a workspace of its own.
  if (isOri) {
    await show('Desktop');
    await card.getByRole('button', { name: /^ori-agent-hotfix/ }).click();
    await page.waitForFunction(() =>
      /ori-agent-hotfix looks like/.test(
        document.getElementById('personalAssistantFolderOfferHeadline')?.textContent || ''
      )
    );
    expect(
      !/already has a workspace/.test(await text('#personalAssistantFolderOfferHeadline')),
      'a sibling worktree was taken for the first checkout'
    );
    await review();
    const sibling = {
      evidence: await list('#personalAssistantFolderEvidence'),
      plan: await list('#personalAssistantFolderPlan')
    };
    console.log('sibling worktree:', JSON.stringify([sibling.plan[0], sibling.plan[2]]));
    expect(
      /Creates a Code project workspace named ori-agent-hotfix/.test(sibling.plan[0]),
      `the sibling's plan: ${sibling.plan[0]}`
    );
    // The assistant made for the first project is saved now, so the sibling
    // would use it as it is rather than make a second one.
    expect(
      /Uses your saved agent Development Assistant/.test(sibling.plan[2]),
      `the sibling's assistant: ${sibling.plan[2]}`
    );
    expect(
      sibling.evidence.some(line =>
        /branch hotfix\/demo.*github\.com\/johnjallday\/ori-agent\.git/.test(line)
      ),
      `the sibling's own branch was not read: ${sibling.evidence}`
    );
    await shot('25-sibling-worktree-is-its-own-project');
  } else {
    console.log('sibling worktree NOT SHOWN: seed with --ori to include one');
  }

  await settled(before, untouched);
  console.log('OK (repeat): one workspace, one assistant, folders unchanged, no page errors');
}

// add-folder: the composer's Add folder, which is how a person with a model set
// up shows the assistant a folder without going to Home's Explore a folder. The
// folder picked is itself a checkout: its marker is at its root, and beside it
// are a package of its own, tests, data and caches. That used to be answered
// with "choose one of N compatible folders", its cache among them.
async function addFolderPhase() {
  const repository = join(sandbox, 'Downloads');
  const files = {
    '.git/HEAD': 'ref: refs/heads/main\n',
    '.git/config':
      '[core]\n\trepositoryformatversion = 0\n[remote "origin"]\n\turl = https://example.invalid/acme/gradebook.git\n',
    'pyproject.toml':
      '[project]\nname = "gradebook-tools"\nversion = "0.1.0"\nrequires-python = ">=3.11"\n\n[tool.pytest.ini_options]\ntestpaths = ["tests"]\n',
    'README.md':
      '# Gradebook tools\n\nScripts that sync a gradebook and enrollment lists.\n\nRun `pytest`.\n',
    'CLAUDE.md': '# Notes for assistants\n\nRun the tests before changing grading code.\n',
    'run.sh': '#!/bin/sh\npython main.py\n',
    'main.py': 'def main():\n    pass\n',
    'gradebook.py': '"""gradebook."""\n',
    'report.py': '"""report."""\n',
    'tests/test_grading.py': 'def test_ok():\n    assert True\n',
    'data/sample.csv': 'a,b\n',
    'enrollment/roster.py': 'ROSTER = []\n',
    'solutions/one.py': 'pass\n',
    '__pycache__/main.cpython-314.pyc': 'x',
    '.env': 'GRADEBOOK_DEMO_SETTING=never-read-this\n'
  };
  for (const [rel, content] of Object.entries(files)) {
    const file = join(repository, rel);
    mkdirSync(dirname(file), { recursive: true, mode: 0o750 });
    writeFileSync(file, content, { mode: 0o600 });
  }
  const before = { hash: hashFolder(repository), workspaces: await workspaceCount() };
  const configured = await context.request.post('/api/settings/system-model', {
    data: { provider: 'codex', model: 'gpt-5.6-luna' }
  });
  expect(configured.ok(), `no model provider in this sandbox (${configured.status()})`);

  // 26. Add folder in the composer. Nothing is sent to a model yet.
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
  await page.waitForFunction(
    () => document.getElementById('personalAssistantReviewFolderSetup')?.disabled === false,
    null,
    { timeout: 30000 }
  );
  await shot('26-add-folder-attached');

  // 27. Send. The real model answers; the proposal beside its reply is Ori's.
  await page.locator('#personalAssistantSend').click();
  await card.waitFor({ state: 'visible', timeout: 240000 });
  expect(
    (await page.locator('[data-folder-setup-suggestion]').count()) === 0,
    'a duplicate setup proposal remains'
  );
  const reply = await panel.locator('[data-message-role="assistant"]').last().innerText();
  const topics = await page.locator('[data-folder-discussion] button').allInnerTexts();
  console.log('reply alongside the canonical review:', reply.slice(0, 1200));
  expect(
    topics.join(',') === 'Discuss this folder',
    `its subfolders are offered as topics: ${topics}`
  );
  const said = await panel.innerText();
  expect(
    !/Choose setup scope|compatible folders/.test(said),
    'the conversation still asks which subfolder to set up'
  );
  expect(!said.includes('never-read-this'), 'a private file reached the conversation');
  await shot('27-add-folder-reply-proposes-the-project');

  // 28. The canonical review is already on the same card; no second setup control.
  const inline = card;
  await inline.waitFor({ state: 'visible', timeout: 30000 });
  await page.locator('#personalAssistantFolderOfferHeadline').waitFor({ state: 'visible' });
  const status = page.locator('#personalAssistantFolderReviewStatus');
  const recognized = {
    headline: await text('#personalAssistantFolderOfferHeadline'),
    question: await text('#personalAssistantFolderOfferQuestion'),
    buttons: await buttons(),
    status: await status.innerText(),
    chooser: await page.locator('#personalAssistantFolderSetupChoices').isVisible()
  };
  console.log('the card in the conversation:', JSON.stringify(recognized, null, 1));
  expect(
    recognized.headline === 'Downloads looks like a Python project.',
    `the card's headline: ${recognized.headline}`
  );
  expect(recognized.buttons[0] === 'Review setup', `first action: ${recognized.buttons}`);
  expect(!recognized.chooser, 'a scope chooser was shown for one project');
  expect(
    !/not available/.test(recognized.status),
    `the line under the conversation disagrees with the card: ${recognized.status}`
  );
  await inline.scrollIntoViewIfNeeded();
  await shot('28-add-folder-project-card');

  // 29. Its review: what was read, and everything Set up will do.
  await review();
  const reviewed = {
    headline: await text('#personalAssistantFolderOfferHeadline'),
    evidence: await list('#personalAssistantFolderEvidence'),
    plan: await list('#personalAssistantFolderPlan'),
    buttons: await buttons(),
    status: await status.innerText()
  };
  console.log('reviewed in the conversation:', JSON.stringify(reviewed, null, 1));
  expect(reviewed.plan.length === 5, `the plan has ${reviewed.plan.length} lines`);
  expect(
    reviewed.buttons.some(isSetupAction),
    `no setup action after a review: ${reviewed.buttons}`
  );
  expect(!/not available/.test(reviewed.status), `after the review: ${reviewed.status}`);
  expect(
    (await workspaceCount()) === before.workspaces,
    'a review in the conversation created a workspace'
  );
  await inline.scrollIntoViewIfNeeded();
  await shot('29-add-folder-reviewed-plan');

  // 30. The reviewed hire, confirmed on the card, makes the one workspace.
  await confirmSetup();
  const deadline = Date.now() + 120000;
  while ((await workspaceCount()) === before.workspaces) {
    expect(Date.now() < deadline, 'Set up in the conversation made no workspace');
    await page.waitForTimeout(1000);
  }
  await page.waitForTimeout(3000);
  const finished = {
    workspaces: (await workspaceCount()) - before.workspaces,
    assistants: await developmentAssistants(),
    status: await status.innerText().catch(() => ''),
    conversation: (await panel.innerText()).replace(/\n{2,}/g, '\n').slice(-900)
  };
  console.log('after Set up:', JSON.stringify(finished, null, 1));
  expect(finished.workspaces === 1, `Set up made ${finished.workspaces} workspaces`);
  expect(finished.assistants === 1, `${finished.assistants} development assistants`);
  expect(hashFolder(repository) === before.hash, 'the project folder changed');
  expect(errors.length === 0, `page errors: ${errors.join('; ')}`);
  await shot('30-add-folder-set-up');
  console.log(
    'OK (add-folder): one proposal, one card, one workspace, folder unchanged, no page errors'
  );
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
  const untouched = ['documents', 'api', 'site'];
  if (phase === 'add-folder') {
    // No folder is shown on Home first: a review waiting there would be the
    // one the conversation is told to finish.
    await addFolderPhase();
  } else if (CONTINUES.includes(phase)) {
    if (phase === 'resume') await resumePhase(untouched);
    if (phase === 'assess-interrupt') await assessInterruptPhase(untouched);
    if (phase === 'assess') await assessPhase(untouched);
    if (phase === 'repeat') await repeatPhase(untouched);
  } else {
    const before = { hashes: hashes(untouched), workspaces: await workspaceCount() };

    await recognize();
    expect((await workspaceCount()) === before.workspaces, 'showing folders created a workspace');
    if (phase === 'review') await reviewPhase();
    if (phase === 'setup') await setupPhase(before);

    expect(
      (await workspaceCount()) === before.workspaces,
      'a review or a paused setup created a workspace'
    );
    expect(
      JSON.stringify(hashes(untouched)) === JSON.stringify(before.hashes),
      'a seeded folder changed during the demo'
    );
    expect(errors.length === 0, `page errors: ${errors.join('; ')}`);
    console.log(`OK (${phase}): folders unchanged, no workspace created, no page errors`);
  }
} finally {
  await browser.close();
}
