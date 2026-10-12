import test from 'node:test';
import assert from 'node:assert/strict';
import { groupBuildState, openGroupBuilder } from './group-builder.js';

const fresh = () => ({
  receipts: {},
  steps: [
    {
      kind: 'project_connect',
      preparation: { exists: false },
      actions: [{ id: 'review_create_group' }]
    }
  ]
});

test('only a current canonical group action enables the guided builder', () => {
  assert.equal(groupBuildState(fresh()), 'create');
  const blocked = fresh();
  blocked.steps[0].actions = [];
  assert.equal(groupBuildState(blocked), 'unavailable');
  blocked.steps[0].preparation = null;
  assert.equal(groupBuildState(blocked), 'unavailable');
});
test('historical project or Home receipts never authorize a replacement group', () => {
  for (const field of ['home_workspace_id', 'project_workspace_id']) {
    const journey = fresh();
    journey.receipts[field] = 'historical-id';
    assert.equal(groupBuildState(journey), 'unavailable');
  }
});
test('a named missing Home provider is its own state, not an unverifiable group', () => {
  const journey = fresh();
  journey.steps[0].actions = [];
  journey.steps[0].status = 'blocked';
  journey.steps[0].reason_code = 'home_provider_missing';
  journey.steps[0].home_provider = {
    plugin_id: 'music-project-management',
    reason: 'plugin_install_required'
  };
  assert.equal(groupBuildState(journey), 'provider');
  // Without the projection the same payload stays unverifiable, as before.
  delete journey.steps[0].home_provider;
  assert.equal(groupBuildState(journey), 'unavailable');
});
test('existing canonical groups are reused and active operations cannot start a group', () => {
  const journey = fresh();
  journey.busy = {};
  assert.equal(groupBuildState(journey), 'busy');
  journey.steps[0].preparation.exists = true;
  assert.equal(groupBuildState(journey), 'existing');
});

// One card, one confirm. Just enough of the page for the builder: the name
// field, the shared creator it opens, and the setup journey's endpoints. Each
// page is its own run: the builder keeps a closed run's name for a reopen.
let builderRuns = 0;
function builderPage({ name = 'Music Production Home', failReview = false } = {}) {
  const runID = `run-${++builderRuns}`;
  const journey = () => ({
    run_id: runID,
    state_revision: 7,
    journey: { source: 'host', id: 'demo_quest' },
    receipts: {},
    steps: [
      {
        kind: 'project_connect',
        preparation: { exists: false, name },
        actions: [{ id: 'review_create_group' }]
      }
    ]
  });
  const calls = [];
  const modalHandlers = {};
  const nameInput = { value: name };
  const opened = { steps: [], closed: 0 };
  const context = { guided: null, submitting: false };
  const manager = {
    workspaceCreatorContext: context,
    showAddWorkspaceModal(options) {
      context.guided = options.guided;
    },
    restoreWorkspaceCreatorDetailsDraft() {},
    refreshWizardChrome() {},
    goToWizardStep(step) {
      opened.steps.push(step);
    }
  };
  const previous = {
    window: globalThis.window,
    document: globalThis.document,
    fetch: globalThis.fetch,
    bootstrap: globalThis.bootstrap,
    CustomEvent: globalThis.CustomEvent
  };
  globalThis.window = { sessionManager: manager, dispatchEvent() {} };
  globalThis.CustomEvent ||= class {
    constructor(type) {
      this.type = type;
    }
  };
  globalThis.document = {
    activeElement: null,
    getElementById: id =>
      id === 'folderNameInput'
        ? nameInput
        : id === 'addFolderModal'
          ? {
              addEventListener: (type, handler) => {
                modalHandlers[type] = handler;
              },
              removeEventListener() {}
            }
          : null
  };
  globalThis.bootstrap = {
    Modal: { getInstance: () => ({ hide: () => opened.closed++ }) }
  };
  globalThis.fetch = async (url, options = {}) => {
    const body = options.body ? JSON.parse(options.body) : null;
    calls.push({ url, method: options.method || 'GET', body });
    const current = journey();
    if (url.endsWith('/actions/review_create_group')) {
      if (failReview)
        return {
          ok: false,
          status: 409,
          json: async () => ({ error: { guidance: 'Another group already uses that folder.' } })
        };
      return {
        ok: true,
        json: async () => ({
          setup_journey: current,
          review: {
            commit_action: 'create_group',
            token: 'review-token',
            group: { name: body.input.name }
          }
        })
      };
    }
    if (url.endsWith('/actions/create_group')) {
      current.steps[0].preparation.exists = true;
      return { ok: true, json: async () => ({ setup_journey: current }) };
    }
    return { ok: true, json: async () => ({ setup_journey: current }) };
  };
  return {
    journey: journey(),
    calls,
    opened,
    context,
    nameInput,
    actions: () =>
      calls.filter(call => call.method === 'POST').map(call => call.url.split('/').pop()),
    // Closing the creator is what releases the builder for the next test.
    close() {
      modalHandlers['hidden.bs.modal']?.();
      Object.assign(globalThis, previous);
    }
  };
}

