// Shared rendering for the read-only Codex (~/.codex) synced section.
// Used by the agent details drawer (agents dashboard) and the dedicated Codex
// agent page. Exposes window.CodexSync.
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

  // A detail response describes the Codex agent when it is the Codex CLI backend
  // (provider "codex" + role "cli_agent").
  function isCodexAgent(detail) {
    const provider = String(detail?.provider || '').toLowerCase();
    const role = String(detail?.role || '').toLowerCase();
    return provider === 'codex' && role === 'cli_agent';
  }

  function listCard(label, items, itemHtml, emptyText) {
    let body;
    if (!Array.isArray(items) || items.length === 0) {
      body = `<div class="codex-sync-value codex-sync-empty">${esc(emptyText)}</div>`;
    } else {
      body = `<ul class="codex-sync-list">${items.map((item) => `<li>${itemHtml(item)}</li>`).join('')}</ul>`;
    }
    return `
      <div class="codex-sync-card">
        <span class="codex-sync-label">${esc(label)}</span>
        ${body}
      </div>`;
  }

  function renderMCPServer(server) {
    const meta = [];
    if (server?.transport) {
      meta.push(esc(server.transport));
    }
    const envNames = Array.isArray(server?.envNames) ? server.envNames.filter(Boolean) : [];
    if (envNames.length > 0) {
      meta.push(`env: ${esc(envNames.join(', '))}`);
    }
    const metaHtml = meta.length > 0
      ? ` <span class="codex-sync-muted">(${meta.join(' / ')})</span>`
      : '';
    return `<strong>${esc(server?.name || 'Unnamed')}</strong>${metaHtml}`;
  }

  // Renders the synced section HTML for a codex_sync payload (or null when the
  // reader is disabled / ~/.codex is absent).
  function renderHtml(sync) {
    const actions = `
      <div class="codex-sync-actions">
        <button type="button" class="modern-btn modern-btn-secondary codex-sync-refresh">Refresh from ~/.codex</button>
        <span class="codex-sync-readonly">Read-only mirror of your Codex setup</span>
      </div>`;

    if (!sync) {
      return `
        <div class="codex-sync">
          ${actions}
          <div class="codex-sync-card">
            <span class="codex-sync-label">Codex sync</span>
            <div class="codex-sync-value codex-sync-empty">Not available. Enable Codex agents in Settings, or install the Codex CLI - synced details from ~/.codex will appear here.</div>
          </div>
        </div>`;
    }

    const config = sync.config || {};
    const model = config.model || sync.model || '';
    const reasoning = config.modelReasoningEffort || '';

    const agents = Array.isArray(sync.agents) ? sync.agents : [];
    const mcpServers = Array.isArray(sync.mcpServers) ? sync.mcpServers : [];
    const skills = Array.isArray(sync.skills) ? sync.skills : [];
    const rules = Array.isArray(sync.rules) ? sync.rules : [];

    return `
      <div class="codex-sync">
        ${actions}
        <div class="codex-sync-card">
          <span class="codex-sync-label">Model &amp; Settings</span>
          <div class="codex-sync-value">
            <div>Model: ${esc(model || 'Not configured')}</div>
            <div>Reasoning effort: ${esc(reasoning || '-')}</div>
          </div>
        </div>
        ${listCard(`Skill Agents (${agents.length})`, agents, (a) =>
          `<strong>${esc(a?.name || 'Unnamed')}</strong>${a?.description ? ' - ' + esc(a.description) : ''}`,
          'No Codex skill agents found in ~/.codex/skills.')}
        ${listCard(`MCP Servers (${mcpServers.length})`, mcpServers, renderMCPServer, 'No MCP servers configured.')}
        ${listCard(`Skill Folders (${skills.length})`, skills, (s) =>
          `<strong>${esc(s?.name || 'Unnamed')}</strong>${s?.path ? ` <span class="codex-sync-path">${esc(s.path)}</span>` : ''}`,
          'No Codex skill folders found.')}
        ${listCard(`Rules (${rules.length})`, rules, (r) =>
          `<strong>${esc(r?.name || 'Unnamed')}</strong>${r?.path ? ` <span class="codex-sync-path">${esc(r.path)}</span>` : ''}`,
          'No Codex rules found.')}
      </div>`;
  }

  // Wires the "Refresh from ~/.codex" button inside root: reloads the cache,
  // re-fetches the agent detail, and calls onReloaded(detail, agentName).
  function wireRefresh(root, agentName, onReloaded) {
    if (!root) {
      return;
    }
    const btn = root.querySelector('.codex-sync-refresh');
    if (!btn) {
      return;
    }

    btn.addEventListener('click', async () => {
      const originalText = btn.textContent;
      btn.disabled = true;
      btn.textContent = 'Refreshing...';

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
          window.Toast.success('Codex data refreshed.');
        }
      } catch (error) {
        console.error('Codex refresh failed:', error);
        if (window.Toast) {
          window.Toast.error(error?.message || 'Failed to refresh Codex data.');
        }
        btn.disabled = false;
        btn.textContent = originalText;
      }
    });
  }

  window.CodexSync = { isCodexAgent, renderHtml, wireRefresh };
})();
