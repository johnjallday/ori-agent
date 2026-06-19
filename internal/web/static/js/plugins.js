// Plugins page: install (by source or from a marketplace), list, enable/disable,
// and uninstall Claude Code- and Codex-compatible plugins via /api/plugins.
(function () {
  'use strict';

  const byId = (id) => document.getElementById(id);

  function esc(s) {
    return String(s == null ? '' : s).replace(/[&<>"']/g, (c) =>
      ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c])
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

  // ---- shared trust disclosure (used by source-install and marketplace-install) ----

  let pendingConfirm = null;

  function showTrust(report, onConfirm) {
    renderTrustBody(report || {});
    pendingConfirm = onConfirm;
    byId('pluginTrust').style.display = 'block';
  }

  window.pluginConfirmInstall = async function () {
    if (!pendingConfirm) return;
    const fn = pendingConfirm;
    pendingConfirm = null;
    try { await fn(); } catch (e) { alert('Install failed: ' + e.message); }
  };

  window.pluginCancelInstall = function () {
    pendingConfirm = null;
    byId('pluginTrust').style.display = 'none';
    byId('pluginTrustBody').innerHTML = '';
  };

  function renderTrustBody(t) {
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

  // ---- installed plugins ----

  window.loadPlugins = async function () {
    const list = byId('pluginList');
    try {
      const data = await api('GET', '/api/plugins');
      const plugins = (data && data.plugins) || [];
      list.innerHTML = plugins.length ? plugins.map(renderPlugin).join('') : '<p class="text-muted mb-0">No plugins installed yet.</p>';
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
      '<button class="modern-btn modern-btn-secondary" onclick="pluginUpdate(\'' + name + '\')">Update</button>' +
      '<button class="modern-btn modern-btn-secondary" onclick="pluginUninstall(\'' + name + '\')">Uninstall</button>' +
      '</div>' +
      '</div>'
    );
  }

  // ---- install by source ----

  window.pluginPreview = async function () {
    const source = byId('pluginSource').value.trim();
    const format = byId('pluginFormat').value;
    if (!source) { alert('Enter a plugin source.'); return; }
    try {
      const data = await api('POST', '/api/plugins/install', { source, format, confirm: false });
      showTrust(data.trust, async () => {
        await api('POST', '/api/plugins/install', { source, format, confirm: true });
        window.pluginCancelInstall();
        byId('pluginSource').value = '';
        loadPlugins();
      });
    } catch (e) {
      alert('Preview failed: ' + e.message);
    }
  };

  window.pluginToggle = async function (name, enable) {
    try {
      await api('POST', '/api/plugins/' + encodeURIComponent(name) + (enable ? '/enable' : '/disable'));
      loadPlugins();
    } catch (e) { alert('Failed: ' + e.message); }
  };

  window.pluginUninstall = async function (name) {
    if (!confirm('Uninstall ' + name + '? This removes its MCP servers and skills.')) return;
    try {
      await api('DELETE', '/api/plugins/' + encodeURIComponent(name));
      loadPlugins();
    } catch (e) { alert('Uninstall failed: ' + e.message); }
  };

  window.pluginUpdate = async function (name) {
    const url = '/api/plugins/' + encodeURIComponent(name) + '/update';
    try {
      const data = await api('POST', url, { confirm: false });
      const doUpdate = async () => {
        await api('POST', url, { confirm: true });
        window.pluginCancelInstall();
        loadPlugins();
      };
      if (data.changed) {
        showTrust(data.trust, doUpdate); // re-prompt: the registered components changed
      } else {
        await doUpdate();
      }
    } catch (e) {
      alert('Update failed: ' + e.message);
    }
  };

  // ---- marketplaces ----

  window.loadMarketplaces = async function () {
    const el = byId('marketplaceList');
    if (!el) return;
    try {
      const data = await api('GET', '/api/plugins/marketplaces');
      const mps = (data && data.marketplaces) || [];
      el.innerHTML = mps.length ? mps.map(renderMarketplace).join('') : '<p class="text-muted mb-0">No marketplaces added.</p>';
    } catch (e) {
      el.innerHTML = '<p class="text-danger mb-0">Failed to load marketplaces: ' + esc(e.message) + '</p>';
    }
  };

  function renderMarketplace(mp) {
    const name = esc(mp.name);
    const entries = (mp.plugins || []).map((e) =>
      '<li class="d-flex justify-content-between align-items-center py-1">' +
      '<span>' + esc(e.name) + (e.description ? ' <span class="text-muted small">' + esc(e.description) + '</span>' : '') + '</span>' +
      '<button class="modern-btn modern-btn-secondary" onclick="marketplaceInstall(\'' + name + '\', \'' + esc(e.name) + '\')">Install</button>' +
      '</li>'
    ).join('');
    return (
      '<div class="border-bottom py-2">' +
      '<div class="fw-semibold">' + name + ' <span class="text-muted small">' + esc(mp.source || '') + '</span></div>' +
      '<ul class="list-unstyled mb-0">' + (entries || '<li class="text-muted small">No plugins listed.</li>') + '</ul>' +
      '</div>'
    );
  }

  window.addMarketplace = async function () {
    const source = byId('marketplaceSource').value.trim();
    if (!source) { alert('Enter a marketplace source.'); return; }
    try {
      await api('POST', '/api/plugins/marketplaces', { source });
      byId('marketplaceSource').value = '';
      window.loadMarketplaces();
    } catch (e) {
      alert('Add marketplace failed: ' + e.message);
    }
  };

  window.marketplaceInstall = async function (marketplace, pluginName) {
    try {
      const data = await api('POST', '/api/plugins/marketplaces/install', { marketplace, plugin: pluginName, confirm: false });
      showTrust(data.trust, async () => {
        await api('POST', '/api/plugins/marketplaces/install', { marketplace, plugin: pluginName, confirm: true });
        window.pluginCancelInstall();
        loadPlugins();
      });
    } catch (e) {
      alert('Preview failed: ' + e.message);
    }
  };

  document.addEventListener('DOMContentLoaded', function () {
    loadPlugins();
    window.loadMarketplaces();
  });
})();
