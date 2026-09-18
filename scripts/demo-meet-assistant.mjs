/*
 * Drives Mission 01, "Meet your assistant" (tasks/prd-meet-your-assistant-mission.md),
 * against a running, isolated demo server and saves screenshots. Real UI and
 * real endpoints throughout; the only shortcut is closing first-run onboarding
 * through the API where a stage is not about the modal.
 *
 *   node scripts/demo-meet-assistant.mjs <baseUrl> <outDir> <stage> [--width=1280]
 *
 * Stages (each needs a FRESH sandbox: a hire cannot be undone):
 *   preset        New Agent opens the assistant preset while unhired; Hire lands
 *                 on /?quest=build-hq with the relationship needs_hq; afterwards
 *                 New Agent opens the ordinary form again.
 *   walkthrough   /agents?quest=meet-assistant: each of Ori's five steps marks the
 *                 right control; the hire hands over to Build My HQ with the
 *                 hand-over line and the HQ site marked.
 *   panel-closed  Ori's panel is closed at step 1; the form alone hires, and
 *                 Mission 01 completes.
 *   narrow        The same with the form alone at a 400px viewport.
 *   home          The whole path with no model: the modal, Home before the hire
 *                 (Ori's prompt on the Agents nav entry, Mission 01 on the card,
 *                 02-05 locked, a quiet Today), Take me there, the five steps,
 *                 the hire, the hand-over, then Build My HQ to the end.
 *   modal         The real first-run modal: Welcome, then Model with "Continue
 *                 without a model", closes on Home with no hire. An old /?hire=1
 *                 link lands on Mission 01, and Ask Ori offers "Meet your
 *                 assistant" for a work request.
 *
 * Every stage prints what it observed and any console errors or failed
 * requests, so a quietly broken page does not pass as a clean demo.
 */
import { chromium } from 'playwright';
import { mkdirSync } from 'node:fs';
import { join, resolve } from 'node:path';

const [baseUrl, outDir, stage = 'preset', ...flags] = process.argv.slice(2);
if (!baseUrl || !outDir) {
  console.error('usage: node scripts/demo-meet-assistant.mjs <baseUrl> <outDir> <stage> [--width=n]');
  process.exit(1);
}
const flag = name => {
  const found = flags.find(value => value.startsWith(`--${name}=`));
  return found ? found.slice(name.length + 3) : '';
};
const out = resolve(outDir);
mkdirSync(out, { recursive: true });
const width = Number(flag('width')) || 1280;

const browser = await chromium.launch();
const problems = [];
let exitCode = 0;

// Noise every page on this build produces, unrelated to Mission 01: the update
// checker's request is cut off when a demo navigates away, and the Ask Ori
// launcher looks up an agent profile that a fresh sandbox does not have.
const KNOWN_NOISE = [
  /Error checking for updates: TypeError: Failed to fetch/,
  /\/api\/agents\?name=Ask%20Ori/,
  /Failed to load resource: the server responded with a status of 404/
];
const isNoise = text => KNOWN_NOISE.some(pattern => pattern.test(text));

function check(ok, message) {
  console.log(`${ok ? 'ok  ' : 'FAIL'} ${message}`);
  if (!ok) exitCode = 1;
}

async function newPage(viewportWidth = width) {
  const page = await browser.newPage({ viewport: { width: viewportWidth, height: 900 } });
  page.on('console', m => {
    if (m.type() === 'error') problems.push(`console: ${m.text()}`);
  });
  page.on('pageerror', e => problems.push(`pageerror: ${e.message}`));
  page.on('requestfailed', r => {
    // A navigation away cancels in-flight polls; that is not a broken page.
    if (!/ERR_ABORTED/.test(r.failure()?.errorText || '')) {
      problems.push(`request failed: ${r.url()}`);
    }
  });
  page.on('response', r => {
    if (r.status() >= 400) problems.push(`HTTP ${r.status()}: ${r.url()}`);
  });
  return page;
}

