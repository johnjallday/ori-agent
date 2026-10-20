import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const source = readFileSync(new URL('./folder-plan-lines.js', import.meta.url), 'utf8');

// A small DOM: elements with children, text, classes, attributes and a dataset,
// enough for the renderer to draw a list into.
class FakeElement {
  constructor(tag) {
    this.tagName = String(tag).toUpperCase();
    this.children = [];
    this.attributes = new Map();
    this.dataset = {};
    this.className = '';
    this._text = '';
  }
  get textContent() {
    return this._text + this.children.map(child => child.textContent).join('');
  }
  set textContent(value) {
    this._text = String(value ?? '');
    this.children = [];
  }
  setAttribute(name, value) {
    this.attributes.set(name, String(value));
  }
  getAttribute(name) {
    return this.attributes.has(name) ? this.attributes.get(name) : null;
  }
  append(...nodes) {
    for (const node of nodes) {
      if (typeof node === 'string') {
        const textNode = new FakeElement('#text');
        textNode.textContent = node;
        this.children.push(textNode);
      } else this.children.push(node);
    }
  }
  replaceChildren() {
    this.textContent = '';
  }
}

function load() {
  const document = { createElement: tag => new FakeElement(tag) };
  const context = vm.createContext({ document });
  vm.runInContext(source, context, { filename: 'folder-plan-lines.js' });
  return { shared: context.OriFolderPlanLines, document };
}

test('setupLinesView keeps named lines as text and trusts only the known states', () => {
  const { shared } = load();
  // The view's objects come from the script's own realm: compared by value.
  const plain = value => JSON.parse(JSON.stringify(value));
  assert.deepEqual(
    plain(
      shared.setupLinesView([
        {
          kind: 'home',
          name: ' Creates Home “Music Production Home” ',
          detail: ' Later projects join it. '
        },
        { kind: 'folder', name: 'Links Album-2 where it is', state: 'done' },
        { kind: 'task', name: 'Queues a first task', state: 'exploded' },
        { kind: 'agents', name: '   ' },
        null,
        'not a line'
      ])
    ),
    [
      {
        kind: 'home',
        name: 'Creates Home “Music Production Home”',
        detail: 'Later projects join it.',
        state: ''
      },
      { kind: 'folder', name: 'Links Album-2 where it is', detail: '', state: 'done' },
      { kind: 'task', name: 'Queues a first task', detail: '', state: '' }
    ]
  );
});

test('renderSetupLines draws the state as a glyph and a word; kind icons only when asked', () => {
  const { shared, document } = load();
  const lines = shared.setupLinesView([
    {
      kind: 'home',
      name: 'Creates Home “Music Production Home”',
      detail: 'Later projects join it.'
    },
    { kind: 'folder', name: 'Links Album-2 where it is', state: 'working' },
    { kind: 'task', name: 'Queues a first task', state: 'done' },
    { kind: 'mystery', name: 'Does something new', state: 'failed' }
  ]);

  // Home's card: a glyph, the sentence, the detail, the state in words.
  const plain = document.createElement('ul');
  shared.renderSetupLines(plain, lines, document);
  assert.equal(plain.children.length, 4);
  assert.deepEqual(
    plain.children.map(item => [item.dataset.kind, item.dataset.state || '', item.textContent]),
    [
      ['home', '', '•Creates Home “Music Production Home”Later projects join it.'],
      ['folder', 'working', '◔Links Album-2 where it is — In progress'],
      ['task', 'done', '✓Queues a first task — Done'],
      ['mystery', 'failed', '!Does something new — Did not finish']
    ]
  );
  assert.deepEqual(
    plain.children[0].children.map(child => child.className),
    ['pa-folder__plan-icon', 'pa-folder__plan-text'],
    'no kind icon unless asked'
  );
  assert.equal(plain.children[0].children[0].getAttribute('aria-hidden'), 'true');

  // The build pane's checklist: the kind's icon leads each row, hidden from
  // assistive tech, and the words are unchanged.
  const iconed = document.createElement('ul');
  shared.renderSetupLines(iconed, lines, document, { kindIcons: true });
  assert.deepEqual(
    iconed.children.map(item => item.textContent),
    plain.children.map(item => item.textContent)
  );
  const kinds = iconed.children.map(item => item.children[0]);
  assert.deepEqual(
    kinds.map(kind => kind.className),
    ['pa-folder__plan-kind', 'pa-folder__plan-kind', 'pa-folder__plan-kind', 'pa-folder__plan-kind']
  );
  assert.deepEqual(
    kinds.map(kind => kind.getAttribute('aria-hidden')),
    ['true', 'true', 'true', 'true']
  );
  assert.deepEqual(
    kinds.map(kind => kind.children[0].className),
    ['bi bi-house-door', 'bi bi-folder-symlink', 'bi bi-clipboard-check', 'bi bi-dot'],
    'a kind the renderer does not know gets a plain dot'
  );

  // A second render replaces the first.
  shared.renderSetupLines(iconed, lines.slice(0, 1), document, { kindIcons: true });
  assert.equal(iconed.children.length, 1);
  shared.renderSetupLines(null, lines, document);
});

