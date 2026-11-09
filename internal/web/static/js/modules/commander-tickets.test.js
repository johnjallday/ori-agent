import test from 'node:test';
import assert from 'node:assert/strict';

import { answerTicket, commanderTicketItems, ticketWaitView } from './commander-tickets.js';
import { commanderPresenter } from './commander-map.js';
import { fakeDocument } from './commander-balloon.test.js';
import { folderAgentReportView } from './folder-agent-card.js';

const waitingTask = (step = {}) => ({
  id: 't1',
  title: 'List the five songs saved most recently',
  context: {
    human_loop: {
      state: 'waiting_for_choice',
      block_id: 'b1',
      question: 'Which album first?',
      workflow_step: {
        step_type: 'ask_choice',
        choices: [
          { id: 'a1', label: 'Album-1', number: '1', recommended: true },
          { id: 'a2', label: 'Album-2', number: '2' },
          { id: 'x', label: '' }
        ],
        ...step
      }
    }
  }
});

test('a waiting ticket reads its question, its listed choices and whether a line is taken', () => {
  const wait = ticketWaitView(waitingTask());
  assert.equal(wait.question, 'Which album first?');
  assert.deepEqual(
    wait.choices.map(c => c.label),
    ['Album-1', 'Album-2']
  );
  assert.equal(wait.freeText, true, 'the ticket page takes a line unless the step forbids it');
  assert.equal(ticketWaitView(waitingTask({ free_text_allowed: false })).freeText, false);
  const form = ticketWaitView(waitingTask({ step_type: 'ask_form', choices: undefined }));
  assert.equal(form.form, true);
  assert.equal(form.freeText, false, 'a form is answered on the ticket');
  assert.equal(ticketWaitView(null), null);
});

test('answering sends the choice the way the ticket page does, to the same endpoint', async () => {
  const sent = [];
  const fetchImpl = async (url, init) => {
    sent.push({ url, init, body: JSON.parse(init.body) });
    return { ok: true, json: async () => ({ success: true }) };
  };
  const wait = ticketWaitView(waitingTask());
  assert.deepEqual(await answerTicket(wait, { choice: wait.choices[1] }, fetchImpl), {
    ok: true,
    error: ''
  });
  assert.equal(sent[0].url, '/api/orchestration/tasks/t1/assist');
  assert.equal(sent[0].init.method, 'POST');
  assert.deepEqual(sent[0].body, {
    action: 'continue_with_instruction',
    block_id: 'b1',
    choice_id: 'a2',
    choice_label: 'Album-2',
    choice_number: '2'
  });
  await answerTicket(wait, { message: '  Start with the newest  ' }, fetchImpl);
  assert.equal(sent[1].body.message, 'Start with the newest');
  const refused = async () => ({ ok: false, json: async () => ({ error: 'Task is not blocked' }) });
  assert.deepEqual(await answerTicket(wait, { choice: wait.choices[0] }, refused), {
    ok: false,
    error: 'Task is not blocked'
  });
  assert.equal((await answerTicket(wait, {}, fetchImpl)).ok, false);
});

function clickable(doc, root, selectorTest) {
  return root.all().find(selectorTest);
}

