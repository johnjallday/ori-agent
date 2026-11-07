import { chromeText as text } from './workspace-command-chrome.js';

// Presentation and delegation only. Agent, member, work and resource state stay
// with WorkspaceCommandView and WorkspaceDetailPage's canonical owners.
export function compactTeamHTML(c) {
  let groups;
  try {
    groups = c.agentGroups().agents;
  } catch (_) {
    groups = null;
  }
  const unknown = !groups || (!c.page?.initialListsLoaded && !groups.length);
  // Keep the existing canonical selection/preference reconciliation, even
  // while the full controls are collapsed. This does not hire or reassign.
  if (Array.isArray(groups)) c.selectedAgentGroup(groups);
  const agents = (groups || []).map(group => c.agentViewModel(group)).filter(Boolean);
  const rows = agents
    .map(
      agent =>
        '<li><button type="button" class="ws-cmd-team-agent" data-cmd-team-agent="' +
        text(agent.encodedName) +
        '">' +
        c.agentCharacterHTML(agent, 'roster') +
        '<span><strong>' +
        text(agent.name) +
        '</strong><small>' +
        text((agent.entry ? 'Lead · ' : '') + (agent.role?.label || 'Agent')) +
        '</small></span><span>' +
        text(agent.status?.label || 'Unknown') +
        '</span></button></li>'
    )
    .join('');
  return (
    '<section class="ws-cmd-team" aria-labelledby="ws-cmd-team-title"><header><h3 id="ws-cmd-team-title">Team' +
    (unknown ? '' : ' <span>' + agents.length + '</span>') +
    '</h3>' +
    '<button type="button" class="ws-cmd-agent-action" data-cmd-team-toggle aria-expanded="' +
    Boolean(c.teamExpanded && !unknown) +
    '"' +
    (unknown ? ' disabled' : '') +
    '>' +
    (c.teamExpanded && !unknown ? 'Close team controls' : 'Manage team') +
    '</button></header>' +
    (unknown
      ? '<p>Team information is not yet available.</p>'
      : rows
        ? '<ul class="ws-cmd-team-list">' + rows + '</ul>'
        : c.teamExpanded
          ? ''
          : '<div class="ws-cmd-team-empty">' + c.emptyRosterHireHTML() + '</div>') +
    (c.teamExpanded && !unknown
      ? '<section class="ws-cmd-garrison">' + c.renderGarrison() + '</section>'
      : c.roleRosterHostHTML()) +
    '</section>'
  );
}

export function addWorkspaceMenuHTML(c) {
  if (!c.isGroupWorkspace()) return '';
  return (
    '<details class="ws-cmd-disclosure ws-cmd-add-workspace" data-cmd-add-workspace' +
    (c.addWorkspaceOpen ? ' open' : '') +
    '><summary class="ws-cmd-nav-btn">Add workspace</summary>' +
    '<div class="ws-cmd-disclosure-body"><button type="button" class="ws-cmd-view-btn" data-cmd-group-create>Create new</button>' +
    '<button type="button" class="ws-cmd-view-btn" data-cmd-group-link>Add existing</button></div></details>'
  );
}

export function linkedWorkspacesHTML(c) {
  if (!c.isGroupWorkspace()) return '';
  return (
    '<section class="ws-cmd-linked-workspaces" aria-label="Linked workspaces"><header><h3>Linked workspaces</h3>' +
    '<div class="ws-cmd-linked-actions"><button type="button" class="ws-cmd-agent-action" data-cmd-member-inventory>Member inventory</button>' +
    addWorkspaceMenuHTML(c) +
    '</div>' +
    '</header><div data-cmd-members-host><p>Workspace membership is loading or unavailable.</p></div></section>'
  );
}

export function resourceAccessHTML(c) {
  const p = c.page || {};
  const resources = [
    ['notes', 'Notes', p.notes],
    ['files', 'Files & linked folders', p.files],
    ['memory', 'Memory', null],
    ['sessions', 'Conversations', p.sessions]
  ];
  return (
    '<section class="ws-cmd-resource-access" aria-label="Resources"><h3>Resources</h3>' +
    resources
      .map(
        ([key, label, rows]) =>
          '<button type="button" class="ws-cmd-resource-row" data-cmd-resource-section="' +
          key +
          '"><span>' +
          label +
          '</span><span>' +
          (key === 'memory'
            ? 'Open'
            : !p.initialListsLoaded || p[key + 'Loading'] || p[key + 'LoadFailed']
              ? 'Unknown'
              : Array.isArray(rows)
                ? rows.length
                : 'Unknown') +
          '</span></button>'
      )
      .join('') +
    '</section>'
  );
}

