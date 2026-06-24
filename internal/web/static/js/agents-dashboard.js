// Operations-first Agents Dashboard

let dashboardAgents = [];
let dashboardFilteredAgents = [];
let dashboardMode = 'operations';
let selectedAgentName = '';
let selectedAgentDetail = null;
let selectedAgentSkills = null;
let lastFocusedElement = null;

const absoluteDateFormatter = new Intl.DateTimeFormat(undefined, {
  month: 'short',
  day: 'numeric',
  year: 'numeric'
});

const safeEscapeHtml = typeof escapeHtml === 'function'
  ? escapeHtml
  : (value) => {
    const div = document.createElement('div');
    div.textContent = String(value ?? '');
    return div.innerHTML;
  };

function notifyError(message) {
  if (window.Toast && typeof Toast.error === 'function') {
    Toast.error(message);
    return;
  }
  alert(message);
}

function notifySuccess(message) {
  if (window.Toast && typeof Toast.success === 'function') {
    Toast.success(message);
    return;
  }
  alert(message);
}

function initializeDashboard() {
  setupEventListeners();
  applyDashboardStateFromUrl();
  loadAgents();
}

function setupEventListeners() {
  const searchInput = document.getElementById('searchInput');
  const modeOperationsBtn = document.getElementById('modeOperationsBtn');
  const modeConfigBtn = document.getElementById('modeConfigBtn');
  const closeDrawerBtn = document.getElementById('closeDrawerBtn');
  const drawerBackdrop = document.getElementById('agentDrawerBackdrop');
  const emptyStateClearBtn = document.getElementById('emptyStateClearBtn');

  if (searchInput) {
    const handleSearchChange = () => {
      syncDashboardStateToUrl();
      applyFiltersAndRender();
    };

    searchInput.addEventListener('input', handleSearchChange);
    searchInput.addEventListener('search', handleSearchChange);
  }

  if (modeOperationsBtn) {
    modeOperationsBtn.addEventListener('click', () => setMode('operations'));
  }

  if (modeConfigBtn) {
    modeConfigBtn.addEventListener('click', () => setMode('config'));
  }

  if (closeDrawerBtn) {
    closeDrawerBtn.addEventListener('click', closeAgentDrawer);
  }

  if (drawerBackdrop) {
    drawerBackdrop.addEventListener('click', closeAgentDrawer);
  }

  if (emptyStateClearBtn) {
    emptyStateClearBtn.addEventListener('click', clearDashboardSearch);
  }

  document.querySelectorAll('.ops-drawer-tab').forEach((button) => {
    button.addEventListener('click', () => {
      const tab = button.dataset.tab || 'overview';
      setDrawerTab(tab, { focusPanel: true });
    });

    button.addEventListener('keydown', handleDrawerTabKeydown);
  });

  document.addEventListener('keydown', handleGlobalKeydown);
  window.addEventListener('popstate', () => {
    applyDashboardStateFromUrl();
    applyFiltersAndRender();
  });
}

function applyDashboardStateFromUrl() {
  const params = new URLSearchParams(window.location.search);
  const searchInput = document.getElementById('searchInput');
  const searchTerm = params.get('q') || '';
  const mode = params.get('mode') || 'operations';

  if (searchInput && searchInput.value !== searchTerm) {
    searchInput.value = searchTerm;
  }

  setMode(mode, { syncUrl: false });
}

function syncDashboardStateToUrl() {
  const params = new URLSearchParams(window.location.search);
  const searchTerm = String(document.getElementById('searchInput')?.value || '').trim();

  if (searchTerm) {
    params.set('q', searchTerm);
  } else {
    params.delete('q');
  }

  if (dashboardMode !== 'operations') {
    params.set('mode', dashboardMode);
  } else {
    params.delete('mode');
  }

  const query = params.toString();
  const nextUrl = query ? `${window.location.pathname}?${query}` : window.location.pathname;
  window.history.replaceState(null, '', nextUrl);
}

function setMode(mode, options = {}) {
  const { syncUrl = true } = options;
  dashboardMode = mode === 'config' ? 'config' : 'operations';

  const shell = document.querySelector('.ops-shell');
  if (shell) {
    shell.classList.toggle('config-mode', dashboardMode === 'config');
  }

  const modeOperationsBtn = document.getElementById('modeOperationsBtn');
  const modeConfigBtn = document.getElementById('modeConfigBtn');

  if (modeOperationsBtn) {
    modeOperationsBtn.classList.toggle('active', dashboardMode === 'operations');
    modeOperationsBtn.setAttribute('aria-pressed', String(dashboardMode === 'operations'));
  }

  if (modeConfigBtn) {
    modeConfigBtn.classList.toggle('active', dashboardMode === 'config');
    modeConfigBtn.setAttribute('aria-pressed', String(dashboardMode === 'config'));
  }

  if (syncUrl) {
    syncDashboardStateToUrl();
  }
}

