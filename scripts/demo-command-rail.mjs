// Drives the Command rail accordion in a disposable demo sandbox and saves
// screenshots, one stage at a time (tasks/tasks-command-rail-accordion.md).
//
//   node scripts/demo-command-rail.mjs <base-url> <shots-dir> <stage> [theme]
//
// Stages:
//   rail      desktop (1280x800): the rows, one section open at a time, every
//             "+", every body verb, Escape, and a keyboard-only pass
//   remember  the open section survives a reload and is per workspace
//   narrow    390x844 and 768x1024: the tab row, and nothing under the
//             launcher pills at the bottom of the page
//
// The four fixtures (an empty workspace, one with notes and linked folders, a
// group, and the Personal HQ) are created on first use and found by name after
// that, so every stage can run against the same sandbox.
// theme is "dark" (default) or "light".
import { chromium } from 'playwright';
import { mkdirSync } from 'node:fs';
import { join, resolve } from 'node:path';

const [base, output, stage = 'rail', theme = 'dark'] = process.argv.slice(2);
if (!base || !output) {
  throw new Error('usage: demo-command-rail.mjs <base-url> <shots-dir> <stage> [dark|light]');
}
mkdirSync(output, { recursive: true });

const browser = await chromium.launch();
const context = await browser.newContext({ viewport: { width: 1280, height: 800 } });
await context.addInitScript(value => {
  try {
    localStorage.setItem('ori-theme', value);
  } catch (_) {
    /* a page without storage keeps its default theme */
  }
}, theme);
const page = await context.newPage();
const problems = [];
page.on('pageerror', error => problems.push(`pageerror: ${error.message}`));
page.on('console', message => {
  // A failed request is reported below with its address, which this is not.
  if (message.type() === 'error' && !/Failed to load resource/.test(message.text())) {
    problems.push(`console: ${message.text()}`);
  }
});
page.on('response', response => {
  if (response.status() >= 400) {
    problems.push(`HTTP ${response.status()}: ${response.request().method()} ${response.url()}`);
  }
});

const RAIL = '#workspaceCommandView .ws-cmd-rail';
const toggle = key => `${RAIL} [data-cmd-manage-section="${key}"]`;
const plus = key =>
  key === 'backlog'
    ? `${RAIL} [data-cmd-backlog-add]`
    : `${RAIL} [data-cmd-primary-section="${key}"]`;

async function shot(name) {
  const path = join(output, `${stage}-${theme}-${name}.png`);
  await page.screenshot({ path });
  console.log(path);
}

function check(condition, message) {
  if (!condition) throw new Error(message);
  console.log(`ok   ${message}`);
}

async function json(response) {
  if (!response.ok()) {
    throw new Error(`${response.status()} ${response.url()}: ${await response.text()}`);
  }
  return response.json();
}

// ---------- fixtures ----------

async function workspaces() {
  const body = await json(await page.request.get(`${base}/api/workspaces`));
  return body.folders || body.workspaces || [];
}

async function createWorkspace(data) {
  const body = await json(await page.request.post(`${base}/api/workspaces`, { data }));
  return body.folder ?? body.workspace ?? body;
}

async function plainWorkspace(name) {
  const found = (await workspaces()).find(ws => ws.name === name);
  if (found) return found;
  return createWorkspace({ name, blank: true, create_template_agents: false });
}

async function busyWorkspace() {
  const name = 'Rail Busy';
  const existing = (await workspaces()).find(ws => ws.name === name);
  if (existing) return existing;
  const ws = await createWorkspace({ name, blank: true, create_template_agents: false });
  for (const note of ['Mix notes', 'Release checklist']) {
    await json(
      await page.request.post(`${base}/api/workspaces/${ws.id}/notes`, {
        data: { name: note, content: `# ${note}\n` }
      })
    );
  }
  for (const folder of ['albums', 'references']) {
    const path = resolve(output, 'linked', folder);
    mkdirSync(path, { recursive: true });
    await json(
      await page.request.post(`${base}/api/workspaces/${ws.id}/directories`, {
        data: { name: folder, path }
      })
    );
  }
  return ws;
}

