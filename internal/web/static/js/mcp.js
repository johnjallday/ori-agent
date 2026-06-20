// MCP Management Page JavaScript

let marketplaceServers = [];
let mcpServers = [];
let statusPollInterval = null;

// Initialize on page load
document.addEventListener('DOMContentLoaded', function() {
  initializeEventListeners();
  loadMarketplaceServers();
  loadServers();
  startStatusPolling();
  // Dark mode is handled by themeManager.js
});

function initializeEventListeners() {
  // Add server button
  document.getElementById('addServerBtn').addEventListener('click', function() {
    const modal = new bootstrap.Modal(document.getElementById('addServerModal'));
    modal.show();
  });

  // Manual config form
  document.getElementById('manualConfigForm').addEventListener('submit', function(e) {
    e.preventDefault();
    addServerManual();
  });

  // Import file
  document.getElementById('importFileInput').addEventListener('change', handleFileImport);
  document.getElementById('importBtn').addEventListener('click', importServers);

  // Marketplace search
  document.getElementById('marketplaceSearch').addEventListener('input', filterMarketplace);
}

async function loadMarketplaceServers() {
  try {
    const response = await fetch('/api/mcp/marketplace');
    const data = await response.json();
    marketplaceServers = data.servers || [];
    renderMarketplace();
  } catch (error) {
    console.error('Failed to load marketplace:', error);
    document.getElementById('marketplaceList').innerHTML =
      '<div class="alert alert-danger">Failed to load marketplace</div>';
  }
}

function renderMarketplace() {
  const container = document.getElementById('marketplaceList');
  container.innerHTML = '';

  if (marketplaceServers.length === 0) {
    container.innerHTML = '<div class="alert alert-info">No marketplace servers available</div>';
    return;
  }

  marketplaceServers.forEach(server => {
    const item = document.createElement('div');
    item.className = 'list-group-item d-flex justify-content-between align-items-start marketplace-item';
    item.style = 'background: var(--bg-tertiary); border-color: var(--border-color); cursor: pointer;';

    const envRequired = server.env_required ? Object.keys(server.env_required).join(', ') : '';

    item.innerHTML = `
      <div class="flex-grow-1">
        <h6 class="mb-1" style="color: var(--text-primary);">${server.name}</h6>
        <p class="mb-1 small" style="color: var(--text-secondary);">${server.description}</p>
        <div class="d-flex gap-2 mt-1">
          <span class="badge bg-secondary">${server.category}</span>
          ${envRequired ? `<span class="badge bg-warning text-dark">Requires: ${envRequired}</span>` : ''}
        </div>
      </div>
      <button class="modern-btn modern-btn-primary modern-btn-sm install-btn" data-server="${encodeURIComponent(JSON.stringify(server))}">
        Install
      </button>
    `;

    container.appendChild(item);
  });

  // Add event listeners to install buttons
  document.querySelectorAll('.install-btn').forEach(btn => {
    btn.addEventListener('click', function(e) {
      e.stopPropagation();
      const serverData = JSON.parse(decodeURIComponent(this.dataset.server));
      installFromMarketplace(serverData);
    });
  });
}

function filterMarketplace() {
  const query = document.getElementById('marketplaceSearch').value.toLowerCase();
  const items = document.querySelectorAll('.marketplace-item');

  items.forEach(item => {
    const text = item.textContent.toLowerCase();
    item.style.display = text.includes(query) ? 'flex' : 'none';
  });
}

async function installFromMarketplace(serverData) {
  const serverConfig = {
    name: serverData.name,
    command: serverData.command,
    args: serverData.args || [],
    env: {},
    transport: serverData.transport || 'stdio',
    enabled: false
  };

  try {
    const response = await fetch('/api/mcp/servers', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(serverConfig)
    });

    if (response.ok) {
      showToast(`${serverData.name} installed successfully`, 'success');
      bootstrap.Modal.getInstance(document.getElementById('addServerModal')).hide();
      loadServers();
    } else {
      const error = await response.text();
      showToast(`Failed to install: ${error}`, 'error');
    }
  } catch (error) {
    console.error('Installation error:', error);
    showToast('Installation failed', 'error');
  }
}

