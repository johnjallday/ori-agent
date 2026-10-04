import test from 'node:test';
import assert from 'node:assert/strict';

import {
  MEMORY_CATEGORIES,
  factCandidate,
  factReviewState,
  findRememberedFact,
  memoryOutcome,
  memoryRequest,
  missingValueHint
} from './personal-assistant-memory.js';
import { MAX_FACT_BYTES, reviewTextValid } from './personal-hq-fact.js';

test('only a short statement the user wrote is offered as a starting fact', () => {
  assert.equal(factCandidate("Mina's birthday is 3 March.", 'user'), "Mina's birthday is 3 March");
  assert.equal(factCandidate('  I park   on level 2 ', 'user'), 'I park on level 2');
  assert.equal(factCandidate('미나 생일은 3월 3일', 'user'), '미나 생일은 3월 3일');
});

test('a draft, a request, or a question is never offered as a fact', () => {
  const greeting = 'Happy birthday, Mina! Wishing you a wonderful day filled with joy.';
  // Whatever the assistant wrote — a greeting, an example date — starts empty.
  assert.equal(factCandidate(greeting, 'assistant'), '');
  assert.equal(factCandidate('Mina turns 30 on 3 March.', 'assistant'), '');
  // The user's requests and questions are not facts either.
  for (const text of [
    'Write a short birthday greeting for my friend Mina.',
    'make it warmer',
    'give it to me in Korean',
    'save this draft and put it in my todo list',
    'remember this',
    'When is her birthday?',
    'please translate it',
    ''
  ]) {
    assert.equal(factCandidate(text, 'user'), '', text);
  }
  // Multi-line or long text is not a single fact.
  assert.equal(factCandidate('line one\nline two', 'user'), '');
  assert.equal(factCandidate('가'.repeat(80), 'user'), '');
});

test('a birthday with no date gets a hint, and Ori never supplies the date', () => {
  const hint = missingValueHint('Write a short birthday greeting for my friend Mina.');
  assert.match(hint, /does not say the date/);
  assert.match(hint, /will not guess/);
  assert.match(missingValueHint('미나 생일 축하 인사'), /does not say the date/);
  // A message that already gives the date needs no hint.
  for (const dated of [
    "Mina's birthday is 3 March",
    "Mina's birthday is March 3rd",
    'her birthday is 03/03',
    '미나 생일은 3월 3일'
  ]) {
    assert.equal(missingValueHint(dated), '', dated);
  }
  // No birthday, no hint. And the hint text itself never contains a date.
  assert.equal(missingValueHint('I park on level 2'), '');
  assert.doesNotMatch(hint, /\d/);
});

test('the review enforces the 500-byte limit without cutting the text', () => {
  const ok = factReviewState("Mina's birthday is 3 March");
  assert.deepEqual(ok, { bytes: 26, limit: MAX_FACT_BYTES, valid: true, problem: '' });

  // Korean is three bytes a character: 166 fit, 167 do not.
  assert.equal(factReviewState('가'.repeat(166)).valid, true);
  const over = factReviewState('가'.repeat(167));
  assert.equal(over.bytes, 501);
  assert.equal(over.valid, false);
  assert.match(over.problem, /501 bytes/);
  assert.match(over.problem, /never cut for you/);

  assert.equal(factReviewState('').valid, false);
  assert.equal(factReviewState('   ').valid, false);
  assert.equal(factReviewState('two  spaces').valid, false);
  assert.equal(factReviewState(' padded').valid, false);
  assert.equal(factReviewState('line\nbreak').valid, false);
  assert.equal(factReviewState('hidden\u0000byte').valid, false);
  // The shared rule is the one the remembered-facts page uses.
  assert.equal(reviewTextValid('가'.repeat(166)), true);
  assert.equal(reviewTextValid('가'.repeat(167)), false);
});

