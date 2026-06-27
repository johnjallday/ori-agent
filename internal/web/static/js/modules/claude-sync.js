// Shared rendering for the read-only Claude Code (~/.claude) synced section.
// Used by the agent details drawer (agents dashboard) and the dedicated Claude
// Code agent page. Exposes window.ClaudeSync.
(function () {
  function esc(value) {
    if (typeof window.safeEscapeHtml === 'function') {
      return window.safeEscapeHtml(value);
    }
    if (typeof window.escapeHtml === 'function') {
      return window.escapeHtml(value);
    }
    return String(value == null ? '' : value).replace(/[&<>"']/g, (c) =>
      ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c])
    );
  }

  // A detail response describes the Claude Code agent when it is the Claude CLI
  // backend (provider "claude" + role "cli_agent").
  function isClaudeCodeAgent(detail) {
    const provider = String(detail?.provider || '').toLowerCase();
    const role = String(detail?.role || '').toLowerCase();
    return provider === 'claude' && role === 'cli_agent';
  }

  function listCard(label, items, itemHtml, emptyText) {
    let body;
    if (!Array.isArray(items) || items.length === 0) {
      body = `<div class="claude-sync-value claude-sync-empty">${esc(emptyText)}</div>`;
    } else {
      body = `<ul class="claude-sync-list">${items.map((item) => `<li>${itemHtml(item)}</li>`).join('')}</ul>`;
    }
    return `
      <div class="claude-sync-card">
        <span class="claude-sync-label">${esc(label)}</span>
        ${body}
      </div>`;
  }

  // Renders the synced section HTML for a claude_sync payload (or null when the
  // reader is disabled / ~/.claude is absent).
  function renderHtml(sync) {
    const actions = `
      <div class="claude-sync-actions">
        <button type="button" class="modern-btn modern-btn-secondary claude-sync-refresh">Refresh from ~/.claude</button>
        <span class="claude-sync-readonly">Read-only mirror of your Claude Code setup</span>
      </div>`;

    if (!sync) {
      return `
        <div class="claude-sync">
          ${actions}
          <div class="claude-sync-card">
            <span class="claude-sync-label">Claude Code sync</span>
            <div class="claude-sync-value claude-sync-empty">Not available. Enable Claude Code agents in Settings, or install the Claude Code CLI — synced details from ~/.claude will appear here.</div>
          </div>
        </div>`;
    }

    const settings = sync.settings || {};
    const model = settings.model || sync.model || '';
    const defaultMode = (settings.permissions && settings.permissions.defaultMode) || '';

    const agents = Array.isArray(sync.agents) ? sync.agents : [];
    const mcpServers = Array.isArray(sync.mcpServers) ? sync.mcpServers : [];
    const plugins = Array.isArray(sync.plugins) ? sync.plugins : [];
    const recent = Array.isArray(sync.recentProjects) ? sync.recentProjects : [];

    return `
      <div class="claude-sync">
        ${actions}
        <div class="claude-sync-card">
          <span class="claude-sync-label">Model &amp; Settings</span>
          <div class="claude-sync-value">
            <div>Model: ${esc(model || 'Not configured')}</div>
            <div>Permission mode: ${esc(defaultMode || '—')}</div>
          </div>
        </div>
        ${listCard(`Subagents (${agents.length})`, agents, (a) =>
          `<strong>${esc(a?.name || 'Unnamed')}</strong>${a?.description ? ' — ' + esc(a.description) : ''}`,
          'No subagents found in ~/.claude/agents.')}
        ${listCard(`MCP Servers (${mcpServers.length})`, mcpServers, (s) =>
          `<strong>${esc(s?.name || 'Unnamed')}</strong>${s?.transport ? ` <span class="claude-sync-muted">(${esc(s.transport)})</span>` : ''}`,
          'No MCP servers configured.')}
        ${listCard(`Plugins (${plugins.length})`, plugins, (p) =>
          esc(p?.name || 'Unnamed'),
          'No plugins installed.')}
        ${listCard(`Recent Projects (${recent.length})`, recent, (p) => {
          const cost = Number(p?.lastCost || 0);
          const costStr = cost > 0 ? ` <span class="claude-sync-muted">$${cost.toFixed(4)}</span>` : '';
          return `<span class="claude-sync-path">${esc(p?.path || '')}</span>${costStr}`;
        }, 'No recent projects.')}
      </div>`;
  }

  // Wires the "Refresh from ~/.claude" button inside root: reloads the cache,
  // re-fetches the agent detail, and calls onReloaded(detail, agentName).
  function wireRefresh(root, agentName, onReloaded) {
    if (!root) {
      return;
    }
    const btn = root.querySelector('.claude-sync-refresh');
    if (!btn) {
      return;
    }

    btn.addEventListener('click', async () => {
      const originalText = btn.textContent;
      btn.disabled = true;
      btn.textContent = 'Refreshing…';

      try {
        const resp = await fetch('/api/external-agents/refresh', { method: 'POST' });
        if (!resp.ok) {
          throw new Error(`Refresh failed (${resp.status})`);
        }
        const detailResp = await fetch(`/api/agents/${encodeURIComponent(agentName)}/detail`);
        if (!detailResp.ok) {
          throw new Error(`Failed to reload (${detailResp.status})`);
        }
        const detail = await detailResp.json();
        if (typeof onReloaded === 'function') {
          onReloaded(detail, agentName);
        }
        if (window.Toast) {
          window.Toast.success('Claude Code data refreshed.');
        }
      } catch (error) {
        console.error('Claude refresh failed:', error);
        if (window.Toast) {
          window.Toast.error(error?.message || 'Failed to refresh Claude Code data.');
        }
        btn.disabled = false;
        btn.textContent = originalText;
      }
    });
  }

  window.ClaudeSync = { isClaudeCodeAgent, renderHtml, wireRefresh };
})();
