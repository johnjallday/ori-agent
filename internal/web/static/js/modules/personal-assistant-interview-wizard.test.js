import test from 'node:test';
import assert from 'node:assert/strict';
import {
  ANSWER_COUNTER_THRESHOLD,
  DRAFT_KEY,
  answerCounterText,
  clearInterviewDraft,
  createInterviewWizardState,
  destinationLabel,
  hasInterviewDraft,
  interviewAnswerError,
  loadInterviewDraft,
  reviewedInterviewRows,
  saveInterviewDraft
} from './personal-assistant-interview-wizard.js';

function memoryStorage() {
  const data = new Map();
  return {
    getItem: key => (data.has(key) ? data.get(key) : null),
    setItem: (key, value) => data.set(key, String(value)),
    removeItem: key => data.delete(key)
  };
}

const throwingStorage = {
  getItem() {
    throw new Error('blocked');
  },
  setItem() {
    throw new Error('quota');
  },
  removeItem() {
    throw new Error('blocked');
  }
};

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

test('interviewAnswerError accepts skipped and valid answers and explains each failure class', () => {
  assert.equal(interviewAnswerError(''), null);
  assert.equal(interviewAnswerError('Finish the portfolio'), null);
  assert.match(interviewAnswerError('é'.repeat(251)), /too long/i);
  assert.match(interviewAnswerError('x'.repeat(501)), /too long/i);
  for (const text of ['double  space', ' leading', 'trailing ', 'two\nlines', ' ']) {
    assert.match(interviewAnswerError(text), /one line/i, JSON.stringify(text));
  }
  assert.match(interviewAnswerError('bell\u0007'), /cannot save/i);
  assert.match(interviewAnswerError('hidden‮flip'), /cannot save/i);
});

test('interviewAnswerError never changes what it was given', () => {
  const text = 'double  space';
  interviewAnswerError(text);
  assert.equal(text, 'double  space');
});

test('the byte counter appears only past the threshold and counts bytes', () => {
  assert.equal(answerCounterText(''), '');
  assert.equal(answerCounterText('x'.repeat(ANSWER_COUNTER_THRESHOLD)), '');
  assert.equal(answerCounterText('x'.repeat(ANSWER_COUNTER_THRESHOLD + 1)), '401 / 500');
  assert.equal(answerCounterText('é'.repeat(201)), '402 / 500');
  assert.ok(!/utf/i.test(answerCounterText('x'.repeat(450))));
});

test('destination labels use plain words', () => {
  const profile = { updated_at: 'now', preferences: { response_style: 'brief' } };
  assert.equal(
    destinationLabel({ destination: 'personal_hq', category: 'projects' }),
    'Personal HQ · Projects'
  );
  assert.equal(
    destinationLabel({ destination: 'personal_hq', category: 'how_you_work' }),
    'Personal HQ · How you work'
  );
  assert.equal(
    destinationLabel({ destination: 'personal_hq', category: 'routines' }),
    'Personal HQ · Routines'
  );
  assert.equal(
    destinationLabel({ destination: 'personal_hq', category: 'people' }),
    'Personal HQ · People'
  );
  assert.equal(
    destinationLabel({ destination: 'profile', preference: 'response_style' }, profile),
    'Your global profile · Response style (currently: brief)'
  );
  assert.equal(
    destinationLabel({ destination: 'profile', preference: 'units' }, profile),
    'Your global profile · Units (currently: not set)'
  );
});

test('drafts round-trip through storage and restore into the state', () => {
  const storage = memoryStorage();
  const state = createInterviewWizardState(questions);
  state.setAnswer('priority', { text: 'Ship it' });
  state.setAnswer('communication', {
    text: 'metric please',
    destination: 'profile',
    preference: 'units'
  });
  state.next();
  assert.equal(saveInterviewDraft(storage, state.toDraft()), true);
  assert.ok(storage.getItem(DRAFT_KEY));
  assert.equal(hasInterviewDraft(storage), true);

  const restored = createInterviewWizardState(questions, loadInterviewDraft(storage));
  assert.equal(restored.index, 1);
  assert.equal(restored.answer('priority').text, 'Ship it');
  assert.equal(restored.answer('communication').destination, 'profile');
  assert.equal(restored.answer('communication').preference, 'units');

  clearInterviewDraft(storage);
  assert.equal(loadInterviewDraft(storage), null);
  assert.equal(hasInterviewDraft(storage), false);
});

test('a draft cannot inject unknown questions, destinations or out-of-range steps', () => {
  const restored = createInterviewWizardState(questions, {
    stepIndex: 99,
    answers: [
      { id: 'nope', text: 'ignored' },
      { id: 'priority', text: 'kept', destination: 'profile', category: 'secrets' },
      { id: 'communication', destination: 'elsewhere', preference: 'bogus' }
    ]
  });
  assert.equal(restored.index, restored.reviewIndex);
  assert.equal(restored.answer('priority').text, 'kept');
  assert.equal(restored.answer('priority').destination, 'personal_hq');
  assert.equal(restored.answer('priority').category, 'projects');
  assert.equal(restored.answer('communication').destination, 'personal_hq');
  assert.equal(restored.answer('communication').preference, 'response_style');
});

test('the draft helper survives unavailable or throwing storage', () => {
  const draft = { answers: [{ id: 'priority', text: 'x' }], stepIndex: 0 };
  assert.equal(saveInterviewDraft(throwingStorage, draft), false);
  assert.equal(loadInterviewDraft(throwingStorage), null);
  assert.equal(hasInterviewDraft(throwingStorage), false);
  assert.doesNotThrow(() => clearInterviewDraft(throwingStorage));
  assert.equal(loadInterviewDraft(null), null);
  assert.doesNotThrow(() => clearInterviewDraft(null));
  const corrupt = memoryStorage();
  corrupt.setItem(DRAFT_KEY, '{not json');
  assert.equal(loadInterviewDraft(corrupt), null);
});
