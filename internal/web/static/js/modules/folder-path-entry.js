// The "or paste a folder path" control offered beside every button that opens
// the native folder dialog.
//
// A typed path reaches the server in exactly one place: select-path's `path`.
// The server checks it and answers with the same selection (path + token) a
// dialog choice produces, so each surface keeps the flow it already has and
// never sends a path to its own endpoint.

export const FOLDER_PATH_ENDPOINT = '/api/folder-picker/select-path';
export const FOLDER_PATH_LABEL = 'or paste a folder path';
export const FOLDER_PATH_BUTTON = 'Use this path';
export const FOLDER_PATH_PLACEHOLDER = '~/Music/Projects';

const REFUSED = 'That folder could not be used. Check the path and try again.';

let entryCounter = 0;

/**
 * The sentence to show when a picker endpoint answered "the dialog is off"
 * instead of opening it; '' for any other answer. The reason is a code and is
 * never shown.
 */
export function folderDialogOffNote(result) {
  if (!result || result.reason !== 'dialog_off') return '';
  return String(result.note || '').trim() || 'The folder dialog is switched off in this session.';
}

/**
 * Ask the server to accept a typed path. Resolves with the selection
 * ({ path, selectionToken, warningNote }) or rejects with an Error whose
 * message is the server's sentence.
 */
export async function selectTypedFolder(
  path,
  { workspaceId = '', title = '', fetchImpl = globalThis.fetch } = {}
) {
  const typed = String(path || '').trim();
  if (!typed) throw new Error('Type or paste a folder path first.');
  const body = { path: typed };
  if (workspaceId) body.workspace_id = workspaceId;
  if (title) body.title = title;
  let response;
  let result = null;
  try {
    response = await fetchImpl(FOLDER_PATH_ENDPOINT, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
      body: JSON.stringify(body)
    });
    result = await response.json();
  } catch (_) {
    throw new Error(REFUSED);
  }
  if (!response.ok || !result?.success || !result.selected) {
    throw new Error(String(result?.error || '').trim() || REFUSED);
  }
  return {
    path: String(result.path || ''),
    selectionToken: String(result.selection_token || ''),
    warningNote: String(result.warning_note || '').trim()
  };
}

function node(doc, tag, className, text) {
  const element = doc.createElement(tag);
  if (className) element.className = className;
  if (text != null) element.textContent = String(text);
  return element;
}

/**
 * Build the control. `onSelect(selection)` receives what the server accepted
 * and may return a promise; a rejection is shown as the control's error and
 * the typed text is kept. `note` is the sentence shown when the dialog is off.
 *
 * Returns { element, input, setNote, setDisabled, focus, reset }.
 */
export function createFolderPathEntry({
  doc = globalThis.document,
  workspaceId = '',
  title = '',
  note = '',
  label = FOLDER_PATH_LABEL,
  buttonLabel = FOLDER_PATH_BUTTON,
  busyLabel = 'Checking…',
  onSelect = () => {},
  select = selectTypedFolder,
  fetchImpl
} = {}) {
  const id = `folder-path-entry-${++entryCounter}`;
  const element = node(doc, 'div', 'folder-path-entry');
  const caption = node(doc, 'label', 'folder-path-entry__label', label);
  caption.setAttribute('for', id);
  const row = node(doc, 'div', 'folder-path-entry__row');
  const input = node(doc, 'input', 'modern-input folder-path-entry__input');
  input.id = id;
  input.type = 'text';
  input.placeholder = FOLDER_PATH_PLACEHOLDER;
  input.setAttribute('autocomplete', 'off');
  input.setAttribute('autocapitalize', 'off');
  input.setAttribute('spellcheck', 'false');
  const use = node(
    doc,
    'button',
    'modern-btn modern-btn-secondary folder-path-entry__use',
    buttonLabel
  );
  use.type = 'button';
  const noteLine = node(doc, 'p', 'folder-path-entry__note');
  const status = node(doc, 'p', 'folder-path-entry__status');
  status.setAttribute('role', 'status');
  status.setAttribute('aria-live', 'polite');
  input.setAttribute('aria-describedby', `${id}-status`);
  status.id = `${id}-status`;
  row.append(input, use);
  element.append(caption, row, noteLine, status);

  let busy = false;
  let disabled = false;

  function setNote(text) {
    noteLine.textContent = String(text || '').trim();
    noteLine.hidden = !noteLine.textContent;
  }
  function setStatus(text, failed = false) {
    status.textContent = String(text || '');
    status.hidden = !status.textContent;
    status.classList.toggle('is-error', Boolean(failed && status.textContent));
  }
  function reflect() {
    input.disabled = busy || disabled;
    use.disabled = busy || disabled;
    use.textContent = busy ? busyLabel : buttonLabel;
  }

  async function submit() {
    if (busy || disabled) return;
    if (!String(input.value || '').trim()) {
      setStatus('Type or paste a folder path first.', true);
      input.focus();
      return;
    }
    busy = true;
    reflect();
    setStatus('');
    try {
      const selection = await select(input.value, { workspaceId, title, fetchImpl });
      await onSelect(selection);
      input.value = '';
      setStatus(selection.warningNote);
    } catch (error) {
      setStatus((error && error.message) || REFUSED, true);
    } finally {
      busy = false;
      reflect();
      if (status.classList.contains('is-error')) input.focus();
    }
  }

  use.addEventListener('click', submit);
  input.addEventListener('keydown', event => {
    if (event.key !== 'Enter') return;
    // The field often sits inside a form or dialog whose own Enter submits it.
    event.preventDefault();
    event.stopPropagation();
    submit();
  });

  setNote(note);
  setStatus('');
  reflect();

  return {
    element,
    input,
    setNote,
    setDisabled(flag) {
      disabled = Boolean(flag);
      reflect();
    },
    focus: () => input.focus(),
    reset() {
      input.value = '';
      setStatus('');
    }
  };
}

