import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const read = name => readFileSync(new URL(name, import.meta.url), 'utf8');

// The two classic scripts the module imports for their effect, loaded the way
// a page loads them: globals on globalThis.
for (const script of ['./folder-plan-lines.js', './agent-hire-card.js']) {
  vm.runInThisContext(read(script));
}

const hire = await import('./personal-assistant-hire-offer.js');
const { readNeedsReveal } = await import('./personal-assistant-needs-reveal.js');

const catalog = {
  entries: [
    {
      slug: 'researcher',
      display_name: 'Researcher',
      tagline: 'Gathers information from the web and files',
      emblem: 'search',
      accent_color: '#0ea5e9'
    },
    {
      slug: 'synthesizer',
      display_name: 'Writer',
      tagline: 'Turns findings into briefs, docs, and emails',
      emblem: 'pencil-square',
      accent_color: '#22c55e'
    },
    {
      slug: 'specialist',
      display_name: 'Specialist',
      tagline: 'Deep expert in one domain you define',
      emblem: 'gem',
      accent_color: '#14b8a6',
      supports_domain: true
    }
  ],
  hire: {
    roles: {
      researcher: {
        default_brief: "Find and summarize what's known about this topic, with sources.",
        blueprint: 'research-project',
        blueprint_label: 'Research Project',
        open_roles: 2
      },
      synthesizer: {
        default_brief: 'Turn the notes and findings here into a clear draft.',
        blueprint: 'writing-project',
        blueprint_label: 'Writing Project',
        open_roles: 2
      },
      specialist: {
        default_brief: 'Act as the expert this workspace needs.',
        blueprint: 'blank',
        blueprint_label: 'Blank',
        open_roles: 0
      }
    },
    domain_brief: 'Act as the {domain} expert for this workspace.',
    brief_max: 300,
    workspace_name_max: 80
  }
};

// The confirmation as the server sends it (hireProposalConfirmation).
const confirmation = {
  action_id: 'hire-agent',
  action_type: 'hire_agent',
  summary: 'Hire Ruth for CRM comparison? Nothing is hired until you press Hire and start.',
  arguments: {
    proposal_id: 'p-1',
    message_id: 'm-4',
    state: 'offered',
    role: 'researcher',
    role_label: 'Researcher',
    tagline: 'Gathers information from the web and files',
    name: 'Ruth',
    domain: '',
    brief: "Compare the three CRMs we're evaluating and recommend one by Friday.",
    workspace_name: 'CRM comparison',
    blueprint: 'research-project',
    blueprint_label: 'Research Project',
    open_roles: 2,
    conversation_id: 'conv-1',
    state_version: 5,
    catalog
  }
};

const plain = value => JSON.parse(JSON.stringify(value));

test('a reply carries a hire card only as a hire_agent confirmation with a proposal id', () => {
  const proposal = hire.proposalFromConfirmation(confirmation);
  assert.equal(proposal.proposalId, 'p-1');
  assert.equal(proposal.conversationId, 'conv-1');
  assert.equal(proposal.stateVersion, 5);
  assert.equal(proposal.state, 'offered');
  assert.equal(proposal.roleLabel, 'Researcher');
  assert.equal(proposal.openRoles, 2);
  assert.equal(proposal.catalog, catalog);
  assert.equal(
    hire.proposalFromConfirmation({ ...confirmation, action_type: 'create_task' }),
    null
  );
  assert.equal(
    hire.proposalFromConfirmation({
      ...confirmation,
      arguments: { ...confirmation.arguments, proposal_id: '' }
    }),
    null
  );
  assert.equal(hire.proposalFromConfirmation(null), null);
});

