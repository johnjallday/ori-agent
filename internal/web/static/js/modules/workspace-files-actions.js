// Review original filesystem items, never workspace records or uploaded files.
import { escapeHtml as esc } from './note-editor.js';

export const isFolder = row => row?.kind === 'linkedFolder';
export const trashable = row =>
  (isFolder(row) || row?.kind === 'linkedFile') && !!row.meta.trashSelection;
export function countItems(rows) {
  const folders = rows.filter(isFolder).length;
  const kind = !folders ? 'file' : folders === rows.length ? 'folder' : 'item';
  return `${rows.length} ${kind}${rows.length === 1 ? '' : 's'}`;
}
export const trashLabel = rows =>
  rows.length === 1 ? 'Move to Trash…' : `Move ${countItems(rows)} to Trash…`;

// Returns null on cancellation, true for Trash, or the exact destination as
// offered before confirmation. No raw paths or transfer payloads are trusted.
export function reviewItems(rows, signal, destinations = null, preferred = null) {
  const folder = rows[0].meta.dirName;
  const folders = rows.some(isFolder);
  const song = rows.some(r => r.song) ? ' This selection includes a song on the map.' : '';
  const includes = folders ? ' Every selected folder moves with all of its contents.' : '';
  if (!destinations && rows.length === 1)
    return window.confirm(
      `Move “${rows[0].meta.path}” from ${folder} to your Mac’s Trash?\n\nThis moves the original ${folders ? 'folder and all of its contents' : 'file'}, not just its listing in Ori.${song}`
    );
  const dialog = document.createElement('dialog');
  dialog.className = 'wf-trash-review';
  const title = destinations ? `Move ${countItems(rows)}` : `Move ${countItems(rows)} to Trash?`;
  dialog.setAttribute('aria-label', title);
  const options = destinations
    ?.map(
      (d, i) =>
        `<option value="${i}" ${d.id === preferred?.id ? 'selected' : ''}>${esc([folder, d.meta.path].filter(Boolean).join(' / '))}</option>`
    )
    .join('');
  const choose = destinations
    ? `<label>Destination folder<select required>${preferred ? '' : '<option value="" selected disabled>Choose a folder…</option>'}${options}</select></label>`
    : '';
  const explanation = destinations
    ? `These originals will move within ${folder}. Nothing will be overwritten.`
    : `These originals will move from ${folder} to your Mac’s Trash.`;
  dialog.innerHTML = `<form method="dialog"><h2>${title}</h2><p>${esc(explanation + includes + song)}</p><ul>${rows.map(r => `<li>${esc(r.meta.path)}${isFolder(r) ? '/' : ''}</li>`).join('')}</ul>${choose}<div class="wf-actions"><button class="wf-action" value="cancel" autofocus formnovalidate>Cancel</button><button class="wf-action" value="confirm">${destinations ? 'Move here' : 'Move to Trash'}</button></div></form>`;
  document.body.append(dialog);
  return new Promise(resolve => {
    const cancel = () => dialog.close('cancel');
    signal.addEventListener('abort', cancel, { once: true });
    dialog.addEventListener(
      'close',
      () => {
        signal.removeEventListener('abort', cancel);
        const choice = dialog.querySelector('select')?.value;
        dialog.remove();
        resolve(
          dialog.returnValue === 'confirm' &&
            !signal.aborted &&
            (destinations ? choice !== '' && destinations[Number(choice)] : true)
        );
      },
      { once: true }
    );
    dialog.showModal();
  });
}

// Fixed, user-initiated settings links; neither the API nor a filename can
// provide the URL. The manual path works in clients that block deep links.
export function trashAccessHelp(code) {
  const settings =
    'x-apple.systempreferences:com.apple.settings.PrivacySecurity.extension?Privacy_';
  if (code === 'trash_directory_denied')
    return `<div class="wf-access-help"><strong>Folder access doesn’t include Trash.</strong><p>To move just this item, select it in Finder and press ⌘-Delete. No permission change is needed in Ori.</p><details><summary>Allow Ori to use Trash</summary><p>macOS has no Trash-only switch. Full Disk Access is much broader: it lets the app running Ori access protected files. If you trust that app, open System Settings → Privacy &amp; Security → Full Disk Access. Enable the app that launched Ori (for example Ori, Herdr, or Terminal), not your browser. If it’s missing, use the + button to add that app.</p><a href="${settings}AllFiles" rel="noopener">Open Full Disk Access settings</a><p>Quit and reopen that app, restart Ori, then refresh the folder and confirm the move again.</p></details></div>`;
  if (code === 'trash_access_denied')
    return `<div class="wf-access-help"><strong>Check folder access</strong><p>Open System Settings → Privacy &amp; Security → Files &amp; Folders. Expand the app that launched Ori (for example Ori, Herdr, or Terminal), not your browser. For Downloads, turn on Downloads Folder if listed.</p><a href="${settings}FilesAndFolders" rel="noopener">Open Files &amp; Folders settings</a><p>If the folder is not listed or access is already on, check the item’s Locked checkbox and the parent folder’s Sharing &amp; Permissions in Finder → Get Info. After any change, restart Ori if prompted, refresh the folder and select the item again.</p></div>`;
  return '';
}

export function canDrop(rows, dest) {
  if (!rows?.length || !dest?.destinationSelection) return false;
  return rows.every(
    r =>
      r.meta.moveSelection &&
      r.workspaceId === dest.workspaceId &&
      r.meta.dirId === dest.meta.dirId &&
      r.parent !== dest.id &&
      (!isFolder(r) ||
        (r.meta.path !== dest.meta.path && !dest.meta.path.startsWith(`${r.meta.path}/`)))
  );
}
