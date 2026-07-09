/*
 * Agents roster + stage controller (game-inspired Agents page, G2).
 *
 * Read-only shell: browse the roster, select an agent, inspect its vitals and
 * three tabs (Overview / Prompt / Workspaces). Selection persists across
 * reloads (URL ?agent= + localStorage). The system prompt is loaded lazily —
 * its DOM is only built the first time the Prompt tab is opened for an agent.
 *
 * Editing (Overview/Prompt saves, workspace assignment, create/delete) arrives
 * in later groups; this file deliberately renders everything read-only.
 */
(function () {
  'use strict';

  var STORAGE_KEY = 'ori.roster.selectedAgent';
  var TAB_ORDER = ['overview', 'prompt', 'workspaces'];

  var state = {
    agents: [],
    filtered: [],
    byName: {},
    selected: null,
    detailCache: {},
    query: '',
    sort: 'name-asc',
    focusIndex: -1,
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

    var tabs = document.querySelectorAll('.stage__tab');
    tabs.forEach(function (tab) {
      tab.addEventListener('click', function () { activateTab(tab.dataset.tab, true); });
      tab.addEventListener('keydown', onTabKeydown);
    });

    window.addEventListener('popstate', function () {
      var name = new URLSearchParams(window.location.search).get('agent');
      if (name && state.byName[name]) selectAgent(name, { push: false });
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

  function fetchDetail(name) {
    if (state.detailCache[name]) return Promise.resolve(state.detailCache[name]);
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
    state.selected = name;
    state.focusIndex = state.filtered.findIndex(function (a) { return a.name === name; });
    safeStorageSet(name);
    syncUrl(name, opts.push !== false);
    highlightSelected();
    renderStage(name);
  }

  /* ---- stage --------------------------------------------------------------- */

  function renderStage(name) {
    var listItem = state.byName[name];
    els.placeholder.hidden = true;
    els.stage.hidden = false;

    // Reset to Overview and clear the lazy Prompt tab for the new agent.
    activateTab('overview', false);
    els.promptBody.dataset.loadedFor = '';
    els.promptBody.innerHTML = '<p class="stage-hint">Loading system prompt…</p>';

    // Hero + roster-derived vitals render immediately from the list item.
    els.avatar.outerHTML = avatarMarkup(listItem, 'stage__avatar', 'stageAvatar');
    els.avatar = document.getElementById('stageAvatar');
    els.name.textContent = listItem.name;
    els.klass.textContent = titleCase(listItem.role || listItem.type || 'agent');

    renderWorkspaces(listItem);

    // Detail (model config) fills Overview; may fail gracefully.
    els.overviewFacts.innerHTML = '<p class="stage-hint">Loading…</p>';
    els.overviewDesc.textContent = '';
    fetchDetail(name)
      .then(function (detail) {
        if (state.selected !== name) return; // selection moved on
        renderVitals(listItem, detail);
        renderOverview(listItem, detail);
      })
      .catch(function (err) {
        if (state.selected !== name) return;
        renderVitals(listItem, null);
        els.overviewFacts.innerHTML = '<p class="stage-hint">Could not load agent details.</p>';
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

  function renderOverview(listItem, detail) {
    var facts = [
      ['Role', titleCase(listItem.role || '—')],
      ['Type', titleCase(listItem.type || '—')],
      ['Model', detail.model || '—'],
      ['Provider', detail.provider ? titleCase(detail.provider) : '—'],
      ['Temperature', detail.temperature != null ? String(detail.temperature) : '—'],
    ];
    if (detail.reasoning_effort) facts.push(['Reasoning effort', titleCase(detail.reasoning_effort)]);
    if (detail.max_output_tokens) facts.push(['Max output tokens', String(detail.max_output_tokens)]);
    facts.push(['Web search', detail.allow_web_search ? 'Allowed' : 'Off']);

    els.overviewFacts.innerHTML = facts.map(function (f) {
      return '<dt>' + esc(f[0]) + '</dt><dd>' + esc(f[1]) + '</dd>';
    }).join('');

    var desc = (listItem.metadata && listItem.metadata.description) || '';
    els.overviewDesc.textContent = desc || 'No description written yet.';
  }

  function renderPrompt(name) {
    if (els.promptBody.dataset.loadedFor === name) return;
    els.promptBody.dataset.loadedFor = name;
    els.promptBody.innerHTML = '<p class="stage-hint">Loading system prompt…</p>';
    fetchDetail(name)
      .then(function (detail) {
        if (state.selected !== name) return;
        var prompt = (detail && detail.system_prompt) || '';
        if (!prompt.trim()) {
          els.promptBody.innerHTML = '<p class="stage-hint">This agent has no custom system prompt.</p>';
          return;
        }
        var pre = document.createElement('pre');
        pre.textContent = prompt;
        els.promptBody.innerHTML = '';
        els.promptBody.appendChild(pre);
      })
      .catch(function () {
        els.promptBody.innerHTML = '<p class="stage-hint">Could not load the system prompt.</p>';
      });
  }

  function renderWorkspaces(listItem) {
    var workspaces = Array.isArray(listItem.workspaces) ? listItem.workspaces : [];
    if (workspaces.length === 0) {
      els.workspacesBody.innerHTML = '<p class="stage-hint">Not attached to any workspace — this is a reusable library agent.</p>';
      return;
    }
    els.workspacesBody.innerHTML = workspaces.map(function (ws) {
      var name = esc(ws.name || 'Workspace');
      var link = ws.id ? '<a href="/workspaces/' + encodeURIComponent(ws.id) + '">' + name + '</a>' : name;
      var pill = ws.entry_point ? '<span class="ws-entry-pill">Entry agent</span>' : '';
      return '<div class="ws-row"><span>' + link + '</span>' + pill + '</div>';
    }).join('');
  }

  /* ---- tabs ---------------------------------------------------------------- */

  function activateTab(tabName, focus) {
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
    if (tabName === 'prompt' && state.selected) renderPrompt(state.selected);
    if (focus) document.getElementById('tab-' + tabName).focus();
  }

  function onTabKeydown(e) {
    var current = e.currentTarget.dataset.tab;
    var idx = TAB_ORDER.indexOf(current);
    if (e.key === 'ArrowRight' || e.key === 'ArrowLeft') {
      e.preventDefault();
      var next = e.key === 'ArrowRight' ? (idx + 1) % TAB_ORDER.length : (idx - 1 + TAB_ORDER.length) % TAB_ORDER.length;
      activateTab(TAB_ORDER[next], true);
    } else if (e.key === 'Home') {
      e.preventDefault();
      activateTab(TAB_ORDER[0], true);
    } else if (e.key === 'End') {
      e.preventDefault();
      activateTab(TAB_ORDER[TAB_ORDER.length - 1], true);
    }
  }

  /* ---- roster interaction -------------------------------------------------- */

  function onListClick(e) {
    var card = e.target.closest('.roster-card');
    if (card && card.dataset.name) selectAgent(card.dataset.name);
  }

  function onListKeydown(e) {
    var n = state.filtered.length;
    if (n === 0) return;
    var idx = state.focusIndex < 0 ? 0 : state.focusIndex;
    if (e.key === 'ArrowDown') {
      e.preventDefault();
      selectByIndex(Math.min(idx + 1, n - 1));
    } else if (e.key === 'ArrowUp') {
      e.preventDefault();
      selectByIndex(Math.max(idx - 1, 0));
    } else if (e.key === 'Home') {
      e.preventDefault();
      selectByIndex(0);
    } else if (e.key === 'End') {
      e.preventDefault();
      selectByIndex(n - 1);
    }
  }

  function selectByIndex(i) {
    var agent = state.filtered[i];
    if (!agent) return;
    selectAgent(agent.name);
    var opt = els.list.querySelector('.roster-card[data-name="' + cssEscape(agent.name) + '"]');
    if (opt && opt.scrollIntoView) opt.scrollIntoView({ block: 'nearest' });
  }

  function onSearch() {
    state.query = els.search.value || '';
    applyFilterSort();
  }

  function onSort() {
    state.sort = els.sort.value || 'name-asc';
    applyFilterSort();
    if (state.selected) highlightSelected();
  }

  /* ---- helpers ------------------------------------------------------------- */

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

  function safeStorageGet() {
    try { return window.localStorage.getItem(STORAGE_KEY); } catch (e) { return null; }
  }
  function safeStorageSet(v) {
    try { window.localStorage.setItem(STORAGE_KEY, v); } catch (e) { /* ignore */ }
  }

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
