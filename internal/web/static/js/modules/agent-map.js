/*
 * agent-map.js — the Agent Map, a spatial view of the agent roster.
 *
 * A third view mode beside Gallery and List: every agent is a tile the user
 * positions themselves, with saved coordinates, pan, zoom and fit-to-screen.
 *
 * This is a port of workspace-map.js's geometry, not a second design. World
 * coordinates, the camera as a look-at point plus zoom, the single zoom floor,
 * deterministic automatic placement and footprint overlap all behave the same
 * way, because a user who arranges one map should not have to learn the other.
 *
 * What is deliberately NOT ported is the district machinery — frame
 * computation, frame-conflict detection, district drag and resize, group
 * collapse, drop-membership intent. The Agent Map draws no frames: a workspace
 * belongs to exactly one group, so districts model a tree, while an agent
 * belongs to many workspaces. Non-overlapping rectangles cannot express
 * many-to-many without duplicating a tile or overlapping frames, so membership
 * is text on the tile instead (agents-page-ux FR-61 through FR-63).
 *
 * Loaded as a classic deferred script; exposes window.OriAgentMap.
 */
(function () {
  'use strict';

  // ---------- world geometry ----------
  //
  // World units are viewport-independent logical units: not CSS pixels, not
  // percentages of the container, not grid column numbers. One world unit
  // happens to equal one CSS pixel at 100% zoom, which is a convenience, not a
  // contract.

  // A tile's footprint. Wider and shorter than a workspace cottage because a
  // tile carries a name, a role line and a membership line rather than a
  // building.
  var CELL_W = 210;
  var CELL_H = 132;
  // Space between the world origin and the first tile.
  var PAD = 24;
  // Columns in the automatic-placement grid. Fixed, so the same unplaced agents
  // land on the same logical coordinates at any window size (FR-68).
  var FALLBACK_COLS = 5;

  // The zoom clamp. ONE floor, shared by framing and gestures, so fit-to-screen
  // can never leave the camera somewhere the user cannot zoom out of by hand
  // (FR-66). The workspace map settled on 10% for exactly this reason.
  var MIN_ZOOM = 0.1;
  var MAX_ZOOM = 2;
  var DEFAULT_ZOOM = 1;

  // Used only before the canvas has been measured, so the first layout is
  // deterministic in tests and during the first paint.
  var DEFAULT_VIEWPORT = { width: 1100, height: 620 };

  // Safe world bounds, matching internal/agentmap. A coordinate outside them is
  // treated as unreadable and falls back to automatic placement rather than
  // rendering somewhere impossible (FR-52).
  var MIN_COORDINATE = -1000000;
  var MAX_COORDINATE = 1000000;

  // How far a tile moves per arrow key press, and with Shift held.
  var KEY_STEP = 12;
  var KEY_STEP_LARGE = CELL_W / 2;

  var LAYOUT_URL = '/api/agent-map/layout';

  // ---------- pure geometry ----------

  function isFiniteNumber(value) {
    return typeof value === 'number' && isFinite(value);
  }

  /** A point is usable only if it is finite AND inside the documented world. */
  function safePoint(point) {
    if (!point || !isFiniteNumber(point.x) || !isFiniteNumber(point.y)) return null;
    if (point.x < MIN_COORDINATE || point.x > MAX_COORDINATE) return null;
    if (point.y < MIN_COORDINATE || point.y > MAX_COORDINATE) return null;
    return { x: point.x, y: point.y };
  }

  /**
   * The one zoom clamp. Framing, gestures, the opening view and a restored
   * saved camera all land here — a camera fit-to-screen can reach is one a
   * gesture can leave, and one the server will store (FR-66).
   */
  function clampZoom(zoom) {
    if (!isFiniteNumber(zoom)) return DEFAULT_ZOOM;
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
   * Zoom about a screen point, keeping the world point under it visually still.
   * This is what makes wheel and pinch zoom feel like the map is being pulled
   * toward the cursor rather than sliding out from under it.
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
   * Zoom about the viewport centre. Buttons and the keyboard use this: with no
   * pointer there is no "point under the cursor" to preserve, and moving the
   * centre would make the map drift every time someone pressed Zoom In.
   */
  function zoomAroundCenter(cam, factor) {
    return { centerX: cam.centerX, centerY: cam.centerY, zoom: clampZoom(cam.zoom * factor) };
  }

  /**
   * Two tiles overlap when their CELL_W x CELL_H boxes share area. Anchors are
   * top-left corners, so this is a same-size axis-aligned intersection test.
   * Exactly abutting edges are NOT an overlap — only shared area is (FR-69).
   */
  function footprintsOverlap(a, b) {
    return Math.abs(a.x - b.x) < CELL_W && Math.abs(a.y - b.y) < CELL_H;
  }

  /** The world rectangle every drawn tile falls inside. */
  function contentBounds(placements) {
    var names = Object.keys(placements);
    if (!names.length) {
      return { minX: 0, minY: 0, maxX: CELL_W, maxY: CELL_H };
    }
    var minX = Infinity;
    var minY = Infinity;
    var maxX = -Infinity;
    var maxY = -Infinity;
    names.forEach(function (name) {
      var point = placements[name];
      if (point.x < minX) minX = point.x;
      if (point.y < minY) minY = point.y;
      if (point.x + CELL_W > maxX) maxX = point.x + CELL_W;
      if (point.y + CELL_H > maxY) maxY = point.y + CELL_H;
    });
    return { minX: minX, minY: minY, maxX: maxX, maxY: maxY };
  }

  /**
   * The camera that shows everything, with a margin.
   *
   * The zoom it produces goes through the SAME clamp gestures use, which is the
   * whole point of there being one floor: a layout too wide to show at 50% can
   * be framed at 10%, and the user can still zoom back out from wherever
   * framing left them (FR-66).
   */
  function fitAllCamera(placements, viewport) {
    var bounds = contentBounds(placements);
    var width = Math.max(bounds.maxX - bounds.minX, CELL_W);
    var height = Math.max(bounds.maxY - bounds.minY, CELL_H);
    var margin = 1.12;
    var zoom = clampZoom(
      Math.min(viewport.width / (width * margin), viewport.height / (height * margin))
    );
    return {
      centerX: bounds.minX + width / 2,
      centerY: bounds.minY + height / 2,
      zoom: zoom
    };
  }

  // ---------- deterministic automatic placement ----------

  function anchorKey(point) {
    // Half-unit rounding, so two anchors that differ only by floating-point
    // noise are recognised as the same cell.
    return Math.round(point.x * 2) / 2 + ',' + Math.round(point.y * 2) / 2;
  }

  /**
   * Where automatic placement starts.
   *
   * With no saved anchors it is the (PAD, PAD) corner. Once the user has placed
   * anything, new agents appear one row below their arranged content rather
   * than back at an origin they may have panned far away from.
   */
  function fallbackOrigin(saved) {
    var names = Object.keys(saved);
    if (!names.length) return { x: PAD, y: PAD };
    var minX = Infinity;
    var maxY = -Infinity;
    names.forEach(function (name) {
      var point = saved[name];
      if (point.x < minX) minX = point.x;
      if (point.y > maxY) maxY = point.y;
    });
    return { x: minX, y: maxY + CELL_H };
  }

  /**
   * Resolve every agent's anchor: saved where there is one, automatic where
   * there is not.
   *
   * Automatic placement is deterministic — the same unplaced agents land on the
   * same logical coordinates regardless of window size, because the grid is a
   * fixed number of columns in WORLD units and never consults the viewport
   * (FR-68). It is also non-overlapping: a cell already claimed by a saved
   * anchor or an earlier automatic one is skipped, so no tile is ever placed
   * under another's hit target (FR-69).
   *
   * Agents are walked in a stable name order so the arrangement does not depend
   * on object key iteration.
   */
  function resolvePlacements(agentNames, saved) {
    var placements = Object.create(null);
    var claimed = Object.create(null);
    var usableSaved = Object.create(null);

    // Saved anchors are claimed first, so automatic placement flows around the
    // arrangement the user actually made.
    agentNames.forEach(function (name) {
      var point = safePoint(saved[name]);
      if (!point) return;
      usableSaved[name] = point;
      placements[name] = point;
      claimed[anchorKey(point)] = true;
    });

    var origin = fallbackOrigin(usableSaved);
    var cursor = 0;
    var ordered = agentNames.slice().sort(function (a, b) {
      return String(a).localeCompare(String(b));
    });
    ordered.forEach(function (name) {
      if (placements[name]) return;
      for (;;) {
        var col = cursor % FALLBACK_COLS;
        var row = Math.floor(cursor / FALLBACK_COLS);
        cursor += 1;
        var point = { x: origin.x + col * CELL_W, y: origin.y + row * CELL_H };
        var key = anchorKey(point);
        if (!claimed[key]) {
          claimed[key] = true;
          placements[name] = point;
          break;
        }
      }
    });
    return placements;
  }

  /**
   * Would `name`'s box land on another tile if dropped at `point`?
   *
   * Cheap enough to call on every pointermove, which is what lets the blocked
   * indicator be live during a drag rather than a surprise on release (FR-69).
   */
  function wouldOverlap(point, name, placements) {
    return Object.keys(placements).some(function (other) {
      if (other === name) return false;
      return footprintsOverlap(point, placements[other]);
    });
  }

  // ---------- the controller ----------

  var state = {
    container: null,
    canvas: null,
    world: null,
    agents: [],
    // Saved anchors, exactly as the server returned them.
    saved: Object.create(null),
    // Resolved anchors: saved where present, automatic elsewhere. This is what
    // is drawn and what overlap is tested against.
    placements: Object.create(null),
    camera: { centerX: 0, centerY: 0, zoom: DEFAULT_ZOOM },
    // The server-issued revision, echoed on every write so a stale tab is
    // recognised rather than silently clobbering a newer layout (FR-53).
    revision: 0,
    // 'loading' | 'ready' | 'readonly'. readonly means the layout could not be
    // loaded: tiles still render on automatic placement and pan/zoom still
    // work, but nothing can be saved.
    status: 'loading',
    snapToGrid: true,
    selected: null,
    // The pre-reset arrangement, kept so the reset can be undone (FR-72).
    undoSnapshot: null,
    drag: null,
    keyboardMove: null,
    onSelect: null,
    cameraSaveTimer: null,
    fitPending: false
  };

  function viewportSize() {
    if (!state.canvas) return { width: DEFAULT_VIEWPORT.width, height: DEFAULT_VIEWPORT.height };
    var rect = state.canvas.getBoundingClientRect();
    return {
      width: rect.width || DEFAULT_VIEWPORT.width,
      height: rect.height || DEFAULT_VIEWPORT.height
    };
  }

  function esc(value) {
    return String(value == null ? '' : value).replace(/[&<>"']/g, function (ch) {
      return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[ch];
    });
  }

  /**
   * A tile's membership line: up to two workspace names plus a +N overflow, or
   * "Library only".
   *
   * Where the agent is the entry agent of one of its workspaces, that workspace
   * is named first; otherwise the existing name-sorted order applies. Both
   * facts are already in the payload — WorkspaceRef carries EntryPoint and the
   * list is already name-sorted — so this needs no new field and no new sort
   * (FR-63).
   */
  function membershipLabel(agent) {
    var workspaces = Array.isArray(agent.workspaces) ? agent.workspaces.slice() : [];
    if (!workspaces.length) return 'Library only';
    workspaces.sort(function (a, b) {
      var aEntry = a && a.entry_point ? 0 : 1;
      var bEntry = b && b.entry_point ? 0 : 1;
      return aEntry - bEntry;
    });
    var names = workspaces
      .map(function (w) {
        return String((w && w.name) || '').trim();
      })
      .filter(Boolean);
    if (!names.length) return 'Library only';
    var shown = names.slice(0, 2);
    if (names.length > 2) shown.push('+' + (names.length - 2));
    return shown.join(' · ');
  }

  /**
   * A tile carries the same four facts as a gallery card: who, role, where,
   * ready? — with membership as text rather than as a frame (FR-60/FR-63).
   */
  function tileHTML(agent, point) {
    var accent = agent.accent ? ' style="--tile-accent:' + esc(agent.accent) + '"' : '';
    var level = agent.builtIn || agent.level == null ? '' : ' · Lv ' + agent.level;
    return (
      '<li class="agent-tile" data-agent-tile="' +
      esc(agent.name) +
      '" role="listitem"' +
      accent +
      ' data-x="' +
      point.x +
      '" data-y="' +
      point.y +
      '">' +
      '<button type="button" class="agent-tile__body" data-tile-open="' +
      esc(agent.name) +
      '" aria-label="' +
      esc(agent.spokenLabel || agent.name) +
      '">' +
      '<span class="agent-tile__flags">' +
      (agent.favorite ? '<span class="agent-tile__fav" aria-hidden="true">★</span>' : '') +
      '<span class="agent-tile__status is-' +
      esc(agent.statusKind || 'idle') +
      '"><span class="agent-tile__dot" aria-hidden="true"></span>' +
      esc(agent.healthText || 'Ready') +
      '</span></span>' +
      '<span class="agent-tile__ident">' +
      '<span class="agent-tile__name">' +
      esc(agent.name) +
      '</span>' +
      '<span class="agent-tile__role">' +
      esc(agent.roleLabel || '') +
      esc(level) +
      '</span>' +
      '</span>' +
      '<span class="agent-tile__where">' +
      esc(membershipLabel(agent)) +
      '</span>' +
      '</button>' +
      '</li>'
    );
  }

  /** Position one tile from its world anchor. */
  function placeTile(el, point) {
    el.style.transform = 'translate(' + point.x + 'px, ' + point.y + 'px)';
    el.dataset.x = String(point.x);
    el.dataset.y = String(point.y);
  }

  /** Apply the camera to the world layer. */
  function applyCamera() {
    if (!state.world) return;
    var viewport = viewportSize();
    var cam = state.camera;
    // The world layer is translated and scaled as one; tiles inside it are
    // positioned in world units and never know about the camera.
    state.world.style.transform =
      'translate(' +
      viewport.width / 2 +
      'px, ' +
      viewport.height / 2 +
      'px) scale(' +
      cam.zoom +
      ') translate(' +
      -cam.centerX +
      'px, ' +
      -cam.centerY +
      'px)';
    if (state.canvas) {
      state.canvas.style.setProperty('--map-zoom', String(cam.zoom));
    }
    var readout = state.container && state.container.querySelector('[data-map-zoom-readout]');
    if (readout) readout.textContent = Math.round(cam.zoom * 100) + '%';
  }

  function render() {
    if (!state.world) return;
    state.placements = resolvePlacements(
      state.agents.map(function (a) {
        return a.name;
      }),
      state.saved
    );
    var html = state.agents
      .map(function (agent) {
        return tileHTML(agent, state.placements[agent.name]);
      })
      .join('');
    state.world.innerHTML = html;
    state.world.querySelectorAll('[data-agent-tile]').forEach(function (el) {
      placeTile(el, state.placements[el.dataset.agentTile]);
    });
    reflectSelection();
    applyCamera();
  }

  function reflectSelection() {
    if (!state.world) return;
    state.world.querySelectorAll('[data-agent-tile]').forEach(function (el) {
      var on = el.dataset.agentTile === state.selected;
      el.classList.toggle('is-selected', on);
      var button = el.querySelector('[data-tile-open]');
      if (button) button.setAttribute('aria-current', on ? 'true' : 'false');
    });
  }

  function setStatus(status, message) {
    state.status = status;
    if (!state.container) return;
    var note = state.container.querySelector('[data-map-status]');
    if (!note) return;
    note.textContent = message || '';
    note.hidden = !message;
  }

  // ---------- persistence ----------

  // Writes are serialised through one chain.
  //
  // Every patch echoes the revision the client last saw, and the server refuses
  // a stale one. Two of OUR OWN writes in flight at once therefore carry the
  // same revision, and whichever lands second is rejected — which is exactly
  // what happened when the debounced camera save overlapped a tile drop: the
  // move was refused as though another tab had made it. The revision exists to
  // catch a genuinely concurrent editor, not to make the client fight itself,
  // so a patch waits for the one before it and reads the revision that write
  // produced.
  var writeChain = Promise.resolve();

  function patch(operations, options) {
    if (state.status === 'readonly') return Promise.resolve(null);
    writeChain = writeChain.then(function () {
      return sendPatch(operations, options);
    });
    return writeChain;
  }

  function sendPatch(operations, options) {
    var body = { operations: operations };
    if (state.revision) body.expected_revision = state.revision;
    return fetch(LAYOUT_URL, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body)
    })
      .then(function (response) {
        if (response.status === 409) {
          // Someone else moved something. Reload rather than retrying the same
          // body, which would just conflict again.
          return response.json().then(function () {
            return load().then(function () {
              setStatus(
                state.status,
                'The map changed in another tab, so it was reloaded. Your last move was not saved.'
              );
              return null;
            });
          });
        }
        if (!response.ok) throw new Error('layout patch ' + response.status);
        return response.json();
      })
      .then(function (data) {
        if (!data || !data.result || !data.result.layout) return null;
        adoptLayout(data.result.layout);
        if (!(options && options.silent)) setStatus(state.status, '');
        return data.result.layout;
      })
      .catch(function (err) {
        console.error('[agent-map] failed to save layout', err);
        setStatus(state.status, 'That move could not be saved.');
        return null;
      });
  }

  function adoptLayout(layout) {
    state.revision = layout.revision || 0;
    state.snapToGrid = layout.snap_to_grid !== false;
    var saved = Object.create(null);
    Object.keys(layout.positions || {}).forEach(function (name) {
      var point = safePoint(layout.positions[name]);
      if (point) saved[name] = point;
    });
    state.saved = saved;
  }

  function load() {
    return fetch(LAYOUT_URL)
      .then(function (response) {
        if (!response.ok) throw new Error('layout ' + response.status);
        return response.json();
      })
      .then(function (data) {
        if (!data || !data.layout) throw new Error('layout payload missing');
        adoptLayout(data.layout);
        state.status = 'ready';
        if (data.layout.viewport) {
          var v = data.layout.viewport;
          state.camera = {
            centerX: isFiniteNumber(v.center_x) ? v.center_x : 0,
            centerY: isFiniteNumber(v.center_y) ? v.center_y : 0,
            zoom: clampZoom(v.zoom)
          };
        } else {
          state.fitPending = true;
        }
        return data.layout;
      })
      .catch(function (err) {
        console.error('[agent-map] failed to load layout', err);
        // A map that cannot save is still a usable map: automatic placement,
        // read-only pan and zoom, and an honest explanation.
        state.status = 'readonly';
        state.fitPending = true;
        setStatus('readonly', 'Positions cannot be saved right now. You can still pan and zoom.');
        return null;
      });
  }

  /**
   * Save a moved tile.
   *
   * Every automatic anchor is materialized alongside it. Deterministic
   * placement is stable only while the set of saved anchors is stable, so
   * saving one formerly-automatic agent would otherwise reshuffle every
   * remaining automatic one on the next render — which reads as unrelated tiles
   * following the one being dragged.
   */
  function commitMove(name, point) {
    var positions = {};
    Object.keys(state.placements).forEach(function (agentName) {
      positions[agentName] = state.placements[agentName];
    });
    positions[name] = point;
    state.saved = positions;
    return patch([{ op: 'set_positions', positions: positions }]);
  }

  // ---------- public API ----------

  var api = {
    /**
     * Mount the map into `container` and draw `agents`.
     *
     * `agents` is the roster's own view models, already normalized — this
     * module does no data fetching of its own beyond the layout, so the Map and
     * the Gallery cannot disagree about what an agent is.
     */
    mount: function (container, agents, options) {
      var opts = options || {};
      state.container = container;
      state.agents = Array.isArray(agents) ? agents : [];
      state.onSelect = typeof opts.onSelect === 'function' ? opts.onSelect : null;
      state.selected = opts.selected || null;

      container.innerHTML =
        '<div class="agent-map" data-map-canvas>' +
        '<ul class="agent-map__world" data-map-world role="list" aria-label="Agent map"></ul>' +
        '</div>' +
        '<div class="agent-map__controls">' +
        '<button type="button" class="agent-map__btn" data-map-action="zoom-out" aria-label="Zoom out">−</button>' +
        '<span class="agent-map__zoom" data-map-zoom-readout>100%</span>' +
        '<button type="button" class="agent-map__btn" data-map-action="zoom-in" aria-label="Zoom in">+</button>' +
        '<button type="button" class="agent-map__btn" data-map-action="fit">Fit</button>' +
        '<button type="button" class="agent-map__btn" data-map-action="reset">Reset layout</button>' +
        '<button type="button" class="agent-map__btn" data-map-action="undo" hidden>Undo reset</button>' +
        '</div>' +
        '<p class="agent-map__note" data-map-status role="status" aria-live="polite" hidden></p>';

      state.canvas = container.querySelector('[data-map-canvas]');
      state.world = container.querySelector('[data-map-world]');
      bindEvents();

      return load().then(function () {
        render();
        if (state.fitPending) {
          state.fitPending = false;
          api.fit();
        }
        return state;
      });
    },

    /** Redraw with a new agent set, keeping the camera and the arrangement. */
    update: function (agents, selected) {
      state.agents = Array.isArray(agents) ? agents : [];
      if (selected !== undefined) state.selected = selected;
      render();
    },

    select: function (name) {
      state.selected = name;
      reflectSelection();
    },

    /** Frame everything, through the same clamp gestures use (FR-66). */
    fit: function () {
      state.camera = fitAllCamera(state.placements, viewportSize());
      applyCamera();
      saveCameraSoon();
    },

    zoomBy: function (factor) {
      state.camera = zoomAroundCenter(state.camera, factor);
      applyCamera();
      saveCameraSoon();
    },

    /**
     * Return every agent to automatic placement, keeping the prior arrangement
     * so it can be undone (FR-72).
     */
    reset: function () {
      state.undoSnapshot = Object.assign(Object.create(null), state.saved);
      state.saved = Object.create(null);
      render();
      reflectUndo();
      return patch([{ op: 'reset' }]).then(function () {
        render();
      });
    },

    undoReset: function () {
      if (!state.undoSnapshot) return Promise.resolve(null);
      var restore = state.undoSnapshot;
      state.undoSnapshot = null;
      state.saved = restore;
      render();
      reflectUndo();
      return patch([{ op: 'restore_positions', positions: restore }]).then(function () {
        render();
      });
    },

    destroy: function () {
      if (state.cameraSaveTimer) window.clearTimeout(state.cameraSaveTimer);
      state.cameraSaveTimer = null;
      state.container = null;
      state.canvas = null;
      state.world = null;
      state.drag = null;
      state.keyboardMove = null;
    },

    // Pure helpers, exported for unit tests and for anything that needs the
    // same geometry without mounting a map.
    _geometry: {
      clampZoom: clampZoom,
      screenToWorld: screenToWorld,
      worldToScreen: worldToScreen,
      zoomAroundPoint: zoomAroundPoint,
      zoomAroundCenter: zoomAroundCenter,
      footprintsOverlap: footprintsOverlap,
      resolvePlacements: resolvePlacements,
      wouldOverlap: wouldOverlap,
      fitAllCamera: fitAllCamera,
      contentBounds: contentBounds,
      safePoint: safePoint,
      membershipLabel: membershipLabel,
      CELL_W: CELL_W,
      CELL_H: CELL_H,
      MIN_ZOOM: MIN_ZOOM,
      MAX_ZOOM: MAX_ZOOM,
      PAD: PAD,
      FALLBACK_COLS: FALLBACK_COLS
    },
    _state: state
  };

  function reflectUndo() {
    if (!state.container) return;
    var btn = state.container.querySelector('[data-map-action="undo"]');
    if (btn) btn.hidden = !state.undoSnapshot;
  }

  // The camera is saved on a trailing timer: a pan produces a stream of
  // positions, and writing each one would spend a revision per frame.
  function saveCameraSoon() {
    if (state.status !== 'ready') return;
    if (state.cameraSaveTimer) window.clearTimeout(state.cameraSaveTimer);
    state.cameraSaveTimer = window.setTimeout(function () {
      state.cameraSaveTimer = null;
      patch(
        [
          {
            op: 'set_viewport',
            viewport: {
              center_x: state.camera.centerX,
              center_y: state.camera.centerY,
              zoom: state.camera.zoom
            }
          }
        ],
        { silent: true }
      );
    }, 600);
  }

  // ---------- interaction ----------

  function bindEvents() {
    var canvas = state.canvas;

    canvas.addEventListener('pointerdown', onPointerDown);
    canvas.addEventListener('wheel', onWheel, { passive: false });
    canvas.addEventListener('keydown', onKeyDown);
    canvas.addEventListener('click', onClick);

    state.container.addEventListener('click', function (event) {
      var action = event.target.closest('[data-map-action]');
      if (!action) return;
      switch (action.dataset.mapAction) {
        case 'zoom-in':
          api.zoomBy(1.2);
          break;
        case 'zoom-out':
          api.zoomBy(1 / 1.2);
          break;
        case 'fit':
          api.fit();
          break;
        case 'reset':
          api.reset();
          break;
        case 'undo':
          api.undoReset();
          break;
        default:
      }
    });

    // Crossing a size change re-centres nothing but does re-apply the camera,
    // because the transform is expressed relative to the viewport centre.
    window.addEventListener('resize', applyCamera);
  }

  function onClick(event) {
    var open = event.target.closest('[data-tile-open]');
    if (!open) return;
    // A drag ends with a click; ignore that one so dropping a tile does not
    // also re-select it and scroll the Inspector.
    if (state.drag && state.drag.moved) return;
    api.select(open.dataset.tileOpen);
    if (state.onSelect) state.onSelect(open.dataset.tileOpen);
  }

  function onPointerDown(event) {
    var tile = event.target.closest('[data-agent-tile]');
    var viewport = viewportSize();
    var rect = state.canvas.getBoundingClientRect();
    var screenPoint = { x: event.clientX - rect.left, y: event.clientY - rect.top };

    if (tile && state.status === 'ready') {
      var name = tile.dataset.agentTile;
      var world = screenToWorld(screenPoint, state.camera, viewport);
      var anchor = state.placements[name];
      state.drag = {
        name: name,
        el: tile,
        offsetX: world.x - anchor.x,
        offsetY: world.y - anchor.y,
        moved: false
      };
      state.canvas.setPointerCapture(event.pointerId);
      state.canvas.addEventListener('pointermove', onPointerMove);
      state.canvas.addEventListener('pointerup', onPointerUp);
      state.canvas.addEventListener('pointercancel', onPointerUp);
      return;
    }

    // Anywhere else on the canvas pans the camera.
    state.drag = {
      pan: true,
      startX: event.clientX,
      startY: event.clientY,
      camX: state.camera.centerX,
      camY: state.camera.centerY,
      moved: false
    };
    state.canvas.setPointerCapture(event.pointerId);
    state.canvas.addEventListener('pointermove', onPointerMove);
    state.canvas.addEventListener('pointerup', onPointerUp);
    state.canvas.addEventListener('pointercancel', onPointerUp);
  }

  function onPointerMove(event) {
    if (!state.drag) return;
    state.drag.moved = true;

    if (state.drag.pan) {
      var dx = (event.clientX - state.drag.startX) / state.camera.zoom;
      var dy = (event.clientY - state.drag.startY) / state.camera.zoom;
      state.camera = {
        centerX: state.drag.camX - dx,
        centerY: state.drag.camY - dy,
        zoom: state.camera.zoom
      };
      applyCamera();
      return;
    }

    var rect = state.canvas.getBoundingClientRect();
    var world = screenToWorld(
      { x: event.clientX - rect.left, y: event.clientY - rect.top },
      state.camera,
      viewportSize()
    );
    var next = {
      x: world.x - state.drag.offsetX,
      y: world.y - state.drag.offsetY
    };
    if (state.snapToGrid) {
      next.x = Math.round(next.x / 38) * 38;
      next.y = Math.round(next.y / 38) * 38;
    }
    state.drag.point = next;
    placeTile(state.drag.el, next);
    // Live blocked feedback: the same test the drop will apply, run on every
    // move, so a refused drop is never a surprise (FR-69).
    state.drag.blocked = wouldOverlap(next, state.drag.name, state.placements);
    state.drag.el.classList.toggle('is-blocked', state.drag.blocked);
  }

  function onPointerUp() {
    var drag = state.drag;
    state.canvas.removeEventListener('pointermove', onPointerMove);
    state.canvas.removeEventListener('pointerup', onPointerUp);
    state.canvas.removeEventListener('pointercancel', onPointerUp);
    if (!drag) return;

    if (drag.pan) {
      state.drag = null;
      if (drag.moved) saveCameraSoon();
      return;
    }

    drag.el.classList.remove('is-blocked');
    if (!drag.moved || !drag.point) {
      state.drag = null;
      return;
    }
    if (drag.blocked) {
      // Refused: put the tile back where it was rather than committing an
      // overlap.
      placeTile(drag.el, state.placements[drag.name]);
      setStatus(state.status, 'That spot is taken.');
      state.drag = null;
      return;
    }
    state.placements[drag.name] = drag.point;
    commitMove(drag.name, drag.point);
    state.drag = null;
  }

  function onWheel(event) {
    event.preventDefault();
    var rect = state.canvas.getBoundingClientRect();
    var factor = event.deltaY < 0 ? 1.1 : 1 / 1.1;
    state.camera = zoomAroundPoint(
      state.camera,
      viewportSize(),
      { x: event.clientX - rect.left, y: event.clientY - rect.top },
      factor
    );
    applyCamera();
    saveCameraSoon();
  }

  /**
   * Keyboard tile movement (FR-70).
   *
   * Arrow keys nudge the focused tile; Shift moves by half a cell. A move that
   * would overlap is refused with the same rule a drag uses, so the keyboard
   * and the pointer cannot disagree about where a tile may go.
   */
  function onKeyDown(event) {
    var open = event.target.closest('[data-tile-open]');
    if (!open) return;
    var name = open.dataset.tileOpen;

    var step = event.shiftKey ? KEY_STEP_LARGE : KEY_STEP;
    var delta = null;
    switch (event.key) {
      case 'ArrowLeft':
        delta = { x: -step, y: 0 };
        break;
      case 'ArrowRight':
        delta = { x: step, y: 0 };
        break;
      case 'ArrowUp':
        delta = { x: 0, y: -step };
        break;
      case 'ArrowDown':
        delta = { x: 0, y: step };
        break;
      default:
        return;
    }
    if (state.status !== 'ready') return;
    event.preventDefault();

    var current = state.placements[name];
    var next = { x: current.x + delta.x, y: current.y + delta.y };
    if (!safePoint(next) || wouldOverlap(next, name, state.placements)) {
      setStatus(state.status, 'That spot is taken.');
      return;
    }
    state.placements[name] = next;
    var el = state.world.querySelector('[data-agent-tile="' + cssEscape(name) + '"]');
    if (el) placeTile(el, next);
    commitMove(name, next);
  }

  function cssEscape(value) {
    if (window.CSS && window.CSS.escape) return window.CSS.escape(value);
    return String(value).replace(/["\\]/g, '\\$&');
  }

  window.OriAgentMap = api;
})();
