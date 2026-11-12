import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

import {
  chooseFolder,
  folderAgentActionPath,
  folderAgentCardView,
  folderAgentHireChoice,
  folderAgentLineTime,
  folderAgentNeedsView,
  folderAgentReceiptRows,
  followFolderAgent,
  notifyPlacement,
  placementLinesDone,
  formatFolderAgentClock,
  formatFolderAgentDuration,
  tickFolderAgentClocks,
  folderAgentReportView,
  folderAgentSetUpLabel,
  folderAgentTileLine,
  mountWorkspaceFolderAgent,
  mountFolderAgentChoice,
  mountFolderAgentReports,
  openFolderAgentPlace,
  folderAgentDestination,
  postFolderAgentAction,
  renderFolderAgentCard,
  renderFolderAgentReport,
  setupFinished,
  startFolderAgent
} from './folder-agent-card.js';

// A stub DOM just big enough for the renderers.
function stubDocument() {
  const make = tag => {
    const node = {
      tagName: String(tag).toUpperCase(),
      className: '',
      textContent: '',
      children: [],
      parentElement: null,
      dataset: {},
      attributes: {},
      listeners: {},
      hidden: false,
      disabled: false,
      value: '',
      setAttribute(name, value) {
        node.attributes[name] = value;
      },
      addEventListener(type, fn) {
        node.listeners[type] = fn;
      },
      append(...kids) {
        kids.forEach(kid => {
          kid.parentElement = node;
          node.children.push(kid);
        });
      },
      replaceChildren(...kids) {
        node.children = [];
        node.append(...kids);
      },
      replaceWith(other) {
        const siblings = node.parentElement?.children;
        if (!siblings) return;
        other.parentElement = node.parentElement;
        siblings.splice(siblings.indexOf(node), 1, other);
      },
      all() {
        return node.children.flatMap(child => [child, ...child.all()]);
      },
      querySelectorAll(selector) {
        return node.all().filter(child => matches(child, selector));
      },
      querySelector(selector) {
        return node.querySelectorAll(selector)[0] || null;
      },
      closest(selector) {
        for (let at = node; at; at = at.parentElement) if (matches(at, selector)) return at;
        return null;
      }
    };
    return node;
  };
  const matches = (node, selector) =>
    selector.startsWith('.')
      ? node.className.split(' ').includes(selector.slice(1))
      : node.tagName === selector.toUpperCase();
  return { createElement: make };
}

const text = (node, selector) => node.querySelector(selector)?.textContent ?? null;
const plain = value => JSON.parse(JSON.stringify(value));

const reported = (overrides = {}) => ({
  workspace_id: 'ws-1',
  workspace_name: 'Ori Music Demo',
  route: '/workspaces/ori-music-demo',
  folder_name: 'Ori Music Demo',
  agent_name: 'Ori Music Demo Manager',
  state: 'reported',
  facts_line: '7 REAPER songs · 1 Logic Pro song · 2 folders with songs · 1 empty folder',
  stage_line: 'Set it up: creates your Music Production Home, installs REAPER tools',
  stage: 'home',
  candidates: [
    {
      id: 'search:x',
      name: 'Mix helper',
      source: 'search',
      marketplace: 'Skills',
      action: 'add',
      why: 'Ranked.'
    },
    {
      id: 'home:music_project_management',
      name: 'Music Production Home',
      required: true,
      action: 'install',
      detail: 'creates your Music Production Home'
    },
    {
      id: 'integration:ori_reaper',
      name: 'REAPER tools',
      required: true,
      action: 'already_here',
      detail: 'already here'
    }
  ],
  model_sentence:
    'No model is set up, so Ori Music Demo Manager could not look itself. This is what Ori found.',
  tools_sentence: 'Search for more tools needs a model',
  actions: ['set_up', 'keep', 'look_further'],
  plan: {
    digest: 'd1',
    runnable: false,
    unavailable: 'Joining a Home from a folder is not available yet.',
    lines: []
  },
  ...overrides
});

test('the card before Start reads the server card and refuses one without a token', () => {
  assert.equal(folderAgentCardView({ folder_name: 'x' }), null);
  const view = folderAgentCardView({
    choice_token: 'fa-1',
    folder_name: 'Ori Music Demo',
    title: 'Start an agent on Ori Music Demo',
    field: 'What should it do?',
    brief: "Find out what's here and set it up",
    brief_max: 300,
    action: 'Hire & start',
    note: 'Nothing is created until you hire.',
    hire: {
      agent_name: 'Ori Music Demo Manager',
      mandate: '',
      provider: 'claude',
      model: 'claude-sonnet-5-5',
      models: [
        { provider: 'claude', model: 'claude-sonnet-5-5', label: 'claude-sonnet-5-5 (claude)' },
        { provider: 'openai', model: 'gpt-x', label: 'gpt-x (openai)' }
      ],
      agents: [{ name: 'Atlas', model: 'claude-sonnet-5-5' }],
      name_max: 60
    }
  });
  assert.deepEqual(plain(view), {
    token: 'fa-1',
    folder: 'Ori Music Demo',
    title: 'Start an agent on Ori Music Demo',
    field: 'What should it do?',
    brief: "Find out what's here and set it up",
    max: 300,
    action: 'Hire & start',
    note: 'Nothing is created until you hire.',
    hire: {
      name: 'Ori Music Demo Manager',
      nameMax: 60,
      mandate: '',
      provider: 'claude',
      model: 'claude-sonnet-5-5',
      models: [
        { provider: 'claude', model: 'claude-sonnet-5-5', label: 'claude-sonnet-5-5 (claude)' },
        { provider: 'openai', model: 'gpt-x', label: 'gpt-x (openai)' }
      ],
      agents: [{ name: 'Atlas', model: 'claude-sonnet-5-5' }],
      existing: '',
      mode: 'new'
    }
  });
  const doc = stubDocument();
  let started = null;
  const card = renderFolderAgentCard(view, doc, {
    onStart: (brief, _button, hire) => (started = { brief, hire })
  });
  assert.equal(text(card, '.folder-agent-card__title'), 'Start an agent on Ori Music Demo');
  assert.equal(text(card, '.folder-agent-card__start'), 'Hire & start');
  assert.equal(text(card, '.folder-agent-card__hire-title'), 'Hire');
  const input = card.querySelector('.folder-agent-card__brief');
  assert.equal(input.maxLength, 300);
  input.value = 'Mix this song';
  // A new agent by default: rename it, pick another model, give it a role.
  const name = card.querySelector('.folder-agent-card__hire-name');
  assert.equal(name.value, 'Ori Music Demo Manager');
  assert.equal(card.querySelector('.folder-agent-card__hire-agent').disabled, true);
  name.value = 'Song Keeper';
  name.listeners.input();
  const model = card.querySelector('.folder-agent-card__hire-model');
  model.value = 'openai\u0000gpt-x';
  model.listeners.change();
  const role = card.querySelector('.folder-agent-card__hire-mandate');
  role.value = 'Keep my songs moving';
  role.listeners.input();
  card.querySelector('.folder-agent-card__start').listeners.click();
  assert.deepEqual(plain(started), {
    brief: 'Mix this song',
    hire: {
      name: 'Song Keeper',
      mandate: 'Keep my songs moving',
      provider: 'openai',
      model: 'gpt-x'
    }
  });
  // One of your agents instead: only its name is sent.
  const reuse = card
    .querySelectorAll('input')
    .find(node => node.dataset.folderAgentHire === 'existing');
  reuse.checked = true;
  reuse.listeners.change();
  assert.equal(name.disabled, true);
  assert.equal(card.querySelector('.folder-agent-card__hire-agent').disabled, false);
  card.querySelector('.folder-agent-card__start').listeners.click();
  assert.deepEqual(plain(started.hire), { existing: 'Atlas' });
});

test('a saved manager is preselected, and no model still lets the agent be hired', () => {
  const view = folderAgentCardView({
    choice_token: 'fa-2',
    folder_name: 'Songs',
    hire: {
      agent_name: 'Songs Manager',
      models: [],
      agents: [{ name: 'Songs Manager' }],
      existing: 'Songs Manager'
    }
  });
  assert.equal(view.hire.mode, 'existing');
  assert.deepEqual(plain(folderAgentHireChoice(view.hire)), { existing: 'Songs Manager' });
  const card = renderFolderAgentCard(view, stubDocument(), {});
  assert.equal(card.querySelector('.folder-agent-card__hire-model'), null);
  assert.match(text(card, '.folder-agent-card__hire-note'), /No model is set up yet/);
  view.hire.mode = 'new';
  assert.deepEqual(plain(folderAgentHireChoice(view.hire)), { name: 'Songs Manager', mandate: '' });

  // Models but no system model: "No model yet" is preselected.
  const unset = folderAgentCardView({
    choice_token: 'fa-3',
    folder_name: 'Songs',
    hire: { models: [{ provider: 'claude_code', model: 'sonnet', label: 'sonnet (claude_code)' }] }
  });
  const picker = renderFolderAgentCard(unset, stubDocument(), {}).querySelector(
    '.folder-agent-card__hire-model'
  );
  assert.equal(picker.children[0].textContent, 'No model yet');
  assert.equal(unset.hire.model, '');
  assert.equal(folderAgentHireChoice(unset.hire).model, undefined);
});

test('the receipt shows the progress line while the agent looks', () => {
  const view = folderAgentReportView({
    workspace_id: 'ws-1',
    workspace_name: 'Ori Music Demo',
    folder_name: 'Ori Music Demo',
    agent_name: 'Ori Music Demo Manager',
    state: 'looking',
    receipt: 'Looking around…',
    progress_line: 'Looking around · 240 entries · 3 folders left'
  });
  assert.equal(view.title, 'Ori Music Demo Manager is looking around Ori Music Demo');
  const card = renderFolderAgentReport(view, stubDocument());
  assert.equal(
    text(card, '.folder-agent-card__receipt'),
    'Looking around · 240 entries · 3 folders left'
  );
  assert.equal(card.querySelector('button'), null);
  assert.equal(
    folderAgentTileLine({ state: 'looking', progress_line: 'Looking around · 2 entries' }),
    'Looking around · 2 entries'
  );
  assert.equal(folderAgentTileLine({ state: 'setting_up' }), 'Setting up…');
  assert.equal(folderAgentTileLine({ state: 'reported' }), '');
});

test('the report lists required tools first, labels search hits and greys what is already here', () => {
  const view = folderAgentReportView(reported());
  assert.deepEqual(
    view.tools.map(tool => [tool.name, tool.required, tool.greyed, tool.label]),
    [
      ['Music Production Home', true, false, ''],
      ['REAPER tools', true, true, ''],
      ['Mix helper', false, false, 'Found by search · Skills']
    ]
  );
  // A plan that cannot run yet drops Set it up and says why.
  assert.deepEqual(
    view.actions.map(a => a.id),
    ['keep', 'look_further']
  );
  const card = renderFolderAgentReport(view, stubDocument());
  assert.equal(text(card, '.folder-agent-card__facts'), reported().facts_line);
  assert.equal(text(card, '.folder-agent-card__stage'), reported().stage_line);
  assert.equal(text(card, '.folder-agent-card__degraded'), reported().model_sentence);
  assert.equal(
    card
      .querySelectorAll('.folder-agent-card__note')
      .map(n => n.textContent)
      .at(-1),
    'Joining a Home from a folder is not available yet.'
  );
  assert.deepEqual(
    card.querySelectorAll('button').map(b => b.textContent),
    ['Keep the folder as it is', 'Look further']
  );
});

