// home-folder-search-dialogs.js — what a linked folder's row asks before it
// changes whether the folder is searchable.
//
// Merge Tree into Files, PR 2 (requirements 34-36). The consent dialog is the
// standalone page's own (search-desk.html, #folderConsent): the same title,
// sentence and buttons, naming the folder. Each dialog is a native modal
// <dialog> in the Explore card's look (explore-folder.css), built when it is
// asked and removed when it closes. Escape and Cancel answer "no".
//
// The wording is exported for the tests and kept in one place.

/** The standalone page's consent sentence, word for word. */
export const CONSENT_SCOPE =
  'Ori will index filenames, locations and dates in this folder on your Mac. It won’t read file contents or give agents access.';

/** The existing verify step for an older link (folder-search-review.js). */
export const VERIFY_SENTENCE =
  'I confirm this is the folder currently linked to this workspace. Its original disk identity was not recorded.';

export function consentTitle(label) {
  return `Allow Ori to search ${label}?`;
}

export function removeTitle(label) {
  return `Remove ${label} from search?`;
}

/** What Remove from search keeps, said before it is done (requirement 36). */
export const REMOVE_KEEPS =
  'Only this folder’s filename index is cleared. The workspace, its link to the folder, its notes, tickets and files all stay, and so does the folder itself.';

function element(tag, text, className) {
  const node = document.createElement(tag);
  if (text) node.textContent = text;
  if (className) node.className = className;
  return node;
}

/**
 * One modal question. `build(body)` adds anything between the description and
 * the buttons and may return `{ canAccept }` to hold the primary button until
 * an extra step is done. Resolves with the value of the button pressed, or ''
 * when the dialog was dismissed. Focus goes back to `returnFocus` afterwards:
 * an element, or a function that puts it back.
 */
function ask({
  title,
  description,
  accept,
  cancel = 'Cancel',
  danger = false,
  build,
  returnFocus
}) {
  return new Promise(resolve => {
    const dialog = element('dialog', null, 'explore-card folder-search-dialog');
    const heading = element('h2', title);
    const text = element('p', description, 'explore-card__description');
    heading.id = `folderSearchDialog-${Math.random().toString(36).slice(2)}`;
    text.id = `${heading.id}-text`;
    dialog.setAttribute('aria-labelledby', heading.id);
    dialog.setAttribute('aria-describedby', text.id);
    const form = element('form');
    form.method = 'dialog';
    const body = element('div');
    const actions = element('div', null, 'explore-card__actions');
    const cancelButton = element('button', cancel);
    cancelButton.type = 'submit';
    cancelButton.value = 'cancel';
    actions.append(cancelButton);
    let acceptButton = null;
    if (accept) {
      acceptButton = element(
        'button',
        accept,
        `explore-card__primary${danger ? ' folder-search-dialog__danger' : ''}`
      );
      acceptButton.type = 'submit';
      acceptButton.value = 'accept';
      actions.append(acceptButton);
    }
    const extra = typeof build === 'function' ? build(body) : null;
    const sync = () => {
      if (acceptButton && extra && typeof extra.canAccept === 'function') {
        acceptButton.disabled = !extra.canAccept();
      }
    };
    body.addEventListener('change', sync);
    sync();
    form.append(body, actions);
    dialog.append(
      element('small', 'Ori / Local file search', 'explore-card__kicker'),
      heading,
      text,
      form
    );
    dialog.returnValue = '';
    dialog.addEventListener(
      'close',
      () => {
        const answer = dialog.returnValue === 'accept' ? 'accept' : '';
        dialog.remove();
        // A function finds the row again: the tree may have been redrawn
        // while the dialog was open, replacing the element focus came from.
        if (typeof returnFocus === 'function') returnFocus();
        else if (returnFocus && returnFocus.isConnected) returnFocus.focus();
        resolve(answer);
      },
      { once: true }
    );
    document.body.append(dialog);
    dialog.showModal();
    // Like the standalone page: Cancel is where the keyboard starts.
    cancelButton.focus();
  });
}

/**
 * Make searchable: the consent dialog, naming the folder. With `verify` (an
 * older link whose identity was not recorded) the existing verify step must be
 * ticked before Allow search can be pressed. `{ allowed, verified }`.
 */
export async function askFolderConsent({ label, verify = false, returnFocus = null }) {
  let box = null;
  const answer = await ask({
    title: consentTitle(label),
    description: CONSENT_SCOPE,
    accept: 'Allow search',
    returnFocus,
    build: body => {
      if (!verify) return null;
      box = element('input');
      box.type = 'checkbox';
      const line = element('label', null, 'explore-card__check');
      line.append(box, document.createTextNode(VERIFY_SENTENCE));
      body.append(line);
      return { canAccept: () => box.checked };
    }
  });
  return { allowed: answer === 'accept', verified: !!(box && box.checked) };
}

/** Remove from search: what is cleared and what stays, then a yes or no. */
export async function askRemoveFromSearch({ label, returnFocus = null }) {
  const answer = await ask({
    title: removeTitle(label),
    description: REMOVE_KEEPS,
    accept: 'Remove from search',
    danger: true,
    returnFocus
  });
  return answer === 'accept';
}

/** A notice with one way out: something the row cannot do, and why. */
export async function showFolderNotice({ title, text, returnFocus = null }) {
  await ask({ title, description: text, cancel: 'OK', returnFocus });
}
