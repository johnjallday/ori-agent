import { escapeHtml as esc } from './note-editor.js';

const palette = new Map(
  Object.entries({
    Red: '#ef4444',
    Orange: '#f97316',
    Yellow: '#eab308',
    Green: '#22c55e',
    Blue: '#3b82f6',
    Purple: '#8b5cf6',
    Pink: '#ec4899'
  })
);
const prefix = 'ori.workspace-folder-color.v1:';
export function folderColor(row) {
  if (!row.meta?.colorKey) return null;
  try {
    const name = localStorage.getItem(prefix + row.meta.colorKey);
    return palette.has(name) ? { name, hex: palette.get(name) } : null;
  } catch {
    return null;
  }
}

export function chooseFolderColor(rows, signal, done) {
  const dialog = document.createElement('dialog');
  dialog.className = 'wf-trash-review wf-color-review';
  const title =
    rows.length === 1 ? `Folder color: ${rows[0].label}` : `Color ${rows.length} folders`;
  const current = rows.map(r => folderColor(r)?.name || 'Default');
  dialog.setAttribute('aria-label', title);
  dialog.innerHTML = `<form method="dialog"><h2>${esc(title)}</h2><p>Shown in Files in this browser. Finder tags don’t change.</p><div class="wf-actions">${[...palette, ['Default', 'transparent']].map(([name, hex]) => `<button class="wf-action" value="${name}" aria-pressed="${current.every(c => c === name)}"><span class="wf-swatch" aria-hidden="true" style="background:${hex}"></span>${name === 'Default' ? 'Clear color' : name}</button>`).join('')}</div><button class="wf-action" autofocus value="cancel">Cancel</button></form>`;
  const close = () => dialog.close('cancel');
  signal.addEventListener('abort', close, { once: true });
  dialog.addEventListener(
    'close',
    () => {
      signal.removeEventListener('abort', close);
      const color = dialog.returnValue;
      dialog.remove();
      if (signal.aborted || (!palette.has(color) && color !== 'Default')) return;
      try {
        for (const r of rows) {
          if (color === 'Default') localStorage.removeItem(prefix + r.meta.colorKey);
          else localStorage.setItem(prefix + r.meta.colorKey, color);
        }
      } catch {
        done('Folder color could not be saved in this browser.');
        return;
      }
      done(color === 'Default' ? 'Folder color cleared.' : `Folder color set to ${color}.`);
    },
    { once: true }
  );
  document.body.append(dialog);
  dialog.showModal();
}
