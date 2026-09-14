/*
 * group-template-status.js — shared copy for a group's template status.
 *
 * GET /api/workspaces/{id}/group-template reports three independent facts:
 * the group exists, whether its required coordinator roles are verified as
 * filled, and whether its source integration is available. The group page and
 * guided setup both describe them with these functions so the two surfaces can
 * never disagree about what "Ready" means. Nothing here infers a fact the
 * server did not verify.
 */

const INTEGRATION_REASONS = {
  plugin_enable_required: 'Unavailable — its plugin is disabled',
  plugin_install_required: 'Unavailable — its plugin is not installed',
  dependency_state_unknown: 'Could not be checked',
  home_incompatible: 'Needs its guided migration',
  template_unavailable: 'Unavailable — its source template is missing',
  not_offered_as_group_template: 'Available',
  home_declaration_conflict: 'Unavailable — its sources disagree'
};

export function groupTemplateProviderLabel(status) {
  const provider = status?.provider;
  if (provider?.plugin_id) {
    return `Plugin: ${provider.plugin_id}${provider.plugin_version ? ` ${provider.plugin_version}` : ''}`;
  }
  return provider?.kind === 'user_template' ? 'Your template' : '';
}

export function groupTemplateTeamFact(status) {
  const team = status?.team || {};
  const required = team.required_home_roles || {};
  const roles = Array.isArray(required.roles) ? required.roles : [];
  const missing = roles.filter(role => role.state !== 'filled');
  const verified = required.verification === 'verified';
  if (team.state === 'migration_required') {
    return { copy: 'Needs its guided migration', className: 'is-unknown', verified, missing };
  }
  if (verified && team.state === 'ready') {
    return {
      copy: `Ready — ${required.filled} of ${required.required} required set up`,
      className: 'is-ready',
      verified,
      missing
    };
  }
  if (verified) {
    return {
      copy: `Incomplete — ${required.filled} of ${required.required} required set up (${missing
        .map(role => role.label)
        .join(', ')})`,
      className: 'is-incomplete',
      verified,
      missing
    };
  }
  return { copy: 'Could not be verified', className: 'is-unknown', verified, missing };
}

export function groupTemplateIntegrationFact(integration) {
  const state = integration?.state || 'unknown';
  let copy = INTEGRATION_REASONS[integration?.reason];
  if (!copy) {
    copy =
      state === 'available'
        ? 'Available'
        : state === 'unknown'
          ? 'Could not be checked'
          : 'Unavailable';
  }
  const className =
    state === 'available' ? 'is-ready' : state === 'unknown' ? 'is-unknown' : 'is-unavailable';
  return { copy, className };
}

// Resolves to the status object, or null when it cannot be read. A null result
// is "unknown", never "not set up".
export function fetchGroupTemplateStatus(workspaceId) {
  if (!workspaceId) return Promise.resolve(null);
  return fetch(`/api/workspaces/${encodeURIComponent(workspaceId)}/group-template`, {
    headers: { Accept: 'application/json' }
  })
    .then(response => (response.ok ? response.json() : null))
    .then(data => data?.group_template || null)
    .catch(() => null);
}
