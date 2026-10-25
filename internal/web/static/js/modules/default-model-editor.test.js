import test from 'node:test';
import assert from 'node:assert/strict';
import { refreshLinkedModelNote, sharedModelSaveFeedback } from './personal-assistant-model.js';

const selection = model => ({
  state: 'configured',
  source: 'default',
  provider: 'claude_code',
  model
});

test('linked note reports conversation without changing saved form values', async () => {
  const note = { textContent: '', hidden: true, value: 'saved-profile' };
  await refreshLinkedModelNote(note, true, { read: async () => selection('conversation-alias') });
  assert.equal(
    note.textContent,
    'Conversation uses Default model: claude_code / conversation-alias.'
  );
  assert.equal(note.value, 'saved-profile');
  await refreshLinkedModelNote(note, true, { read: async () => ({ state: 'unconfigured' }) });
  assert.equal(note.textContent, 'Conversation uses Default model: not configured.');
  assert.equal(note.value, 'saved-profile');
});

test('independent editors neither load nor advertise the shared effect', async () => {
  const note = { textContent: 'old linked note' };
  await refreshLinkedModelNote(note, false, { read: () => assert.fail('must not read default') });
  assert.equal(note.hidden, true);
  assert.equal(note.textContent, '');
  const feedback = await sharedModelSaveFeedback(null, {
    read: () => assert.fail('must not read default'),
    emit: () => assert.fail('must not emit shared write')
  });
  assert.equal(feedback.type, 'success');
  assert.doesNotMatch(feedback.message, /Default model/);
});

test('a late linked note cannot relabel an independent editor', async () => {
  let resolve;
  const note = {};
  const pending = refreshLinkedModelNote(note, true, {
    read: () =>
      new Promise(done => {
        resolve = done;
      })
  });
  await refreshLinkedModelNote(note, false);
  resolve(selection('old'));
  await pending;
  assert.equal(note.textContent, '');
  assert.equal(note.hidden, true);
});

test('shared-save feedback rereads rather than declaring the attempted value current', async () => {
  const emitted = [];
  const feedback = await sharedModelSaveFeedback(
    { mirrored: true, provider: 'old-provider', model: 'attempted' },
    {
      read: async () => selection('authoritative'),
      emit: data => emitted.push(data)
    }
  );
  assert.equal(feedback.type, 'success');
  assert.match(feedback.message, /claude_code \/ authoritative/);
  assert.doesNotMatch(feedback.message, /attempted/);
  assert.equal(emitted[0].model, 'authoritative');
});

test('rejected mirror distinguishes saved profile, rejected default and effective readback', async () => {
  const feedback = await sharedModelSaveFeedback(
    { mirrored: false, reason: 'Provider unavailable', model: 'attempted' },
    {
      read: async () => selection('unchanged'),
      emit: () => assert.fail('no successful shared write')
    }
  );
  assert.equal(feedback.type, 'error');
  assert.match(
    feedback.message,
    /Agent saved, but.*not confirmed.*Provider unavailable.*unchanged/
  );
  assert.doesNotMatch(feedback.message, /attempted/);
});

test('failed persistence may change in-memory selection; feedback does not promise rollback', async () => {
  const feedback = await sharedModelSaveFeedback(
    { mirrored: false, reason: 'Could not save settings' },
    {
      read: async () => selection('in-memory-change')
    }
  );
  assert.match(feedback.message, /not confirmed.*in-memory-change/);
  assert.doesNotMatch(feedback.message, /left unchanged|saved successfully/);
});

test('read failure is unknown, not a fabricated successful effective model', async () => {
  const read = async () => {
    throw new Error('offline');
  };
  const feedback = await sharedModelSaveFeedback({ mirrored: true, model: 'old' }, { read });
  assert.equal(feedback.type, 'error');
  assert.match(feedback.message, /Agent saved.*could not be read/);
  const note = {};
  await refreshLinkedModelNote(note, true, { read });
  assert.equal(note.textContent, 'Conversation uses Default model: unknown.');
});
