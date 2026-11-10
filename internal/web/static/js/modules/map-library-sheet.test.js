import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import {
  CONSENT_LINE,
  assistantState,
  confirmLines,
  confirmStaffing,
  defaultTicked,
  openLibrarySheet,
  progressLine,
  sheetPosition,
  loadConnectedStaffing,
  mergeRuns,
  resultRows,
  sheetView,
  staffingRecovery,
  summarizeRepair,
  summarizeRun
} from './map-library-sheet.js';

const rows = [
  { id: 'e1', name: 'Album-1', kind: 'setup', files: ['Album-1.rpp'] },
  { id: 'e2', name: 'Album-2', kind: 'setup', files: ['Album-2.rpp'] },
  { id: 'e3', name: 'Album-3', kind: 'file_choice', files: ['Album-3.rpp', 'Album-3 v2.rpp'] },
  { id: 'e4', name: 'Album-4', kind: 'connected', files: [], workspaceId: 'ws-4' },
  { id: 'e6', name: 'Demo', kind: 'dashboard', files: [], reason: 'Needs a project integration' }
];

test('single-file rows are ticked by default; a file-choice row needs a file first', () => {
  const ticked = defaultTicked(rows);
  assert.deepEqual([...ticked], ['e1', 'e2']);
  const view = sheetView(rows, { ticked });
  assert.deepEqual(
    view.items.map(item => [item.id, item.checkable, item.checked, item.file]),
    [
      ['e1', true, true, 'Album-1.rpp'],
      ['e2', true, true, 'Album-2.rpp'],
      ['e3', true, false, ''],
      ['e4', false, false, ''],
      ['e6', false, false, '']
    ]
  );
  assert.equal(view.buttonLabel, 'Put 2 on the map');
  assert.equal(view.buttonDisabled, false);
  assert.equal(view.subtitle, "5 on the shelf · 1 already on the map · 1 can't be set up here");
  assert.equal(view.items[4].reason, 'Needs a project integration');
  assert.equal(view.allConnected, false);
});

test("rows are ordered: to set up, then file choice, then on the map, then can't be set up here", () => {
  const shuffled = [rows[4], rows[3], rows[2], rows[1], rows[0]];
  assert.deepEqual(
    sheetView(shuffled, {}).items.map(item => item.id),
    ['e2', 'e1', 'e3', 'e4', 'e6'],
    'library order is kept inside each bucket'
  );
});

test('choosing a file makes the file-choice row count; an unknown file does not', () => {
  const ticked = new Set(['e3']);
  assert.equal(sheetView(rows, { ticked, chosen: { e3: 'Nope.rpp' } }).selected.length, 0);
  const view = sheetView(rows, { ticked, chosen: { e3: 'Album-3 v2.rpp' } });
  assert.deepEqual(
    view.selected.map(item => [item.id, item.file]),
    [['e3', 'Album-3 v2.rpp']]
  );
  assert.equal(view.buttonLabel, 'Put 1 on the map');
  assert.deepEqual(confirmLines(view.selected), ['Album-3 — Album-3 v2.rpp']);
});

test('a row with progress is no longer checkable, and nothing selected disables the button', () => {
  const view = sheetView(rows, {
    ticked: new Set(['e1']),
    progress: { e1: { status: 'working' } }
  });
  assert.equal(view.items[0].checkable, false);
  assert.equal(view.items[0].checked, false);
  assert.equal(view.buttonDisabled, true);
  assert.equal(sheetView([], {}).buttonLabel, 'Put 0 on the map');
  assert.equal(
    sheetView([rows[3]], {}).allConnected,
    true,
    'everything on the map means the sheet can close itself after a run'
  );
});

test('the sheet never says "ready"', () => {
  const source = readFileSync(new URL('./map-library-sheet.js', import.meta.url), 'utf8');
  assert.doesNotMatch(source.replace(/\/\/.*$/gm, ''), /'[^']*\bready\b[^']*'/i);
  assert.doesNotMatch(CONSENT_LINE, /ready/i);
});