async function addServerManual() {
  const name = document.getElementById('manualName').value.trim();
  const command = document.getElementById('manualCommand').value.trim();
  const argsText = document.getElementById('manualArgs').value.trim();
  const envText = document.getElementById('manualEnv').value.trim();
  const transport = document.getElementById('manualTransport').value;

  if (!name || !command) {
    showToast('Name and command are required', 'error');
    return;
  }

  let args = [];
  let env = {};

  try {
    if (argsText) {
      args = JSON.parse(argsText);
      if (!Array.isArray(args)) {
        throw new Error('Arguments must be an array');
      }
    }
  } catch (error) {
    showToast('Invalid arguments JSON: ' + error.message, 'error');
    return;
  }

  try {
    if (envText) {
      env = JSON.parse(envText);
      if (typeof env !== 'object' || Array.isArray(env)) {
        throw new Error('Environment must be an object');
      }
    }
  } catch (error) {
    showToast('Invalid environment JSON: ' + error.message, 'error');
    return;
  }

  const serverConfig = {
    name: name,
    command: command,
    args: args,
    env: env,
    transport: transport,
    enabled: false
  };

  try {
    const response = await fetch('/api/mcp/servers', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(serverConfig)
    });

    if (response.ok) {
      showToast('Server added successfully', 'success');
      document.getElementById('manualConfigForm').reset();
      bootstrap.Modal.getInstance(document.getElementById('addServerModal')).hide();
      loadServers();
    } else {
      const error = await response.text();
      showToast('Failed to add server: ' + error, 'error');
    }
  } catch (error) {
    console.error('Add server error:', error);
    showToast('Failed to add server', 'error');
  }
}

function handleFileImport(event) {
  const file = event.target.files[0];
  if (!file) return;

  const reader = new FileReader();
  reader.onload = function(e) {
    try {
      const content = e.target.result;
      const config = JSON.parse(content);

      document.getElementById('importPreviewContent').textContent = JSON.stringify(config, null, 2);
      document.getElementById('importPreview').style.display = 'block';
      document.getElementById('importBtn').style.display = 'block';
    } catch (error) {
      showToast('Invalid JSON file: ' + error.message, 'error');
      document.getElementById('importPreview').style.display = 'none';
      document.getElementById('importBtn').style.display = 'none';
    }
  };
  reader.readAsText(file);
}

async function importServers() {
  const fileInput = document.getElementById('importFileInput');
  const file = fileInput.files[0];

  if (!file) {
    showToast('Please select a file', 'error');
    return;
  }

  const formData = new FormData();
  formData.append('config_file', file);

  try {
    const response = await fetch('/api/mcp/import', {
      method: 'POST',
      body: formData
    });

    const result = await response.json();

    if (result.added && result.added.length > 0) {
      showToast(`Imported ${result.added.length} server(s)`, 'success');
    }

    if (result.errors && result.errors.length > 0) {
      console.error('Import errors:', result.errors);
      showToast(`${result.errors.length} error(s) during import`, 'warning');
    }

    bootstrap.Modal.getInstance(document.getElementById('addServerModal')).hide();
    loadServers();
  } catch (error) {
    console.error('Import error:', error);
    showToast('Import failed', 'error');
  }
}

async function loadServers() {
  const container = document.getElementById('mcpServersList');
  const emptyState = document.getElementById('emptyState');

  container.innerHTML = '<div class="text-center py-3"><div class="spinner-border text-primary"></div></div>';
  emptyState.style.display = 'none';

  try {
    const response = await fetch('/api/mcp/servers');
    const data = await response.json();
    const servers = data.servers || [];
    const stats = data.stats || {};

    // Merge server configs with runtime stats
    mcpServers = servers.map(server => {
      const serverStats = stats[server.name] || {};
      return {
        ...server,
        status: serverStats.status || 'stopped',
        tool_count: serverStats.tool_count || 0,
        enabled: serverStats.enabled !== undefined ? serverStats.enabled : server.enabled
      };
    });

    if (mcpServers.length === 0) {
      container.innerHTML = '';
      emptyState.style.display = 'block';
      return;
    }

    renderServers();
  } catch (error) {
    console.error('Failed to load servers:', error);
    container.innerHTML = '<div class="alert alert-danger">Failed to load servers</div>';
  }
}

function renderServers() {
  const container = document.getElementById('mcpServersList');
  container.innerHTML = '';

  mcpServers.forEach(server => {
    const card = createServerCard(server);
    container.appendChild(card);
  });
}