// A plain POST with kind=group is refused: a group is created through the
// reviewed roster plan, as the Create Workspace flow does.
async function groupWorkspace() {
  const name = 'Rail Group';
  const existing = (await workspaces()).find(ws => ws.name === name);
  if (existing) return existing;
  const plan = await json(
    await page.request.post(`${base}/api/workspaces/template-agent-plan`, {
      data: { group_roster: true, group_name: name }
    })
  );
  const manager = plan.agents[0];
  const group = await createWorkspace({
    name,
    kind: 'group',
    group_roster: true,
    create_template_agents: true,
    template_agent_review: {
      version: 1,
      plan_revision: plan.revision,
      expectations: [{ index: 0, name: manager.name, action: manager.action }]
    }
  });
  await createWorkspace({
    name: 'Rail Member',
    parent_id: group.id,
    blank: true,
    create_template_agents: false
  });
  return group;
}

async function personalHQ() {
  const read = async () =>
    (await json(await page.request.get(`${base}/api/personal-hq/status`)))?.status;
  let status = await read();
  if (!status?.workspace?.folder_slug) {
    const assistant = async () =>
      (await json(await page.request.get(`${base}/api/personal-assistant`))).personal_assistant;
    await page.request.post(`${base}/api/personal-assistant/hire`, {
      data: {
        request_id: 'rail-demo-hire',
        if_version: (await assistant()).state_version ?? 0,
        display_name: 'Atlas',
        mandate: 'Keep my projects moving.',
        focus_areas: ['plan_my_day']
      }
    });
    await page.request.post(`${base}/api/personal-assistant/hq`, {
      data: {
        request_id: 'rail-demo-hq',
        if_version: (await assistant()).state_version,
        name: 'My HQ',
        timezone: 'UTC'
      }
    });
    status = await read();
  }
  if (!status?.workspace?.folder_slug) {
    throw new Error('could not find or build a Personal HQ in this sandbox');
  }
  return { ...status.workspace, id: status.workspace_id || status.workspace.id };
}

async function fixtures() {
  await page.request.post(`${base}/api/onboarding/skip`);
  await page.request.post(`${base}/api/settings/workspace-root`, {
    data: { workspace_root: '' }
  });
  return {
    empty: await plainWorkspace('Rail Empty'),
    busy: await busyWorkspace(),
    group: await groupWorkspace(),
    hq: await personalHQ()
  };
}

// ---------- page helpers ----------

async function applyTheme() {
  await page.evaluate(value => {
    document.documentElement.setAttribute('data-bs-theme', value);
  }, theme);
}

// A workspace with no agents opens onto "Create a Commander for this
// workspace?", which covers the page. The demo is about the rail underneath,
// so the prompt is marked dismissed for the session, as the smoke spec does.
async function openDetails(ws) {
  await page.addInitScript(id => {
    try {
      window.sessionStorage.setItem(`workspace-detail-entry-agent-prompt-dismissed:${id}`, '1');
    } catch (_) {
      /* the prompt shows; the stage will fail on the first click and say so */
    }
  }, ws.id);
  await page.goto(`${base}/workspaces/${ws.folder_slug}`);
  await page.locator(RAIL).waitFor({ state: 'visible' });
  await applyTheme();
  await page.waitForTimeout(900); // the command view fades in
}

async function rail() {
  return page.evaluate(selector => {
    const root = document.querySelector(selector);
    // First row's top to last row's (or open body's) bottom: the rail's own
    // box also holds the launcher clearance, which is not rows.
    const panels = [...root.querySelectorAll('.ws-cmd-panel')].map(el =>
      el.getBoundingClientRect()
    );
    return {
      height: Math.round(panels[panels.length - 1].bottom - panels[0].top),
      rows: [...root.querySelectorAll('[data-cmd-manage-section]')].map(el => ({
        key: el.getAttribute('data-cmd-manage-section'),
        expanded: el.getAttribute('aria-expanded'),
        count: el.querySelector('.ws-cmd-panel-count')?.textContent?.trim(),
        height: Math.round(el.closest('.ws-cmd-panel-head').getBoundingClientRect().height)
      })),
      bodies: [...root.querySelectorAll('.ws-cmd-panel-body')].map(el => ({
        id: el.id,
        role: el.getAttribute('role'),
        labelledBy: el.getAttribute('aria-labelledby'),
        label: document.getElementById(el.getAttribute('aria-labelledby') || '')?.textContent
      })),
      focused: document.activeElement?.getAttribute('data-cmd-manage-section')
        ? `toggle:${document.activeElement.getAttribute('data-cmd-manage-section')}`
        : document.activeElement?.getAttribute('data-cmd-primary-section')
          ? `plus:${document.activeElement.getAttribute('data-cmd-primary-section')}`
          : document.activeElement?.tagName
    };
  }, RAIL);
}

