import test from 'node:test';
import assert from 'node:assert/strict';

import { setupRunView } from './personal-assistant-folder.js';

// A stopped setup's headline says what it needs ("Album-4 needs you to choose
// the project file"), so the card can be read without its sentence. The ask
// comes from the same stop reason as the sentence and the primary action.
const stopped = reason => ({
  status: 'stopped',
  stop_reason: reason,
  lines: [{ kind: 'workspace', name: 'Creates a Song workspace', state: 'done' }]
});

const ask = (reason, options = {}) => setupRunView(stopped(reason), 'Album-4', options).ask;

test('each stop reason asks for the step its primary action takes', () => {
  const cases = {
    needs_choice: 'choose the project file',
    needs_pick: 'pick the folder again',
    needs_model: 'set up a model',
    plan_changed: 'review the changed setup',
    install_failed: 'check a plugin that did not install',
    interrupted: 'finish setup',
    consent_stale: 'review its shared agent',
    assistant_missing: 'choose its agent',
    failed: 'retry a step that did not finish'
  };
  for (const [reason, expected] of Object.entries(cases)) assert.equal(ask(reason), expected);
});

test('a variant words only the asks its own stop copy explains differently', () => {
  assert.equal(ask('plan_changed', { software: true }), 'review the setup again');
  assert.equal(ask('plan_changed', { structure: true }), 'review the saved workspaces');
  assert.equal(ask('member_failed', { structure: true }), 'continue creating its workspaces');
  // An inbox setup re-reviews a changed plan from a new pick, as its action does.
  assert.equal(ask('plan_changed', { inbox: true }), 'pick the folder again');
  assert.equal(ask('needs_model', { software: true }), 'set up a model');
  assert.equal(ask('needs_model', { portfolio: true }), 'set up a model');
});

test('a reason the card does not explain asks what its "did not finish" sentence does', () => {
  assert.equal(ask('something_unknown'), 'retry a step that did not finish');
  // The project card has no group workspaces to continue.
  assert.equal(ask('member_failed'), 'retry a step that did not finish');
  assert.equal(ask('needs_choice', { inbox: true }), 'retry a step that did not finish');
  assert.equal(ask('consent_stale', { software: true }), 'retry a step that did not finish');
  assert.equal(ask(''), 'retry a step that did not finish');
});

test('only a stopped run carries an ask', () => {
  for (const status of ['running', 'done']) {
    const view = setupRunView({ status, lines: [] }, 'Album-4');
    assert.equal(view.ask, undefined, status);
  }
});