async function loadAgents() {
  showLoading(true);

  try {
    const response = await fetch('/api/agents/dashboard/list?sort_by=name&order=asc');

    if (!response.ok) {
      throw new Error('Failed to load agents');
    }

    const data = await response.json();
    dashboardAgents = Array.isArray(data.agents) ? data.agents : [];
    applyFiltersAndRender();
  } catch (error) {
    console.error('Failed to load dashboard agents:', error);
    notifyError(error.message || 'Failed to load agents');
    dashboardAgents = [];
    dashboardFilteredAgents = [];
    showEmptyState();
  } finally {
    showLoading(false);
  }
}

function applyFiltersAndRender() {
  const rawSearchTerm = String(document.getElementById('searchInput')?.value || '').trim();
  const searchTerm = rawSearchTerm.toLowerCase();

  dashboardFilteredAgents = dashboardAgents.filter((agent) => {
    // Hide workspace-scoped entry agents from the top-level agents list —
    // they're managed from their workspace detail page.
    if (String(agent?.scope || '').toLowerCase() === 'workspace') {
      return false;
    }
    const name = String(agent?.name || '').toLowerCase();
    const description = String(agent?.metadata?.description || '').toLowerCase();
    return name.includes(searchTerm) || description.includes(searchTerm);
  });

  if (dashboardFilteredAgents.length === 0) {
    renderSummary({ needsAttention: 0, ready: 0, paused: 0, total: 0 });
    renderHealthMessage(0, 0);
    showEmptyState({
      searchTerm: rawSearchTerm,
      hasAgents: dashboardAgents.length > 0
    });
    return;
  }

  const buckets = createBuckets(dashboardFilteredAgents);
  renderSummary({
    needsAttention: buckets.needsAttention.length,
    ready: buckets.ready.length,
    paused: buckets.paused.length,
    total: dashboardFilteredAgents.length
  });

  renderHealthMessage(buckets.needsAttention.length, dashboardFilteredAgents.length);
  renderBucket('bucketNeedsAttention', buckets.needsAttention, 'No issues detected');
  renderBucket('bucketReady', buckets.ready, 'No ready agents');
  renderBucket('bucketPaused', buckets.paused, 'No paused agents');
  updateBucketCounters(buckets);
  showBoard();
}

function createBuckets(agents) {
  const buckets = {
    needsAttention: [],
    ready: [],
    paused: []
  };

  const sortedAgents = [...agents].sort((a, b) => {
    const aName = String(a?.name || '').toLowerCase();
    const bName = String(b?.name || '').toLowerCase();
    return aName.localeCompare(bName);
  });

  sortedAgents.forEach((agent) => {
    const health = getHealthState(agent);

    if (health.kind === 'error' || health.kind === 'needs-setup') {
      buckets.needsAttention.push(agent);
      return;
    }

    if (health.kind === 'paused') {
      buckets.paused.push(agent);
      return;
    }

    buckets.ready.push(agent);
  });

  return buckets;
}

function renderSummary(summary) {
  const countNeedsAttention = document.getElementById('countNeedsAttention');
  const countReady = document.getElementById('countReady');
  const countPaused = document.getElementById('countPaused');
  const countTotal = document.getElementById('countTotal');

  if (countNeedsAttention) {
    countNeedsAttention.textContent = String(summary.needsAttention);
  }

  if (countReady) {
    countReady.textContent = String(summary.ready);
  }

  if (countPaused) {
    countPaused.textContent = String(summary.paused);
  }

  if (countTotal) {
    countTotal.textContent = String(summary.total);
  }
}

function renderHealthMessage(needsAttentionCount, totalCount) {
  const healthMessage = document.getElementById('healthMessage');
  if (!healthMessage) {
    return;
  }

  if (totalCount === 0) {
    healthMessage.classList.add('hidden');
    healthMessage.classList.remove('warn');
    healthMessage.textContent = '';
    return;
  }

  healthMessage.classList.remove('hidden');

  if (needsAttentionCount > 0) {
    healthMessage.classList.add('warn');
    healthMessage.textContent = `${needsAttentionCount} agent${needsAttentionCount === 1 ? '' : 's'} need attention before reliable chats.`;
    return;
  }

  healthMessage.classList.remove('warn');
  healthMessage.textContent = 'All agents look healthy and ready to chat.';
}