async function closeAll() {
  const state = await rail();
  const open = state.rows.find(row => row.expanded === 'true');
  if (open) await page.locator(toggle(open.key)).click();
}

// ---------- stages ----------

const ORDER = [
  'backlog',
  'notes',
  'schedules',
  'sessions',
  'folders',
  'members',
  'files',
  'systems',
  'stations'
];

async function railStage() {
  const fx = await fixtures();

  for (const [name, ws, present] of [
    ['empty', fx.empty, ORDER.filter(key => key !== 'members' && key !== 'stations')],
    ['busy', fx.busy, ORDER.filter(key => key !== 'members' && key !== 'stations')],
    ['group', fx.group, ORDER.filter(key => key !== 'stations')],
    ['hq', fx.hq, ORDER.filter(key => key !== 'members')]
  ]) {
    console.log(`\n== ${name}: /workspaces/${ws.folder_slug}`);
    await openDetails(ws);
    await closeAll();
    let state = await rail();
    check(
      JSON.stringify(state.rows.map(row => row.key)) === JSON.stringify(present),
      `${name}: rows are ${present.join(', ')}`
    );
    check(state.bodies.length === 0, `${name}: closed, the rail has no body element`);
    check(
      state.rows.every(row => row.height >= 40 && row.height <= 44),
      `${name}: every row is 40-44px tall (${[...new Set(state.rows.map(row => row.height))].join('/')})`
    );
    console.log(
      `     collapsed rail: ${state.height}px for ${state.rows.length} rows · counts ${state.rows
        .map(row => `${row.key} ${row.count}`)
        .join(', ')}`
    );
    await page.locator(RAIL).scrollIntoViewIfNeeded();
    await shot(`${name}-01-collapsed`);

    // One open at a time, focus stays on the toggle, and a second click closes.
    for (const key of present) {
      await page.locator(toggle(key)).click();
      state = await rail();
      const open = state.rows.filter(row => row.expanded === 'true').map(row => row.key);
      check(
        open.length === 1 && open[0] === key && state.bodies.length === 1,
        `${name}: ${key} opens alone`
      );
      check(
        state.bodies[0].role === 'region' && Boolean(state.bodies[0].label),
        `${name}: ${key} body is a region named "${state.bodies[0].label}"`
      );
      check(state.focused === `toggle:${key}`, `${name}: focus is back on the ${key} toggle`);
      if (name === 'busy' && (key === 'notes' || key === 'folders')) {
        await page.waitForTimeout(300);
        await shot(`busy-02-${key}-open`);
      }
      if (name === 'group' && key === 'members') {
        await page.waitForTimeout(1200);
        await shot('group-02-detachment-open');
      }
      if (name === 'hq' && (key === 'stations' || key === 'systems')) {
        await page.waitForTimeout(1200);
        await shot(`hq-02-${key}-open`);
      }
    }
    await page.locator(toggle(present[present.length - 1])).click();
    state = await rail();
    check(state.bodies.length === 0, `${name}: clicking the open toggle closes it`);
  }

  await actionsOn(fx.busy);
  await keyboardOn(fx.busy);
  await groupActions(fx.group);
  await hqActions(fx.hq);
}

const openKey = async () => (await rail()).rows.find(row => row.expanded === 'true')?.key || '';

async function dismissModal(selector) {
  const modal = page.locator(selector);
  await modal.waitFor({ state: 'visible' });
  await page.waitForTimeout(350); // Bootstrap ignores a close during its show transition
  await modal.locator('[data-bs-dismiss="modal"], .btn-close').first().click();
  await modal.waitFor({ state: 'hidden' });
}

