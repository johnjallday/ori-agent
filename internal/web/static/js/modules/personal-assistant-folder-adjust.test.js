import test from 'node:test';
import assert from 'node:assert/strict';
import {
  adjustModel,
  adjustProblem,
  adjustmentFrom,
  createAdjustSheet,
  groupTypeChoices
} from './personal-assistant-folder-adjust.js';

const structure = (group = {}) => ({
  version: 1,
  folder: 'Albums',
  group: { name: 'Albums', type: 'group', destination_state: 'new', ...group },
  members: [
    { candidate_id: 'candidate-1', name: 'Album-1', folder: 'Album-1', included: true },
    { candidate_id: 'candidate-2', name: 'Album-2', folder: 'Album-2', included: true },
    {
      candidate_id: 'candidate-3',
      name: 'Logic Sketch',
      folder: 'Logic Sketch',
      type_label: 'Plain workspace',
      included: true
    }
  ]
});

test('the sheet edits a copy and sends members only by the ids the proposal gave', () => {
  const source = structure();
  const model = adjustModel(source);
  model.plainName = '  Albums 2026 ';
  model.members[2].included = false;
  model.members[0].name = 'First Record';
  assert.deepEqual(adjustmentFrom(model), {
    group_type: 'group',
    group_name: 'Albums 2026',
    group_name_set: true,
    members: [
      { candidate_id: 'candidate-1', included: true, name: 'First Record', name_set: true },
      { candidate_id: 'candidate-2', included: true, name: 'Album-2', name_set: true },
      { candidate_id: 'candidate-3', included: false, name: 'Logic Sketch', name_set: true }
    ]
  });
  // The record the card was drawn from is untouched.
  assert.equal(source.members[0].name, 'Album-1');
  assert.equal(source.group.name, 'Albums');
});

test('Done is stopped by one sentence: no folder, no name, or one name twice', () => {
  const model = adjustModel(structure());
  assert.equal(adjustProblem(model), '');
  model.members[0].name = 'album-2';
  assert.equal(adjustProblem(model), 'Two workspaces would have the name Album-2.');
  model.members[0].name = ' ';
  assert.equal(adjustProblem(model), 'Give the workspace for Album-1 a name.');
  // An unchecked member's name does not matter.
  model.members[0].included = false;
  assert.equal(adjustProblem(model), '');
  model.members.forEach(member => (member.included = false));
  assert.equal(adjustProblem(model), 'Choose at least one folder.');
  model.plainName = '';
  assert.equal(adjustProblem(model), 'Give the group a name.');
  // Listing in the Home makes no workspace, so no member is needed.
  model.groupType = 'library';
  assert.equal(adjustProblem(model), '');
  assert.equal(adjustmentFrom(model).group_name, '');
  assert.equal(adjustmentFrom(model).group_name_set, true);
});

test('group types are offered only when the folder has a Home, and only when it can be used', () => {
  assert.deepEqual(groupTypeChoices(adjustModel(structure())), []);
  const unavailable = groupTypeChoices(adjustModel(structure({ home_name: 'Music Home' })));
  assert.deepEqual(
    unavailable.map(choice => [choice.value, choice.disabled]),
    [
      ['group', false],
      ['library', true]
    ]
  );
  assert.equal(unavailable[1].hint, 'Music Home is not available right now.');
  const available = groupTypeChoices(
    adjustModel(structure({ home_name: 'Music Home', home_available: true }))
  );
  assert.equal(
    available.some(choice => choice.disabled),
    false
  );
  assert.equal(available[1].label, 'Add to Music Home instead');
  assert.match(available[1].hint, /first time you open it there/);
  // A Home whose provider is not in place is listed, unavailable, with why.
  for (const [reason, hint] of [
    ['provider_not_installed', 'Music Home is not installed.'],
    ['provider_unverified', 'Music Home could not be verified right now.']
  ]) {
    const blocked = groupTypeChoices(
      adjustModel(
        structure({ home_name: 'Music Home', home_available: true, availability: reason })
      )
    );
    assert.equal(blocked[1].label, 'Add to Music Home instead');
    assert.equal(blocked[1].disabled, true);
    assert.equal(blocked[1].hint, hint);
    // Home's own card walks through installing it, so there it can be chosen.
    const guided = groupTypeChoices(
      adjustModel(
        structure({ home_name: 'Music Home', home_available: true, availability: reason })
      ),
      { guidedHome: true }
    );
    assert.equal(guided[1].disabled, false);
    assert.equal(guided[1].hint, `${hint} Choosing it walks you through setting it up.`);
  }
  // A listing that is the current choice starts selected, with the plain name
  // falling back to the folder's.
  const listed = adjustModel(
    structure({
      name: 'Music Home',
      type: 'library',
      home_name: 'Music Home',
      home_available: true
    })
  );
  assert.equal(listed.groupType, 'library');
  assert.equal(listed.plainName, 'Albums');
});

