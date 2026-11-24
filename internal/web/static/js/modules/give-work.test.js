import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  canStartNow,
  doorSelector,
  giveWorkHTML,
  giveWorkOutcome,
  giveWorkRequest,
  newGiveWorkKey,
  openGiveWork
} from './give-work.js';

test('what is required, and the error names the field', () => {
  assert.deepEqual(giveWorkRequest({ what: '   ' }, 'k'), {
    error: 'Say what needs doing.',
    field: 'what'
  });
});

test('nobody chosen goes to Backlog and cannot start', () => {
  const { input } = giveWorkRequest({ what: ' Tidy up ', who: '', startNow: true }, 'k');
  assert.equal(input.state, 'backlog');
  assert.equal(input.title, 'Tidy up');
  assert.equal(input.assignee, '');
  assert.equal(input.start, false, 'Start now needs an agent, whatever the checkbox says');
  assert.equal(input.idempotencyKey, 'k');
});

test('an agent makes it Ready, and only Start now starts it', () => {
  const quiet = giveWorkRequest({ what: 'Write', who: 'Writer' }, 'k').input;
  assert.equal(quiet.state, 'ready');
  assert.equal(quiet.assignee, 'Writer');
  assert.equal(quiet.start, false);
  const started = giveWorkRequest({ what: 'Write', who: 'Writer', startNow: true }, 'k').input;
  assert.equal(started.state, 'ready');
  assert.equal(started.start, true);
});

test('a due day becomes an instant, and no due stays empty', () => {
  assert.equal(
    giveWorkRequest({ what: 'X', due: '2026-12-01' }, 'k').input.dueDate,
    new Date('2026-12-01T23:59:59').toISOString()
  );
  assert.equal(giveWorkRequest({ what: 'X', due: '' }, 'k').input.dueDate, null);
});

test('Start now is available only with an agent', () => {
  assert.equal(canStartNow({ who: '' }), false);
  assert.equal(canStartNow({ who: '  ' }), false);
  assert.equal(canStartNow({ who: 'Writer' }), true);
  assert.equal(canStartNow(null), false);
});

test('each opening gets its own key', () => {
  assert.notEqual(newGiveWorkKey(), newGiveWorkKey());
  assert.match(newGiveWorkKey(), /^give-work:/);
});

test('the outcome says where the ticket went', () => {
  assert.deepEqual(giveWorkOutcome({ displayNumber: '#7', stateLabel: 'Backlog' }), {
    kind: 'saved',
    message: '#7 is in Backlog.'
  });
  assert.equal(giveWorkOutcome({ displayNumber: '#7', start: { started: true } }).kind, 'started');
});

test('a refused start says the ticket is Ready and why', () => {
  const outcome = giveWorkOutcome({
    displayNumber: '#7',
    start: { started: false, error: 'workspace is paused' }
  });
  assert.equal(outcome.kind, 'start_refused');
  assert.equal(outcome.message, '#7 is Ready, but it could not start: workspace is paused');
});

test('the form holds what, details, who, due and Start now, and nothing about output', () => {
  const html = giveWorkHTML({ workspaceName: 'Alpha', agents: ['Writer'] });
  for (const name of ['what', 'details', 'who', 'due', 'startNow']) {
    assert.match(html, new RegExp(`name="${name}"`));
  }
  assert.doesNotMatch(html, /output|contract|metadata|approval|schedule|\btask\b/i);
  assert.match(html, /to Alpha/);
  assert.match(html, /Nobody yet/);
});

test('the door can preselect an agent, even one not in the list it passed', () => {
  assert.match(
    giveWorkHTML({ agents: ['Writer', 'Editor'], assignee: 'Editor' }),
    /<option value="Editor" selected>Editor/
  );
  assert.match(
    giveWorkHTML({ agents: [], assignee: 'Writer' }),
    /<option value="Writer" selected>/
  );
});

test('with no agents the form explains where the work goes', () => {
  const html = giveWorkHTML({ agents: [] });
  assert.doesNotMatch(html, /name="who"/);
  assert.match(html, /goes to the Backlog/);
});

test('a door can be found again after a re-render replaced it', () => {
  const door = (attributes, id = '') => ({
    attributes,
    getAttribute: name => (name === 'id' ? id : null)
  });
  assert.equal(doorSelector(door([], 'hubTicketsGiveWork')), '[id="hubTicketsGiveWork"]');
  assert.equal(
    doorSelector(
      door([
        { name: 'class', value: 'x' },
        { name: 'data-cmd-add-task', value: 'Atlas%20"Prime' }
      ])
    ),
    '[data-cmd-add-task="Atlas%20\\"Prime"]'
  );
  assert.equal(doorSelector(door([{ name: 'class', value: 'x' }])), '');
  assert.equal(doorSelector(null), '');
});

test('hostile names are escaped', () => {
  const html = giveWorkHTML({ workspaceName: '<b>x</b>', agents: ['"><script>'] });
  assert.doesNotMatch(html, /<b>x<\/b>|<script>/);
});

test('openGiveWork does nothing without a page, a workspace or the ticket client', () => {
  assert.equal(openGiveWork({ workspaceId: 'w' }), null, 'no document in this runtime');
  globalThis.document = { createElement: () => assert.fail('must not build a dialog') };
  try {
    assert.equal(openGiveWork({}), null);
    assert.equal(openGiveWork({ workspaceId: 'w' }), null, 'no ticket client');
  } finally {
    delete globalThis.document;
  }
});