// Every "+" opens its section first and then acts; every body verb acts
// without touching which section is open.
async function actionsOn(ws) {
  console.log(`\n== actions: /workspaces/${ws.folder_slug}`);
  // The folder picker is a desktop dialog. Answer the request that would open
  // it, so the demo proves the button reaches it without launching one.
  let pickerRequests = 0;
  await page.route('**/api/launch-folder-picker', route => {
    pickerRequests += 1;
    return route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ success: true })
    });
  });
  await openDetails(ws);
  await closeAll();

  await page.locator(plus('notes')).click();
  await page.locator('#noteEditorModal').waitFor({ state: 'visible' });
  check((await openKey()) === 'notes', 'notes "+": Notes is open behind the new-note dialog');
  await shot('busy-03-notes-plus');
  await dismissModal('#noteEditorModal');

  await page.locator(plus('files')).click();
  await page.locator('#hubAddFileModal').waitFor({ state: 'visible' });
  check((await openKey()) === 'files', 'files "+": Files is open behind the upload dialog');
  await dismissModal('#hubAddFileModal');

  await page.locator(plus('folders')).click();
  await page.waitForTimeout(400);
  check(pickerRequests === 1, 'folders "+": asks for the folder picker');
  check((await openKey()) === 'folders', 'folders "+": Linked Folders is open');
  check(
    (await page.locator(plus('folders')).textContent()).trim() === '+' &&
      (await page.locator(plus('folders')).isEnabled()),
    'folders "+": the button is itself again once the picker request returns'
  );

  await page.locator(plus('schedules')).click();
  await page.locator('#scheduledTasksModal').waitFor({ state: 'visible' });
  check((await openKey()) === 'schedules', 'schedules "+": Schedules is open behind its dialog');
  await page.locator('#scheduledTasksModalClose').click();
  await page.locator('#scheduledTasksModal').waitFor({ state: 'hidden' });

  // Body verbs. While a section is open its row "+" and its verb are two
  // buttons under two attributes, so each selector still matches one element.
  for (const [key, expectation] of [
    ['notes', () => dismissModal('#noteEditorModal')],
    ['files', () => dismissModal('#hubAddFileModal')],
    [
      'folders',
      async () => {
        await page.waitForTimeout(400);
        check(pickerRequests === 2, 'folders verb: asks for the folder picker');
      }
    ]
  ]) {
    if ((await openKey()) !== key) await page.locator(toggle(key)).click();
    const verb = page.locator(`${RAIL} [data-cmd-section-verb="${key}"]`);
    check((await verb.count()) === 1, `${key}: one body verb ("${await verb.textContent()}")`);
    check((await page.locator(plus(key)).count()) === 1, `${key}: still exactly one row "+"`);
    await verb.click();
    await expectation();
    check((await openKey()) === key, `${key} verb: the section stays open`);
  }

  // Backlog: the "+" leaves for the Tickets create form and opens no row; the
  // body's shortcut goes to Tickets filtered to the backlog.
  await closeAll();
  await page.locator(plus('backlog')).click();
  await page.locator('[data-cmd-view-mode="tickets"][aria-pressed="true"]').waitFor();
  check(true, 'backlog "+": switches to Tickets');
  await page.locator('[data-cmd-view-mode="details"]').click();
  await page.locator(RAIL).waitFor({ state: 'visible' });
  check((await openKey()) !== 'backlog', 'backlog "+": did not open the Backlog row');
  if ((await openKey()) !== 'backlog') await page.locator(toggle('backlog')).click();
  await shot('busy-04-backlog-open');
  await page.locator(`${RAIL} [data-cmd-open-tickets="backlog"]`).click();
  await page.locator('[data-cmd-view-mode="tickets"][aria-pressed="true"]').waitFor();
  check(true, 'backlog "View in Tickets": switches to Tickets');
  await page.locator('[data-cmd-view-mode="details"]').click();
  await page.locator(RAIL).waitFor({ state: 'visible' });

  // Sessions last: it creates a real session and opens the chat.
  const before = (await rail()).rows.find(row => row.key === 'sessions').count;
  await page.locator(plus('sessions')).click();
  await page.waitForFunction(
    ([selector, count]) =>
      document.querySelector(`${selector} .ws-cmd-panel-count`)?.textContent?.trim() !== count,
    [`${RAIL} [data-cmd-manage-section="sessions"]`, before]
  );
  check((await openKey()) === 'sessions', 'sessions "+": Sessions is open with the new session');
  await page.unroute('**/api/launch-folder-picker');
}

