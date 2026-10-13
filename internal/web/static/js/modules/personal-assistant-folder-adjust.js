// The Adjust sheet for a proposed group: its name and type, and which folders
// get a workspace under which name. It is shared by every folder entry, so it
// sends nothing itself: the caller is handed the adjustment and decides where
// it goes. Every string shown comes from the host's structure record.
const text = value => String(value == null ? '' : value).trim();

// adjustModel is the sheet's editable copy of a structure record.
export function adjustModel(structure) {
  const group = structure?.group || {};
  return {
    folder: text(structure?.folder),
    groupName: text(group.name),
    // The plain group's own name, kept while another type is selected.
    plainName: group.type === 'group' ? text(group.name) : text(structure?.folder),
    groupType: group.type === 'library' ? 'library' : 'group',
    homeName: text(group.home_name),
    homeAvailable: group.home_available === true,
    members: (Array.isArray(structure?.members) ? structure.members : []).map(member => ({
      id: text(member.candidate_id),
      folder: text(member.folder) || text(member.name),
      name: text(member.name),
      type: text(member.type_label),
      included: member.included === true,
      existing: text(member.existing_workspace)
    }))
  };
}

// groupTypeChoices are the group types the sheet offers. A folder with no Home
// to offer has only the plain group, and the sheet then shows no choice at all.
// A Home is offered as a place to list the folders: it makes each project's
// workspace itself, when that project is opened there.
export function groupTypeChoices(model) {
  if (!model.homeName) return [];
  return [
    {
      value: 'group',
      label: 'A plain group, with a workspace for each folder',
      hint: '',
      disabled: false
    },
    {
      value: 'library',
      label: `List them in ${model.homeName}`,
      hint: model.homeAvailable
        ? 'A project gets its workspace the first time you open it there.'
        : `${model.homeName} is not available right now.`,
      disabled: !model.homeAvailable
    }
  ];
}

// adjustProblem is the one sentence that stops Done, or '' when the model can
// be sent. The server checks the same things again.
export function adjustProblem(model) {
  if (model.groupType === 'group' && !text(model.plainName)) return 'Give the group a name.';
  if (model.groupType === 'library') return '';
  const included = model.members.filter(member => member.included);
  if (!included.length) return 'Choose at least one folder.';
  const seen = new Set();
  for (const member of included) {
    const name = text(member.name);
    if (!name) return `Give the workspace for ${member.folder} a name.`;
    if (seen.has(name.toLowerCase())) return `Two workspaces would have the name ${name}.`;
    seen.add(name.toLowerCase());
  }
  return '';
}

// adjustmentFrom is the request the host's review takes: the group's name and
// type, and every member by the id the proposal gave it.
export function adjustmentFrom(model) {
  return {
    group_type: model.groupType,
    ...(model.groupType === 'group' ? { group_name: text(model.plainName) } : {}),
    members: model.members.map(member => ({
      candidate_id: member.id,
      included: member.included,
      name: text(member.name)
    }))
  };
}

const element = (doc, tag, className = '', content = '') => {
  const node = doc.createElement(tag);
  if (className) node.className = className;
  if (content) node.textContent = content;
  return node;
};

let sequence = 0;

