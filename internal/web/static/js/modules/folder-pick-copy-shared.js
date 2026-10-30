// One reason table for module-based cards and the classic-script build pane.
// Folder names are data; callers render this string as text, never as markup.
(function () {
  'use strict';

  const copy = {
    moved: folder =>
      `${folder} has moved or been renamed since you showed it to me. Pick it again to carry on.`,
    changed: folder =>
      `${folder} is not the folder you showed me: it was replaced. Pick it again to carry on.`,
    lost: folder => `Ori no longer has ${folder} open. Pick it again to carry on.`,
    expired: folder => `Ori no longer has ${folder} open. Pick it again to carry on.`
  };

  globalThis.OriFolderPickCopy = function folderPickCopy(folder, reason) {
    const name = String(folder || '').trim() || 'that folder';
    const message = Object.hasOwn(copy, reason) ? copy[reason] : copy.lost;
    return message(name);
  };
})();
