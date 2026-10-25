import test from 'node:test';
import assert from 'node:assert/strict';
import './folder-setup-visual.js';

const { visualView, renderDiagram, requirementsView, renderRequirements, requirementsActionLabel } =
  globalThis.OriFolderSetupVisual;
const plan = {
  visual: { entity: { kind: 'workspace', name: 'Song', operation: 'create' } },
  destination: { status: 'existing', name: 'Music', kind: 'plugin' },
  hire: { mode: 'create', agent_name: 'Ari', role_label: 'Producer', model: 'Configured model' }
};

test('review uses typed names and proposed assignment, never plan prose as evidence', () => {
  const view = visualView({ offer: { subject: { name: 'Source' }, plan } });
  assert.equal(view.entity.name, 'Song');
  assert.equal(view.entity.status, 'Proposed · new');
  assert.equal(view.parent, 'Existing Home · Music');
  assert.equal(view.agent.status, 'Needs hiring');
  assert.equal(view.agent.action, 'Hire & assign on setup');
  assert.equal(view.agent.membership, 'unknown');
  assert.match(view.summary, /Nothing here proves creation or assignment/);
  assert.equal(
    visualView({ plan: { lines: [{ kind: 'workspace', name: 'Creates Fake' }] } }).entity,
    null
  );
});

test('unknown, legacy and pending facts remain unknown rather than absent or invented', () => {
  const view = visualView({
    plan: {
      ...plan,
      visual: null,
      hire: { mode: 'pending', agent_name: 'Do not show', role_label: 'Producer' }
    }
  });
  assert.equal(view.entity, null);
  assert.equal(view.agent.name, '');
  assert.equal(view.agent.status, 'Identity pending');
  assert.equal(visualView({}).parent, 'Destination not yet reviewed');
  assert.equal(visualView({ plan: { hire: { mode: 'future' } } }).agent, null);
  assert.equal(
    visualView({
      plan: { visual: { entity: { kind: 'workspace', name: 'Fake', operation: 'done' } } }
    }).entity,
    null
  );
});

test('new/existing/standalone destinations and bind are distinct', () => {
  for (const [status, expected] of [
    ['new', 'Proposed new Home · Music'],
    ['existing', 'Existing Home · Music'],
    ['standalone', 'Separate workspace · no parent Home']
  ]) {
    const view = visualView({
      plan: {
        ...plan,
        destination: { status, name: 'Music' },
        hire: { mode: 'bind', agent_name: 'Saved agent' }
      }
    });
    assert.equal(view.parent, expected);
    assert.equal(view.agent.status, 'Already hired');
    assert.equal(view.agent.action, 'Assign on setup');
    assert.equal(view.agent.existence, 'existing');
  }
});

test('only actual root entries are displayed, never an invented file or a child total', () => {
  const observation = {
    folder: 'Root',
    tree: {
      nodes: [
        { name: 'a.rpp', kind: 'file' },
        { name: 'child', kind: 'folder' },
        { name: 'nested', kind: 'file', parent_id: 'child' }
      ]
    }
  };
  const view = visualView({ observation });
  assert.deepEqual(
    view.entries.map(row => row.name),
    ['a.rpp', 'child']
  );
  assert.match(view.sourceNote, /contents not read/);
  assert.equal(
    visualView({ observation, offer: { subject: { name: 'Child' } } }).entries.length,
    0
  );
  assert.match(view.summary, /No setup has started/);
});

test('visible consequences retain install/read/token/shared-agent and profile consent', () => {
  const effects = globalThis.OriFolderSetupVisual.reviewConsequences({
    ...plan,
    visual: { plugins: [{ kind: 'provider', name: 'Music', operation: 'update', version: '2' }] },
    lines: [
      { kind: 'provider', name: 'Update Music' },
      { kind: 'folder', name: 'Link the folder', detail: 'Read-only' },
      { kind: 'agents', name: 'Add agent', detail: 'Shared with later projects' },
      { kind: 'task', name: 'One first look' },
      { kind: 'profile', name: 'Home profile', detail: 'Reads the templates folder' }
    ]
  });
  assert.ok(effects.includes('Update Music to 2.'));
  assert.match(effects.join(' '), /Shared with later projects/);
  assert.match(effects.join(' '), /Reads the templates folder/);
  assert.match(effects.join(' '), /shares permitted folder content.*model tokens/);
  assert.doesNotMatch(effects.join(' '), /from 1/);
});