function createServerCard(server) {
  const div = document.createElement('div');
  div.className = 'modern-card p-3 mb-3';

  const statusBadge = getStatusBadge(server.status || 'stopped');
  const argsDisplay = server.args ? server.args.join(' ') : '';
  const toolCountBadge = server.tool_count > 0 ? `<span class="badge bg-info ms-2">${server.tool_count} tools</span>` : '';
  const enabledBadge = server.enabled
    ? '<span class="badge bg-primary ms-2">Enabled globally</span>'
    : '<span class="badge bg-secondary ms-2">Disabled globally</span>';
  const toggleLabel = server.enabled ? 'Disable' : 'Enable';
  const toggleClass = server.enabled ? 'modern-btn-secondary' : 'modern-btn-primary';
  // Plugin-owned servers are namespaced "<plugin>/<server>"; they're read-only
  // here (manage via the Plugins page), though enable/disable is still allowed.
  const isPlugin = server.name.includes('/');
  const pluginBadge = isPlugin ? '<span class="badge bg-info ms-2">Plugin (read-only)</span>' : '';

  div.innerHTML = `
    <div class="d-flex justify-content-between align-items-start">
      <div class="flex-grow-1">
        <h6 class="mb-1" style="color: var(--text-primary);">${server.name}</h6>
        <p class="mb-1 small text-muted">${server.command} ${argsDisplay}</p>
        <div class="mt-2">
          ${statusBadge}
          ${toolCountBadge}
          ${enabledBadge}
          ${pluginBadge}
        </div>
      </div>
      <div class="d-flex gap-2 flex-wrap justify-content-end">
        <button class="modern-btn modern-btn-secondary modern-btn-sm" onclick="viewServerDetails('${encodeURIComponent(server.name)}')">
          Details
        </button>
        <button class="modern-btn ${toggleClass} modern-btn-sm" onclick="toggleServerEnabled('${encodeURIComponent(server.name)}', ${server.enabled})">
          ${toggleLabel}
        </button>
        <button class="modern-btn modern-btn-secondary modern-btn-sm" onclick="testConnection('${server.name}')">
          Test
        </button>
        ${server.status === 'error' ? `
          <button class="modern-btn modern-btn-warning modern-btn-sm" onclick="retryConnection('${server.name}')">
            Retry
          </button>
        ` : ''}
        ${isPlugin
          ? '<span class="modern-btn modern-btn-secondary modern-btn-sm disabled" title="Managed by its plugin; uninstall from the Plugins page">Managed by plugin</span>'
          : `<button class="modern-btn modern-btn-danger modern-btn-sm" onclick="confirmRemoveServer('${server.name}')">Remove</button>`}
      </div>
    </div>
  `;

  return div;
}

async function toggleServerEnabled(encodedServerName, currentlyEnabled) {
  const serverName = decodeURIComponent(encodedServerName);
  const action = currentlyEnabled ? 'disable' : 'enable';

  try {
    const response = await fetch(`/api/mcp/servers/${encodeURIComponent(serverName)}/${action}`, {
      method: 'POST'
    });

    if (!response.ok) {
      const error = await response.text();
      showToast(`Failed to ${action} ${serverName}: ${error}`, 'error');
      return;
    }

    const result = await response.json();
    if (!currentlyEnabled && result.start_error) {
      showToast(`${serverName} enabled globally, but startup failed: ${result.start_error}`, 'warning');
    } else {
      showToast(`${serverName} ${currentlyEnabled ? 'disabled' : 'enabled'} globally`, 'success');
    }

    loadServers();
  } catch (error) {
    console.error(`Failed to ${action} MCP server:`, error);
    showToast(`Failed to ${action} ${serverName}`, 'error');
  }
}

function getStatusBadge(status) {
  const badges = {
    'running': '<span class="badge bg-success">Running</span>',
    'stopped': '<span class="badge bg-secondary">Stopped</span>',
    'starting': '<span class="badge bg-info">Starting</span>',
    'error': '<span class="badge bg-danger">Error</span>',
    'restarting': '<span class="badge bg-warning">Restarting</span>'
  };
  return badges[status] || badges['stopped'];
}

async function testConnection(serverName) {
  showToast('Testing connection...', 'info');

  try {
    const response = await fetch(`/api/mcp/servers/${serverName}/test`, {
      method: 'POST'
    });

    const result = await response.json();

    if (result.success) {
      showToast(`✓ Connection successful (${result.tool_count} tools available)`, 'success');
    } else {
      showToast(`✗ Connection failed: ${result.error}`, 'error');
    }
  } catch (error) {
    console.error('Test connection error:', error);
    showToast('Test failed', 'error');
  }
}

async function retryConnection(serverName) {
  showToast('Retrying connection...', 'info');

  try {
    const response = await fetch(`/api/mcp/servers/${serverName}/retry`, {
      method: 'POST'
    });

    if (response.ok) {
      showToast('Server restart initiated', 'success');
      setTimeout(loadServers, 1000);
    } else {
      const error = await response.text();
      showToast('Retry failed: ' + error, 'error');
    }
  } catch (error) {
    console.error('Retry error:', error);
    showToast('Retry failed', 'error');
  }
}

function confirmRemoveServer(serverName) {
  document.getElementById('removeServerName').textContent = serverName;
  const modal = new bootstrap.Modal(document.getElementById('removeServerModal'));
  modal.show();

  document.getElementById('confirmRemoveBtn').onclick = function() {
    removeServer(serverName);
    modal.hide();
  };
}