// createAdjustSheet builds the sheet for one structure. onDone receives the
// adjustment; onSingle (optional) is the way to set up one folder instead of
// the group; onCancel closes it. The caller inserts `element` and removes it.
export function createAdjustSheet({ structure, onDone, onCancel, onSingle, doc = document }) {
  const model = adjustModel(structure);
  const id = `personalAssistantFolderAdjust${++sequence}`;
  const sheet = element(doc, 'section', 'personal-assistant-folder-adjust');
  sheet.dataset.folderAdjust = 'true';
  sheet.setAttribute('role', 'group');
  sheet.setAttribute('aria-label', 'Adjust the proposed group');

  const nameLabel = element(doc, 'label', 'personal-assistant-folder-adjust__label', 'Group name');
  nameLabel.setAttribute('for', `${id}Name`);
  const nameInput = element(doc, 'input', 'form-control form-control-sm');
  nameInput.id = `${id}Name`;
  nameInput.type = 'text';
  nameInput.maxLength = 96;
  nameInput.addEventListener('input', () => {
    if (model.groupType === 'group') model.plainName = nameInput.value;
  });
  sheet.append(nameLabel, nameInput);

  const error = element(doc, 'p', 'personal-assistant-folder-adjust__error');
  error.setAttribute('role', 'alert');

  const members = element(doc, 'fieldset');
  members.append(element(doc, 'legend', '', 'Workspaces'));
  const syncGroup = () => {
    const plain = model.groupType === 'group';
    nameInput.value = plain ? model.plainName : model.homeName;
    nameInput.disabled = !plain;
    members.hidden = model.groupType === 'library';
  };

  const choices = groupTypeChoices(model);
  if (choices.length) {
    const types = element(doc, 'fieldset');
    types.append(element(doc, 'legend', '', 'Group type'));
    for (const choice of choices) {
      const row = element(doc, 'label', 'personal-assistant-folder-adjust__member');
      const radio = element(doc, 'input');
      radio.type = 'radio';
      radio.name = `${id}Type`;
      radio.value = choice.value;
      radio.checked = model.groupType === choice.value;
      radio.disabled = choice.disabled;
      radio.addEventListener('change', () => {
        if (!radio.checked) return;
        model.groupType = choice.value;
        syncGroup();
      });
      row.append(radio, element(doc, 'span', '', choice.label));
      if (choice.hint)
        row.append(element(doc, 'small', 'personal-assistant-folder-adjust__hint', choice.hint));
      types.append(row);
    }
    sheet.append(types);
  }

  for (const member of model.members) {
    const row = element(doc, 'div', 'personal-assistant-folder-adjust__member');
    const box = element(doc, 'input');
    box.type = 'checkbox';
    box.checked = member.included;
    box.setAttribute('aria-label', `Make a workspace for ${member.folder}`);
    const name = element(doc, 'input', 'form-control form-control-sm');
    name.type = 'text';
    name.maxLength = 96;
    name.value = member.name;
    name.disabled = !member.included;
    name.setAttribute('aria-label', `Workspace name for ${member.folder}`);
    box.addEventListener('change', () => {
      member.included = box.checked;
      name.disabled = !box.checked;
    });
    name.addEventListener('input', () => {
      member.name = name.value;
    });
    const detail = [member.type, member.folder !== text(member.name) ? member.folder : '']
      .concat(member.existing ? [`already a workspace: ${member.existing}`] : [])
      .filter(Boolean)
      .join(' · ');
    row.append(box, name, element(doc, 'small', '', detail));
    members.append(row);
  }
  sheet.append(members, error);

  const actions = element(doc, 'div', 'personal-assistant-structure__actions');
  const done = element(doc, 'button', 'btn btn-sm btn-primary', 'Done');
  done.type = 'button';
  done.addEventListener('click', () => {
    const problem = adjustProblem(model);
    error.textContent = problem;
    if (!problem) onDone?.(adjustmentFrom(model));
  });
  actions.append(done);
  if (onSingle) {
    const single = element(doc, 'button', 'btn btn-sm btn-outline-secondary');
    single.type = 'button';
    single.textContent = 'Set up one folder instead…';
    single.addEventListener('click', () => onSingle());
    actions.append(single);
  }
  const cancel = element(doc, 'button', 'btn btn-sm btn-link', 'Cancel');
  cancel.type = 'button';
  cancel.addEventListener('click', () => onCancel?.());
  actions.append(cancel);
  sheet.append(actions);
  sheet.addEventListener('keydown', event => {
    if (event.key !== 'Escape') return;
    event.preventDefault();
    event.stopPropagation();
    onCancel?.();
  });
  syncGroup();
  return {
    element: sheet,
    focus: () => (nameInput.disabled ? done : nameInput).focus({ preventScroll: true }),
    setError: message => {
      error.textContent = text(message);
    }
  };
}
