// MCP (Model Context Protocol) Management Module

// Load MCP servers on page load
document.addEventListener('DOMContentLoaded', function() {
  if (document.getElementById('mcpServersList')) {
    loadMcpServers();
  }
});

// Add MCP Server button
document.getElementById('addMcpServerBtn')?.addEventListener('click', function() {
  const modal = new bootstrap.Modal(document.getElementById('addMcpServerModal'));
  modal.show();
});

// Save MCP Server
document.getElementById('saveMcpServerBtn')?.addEventListener('click', async function() {
  const name = document.getElementById('mcpServerName').value.trim();
  const command = document.getElementById('mcpServerCommand').value.trim();
  const argsText = document.getElementById('mcpServerArgs').value.trim();
  const envText = document.getElementById('mcpServerEnv').value.trim();

  if (!name || !command) {
    alert('Please enter server name and command');
    return;
  }

  // Parse arguments (one per line)
  const args = argsText.split('\n').map(a => a.trim()).filter(a => a);

  // Parse environment variables (KEY=value format)
  const env = {};
  if (envText) {
    envText.split('\n').forEach(line => {
      const [key, ...valueParts] = line.split('=');
      if (key && valueParts.length > 0) {
        env[key.trim()] = valueParts.join('=').trim();
      }
    });
  }

  const serverConfig = {
    name: name,
    command: command,
    args: args,
    env: env,
    transport: 'stdio',
    enabled: false
  };

  try {
    const response = await fetch('/api/mcp/servers', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(serverConfig)
    });

    if (response.ok) {
      alert('MCP server added successfully!');
      bootstrap.Modal.getInstance(document.getElementById('addMcpServerModal')).hide();
      document.getElementById('addMcpServerForm').reset();
      loadMcpServers();
    } else {
      const error = await response.text();
      alert('Failed to add server: ' + error);
    }
  } catch (error) {
    console.error('Error adding MCP server:', error);
    alert('Error adding server: ' + error.message);
  }
});

// Load and display MCP servers
async function loadMcpServers() {
  const container = document.getElementById('mcpServersList');
  if (!container) return;

  container.innerHTML = '<div class="text-center py-4" style="color: var(--text-secondary);"><div class="spinner-border spinner-border-sm me-2"></div>Loading...</div>';

  try {
    const response = await fetch('/api/mcp/servers');
    if (!response.ok) throw new Error('Failed to load servers');

    const data = await response.json();
    const servers = data.servers || [];
    const stats = data.stats || {};

    if (servers.length === 0) {
      container.innerHTML = '<div class="text-center py-4" style="color: var(--text-secondary);">No MCP servers configured. Click "Add MCP Server" to get started.</div>';
      return;
    }

    // Render server list
    let html = '';
    for (const server of servers) {
      const stat = stats[server.name] || {};
      const status = stat.status || 'stopped';
      const toolCount = stat.tool_count || 0;
      const enabled = stat.enabled || false;

      const statusBadge = status === 'running'
        ? '<span class="badge bg-success">Running</span>'
        : status === 'error'
        ? '<span class="badge bg-danger">Error</span>'
        : '<span class="badge bg-secondary">Stopped</span>';

      html += `
        <div class="card mb-2" style="background: var(--bg-secondary); border: 1px solid var(--border-color);">
          <div class="card-body">
            <div class="d-flex justify-content-between align-items-start">
              <div class="flex-grow-1">
                <h6 class="mb-1" style="color: var(--text-primary);">${escapeHtml(server.name)}</h6>
                <div class="text-muted small mb-2">
                  <code>${escapeHtml(server.command)} ${(server.args || []).join(' ')}</code>
                </div>
                <div class="d-flex gap-2 align-items-center">
                  ${statusBadge}
                  ${toolCount > 0 ? `<span class="badge bg-info">${toolCount} tools</span>` : ''}
                  ${enabled ? '<span class="badge bg-primary">Enabled</span>' : ''}
                </div>
              </div>
              <div class="btn-group btn-group-sm">
                <button class="btn btn-outline-secondary btn-sm" onclick="toggleMcpServer('${escapeHtml(server.name)}', ${enabled})" title="${enabled ? 'Disable' : 'Enable'}">
                  ${enabled ? 'Disable' : 'Enable'}
                </button>
                <button class="btn btn-outline-info btn-sm" onclick="viewMcpServerDetails('${escapeHtml(server.name)}')" title="View Details">
                  <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
                    <path d="M12,9A3,3 0 0,0 9,12A3,3 0 0,0 12,15A3,3 0 0,0 15,12A3,3 0 0,0 12,9M12,17A5,5 0 0,1 7,12A5,5 0 0,1 12,7A5,5 0 0,1 17,12A5,5 0 0,1 12,17M12,4.5C7,4.5 2.73,7.61 1,12C2.73,16.39 7,19.5 12,19.5C17,19.5 21.27,16.39 23,12C21.27,7.61 17,4.5 12,4.5Z"/>
                  </svg>
                </button>
                <button class="btn btn-outline-danger btn-sm" onclick="removeMcpServer('${escapeHtml(server.name)}')" title="Remove">
                  <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
                    <path d="M19,4H15.5L14.5,3H9.5L8.5,4H5V6H19M6,19A2,2 0 0,0 8,21H16A2,2 0 0,0 18,19V7H6V19Z"/>
                  </svg>
                </button>
              </div>
            </div>
          </div>
        </div>
      `;
    }

    container.innerHTML = html;
  } catch (error) {
    console.error('Error loading MCP servers:', error);
    container.innerHTML = '<div class="alert alert-danger">Error loading servers: ' + error.message + '</div>';
  }
}

