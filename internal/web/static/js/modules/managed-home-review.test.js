import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  managedHomeReviewKey,
  forgetManagedHomeReference,
  managedHomeLauncherLanding
} from './managed-home-review.js';

test('a launcher landing survives the shared result only when nothing needs the group notice', () => {
  const landing = '/assistant?folder_offer_id=offer-1';
  assert.equal(managedHomeLauncherLanding({ landing, notice: { tone: 'success' } }), landing);
  // A role that failed to staff must be shown on the group page instead.
  assert.equal(managedHomeLauncherLanding({ landing, notice: { tone: 'warning' } }), '');
  assert.equal(managedHomeLauncherLanding({ landing: '', notice: { tone: 'success' } }), '');
  assert.equal(managedHomeLauncherLanding(null), '');
});

test('managed Home material key binds owner generation, exact review/source/name and full staged staffing', t => {
  const previous = globalThis.document;
  let name = 'Reviewed Home';
  globalThis.document = { getElementById: () => ({ value: name }) };
  t.after(() => {
    if (previous === undefined) delete globalThis.document;
    else globalThis.document = previous;
  });
  const entry = { id: 'exact-template', revision: 'revision-1' };
  const staffing = [
    {
      roleId: 'lead',
      fill: {
        mode: 'create',
        name: 'Reviewed Agent',
        model: 'reviewed-model',
        provider: 'lmstudio'
      }
    }
  ];
  const manager = {
    workspaceCreatorContext: {
      generation: 1,
      groupTemplates: { review: { data: { review_token: 'exact-review' } } }
    }
  };
  const creator = {
    selectedManaged: () => entry,
    reviewRequest: (source, text) => ({
      group_template_id: source.id,
      revision: source.revision,
      name: text
    }),
    staffingPlan: () => staffing
  };
  const key = () => managedHomeReviewKey(manager, creator);
  const original = key();
  for (const [object, property, changed] of [
    [entry, 'revision', 'revision-2'],
    [staffing[0].fill, 'model', 'changed-model'],
    [staffing[0].fill, 'provider', 'changed-provider'],
    [staffing[0].fill, 'name', 'Changed Agent'],
    [manager.workspaceCreatorContext, 'generation', 2],
    [manager.workspaceCreatorContext.groupTemplates.review.data, 'review_token', 'another-review']
  ]) {
    const old = object[property];
    object[property] = changed;
    assert.notEqual(key(), original, property);
    object[property] = old;
  }
  name = 'Changed Home';
  assert.notEqual(key(), original);
  name = 'Reviewed Home';
  staffing[0].fill.appearance = null;
  assert.notEqual(key(), original, 'presence-sensitive staffing is not collapsed');
  delete staffing[0].fill.appearance;
  assert.equal(key(), original);
});

test('closing an older Home result cannot erase another tab’s newer retained reference', () => {
  let current = JSON.stringify({
    request: { idempotency_key: 'new-operation', group_review_token: 'new-review' }
  });
  let removed = 0;
  const storage = {
    getItem: () => current,
    removeItem: () => {
      removed++;
      current = null;
    }
  };
  forgetManagedHomeReference(storage, {
    request: { idempotency_key: 'old-operation', group_review_token: 'old-review' }
  });
  assert.equal(removed, 0);
  forgetManagedHomeReference(storage, {
    request: { idempotency_key: 'new-operation', group_review_token: 'old-review' }
  });
  assert.equal(removed, 0);
  forgetManagedHomeReference(storage, {
    request: { idempotency_key: 'new-operation', group_review_token: 'new-review' }
  });
  assert.equal(removed, 1);
});