function updateBucketCounters(buckets) {
  const map = [
    ['bucketNeedsAttentionCount', buckets.needsAttention.length],
    ['bucketReadyCount', buckets.ready.length],
    ['bucketPausedCount', buckets.paused.length]
  ];

  map.forEach(([id, value]) => {
    const element = document.getElementById(id);
    if (element) {
      element.textContent = String(value);
    }
  });
}

function renderBucket(containerId, agents, emptyMessage) {
  const container = document.getElementById(containerId);
  if (!container) {
    return;
  }

  container.innerHTML = '';

  if (!Array.isArray(agents) || agents.length === 0) {
    container.innerHTML = `<div class="ops-empty-column">${safeEscapeHtml(emptyMessage)}</div>`;
    return;
  }

  agents.forEach((agent) => {
    const card = createAgentCard(agent);
    container.appendChild(card);
  });
}

function createAgentCard(agent) {
  const card = document.createElement('article');
  card.className = 'ops-agent-card';

  const name = String(agent?.name || 'Untitled Agent');
  const description = String(agent?.metadata?.description || 'No purpose written yet.');
  const model = String(agent?.model || '').trim();
  const pluginsCount = Array.isArray(agent?.enabled_plugins) ? agent.enabled_plugins.length : 0;
  const typeLabel = toTitleCase(String(agent?.type || 'tool-calling'));
  const health = getHealthState(agent);
  const isPaused = health.kind === 'paused';
  const chatDisabled = health.kind === 'needs-setup';
  const primaryAction = chatDisabled ? 'setup' : (isPaused ? 'paused' : 'chat');
  const primaryLabel = chatDisabled ? 'Setup' : (isPaused ? 'Paused' : 'Chat');
  const pauseLabel = health.kind === 'paused' ? 'Resume' : 'Pause';
  const primaryDisabledAttr = isPaused
    ? 'disabled title="Resume this agent before starting a chat."'
    : '';
  const isSystemAgent = isSystemAssistantAgentName(name);
  const deleteDisabledAttr = isSystemAgent
    ? 'disabled title="System assistant cannot be deleted."'
    : '';

  card.innerHTML = `
    <div class="ops-card-top">
      ${getAvatarHtml(agent, 'ops-agent-avatar')}
      <div class="ops-agent-main">
        <div class="ops-agent-name-row">
          <h4 class="ops-agent-name" title="${safeEscapeHtml(name)}">${safeEscapeHtml(name)}</h4>
          <span class="ops-health-pill ${safeEscapeHtml(health.kind)}">${safeEscapeHtml(health.label)}</span>
        </div>
        <p class="ops-agent-purpose" title="${safeEscapeHtml(description)}">${safeEscapeHtml(description)}</p>
        <div class="ops-agent-time">Last active: ${safeEscapeHtml(formatDate(agent?.statistics?.last_active || ''))}</div>
      </div>
    </div>
    <div class="ops-card-actions">
      <button class="ops-action-btn primary" data-action="primary" type="button" ${primaryDisabledAttr}>${safeEscapeHtml(primaryLabel)}</button>
      <button class="ops-action-btn" data-action="details" type="button" aria-haspopup="dialog" aria-controls="agentDrawer">Details</button>
      <button class="ops-action-btn" data-action="pause" type="button">${safeEscapeHtml(pauseLabel)}</button>
      <button class="ops-action-btn danger" data-action="delete" ${deleteDisabledAttr}>Delete</button>
    </div>
    <div class="config-only">
      <div>Type: ${safeEscapeHtml(typeLabel)}</div>
      <div>Model: ${safeEscapeHtml(model || 'Not configured')}</div>
      <div>Tools: ${pluginsCount} plugins</div>
      <div>Total cost: $${Number(agent?.statistics?.total_cost || 0).toFixed(2)}</div>
    </div>
  `;

  const primaryButton = card.querySelector('[data-action="primary"]');
  const detailsButton = card.querySelector('[data-action="details"]');
  const pauseButton = card.querySelector('[data-action="pause"]');
  const deleteButton = card.querySelector('[data-action="delete"]');

  if (primaryButton) {
    primaryButton.setAttribute(
      'aria-label',
      primaryAction === 'setup' ? `Set up ${name}` : `Chat with ${name}`
    );
    primaryButton.addEventListener('click', async () => {
      if (primaryAction === 'setup') {
        openAgentEditor(name);
        return;
      }
      if (primaryAction === 'paused') {
        notifyError(`Agent "${name}" is paused. Resume it before starting a chat.`);
        return;
      }

      await openChatWithAgent(name, primaryButton);
    });
  }

  if (detailsButton) {
    detailsButton.setAttribute('aria-label', `Open details for ${name}`);
    detailsButton.addEventListener('click', () => {
      openAgentDrawer(name);
    });
  }

  if (pauseButton) {
    pauseButton.addEventListener('click', async () => {
      await togglePauseAgent(name, pauseButton);
    });
  }

  if (deleteButton) {
    deleteButton.setAttribute('type', 'button');
    deleteButton.addEventListener('click', async () => {
      if (isSystemAgent) {
        notifyError('System assistant cannot be deleted.');
        return;
      }
      await deleteAgentFromDashboard(name, deleteButton);
    });
  }

  return card;
}

