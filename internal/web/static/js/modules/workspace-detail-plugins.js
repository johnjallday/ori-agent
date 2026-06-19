/**
 * Workspace Plugins Manager
 *
 * Owns the workspace detail page's "Plugins" tab. Plugins are installed
 * globally (see the Plugins page); a plugin is "in" a workspace when its
 * MCP servers and skills are bound to that workspace. This manager lists the
 * globally installed plugins, shows whether each is attached to the current
 * workspace, and lets the user add or remove a plugin's components.
 *
 * - Add reuses WorkspaceBootstrapReview.applySelectedPlan, the same code path
 *   the workspace-creation modal uses to install/enable a plugin and bind its
 *   components.
 * - Remove deletes the workspace MCP/skill bindings that match the plugin's
 *   components (the global plugin install is left untouched).
 *
 * Extracted to mirror WorkspaceMCPManager / WorkspaceSkillsManager. The host
 * (WorkspaceDetailPage) provides workspace, workspaceId, escapeHtml,
 * loadWorkspace, and DOM element refs.
 *
 * @module workspace-detail-plugins
 */

export class WorkspacePluginsManager {
  constructor(host) {
    this.host = host;
    this.installedPlugins = [];
    this.loaded = false;
    this.loadPromise = null;
    this.busyPlugins = new Set();
  }

  bindEvents() {
    const elements = this.host.elements;
    elements.refreshPluginsBtn?.addEventListener('click', () => this.reload());
    elements.pluginsList?.addEventListener('click', event => this.handleListClick(event));
  }

  normalize(value) {
    return String(value || '')
      .trim()
      .toLowerCase();
  }

  async loadInstalledPlugins(force = false) {
    if (!force && this.loaded) {
      return this.installedPlugins;
    }
    if (!force && this.loadPromise) {
      return this.loadPromise;
    }

    this.loadPromise = (async () => {
      const response = await fetch('/api/plugins');
      if (!response.ok) {
        const text = await response.text();
        throw new Error(text || 'Failed to load plugins');
      }
      const data = await response.json();
      this.installedPlugins = Array.isArray(data?.plugins) ? data.plugins : [];
      this.loaded = true;
      return this.installedPlugins;
    })();

    try {
      return await this.loadPromise;
    } finally {
      this.loadPromise = null;
    }
  }

  async reload() {
    try {
      await this.loadInstalledPlugins(true);
    } catch (error) {
      console.error('Failed to refresh plugins:', error);
      if (window.Toast) window.Toast.error(error.message || 'Failed to refresh plugins');
    }
    this.render();
  }

  /**
   * Render the plugins list. Uses the cached installed-plugins list (loading it
   * once on first render) and the current workspace bindings, so add/remove and
   * workspace reloads re-render attachment status without refetching the list.
   */
  render() {
    if (!this.host.elements.pluginsList) return;

    if (!this.loaded) {
      if (!this.loadPromise) {
        this.loadInstalledPlugins()
          .then(() => this.render())
          .catch(error => {
            console.error('Failed to load plugins:', error);
            this.renderError(error);
          });
      }
      this.host.elements.pluginsList.innerHTML = `
        <div class="workspace-detail-empty">Loading installed plugins...</div>
      `;
      return;
    }

    const plugins = Array.isArray(this.installedPlugins) ? this.installedPlugins : [];
    if (plugins.length === 0) {
      this.host.elements.pluginsList.innerHTML = `
        <div class="workspace-detail-empty">
          No plugins installed yet.
          <div class="workspace-detail-mcp-empty-note">Install Claude Code- or Codex-compatible plugins on the <a href="/plugins">Plugins page</a>, then add them to this workspace here.</div>
        </div>
      `;
      return;
    }

    this.host.elements.pluginsList.innerHTML = plugins
      .map(plugin => this.renderPluginCard(plugin))
      .join('');
  }

  renderError(error) {
    if (!this.host.elements.pluginsList) return;
    this.host.elements.pluginsList.innerHTML = `
      <div class="workspace-detail-empty">Failed to load plugins: ${this.host.escapeHtml(
        error?.message || 'unknown error'
      )}</div>
    `;
  }

  getBoundComponentNames(bindings, ...keys) {
    const names = new Set();
    (Array.isArray(bindings) ? bindings : []).forEach(binding => {
      for (const key of keys) {
        const value = this.normalize(binding?.[key]);
        if (value) {
          names.add(value);
          break;
        }
      }
    });
    return names;
  }