test('a runnable plan shows its lines and Set it up first', () => {
  const view = folderAgentReportView(
    reported({
      stage: 'tools',
      plan: {
        digest: 'd2',
        runnable: true,
        lines: [
          {
            kind: 'integration',
            name: 'Installs REAPER tools',
            detail: 'REAPER is installed on this Mac.'
          },
          { kind: 'folder', name: 'Nothing is moved, copied or read' }
        ]
      }
    })
  );
  assert.deepEqual(
    view.actions.map(a => a.label),
    ['Set it up', 'Keep the folder as it is', 'Look further']
  );
  const card = renderFolderAgentReport(view, stubDocument());
  assert.deepEqual(
    card.querySelectorAll('.folder-agent-card__line-name').map(n => n.textContent),
    ['Installs REAPER tools', 'Nothing is moved, copied or read']
  );
});

test('a setup run shows its line states and a stop says Set it up carries on', () => {
  const view = folderAgentReportView(
    reported({
      state: 'set_up',
      actions: ['set_up', 'look_further'],
      plan: { digest: 'd2', runnable: true, lines: [] },
      setup: {
        status: 'stopped',
        lines: [
          { name: 'Installs REAPER tools', state: 'done' },
          { name: 'Connects REAPER tools to Song', state: 'failed' }
        ]
      }
    })
  );
  const card = renderFolderAgentReport(view, stubDocument());
  assert.deepEqual(
    card.querySelectorAll('.folder-agent-card__line-state').map(n => n.textContent),
    ['Done', 'Did not finish']
  );
  assert.equal(
    text(card, '.folder-agent-card__failure'),
    'A step did not finish. Set it up again carries on from there.'
  );
});

test('actions post to the workspace path and Set it up carries the digest it showed', async () => {
  assert.equal(
    folderAgentActionPath('ws 1', 'look_further'),
    '/api/personal-assistant/folder-agent/ws%201/look-further'
  );
  assert.equal(folderAgentActionPath('ws-1', 'delete'), '');
  const calls = [];
  const fetchImpl = async (url, init) => {
    calls.push([url, JSON.parse(init.body)]);
    return {
      ok: true,
      status: 200,
      json: async () => ({ folder_agent: { workspace_id: 'ws-1', state: 'setting_up' } })
    };
  };
  const view = folderAgentReportView(
    reported({ plan: { digest: 'abc', runnable: true, lines: [] } })
  );
  const result = await postFolderAgentAction(view, 'set_up', fetchImpl);
  assert.deepEqual(calls[0], [
    '/api/personal-assistant/folder-agent/ws-1/setup',
    { plan_digest: 'abc' }
  ]);
  assert.equal(result.view.state, 'setting_up');
});

test('choose and start send tokens, never paths, and surface refusals', async () => {
  const calls = [];
  const respond = payload => async (url, init) => {
    calls.push([url, JSON.parse(init.body)]);
    return { ok: !payload.error, status: payload.error ? 409 : 200, json: async () => payload };
  };
  assert.deepEqual(await chooseFolder({ picker: true }, respond({ cancelled: true })), {
    cancelled: true
  });
  assert.deepEqual(calls[0], ['/api/personal-assistant/folder-agent/choose', { picker: true }]);
  const refused = await startFolderAgent(
    'fa-1',
    'brief',
    respond({
      error: 'This folder already belongs to the workspace "Songs".',
      duplicate: { name: 'Songs' }
    })
  );
  assert.equal(refused.duplicate.name, 'Songs');
  assert.deepEqual(calls[1], [
    '/api/personal-assistant/folder-agent/start',
    { choice_token: 'fa-1', brief: 'brief' }
  ]);
});

test('Needs you mounts only folder agent reports and redraws an error in place', async () => {
  assert.equal(folderAgentNeedsView({ kind: 'folder_first_look' }), null);
  const doc = stubDocument();
  const host = doc.createElement('div');
  let done = 0;
  const items = [
    { kind: 'folder_agent_look', folder_agent: reported({ state: 'looking' }) },
    { kind: 'folder_agent_report', folder_agent: reported() }
  ];
  const fetchImpl = async () => ({
    ok: false,
    status: 409,
    json: async () => ({ error: 'The report has moved on.' })
  });
  assert.equal(mountFolderAgentReports(host, items, { doc, fetchImpl, onDone: () => done++ }), 1);
  const keep = host.querySelectorAll('button').find(b => b.dataset.folderAgentAction === 'keep');
  await keep.listeners.click();
  assert.equal(text(host, '.folder-agent-card__error'), 'The report has moved on.');
  assert.equal(done, 0);
});

// The report card is the steps and the actions: no "After setup", no goal
// input, no first-ticket preview, and Set it up sends only the steps
// (folder-setup-ends-in-home FR14).
test('the report card shows what it is, the steps and its actions; Set it up sends only the steps', () => {
  const view = folderAgentReportView(
    reported({
      what_it_is: 'Seven songs and a sketch across two album folders.',
      suggested_goal: 'Keep these songs moving toward release.',
      first_ticket: 'List the five songs saved most recently.',
      first_ticket_why: 'Seeing what moved last shows where to start.',
      left_out: { suggested_goal: 'left out', suggested_first_ticket: 'left out' },
      model_sentence: '',
      tools_sentence: '',
      plan: {
        digest: 'd9',
        runnable: true,
        lines: [{ kind: 'folder', name: 'Nothing is moved, copied or read' }]
      }
    })
  );
  assert.equal(view.goalOffer, null, 'no goal offer on the report card');
  const calls = [];
  const card = renderFolderAgentReport(view, stubDocument(), {
    onAction: (action, _button, extra) => calls.push([action, extra])
  });
  assert.equal(text(card, '.folder-agent-card__what'), view.whatItIs);
  for (const gone of [
    '.folder-agent-card__after',
    '.folder-agent-card__goal-input',
    '.folder-agent-card__goal-offer',
    '.folder-agent-card__ticket-title',
    '.folder-agent-card__degraded'
  ]) {
    assert.equal(card.querySelector(gone), null, gone);
  }
  assert.equal(card.querySelectorAll('input').filter(i => i.type === 'text').length, 0);
  card
    .querySelectorAll('button')
    .find(b => b.dataset.folderAgentAction === 'set_up')
    .listeners.click();
  assert.deepEqual(plain(calls[0]), ['set_up', { selected: [] }]);
});

// After the run, the receipt keeps "First ticket" and Start this (FR20), and
// the Commander offers the goal: Use this goal, Edit (Save, Cancel), Not now
// (FR17, FR18).
test('a done run offers the goal: use it, edit and save it, or not now', async () => {
  const view = folderAgentReportView(
    setUp({
      goal_offer: { goal: 'Keep these songs moving toward release.', max_runes: 300 },
      next_ticket: { id: 't1', title: 'List the five songs saved most recently.', started: false },
      actions: ['start_next', 'look_further']
    })
  );
  assert.deepEqual(view.goalOffer, {
    goal: 'Keep these songs moving toward release.',
    maxRunes: 300,
    // The first look's goal: no basis is named.
    basis: ''
  });
  const calls = [];
  const card = renderFolderAgentReport(view, stubDocument(), {
    onAction: (action, _button, extra) => calls.push([action, extra])
  });
  assert.equal(
    text(card, '.folder-agent-card__next-ticket'),
    'First ticket: List the five songs saved most recently.'
  );
  assert.ok(
    card.querySelectorAll('button').find(b => b.dataset.folderAgentAction === 'start_next')
  );
  const offer = card.querySelector('.folder-agent-card__goal-offer');
  assert.equal(
    text(offer, '.folder-agent-card__goal-text'),
    'Keep these songs moving toward release.'
  );
  const pick = id => offer.querySelectorAll('button').find(b => b.dataset.folderAgentAction === id);
  assert.deepEqual(
    offer.querySelectorAll('button').map(b => b.textContent),
    ['Use this goal', 'Edit', 'Not now']
  );
  pick('use_goal').listeners.click();
  pick('dismiss_goal').listeners.click();
  assert.deepEqual(plain(calls), [
    ['use_goal', {}],
    ['dismiss_goal', {}]
  ]);
  // Edit: a one-line input with the sentence, Save and Cancel.
  pick('edit_goal').listeners.click();
  const input = offer.querySelector('.folder-agent-card__goal-input');
  assert.equal(input.value, 'Keep these songs moving toward release.');
  assert.equal(input.maxLength, 300);
  assert.deepEqual(
    offer.querySelectorAll('button').map(b => b.textContent),
    ['Save', 'Cancel']
  );
  input.value = '  Finish two songs this month ';
  pick('use_goal').listeners.click();
  assert.deepEqual(plain(calls[2]), ['use_goal', { goal: 'Finish two songs this month' }]);
  pick('cancel_goal').listeners.click();
  assert.equal(offer.querySelector('.folder-agent-card__goal-input'), null);
  assert.ok(pick('edit_goal'), 'Cancel brings the offer back');

  // The answers post to the goal endpoint.
  const posts = [];
  const fetchImpl = async (url, init) => {
    posts.push([url, JSON.parse(init.body)]);
    return okJSON({ folder_agent: null });
  };
  await postFolderAgentAction(view, 'use_goal', fetchImpl, { goal: 'Finish it' });
  await postFolderAgentAction(view, 'use_goal', fetchImpl, {});
  await postFolderAgentAction(view, 'dismiss_goal', fetchImpl, {});
  assert.deepEqual(posts, [
    ['/api/personal-assistant/folder-agent/ws-1/goal', { answer: 'use', goal: 'Finish it' }],
    ['/api/personal-assistant/folder-agent/ws-1/goal', { answer: 'use' }],
    ['/api/personal-assistant/folder-agent/ws-1/goal', { answer: 'dismiss' }]
  ]);
});

