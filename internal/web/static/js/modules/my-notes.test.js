import assert from 'node:assert/strict';
import { test } from 'node:test';
import { latestNotesRequest, myNotesConnection } from './my-notes.js';

test('notes setup prerequisites are not presented as connection errors or empty content', () => {
  assert.equal(
    myNotesConnection({ status: 'not_connected', setup_required: 'assistant' }).href,
    '/?quest=meet-assistant'
  );
  assert.equal(
    myNotesConnection({ status: 'not_connected', setup_required: 'hq' }).href,
    '/?quest=build-hq'
  );
  assert.equal(myNotesConnection({ status: 'not_connected' }).manage, 'Set up notes…');
  assert.equal(myNotesConnection().retry, true);
  assert.equal(myNotesConnection({ status: 'connected', source: {} }).retry, true);
});

test('missing and connected sources have distinct actions and modes', () => {
  const source = { id: 'notebook', name: 'Field notes', origin: 'connected' };
  assert.match(myNotesConnection({ status: 'connected', source }).detail, /read-only/);
  assert.match(
    myNotesConnection({ status: 'connected', source: { ...source, origin: 'created_by_ori' } })
      .detail,
    /capture to Inbox/
  );
  const missing = myNotesConnection({ status: 'missing', source });
  assert.equal(missing.connected, undefined);
  assert.equal(missing.manage, 'Choose again…');
});

test('a slow earlier request and a response after departure cannot repaint', () => {
  const requests = latestNotesRequest();
  const initial = requests.begin();
  const retry = requests.begin();
  assert.equal(requests.isCurrent(initial), false);
  assert.equal(requests.isCurrent(retry), true);
  requests.cancel();
  assert.equal(requests.isCurrent(retry), false);
  assert.equal(requests.isCurrent(requests.begin()), true);
});