test('a reloaded proposal keeps its state and takes the catalog the reload carries', () => {
  const view = {
    ...confirmation.arguments,
    catalog: undefined,
    state: 'hired',
    hire_id: 'h-1',
    agent_name: 'Ruth',
    workspace_id: 'ws-1',
    workspace_route: '/workspaces/crm-comparison'
  };
  const proposal = hire.proposalFromReload(view, catalog, 'conv-1');
  assert.equal(proposal.state, 'hired');
  assert.equal(proposal.hireId, 'h-1');
  assert.equal(proposal.workspaceRoute, '/workspaces/crm-comparison');
  assert.equal(proposal.catalog, catalog);
  assert.equal(proposal.conversationId, 'conv-1');
  // An unknown state is drawn as an offer rather than trusted.
  assert.equal(
    hire.proposalFromReload({ ...view, state: 'maybe' }, catalog, 'conv-1').state,
    'offered'
  );
  assert.deepEqual([...hire.offeredMessageIds([view, { message_id: '' }, null])], ['m-4']);
});

test('the lead is the first paragraph of the reply; the offer text stays with the card', () => {
  assert.equal(
    hire.lead(
      'Sounds like a job for a Researcher.\n\nOffer: hire Ruth, a Researcher, for a workspace named "CRM comparison". Brief: "…". Nothing is hired until you press Hire and start on the card.'
    ),
    'Sounds like a job for a Researcher.'
  );
  assert.equal(hire.lead('  One line only  '), 'One line only');
  assert.equal(hire.lead(''), '');
});

test('the headline reads Hire <name> for <workspace>, as the folder hire card writes it', () => {
  const proposal = hire.proposalFromConfirmation(confirmation);
  assert.deepEqual(plain(hire.headlineSegments(proposal)), [
    { text: 'Hire ', strong: false },
    { text: 'Ruth', strong: true },
    { text: ' for ', strong: false },
    { text: 'CRM comparison', strong: true }
  ]);
  assert.equal(
    hire.detailText(proposal),
    'Researcher role · Gathers information from the web and files'
  );
  assert.equal(hire.settledText({ ...proposal, state: 'declined' }), 'Put aside.');
  assert.match(hire.settledText({ ...proposal, state: 'adjusted' }), /workspace form/);
  assert.equal(
    hire.settledText({ ...proposal, state: 'hired' }, { agent_name: 'Ruth' }),
    'Hired. Ruth is taking a first look; your assistant will report back.'
  );
  assert.equal(
    hire.settledText({ ...proposal, state: 'hired', agentName: 'Ruth 2' }),
    'Hired. Ruth 2 is taking a first look; your assistant will report back.'
  );
  assert.equal(hire.settledText(proposal), '');
  assert.equal(hire.primaryLabel({ buttonLabel: 'Try again' }), 'Try again');
  assert.equal(hire.primaryLabel(null), 'Hire and start');
});

// A document small enough to hold the card: elements, text, listeners.
class Node {
  constructor(tag) {
    this.tagName = tag;
    this.children = [];
    this.dataset = {};
    this.attributes = {};
    this.listeners = {};
    this.className = '';
    this.text_ = '';
    this.disabled = false;
    this.parent = null;
    this.style = {};
  }
  // As in the DOM, setting textContent replaces every child.
  get textContent() {
    return this.text_;
  }
  set textContent(value) {
    this.text_ = String(value);
    this.children = [];
  }
  get ownerDocument() {
    return documentStub;
  }
  get firstElementChild() {
    return this.children[0] || null;
  }
  setAttribute(name, value) {
    this.attributes[name] = String(value);
  }
  removeAttribute(name) {
    delete this.attributes[name];
  }
  append(...nodes) {
    for (const node of nodes) {
      node.parent = this;
      this.children.push(node);
    }
  }
  remove() {
    if (this.parent) this.parent.children = this.parent.children.filter(node => node !== this);
    this.parent = null;
  }
  addEventListener(type, handler) {
    this.listeners[type] = handler;
  }
  querySelector(selector) {
    const wanted = selector.replace(/^\./, '');
    for (const child of this.children) {
      if (child.className.split(/\s+/).includes(wanted)) return child;
      const nested = child.querySelector(selector);
      if (nested) return nested;
    }
    return null;
  }
  querySelectorAll() {
    return [];
  }
  find(className) {
    return this.querySelector(`.${className}`);
  }
  text() {
    return this.textContent + this.children.map(child => child.text()).join('');
  }
}
const documentStub = { createElement: tag => new Node(tag) };