test('run milestones use applicable line evidence, not timers, profiles or first-look guesses', () => {
  const setup = {
    status: 'done',
    lines: [
      { kind: 'workspace', name: 'Workspace', state: 'done' },
      { kind: 'profile', name: 'Optional profile', state: 'failed' },
      { kind: 'agents', name: 'Staffing', state: 'waiting' },
      { kind: 'task', name: 'First look', state: 'done' },
      { kind: 'future', name: 'Unknown', state: 'done' }
    ]
  };
  const stages = globalThis.OriFolderSetupVisual.setupMilestones(setup);
  assert.deepEqual(
    stages.map(row => [row.kind, row.state]),
    [
      ['workspace', 'done'],
      ['profile', 'failed'],
      ['agents', 'waiting']
    ]
  );
  const view = visualView({
    offer: { plan, setup: { ...setup, visual: plan.visual, hire: plan.hire } }
  });
  assert.equal(view.entity.status, 'Created');
  assert.equal(view.agent.status, 'Needs hiring');
  assert.match(view.footer, /separate from the first look/);
  assert.doesNotMatch(JSON.stringify(view), /percent|first look complete|Assigned/);
  const group = globalThis.OriFolderSetupVisual.setupMilestones({
    lines: [
      { kind: 'member', name: 'Child A', state: 'working' },
      { kind: 'task', name: 'No task is created', state: 'done' }
    ]
  });
  assert.deepEqual(
    group.map(row => row.label),
    ['Child A']
  );
});

test('resolved visuals trust structured canonical receipts, never hire names or receipt prose', () => {
  const offer = {
    status: 'resolved',
    plan,
    subject: { name: 'Project' },
    outcome: {
      kind: 'project',
      receipt: [
        {
          kind: 'workspace',
          name: 'Actual workspace',
          operation: 'reuse',
          placement: 'standalone'
        },
        { kind: 'agent', name: 'No assistant yet', detail: 'added' },
        { kind: 'agent', name: 'Actual member', assigned: true, model: 'Recorded model' }
      ]
    }
  };
  let view = visualView({ offer });
  assert.equal(view.entity.name, 'Actual workspace');
  assert.equal(view.entity.status, 'Reused');
  assert.equal(view.agent.name, 'Actual member');
  assert.equal(view.agent.model, 'Recorded model');
  assert.match(view.parent, /Separate workspace/);
  assert.equal(view.agent.status, 'Assigned here');
  assert.equal(view.agent.action, 'On this workspace');
  assert.equal(view.agent.membership, 'assigned');
  offer.outcome.receipt = [
    { kind: 'workspace', name: 'Old record', detail: 'created' },
    { kind: 'agent', name: 'Legacy row' }
  ];
  view = visualView({ offer });
  assert.equal(view.entity.status, 'Ready');
  assert.equal(view.agent, null);
  assert.match(view.summary, /membership not reported/);
  for (const kind of ['group', 'home']) {
    offer.outcome.kind = kind;
    offer.outcome.receipt[0].kind = kind === 'home' ? 'home' : 'workspace';
    view = visualView({ offer });
    assert.equal(view.entity.kind, kind);
    assert.match(
      view.footer,
      kind === 'group' ? /No automatic task/ : /does not mean every project/
    );
  }
});

test('an unknown or incomplete result cannot claim a workspace is ready', () => {
  for (const outcome of [
    { kind: 'future', receipt: [] },
    { kind: 'project', receipt: [{ kind: 'workspace' }] }
  ]) {
    const view = visualView({ offer: { status: 'resolved', outcome } });
    assert.equal(view.entity, null);
    assert.equal(view.agent, null);
    assert.match(view.footer, /saved receipt/);
    assert.doesNotMatch(view.footer, /Workspace ready/);
  }
});

test('existing membership requires evidence for this exact reused workspace', () => {
  const reused = {
    ...plan,
    visual: { entity: { kind: 'workspace', name: 'Existing', operation: 'reuse' } },
    hire: { mode: 'bind', agent_name: 'Ari', assigned_here: true }
  };
  let view = visualView({ plan: reused });
  assert.equal(view.agent.status, 'Assigned here');
  assert.equal(view.agent.action, 'On this workspace');
  assert.equal(view.agent.membership, 'assigned');
  assert.match(view.footer, /Existing workspace membership is kept/);
  assert.match(view.summary, /Existing membership observed/);
  assert.doesNotMatch(view.footer, /nothing created or assigned/);
  view = visualView({ plan: { ...reused, visual: plan.visual } });
  assert.equal(
    view.agent.status,
    'Already hired',
    'a new workspace cannot inherit another workspace membership'
  );
  assert.equal(view.agent.membership, 'unknown');
  view = visualView({
    offer: {
      setup: { hire: plan.hire, visual: plan.visual, lines: [{ kind: 'agents', state: 'done' }] }
    }
  });
  assert.equal(view.agent.status, 'Needs hiring');
  assert.match(view.agent.progress, /membership awaits receipt/);
  assert.equal(view.agent.membership, 'unknown');
  const pending = visualView({
    plan: { ...plan, hire: { mode: 'pending', agent_name: 'Fake', model: 'Fake model' } }
  });
  assert.equal(pending.agent.name, '');
  assert.equal(pending.agent.model, '');
});

