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

  var CELL_W = 176;
  var CELL_H = 150;
  var PAD = 26;
  var MAX_COLS = 5;
  var HQ_SITE_ID = '__personal_hq_site__';

  // Currently-selected workspace id, remembered across re-mounts (data refreshes).
  var selectedId = '';

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

  // curated, distinct building palettes (picked by stable id hash)
  var PALETTE = [
    { key: '#4f9bf0', floor: '#0c2238', top: '#2a5da0', l: '#16365e', r: '#0e2748' }, // blue
    { key: '#e8b54b', floor: '#332708', top: '#caa23f', l: '#5e4a16', r: '#48380e' }, // gold
    { key: '#e2864d', floor: '#3a1f08', top: '#c66a34', l: '#6e3a18', r: '#522c12' }, // orange
    { key: '#7b6cf0', floor: '#241c44', top: '#6457c4', l: '#3a306b', r: '#2b2452' }, // purple
    { key: '#46d39a', floor: '#0c3326', top: '#2f9d72', l: '#15503c', r: '#0f3d2e' }, // green
    { key: '#4ec5e6', floor: '#0b2a33', top: '#2f8ba6', l: '#134350', r: '#0d3540' } // teal
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

  function structSVG(pal) {
    return (
      '<svg class="ws-map-struct" width="112" height="92" viewBox="0 0 118 96" aria-hidden="true">' +
      '<polygon points="59,86 110,60 59,34 8,60" fill="' +
      pal.floor +
      '" stroke="' +
      pal.key +
      '" stroke-opacity=".5"/>' +
      '<polygon points="34,54 59,68 59,40 34,26" fill="' +
      pal.l +
      '"/>' +
      '<polygon points="84,54 59,68 59,40 84,26" fill="' +
      pal.r +
      '"/>' +
      '<polygon points="59,26 84,40 59,54 34,40" fill="' +
      pal.top +
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
    var left = PAD + tile.col * CELL_W;
    var top = PAD + tile.row * CELL_H;
    var meta =
      agents +
      (agents === 1 ? ' agent' : ' agents') +
      ' · ' +
      openTasks +
      (openTasks === 1 ? ' task' : ' tasks');
    // A single click selects; opening happens on double-click, or on a
    // click/Enter when the tile is already selected — so the aria-label
    // advertises the currently-available action.
    var actionHint = isSel
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
    var left = PAD + cell.col * CELL_W;
    var top = PAD + cell.row * CELL_H;
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

  function districtHTML(d) {
    var ws = d.ws || {};
    var left = PAD + d.col * CELL_W - 12;
    var top = PAD + d.row * CELL_H - 6;
    var width = d.w * CELL_W - 8;
    var height = d.h * CELL_H - 10;
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
      '<button type="button" class="ws-map-district-tag" data-ws-id="' +
      escapeHtml(ws.id) +
      '" ' +
      'aria-label="' +
      escapeHtml('Open ' + label) +
      '">▢ ' +
      escapeHtml(ws.name || 'Group') +
      ' · Group</button>' +
      '</div>'
    );
  }

  function padHTML(col, row) {
    var left = PAD + col * CELL_W;
    var top = PAD + row * CELL_H;
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

  function occupiedMapCells(layout) {
    var occupied = Object.create(null);
    layout.tiles.forEach(function (tile) {
      occupied[tile.col + ',' + tile.row] = true;
    });
    layout.districts.forEach(function (district) {
      for (var row = district.row; row < district.row + district.h; row++) {
        for (var col = district.col; col < district.col + district.w; col++) {
          occupied[col + ',' + row] = true;
        }
      }
    });
    return occupied;
  }

  function nextFreeMapCell(occupied, cols) {
    var maxCols = Math.max(1, cols || MAX_COLS);
    for (var row = 0; ; row++) {
      for (var col = 0; col < maxCols; col++) {
        if (!occupied[col + ',' + row]) return { col: col, row: row };
      }
    }
  }

  function canvasHTML(workspaces, selectedId, maxCols) {
    var cols = Math.max(1, maxCols || MAX_COLS);
    var layout = computeMapLayout(workspaces, { maxCols: cols });
    var parts = [];
    layout.districts.forEach(function (d) {
      parts.push(districtHTML(d));
    });
    layout.tiles.forEach(function (t, i) {
      parts.push(tileHTML(t, selectedId, i));
    });

    var occupied = occupiedMapCells(layout);
    var lastRow = 0;
    Object.keys(occupied).forEach(function (key) {
      var row = Number(key.split(',')[1]);
      if (row > lastRow) lastRow = row;
    });

    var site = hqSiteView(hqStatus);
    if (site.show) {
      var hqCell = nextFreeMapCell(occupied, cols);
      occupied[hqCell.col + ',' + hqCell.row] = true;
      if (hqCell.row > lastRow) lastRow = hqCell.row;
      parts.push(hqSiteHTML(hqCell, selectedId, layout.tiles.length, site));
    }

    // Keep the ordinary create affordance after all real and reserved sites.
    var padCell = nextFreeMapCell(occupied, cols);
    if (padCell.row > lastRow) lastRow = padCell.row;
    parts.push(padHTML(padCell.col, padCell.row));

    var height = PAD * 2 + (lastRow + 1) * CELL_H;
    return {
      html:
        '<div class="ws-map-canvas" role="group" aria-label="Workspaces map" style="height:' +
        height +
        'px">' +
        parts.join('') +
        '</div>',
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
      '<div class="ws-map-ov-row"><span class="ws-map-ov-k">Tools · MCP</span>' +
      '<span class="ws-map-ov-v">' +
      mcp +
      '</span></div>' +
      '<div class="ws-map-ov-row"><span class="ws-map-ov-k">Skills</span>' +
      '<span class="ws-map-ov-v">' +
      skills +
      '</span></div>' +
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

  function shellHTML(stats, workspaces, selectedId, maxCols, options) {
    var site = hqSiteView(hqStatus);
    var canvas =
      (Array.isArray(workspaces) && workspaces.length > 0) || site.show
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
    var body = container.querySelector('.ws-map-overview-body');
    if (body) {
      body.innerHTML = overviewBodyHTML(findWs(workspaces, id), options);
      bindOverviewActions(container, options);
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
    Array.prototype.forEach.call(selectables, function (el) {
      var isTile = el.classList.contains('ws-map-tile');
      // Single click selects; clicking (or Enter, which fires click on a
      // button) an already-selected tile opens it. Double-click always opens.
      // The corner checkbox — or a Cmd/Ctrl/Shift-click anywhere on a tile —
      // toggles the tile into the multi-select set instead (group districts
      // can't be multi-selected).
      el.addEventListener('click', function (e) {
        var id = el.getAttribute('data-ws-id');
        var onCheck = e.target && e.target.closest && e.target.closest('[data-ws-check]');
        if (isTile && (onCheck || e.metaKey || e.ctrlKey || e.shiftKey)) {
          e.preventDefault();
          toggleMulti(container, id);
          return;
        }
        if (id && id === selectedId) {
          openWorkspace(id);
          return;
        }
        applySelection(container, workspaces, id, options);
      });
      el.addEventListener('dblclick', function () {
        openWorkspace(el.getAttribute('data-ws-id'));
      });
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

    var maxCols = measureMaxCols(container);
    lastMount = { container: container, state: state };
    lastMaxCols = maxCols;

    container.innerHTML = shellHTML(
      computeStats(workspaces),
      workspaces,
      selectedId,
      maxCols,
      state
    );
    bindCreate(container);
    bindTiles(container, workspaces, state);
    bindHQSite(container, state);
    bindOverviewActions(container, state);
    bindSelBar(container);

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
    multiSelected = Object.create(null);
  }

  window.OriWorkspaceMap = {
    mount: mount,
    unmount: unmount,
    computeLayout: computeMapLayout,
    computeStats: computeStats,
    computeMaxCols: computeMaxCols,
    tileHTML: tileHTML,
    overviewBodyHTML: overviewBodyHTML,
    selBarHTML: selBarHTML,
    hqSiteView: hqSiteView,
    hqSiteHTML: hqSiteHTML,
    hqOverviewHTML: hqOverviewHTML,
    nextFreeMapCell: nextFreeMapCell,
    setHQStatus: setHQStatus,
    // Test-only seam for the real designated-HQ tile badge.
    _setHQWorkspaceIdForTest: function (id) {
      hqWorkspaceId = id;
      hqStatus = id ? { valid: true, workspace_id: id } : null;
    }
  };
})();
