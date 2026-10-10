import test from 'node:test';
import assert from 'node:assert/strict';
import {
  proposalView,
  placementProposalView,
  renderProposal
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
