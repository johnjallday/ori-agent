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
  ensureRoleCatalog,
  folderAgentSetUpLabel,
  folderAgentTileFaces,
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
  assert.equal(text(card, '.folder-agent-card__who-title'), 'Hire');
  const input = card.querySelector('.folder-agent-card__brief');
  assert.equal(input.maxLength, 300);
  input.value = 'Mix this song';
  // A new agent by default: rename it, pick another model, give it a role.
  const name = card.querySelector('.folder-agent-card__who-name');
  assert.equal(name.value, 'Ori Music Demo Manager');
  const atlas = card
    .querySelectorAll('.folder-agent-card__who-agent-radio')
    .find(radio => radio.value === 'Atlas');
  assert.equal(atlas.checked, false);
  name.value = 'Song Keeper';
  name.listeners.input();
  const model = card.querySelector('.folder-agent-card__who-model');
  model.value = 'openai\u0000gpt-x';
  model.listeners.change();
  const role = card.querySelector('.folder-agent-card__who-role');
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
  atlas.checked = true;
  atlas.listeners.change();
  // The new agent's row is dimmed, not disabled, so typing in it chooses it again.
  assert.equal(card.querySelector('.folder-agent-card__who-new').dataset.selected, 'false');
  assert.equal(name.disabled, false);
  assert.equal(atlas.checked, true);
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
  assert.equal(card.querySelector('.folder-agent-card__who-model'), null);
  assert.match(text(card, '.folder-agent-card__who-note'), /No model is set up yet/);
  view.hire.mode = 'new';
  assert.deepEqual(plain(folderAgentHireChoice(view.hire)), { name: 'Songs Manager', mandate: '' });

  // Models but no system model: "No model yet" is preselected.
  const unset = folderAgentCardView({
    choice_token: 'fa-3',
    folder_name: 'Songs',
    hire: { models: [{ provider: 'claude_code', model: 'sonnet', label: 'sonnet (claude_code)' }] }
  });
  const picker = renderFolderAgentCard(unset, stubDocument(), {}).querySelector(
    '.folder-agent-card__who-model'
  );
  assert.equal(picker.children[0].textContent, 'No model yet');
  assert.equal(unset.hire.model, '');
  assert.equal(folderAgentHireChoice(unset.hire).model, undefined);
});

// A window with just what the hire section reads to draw a face: the shared
// renderer (recording what it is asked to draw), the role catalog that shapes
// the ring and emblem, and the character catalog.
function faceWindow({ rolesLoaded = true } = {}) {
  const roles = { loaded: rolesLoaded, resolve: null };
  const ready = new Promise(resolve => {
    roles.resolve = resolve;
  });
  const characters = { listeners: [], loads: 0, stopped: 0, byId: {} };
  const win = {
    drawn: [],
    roles,
    characters,
    AgentAvatar: {
      markup(input, options) {
        win.drawn.push({ input: plain(input), options: plain(options) });
        return `<span data-face="${input.name}"></span>`;
      }
    },
    RoleCatalog: {
      isLoaded: () => roles.loaded,
      entry: slug =>
        slug === 'orchestrator' ? { accent_color: '#2c7a4b', emblem: 'compass' } : null,
      ready: () => ready
    },
    CharacterCatalog: {
      get: id => characters.byId[id] || null,
      load: () => (characters.loads += 1),
      onChange(fn) {
        characters.listeners.push(fn);
        return () => (characters.stopped += 1);
      }
    }
  };
  return win;
}
const lastFace = win => win.drawn.at(-1);
const faceOf = card => card.querySelector('.folder-agent-card__who-face');
const hireCardView = (hire = {}) =>
  folderAgentCardView({
    choice_token: 'fa-9',
    folder_name: 'Downloads',
    hire: { agent_name: 'Downloads Manager', models: [], agents: [], ...hire }
  });
const hireCard = (win, hire) => renderFolderAgentCard(hireCardView(hire), stubDocument(), { win });

test('the hire card leads with the face the Agents page will draw for the agent it creates', () => {
  const win = faceWindow();
  const card = hireCard(win, {
    models: [
      { provider: 'claude', model: 'claude-sonnet-5-5', label: 'claude-sonnet-5-5 (claude)' }
    ],
    provider: 'claude',
    model: 'claude-sonnet-5-5'
  });
  const row = card.querySelector('.folder-agent-card__who-new');
  assert.equal(row.children[0].className, 'folder-agent-card__who-face');
  assert.equal(row.children[1].className, 'folder-agent-card__who-main');
  // The name, then the model as a chip under it.
  const [name, chip] = row.children[1].children;
  assert.equal(name.value, 'Downloads Manager');
  // Its text is the model picked; the native list sits over it.
  assert.equal(chip.className, 'folder-agent-card__who-chip');
  const [chipText, list] = chip.children;
  assert.equal(chipText.textContent, 'claude-sonnet-5-5 (claude)');
  assert.equal(chipText.attributes['aria-hidden'], 'true');
  assert.equal(list.className, 'folder-agent-card__who-model');
  assert.equal(list.attributes['aria-label'], 'Model');
  assert.equal(faceOf(card).innerHTML, '<span data-face="Downloads Manager"></span>');
  // Name, source, role and the role's ring and emblem: what agents-roster.js
  // passes for an agent hired as an orchestrator. A name alone draws another face.
  assert.deepEqual(lastFace(win), {
    input: {
      name: 'Downloads Manager',
      source: 'user',
      role: 'orchestrator',
      roleAccent: '#2c7a4b',
      roleEmblem: 'compass',
      appearance: { mode: 'generated', generated: {} }
    },
    options: { size: 48, className: 'folder-agent-card__who-avatar' }
  });
  assert.equal(win.characters.loads, 1);
});

test('the face follows the name as it is typed, and is neutral without one', () => {
  const win = faceWindow();
  const card = hireCard(win);
  const name = card.querySelector('.folder-agent-card__who-name');
  const typed = value => {
    name.value = value;
    name.listeners.input();
    return lastFace(win).input;
  };
  // The server saves a run of spaces as one, so the face is drawn from that.
  assert.equal(typed('Song   Keeper').name, 'Song Keeper');
  assert.deepEqual(typed(''), { name: '' });
  assert.deepEqual(typed('   '), { name: '' });
  assert.equal(typed('Atlas').name, 'Atlas');
  assert.equal(faceOf(card).innerHTML, '<span data-face="Atlas"></span>');
});

test('the face is neutral until the role catalog arrives, never a wrong one', async () => {
  const win = faceWindow({ rolesLoaded: false });
  const card = hireCard(win);
  assert.deepEqual(lastFace(win).input, { name: '' });
  win.roles.loaded = true;
  win.roles.resolve();
  await new Promise(resolve => setTimeout(resolve, 0));
  assert.equal(lastFace(win).input.name, 'Downloads Manager');
  assert.equal(lastFace(win).input.roleEmblem, 'compass');
  assert.equal(faceOf(card).innerHTML, '<span data-face="Downloads Manager"></span>');
  // A page with no role catalog at all draws the neutral face too.
  const bare = faceWindow();
  delete bare.RoleCatalog;
  hireCard(bare);
  assert.deepEqual(lastFace(bare).input, { name: '' });
});