test('Build Group opens on one reviewed card, and its one button creates the group', async () => {
  const page = builderPage();
  try {
    assert.equal(openGroupBuilder({ journey: page.journey }), true);
    // Setup already names the group, so the creator opens on its Review.
    assert.deepEqual(page.opened.steps, [4]);
    const guided = page.context.guided;
    assert.equal(guided.review, null);

    // The creator shows Review: the group is reviewed without a press.
    await guided.ensureReview();
    assert.deepEqual(page.actions(), ['review_create_group']);
    assert.deepEqual(guided.review, {
      name: 'Music Production Home',
      existing: false,
      pending: false
    });
    // Repaints of the same card review nothing more.
    await guided.ensureReview();
    await guided.ensureReview();
    assert.deepEqual(page.actions(), ['review_create_group']);

    // The one press commits that review.
    await guided.submit();
    assert.deepEqual(page.actions(), ['review_create_group', 'create_group']);
    const commit = page.calls.at(-1).body;
    assert.equal(commit.review_token, 'review-token');
    assert.deepEqual(commit.input, { name: 'Music Production Home' });
    assert.equal(page.opened.closed, 1);
  } finally {
    page.close();
  }
});

test('a renamed group is reviewed again when Review is shown, never created under the old review', async () => {
  const page = builderPage();
  try {
    openGroupBuilder({ journey: page.journey });
    const guided = page.context.guided;
    await guided.ensureReview();
    page.nameInput.value = 'Studio B';
    await guided.ensureReview();
    assert.deepEqual(page.actions(), ['review_create_group', 'review_create_group']);
    assert.equal(guided.review.name, 'Studio B');
    await guided.submit();
    assert.deepEqual(page.calls.at(-1).body.input, { name: 'Studio B' });
  } finally {
    page.close();
  }
});

test('a group review that fails is not repeated by a repaint; the button asks again', async () => {
  const page = builderPage({ failReview: true });
  try {
    openGroupBuilder({ journey: page.journey });
    const guided = page.context.guided;
    await guided.ensureReview();
    assert.equal(guided.review, null);
    assert.match(guided.error, /Another group already uses that folder/);
    await guided.ensureReview();
    assert.deepEqual(page.actions(), ['review_create_group']);
    // Pressing the button is the retry; nothing is created without a review.
    await guided.submit();
    assert.deepEqual(page.actions(), ['review_create_group', 'review_create_group']);
  } finally {
    page.close();
  }
});

test('a group with no name yet opens on Details and reviews nothing', async () => {
  const page = builderPage({ name: '' });
  try {
    openGroupBuilder({ journey: page.journey });
    assert.deepEqual(page.opened.steps, []);
    await page.context.guided.ensureReview();
    assert.deepEqual(page.actions(), []);
  } finally {
    page.close();
  }
});