function openAgentEditor(agentName) {
  window.location.href = `/agents/${encodeURIComponent(agentName)}`;
}

async function openChatWithAgent(agentName, button) {
  const originalText = button?.textContent || 'Chat';
  if (button) {
    button.disabled = true;
    button.textContent = 'Opening…';
  }

  try {
    const agent = dashboardAgents.find((item) => String(item?.name || '') === agentName);
    if (String(agent?.status || '') === 'disabled') {
      notifyError(`Agent "${agentName}" is paused. Resume it before starting a chat.`);
      return;
    }

    await showChatSessionModalForAgent(agentName);
  } catch (error) {
    console.error('Failed to open chat with agent:', error);
    notifyError(error.message || `Failed to open chat with ${agentName}`);
  } finally {
    if (button && button.isConnected) {
      button.disabled = false;
      button.textContent = originalText;
    }
  }
}

async function showChatSessionModalForAgent(agentName) {
  const manager = window.sessionManager;
  if (!manager || typeof manager.showCreateChatModal !== 'function') {
    throw new Error('Chat session modal is unavailable on this page.');
  }

  await manager.showCreateChatModal();

  const agentSelect = document.getElementById('chatAgentSelect');
  if (agentSelect) {
    const matchedOption = Array.from(agentSelect.options || []).find(
      (option) => String(option.value || '') === agentName
    );
    if (!matchedOption) {
      throw new Error(`Agent "${agentName}" is unavailable for new chat sessions.`);
    }
    agentSelect.value = matchedOption.value;
    agentSelect.dispatchEvent(new Event('change', { bubbles: true }));

    // Update auto mode info text to show the pre-selected agent
    const autoModeText = document.getElementById('chatAutoModeText');
    if (autoModeText) {
      autoModeText.textContent = `Chatting with ${agentName}. The AI will automatically select the best workspace after a few messages.`;
    }
  }

  const targetInput = manager.chatAutoMode
    ? document.getElementById('chatAutoMessage')
    : document.getElementById('chatManualMessage');
  if (targetInput) {
    requestAnimationFrame(() => {
      targetInput.focus();
      const valueLength = targetInput.value.length;
      targetInput.setSelectionRange(valueLength, valueLength);
    });
  }
}

async function togglePauseAgent(agentName, button) {
  const agent = dashboardAgents.find((item) => String(item?.name || '') === agentName);
  if (!agent) {
    return;
  }

  const currentlyPaused = String(agent?.status || '') === 'disabled';
  const nextStatus = currentlyPaused ? 'active' : 'disabled';
  const originalText = button.textContent;

  button.disabled = true;
  button.textContent = currentlyPaused ? 'Resuming…' : 'Pausing…';

  try {
    await updateAgentStatus(agentName, nextStatus);
    notifySuccess(`Agent "${agentName}" ${currentlyPaused ? 'resumed' : 'paused'}.`);
    await loadAgents();

    if (selectedAgentName === agentName) {
      await openAgentDrawer(agentName);
    }
  } catch (error) {
    console.error('Failed to toggle pause status:', error);
    notifyError(error.message || `Failed to update ${agentName}`);
    button.disabled = false;
    button.textContent = originalText;
  }
}

async function updateAgentStatus(agentName, status) {
  const endpoint = `/api/agents/${encodeURIComponent(agentName)}/status`;
  const response = await fetch(endpoint, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ status })
  });

  if (response.ok) {
    return;
  }

  const errorBody = await response.json().catch(() => null);
  throw new Error(errorBody?.error || `Status update failed (${response.status})`);
}

async function setCurrentAgent(agentName) {
  const endpoint = `/api/agents?name=${encodeURIComponent(agentName)}`;
  const response = await fetch(endpoint, { method: 'PUT' });

  if (response.ok) {
    return;
  }

  const errorBody = await response.json().catch(() => null);
  throw new Error(errorBody?.error || `Failed to activate ${agentName}`);
}

