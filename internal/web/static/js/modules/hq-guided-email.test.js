import { test } from 'node:test';
import assert from 'node:assert/strict';
import { emailNextStep } from './hq-guided-email.js';

const ops = { exists: true, workspace_id: 'email-ops-1', workspace_slug: 'my-mail' };
const ready = { connected: true, account_id: 'account-1', setup: { ready: true } };
function reader(workspace, status) {
  return async path => {
    if (path === '/api/personal-hq/email-ops') return { status: workspace };
    assert.equal(path, '/api/workspaces/email-ops-1/email/status');
    return { status };
  };
}
test('new workspace setup uses the card; ready requires exact workspace, account and readiness', async () => {
  assert.equal((await emailNextStep(reader({ exists: false }, null))).setup, true);
  assert.equal(
    (await emailNextStep(reader(ops, ready), { workspace: ops.workspace_id, account: 'account-1' }))
      .href,
    '/workspaces/my-mail'
  );
  for (const [status, expected] of [
    [ready, { workspace: 'other' }],
    [ready, { attempt: 'unconfirmed-new-mailbox' }],
    [ready, { account: 'other' }],
    [{ connected: true }, {}],
    [{ ...ready, connected: false }, {}]
  ])
    assert.equal((await emailNextStep(reader(ops, status), expected)).href, undefined);
});
test('only server-named repairs get navigation; no credentials or arbitrary destinations', async () => {
  for (const [action, href] of [
    ['repair_vault', '/vaults'],
    ['link_account', '/workspaces/my-mail'],
    ['reconnect_gmail', '/settings#google-account'],
    ['other', undefined]
  ]) {
    const step = await emailNextStep(
      reader(ops, { setup: { ready: false, action, action_url: 'https://other.test' } })
    );
    assert.equal(step.href, href);
  }
});