test('every offered step has a box; unticking one refreshes the plan and Set it up carries the handles', async () => {
  const view = folderAgentReportView(
    reported({
      model_sentence: '',
      tools_sentence: '',
      candidates: [
        { id: 'integration:x', name: 'X tools', required: true, action: 'install' },
        {
          id: 'skill:a@b',
          name: 'mix-notes',
          source: 'search',
          marketplace: 'skills.sh',
          ranked: true,
          action: 'add'
        }
      ],
      plan: {
        digest: 'with-skill',
        runnable: true,
        source: 'default',
        selected: ['mix-notes', 'x-tools'],
        steps: [
          {
            handle: 'x-tools',
            kind: 'integration',
            title: 'Install X tools and connect them to Ori Music Demo',
            why: 'Lets the agent work with the songs here.',
            chosen: true
          },
          {
            handle: 'mix-notes',
            kind: 'add_on',
            title: 'Add the skill mix-notes',
            label: 'Found by search · skills.sh',
            chosen: true
          }
        ],
        lines: [
          { kind: 'integration', name: 'Installs X tools', step: 'x-tools' },
          { kind: 'workspace', name: 'Connects X tools to Ori Music Demo', step: 'x-tools' },
          { kind: 'add_on', name: 'Adds skill mix-notes · found by search', step: 'mix-notes' },
          { kind: 'folder', name: 'Nothing is moved, copied or read' }
        ]
      }
    })
  );
  const requests = [];
  const fetchImpl = async (url, init) => {
    requests.push([url, JSON.parse(init.body)]);
    return {
      ok: true,
      status: 200,
      json: async () => ({
        plan: {
          digest: 'without',
          runnable: true,
          source: 'default',
          selected: ['x-tools'],
          steps: [
            {
              handle: 'x-tools',
              title: 'Install X tools and connect them to Ori Music Demo',
              chosen: true
            },
            { handle: 'mix-notes', title: 'Add the skill mix-notes', chosen: false }
          ],
          lines: [
            { kind: 'integration', name: 'Installs X tools', step: 'x-tools' },
            { kind: 'workspace', name: 'Connects X tools to Ori Music Demo', step: 'x-tools' },
            { kind: 'folder', name: 'Nothing is moved, copied or read' }
          ]
        }
      })
    };
  };
  const calls = [];
  const card = renderFolderAgentReport(view, stubDocument(), {
    fetchImpl,
    onAction: (action, _button, extra) => calls.push([action, extra])
  });
  assert.equal(text(card, '.folder-agent-card__heading'), 'Steps');
  const boxes = card.querySelectorAll('input').filter(box => box.dataset.stepHandle);
  assert.deepEqual(
    boxes.map(b => [b.dataset.stepHandle, b.checked, b.disabled]),
    [
      ['x-tools', true, false],
      ['mix-notes', true, false]
    ]
  );
  assert.equal(text(card, '.folder-agent-card__tool-label'), 'Found by search · skills.sh');
  boxes[1].checked = false;
  await boxes[1].listeners.change();
  assert.deepEqual(requests[0], [
    '/api/personal-assistant/folder-agent/ws-1/plan',
    { selected: ['x-tools'] }
  ]);
  // One list: the chosen step with its actions under it, the unticked step
  // after it, the assurance as a note.
  assert.deepEqual(
    card.querySelectorAll('.folder-agent-card__line-name').map(n => n.textContent),
    [
      'Install X tools and connect them to Ori Music Demo',
      'Add the skill mix-notes',
      'Nothing is moved, copied or read'
    ]
  );
  assert.deepEqual(
    card.querySelectorAll('.folder-agent-card__action-name').map(n => n.textContent),
    ['Installs X tools', 'Connects X tools to Ori Music Demo']
  );
  // The redrawn list follows the server's answer.
  const redrawn = card.querySelectorAll('input').filter(box => box.dataset.stepHandle);
  assert.deepEqual(
    redrawn.map(b => [b.dataset.stepHandle, b.checked]),
    [
      ['x-tools', true],
      ['mix-notes', false]
    ]
  );
  card
    .querySelectorAll('button')
    .find(b => b.dataset.folderAgentAction === 'set_up')
    .listeners.click();
  assert.deepEqual(plain(calls[0]), ['set_up', { selected: ['x-tools'] }]);
  assert.equal(view.plan.digest, 'without');
});

test('the plan reads as numbered steps, the run says where it stands, the assurance is a note', () => {
  const planned = folderAgentReportView(
    reported({
      plan: {
        digest: 'p',
        runnable: true,
        lines: [
          { kind: 'library', name: 'Adds Albums to your Music Production Home' },
          {
            kind: 'songs',
            name: 'Lists 6 songs on its shelf',
            detail: '6 songs listed on the shelf'
          },
          { kind: 'folder', name: 'Nothing is moved, copied or read' }
        ]
      }
    })
  );
  const run = renderFolderAgentReport(planned, stubDocument(), {}).querySelector(
    '.folder-agent-card__run'
  );
  assert.equal(run.dataset.phase, 'plan');
  assert.equal(text(run, '.folder-agent-card__run-head'), 'Plan · 2 steps');
  assert.deepEqual(
    run.querySelectorAll('.folder-agent-card__line-mark').map(m => m.textContent),
    ['1', '2']
  );
  assert.equal(run.querySelectorAll('li').length, 2);
  assert.equal(text(run, '.folder-agent-card__line-detail'), '6 songs listed on the shelf');
  assert.equal(
    text(run.querySelector('.folder-agent-card__line--note'), '.folder-agent-card__line-name'),
    'Nothing is moved, copied or read'
  );

  const running = folderAgentReportView(
    reported({
      state: 'setting_up',
      actions: [],
      setup: {
        status: 'running',
        lines: [
          { kind: 'library', name: 'A', state: 'done' },
          { kind: 'songs', name: 'B', state: 'working' },
          { kind: 'place', name: 'C', state: 'waiting' },
          { kind: 'folder', name: 'Nothing is moved, copied or read', state: 'waiting' }
        ]
      }
    })
  );
  const live = renderFolderAgentReport(running, stubDocument(), {}).querySelector(
    '.folder-agent-card__run'
  );
  assert.equal(live.dataset.phase, 'running');
  assert.equal(text(live, '.folder-agent-card__run-head-text'), 'Setting up · step 2 of 3 · B');
  assert.equal(live.querySelector('.folder-agent-card__run-head').attributes.role, 'status');
  assert.deepEqual(
    live.querySelectorAll('li').map(li => [li.dataset.state, li.attributes.style]),
    [
      ['done', '--step:0'],
      ['working', '--step:1'],
      ['waiting', '--step:2']
    ]
  );
  // The words stay for the ear: Done beside the check, Working beside the pulse.
  assert.deepEqual(
    live.querySelectorAll('.folder-agent-card__line-state').map(n => n.textContent),
    ['Done', 'Working']
  );

  const stopped = folderAgentReportView(
    reported({
      state: 'set_up',
      actions: ['set_up', 'look_further'],
      setup: {
        status: 'stopped',
        lines: [
          { name: 'A', state: 'done' },
          { name: 'B', state: 'failed' },
          { name: 'C', state: 'waiting' }
        ]
      }
    })
  );
  const halted = renderFolderAgentReport(stopped, stubDocument(), {}).querySelector(
    '.folder-agent-card__run'
  );
  assert.equal(halted.dataset.phase, 'stopped');
  assert.equal(text(halted, '.folder-agent-card__run-head'), 'Stopped at step 2 of 3');

  const finished = folderAgentReportView(
    reported({
      state: 'set_up',
      actions: ['look_further'],
      setup: {
        status: 'done',
        lines: [
          { name: 'A', state: 'done' },
          { kind: 'folder', name: 'Nothing is moved, copied or read', state: 'done' }
        ]
      }
    })
  );
  const doneRun = renderFolderAgentReport(finished, stubDocument(), {}).querySelector(
    '.folder-agent-card__run'
  );
  assert.equal(doneRun.dataset.phase, 'done');
  assert.equal(text(doneRun, '.folder-agent-card__run-head'), 'All done · 1 step');
});

test('durations read <1 s, seconds, then m:ss; a line times itself from the server', () => {
  assert.equal(formatFolderAgentDuration(0), '<1 s');
  assert.equal(formatFolderAgentDuration(999), '<1 s');
  assert.equal(formatFolderAgentDuration(6400), '6 s');
  assert.equal(formatFolderAgentDuration(65000), '1:05');
  assert.equal(formatFolderAgentDuration(-50), '<1 s');
  assert.equal(formatFolderAgentClock(14000), '0:14');
  assert.equal(formatFolderAgentClock(125000), '2:05');
  const t0 = Date.parse('2026-11-09T10:00:00Z');
  assert.equal(folderAgentLineTime({ state: 'waiting' }, t0), null);
  assert.equal(folderAgentLineTime({ state: 'working', startedAt: t0 }, t0 + 4200), 4200);
  // A client clock behind the server never shows a negative time.
  assert.equal(folderAgentLineTime({ state: 'working', startedAt: t0 }, t0 - 900), 0);
  assert.equal(
    folderAgentLineTime({ state: 'done', startedAt: t0, finishedAt: t0 + 6000 }, t0 + 99999),
    6000
  );
});

test('a running setup shows the working action with its words and time, and a done one with its receipt', () => {
  const t0 = Date.parse('2026-11-09T10:00:00Z');
  const iso = ms => new Date(ms).toISOString();
  const running = folderAgentReportView(
    reported({
      state: 'setting_up',
      actions: [],
      setup: {
        status: 'running',
        started_at: iso(t0),
        lines: [
          {
            kind: 'integration',
            step: 'tools',
            name: 'Installs REAPER tools',
            state: 'done',
            progress: 'Installed Ori REAPER Plugin 0.9.0',
            started_at: iso(t0),
            finished_at: iso(t0 + 6000)
          },
          {
            kind: 'workspace',
            step: 'tools',
            name: 'Connects REAPER tools to One Song',
            state: 'working',
            progress: 'Enabling it in One Song',
            started_at: iso(t0 + 6000)
          },
          { kind: 'capability', step: 'curator', name: 'Adds File Janitor', state: 'waiting' },
          { kind: 'folder', name: 'Nothing in One Song is moved, copied or read', state: 'waiting' }
        ]
      }
    })
  );
  const card = renderFolderAgentReport(running, stubDocument(), { now: t0 + 9400 });
  const run = card.querySelector('.folder-agent-card__run');
  assert.equal(
    text(run, '.folder-agent-card__run-head-text'),
    'Setting up · step 1 of 2 · Enabling it in One Song'
  );
  const clock = run.querySelector('.folder-agent-card__run-clock');
  assert.equal(clock.textContent, '0:09');
  assert.equal(clock.attributes['aria-hidden'], 'true');
  assert.equal(clock.dataset.startedAt, String(t0));

  const [installed, connecting, queued] = run.querySelectorAll('.folder-agent-card__action');
  assert.equal(installed.dataset.state, 'done');
  assert.equal(
    text(installed, '.folder-agent-card__action-name'),
    'Installed Ori REAPER Plugin 0.9.0'
  );
  assert.equal(text(installed, '.folder-agent-card__action-time'), '6 s');
  assert.equal(installed.querySelector('.folder-agent-card__spinner'), null);
  assert.equal(connecting.dataset.state, 'working');
  assert.ok(connecting.querySelector('.folder-agent-card__spinner'));
  assert.equal(
    text(connecting, '.folder-agent-card__action-name'),
    'Connects REAPER tools to One Song'
  );
  assert.equal(text(connecting, '.folder-agent-card__action-progress'), 'Enabling it in One Song');
  const ticking = connecting.querySelector('.folder-agent-card__action-time');
  assert.equal(ticking.textContent, '3 s');
  assert.equal(ticking.dataset.startedAt, String(t0 + 6000));
  assert.equal(queued.dataset.state, 'waiting');
  assert.equal(queued.querySelector('.folder-agent-card__action-time'), null);
  assert.equal(queued.querySelector('.folder-agent-card__action-progress'), null);

  // One of three actions done.
  const bar = run.querySelector('.folder-agent-card__progress');
  assert.equal(bar.attributes.role, 'progressbar');
  assert.deepEqual([bar.attributes['aria-valuenow'], bar.attributes['aria-valuemax']], ['1', '3']);
  assert.equal(
    run.querySelector('.folder-agent-card__progress-fill').attributes.style,
    'width:33%'
  );

  // The clock moves between polls.
  tickFolderAgentClocks(run, t0 + 12100);
  assert.equal(ticking.textContent, '6 s');
  assert.equal(clock.textContent, '0:12');
  assert.equal(text(installed, '.folder-agent-card__action-time'), '6 s');

  // State words stay for screen readers.
  assert.deepEqual(
    run.querySelectorAll('.folder-agent-card__line-state').map(n => n.textContent),
    ['Working']
  );
});

