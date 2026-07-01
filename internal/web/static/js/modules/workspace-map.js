/*
 * workspace-map.js — Workspace Map view ("tactical command-center")
 *
 * Opt-in third view on the /workspaces hub, beside Cards and Tree. Loaded as a
 * plain deferred script (not an ES module); exposes itself on
 * window.OriWorkspaceMap, which workspace-hub.js calls when the "map" view is
 * active.
 *
 * Phase 1 = the shell: command bar with live counts, theatre chrome, and an
 * empty Overview. Building tiles, districts, and a populated Overview land in
 * Phase 2-3.
 */
(function () {
  'use strict';

  function escapeHtml(value) {
    return String(value == null ? '' : value).replace(/[&<>"']/g, function (c) {
      return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c];
    });
  }

  function isGroup(ws) {
    return String((ws && ws.kind) || '').trim().toLowerCase() === 'group';
  }

  function computeStats(workspaces) {
    var list = Array.isArray(workspaces) ? workspaces : [];
    var agents = 0;
    var activeTasks = 0;
    var groups = 0;
    list.forEach(function (ws) {
      agents += Number((ws && ws.agent_count) || 0);
      activeTasks += Number((ws && ws.open_task_count) || 0);
      if (isGroup(ws)) groups += 1;
    });
    return { workspaces: list.length, agents: agents, activeTasks: activeTasks, groups: groups };
  }

  function statBox(value, label) {
    return (
      '<div class="ws-map-stat"><div class="ws-v">' +
      escapeHtml(value) +
      '</div><div class="ws-l">' +
      escapeHtml(label) +
      '</div></div>'
    );
  }

  function shellHTML(stats) {
    return (
      '<header class="ws-map-topbar">' +
      '<div class="ws-map-crest">' +
      '<svg width="26" height="26" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.3">' +
      '<path d="M2 20h20"/><path d="M5 20V9l5-3 5 3v11"/><path d="M15 20v-7l4-2 0 9"/><path d="M8 12h2M8 15h2"/></svg>' +
      '</div>' +
      '<div class="ws-map-title">' +
      '<div class="ws-kicker"><span class="ws-dot"></span><span class="ws-tick">Workspace Map</span></div>' +
      '<h2>Workspaces</h2>' +
      '<div class="ws-sub">Select a workspace to preview it, then open</div>' +
      '</div>' +
      '<div class="ws-map-readout">' +
      statBox(stats.workspaces, 'Workspaces') +
      statBox(stats.agents, 'Agents') +
      statBox(stats.activeTasks, 'Active Tasks') +
      statBox(stats.groups, 'Groups') +
      '</div>' +
      '<button type="button" class="ws-map-create" data-ws-map-create>⊕ New Workspace</button>' +
      '</header>' +
      '<div class="ws-map-layout">' +
      '<section class="ws-map-theatre">' +
      '<div class="ws-map-theatre-tag"><span class="ws-map-ix">MAP</span><h3>All Workspaces</h3></div>' +
      '<div class="ws-map-compass">N<b>▲</b></div>' +
      '<div class="ws-map-canvas"><div class="ws-map-canvas-empty">Building tiles arrive in the next update</div></div>' +
      '</section>' +
      '<aside class="ws-map-overview">' +
      '<div class="ws-map-overview-head"><div><span class="ws-map-ix">WS</span> <h3>Overview</h3></div></div>' +
      '<div class="ws-map-overview-body">' +
      '<div class="ws-map-overview-empty"><span class="ws-big">◈</span>Select a workspace to see its agents, tasks, tools, and skills.</div>' +
      '</div>' +
      '</aside>' +
      '</div>'
    );
  }

  /**
   * Mount/refresh the map view. Idempotent — safe to call on every re-render.
   * @param {HTMLElement} container - the #launcherMap element.
   * @param {object} [state] - { workspaces: [...enriched summaries] }.
   */
  function mount(container, state) {
    if (!container) return;
    var workspaces = (state && state.workspaces) || [];
    container.innerHTML = shellHTML(computeStats(workspaces));

    var createBtn = container.querySelector('[data-ws-map-create]');
    if (createBtn) {
      createBtn.addEventListener('click', function () {
        // Reuse the hub's existing create flow.
        var hubCreate = document.getElementById('launcherCreateWorkspaceBtn');
        if (hubCreate) hubCreate.click();
      });
    }
  }

  /** Tear down the map view (called when switching away). */
  function unmount(container) {
    if (!container) return;
    container.innerHTML = '';
  }

  window.OriWorkspaceMap = { mount: mount, unmount: unmount };
})();
