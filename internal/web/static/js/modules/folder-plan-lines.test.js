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

test('entry choice uses native labelled radios in plan order and preserves focusable nodes', () => {
  const { shared, document } = load();
  const container = document.createElement('div');
  const chosen = [];
  const plan = { entry_name: 'Song.rpp', entry_candidates: ['Song.rpp', 'Alternate <img>.rpp'] };
  const options = { onChange: name => chosen.push(name) };
  shared.renderEntryChoice(container, plan, options, document);
  const fieldset = container.children[0];
  assert.equal(fieldset.tagName, 'FIELDSET');
  assert.equal(fieldset.children[0].tagName, 'LEGEND');
  assert.equal(fieldset.children[0].textContent, 'Which file is the song?');
  const first = fieldset.children[1].children[0];
  const second = fieldset.children[2].children[0];
  assert.equal(first.type, 'radio');
  assert.equal(first.name, second.name);
  assert.equal(first.checked, true);
  assert.equal(second.checked, false);
  assert.equal(fieldset.children[2].children[1].textContent, 'Alternate <img>.rpp');
  second.checked = true;
  second.onchange();
  assert.deepEqual(chosen, ['Alternate <img>.rpp']);
  shared.renderEntryChoice(container, plan, { ...options, disabled: true }, document);
  assert.equal(first.checked, false);
  assert.equal(second.checked, true, 'the busy render must not undo the native click');
  shared.renderEntryChoice(
    container,
    { ...plan, entry_name: 'Alternate <img>.rpp' },
    options,
    document
  );
  assert.equal(container.children[0], fieldset);
  assert.equal(first.checked, false);
  assert.equal(second.checked, true);
  shared.renderEntryChoice(container, plan, { ...options, disabled: true }, document);
  assert.equal(fieldset.disabled, true);
  assert.equal(first.checked, true, 'an old pending choice must not override a canonical reply');
  second.checked = true;
  second.onchange();
  assert.equal(chosen.length, 1);
});

test('single-file and saved-run disclosures name the file without another chooser', () => {
  const { shared, document } = load();
  const container = document.createElement('div');
  shared.renderEntryChoice(container, { entry_name: 'Song.rpp' }, {}, document);
  assert.equal(container.children[0].tagName, 'P');
  assert.equal(container.textContent, 'Opens Song.rpp');
  shared.renderEntryChoice(
    container,
    { entry_name: 'Song.rpp', entry_candidates: ['Song.rpp', 'Alt.rpp'] },
    { readOnly: true },
    document
  );
  assert.equal(container.children[0].tagName, 'P');
  shared.renderEntryChoice(
    container,
    { entry_name: 'Gone.rpp', entry_candidates: ['Remaining.rpp'], stop_reason: 'needs_choice' },
    { readOnly: true },
    document
  );
  assert.equal(container.textContent, 'Previously chose Gone.rpp');
  shared.renderEntryChoice(container, {}, {}, document);
  assert.equal(container.hidden, true);
});

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

// A single song's plan, as the host words it (FR5), in the browser's shape.
function handoffLines(shared, { homeLines = true, handoff = {} } = {}) {
  return shared.setupLinesView([
    { kind: 'integration', name: 'Uses the installed reviewed REAPER integration' },
    { kind: 'provider', name: 'Uses the installed reviewed Music Project Management plugin' },
    ...(homeLines
      ? [
          { kind: 'home', name: 'Creates your Music Production Home' },
          { kind: 'agents', name: 'Adds the agents the Home requires' }
        ]
      : []),
    { kind: 'library', name: 'Lists Album-5 in the Home’s library' },
    {
      kind: 'handoff',
      name: 'Your Portfolio Manager then opens Album-5',
      detail: 'Right after the Home is ready, under this one Set up.',
      ...handoff
    },
    { kind: 'workspace', name: 'Creates a Reaper Song workspace named Album-5' },
    { kind: 'folder', name: 'Linked where it is; nothing is moved, copied or run' },
    { kind: 'mode', name: 'Uses File-only mode' },
    { kind: 'agents', name: 'Adds the agents this blueprint requires' },
    { kind: 'task', name: 'An optional read-only summary' }
  ]);
}

test('a single song’s plan reads as the Home’s half, then its Portfolio Manager’s', () => {
  const { shared, document } = load();
  const names = { home: 'Music Production Home', song: 'Album-5' };
  const list = document.createElement('ul');
  shared.renderSetupLines(list, handoffLines(shared), document, { sections: names });
  assert.equal(list.dataset.sections, 'handoff');
  const rows = list.children.map(item => [item.dataset.section || '', item.dataset.kind]);
  assert.deepEqual(rows.slice(0, 6), [
    ['home', 'section'],
    ['', 'integration'],
    ['', 'provider'],
    ['', 'home'],
    ['', 'agents'],
    ['', 'library']
  ]);
  assert.deepEqual(rows[6], ['handoff', 'handoff']);
  assert.equal(list.children.length, 12, 'one heading row, eleven plan lines');
  const heading = list.children[0].children[0];
  assert.equal(heading.textContent, 'Your assistant sets up Music Production Home');
  assert.equal(heading.getAttribute('role'), 'heading');
  // The handoff line heads the second half and is still a plan line.
  const handoff = list.children[6];
  assert.equal(handoff.className, 'pa-folder__plan-section');
  const title = handoff.children[1].children[0];
  assert.equal(title.getAttribute('role'), 'heading');
  assert.equal(title.textContent, 'Your Portfolio Manager then opens Album-5');

  // OQ1: nothing to create for the Home, so the assistant adds the song to it.
  shared.renderSetupLines(list, handoffLines(shared, { homeLines: false }), document, {
    sections: names
  });
  assert.equal(
    list.children[0].textContent,
    'Your assistant adds Album-5 to Music Production Home'
  );
  assert.equal(list.children.length, 10);

  // A plan that hands nothing over is drawn exactly as before.
  const plain = shared.setupLinesView([{ kind: 'workspace', name: 'Creates a workspace' }]);
  shared.renderSetupLines(list, plain, document, { sections: names });
  assert.equal(list.dataset.sections, undefined);
  assert.deepEqual(
    list.children.map(item => item.dataset.kind),
    ['workspace']
  );
});

