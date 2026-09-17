import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const source = readFileSync(new URL('./activity-bubble-lines.js', import.meta.url), 'utf8');

function loadLines() {
  const window = {};
  vm.runInNewContext(source, { window }, { filename: 'activity-bubble-lines.js' });
  return window.OriActivityBubbleLines;
}

const toolCall = name => ({
  kind: 'task',
  phase: 'step',
  step: { type: 'tool_call', tool_name: name }
});

test('every row of the FR25 table', () => {
  const { lineFor } = loadLines();
  const rows = [
    [{ kind: 'task', phase: 'started' }, 'On it.'],
    [{ kind: 'task', phase: 'step', step: { type: 'thinking' } }, 'Thinking it through…'],
    [toolCall('read_file'), 'Reading files…'],
    [toolCall('get_file_contents'), 'Reading files…'],
    [toolCall('list_directory'), 'Reading files…'],
    [toolCall('open_url'), 'Reading files…'],
    [toolCall('write_file'), 'Writing it down…'],
    [toolCall('create_ticket'), 'Writing it down…'],
    [toolCall('save_draft'), 'Writing it down…'],
    [toolCall('edit_document'), 'Writing it down…'],
    [toolCall('web_search'), 'Looking it up…'],
    [toolCall('fetch_page'), 'Looking it up…'],
    [toolCall('browse'), 'Looking it up…'],
    [toolCall('notebook'), 'Taking notes…'],
    [toolCall('calendar_events'), 'Checking the calendar…'],
    [toolCall('gmail_inbox'), 'Going through mail…'],
    [toolCall('mailbox_triage'), 'Going through mail…'],
    [toolCall('plugin_transport'), 'Using plugin transport…'],
    [toolCall('summarize_numbers'), 'Using summarize numbers…'],
    [
      { kind: 'task', phase: 'step', step: { type: 'tool_result', tool_name: 'x', ok: false } },
      'Hit a snag, adjusting…'
    ],
    [{ kind: 'task', phase: 'step', step: { type: 'delegation' } }, 'Asking for a hand…'],
    [{ kind: 'task', phase: 'blocked' }, 'I need your input.'],
    [{ kind: 'task', phase: 'resumed' }, 'Back to it.'],
    [{ kind: 'daily_brief', phase: 'started' }, 'Preparing today’s brief…'],
    [{ kind: 'file_janitor', phase: 'started' }, 'Sorting through the folder…'],
    [{ kind: 'task', phase: 'finished', outcome: 'succeeded' }, 'Done.'],
    [{ kind: 'task', phase: 'finished', outcome: 'partial' }, 'Done.'],
    [{ kind: 'task', phase: 'finished', outcome: 'failed' }, 'This one didn’t work out.'],
    [{ kind: 'task', phase: 'finished', outcome: 'timeout' }, 'This one didn’t work out.'],
    [
      { kind: 'file_janitor', phase: 'finished', outcome: 'succeeded', count: 0 },
      'Nothing to tidy.'
    ],
    [{ kind: 'file_janitor', phase: 'finished', outcome: 'succeeded', count: 3 }, 'Done.']
  ];
  for (const [event, want] of rows) {
    assert.equal(lineFor(event), want, JSON.stringify(event));
  }
});

test('events with nothing new to say return an empty line', () => {
  const { lineFor } = loadLines();
  assert.equal(
    lineFor({ phase: 'step', step: { type: 'tool_result', tool_name: 'x', ok: true } }),
    ''
  );
  assert.equal(lineFor({ phase: 'finished', outcome: '' }), '');
  assert.equal(lineFor({ phase: 'step' }), '');
  assert.equal(lineFor(toolCall('')), '');
  assert.equal(lineFor(null), '');
});

test('tool matching is case-insensitive and the first match wins', () => {
  const { lineFor } = loadLines();
  assert.equal(lineFor(toolCall('WEB_SEARCH')), 'Looking it up…');
  assert.equal(lineFor(toolCall('Read_Notes')), 'Reading files…', 'read beats note');
  assert.equal(lineFor(toolCall('create_note')), 'Writing it down…', 'create beats note');
  assert.equal(lineFor(toolCall('gmail_list_messages')), 'Reading files…', 'list beats mail');
});

test('unknown tools become a label cut to 28 characters', () => {
  const { lineFor, toolLabel } = loadLines();
  const long = 'summarize_the_quarterly_numbers_for_everyone';
  const label = toolLabel(long);
  assert.ok(label.length <= 28, label);
  assert.equal(label, 'summarize the quarterly numb');
  assert.equal(lineFor(toolCall(long)), 'Using ' + label + '…');
});

test('no line ever contains anything but the tool label', () => {
  const { lineFor, toolLabel } = loadLines();
  const hostile = 'zz_<img src=x>_42';
  const event = {
    kind: 'task',
    phase: 'step',
    step: { type: 'tool_call', tool_name: hostile, arguments: { path: '/secret/file.txt' } },
    result: 'SECRET RESULT',
    description: 'SECRET DESCRIPTION',
    count: 7
  };
  const line = lineFor(event);
  assert.equal(line, 'Using ' + toolLabel(hostile) + '…');
  for (const banned of ['secret', 'SECRET', '7', 'file.txt']) {
    assert.ok(!line.includes(banned), `line ${line} contains ${banned}`);
  }
  const fixed = lineFor({
    kind: 'task',
    phase: 'finished',
    outcome: 'succeeded',
    result: 'SECRET',
    count: 9
  });
  assert.equal(fixed, 'Done.');
});
