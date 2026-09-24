import assert from 'node:assert/strict';
import test from 'node:test';

import {
  dossierEmptyGuidance,
  groupReviewedKnowledge,
  reviewTextValid
} from './personal-hq-knowledge.js';

test('review ledger keeps pending and suspended separate from current canonical facts', () => {
  const { queue, approved } = groupReviewedKnowledge([
    { id: 'candidate', version: 1, state: 'candidate', text: 'Maybe use Obsidian' },
    { id: 'approved', version: 2, state: 'approved', text: 'I use Obsidian' },
    { id: 'stale', version: 3, state: 'needs_review', text: 'Old wording' },
    {
      id: 'invalid-source',
      version: 2,
      state: 'approved',
      review_unavailable: 'source_unavailable',
      text: ''
    },
    { id: 'forgotten', version: 2, state: 'forgotten', text: 'Should be erased' },
    { id: 'rejected', version: 2, state: 'rejected', text: 'Should be erased' },
    { id: 'missing-version', state: 'candidate', text: 'untrusted' }
  ]);
  assert.deepEqual(
    queue.map(item => item.id),
    ['candidate', 'stale']
  );
  assert.deepEqual(
    approved.map(item => item.id),
    ['approved']
  );
});

test('empty-section setup nudges stop when connected and never promise future learning', () => {
  const calendar = {
    status: 'not_configured',
    action_route: '/?create=1&blueprint=calendar-ops',
    action_label: 'Set up Calendar Ops'
  };
  const setup = dossierEmptyGuidance('routines', calendar);
  assert.equal(setup.route, calendar.action_route);
  assert.match(setup.message, /not automatic reviewed-memory learning/);
  const connected = dossierEmptyGuidance('routines', { ...calendar, status: 'available' });
  assert.equal(connected.route, undefined);
  assert.match(connected.message, /Calendar is connected/);
  assert.match(connected.message, /not enabled/);
  const healthy = dossierEmptyGuidance('routines', { ...calendar, status: 'healthy_empty' });
  assert.equal(healthy.route, undefined);
  assert.match(healthy.message, /Calendar is connected/);
  const revoked = dossierEmptyGuidance('routines', { ...calendar, status: 'revoked' });
  assert.equal(revoked.route, calendar.action_route);
  const unavailable = dossierEmptyGuidance('routines', { ...calendar, status: 'unavailable' });
  assert.equal(unavailable.route, '#addPersonalHQFact');
  assert.match(unavailable.message, /could not be verified/);
  const unsafe = dossierEmptyGuidance('routines', {
    ...calendar,
    action_route: '//outside.example'
  });
  assert.notEqual(unsafe.route, '//outside.example');
  const people = dossierEmptyGuidance('people', { ...calendar, status: 'available' });
  assert.equal(people.route, '#addPersonalHQFact');
  assert.doesNotMatch(people.message, /connect.*email/i);
});

test('a reviewed line has a strict UTF-8 byte limit and no blank, multiline, control or bidi text', () => {
  assert.equal(reviewTextValid('My priority is launch readiness'), true);
  assert.equal(reviewTextValid(''), false);
  assert.equal(reviewTextValid('  padded'), false);
  assert.equal(reviewTextValid('two  spaces'), false);
  assert.equal(reviewTextValid('non\u00a0breaking'), false);
  assert.equal(reviewTextValid('line\nsecond line'), false);
  assert.equal(reviewTextValid('é'.repeat(251)), false);
  assert.equal(reviewTextValid('é'.repeat(250)), true);
  assert.equal(reviewTextValid('hidden\u0000byte'), false);
  assert.equal(reviewTextValid('visual\u202espoof'), false);
});