function replyRow() {
  const row = new Node('div');
  row.append(new Node('div'));
  return row;
}

// A stand-in for the shared card's mount: it records what it was asked for
// and lets a test drive onChange and submit.
function stubMount() {
  const calls = { mounts: [], submits: 0, destroyed: 0 };
  const api = globalThis.OriAgentHire;
  const original = api.mountHireCard;
  api.mountHireCard = (host, options) => {
    calls.mounts.push({ host, options });
    return {
      submit: async () => {
        calls.submits += 1;
        return calls.result ? options.onHired(calls.result) : null;
      },
      view: () => ({}),
      chooseRole: () => {},
      focus: () => {},
      destroy: () => {
        calls.destroyed += 1;
      }
    };
  };
  calls.restore = () => {
    api.mountHireCard = original;
  };
  return calls;
}

test('an offer draws the shared card under the reply, prefilled with the proposal and its id as the request id', () => {
  const calls = stubMount();
  try {
    const row = replyRow();
    const card = hire.render(row, confirmation, {});
    assert.equal(card.className, 'personal-assistant-hire');
    assert.equal(card.dataset.state, 'offered');
    assert.equal(card.dataset.hireProposalId, 'p-1');
    assert.equal(card.attributes['aria-label'], 'Hire Ruth for CRM comparison');
    assert.equal(row.firstElementChild.style.maxWidth, '100%');
    assert.equal(
      card.find('personal-assistant-hire__headline').text(),
      'Hire Ruth for CRM comparison'
    );
    assert.equal(
      card.find('personal-assistant-hire__detail').textContent,
      'Researcher role · Gathers information from the web and files'
    );
    const buttons = card
      .find('personal-assistant-hire__actions')
      .children.map(button => button.textContent);
    assert.deepEqual(buttons, ['Hire and start', 'Not now', 'Adjust']);
    assert.equal(card.find('personal-assistant-hire__actions').children[0].dataset.hirePrimary, '');
    assert.equal(calls.mounts.length, 1);
    const { host, options } = calls.mounts[0];
    assert.equal(host.className, 'personal-assistant-hire__card');
    assert.equal(options.requestId, 'p-1');
    assert.equal(options.appearance, false);
    assert.equal(options.catalog, catalog);
    assert.deepEqual(plain(options.prefill), {
      role: 'researcher',
      name: 'Ruth',
      domain: '',
      brief: "Compare the three CRMs we're evaluating and recommend one by Friday.",
      workspaceName: 'CRM comparison'
    });
  } finally {
    calls.restore();
  }
});

test('the headline follows the card: a role change renames the hire, an edited brief and name show up', () => {
  const calls = stubMount();
  try {
    const row = replyRow();
    const card = hire.render(row, confirmation, {});
    const { options } = calls.mounts[0];
    const primary = card.find('personal-assistant-hire__actions').children[0];
    options.onChange({
      fields: {
        role: 'synthesizer',
        name: 'Writer',
        domain: '',
        brief: 'Write the comparison up.',
        workspaceName: 'CRM comparison'
      },
      busy: false,
      buttonLabel: 'Hire and start'
    });
    assert.equal(
      card.find('personal-assistant-hire__headline').text(),
      'Hire Writer for CRM comparison'
    );
    assert.equal(
      card.find('personal-assistant-hire__detail').textContent,
      'Writer role · Turns findings into briefs, docs, and emails'
    );
    options.onChange({
      fields: { role: 'synthesizer', name: 'Writer', brief: 'b', workspaceName: 'w' },
      busy: true,
      buttonLabel: 'Hire and start'
    });
    assert.equal(primary.disabled, true);
    assert.equal(primary.attributes['aria-busy'], 'true');
    assert.equal(card.find('personal-assistant-hire__actions').children[1].disabled, true);
    options.onChange({
      fields: { role: 'synthesizer', name: 'Writer', brief: 'b', workspaceName: 'w' },
      busy: false,
      buttonLabel: 'Try again'
    });
    assert.equal(primary.disabled, false);
    assert.equal(primary.textContent, 'Try again');
    assert.equal(primary.attributes['aria-busy'], undefined);
  } finally {
    calls.restore();
  }
});

