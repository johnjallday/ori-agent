// Presentation only: a source may borrow the creator's chrome, never its build
// session. This module has no requests, plan, consent or persisted draft.
// The source owns currentness, material, and every action inside `host`.
export function createSourceSetupDialog({
  doc = globalThis.document,
  win = globalThis.window,
  modal = doc?.getElementById('addFolderModal'),
  host = doc?.getElementById('sourceSetupDialogHost'),
  bootstrap = globalThis.bootstrap,
  occupied = () =>
    Boolean(win?.sessionManager?.workspaceCreatorContext || win?.sessionManager?.workspaceBuild)
} = {}) {
  let active = null;
  let phase = 'closed';
  let dismissal = null;
  let savedTitle = '';
  let savedCloseLabel = '';
  let resolveHidden = null;
  const title = doc?.getElementById('folderModalTitle');
  const close = modal?.querySelector('.modal-header [data-bs-dismiss="modal"]');
  const instance = () => bootstrap?.Modal?.getOrCreateInstance(modal);
  const current = ref => Boolean(ref && ref.current?.());
  const otherDialog = () =>
    Array.from(doc?.querySelectorAll('dialog[open], .modal.show') || []).some(
      node => node !== modal
    );

  function finishHidden() {
    const ref = active;
    const mode = dismissal || 'close';
    active = null;
    phase = 'closed';
    dismissal = null;
    delete modal.dataset.sourceSetupOwner;
    host.hidden = true;
    if (title) title.textContent = savedTitle;
    if (close) close.setAttribute('aria-label', savedCloseLabel);
    const done = resolveHidden;
    resolveHidden = null;
    done?.(true);
    if (mode === 'close') {
      ref?.onClose?.();
      if (current(ref) && ref.invoker?.isConnected && !otherDialog())
        ref.invoker.focus({ preventScroll: true });
    }
  }

  // Capture before the ordinary creator's catalog/reset/abandon/announcement
  // listeners, including hidden: ownership lasts until Bootstrap is finished.
  for (const name of ['show', 'shown', 'hide', 'hidden']) {
    modal?.addEventListener(
      `${name}.bs.modal`,
      event => {
        if (!active) return;
        event.stopImmediatePropagation();
        if (name === 'show' && !current(active)) {
          event.preventDefault();
          finishHidden();
        } else if (name === 'shown') {
          phase = 'open';
          if (dismissal || !current(active)) {
            dismissal ||= 'invalidate';
            instance()?.hide();
          } else {
            (host.querySelector('[data-source-dialog-title]') || title)?.focus({
              preventScroll: true
            });
          }
        } else if (name === 'hide') phase = 'closing';
        else if (name === 'hidden') finishHidden();
      },
      true
    );
  }

  function hide(mode = 'close') {
    if (!active) return Promise.resolve(true);
    if (resolveHidden) return Promise.resolve(false);
    dismissal = mode;
    const hidden = new Promise(resolve => {
      resolveHidden = resolve;
    });
    if (phase !== 'opening' && phase !== 'closing') instance()?.hide();
    return hidden;
  }

  modal?.addEventListener(
    'click',
    event => {
      if (!active || !event.target.closest?.('[data-bs-dismiss="modal"]')) return;
      event.preventDefault();
      event.stopImmediatePropagation();
      void hide();
    },
    true
  );
  win?.addEventListener(
    'keydown',
    event => {
      if (!active || event.key !== 'Escape') return;
      event.preventDefault();
      event.stopImmediatePropagation();
      void hide();
    },
    true
  );

  return {
    host,
    owns: ref => active === ref,
    visible: () => Boolean(active),
    // ref = { kind: 'music-folder', offerID, generation, current, invoker,
    //         title, onClose }. It is an identity reference, not a setup plan.
    open(ref) {
      if (
        !modal ||
        !host ||
        !bootstrap?.Modal ||
        !current(ref) ||
        ref.kind !== 'music-folder' ||
        !ref.offerID
      )
        return false;
      if (active) return active === ref && !dismissal;
      if (
        occupied() ||
        otherDialog() ||
        modal.classList.contains('show') ||
        instance()?._isTransitioning
      )
        return false;
      active = ref;
      phase = 'opening';
      savedTitle = title?.textContent || '';
      savedCloseLabel = close?.getAttribute('aria-label') || 'Close create workspace';
      modal.dataset.sourceSetupOwner = 'music-folder';
      host.hidden = false;
      if (title) title.textContent = ref.title || 'Review music folder setup';
      if (close) close.setAttribute('aria-label', 'Close setup review');
      instance().show();
      return true;
    },
    close: () => hide(),
    suspend: () => hide('suspend'),
    invalidate: () => hide('invalidate')
  };
}
