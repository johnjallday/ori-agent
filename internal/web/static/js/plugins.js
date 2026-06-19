// Plugins page: install, list, enable/disable, and uninstall Claude Code- and
// Codex-compatible plugins via the /api/plugins endpoints.
(function () {
  'use strict';

  const byId = (id) => document.getElementById(id);

  function esc(s) {
    return String(s == null ? '' : s).replace(/[&<>"]/g, (c) =>
      ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' }[c])
    );
  }

  async function api(method, url, body) {
    const opts = { method, headers: { 'Content-Type': 'application/json' } };
    if (body !== undefined) opts.body = JSON.stringify(body);
    const res = await fetch(url, opts);
    const text = await res.text();
    let data = {};
    try { data = text ? JSON.parse(text) : {}; } catch (_) { data = { message: text }; }
    if (!res.ok) throw new Error(data.message || ('HTTP ' + res.status));
    return data;
  }

  window.loadPlugins = async function () {
    const list = byId('pluginList');
    try {
      const data = await api('GET', '/api/plugins');
      const plugins = (data && data.plugins) || [];
      if (!plugins.length) {
        list.innerHTML = '<p class="text-muted mb-0">No plugins installed yet.</p>';
        return;
      }
      list.innerHTML = plugins.map(renderPlugin).join('');
    } catch (e) {
      list.innerHTML = '<p class="text-danger mb-0">Failed to load plugins: ' + esc(e.message) + '</p>';
    }
  };

  function renderPlugin(p) {
    const servers = (p.mcp_servers || []).map(esc).join(', ') || '&mdash;';
    const skills = (p.skills || []).map(esc).join(', ') || '&mdash;';
    const badge = '<span class="badge bg-secondary">' + esc(p.format) + '</span>';
    const name = esc(p.name);
    return (
      '<div class="d-flex align-items-start justify-content-between border-bottom py-3">' +
      '<div>' +
      '<div class="fw-semibold">' + name + ' <span class="text-muted small">' + esc(p.version || '') + '</span> ' + badge + '</div>' +
      (p.description ? '<div class="small text-muted">' + esc(p.description) + '</div>' : '') +
      '<div class="small mt-1">MCP: ' + servers + ' &middot; Skills: ' + skills + '</div>' +
      '</div>' +
      '<div class="d-flex gap-2">' +
      '<button class="modern-btn modern-btn-secondary" onclick="pluginToggle(\'' + name + '\', ' + (p.enabled ? 'false' : 'true') + ')">' +
      (p.enabled ? 'Disable' : 'Enable') + '</button>' +
      '<button class="modern-btn modern-btn-secondary" onclick="pluginUninstall(\'' + name + '\')">Uninstall</button>' +
      '</div>' +
      '</div>'
    );
  }

  function installArgs() {
    return {
      source: byId('pluginSource').value.trim(),
      format: byId('pluginFormat').value,
    };
  }

  window.pluginPreview = async function () {
    const { source, format } = installArgs();
    if (!source) { alert('Enter a plugin source.'); return; }
    try {
      const data = await api('POST', '/api/plugins/install', { source, format, confirm: false });
      renderTrust(data.trust || {});
      byId('pluginTrust').style.display = 'block';
    } catch (e) {
      alert('Preview failed: ' + e.message);
    }
  };

  function renderTrust(t) {
    const mcp = (t.MCPCommands || []).map((c) => '<li><code>' + esc(c) + '</code></li>').join('');
    const skills = (t.Skills || []).map(esc).join(', ');
    const unsupported = (t.Unsupported || []).map((u) => '<li>' + esc(u.kind) + ': ' + esc(u.detail) + '</li>').join('');
    const warnings = (t.Warnings || []).map((w) => '<li class="text-warning">' + esc(w) + '</li>').join('');
    let html = '';
    if (mcp) html += '<div class="small fw-semibold">MCP servers (run as local commands):</div><ul class="small">' + mcp + '</ul>';
    if (skills) html += '<div class="small">Skills: ' + esc(skills) + '</div>';
    if (unsupported) html += '<div class="small fw-semibold mt-2">Skipped (not yet supported):</div><ul class="small">' + unsupported + '</ul>';
    if (warnings) html += '<ul class="small mb-0">' + warnings + '</ul>';
    byId('pluginTrustBody').innerHTML = html || '<div class="small text-muted">Nothing to register.</div>';
  }

  window.pluginConfirmInstall = async function () {
    const { source, format } = installArgs();
    try {
      await api('POST', '/api/plugins/install', { source, format, confirm: true });
      pluginCancelInstall();
      byId('pluginSource').value = '';
      loadPlugins();
    } catch (e) {
      alert('Install failed: ' + e.message);
    }
  };

  window.pluginCancelInstall = function () {
    byId('pluginTrust').style.display = 'none';
    byId('pluginTrustBody').innerHTML = '';
  };

  window.pluginToggle = async function (name, enable) {
    try {
      await api('POST', '/api/plugins/' + encodeURIComponent(name) + (enable ? '/enable' : '/disable'));
      loadPlugins();
    } catch (e) {
      alert('Failed: ' + e.message);
    }
  };

  window.pluginUninstall = async function (name) {
    if (!confirm('Uninstall ' + name + '? This removes its MCP servers and skills.')) return;
    try {
      await api('DELETE', '/api/plugins/' + encodeURIComponent(name));
      loadPlugins();
    } catch (e) {
      alert('Uninstall failed: ' + e.message);
    }
  };

  document.addEventListener('DOMContentLoaded', loadPlugins);
})();