/**
 * A small dialog for a surface whose button used to open the native dialog
 * straight away and so has nowhere to put the field: "Choose folder…" and the
 * path control, side by side.
 *
 * `onChoose()` opens the native dialog and resolves with the picker endpoint's
 * answer; `onSelect(selection)` handles a typed path. Either closes the dialog
 * when it resolves truthy. Returns { dialog, entry, close } or null when there
 * is no document to render into.
 */
export function openFolderPathDialog({
  doc = globalThis.document,
  heading = 'Link a folder',
  copy = '',
  chooseLabel = 'Choose folder…',
  workspaceId = '',
  title = '',
  onChoose = null,
  onSelect = () => {},
  select,
  fetchImpl
} = {}) {
  if (!doc || !doc.body || typeof doc.createElement !== 'function') return null;

  const opener = doc.activeElement;
  const dialog = node(doc, 'dialog', 'folder-path-dialog');
  const headingNode = node(doc, 'h2', 'folder-path-dialog__title', heading);
  headingNode.id = `folder-path-dialog-title-${++entryCounter}`;
  dialog.setAttribute('aria-labelledby', headingNode.id);
  const body = node(doc, 'div', 'folder-path-dialog__body');
  if (copy) body.append(node(doc, 'p', 'folder-path-dialog__copy', copy));
  const actions = node(doc, 'div', 'folder-path-dialog__actions');
  const cancel = node(doc, 'button', 'modern-btn modern-btn-secondary', 'Cancel');
  cancel.type = 'button';

  let closed = false;
  function close() {
    if (closed) return;
    closed = true;
    if (dialog.open && typeof dialog.close === 'function') dialog.close();
    dialog.remove();
    if (opener && typeof opener.focus === 'function' && opener.isConnected !== false) {
      opener.focus();
    }
  }

  const entry = createFolderPathEntry({
    doc,
    workspaceId,
    title,
    select,
    fetchImpl,
    onSelect: async selection => {
      if ((await onSelect(selection)) !== false) close();
    }
  });

  let choose = null;
  if (typeof onChoose === 'function') {
    choose = node(
      doc,
      'button',
      'modern-btn modern-btn-primary folder-path-dialog__choose',
      chooseLabel
    );
    choose.type = 'button';
    choose.addEventListener('click', async () => {
      choose.disabled = true;
      try {
        const result = await onChoose();
        const off = folderDialogOffNote(result);
        if (off) {
          // The dialog cannot open here: say so and leave the field.
          entry.setNote(off);
          choose.hidden = true;
          entry.focus();
          return;
        }
        if (result) close();
      } finally {
        if (!closed) choose.disabled = false;
      }
    });
    body.append(choose);
  }
  body.append(entry.element);
  cancel.addEventListener('click', close);
  dialog.addEventListener('cancel', event => {
    event.preventDefault();
    close();
  });
  actions.append(cancel);
  dialog.append(headingNode, body, actions);

  doc.body.append(dialog);
  if (typeof dialog.showModal === 'function') dialog.showModal();
  else dialog.setAttribute('open', '');
  (choose || entry.input).focus();
  return { dialog, entry, close };
}
