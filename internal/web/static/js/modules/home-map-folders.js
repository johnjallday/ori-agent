import {
  mountFolderAgentChoice,
  mountWorkspaceFolderAgent,
  unmountWorkspaceFolderAgent
} from './folder-agent-card.js';

import { escapeHtml } from './note-editor.js';

const pendingLabel = {
  looking: 'Looking…',
  reported: 'Waiting on Set up',
  setting_up: 'Setting up…',
  failed: 'Review look'
};

// One row per folder, even during the moment a still-running setup places it.
export function homeFolderChips(data, pending, memberViews = []) {
  const incomplete = new Set(
    memberViews
      .filter(v => v.setup?.receipt?.partial_listing || v.setup?.receipt?.unreached > 0)
      .map(v => v.workspace_id)
  );
  const rows = new Map((data?.folders || []).map(row => [row.workspace_id, row]));
  for (const v of pending)
    rows.set(v.workspace_id, {
      workspace_id: v.workspace_id,
      name: v.folder_name,
      status: pendingLabel[v.state]
    });
  const chips = [...rows.values()]
    .sort((a, b) => a.name.localeCompare(b.name) || a.workspace_id.localeCompare(b.workspace_id))
    .map(row => {
      const name = escapeHtml(row.name),
        id = escapeHtml(row.workspace_id);
      if (row.status)
        return `<button type="button" class="ws-cmd-nav-btn" data-cmd-home-feed="${id}">${name} · ${row.status}</button>`;
      const count = `${row.songs} ${row.songs === 1 ? 'song' : 'songs'}`;
      const partial = row.partial || incomplete.has(row.workspace_id);
      let setup = '';
      if (row.root_id && Number.isInteger(row.to_set_up)) {
        const label =
          partial && !row.to_set_up
            ? 'Listing incomplete'
            : `${partial ? 'At least ' : ''}${row.to_set_up} to set up`;
        setup = `<button type="button" class="ws-folder-setup" data-cmd-folder-root="${escapeHtml(row.root_id)}" aria-label="${name}: ${label}">${label}</button>`;
      }
      return `<span class="ws-map-folder-chip"><button type="button" data-cmd-frame-folder="${id}" title="Frame ${name}"><strong>${name}</strong> · ${count}${row.missing ? ' · Location missing' : ''}</button>${setup}</span>`;
    });
  if (!chips.length) return '';
  const more =
    chips.length > 4
      ? `<details class="ws-cmd-disclosure ws-home-folder-more"><summary class="ws-cmd-nav-btn">${chips.length - 4} more ${chips.length === 5 ? 'folder' : 'folders'}</summary><div class="ws-cmd-disclosure-body">${chips.slice(4).join('')}</div></details>`
      : '';
  const other =
    data.outside > 0
      ? `<small>${data.outside} other ${data.outside === 1 ? 'workspace' : 'workspaces'}</small>`
      : '';
  return chips.slice(0, 4).join('') + more + other;
}

// The report can resume even before a refreshed members tree reaches the map.
// Membership itself always comes from the workspace tree, not the report.
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
    void mountFolderAgentChoice(
      host,
      { picker: true, from_workspace_id: homeID, in_home: true },
      options
    ).then(result => {
      if (!active()) return;
      if (result.cancelled) dialog.close();
      else if (result.error) showError(result.error);
    });
  }
  return dialog;
}
