import test from 'node:test';
import assert from 'node:assert/strict';
import {
  ANSWER_COUNTER_THRESHOLD,
  DRAFT_KEY,
  alternateSuggestion,
  answerCounterText,
  applySuggestion,
  clearInterviewDraft,
  cleanSuggestion,
  createInterviewWizardState,
  destinationLabel,
  hasInterviewDraft,
  interviewAnswerError,
  loadInterviewDraft,
  reviewedInterviewRows,
  saveInterviewDraft,
  suggestionCaptionVisible,
  usePendingSuggestion
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

test('a draft restores only a suggestion whose text is a string, and only on question 1', () => {
  const restored = createInterviewWizardState(questions, {
    answers: [
      {
        id: 'priority',
        text: 'kept',
        suggestion: { text: { toString: () => 'injected' }, folder: 'Thesis' },
        pending: {
          text: 'Thesis, a LaTeX manuscript',
          folder: 42,
          path: '/Users/me/Documents/Thesis',
          alternates: [
            {
              text: 'website, a Node.js package',
              folder: 'website',
              alternates: [{ text: 'deep' }]
            },
            { text: 7 },
            null,
            { text: 'b' },
            { text: 'c' },
            { text: 'd' }
          ]
        }
      },
      { id: 'communication', text: 'brief', suggestion: { text: 'not a folder question' } }
    ]
  });
  const priority = restored.answer('priority');
  assert.equal(priority.suggestion, null);
  assert.deepEqual(priority.pending, {
    text: 'Thesis, a LaTeX manuscript',
    folder: '',
    alternates: [
      { text: 'website, a Node.js package', folder: 'website' },
      { text: 'b', folder: '' },
      { text: 'c', folder: '' }
    ]
  });
  assert.equal('suggestion' in restored.answer('communication'), false);

  for (const bad of [null, undefined, 'text', 7, {}, { text: '' }, { text: null }, []]) {
    assert.equal(cleanSuggestion(bad), null, JSON.stringify(bad));
  }
});

const thesis = {
  text: 'Thesis, a LaTeX manuscript',
  folder: 'Thesis',
  alternates: [
    { text: 'website, a Node.js package', folder: 'website' },
    { text: 'Album', folder: 'Album' }
  ]
};

test('applySuggestion fills an empty box and never overwrites typed text', () => {
  const empty = createInterviewWizardState(questions).answer('priority');
  const filled = applySuggestion(empty, thesis);
  assert.equal(filled.text, thesis.text);
  assert.deepEqual(filled.suggestion, thesis);
  assert.equal(filled.pending, null);
  assert.equal(empty.text, '', 'the answer it was given is not changed');

  // Only spaces is still an empty box.
  assert.equal(applySuggestion({ ...empty, text: '   ' }, thesis).text, thesis.text);

  // An unedited earlier suggestion is replaced by a newer one.
  const album = { text: 'Album', folder: 'Album' };
  const replaced = applySuggestion(filled, album);
  assert.equal(replaced.text, 'Album');
  assert.deepEqual(replaced.suggestion, album);
  assert.equal(replaced.pending, null);

  // Typed text stays; the suggestion waits as pending.
  const typed = { ...empty, text: 'Ship the portfolio' };
  const kept = applySuggestion(typed, thesis);
  assert.equal(kept.text, 'Ship the portfolio');
  assert.equal(kept.suggestion, null);
  assert.deepEqual(kept.pending, thesis);

  // An edited suggestion is typed text too.
  const edited = { ...filled, text: 'Thesis, due in March' };
  const afterEdit = applySuggestion(edited, album);
  assert.equal(afterEdit.text, 'Thesis, due in March');
  assert.deepEqual(afterEdit.pending, album);

  // Something that is not a suggestion changes nothing.
  assert.deepEqual(applySuggestion(typed, { text: 5 }), typed);
  assert.deepEqual(applySuggestion(typed, null), typed);
});

test('usePendingSuggestion applies the waiting suggestion on request', () => {
  const typed = { ...createInterviewWizardState(questions).answer('priority'), text: 'Mine' };
  const waiting = applySuggestion(typed, thesis);
  const used = usePendingSuggestion(waiting);
  assert.equal(used.text, thesis.text);
  assert.deepEqual(used.suggestion, thesis);
  assert.equal(used.pending, null);
  assert.deepEqual(usePendingSuggestion(typed), typed, 'nothing pending, nothing changes');
});

test('the caption shows only while the text is exactly the stored suggestion', () => {
  const answer = applySuggestion(createInterviewWizardState(questions).answer('priority'), thesis);
  assert.equal(suggestionCaptionVisible(answer), true);
  assert.equal(suggestionCaptionVisible({ ...answer, text: `${thesis.text} ` }), false);
  assert.equal(suggestionCaptionVisible({ ...answer, text: '' }), false);
  assert.equal(suggestionCaptionVisible({ ...answer, suggestion: null }), false);
  assert.equal(suggestionCaptionVisible({ text: 'typed', pending: thesis }), false);
  assert.equal(suggestionCaptionVisible({ text: '' }), false);
  assert.equal(suggestionCaptionVisible(undefined), false);
});

test('choosing an alternate keeps the other projects on offer', () => {
  const chosen = alternateSuggestion(thesis, 0);
  assert.deepEqual(chosen, {
    text: 'website, a Node.js package',
    folder: 'website',
    alternates: [
      { text: 'Thesis, a LaTeX manuscript', folder: 'Thesis' },
      { text: 'Album', folder: 'Album' }
    ]
  });
  // It is applied through applySuggestion: the box holds the unedited suggestion.
  const answer = applySuggestion(createInterviewWizardState(questions).answer('priority'), thesis);
  const switched = applySuggestion(answer, chosen);
  assert.equal(switched.text, 'website, a Node.js package');
  assert.equal(suggestionCaptionVisible(switched), true);
  // An index that is not there leaves the suggestion as it was.
  assert.deepEqual(alternateSuggestion(thesis, 9), thesis);
});

test('a suggestion and its caption survive close and resume through the draft', () => {
  const storage = memoryStorage();
  const state = createInterviewWizardState(questions);
  state.setAnswer('priority', applySuggestion(state.answer('priority'), thesis));
  saveInterviewDraft(storage, state.toDraft());

  const resumed = createInterviewWizardState(questions, loadInterviewDraft(storage));
  assert.equal(resumed.answer('priority').text, thesis.text);
  assert.deepEqual(resumed.answer('priority').suggestion, thesis);
  assert.equal(suggestionCaptionVisible(resumed.answer('priority')), true);

  // A waiting suggestion is kept beside the typed text.
  const typing = createInterviewWizardState(questions);
  typing.setAnswer('priority', { text: 'Mine' });
  typing.setAnswer('priority', applySuggestion(typing.answer('priority'), thesis));
  saveInterviewDraft(storage, typing.toDraft());
  const again = createInterviewWizardState(questions, loadInterviewDraft(storage));
  assert.equal(again.answer('priority').text, 'Mine');
  assert.deepEqual(again.answer('priority').pending, thesis);
  assert.equal(suggestionCaptionVisible(again.answer('priority')), false);

  // What a folder proposed is never part of what is saved.
  const rows = reviewedInterviewRows(resumed.toAnswers());
  assert.deepEqual(rows, [
    { row_id: 'priority', destination: 'personal_hq', category: 'projects', text: thesis.text }
  ]);
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
