import test from 'node:test';
import assert from 'node:assert/strict';
import {
  proposalView,
  placementProposalView,
  renderProposal,
  structureOption
} from './personal-assistant-proposal.js';

const option = (kind, effect, extra = {}) => ({
  presentation: {
    version: 1,
    state: 'proposed',
    kind,
    effect,
    destination_state: 'unknown',
    ...extra
  }
});
test('proposal kind and effects use closed host fields, never type prose/names/counts', () => {
  assert.equal(
    proposalView(
      { workspace_type: 'Music Home group', art: '<svg/>' },
      { name: 'Albums', projects: 5 }
    ).kind,
    'unknown'
  );
  const building = proposalView(
    option('workspace', 'workspace', {
      destination_state: 'existing',
      destination_name: 'Parent group'
    }),
    { name: 'Album', root: false }
  );
  assert.equal(building.art, 'neutral');
  assert.equal(building.label, 'Proposed workspace');
  assert.equal(building.destination, 'Existing destination · Parent group');
  assert.equal(building.scope, 'One observed folder');
  const group = proposalView(option('group', 'home_library'), { name: 'Albums' });
  assert.equal(group.art, 'district');
  assert.match(group.description, /not promised child workspaces/);
  const collection = proposalView(
    option('group', 'library', { destination_state: 'existing', destination_name: 'Music Home' })
  );
  assert.equal(collection.label, 'Proposed collection');
  assert.match(collection.description, /not new workspaces/);
  assert.equal(proposalView(option('group', 'workspace')).kind, 'unknown');
  assert.equal(proposalView(option('workspace', 'workspace', { version: 99 })).kind, 'unknown');
  assert.equal(
    proposalView(option('workspace', 'workspace', { state: 'completed' })).kind,
    'unknown'
  );
  assert.equal(
    proposalView(option('workspace', 'workspace', { state: 'unavailable' })).label,
    'Setup unavailable'
  );
});

test('supporting art requires the verified host operation and opaque destination', () => {
  assert.equal(proposalView(option('supporting_folder', 'supporting_folder')).kind, 'unknown');
  assert.equal(
    placementProposalView({ operation: 'link_supporting_folder' }, 'Folder').kind,
    'unknown'
  );
  const view = placementProposalView(
    { operation: 'link_supporting_folder', destination_id: 'project-id' },
    'Folder'
  );
  assert.equal(view.art, 'support');
  assert.match(view.description, /No new workspace/);
  assert.equal(view.action, 'Review supporting folder');
  assert.equal(placementProposalView({ operation: 'bulk_create' }, 'Albums').kind, 'unknown');
});

test('renderer inserts bounded hostile names as literal text and only curated inert art', () => {
  const nodes = [];
  const doc = {
    createElement(tag) {
      const node = {
        tag,
        children: [],
        dataset: {},
        attrs: {},
        textContent: '',
        append(...children) {
          this.children.push(...children);
        },
        setAttribute(key, value) {
          this.attrs[key] = value;
        }
      };
      nodes.push(node);
      return node;
    }
  };
  const name = '<img src=x onerror=confirm()>' + '界'.repeat(300);
  const view = proposalView(
    option('workspace', 'workspace', {
      art: name,
      destination_state: 'existing',
      destination_name: name
    }),
    { name }
  );
  const calls = [];
  const card = renderProposal(view, doc, {
    svgForVariant(variant, opts) {
      calls.push([variant, opts]);
      return '<svg aria-hidden="true"/>';
    }
  });
  assert.equal(card.attrs['aria-label'], 'Proposed workspace');
  assert.ok(nodes.some(node => node.textContent.startsWith('<img')));
  assert.ok(Array.from(view.subject).length <= 120);
  assert.equal(
    nodes.some(node => node.tag === 'img' || node.tag === 'a' || node.tag === 'input'),
    false
  );
  assert.deepEqual(calls, [['neutral', { context: 'chat' }]]);
  assert.equal(nodes.filter(node => node.innerHTML).length, 1);
  assert.equal(nodes.find(node => node.innerHTML).attrs['aria-hidden'], 'true');
  assert.equal(view.destination.includes('unknown'), false);
});

const albums = (extra = {}, group = {}) => ({
  version: 1,
  folder: 'Albums',
  group: { name: 'Albums', type: 'group', destination_state: 'new', ...group },
  members: [
    ...[1, 2, 3, 4, 5].map(number => ({
      candidate_id: `candidate-${number}`,
      name: `Album-${number}`,
      type_label: 'Session folder',
      included: true,
      availability: 'integration_not_installed'
    })),
    {
      candidate_id: 'candidate-6',
      name: 'Logic Sketch',
      type_label: 'Plain workspace',
      included: true
    }
  ],
  notes: ['The integration is not installed; it can be added later.'],
  ...extra
});