async function deleteAgentFromDashboard(agentName, button) {
  const normalizedName = String(agentName || '').trim();
  if (!normalizedName) {
    return;
  }

  if (!confirm(`Are you sure you want to delete agent "${normalizedName}"? This action cannot be undone.`)) {
    return;
  }

  const originalText = button?.textContent || 'Delete';
  if (button) {
    button.disabled = true;
    button.textContent = 'Deleting…';
  }

  try {
    const endpoint = `/api/agents?name=${encodeURIComponent(normalizedName)}`;
    const response = await fetch(endpoint, { method: 'DELETE' });
    if (!response.ok) {
      const errorBody = await response.json().catch(() => null);
      throw new Error(errorBody?.error || `Failed to delete agent (${response.status})`);
    }

    if (selectedAgentName === normalizedName) {
      selectedAgentName = '';
      selectedAgentDetail = null;
      selectedAgentSkills = null;
      closeAgentDrawer();
    }

    notifySuccess(`Agent "${normalizedName}" deleted.`);
    await loadAgents();
  } catch (error) {
    console.error('Failed to delete agent:', error);
    notifyError(error?.message || `Failed to delete "${normalizedName}"`);
  } finally {
    if (button && button.isConnected) {
      button.disabled = false;
      button.textContent = originalText;
    }
  }
}

async function openAgentDrawer(agentName) {
  selectedAgentName = agentName;
  selectedAgentDetail = null;
  selectedAgentSkills = null;

  const drawer = document.getElementById('agentDrawer');
  const backdrop = document.getElementById('agentDrawerBackdrop');
  const drawerAgentName = document.getElementById('drawerAgentName');
  const closeDrawerBtn = document.getElementById('closeDrawerBtn');
  lastFocusedElement = document.activeElement instanceof HTMLElement ? document.activeElement : null;

  if (drawerAgentName) {
    drawerAgentName.textContent = agentName;
  }

  syncDrawerActionLinks(agentName);

  if (drawer) {
    drawer.classList.add('open');
    drawer.setAttribute('aria-hidden', 'false');
  }

  if (backdrop) {
    backdrop.classList.remove('hidden');
    backdrop.setAttribute('aria-hidden', 'false');
  }

  setBackgroundInteractivity(false);
  document.body.classList.add('ops-modal-open');
  requestAnimationFrame(() => closeDrawerBtn?.focus());

  setDrawerTab('overview');
  setDrawerLoadingState();

  const [detailResult, skillsResult] = await Promise.allSettled([
    fetchAgentDetail(agentName),
    fetchAgentSkills(agentName)
  ]);

  if (selectedAgentName !== agentName) {
    return;
  }

  if (detailResult.status === 'fulfilled') {
    selectedAgentDetail = detailResult.value;
  } else {
    console.error('Failed to fetch agent detail:', detailResult.reason);
  }

  if (skillsResult.status === 'fulfilled') {
    selectedAgentSkills = skillsResult.value;
  } else {
    console.error('Failed to fetch agent skills:', skillsResult.reason);
  }

  renderDrawerContent();
}

function closeAgentDrawer() {
  const drawer = document.getElementById('agentDrawer');
  const backdrop = document.getElementById('agentDrawerBackdrop');
  const focusTarget = lastFocusedElement;

  if (drawer) {
    drawer.classList.remove('open');
    drawer.setAttribute('aria-hidden', 'true');
  }

  if (backdrop) {
    backdrop.classList.add('hidden');
    backdrop.setAttribute('aria-hidden', 'true');
  }

  setBackgroundInteractivity(true);
  document.body.classList.remove('ops-modal-open');
  selectedAgentName = '';
  selectedAgentDetail = null;
  selectedAgentSkills = null;
  syncDrawerActionLinks('');
  lastFocusedElement = null;

  if (focusTarget && document.contains(focusTarget)) {
    requestAnimationFrame(() => focusTarget.focus());
  }
}

function setDrawerTab(tabName, options = {}) {
  const { focusPanel = false } = options;
  const normalized = tabName === 'tools' || tabName === 'advanced' ? tabName : 'overview';

  document.querySelectorAll('.ops-drawer-tab').forEach((button) => {
    const isActive = button.dataset.tab === normalized;
    button.classList.toggle('active', isActive);
    button.setAttribute('aria-selected', String(isActive));
    button.tabIndex = isActive ? 0 : -1;
  });

  document.querySelectorAll('.ops-drawer-panel').forEach((panel) => {
    const isActive = panel.id === `drawer${toTitleCase(normalized)}Tab`;
    panel.classList.toggle('active', isActive);
    panel.hidden = !isActive;
  });

  const panel = document.getElementById(`drawer${toTitleCase(normalized)}Tab`);
  if (panel && focusPanel) {
    panel.focus();
  }
}