test('a finished run shows its receipt with links, leaves out empty rows and collapses its steps', () => {
  const t0 = Date.parse('2026-11-09T10:00:00Z');
  const iso = ms => new Date(ms).toISOString();
  const view = folderAgentReportView(
    reported({
      workspace_name: 'Albums',
      route: '/workspaces/albums',
      state: 'set_up',
      actions: [],
      setup: {
        status: 'done',
        started_at: iso(t0),
        finished_at: iso(t0 + 41000),
        receipt: {
          installed: ['Music Project Management 0.5.0', 'Ori REAPER Plugin 0.9.0'],
          home: { name: 'Music Production Home', route: '/workspaces/music-production-home' },
          home_created: true,
          hired: ['Portfolio Manager', 'Library Manager'],
          listed: 6,
          listed_noun: 'songs'
        },
        lines: [
          {
            kind: 'provider',
            step: 'home',
            name: 'Installs Music Project Management',
            state: 'done',
            progress: 'Installed Music Project Management 0.5.0',
            started_at: iso(t0),
            finished_at: iso(t0 + 6000)
          },
          {
            kind: 'place',
            step: 'home',
            name: 'Places Albums inside your Music Production Home',
            state: 'done',
            started_at: iso(t0 + 6000),
            finished_at: iso(t0 + 9000)
          },
          { kind: 'folder', name: 'Nothing in Albums is moved, copied or read', state: 'done' }
        ]
      }
    })
  );
  const card = renderFolderAgentReport(view, stubDocument(), {});
  const run = card.querySelector('.folder-agent-card__run');
  assert.equal(text(run, '.folder-agent-card__run-head'), 'All done · 1 step · 41 s');
  const rows = run
    .querySelectorAll('.folder-agent-card__receipt-row')
    .map(row => [
      row.dataset.kind,
      text(row, '.folder-agent-card__receipt-text'),
      row.querySelector('.folder-agent-card__receipt-link')?.textContent || '',
      row.querySelector('.folder-agent-card__receipt-link')?.href || ''
    ]);
  assert.deepEqual(rows, [
    [
      'installed',
      'Music Project Management 0.5.0 · Ori REAPER Plugin 0.9.0',
      'Plugins',
      '/plugins'
    ],
    ['created', 'Music Production Home', 'Open', '/workspaces/music-production-home'],
    ['hired', 'Portfolio Manager · Library Manager', 'Agents', '/agents'],
    ['listed', '6 songs on the shelf', '', ''],
    ['placed', 'Albums inside your Music Production Home', 'Open Albums', '/workspaces/albums']
  ]);
  // The Placed row opens the place; no second link at the bottom.
  assert.equal(card.querySelector('.folder-agent-card__open'), null);

  // The step collapses to its title and time; a click shows its actions.
  const step = run.querySelector('li');
  const toggle = step.querySelector('.folder-agent-card__line-toggle');
  assert.equal(step.dataset.collapsed, 'true');
  assert.equal(toggle.attributes['aria-expanded'], 'false');
  assert.equal(text(step, '.folder-agent-card__step-time'), '9 s');
  toggle.listeners.click();
  assert.equal(step.dataset.collapsed, 'false');
  assert.equal(toggle.attributes['aria-expanded'], 'true');
});

test('a Group run receipt counts its members; a stopped run shows only what finished', () => {
  const group = folderAgentReportView(
    reported({
      workspace_name: 'Three Drafts',
      route: '/workspaces/three-drafts',
      state: 'set_up',
      actions: [],
      setup: {
        status: 'done',
        receipt: {
          group: true,
          members: [
            { name: 'Draft A', route: '/workspaces/draft-a' },
            { name: 'Draft B', route: '/workspaces/draft-b' },
            { name: 'Draft C', route: 'https://elsewhere.example/x' }
          ]
        },
        lines: [
          { kind: 'promote', step: 'group', name: 'Turns Three Drafts into a group', state: 'done' }
        ]
      }
    })
  );
  const rows = folderAgentReceiptRows(group).map(row => [row.label, row.words]);
  assert.deepEqual(rows, [
    ['Created', '3 workspaces in Three Drafts'],
    ['Placed', 'Three Drafts is now a group with 3 workspaces']
  ]);
  // A route that is not one of the app's workspace pages is never linked.
  assert.deepEqual(
    group.setup.receipt.members.map(m => m.route),
    ['/workspaces/draft-a', '/workspaces/draft-b', '']
  );

  const stopped = folderAgentReportView(
    reported({
      workspace_name: 'Albums',
      route: '/workspaces/albums',
      state: 'set_up',
      actions: ['set_up'],
      setup: {
        status: 'stopped',
        receipt: { installed: ['Music Project Management 0.5.0'] },
        lines: [
          {
            kind: 'provider',
            step: 'home',
            name: 'Installs Music Project Management',
            state: 'done'
          },
          {
            kind: 'home',
            step: 'home',
            name: 'Creates your Music Production Home',
            state: 'failed'
          }
        ]
      }
    })
  );
  const run = renderFolderAgentReport(stopped, stubDocument(), {}).querySelector(
    '.folder-agent-card__run'
  );
  assert.equal(text(run, '.folder-agent-card__run-head'), 'Stopped at step 1 of 1');
  assert.deepEqual(
    run.querySelectorAll('.folder-agent-card__receipt-row').map(row => row.dataset.kind),
    ['installed']
  );
  // A stopped run's steps stay open to show which action failed.
  assert.equal(run.querySelector('li').dataset.collapsed, undefined);
});

test('a run counts only the steps it runs, not a step the plan still marks chosen', () => {
  // The agent recommended the group; the owner ticked the Home instead. The
  // view's plan still marks the group chosen, but the run carries no lines for it.
  const view = folderAgentReportView(
    reported({
      state: 'set_up',
      actions: [],
      plan: {
        digest: 'p',
        runnable: true,
        lines: [],
        steps: [
          { handle: 'group', title: 'Turn Albums into a group', chosen: true },
          { handle: 'home', title: 'Create your Music Production Home', chosen: false }
        ]
      },
      setup: {
        status: 'done',
        lines: [
          { kind: 'home', step: 'home', name: 'Creates your Music Production Home', state: 'done' },
          { kind: 'folder', name: 'Nothing in Albums is moved, copied or read', state: 'done' }
        ]
      }
    })
  );
  const run = renderFolderAgentReport(view, stubDocument(), {}).querySelector(
    '.folder-agent-card__run'
  );
  assert.equal(text(run, '.folder-agent-card__run-head'), 'All done · 1 step');
  assert.deepEqual(
    run.querySelectorAll('li').map(li => li.dataset.step),
    ['home']
  );
});

test('a done action shows its duration; one under a second reads <1 s; no bar once the run ends', () => {
  const t0 = Date.parse('2026-11-09T10:00:00Z');
  const iso = ms => new Date(ms).toISOString();
  const finished = folderAgentReportView(
    reported({
      state: 'set_up',
      actions: [],
      setup: {
        status: 'done',
        started_at: iso(t0),
        lines: [
          {
            kind: 'workspace',
            step: 'tools',
            name: 'Connects REAPER tools to One Song',
            state: 'done',
            progress: 'Connected REAPER tools to One Song',
            started_at: iso(t0),
            finished_at: iso(t0 + 300)
          }
        ]
      }
    })
  );
  const run = renderFolderAgentReport(finished, stubDocument(), {}).querySelector(
    '.folder-agent-card__run'
  );
  assert.equal(text(run, '.folder-agent-card__action-time'), '<1 s');
  assert.equal(run.querySelector('.folder-agent-card__progress'), null);
  assert.equal(run.querySelector('.folder-agent-card__run-clock'), null);
});

test('ticking a step that cannot run with a chosen one unticks the other before asking', async () => {
  const steps = chosen => [
    {
      handle: 'music-production-home',
      title: 'Add Ori Music Demo to your Music Production Home and list its 8 songs',
      chosen: chosen === 'home',
      excludes: 'group'
    },
    {
      handle: 'group',
      title: 'Turn Ori Music Demo into a group with one workspace per project (8)',
      chosen: chosen === 'group',
      excludes: 'music-production-home'
    }
  ];
  const view = folderAgentReportView(
    reported({
      model_sentence: '',
      tools_sentence: '',
      plan: {
        digest: 'p',
        runnable: true,
        source: 'agent',
        words: 'Shelve the songs.',
        selected: ['music-production-home'],
        steps: steps('home'),
        lines: [{ kind: 'library', name: 'Adds Ori Music Demo to your Music Production Home' }]
      }
    })
  );
  const requests = [];
  const fetchImpl = async (_url, init) => {
    requests.push(JSON.parse(init.body));
    return {
      ok: true,
      status: 200,
      json: async () => ({
        plan: {
          digest: 'g',
          runnable: true,
          source: 'agent',
          words: 'Shelve the songs.',
          selected: ['group'],
          steps: steps('group'),
          lines: [{ kind: 'promote', name: 'Turns Ori Music Demo into a group' }]
        }
      })
    };
  };
  const card = renderFolderAgentReport(view, stubDocument(), { fetchImpl });
  const group = card.querySelectorAll('input').find(b => b.dataset.stepHandle === 'group');
  group.checked = true;
  await group.listeners.change();
  assert.deepEqual(requests, [{ selected: ['group'] }]);
  assert.deepEqual(
    card
      .querySelectorAll('input')
      .filter(b => b.dataset.stepHandle)
      .map(b => [b.dataset.stepHandle, b.checked]),
    [
      ['group', true],
      ['music-production-home', false]
    ]
  );
  assert.equal(text(card, '.folder-agent-card__line-name'), 'Turns Ori Music Demo into a group');
});

