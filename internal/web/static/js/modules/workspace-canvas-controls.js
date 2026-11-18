// Native disclosure: ordinary Tab/Enter buttons, not an ARIA application menu.
export function bindCanvasAdd(doc = document) {
  const disclosure = doc.getElementById('canvas-add-disclosure');
  const trigger = disclosure?.querySelector('summary');
  if (!trigger || disclosure.dataset.canvasAddBound === '1') return;
  disclosure.dataset.canvasAddBound = '1';
  let pendingDialog = null;
  const close = restore => {
    disclosure.open = false;
    if (restore) trigger.focus();
  };
  disclosure.addEventListener('keydown', event => {
    if (event.key !== 'Escape' || !disclosure.open) return;
    event.preventDefault();
    event.stopPropagation();
    close(true);
  });
  doc.addEventListener('pointerdown', event => {
    if (disclosure.open && !disclosure.contains(event.target)) close(false);
  });
  // Let the existing inline action receive its click before hiding its entry.
  // Do not move focus away from the dialog that action is opening.
  disclosure.addEventListener('click', event => {
    const choice = event.target.closest?.('[data-canvas-add-dialog]');
    if (!choice || !disclosure.contains(choice)) return;
    pendingDialog = choice.dataset.canvasAddDialog;
    close(false);
  });
  const afterDialog = event => {
    if (!pendingDialog || !event.target.matches?.(pendingDialog)) return;
    pendingDialog = null;
    if (disclosure.isConnected && !doc.querySelector('.modal.show, dialog[open]')) trigger.focus();
  };
  doc.addEventListener('hidden.bs.modal', afterDialog);
  doc.addEventListener('close', afterDialog, true);
}
if (typeof document !== 'undefined') {
  if (document.readyState === 'loading')
    document.addEventListener('DOMContentLoaded', () => bindCanvasAdd(), { once: true });
  else bindCanvasAdd();
}
