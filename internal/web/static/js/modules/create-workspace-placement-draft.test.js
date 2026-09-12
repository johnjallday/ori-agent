import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const SOURCE = readFileSync(
  new URL('./create-workspace-placement-draft.js', import.meta.url),
  'utf8'
);

function loadAPI() {
  const sandbox = { window: {} };
  sandbox.globalThis = sandbox;
  vm.createContext(sandbox);
  vm.runInContext(SOURCE, sandbox, { filename: 'create-workspace-placement-draft.js' });
  return sandbox.window.CreateWorkspacePlacementDraft;
}

function projection(overrides = {}) {
  return {
    version: 1,
    source_revision: 'a'.repeat(64),
    state: 'ready_grouped',
    policy: 'required',
    selected_composition: 'grouped',
    home: { exists: true, workspace_id: 'home-1', name: 'Research Home' },
    required_home_roles: {
      verification: 'verified',
      required: 1,
      filled: 0,
      missing: 1,
      roles: [{ role_id: 'coordinator', state: 'empty' }]
    },
    ...overrides
  };
}

test('loading is distinct from confirmed absence', () => {
  const api = loadAPI();
  const draft = api.createDraft({
    templateKey: 'template:research',
    policy: 'required',
    composition: 'grouped'
  });
  const ticket = api.begin(draft);
  assert.equal(draft.status, 'loading');
  assert.equal(draft.projection, null);

  assert.equal(
    api.resolve(
      draft,
      ticket,
      projection({
        state: 'home_creation_review_required',
        home: { exists: false, proposed_name: 'Research Home' },
        required_home_roles: {
          verification: 'group_absent',
          required: 1,
          filled: 0,
          missing: 1,
          roles: [{ role_id: 'coordinator', state: 'empty' }]
        }
      })
    ),
    true
  );
  assert.equal(draft.status, 'ready');
  assert.equal(draft.projection.home.exists, false);
});

test('late responses lose ownership after composition changes', () => {
  const api = loadAPI();
  const draft = api.createDraft({
    templateKey: 'template:research',
    policy: 'recommended',
    composition: 'grouped'
  });
  const groupedTicket = api.begin(draft);
  assert.equal(api.setComposition(draft, 'standalone'), true);
  const standaloneTicket = api.begin(draft);

  assert.equal(api.resolve(draft, groupedTicket, projection()), false);
  assert.equal(draft.status, 'loading');
  assert.equal(
    api.resolve(
      draft,
      standaloneTicket,
      projection({
        state: 'ready_standalone',
        selected_composition: 'standalone',
        home: undefined,
        required_home_roles: { verification: 'not_applicable' }
      })
    ),
    true
  );
  assert.equal(draft.projection.state, 'ready_standalone');
});

test('an old modal generation cannot overwrite a new source draft', () => {
  const api = loadAPI();
  const oldDraft = api.createDraft({
    templateKey: 'template:old',
    policy: 'required',
    composition: 'grouped'
  });
  const ticket = api.begin(oldDraft);
  const newDraft = api.createDraft({
    templateKey: 'template:new',
    policy: 'required',
    composition: 'grouped'
  });
  assert.equal(api.resolve(newDraft, ticket, projection()), false);
  assert.equal(newDraft.status, 'idle');
  assert.equal(newDraft.projection, null);
});

test('canonical source destination and holder changes invalidate final review', () => {
  const api = loadAPI();
  const draft = api.createDraft({
    templateKey: 'template:research',
    policy: 'required',
    composition: 'grouped'
  });
  api.resolve(draft, api.begin(draft), projection());
  draft.review = { review_token: 'final-placement' };

  const staffed = projection({
    required_home_roles: {
      verification: 'verified',
      required: 1,
      filled: 1,
      missing: 0,
      roles: [{ role_id: 'coordinator', state: 'filled', agent: { name: 'Coordinator' } }]
    }
  });
  assert.equal(api.resolve(draft, api.begin(draft), staffed), true);
  assert.equal(draft.review, null);

  draft.review = { review_token: 'fresh-placement' };
  assert.equal(api.resolve(draft, api.begin(draft), staffed), true);
  assert.equal(
    draft.review.review_token,
    'fresh-placement',
    'an identical canonical recheck invalidated valid review'
  );

  draft.review = { review_token: 'stale-source' };
  assert.equal(
    api.resolve(draft, api.begin(draft), {
      ...staffed,
      source_revision: 'b'.repeat(64)
    }),
    true
  );
  assert.equal(draft.review, null);
});

test('errors are scoped to the request generation that owns them', () => {
  const api = loadAPI();
  const draft = api.createDraft({
    templateKey: 'template:research',
    policy: 'required',
    composition: 'grouped'
  });
  const stale = api.begin(draft);
  const current = api.begin(draft);
  assert.equal(api.reject(draft, stale, 'old failure'), false);
  assert.equal(api.reject(draft, current, 'current failure'), true);
  assert.equal(draft.status, 'error');
  assert.equal(draft.projectionError, 'current failure');
});
