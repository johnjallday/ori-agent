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
test('activation-selection receipts report pending independent reviews, never project activation', () => {
  const history = fixture();
  history.arrangement.state = 'completed';
  history.collection_receipt = {
    digest: 'a'.repeat(64),
    catalog_revision: 4,
    applied_at: '2026-01-01T00:00:00Z',
    effects: [
      {
        kind: 'select_activation',
        collection_id: '',
        queue_id: 'queue-a',
        selected_count: 1,
        before_revision: 0,
        after_revision: 0
      }
    ]
  };
  const view = arrangementPresentation(history);
  assert.equal(view.appliedEffects, 0);
  assert.equal(view.queuedProjects, 1);
  assert.equal(view.title, 'Saved metadata and activation-review selection');
  history.collection_receipt.effects[0].selected_count = 101;
  assert.equal(arrangementPresentation(history), null);
  history.collection_receipt.effects[0].selected_count = 1;
  history.collection_receipt.effects.push({
    ...history.collection_receipt.effects[0],
    queue_id: 'queue-b'
  });
  assert.equal(arrangementPresentation(history), null);
});

test('owner correction summaries remain distinct from approval and reject forged future revisions', () => {
  const history = fixture();
  history.arrangement.revision = 2;
  history.collection_correction = {
    revision: 2,
    operation: 0,
    renamed: true,
    excluded: 3,
    at: '2026-01-01T00:00:00Z'
  };
  const view = arrangementPresentation(history);
  assert.match(view.origin, /Owner correction, revision 2/);
  assert.equal(view.title, 'Saved arrangement — not applied');
  history.collection_correction.revision = 3;
  assert.equal(arrangementPresentation(history), null);
});

test('revision questions preserve intent as history but cannot claim review or completion', () => {
  const history = fixture();
  history.arrangement.state = 'needs_more_evidence';
  history.arrangement.revision = 2;
  history.revision_discussion = {
    goal: 'Which client?',
    answer: '<script>Untrusted question</script>',
    provider: 'openai',
    model: 'fixture',
    revision: 2,
    at: '2026-01-01T00:00:00Z'
  };
  const view = arrangementPresentation(history);
  assert.equal(view.title, 'Saved revision question — prior proposal retained');
  assert.equal(view.goal, history.arrangement.goal);
  history.arrangement.state = 'cancelled';
  assert.equal(arrangementPresentation(history).title, 'Declined arrangement — not applied');
  history.revision_discussion.revision = 1;
  assert.equal(arrangementPresentation(history), null);
});

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

test('collection completion requires a canonical Home receipt and survives redacted history', () => {
  const input = fixture();
  input.arrangement.state = 'completed';
  assert.equal(arrangementPresentation(input), null);
  input.collection_receipt = {
    digest: 'a'.repeat(64),
    catalog_revision: 3,
    applied_at: '2026-01-01T00:00:00Z',
    effects: [
      {
        kind: 'create_collection',
        collection_id: 'collection-id',
        before_revision: 0,
        after_revision: 1
      }
    ]
  };
  const root = dom();
  assert.equal(renderArrangementHistory(root, input), true);
  assert.match(contents(root), /Saved collection metadata receipt/);
  assert.match(contents(root), /Canonical receipt: 1 logical-collection/);
  assert.doesNotMatch(contents(root), /Saved intent only|not applied/);
  assert.equal(
    nodes(root).some(node => node.tag === 'button'),
    false
  );
  input.arrangement.state = 'stale';
  assert.equal(arrangementPresentation(input).appliedEffects, 1);
  // A catalog that moved on after the apply is a note on the receipt, not a
  // warning that the record is no longer authority.
  input.catalog_current = false;
  const moved = dom();
  renderArrangementHistory(moved, input);
  assert.match(contents(moved), /The catalog has changed since this was applied/);
  assert.doesNotMatch(contents(moved), /Historical context has changed/);
  input.collection_receipt.effects[0].kind = 'select_activation';
  assert.equal(arrangementPresentation(input), null);
});

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
  // A Home proposal is reviewed beside this card, so the card never claims
  // that review is unavailable; one without a reviewer still says so.
  const home = dom();
  renderArrangementHistory(home, fixture());
  assert.match(contents(home), /Nothing changes until you review the exact collection metadata/);
  assert.doesNotMatch(contents(home), /not available here/);
  const inert = fixture();
  delete inert.kind;
  delete inert.catalog_current;
  const plain = dom();
  renderArrangementHistory(plain, inert);
  assert.match(contents(plain), /Review and apply are not available here/);
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

function paFixture(groupState = 'not_reviewed', reviewable = true, declinable = true) {
  return {
    conversation_id: 'chat-1',
    message_id: 'answer-1',
    context_current: true,
    arrangement: {
      ...fixture().arrangement,
      id: 'saved-id',
      operations: 2,
      proposed_operations: [
        { kind: 'create_group', name: 'Client <b>work</b>', members: 2 },
        { kind: 'create_group', name: 'Experiments', members: 1 }
      ],
      explicit_needs_review: 0,
      review_questions: []
    },
    execution: {
      state: 'draft',
      reviewable,
      declinable,
      groups: [
        { operation: 0, name: 'Client <b>work</b>', members: 2, state: groupState },
        { operation: 1, name: 'Experiments', members: 1, state: 'not_reviewed' }
      ]
    }
  };
}

