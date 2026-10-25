import test from 'node:test';
import assert from 'node:assert/strict';
import { folderTaskDisclosure } from './folder-task-disclosure.js';
import { folderFirstLookOpen, folderReceiptView } from './personal-assistant-folder.js';
import { assessmentBannerView } from './workspace-first-task-banner.js';

const look = {
  state: 'seeded',
  recipe: 'folder_summary',
  optional: true,
  can_start: true,
  task_id: 'summary',
  description: 'Tracks, session overview and one next step',
  input_scope: 'Saved Session.rpp only',
  executor: { agent: 'Reader', provider: 'fixture', model: '<literal model>', digest: 'reviewed' }
};

test('optional summary names actual inputs, executor, token use and enforced read-only scope', () => {
  const disclosure = folderTaskDisclosure(look);
  assert.match(disclosure, /Saved Session.rpp only/);
  assert.match(disclosure, /Reader · fixture \/ <literal model>/);
  assert.match(disclosure, /connection not verified/);
  assert.match(disclosure, /Each explicit attempt uses model tokens/);
  assert.match(disclosure, /no model tools, source-file changes or external actions/);
  assert.match(folderTaskDisclosure({}), /configuration is unavailable/);
});

test('ready workspace keeps its optional task discoverable without an urgent Home setup state', () => {
  const offer = {
    id: 'offer',
    status: 'resolved',
    first_task: look,
    outcome: {
      kind: 'project',
      route: '/workspaces/session',
      receipt: [{ kind: 'workspace', name: 'Session' }]
    }
  };
  const receipt = folderReceiptView(offer);
  assert.equal(receipt.visible, true);
  assert.equal(receipt.firstLook.actions[0].label, 'Start summary');
  assert.match(receipt.firstLook.hint, /<literal model>/);
  assert.equal(folderFirstLookOpen(offer), false);
  assert.equal(folderFirstLookOpen({ ...offer, first_task: { ...look, state: 'running' } }), true);
  assert.equal(folderFirstLookOpen({ ...offer, first_task: { ...look, state: 'failed' } }), true);
});

test('workspace banner describes the same optional summary and executor without launching it', () => {
  const view = assessmentBannerView(look);
  assert.equal(view.phase, 'available');
  assert.match(view.heading, /Tracks, session overview and one next step/);
  assert.match(view.detail, /Reader · fixture/);
  assert.equal(view.action.label, 'Start summary');
  const missing = assessmentBannerView({
    ...look,
    can_start: false,
    reason: 'no_model',
    executor: null,
    message: 'Reader has no model chosen.'
  });
  assert.equal(missing.phase, 'available');
  assert.equal(missing.action, null);
  assert.match(missing.detail, /no model chosen/);
});
