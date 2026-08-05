/*
 * workspace-map.js — Workspace Map view ("tactical command-center")
 *
 * Primary browse/open view on the /workspaces hub, beside the Tree management
 * view. Loaded as a
 * plain deferred script (not an ES module); exposes itself on
 * window.OriWorkspaceMap, which workspace-hub.js calls when the "map" view is
 * active.
 *
 * Phase 3 = selecting a tile populates the Overview panel (entry agent, roster,
 * tasks, tools, skills) and Open Workspace navigates to the detail page.
 */
(function () {
  'use strict';

  // ---------- world coordinates ----------
  //
  // Everything the map draws is anchored in WORLD space: viewport-independent
  // logical units, not CSS pixels, not percentages of the current container, and
  // not grid column numbers (#292 FR-2). One world unit happens to equal one CSS
  // pixel at 100% zoom, which is what keeps the art, the spacing, and the
  // background grid identical to the pre-coordinate map — but nothing here reads
  // the container's width, so a resize can never move a building (FR-13).
  var CELL_W = 176;
  // Tiles measure ~155px tall (status flag, structure, name, ops mode, meta).
  // CELL_H was 150, so every row's meta line collided with the status flag of
  // the row below it. Keep a real gutter so each site reads as one unit and its
  // operational signals are unambiguous (PRD FR30).
  var CELL_H = 170;
  var PAD = 26;
  // FALLBACK_COLS replaces the old responsive `maxCols`. Automatic placement now
  // uses one fixed world-space rule so the same unplaced workspaces land on the
  // same logical coordinates on Home and on /workspaces, at any window size
  // (FR-19, FR-21).
  var FALLBACK_COLS = 5;
  // The district outline is drawn around its members with this much slack, and a
  // group's own anchor sits at the outline's corner rather than on a member's
  // cell — otherwise an empty group and its first member would want the same
  // point (FR-82, FR-84).
  var DISTRICT_PAD_X = 12;
  var DISTRICT_PAD_Y = 6;
  // Safe world bounds, mirroring internal/workspacemap/model.go. A coordinate
  // outside them is treated as unreadable and the record falls back to
  // automatic placement rather than being drawn somewhere impossible (FR-12).
  var MIN_COORD = -1000000;
  var MAX_COORD = 1000000;
  // Fallback margin used when the real viewport size is not known yet, so world
  // sizing stays deterministic in tests and during the first paint (FR-11).
  var DEFAULT_VIEWPORT = { width: 1100, height: 620 };
  var HQ_SITE_ID = '__personal_hq_site__';

  // ---------- camera ----------
  //
  // The camera is world-space: a point to look at plus a zoom level (FR-43).
  // Storing a scroll offset instead would tie the saved view to the container
  // it was saved from, so the same layout would open somewhere else on a
  // different window size or on the other Map surface.
  var MIN_ZOOM = 0.5;
  var MAX_ZOOM = 2;
  var DEFAULT_ZOOM = 1;
  // One press of Zoom In/Out. Small enough to feel like a step, large enough
  // that crossing the 50%–200% range does not take a dozen presses.
  var ZOOM_STEP = 1.25;
  // Wheel-zoom sensitivity, applied per normalized wheel notch.
  var WHEEL_ZOOM_RATE = 0.0022;
  // A press must travel this far before it becomes a pan rather than a click
  // (FR-33).
  var PAN_THRESHOLD = 4;
  // Comfortable breathing room around the content that Fit All frames (FR-40).
  var FIT_PADDING = 48;
  // The background grid's spacing at 100% zoom, shared with the CSS grid so a
  // snapped anchor always lands on a line the user can see (FR-58).
  var SNAP_STEP = 38;
  // Camera saves are debounced: a pan is hundreds of events and one intent
  // (FR-44).
  var CAMERA_SAVE_DELAY = 600;

  // ---------- current user's coordinate layout ----------
  //
  // One layout, shared by the Map on Home and the Map on /workspaces (FR-4).
  // It lives at module scope rather than per-mount because both surfaces load
  // the same script and must not each hold their own idea of where a building
  // is. The server record is authoritative: localStorage caches nothing
  // canonical here (FR-103).
  var LAYOUT_ENDPOINT = '/api/workspace-map/layout';
  var LAYOUT_LOAD_TIMEOUT_MS = 2500;

  var layoutState = {
    // idle → loading → ready | unavailable. "unavailable" is the honest
    // read-only state: deterministic fallback placement is still drawn and
    // still navigable, but nothing can be saved (FR-105).
    status: 'idle',
    positions: Object.create(null),
    viewport: null,
    snapToGrid: true,
    revision: 0,
    schemaVersion: 1
  };
  // Bumped for every layout request. A response whose sequence no longer
  // matches is a stale answer to a question nobody is asking any more, and is
  // dropped rather than painted over the current world.
  var layoutRequestSeq = 0;

  function isSafeCoordinate(value) {
    return typeof value === 'number' && isFinite(value) && value >= MIN_COORD && value <= MAX_COORD;
  }

  function safePoint(raw) {
    if (!raw || typeof raw !== 'object') return null;
    var x = Number(raw.x);
    var y = Number(raw.y);
    if (!isSafeCoordinate(x) || !isSafeCoordinate(y)) return null;
    return { x: x, y: y };
  }

  function safeViewport(raw) {
    if (!raw || typeof raw !== 'object') return null;
    var center = safePoint({ x: raw.center_x, y: raw.center_y });
    var zoom = Number(raw.zoom);
    if (!center || !isFinite(zoom) || zoom <= 0) return null;
    return { centerX: center.x, centerY: center.y, zoom: zoom };
  }

  // applyLayoutPayload degrades per field, never wholesale: one unreadable
  // anchor costs that building its saved spot and nothing else, and an
  // unreadable camera opens on the default view without hiding valid buildings
  // (FR-22, FR-45).
  function applyLayoutPayload(payload) {
    var positions = Object.create(null);
    var raw = (payload && payload.positions) || {};
    Object.keys(raw).forEach(function (id) {
      if (!id || id === HQ_SITE_ID) return;
      var point = safePoint(raw[id]);
      if (point) positions[id] = point;
    });
    layoutState.positions = positions;
    layoutState.viewport = safeViewport(payload && payload.viewport);
    layoutState.snapToGrid =
      payload && typeof payload.snap_to_grid === 'boolean' ? payload.snap_to_grid : true;
    layoutState.revision = Number((payload && payload.revision) || 0) || 0;
    layoutState.schemaVersion = Number((payload && payload.schema_version) || 1) || 1;
  }

  // ensureLayoutLoaded fetches the layout once per page. Both Map surfaces call
  // it, and whichever mounts first pays for it; the second reuses the result,
  // which is what makes the two surfaces agree by construction rather than by
  // coincidence (FR-4, FR-16).
  function ensureLayoutLoaded() {
    if (layoutState.status !== 'idle') return;
    if (typeof fetch !== 'function') {
      layoutState.status = 'unavailable';
      return;
    }
    layoutState.status = 'loading';
    var seq = ++layoutRequestSeq;
    // A request that never answers must not leave the world hidden forever.
    // After the deadline the map settles into its honest read-only state:
    // deterministic placement, navigable, nothing saveable (FR-105).
    if (typeof setTimeout === 'function') {
      var deadline = setTimeout(function () {
        if (seq !== layoutRequestSeq || layoutState.status !== 'loading') return;
        layoutState.status = 'unavailable';
        settleLayout();
      }, LAYOUT_LOAD_TIMEOUT_MS);
      if (deadline && typeof deadline.unref === 'function') deadline.unref();
    }
    fetch(LAYOUT_ENDPOINT, { headers: { Accept: 'application/json' } })
      .then(function (response) {
        if (!response || !response.ok) throw new Error('layout unavailable');
        return response.json();
      })
      .then(function (data) {
        if (seq !== layoutRequestSeq) return;
        applyLayoutPayload(data && data.layout);
        layoutState.status = 'ready';
        settleLayout();
      })
      .catch(function () {
        if (seq !== layoutRequestSeq) return;
        layoutState.status = 'unavailable';
        settleLayout();
      });
  }

  /**
   * Send one partial update and reconcile against what the server committed.
   *
   * The body carries operations, never a whole-layout snapshot, so a tab that
   * has been open for a while can save the one thing the user just did without
   * proposing values for anything else (FR-96, FR-101). The response — not the
   * request — is what updates local state, so the revision and coordinates the
   * client believes are always the ones actually stored (FR-102).
   */
  function patchLayout(operations) {
    if (typeof fetch !== 'function') {
      return Promise.reject(new Error('workspace map layout is unavailable'));
    }
    if (layoutState.status !== 'ready') {
      return Promise.reject(new Error('workspace map layout is read-only'));
    }
    return fetch(LAYOUT_ENDPOINT, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
      body: JSON.stringify({ operations: operations })
    })
      .then(function (response) {
        if (!response || !response.ok) throw new Error('workspace map layout save failed');
        return response.json();
      })
      .then(function (data) {
        var result = (data && data.result) || null;
        if (!result) throw new Error('workspace map layout save returned nothing');
        if (typeof result.revision === 'number') layoutState.revision = result.revision;
        if (typeof result.snap_to_grid === 'boolean') layoutState.snapToGrid = result.snap_to_grid;
        var viewport = safeViewport(result.viewport);
        if (viewport) layoutState.viewport = viewport;
        Object.keys(result.positions || {}).forEach(function (id) {
          var point = safePoint(result.positions[id]);
          if (point) layoutState.positions[id] = point;
        });
        return result;
      });
  }

  // settleLayout repaints the surface that is still mounted once the layout
  // resolves. It is the only place a load result reaches the DOM, so a response
  // arriving after the user switched to Tree, navigated away, or remounted is
  // simply dropped.
  function settleLayout() {
    if (!lastMount || !lastMount.container) return;
    if (lastMount.container.isConnected === false) return;
    mount(lastMount.container, lastMount.state);
  }

  // Currently-selected workspace id, remembered across re-mounts (data refreshes).
  var selectedId = '';

  // Cockpit mode flags, set from the mount options on every mount() so the
  // exported HTML builders (which keep their existing signatures for tests)
  // can describe the semantics that are actually bound.
  //
  // selectOnlyMode  — PRD FR35/FR36: a pointer click selects and never opens,
  //                   and there is no double-click opening rule. Home sets it;
  //                   the legacy /workspaces launcher does not.
  // hideChromeMode  — PRD FR15: the cockpit's persistent context rail owns the
  //                   overview and the workspace-area header owns the title,
  //                   stats, and New Workspace action, so the map must not
  //                   render a second copy of either.
  var selectOnlyMode = false;
  var hideChromeMode = false;

  // Personal HQ status is owned by personal-hq-onboarding.js so the status,
  // setup actions, and existing modals all share one source of truth. The map
  // renders either the real HQ badge or a reserved site that explains what is
  // missing without counting it as a workspace.
  var hqStatus = null;
  var hqWorkspaceId = null;
  var hqFocusConsumed = false;

  function hqSiteView(status) {
    if (!status || status.valid) return { show: false };
    var repair = !!status.workspace_id;
    var onboardingState = String(status.hq_onboarding_state || 'unseen');
    return {
      show: true,
      repair: repair,
      onboardingState: onboardingState,
      statusLabel: repair ? 'Needs repair' : 'Not created',
      title: repair ? 'Personal HQ needs repair' : 'Personal HQ has not been created',
      detail: repair
        ? 'The workspace previously designated as your Personal HQ is no longer available. Build a replacement or choose another workspace.'
        : 'Create a home base for your daily brief, follow-ups, and a clear place to resume work.',
      showSkip: !repair && onboardingState !== 'skipped'
    };
  }

  function hasHQFocusIntent() {
    if (!window.location || !window.location.search) return false;
    var params = new URLSearchParams(window.location.search);
    return params.get('focus') === 'personal-hq' || params.get('hq_onboarding') === '1';
  }

  function setHQStatus(status) {
    hqStatus = status || null;
    hqWorkspaceId = hqStatus && hqStatus.valid ? hqStatus.workspace_id : null;
    if (lastMount) mount(lastMount.container, lastMount.state);
  }

  // Ids checked for a bulk operation (multi-select), remembered across re-mounts.
  // Distinct from selectedId: selectedId drives the Overview preview, while this
  // set drives the multi-select action bar. Only plain workspace tiles (not
  // group districts) can be multi-selected.
  var multiSelected = Object.create(null);

  function multiIds() {
    return Object.keys(multiSelected);
  }
  function multiCount() {
    return multiIds().length;
  }

  // Curated cottage palettes, picked by stable id hash so a workspace keeps its
  // colour across refresh, add, and remove.
  //
  // Warm and earthy — plaster walls under a tiled roof — to match the cozy
  // visual language (PRD FR-3). Colour is decoration only: it is derived from
  // the workspace id and says nothing about status, which is carried by the
  // text flag and LED beside the tile (FR-6/FR-7).
  //
  // `wall`/`wallShade` are the two lit sides, `roof`/`roofShade` the two roof
  // planes, `trim` the door and window frame, `plot` the ground the cottage
  // sits on.
  var PALETTE = [
    // slate blue
    {
      key: '#6f96b8',
      plot: '#3a4a52',
      wall: '#cfd8de',
      wallShade: '#a8b6bf',
      roof: '#4d6f88',
      roofShade: '#3a566b',
      trim: '#2f4657'
    },
    // gold
    {
      key: '#d3a44a',
      plot: '#4d4126',
      wall: '#ecdfc2',
      wallShade: '#c8b795',
      roof: '#a8762c',
      roofShade: '#835b20',
      trim: '#5b3f16'
    },
    // clay
    {
      key: '#c0714c',
      plot: '#4c3428',
      wall: '#eddbcd',
      wallShade: '#c9b0a0',
      roof: '#a2543a',
      roofShade: '#7d3f2b',
      trim: '#57291c'
    },
    // plum
    {
      key: '#8f78ad',
      plot: '#3f3550',
      wall: '#ded6e6',
      wallShade: '#b8adc4',
      roof: '#6b568a',
      roofShade: '#52416b',
      trim: '#3a2d4d'
    },
    // moss
    {
      key: '#6a9a5f',
      plot: '#33452f',
      wall: '#dde6d3',
      wallShade: '#b5c2aa',
      roof: '#4c7444',
      roofShade: '#3a5a34',
      trim: '#2a4125'
    },
    // teal
    {
      key: '#5c9aa3',
      plot: '#2f474a',
      wall: '#d5e4e4',
      wallShade: '#adc2c3',
      roof: '#417078',
      roofShade: '#32565c',
      trim: '#24403f'
    }
  ];

  function escapeHtml(value) {
    return String(value == null ? '' : value).replace(/[&<>"']/g, function (c) {
      return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c];
    });
  }

  // FNV-1a — stable hash so a workspace keeps its spot across refresh/add/remove.
  function hashKey(id) {
    var h = 2166136261;
    var s = String(id || '');
    for (var i = 0; i < s.length; i++) {
      h ^= s.charCodeAt(i);
      h = Math.imul(h, 16777619);
    }
    return h >>> 0;
  }

  function isGroup(ws) {
    return (
      String((ws && ws.kind) || '')
        .trim()
        .toLowerCase() === 'group'
    );
  }

  function paletteFor(id) {
    return PALETTE[hashKey(id) % PALETTE.length];
  }

  function opsModeLabel(mode) {
    switch (
      String(mode || '')
        .trim()
        .toLowerCase()
    ) {
      case 'guided':
        return 'Guided';
      case 'direct':
        return 'Direct';
      case 'plan_then_execute':
        return 'Autonomous';
      case '':
        return '';
      default:
        return String(mode).charAt(0).toUpperCase() + String(mode).slice(1);
    }
  }

  function findWs(workspaces, id) {
    if (!id || !Array.isArray(workspaces)) return null;
    for (var i = 0; i < workspaces.length; i++) {
      if (workspaces[i] && workspaces[i].id === id) return workspaces[i];
    }
    return null;
  }

  function initials(name) {
    var parts = String(name || '')
      .trim()
      .split(/\s+/)
      .filter(Boolean);
    if (!parts.length) return '?';
    if (parts.length === 1) return parts[0].slice(0, 2).toUpperCase();
    return (parts[0][0] + parts[1][0]).toUpperCase();
  }

  // Default selection = most recently updated workspace (fallback: first present).
  function pickDefaultSelection(workspaces) {
    var list = (Array.isArray(workspaces) ? workspaces : []).filter(function (w) {
      return w && w.id;
    });
    if (!list.length) return '';
    var best = list[0];
    list.forEach(function (w) {
      if (String(w.updated_at || '') > String(best.updated_at || '')) best = w;
    });
    return best.id;
  }

  /**
   * Pure, stable, no-overlap arrangement in grid cells.
   *
   * This is no longer the map's layout — it is the *seed* for automatic
   * placement (FR-21). It decides the order and relative arrangement of records
   * that have never been placed by hand; computeWorldLayout turns those cells
   * into world coordinates and lets any saved anchor override them. The cell
   * ordering is keyed by a hash of each immutable ID, so it does not depend on
   * the order the API returned records in (FR-19).
   *
   * @returns {{tiles: Array, districts: Array, cols: number, rows: number}}
   */
  function computeMapLayout(workspaces, options) {
    var opts = options || {};
    var maxCols = Math.max(1, opts.maxCols || FALLBACK_COLS);
    var list = (Array.isArray(workspaces) ? workspaces : []).filter(function (w) {
      return w && w.id;
    });

    var byId = {};
    list.forEach(function (w) {
      byId[w.id] = w;
    });

    function parentGroupId(w) {
      var pid = (w && w.parent_id) || '';
      return pid && byId[pid] && isGroup(byId[pid]) ? pid : '';
    }
    function isTopGroup(w) {
      return isGroup(w) && parentGroupId(w) === '';
    }

    var membersByGroup = {};
    var roots = [];
    list.forEach(function (w) {
      var pid = parentGroupId(w);
      if (pid && isTopGroup(byId[pid])) {
        (membersByGroup[pid] = membersByGroup[pid] || []).push(w);
      } else {
        roots.push(w);
      }
    });

    function byHash(a, b) {
      var ha = hashKey(a.id),
        hb = hashKey(b.id);
      if (ha !== hb) return ha - hb;
      return a.id < b.id ? -1 : 1;
    }
    roots.sort(byHash);
    Object.keys(membersByGroup).forEach(function (g) {
      membersByGroup[g].sort(byHash);
    });

    // each root becomes an "island" with a cell footprint
    var islands = roots.map(function (w) {
      if (isGroup(w)) {
        var members = membersByGroup[w.id] || [];
        var n = Math.max(members.length, 1);
        var gc = Math.min(maxCols, Math.ceil(Math.sqrt(n)));
        var gr = Math.ceil(n / gc);
        return { kind: 'group', ws: w, members: members, w: gc, h: gr };
      }
      return { kind: 'ws', ws: w, members: [], w: 1, h: 1 };
    });

    // shelf-pack islands into a maxCols-wide grid (no overlap, clean rows)
    var tiles = [];
    var districts = [];
    var col = 0,
      shelfRow = 0,
      shelfHeight = 0,
      usedCols = 0,
      totalRows = 0;

    islands.forEach(function (isl) {
      var w = Math.min(isl.w, maxCols);
      if (col + w > maxCols) {
        shelfRow += shelfHeight;
        col = 0;
        shelfHeight = 0;
      }
      var baseCol = col,
        baseRow = shelfRow;
      if (isl.kind === 'group') {
        districts.push({ id: isl.ws.id, ws: isl.ws, col: baseCol, row: baseRow, w: w, h: isl.h });
        isl.members.forEach(function (m, i) {
          tiles.push({
            id: m.id,
            ws: m,
            groupId: isl.ws.id,
            col: baseCol + (i % w),
            row: baseRow + Math.floor(i / w)
          });
        });
      } else {
        tiles.push({ id: isl.ws.id, ws: isl.ws, groupId: '', col: baseCol, row: baseRow });
      }
      col += w;
      shelfHeight = Math.max(shelfHeight, isl.h);
      usedCols = Math.max(usedCols, col);
      totalRows = Math.max(totalRows, shelfRow + isl.h);
    });

    return {
      tiles: tiles,
      districts: districts,
      cols: Math.max(1, Math.min(maxCols, usedCols)),
      rows: Math.max(1, totalRows)
    };
  }

  // ---------- world placement ----------

  // Anchors are compared on a rounded key so "the same point" means the same
  // thing to the uniqueness check as it does to the eye. Exact float equality
  // would let two buildings sit a millionth of a unit apart and both claim to be
  // unique (FR-20).
  function anchorKey(point) {
    return Math.round(point.x * 2) / 2 + ',' + Math.round(point.y * 2) / 2;
  }

  /**
   * FallbackGrid hands out automatic anchors.
   *
   * It walks a fixed-width grid of world cells in a deterministic order and
   * skips any cell already claimed, so two records can never be handed the same
   * anchor and no building is ever placed under another one's hit target
   * (FR-20). The grid can be based anywhere: placing new records relative to the
   * content that already exists is what keeps an externally created workspace
   * visible instead of stranding it at a distant origin the user panned away
   * from years ago (FR-24).
   */
  function createFallbackGrid(origin) {
    var claimed = Object.create(null);
    var cursor = 0;
    function claim(point) {
      claimed[anchorKey(point)] = true;
      return point;
    }
    return {
      claim: claim,
      // cell translates a seed cell from computeMapLayout into world space. The
      // whole seeded arrangement moves with the origin, so its relative shape —
      // which records sit beside which — is preserved wherever it is based.
      cell: function (col, row, base) {
        var from = base || origin;
        return { x: from.x + col * CELL_W, y: from.y + row * CELL_H };
      },
      // next returns the next unclaimed cell, scanning row by row from the
      // grid's base. The cursor only moves forward, so assigning N records
      // costs one pass rather than N scans.
      next: function () {
        for (;;) {
          var col = cursor % FALLBACK_COLS;
          var row = Math.floor(cursor / FALLBACK_COLS);
          cursor += 1;
          var point = { x: origin.x + col * CELL_W, y: origin.y + row * CELL_H };
          if (!claimed[anchorKey(point)]) return claim(point);
        }
      },
      // place takes the record's preferred point, or the next free cell when
      // something already occupies it.
      place: function (point) {
        return claimed[anchorKey(point)] ? this.next() : claim(point);
      }
    };
  }

  // fallbackOrigin decides where automatic placement starts.
  //
  // With no saved anchors at all — a legacy map opened for the first time — it
  // is the classic (PAD, PAD) corner, so nothing appears to have moved (FR-18).
  // Once the user has placed anything, new records appear one row below their
  // arranged content instead of back at an origin they may have panned far away
  // from (FR-24).
  function fallbackOrigin(savedAnchors) {
    var ids = Object.keys(savedAnchors);
    if (!ids.length) return { x: PAD, y: PAD };
    var minX = Infinity;
    var maxY = -Infinity;
    ids.forEach(function (id) {
      var point = savedAnchors[id];
      if (point.x < minX) minX = point.x;
      if (point.y > maxY) maxY = point.y;
    });
    return { x: minX, y: maxY + CELL_H };
  }

  // groupAffinityOrigin places an unplaced member beneath the cluster its group
  // already occupies, so a workspace created into a moved district joins that
  // district rather than appearing across the world from it (FR-24).
  function groupAffinityOrigin(groupId, placedByGroup) {
    var cluster = placedByGroup[groupId];
    if (!cluster) return null;
    return { x: cluster.minX, y: cluster.maxY + CELL_H };
  }

  /**
   * Resolve every record's world anchor: saved coordinates win, everything else
   * gets a deterministic automatic one (FR-17, FR-18).
   *
   * Pure and viewport-independent by construction — nothing in here reads a
   * container size, a breakpoint, or the order the API happened to return
   * records in, which is what lets a resize, a filter, a Map/Tree switch, or a
   * data refresh leave every coordinate exactly where it was (FR-13, FR-19).
   *
   * @param {Array} workspaces enriched workspace summaries
   * @param {object} [options] { positions, viewport, hqSite }
   */
  function computeWorldLayout(workspaces, options) {
    var opts = options || {};
    var savedInput = opts.positions || {};
    var viewport = opts.viewport || DEFAULT_VIEWPORT;
    var grid = computeMapLayout(workspaces, { maxCols: FALLBACK_COLS });

    // 1. Keep only saved anchors that are safe numbers AND belong to a record
    //    this map is actually drawing. A coordinate for a trashed or deleted
    //    workspace stays in the layout record for restore, but it must not
    //    influence anything drawn now (FR-26).
    var present = Object.create(null);
    grid.tiles.forEach(function (tile) {
      present[tile.id] = true;
    });
    grid.districts.forEach(function (district) {
      present[district.id] = true;
    });
    var saved = Object.create(null);
    Object.keys(savedInput).forEach(function (id) {
      if (!present[id]) return;
      var point = safePoint(savedInput[id]);
      if (point) saved[id] = point;
    });

    var placer = createFallbackGrid(fallbackOrigin(saved));
    // Saved anchors are claimed before any automatic one is handed out, so
    // automatic placement can never land on top of a building the user placed.
    Object.keys(saved).forEach(function (id) {
      placer.claim(saved[id]);
    });

    // 2. Group anchors first: a member's automatic placement may want to sit
    //    near its district, which needs the district's anchor to exist.
    var districtAnchors = Object.create(null);
    grid.districts.forEach(function (district) {
      var anchor = saved[district.id];
      if (anchor) {
        // A saved district still occupies a cell, so claim the one it covers.
        // Otherwise the automatic scan would hand that cell to a building and
        // park it inside the outline (FR-20).
        placer.claim({ x: anchor.x + DISTRICT_PAD_X, y: anchor.y + DISTRICT_PAD_Y });
      } else {
        // The group's own anchor is the outline's corner, offset from the cell
        // it occupies — otherwise an empty group and its first member would
        // compete for the same point (FR-82). The cell is what gets claimed,
        // because that is the space the district actually covers.
        var cell = placer.place(placer.cell(district.col, district.row));
        anchor = { x: cell.x - DISTRICT_PAD_X, y: cell.y - DISTRICT_PAD_Y };
      }
      districtAnchors[district.id] = anchor;
    });

    // 3. Buildings. Saved wins; otherwise the seeded cell if it is free, else
    //    the next free cell in the deterministic scan.
    var placedByGroup = Object.create(null);
    function recordCluster(groupId, anchor) {
      if (!groupId) return;
      var cluster = placedByGroup[groupId];
      if (!cluster) {
        placedByGroup[groupId] = { minX: anchor.x, maxY: anchor.y };
        return;
      }
      if (anchor.x < cluster.minX) cluster.minX = anchor.x;
      if (anchor.y > cluster.maxY) cluster.maxY = anchor.y;
    }
    grid.tiles.forEach(function (tile) {
      if (saved[tile.id]) recordCluster(tile.groupId, saved[tile.id]);
    });

    var nodes = grid.tiles.map(function (tile) {
      var anchor = saved[tile.id];
      var isSaved = !!anchor;
      if (!anchor) {
        // A member of a group whose cluster already sits somewhere joins that
        // cluster; everything else takes its seeded cell in the fallback grid.
        var affinity = tile.groupId ? groupAffinityOrigin(tile.groupId, placedByGroup) : null;
        anchor = placer.place(
          affinity ? placer.cell(0, 0, affinity) : placer.cell(tile.col, tile.row)
        );
        recordCluster(tile.groupId, anchor);
      }
      return {
        id: tile.id,
        kind: 'workspace',
        ws: tile.ws,
        groupId: tile.groupId,
        x: anchor.x,
        y: anchor.y,
        saved: isSaved
      };
    });

    // 4. District outlines are elastic presentation derived from the anchors
    //    above. They never rewrite a member's coordinate; a member dragged past
    //    the old edge simply makes the outline grow (FR-84).
    var membersByGroup = Object.create(null);
    nodes.forEach(function (node) {
      if (!node.groupId) return;
      (membersByGroup[node.groupId] = membersByGroup[node.groupId] || []).push(node);
    });
    var districts = grid.districts.map(function (district) {
      var anchor = districtAnchors[district.id];
      var members = membersByGroup[district.id] || [];
      var minX = anchor.x;
      var minY = anchor.y;
      var maxX = anchor.x + CELL_W - 8;
      var maxY = anchor.y + CELL_H - 10;
      members.forEach(function (member) {
        var left = member.x - DISTRICT_PAD_X;
        var top = member.y - DISTRICT_PAD_Y;
        if (left < minX) minX = left;
        if (top < minY) minY = top;
        if (member.x + CELL_W - 8 > maxX) maxX = member.x + CELL_W - 8;
        if (member.y + CELL_H - 10 > maxY) maxY = member.y + CELL_H - 10;
      });
      return {
        id: district.id,
        ws: district.ws,
        anchorX: anchor.x,
        anchorY: anchor.y,
        x: minX,
        y: minY,
        width: Math.max(CELL_W - 8, maxX - minX),
        height: Math.max(CELL_H - 10, maxY - minY),
        saved: !!saved[district.id]
      };
    });

    // 5. The reserved Personal HQ site and the New Workspace pad get stable
    //    automatic anchors from the same scan, but they are never persisted:
    //    neither is a workspace, so neither may occupy a workspace ID in the
    //    saved layout (FR-30).
    var hqSite = opts.hqSite ? placer.next() : null;
    var pad = placer.next();

    var bounds = worldBounds(nodes, districts, hqSite, pad);
    return {
      nodes: nodes,
      districts: districts,
      hqSite: hqSite,
      pad: pad,
      bounds: bounds,
      world: expandWorld(bounds, viewport)
    };
  }

  // worldBounds is the tight box around everything currently drawn. It grows
  // automatically as content is placed further out, and it never adjusts a
  // coordinate to fit (FR-10).
  function worldBounds(nodes, districts, hqSite, pad) {
    var minX = Infinity;
    var minY = Infinity;
    var maxX = -Infinity;
    var maxY = -Infinity;
    function include(x, y, w, h) {
      if (x < minX) minX = x;
      if (y < minY) minY = y;
      if (x + w > maxX) maxX = x + w;
      if (y + h > maxY) maxY = y + h;
    }
    nodes.forEach(function (node) {
      include(node.x, node.y, CELL_W, CELL_H);
    });
    districts.forEach(function (district) {
      include(district.x, district.y, district.width, district.height);
    });
    if (hqSite) include(hqSite.x, hqSite.y, CELL_W, CELL_H);
    if (pad) include(pad.x, pad.y, CELL_W, CELL_H);
    if (minX === Infinity) {
      return { minX: 0, minY: 0, maxX: CELL_W, maxY: CELL_H };
    }
    return { minX: minX, minY: minY, maxX: maxX, maxY: maxY };
  }

  // expandWorld adds at least one viewport of navigable margin around the
  // outermost content, so there is always somewhere to pan to and somewhere to
  // build near an edge (FR-11).
  function expandWorld(bounds, viewport) {
    var marginX = Math.max(CELL_W, (viewport && viewport.width) || DEFAULT_VIEWPORT.width);
    var marginY = Math.max(CELL_H, (viewport && viewport.height) || DEFAULT_VIEWPORT.height);
    return {
      minX: bounds.minX - marginX,
      minY: bounds.minY - marginY,
      maxX: bounds.maxX + marginX,
      maxY: bounds.maxY + marginY,
      width: bounds.maxX - bounds.minX + marginX * 2,
      height: bounds.maxY - bounds.minY + marginY * 2
    };
  }

  // ---------- camera transforms ----------
  //
  // Every one of these is pure: camera in, camera out, no DOM. That is what
  // lets pointer-centred zoom, Fit All, and clamping be asserted exactly rather
  // than eyeballed in a browser (FR-123).

  function clampZoom(zoom) {
    if (typeof zoom !== 'number' || !isFinite(zoom)) return DEFAULT_ZOOM;
    return Math.min(MAX_ZOOM, Math.max(MIN_ZOOM, zoom));
  }

  /** Convert a point in viewport CSS pixels into world units. */
  function screenToWorld(point, cam, viewport) {
    return {
      x: (point.x - viewport.width / 2) / cam.zoom + cam.centerX,
      y: (point.y - viewport.height / 2) / cam.zoom + cam.centerY
    };
  }

  /** Convert a world point into viewport CSS pixels. */
  function worldToScreen(point, cam, viewport) {
    return {
      x: (point.x - cam.centerX) * cam.zoom + viewport.width / 2,
      y: (point.y - cam.centerY) * cam.zoom + viewport.height / 2
    };
  }

  /**
   * Zoom about a screen point, keeping the world point under it visually still
   * (FR-39). This is what makes wheel and pinch zoom feel like the map is being
   * pulled toward the cursor rather than sliding out from under it.
   */
  function zoomAroundPoint(cam, viewport, screenPoint, factor) {
    var zoom = clampZoom(cam.zoom * factor);
    var world = screenToWorld(screenPoint, cam, viewport);
    return {
      centerX: world.x - (screenPoint.x - viewport.width / 2) / zoom,
      centerY: world.y - (screenPoint.y - viewport.height / 2) / zoom,
      zoom: zoom
    };
  }

  /**
   * Zoom about the viewport centre (FR-39). Buttons and keyboard use this: with
   * no pointer there is no "point under the cursor" to preserve, and moving the
   * centre would make the map drift every time someone pressed Zoom In.
   */
  function zoomAroundCenter(cam, factor) {
    return { centerX: cam.centerX, centerY: cam.centerY, zoom: clampZoom(cam.zoom * factor) };
  }

  /** The camera that frames everything in `bounds` with padding (FR-40). */
  function fitBounds(bounds, viewport, padding) {
    var pad = typeof padding === 'number' ? padding : FIT_PADDING;
    var width = Math.max(1, bounds.maxX - bounds.minX);
    var height = Math.max(1, bounds.maxY - bounds.minY);
    var usableWidth = Math.max(1, (viewport.width || DEFAULT_VIEWPORT.width) - pad * 2);
    var usableHeight = Math.max(1, (viewport.height || DEFAULT_VIEWPORT.height) - pad * 2);
    return {
      centerX: (bounds.minX + bounds.maxX) / 2,
      centerY: (bounds.minY + bounds.maxY) / 2,
      zoom: clampZoom(Math.min(usableWidth / width, usableHeight / height))
    };
  }

  /** Look at one node without moving it (FR-41). */
  function centerOn(cam, point) {
    return { centerX: point.x + CELL_W / 2, centerY: point.y + CELL_H / 2, zoom: cam.zoom };
  }

  /**
   * Keep the camera inside the navigable world.
   *
   * Panning may roam a viewport past the outermost building — that margin is
   * what makes it possible to build near an edge (FR-11) — but not further, so
   * it is never possible to end up in empty space with no content in any
   * direction and no idea which way to go back.
   */
  function clampCamera(cam, world) {
    return {
      centerX: Math.min(Math.max(cam.centerX, world.minX), world.maxX),
      centerY: Math.min(Math.max(cam.centerY, world.minY), world.maxY),
      zoom: clampZoom(cam.zoom)
    };
  }

  // ---------- camera state ----------

  var camera = { centerX: 0, centerY: 0, zoom: DEFAULT_ZOOM };
  // Until the layout has settled and the container has a real size, there is
  // nothing meaningful to frame; the first honest measurement initializes the
  // camera once and every later mount reuses it (FR-106).
  var cameraReady = false;
  // The most recent computed world, held so the camera controls can fit, clamp,
  // and centre without recomputing placement.
  var lastWorldLayout = null;
  var cameraSaveTimer = null;

  function viewportSize(canvas) {
    var width = (canvas && canvas.clientWidth) || 0;
    var height = (canvas && canvas.clientHeight) || 0;
    if (width <= 0 || height <= 0) return DEFAULT_VIEWPORT;
    return { width: width, height: height };
  }

  function setCamera(next, container) {
    var world = lastWorldLayout ? lastWorldLayout.world : null;
    camera = world
      ? clampCamera(next, world)
      : { centerX: next.centerX, centerY: next.centerY, zoom: clampZoom(next.zoom) };
    applyCamera(container);
    scheduleCameraSave();
  }

  /**
   * Push the camera into the DOM.
   *
   * One transform on the world layer moves every building at once, so panning
   * and zooming never re-render the map or touch a node's coordinates
   * (FR-31, FR-122). The background grid is offset and scaled by the same
   * camera so it stays welded to world space instead of sliding under it.
   */
  function applyCamera(container) {
    if (!container || typeof container.querySelector !== 'function') return;
    var canvas = container.querySelector('.ws-map-canvas');
    if (!canvas) return;
    var world = canvas.querySelector('.ws-map-world');
    var viewport = viewportSize(canvas);
    var translateX = viewport.width / 2 - camera.centerX * camera.zoom;
    var translateY = viewport.height / 2 - camera.centerY * camera.zoom;
    if (world && world.style) {
      world.style.transform =
        'translate(' + translateX + 'px, ' + translateY + 'px) scale(' + camera.zoom + ')';
    }
    if (canvas.style && typeof canvas.style.setProperty === 'function') {
      var grid = SNAP_STEP * camera.zoom;
      canvas.style.setProperty('--ws-map-grid', grid + 'px');
      canvas.style.setProperty('--ws-map-grid-x', (translateX % grid) + 'px');
      canvas.style.setProperty('--ws-map-grid-y', (translateY % grid) + 'px');
    }
    updateCameraControls(container);
  }

  // scheduleCameraSave persists the settled camera, debounced. A pan is
  // hundreds of events and one intention (FR-44), and a camera that fails to
  // save is a lost view — never a lost building (FR-108).
  function scheduleCameraSave() {
    if (layoutState.status !== 'ready') return;
    if (typeof setTimeout !== 'function') return;
    if (cameraSaveTimer) clearTimeout(cameraSaveTimer);
    cameraSaveTimer = setTimeout(function () {
      cameraSaveTimer = null;
      patchLayout([
        {
          op: 'set_viewport',
          viewport: { center_x: camera.centerX, center_y: camera.centerY, zoom: camera.zoom }
        }
      ]).catch(function () {
        // Best-effort by design: committed positions are untouched either way.
      });
    }, CAMERA_SAVE_DELAY);
    if (cameraSaveTimer && typeof cameraSaveTimer.unref === 'function') cameraSaveTimer.unref();
  }

  // ensureCamera initializes the view exactly once: from the user's saved
  // camera when there is a usable one, and otherwise from Fit All, so a missing
  // or unreadable viewport opens on something sensible rather than leaving
  // valid buildings off-screen (FR-45).
  function ensureCamera(container) {
    if (cameraReady || !lastWorldLayout) return;
    if (layoutState.status === 'loading') return;
    var stored = layoutState.viewport;
    if (stored) {
      camera = { centerX: stored.centerX, centerY: stored.centerY, zoom: clampZoom(stored.zoom) };
      cameraReady = true;
      return;
    }
    // Wait for something worth framing. The first mount can happen before the
    // workspace list arrives, and framing the bare create pad would lock the
    // camera onto an empty corner that every later refresh then inherits.
    if (!lastWorldLayout.nodes.length && !lastWorldLayout.districts.length) return;
    var canvas = container && container.querySelector && container.querySelector('.ws-map-canvas');
    var fitted = fitBounds(lastWorldLayout.bounds, viewportSize(canvas));
    // Fit All zooms in when there is little content; the opening view does not.
    // Landing at 200% on a two-workspace map is disorienting, and the button is
    // right there for anyone who wants it.
    camera = {
      centerX: fitted.centerX,
      centerY: fitted.centerY,
      zoom: Math.min(DEFAULT_ZOOM, fitted.zoom)
    };
    cameraReady = true;
  }

  // ---------- rendering ----------

  function computeStats(workspaces) {
    var list = Array.isArray(workspaces) ? workspaces : [];
    var agents = 0,
      openTasks = 0,
      groups = 0;
    list.forEach(function (ws) {
      agents += Number((ws && ws.agent_count) || 0);
      openTasks += Number((ws && ws.open_task_count) || 0);
      if (isGroup(ws)) groups += 1;
    });
    return { workspaces: list.length, agents: agents, openTasks: openTasks, groups: groups };
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

  // One workspace, drawn as a cottage on its own plot.
  //
  // Isometric, viewed corner-on: two plaster walls below, a hip roof above
  // meeting at a front ridge, with a door and a shuttered window. The footprint
  // and viewBox are unchanged from the block it replaces, so tile geometry,
  // spacing, and the selection ellipse all still line up (PRD FR-3/FR-9).
  //
  // Nothing here encodes state. The building never changes with activity,
  // attention, or setup — those are carried by the text flag and LED beside the
  // tile, so a lit window can never imply an agent is working (FR-6/FR-7).
  function structSVG(pal) {
    return (
      '<svg class="ws-map-struct" width="112" height="92" viewBox="0 0 118 96" aria-hidden="true">' +
      // plot
      '<polygon points="59,86 110,60 59,34 8,60" fill="' +
      pal.plot +
      '" stroke="' +
      pal.key +
      '" stroke-opacity=".45"/>' +
      // left wall (shaded) and right wall (lit)
      '<polygon points="34,40 34,54 59,68 59,54" fill="' +
      pal.wallShade +
      '"/>' +
      '<polygon points="84,40 84,54 59,68 59,54" fill="' +
      pal.wall +
      '"/>' +
      // hip roof: two visible planes meeting at the front ridge
      '<polygon points="34,40 59,54 59,16" fill="' +
      pal.roofShade +
      '"/>' +
      '<polygon points="84,40 59,54 59,16" fill="' +
      pal.roof +
      '"/>' +
      // roof eaves, a thin lip that reads as overhang at small sizes
      '<polyline points="34,40 59,54 84,40" fill="none" stroke="' +
      pal.trim +
      '" stroke-opacity=".55" stroke-width="1.5"/>' +
      // door on the shaded wall
      '<polygon points="42,48 50,52 50,63 42,59" fill="' +
      pal.trim +
      '"/>' +
      // shuttered window on the lit wall
      '<polygon points="66,52 74,48 74,56 66,60" fill="' +
      pal.trim +
      '" fill-opacity=".85"/>' +
      // chimney
      '<polygon points="70,25 76,28 76,36 70,33" fill="' +
      pal.roofShade +
      '"/>' +
      '<polygon points="70,25 76,28 79,26 73,23" fill="' +
      pal.trim +
      '"/>' +
      '</svg>'
    );
  }

  // Personal HQ landmark: a grander gold citadel — a wide base block topped by a
  // central tower, beacon spire, and pennant — so the designated HQ reads as a
  // capital building on the map, not just another tile. Fixed gold palette
  // (independent of the per-workspace hash) so it's instantly recognizable.
  function structSVGHQ() {
    return (
      '<svg class="ws-map-struct is-hq-struct" width="118" height="104" viewBox="0 0 118 110" aria-hidden="true">' +
      // ground pad
      '<polygon points="59,64 109,86 59,108 9,86" fill="#2a2109" stroke="#e8b54b" stroke-opacity=".55"/>' +
      // base block
      '<polygon points="19,72 19,88 59,108 59,92" fill="#4c3c17"/>' +
      '<polygon points="99,72 99,88 59,108 59,92" fill="#372c11"/>' +
      '<polygon points="59,52 99,72 59,92 19,72" fill="#7a5f22" stroke="#e8b54b" stroke-opacity=".35"/>' +
      // tower walls
      '<polygon points="37,40 37,68 59,79 59,51" fill="#9c7a2c"/>' +
      '<polygon points="81,40 81,68 59,79 59,51" fill="#785e22"/>' +
      // tower windows (lit)
      '<circle cx="46" cy="53" r="1.5" fill="#ffe08a"/>' +
      '<circle cx="46" cy="62" r="1.5" fill="#ffe08a"/>' +
      '<circle cx="72" cy="53" r="1.5" fill="#ffe08a"/>' +
      '<circle cx="72" cy="62" r="1.5" fill="#ffe08a"/>' +
      // gold roof
      '<polygon points="59,29 81,40 59,51 37,40" fill="#f2c85c" stroke="#ffe08a" stroke-opacity=".5"/>' +
      // spire + pennant + beacon
      '<line x1="59" y1="29" x2="59" y2="10" stroke="#ffd469" stroke-width="2.4" stroke-linecap="round"/>' +
      '<polygon points="59,12 74,17 59,22" fill="#ffd469"/>' +
      '<circle cx="59" cy="9" r="3.4" fill="#ffe08a" stroke="#ffd469"/>' +
      '</svg>'
    );
  }

  function structSVGHQSite() {
    return (
      '<svg class="ws-map-struct is-hq-site-struct" viewBox="0 0 120 78" aria-hidden="true">' +
      '<polygon class="ws-map-hq-site-pad" points="60,8 111,34 60,60 9,34"/>' +
      '<path class="ws-map-hq-site-outline" d="M36 38V26l24-12 24 12v12M43 42h34M48 50h24"/>' +
      '<path class="ws-map-hq-site-survey" d="M18 31h12M90 31h12M60 57v12"/>' +
      '<circle class="ws-map-hq-site-beacon" cx="60" cy="14" r="3"/>' +
      '</svg>'
    );
  }

  function tileHTML(tile, selectedId, index) {
    var ws = tile.ws || {};
    var pal = paletteFor(ws.id);
    var active = !!ws.active;
    var statusText = active ? 'Working' : 'Idle';
    var agents = Number(ws.agent_count || 0);
    var openTasks = Number(ws.open_task_count || 0);
    var mode = opsModeLabel(ws.ops_mode);
    var hasKeeper = String(ws.entry_agent_name || '').trim() !== '';
    var isHQ = !!hqWorkspaceId && ws.id === hqWorkspaceId;
    var isSel = selectedId && ws.id === selectedId;
    var selected = isSel ? ' is-selected' : '';
    var isMulti = !!multiSelected[ws.id];
    var multiCls = isMulti ? ' is-multi' : '';
    // Layer coordinates: the node's world anchor with the world layer's origin
    // already subtracted by canvasHTML. The building itself knows only where it
    // sits in the world; nothing here reads the container (FR-2).
    var left = Number(tile.left) || 0;
    var top = Number(tile.top) || 0;
    var meta =
      agents +
      (agents === 1 ? ' agent' : ' agents') +
      ' · ' +
      openTasks +
      (openTasks === 1 ? ' task' : ' tasks');
    // The action hint must describe the semantics actually bound (see
    // bindTiles). In select-only mode (the Home cockpit, PRD FR35/FR36/FR125)
    // a pointer click and Space never open — only Enter or the rail's explicit
    // Open Workspace action do. The legacy launcher keeps its select-then-open
    // behavior until /workspaces redirects to Home.
    var actionHint = selectOnlyMode
      ? isSel
        ? '. Selected — press Enter to open'
        : '. Activate to select, Enter to open'
      : isSel
        ? '. Selected — activate to open'
        : '. Activate to select, double-click to open';

    return (
      '<button type="button" class="ws-map-tile' +
      selected +
      multiCls +
      (isHQ ? ' is-hq' : '') +
      '" ' +
      'data-ws-id="' +
      escapeHtml(ws.id) +
      '" ' +
      'aria-pressed="' +
      (isSel ? 'true' : 'false') +
      '" ' +
      'style="left:' +
      left +
      'px;top:' +
      top +
      'px;--i:' +
      (index || 0) +
      '" ' +
      'aria-label="' +
      escapeHtml(ws.name || 'Workspace') +
      ', ' +
      meta +
      ', ' +
      statusText +
      (hasKeeper ? ', entry agent ' + escapeHtml(ws.entry_agent_name) : '') +
      (isHQ ? ', Personal HQ' : '') +
      actionHint +
      '">' +
      '<span class="ws-map-tile-check" data-ws-check role="checkbox" tabindex="-1" ' +
      'aria-checked="' +
      (isMulti ? 'true' : 'false') +
      '" ' +
      'aria-label="Select for bulk action" title="Select"></span>' +
      '<span class="ws-map-tile-flag"><span class="ws-map-led' +
      (active ? ' is-working' : '') +
      '"></span>' +
      escapeHtml(statusText) +
      '</span>' +
      (hasKeeper ? '<span class="ws-map-tile-crest" title="Commander (locked)">★</span>' : '') +
      (isHQ ? '<span class="ws-map-tile-hq-badge" title="Personal HQ">HQ</span>' : '') +
      (isHQ ? structSVGHQ() : structSVG(pal)) +
      '<span class="ws-map-tile-name">' +
      escapeHtml(ws.name || 'Workspace') +
      '</span>' +
      (mode ? '<span class="ws-map-tile-type">' + escapeHtml(mode) + '</span>' : '') +
      '<span class="ws-map-tile-meta">' +
      escapeHtml(meta) +
      '</span>' +
      '</button>'
    );
  }

  function hqSiteHTML(cell, selectedId, index, view) {
    var isSelected = selectedId === HQ_SITE_ID;
    var left = Number(cell && cell.left) || 0;
    var top = Number(cell && cell.top) || 0;
    var status = view.statusLabel || 'Not created';
    return (
      '<button type="button" class="ws-map-tile ws-map-hq-site' +
      (isSelected ? ' is-selected' : '') +
      (view.repair ? ' is-repair' : '') +
      '" ' +
      'data-hq-site aria-pressed="' +
      (isSelected ? 'true' : 'false') +
      '" ' +
      'style="left:' +
      left +
      'px;top:' +
      top +
      'px;--i:' +
      (index || 0) +
      '" ' +
      'aria-label="Personal HQ site, ' +
      escapeHtml(status) +
      '. Activate to view setup options">' +
      '<span class="ws-map-tile-flag"><span class="ws-map-led"></span>' +
      escapeHtml(status) +
      '</span>' +
      '<span class="ws-map-tile-hq-badge" title="Personal HQ site">HQ site</span>' +
      structSVGHQSite() +
      '<span class="ws-map-tile-name">Personal HQ</span>' +
      '<span class="ws-map-tile-type">Personal</span>' +
      '<span class="ws-map-tile-meta">' +
      escapeHtml(status) +
      '</span>' +
      '</button>'
    );
  }

  function districtHTML(d, selectedId) {
    var ws = d.ws || {};
    // A selected GROUP must keep its highlight across a re-mount, not only
    // until the next applySelection call (PRD FR58).
    var isSel = !!selectedId && ws.id === selectedId;
    // Elastic bounds computed by computeWorldLayout from the group's anchor and
    // its members' anchors. The outline is presentation only — it grows around
    // a member that moved and never claims that geometry changed membership
    // (FR-84, FR-8).
    var left = Number(d.left) || 0;
    var top = Number(d.top) || 0;
    var width = Number(d.width) || CELL_W;
    var height = Number(d.height) || CELL_H;
    var label = (ws.name || 'Group') + ' group';
    return (
      '<div class="ws-map-district" role="group" aria-label="' +
      escapeHtml(label) +
      '" ' +
      'data-group-id="' +
      escapeHtml(ws.id) +
      '" ' +
      'style="left:' +
      left +
      'px;top:' +
      top +
      'px;width:' +
      width +
      'px;height:' +
      height +
      'px">' +
      '<button type="button" class="ws-map-district-tag' +
      (isSel ? ' is-selected' : '') +
      '" data-ws-id="' +
      escapeHtml(ws.id) +
      '" ' +
      'aria-pressed="' +
      (isSel ? 'true' : 'false') +
      '" ' +
      'aria-label="' +
      escapeHtml('Open ' + label) +
      '">▢ ' +
      escapeHtml(ws.name || 'Group') +
      ' · Group</button>' +
      '</div>'
    );
  }

  function padHTML(left, top) {
    return (
      '<button type="button" class="ws-map-pad" data-ws-map-create ' +
      'style="left:' +
      left +
      'px;top:' +
      top +
      'px" aria-label="Create a new workspace">' +
      '<span class="ws-map-pad-plate">＋</span><span class="ws-map-pad-label">New workspace</span>' +
      '</button>'
    );
  }

  /**
   * Draw the world.
   *
   * The canvas is the viewport; the layer inside it holds every building at its
   * world anchor with the layer's origin subtracted. Keeping that one
   * subtraction in a single place is what lets the same anchors be rendered by
   * a scrolled layer today and by a panned/zoomed camera later without touching
   * a single node coordinate.
   */
  function canvasHTML(workspaces, selectedId, options) {
    var opts = options || {};
    var site = hqSiteView(hqStatus);
    var layout = computeWorldLayout(workspaces, {
      positions: layoutState.positions,
      viewport: opts.viewport,
      hqSite: site.show
    });
    // Nodes carry their raw world coordinates into the DOM; the world layer's
    // camera transform is the only thing standing between world space and the
    // screen (FR-31). Nothing here subtracts an origin, so panning and zooming
    // move one element rather than rewriting every position.
    lastWorldLayout = layout;
    function toLayer(point) {
      return { left: point.x, top: point.y };
    }

    var parts = [];
    layout.districts.forEach(function (district) {
      var placed = toLayer(district);
      parts.push(
        districtHTML(
          {
            ws: district.ws,
            left: placed.left,
            top: placed.top,
            width: district.width,
            height: district.height
          },
          selectedId
        )
      );
    });
    layout.nodes.forEach(function (node, index) {
      var placed = toLayer(node);
      parts.push(tileHTML({ ws: node.ws, left: placed.left, top: placed.top }, selectedId, index));
    });
    if (layout.hqSite) {
      parts.push(hqSiteHTML(toLayer(layout.hqSite), selectedId, layout.nodes.length, site));
    }
    // Keep the ordinary create affordance after all real and reserved sites.
    var pad = toLayer(layout.pad);
    parts.push(padHTML(pad.left, pad.top));
    // The build-site marker lives in world space so it tracks the candidate
    // coordinate through pans and zooms rather than floating over the screen.
    parts.push(
      '<div class="ws-map-build-site" data-map-build-marker hidden aria-hidden="true"></div>'
    );

    var settling = layoutState.status === 'loading' ? ' is-settling' : '';
    var readOnly = layoutState.status === 'unavailable' ? ' is-readonly' : '';
    return {
      html:
        '<div class="ws-map-canvas' +
        settling +
        readOnly +
        '" role="group" aria-label="Workspaces map" tabindex="0"' +
        (settling ? ' aria-busy="true"' : '') +
        ' data-ws-map-viewport>' +
        '<div class="ws-map-world">' +
        parts.join('') +
        '</div></div>' +
        cameraControlsHTML(),
      empty: layout.nodes.length === 0,
      layout: layout
    };
  }

  /**
   * Visible camera controls.
   *
   * Every navigation gesture the map supports by pointer also has a button
   * here, so a wheel, a trackpad, and a steady hand are conveniences rather
   * than requirements (FR-37, FR-115). The three framing actions are separate
   * and named for what they do: Fit All changes the zoom to show everything,
   * Center Selected moves the view to one record, Reset View returns to the
   * default view. None of them moves a building — that distinction is what
   * keeps Reset View from being mistaken for Reset Layout (FR-42, FR-109).
   */
  function cameraControlsHTML() {
    var snapOn = layoutState.snapToGrid;
    var readOnly = layoutState.status !== 'ready';
    return (
      buildBannerHTML() +
      '<div class="ws-map-controls" role="group" aria-label="Map view controls">' +
      '<button type="button" class="ws-map-ctl" data-map-zoom-out aria-label="Zoom out">−</button>' +
      '<span class="ws-map-zoom" data-map-zoom-readout aria-hidden="true">100%</span>' +
      '<button type="button" class="ws-map-ctl" data-map-zoom-in aria-label="Zoom in">+</button>' +
      '<button type="button" class="ws-map-ctl ws-map-ctl--wide" data-map-fit>Fit all</button>' +
      '<button type="button" class="ws-map-ctl ws-map-ctl--wide" data-map-center>Center selected</button>' +
      '<button type="button" class="ws-map-ctl ws-map-ctl--wide" data-map-reset-view>Reset view</button>' +
      '<button type="button" class="ws-map-ctl ws-map-ctl--wide ws-map-ctl--build" data-map-build' +
      (readOnly ? ' disabled' : '') +
      '>⊕ Build workspace</button>' +
      '<button type="button" class="ws-map-ctl ws-map-ctl--wide" data-map-snap aria-pressed="' +
      (snapOn ? 'true' : 'false') +
      '"' +
      (readOnly ? ' disabled' : '') +
      '>Snap to grid: ' +
      (snapOn ? 'on' : 'off') +
      '</button>' +
      '</div>' +
      '<p class="ws-map-live" data-map-live role="status" aria-live="polite"></p>'
    );
  }

  /**
   * The placement-mode banner.
   *
   * Build mode has to be unmistakable — it changes what the next click on empty
   * space means, and a mode the user cannot see is a mode they will trigger by
   * accident (FR-48, FR-49). The banner names the mode, shows the candidate
   * coordinate and whether snapping is applied (FR-50), and carries its own
   * cancel action so leaving does not depend on knowing about Escape.
   *
   * It is rendered on every mount and hidden, so entering the mode is a class
   * change rather than a re-render that would disturb the map underneath.
   */
  function buildBannerHTML() {
    return (
      '<div class="ws-map-build" data-map-build-banner hidden>' +
      '<span class="ws-map-build-dot" aria-hidden="true">◎</span>' +
      '<span class="ws-map-build-text">Choose where to build — click an empty spot, or use the arrow keys and Enter. Escape cancels.</span>' +
      '<span class="ws-map-build-coords" data-map-build-coords></span>' +
      '<button type="button" class="ws-map-ctl" data-map-build-cancel>Cancel</button>' +
      '</div>'
    );
  }

  function emptyCanvasHTML() {
    return (
      '<div class="ws-map-canvas ws-map-canvas--empty">' +
      '<div class="ws-map-empty-cluster">' +
      '<button type="button" class="ws-map-pad ws-map-pad--hero" data-ws-map-create aria-label="Create a new workspace">' +
      '<span class="ws-map-pad-plate">＋</span><span class="ws-map-pad-label">New workspace</span></button>' +
      '<div class="ws-map-empty-note">No workspaces yet — build your first one.</div>' +
      '</div></div>'
    );
  }

  // Contextual action bar for the multi-select set. Rendered inside the theatre
  // and shown only while at least one tile is checked. Count text is refreshed
  // in place by updateSelBar so toggling selection never re-mounts the map.
  function selBarHTML() {
    var n = multiCount();
    return (
      '<div class="ws-map-selbar" data-ws-selbar' +
      (n ? '' : ' hidden') +
      '>' +
      '<span class="ws-map-selbar-count" data-ws-selbar-count>' +
      n +
      ' selected</span>' +
      '<div class="ws-map-selbar-actions">' +
      '<button type="button" class="ws-map-selbar-group" data-ws-selbar-group>⊕ Group</button>' +
      '<button type="button" class="ws-map-selbar-del" data-ws-selbar-delete>✕ Delete</button>' +
      '<button type="button" class="ws-map-selbar-clear" data-ws-selbar-clear>Clear</button>' +
      '</div>' +
      '</div>'
    );
  }

  function avatarHTML(name, extraClass) {
    var pal = paletteFor(name);
    return (
      '<span class="ws-map-av' +
      (extraClass ? ' ' + extraClass : '') +
      '" style="--av:' +
      pal.key +
      '" title="' +
      escapeHtml(name) +
      '">' +
      escapeHtml(initials(name)) +
      '</span>'
    );
  }

  function mapMetadata(options) {
    return options && options.metadata && typeof options.metadata === 'object'
      ? options.metadata
      : {};
  }

  function metadataValue(options, key, id) {
    var metadata = mapMetadata(options);
    var table = metadata && metadata[key];
    return table && id ? table[id] : null;
  }

  function normalizeTags(tags) {
    return (Array.isArray(tags) ? tags : [])
      .map(function (tag) {
        return String(tag || '').trim();
      })
      .filter(Boolean);
  }

  function folderDisplayFor(ws, options) {
    if (!ws || !ws.id) return null;
    return (
      metadataValue(options, 'folderDisplayById', ws.id) ||
      ws.map_folder_display ||
      ws.folder_display ||
      null
    );
  }

  function tagsFor(ws, options) {
    if (!ws || !ws.id) return [];
    return normalizeTags(metadataValue(options, 'tagsById', ws.id) || ws.tags);
  }

  function groupPreviewFor(ws, options) {
    if (!ws || !ws.id || !isGroup(ws)) return null;
    var fromMetadata = metadataValue(options, 'groupPreviewById', ws.id);
    if (fromMetadata) return fromMetadata;

    var children = Array.isArray(ws.children) ? ws.children : [];
    var names = children
      .slice(0, 3)
      .map(function (child) {
        return String((child && (child.name || child.id)) || 'Untitled Workspace').trim();
      })
      .filter(Boolean);
    return {
      childCount: children.length,
      previewNames: names,
      overflowCount: Math.max(0, children.length - names.length)
    };
  }

  function folderOverviewHTML(ws, options) {
    var folder = folderDisplayFor(ws, options);
    if (!folder) return '';
    var badgeClass = folder.badgeClass || (folder.linked ? 'is-linked' : 'is-unlinked');
    var badge = folder.badgeLabel || (folder.linked ? 'Linked folder' : 'No folder linked');
    var detail = folder.detail || (folder.linked ? 'Linked folder' : 'No local folder attached.');
    var title = folder.detailTitle || detail;
    return (
      '<div class="ws-map-ov-label">Folder</div>' +
      '<div class="ws-map-folder ' +
      escapeHtml(badgeClass) +
      '">' +
      '<span class="ws-map-folder-badge">' +
      escapeHtml(badge) +
      '</span>' +
      '<span class="ws-map-folder-path" title="' +
      escapeHtml(title) +
      '">' +
      escapeHtml(detail) +
      '</span>' +
      '</div>'
    );
  }

  function tagsOverviewHTML(ws, options) {
    var tags = tagsFor(ws, options);
    var workspaceId = ws && ws.id ? String(ws.id) : '';
    var limit = 4;
    var visible = tags.slice(0, limit);
    var overflow = Math.max(0, tags.length - visible.length);
    var body = '';

    if (!tags.length) {
      body = '<span class="ws-map-ov-none">No tags yet</span>';
    } else {
      body =
        '<div class="ws-map-tags">' +
        visible
          .map(function (tag) {
            var safeTag = escapeHtml(tag);
            return (
              '<span class="ws-map-tag" title="' +
              safeTag +
              '">' +
              '<button type="button" class="ws-map-tag-label" data-ws-tag-filter="' +
              safeTag +
              '" title="Filter by ' +
              safeTag +
              '">' +
              safeTag +
              '</button>' +
              '<button type="button" class="ws-map-tag-remove" data-ws-tag-remove="' +
              escapeHtml(workspaceId) +
              '" data-ws-tag="' +
              safeTag +
              '" aria-label="Remove tag ' +
              safeTag +
              '" title="Remove tag">&times;</button>' +
              '</span>'
            );
          })
          .join('') +
        (overflow > 0
          ? '<span class="ws-map-tag ws-map-tag-more" title="' +
            escapeHtml(tags.slice(limit).join(', ')) +
            '">+' +
            overflow +
            ' more</span>'
          : '') +
        '</div>';
    }

    return '<div class="ws-map-ov-label">Tags</div>' + body;
  }

  function groupPreviewHTML(ws, options) {
    var preview = groupPreviewFor(ws, options);
    if (!preview) return '';
    var count = Number(preview.childCount || 0);
    var names = normalizeTags(preview.previewNames);
    var overflow = Number(preview.overflowCount || 0);
    var summary = count + ' workspace' + (count === 1 ? '' : 's');
    var body = names.length
      ? names.join(' · ') + (overflow > 0 ? ' +' + overflow + ' more' : '')
      : 'Drop workspaces here to organize related work.';
    return (
      '<div class="ws-map-ov-label">Group Preview</div>' +
      '<div class="ws-map-group-preview">' +
      '<span class="ws-map-group-count">' +
      escapeHtml(summary) +
      '</span>' +
      '<span class="ws-map-group-names" title="' +
      escapeHtml(body) +
      '">' +
      escapeHtml(body) +
      '</span>' +
      '</div>'
    );
  }

  function overviewBodyHTML(ws, options) {
    if (!ws) {
      return (
        '<div class="ws-map-overview-empty"><span class="ws-big">◈</span>' +
        'Select a workspace to see its agents, tasks, tools, and skills.</div>'
      );
    }
    var pal = paletteFor(ws.id);
    var mode = opsModeLabel(ws.ops_mode);
    var entry = String(ws.entry_agent_name || '').trim();
    var agents = (Array.isArray(ws.agents) ? ws.agents : []).filter(Boolean);
    var openTasks = Number(ws.open_task_count || 0);
    var backlogCount = Number(ws.backlog_count || 0);
    var mcp = Number(ws.mcp_count || 0);
    var skills = Number(ws.skill_count || 0);
    var description = String(ws.description || '').trim();
    var delLabel = isGroup(ws) ? 'Delete group' : 'Delete workspace';

    var keeper = entry
      ? '<div class="ws-map-ov-keeper">' +
        avatarHTML(entry, 'is-keeper') +
        '<div><div class="ws-map-ov-keeper-name">' +
        escapeHtml(entry) +
        '</div>' +
        '<div class="ws-map-ov-keeper-badge">★ Locked · can&#39;t remove</div></div></div>'
      : '<span class="ws-map-ov-none">No Commander</span>';

    var roster = agents.length
      ? agents
          .map(function (a) {
            return avatarHTML(a);
          })
          .join('')
      : '<span class="ws-map-ov-none">No agents yet</span>';

    return (
      '<div class="ws-map-ov-hero">' +
      '<span class="ws-map-ov-ic" style="--av:' +
      pal.key +
      '">' +
      escapeHtml(initials(ws.name)) +
      '</span>' +
      '<div class="ws-map-ov-herometa"><div class="ws-map-ov-name">' +
      escapeHtml(ws.name || 'Workspace') +
      '</div>' +
      (mode ? '<div class="ws-map-ov-tag">' + escapeHtml(mode) + '</div>' : '') +
      '</div>' +
      '<button type="button" class="ws-map-ov-open" data-ws-open="' +
      escapeHtml(ws.id) +
      '" aria-label="Open ' +
      escapeHtml(ws.name || 'workspace') +
      '">Open ▸</button>' +
      '</div>' +
      (description
        ? '<p class="ws-map-ov-desc" title="' +
          escapeHtml(description) +
          '">' +
          escapeHtml(description) +
          '</p>'
        : '<p class="ws-map-ov-desc is-empty">No description yet.</p>') +
      folderOverviewHTML(ws, options) +
      tagsOverviewHTML(ws, options) +
      groupPreviewHTML(ws, options) +
      '<div class="ws-map-ov-label">Commander</div>' +
      '<div class="ws-map-ov-keeperwrap">' +
      keeper +
      '</div>' +
      '<div class="ws-map-ov-label">Agents · ' +
      agents.length +
      '</div>' +
      '<div class="ws-map-ov-roster">' +
      roster +
      '</div>' +
      '<div class="ws-map-ov-row"><span class="ws-map-ov-k">Tasks</span>' +
      '<span class="ws-map-ov-v">' +
      openTasks +
      ' open</span></div>' +
      // Local count + open action only (FR58-59): no individual items render on
      // the global canvas and no global aggregate/edit surface exists here.
      '<div class="ws-map-ov-row"><span class="ws-map-ov-k">Backlog</span>' +
      '<button type="button" class="ws-map-ov-backlog-open" data-ws-open-backlog="' +
      escapeHtml(ws.id) +
      '" aria-label="Open Backlog for ' +
      escapeHtml(ws.name || 'workspace') +
      '"><span class="ws-map-ov-v">' +
      backlogCount +
      '</span> Open Backlog ▸</button></div>' +
      '<div class="ws-map-ov-row"><span class="ws-map-ov-k">Tools · MCP</span>' +
      '<span class="ws-map-ov-v">' +
      mcp +
      '</span></div>' +
      '<div class="ws-map-ov-row"><span class="ws-map-ov-k">Skills</span>' +
      '<span class="ws-map-ov-v">' +
      skills +
      '</span></div>' +
      setupOverviewHTML(ws) +
      '<div class="ws-map-ov-actions">' +
      '<button type="button" class="ws-map-ov-delete" data-ws-delete="' +
      escapeHtml(ws.id) +
      '" aria-label="' +
      escapeHtml(delLabel + ' ' + (ws.name || '')) +
      '">✕ ' +
      escapeHtml(delLabel) +
      '</button>' +
      '</div>'
    );
  }

  function hqOverviewHTML(view) {
    var primaryLabel = view.repair ? 'Build replacement HQ' : 'Build My HQ';
    return (
      '<div class="ws-map-ov-hero ws-map-hq-site-hero">' +
      '<span class="ws-map-ov-ic ws-map-hq-site-icon">HQ</span>' +
      '<div class="ws-map-ov-herometa"><div class="ws-map-ov-name">Personal HQ</div>' +
      '<div class="ws-map-ov-tag">' +
      escapeHtml(view.statusLabel) +
      '</div></div>' +
      '</div>' +
      '<h4 class="ws-map-hq-site-title">' +
      escapeHtml(view.title) +
      '</h4>' +
      '<p class="ws-map-ov-desc ws-map-hq-site-desc">' +
      escapeHtml(view.detail) +
      '</p>' +
      '<div class="ws-map-hq-benefits" aria-label="Personal HQ benefits">' +
      '<div class="ws-map-hq-benefit"><span>01</span><div><b>Daily orientation</b><small>Start with a focused brief.</small></div></div>' +
      '<div class="ws-map-hq-benefit"><span>02</span><div><b>Follow-through</b><small>Keep decisions and next steps together.</small></div></div>' +
      '<div class="ws-map-hq-benefit"><span>03</span><div><b>Clear routing</b><small>Give Ori a dependable home base.</small></div></div>' +
      '</div>' +
      '<div class="ws-map-hq-actions">' +
      '<button type="button" class="ws-map-hq-action ws-map-hq-action-primary" data-hq-action="build">' +
      escapeHtml(primaryLabel) +
      ' ▸</button>' +
      '<button type="button" class="ws-map-hq-action ws-map-hq-action-secondary" data-hq-action="import">Import HQ</button>' +
      (view.repair
        ? '<button type="button" class="ws-map-hq-action ws-map-hq-action-quiet" data-hq-action="clear">Clear broken HQ link</button>'
        : view.showSkip
          ? '<button type="button" class="ws-map-hq-action ws-map-hq-action-quiet" data-hq-action="skip">Not now</button>'
          : '') +
      '</div>'
    );
  }

  function shellHTML(stats, workspaces, selectedId, viewport, options) {
    var site = hqSiteView(hqStatus);
    var canvas =
      (Array.isArray(workspaces) && workspaces.length > 0) || site.show
        ? canvasHTML(workspaces, selectedId, { viewport: viewport }).html
        : emptyCanvasHTML();
    // Cockpit mode: the workspace-area header and the persistent context rail
    // already own the title, the stat readout, New Workspace, and the selected
    // workspace's overview (PRD FR15, FR17, FR29, FR62-FR69). Rendering the
    // map's own topbar/overview here would duplicate all four.
    if (hideChromeMode) {
      return (
        '<div class="ws-map-layout is-cockpit">' +
        '<section class="ws-map-theatre">' +
        '<div class="ws-map-compass">N<b>▲</b></div>' +
        canvas +
        '</section>' +
        '</div>' +
        selBarHTML()
      );
    }
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
      statBox(stats.openTasks, 'Open Tasks') +
      statBox(stats.groups, 'Groups') +
      '</div>' +
      '<button type="button" class="ws-map-create" data-ws-map-create>⊕ New Workspace</button>' +
      '</header>' +
      '<div class="ws-map-layout">' +
      '<section class="ws-map-theatre">' +
      '<div class="ws-map-theatre-tag"><span class="ws-map-ix">MAP</span><h3>All Workspaces</h3></div>' +
      '<div class="ws-map-compass">N<b>▲</b></div>' +
      canvas +
      '</section>' +
      '<aside class="ws-map-overview" role="region" aria-label="Workspace overview">' +
      '<div class="ws-map-overview-head"><div><span class="ws-map-ix">WS</span> <h3>Overview</h3></div></div>' +
      '<div class="ws-map-overview-body">' +
      (selectedId === HQ_SITE_ID
        ? hqOverviewHTML(site)
        : overviewBodyHTML(findWs(workspaces, selectedId), options)) +
      '</div>' +
      '</aside>' +
      '</div>' +
      // The selbar is position:fixed, so it must live OUTSIDE .ws-map-theatre —
      // that panel's clip-path + overflow:hidden would otherwise clip the bar
      // (fixed descendants are still clipped by an ancestor's clip-path), making
      // it invisible at the viewport bottom. Kept inside the container so
      // bindSelBar/updateSelBar still resolve it.
      selBarHTML()
    );
  }

  // Below this width the layout stacks: theatre full-width, overview in flow
  // beneath it (no overview column beside the map).
  function isNarrowViewport() {
    return (
      typeof window.matchMedia === 'function' && window.matchMedia('(max-width: 900px)').matches
    );
  }

  // ---------- viewport measurement ----------
  //
  // The container's size is used for exactly one thing: how much navigable
  // margin to leave around the content (FR-11). It never decides where a
  // building goes, which is why a resize cannot move one (FR-13). The old
  // responsive column count that used to reflow the whole map on every
  // breakpoint is gone.
  function measureViewport(container) {
    var width = (container && container.clientWidth) || 0;
    var height = (container && container.clientHeight) || 0;
    if (width <= 0 || height <= 0) return DEFAULT_VIEWPORT;
    // Beside the theatre sits the 338px overview column + 18px grid gap, except
    // in the stacked narrow layout and in cockpit mode, where the theatre spans
    // the full width.
    var theatre = hideChromeMode || isNarrowViewport() ? width : width - 338 - 18;
    return { width: Math.max(CELL_W, theatre), height: Math.max(CELL_H, height) };
  }

  var lastMount = null; // { container, state }

  // Reuse the page's existing create flow for every create affordance, rather
  // than opening a second Create Workspace path (PRD FR105). The cockpit's
  // button is checked first because the launcher's is absent on Home.
  function bindCreate(container) {
    var els = container.querySelectorAll('[data-ws-map-create]');
    Array.prototype.forEach.call(els, function (el) {
      el.addEventListener('click', function () {
        var create =
          document.getElementById('cockpitCreateWorkspaceBtn') ||
          document.getElementById('launcherCreateWorkspaceBtn');
        if (create) create.click();
      });
    });
  }

  // ---- blueprint setup status -------------------------------------------
  //
  // The Map shows a workspace's setup state so an unfinished blueprint is
  // visible from the base map, not only from inside the workspace. It is read
  // lazily for the *selected* workspace only: evaluating every workspace's
  // adapters on every list would make opening the Map cost one domain check per
  // tile. Every entry point here navigates into the workspace's own dialog, so
  // there is exactly one persisted setup state and one place it is changed.
  var setupStatusCache = {};

  function setupPresentation(status) {
    if (!status || !status.applicable) return null;
    if (status.state === 'ready') {
      return { state: 'Ready', tone: 'ready', action: 'View setup' };
    }
    if (status.state === 'needs_attention') {
      return { state: 'Needs attention', tone: 'attention', action: 'Repair setup' };
    }
    return { state: 'Setup required', tone: 'required', action: 'Continue setup' };
  }

  function setupOverviewHTML(ws) {
    var view = setupPresentation(ws && setupStatusCache[ws.id]);
    if (!view) return '';
    return (
      '<div class="ws-map-ov-row ws-map-ov-setup">' +
      '<span class="ws-map-ov-k">Setup</span>' +
      '<button type="button" class="ws-map-ov-setup-open is-' +
      escapeHtml(view.tone) +
      '" data-ws-open-setup="' +
      escapeHtml(ws.id) +
      '" aria-label="' +
      escapeHtml(view.state + ' \u2014 ' + view.action + ' for ' + (ws.name || 'workspace')) +
      '"><span class="ws-map-ov-v">' +
      escapeHtml(view.state) +
      '</span><span class="ws-map-ov-setup-action">' +
      escapeHtml(view.action) +
      ' \u25b8</span></button></div>'
    );
  }

  // ensureSetupStatus fetches once per workspace and patches the row in when the
  // answer arrives, so a workspace without a wizard never renders a placeholder
  // that then disappears.
  function ensureSetupStatus(container, ws) {
    if (!ws || !ws.id || Object.prototype.hasOwnProperty.call(setupStatusCache, ws.id)) return;
    setupStatusCache[ws.id] = null;
    var id = ws.id;
    fetch('/api/workspaces/' + encodeURIComponent(id) + '/setup-wizard')
      .then(function (response) {
        return response.ok ? response.json() : null;
      })
      .then(function (payload) {
        var status = payload && payload.setup;
        setupStatusCache[id] = status || null;
        if (!status || !status.applicable || selectedId !== id) return;
        var actions = container.querySelector('.ws-map-ov-actions');
        var body = container.querySelector('.ws-map-overview-body');
        if (!actions || !body || body.querySelector('[data-ws-open-setup]')) return;
        var holder = document.createElement('div');
        holder.innerHTML = setupOverviewHTML(ws);
        var row = holder.firstChild;
        if (!row) return;
        actions.parentNode.insertBefore(row, actions);
        bindOverviewActions(container, {});
      })
      .catch(function () {
        /* A status that cannot be read leaves the Map as it was: the workspace
           page is still the authoritative place to see and fix setup. */
      });
  }

  function openWorkspace(id, opts) {
    if (!id) return;
    // Owning-workspace deep link (FR59): ?panel=backlog opens straight into
    // the Details Backlog drawer. The global Map never mutates/promotes/
    // deletes backlog items itself — it only ever navigates there.
    var panel = opts && opts.panel;
    var query = panel ? '?panel=' + encodeURIComponent(panel) : '';
    // ?setup=1 opens the workspace's own Setup Wizard on arrival — the same
    // persisted state its banner and dialog show, never a second copy.
    if (opts && opts.setup) query = '?setup=1';
    window.location.href = '/workspaces/' + encodeURIComponent(id) + query;
  }

  function deleteWorkspace(id) {
    if (!id) return;
    // Reuse the hub's single-item delete flow (confirm modal, group handling,
    // Trash + Undo, toasts). It reloads on success, which re-mounts the map.
    if (window.WorkspaceHub && typeof window.WorkspaceHub.deleteWorkspace === 'function') {
      window.WorkspaceHub.deleteWorkspace(id);
    }
  }

  function bindOverviewActions(container, options) {
    var hqActions = container.querySelectorAll('[data-hq-action]');
    Array.prototype.forEach.call(hqActions, function (el) {
      el.addEventListener('click', function () {
        window.dispatchEvent(
          new CustomEvent('ori:personal-hq-action', {
            detail: { action: el.getAttribute('data-hq-action') }
          })
        );
      });
    });
    var opens = container.querySelectorAll('[data-ws-open]');
    Array.prototype.forEach.call(opens, function (el) {
      el.addEventListener('click', function () {
        openWorkspace(el.getAttribute('data-ws-open'));
      });
    });
    var openBacklogs = container.querySelectorAll('[data-ws-open-backlog]');
    Array.prototype.forEach.call(openBacklogs, function (el) {
      el.addEventListener('click', function () {
        openWorkspace(el.getAttribute('data-ws-open-backlog'), { panel: 'backlog' });
      });
    });
    var openSetups = container.querySelectorAll('[data-ws-open-setup]');
    Array.prototype.forEach.call(openSetups, function (el) {
      if (el.dataset && el.dataset.wsSetupBound === '1') return;
      if (el.dataset) el.dataset.wsSetupBound = '1';
      el.addEventListener('click', function () {
        openWorkspace(el.getAttribute('data-ws-open-setup'), { setup: true });
      });
    });
    var deletes = container.querySelectorAll('[data-ws-delete]');
    Array.prototype.forEach.call(deletes, function (el) {
      el.addEventListener('click', function () {
        deleteWorkspace(el.getAttribute('data-ws-delete'));
      });
    });
    var tagFilters = container.querySelectorAll('[data-ws-tag-filter]');
    Array.prototype.forEach.call(tagFilters, function (el) {
      el.addEventListener('click', function (event) {
        event.preventDefault();
        var callback = options && options.onTagFilter;
        if (typeof callback === 'function') {
          callback(el.getAttribute('data-ws-tag-filter'));
        }
      });
    });
    var tagRemoves = container.querySelectorAll('[data-ws-tag-remove]');
    Array.prototype.forEach.call(tagRemoves, function (el) {
      el.addEventListener('click', function (event) {
        event.preventDefault();
        var callback = options && options.onTagRemove;
        if (typeof callback === 'function') {
          callback(el.getAttribute('data-ws-tag-remove'), el.getAttribute('data-ws-tag'));
        }
      });
    });
  }

  // Update selection in place (no full re-mount) to avoid flicker.
  function applySelection(container, workspaces, id, options) {
    selectedId = id;
    var tiles = container.querySelectorAll('.ws-map-tile');
    Array.prototype.forEach.call(tiles, function (el) {
      var sel = el.getAttribute('data-ws-id') === id;
      el.classList.toggle('is-selected', sel);
      el.setAttribute('aria-pressed', sel ? 'true' : 'false');
    });
    var districts = container.querySelectorAll('.ws-map-district-tag[data-ws-id]');
    Array.prototype.forEach.call(districts, function (el) {
      el.classList.toggle('is-selected', el.getAttribute('data-ws-id') === id);
    });
    var selected = findWs(workspaces, id);
    // The map's own overview panel is absent in cockpit mode — the persistent
    // context rail renders the selection instead, via onSelect.
    var body = container.querySelector('.ws-map-overview-body');
    if (body) {
      body.innerHTML = overviewBodyHTML(selected, options);
      bindOverviewActions(container, options);
      ensureSetupStatus(container, selected);
    }
    // Center Selected is only meaningful once something is selected, so the
    // control's enabled state has to follow the selection rather than waiting
    // for the next camera change (FR-41).
    updateCameraControls(container);
    if (options && typeof options.onSelect === 'function') {
      options.onSelect(id, selected || null);
    }
  }

  function applyHQSelection(container, view, options) {
    selectedId = HQ_SITE_ID;
    var tiles = container.querySelectorAll('.ws-map-tile');
    Array.prototype.forEach.call(tiles, function (el) {
      var selected = el.hasAttribute('data-hq-site');
      el.classList.toggle('is-selected', selected);
      el.setAttribute('aria-pressed', selected ? 'true' : 'false');
    });
    var body = container.querySelector('.ws-map-overview-body');
    if (body) {
      body.innerHTML = hqOverviewHTML(view);
      bindOverviewActions(container, options);
    }
    if (options && typeof options.onSelectHQSite === 'function') {
      options.onSelectHQSite(view);
    }
  }

  // Refresh the multi-select action bar and per-tile checked state in place
  // (no re-mount) to keep toggling flicker-free.
  function updateSelBar(container) {
    var n = multiCount();
    var bar = container.querySelector('[data-ws-selbar]');
    if (bar) {
      bar.hidden = n === 0;
      var count = bar.querySelector('[data-ws-selbar-count]');
      if (count) count.textContent = n + ' selected';
    }
    var tiles = container.querySelectorAll('.ws-map-tile[data-ws-id]');
    Array.prototype.forEach.call(tiles, function (el) {
      var on = !!multiSelected[el.getAttribute('data-ws-id')];
      el.classList.toggle('is-multi', on);
      var check = el.querySelector('[data-ws-check]');
      if (check) check.setAttribute('aria-checked', on ? 'true' : 'false');
    });
  }

  function toggleMulti(container, id) {
    if (!id) return;
    if (multiSelected[id]) delete multiSelected[id];
    else multiSelected[id] = true;
    updateSelBar(container);
  }

  function clearMulti(container) {
    multiSelected = Object.create(null);
    updateSelBar(container);
  }

  function deleteMulti() {
    var ids = multiIds();
    if (!ids.length) return;
    // Reuse the hub's batch delete (confirm + Trash + Undo). On success it
    // reloads and re-mounts the map, where mount() prunes the deleted ids.
    if (window.WorkspaceHub && typeof window.WorkspaceHub.deleteWorkspaces === 'function') {
      window.WorkspaceHub.deleteWorkspaces(ids);
    }
  }

  function groupMulti() {
    var ids = multiIds();
    if (!ids.length) return;
    // Reuse the hub's Create Group modal + create/reparent flow. On success it
    // reloads and re-mounts the map, where mount() prunes the grouped ids.
    if (window.WorkspaceHub && typeof window.WorkspaceHub.groupWorkspaces === 'function') {
      window.WorkspaceHub.groupWorkspaces(ids);
    }
  }

  function bindSelBar(container) {
    var group = container.querySelector('[data-ws-selbar-group]');
    if (group)
      group.addEventListener('click', function () {
        groupMulti();
      });
    var del = container.querySelector('[data-ws-selbar-delete]');
    if (del)
      del.addEventListener('click', function () {
        deleteMulti();
      });
    var clr = container.querySelector('[data-ws-selbar-clear]');
    if (clr)
      clr.addEventListener('click', function () {
        clearMulti(container);
      });
  }

  function bindTiles(container, workspaces, options) {
    var selectables = container.querySelectorAll(
      '.ws-map-tile[data-ws-id], .ws-map-district-tag[data-ws-id]'
    );
    var selectOnly = !!(options && options.selectOnly);
    Array.prototype.forEach.call(selectables, function (el) {
      var isTile = el.classList.contains('ws-map-tile');
      // The corner checkbox — or a Cmd/Ctrl/Shift-click anywhere on a tile —
      // toggles the tile into the multi-select set (group districts can't be
      // multi-selected). That is true in both modes.
      //
      // Otherwise:
      //   select-only (cockpit): a click ALWAYS selects and never opens, no
      //     matter how many times it is repeated, and there is no dblclick
      //     rule (PRD FR35/FR36). Opening is Enter or the rail's explicit
      //     Open Workspace action only.
      //   legacy launcher: a click on an already-selected tile opens it, and
      //     double-click always opens.
      el.addEventListener('click', function (e) {
        var id = el.getAttribute('data-ws-id');
        var onCheck = e.target && e.target.closest && e.target.closest('[data-ws-check]');
        if (isTile && (onCheck || e.metaKey || e.ctrlKey || e.shiftKey)) {
          e.preventDefault();
          toggleMulti(container, id);
          return;
        }
        if (!selectOnly && id && id === selectedId) {
          openWorkspace(id);
          return;
        }
        applySelection(container, workspaces, id, options);
      });
      if (selectOnly) {
        // A <button> fires click on BOTH Enter (keydown) and Space (keyup), so
        // the click handler above already gives Space its select-only meaning.
        // Enter is intercepted here and turned into the explicit open, with the
        // default suppressed so it does not also fire a selecting click.
        el.addEventListener('keydown', function (e) {
          if (e.key !== 'Enter') return;
          e.preventDefault();
          var id = el.getAttribute('data-ws-id');
          if (!id) return;
          if (options && typeof options.onOpen === 'function') {
            options.onOpen(id);
            return;
          }
          openWorkspace(id);
        });
        return;
      }
      el.addEventListener('dblclick', function () {
        openWorkspace(el.getAttribute('data-ws-id'));
      });
    });
  }

  // ---------- viewport navigation ----------

  // announce speaks through the map's shared live region. It never moves focus:
  // a user in the middle of a gesture must not be yanked somewhere else to be
  // told what just happened (FR-116).
  function announce(container, message) {
    if (!container || typeof container.querySelector !== 'function') return;
    var region = container.querySelector('[data-map-live]');
    if (region) region.textContent = message;
  }

  // isInteractiveTarget guards empty-space panning. A gesture that starts on a
  // building, a group label, a checkbox, a control, or a link belongs to that
  // element, not to the camera (FR-34).
  function isInteractiveTarget(target) {
    if (!target || typeof target.closest !== 'function') return false;
    return !!target.closest(
      '.ws-map-tile, .ws-map-district-tag, .ws-map-pad, .ws-map-controls, button, a, input, select, textarea, [data-ws-check], [role="checkbox"]'
    );
  }

  function pointerPosition(canvas, event) {
    if (canvas && typeof canvas.getBoundingClientRect === 'function') {
      var rect = canvas.getBoundingClientRect();
      return { x: event.clientX - rect.left, y: event.clientY - rect.top };
    }
    return { x: event.clientX || 0, y: event.clientY || 0 };
  }

  /**
   * Empty-space panning.
   *
   * A press only becomes a pan once it has travelled past a small threshold, so
   * an ordinary click on the background still reads as a click (FR-33). Pointer
   * capture keeps the gesture alive when the pointer outruns the canvas, and
   * every exit — up, cancel, blur, unmount — releases it, so the map can never
   * be left stuck in a drag the user already finished (FR-35).
   */
  function bindViewportPan(container) {
    var canvas = container.querySelector('[data-ws-map-viewport]');
    if (!canvas || typeof canvas.addEventListener !== 'function') return;

    var pan = null;

    function stop() {
      if (!pan) return;
      if (
        canvas.releasePointerCapture &&
        canvas.hasPointerCapture &&
        canvas.hasPointerCapture(pan.pointerId)
      ) {
        canvas.releasePointerCapture(pan.pointerId);
      }
      if (canvas.classList) canvas.classList.remove('is-panning');
      pan = null;
    }

    canvas.addEventListener('pointerdown', function (event) {
      if (event.button != null && event.button !== 0) return;
      if (isInteractiveTarget(event.target)) return;
      var start = pointerPosition(canvas, event);
      pan = {
        pointerId: event.pointerId,
        startX: start.x,
        startY: start.y,
        centerX: camera.centerX,
        centerY: camera.centerY,
        moved: false
      };
      if (canvas.setPointerCapture) canvas.setPointerCapture(event.pointerId);
    });

    canvas.addEventListener('pointermove', function (event) {
      if (!pan && buildState.active) {
        // Preview where the next click would land, snapped or free, so the
        // candidate is visible before it is committed (FR-49, FR-50).
        var previewViewport = viewportSize(canvas);
        setBuildCandidate(
          container,
          snapPoint(
            screenToWorld(pointerPosition(canvas, event), camera, previewViewport),
            !!event.altKey
          )
        );
        return;
      }
      if (!pan || event.pointerId !== pan.pointerId) return;
      var point = pointerPosition(canvas, event);
      var dx = point.x - pan.startX;
      var dy = point.y - pan.startY;
      if (!pan.moved) {
        if (Math.abs(dx) < PAN_THRESHOLD && Math.abs(dy) < PAN_THRESHOLD) return;
        pan.moved = true;
        if (canvas.classList) canvas.classList.add('is-panning');
      }
      if (event.preventDefault) event.preventDefault();
      setCamera(
        {
          centerX: pan.centerX - dx / camera.zoom,
          centerY: pan.centerY - dy / camera.zoom,
          zoom: camera.zoom
        },
        container
      );
    });

    canvas.addEventListener('pointerup', function (event) {
      if (pan && event.pointerId !== pan.pointerId) return;
      var wasDrag = !!(pan && pan.moved);
      stop();
      // In build mode a press on empty world space that did not become a pan is
      // the user choosing where to build. A press that landed on a building, a
      // group label, or a control was never a site (FR-51, FR-52), and a drag
      // was a pan (FR-33).
      if (!buildState.active || wasDrag || isInteractiveTarget(event.target)) return;
      var viewport = viewportSize(canvas);
      var world = screenToWorld(pointerPosition(canvas, event), camera, viewport);
      // Option/Alt temporarily bypasses snapping without changing the saved
      // preference (FR-59).
      chooseBuildSite(container, snapPoint(world, !!event.altKey));
    });

    ['pointercancel', 'pointerleave'].forEach(function (type) {
      canvas.addEventListener(type, function (event) {
        if (pan && event.pointerId !== pan.pointerId) return;
        stop();
      });
    });
    if (typeof window !== 'undefined' && window.addEventListener) {
      window.addEventListener('blur', stop);
    }
  }

  /**
   * Wheel and trackpad navigation.
   *
   * An ordinary wheel or two-finger swipe pans, which is what those gestures do
   * everywhere else; a pinch (which browsers report as a ctrl-wheel) or the
   * platform's zoom modifier zooms about the pointer (FR-36, FR-39). The page's
   * own scrolling is only suppressed for gestures the map actually consumes.
   */
  function bindViewportWheel(container) {
    var canvas = container.querySelector('[data-ws-map-viewport]');
    if (!canvas || typeof canvas.addEventListener !== 'function') return;
    canvas.addEventListener(
      'wheel',
      function (event) {
        if (isInteractiveTarget(event.target)) return;
        if (event.preventDefault) event.preventDefault();
        var viewport = viewportSize(canvas);
        if (event.ctrlKey || event.metaKey) {
          var factor = Math.exp(-(event.deltaY || 0) * WHEEL_ZOOM_RATE);
          setCamera(
            zoomAroundPoint(camera, viewport, pointerPosition(canvas, event), factor),
            container
          );
          return;
        }
        setCamera(
          {
            centerX: camera.centerX + (event.deltaX || 0) / camera.zoom,
            centerY: camera.centerY + (event.deltaY || 0) / camera.zoom,
            zoom: camera.zoom
          },
          container
        );
      },
      { passive: false }
    );
  }

  // updateCameraControls keeps the readout truthful and disables a zoom button
  // that can no longer do anything, so the clamp is visible rather than a
  // button that silently stops responding (FR-38).
  function updateCameraControls(container) {
    if (!container || typeof container.querySelector !== 'function') return;
    var readout = container.querySelector('[data-map-zoom-readout]');
    if (readout) readout.textContent = Math.round(camera.zoom * 100) + '%';
    var zoomIn = container.querySelector('[data-map-zoom-in]');
    var zoomOut = container.querySelector('[data-map-zoom-out]');
    if (zoomIn) zoomIn.disabled = camera.zoom >= MAX_ZOOM - 0.0001;
    if (zoomOut) zoomOut.disabled = camera.zoom <= MIN_ZOOM + 0.0001;
    var center = container.querySelector('[data-map-center]');
    if (center) center.disabled = !selectedNodeAnchor();
  }

  // selectedNodeAnchor finds the world anchor of whatever is selected, whether
  // that is a building or a group district.
  function selectedNodeAnchor() {
    if (!selectedId || !lastWorldLayout) return null;
    var node = null;
    lastWorldLayout.nodes.forEach(function (candidate) {
      if (candidate.id === selectedId) node = { x: candidate.x, y: candidate.y };
    });
    if (node) return node;
    lastWorldLayout.districts.forEach(function (district) {
      if (district.id === selectedId) node = { x: district.anchorX, y: district.anchorY };
    });
    return node;
  }

  function bindCameraControls(container) {
    var canvas = container.querySelector('[data-ws-map-viewport]');

    function on(selector, handler) {
      var el = container.querySelector(selector);
      if (el && typeof el.addEventListener === 'function') el.addEventListener('click', handler);
    }

    on('[data-map-zoom-in]', function () {
      setCamera(zoomAroundCenter(camera, ZOOM_STEP), container);
      announce(container, 'Zoomed to ' + Math.round(camera.zoom * 100) + ' percent');
    });
    on('[data-map-zoom-out]', function () {
      setCamera(zoomAroundCenter(camera, 1 / ZOOM_STEP), container);
      announce(container, 'Zoomed to ' + Math.round(camera.zoom * 100) + ' percent');
    });
    on('[data-map-fit]', function () {
      fitAll(container);
      announce(container, 'Showing every workspace');
    });
    on('[data-map-center]', function () {
      var anchor = selectedNodeAnchor();
      if (!anchor) {
        announce(container, 'Select a workspace first');
        return;
      }
      // Centering moves the view, never the record (FR-41).
      setCamera(centerOn(camera, anchor), container);
      announce(container, 'Centered the selected workspace');
    });
    on('[data-map-reset-view]', function () {
      resetView(container);
      announce(container, 'View reset. Workspace positions are unchanged');
    });

    if (!canvas || typeof canvas.addEventListener !== 'function') return;
    // Keyboard equivalents for every camera gesture, so navigating the map
    // never requires a pointer (FR-115).
    canvas.addEventListener('keydown', function (event) {
      // Build mode owns the arrow keys while it is active: they move the
      // candidate site, not the camera (FR-60).
      if (buildState.active) return;
      var step = (event.shiftKey ? 4 : 1) * (SNAP_STEP * 2);
      var handled = true;
      switch (event.key) {
        case '+':
        case '=':
          setCamera(zoomAroundCenter(camera, ZOOM_STEP), container);
          break;
        case '-':
        case '_':
          setCamera(zoomAroundCenter(camera, 1 / ZOOM_STEP), container);
          break;
        case '0':
          resetView(container);
          break;
        case 'f':
        case 'F':
          fitAll(container);
          break;
        case 'ArrowLeft':
          setCamera(
            { centerX: camera.centerX - step, centerY: camera.centerY, zoom: camera.zoom },
            container
          );
          break;
        case 'ArrowRight':
          setCamera(
            { centerX: camera.centerX + step, centerY: camera.centerY, zoom: camera.zoom },
            container
          );
          break;
        case 'ArrowUp':
          setCamera(
            { centerX: camera.centerX, centerY: camera.centerY - step, zoom: camera.zoom },
            container
          );
          break;
        case 'ArrowDown':
          setCamera(
            { centerX: camera.centerX, centerY: camera.centerY + step, zoom: camera.zoom },
            container
          );
          break;
        default:
          handled = false;
      }
      if (handled && event.preventDefault) event.preventDefault();
    });
  }

  function fitAll(container) {
    if (!lastWorldLayout) return;
    var canvas = container.querySelector('[data-ws-map-viewport]');
    setCamera(fitBounds(lastWorldLayout.bounds, viewportSize(canvas)), container);
  }

  // Reset View restores the default framing — the content, centred, at 100%.
  // It deliberately changes nothing else: no building moves, and the snap
  // preference is untouched (FR-42).
  function resetView(container) {
    if (!lastWorldLayout) return;
    setCamera(
      {
        centerX: (lastWorldLayout.bounds.minX + lastWorldLayout.bounds.maxX) / 2,
        centerY: (lastWorldLayout.bounds.minY + lastWorldLayout.bounds.maxY) / 2,
        zoom: DEFAULT_ZOOM
      },
      container
    );
  }

  // ---------- build mode ----------
  //
  // An explicit, single-use placement state. Ordinary empty-space clicks keep
  // their ordinary meaning; only inside this mode does the next selection choose
  // a build site, and the mode ends as soon as it has one (FR-48).

  var buildState = { active: false, candidate: null, pending: null, container: null };

  function snapValue(value) {
    return Math.round(value / SNAP_STEP) * SNAP_STEP;
  }

  // snapPoint applies the one shared grid constant, aligned with the visible
  // background grid at 100% zoom (FR-58). `bypass` is the Option/Alt override:
  // it changes this one placement and never the persisted preference (FR-59).
  function snapPoint(point, bypass) {
    if (!layoutState.snapToGrid || bypass) return { x: point.x, y: point.y };
    return { x: snapValue(point.x), y: snapValue(point.y) };
  }

  function formatCoordinate(point) {
    return Math.round(point.x) + ', ' + Math.round(point.y);
  }

  function candidateLabel(point) {
    return formatCoordinate(point) + (layoutState.snapToGrid ? ' · snapped' : ' · free');
  }

  function setBuildCandidate(container, point) {
    buildState.candidate = point;
    var readout = container.querySelector('[data-map-build-coords]');
    if (readout) readout.textContent = point ? candidateLabel(point) : '';
    var marker = container.querySelector('[data-map-build-marker]');
    if (marker && marker.style) {
      if (point) {
        marker.hidden = false;
        marker.style.left = point.x + 'px';
        marker.style.top = point.y + 'px';
      } else {
        marker.hidden = true;
      }
    }
  }

  function startBuild(container) {
    if (layoutState.status !== 'ready') {
      announce(
        container,
        'Positions cannot be saved right now, so building at a point is unavailable'
      );
      return;
    }
    buildState.active = true;
    buildState.container = container;
    var canvas = container.querySelector('[data-ws-map-viewport]');
    if (canvas && canvas.classList) canvas.classList.add('is-building');
    var banner = container.querySelector('[data-map-build-banner]');
    if (banner) banner.hidden = false;
    // Keyboard placement starts at the middle of what the user is looking at,
    // which is the only starting point that needs no pointer (FR-60).
    var viewport = viewportSize(canvas);
    setBuildCandidate(
      container,
      snapPoint(screenToWorld({ x: viewport.width / 2, y: viewport.height / 2 }, camera, viewport))
    );
    if (canvas && canvas.focus) canvas.focus();
    announce(container, 'Build mode. Choose a spot on the map, then press Enter. Escape cancels.');
  }

  // exitBuildMode leaves placement mode. It deliberately does NOT clear the
  // pending coordinate: choosing a site ends the mode but the coordinate has to
  // survive until the create flow either uses it or is abandoned.
  function exitBuildMode(container) {
    var host = container || buildState.container;
    buildState.active = false;
    buildState.candidate = null;
    if (!host || typeof host.querySelector !== 'function') return;
    var canvas = host.querySelector('[data-ws-map-viewport]');
    if (canvas && canvas.classList) canvas.classList.remove('is-building');
    var banner = host.querySelector('[data-map-build-banner]');
    if (banner) banner.hidden = true;
    setBuildCandidate(host, null);
  }

  // cancelBuild is the abandonment path: leave the mode AND forget the site, so
  // nothing is left pointing at a workspace that will never exist (FR-54).
  function cancelBuild(container) {
    buildState.pending = null;
    exitBuildMode(container);
  }

  /**
   * Commit to a site and hand off to the existing Create Workspace flow.
   *
   * The coordinate is held as `pending` rather than saved: nothing is written
   * for a workspace that does not exist yet, so cancelling the modal leaves no
   * stray position behind (FR-54). The modal itself is Ori's existing one —
   * there is deliberately no second creation form (FR-51).
   */
  function chooseBuildSite(container, point) {
    if (!buildState.active || !point) return;
    buildState.pending = { x: point.x, y: point.y };
    exitBuildMode(container);
    announce(
      container,
      'Building at ' + formatCoordinate(point) + '. Complete the workspace details.'
    );
    var manager = typeof window !== 'undefined' ? window.sessionManager : null;
    if (manager && typeof manager.showAddWorkspaceModal === 'function') {
      manager.showAddWorkspaceModal({ mapOrigin: true, entryPoint: 'workspace_map_build' });
      return;
    }
    // No modal available (an older page): keep the map usable rather than
    // stranding the user in a mode with nowhere to go.
    buildState.pending = null;
  }

  /**
   * Save the coordinate the user chose for the workspace they just created.
   *
   * Creation and placement are separate failure domains on purpose (FR-56). By
   * the time this runs the workspace exists; if its coordinate cannot be saved,
   * the workspace stays, appears at its deterministic fallback, and the user is
   * told plainly and offered a retry — the client never deletes a real
   * workspace to tidy up a layout failure.
   */
  function completeBuild(workspaceId) {
    var point = buildState.pending;
    var container = buildState.container;
    buildState.pending = null;
    if (!workspaceId) return Promise.resolve(false);
    if (!point) return Promise.resolve(false);
    var positions = {};
    positions[workspaceId] = { x: point.x, y: point.y };
    return patchLayout([{ op: 'set_positions', positions: positions }])
      .then(function () {
        selectedId = workspaceId;
        announce(container, 'Workspace built at ' + formatCoordinate(point));
        settleLayout();
        return true;
      })
      .catch(function () {
        rememberFailedPlacement(workspaceId, point);
        announce(
          container,
          'The workspace was created, but its position could not be saved. It is shown at a default spot; use Retry position to try again.'
        );
        settleLayout();
        return false;
      });
  }

  // Failed initial placements are remembered so the honest retry offered to the
  // user saves the coordinate they actually chose, not a new guess (FR-56).
  var failedPlacements = Object.create(null);

  function rememberFailedPlacement(workspaceId, point) {
    failedPlacements[workspaceId] = { x: point.x, y: point.y };
  }

  function retryFailedPlacement(workspaceId) {
    var point = failedPlacements[workspaceId];
    if (!point) return Promise.resolve(false);
    var positions = {};
    positions[workspaceId] = point;
    return patchLayout([{ op: 'set_positions', positions: positions }])
      .then(function () {
        delete failedPlacements[workspaceId];
        settleLayout();
        return true;
      })
      .catch(function () {
        return false;
      });
  }

  function toggleSnap(container) {
    var next = !layoutState.snapToGrid;
    layoutState.snapToGrid = next;
    updateSnapControl(container);
    if (buildState.active && buildState.candidate) {
      setBuildCandidate(container, snapPoint(buildState.candidate));
    }
    announce(container, next ? 'Snap to grid on' : 'Snap to grid off');
    // The preference is the user's, so it persists (FR-57). A failed save
    // leaves the toggle where they put it for this session rather than
    // silently flipping back under them.
    patchLayout([{ op: 'set_preferences', snap_to_grid: next }]).catch(function () {
      announce(container, 'Snap preference could not be saved for next time');
    });
  }

  function updateSnapControl(container) {
    var toggle = container.querySelector('[data-map-snap]');
    if (!toggle) return;
    if (toggle.setAttribute)
      toggle.setAttribute('aria-pressed', layoutState.snapToGrid ? 'true' : 'false');
    toggle.textContent = 'Snap to grid: ' + (layoutState.snapToGrid ? 'on' : 'off');
  }

  // restoreBuildMode reinstates an in-progress placement after a re-render, so a
  // realtime workspace refresh cannot silently drop the user out of build mode
  // (FR-106).
  function restoreBuildMode(container) {
    buildState.container = container;
    var canvas = container.querySelector('[data-ws-map-viewport]');
    if (canvas && canvas.classList) canvas.classList.add('is-building');
    var banner = container.querySelector('[data-map-build-banner]');
    if (banner) banner.hidden = false;
    setBuildCandidate(container, buildState.candidate);
  }

  function bindBuildMode(container) {
    var canvas = container.querySelector('[data-ws-map-viewport]');

    var build = container.querySelector('[data-map-build]');
    if (build && build.addEventListener) {
      build.addEventListener('click', function () {
        startBuild(container);
      });
    }
    var snap = container.querySelector('[data-map-snap]');
    if (snap && snap.addEventListener) {
      snap.addEventListener('click', function () {
        toggleSnap(container);
      });
    }
    var cancel = container.querySelector('[data-map-build-cancel]');
    if (cancel && cancel.addEventListener) {
      cancel.addEventListener('click', function () {
        cancelBuild(container);
        announce(container, 'Build cancelled. Nothing was created.');
      });
    }
    if (!canvas || typeof canvas.addEventListener !== 'function') return;

    canvas.addEventListener('keydown', function (event) {
      if (!buildState.active) return;
      var step = layoutState.snapToGrid ? SNAP_STEP : 1;
      if (event.shiftKey) step *= 10;
      var candidate = buildState.candidate || { x: 0, y: 0 };
      var moved = null;
      switch (event.key) {
        case 'ArrowLeft':
          moved = { x: candidate.x - step, y: candidate.y };
          break;
        case 'ArrowRight':
          moved = { x: candidate.x + step, y: candidate.y };
          break;
        case 'ArrowUp':
          moved = { x: candidate.x, y: candidate.y - step };
          break;
        case 'ArrowDown':
          moved = { x: candidate.x, y: candidate.y + step };
          break;
        case 'Enter':
          if (event.preventDefault) event.preventDefault();
          if (event.stopPropagation) event.stopPropagation();
          chooseBuildSite(container, buildState.candidate);
          return;
        case 'Escape':
          if (event.preventDefault) event.preventDefault();
          cancelBuild(container);
          announce(container, 'Build cancelled. Nothing was created.');
          return;
        default:
          return;
      }
      if (event.preventDefault) event.preventDefault();
      if (event.stopPropagation) event.stopPropagation();
      setBuildCandidate(container, moved);
      announce(container, 'Candidate ' + candidateLabel(moved));
    });
  }

  function bindHQSite(container, options) {
    var site = container.querySelector('[data-hq-site]');
    if (!site) return;
    site.addEventListener('click', function () {
      applyHQSelection(container, hqSiteView(hqStatus), options);
    });
  }

  /**
   * Mount/refresh the map view. Idempotent — safe to call on every re-render.
   * @param {HTMLElement} container - the #launcherMap element.
   * @param {object} [state] - { workspaces: [...enriched summaries], selectedId }.
   */
  function mount(container, state) {
    if (!container) return;
    // Start the layout load before anything is drawn so the first settled paint
    // shows saved coordinates rather than fallback placement that then jumps
    // (FR-16). Whichever surface mounts first pays for the request; the other
    // reuses the same module-level result (FR-4).
    ensureLayoutLoaded();
    // Read the cockpit mode flags before any HTML is built — the exported
    // builders below consult them.
    selectOnlyMode = !!(state && state.selectOnly);
    hideChromeMode = !!(state && state.hideChrome);
    var workspaces = (state && state.workspaces) || [];
    var incoming = (state && state.selectedId) || '';
    var site = hqSiteView(hqStatus);
    var focusHQ = !hqFocusConsumed && hasHQFocusIntent();
    if (focusHQ && site.show) {
      selectedId = HQ_SITE_ID;
      hqFocusConsumed = true;
    } else if (focusHQ && findWs(workspaces, hqWorkspaceId)) {
      selectedId = hqWorkspaceId;
      hqFocusConsumed = true;
    } else if (selectedId === HQ_SITE_ID && site.show) {
      selectedId = HQ_SITE_ID;
    } else if (selectedId === HQ_SITE_ID && findWs(workspaces, hqWorkspaceId)) {
      selectedId = hqWorkspaceId;
    } else if (state && state.noAutoSelect) {
      // Cockpit mode: a bare Home load must open on the Today rail with nothing
      // selected (PRD FR74), so the map never invents a selection. It still
      // honors a selection that already exists or was passed in.
      selectedId = findWs(workspaces, selectedId)
        ? selectedId
        : findWs(workspaces, incoming)
          ? incoming
          : '';
    } else {
      // Keep the current selection if it still exists, else honor an incoming
      // selection, else fall back to a sensible default.
      selectedId = findWs(workspaces, selectedId)
        ? selectedId
        : findWs(workspaces, incoming)
          ? incoming
          : pickDefaultSelection(workspaces);
    }

    // Drop any multi-selected ids that no longer exist (e.g. after a delete
    // reload) so the action bar count stays truthful.
    multiIds().forEach(function (id) {
      if (!findWs(workspaces, id)) delete multiSelected[id];
    });

    var viewport = measureViewport(container);
    lastMount = { container: container, state: state };

    container.innerHTML = shellHTML(
      computeStats(workspaces),
      workspaces,
      selectedId,
      viewport,
      state
    );
    bindCreate(container);
    bindTiles(container, workspaces, state);
    bindHQSite(container, state);
    bindOverviewActions(container, state);
    // The camera survives every re-mount: a workspace refresh, a filter, or a
    // Map/Tree switch reconciles into the world the user is already looking at
    // rather than snapping them back to a default view (FR-106).
    ensureCamera(container);
    applyCamera(container);
    bindViewportPan(container);
    bindViewportWheel(container);
    bindCameraControls(container);
    bindBuildMode(container);
    // A re-render (a refresh landing mid-placement) rebuilt the banner and the
    // marker, so restore what the user was in the middle of.
    if (buildState.active) restoreBuildMode(container);
    // The first paint has a selection too, so its setup state is read here as
    // well as on every later selection change.
    ensureSetupStatus(container, findWs(workspaces, selectedId));
    bindSelBar(container);
    // No resize listener: world coordinates are viewport-independent, so a
    // resize changes only how much of the world is visible — never where a
    // building is (FR-13, FR-46).
  }

  /** Tear down the map view (called when switching away). */
  function unmount(container) {
    if (!container) return;
    container.innerHTML = '';
    // Clearing lastMount is what makes a layout response still in flight a
    // no-op when it lands: settleLayout has nothing to repaint.
    lastMount = null;
    multiSelected = Object.create(null);
  }

  window.OriWorkspaceMap = {
    mount: mount,
    unmount: unmount,
    // The map resolves the effective selection during mount (an item may have
    // been deleted since the caller's snapshot). The cockpit reads it back so
    // its shared selection state and the context rail cannot drift (FR57, FR73).
    getSelectedId: function () {
      return selectedId;
    },
    // The reserved-HQ-site view derived from the current Personal HQ status.
    // The cockpit needs it to render the site's build/repair/skip choices in
    // the context rail, since cockpit mode has no map-owned overview panel.
    getHQSiteView: function () {
      return hqSiteView(hqStatus);
    },
    isHQSiteId: function (id) {
      return id === HQ_SITE_ID;
    },
    setSelectedId: function (container, workspaces, id, options) {
      if (!container) {
        selectedId = id || '';
        return;
      }
      applySelection(container, workspaces || [], id || '', options);
    },
    computeLayout: computeMapLayout,
    // The coordinate engine: saved anchors, deterministic fallback placement,
    // elastic districts, content bounds, and world sizing, all pure so they can
    // be asserted without a browser (FR-123).
    computeWorldLayout: computeWorldLayout,
    // Pure camera math (FR-123). Exported so pointer-centred zoom, framing, and
    // clamping can be asserted exactly rather than eyeballed.
    camera: {
      clampZoom: clampZoom,
      screenToWorld: screenToWorld,
      worldToScreen: worldToScreen,
      zoomAroundPoint: zoomAroundPoint,
      zoomAroundCenter: zoomAroundCenter,
      fitBounds: fitBounds,
      centerOn: centerOn,
      clampCamera: clampCamera,
      limits: { min: MIN_ZOOM, max: MAX_ZOOM, step: ZOOM_STEP }
    },
    getCamera: function () {
      return { centerX: camera.centerX, centerY: camera.centerY, zoom: camera.zoom };
    },
    // Build mode's seam with the existing Create Workspace flow. sessions.js
    // calls completeBuild after a successful create and cancelBuild when the
    // modal closes without one, so the pending coordinate is consumed exactly
    // once and never for a workspace that does not exist (FR-53, FR-54).
    completeBuild: completeBuild,
    cancelBuild: function () {
      cancelBuild();
    },
    hasPendingBuild: function () {
      return !!buildState.pending;
    },
    retryPlacement: retryFailedPlacement,
    snapPoint: snapPoint,
    snapStep: SNAP_STEP,
    computeStats: computeStats,
    tileHTML: tileHTML,
    overviewBodyHTML: overviewBodyHTML,
    selBarHTML: selBarHTML,
    hqSiteView: hqSiteView,
    hqSiteHTML: hqSiteHTML,
    hqOverviewHTML: hqOverviewHTML,
    setHQStatus: setHQStatus,
    // The one reconciled layout state both surfaces read. Exposed so Home and
    // the /workspaces launcher can reflect "positions cannot be saved right
    // now" without each keeping its own copy (FR-4, FR-105).
    getLayoutState: function () {
      return {
        status: layoutState.status,
        revision: layoutState.revision,
        snapToGrid: layoutState.snapToGrid,
        readOnly: layoutState.status !== 'ready',
        positions: Object.assign(Object.create(null), layoutState.positions),
        viewport: layoutState.viewport ? Object.assign({}, layoutState.viewport) : null
      };
    },
    // Test-only seam for the persisted layout, so coordinate behavior can be
    // asserted without a network round-trip.
    _setLayoutForTest: function (payload, status) {
      applyLayoutPayload(payload || {});
      layoutState.status = status || 'ready';
    },
    // Test-only seam for the lazily-read setup status, so the overview's setup
    // row can be asserted without a network round-trip.
    _setSetupStatusForTest: function (workspaceId, status) {
      setupStatusCache[workspaceId] = status;
    },
    // Test-only seam for the real designated-HQ tile badge.
    _setHQWorkspaceIdForTest: function (id) {
      hqWorkspaceId = id;
      hqStatus = id ? { valid: true, workspace_id: id } : null;
    }
  };
})();
