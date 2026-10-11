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
//              the receipt is what was saved, the same folder shown again opens
//              that workspace, and a sibling worktree is a different project
//
// Usage:
//   ./scripts/demo-server.sh 8947 "$TMPDIR/ori-software-demo"      (background)
//   ./scripts/software-folder-seed.sh "$TMPDIR/ori-software-demo" --ori
//   node scripts/demo-software-folder.mjs http://localhost:8947 "$TMPDIR/ori-software-demo" <shots-dir> [phase]
//
// For resume: run setup, stop the server (./scripts/stop-demo-server.sh 8947),
// start it again on the same sandbox, then run resume with the same shots dir.
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
  rmSync,
  writeFileSync
} from 'node:fs';
import { join, relative } from 'node:path';

const [base, sandbox, output, phase = 'review'] = process.argv.slice(2);
if (!base || !sandbox || !output || !['recognize', 'review', 'setup', 'resume'].includes(phase))
  throw new Error(
    'usage: demo-software-folder.mjs <base-url> <sandbox-dir> <shots-dir> [recognize|review|setup|resume]'
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
}
const text = id => page.locator(id).innerText();
const list = id => page.locator(`${id} li`).allInnerTexts();
const buttons = () =>
  card.locator('#personalAssistantFolderOfferActions').locator('button, a').allInnerTexts();
const press = name => card.getByRole('button', { name, exact: true }).click();
const focused = () => page.evaluate(() => document.activeElement?.dataset?.folderAction || '');
function expect(condition, message) {
  if (!condition) throw new Error(message);
}
// review presses Review setup (or Review again) and waits for what was read.
async function review(label = 'Review setup') {
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
    reviewed.buttons.includes('Set up') === available,
    `Set up shown=${reviewed.buttons.includes('Set up')} but setup_available=${available}`
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
  rmSync(folders.scratchpad, { recursive: true });
  mkdirSync(folders.scratchpad, { mode: 0o750 });
  writeFileSync(join(folders.scratchpad, 'package.json'), '{ "name": "scratchpad" }\n', {
    mode: 0o600
  });
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

// The run pop-up: what it says, its steps, its receipt, and its buttons.
const runModal = page.locator('#folderSetupRunModal');
async function runModalState() {
  return {
    eyebrow: await text('#folderSetupRunEyebrow'),
    title: await text('#folderSetupRunTitle'),
    status: await text('#folderSetupRunStatus'),
    count: await text('#folderSetupRunCount'),
    steps: await page.locator('#folderSetupRunSteps li').allInnerTexts(),
    receipt: await page.locator('#folderSetupRunReceipt li').allInnerTexts(),
    buttons: await page.locator('#folderSetupRunActions').locator('button, a').allInnerTexts()
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
  expect((await buttons())[0] === 'Set up', `first action: ${await buttons()}`);
  await press('Set up');
  await runModal.waitFor({ state: 'visible', timeout: 30000 });
  await page.waitForFunction(
    () => /needs you/.test(document.getElementById('folderSetupRunTitle')?.textContent || ''),
    null,
    { timeout: 30000 }
  );
  const paused = await runModalState();
  console.log('paused setup:', JSON.stringify(paused, null, 1));
  expect(/^0 of 5 steps finished/.test(paused.count), `steps finished: ${paused.count}`);
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
  await runModal.waitFor({ state: 'visible', timeout: 30000 });
  await page.waitForFunction(
    () => /is ready/.test(document.getElementById('folderSetupRunTitle')?.textContent || ''),
    null,
    { timeout: 60000 }
  );
  const done = await runModalState();
  console.log('finished setup:', JSON.stringify(done, null, 1));
  // A finished setup ends on its receipt, read back from what was saved; the
  // run's own step list is gone with the run.
  expect(done.eyebrow === 'ALL SET', `the pop-up did not end on done: ${done.eyebrow}`);
  expect(
    /Nothing in the project was run or changed/.test(done.status),
    `the receipt's summary: ${done.status}`
  );
  const receipt = done.receipt.join(' | ').replace(/\n/g, ' ');
  for (const wanted of [
    new RegExp(`${projectName} \\(created\\)`),
    /linked where it is, read-only/,
    /Code Project \(planning first; approved work waits for you/,
    /Development Assistant \(development assistant, added\)/,
    // The assessment waits: it is startable on a click, or it says why not.
    /Assess this project and suggest a first task \((Cannot start here yet|Needs a model before it can start|Starts when you press [^)]*)\)/
  ])
    expect(wanted.test(receipt), `receipt lacks ${wanted}: ${receipt}`);
  expect(!/Ready to start/.test(receipt), `the receipt promises a start: ${receipt}`);
  expect(
    (await workspaceCount()) === before.workspaces + 1,
    'the setup did not make one workspace'
  );
  expect((await developmentAssistants()) === 1, 'the setup did not make exactly one assistant');
  await shot('22-setup-receipt');

  // 12. The card afterwards: the same receipt, and an assessment that waits.
  await page.locator('#folderSetupRunModal .btn-close').click();
  await runModal.waitFor({ state: 'hidden' });
  await card.scrollIntoViewIfNeeded();
  const resolved = await currentOffer();
  console.log('first assessment:', JSON.stringify(resolved?.first_task));
  expect(resolved?.status === 'resolved', `the offer is ${resolved?.status}`);
  expect(resolved?.first_task?.state === 'seeded', 'the assessment was started by setup');
  await shot('23-card-receipt');

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

  expect(
    (await workspaceCount()) === before.workspaces + 1,
    'showing folders again changed the workspaces'
  );
  expect((await developmentAssistants()) === 1, 'a second assistant appeared');
  expect(
    JSON.stringify(hashes(untouched)) === JSON.stringify(before.hashes),
    'a seeded folder changed during setup'
  );
  expect(errors.length === 0, `page errors: ${errors.join('; ')}`);
  console.log('OK (resume): one workspace, one assistant, folders unchanged, no page errors');
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
  if (phase === 'resume') {
    await resumePhase(untouched);
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