function documentFixture() {
  const doc = {
    createElement: tag => ({
      tag,
      ownerDocument: doc,
      children: [],
      attributes: {},
      dataset: {},
      textContent: '',
      setAttribute(key, value) {
        this.attributes[key] = value;
      },
      append(...nodes) {
        this.children.push(...nodes);
      },
      replaceChildren(...nodes) {
        this.children = nodes;
      },
      set innerHTML(_) {
        throw new Error('untrusted HTML sink');
      }
    })
  };
  return doc;
}
function nodes(node) {
  return [node, ...node.children.flatMap(nodes)];
}

test('requirements distinguish install/update/enable/ready from unchecked, unavailable and unverified', () => {
  const labels = {
    install: 'Not installed · Install',
    update: 'Update needed',
    enable: 'Disabled · Enable · No download',
    ready: 'Ready · No download',
    not_checked: 'Not checked',
    unavailable: 'Check unavailable',
    unverified: 'Not verified'
  };
  for (const [operation, status] of Object.entries(labels)) {
    const plan = {
      visual: {
        requirements_checked: true,
        plugins: [{ kind: 'integration', name: 'Literal plugin', operation }]
      }
    };
    const view = requirementsView({ plan });
    assert.equal(view.rows[0].status, status);
    assert.equal(view.rows[0].version, 'Version not reported');
    assert.equal(view.rows[0].source, '');
    assert.equal(view.downloads, ['install', 'update'].includes(operation) ? 1 : 0);
    assert.equal(view.complete, ['install', 'update', 'enable', 'ready'].includes(operation));
    assert.equal(view.summary === 'Nothing to download', ['enable', 'ready'].includes(operation));
    if (!view.complete) assert.equal(requirementsActionLabel(plan, 'Hire & start'), 'Hire & start');
  }
});

test('partial/legacy requirements keep known rows without inventing a zero-download total', () => {
  const plugins = [
    {
      kind: 'integration',
      name: 'Integration',
      operation: 'update',
      installed_version: '1.0',
      version: '2.0'
    },
    { kind: 'provider', name: 'Home', operation: 'unavailable' }
  ];
  const view = requirementsView({ readiness: { plugins } });
  assert.equal(view.rows.length, 2);
  assert.equal(view.rows[0].version, '1.0 → 2.0');
  assert.match(view.summary, /1 plugin download known/);
  const legacy = requirementsView({
    plan: { lines: [{ kind: 'integration', name: 'Do not parse Plugin 9.9 here' }] }
  });
  assert.equal(legacy.rows[0].name, 'Project integration');
  assert.equal(legacy.rows[0].version, 'Version not reported');
  assert.equal(legacy.complete, false);
  assert.equal(requirementsView({ plan: { lines: [{ kind: 'folder' }] } }).rows.length, 0);
  assert.equal(
    requirementsView({ offer: { setup: { visual: { plugins } } } }).summary,
    'Requirements recorded for this setup'
  );
});

test('requirements actions name only the checked operations and keep complete setup consent', () => {
  const plan = {
    visual: {
      requirements_checked: true,
      plugins: [
        { kind: 'integration', name: 'Integration', operation: 'install' },
        { kind: 'provider', name: 'Home', operation: 'update' }
      ]
    }
  };
  const before = JSON.stringify(plan);
  assert.equal(requirementsActionLabel(plan), 'Install/update 2 plugins & set up');
  assert.equal(JSON.stringify(plan), before);
  plan.visual.plugins[0].operation = 'ready';
  plan.visual.plugins[1].operation = 'enable';
  assert.equal(requirementsActionLabel(plan), 'Enable 1 plugin & set up');
  plan.visual.plugins[1].operation = 'ready';
  assert.equal(requirementsActionLabel(plan, 'Assign & start'), 'Assign & start');
});

test('requirement identity, purpose and source are literal inert text with stable disclosures', () => {
  const container = documentFixture().createElement('div');
  const hostile = '<img src=x onerror=alert(1)>界';
  const input = {
    readiness: {
      plugins: [
        {
          kind: 'integration',
          name: hostile,
          purpose: hostile,
          source: hostile,
          operation: 'install',
          version: hostile
        }
      ]
    }
  };
  renderRequirements(container, input);
  const original = container.children[0];
  const all = nodes(container);
  assert.ok(all.some(node => node.tag === 'strong' && node.textContent === hostile));
  assert.ok(all.some(node => node.tag === 'p' && node.textContent === hostile));
  assert.ok(all.some(node => node.tag === 'details'));
  assert.ok(!all.some(node => ['button', 'a', 'input', 'img'].includes(node.tag)));
  renderRequirements(container, input);
  assert.equal(
    container.children[0],
    original,
    'unchanged polls do not collapse source disclosure'
  );
  renderRequirements(container, {});
  assert.equal(container.hidden, true);
});

