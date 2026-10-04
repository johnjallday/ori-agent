import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

import {
  EXPLORE_FOLDER_URL,
  assistantCheckInLine,
  assistantChipView,
  assistantMoreLinks,
  assistantOpenFocusTarget,
  assistantPanelShouldCloseOnKey,
  boundedAssistantHandoff,
  canSubmitAssistantWork,
  personalAssistantPanelView,
  restoreAssistantPanelFocus,
  restoredDraft,
  safeTodayRoute
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

  const repair = personalAssistantPanelView({ state: 'repair_needed', display_name: 'Nova' });
  assert.equal(repair.repair, true);
  assert.equal(repair.available, false);
  assert.equal(repair.visible, true, 'repair guidance stays reachable without enabling work');
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
  // A reply still in flight blocks a second turn: it has no conversation to join yet.
  assert.equal(
    canSubmitAssistantWork({ available: true, pending: false, busy: true, text: 'help' }),
    false
  );
  assert.equal(
    canSubmitAssistantWork({ available: true, pending: false, busy: false, text: 'help' }),
    true
  );
});

test('an unsent message returns to the composer without overwriting newer typing', () => {
  assert.equal(restoredDraft('', 'make it warmer'), 'make it warmer');
  assert.equal(restoredDraft('   ', 'make it warmer'), 'make it warmer');
  assert.equal(restoredDraft('typed since', 'make it warmer'), 'typed since');
  assert.equal(restoredDraft('', '생일 축하해 🎂'), '생일 축하해 🎂');
  assert.equal(restoredDraft(null, null), '');
});

test('opening the drawer focuses the composer whenever the assistant can accept work', () => {
  for (const state of ['active', 'paused']) {
    assert.equal(assistantOpenFocusTarget(personalAssistantPanelView({ state })), 'composer');
  }
  // Not hired, HQ not built, repair needed, unknown: the composer is disabled,
  // so focus goes to the first control in the drawer instead.
  for (const state of [
    'needs_hire',
    'hiring',
    'needs_hq',
    'provisioning_hq',
    'repair_needed',
    'unavailable'
  ]) {
    assert.equal(
      assistantOpenFocusTarget(personalAssistantPanelView({ state })),
      'first-control',
      state
    );
  }
  assert.equal(assistantOpenFocusTarget(null), 'first-control');
});

test('"Explore a folder" is offered when the assistant can accept work, and waits its turn', () => {
  // Active or paused already means a Personal HQ exists.
  for (const state of ['active', 'paused']) {
    const { available } = personalAssistantPanelView({ state });
    assert.deepEqual(assistantChipView({ available, folderBusy: false }), {
      visible: true,
      disabled: false
    });
    // A folder is being chosen or explored: the chip stays, and cannot be pressed.
    assert.deepEqual(assistantChipView({ available, folderBusy: true }), {
      visible: true,
      disabled: true
    });
  }
  for (const state of ['needs_hire', 'needs_hq', 'provisioning_hq', 'repair_needed']) {
    const { available } = personalAssistantPanelView({ state });
    assert.equal(assistantChipView({ available }).visible, false, state);
  }
  assert.equal(assistantChipView().visible, false);
  // Off Home the chip goes to Home's flow, which the drawer there starts.
  assert.equal(EXPLORE_FOLDER_URL, '/?panel=today&folder=show');
});

test('the chip sits directly above the composer, on every page', () => {
  const drawer = readFileSync(
    new URL('../../../templates/components/ori-guide.tmpl', import.meta.url),
    'utf8'
  );
  const chips = drawer.indexOf('id="personalAssistantChips"');
  const form = drawer.indexOf('id="personalAssistantForm"');
  assert.ok(chips > 0 && chips < form);
  // Nothing else is between them, and it is outside the scrolling region.
  assert.ok(drawer.indexOf('id="personalAssistantPanelStatus"') < chips);
  assert.ok(drawer.indexOf('id="personalAssistantThread"') < chips);
  assert.equal((drawer.match(/class="personal-assistant-panel__chip"/g) || []).length, 1);
  assert.match(drawer, /id="personalAssistantFolderChip"[\s\S]{0,200}Explore a folder/);
  // Not inside a part of the drawer that only some pages render: the last
  // page condition before it has already ended.
  const before = drawer.slice(0, chips);
  const lastCondition = before.lastIndexOf('.CurrentPage "index"}}');
  assert.ok(lastCondition > 0);
  assert.match(before.slice(lastCondition), /\{\{end\}\}/);
});