test('Hire and start submits the card once, and a finished hire settles the card with its message and link', async () => {
  const calls = stubMount();
  const hired = [];
  const tab = new Map();
  const hadStorage = Object.getOwnPropertyDescriptor(globalThis, 'sessionStorage');
  Object.defineProperty(globalThis, 'sessionStorage', {
    configurable: true,
    value: { getItem: key => tab.get(key) ?? null, setItem: (key, value) => tab.set(key, value) }
  });
  try {
    const row = replyRow();
    const card = hire.render(row, confirmation, {
      onHired: (result, proposal) => hired.push([result, proposal])
    });
    const actions = card.find('personal-assistant-hire__actions');
    const primary = actions.children[0];
    calls.result = {
      hire_id: 'h-1',
      agent_name: 'Ruth',
      workspace_id: 'ws-1',
      workspace_name: 'CRM comparison',
      workspace_route: '/workspaces/crm-comparison',
      look_state: 'looking'
    };
    await primary.listeners.click();
    assert.equal(calls.submits, 1);
    assert.equal(card.dataset.state, 'hired');
    assert.equal(card.querySelector('.personal-assistant-hire__actions'), null);
    const status = card.find('personal-assistant-hire__status');
    assert.equal(
      status.textContent,
      'Hired. Ruth is taking a first look; your assistant will report back.'
    );
    const link = status.find('personal-assistant-hire__link');
    assert.equal(link.href, '/workspaces/crm-comparison');
    assert.equal(link.textContent, 'Open CRM comparison');
    // The card body stays, as the finished plan, with its request id spent.
    assert.ok(card.find('personal-assistant-hire__card'));
    assert.equal(hired.length, 1);
    assert.equal(hired[0][1].state, 'hired');
    // This tab made the hire, so its report will open Today's strip once.
    assert.deepEqual(readNeedsReveal().own, ['hire:h-1']);
  } finally {
    calls.restore();
    if (hadStorage) Object.defineProperty(globalThis, 'sessionStorage', hadStorage);
    else delete globalThis.sessionStorage;
  }
});

test('a reply that is no longer current hires nothing and says so', async () => {
  const calls = stubMount();
  try {
    const row = replyRow();
    const card = hire.render(row, confirmation, { isCurrent: () => false });
    const primary = card.find('personal-assistant-hire__actions').children[0];
    await primary.listeners.click();
    assert.equal(calls.submits, 0);
    assert.match(
      card.find('personal-assistant-hire__status').textContent,
      /Reopen the conversation/
    );
    assert.equal(card.dataset.state, 'offered');
  } finally {
    calls.restore();
  }
});

test('Not now and Adjust settle the card through their handlers, and a refused one leaves it as it was', async () => {
  const calls = stubMount();
  try {
    const seen = [];
    const row = replyRow();
    const card = hire.render(row, confirmation, {
      decline: async proposal => seen.push(['decline', proposal.proposalId]),
      adjust: async () => {
        throw new Error('The workspace form is unavailable on this page.');
      }
    });
    const actions = card.find('personal-assistant-hire__actions');
    const [, later, adjust] = actions.children;
    await adjust.listeners.click();
    assert.equal(card.dataset.state, 'offered');
    assert.equal(adjust.disabled, false);
    assert.equal(
      card.find('personal-assistant-hire__status').textContent,
      'The workspace form is unavailable on this page.'
    );
    await later.listeners.click();
    assert.deepEqual(seen, [['decline', 'p-1']]);
    assert.equal(card.dataset.state, 'declined');
    assert.equal(card.querySelector('.personal-assistant-hire__actions'), null);
    assert.equal(card.querySelector('.personal-assistant-hire__card'), null);
    assert.equal(calls.destroyed, 1);
    assert.equal(card.find('personal-assistant-hire__status').textContent, 'Put aside.');
    assert.equal(
      card.find('personal-assistant-hire__headline').text(),
      'Hire Ruth for CRM comparison'
    );
  } finally {
    calls.restore();
  }
});

