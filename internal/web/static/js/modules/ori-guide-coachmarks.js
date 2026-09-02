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
      label: 'Ask Ori composer'
    },
    quick_capture: {
      routes: ['/'],
      selector: '#cockpitCaptureBtn',
      label: 'Add to backlog'
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
    },
    agent_toolbox: {
      routes: ['/agents'],
      // The Inspector's Toolbox tab. Present only once an agent is open, which
      // resolve() handles: an absent target degrades to the destination rather
      // than marking nothing.
      selector: '#tab-toolbox',
      label: "an agent's Toolbox tab"
    },
    action_center_review: {
      routes: ['/action-center'],
      selector: '#action-center-list',
      label: 'the Action Center review list'
    },
    add_mcp_server: {
      routes: ['/mcp'],
      selector: '#addServerBtn',
      label: 'Add MCP Server'
    },
    // The guided Personal HQ walkthrough. Both selectors are hand-written
    // attributes on Home's own markup; the walkthrough sends only these keys.
    personal_hq_site: {
      routes: ['/'],
      selector: '[data-hq-site]',
      label: 'the reserved Personal HQ site'
    },
    personal_hq_build: {
      routes: ['/'],
      // The Build action inside the HQ site's context dialog. Present only once
      // the user has selected the site, which resolve() handles: an absent
      // target yields no mark rather than a mark on something else.
      selector: '[data-hq-action="build"]',
      label: 'Build My HQ'
    }
    // Deliberately absent:
    //
    // /vaults — its controls are rendered by page script with no stable ids, so
    // any selector here would be a guess that breaks silently.
    //
    // account connection — the connect control lives on /settings, but the
    // Connection topic's canonical destination is /vaults, and the server only
    // offers a coachmark on a topic's own route. An entry here would never fire.
    //
    // Both fall back to the canonical destination, which is the documented
    // behaviour rather than a missing feature (PRD FR-43).
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
