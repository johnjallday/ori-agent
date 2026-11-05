import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import {
  CONSENT_LINE,
  confirmLines,
  confirmStaffing,
  defaultTicked,
  openLibrarySheet,
  progressLine,
  sheetPosition,
  sheetView,
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
  assert.equal(
    view.subtitle,
    '5 found by the last scan · 1 already on the map · 1 needs the dashboard'
  );
  assert.equal(view.items[4].reason, 'Needs a project integration');
  assert.equal(view.allConnected, false);
});

test('rows are ordered: to set up, then file choice, then on the map, then needs the dashboard', () => {
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
    '2 projects are on the map. It has your shared REAPER Assistant. Album-2 is on the map, but its REAPER Assistant was not added: Set up a model. 2 not reached and left ticked.'
  );
  assert.equal(
    summarizeRun({ done: [{}], failed: null, remaining: [] }, null, createPlan),
    '1 project is on the map.'
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
});

test('openLibrarySheet refuses without a document or Home id', () => {
  assert.equal(openLibrarySheet({ homeID: 'h', doc: null }), null);
  assert.equal(openLibrarySheet({ homeID: '', doc: {} }), null);
});
