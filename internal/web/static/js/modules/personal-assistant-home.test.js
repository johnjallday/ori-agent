import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

import {
  MEETING_BADGES,
  meetingsSectionView,
  needsHireBanner,
  partialTodaySummary,
  personalAssistantLauncherCue,
  personalAssistantLauncherCueTone,
  personalAssistantTodayView,
  progressRowText,
  progressRowVisible,
  resumeWorkspaceBuild,
  safeTodayRoute,
  specialistSetupView,
  studioSectionView,
  summaryFoldAfter,
  summaryStripText,
  summaryStripView,
  todayLabel,
  todaySectionItems,
  todayThreeSectionView,
  todaySectionRows
} from './personal-assistant-home.js';

test('an unfinished build keeps its id and only the controls Today knows', () => {
  const [build, other] = todaySectionItems({
    items: [
      {
        id: 'build-1',
        kind: 'workspace_build',
        title: 'Finish building Newsletter Desk',
        actions: ['resume', 'discard', 'delete_everything']
      },
      { id: 'o1', kind: 'folder_offer', title: 'Look at Documents', actions: ['resume'] }
    ]
  });
  assert.equal(build.id, 'build-1');
  assert.deepEqual(build.actions, ['resume', 'discard']);
  assert.equal('actions' in other, false, 'other rows stay links');
  assert.equal('id' in other, false);
});

test('Resume opens the dialog in build mode, or goes Home where the dialog is', () => {
  const opened = [];
  const withDialog = {
    sessionManager: { showAddWorkspaceModal: options => opened.push(options) },
    document: { getElementById: id => (id === 'addFolderModal' ? {} : null) },
    PersonalAssistantPanel: { close() {} },
    location: { href: '/agents' }
  };
  assert.equal(resumeWorkspaceBuild(withDialog), 'opened');
  assert.deepEqual(opened, [{ entryPoint: 'personal_assistant_ask', buildResume: true }]);

  const withoutDialog = { document: { getElementById: () => null }, location: { href: '/agents' } };
  assert.equal(resumeWorkspaceBuild(withoutDialog), 'navigated');
  assert.equal(withoutDialog.location.href, '/?build=resume');
});

test('three Today sections hide empty rows and report unavailable sources only once in the footer', () => {
  assert.equal(todayLabel('waiting_for_choice'), 'Waiting for your choice');
  assert.equal(todayLabel('future_status'), 'Future status');
  assert.deepEqual(todaySectionItems({ health: { status: 'unavailable' }, items: [] }), []);
  const view = todayThreeSectionView({
    working_on: {
      items: [
        {
          kind: 'hq_status',
          title: 'Personal HQ',
          detail: 'waiting_for_choice',
          route: '/workspaces/my-hq'
        }
      ]
    },
    needs_you: { items: [] },
    done: { items: [] },
    unavailable_sources: ['follow-ups', 'follow-ups', 'decisions']
  });
  assert.equal(view.working[0].detail, 'Waiting for your choice');
  assert.deepEqual(view.needs, []);
  assert.equal(view.footer, "Couldn't read: follow-ups, decisions.");
  // The HQ's own status line is listed under Working on but is not work.
  assert.equal(view.inProgress, 0);
  assert.equal(view.doneToday, 0);
});

test('the progress row counts work in progress and what was done today', () => {
  const now = new Date(2026, 9, 7, 15, 0, 0);
  const at = (day, hour) => new Date(2026, 9, day, hour, 0, 0).toISOString();
  const view = todayThreeSectionView(
    {
      working_on: {
        items: [
          { kind: 'hq_status', title: 'My HQ' },
          { kind: 'folder_workspace', title: 'Song Sketches' },
          { kind: 'janitor_work', title: 'File Janitor' }
        ]
      },
      done: {
        items: [
          { kind: 'task_result', title: 'Sorted Samples', source_at: at(7, 9) },
          { kind: 'task_result', title: 'Drafted a reply', source_at: at(7, 14) },
          // Done keeps a week of results; yesterday's is not "done today".
          { kind: 'task_result', title: 'Last night', source_at: at(6, 23) },
          { kind: 'hq_setup', title: 'Set up My HQ', source_at: '0001-01-01T00:00:00Z' },
          { kind: 'follow_up', title: 'No timestamp' }
        ]
      }
    },
    now
  );
  assert.equal(view.inProgress, 2);
  assert.equal(view.doneToday, 2);
  assert.equal(view.done.length, 5, 'the Done list itself still shows every item');
  assert.equal(progressRowText(view.inProgress, view.doneToday), '2 in progress · 2 done today');

  assert.equal(progressRowText(0, 0), '0 in progress · 0 done today');
  assert.equal(progressRowText(1, 12), '1 in progress · 12 done today');
  assert.equal(progressRowText(undefined, -3), '0 in progress · 0 done today');
});

