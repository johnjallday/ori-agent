/**
 * Role Catalog lookup: fetches GET /api/agents/catalog once and exposes a
 * shared slug -> {displayName, emblem, accentColor, tagline} map, so every
 * surface that renders a role (roster card, stage panel, filters, Map
 * insignia) uses the same identity instead of re-deriving it (PRD design
 * consideration: "consistent everywhere the role appears").
 *
 * Usage:
 *   RoleCatalog.ready().then(function () { ... })
 *   RoleCatalog.label(role)   // display name, or "Unspecialized" for ""/"general"
 *   RoleCatalog.entry(role)   // full catalog entry, or null for Unspecialized/unknown
 */
(function () {
  'use strict';

  var byRole = {};
  var loaded = false;
  var loadPromise = null;

  function load() {
    if (loadPromise) return loadPromise;
    loadPromise = fetch('/api/agents/catalog')
      .then(function (r) {
        if (!r.ok) throw new Error('catalog ' + r.status);
        return r.json();
      })
      .then(function (data) {
        var entries = Array.isArray(data.entries) ? data.entries : [];
        entries.forEach(function (e) {
          if (e && e.slug) byRole[e.slug] = e;
        });
        loaded = true;
      })
      .catch(function (err) {
        console.error('[role-catalog] failed to load', err);
        loaded = true; // don't retry forever; callers fall back to raw labels
      });
    return loadPromise;
  }

  function normalizeRole(role) {
    return String(role || '').trim().toLowerCase();
  }

  function isUnspecialized(role) {
    var r = normalizeRole(role);
    return r === '' || r === 'general';
  }

  // entry returns the catalog entry for a role slug, or null for
  // Unspecialized (empty/"general") or any role not in the catalog
  // (cli_agent, or a slug the catalog doesn't recognize).
  function entry(role) {
    if (isUnspecialized(role)) return null;
    return byRole[normalizeRole(role)] || null;
  }

  // label returns the display name for a role: catalog display_name, or
  // "Unspecialized" for empty/"general", or a titlecased fallback for any
  // other unrecognized slug (e.g. cli_agent, or a catalog role before the
  // catalog fetch resolves).
  function label(role) {
    if (isUnspecialized(role)) return 'Unspecialized';
    var e = entry(role);
    if (e) return e.display_name;
    return String(role || '')
      .replace(/[_-]+/g, ' ')
      .trim()
      .replace(/\w\S*/g, function (w) {
        return w.charAt(0).toUpperCase() + w.slice(1);
      });
  }

  // ORDERED_ROLES lists the 6 catalog role slugs in the catalog's own display
  // order, for building fixed filter/picker option lists without waiting on
  // a fetch that might still be in flight.
  var ORDERED_ROLES = [
    'orchestrator',
    'researcher',
    'analyzer',
    'synthesizer',
    'validator',
    'specialist'
  ];

  window.RoleCatalog = {
    ready: load,
    isLoaded: function () {
      return loaded;
    },
    entry: entry,
    label: label,
    isUnspecialized: isUnspecialized,
    orderedRoles: ORDERED_ROLES
  };

  load();
})();