test('the balloon items: working, waiting with choices, done with its result', () => {
  const doc = fakeDocument();
  const answers = [];
  const opened = [];
  const actions = {
    answer: (wait, payload) => answers.push([wait.taskId, payload.choice?.id || payload.message]),
    open: id => opened.push(id),
    openResult: result => opened.push(result.parcelId)
  };
  const titleOf = () => 'List the five songs saved most recently';
  const working = commanderTicketItems(
    { agent: 'Albums Manager', line: 'On it.', taskId: 't1' },
    { titleOf, actions }
  );
  assert.equal(working[0].kind, 'working');
  assert.equal(working[0].line, 'Working on: List the five songs saved most recently');
  const step = commanderTicketItems(
    { agent: 'Albums Manager', line: 'Thinking it through…', taskId: 't1' },
    { titleOf, actions }
  );
  assert.equal(step[0].line, 'Thinking it through…');
  const waiting = commanderTicketItems(
    { agent: 'Albums Manager', line: 'I need your input.', blocked: true, taskId: 't1' },
    { titleOf, taskOf: () => waitingTask(), actions }
  );
  assert.equal(waiting[0].kind, 'ticket_waiting');
  assert.match(waiting[0].line, /^I need your input/);
  const body = waiting[0].body(doc);
  const choice = clickable(doc, body, n => n.dataset.commanderChoice === 'a2');
  choice.dispatch('click');
  assert.deepEqual(answers, [['t1', 'a2']]);
  clickable(doc, body, n => n.dataset.activityOpenTask === 't1').dispatch('click');
  assert.deepEqual(opened, ['t1']);
  const done = commanderTicketItems(
    {
      agent: 'Albums Manager',
      line: '',
      parcel: {
        id: 'p1',
        title: 'Five newest songs',
        summary: 'Album-3/Song\nmore',
        outcome: 'succeeded'
      }
    },
    { titleOf, actions }
  );
  assert.equal(done[0].kind, 'result');
  assert.equal(done[0].line, 'Done: Five newest songs');
  const result = done[0].body(doc);
  assert.equal(
    clickable(doc, result, n => n.className === 'commander-ticket__result').textContent,
    'Album-3/Song'
  );
  clickable(doc, result, n => n.dataset.commanderOpenResult === 'p1').dispatch('click');
  assert.deepEqual(opened, ['t1', 'p1']);
  const dismissed = commanderTicketItems(
    { agent: 'A', parcel: { id: 'p1', title: 'x' } },
    { dismissed: new Set(['p1']) }
  );
  assert.deepEqual(dismissed, [], 'an opened or dismissed result says nothing more');
  assert.deepEqual(commanderTicketItems(null), []);
});

test('a waiting ticket comes before a waiting report, with "and 1 more" opening the next (FR25)', () => {
  let fire = null;
  let activityFn = null;
  const balloon = {
    content: null,
    update(content) {
      this.content = content;
    },
    expand() {},
    collapse() {},
    focusFirst() {}
  };
  const map = {
    balloon,
    attach: () => true,
    visible: () => true,
    narrow: () => false,
    reveal() {},
    onChange: fn => ((fire = fn), () => {}),
    onActivity: fn => ((activityFn = fn), () => {})
  };
  const presenter = commanderPresenter(map, {
    ticketItems: activity =>
      commanderTicketItems(activity, { taskOf: () => waitingTask(), titleOf: () => 'Ticket' })
  });
  const host = { replaceChildren() {} };
  const doc = fakeDocument();
  const raw = {
    workspace_id: 'ws-1',
    workspace_name: 'Albums',
    folder_name: 'Albums',
    agent_name: 'Albums Manager',
    state: 'reported',
    actions: ['set_up']
  };
  presenter.present({
    view: folderAgentReportView(raw),
    raw,
    previous: null,
    followed: false,
    error: '',
    opening: false,
    host,
    doc,
    card: () => doc.createElement('article')
  });
  assert.equal(balloon.content.line, 'I have a plan for Albums · Review');
  assert.equal(balloon.content.more, null);
  activityFn({
    workspaceId: 'ws-1',
    agent: 'Albums Manager',
    line: 'I need your input.',
    blocked: true,
    taskId: 't1'
  });
  assert.match(balloon.content.line, /^I need your input/);
  assert.equal(balloon.content.more.label, 'and 1 more');
  balloon.content.onMore();
  assert.equal(balloon.content.line, 'I have a plan for Albums · Review');
  // The ticket is answered elsewhere: its controls leave the balloon (FR30).
  activityFn({ workspaceId: 'ws-1', agent: 'Albums Manager', line: 'Back to it.', taskId: 't1' });
  assert.equal(balloon.content.more.label, 'and 1 more');
  activityFn(null);
  assert.equal(balloon.content.line, 'I have a plan for Albums · Review');
  assert.equal(balloon.content.more, null);
  assert.equal(typeof fire, 'function');
  // A setup run in progress outranks the ticket line the same work makes.
  const running = {
    ...raw,
    state: 'setting_up',
    setup: {
      status: 'running',
      lines: [{ step: 'tools', name: 'Installs REAPER tools', state: 'working' }]
    }
  };
  presenter.present({
    view: folderAgentReportView(running),
    raw: running,
    previous: folderAgentReportView(raw),
    followed: false,
    error: '',
    opening: false,
    host,
    doc,
    card: () => doc.createElement('article')
  });
  activityFn({
    workspaceId: 'ws-1',
    agent: 'Albums Manager',
    line: 'Thinking it through…',
    taskId: 't9'
  });
  assert.match(balloon.content.line, /^Setting up Albums/);
  assert.equal(balloon.content.scrollKey, 'report|running');
});