test('the progress row is hidden only when there is no work, nothing done today and no meetings', () => {
  assert.equal(progressRowVisible({ working: 0, done: 0, meetings: false }), false);
  assert.equal(progressRowVisible({ working: 1, done: 0, meetings: false }), true);
  assert.equal(progressRowVisible({ working: 0, done: 3, meetings: false }), true);
  // Today's meetings are listed under Working on, so the row still expands.
  assert.equal(progressRowVisible({ working: 0, done: 0, meetings: true }), true);
});

test('ready attention defaults folded and preserves explicit expansion across chat and reopen', () => {
  const rest = { started: false, expanded: false };
  const folded = { started: true, expanded: false };
  const shown = { started: true, expanded: true };

  // Ready includes an empty or rehydrated drawer, without requiring a send.
  assert.deepEqual(summaryFoldAfter(rest, { type: 'ready' }), folded);
  assert.deepEqual(summaryFoldAfter(shown, { type: 'ready' }), shown);
  assert.deepEqual(summaryFoldAfter(rest, { type: 'sent' }), folded);
  assert.deepEqual(summaryFoldAfter(rest, { type: 'folder', by: 'user' }), folded);
  // The assistant speaking first (its first-folder prompt) folds nothing.
  assert.deepEqual(summaryFoldAfter(rest, { type: 'folder', by: 'assistant' }), rest);
  assert.deepEqual(summaryFoldAfter(rest, { type: 'folder' }), rest);

  // Show and Hide reverse each other, and do nothing before a conversation.
  assert.deepEqual(summaryFoldAfter(folded, { type: 'toggle' }), shown);
  assert.deepEqual(summaryFoldAfter(shown, { type: 'toggle' }), folded);
  assert.deepEqual(summaryFoldAfter(rest, { type: 'toggle' }), rest);
  // Something under Needs you has to be seen.
  assert.deepEqual(summaryFoldAfter(folded, { type: 'expand' }), shown);
  assert.deepEqual(summaryFoldAfter(rest, { type: 'expand' }), shown);
  // Sending or opening another flow must not close something explicitly shown.
  assert.deepEqual(summaryFoldAfter(shown, { type: 'sent' }), shown);
  assert.deepEqual(summaryFoldAfter(shown, { type: 'folder', by: 'user' }), shown);

  // Reopening preserves the choice regardless of conversation hydration timing.
  assert.deepEqual(summaryFoldAfter(folded, { type: 'opened', conversationActive: false }), folded);
  assert.deepEqual(summaryFoldAfter(shown, { type: 'opened', conversationActive: false }), shown);
  assert.deepEqual(summaryFoldAfter(folded, { type: 'opened', conversationActive: true }), folded);
  assert.deepEqual(summaryFoldAfter(shown, { type: 'opened', conversationActive: true }), shown);

  assert.deepEqual(summaryFoldAfter(folded, { type: 'something-else' }), folded);
  assert.deepEqual(summaryFoldAfter(undefined, undefined), rest);
});

