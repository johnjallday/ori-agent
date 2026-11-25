import { claimWorkspaceArrival, currentHQAttempt } from './hq-guided-attempt.js';

// The real Bootstrap trigger remains the sole opener, for pointer and keyboard.
// Carry the intention on its one show event, not in reusable modal options.
export function showWorkspaceGuide(win, doc) {
  const button = doc.getElementById('cockpitCreateWorkspaceBtn');
  const hint = doc.getElementById('hqWorkspaceHint');
  const modal = doc.getElementById('addFolderModal');
  if (!button || !hint || !modal) return;
  const url = new URL(win.location.href);
  if (url.hash || [...url.searchParams.keys()].some(k => !['hq', 'guided', 'attempt'].includes(k)))
    return;
  const attempt = claimWorkspaceArrival(url.search);
  if (!attempt) return;
  win.history.replaceState(win.history.state, '', url.pathname);
  const controller = new AbortController();
  const options = { signal: controller.signal };
  const close = () => {
    controller.abort();
    hint.hidden = true;
    button.removeAttribute('aria-describedby');
  };
  // This Home visit uses the guide's pulse instead of Atlas's entry ring.
  // Keep the marker after dismissal so the entry ring doesn't start then.
  button.dataset.hqGuidedArrival = 'true';
  hint.hidden = false;
  button.setAttribute('aria-describedby', 'hqWorkspaceInstruction');
  button.scrollIntoView({ block: 'nearest', behavior: 'instant' });
  button.focus({ preventScroll: true });
  hint.querySelector('button').addEventListener(
    'click',
    () => {
      close();
      button.focus();
    },
    options
  );
  doc.addEventListener(
    'keydown',
    e => {
      if (e.key === 'Escape') close();
    },
    options
  );
  win.addEventListener('pagehide', close, options);
  doc.addEventListener(
    'show.bs.modal',
    event => {
      if (
        event.target === modal &&
        event.relatedTarget === button &&
        currentHQAttempt(attempt.hq, 'workspace', attempt.attempt)
      )
        event.hqWorkspaceAttempt = attempt;
      close();
    },
    { ...options, capture: true }
  );
}