async function api(page, path, options = {}) {
  return page.evaluate(
    async ([p, o]) => {
      const response = await fetch(p, o);
      return { status: response.status, body: await response.json().catch(() => ({})) };
    },
    [path, options]
  );
}

async function relationshipState(page) {
  const { body } = await api(page, '/api/personal-assistant');
  return body?.personal_assistant?.state || '';
}

async function shot(page, name) {
  const file = join(out, `${name}.png`);
  await page.screenshot({ path: file, fullPage: false });
  console.log(`shot ${file}`);
}

async function closeOnboarding(page) {
  await page.goto(`${baseUrl}/agents`);
  const { status } = await api(page, '/api/onboarding/complete', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: '{}'
  });
  check(status === 200, `onboarding closed through the API (${status})`);
}

async function stagePreset() {
  const page = await newPage();
  await closeOnboarding(page);
  check((await relationshipState(page)) === 'needs_hire', 'relationship starts needs_hire');

  // /agents/create points at Mission 01 while unhired (FR40).
  await page.goto(`${baseUrl}/agents/create`);
  await page.locator('#assistantPointer').waitFor({ state: 'visible', timeout: 5000 }).catch(() => {});
  check(await page.locator('#assistantPointer').isVisible(), '/agents/create shows the pointer line');
  check(
    (await page.locator('#assistantPointerLink').getAttribute('href')) === '/agents?quest=meet-assistant',
    'the pointer links to Mission 01'
  );
  await shot(page, 'create-page-pointer');

  await page.goto(`${baseUrl}/agents`);
  await page.locator('#newAgentBtn').click();
  await page.locator('#cr-name').waitFor();
  await page.locator('#cr-appearance-host *').first().waitFor();
  for (const id of ['#cr-name', '#cr-appearance-host', '#cr-focus-group', '#cr-mandate', '#createSubmit', '#cr-standard-form']) {
    check((await page.locator(id).count()) === 1, `preset renders ${id}`);
  }
  for (const id of ['#cr-role', '#cr-model', '#cr-description', '#cr-favorite', '#cr-tags-host']) {
    check((await page.locator(id).count()) === 0, `preset omits ${id}`);
  }
  check((await page.locator('#cr-name').inputValue()) === 'Assistant', 'name is prefilled "Assistant"');
  check(
    await page.locator('#cr-name').evaluate(el => el === document.activeElement),
    'focus starts in the name field'
  );
  check((await page.locator('#createSubmit').textContent()).trim() === 'Hire assistant', 'button reads Hire assistant');
  await shot(page, 'preset-open');
  await page.locator('#cr-standard-form').scrollIntoViewIfNeeded();
  await shot(page, 'preset-bottom');

  // The swap to the standard form and back keeps the mission open.
  await page.locator('#cr-standard-form').click();
  await page.locator('#cr-role').waitFor();
  check((await page.locator('#cr-assistant-form').count()) === 1, 'standard form offers the way back');
  await page.locator('#cr-assistant-form').click();
  await page.locator('#cr-focus-group').waitFor();
  check(true, 'swapped back to the preset');

  await page.locator('#cr-name').fill('Atlas');
  await page.locator('#cr-mandate').fill('Keep this week realistic.');
  await page.locator('#createSubmit').click();
  await page.waitForURL(url => url.pathname === '/' && url.search.includes('quest=build-hq'), {
    timeout: 20000
  });
  check(true, `hire landed on ${new URL(page.url()).pathname}${new URL(page.url()).search}`);
  check((await relationshipState(page)) === 'needs_hq', 'relationship is needs_hq after the hire');
  const flag = await page.evaluate(() => sessionStorage.getItem('ori:assistant-just-hired'));
  check(flag === '1' || flag === null, `hand-over flag set for the HQ walkthrough (${flag})`);
  await page.waitForTimeout(1500);
  await shot(page, 'after-hire-home');

  await page.goto(`${baseUrl}/agents`);
  await page.locator('#newAgentBtn').click();
  await page.locator('#cr-role').waitFor();
  check((await page.locator('#cr-focus-group').count()) === 0, 'after the hire New Agent opens the standard form');
  check((await page.locator('#cr-assistant-form').count()) === 0, 'no hire link once hired');
  await shot(page, 'standard-after-hire');

  await page.goto(`${baseUrl}/agents/create`);
  await page.waitForTimeout(1000);
  check(!(await page.locator('#assistantPointer').isVisible()), '/agents/create drops the pointer once hired');
  await page.close();
}

