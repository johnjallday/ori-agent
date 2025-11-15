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
  initializeDarkMode();
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

  div.innerHTML = `
    <div class="d-flex justify-content-between align-items-start">
      <div class="flex-grow-1">
        <h6 class="mb-1" style="color: var(--text-primary);">${server.name}</h6>
        <p class="mb-1 small text-muted">${server.command} ${argsDisplay}</p>
        <div class="mt-2">
          ${statusBadge}
          ${toolCountBadge}
        </div>
      </div>
      <div class="d-flex gap-2">
        <button class="modern-btn modern-btn-secondary modern-btn-sm" onclick="testConnection('${server.name}')">
          Test
        </button>
        ${server.status === 'error' ? `
          <button class="modern-btn modern-btn-warning modern-btn-sm" onclick="retryConnection('${server.name}')">
            Retry
          </button>
        ` : ''}
        <button class="modern-btn modern-btn-danger modern-btn-sm" onclick="confirmRemoveServer('${server.name}')">
          Remove
        </button>
      </div>
    </div>
  `;

  return div;
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

function initializeDarkMode() {
  const darkModeToggle = document.getElementById('darkModeToggle');
  const html = document.documentElement;

  // Check saved preference
  const savedTheme = localStorage.getItem('theme') || 'light';
  html.setAttribute('data-bs-theme', savedTheme);
  updateDarkModeButton(savedTheme);

  darkModeToggle.addEventListener('click', function() {
    const currentTheme = html.getAttribute('data-bs-theme');
    const newTheme = currentTheme === 'dark' ? 'light' : 'dark';
    html.setAttribute('data-bs-theme', newTheme);
    localStorage.setItem('theme', newTheme);
    updateDarkModeButton(newTheme);
  });
}

function updateDarkModeButton(theme) {
  const button = document.getElementById('darkModeToggle');
  const span = button.querySelector('span');
  span.textContent = theme === 'dark' ? 'Light' : 'Dark';
}

// Cleanup on page unload
window.addEventListener('beforeunload', function() {
  if (statusPollInterval) {
    clearInterval(statusPollInterval);
  }
});
