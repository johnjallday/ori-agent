import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

import {
  finishedAgo,
  firstLookReportAction,
  firstLookReportView,
  isFirstLookReportKind,
  mapPlacementView,
  reportedLine
} from './first-look-report.js';

import './folder-setup-visual.js';

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

// A hire from the Agents page as Today sends it (agents-hire-with-a-brief).
const hireReport = (overrides = {}) =>
  report({
    id: 'hire-report-hire-1',
    kind: 'hire_first_look_report',
    detail: 'You want one app. I worked from the brief.',
    attribution: 'Rosa',
    workspace: 'Compare Note Apps',
    route: '/workspaces/compare-note-apps',
    report_route: '/workspaces/compare-note-apps?panel=backlog',
    ref: { workspace_id: 'ws-2', entity_type: 'hire', entity_id: 'hire-1' },
    suggestion: { text: 'List the three apps and the criteria', evidence: 'Criteria come first.' },
    ...overrides
  });

test('a hire reports through the same card, with a link to its workspace', () => {
  assert.equal(isFirstLookReportKind('hire_first_look_report'), true);
  assert.equal(isFirstLookReportKind('folder_first_look'), true);
  assert.equal(isFirstLookReportKind('hire_first_look'), false);
  const view = firstLookReportView(hireReport(), now);
  assert.equal(view.kind, 'hire');
  assert.equal(view.lookId, 'hire-1');
  assert.equal(view.meta, 'Compare Note Apps · finished 2 min ago');
  assert.equal(view.summary, 'You want one app. I worked from the brief.');
  assert.equal(view.suggestion.text, 'List the three apps and the criteria');
  assert.deepEqual(
    view.actions.map(action => action.id),
    ['start_this', 'not_now']
  );
  assert.deepEqual(view.report, { label: 'Open workspace', href: '/workspaces/compare-note-apps' });
  // A look still running is a Working on row, never a card.
  assert.equal(firstLookReportView(hireReport({ state: 'working' }), now), null);
});

test('a hire whose look did not finish offers Retry and Open workspace, not Start this', () => {
  const view = firstLookReportView(
    hireReport({
      state: 'failed',
      title: 'First look did not finish',
      detail: 'Choose a model for Rosa before the goal can run.',
      actions: ['retry']
    }),
    now
  );
  assert.equal(view.title, 'First look did not finish');
  assert.equal(view.summary, 'Choose a model for Rosa before the goal can run.');
  assert.equal(view.suggestion, null);
  assert.equal(view.meta, 'Compare Note Apps');
  assert.deepEqual(
    view.actions.map(action => [action.id, action.label, action.primary]),
    [['retry', 'Retry', true]]
  );
  assert.equal(view.report.href, '/workspaces/compare-note-apps');
  // A folder look has no Retry; a failed folder item is not a card.
  assert.equal(firstLookReportView(report({ state: 'failed', actions: ['retry'] }), now), null);
});

test('a hire report posts to its hire, never to a workspace the browser names', async () => {
  const calls = [];
  const fetchImpl = async (url, init) => {
    calls.push([url, init.method, init.body]);
    return { ok: true, json: async () => ({}) };
  };
  const view = firstLookReportView(hireReport(), now);
  for (const action of ['start_this', 'not_now', 'retry']) {
    await firstLookReportAction(view, action, fetchImpl);
  }
  assert.deepEqual(calls, [
    ['/api/personal-assistant/hires/hire-1/start-suggestion', 'POST', '{}'],
    ['/api/personal-assistant/hires/hire-1/dismiss', 'POST', '{}'],
    ['/api/personal-assistant/hires/hire-1/retry', 'POST', '{}']
  ]);
  // A folder look never posts Retry, and an unknown action posts nothing.
  const folder = firstLookReportView(report(), now);
  assert.equal((await firstLookReportAction(folder, 'retry', fetchImpl)).ok, false);
  assert.equal((await firstLookReportAction(view, 'delete', fetchImpl)).ok, false);
  assert.equal(calls.length, 3);
});

test('Home reads Today before Personal HQ exists, so a hire reports there without a second Build link', () => {
  const home = read('personal-assistant-home.js');
  assert.match(
    home,
    /if \(view\.needsHQ\) \{[\s\S]*?els\.banner\.hidden = true;[\s\S]*?void loadToday\(\);\s*return;\s*\}/
  );
  assert.doesNotMatch(home, /needs a home base before Today can prepare a brief/);
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

test('acceptance claims no hire; a completed look points at its report separately', () => {
  const accepted = globalThis.OriFolderSetupVisual.setupAcceptedLine('Project');
  assert.match(accepted, /Setup accepted for Project/);
  assert.doesNotMatch(accepted, /Hired|assigned|is reading|finished its first look/);
  assert.equal(
    reportedLine('Development Assistant', 'elite_school'),
    "Development Assistant finished its first look at elite_school. It's under Needs you, with what it suggests first."
  );
  const folder = read('personal-assistant-folder.js');
  assert.doesNotMatch(folder, /sayInConversation\(hiredLine/);
  assert.match(folder, /before === 'running' &&\s+after === 'finished'/);
  assert.match(folder, /window\.OriAskRouting\?\.appendMessage\?\.\('assistant', line\)/);
});

test('Home draws reports as Needs you cards that count, and the drawer draws them on every page', () => {
  const home = read('personal-assistant-home.js');
  assert.match(home, /renderFirstLookReports\(els, today\?\.needs_you\?\.items\)/);
  assert.match(home, /!isFirstLookReportKind\(row\.kind\)/);
  assert.match(home, /card\.id === 'personalAssistantNeedsYouReports'/);
  // Opening the drawer shows a report (FR20): while Today is folded behind its
  // summary line, the cards (and a folder card waiting on its answer) wait in
  // a slot right under that line, and still count.
  assert.match(
    home,
    /if \(slot\.previousElementSibling !== els\.summary\) els\.summary\.after\(slot\);/
  );
  assert.match(home, /if \(list && slot\.firstElementChild !== list\) slot\.prepend\(list\);/);
  assert.match(home, /if \(waiting && waiting\.parentElement !== slot\) slot\.append\(waiting\);/);
  assert.match(home, /placeFirstLookReports\(els\);\n\}/);
  assert.match(home, /const count = cards \+ reported \+ folded \+ queued;/);
  assert.match(home, /needsHost: \(\) => needsHost\(\)/);
  // The folder card asks Home where Needs you is, so the two agree.
  const folder = read('personal-assistant-folder.js');
  assert.match(folder, /window\.PersonalAssistantToday\?\.needsHost\?\.\(\) \|\| els\.needs/);
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
