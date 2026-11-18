import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

// This page still owns its inline controller; test its actual production source.
const template = readFileSync(
  new URL('../../../templates/pages/models.tmpl', import.meta.url),
  'utf8'
);
const source = template.match(/<script>\s*([\s\S]*?)<\/script>/)[1];

function harness() {
  const nodes = new Map();
  const documentEvents = new Map();
  let sequence = 0;
  const document = {
    addEventListener: (name, callback) => documentEvents.set(name, callback),
    querySelectorAll: () => [],
    querySelector: () => null,
    getElementById: id => node(id),
    createElement: tag =>
      Object.assign(node(`generated-${++sequence}`), { tagName: tag.toUpperCase() })
  };
  function node(id) {
    if (nodes.has(id)) return nodes.get(id);
    const events = new Map();
    const attributes = new Map();
    const classes = new Set();
    const element = {
      children: [],
      value: '',
      dataset: {},
      style: { setProperty: (key, value) => attributes.set(`style:${key}`, value) },
      classList: {
        add: name => classes.add(name),
        remove: name => classes.delete(name),
        contains: name => classes.has(name),
        toggle: (name, force = !classes.has(name)) => {
          if (force) classes.add(name);
          else classes.delete(name);
        }
      },
      setAttribute: (name, value) => attributes.set(name, value),
      getAttribute: name => attributes.get(name),
      removeAttribute: name => attributes.delete(name),
      addEventListener: (name, callback) => events.set(name, callback),
      dispatch: name =>
        events.get(name)({
          currentTarget: element,
          target: element,
          preventDefault: () => {
            element.defaultPrevented = true;
          }
        }),
      reset: () => {
        element.resetCalls = (element.resetCalls || 0) + 1;
      },
      appendChild: child => element.children.push(child),
      contains: child => element.children.includes(child),
      focus: () => {
        document.activeElement = element;
      },
      querySelectorAll: () => [],
      closest: () => null
    };
    nodes.set(id, element);
    return element;
  }
  const sandbox = { document, console };
  vm.createContext(sandbox);
  vm.runInContext(source, sandbox);
  const loadData = sandbox.loadData;
  sandbox.loadData = () => {};
  return { sandbox, node, documentEvents, loadData };
}

test('native Add Category passes no category ID, not its pointer event', () => {
  const { sandbox, node, documentEvents } = harness();
  const calls = [];
  sandbox.openCategoryEditor = (...args) => calls.push(args);
  documentEvents.get('DOMContentLoaded')();
  node('addCategoryBtn').dispatch('click');
  assert.equal(calls.length, 1);
  assert.equal(calls[0][0], null);
  assert.equal(calls[0][1], node('addCategoryBtn'));
});

test('editor reset clears native interaction state and submit never navigates', () => {
  const { sandbox, node, documentEvents } = harness();
  sandbox.showModelsModal = () => {};
  const input = node('categoryName');
  input.value = 'Unsaved prior draft';
  input.classList.add('is-invalid');
  sandbox.openCategoryEditor(null, node('addCategoryBtn'));
  assert.equal(node('categoryEditorForm').resetCalls, 1);
  assert.equal(input.value, '');
  assert.equal(input.classList.contains('is-invalid'), false);
  let saves = 0;
  sandbox.saveCategory = () => {
    saves++;
  };
  documentEvents.get('DOMContentLoaded')();
  node('categoryEditorForm').dispatch('submit');
  assert.equal(node('categoryEditorForm').defaultPrevented, true);
  assert.equal(saves, 1);
});