test('a character catalog change redraws the face, and a card that left the page stops listening', () => {
  const win = faceWindow();
  const card = hireCard(win);
  const drawn = win.drawn.length;
  win.characters.listeners[0]();
  assert.equal(win.drawn.length, drawn + 1);
  card.querySelector('.folder-agent-card__who').isConnected = false;
  win.characters.listeners[0]();
  assert.equal(win.drawn.length, drawn + 1);
  assert.equal(win.characters.stopped, 1);
});

test('without the shared renderer the face is the first letter of the name', () => {
  const card = hireCard(null);
  assert.equal(faceOf(card).textContent, 'D');
  const name = card.querySelector('.folder-agent-card__who-name');
  name.value = '';
  name.listeners.input();
  assert.equal(faceOf(card).textContent, '?');
});

test('Role waits behind Customize, closed unless it already holds text', () => {
  const closed = hireCard(null);
  const details = closed.querySelector('.folder-agent-card__who-customize');
  assert.equal(details.open, false);
  assert.equal(text(closed, '.folder-agent-card__who-customize-toggle'), 'Customize');
  const role = details.querySelector('.folder-agent-card__who-role');
  assert.equal(role.placeholder, 'Same as what it should do');
  assert.equal(role.maxLength, 300);
  const filled = hireCard(null, { mandate: 'Keep my downloads tidy' });
  assert.equal(filled.querySelector('.folder-agent-card__who-customize').open, true);
  assert.equal(
    filled.querySelector('.folder-agent-card__who-role').value,
    'Keep my downloads tidy'
  );
});

test('the model chip lists the same models, and what it picks is what Start sends', () => {
  const view = hireCardView({
    models: [
      { provider: 'claude', model: 'claude-sonnet-5-5', label: 'claude-sonnet-5-5 (claude)' },
      { provider: 'openai', model: 'gpt-x', label: 'gpt-x (openai)' }
    ]
  });
  const card = renderFolderAgentCard(view, stubDocument(), {});
  const chip = card.querySelector('.folder-agent-card__who-model');
  // No system model: "No model yet" leads, so a hire without one stays possible.
  assert.deepEqual(
    chip.children.map(option => option.textContent),
    ['No model yet', 'claude-sonnet-5-5 (claude)', 'gpt-x (openai)']
  );
  assert.equal(folderAgentHireChoice(view.hire).model, undefined);
  const chipText = () => card.querySelector('.folder-agent-card__who-chip-text').textContent;
  assert.equal(chipText(), 'No model yet');
  chip.value = ['openai', 'gpt-x'].join(String.fromCharCode(0));
  chip.listeners.change();
  assert.equal(chipText(), 'gpt-x (openai)');
  assert.deepEqual(plain(folderAgentHireChoice(view.hire)), {
    name: 'Downloads Manager',
    mandate: '',
    provider: 'openai',
    model: 'gpt-x'
  });
});

// The owner's own agents, as the hire payload names them (name and model only).
const SAVED = [{ name: 'Atlas', model: 'claude-sonnet-5-5' }, { name: 'Beta' }, { name: 'Cato' }];
const agentItems = card => card.querySelectorAll('.folder-agent-card__who-agent');
const agentRadio = (card, name) =>
  card.querySelectorAll('.folder-agent-card__who-agent-radio').find(radio => radio.value === name);
const agentItem = (card, name) =>
  agentItems(card).find(item => text(item, '.folder-agent-card__who-agent-name') === name);
const pick = (card, name) => {
  const radio = agentRadio(card, name);
  radio.checked = true;
  radio.listeners.change();
};
// What the card marks as chosen: the new agent's row or one agent's face.
const chosen = card =>
  [...card.querySelectorAll('.folder-agent-card__who-new'), ...agentItems(card)].filter(
    node => node.dataset.selected === 'true'
  );
const listOf = agents => async () => ({ ok: true, json: async () => ({ agents }) });
// settle lets an answer land and its redraw run, and lets "nothing happened" be
// asserted. It was 25 ms and failed twice on a saturated machine (the whole suite
// runs in parallel), so it leaves a wider margin; a wait for something that must
// happen uses untilTrue, defined further down.
const settle = () => new Promise(resolve => setTimeout(resolve, 100));
const agentDraws = win => win.drawn.filter(draw => draw.options.size === 40);

test('your agents are a row of faces under "Or one of your agents", not a dropdown', () => {
  const win = faceWindow();
  const card = renderFolderAgentCard(hireCardView({ agents: SAVED }), stubDocument(), {
    win,
    fetchImpl: listOf([])
  });
  const heading = card.querySelector('.folder-agent-card__who-heading');
  assert.equal(heading.textContent, 'Or one of your agents');
  const row = card.querySelector('.folder-agent-card__who-agents');
  assert.equal(row.attributes.role, 'radiogroup');
  assert.equal(row.attributes['aria-labelledby'], heading.id);
  assert.deepEqual(
    agentItems(card).map(item => text(item, '.folder-agent-card__who-agent-name')),
    ['Atlas', 'Beta', 'Cato']
  );
  // The name is under the face; the title carries it when the name is cut short.
  assert.equal(agentItem(card, 'Atlas').attributes.title, 'Atlas');
  // Each choice is announced with the agent's name and model.
  assert.equal(agentRadio(card, 'Atlas').attributes['aria-label'], 'Atlas, claude-sonnet-5-5');
  assert.equal(agentRadio(card, 'Beta').attributes['aria-label'], 'Beta');
  const radios = card.querySelectorAll('.folder-agent-card__who-agent-radio');
  assert.equal(new Set(radios.map(radio => radio.name)).size, 1);
  assert.ok(radios.every(radio => radio.type === 'radio'));
  assert.equal(card.querySelectorAll('select').length, 0);
  // About 40px, through the shared renderer, in the agent-sized class.
  assert.equal(agentDraws(win).length, 3);
  assert.equal(
    agentDraws(win)[0].options.className,
    'folder-agent-card__who-avatar folder-agent-card__who-avatar--agent'
  );
});

test('with no agents of your own there is no row, no heading and no request for them', async () => {
  let asked = 0;
  const card = renderFolderAgentCard(hireCardView(), stubDocument(), {
    win: faceWindow(),
    fetchImpl: async () => {
      asked += 1;
      return { ok: true, json: async () => ({ agents: [] }) };
    }
  });
  assert.equal(card.querySelector('.folder-agent-card__who-reuse'), null);
  assert.equal(card.querySelector('.folder-agent-card__who-heading'), null);
  assert.equal(card.querySelector('.folder-agent-card__who-agents'), null);
  await settle();
  assert.equal(asked, 0);
});

