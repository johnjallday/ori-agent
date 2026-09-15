// Rendering and launch helpers for the host account-link setup shape
// (workspace_create → account_connect → account_link → summary). The journey
// shell in setup-journey.js owns the modal, step rail, and server requests;
// this module only turns server projections into rows and opens the shared
// Workspace creator. Nothing here selects a route, owner, or account: every
// value comes from the server projection or is compiled below.

export const ACCOUNT_STEP_KINDS = Object.freeze([
  'workspace_create',
  'account_connect',
  'account_link'
]);

export const EMAIL_OPS_BLUEPRINT_ID = 'email-ops';
export const EMAIL_OPS_DEFAULT_NAME = 'Email Ops';
export const INBOX_LOCK_REASON = 'Mail access is granted to the agent named Inbox.';

const WORKSPACE_ROUTE_PATTERN = /^\/workspaces\/[^/?#\\%\s]+$/;

export function isAccountStepKind(kind) {
  return ACCOUNT_STEP_KINDS.includes(kind);
}

// accountStepReceiptRows lists what the server says is already true for one
// account-link step. Rows with no value are dropped by the caller.
export function accountStepReceiptRows(step) {
  const rows = [];
  const created = step?.workspace_create;
  if (step?.kind === 'workspace_create' && created) {
    rows.push(['Blueprint', created.template_title]);
    rows.push(['Workspace', created.workspace_id ? created.workspace_label : 'Not created yet']);
  }
  const connect = step?.account_connect;
  if (step?.kind === 'account_connect' && connect) {
    rows.push(['Google account', connect.identity_email || 'Not connected']);
    rows.push(['Gmail', GMAIL_HEALTH_LABELS[connect.gmail_health] || 'Unknown']);
  }
  const link = step?.account_link;
  if (step?.kind === 'account_link' && link) {
    rows.push(['Workspace', link.workspace_label]);
    rows.push([
      'Mailbox',
      link.ready ? link.account_email : link.linked ? 'Linked, needs repair' : 'Not linked yet'
    ]);
  }
  return rows;
}

const GMAIL_HEALTH_LABELS = Object.freeze({
  unconfigured: 'Google sign-in is not configured on this Ori server',
  not_connected: 'Connect Google first',
  not_enabled: 'Not enabled yet',
  unhealthy: 'Needs reconnecting',
  vault_unavailable: 'Credential vault needs attention',
  healthy: 'Enabled'
});

export const GOOGLE_ACCOUNT_SETTINGS_URL = '/settings#google-account';
export const MODEL_SETTINGS_URL = '/settings#system-model';
export const TRIAGE_MODEL_NOTE = 'Triage needs an AI model; mailbox readiness does not.';
export const SETTINGS_TAB_NOTE =
  'Google Account opened in a new tab. Finish there, then come back and choose Check again.';

const SETTINGS_ROUTE_PATTERN = /^\/settings(?:[#?][^\s\\:<>"']*)?$/;

// accountActionLabel lets the connection step name its exact repair ("Enable
// Gmail", "Unlock vault") on the one compiled Settings action.
export function accountActionLabel(step, action) {
  const label = String(step?.account_connect?.action_label || '').trim();
  if (step?.kind === 'account_connect' && action?.id === 'open_account_settings' && label) {
    return label;
  }
  return action?.label || '';
}

// accountSettingsURL uses the readiness owner's own Settings route when it is a
// plain same-origin Settings route, and the Google Account card otherwise.
export function accountSettingsURL(journey) {
  const step = (journey?.steps || []).find(item => item?.kind === 'account_connect');
  const url = String(step?.account_connect?.action_url || '');
  return SETTINGS_ROUTE_PATTERN.test(url) && !url.includes('//')
    ? url
    : GOOGLE_ACCOUNT_SETTINGS_URL;
}

// summaryModelNote explains why triage is not offered, only when the summary
// offers model settings instead.
export function summaryModelNote(step) {
  if (step?.kind !== 'summary') return '';
  return (step.actions || []).some(action => action?.id === 'open_model_settings')
    ? TRIAGE_MODEL_NOTE
    : '';
}

// accountLinkReviewPresentation is the consent card for the reviewed link. The
// server bound this exact workspace, account, and disclosure into the token.
export function accountLinkReviewPresentation(review) {
  const link = review?.account_link;
  if (!link) return null;
  const workspace = link.workspace_label || 'this workspace';
  return {
    title: `Link this mailbox to “${workspace}”?`,
    confirm: 'Link mailbox',
    description:
      'Ori uses the Google account you already connected. No new sign-in happens and no new permission is requested.',
    rows: [
      ['Workspace', workspace],
      ['Google account', link.account_email || 'Your connected account'],
      ['Email Ops can', 'Read and search this mailbox, and prepare drafts'],
      ['Email Ops never', 'Sends a message without your confirmation of that specific message'],
      ['Sign-in', 'No new sign-in happens']
    ]
  };
}

// teamReviewCreatorOptions opens the one shared Workspace creator on the Email
// Ops blueprint with its Postmaster and Inbox roles proposed. The Inbox role
// stays filled and named Inbox because mail tools are granted to that agent by
// name; the page stays put so the quest can continue.
export function teamReviewCreatorOptions(onCreated) {
  return {
    blueprint: EMAIL_OPS_BLUEPRINT_ID,
    entryPoint: 'host_setup_quest',
    stayAfterCreate: true,
    stageBlueprintRoles: true,
    teamLock: { agentNames: ['Inbox'], reason: INBOX_LOCK_REASON },
    onCreated: typeof onCreated === 'function' ? onCreated : undefined
  };
}

// accountStepWorkspaceRoute returns the server-validated workspace route from
// the team step's projection, or '' so the caller resolves the ID instead.
export function accountStepWorkspaceRoute(journey, suffix = '') {
  const step = (journey?.steps || []).find(item => item?.kind === 'workspace_create');
  const route = String(step?.workspace_create?.workspace_route || '');
  if (!WORKSPACE_ROUTE_PATTERN.test(route)) return '';
  return `${route}${suffix}`;
}

// advanceCreatorToTeam waits for the preselected blueprint, fills the default
// name only when Details is still empty, and moves to Team. goToWizardStep
// keeps the user on Details with an error when the name cannot be used.
export async function advanceCreatorToTeam({
  manager,
  getSelectedTemplate,
  nameInput,
  isCurrent = () => true,
  wait = ms => new Promise(resolve => setTimeout(resolve, ms)),
  attempts = 60
}) {
  for (let attempt = 0; attempt < attempts && isCurrent(); attempt++) {
    const template = getSelectedTemplate?.();
    if (template?.id === EMAIL_OPS_BLUEPRINT_ID && !manager.blueprintSelectionBlocked?.()) {
      if (nameInput && !String(nameInput.value || '').trim()) {
        nameInput.value = EMAIL_OPS_DEFAULT_NAME;
        // Let the creator's own listeners update its slug preview.
        if (typeof Event === 'function') {
          nameInput.dispatchEvent?.(new Event('input', { bubbles: true }));
        }
      }
      manager.goToWizardStep(3);
      return true;
    }
    await wait(75);
  }
  return false;
}
