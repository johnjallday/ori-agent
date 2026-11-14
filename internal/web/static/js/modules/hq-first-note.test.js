import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readNoteDraft, writeNoteDraft, saveFirstNote } from './hq-first-note.js';

const notes = { status: 'connected', source: { id: 'notes', origin: 'created_by_ori' } };
const readStatus = async () => notes;
const response = (status, body) => ({ ok: status < 400, status, json: async () => body });
const draft = () =>
  writeNoteDraft({ text: 'Garden reminder', requestId: 'one-draft', sourceId: 'notes' });

test('one note shares its request ID across an uncertain save and retry; editing cannot change the uncertain payload', async () => {
  draft();
  const requests = [];
  let attempt = 0;
  const fetcher = async (url, options) => {
    requests.push(JSON.parse(options.body));
    return attempt++ === 0
      ? response(500, { error: 'Temporarily unavailable' })
      : response(200, { capture: { note: 'Inbox/garden.md' } });
  };
  await assert.rejects(
    saveFirstNote('Garden reminder', { readStatus, fetcher }),
    /Temporarily unavailable/
  );
  assert.equal(readNoteDraft().pendingText, 'Garden reminder');
  const saved = await saveFirstNote('Changed after an uncertain response', { readStatus, fetcher });
  assert.deepEqual(requests, [
    { text: 'Garden reminder', request_id: 'one-draft' },
    { text: 'Garden reminder', request_id: 'one-draft' }
  ]);
  assert.deepEqual(saved, { text: 'Garden reminder', note: 'Inbox/garden.md' });
  assert.equal(readNoteDraft().text, '');
  assert.notEqual(readNoteDraft().requestId, 'one-draft');
});

test('network failures and malformed success receipts preserve uncertain draft identity', async () => {
  for (const fetcher of [
    async () => {
      throw Error('network');
    },
    async () => response(200, {})
  ]) {
    draft();
    await assert.rejects(
      saveFirstNote('Garden reminder', { readStatus, fetcher }),
      /could not confirm/
    );
    assert.equal(readNoteDraft().requestId, 'one-draft');
    assert.equal(readNoteDraft().pendingText, 'Garden reminder');
  }
});

test('known refusal keeps the text editable, and source changes never write to the replacement', async () => {
  draft();
  await assert.rejects(
    saveFirstNote('Garden reminder', {
      readStatus,
      fetcher: async () => response(400, { error: 'Please shorten this note' })
    }),
    /Please shorten/
  );
  assert.equal(readNoteDraft().text, 'Garden reminder');
  assert.equal(readNoteDraft().pendingText, undefined);
  for (const changed of [
    { ...notes, source: { ...notes.source, id: 'other' } },
    { ...notes, status: 'missing' },
    { ...notes, setup: { status: 'pending' } },
    { ...notes, source: { ...notes.source, origin: 'connected' } }
  ]) {
    await assert.rejects(
      saveFirstNote('Garden reminder', {
        readStatus: async () => changed,
        fetcher: async () => assert.fail('must not capture')
      }),
      /connection changed/
    );
  }
});

test('blank notes never read or write; inaccessible session storage does not break same-page retries', async () => {
  writeNoteDraft({ text: '', requestId: 'blank', sourceId: 'notes' });
  await assert.rejects(
    saveFirstNote('  ', { readStatus: async () => assert.fail('must not read') }),
    /Write something/
  );
  draft();
  const saved = await saveFirstNote('Garden reminder', {
    readStatus,
    fetcher: async () => response(200, { capture: { note: 'Inbox/garden.md' } })
  });
  assert.equal(saved.note, 'Inbox/garden.md');
});