async function keyboardOn(ws) {
  console.log(`\n== keyboard: /workspaces/${ws.folder_slug}`);
  await openDetails(ws);
  await closeAll();

  // Opening a row rebuilds the view. That must not replay the view's entrance
  // fade, or every click blinks the whole page.
  await page.locator(toggle('files')).click();
  const replayed = await page.evaluate(
    () =>
      [
        ...document.querySelectorAll(
          '.ws-cmd-topbar, .ws-cmd-mission, .ws-cmd-garrison, .ws-cmd-rail'
        )
      ]
        .flatMap(el => el.getAnimations())
        .filter(animation => animation.playState === 'running').length
  );
  check(replayed === 0, `toggling a row replays no entrance animation (${replayed} running)`);
  await closeAll();
  await page.locator(toggle('backlog')).focus();
  await page.keyboard.press('Tab');
  check((await rail()).focused === 'BUTTON', 'Tab from the Backlog toggle lands on its "+"');
  await page.keyboard.press('Tab');
  check((await rail()).focused === 'toggle:notes', 'Tab again lands on the Notes toggle');
  await page.keyboard.press('Enter');
  let state = await rail();
  check(
    (await openKey()) === 'notes' && state.focused === 'toggle:notes',
    'Enter opens Notes and focus stays on its toggle'
  );
  await page.keyboard.press('Space');
  state = await rail();
  check(
    state.bodies.length === 0 && state.focused === 'toggle:notes',
    'Space closes it, focus still on the toggle'
  );
  await page.keyboard.press('Space');
  await page.keyboard.press('Tab');
  check((await rail()).focused === 'plus:notes', 'open: Tab goes to the row "+"');
  await page.keyboard.press('Tab');
  const inBody = await page.evaluate(
    () => document.activeElement?.getAttribute('data-cmd-section-verb') || ''
  );
  check(inBody === 'notes', 'then into the body, starting at the New Note verb');
  await shot('busy-05-keyboard-focus-in-body');
  await page.keyboard.press('Escape');
  state = await rail();
  check(
    state.bodies.length === 0 && state.focused === 'toggle:notes',
    'Escape closes Notes and returns focus to its toggle'
  );
}

async function groupActions(ws) {
  console.log(`\n== group: /workspaces/${ws.folder_slug}`);
  await openDetails(ws);
  await closeAll();
  await page.locator(plus('members')).click();
  check((await openKey()) === 'members', 'detachment "+": Detachment is open');
  await page.locator(`${RAIL} [data-cmd-members-host] .workspace-detail-panel-members`).waitFor();
  check(true, 'detachment body mounts the shared members panel');
  check(
    (await page.locator(`${RAIL} [data-cmd-section-verb="members"]`).count()) === 1,
    'detachment body has the Add Member verb'
  );
  await page.waitForTimeout(600);
  await shot('group-03-detachment-plus');
}

async function hqActions(ws) {
  console.log(`\n== hq: /workspaces/${ws.folder_slug}`);
  await openDetails(ws);
  if ((await openKey()) !== 'stations') await page.locator(toggle('stations')).click();
  const row = page.locator(
    `${RAIL} .ws-cmd-panel.is-hq-stations [data-cmd-hq-station="daily-brief"]`
  );
  await row.waitFor({ state: 'visible' });
  await row.click();
  await page.locator('.ws-cmd-modal-panel.is-daily-brief').waitFor({ state: 'visible' });
  check(true, 'a station row opens its panel');
  await page.getByRole('button', { name: 'Close Daily Brief' }).click();
  await page.locator('.ws-cmd-modal-panel.is-daily-brief').waitFor({ state: 'hidden' });

  // Systems widens the layout while it is the open section.
  const railWidth = () =>
    page.evaluate(selector => Math.round(document.querySelector(selector).clientWidth), RAIL);
  const narrow = await railWidth();
  await page.locator(toggle('systems')).click();
  await page.locator(`${RAIL} [data-cmd-system-host]`).waitFor();
  const wide = await railWidth();
  check(wide >= 520 && wide > narrow, `systems open widens the rail (${narrow}px -> ${wide}px)`);
  await page.locator(`${RAIL} [data-cmd-system-tab="triggers"]`).click();
  check((await openKey()) === 'systems', 'switching a Systems tab keeps Systems open');
  await page.keyboard.press('Escape');
  check((await railWidth()) === narrow, 'closing Systems returns the layout');
}

