import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const source = readFileSync(new URL('./workspace-continuity.js', import.meta.url), 'utf8');

// Loaded into THIS realm so arrays share the test realm's prototypes, which
// deepStrictEqual compares.
function load() {
  globalThis.window = globalThis.window || {};
  vm.runInThisContext(source, { filename: 'workspace-continuity.js' });
  return globalThis.window.WorkspaceContinuity;
}

const continuity = load();

const reviewable = {
  status: 'review_required',
  import_supported: true,
  tree_digest: 't'.repeat(64),
  destination_digest: 'd'.repeat(64),
  members: 2,
  saved_work: { tasks: 4 },
  history: { sessions: 1, messages: 2, follow_ups: 6, brief_revisions: 3, notes: 0, uploads: 1 },
  assistant_candidates: [{ display_name: 'Ada' }]
};

test('a legacy folder keeps the ordinary import path', () => {
  const plain = continuity.describeReview({ status: 'legacy' });
  assert.equal(plain.mode, 'legacy');
  // Any other folder (not an exported Ori workspace) gets no notice at all.
  assert.deepEqual(plain.lines, []);
  assert.equal(plain.adopt, undefined);
});

test('an older Ori copy says what it lacks and offers only the assistant its files prove', () => {
  const older = continuity.describeReview({ status: 'legacy', legacy_workspace: true });
  assert.equal(older.title, 'Older copy');
  assert.match(
    older.lines[0],
    /conversations, follow-ups, Daily Briefs and uploaded files are not in it/
  );
  assert.equal(older.adopt, null);

  const hq = continuity.describeReview({
    status: 'legacy',
    legacy_workspace: true,
    legacy_assistant: { display_name: 'Ada' }
  });
  assert.equal(hq.adopt.label, 'Continue with Ada as my personal assistant');
  assert.match(hq.lines.join(' '), /starts paused, and focus and brief schedule are new choices/);

  const blocked = continuity.describeReview({
    status: 'legacy',
    legacy_workspace: true,
    legacy_assistant: { display_name: 'Ada' },
    legacy_adoption_blocked: 'existing_assistant'
  });
  assert.equal(blocked.adopt, null);
  assert.match(blocked.lines.join(' '), /Your current assistant stays unchanged/);
});

test('a damaged or incomplete copy is blocked, never treated as a legacy success', () => {
  const view = continuity.describeReview({ status: 'unavailable' });
  assert.equal(view.mode, 'blocked');
  assert.deepEqual(view.actions, []);
  assert.match(view.lines.join(' '), /Nothing was imported/);
  assert.match(view.lines.join(' '), /Ready to move/);
});

test('a clean destination offers Import and continue first, with workspace-only as the alternative', () => {
  const view = continuity.describeReview({
    ...reviewable,
    actions: ['continue', 'workspace_only'],
    recommended_action: 'continue'
  });
  assert.equal(view.mode, 'review');
  assert.equal(view.title, 'Continue with Ada?');
  assert.deepEqual(
    view.actions.map(action => [action.action, action.label, action.primary]),
    [
      ['continue', 'Import and continue', true],
      ['workspace_only', 'Import workspace only', false]
    ]
  );
  const text = view.lines.join(' ');
  assert.match(text, /across 2 workspaces/);
  assert.match(text, /6 follow-ups, 3 Daily Briefs/);
  assert.match(text, /Background routines stay off/);
  assert.match(text, /API keys and permissions are not copied/);
});

test('an existing assistant keeps its place and the copy imports as a workspace only', () => {
  const view = continuity.describeReview({
    ...reviewable,
    actions: ['workspace_only'],
    recommended_action: 'workspace_only',
    adoption_blocked: 'existing_assistant'
  });
  assert.equal(view.title, 'Your current assistant will stay unchanged');
  assert.deepEqual(
    view.actions.map(action => action.action),
    ['workspace_only']
  );
  assert.equal(view.actions[0].label, 'Import workspace');
  assert.equal(view.actions[0].primary, true);
});

