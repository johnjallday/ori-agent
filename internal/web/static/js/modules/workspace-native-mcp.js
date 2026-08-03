/**
 * Workspace native-MCP CLI opt-in panel.
 *
 * Renders the workspace-level toggle plus per-agent toggles for CLI-provider
 * agents (Claude Code / Codex), wiring them to the
 * /api/workspaces/{id}/native-mcp endpoints. The panel stays hidden unless the
 * workspace has at least one CLI-provider agent (the only agents the opt-in
 * affects).
 */
export class WorkspaceNativeMCPManager {
  constructor(host) {
    this.host = host;
    this.bound = false;
  }

  get workspaceId() {
    return this.host?.workspaceId || '';
  }

  bindEvents() {
    if (this.bound) return;
    const container = document.getElementById('workspace-detail-native-mcp');
    if (!container) return;
    this.bound = true;
    container.addEventListener('change', event => {
      const target = event.target;
      if (!target || target.type !== 'checkbox') return;
      const wsId = this.workspaceId;
      if (!wsId) return;
      if (target.id === 'workspace-detail-native-mcp-ws-toggle') {
        this.patch(`/api/workspaces/${encodeURIComponent(wsId)}/native-mcp`, target.checked);
      } else if (target.dataset && target.dataset.agent) {
        this.patch(
          `/api/workspaces/${encodeURIComponent(wsId)}/agents/${encodeURIComponent(target.dataset.agent)}/native-mcp`,
          target.checked
        );
      }
    });
  }

  async load() {
    const wsId = this.workspaceId;
    const container = document.getElementById('workspace-detail-native-mcp');
    if (!wsId || !container) return;
    try {
      const res = await fetch(`/api/workspaces/${encodeURIComponent(wsId)}/native-mcp`);
      if (!res.ok) {
        container.classList.add('d-none');
        return;
      }
      this.render(await res.json());
    } catch (error) {
      console.warn('Failed to load native-MCP settings:', error);
      container.classList.add('d-none');
    }
  }

  render(data) {
    const container = document.getElementById('workspace-detail-native-mcp');
    const wsToggle = document.getElementById('workspace-detail-native-mcp-ws-toggle');
    const agentsEl = document.getElementById('workspace-detail-native-mcp-agents');
    if (!container || !wsToggle || !agentsEl) return;

    const cliAgents = Array.isArray(data?.agents)
      ? data.agents.filter(a => a && a.is_cli_provider)
      : [];

    // Only surface the opt-in when a CLI-provider agent can actually use it.
    if (cliAgents.length === 0) {
      container.classList.add('d-none');
      return;
    }
    container.classList.remove('d-none');

    wsToggle.checked = !!data.workspace_enabled;
    agentsEl.innerHTML = cliAgents
      .map(
        a => `
        <label class="workspace-detail-native-mcp-agent">
          <input type="checkbox" class="form-check-input" data-agent="${escapeAttr(a.name)}" ${a.enabled ? 'checked' : ''}>
          <span>${escapeHtml(a.name)} <small class="text-muted">(${escapeHtml(a.provider || 'unknown')})</small></span>
        </label>`
      )
      .join('');
  }

  async patch(url, enabled) {
    try {
      const res = await fetch(url, {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ enabled: !!enabled })
      });
      if (!res.ok) {
        console.warn('native-MCP update failed:', res.status);
      }
    } catch (error) {
      console.warn('native-MCP update error:', error);
    }
  }
}

function escapeHtml(value) {
  return String(value == null ? '' : value)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;');
}

function escapeAttr(value) {
  return escapeHtml(value).replace(/'/g, '&#39;');
}
