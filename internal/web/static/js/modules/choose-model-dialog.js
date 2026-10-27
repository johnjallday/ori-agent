/*
 * choose-model-dialog.js — "Choose a model", for an assistant that was hired
 * without one.
 *
 * Today's banner offers it when the assistant is hired but Ori has no Default
 * model ("Hired — choose a model to chat."). It is the same picker the hire
 * preset uses, in a small dialog with Save and Cancel. Save is the Settings
 * save: POST /api/settings/system-model, then systemModel:changed so every
 * surface that shows the model (the drawer's chip, Today) reads it again.
 */
import { createDefaultModelPicker } from './default-model-picker.js';

export const CHOOSE_MODEL_TITLE = 'Choose a model';
export const CHOOSE_MODEL_LEAD =
  'This is the AI your assistant thinks with. It also becomes Ori’s default for new agents and AI features. You can change it any time in Settings → Default model.';
export const BANNER_TEXT = 'Hired — choose a model to chat.';
export const BANNER_BUTTON_LABEL = 'Choose a model';

const SAVE_ERROR = 'Ori could not save that model. Check the provider, then try again.';

// saveDefaultModel is the Settings save: the same request and the same
// reasoning rule (Codex only). It resolves { ok, model, error }.
export async function saveDefaultModel(selection, { fetchImpl = globalThis.fetch } = {}) {
  if (!selection || !selection.provider || !selection.model) {
    return { ok: false, model: null, error: 'Choose a provider and a model first.' };
  }
  const body = {
    provider: selection.provider,
    model: selection.model,
    reasoning_effort: selection.provider === 'codex' ? selection.reasoning_effort || 'medium' : ''
  };
  try {
    const response = await fetchImpl('/api/settings/system-model', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body)
    });
    if (!response.ok) return { ok: false, model: null, error: SAVE_ERROR };
    return { ok: true, model: body, error: '' };
  } catch (_) {
    return { ok: false, model: null, error: SAVE_ERROR };
  }
}

// openChooseModelDialog shows the dialog and resolves when it closes, with
// { saved, model }. It builds its own markup, so any page can call it.
export function openChooseModelDialog({
  documentImpl = globalThis.document,
  fetchImpl = globalThis.fetch,
  createPicker = createDefaultModelPicker,
  onSaved = () => {},
  emit = model => globalThis.EventBus?.emit?.('systemModel:changed', model)
} = {}) {
  const doc = documentImpl;
  const el = (tag, className = '') => {
    const node = doc.createElement(tag);
    if (className) node.className = className;
    return node;
  };

  return new Promise(resolve => {
    const overlay = el('div', 'choose-model-dialog');
    overlay.setAttribute('role', 'dialog');
    overlay.setAttribute('aria-modal', 'true');
    overlay.setAttribute('aria-labelledby', 'chooseModelTitle');
    const card = el('div', 'choose-model-dialog__card');
    const title = el('h2', 'choose-model-dialog__title');
    title.id = 'chooseModelTitle';
    title.textContent = CHOOSE_MODEL_TITLE;
    const lead = el('p', 'form-helper');
    lead.textContent = CHOOSE_MODEL_LEAD;
    const pickerHost = el('div');
    const error = el('div', 'alert alert-danger');
    error.setAttribute('role', 'alert');
    error.hidden = true;
    const actions = el('div', 'choose-model-dialog__actions');
    const cancel = el('button', 'modern-btn modern-btn-secondary');
    cancel.type = 'button';
    cancel.id = 'chooseModelCancel';
    cancel.textContent = 'Cancel';
    const save = el('button', 'modern-btn modern-btn-primary');
    save.type = 'button';
    save.id = 'chooseModelSave';
    save.textContent = 'Save';
    save.disabled = true;
    actions.appendChild(cancel);
    actions.appendChild(save);
    [title, lead, pickerHost, error, actions].forEach(node => card.appendChild(node));
    overlay.appendChild(card);
    doc.body.appendChild(overlay);

    const picker = createPicker({
      host: pickerHost,
      idPrefix: 'choose-model',
      fetchImpl,
      // Save is enabled only while the picker holds a usable selection.
      onChange: selection => {
        save.disabled = !selection;
      }
    });

    const onKey = event => {
      if (event.key === 'Escape') close({ saved: false, model: null });
    };
    doc.addEventListener('keydown', onKey);
    let closed = false;
    function close(result) {
      if (closed) return;
      closed = true;
      doc.removeEventListener('keydown', onKey);
      overlay.remove();
      resolve(result);
    }

    cancel.addEventListener('click', () => close({ saved: false, model: null }));
    save.addEventListener('click', async () => {
      save.disabled = true;
      error.hidden = true;
      const result = await saveDefaultModel(picker.selection(), { fetchImpl });
      if (!result.ok) {
        error.textContent = result.error;
        error.hidden = false;
        save.disabled = !picker.selection();
        return;
      }
      try {
        emit(result.model);
        await onSaved(result.model);
      } finally {
        close({ saved: true, model: result.model });
      }
    });
    // Focus lands on the first control once the picker has loaded, or on Cancel
    // when it cannot (no provider), so the keyboard is never left on the page.
    Promise.resolve(picker.ready).then(() => {
      const first = pickerHost.querySelector?.('select');
      (first && !first.disabled ? first : cancel).focus?.();
    });
  });
}
