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
  // This Home visit uses the guide's pulse instead of Atlas's entry ring.
  button.dataset.hqGuidedArrival = 'true';
  focusWorkspaceGuide(win, doc, button, hint, modal, 'hqWorkspaceInstruction', event => {
    if (currentHQAttempt(attempt.hq, 'workspace', attempt.attempt))
      event.hqWorkspaceAttempt = attempt;
  });
}

// Keep the handoff in this page only. An unrelated modal, Escape, dismissal,
// returning to chat or leaving Home retires it. Bootstrap remains the opener.
export function showChatWorkspaceGuide(win, doc, label, resume) {
  const button = doc.getElementById('cockpitCreateWorkspaceBtn');
  const hint = doc.getElementById('chatWorkspaceHint');
  const modal = doc.getElementById('addFolderModal');
  if (!button || !hint || !modal) return false;
  win.PersonalAssistantPanel.close({ restoreFocus: false });
  doc.getElementById('chatWorkspaceInstruction').textContent = `Start here to use ${label}.`;
  focusWorkspaceGuide(win, doc, button, hint, modal, 'chatWorkspaceInstruction', event => {
    event.chatFolderResume = resume;
  });
  return true;
}

function focusWorkspaceGuide(win, doc, button, hint, modal, description, onActivate) {
  doc.dispatchEvent(new Event('ori:workspace-guide-starting'));
  const controller = new AbortController();
  const options = { signal: controller.signal };
  const close = () => {
    controller.abort();
    hint.hidden = true;
    button.removeAttribute('aria-describedby');
  };
  doc.addEventListener('ori:workspace-guide-starting', close, options);
  if (description === 'chatWorkspaceInstruction') {
    doc.addEventListener('personal-assistant:opened', close, options);
    doc.addEventListener('ori:home-view-changed', close, options);
    win.addEventListener('popstate', close, options);
  }
  hint.hidden = false;
  button.setAttribute('aria-describedby', description);
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
      if (e.key === 'Escape') {
        if (hint.contains(doc.activeElement)) button.focus();
        close();
      }
    },
    options
  );
  win.addEventListener('pagehide', close, options);
  doc.addEventListener(
    'show.bs.modal',
    event => {
      if (event.target === modal && event.relatedTarget === button) onActivate(event);
      close();
    },
    { ...options, capture: true }
  );
}
