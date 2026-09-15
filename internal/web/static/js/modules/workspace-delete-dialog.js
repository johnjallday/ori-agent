// The one in-app dialog for deleting workspaces and groups from Home.
//
// Deleting used to run on native `window.confirm()` chains: a group asked
// "OK = delete everything, Cancel = maybe keep the workspaces", then asked
// again, and a protected Assistant Home answered a third time with "open the
// Assistant Home now?" and navigated away. Native dialogs cannot be styled,
// cannot show a choice as a choice, and block browser automation.
//
// This module renders a single `<dialog>` that stays open while one deletion
// runs through its steps. It knows nothing about the API: the controller in
// workspace-bulk-actions.js drives it step by step, so the decision logic stays
// testable under plain Node and this file stays a dumb view.
//
// Session contract (every step returns a promise the controller awaits):
//   chooseDelete({ name, group, memberCount }) -> { mode } | null
//   confirmCount({ count })                    -> boolean
//   busy(text)                                 disable controls, show progress
//   review({ heading, summary, impact, confirmLabel, progress, confirm })
//                                              -> boolean (true once `confirm` resolved)
//   notice({ message, action })                -> resolves when dismissed
//   close()                                    remove the dialog, restore focus
//
// Escape and Cancel resolve the pending step with its "declined" value
// (null / false / undefined), so the controller never has to special-case them.

let dialogCounter = 0;

function node(doc, tag, className, textContent) {
  const element = doc.createElement(tag);
  if (className) element.className = className;
  if (textContent != null) element.textContent = String(textContent);
  return element;
}

function plural(count, noun) {
  return `${count} ${noun}${count === 1 ? '' : 's'}`;
}

/**
 * Open the delete dialog. Returns null when there is no document to render
 * into, which the controller treats as "no way to ask, so nothing is deleted".
 */
