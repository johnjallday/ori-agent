import test from 'node:test';
import assert from 'node:assert/strict';
import {
  arrangementPresentation,
  renderArrangementHistory,
  mountArrangementHistory
} from './workspace-arrangement.js';

function fixture() {
  return {
    kind: 'proposal',
    catalog_current: true,
    arrangement: {
      id: 'saved-id',
      revision: 1,
      state: 'draft',
      origin: 'model',
      goal: 'Keep experiments catalog-only',
      rationale: 'Only the selected client projects need grouping.',
      unknowns: ['Client ownership is unknown'],
      operations: 1,
      proposed_operations: [
        { kind: 'create_collection', name: '<img src=x onerror=alert(1)>', members: 1000 }
      ],
      explicit_unchanged: 200,
      explicit_needs_review: 3,
      review_questions: [{ reason: 'Which version is authoritative?', members: 3 }],
      coverage: {
        origin: 'saved_home_catalog',
        partial: true,
        note: 'Saved catalog, not a fresh inspection.'
      },
      review_available: false
    }
  };
}
function dom() {
  const document = {
    createElement(tag) {
      return {
        tag,
        ownerDocument: document,
        children: [],
        attributes: {},
        events: {},
        textContent: '',
        isConnected: true,
        append(...nodes) {
          this.children.push(...nodes);
        },
        replaceChildren(...nodes) {
          this.children = nodes;
          this.textContent = '';
        },
        setAttribute(key, value) {
          this.attributes[key] = value;
        },
        addEventListener(key, fn) {
          this.events[key] = fn;
        }
      };
    }
  };
  return document.createElement('div');
}
const nodes = root => [root, ...root.children.flatMap(child => nodes(child))];
const contents = root =>
  nodes(root)
    .map(node => node.textContent)
    .join('\n');

test('canonical history is inert, coverage-aware and count-bounded, not a membership list', () => {
  const input = fixture();
  const view = arrangementPresentation(input);
  assert.match(view.title, /not applied/);
  assert.equal(view.coverageLabel, 'Partial evidence');
  assert.match(view.operations[0], /1000 member/);
  assert.match(view.dispositions, /not a complete inventory/);
  view.unknowns.push('edited');
  assert.equal(input.arrangement.unknowns.length, 1);
  input.catalog_current = false;
  assert.equal(arrangementPresentation(input).contextChanged, true);
  input.arrangement.coverage.partial = false;
  assert.equal(arrangementPresentation(input).coverageLabel, 'Bounded saved evidence');
});

test('clarification and its redacted tombstone are not proposals', () => {
  const input = fixture();
  Object.assign(input.arrangement, {
    state: 'needs_more_evidence',
    rationale: '',
    unknowns: [],
    operations: 0,
    proposed_operations: [],
    explicit_unchanged: 0,
    explicit_needs_review: 0,
    review_questions: []
  });
  input.kind = 'clarification';
  input.clarification = 'Which client?';
  assert.match(arrangementPresentation(input).title, /discussion/);
  input.clarification = '';
  assert.equal(arrangementPresentation(input), null);
  input.arrangement.state = 'stale';
  const container = dom();
  assert.equal(renderArrangementHistory(container, input), true);
  assert.match(contents(container), /redacted/);
  assert.match(contents(container), /not current source authority/);
});

test('malformed, executable, oversized and silently truncated projections refuse', () => {
  const mutations = [
    x => {
      x.arrangement.state = 'completed';
    },
    x => {
      x.arrangement.review_available = true;
    },
    x => {
      x.arrangement.id = '../other';
    },
    x => {
      x.arrangement.operations = 2;
    },
    x => {
      x.arrangement.proposed_operations[0].kind = 'move_files';
    },
    x => {
      x.arrangement.proposed_operations[0].members = 5001;
    },
    x => {
      x.arrangement.unknowns = Array(17).fill('unknown');
    },
    x => {
      x.kind = 'clarification';
      x.clarification = 'Yet still has effects';
    },
    x => {
      x.untrusted_extra = '<'.repeat(65536);
    },
    x => {
      x.arrangement.goal = 123;
    }
  ];
  for (const mutate of mutations) {
    const input = fixture();
    mutate(input);
    assert.equal(arrangementPresentation(input), null);
  }
  assert.equal(arrangementPresentation('Generated JSON is just text'), null);
});

test('literal model text uses semantic text nodes; no executable UI or hidden tail', () => {
  const input = fixture();
  input.arrangement.operations = 32;
  input.arrangement.proposed_operations = Array.from({ length: 32 }, (_, i) => ({
    kind: 'create_collection',
    name: `Literal <svg> ${i}`,
    members: 1000
  }));
  const container = dom();
  renderArrangementHistory(container, input);
  const all = nodes(container);
  assert.equal(all.filter(node => node.tag === 'li').length, 34);
  assert.ok(all.some(node => node.textContent.includes('Literal <svg> 31')));
  assert.ok(
    all.some(node => node.tag === 'summary' && node.textContent === 'Proposed intentions (32)')
  );
  assert.ok(
    all.every(
      node => !('innerHTML' in node) && !['a', 'button', 'input', 'form'].includes(node.tag)
    )
  );
  assert.match(contents(container), /independent project reviews/);
});

test('opening saved history is explicit and context-bound, never an automatic model call', async () => {
  const container = dom();
  let calls = 0;
  assert.equal(
    mountArrangementHistory(container, {
      id: 'saved-id',
      load: async () => {
        calls++;
        return fixture();
      }
    }),
    true
  );
  assert.equal(calls, 0);
  const button = container.children[0];
  await button.events.click();
  assert.equal(calls, 1);
  assert.match(contents(container), /Saved arrangement — not applied/);
  assert.equal(button.disabled, false);
});

test('late, foreign and failed reads cannot attach authority or disclose raw errors', async () => {
  let finish;
  let current = true;
  const container = dom();
  mountArrangementHistory(container, {
    id: 'saved-id',
    isCurrent: () => current,
    load: () =>
      new Promise(resolve => {
        finish = resolve;
      })
  });
  const pending = container.children[0].events.click();
  current = false;
  finish(fixture());
  await pending;
  assert.doesNotMatch(contents(container), /Keep experiments|Reading saved history/);
  assert.match(contents(container), /Context changed/);
  assert.equal(container.children[0].disabled, true);
  for (const load of [
    async () => ({ ...fixture(), arrangement: { ...fixture().arrangement, id: 'foreign' } }),
    async () => {
      throw new Error('/private/provider-secret');
    }
  ]) {
    const target = dom();
    mountArrangementHistory(target, { id: 'saved-id', load });
    await target.children[0].events.click();
    assert.match(contents(target), /unavailable/);
    assert.doesNotMatch(contents(target), /provider-secret|Keep experiments/);
  }
});