test('a folder of several projects is one proposed group with its member workspaces', () => {
  const view = proposalView(structureOption(albums()), { name: 'ignored', root: true });
  assert.equal(view.kind, 'structure');
  // One question and one line: no label, scope or availability sentence.
  assert.equal(view.label, 'Set up Albums as a group?');
  assert.equal(
    view.description,
    '6 workspaces, one per folder · linked to their folders · nothing is moved'
  );
  assert.equal(view.subject, '');
  assert.equal(view.scope, '');
  assert.equal(view.art, 'district');
  assert.equal(view.destination, '');
  assert.equal(view.action, 'Set up');
  assert.deepEqual(view.members[0], {
    name: 'Album-1',
    type: 'Session folder',
    note: 'plain for now'
  });
  assert.deepEqual(view.members[5], { name: 'Logic Sketch', type: 'Plain workspace', note: '' });
  assert.deepEqual(view.notes, []);

  // A group named apart from its folder says the name it gets.
  const renamed = albums({}, { name: 'Albums group' });
  assert.equal(
    proposalView(structureOption(renamed)).destination,
    'Creates the group Albums group'
  );
  // The shape's Home, new or the owner's own; unchecked members are not listed.
  const home = albums({}, { name: 'Music Home', type: 'home' });
  assert.equal(proposalView(structureOption(home)).destination, 'Creates Music Home');
  const existing = albums({}, { name: 'Music Home', type: 'home', destination_state: 'existing' });
  assert.equal(
    proposalView(structureOption(existing)).destination,
    'Adds to your existing Music Home'
  );
  const adjusted = albums();
  adjusted.members[5].included = false;
  const fewer = proposalView(structureOption(adjusted));
  assert.match(fewer.description, /^5 workspaces, one per folder/);
  assert.equal(fewer.members.length, 5);
  adjusted.members.forEach((member, index) => (member.included = index === 0));
  assert.equal(
    proposalView(structureOption(adjusted)).description,
    '1 workspace · linked to its folder · nothing is moved'
  );

  // Only the closed host fields make a structure: a type string, a foreign
  // version, an unavailable state or the library choice never does.
  for (const broken of [
    { structure: albums() },
    { structure: albums(), presentation: { version: 1, state: 'unavailable', kind: 'structure' } },
    structureOption(albums({ version: 2 })),
    structureOption(albums({}, { type: 'library' })),
    structureOption({ version: 1, group: { type: 'group' } }),
    { workspace_type: 'Group with 6 workspaces' }
  ])
    assert.notEqual(proposalView(broken).kind, 'structure');
});

test('the member list is literal text, collapsed after three rows, with no sentences', () => {
  const nodes = [];
  const doc = {
    createElement(tag) {
      const node = {
        tag,
        children: [],
        dataset: {},
        attrs: {},
        listeners: {},
        textContent: '',
        hidden: false,
        append(...children) {
          this.children.push(...children);
        },
        setAttribute(key, value) {
          this.attrs[key] = value;
        },
        addEventListener(type, listener) {
          this.listeners[type] = listener;
        }
      };
      nodes.push(node);
      return node;
    }
  };
  const structure = albums({ omitted: 3 });
  structure.members.push(
    {
      candidate_id: 'candidate-7',
      name: '<img src=x onerror=confirm()>',
      type_label: 'Plain workspace',
      included: true
    },
    { candidate_id: 'candidate-8', name: 'Eighth', type_label: 'Plain workspace', included: true }
  );
  renderProposal(proposalView(structureOption(structure)), doc, null);
  const rows = nodes.filter(node => node.className === 'personal-assistant-proposal__member');
  assert.equal(rows.length, 8);
  assert.deepEqual(
    rows.map(row => row.hidden),
    [false, false, false, true, true, true, true, true]
  );
  assert.equal(rows[0].children[1].textContent, 'Session folder · plain for now');
  assert.equal(rows[6].children[0].textContent, '<img src=x onerror=confirm()>');
  const more = nodes.find(node => node.className === 'personal-assistant-proposal__more');
  assert.equal(more.textContent, 'Show all 8');
  more.listeners.click();
  assert.equal(
    rows.some(row => row.hidden),
    false
  );
  assert.equal(more.hidden, true);
  const notes = nodes
    .filter(node => node.className === 'personal-assistant-proposal__note')
    .map(node => node.textContent);
  assert.deepEqual(notes, ['3 more folders are not listed and get no workspace here.']);
  assert.equal(
    nodes.some(node => ['img', 'a', 'input'].includes(node.tag) || node.innerHTML),
    false
  );
  // The card's text is its question and its one line; empty parts are not drawn.
  const lines = nodes.filter(
    node => node.tag === 'p' && String(node.className || '') !== 'personal-assistant-proposal__note'
  );
  assert.deepEqual(
    lines.map(node => node.textContent),
    [
      'Set up Albums as a group?',
      '8 workspaces, one per folder · linked to their folders · nothing is moved'
    ]
  );

  // Three or fewer rows need no "Show all".
  nodes.length = 0;
  const trio = albums();
  trio.members = trio.members.slice(0, 3);
  renderProposal(proposalView(structureOption(trio)), doc, null);
  assert.equal(
    nodes.some(node => node.className === 'personal-assistant-proposal__more'),
    false
  );
});
