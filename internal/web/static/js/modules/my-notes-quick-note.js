import { HQCaptureDraft, hqCaptureReference } from './hq-quick-capture.js';

// The Quick note contract, shared with HQ's capture: every note goes to the
// notebook's Inbox, whatever folder is open, and says so before and after.
export const QUICK_NOTE_DESTINATION = 'My Notes → Inbox';
export const QUICK_NOTE_RECEIPT = 'Saved to My Notes → Inbox.';

// inboxHasUserNotes says whether Inbox already holds a note the person wrote
// (a capture or their own file), from GET …/inbox alone: Inbox, not the tree.
// Setup files (seed and follow-up notes, each folder's README) do not count.
// A listing that was cut short cannot prove there is none.
export function inboxHasUserNotes(inbox) {
  const notes = Array.isArray(inbox?.notes) ? inbox.notes : [];
  if (notes.some(note => note.kind === 'capture' || note.kind === 'user')) return true;
  return inbox?.partial === true || Number(inbox?.total) > notes.length;
}

// quickNoteEntry is the status bar's Quick note control, or null. It exists
// only where Ori may write (an Ori notebook that is there), or while a draft
// written for an earlier connection still needs a decision.
export function quickNoteEntry({ view, draft, firstNote = false }) {
  const ref = hqCaptureReference(view);
  if (draft?.text && draft.ref && draft.ref !== ref) {
    return { label: 'Unsent note', stranded: true };
  }
  if (!ref) return null;
  return { label: firstNote ? 'Write your first note' : 'Quick note', stranded: false };
}

function node(tag, props = {}, ...children) {
  const el = document.createElement(tag);
  for (const [key, value] of Object.entries(props)) {
    if (value === undefined || value === null || value === false) continue;
    if (key === 'className') el.className = value;
    else if (key === 'text') el.textContent = value;
    else if (key === 'dataset') Object.assign(el.dataset, value);
    else if (key in el && typeof value !== 'string') el[key] = value;
    else el.setAttribute(key, value === true ? '' : String(value));
  }
  el.append(...children.filter(Boolean));
  return el;
}

function control(text, action, primary = false, type = 'button') {
  return node('button', {
    type,
    className: `modern-btn modern-btn-${primary ? 'primary' : 'secondary'} modern-btn-sm`,
    dataset: { quickAction: action },
    text
  });
}

// MyNotesQuickNote is the Quick note dialog. It never touches the notebook
// browser: the folder, note and scroll behind it stay as they were.
export class MyNotesQuickNote {
  constructor(dialog, { saved, failed, returnFocus, changed = () => {} }) {
    this.dialog = dialog;
    this.saved = saved;
    this.failed = failed;
    this.returnFocus = returnFocus;
    // The status bar's control follows what happened to an earlier draft.
    this.changed = changed;
    this.draft = new HQCaptureDraft('my-notes');
    this.view = null;
    this.firstNote = false;
    dialog.addEventListener('close', () => this.returnFocus());
    // The backdrop closes the dialog; a drag that starts inside it does not.
    dialog.addEventListener('pointerdown', event => {
      this.downOnBackdrop = event.target === dialog;
    });
    dialog.addEventListener('click', event => {
      if (event.target === dialog && this.downOnBackdrop) dialog.close();
      const action = event.target.closest('[data-quick-action]')?.dataset.quickAction;
      if (action) this.act(action);
    });
    dialog.addEventListener('submit', event => {
      event.preventDefault();
      void this.submit();
    });
    dialog.addEventListener('input', event => {
      if (event.target.matches('[data-quick-text]'))
        this.draft.edit(event.target.value, hqCaptureReference(this.view));
    });
    dialog.addEventListener('keydown', event => {
      if (
        event.key === 'Enter' &&
        (event.metaKey || event.ctrlKey) &&
        event.target.matches('[data-quick-text]') &&
        !event.isComposing
      ) {
        event.preventDefault();
        void this.submit();
      }
    });
  }

  get stranded() {
    return Boolean(
      this.draft.text && this.draft.ref && this.draft.ref !== hqCaptureReference(this.view)
    );
  }

  open(view, { firstNote = false } = {}) {
    this.view = view;
    this.firstNote = firstNote;
    this.draft.receipt = '';
    this.render();
    if (!this.dialog.open) this.dialog.showModal();
    this.focusField();
  }

