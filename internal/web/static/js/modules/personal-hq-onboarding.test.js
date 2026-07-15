import { test } from 'node:test';
import assert from 'node:assert/strict';
import { resolveGuidedMode, resumeCopy, wantsGuidedTakeover } from './personal-hq-onboarding.js';

function status(overrides) {
  return { workspace_id: '', valid: false, hq_onboarding_state: 'unseen', ...overrides };
}

test('resolveGuidedMode: no status at all resolves to none (degrade safely)', () => {
  assert.equal(resolveGuidedMode(null), 'none');
  assert.equal(resolveGuidedMode(undefined), 'none');
});

test('resolveGuidedMode: brand-new profile (unseen, no designation) is guided', () => {
  assert.equal(resolveGuidedMode(status({ hq_onboarding_state: 'unseen' })), 'guided');
});

test('resolveGuidedMode: a valid designated HQ is none, regardless of onboarding state', () => {
  assert.equal(resolveGuidedMode(status({ workspace_id: 'ws-1', valid: true, hq_onboarding_state: 'unseen' })), 'none');
  assert.equal(resolveGuidedMode(status({ workspace_id: 'ws-1', valid: true, hq_onboarding_state: 'completed' })), 'none');
});

test('resolveGuidedMode: a stale designation (workspace_id set, not valid) is repair even when unseen', () => {
  // Defensive precedence: a broken reference must never re-show the
  // full-screen guided takeover as if nothing had ever happened.
  assert.equal(resolveGuidedMode(status({ workspace_id: 'ws-1', valid: false, hq_onboarding_state: 'unseen' })), 'repair');
});

test('resolveGuidedMode: skipped with no designation is resume, not guided', () => {
  assert.equal(resolveGuidedMode(status({ hq_onboarding_state: 'skipped' })), 'resume');
});

test('resolveGuidedMode: in_progress with no designation is resume', () => {
  assert.equal(resolveGuidedMode(status({ hq_onboarding_state: 'in_progress' })), 'resume');
});

test('resolveGuidedMode: completed onboarding but no current designation (e.g. cleared) is resume', () => {
  assert.equal(resolveGuidedMode(status({ hq_onboarding_state: 'completed', valid: false, workspace_id: '' })), 'resume');
});

test('wantsGuidedTakeover: only an unseen profile WITH explicit intent triggers the takeover', () => {
  // The home-first flow: a brand-new profile browsing to the launcher must
  // see the normal launcher; the full-screen takeover appears only after the
  // user clicks "Start mission" (which arrives with ?hq_onboarding=1).
  assert.equal(wantsGuidedTakeover('unseen', true), true);
  assert.equal(wantsGuidedTakeover('unseen', false), false);
  assert.equal(wantsGuidedTakeover('seen', true), false);
  assert.equal(wantsGuidedTakeover('seen', false), false);
  assert.equal(wantsGuidedTakeover(null, true), false);
});

test('resumeCopy: repair mode offers Build, Choose, and Clear', () => {
  const copy = resumeCopy('repair');
  assert.equal(copy.showBuild, true);
  assert.equal(copy.showChoose, true);
  assert.equal(copy.showClear, true);
  assert.match(copy.text, /needs attention/i);
});

test('resumeCopy: resume mode offers Build and Choose but not Clear (nothing to clear)', () => {
  const copy = resumeCopy('resume');
  assert.equal(copy.showBuild, true);
  assert.equal(copy.showChoose, true);
  assert.equal(copy.showClear, false);
});