test('satisfiedLinesView checks off a plugin line whose need is ready, and a run’s own states win', () => {
  const { shared, document } = load();
  const plain = value => JSON.parse(JSON.stringify(value));
  const lines = shared.setupLinesView([
    { kind: 'integration', name: 'Uses the installed reviewed REAPER integration' },
    { kind: 'provider', name: 'Installs the Music Project Management plugin' },
    { kind: 'home', name: 'Creates Home “Music Production Home”' }
  ]);
  const needs = [
    { kind: 'integration', name: 'REAPER integration', state: 'ready' },
    { kind: 'provider', name: 'Music Project Management', state: 'install' }
  ];
  assert.deepEqual(
    plain(shared.satisfiedLinesView(lines, needs)).map(line => [line.kind, line.state]),
    [
      ['integration', 'already'],
      ['provider', ''],
      ['home', '']
    ]
  );
  // Nothing ready, nothing marked; the same lines come back.
  assert.equal(shared.satisfiedLinesView(lines, []), lines);
  assert.equal(shared.satisfiedLinesView(lines, undefined), lines);
  assert.equal(shared.satisfiedLinesView(lines, [{ kind: 'integration', state: 'enable' }]), lines);

  // During the run a waiting line is still "already"; working, done and failed
  // are the run's to say.
  const running = shared.setupLinesView([
    { kind: 'integration', name: 'REAPER', state: 'waiting' },
    { kind: 'provider', name: 'Music', state: 'working' },
    { kind: 'home', name: 'Home', state: 'done' }
  ]);
  const ready = [
    { kind: 'integration', state: 'ready' },
    { kind: 'provider', state: 'ready' },
    { kind: 'home', state: 'ready' }
  ];
  assert.deepEqual(
    plain(shared.satisfiedLinesView(running, ready)).map(line => line.state),
    ['already', 'working', 'done']
  );

  // Drawn: a filled check and "Already in place" for assistive tech.
  const list = document.createElement('ul');
  shared.renderSetupLines(list, shared.satisfiedLinesView(lines, needs), document, {
    kindIcons: true
  });
  assert.equal(list.children[0].dataset.state, 'already');
  assert.equal(
    list.children[0].textContent,
    '✓Uses the installed reviewed REAPER integration — Already in place'
  );
  assert.equal(list.children[1].dataset.state, undefined);
  assert.equal(list.children[1].textContent, '•Installs the Music Project Management plugin');
});

test('setupLineKindIcon names an icon for every kind the host plans with', () => {
  const { shared } = load();
  for (const kind of [
    'integration',
    'provider',
    'home',
    'group',
    'workspace',
    'workspaces',
    'member',
    'folder',
    'mode',
    'agents',
    'assistant',
    'task',
    'library',
    'songs',
    'profile',
    'unavailable',
    'arrangement'
  ]) {
    assert.match(shared.setupLineKindIcon(kind), /^bi-[a-z0-9-]+$/, kind);
    assert.notEqual(shared.setupLineKindIcon(kind), 'bi-dot', kind);
  }
  assert.equal(shared.setupLineKindIcon(''), 'bi-dot');
  assert.equal(shared.setupLineKindIcon(undefined), 'bi-dot');
});
