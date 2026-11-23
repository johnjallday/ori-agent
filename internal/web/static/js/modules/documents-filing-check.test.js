import test from 'node:test';
import assert from 'node:assert/strict';
import { documentsFilingCheck, parseFolder, undoSummary } from './documents-filing-check.js';

// A tiny DOM: enough to drive the module the way a person would.
function el(tag) {
  const n = {
    tag,
    textContent: '',
    className: '',
    children: [],
    listeners: {},
    attributes: {},
    disabled: false,
    value: '',
    focused: false,
    append(...kids) {
      n.children.push(...kids);
    },
    replaceChildren(...kids) {
      n.children = kids;
    },
    setAttribute(name, value) {
      n.attributes[name] = value;
    },
    addEventListener(type, fn) {
      n.listeners[type] = fn;
    },
    focus() {
      n.focused = true;
    },
    querySelectorAll(selector) {
      return all(n).filter(c => selector === 'input' && c.tag === 'input');
    }
  };
  return n;
}
const all = n => n.children.flatMap(c => [c, ...all(c)]);
const textOf = n => [n.textContent, ...n.children.map(textOf)].join(' ');
const node = (tag, text, cls) => {
  const n = el(tag);
  if (text) n.textContent = text;
  if (cls) n.className = cls;
  return n;
};
const button = (text, fn) => {
  const b = node('button', text);
  b.addEventListener('click', event => fn(event));
  return b;
};
const buttonNamed = (root, label) =>
  all(root).find(n => n.tag === 'button' && n.textContent.includes(label));
const click = async b => {
  assert.ok(b, 'button not found');
  await b.listeners.click({ currentTarget: b });
  await new Promise(r => setImmediate(r));
};

function fixture({ responses, exampleFolder = '' }) {
  const calls = [];
  const post = async (path, body) => {
    calls.push({ path, body });
    const next = responses[path]?.shift();
    if (!next) throw new Error('no response programmed for ' + path);
    if (next.fail) {
      const error = new Error('failed');
      error.body = next.fail;
      throw error;
    }
    return next.ok;
  };
  const sources = new Map([
    ['r1', { file: { source: { path: 'one.pdf', example: false }, status: 'read' } }],
    ['r2', { file: { source: { path: 'two.pdf', example: false }, status: 'read' } }],
    ['ex', { file: { source: { path: 'Examples/e.pdf', example: true }, status: 'read' } }]
  ]);
  let refreshed = 0;
  const box = documentsFilingCheck({
    node,
    button,
    group: { id: 'g', members: ['r1', 'r2', 'ex'], example_folder: exampleFolder },
    review: { id: 'rv', revision: 3 },
    sources,
    route: '/workspaces/docs',
    post,
    refresh: () => refreshed++
  });
  const checkboxes = all(box).filter(n => n.tag === 'input' && n.type === 'checkbox');
  return {
    box,
    calls,
    checkboxes,
    refreshed: () => refreshed,
    tick: async (...indexes) => {
      for (const i of indexes) {
        checkboxes[i].checked = true;
        checkboxes[i].listeners.change();
      }
    }
  };
}

const READY = {
  state: 'ready',
  digest: 'a'.repeat(64),
  folder: 'Taxes',
  items: [
    { name: 'one.pdf', final_name: 'one.pdf', to: 'Taxes/one.pdf' },
    { name: 'two.pdf', final_name: 'two (2).pdf', to: 'Taxes/two (2).pdf', renamed: true }
  ]
};
const filed = { filing: { filed: 2, folder: 'Taxes', outcomes: [], decision_recorded: true } };

test('parseFolder shapes the text and leaves the decision to the server', () => {
  assert.deepEqual(parseFolder(' Taxes/2024/ '), {
    folder: 'Taxes/2024',
    parent: 'Taxes',
    last: '2024'
  });
  assert.deepEqual(parseFolder('Taxes'), { folder: 'Taxes', parent: '', last: 'Taxes' });
  for (const bad of ['', '   ', 'a/b/c', 'Taxes//2024', '../x', 'a/..', './a'])
    assert.ok(parseFolder(bad).error, `should refuse ${JSON.stringify(bad)}`);
});

