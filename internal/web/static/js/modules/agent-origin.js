// agent-origin.js — where a roster entry comes from, as the Agents page shows it.
//
// The roster is the user's own agents (<Workspace Directory>/Agents), the
// built-in assistant, and agents that only a trusted workspace holds, read
// from that workspace's own copy. The list and detail responses carry this as
// an `origin` object:
//
//   { source: 'system' | 'roster' | 'workspace',
//     workspace_id, workspace_name,          // workspace entries
//     customised_in: [{ id, name }] }        // system/roster entries
//
// A workspace entry is edited in its workspace, not on the Agents page, so the
// page hides edit, delete, and selection for it and offers "Add to my agents".
//
// Classic script (loaded with defer) so the page controllers can read
// window.AgentOrigin at load, like agent-avatar.js.
(function () {
  'use strict';

  function originOf(agent) {
    var origin = agent && agent.origin;
    return origin && typeof origin === 'object' ? origin : null;
  }

  function isWorkspaceOwned(agent) {
    var origin = originOf(agent);
    return !!origin && origin.source === 'workspace';
  }

  function workspaceName(agent) {
    var origin = originOf(agent);
    if (!origin) return '';
    return String(origin.workspace_name || origin.workspace_id || '').trim();
  }

  // "From workspace: Studio" for a workspace entry, '' otherwise.
  function workspaceMarker(agent) {
    if (!isWorkspaceOwned(agent)) return '';
    return 'From workspace: ' + (workspaceName(agent) || 'a workspace');
  }

  // The workspaces holding their own copy of one of the user's agents.
  function customisedIn(agent) {
    var origin = originOf(agent);
    if (!origin || origin.source === 'workspace' || !Array.isArray(origin.customised_in)) return [];
    return origin.customised_in
      .map(function (ref) {
        return String((ref && (ref.name || ref.id)) || '').trim();
      })
      .filter(Boolean);
  }

  // "Customised in: Studio, Garage", or '' when no workspace has a copy.
  function customisedInLabel(agent) {
    var names = customisedIn(agent);
    return names.length ? 'Customised in: ' + names.join(', ') : '';
  }

  var api = {
    isWorkspaceOwned: isWorkspaceOwned,
    workspaceName: workspaceName,
    workspaceMarker: workspaceMarker,
    customisedIn: customisedIn,
    customisedInLabel: customisedInLabel
  };

  if (typeof window !== 'undefined') {
    window.AgentOrigin = api;
  }
})();
