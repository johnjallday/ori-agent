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
  };

  var els = {};

  document.addEventListener('DOMContentLoaded', init);

  function init() {
    els = {
      list: document.getElementById('rosterList'),
      count: document.getElementById('rosterCount'),
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
      newAgentBtn: document.getElementById('newAgentBtn'),
      createPanel: document.getElementById('createPanel'),
      createBody: document.getElementById('createBody'),
      createCancel: document.getElementById('createCancel'),
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
    els.newAgentBtn.addEventListener('click', openCreate);
    els.createCancel.addEventListener('click', closeCreate);
    els.stageDelete.addEventListener('click', onDeleteClick);

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

    loadAgents();
  }

  /* ---- data ---------------------------------------------------------------- */

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
        applyFilterSort();
        restoreSelection();
      })
      .catch(function (err) {
        els.count.textContent = 'Could not load agents.';
        console.error('[roster] load failed', err);
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

    if (shown === 0) {
      els.empty.hidden = false;
      els.count.textContent = total === 0 ? 'No agents yet.' : '0 of ' + total + ' agents';
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
  }

  function buildCard(agent, idx) {
    var li = document.createElement('li');
    li.className = 'roster-card';
    li.setAttribute('role', 'option');
    li.id = 'roster-opt-' + idx;
    li.dataset.name = agent.name;
    li.setAttribute('aria-selected', 'false');

    var status = healthKind(agent);
    var metaBits = [];
    if (agent.model) metaBits.push(agent.model);
    var wc = agent.workspace_count || 0;
    metaBits.push(wc === 0 ? 'Library' : wc + ' workspace' + (wc === 1 ? '' : 's'));

    li.innerHTML =
      avatarMarkup(agent, 'roster-card__avatar') +
      '<div class="roster-card__body">' +
      '<p class="roster-card__name">' + esc(agent.name) + '</p>' +
      '<p class="roster-card__meta">' + esc(metaBits.join(' · ')) + '</p>' +
      '</div>' +
      '<span class="roster-card__status is-' + status + '" title="' + esc(status) + '"></span>';
    return li;
  }

  function highlightSelected() {
    var options = els.list.querySelectorAll('.roster-card');
    options.forEach(function (opt) {
      var isSel = opt.dataset.name === state.selected;
      opt.setAttribute('aria-selected', isSel ? 'true' : 'false');
      if (isSel) els.list.setAttribute('aria-activedescendant', opt.id);
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
    els.vitals.innerHTML = vitals.join('');
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
      els.overviewFacts.className = 'stage-facts';
      els.overviewFacts.innerHTML = readonlyFacts(detail);
      els.overviewDesc.innerHTML = '<p class="stage-hint">This is a built-in agent and cannot be edited here.</p>';
      return;
    }

    // The editable form manages its own layout, so drop the read-only facts grid.
    els.overviewFacts.className = 'stage-editwrap';
    els.overviewFacts.innerHTML =
      '<form class="stage-form" id="overviewForm" novalidate>' +
      field('Role', selectInput('ov-role', ROLES, detail.role, titleCase)) +
      field('Model', textInput('ov-model', detail.model || '')) +
      field('Provider', textInput('ov-provider', detail.provider || '', 'openai / anthropic / ollama…')) +
      field('Temperature', numInput('ov-temperature', detail.temperature, '0', '2', '0.1')) +
      field('Reasoning effort', selectInput('ov-reasoning', REASONING, detail.reasoning_effort || '', function (v) { return v ? titleCase(v) : 'Default'; })) +
      field('Max output tokens', numInput('ov-maxtokens', detail.max_output_tokens || '', '0', '', '1')) +
      field('Web search', checkInput('ov-websearch', detail.allow_web_search)) +
      field('Description', textareaInput('ov-description', (detail.metadata && detail.metadata.description) || '', 3)) +
      '</form>' +
      saveBar('overview');

    els.overviewDesc.innerHTML = '';
    wireDirty('overview', document.getElementById('overviewForm'));
    wireSaveBar('overview', function () { saveOverview(name); });
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
      field('Name', textInput('cr-name', '', 'Unique agent name')) +
      field('Role', selectInput('cr-role', ROLES, 'general', titleCase)) +
      field('Model', textInput('cr-model', 'gpt-4o-mini')) +
      field('Description', textareaInput('cr-description', '', 3)) +
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

  function onListClick(e) {
    var card = e.target.closest('.roster-card');
    if (card && card.dataset.name) selectAgent(card.dataset.name);
  }

  function onListKeydown(e) {
    var n = state.filtered.length;
    if (n === 0) return;
    var idx = state.focusIndex < 0 ? 0 : state.focusIndex;
    if (e.key === 'ArrowDown') { e.preventDefault(); selectByIndex(Math.min(idx + 1, n - 1)); }
    else if (e.key === 'ArrowUp') { e.preventDefault(); selectByIndex(Math.max(idx - 1, 0)); }
    else if (e.key === 'Home') { e.preventDefault(); selectByIndex(0); }
    else if (e.key === 'End') { e.preventDefault(); selectByIndex(n - 1); }
  }

  function selectByIndex(i) {
    var agent = state.filtered[i];
    if (!agent) return;
    selectAgent(agent.name);
    var opt = els.list.querySelector('.roster-card[data-name="' + cssEscape(agent.name) + '"]');
    if (opt && opt.scrollIntoView) opt.scrollIntoView({ block: 'nearest' });
  }

  function onSearch() { state.query = els.search.value || ''; applyFilterSort(); }
  function onSort() { state.sort = els.sort.value || 'name-asc'; applyFilterSort(); if (state.selected) highlightSelected(); }

  /* ---- form field builders ------------------------------------------------- */

  function field(label, control) {
    return '<div class="field"><label class="field__label">' + esc(label) + '</label>' +
      '<div class="field__control">' + control + '</div></div>';
  }
  function textInput(id, value, placeholder) {
    return '<input id="' + id + '" type="text" value="' + esc(value) + '"' +
      (placeholder ? ' placeholder="' + esc(placeholder) + '"' : '') + '>';
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
    var palette = ['#4f46e5', '#0891b2', '#7c3aed', '#059669', '#d97706', '#db2777', '#2563eb', '#dc2626'];
    var sum = 0;
    String(name || '').split('').forEach(function (c) { sum += c.charCodeAt(0); });
    return palette[sum % palette.length];
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
    params.set('view', 'roster');
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