test('a reload draws each saved offer under its reply in the state it is in now, and nothing for an unknown row', () => {
  const calls = stubMount();
  try {
    const rows = { 'm-4': replyRow(), 'm-9': replyRow() };
    const proposals = [
      { ...confirmation.arguments, catalog: undefined },
      {
        ...confirmation.arguments,
        catalog: undefined,
        proposal_id: 'p-2',
        message_id: 'm-9',
        state: 'hired',
        agent_name: 'Ruth 2',
        workspace_route: '/workspaces/crm-2'
      },
      {
        ...confirmation.arguments,
        catalog: undefined,
        proposal_id: 'p-3',
        message_id: 'm-none',
        state: 'declined'
      }
    ];
    const drawn = hire.hydrate('conv-1', proposals, catalog, {
      doc: documentStub,
      rowFor: id => rows[id] || null
    });
    assert.equal(drawn, 2);
    const first = rows['m-4'].firstElementChild.find('personal-assistant-hire');
    assert.equal(first.dataset.state, 'offered');
    assert.equal(calls.mounts.length, 1);
    assert.equal(calls.mounts[0].options.catalog, catalog);
    const second = rows['m-9'].firstElementChild.find('personal-assistant-hire');
    assert.equal(second.dataset.state, 'hired');
    assert.equal(second.querySelector('.personal-assistant-hire__actions'), null);
    assert.equal(second.find('personal-assistant-hire__link').href, '/workspaces/crm-2');
    assert.equal(
      second.find('personal-assistant-hire__headline').text(),
      'Hire Ruth for CRM comparison'
    );
    assert.equal(hire.hydrate('conv-1', [], catalog, { doc: documentStub }), 0);
  } finally {
    calls.restore();
  }
});

test('the drawer routes a hire_agent reply to the card and no longer knows the workspace proposal', () => {
  const dashboard = read('./dashboard.js');
  assert.match(dashboard, /action_type === 'hire_agent'/);
  assert.match(dashboard, /renderHireProposalCard\(/);
  assert.match(dashboard, /window\.PersonalAssistantHireOffer\.lead\(/);
  // The assistant's own hire module keeps its name and its global.
  assert.doesNotMatch(dashboard, /window\.PersonalAssistantHire\b[^O]/);
  assert.match(dashboard, /invalid_hire_proposal/);
  assert.match(dashboard, /OriAskRouting\.hireCardOptions = function/);
  // Not now and Adjust record the offer's state through its own routes, and
  // Adjust hands the form the blueprint, the goal and the staffed seat.
  assert.match(dashboard, /\/api\/personal-assistant\/hire-proposals\//);
  assert.match(dashboard, /settle\(proposal, 'decline'\)/);
  assert.match(dashboard, /settle\(proposal, 'adjust'\)/);
  assert.match(dashboard, /hireAdjustArguments\(conversationId, proposal\)/);
  assert.match(dashboard, /entry_point: 'assistant_hire_adjust'/);
  // The Agents page opens its card from the no-tools chip's link.
  const roster = read('../agents-roster.js');
  assert.match(roster, /arrival\.get\('hire'\) === '1'/);
  assert.match(roster, /prefill: options\.prefill \|\| undefined/);
  assert.match(read('./agent-hire-card.js'), /prefill\.requireRole/);
  assert.doesNotMatch(dashboard, /prepare_workspace/);
  assert.doesNotMatch(dashboard, /Review workspace setup/);
  assert.doesNotMatch(dashboard, /invalid_workspace_proposal/);
  const conversation = read('./personal-assistant-conversation.js');
  assert.match(conversation, /hire\.hydrate\(/);
  assert.match(conversation, /offeredMessageIds\(result\.body\.hire_proposals\)/);
  assert.match(conversation, /invalid_hire_proposal/);
  const head = read('../../../templates/layout/head.tmpl');
  assert.match(head, /\/css\/agent-hire\.css/);
  assert.match(head, /\/js\/modules\/personal-assistant-hire-offer\.js/);
  assert.match(read('./personal-assistant-hire.js'), /window\.OriAssistantHire/);
});