async function removeServer(serverName) {
  try {
    const response = await fetch(`/api/mcp/servers/${serverName}`, {
      method: 'DELETE'
    });

    if (response.ok) {
      showToast('Server removed', 'success');
      loadServers();
    } else {
      const error = await response.text();
      showToast('Failed to remove: ' + error, 'error');
    }
  } catch (error) {
    console.error('Remove error:', error);
    showToast('Failed to remove server', 'error');
  }
}

function startStatusPolling() {
  // Poll every 15 seconds
  if (statusPollInterval) {
    clearInterval(statusPollInterval);
  }

  statusPollInterval = setInterval(() => {
    if (mcpServers.length > 0) {
      loadServers();
    }
  }, 15000);
}

function showToast(message, type = 'info') {
  // Create a simple toast notification
  const toastContainer = document.getElementById('toastContainer') || createToastContainer();

  const toast = document.createElement('div');
  toast.className = `alert alert-${type === 'error' ? 'danger' : type === 'success' ? 'success' : type === 'warning' ? 'warning' : 'info'} alert-dismissible fade show`;
  toast.style = 'margin-bottom: 0.5rem;';
  toast.innerHTML = `
    ${message}
    <button type="button" class="btn-close" data-bs-dismiss="alert"></button>
  `;

  toastContainer.appendChild(toast);

  setTimeout(() => {
    toast.remove();
  }, 5000);
}

function createToastContainer() {
  const container = document.createElement('div');
  container.id = 'toastContainer';
  container.style = 'position: fixed; top: 100px; right: 20px; z-index: 9999; min-width: 300px;';
  document.body.appendChild(container);
  return container;
}

// Cleanup on page unload
window.addEventListener('beforeunload', function() {
  if (statusPollInterval) {
    clearInterval(statusPollInterval);
  }
});

// ============================================================
// BROWSE TAB — Registry browser
// ============================================================

let allRegistryEntries = [];   // full cached result from /api/mcp/search
let registrySources = [];      // from /api/mcp/registry-sources
let browseLoaded = false;      // avoid duplicate fetches on tab re-click
let activeCategoryFilter = 'all';
let sourcesPanelOpen = false;

/**
 * Called when the Browse tab is first clicked.
 * Defers the initial fetch to avoid blocking page load.
 */
function onBrowseTabActivated() {
  if (browseLoaded) return;
  browseLoaded = true;
  loadRegistrySources();
  loadSearchResults();
}

/** Fetch all registry sources and populate the source filter dropdown. */
async function loadRegistrySources() {
  try {
    const res = await fetch('/api/mcp/registry-sources');
    registrySources = await res.json();
    populateSourceFilter(registrySources);
    if (sourcesPanelOpen) {
      renderSourcesList(registrySources);
    }
  } catch (err) {
    console.error('Failed to load registry sources:', err);
  }
}

function populateSourceFilter(sources) {
  const select = document.getElementById('browseSource');
  if (!select) return;

  // Keep the "All Sources" option, remove dynamic ones
  while (select.options.length > 1) select.remove(1);

  sources.forEach(src => {
    const opt = document.createElement('option');
    opt.value = src.name.toLowerCase();
    opt.textContent = src.name;
    select.appendChild(opt);
  });
}

/** Fetch entries from the backend search endpoint (applies server-side cache). */
async function loadSearchResults() {
  const container = document.getElementById('browseResults');
  if (!container) return;

  container.innerHTML = '<div class="text-center py-5"><div class="spinner-border text-primary" role="status"></div></div>';

  try {
    const res = await fetch('/api/mcp/search');
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    allRegistryEntries = await res.json();
    filterSearch(); // apply current UI filters to the freshly loaded data
  } catch (err) {
    console.error('Failed to load registry entries:', err);
    container.innerHTML = '<div class="alert alert-danger m-3">Failed to load registry. Check your connection and try refreshing.</div>';
  }
}

/**
 * Applies the current search text, category, and source filters
 * client-side against allRegistryEntries and re-renders cards.
 */
function filterSearch() {
  const q = (document.getElementById('browseSearch')?.value || '').toLowerCase().trim();
  const source = (document.getElementById('browseSource')?.value || 'all').toLowerCase();
  const category = activeCategoryFilter;

  const filtered = allRegistryEntries.filter(entry => {
    if (q) {
      const haystack = [entry.name, entry.description, entry.category, ...(entry.tags || [])].join(' ').toLowerCase();
      if (!haystack.includes(q)) return false;
    }
    if (category && category !== 'all' && (entry.category || '').toLowerCase() !== category) return false;
    if (source && source !== 'all' && (entry.source || '').toLowerCase() !== source) return false;
    return true;
  });

  renderSearchResults(filtered);
}