// A dialog just real enough for openGiveWork: the fields it reads, the buttons
// it wires, and the calls it makes. `listeners` lets a test press them.
function fakeGiveWorkPage(t) {
  const listeners = new Map();
  const on = name => (type, handler) => listeners.set(`${name}:${type}`, handler);
  const field = (name, extra = {}) => ({
    name,
    value: '',
    focus() {},
    addEventListener: on(name),
    matches: () => true,
    ...extra
  });
  const elements = {
    what: field('what'),
    details: field('details'),
    due: field('due'),
    startNow: field('startNow', { checked: false, disabled: false })
  };
  const form = { elements, addEventListener: on('form') };
  const status = { textContent: '', dataset: {} };
  const part = (name, extra = {}) => ({
    addEventListener: on(name),
    hidden: false,
    disabled: false,
    focus() {},
    ...extra
  });
  const submit = part('submit');
  const retry = part('retry', { hidden: true });
  const cancel = part('cancel', { textContent: 'Cancel' });
  const dialog = {
    open: false,
    removed: false,
    className: '',
    setAttribute() {},
    set innerHTML(_value) {},
    addEventListener: on('dialog'),
    showModal() {
      this.open = true;
    },
    close() {
      this.open = false;
    },
    remove() {
      this.removed = true;
    },
    querySelector: selector =>
      ({
        form,
        '.give-work-start': { classList: { toggle() {} } },
        '[data-give-work-status]': status,
        '[data-give-work-submit]': submit,
        '[data-give-work-retry]': retry,
        '[data-give-work-cancel]': cancel
      })[selector] || null
  };
  const created = [];
  const saved = { document: globalThis.document, window: globalThis.window };
  globalThis.document = {
    createElement: () => dialog,
    body: { appendChild() {} }
  };
  globalThis.window = {
    WorkspaceHubTickets: {
      api: {
        create: async (workspaceId, input) => {
          created.push({ workspaceId, input });
          return { displayNumber: '#1', stateLabel: 'Backlog' };
        }
      }
    }
  };
  t.after(() => {
    globalThis.document = saved.document;
    globalThis.window = saved.window;
  });
  const press = async (name, type, event = { preventDefault() {} }) => {
    listeners.get(`${name}:${type}`)(event);
    await new Promise(resolve => setImmediate(resolve));
  };
  return { dialog, elements, created, press, status };
}

test('a prefilled Give work holds plain text, assigns nobody and cannot start anything', t => {
  const page = fakeGiveWorkPage(t);
  const hostile = '<img src=x onerror=alert(1)>';
  const opened = openGiveWork({
    workspaceId: 'ws-1',
    agents: ['Writer'],
    what: 'Add a health check endpoint.',
    details: hostile
  });
  assert.equal(opened, page.dialog);
  assert.equal(page.dialog.open, true);
  assert.equal(page.elements.what.value, 'Add a health check endpoint.');
  assert.equal(page.elements.details.value, hostile, 'text goes in as a value, never markup');
  assert.equal(page.elements.startNow.checked, false, 'Start now is off');
  assert.equal(page.elements.startNow.disabled, true, 'and unavailable with nobody chosen');
  assert.deepEqual(page.created, [], 'opening saves nothing');
});

test('cancelling a prefilled Give work creates nothing, however it is closed', async t => {
  for (const [name, type, event] of [
    ['cancel', 'click', undefined],
    ['dialog', 'cancel', { preventDefault() {} }]
  ]) {
    const page = fakeGiveWorkPage(t);
    openGiveWork({ workspaceId: 'ws-1', what: 'Suggested ticket', details: 'More.' });
    await page.press(name, type, event);
    assert.equal(page.dialog.removed, true, `${name} closes the dialog`);
    assert.deepEqual(page.created, [], `${name} saves nothing`);
  }
});

test('deliberately saving a prefilled Give work makes one backlog ticket and starts nothing', async t => {
  const page = fakeGiveWorkPage(t);
  openGiveWork({
    workspaceId: 'ws-1',
    what: 'Add a health check endpoint.',
    details: 'Suggested by the first assessment of widget.'
  });
  await page.press('form', 'submit');
  assert.equal(page.created.length, 1);
  const { workspaceId, input } = page.created[0];
  assert.equal(workspaceId, 'ws-1');
  assert.equal(input.title, 'Add a health check endpoint.');
  assert.equal(input.description, 'Suggested by the first assessment of widget.');
  assert.equal(input.state, 'backlog');
  assert.equal(input.assignee, '');
  assert.equal(input.start, false, 'no run');
  assert.match(input.idempotencyKey, /^give-work:/);
});

test('editing the suggestion before saving saves the edit, not the suggestion', async t => {
  const page = fakeGiveWorkPage(t);
  openGiveWork({ workspaceId: 'ws-1', what: 'Add a health check endpoint.', details: 'Old.' });
  page.elements.what.value = 'Add a readiness probe.';
  page.elements.details.value = 'My own words.';
  await page.press('form', 'submit');
  assert.equal(page.created.length, 1);
  assert.equal(page.created[0].input.title, 'Add a readiness probe.');
  assert.equal(page.created[0].input.description, 'My own words.');
});