const createPlan = {
  plan: 'create',
  agentName: 'REAPER Assistant',
  roleLabel: 'REAPER Assistant',
  teamDigest: 'a'.repeat(64)
};

test('confirmStaffing discloses the assistant and keeps a placement-only choice', () => {
  const staff = confirmStaffing(createPlan, 4, 'staff');
  assert.equal(staff.staff, true);
  assert.equal(staff.teamDigest, 'a'.repeat(64));
  assert.equal(staff.buttonLabel, 'Put on the map and add the assistant');
  assert.match(staff.staffLine, /REAPER Assistant/);
  const placement = confirmStaffing(createPlan, 4, 'placement');
  assert.equal(placement.staff, false);
  // Choosing placement only no longer claims the approval is being used.
  assert.doesNotMatch(placement.note, /Uses the sharing/);
  assert.match(placement.note, /No assistant is added now/);
  assert.equal(placement.teamDigest, '');
  assert.equal(placement.buttonLabel, 'Put on the map');
  // A blocker never stages staffing, whatever the choice says.
  const blocked = confirmStaffing({ plan: 'needs_model', message: 'Set up a model.' }, 2, 'staff');
  assert.equal(blocked.staff, false);
  assert.equal(blocked.canStaff, false);
  assert.match(blocked.note, /Set up a model/);
});

test('progressLine keeps the connection and the assistant apart', () => {
  assert.equal(progressLine({ status: 'working' }), 'Setting up…');
  assert.equal(
    progressLine({ status: 'done', staffing: { state: 'added' } }),
    'On the map · assistant added'
  );
  assert.equal(
    progressLine({ status: 'done', staffing: { state: 'not_requested' } }),
    'On the map · no assistant (placement only)'
  );
  assert.equal(
    progressLine({ status: 'failed', stage: 'staffing', message: 'Set up a model.' }),
    'On the map · assistant not added: Set up a model.'
  );
  assert.equal(
    progressLine({ status: 'failed', stage: 'connection', message: 'it changed' }),
    'Failed: it changed'
  );
});

test('a connected row offers the assistant only when the Home can fill it, never ticked by default', () => {
  assert.deepEqual(assistantState(null), {
    known: false,
    repairable: false,
    line: '',
    blocked: ''
  });
  assert.equal(assistantState({ filled: false, plan: 'join' }).repairable, true);
  assert.equal(assistantState({ filled: false, plan: 'create' }).line, 'no assistant yet');
  const off = assistantState({ filled: false, plan: 'off', message: 'Switched off.' });
  assert.equal(off.repairable, false);
  assert.equal(off.blocked, 'Switched off.');
  assert.equal(
    assistantState({ filled: true, shared: true, agent_name: 'REAPER Assistant' }).line,
    'has REAPER Assistant'
  );
  assert.equal(
    assistantState({ filled: true, shared: false, agent_name: 'Mix Buddy' }).line,
    'has its own Mix Buddy'
  );
  const rowStaffing = {
    e4: { filled: false, plan: 'join' },
    e5: { filled: false, plan: 'off', message: 'Switched off.' }
  };
  const connected = [
    { id: 'e4', name: 'Album-4', kind: 'connected', files: [] },
    { id: 'e5', name: 'Album-5', kind: 'connected', files: [] }
  ];
  // Nothing is selected for repair until the person ticks it.
  assert.equal(sheetView(connected, { rowStaffing }).repairing.length, 0);
  const view = sheetView(connected, { rowStaffing, repair: new Set(['e4', 'e5']) });
  assert.deepEqual(
    view.repairing.map(item => item.id),
    ['e4']
  );
  assert.equal(view.repairLabel, 'Add the assistant to 1');
});

const albums = ['Album-1', 'Album-2', 'Album-3', 'Album-4'].map((name, i) => ({
  id: `e${i + 1}`,
  name,
  file: `${name}.rpp`
}));

