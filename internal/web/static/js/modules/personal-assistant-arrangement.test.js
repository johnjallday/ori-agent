import test from 'node:test';
import assert from 'node:assert/strict';
import {
  arrangementAttachmentContext,
  libraryHomeLinks
} from './personal-assistant-arrangement.js';
import { ArrangementConsent } from './workspace-arrangement-consent.js';
const source = () => ({
  assistant: {
    assistant_id: 'pa',
    hq_workspace_id: 'hq',
    hq_agent_instance_id: 'instance',
    state: 'active',
    state_version: 3
  },
  view: { name: 'Atlas', available: true },
  conversation: { id: 'conversation' },
  context: { origin: 'personal_assistant_panel', page_path: '/' },
  folder: { selection_id: 'selection', revision: 'revision' },
  model: { configured: true, provider: 'lmstudio', model: 'fixture' },
  goal: 'Organize client work'
});

test('PA uses actual relationship fields and exact folder/page/conversation/model identity', () => {
  for (const mutate of [
    x => {
      x.assistant.assistant_id = 'other';
    },
    x => {
      x.assistant.hq_workspace_id = 'other';
    },
    x => {
      x.assistant.hq_agent_instance_id = 'other';
    },
    x => {
      x.assistant.state_version++;
    },
    x => {
      x.folder.revision = 'other';
    },
    x => {
      x.folder.selection_id = 'other';
    },
    x => {
      x.conversation.id = 'other';
    },
    x => {
      x.context.page_path = '/settings';
    },
    x => {
      x.model.model = 'other';
    }
  ]) {
    const input = source();
    const consent = new ArrangementConsent();
    const { epoch } = consent.update(arrangementAttachmentContext(input));
    consent.chooseSharing(true, epoch);
    mutate(input);
    assert.equal(consent.take(arrangementAttachmentContext(input)), null);
  }
});

test('unfocused live attachment is eligible, but missing/historical/focused context is not', () => {
  const consent = new ArrangementConsent();
  assert.equal(consent.update(arrangementAttachmentContext(source())).available, true);
  for (const patch of [
    { folder: null },
    { conversation: null },
    { model: null },
    { folder: { selection_id: 'selection', historical: true } },
    { folder: { selection_id: 'selection', focus_ids: ['node'] } }
  ]) {
    assert.equal(
      consent.update(arrangementAttachmentContext({ ...source(), ...patch })).available,
      false
    );
  }
});

test('the Home handoff links only to validated Home routes, by name', () => {
  assert.deepEqual(
    libraryHomeLinks({
      homes: [
        { id: 'h1', name: ' Music Production Home ', route: '/workspaces/music-home' },
        { id: 'h2', name: '<img src=x>', route: '/workspaces/studio' },
        { id: 'h3', name: 'Offsite', route: 'https://example.com/workspaces/x' },
        { id: 'h4', name: 'Nested', route: '/workspaces/a/b' },
        { id: 'h5', name: 'Query', route: '/workspaces/a?panel=x' },
        { id: 'h6', name: '', route: '/workspaces/blank' },
        { id: 'h7', name: 'No route' },
        null
      ]
    }),
    [
      { name: 'Music Production Home', href: '/workspaces/music-home' },
      { name: '<img src=x>', href: '/workspaces/studio' }
    ]
  );
  assert.deepEqual(libraryHomeLinks(null), []);
  assert.deepEqual(libraryHomeLinks({ homes: 'many' }), []);
});