test('the summary strip says what it folded, and shows only when there is something', () => {
  assert.equal(
    summaryStripText({ needs: 2, brief: 'Brief ready', inProgress: 2, doneToday: 5 }),
    'Needs you 2 · Brief ready · 2 in progress'
  );
  // A part with nothing to say is left out.
  assert.equal(summaryStripText({ needs: 0, brief: 'Brief ready', inProgress: 0 }), 'Brief ready');
  assert.equal(summaryStripText({ needs: 1, brief: '', inProgress: 0 }), 'Needs you 1');
  // With nothing in progress, the row's other number is the one worth saying.
  assert.equal(
    summaryStripText({ brief: 'Brief failed', doneToday: 3 }),
    'Brief failed · 3 done today'
  );
  assert.equal(summaryStripText({ needs: -1, inProgress: 'x' }), '');
  assert.equal(summaryStripText(), '');
  assert.equal(summaryStripText({ unavailable: true }), 'Today sources unavailable');
  assert.equal(
    summaryStripView({ started: true, expanded: false }, summaryStripText({ unavailable: true }))
      .sectionsHidden,
    true
  );

  const text = 'Needs you 2';
  assert.deepEqual(summaryStripView({ started: true, expanded: false }, text), {
    visible: true,
    expanded: false,
    sectionsHidden: true,
    toggleLabel: 'Show'
  });
  assert.deepEqual(summaryStripView({ started: true, expanded: true }, text), {
    visible: true,
    expanded: true,
    sectionsHidden: false,
    toggleLabel: 'Hide'
  });
  // Before a conversation nothing is folded, so there is no strip.
  assert.equal(summaryStripView({ started: false, expanded: false }, text).visible, false);
  assert.equal(summaryStripView({ started: false, expanded: false }, text).sectionsHidden, false);
  // With nothing to summarise there is nothing to fold either.
  assert.equal(summaryStripView({ started: true, expanded: false }, '').visible, false);
  assert.equal(summaryStripView({ started: true, expanded: false }, '').sectionsHidden, false);
});

test('the summary strip is one control in the Today frame, wired to the sections it folds', () => {
  const template = readFileSync(
    new URL('../../../templates/components/personal-assistant-today.tmpl', import.meta.url),
    'utf8'
  );
  assert.match(
    template,
    /id="personalAssistantSummaryToggle"[^>]*aria-expanded="false"[^>]*aria-controls="personalAssistantTodaySections"/
  );
  // The strip comes before what it stands for.
  assert.ok(
    template.indexOf('id="personalAssistantSummary"') <
      template.indexOf('id="personalAssistantTodaySections"')
  );
});

test('Today view distinguishes active, paused, partial, no-model, empty, and fatal states', () => {
  assert.equal(personalAssistantTodayView({ state: 'active' }).active, true);
  assert.equal(personalAssistantTodayView({ state: 'paused' }).paused, true);
  assert.equal(personalAssistantTodayView({ state: 'partial' }).partial, true);
  assert.equal(personalAssistantTodayView({ state: 'model_unavailable' }).modelUnavailable, true);
  assert.equal(personalAssistantTodayView({ state: 'healthy_empty' }).active, true);
  assert.equal(personalAssistantTodayView({ state: 'unavailable' }).unavailable, true);
});

test('partial Today always names the limitation even without a source list or generated opening', () => {
  assert.match(partialTodaySummary({ state: 'partial' }), /sources are unavailable.*no all-clear/);
  assert.match(
    partialTodaySummary({ brief: { opening_summary: 'Prior records are still stored.' } }),
    /unavailable.*Prior records are still stored/
  );
  assert.match(
    summaryStripText({ brief: 'Brief ready', unavailable: true }),
    /Brief ready.*sources unavailable/
  );
});

test('Today distinguishes a hired assistant with no HQ from needs_hire and does not claim active/paused', () => {
  const view = personalAssistantTodayView({ state: 'needs_hq', display_name: 'Atlas' });
  assert.equal(view.needsHQ, true);
  assert.equal(view.needsHire, false);
  assert.equal(view.active, false);
  assert.equal(view.paused, false);
  assert.equal(view.partial, false);
  assert.equal(view.displayName, 'Atlas');
});