test('category colors are retained as swatches and labels stay literal', () => {
  const { sandbox } = harness();
  const markup = sandbox.categoryBadgeMarkup({
    color: '#ffffff',
    icon: 'book',
    name: '<Notes> & "quotes"'
  });
  assert.match(markup, /class="badge category-badge category-swatch-badge"/);
  assert.match(markup, /--category-color: #ffffff/);
  assert.match(markup, /&lt;Notes&gt; &amp; &quot;quotes&quot;/);
  assert.doesNotMatch(markup, /style="background:/);
  assert.equal(sandbox.modelsColor('red; background: url(example)'), '#3b82f6');
  assert.equal(sandbox.modelsIcon('" onclick="bad'), 'tag');
});

test('disclosures are heading buttons and hide descendants without fixed heights', () => {
  const { sandbox, node } = harness();
  const section = node('section');
  const header = sandbox.modelsSectionHeader(section, 'provider-header', 'Fictional provider', 1);
  assert.equal(header.tagName, 'BUTTON');
  assert.equal(header.type, 'button');
  assert.equal(section.children[0].tagName, 'H2');
  const list = node('list');
  const child = node('child');
  list.appendChild(child);
  child.focus();
  sandbox.setupAccordion(header, list);
  assert.equal(header.getAttribute('aria-controls'), list.id);
  header.dispatch('click');
  assert.equal(list.hidden, true);
  assert.equal(header.getAttribute('aria-expanded'), 'false');
  assert.equal(sandbox.document.activeElement, header);
  header.dispatch('click');
  assert.equal(list.hidden, false);
  assert.equal(header.getAttribute('aria-expanded'), 'true');
  assert.equal(list.style.maxHeight, undefined);
});

test('color and icon choices use named pressed buttons', () => {
  const { sandbox, node } = harness();
  vm.runInContext(
    "predefinedColors = ['#ffffff']; selectedColor = '#ffffff'; predefinedIcons = ['book']; selectedIcon = 'book';",
    sandbox
  );
  sandbox.renderColorPicker();
  sandbox.renderIconPicker();
  assert.match(node('colorPicker').innerHTML, /<button type="button"/);
  assert.match(
    node('colorPicker').innerHTML,
    /aria-label="Choose color #ffffff" aria-pressed="true"/
  );
  assert.match(node('iconPicker').innerHTML, /<button type="button"/);
  assert.match(node('iconPicker').innerHTML, /aria-label="Choose icon book" aria-pressed="true"/);
});

test('assignment changes mirror native checked state rather than toggling it twice', () => {
  const { sandbox, node } = harness();
  const label = node('label');
  const checkbox = { checked: true };
  label.querySelector = () => checkbox;
  sandbox.toggleAssignment(label);
  assert.equal(checkbox.checked, true);
  assert.equal(label.classList.contains('selected'), true);
  checkbox.checked = false;
  sandbox.toggleAssignment(label);
  assert.equal(label.classList.contains('selected'), false);
});

test('correcting a required name clears stale feedback and escapes its preview', () => {
  const { sandbox, node } = harness();
  const input = node('categoryName');
  input.value = '<Fictional name>';
  input.classList.add('is-invalid');
  input.setAttribute('aria-invalid', 'true');
  node('categoryNameError').textContent = 'Name is required';
  sandbox.updateCategoryName();
  assert.equal(input.classList.contains('is-invalid'), false);
  assert.equal(input.getAttribute('aria-invalid'), undefined);
  assert.equal(node('categoryNameError').textContent, '');
  assert.match(node('categoryPreview').innerHTML, /&lt;Fictional name&gt;/);
});

test('failed category reads are not empty success or stale analysis availability', async () => {
  const { sandbox, node, loadData } = harness();
  sandbox.console = { error() {} };
  sandbox.fetch = async path => ({
    ok: path !== '/api/model-categories',
    json: async () => ({ available: true, providers: [] })
  });
  assert.equal(await loadData(), false);
  assert.equal(node('manageCategoriesBtn').disabled, true);
  assert.equal(node('modelsContent').classList.contains('d-none'), true);
  assert.equal(node('errorMessage').textContent, 'Failed to fetch categories');
  assert.equal(node('autoCategorizeAllBtn').classList.contains('d-none'), true);
  assert.equal(vm.runInContext('autoCategorizeAvailable', sandbox), false);
});

test('retained selection state tolerates its absent launcher and clears hidden controls', () => {
  const { sandbox, node } = harness();
  const label = node('select-label');
  const checkbox = node('select-checkbox');
  const row = node('model-row');
  sandbox.document.getElementById = id => (id === 'toggleSelectionBtn' ? null : node(id));
  sandbox.document.querySelectorAll = selector => {
    if (selector === '.model-select-label') return [label];
    if (selector === '.model-select-checkbox') return [checkbox];
    if (selector === '.model-item.selected') return [row];
    return [];
  };
  checkbox.closest = () => row;
  sandbox.toggleSelectionMode();
  assert.equal(label.hidden, false);
  checkbox.checked = true;
  sandbox.toggleModelSelection('fictional', checkbox);
  assert.equal(node('selectedCount').textContent, '1 model selected');
  sandbox.cancelSelection();
  assert.equal(label.hidden, true);
  assert.equal(checkbox.checked, false);
  assert.equal(row.classList.contains('selected'), false);
  assert.equal(node('floatingActionBar').classList.contains('d-none'), true);
});

test('restored model focus retains provider identity for duplicate model IDs', () => {
  const { sandbox, node } = harness();
  const wanted = node('second-provider-button');
  const first = {
    dataset: { modelId: 'same', provider: 'first' },
    querySelector: () => node('wrong-button')
  };
  const second = { dataset: { modelId: 'same', provider: 'second' }, querySelector: () => wanted };
  sandbox.document.querySelectorAll = () => [first, second];
  const removed = { isConnected: false, closest: () => second };
  assert.equal(sandbox.modelsReturnControl(removed), wanted);
});

test('category deletion carries the actual invoker through confirmation and redraw', async () => {
  const { sandbox, node } = harness();
  const trigger = node('delete-trigger');
  sandbox.confirm = () => false;
  sandbox.fetch = () => assert.fail('cancel must not write');
  await sandbox.deleteCategory('fictional', trigger);
  assert.equal(sandbox.document.activeElement, trigger);
  sandbox.confirm = () => true;
  sandbox.fetch = async (path, options) => {
    assert.equal(path, '/api/model-categories/fictional');
    assert.equal(options.method, 'DELETE');
    return { ok: true };
  };
  sandbox.loadData = async () => true;
  let redrawTrigger;
  sandbox.renderCategoriesList = value => {
    redrawTrigger = value;
  };
  await sandbox.deleteCategory('fictional', trigger);
  assert.equal(redrawTrigger, trigger);
});

test('suggestion controls have names and literal labels without invoking analysis', () => {
  const { sandbox, node } = harness();
  vm.runInContext(
    `pendingSuggestions = ${JSON.stringify([{ model_id: '<fictional>', reasoning: '<not markup>', confidence: 0.8 }])};`,
    sandbox
  );
  sandbox.renderSuggestions();
  const markup = node('suggestionsTableBody').innerHTML;
  assert.match(markup, /aria-label="Apply suggestion for &lt;fictional&gt;"/);
  assert.match(markup, /aria-label="Suggested category for &lt;fictional&gt;"/);
  assert.match(markup, /models-checkbox-target/);
  assert.match(markup, /&lt;not markup&gt;/);
  assert.doesNotMatch(markup, /<not markup>/);
});

test('a rejected view-preference save restores the prior native choice', async () => {
  const { sandbox, node, documentEvents } = harness();
  const provider = node('viewProvider');
  const category = node('viewCategory');
  category.value = 'category';
  sandbox.document.querySelectorAll = () => [provider, category];
  let renders = 0;
  const notices = [];
  sandbox.applyFilters = () => {
    renders++;
  };
  sandbox.showToast = (...args) => notices.push(args);
  sandbox.fetch = async (path, options) => {
    assert.equal(path, '/api/model-categories/view-preference');
    assert.deepEqual(JSON.parse(options.body), { preference: 'category' });
    return { ok: false };
  };
  documentEvents.get('DOMContentLoaded')();
  await category.dispatch('change');
  assert.equal(vm.runInContext('viewPreference', sandbox), 'provider');
  assert.equal(provider.checked, true);
  assert.equal(provider.disabled, false);
  assert.equal(category.disabled, false);
  assert.equal(renders, 2);
  assert.deepEqual(notices, [['Failed to save view preference', 'danger']]);
});