// Where the fixed launcher pills sit, and whether anything in the rail is
// under them once the page is scrolled as far as it goes.
async function launcherOverlap() {
  await page.evaluate(() => window.scrollTo(0, document.documentElement.scrollHeight));
  await page.waitForTimeout(250);
  return page.evaluate(selector => {
    const viewport = { width: window.innerWidth, height: window.innerHeight };
    const pills = [...document.querySelectorAll('#oriGuideRoot > *')]
      .map(el => el.getBoundingClientRect())
      .filter(box => box.width > 0 && box.height > 0);
    const stackTop = pills.length ? Math.min(...pills.map(box => box.top)) : viewport.height;
    const covered = [];
    for (const el of document.querySelectorAll(
      `${selector} .ws-cmd-panel-head, ${selector} .ws-cmd-panel-body`
    )) {
      const box = el.getBoundingClientRect();
      if (
        pills.some(
          pill =>
            box.left < pill.right &&
            box.right > pill.left &&
            box.top < pill.bottom &&
            box.bottom > pill.top
        )
      ) {
        covered.push(el.className + ' ' + (el.id || el.textContent.trim().slice(0, 24)));
      }
    }
    const railBox = document.querySelector(selector).getBoundingClientRect();
    const content = [
      ...document.querySelectorAll(`${selector} .ws-cmd-panel-head, ${selector} .ws-cmd-panel-body`)
    ].map(el => el.getBoundingClientRect().bottom);
    return {
      viewport,
      pills: pills.map(box => ({
        left: Math.round(box.left),
        top: Math.round(box.top),
        width: Math.round(box.width),
        height: Math.round(box.height)
      })),
      launcherHeightFromFloor: Math.round(viewport.height - stackTop),
      lastRailContentBottom: Math.round(Math.max(...content)),
      railBottom: Math.round(railBox.bottom),
      covered
    };
  }, RAIL);
}

async function measureStage() {
  const fx = await fixtures();
  for (const size of [
    { width: 1280, height: 800 },
    { width: 768, height: 1024 },
    { width: 390, height: 844 }
  ]) {
    await page.setViewportSize(size);
    await openDetails(fx.busy);
    console.log(`\n== ${size.width}x${size.height}`);
    console.log(JSON.stringify(await launcherOverlap(), null, 1));
  }
}

// ---------- remember ----------

const railKey = ws => `ori:command-rail-section:${ws.id}`;
const remembered = ws => page.evaluate(key => localStorage.getItem(key), railKey(ws));

// Every open section the rail showed while the page loaded, in order. The
// default is chosen once, so a load never passes through a section it then
// leaves: the log is at most "nothing, then the one that was chosen".
async function watchOpenSections() {
  await page.addInitScript(() => {
    window.__railOpenLog = [];
    new MutationObserver(() => {
      const rail = document.querySelector('#workspaceCommandView .ws-cmd-rail');
      if (!rail) return;
      const open = rail.querySelector('[data-cmd-manage-section][aria-expanded="true"]');
      const key = open ? open.getAttribute('data-cmd-manage-section') : '';
      const log = window.__railOpenLog;
      if (log[log.length - 1] !== key) log.push(key);
      // The document itself: an init script runs before there is an <html>.
    }).observe(document, { childList: true, subtree: true });
  });
}

async function listsLoaded() {
  await page.locator(RAIL).waitFor({ state: 'visible' });
  await page.waitForFunction(() => window.workspaceDetail?.initialListsLoaded === true);
  await applyTheme();
  await page.waitForTimeout(400);
}

