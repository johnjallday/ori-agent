import { bindProjectDiscovery } from './documents-project-discovery.js';

// A review of the saved Files anchor, never a path or a filename-search grant.
export async function folderStaffingDraft(id) {
  try {
    const r = await fetch(`/api/workspaces/${encodeURIComponent(id)}/folder-manager-draft`, {
      cache: 'no-store'
    });
    return r.status === 200 ? await r.json() : null;
  } catch {
    return null;
  }
}

// Dismissal is a local presentation preference, never folder authority. A
// saved look always takes precedence, even when a different tab started it.
export function renderDocumentsLook(host, page, draft, current) {
  const id = draft.workspace_id;
  const key = `ori.documentsLook.dismissed.${encodeURIComponent(draft.owner)}.${encodeURIComponent(id)}`;
  const el = name => host.querySelector(`[data-documents-${name}]`);
  const action = el('action'),
    dismiss = el('dismiss'),
    status = el('status');
  const heading = host.querySelector('h2');
  const face = () => {
    if (current())
      el('face').innerHTML = globalThis.WorkspaceRoleRoster?.avatarMarkup?.(draft.manager) || '';
  };
  face();
  if (draft.manager.appearance?.mode === 'character')
    globalThis.CharacterCatalog?.load?.()
      ?.then(face)
      .catch(() => {});
  el('manager').textContent = draft.manager.name;
  status.textContent = '';
  let collapsed = false;
  try {
    collapsed = localStorage.getItem(key) === '1';
  } catch {
    /* storage unavailable */
  }
  const saved = draft.look_state;
  const verify = draft.verify_folder;
  bindProjectDiscovery(el('projects'), el('project-panel'), id, Boolean(draft.project_discovery));
  const build = el('build');
  if (build) {
    build.hidden = !draft.station_offer;
    build.onclick = () => {
      if (current() && draft.station_offer) window.FileJanitorConsole?.openStationBuild(id, build);
    };
  }
  const paint = () => {
    const quiet = collapsed && !saved;
    el('look-copy').hidden = quiet;
    dismiss.hidden = Boolean(saved || quiet);
    action.disabled = false;
    heading.textContent = saved
      ? 'Your Documents first look'
      : verify
        ? 'Verify the Documents folder'
        : 'Want help organizing Documents?';
    el('description').textContent = saved
      ? saved === 'looking'
        ? 'Your manager is checking names and folder structure.'
        : saved === 'failed'
          ? 'The first look did not finish. Review the saved ticket before trying again.'
          : 'Your manager has a saved first look. No new look is needed.'
      : verify
        ? 'This workspace predates folder verification. Confirm that its linked folder is your current Documents folder. This saves its identity; it does not list files, enable search or start a look.'
        : 'I can check names and folder structure. If a model is configured, it may receive those names and metadata, not document contents. Nothing moves.';
    action.textContent = saved
      ? saved === 'looking'
        ? 'View progress'
        : 'View report'
      : quiet
        ? 'Show Documents first look'
        : verify
          ? 'Verify Documents folder'
          : 'Look around Documents';
  };
  paint();
  const collapse = value => {
    collapsed = value;
    try {
      if (value) localStorage.setItem(key, '1');
      else localStorage.removeItem(key);
    } catch {
      /* keep this visit usable */
    }
    paint();
    status.textContent = value ? 'First look dismissed. Nothing was started.' : '';
    action.focus();
  };
  dismiss.onclick = () => {
    if (current()) collapse(true);
  };
  action.onclick = async () => {
    if (!current() || action.disabled) return;
    if (saved) {
      if (
        !globalThis.workspaceCommand?.openFolderAgent?.(action) &&
        !page.commanderPresenter?.review?.()
      )
        status.textContent = 'The saved look is loading. Try again in a moment.';
      return;
    }
    if (collapsed) {
      collapse(false);
      return;
    }
    action.disabled = true;
    dismiss.disabled = true;
    status.textContent = verify ? 'Verifying the linked folder…' : 'Opening the first-look ticket…';
    try {
      const r = await fetch(
        `/api/workspaces/${encodeURIComponent(id)}/${verify ? 'verify-documents-folder' : 'folder-manager-look'}`,
        {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(
            verify
              ? { verification: verify }
              : { link: draft.look_link, agent: draft.manager.name, brief: draft.brief }
          )
        }
      );
      if (!r.ok) throw Error();
      if (current()) location.reload();
    } catch {
      // A lost POST reply is not permission to retry the write. Read status.
      const observed = await folderStaffingDraft(id);
      if (!current()) return;
      if (
        observed?.workspace_id === id &&
        (observed.look_state || (verify && observed.look_link))
      ) {
        location.reload();
        return;
      }
      status.textContent =
        'The folder or ticket could not be verified. Reload to check its current status.';
      action.textContent = 'Reload status';
      dismiss.hidden = true;
      action.disabled = false;
      action.onclick = () => location.reload();
      action.focus();
    }
  };
  dismiss.disabled = false;
}

