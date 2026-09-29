// Workspace continuity status — the "Ready to move" chip on a workspace and,
// for an imported workspace, the explicit local choice to turn its background
// routines on. Copy is decided by window.WorkspaceContinuity.describeStatus.
(function () {
  'use strict';

  const state = { workspaceId: '', status: null, busy: false, chip: null };

  function el(id) {
    return document.getElementById(id);
  }

  // The chip is moved into the Command view header, which re-renders by
  // replacing its markup: the chip node can be detached from the document at
  // any time. Keep our own reference so it can always be put back.
  function chipElement() {
    return state.chip || el('workspaceContinuityChip');
  }

  function setText(id, text) {
    const node = el(id);
    if (node) node.textContent = text;
  }

  async function request(path, method = 'GET', body) {
    // Every POST goes as JSON: the server refuses non-JSON consent requests,
    // which a page on another site could otherwise send (CSRF).
    const post = method !== 'GET';
    const response = await fetch(path, {
      method,
      headers: post ? { 'Content-Type': 'application/json' } : undefined,
      body: post ? JSON.stringify(body || {}) : undefined
    });
    const result = await response.json().catch(() => ({}));
    return { ok: response.ok && result.success !== false, result };
  }

  function render() {
    const view = window.WorkspaceContinuity?.describeStatus?.(state.status);
    const chip = chipElement();
    if (!view || !chip) return;
    chip.hidden = false;
    chip.dataset.state = view.state;
    chip.textContent =
      view.imported && !view.backgroundAllowed
        ? `Imported · routines off · ${view.label}`
        : view.label;
    chip.setAttribute('aria-label', `Portability: ${view.label}. ${view.detail}`);
    setText('workspaceContinuityDialogLabel', view.label);
    setText('workspaceContinuityDialogDetail', view.detail);
    const imported = el('workspaceContinuityImported');
    if (imported) imported.hidden = !view.imported;
    setText(
      'workspaceContinuityRoutines',
      view.backgroundAllowed
        ? 'Background routines (schedules, missions, triggers, first-open briefs) are on for this workspace here.'
        : 'Background routines (schedules, missions, triggers, first-open briefs) are off here. You can read and use everything by hand.'
    );
    const activate = el('workspaceContinuityActivateBtn');
    if (activate) activate.hidden = !view.canActivate;
    const deactivate = el('workspaceContinuityDeactivateBtn');
    if (deactivate) deactivate.hidden = !view.canDeactivate;
    const confirm = el('workspaceContinuityConfirm');
    if (confirm) confirm.hidden = true;
  }

  async function load() {
    if (!state.workspaceId) return;
    try {
      const { ok, result } = await request(
        `/api/workspaces/${encodeURIComponent(state.workspaceId)}/continuity`
      );
      if (!ok || !result.continuity) return;
      state.status = result.continuity;
      render();
    } catch (error) {
      console.warn('Workspace portability status unavailable:', error);
    }
  }

  async function prepareNow() {
    if (state.busy) return;
    state.busy = true;
    setText('workspaceContinuityDialogDetail', 'Preparing this folder…');
    try {
      const { result } = await request(
        `/api/workspaces/${encodeURIComponent(state.workspaceId)}/continuity/prepare`,
        'POST'
      );
      if (result.continuity) state.status = result.continuity;
      render();
      if (!result.continuity && result.error)
        setText('workspaceContinuityDialogDetail', result.error);
    } finally {
      state.busy = false;
    }
  }

  async function setRoutines(enable) {
    if (state.busy || !state.status) return;
    state.busy = true;
    try {
      const { ok, result } = await request(
        `/api/workspaces/${encodeURIComponent(state.workspaceId)}/continuity/activate`,
        'POST',
        { enable, version: state.status.attachment_version || 0 }
      );
      if (result.continuity) state.status = result.continuity;
      render();
      if (!ok) setText('workspaceContinuityDialogDetail', result.error || 'Nothing was changed.');
      else
        window.Toast?.success?.(
          enable ? 'Background routines turned on here' : 'Background routines turned off'
        );
    } finally {
      state.busy = false;
    }
  }

  function open() {
    const dialog = el('workspaceContinuityDialog');
    if (!dialog) return;
    render();
    void load();
    if (typeof dialog.showModal === 'function') dialog.showModal();
    else dialog.setAttribute('open', '');
  }

  // The Command view header re-renders often; keep the chip in its mount.
  function place() {
    const chip = chipElement();
    const mount = document.querySelector('[data-cmd-continuity-mount]');
    if (chip && mount && chip.parentElement !== mount) mount.appendChild(chip);
  }

  function wire() {
    state.workspaceId = document.body?.dataset?.workspaceId || '';
    state.chip = el('workspaceContinuityChip');
    if (!state.workspaceId || !state.chip) return;
    state.chip.addEventListener('click', open);
    // The dialog is declared inside the Details view; a modal inside a view
    // that is currently hidden (Map, Tickets) would open without rendering.
    const dialog = el('workspaceContinuityDialog');
    if (dialog && dialog.parentElement !== document.body) document.body.appendChild(dialog);
    place();
    if (typeof MutationObserver === 'function') {
      let queued = false;
      new MutationObserver(() => {
        if (queued) return;
        queued = true;
        // setTimeout, not requestAnimationFrame: rAF never fires in a
        // background tab, which would leave the chip out of the header.
        setTimeout(() => {
          queued = false;
          place();
        }, 50);
      }).observe(document.body, { childList: true, subtree: true });
    }
    el('workspaceContinuityPrepareBtn')?.addEventListener('click', () => void prepareNow());
    el('workspaceContinuityActivateBtn')?.addEventListener('click', () => {
      const confirm = el('workspaceContinuityConfirm');
      if (confirm) confirm.hidden = false;
      el('workspaceContinuityConfirmBtn')?.focus?.();
    });
    el('workspaceContinuityConfirmBtn')?.addEventListener('click', () => void setRoutines(true));
    el('workspaceContinuityDeactivateBtn')?.addEventListener(
      'click',
      () => void setRoutines(false)
    );
    el('workspaceContinuityCloseBtn')?.addEventListener('click', () =>
      el('workspaceContinuityDialog')?.close?.()
    );
    // Preparation runs in the background; keep the label current, checking
    // sooner while a save is still being folded into the folder.
    // The first load always runs, even in a background tab.
    const refresh = async (first = false) => {
      if (first || (document.visibilityState !== 'hidden' && !state.busy)) await load();
      const settled = ['ready', 'unavailable'].includes(state.status?.state || '');
      setTimeout(refresh, settled ? 30000 : 6000);
    };
    void refresh(true);
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', wire, { once: true });
  } else {
    wire();
  }
})();
