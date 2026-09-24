import test from 'node:test';
import assert from 'node:assert/strict';
import { reviewedInterviewRows } from './personal-assistant-interview.js';

const questions = [
  { id: 'priority', category: 'projects', destination: 'personal_hq', text: '' },
  { id: 'communication', category: 'how_you_work', destination: 'personal_hq', text: '' },
  { id: 'person_or_routine', category: 'routines', destination: 'personal_hq', text: '' }
];

test('skipped answers never become canonical rows', () => {
  assert.deepEqual(reviewedInterviewRows(questions), []);
});

test('final reviewed rows show exact category, HQ destination and profile compare snapshot', () => {
  const answers = questions.map(answer => ({ ...answer }));
  answers[0].text = 'Finish the portfolio';
  answers[1].text = 'concise';
  answers[1].destination = 'profile';
  answers[1].preference = 'response_style';
  const snapshot = {
    updated_at: '2026-09-22T12:00:00Z',
    preferences: { response_style: 'brief' }
  };
  assert.deepEqual(reviewedInterviewRows(answers, snapshot), [
    {
      row_id: 'priority',
      destination: 'personal_hq',
      category: 'projects',
      text: 'Finish the portfolio'
    },
    {
      row_id: 'communication',
      destination: 'profile',
      category: 'how_you_work',
      text: 'concise',
      preference: 'response_style',
      expected_profile_value: 'brief',
      expected_profile_updated_at: '2026-09-22T12:00:00Z'
    }
  ]);
});

test('invalid and overlong final answers cannot be silently trimmed', () => {
  for (const text of [' leading', 'first\nsecond', 'x'.repeat(501), ' ']) {
    const answers = questions.map(answer => ({ ...answer }));
    answers[0].text = text;
    assert.throws(() => reviewedInterviewRows(answers));
  }
  const answers = questions.map(answer => ({ ...answer }));
  answers[1].destination = 'profile';
  answers[1].preference = 'response_style';
  answers[1].text = 'concise';
  assert.throws(() => reviewedInterviewRows(answers), /global profile/i);
});
