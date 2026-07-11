// reaper-setup-card.js — the Create Workspace "REAPER Setup" card.
//
// Shown only when the Reaper Song template is selected. It reads the pre-create
// preview from /api/reaper-setup/preview (the same backend the live readiness
// resolver uses) and explains, with visible non-color text, whether the
// installed reaper-plugin will attach, whether it must be enabled, or whether
// the workspace will start as a usable file-only project. Workspace creation is
// never blocked. Verification of REAPER/Web Remote/runner happens later, when
// setup runs — the card never claims REAPER is connected.
(function () {
  'use strict';

  const REAPER_SONG_TEMPLATE_ID = 'reaper-song';
  const PLUGIN_NAME = 'reaper-plugin';

  const els = () => ({
    card: document.getElementById('reaperSetupCard'),
    status: document.getElementById('reaperSetupStatusText'),
    badge: document.getElementById('reaperSetupBadge'),
    detail: document.getElementById('reaperSetupDetail'),
    actions: document.getElementById('reaperSetupActions'),
  });

  let busy = false;

  function isReaperTemplate(template) {
    if (!template) return false;
    const id = String(template.id || template.ID || '').toLowerCase();
    return id === REAPER_SONG_TEMPLATE_ID;
  }

  function hide() {
    const { card } = els();
    if (card) card.hidden = true;
  }

  function setBadge(badge, label) {
    if (!badge) return;
    badge.textContent = label;
    badge.className = 'reaper-setup-badge reaper-setup-badge-' + label.toLowerCase().replace(/[^a-z]+/g, '-');
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
    actions.querySelectorAll('button').forEach((b) => { b.disabled = on; });
  }

  async function enablePlugin() {
    if (busy) return;
    setBusy(true);
    try {
      const resp = await fetch('/api/plugins/' + encodeURIComponent(PLUGIN_NAME) + '/enable', { method: 'POST' });
      if (!resp.ok) throw new Error('enable failed');
      await refresh();
    } catch (_) {
      renderError('Could not enable reaper-plugin. You can still create a file-only workspace.');
    } finally {
      setBusy(false);
    }
  }

  function openPluginsPage() {
    // Fallback per PRD FR18: focus the existing Plugins installation surface with
    // the exact plugin name when no marketplace resolves it inline.
    window.open('/plugins?install=' + encodeURIComponent(PLUGIN_NAME), '_blank', 'noopener');
  }

  function renderError(message) {
    const { card, status, detail, actions, badge } = els();
    if (!card) return;
    card.hidden = false;
    setBadge(badge, 'Error');
    if (status) status.textContent = message || 'Could not check reaper-plugin.';
    if (detail) detail.textContent = 'File-only creation is still available.';
    if (actions) {
      actions.textContent = '';
      actions.appendChild(button('Retry', { onClick: refresh }));
    }
  }

  const verificationNote =
    'You can create the workspace now. REAPER, Web Remote, and runner readiness are checked later, when setup runs.';

  function render(preview) {
    const { card, status, detail, actions, badge } = els();
    if (!card) return;
    card.hidden = false;
    if (actions) actions.textContent = '';

    const components = (preview.would_attach || []).map((c) => c.name).join(', ');

    switch (preview.status) {
      case 'ready_to_attach':
        setBadge(badge, 'Ready');
        status.textContent = 'reaper-plugin is installed. Its components will attach when you create this workspace.';
        detail.textContent = (components ? 'Will attach: ' + components + '. ' : '') + verificationNote;
        break;
      case 'plugin_disabled':
        setBadge(badge, 'Disabled');
        status.textContent = 'reaper-plugin is installed but globally disabled.';
        detail.textContent = 'Enable it to attach its components, or create a file-only project now. ' + verificationNote;
        actions.appendChild(button('Enable plugin', { primary: true, onClick: enablePlugin }));
        break;
      case 'plugin_missing':
      default:
        setBadge(badge, 'File-only');
        status.textContent = 'File-only project: reaper-plugin is not installed.';
        detail.textContent =
          'Creation still succeeds and your .rpp is intact. Install reaper-plugin (a global install; attachment is per-workspace) to enable live control later. ' +
          verificationNote;
        actions.appendChild(button('Install plugin', { primary: true, onClick: openPluginsPage }));
        break;
    }
    // Live control also needs a Codex or Claude Code setup agent + explicit native
    // CLI access, configured inside the workspace after creation (FR20/FR21).
    const note = document.createElement('div');
    note.style.cssText = 'font-size:11px;color:var(--text-secondary);margin-top:6px;opacity:0.85;';
    note.textContent =
      'Live local REAPER control also needs a Codex or Claude Code agent and native CLI access, which you enable inside the workspace after creation.';
    detail.appendChild(note);
  }

  async function refresh() {
    const { card, status, badge } = els();
    if (!card) return;
    card.hidden = false;
    setBadge(badge, 'Checking');
    if (status) status.textContent = 'Checking reaper-plugin…';
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
      hide();
      return;
    }
    void refresh();
  }

  window.ReaperSetupCard = { showForTemplate, hide, refresh, isReaperTemplate };
})();