test('an identical earlier import opens the workspace instead of importing again', () => {
  const view = continuity.describeReview({
    ...reviewable,
    import_supported: false,
    recommended_action: 'open',
    already_imported: { operation_id: 'op', workspace_id: 'ws-1' }
  });
  assert.equal(view.mode, 'already');
  assert.equal(view.workspaceId, 'ws-1');
  assert.deepEqual(
    view.actions.map(action => action.action),
    ['open']
  );
});

test('a workspace that already exists here is a conflict with no import button', () => {
  const view = continuity.describeReview({ ...reviewable, conflict: 'workspace_exists' });
  assert.equal(view.mode, 'blocked');
  assert.deepEqual(view.actions, []);
});

test('the report separates restored work, missing data and local setup', () => {
  const view = continuity.describeReport({
    status: 'complete',
    adopted: true,
    assistant_name: 'Ada',
    members: [
      {
        components: [
          { domain: 'sessions', status: 'restored', count: 3 },
          { domain: 'followups', status: 'restored', count: 6 },
          { domain: 'uploads', status: 'restored', count: 1, reason: 'some_files_unavailable' },
          { domain: 'brief_history', status: 'unavailable', reason: 'not_in_copy' },
          { domain: 'notes', status: 'restored', count: 0 }
        ]
      }
    ]
  });
  assert.equal(view.complete, true);
  assert.match(view.title, /some information was not in this folder/);
  assert.deepEqual(view.restored, ['Conversations: 3', 'Follow-ups: 6', 'Uploaded files: 1']);
  assert.deepEqual(view.missing, ['Daily Brief history: not in this copy']);
  assert.match(view.setup[0], /Ada is your personal assistant again\. It starts paused/);
  assert.ok(view.setup.some(line => /reconnected here/.test(line)));
  assert.ok(view.setup.some(line => /Background routines are off/.test(line)));
});

test('a workspace-only HQ reports its assistant as kept, not missing', () => {
  const view = continuity.describeReport({
    status: 'complete',
    members: [
      { components: [{ domain: 'assistant', status: 'unavailable', reason: 'not_adopted' }] }
    ]
  });
  assert.deepEqual(view.missing, []);
  assert.ok(view.setup.some(line => /current assistant is unchanged/.test(line)));
});

test('an interrupted import is retryable and never looks complete', () => {
  const view = continuity.describeReport({ status: 'interrupted', members: [] });
  assert.equal(view.complete, false);
  assert.equal(view.retryable, true);
  assert.match(view.title, /stopped before it finished/);
});

test('status explains readiness to move and imported activation', () => {
  const ready = continuity.describeStatus({
    state: 'ready',
    checkpoint_at: '2026-09-01T12:00:00Z',
    tree_ready: true
  });
  assert.match(ready.label, /^Ready to move as of /);
  const imported = continuity.describeStatus({
    state: 'update_needed',
    attachment: 'imported_inactive',
    imported: true,
    attachment_version: 3
  });
  assert.equal(imported.label, 'Saving latest work');
  assert.match(imported.detail, /Wait for “Ready to move” before copying/);
  assert.equal(imported.canActivate, true);
  assert.equal(imported.version, 3);
  const unsafe = continuity.describeStatus({ state: 'unavailable', reason: 'folder_unsafe' });
  assert.match(unsafe.detail, /link or a file name/);
});

test('confirmImport sends exactly the reviewed digests and choice', async () => {
  let sent;
  const result = await continuity.confirmImport(
    async (url, init) => {
      sent = { url, body: JSON.parse(init.body) };
      return {
        ok: true,
        status: 201,
        json: async () => ({ success: true, import: { status: 'complete' } })
      };
    },
    '/copy/hq',
    reviewable,
    'continue'
  );
  assert.equal(sent.url, '/api/workspaces/import/continuity');
  assert.deepEqual(sent.body, {
    path: '/copy/hq',
    tree_digest: reviewable.tree_digest,
    destination_digest: reviewable.destination_digest,
    action: 'continue'
  });
  assert.equal(result.ok, true);
});
