import {
  mountFolderAgentChoice,
  mountWorkspaceFolderAgent,
  unmountWorkspaceFolderAgent
} from './folder-agent-card.js';

// Origin is a server-verified navigation hint. A pending hire is not a Home
// member, and its origin does not promise where the setup will place it.
export function pendingHomeFeeds(views, homeID) {
  return views.filter(
    v =>
      homeID &&
      v.origin_home_id === homeID &&
      ['looking', 'reported', 'setting_up', 'failed'].includes(v.state)
  );
}

// The map replaces its toolbar DOM. Keep the actual card, including draft hire
// fields and its single poller, outside that subtree until explicitly closed.
export function openHomeFeed({
  homeID,
  runID,
  current,
  onStarted,
  onClose,
  restoreFocus,
  navigate,
  doc = document
}) {
  const dialog = doc.createElement('dialog');
  dialog.className = 'ws-home-feed';
  dialog.setAttribute('aria-label', runID ? 'Folder setup' : 'Add folder');
  const close = doc.createElement('button');
  close.type = 'button';
  close.textContent = 'Close';
  close.className = 'ws-cmd-nav-btn';
  const host = doc.createElement('div');
  host.textContent = runID ? 'Loading folder setup…' : 'Choose a folder in the system dialog…';
  dialog.append(close, host);
  doc.body.append(dialog);
  const active = () => dialog.open && current();
  dialog.addEventListener(
    'close',
    () => {
      unmountWorkspaceFolderAgent(host);
      dialog.remove();
      onClose();
      if (current()) restoreFocus();
    },
    { once: true }
  );
  close.addEventListener('click', () => dialog.close());
  dialog.showModal();
  const options = {
    doc,
    isActive: active,
    navigate: route => {
      if (active()) navigate(route);
    },
    onStarted
  };
  const showError = message => {
    if (active()) {
      host.hidden = false;
      host.textContent = message;
    }
  };
  if (runID) {
    void mountWorkspaceFolderAgent(host, runID, options).then(view => {
      if (!view) showError('Folder setup is unavailable.');
    });
  } else {
    void mountFolderAgentChoice(host, { picker: true, from_workspace_id: homeID }, options).then(
      result => {
        if (!active()) return;
        if (result.cancelled) dialog.close();
        else if (result.error) showError(result.error);
      }
    );
  }
  return dialog;
}
