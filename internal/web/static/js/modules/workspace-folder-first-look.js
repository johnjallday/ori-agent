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

export async function showAssignedFolderLook(page) {
  const host = globalThis.document?.getElementById?.('workspaceAssignedFolderLook');
  if (!host) return;
  const id = page.workspaceId;
  const generation = (page.lookGeneration = (page.lookGeneration || 0) + 1);
  const current = () =>
    !page._destroyed && page.workspaceId === id && page.lookGeneration === generation;
  const names = page.workspace?.shared_data?.anchor_directory_reference_id
    ? page.getWorkspaceAgentNames()
    : [];
  if (!page.workspace?.shared_data?.anchor_directory_reference_id || !names.length) {
    host.hidden = true;
    return;
  }
  const draft = await folderStaffingDraft(id);
  if (!current()) return;
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
