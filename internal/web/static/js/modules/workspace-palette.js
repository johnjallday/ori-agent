/*
 * workspace-palette.js — the workspace colour key, shared across surfaces.
 *
 * A workspace's colour is derived from its id, so it stays the same across a
 * refresh, an add, and a remove, and the same workspace reads as the same place
 * wherever it is drawn. The Workspace Map has picked cottage colours this way
 * since #292; this module exists so the Agents roster's workspace sections can
 * be the same green as the Studio cottage rather than a second, similar-looking
 * green (agents-page-ux FR-24).
 *
 * Only the `key` colour lives here — the accent that identifies the workspace.
 * The map's wall/roof/trim/plot shades stay in workspace-map.js, because
 * nothing else draws a cottage.
 *
 * NOTE: the KEYS array below must stay in step with the `key` values of
 * PALETTE in workspace-map.js, in the same order. workspace-map.js is switched
 * over to read from this module in the group that already edits it; until then
 * the two lists are checked against each other by
 * modules/workspace-palette.test.js.
 *
 * Loaded as a classic deferred script; exposes window.WorkspacePalette.
 */
(function () {
  'use strict';

  // Warm and earthy, to match the cozy visual language. Colour here is
  // identity, never status: an agent's or a workspace's health is always
  // carried by text alongside it.
  var KEYS = [
    '#6f96b8', // slate blue
    '#d3a44a', // gold
    '#c0714c', // terracotta
    '#8f78ad', // plum
    '#6a9a5f', // moss
    '#5c9aa3' // teal
  ];

  // FNV-1a. Chosen for being stable across engines and reloads, which is the
  // whole point: a workspace that changes colour on refresh is worse than one
  // with no colour at all.
  function hashKey(id) {
    var h = 2166136261;
    var s = String(id || '');
    for (var i = 0; i < s.length; i++) {
      h ^= s.charCodeAt(i);
      h = Math.imul(h, 16777619);
    }
    return h >>> 0;
  }

  function keyFor(id) {
    return KEYS[hashKey(id) % KEYS.length];
  }

  window.WorkspacePalette = {
    KEYS: KEYS.slice(),
    hashKey: hashKey,
    keyFor: keyFor
  };
})();