export async function showAssignedFolderLook(page) {
  const host = globalThis.document?.getElementById?.('workspaceAssignedFolderLook');
  if (!host) return;
  const guided = document.getElementById('workspaceDocumentsLook');
  const id = page.workspaceId;
  const generation = (page.lookGeneration = (page.lookGeneration || 0) + 1);
  const current = () =>
    !page._destroyed && page.workspaceId === id && page.lookGeneration === generation;
  const names = page.workspace?.shared_data?.anchor_directory_reference_id
    ? page.getWorkspaceAgentNames()
    : [];
  if (!page.workspace?.shared_data?.anchor_directory_reference_id || !names.length) {
    host.hidden = true;
    if (guided) guided.hidden = true;
    return;
  }
  const draft = await folderStaffingDraft(id);
  if (!current()) return;
  const managerOffer = guided && draft?.workspace_id === id && draft?.manager?.name;
  if (guided) guided.hidden = !managerOffer;
  if (managerOffer) {
    host.hidden = true;
    renderDocumentsLook(guided, page, draft, current);
    return;
  }
  host.hidden = draft?.workspace_id !== id || !(draft.look_link || draft.look_state);
  if (host.hidden) return;
  const select = host.querySelector('[data-look-agent]'),
    action = host.querySelector('[data-look-action]'),
    status = host.querySelector('[data-look-status]');
  const selected = select.value;
  select.replaceChildren(...names.map(name => new Option(name, name)));
  if (names.includes(selected)) select.value = selected;
  host.querySelector('[data-look-description]').textContent = draft.look_state
    ? 'This workspace already has a first look.'
    : 'Open one ticket to review folder names and structure and suggest setup. File contents stay unread. Nothing is moved or installed.';
  host.querySelector('[data-look-picker]').hidden = Boolean(draft.look_state);
  action.textContent = draft.look_state
    ? draft.look_state === 'looking'
      ? 'View progress'
      : 'View report'
    : 'Look around';
  action.disabled = Boolean(draft.look_state && !page.folderAgentPhase);
  status.textContent = action.disabled ? 'Loading saved first look…' : '';
  action.onclick = async () => {
    if (!current()) return;
    if (draft.look_state) {
      if (!page.commanderPresenter?.review?.())
        status.textContent = 'The saved look is loading. Try again in a moment.';
      return;
    }
    action.disabled = true;
    try {
      const r = await fetch(`/api/workspaces/${encodeURIComponent(id)}/folder-manager-look`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ link: draft.look_link, agent: select.value, brief: draft.brief })
      });
      if (!r.ok) throw Error();
      if (current()) location.reload();
    } catch {
      if (current()) {
        status.textContent =
          'The first look could not be verified. Reload to check the saved ticket or report.';
      }
    }
  };
}