  describePluginAttachment(plugin) {
    const mcpServers = (Array.isArray(plugin?.mcp_servers) ? plugin.mcp_servers : []).filter(Boolean);
    const skills = (Array.isArray(plugin?.skills) ? plugin.skills : []).filter(Boolean);
    const total = mcpServers.length + skills.length;

    const boundMCP = this.getBoundComponentNames(
      this.host.workspace?.mcp_bindings,
      'server_name',
      'serverName'
    );
    const boundSkill = this.getBoundComponentNames(
      this.host.workspace?.skill_bindings,
      'skill_name',
      'skillName'
    );

    const bound =
      mcpServers.filter(name => boundMCP.has(this.normalize(name))).length +
      skills.filter(name => boundSkill.has(this.normalize(name))).length;

    let status = 'detached';
    if (total > 0 && bound >= total) status = 'attached';
    else if (bound > 0) status = 'partial';

    return { total, bound, mcpServers, skills, status };
  }

  renderPluginCard(plugin) {
    const name = String(plugin?.name || '').trim() || 'unknown';
    const escName = this.host.escapeHtml(name);
    const version = String(plugin?.version || '').trim();
    const description = String(plugin?.description || '').trim();
    const format = String(plugin?.format || '').trim();
    const { total, bound, mcpServers, skills, status } = this.describePluginAttachment(plugin);
    const busy = this.busyPlugins.has(this.normalize(name));

    const componentBits = [];
    if (mcpServers.length > 0) {
      componentBits.push(`${mcpServers.length} MCP server${mcpServers.length === 1 ? '' : 's'}`);
    }
    if (skills.length > 0) {
      componentBits.push(`${skills.length} skill${skills.length === 1 ? '' : 's'}`);
    }
    const componentSummary = componentBits.length > 0 ? componentBits.join(' · ') : 'No bindable components';

    const statusChip =
      status === 'attached'
        ? '<span class="workspace-detail-mcp-chip status">In this workspace</span>'
        : status === 'partial'
          ? `<span class="workspace-detail-mcp-chip status is-disabled">Partially added (${bound}/${total})</span>`
          : '<span class="workspace-detail-mcp-chip source">Not in workspace</span>';

    const chips = [
      statusChip,
      format ? `<span class="workspace-detail-mcp-chip source">${this.host.escapeHtml(format)}</span>` : '',
      `<span class="workspace-detail-mcp-chip scope">${this.host.escapeHtml(componentSummary)}</span>`,
      plugin?.enabled === false
        ? '<span class="workspace-detail-mcp-chip status is-disabled">Globally disabled</span>'
        : ''
    ]
      .filter(Boolean)
      .join('');

    let actionButton = '';
    if (total === 0) {
      actionButton = '';
    } else if (status === 'detached') {
      actionButton = `
        <button type="button"
                class="workspace-detail-panel-btn workspace-detail-panel-btn-primary"
                data-workspace-plugin-action="add"
                data-plugin-name="${escName}"
                ${busy ? 'disabled' : ''}>
          ${busy ? 'Adding…' : 'Add to workspace'}
        </button>
      `;
    } else {
      actionButton = `
        <button type="button"
                class="workspace-detail-panel-btn"
                data-workspace-plugin-action="remove"
                data-plugin-name="${escName}"
                ${busy ? 'disabled' : ''}>
          ${busy ? 'Removing…' : status === 'partial' ? 'Remove remaining' : 'Remove from workspace'}
        </button>
      `;
    }

    return `
      <div class="workspace-detail-mcp-card" data-plugin-name="${escName}">
        <div class="workspace-detail-mcp-card-top">
          <div class="workspace-detail-mcp-card-top-main">
            <div class="workspace-detail-mcp-server">
              <span>${escName}</span>
              ${version ? `<code>${this.host.escapeHtml(version)}</code>` : ''}
            </div>
            <div class="workspace-detail-mcp-meta">${this.host.escapeHtml(componentSummary)}</div>
          </div>
          ${actionButton ? `<div class="workspace-detail-mcp-card-actions">${actionButton}</div>` : ''}
        </div>
        ${description ? `<div class="workspace-detail-mcp-description">${this.host.escapeHtml(description)}</div>` : ''}
        <div class="workspace-detail-mcp-chip-row">${chips}</div>
      </div>
    `;
  }

  handleListClick(event) {
    const button = event.target.closest('[data-workspace-plugin-action]');
    if (!button) return;
    event.preventDefault();
    event.stopPropagation();

    const action = String(button.dataset.workspacePluginAction || '').trim();
    const name = String(button.dataset.pluginName || '').trim();
    if (!name) return;

    if (action === 'add') {
      this.addPlugin(name);
    } else if (action === 'remove') {
      this.removePlugin(name);
    }
  }