// Before the hire, Today is quiet: one sentence, one link, to Mission 01
// (meet-your-assistant FR8). No other call to action competes with it.
test('before the hire Today says only "Meet your assistant to start Today."', () => {
  const banner = needsHireBanner();
  assert.equal(`${banner.linkText}${banner.trail}`, 'Meet your assistant to start Today.');
  // The walkthrough from its first step, which points at the Agents nav entry.
  assert.equal(banner.href, '/?quest=meet-assistant');
  assert.deepEqual(Object.keys(banner).sort(), ['href', 'linkText', 'trail']);
});

test('Home never links to the retired /?hire=1', () => {
  const source = readFileSync(new URL('./personal-assistant-home.js', import.meta.url), 'utf8');
  assert.doesNotMatch(source, /hire=1/);
  assert.doesNotMatch(source, /Hire your personal assistant/);
});

test('launcher cues are textual, bounded, and derived only from canonical states', () => {
  assert.equal(personalAssistantLauncherCue({ state: 'active' }, null), 'Loading Today');
  assert.equal(
    personalAssistantLauncherCue({ state: 'active' }, { state: 'healthy_empty' }),
    'Today ready'
  );
  assert.equal(personalAssistantLauncherCue({ state: 'paused' }, { state: 'active' }), 'Paused');
  assert.equal(personalAssistantLauncherCue({ state: 'needs_hq' }, null), 'Build HQ');
  assert.equal(
    personalAssistantLauncherCue({ state: 'active' }, { state: 'partial' }),
    'Sources unavailable'
  );
  assert.equal(
    personalAssistantLauncherCue({ state: 'active' }, { state: 'unavailable' }),
    'Sources unavailable'
  );
  assert.equal(
    personalAssistantLauncherCue({ state: 'active' }, { state: 'model_unavailable' }),
    'Model unavailable'
  );
  assert.equal(personalAssistantLauncherCue({ state: 'repair_needed' }, null), 'Repair needed');
  assert.equal(personalAssistantLauncherCue(null, null), '');
});

test('only routine progress cues may rest on the compact Home launcher', () => {
  // Every cue the launcher can produce, classified. Anything that asks the
  // user to act must stay visible at rest (home-workspace-map-ui-refresh 3.5).
  const cues = [
    [{ state: 'active' }, null],
    [{ state: 'active' }, { state: 'healthy_empty' }],
    [{ state: 'active' }, { state: 'active' }],
    [{ state: 'paused' }, null],
    [{ state: 'needs_hq' }, null],
    [{ state: 'provisioning_hq' }, null],
    [{ state: 'repair_needed' }, null],
    [{ state: 'active' }, { state: 'partial' }],
    [{ state: 'active' }, { state: 'model_unavailable' }]
  ].map(([relationship, today]) => personalAssistantLauncherCue(relationship, today));
  const tones = Object.fromEntries(cues.map(cue => [cue, personalAssistantLauncherCueTone(cue)]));
  assert.deepEqual(tones, {
    'Loading Today': 'info',
    'Today ready': 'info',
    Paused: 'action',
    'Build HQ': 'action',
    'Repair needed': 'action',
    'Sources unavailable': 'action',
    'Model unavailable': 'action'
  });
  assert.equal(personalAssistantLauncherCueTone(''), '');
  // An unknown future cue defaults to visible rather than silently hidden.
  assert.equal(personalAssistantLauncherCueTone('Something new'), 'action');
});

test('reviewed Today recap distinguishes current fact links, existing next actions and unavailable sources', () => {
  const rows = todaySectionRows({
    health: { status: 'available' },
    items: [
      {
        kind: 'reviewed_memory',
        title: 'Finish my portfolio',
        route: '/profile#personalHQKnowledge'
      },
      {
        kind: 'existing_next_action',
        title: 'Next from Today: Morning briefing',
        route: '/workspaces/my-hq?ticket=123'
      }
    ]
  });
  assert.equal(rows.length, 2);
  assert.equal(rows[0].title, 'Finish my portfolio');
  assert.equal(rows[0].route, '/profile#personalHQKnowledge');
  assert.equal(rows[1].route, '/workspaces/my-hq?ticket=123');
  assert.match(
    todaySectionRows({ health: { status: 'unavailable' }, items: [] })[0].title,
    /unavailable/i
  );
});