test('the handoff line shows each state of the Portfolio Manager’s open', () => {
  const { shared, document } = load();
  const names = { home: 'Music Production Home', song: 'Album-5' };
  const states = [
    ['waiting', 'Your Portfolio Manager then opens Album-5', '○', 'Waiting'],
    ['working', 'Portfolio Manager is opening Album-5…', '◔', 'In progress'],
    ['done', 'Portfolio Manager opened Album-5', '✓', 'Done'],
    ['failed', 'Your Portfolio Manager then opens Album-5', '!', 'Did not finish']
  ];
  // The run keeps the plan's words; the card says what the state means (FR19).
  for (const [state, name, glyph, word] of states) {
    const list = document.createElement('ul');
    shared.renderSetupLines(
      list,
      handoffLines(shared, {
        handoff: { state, name: 'Your Portfolio Manager then opens Album-5', detail: '' }
      }),
      document,
      { sections: names }
    );
    const handoff = list.children.find(item => item.dataset.section === 'handoff');
    assert.equal(handoff.dataset.state, state, state);
    assert.equal(handoff.textContent, `${glyph}${name} — ${word}`, state);
  }
  // Words the card does not recognize, or another song's, are shown as they are.
  assert.equal(
    shared.handoffLineName({ name: 'Opens it later', state: 'done' }, 'Album-5'),
    'Opens it later'
  );
  assert.equal(
    shared.handoffLineName(
      { name: 'Your Music Portfolio Manager then opens Album-6', state: 'done' },
      'Album-5'
    ),
    'Your Music Portfolio Manager then opens Album-6'
  );
  assert.equal(
    shared.handoffLineName(
      { name: 'Your Music Portfolio Manager then opens Album-5', state: 'working' },
      'Album-5'
    ),
    'Music Portfolio Manager is opening Album-5…'
  );
});

test('handoffNames reads the Home and the song from a plan, a run or a build plan', () => {
  const { shared } = load();
  const plain = value => JSON.parse(JSON.stringify(value));
  const visual = { entity: { kind: 'workspace', name: 'Album-5' } };
  assert.deepEqual(
    plain(
      shared.handoffNames({ destination: { status: 'new', name: 'Music Production Home' }, visual })
    ),
    { home: 'Music Production Home', song: 'Album-5' }
  );
  // The build pane's plan names its Home under home.
  assert.deepEqual(
    plain(
      shared.handoffNames({ home: { name: 'Music Production Home', status: 'existing' }, visual })
    ),
    { home: 'Music Production Home', song: 'Album-5' }
  );
  assert.deepEqual(plain(shared.handoffNames(null)), { home: '', song: '' });
  // Without names the headings still read plainly.
  assert.equal(
    shared.handoffSections(handoffLines(shared), {}).heading,
    'Your assistant sets up your Home'
  );
  // Home first: the picture is the Home, so the song is the handoff line's.
  const suggested = {
    destination: { status: 'new', name: 'Music Production Home' },
    visual: { entity: { kind: 'home', name: 'Music Production Home' } },
    lines: handoffLines(shared, {
      handoff: { name: 'Your Portfolio Manager then suggests opening Album-5' }
    })
  };
  assert.deepEqual(plain(shared.handoffNames(suggested)), {
    home: 'Music Production Home',
    song: 'Album-5'
  });
  assert.deepEqual(plain(shared.handoffLineParts(suggested.lines)), {
    manager: 'Your Portfolio Manager',
    song: 'Album-5'
  });
  assert.equal(shared.handoffLineParts([{ kind: 'workspace', name: 'Creates' }]), null);
});

test('a suggested open names its states, and each card draws its own half', () => {
  const { shared, document } = load();
  const names = { home: 'Music Production Home', song: 'Album-5' };
  const suggests = 'Your Portfolio Manager then suggests opening Album-5';
  assert.equal(
    shared.handoffLineName({ name: suggests, state: 'working' }, 'Album-5'),
    'Portfolio Manager is opening Album-5…'
  );
  assert.equal(
    shared.handoffLineName({ name: suggests, state: 'done' }, 'Album-5'),
    'Portfolio Manager opened Album-5'
  );
  const lines = handoffLines(shared, { handoff: { name: suggests } });
  const list = document.createElement('ul');
  // The assistant's card: the Home's half, closed by the manager's suggestion.
  shared.renderSetupLines(list, lines, document, { sections: names, half: 'home' });
  assert.equal(list.dataset.sections, 'handoff');
  assert.deepEqual(
    list.children.map(item => item.dataset.kind),
    ['section', 'integration', 'provider', 'home', 'agents', 'library', 'handoff']
  );
  // The Home's card: only what opening the song does, with no headings.
  shared.renderSetupLines(list, lines, document, { sections: names, half: 'song' });
  assert.equal(list.dataset.sections, undefined);
  assert.deepEqual(
    list.children.map(item => item.dataset.kind),
    ['workspace', 'folder', 'mode', 'agents', 'task']
  );
});
