// One reason table for review cards, Home, the project library and build pane.
// Folder names are data; callers render this string as text, never as markup.
const FOLDER_PICK_COPY = {
  moved: folder =>
    `${folder} has moved or been renamed since you showed it to me. Pick it again to carry on.`,
  changed: folder =>
    `${folder} is not the folder you showed me: it was replaced. Pick it again to carry on.`,
  lost: folder => `Ori no longer has ${folder} open. Pick it again to carry on.`,
  expired: folder => `Ori no longer has ${folder} open. Pick it again to carry on.`
};

export function folderPickCopy(folder, reason) {
  const name = String(folder || '').trim() || 'that folder';
  const copy = Object.hasOwn(FOLDER_PICK_COPY, reason)
    ? FOLDER_PICK_COPY[reason]
    : FOLDER_PICK_COPY.lost;
  return copy(name);
}
