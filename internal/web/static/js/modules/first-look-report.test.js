import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

import {
  finishedAgo,
  firstLookReportAction,
  firstLookReportView,
  hiredLine,
  mapPlacementView,
  reportedLine
} from './first-look-report.js';

const read = name => readFileSync(new URL(`./${name}`, import.meta.url), 'utf8');
const now = new Date('2026-10-21T10:00:00Z');

// A hired agent's report as the server's Today sends it under Needs you.
const report = (overrides = {}) => ({
  id: 'first-look-report-look-1',
  kind: 'folder_first_look',
  state: 'finished',
  title: 'Finished its first look',
  detail: 'A Django site with three apps. No CI runs it.',
  attribution: 'Development Assistant',
  workspace: 'elite_school',
  source_at: '2026-10-21T09:58:00Z',
  route: '/workspaces/elite-school/task/look-1',
  report_route: '/workspaces/elite-school/task/look-1',
  ref: { workspace_id: 'ws-1', entity_type: 'task', entity_id: 'look-1' },
  suggestion: {
    text: 'Add a CI workflow that runs the existing pytest suite.',
    evidence: 'Small · from pyproject.toml, tests/'
  },
  actions: ['start_this', 'not_now'],
  ...overrides
});

test('a report says who looked, where, when, what it found and what it suggests first', () => {
  const view = firstLookReportView(report(), now);
  assert.equal(view.lookId, 'look-1');
  assert.equal(view.agent, 'Development Assistant');
  assert.equal(view.initials, 'DA');
  assert.equal(view.meta, 'elite_school · finished 2 min ago');
  assert.equal(view.title, 'Finished its first look');
  assert.equal(view.summary, 'A Django site with three apps. No CI runs it.');
  assert.deepEqual(view.suggestion, {
    text: 'Add a CI workflow that runs the existing pytest suite.',
    evidence: 'Small · from pyproject.toml, tests/'
  });
  assert.deepEqual(
    view.actions.map(action => action.label),
    ['Start this', 'Not now']
  );
  assert.equal(view.actions[0].primary, true);
  assert.deepEqual(view.report, {
    label: 'Read the full report',
    href: '/workspaces/elite-school/task/look-1'
  });
});

test('without a suggestion there is no Start this and no suggestion box', () => {
  const view = firstLookReportView(report({ suggestion: undefined, actions: ['not_now'] }), now);
  assert.equal(view.suggestion, null);
  assert.deepEqual(
    view.actions.map(action => action.id),
    ['not_now']
  );
  // A Start this the server listed without a suggestion is still not offered.
  const forged = firstLookReportView(report({ suggestion: { text: '' } }), now);
  assert.ok(!forged.actions.some(action => action.id === 'start_this'));
});

test('only a finished look report with a safe report link is drawn', () => {
  assert.equal(firstLookReportView(report({ state: 'running' }), now), null);
  assert.equal(firstLookReportView(report({ kind: 'folder_result' }), now), null);
  assert.equal(firstLookReportView(report({ ref: { entity_id: '../x' } }), now), null);
  assert.equal(firstLookReportView(report({ report_route: '//evil/task/x' }), now).report, null);
  assert.equal(
    firstLookReportView(report({ report_route: '/workspaces/a/plans' }), now).report,
    null
  );
  // Unknown actions are dropped.
  assert.deepEqual(
    firstLookReportView(report({ actions: ['start_this', 'delete'] }), now).actions.map(
      action => action.id
    ),
    ['start_this']
  );
});

test('On the Map says where the building sits and Move opens Home with it in move mode', () => {
  assert.deepEqual(firstLookReportView(report(), now).map, {
    label: 'On the Map',
    text: "Development Assistant's building was placed automatically.",
    move: { label: 'Move', href: '/?move=ws-1' }
  });
  assert.equal(
    firstLookReportView(report({ home: 'Music Production Home' }), now).map.text,
    "Development Assistant's building sits inside Music Production Home."
  );
  // Without a workspace, or with one that is not an id, there is nothing to move.
  assert.equal(
    firstLookReportView(report({ ref: { entity_type: 'task', entity_id: 'look-1' } }), now).map,
    null
  );
  assert.equal(
    mapPlacementView({ agent: '', home: '', workspaceId: '../ws' }),
    null,
    'a forged id is not a link'
  );
  assert.equal(mapPlacementView({ workspaceId: 'ws 1' }), null);
  assert.equal(
    mapPlacementView({ workspaceId: 'ws-2' }).text,
    'Its building was placed automatically.'
  );
});

