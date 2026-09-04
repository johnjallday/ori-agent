import test from 'node:test';
import assert from 'node:assert/strict';

await import('./personal-assistant-continuity.js');
const continuity = globalThis.PersonalAssistantContinuity;

test('working-agreement conflict requires a deliberate reapply with server values', () => {
  const current = { state_version: 8, display_name: 'Atlas', mandate: 'Current mandate' };
  const view = continuity.conflictView({ error: 'Changed elsewhere.', current });
  assert.equal(view.requiresReapply, true);
  assert.equal(view.current, current);
  assert.match(view.message, /Changed elsewhere/);
});

test('rename and rhythm list input normalize without inventing values', () => {
  assert.deepEqual(continuity.splitList(' plan_my_day,  help_with_email ,, '), [
    'plan_my_day',
    'help_with_email'
  ]);
});

test('capability copy states reads, proposals, confirmation, and mapped writes', () => {
  const lines = continuity.capabilityCopy({
    can_read: 'Existing events.',
    can_propose: 'Meeting preparation.',
    requires_confirmation: 'External changes stay gated.',
    mapped_write: false
  });
  assert.deepEqual(lines, [
    'Can read: Existing events.',
    'Can propose: Meeting preparation.',
    'Confirmation: External changes stay gated.',
    'Mapped write: No.'
  ]);
});

test('unavailable capability copy never claims a healthy connection', () => {
  const lines = continuity.capabilityCopy({ status: 'unavailable' });
  assert.match(lines.join(' '), /Nothing until this source is configured/);
  assert.doesNotMatch(lines.join(' '), /connected|available/i);
});

test('working agreement remembers the visible assistant view without targeting a hidden link', () => {
  const assistantTrigger = {
    closest: selector => (selector === '#personalAssistantPanel' ? {} : null)
  };
  assert.deepEqual(
    continuity.assistantReturnView(assistantTrigger, {
      _state: { open: true, activeView: 'ask' }
    }),
    { fromAssistant: true, view: 'ask' }
  );
  assert.deepEqual(
    continuity.assistantReturnView(assistantTrigger, {
      _state: { open: false, activeView: 'ask' }
    }),
    { fromAssistant: false, view: 'today' }
  );
  assert.deepEqual(continuity.assistantReturnView(null, null), {
    fromAssistant: false,
    view: 'today'
  });
});