test('a retry of the same wording keeps its key; changed wording is a new action', () => {
  let made = 0;
  const generate = () => `id-${++made}`;
  const first = memoryRequest(null, 'fact', 'people', 7, generate);
  assert.deepEqual(first, { id: 'id-1', text: 'fact', category: 'people', stateVersion: 7 });
  // Same action: same key, nothing regenerated.
  assert.equal(memoryRequest(first, 'fact', 'people', 7, generate), first);
  assert.equal(made, 1);
  // Any change is a new action with a new key.
  assert.equal(memoryRequest(first, 'fact edited', 'people', 7, generate).id, 'id-2');
  assert.equal(memoryRequest(first, 'fact', 'routines', 7, generate).id, 'id-3');
  assert.equal(memoryRequest(first, 'fact', 'people', 8, generate).id, 'id-4');
});

const approved = { id: 'k-1', state: 'approved', text: 'fact', category: 'people' };

test('only an approved, current, exact fact counts as remembered', () => {
  assert.equal(findRememberedFact([approved], 'fact'), approved);
  assert.equal(findRememberedFact([{ ...approved, state: 'forgotten' }], 'fact'), null);
  assert.equal(findRememberedFact([{ ...approved, state: 'candidate' }], 'fact'), null);
  assert.equal(findRememberedFact([{ ...approved, review_unavailable: true }], 'fact'), null);
  assert.equal(findRememberedFact([approved], 'Fact'), null);
  assert.equal(findRememberedFact(null, 'fact'), null);
});

test('a save is reported as saved only when it is verified', () => {
  assert.deepEqual(memoryOutcome(200, approved, null, 'fact', 7), {
    kind: 'saved',
    item: approved
  });
  // A 200 with no item is not a verified save.
  assert.equal(memoryOutcome(200, null, { ok: false }, 'fact', 7).kind, 'unknown');
});

test('a save that could not be confirmed is checked against the canonical list', () => {
  // Saved, but the confirmation read failed on the server: the list shows it.
  const savedUnverified = memoryOutcome(
    503,
    null,
    { ok: true, items: [approved], stateVersion: 7 },
    'fact',
    7
  );
  assert.deepEqual(savedUnverified, { kind: 'saved', item: approved });

  // No response and no readable list: say so, and offer the same save again.
  const lost = memoryOutcome(0, null, { ok: false }, 'fact', 7);
  assert.equal(lost.kind, 'unknown');
  assert.match(lost.message, /Could not confirm whether this was remembered/);
  assert.match(lost.message, /never applied twice/);
  assert.doesNotMatch(lost.message, /Nothing was remembered/);

  // Not in the list after a server error: still unknown, not "failed".
  assert.equal(
    memoryOutcome(503, null, { ok: true, items: [], stateVersion: 7 }, 'fact', 7).kind,
    'unknown'
  );
});

test('a refused save says why and keeps the wording', () => {
  const invalid = memoryOutcome(
    400,
    null,
    { ok: true, items: [], stateVersion: 7 },
    'sk-secret',
    7
  );
  assert.equal(invalid.kind, 'invalid');
  assert.match(invalid.message, /Vault/);
  assert.match(invalid.message, /Nothing was remembered/);

  const stale = memoryOutcome(409, null, { ok: true, items: [], stateVersion: 8 }, 'fact', 7);
  assert.equal(stale.kind, 'stale');
  assert.match(stale.message, /your wording is still here/);

  // Same state, refused: Personal HQ memory's own reason is shown as given.
  const full = memoryOutcome(
    409,
    null,
    { ok: true, items: [], stateVersion: 7 },
    'fact',
    7,
    'The review queue is full; resolve existing items before adding more'
  );
  assert.equal(full.kind, 'refused');
  assert.equal(
    full.message,
    'The review queue is full; resolve existing items before adding more. Nothing was remembered; your wording is still here.'
  );
  const unexplained = memoryOutcome(409, null, { ok: true, items: [], stateVersion: 7 }, 'fact', 7);
  assert.equal(unexplained.kind, 'refused');
  assert.match(unexplained.message, /needs attention first\. Nothing was remembered/);

  // Refused, and the list shows the exact fact: it was already remembered.
  const already = memoryOutcome(
    409,
    null,
    { ok: true, items: [approved], stateVersion: 7 },
    'fact',
    7
  );
  assert.deepEqual(already, { kind: 'existing', item: approved });
});

test('categories are the existing reviewed-memory categories, with People first', () => {
  assert.deepEqual(
    MEMORY_CATEGORIES.map(item => item.id),
    ['people', 'how_you_work', 'projects', 'routines']
  );
});