// Toggle server enabled/disabled for current agent
async function toggleMcpServer(serverName, currentlyEnabled) {
  const action = currentlyEnabled ? 'disable' : 'enable';

  try {
    const response = await fetch(`/api/mcp/servers/${serverName}/${action}`, {
      method: 'POST'
    });

    if (response.ok) {
      loadMcpServers();
    } else {
      const error = await response.text();
      alert(`Failed to ${action} server: ` + error);
    }
  } catch (error) {
    console.error(`Error ${action}ing server:`, error);
    alert(`Error ${action}ing server: ` + error.message);
  }
}

// View server details and tools
async function viewMcpServerDetails(serverName) {
  const modal = new bootstrap.Modal(document.getElementById('mcpServerDetailsModal'));
  const titleEl = document.getElementById('mcpServerDetailsTitle');
  const bodyEl = document.getElementById('mcpServerDetailsBody');

  titleEl.textContent = serverName;
  bodyEl.innerHTML = '<div class="text-center py-4"><div class="spinner-border"></div></div>';
  modal.show();

  try {
    // Load server tools
    const toolsResponse = await fetch(`/api/mcp/servers/${serverName}/tools`);
    if (!toolsResponse.ok) throw new Error('Failed to load server tools');

    const toolsData = await toolsResponse.json();
    const tools = toolsData.tools || [];

    let html = '<h6 class="mb-3">Available Tools</h6>';

    if (tools.length === 0) {
      html += '<p class="text-muted">No tools available or server not running.</p>';
    } else {
      html += '<div class="list-group">';
      for (const tool of tools) {
        html += `
          <div class="list-group-item">
            <div class="d-flex w-100 justify-content-between">
              <h6 class="mb-1"><code>${escapeHtml(tool.name)}</code></h6>
            </div>
            ${tool.description ? `<p class="mb-1 small">${escapeHtml(tool.description)}</p>` : ''}
          </div>
        `;
      }
      html += '</div>';
    }

    bodyEl.innerHTML = html;
  } catch (error) {
    console.error('Error loading server details:', error);
    bodyEl.innerHTML = '<div class="alert alert-danger">Error loading details: ' + error.message + '</div>';
  }
}

// Remove MCP server
async function removeMcpServer(serverName) {
  if (!confirm(`Are you sure you want to remove the "${serverName}" MCP server?`)) {
    return;
  }

  try {
    const response = await fetch(`/api/mcp/servers/${serverName}`, {
      method: 'DELETE'
    });

    if (response.ok) {
      loadMcpServers();
    } else {
      const error = await response.text();
      alert('Failed to remove server: ' + error);
    }
  } catch (error) {
    console.error('Error removing server:', error);
    alert('Error removing server: ' + error.message);
  }
}

// Utility function to escape HTML
function escapeHtml(text) {
  const div = document.createElement('div');
  div.textContent = text;
  return div.innerHTML;
}

// Make functions globally available
window.toggleMcpServer = toggleMcpServer;
window.viewMcpServerDetails = viewMcpServerDetails;
window.removeMcpServer = removeMcpServer;
