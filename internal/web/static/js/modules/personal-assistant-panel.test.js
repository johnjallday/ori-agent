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

test('a hired assistant with no HQ is named but disabled, unlike needsHire', () => {
  const needsHQ = personalAssistantPanelView({ state: 'needs_hq', display_name: 'Atlas' });
  assert.equal(needsHQ.known, true);
  assert.equal(needsHQ.available, false, 'submission must stay closed before HQ exists');
  assert.equal(needsHQ.needsHQ, true);
  assert.equal(needsHQ.needsHire, false);
  assert.equal(needsHQ.name, 'Atlas');
  // Unlike needsHire, the launcher may show this real identity.
  assert.equal(needsHQ.visible, true);
  assert.match(needsHQ.placeholder, /Build Atlas.s Personal HQ/);

  const provisioning = personalAssistantPanelView({
    state: 'provisioning_hq',
    display_name: 'Atlas'
  });
  assert.equal(provisioning.needsHQ, true);
  assert.equal(provisioning.available, false);
  assert.equal(provisioning.visible, true);

  // needsHire still has nothing trustworthy to show.
  const preHire = personalAssistantPanelView({ state: 'needs_hire' });
  assert.equal(preHire.visible, false);
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
