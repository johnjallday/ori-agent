/*
 * Agents roster + stage controller (game-inspired Agents page, G2–G4).
 *
 * Browse the roster, select an agent, and inspect/edit it on the stage across
 * three tabs (Overview / Prompt / Workspaces). Selection persists across
 * reloads (URL ?agent= + localStorage). The system prompt is loaded lazily —
 * fetched/built only the first time the Prompt tab is opened for an agent.
 *
 * G3 adds editing to Overview and Prompt: version-checked saves against
 * PATCH /api/agents/{name} (expected_version), inline validation, a stale-edit
 * conflict banner with reload, shared-definition confirmation, and an
 * unsaved-changes guard on tab/card switches. CLI/built-in agents (no version
 * token) stay read-only.
 *
 * G4 adds the Workspaces tab editor (reconcile membership via
 * PUT /api/agents/{name}/workspaces) plus create-agent and delete-agent
 * lifecycle from the roster/stage.
 */
(function () {
  'use strict';

  var STORAGE_KEY = 'ori.roster.selectedAgent';
  var TAB_ORDER = ['overview', 'prompt', 'workspaces'];
  var ROLES = [
    'general',
    'orchestrator',
    'researcher',
    'analyzer',
    'synthesizer',
    'validator',
    'specialist'
  ];
  var REASONING = ['', 'minimal', 'low', 'medium', 'high'];
  // Agent capability types; these mirror the model catalog's category strings so
  // the Model picker can be filtered to models that fit the selected type.
  var TYPES = ['tool-calling', 'general', 'research', 'orchestration'];

  var state = {
    agents: [],
    filtered: [],
    byName: {},
    selected: null,
    detailCache: {},
    query: '',
    sort: 'name-asc',
    focusIndex: -1,
    dirty: { overview: false, prompt: false, workspaces: false },
    allWorkspaces: null,
    creating: false,
    providers: null,
    // Metadata-driven filters. Categories combine with AND; `health` is a
    // multi-value set combining with OR (PRD FR73). None are ever persisted to
    // the checked set — only to the URL (PRD FR77/FR80).
    filters: { health: new Set(), role: '', source: '', assignment: '', tag: '', favorite: false },
    // Checked agents: the session-only bulk-selection set, kept entirely separate
    // from `selected` (the focused agent driving the stage). Never persisted to
    // URL or localStorage; a reload starts empty (PRD FR1/FR12/FR13).
    checked: new Set(),
    // Anchor index (into the current sorted+filtered roster) for Shift-click and
    // Shift+Space contiguous range selection (PRD FR10).
    rangeAnchor: -1
  };

  var els = {};

  // Active OriTagInput instance for the focused agent's Overview tab (rebuilt on
  // each render); read back in overviewEdits.
  var overviewTagsInput = null;
  // OriTagInput instance for the Create panel; read back in submitCreate.
  var createTagsInput = null;

  document.addEventListener('DOMContentLoaded', init);

  function init() {
    els = {
      list: document.getElementById('rosterList'),
      count: document.getElementById('rosterCount'),
      stats: document.getElementById('rosterStats'),
      search: document.getElementById('rosterSearch'),
      sort: document.getElementById('rosterSort'),
      empty: document.getElementById('rosterEmpty'),
      clearSearch: document.getElementById('rosterClearSearch'),
      placeholder: document.getElementById('stagePlaceholder'),
      stage: document.getElementById('stage'),
      avatar: document.getElementById('stageAvatar'),
      name: document.getElementById('stageName'),
      klass: document.getElementById('stageClass'),
      vitals: document.getElementById('stageVitals'),
      progression: document.getElementById('stageProgression'),
      overviewFacts: document.getElementById('overviewFacts'),
      overviewDesc: document.getElementById('overviewDesc'),
      promptBody: document.getElementById('promptBody'),
      workspacesBody: document.getElementById('workspacesBody'),
      stageDelete: document.getElementById('stageDelete'),
      stageFullPage: document.getElementById('stageFullPage'),
      newAgentBtn: document.getElementById('newAgentBtn'),
      createPanel: document.getElementById('createPanel'),
      createBody: document.getElementById('createBody'),
      createCancel: document.getElementById('createCancel'),
      selectAll: document.getElementById('rosterSelectAll'),
      clearSelection: document.getElementById('rosterClearSelection'),
      bulkBar: document.getElementById('bulkBar'),
      bulkCount: document.getElementById('bulkCount'),
      bulkLive: document.getElementById('bulkLive'),
      bulkAddTags: document.getElementById('bulkAddTags'),
      bulkRemoveTags: document.getElementById('bulkRemoveTags'),
      bulkSetRole: document.getElementById('bulkSetRole'),
      bulkFavorite: document.getElementById('bulkFavorite'),
      bulkUnfavorite: document.getElementById('bulkUnfavorite'),
      bulkDelete: document.getElementById('bulkDelete'),
      bulkResult: document.getElementById('bulkResult'),
      bulkResultSummary: document.getElementById('bulkResultSummary'),
      bulkResultList: document.getElementById('bulkResultList'),
      bulkResultDismiss: document.getElementById('bulkResultDismiss'),
      bulkDeleteDialog: document.getElementById('bulkDeleteDialog'),
      bulkDeleteBody: document.getElementById('bulkDeleteBody'),
      bulkDeleteCancel: document.getElementById('bulkDeleteCancel'),
      bulkDeleteConfirm: document.getElementById('bulkDeleteConfirm'),
      bulkTagsDialog: document.getElementById('bulkTagsDialog'),
      bulkTagsTitle: document.getElementById('bulkTagsTitle'),
      bulkTagsBody: document.getElementById('bulkTagsBody'),
      bulkTagsCancel: document.getElementById('bulkTagsCancel'),
      bulkTagsConfirm: document.getElementById('bulkTagsConfirm'),
      filterRole: document.getElementById('filterRole'),
      filterSource: document.getElementById('filterSource'),
      filterAssignment: document.getElementById('filterAssignment'),
      filterTag: document.getElementById('filterTag'),
      filterFavorite: document.getElementById('filterFavorite'),
      clearFilters: document.getElementById('clearFilters'),
      emptyMsg: document.getElementById('rosterEmptyMsg'),
      emptyClearFilters: document.getElementById('rosterEmptyClearFilters'),
      emptyCreate: document.getElementById('rosterEmptyCreate')
    };

    els.search.addEventListener('input', onSearch);
    els.sort.addEventListener('change', onSort);
    els.clearSearch.addEventListener('click', function () {
      els.search.value = '';
      onSearch();
      els.search.focus();
    });
    els.list.addEventListener('click', onListClick);
    els.list.addEventListener('keydown', onListKeydown);
    els.list.addEventListener('change', onListChange);
    els.newAgentBtn.addEventListener('click', openCreate);
    els.createCancel.addEventListener('click', closeCreate);
    els.stageDelete.addEventListener('click', onDeleteClick);

    els.selectAll.addEventListener('click', selectAllVisible);
    els.clearSelection.addEventListener('click', function () {
      clearSelection(true);
    });

    els.filterRole.addEventListener('change', function () {
      state.filters.role = els.filterRole.value;
      onFilterChange();
    });
    els.filterSource.addEventListener('change', function () {
      state.filters.source = els.filterSource.value;
      onFilterChange();
    });
    els.filterAssignment.addEventListener('change', function () {
      state.filters.assignment = els.filterAssignment.value;
      onFilterChange();
    });
    els.filterTag.addEventListener('change', function () {
      state.filters.tag = els.filterTag.value;
      onFilterChange();
    });
    els.filterFavorite.addEventListener('click', function () {
      state.filters.favorite = !state.filters.favorite;
      els.filterFavorite.setAttribute('aria-pressed', state.filters.favorite ? 'true' : 'false');
      els.filterFavorite.classList.toggle('is-active', state.filters.favorite);
      onFilterChange();
    });
    els.clearFilters.addEventListener('click', clearFilters);
    els.emptyClearFilters.addEventListener('click', clearFilters);
    els.emptyCreate.addEventListener('click', openCreate);
    // Status summary tiles double as health filter buttons (PRD FR72).
    els.stats.addEventListener('click', onStatTileClick);

    els.bulkDelete.addEventListener('click', openBulkDelete);
    els.bulkDeleteCancel.addEventListener('click', function () {
      closeBulkDelete();
    });
    els.bulkDeleteConfirm.addEventListener('click', runBulkDelete);
    els.bulkResultDismiss.addEventListener('click', dismissBulkResult);
    // Native <dialog> fires 'cancel' on Escape; keep our teardown consistent.
    els.bulkDeleteDialog.addEventListener('cancel', function (e) {
      e.preventDefault();
      closeBulkDelete();
    });

    els.bulkAddTags.addEventListener('click', function () {
      openBulkTags('add');
    });
    els.bulkRemoveTags.addEventListener('click', function () {
      openBulkTags('remove');
    });
    els.bulkSetRole.addEventListener('click', function () {
      openBulkTags('role');
    });
    els.bulkTagsCancel.addEventListener('click', closeBulkTags);
    els.bulkTagsConfirm.addEventListener('click', runBulkTags);
    els.bulkTagsDialog.addEventListener('cancel', function (e) {
      e.preventDefault();
      closeBulkTags();
    });
    els.bulkFavorite.addEventListener('click', function () {
      runBulkFavorite(true);
    });
    els.bulkUnfavorite.addEventListener('click', function () {
      runBulkFavorite(false);
    });

    var tabs = document.querySelectorAll('.stage__tab');
    tabs.forEach(function (tab) {
      tab.addEventListener('click', function () {
        requestTab(tab.dataset.tab, true);
      });
      tab.addEventListener('keydown', onTabKeydown);
    });

    window.addEventListener('popstate', function () {
      // Restore search/sort/filters/tab and the focused agent from the URL, but
      // never the (session-only) checked set (PRD FR14/FR80).
      applyUrlToState();
      populateFilterOptions();
      applyFilterSort();
      var name = new URLSearchParams(window.location.search).get('agent');
      if (name && state.byName[name]) selectAgent(name, { push: false });
      restoreTabFromUrl();
    });

    window.addEventListener('beforeunload', function (e) {
      if (anyDirty()) {
        e.preventDefault();
        e.returnValue = '';
      }
    });

    loadProviders();
    // Wait for the role catalog (emblem/accent-color lookup) so the first
    // paint of roster cards renders correct role chips instead of a
    // flash-of-Unspecialized while the catalog fetch is still in flight.
    if (window.RoleCatalog) {
      window.RoleCatalog.ready().then(loadAgents);
    } else {
      loadAgents();
    }
  }

  /* ---- data ---------------------------------------------------------------- */

  // Load the available LLM providers + their models so the overview Model field
  // can offer a picker instead of free text. Fire-and-forget: if it fails or is
  // slow, renderOverview falls back to a text input.
  function loadProviders() {
    fetch('/api/providers')
      .then(function (r) {
        return r.ok ? r.json() : null;
      })
      .then(function (data) {
        state.providers = data && Array.isArray(data.providers) ? data.providers : [];
        // If an agent was already selected and its stage rendered before the model
        // catalog arrived, refresh the catalog-dependent surfaces: the overview
        // (picker + notes) and the vitals (legacy badge). Guarded by !dirty so we
        // never clobber unsaved edits.
        if (state.selected && !state.dirty.overview && state.detailCache[state.selected]) {
          renderVitals(state.byName[state.selected], state.detailCache[state.selected]);
          renderOverview(state.selected, state.detailCache[state.selected]);
        }
      })
      .catch(function () {
        state.providers = [];
      });
  }

  function loadAgents() {
    fetch('/api/agents/dashboard/list?sort_by=name&order=asc')
      .then(function (r) {
        if (!r.ok) throw new Error('list ' + r.status);
        return r.json();
      })
      .then(function (data) {
        var agents = Array.isArray(data) ? data : (data && data.agents) || [];
        state.agents = agents;
        state.byName = {};
        agents.forEach(function (a) {
          state.byName[a.name] = a;
          // Compare-on-refresh stage-up detection (PRD FR19): no-ops on first
          // sight of an agent, fires a toast on genuine forward progress.
          if (window.StageUpToast && a.evolution && a.evolution.stage) {
            window.StageUpToast.check(a.name, a.evolution.stage);
          }
        });
        pruneChecked();
        applyUrlToState();
        populateFilterOptions();
        applyFilterSort();
        restoreSelection();
        restoreTabFromUrl();
      })
      .catch(function (err) {
        els.count.textContent = 'Could not load agents.';
        console.error('[roster] load failed', err);
      });
  }

  // Drop checked names that no longer exist in the roster (e.g. after a reload or
  // a bulk delete) so counts and the action bar never reference missing agents.
  function pruneChecked() {
    if (state.checked.size === 0) return;
    var stale = [];
    state.checked.forEach(function (name) {
      if (!state.byName[name]) stale.push(name);
    });
    stale.forEach(function (name) {
      state.checked.delete(name);
    });
  }

  function fetchDetail(name, force) {
    if (!force && state.detailCache[name]) return Promise.resolve(state.detailCache[name]);
    return fetch('/api/agents/' + encodeURIComponent(name) + '/detail')
      .then(function (r) {
        if (!r.ok) throw new Error('detail ' + r.status);
        return r.json();
      })
      .then(function (detail) {
        state.detailCache[name] = detail;
        return detail;
      });
  }

  /* ---- roster -------------------------------------------------------------- */

  function applyFilterSort() {
    var q = state.query.trim().toLowerCase();
    var list = state.agents.filter(function (a) {
      return matchesSearch(a, q) && matchesFilters(a);
    });

    list.sort(function (a, b) {
      switch (state.sort) {
        case 'name-desc':
          return b.name.localeCompare(a.name);
        case 'level-desc':
          return (
            ((b.evolution && b.evolution.level) || 0) - ((a.evolution && a.evolution.level) || 0) ||
            a.name.localeCompare(b.name)
          );
        case 'workspaces-desc':
          return (
            (b.workspace_count || 0) - (a.workspace_count || 0) || a.name.localeCompare(b.name)
          );
        case 'active-desc':
          return lastActive(b) - lastActive(a) || a.name.localeCompare(b.name);
        default:
          return a.name.localeCompare(b.name);
      }
    });

    state.filtered = list;
    renderRoster();
  }

  // Search matches name, role, description, and tags (PRD FR74).
  function matchesSearch(a, q) {
    if (!q) return true;
    var tags = a.metadata && Array.isArray(a.metadata.tags) ? a.metadata.tags.join(' ') : '';
    var hay = (
      a.name +
      ' ' +
      (a.role || '') +
      ' ' +
      ((a.metadata && a.metadata.description) || '') +
      ' ' +
      tags
    ).toLowerCase();
    return hay.indexOf(q) !== -1;
  }

  // Filter categories combine with AND; multiple health values combine with OR
  // (PRD FR73).
  function matchesFilters(a) {
    var f = state.filters;
    if (f.health.size > 0 && !f.health.has(agentHealth(a))) return false;
    if (f.role === UNSPECIALIZED_FILTER_VALUE) {
      var isUnspec = window.RoleCatalog ? window.RoleCatalog.isUnspecialized(a.role) : !a.role;
      if (!isUnspec) return false;
    } else if (f.role && String(a.role || '').toLowerCase() !== f.role.toLowerCase()) {
      return false;
    }
    if (f.source && agentSourceKind(a) !== f.source) return false;
    if (f.assignment === 'library' && (a.workspace_count || 0) > 0) return false;
    if (f.assignment === 'assigned' && (a.workspace_count || 0) === 0) return false;
    if (f.favorite && !(a.metadata && a.metadata.favorite)) return false;
    if (f.tag) {
      var tags = a.metadata && Array.isArray(a.metadata.tags) ? a.metadata.tags : [];
      var has = tags.some(function (t) {
        return String(t).toLowerCase() === f.tag.toLowerCase();
      });
      if (!has) return false;
    }
    return true;
  }

  // Health bucket mirroring the status-summary tiles.
  function agentHealth(a) {
    var status = String((a && a.status) || 'idle').toLowerCase();
    if (status === 'disabled') return 'disabled';
    if (status === 'error' || !String((a && a.model) || '').trim()) return 'needs';
    return 'ready';
  }

  function agentSourceKind(a) {
    return String((a && a.source) || 'user').toLowerCase() === 'cli' ? 'cli' : 'user';
  }

  function filtersActive() {
    var f = state.filters;
    return f.health.size > 0 || !!f.role || !!f.source || !!f.assignment || !!f.tag || f.favorite;
  }

  function onFilterChange() {
    els.clearFilters.hidden = !filtersActive();
    applyFilterSort();
    syncUrl(state.selected, false);
  }

  function clearFilters() {
    state.filters = {
      health: new Set(),
      role: '',
      source: '',
      assignment: '',
      tag: '',
      favorite: false
    };
    els.filterRole.value = '';
    els.filterSource.value = '';
    els.filterAssignment.value = '';
    els.filterTag.value = '';
    els.filterFavorite.setAttribute('aria-pressed', 'false');
    els.filterFavorite.classList.remove('is-active');
    onFilterChange();
  }

  function onStatTileClick(e) {
    var tile = e.target.closest('.roster-stat');
    if (!tile) return;
    var health = tile.dataset.health;
    if (!health || health === 'total') {
      state.filters.health.clear();
    } else if (state.filters.health.has(health)) {
      state.filters.health.delete(health);
    } else {
      state.filters.health.add(health);
    }
    onFilterChange();
  }

  // Role filter sentinel for "no role / general" (PRD FR20: "6 roles +
  // Unspecialized"). Distinct from "" (which means "All roles").
  var UNSPECIALIZED_FILTER_VALUE = 'unspecialized';

  // Populate the Role and Tag filter dropdowns. Role is a fixed list — the 6
  // catalog roles (in catalog order) plus Unspecialized — regardless of which
  // roles are actually present in the current roster, so the option never
  // shifts under the user (PRD FR20). Tags stay dynamically derived.
  function populateFilterOptions() {
    var tags = {};
    state.agents.forEach(function (a) {
      var t = a.metadata && Array.isArray(a.metadata.tags) ? a.metadata.tags : [];
      t.forEach(function (tag) {
        var k = String(tag).toLowerCase();
        if (k) tags[k] = tag;
      });
    });

    if (els.filterRole) {
      var roleOrder = (window.RoleCatalog && window.RoleCatalog.orderedRoles) || [];
      var html = '<option value="">All roles</option>';
      roleOrder.forEach(function (slug) {
        html += '<option value="' + esc(slug) + '">' + esc(roleLabel(slug)) + '</option>';
      });
      html +=
        '<option value="' + UNSPECIALIZED_FILTER_VALUE + '">' + esc(roleLabel('general')) + '</option>';
      els.filterRole.innerHTML = html;
      if (state.filters.role) els.filterRole.value = state.filters.role;
    }

    fillSelect(els.filterTag, 'All tags', tags, state.filters.tag, null);
    if (state.filters.tag && !tags[state.filters.tag.toLowerCase()]) {
      state.filters.tag = '';
      els.filterTag.value = '';
    }
  }

  function fillSelect(sel, allLabel, valueMap, current, labeler) {
    if (!sel) return;
    var keys = Object.keys(valueMap).sort(function (a, b) {
      return valueMap[a].localeCompare(valueMap[b]);
    });
    var html = '<option value="">' + esc(allLabel) + '</option>';
    keys.forEach(function (k) {
      var v = valueMap[k];
      var label = labeler ? labeler(v) : v;
      html += '<option value="' + esc(v) + '">' + esc(label) + '</option>';
    });
    sel.innerHTML = html;
    if (current) sel.value = current;
  }

  function renderRoster() {
    els.list.innerHTML = '';
    var total = state.agents.length;
    var shown = state.filtered.length;

    renderStatusTiles();

    if (shown === 0) {
      renderEmptyState(total);
      els.count.textContent = total === 0 ? 'No agents yet.' : '0 of ' + total + ' agents';
      updateBulkBar();
      return;
    }
    els.empty.hidden = true;
    els.count.textContent =
      shown === total
        ? total + ' agent' + (total === 1 ? '' : 's')
        : shown + ' of ' + total + ' agents';

    var frag = document.createDocumentFragment();
    state.filtered.forEach(function (agent, idx) {
      frag.appendChild(buildCard(agent, idx));
    });
    els.list.appendChild(frag);
    highlightSelected();
    updateBulkBar();
  }

  // At-a-glance status summary over ALL agents (not the filtered view). Mirrors
  // the health model the retired classic dashboard used: an agent needs attention
  // when it errored or has no model; disabled is counted on its own; everything
  // else is ready.
  function renderStatusTiles() {
    if (!els.stats) return;
    var total = state.agents.length;
    if (total === 0) {
      els.stats.hidden = true;
      els.stats.innerHTML = '';
      return;
    }
    var needs = 0,
      disabled = 0;
    state.agents.forEach(function (a) {
      var status = String((a && a.status) || 'idle').toLowerCase();
      if (status === 'disabled') {
        disabled++;
        return;
      }
      if (status === 'error' || !String((a && a.model) || '').trim()) {
        needs++;
      }
    });
    var ready = total - needs - disabled;
    els.stats.hidden = false;
    els.stats.innerHTML =
      statTile('needs', 'Needs attention', needs) +
      statTile('ready', 'Ready', ready) +
      statTile('disabled', 'Disabled', disabled) +
      statTile('total', 'Total', total);
  }

  // Each tile is a toggle filter button (PRD FR72). "Total" clears the health
  // filter; the others toggle their health bucket. Selection is reflected with
  // aria-pressed + an is-selected class (not color alone).
  function statTile(kind, label, value) {
    var zero = value === 0 ? ' roster-stat--zero' : '';
    var health = kind === 'total' ? 'total' : kind;
    var selected = kind !== 'total' && state.filters.health.has(kind);
    var sel = selected ? ' is-selected' : '';
    return (
      '<button type="button" class="roster-stat roster-stat--' +
      kind +
      zero +
      sel +
      '"' +
      ' data-health="' +
      health +
      '" aria-pressed="' +
      (selected ? 'true' : 'false') +
      '">' +
      '<span class="roster-stat__value">' +
      value +
      '</span>' +
      '<span class="roster-stat__label">' +
      esc(label) +
      '</span>' +
      '</button>'
    );
  }

  function buildCard(agent, idx) {
    var li = document.createElement('li');
    li.className = 'roster-card' + (isPermanent(agent) ? ' is-permanent' : '');
    li.dataset.name = agent.name;
    li.dataset.index = idx;

    var status = healthKind(agent);
    var statusText = titleCase(String(agent.status || 'idle'));
    var metaBits = [];
    if (agent.model) metaBits.push(agent.model);
    var wc = agent.workspace_count || 0;
    metaBits.push(wc === 0 ? 'Library' : wc + ' workspace' + (wc === 1 ? '' : 's'));
    var active = lastActiveLabel(agent);
    if (active) metaBits.push(active);

    var permanent = isPermanent(agent);
    var isChecked = state.checked.has(agent.name);
    var favorite = !!(agent.metadata && agent.metadata.favorite);
    var tags = agent.metadata && Array.isArray(agent.metadata.tags) ? agent.metadata.tags : [];

    // Concise spoken label so screen readers don't read the raw dot markup, and
    // it carries the FULL tag list even when the visible chips are truncated
    // (PRD FR59).
    var roleLbl = roleLabel(agent.role);
    var evolution = agent.evolution || {};
    var stage = evolution.stage || 'spark';
    var level = evolution.level || 0;
    var progressLabel = 'Lv ' + level + ' · ' + titleCase(stage);

    var wcLabel =
      wc === 0 ? 'library agent, unattached' : wc + ' workspace' + (wc === 1 ? '' : 's');
    var openLabel =
      agent.name +
      (permanent ? ', built-in' : '') +
      (favorite ? ', favorite' : '') +
      ', ' +
      roleLbl +
      ', ' +
      progressLabel +
      ', ' +
      statusText +
      ', ' +
      wcLabel +
      (tags.length ? ', tags: ' + tags.join(', ') : '');

    var badge = permanent
      ? '<span class="roster-card__badge" title="Built-in agent — always available and cannot be deleted">Built-in</span>'
      : '';
    var star = favorite
      ? '<span class="roster-card__fav" title="Favorite" aria-hidden="true">★</span>'
      : '';
    var tagsRow = tagsRowHTML(tags);
    var progressRow = permanent ? '' : roleProgressRowHTML(agent.role, roleLbl, progressLabel);

    // A labeled checkbox and a separate open/focus button are siblings — never
    // nested — so the checkbox is not a child of an interactive element and the
    // two actions stay independent (PRD FR2/FR3/FR4).
    li.innerHTML =
      '<label class="roster-card__checkwrap">' +
      '<span class="visually-hidden">Select ' +
      esc(agent.name) +
      '</span>' +
      '<input type="checkbox" class="roster-card__check" data-check="' +
      esc(agent.name) +
      '"' +
      (isChecked ? ' checked' : '') +
      '>' +
      '</label>' +
      '<button type="button" class="roster-card__open" data-open="' +
      esc(agent.name) +
      '" aria-label="' +
      esc(openLabel) +
      '">' +
      avatarMarkup(agent, 'roster-card__avatar') +
      '<span class="roster-card__body">' +
      '<span class="roster-card__namerow">' +
      '<span class="roster-card__name">' +
      esc(agent.name) +
      '</span>' +
      star +
      badge +
      '<span class="roster-card__statuslabel is-' +
      status +
      '">' +
      esc(statusText) +
      '</span>' +
      '</span>' +
      '<span class="roster-card__meta">' +
      esc(metaBits.join(' · ')) +
      '</span>' +
      progressRow +
      tagsRow +
      '</span>' +
      '<span class="roster-card__status is-' +
      status +
      '" title="' +
      esc(statusText) +
      '" aria-hidden="true"></span>' +
      '</button>';

    if (isChecked) li.classList.add('is-checked');
    return li;
  }

  // Role chip (emblem + name for a catalog role; a neutral italic badge with
  // no emblem for Unspecialized — PRD FR7/FR17) plus a level/stage chip.
  // Purely decorative (aria-hidden); the spoken label carries the same info
  // via openLabel.
  function roleProgressRowHTML(role, roleLbl, progressLabel) {
    var entry = window.RoleCatalog ? window.RoleCatalog.entry(role) : null;
    var roleChip;
    if (entry) {
      roleChip =
        '<span class="roster-card__role-chip" style="--role-accent: ' +
        esc(entry.accent_color) +
        ';">' +
        '<i class="bi bi-' +
        esc(entry.emblem) +
        ' roster-card__role-emblem"></i>' +
        esc(roleLbl) +
        '</span>';
    } else {
      roleChip = '<span class="roster-card__role-chip is-unspecialized">' + esc(roleLbl) + '</span>';
    }
    var levelChip = '<span class="roster-card__level-chip">' + esc(progressLabel) + '</span>';
    return '<span class="roster-card__progress" aria-hidden="true">' + roleChip + levelChip + '</span>';
  }

  // Up to two visible tag chips + a "+N" indicator when more exist. The full list
  // still reaches assistive tech via the open button's aria-label (PRD FR58/FR59).
  function tagsRowHTML(tags) {
    if (!tags || tags.length === 0) return '';
    var shown = tags
      .slice(0, 2)
      .map(function (t) {
        return '<span class="roster-card__tag">' + esc(t) + '</span>';
      })
      .join('');
    var more =
      tags.length > 2
        ? '<span class="roster-card__tag roster-card__tag--more">+' + (tags.length - 2) + '</span>'
        : '';
    return '<span class="roster-card__tags" aria-hidden="true">' + shown + more + '</span>';
  }

  // Distinct empty states (PRD FR81): no agents at all vs. an active
  // search/filter that hid everything.
  function renderEmptyState(total) {
    els.empty.hidden = false;
    var hasSearch = state.query.trim() !== '';
    var hasFilters = filtersActive();
    els.emptyClearFilters.hidden = !hasFilters;
    els.clearSearch.hidden = !hasSearch;
    els.emptyCreate.hidden = total !== 0;
    if (total === 0) {
      els.emptyMsg.textContent = 'No agents yet. Create your first agent to get started.';
    } else if (hasSearch || hasFilters) {
      els.emptyMsg.textContent = 'No agents match the current search and filters.';
    } else {
      els.emptyMsg.textContent = 'No agents to show.';
    }
  }

  function highlightSelected() {
    var cards = els.list.querySelectorAll('.roster-card');
    cards.forEach(function (card) {
      var open = card.querySelector('.roster-card__open');
      var isSel = card.dataset.name === state.selected;
      card.classList.toggle('is-focused', isSel);
      if (open) open.setAttribute('aria-current', isSel ? 'true' : 'false');
    });
  }

  /* ---- selection ----------------------------------------------------------- */

  function restoreSelection() {
    var fromUrl = new URLSearchParams(window.location.search).get('agent');
    var fromStore = safeStorageGet();
    var pick =
      (fromUrl && state.byName[fromUrl] && fromUrl) ||
      (fromStore && state.byName[fromStore] && fromStore) ||
      (state.filtered[0] && state.filtered[0].name) ||
      null;
    if (pick) selectAgent(pick, { push: false });
  }

  function selectAgent(name, opts) {
    opts = opts || {};
    if (!state.byName[name]) return;
    if (name !== state.selected && !guardUnsaved()) return;
    state.selected = name;
    state.focusIndex = state.filtered.findIndex(function (a) {
      return a.name === name;
    });
    resetDirty();
    safeStorageSet(name);
    syncUrl(name, opts.push !== false);
    highlightSelected();
    renderStage(name);
  }

  /* ---- stage --------------------------------------------------------------- */

  function renderStage(name) {
    var listItem = state.byName[name];
    state.creating = false;
    els.createPanel.hidden = true;
    els.placeholder.hidden = true;
    els.stage.hidden = false;

    setActiveTab('overview');
    els.promptBody.dataset.loadedFor = '';
    els.promptBody.innerHTML = '<p class="stage-hint">Loading system prompt…</p>';
    els.stageDelete.hidden = true;

    els.avatar.outerHTML = avatarMarkup(listItem, 'stage__avatar', 'stageAvatar');
    els.avatar = document.getElementById('stageAvatar');
    els.name.textContent = listItem.name;
    els.klass.textContent = listItem.role
      ? roleLabel(listItem.role)
      : titleCase(listItem.type || 'agent');
    // Deep-link to the full agent detail page (/agents/{name}). The server routes
    // this to the rich editor for catalog agents and to the dedicated read-only
    // pages for the built-in Claude Code / Codex CLI agents.
    els.stageFullPage.href = '/agents/' + encodeURIComponent(name);

    renderProgression(name, listItem);

    els.workspacesBody.innerHTML = '<p class="stage-hint">Loading…</p>';
    els.overviewFacts.innerHTML = '<p class="stage-hint">Loading…</p>';
    els.overviewDesc.textContent = '';
    fetchDetail(name)
      .then(function (detail) {
        if (state.selected !== name) return;
        renderVitals(listItem, detail);
        renderOverview(name, detail);
        renderWorkspaces(name, listItem, detail);
        // Deletable only for editable (non-CLI) agents; the server still guards
        // system-assistant and workspace-attached deletes.
        els.stageDelete.hidden = !isEditable(detail);
      })
      .catch(function (err) {
        if (state.selected !== name) return;
        renderVitals(listItem, null);
        els.overviewFacts.innerHTML = '<p class="stage-hint">Could not load agent details.</p>';
        renderWorkspaces(name, listItem, null);
        console.error('[roster] detail failed', err);
      });
  }

  // Stage-panel progression block (PRD FR18): role emblem + name, level,
  // stage, an XP bar toward the next level, and slot usage. Built-in/CLI
  // agents don't have a catalog role or evolution, so the block is hidden
  // for them. Renders synchronously from the already-loaded list item, then
  // fills in the XP bar and slot usage once their (fast, independent)
  // fetches resolve.
  function renderProgression(name, listItem) {
    if (!els.progression) return;
    if (isPermanent(listItem)) {
      els.progression.innerHTML = '';
      return;
    }

    var role = listItem.role || '';
    var entry = window.RoleCatalog ? window.RoleCatalog.entry(role) : null;
    var lbl = roleLabel(role);
    var evo = listItem.evolution || {};
    var stage = evo.stage || 'spark';
    var level = evo.level || 0;

    var emblemHtml = entry
      ? '<span class="stage__progression-emblem" style="--role-accent: ' +
        esc(entry.accent_color) +
        ';"><i class="bi bi-' +
        esc(entry.emblem) +
        '"></i></span>'
      : '';
    var roleStyle = entry ? ' style="--role-accent: ' + esc(entry.accent_color) + ';"' : '';

    els.progression.innerHTML =
      '<span class="stage__progression-role"' +
      roleStyle +
      '>' +
      emblemHtml +
      esc(lbl) +
      '</span>' +
      '<span class="stage__progression-level">Lv ' +
      level +
      ' · ' +
      esc(titleCase(stage)) +
      '</span>' +
      '<span class="stage__progression-xp" id="stageProgressionXp"></span>' +
      '<span class="stage__progression-slots" id="stageProgressionSlots">…</span>';

    fetch('/api/agents/' + encodeURIComponent(name) + '/evolution')
      .then(function (r) {
        if (!r.ok) throw new Error('evolution ' + r.status);
        return r.json();
      })
      .then(function (data) {
        if (state.selected !== name) return;
        var e = (data && data.evolution) || {};
        var xpPerLevel = data && data.xp_per_level;
        if (e.stage) window.StageUpToast && window.StageUpToast.check(name, e.stage);
        var xpHost = document.getElementById('stageProgressionXp');
        if (!xpHost || !xpPerLevel) return;
        var xp = Number(e.experience || 0);
        var intoLevel = xp % xpPerLevel;
        var pct = Math.max(0, Math.min(100, Math.round((intoLevel / xpPerLevel) * 100)));
        xpHost.innerHTML =
          '<span class="stage__progression-xpbar"><span class="stage__progression-xpfill" style="width: ' +
          pct +
          '%;"></span></span>' +
          '<span class="stage__progression-xplabel">' +
          intoLevel +
          ' / ' +
          xpPerLevel +
          ' XP</span>';
      })
      .catch(function (err) {
        console.error('[roster] evolution fetch failed', err);
      });

    fetch('/api/skills?agent=' + encodeURIComponent(name))
      .then(function (r) {
        if (!r.ok) throw new Error('skills ' + r.status);
        return r.json();
      })
      .then(function (data) {
        if (state.selected !== name) return;
        var slotsHost = document.getElementById('stageProgressionSlots');
        if (!slotsHost) return;
        var loadout = data && data.loadout;
        if (!loadout) {
          slotsHost.textContent = '';
          return;
        }
        slotsHost.textContent = loadout.expert_mode
          ? loadout.slots_used + ' active · expert'
          : loadout.slots_used + '/' + loadout.slot_cap + ' slots';
      })
      .catch(function (err) {
        console.error('[roster] loadout fetch failed', err);
      });
  }

  function renderVitals(listItem, detail) {
    var vitals = [];
    vitals.push(vital('Status', titleCase(String(listItem.status || 'idle'))));
    if (detail && detail.model) vitals.push(vital('Model', detail.model));
    if (detail && detail.provider) vitals.push(vital('Provider', titleCase(detail.provider)));
    var msgs = listItem.statistics && listItem.statistics.message_count;
    if (msgs) vitals.push(vital('Messages', String(msgs)));
    var cost = listItem.statistics && listItem.statistics.total_cost;
    if (cost) vitals.push(vital('Cost', '$' + Number(cost).toFixed(2)));
    var meta = detail && detail.model ? modelMeta(detail.model) : null;
    if (meta && meta.is_legacy) {
      vitals.push(
        '<span class="vital vital--warn" title="This model is past its deprecation date"><span>Model</span><b>⚠ Legacy</b></span>'
      );
    }
    els.vitals.innerHTML = vitals.join('');
  }

  // Look up a model in the loaded provider catalog; returns its ProviderModel-ish
  // entry (pricing, is_legacy, etc.) or null when the catalog isn't loaded / the
  // model isn't listed.
  function modelMeta(model) {
    var providers = state.providers || [];
    for (var i = 0; i < providers.length; i++) {
      var models = providers[i] && providers[i].models;
      if (!Array.isArray(models)) continue;
      for (var j = 0; j < models.length; j++) {
        if (models[j] && models[j].value === model) return models[j];
      }
    }
    return null;
  }

  /* ---- overview (editable) ------------------------------------------------- */

  function isEditable(detail) {
    // CLI / built-in agents come back without a version token and reject PATCH.
    return !!(detail && detail.version);
  }

  function renderOverview(name, detail) {
    els.overviewDesc.textContent = '';
    var editable = isEditable(detail);

    if (!editable) {
      els.overviewFacts.innerHTML = '<dl class="stage-facts">' + readonlyFacts(detail) + '</dl>';
      els.overviewDesc.innerHTML =
        '<p class="stage-hint">This is a built-in agent and cannot be edited here.</p>';
      return;
    }

    // Offer a model picker when the provider/model catalog is loaded; otherwise
    // fall back to free text so the field always works.
    var hasCatalog = Array.isArray(state.providers) && state.providers.length > 0;
    var agentType = detail.type || 'tool-calling';
    var modelControl = hasCatalog
      ? modelSelectInput('ov-model', detail.model || '', detail.provider || '', agentType)
      : textInput('ov-model', detail.model || '');
    // With the picker, provider is derived from the chosen model, so it is shown
    // read-only. Without a catalog (text fallback) it stays editable.
    var providerControl = hasCatalog
      ? readonlyInput('ov-provider', detail.provider || '', 'Set by the selected model')
      : textInput('ov-provider', detail.provider || '', 'openai / anthropic / ollama…');

    var md = detail.metadata || {};
    els.overviewFacts.innerHTML =
      '<form class="stage-form" id="overviewForm" novalidate>' +
      field('Role', selectInput('ov-role', ROLES, detail.role, roleLabel), 'ov-role') +
      field('Type', selectInput('ov-type', TYPES, agentType, typeLabel), 'ov-type') +
      field(
        'Model',
        modelControl + '<p class="model-note" id="ov-model-note" aria-live="polite"></p>',
        'ov-model'
      ) +
      field('Provider', providerControl, 'ov-provider') +
      field(
        'Temperature',
        numInput('ov-temperature', detail.temperature, '0', '2', '0.1'),
        'ov-temperature'
      ) +
      '<div class="field" id="ov-reasoning-field">' +
      '<label class="field__label" for="ov-reasoning">Reasoning effort</label>' +
      '<div class="field__control">' +
      selectInput('ov-reasoning', REASONING, detail.reasoning_effort || '', function (v) {
        return v ? titleCase(v) : 'Default';
      }) +
      '</div></div>' +
      field(
        'Max output tokens',
        numInput('ov-maxtokens', detail.max_output_tokens || '', '0', '', '1'),
        'ov-maxtokens'
      ) +
      field('Web search', checkInput('ov-websearch', detail.allow_web_search)) +
      field(
        'Description',
        textareaInput('ov-description', md.description || '', 3),
        'ov-description'
      ) +
      field(
        'Favorite',
        '<label class="check"><input id="ov-favorite" type="checkbox"' +
          (md.favorite ? ' checked' : '') +
          '> Favorited</label>'
      ) +
      field('Tags', '<div id="ov-tags-host"></div>', 'ov-tags-host') +
      field(
        'Avatar color',
        colorInput('ov-avatarcolor', md.avatar_color || colorFor(name)),
        'ov-avatarcolor'
      ) +
      '<div class="field"><span class="field__label">Avatar image</span><div class="field__control" id="ov-avatar-control"></div></div>' +
      '</form>' +
      readonlyMetaHTML(detail) +
      saveBar('overview');

    els.overviewDesc.innerHTML = '';
    wireOverviewTags(name, md.tags || []);
    wireAvatarControl(name, md);
    wireColorInput('ov-avatarcolor');
    wireDirty('overview', document.getElementById('overviewForm'));
    wireSaveBar('overview', function () {
      saveOverview(name);
    });

    var modelSel = document.getElementById('ov-model');
    if (modelSel && modelSel.tagName === 'SELECT') {
      // Reflect the picked model everywhere derived state depends on it: Provider
      // (owning provider), the model note (pricing / good-for / legacy warning),
      // and whether the Reasoning effort field is relevant.
      var syncFromModel = function () {
        var opt = modelSel.options[modelSel.selectedIndex];
        var provEl = document.getElementById('ov-provider');
        var prov = opt && opt.getAttribute('data-provider');
        if (prov && provEl) provEl.value = prov;
        updateModelNote(opt);
        updateReasoningVisibility(modelSel.value, detail.reasoning_effort);
      };
      modelSel.addEventListener('change', syncFromModel);

      // Changing Type re-scopes the catalog to models that fit that type, keeping
      // the current selection when it still qualifies.
      var typeSel = document.getElementById('ov-type');
      if (typeSel) {
        typeSel.addEventListener('change', function () {
          var keep = modelSel.value;
          modelSel.innerHTML = modelOptionsHTML(keep, val('ov-provider'), typeSel.value);
          syncFromModel();
        });
      }

      // Prime the derived state for the initially-loaded model.
      updateModelNote(modelSel.options[modelSel.selectedIndex]);
      updateReasoningVisibility(modelSel.value, detail.reasoning_effort);
    }
  }

  // Populate #ov-model-note with the selected option's pricing / good-for hint and
  // a legacy/deprecation warning when applicable.
  function updateModelNote(opt) {
    var note = document.getElementById('ov-model-note');
    if (!note) return;
    if (!opt) {
      note.textContent = '';
      note.className = 'model-note';
      return;
    }
    var bits = [];
    var goodFor = opt.getAttribute('data-goodfor');
    if (goodFor) bits.push(goodFor);
    var pricing = opt.getAttribute('data-pricing');
    if (pricing) bits.push(pricing);
    var legacy = opt.getAttribute('data-legacy') === '1';
    var dep = opt.getAttribute('data-deprecation');
    note.className = 'model-note' + (legacy ? ' is-legacy' : '');
    var warn = legacy ? '⚠ Legacy model' + (dep ? ' — deprecated ' + dep : '') + '. ' : '';
    note.textContent = warn + bits.join(' · ');
  }

  // Show Reasoning effort only for models that use it (OpenAI o-series / gpt-5 /
  // Codex). Always show it when the agent already has an effort set, so existing
  // configuration is never hidden from view.
  function updateReasoningVisibility(model, existingEffort) {
    var wrap = document.getElementById('ov-reasoning-field');
    if (!wrap) return;
    var relevant = supportsReasoning(model) || !!(existingEffort && String(existingEffort).trim());
    wrap.hidden = !relevant;
  }

  function supportsReasoning(model) {
    var m = String(model || '')
      .toLowerCase()
      .trim();
    if (!m) return false;
    return /^o[1345](-|$)/.test(m) || m.indexOf('gpt-5') !== -1 || m.indexOf('codex') !== -1;
  }

  function typeLabel(v) {
    switch (v) {
      case 'tool-calling':
        return 'Tool Calling';
      case 'general':
        return 'General Purpose';
      case 'research':
        return 'Research';
      case 'orchestration':
        return 'Orchestration';
      default:
        return titleCase(v || '');
    }
  }

  function readonlyFacts(detail) {
    var facts = [
      ['Role', (detail && detail.role) ? roleLabel(detail.role) : '—'],
      ['Model', (detail && detail.model) || '—'],
      ['Provider', detail && detail.provider ? titleCase(detail.provider) : '—'],
      ['Temperature', detail && detail.temperature != null ? String(detail.temperature) : '—']
    ];
    return facts
      .map(function (f) {
        return '<dt>' + esc(f[0]) + '</dt><dd>' + esc(f[1]) + '</dd>';
      })
      .join('');
  }

  function overviewEdits(detail) {
    // Diff current inputs against the loaded detail; return only changed fields
    // keyed by the PATCH request's field names.
    var out = {};
    var role = val('ov-role');
    if (role !== (detail.role || '')) out.role = role;
    var type = val('ov-type');
    if (type && type !== (detail.type || '')) out.type = type;
    var model = val('ov-model').trim();
    if (model !== (detail.model || '')) out.model = model;
    var provider = val('ov-provider').trim();
    if (provider !== (detail.provider || '')) out.llm_provider = provider;
    var temp = parseFloat(val('ov-temperature'));
    if (!isNaN(temp) && temp !== Number(detail.temperature)) out.temperature = temp;
    var reasoning = val('ov-reasoning');
    if (reasoning !== (detail.reasoning_effort || '')) out.reasoning_effort = reasoning;
    var maxRaw = val('ov-maxtokens').trim();
    var maxNum = maxRaw === '' ? 0 : parseInt(maxRaw, 10);
    if (!isNaN(maxNum) && maxNum !== Number(detail.max_output_tokens || 0))
      out.max_output_tokens = maxNum;
    var web = checked('ov-websearch');
    if (web !== !!detail.allow_web_search) out.allow_web_search = web;
    var md = detail.metadata || {};
    var desc = val('ov-description');
    if (desc !== (md.description || '')) out.description = desc;
    var fav = checked('ov-favorite');
    if (fav !== !!md.favorite) out.favorite = fav;
    var color = val('ov-avatarcolor');
    if (color && color !== (md.avatar_color || '')) out.avatar_color = color;
    // Tags: compare case-insensitively so re-saving unchanged tags is a no-op.
    if (overviewTagsInput) {
      var nextTags = overviewTagsInput.getTags();
      if (tagsDiffer(nextTags, md.tags || [])) out.tags = nextTags;
    }
    return out;
  }

  function tagsDiffer(a, b) {
    var na = (a || [])
      .map(function (t) {
        return String(t).toLowerCase().trim();
      })
      .filter(Boolean)
      .sort();
    var nb = (b || [])
      .map(function (t) {
        return String(t).toLowerCase().trim();
      })
      .filter(Boolean)
      .sort();
    if (na.length !== nb.length) return true;
    for (var i = 0; i < na.length; i++) {
      if (na[i] !== nb[i]) return true;
    }
    return false;
  }

  function wireOverviewTags(name, initial) {
    overviewTagsInput = null;
    var host = document.getElementById('ov-tags-host');
    if (!host) return;
    if (window.OriTagInput) {
      overviewTagsInput = window.OriTagInput.createTagInput({
        container: host,
        initialTags: initial,
        onChange: function () {
          markDirty('overview', true);
        }
      });
    } else {
      host.innerHTML =
        '<input id="ov-tags-text" type="text" value="' +
        esc((initial || []).join(', ')) +
        '" placeholder="tag1, tag2">';
    }
  }

  // Avatar upload/remove control, reusing the existing avatar endpoints. Uploads
  // apply immediately (multipart) and refresh the card + stage avatar.
  function wireAvatarControl(name, md) {
    var host = document.getElementById('ov-avatar-control');
    if (!host) return;
    var hasImage = !!(md && md.avatar_image);
    host.innerHTML =
      '<div class="avatar-control">' +
      '<input type="file" id="ov-avatar-file" aria-label="Upload avatar image" accept="image/png,image/jpeg,image/gif,image/webp" class="avatar-control__file">' +
      (hasImage
        ? '<button type="button" class="btn-ghost avatar-control__remove" id="ov-avatar-remove">Remove image</button>'
        : '') +
      '<span class="avatar-control__status" id="ov-avatar-status" aria-live="polite"></span>' +
      '</div>';
    var file = document.getElementById('ov-avatar-file');
    if (file)
      file.addEventListener('change', function () {
        uploadAvatar(name, file.files && file.files[0]);
      });
    var remove = document.getElementById('ov-avatar-remove');
    if (remove)
      remove.addEventListener('click', function () {
        removeAvatar(name);
      });
  }

  function uploadAvatar(name, fileObj) {
    if (!fileObj) return;
    var status = document.getElementById('ov-avatar-status');
    if (status) status.textContent = 'Uploading…';
    var form = new FormData();
    form.append('avatar', fileObj);
    fetch('/api/agents/' + encodeURIComponent(name) + '/avatar', { method: 'POST', body: form })
      .then(function (r) {
        return r
          .json()
          .catch(function () {
            return {};
          })
          .then(function (d) {
            return { status: r.status, data: d };
          });
      })
      .then(function (res) {
        if (res.status >= 200 && res.status < 300) afterAvatarChange(name, status, 'Uploaded.');
        else if (status) status.textContent = (res.data && res.data.message) || 'Upload failed.';
      })
      .catch(function () {
        if (status) status.textContent = 'Network error.';
      });
  }

  function removeAvatar(name) {
    var status = document.getElementById('ov-avatar-status');
    if (status) status.textContent = 'Removing…';
    fetch('/api/agents/' + encodeURIComponent(name) + '/avatar', { method: 'DELETE' })
      .then(function (r) {
        return r.status;
      })
      .then(function (code) {
        if (code >= 200 && code < 300) afterAvatarChange(name, status, 'Removed.');
        else if (status) status.textContent = 'Remove failed.';
      })
      .catch(function () {
        if (status) status.textContent = 'Network error.';
      });
  }

  function afterAvatarChange(name, status, msg) {
    fetchDetail(name, true).then(function (detail) {
      if (status) status.textContent = msg;
      if (state.selected !== name) return;
      // Re-render avatar + card from the fresh metadata.
      var item = state.byName[name];
      if (item && detail.metadata)
        item.metadata = Object.assign({}, item.metadata, detail.metadata);
      els.avatar.outerHTML = avatarMarkup(item, 'stage__avatar', 'stageAvatar');
      els.avatar = document.getElementById('stageAvatar');
      refreshRosterMeta(name, detail);
      wireAvatarControl(name, detail.metadata || {});
    });
  }

  // Read-only created / updated / last-active timestamps below the editable form.
  function readonlyMetaHTML(detail) {
    var s = detail.statistics || {};
    var rows = [
      ['Created', fmtDate(s.created_at)],
      ['Updated', fmtDate(s.updated_at)],
      ['Last active', fmtDate(s.last_active)]
    ].filter(function (r) {
      return r[1];
    });
    if (rows.length === 0) return '';
    return (
      '<dl class="stage-meta">' +
      rows
        .map(function (r) {
          return '<dt>' + esc(r[0]) + '</dt><dd>' + esc(r[1]) + '</dd>';
        })
        .join('') +
      '</dl>'
    );
  }

  var dateFmt =
    typeof Intl !== 'undefined' && Intl.DateTimeFormat
      ? new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' })
      : null;
  function fmtDate(v) {
    if (!v) return '';
    var t = Date.parse(v);
    if (isNaN(t) || t <= 0) return '';
    return dateFmt ? dateFmt.format(new Date(t)) : new Date(t).toLocaleString();
  }

  function saveOverview(name) {
    var detail = state.detailCache[name];
    if (!detail) return;
    var edits = overviewEdits(detail);
    if (Object.keys(edits).length === 0) {
      showStatus('overview', 'No changes to save.', 'muted');
      return;
    }
    submitPatch(name, 'overview', edits);
  }

  /* ---- prompt (lazy + editable) -------------------------------------------- */

  function renderPrompt(name) {
    if (els.promptBody.dataset.loadedFor === name) return;
    els.promptBody.dataset.loadedFor = name;
    els.promptBody.innerHTML = '<p class="stage-hint">Loading system prompt…</p>';
    fetchDetail(name)
      .then(function (detail) {
        if (state.selected !== name) return;
        if (!isEditable(detail)) {
          var prompt = (detail && detail.system_prompt) || '';
          els.promptBody.innerHTML = prompt.trim()
            ? '<pre></pre>'
            : '<p class="stage-hint">This agent has no custom system prompt.</p>';
          if (prompt.trim()) els.promptBody.querySelector('pre').textContent = prompt;
          return;
        }
        els.promptBody.innerHTML =
          '<form class="stage-form" id="promptForm" novalidate>' +
          '<textarea id="pr-prompt" class="stage-textarea" rows="16" spellcheck="false" ' +
          'placeholder="No system prompt set. Add one to steer this agent."></textarea>' +
          '</form>' +
          saveBar('prompt');
        document.getElementById('pr-prompt').value = detail.system_prompt || '';
        wireDirty('prompt', document.getElementById('promptForm'));
        wireSaveBar('prompt', function () {
          savePrompt(name);
        });
      })
      .catch(function () {
        els.promptBody.innerHTML = '<p class="stage-hint">Could not load the system prompt.</p>';
      });
  }

  function savePrompt(name) {
    var detail = state.detailCache[name];
    if (!detail) return;
    var next = val('pr-prompt');
    if (next === (detail.system_prompt || '')) {
      showStatus('prompt', 'No changes to save.', 'muted');
      return;
    }
    submitPatch(name, 'prompt', { system_prompt: next });
  }

  /* ---- shared save path ---------------------------------------------------- */

  function submitPatch(name, tab, fields, confirmShared) {
    var detail = state.detailCache[name];
    var body = Object.assign({ expected_version: detail.version }, fields);
    if (confirmShared) body.confirm_shared_edit = true;
    setSaving(tab, true);
    clearBanner(tab);

    fetch('/api/agents/' + encodeURIComponent(name), {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body)
    })
      .then(function (r) {
        return r
          .json()
          .catch(function () {
            return {};
          })
          .then(function (data) {
            return { status: r.status, data: data };
          });
      })
      .then(function (res) {
        setSaving(tab, false);
        if (res.status >= 200 && res.status < 300) return onSaved(name, tab);
        if (res.status === 409 && res.data && res.data.error === 'stale_agent_edit')
          return onStale(name, tab, res.data);
        if (
          res.status === 409 &&
          res.data &&
          res.data.error === 'shared_agent_edit_requires_confirmation'
        )
          return onSharedConfirm(name, tab, fields, res.data);
        if (res.status === 409 && res.data && res.data.error === 'entry_agent_removal_blocked')
          return showStatus(tab, res.data.message || 'Blocked.', 'error');
        // Validation / other errors surface their message inline.
        showStatus(
          tab,
          (res.data && res.data.message) || 'Save failed (' + res.status + ').',
          'error'
        );
      })
      .catch(function (err) {
        setSaving(tab, false);
        showStatus(tab, 'Network error — save not applied.', 'error');
        console.error('[roster] save failed', err);
      });
  }

  function onSaved(name, tab) {
    // Refetch to reseed the version token and reflect any server normalization,
    // then re-render the tab and the roster meta.
    fetchDetail(name, true).then(function (detail) {
      state.dirty[tab] = false;
      if (state.selected !== name) return;
      refreshRosterMeta(name, detail);
      if (tab === 'overview') {
        renderVitals(state.byName[name], detail);
        renderOverview(name, detail);
      } else if (tab === 'prompt') {
        els.promptBody.dataset.loadedFor = '';
        renderPrompt(name);
      }
      showStatus(tab, 'Saved.', 'ok');
    });
  }

  function onStale(name, tab, data) {
    showBanner(
      tab,
      'This agent was changed elsewhere since you loaded it. Reload the latest version to continue — your unsaved edits in this tab will be replaced.',
      'Reload latest',
      function () {
        fetchDetail(name, true).then(function (detail) {
          if (state.selected !== name) return;
          state.dirty[tab] = false;
          if (tab === 'overview') {
            renderVitals(state.byName[name], detail);
            renderOverview(name, detail);
          } else if (tab === 'prompt') {
            els.promptBody.dataset.loadedFor = '';
            renderPrompt(name);
          }
        });
      }
    );
    if (data && data.current_version && state.detailCache[name]) {
      // Keep the cached version in sync so a subsequent explicit reload lines up.
      state.detailCache[name]._staleVersion = data.current_version;
    }
  }

  function onSharedConfirm(name, tab, fields, data) {
    var n = (data && data.workspace_count) || 'multiple';
    var ok = window.confirm(
      '“' +
        name +
        '” is attached to ' +
        n +
        ' workspaces. This change affects all of them. Apply it?'
    );
    if (ok) submitPatch(name, tab, fields, true);
    else showStatus(tab, 'Save cancelled.', 'muted');
  }

  /* ---- workspaces (read-only in G3) ---------------------------------------- */

  function renderWorkspaces(name, listItem, detail) {
    var members = Array.isArray(listItem.workspaces) ? listItem.workspaces : [];

    // CLI / built-in agents cannot be attached — show the membership read-only.
    if (!isEditable(detail)) {
      els.workspacesBody.innerHTML =
        members.length === 0
          ? '<p class="stage-hint">Not attached to any workspace.</p>'
          : members.map(readonlyWsRow).join('');
      return;
    }

    els.workspacesBody.innerHTML = '<p class="stage-hint">Loading workspaces…</p>';
    fetchWorkspaces()
      .then(function (all) {
        if (state.selected !== name) return;
        renderWorkspacesEditor(name, members, all);
      })
      .catch(function () {
        if (state.selected !== name) return;
        // Fall back to a read-only view of current memberships.
        els.workspacesBody.innerHTML =
          (members.length === 0
            ? '<p class="stage-hint">Not attached to any workspace.</p>'
            : members.map(readonlyWsRow).join('')) +
          '<p class="stage-hint">Could not load the full workspace list to edit assignments.</p>';
      });
  }

  function readonlyWsRow(ws) {
    var nm = esc(ws.name || 'Workspace');
    var link = ws.id
      ? '<a href="/workspaces/' + encodeURIComponent(ws.id) + '">' + nm + '</a>'
      : nm;
    var pill = ws.entry_point ? '<span class="ws-entry-pill">Entry agent</span>' : '';
    return '<div class="ws-row"><span>' + link + '</span>' + pill + '</div>';
  }

  function renderWorkspacesEditor(name, members, all) {
    var memberIds = {};
    var entryIds = {};
    members.forEach(function (m) {
      memberIds[m.id] = true;
      if (m.entry_point) entryIds[m.id] = true;
    });

    var rows = all
      .map(function (ws) {
        var isMember = !!memberIds[ws.id];
        var isEntry = !!entryIds[ws.id];
        // The agent can't be unassigned from a workspace it's the entry agent of;
        // lock that checkbox and explain, matching the server guard.
        var disabled = isEntry ? ' disabled' : '';
        var pill = isEntry ? '<span class="ws-entry-pill">Entry agent</span>' : '';
        return (
          '<label class="ws-check' +
          (disabled ? ' is-locked' : '') +
          '">' +
          '<input type="checkbox" data-ws-id="' +
          esc(ws.id) +
          '"' +
          (isMember ? ' checked' : '') +
          disabled +
          '>' +
          '<span class="ws-check__name">' +
          esc(ws.name || ws.id) +
          '</span>' +
          pill +
          '</label>'
        );
      })
      .join('');

    els.workspacesBody.innerHTML =
      (all.length === 0
        ? '<p class="stage-hint">No workspaces exist yet. Create one from the Workspaces page.</p>'
        : '') +
      '<form class="ws-list" id="workspacesForm">' +
      rows +
      '</form>' +
      saveBar('workspaces');

    wireDirty('workspaces', document.getElementById('workspacesForm'));
    wireSaveBar('workspaces', function () {
      saveWorkspaces(name, members);
    });
  }

  function saveWorkspaces(name, members) {
    var checks = els.workspacesBody.querySelectorAll('input[data-ws-id]');
    var desired = [];
    checks.forEach(function (c) {
      if (c.checked) desired.push(c.getAttribute('data-ws-id'));
    });
    // Entry-agent memberships have disabled (unchecked-proof) boxes but must stay
    // in the desired set so the server doesn't try to remove them.
    members.forEach(function (m) {
      if (m.entry_point && desired.indexOf(m.id) === -1) desired.push(m.id);
    });

    setSaving('workspaces', true);
    fetch('/api/agents/' + encodeURIComponent(name) + '/workspaces', {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ workspace_ids: desired })
    })
      .then(function (r) {
        return r
          .json()
          .catch(function () {
            return {};
          })
          .then(function (data) {
            return { status: r.status, data: data };
          });
      })
      .then(function (res) {
        setSaving('workspaces', false);
        if (res.status >= 200 && res.status < 300) {
          state.dirty.workspaces = false;
          // Reflect the reconciled membership everywhere.
          var item = state.byName[name];
          if (item) {
            item.workspaces = res.data.workspaces || [];
            item.workspace_count = res.data.workspace_count || 0;
          }
          refreshRosterMeta(name, state.detailCache[name] || {});
          renderWorkspaces(name, item, state.detailCache[name]);
          showStatus('workspaces', 'Saved.', 'ok');
        } else if (
          res.status === 409 &&
          res.data &&
          res.data.error === 'entry_agent_removal_blocked'
        ) {
          showStatus('workspaces', res.data.message || 'Cannot remove the entry agent.', 'error');
        } else {
          showStatus(
            'workspaces',
            (res.data && res.data.message) || 'Save failed (' + res.status + ').',
            'error'
          );
        }
      })
      .catch(function () {
        setSaving('workspaces', false);
        showStatus('workspaces', 'Network error — not saved.', 'error');
      });
  }

  function fetchWorkspaces() {
    if (state.allWorkspaces) return Promise.resolve(state.allWorkspaces);
    return fetch('/api/workspaces')
      .then(function (r) {
        if (!r.ok) throw new Error('workspaces ' + r.status);
        return r.json();
      })
      .then(function (data) {
        var list = (data && data.workspaces) || [];
        // Only assignable (non-trashed / non-missing) workspaces.
        list = list.filter(function (w) {
          var s = String(w.status || '').toLowerCase();
          return s !== 'trashed' && s !== 'missing';
        });
        list.sort(function (a, b) {
          return String(a.name || '').localeCompare(String(b.name || ''));
        });
        state.allWorkspaces = list;
        return list;
      });
  }

  /* ---- create agent -------------------------------------------------------- */

  function openCreate() {
    if (!guardUnsaved()) return;
    state.creating = true;
    els.stage.hidden = true;
    els.placeholder.hidden = true;
    els.createPanel.hidden = false;
    els.createBody.innerHTML =
      '<form class="stage-form" id="createForm" novalidate>' +
      field('Name', textInput('cr-name', '', 'Unique agent name'), 'cr-name') +
      field('Role', selectInput('cr-role', ROLES, 'general', roleLabel), 'cr-role') +
      field('Model', textInput('cr-model', 'gpt-4o-mini'), 'cr-model') +
      field('Description', textareaInput('cr-description', '', 3), 'cr-description') +
      field(
        'Favorite',
        '<label class="check"><input id="cr-favorite" type="checkbox"> Favorited</label>'
      ) +
      field('Tags', '<div id="cr-tags-host"></div>', 'cr-tags-host') +
      field('Avatar color', colorInput('cr-avatarcolor', '#4f46e5'), 'cr-avatarcolor') +
      '</form>' +
      '<div class="save-bar" id="savebar-create">' +
      '<span class="save-status is-muted"></span>' +
      '<button type="button" class="btn-ghost" id="createCancel2">Cancel</button>' +
      '<button type="button" class="btn-primary" id="createSubmit">Create agent</button>' +
      '</div>';
    createTagsInput = null;
    var tagHost = document.getElementById('cr-tags-host');
    if (window.OriTagInput && tagHost) {
      createTagsInput = window.OriTagInput.createTagInput({
        container: tagHost,
        placeholder: 'Add tag…'
      });
    } else if (tagHost) {
      tagHost.innerHTML = '<input id="cr-tags-text" type="text" placeholder="tag1, tag2">';
    }
    wireColorInput('cr-avatarcolor');
    document.getElementById('createSubmit').addEventListener('click', submitCreate);
    document.getElementById('createCancel2').addEventListener('click', closeCreate);
    var nameInput = document.getElementById('cr-name');
    if (nameInput) nameInput.focus();
  }

  function closeCreate() {
    state.creating = false;
    els.createPanel.hidden = true;
    if (state.selected) {
      els.stage.hidden = false;
    } else {
      els.placeholder.hidden = false;
    }
  }

  function submitCreate() {
    var name = val('cr-name').trim();
    var status = document.querySelector('#savebar-create .save-status');
    if (!name) {
      status.textContent = 'Name is required.';
      status.className = 'save-status is-error';
      return;
    }
    var crTags = createTagsInput
      ? createTagsInput.getTags()
      : val('cr-tags-text')
        ? val('cr-tags-text')
            .split(',')
            .map(function (t) {
              return t.trim();
            })
            .filter(Boolean)
        : [];
    var body = {
      name: name,
      type: 'tool-calling',
      role: val('cr-role'),
      model: val('cr-model').trim(),
      description: val('cr-description'),
      tags: crTags,
      favorite: checked('cr-favorite'),
      avatar_color: val('cr-avatarcolor')
    };
    var submit = document.getElementById('createSubmit');
    submit.disabled = true;
    submit.textContent = 'Creating…';
    fetch('/api/agents', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body)
    })
      .then(function (r) {
        return r
          .json()
          .catch(function () {
            return {};
          })
          .then(function (d) {
            return { status: r.status, data: d };
          });
      })
      .then(function (res) {
        submit.disabled = false;
        submit.textContent = 'Create agent';
        if (res.status >= 200 && res.status < 300) {
          reloadThenSelect(name);
          closeCreate();
        } else {
          status.textContent =
            (res.data && res.data.message) || 'Create failed (' + res.status + ').';
          status.className = 'save-status is-error';
        }
      })
      .catch(function () {
        submit.disabled = false;
        submit.textContent = 'Create agent';
        status.textContent = 'Network error.';
        status.className = 'save-status is-error';
      });
  }

  // Reload the roster from the server, then select the named agent if present.
  function reloadThenSelect(name) {
    fetch('/api/agents/dashboard/list?sort_by=name&order=asc')
      .then(function (r) {
        return r.json();
      })
      .then(function (data) {
        var agents = Array.isArray(data) ? data : (data && data.agents) || [];
        state.agents = agents;
        state.byName = {};
        agents.forEach(function (a) {
          state.byName[a.name] = a;
        });
        state.detailCache = {};
        pruneChecked();
        populateFilterOptions();
        applyFilterSort();
        if (name && state.byName[name]) selectAgent(name, { push: true });
        else if (state.filtered[0]) selectAgent(state.filtered[0].name, { push: false });
        else {
          els.stage.hidden = true;
          els.placeholder.hidden = false;
          state.selected = null;
        }
      });
  }

  /* ---- delete agent -------------------------------------------------------- */

  function onDeleteClick() {
    var name = state.selected;
    if (!name) return;
    if (
      !window.confirm(
        'Delete “' + name + '”? This permanently removes the agent and cannot be undone.'
      )
    )
      return;
    deleteAgent(name);
  }

  function deleteAgent(name) {
    fetch('/api/agents?name=' + encodeURIComponent(name), { method: 'DELETE' })
      .then(function (r) {
        return r
          .json()
          .catch(function () {
            return {};
          })
          .then(function (d) {
            return { status: r.status, data: d };
          });
      })
      .then(function (res) {
        if (res.status >= 200 && res.status < 300) {
          resetDirty();
          safeStorageSet('');
          reloadThenSelect(null);
        } else {
          // Attached-to-workspace (409) or built-in (400): surface on the stage.
          window.alert((res.data && res.data.message) || 'Delete failed (' + res.status + ').');
        }
      })
      .catch(function () {
        window.alert('Network error — agent not deleted.');
      });
  }

  function refreshRosterMeta(name, detail) {
    var item = state.byName[name];
    if (!item) return;
    if (detail.model) item.model = detail.model;
    // Merge the full metadata snapshot (description, tags, favorite, avatar) so
    // the rebuilt card reflects every edit, not just the description.
    if (detail.metadata) item.metadata = Object.assign({}, item.metadata, detail.metadata);
    if (detail.role) item.role = detail.role;
    var card = els.list.querySelector('.roster-card[data-name="' + cssEscape(name) + '"]');
    if (card) {
      var idx = Number(card.dataset.index);
      var rebuilt = buildCard(item, isNaN(idx) ? 0 : idx);
      if (card.classList.contains('is-focused')) rebuilt.classList.add('is-focused');
      card.replaceWith(rebuilt);
    }
  }

  /* ---- tabs ---------------------------------------------------------------- */

  function requestTab(tabName, focus) {
    var current = currentTab();
    if (tabName !== current && state.dirty[current] && !guardUnsaved()) {
      return; // stay on the dirty tab
    }
    setActiveTab(tabName);
    if (tabName === 'prompt' && state.selected) renderPrompt(state.selected);
    if (focus) document.getElementById('tab-' + tabName).focus();
    syncUrl(state.selected, false);
  }

  function setActiveTab(tabName) {
    TAB_ORDER.forEach(function (t) {
      var tab = document.getElementById('tab-' + t);
      var panel = document.getElementById('panel-' + t);
      var active = t === tabName;
      tab.classList.toggle('is-active', active);
      tab.setAttribute('aria-selected', active ? 'true' : 'false');
      tab.tabIndex = active ? 0 : -1;
      panel.hidden = !active;
      panel.classList.toggle('is-active', active);
    });
  }

  function currentTab() {
    var active = document.querySelector('.stage__tab.is-active');
    return active ? active.dataset.tab : 'overview';
  }

  function onTabKeydown(e) {
    var current = e.currentTarget.dataset.tab;
    var idx = TAB_ORDER.indexOf(current);
    if (e.key === 'ArrowRight' || e.key === 'ArrowLeft') {
      e.preventDefault();
      var next =
        e.key === 'ArrowRight'
          ? (idx + 1) % TAB_ORDER.length
          : (idx - 1 + TAB_ORDER.length) % TAB_ORDER.length;
      requestTab(TAB_ORDER[next], true);
    } else if (e.key === 'Home') {
      e.preventDefault();
      requestTab(TAB_ORDER[0], true);
    } else if (e.key === 'End') {
      e.preventDefault();
      requestTab(TAB_ORDER[TAB_ORDER.length - 1], true);
    }
  }

  /* ---- dirty tracking + save bar ------------------------------------------- */

  function wireDirty(tab, form) {
    if (!form) return;
    form.addEventListener('input', function () {
      markDirty(tab, true);
    });
    form.addEventListener('change', function () {
      markDirty(tab, true);
    });
  }

  function markDirty(tab, on) {
    state.dirty[tab] = on;
    var bar = document.getElementById('savebar-' + tab);
    if (!bar) return;
    bar.classList.toggle('is-dirty', on);
    var save = bar.querySelector('[data-role="save"]');
    if (save) save.disabled = !on;
    var dot = bar.querySelector('.dirty-note');
    if (dot) dot.textContent = on ? 'Unsaved changes' : '';
  }

  function wireSaveBar(tab, onSave) {
    var bar = document.getElementById('savebar-' + tab);
    if (!bar) return;
    bar.querySelector('[data-role="save"]').addEventListener('click', onSave);
    var revert = bar.querySelector('[data-role="revert"]');
    if (revert)
      revert.addEventListener('click', function () {
        state.dirty[tab] = false;
        if (tab === 'overview') renderOverview(state.selected, state.detailCache[state.selected]);
        else if (tab === 'prompt') {
          els.promptBody.dataset.loadedFor = '';
          renderPrompt(state.selected);
        } else if (tab === 'workspaces')
          renderWorkspaces(
            state.selected,
            state.byName[state.selected],
            state.detailCache[state.selected]
          );
      });
    markDirty(tab, false);
  }

  function setSaving(tab, on) {
    var bar = document.getElementById('savebar-' + tab);
    if (!bar) return;
    var save = bar.querySelector('[data-role="save"]');
    if (save) {
      save.disabled = on || !state.dirty[tab];
      save.textContent = on ? 'Saving…' : 'Save';
    }
  }

  function showStatus(tab, msg, kind) {
    var bar = document.getElementById('savebar-' + tab);
    if (!bar) return;
    var status = bar.querySelector('.save-status');
    if (!status) return;
    status.textContent = msg;
    status.className = 'save-status is-' + (kind || 'muted');
    if (kind === 'ok' || kind === 'muted') {
      window.clearTimeout(status._t);
      status._t = window.setTimeout(function () {
        status.textContent = '';
      }, 2600);
    }
  }

  function showBanner(tab, msg, actionLabel, onAction) {
    var panel = document.getElementById('panel-' + tab);
    clearBanner(tab);
    var banner = document.createElement('div');
    banner.className = 'conflict-banner';
    banner.id = 'banner-' + tab;
    banner.setAttribute('role', 'alert');
    banner.innerHTML = '<span>' + esc(msg) + '</span>';
    var btn = document.createElement('button');
    btn.type = 'button';
    btn.className = 'conflict-banner__action';
    btn.textContent = actionLabel;
    btn.addEventListener('click', function () {
      clearBanner(tab);
      onAction();
    });
    banner.appendChild(btn);
    panel.insertBefore(banner, panel.firstChild);
  }

  function clearBanner(tab) {
    var existing = document.getElementById('banner-' + tab);
    if (existing) existing.remove();
  }

  /* ---- unsaved guard ------------------------------------------------------- */

  function anyDirty() {
    return !!(state.dirty.overview || state.dirty.prompt || state.dirty.workspaces);
  }

  function guardUnsaved() {
    if (!anyDirty()) return true;
    var ok = window.confirm('You have unsaved changes that will be lost. Continue?');
    if (ok) resetDirty();
    return ok;
  }

  function resetDirty() {
    state.dirty.overview = false;
    state.dirty.prompt = false;
    state.dirty.workspaces = false;
  }

  /* ---- roster interaction -------------------------------------------------- */

  // Clicking a card's open button focuses that agent (drives the stage) without
  // touching its checkbox. The checkbox is handled by onListChange so a plain
  // click there only changes bulk-selection state (PRD FR3/FR4).
  function onListClick(e) {
    var open = e.target.closest('.roster-card__open');
    if (open && open.dataset.open) {
      selectAgent(open.dataset.open);
      return;
    }

    // Shift-click on (or near) a checkbox extends a contiguous range from the
    // anchor. The native toggle still fires via onListChange; here we only widen
    // the range when Shift is held.
    var check = e.target.closest('.roster-card__check');
    if (check && e.shiftKey) {
      e.preventDefault();
      var card = check.closest('.roster-card');
      var idx = card ? Number(card.dataset.index) : -1;
      if (idx >= 0) {
        rangeSelectTo(idx);
      }
    }
  }

  function onListChange(e) {
    var check = e.target.closest('.roster-card__check');
    if (!check) return;
    var name = check.dataset.check;
    var card = check.closest('.roster-card');
    var idx = card ? Number(card.dataset.index) : -1;
    setChecked(name, check.checked);
    state.rangeAnchor = idx;
    if (card) card.classList.toggle('is-checked', check.checked);
    updateBulkBar();
  }

  function onListKeydown(e) {
    // Roving focus over the open buttons; Space toggles the row's checkbox
    // without moving focus (PRD FR83). Shift+Space extends a range from the
    // anchor — the documented keyboard equivalent of Shift-click (PRD FR10).
    var open = e.target.closest('.roster-card__open');
    var n = state.filtered.length;
    if (n === 0) return;

    if (e.key === 'ArrowDown' || e.key === 'ArrowUp' || e.key === 'Home' || e.key === 'End') {
      if (!open) return;
      e.preventDefault();
      var card = open.closest('.roster-card');
      var idx = card ? Number(card.dataset.index) : 0;
      var next = idx;
      if (e.key === 'ArrowDown') next = Math.min(idx + 1, n - 1);
      else if (e.key === 'ArrowUp') next = Math.max(idx - 1, 0);
      else if (e.key === 'Home') next = 0;
      else next = n - 1;
      focusRow(next);
      return;
    }

    if (e.key === ' ' || e.key === 'Spacebar') {
      if (!open) return;
      e.preventDefault();
      var c = open.closest('.roster-card');
      var i = c ? Number(c.dataset.index) : -1;
      if (i < 0) return;
      if (e.shiftKey) {
        rangeSelectTo(i);
      } else {
        var name = c.dataset.name;
        var nowChecked = !state.checked.has(name);
        setChecked(name, nowChecked);
        state.rangeAnchor = i;
        var box = c.querySelector('.roster-card__check');
        if (box) box.checked = nowChecked;
        c.classList.toggle('is-checked', nowChecked);
        updateBulkBar();
      }
    }
  }

  // Move keyboard focus to the open button of the row at filtered index i.
  function focusRow(i) {
    var agent = state.filtered[i];
    if (!agent) return;
    var card = els.list.querySelector('.roster-card[data-name="' + cssEscape(agent.name) + '"]');
    if (!card) return;
    var open = card.querySelector('.roster-card__open');
    if (open) open.focus();
    if (card.scrollIntoView) card.scrollIntoView({ block: 'nearest' });
  }

  /* ---- bulk selection ------------------------------------------------------ */

  function setChecked(name, on) {
    if (!name) return;
    if (on) state.checked.add(name);
    else state.checked.delete(name);
  }

  // Check every agent in the current filtered result set — and only those, never
  // agents hidden by the active filters (PRD FR7).
  function selectAllVisible() {
    state.filtered.forEach(function (a) {
      state.checked.add(a.name);
    });
    reflectCheckedInDom();
    updateBulkBar();
    announce(state.checked.size + ' agent' + (state.checked.size === 1 ? '' : 's') + ' selected.');
  }

  function clearSelection(focusControl) {
    state.checked.clear();
    state.rangeAnchor = -1;
    reflectCheckedInDom();
    updateBulkBar();
    announce('Selection cleared.');
    if (focusControl && els.selectAll) els.selectAll.focus();
  }

  // Select the contiguous range between the anchor and index i in the current
  // sorted+filtered order, adding every row in between to the checked set.
  function rangeSelectTo(i) {
    var anchor = state.rangeAnchor;
    if (anchor < 0 || anchor >= state.filtered.length) anchor = i;
    var lo = Math.min(anchor, i);
    var hi = Math.max(anchor, i);
    for (var k = lo; k <= hi; k++) {
      var a = state.filtered[k];
      if (a) state.checked.add(a.name);
    }
    state.rangeAnchor = i;
    reflectCheckedInDom();
    updateBulkBar();
  }

  // Sync checkbox + card classes to the checked set without a full re-render.
  function reflectCheckedInDom() {
    els.list.querySelectorAll('.roster-card').forEach(function (card) {
      var on = state.checked.has(card.dataset.name);
      var box = card.querySelector('.roster-card__check');
      if (box) box.checked = on;
      card.classList.toggle('is-checked', on);
    });
  }

  // Count checked agents that are not in the current filtered result set, so the
  // action bar can disclose "N selected · M hidden by filters" (PRD FR9).
  function hiddenCheckedCount() {
    if (state.checked.size === 0) return 0;
    var visible = {};
    state.filtered.forEach(function (a) {
      visible[a.name] = true;
    });
    var hidden = 0;
    state.checked.forEach(function (name) {
      if (!visible[name]) hidden++;
    });
    return hidden;
  }

  function updateBulkBar() {
    var total = state.checked.size;
    if (els.clearSelection) els.clearSelection.hidden = total === 0;
    if (!els.bulkBar) return;
    if (total === 0) {
      els.bulkBar.hidden = true;
      els.bulkCount.textContent = '';
      return;
    }
    els.bulkBar.hidden = false;
    var hidden = hiddenCheckedCount();
    var label = total + ' selected';
    if (hidden > 0) label += ' · ' + hidden + ' hidden by filters';
    els.bulkCount.textContent = label;
  }

  function announce(msg) {
    if (els.bulkLive) els.bulkLive.textContent = msg;
  }

  /* ---- bulk delete --------------------------------------------------------- */

  // Advisory client-side eligibility. The server re-checks and is authoritative
  // (PRD FR40); this only drives the preview grouping and the eligible count.
  function deleteEligibility(agent) {
    if (!agent) return { eligible: false, reason: 'Agent not found.' };
    if (isPermanent(agent)) {
      var role = String(agent.role || '').toLowerCase();
      var source = String(agent.source || '').toLowerCase();
      if (source === 'cli' || role === 'cli_agent')
        return { eligible: false, reason: 'Built-in CLI agent.' };
      return { eligible: false, reason: 'System assistant.' };
    }
    if ((agent.workspace_count || 0) > 0) {
      return {
        eligible: false,
        reason:
          'Attached to ' +
          agent.workspace_count +
          ' workspace' +
          (agent.workspace_count === 1 ? '' : 's') +
          '.'
      };
    }
    return { eligible: true, reason: '' };
  }

  function openBulkDelete() {
    if (state.checked.size === 0) return;
    // Deleting the focused agent will replace the stage; guard unsaved edits
    // before we commit to the flow (PRD FR15).
    var names = Array.from(state.checked);
    var eligible = [];
    var skipped = [];
    names.forEach(function (name) {
      var agent = state.byName[name];
      var e = deleteEligibility(agent);
      (e.eligible ? eligible : skipped).push({ name: name, reason: e.reason });
    });

    var body = '';
    body += deleteGroupHTML('To delete', eligible, true);
    if (skipped.length) body += deleteGroupHTML('Will be skipped', skipped, false);
    if (eligible.length === 0) {
      body += '<p class="bulk-dialog__none">None of the selected agents can be deleted.</p>';
    }
    els.bulkDeleteBody.innerHTML = body;

    els.bulkDeleteConfirm.disabled = eligible.length === 0;
    els.bulkDeleteConfirm.textContent = eligible.length
      ? 'Delete ' + eligible.length + ' agent' + (eligible.length === 1 ? '' : 's')
      : 'Delete';
    // Stash the authoritative-request payload: send ALL checked names so the
    // server returns a result per agent and eligibility changes are caught.
    els.bulkDeleteConfirm.dataset.names = JSON.stringify(names);
    els.bulkDeleteConfirm.dataset.eligible = String(eligible.length);

    if (typeof els.bulkDeleteDialog.showModal === 'function') els.bulkDeleteDialog.showModal();
    else els.bulkDeleteDialog.setAttribute('open', '');
  }

  function deleteGroupHTML(title, rows, danger) {
    if (!rows.length) return '';
    var items = rows
      .map(function (r) {
        var agent = state.byName[r.name];
        var reason = r.reason
          ? '<span class="bulk-dialog__reason">' + esc(r.reason) + '</span>'
          : '';
        var wsLink = '';
        if (agent && Array.isArray(agent.workspaces) && agent.workspaces.length) {
          var ws = agent.workspaces[0];
          if (ws && ws.id)
            wsLink =
              ' <a class="bulk-dialog__wslink" href="/workspaces/' +
              encodeURIComponent(ws.id) +
              '">' +
              esc(ws.name || 'workspace') +
              '</a>';
        }
        return (
          '<li><span class="bulk-dialog__agent">' +
          esc(r.name) +
          '</span>' +
          reason +
          wsLink +
          '</li>'
        );
      })
      .join('');
    return (
      '<div class="bulk-dialog__group' +
      (danger ? ' is-danger' : '') +
      '">' +
      '<h3 class="bulk-dialog__grouptitle">' +
      esc(title) +
      ' (' +
      rows.length +
      ')</h3>' +
      '<ul class="bulk-dialog__grouplist">' +
      items +
      '</ul></div>'
    );
  }

  function closeBulkDelete() {
    if (els.bulkDeleteDialog.open && typeof els.bulkDeleteDialog.close === 'function')
      els.bulkDeleteDialog.close();
    else els.bulkDeleteDialog.removeAttribute('open');
    // Return focus to a stable control rather than a possibly-removed card.
    if (els.selectAll) els.selectAll.focus();
  }

  function runBulkDelete() {
    var btn = els.bulkDeleteConfirm;
    if (btn.disabled || btn.dataset.busy === '1') return; // prevent double submit
    var names;
    try {
      names = JSON.parse(btn.dataset.names || '[]');
    } catch (e) {
      names = [];
    }
    if (!names.length) {
      closeBulkDelete();
      return;
    }

    // If the focused agent is among those being deleted, honor the unsaved guard.
    if (state.selected && names.indexOf(state.selected) !== -1 && !guardUnsaved()) return;

    btn.dataset.busy = '1';
    btn.disabled = true;
    var restoreLabel = btn.textContent;
    btn.textContent = 'Deleting…';
    announce('Deleting agents…');

    fetch('/api/agents/bulk', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ operation: 'delete', agent_names: names })
    })
      .then(function (r) {
        return r
          .json()
          .catch(function () {
            return null;
          })
          .then(function (d) {
            return { status: r.status, data: d };
          });
      })
      .then(function (res) {
        btn.dataset.busy = '';
        btn.textContent = restoreLabel;
        if (
          res.status < 200 ||
          res.status >= 300 ||
          !res.data ||
          !Array.isArray(res.data.results)
        ) {
          // Do NOT claim any deletion happened on a malformed/error response.
          announce('Bulk delete failed.');
          els.bulkDeleteBody.insertAdjacentHTML(
            'afterbegin',
            '<p class="bulk-dialog__error" role="alert">Delete failed (' +
              res.status +
              '). No agents were changed.</p>'
          );
          btn.disabled = false;
          return;
        }
        applyBulkDeleteResults(res.data);
      })
      .catch(function (err) {
        btn.dataset.busy = '';
        btn.disabled = false;
        btn.textContent = restoreLabel;
        announce('Network error — no agents deleted.');
        els.bulkDeleteBody.insertAdjacentHTML(
          'afterbegin',
          '<p class="bulk-dialog__error" role="alert">Network error — no agents were deleted.</p>'
        );
        console.error('[roster] bulk delete failed', err);
      });
  }

  function applyBulkDeleteResults(payload) {
    var results = payload.results || [];
    var summary = payload.summary || {};

    // Drop successfully deleted agents from the checked set; skipped/failed stay
    // checked and inspectable (PRD FR44).
    var focusedDeleted = false;
    results.forEach(function (r) {
      if (r.status === 'succeeded') {
        state.checked.delete(r.name);
        if (r.name === state.selected) focusedDeleted = true;
      }
    });

    closeBulkDelete();
    renderBulkResult(summary, results);

    // Reload from the server (authoritative): deleted agents vanish, skipped
    // survive and remain checked via pruneChecked. Keep the focused agent when
    // it survived; otherwise fall back predictably.
    reloadThenSelect(focusedDeleted ? null : state.selected);

    var msg = (summary.succeeded || 0) + ' deleted';
    if (summary.skipped) msg += ', ' + summary.skipped + ' skipped';
    if (summary.failed) msg += ', ' + summary.failed + ' failed';
    announce(msg + '.');
  }

  function renderBulkResult(summary, results) {
    if (!els.bulkResult) return;
    var parts = [];
    parts.push((summary.requested || results.length) + ' requested');
    parts.push((summary.succeeded || 0) + ' deleted');
    if (summary.skipped) parts.push(summary.skipped + ' skipped');
    if (summary.failed) parts.push(summary.failed + ' failed');
    els.bulkResultSummary.textContent = parts.join(' · ');

    // List only non-success results — those are the ones worth inspecting.
    var rows = results
      .filter(function (r) {
        return r.status !== 'succeeded';
      })
      .map(function (r) {
        var reason = r.message ? esc(r.message) : esc(r.reason_code || r.status);
        return (
          '<li class="bulk-result__item is-' +
          esc(r.status) +
          '">' +
          '<span class="bulk-result__name">' +
          esc(r.name) +
          '</span>' +
          '<span class="bulk-result__reason">' +
          reason +
          '</span></li>'
        );
      })
      .join('');
    els.bulkResultList.innerHTML = rows;
    els.bulkResult.hidden = false;
  }

  function dismissBulkResult() {
    if (!els.bulkResult) return;
    els.bulkResult.hidden = true;
    els.bulkResultList.innerHTML = '';
    els.bulkResultSummary.textContent = '';
    if (els.selectAll) els.selectAll.focus();
  }

  /* ---- bulk tags + favorite ------------------------------------------------ */

  var bulkTagsInput = null; // active OriTagInput instance for the add flow

  // Reused for tags AND bulk role assignment (PRD FR9, Group 5.4): the dialog
  // is a generic "bulk edit" shell keyed by dlg.dataset.mode.
  function openBulkTags(mode) {
    if (state.checked.size === 0) return;
    if (state.selected && state.checked.has(state.selected) && !guardUnsaved()) return;
    var dlg = els.bulkTagsDialog;
    dlg.dataset.mode = mode;
    var titles = { add: 'Add tags', remove: 'Remove tags', role: 'Set role' };
    els.bulkTagsTitle.textContent = titles[mode] || '';
    els.bulkTagsConfirm.disabled = false;
    els.bulkTagsConfirm.textContent = mode === 'role' ? 'Set role' : titles[mode];
    bulkTagsInput = null;

    if (mode === 'role') {
      var roleOrder = (window.RoleCatalog && window.RoleCatalog.orderedRoles) || [];
      var options = roleOrder
        .map(function (slug) {
          return '<option value="' + esc(slug) + '">' + esc(roleLabel(slug)) + '</option>';
        })
        .join('');
      options +=
        '<option value="general">' + esc(roleLabel('general')) + '</option>';
      els.bulkTagsBody.innerHTML =
        '<p class="bulk-dialog__lead">Assign one role to every selected agent. Metadata only — model, prompt, and skills are unchanged.</p>' +
        '<label class="bulk-dialog__field"><span class="visually-hidden">Role</span>' +
        '<select id="bulkRoleSelect" class="bulk-dialog__text">' +
        options +
        '</select></label>';
      if (typeof dlg.showModal === 'function') dlg.showModal();
      else dlg.setAttribute('open', '');
      return;
    }

    if (mode === 'add') {
      els.bulkTagsBody.innerHTML =
        '<p class="bulk-dialog__lead">Tags are added to every eligible selected agent; existing tags are kept.</p>' +
        '<div id="bulkTagsInputHost"></div>';
      var host = document.getElementById('bulkTagsInputHost');
      if (window.OriTagInput && host) {
        bulkTagsInput = window.OriTagInput.createTagInput({
          container: host,
          placeholder: 'Add tag…'
        });
      } else if (host) {
        host.innerHTML =
          '<input id="bulkTagsText" type="text" class="bulk-dialog__text" placeholder="tag1, tag2">';
      }
    } else {
      // Remove: offer the union of tags across the checked set as a checklist so
      // the user picks from values that actually exist (PRD FR27).
      var union = tagsUnion(Array.from(state.checked));
      if (union.length === 0) {
        els.bulkTagsBody.innerHTML =
          '<p class="bulk-dialog__none">The selected agents have no tags to remove.</p>';
        els.bulkTagsConfirm.disabled = true;
      } else {
        els.bulkTagsBody.innerHTML =
          '<p class="bulk-dialog__lead">Remove these tags from every selected agent that has them.</p>' +
          '<div class="bulk-tags-checklist">' +
          union
            .map(function (t) {
              return (
                '<label class="bulk-tags-checklist__item"><input type="checkbox" value="' +
                esc(t) +
                '"> ' +
                esc(t) +
                '</label>'
              );
            })
            .join('') +
          '</div>';
      }
    }
    if (typeof dlg.showModal === 'function') dlg.showModal();
    else dlg.setAttribute('open', '');
  }

  function closeBulkTags() {
    var dlg = els.bulkTagsDialog;
    if (dlg.open && typeof dlg.close === 'function') dlg.close();
    else dlg.removeAttribute('open');
    bulkTagsInput = null;
    if (els.selectAll) els.selectAll.focus();
  }

  // Union of tags across the given agents (case-insensitive, original casing).
  function tagsUnion(names) {
    var seen = {};
    var out = [];
    names.forEach(function (name) {
      var a = state.byName[name];
      var tags = (a && a.metadata && a.metadata.tags) || [];
      tags.forEach(function (t) {
        var key = String(t).toLowerCase();
        if (!seen[key]) {
          seen[key] = true;
          out.push(t);
        }
      });
    });
    out.sort(function (a, b) {
      return String(a).localeCompare(String(b));
    });
    return out;
  }

  function runBulkTags() {
    var btn = els.bulkTagsConfirm;
    if (btn.disabled || btn.dataset.busy === '1') return;
    var mode = els.bulkTagsDialog.dataset.mode;

    if (mode === 'role') {
      var roleSel = document.getElementById('bulkRoleSelect');
      var role = roleSel ? roleSel.value : '';
      if (!role) return;
      btn.dataset.busy = '1';
      btn.disabled = true;
      var restoreRole = btn.textContent;
      btn.textContent = 'Working…';
      submitBulkMetadata(
        { operation: 'set_role', agent_names: Array.from(state.checked), role: role },
        function () {
          btn.dataset.busy = '';
          btn.disabled = false;
          btn.textContent = restoreRole;
        },
        closeBulkTags
      );
      return;
    }

    var tags = [];
    if (mode === 'add') {
      if (bulkTagsInput) tags = bulkTagsInput.getTags();
      else {
        var txt = document.getElementById('bulkTagsText');
        tags = txt ? txt.value.split(',') : [];
      }
    } else {
      els.bulkTagsBody.querySelectorAll('input[type="checkbox"]:checked').forEach(function (c) {
        tags.push(c.value);
      });
    }
    tags = tags
      .map(function (t) {
        return String(t).trim();
      })
      .filter(Boolean);
    if (tags.length === 0) {
      els.bulkTagsBody.insertAdjacentHTML(
        'afterbegin',
        '<p class="bulk-dialog__error" role="alert">Enter at least one tag.</p>'
      );
      return;
    }
    var op = mode === 'add' ? 'add_tags' : 'remove_tags';
    btn.dataset.busy = '1';
    btn.disabled = true;
    var restore = btn.textContent;
    btn.textContent = 'Working…';
    submitBulkMetadata(
      { operation: op, agent_names: Array.from(state.checked), tags: tags },
      function () {
        btn.dataset.busy = '';
        btn.disabled = false;
        btn.textContent = restore;
      },
      closeBulkTags
    );
  }

  function runBulkFavorite(favorite) {
    if (state.checked.size === 0) return;
    if (state.selected && state.checked.has(state.selected) && !guardUnsaved()) return;
    var btns = [els.bulkFavorite, els.bulkUnfavorite];
    btns.forEach(function (b) {
      b.disabled = true;
    });
    announce(favorite ? 'Favoriting…' : 'Unfavoriting…');
    submitBulkMetadata(
      { operation: 'set_favorite', agent_names: Array.from(state.checked), favorite: favorite },
      function () {
        btns.forEach(function (b) {
          b.disabled = false;
        });
      },
      null
    );
  }

  // Shared bulk-metadata POST with shared-definition confirmation retry. `always`
  // runs on completion; `onDone` (optional) runs only on a successful response
  // (e.g. to close a dialog).
  function submitBulkMetadata(payload, always, onDone) {
    fetch('/api/agents/bulk', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload)
    })
      .then(function (r) {
        return r
          .json()
          .catch(function () {
            return null;
          })
          .then(function (d) {
            return { status: r.status, data: d };
          });
      })
      .then(function (res) {
        if (always) always();
        if (
          res.status < 200 ||
          res.status >= 300 ||
          !res.data ||
          !Array.isArray(res.data.results)
        ) {
          announce('Bulk update failed.');
          return;
        }
        var results = res.data.results;
        // Any agent needing shared-definition confirmation → ask once, resend
        // with confirm_shared_edit for the whole batch (PRD FR31).
        var needsConfirm = results.some(function (r) {
          return r.reason_code === 'shared_edit_requires_confirmation';
        });
        if (needsConfirm && !payload.confirm_shared_edit) {
          var n = results.filter(function (r) {
            return r.reason_code === 'shared_edit_requires_confirmation';
          }).length;
          if (
            window.confirm(
              n +
                ' selected agent(s) are attached to multiple workspaces. This change affects all of them. Apply it?'
            )
          ) {
            submitBulkMetadata(
              Object.assign({}, payload, { confirm_shared_edit: true }),
              always,
              onDone
            );
            return;
          }
        }
        if (onDone) onDone();
        renderBulkMetadataResult(res.data);
        // Refresh roster data so cards/overview/filters reflect the change,
        // keeping the focused agent and (via pruneChecked) the checked set.
        reloadThenSelect(state.selected);
      })
      .catch(function (err) {
        if (always) always();
        announce('Network error — no changes applied.');
        console.error('[roster] bulk metadata failed', err);
      });
  }

  function renderBulkMetadataResult(payload) {
    var summary = payload.summary || {};
    var results = payload.results || [];
    if (!els.bulkResult) return;
    var parts = [];
    parts.push((summary.requested || results.length) + ' requested');
    parts.push((summary.succeeded || 0) + ' updated');
    if (summary.skipped) parts.push(summary.skipped + ' skipped');
    if (summary.failed) parts.push(summary.failed + ' failed');
    els.bulkResultSummary.textContent = parts.join(' · ');
    var rows = results
      .filter(function (r) {
        return r.status !== 'succeeded';
      })
      .map(function (r) {
        var reason = r.message ? esc(r.message) : esc(r.reason_code || r.status);
        return (
          '<li class="bulk-result__item is-' +
          esc(r.status) +
          '">' +
          '<span class="bulk-result__name">' +
          esc(r.name) +
          '</span>' +
          '<span class="bulk-result__reason">' +
          reason +
          '</span></li>'
        );
      })
      .join('');
    els.bulkResultList.innerHTML = rows;
    els.bulkResult.hidden = false;
    var msg = (summary.succeeded || 0) + ' updated';
    if (summary.skipped) msg += ', ' + summary.skipped + ' skipped';
    announce(msg + '.');
  }

  function onSearch() {
    state.query = els.search.value || '';
    applyFilterSort();
    syncUrl(state.selected, false);
  }
  function onSort() {
    state.sort = els.sort.value || 'name-asc';
    applyFilterSort();
    if (state.selected) highlightSelected();
    syncUrl(state.selected, false);
  }

  /* ---- form field builders ------------------------------------------------- */

  function field(label, control, forId) {
    // Associate the label with its control (forId) for a11y; controls that carry
    // their own wrapping label (e.g. the checkbox) pass no forId and get a span.
    var lab = forId
      ? '<label class="field__label" for="' + forId + '">' + esc(label) + '</label>'
      : '<span class="field__label">' + esc(label) + '</span>';
    return '<div class="field">' + lab + '<div class="field__control">' + control + '</div></div>';
  }
  function textInput(id, value, placeholder) {
    return (
      '<input id="' +
      id +
      '" type="text" value="' +
      esc(value) +
      '"' +
      (placeholder ? ' placeholder="' + esc(placeholder) + '"' : '') +
      '>'
    );
  }
  // A read-only text input for derived values (e.g. Provider, set by the model).
  // Kept as an <input> so the form still submits its value, but not user-editable.
  function readonlyInput(id, value, title) {
    return (
      '<input id="' +
      id +
      '" class="field__readonly" type="text" readonly tabindex="-1" value="' +
      esc(value) +
      '"' +
      (title ? ' title="' + esc(title) + '"' : '') +
      '>'
    );
  }
  function numInput(id, value, min, max, step) {
    return (
      '<input id="' +
      id +
      '" type="number" value="' +
      esc(value === '' ? '' : value) +
      '"' +
      (min !== '' ? ' min="' + min + '"' : '') +
      (max ? ' max="' + max + '"' : '') +
      (step ? ' step="' + step + '"' : '') +
      '>'
    );
  }
  function checkInput(id, on) {
    return (
      '<label class="check"><input id="' +
      id +
      '" type="checkbox"' +
      (on ? ' checked' : '') +
      '> Allowed</label>'
    );
  }
  function selectInput(id, options, value, labeler) {
    var opts = options
      .map(function (o) {
        var label = labeler ? labeler(o) : o;
        return (
          '<option value="' +
          esc(o) +
          '"' +
          (o === value ? ' selected' : '') +
          '>' +
          esc(label) +
          '</option>'
        );
      })
      .join('');
    return '<select id="' + id + '">' + opts + '</select>';
  }
  function textareaInput(id, value, rows) {
    return '<textarea id="' + id + '" rows="' + rows + '">' + esc(value) + '</textarea>';
  }
  // Color picker + hex text, kept in sync, for the avatar color field.
  function colorInput(id, value) {
    var v = /^#[0-9a-fA-F]{6}$/.test(String(value)) ? value : '#4f46e5';
    return (
      '<span class="color-input">' +
      '<input id="' +
      id +
      '" type="color" value="' +
      esc(v) +
      '">' +
      '<input id="' +
      id +
      '-hex" type="text" class="color-input__hex" value="' +
      esc(v) +
      '" maxlength="7" spellcheck="false" aria-label="Avatar color hex">' +
      '</span>'
    );
  }
  // Keep the color picker and its hex text field in sync (either drives the other).
  function wireColorInput(id) {
    var picker = document.getElementById(id);
    var hex = document.getElementById(id + '-hex');
    if (!picker || !hex) return;
    picker.addEventListener('input', function () {
      hex.value = picker.value;
    });
    hex.addEventListener('input', function () {
      if (/^#[0-9a-fA-F]{6}$/.test(hex.value)) picker.value = hex.value;
    });
  }
  // Grouped <select> of available models (optgroup per provider). Each option
  // carries data-provider so the Provider field can follow the chosen model. A
  // model that isn't in the catalog (custom, or a provider without a key) is
  // preserved under a "Current" group so switching to a picker never drops it.
  function modelSelectInput(id, currentValue, currentProvider, typeFilter) {
    return (
      '<select id="' +
      id +
      '">' +
      modelOptionsHTML(currentValue, currentProvider, typeFilter) +
      '</select>'
    );
  }
  // Build the grouped <option>/<optgroup> markup for the model picker. Each option
  // carries the data the overview surfaces: provider (for provider sync), pricing
  // and good-for (the model note), and legacy/deprecation flags (the warning). A
  // typeFilter scopes options to models whose catalog category matches, and any
  // off-catalog current model is preserved under a "Current" group.
  function modelOptionsHTML(currentValue, currentProvider, typeFilter) {
    var providers = state.providers || [];
    var groups = '';
    var matched = false;
    providers.forEach(function (p) {
      if (!p || !Array.isArray(p.models) || p.models.length === 0) return;
      var seen = {};
      var opts = '';
      p.models.forEach(function (m) {
        if (!m || !m.value || seen[m.value]) return;
        // Filter by agent type when the model declares a category.
        if (typeFilter && m.type && m.type !== typeFilter) return;
        seen[m.value] = true;
        var sel = m.value === currentValue;
        if (sel) matched = true;
        opts += modelOptionHTML(m.value, m.provider || p.name, m, sel);
      });
      if (opts)
        groups += '<optgroup label="' + esc(p.display_name || p.name) + '">' + opts + '</optgroup>';
    });
    // Preserve the agent's current model when the type filter excluded it (its real
    // category differs) or it isn't in the catalog at all. Enrich it from the full
    // catalog so its note/warning still show even outside the filtered groups.
    if (currentValue && !matched) {
      var meta = modelMeta(currentValue);
      groups =
        '<optgroup label="Current">' +
        modelOptionHTML(
          currentValue,
          (meta && meta.provider) || currentProvider || '',
          meta,
          true
        ) +
        '</optgroup>' +
        groups;
    }
    return groups;
  }
  // Render one <option> for the model picker from a catalog entry (meta may be null
  // for a truly unknown model). Encodes the data the overview reads back.
  function modelOptionHTML(value, provider, meta, selected) {
    var pricing = meta && meta.pricing ? meta.pricing : '';
    var goodFor =
      meta && Array.isArray(meta.good_for) && meta.good_for.length ? meta.good_for[0] : '';
    var legacy = !!(meta && meta.is_legacy);
    var dep = meta && meta.deprecation_date ? meta.deprecation_date : '';
    var label = (meta && meta.label) || value;
    return (
      '<option value="' +
      esc(value) +
      '"' +
      ' data-provider="' +
      esc(provider) +
      '"' +
      (pricing ? ' data-pricing="' + esc(pricing) + '"' : '') +
      (goodFor ? ' data-goodfor="' + esc(goodFor) + '"' : '') +
      (legacy ? ' data-legacy="1"' : '') +
      (dep ? ' data-deprecation="' + esc(dep) + '"' : '') +
      (selected ? ' selected' : '') +
      '>' +
      esc(label) +
      (legacy ? ' ⚠' : '') +
      (pricing ? ' · ' + esc(pricing) : '') +
      '</option>'
    );
  }
  function saveBar(tab) {
    return (
      '<div class="save-bar" id="savebar-' +
      tab +
      '">' +
      '<span class="dirty-note"></span>' +
      '<span class="save-status is-muted"></span>' +
      '<button type="button" class="btn-ghost" data-role="revert">Revert</button>' +
      '<button type="button" class="btn-primary" data-role="save" disabled>Save</button>' +
      '</div>'
    );
  }

  /* ---- misc helpers -------------------------------------------------------- */

  function val(id) {
    var el = document.getElementById(id);
    return el ? el.value : '';
  }
  function checked(id) {
    var el = document.getElementById(id);
    return !!(el && el.checked);
  }

  function vital(label, value) {
    return '<span class="vital"><span>' + esc(label) + '</span><b>' + esc(value) + '</b></span>';
  }

  function avatarMarkup(agent, className, id) {
    var idAttr = id ? ' id="' + id + '"' : '';
    var image = agent && agent.metadata && agent.metadata.avatar_image;
    if (image) {
      return (
        '<div class="' +
        className +
        '"' +
        idAttr +
        ' style="padding:0;overflow:hidden;">' +
        '<img src="/avatars/' +
        esc(String(image)) +
        '" alt="" loading="lazy" decoding="async" style="width:100%;height:100%;object-fit:cover;"></div>'
      );
    }
    var color =
      (agent && agent.metadata && agent.metadata.avatar_color) || colorFor(agent ? agent.name : '');
    return (
      '<div class="' +
      className +
      '"' +
      idAttr +
      ' style="background:' +
      esc(color) +
      ';">' +
      esc(initials(agent ? agent.name : '')) +
      '</div>'
    );
  }

  function initials(name) {
    var parts = String(name || '?')
      .trim()
      .split(/\s+/);
    if (parts.length === 1) return parts[0].slice(0, 2).toUpperCase();
    return (parts[0][0] + parts[parts.length - 1][0]).toUpperCase();
  }

  function colorFor(name) {
    // 700-level shades so white initials clear WCAG AA contrast (>= 4.5:1).
    var palette = [
      '#4338ca',
      '#0e7490',
      '#6d28d9',
      '#047857',
      '#b45309',
      '#be185d',
      '#1d4ed8',
      '#b91c1c'
    ];
    var sum = 0;
    String(name || '')
      .split('')
      .forEach(function (c) {
        sum += c.charCodeAt(0);
      });
    return palette[sum % palette.length];
  }

  // "Permanent residency" agents: the built-in CLI agents (Claude Code, Codex,
  // Gemini CLI) and the Ori system assistant. They're always available and the
  // server won't delete them.
  function isPermanent(agent) {
    if (!agent) return false;
    var source = String(agent.source || '').toLowerCase();
    var role = String(agent.role || '').toLowerCase();
    if (source === 'cli' || role === 'cli_agent') return true;
    var name = String(agent.name || '')
      .trim()
      .toLowerCase();
    return name === 'ori' || name === '__assistant__';
  }

  function healthKind(agent) {
    var s = String((agent && agent.status) || 'idle').toLowerCase();
    if (s === 'active') return 'active';
    if (s === 'error') return 'error';
    if (s === 'disabled') return 'disabled';
    return 'idle';
  }

  function lastActive(agent) {
    var v = agent && agent.statistics && agent.statistics.last_active;
    var t = v ? Date.parse(v) : 0;
    return isNaN(t) ? 0 : t;
  }

  // Locale-aware relative "last active" so the Recently Active sort is legible
  // (PRD FR60). Returns '' when there's no usable timestamp. A zero/epoch value
  // (never active) is treated as missing rather than "55 years ago".
  var relTimeFmt =
    typeof Intl !== 'undefined' && Intl.RelativeTimeFormat
      ? new Intl.RelativeTimeFormat(undefined, { numeric: 'auto' })
      : null;
  function lastActiveLabel(agent) {
    var t = lastActive(agent);
    if (!t) return '';
    var diffMs = t - Date.now();
    var absMin = Math.abs(diffMs) / 60000;
    if (absMin < 1) return 'Active now';
    if (!relTimeFmt) return '';
    var units = [
      ['year', 525600],
      ['month', 43200],
      ['week', 10080],
      ['day', 1440],
      ['hour', 60],
      ['minute', 1]
    ];
    for (var i = 0; i < units.length; i++) {
      var perUnit = units[i][1];
      if (absMin >= perUnit || units[i][0] === 'minute') {
        var value = Math.round(diffMs / 60000 / perUnit);
        return relTimeFmt.format(value, units[i][0]);
      }
    }
    return '';
  }

  // roleLabel wraps RoleCatalog.label with a fallback for the (brief) window
  // before role-catalog.js's fetch resolves, or if it failed to load.
  function roleLabel(role) {
    return window.RoleCatalog ? window.RoleCatalog.label(role) : titleCase(role || 'Unspecialized');
  }

  function titleCase(s) {
    s = String(s || '')
      .replace(/[_-]+/g, ' ')
      .trim();
    if (!s) return '';
    return s.replace(/\w\S*/g, function (w) {
      return w.charAt(0).toUpperCase() + w.slice(1);
    });
  }

  // Serialize search, sort, filters, focused agent, and active tab to the URL.
  // Checked-agent state is intentionally excluded (PRD FR77/FR80). `push` adds a
  // history entry (agent focus changes); everything else replaces in place.
  function syncUrl(name, push) {
    var params = new URLSearchParams();
    if (name) params.set('agent', name);
    if (state.query.trim()) params.set('q', state.query.trim());
    if (state.sort && state.sort !== 'name-asc') params.set('sort', state.sort);
    var tab = currentTab();
    if (tab && tab !== 'overview') params.set('tab', tab);
    var f = state.filters;
    if (f.role) params.set('role', f.role);
    if (f.source) params.set('source', f.source);
    if (f.assignment) params.set('assign', f.assignment);
    if (f.favorite) params.set('fav', '1');
    if (f.tag) params.append('tag', f.tag);
    f.health.forEach(function (h) {
      params.append('health', h);
    });

    var qs = params.toString();
    var url = window.location.pathname + (qs ? '?' + qs : '');
    var stateObj = { agent: name };
    if (push) window.history.pushState(stateObj, '', url);
    else window.history.replaceState(stateObj, '', url);
  }

  // Read filter/search/sort/tab state from the URL into state (not the focused
  // agent — that is resolved separately against the loaded roster). Invalid or
  // unknown values are discarded safely (PRD FR79).
  function applyUrlToState() {
    var p = new URLSearchParams(window.location.search);
    state.query = p.get('q') || '';
    if (els.search) els.search.value = state.query;
    var sort = p.get('sort') || 'name-asc';
    var validSorts = {
      'name-asc': 1,
      'name-desc': 1,
      'level-desc': 1,
      'workspaces-desc': 1,
      'active-desc': 1
    };
    state.sort = validSorts[sort] ? sort : 'name-asc';
    if (els.sort) els.sort.value = state.sort;

    var f = { health: new Set(), role: '', source: '', assignment: '', tag: '', favorite: false };
    var role = p.get('role');
    if (role) f.role = role;
    var source = p.get('source');
    if (source === 'user' || source === 'cli') f.source = source;
    var assign = p.get('assign');
    if (assign === 'library' || assign === 'assigned') f.assignment = assign;
    if (p.get('fav') === '1') f.favorite = true;
    var tag = p.get('tag');
    if (tag) f.tag = tag;
    p.getAll('health').forEach(function (h) {
      if (h === 'needs' || h === 'ready' || h === 'disabled') f.health.add(h);
    });
    state.filters = f;
    reflectFiltersInControls();
  }

  // Push current filter state onto the controls (after a URL restore / popstate).
  function reflectFiltersInControls() {
    var f = state.filters;
    if (els.filterRole) els.filterRole.value = f.role;
    if (els.filterSource) els.filterSource.value = f.source;
    if (els.filterAssignment) els.filterAssignment.value = f.assignment;
    if (els.filterTag) els.filterTag.value = f.tag;
    if (els.filterFavorite) {
      els.filterFavorite.setAttribute('aria-pressed', f.favorite ? 'true' : 'false');
      els.filterFavorite.classList.toggle('is-active', f.favorite);
    }
    if (els.clearFilters) els.clearFilters.hidden = !filtersActive();
  }

  function restoreTabFromUrl() {
    var tab = new URLSearchParams(window.location.search).get('tab');
    if (tab && TAB_ORDER.indexOf(tab) !== -1 && tab !== 'overview') requestTab(tab, false);
  }

  function safeStorageGet() {
    try {
      return window.localStorage.getItem(STORAGE_KEY);
    } catch (e) {
      return null;
    }
  }
  function safeStorageSet(v) {
    try {
      window.localStorage.setItem(STORAGE_KEY, v);
    } catch (e) {
      /* ignore */
    }
  }

  function cssEscape(s) {
    if (window.CSS && window.CSS.escape) return window.CSS.escape(s);
    return String(s).replace(/"/g, '\\"');
  }

  function esc(s) {
    return String(s == null ? '' : s)
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;')
      .replace(/'/g, '&#39;');
  }
})();
