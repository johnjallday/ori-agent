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
// disabled, this card disables the Create Workspace button and offers inline
// Install/Enable actions; the backend enforces the same rule with a 409.
// Verification of REAPER/Web Remote/runner still happens later, when setup runs —
// the card never claims REAPER is connected.
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

  function isReaperTemplate(template) {
    if (!template) return false;
    const id = String(template.id || template.ID || '').toLowerCase();
    return id === REAPER_SONG_TEMPLATE_ID;
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
    actions.querySelectorAll('button').forEach(b => {
      b.disabled = on;
    });
  }

  async function enablePlugin() {
    if (busy) return;
    setBusy(true);
    try {
      const resp = await fetch('/api/plugins/' + encodeURIComponent(PLUGIN_NAME) + '/enable', {
        method: 'POST'
      });
      if (!resp.ok) throw new Error('enable failed');
      await refresh();
    } catch (_) {
      renderError('Could not enable reaper-plugin. Enable it, then try again.');
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
    if (detail) detail.textContent = 'Try again; the backend re-checks the plugin when you create.';
    if (actions) {
      actions.textContent = '';
      actions.appendChild(button('Retry', { onClick: refresh }));
    }
    // Don't hard-block on a transient preview error — the backend guard still
    // enforces the requirement on create.
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
          'reaper-plugin is required and not installed. Install it to create this workspace.';
        detail.textContent =
          'The Reaper Song template requires reaper-plugin (a global install; attachment is per-workspace). Creating is blocked until it is installed and enabled. ' +
          verificationNote;
        actions.appendChild(button('Install plugin', { primary: true, onClick: openPluginsPage }));
        actions.appendChild(button('Re-check', { onClick: refresh }));
        setCreateBlocked(true, 'Install reaper-plugin to create this workspace');
        break;
    }
    const note = document.createElement('div');
    note.style.cssText = 'font-size:11px;color:var(--text-secondary);margin-top:6px;opacity:0.85;';
    note.textContent = liveControlNote;
    detail.appendChild(note);
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
      hide();
      return;
    }
    void refresh();
  }

  window.ReaperSetupCard = { showForTemplate, hide, refresh, isReaperTemplate };
})();
