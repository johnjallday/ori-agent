const unavailable = {
  text: 'Mailbox readiness could not be verified. Check again before connecting.'
};
const id = value => typeof value === 'string' && /^[\w-]+$/.test(value);

// Always address Email Ops, never the HQ's own mailbox. Connection-only
// status, a redirect, and an unbound login cannot certify workspace readiness.
export async function emailNextStep(read, expected = {}) {
  const ops = (await read('/api/personal-hq/email-ops'))?.status;
  if (ops?.exists === false && !expected.workspace)
    return {
      text: 'No mailbox is ready in Email Ops. Setup will create the workspace and link a mailbox for reading and searching.',
      setup: true
    };
  if (
    !ops?.exists ||
    (expected.attempt && !expected.workspace) ||
    !id(ops.workspace_id) ||
    (expected.workspace && ops.workspace_id !== expected.workspace)
  )
    return unavailable;
  const workspace = ops.workspace_id;
  const status = (await read(`/api/workspaces/${encodeURIComponent(workspace)}/email/status`))
    ?.status;
  if (!status?.setup || !id(ops.workspace_slug)) return unavailable;
  const href = `/workspaces/${encodeURIComponent(ops.workspace_slug)}`;
  if (status.setup.ready === true) {
    if (
      !status.connected ||
      !status.account_id ||
      (expected.account && status.account_id !== expected.account)
    )
      return unavailable;
    return {
      workspace,
      account: status.account_id,
      text: 'Email Ops has a ready mailbox. No email has been reviewed by this action.',
      href,
      label: 'Review email in Email Ops'
    };
  }
  const next = {
    workspace,
    account: status.account_id,
    text: status.setup.message || 'The mailbox is not ready.'
  };
  switch (status.setup.action) {
    case 'set_up_email':
      return { ...next, setup: true };
    case 'link_account':
      return { ...next, href, label: 'Review mailbox link in Email Ops' };
    case 'repair_vault':
      return { ...next, href: '/vaults', label: 'Unlock vault' };
    case 'connect_google':
    case 'enable_gmail':
    case 'reconnect_gmail':
      return { ...next, href: '/settings#google-account', label: 'Review Google Account' };
    default:
      return unavailable;
  }
}