test('nothing is posted to the filing route until the explicit press', async () => {
  const f = fixture({
    exampleFolder: 'Taxes',
    responses: {
      'organization-filing-selection': [
        { ok: { selection: { state: 'matched', names: ['one.pdf', 'two.pdf'] } } }
      ],
      'organization-filing-preview': [{ ok: { preview: READY } }],
      'organization-filing': [{ ok: filed }]
    }
  });
  assert.equal(f.checkboxes.length, 2, 'an example is never offered');
  assert.ok(buttonNamed(f.box, 'Check selected files').disabled, 'no files chosen yet');
  await f.tick(0, 1);
  await click(buttonNamed(f.box, 'Check selected files'));
  assert.match(textOf(f.box), /Matched: one\.pdf, two\.pdf\. Nothing has moved\./);

  const input = all(f.box).find(n => n.tag === 'input' && n.type === 'text');
  assert.equal(input.value, 'Taxes', 'the review suggestion comes first');
  await click(buttonNamed(f.box, 'Preview filing'));

  const preview = f.calls.at(-1);
  assert.equal(preview.path, 'organization-filing-preview');
  assert.deepEqual(preview.body, {
    review: 'rv',
    group: 'g',
    revision: 3,
    sources: ['r1', 'r2'],
    destination: { folder: 'Taxes' }
  });
  const shown = textOf(f.box);
  assert.match(shown, /one\.pdf → Taxes\/one\.pdf/);
  assert.match(shown, /two\.pdf → Taxes\/two \(2\)\.pdf \(name adjusted/);
  assert.match(shown, /No file is replaced\. Nothing moves until you press/);
  assert.equal(
    f.calls.filter(c => c.path === 'organization-filing').length,
    0,
    'check and preview must never file'
  );

  await click(buttonNamed(f.box, 'File 2 documents into Taxes'));
  const press = f.calls.at(-1);
  assert.equal(press.path, 'organization-filing');
  assert.equal(press.body.digest, READY.digest);
  assert.match(press.body.client_key, /^[A-Za-z0-9_-]{16,128}$/);
  assert.deepEqual(press.body.destination, { folder: 'Taxes' });
  assert.match(textOf(f.box), /Filed 2 into Taxes\./);
  assert.match(textOf(f.box), /Nothing was replaced or deleted\. You can put these back\./);
  assert.ok(
    f.checkboxes.every(c => c.disabled),
    'the chosen files cannot be changed after filing'
  );

  await click(buttonNamed(f.box, 'Reload this view'));
  assert.equal(f.refreshed(), 1);
});

test('a folder that is not there can be made, one level at a time', async () => {
  const f = fixture({
    responses: {
      'organization-filing-selection': [
        { ok: { selection: { state: 'matched', names: ['one.pdf'] } } }
      ],
      'organization-filing-preview': [
        { ok: { preview: { state: 'destination' } } },
        { ok: { preview: { ...READY, folder: 'Taxes/2025', create: ['Taxes/2025'] } } }
      ]
    }
  });
  await f.tick(0);
  await click(buttonNamed(f.box, 'Check selected files'));
  all(f.box).find(n => n.tag === 'input' && n.type === 'text').value = 'Taxes/2025';
  await click(buttonNamed(f.box, 'Preview filing'));
  assert.match(textOf(f.box), /That folder is not available/);

  await click(buttonNamed(f.box, 'Preview making "2025"'));
  assert.deepEqual(f.calls.at(-1).body.destination, { folder: 'Taxes', new: '2025' });
  assert.match(textOf(f.box), /Ori will make the folder Taxes\/2025 first/);
});

test('a retry of the same plan reuses its key, and a changed plan needs a fresh press', async () => {
  const changed = { ...READY, digest: 'b'.repeat(64) };
  const f = fixture({
    exampleFolder: 'Taxes',
    responses: {
      'organization-filing-selection': [
        { ok: { selection: { state: 'matched', names: ['one.pdf', 'two.pdf'] } } }
      ],
      'organization-filing-preview': [{ ok: { preview: READY } }],
      'organization-filing': [
        { fail: null },
        { fail: { code: 'filing_changed', preview: changed } },
        { ok: filed }
      ]
    }
  });
  await f.tick(0, 1);
  await click(buttonNamed(f.box, 'Check selected files'));
  await click(buttonNamed(f.box, 'Preview filing'));

  await click(buttonNamed(f.box, 'File 2 documents'));
  assert.match(textOf(f.box), /Pressing the button again will not file anything twice/);
  await click(buttonNamed(f.box, 'File 2 documents'));
  const [first, second] = f.calls.filter(c => c.path === 'organization-filing');
  assert.equal(first.body.client_key, second.body.client_key, 'a retry of the same plan replays');

  // The second answer said the plan changed: the new plan is shown, nothing filed.
  assert.match(textOf(f.box), /changed since you looked\. Nothing was moved/);
  await click(buttonNamed(f.box, 'File 2 documents'));
  const third = f.calls.filter(c => c.path === 'organization-filing')[2];
  assert.equal(third.body.digest, changed.digest, 'only the new plan can be approved');
  assert.notEqual(third.body.client_key, first.body.client_key, 'a new plan is a new press');
});

test('what was not moved is said plainly, with the folder and the note', async () => {
  const f = fixture({
    exampleFolder: 'Taxes',
    responses: {
      'organization-filing-selection': [
        { ok: { selection: { state: 'matched', names: ['one.pdf', 'two.pdf'] } } }
      ],
      'organization-filing-preview': [{ ok: { preview: READY } }],
      'organization-filing': [
        {
          ok: {
            filing: {
              filed: 1,
              folder: 'Taxes',
              folder_created: true,
              interrupted: true,
              not_attempted: 0,
              decision_recorded: false,
              outcomes: [
                { name: 'one.pdf', result: 'applied' },
                { name: 'two.pdf', result: 'stale', message: 'That name was taken.' }
              ]
            }
          }
        }
      ]
    }
  });
  await f.tick(0, 1);
  await click(buttonNamed(f.box, 'Check selected files'));
  await click(buttonNamed(f.box, 'Preview filing'));
  await click(buttonNamed(f.box, 'File 2 documents'));
  const shown = textOf(f.box);
  assert.match(shown, /Filed 1 into Taxes\./);
  assert.match(shown, /Ori made the folder Taxes\./);
  assert.match(shown, /two\.pdf: That name was taken\./);
  assert.doesNotMatch(shown, /one\.pdf: /, 'a filed file is not listed as a problem');
  assert.match(shown, /Ori stopped before it finished/);
  assert.match(shown, /could not add a note to this saved view/);
});

test('every check state still reaches the person in plain words', async () => {
  for (const [state, pattern] of [
    ['install', /Filing installation/],
    ['access', /Review Filing access/],
    ['scan', /manual scan of Documents/],
    ['changed', /changed or is no longer eligible/]
  ]) {
    const f = fixture({
      responses: { 'organization-filing-selection': [{ ok: { selection: { state } } }] }
    });
    await f.tick(0);
    await click(buttonNamed(f.box, 'Check selected files'));
    assert.match(textOf(f.box), pattern, state);
    assert.equal(f.calls.length, 1, `${state} must not go on to preview`);
  }
});

const FILED_TWO = {
  filing: {
    filing_id: 'filing-1',
    filed: 2,
    folder: 'Taxes',
    decision_recorded: true,
    outcomes: [
      {
        name: 'one.pdf',
        result: 'applied',
        destination: 'Taxes/one.pdf',
        action_id: 'a1',
        undoable: true
      },
      {
        name: 'two.pdf',
        result: 'applied',
        destination: 'Taxes/two.pdf',
        action_id: 'a2',
        undoable: true
      }
    ]
  }
};

async function filedFixture(undoResponses) {
  const f = fixture({
    exampleFolder: 'Taxes',
    responses: {
      'organization-filing-selection': [
        { ok: { selection: { state: 'matched', names: ['one.pdf', 'two.pdf'] } } }
      ],
      'organization-filing-preview': [{ ok: { preview: READY } }],
      'organization-filing': [{ ok: FILED_TWO }],
      'organization-filing-undo': undoResponses
    }
  });
  await f.tick(0, 1);
  await click(buttonNamed(f.box, 'Check selected files'));
  await click(buttonNamed(f.box, 'Preview filing'));
  await click(buttonNamed(f.box, 'File 2 documents'));
  return f;
}

test('each filed file can be put back on its own, or all together', async () => {
  const f = await filedFixture([
    { ok: { undo: { back: 1, not_back: 0, outcomes: [{ name: 'one.pdf', result: 'undone' }] } } },
    { ok: { undo: { back: 1, not_back: 0, outcomes: [{ name: 'two.pdf', result: 'undone' }] } } }
  ]);
  assert.match(textOf(f.box), /one\.pdf → Taxes\/one\.pdf/);
  const [first] = all(f.box).filter(n => n.tag === 'button' && n.textContent === 'Put back');
  await click(first);
  assert.deepEqual(f.calls.at(-1), {
    path: 'organization-filing-undo',
    body: { filing: 'filing-1', action: 'a1' }
  });
  assert.match(textOf(f.box), /Put back 1 file\./);
  assert.ok(first.disabled, 'a file already put back cannot be put back twice');

  await click(buttonNamed(f.box, 'Put all back'));
  assert.deepEqual(f.calls.at(-1).body, { filing: 'filing-1' });
});

test('a file that could not be put back says why and where it is', async () => {
  const f = await filedFixture([
    {
      ok: {
        undo: {
          back: 1,
          not_back: 1,
          outcomes: [
            { name: 'one.pdf', result: 'undone' },
            {
              name: 'two.pdf',
              result: 'failed',
              message:
                'Something else is already using the original name, so Ori did not overwrite it.',
              destination: 'Taxes/two.pdf'
            }
          ]
        }
      }
    }
  ]);
  await click(buttonNamed(f.box, 'Put all back'));
  const shown = textOf(f.box);
  assert.match(shown, /Put back 1 file\./);
  assert.match(
    shown,
    /two\.pdf: Something else is already using the original name, so Ori did not overwrite it\. It is in Taxes\/two\.pdf\./
  );
});

test('an unavailable undo is one plain sentence and the buttons stay usable', async () => {
  const f = await filedFixture([{ fail: { code: 'undo_unavailable' } }]);
  const all1 = buttonNamed(f.box, 'Put all back');
  await click(all1);
  assert.match(textOf(f.box), /cannot be put back from here\. Nothing was moved\./);
  assert.equal(all1.disabled, false);
});

test('undoSummary reports what is back and what is not', () => {
  assert.equal(undoSummary({ back: 0, outcomes: [] }), 'Nothing was put back.');
  assert.equal(undoSummary({ back: 2, outcomes: [] }), 'Put back 2 files.');
  assert.equal(
    undoSummary({
      back: 1,
      outcomes: [{ name: 'a.pdf', result: 'interrupted', message: 'Ori stopped.' }]
    }),
    'Put back 1 file. a.pdf: Ori stopped.'
  );
});