test('resultRows keeps connection, assistant and unreached rows apart', () => {
  const rows = resultRows({
    items: albums,
    result: {
      done: [
        { id: 'e1', workspaceId: 'ws-1', created: true, staffing: { state: 'added' } },
        {
          id: 'e2',
          workspaceId: 'ws-2',
          created: false,
          staffing: { state: 'already', shared: false, agent_name: 'Mix Buddy' }
        }
      ],
      failed: {
        id: 'e3',
        name: 'Album-3',
        stage: 'staffing',
        reason: 'needs_model',
        workspaceId: 'ws-3',
        created: true,
        message: 'Set up a model.'
      },
      remaining: [albums[3]]
    }
  });
  assert.deepEqual(
    rows.map(row => [
      row.name,
      row.connection,
      row.assistant,
      row.complete,
      row.recovery?.kind || ''
    ]),
    [
      ['Album-1', 'Put on the map', 'assistant added', true, ''],
      ['Album-2', 'Already on the map', 'kept its own Mix Buddy', true, ''],
      ['Album-3', 'Put on the map', 'assistant not added: Set up a model.', false, 'settings'],
      ['Album-4', 'Not reached', '', false, '']
    ]
  );
  // A connected-but-unstaffed row keeps its workspace for the map and link.
  assert.equal(rows[2].workspaceId, 'ws-3');
  const closed = resultRows({
    items: albums.slice(0, 2),
    result: {
      done: [
        { id: 'e1', workspaceId: 'ws-1', created: true, staffing: { state: 'not_requested' } }
      ],
      failed: null,
      remaining: [albums[1]],
      stopped: true
    }
  });
  assert.deepEqual(
    closed.map(row => [row.connection, row.assistant]),
    [
      ['Put on the map', 'no assistant (placement only)'],
      ['Not started (closed)', '']
    ]
  );
  const notConnected = resultRows({
    items: albums.slice(0, 1),
    result: {
      done: [],
      failed: { id: 'e1', stage: 'connection', message: 'it changed' },
      remaining: []
    }
  });
  assert.equal(notConnected[0].connection, 'Not set up: it changed');
  assert.equal(notConnected[0].recovery.kind, 'retry');
});

test('each stop points at the place that settles it; an unknown one retries', () => {
  assert.equal(staffingRecovery('needs_model').kind, 'settings');
  assert.equal(staffingRecovery('off').kind, 'sharing');
  assert.equal(staffingRecovery('consent_stale').kind, 'sharing');
  assert.equal(staffingRecovery('assistant_missing').kind, 'sharing');
  assert.equal(staffingRecovery('reconcile').kind, 'agents');
  assert.equal(staffingRecovery('sharing_changed').kind, 'reload');
  assert.equal(staffingRecovery('failed').kind, 'retry');
  assert.equal(staffingRecovery('unsettled').kind, 'retry');
  assert.equal(staffingRecovery('').kind, 'retry');
});

test('mergeRuns keeps earlier successes and every confirmed row', () => {
  const first = {
    items: albums,
    staffing: { teamDigest: 'd' },
    kind: 'setup',
    result: {
      done: [{ id: 'e1', workspaceId: 'ws-1', created: true, staffing: { state: 'added' } }],
      failed: { id: 'e2', stage: 'staffing', reason: 'failed', workspaceId: 'ws-2' },
      remaining: albums.slice(2)
    }
  };
  const retried = {
    items: albums.slice(1),
    staffing: { teamDigest: 'd' },
    kind: 'setup',
    result: {
      done: albums.slice(1).map((a, i) => ({
        id: a.id,
        workspaceId: `ws-${i + 2}`,
        created: i > 0,
        staffing: { state: 'joined' }
      })),
      failed: null,
      remaining: []
    }
  };
  const merged = mergeRuns(first, retried);
  assert.equal(merged.items.length, 4);
  assert.deepEqual(
    merged.result.done.map(item => item.id),
    ['e1', 'e2', 'e3', 'e4']
  );
  assert.equal(merged.result.failed, null);
  assert.ok(resultRows(merged).every(row => row.complete));
});