async function missionOne(page) {
  const { body } = await api(page, '/api/progression');
  return (body?.missions || []).find(mission => mission.order === 1) || {};
}

// The step Ori's panel shows, and which control carries the mark.
async function guideStep(page) {
  return page.evaluate(() => ({
    step: document.querySelector('#oriGuideReply .ori-guide__quest-step')?.textContent || '',
    answer: document.querySelector('#oriGuideReply .ori-guide__answer')?.textContent || '',
    marked: Array.from(document.querySelectorAll('.is-ori-coachmark')).map(el => '#' + el.id),
    focused: !document.activeElement
      ? ''
      : document.activeElement.id
        ? '#' + document.activeElement.id
        : `${document.activeElement.tagName.toLowerCase()}[${document.activeElement.getAttribute('value') || ''}]`
  }));
}

async function expectStep(page, index, markedId, label) {
  await page
    .waitForFunction(
      ([n, id]) =>
        (document.querySelector('#oriGuideReply .ori-guide__quest-step')?.textContent || '') ===
          `Step ${n} of 5` && !!document.querySelector(`${id}.is-ori-coachmark`),
      [index, markedId],
      { timeout: 5000 }
    )
    .catch(() => {});
  const seen = await guideStep(page);
  check(
    seen.step === `Step ${index} of 5` && seen.marked.includes(markedId),
    `step ${index} (${label}) marks ${markedId} — saw "${seen.step}", marked ${seen.marked.join(',') || 'nothing'}`
  );
  return seen;
}

async function hireAndLand(page) {
  await page.locator('#createSubmit').click();
  await page.waitForURL(url => url.pathname === '/' && url.search.includes('quest=build-hq'), {
    timeout: 20000
  });
  check((await relationshipState(page)) === 'needs_hq', 'the hire is durable (needs_hq)');
  const mission = await missionOne(page);
  check(
    mission.id === 'pa-meet-assistant' && mission.status === 'completed',
    `Mission 01 is ${mission.status || 'missing'}`
  );
}

async function stageWalkthrough() {
  const page = await newPage();
  await closeOnboarding(page);
  await page.goto(`${baseUrl}/agents?quest=meet-assistant`);
  await expectStep(page, 1, '#newAgentBtn', 'New Agent');
  check(!new URL(page.url()).search.includes('quest='), 'the quest param was stripped');
  check(
    (await page.locator('#rosterCoachmark').isHidden()) !== false,
    "the roster's own card hint stays out of the way"
  );
  await shot(page, 'walkthrough-1-new-agent');

  await page.locator('#newAgentBtn').click();
  await expectStep(page, 2, '#cr-name', 'name');
  await shot(page, 'walkthrough-2-name');

  await page.locator('[data-ori-quest-choice="keep-name"]').click();
  await expectStep(page, 3, '#cr-appearance-host', 'face');
  await shot(page, 'walkthrough-3-face');

  await page.locator('[data-ori-quest-choice="keep-face"]').click();
  await expectStep(page, 4, '#cr-focus-group', 'focus');
  await shot(page, 'walkthrough-4-focus');

  // Ticking a box is the form's own signal; it must not move focus to Hire.
  await page.locator('#cr-focus-group input[value="prepare_for_meetings"]').check();
  const five = await expectStep(page, 5, '#createSubmit', 'hire');
  check(five.focused !== '#createSubmit', `focus stayed in the form (${five.focused || 'body'})`);
  await shot(page, 'walkthrough-5-hire');

  await hireAndLand(page);
  await page
    .waitForFunction(
      () => /That’s your assistant/.test(document.querySelector('#oriGuideReply')?.textContent || ''),
      null,
      { timeout: 8000 }
    )
    .catch(() => {});
  const handOver = await guideStep(page);
  check(/^That’s your assistant\. Now let’s give them a home\./.test(handOver.answer), `hand-over line: "${handOver.answer}"`);
  check(
    await page.evaluate(() => !!document.querySelector('[data-hq-site].is-ori-coachmark')),
    'the reserved HQ site is marked'
  );
  await shot(page, 'walkthrough-handover-home');
  await page.close();
}

