import { HQCaptureDraft, hqCaptureReference } from './hq-quick-capture.js';
import { notesRequest } from './notes-api.js';

function connectionProblem(view) {
  if (view?.setup_required === 'assistant')
    return 'Meet your assistant in My Notes to set up your notebook.';
  if (view?.setup_required === 'hq') return 'Build My HQ in My Notes to set up your notebook.';
  if (view?.status === 'missing') return 'Reconnect the missing folder in My Notes.';
  if (view?.status === 'connected') return 'This notebook is read-only for Ori.';
  if (view?.status === 'not_connected') return 'Set up a writable notebook in My Notes.';
  return 'Ori could not check your notebook. Open My Notes to check the connection.';
}

// The cockpit owns the modal lifecycle. This form owns only the notebook
// generation and the same in-memory, idempotent draft as HQ and My Notes.
export class HomeNoteForm {
  draft = new HQCaptureDraft('home-note');
  generation = 0;
  constructor(els, available, saved) {
    this.els = els;
    this.available = available;
    this.saved = saved;
  }
  wire() {
    const { captureTitle: field, captureDiscard } = this.els;
    field?.addEventListener('input', () => {
      this.draft.edit(field.value, this.ref);
      this.paint();
    });
    captureDiscard?.addEventListener('click', () => {
      this.draft.edit('', '');
      field.value = '';
      this.paint();
      field.focus();
    });
  }
  async check() {
    const generation = ++this.generation;
    this.checking = true;
    this.paint();
    let view;
    if (this.available()) {
      try {
        view = (await notesRequest('?view=connection')).second_brain;
      } catch {
        /* An unreadable connection never authorizes a save. */
      }
    }
    if (generation !== this.generation) return;
    this.checking = false;
    this.view = view;
    this.ref = hqCaptureReference(view);
    this.paint();
  }
  paint() {
    const {
      captureSave: save,
      captureTitle: field,
      captureStatus: status,
      captureSetup: setup,
      captureDiscard: discard
    } = this.els;
    const draft = this.draft;
    const stranded = Boolean(draft.text && this.ref !== draft.ref);
    save.disabled = draft.busy || this.checking || !this.ref || stranded;
    save.textContent = draft.busy ? 'Saving…' : 'Save to Inbox';
    field.readOnly = draft.busy || this.checking || stranded;
    discard.hidden = !stranded || this.checking || draft.busy;
    setup.hidden = this.checking || Boolean(this.ref);
    status.textContent = draft.busy
      ? 'Saving to Inbox…'
      : this.checking
        ? 'Checking your notebook…'
        : [
            !this.ref
              ? this.available()
                ? connectionProblem(this.view)
                : 'Finish Ori setup before saving a note.'
              : '',
            stranded
              ? 'Your draft was not sent to another notebook. Copy it before discarding.'
              : draft.problem
          ]
            .filter(Boolean)
            .join(' ');
  }
  async submit() {
    if (this.els.captureSave.disabled) return;
    const pending = this.draft.save(this.ref);
    this.paint();
    if (await pending) {
      this.els.captureTitle.value = '';
      this.saved(this.draft);
    } else if (this.draft.id) {
      // A rejected save can be the first sign of a changed connection.
      await this.check();
    }
    this.paint();
  }
}