export function openDeleteDialog({ title = '', doc = globalThis.document } = {}) {
  if (!doc || !doc.body || typeof doc.createElement !== 'function') return null;

  const opener = doc.activeElement;
  const dialog = node(doc, 'dialog', 'ws-delete-dialog');
  const heading = node(doc, 'h2', 'ws-delete-dialog__title', title);
  heading.id = `ws-delete-dialog-title-${++dialogCounter}`;
  dialog.setAttribute('aria-labelledby', heading.id);
  const body = node(doc, 'div', 'ws-delete-dialog__body');
  const status = node(doc, 'p', 'ws-delete-dialog__status');
  status.setAttribute('role', 'status');
  status.setAttribute('aria-live', 'polite');
  const error = node(doc, 'p', 'ws-delete-dialog__error');
  error.setAttribute('role', 'alert');
  const actions = node(doc, 'div', 'ws-delete-dialog__actions');
  dialog.append(heading, body, status, error, actions);

  let pending = null; // { resolve, declined } for the step the controller awaits
  let busy = false;
  let closed = false;

  function settle(value) {
    const current = pending;
    pending = null;
    if (current) current.resolve(value);
  }

  function step(declined) {
    return new Promise(resolve => {
      pending = { resolve, declined };
    });
  }

  function close() {
    if (closed) return;
    closed = true;
    if (pending) settle(pending.declined);
    if (dialog.open && typeof dialog.close === 'function') dialog.close();
    dialog.remove();
    if (opener && typeof opener.focus === 'function' && opener.isConnected !== false) {
      opener.focus();
    }
  }

  dialog.addEventListener('cancel', event => {
    // Escape. While a request is in flight the dialog must stay put, otherwise
    // the user loses the only place the outcome will be reported.
    event.preventDefault();
    if (!busy) close();
  });

  function setBusy(flag, text = '') {
    busy = flag;
    dialog.classList.toggle('is-busy', flag);
    for (const control of actions.querySelectorAll('button')) control.disabled = flag;
    status.textContent = text;
  }

  function showError(message) {
    error.textContent = message || '';
  }

  function button(label, className, onClick) {
    const control = node(doc, 'button', `modern-btn ${className}`, label);
    control.type = 'button';
    control.addEventListener('click', onClick);
    return control;
  }

  function present({ title: nextTitle, content, controls, focus }) {
    heading.textContent = nextTitle;
    body.replaceChildren(...content);
    actions.replaceChildren(...controls);
    showError('');
    setBusy(false, '');
    if (focus && typeof focus.focus === 'function') focus.focus();
  }

  function modeOption(value, label, detail, checked) {
    const wrapper = node(doc, 'label', 'ws-delete-dialog__mode');
    const input = node(doc, 'input');
    input.type = 'radio';
    input.name = `${heading.id}-mode`;
    input.value = value;
    input.checked = checked;
    const copy = node(doc, 'span', 'ws-delete-dialog__mode-copy');
    copy.append(
      node(doc, 'strong', '', label),
      node(doc, 'small', 'ws-delete-dialog__mode-detail', detail)
    );
    wrapper.append(input, copy);
    return wrapper;
  }

  const session = {
    chooseDelete({ name = '', group = false, memberCount = 0 } = {}) {
      const count = Math.max(0, Number(memberCount) || 0);
      const content = [];
      let readMode = () => '';
      if (group && count > 0) {
        const members = plural(count, 'workspace');
        const fieldset = node(doc, 'fieldset', 'ws-delete-dialog__modes');
        fieldset.append(
          node(
            doc,
            'legend',
            'ws-delete-dialog__legend',
            `What should happen to the ${members} inside it?`
          ),
          modeOption(
            'group_only',
            'Delete only the group',
            `The ${members} move back to the top level. The group moves to the Trash and can be restored with Undo.`,
            true
          ),
          modeOption(
            'contents',
            'Delete the group and everything inside it',
            `The group and its ${members} move to the Trash together and can be restored with Undo.`,
            false
          )
        );
        content.push(fieldset);
        const reflectSelection = () => {
          for (const option of fieldset.querySelectorAll('.ws-delete-dialog__mode')) {
            option.classList.toggle('is-selected', !!option.querySelector('input:checked'));
          }
        };
        fieldset.addEventListener('change', reflectSelection);
        reflectSelection();
        readMode = () => fieldset.querySelector('input:checked')?.value || 'group_only';
      } else if (group) {
        content.push(
          node(
            doc,
            'p',
            'ws-delete-dialog__copy',
            'The group is empty. It moves to the Trash and can be restored with Undo.'
          )
        );
        readMode = () => 'group_only';
      } else {
        content.push(
          node(
            doc,
            'p',
            'ws-delete-dialog__copy',
            'It moves to the Trash and can be restored with Undo.'
          )
        );
      }
      const cancel = button('Cancel', 'modern-btn-secondary', close);
      const confirm = button('Delete', 'modern-btn-danger', () => settle({ mode: readMode() }));
      present({
        title: `Delete "${name}"?`,
        content,
        controls: [cancel, confirm],
        focus: cancel
      });
      return step(null);
    },

    confirmCount({ count = 0 } = {}) {
      const total = Math.max(0, Number(count) || 0);
      const cancel = button('Cancel', 'modern-btn-secondary', close);
      const confirm = button('Delete', 'modern-btn-danger', () => settle(true));
      present({
        title: `Delete ${plural(total, 'selected item')}?`,
        content: [
          node(
            doc,
            'p',
            'ws-delete-dialog__copy',
            'They move to the Trash and can be restored with Undo.'
          )
        ],
        controls: [cancel, confirm],
        focus: cancel
      });
      return step(false);
    },

    busy(text = '') {
      showError('');
      setBusy(true, text);
    },

    review({
      heading: reviewTitle = '',
      summary = '',
      impact = [],
      confirmLabel = 'Remove',
      progress = 'Removing…',
      confirm = async () => {}
    } = {}) {
      const content = [];
      if (summary) content.push(node(doc, 'p', 'ws-delete-dialog__summary', summary));
      const lines = (Array.isArray(impact) ? impact : [])
        .map(line => String(line || ''))
        .filter(Boolean);
      if (lines.length) {
        const list = node(doc, 'ul', 'ws-delete-dialog__impact');
        for (const line of lines) list.append(node(doc, 'li', '', line));
        content.push(list);
      }
      const cancel = button('Cancel', 'modern-btn-secondary', close);
      const proceed = button(confirmLabel, 'modern-btn-danger', async () => {
        setBusy(true, progress);
        showError('');
        try {
          await confirm();
          settle(true);
        } catch (err) {
          setBusy(false, '');
          showError((err && err.message) || 'The removal could not be completed.');
          proceed.focus();
        }
      });
      present({
        title: reviewTitle,
        content,
        controls: [cancel, proceed],
        focus: cancel
      });
      return step(false);
    },

    notice({ message = '', action = null } = {}) {
      const controls = [];
      if (action && action.href && action.label) {
        const link = node(doc, 'a', 'modern-btn modern-btn-primary', action.label);
        link.href = action.href;
        controls.push(link);
      }
      const dismiss = button('Close', 'modern-btn-secondary', () => settle(undefined));
      controls.push(dismiss);
      present({
        title: heading.textContent,
        content: [node(doc, 'p', 'ws-delete-dialog__copy', message)],
        controls,
        focus: dismiss
      });
      return step(undefined);
    },

    close
  };

  doc.body.append(dialog);
  if (typeof dialog.showModal === 'function') dialog.showModal();
  else dialog.setAttribute('open', '');
  return session;
}