test('a retried row is still reported as put on the map by the run that stopped at it', () => {
  const first = {
    items: albums.slice(0, 2),
    staffing: { teamDigest: 'd' },
    kind: 'setup',
    result: {
      done: [],
      failed: { id: 'e1', stage: 'staffing', reason: '', workspaceId: 'ws-1', created: true },
      remaining: [albums[1]]
    }
  };
  const merged = mergeRuns(first, {
    items: albums.slice(0, 2),
    staffing: { teamDigest: 'd' },
    kind: 'setup',
    result: {
      done: [
        {
          id: 'e1',
          workspaceId: 'ws-1',
          created: false,
          staffing: { state: 'already', shared: true }
        },
        { id: 'e2', workspaceId: 'ws-2', created: true, staffing: { state: 'joined' } }
      ],
      failed: null,
      remaining: []
    }
  });
  assert.deepEqual(
    resultRows(merged).map(row => row.connection),
    ['Put on the map', 'Put on the map']
  );
});

test('a status line ends each sentence once, whatever the server message ends with', () => {
  const plan = { roleLabel: 'REAPER Assistant' };
  const stopped = message =>
    summarizeRun(
      {
        done: [],
        failed: { name: 'Album-1', stage: 'staffing', workspaceId: 'ws-1', message },
        remaining: [{}]
      },
      { teamDigest: 'd' },
      plan
    );
  assert.equal(
    stopped('Library request failed (502)'),
    '1 project is on the map. Album-1 is on the map, but its REAPER Assistant was not added: Library request failed (502). 1 not reached and left ticked.'
  );
  assert.equal(
    stopped('Set up a model.'),
    '1 project is on the map. Album-1 is on the map, but its REAPER Assistant was not added: Set up a model. 1 not reached and left ticked.'
  );
  assert.equal(
    summarizeRun(
      {
        done: [],
        failed: { name: 'Album-1', stage: 'connection', message: 'it changed.' },
        remaining: []
      },
      null,
      plan
    ),
    'Stopped at Album-1: it changed.'
  );
});

test('summarizeRepair says what changed and where it stopped', () => {
  const plan = { roleLabel: 'REAPER Assistant' };
  assert.equal(
    summarizeRepair({ done: [{}, {}], failed: null, remaining: [] }, plan),
    '2 projects now have your shared REAPER Assistant.'
  );
  assert.equal(
    summarizeRepair(
      { done: [{}], failed: { name: 'Album-3', message: 'Set up a model.' }, remaining: [{}] },
      plan
    ),
    '1 project now has your shared REAPER Assistant. Album-3: Set up a model. 1 not reached and left ticked.'
  );
});

test('loadConnectedStaffing reads each connected row and drops unreadable ones', async () => {
  const calls = [];
  const statuses = await loadConnectedStaffing(
    {
      request: async path => {
        calls.push(path);
        if (path.includes('bad')) throw new Error('no');
        return { filled: false, plan: 'join' };
      }
    },
    [
      { id: 'e1', kind: 'setup' },
      { id: 'e4', kind: 'connected' },
      { id: 'bad', kind: 'connected' }
    ]
  );
  assert.deepEqual(calls, ['/projects/e4/staffing', '/projects/bad/staffing']);
  assert.deepEqual(Object.keys(statuses), ['e4']);
});

test('summarizeRun never calls a connected but unstaffed project fully set up', () => {
  const staffing = { teamDigest: 'a'.repeat(64) };
  assert.equal(
    summarizeRun({ done: [{}, {}], failed: null, remaining: [] }, staffing, createPlan),
    '2 projects are on the map. 2 have your shared REAPER Assistant.'
  );
  assert.equal(
    summarizeRun(
      {
        done: [{}],
        failed: {
          name: 'Album-2',
          stage: 'staffing',
          workspaceId: 'ws-2',
          message: 'Set up a model.'
        },
        remaining: [{}, {}]
      },
      staffing,
      createPlan
    ),
    '2 projects are on the map. 1 of 2 has your shared REAPER Assistant. Album-2 is on the map, but its REAPER Assistant was not added: Set up a model. 2 not reached and left ticked.'
  );
  assert.equal(
    summarizeRun({ done: [{}], failed: null, remaining: [] }, null, createPlan),
    '1 project is on the map.'
  );
});