export function capabilityAccessHTML(c) {
  const entries = c.mapStationRegistry();
  const stations = (Array.isArray(entries) ? entries : []).filter(entry => entry.key !== 'memory');
  return stations.length
    ? '<section class="ws-cmd-capability-access" aria-label="Workspace stations"><h3>Stations</h3>' +
        stations
          .map(
            entry =>
              '<button type="button" class="ws-cmd-resource-row" data-cmd-hq-station="' +
              text(entry.key) +
              '">' +
              text(entry.label) +
              '</button>'
          )
          .join('') +
        '</section>'
    : '';
}

export function overviewHTML(c) {
  return (
    '<div class="ws-cmd-layout ws-cmd-overview"><main class="ws-cmd-main">' +
    c.renderWorkFirst() +
    c.renderUnstaffedBanner() +
    c.renderMissionPanel() +
    c.renderFollowUpsHost() +
    linkedWorkspacesHTML(c) +
    '</main><aside class="ws-cmd-context">' +
    compactTeamHTML(c) +
    resourceAccessHTML(c) +
    capabilityAccessHTML(c) +
    '</aside></div>'
  );
}

export function captureOverview(c) {
  const menu = c.container?.querySelector?.('[data-cmd-add-workspace]');
  if (menu) c.addWorkspaceOpen = menu.open;
  c.overviewFocus = null;
  const active = typeof document === 'undefined' ? null : document.activeElement;
  const picker = c.container?.querySelector?.('[data-cmd-detachment-picker]');
  const choice = picker?.querySelector?.('[data-member-add-select]');
  if (c.detachmentPickerOpen && choice)
    c.detachmentPickerDraft = { value: choice.value, focused: choice === active };
  if (!active || !c.container?.contains?.(active)) return;
  for (const attribute of [
    'data-cmd-team-agent',
    'data-cmd-team-toggle',
    'data-cmd-resource-section',
    'data-cmd-hq-station',
    'data-cmd-group-create',
    'data-cmd-group-link',
    'data-cmd-member-inventory'
  ]) {
    if (active.hasAttribute?.(attribute))
      c.overviewFocus = { attribute, value: active.getAttribute(attribute) };
  }
  if (menu?.querySelector('summary') === active) c.overviewFocus = { menu: true };
}

export function bindOverview(c) {
  if (c.viewMode !== 'details' || c.isPersonalHQ()) return;
  const root = c.container?.querySelector?.('.ws-cmd-overview');
  if (!root) return;
  const focus = c.overviewFocus;
  if (focus?.menu)
    root.querySelector('[data-cmd-add-workspace] summary')?.focus?.({ preventScroll: true });
  else if (focus)
    Array.from(root.querySelectorAll('[' + focus.attribute + ']'))
      .find(node => node.getAttribute(focus.attribute) === focus.value)
      ?.focus?.({ preventScroll: true });
  root.addEventListener('click', event => {
    const create = event.target.closest('[data-cmd-group-create]');
    const link = event.target.closest('[data-cmd-group-link]');
    if (create || link) {
      const menu = (create || link).closest('details');
      const opener = menu.querySelector('summary');
      menu.open = false;
      if (create) c.openGroupCreator({ entryPoint: 'group_overview', invoker: opener });
      else c.page?.membersPanel?.openAddPicker?.();
      return;
    }
    if (event.target.closest('[data-cmd-member-inventory]')) {
      const owner = c.page?.membersPanel;
      if (owner) {
        owner.rollupsLoaded = true;
        void owner.loadRollups?.();
      }
      return;
    }
    const resource = event.target.closest('[data-cmd-resource-section]');
    if (resource) {
      c.openWorkspacePanel(
        'resources',
        resource,
        resource.getAttribute('data-cmd-resource-section')
      );
      return;
    }
    const station = event.target.closest('[data-cmd-hq-station]');
    if (station) {
      c.runHQStationAction(station.getAttribute('data-cmd-hq-station'), station);
      return;
    }
    const agent = event.target.closest('[data-cmd-team-agent]');
    const toggle = event.target.closest('[data-cmd-team-toggle]');
    if (agent || toggle) {
      c.teamExpanded = agent ? true : !c.teamExpanded;
      if (agent) c.selectAgent(agent.getAttribute('data-cmd-team-agent'), { focus: false });
      else c.render();
      c.container
        .querySelector(c.teamExpanded ? '.ws-cmd-garrison button' : '[data-cmd-team-toggle]')
        ?.focus?.({ preventScroll: true });
      return;
    }
    const shared = event.target.closest('.ws-cmd-team-empty [data-shared-assistant-add]');
    if (shared) {
      void c.addSharedAssistantFromOffer(shared);
      return;
    }
    const hire = event.target.closest('.ws-cmd-team-empty [data-cmd-add-agent]');
    if (hire) c.requestHire(hire.getAttribute('data-cmd-hire-role'));
  });
  root.addEventListener('keydown', event => {
    const menu = event.target.closest('[data-cmd-add-workspace]');
    if (event.key === 'Escape' && menu?.open) {
      event.stopPropagation();
      event.preventDefault();
      menu.open = false;
      menu.querySelector('summary').focus();
    }
  });
}