function setDrawerLoadingState() {
  const loadingHtml = '<div class="ops-data-card"><span class="ops-data-label">Loading</span><div class="ops-data-value">Fetching latest agent details…</div></div>';

  const overview = document.getElementById('drawerOverviewContent');
  const tools = document.getElementById('drawerToolsContent');
  const advanced = document.getElementById('drawerAdvancedContent');

  if (overview) {
    overview.innerHTML = loadingHtml;
  }

  if (tools) {
    tools.innerHTML = loadingHtml;
  }

  if (advanced) {
    advanced.innerHTML = loadingHtml;
  }
}

function renderDrawerContent() {
  const fallbackAgent = dashboardAgents.find((agent) => String(agent?.name || '') === selectedAgentName) || null;
  const detail = selectedAgentDetail || fallbackAgent || {};
  const health = getHealthState(detail);

  const overview = document.getElementById('drawerOverviewContent');
  const tools = document.getElementById('drawerToolsContent');
  const advanced = document.getElementById('drawerAdvancedContent');

  if (overview) {
    overview.innerHTML = `
      <div class="ops-data-card">
        <span class="ops-data-label">Status</span>
        <div class="ops-data-value">${safeEscapeHtml(health.label)}</div>
      </div>
      <div class="ops-data-card">
        <span class="ops-data-label">Purpose</span>
        <div class="ops-data-value">${safeEscapeHtml(detail?.metadata?.description || 'No purpose description yet.')}</div>
      </div>
      <div class="ops-data-card">
        <span class="ops-data-label">Type</span>
        <div class="ops-data-value">${safeEscapeHtml(toTitleCase(detail?.type || 'tool-calling'))}</div>
      </div>
      <div class="ops-data-card">
        <span class="ops-data-label">Model</span>
        <div class="ops-data-value">${safeEscapeHtml(detail?.model || 'Not configured')}</div>
      </div>
      <div class="ops-data-card">
        <span class="ops-data-label">Last Active</span>
        <div class="ops-data-value">${safeEscapeHtml(formatDate(detail?.statistics?.last_active || ''))}</div>
      </div>
    `;
  }

  const enabledPlugins = Array.isArray(detail?.enabled_plugins)
    ? detail.enabled_plugins
        .map((plugin) => (typeof plugin === 'string' ? plugin : plugin?.name))
        .filter(Boolean)
    : [];
  const skillCount = Number(selectedAgentSkills?.total || 0);
  const enabledSkillCount = Number(selectedAgentSkills?.enabled || 0);
  let skillSummary = `${enabledSkillCount}/${skillCount} enabled`;

  if (selectedAgentSkills?.conflict) {
    skillSummary = 'Unavailable while another skills source is active.';
  } else if (selectedAgentSkills?.error) {
    skillSummary = 'Unable to load skills right now.';
  }

  if (tools) {
    tools.innerHTML = `
      <div class="ops-data-card">
        <span class="ops-data-label">Plugins (${enabledPlugins.length})</span>
        <div class="ops-data-value">${enabledPlugins.length > 0 ? safeEscapeHtml(enabledPlugins.join(', ')) : 'No plugins enabled.'}</div>
      </div>
      <div class="ops-data-card">
        <span class="ops-data-label">Skills</span>
        <div class="ops-data-value">${safeEscapeHtml(skillSummary)}</div>
      </div>
      <div class="ops-data-card">
        <span class="ops-data-label">MCP Access</span>
        <div class="ops-data-value">Workspace-scoped. Configure connector bindings from the target workspace.</div>
      </div>
    `;
  }

  const systemPrompt = String(detail?.system_prompt || '').trim();

  if (advanced) {
    advanced.innerHTML = `
      <div class="ops-data-card">
        <span class="ops-data-label">Provider</span>
        <div class="ops-data-value">${safeEscapeHtml(detail?.provider || '-')}</div>
      </div>
      <div class="ops-data-card">
        <span class="ops-data-label">Temperature</span>
        <div class="ops-data-value">${safeEscapeHtml(Number(detail?.temperature || 0).toFixed(2))}</div>
      </div>
      <div class="ops-data-card">
        <span class="ops-data-label">Max Output Tokens</span>
        <div class="ops-data-value">${safeEscapeHtml(detail?.max_output_tokens || '-')}</div>
      </div>
      <div class="ops-data-card">
        <span class="ops-data-label">System Prompt</span>
        <div class="ops-data-value">${safeEscapeHtml(systemPrompt || 'No custom system prompt.')}</div>
      </div>
    `;
  }
}

async function fetchAgentDetail(agentName) {
  const endpoint = `/api/agents/${encodeURIComponent(agentName)}/detail`;
  const response = await fetch(endpoint);

  if (!response.ok) {
    throw new Error(`Failed to fetch detail (${response.status})`);
  }

  return response.json();
}