/** Render a grid of server cards. */
function renderSearchResults(entries) {
  const container = document.getElementById('browseResults');
  if (!container) return;

  if (entries.length === 0) {
    container.innerHTML = `
      <div class="text-center py-5" style="color: var(--text-secondary);">
        <svg width="48" height="48" viewBox="0 0 24 24" fill="currentColor" style="opacity:0.3;" class="mb-3">
          <path d="M15.5,14H14.71L14.43,13.73C15.41,12.59 16,11.11 16,9.5A6.5,6.5 0 0,0 9.5,3A6.5,6.5 0 0,0 3,9.5A6.5,6.5 0 0,0 9.5,16C11.11,16 12.59,15.41 13.73,14.43L14,14.71V15.5L19,20.5L20.5,19L15.5,14M9.5,14C7,14 5,12 5,9.5C5,7 7,5 9.5,5C12,5 14,7 14,9.5C14,12 12,14 9.5,14Z"/>
        </svg>
        <p>No servers found. Try a different search or category.</p>
      </div>`;
    return;
  }

  // Build a set of currently installed server names
  const installed = new Set(mcpServers.map(s => s.name));

  container.innerHTML = '';
  const grid = document.createElement('div');
  grid.className = 'row g-3';

  entries.forEach(entry => {
    const isInstalled = installed.has(entry.name);
    const envWarning = entry.env_required && Object.keys(entry.env_required).length > 0
      ? `<span class="badge bg-warning text-dark me-1" title="Requires environment variables">Needs env</span>`
      : '';

    const col = document.createElement('div');
    col.className = 'col-12 col-md-6 col-xl-4';
    col.innerHTML = `
      <div class="browse-server-card h-100 d-flex flex-column">
        <div class="d-flex justify-content-between align-items-start mb-2">
          <h6 class="mb-0 fw-semibold" style="color: var(--text-primary);">${mcpEscapeHtml(entry.name)}</h6>
          <div class="d-flex gap-1 flex-shrink-0 ms-2">
            <span class="badge bg-secondary">${mcpEscapeHtml(entry.category || 'other')}</span>
            ${entry.source ? `<span class="badge" style="background: var(--bg-tertiary); color: var(--text-secondary); border: 1px solid var(--border-color);">${mcpEscapeHtml(entry.source)}</span>` : ''}
          </div>
        </div>
        <p class="small mb-2 flex-grow-1" style="color: var(--text-secondary);">${mcpEscapeHtml(entry.description || '')}</p>
        <div class="d-flex align-items-center justify-content-between mt-auto pt-2" style="border-top: 1px solid var(--border-color);">
          <div>${envWarning}${entry.maintainer ? `<small style="color: var(--text-secondary);">${mcpEscapeHtml(entry.maintainer)}</small>` : ''}</div>
          ${isInstalled
            ? `<button class="modern-btn modern-btn-secondary modern-btn-sm" disabled>Added</button>`
            : `<button class="modern-btn modern-btn-primary modern-btn-sm" onclick='addFromRegistry(${JSON.stringify(entry)})'>Add</button>`
          }
        </div>
      </div>`;
    grid.appendChild(col);
  });

  container.appendChild(grid);
}

/** Add a server from the registry to "My Servers". */
async function addFromRegistry(entry) {
  const serverConfig = {
    name: entry.name,
    command: entry.command,
    args: entry.args || [],
    env: {},
    transport: entry.transport || 'stdio',
    enabled: false
  };

  try {
    const res = await fetch('/api/mcp/servers', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(serverConfig)
    });

    if (res.ok) {
      showToast(`${entry.name} added to My Servers`, 'success');
      await loadServers(); // refresh My Servers list
      filterSearch();      // re-render cards to show "Added" state
    } else {
      const err = await res.text();
      showToast(`Failed to add: ${err}`, 'error');
    }
  } catch (err) {
    console.error('addFromRegistry error:', err);
    showToast('Failed to add server', 'error');
  }
}

/** Update the active category filter pill and re-filter. */
function setCategoryFilter(btn) {
  document.querySelectorAll('.category-pill').forEach(p => p.classList.remove('active'));
  btn.classList.add('active');
  activeCategoryFilter = btn.dataset.category || 'all';
  filterSearch();
}

/** Force-refresh all registry sources and reload results. */
async function refreshRegistry() {
  const btn = document.getElementById('refreshBtn');
  if (btn) btn.disabled = true;

  try {
    const res = await fetch('/api/mcp/registry/refresh', { method: 'POST' });
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    const data = await res.json();
    showToast(`Registry refreshed — ${data.count || 0} servers loaded`, 'success');
    browseLoaded = false; // allow re-fetch
    await loadSearchResults();
    await loadRegistrySources();
  } catch (err) {
    console.error('refreshRegistry error:', err);
    showToast('Failed to refresh registry', 'error');
  } finally {
    if (btn) btn.disabled = false;
  }
}