test('each agent shows its real face once the list answers, and a fallback face until then', async () => {
  const win = faceWindow();
  win.characters.byId.fox = { id: 'fox', assets: { portrait: '/characters/fox.png' } };
  const records = [
    {
      name: 'Atlas',
      source: 'user',
      role: 'orchestrator',
      appearance: { mode: 'character', character: { catalog_id: 'fox' } }
    },
    {
      name: 'Beta',
      source: 'user',
      role: '',
      appearance: { mode: 'uploaded', uploaded: { image: 'beta.png' } }
    },
    {
      name: 'Cato',
      source: 'cli',
      role: 'cli_agent',
      appearance: { mode: 'generated', generated: { color: '#112233' } }
    }
  ];
  const asked = [];
  renderFolderAgentCard(hireCardView({ agents: SAVED }), stubDocument(), {
    win,
    fetchImpl: async url => {
      asked.push(url);
      return { ok: true, json: async () => ({ agents: records }) };
    }
  });
  // Until the list answers: the name's generated face.
  const fallback = name => ({ name, source: 'user', appearance: {} });
  assert.deepEqual(
    agentDraws(win).map(draw => draw.input),
    [fallback('Atlas'), fallback('Beta'), fallback('Cato')]
  );
  await settle();
  // Then each agent as the Agents page draws it: the appearance exactly as
  // returned, the source and role, the role's ring, and the character.
  assert.deepEqual(asked, ['/api/agents/dashboard/list']);
  const [atlas, beta, cato] = agentDraws(win)
    .slice(-3)
    .map(draw => draw.input);
  assert.deepEqual(atlas, {
    name: 'Atlas',
    source: 'user',
    role: 'orchestrator',
    roleAccent: '#2c7a4b',
    roleEmblem: 'compass',
    appearance: records[0].appearance,
    character: { id: 'fox', assets: { portrait: '/characters/fox.png' } }
  });
  assert.deepEqual(beta, {
    name: 'Beta',
    source: 'user',
    role: '',
    roleAccent: '',
    roleEmblem: '',
    appearance: records[1].appearance,
    character: null
  });
  assert.equal(cato.source, 'cli');
  assert.equal(cato.role, 'cli_agent');
  assert.deepEqual(cato.appearance, records[2].appearance);
});

test('a list that cannot be read leaves the fallback faces and the choice still works', async () => {
  const unreadable = [
    async () => {
      throw new Error('offline');
    },
    async () => ({ ok: false }),
    async () => ({
      ok: true,
      json: async () => {
        throw new Error('not json');
      }
    })
  ];
  for (const fetchImpl of unreadable) {
    const win = faceWindow();
    const view = hireCardView({ agents: SAVED });
    const card = renderFolderAgentCard(view, stubDocument(), { win, fetchImpl });
    const drawn = win.drawn.length;
    await settle();
    assert.equal(win.drawn.length, drawn);
    pick(card, 'Beta');
    assert.deepEqual(plain(folderAgentHireChoice(view.hire)), { existing: 'Beta' });
  }
});

test('the list is asked for once per card, and an answer after the card left is ignored', async () => {
  const records = SAVED.map(agent => ({ name: agent.name, role: '', appearance: {} }));
  const win = faceWindow();
  let asked = 0;
  renderFolderAgentCard(hireCardView({ agents: SAVED }), stubDocument(), {
    win,
    fetchImpl: async () => {
      asked += 1;
      return { ok: true, json: async () => ({ agents: records }) };
    }
  });
  await settle();
  // A character catalog change redraws the faces; it does not ask again.
  win.characters.listeners[0]();
  await settle();
  assert.equal(asked, 1);

  const late = faceWindow();
  let release = null;
  const gate = new Promise(resolve => {
    release = resolve;
  });
  const card = renderFolderAgentCard(hireCardView({ agents: SAVED }), stubDocument(), {
    win: late,
    fetchImpl: async () => {
      await gate;
      return { ok: true, json: async () => ({ agents: records }) };
    }
  });
  card.querySelector('.folder-agent-card__who').isConnected = false;
  const drawn = late.drawn.length;
  release();
  await settle();
  assert.equal(late.drawn.length, drawn);
});

test('exactly one of the new agent and your agents is chosen; typing in the new one chooses it again', () => {
  const view = hireCardView({ agents: SAVED });
  const card = renderFolderAgentCard(view, stubDocument(), { win: faceWindow() });
  const fresh = card.querySelector('.folder-agent-card__who-new');
  const name = card.querySelector('.folder-agent-card__who-name');
  const only = (node, mode) => {
    assert.deepEqual(chosen(card), [node]);
    assert.equal(view.hire.mode, mode);
    // Role belongs to the new agent: it is dimmed whenever the new agent is not chosen.
    assert.equal(
      card.querySelector('.folder-agent-card__who-customize').dataset.selected,
      String(mode === 'new')
    );
    // The radios agree with the marks: one checked at most, and it is the chosen one.
    const checked = card
      .querySelectorAll('.folder-agent-card__who-agent-radio')
      .filter(radio => radio.checked)
      .map(radio => radio.value);
    assert.deepEqual(checked, mode === 'existing' ? [node.querySelector('input').value] : []);
  };
  only(fresh, 'new');

  pick(card, 'Beta');
  only(agentItem(card, 'Beta'), 'existing');
  // The request for a reused agent is what it was: only its name.
  assert.deepEqual(plain(folderAgentHireChoice(view.hire)), { existing: 'Beta' });

  pick(card, 'Cato');
  only(agentItem(card, 'Cato'), 'existing');
  assert.deepEqual(plain(folderAgentHireChoice(view.hire)), { existing: 'Cato' });

  name.value = 'Mine';
  name.listeners.input();
  only(fresh, 'new');
  assert.deepEqual(plain(folderAgentHireChoice(view.hire)), { name: 'Mine', mandate: '' });

  pick(card, 'Atlas');
  fresh.listeners.click();
  only(fresh, 'new');

  pick(card, 'Atlas');
  fresh.listeners.focusin();
  only(fresh, 'new');
});

