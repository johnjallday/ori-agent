import test from 'node:test';
import assert from 'node:assert/strict';
import { folderOutcomeNote, setupRunView } from './personal-assistant-folder.js';

const { requirementsView, stoppedRequirement } = globalThis.OriFolderSetupVisual;
const visual = {
  requirements_checked: true,
  plugins: [
    { kind: 'integration', name: 'Integration fixture', operation: 'install', version: '1.0' },
    {
      kind: 'provider',
      name: 'Home fixture',
      operation: 'update',
      installed_version: '1.0',
      version: '2.0'
    }
  ],
  model: { state: 'configured', provider: 'fixture', name: 'recorded-model', settings: 'system' }
};

test('saved requirement rows use only reported runner progress and recorded configuration', () => {
  for (const [state, status] of Object.entries({
    waiting: 'Waiting',
    working: 'In progress',
    done: 'Complete',
    failed: 'Stopped here'
  })) {
    const setup = {
      visual,
      lines: [
        { kind: 'integration', state: 'done' },
        { kind: 'provider', state }
      ]
    };
    const view = requirementsView({
      offer: { setup, plan: { visual: { ...visual, model: { state: 'missing' } } } }
    });
    assert.equal(view.rows[0].status, 'Complete');
    assert.equal(view.rows[1].status, status);
    assert.equal(view.rows[1].version, 'Reviewed: 1.0 → 2.0');
    assert.equal(view.model.status, 'Configured');
    assert.equal(view.model.settings, '');
    assert.match(view.summary, /recorded/);
    assert.doesNotMatch(JSON.stringify(view), /downloading|percent|network failure/i);
  }
});

test('lost-selection recovery never falls back to an invented active-work or completed receipt note', () => {
  const offer = {
    status: 'awaiting_outcome',
    needs_pick: true,
    subject: { name: 'Session' },
    setup: { status: 'stopped', stop_reason: 'needs_model', lines: [] }
  };
  assert.equal(folderOutcomeNote(offer), '');
  assert.equal(
    folderOutcomeNote({ ...offer, setup: { status: 'done', lines: [] } }),
    'Setup finished; checking the saved result.'
  );
  assert.match(
    folderOutcomeNote({
      ...offer,
      setup: {
        status: 'running',
        lines: [{ kind: 'provider', name: 'Update Home plugin', state: 'working' }]
      }
    }),
    /Working on: Update Home plugin/
  );
});

test('a legacy run never borrows a fresh plan identity or model', () => {
  const view = requirementsView({
    offer: { setup: { lines: [{ kind: 'provider', state: 'failed' }] }, plan: { visual } }
  });
  assert.equal(view.rows[0].name, 'Home provider');
  assert.equal(view.model, null);
  assert.equal(view.complete, false);
});

test('a named failed prerequisite retains completed work and existing recovery precedence', () => {
  const setup = {
    status: 'stopped',
    stop_reason: 'install_failed',
    visual,
    lines: [
      { kind: 'integration', name: 'Installs the integration', state: 'done' },
      { kind: 'provider', name: 'Updates the Home plugin', state: 'failed' }
    ]
  };
  assert.equal(stoppedRequirement(setup), 'Home fixture');
  const view = setupRunView(setup, 'Session');
  assert.match(view.question, /1 of 2 steps finished/);
  assert.match(view.question, /Stopped at Home fixture/);
  assert.ok(view.actions.some(action => action.retry));
  assert.doesNotMatch(view.question, /network/i);
  for (const reason of ['plan_changed', 'needs_pick', 'consent_stale', 'assistant_missing']) {
    assert.ok(
      !setupRunView({ ...setup, stop_reason: reason }, 'Session').actions.some(
        action => action.retry
      )
    );
  }
  assert.equal(
    setupRunView({ ...setup, status: 'done' }, 'Session').statusLine,
    'Setup finished; checking the saved result.'
  );
});