/** Show the "Add Registry Source" modal. */
function showAddSourceModal() {
  document.getElementById('sourceNameInput').value = '';
  document.getElementById('sourceUrlInput').value = '';
  const modal = new bootstrap.Modal(document.getElementById('addSourceModal'));
  modal.show();
}

/** Submit the Add Registry Source form. */
async function submitAddSource() {
  const name = document.getElementById('sourceNameInput').value.trim();
  const url = document.getElementById('sourceUrlInput').value.trim();

  if (!name || !url) {
    showToast('Name and URL are required', 'error');
    return;
  }

  try {
    const res = await fetch('/api/mcp/registry-sources', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ name, url })
    });

    if (res.ok) {
      bootstrap.Modal.getInstance(document.getElementById('addSourceModal')).hide();
      showToast(`Source "${name}" added`, 'success');
      await loadRegistrySources();
      browseLoaded = false;
      await loadSearchResults();
    } else {
      const err = await res.text();
      showToast(`Failed to add source: ${err}`, 'error');
    }
  } catch (err) {
    console.error('submitAddSource error:', err);
    showToast('Failed to add source', 'error');
  }
}

/** Delete a registry source by ID. */
async function deleteRegistrySource(id) {
  try {
    const res = await fetch(`/api/mcp/registry-sources/${id}`, { method: 'DELETE' });
    if (res.ok) {
      showToast('Source removed', 'success');
      await loadRegistrySources();
      browseLoaded = false;
      await loadSearchResults();
    } else {
      const err = await res.text();
      showToast(`Failed to remove: ${err}`, 'error');
    }
  } catch (err) {
    console.error('deleteRegistrySource error:', err);
    showToast('Failed to remove source', 'error');
  }
}

/** Toggle the registry sources collapsible panel. */
function toggleSourcesPanel() {
  sourcesPanelOpen = !sourcesPanelOpen;
  const panel = document.getElementById('sourcesPanel');
  const chevron = document.getElementById('sourcesChevron');
  if (!panel) return;

  panel.style.display = sourcesPanelOpen ? 'block' : 'none';
  if (chevron) chevron.style.transform = sourcesPanelOpen ? 'rotate(180deg)' : '';

  if (sourcesPanelOpen) {
    if (registrySources.length === 0) {
      loadRegistrySources().then(() => renderSourcesList(registrySources));
    } else {
      renderSourcesList(registrySources);
    }
  }
}

/** Render the sources list inside the collapsible panel. */
function renderSourcesList(sources) {
  const container = document.getElementById('sourcesList');
  if (!container) return;
  container.innerHTML = '';

  if (!sources || sources.length === 0) {
    container.innerHTML = '<p class="text-muted small">No sources configured.</p>';
    return;
  }

  sources.forEach(src => {
    const row = document.createElement('div');
    row.className = 'd-flex align-items-center justify-content-between py-2';
    row.style = 'border-bottom: 1px solid var(--border-color);';
    row.innerHTML = `
      <div>
        <span class="fw-semibold small" style="color: var(--text-primary);">${mcpEscapeHtml(src.name)}</span>
        ${src.is_builtin ? '<span class="badge bg-secondary ms-2" style="font-size:0.7rem;">built-in</span>' : ''}
        ${src.url ? `<br><small style="color: var(--text-secondary);">${mcpEscapeHtml(src.url)}</small>` : ''}
      </div>
      <div>
        ${!src.is_builtin
          ? `<button class="modern-btn modern-btn-danger modern-btn-sm" onclick="deleteRegistrySource('${mcpEscapeHtml(src.id)}')">Remove</button>`
          : ''
        }
      </div>`;
    container.appendChild(row);
  });
}

// ============================================================
// SERVER DETAILS — README + tools modal
// ============================================================

/**
 * Open the details modal for an installed server and load its README,
 * native MCP instructions, and live tool list from the backend.
 */
async function viewServerDetails(encodedServerName) {
  const serverName = decodeURIComponent(encodedServerName);
  const modalEl = document.getElementById('serverDetailsModal');
  if (!modalEl) return;

  const modal = bootstrap.Modal.getOrCreateInstance(modalEl);
  document.getElementById('serverDetailsTitle').textContent = serverName;
  const body = document.getElementById('serverDetailsBody');
  body.innerHTML = `
    <div class="text-center py-5">
      <div class="spinner-border text-primary" role="status"></div>
      <p class="mt-3 mb-0 small" style="color: var(--text-secondary);">Loading details…</p>
    </div>`;
  modal.show();

  // Opening details does NOT start the server — README/Info load without it.
  await loadServerDetails(serverName, false);
}

/**
 * Fetch and render the details payload. When start is true the backend will
 * start a stopped server so its live tools/instructions can be read.
 */