test('Today section never turns unavailable into a healthy empty all-clear', () => {
  assert.match(
    todaySectionRows({ health: { status: 'unavailable' }, items: [] })[0].title,
    /unavailable/
  );
  assert.match(
    todaySectionRows({ health: { status: 'healthy_empty' }, items: [] })[0].title,
    /Nothing here/
  );
});

test('partial follow-up sections retain healthy Email Ops rows with a source warning', () => {
  const rows = todaySectionRows({
    health: { status: 'partial', reason: 'some_sources_unavailable' },
    items: [
      {
        kind: 'follow_up',
        title: 'Waiting for Alex’s signed agreement',
        route: '/workspaces/email-ops?follow_up=follow-1'
      },
      {
        kind: 'follow_up',
        title: 'Unsafe owner route',
        route: '//evil.example/workspaces/email-ops?follow_up=follow-2'
      }
    ]
  });
  assert.equal(rows.length, 3);
  assert.equal(rows[0].kind, 'status');
  assert.match(rows[0].title, /Some sources are unavailable/i);
  assert.equal(rows[1].route, '/workspaces/email-ops?follow_up=follow-1');
  assert.equal(rows[2].route, '');
});

test('Today section caps records and only preserves server-owned internal routes', () => {
  const items = Array.from({ length: 15 }, (_, index) => ({
    kind: 'ticket',
    title: `Ticket ${index}`,
    route: `/workspaces/personal-hq?ticket=${index}`
  }));
  const rows = todaySectionRows({ health: { status: 'available' }, items });
  assert.equal(rows.length, 10);
  assert.ok(rows.every(row => safeTodayRoute(row.route)));

  for (const route of [
    'https://evil.example',
    '//evil.example',
    'javascript:alert(1)',
    'workspaces/x',
    '/workspaces/../settings',
    '/workspaces/%2e%2e/settings',
    '/workspaces/email-ops\\settings',
    '/workspaces/%zz'
  ]) {
    assert.equal(safeTodayRoute(route), false, route);
  }
});

test('Today rows carry who did the work, by name, only when the record says so', () => {
  const rows = todaySectionRows({
    health: { status: 'available' },
    items: [
      { title: 'Rough mix of Ivory', attribution: 'Reaper Producer', route: '/workspaces/ivory' },
      { title: 'Archived the stems', route: '/workspaces/ivory' }
    ]
  });
  assert.equal(rows[0].attribution, 'Reaper Producer');
  assert.equal(rows[1].attribution, '');
});

test('the studio section is absent unless the server reports one', () => {
  for (const studio of [null, undefined]) {
    assert.equal(studioSectionView(studio).visible, false);
  }
});

test('the studio section reports and points at the specialist without claiming to direct it', () => {
  const view = studioSectionView({
    health: { status: 'available' },
    domain: 'music projects',
    specialist_name: 'Reaper Producer',
    workspace_name: 'Ivory',
    route: '/workspaces/ivory',
    items: [{ title: 'Rough mix', attribution: 'Reaper Producer' }]
  });

  assert.equal(view.visible, true);
  assert.equal(view.heading, 'From Ivory');
  assert.equal(view.route, '/workspaces/ivory');
  assert.equal(view.linkLabel, 'Open Ivory');
  // Addressing the specialist directly is offered plainly, and nothing claims
  // the assistant can hand it work — it cannot.
  assert.match(view.note, /ask Reaper Producer directly/i);
  assert.doesNotMatch(view.note, /assign|delegate|instruct|hand off|on your behalf/i);
  assert.doesNotMatch(view.note, /instead|workaround|advanced|fall ?back/i);
  assert.equal(view.section.items.length, 1);
});