test("the agent's plan shows in its words; Ori's default says why", () => {
  const steps = [
    {
      handle: 'reaper-tools',
      name: 'REAPER tools',
      title: 'Connect REAPER tools to Ori Music Demo',
      chosen: true
    },
    {
      handle: 'group',
      name: 'a group',
      title: 'Turn Ori Music Demo into a group with one workspace per project (8)',
      chosen: false
    }
  ];
  const agent = folderAgentReportView(
    reported({
      model_sentence: '',
      tools_sentence: '',
      plan: {
        digest: 'p',
        runnable: true,
        source: 'agent',
        selected: ['reaper-tools'],
        words: 'Connect REAPER tools first. Leave the Logic sketch alone.',
        words_raw: 'Connect [reaper-tools] first [later]. Leave the Logic sketch alone.',
        steps,
        lines: [
          {
            kind: 'workspace',
            name: 'Connects REAPER tools to Ori Music Demo',
            step: 'reaper-tools'
          }
        ]
      }
    })
  );
  const card = renderFolderAgentReport(agent, stubDocument(), {});
  assert.equal(text(card, '.folder-agent-card__heading'), 'Ori Music Demo Manager recommends');
  // The step the agent named reads as its name, as a chip; an unknown token
  // is left out.
  const words = card.querySelector('.folder-agent-card__words');
  assert.deepEqual(
    words.children.map(node => [node.className, node.textContent, node.dataset.step]),
    [
      ['folder-agent-card__words-text', 'Connect ', undefined],
      ['folder-agent-card__step-chip', 'REAPER tools', 'reaper-tools'],
      ['folder-agent-card__words-text', ' first ', undefined],
      ['folder-agent-card__words-text', '. Leave the Logic sketch alone.', undefined]
    ]
  );
  assert.equal(card.querySelector('.folder-agent-card__stage'), null);
  assert.deepEqual(
    card.querySelectorAll('.folder-agent-card__line').map(n => n.dataset.chosen),
    ['true', 'false']
  );
  assert.equal(text(card, '.folder-agent-card__run-head'), 'Plan · 1 step');
  // No reorder: no note under the words.
  assert.equal(card.querySelector('.folder-agent-card__degraded'), null);

  // Ori ran the tools ahead of the agent's order: one muted line under its words.
  const reordered = folderAgentReportView(
    reported({
      model_sentence: '',
      tools_sentence: '',
      plan: {
        digest: 'r',
        runnable: true,
        source: 'agent',
        selected: ['reaper-tools'],
        words: 'Connect REAPER tools first.',
        reorder_note: 'Ori runs the tools first, so the Home can list what they can open.',
        steps,
        lines: []
      }
    })
  );
  const reorderedRun = renderFolderAgentReport(reordered, stubDocument(), {}).querySelector(
    '.folder-agent-card__run'
  );
  assert.deepEqual(
    reorderedRun.children.slice(0, 3).map(node => node.className),
    ['folder-agent-card__heading', 'folder-agent-card__words', 'folder-agent-card__degraded']
  );
  assert.equal(
    text(reorderedRun, '.folder-agent-card__degraded'),
    'Ori runs the tools first, so the Home can list what they can open.'
  );

  const fallback = folderAgentReportView(
    reported({
      model_sentence: '',
      tools_sentence: '',
      plan: {
        digest: 'd',
        runnable: true,
        source: 'default',
        default_reason: "The agent named no step Ori can run, so this is Ori's default plan.",
        words: 'Nothing needed.',
        selected: ['reaper-tools'],
        steps: steps.slice(0, 1),
        lines: []
      }
    })
  );
  const plainCard = renderFolderAgentReport(fallback, stubDocument(), {});
  const section = plainCard.querySelector('.folder-agent-card__run');
  assert.equal(text(section, '.folder-agent-card__heading'), 'Steps');
  assert.equal(
    text(section, '.folder-agent-card__degraded'),
    "The agent named no step Ori can run, so this is Ori's default plan."
  );
  // The step list replaces Ori's one-line summary.
  assert.equal(plainCard.querySelector('.folder-agent-card__stage'), null);

  // Once set up, the agent's words stay as the why; the boxes are gone.
  const done = folderAgentReportView(
    reported({
      state: 'set_up',
      actions: ['look_further'],
      setup: {
        status: 'done',
        lines: [{ name: 'Connects REAPER tools to Ori Music Demo', state: 'done' }]
      },
      plan: {
        digest: 'p',
        runnable: true,
        source: 'agent',
        words: 'Connect REAPER first.',
        steps,
        lines: []
      }
    })
  );
  const after = renderFolderAgentReport(done, stubDocument(), {});
  assert.equal(text(after, '.folder-agent-card__heading'), 'Ori Music Demo Manager recommended');
  assert.deepEqual(
    after.querySelector('.folder-agent-card__words').children.map(node => node.textContent),
    ['Connect REAPER first.']
  );
  assert.equal(after.querySelector('.folder-agent-card__tool-pick'), null);
});

test('a left-out part shows one muted sentence in its place', () => {
  const view = folderAgentReportView(
    reported({
      left_out: {
        what_it_is: 'Demo Manager left this part out.',
        suggested_goal: 'Demo Manager left this part out.'
      }
    })
  );
  const card = renderFolderAgentReport(view, stubDocument());
  const muted = card
    .querySelectorAll('.folder-agent-card__degraded')
    .map(n => n.textContent)
    .filter(t => t === 'Demo Manager left this part out.');
  // Only "what it is" is on the report card now; the goal is offered later.
  assert.equal(muted.length, 1);
  assert.equal(card.querySelector('.folder-agent-card__what'), null);
});

test('the one question offers folder chips and a one-line answer, both looking further', () => {
  const view = folderAgentReportView(
    reported({
      question: 'Which box should I open first?',
      ask_folders: [
        { name: 'Clients', ref: 'Boxes/Clients' },
        { name: 'Archive', ref: 'Boxes/Archive' }
      ]
    })
  );
  const calls = [];
  const card = renderFolderAgentReport(view, stubDocument(), {
    onAction: (action, _button, extra) => calls.push([action, extra])
  });
  assert.equal(text(card, '.folder-agent-card__question-text'), 'Which box should I open first?');
  const chips = card.querySelectorAll('button').filter(b => b.dataset.folderAgentChip);
  assert.deepEqual(
    chips.map(c => c.textContent),
    ['Clients', 'Archive']
  );
  chips[0].listeners.click();
  const answer = card.querySelector('.folder-agent-card__answer').children[0];
  answer.value = 'Start with the clients';
  card
    .querySelectorAll('button')
    .find(b => b.dataset.folderAgentAction === 'answer')
    .listeners.click();
  assert.deepEqual(plain(calls), [
    ['look_further', { folders: ['Boxes/Clients'] }],
    ['look_further', { hint: 'Start with the clients' }]
  ]);
  // Only names are ever shown, never the map's paths.
  assert.ok(!JSON.stringify(card.all().map(n => n.textContent)).includes('Boxes/'));
});

test('after Set it up the receipt offers Start this for the first ticket', () => {
  const view = folderAgentReportView(
    reported({
      state: 'set_up',
      actions: ['start_next', 'look_further'],
      next_ticket: { id: 't1', title: 'List the five songs saved most recently.', started: false },
      setup: { status: 'done', lines: [{ name: 'Installs tools', state: 'done' }] }
    })
  );
  assert.deepEqual(
    view.actions.map(a => a.label),
    ['Start this', 'Look further']
  );
  const card = renderFolderAgentReport(view, stubDocument());
  assert.equal(
    text(card, '.folder-agent-card__next-ticket'),
    'First ticket: List the five songs saved most recently.'
  );
  assert.equal(
    folderAgentActionPath('ws-1', 'start_next'),
    '/api/personal-assistant/folder-agent/ws-1/start-next'
  );
});

test('Create Workspace mounts the same card, and Start swaps in the receipt', async () => {
  const doc = stubDocument();
  const host = doc.createElement('section');
  host.hidden = true;
  const sent = [];
  const fetchImpl = async (url, init = {}) => {
    sent.push([url, init.body ? JSON.parse(init.body) : null]);
    const json = url.endsWith('/choose')
      ? {
          card: {
            choice_token: 'tok',
            folder_name: 'Songs',
            title: 'Start an agent on Songs',
            brief: "Find out what's here and set it up"
          }
        }
      : url.endsWith('/start')
        ? { folder_agent: reported({ workspace_id: 'ws-9', state: 'looking' }) }
        : { folder_agent: reported({ workspace_id: 'ws-9' }) };
    return { ok: true, status: url.endsWith('/start') ? 201 : 200, json: async () => json };
  };
  const started = [];
  const result = await mountFolderAgentChoice(
    host,
    { chip: 'downloads' },
    { doc, fetchImpl, onStarted: view => started.push(view.workspace_id) }
  );
  assert.equal(result.card.title, 'Start an agent on Songs');
  assert.equal(host.hidden, false);
  assert.equal(text(host, '.folder-agent-card__title'), 'Start an agent on Songs');
  assert.equal(host.querySelector('textarea').value, "Find out what's here and set it up");
  await host.querySelector('.folder-agent-card__start').listeners.click();
  await new Promise(resolve => setTimeout(resolve, 0));
  assert.deepEqual(started, ['ws-9']);
  assert.deepEqual(plain(sent.slice(0, 2)), [
    ['/api/personal-assistant/folder-agent/choose', { chip: 'downloads' }],
    [
      '/api/personal-assistant/folder-agent/start',
      {
        choice_token: 'tok',
        brief: "Find out what's here and set it up",
        hire: { name: 'Songs Manager', mandate: '' }
      }
    ]
  ]);
  assert.equal(sent[2][0], '/api/personal-assistant/folder-agent/ws-9');
  assert.equal(text(host, '.folder-agent-card__facts'), reported().facts_line);

  const refused = doc.createElement('section');
  refused.hidden = true;
  const answer = await mountFolderAgentChoice(
    refused,
    { chip: 'desktop' },
    {
      doc,
      fetchImpl: async () => ({
        ok: false,
        status: 400,
        json: async () => ({ error: 'That folder is not on this computer' })
      })
    }
  );
  assert.deepEqual(answer, { error: 'That folder is not on this computer' });
  assert.equal(refused.hidden, true);
});

test('the workspace page mount follows a look until the report arrives', async () => {
  const doc = stubDocument();
  const host = doc.createElement('section');
  host.hidden = true;
  const states = [
    reported({ state: 'looking', progress_line: 'Looking around · 3 entries' }),
    reported()
  ];
  const fetchImpl = async () => ({
    ok: true,
    status: 200,
    json: async () => ({ folder_agent: states.shift() || reported() })
  });
  await mountWorkspaceFolderAgent(host, 'ws-1', { doc, fetchImpl, pollMs: 1 });
  assert.equal(host.hidden, false);
  assert.equal(text(host, '.folder-agent-card__receipt'), 'Looking around · 3 entries');
  await new Promise(resolve => setTimeout(resolve, 20));
  assert.equal(text(host, '.folder-agent-card__facts'), reported().facts_line);
  const none = doc.createElement('section');
  await mountWorkspaceFolderAgent(none, 'ws-2', {
    doc,
    fetchImpl: async () => ({ ok: false, status: 404, json: async () => ({}) })
  });
  assert.equal(none.hidden, true);
});

test('the compact card drops its title and keeps the plan and actions with their handlers', () => {
  const view = folderAgentReportView(
    reported({
      suggested_goal: 'Finish one song a week',
      plan: { digest: 'd1', runnable: true, selected: [], lines: [], steps: [] }
    })
  );
  const actions = [];
  const card = renderFolderAgentReport(view, stubDocument(), {
    compact: true,
    onAction: id => actions.push(id)
  });
  assert.ok(card.className.includes('folder-agent-card--compact'));
  assert.equal(card.querySelector('.folder-agent-card__title'), null);
  assert.equal(card.querySelector('.folder-agent-card__goal-input'), null);
  const buttons = card.querySelectorAll('button').filter(b => b.dataset.folderAgentAction);
  assert.deepEqual(
    buttons.map(b => b.dataset.folderAgentAction),
    ['set_up', 'keep', 'look_further'].filter(id => view.actions.some(a => a.id === id))
  );
  buttons[buttons.length - 1].listeners.click();
  assert.equal(actions.length, 1);
  const full = renderFolderAgentReport(view, stubDocument());
  assert.ok(full.querySelector('.folder-agent-card__title'));
  // While it looks, the balloon's header carries the line: no receipt.
  const looking = renderFolderAgentReport(
    folderAgentReportView(
      reported({ state: 'looking', progress_line: 'Looking around · 3 entries' })
    ),
    stubDocument(),
    { compact: true }
  );
  assert.equal(looking.querySelector('.folder-agent-card__receipt'), null);
  assert.equal(text(looking, '.folder-agent-card__workspace'), 'Workspace Ori Music Demo');
});