const buttons = root => nodes(root).filter(node => node.tag === 'button');

test('a PA arrangement lists its groups with a review control only while it is open', () => {
  const root = dom();
  assert.equal(
    renderArrangementHistory(root, paFixture(), { reviewGroup() {}, decline() {} }),
    true
  );
  const all = nodes(root);
  assert.ok(all.some(node => node.attributes['aria-label'] === 'Proposed groups'));
  assert.deepEqual(
    buttons(root).map(button => button.attributes['aria-label'] || button.textContent),
    ['Review group Client <b>work</b>', 'Review group Experiments', 'Decline proposal']
  );
  assert.match(contents(root), /Set up there/);
  assert.ok(all.every(node => !('innerHTML' in node)));
  // Without caller actions the same card is read-only.
  const plain = dom();
  renderArrangementHistory(plain, paFixture());
  assert.equal(buttons(plain).length, 0);
  // Not reviewable: no review control, and the card says why.
  const historic = dom();
  renderArrangementHistory(historic, paFixture('not_reviewed', false), {
    reviewGroup() {},
    decline() {}
  });
  assert.deepEqual(
    buttons(historic).map(button => button.textContent),
    ['Decline proposal']
  );
  assert.match(contents(historic), /no longer the one attached/);
  // A record the server will not decline (imported, or superseded) shows no
  // Decline, even while it reads as open.
  const imported = dom();
  renderArrangementHistory(imported, paFixture('not_reviewed', false, false), {
    reviewGroup() {},
    decline() {}
  });
  assert.equal(buttons(imported).length, 0);
});

test('a group list that does not match the record, or claims a receipt it lacks, hides the card', () => {
  const cases = [
    history => (history.execution.groups[0].operation = 5),
    history => (history.execution.groups[1].operation = 0),
    history => (history.arrangement.proposed_operations[0].kind = 'create_collection'),
    history => (history.execution.groups[0].state = 'completed'),
    history => {
      history.execution.groups[0].state = 'completed';
      history.execution.groups[0].route = 'https://example.com/x';
      history.execution.groups[0].workspaces = 2;
    },
    history => (history.execution.groups[0].state = 'approved'),
    history => (history.execution.reviewable = 'yes'),
    history => (history.execution.declinable = 'yes'),
    history => (history.execution.groups[0].members = 25),
    history => {
      history.kind = 'proposal';
      history.catalog_current = true;
    }
  ];
  for (const mutate of cases) {
    const history = paFixture();
    mutate(history);
    assert.equal(arrangementPresentation(history), null);
  }
});

test('created and confirmed groups show their receipt, never another review or a decline', () => {
  const history = paFixture('completed');
  Object.assign(history.execution.groups[0], { route: '/workspaces/client-work', workspaces: 2 });
  history.execution.state = 'partially_applied';
  const root = dom();
  renderArrangementHistory(root, history, { reviewGroup() {}, decline() {} });
  const view = arrangementPresentation(history);
  assert.equal(view.title, 'Arrangement partly applied');
  const link = nodes(root).find(node => node.tag === 'a');
  assert.equal(link.href, '/workspaces/client-work');
  assert.match(contents(root), /2 workspaces read back from the receipt/);
  assert.match(contents(root), /Canonical receipts: 1 group was created/);
  assert.deepEqual(
    buttons(root).map(button => button.attributes['aria-label']),
    ['Review group Experiments']
  );
  history.execution.groups[1].state = 'applying';
  const busy = dom();
  renderArrangementHistory(busy, history, { reviewGroup() {}, decline() {} });
  assert.equal(buttons(busy).length, 0);
});

test('group actions report host messages and re-render only canonical history', async () => {
  const container = dom();
  const calls = [];
  let fail = true;
  assert.equal(
    mountArrangementHistory(container, {
      id: 'saved-id',
      load: async () => paFixture(),
      initial: paFixture(),
      actions: {
        reviewGroup: async (operation, history) => {
          calls.push([operation, history.arrangement.revision]);
          if (fail) throw new Error('Another setup review is open. Nothing was created.');
          return {};
        },
        decline: async () => {
          const declined = paFixture();
          declined.arrangement.state = 'cancelled';
          declined.arrangement.revision = 2;
          declined.execution.state = 'cancelled';
          declined.execution.reviewable = false;
          declined.execution.declinable = false;
          return { history: declined, message: 'Proposal declined.' };
        }
      }
    }),
    true
  );
  assert.equal(container.children[0].textContent, 'Refresh saved arrangement');
  const review = buttons(container).find(
    button => button.attributes['aria-label'] === 'Review group Experiments'
  );
  await review.events.click();
  assert.deepEqual(calls, [[1, 1]]);
  assert.match(contents(container), /Another setup review is open/);
  fail = false;
  const decline = buttons(container).find(button => button.textContent === 'Decline proposal');
  await decline.events.click();
  assert.match(contents(container), /Proposal declined\./);
  assert.match(contents(container), /Declined arrangement — not applied/);
  assert.ok(!buttons(container).some(button => button.textContent === 'Decline proposal'));
});