test('a working agent wears its face on its tile only while the tile has a line (visual-hire FR17)', () => {
  const win = faceWindow();
  const albums = {
    name: 'Albums Manager',
    source: 'user',
    role: 'orchestrator',
    appearance: { mode: 'generated', generated: {} }
  };
  const records = new Map([[albums.name, albums]]);
  const views = [
    { workspace_id: 'ws-1', agent_name: 'Albums Manager', state: 'looking', progress_line: 'x' },
    { workspace_id: 'ws-2', agent_name: 'Beta Manager', state: 'setting_up' },
    { workspace_id: 'ws-3', agent_name: 'Gamma Manager', state: 'reported' },
    { workspace_id: 'ws-4', agent_name: 'Delta Manager', state: 'failed' },
    { workspace_id: 'ws-5', state: 'looking' }
  ];
  const faces = folderAgentTileFaces(views, records, win);
  // Only the tiles that show a line, and only for an agent that has a name.
  assert.deepEqual(Object.keys(faces), ['ws-1', 'ws-2']);
  assert.equal(faces['ws-1'], '<span data-face="Albums Manager"></span>');
  // At the tile's size through the shared renderer. An agent the list has is drawn
  // as the hire card and the Agents page draw it; one it has not yet is drawn as
  // its name's own face, and the tile redraws when the list answers.
  assert.equal(win.drawn[0].options.size, 32);
  assert.deepEqual(win.drawn[0].input, {
    name: 'Albums Manager',
    source: 'user',
    role: 'orchestrator',
    roleAccent: '#2c7a4b',
    roleEmblem: 'compass',
    appearance: albums.appearance,
    character: null
  });
  assert.deepEqual(win.drawn[1].input, { name: 'Beta Manager', source: 'user', appearance: {} });
  // Nothing to draw with, or nothing to draw: no faces.
  assert.deepEqual(folderAgentTileFaces(views, records, null), {});
  assert.deepEqual(folderAgentTileFaces(null, records, win), {});

  // An agent whose record is still being asked for wears no face rather than its
  // name's own, which is not its real one and would flash first. The agents that
  // are not pending are unaffected; once the lookup ends it is drawn.
  const waiting = folderAgentTileFaces(views, records, win, new Set(['Albums Manager']));
  assert.deepEqual(Object.keys(waiting), ['ws-2']);
  assert.deepEqual(Object.keys(folderAgentTileFaces(views, records, win, new Set())), [
    'ws-1',
    'ws-2'
  ]);
});

// A page double that records the scripts added to its head.
function headDocument({ head = true } = {}) {
  const added = [];
  return {
    added,
    createElement: tag => ({ tag, src: '', async: false, onload: null, onerror: null }),
    head: head ? { appendChild: node => added.push(node) } : undefined
  };
}

test('the role catalog is fetched only when a face needs it, once per window, and never blocks a face (visual-hire FR4)', async () => {
  // Already on the page (the Agents page loads it): nothing is added.
  const have = faceWindow();
  have.roles.resolve();
  const owned = headDocument();
  assert.equal(await ensureRoleCatalog(owned, have), have.RoleCatalog);
  assert.deepEqual(owned.added, []);

  // Not on the page: one script is added however many faces ask, and the answer
  // is the catalog the script installed.
  const bare = {};
  const page = headDocument();
  const first = ensureRoleCatalog(page, bare);
  const second = ensureRoleCatalog(page, bare);
  assert.equal(first, second);
  assert.equal(page.added.length, 1);
  assert.equal(page.added[0].src, '/js/modules/role-catalog.js');
  const catalog = { ready: () => Promise.resolve() };
  bare.RoleCatalog = catalog;
  page.added[0].onload();
  assert.equal(await first, catalog);
  // Once it is there, later asks use it and add nothing.
  assert.equal(await ensureRoleCatalog(page, bare), catalog);
  assert.equal(page.added.length, 1);

  // A script that fails, a page with no head, and no window all answer null.
  const broken = headDocument();
  const failing = ensureRoleCatalog(broken, {});
  broken.added[0].onerror();
  assert.equal(await failing, null);
  assert.equal(await ensureRoleCatalog(headDocument({ head: false }), {}), null);
  assert.equal(await ensureRoleCatalog(headDocument(), null), null);
});

test('a page without the role catalog loads it when the card is drawn, and the real face replaces the neutral one (visual-hire FR4)', async () => {
  const win = faceWindow();
  const roles = win.RoleCatalog;
  delete win.RoleCatalog;
  const doc = stubDocument();
  const added = [];
  doc.head = { appendChild: node => added.push(node) };
  const card = renderFolderAgentCard(hireCardView(), doc, { win });
  // The neutral face while it loads, and the page was asked for the script once.
  assert.deepEqual(lastFace(win).input, { name: '' });
  assert.equal(added.length, 1);
  assert.equal(added[0].src, '/js/modules/role-catalog.js');
  // The script arrives and installs the catalog: the face is redrawn as the Agents page draws it.
  win.RoleCatalog = roles;
  win.roles.resolve();
  added[0].onload();
  await settle();
  assert.equal(lastFace(win).input.roleEmblem, 'compass');
  assert.equal(faceOf(card).innerHTML, '<span data-face="Downloads Manager"></span>');
});

test('no face is drawn on a tile until the role catalog is here, and no page pays for it unless it draws one (visual-hire FR4)', () => {
  const views = [{ workspace_id: 'ws-1', agent_name: 'Albums Manager', state: 'looking' }];
  const records = new Map();
  assert.deepEqual(Object.keys(folderAgentTileFaces(views, records, faceWindow())), ['ws-1']);
  assert.deepEqual(folderAgentTileFaces(views, records, faceWindow({ rolesLoaded: false })), {});
  const bare = faceWindow();
  delete bare.RoleCatalog;
  assert.deepEqual(folderAgentTileFaces(views, records, bare), {});

  // The shared head must not load it (the README capture fails any page that makes a
  // request it does not expect); the Agents page, which draws from it, does.
  const read = path => readFileSync(new URL(path, import.meta.url), 'utf8');
  assert.doesNotMatch(read('../../../templates/layout/head.tmpl'), /role-catalog\.js/);
  assert.match(read('../../../templates/pages/agents-roster.tmpl'), /role-catalog\.js/);
});

