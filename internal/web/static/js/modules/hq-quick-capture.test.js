import assert from 'node:assert/strict';
import { test } from 'node:test';
import { HQCaptureDraft, hqCaptureReference } from './hq-quick-capture.js';

test('only a present Ori-created connection can receive quick capture', () => {
  const view = {
    status: 'connected',
    source: { origin: 'created_by_ori' },
    browse_ref: 'connection-a'
  };
  assert.equal(hqCaptureReference(view), 'connection-a');
  assert.equal(hqCaptureReference({ ...view, status: 'missing' }), '');
  assert.equal(hqCaptureReference({ ...view, source: { origin: 'connected' } }), '');
  assert.equal(hqCaptureReference(null), '');
});

test('an uncertain save retains the draft and retries the same request, but never to a new connection', async () => {
  const draft = new HQCaptureDraft();
  draft.edit('A recoverable thought', 'connection-a');
  const sends = [];
  const send = async (...args) => {
    sends.push(args);
    if (sends.length === 1) throw new Error('Lost response');
    return { capture: { note: 'Inbox/thought.md' } };
  };
  assert.equal(await draft.save('connection-a', send), false);
  assert.equal(draft.text, 'A recoverable thought');
  assert.equal(draft.receipt, '');
  assert.equal(await draft.save('connection-b', send), false);
  assert.equal(sends.length, 1);
  assert.equal(await draft.save('connection-a', send), true);
  assert.deepEqual(sends[0], sends[1]);
  assert.equal(sends[1][1].source_ref, 'connection-a');
  assert.equal(draft.text, '');
  assert.match(draft.receipt, /Saved/);
});

test('an invalid success response is not a receipt', async () => {
  const draft = new HQCaptureDraft();
  draft.edit('Do not silently lose this', 'connection-a');
  assert.equal(await draft.save('connection-a', async () => ({})), false);
  assert.equal(draft.text, 'Do not silently lose this');
  assert.match(draft.problem, /No save receipt/);
});