test('the header line says when the next check-in is, in the wording Today used', () => {
  assert.deepEqual(
    assistantCheckInLine({ state: 'active', next_check_in: '2026-10-08T08:00:00Z' }),
    { text: 'Next check-in · ', time: '2026-10-08T08:00:00Z' }
  );
  assert.deepEqual(assistantCheckInLine({ state: 'active' }), {
    text: 'No check-in scheduled',
    time: ''
  });
  // Paused wins over a stored time: nothing is coming while paused.
  assert.deepEqual(
    assistantCheckInLine({ state: 'paused', next_check_in: '2026-10-08T08:00:00Z' }),
    { text: 'Check-ins paused', time: '' }
  );
  assert.deepEqual(assistantCheckInLine({ state: 'active', next_check_in: 'not a date' }), {
    text: 'Next check-in unavailable',
    time: ''
  });
  for (const state of ['partial', 'model_unavailable', 'healthy_empty']) {
    assert.equal(assistantCheckInLine({ state }).text, 'No check-in scheduled', state);
  }
});

test('the header line is the plain role until a check-in can be known', () => {
  for (const today of [
    null,
    undefined,
    {},
    { state: 'loading' },
    { state: 'unavailable' },
    { state: 'needs_hire' },
    { state: 'needs_hq' },
    { state: 'repair_needed', next_check_in: '2026-10-08T08:00:00Z' }
  ]) {
    assert.deepEqual(assistantCheckInLine(today), { text: 'Personal Assistant', time: '' });
  }
});

test('the More menu shows only links Today validated, and the interview only when offered', () => {
  const links = assistantMoreLinks({
    links: {
      personal_hq: '/workspaces/my-hq',
      working_agreement: '/?personal-assistant=working-agreement',
      memory: '/workspaces/my-hq#memory',
      advanced: '/agents'
    },
    interview_status: 'offered'
  });
  assert.deepEqual(links, {
    personal_hq: '/workspaces/my-hq',
    working_agreement: '/?personal-assistant=working-agreement',
    memory: '/workspaces/my-hq#memory',
    advanced: '/agents',
    interview: true
  });

  const hostile = assistantMoreLinks({
    links: { personal_hq: 'https://evil.example/', memory: '//evil.example', advanced: '' },
    interview_status: 'completed'
  });
  assert.deepEqual(hostile, {
    personal_hq: '',
    working_agreement: '',
    memory: '',
    advanced: '',
    interview: false
  });
  for (const status of ['available', 'offered', 'deferred']) {
    assert.equal(assistantMoreLinks({ interview_status: status }).interview, true, status);
  }
  assert.equal(assistantMoreLinks(null).interview, false);
});

test('safeTodayRoute accepts same-origin paths and refuses everything else', () => {
  assert.equal(safeTodayRoute('/workspaces/my-hq?station=daily-brief'), true);
  assert.equal(safeTodayRoute('/'), true);
  for (const route of [
    '',
    'workspaces/my-hq',
    '//evil.example',
    'https://evil.example/',
    '/a/../b',
    '/a\\b',
    'javascript:alert(1)'
  ]) {
    assert.equal(safeTodayRoute(route), false, route);
  }
});

test('Escape closes only an open drawer and focus returns only to a connected trigger', () => {
  assert.equal(assistantPanelShouldCloseOnKey('Escape', true), true);
  assert.equal(assistantPanelShouldCloseOnKey('Escape', true, true), false);
  assert.equal(assistantPanelShouldCloseOnKey('Escape', false), false);
  assert.equal(assistantPanelShouldCloseOnKey('Enter', true), false);

  let focused = false;
  const trigger = { focus: () => (focused = true) };
  assert.equal(restoreAssistantPanelFocus(trigger, { contains: value => value === trigger }), true);
  assert.equal(focused, true);
  assert.equal(restoreAssistantPanelFocus(trigger, { contains: () => false }), false);
  assert.equal(restoreAssistantPanelFocus(null, { contains: () => true }), false);
});
