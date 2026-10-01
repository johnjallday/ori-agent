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

  // Where a saved model or prompt edit went, from the update response's
  // `carried` { updated: [{id, name}], customised: [{id, name}] }: the
  // workspaces whose copy Ori keeps in step got it; one changed there kept its
  // own. '' when the edit reached no workspace copy.
  function carriedEditLabel(carried) {
    if (!carried || typeof carried !== 'object') return '';
    var refNames = function (refs) {
      return (Array.isArray(refs) ? refs : [])
        .map(function (ref) {
          return String((ref && (ref.name || ref.id)) || '').trim();
        })
        .filter(Boolean);
    };
    var updated = refNames(carried.updated).length;
    var kept = refNames(carried.customised);
    var parts = [];
    if (updated) {
      parts.push('Also updated in ' + updated + ' workspace' + (updated === 1 ? '' : 's') + '.');
    }
    if (kept.length) {
      parts.push(
        kept.join(', ') + (kept.length === 1 ? ' keeps its' : ' keep their') + ' own changes.'
      );
    }
    return parts.join(' ');
  }

  // What a delete of an agent that works in workspaces would hit, said before
  // anything is sent (the server refuses that delete): '“Studio Assistant” works
  // in 12 workspaces (Song 1, Song 2, Song 3 and 9 more). Remove it from those
  // workspaces before deleting it.' '' when it works in none.
  function attachedDeleteMessage(agent) {
    var count = Number((agent && agent.workspace_count) || 0);
    if (!count) return '';
    var shown = (agent && Array.isArray(agent.workspaces) ? agent.workspaces : [])
      .map(function (ref) {
        return String((ref && (ref.name || ref.id)) || '').trim();
      })
      .filter(Boolean)
      .slice(0, 3);
    var list = '';
    if (shown.length) {
      list =
        ' (' +
        shown.join(', ') +
        (count > shown.length ? ' and ' + (count - shown.length) + ' more' : '') +
        ')';
    }
    return (
      '“' +
      String((agent && agent.name) || 'This agent') +
      '” works in ' +
      count +
      ' workspace' +
      (count === 1 ? '' : 's') +
      list +
      '. Remove it from ' +
      (count === 1 ? 'that workspace' : 'those workspaces') +
      ' before deleting it.'
    );
  }

  var api = {
    isWorkspaceOwned: isWorkspaceOwned,
    workspaceName: workspaceName,
    workspaceMarker: workspaceMarker,
    customisedIn: customisedIn,
    customisedInLabel: customisedInLabel,
    carriedEditLabel: carriedEditLabel,
    attachedDeleteMessage: attachedDeleteMessage
  };

  if (typeof window !== 'undefined') {
    window.AgentOrigin = api;
  }
})();
