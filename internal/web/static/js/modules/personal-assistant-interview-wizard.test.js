import test from 'node:test';
import assert from 'node:assert/strict';
import {
  createInterviewWizardState,
  reviewedInterviewRows
} from './personal-assistant-interview-wizard.js';

const questions = [
  { id: 'priority', category: 'projects' },
  { id: 'communication', category: 'how_you_work' },
  { id: 'person_or_routine', category: 'routines' }
];

test('steps follow server order, then review', () => {
  const state = createInterviewWizardState(questions);
  const seen = [];
  while (!state.isReview()) {
    seen.push(state.index);
    state.next();
  }
  assert.deepEqual(seen, [0, 1, 2]);
  assert.equal(state.index, 3);
  state.next();
  assert.equal(state.index, 3, 'next at review is a no-op');
});

test('back at the first step is a no-op', () => {
  const state = createInterviewWizardState(questions);
  assert.equal(state.back(), 0);
  state.next();
  assert.equal(state.back(), 0);
});

test('answers survive moving between steps', () => {
  const state = createInterviewWizardState(questions);
  state.setAnswer('priority', { text: 'Ship the portfolio' });
  state.next();
  state.next();
  state.goTo('priority');
  assert.equal(state.index, 0);
  assert.equal(state.answer('priority').text, 'Ship the portfolio');
  state.goTo('review');
  assert.ok(state.isReview());
  assert.equal(state.toAnswers()[0].text, 'Ship the portfolio');
});

test('toAnswers output feeds reviewedInterviewRows', () => {
  const state = createInterviewWizardState(questions);
  assert.deepEqual(reviewedInterviewRows(state.toAnswers()), []);
  state.setAnswer('priority', { text: 'Finish the portfolio' });
  state.setAnswer('communication', {
    text: 'concise',
    destination: 'profile',
    preference: 'units'
  });
  state.setAnswer('person_or_routine', { text: 'Sam owns mixes', category: 'people' });
  const rows = reviewedInterviewRows(state.toAnswers(), {
    updated_at: '2026-09-22T12:00:00Z',
    preferences: { units: 'metric' }
  });
  assert.deepEqual(
    rows.map(row => [row.row_id, row.destination, row.category]),
    [
      ['priority', 'personal_hq', 'projects'],
      ['communication', 'profile', 'how_you_work'],
      ['person_or_routine', 'personal_hq', 'people']
    ]
  );
  assert.equal(rows[1].expected_profile_value, 'metric');
});