test('a saved manager of the same name is chosen when the card opens, and the chosen face is scrolled into view', () => {
  const win = faceWindow();
  const queued = [];
  win.requestAnimationFrame = run => queued.push(run);
  const view = hireCardView({
    agent_name: 'Songs Manager',
    agents: [{ name: 'Atlas' }, { name: 'Songs Manager' }],
    existing: 'Songs Manager'
  });
  const card = renderFolderAgentCard(view, stubDocument(), { win });
  assert.equal(view.hire.mode, 'existing');
  assert.deepEqual(chosen(card), [agentItem(card, 'Songs Manager')]);
  assert.equal(card.querySelector('.folder-agent-card__who-new').dataset.selected, 'false');

  const scrolled = [];
  agentItems(card).forEach(item => {
    item.scrollIntoView = options =>
      scrolled.push([text(item, '.folder-agent-card__who-agent-name'), plain(options)]);
  });
  const near = { block: 'nearest', inline: 'nearest' };
  queued.forEach(run => run());
  assert.deepEqual(scrolled, [['Songs Manager', near]]);

  // Choosing another face brings that one into view too.
  pick(card, 'Atlas');
  assert.deepEqual(scrolled.at(-1), ['Atlas', near]);
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

// Outcomes instead of actions (setup-card-clarity FR1–FR4): a chosen step reads
// as the host's outcome items before Set it up; its every action is one
// button away; from the run on, every action shows with its live state.
const HOME = 'music-production-home';
// The run's words for an action, and the shorter outcome item the host gives it.
const creates = 'Creates your Music Production Home';
const createsItem = 'Creates Music Production Home';
const listsSongs = 'Lists 8 songs on its shelf';
const listsItem = 'Lists 8 songs';
const hiresAgents = "Hires the Home's agents";
const homeLine = (kind, name, outcome = '') => ({
  kind,
  name,
  step: HOME,
  ...(outcome ? { outcome } : {})
});
const assurance = {
  kind: 'folder',
  name: 'Nothing in Ori Music Demo is moved, copied or read.'
};
// What the server sends for a new Home with its plugin not yet installed.
const newHomeLines = [
  homeLine('provider', 'Installs Music Project Management'),
  homeLine('home', creates, createsItem),
  homeLine('hire', "Fills the Home's roles", hiresAgents),
  homeLine('library', 'Adds Ori Music Demo to your Music Production Home'),
  homeLine('songs', listsSongs, listsItem),
  homeLine('scopes', 'Looks into 2 folders for songs'),
  homeLine('place', 'Places Ori Music Demo inside your Music Production Home'),
  assurance
];
const homePlanOf = (lines, installs = [providerInstall]) => ({
  digest: 'home',
  runnable: true,
  source: 'default',
  selected: [HOME],
  steps: [
    {
      handle: HOME,
      kind: 'home',
      name: 'Music Production Home',
      title: 'Create your Music Production Home and put Ori Music Demo on its shelf (8 songs)',
      chosen: true
    }
  ],
  installs,
  lines
});
const homeCard = (lines, installs, overrides = {}) =>
  renderFolderAgentReport(
    folderAgentReportView(
      reported({
        model_sentence: '',
        tools_sentence: '',
        plan: homePlanOf(lines, installs),
        ...overrides
      })
    ),
    stubDocument(),
    {}
  );
const outcomeText = card => text(card, '.folder-agent-card__outcomes');
const detailsToggle = card => card.querySelector('.folder-agent-card__details-toggle');
const actionNames = node =>
  node.querySelectorAll('.folder-agent-card__action-name').map(n => n.textContent);

test('a chosen step reads as three outcomes, with its other actions behind a closed button', () => {
  const card = homeCard(newHomeLines);
  assert.equal(outcomeText(card), `${createsItem} · ${hiresAgents} · ${listsItem}`);
  // The line and its button share one row.
  const row = card.querySelector('.folder-agent-card__outcome-row');
  assert.ok(row.querySelector('.folder-agent-card__outcomes'));
  assert.ok(row.querySelector('.folder-agent-card__details-toggle'));
  // The install is the block's; the other six lines are the step's actions.
  const toggle = detailsToggle(card);
  assert.equal(toggle.tagName, 'BUTTON');
  assert.equal(toggle.type, 'button');
  assert.equal(toggle.textContent, 'Show all 6 actions');
  assert.equal(toggle.attributes['aria-expanded'], 'false');
  const all = card.querySelector('.folder-agent-card__actions-line');
  assert.equal(toggle.attributes['aria-controls'], all.id);
  assert.equal(all.hidden, true);
  assert.equal(card.querySelector('.folder-agent-card__outcomes').hidden, false);
  // The install is listed once, in the block.
  assert.equal(card.querySelectorAll('.folder-agent-card__install').length, 1);
  assert.ok(!actionNames(card).includes('Installs Music Project Management'));
});

test('opening the button shows every action in run order, and a redraw keeps it open', () => {
  const view = reported({
    model_sentence: '',
    tools_sentence: '',
    plan: homePlanOf(newHomeLines)
  });
  const draw = () => renderFolderAgentReport(folderAgentReportView(view), stubDocument(), {});
  const card = draw();
  const toggle = detailsToggle(card);
  toggle.listeners.click();
  assert.equal(toggle.attributes['aria-expanded'], 'true');
  assert.equal(toggle.textContent, 'Show less');
  assert.equal(card.querySelector('.folder-agent-card__outcomes').hidden, true);
  const all = card.querySelector('.folder-agent-card__actions-line');
  assert.equal(all.hidden, false);
  assert.deepEqual(actionNames(all), [
    creates,
    "Fills the Home's roles",
    'Adds Ori Music Demo to your Music Production Home',
    listsSongs,
    'Looks into 2 folders for songs',
    'Places Ori Music Demo inside your Music Production Home'
  ]);
  // A redraw (a tick box, a swap) keeps what the owner opened.
  const again = draw();
  assert.equal(detailsToggle(again).attributes['aria-expanded'], 'true');
  assert.equal(again.querySelector('.folder-agent-card__actions-line').hidden, false);
  // Closing it again restores the outcome line.
  detailsToggle(again).listeners.click();
  assert.equal(detailsToggle(again).attributes['aria-expanded'], 'false');
  assert.equal(detailsToggle(again).textContent, 'Show all 6 actions');
  assert.equal(again.querySelector('.folder-agent-card__outcomes').hidden, false);
  assert.equal(detailsToggle(draw()).attributes['aria-expanded'], 'false');
});

test('a Home that exists says nothing of creating it; hires appear only when there are some', () => {
  // The Home is there and every role is filled: one item, nothing created.
  const joined = homeCard(
    [
      homeLine('library', 'Adds Ori Music Demo to your Music Production Home'),
      homeLine('songs', listsSongs, listsItem),
      homeLine('place', 'Places Ori Music Demo inside your Music Production Home'),
      assurance
    ],
    []
  );
  assert.equal(outcomeText(joined), listsItem);
  assert.ok(!outcomeText(joined).includes('Creates'));
  assert.ok(!outcomeText(joined).includes('Hires'));
  assert.equal(detailsToggle(joined).textContent, 'Show all 3 actions');
  // Two roles missing: two hire lines read as one item.
  const hiring = homeCard(
    [
      homeLine('hire', 'Hires Portfolio Manager', 'Hires 2 agents'),
      homeLine('hire', 'Hires Library Manager', 'Hires 2 agents'),
      homeLine('library', 'Adds Ori Music Demo to your Music Production Home'),
      homeLine('songs', listsSongs, listsItem),
      homeLine('place', 'Places Ori Music Demo inside your Music Production Home'),
      assurance
    ],
    []
  );
  assert.equal(outcomeText(hiring), `Hires 2 agents · ${listsItem}`);
  assert.equal(detailsToggle(hiring).textContent, 'Show all 5 actions');
});

test('a step whose every action is an outcome has no button; a step with no marks shows its actions as before', () => {
  const tools = renderFolderAgentReport(
    folderAgentReportView(
      reported({ model_sentence: '', tools_sentence: '', plan: installPlan(['reaper-tools']) })
    ),
    stubDocument(),
    {}
  );
  // No marks: the actions stay on their muted line, nothing is hidden.
  assert.equal(tools.querySelector('.folder-agent-card__outcomes'), null);
  assert.equal(detailsToggle(tools), null);
  assert.deepEqual(actionNames(tools.querySelector('.folder-agent-card__actions-line')), [
    'Connects REAPER tools to Ori Music Demo'
  ]);
  // Marked: the connection is the one outcome and it is all there is to open.
  const connects = 'Connects REAPER tools to Ori Music Demo';
  const marked = installPlan(['reaper-tools']);
  marked.lines = marked.lines.map(line =>
    line.kind === 'workspace' ? { ...line, outcome: connects } : line
  );
  const card = renderFolderAgentReport(
    folderAgentReportView(reported({ model_sentence: '', tools_sentence: '', plan: marked })),
    stubDocument(),
    {}
  );
  assert.equal(outcomeText(card), connects);
  assert.equal(detailsToggle(card), null);
  assert.equal(card.querySelector('.folder-agent-card__actions-line'), null);
});

test('from the moment the run starts every action shows with its live state and no button', () => {
  const lines = newHomeLines.slice(0, -1).map((line, i) => ({
    ...line,
    state: i < 2 ? 'done' : i === 2 ? 'working' : 'waiting',
    progress: i === 2 ? 'Hiring the Portfolio Manager' : ''
  }));
  const run = (state, status) =>
    renderFolderAgentReport(
      folderAgentReportView(
        reported({
          state,
          actions: [],
          model_sentence: '',
          tools_sentence: '',
          plan: homePlanOf(newHomeLines),
          setup: { status, started_at: '2026-11-11T10:00:00Z', lines }
        })
      ),
      stubDocument(),
      {}
    );
  for (const card of [run('setting_up', 'running'), run('set_up', 'stopped')]) {
    assert.equal(card.querySelector('.folder-agent-card__outcomes'), null);
    assert.equal(detailsToggle(card), null);
    // The install row is the block's; every other action is listed, in order.
    assert.deepEqual(actionNames(card.querySelector('.folder-agent-card__actions-line')), [
      creates,
      "Fills the Home's roles",
      'Adds Ori Music Demo to your Music Production Home',
      listsSongs,
      'Looks into 2 folders for songs',
      'Places Ori Music Demo inside your Music Production Home'
    ]);
  }
  // Each action keeps the state the run gave its line.
  assert.deepEqual(
    run('setting_up', 'running')
      .querySelectorAll('.folder-agent-card__action')
      .map(a => a.dataset.state),
    ['done', 'working', 'waiting', 'waiting', 'waiting', 'waiting']
  );
});

// Named hires (setup-card-clarity FR6, FR7, FR9, FR10, FR14): the agents a Home
// step will hire, read-only, directly under the install block.
const MODEL = 'claude-sonnet-5-5 (claude)';
const hire = (name, extra = {}) => ({ step: HOME, name, model: MODEL, ...extra });
const hiresCard = (hires, hiresNote = '', options = {}) =>
  renderFolderAgentReport(
    folderAgentReportView(
      reported({
        model_sentence: '',
        tools_sentence: '',
        plan: {
          ...homePlanOf(options.lines || newHomeLines, options.installs),
          hires,
          hires_note: hiresNote
        },
        ...(options.report || {})
      })
    ),
    stubDocument(),
    {}
  );
const hiresBlock = card => card.querySelector('.folder-agent-card__hires');
const classesOf = node => node.className.split(' ');

test('the hires block is absent when nobody is hired', () => {
  assert.equal(hiresBlock(hiresCard([])), null);
  // A plan from before the host named hires has none either.
  const old = renderFolderAgentReport(
    folderAgentReportView(
      reported({ model_sentence: '', tools_sentence: '', plan: homePlanOf(newHomeLines) })
    ),
    stubDocument(),
    {}
  );
  assert.equal(hiresBlock(old), null);
});

test('one hire: its name, what it does and its model, one link to Agents, nothing to change', () => {
  const card = hiresCard([
    hire('Portfolio Manager', { job: 'Coordinates status and priorities.' })
  ]);
  const block = hiresBlock(card);
  assert.equal(text(block, '.folder-agent-card__heading'), 'Hires 1 agent');
  const rows = block.querySelectorAll('.folder-agent-card__hire');
  assert.equal(rows.length, 1);
  assert.equal(text(rows[0], '.folder-agent-card__hire-name'), 'Portfolio Manager');
  assert.equal(text(rows[0], '.folder-agent-card__hire-job'), 'Coordinates status and priorities.');
  assert.equal(text(rows[0], '.folder-agent-card__hire-model'), MODEL);
  assert.equal(rows[0].dataset.step, HOME);
  // The reading order, which a narrow card wraps in: name, job, model.
  assert.deepEqual(
    rows[0].children.map(child =>
      classesOf(child).find(name => name.startsWith('folder-agent-card__hire-'))
    ),
    ['folder-agent-card__hire-name', 'folder-agent-card__hire-job', 'folder-agent-card__hire-model']
  );
  // One link, to Agents; no rename, no model picker, no opt-out (decision 2A).
  const links = block.querySelectorAll('a');
  assert.deepEqual(
    links.map(link => [link.textContent, link.href]),
    [['Agents', '/agents']]
  );
  for (const tag of ['input', 'select', 'button', 'textarea'])
    assert.equal(block.querySelectorAll(tag).length, 0, `a ${tag} in the hires block`);
  assert.equal(block.querySelectorAll('.folder-agent-card__tool-pick').length, 0);
});

test('several hires keep their order; a missing job, model or note leaves its part out or says so', () => {
  const card = hiresCard([
    hire('Portfolio Manager', { job: 'Plans the work.' }),
    hire('Library Manager 2', { note: 'Your existing Library Manager stays as it is.' }),
    hire('Third', { model: '' })
  ]);
  const block = hiresBlock(card);
  assert.equal(text(block, '.folder-agent-card__heading'), 'Hires 3 agents');
  const rows = block.querySelectorAll('.folder-agent-card__hire');
  assert.deepEqual(
    rows.map(row => text(row, '.folder-agent-card__hire-name')),
    ['Portfolio Manager', 'Library Manager 2', 'Third']
  );
  // No job from the provider: no empty job line.
  assert.equal(rows[1].querySelector('.folder-agent-card__hire-job'), null);
  // A taken name is hired beside the owner's own agent, and the row says so.
  assert.equal(
    text(rows[1], '.folder-agent-card__hire-note'),
    'Your existing Library Manager stays as it is.'
  );
  assert.equal(rows[0].querySelector('.folder-agent-card__hire-note'), null);
  // No model can run yet: the row says so, and the run will ask for one.
  assert.equal(text(rows[2], '.folder-agent-card__hire-model'), 'No model yet');
  assert.equal(text(rows[0], '.folder-agent-card__hire-model'), MODEL);
});

test('when the host cannot name the hires it says so in one sentence, with no rows and no link', () => {
  const sentence = "Hires the Home's agents; each is named as it is hired";
  const block = hiresBlock(hiresCard([], sentence));
  assert.equal(text(block, '.folder-agent-card__hires-note'), sentence);
  assert.equal(block.querySelectorAll('.folder-agent-card__hire').length, 0);
  assert.equal(block.querySelector('.folder-agent-card__heading'), null);
  assert.equal(block.querySelectorAll('a').length, 0);
});

test('the block sits directly under the install block, before the assurance, and in its place when nothing installs', () => {
  const order = card =>
    card
      .querySelector('.folder-agent-card__run')
      .children.map(child =>
        classesOf(child).find(name => /__(plan|installs|hires|line)$/.test(name))
      )
      .filter(Boolean);
  const withInstall = hiresCard([hire('Portfolio Manager')]);
  const kinds = order(withInstall);
  assert.deepEqual(kinds.slice(-4), [
    'folder-agent-card__plan',
    'folder-agent-card__installs',
    'folder-agent-card__hires',
    'folder-agent-card__line'
  ]);
  // The assurance note follows the block.
  const run = withInstall.querySelector('.folder-agent-card__run');
  const last = run.children[run.children.length - 1];
  assert.equal(last.dataset.kind, 'folder');
  // Nothing installs: the hires block follows the steps directly.
  const noInstalls = order(hiresCard([hire('Portfolio Manager')], '', { installs: [] }));
  assert.deepEqual(noInstalls.slice(-3), [
    'folder-agent-card__plan',
    'folder-agent-card__hires',
    'folder-agent-card__line'
  ]);
  assert.equal(
    hiresCard([hire('Portfolio Manager')], '', { installs: [] }).querySelector(
      '.folder-agent-card__installs'
    ),
    null
  );
});

test('the hires block is drawn before Set it up only: the run and its receipt list what happened', () => {
  const lines = newHomeLines.slice(0, -1).map(line => ({ ...line, state: 'done' }));
  for (const [state, status] of [
    ['setting_up', 'running'],
    ['set_up', 'done'],
    ['set_up', 'stopped']
  ]) {
    const card = hiresCard([hire('Portfolio Manager')], '', {
      report: {
        state,
        actions: [],
        setup: { status, started_at: '2026-11-11T10:00:00Z', lines }
      }
    });
    assert.equal(hiresBlock(card), null, `a hires block in the ${status} card`);
  }
});

// One main path (setup-card-clarity FR15-FR20): a step that cannot run with a
// chosen one is not in the main list; it waits under "Other ways to set this
// up", collapsed, with what taking it gives up and "Use this instead".
const GROUP = 'group';
const HOME_TITLE =
  'Create your Music Production Home and put Ori Music Demo on its shelf (8 songs)';
const GROUP_TITLE = 'Turn Ori Music Demo into a group with one workspace per song (8)';
const homeTradeoff =
  'One shelf for every song across your folders; songs get workspaces when you open them.';
const groupTradeoff =
  'One workspace per song inside Ori Music Demo, and no Music Production Home: nothing lists songs across your other folders.';
const swapPlan = chosen => ({
  digest: `swap-${chosen}`,
  runnable: true,
  source: 'default',
  selected: [chosen],
  steps: [
    {
      handle: HOME,
      kind: 'home',
      name: 'Music Production Home',
      title: HOME_TITLE,
      excludes: GROUP,
      chosen: chosen === HOME
    },
    {
      handle: GROUP,
      kind: 'group',
      name: 'a group',
      title: GROUP_TITLE,
      excludes: HOME,
      chosen: chosen === GROUP
    }
  ],
  installs: chosen === HOME ? [providerInstall] : [],
  hires: chosen === HOME ? [hire('Portfolio Manager')] : [],
  alternatives:
    chosen === HOME
      ? [{ step: GROUP, title: GROUP_TITLE, tradeoff: groupTradeoff }]
      : [{ step: HOME, title: HOME_TITLE, tradeoff: homeTradeoff }],
  lines:
    chosen === HOME
      ? newHomeLines
      : [
          {
            kind: 'promote',
            step: GROUP,
            name: 'Turns Ori Music Demo into a group',
            outcome: 'Turns Ori Music Demo into a group'
          },
          {
            kind: 'member',
            step: GROUP,
            name: 'Creates Album-1, linked to its own folder',
            outcome: 'Creates 8 workspaces'
          },
          assurance
        ]
});
const swapCard = (chosen, options = {}, report = {}) =>
  renderFolderAgentReport(
    folderAgentReportView(
      reported({ model_sentence: '', tools_sentence: '', plan: swapPlan(chosen), ...report })
    ),
    stubDocument(),
    options
  );
const alternativesBlock = card => card.querySelector('.folder-agent-card__alternatives');
const stepRows = card =>
  card
    .querySelectorAll('.folder-agent-card__line')
    .filter(row => row.dataset.step)
    .map(row => [row.dataset.step, row.dataset.chosen]);

test('the other way is collapsed under "Other ways to set this up", not in the main list', () => {
  const card = swapCard(HOME);
  const block = alternativesBlock(card);
  const toggle = block.querySelector('.folder-agent-card__alternatives-toggle');
  assert.equal(toggle.tagName, 'BUTTON');
  assert.equal(toggle.type, 'button');
  assert.equal(toggle.textContent, 'Other ways to set this up');
  assert.equal(toggle.attributes['aria-expanded'], 'false');
  const list = block.querySelector('.folder-agent-card__alternative-list');
  assert.equal(toggle.attributes['aria-controls'], list.id);
  assert.equal(list.hidden, true);
  // The main list holds only the chosen step: no unticked group row, no box.
  assert.deepEqual(stepRows(card), [[HOME, 'true']]);
  assert.ok(!card.querySelectorAll('input').some(box => box.dataset.stepHandle === GROUP));
  // After the plan, before the install block.
  const kinds = card
    .querySelector('.folder-agent-card__run')
    .children.map(child =>
      classesOf(child).find(name => /__(plan|alternatives|installs|hires)$/.test(name))
    )
    .filter(Boolean);
  assert.deepEqual(kinds, [
    'folder-agent-card__plan',
    'folder-agent-card__alternatives',
    'folder-agent-card__installs',
    'folder-agent-card__hires'
  ]);
});

test('opening it shows the way, what it gives up and a button to take it instead', () => {
  const card = swapCard(HOME);
  const toggle = alternativesBlock(card).querySelector('.folder-agent-card__alternatives-toggle');
  toggle.listeners.click();
  assert.equal(toggle.attributes['aria-expanded'], 'true');
  const rows = alternativesBlock(card).querySelectorAll('.folder-agent-card__alternative');
  assert.equal(rows.length, 1);
  assert.equal(text(rows[0], '.folder-agent-card__line-name'), GROUP_TITLE);
  assert.equal(text(rows[0], '.folder-agent-card__line-detail'), groupTradeoff);
  const use = rows[0].querySelectorAll('button');
  assert.deepEqual(
    use.map(b => b.textContent),
    ['Use this instead']
  );
  assert.equal(use[0].type, 'button');
  assert.equal(
    alternativesBlock(card).querySelector('.folder-agent-card__alternative-list').hidden,
    false
  );
  toggle.listeners.click();
  assert.equal(toggle.attributes['aria-expanded'], 'false');
});

test('no other way, no control', () => {
  // The install-block fixtures hold no alternatives.
  assert.equal(alternativesBlock(installCard(['reaper-tools', HOME])), null);
  const none = swapPlan(HOME);
  none.alternatives = [];
  const card = renderFolderAgentReport(
    folderAgentReportView(reported({ model_sentence: '', tools_sentence: '', plan: none })),
    stubDocument(),
    {}
  );
  assert.equal(alternativesBlock(card), null);
  // A plan from before the host listed alternatives has none either.
  delete none.alternatives;
  const old = renderFolderAgentReport(
    folderAgentReportView(reported({ model_sentence: '', tools_sentence: '', plan: none })),
    stubDocument(),
    {}
  );
  assert.equal(alternativesBlock(old), null);
});

test('"Use this instead" swaps the plan in place and swapping back restores the first one', async () => {
  const requests = [];
  const fetchImpl = async (_url, init) => {
    const body = JSON.parse(init.body);
    requests.push(body);
    const chosen = body.selected.includes(GROUP) ? GROUP : HOME;
    return { ok: true, status: 200, json: async () => ({ plan: swapPlan(chosen) }) };
  };
  const card = swapCard(HOME, { fetchImpl });
  const setUp = card.querySelectorAll('button').find(b => b.dataset.folderAgentAction === 'set_up');
  assert.equal(setUp.textContent, 'Install 1 & set up');
  assert.ok(hiresBlock(card));

  // Take the group instead: the re-plan carries the group in place of the Home.
  await alternativesBlock(card)
    .querySelector('.folder-agent-card__alternative-use')
    .listeners.click();
  assert.deepEqual(requests, [{ selected: [GROUP] }]);
  assert.deepEqual(stepRows(card), [[GROUP, 'true']]);
  assert.equal(outcomeText(card), 'Turns Ori Music Demo into a group · Creates 8 workspaces');
  // The plan no longer installs or hires anything: those blocks and the count on
  // the button follow it without a reload.
  assert.equal(card.querySelector('.folder-agent-card__installs'), null);
  assert.equal(hiresBlock(card), null);
  assert.equal(setUp.textContent, 'Set it up');
  assert.equal(setUp.disabled, false);
  // The Home is now the other way, collapsed again, with its own trade-off.
  const toggle = alternativesBlock(card).querySelector('.folder-agent-card__alternatives-toggle');
  assert.equal(toggle.attributes['aria-expanded'], 'false');
  toggle.listeners.click();
  const rows = alternativesBlock(card).querySelectorAll('.folder-agent-card__alternative');
  assert.deepEqual(
    rows.map(row => [row.dataset.step, text(row, '.folder-agent-card__line-detail')]),
    [[HOME, homeTradeoff]]
  );

  // And back: the first plan returns, installs and hires with it.
  await alternativesBlock(card)
    .querySelector('.folder-agent-card__alternative-use')
    .listeners.click();
  assert.deepEqual(requests, [{ selected: [GROUP] }, { selected: [HOME] }]);
  assert.deepEqual(stepRows(card), [[HOME, 'true']]);
  assert.equal(setUp.textContent, 'Install 1 & set up');
  assert.equal(card.querySelectorAll('.folder-agent-card__install').length, 1);
  assert.equal(hiresBlock(card).querySelectorAll('.folder-agent-card__hire').length, 1);
  assert.equal(
    alternativesBlock(card).querySelector('.folder-agent-card__alternative').dataset.step,
    GROUP
  );
});

test('a plan that changed after it was shown is redrawn on the owner’s own choice, not Ori’s default', async () => {
  const calls = [];
  const raw = reported({ model_sentence: '', tools_sentence: '', plan: swapPlan(GROUP) });
  const owners = swapPlan(HOME);
  const respond = replan => async (url, init) => {
    calls.push([url, JSON.parse(init.body)]);
    if (url.endsWith('/setup'))
      return {
        ok: false,
        status: 409,
        json: async () => ({
          error: 'The plan changed since it was shown. Review it again.',
          plan_changed: true,
          folder_agent: raw
        })
      };
    return replan();
  };
  const view = folderAgentReportView(reported({ plan: swapPlan(HOME) }));

  // The server refused, so the card gets its view back, with the plan for the
  // steps the owner had ticked: the Home they chose, the hires they were shown.
  let result = await postFolderAgentAction(
    view,
    'set_up',
    respond(async () => ({ ok: true, status: 200, json: async () => ({ plan: owners }) })),
    { selected: [HOME] }
  );
  assert.deepEqual(
    calls.map(([url, body]) => [url.replace(/^.*folder-agent\/ws-1/, ''), body]),
    [
      ['/setup', { plan_digest: `swap-${HOME}`, selected: [HOME] }],
      ['/plan', { selected: [HOME] }]
    ]
  );
  assert.match(result.error, /plan changed/);
  const redrawn = folderAgentReportView(result.view);
  assert.deepEqual(redrawn.plan.selected, [HOME]);
  assert.equal(redrawn.plan.hires.length, 1);
  assert.equal(redrawn.plan.digest, `swap-${HOME}`);

  // If that re-plan cannot be had, the server's own view stands.
  calls.length = 0;
  result = await postFolderAgentAction(
    view,
    'set_up',
    respond(async () => ({ ok: false, status: 500, json: async () => ({}) })),
    { selected: [HOME] }
  );
  assert.equal(folderAgentReportView(result.view).plan.digest, `swap-${GROUP}`);
  result = await postFolderAgentAction(
    view,
    'set_up',
    respond(async () => {
      throw new Error('offline');
    }),
    { selected: [HOME] }
  );
  assert.equal(folderAgentReportView(result.view).plan.digest, `swap-${GROUP}`);

  // Any other refusal is shown as before: no second request.
  calls.length = 0;
  result = await postFolderAgentAction(
    view,
    'set_up',
    async (url, init) => {
      calls.push([url, JSON.parse(init.body)]);
      return {
        ok: false,
        status: 409,
        json: async () => ({ error: 'Answer the question first.', folder_agent: raw })
      };
    },
    { selected: [HOME] }
  );
  assert.equal(calls.length, 1);
  assert.equal(result.error, 'Answer the question first.');
  assert.equal(folderAgentReportView(result.view).plan.digest, `swap-${GROUP}`);
});

test('a card that cannot act lists the other way without a button to take it', () => {
  // No Set it up on this card (for example the owner has not answered the
  // premise): the plan reads, nothing on it can be changed.
  const card = swapCard(HOME, {}, { actions: ['keep', 'look_further'] });
  const block = alternativesBlock(card);
  assert.ok(block);
  assert.equal(block.querySelectorAll('.folder-agent-card__alternative-use').length, 0);
  assert.equal(block.querySelectorAll('.folder-agent-card__alternative').length, 1);
});
