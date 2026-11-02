import { test } from 'node:test';
import assert from 'node:assert/strict';

import { evaluate, scanScript, scanTemplate } from './check-user-facing-task-copy.mjs';

const texts = hits => hits.map(hit => hit.text);

test('visible template text and user-facing attributes fail', () => {
  assert.deepEqual(texts(scanTemplate('<h2>Create task</h2>')), ['Create task']);
  assert.deepEqual(texts(scanTemplate('<button title="Run this task">Go</button>')), [
    'Run this task'
  ]);
  assert.deepEqual(texts(scanTemplate('<input placeholder="Describe the task">')), [
    'Describe the task'
  ]);
  assert.deepEqual(texts(scanTemplate('<a aria-label="Open tasks" href="/x"></a>')), [
    'Open tasks'
  ]);
  assert.deepEqual(texts(scanTemplate('<span>Tasks</span>')), ['Tasks']);
});

test('template identifiers, URLs, comments and actions pass', () => {
  const clean = [
    '<div id="taskList" class="task-card" data-task-id="1"></div>',
    '<a href="/workspaces/x/task/1">Open run</a>',
    '<!-- the task list -->',
    '{{ if .Task }}{{ .Task.Title }}{{ end }}',
    '<div data-role="task"></div>',
    '<style>.task { color: red; }</style>',
    '<script src="/static/js/modules/quick-task.js"></script>'
  ];
  for (const source of clean) assert.deepEqual(scanTemplate(source), [], source);
});

test('an inline script is read as a script', () => {
  const source = "<h1>Runs</h1>\n<script>\nel.textContent = 'Task saved';\n</script>";
  assert.deepEqual(scanTemplate(source), [{ line: 3, text: 'Task saved' }]);
});

test('display strings fail wherever they reach a person', () => {
  const copy = [
    ["el.textContent = 'Create task';", 'Create task'],
    ['showToast(`Task ${id} saved`);', 'Task saved'],
    ["const label = 'Tasks';", 'Tasks'],
    ['const html = \'<button class="task-btn">Delete task</button>\';', 'Delete task'],
    ["el.setAttribute('aria-label', 'Open task');", 'Open task'],
    ['throw new Error("That task is gone");', 'That task is gone']
  ];
  for (const [source, text] of copy) {
    const found = texts(scanScript(source)).map(line => line.replace(/ +/g, ' '));
    assert.deepEqual(found, [text], source);
  }
});

test('script identifiers, URLs, code and comments pass', () => {
  const clean = [
    "if (kind === 'task') go();",
    "const kind = 'task';",
    "fetch('/api/workspaces/x/tasks/1');",
    'fetch(`/api/orchestration/tasks?id=${id}`);',
    "document.querySelector('.task-card .title');",
    "document.getElementById('taskList');",
    "const event = new CustomEvent('task:updated');",
    "console.log('Task saved');",
    '// Create a task',
    '/* the task list */',
    "el.className = 'task-row is task';",
    "const key = 'task_id';",
    "const path = 'tasks/index.md';",
    "el.setAttribute('data-kind', 'a task');",
    'const html = \'<div class="task" data-task="1"></div>\';',
    'const re = /create task/i;',
    "el.dataset.kind = 'some task';",
    "x.includes('a task');"
  ];
  for (const source of clean) assert.deepEqual(scanScript(source), [], source);
});

test('copy inside a template hole is still found', () => {
  const source = "el.innerHTML = `<p>${done ? 'Task finished' : 'Waiting'}</p>`;";
  assert.deepEqual(texts(scanScript(source)), ['Task finished']);
});

const hits = new Map([['a.js', [{ line: 1, text: 'Create task' }]]]);

test('a hit outside the allowlist fails, with where it is', () => {
  assert.deepEqual(evaluate(hits, { entries: [] }, 0), ['a.js:1: Create task']);
  assert.deepEqual(evaluate(new Map(), { entries: [] }, 0), []);
});

test('an entry names a file and a reason', () => {
  assert.deepEqual(evaluate(hits, { entries: [{ file: 'a.js', reason: 'Why' }] }, 1), []);
  const [problem] = evaluate(hits, { entries: [{ file: 'a.js' }] }, 1);
  assert.match(problem, /needs a file and a reason/);
});

test('a new allowlist entry fails', () => {
  const problems = evaluate(hits, { entries: [{ file: 'a.js', reason: 'Why' }] }, 0);
  assert.equal(problems.length, 1);
  assert.match(problems[0], /at most 0/);
});

test('an entry that is no longer needed fails, so the list only shrinks', () => {
  const [problem] = evaluate(new Map(), { entries: [{ file: 'a.js', reason: 'Why' }] }, 1);
  assert.match(problem, /no longer says/);
});

test('query strings pass and "subtask" is copy', () => {
  assert.deepEqual(scanScript("const href = base + '?panel=tasks&task=' + id;"), []);
  assert.deepEqual(scanScript("const q = 'panel=tasks';"), []);
  assert.deepEqual(texts(scanScript("el.textContent = 'Add Subtask';")), ['Add Subtask']);
});

test('a template hit is reported on the line of its words', () => {
  assert.deepEqual(scanTemplate('<div>\n  <span>\n    Create task\n  </span>\n</div>'), [
    { line: 3, text: 'Create task' }
  ]);
});

test('an entry with texts allows only those lines', () => {
  const two = new Map([
    [
      'a.js',
      [
        { line: 1, text: 'You are a task runner' },
        { line: 9, text: 'Create task' }
      ]
    ]
  ]);
  const entry = { file: 'a.js', reason: 'Prompt', texts: ['You are a task'] };
  assert.deepEqual(evaluate(two, { entries: [entry] }, 1), ['a.js:9: Create task']);
  const [stale] = evaluate(two, { entries: [{ ...entry, texts: ['Gone'] }] }, 1);
  assert.match(stale, /no longer says "Gone"/);
});

test('a label ending in a colon is copy, an event name is not', () => {
  assert.deepEqual(texts(scanScript('el.textContent = `Optional project task: ${name}`;')), [
    'Optional project task:'
  ]);
  assert.deepEqual(scanScript("bus.emit('task:updated', detail);"), []);
});

test('a key built from template holes is not copy', () => {
  assert.deepEqual(scanScript('const key = `task:${action.id}:${task.id}`;'), []);
});

test('a plural made with a template hole is still copy', () => {
  assert.deepEqual(texts(scanScript("el.textContent = `${n} subtask${n === 1 ? '' : 's'}`;")), [
    'subtask'
  ]);
});
