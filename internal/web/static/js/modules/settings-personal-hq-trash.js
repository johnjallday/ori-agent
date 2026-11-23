async function jsonResponse(response, fallback) {
  const payload = await response.json().catch(() => ({}));
  const message = payload?.message || payload?.error;
  if (!response.ok) throw new Error(typeof message === 'string' ? message : fallback);
  return payload;
}

export function wirePersonalHQTrash({ button, status, request, confirm }) {
  if (!button || !status) return;
  button.addEventListener('click', async () => {
    if (button.disabled) return;
    button.disabled = true;
    let confirmed = false;
    status.textContent = 'Checking the current Personal HQ folder…';
    try {
      const current = await jsonResponse(
        await request('/api/personal-hq/status', { cache: 'no-store' }),
        'Could not check the current Personal HQ.'
      );
      const id = current?.status?.workspace_id;
      if (!current?.status?.valid || !id) {
        status.textContent = 'No current Personal HQ is registered. No files were moved.';
        return;
      }
      const url = `/api/workspaces/${encodeURIComponent(id)}?trash_personal_hq=true`;
      const review = await jsonResponse(
        await request(url, { method: 'DELETE', headers: { 'X-Requested-With': 'XMLHttpRequest' } }),
        'Could not review the Personal HQ folder.'
      );
      const path = review?.folder_path;
      if (typeof path !== 'string' || !path)
        throw new Error('Ori did not return a folder to review.');
      if (
        !confirm(
          `Move “${review.name || 'Personal HQ'}” and everything inside this folder to the system Trash?\n\n${path}\n\nThis removes it from your active workspaces. It does not reset the rest of Ori or erase conversation records.`
        )
      ) {
        status.textContent = 'Cancelled. No files were moved.';
        return;
      }
      confirmed = true;
      status.textContent = 'Moving Personal HQ to Trash…';
      const result = await jsonResponse(
        await request(`${url}&confirm=true`, {
          method: 'DELETE',
          headers: { 'Content-Type': 'application/json', 'X-Requested-With': 'XMLHttpRequest' },
          body: JSON.stringify({ folder_path: path })
        }),
        'Could not confirm the Trash move. Check the folder and the system Trash before retrying.'
      );
      if (result?.trashed !== true)
        throw new Error(
          'Ori did not confirm the Trash move. Check the folder and the system Trash before retrying.'
        );
      status.textContent =
        'Personal HQ moved to the system Trash and removed from your active workspaces.';
    } catch (error) {
      status.textContent =
        confirmed && error instanceof TypeError
          ? 'Ori did not confirm the Trash move. Check the folder and the system Trash before retrying.'
          : error.message || 'Personal HQ removal is unavailable.';
    } finally {
      button.disabled = false;
    }
  });
}

if (typeof document !== 'undefined') {
  wirePersonalHQTrash({
    button: document.getElementById('reviewPersonalHQTrashBtn'),
    status: document.getElementById('personalHQTrashStatus'),
    request: (...args) => fetch(...args),
    confirm: message => window.confirm(message)
  });
}