test('the workspace page mount hands each view to a presenter that may draw the host', async () => {
  const doc = stubDocument();
  const host = doc.createElement('section');
  const seen = [];
  const presenter = {
    present(args) {
      seen.push(args && { state: args.view.state, previous: args.previous?.state || '' });
      if (!args) return false;
      args.host.replaceChildren(args.card(true));
      return true;
    }
  };
  const states = [reported({ state: 'looking' }), reported()];
  const fetchImpl = async () => okJSONBody({ folder_agent: states.shift() || reported() });
  await mountWorkspaceFolderAgent(host, 'ws-1', { doc, fetchImpl, pollMs: 1, presenter });
  await new Promise(resolve => setTimeout(resolve, 20));
  assert.deepEqual(seen.slice(0, 2), [
    { state: 'looking', previous: '' },
    { state: 'reported', previous: 'looking' }
  ]);
  assert.ok(host.children[0].className.includes('folder-agent-card--compact'));
});

const okJSONBody = body => ({ ok: true, status: 200, json: async () => body });

// A Set it up run in progress, and the same run finished.
const settingUp = (overrides = {}) =>
  reported({
    state: 'setting_up',
    actions: [],
    plan: { digest: 'd1', runnable: true, selected: ['reaper-tools'], lines: [], steps: [] },
    setup: {
      status: 'running',
      lines: [{ kind: 'integration', step: 'reaper-tools', name: 'REAPER tools', state: 'working' }]
    },
    ...overrides
  });
const setUp = (overrides = {}) =>
  settingUp({
    state: 'set_up',
    setup: {
      status: 'done',
      finished_at: '2026-11-08T12:00:00Z',
      lines: [{ kind: 'integration', step: 'reaper-tools', name: 'REAPER tools', state: 'done' }]
    },
    ...overrides
  });
const okJSON = body => ({ ok: true, status: 200, json: async () => body });
// settle lets 1 ms polls land and lets "nothing happened" be asserted. It was 25 ms
// and failed twice on a saturated machine (the whole suite runs in parallel), so it
// leaves a wider margin; a wait for something that must happen uses untilTrue.
const settle = () => new Promise(resolve => setTimeout(resolve, 100));

// untilTrue waits, up to five seconds, for a condition a background poll makes
// true, and fails loudly instead of hanging when it never does.
async function untilTrue(condition) {
  const deadline = Date.now() + 5000;
  while (!condition()) {
    if (Date.now() > deadline) assert.fail('timed out waiting for the poll');
    await new Promise(resolve => setTimeout(resolve, 5));
  }
}

test('a placement line newly done tells Home to build the place; other polls do not', async () => {
  const lines = (...states) =>
    [
      { kind: 'provider', name: 'Installs Music Project Management' },
      { kind: 'home', name: 'Creates your Music Production Home' },
      { kind: 'place', name: 'Places Albums inside your Music Production Home' }
    ].map((line, i) => ({ ...line, state: states[i] || 'waiting' }));
  const view = (...states) => ({ setup: { lines: lines(...states) } });
  assert.equal(
    placementLinesDone(view('working'), view('done')),
    false,
    'an install is no placement'
  );
  assert.equal(placementLinesDone(view('done', 'working'), view('done', 'done')), true);
  assert.equal(placementLinesDone(view('done', 'done'), view('done', 'done', 'working')), false);
  assert.equal(
    placementLinesDone(view('done', 'done', 'working'), view('done', 'done', 'done')),
    true
  );
  assert.equal(placementLinesDone(null, view('done', 'done')), true);

  // Followed through polls: one event per placement, carrying the flag.
  const events = [];
  const previousWindow = globalThis.window;
  const previousEvent = globalThis.CustomEvent;
  globalThis.CustomEvent = class {
    constructor(type, init) {
      this.type = type;
      this.detail = init?.detail;
    }
  };
  globalThis.window = { dispatchEvent: event => events.push(event) };
  try {
    const polls = [
      { ...settingUp(), ...view('done', 'working') },
      { ...settingUp(), ...view('done', 'done', 'working') },
      { ...settingUp(), ...view('done', 'done', 'working') },
      { ...setUp(), ...view('done', 'done', 'done') }
    ];
    const stop = followFolderAgent(
      { ...settingUp(), ...view('working') },
      { fetchImpl: async () => okJSON({ folder_agent: polls.shift() || null }), pollMs: 1 }
    );
    // Wait for the follower to have taken all four polls, not for a guessed
    // 25 ms (a saturated machine is slower than that), then let the last one land.
    await untilTrue(() => polls.length === 0);
    await settle();
    stop();
  } finally {
    globalThis.window = previousWindow;
    globalThis.CustomEvent = previousEvent;
  }
  assert.deepEqual(
    events.map(event => [event.type, event.detail.placement]),
    [
      ['ori:folder-agent-changed', true],
      ['ori:folder-agent-changed', true]
    ]
  );
});

test('every follower of a run announces a placement through one helper, before it opens the place', async () => {
  const events = [];
  const previousWindow = globalThis.window;
  const previousEvent = globalThis.CustomEvent;
  globalThis.CustomEvent = class {
    constructor(type, init) {
      this.type = type;
      this.detail = init?.detail;
    }
  };
  globalThis.window = { dispatchEvent: event => events.push(event) };
  try {
    const was = {
      workspace_id: 'ws-1',
      setup: { lines: [{ kind: 'place', name: 'P', state: 'working' }] }
    };
    const now = {
      workspace_id: 'ws-1',
      setup: { lines: [{ kind: 'place', name: 'P', state: 'done' }] }
    };
    assert.equal(notifyPlacement(was, was), false);
    assert.equal(notifyPlacement(was, now), true);
  } finally {
    globalThis.window = previousWindow;
    globalThis.CustomEvent = previousEvent;
  }
  assert.deepEqual(
    events.map(event => [event.type, event.detail]),
    [['ori:folder-agent-changed', { workspaceID: 'ws-1', placement: true }]]
  );

  // The Home drawer follows its own poll; it must announce the same way, and
  // before its run's end opens the place, so the open waits for the motion.
  const drawer = readFileSync(new URL('./personal-assistant-folder.js', import.meta.url), 'utf8');
  const poll = drawer.slice(drawer.indexOf('function scheduleAgentPoll()'));
  assert.match(poll, /notifyPlacement\(view, next\);/);
  assert.ok(
    poll.indexOf('notifyPlacement(view, next)') < poll.indexOf('openFolderAgentPlace(next)'),
    'the map is told before the place is opened'
  );
});

test('opening the place waits for Home to finish building it, or one second', async () => {
  const went = [];
  let release;
  const motion = {
    whenMotionSettled: maxMs => {
      assert.equal(maxMs, 1000);
      return new Promise(resolve => (release = resolve));
    }
  };
  assert.equal(
    openFolderAgentPlace(
      { route: '/workspaces/albums' },
      { navigate: route => went.push(route), delayMs: 0, motion }
    ),
    true
  );
  await settle();
  assert.deepEqual(went, [], 'it does not leave while the map still moves');
  release();
  await settle();
  assert.deepEqual(went, ['/workspaces/albums']);

  // Without a map (the workspace page), it goes on its own beat.
  const direct = [];
  openFolderAgentPlace(
    { route: '/workspaces/albums' },
    { navigate: r => direct.push(r), delayMs: 0, motion: null }
  );
  assert.deepEqual(direct, ['/workspaces/albums']);
});

test('a run watched to its end says it is opening the place and goes there; a stopped one stays', async () => {
  assert.equal(setupFinished(settingUp(), setUp()), true);
  assert.equal(
    setupFinished(settingUp(), setUp({ setup: { status: 'stopped', lines: [] } })),
    false
  );
  assert.equal(setupFinished(reported(), setUp()), false);
  assert.equal(
    openFolderAgentPlace({ route: '' }, { navigate: () => assert.fail('no route') }),
    false
  );

  const doc = stubDocument();
  const host = doc.createElement('section');
  const states = [settingUp(), setUp()];
  const went = [];
  await mountWorkspaceFolderAgent(host, 'ws-1', {
    doc,
    fetchImpl: async () => okJSON({ folder_agent: states.shift() || null }),
    pollMs: 1,
    openDelayMs: 0,
    navigate: route => went.push(route)
  });
  assert.equal(text(host, '.folder-agent-card__title'), 'Setting up Ori Music Demo');
  assert.equal(text(host, '.folder-agent-card__open'), 'Open Ori Music Demo');
  await settle();
  assert.equal(text(host, '.folder-agent-card__title'), 'Ori Music Demo is set up');
  assert.equal(text(host, '.folder-agent-card__opening'), 'Opening Ori Music Demo…');
  assert.equal(host.querySelector('.folder-agent-card__opening').attributes.role, 'status');
  assert.equal(host.querySelector('.folder-agent-card__open'), null);
  assert.deepEqual(went, ['/workspaces/ori-music-demo']);

  // A run that stopped keeps the card and its link; nothing opens.
  const stopped = doc.createElement('section');
  const halts = [settingUp(), setUp({ setup: { status: 'stopped', lines: [] } })];
  const stayed = [];
  await mountWorkspaceFolderAgent(stopped, 'ws-1', {
    doc,
    fetchImpl: async () => okJSON({ folder_agent: halts.shift() || null }),
    pollMs: 1,
    openDelayMs: 0,
    navigate: route => stayed.push(route)
  });
  await settle();
  assert.equal(text(stopped, '.folder-agent-card__open'), 'Open Ori Music Demo');
  assert.equal(stopped.querySelector('.folder-agent-card__opening'), null);
  assert.deepEqual(stayed, []);

  // A page that wants to stay (openPlace: false) is left where it is.
  const staying = doc.createElement('section');
  const runs = [settingUp(), setUp()];
  await mountWorkspaceFolderAgent(staying, 'ws-1', {
    doc,
    fetchImpl: async () => okJSON({ folder_agent: runs.shift() || null }),
    pollMs: 1,
    openPlace: false,
    openDelayMs: 0,
    navigate: () => assert.fail('the page asked to stay')
  });
  await settle();
  assert.equal(text(staying, '.folder-agent-card__open'), 'Open Ori Music Demo');
});

