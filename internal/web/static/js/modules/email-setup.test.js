import test from 'node:test';
import assert from 'node:assert/strict';

import {
  connectPayload,
  failureView,
  fieldsFor,
  isEmailSetupHref,
  receiptLines,
  safeRoute,
  withoutSetupParam
} from './email-setup.js';

const ORIGIN = 'http://localhost:8765';

test('only a same-origin link to /?setup=email opens the card', () => {
  assert.equal(isEmailSetupHref('/?setup=email', ORIGIN), true);
  assert.equal(isEmailSetupHref('http://localhost:8765/?setup=email&from=home', ORIGIN), true);
  assert.equal(isEmailSetupHref('/?setup=quest&source=host&quest=email_ops_setup', ORIGIN), false);
  assert.equal(isEmailSetupHref('/settings?setup=email', ORIGIN), false);
  assert.equal(isEmailSetupHref('https://evil.example/?setup=email', ORIGIN), false);
  assert.equal(isEmailSetupHref('', ORIGIN), false);
});

test('the deep link is removed once consumed, keeping everything else', () => {
  assert.equal(withoutSetupParam(`${ORIGIN}/?setup=email`), '/');
  assert.equal(withoutSetupParam(`${ORIGIN}/?tab=today&setup=email#x`), '/?tab=today#x');
});

test('the second step asks only for what the provider needs', () => {
  assert.deepEqual(fieldsFor({ provider: 'gmail' }), { username: false, server: false });
  assert.deepEqual(fieldsFor({ needs_username: true }), { username: true, server: false });
  assert.deepEqual(fieldsFor({ needs_server: true }), { username: false, server: true });
  assert.deepEqual(fieldsFor(null), { username: false, server: false });
});

test('the connect body sends optional fields only when the card asked for them', () => {
  const gmail = { address: 'me@gmail.com' };
  assert.deepEqual(
    connectPayload(gmail, { password: 'pw', username: 'ignored', server: 'x.example' }),
    {
      address: 'me@gmail.com',
      password: 'pw'
    }
  );

  const custom = { address: 'me@own.example', needs_server: true };
  assert.deepEqual(
    connectPayload(custom, { password: 'pw', server: ' imap.own.example ', port: '143' }),
    {
      address: 'me@own.example',
      password: 'pw',
      imap_host: 'imap.own.example',
      imap_port: 143
    }
  );
  assert.deepEqual(connectPayload(custom, { password: 'pw', server: '', port: 'abc' }), {
    address: 'me@own.example',
    password: 'pw'
  });

  const icloudDomain = { address: 'me@family.example', needs_username: true };
  assert.equal(
    connectPayload(icloudDomain, { password: 'pw', username: ' me@icloud.com ' }).username,
    'me@icloud.com'
  );
});

test('a failure sits next to the field it names, or at the top', () => {
  assert.deepEqual(failureView({ error: 'wrong_password', field: 'password', message: 'No.' }), {
    field: 'password',
    message: 'No.',
    code: 'wrong_password'
  });
  assert.equal(
    failureView({ error: 'unreachable', field: 'nonsense', message: 'Down.' }).field,
    ''
  );
  assert.equal(failureView({}).message, 'Email setup failed. Try again.');
});

test('the receipt says what is now true', () => {
  const created = receiptLines({
    label: 'Gmail',
    address: 'me@gmail.com',
    workspace: { name: 'Email Ops', created: true }
  });
  assert.equal(created[0], 'Signed in to Gmail as me@gmail.com');
  assert.match(created[2], /^Created Email Ops/);
  assert.match(created[3], /nothing is sent without you/);

  const linked = receiptLines({
    label: 'iCloud Mail',
    address: 'me@icloud.com',
    workspace: { name: 'Email Ops 2' }
  });
  assert.equal(linked[2], 'Linked your inbox to Email Ops 2');
});

test('the Email Ops link never leaves this origin', () => {
  assert.equal(safeRoute('/workspaces/email-ops'), '/workspaces/email-ops');
  assert.equal(safeRoute('//evil.example/x'), '');
  assert.equal(safeRoute('https://evil.example'), '');
  assert.equal(safeRoute(undefined), '');
});
