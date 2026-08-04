/*
 * Coachmark registry — the browser's allowlist of controls Ori may point at.
 *
 * The server sends a typed key such as "new_agent". This file is the only place
 * a key becomes an element, and it does so through selectors written here, by
 * hand, in advance. No selector ever arrives over the wire, so neither the
 * server nor a model can aim the guide at arbitrary page structure
 * (PRD FR-41).
 *
 * A coachmark marks and focuses. It never clicks, never submits, and never
 * changes a value — pointing at the New Agent button must not create an agent
 * (FR-42).
 *
 * Loaded as a classic deferred script; `window.OriGuideCoachmarks` is the seam
 * the controller and unit tests use.
 */
(function () {
  'use strict';

  // key -> { route, selector, label }
  //
  // `route` records which page owns the control. A key is only honoured on its
  // own route, so a stale key from a previous page cannot mark a same-named
  // element somewhere else.
  var REGISTRY = {
    workspace_manager: {
      routes: ['/'],
      selector: '#homeAssistantInput',
      label: 'Workspace Manager command box'
    },
    quick_capture: {
      routes: ['/'],
      selector: '#cockpitCaptureBtn',
      label: 'Quick Capture'
    },
    new_workspace: {
      routes: ['/', '/workspaces'],
      selector: '#cockpitCreateWorkspaceBtn',
      label: 'New Workspace'
    },
    view_toggle: {
      routes: ['/'],
      selector: '#cockpitViewTree',
      label: 'Map / Tree view switch'
    },
    new_agent: {
      routes: ['/agents'],
      selector: '#newAgentBtn',
      label: 'New Agent'
    }
  };

  function normalizeRoute(route) {
    var r = String(route || '/').trim();
    if (!r.startsWith('/')) return '/';
    if (r.length > 1 && r.endsWith('/')) r = r.slice(0, -1);
    return r === '' ? '/' : r;
  }

  function entryFor(key) {
    if (!key || typeof key !== 'string') return null;
    return Object.prototype.hasOwnProperty.call(REGISTRY, key) ? REGISTRY[key] : null;
  }

  // supports reports whether a key is registered for a route at all — separate
  // from whether its element currently exists, because those are different
  // failures and the guide explains them differently (FR-43).
  function supports(key, route) {
    var entry = entryFor(key);
    if (!entry) return false;
    var r = normalizeRoute(route);
    return entry.routes.some(function (allowed) {
      return normalizeRoute(allowed) === r;
    });
  }

  // resolve returns the live element for a key, or null when the key is
  // unknown, wrong for this route, or its target is absent or hidden. A hidden
  // target counts as absent: marking something the user cannot see would be a
  // promise the page does not keep.
  function resolve(key, route, doc) {
    var d = doc || (typeof document !== 'undefined' ? document : null);
    if (!d || !supports(key, route)) return null;

    var el = null;
    try {
      el = d.querySelector(REGISTRY[key].selector);
    } catch (err) {
      return null;
    }
    if (!el) return null;
    if (el.hidden || el.getAttribute('aria-hidden') === 'true') return null;
    if (typeof el.getClientRects === 'function' && el.getClientRects().length === 0) return null;
    return el;
  }

  function labelFor(key) {
    var entry = entryFor(key);
    return entry ? entry.label : '';
  }

  function keys() {
    return Object.keys(REGISTRY);
  }

  var api = {
    supports: supports,
    resolve: resolve,
    labelFor: labelFor,
    keys: keys,
    normalizeRoute: normalizeRoute,
    // Exposed so tests assert against the real table rather than a copy that
    // could drift from it.
    REGISTRY: REGISTRY
  };

  if (typeof window !== 'undefined') {
    window.OriGuideCoachmarks = api;
  }
})();