test('a run that ran the Home step lands in the Home, named on the line; others keep the folder', async () => {
  const HOME_ROUTE =
    '/workspaces/music-production-home?mode=map&setup_beside=ws-1&setup_projects=1';
  const landing = { route: HOME_ROUTE, name: 'Music Production Home' };
  // Raw views (the drawer's poll) and report views (the card) alike.
  assert.deepEqual(
    folderAgentDestination({ route: '/workspaces/albums', workspace_name: 'Albums', landing }),
    { route: HOME_ROUTE, name: 'Music Production Home', landing: true }
  );
  assert.deepEqual(folderAgentDestination(folderAgentReportView(setUp({ landing }))), {
    route: HOME_ROUTE,
    name: 'Music Production Home',
    landing: true
  });
  assert.deepEqual(
    folderAgentDestination({ route: '/workspaces/albums', workspace_name: 'Albums' }),
    {
      route: '/workspaces/albums',
      name: 'Albums',
      landing: false
    }
  );
  // A landing off the app's own pages is never followed.
  assert.equal(
    folderAgentDestination({
      route: '/workspaces/albums',
      landing: { route: 'https://x.example/' }
    }).route,
    '/workspaces/albums'
  );
  const raw = [];
  openFolderAgentPlace(
    { route: '/workspaces/albums', landing },
    { navigate: r => raw.push(r), delayMs: 0, motion: null }
  );
  assert.deepEqual(raw, [HOME_ROUTE]);

  const doc = stubDocument();
  const host = doc.createElement('section');
  const states = [settingUp(), setUp({ landing })];
  const went = [];
  await mountWorkspaceFolderAgent(host, 'ws-1', {
    doc,
    fetchImpl: async () => okJSON({ folder_agent: states.shift() || null }),
    pollMs: 1,
    openDelayMs: 0,
    navigate: route => went.push(route)
  });
  await settle();
  assert.equal(text(host, '.folder-agent-card__opening'), 'Opening Music Production Home…');
  assert.deepEqual(went, [HOME_ROUTE]);
});

test('Needs you follows a Set it up in place until it ends, then opens the place', async () => {
  const doc = stubDocument();
  const host = doc.createElement('div');
  const polls = [setUp()];
  const posts = [];
  let done = 0;
  const went = [];
  const fetchImpl = async (url, init) => {
    if (init?.method === 'POST') {
      posts.push(url);
      return okJSON({ folder_agent: settingUp() });
    }
    return okJSON({ folder_agent: polls.shift() || null });
  };
  const items = [
    {
      kind: 'folder_agent_report',
      folder_agent: reported({
        plan: { digest: 'd1', runnable: true, selected: [], lines: [], steps: [] }
      })
    }
  ];
  mountFolderAgentReports(host, items, {
    doc,
    fetchImpl,
    pollMs: 1,
    openDelayMs: 0,
    navigate: route => went.push(route),
    onDone: () => done++
  });
  const setUpButton = host
    .querySelectorAll('button')
    .find(button => button.dataset.folderAgentAction === 'set_up');
  await setUpButton.listeners.click();
  assert.deepEqual(posts, ['/api/personal-assistant/folder-agent/ws-1/setup']);
  assert.equal(done, 1);
  // The run replaced the report in place, and the card follows it.
  assert.equal(host.children.length, 1);
  assert.equal(text(host, '.folder-agent-card__title'), 'Setting up Ori Music Demo');
  // Today's reload lists the run among the working, not the reports: the
  // card leaves the list, the run is still watched, and the place opens.
  assert.equal(
    mountFolderAgentReports(host, [], {
      doc,
      fetchImpl,
      pollMs: 1,
      openDelayMs: 0,
      navigate: route => went.push(route),
      onDone: () => done++
    }),
    0
  );
  assert.equal(host.children.length, 0);
  await settle();
  assert.deepEqual(went, ['/workspaces/ori-music-demo']);
  // Leaving the page, the list is not reloaded under the user.
  assert.equal(done, 1);

  // A report that stays listed is redrawn in place as its run ends.
  const kept = doc.createElement('div');
  const laterPolls = [settingUp(), setUp()];
  const stayed = [];
  mountFolderAgentReports(kept, [{ kind: 'folder_agent_report', folder_agent: settingUp() }], {
    doc,
    fetchImpl: async () => okJSON({ folder_agent: laterPolls.shift() || null }),
    pollMs: 1,
    openDelayMs: 0,
    navigate: route => stayed.push(route),
    onDone: () => done++
  });
  // The poll ends the run in the background: wait for the end, not for a fixed
  // moment (a saturated machine took longer than a short sleep).
  await untilTrue(
    () =>
      stayed.length > 0 && text(kept, '.folder-agent-card__title') === 'Ori Music Demo is set up'
  );
  assert.equal(kept.children.length, 1);
  assert.equal(text(kept, '.folder-agent-card__title'), 'Ori Music Demo is set up');
  assert.equal(text(kept, '.folder-agent-card__opening'), 'Opening Ori Music Demo…');
  assert.deepEqual(stayed, ['/workspaces/ori-music-demo']);
});

test('Downloads shows factual counts beside model words and a normal keyboard-accessible chooser link', () => {
  const view = folderAgentReportView(
    reported({
      policy_note: 'Downloads stays an inbox. Confirm folder access separately.',
      explore_folder: true,
      what_it_is: 'The agent found projects here.',
      plan: { digest: 'fresh', runnable: true, selected: ['file-curator'], steps: [], lines: [] }
    })
  );
  const card = renderFolderAgentReport(view, stubDocument());
  assert.equal(text(card, '.folder-agent-card__facts'), view.facts);
  assert.equal(text(card, '.folder-agent-card__note'), view.policyNote);
  const link = card
    .querySelectorAll('a')
    .find(node => node.textContent === 'Explore a project folder…');
  assert.equal(link.href, '/?panel=today&folder=show');
  assert.equal(link.attributes.tabindex, undefined);
  assert.deepEqual(link.listeners, {}); // Navigation only: no mutation or automatic selection.
});

test('ordinary reports do not acquire an inbox policy or project-folder action', () => {
  const view = folderAgentReportView(reported());
  assert.equal(view.policyNote, '');
  assert.equal(view.exploreFolder, false);
  const card = renderFolderAgentReport(view, stubDocument());
  assert.equal(
    card.querySelectorAll('a').some(node => node.textContent === 'Explore a project folder…'),
    false
  );
});

// The install block (confirm-and-visible-installs FR13–FR22): the plugins the
// ticked steps install, a marker beside each step that installs, and a
// button that says how many it installs.
const installSteps = chosen => [
  {
    handle: 'reaper-tools',
    kind: 'integration',
    name: 'REAPER tools',
    title: 'Install REAPER tools and connect them to Ori Music Demo',
    chosen: chosen.includes('reaper-tools')
  },
  {
    handle: 'music-production-home',
    kind: 'home',
    name: 'Music Production Home',
    title: 'Create your Music Production Home and put Ori Music Demo on its shelf (8 songs)',
    chosen: chosen.includes('music-production-home')
  }
];
const reaperInstall = {
  step: 'reaper-tools',
  line: 'integration',
  name: 'REAPER tools',
  version: '0.9.1',
  publisher: 'Ori',
  source_label: 'johnjallday/reaper-plugin',
  source_url: 'https://github.com/johnjallday/reaper-plugin',
  why: 'Lets the agent work with the REAPER songs here.',
  app_sentence: 'REAPER is on this Mac; its Ori tools are not yet.'
};
const providerInstall = {
  step: 'music-production-home',
  line: 'provider',
  name: 'Music Project Management',
  publisher: 'Ori',
  source_label: 'johnjallday/music-project-management',
  source_url: 'https://github.com/johnjallday/music-project-management',
  why: 'Provides your Music Production Home.'
};
const installPlan = chosen => ({
  digest: chosen.join('+'),
  runnable: chosen.length > 0,
  source: 'default',
  selected: chosen,
  steps: installSteps(chosen),
  installs: [
    chosen.includes('reaper-tools') ? reaperInstall : null,
    chosen.includes('music-production-home') ? providerInstall : null
  ].filter(Boolean),
  lines: [
    chosen.includes('reaper-tools')
      ? { kind: 'integration', name: 'Installs REAPER tools', step: 'reaper-tools' }
      : null,
    chosen.includes('reaper-tools')
      ? { kind: 'workspace', name: 'Connects REAPER tools to Ori Music Demo', step: 'reaper-tools' }
      : null,
    chosen.includes('music-production-home')
      ? {
          kind: 'provider',
          name: 'Installs Music Project Management',
          step: 'music-production-home'
        }
      : null,
    chosen.includes('music-production-home')
      ? { kind: 'home', name: 'Creates your Music Production Home', step: 'music-production-home' }
      : null,
    { kind: 'folder', name: 'Nothing in Ori Music Demo is moved, copied or read.' }
  ].filter(Boolean)
});
const installCard = (chosen, options = {}) =>
  renderFolderAgentReport(
    folderAgentReportView(
      reported({ model_sentence: '', tools_sentence: '', plan: installPlan(chosen) })
    ),
    stubDocument(),
    options
  );

test('the button says how many plugins it installs: Set it up at none', () => {
  assert.equal(folderAgentSetUpLabel({ installs: [] }), 'Set it up');
  assert.equal(folderAgentSetUpLabel(null), 'Set it up');
  assert.equal(folderAgentSetUpLabel({ installs: [{}] }), 'Install 1 & set up');
  assert.equal(folderAgentSetUpLabel({ installs: [{}, {}] }), 'Install 2 & set up');
  const view = folderAgentReportView(
    reported({ model_sentence: '', tools_sentence: '', plan: installPlan(['reaper-tools']) })
  );
  assert.deepEqual(
    view.actions.map(a => a.label),
    ['Install 1 & set up', 'Keep the folder as it is', 'Look further']
  );
  const nothingToInstall = folderAgentReportView(
    reported({
      model_sentence: '',
      tools_sentence: '',
      plan: { ...installPlan(['reaper-tools']), installs: [] }
    })
  );
  assert.equal(nothingToInstall.actions[0].label, 'Set it up');
});

test('the install block lists each plugin with its release, source and why; no block at none', () => {
  const card = installCard(['reaper-tools', 'music-production-home']);
  const block = card.querySelector('.folder-agent-card__installs');
  assert.equal(text(block, '.folder-agent-card__heading'), 'Downloads and installs 2 plugins');
  const rows = block.querySelectorAll('.folder-agent-card__install');
  assert.deepEqual(
    rows.map(row => [row.dataset.step, text(row, '.folder-agent-card__line-name')]),
    [
      ['reaper-tools', 'REAPER tools'],
      ['music-production-home', 'Music Project Management']
    ]
  );
  // Name and version; a version the host does not know yet says so.
  assert.equal(text(rows[0], '.folder-agent-card__install-version'), '0.9.1');
  assert.equal(text(rows[1], '.folder-agent-card__install-version'), 'latest reviewed release');
  // Reviewed release, the publisher, the source in a new tab, and Plugins.
  const links = rows[0].querySelectorAll('a');
  assert.deepEqual(
    links.map(a => [a.textContent, a.href, a.target || '']),
    [
      ['johnjallday/reaper-plugin', 'https://github.com/johnjallday/reaper-plugin', '_blank'],
      ['Plugins', '/plugins', '']
    ]
  );
  assert.equal(links[0].rel, 'noopener noreferrer');
  assert.equal(text(rows[0], '.folder-agent-card__install-review'), 'Reviewed release');
  assert.equal(text(rows[0], '.folder-agent-card__install-publisher'), 'Ori');
  assert.deepEqual(
    rows[0].querySelectorAll('.folder-agent-card__line-detail').map(n => n.textContent),
    [
      'Lets the agent work with the REAPER songs here.',
      'REAPER is on this Mac; its Ori tools are not yet.'
    ]
  );
  // No state before a run.
  assert.equal(rows[0].dataset.state, undefined);
  assert.equal(rows[0].querySelector('.folder-agent-card__line-state'), null);
  // The install lines are not repeated among the steps' actions; each step
  // that installs carries a marker beside its title.
  assert.deepEqual(
    card.querySelectorAll('.folder-agent-card__action-name').map(n => n.textContent),
    ['Connects REAPER tools to Ori Music Demo', 'Creates your Music Production Home']
  );
  assert.deepEqual(
    card.querySelectorAll('.folder-agent-card__install-marker').map(n => n.textContent),
    ['Installs 1 plugin', 'Installs 1 plugin']
  );
  // The block sits between the steps and the assurance note, above the buttons.
  const run = card.querySelector('.folder-agent-card__run');
  const kinds = run.children.map(n => n.className.split(' ')[0]);
  assert.ok(
    kinds.indexOf('folder-agent-card__plan') < kinds.indexOf('folder-agent-card__installs')
  );
  assert.ok(
    kinds.indexOf('folder-agent-card__installs') < kinds.indexOf('folder-agent-card__line')
  );

  assert.equal(installCard([]).querySelector('.folder-agent-card__installs'), null);
});