test('the studio section degrades safely without a name, a workspace, or a usable route', () => {
  const noRoute = studioSectionView({
    health: { status: 'unavailable' },
    specialist_name: 'Reaper Producer',
    route: 'https://evil.example',
    items: []
  });
  assert.equal(noRoute.route, '');
  assert.match(noRoute.note, /works in its own workspace/i);

  const bare = studioSectionView({ health: { status: 'available' }, items: [] });
  assert.equal(bare.visible, true);
  assert.equal(bare.heading, 'From your studio');
  assert.equal(bare.linkLabel, 'Open the workspace');
  assert.match(bare.note, /your specialist/i);
});

test('specialist setup reports exact project and child status with closed actions', () => {
  const view = specialistSetupView({
    health: { status: 'available' },
    title: 'Set up a domain',
    lifecycle: 'ready',
    connected_project_count: 2,
    child_run_count: 1,
    unfinished_child_count: 1,
    runs: [
      { run_kind: 'root', lifecycle: 'ready', project_name: 'First project' },
      { run_kind: 'child', lifecycle: 'needs_attention', project_name: 'Second project' }
    ],
    sample_library: {
      state: 'active',
      active_root_count: 2,
      indexed_root_count: 1
    },
    actions: [
      { id: 'review_setup', label: 'Review setup' },
      { id: 'connect_another', label: 'Connect another project' },
      { id: 'open_home', label: 'Open Home', route: '/workspaces/home/assistant' },
      { id: 'open_project', label: 'Unsafe', route: 'https://evil.example' },
      { id: 'made_up', label: 'Made up action', route: '/plugins' }
    ]
  });
  assert.equal(view.visible, true);
  assert.match(view.status, /2 connected projects/i);
  assert.match(view.status, /1 later setup needs attention/i);
  assert.deepEqual(view.runs, ['First project — ready', 'Second project — Needs attention']);
  assert.match(view.sampleStatus, /2 approved folders, 1 indexed/i);
  assert.deepEqual(
    view.actions.map(action => action.id),
    ['review_setup', 'connect_another', 'open_home']
  );
});

test('specialist setup keeps unavailable distinct from an empty setup', () => {
  assert.equal(specialistSetupView(null).visible, false);
  const unavailable = specialistSetupView({
    health: { status: 'unavailable' },
    lifecycle: 'not_started',
    connected_project_count: 0,
    actions: []
  });
  assert.match(unavailable.status, /temporarily unavailable/i);
  assert.doesNotMatch(unavailable.status, /ready/i);
});

test('the meetings section is absent unless the server reports one', () => {
  for (const meetings of [undefined, null, 'ready']) {
    assert.equal(meetingsSectionView(meetings).visible, false);
  }
});

test('a calendar that is not connected is a nudge to Mission 04, never an empty agenda', () => {
  const view = meetingsSectionView({
    state: 'not_connected',
    health: { status: 'healthy_empty', reason: 'not_connected' },
    setup_route: '/?create=1&blueprint=calendar-ops',
    setup_label: 'Connect your calendar',
    items: []
  });
  assert.equal(view.visible, true);
  assert.deepEqual(view.rows, []);
  assert.match(view.note, /Connect a calendar/);
  assert.equal(view.noteRoute, '/?create=1&blueprint=calendar-ops');
  assert.equal(view.noteLinkLabel, 'Connect your calendar');
});

test('a connection that needs setup routes to the workspace that owns it', () => {
  const view = meetingsSectionView({
    state: 'needs_setup',
    health: { status: 'healthy_empty', reason: 'connection_not_ready' },
    setup_route: '/workspaces/calendar-ops?panel=calendar',
    setup_label: 'Finish Calendar Ops setup'
  });
  assert.match(view.note, /needs attention/);
  assert.equal(view.noteRoute, '/workspaces/calendar-ops?panel=calendar');
  assert.equal(view.noteLinkLabel, 'Finish Calendar Ops setup');
});

