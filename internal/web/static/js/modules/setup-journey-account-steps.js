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
  return rows;
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