  findPlugin(name) {
    const normalized = this.normalize(name);
    return (
      (Array.isArray(this.installedPlugins) ? this.installedPlugins : []).find(
        plugin => this.normalize(plugin?.name) === normalized
      ) || null
    );
  }

  setPluginBusy(name, busy) {
    const key = this.normalize(name);
    if (busy) {
      this.busyPlugins.add(key);
    } else {
      this.busyPlugins.delete(key);
    }
    this.render();
  }

  async addPlugin(name) {
    const plugin = this.findPlugin(name);
    if (!plugin) return;

    if (
      !window.WorkspaceBootstrapReview ||
      typeof window.WorkspaceBootstrapReview.applySelectedPlan !== 'function'
    ) {
      if (window.Toast) window.Toast.error('Plugin setup is unavailable');
      return;
    }

    this.setPluginBusy(name, true);
    try {
      const candidate = {
        id: `plugin-${this.normalize(plugin.name).replace(/[^a-z0-9]+/g, '-')}`,
        name: String(plugin.name || '').trim(),
        action: 'attach',
        mcpServers: (Array.isArray(plugin.mcp_servers) ? plugin.mcp_servers : []).filter(Boolean),
        skills: (Array.isArray(plugin.skills) ? plugin.skills : []).filter(Boolean)
      };

      const result = await window.WorkspaceBootstrapReview.applySelectedPlan(this.host.workspaceId, {
        agents: [],
        mcps: [],
        skills: [],
        plugins: [candidate],
        queries: []
      });

      await this.host.loadWorkspace();

      const failures = Array.isArray(result?.failures) ? result.failures : [];
      if (failures.length > 0) {
        if (window.Toast) {
          window.Toast.warning(`Added ${candidate.name} with issues: ${failures[0]}`);
        }
      } else if (window.Toast) {
        window.Toast.success(`Added ${candidate.name} to this workspace`);
      }
    } catch (error) {
      console.error('Failed to add plugin to workspace:', error);
      if (window.Toast) window.Toast.error(error.message || 'Failed to add plugin to workspace');
    } finally {
      this.setPluginBusy(name, false);
    }
  }

  async removePlugin(name) {
    const plugin = this.findPlugin(name);
    if (!plugin) return;

    const label = String(plugin.name || '').trim();
    if (
      !window.confirm(
        `Remove "${label}" from this workspace? Its MCP servers and skills will be unbound here (the plugin stays installed globally).`
      )
    ) {
      return;
    }

    this.setPluginBusy(name, true);
    try {
      const mcpServers = new Set(
        (Array.isArray(plugin.mcp_servers) ? plugin.mcp_servers : [])
          .filter(Boolean)
          .map(value => this.normalize(value))
      );
      const skills = new Set(
        (Array.isArray(plugin.skills) ? plugin.skills : [])
          .filter(Boolean)
          .map(value => this.normalize(value))
      );

      const mcpBindings = (
        Array.isArray(this.host.workspace?.mcp_bindings) ? this.host.workspace.mcp_bindings : []
      ).filter(binding =>
        mcpServers.has(this.normalize(binding?.server_name || binding?.serverName))
      );
      const skillBindings = (
        Array.isArray(this.host.workspace?.skill_bindings) ? this.host.workspace.skill_bindings : []
      ).filter(binding => skills.has(this.normalize(binding?.skill_name || binding?.skillName)));

      const workspaceId = this.host.workspaceId;
      for (const binding of mcpBindings) {
        const id = String(binding?.id || '').trim();
        if (!id) continue;
        const response = await fetch(
          `/api/workspaces/${encodeURIComponent(workspaceId)}/mcp-bindings/${encodeURIComponent(id)}`,
          { method: 'DELETE' }
        );
        if (!response.ok) {
          const text = await response.text();
          throw new Error(text || 'Failed to remove MCP binding');
        }
      }
      for (const binding of skillBindings) {
        const id = String(binding?.id || '').trim();
        if (!id) continue;
        const response = await fetch(
          `/api/workspaces/${encodeURIComponent(workspaceId)}/skill-bindings/${encodeURIComponent(id)}`,
          { method: 'DELETE' }
        );
        if (!response.ok) {
          const text = await response.text();
          throw new Error(text || 'Failed to remove skill binding');
        }
      }

      await this.host.loadWorkspace();
      if (window.Toast) window.Toast.success(`Removed ${label} from this workspace`);
    } catch (error) {
      console.error('Failed to remove plugin from workspace:', error);
      if (window.Toast) window.Toast.error(error.message || 'Failed to remove plugin from workspace');
    } finally {
      this.setPluginBusy(name, false);
    }
  }
}