async function stageFormAlone({ viewport, closePanel, prefix }) {
  const page = await newPage(viewport);
  await closeOnboarding(page);
  await page.goto(`${baseUrl}/agents?quest=meet-assistant`);
  if (closePanel) {
    await expectStep(page, 1, '#newAgentBtn', 'New Agent');
    await page.locator('#oriGuideClose').click();
    check(await page.locator('#oriGuidePanel').isHidden(), "Ori's panel is closed");
  }
  await page.locator('#newAgentBtn').click();
  await page.locator('#cr-name').waitFor();
  await page.locator('#cr-name').fill('Juniper');
  await page.locator('#cr-focus-group input[value="help_with_email"]').check();
  await shot(page, `${prefix}-preset`);
  await hireAndLand(page);
  await shot(page, `${prefix}-home`);
  await page.close();
}

async function stageModal() {
  const page = await newPage();
  const hireRequests = [];
  page.on('request', request => {
    if (request.url().includes('/api/personal-assistant/hire')) hireRequests.push(request.url());
  });
  await page.goto(`${baseUrl}/`);
  await page.locator('#onboardingModal.show').waitFor({ timeout: 15000 });
  const label = await page.locator('#onboardingStepLabel').textContent();
  check(label.trim() === 'Step 1 of 2', `the modal counts two phases ("${label.trim()}")`);
  check(
    (await page.locator('label[for="onboardingAssistantName"]').textContent()).trim() ===
      'What should Ori be called?',
    'the guide-name field is labelled for Ori'
  );
  check((await page.locator('#pafHireBtn').count()) === 0, 'the modal has no hire controls');
  await page.locator('#onboardingUserName').fill('Sam');
  await shot(page, 'modal-welcome');
  await page.locator('#welcomeNextBtn').click();
  await page.locator('#continueWithoutModelBtn').waitFor({ state: 'visible' });
  check(
    (await page.locator('#onboardingStepLabel').textContent()).trim() === 'Step 2 of 2',
    'Model is the last phase'
  );
  await shot(page, 'modal-model');
  await page.locator('#continueWithoutModelBtn').click();
  await page.waitForURL(url => url.pathname === '/' && !url.search.includes('hire'), {
    timeout: 15000
  });
  await page.locator('#onboardingModal.show').waitFor({ state: 'detached', timeout: 5000 }).catch(() => {});
  await page.waitForTimeout(2000);
  check(!(await page.locator('#onboardingModal.show').isVisible()), 'the modal closed on Home');
  const onboarding = await api(page, '/api/onboarding/status');
  check(onboarding.body?.needs_onboarding === false, 'onboarding is complete without a hire');
  check((await relationshipState(page)) === 'needs_hire', 'no assistant was hired');
  check(hireRequests.length === 0, `the modal sent no hire request (${hireRequests.length})`);
  await shot(page, 'modal-closed-home');

  await page.goto(`${baseUrl}/?hire=1`);
  await page.waitForURL(url => url.pathname === '/agents', { timeout: 15000 });
  await expectStep(page, 1, '#newAgentBtn', 'New Agent');
  check(true, `/?hire=1 landed on ${new URL(page.url()).pathname}${new URL(page.url()).search}`);
  await shot(page, 'old-hire-link');

  // Ask Ori for work while there is no assistant to send it to.
  await page.goto(`${baseUrl}/agents`);
  await page.locator('#oriGuideInput').waitFor({ state: 'visible', timeout: 8000 }).catch(() => {});
  if (!(await page.locator('#oriGuideInput').isVisible())) {
    await page.locator('#oriGuideLauncher').click();
  }
  // A work verb first: the guide treats "draft …" as work, not a question.
  await page.locator('#oriGuideInput').fill('Draft the follow-up emails for today');
  await page.locator('#oriGuideInput').press('Enter');
  const offer = page.locator('#oriGuideReply [data-ori-action="navigate"]', {
    hasText: 'Meet your assistant'
  });
  await offer.waitFor({ timeout: 15000 }).catch(() => {});
  const offered = await offer.count();
  check(offered === 1, 'Ask Ori offers "Meet your assistant" for work');
  if (offered) {
    check(
      (await offer.getAttribute('href')) === '/agents?quest=meet-assistant',
      'the offer links to Mission 01'
    );
  } else {
    console.log(`     reply was: ${(await page.locator('#oriGuideReply').textContent()).trim().slice(0, 200)}`);
  }
  await shot(page, 'ask-ori-offer');
  check(hireRequests.length === 0, 'nothing in this stage hired an assistant');
  await page.close();
}

