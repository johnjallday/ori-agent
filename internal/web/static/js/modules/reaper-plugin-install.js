// reaper-plugin-install.js — the shared inline reaper-plugin install flow used by
// both the Create Workspace REAPER Setup card and the workspace-detail repair
// card. It owns a single host element, into which it renders a status line, the
// trust disclosure, and the confirm/cancel (or source-input) controls, then
// installs + enables the plugin and calls onComplete so the caller re-checks.
//
// Resolution order: a template-declared source (one click), else a configured
// marketplace, else a pasted source. The trust preview is always shown before
// anything installs — nothing installs silently.
(function () {
  'use strict';

  const PLUGIN_NAME = 'reaper-plugin';
  let busy = false;

  async function fetchJSON(url, opts) {
    const resp = await fetch(url, opts);
    let data = {};
    try {
      data = await resp.json();
    } catch (_) {
      data = {};
    }
    if (!resp.ok) throw new Error(data.error || 'request failed: ' + resp.status);
    return data;
  }

  function postJSON(body) {
    return {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body)
    };
  }

  // panel clears the host and returns a single column container to render into,
  // so the install UI lays out cleanly regardless of the host's own flex rules
  // (both cards' action rows are horizontal flex).
  function panel(host) {
    host.textContent = '';
    const p = document.createElement('div');
    p.className = 'reaper-install-panel';
    p.style.cssText = 'display:flex;flex-direction:column;gap:6px;width:100%;';
    host.appendChild(p);
    return p;
  }

  function statusLine(c, text, primaryText) {
    const d = document.createElement('div');
    d.className = 'reaper-install-status';
    d.style.cssText = primaryText
      ? 'font-size:12px;color:var(--text-primary);'
      : 'font-size:12px;color:var(--text-secondary);';
    d.textContent = text;
    c.appendChild(d);
    return d;
  }

  function line(c, text) {
    const d = document.createElement('div');
    d.style.cssText = 'font-size:11px;color:var(--text-secondary);';
    d.textContent = text;
    c.appendChild(d);
    return d;
  }

  function bar(c) {
    const b = document.createElement('div');
    b.style.cssText = 'display:flex;flex-wrap:wrap;gap:6px;align-items:center;';
    c.appendChild(b);
    return b;
  }

  function button(c, label, primary, onClick) {
    const b = document.createElement('button');
    b.type = 'button';
    b.className = 'modern-btn ' + (primary ? 'modern-btn-primary' : 'modern-btn-secondary');
    b.style.fontSize = '12px';
    b.textContent = label;
    if (onClick) b.addEventListener('click', onClick);
    c.appendChild(b);
    return b;
  }

  async function resolveMarketplace() {
    try {
      const data = await fetchJSON('/api/plugins/marketplaces');
      const list = Array.isArray(data.marketplaces) ? data.marketplaces : [];
      for (const mp of list) {
        const plugins = Array.isArray(mp.plugins) ? mp.plugins : [];
        const hit = plugins.find(p => String(p.name || '').toLowerCase() === PLUGIN_NAME);
        if (hit) return { marketplace: mp.name, plugin: hit.name || PLUGIN_NAME };
      }
    } catch (_) {
      /* fall through */
    }
    return null;
  }

  function message(host, text) {
    const c = panel(host);
    statusLine(c, text);
    return c;
  }

  // renderTrust shows the disclosure and an install confirm, so nothing installs
  // without the user seeing what it registers.
  function renderTrust(host, trust, sourceLabel, doInstall, opts) {
    const t = trust || {};
    const c = panel(host);
    statusLine(c, 'Install ' + (t.Name || PLUGIN_NAME) + ' from ' + sourceLabel + '?', true);
    const skills = Array.isArray(t.Skills) ? t.Skills : [];
    const cmds = Array.isArray(t.MCPCommands) ? t.MCPCommands : [];
    const warnings = Array.isArray(t.Warnings) ? t.Warnings : [];
    if (skills.length) line(c, 'Skills: ' + skills.join(', '));
    if (cmds.length) line(c, 'Runs: ' + cmds.join('; '));
    if (!skills.length && !cmds.length) line(c, 'Registers reaper-plugin components.');
    warnings.forEach(wtext => {
      const wl = line(c, '⚠ ' + wtext);
      wl.style.color = 'var(--warning-color, #d97706)';
    });
    const b = bar(c);
    button(b, 'Install & enable', true, () => doInstall());
    button(b, 'Cancel', false, () => finishCancel(opts));
  }

  function renderSourceInput(host, opts, keepStatus) {
    const c = panel(host);
    if (!keepStatus) {
      statusLine(
        c,
        'No configured marketplace has reaper-plugin. Paste its source to install it here.'
      );
    }
    line(
      c,
      'A GitHub repo URL or local path to reaper-plugin. The trust preview is shown before anything installs.'
    );
    const b = bar(c);
    const input = document.createElement('input');
    input.type = 'text';
    input.className = 'modern-input';
    input.placeholder = 'e.g. https://github.com/owner/reaper-plugin.git';
    input.style.cssText = 'flex:1 1 220px;font-size:12px;';
    input.setAttribute('aria-label', 'reaper-plugin source');
    input.addEventListener('keydown', e => {
      if (e.key === 'Enter') previewSource(host, input.value, opts);
    });
    b.appendChild(input);
    button(b, 'Preview install', true, () => previewSource(host, input.value, opts));
    button(b, 'Open Plugins page', false, () =>
      window.open('/plugins?install=' + encodeURIComponent(PLUGIN_NAME), '_blank', 'noopener')
    );
    input.focus?.();
  }

  async function previewSource(host, source, opts) {
    source = String(source || '').trim();
    if (!source || busy) return;
    busy = true;
    message(host, 'Checking ' + source + '…');
    try {
      const prev = await fetchJSON('/api/plugins/install', postJSON({ source, confirm: false }));
      renderTrust(
        host,
        prev.trust,
        source,
        () =>
          doInstall(
            host,
            () => fetchJSON('/api/plugins/install', postJSON({ source, confirm: true })),
            opts
          ),
        opts
      );
    } catch (e) {
      renderSourceInputWithError(
        host,
        opts,
        'Could not read that source: ' + (e.message || 'unknown error')
      );
    } finally {
      busy = false;
    }
  }

  function renderSourceInputWithError(host, opts, msg) {
    const c = panel(host);
    const s = statusLine(c, msg);
    s.style.color = 'var(--warning-color, #d97706)';
    line(c, 'A GitHub repo URL or local path to reaper-plugin.');
    const b = bar(c);
    const input = document.createElement('input');
    input.type = 'text';
    input.className = 'modern-input';
    input.placeholder = 'e.g. https://github.com/owner/reaper-plugin.git';
    input.style.cssText = 'flex:1 1 220px;font-size:12px;';
    input.setAttribute('aria-label', 'reaper-plugin source');
    input.addEventListener('keydown', e => {
      if (e.key === 'Enter') previewSource(host, input.value, opts);
    });
    b.appendChild(input);
    button(b, 'Preview install', true, () => previewSource(host, input.value, opts));
  }

  async function doInstall(host, installFn, opts) {
    if (busy) return;
    busy = true;
    message(host, 'Installing reaper-plugin…');
    try {
      await installFn();
      // Installs land disabled; enable so the plugin will attach.
      try {
        await fetch('/api/plugins/' + encodeURIComponent(PLUGIN_NAME) + '/enable', {
          method: 'POST'
        });
      } catch (_) {
        /* best effort; the caller's re-check surfaces a disabled state */
      }
      finishComplete(opts);
    } catch (e) {
      const c = message(host, 'Install failed: ' + (e.message || 'unknown error'));
      button(bar(c), 'Retry', false, () => begin(opts));
    } finally {
      busy = false;
    }
  }

  function finishComplete(opts) {
    if (opts && typeof opts.onComplete === 'function') opts.onComplete();
  }
  function finishCancel(opts) {
    if (opts && typeof opts.onCancel === 'function') opts.onCancel();
    else finishComplete(opts);
  }

  // begin starts the install flow into opts.host. opts:
  //   host           element to render the flow into (required)
  //   declaredSource optional exact source (template-declared) for one-click
  //   onComplete     called after install+enable (caller re-checks readiness)
  //   onCancel       called when the user cancels (defaults to onComplete)
  async function begin(opts) {
    const host = opts && opts.host;
    if (!host || busy) return;
    if (opts.declaredSource) {
      await previewSource(host, opts.declaredSource, opts);
      return;
    }
    busy = true;
    message(host, 'Finding reaper-plugin…');
    try {
      const mp = await resolveMarketplace();
      if (mp) {
        const prev = await fetchJSON(
          '/api/plugins/marketplaces/install',
          postJSON({ marketplace: mp.marketplace, plugin: mp.plugin, confirm: false })
        );
        renderTrust(
          host,
          prev.trust,
          mp.plugin + ' · ' + mp.marketplace,
          () =>
            doInstall(
              host,
              () =>
                fetchJSON(
                  '/api/plugins/marketplaces/install',
                  postJSON({ marketplace: mp.marketplace, plugin: mp.plugin, confirm: true })
                ),
              opts
            ),
          opts
        );
      } else {
        renderSourceInput(host, opts);
      }
    } catch (_) {
      renderSourceInput(host, opts);
    } finally {
      busy = false;
    }
  }

  window.ReaperPluginInstall = { begin };
})();
