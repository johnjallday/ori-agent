import test from 'node:test';
import assert from 'node:assert/strict';

import {
  boundedAssistantHandoff,
  canSubmitAssistantWork,
  personalAssistantPanelView
} from './personal-assistant-panel.js';

test('personal assistant panel covers unavailable, pre-hire, active, paused, and repair states', () => {
  assert.equal(personalAssistantPanelView(null).known, false);

  const preHire = personalAssistantPanelView({ state: 'needs_hire' });
  assert.equal(preHire.known, true);
  assert.equal(preHire.helpOnly, true);
  assert.equal(preHire.available, false);
  assert.equal(preHire.needsHire, true);

  const active = personalAssistantPanelView({ state: 'active', display_name: 'Nova' });
  assert.equal(active.available, true);
  assert.equal(active.name, 'Nova');
  assert.match(active.placeholder, /Ask Nova/);

  const paused = personalAssistantPanelView({ state: 'paused', display_name: 'Nova' });
  assert.equal(paused.available, true);
  assert.equal(paused.paused, true);

  const repair = personalAssistantPanelView({ state: 'repair_needed' });
  assert.equal(repair.repair, true);
  assert.equal(repair.available, false);
});

test('handoff text is exact, trimmed, unicode-safe, and bounded without submitting', () => {
  assert.equal(boundedAssistantHandoff('  send the notes  '), 'send the notes');
  assert.equal(Array.from(boundedAssistantHandoff('🦊'.repeat(500))).length, 400);
});

test('assistant composer refuses empty, unavailable, pending, and double-click states', () => {
  assert.equal(canSubmitAssistantWork({ available: true, pending: false, text: 'help' }), true);
  assert.equal(canSubmitAssistantWork({ available: false, pending: false, text: 'help' }), false);
  assert.equal(canSubmitAssistantWork({ available: true, pending: true, text: 'help' }), false);
  assert.equal(canSubmitAssistantWork({ available: true, pending: false, text: '  ' }), false);
});
