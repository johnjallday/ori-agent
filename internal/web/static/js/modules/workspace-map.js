/*
 * workspace-map.js — Workspace Map view ("tactical command-center")
 *
 * Opt-in third view on the /workspaces hub, beside Cards and Tree. Loaded as a
 * plain deferred script (not an ES module), so it exposes itself on a global
 * namespace that workspace-hub.js calls when the "map" view is activated.
 *
 * Phase 0.0 = inert scaffolding. mount/unmount are safe no-ops; the real
 * command bar, building tiles, districts, and Overview land in Phase 1–2.
 */
(function () {
  'use strict';

  /**
   * Mount the map view into its container.
   * @param {HTMLElement} container - the #launcherMap element.
   * @param {object} [state] - shared hub state (workspaces, selection, etc.).
   */
  function mount(container /*, state */) {
    if (!container) return;
    // Inert until Phase 1. Intentionally renders nothing yet.
  }

  /** Tear down the map view (called when switching away). */
  function unmount(container) {
    if (!container) return;
    // Inert until Phase 1.
  }

  window.OriWorkspaceMap = { mount, unmount };
})();