test('finished-ago reads like a person', () => {
  assert.equal(finishedAgo('2026-10-21T09:59:50Z', now), 'finished just now');
  assert.equal(finishedAgo('2026-10-21T09:00:00Z', now), 'finished 1 h ago');
  assert.equal(finishedAgo('2026-10-20T09:00:00Z', now), 'finished yesterday');
  assert.equal(finishedAgo('not a time', now), '');
});

test('Start this and Not now name only the look', async () => {
  const calls = [];
  const fetchImpl = async (url, init) => {
    calls.push([url, init.method, init.body]);
    return { ok: true, json: async () => ({ started: true }) };
  };
  const view = firstLookReportView(report(), now);
  assert.equal((await firstLookReportAction(view, 'start_this', fetchImpl)).ok, true);
  assert.equal((await firstLookReportAction(view, 'not_now', fetchImpl)).ok, true);
  assert.equal((await firstLookReportAction(view, 'delete', fetchImpl)).ok, false);
  assert.deepEqual(calls, [
    ['/api/personal-assistant/first-look/look-1/start-suggestion', 'POST', '{}'],
    ['/api/personal-assistant/first-look/look-1/dismiss', 'POST', '{}']
  ]);
  // A suggestion already started elsewhere is not a failure to report.
  const conflict = async () => ({
    ok: false,
    json: async () => ({ code: 'already_acted' })
  });
  assert.equal((await firstLookReportAction(view, 'start_this', conflict)).ok, true);
  const refused = async () => ({ ok: false, json: async () => ({ code: 'no_suggestion' }) });
  assert.equal((await firstLookReportAction(view, 'start_this', refused)).ok, false);
});

test('the assistant says the hire is made, and points at the report when it lands', () => {
  assert.equal(
    hiredLine('Development Assistant'),
    "Hired. Development Assistant is on it. I'll tell you here when it has something."
  );
  assert.equal(
    reportedLine('Development Assistant', 'elite_school'),
    "Development Assistant finished its first look at elite_school. It's under Needs you, with what it suggests first."
  );
  const folder = read('personal-assistant-folder.js');
  // Host lines, no model turn: written straight into the conversation.
  assert.match(
    folder,
    /if \(hire\) sayInConversation\(hiredLine\(hire\.agent_name \|\| hire\.role_label\)\);/
  );
  assert.match(folder, /before === 'running' &&\s+after === 'finished'/);
  assert.match(folder, /window\.OriAskRouting\?\.appendMessage\?\.\('assistant', line\)/);
});

test('Home draws reports as Needs you cards that count, and the drawer draws them on every page', () => {
  const home = read('personal-assistant-home.js');
  assert.match(home, /renderFirstLookReports\(els, today\?\.needs_you\?\.items\)/);
  assert.match(home, /row\.kind !== FIRST_LOOK_REPORT_KIND/);
  assert.match(home, /card\.id === 'personalAssistantNeedsYouReports'/);
  // Opening the drawer shows a report (FR20): while Today is folded behind its
  // summary line, the cards wait right under that line, and still count.
  assert.match(
    home,
    /if \(list\.previousElementSibling !== els\.summary\) els\.summary\.after\(list\);/
  );
  assert.match(home, /placeFirstLookReports\(els\);\n\}/);
  assert.match(home, /const count = cards \+ reported \+ queued;/);
  const panel = read('personal-assistant-panel.js');
  assert.match(
    panel,
    /mountFirstLookReports\(reports, personal \? state\.today\?\.needs_you\?\.items : \[\]/
  );
  const guide = readFileSync(
    new URL('../../../templates/components/ori-guide.tmpl', import.meta.url),
    'utf8'
  );
  assert.match(guide, /id="personalAssistantNeedsReports"/);
});
