import test from 'node:test';
import assert from 'node:assert/strict';

import {
  chooseFolder,
  folderAgentActionPath,
  folderAgentCardView,
  folderAgentNeedsView,
  folderAgentReportView,
  folderAgentTileLine,
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
    action: 'Start',
    note: 'It looks before it touches anything. You confirm every step after.'
  });
  assert.deepEqual(plain(view), {
    token: 'fa-1',
    folder: 'Ori Music Demo',
    title: 'Start an agent on Ori Music Demo',
    field: 'What should it do?',
    brief: "Find out what's here and set it up",
    max: 300,
    action: 'Start',
    note: 'It looks before it touches anything. You confirm every step after.'
  });
  const doc = stubDocument();
  let started = null;
  const card = renderFolderAgentCard(view, doc, { onStart: brief => (started = brief) });
  assert.equal(text(card, '.folder-agent-card__title'), 'Start an agent on Ori Music Demo');
  const input = card.querySelector('.folder-agent-card__brief');
  assert.equal(input.maxLength, 300);
  input.value = 'Mix this song';
  card.querySelector('button').listeners.click();
  assert.equal(started, 'Mix this song');
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