async function fetchAgentSkills(agentName) {
  const endpoint = `/api/skills?agent=${encodeURIComponent(agentName)}`;
  const response = await fetch(endpoint);

  if (response.status === 409) {
    return { total: 0, enabled: 0, conflict: true };
  }

  if (!response.ok) {
    return { total: 0, enabled: 0, error: true };
  }

  const data = await response.json();
  const skills = Array.isArray(data?.skills) ? data.skills : [];
  const enabled = skills.filter((skill) => skill?.enabled !== false).length;

  return {
    total: skills.length,
    enabled,
    conflict: false,
    error: false
  };
}

function getHealthState(agent) {
  const status = String(agent?.status || 'idle');

  if (status === 'error') {
    return { kind: 'error', label: 'Error' };
  }

  if (!String(agent?.model || '').trim()) {
    return { kind: 'needs-setup', label: 'Needs Setup' };
  }

  if (status === 'disabled') {
    return { kind: 'paused', label: 'Paused' };
  }

  if (status === 'active') {
    return { kind: 'healthy', label: 'Healthy' };
  }

  return { kind: 'idle', label: 'Idle' };
}

function isSystemAssistantAgentName(name) {
  return String(name || '').trim().toLowerCase() === 'ori';
}

function getAgentColor(agent) {
  const avatarColor = String(agent?.metadata?.avatar_color || '').trim();
  if (avatarColor) {
    return avatarColor;
  }

  const name = String(agent?.name || 'A');
  const hash = name.split('').reduce((acc, char) => char.charCodeAt(0) + ((acc << 5) - acc), 0);
  return `hsl(${Math.abs(hash) % 360}, 62%, 46%)`;
}

function getAgentInitials(name) {
  const parts = String(name || '').trim().split(/[\s_-]+/).filter(Boolean);
  if (parts.length >= 2) {
    return `${parts[0][0]}${parts[1][0]}`.toUpperCase();
  }

  return String(name || 'A').slice(0, 2).toUpperCase();
}

function getAvatarHtml(agent, className) {
  const image = String(agent?.metadata?.avatar_image || '').trim();
  const name = String(agent?.name || 'Agent');

  if (image) {
    return `<div class="${safeEscapeHtml(className)}" style="padding:0;overflow:hidden;"><img src="/avatars/${safeEscapeHtml(image)}" alt="${safeEscapeHtml(name)}" loading="lazy" decoding="async" width="36" height="36" style="width:100%;height:100%;object-fit:cover;"></div>`;
  }

  return `<div class="${safeEscapeHtml(className)}" style="background:${safeEscapeHtml(getAgentColor(agent))};">${safeEscapeHtml(getAgentInitials(name))}</div>`;
}

function toTitleCase(value) {
  return String(value || '')
    .split(/[\s_-]+/)
    .filter(Boolean)
    .map((part) => part.charAt(0).toUpperCase() + part.slice(1).toLowerCase())
    .join(' ');
}

function formatDate(value) {
  if (!value) {
    return 'Never';
  }

  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return 'Never';
  }

  const now = Date.now();
  const diff = now - date.getTime();

  const minute = 60 * 1000;
  const hour = 60 * minute;
  const day = 24 * hour;

  if (diff < minute) {
    return 'Just now';
  }

  if (diff < hour) {
    return `${Math.floor(diff / minute)}m ago`;
  }

  if (diff < day) {
    return `${Math.floor(diff / hour)}h ago`;
  }

  if (diff < 7 * day) {
    return `${Math.floor(diff / day)}d ago`;
  }

  return absoluteDateFormatter.format(date);
}

function showLoading(show) {
  const loadingState = document.getElementById('loadingState');
  const board = document.getElementById('opsBoard');
  const emptyState = document.getElementById('emptyState');

  if (loadingState) {
    loadingState.classList.toggle('hidden', !show);
  }

  if (board) {
    board.classList.toggle('hidden', show);
  }

  if (show && emptyState) {
    emptyState.classList.add('hidden');
  }
}

function showBoard() {
  const board = document.getElementById('opsBoard');
  const emptyState = document.getElementById('emptyState');

  if (board) {
    board.classList.remove('hidden');
  }

  if (emptyState) {
    emptyState.classList.add('hidden');
  }
}