// An inbox folder's plan: File Janitor is built in and its first batch calls
// no model, so nothing is downloaded and nothing waits on a model.
const inboxPlan = {
  visual: {
    entity: { kind: 'workspace', name: 'Downloads', operation: 'create' },
    placement: 'standalone',
    requirements_checked: true,
    builtin: 'File Janitor',
    model: { state: 'not_needed' }
  },
  hire: { mode: 'create', agent_name: 'File Curator', role_label: 'specialist', model: '' },
  lines: [
    { kind: 'agents', name: 'Hire File Curator' },
    { kind: 'workspace', name: 'A File Janitor workspace named Downloads' },
    {
      kind: 'folder',
      name: 'Linked where it is',
      detail: 'Ori lists the files directly inside it.'
    },
    {
      kind: 'task',
      name: 'A first look: a metadata-only scan that proposes the first filing batch'
    }
  ]
};

test('a built-in recipe says why nothing is downloaded and that no model is needed', () => {
  const view = requirementsView({ plan: inboxPlan });
  assert.equal(view.summary, 'Nothing to download: File Janitor is built into Ori');
  assert.equal(view.rows.length, 0);
  assert.equal(view.model.state, 'not_needed');
  assert.equal(view.model.status, 'Not needed for the first batch');
  assert.equal(view.model.settings, '', 'no configuration route for a model nothing needs');
  const container = documentFixture().createElement('div');
  renderRequirements(container, { plan: inboxPlan });
  assert.equal(container.hidden, false);
  const texts = nodes(container).map(node => node.textContent);
  assert.ok(texts.includes('Nothing to download: File Janitor is built into Ori'));
  assert.ok(texts.includes('Model'));
  assert.ok(!texts.some(text => /Ori plugins are separate/.test(text)));
  // A built-in name never stands beside a plugin to download or unchecked rows.
  assert.equal(
    requirementsView({
      plan: { visual: { ...inboxPlan.visual, requirements_checked: false } }
    }).summary,
    'Download requirements not fully checked'
  );
});

test('an inbox hire is a File Janitor workspace with no parent and no model to wait on', () => {
  const view = visualView({ offer: { subject: { name: 'Downloads' }, plan: inboxPlan } });
  assert.equal(view.entity.label, 'File Janitor workspace');
  assert.equal(view.entity.status, 'Proposed · new');
  assert.equal(view.parent, 'Separate workspace · no parent Home');
  assert.equal(view.agent.status, 'Needs hiring');
  assert.equal(view.agent.name, 'File Curator');
  const container = documentFixture().createElement('div');
  renderDiagram(
    container,
    { offer: { subject: { name: 'Downloads' }, plan: inboxPlan } },
    { artwork: null }
  );
  const texts = nodes(container).map(node => node.textContent);
  assert.ok(texts.includes('File Janitor workspace'));
  assert.ok(texts.includes('specialist · No model needed for the first batch'));
  // Before you start never claims model tokens for a scan that calls no model.
  const effects = globalThis.OriFolderSetupVisual.reviewConsequences(inboxPlan);
  assert.ok(!effects.some(effect => /model tokens/.test(effect)));
  assert.ok(effects.some(effect => /Linked where it is/.test(effect)));
  // A plain workspace keeps its plain kind.
  assert.equal(
    visualView({ offer: { subject: { name: 'Source' }, plan } }).entity.label,
    'Workspace'
  );
});

test('all external strings are text nodes with a semantic diagram alternative and no controls', () => {
  const doc = documentFixture();
  const container = doc.createElement('div');
  const hostile = '<img src=x onerror=alert(1)>界'.repeat(30);
  renderDiagram(
    container,
    {
      observation: { folder: hostile },
      plan: {
        ...plan,
        visual: { entity: { kind: 'workspace', name: hostile, operation: 'reuse' } }
      }
    },
    { artwork: null }
  );
  const all = nodes(container);
  assert.ok(all.some(node => node.tag === 'h4' && node.textContent === hostile));
  assert.ok(all.some(node => node.tag === 'figcaption' && node.textContent.includes(hostile)));
  assert.ok(all.some(node => node.attributes['aria-label'] === 'Folder setup relationship'));
  assert.ok(!all.some(node => ['button', 'a', 'input', 'img'].includes(node.tag)));
});