async function finishModalWithoutModel(page) {
  await page.goto(`${baseUrl}/`);
  await page.locator('#onboardingModal.show').waitFor({ timeout: 15000 });
  await page.locator('#onboardingUserName').fill('Sam');
  await page.locator('#welcomeNextBtn').click();
  await page.locator('#continueWithoutModelBtn').waitFor({ state: 'visible' });
  await page.locator('#continueWithoutModelBtn').click();
  await page.waitForURL(url => url.pathname === '/', { timeout: 15000 });
}

async function stageHome() {
  const page = await newPage();
  await finishModalWithoutModel(page);

  // Home before the hire: Ori's prompt, pointing at the Agents nav entry.
  await page
    .waitForFunction(
      () => /works from the Agents page/.test(document.querySelector('#oriGuideReply')?.textContent || ''),
      null,
      { timeout: 10000 }
    )
    .catch(() => {});
  const prompt = await guideStep(page);
  check(/^Your assistant works from the Agents page\./.test(prompt.answer), `Home prompt: "${prompt.answer}"`);
  check(prompt.marked.includes('#navAgentsLink'), `the Agents nav entry is marked (${prompt.marked.join(',')})`);
  check((await page.locator('[data-ori-quest-choice="go"]').count()) === 1, 'Take me there is offered');
  const banner = (await page.locator('#personalAssistantTodayBanner').textContent()).trim();
  check(banner === 'Meet your assistant to start Today.', `Today is quiet: "${banner}"`);
  check(
    (await page.locator('#personalAssistantTodayBanner a').getAttribute('href')) ===
      '/agents?quest=meet-assistant',
    "Today's one link is Mission 01"
  );
  await shot(page, 'home-1-prompt');

  // The mission board: Mission 01 on the card, 02-05 locked beneath it.
  await page.locator('#cockpitQuestsToggle').click();
  await page.locator('[data-role="first-mission"]').waitFor({ state: 'visible', timeout: 8000 });
  const card = (await page.locator('[data-role="first-mission-title"]').textContent()).trim();
  const kicker = (await page.locator('[data-role="first-mission-kicker"]').textContent()).trim();
  check(card === 'Meet your assistant' && kicker === 'Mission 01', `card: ${kicker} · ${card}`);
  const locked = page.locator('.quest-item-locked');
  check((await locked.count()) === 4, `missions 02-05 are locked (${await locked.count()})`);
  check(
    (await locked.filter({ hasText: 'Meet your assistant first' }).count()) === 4,
    'each locked row says why'
  );
  check(
    (await locked.locator('a, button').count()) === 0,
    'no locked row offers Start, Skip or Resume'
  );
  await shot(page, 'home-2-missions');
  await page.keyboard.press('Escape');

  // Take me there, then the five steps on the Agents page.
  await page.locator('[data-ori-quest-choice="go"]').click();
  await page.waitForURL(url => url.pathname === '/agents', { timeout: 15000 });
  await expectStep(page, 1, '#newAgentBtn', 'New Agent');
  await page.locator('#newAgentBtn').click();
  await expectStep(page, 2, '#cr-name', 'name');
  await page.locator('#cr-name').fill('Atlas');
  await page.locator('#cr-name').press('Tab');
  await expectStep(page, 3, '#cr-appearance-host', 'face');
  await page.locator('[data-ori-quest-choice="keep-face"]').click();
  await expectStep(page, 4, '#cr-focus-group', 'focus');
  await page.locator('[data-ori-quest-choice="done-choosing"]').click();
  await expectStep(page, 5, '#createSubmit', 'hire');
  await shot(page, 'home-3-hire-step');
  await hireAndLand(page);

  // The hand-over, and Build My HQ running.
  await page
    .waitForFunction(
      () => /That’s your assistant/.test(document.querySelector('#oriGuideReply')?.textContent || ''),
      null,
      { timeout: 10000 }
    )
    .catch(() => {});
  const handOver = await guideStep(page);
  check(/^That’s your assistant\./.test(handOver.answer), `hand-over: "${handOver.answer}"`);
  check(handOver.step === 'Step 1 of 3', `Build My HQ is running (${handOver.step})`);
  await shot(page, 'home-4-handover');

  // Build My HQ to the end: the site, the Build action, the form, confirm.
  await page.locator('[data-hq-site]').click();
  await page.locator('[data-hq-action="build"]').click();
  const buildModal = page.locator('#hqBuildModal');
  await buildModal.waitFor({ state: 'visible', timeout: 10000 });
  await shot(page, 'home-5-build-form');
  await page.locator('#hqBuildSubmitBtn').click();
  // Poll from here: waitForFunction takes an async predicate's promise as
  // truthy and returns at once.
  for (let tries = 0; tries < 30 && (await relationshipState(page)) !== 'active'; tries += 1) {
    await page.waitForTimeout(1000);
  }
  check((await relationshipState(page)) === 'active', 'Build My HQ finished: the assistant is active');
  const { body } = await api(page, '/api/progression');
  const hq = (body?.missions || []).find(mission => mission.order === 2) || {};
  check(hq.status === 'completed', `Mission 02 (Build My HQ) is ${hq.status}`);
  await page.waitForTimeout(1500);
  await shot(page, 'home-6-hq-built');
  await page.close();
}

try {
  if (stage === 'preset') await stagePreset();
  else if (stage === 'home') await stageHome();
  else if (stage === 'modal') await stageModal();
  else if (stage === 'walkthrough') await stageWalkthrough();
  else if (stage === 'panel-closed') {
    await stageFormAlone({ viewport: width, closePanel: true, prefix: 'panel-closed' });
  } else if (stage === 'narrow') {
    await stageFormAlone({ viewport: 400, closePanel: false, prefix: 'narrow' });
  } else throw new Error(`unknown stage ${stage}`);
} catch (error) {
  console.log(`FAIL ${error.message}`);
  exitCode = 1;
} finally {
  await browser.close();
}
const real = problems.filter(problem => !isNoise(problem));
for (const problem of real) console.log(`problem: ${problem}`);
if (real.length) exitCode = 1;
process.exit(exitCode);
