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
  // Height of the floating control strip along the bottom of the map. Framing
  // actions keep content clear of it so a fitted building is always clickable.
  var CONTROL_STRIP_HEIGHT = 76;
  // The background grid's spacing at 100% zoom, shared with the CSS grid so a
  // snapped anchor always lands on a line the user can see (FR-58).
  var SNAP_STEP = 38;
  // Camera saves are debounced: a pan is hundreds of events and one intent
  // (FR-44).
  var CAMERA_SAVE_DELAY = 600;

  // The banner's two voices. Build mode asks for a site; a move reports where
  // the thing being moved would land.
  var BUILD_INSTRUCTION =
    'Choose where to build — click an empty spot, or use the arrow keys and Enter. Escape cancels.';
  var MOVE_INSTRUCTION = 'Moving — release to drop, or press Escape to put it back.';
  var KEYBOARD_MOVE_INSTRUCTION =
    'Moving — arrow keys to position, Enter to save, Escape to put it back.';
  // Shown in place of the instruction above while the current candidate
  // overlaps another building — the red outline carries the same meaning for
  // sighted users, but state must never be colour alone (FR-120).
  var MOVE_BLOCKED_INSTRUCTION = 'Occupied — this would land on top of another building.';

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
    // Every pan, zoom, fit, centre and reset lands here, which makes it the one
    // place that can honestly say "the camera moved". An open context menu is
    // anchored to a screen point, so it stops meaning anything the moment the
    // world slides underneath it (FR-18).
    closeContextMenu({ restoreFocus: false });
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
    var fitted = fitBounds(lastWorldLayout.bounds, framedViewport(canvas));
    // Fit All zooms in when there is little content; the opening view does not.
    // Landing at 200% on a two-workspace map is disorienting, and the button is
    // right there for anyone who wants it.
    camera = liftAboveControls({
      centerX: fitted.centerX,
      centerY: fitted.centerY,
      zoom: Math.min(DEFAULT_ZOOM, fitted.zoom)
    });
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
      'aria-label="Select for bulk action" ' +
      'title="Select for bulk action (or Shift/Cmd + Enter on the tile)"></span>' +
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
      // A separate, touch-sized handle for cluster movement. Dragging the label
      // would make "select this group" and "move everything in it" the same
      // gesture, and every existing group action — select, overview, open,
      // delete, Tree management — has to stay reachable (FR-85, FR-94).
      '<button type="button" class="ws-map-district-handle" data-group-drag="' +
      escapeHtml(ws.id) +
      '" aria-label="' +
      escapeHtml('Move the ' + (ws.name || 'group') + ' district and its workspaces') +
      '" title="Move this district">⤧</button>' +
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
      // Two clusters, because they are two different jobs. Navigation moves the
      // camera and can never change the map; the placement actions change where
      // things are. Keeping them apart also keeps either group from growing into
      // a bar that covers the buildings it is meant to help with.
      '<div class="ws-map-actions" role="group" aria-label="Map placement actions">' +
      '<button type="button" class="ws-map-ctl ws-map-ctl--wide ws-map-ctl--build" data-map-build' +
      (readOnly ? ' disabled' : '') +
      '>⊕ Build</button>' +
      '<button type="button" class="ws-map-ctl ws-map-ctl--wide" data-map-move' +
      (readOnly ? ' disabled' : '') +
      '>Move</button>' +
      '<button type="button" class="ws-map-ctl ws-map-ctl--wide" data-map-snap aria-pressed="' +
      (snapOn ? 'true' : 'false') +
      '"' +
      (readOnly ? ' disabled' : '') +
      '>Snap: ' +
      (snapOn ? 'on' : 'off') +
      '</button>' +
      // Reset Layout is deliberately worded and placed apart from Reset View:
      // one changes where the camera is, the other changes where every building
      // is (FR-109).
      '<button type="button" class="ws-map-ctl ws-map-ctl--wide" data-map-reset-layout' +
      (readOnly ? ' disabled' : '') +
      '>Reset layout…</button>' +
      '<button type="button" class="ws-map-ctl ws-map-ctl--wide" data-map-undo-reset hidden>Undo reset</button>' +
      '<button type="button" class="ws-map-ctl" data-map-help aria-expanded="false" aria-label="How the map works">?</button>' +
      '</div>' +
      helpHTML() +
      (readOnly
        ? '<p class="ws-map-notice" role="status">Positions cannot be saved right now. You can still look around; building and moving are unavailable until the map layout loads.</p>'
        : '') +
      '<div class="ws-map-controls" role="group" aria-label="Map view controls">' +
      '<button type="button" class="ws-map-ctl" data-map-zoom-out aria-label="Zoom out">−</button>' +
      '<span class="ws-map-zoom" data-map-zoom-readout aria-hidden="true">100%</span>' +
      '<button type="button" class="ws-map-ctl" data-map-zoom-in aria-label="Zoom in">+</button>' +
      '<button type="button" class="ws-map-ctl ws-map-ctl--wide" data-map-fit>Fit all</button>' +
      '<button type="button" class="ws-map-ctl ws-map-ctl--wide" data-map-center aria-label="Center selected">Center</button>' +
      '<button type="button" class="ws-map-ctl ws-map-ctl--wide" data-map-reset-view aria-label="Reset view">Reset</button>' +
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
  /**
   * Concise, discoverable help (FR-121).
   *
   * Every gesture the map supports is worth exactly one line. A user who never
   * opens this should still be able to work the map from the buttons; this is
   * for the ones that have no button — drag to pan, pinch to zoom, Alt to
   * bypass snapping.
   */
  function helpHTML() {
    return (
      '<div class="ws-map-help" data-map-help-panel hidden role="region" aria-label="How the map works">' +
      '<h4>How the map works</h4>' +
      '<ul>' +
      '<li><b>Pan</b> — drag empty space, or scroll/swipe.</li>' +
      '<li><b>Zoom</b> — pinch, ' +
      (isApplePlatform() ? '⌘' : 'Ctrl') +
      '+scroll, the + / − buttons, or the + / − keys.</li>' +
      '<li><b>Move a building</b> — drag it. Select it and press Move to do the same with arrow keys; Enter saves, Escape cancels.</li>' +
      '<li><b>Move a district</b> — drag the small handle on its outline. Its workspaces move with it; membership never changes.</li>' +
      '<li><b>Build</b> — press Build, then click an empty spot (or use arrow keys and Enter).</li>' +
      '<li><b>Snap</b> — on by default. Hold ' +
      (isApplePlatform() ? 'Option' : 'Alt') +
      ' to place freely for one move.</li>' +
      '<li><b>Escape</b> — cancels whatever is in progress without saving.</li>' +
      '</ul>' +
      '<p class="ws-map-help-note">Fit all, Center and Reset view move the camera. Reset layout moves the buildings — and can be undone.</p>' +
      '</div>'
    );
  }

  function isApplePlatform() {
    if (typeof navigator === 'undefined') return true;
    return /Mac|iPhone|iPad/i.test(String(navigator.platform || navigator.userAgent || ''));
  }

  function buildBannerHTML() {
    return (
      '<div class="ws-map-build" data-map-build-banner hidden>' +
      '<span class="ws-map-build-dot" aria-hidden="true">◎</span>' +
      '<span class="ws-map-build-text" data-map-build-text>' +
      BUILD_INSTRUCTION +
      '</span>' +
      '<span class="ws-map-build-coords" data-map-build-coords></span>' +
      '<button type="button" class="ws-map-ctl" data-map-build-cancel hidden>Cancel</button>' +
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

  // Readout for the multi-select set. Shown only while at least one tile is
  // checked, and refreshed in place by updateSelBar so toggling selection never
  // re-mounts the map.
  //
  // Group and Delete used to live here. They moved into the context menu (#317)
  // where the count is in the label and the cursor is already on one of the
  // checked buildings; the bar keeps only what it is uniquely good at — saying
  // how many are checked, and letting go of all of them at once.
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
        selBarHTML() +
        menuHostHTML()
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
      selBarHTML() +
      // The context menu's host, for the same reason: the menu is positioned in
      // viewport coordinates, so it must sit outside both the theatre's
      // clip-path and the world layer's pan/zoom transform.
      menuHostHTML()
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

  // Delete and group are owned by the host, not the map: the cockpit passes them
  // in as options wired to workspace-bulk-actions.js, which Tree also uses.
  //
  // These used to call `window.WorkspaceHub` directly. That global ships only
  // with the retired `/workspaces` launcher, which redirects to Home, so it was
  // never defined where the map actually renders and every click was a silent
  // no-op. `requireAction` keeps the fallback for the legacy page but refuses to
  // fail quietly again: an unwired action is a wiring bug and says so.
  var LEGACY_HUB_ACTIONS = {
    onDeleteWorkspace: 'deleteWorkspace',
    onDeleteWorkspaces: 'deleteWorkspaces',
    onGroupWorkspaces: 'groupWorkspaces'
  };

  function requireAction(options, name) {
    if (options && typeof options[name] === 'function') return options[name];
    var hub = window.WorkspaceHub;
    var legacy = LEGACY_HUB_ACTIONS[name];
    if (hub && legacy && typeof hub[legacy] === 'function') return hub[legacy].bind(hub);
    console.error('workspace-map: no handler for ' + name + '. The host must pass it to mount().');
    return null;
  }

  function deleteWorkspace(id, options) {
    if (!id) return;
    var action = requireAction(options, 'onDeleteWorkspace');
    if (action) action(id);
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
        deleteWorkspace(el.getAttribute('data-ws-delete'), options);
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

  // Batch delete + group. On success the host reloads and re-mounts the map,
  // where mount() prunes ids that no longer exist.
  function deleteMulti(options) {
    var ids = multiIds();
    if (!ids.length) return;
    var action = requireAction(options, 'onDeleteWorkspaces');
    if (action) action(ids);
  }

  function groupMulti(options) {
    var ids = multiIds();
    if (!ids.length) return;
    var action = requireAction(options, 'onGroupWorkspaces');
    if (action) action(ids);
  }

  // Only Clear is bound here now: Group and Delete are reached by right-clicking
  // any checked building (see contextMenuItemsFor).
  function bindSelBar(container) {
    var clr = container.querySelector('[data-ws-selbar-clear]');
    if (clr)
      clr.addEventListener('click', function () {
        clearMulti(container);
      });
  }

  // ---------- context menu ----------
  //
  // Right-click puts a target's own actions under the cursor (#317). Three
  // rules shape everything below:
  //
  //   1. The menu is mounted in a host that lives OUTSIDE the transformed world
  //      layer — and outside the theatre's clip-path, like the selection bar —
  //      so it is positioned in viewport coordinates. A menu inside the world
  //      would scale with the zoom level and slide away during a pan.
  //   2. One delegated `contextmenu` listener on the canvas resolves its target
  //      with closest(). Tiles are re-rendered on every data refresh, so
  //      per-tile binding would be lost or doubled across re-mounts.
  //   3. Every item routes to an action that already exists — the rail's
  //      handlers, the bulk callbacks the host passes in, or the camera
  //      controls. The menu adds no capability of its own.

  var MENU_EDGE_PAD = 8;
  // Used only when the environment cannot measure the menu (before layout, or
  // in a stub DOM). Flipping on an estimate beats not flipping at all.
  var MENU_FALLBACK_SIZE = { width: 232, height: 260 };

  // The one open menu: its host, the element focus returns to, the action
  // context, and the listeners that close it.
  var menuState = null;

  function menuDivider() {
    return { divider: true };
  }

  // The same read-only condition the placement controls already use: a layout
  // that cannot be loaded cannot be saved, so anything that mutates workspaces
  // or layout is offered but disabled (FR-15, mirroring "Reset layout…").
  function isMapReadOnly() {
    return layoutState.status !== 'ready';
  }

  function contextMenuHTML(items, options) {
    var opts = options || {};
    var body = (Array.isArray(items) ? items : [])
      .map(function (item) {
        if (!item) return '';
        if (item.divider) return '<div class="ori-context-divider" role="separator"></div>';
        return (
          '<button type="button" class="ori-context-item ws-map-menu-item' +
          (item.variant === 'danger' ? ' ori-context-danger' : '') +
          '" role="menuitem" tabindex="-1" data-menu-action="' +
          escapeHtml(item.action) +
          '"' +
          // Disabled items keep their place and their announcement; only the
          // activation and the arrow stop are withheld (FR-21).
          (item.disabled ? ' aria-disabled="true"' : '') +
          '>' +
          escapeHtml(item.label) +
          '</button>'
        );
      })
      .join('');
    return (
      '<div class="ori-context-menu ws-map-menu" data-ws-map-menu role="menu" aria-label="' +
      escapeHtml(opts.label || 'Map actions') +
      '">' +
      body +
      '</div>'
    );
  }

  function menuHostHTML() {
    return '<div class="ws-map-menu-host" data-ws-map-menu-host></div>';
  }

  // Single workspace tile. Every entry mirrors a control the Overview rail
  // already renders, including the rail's own "Delete group" wording for a
  // group and its condition for the Setup entry point.
  function tileMenuItems(ws) {
    var id = (ws && ws.id) || '';
    var items = [
      { label: 'Open workspace', action: 'open' },
      { label: 'Open → Backlog', action: 'open-backlog' }
    ];
    if (setupPresentation(setupStatusCache[id])) {
      items.push({ label: 'Open → Setup', action: 'open-setup' });
    }
    items.push(menuDivider());
    items.push({
      label: multiSelected[id] ? 'Remove from selection' : 'Add to selection',
      action: 'toggle-selection'
    });
    items.push(menuDivider());
    items.push({
      label: isGroup(ws) ? 'Delete group' : 'Delete workspace',
      action: 'delete',
      variant: 'danger',
      disabled: isMapReadOnly()
    });
    return items;
  }

  function workspaceCountLabel(verb, count) {
    return verb + ' ' + count + (count === 1 ? ' workspace' : ' workspaces');
  }

  // The checked set, when the right-click landed inside it. The count is in
  // every label rather than only in the confirm dialog, so the blast radius is
  // visible before the click (FR-7, FR-11).
  function multiMenuItems() {
    var count = multiCount();
    var readOnly = isMapReadOnly();
    return [
      { label: workspaceCountLabel('Group', count), action: 'group-multi', disabled: readOnly },
      { label: 'Clear selection', action: 'clear-selection' },
      menuDivider(),
      {
        label: workspaceCountLabel('Delete', count),
        action: 'delete-multi',
        variant: 'danger',
        disabled: readOnly
      }
    ];
  }

  // The item set for a resolved target. Pure apart from the module state the
  // rail reads too (multi-select set, setup cache, layout status), so a menu can
  // be asserted without a DOM.
  function contextMenuItemsFor(target) {
    var spec = target || {};
    if (spec.type === 'tile') {
      // A tile inside the checked set acts on the whole set; a tile outside it
      // acts on itself. Which menu you get is therefore a statement about what
      // is already selected, not a mode.
      if (spec.id && multiSelected[spec.id]) return multiMenuItems();
      return tileMenuItems(spec.ws || { id: spec.id });
    }
    return [];
  }

  function menuLabelFor(target) {
    var spec = target || {};
    if (spec.type === 'tile') {
      if (spec.id && multiSelected[spec.id]) {
        return 'Actions for ' + multiCount() + ' selected';
      }
      var name = (spec.ws && spec.ws.name) || 'workspace';
      return 'Actions for ' + name;
    }
    return 'Map actions';
  }

  function clampToRange(value, min, max) {
    if (max < min) return min;
    return Math.max(min, Math.min(value, max));
  }

  /**
   * Where the menu opens.
   *
   * The menu flips back across the anchor point when it would otherwise run
   * past a viewport edge, and is then clamped inside the viewport, so it can
   * never render off-screen or push the page into a scroll (FR-17). Pure, so
   * the edge cases are assertable without a browser.
   */
  function menuPosition(point, size, viewport) {
    var left = point.x + size.width > viewport.width ? point.x - size.width : point.x;
    var top = point.y + size.height > viewport.height ? point.y - size.height : point.y;
    return {
      left: clampToRange(left, MENU_EDGE_PAD, viewport.width - size.width - MENU_EDGE_PAD),
      top: clampToRange(top, MENU_EDGE_PAD, viewport.height - size.height - MENU_EDGE_PAD)
    };
  }

  function menuViewport() {
    var width = (typeof window !== 'undefined' && window.innerWidth) || 0;
    var height = (typeof window !== 'undefined' && window.innerHeight) || 0;
    return {
      width: width > 0 ? width : DEFAULT_VIEWPORT.width,
      height: height > 0 ? height : DEFAULT_VIEWPORT.height
    };
  }

  function measureMenu(menu) {
    var rect =
      menu && typeof menu.getBoundingClientRect === 'function'
        ? menu.getBoundingClientRect()
        : null;
    var width = (rect && rect.width) || (menu && menu.offsetWidth) || 0;
    var height = (rect && rect.height) || (menu && menu.offsetHeight) || 0;
    return {
      width: width > 0 ? width : MENU_FALLBACK_SIZE.width,
      height: height > 0 ? height : MENU_FALLBACK_SIZE.height
    };
  }

  function placeMenu(menu, point) {
    if (!menu || !menu.style) return;
    var placed = menuPosition(point, measureMenu(menu), menuViewport());
    menu.style.left = placed.left + 'px';
    menu.style.top = placed.top + 'px';
  }

  function menuItemElements(menu) {
    if (!menu || typeof menu.querySelectorAll !== 'function') return [];
    return Array.prototype.slice.call(menu.querySelectorAll('[data-menu-action]'));
  }

  function isMenuItemDisabled(el) {
    return !!(el && el.getAttribute && el.getAttribute('aria-disabled') === 'true');
  }

  // Roving tabindex: exactly one item is tabbable at a time, and it is the one
  // that has focus (FR-21, FR-22).
  function focusMenuItem(index) {
    var state = menuState;
    if (!state) return;
    var items = state.items;
    if (!items.length) return;
    var next = clampToRange(index, 0, items.length - 1);
    state.index = next;
    items.forEach(function (el, i) {
      if (el && el.setAttribute) el.setAttribute('tabindex', i === next ? '0' : '-1');
    });
    var target = items[next];
    if (target && typeof target.focus === 'function') target.focus();
  }

  // Arrow navigation wraps at both ends and steps over disabled items, which
  // stay visible and announced (FR-21).
  function nextEnabledIndex(from, step) {
    var state = menuState;
    if (!state || !state.items.length) return -1;
    var count = state.items.length;
    for (var i = 1; i <= count; i++) {
      var candidate = (((from + step * i) % count) + count) % count;
      if (!isMenuItemDisabled(state.items[candidate])) return candidate;
    }
    return -1;
  }

  function firstEnabledIndex(step) {
    var state = menuState;
    if (!state || !state.items.length) return -1;
    var start = step > 0 ? -1 : 0;
    return nextEnabledIndex(start, step);
  }

  function listenWhileOpen(target, type, handler, capture) {
    if (!target || typeof target.addEventListener !== 'function') return;
    target.addEventListener(type, handler, capture);
    if (menuState) {
      menuState.teardown.push(function () {
        if (typeof target.removeEventListener === 'function') {
          target.removeEventListener(type, handler, capture);
        }
      });
    }
  }

  function insideOpenMenu(node) {
    if (!node || typeof node.closest !== 'function') return false;
    return !!node.closest('[data-ws-map-menu]');
  }

  /**
   * Close the open menu.
   *
   * Every dismissal route lands here — Escape, a click or right-click outside,
   * choosing an item, a resize, a camera change, and a re-mount — so focus
   * returns to the element the menu was opened from exactly once, no matter how
   * it was closed (FR-19).
   */
  function closeContextMenu(options) {
    var state = menuState;
    if (!state) return;
    menuState = null;
    state.teardown.forEach(function (off) {
      off();
    });
    if (state.host) state.host.innerHTML = '';
    var restore = !(options && options.restoreFocus === false);
    if (restore && state.origin && typeof state.origin.focus === 'function') state.origin.focus();
  }

  function activateMenuItem(el) {
    var state = menuState;
    if (!state || !el || isMenuItemDisabled(el)) return;
    var action = el.getAttribute ? el.getAttribute('data-menu-action') : '';
    var container = state.container;
    var context = state.context;
    var options = state.options;
    // Close first: focus goes back to the target before the action runs, so a
    // confirm dialog or a navigation starts from a sane place.
    closeContextMenu();
    runMenuAction(container, action, context, options);
  }

  function bindMenuInteractions() {
    var state = menuState;
    if (!state) return;
    state.items.forEach(function (el) {
      if (!el || typeof el.addEventListener !== 'function') return;
      el.addEventListener('click', function (event) {
        if (event && event.preventDefault) event.preventDefault();
        activateMenuItem(el);
      });
    });
    listenWhileOpen(state.menu, 'keydown', function (event) {
      handleMenuKey(event);
    });
    // Dismissal that cannot be seen from inside the menu: a pointer or a key
    // elsewhere on the page, and a resize that would strand the menu away from
    // what it was anchored to (FR-18).
    //
    // `openEvent` is the right-click (or key press) that opened this menu, and
    // it is still propagating while these listeners are being added. A listener
    // attached to a node the event has not reached yet is still called for that
    // event, so without this guard the menu would close itself on the way up to
    // the document — opening and dismissing in one gesture.
    var openEvent = state.openEvent;
    if (typeof document !== 'undefined') {
      listenWhileOpen(document, 'mousedown', function (event) {
        if (event === openEvent) return;
        if (insideOpenMenu(event && event.target)) return;
        closeContextMenu();
      });
      listenWhileOpen(document, 'contextmenu', function (event) {
        if (event === openEvent) return;
        if (insideOpenMenu(event && event.target)) return;
        closeContextMenu();
      });
      listenWhileOpen(document, 'keydown', function (event) {
        if (event === openEvent) return;
        if (!event || event.key !== 'Escape') return;
        closeContextMenu();
      });
    }
    if (typeof window !== 'undefined') {
      listenWhileOpen(window, 'resize', function () {
        closeContextMenu();
      });
    }
  }

  function handleMenuKey(event) {
    var state = menuState;
    if (!state || !event) return;
    var key = event.key;
    var handled = true;
    switch (key) {
      case 'ArrowDown':
        focusMenuItem(nextEnabledIndex(state.index, 1));
        break;
      case 'ArrowUp':
        focusMenuItem(nextEnabledIndex(state.index, -1));
        break;
      case 'Home':
        focusMenuItem(firstEnabledIndex(1));
        break;
      case 'End':
        focusMenuItem(firstEnabledIndex(-1));
        break;
      case 'Enter':
      case ' ':
      case 'Spacebar':
        activateMenuItem(state.items[state.index]);
        break;
      case 'Escape':
      case 'Tab':
        closeContextMenu();
        break;
      default:
        handled = false;
    }
    // Enter and Space are prevented too: a <button> would otherwise synthesize
    // a click and run the action a second time.
    if (handled && event.preventDefault) event.preventDefault();
  }

  function openContextMenu(container, spec) {
    closeContextMenu({ restoreFocus: false });
    if (!container || typeof container.querySelector !== 'function') return false;
    var host = container.querySelector('[data-ws-map-menu-host]');
    if (!host) return false;
    var items = (spec.items || []).filter(Boolean);
    if (!items.length) return false;
    host.innerHTML = contextMenuHTML(items, { label: spec.label });
    var menu =
      typeof host.querySelector === 'function' ? host.querySelector('[data-ws-map-menu]') : null;
    if (!menu) {
      host.innerHTML = '';
      return false;
    }
    menuState = {
      container: container,
      host: host,
      menu: menu,
      items: menuItemElements(menu),
      index: 0,
      origin: spec.origin || null,
      context: spec.context || {},
      options: spec.options || {},
      // The gesture that opened this menu, so the dismissal listeners can
      // ignore it while it is still propagating (see bindMenuInteractions).
      openEvent: spec.event || null,
      teardown: []
    };
    placeMenu(menu, spec.at || { x: 0, y: 0 });
    bindMenuInteractions();
    focusMenuItem(firstEnabledIndex(1));
    return true;
  }

  function runMenuAction(container, action, context, options) {
    var id = context.id || '';
    switch (action) {
      case 'open':
        // The map's explicit open: the host's onOpen when it has one (the
        // cockpit records the first action there), else plain navigation, which
        // is what the rail's Open button does.
        if (options && typeof options.onOpen === 'function') options.onOpen(id);
        else openWorkspace(id);
        break;
      case 'open-backlog':
        openWorkspace(id, { panel: 'backlog' });
        break;
      case 'open-setup':
        openWorkspace(id, { setup: true });
        break;
      case 'toggle-selection':
        toggleMulti(container, id);
        break;
      case 'delete':
        deleteWorkspace(id, options);
        break;
      // The bulk actions the selection bar used to carry. They run the host's
      // own callbacks, so the existing confirm paths in workspace-bulk-actions
      // are the only confirmation — the menu adds no second one.
      case 'group-multi':
        groupMulti(options);
        break;
      case 'delete-multi':
        deleteMulti(options);
        break;
      case 'clear-selection':
        clearMulti(container);
        break;
      default:
        break;
    }
  }

  // resolveMenuTarget turns whatever was right-clicked into one of the map's
  // four targets. Order matters: the HQ site is also a `.ws-map-tile`, and a
  // district's label sits inside the district outline.
  function resolveMenuTarget(node, workspaces) {
    if (!node || typeof node.closest !== 'function') return null;
    var hq = node.closest('[data-hq-site]');
    if (hq) return { type: 'hq', element: hq };
    var tile = node.closest('.ws-map-tile[data-ws-id]');
    if (tile) {
      var tileId = tile.getAttribute('data-ws-id');
      return { type: 'tile', id: tileId, ws: findWs(workspaces, tileId), element: tile };
    }
    var district = node.closest('.ws-map-district');
    if (district) {
      var groupId = district.getAttribute('data-group-id');
      return { type: 'district', id: groupId, ws: findWs(workspaces, groupId), element: district };
    }
    var canvas = node.closest('.ws-map-canvas');
    if (canvas) return { type: 'canvas', element: canvas };
    return null;
  }

  function openMenuForTarget(container, workspaces, options, target, at, event) {
    // Right-clicking a tile that is not in the multi-select set selects it
    // first, so the menu always acts on something the user can see is chosen
    // (FR-6). It never opens the workspace and never touches the checkbox.
    if (target.type === 'tile' && target.id && !multiSelected[target.id]) {
      applySelection(container, workspaces, target.id, options);
    }
    var items = contextMenuItemsFor(target);
    if (!items.length) return false;
    return openContextMenu(container, {
      items: items,
      label: menuLabelFor(target),
      at: at,
      origin: target.element,
      context: { id: target.id || '', type: target.type },
      options: options,
      event: event
    });
  }

  function bindContextMenu(container, workspaces, options) {
    var canvas =
      container.querySelector('[data-ws-map-viewport]') ||
      container.querySelector('.ws-map-canvas');
    if (!canvas || typeof canvas.addEventListener !== 'function') return;
    // One delegated listener, because tiles are replaced on every refresh.
    canvas.addEventListener('contextmenu', function (event) {
      // A menu opened mid-gesture would act on a target that is still moving.
      if (dragState || clusterDrag) return;
      var target = resolveMenuTarget(event && event.target, workspaces);
      if (!target) return;
      var items = contextMenuItemsFor(target);
      // No item set for this target yet: leave the browser's own menu alone
      // rather than suppressing it and offering nothing in its place.
      if (!items.length) return;
      if (event.preventDefault) event.preventDefault();
      openMenuForTarget(
        container,
        workspaces,
        options,
        target,
        { x: (event && event.clientX) || 0, y: (event && event.clientY) || 0 },
        event
      );
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
      // Keyboard equivalent of the modifier-click and the corner checkbox. The
      // checkbox itself stays tabindex="-1" — it lives inside the tile <button>,
      // and a focusable control nested in a control is invalid — so without this
      // handler the whole bulk bar was reachable by mouse only.
      if (isTile) {
        el.addEventListener('keydown', function (e) {
          if (e.key !== 'Enter' && e.key !== ' ' && e.key !== 'Spacebar') return;
          if (!(e.metaKey || e.ctrlKey || e.shiftKey)) return;
          // Suppress the synthesized click so the tile does not also select.
          e.preventDefault();
          toggleMulti(container, el.getAttribute('data-ws-id'));
        });
      }
      if (selectOnly) {
        // A <button> fires click on BOTH Enter (keydown) and Space (keyup), so
        // the click handler above already gives Space its select-only meaning.
        // Enter is intercepted here and turned into the explicit open, with the
        // default suppressed so it does not also fire a selecting click.
        el.addEventListener('keydown', function (e) {
          if (e.key !== 'Enter') return;
          // A modified Enter is a multi-select toggle, handled above.
          if (e.metaKey || e.ctrlKey || e.shiftKey) return;
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
      '.ws-map-tile, .ws-map-district-tag, .ws-map-district-handle, .ws-map-pad, .ws-map-controls, .ws-map-actions, .ws-map-build, button, a, input, select, textarea, [data-ws-check], [role="checkbox"]'
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
      // Zoom is clamped at 50%, so a layout spread wider than two viewports
      // cannot literally all fit. Saying so is better than a button that
      // quietly under-delivers (FR-38, FR-40).
      announce(
        container,
        camera.zoom <= MIN_ZOOM + 0.0001
          ? 'Zoomed out as far as the map goes. Some workspaces are still off-screen — pan to reach them.'
          : 'Showing every workspace'
      );
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
    on('[data-map-move]', function () {
      startKeyboardMove(container);
      var canvasEl = container.querySelector('[data-ws-map-viewport]');
      if (canvasEl && canvasEl.focus) canvasEl.focus();
    });

    if (!canvas || typeof canvas.addEventListener !== 'function') return;
    // Keyboard equivalents for every camera gesture, so navigating the map
    // never requires a pointer (FR-115).
    canvas.addEventListener('keydown', function (event) {
      // Build mode and keyboard Move own the arrow keys while either is active:
      // they move the candidate, not the camera (FR-60, FR-78).
      if (buildState.active) return;
      if (handleMoveKey(container, event)) {
        if (event.preventDefault) event.preventDefault();
        return;
      }
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

  // framedViewport is the part of the canvas that is actually clear. The camera
  // controls float over the bottom of the map, so framing content into the full
  // height would park buildings underneath them — visible, but not clickable,
  // which is exactly what FR-73 forbids.
  function framedViewport(canvas) {
    var viewport = viewportSize(canvas);
    return {
      width: viewport.width,
      height: Math.max(CELL_H, viewport.height - CONTROL_STRIP_HEIGHT),
      full: viewport
    };
  }

  // liftAboveControls shifts a framing camera up by half the reserved strip, so
  // the content it framed is centred in the clear area rather than in the whole
  // canvas.
  function liftAboveControls(cam) {
    return {
      centerX: cam.centerX,
      centerY: cam.centerY + CONTROL_STRIP_HEIGHT / 2 / cam.zoom,
      zoom: cam.zoom
    };
  }

  function fitAll(container) {
    if (!lastWorldLayout) return;
    var canvas = container.querySelector('[data-ws-map-viewport]');
    setCamera(
      liftAboveControls(fitBounds(lastWorldLayout.bounds, framedViewport(canvas))),
      container
    );
  }

  // Reset View restores the default framing — the content, centred, at 100%.
  // It deliberately changes nothing else: no building moves, and the snap
  // preference is untouched (FR-42).
  function resetView(container) {
    if (!lastWorldLayout) return;
    setCamera(
      liftAboveControls({
        centerX: (lastWorldLayout.bounds.minX + lastWorldLayout.bounds.maxX) / 2,
        centerY: (lastWorldLayout.bounds.minY + lastWorldLayout.bounds.maxY) / 2,
        zoom: DEFAULT_ZOOM
      }),
      container
    );
  }

  // ---------- moving buildings ----------
  //
  // A drag is a state machine, not a click handler. It only becomes a drag once
  // the pointer has travelled past a threshold, so every existing meaning of a
  // press — select, open, check for a bulk action — survives untouched
  // (FR-63, FR-64).

  var dragState = null;
  // Set for one click after a drag so the click the browser synthesises on
  // pointerup cannot re-select or open the workspace that was just dropped
  // (FR-66).
  var suppressClickFor = null;
  // The record to put focus back on after the re-render that follows a
  // committed move (FR-117).
  var pendingFocusId = '';

  /** The anchor a node is currently committed to, saved or automatic. */
  function committedAnchor(id) {
    var saved = layoutState.positions[id];
    if (saved) return { x: saved.x, y: saved.y };
    if (!lastWorldLayout) return null;
    var found = null;
    lastWorldLayout.nodes.forEach(function (node) {
      if (node.id === id) found = { x: node.x, y: node.y };
    });
    if (found) return found;
    lastWorldLayout.districts.forEach(function (district) {
      if (district.id === id) found = { x: district.anchorX, y: district.anchorY };
    });
    return found;
  }

  /**
   * Would a box of CELL_W x CELL_H anchored at `a` overlap the same box
   * anchored at `b`? Anchors are top-left corners (see placeElement), so this
   * is a same-size axis-aligned rectangle intersection test. Touching edges
   * (the boxes exactly abut) is not an overlap — only shared area is.
   */
  function footprintsOverlap(a, b) {
    return Math.abs(a.x - b.x) < CELL_W && Math.abs(a.y - b.y) < CELL_H;
  }

  /** Every committed anchor other than `excludeId`'s own. */
  function occupiedAnchors(excludeId) {
    var occupied = [];
    function claim(nodeId, anchor) {
      if (!anchor || nodeId === excludeId) return;
      occupied.push(anchor);
    }
    Object.keys(layoutState.positions).forEach(function (nodeId) {
      claim(nodeId, layoutState.positions[nodeId]);
    });
    if (lastWorldLayout) {
      lastWorldLayout.nodes.forEach(function (node) {
        claim(node.id, node);
      });
    }
    return occupied;
  }

  /**
   * Would `excludeId`'s box land on another building if dropped at `point`?
   * Cheap enough to call on every pointermove/keystroke for live feedback —
   * same recompute-per-move cost the readout coordinate already pays.
   */
  function wouldOverlapOccupied(point, excludeId) {
    return occupiedAnchors(excludeId).some(function (anchor) {
      return footprintsOverlap(point, anchor);
    });
  }

  /**
   * Resolve where a drop may actually land.
   *
   * A building's on-screen box is CELL_W x CELL_H, so "occupied" means "any
   * part of this box already belongs to another building" — not just "the
   * exact same point" (FR-72, FR-73: two buildings may not be committed to
   * the same anchor, and every building's hit target must stay reachable).
   * When the requested point would overlap, the nearest free anchor is chosen
   * by walking a deterministic ring of snap-step offsets — nearest first, and
   * in a fixed order at equal distance, so the same drop always resolves the
   * same way. No other building is moved to make room (FR-74).
   */
  function resolveDropAnchor(id, point) {
    var occupied = occupiedAnchors(id);
    function overlapsOccupied(candidate) {
      return occupied.some(function (anchor) {
        return footprintsOverlap(candidate, anchor);
      });
    }
    if (!overlapsOccupied(point)) return { x: point.x, y: point.y, resolved: false };

    var step = layoutState.snapToGrid ? SNAP_STEP : Math.round(CELL_W / 4);
    for (var ring = 1; ring <= 12; ring++) {
      for (var dy = -ring; dy <= ring; dy++) {
        for (var dx = -ring; dx <= ring; dx++) {
          if (Math.max(Math.abs(dx), Math.abs(dy)) !== ring) continue;
          var candidate = { x: point.x + dx * step, y: point.y + dy * step };
          if (
            !overlapsOccupied(candidate) &&
            isSafeCoordinate(candidate.x) &&
            isSafeCoordinate(candidate.y)
          ) {
            return { x: candidate.x, y: candidate.y, resolved: true };
          }
        }
      }
    }
    return { x: point.x, y: point.y, resolved: false };
  }

  function placeElement(el, point) {
    if (!el || !el.style) return;
    el.style.left = point.x + 'px';
    el.style.top = point.y + 'px';
  }

  /**
   * Commit a moved node.
   *
   * One drop is at most one request (FR-69), sent only after the gesture ends.
   * The server's answer — not the browser's optimism — becomes the committed
   * position (FR-70); if it refuses or never arrives, the building goes back
   * where it was, keeps focus and selection, and says so with a retry
   * (FR-71).
   */
  function commitMove(container, id, el, point, previous) {
    var positions = {};
    positions[id] = { x: point.x, y: point.y };
    return patchLayout([{ op: 'set_positions', positions: positions }])
      .then(function (result) {
        var committed = (result && result.positions && result.positions[id]) || point;
        placeElement(el, committed);
        pendingFocusId = id;
        announce(container, 'Moved to ' + formatCoordinate(committed));
        settleLayout();
        return true;
      })
      .catch(function () {
        placeElement(el, previous);
        if (el && el.classList) el.classList.add('is-unsaved');
        rememberFailedPlacement(id, point);
        if (el && el.focus) el.focus();
        announce(
          container,
          'That move could not be saved. The workspace is back at ' +
            formatCoordinate(previous) +
            '. Use Retry position to try again.'
        );
        return false;
      });
  }

  function bindTileDrag(container) {
    var tiles = container.querySelectorAll('.ws-map-tile[data-ws-id]');
    Array.prototype.forEach.call(tiles, function (el) {
      if (!el || typeof el.addEventListener !== 'function') return;

      el.addEventListener('pointerdown', function (event) {
        if (layoutState.status !== 'ready') return;
        if (event.button != null && event.button !== 0) return;
        // The checkbox and modifier-clicks belong to bulk selection, and must
        // never start a spatial move (FR-76).
        if (event.metaKey || event.ctrlKey || event.shiftKey) return;
        if (event.target && event.target.closest && event.target.closest('[data-ws-check]')) return;
        var id = el.getAttribute('data-ws-id');
        var origin = committedAnchor(id);
        if (!id || !origin) return;
        dragState = {
          id: id,
          el: el,
          pointerId: event.pointerId,
          startX: event.clientX,
          startY: event.clientY,
          origin: origin,
          candidate: origin,
          moved: false
        };
        if (el.setPointerCapture) el.setPointerCapture(event.pointerId);
      });

      el.addEventListener('pointermove', function (event) {
        if (!dragState || dragState.el !== el || event.pointerId !== dragState.pointerId) return;
        var dx = event.clientX - dragState.startX;
        var dy = event.clientY - dragState.startY;
        if (!dragState.moved) {
          if (Math.abs(dx) < PAN_THRESHOLD && Math.abs(dy) < PAN_THRESHOLD) return;
          dragState.moved = true;
          if (el.classList) el.classList.add('is-dragging');
          announce(container, 'Moving. Release to drop, or press Escape to cancel.');
        }
        if (event.preventDefault) event.preventDefault();
        // Screen movement becomes world movement through the current camera, so
        // a drag tracks the pointer at any zoom (FR-67).
        var moved = {
          x: dragState.origin.x + dx / camera.zoom,
          y: dragState.origin.y + dy / camera.zoom
        };
        dragState.candidate = snapPoint(moved, !!event.altKey);
        // Only the dragged element is touched: no re-render, no request
        // (FR-65, FR-69, FR-122).
        placeElement(el, dragState.candidate);
        var blocked = wouldOverlapOccupied(dragState.candidate, dragState.id);
        if (el.classList) el.classList.toggle('is-blocked', blocked);
        setDragReadout(
          container,
          dragState.candidate,
          blocked ? MOVE_BLOCKED_INSTRUCTION : undefined
        );
      });

      function finish(event, cancelled) {
        if (!dragState || dragState.el !== el) return;
        if (event && event.pointerId != null && event.pointerId !== dragState.pointerId) return;
        var state = dragState;
        dragState = null;
        if (
          el.releasePointerCapture &&
          el.hasPointerCapture &&
          el.hasPointerCapture(state.pointerId)
        ) {
          el.releasePointerCapture(state.pointerId);
        }
        if (el.classList) {
          el.classList.remove('is-dragging');
          el.classList.remove('is-blocked');
        }
        setDragReadout(container, null);
        if (!state.moved) return;
        // A gesture that became a drag must not also read as a click (FR-66).
        suppressClickFor = state.id;
        if (cancelled) {
          placeElement(el, state.origin);
          announce(container, 'Move cancelled. The workspace is back where it was.');
          return;
        }
        var target = resolveDropAnchor(state.id, state.candidate);
        if (target.resolved) {
          placeElement(el, target);
          announce(container, 'That spot was taken; moved to the nearest free one.');
        }
        commitMove(container, state.id, el, target, state.origin);
      }

      el.addEventListener('pointerup', function (event) {
        finish(event, false);
      });
      el.addEventListener('pointercancel', function (event) {
        finish(event, true);
      });
      el.addEventListener('keydown', function (event) {
        if (event.key === 'Escape' && dragState && dragState.el === el) finish(null, true);
      });
      // Runs before the selection handler bound in bindTiles, so a drop is
      // swallowed rather than re-selecting or opening the workspace.
      el.addEventListener(
        'click',
        function (event) {
          if (suppressClickFor !== el.getAttribute('data-ws-id')) return;
          suppressClickFor = null;
          if (event.preventDefault) event.preventDefault();
          if (event.stopPropagation) event.stopPropagation();
          if (event.stopImmediatePropagation) event.stopImmediatePropagation();
        },
        true
      );
    });
  }

  // setDragReadout shows the live candidate coordinate during a move. It shares
  // the banner with Build mode but never its words: a user dragging a building
  // must not be told to "choose where to build" (FR-68).
  function setDragReadout(container, point, instruction) {
    var readout = container.querySelector('[data-map-build-coords]');
    var banner = container.querySelector('[data-map-build-banner]');
    if (!readout || !banner) return;
    if (!point) {
      if (!buildState.active) banner.hidden = true;
      if (banner.classList) banner.classList.remove('is-blocked');
      return;
    }
    banner.hidden = false;
    // The banner itself carries the same red signal as the tile/district
    // outline — driven by which instruction it's showing, so every call site
    // that already passes MOVE_BLOCKED_INSTRUCTION gets this for free.
    if (banner.classList) {
      banner.classList.toggle('is-blocked', instruction === MOVE_BLOCKED_INSTRUCTION);
    }
    setBannerMode(container, instruction || MOVE_INSTRUCTION, false);
    readout.textContent = candidateLabel(point);
  }

  // setBannerMode swaps the banner's instruction and whether it offers Cancel.
  // Build mode owns the Cancel button; a drag is cancelled with Escape or by
  // dropping, so offering a button there would be a third way to do the same
  // thing.
  function setBannerMode(container, instruction, showCancel) {
    var text = container.querySelector('[data-map-build-text]');
    if (text) text.textContent = instruction;
    var cancel = container.querySelector('[data-map-build-cancel]');
    if (cancel) cancel.hidden = !showCancel;
  }

  // ---------- moving districts ----------
  //
  // A cluster move is one world-space delta applied to the group and every
  // visible descendant, preserving all relative spacing (FR-86). It is a
  // presentation move: `parent_id` is never in the payload, so no arrangement
  // of buildings can change who belongs to what (FR-8).

  var clusterDrag = null;

  /** The group's members as currently drawn, with their live anchors. */
  function clusterMembers(container, groupId) {
    if (!lastWorldLayout) return [];
    var members = [];
    lastWorldLayout.nodes.forEach(function (node) {
      if (node.groupId !== groupId) return;
      members.push({
        id: node.id,
        el: container.querySelector('.ws-map-tile[data-ws-id="' + node.id + '"]'),
        origin: committedAnchor(node.id) || { x: node.x, y: node.y }
      });
    });
    return members;
  }

  /**
   * Would this delta drop a cluster member on top of a building outside it?
   *
   * Overlap, not just exact-anchor equality, counts as a collision here too
   * (same footprint rule as resolveDropAnchor) — otherwise a group drag could
   * still bury an outside building under a member's box. A cluster collision
   * is refused rather than resolved: nudging one member would shear the
   * district, and moving the resident building would move something the user
   * never touched (FR-88).
   */
  function clusterCollides(groupId, members, delta) {
    var inCluster = Object.create(null);
    inCluster[groupId] = true;
    members.forEach(function (member) {
      inCluster[member.id] = true;
    });
    var occupied = [];
    if (lastWorldLayout) {
      lastWorldLayout.nodes.forEach(function (node) {
        if (inCluster[node.id]) return;
        occupied.push(committedAnchor(node.id) || { x: node.x, y: node.y });
      });
    }
    return members.some(function (member) {
      var candidate = { x: member.origin.x + delta.x, y: member.origin.y + delta.y };
      return occupied.some(function (anchor) {
        return footprintsOverlap(candidate, anchor);
      });
    });
  }

  function previewCluster(state, delta) {
    placeElement(state.districtEl, {
      x: state.districtOrigin.x + delta.x,
      y: state.districtOrigin.y + delta.y
    });
    state.members.forEach(function (member) {
      placeElement(member.el, { x: member.origin.x + delta.x, y: member.origin.y + delta.y });
    });
  }

  function restoreCluster(state) {
    previewCluster(state, { x: 0, y: 0 });
  }

  /**
   * Mirror .is-blocked onto the district AND every member tile, not just the
   * district's own border — the member tiles are the opaque things actually
   * covering whatever they'd land on, so they're what needs to go
   * translucent for that building to show through (see .ws-map-tile.is-
   * blocked's opacity in workspace-map.css).
   */
  function setClusterBlocked(state, blocked) {
    if (state.districtEl && state.districtEl.classList) {
      state.districtEl.classList.toggle('is-blocked', blocked);
    }
    (state.members || []).forEach(function (member) {
      if (member.el && member.el.classList) {
        member.el.classList.toggle('is-blocked', blocked);
      }
    });
  }

  /**
   * Commit a cluster move.
   *
   * The client sends the group and one delta, not a list of coordinates: the
   * server resolves the district's current members and translates their latest
   * anchors inside one transaction, so the whole cluster lands or none of it
   * does (FR-87).
   */
  function commitClusterMove(container, state, delta) {
    return patchLayout([
      { op: 'translate_group', group_id: state.groupId, delta: { x: delta.x, y: delta.y } }
    ])
      .then(function () {
        announce(container, 'District moved. Every workspace kept its place inside it.');
        pendingFocusId = '';
        settleLayout();
        return true;
      })
      .catch(function () {
        // Every prior anchor comes back together — a district cannot be left
        // half-moved (FR-87).
        restoreCluster(state);
        announce(
          container,
          'That district move could not be saved. Everything is back where it was.'
        );
        return false;
      });
  }

  function bindDistrictDrag(container) {
    var handles = container.querySelectorAll('[data-group-drag]');
    Array.prototype.forEach.call(handles, function (handle) {
      if (!handle || typeof handle.addEventListener !== 'function') return;

      handle.addEventListener('pointerdown', function (event) {
        if (layoutState.status !== 'ready') return;
        if (event.button != null && event.button !== 0) return;
        var groupId = handle.getAttribute('data-group-drag');
        var origin = committedAnchor(groupId);
        if (!groupId || !origin) return;
        clusterDrag = {
          groupId: groupId,
          handle: handle,
          pointerId: event.pointerId,
          startX: event.clientX,
          startY: event.clientY,
          districtEl: container.querySelector('.ws-map-district[data-group-id="' + groupId + '"]'),
          districtOrigin: origin,
          members: clusterMembers(container, groupId),
          delta: { x: 0, y: 0 },
          moved: false
        };
        if (handle.setPointerCapture) handle.setPointerCapture(event.pointerId);
        if (event.stopPropagation) event.stopPropagation();
        // Suppress the browser's own drag/selection behaviour so the gesture
        // moves the district instead of highlighting its label.
        if (event.preventDefault) event.preventDefault();
      });

      handle.addEventListener('pointermove', function (event) {
        if (!clusterDrag || clusterDrag.handle !== handle) return;
        if (event.pointerId !== clusterDrag.pointerId) return;
        var dx = event.clientX - clusterDrag.startX;
        var dy = event.clientY - clusterDrag.startY;
        if (!clusterDrag.moved) {
          if (Math.abs(dx) < PAN_THRESHOLD && Math.abs(dy) < PAN_THRESHOLD) return;
          clusterDrag.moved = true;
          if (clusterDrag.districtEl && clusterDrag.districtEl.classList) {
            clusterDrag.districtEl.classList.add('is-dragging');
          }
        }
        if (event.preventDefault) event.preventDefault();
        var raw = { x: dx / camera.zoom, y: dy / camera.zoom };
        var snappedTarget = snapPoint(
          { x: clusterDrag.districtOrigin.x + raw.x, y: clusterDrag.districtOrigin.y + raw.y },
          !!event.altKey
        );
        clusterDrag.delta = {
          x: snappedTarget.x - clusterDrag.districtOrigin.x,
          y: snappedTarget.y - clusterDrag.districtOrigin.y
        };
        previewCluster(clusterDrag, clusterDrag.delta);
        var blocked = clusterCollides(clusterDrag.groupId, clusterDrag.members, clusterDrag.delta);
        setClusterBlocked(clusterDrag, blocked);
        setDragReadout(container, snappedTarget, blocked ? MOVE_BLOCKED_INSTRUCTION : undefined);
      });

      function finish(event, cancelled) {
        if (!clusterDrag || clusterDrag.handle !== handle) return;
        if (event && event.pointerId != null && event.pointerId !== clusterDrag.pointerId) return;
        var state = clusterDrag;
        clusterDrag = null;
        if (
          handle.releasePointerCapture &&
          handle.hasPointerCapture &&
          handle.hasPointerCapture(state.pointerId)
        ) {
          handle.releasePointerCapture(state.pointerId);
        }
        if (state.districtEl && state.districtEl.classList) {
          state.districtEl.classList.remove('is-dragging');
        }
        setClusterBlocked(state, false);
        setDragReadout(container, null);
        if (!state.moved) return;
        if (cancelled) {
          restoreCluster(state);
          announce(container, 'District move cancelled.');
          return;
        }
        if (clusterCollides(state.groupId, state.members, state.delta)) {
          restoreCluster(state);
          announce(
            container,
            'That would land a workspace on top of one outside this district. The district is back where it was.'
          );
          return;
        }
        commitClusterMove(container, state, state.delta);
      }

      handle.addEventListener('pointerup', function (event) {
        finish(event, false);
      });
      handle.addEventListener('pointercancel', function (event) {
        finish(event, true);
      });
      handle.addEventListener('keydown', function (event) {
        if (event.key === 'Escape' && clusterDrag && clusterDrag.handle === handle) {
          finish(null, true);
        }
      });
    });
  }

  // ---------- keyboard move ----------
  //
  // The same commit contract as a drag, reachable without a pointer (FR-77).

  var moveState = null;

  // isDistrictId reports whether the selection is a group district rather than a
  // building, which decides whether a keyboard move carries one anchor or a
  // whole cluster (FR-93).
  function isDistrictId(id) {
    if (!lastWorldLayout) return false;
    return lastWorldLayout.districts.some(function (district) {
      return district.id === id;
    });
  }

  function startKeyboardMove(container) {
    if (layoutState.status !== 'ready') {
      announce(container, 'Positions cannot be saved right now, so moving is unavailable');
      return;
    }
    var anchor = selectedNodeAnchor();
    if (!selectedId || !anchor) {
      announce(container, 'Select a workspace or group first');
      return;
    }
    if (isDistrictId(selectedId)) {
      // A selected group moves as the same cluster the drag handle moves, with
      // the same commit-or-cancel contract (FR-93).
      moveState = {
        cluster: {
          groupId: selectedId,
          districtEl: container.querySelector(
            '.ws-map-district[data-group-id="' + selectedId + '"]'
          ),
          districtOrigin: anchor,
          members: clusterMembers(container, selectedId)
        },
        origin: anchor,
        candidate: anchor
      };
    } else {
      moveState = {
        id: selectedId,
        el: container.querySelector('.ws-map-tile[data-ws-id="' + selectedId + '"]'),
        origin: anchor,
        candidate: anchor
      };
    }
    var banner = container.querySelector('[data-map-build-banner]');
    if (banner) banner.hidden = false;
    setDragReadout(container, anchor, KEYBOARD_MOVE_INSTRUCTION);
    announce(
      container,
      (moveState.cluster ? 'Moving this district from ' : 'Moving from ') +
        formatCoordinate(anchor) +
        '. Arrow keys move, Enter saves, Escape cancels.'
    );
  }

  function endKeyboardMove(container, commit) {
    if (!moveState) return;
    var state = moveState;
    moveState = null;
    setDragReadout(container, null);
    var banner = container.querySelector('[data-map-build-banner]');
    if (banner && !buildState.active) banner.hidden = true;
    if (state.cluster) {
      setClusterBlocked(state.cluster, false);
    } else if (state.el && state.el.classList) {
      state.el.classList.remove('is-blocked');
    }

    if (state.cluster) {
      var delta = {
        x: state.candidate.x - state.origin.x,
        y: state.candidate.y - state.origin.y
      };
      if (!commit) {
        restoreCluster(state.cluster);
        announce(container, 'Move cancelled. The district is back where it was.');
        return;
      }
      if (clusterCollides(state.cluster.groupId, state.cluster.members, delta)) {
        restoreCluster(state.cluster);
        announce(
          container,
          'That would land a workspace on top of one outside this district. The district is back where it was.'
        );
        return;
      }
      commitClusterMove(container, state.cluster, delta);
      return;
    }

    if (!commit) {
      placeElement(state.el, state.origin);
      announce(container, 'Move cancelled. The workspace is back where it was.');
      return;
    }
    var target = resolveDropAnchor(state.id, state.candidate);
    commitMove(container, state.id, state.el, target, state.origin);
  }

  function handleMoveKey(container, event) {
    if (!moveState) return false;
    var step = layoutState.snapToGrid ? SNAP_STEP : 1;
    if (event.shiftKey) step *= 10;
    var candidate = moveState.candidate;
    var next = null;
    switch (event.key) {
      case 'ArrowLeft':
        next = { x: candidate.x - step, y: candidate.y };
        break;
      case 'ArrowRight':
        next = { x: candidate.x + step, y: candidate.y };
        break;
      case 'ArrowUp':
        next = { x: candidate.x, y: candidate.y - step };
        break;
      case 'ArrowDown':
        next = { x: candidate.x, y: candidate.y + step };
        break;
      case 'Enter':
        endKeyboardMove(container, true);
        return true;
      case 'Escape':
        endKeyboardMove(container, false);
        return true;
      default:
        return false;
    }
    moveState.candidate = next;
    var blocked;
    if (moveState.cluster) {
      var delta = { x: next.x - moveState.origin.x, y: next.y - moveState.origin.y };
      previewCluster(moveState.cluster, delta);
      blocked = clusterCollides(moveState.cluster.groupId, moveState.cluster.members, delta);
      setClusterBlocked(moveState.cluster, blocked);
    } else {
      placeElement(moveState.el, next);
      blocked = wouldOverlapOccupied(next, moveState.id);
      if (moveState.el && moveState.el.classList) {
        moveState.el.classList.toggle('is-blocked', blocked);
      }
    }
    setDragReadout(container, next, blocked ? MOVE_BLOCKED_INSTRUCTION : KEYBOARD_MOVE_INSTRUCTION);
    announce(container, 'Candidate ' + candidateLabel(next));
    return true;
  }

  // ---------- reset and undo ----------
  //
  // Reset Layout clears the user's arrangement, not their workspaces. It is
  // separate from Reset View in name, in placement, and in what it touches
  // (FR-109, FR-111).

  // The exact position set as it stood before the last reset, held in memory
  // for the session. A permanent history table would be a lot of machinery for
  // one undo of one action (FR-112).
  var undoSnapshot = null;

  function resetLayout(container) {
    if (layoutState.status !== 'ready') {
      announce(container, 'Positions cannot be saved right now, so Reset layout is unavailable');
      return;
    }
    var count = Object.keys(layoutState.positions).length;
    var message =
      'Reset the map layout?\n\n' +
      'Every workspace and district goes back to an automatic position. ' +
      'Nothing is deleted, renamed, regrouped or reordered — only where things sit on your map changes. ' +
      'Your Snap to Grid setting is kept, and you can undo this during this session.' +
      (count
        ? '\n\n' + count + ' saved position' + (count === 1 ? '' : 's') + ' will be cleared.'
        : '');
    if (
      typeof window !== 'undefined' &&
      typeof window.confirm === 'function' &&
      !window.confirm(message)
    ) {
      return;
    }

    // Snapshot before asking the server, so Undo restores exactly what was
    // there rather than whatever survived the reset.
    var snapshot = {};
    Object.keys(layoutState.positions).forEach(function (id) {
      snapshot[id] = { x: layoutState.positions[id].x, y: layoutState.positions[id].y };
    });

    deleteLayout()
      .then(function () {
        undoSnapshot = snapshot;
        layoutState.positions = Object.create(null);
        announce(
          container,
          'Layout reset. Every workspace is at an automatic position; nothing else changed. Undo reset is available.'
        );
        settleLayout();
        fitAll(lastMount ? lastMount.container : container);
      })
      .catch(function () {
        // A failed reset changes nothing, and says so (FR-112).
        announce(container, 'The layout could not be reset. Everything is still where it was.');
      });
  }

  function undoReset(container) {
    if (!undoSnapshot) return;
    var snapshot = undoSnapshot;
    patchLayout([{ op: 'restore_positions', positions: snapshot }])
      .then(function () {
        undoSnapshot = null;
        Object.keys(snapshot).forEach(function (id) {
          layoutState.positions[id] = snapshot[id];
        });
        announce(container, 'Reset undone. Every workspace is back where it was.');
        settleLayout();
      })
      .catch(function () {
        // The post-reset state is still valid and still saved; only the undo
        // failed, so the offer stays (FR-112).
        announce(
          container,
          'That could not be undone. The reset layout is unchanged — try Undo reset again.'
        );
      });
  }

  function deleteLayout() {
    if (typeof fetch !== 'function') return Promise.reject(new Error('unavailable'));
    return fetch(LAYOUT_ENDPOINT, {
      method: 'DELETE',
      headers: { Accept: 'application/json' }
    }).then(function (response) {
      if (!response || !response.ok) throw new Error('workspace map layout reset failed');
      return response.json();
    });
  }

  function bindResetLayout(container) {
    var reset = container.querySelector('[data-map-reset-layout]');
    if (reset && reset.addEventListener) {
      reset.addEventListener('click', function () {
        resetLayout(container);
      });
    }
    var undo = container.querySelector('[data-map-undo-reset]');
    if (undo) {
      undo.hidden = !undoSnapshot;
      if (undo.addEventListener) {
        undo.addEventListener('click', function () {
          undoReset(container);
        });
      }
    }
    var help = container.querySelector('[data-map-help]');
    var panel = container.querySelector('[data-map-help-panel]');
    if (help && panel && help.addEventListener) {
      help.addEventListener('click', function () {
        panel.hidden = !panel.hidden;
        if (help.setAttribute) help.setAttribute('aria-expanded', panel.hidden ? 'false' : 'true');
      });
    }
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
    setBannerMode(container, BUILD_INSTRUCTION, true);
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
    // Two buildings may not be committed to the same anchor, and a snapped
    // build click lands on the grid exactly like a snapped drop does — so
    // placement resolves collisions through the same rule (FR-72).
    var target = resolveDropAnchor(workspaceId, point);
    point = { x: target.x, y: target.y };
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
    setBannerMode(container, BUILD_INSTRUCTION, true);
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
    // The re-render below destroys the menu's host along with everything else.
    // Closing it first keeps the listeners and the focus-restore target from
    // outliving the DOM they referred to.
    closeContextMenu({ restoreFocus: false });
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
    bindContextMenu(container, workspaces, state);
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
    bindTileDrag(container);
    bindDistrictDrag(container);
    bindResetLayout(container);
    // A re-render (a refresh landing mid-placement) rebuilt the banner and the
    // marker, so restore what the user was in the middle of.
    if (buildState.active) restoreBuildMode(container);
    // Focus returns to the record it was on before a committed move or a
    // refresh re-rendered the map (FR-117).
    if (pendingFocusId) {
      var refocus = container.querySelector('.ws-map-tile[data-ws-id="' + pendingFocusId + '"]');
      pendingFocusId = '';
      if (refocus && refocus.focus) refocus.focus();
    }
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
    closeContextMenu({ restoreFocus: false });
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
    // Clear the multi-select set once the host finishes a bulk action. Delete
    // needs no call — mount() already prunes ids that stopped existing — but a
    // grouped workspace still exists, so without this its tile would come back
    // still checked and the action bar would still claim a live selection.
    clearMultiSelection: function () {
      multiSelected = Object.create(null);
      if (lastMount && lastMount.container) updateSelBar(lastMount.container);
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
    hasUndoableReset: function () {
      return !!undoSnapshot;
    },
    computeStats: computeStats,
    // The context menu's pure halves: the item set a target offers, the markup
    // that renders it, and the edge-flipping placement math (FR-17, FR-123).
    contextMenuItemsFor: contextMenuItemsFor,
    contextMenuHTML: contextMenuHTML,
    contextMenuPosition: menuPosition,
    closeContextMenu: function () {
      closeContextMenu({ restoreFocus: false });
    },
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
