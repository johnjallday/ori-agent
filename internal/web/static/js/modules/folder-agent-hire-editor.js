// A local-only editor for an uncreated, host-owned setup role. Persistence is
// the parent's plan-review callback, never the agent create/PATCH endpoints.
let sequence = 0;

export function hireModelProviders(models) {
  const groups = new Map();
  (models || []).forEach(model => {
    if (!groups.has(model.provider))
      groups.set(model.provider, { name: model.provider, models: [] });
    groups.get(model.provider).models.push({
      value: model.model,
      label: model.label || model.model,
      provider: model.provider
    });
  });
  return [...groups.values()];
}

export function renderHireDraftEditor(doc, { entry, models, taken, onSave, onCancel }) {
  const win = doc.defaultView || globalThis.window;
  const make = (tag, className, text = '') => {
    const node = doc.createElement(tag);
    node.className = className;
    node.textContent = text;
    return node;
  };
  const prefix = `folderHireDraft${++sequence}`;
  const element = make('form', 'folder-agent-card__hire-editor');
  element.setAttribute('aria-label', `Edit planned agent ${entry.name}`);
  element.append(make('h5', '', 'Edit agent'));
  const fields = make('div', '');
  const face = make('div', '');
  const error = make('p', 'folder-agent-card__error');
  error.setAttribute('role', 'alert');
  error.hidden = true;
  element.append(
    make(
      'p',
      'folder-agent-card__draft-note',
      'Pending setup only. The required role, instructions, skills and folder access stay unchanged.'
    ),
    fields,
    face
  );
  const form = win.AgentCreateForm.mount(fields, {
    idPrefix: `${prefix}Form`,
    profile: 'template',
    providers: hireModelProviders(models),
    omitFields: ['systemPrompt', 'reasoningEffort'],
    values: { name: entry.name, provider: entry.provider, model: entry.modelID }
  });
  const name = form.get('name');
  if (name) name.maxLength = 60;
  const nameHelp = fields.querySelector('[data-agent-create-id="NameHelp"]');
  if (nameHelp)
    nameHelp.textContent =
      'Use 1–60 characters: letters, numbers, spaces, underscores, and hyphens.';
  const appearance = win.AgentAppearanceEditor.create({
    host: face,
    idPrefix: `${prefix}Face`,
    mode: 'create',
    agent: { name: entry.name, source: 'user' },
    appearance: entry.character
      ? { mode: 'character', character: { catalog_id: entry.character } }
      : { mode: 'generated' },
    allowedModes: ['generated', 'character'],
    allowGeneratedColor: false,
    takenCharacterIds: () => taken || []
  });
  const updateName = () => appearance?.setAgentName(name.value);
  name?.addEventListener('input', updateName);
  const pickerFocus = event => {
    if (event.target?.closest?.('#charPickerHost')) event.stopImmediatePropagation();
  };
  doc.addEventListener?.('focusin', pickerFocus, true);
  const actions = make('div', 'folder-agent-card__actions');
  const save = make('button', 'btn btn-primary btn-sm', 'Save draft');
  save.type = 'submit';
  const cancel = make('button', 'btn btn-sm folder-agent-card__secondary', 'Cancel');
  cancel.type = 'button';
  cancel.addEventListener('click', () => onCancel());
  actions.append(save, cancel);
  element.append(error, actions);
  let saving = false;
  let destroyed = false;
  element.addEventListener('keydown', event => {
    if (event.key !== 'Escape' || saving) return;
    event.preventDefault();
    event.stopPropagation();
    onCancel();
  });
  element.addEventListener('submit', async event => {
    event.preventDefault();
    if (saving || destroyed) return;
    const result = form.extract();
    if (!result.valid) return form.focus('name');
    if ([...result.values.name].length > 60) {
      form.setError('name', 'Use 60 characters or fewer.');
      return form.focus('name');
    }
    saving = save.disabled = cancel.disabled = true;
    error.hidden = true;
    try {
      const choice = appearance?.appearance();
      await onSave(
        {
          name: result.values.name,
          provider: result.values.provider || '',
          model: result.values.model || ''
        },
        choice?.mode === 'character' ? choice.character?.catalog_id || '' : ''
      );
    } catch (failure) {
      error.textContent = failure?.message || 'The draft could not be reviewed. Try again.';
      error.hidden = false;
    } finally {
      if (!destroyed) saving = save.disabled = cancel.disabled = false;
    }
  });
  return {
    element,
    focus: () => form.focus('name'),
    destroy() {
      destroyed = true;
      name?.removeEventListener('input', updateName);
      doc.removeEventListener?.('focusin', pickerFocus, true);
      appearance?.destroy();
    }
  };
}