async function loadServerDetails(serverName, start) {
  const body = document.getElementById('serverDetailsBody');
  if (start) {
    body.innerHTML = `
      <div class="text-center py-5">
        <div class="spinner-border text-primary" role="status"></div>
        <p class="mt-3 mb-0 small" style="color: var(--text-secondary);">Starting server &amp; loading tools…</p>
      </div>`;
  }

  try {
    const url = `/api/mcp/servers/${encodeURIComponent(serverName)}/details${start ? '?start=true' : ''}`;
    const res = await fetch(url);
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    const data = await res.json();
    body.innerHTML = renderServerDetails(data);
    if (start) {
      loadServers(); // refresh the card list so the status badge reflects the now-running server
    }
  } catch (err) {
    console.error('Failed to load server details:', err);
    body.innerHTML = `<div class="alert alert-danger">Failed to load details: ${mcpEscapeHtml(err.message)}</div>`;
  }
}

/** Explicitly start a server from within the details modal, then reload tools. */
function startServerDetails(encodedServerName) {
  loadServerDetails(decodeURIComponent(encodedServerName), true);
}

/** Build the inner HTML for the details modal (tabbed: Tools / README / Info). */
function renderServerDetails(data) {
  const tools = data.tools || [];
  const hasInstructions = !!(data.instructions && data.instructions.trim());

  return `
    <ul class="nav nav-tabs mb-3" role="tablist">
      <li class="nav-item" role="presentation">
        <button class="nav-link active" data-bs-toggle="tab" data-bs-target="#detailToolsPane" type="button" role="tab">
          Tools <span class="badge bg-secondary ms-1">${tools.length}</span>
        </button>
      </li>
      <li class="nav-item" role="presentation">
        <button class="nav-link" data-bs-toggle="tab" data-bs-target="#detailReadmePane" type="button" role="tab">README</button>
      </li>
      <li class="nav-item" role="presentation">
        <button class="nav-link" data-bs-toggle="tab" data-bs-target="#detailInfoPane" type="button" role="tab">Info</button>
      </li>
    </ul>
    <div class="tab-content">
      <div class="tab-pane fade show active" id="detailToolsPane" role="tabpanel">
        ${renderDetailTools(data)}
      </div>
      <div class="tab-pane fade" id="detailReadmePane" role="tabpanel">
        ${renderDetailReadme(data.readme, hasInstructions ? data.instructions : '')}
      </div>
      <div class="tab-pane fade" id="detailInfoPane" role="tabpanel">
        ${renderDetailInfo(data)}
      </div>
    </div>`;
}

/** Render the tools list, each with collapsible parameters. */
function renderDetailTools(data) {
  const tools = data.tools || [];

  if (tools.length === 0) {
    if (data.status !== 'running') {
      const warning = data.start_error
        ? `<div class="alert alert-warning">Couldn't start the server: <code>${mcpEscapeHtml(data.start_error)}</code></div>`
        : '';
      return `
        ${warning}
        <div class="text-center py-4">
          <p style="color: var(--text-secondary);">
            This server isn't running. Tools are listed once it starts —
            but you can read the <strong>README</strong> and <strong>Info</strong> tabs without starting it.
          </p>
          <button class="modern-btn modern-btn-primary" onclick="startServerDetails('${encodeURIComponent(data.server)}')">
            Start server &amp; load tools
          </button>
        </div>`;
    }
    return '<p class="text-muted">This server is running but reported no tools.</p>';
  }

  return tools.map((tool, i) => {
    const params = renderToolParams(tool.inputSchema);
    const collapseId = `toolParams${i}`;
    return `
      <div class="modern-card p-3 mb-2">
        <div class="d-flex justify-content-between align-items-start">
          <div class="flex-grow-1">
            <code style="color: var(--text-primary); font-weight: 600;">${mcpEscapeHtml(tool.name)}</code>
            ${tool.title && tool.title !== tool.name ? `<span class="ms-2 small" style="color: var(--text-secondary);">${mcpEscapeHtml(tool.title)}</span>` : ''}
            ${tool.description ? `<p class="mb-0 mt-1 small" style="color: var(--text-secondary);">${mcpEscapeHtml(tool.description)}</p>` : ''}
          </div>
          ${params ? `
          <button class="modern-btn modern-btn-secondary modern-btn-sm flex-shrink-0 ms-2" type="button" data-bs-toggle="collapse" data-bs-target="#${collapseId}">
            Params
          </button>` : ''}
        </div>
        ${params ? `<div class="collapse mt-2" id="${collapseId}">${params}</div>` : ''}
      </div>`;
  }).join('');
}

