// reaper-setup-card.js — the Create Workspace "REAPER Setup" card.
//
// Shown only when the Reaper Song template is selected. It reads the pre-create
// preview from /api/reaper-setup/preview (the same backend the live readiness
// resolver uses) and explains, with visible non-color text, whether the
// installed reaper-plugin will attach or must be installed/enabled first.
//
// Required-plugin gate (product decision): the Reaper Song template requires
// reaper-plugin installed AND enabled before the workspace can be created, so it
// never starts missing its required tools. While the plugin is missing or
// disabled, this card disables the Create Workspace button.
//
// Installing is done inline, without leaving the modal: the card resolves
// reaper-plugin from the configured marketplaces (or takes a source you paste),
// shows the trust disclosure, installs on confirmation, enables it, and
// re-checks — so a missing plugin becomes ready in a couple of clicks. The trust
// preview is always shown; nothing is installed silently. Verification of
// REAPER/Web Remote/runner still happens later, when setup runs.
(function () {
  'use strict';

  const REAPER_SONG_TEMPLATE_ID = 'reaper-song';
  const PLUGIN_NAME = 'reaper-plugin';

  const els = () => ({
    card: document.getElementById('reaperSetupCard'),
    status: document.getElementById('reaperSetupStatusText'),
    badge: document.getElementById('reaperSetupBadge'),
    detail: document.getElementById('reaperSetupDetail'),
    actions: document.getElementById('reaperSetupActions')
  });

  let busy = false;
  // The exact install source the selected template declares for reaper-plugin
  // (tools.plugin_sources). When set, Install is one click (trust-previewed) with
  // no marketplace lookup or pasted source needed.
  let declaredSource = '';

  function isReaperTemplate(template) {
    if (!template) return false;
    const id = String(template.id || template.ID || '').toLowerCase();
    return id === REAPER_SONG_TEMPLATE_ID;
  }

  // declaredPluginSource reads the template's declared source for reaper-plugin,
  // matching the plugin name case-insensitively.
  function declaredPluginSource(template) {
    const sources = template && template.tools && template.tools.plugin_sources;
    if (!sources || typeof sources !== 'object') return '';
    for (const key of Object.keys(sources)) {
      if (String(key).toLowerCase() === PLUGIN_NAME) return String(sources[key] || '').trim();
    }
    return '';
  }

  // setCreateBlocked disables/enables the shared Create Workspace button so a
  // Reaper Song workspace cannot be created until reaper-plugin will attach.
  function setCreateBlocked(blocked, reason) {
    const btn = document.getElementById('createFolderBtn');
    if (!btn) return;
    btn.disabled = !!blocked;
    btn.setAttribute('aria-disabled', blocked ? 'true' : 'false');
    if (blocked) {
      btn.dataset.reaperBlocked = '1';
      if (reason) btn.title = reason;
    } else if (btn.dataset.reaperBlocked) {
      delete btn.dataset.reaperBlocked;
      btn.removeAttribute('title');
    }
  }

  function hide() {
    const { card } = els();
    if (card) card.hidden = true;
    // A non-REAPER template must never leave creation blocked.
    setCreateBlocked(false);
  }

  function setBadge(badge, label) {
    if (!badge) return;
    badge.textContent = label;
    badge.className =
      'reaper-setup-badge reaper-setup-badge-' + label.toLowerCase().replace(/[^a-z]+/g, '-');
  }

  function button(label, opts = {}) {
    const b = document.createElement('button');
    b.type = 'button';
    b.className = 'modern-btn ' + (opts.primary ? 'modern-btn-primary' : 'modern-btn-secondary');
    b.style.fontSize = '12px';
    b.textContent = label;
    if (opts.onClick) b.addEventListener('click', opts.onClick);
    if (opts.disabled) b.disabled = true;
    return b;
  }

  function setBusy(on) {
    busy = on;
    const { actions } = els();
    if (!actions) return;
    actions.querySelectorAll('button, input').forEach(el => {
      el.disabled = on;
    });
  }

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

  // --- Inline install flow -------------------------------------------------

  // resolveMarketplacePlugin scans the configured marketplaces for reaper-plugin
  // so it can be installed with the trust preview and no hard-coded source.
  async function resolveMarketplacePlugin() {
    try {
      const data = await fetchJSON('/api/plugins/marketplaces');
      const list = Array.isArray(data.marketplaces) ? data.marketplaces : [];
      for (const mp of list) {
        const plugins = Array.isArray(mp.plugins) ? mp.plugins : [];
        const hit = plugins.find(p => String(p.name || '').toLowerCase() === PLUGIN_NAME);
        if (hit) return { marketplace: mp.name, plugin: hit.name || PLUGIN_NAME };
      }
    } catch (_) {
      /* fall through to the source-paste path */
    }
    return null;
  }

  async function beginInstall() {
    if (busy) return;
    // Preferred path: the template declares the exact source, so install is one
    // click (still trust-previewed) with no marketplace lookup or paste.
    if (declaredSource) {
      await previewSourceInstall(declaredSource);
      return;
    }
    setBusy(true);
    renderMessage('Finding reaper-plugin…');
    try {
      const mp = await resolveMarketplacePlugin();
      if (mp) {
        const prev = await fetchJSON('/api/plugins/marketplaces/install', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ marketplace: mp.marketplace, plugin: mp.plugin, confirm: false })
        });
        renderTrust(prev.trust, mp.plugin + ' · ' + mp.marketplace, () =>
          confirmMarketplaceInstall(mp)
        );
      } else {
        renderSourceInput();
      }
    } catch (e) {
      renderError(
        'Could not reach the plugin marketplaces. Paste a source instead, or open the Plugins page.'
      );
      renderSourceInput(true);
    } finally {
      setBusy(false);
    }
  }

  async function confirmMarketplaceInstall(mp) {
    if (busy) return;
    setBusy(true);
    renderMessage('Installing ' + PLUGIN_NAME + '…');
    try {
      await fetchJSON('/api/plugins/marketplaces/install', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ marketplace: mp.marketplace, plugin: mp.plugin, confirm: true })
      });
      await enableAndRefresh();
    } catch (e) {
      renderError('Install failed: ' + (e.message || 'unknown error'));
    } finally {
      setBusy(false);
    }
  }

  async function previewSourceInstall(source) {
    source = String(source || '').trim();
    if (!source) return;
    if (busy) return;
    setBusy(true);
    renderMessage('Checking ' + source + '…');
    try {
      const prev = await fetchJSON('/api/plugins/install', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ source, confirm: false })
      });
      renderTrust(prev.trust, source, () => confirmSourceInstall(source));
    } catch (e) {
      renderError('Could not read that source: ' + (e.message || 'unknown error'));
      renderSourceInput(true);
    } finally {
      setBusy(false);
    }
  }

  async function confirmSourceInstall(source) {
    if (busy) return;
    setBusy(true);
    renderMessage('Installing ' + PLUGIN_NAME + '…');
    try {
      await fetchJSON('/api/plugins/install', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ source, confirm: true })
      });
      await enableAndRefresh();
    } catch (e) {
      renderError('Install failed: ' + (e.message || 'unknown error'));
    } finally {
      setBusy(false);
    }
  }

  // enableAndRefresh enables the freshly installed plugin (install records it
  // disabled) and re-checks readiness, which unblocks creation when ready.
  async function enableAndRefresh() {
    try {
      await fetch('/api/plugins/' + encodeURIComponent(PLUGIN_NAME) + '/enable', {
        method: 'POST'
      });
    } catch (_) {
      /* best effort; refresh will show the disabled state and offer Enable */
    }
    await refresh();
  }

  // --- Inline panels -------------------------------------------------------

  function renderMessage(message) {
    const { status } = els();
    if (status) status.textContent = message;
    const { actions } = els();
    if (actions) actions.textContent = '';
  }

  // renderTrust shows the install disclosure (what will be registered) so nothing
  // installs without the user seeing it.
  function renderTrust(trust, sourceLabel, onConfirm) {
    const { detail, actions } = els();
    if (!detail || !actions) return;
    detail.textContent = '';
    const t = trust || {};
    const head = document.createElement('div');
    head.style.cssText = 'font-size:12px;color:var(--text-primary);margin-bottom:4px;';
    head.textContent = 'Install ' + (t.Name || PLUGIN_NAME) + ' from ' + sourceLabel + '?';
    detail.appendChild(head);

    const disclosure = document.createElement('div');
    disclosure.style.cssText = 'font-size:11px;color:var(--text-secondary);';
    const skills = Array.isArray(t.Skills) ? t.Skills : [];
    const cmds = Array.isArray(t.MCPCommands) ? t.MCPCommands : [];
    const warnings = Array.isArray(t.Warnings) ? t.Warnings : [];
    if (skills.length) disclosure.appendChild(line('Skills: ' + skills.join(', ')));
    if (cmds.length) disclosure.appendChild(line('Runs: ' + cmds.join('; ')));
    if (!skills.length && !cmds.length)
      disclosure.appendChild(line('Registers reaper-plugin components.'));
    warnings.forEach(wtext => {
      const wl = line('⚠ ' + wtext);
      wl.style.color = 'var(--warning-color, #d97706)';
      disclosure.appendChild(wl);
    });
    detail.appendChild(disclosure);

    actions.textContent = '';
    actions.appendChild(button('Install & enable', { primary: true, onClick: onConfirm }));
    actions.appendChild(button('Cancel', { onClick: refresh }));
  }

  function line(text) {
    const d = document.createElement('div');
    d.textContent = text;
    return d;
  }

  // renderSourceInput lets the user paste a source when no marketplace resolves
  // reaper-plugin, so install still happens inline (with the trust preview).
  function renderSourceInput(keepMessage) {
    const { status, detail, actions } = els();
    if (!detail || !actions) return;
    if (status && !keepMessage) {
      status.textContent =
        'No configured marketplace has reaper-plugin. Paste its source to install it here.';
    }
    detail.textContent = '';
    const hint = line(
      'A GitHub repo, owner/repo, or local path to reaper-plugin. The trust preview is shown before anything installs.'
    );
    hint.style.cssText = 'font-size:11px;color:var(--text-secondary);';
    detail.appendChild(hint);

    actions.textContent = '';
    const input = document.createElement('input');
    input.type = 'text';
    input.className = 'modern-input';
    input.placeholder = 'e.g. johnjallday/reaper-plugin';
    input.style.cssText = 'flex:1 1 220px;font-size:12px;';
    input.setAttribute('aria-label', 'reaper-plugin source');
    input.addEventListener('keydown', e => {
      if (e.key === 'Enter') previewSourceInstall(input.value);
    });
    actions.appendChild(input);
    actions.appendChild(
      button('Preview install', { primary: true, onClick: () => previewSourceInstall(input.value) })
    );
    actions.appendChild(button('Open Plugins page', { onClick: openPluginsPage }));
    input.focus?.();
  }

  function openPluginsPage() {
    window.open('/plugins?install=' + encodeURIComponent(PLUGIN_NAME), '_blank', 'noopener');
  }

  function renderError(message) {
    const { card, status, badge, detail, actions } = els();
    if (!card) return;
    card.hidden = false;
    setBadge(badge, 'Error');
    if (status) status.textContent = message || 'Could not check reaper-plugin.';
    if (detail) detail.textContent = '';
    if (actions) {
      actions.textContent = '';
      actions.appendChild(button('Retry', { onClick: refresh }));
    }
    // Don't hard-block on a transient error — the backend guard still enforces
    // the requirement on create.
    setCreateBlocked(false);
  }

  const verificationNote =
    'REAPER, Web Remote, and runner readiness are checked later, when setup runs.';
  const liveControlNote =
    'Live local REAPER control also needs a Codex or Claude Code agent and native CLI access, which you enable inside the workspace after creation.';

  function render(preview) {
    const { card, status, detail, actions, badge } = els();
    if (!card) return;
    card.hidden = false;
    if (actions) actions.textContent = '';

    const components = (preview.would_attach || []).map(c => c.name).join(', ');

    switch (preview.status) {
      case 'ready_to_attach':
        setBadge(badge, 'Ready');
        status.textContent =
          'reaper-plugin is installed. Its components will attach when you create this workspace.';
        detail.textContent =
          (components ? 'Will attach: ' + components + '. ' : '') + verificationNote;
        setCreateBlocked(false);
        break;
      case 'plugin_disabled':
        setBadge(badge, 'Required');
        status.textContent =
          'reaper-plugin is installed but globally disabled. Enable it to create this workspace.';
        detail.textContent =
          'The Reaper Song template requires reaper-plugin. Creating is blocked until it is enabled. ' +
          verificationNote;
        actions.appendChild(button('Enable plugin', { primary: true, onClick: enablePlugin }));
        setCreateBlocked(true, 'Enable reaper-plugin to create this workspace');
        break;
      case 'plugin_missing':
      default:
        setBadge(badge, 'Required');
        status.textContent =
          'reaper-plugin is required and not installed. Install it below to create this workspace.';
        detail.textContent =
          'The Reaper Song template requires reaper-plugin (a global install; attachment is per-workspace). ' +
          verificationNote;
        actions.appendChild(button('Install plugin', { primary: true, onClick: beginInstall }));
        actions.appendChild(button('Re-check', { onClick: refresh }));
        setCreateBlocked(true, 'Install reaper-plugin to create this workspace');
        break;
    }
    if (detail) {
      const note = document.createElement('div');
      note.style.cssText =
        'font-size:11px;color:var(--text-secondary);margin-top:6px;opacity:0.85;';
      note.textContent = liveControlNote;
      detail.appendChild(note);
    }
  }

  async function enablePlugin() {
    if (busy) return;
    setBusy(true);
    renderMessage('Enabling reaper-plugin…');
    try {
      const resp = await fetch('/api/plugins/' + encodeURIComponent(PLUGIN_NAME) + '/enable', {
        method: 'POST'
      });
      if (!resp.ok) throw new Error('enable failed');
      await refresh();
    } catch (_) {
      renderError('Could not enable reaper-plugin. Enable it, then Re-check.');
    } finally {
      setBusy(false);
    }
  }

  async function refresh() {
    const { card, status, badge } = els();
    if (!card) return;
    card.hidden = false;
    setBadge(badge, 'Checking');
    if (status) status.textContent = 'Checking reaper-plugin…';
    // Block creation while we resolve state, to avoid a create during the gap.
    setCreateBlocked(true, 'Checking reaper-plugin…');
    try {
      const resp = await fetch('/api/reaper-setup/preview');
      if (!resp.ok) throw new Error('preview failed');
      const preview = await resp.json();
      render(preview);
    } catch (_) {
      renderError();
    }
  }

  function showForTemplate(template) {
    if (!isReaperTemplate(template)) {
      declaredSource = '';
      hide();
      return;
    }
    declaredSource = declaredPluginSource(template);
    void refresh();
  }

  window.ReaperSetupCard = { showForTemplate, hide, refresh, isReaperTemplate };
})();
