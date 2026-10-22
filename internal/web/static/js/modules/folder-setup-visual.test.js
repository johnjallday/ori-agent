import test from 'node:test';
import assert from 'node:assert/strict';
import './folder-setup-visual.js';

const { visualView, renderDiagram } = globalThis.OriFolderSetupVisual;
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
  assert.equal(view.agent.status, 'Proposed new agent');
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
    assert.equal(view.agent.status, 'Proposed assignment');
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

function documentFixture() {
  const doc = {
    createElement: tag => ({
      tag,
      ownerDocument: doc,
      children: [],
      attributes: {},
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