/** Render an input-schema's properties as a small parameter list. */
function renderToolParams(schema) {
  if (!schema || typeof schema !== 'object') return '';
  const props = schema.properties || {};
  const required = new Set(schema.required || []);
  const names = Object.keys(props);
  if (names.length === 0) return '<div class="small" style="color: var(--text-secondary);">No parameters.</div>';

  return names.map(name => {
    const p = props[name] || {};
    let type = p.type || (Array.isArray(p.enum) ? 'enum' : '');
    if (Array.isArray(type)) type = type.join(' | ');
    const reqBadge = required.has(name)
      ? '<span class="badge bg-danger ms-1" style="font-size:0.65rem;">required</span>'
      : '';
    const typeBadge = type
      ? `<span class="badge bg-secondary ms-1" style="font-size:0.65rem;">${mcpEscapeHtml(type)}</span>`
      : '';
    return `
      <div class="py-1" style="border-bottom: 1px solid var(--border-color);">
        <code style="color: var(--text-primary);">${mcpEscapeHtml(name)}</code>${typeBadge}${reqBadge}
        ${p.description ? `<div class="small mt-1" style="color: var(--text-secondary);">${mcpEscapeHtml(p.description)}</div>` : ''}
      </div>`;
  }).join('');
}

/** Render the README tab: native instructions (if any) + fetched README markdown. */
function renderDetailReadme(readme, instructions) {
  let html = '';

  if (instructions && instructions.trim()) {
    html += `
      <div class="mb-4">
        <h6 style="color: var(--text-primary);">Server instructions</h6>
        <div class="mcp-markdown small">${renderMcpMarkdown(instructions)}</div>
      </div>`;
  }

  if (readme && readme.markdown) {
    const sourceLink = readme.source_url
      ? `<a href="${mcpEscapeHtml(readme.source_url)}" target="_blank" rel="noopener noreferrer" class="small">View source (${mcpEscapeHtml(readme.source || 'link')})</a>`
      : '';
    html += `
      <div class="d-flex justify-content-between align-items-center mb-2">
        <h6 class="mb-0" style="color: var(--text-primary);">README</h6>
        ${sourceLink}
      </div>
      <div class="mcp-markdown">${renderMcpMarkdown(readme.markdown)}</div>`;
  } else if (!instructions || !instructions.trim()) {
    html = '<p class="text-muted">No README or instructions found for this server. It may not publish documentation, or the package homepage is unknown.</p>';
  }

  return html;
}

/** Render the Info tab: config summary + server identity. */
function renderDetailInfo(data) {
  const info = data.server_info || {};
  const argsLine = (data.args || []).join(' ');
  const envKeys = data.env_keys || [];
  const enabledBadge = data.enabled
    ? '<span class="badge bg-primary">Enabled globally</span>'
    : '<span class="badge bg-secondary">Disabled globally</span>';

  const rows = [
    ['Status', getStatusBadge(data.status || 'stopped')],
    ['Enabled', enabledBadge],
    ['Command', `<code style="color: var(--text-primary);">${mcpEscapeHtml(data.command || '')} ${mcpEscapeHtml(argsLine)}</code>`],
    ['Transport', mcpEscapeHtml(data.transport || 'stdio')],
  ];

  if (info.name) {
    rows.push(['Reported name', mcpEscapeHtml(info.title || info.name)]);
  }
  if (info.version) {
    rows.push(['Version', mcpEscapeHtml(info.version)]);
  }
  if (envKeys.length > 0) {
    rows.push(['Environment', envKeys.map(k => `<span class="badge bg-secondary me-1">${mcpEscapeHtml(k)}</span>`).join('')]);
  }

  return `
    <table class="table table-sm align-middle mb-0">
      <tbody>
        ${rows.map(([label, value]) => `
          <tr>
            <th scope="row" class="text-nowrap" style="color: var(--text-secondary); font-weight: 600; width: 140px;">${label}</th>
            <td style="color: var(--text-primary);">${value}</td>
          </tr>`).join('')}
      </tbody>
    </table>`;
}

/** Render markdown to sanitized HTML using marked + DOMPurify, with a safe fallback. */
function renderMcpMarkdown(md) {
  if (!md) return '';
  try {
    if (window.marked && window.DOMPurify) {
      const html = window.marked.parse(md, { breaks: true, gfm: true });
      return window.DOMPurify.sanitize(html);
    }
  } catch (err) {
    console.error('Markdown render failed:', err);
  }
  return `<pre style="white-space: pre-wrap; color: var(--text-primary);">${mcpEscapeHtml(md)}</pre>`;
}

/** Safely escape HTML to prevent XSS when building innerHTML. */
function mcpEscapeHtml(str) {
  if (typeof str !== 'string') return '';
  return str.replace(/&/g, '&amp;')
            .replace(/</g, '&lt;')
            .replace(/>/g, '&gt;')
            .replace(/"/g, '&quot;')
            .replace(/'/g, '&#039;');
}
