import { notesPost } from './notes-api.js';
import { newRequestID, secondBrainCaptureProblem } from './personal-assistant-second-brain.js';

export const MY_NOTES_URL = '/my-notes';

export function hqCaptureReference(view) {
  return view?.status === 'connected' && view.source?.origin === 'created_by_ori'
    ? view.browse_ref || ''
    : '';
}

export class HQCaptureDraft {
  text = '';
  ref = '';
  id = '';
  busy = false;
  problem = '';
  receipt = '';
  receiptURL = '';

  constructor(prefix = 'hq-capture') {
    this.prefix = prefix;
  }

  // retarget sends a draft written for an earlier connection to the current
  // one, only because the person reviewed it and asked. A new destination is a
  // new request.
  retarget(ref) {
    if (this.busy || !this.text || !ref || ref === this.ref) return false;
    this.ref = ref;
    this.id = '';
    this.problem = '';
    this.receipt = '';
    return true;
  }

  edit(text, ref) {
    if (this.busy || this.text === text) return;
    this.text = text;
    this.ref = text ? this.ref || ref : '';
    this.id = '';
    this.receipt = '';
    this.problem = '';
  }

  async save(ref, send = notesPost) {
    if (this.busy) return false;
    this.receipt = '';
    if (!ref || ref !== this.ref) {
      this.problem = this.text
        ? 'The notes connection changed. This draft was not sent.'
        : 'Write a note first.';
      return false;
    }
    this.problem = secondBrainCaptureProblem(this.text);
    if (this.problem) return false;
    this.id ||= newRequestID(this.prefix);
    this.busy = true;
    try {
      const { capture } = await send(
        '/capture',
        { text: this.text, source_ref: this.ref },
        this.id
      );
      if (typeof capture?.note !== 'string' || !/^Inbox\/[^/\\]+\.md$/u.test(capture.note))
        throw new Error('No save receipt was returned. Try again to check the save.');
      this.text = '';
      this.ref = '';
      this.id = '';
      this.receipt = 'Saved to My Notes → Inbox.';
      // A fragment keeps the note filename out of Home navigation request logs.
      this.receiptURL = `${MY_NOTES_URL}#${new URLSearchParams({ ref, note: capture.note.slice(6) })}`;
      return true;
    } catch (error) {
      this.problem = error.message || 'Ori could not confirm the save. Your draft is still here.';
      return false;
    } finally {
      this.busy = false;
    }
  }
}

function node(tag, text, className = '') {
  const element = document.createElement(tag);
  element.textContent = text;
  if (className) element.className = className;
  return element;
}

function link(text, href) {
  const element = node('a', text, 'modern-btn modern-btn-secondary modern-btn-sm');
  element.href = href;
  element.dataset.hqCaptureControl = href;
  return element;
}

export class HQQuickCapture {
  draft = new HQCaptureDraft();

  constructor(changed) {
    this.changed = changed;
  }

  mount(host, notes) {
    this.host = host;
    this.notes = notes;
    if (host) this.render();
  }

  focus() {
    const field = this.host?.querySelector('input:not([readonly])');
    field?.focus();
    return Boolean(field);
  }

  render() {
    const host = this.host;
    if (!host) return;
    const notes = this.notes;
    const ref = hqCaptureReference(notes);
    const draft = this.draft;
    const changed = Boolean(draft.text && draft.ref !== ref);
    const heading = node('h3', 'My Notes', 'ws-hq-capture-title');
    const actions = node('div', '', 'ws-hq-capture-actions');
    const open = link('Open My Notes', MY_NOTES_URL);
    const journal = link('Today’s journal', '/?journal=1');
    actions.append(open, journal);
    const savedFocus = host.contains(document.activeElement)
      ? document.activeElement.dataset.hqCaptureControl
      : '';
    host.replaceChildren(heading);
    if (!ref && !draft.text) {
      host.append(
        node(
          'p',
          notes?.status === 'missing'
            ? 'The notes folder is missing. Open My Notes to reconnect it.'
            : notes?.status === 'not_connected'
              ? 'Set up or connect a folder in My Notes.'
              : notes?.status === 'connected'
                ? 'This folder is read-only for Ori.'
                : 'Open My Notes to check your notebook connection.'
        )
      );
    } else {
      const form = node('form', '', 'ws-hq-capture-form');
      const label = node(
        'label',
        changed ? 'Draft from your previous notes connection' : 'Save to My Notes → Inbox'
      );
      label.htmlFor = 'hq-my-notes-draft';
      const input = node('input', '', 'form-control');
      input.id = label.htmlFor;
      input.dataset.hqCaptureControl = 'draft';
      input.value = draft.text;
      input.maxLength = 4000;
      input.placeholder = 'A thought to keep…';
      input.readOnly = draft.busy || changed;
      input.addEventListener('input', () => draft.edit(input.value, ref));
      const controls = node('div', '', 'ws-hq-capture-actions');
      const save = node(
        'button',
        changed ? 'Discard draft' : draft.busy ? 'Saving…' : 'Save to Inbox',
        'modern-btn modern-btn-primary modern-btn-sm'
      );
      save.type = changed ? 'button' : 'submit';
      save.dataset.hqCaptureControl = 'save';
      save.disabled = draft.busy;
      controls.append(save);
      form.append(label, input, controls);
      host.append(form);
      if (changed) {
        host.append(
          node('p', 'Copy this draft if you want to keep it. Ori won’t send it to another folder.')
        );
        save.addEventListener('click', () => {
          draft.edit('', '');
          this.render();
          this.focus();
        });
      }
      form.addEventListener('submit', async event => {
        event.preventDefault();
        if (changed || draft.busy) return;
        const pending = draft.save(ref);
        this.render();
        if (await pending) this.changed?.();
        this.render();
      });
    }
    if (draft.problem || draft.receipt) {
      const notice = node('p', draft.problem || draft.receipt);
      notice.setAttribute('role', draft.problem ? 'alert' : 'status');
      host.append(notice);
    }
    host.append(actions);
    if (savedFocus) {
      const target = [...host.querySelectorAll('[data-hq-capture-control]')].find(
        el => el.dataset.hqCaptureControl === savedFocus
      );
      (target && !target.disabled ? target : host.querySelector('input') || open).focus({
        preventScroll: true
      });
    }
  }
}
