import test from 'node:test';
import assert from 'node:assert/strict';

import {
  chooseFolder,
  folderAgentActionPath,
  folderAgentCardView,
  folderAgentHireChoice,
  folderAgentNeedsView,
  folderAgentReportView,
  folderAgentTileLine,
  mountWorkspaceFolderAgent,
  mountFolderAgentChoice,
  mountFolderAgentReports,
  postFolderAgentAction,
  renderFolderAgentCard,
  renderFolderAgentReport,
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

test('the agent sections render as text, the goal travels with Set it up', () => {
  const view = folderAgentReportView(
    reported({
      what_it_is: 'Seven songs and a sketch across two album folders.',
      suggested_goal: 'Keep these songs moving toward release.',
      first_ticket: 'List the five songs saved most recently.',
      first_ticket_why: 'Seeing what moved last shows where to start.',
      model_sentence: '',
      tools_sentence: '',
      plan: {
        digest: 'd9',
        runnable: true,
        lines: [{ kind: 'folder', name: 'Nothing is moved, copied or read' }]
      }
    })
  );
  assert.equal(view.offersGoal, true);
  const calls = [];
  const card = renderFolderAgentReport(view, stubDocument(), {
    onAction: (action, _button, extra) => calls.push([action, extra])
  });
  assert.equal(text(card, '.folder-agent-card__what'), view.whatItIs);
  assert.equal(text(card, '.folder-agent-card__ticket-title'), view.firstTicket);
  assert.equal(text(card, '.folder-agent-card__ticket-why'), view.firstTicketWhy);
  const goal = card.querySelector('.folder-agent-card__goal');
  const [input, label] = goal.children.slice(1);
  assert.equal(input.value, 'Keep these songs moving toward release.');
  input.value = 'Finish two songs this month';
  const setUp = () =>
    card.querySelectorAll('button').find(b => b.dataset.folderAgentAction === 'set_up');
  setUp().listeners.click();
  assert.deepEqual(plain(calls[0]), [
    'set_up',
    { selected: [], use_goal: true, goal: 'Finish two songs this month' }
  ]);
  label.children[0].checked = false;
  setUp().listeners.click();
  assert.equal(calls[1][1].use_goal, false);
});

test('optional tools can be unticked; required are locked; the plan follows the selection', async () => {
  const view = folderAgentReportView(
    reported({
      candidates: [
        { id: 'integration:x', name: 'X tools', required: true, action: 'install' },
        {
          id: 'skill:a@b',
          name: 'mix-notes',
          source: 'search',
          marketplace: 'skills.sh',
          ranked: true,
          action: 'add'
        },
        { id: 'mcp:notes', name: 'notes', ranked: true, action: 'already_here' }
      ],
      plan: {
        digest: 'with-skill',
        runnable: true,
        selected: ['skill:a@b'],
        lines: [{ kind: 'add_on', name: 'Adds skill mix-notes · found by search' }]
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
          selected: [],
          lines: [{ kind: 'folder', name: 'Nothing is moved, copied or read' }]
        }
      })
    };
  };
  const calls = [];
  const card = renderFolderAgentReport(view, stubDocument(), {
    fetchImpl,
    onAction: (action, _button, extra) => calls.push([action, extra])
  });
  const boxes = card.querySelectorAll('input').filter(box => box.dataset.toolId);
  assert.deepEqual(
    boxes.map(b => [b.dataset.toolId, b.checked, b.disabled]),
    [
      ['integration:x', true, true],
      ['skill:a@b', true, false]
    ]
  );
  boxes[1].checked = false;
  await boxes[1].listeners.change();
  assert.deepEqual(requests[0], [
    '/api/personal-assistant/folder-agent/ws-1/plan',
    { selected: [] }
  ]);
  assert.deepEqual(
    card.querySelectorAll('.folder-agent-card__line-name').map(n => n.textContent),
    ['Nothing is moved, copied or read']
  );
  card
    .querySelectorAll('button')
    .find(b => b.dataset.folderAgentAction === 'set_up')
    .listeners.click();
  assert.deepEqual(plain(calls[0]), ['set_up', { selected: [] }]);
  assert.equal(view.plan.digest, 'without');
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
  assert.equal(muted.length, 2);
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