// A minimal document: enough to build the sheet and press its controls.
function fakeDocument() {
  const make = tag => {
    const node = {
      tag,
      children: [],
      dataset: {},
      attrs: {},
      listeners: {},
      textContent: '',
      value: '',
      hidden: false,
      disabled: false,
      checked: false,
      append(...children) {
        this.children.push(...children);
      },
      setAttribute(key, value) {
        this.attrs[key] = value;
      },
      addEventListener(type, listener) {
        this.listeners[type] = listener;
      },
      focus() {
        node.focused = true;
      }
    };
    return node;
  };
  return { createElement: make };
}

const walk = (node, found = []) => {
  found.push(node);
  for (const child of node.children || []) walk(child, found);
  return found;
};

test('the sheet reports a problem instead of sending, then sends the adjustment once fixed', () => {
  const sent = [];
  let cancelled = 0;
  let single = 0;
  const sheet = createAdjustSheet({
    structure: structure({ home_name: 'Music Home' }),
    onDone: adjustment => sent.push(adjustment),
    onCancel: () => cancelled++,
    onSingle: () => single++,
    doc: fakeDocument()
  });
  const nodes = walk(sheet.element);
  const boxes = nodes.filter(node => node.type === 'checkbox');
  const names = nodes.filter(node => node.type === 'text');
  const buttons = nodes.filter(node => node.tag === 'button');
  const error = nodes.find(node => node.className === 'personal-assistant-folder-adjust__error');
  assert.equal(boxes.length, 3);
  assert.equal(names[0].value, 'Albums'); // the group name
  assert.deepEqual(
    buttons.map(button => button.textContent),
    ['Done', 'Set up one folder instead…', 'Cancel']
  );
  // Uncheck everything: Done says why and sends nothing.
  for (const box of boxes) {
    box.checked = false;
    box.listeners.change();
  }
  buttons[0].listeners.click();
  assert.equal(error.textContent, 'Choose at least one folder.');
  assert.equal(sent.length, 0);
  // Check two, rename the group, and send.
  for (const box of boxes.slice(0, 2)) {
    box.checked = true;
    box.listeners.change();
  }
  names[0].value = 'Albums 2026';
  names[0].listeners.input();
  buttons[0].listeners.click();
  assert.equal(error.textContent, '');
  assert.equal(sent.length, 1);
  assert.equal(sent[0].group_name, 'Albums 2026');
  assert.deepEqual(
    sent[0].members.map(member => member.included),
    [true, true, false]
  );
  buttons[1].listeners.click();
  buttons[2].listeners.click();
  assert.deepEqual([single, cancelled], [1, 1]);
  // A server refusal is shown in the same place.
  sheet.setError('Music Home cannot be used right now. Nothing was saved.');
  assert.match(error.textContent, /Nothing was saved/);
  // An unavailable Home cannot be selected.
  const radios = nodes.filter(node => node.type === 'radio');
  assert.deepEqual(
    radios.map(radio => [radio.value, radio.disabled, radio.checked]),
    [
      ['group', false, true],
      ['library', true, false]
    ]
  );
});

test('library reopening preserves explicit plain-group edits and deliberate empty fields', () => {
  const source = structure({
    type: 'library',
    name: 'Renamed Home',
    home_name: 'Renamed Home',
    home_available: true
  });
  source.edits = {
    group_type: 'library',
    group_name: '',
    group_name_set: true,
    members: [
      { candidate_id: source.members[0].candidate_id, name: '', name_set: true, included: true }
    ]
  };
  const model = adjustModel(source);
  assert.equal(model.plainName, '');
  assert.equal(model.members[0].name, '');
  assert.equal(adjustProblem(model), '');
  const sent = adjustmentFrom(model);
  assert.equal(sent.group_name, '');
  assert.equal(sent.members[0].name_set, true);
  model.groupType = 'group';
  assert.equal(adjustProblem(model), 'Give the group a name.');
  model.plainName = 'Records';
  assert.match(adjustProblem(model), /Give the workspace/);
});