test('summarizeRun adds the one placement reason for a song that went into the Home instead', () => {
  const note =
    'Album-1 went into your Music Home, not into Albums, because it has a running ticket.';
  assert.equal(
    summarizeRun(
      { done: [{ placementNote: note }, {}], failed: null, remaining: [] },
      null,
      createPlan
    ),
    `2 projects are on the map. ${note}`
  );
  assert.equal(
    summarizeRun({ done: [{}, {}], failed: null, remaining: [] }, null, createPlan),
    '2 projects are on the map.'
  );
});

test('sheetPosition sits beside the anchor, falls below when there is no room, and stays in view', () => {
  const size = { width: 440, height: 500 };
  const view = { width: 1400, height: 900 };
  assert.deepEqual(sheetPosition({ left: 100, right: 220, top: 150, bottom: 194 }, size, view), {
    left: 232,
    top: 150,
    width: 440,
    maxHeight: 500
  });
  const below = sheetPosition({ left: 1000, right: 1120, top: 150, bottom: 194 }, size, view);
  assert.equal(below.top, 202, 'below the anchor');
  assert.equal(below.left, 1400 - 16 - 440, 'pulled back inside the right gutter');
  const tiny = sheetPosition({ left: 0, right: 40, top: 0, bottom: 40 }, size, {
    width: 400,
    height: 600
  });
  assert.deepEqual(
    tiny,
    { left: 16, top: 72, width: 368, maxHeight: 500 },
    'no room to the right: below the anchor, narrowed to the viewport, never under the top bar'
  );
  const short = sheetPosition({ left: 100, right: 220, top: 10, bottom: 54 }, size, {
    width: 1440,
    height: 560
  });
  assert.equal(short.top, 72, 'a short window keeps the title clear of the top bar');
  assert.equal(short.maxHeight, 560 - 72 - 16);

  // A full-width toolbar row (a Home's own map) has no room beside it: the
  // sheet goes under it and fits the space there, leaving the row in sight.
  const tall = { width: 440, height: 640 };
  const screen = { width: 1440, height: 900 };
  const underRow = sheetPosition({ left: 32, right: 1408, top: 340, bottom: 395 }, tall, screen);
  assert.deepEqual(underRow, { left: 32, top: 403, width: 440, maxHeight: 900 - 16 - 403 });
  // Too little room under it for a usable sheet: it moves up, as before.
  const lowRow = sheetPosition({ left: 32, right: 1408, top: 560, bottom: 615 }, tall, screen);
  assert.equal(lowRow.maxHeight, 640);
  assert.equal(lowRow.top, 900 - 16 - 640);
});

test('openLibrarySheet refuses without a document or Home id', () => {
  assert.equal(openLibrarySheet({ homeID: 'h', doc: null }), null);
  assert.equal(openLibrarySheet({ homeID: '', doc: {} }), null);
});

// One vocabulary (folder-setup-ends-in-home FR22–FR25): "shelf" and "on the
// map"; no "dashboard" or "File-only" in anything the sheet says, one wording
// for both shelf links, and the new assurance line.
test('the sheet speaks of the shelf and the map, never the dashboard', () => {
  const strings = file =>
    readFileSync(new URL(file, import.meta.url), 'utf8')
      .replace(/\/\*[\s\S]*?\*\/|\/\/.*$/gm, '')
      .match(/'[^'\n]*'|"[^"\n]*"|`[^`]*`/g)
      .join('\n');
  for (const file of ['./map-library-sheet.js', './library-setup.js']) {
    // The bucket's key and property reads are code, not words the sheet says.
    const said = strings(file).replace(/'dashboard'|DASHBOARD|is-dashboard|\.dashboard\b/g, '');
    assert.doesNotMatch(said, /dashboard|File-only/i, file);
  }
  const sheet = strings('./map-library-sheet.js');
  assert.equal((sheet.match(/'Open the shelf'/g) || []).length, 2, 'row link and footer link');
  assert.match(
    sheet,
    /Each workspace only reads its project file\. Your files stay where they are and are never changed\./
  );
  assert.match(sheet, /Put \$\{selected\.length\} on the map/);
});
