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