test('ready meetings render as rows with badges, counts, and the console route', () => {
  const view = meetingsSectionView({
    state: 'ready',
    health: { status: 'available' },
    route: '/workspaces/calendar-ops?panel=calendar',
    conflict_count: 2,
    back_to_back_count: 1,
    more_count: 3,
    items: [
      {
        id: 'design',
        title: 'Design review',
        detail: '11:00 AM–12:00 PM',
        state: 'conflict',
        route: '/workspaces/calendar-ops?calendar=primary&event=design&panel=calendar'
      },
      { id: 'standup', title: 'Standup', detail: '9:00–9:30 AM · Room 2', state: 'needs_prep' },
      { id: 'lunch', title: 'Lunch', state: 'back_to_back' },
      { id: 'prepped', title: 'Board prep', state: 'prep_ready' },
      { id: 'running', title: 'Hiring sync', state: 'prep_pending' },
      { id: 'private', title: 'Private event', state: '' },
      { id: 'odd', title: 'Odd', state: 'toString' }
    ]
  });
  assert.equal(view.heading, 'Meetings · 2 overlaps · 1 back-to-back');
  assert.deepEqual(
    view.rows.map(row => row.badge),
    ['Overlaps', 'Prepare', 'Back-to-back', 'Prep ready', 'Preparing…', '', '']
  );
  assert.equal(
    view.rows[0].route,
    '/workspaces/calendar-ops?calendar=primary&event=design&panel=calendar'
  );
  assert.equal(view.rows[1].detail, '9:00–9:30 AM · Room 2');
  assert.equal(view.note, '3 more meetings today.');
  assert.equal(view.noteRoute, '/workspaces/calendar-ops?panel=calendar');
  assert.equal(view.noteLinkLabel, 'Open calendar');
  assert.equal(Object.isFrozen(MEETING_BADGES), true);
});

test('a ready calendar with nothing today says so plainly', () => {
  const view = meetingsSectionView({
    state: 'ready',
    health: { status: 'healthy_empty' },
    route: '/workspaces/calendar-ops?panel=calendar',
    items: []
  });
  assert.deepEqual(
    view.rows.map(row => [row.kind, row.title]),
    [['status', 'Nothing scheduled today.']]
  );
  assert.equal(view.heading, 'Meetings');
  assert.equal(view.note, '');
});

test('an unreadable calendar is named, never an empty day', () => {
  const failed = meetingsSectionView({
    state: 'unavailable',
    health: { status: 'unavailable', reason: 'read_failed' },
    items: []
  });
  assert.equal(failed.rows.length, 1);
  assert.equal(failed.rows[0].kind, 'status');
  assert.match(failed.rows[0].title, /could not be read/);
  assert.doesNotMatch(failed.rows[0].title, /Nothing scheduled/);

  const timedOut = meetingsSectionView({
    state: 'unavailable',
    health: { status: 'unavailable', reason: 'timed_out' }
  });
  assert.match(timedOut.rows[0].title, /did not answer in time/);
});

test('some calendars failing keeps the readable meetings behind a warning', () => {
  const view = meetingsSectionView({
    state: 'ready',
    health: { status: 'partial', reason: 'some_calendars_unavailable' },
    items: [{ id: 'standup', title: 'Standup', detail: '9:00–9:30 AM', state: '' }]
  });
  assert.deepEqual(
    view.rows.map(row => row.kind),
    ['status', 'meeting']
  );
  assert.match(view.rows[0].title, /Some calendars could not be read/);
});

test('meeting routes that leave the app are dropped', () => {
  const view = meetingsSectionView({
    state: 'ready',
    health: { status: 'available' },
    route: 'https://evil.example/calendar',
    setup_route: '//evil.example',
    items: [
      { id: 'a', title: 'A', route: 'https://evil.example/a' },
      { id: 'b', title: 'B', route: '//evil.example/b' },
      { id: 'c', title: 'C', route: 'javascript:alert(1)' }
    ]
  });
  assert.deepEqual(
    view.rows.map(row => row.route),
    ['', '', '']
  );
  assert.equal(view.noteRoute, '');
  assert.equal(view.noteLinkLabel, '');

  const nudge = meetingsSectionView({
    state: 'not_connected',
    health: { status: 'healthy_empty' },
    setup_route: 'https://evil.example/create',
    setup_label: 'Connect your calendar'
  });
  assert.equal(nudge.noteRoute, '');
  assert.equal(nudge.noteLinkLabel, '');
});