async function arrive(ws) {
  await openDetails(ws);
  await listsLoaded();
  return page.evaluate(() => window.__railOpenLog.join(' > '));
}

async function reload() {
  await page.reload();
  await listsLoaded();
  return page.evaluate(() => window.__railOpenLog.join(' > '));
}

async function rememberStage() {
  const fx = await fixtures();
  await watchOpenSections();

  console.log('\n== nothing remembered: the default');
  let log = await arrive(fx.busy);
  check((await remembered(fx.busy)) === null, 'busy: a fresh browser remembers nothing');
  check((await openKey()) === 'notes', 'busy: the first section with content, Notes, is open');
  check(log === ' > notes', `busy: chosen once while loading (${log || 'nothing'})`);
  await page.locator(RAIL).scrollIntoViewIfNeeded();
  await shot('01-default-first-with-content');

  log = await arrive(fx.empty);
  check(
    (await openKey()) === '' && log === '',
    'empty: only its own project folder, so nothing opens'
  );
  await shot('02-default-nothing');

  log = await arrive(fx.hq);
  check((await openKey()) === 'stations', `hq: no content, so Stations is open (${log})`);
  await page.locator(toggle('stations')).scrollIntoViewIfNeeded();
  await shot('03-default-hq-stations');

  console.log('\n== remembered, per workspace');
  await arrive(fx.busy);
  await page.locator(toggle('files')).click();
  check((await remembered(fx.busy)) === 'files', 'busy: opening Files remembers "files"');
  log = await reload();
  check((await openKey()) === 'files', 'busy: Files is open after a reload');
  check(log === 'files', `busy: opened at once, not after the lists (${log})`);
  await page.locator(toggle('files')).scrollIntoViewIfNeeded();
  await shot('04-reload-files');

  await arrive(fx.empty);
  check((await openKey()) === '', 'empty: has its own memory, still nothing open');
  await page.locator(toggle('sessions')).click();
  await reload();
  check((await openKey()) === 'sessions', 'empty: Sessions is open after a reload');
  await arrive(fx.busy);
  check((await openKey()) === 'files', 'busy: still Files');

  console.log('\n== what does not change the memory');
  await page.locator(plus('notes')).click();
  await dismissModal('#noteEditorModal');
  check((await openKey()) === 'notes', 'busy: the Notes "+" opened Notes');
  check((await remembered(fx.busy)) === 'files', 'busy: but "files" is still what is remembered');
  await page.keyboard.press('Escape');
  check((await openKey()) === '', 'busy: Escape closed it');
  check((await remembered(fx.busy)) === 'files', 'busy: and "files" is still remembered');
  await reload();
  check((await openKey()) === 'files', 'busy: a reload is back on Files');

  console.log('\n== returning to Details');
  await page.locator(toggle('sessions')).click();
  await page.locator('[data-cmd-view-mode="tickets"]').click();
  await page.locator('[data-cmd-view-mode="tickets"][aria-pressed="true"]').waitFor();
  await page.locator('[data-cmd-view-mode="details"]').click();
  await page.locator(RAIL).waitFor({ state: 'visible' });
  check((await openKey()) === 'sessions', 'busy: back from Tickets on the remembered section');

  console.log('\n== closed is remembered too; forgetting falls back to the default');
  await page.locator(toggle('sessions')).click();
  check((await remembered(fx.busy)) === '', 'busy: closing remembers "closed"');
  log = await reload();
  check((await openKey()) === '' && log === '', 'busy: nothing is open after a reload');
  await page.evaluate(key => localStorage.removeItem(key), railKey(fx.busy));
  await reload();
  check((await openKey()) === 'notes', 'busy: with the key removed, Notes opens again');
}

const stages = { rail: railStage, remember: rememberStage, measure: measureStage };
if (!stages[stage]) throw new Error(`unknown stage: ${stage}`);

try {
  await stages[stage]();
} finally {
  if (problems.length) {
    console.log('\nPage problems:');
    for (const problem of [...new Set(problems)]) console.log(`  ${problem}`);
  }
  await browser.close();
}
