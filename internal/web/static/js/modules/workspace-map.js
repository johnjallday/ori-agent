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
  // A building's visible footprint inside its cell. The cell pitch above leaves
  // a gutter; this is the part a district frame has to enclose.
  var MEMBER_W = CELL_W - 8;
  var MEMBER_H = CELL_H - 10;
  // The district frame is drawn around its members with this much slack on every
  // side, and a group's own anchor sits at the frame's corner rather than on a
  // member's cell — otherwise an empty group and its first member would want the
  // same point (FR-82, FR-84).
  //
  // The padding is deliberately *half the cell gutter* (#346). An automatic
  // frame around one grid-aligned member is then exactly one cell — so it
  // reaches the neighbouring cell's edge and stops. Edges that touch without
  // sharing area are allowed (FR-80), which means the default arrangement
  // produces no false-containment conflicts at all, while any larger padding
  // would make every district overlap its neighbour the moment it was drawn.
  var DISTRICT_PAD_X = (CELL_W - MEMBER_W) / 2;
  var DISTRICT_PAD_Y = (CELL_H - MEMBER_H) / 2;
  // The documented minimum district (FR-43): one cell, which is the size an
  // empty group renders at and the floor a manual resize clamps to. Mirrored by
  // MinFrameWidth/MinFrameHeight in internal/workspacemap/presentation.go.
  var DISTRICT_MIN_W = CELL_W;
  var DISTRICT_MIN_H = CELL_H;
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
  //
  // Zoom bottoms out at 10% (#307). The old 50% floor was set for legibility —
  // below it a building is a smudge — but it made Fit All a liar on any map
  // spread wider than two viewports, and it left the camera somewhere the user
  // could not zoom back out of by hand. One floor, applied to framing and to
  // gestures alike, keeps "show me everything" honest and keeps every view the
  // map can reach a view the user can also get to. 10% is low enough for a very
  // wide arrangement and still stops one stray coordinate from rendering the
  // whole map as a dot.
  var MIN_ZOOM = 0.1;
  var MAX_ZOOM = 2;
  var DEFAULT_ZOOM = 1;
  // One press of Zoom In/Out. Small enough to feel like a step, large enough
  // that crossing the range does not take a dozen presses.
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

  // The banner's voice: a move reports where the thing being moved would land.
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
    // Per-group district presentation, keyed by immutable group id: the
    // rectangle the user sized by hand, whether the district is collapsed, and
    // which curated accent and theme it wears (#346 FR-173). A group absent from
    // here renders with DEFAULT_PRESENTATION, so an older layout that has no
    // district records at all reads as compact automatic districts.
    groups: Object.create(null),
    viewport: null,
    snapToGrid: true,
    revision: 0,
    schemaVersion: 1
  };
  // Bumped for every layout request. A response whose sequence no longer
  // matches is a stale answer to a question nobody is asking any more, and is
  // dropped rather than painted over the current world.
  var layoutRequestSeq = 0;

  // Pointer translation is an explicit, page-local mode (#374). It is off for
  // every new page session and intentionally does not belong to layout,
  // settings, or local storage. Keeping it at module scope lets ordinary Map
  // redraws and Home's Map/Tree hide/show cycle preserve the user's choice.
  var dragModeEnabled = false;

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

  // A stored camera is usable or it is not. Zoom outside the range the map can
  // actually restore is treated as unreadable rather than clamped into range,
  // so an unusable saved view opens on a fitted one instead of on an arbitrary
  // rescue of it (FR-45).
  function safeViewport(raw) {
    if (!raw || typeof raw !== 'object') return null;
    var center = safePoint({ x: raw.center_x, y: raw.center_y });
    var zoom = Number(raw.zoom);
    if (!center || !isFinite(zoom)) return null;
    if (zoom < MIN_ZOOM || zoom > MAX_ZOOM) return null;
    return { centerX: center.x, centerY: center.y, zoom: zoom };
  }

  // ---------- district presentation (#346) ----------
  //
  // Accent and theme are curated, app-defined identifiers, never CSS. The
  // catalogs below are the only values that ever reach a class name, so a
  // record written by a newer build — or a hostile one — falls back to the
  // default instead of being interpolated into a stylesheet (FR-125, FR-194).
  // They mirror accentCatalog/themeCatalog in
  // internal/workspacemap/presentation.go, which validates every write.
  // Named, not unlabelled colour dots: a district is identified by its name and
  // its shape as well as its colour, and a screen reader has to be able to say
  // which one is chosen (FR-130, design §"Accent choices should be named").
  var DISTRICT_ACCENT_CATALOG = [
    { id: 'default', label: 'Keeper amber' },
    { id: 'beacon', label: 'Beacon blue' },
    { id: 'moss', label: 'Moss green' },
    { id: 'orchid', label: 'Orchid violet' },
    { id: 'slate', label: 'Slate grey' },
    { id: 'tide', label: 'Tide teal' }
  ];
  var DISTRICT_THEME_CATALOG = [
    { id: 'default', label: 'Standard district', hint: 'Dashed outline, faint fill' },
    { id: 'blueprint', label: 'Blueprint', hint: 'Solid rule with a grid hatch' },
    { id: 'terrace', label: 'Terrace', hint: 'Rounded surface with a header band' }
  ];
  var DISTRICT_ACCENTS = DISTRICT_ACCENT_CATALOG.map(function (entry) {
    return entry.id;
  });
  var DISTRICT_THEMES = DISTRICT_THEME_CATALOG.map(function (entry) {
    return entry.id;
  });
  var DEFAULT_ACCENT = 'default';
  var DEFAULT_THEME = 'default';

  function safeAccent(value) {
    return DISTRICT_ACCENTS.indexOf(value) === -1 ? DEFAULT_ACCENT : value;
  }
  function safeTheme(value) {
    return DISTRICT_THEMES.indexOf(value) === -1 ? DEFAULT_THEME : value;
  }

  // safeFrame is the read-side rectangle guard. A frame is usable whole or not
  // at all: half a rectangle is not a frame, so anything missing, non-finite,
  // non-positive, below the documented minimum, or reaching outside the safe
  // world drops the custom frame entirely and that district falls back to
  // automatic sizing, which is always drawable (FR-44, FR-45, FR-192).
  function safeFrame(raw) {
    if (!raw || typeof raw !== 'object') return null;
    var x = Number(raw.x);
    var y = Number(raw.y);
    var width = Number(raw.width);
    var height = Number(raw.height);
    if (!isSafeCoordinate(x) || !isSafeCoordinate(y)) return null;
    if (!isFinite(width) || !isFinite(height)) return null;
    if (width < DISTRICT_MIN_W || height < DISTRICT_MIN_H) return null;
    if (!isSafeCoordinate(x + width) || !isSafeCoordinate(y + height)) return null;
    return { x: x, y: y, width: width, height: height };
  }

  // The presentation a district with no saved record renders as: automatic
  // sizing, expanded, default appearance (FR-18 – FR-20, FR-31, FR-101).
  function defaultPresentation() {
    return {
      sizingMode: 'auto',
      frame: null,
      collapsed: false,
      accent: DEFAULT_ACCENT,
      theme: DEFAULT_THEME
    };
  }

  // safePresentation normalizes one stored district record. Every facet degrades
  // on its own, so an unknown accent costs that district its colour and nothing
  // else, and one corrupt record never disturbs a sibling (FR-192).
  function safePresentation(raw) {
    var record = defaultPresentation();
    if (!raw || typeof raw !== 'object') return record;
    record.collapsed = raw.collapsed === true;
    record.accent = safeAccent(raw.accent);
    record.theme = safeTheme(raw.theme);
    if (raw.sizing_mode === 'custom' || raw.sizingMode === 'custom') {
      var frame = safeFrame(raw.frame);
      if (frame) {
        record.sizingMode = 'custom';
        record.frame = frame;
      }
    }
    return record;
  }

  // Read one district's presentation out of the shared layout state.
  function presentationFor(groupId) {
    return (groupId && layoutState.groups[groupId]) || defaultPresentation();
  }

  // A district that carries nothing but defaults is indistinguishable from one
  // with no record at all, and is stored as neither.
  function isDefaultPresentation(record) {
    return (
      !!record &&
      record.sizingMode === 'auto' &&
      !record.frame &&
      !record.collapsed &&
      record.accent === DEFAULT_ACCENT &&
      record.theme === DEFAULT_THEME
    );
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

    var groups = Object.create(null);
    var rawGroups = (payload && payload.groups) || {};
    Object.keys(rawGroups).forEach(function (id) {
      if (!id || id === HQ_SITE_ID) return;
      groups[id] = safePresentation(rawGroups[id]);
    });
    layoutState.groups = groups;

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

        // Revisions are server-issued and monotonic, so a response that carries
        // an older one is the answer to a question a newer write has already
        // superseded. Applying it would roll local state backwards — the
        // classic out-of-order-response bug — so it is accepted as a completed
        // request and dropped as a source of truth (#346 FR-189).
        var revision = typeof result.revision === 'number' ? result.revision : null;
        if (revision !== null && revision < layoutState.revision) return result;
        if (revision !== null) layoutState.revision = revision;

        if (typeof result.snap_to_grid === 'boolean') layoutState.snapToGrid = result.snap_to_grid;
        var viewport = safeViewport(result.viewport);
        if (viewport) layoutState.viewport = viewport;
        Object.keys(result.positions || {}).forEach(function (id) {
          var point = safePoint(result.positions[id]);
          if (point) layoutState.positions[id] = point;
        });
        // Districts reconcile from the canonical record the server returned —
        // including the facets this request did not mention — so the client
        // never merges a fragment into state it may already have wrong (#346
        // FR-190). A record that came back at every default is dropped rather
        // than stored, matching how the server stops storing it.
        Object.keys(result.groups || {}).forEach(function (id) {
          if (!id || id === HQ_SITE_ID) return;
          var record = safePresentation(result.groups[id]);
          if (isDefaultPresentation(record)) delete layoutState.groups[id];
          else layoutState.groups[id] = record;
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
  // hideChromeMode  — Home's on-demand context modal owns the overview and the
  //                   workspace-area header owns the title,
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
    // mount() announces a focus-intent selection itself, exactly once. Remember
    // whether this call is the one that consumed it, so the refresh below does
    // not announce the same selection a second time.
    var focusPending = !hqFocusConsumed;
    if (lastMount) mount(lastMount.container, lastMount.state);
    var announcedByMount = focusPending && hqFocusConsumed && selectedId === HQ_SITE_ID;

    // The host renders the site's own choices — cockpit mode has no map-owned
    // overview panel — from a view it was handed once. A status change rewrites
    // that view, so without this the rail keeps offering stale actions: "Not
    // now" survives being clicked, and a repaired HQ still reads as broken.
    var opts = lastMount && lastMount.state;
    if (
      !announcedByMount &&
      selectedId === HQ_SITE_ID &&
      opts &&
      typeof opts.onSelectHQSite === 'function'
    ) {
      opts.onSelectHQSite(hqSiteView(hqStatus));
    }
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
    // A populated district's frame comes from its members, so its own stored
    // anchor is not part of the layout any more (#346 FR-16, FR-25). Dropping it
    // here rather than only at draw time is what keeps a stale anchor out of
    // fallbackOrigin and out of the cell scan too — otherwise a group anchor
    // left far below the arranged content would still push new records and the
    // Personal HQ site down there, and Fit all would still zoom out to reach
    // them.
    var populatedGroups = Object.create(null);
    grid.tiles.forEach(function (tile) {
      if (tile.groupId) populatedGroups[tile.groupId] = true;
    });

    var saved = Object.create(null);
    Object.keys(savedInput).forEach(function (id) {
      if (!present[id] || populatedGroups[id]) return;
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
    //
    //    Only an EMPTY district needs one. A populated district resolves its
    //    frame from its members, so handing it a fallback cell would claim
    //    space nothing draws in and shift every automatic placement after it.
    var districtAnchors = Object.create(null);
    grid.districts.forEach(function (district) {
      if (populatedGroups[district.id]) return;
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

    // 4. District frames. Each district resolves ONE effective frame, and its
    //    top-left corner is both the visible outline and the logical anchor —
    //    there is no second hidden anchor that can enlarge what is drawn (#346
    //    FR-29, FR-46). A populated automatic district follows only its members,
    //    so a stale or fallback group anchor sitting far from them can no longer
    //    stretch the outline across empty world and drag Fit all out with it
    //    (FR-16, FR-25).
    var membersByGroup = Object.create(null);
    nodes.forEach(function (node) {
      if (!node.groupId) return;
      (membersByGroup[node.groupId] = membersByGroup[node.groupId] || []).push(node);
    });
    var presentations = opts.groupPresentations || null;
    var districts = grid.districts.map(function (district) {
      var record = presentations
        ? safePresentation(presentations[district.id])
        : presentationFor(district.id);
      var members = membersByGroup[district.id] || [];
      // A collapsed district resolves its expanded frame anyway: its corner is
      // where the summary sits, and expanding has to restore exactly the
      // rectangle that was there before (#346 FR-114, FR-116).
      var frame = effectiveDistrictFrame({
        anchor: districtAnchors[district.id],
        members: members,
        presentation: record
      });
      var box = record.collapsed
        ? { x: frame.x, y: frame.y, width: DISTRICT_MIN_W, height: DISTRICT_MIN_H }
        : frame;
      return {
        id: district.id,
        ws: district.ws,
        // The frame's corner IS the anchor (FR-17, FR-46).
        anchorX: box.x,
        anchorY: box.y,
        x: box.x,
        y: box.y,
        width: box.width,
        height: box.height,
        // The frame the district returns to when it is expanded again.
        expandedFrame: frame,
        sizingMode: record.sizingMode,
        customFrame: record.frame,
        collapsed: record.collapsed,
        accent: record.accent,
        theme: record.theme,
        memberCount: members.length,
        saved: !!savedInput[district.id]
      };
    });

    // 5. Hidden descendants. A collapsed district's members keep their
    //    coordinates and their membership — they simply stop being drawn
    //    (#346 FR-104). Dropping them from `nodes` here is what makes them
    //    unfocusable, un-clickable, un-right-clickable, and absent from Fit
    //    all's bounds all at once, rather than each of those being a separate
    //    rule that could be forgotten (FR-105, FR-112).
    var collapsedGroups = Object.create(null);
    districts.forEach(function (district) {
      if (district.collapsed) collapsedGroups[district.id] = true;
    });
    var hiddenNodes = nodes.filter(function (node) {
      return node.groupId && collapsedGroups[node.groupId];
    });
    if (hiddenNodes.length) {
      nodes = nodes.filter(function (node) {
        return !(node.groupId && collapsedGroups[node.groupId]);
      });
    }

    // A hierarchy change made in Tree or another tab is authoritative: a
    // workspace reparented into this group can land its frame across an
    // unrelated building, and the Map's job then is to show what is actually
    // true and say the layout needs attention — not to quietly move something
    // to make the picture tidy (#346 FR-87 – FR-89). Marking it is a read-only
    // observation; nothing here writes. It runs against the VISIBLE nodes: a
    // building hidden inside a collapsed district is not something a frame can
    // be seen to enclose (FR-78).
    districts.forEach(function (district) {
      var conflict = frameConflict(district, district.id, { nodes: nodes, districts: districts });
      district.conflict = conflict.blocked ? conflict : null;
    });

    // 6. The reserved Personal HQ site gets a stable automatic anchor from the
    //    same scan, but it is never persisted: it is not a workspace, so it may
    //    not occupy a workspace ID in the saved layout (FR-30).
    //
    //    The ordinary New Workspace pad used to take an anchor here too. It was
    //    removed with #367: creating a workspace is offered by the map topbar
    //    and by Home's workspace-area header, so the pad was a third copy that
    //    also had to be placed, drawn, and — worst of all — included in the
    //    world bounds, pushing Fit all out toward a site nothing is drawn in.
    var hqSite = opts.hqSite ? placer.next() : null;

    var bounds = worldBounds(nodes, districts, hqSite);
    return {
      nodes: nodes,
      // Kept for the movement path, which has to translate hidden descendants
      // atomically with the collapsed district they belong to (FR-113).
      hiddenNodes: hiddenNodes,
      districts: districts,
      hqSite: hqSite,
      bounds: bounds,
      world: expandWorld(bounds, viewport)
    };
  }

  // ---------- district effective frames (#346 FR-29 – FR-51) ----------

  /**
   * memberBounds is the padded rectangle a district's current members require.
   *
   * This is the floor for every frame: no district may be smaller than this, or
   * it would be visibly clipping a workspace it claims to contain (FR-76).
   * Returns null for an empty group, which has no contents to follow.
   */
  function memberBounds(members) {
    if (!members || !members.length) return null;
    var minX = Infinity;
    var minY = Infinity;
    var maxX = -Infinity;
    var maxY = -Infinity;
    members.forEach(function (member) {
      if (member.x < minX) minX = member.x;
      if (member.y < minY) minY = member.y;
      if (member.x + MEMBER_W > maxX) maxX = member.x + MEMBER_W;
      if (member.y + MEMBER_H > maxY) maxY = member.y + MEMBER_H;
    });
    return {
      x: minX - DISTRICT_PAD_X,
      y: minY - DISTRICT_PAD_Y,
      width: maxX - minX + DISTRICT_PAD_X * 2,
      height: maxY - minY + DISTRICT_PAD_Y * 2
    };
  }

  // unionFrames returns the smallest rectangle containing both.
  function unionFrames(a, b) {
    if (!a) return b;
    if (!b) return a;
    var minX = Math.min(a.x, b.x);
    var minY = Math.min(a.y, b.y);
    var maxX = Math.max(a.x + a.width, b.x + b.width);
    var maxY = Math.max(a.y + a.height, b.y + b.height);
    return { x: minX, y: minY, width: maxX - minX, height: maxY - minY };
  }

  // clampFrameToWorld keeps a resolved frame finite and inside the documented
  // safe world (FR-44). It is a last guard, not a sizing rule: every input it
  // sees has already been validated, so in practice it only fires for a member
  // parked at the very edge of the world.
  function clampFrameToWorld(frame) {
    var width = Math.max(DISTRICT_MIN_W, Math.min(frame.width, MAX_COORD - MIN_COORD));
    var height = Math.max(DISTRICT_MIN_H, Math.min(frame.height, MAX_COORD - MIN_COORD));
    var x = Math.min(Math.max(frame.x, MIN_COORD), MAX_COORD - width);
    var y = Math.min(Math.max(frame.y, MIN_COORD), MAX_COORD - height);
    return { x: x, y: y, width: width, height: height };
  }

  /**
   * The one effective-frame rule, pure and viewport-independent (FR-51).
   *
   *   populated + auto   → the tight padded box around the rendered members.
   *                        Nothing else contributes, so an unrelated group
   *                        anchor cannot enlarge it (FR-32, FR-25).
   *   empty + auto       → a minimum-size district at the group's own valid
   *                        saved or deterministic fallback anchor (FR-24).
   *   custom             → the union of the user's saved minimum rectangle and
   *                        whatever its members currently require, so it expands
   *                        for an outward member (FR-36) but never shrinks on
   *                        its own when one moves back in (FR-37).
   *
   * @param {{anchor: {x:number,y:number}, members: Array, presentation: object}} input
   */
  function effectiveDistrictFrame(input) {
    var members = memberBounds(input.members);
    var record = input.presentation || defaultPresentation();
    var frame;
    if (record.sizingMode === 'custom' && record.frame) {
      frame = unionFrames(record.frame, members);
    } else if (members) {
      frame = members;
    } else {
      var anchor = input.anchor || { x: 0, y: 0 };
      frame = { x: anchor.x, y: anchor.y, width: DISTRICT_MIN_W, height: DISTRICT_MIN_H };
    }
    // Never below the documented minimum, whatever the contents (FR-43).
    frame = {
      x: frame.x,
      y: frame.y,
      width: Math.max(DISTRICT_MIN_W, frame.width),
      height: Math.max(DISTRICT_MIN_H, frame.height)
    };
    return clampFrameToWorld(frame);
  }

  // ---------- district resizing, pure (#346 FR-52 – FR-82) ----------
  //
  // All eight handles, the member minimum, the documented minimum, the safe
  // world, snapping, and the collision rule are decided here and nowhere else.
  // Pointer, keyboard, context-menu, and rail paths all call these, which is
  // what stops four surfaces from developing four different answers (FR-156).

  // The eight resize handles, in a stable order: four corners and four edges.
  var RESIZE_HANDLES = ['n', 'ne', 'e', 'se', 's', 'sw', 'w', 'nw'];

  // Human names, used for accessible labels and blocked-state copy (FR-69).
  var RESIZE_HANDLE_LABELS = {
    n: 'top edge',
    s: 'bottom edge',
    e: 'right edge',
    w: 'left edge',
    ne: 'top-right corner',
    nw: 'top-left corner',
    se: 'bottom-right corner',
    sw: 'bottom-left corner'
  };

  /**
   * Do two rectangles share area?
   *
   * Strictly: rectangles that merely touch are NOT overlapping (FR-80). That is
   * what makes the default arrangement conflict-free — an automatic frame around
   * a grid-aligned member is exactly one cell, so it reaches its neighbour's
   * edge and stops.
   */
  function rectsOverlap(a, b) {
    return (
      a.x < b.x + b.width && b.x < a.x + a.width && a.y < b.y + b.height && b.y < a.y + a.height
    );
  }

  /**
   * Would this frame claim something that is not in the group?
   *
   * A district frame is a membership boundary, so a rectangle drawn around an
   * unrelated workspace — or across another district — is the Map telling a lie
   * about the hierarchy. Both are refused (FR-78, FR-79); the group's own
   * members never conflict with their own frame.
   *
   * @returns {{blocked: boolean, reason: string, name: string}}
   */
  function frameConflict(frame, groupId, layout) {
    var world = layout || lastWorldLayout;
    if (!world || !frame) return { blocked: false, reason: '', name: '' };

    var hit = null;
    world.nodes.forEach(function (node) {
      if (hit || node.groupId === groupId) return;
      var footprint = { x: node.x, y: node.y, width: MEMBER_W, height: MEMBER_H };
      if (rectsOverlap(frame, footprint)) {
        hit = {
          blocked: true,
          reason: 'workspace',
          name: (node.ws && node.ws.name) || 'a workspace'
        };
      }
    });
    if (hit) return hit;

    world.districts.forEach(function (district) {
      if (hit || district.id === groupId) return;
      if (rectsOverlap(frame, district)) {
        hit = {
          blocked: true,
          reason: 'district',
          name: (district.ws && district.ws.name) || 'another group'
        };
      }
    });
    return hit || { blocked: false, reason: '', name: '' };
  }

  /**
   * Apply a resize gesture to a frame.
   *
   * Only the dragged edges move; the opposite edge or corner is pinned
   * (FR-58, FR-59). The result is clamped rather than refused: a resize pushed
   * past its members stops *at* them, so the gesture stays continuous and no
   * member is ever hidden or clipped (FR-77).
   *
   * @param {{x:number,y:number,width:number,height:number}} frame committed frame
   * @param {string} handle one of RESIZE_HANDLES
   * @param {{x:number,y:number}} delta world-space pointer delta
   * @param {{snap?: boolean, contentBounds?: object}} [options]
   * @returns {{frame: object, clamped: boolean, changed: boolean}}
   */
  function resizeFrame(frame, handle, delta, options) {
    var opts = options || {};
    var snap = !!opts.snap;
    var content = opts.contentBounds || null;
    var h = String(handle || '');
    var move = delta || { x: 0, y: 0 };

    // A gesture that has not moved is not a resize. Without this, snapping
    // would pull an already-off-grid edge onto the grid on pointer-DOWN, so a
    // click that never dragged would resize the district (FR-64).
    if (!move.x && !move.y) {
      return {
        frame: { x: frame.x, y: frame.y, width: frame.width, height: frame.height },
        clamped: false,
        changed: false
      };
    }

    var left = frame.x;
    var top = frame.y;
    var right = frame.x + frame.width;
    var bottom = frame.y + frame.height;
    var clamped = false;

    function place(value) {
      return snap ? snapValue(value) : value;
    }
    function clampTo(value, limit, towardMax) {
      var limited = towardMax ? Math.max(value, limit) : Math.min(value, limit);
      if (limited !== value) clamped = true;
      return limited;
    }

    if (h.indexOf('w') !== -1) {
      // The west edge may not pass the leftmost member, may not squeeze the
      // frame below its minimum, and may not leave the safe world.
      var westLimit = right - DISTRICT_MIN_W;
      if (content) westLimit = Math.min(westLimit, content.x);
      left = clampTo(place(left + move.x), westLimit, false);
      left = clampTo(left, MIN_COORD, true);
    }
    if (h.indexOf('e') !== -1) {
      var eastLimit = left + DISTRICT_MIN_W;
      if (content) eastLimit = Math.max(eastLimit, content.x + content.width);
      right = clampTo(place(right + move.x), eastLimit, true);
      right = clampTo(right, MAX_COORD, false);
    }
    if (h.indexOf('n') !== -1) {
      var northLimit = bottom - DISTRICT_MIN_H;
      if (content) northLimit = Math.min(northLimit, content.y);
      top = clampTo(place(top + move.y), northLimit, false);
      top = clampTo(top, MIN_COORD, true);
    }
    if (h.indexOf('s') !== -1) {
      var southLimit = top + DISTRICT_MIN_H;
      if (content) southLimit = Math.max(southLimit, content.y + content.height);
      bottom = clampTo(place(bottom + move.y), southLimit, true);
      bottom = clampTo(bottom, MAX_COORD, false);
    }

    var next = { x: left, y: top, width: right - left, height: bottom - top };
    return {
      frame: next,
      clamped: clamped,
      changed:
        next.x !== frame.x ||
        next.y !== frame.y ||
        next.width !== frame.width ||
        next.height !== frame.height
    };
  }

  /**
   * What a custom district's stored minimum has to become for a member move
   * (#346 FR-35, FR-36, FR-38, FR-39).
   *
   * Only `custom` districts have a stored minimum to reconcile. An automatic one
   * follows its members on every render, so a move needs no frame write at all —
   * which is what keeps the ordinary case a single-operation patch.
   *
   * The result is the union of the stored minimum and what the members need
   * *after* the move. It never shrinks: a member pulled inward, removed,
   * trashed, or reparented away leaves the reserved room exactly as the user
   * chose it (FR-37).
   *
   * @param {object} district a rendered district
   * @param {Array} members the district's rendered member nodes
   * @param {string} memberId the member being moved
   * @param {{x:number,y:number}} point where it is going
   * @returns {{frame: object, changed: boolean}|null} null when there is nothing to reconcile
   */
  function reconcileCustomFrame(district, members, memberId, point) {
    if (!district || district.sizingMode !== 'custom' || !district.customFrame) return null;
    var moved = (members || []).map(function (member) {
      return member.id === memberId ? { x: point.x, y: point.y } : { x: member.x, y: member.y };
    });
    var required = memberBounds(moved);
    var next = unionFrames(district.customFrame, required);
    var stored = district.customFrame;
    return {
      frame: next,
      changed:
        next.x !== stored.x ||
        next.y !== stored.y ||
        next.width !== stored.width ||
        next.height !== stored.height
    };
  }

  /**
   * Which district would a dropped workspace land inside? (#346 FR-6a)
   *
   * Evaluated against the frames as they stand BEFORE the drop, which is what
   * makes the question well-posed: a member dragged past its own group's edge
   * is outside that frame at the moment of release, even though the frame would
   * grow to follow it afterwards.
   *
   * A collapsed district is not a drop target. Its members are not on screen, so
   * "inside it" is not something a user can see or aim at — dropping onto a
   * compact summary would be a guess, not a gesture.
   *
   * Smallest match wins. Districts do not nest visually in V1, but a stale
   * hierarchy refresh can leave two frames overlapping, and the tighter one is
   * the one the pointer is most plausibly aiming at.
   *
   * @returns {object|null} the district, or null when the point is on open ground
   */
  /** The middle of a workspace whose stored top-left anchor is `point`. */
  function memberCenter(point) {
    if (!point) return null;
    return { x: point.x + MEMBER_W / 2, y: point.y + MEMBER_H / 2 };
  }

  function districtAtPoint(point, layout) {
    var world = layout || lastWorldLayout;
    if (!world || !point) return null;
    var best = null;
    world.districts.forEach(function (district) {
      if (district.collapsed) return;
      var inside =
        point.x >= district.x &&
        point.x <= district.x + district.width &&
        point.y >= district.y &&
        point.y <= district.y + district.height;
      if (!inside) return;
      var area = district.width * district.height;
      if (!best || area < best.area) best = { district: district, area: area };
    });
    return best ? best.district : null;
  }

  /**
   * What a drop at `point` means for the dragged workspace's membership.
   *
   * An eligible expanded district means `join`, unless it is already the
   * record's current group. Genuinely open ground means `leave` only when the
   * moving record has a valid group parent. Top-level records stay top-level.
   * Every name remains raw data here and is escaped at its rendering boundary.
   *
   * @returns {{kind: 'none'|'join'|'leave', groupId?: string, name?: string, movingName?: string, reason?: string}}
   */
  function dropMembershipIntent(id, point, workspaces, layout) {
    var world = layout || lastWorldLayout;
    var node = null;
    (world.nodes || []).forEach(function (candidate) {
      if (candidate.id === id) node = candidate;
    });
    var moving = findWs(workspaces, id);
    var source = moving && moving.parent_id ? findWs(workspaces, moving.parent_id) : null;
    if (!source || !isGroup(source)) source = null;

    // Aim from the middle of the workspace, not from its top-left anchor. The
    // anchor is where the tile is *stored*; its centre is where it *looks like
    // it is*. Testing the anchor makes the gesture lopsided — a tile covering a
    // frame's top-left corner would not join, while one hanging off the
    // bottom-right by all but a pixel would.
    var target = districtAtPoint(memberCenter(point), world);
    if (!target) {
      if (!source) return { kind: 'none' };
      return {
        kind: 'leave',
        groupId: source.id,
        name: source.name || 'its current group',
        movingName: (moving && moving.name) || 'this workspace'
      };
    }
    if (target.id === id) return { kind: 'none' };

    // Already in it: this is a reposition, not a join. Membership comes from
    // the hierarchy record, not visual containment, so nested records work even
    // when only top-level districts are drawn.
    if ((source && source.id === target.id) || (node && node.groupId === target.id)) {
      return { kind: 'none' };
    }

    // A group may not be dropped into itself or into its own descendant. Tree
    // owns that rule; the Map asks Tree's own validator rather than inventing a
    // second one that could disagree with it.
    var rejection = dropRejectionReason(workspaces, id, target.id);
    if (rejection) return { kind: 'none', reason: rejection };

    return {
      kind: 'join',
      groupId: target.id,
      name: (target.ws && target.ws.name) || 'that group',
      // Carried on the intent rather than looked up later: this is resolved
      // while the drag's own workspace list is still in hand, and the
      // confirmation needs to name both sides of the move.
      movingName: (moving && moving.name) || 'this workspace'
    };
  }

  /**
   * Why this reparent is not allowed, or '' when it is.
   *
   * Mirrors home-workspace-tree.js's moveRejectionReason. It is duplicated
   * rather than imported because workspace-map.js is a plain deferred script,
   * not a module — the two are kept in step by
   * `map and tree refuse the same reparents` in workspace-map.test.js.
   */
  function dropRejectionReason(workspaces, movingId, targetParentId) {
    var rows = Array.isArray(workspaces) ? workspaces : [];
    var byId = Object.create(null);
    rows.forEach(function (row) {
      if (row && row.id) byId[row.id] = row;
    });
    if (targetParentId === movingId) return 'A group cannot be moved into itself.';
    // Walk up from the target: if the moving item is an ancestor, this would
    // make a cycle. `seen` guards malformed data rather than trusting it.
    var seen = Object.create(null);
    var cursor = byId[targetParentId];
    while (cursor && cursor.parent_id && !seen[cursor.parent_id]) {
      if (cursor.parent_id === movingId) {
        return 'A group cannot be moved into something inside it.';
      }
      seen[cursor.parent_id] = true;
      cursor = byId[cursor.parent_id];
    }
    return '';
  }

  // worldBounds is the tight box around everything currently drawn. It grows
  // automatically as content is placed further out, and it never adjusts a
  // coordinate to fit (FR-10).
  function worldBounds(nodes, districts, hqSite) {
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

  /**
   * The one zoom clamp: framing, gestures, the opening view, a restored saved
   * camera, and every non-zoom action that carries a zoom along all land here
   * (FR-38). Sharing it is the point — a camera Fit All can reach is one a
   * gesture can leave, and one the server will store.
   */
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
    return {
      centerX: cam.centerX,
      centerY: cam.centerY,
      zoom: clampZoom(cam.zoom * factor)
    };
  }

  /**
   * The camera that frames everything in `bounds` with padding (FR-40).
   *
   * `fitsEverything` reports whether the framing floor was room enough. It is
   * decided here, where the zoom the content actually needs is known, because
   * the resulting camera cannot answer it: a fit that lands exactly on the
   * floor and a fit that was cut off by it look identical afterwards, and Fit
   * All's announcement is the difference between "showing every workspace" and
   * telling someone to go looking (#307).
   */
  function fitBounds(bounds, viewport, padding) {
    var pad = typeof padding === 'number' ? padding : FIT_PADDING;
    var width = Math.max(1, bounds.maxX - bounds.minX);
    var height = Math.max(1, bounds.maxY - bounds.minY);
    var usableWidth = Math.max(1, (viewport.width || DEFAULT_VIEWPORT.width) - pad * 2);
    var usableHeight = Math.max(1, (viewport.height || DEFAULT_VIEWPORT.height) - pad * 2);
    var required = Math.min(usableWidth / width, usableHeight / height);
    return {
      centerX: (bounds.minX + bounds.maxX) / 2,
      centerY: (bounds.minY + bounds.maxY) / 2,
      zoom: clampZoom(required),
      fitsEverything: isFinite(required) && required >= MIN_ZOOM
    };
  }

  /**
   * Look at one node without moving it (FR-41).
   *
   * `point` may carry its own width/height. A district is centred by its
   * effective frame rather than by a building-sized box at its corner, so
   * Center selected on a large district looks at the district rather than at
   * its top-left tile (#346 FR-47).
   */
  function centerOn(cam, point) {
    var width = typeof point.width === 'number' && isFinite(point.width) ? point.width : CELL_W;
    var height = typeof point.height === 'number' && isFinite(point.height) ? point.height : CELL_H;
    return { centerX: point.x + width / 2, centerY: point.y + height / 2, zoom: cam.zoom };
  }

  /**
   * Keep the camera inside the navigable world.
   *
   * Panning may roam a viewport past the outermost building — that margin is
   * what makes it possible to build near an edge (FR-11) — but not further, so
   * it is never possible to end up in empty space with no content in any
   * direction and no idea which way to go back.
   *
   * The zoom it carries is only sanity-checked, never adjusted: a pan is not a
   * zoom, and a camera Fit All framed at 26% must still be at 26% after one
   * (#307).
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
    // The resize overlay is screen-space, so the camera moving under it is
    // exactly when it has to be re-placed (#346 FR-55).
    positionResizeOverlay(container);
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
      camera = {
        centerX: stored.centerX,
        centerY: stored.centerY,
        // A saved camera is very often a saved Fit All, so reopening a wide map
        // must restore the fitted zoom rather than clamp it away (#307).
        zoom: clampZoom(stored.zoom)
      };
      cameraReady = true;
      return;
    }
    // Wait for something worth framing. The first mount can happen before the
    // workspace list arrives, and framing an empty world would lock the camera
    // onto a corner nothing is drawn in, which every later refresh inherits.
    //
    // The reserved Personal HQ site counts. It is the whole map on a profile
    // that has no workspaces yet, and leaving the camera at its hard-coded
    // default there put the landmark low and right of centre with its caption
    // behind the control strip — a first impression of the product with the
    // one thing on screen half-hidden (#329). The create pad used to be the
    // other thing that explicitly did not count here; since #367 it is not
    // drawn at all, so only real content and the HQ site can reach this test.
    if (
      !lastWorldLayout.nodes.length &&
      !lastWorldLayout.districts.length &&
      !lastWorldLayout.hqSite
    ) {
      return;
    }
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

  // How a district states its size in words. "Unavailable" is deliberately
  // distinct from a truthful zero: a group that has not reported its contents
  // must not claim to be empty (#346 FR-107).
  function districtCountLabel(count) {
    if (typeof count !== 'number' || !isFinite(count) || count < 0) return 'Count unavailable';
    if (count === 0) return 'No workspaces';
    return count === 1 ? '1 workspace' : count + ' workspaces';
  }

  /**
   * An expanded district and its header (#346 FR-139 – FR-145).
   *
   * The header replaces the detached `▢ Name · Group` tag and the `⤧` glyph that
   * preceded it. Both were unlabelled in every sense that mattered: the tag
   * claimed a click would "Open" the group when on Home a click only selects it,
   * and the glyph gave a screen reader a symbol with no name and a sighted user
   * no idea it was a drag handle.
   *
   * The three controls stay separate on purpose. Selecting a group, moving
   * everything inside it, and reaching its actions are three different
   * intentions, and making the whole header draggable would collapse the first
   * two into one gesture (FR-144, design §6).
   */
  function districtHTML(d, selectedId) {
    var ws = d.ws || {};
    // A selected GROUP must keep its highlight across a re-mount, not only
    // until the next applySelection call (PRD FR58).
    var isSel = !!selectedId && ws.id === selectedId;
    // The effective frame resolved by computeWorldLayout. It is presentation
    // only — it grows around a member that moved and never claims that geometry
    // changed membership (#346 FR-29, FR-8).
    var left = Number(d.left) || 0;
    var top = Number(d.top) || 0;
    var width = Number(d.width) || CELL_W;
    var height = Number(d.height) || CELL_H;
    var name = ws.name || 'Group';
    var label = name + ' group';
    var countLabel = districtCountLabel(d.memberCount);
    // A district whose frame has ended up around something that is not in it —
    // always because the hierarchy changed underneath it, never because of
    // anything the user did on the Map (#346 FR-88).
    var conflict = d.conflict || null;
    // A collapsed district is the same group in a compact state, not a new kind
    // of thing: the same header, the same three controls, the same identity —
    // just without its buildings drawn (#346 FR-106, FR-117).
    var collapsed = !!d.collapsed;
    // Home selects on click and opens on Enter, so the control cannot promise
    // "Open" as its only meaning. It names the group and its state instead, and
    // aria-pressed carries the selection (FR-141, FR-143).
    var selectLabel = name + ' group, ' + countLabel;
    // Preset CLASSES, never inline style. The identifier is validated against
    // the catalog first, so an unknown or hostile one becomes the default and
    // can never be interpolated into a rule (#346 FR-125, FR-194).
    var accent = safeAccent(d.accent);
    var theme = safeTheme(d.theme);
    return (
      '<div class="ws-map-district ws-map-accent-' +
      accent +
      ' ws-map-theme-' +
      theme +
      (collapsed ? ' is-collapsed' : '') +
      (conflict ? ' is-conflicted' : '') +
      '" role="group" aria-label="' +
      escapeHtml(conflict ? label + ', needs layout attention' : label) +
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
      '<div class="ws-map-district-header">' +
      '<button type="button" class="ws-map-district-tag' +
      (isSel ? ' is-selected' : '') +
      '" data-ws-id="' +
      escapeHtml(ws.id) +
      '" ' +
      'aria-pressed="' +
      (isSel ? 'true' : 'false') +
      '" ' +
      // The full name stays available to assistive technology even when the
      // visible label truncates (FR-135).
      'title="' +
      escapeHtml(name) +
      '" ' +
      'aria-label="' +
      escapeHtml(selectLabel) +
      '">' +
      '<span class="ws-map-district-name">' +
      escapeHtml(name) +
      '</span>' +
      '<span class="ws-map-district-count">' +
      escapeHtml(countLabel) +
      '</span>' +
      '</button>' +
      // Collapse is its own control with its own accurate label, distinct from
      // selecting, opening, moving, and deleting the group (#346 FR-109,
      // FR-145). aria-expanded lives here rather than on the outline because
      // this is the control that changes it (FR-110).
      '<button type="button" class="ws-map-district-collapse" data-group-collapse="' +
      escapeHtml(ws.id) +
      '" aria-expanded="' +
      (collapsed ? 'false' : 'true') +
      '" aria-label="' +
      escapeHtml((collapsed ? 'Expand group: ' : 'Collapse group: ') + name) +
      '" title="' +
      escapeHtml(collapsed ? 'Expand group' : 'Collapse group') +
      '"><span aria-hidden="true">' +
      (collapsed ? '▸' : '▾') +
      '</span></button>' +
      // A separate, touch-sized handle for cluster movement. Dragging the label
      // would make "select this group" and "move everything in it" the same
      // gesture, and every existing group action — select, overview, open,
      // delete, Tree management — has to stay reachable (FR-85, FR-94).
      //
      // The ⤧ glyph is the map's established symbol for this and stays. What
      // made it cryptic was never the symbol — it was that the control had no
      // name at all, so a screen reader read a bare character and a hover said
      // nothing about what would move. The name is what FR-140 asked for, and
      // the name is what changed.
      '<button type="button" class="ws-map-district-handle" data-group-drag="' +
      escapeHtml(ws.id) +
      '" aria-label="' +
      escapeHtml('Move group: ' + name) +
      '" title="' +
      escapeHtml('Move group: ' + name) +
      '"><span class="ws-map-district-grip" aria-hidden="true">⤧</span></button>' +
      // The overflow control opens the same menu right-click does, so a pointer
      // user who never right-clicks and a keyboard user both reach the group's
      // actions (FR-139, FR-149).
      '<button type="button" class="ws-map-district-more" data-group-menu="' +
      escapeHtml(ws.id) +
      '" aria-haspopup="menu" aria-expanded="false" aria-label="' +
      escapeHtml('Actions for ' + label) +
      '" title="' +
      escapeHtml('Actions for ' + name) +
      '"><span aria-hidden="true">⋯</span></button>' +
      '</div>' +
      // Explanatory text, not a colour: it names what the frame has ended up
      // around and what the user can do about it (FR-88, FR-163).
      (conflict
        ? '<p class="ws-map-district-conflict" role="status">' +
          escapeHtml(
            'This group’s outline now reaches ' +
              conflict.name +
              ', which is not in it. Move or resize the group, or move its workspaces.'
          ) +
          '</p>'
        : '') +
      '</div>'
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
            height: district.height,
            memberCount: district.memberCount,
            collapsed: district.collapsed,
            conflict: district.conflict,
            accent: district.accent,
            theme: district.theme
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
    // No ordinary create pad is drawn among the sites (#367). Creating a
    // workspace belongs to the chrome around the map — the topbar's ⊕ New
    // Workspace, and Home's workspace-area header — not to a building-shaped
    // tile competing with real workspaces for a spot in the world. The
    // zero-workspace presentations keep their own create actions: the legacy
    // hero in emptyCanvasHTML, and the cockpit's create/import overlay.
    //
    // No candidate marker either: build is not a mode any more, so there is no
    // in-between state to preview. Right-click is the coordinate.

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
        '</div>' +
        // The resize overlay lives OUTSIDE the world layer on purpose. Inside
        // it, the camera transform would scale the handles with the map, so at
        // 10% zoom a 44px target would be 4px of screen (#346 FR-55, FR-56).
        // Here it is screen-space, and worldToScreen re-places it on every
        // render, camera change, and viewport resize.
        resizeOverlayHTML() +
        '</div>' +
        cameraControlsHTML(),
      empty: layout.nodes.length === 0,
      layout: layout
    };
  }

  /**
   * The selected district's resize overlay (#346 FR-52 – FR-57).
   *
   * Rendered empty and hidden; positionResizeOverlay fills it in when an
   * expanded district is selected and the layout is writable. An unselected
   * district shows no handles and no permanent toolbar (FR-53).
   *
   * Only the handles themselves take pointer input — the box between them is
   * inert, so panning, selecting, and right-clicking the map underneath a
   * selected district all still work (FR-157).
   */
  function resizeOverlayHTML() {
    var handles = RESIZE_HANDLES.map(function (handle) {
      return (
        '<button type="button" class="ws-map-resize-handle ws-map-resize-' +
        handle +
        '" data-resize-handle="' +
        handle +
        '" tabindex="-1" aria-label=""><span class="ws-map-resize-dot" aria-hidden="true"></span>' +
        '</button>'
      );
    }).join('');
    return (
      '<div class="ws-map-resize-overlay" data-ws-map-resize hidden>' +
      '<div class="ws-map-resize-box" data-ws-map-resize-box>' +
      handles +
      '</div>' +
      '<div class="ws-map-resize-readout" data-ws-map-resize-readout hidden></div>' +
      '</div>'
    );
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
      // Build is not a button any more (#317). It was a mode — press Build, then
      // click a spot — and the context menu already knows the spot: right-click
      // where you want the workspace and choose Build. One gesture instead of
      // three, and no mode to be stuck in.
      '<button type="button" class="ws-map-ctl ws-map-ctl--wide" data-map-drag aria-pressed="' +
      (dragModeEnabled ? 'true' : 'false') +
      '"' +
      (readOnly ? ' disabled' : '') +
      '>Drag: ' +
      (dragModeEnabled ? 'on' : 'off') +
      '</button>' +
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
      // Zoom only. Fit all, Center selected and Reset view moved into the
      // canvas context menu (#317): they are framing choices you make about a
      // spot on the map, so they belong under the cursor rather than in a
      // permanent strip across the bottom of it. Keyboard users reach them by
      // Shift+F10 on the focused canvas, which opens that same menu; 0 still
      // resets the view directly.
      '<div class="ws-map-controls" role="group" aria-label="Map view controls">' +
      '<button type="button" class="ws-map-ctl" data-map-zoom-out aria-label="Zoom out">−</button>' +
      '<span class="ws-map-zoom" data-map-zoom-readout aria-hidden="true">100%</span>' +
      '<button type="button" class="ws-map-ctl" data-map-zoom-in aria-label="Zoom in">+</button>' +
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
      '<li><b>Right-click</b> — anything on the map has a menu: a building, a district, the HQ site, or empty ground. Shift+F10 does the same from the keyboard.</li>' +
      '</ul>' +
      '<p class="ws-map-help-note">Fit all, Center and Reset view live on the empty-ground menu (0 resets the view directly) and move the camera. Reset layout moves the buildings — and can be undone.</p>' +
      '</div>'
    );
  }

  function isApplePlatform() {
    if (typeof navigator === 'undefined') return true;
    return /Mac|iPhone|iPad/i.test(String(navigator.platform || navigator.userAgent || ''));
  }

  // The move banner. It kept the `build` names from when placement was a mode
  // and this told you to pick a spot; today it belongs entirely to moving, and
  // renaming the hooks would churn CSS and tests for no user-visible gain.
  function buildBannerHTML() {
    return (
      '<div class="ws-map-build" data-map-build-banner hidden>' +
      '<span class="ws-map-build-dot" aria-hidden="true">◎</span>' +
      '<span class="ws-map-build-text" data-map-build-text>' +
      MOVE_INSTRUCTION +
      '</span>' +
      '<span class="ws-map-build-coords" data-map-build-coords></span>' +
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

  function cockpitEmptyActionsHTML() {
    return (
      '<div class="cockpit-empty-map-actions" role="group" aria-label="Create or import a workspace">' +
      '<button type="button" class="modern-btn modern-btn-primary" data-bs-toggle="modal" data-bs-target="#addFolderModal" data-workspace-import-mode="false" data-workspace-entry-point="home_cockpit_create">New Workspace</button>' +
      '<button type="button" class="modern-btn modern-btn-secondary" data-bs-toggle="modal" data-bs-target="#addFolderModal" data-workspace-import-mode="true" data-workspace-entry-point="home_cockpit_import">Import Folder</button>' +
      '</div>'
    );
  }

  function shellHTML(stats, workspaces, selectedId, viewport, options) {
    var site = hqSiteView(hqStatus);
    var authoritativeEmpty =
      options &&
      options.emptyPresentation === 'canvas' &&
      Array.isArray(workspaces) &&
      workspaces.length === 0;
    var canvas =
      (Array.isArray(workspaces) && workspaces.length > 0) || site.show || authoritativeEmpty
        ? canvasHTML(workspaces, selectedId, { viewport: viewport }).html
        : emptyCanvasHTML();
    // Cockpit mode: the workspace-area header and on-demand context modal
    // already own the title, the stat readout, New Workspace, and the selected
    // workspace's overview (PRD FR15, FR17, FR29, FR62-FR69). Rendering the
    // map's own topbar/overview here would duplicate all four.
    if (hideChromeMode) {
      return (
        '<div class="ws-map-layout is-cockpit">' +
        '<section class="ws-map-theatre">' +
        '<div class="ws-map-compass">N<b>▲</b></div>' +
        canvas +
        (authoritativeEmpty ? cockpitEmptyActionsHTML() : '') +
        '</section>' +
        '</div>' +
        selBarHTML() +
        menuHostHTML() +
        confirmHostHTML()
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
      menuHostHTML() +
      confirmHostHTML()
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

  // Keeping the view centred when the container resizes.
  //
  // World coordinates really are viewport-independent — a resize never moves a
  // building (FR-13, FR-46). The *transform* is not: applyCamera anchors
  // camera.centerX at the canvas's live horizontal centre, so a canvas that
  // changes size without a re-mount keeps the translate it was given at its old
  // width. The content stays pinned to where the old centre was and the space
  // just gained sits empty beside it.
  //
  // Re-applying the camera fixes that framing while preserving the user's pan
  // and zoom. Deliberately NOT fitAll: refitting on every resize would discard
  // the view the user chose, and the camera is supposed to survive (FR-106).
  var resizeObserver = null;

  function stopResizeWatch() {
    if (resizeObserver && typeof resizeObserver.disconnect === 'function') {
      resizeObserver.disconnect();
    }
    resizeObserver = null;
  }

  function watchResize(container) {
    stopResizeWatch();
    if (typeof ResizeObserver !== 'function' || !container) return;
    // ResizeObserver already coalesces to one callback per animation frame, so
    // an animating rail costs one applyCamera per painted frame — a single
    // style write, no re-render.
    resizeObserver = new ResizeObserver(function () {
      if (lastMount && lastMount.container === container) applyCamera(container);
    });
    resizeObserver.observe(container);
  }

  // Reuse the page's existing create flow for every create affordance, rather
  // than opening a second Create Workspace path (PRD FR105). The cockpit's
  // button is checked first because the launcher's is absent on Home.
  function triggerCreateWorkspace() {
    if (typeof document === 'undefined' || typeof document.getElementById !== 'function') return;
    var create =
      document.getElementById('cockpitCreateWorkspaceBtn') ||
      document.getElementById('launcherCreateWorkspaceBtn');
    if (create && typeof create.click === 'function') create.click();
  }

  function bindCockpitEmptyActions(container) {
    var buttons = container.querySelectorAll('.cockpit-empty-map-actions button');
    buttons.forEach(function (button) {
      button.addEventListener('click', function () {
        var modal = document.getElementById('addFolderModal');
        if (!modal) return;
        var entryPoint = button.getAttribute('data-workspace-entry-point');
        modal.addEventListener(
          'hidden.bs.modal',
          function restoreEmptyActionFocus() {
            // A late HQ response may remount the Map while the modal is open.
            // Resolve the current button by its stable contract rather than
            // focusing a detached trigger from the previous canvas.
            var current = container.querySelector(
              '.cockpit-empty-map-actions [data-workspace-entry-point="' + entryPoint + '"]'
            );
            if (current && current.focus) current.focus();
          },
          { once: true }
        );
      });
    });
  }

  function bindCreate(container) {
    var els = container.querySelectorAll('[data-ws-map-create]');
    Array.prototype.forEach.call(els, function (el) {
      el.addEventListener('click', function () {
        triggerCreateWorkspace();
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
      var sel = el.getAttribute('data-ws-id') === id;
      el.classList.toggle('is-selected', sel);
      // The selected state has to update in place, not only on the next full
      // render: a screen reader reading the control it just activated must hear
      // the new state, and the class alone is invisible to it (#346 FR-143).
      el.setAttribute('aria-pressed', sel ? 'true' : 'false');
    });
    var selected = findWs(workspaces, id);
    // The map's own overview panel is absent in cockpit mode — Home renders
    // selection through its stable context modal, via onSelect.
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
    // Resize handles appear only on the selected expanded district, so a
    // selection change is what shows or hides them (#346 FR-52, FR-53).
    if (resizeState && resizeState.groupId !== id) cancelResize();
    positionResizeOverlay(container);
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

  // The drop confirmation's host. Separate from the menu's because the two can
  // never be open at once but do not share a lifetime: closing the menu must
  // not take a pending confirmation with it.
  function confirmHostHTML() {
    return '<div class="ws-map-menu-host" data-ws-map-confirm-host></div>';
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

  // A group district. Districts cannot be multi-selected, so this menu is
  // always single-target, and its delete runs the same host callback (and the
  // same group_only confirm) the rail's Delete group runs.
  //
  // The layout actions sit between Open and Delete, so the destructive item
  // stays visually separated from the non-destructive ones (#346 FR-147), and
  // each carries a truthful disabled state rather than being hidden — a missing
  // item tells the user nothing about why (FR-148).
  function districtMenuItems(ws) {
    var groupId = (ws && ws.id) || '';
    var district = renderedDistrict(groupId);
    var readOnly = isMapReadOnly();
    var collapsed = !!(district && district.collapsed);
    var items = [{ label: 'Open group', action: 'open' }, menuDivider()];
    items.push({
      label: collapsed ? 'Expand group' : 'Collapse group',
      action: collapsed ? 'expand-group' : 'collapse-group',
      disabled: readOnly || !district
    });
    // Resize and Fit are meaningless on a collapsed district: there is no frame
    // on screen to size (FR-115).
    items.push({
      label: 'Resize group',
      action: 'resize-group',
      disabled: readOnly || collapsed || !district
    });
    items.push({
      label: 'Fit to contents',
      action: 'fit-group',
      // Fitting an already-automatic district would consume a revision to
      // change nothing.
      disabled: readOnly || collapsed || !district || district.sizingMode !== 'custom'
    });
    // Offered only when there is a customization to undo, so the menu does not
    // carry a permanently dead entry (FR-137, FR-146).
    if (district && (district.accent !== DEFAULT_ACCENT || district.theme !== DEFAULT_THEME)) {
      items.push({
        label: 'Use default appearance',
        action: 'reset-appearance',
        disabled: readOnly
      });
    }
    items.push(menuDivider());
    items.push({
      label: isGroup(ws) ? 'Delete group' : 'Delete workspace',
      action: 'delete',
      variant: 'danger',
      disabled: readOnly
    });
    return items;
  }

  // The reserved Personal HQ site. The conditions mirror hqOverviewHTML exactly:
  // build and import always; clear only while repairing a broken link; skip only
  // when not repairing and the offer has not been dismissed. Clear and skip are
  // mutually exclusive.
  function hqMenuItems(view) {
    var site = view || {};
    var items = [
      { label: site.repair ? 'Build replacement HQ' : 'Build My HQ', action: 'hq-build' },
      { label: 'Import HQ', action: 'hq-import' }
    ];
    if (site.repair) items.push({ label: 'Clear broken HQ link', action: 'hq-clear' });
    else if (site.showSkip) items.push({ label: 'Not now', action: 'hq-skip' });
    return items;
  }

  // Empty ground. Build plus the three framing actions, which is what the
  // control cluster used to offer — just under the cursor, and Build now knows
  // *where*: the workspace is created at the point that was right-clicked.
  // Centre is disabled with nothing selected, the same rule the control applied.
  function canvasMenuItems() {
    var items = [
      { label: 'Build', action: 'build', disabled: isMapReadOnly() },
      menuDivider(),
      { label: 'Fit all', action: 'fit' },
      { label: 'Center selected', action: 'center', disabled: !selectedNodeAnchor() },
      { label: 'Reset view', action: 'reset-view' }
    ];
    // Offered only when there is something to clear, so the menu does not carry
    // a permanently dead entry.
    if (multiCount() > 0) {
      items.push(menuDivider());
      items.push({ label: 'Clear selection', action: 'clear-selection' });
    }
    return items;
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
    if (spec.type === 'district') return districtMenuItems(spec.ws || { id: spec.id });
    if (spec.type === 'hq') return hqMenuItems(spec.view || hqSiteView(hqStatus));
    if (spec.type === 'canvas') return canvasMenuItems();
    return [];
  }

  function menuLabelFor(target) {
    var spec = target || {};
    if (spec.type === 'tile') {
      if (spec.id && multiSelected[spec.id]) {
        return 'Actions for ' + multiCount() + ' selected';
      }
      return 'Actions for ' + ((spec.ws && spec.ws.name) || 'workspace');
    }
    if (spec.type === 'district') {
      return 'Actions for ' + ((spec.ws && spec.ws.name) || 'group') + ' group';
    }
    if (spec.type === 'hq') return 'Personal HQ actions';
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

  // A right-click anchors at the cursor; the keyboard open path anchors at the
  // focused element instead, so the menu appears where the user's attention
  // already is (FR-3).
  function anchorForElement(el) {
    if (el && typeof el.getBoundingClientRect === 'function') {
      var rect = el.getBoundingClientRect();
      if (rect) return { x: rect.left || 0, y: (rect.top || 0) + (rect.height || 0) };
    }
    return { x: 0, y: 0 };
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
    // The chosen item's own wording travels with the action, so an announcement
    // can say what the user actually picked.
    var context = Object.assign({}, state.context, {
      label: String(el.textContent || el.label || '').trim()
    });
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

  /**
   * Run a chosen item.
   *
   * Every result is spoken through the map's existing live region rather than a
   * second one of the menu's own (FR-23), and announce() never moves focus
   * (FR-24). Where the action is the host's — delete, group — the map says what
   * it asked for, not what happened: the confirmation and the outcome belong to
   * workspace-bulk-actions, and claiming a result here would sometimes be a lie.
   */
  function runMenuAction(container, action, context, options) {
    var id = context.id || '';
    var name = context.name || 'workspace';
    var count = multiCount();
    switch (action) {
      case 'open':
        // The map's explicit open: the host's onOpen when it has one (the
        // cockpit records the first action there), else plain navigation, which
        // is what the context modal's Open button does.
        announce(container, 'Opening ' + name);
        if (options && typeof options.onOpen === 'function') options.onOpen(id);
        else openWorkspace(id);
        break;
      case 'open-backlog':
        announce(container, 'Opening the Backlog for ' + name);
        openWorkspace(id, { panel: 'backlog' });
        break;
      // District layout actions (#346 FR-146). They run the same controller the
      // direct handles and the Home rail run, so all three surfaces validate,
      // persist, announce, and fail identically (FR-156).
      case 'resize-group':
        startGroupResize(container, id);
        break;
      case 'fit-group':
        void fitGroupToContents(container, id);
        break;
      case 'collapse-group':
        void setGroupCollapsed(container, id, true);
        break;
      case 'expand-group':
        void setGroupCollapsed(container, id, false);
        break;
      case 'reset-appearance':
        void resetGroupAppearance(container, id);
        break;
      case 'open-setup':
        announce(container, 'Opening Setup for ' + name);
        openWorkspace(id, { setup: true });
        break;
      case 'toggle-selection':
        var wasSelected = !!multiSelected[id];
        toggleMulti(container, id);
        announce(
          container,
          (wasSelected ? 'Removed ' + name + ' from the selection. ' : 'Added ' + name + '. ') +
            multiCount() +
            ' selected'
        );
        break;
      case 'delete':
        announce(container, 'Delete ' + name + ' — confirm to continue');
        deleteWorkspace(id, options);
        break;
      // The bulk actions the selection bar used to carry. They run the host's
      // own callbacks, so the existing confirm paths in workspace-bulk-actions
      // are the only confirmation — the menu adds no second one.
      case 'group-multi':
        announce(container, workspaceCountLabel('Grouping', count));
        groupMulti(options);
        break;
      case 'delete-multi':
        announce(container, workspaceCountLabel('Delete', count) + ' — confirm to continue');
        deleteMulti(options);
        break;
      case 'clear-selection':
        clearMulti(container);
        announce(container, 'Selection cleared');
        break;
      // The HQ site's four choices reach the host exactly as the rail's buttons
      // do — one custom event, one action name.
      case 'hq-build':
      case 'hq-import':
      case 'hq-clear':
      case 'hq-skip':
        announce(container, 'Personal HQ: ' + (context.label || action.slice(3)));
        dispatchHQAction(action.slice(3));
        break;
      // Build creates the workspace where the menu was opened. The coordinate is
      // held as pending rather than saved — nothing is written for a workspace
      // that does not exist yet — and the existing create modal takes it from
      // there, exactly as the retired build mode did.
      case 'build':
        if (context.world) {
          chooseBuildSite(container, context.world);
          break;
        }
        // No usable coordinate (a map with no canvas to measure): fall back to
        // the plain create flow rather than silently doing nothing.
        announce(container, 'Opening the new workspace flow');
        triggerCreateWorkspace();
        break;
      // Framing actions. Same calls and same announcements as the control
      // cluster, so the two cannot drift apart.
      case 'fit':
        announce(container, fitAllAnnouncement(fitAll(container)));
        break;
      case 'center':
        var anchor = selectedNodeAnchor();
        if (!anchor) {
          announce(container, 'Select a workspace first');
          break;
        }
        setCamera(centerOn(camera, anchor), container);
        announce(container, 'Centered the selected workspace');
        break;
      case 'reset-view':
        resetView(container);
        announce(container, 'View reset. Workspace positions are unchanged');
        break;
      default:
        break;
    }
  }

  function dispatchHQAction(name) {
    if (typeof window === 'undefined' || typeof window.dispatchEvent !== 'function') return;
    window.dispatchEvent(new CustomEvent('ori:personal-hq-action', { detail: { action: name } }));
  }

  // resolveMenuTarget turns whatever was right-clicked into one of the map's
  // four targets. Order matters: the HQ site is also a `.ws-map-tile`, and a
  // district's label sits inside the district outline.
  function resolveMenuTarget(node, workspaces) {
    if (!node || typeof node.closest !== 'function') return null;
    var hq = node.closest('[data-hq-site]');
    if (hq) return { type: 'hq', view: hqSiteView(hqStatus), element: hq };
    var tile = node.closest('.ws-map-tile[data-ws-id]');
    if (tile) {
      var tileId = tile.getAttribute('data-ws-id');
      return { type: 'tile', id: tileId, ws: findWs(workspaces, tileId), element: tile };
    }
    var district = node.closest('.ws-map-district');
    if (district) {
      var groupId = district.getAttribute('data-group-id');
      // Focus goes back to the district's label button, not to the outline: the
      // outline is a plain div and cannot hold focus (FR-19).
      var tag =
        typeof district.querySelector === 'function'
          ? district.querySelector('.ws-map-district-tag')
          : null;
      return {
        type: 'district',
        id: groupId,
        ws: findWs(workspaces, groupId),
        element: tag || district
      };
    }
    var canvas = node.closest('.ws-map-canvas');
    if (canvas) return { type: 'canvas', element: canvas };
    return null;
  }

  /**
   * The world coordinate the menu was opened over.
   *
   * Build creates a workspace *there*, so the screen point has to be converted
   * back through the camera the moment the menu opens — after a pan or a zoom
   * the same screen point means somewhere else entirely. Snapped by the user's
   * own preference, exactly as a drag-drop is.
   *
   * A menu opened from the keyboard has no cursor, so it builds at the middle of
   * what the user is looking at — the same choice the old build mode made for
   * its keyboard start point.
   */
  function menuWorldPoint(container, at, viaKeyboard) {
    var canvas = container && container.querySelector && container.querySelector('.ws-map-canvas');
    if (!canvas) return null;
    var viewport = viewportSize(canvas);
    var local = viaKeyboard
      ? { x: viewport.width / 2, y: viewport.height / 2 }
      : localPoint(canvas, at);
    return snapPoint(screenToWorld(local, camera, viewport));
  }

  function localPoint(canvas, at) {
    var point = at || { x: 0, y: 0 };
    if (typeof canvas.getBoundingClientRect === 'function') {
      var rect = canvas.getBoundingClientRect();
      if (rect) return { x: point.x - (rect.left || 0), y: point.y - (rect.top || 0) };
    }
    return { x: point.x, y: point.y };
  }

  function openMenuForTarget(container, workspaces, options, target, at, event, viaKeyboard) {
    // Right-clicking a site that is not in the multi-select set selects it
    // first, so the menu always acts on something the user can see is chosen
    // (FR-6). It never opens the workspace and never touches the checkbox.
    // Empty canvas is the exception: it has nothing of its own to select, and
    // clearing the selection to open a menu would destroy what the menu's own
    // Center and Clear items act on (FR-8).
    if (target.type === 'tile' && target.id && !multiSelected[target.id]) {
      applySelection(container, workspaces, target.id, options);
    }
    if (target.type === 'district' && target.id) {
      applySelection(container, workspaces, target.id, options);
    }
    if (target.type === 'hq') {
      applyHQSelection(container, target.view || hqSiteView(hqStatus), options);
    }
    var items = contextMenuItemsFor(target);
    if (!items.length) return false;
    return openContextMenu(container, {
      items: items,
      label: menuLabelFor(target),
      at: at,
      origin: target.element,
      context: {
        id: target.id || '',
        type: target.type,
        name: (target.ws && target.ws.name) || (target.type === 'hq' ? 'Personal HQ' : 'workspace'),
        // Only the canvas menu builds, and only it needs a coordinate.
        world: target.type === 'canvas' ? menuWorldPoint(container, at, viaKeyboard) : null
      },
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

    // The keyboard equivalent (FR-3). Both platform gestures work — the Context
    // Menu key and Shift+F10 — and the menu opens at the focused element,
    // because there is no cursor to anchor to. The canvas counts as a target
    // here: it is focusable, and since the framing buttons were retired the
    // menu is the only way to reach Center Selected without a mouse.
    canvas.addEventListener('keydown', function (event) {
      if (!event) return;
      var wanted = event.key === 'ContextMenu' || (event.key === 'F10' && event.shiftKey);
      if (!wanted) return;
      if (dragState || clusterDrag) return;
      var target = resolveMenuTarget(event.target, workspaces);
      if (!target) return;
      if (!contextMenuItemsFor(target).length) return;
      if (event.preventDefault) event.preventDefault();
      openMenuForTarget(
        container,
        workspaces,
        options,
        target,
        anchorForElement(target.element),
        event,
        true
      );
    });

    // The district header's collapse control (#346 FR-102).
    var collapsers = container.querySelectorAll('[data-group-collapse]');
    Array.prototype.forEach.call(collapsers, function (button) {
      if (!button || typeof button.addEventListener !== 'function') return;
      button.addEventListener('click', function (event) {
        if (event.preventDefault) event.preventDefault();
        if (event.stopPropagation) event.stopPropagation();
        var groupId = button.getAttribute('data-group-collapse');
        if (isMapReadOnly()) {
          announce(container, 'The map layout cannot be saved right now.');
          return;
        }
        void setGroupCollapsed(container, groupId, button.getAttribute('aria-expanded') === 'true');
      });
    });

    // The district header's overflow control. It opens the very same menu the
    // right-click and Shift+F10 paths open, so the three routes cannot offer
    // different actions or different validation (#346 FR-156).
    var overflows = container.querySelectorAll('[data-group-menu]');
    Array.prototype.forEach.call(overflows, function (button) {
      if (!button || typeof button.addEventListener !== 'function') return;
      button.addEventListener('click', function (event) {
        if (dragState || clusterDrag) return;
        var groupId = button.getAttribute('data-group-menu');
        var target = {
          type: 'district',
          id: groupId,
          ws: findWs(workspaces, groupId),
          element: button
        };
        if (!contextMenuItemsFor(target).length) return;
        if (event.preventDefault) event.preventDefault();
        if (event.stopPropagation) event.stopPropagation();
        openMenuForTarget(
          container,
          workspaces,
          options,
          target,
          anchorForElement(button),
          event,
          true
        );
      });
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

  function updateDragControl(container) {
    if (!container || typeof container.querySelector !== 'function') return;
    var toggle = container.querySelector('[data-map-drag]');
    var writable = layoutState.status === 'ready';
    if (toggle) {
      toggle.disabled = !writable;
      if (toggle.setAttribute) {
        toggle.setAttribute('aria-pressed', dragModeEnabled ? 'true' : 'false');
      }
      toggle.textContent = 'Drag: ' + (dragModeEnabled ? 'on' : 'off');
    }
    var canvas = container.querySelector('[data-ws-map-viewport]');
    if (canvas && canvas.classList) {
      canvas.classList.toggle('is-drag-enabled', writable && dragModeEnabled);
    }
  }

  /**
   * Cancel pointer-only translations without saving.
   *
   * A mode switch or redraw can destroy the elements that own pointer capture.
   * Restore previews and release capture first, so no stale drag object can
   * write through a later event from detached DOM (#374 AR5).
   */
  function cancelPointerTranslations(container) {
    var tileState = dragState;
    dragState = null;
    if (tileState) {
      var tile = tileState.el;
      if (
        tile &&
        tile.releasePointerCapture &&
        tile.hasPointerCapture &&
        tile.hasPointerCapture(tileState.pointerId)
      ) {
        tile.releasePointerCapture(tileState.pointerId);
      }
      if (tile && tile.classList) {
        tile.classList.remove('is-dragging');
        tile.classList.remove('is-blocked');
        tile.classList.remove('is-leaving');
      }
      if (tileState.moved && tile) placeElement(tile, tileState.origin);
    }

    var districtState = clusterDrag;
    clusterDrag = null;
    if (districtState) {
      var handle = districtState.handle;
      if (
        handle &&
        handle.releasePointerCapture &&
        handle.hasPointerCapture &&
        handle.hasPointerCapture(districtState.pointerId)
      ) {
        handle.releasePointerCapture(districtState.pointerId);
      }
      if (districtState.districtEl && districtState.districtEl.classList) {
        districtState.districtEl.classList.remove('is-dragging');
      }
      setClusterBlocked(districtState, false);
      if (districtState.moved) restoreCluster(districtState);
    }

    suppressClickFor = null;
    markDropTarget(container, '');
    markLeaveSource(container, '');
    setDragReadout(container, null);
  }

  function setDragMode(container, enabled) {
    var next = !!enabled;
    if (next === dragModeEnabled) {
      updateDragControl(container);
      return;
    }
    dragModeEnabled = next;
    if (!next) cancelPointerTranslations(container);
    updateDragControl(container);
    announce(
      container,
      next
        ? 'Drag mode enabled. Workspace and district dragging is available.'
        : 'Drag mode disabled. Workspace and district dragging is off.'
    );
  }

  function bindDragControl(container) {
    var toggle = container.querySelector('[data-map-drag]');
    if (toggle && typeof toggle.addEventListener === 'function') {
      toggle.addEventListener('click', function () {
        if (layoutState.status !== 'ready') return;
        setDragMode(container, !dragModeEnabled);
      });
    }
    updateDragControl(container);
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
      stop();
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
    // Center Selected has no button to keep in step any more; the canvas menu
    // computes its own disabled state from selectedNodeAnchor() each time it
    // opens, which cannot go stale.
  }

  // selectedNodeAnchor finds the world box of whatever is selected, whether that
  // is a building or a group district. A district reports its whole effective
  // frame so Center selected frames the district, not a tile-sized patch of its
  // corner (#346 FR-47).
  function selectedNodeAnchor() {
    if (!selectedId || !lastWorldLayout) return null;
    var node = null;
    lastWorldLayout.nodes.forEach(function (candidate) {
      if (candidate.id === selectedId) node = { x: candidate.x, y: candidate.y };
    });
    if (node) return node;
    var district = renderedDistrict(selectedId);
    if (district) {
      return { x: district.x, y: district.y, width: district.width, height: district.height };
    }
    return null;
  }

  // renderedDistrict returns the district record currently drawn for a group id,
  // which is where its effective frame lives.
  function renderedDistrict(id) {
    if (!id || !lastWorldLayout) return null;
    var found = null;
    lastWorldLayout.districts.forEach(function (district) {
      if (district.id === id) found = district;
    });
    return found;
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
    // Fit all, Center selected and Reset view have no buttons any more: they are
    // items on the canvas context menu (see runMenuAction), plus the f / 0 keys
    // below. Nothing about what they do changed.
    on('[data-map-move]', function () {
      startKeyboardMove(container);
      var canvasEl = container.querySelector('[data-ws-map-viewport]');
      if (canvasEl && canvasEl.focus) canvasEl.focus();
    });

    if (!canvas || typeof canvas.addEventListener !== 'function') return;
    // Keyboard equivalents for every camera gesture, so navigating the map
    // never requires a pointer (FR-115).
    canvas.addEventListener('keydown', function (event) {
      // An active resize owns Escape before anything else does (#346 FR-165).
      // Bound here as well as on the handle because a POINTER resize does not
      // require the handle to still hold focus, and "Escape cancels what I am
      // doing" must not depend on where focus happens to be.
      if (event.key === 'Escape' && resizeState) {
        if (event.preventDefault) event.preventDefault();
        if (event.stopPropagation) event.stopPropagation();
        cancelResize('Resize cancelled. The group is back at its saved size.');
        return;
      }
      // Keyboard Move owns the arrow keys while it is active: they move the
      // building, not the camera (FR-78).
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
        // With the framing buttons gone, this key is a direct route to what the
        // canvas menu offers, so it announces its result the way the menu item
        // does.
        case '0':
          resetView(container);
          announce(container, 'View reset. Workspace positions are unchanged');
          break;
        // `f` never reaches this handler in the app: keyboard-navigation.js
        // claims it globally as the link-hint activation key and stops the event
        // in the capture phase. It is kept because the behaviour is correct on
        // its own terms — a page without that module still gets Fit All — but
        // nothing may advertise it. The reachable routes are the canvas context
        // menu (right-click, or Shift+F10 on the focused canvas) and the buttons
        // that action never lost.
        case 'f':
        case 'F':
          announce(container, fitAllAnnouncement(fitAll(container)));
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

  // fitAll returns what it managed to do, so the two routes that offer it —
  // the canvas menu and the keyboard — can announce the same thing without
  // each re-deriving it from the resulting camera and drifting apart.
  function fitAll(container) {
    if (!lastWorldLayout) return null;
    var canvas = container.querySelector('[data-ws-map-viewport]');
    var fitted = fitBounds(lastWorldLayout.bounds, framedViewport(canvas));
    setCamera(liftAboveControls(fitted), container);
    return fitted;
  }

  // What Fit All says afterwards. Framing far enough out to show everything is
  // the ordinary case now (#307); the limitation message is reserved for the
  // layout so wide that even the framing floor could not contain it, where
  // claiming success would send someone looking for a workspace that is not on
  // screen.
  function fitAllAnnouncement(result) {
    return result && result.fitsEverything === false
      ? 'Zoomed out as far as the map goes. Some workspaces are still off-screen — pan to reach them.'
      : 'Showing every workspace';
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

  // ---------- district resizing, wired (#346 FR-52 – FR-75) ----------
  //
  // One state machine serves the pointer and the keyboard. Both produce a
  // preview, both validate through the same collision evaluator, and both
  // commit through the same single PATCH — so "resize by dragging" and "resize
  // by arrow keys" cannot end up meaning different things.

  var resizeState = null;

  /** Is a district resizable right now? (FR-52, FR-115, FR-148) */
  function canResizeDistrict(groupId) {
    if (isMapReadOnly()) return false;
    var district = renderedDistrict(groupId);
    return !!district && !district.collapsed;
  }

  /** The padded rectangle a district's current members require (FR-76). */
  function districtContentBounds(groupId) {
    if (!lastWorldLayout) return null;
    var members = lastWorldLayout.nodes.filter(function (node) {
      return node.groupId === groupId;
    });
    return memberBounds(members);
  }

  function resizeOverlayEls(container) {
    if (!container || typeof container.querySelector !== 'function') return null;
    var overlay = container.querySelector('[data-ws-map-resize]');
    if (!overlay) return null;
    return {
      overlay: overlay,
      box: container.querySelector('[data-ws-map-resize-box]'),
      readout: container.querySelector('[data-ws-map-resize-readout]')
    };
  }

  /**
   * Put the overlay over the selected district, in screen space.
   *
   * Called on render, on every camera change, and on viewport resize. When
   * nothing resizable is selected the overlay is hidden outright rather than
   * left as an invisible rectangle over the map (FR-53, FR-157).
   */
  function positionResizeOverlay(container) {
    var els = resizeOverlayEls(container);
    if (!els) return;
    var groupId = selectedId;
    var district = canResizeDistrict(groupId) ? renderedDistrict(groupId) : null;
    if (!district) {
      els.overlay.hidden = true;
      return;
    }
    var frame = resizeState && resizeState.groupId === groupId ? resizeState.preview : district;
    var canvas = container.querySelector('[data-ws-map-viewport]');
    var viewport = framedViewport(canvas).full;
    var topLeft = worldToScreen({ x: frame.x, y: frame.y }, camera, viewport);
    var size = { width: frame.width * camera.zoom, height: frame.height * camera.zoom };

    els.overlay.hidden = false;
    // The overlay belongs to one district, so it wears that district's accent
    // rather than a fixed colour — a violet group with an amber selection
    // rectangle reads as two different things (#346 FR-129). Blocked and error
    // states still override it, because those must never be theme-coloured
    // (FR-86).
    if (els.overlay.classList) {
      DISTRICT_ACCENTS.forEach(function (id) {
        els.overlay.classList.toggle('ws-map-accent-' + id, id === safeAccent(district.accent));
      });
    }
    if (els.box && els.box.style) {
      els.box.style.left = topLeft.x + 'px';
      els.box.style.top = topLeft.y + 'px';
      els.box.style.width = size.width + 'px';
      els.box.style.height = size.height + 'px';
    }
    var name = (district.ws && district.ws.name) || 'group';
    var handles = container.querySelectorAll('[data-resize-handle]');
    Array.prototype.forEach.call(handles, function (handle) {
      var edge = handle.getAttribute('data-resize-handle');
      // An accurate name for each handle: which group, and which edge or corner
      // it changes (FR-69).
      handle.setAttribute(
        'aria-label',
        'Resize ' + name + ' group: ' + (RESIZE_HANDLE_LABELS[edge] || edge)
      );
      handle.setAttribute('tabindex', '0');
    });
  }

  /** The concise width×height and snap-state readout (FR-67, FR-68). */
  function resizeReadout(state) {
    var frame = state.preview;
    var size = Math.round(frame.width) + ' × ' + Math.round(frame.height);
    var snapping = state.snapBypassed || !layoutState.snapToGrid ? 'free' : 'snapped';
    if (state.conflict && state.conflict.blocked) {
      return size + ' · blocked by ' + state.conflict.name;
    }
    if (state.clamped) return size + ' · smallest that fits its workspaces';
    return size + ' · ' + snapping;
  }

  function paintResize(container, state) {
    var els = resizeOverlayEls(container);
    if (!els) return;
    positionResizeOverlay(container);
    var blocked = !!(state.conflict && state.conflict.blocked);
    if (els.box && els.box.classList) {
      els.box.classList.toggle('is-resizing', true);
      // Blocked and clamped are different states and read differently: blocked
      // is refused, clamped is the honest floor. Neither relies on colour —
      // the border style changes and the readout says which (FR-81, FR-163).
      els.box.classList.toggle('is-blocked', blocked);
      els.box.classList.toggle('is-clamped', !blocked && !!state.clamped);
    }
    if (els.readout) {
      els.readout.hidden = false;
      els.readout.textContent = resizeReadout(state);
    }
  }

  function clearResizePaint(container) {
    var els = resizeOverlayEls(container);
    if (!els) return;
    if (els.box && els.box.classList) {
      els.box.classList.remove('is-resizing');
      els.box.classList.remove('is-blocked');
      els.box.classList.remove('is-clamped');
    }
    if (els.readout) {
      els.readout.hidden = true;
      els.readout.textContent = '';
    }
  }

  /**
   * Begin a resize gesture.
   *
   * `mode` is 'pointer' or 'keyboard'; the geometry is identical either way, and
   * only how the delta arrives differs (FR-70).
   */
  function beginResize(container, groupId, handle, mode) {
    if (!canResizeDistrict(groupId)) return null;
    var district = renderedDistrict(groupId);
    if (!district) return null;
    resizeState = {
      container: container,
      groupId: groupId,
      handle: handle,
      mode: mode,
      // The frame the gesture started from, and the one Escape restores (FR-65).
      origin: { x: district.x, y: district.y, width: district.width, height: district.height },
      preview: { x: district.x, y: district.y, width: district.width, height: district.height },
      contentBounds: districtContentBounds(groupId),
      snapBypassed: false,
      clamped: false,
      changed: false,
      conflict: { blocked: false, reason: '', name: '' },
      // Announcements are bounded: start, blocked-state changes, commit,
      // cancel, and failure — never one per pointer move (FR-162).
      announcedBlocked: false,
      initiator: null
    };
    announce(
      container,
      'Resizing ' +
        ((district.ws && district.ws.name) || 'group') +
        ' — ' +
        (mode === 'keyboard'
          ? 'arrow keys to size, Enter to save, Escape to put it back.'
          : 'release to save, or press Escape to put it back.')
    );
    return resizeState;
  }

  /** Apply a delta to the active gesture and repaint its preview. */
  function updateResize(delta, options) {
    if (!resizeState) return;
    var opts = options || {};
    if (typeof opts.snapBypassed === 'boolean') resizeState.snapBypassed = opts.snapBypassed;
    var result = resizeFrame(resizeState.origin, resizeState.handle, delta, {
      snap: layoutState.snapToGrid && !resizeState.snapBypassed,
      contentBounds: resizeState.contentBounds
    });
    resizeState.preview = result.frame;
    resizeState.clamped = result.clamped;
    resizeState.changed = result.changed;
    resizeState.conflict = frameConflict(result.frame, resizeState.groupId, lastWorldLayout);
    paintResize(resizeState.container, resizeState);

    // Only a *change* in blocked state is announced, not every move (FR-162).
    if (resizeState.conflict.blocked !== resizeState.announcedBlocked) {
      resizeState.announcedBlocked = resizeState.conflict.blocked;
      if (resizeState.conflict.blocked) {
        announce(
          resizeState.container,
          'Blocked — this would draw the group around ' + resizeState.conflict.name + '.'
        );
      }
    }
  }

  /** End a gesture without saving, restoring the committed frame (FR-65). */
  function cancelResize(message) {
    if (!resizeState) return;
    var state = resizeState;
    resizeState = null;
    clearResizePaint(state.container);
    positionResizeOverlay(state.container);
    restoreResizeFocus(state);
    if (message) announce(state.container, message);
  }

  function restoreResizeFocus(state) {
    // Focus goes back to whatever started the gesture, unless it is gone
    // (FR-75).
    var target = state.initiator;
    if (target && typeof target.focus === 'function') {
      target.focus();
      return;
    }
    var handle = resizeHandleEl(state.container, state.handle);
    if (handle && typeof handle.focus === 'function') handle.focus();
  }

  /** One overlay per container, so the handle lookup is unambiguous. */
  function resizeHandleEl(container, edge) {
    if (!container || typeof container.querySelector !== 'function') return null;
    return container.querySelector('[data-resize-handle="' + edge + '"]');
  }

  /**
   * Commit a resize.
   *
   * Exactly one bounded PATCH for a changed, valid release (FR-63); nothing at
   * all for an unchanged one (FR-64) or a blocked one (FR-82). A failure puts
   * the committed frame back, keeps the selection, and offers a real retry
   * (FR-66).
   */
  function commitResize() {
    if (!resizeState) return Promise.resolve(false);
    var state = resizeState;
    resizeState = null;
    var container = state.container;
    clearResizePaint(container);

    if (state.conflict && state.conflict.blocked) {
      positionResizeOverlay(container);
      restoreResizeFocus(state);
      announce(
        container,
        'Resize refused — it would have drawn the group around ' + state.conflict.name + '.'
      );
      return Promise.resolve(false);
    }
    if (!state.changed) {
      positionResizeOverlay(container);
      restoreResizeFocus(state);
      return Promise.resolve(false);
    }
    return applyGroupFrame(container, state.groupId, state.preview).then(function (ok) {
      restoreResizeFocus(state);
      return ok;
    });
  }

  // ---------- the one district action surface (#346 FR-156) ----------
  //
  // Direct handles, the group context menu, and the Home Map-layout rail all
  // call these. One implementation means one validation rule, one persistence
  // path, one announcement, and one failure behaviour, whichever surface the
  // user reached for.

  /** Store a user-chosen minimum rectangle (FR-42). */
  function applyGroupFrame(container, groupId, frame) {
    return patchLayout([
      {
        op: 'set_group_frame',
        group_id: groupId,
        frame: { x: frame.x, y: frame.y, width: frame.width, height: frame.height }
      }
    ])
      .then(function () {
        settleLayout();
        notifyDistrictChanged(groupId);
        announce(container, 'Group size saved.');
        return true;
      })
      .catch(function () {
        // The canonical committed frame is whatever the server still holds, so
        // simply repainting from unchanged local state restores it.
        settleLayout();
        announceRetryableFailure(
          container,
          'That group size could not be saved. The group is back at its last saved size.',
          function () {
            return applyGroupFrame(container, groupId, frame);
          }
        );
        return false;
      });
  }

  /**
   * Enter keyboard resize mode without requiring focus on a small edge target
   * (FR-74).
   *
   * This is the route the context menu and the Home rail use. It selects the
   * district first — a resize with nothing selected has no overlay to drive —
   * then focuses the bottom-right handle, which is the one a user reaching for
   * "make this bigger" expects.
   */
  function startGroupResize(container, groupId) {
    if (!canResizeDistrict(groupId)) {
      announce(container, 'That group cannot be resized right now.');
      return false;
    }
    if (selectedId !== groupId && lastMount) {
      applySelection(container, lastMount.state.workspaces || [], groupId, lastMount.state);
    }
    positionResizeOverlay(container);
    // The bottom-right corner: the one a user reaching for "make this bigger"
    // expects, and the one that grows without moving the district's anchor.
    var handle = resizeHandleEl(container, 'se');
    var state = beginResize(container, groupId, 'se', 'keyboard');
    if (!state) return false;
    state.initiator = handle;
    state.step = { x: 0, y: 0 };
    if (handle && typeof handle.focus === 'function') handle.focus();
    return true;
  }

  /**
   * Collapse or expand a district (#346 FR-102, FR-103).
   *
   * Collapsing prunes any of the group's members from the visible multi-select
   * set first: a checked building that is about to stop being drawn would leave
   * the action bar claiming a selection the user can no longer see or clear
   * (FR-105). The group's own selection is deliberately kept.
   */
  function setGroupCollapsed(container, groupId, collapsed) {
    if (collapsed && lastWorldLayout) {
      lastWorldLayout.nodes.forEach(function (node) {
        if (node.groupId === groupId) delete multiSelected[node.id];
      });
      // The action bar has to stop claiming those workspaces immediately, not
      // after the round-trip: they are about to disappear from the map, and a
      // count the user cannot reconcile with what they can see is worse than a
      // slightly early one.
      if (container) updateSelBar(container);
    }
    // A collapsed district has no frame on screen to size, so any resize in
    // flight ends rather than being left pointing at something invisible.
    if (collapsed && resizeState && resizeState.groupId === groupId) cancelResize();

    return patchLayout([{ op: 'set_group_collapsed', group_id: groupId, collapsed: !!collapsed }])
      .then(function () {
        settleLayout();
        notifyDistrictChanged(groupId);
        announce(
          container,
          collapsed
            ? 'Group collapsed. Its workspaces are hidden on the map, not removed.'
            : 'Group expanded.'
        );
        return true;
      })
      .catch(function () {
        settleLayout();
        announceRetryableFailure(
          container,
          collapsed
            ? 'That group could not be collapsed. It is still open.'
            : 'That group could not be expanded. It is still collapsed.',
          function () {
            return setGroupCollapsed(container, groupId, collapsed);
          }
        );
        return false;
      });
  }

  /**
   * Choose a curated accent and/or district theme (#346 FR-121, FR-126).
   *
   * Only identifiers travel — never a colour, never a rule, never a URL. The
   * server validates them against the same catalog, and an unknown one is
   * refused rather than stored, so nothing a client can invent ever reaches a
   * stylesheet (FR-125, FR-194).
   */
  function setGroupAppearance(container, groupId, choice) {
    var operation = { op: 'set_group_appearance', group_id: groupId };
    if (choice && choice.accent) operation.accent = choice.accent;
    if (choice && choice.theme) operation.theme = choice.theme;
    if (!operation.accent && !operation.theme) return Promise.resolve(false);

    return patchLayout([operation])
      .then(function () {
        settleLayout();
        notifyDistrictChanged(groupId);
        announce(container, 'Group appearance updated.');
        return true;
      })
      .catch(function () {
        settleLayout();
        announceRetryableFailure(
          container,
          'That appearance could not be saved. The group looks the way it did.',
          function () {
            return setGroupAppearance(container, groupId, choice);
          }
        );
        return false;
      });
  }

  /**
   * Restore the default accent and theme (#346 FR-137).
   *
   * Appearance only: geometry, sizing mode, collapse state, members, and
   * coordinates are all left exactly as they are.
   */
  function resetGroupAppearance(container, groupId) {
    return patchLayout([{ op: 'reset_group_appearance', group_id: groupId }])
      .then(function () {
        settleLayout();
        notifyDistrictChanged(groupId);
        announce(container, 'Group appearance reset to the default.');
        return true;
      })
      .catch(function () {
        settleLayout();
        announceRetryableFailure(
          container,
          'That appearance could not be reset. The group looks the way it did.',
          function () {
            return resetGroupAppearance(container, groupId);
          }
        );
        return false;
      });
  }

  /** Return a district to automatic sizing (FR-40, FR-41). */
  function fitGroupToContents(container, groupId) {
    return patchLayout([{ op: 'fit_group_to_contents', group_id: groupId }])
      .then(function () {
        settleLayout();
        notifyDistrictChanged(groupId);
        announce(container, 'Group fitted to its workspaces.');
        return true;
      })
      .catch(function () {
        settleLayout();
        announceRetryableFailure(
          container,
          'Fit to contents could not be saved. Nothing changed.',
          function () {
            return fitGroupToContents(container, groupId);
          }
        );
        return false;
      });
  }

  /**
   * Tell the host a district's presentation changed (#346 FR-152).
   *
   * The Home rail states the sizing mode in words, and a rail that still reads
   * "Automatic size" after a committed resize is simply wrong. The rail owns its
   * own rendering, so the Map reports the change rather than reaching into it.
   */
  function notifyDistrictChanged(groupId) {
    var state = lastMount && lastMount.state;
    if (state && typeof state.onDistrictChanged === 'function') {
      state.onDistrictChanged(groupId);
    }
  }

  // The last failed district action, so a surface can offer a real retry rather
  // than a button that reruns a guess (FR-66).
  var lastDistrictFailure = null;

  function announceRetryableFailure(container, message, retry) {
    lastDistrictFailure = { message: message, retry: retry };
    announce(container, message + ' Choose Retry to try again.');
  }

  function retryLastDistrictAction() {
    var failure = lastDistrictFailure;
    if (!failure) return Promise.resolve(false);
    lastDistrictFailure = null;
    return failure.retry();
  }

  // ---------- grouping handoff (#346 FR-13 – FR-22, FR-27, FR-28) ----------
  //
  // Grouping is a HIERARCHY mutation, owned by workspace-bulk-actions.js and
  // shared with Tree. The Map adds two things around it and nothing else: it
  // pins the coordinates of the workspaces about to be grouped, and afterwards
  // it selects and frames the district the hierarchy actually produced. No part
  // of this sends a parent or an order index through the layout API — it has no
  // field for either (FR-98).

  /**
   * The drawn anchors of records that are about to be grouped but have never
   * been placed by hand.
   *
   * Grouping does not move anything, but it does change what *automatic*
   * placement would produce: a workspace that was its own island becomes a
   * member of a group island, so its seeded cell changes and an unplaced
   * building would appear to jump on the next render. Pinning its current
   * coordinate first is what makes "grouping preserves every selected
   * workspace's exact world coordinate" true for unplaced records too (FR-13).
   *
   * Records the user has already placed are skipped: their saved anchor already
   * says where they are, and rewriting it would be a pointless write.
   */
  function captureGroupingAnchors(ids) {
    var wanted = Object.create(null);
    (Array.isArray(ids) ? ids : []).forEach(function (id) {
      if (id) wanted[id] = true;
    });
    var anchors = Object.create(null);
    if (!lastWorldLayout) return anchors;
    lastWorldLayout.nodes.forEach(function (node) {
      if (!wanted[node.id] || node.saved) return;
      anchors[node.id] = { x: node.x, y: node.y };
    });
    return anchors;
  }

  /**
   * Is the whole district already on screen? Used to keep the camera still
   * unless it actually has to move (FR-22).
   */
  function districtFullyVisible(district, container) {
    var canvas = container && container.querySelector('[data-ws-map-viewport]');
    var viewport = framedViewport(canvas).full;
    if (!viewport.width || !viewport.height) return false;
    var topLeft = worldToScreen({ x: district.x, y: district.y }, camera, viewport);
    var bottomRight = worldToScreen(
      { x: district.x + district.width, y: district.y + district.height },
      camera,
      viewport
    );
    return (
      topLeft.x >= 0 &&
      topLeft.y >= 0 &&
      bottomRight.x <= viewport.width &&
      bottomRight.y <= viewport.height - CONTROL_STRIP_HEIGHT
    );
  }

  /**
   * Frame a district without changing the zoom the user chose (FR-22).
   * Returns whether the camera actually moved.
   */
  function focusGroupDistrict(groupId, container) {
    var district = renderedDistrict(groupId);
    if (!district || !container) return false;
    if (districtFullyVisible(district, container)) return false;
    setCamera(centerOn(camera, district), container);
    return true;
  }

  /**
   * Adopt a group the hierarchy just created.
   *
   * `placed` is the authoritative member list — the workspaces the workspace
   * store actually reparented, not the ones the click asked for — so a run where
   * only some reparents succeeded frames only what really moved (FR-28).
   *
   * Nothing about the district's *presentation* is written here. A new group is
   * expanded, automatically sized, and default-looking (FR-18 – FR-20), which is
   * exactly the record the layout deliberately does not store, and its frame is
   * derived from its members on every render. So the compact district is a
   * property of the model rather than of a write that could fail. The one write
   * this makes is the coordinate pin above, and its failure is survivable: the
   * group and its membership are already committed, the district still renders
   * tight and automatic, and the caller is told plainly (FR-27).
   */
  function adoptNewGroup(groupId, anchors, options) {
    var opts = options || {};
    var container = (lastMount && lastMount.container) || opts.container || null;
    // The grouped workspaces still exist, so mount()'s prune-on-refresh cannot
    // drop them from the checked set — clear it explicitly (FR-21).
    multiSelected = Object.create(null);

    var pinned = Object.keys(anchors || {});
    var write =
      pinned.length && layoutState.status === 'ready'
        ? patchLayout([{ op: 'set_positions', positions: anchors }])
        : Promise.resolve(null);

    return write.then(
      function () {
        settleLayout();
        if (container) focusGroupDistrict(groupId, container);
        return { groupId: groupId, pinned: pinned, saved: true };
      },
      function () {
        // Membership stands; only the coordinate pin failed. Say so, and hand
        // back a retry that reuses the exact anchors we meant to save.
        settleLayout();
        if (container) {
          focusGroupDistrict(groupId, container);
          announce(
            container,
            'The group was created, but the positions of its workspaces could not be saved. They may move to automatic placement.'
          );
        }
        return {
          groupId: groupId,
          pinned: pinned,
          saved: false,
          retry: function () {
            return adoptNewGroup(groupId, anchors, options);
          }
        };
      }
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
    // A district is anchored by the corner of its effective frame, which is
    // checked first because a populated automatic district's stored point means
    // nothing any more — it is not what was drawn (#346 FR-17, FR-46).
    var district = renderedDistrict(id);
    if (district) return { x: district.x, y: district.y };
    var saved = layoutState.positions[id];
    if (saved) return { x: saved.x, y: saved.y };
    if (!lastWorldLayout) return null;
    var found = null;
    lastWorldLayout.nodes.forEach(function (node) {
      if (node.id === id) found = { x: node.x, y: node.y };
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
  /**
   * The operations one member move has to commit.
   *
   * Usually just the anchor. When the member belongs to a custom-sized district
   * and the move pushes past one of its edges, the reconciled minimum rectangle
   * rides along in the SAME patch, so the coordinate and the frame that has to
   * contain it are one accepted layout change rather than two that can half-fail
   * (#346 FR-38).
   *
   * Returns null when the move is refused: expanding the frame across a
   * workspace outside the group, or across another district, would make the Map
   * claim a membership that does not exist, and that is blocked before save
   * rather than saved and explained afterwards (FR-83).
   */
  function memberMoveOperations(id, point, intent) {
    var positions = {};
    positions[id] = { x: point.x, y: point.y };
    var operations = [{ op: 'set_positions', positions: positions }];

    // A pending leave is evaluated against the hierarchy it intends to create.
    // Do not expand or collision-check the source's custom minimum solely to
    // contain a member that will be removed after confirmation. If the user
    // declines or the hierarchy write fails, the unchanged saved minimum plus
    // the retained member make effectiveDistrictFrame render truthful expanded
    // containment without persisting that temporary expansion (#374 AR15).
    if (intent && intent.kind === 'leave') {
      return { operations: operations, conflict: null };
    }

    var node = null;
    if (lastWorldLayout) {
      lastWorldLayout.nodes.forEach(function (candidate) {
        if (candidate.id === id) node = candidate;
      });
    }
    if (!node || !node.groupId) return { operations: operations, conflict: null };

    var district = renderedDistrict(node.groupId);
    var members = lastWorldLayout.nodes.filter(function (candidate) {
      return candidate.groupId === node.groupId;
    });
    var reconciled = reconcileCustomFrame(district, members, id, point);
    if (!reconciled || !reconciled.changed) return { operations: operations, conflict: null };

    var conflict = frameConflict(reconciled.frame, node.groupId, lastWorldLayout);
    if (conflict.blocked) return { operations: null, conflict: conflict };

    operations.push({
      op: 'set_group_frame',
      group_id: node.groupId,
      frame: {
        x: reconciled.frame.x,
        y: reconciled.frame.y,
        width: reconciled.frame.width,
        height: reconciled.frame.height
      }
    });
    return { operations: operations, conflict: null };
  }

  function commitMove(container, id, el, point, previous, intent) {
    var plan = memberMoveOperations(id, point, intent);
    if (!plan.operations) {
      // Blocked before save: nothing moved, nothing was asked of the server.
      placeElement(el, previous);
      if (el && el.focus) el.focus();
      announce(
        container,
        'That move is blocked — its group would have to grow around ' +
          plan.conflict.name +
          ', which is not in it.'
      );
      return Promise.resolve(false);
    }
    return patchLayout(plan.operations)
      .then(function (result) {
        var committed = (result && result.positions && result.positions[id]) || point;
        placeElement(el, committed);
        pendingFocusId = id;
        // The coordinate is saved first and the membership second, because they
        // are two different APIs and cannot share a transaction. This order is
        // the survivable one: a failed reparent leaves a workspace that moved
        // but did not change groups, which is a state the user can see and
        // repeat. The reverse would leave it in a new group at its old spot.
        // ...and the membership is asked about before it is written. This is
        // the only Map gesture with no Map-side undo (FR-6g).
        if (intent && (intent.kind === 'join' || intent.kind === 'leave')) {
          return confirmMembershipOnDrop(container, id, el, intent, committed);
        }
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

  // ---------------------------------------------------------------------------
  // Drop confirmation (#346 FR-6g, #374)
  //
  // Every drop that changes group membership asks first. Join and leave share
  // one panel controller so focus, dismissal, escaping, and one-answer ownership
  // cannot drift between visually symmetric gestures.
  //
  // The panel is the map's own, not window.confirm, for the same reason the
  // context menu is: a native modal seizing focus the instant the mouse comes up
  // reads as an error, and it cannot show which district it is talking about.
  // ---------------------------------------------------------------------------

  // The one open confirmation. Never more than one: it is opened by a pointer
  // release, and a release cannot overlap another.
  var dropConfirmState = null;

  function dropConfirmHTML(spec) {
    var leave = spec.kind === 'leave';
    return (
      '<div class="ws-map-drop-confirm' +
      (leave ? ' is-leave' : '') +
      '" data-ws-map-drop-confirm role="dialog" ' +
      'aria-labelledby="wsMapDropConfirmTitle" aria-describedby="wsMapDropConfirmBody">' +
      '<p class="ws-map-drop-confirm-title" id="wsMapDropConfirmTitle">' +
      (leave ? 'Remove ' : 'Move ') +
      escapeHtml(spec.wsName) +
      (leave ? ' from ' : ' into ') +
      escapeHtml(spec.groupName) +
      '?</p>' +
      '<p class="ws-map-drop-confirm-body" id="wsMapDropConfirmBody">' +
      'Its position stays where you dropped it.</p>' +
      '<div class="ws-map-drop-confirm-actions">' +
      '<button type="button" class="ws-map-drop-confirm-go" data-drop-confirm="' +
      (leave ? 'leave' : 'join') +
      '">' +
      (leave ? 'Remove from group' : 'Move into group') +
      '</button>' +
      '<button type="button" class="ws-map-drop-confirm-no" data-drop-confirm="decline">' +
      (leave ? 'Keep in group' : 'Keep it out') +
      '</button>' +
      '</div>' +
      '</div>'
    );
  }

  /**
   * Ask before the reparent, and answer nothing until the user does.
   *
   * @returns {boolean} false when there is nowhere to render it, which the
   * caller must treat as "do not join" rather than as consent.
   */
  function openDropConfirm(container, spec) {
    closeDropConfirm({ restoreFocus: false });
    if (!container || typeof container.querySelector !== 'function') return false;
    var host = container.querySelector('[data-ws-map-confirm-host]');
    if (!host) return false;
    host.innerHTML = dropConfirmHTML(spec);
    var panel =
      typeof host.querySelector === 'function'
        ? host.querySelector('[data-ws-map-drop-confirm]')
        : null;
    if (!panel) {
      host.innerHTML = '';
      return false;
    }

    dropConfirmState = {
      container: container,
      host: host,
      panel: panel,
      groupId: spec.groupId,
      confirmAnswer: spec.kind === 'leave' ? 'leave' : 'join',
      onConfirm: spec.onConfirm,
      onDecline: spec.onDecline,
      // Focus goes back to the workspace that was dropped, so a keyboard user
      // is returned to the thing they were moving rather than to the page top.
      restoreFocusTo: spec.el || null,
      settled: false,
      teardown: []
    };

    // The involved district stays visually identified while the question is on
    // screen. Join marks a destination; leave uses a distinct source state so
    // the Map never implies the workspace is about to join its current group.
    if (spec.kind === 'leave') markLeaveSource(container, spec.groupId);
    else markDropTarget(container, spec.groupId);
    // ...and the panel wears that district's colour, so a violet group is never
    // asked about in the default amber (the same fix the resize overlay needed).
    wearDistrictAccent(container, panel, spec.groupId);
    placeMenu(panel, anchorForElement(spec.el));
    bindDropConfirmInteractions();
    focusDropConfirmButton(0);
    return true;
  }

  /**
   * Give `el` the accent of the district it is about (#346 FR-129).
   *
   * Same reasoning as the resize overlay: a panel that talks about a violet
   * group while wearing the default amber reads as being about something else.
   */
  function wearDistrictAccent(container, el, groupId) {
    if (!el || !el.classList) return;
    var accent = safeAccent(presentationFor(groupId).accent);
    DISTRICT_ACCENTS.forEach(function (id) {
      el.classList.toggle('ws-map-accent-' + id, id === accent);
    });
  }

  function dropConfirmButtons() {
    var state = dropConfirmState;
    if (!state || typeof state.panel.querySelectorAll !== 'function') return [];
    return Array.prototype.slice.call(state.panel.querySelectorAll('[data-drop-confirm]'));
  }

  function focusDropConfirmButton(index) {
    var buttons = dropConfirmButtons();
    var el = buttons[index];
    if (el && typeof el.focus === 'function') el.focus();
  }

  /**
   * Answer the question exactly once.
   *
   * Every dismissal route lands here, and everything that is not the panel's
   * explicit affirmative answer is a decline — an unanswered question must
   * never be read as a yes.
   */
  function settleDropConfirm(answer, options) {
    var state = dropConfirmState;
    if (!state || state.settled) return;
    state.settled = true;
    var confirmed = answer === state.confirmAnswer;
    var onConfirm = state.onConfirm;
    var onDecline = state.onDecline;
    closeDropConfirm({ restoreFocus: !options || options.restoreFocus !== false });
    if (confirmed) {
      if (typeof onConfirm === 'function') onConfirm();
    } else if (typeof onDecline === 'function') {
      onDecline(options);
    }
  }

  function closeDropConfirm(options) {
    var state = dropConfirmState;
    if (!state) return;
    dropConfirmState = null;
    state.teardown.forEach(function (undo) {
      if (typeof undo === 'function') undo();
    });
    if (state.host) state.host.innerHTML = '';
    markDropTarget(state.container, '');
    markLeaveSource(state.container, '');
    var restore = options && options.restoreFocus;
    if (restore && state.restoreFocusTo && typeof state.restoreFocusTo.focus === 'function') {
      state.restoreFocusTo.focus();
    }
  }

  /**
   * Keyboard and pointer routes out of the panel.
   *
   * Escape and a click on the map behind it both decline, because the safe
   * reading of "went away without choosing" is that no group changed. Tab wraps
   * between the two buttons so focus cannot wander onto the map underneath
   * while a question is still open.
   */
  function bindDropConfirmInteractions() {
    var state = dropConfirmState;
    if (!state) return;

    var onKeyDown = function (event) {
      if (!dropConfirmState) return;
      if (event.key === 'Escape') {
        event.preventDefault();
        settleDropConfirm('decline');
        return;
      }
      if (event.key !== 'Tab') return;
      var buttons = dropConfirmButtons();
      if (buttons.length < 2) return;
      var at = buttons.indexOf(event.target);
      // Only the ends need handling; the middle is the browser's own business.
      if (event.shiftKey && at === 0) {
        event.preventDefault();
        focusDropConfirmButton(buttons.length - 1);
      } else if (!event.shiftKey && at === buttons.length - 1) {
        event.preventDefault();
        focusDropConfirmButton(0);
      }
    };

    var onClick = function (event) {
      if (!dropConfirmState) return;
      var target = event.target;
      var button =
        target && typeof target.closest === 'function'
          ? target.closest('[data-drop-confirm]')
          : null;
      if (button) {
        event.preventDefault();
        settleDropConfirm(button.getAttribute('data-drop-confirm'));
        return;
      }
      // A click anywhere else dismisses it, which is a decline.
      if (
        target &&
        typeof target.closest === 'function' &&
        target.closest('[data-ws-map-drop-confirm]')
      ) {
        return;
      }
      settleDropConfirm('decline');
    };

    if (typeof document !== 'undefined' && typeof document.addEventListener === 'function') {
      document.addEventListener('keydown', onKeyDown, true);
      document.addEventListener('pointerdown', onClick, true);
      state.teardown.push(function () {
        document.removeEventListener('keydown', onKeyDown, true);
        document.removeEventListener('pointerdown', onClick, true);
      });
    }
  }

  /**
   * Put the question on screen and resolve with what the user chose.
   *
   * The coordinate is already saved by the time this runs, so declining is not
   * a failure and is not reported as one: it leaves exactly the state a drop on
   * open ground would have left.
   */
  function confirmMembershipOnDrop(container, id, el, intent, committed) {
    var leaving = intent.kind === 'leave';
    return new Promise(function (resolve) {
      var opened = openDropConfirm(container, {
        kind: intent.kind,
        el: el,
        groupId: intent.groupId,
        groupName: intent.name,
        wsName: intent.movingName || 'this workspace',
        onConfirm: function () {
          resolve(changeMembershipOnDrop(container, id, intent, committed));
        },
        onDecline: function (options) {
          announce(
            container,
            'Moved to ' +
              formatCoordinate(committed) +
              (leaving ? '. It stays in ' : '. It stays out of ') +
              intent.name +
              '.'
          );
          if (!options || !options.skipRedraw) settleLayout();
          resolve(false);
        }
      });
      if (opened) {
        announce(
          container,
          'Moved to ' +
            formatCoordinate(committed) +
            '. Confirm whether to ' +
            (leaving ? 'remove it from ' : 'move it into ') +
            intent.name +
            '.'
        );
        return;
      }
      // Nothing to render into. The coordinate stands; the membership question
      // goes unasked, and an unasked question is never a yes.
      announce(
        container,
        'Moved to ' +
          formatCoordinate(committed) +
          (leaving ? '. It stays in ' + intent.name + '.' : '')
      );
      settleLayout();
      resolve(false);
    });
  }

  /**
   * Apply the one confirmed hierarchy change produced by a Map drop.
   *
   * This is the ONE place the Map writes hierarchy, and it does not do so
   * through the layout API — that API has no vocabulary for a parent and keeps
   * none. It calls the same workspace endpoint Tree uses, with a target id for
   * join or the same empty parent Tree sends for moving to top level.
   *
   * The coordinate is already saved, so failure here is partial rather than
   * total: the workspace stays where it was dropped and retains its old group.
   */
  function changeMembershipOnDrop(container, id, intent, committed) {
    if (typeof fetch !== 'function') return Promise.resolve(false);
    var leaving = intent.kind === 'leave';
    return fetch('/api/workspaces/' + encodeURIComponent(id), {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ parent_id: leaving ? '' : intent.groupId })
    })
      .then(function (response) {
        if (!response || !response.ok) throw new Error('reparent failed');
        announce(
          container,
          'Moved to ' +
            formatCoordinate(committed) +
            (leaving ? ' and removed from ' : ' and added to ') +
            intent.name +
            '.'
        );
        // Membership is the host's data, not the Map's: it reloads the tree and
        // re-mounts, which is what redraws district membership and counts.
        notifyHierarchyChanged();
        return true;
      })
      .catch(function () {
        announce(
          container,
          'Moved to ' +
            formatCoordinate(committed) +
            (leaving ? ', but it could not be removed from ' : ', but it could not be added to ') +
            intent.name +
            '. It is still in ' +
            (leaving ? intent.name : 'its previous group') +
            '.'
        );
        settleLayout();
        return false;
      });
  }

  /**
   * Highlight the district a release would drop into (#346 FR-6a).
   *
   * Exactly one at a time, and cleared when the answer is "no group" — a
   * highlight left behind after the pointer moves out would promise a
   * membership change that is no longer going to happen.
   */
  function markDropTarget(container, groupId) {
    if (!container || typeof container.querySelectorAll !== 'function') return;
    var districts = container.querySelectorAll('.ws-map-district[data-group-id]');
    Array.prototype.forEach.call(districts, function (el) {
      if (!el.classList) return;
      el.classList.toggle(
        'is-drop-target',
        !!groupId && el.getAttribute('data-group-id') === groupId
      );
    });
  }

  function markLeaveSource(container, groupId) {
    if (!container || typeof container.querySelectorAll !== 'function') return;
    var districts = container.querySelectorAll('.ws-map-district[data-group-id]');
    Array.prototype.forEach.call(districts, function (el) {
      if (!el.classList) return;
      el.classList.toggle(
        'is-leave-source',
        !!groupId && el.getAttribute('data-group-id') === groupId
      );
    });
  }

  /** Tell the host that workspace hierarchy changed underneath it. */
  function notifyHierarchyChanged() {
    var state = lastMount && lastMount.state;
    if (state && typeof state.onHierarchyChanged === 'function') {
      state.onHierarchyChanged();
      return;
    }
    // Every surface already listens for this; it is how Tree's own mutations
    // reach the rest of the page.
    if (typeof window !== 'undefined' && typeof window.dispatchEvent === 'function') {
      window.dispatchEvent(new Event('ori:workspaces-changed'));
    }
  }

  function bindTileDrag(container, workspaces) {
    var tiles = container.querySelectorAll('.ws-map-tile[data-ws-id]');
    Array.prototype.forEach.call(tiles, function (el) {
      if (!el || typeof el.addEventListener !== 'function') return;

      el.addEventListener('pointerdown', function (event) {
        if (layoutState.status !== 'ready' || !dragModeEnabled) return;
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
        // Show what releasing here would MEAN, not just where it would land. A
        // drop that changes which group a workspace belongs to must never be a
        // surprise discovered afterwards (#346 FR-6a).
        var intent = blocked
          ? { kind: 'none' }
          : dropMembershipIntent(dragState.id, dragState.candidate, workspaces, lastWorldLayout);
        markDropTarget(container, intent.kind === 'join' ? intent.groupId : '');
        if (el.classList) el.classList.toggle('is-leaving', intent.kind === 'leave');
        dragState.intent = intent;
        setDragReadout(
          container,
          dragState.candidate,
          blocked
            ? MOVE_BLOCKED_INSTRUCTION
            : intent.kind === 'join'
              ? 'Release to move this workspace into ' + intent.name + '.'
              : intent.kind === 'leave'
                ? 'Release to remove this workspace from ' + intent.name + '.'
                : undefined,
          intent.kind
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
          el.classList.remove('is-leaving');
        }
        // Every exit path clears it — up, cancel, Escape — so a district can
        // never be left advertising a drop that already happened or was
        // abandoned.
        markDropTarget(container, '');
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
        // Resolve membership against the frames as they stand NOW, before the
        // move is committed and they follow the workspace (#346 FR-6a).
        var intent = dropMembershipIntent(state.id, target, workspaces, lastWorldLayout);
        commitMove(container, state.id, el, target, state.origin, intent);
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
  function setDragReadout(container, point, instruction, membershipKind) {
    var readout = container.querySelector('[data-map-build-coords]');
    var banner = container.querySelector('[data-map-build-banner]');
    if (!readout || !banner) return;
    if (!point) {
      banner.hidden = true;
      if (banner.classList) {
        banner.classList.remove('is-blocked');
        banner.classList.remove('is-leaving');
      }
      return;
    }
    banner.hidden = false;
    // The banner itself carries the same red signal as the tile/district
    // outline — driven by which instruction it's showing, so every call site
    // that already passes MOVE_BLOCKED_INSTRUCTION gets this for free.
    if (banner.classList) {
      banner.classList.toggle('is-blocked', instruction === MOVE_BLOCKED_INSTRUCTION);
      banner.classList.toggle('is-leaving', membershipKind === 'leave');
    }
    setBannerMode(container, instruction || MOVE_INSTRUCTION, false);
    readout.textContent = candidateLabel(point);
  }

  // setBannerMode swaps the banner's instruction. The banner belongs to moving
  // now that build is not a mode: a move is cancelled with Escape or by
  // dropping, so it offers no button of its own.
  function setBannerMode(container, instruction) {
    var text = container.querySelector('[data-map-build-text]');
    if (text) text.textContent = instruction;
  }

  // ---------- moving districts ----------
  //
  // A cluster move is one world-space delta applied to the group and every
  // visible descendant, preserving all relative spacing (FR-86). It is a
  // presentation move: `parent_id` is never in the payload, so no arrangement
  // of buildings can change who belongs to what (FR-8).

  var clusterDrag = null;

  /**
   * The group's members, with their live anchors.
   *
   * Hidden descendants of a collapsed district are included (with no element to
   * preview). They still have to travel with the district and still have to be
   * checked for where they land, even though nothing is drawn for them —
   * otherwise collapsing a group would be a way to move buildings onto other
   * buildings without the Map noticing (#346 FR-113, and task 5.7's
   * "hidden descendant footprints").
   */
  function clusterMembers(container, groupId) {
    if (!lastWorldLayout) return [];
    var members = [];
    var visit = function (node, hidden) {
      if (node.groupId !== groupId) return;
      members.push({
        id: node.id,
        hidden: hidden,
        el: hidden ? null : container.querySelector('.ws-map-tile[data-ws-id="' + node.id + '"]'),
        origin: committedAnchor(node.id) || { x: node.x, y: node.y }
      });
    };
    lastWorldLayout.nodes.forEach(function (node) {
      visit(node, false);
    });
    (lastWorldLayout.hiddenNodes || []).forEach(function (node) {
      visit(node, true);
    });
    return members;
  }

  /**
   * The point a district drag snaps against.
   *
   * Not the frame corner. A district's corner is its top-left member inset by
   * the district padding (#346), so it sits half a gutter off the grid by
   * construction — snapping *it* to the grid would push every member off the
   * grid instead of onto it. The promise "with snapping on, a dragged district
   * lands on the grid" is really about its buildings, so the top-left member
   * anchor is what gets snapped and the frame follows by the same delta. An
   * empty district has no members to speak for it and snaps by its own corner.
   */
  function clusterSnapOrigin(members, frameCorner) {
    if (!members || !members.length) return frameCorner;
    var x = Infinity;
    var y = Infinity;
    members.forEach(function (member) {
      if (member.origin.x < x) x = member.origin.x;
      if (member.origin.y < y) y = member.origin.y;
    });
    return { x: x, y: y };
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
    var buriesBuilding = members.some(function (member) {
      var candidate = { x: member.origin.x + delta.x, y: member.origin.y + delta.y };
      return occupied.some(function (anchor) {
        return footprintsOverlap(candidate, anchor);
      });
    });
    if (buriesBuilding) return true;

    // The frame travels with the cluster, so it has to be checked where it is
    // going too (#346 FR-84). A district whose buildings all land in clear space
    // can still arrive with its outline drawn around someone else's workspace,
    // and that outline is a claim about membership.
    var district = renderedDistrict(groupId);
    if (!district) return false;
    var translated = {
      x: district.x + delta.x,
      y: district.y + delta.y,
      width: district.width,
      height: district.height
    };
    return frameConflict(translated, groupId, lastWorldLayout).blocked;
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

  /**
   * Pointer and keyboard resizing, bound to the screen-space handles.
   *
   * Pointer capture keeps the gesture alive when the pointer outruns the handle,
   * and every exit — up, cancel, Escape — releases it, so the map can never be
   * left stuck mid-resize (FR-65). Nothing persists on pointer-move: the whole
   * gesture is one preview and one PATCH (FR-60, FR-63).
   */
  function bindResizeHandles(container) {
    var handles = container.querySelectorAll('[data-resize-handle]');
    Array.prototype.forEach.call(handles, function (handle) {
      if (!handle || typeof handle.addEventListener !== 'function') return;
      var edge = handle.getAttribute('data-resize-handle');

      handle.addEventListener('pointerdown', function (event) {
        if (event.button != null && event.button !== 0) return;
        var state = beginResize(container, selectedId, edge, 'pointer');
        if (!state) return;
        state.pointerId = event.pointerId;
        state.startX = event.clientX;
        state.startY = event.clientY;
        state.initiator = handle;
        if (handle.setPointerCapture) handle.setPointerCapture(event.pointerId);
        if (event.stopPropagation) event.stopPropagation();
        // Suppress text selection and page scrolling for this gesture only
        // (FR-166).
        if (event.preventDefault) event.preventDefault();
      });

      handle.addEventListener('pointermove', function (event) {
        if (!resizeState || resizeState.mode !== 'pointer') return;
        if (resizeState.initiator !== handle) return;
        if (event.pointerId !== resizeState.pointerId) return;
        if (event.preventDefault) event.preventDefault();
        updateResize(
          {
            x: (event.clientX - resizeState.startX) / camera.zoom,
            y: (event.clientY - resizeState.startY) / camera.zoom
          },
          // Option on Apple platforms, Alt elsewhere — one gesture, and the
          // saved snap preference is untouched (FR-62).
          { snapBypassed: !!event.altKey }
        );
      });

      function releasePointer() {
        if (
          handle.releasePointerCapture &&
          handle.hasPointerCapture &&
          resizeState &&
          handle.hasPointerCapture(resizeState.pointerId)
        ) {
          handle.releasePointerCapture(resizeState.pointerId);
        }
      }

      handle.addEventListener('pointerup', function (event) {
        if (!resizeState || resizeState.mode !== 'pointer') return;
        if (resizeState.initiator !== handle) return;
        if (event && event.pointerId != null && event.pointerId !== resizeState.pointerId) return;
        releasePointer();
        void commitResize();
      });

      handle.addEventListener('pointercancel', function () {
        if (!resizeState || resizeState.initiator !== handle) return;
        releasePointer();
        cancelResize('Resize cancelled.');
      });

      // Keyboard resizing from a focused handle (FR-70 – FR-73).
      handle.addEventListener('keydown', function (event) {
        if (!event) return;
        var key = event.key;
        if (!resizeState || resizeState.initiator !== handle) {
          if (key !== 'Enter' && key !== ' ' && key !== 'Spacebar') return;
          var started = beginResize(container, selectedId, edge, 'keyboard');
          if (!started) return;
          started.initiator = handle;
          started.step = { x: 0, y: 0 };
          if (event.preventDefault) event.preventDefault();
          return;
        }
        if (key === 'Escape') {
          if (event.preventDefault) event.preventDefault();
          if (event.stopPropagation) event.stopPropagation();
          cancelResize('Resize cancelled. The group is back at its saved size.');
          return;
        }
        if (key === 'Enter') {
          if (event.preventDefault) event.preventDefault();
          void commitResize();
          return;
        }
        var direction = ARROW_DIRECTIONS[key];
        if (!direction) return;
        if (event.preventDefault) event.preventDefault();
        // One snap step, or one world unit when snapping is bypassed; Shift
        // moves four normal steps (FR-71, FR-72).
        var bypass = !!event.altKey;
        var unit = layoutState.snapToGrid && !bypass ? SNAP_STEP : 1;
        var magnitude = unit * (event.shiftKey ? 4 : 1);
        resizeState.step = {
          x: resizeState.step.x + direction.x * magnitude,
          y: resizeState.step.y + direction.y * magnitude
        };
        updateResize(resizeState.step, { snapBypassed: bypass });
      });
    });
  }

  var ARROW_DIRECTIONS = {
    ArrowLeft: { x: -1, y: 0 },
    ArrowRight: { x: 1, y: 0 },
    ArrowUp: { x: 0, y: -1 },
    ArrowDown: { x: 0, y: 1 }
  };

  function bindDistrictDrag(container) {
    var handles = container.querySelectorAll('[data-group-drag]');
    Array.prototype.forEach.call(handles, function (handle) {
      if (!handle || typeof handle.addEventListener !== 'function') return;

      handle.addEventListener('pointerdown', function (event) {
        if (layoutState.status !== 'ready' || !dragModeEnabled) return;
        if (event.button != null && event.button !== 0) return;
        var groupId = handle.getAttribute('data-group-drag');
        var origin = committedAnchor(groupId);
        if (!groupId || !origin) return;
        var members = clusterMembers(container, groupId);
        clusterDrag = {
          groupId: groupId,
          handle: handle,
          pointerId: event.pointerId,
          startX: event.clientX,
          startY: event.clientY,
          districtEl: container.querySelector('.ws-map-district[data-group-id="' + groupId + '"]'),
          districtOrigin: origin,
          snapOrigin: clusterSnapOrigin(members, origin),
          members: members,
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
        var snapOrigin = clusterDrag.snapOrigin;
        var snappedTarget = snapPoint(
          { x: snapOrigin.x + raw.x, y: snapOrigin.y + raw.y },
          !!event.altKey
        );
        clusterDrag.delta = {
          x: snappedTarget.x - snapOrigin.x,
          y: snappedTarget.y - snapOrigin.y
        };
        previewCluster(clusterDrag, clusterDrag.delta);
        var blocked = clusterCollides(clusterDrag.groupId, clusterDrag.members, clusterDrag.delta);
        setClusterBlocked(clusterDrag, blocked);
        // The readout describes where the district itself lands, which is the
        // thing being dragged, even though the grid snap was taken from its
        // top-left member.
        setDragReadout(
          container,
          {
            x: clusterDrag.districtOrigin.x + clusterDrag.delta.x,
            y: clusterDrag.districtOrigin.y + clusterDrag.delta.y
          },
          blocked ? MOVE_BLOCKED_INSTRUCTION : undefined
        );
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
    if (banner) banner.hidden = true;
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
    // The copy has to name everything this clears, now that it clears more than
    // anchors — and everything it does not, so nobody avoids Reset layout for
    // fear of losing the colours they picked (#346 FR-186).
    var districts = Object.keys(layoutState.groups).filter(function (id) {
      var record = layoutState.groups[id];
      return record.sizingMode === 'custom' || record.collapsed;
    }).length;
    var message =
      'Reset the map layout?\n\n' +
      'Every workspace and district goes back to an automatic position and size, and any ' +
      'collapsed group is reopened. ' +
      'Nothing is deleted, renamed, regrouped or reordered — only where things sit on your map changes. ' +
      'Your group colours and themes, and your Snap to Grid setting, are kept, and you can undo ' +
      'this during this session.' +
      (count
        ? '\n\n' + count + ' saved position' + (count === 1 ? '' : 's') + ' will be cleared.'
        : '') +
      (districts
        ? (count ? ' ' : '\n\n') +
          districts +
          ' district' +
          (districts === 1 ? '' : 's') +
          ' will return to automatic sizing.'
        : '');
    if (
      typeof window !== 'undefined' &&
      typeof window.confirm === 'function' &&
      !window.confirm(message)
    ) {
      return;
    }

    // Snapshot before asking the server, so Undo restores exactly what was
    // there rather than whatever survived the reset. Since #346 a layout is
    // more than anchors: the districts' custom rectangles, sizing modes, and
    // collapsed states are cleared too, so they have to be remembered too or
    // Undo would return the buildings and leave every district automatic and
    // open (FR-187).
    var snapshot = captureGeometrySnapshot();

    deleteLayout()
      .then(function () {
        undoSnapshot = snapshot;
        layoutState.positions = Object.create(null);
        // Appearance is deliberately untouched by a reset, so only the geometry
        // half of each stored record is cleared locally (FR-186).
        Object.keys(layoutState.groups).forEach(function (id) {
          var record = layoutState.groups[id];
          record.sizingMode = 'auto';
          record.frame = null;
          record.collapsed = false;
          if (isDefaultPresentation(record)) delete layoutState.groups[id];
        });
        announce(
          container,
          'Layout reset. Every workspace and district is back to an automatic position and size; ' +
            'your colours, themes, and Snap to Grid setting are unchanged. Undo reset is available.'
        );
        settleLayout();
        fitAll(lastMount ? lastMount.container : container);
      })
      .catch(function () {
        // A failed reset changes nothing, and says so (FR-112, FR-188).
        announce(container, 'The layout could not be reset. Everything is still where it was.');
      });
  }

  /** The exact geometry a reset is about to clear (#346 FR-187). */
  function captureGeometrySnapshot() {
    var positions = {};
    Object.keys(layoutState.positions).forEach(function (id) {
      positions[id] = { x: layoutState.positions[id].x, y: layoutState.positions[id].y };
    });
    var groups = {};
    Object.keys(layoutState.groups).forEach(function (id) {
      var record = layoutState.groups[id];
      // Appearance is not captured: a reset never takes it away, so an Undo has
      // nothing to put back — and carrying it would let an Undo silently revert
      // a colour chosen after the reset.
      var geometry = { sizing_mode: record.sizingMode, collapsed: record.collapsed };
      if (record.sizingMode === 'custom' && record.frame) {
        geometry.frame = {
          x: record.frame.x,
          y: record.frame.y,
          width: record.frame.width,
          height: record.frame.height
        };
      }
      groups[id] = geometry;
    });
    return { positions: positions, groups: groups };
  }

  function undoReset(container) {
    if (!undoSnapshot) return;
    var snapshot = undoSnapshot;
    patchLayout([
      { op: 'restore_geometry', positions: snapshot.positions, groups: snapshot.groups }
    ])
      .then(function () {
        undoSnapshot = null;
        Object.keys(snapshot.positions).forEach(function (id) {
          layoutState.positions[id] = snapshot.positions[id];
        });
        announce(container, 'Reset undone. Every workspace and district is back the way it was.');
        settleLayout();
        // Reset framed the automatic layout on its way out, so without this the
        // restored arrangement lands wherever that camera is still looking —
        // usually at empty world. An Undo that appears to have done nothing is
        // indistinguishable from one that failed.
        fitAll(lastMount ? lastMount.container : container);
      })
      .catch(function () {
        // The post-reset state is still valid and still saved; only the undo
        // failed, so the offer stays (FR-112, FR-188).
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
  // Placement is no longer a mode. It used to be one — press Build, watch a
  // banner, click a spot — because a button has no way of knowing where you
  // want the building. The context menu does: it opens at a point, so Build
  // takes that point and goes straight to the create modal (#317).
  //
  // What survives is the part that was never about the mode: a *pending*
  // coordinate, held rather than saved, because nothing may be written for a
  // workspace that does not exist yet (FR-54). sessions.js consumes it through
  // completeBuild/cancelBuild when its modal closes.

  var buildState = { pending: null, container: null };

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

  // cancelBuild is the abandonment path: forget the site, so nothing is left
  // pointing at a workspace that will never exist (FR-54). sessions.js calls it
  // when the create modal closes without creating anything.
  function cancelBuild() {
    buildState.pending = null;
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
    if (!point) return;
    buildState.pending = { x: point.x, y: point.y };
    buildState.container = container;
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

  // Snap is still a control of its own: it is a persisted preference that
  // affects every placement — a drop, a keyboard move, and the coordinate the
  // context menu's Build hands to the create flow.
  function bindSnapControl(container) {
    var snap = container.querySelector('[data-map-snap]');
    if (snap && snap.addEventListener) {
      snap.addEventListener('click', function () {
        toggleSnap(container);
      });
    }
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
    // A focus intent selects on the host's behalf, so the host has to be told.
    // Every other selection reaches it through a click handler; this one has no
    // click, and the host cannot poll for it either — HQ status arrives async
    // and re-mounts us from setHQStatus, long after the host's own mount call
    // returned. Left unannounced, the map shows a selected HQ landmark while
    // the cockpit rail still sits on Today with no Build affordance anywhere on
    // screen (#322). Recorded here, dispatched once the DOM below exists.
    var focusNotify = '';
    if (focusHQ && site.show) {
      selectedId = HQ_SITE_ID;
      hqFocusConsumed = true;
      focusNotify = 'hq-site';
    } else if (focusHQ && findWs(workspaces, hqWorkspaceId)) {
      selectedId = hqWorkspaceId;
      hqFocusConsumed = true;
      focusNotify = 'hq-workspace';
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
    // A hierarchy/data refresh rebuilds the live region too. Preserve its last
    // truthful outcome so a required partial-failure or membership message does
    // not disappear in the same tick that redraws retained membership.
    var liveBeforeRemount = container.querySelector('[data-map-live]');
    var preservedAnnouncement = liveBeforeRemount ? liveBeforeRemount.textContent : '';
    // The re-render below destroys interaction hosts and pointer owners. Close
    // or cancel them first so listeners, capture, and previews cannot outlive
    // the DOM they referred to.
    closeContextMenu({ restoreFocus: false });
    settleDropConfirm('decline', { restoreFocus: false, skipRedraw: true });
    cancelPointerTranslations(container);
    lastMount = { container: container, state: state };

    container.innerHTML = shellHTML(
      computeStats(workspaces),
      workspaces,
      selectedId,
      viewport,
      state
    );
    bindCreate(container);
    bindCockpitEmptyActions(container);
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
    bindDragControl(container);
    bindSnapControl(container);
    bindTileDrag(container, workspaces);
    bindDistrictDrag(container);
    bindResizeHandles(container);
    bindResetLayout(container);
    if (preservedAnnouncement) {
      var liveAfterRemount = container.querySelector('[data-map-live]');
      if (liveAfterRemount) liveAfterRemount.textContent = preservedAnnouncement;
    }
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
    // Announce the focus-intent selection recorded above, now that the tiles it
    // refers to actually exist. `hqFocusConsumed` makes this fire at most once
    // per page load, so later re-mounts (filters, refreshes, Map/Tree switches)
    // stay silent instead of re-announcing to screen readers. The host's own
    // post-mount reconciliation then no-ops, because its selection already
    // matches what it was just told.
    if (focusNotify === 'hq-site') {
      if (state && typeof state.onSelectHQSite === 'function') state.onSelectHQSite(site);
    } else if (focusNotify === 'hq-workspace') {
      if (state && typeof state.onSelect === 'function') {
        state.onSelect(selectedId, findWs(workspaces, selectedId) || null);
      }
    }
    // A resize changes only how much of the world is visible — never where a
    // building is (FR-13, FR-46). It does still change where the world layer
    // has to be translated to keep the camera centred, which is all
    // watchResize re-applies.
    watchResize(container);
  }

  /** Tear down the map view (called when switching away). */
  function unmount(container) {
    if (!container) return;
    stopResizeWatch();
    closeContextMenu({ restoreFocus: false });
    settleDropConfirm('decline', { restoreFocus: false, skipRedraw: true });
    cancelPointerTranslations(container);
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
    // its shared selection state and context modal cannot drift (FR57, FR73).
    getSelectedId: function () {
      return selectedId;
    },
    // The reserved-HQ-site view derived from the current Personal HQ status.
    // The cockpit needs it to render the site's build/repair/skip choices in
    // the context modal, since cockpit mode has no map-owned overview panel.
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
    // The Map's half of the grouping flow (#346). The host still runs the one
    // shared hierarchy mutation; these only pin coordinates beforehand and
    // select/frame the resulting district afterwards.
    captureGroupingAnchors: captureGroupingAnchors,
    adoptNewGroup: adoptNewGroup,
    /**
     * A read-only snapshot of one district as currently drawn (#346 FR-151).
     *
     * The Home rail renders Map-layout controls from this rather than keeping
     * its own copy of district state, so the rail and the district can never
     * disagree about whether a group is custom-sized or collapsed.
     */
    getDistrictView: function (groupId) {
      var district = renderedDistrict(groupId);
      if (!district) return null;
      return {
        id: district.id,
        sizingMode: district.sizingMode,
        collapsed: district.collapsed,
        accent: district.accent,
        theme: district.theme,
        memberCount: district.memberCount,
        readOnly: isMapReadOnly(),
        // The catalogs travel with the snapshot so the rail renders named
        // choices without keeping its own copy of the identifiers (#346 FR-125).
        accents: DISTRICT_ACCENT_CATALOG.map(function (entry) {
          return { id: entry.id, label: entry.label };
        }),
        themes: DISTRICT_THEME_CATALOG.map(function (entry) {
          return { id: entry.id, label: entry.label, hint: entry.hint };
        })
      };
    },
    // The district action controller. The context menu and the Home rail call
    // exactly these, which is what keeps every surface on one validation,
    // persistence, announcement, and failure path (#346 FR-156).
    districtActions: {
      resize: function (groupId) {
        return startGroupResize((lastMount && lastMount.container) || null, groupId);
      },
      fitToContents: function (groupId) {
        return fitGroupToContents((lastMount && lastMount.container) || null, groupId);
      },
      setCollapsed: function (groupId, collapsed) {
        return setGroupCollapsed((lastMount && lastMount.container) || null, groupId, collapsed);
      },
      setAppearance: function (groupId, choice) {
        return setGroupAppearance((lastMount && lastMount.container) || null, groupId, choice);
      },
      resetAppearance: function (groupId) {
        return resetGroupAppearance((lastMount && lastMount.container) || null, groupId);
      },
      retryLastFailure: retryLastDistrictAction,
      hasRetry: function () {
        return !!lastDistrictFailure;
      }
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
    // district effective frames, content bounds, and world sizing, all pure so
    // they can be asserted without a browser (FR-123).
    computeWorldLayout: computeWorldLayout,
    // The one district geometry, exported so frame assertions are written
    // against the implementation's padding rather than against copied numbers
    // that silently drift from it (#346 FR-43).
    districtGeometry: {
      padX: DISTRICT_PAD_X,
      padY: DISTRICT_PAD_Y,
      memberWidth: MEMBER_W,
      memberHeight: MEMBER_H,
      minWidth: DISTRICT_MIN_W,
      minHeight: DISTRICT_MIN_H
    },
    // The curated, app-defined preset catalogs. They are identifiers with human
    // names, never CSS (FR-125), and the server validates writes against the
    // same lists.
    districtAccents: DISTRICT_ACCENT_CATALOG.map(function (entry) {
      return { id: entry.id, label: entry.label };
    }),
    districtThemes: DISTRICT_THEME_CATALOG.map(function (entry) {
      return { id: entry.id, label: entry.label, hint: entry.hint };
    }),
    // Pure district frame math (FR-123).
    effectiveDistrictFrame: effectiveDistrictFrame,
    reconcileCustomFrame: reconcileCustomFrame,
    // Drop-to-group resolution (#346 FR-6a): which district a release lands in,
    // and what that means for membership.
    districtAtPoint: districtAtPoint,
    dropMembershipIntent: dropMembershipIntent,
    // Exposed only so the cycle rule can be checked against Tree's own, which
    // this module cannot import (it is a plain deferred script, not a module).
    _dropRejectionReasonForTest: dropRejectionReason,
    // The one resize geometry and the one collision rule, shared by the pointer,
    // keyboard, context-menu, and rail paths (#346 FR-156).
    districtResize: {
      handles: RESIZE_HANDLES.slice(),
      handleLabels: Object.assign({}, RESIZE_HANDLE_LABELS),
      resizeFrame: resizeFrame,
      rectsOverlap: rectsOverlap,
      frameConflict: frameConflict
    },
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
    districtHTML: districtHTML,
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
      var groups = Object.create(null);
      Object.keys(layoutState.groups).forEach(function (id) {
        groups[id] = Object.assign({}, layoutState.groups[id]);
      });
      return {
        status: layoutState.status,
        revision: layoutState.revision,
        snapToGrid: layoutState.snapToGrid,
        readOnly: layoutState.status !== 'ready',
        positions: Object.assign(Object.create(null), layoutState.positions),
        groups: groups,
        viewport: layoutState.viewport ? Object.assign({}, layoutState.viewport) : null
      };
    },
    // Test-only seams for the reset/undo lifecycle, so the geometry snapshot
    // and the local clear can be asserted without driving window.confirm.
    _captureGeometrySnapshotForTest: captureGeometrySnapshot,
    _resetLayoutForTest: function () {
      Object.keys(layoutState.groups).forEach(function (id) {
        var record = layoutState.groups[id];
        record.sizingMode = 'auto';
        record.frame = null;
        record.collapsed = false;
        if (isDefaultPresentation(record)) delete layoutState.groups[id];
      });
      layoutState.positions = Object.create(null);
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