  // update follows the connection. An open dialog shows a draft for an earlier
  // connection as read-only until the person decides what happens to it.
  update(view, { firstNote = this.firstNote } = {}) {
    const before = this.shape();
    this.view = view;
    this.firstNote = firstNote;
    // A refresh of the same connection leaves the field (and its caret) alone.
    if (!this.dialog.open || this.shape() === before) return;
    const inside = this.dialog.contains(document.activeElement);
    this.render();
    if (inside) this.focusField();
  }

  shape() {
    return `${this.stranded}:${Boolean(hqCaptureReference(this.view))}`;
  }

  focusField() {
    const target =
      this.dialog.querySelector('[data-quick-text]:not([readonly])') ||
      this.dialog.querySelector('[data-quick-action]');
    target?.focus();
  }

  act(action) {
    if (action === 'cancel') {
      this.dialog.close();
    } else if (action === 'discard') {
      this.draft.edit('', '');
      this.changed();
      if (hqCaptureReference(this.view)) {
        this.render();
        this.focusField();
      } else {
        this.dialog.close();
      }
    } else if (action === 'retarget') {
      if (this.draft.retarget(hqCaptureReference(this.view))) {
        this.changed();
        this.render();
        this.focusField();
      }
    }
  }

  async submit() {
    if (this.draft.busy || this.stranded) return;
    const pending = this.draft.save(hqCaptureReference(this.view));
    this.render();
    const ok = await pending;
    if (ok) {
      this.firstNote = false;
      if (this.dialog.open) this.dialog.close();
      this.saved(QUICK_NOTE_RECEIPT);
      return;
    }
    if (this.dialog.open) {
      this.render();
      this.focusField();
    }
    this.failed(this.dialog.open);
  }

  render() {
    const draft = this.draft;
    const writable = Boolean(hqCaptureReference(this.view));
    const stranded = this.stranded;
    const title = stranded
      ? 'Unsent note'
      : this.firstNote
        ? 'Write your first note'
        : 'Quick note';
    const destination = node(
      'p',
      { className: 'my-notes-quick-destination' },
      'Saves to ',
      node('strong', { text: QUICK_NOTE_DESTINATION }),
      ' — you stay on the note you are reading.'
    );
    const field = node('textarea', {
      id: 'my-notes-quick-text',
      className: 'form-control',
      rows: 5,
      maxLength: 4000,
      placeholder: 'A thought to keep…',
      readOnly: draft.busy || stranded,
      dataset: { quickText: '' }
    });
    field.value = draft.text;
    const label = node('label', {
      for: field.id,
      text: stranded
        ? 'Draft from your previous notes connection'
        : `Note for ${QUICK_NOTE_DESTINATION}`
    });
    const actions = node('div', { className: 'my-notes-quick-actions' });
    if (stranded) {
      if (writable) actions.append(control('Keep for this notebook', 'retarget', true));
      actions.append(control('Discard draft', 'discard'), control('Close', 'cancel'));
    } else {
      const save = control(draft.busy ? 'Saving…' : 'Save to Inbox', 'save', true, 'submit');
      save.disabled = draft.busy;
      actions.append(save, control('Cancel', 'cancel'));
    }
    const form = node('form', { className: 'my-notes-quick-form' }, label, field);
    if (stranded) {
      form.append(
        node('p', {
          className: 'my-notes-quick-line',
          text: writable
            ? 'Your notes connection changed. Ori won’t send this draft to the new notebook unless you keep it for this one.'
            : 'Your notes connection changed and Ori can’t write here. Copy this draft if you want to keep it.'
        })
      );
    }
    if (draft.problem)
      form.append(
        node('p', { className: 'my-notes-quick-problem', role: 'alert', text: draft.problem })
      );
    else if (!stranded)
      form.append(
        node('p', {
          className: 'my-notes-quick-line',
          text: 'Cmd/Ctrl + Enter saves. Esc keeps the draft.'
        })
      );
    form.append(actions);
    this.dialog.replaceChildren(
      node(
        'div',
        { className: 'my-notes-quick-body' },
        node('h2', { id: 'my-notes-quick-title', text: title }),
        stranded ? null : destination,
        form
      )
    );
  }
}
