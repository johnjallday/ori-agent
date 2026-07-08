/*
 * workspace-map.js — Workspace Map view ("tactical command-center")
 *
 * Opt-in third view on the /workspaces hub, beside Cards and Tree. Loaded as a
 * plain deferred script (not an ES module); exposes itself on
 * window.OriWorkspaceMap, which workspace-hub.js calls when the "map" view is
 * active.
 *
 * Phase 3 = selecting a tile populates the Overview panel (entry agent, roster,
 * tasks, tools, skills) and Open Workspace navigates to the detail page.
 */
(function () {
  'use strict';

  var CELL_W = 176;
  var CELL_H = 150;
  var PAD = 26;
  var MAX_COLS = 5;

  // Currently-selected workspace id, remembered across re-mounts (data refreshes).
  var selectedId = '';

  // curated, distinct building palettes (picked by stable id hash)
  var PALETTE = [
    { key: '#4f9bf0', floor: '#0c2238', top: '#2a5da0', l: '#16365e', r: '#0e2748' }, // blue
    { key: '#e8b54b', floor: '#332708', top: '#caa23f', l: '#5e4a16', r: '#48380e' }, // gold
    { key: '#e2864d', floor: '#3a1f08', top: '#c66a34', l: '#6e3a18', r: '#522c12' }, // orange
    { key: '#7b6cf0', floor: '#241c44', top: '#6457c4', l: '#3a306b', r: '#2b2452' }, // purple
    { key: '#46d39a', floor: '#0c3326', top: '#2f9d72', l: '#15503c', r: '#0f3d2e' }, // green
    { key: '#4ec5e6', floor: '#0b2a33', top: '#2f8ba6', l: '#134350', r: '#0d3540' }  // teal
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
    return String((ws && ws.kind) || '').trim().toLowerCase() === 'group';
  }

  function paletteFor(id) {
    return PALETTE[hashKey(id) % PALETTE.length];
  }

  function opsModeLabel(mode) {
    switch (String(mode || '').trim().toLowerCase()) {
      case 'guided': return 'Guided';
      case 'direct': return 'Direct';
      case 'plan_then_execute': return 'Autonomous';
      case '': return '';
      default: return String(mode).charAt(0).toUpperCase() + String(mode).slice(1);
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
    var parts = String(name || '').trim().split(/\s+/).filter(Boolean);
    if (!parts.length) return '?';
    if (parts.length === 1) return parts[0].slice(0, 2).toUpperCase();
    return (parts[0][0] + parts[1][0]).toUpperCase();
  }

  // Default selection = most recently updated workspace (fallback: first present).
  function pickDefaultSelection(workspaces) {
    var list = (Array.isArray(workspaces) ? workspaces : []).filter(function (w) { return w && w.id; });
    if (!list.length) return '';
    var best = list[0];
    list.forEach(function (w) {
      if (String(w.updated_at || '') > String(best.updated_at || '')) best = w;
    });
    return best.id;
  }

  /**
   * Pure, stable, no-overlap layout. Returns grid-cell coordinates so rendering
   * can convert them to pixels. Groups cluster into rectangular districts; only
   * top-level groups form districts (nesting capped — deeper members flatten).
   * @returns {{tiles: Array, districts: Array, cols: number, rows: number}}
   */
  function computeMapLayout(workspaces, options) {
    var opts = options || {};
    var maxCols = Math.max(1, opts.maxCols || MAX_COLS);
    var list = (Array.isArray(workspaces) ? workspaces : []).filter(function (w) {
      return w && w.id;
    });

    var byId = {};
    list.forEach(function (w) { byId[w.id] = w; });

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
      var ha = hashKey(a.id), hb = hashKey(b.id);
      if (ha !== hb) return ha - hb;
      return a.id < b.id ? -1 : 1;
    }
    roots.sort(byHash);
    Object.keys(membersByGroup).forEach(function (g) { membersByGroup[g].sort(byHash); });

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
    var col = 0, shelfRow = 0, shelfHeight = 0, usedCols = 0, totalRows = 0;

    islands.forEach(function (isl) {
      var w = Math.min(isl.w, maxCols);
      if (col + w > maxCols) {
        shelfRow += shelfHeight;
        col = 0;
        shelfHeight = 0;
      }
      var baseCol = col, baseRow = shelfRow;
      if (isl.kind === 'group') {
        districts.push({ id: isl.ws.id, ws: isl.ws, col: baseCol, row: baseRow, w: w, h: isl.h });
        isl.members.forEach(function (m, i) {
          tiles.push({
            id: m.id, ws: m, groupId: isl.ws.id,
            col: baseCol + (i % w), row: baseRow + Math.floor(i / w)
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

  // ---------- rendering ----------

  function computeStats(workspaces) {
    var list = Array.isArray(workspaces) ? workspaces : [];
    var agents = 0, openTasks = 0, groups = 0;
    list.forEach(function (ws) {
      agents += Number((ws && ws.agent_count) || 0);
      openTasks += Number((ws && ws.open_task_count) || 0);
      if (isGroup(ws)) groups += 1;
    });
    return { workspaces: list.length, agents: agents, openTasks: openTasks, groups: groups };
  }

  function statBox(value, label) {
    return '<div class="ws-map-stat"><div class="ws-v">' + escapeHtml(value) +
      '</div><div class="ws-l">' + escapeHtml(label) + '</div></div>';
  }

  function structSVG(pal) {
    return (
      '<svg class="ws-map-struct" width="112" height="92" viewBox="0 0 118 96" aria-hidden="true">' +
      '<polygon points="59,86 110,60 59,34 8,60" fill="' + pal.floor + '" stroke="' + pal.key + '" stroke-opacity=".5"/>' +
      '<polygon points="34,54 59,68 59,40 34,26" fill="' + pal.l + '"/>' +
      '<polygon points="84,54 59,68 59,40 84,26" fill="' + pal.r + '"/>' +
      '<polygon points="59,26 84,40 59,54 34,40" fill="' + pal.top + '"/>' +
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
    var isSel = selectedId && ws.id === selectedId;
    var selected = isSel ? ' is-selected' : '';
    var left = PAD + tile.col * CELL_W;
    var top = PAD + tile.row * CELL_H;
    var meta = agents + (agents === 1 ? ' agent' : ' agents') + ' · ' +
      openTasks + (openTasks === 1 ? ' task' : ' tasks');
    // A single click selects; opening happens on double-click, or on a
    // click/Enter when the tile is already selected — so the aria-label
    // advertises the currently-available action.
    var actionHint = isSel ? '. Selected — activate to open' : '. Activate to select, double-click to open';

    return (
      '<button type="button" class="ws-map-tile' + selected + '" ' +
      'data-ws-id="' + escapeHtml(ws.id) + '" ' +
      'aria-pressed="' + (isSel ? 'true' : 'false') + '" ' +
      'style="left:' + left + 'px;top:' + top + 'px;--i:' + (index || 0) + '" ' +
      'aria-label="' + escapeHtml(ws.name || 'Workspace') + ', ' + meta + ', ' + statusText +
      (hasKeeper ? ', entry agent ' + escapeHtml(ws.entry_agent_name) : '') + actionHint + '">' +
      '<span class="ws-map-tile-flag"><span class="ws-map-led' + (active ? ' is-working' : '') + '"></span>' +
      escapeHtml(statusText) + '</span>' +
      (hasKeeper ? '<span class="ws-map-tile-crest" title="Entry agent (locked)">★</span>' : '') +
      structSVG(pal) +
      '<span class="ws-map-tile-name">' + escapeHtml(ws.name || 'Workspace') + '</span>' +
      (mode ? '<span class="ws-map-tile-type">' + escapeHtml(mode) + '</span>' : '') +
      '<span class="ws-map-tile-meta">' + escapeHtml(meta) + '</span>' +
      '</button>'
    );
  }

  function districtHTML(d) {
    var ws = d.ws || {};
    var left = PAD + d.col * CELL_W - 12;
    var top = PAD + d.row * CELL_H - 6;
    var width = d.w * CELL_W - 8;
    var height = d.h * CELL_H - 10;
    var label = (ws.name || 'Group') + ' group';
    return (
      '<div class="ws-map-district" role="group" aria-label="' + escapeHtml(label) + '" ' +
      'data-group-id="' + escapeHtml(ws.id) + '" ' +
      'style="left:' + left + 'px;top:' + top + 'px;width:' + width + 'px;height:' + height + 'px">' +
      '<button type="button" class="ws-map-district-tag" data-ws-id="' + escapeHtml(ws.id) + '" ' +
      'aria-label="' + escapeHtml('Open ' + label) + '">▢ ' +
      escapeHtml(ws.name || 'Group') + ' · Group</button>' +
      '</div>'
    );
  }

  function padHTML(col, row) {
    var left = PAD + col * CELL_W;
    var top = PAD + row * CELL_H;
    return (
      '<button type="button" class="ws-map-pad" data-ws-map-create ' +
      'style="left:' + left + 'px;top:' + top + 'px" aria-label="Create a new workspace">' +
      '<span class="ws-map-pad-plate">＋</span><span class="ws-map-pad-label">New workspace</span>' +
      '</button>'
    );
  }

  function canvasHTML(workspaces, selectedId, maxCols) {
    var cols = Math.max(1, maxCols || MAX_COLS);
    var layout = computeMapLayout(workspaces, { maxCols: cols });
    var parts = [];
    layout.districts.forEach(function (d) { parts.push(districtHTML(d)); });
    layout.tiles.forEach(function (t, i) { parts.push(tileHTML(t, selectedId, i)); });

    // place the "new workspace" pad in the next free cell on the last shelf
    var lastRow = 0;
    layout.tiles.forEach(function (t) { if (t.row > lastRow) lastRow = t.row; });
    var padCol = 0;
    layout.tiles.forEach(function (t) { if (t.row === lastRow && t.col >= padCol) padCol = t.col + 1; });
    if (padCol >= cols) { padCol = 0; lastRow += 1; }
    parts.push(padHTML(padCol, lastRow));

    var height = PAD * 2 + (lastRow + 1) * CELL_H;
    return {
      html: '<div class="ws-map-canvas" role="group" aria-label="Workspaces map" style="height:' + height + 'px">' +
        parts.join('') + '</div>',
      empty: layout.tiles.length === 0
    };
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

  function avatarHTML(name, extraClass) {
    var pal = paletteFor(name);
    return '<span class="ws-map-av' + (extraClass ? ' ' + extraClass : '') +
      '" style="--av:' + pal.key + '" title="' + escapeHtml(name) + '">' +
      escapeHtml(initials(name)) + '</span>';
  }

  function overviewBodyHTML(ws) {
    if (!ws) {
      return '<div class="ws-map-overview-empty"><span class="ws-big">◈</span>' +
        'Select a workspace to see its agents, tasks, tools, and skills.</div>';
    }
    var pal = paletteFor(ws.id);
    var mode = opsModeLabel(ws.ops_mode);
    var entry = String(ws.entry_agent_name || '').trim();
    var agents = (Array.isArray(ws.agents) ? ws.agents : []).filter(Boolean);
    var openTasks = Number(ws.open_task_count || 0);
    var mcp = Number(ws.mcp_count || 0);
    var skills = Number(ws.skill_count || 0);
    var delLabel = isGroup(ws) ? 'Delete group' : 'Delete workspace';

    var keeper = entry
      ? '<div class="ws-map-ov-keeper">' + avatarHTML(entry, 'is-keeper') +
        '<div><div class="ws-map-ov-keeper-name">' + escapeHtml(entry) + '</div>' +
        '<div class="ws-map-ov-keeper-badge">★ Locked · can&#39;t remove</div></div></div>'
      : '<span class="ws-map-ov-none">No entry agent</span>';

    var roster = agents.length
      ? agents.map(function (a) { return avatarHTML(a); }).join('')
      : '<span class="ws-map-ov-none">No agents yet</span>';

    return (
      '<div class="ws-map-ov-hero">' +
      '<span class="ws-map-ov-ic" style="--av:' + pal.key + '">' + escapeHtml(initials(ws.name)) + '</span>' +
      '<div class="ws-map-ov-herometa"><div class="ws-map-ov-name">' + escapeHtml(ws.name || 'Workspace') + '</div>' +
      (mode ? '<div class="ws-map-ov-tag">' + escapeHtml(mode) + '</div>' : '') +
      '</div>' +
      '<button type="button" class="ws-map-ov-open" data-ws-open="' + escapeHtml(ws.id) +
      '" aria-label="Open ' + escapeHtml(ws.name || 'workspace') + '">Open ▸</button>' +
      '</div>' +
      '<div class="ws-map-ov-label">Entry agent</div>' +
      '<div class="ws-map-ov-keeperwrap">' + keeper + '</div>' +
      '<div class="ws-map-ov-label">Agents · ' + agents.length + '</div>' +
      '<div class="ws-map-ov-roster">' + roster + '</div>' +
      '<div class="ws-map-ov-row"><span class="ws-map-ov-k">Tasks</span>' +
      '<span class="ws-map-ov-v">' + openTasks + ' open</span></div>' +
      '<div class="ws-map-ov-row"><span class="ws-map-ov-k">Tools · MCP</span>' +
      '<span class="ws-map-ov-v">' + mcp + '</span></div>' +
      '<div class="ws-map-ov-row"><span class="ws-map-ov-k">Skills</span>' +
      '<span class="ws-map-ov-v">' + skills + '</span></div>' +
      '<div class="ws-map-ov-actions">' +
      '<button type="button" class="ws-map-ov-delete" data-ws-delete="' + escapeHtml(ws.id) +
      '" aria-label="' + escapeHtml(delLabel + ' ' + (ws.name || '')) + '">✕ ' + escapeHtml(delLabel) + '</button>' +
      '</div>'
    );
  }

  function shellHTML(stats, workspaces, selectedId, maxCols) {
    var canvas = (Array.isArray(workspaces) && workspaces.length > 0)
      ? canvasHTML(workspaces, selectedId, maxCols).html
      : emptyCanvasHTML();
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
      overviewBodyHTML(findWs(workspaces, selectedId)) +
      '</div>' +
      '</aside>' +
      '</div>'
    );
  }

  // Below this width the layout stacks: theatre full-width, overview in flow
  // beneath it (no overview column beside the map).
  function isNarrowViewport() {
    return typeof window.matchMedia === 'function' && window.matchMedia('(max-width: 900px)').matches;
  }

  // ---------- responsive columns ----------

  // Grid column count from the theatre's usable width, capped at MAX_COLS so
  // wide screens keep the compact command-table look. Unknown/zero width
  // (e.g. measured while hidden) falls back to the cap. The theatre clips
  // overflow, so tiles MUST wrap into rows that fit — a fixed column count
  // cuts tiles off on narrow windows with no way to scroll to them.
  function computeMaxCols(theatreWidth) {
    if (!theatreWidth || theatreWidth <= 0) return MAX_COLS;
    return Math.max(1, Math.min(MAX_COLS, Math.floor((theatreWidth - PAD * 2) / CELL_W)));
  }

  function measureMaxCols(container) {
    var width = (container && container.clientWidth) || 0;
    if (width <= 0) return MAX_COLS;
    // Beside the theatre sits the 338px overview column + 18px grid gap,
    // except in the stacked narrow layout where the theatre spans full width.
    var theatre = isNarrowViewport() ? width : width - 338 - 18;
    return computeMaxCols(theatre);
  }

  // Re-lay-out when a window resize changes how many columns fit. mount() is
  // idempotent and preserves the selection, so a plain re-mount is safe.
  var lastMount = null; // { container, state }
  var lastMaxCols = 0;
  var resizeTimer = null;
  var resizeBound = false;
  function handleResize() {
    if (resizeTimer) clearTimeout(resizeTimer);
    resizeTimer = setTimeout(function () {
      resizeTimer = null;
      if (!lastMount || !lastMount.container) return;
      if (!lastMount.container.isConnected || lastMount.container.hidden) return;
      if (measureMaxCols(lastMount.container) !== lastMaxCols) {
        mount(lastMount.container, lastMount.state);
      }
    }, 150);
  }

  // Reuse the hub's existing create flow for every create affordance.
  function bindCreate(container) {
    var els = container.querySelectorAll('[data-ws-map-create]');
    Array.prototype.forEach.call(els, function (el) {
      el.addEventListener('click', function () {
        var hubCreate = document.getElementById('launcherCreateWorkspaceBtn');
        if (hubCreate) hubCreate.click();
      });
    });
  }

  function openWorkspace(id) {
    if (id) window.location.href = '/workspaces/' + encodeURIComponent(id);
  }

  function deleteWorkspace(id) {
    if (!id) return;
    // Reuse the hub's single-item delete flow (confirm modal, group handling,
    // Trash + Undo, toasts). It reloads on success, which re-mounts the map.
    if (window.WorkspaceHub && typeof window.WorkspaceHub.deleteWorkspace === 'function') {
      window.WorkspaceHub.deleteWorkspace(id);
    }
  }

  function bindOverviewActions(container) {
    var opens = container.querySelectorAll('[data-ws-open]');
    Array.prototype.forEach.call(opens, function (el) {
      el.addEventListener('click', function () {
        openWorkspace(el.getAttribute('data-ws-open'));
      });
    });
    var deletes = container.querySelectorAll('[data-ws-delete]');
    Array.prototype.forEach.call(deletes, function (el) {
      el.addEventListener('click', function () {
        deleteWorkspace(el.getAttribute('data-ws-delete'));
      });
    });
  }

  // Update selection in place (no full re-mount) to avoid flicker.
  function applySelection(container, workspaces, id) {
    selectedId = id;
    var tiles = container.querySelectorAll('.ws-map-tile');
    Array.prototype.forEach.call(tiles, function (el) {
      var sel = el.getAttribute('data-ws-id') === id;
      el.classList.toggle('is-selected', sel);
      el.setAttribute('aria-pressed', sel ? 'true' : 'false');
    });
    var body = container.querySelector('.ws-map-overview-body');
    if (body) {
      body.innerHTML = overviewBodyHTML(findWs(workspaces, id));
      bindOverviewActions(container);
    }
  }

  function bindTiles(container, workspaces) {
    var selectables = container.querySelectorAll(
      '.ws-map-tile[data-ws-id], .ws-map-district-tag[data-ws-id]'
    );
    Array.prototype.forEach.call(selectables, function (el) {
      // Single click selects; clicking (or Enter, which fires click on a
      // button) an already-selected tile opens it. Double-click always opens.
      el.addEventListener('click', function () {
        var id = el.getAttribute('data-ws-id');
        if (id && id === selectedId) { openWorkspace(id); return; }
        applySelection(container, workspaces, id);
      });
      el.addEventListener('dblclick', function () {
        openWorkspace(el.getAttribute('data-ws-id'));
      });
    });
  }

  /**
   * Mount/refresh the map view. Idempotent — safe to call on every re-render.
   * @param {HTMLElement} container - the #launcherMap element.
   * @param {object} [state] - { workspaces: [...enriched summaries], selectedId }.
   */
  function mount(container, state) {
    if (!container) return;
    var workspaces = (state && state.workspaces) || [];
    var incoming = (state && state.selectedId) || '';
    // Keep the current selection if it still exists, else honor an incoming
    // selection, else fall back to a sensible default.
    selectedId = findWs(workspaces, selectedId) ? selectedId
      : (findWs(workspaces, incoming) ? incoming : pickDefaultSelection(workspaces));

    var maxCols = measureMaxCols(container);
    lastMount = { container: container, state: state };
    lastMaxCols = maxCols;

    container.innerHTML = shellHTML(computeStats(workspaces), workspaces, selectedId, maxCols);
    bindCreate(container);
    bindTiles(container, workspaces);
    bindOverviewActions(container);

    if (!resizeBound && typeof window.addEventListener === 'function') {
      window.addEventListener('resize', handleResize);
      resizeBound = true;
    }
  }

  /** Tear down the map view (called when switching away). */
  function unmount(container) {
    if (!container) return;
    container.innerHTML = '';
    lastMount = null;
  }

  window.OriWorkspaceMap = {
    mount: mount,
    unmount: unmount,
    computeLayout: computeMapLayout,
    computeStats: computeStats,
    computeMaxCols: computeMaxCols,
    tileHTML: tileHTML,
    overviewBodyHTML: overviewBodyHTML
  };
})();