function showEmptyState(options = {}) {
  const {
    searchTerm = '',
    hasAgents = dashboardAgents.length > 0
  } = options;
  const board = document.getElementById('opsBoard');
  const emptyState = document.getElementById('emptyState');
  const emptyStateTitle = document.getElementById('emptyStateTitle');
  const emptyStateMessage = document.getElementById('emptyStateMessage');
  const emptyStateClearBtn = document.getElementById('emptyStateClearBtn');
  const emptyStateCreateLink = document.getElementById('emptyStateCreateLink');

  if (board) {
    board.classList.add('hidden');
  }

  if (emptyStateTitle) {
    emptyStateTitle.textContent = searchTerm && hasAgents ? 'No matching agents' : 'No agents found';
  }

  if (emptyStateMessage) {
    emptyStateMessage.textContent = searchTerm && hasAgents
      ? `No agents match "${searchTerm}". Clear your search to see all agents.`
      : 'Create your first agent to get started.';
  }

  if (emptyStateClearBtn) {
    emptyStateClearBtn.classList.toggle('hidden', !(searchTerm && hasAgents));
  }

  if (emptyStateCreateLink) {
    emptyStateCreateLink.classList.toggle('hidden', searchTerm && hasAgents);
  }

  if (emptyState) {
    emptyState.classList.remove('hidden');
  }
}

function clearDashboardSearch() {
  const searchInput = document.getElementById('searchInput');
  if (!searchInput) {
    return;
  }

  searchInput.value = '';
  syncDashboardStateToUrl();
  applyFiltersAndRender();
  searchInput.focus();
}

window.closeAgentDrawer = closeAgentDrawer;

function syncDrawerActionLinks(agentName) {
  const editLink = document.getElementById('drawerEditLink');

  if (editLink) {
    editLink.href = agentName
      ? `/agents/${encodeURIComponent(agentName)}`
      : '/agents';
  }
}

function setBackgroundInteractivity(enabled) {
  document.querySelectorAll('.navbar, .main-content-wrapper, .skip-link').forEach((element) => {
    if (!element) {
      return;
    }

    if ('inert' in element) {
      element.inert = !enabled;
    }

    if (!enabled) {
      element.setAttribute('aria-hidden', 'true');
    } else {
      element.removeAttribute('aria-hidden');
    }
  });
}

function getFocusableElements(root) {
  if (!root) {
    return [];
  }

  return Array.from(
    root.querySelectorAll(
      'a[href], button:not([disabled]), textarea:not([disabled]), input:not([disabled]), select:not([disabled]), [tabindex]:not([tabindex="-1"])'
    )
  ).filter((element) => {
    if (!(element instanceof HTMLElement)) {
      return false;
    }

    if (element.hidden || element.closest('[hidden]')) {
      return false;
    }

    return element.offsetParent !== null || getComputedStyle(element).position === 'fixed';
  });
}

function trapDrawerFocus(event) {
  const drawer = document.getElementById('agentDrawer');
  if (!drawer || !drawer.classList.contains('open')) {
    return;
  }

  const focusableElements = getFocusableElements(drawer);
  if (focusableElements.length === 0) {
    event.preventDefault();
    drawer.focus();
    return;
  }

  const firstElement = focusableElements[0];
  const lastElement = focusableElements[focusableElements.length - 1];
  const activeElement = document.activeElement;

  if (!drawer.contains(activeElement)) {
    event.preventDefault();
    firstElement.focus();
    return;
  }

  if (event.shiftKey && activeElement === firstElement) {
    event.preventDefault();
    lastElement.focus();
    return;
  }

  if (!event.shiftKey && activeElement === lastElement) {
    event.preventDefault();
    firstElement.focus();
  }
}

function handleGlobalKeydown(event) {
  const drawer = document.getElementById('agentDrawer');
  if (!drawer || !drawer.classList.contains('open')) {
    return;
  }

  if (event.key === 'Escape') {
    event.preventDefault();
    closeAgentDrawer();
    return;
  }

  if (event.key === 'Tab') {
    trapDrawerFocus(event);
  }
}

function handleDrawerTabKeydown(event) {
  const tabs = Array.from(document.querySelectorAll('.ops-drawer-tab'));
  const currentIndex = tabs.indexOf(event.currentTarget);
  if (currentIndex === -1) {
    return;
  }

  let nextIndex = currentIndex;

  switch (event.key) {
    case 'ArrowRight':
    case 'ArrowDown':
      nextIndex = (currentIndex + 1) % tabs.length;
      break;
    case 'ArrowLeft':
    case 'ArrowUp':
      nextIndex = (currentIndex - 1 + tabs.length) % tabs.length;
      break;
    case 'Home':
      nextIndex = 0;
      break;
    case 'End':
      nextIndex = tabs.length - 1;
      break;
    default:
      return;
  }

  event.preventDefault();
  const nextTab = tabs[nextIndex];
  nextTab.focus();
  setDrawerTab(nextTab.dataset.tab || 'overview');
}

if (document.readyState === 'loading') {
  document.addEventListener('DOMContentLoaded', initializeDashboard);
} else {
  initializeDashboard();
}