test('a search add-on reads Found by search with its marketplace in place of the review', () => {
  const plan = {
    ...installPlan([]),
    runnable: true,
    selected: ['mix-kit'],
    steps: [
      { handle: 'mix-kit', kind: 'add_on', name: 'Mix Kit', title: 'Install Mix Kit', chosen: true }
    ],
    installs: [
      {
        step: 'mix-kit',
        line: 'add_on',
        name: 'Mix Kit',
        search: true,
        marketplace: 'Community',
        why: 'Mixes.'
      }
    ],
    lines: [{ kind: 'add_on', name: 'Installs Mix Kit · found by search', step: 'mix-kit' }]
  };
  const card = renderFolderAgentReport(
    folderAgentReportView(reported({ model_sentence: '', tools_sentence: '', plan })),
    stubDocument(),
    {}
  );
  const row = card.querySelector('.folder-agent-card__install');
  assert.equal(text(row, '.folder-agent-card__tool-label'), 'Found by search · Community');
  assert.equal(row.querySelector('.folder-agent-card__install-review'), null);
  assert.deepEqual(
    row.querySelectorAll('a').map(a => a.textContent),
    ['Plugins']
  );
});

test('unticking a step updates the block, the markers and the button from the same re-plan', async () => {
  const requests = [];
  const fetchImpl = async (_url, init) => {
    const body = JSON.parse(init.body);
    requests.push(body);
    return { ok: true, status: 200, json: async () => ({ plan: installPlan(body.selected) }) };
  };
  const card = installCard(['reaper-tools', 'music-production-home'], { fetchImpl });
  const setUp = card.querySelectorAll('button').find(b => b.dataset.folderAgentAction === 'set_up');
  assert.equal(setUp.textContent, 'Install 2 & set up');
  const home = card
    .querySelectorAll('input')
    .find(b => b.dataset.stepHandle === 'music-production-home');
  home.checked = false;
  await home.listeners.change();
  assert.deepEqual(requests, [{ selected: ['reaper-tools'] }]);
  assert.equal(
    text(card.querySelector('.folder-agent-card__installs'), '.folder-agent-card__heading'),
    'Downloads and installs 1 plugin'
  );
  assert.equal(card.querySelectorAll('.folder-agent-card__install').length, 1);
  assert.equal(card.querySelectorAll('.folder-agent-card__install-marker').length, 1);
  assert.equal(setUp.textContent, 'Install 1 & set up');
  home.checked = true;
  await home.listeners.change();
  assert.equal(setUp.textContent, 'Install 2 & set up');
  assert.equal(card.querySelectorAll('.folder-agent-card__install').length, 2);
});

test('during and after a run each install row shows its state from the run line', () => {
  const plan = installPlan(['reaper-tools', 'music-production-home']);
  const runCard = (state, status, lines) =>
    renderFolderAgentReport(
      folderAgentReportView(
        reported({
          state,
          actions: [],
          model_sentence: '',
          tools_sentence: '',
          plan,
          setup: { status, started_at: '2026-11-11T10:00:00Z', lines }
        })
      ),
      stubDocument(),
      {}
    );
  const states = card =>
    card
      .querySelectorAll('.folder-agent-card__install')
      .map(row => [row.dataset.state, text(row, '.folder-agent-card__line-state')]);
  const running = runCard('setting_up', 'running', [
    {
      kind: 'integration',
      name: 'Installs REAPER tools',
      step: 'reaper-tools',
      state: 'working',
      progress: 'Downloading REAPER 0.9.1'
    },
    { kind: 'workspace', name: 'Connects REAPER tools', step: 'reaper-tools', state: 'waiting' },
    {
      kind: 'provider',
      name: 'Installs Music Project Management',
      step: 'music-production-home',
      state: 'waiting'
    }
  ]);
  assert.deepEqual(states(running), [
    ['working', 'Downloading REAPER 0.9.1'],
    ['waiting', 'Waiting']
  ]);
  const ended = runCard('set_up', 'stopped', [
    {
      kind: 'integration',
      name: 'Installs REAPER tools',
      step: 'reaper-tools',
      state: 'done',
      progress: 'Installed REAPER 0.9.1'
    },
    {
      kind: 'provider',
      name: 'Installs Music Project Management',
      step: 'music-production-home',
      state: 'failed',
      progress: 'The download did not verify'
    }
  ]);
  assert.deepEqual(states(ended), [
    ['done', 'Installed 0.9.1'],
    ['failed', 'Failed: The download did not verify']
  ]);
});

// The premise (confirm-and-visible-installs FR1–FR6, FR12).
const askedPremise = {
  state: 'asked',
  sentence: 'This looks like a music production folder: 7 REAPER songs and 1 Logic Pro song.',
  confirm: 'Manage them as songs',
  other: "It's something else"
};

test('while the premise is asked the card shows only it, with two real buttons in order', async () => {
  const view = folderAgentReportView(
    reported({
      what_it_is: 'A music demo folder.',
      premise: askedPremise,
      plan: installPlan(['reaper-tools', 'music-production-home'])
    })
  );
  const actions = [];
  const card = renderFolderAgentReport(view, stubDocument(), {
    onAction: (id, button) => actions.push([id, button.textContent])
  });
  const premise = card.querySelector('.folder-agent-card__premise');
  assert.deepEqual(
    premise.children.map(node => node.className),
    ['folder-agent-card__premise-text', 'folder-agent-card__actions']
  );
  assert.equal(text(premise, '.folder-agent-card__premise-text'), askedPremise.sentence);
  const buttons = card.querySelectorAll('button');
  assert.deepEqual(
    buttons.map(b => [b.textContent, b.type, b.className.includes('btn-primary')]),
    [
      ['Manage them as songs', 'button', true],
      ["It's something else", 'button', false]
    ]
  );
  // No recommendation, plan, install block, report buttons or agent words.
  for (const hidden of [
    '.folder-agent-card__run',
    '.folder-agent-card__installs',
    '.folder-agent-card__what',
    '.folder-agent-card__degraded',
    '.folder-agent-card__facts'
  ])
    assert.equal(card.querySelector(hidden), null, hidden);
  assert.equal(
    card.querySelectorAll('a').some(a => a.textContent === 'Open Ori Music Demo'),
    true
  );
  await buttons[0].listeners.click();
  await buttons[1].listeners.click();
  assert.deepEqual(actions, [
    ['premise_confirm', 'Manage them as songs'],
    ['premise_other', "It's something else"]
  ]);
});

test('the premise answers post to the premise route with the answer', async () => {
  const calls = [];
  const fetchImpl = async (url, init) => {
    calls.push([url, JSON.parse(init.body)]);
    return { ok: true, status: 200, json: async () => ({ folder_agent: null }) };
  };
  const view = { workspaceID: 'ws-1' };
  await postFolderAgentAction(view, 'premise_confirm', fetchImpl);
  await postFolderAgentAction(view, 'premise_other', fetchImpl);
  assert.deepEqual(calls, [
    ['/api/personal-assistant/folder-agent/ws-1/premise', { answer: 'confirmed' }],
    ['/api/personal-assistant/folder-agent/ws-1/premise', { answer: 'something_else' }]
  ]);
});

test('confirmed, the premise is one plain line in place of the agent sentence and the plan shows', () => {
  const view = folderAgentReportView(
    reported({
      what_it_is: 'A music demo folder.',
      model_sentence: '',
      tools_sentence: '',
      premise: {
        state: 'confirmed',
        sentence: 'A music production folder: 7 REAPER songs and 1 Logic Pro song.'
      },
      plan: installPlan(['reaper-tools'])
    })
  );
  const card = renderFolderAgentReport(view, stubDocument(), {});
  assert.equal(
    text(card, '.folder-agent-card__premise-text'),
    'A music production folder: 7 REAPER songs and 1 Logic Pro song.'
  );
  assert.equal(card.querySelector('.folder-agent-card__premise'), null);
  assert.equal(card.querySelector('.folder-agent-card__what'), null);
  assert.equal(card.querySelector('.folder-agent-card__facts'), null);
  assert.ok(card.querySelector('.folder-agent-card__installs'));
  assert.equal(
    card.querySelectorAll('button').find(b => b.dataset.folderAgentAction === 'set_up').textContent,
    'Install 1 & set up'
  );
});

test("after It's something else the card offers a plain workspace or the owner's words", async () => {
  const view = folderAgentReportView(
    reported({
      actions: ['keep', 'look_further'],
      plan: null,
      premise: {
        state: 'something_else',
        sentence: 'Nothing is installed or created.',
        withdrawn: true
      }
    })
  );
  const actions = [];
  const card = renderFolderAgentReport(view, stubDocument(), {
    onAction: (id, _button, extra) => actions.push([id, extra])
  });
  assert.equal(text(card, '.folder-agent-card__premise-text'), 'Nothing is installed or created.');
  assert.equal(card.querySelector('.folder-agent-card__run'), null);
  const field = card.querySelector('input');
  assert.equal(field.attributes['aria-label'], 'Tell Ori Music Demo Manager what it is');
  const buttons = card.querySelectorAll('button');
  assert.deepEqual(
    buttons.map(b => b.textContent),
    ['Look again', 'Keep it as a plain workspace']
  );
  // An empty field asks nothing; a typed one looks again with it as the hint.
  await buttons[0].listeners.click();
  field.value = 'Lecture recordings';
  await buttons[0].listeners.click();
  await buttons[1].listeners.click();
  assert.deepEqual(actions, [
    ['look_further', { hint: 'Lecture recordings' }],
    ['keep', undefined]
  ]);
  // Kept: only the field remains.
  const kept = renderFolderAgentReport(
    folderAgentReportView(
      reported({
        state: 'kept',
        actions: ['look_further'],
        plan: null,
        premise: {
          state: 'something_else',
          sentence: 'Nothing is installed or created.',
          withdrawn: true
        }
      })
    ),
    stubDocument(),
    {}
  );
  assert.deepEqual(
    kept.querySelectorAll('button').map(b => b.textContent),
    ['Look again']
  );
});
