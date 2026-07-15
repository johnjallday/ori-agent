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
  var ROLES = ['general', 'orchestrator', 'researcher', 'analyzer', 'synthesizer', 'validator', 'specialist'];
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
    // Checked agents: the session-only bulk-selection set, kept entirely separate
    // from `selected` (the focused agent driving the stage). Never persisted to
    // URL or localStorage; a reload starts empty (PRD FR1/FR12/FR13).
    checked: new Set(),
    // Anchor index (into the current sorted+filtered roster) for Shift-click and
    // Shift+Space contiguous range selection (PRD FR10).
    rangeAnchor: -1,
  };

  var els = {};

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
    els.clearSelection.addEventListener('click', function () { clearSelection(true); });

    els.bulkDelete.addEventListener('click', openBulkDelete);
    els.bulkDeleteCancel.addEventListener('click', function () { closeBulkDelete(); });
    els.bulkDeleteConfirm.addEventListener('click', runBulkDelete);
    els.bulkResultDismiss.addEventListener('click', dismissBulkResult);
    // Native <dialog> fires 'cancel' on Escape; keep our teardown consistent.
    els.bulkDeleteDialog.addEventListener('cancel', function (e) { e.preventDefault(); closeBulkDelete(); });

    var tabs = document.querySelectorAll('.stage__tab');
    tabs.forEach(function (tab) {
      tab.addEventListener('click', function () { requestTab(tab.dataset.tab, true); });
      tab.addEventListener('keydown', onTabKeydown);
    });

    window.addEventListener('popstate', function () {
      var name = new URLSearchParams(window.location.search).get('agent');
      if (name && state.byName[name]) selectAgent(name, { push: false });
    });

    window.addEventListener('beforeunload', function (e) {
      if (anyDirty()) { e.preventDefault(); e.returnValue = ''; }
    });

    loadProviders();
    loadAgents();
  }

  /* ---- data ---------------------------------------------------------------- */

  // Load the available LLM providers + their models so the overview Model field
  // can offer a picker instead of free text. Fire-and-forget: if it fails or is
  // slow, renderOverview falls back to a text input.
  function loadProviders() {
    fetch('/api/providers')
      .then(function (r) { return r.ok ? r.json() : null; })
      .then(function (data) {
        state.providers = (data && Array.isArray(data.providers)) ? data.providers : [];
        // If an agent was already selected and its stage rendered before the model
        // catalog arrived, refresh the catalog-dependent surfaces: the overview
        // (picker + notes) and the vitals (legacy badge). Guarded by !dirty so we
        // never clobber unsaved edits.
        if (state.selected && !state.dirty.overview && state.detailCache[state.selected]) {
          renderVitals(state.byName[state.selected], state.detailCache[state.selected]);
          renderOverview(state.selected, state.detailCache[state.selected]);
        }
      })
      .catch(function () { state.providers = []; });
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
        agents.forEach(function (a) { state.byName[a.name] = a; });
        pruneChecked();
        applyFilterSort();
        restoreSelection();
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
    state.checked.forEach(function (name) { if (!state.byName[name]) stale.push(name); });
    stale.forEach(function (name) { state.checked.delete(name); });
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
      if (!q) return true;
      var hay = (a.name + ' ' + (a.role || '') + ' ' + ((a.metadata && a.metadata.description) || '')).toLowerCase();
      return hay.indexOf(q) !== -1;
    });

    list.sort(function (a, b) {
      switch (state.sort) {
        case 'name-desc': return b.name.localeCompare(a.name);
        case 'workspaces-desc': return (b.workspace_count || 0) - (a.workspace_count || 0) || a.name.localeCompare(b.name);
        case 'active-desc': return lastActive(b) - lastActive(a) || a.name.localeCompare(b.name);
        default: return a.name.localeCompare(b.name);
      }
    });

    state.filtered = list;
    renderRoster();
  }

  function renderRoster() {
    els.list.innerHTML = '';
    var total = state.agents.length;
    var shown = state.filtered.length;

    renderStatusTiles();

    if (shown === 0) {
      els.empty.hidden = false;
      els.count.textContent = total === 0 ? 'No agents yet.' : '0 of ' + total + ' agents';
      updateBulkBar();
      return;
    }
    els.empty.hidden = true;
    els.count.textContent = shown === total ? total + ' agent' + (total === 1 ? '' : 's') : shown + ' of ' + total + ' agents';

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
    if (total === 0) { els.stats.hidden = true; els.stats.innerHTML = ''; return; }
    var needs = 0, disabled = 0;
    state.agents.forEach(function (a) {
      var status = String((a && a.status) || 'idle').toLowerCase();
      if (status === 'disabled') { disabled++; return; }
      if (status === 'error' || !String((a && a.model) || '').trim()) { needs++; }
    });
    var ready = total - needs - disabled;
    els.stats.hidden = false;
    els.stats.innerHTML =
      statTile('needs', 'Needs attention', needs) +
      statTile('ready', 'Ready', ready) +
      statTile('disabled', 'Disabled', disabled) +
      statTile('total', 'Total', total);
  }

  function statTile(kind, label, value) {
    var zero = value === 0 ? ' roster-stat--zero' : '';
    return '<div class="roster-stat roster-stat--' + kind + zero + '">' +
      '<span class="roster-stat__value">' + value + '</span>' +
      '<span class="roster-stat__label">' + esc(label) + '</span>' +
      '</div>';
  }

  function buildCard(agent, idx) {
    var li = document.createElement('li');
    li.className = 'roster-card' + (isPermanent(agent) ? ' is-permanent' : '');
    li.dataset.name = agent.name;
    li.dataset.index = idx;

    var status = healthKind(agent);
    var metaBits = [];
    if (agent.model) metaBits.push(agent.model);
    var wc = agent.workspace_count || 0;
    metaBits.push(wc === 0 ? 'Library' : wc + ' workspace' + (wc === 1 ? '' : 's'));

    var permanent = isPermanent(agent);
    var isChecked = state.checked.has(agent.name);

    // Concise spoken label so screen readers don't read the raw dot markup.
    var wcLabel = wc === 0 ? 'library agent, unattached' : wc + ' workspace' + (wc === 1 ? '' : 's');
    var openLabel = agent.name + (permanent ? ', built-in' : '') + ', ' + status + ', ' + wcLabel;

    var badge = permanent
      ? '<span class="roster-card__badge" title="Built-in agent — always available and cannot be deleted">Built-in</span>'
      : '';

    // A labeled checkbox and a separate open/focus button are siblings — never
    // nested — so the checkbox is not a child of an interactive element and the
    // two actions stay independent (PRD FR2/FR3/FR4).
    li.innerHTML =
      '<label class="roster-card__checkwrap">' +
      '<span class="visually-hidden">Select ' + esc(agent.name) + '</span>' +
      '<input type="checkbox" class="roster-card__check" data-check="' + esc(agent.name) + '"' + (isChecked ? ' checked' : '') + '>' +
      '</label>' +
      '<button type="button" class="roster-card__open" data-open="' + esc(agent.name) + '" aria-label="' + esc(openLabel) + '">' +
      avatarMarkup(agent, 'roster-card__avatar') +
      '<span class="roster-card__body">' +
      '<span class="roster-card__namerow">' +
      '<span class="roster-card__name">' + esc(agent.name) + '</span>' + badge +
      '</span>' +
      '<span class="roster-card__meta">' + esc(metaBits.join(' · ')) + '</span>' +
      '</span>' +
      '<span class="roster-card__status is-' + status + '" title="' + esc(status) + '"></span>' +
      '</button>';

    if (isChecked) li.classList.add('is-checked');
    return li;
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
    var pick = (fromUrl && state.byName[fromUrl] && fromUrl) ||
      (fromStore && state.byName[fromStore] && fromStore) ||
      (state.filtered[0] && state.filtered[0].name) || null;
    if (pick) selectAgent(pick, { push: false });
  }

  function selectAgent(name, opts) {
    opts = opts || {};
    if (!state.byName[name]) return;
    if (name !== state.selected && !guardUnsaved()) return;
    state.selected = name;
    state.focusIndex = state.filtered.findIndex(function (a) { return a.name === name; });
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
    els.klass.textContent = titleCase(listItem.role || listItem.type || 'agent');
    // Deep-link to the full agent detail page (/agents/{name}). The server routes
    // this to the rich editor for catalog agents and to the dedicated read-only
    // pages for the built-in Claude Code / Codex CLI agents.
    els.stageFullPage.href = '/agents/' + encodeURIComponent(name);

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
      vitals.push('<span class="vital vital--warn" title="This model is past its deprecation date"><span>Model</span><b>⚠ Legacy</b></span>');
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
      els.overviewDesc.innerHTML = '<p class="stage-hint">This is a built-in agent and cannot be edited here.</p>';
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

    els.overviewFacts.innerHTML =
      '<form class="stage-form" id="overviewForm" novalidate>' +
      field('Role', selectInput('ov-role', ROLES, detail.role, titleCase), 'ov-role') +
      field('Type', selectInput('ov-type', TYPES, agentType, typeLabel), 'ov-type') +
      field('Model', modelControl + '<p class="model-note" id="ov-model-note" aria-live="polite"></p>', 'ov-model') +
      field('Provider', providerControl, 'ov-provider') +
      field('Temperature', numInput('ov-temperature', detail.temperature, '0', '2', '0.1'), 'ov-temperature') +
      '<div class="field" id="ov-reasoning-field">' +
        '<label class="field__label" for="ov-reasoning">Reasoning effort</label>' +
        '<div class="field__control">' +
        selectInput('ov-reasoning', REASONING, detail.reasoning_effort || '', function (v) { return v ? titleCase(v) : 'Default'; }) +
        '</div></div>' +
      field('Max output tokens', numInput('ov-maxtokens', detail.max_output_tokens || '', '0', '', '1'), 'ov-maxtokens') +
      field('Web search', checkInput('ov-websearch', detail.allow_web_search)) +
      field('Description', textareaInput('ov-description', (detail.metadata && detail.metadata.description) || '', 3), 'ov-description') +
      '</form>' +
      saveBar('overview');

    els.overviewDesc.innerHTML = '';
    wireDirty('overview', document.getElementById('overviewForm'));
    wireSaveBar('overview', function () { saveOverview(name); });

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
    if (!opt) { note.textContent = ''; note.className = 'model-note'; return; }
    var bits = [];
    var goodFor = opt.getAttribute('data-goodfor');
    if (goodFor) bits.push(goodFor);
    var pricing = opt.getAttribute('data-pricing');
    if (pricing) bits.push(pricing);
    var legacy = opt.getAttribute('data-legacy') === '1';
    var dep = opt.getAttribute('data-deprecation');
    note.className = 'model-note' + (legacy ? ' is-legacy' : '');
    var warn = legacy ? ('⚠ Legacy model' + (dep ? ' — deprecated ' + dep : '') + '. ') : '';
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
    var m = String(model || '').toLowerCase().trim();
    if (!m) return false;
    return /^o[1345](-|$)/.test(m) || m.indexOf('gpt-5') !== -1 || m.indexOf('codex') !== -1;
  }

  function typeLabel(v) {
    switch (v) {
      case 'tool-calling': return 'Tool Calling';
      case 'general': return 'General Purpose';
      case 'research': return 'Research';
      case 'orchestration': return 'Orchestration';
      default: return titleCase(v || '');
    }
  }

  function readonlyFacts(detail) {
    var facts = [
      ['Role', titleCase((detail && detail.role) || '—')],
      ['Model', (detail && detail.model) || '—'],
      ['Provider', detail && detail.provider ? titleCase(detail.provider) : '—'],
      ['Temperature', detail && detail.temperature != null ? String(detail.temperature) : '—'],
    ];
    return facts.map(function (f) { return '<dt>' + esc(f[0]) + '</dt><dd>' + esc(f[1]) + '</dd>'; }).join('');
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
    if (!isNaN(maxNum) && maxNum !== Number(detail.max_output_tokens || 0)) out.max_output_tokens = maxNum;
    var web = checked('ov-websearch');
    if (web !== !!detail.allow_web_search) out.allow_web_search = web;
    var desc = val('ov-description');
    if (desc !== ((detail.metadata && detail.metadata.description) || '')) out.description = desc;
    return out;
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
          '</form>' + saveBar('prompt');
        document.getElementById('pr-prompt').value = (detail.system_prompt) || '';
        wireDirty('prompt', document.getElementById('promptForm'));
        wireSaveBar('prompt', function () { savePrompt(name); });
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
      body: JSON.stringify(body),
    })
      .then(function (r) {
        return r.json().catch(function () { return {}; }).then(function (data) { return { status: r.status, data: data }; });
      })
      .then(function (res) {
        setSaving(tab, false);
        if (res.status >= 200 && res.status < 300) return onSaved(name, tab);
        if (res.status === 409 && res.data && res.data.error === 'stale_agent_edit') return onStale(name, tab, res.data);
        if (res.status === 409 && res.data && res.data.error === 'shared_agent_edit_requires_confirmation') return onSharedConfirm(name, tab, fields, res.data);
        if (res.status === 409 && res.data && res.data.error === 'entry_agent_removal_blocked') return showStatus(tab, res.data.message || 'Blocked.', 'error');
        // Validation / other errors surface their message inline.
        showStatus(tab, (res.data && res.data.message) || ('Save failed (' + res.status + ').'), 'error');
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
    showBanner(tab,
      'This agent was changed elsewhere since you loaded it. Reload the latest version to continue — your unsaved edits in this tab will be replaced.',
      'Reload latest', function () {
        fetchDetail(name, true).then(function (detail) {
          if (state.selected !== name) return;
          state.dirty[tab] = false;
          if (tab === 'overview') { renderVitals(state.byName[name], detail); renderOverview(name, detail); }
          else if (tab === 'prompt') { els.promptBody.dataset.loadedFor = ''; renderPrompt(name); }
        });
      });
    if (data && data.current_version && state.detailCache[name]) {
      // Keep the cached version in sync so a subsequent explicit reload lines up.
      state.detailCache[name]._staleVersion = data.current_version;
    }
  }

  function onSharedConfirm(name, tab, fields, data) {
    var n = (data && data.workspace_count) || 'multiple';
    var ok = window.confirm('“' + name + '” is attached to ' + n + ' workspaces. This change affects all of them. Apply it?');
    if (ok) submitPatch(name, tab, fields, true);
    else showStatus(tab, 'Save cancelled.', 'muted');
  }

  /* ---- workspaces (read-only in G3) ---------------------------------------- */

  function renderWorkspaces(name, listItem, detail) {
    var members = Array.isArray(listItem.workspaces) ? listItem.workspaces : [];

    // CLI / built-in agents cannot be attached — show the membership read-only.
    if (!isEditable(detail)) {
      els.workspacesBody.innerHTML = members.length === 0
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
        els.workspacesBody.innerHTML = (members.length === 0
          ? '<p class="stage-hint">Not attached to any workspace.</p>'
          : members.map(readonlyWsRow).join('')) +
          '<p class="stage-hint">Could not load the full workspace list to edit assignments.</p>';
      });
  }

  function readonlyWsRow(ws) {
    var nm = esc(ws.name || 'Workspace');
    var link = ws.id ? '<a href="/workspaces/' + encodeURIComponent(ws.id) + '">' + nm + '</a>' : nm;
    var pill = ws.entry_point ? '<span class="ws-entry-pill">Entry agent</span>' : '';
    return '<div class="ws-row"><span>' + link + '</span>' + pill + '</div>';
  }

  function renderWorkspacesEditor(name, members, all) {
    var memberIds = {};
    var entryIds = {};
    members.forEach(function (m) { memberIds[m.id] = true; if (m.entry_point) entryIds[m.id] = true; });

    var rows = all.map(function (ws) {
      var isMember = !!memberIds[ws.id];
      var isEntry = !!entryIds[ws.id];
      // The agent can't be unassigned from a workspace it's the entry agent of;
      // lock that checkbox and explain, matching the server guard.
      var disabled = isEntry ? ' disabled' : '';
      var pill = isEntry ? '<span class="ws-entry-pill">Entry agent</span>' : '';
      return '<label class="ws-check' + (disabled ? ' is-locked' : '') + '">' +
        '<input type="checkbox" data-ws-id="' + esc(ws.id) + '"' + (isMember ? ' checked' : '') + disabled + '>' +
        '<span class="ws-check__name">' + esc(ws.name || ws.id) + '</span>' + pill + '</label>';
    }).join('');

    els.workspacesBody.innerHTML =
      (all.length === 0 ? '<p class="stage-hint">No workspaces exist yet. Create one from the Workspaces page.</p>' : '') +
      '<form class="ws-list" id="workspacesForm">' + rows + '</form>' +
      saveBar('workspaces');

    wireDirty('workspaces', document.getElementById('workspacesForm'));
    wireSaveBar('workspaces', function () { saveWorkspaces(name, members); });
  }

  function saveWorkspaces(name, members) {
    var checks = els.workspacesBody.querySelectorAll('input[data-ws-id]');
    var desired = [];
    checks.forEach(function (c) { if (c.checked) desired.push(c.getAttribute('data-ws-id')); });
    // Entry-agent memberships have disabled (unchecked-proof) boxes but must stay
    // in the desired set so the server doesn't try to remove them.
    members.forEach(function (m) { if (m.entry_point && desired.indexOf(m.id) === -1) desired.push(m.id); });

    setSaving('workspaces', true);
    fetch('/api/agents/' + encodeURIComponent(name) + '/workspaces', {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ workspace_ids: desired }),
    })
      .then(function (r) {
        return r.json().catch(function () { return {}; }).then(function (data) { return { status: r.status, data: data }; });
      })
      .then(function (res) {
        setSaving('workspaces', false);
        if (res.status >= 200 && res.status < 300) {
          state.dirty.workspaces = false;
          // Reflect the reconciled membership everywhere.
          var item = state.byName[name];
          if (item) { item.workspaces = res.data.workspaces || []; item.workspace_count = res.data.workspace_count || 0; }
          refreshRosterMeta(name, state.detailCache[name] || {});
          renderWorkspaces(name, item, state.detailCache[name]);
          showStatus('workspaces', 'Saved.', 'ok');
        } else if (res.status === 409 && res.data && res.data.error === 'entry_agent_removal_blocked') {
          showStatus('workspaces', res.data.message || 'Cannot remove the entry agent.', 'error');
        } else {
          showStatus('workspaces', (res.data && res.data.message) || ('Save failed (' + res.status + ').'), 'error');
        }
      })
      .catch(function () { setSaving('workspaces', false); showStatus('workspaces', 'Network error — not saved.', 'error'); });
  }

  function fetchWorkspaces() {
    if (state.allWorkspaces) return Promise.resolve(state.allWorkspaces);
    return fetch('/api/workspaces')
      .then(function (r) { if (!r.ok) throw new Error('workspaces ' + r.status); return r.json(); })
      .then(function (data) {
        var list = (data && data.workspaces) || [];
        // Only assignable (non-trashed / non-missing) workspaces.
        list = list.filter(function (w) { var s = String(w.status || '').toLowerCase(); return s !== 'trashed' && s !== 'missing'; });
        list.sort(function (a, b) { return String(a.name || '').localeCompare(String(b.name || '')); });
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
      field('Role', selectInput('cr-role', ROLES, 'general', titleCase), 'cr-role') +
      field('Model', textInput('cr-model', 'gpt-4o-mini'), 'cr-model') +
      field('Description', textareaInput('cr-description', '', 3), 'cr-description') +
      '</form>' +
      '<div class="save-bar" id="savebar-create">' +
      '<span class="save-status is-muted"></span>' +
      '<button type="button" class="btn-ghost" id="createCancel2">Cancel</button>' +
      '<button type="button" class="btn-primary" id="createSubmit">Create agent</button>' +
      '</div>';
    document.getElementById('createSubmit').addEventListener('click', submitCreate);
    document.getElementById('createCancel2').addEventListener('click', closeCreate);
    var nameInput = document.getElementById('cr-name');
    if (nameInput) nameInput.focus();
  }

  function closeCreate() {
    state.creating = false;
    els.createPanel.hidden = true;
    if (state.selected) { els.stage.hidden = false; }
    else { els.placeholder.hidden = false; }
  }

  function submitCreate() {
    var name = val('cr-name').trim();
    var status = document.querySelector('#savebar-create .save-status');
    if (!name) { status.textContent = 'Name is required.'; status.className = 'save-status is-error'; return; }
    var body = {
      name: name,
      type: 'tool-calling',
      role: val('cr-role'),
      model: val('cr-model').trim(),
      description: val('cr-description'),
    };
    var submit = document.getElementById('createSubmit');
    submit.disabled = true; submit.textContent = 'Creating…';
    fetch('/api/agents', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) })
      .then(function (r) { return r.json().catch(function () { return {}; }).then(function (d) { return { status: r.status, data: d }; }); })
      .then(function (res) {
        submit.disabled = false; submit.textContent = 'Create agent';
        if (res.status >= 200 && res.status < 300) {
          reloadThenSelect(name);
          closeCreate();
        } else {
          status.textContent = (res.data && res.data.message) || ('Create failed (' + res.status + ').');
          status.className = 'save-status is-error';
        }
      })
      .catch(function () { submit.disabled = false; submit.textContent = 'Create agent'; status.textContent = 'Network error.'; status.className = 'save-status is-error'; });
  }

  // Reload the roster from the server, then select the named agent if present.
  function reloadThenSelect(name) {
    fetch('/api/agents/dashboard/list?sort_by=name&order=asc')
      .then(function (r) { return r.json(); })
      .then(function (data) {
        var agents = Array.isArray(data) ? data : (data && data.agents) || [];
        state.agents = agents;
        state.byName = {};
        agents.forEach(function (a) { state.byName[a.name] = a; });
        state.detailCache = {};
        pruneChecked();
        applyFilterSort();
        if (name && state.byName[name]) selectAgent(name, { push: true });
        else if (state.filtered[0]) selectAgent(state.filtered[0].name, { push: false });
        else { els.stage.hidden = true; els.placeholder.hidden = false; state.selected = null; }
      });
  }

  /* ---- delete agent -------------------------------------------------------- */

  function onDeleteClick() {
    var name = state.selected;
    if (!name) return;
    if (!window.confirm('Delete “' + name + '”? This permanently removes the agent and cannot be undone.')) return;
    deleteAgent(name);
  }

  function deleteAgent(name) {
    fetch('/api/agents?name=' + encodeURIComponent(name), { method: 'DELETE' })
      .then(function (r) { return r.json().catch(function () { return {}; }).then(function (d) { return { status: r.status, data: d }; }); })
      .then(function (res) {
        if (res.status >= 200 && res.status < 300) {
          resetDirty();
          safeStorageSet('');
          reloadThenSelect(null);
        } else {
          // Attached-to-workspace (409) or built-in (400): surface on the stage.
          window.alert((res.data && res.data.message) || ('Delete failed (' + res.status + ').'));
        }
      })
      .catch(function () { window.alert('Network error — agent not deleted.'); });
  }

  function refreshRosterMeta(name, detail) {
    var item = state.byName[name];
    if (!item) return;
    if (detail.model) item.model = detail.model;
    if (detail.metadata) item.metadata = Object.assign({}, item.metadata, { description: (detail.metadata.description || '') });
    var card = els.list.querySelector('.roster-card[data-name="' + cssEscape(name) + '"]');
    if (card) {
      var meta = card.querySelector('.roster-card__meta');
      var wc = item.workspace_count || 0;
      if (meta) meta.textContent = [detail.model, wc === 0 ? 'Library' : wc + ' workspace' + (wc === 1 ? '' : 's')].filter(Boolean).join(' · ');
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
      var next = e.key === 'ArrowRight' ? (idx + 1) % TAB_ORDER.length : (idx - 1 + TAB_ORDER.length) % TAB_ORDER.length;
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
    form.addEventListener('input', function () { markDirty(tab, true); });
    form.addEventListener('change', function () { markDirty(tab, true); });
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
    if (revert) revert.addEventListener('click', function () {
      state.dirty[tab] = false;
      if (tab === 'overview') renderOverview(state.selected, state.detailCache[state.selected]);
      else if (tab === 'prompt') { els.promptBody.dataset.loadedFor = ''; renderPrompt(state.selected); }
      else if (tab === 'workspaces') renderWorkspaces(state.selected, state.byName[state.selected], state.detailCache[state.selected]);
    });
    markDirty(tab, false);
  }

  function setSaving(tab, on) {
    var bar = document.getElementById('savebar-' + tab);
    if (!bar) return;
    var save = bar.querySelector('[data-role="save"]');
    if (save) { save.disabled = on || !state.dirty[tab]; save.textContent = on ? 'Saving…' : 'Save'; }
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
      status._t = window.setTimeout(function () { status.textContent = ''; }, 2600);
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
    btn.addEventListener('click', function () { clearBanner(tab); onAction(); });
    banner.appendChild(btn);
    panel.insertBefore(banner, panel.firstChild);
  }

  function clearBanner(tab) {
    var existing = document.getElementById('banner-' + tab);
    if (existing) existing.remove();
  }

  /* ---- unsaved guard ------------------------------------------------------- */

  function anyDirty() { return !!(state.dirty.overview || state.dirty.prompt || state.dirty.workspaces); }

  function guardUnsaved() {
    if (!anyDirty()) return true;
    var ok = window.confirm('You have unsaved changes that will be lost. Continue?');
    if (ok) resetDirty();
    return ok;
  }

  function resetDirty() { state.dirty.overview = false; state.dirty.prompt = false; state.dirty.workspaces = false; }

  /* ---- roster interaction -------------------------------------------------- */

  // Clicking a card's open button focuses that agent (drives the stage) without
  // touching its checkbox. The checkbox is handled by onListChange so a plain
  // click there only changes bulk-selection state (PRD FR3/FR4).
  function onListClick(e) {
    var open = e.target.closest('.roster-card__open');
    if (open && open.dataset.open) { selectAgent(open.dataset.open); return; }

    // Shift-click on (or near) a checkbox extends a contiguous range from the
    // anchor. The native toggle still fires via onListChange; here we only widen
    // the range when Shift is held.
    var check = e.target.closest('.roster-card__check');
    if (check && e.shiftKey) {
      e.preventDefault();
      var card = check.closest('.roster-card');
      var idx = card ? Number(card.dataset.index) : -1;
      if (idx >= 0) { rangeSelectTo(idx); }
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
      if (e.shiftKey) { rangeSelectTo(i); }
      else {
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
    state.filtered.forEach(function (a) { state.checked.add(a.name); });
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
    state.filtered.forEach(function (a) { visible[a.name] = true; });
    var hidden = 0;
    state.checked.forEach(function (name) { if (!visible[name]) hidden++; });
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
      if (source === 'cli' || role === 'cli_agent') return { eligible: false, reason: 'Built-in CLI agent.' };
      return { eligible: false, reason: 'System assistant.' };
    }
    if ((agent.workspace_count || 0) > 0) {
      return { eligible: false, reason: 'Attached to ' + agent.workspace_count + ' workspace' + (agent.workspace_count === 1 ? '' : 's') + '.' };
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
    var items = rows.map(function (r) {
      var agent = state.byName[r.name];
      var reason = r.reason ? '<span class="bulk-dialog__reason">' + esc(r.reason) + '</span>' : '';
      var wsLink = '';
      if (agent && Array.isArray(agent.workspaces) && agent.workspaces.length) {
        var ws = agent.workspaces[0];
        if (ws && ws.id) wsLink = ' <a class="bulk-dialog__wslink" href="/workspaces/' + encodeURIComponent(ws.id) + '">' + esc(ws.name || 'workspace') + '</a>';
      }
      return '<li><span class="bulk-dialog__agent">' + esc(r.name) + '</span>' + reason + wsLink + '</li>';
    }).join('');
    return '<div class="bulk-dialog__group' + (danger ? ' is-danger' : '') + '">' +
      '<h3 class="bulk-dialog__grouptitle">' + esc(title) + ' (' + rows.length + ')</h3>' +
      '<ul class="bulk-dialog__grouplist">' + items + '</ul></div>';
  }

  function closeBulkDelete() {
    if (els.bulkDeleteDialog.open && typeof els.bulkDeleteDialog.close === 'function') els.bulkDeleteDialog.close();
    else els.bulkDeleteDialog.removeAttribute('open');
    // Return focus to a stable control rather than a possibly-removed card.
    if (els.selectAll) els.selectAll.focus();
  }

  function runBulkDelete() {
    var btn = els.bulkDeleteConfirm;
    if (btn.disabled || btn.dataset.busy === '1') return; // prevent double submit
    var names;
    try { names = JSON.parse(btn.dataset.names || '[]'); } catch (e) { names = []; }
    if (!names.length) { closeBulkDelete(); return; }

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
      body: JSON.stringify({ operation: 'delete', agent_names: names }),
    })
      .then(function (r) { return r.json().catch(function () { return null; }).then(function (d) { return { status: r.status, data: d }; }); })
      .then(function (res) {
        btn.dataset.busy = '';
        btn.textContent = restoreLabel;
        if (res.status < 200 || res.status >= 300 || !res.data || !Array.isArray(res.data.results)) {
          // Do NOT claim any deletion happened on a malformed/error response.
          announce('Bulk delete failed.');
          els.bulkDeleteBody.insertAdjacentHTML('afterbegin',
            '<p class="bulk-dialog__error" role="alert">Delete failed (' + res.status + '). No agents were changed.</p>');
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
        els.bulkDeleteBody.insertAdjacentHTML('afterbegin',
          '<p class="bulk-dialog__error" role="alert">Network error — no agents were deleted.</p>');
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
    var rows = results.filter(function (r) { return r.status !== 'succeeded'; }).map(function (r) {
      var reason = r.message ? esc(r.message) : esc(r.reason_code || r.status);
      return '<li class="bulk-result__item is-' + esc(r.status) + '">' +
        '<span class="bulk-result__name">' + esc(r.name) + '</span>' +
        '<span class="bulk-result__reason">' + reason + '</span></li>';
    }).join('');
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

  function onSearch() { state.query = els.search.value || ''; applyFilterSort(); }
  function onSort() { state.sort = els.sort.value || 'name-asc'; applyFilterSort(); if (state.selected) highlightSelected(); }

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
    return '<input id="' + id + '" type="text" value="' + esc(value) + '"' +
      (placeholder ? ' placeholder="' + esc(placeholder) + '"' : '') + '>';
  }
  // A read-only text input for derived values (e.g. Provider, set by the model).
  // Kept as an <input> so the form still submits its value, but not user-editable.
  function readonlyInput(id, value, title) {
    return '<input id="' + id + '" class="field__readonly" type="text" readonly tabindex="-1" value="' + esc(value) + '"' +
      (title ? ' title="' + esc(title) + '"' : '') + '>';
  }
  function numInput(id, value, min, max, step) {
    return '<input id="' + id + '" type="number" value="' + esc(value === '' ? '' : value) + '"' +
      (min !== '' ? ' min="' + min + '"' : '') + (max ? ' max="' + max + '"' : '') + (step ? ' step="' + step + '"' : '') + '>';
  }
  function checkInput(id, on) {
    return '<label class="check"><input id="' + id + '" type="checkbox"' + (on ? ' checked' : '') + '> Allowed</label>';
  }
  function selectInput(id, options, value, labeler) {
    var opts = options.map(function (o) {
      var label = labeler ? labeler(o) : o;
      return '<option value="' + esc(o) + '"' + (o === value ? ' selected' : '') + '>' + esc(label) + '</option>';
    }).join('');
    return '<select id="' + id + '">' + opts + '</select>';
  }
  function textareaInput(id, value, rows) {
    return '<textarea id="' + id + '" rows="' + rows + '">' + esc(value) + '</textarea>';
  }
  // Grouped <select> of available models (optgroup per provider). Each option
  // carries data-provider so the Provider field can follow the chosen model. A
  // model that isn't in the catalog (custom, or a provider without a key) is
  // preserved under a "Current" group so switching to a picker never drops it.
  function modelSelectInput(id, currentValue, currentProvider, typeFilter) {
    return '<select id="' + id + '">' + modelOptionsHTML(currentValue, currentProvider, typeFilter) + '</select>';
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
      if (opts) groups += '<optgroup label="' + esc(p.display_name || p.name) + '">' + opts + '</optgroup>';
    });
    // Preserve the agent's current model when the type filter excluded it (its real
    // category differs) or it isn't in the catalog at all. Enrich it from the full
    // catalog so its note/warning still show even outside the filtered groups.
    if (currentValue && !matched) {
      var meta = modelMeta(currentValue);
      groups = '<optgroup label="Current">' +
        modelOptionHTML(currentValue, (meta && meta.provider) || currentProvider || '', meta, true) +
        '</optgroup>' + groups;
    }
    return groups;
  }
  // Render one <option> for the model picker from a catalog entry (meta may be null
  // for a truly unknown model). Encodes the data the overview reads back.
  function modelOptionHTML(value, provider, meta, selected) {
    var pricing = meta && meta.pricing ? meta.pricing : '';
    var goodFor = meta && Array.isArray(meta.good_for) && meta.good_for.length ? meta.good_for[0] : '';
    var legacy = !!(meta && meta.is_legacy);
    var dep = meta && meta.deprecation_date ? meta.deprecation_date : '';
    var label = (meta && meta.label) || value;
    return '<option value="' + esc(value) + '"' +
      ' data-provider="' + esc(provider) + '"' +
      (pricing ? ' data-pricing="' + esc(pricing) + '"' : '') +
      (goodFor ? ' data-goodfor="' + esc(goodFor) + '"' : '') +
      (legacy ? ' data-legacy="1"' : '') +
      (dep ? ' data-deprecation="' + esc(dep) + '"' : '') +
      (selected ? ' selected' : '') + '>' +
      esc(label) + (legacy ? ' ⚠' : '') + (pricing ? ' · ' + esc(pricing) : '') +
      '</option>';
  }
  function saveBar(tab) {
    return '<div class="save-bar" id="savebar-' + tab + '">' +
      '<span class="dirty-note"></span>' +
      '<span class="save-status is-muted"></span>' +
      '<button type="button" class="btn-ghost" data-role="revert">Revert</button>' +
      '<button type="button" class="btn-primary" data-role="save" disabled>Save</button>' +
      '</div>';
  }

  /* ---- misc helpers -------------------------------------------------------- */

  function val(id) { var el = document.getElementById(id); return el ? el.value : ''; }
  function checked(id) { var el = document.getElementById(id); return !!(el && el.checked); }

  function vital(label, value) {
    return '<span class="vital"><span>' + esc(label) + '</span><b>' + esc(value) + '</b></span>';
  }

  function avatarMarkup(agent, className, id) {
    var idAttr = id ? ' id="' + id + '"' : '';
    var image = agent && agent.metadata && agent.metadata.avatar_image;
    if (image) {
      return '<div class="' + className + '"' + idAttr + ' style="padding:0;overflow:hidden;">' +
        '<img src="/avatars/' + esc(String(image)) + '" alt="" loading="lazy" decoding="async" style="width:100%;height:100%;object-fit:cover;"></div>';
    }
    var color = (agent && agent.metadata && agent.metadata.avatar_color) || colorFor(agent ? agent.name : '');
    return '<div class="' + className + '"' + idAttr + ' style="background:' + esc(color) + ';">' + esc(initials(agent ? agent.name : '')) + '</div>';
  }

  function initials(name) {
    var parts = String(name || '?').trim().split(/\s+/);
    if (parts.length === 1) return parts[0].slice(0, 2).toUpperCase();
    return (parts[0][0] + parts[parts.length - 1][0]).toUpperCase();
  }

  function colorFor(name) {
    // 700-level shades so white initials clear WCAG AA contrast (>= 4.5:1).
    var palette = ['#4338ca', '#0e7490', '#6d28d9', '#047857', '#b45309', '#be185d', '#1d4ed8', '#b91c1c'];
    var sum = 0;
    String(name || '').split('').forEach(function (c) { sum += c.charCodeAt(0); });
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
    var name = String(agent.name || '').trim().toLowerCase();
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

  function titleCase(s) {
    s = String(s || '').replace(/[_-]+/g, ' ').trim();
    if (!s) return '';
    return s.replace(/\w\S*/g, function (w) { return w.charAt(0).toUpperCase() + w.slice(1); });
  }

  function syncUrl(name, push) {
    var params = new URLSearchParams(window.location.search);
    params.delete('view'); // roster is the default Agents page now
    params.set('agent', name);
    var url = window.location.pathname + '?' + params.toString();
    if (push) window.history.pushState({ agent: name }, '', url);
    else window.history.replaceState({ agent: name }, '', url);
  }

  function safeStorageGet() { try { return window.localStorage.getItem(STORAGE_KEY); } catch (e) { return null; } }
  function safeStorageSet(v) { try { window.localStorage.setItem(STORAGE_KEY, v); } catch (e) { /* ignore */ } }

  function cssEscape(s) {
    if (window.CSS && window.CSS.escape) return window.CSS.escape(s);
    return String(s).replace(/"/g, '\\"');
  }

  function esc(s) {
    return String(s == null ? '' : s)
      .replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;').replace(/'/g, '&#39;');
  }
})();
