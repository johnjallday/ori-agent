import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

import {
  folderActionAvailable,
  folderChipBusy,
  folderDecisionProgress,
  folderChooserView,
  folderOfferWaiting,
  folderPinnedOffer,
  folderPlacement,
  folderSceneView,
  firstFolderPromptView,
  folderOfferView,
  folderOutcomeNote,
  folderReceiptView,
  folderFirstLookOpen,
  folderFirstLookView,
  firstLookStartMessage,
  FIRST_LOOK_HINT,
  folderProjectModalOptions,
  portfolioLibraryURL,
  portfolioProviderAction,
  profileReceiptLine,
  setupModalView
} from './personal-assistant-folder.js';

test('setup busy text names the reviewed project, not its observation root', () => {
  const offer = { folder: 'Documents', subject: { name: 'Album-5' } };
  assert.equal(
    folderDecisionProgress(offer, { create: true }),
    'Setting up the workspace for Album-5…'
  );
  assert.equal(
    folderDecisionProgress(offer, { walkthrough: true }),
    'Setting up File Janitor for Album-5…'
  );
  assert.equal(folderDecisionProgress(offer, {}), '');
  assert.equal(
    folderDecisionProgress({ folder: 'Documents' }, { create: true }),
    'Setting up the workspace for Documents…'
  );
});

const runLines = states =>
  states.map((state, index) => ({ kind: 'other', name: `Step ${index + 1}`, detail: '', state }));

test('the run pop-up says the assistant is working and counts finished steps', () => {
  const view = setupModalView({
    status: 'awaiting_outcome',
    subject: { name: 'Session' },
    capability: { recognized: 'REAPER', workspace: 'song workspace' },
    setup: {
      status: 'running',
      lines: runLines(['done', 'done', 'working', 'waiting'])
    }
  });
  assert.equal(view.visible, true);
  assert.equal(view.phase, 'running');
  assert.equal(view.eyebrow, 'Your assistant is working');
  assert.equal(view.title, 'Setting up Session');
  assert.equal(view.count, '2 of 4 steps finished');
  assert.equal(view.percent, 50);
  assert.equal(view.status, 'Working on: Step 3');
  assert.deepEqual(view.actions, []);
});

test('a stopped run keeps its progress and offers the way forward in the pop-up', () => {
  const view = setupModalView({
    status: 'awaiting_outcome',
    subject: { name: 'Session' },
    setup: {
      status: 'stopped',
      stop_reason: 'needs_model',
      lines: runLines(['done', 'waiting'])
    }
  });
  assert.equal(view.phase, 'stopped');
  assert.equal(view.title, 'Session needs you');
  assert.equal(view.percent, 50);
  assert.match(view.status, /needs a model/);
  assert.doesNotMatch(JSON.stringify(view), /needs_model/);
  assert.deepEqual(
    view.actions.map(action => action.label),
    ['Set up a model', 'Try again', 'Continue setup']
  );
});

test('a finished run shows the receipt and the Open link in the pop-up', () => {
  const view = setupModalView({
    status: 'resolved',
    subject: { name: 'Session' },
    setup: { status: 'done', lines: runLines(['done', 'done']) },
    outcome: {
      kind: 'project',
      route: '/workspaces/session',
      receipt: [{ kind: 'workspace', name: 'Session' }]
    }
  });
  assert.equal(view.phase, 'done');
  assert.equal(view.percent, 100);
  assert.equal(view.route, '/workspaces/session');
  assert.equal(view.openLabel, 'Open Session');
  assert.equal(view.receiptRows.length, 1);
});

test('a settled offer has dropped its run but the pop-up still ends on the receipt', () => {
  const view = setupModalView({
    status: 'resolved',
    subject: { name: 'Session' },
    outcome: {
      kind: 'project',
      route: '/workspaces/session',
      receipt: [{ kind: 'workspace', name: 'Session' }]
    }
  });
  assert.equal(view.visible, true);
  assert.equal(view.phase, 'done');
  assert.equal(view.openLabel, 'Open Session');
});

test('the run pop-up is not shown without a one-card run', () => {
  assert.equal(setupModalView(null).visible, false);
  assert.equal(setupModalView({ status: 'pending', plan: { lines: [] } }).visible, false);
  assert.equal(setupModalView({ portfolio: { projects: 3 } }).visible, false);
});

test('a collection run shows in the pop-up and ends on its Home receipt', () => {
  const running = setupModalView({
    status: 'awaiting_outcome',
    subject: { name: 'Songs' },
    portfolio: { projects: 200 },
    setup: { status: 'running', lines: runLines(['done', 'working', 'waiting']) }
  });
  assert.equal(running.visible, true);
  assert.equal(running.phase, 'running');
  assert.equal(running.title, 'Setting up Songs');
  const done = setupModalView({
    status: 'resolved',
    subject: { name: 'Songs' },
    portfolio: { projects: 200 },
    outcome: {
      kind: 'home',
      route: '/workspaces/music-home',
      receipt: [
        {
          kind: 'home',
          name: 'Music Production Home',
          detail: 'created',
          route: '/workspaces/music-home/assistant#projectLibraryPanel'
        },
        { kind: 'library', name: 'Listed 200 music projects in Songs' }
      ]
    }
  });
  assert.equal(done.phase, 'done');
  assert.equal(done.title, 'Music Production Home is ready');
  assert.match(done.status, /Open a project from the library/);
  // Open lands on the library, with no folder handoff to start again.
  assert.equal(done.route, '/workspaces/music-home/assistant#projectLibraryPanel');
  assert.equal(done.openLabel, 'Open Music Production Home');
  assert.equal(done.receiptRows.length, 2);
});

const homeRunOffer = (outcome = {}) => ({
  id: 'offer-1',
  status: 'resolved',
  subject: { name: 'Documents' },
  portfolio: { projects: 12 },
  setup: { status: 'done', lines: runLines(['done', 'done', 'done']) },
  outcome: {
    kind: 'home',
    workspace_id: 'home-1',
    route: '/workspaces/music-home',
    receipt: [
      {
        kind: 'home',
        name: 'Music Production Home',
        detail: 'created',
        route: '/workspaces/music-home/assistant#projectLibraryPanel'
      },
      { kind: 'library', name: 'Listed 12 music projects in Documents' }
    ],
    ...outcome
  }
});

// Local calendar times, so the labels hold in any time zone the tests run in.
const today = new Date(2026, 9, 2, 15, 0);
const savedDaysAgo = days =>
  new Date(today.getFullYear(), today.getMonth(), today.getDate() - days, 11).toISOString();
const songRows = count =>
  Array.from({ length: count }, (_, index) => ({
    id: `entry-${index + 1}`,
    name: `Song ${String(index + 1).padStart(3, '0')}`,
    connection: 'catalog_only',
    can_open: true,
    last_saved_at: savedDaysAgo(index)
  }));

test('the last screen says one quiet line about the Home profile, with a Review link', () => {
  const profileRow = (extra = {}) => ({
    kind: 'profile',
    name: 'Your studio',
    detail: 'REAPER · 4 templates',
    route: '/workspaces/music-home/assistant#homeProfilePanel',
    ...extra
  });
  const withProfile = row =>
    homeRunOffer({ receipt: [...homeRunOffer().outcome.receipt, ...(row ? [row] : [])] });

  // On "Pick a song to start with" and on the plain receipt screen alike.
  const songs = setupModalView(withProfile(profileRow()), {
    songs: songRows(3),
    total: 12,
    now: today
  });
  assert.equal(songs.title, 'Pick a song to start with');
  assert.deepEqual(songs.profile, {
    text: 'Your studio: REAPER · 4 templates',
    route: '/workspaces/music-home/assistant#homeProfilePanel',
    linkLabel: 'Review'
  });
  const plain = setupModalView(
    withProfile(profileRow({ detail: 'REAPER and Logic Pro · pick your main DAW' }))
  );
  assert.equal(plain.profile.text, 'Your studio: REAPER and Logic Pro · pick your main DAW');
  // It blocks nothing: the status, the songs and the Open are unchanged.
  const without = setupModalView(homeRunOffer(), { songs: songRows(3), total: 12, now: today });
  assert.equal(without.profile, null);
  assert.equal(songs.status, without.status);
  assert.equal(songs.route, without.route);
  assert.deepEqual(songs.songs, without.songs);

  // A route that is not the Home's profile card is never linked.
  for (const route of [
    '/agents',
    'https://example.com/',
    '/workspaces/music-home/assistant#projectLibraryPanel',
    ''
  ]) {
    assert.equal(profileReceiptLine([profileRow({ route })]).route, '', route);
  }
  // No profile row, or one with nothing to say, shows no line.
  assert.equal(profileReceiptLine([]), null);
  assert.equal(profileReceiptLine([profileRow({ detail: '' })]), null);
  assert.equal(profileReceiptLine(null), null);
  // A run that has not finished shows no line.
  const running = setupModalView({
    status: 'awaiting_outcome',
    subject: { name: 'Songs' },
    portfolio: { projects: 200 },
    setup: { status: 'running', lines: runLines(['done', 'working', 'waiting']) }
  });
  assert.equal(running.profile, undefined);
});

test('a step that did not finish still says so on the last screen', () => {
  // The Home's profile step may fail without stopping the run. The run is
  // over and the Home is ready, but that line is not turned into "done".
  const offer = homeRunOffer();
  offer.setup.lines = runLines(['done', 'failed', 'done']);
  const view = setupModalView(offer);
  assert.equal(view.phase, 'done');
  assert.equal(view.title, 'Music Production Home is ready');
  assert.deepEqual(
    view.lines.map(line => line.state),
    ['done', 'failed', 'done']
  );
  assert.equal(view.count, '2 of 3 steps finished');
  // A run where every step finished reads as before.
  const clean = setupModalView(homeRunOffer());
  assert.deepEqual(
    clean.lines.map(line => line.state),
    ['done', 'done', 'done']
  );
  assert.equal(clean.count, '3 of 3 steps finished');
});

test('the Home receipt names the Home by the workspace ID in the outcome', () => {
  assert.equal(folderReceiptView(homeRunOffer()).homeID, 'home-1');
  // Never parsed out of a route.
  assert.equal(folderReceiptView(homeRunOffer({ workspace_id: undefined })).homeID, '');
});

test('a finished Home run ends on the six songs saved most recently', () => {
  const rows = [
    { id: 'gone', name: 'Unsupported', can_open: false, last_saved_at: savedDaysAgo(0) },
    { id: 'undated', name: 'Undated', can_open: true },
    ...songRows(8)
  ];
  const view = setupModalView(homeRunOffer(), { songs: rows, total: 12, now: today });
  assert.equal(view.visible, true);
  assert.equal(view.phase, 'done');
  assert.equal(view.eyebrow, 'All set');
  assert.equal(view.title, 'Pick a song to start with');
  assert.equal(
    view.status,
    'Music Production Home is ready with 12 songs. These are the ones you saved most recently.'
  );
  assert.deepEqual(
    view.songs.map(song => [song.name, song.saved]),
    [
      ['Song 001', 'Saved today'],
      ['Song 002', 'Saved yesterday'],
      ['Song 003', 'Saved 2 days ago'],
      ['Song 004', 'Saved 3 days ago'],
      ['Song 005', 'Saved 4 days ago'],
      ['Song 006', 'Saved 5 days ago']
    ]
  );
  assert.equal(view.songs[0].id, 'entry-1');
  assert.equal(
    view.songs[0].ariaLabel,
    'Open Song 001: makes its workspace and adds your assistant'
  );
  // What was set up is still there (drawn folded), and the link browses the library.
  assert.equal(view.receiptRows.length, 2);
  assert.equal(view.route, '/workspaces/music-home/assistant#projectLibraryPanel');
  assert.equal(view.openLabel, 'Browse all 12 songs');
  assert.doesNotMatch(JSON.stringify(view), /last_saved_at|0001-01-01/);
});

test('with no song to open, the done screen is the one it always was', () => {
  const offer = homeRunOffer();
  const before = setupModalView(offer);
  assert.equal(before.title, 'Music Production Home is ready');
  assert.equal(before.openLabel, 'Open Music Production Home');
  assert.equal(before.songs, undefined);
  for (const [name, options] of [
    ['the list was never read (or the read failed)', {}],
    ['the Home has no songs', { songs: [], total: 0 }],
    [
      'no song can be opened here',
      { songs: songRows(3).map(row => ({ ...row, can_open: false })), total: 3 }
    ],
    [
      'no song has a save time',
      {
        songs: songRows(3).map(row => ({ ...row, last_saved_at: '0001-01-01T00:00:00Z' })),
        total: 3
      }
    ]
  ]) {
    assert.deepEqual(setupModalView(offer, { ...options, now: today }), before, name);
  }
});

test('songs are only for a finished Home run', () => {
  const songs = songRows(3);
  // A single song set up through the card ends on its own workspace.
  const project = setupModalView(
    {
      status: 'resolved',
      subject: { name: 'Session' },
      setup: { status: 'done', lines: runLines(['done']) },
      outcome: {
        kind: 'project',
        route: '/workspaces/session',
        receipt: [{ kind: 'workspace', name: 'Session' }]
      }
    },
    { songs, total: 3 }
  );
  assert.equal(project.title, 'Session is ready');
  assert.equal(project.songs, undefined);
  // A Home run still going (or stopped) shows its steps, never a list.
  for (const status of ['running', 'stopped']) {
    const run = setupModalView(
      {
        status: 'awaiting_outcome',
        subject: { name: 'Documents' },
        portfolio: { projects: 3 },
        setup: { status, stop_reason: 'failed', lines: runLines(['done', 'waiting']) }
      },
      { songs, total: 3 }
    );
    assert.equal(run.phase, status);
    assert.equal(run.songs, undefined);
  }
});

test('each song on the last screen carries its facts, and a song without facts none', () => {
  const rows = songRows(3);
  rows[0].facts = { track_count: 14, tempo_bpm: 92, tempo_varies: true, length_seconds: 221 };
  rows[1].facts = { track_count: 1 };
  const view = setupModalView(homeRunOffer(), { songs: rows, total: 3, now: today });
  assert.deepEqual(
    view.songs.map(song => [song.name, song.saved, song.facts]),
    [
      ['Song 001', 'Saved today', '14 tracks · 92 BPM, varies · 3:41'],
      ['Song 002', 'Saved yesterday', '1 track'],
      ['Song 003', 'Saved 2 days ago', '']
    ]
  );
  assert.doesNotMatch(JSON.stringify(view.songs), /tempo_bpm|track_count|unknown|progress/);
});

test('one song is counted as one song', () => {
  const view = setupModalView(homeRunOffer(), { songs: songRows(1), total: 1, now: today });
  assert.match(view.status, /is ready with 1 song\./);
  assert.equal(view.openLabel, 'Browse all 1 song');
});

test('a collection resolved step by step has no receipt and keeps its note', () => {
  const offer = {
    status: 'resolved',
    subject: { name: 'Songs' },
    portfolio: { projects: 6 },
    outcome: { kind: 'home', route: '/workspaces/music-home' }
  };
  assert.equal(folderReceiptView(offer).visible, false);
  // A Home row must link to that Home's library, nowhere else.
  offer.outcome.receipt = [{ kind: 'home', name: 'Home', route: '/agents' }];
  assert.equal(folderReceiptView(offer).visible, false);
});

test('an older installed reviewed Home provider is offered as an update', () => {
  assert.equal(
    portfolioProviderAction({
      installed: true,
      update: true,
      installed_version: '0.1.0',
      version: '0.1.1'
    }),
    'update the installed provider from 0.1.0 to 0.1.1'
  );
  assert.equal(portfolioProviderAction({ installed: true }), 'enable the installed provider');
  assert.equal(portfolioProviderAction({}), 'install and enable the reviewed provider');
});

const headlineText = view => view.headline.map(part => part.text).join('');
const strongText = view => view.headline.filter(part => part.strong).map(part => part.text);

test('portfolio continuation carries only a canonical Home route and opaque offer ID', () => {
  assert.equal(
    portfolioLibraryURL({
      id: 'offer-123',
      status: 'resolved',
      outcome: { kind: 'home', route: '/workspaces/music-home' }
    }),
    '/workspaces/music-home/assistant?folder_offer_id=offer-123#projectLibraryPanel'
  );
  assert.equal(
    portfolioLibraryURL({
      id: 'offer-123',
      status: 'awaiting_outcome',
      outcome: { kind: 'home', route: '/workspaces/music-home' }
    }),
    ''
  );
  assert.equal(
    portfolioLibraryURL({
      id: 'offer-123',
      status: 'resolved',
      outcome: { kind: 'home', route: '//external' }
    }),
    ''
  );
  assert.equal(
    portfolioLibraryURL({
      id: 'offer-123',
      status: 'resolved',
      outcome: { kind: 'project', route: '/workspaces/song' }
    }),
    ''
  );
});

test('the action is offered only to an active or paused assistant', () => {
  assert.equal(folderActionAvailable({ state: 'active' }), true);
  assert.equal(folderActionAvailable({ state: 'paused' }), true);
  for (const state of [
    'needs_hire',
    'hiring',
    'needs_hq',
    'provisioning_hq',
    'repair_needed',
    '',
    undefined
  ]) {
    assert.equal(folderActionAvailable({ state }), false, state);
  }
});

test('resolved project receipt names only server rows and offers the canonical workspace route', () => {
  const offer = {
    status: 'resolved',
    verdict: 'project',
    subject: { name: 'Draft' },
    outcome: {
      kind: 'project',
      route: '/workspaces/thesis',
      receipt: [
        { kind: 'workspace', name: '<Thesis>', route: '/workspaces/thesis' },
        { kind: 'folder', name: 'Draft', detail: 'linked as primary' },
        { kind: 'task', name: 'Summarize current draft' }
      ]
    }
  };
  assert.deepEqual(folderReceiptView(offer), {
    visible: true,
    rows: [
      { kind: 'workspace', name: '<Thesis>', detail: '' },
      { kind: 'folder', name: 'Draft', detail: 'linked as primary' },
      { kind: 'task', name: 'Summarize current draft', detail: '' }
    ],
    route: '/workspaces/thesis',
    homeRoute: '',
    openLabel: 'Open <Thesis>'
  });
  assert.equal(folderOfferView(offer).question, "Here's what I set up:");
  assert.equal(
    folderReceiptView({ ...offer, outcome: { ...offer.outcome, route: '//evil' } }).route,
    ''
  );
  assert.equal(
    folderReceiptView({
      ...offer,
      outcome: {
        ...offer.outcome,
        home_route: '/workspaces/music-home/assistant#projectLibraryPanel'
      }
    }).homeRoute,
    '/workspaces/music-home/assistant#projectLibraryPanel'
  );
  assert.equal(
    folderReceiptView({
      ...offer,
      outcome: { ...offer.outcome, home_route: '//evil/assistant#projectLibraryPanel' }
    }).homeRoute,
    ''
  );
  assert.equal(folderReceiptView({ ...offer, outcome: { kind: 'tidy' } }).visible, false);
});

test('a verified plugin quest offers its Home review route without a legacy folder-link receipt', () => {
  const offer = {
    status: 'resolved',
    verdict: 'project',
    subject: { name: 'Song' },
    capability: { setup_source: 'plugin' },
    outcome: {
      kind: 'project',
      route: '/workspaces/single-song',
      home_route: '/workspaces/music-home/assistant#projectLibraryPanel'
    }
  };
  assert.deepEqual(folderReceiptView(offer), {
    visible: true,
    rows: [],
    route: '/workspaces/single-song',
    homeRoute: '/workspaces/music-home/assistant#projectLibraryPanel',
    openLabel: 'Open Song'
  });
  assert.equal(folderOfferView(offer).question, "Here's what I set up:");
  assert.equal(
    folderOfferView({
      ...offer,
      capability: { ...offer.capability, integration: 'Setup will install REAPER' }
    }).capabilityDetail,
    ''
  );
  assert.equal(folderReceiptView({ ...offer, capability: undefined }).visible, false);
  assert.equal(
    folderReceiptView({ ...offer, outcome: { ...offer.outcome, route: '//evil' } }).visible,
    false
  );
  assert.equal(
    folderReceiptView({ ...offer, outcome: { ...offer.outcome, home_route: '//evil' } }).visible,
    false
  );
});

test('the first-folder hand-over is an active-only server receipt, never a pre-HQ prompt', () => {
  assert.deepEqual(firstFolderPromptView({ prompt_first_folder: true }, true), {
    expand: true,
    line: "Now let's explore a folder you're working in."
  });
  assert.equal(firstFolderPromptView({ prompt_first_folder: true }, false).expand, false);
  assert.equal(firstFolderPromptView({ prompt_first_folder: false }, true).expand, false);
});

test('the chooser renders the chips the server sent and hides the picker when it is unavailable', () => {
  const view = folderChooserView({
    chips: [
      { id: 'downloads', label: 'Downloads' },
      { id: 'documents', label: 'Documents' }
    ],
    picker_available: false,
    picker_note: 'Pick a folder from the list for now.'
  });
  assert.deepEqual(
    view.chips.map(chip => chip.id),
    ['downloads', 'documents']
  );
  assert.equal(view.pickerVisible, false);
  assert.equal(view.filePickerVisible, false);
  assert.equal(view.note, 'Pick a folder from the list for now.');

  const withPicker = folderChooserView({
    chips: [{ id: 'desktop', label: 'Desktop' }],
    picker_available: true,
    file_picker_available: true
  });
  assert.equal(withPicker.pickerVisible, true);
  assert.equal(withPicker.filePickerVisible, true);
  assert.equal(withPicker.filePickerLabel, 'Pick a file…');
  assert.equal(withPicker.note, '');
  assert.equal(withPicker.pickerLabel, 'Pick another folder…');
  assert.deepEqual(folderChooserView(null).chips, []);
});

// A chooser with nothing to press must say so, even on a payload that
// forgot its note; the server's own note always wins.
test('the chooser explains itself when there is nothing to choose', () => {
  const explained = folderChooserView({
    chips: [],
    picker_available: false,
    picker_note:
      'Downloads, Documents and Desktop are not under this home, and the folder dialog is switched off in this session (ORI_NO_DESKTOP_OPEN).'
  });
  assert.deepEqual(explained.chips, []);
  assert.equal(explained.pickerVisible, false);
  assert.equal(explained.filePickerVisible, false);
  assert.match(explained.note, /not under this home, and the folder dialog is switched off/);

  const bare = folderChooserView({ chips: [], picker_available: false });
  assert.equal(bare.note, 'No folder can be chosen here.');
  assert.equal(folderChooserView(null).note, 'No folder can be chosen here.');

  const pickerOnly = folderChooserView({
    chips: [],
    picker_available: true,
    picker_note: 'Downloads, Documents and Desktop are not under this home; pick another folder.'
  });
  assert.equal(pickerOnly.pickerVisible, true);
  assert.match(pickerOnly.note, /pick another folder/);
});

test('the folder field trip mirrors real scan state and only server-observed counts', () => {
  // While a folder is still being chosen the chooser is the assistant's whole
  // reply: the scene is the reply that follows, once there is a folder.
  assert.deepEqual(folderSceneView(), { visible: false, phase: 'choosing', label: '', finds: [] });
  assert.deepEqual(folderSceneView({ scanning: true, scanName: 'Documents' }), {
    visible: true,
    phase: 'scanning',
    label: 'Exploring Documents…',
    finds: []
  });
  const offer = {
    verdict: 'mixed',
    status: 'pending',
    folder: '<Documents>',
    projects_count: 3,
    loose_files: 40,
    blueprint_label: 'Proposed blueprint'
  };
  assert.deepEqual(folderSceneView({ offer }), {
    visible: true,
    phase: 'found',
    label: "Here's what I noticed in <Documents>.",
    finds: ['3 projects', '40 loose files']
  });
  assert.deepEqual(folderSceneView({ offer, failed: true }), {
    visible: true,
    phase: 'error',
    label: 'I could not explore that folder.',
    finds: []
  });
  assert.deepEqual(
    folderSceneView({ offer: { ...offer, status: 'resolved' } }).label,
    '<Documents> is ready.'
  );
  assert.deepEqual(
    folderSceneView({ offer: { ...offer, projects_count: '3', loose_files: -1 } }).finds,
    []
  );
  assert.deepEqual(
    folderSceneView({
      offer: {
        verdict: 'project',
        status: 'pending',
        folder: 'Thesis',
        subject: { marker: 'LaTeX manuscript' }
      }
    }).finds,
    ['LaTeX manuscript']
  );
});

test('the folder flow shows in the conversation, and a waiting offer in Needs you', () => {
  const pending = { id: 'o1', status: 'pending', verdict: 'project' };
  const running = { id: 'o2', status: 'awaiting_outcome', verdict: 'project' };
  const resolved = { id: 'o3', status: 'resolved', verdict: 'project' };

  // Waiting means the user still has something to do with it.
  assert.equal(folderOfferWaiting(pending), true);
  assert.equal(folderOfferWaiting(running), true);
  for (const status of ['resolved', 'declined', 'later', 'closed', '']) {
    assert.equal(folderOfferWaiting({ id: 'o', status }), false, status);
  }
  assert.equal(folderOfferWaiting({ status: 'pending' }), false, 'an offer with no id');
  assert.equal(folderOfferWaiting(null), false);

  // A page that loads onto a waiting offer pins it; nothing else is pinned.
  assert.equal(folderPinnedOffer({ inThread: false, pinned: '', offer: pending }), 'o1');
  assert.equal(folderPinnedOffer({ inThread: false, pinned: '', offer: running }), 'o2');
  assert.equal(folderPinnedOffer({ inThread: false, pinned: '', offer: resolved }), '');
  assert.equal(folderPinnedOffer({ inThread: false, pinned: '', offer: null }), '');
  // The card acted on under Needs you stays there through a later read...
  assert.equal(folderPinnedOffer({ inThread: false, pinned: 'o3', offer: resolved }), 'o3');
  // ...but a different, finished offer does not take its place.
  assert.equal(folderPinnedOffer({ inThread: false, pinned: 'o1', offer: resolved }), '');
  // Once the flow is in the conversation, the conversation has the card.
  assert.equal(folderPinnedOffer({ inThread: true, pinned: 'o1', offer: pending }), '');

  const place = options => folderPlacement({ available: true, ...options });
  assert.equal(place({ inThread: true, pinned: '', offer: pending }), 'thread');
  assert.equal(place({ inThread: true, pinned: '', offer: null }), 'thread');
  assert.equal(place({ inThread: false, pinned: 'o1', offer: pending }), 'needs');
  assert.equal(place({ inThread: false, pinned: 'o3', offer: resolved }), 'needs');
  // At rest, with nothing waiting, the conversation is empty.
  assert.equal(place({ inThread: false, pinned: '', offer: resolved }), 'none');
  assert.equal(place({ inThread: false, pinned: '', offer: null }), 'none');
  assert.equal(place({ inThread: false, pinned: 'o1', offer: null }), 'none');
  // Never before the hire or while HQ is being built.
  assert.equal(
    folderPlacement({ available: false, inThread: true, pinned: 'o1', offer: pending }),
    'none'
  );
});

test('"Explore a folder" is disabled while a folder is being chosen or explored', () => {
  assert.equal(folderChipBusy({ inThread: true, chooserOpen: true }), true);
  assert.equal(folderChipBusy({ inThread: true, scanning: true }), true);
  assert.equal(folderChipBusy({ inThread: true, busy: true }), true);
  // An offer on screen, in the conversation or under Needs you, does not.
  assert.equal(folderChipBusy({ inThread: true }), false);
  assert.equal(folderChipBusy({ inThread: false, chooserOpen: true }), false);
  assert.equal(folderChipBusy(), false);
});

test('the folder turn and work controller mount once on every drawer host', () => {
  const read = name =>
    readFileSync(new URL(`../../../templates/components/${name}`, import.meta.url), 'utf8');
  const turn = read('personal-assistant-folder.tmpl');
  const today = read('personal-assistant-today.tmpl');
  const drawer = read('ori-guide.tmpl');
  assert.doesNotMatch(today, /personalAssistantFolder/);
  assert.match(
    drawer,
    /id="personalAssistantThread"[^>]*>\s*<div[^>]*personalAssistantActivityMount[^>]*>\s*\{\{template "ask-ori-activity\.tmpl" \.\}\}\s*<\/div>\s*\{\{template "personal-assistant-folder\.tmpl" \.\}\}/
  );
  const head = read('../layout/head.tmpl');
  assert.equal((head.match(/src="\/js\/modules\/dashboard.js"/g) || []).length, 1);
  for (const page of [
    '../layout/base.tmpl',
    '../pages/workspaces.tmpl',
    '../pages/workspace-detail.tmpl',
    '../pages/workspace-task.tmpl',
    '../pages/workspace-canvas.tmpl'
  ]) {
    assert.doesNotMatch(read(page), /src="\/js\/modules\/dashboard.js"/, page);
  }
  for (const id of [
    'personalAssistantFolder',
    'personalAssistantFolderRequest',
    'personalAssistantFolderChooser',
    'personalAssistantFolderScene',
    'personalAssistantFolderOffer'
  ]) {
    assert.equal((turn.match(new RegExp(`id="${id}"`, 'g')) || []).length, 1, id);
  }
  // The user's request, then the assistant's replies in the order they come.
  const order = ['Request', 'Chooser', 'Scene', 'Offer'].map(part =>
    turn.indexOf(`id="personalAssistantFolder${part}"`)
  );
  assert.deepEqual(
    order,
    [...order].sort((a, b) => a - b)
  );
  assert.match(turn, /Explore a folder/);
  assert.match(turn, /Which folder should I explore\?/);
  assert.match(turn, /class="pa-folder__assurance">A read-only peek\. Nothing moves\./);
});

const thesisOffer = {
  id: 'o1',
  status: 'pending',
  verdict: 'project',
  folder: 'Documents',
  subject: { name: 'Thesis', shape: 'manuscript', marker: 'LaTeX manuscript' },
  reason: '14 LaTeX files, edited yesterday',
  remember: true,
  blueprint: 'writing-project',
  blueprint_label: 'Writing project',
  create_available: true
};

test('a mixed-DAW portfolio names its evidence and a one-time revival acknowledges no', () => {
  const offer = {
    id: 'collection',
    status: 'pending',
    verdict: 'project',
    folder: 'Music',
    subject: { name: 'Music', shape: 'audio', is_root: true },
    portfolio: { shape: 'audio', projects: 7, provider_key: 'music_project_management' },
    reason: '7 audio project folders',
    capability: {
      recognized: '7 music projects',
      workspace: 'Music Production Home',
      question: 'Set up one Music Production Home for these projects?',
      revived: true,
      integration: 'Setup will install the reviewed Music Project Management provider if needed.',
      accept_label: 'Yes, set up Music Production Home',
      decline_label: 'No thanks'
    }
  };
  const view = folderOfferView(offer);
  assert.match(headlineText(view), /7 music projects/);
  assert.match(view.question, /said no before/);
  assert.match(view.question, /Music Production Home/);
  assert.equal(view.actions[0].journey, true);
  assert.equal(view.reason, '7 audio project folders');
});

test('a collection offered to an existing Home asks to add it, not to set up another Home', () => {
  const offer = {
    id: 'collection',
    status: 'pending',
    verdict: 'project',
    folder: 'Albums',
    subject: { name: 'Albums', shape: 'audio', is_root: true },
    portfolio: {
      shape: 'audio',
      projects: 5,
      provider_key: 'music_project_management',
      existing_home: true
    },
    reason: '5 audio project folders',
    capability: {
      recognized: '5 music projects',
      workspace: 'Music Production Home',
      question: 'Add this collection to your Music Production Home?',
      integration: 'Nothing is installed or created.',
      accept_label: 'Yes, add to my Home',
      decline_label: 'No thanks'
    }
  };
  const view = folderOfferView(offer);
  assert.match(view.question, /Add this collection to your Music Production Home/);
  assert.doesNotMatch(view.question, /Set up one/);
  assert.equal(view.actions[0].journey, true);
  assert.equal(view.actions[0].label, 'Yes, add to my Home');
});

test('a resolved existing-Home outcome says nothing was created, moved, linked, or scanned', () => {
  const added = folderOutcomeNote({
    status: 'resolved',
    folder: 'Albums',
    outcome: { kind: 'home', existing: true, workspace_id: 'home', route: '/workspaces/home' }
  });
  assert.match(added, /waiting in its library/);
  assert.match(added, /Nothing was moved, linked, or scanned/);
  assert.doesNotMatch(added, /is ready\./, 'no new Home was made, so it must not say one is ready');

  const created = folderOutcomeNote({
    status: 'resolved',
    folder: 'Albums',
    outcome: { kind: 'home', workspace_id: 'home', route: '/workspaces/home' }
  });
  assert.match(created, /Music Production Home is ready/);
});

test('adding a collection to an existing Home names no Home and sends no path', () => {
  const source = readFileSync(new URL('./personal-assistant-folder.js', import.meta.url), 'utf8');
  // The server re-reads the Home; the only body is the request id postOffer adds.
  assert.match(source, /postOffer\(offer\.id, 'existing-home', \{\}\)/);
  assert.doesNotMatch(source, /existing-home[^)]*home_id/);
  assert.match(source, /if \(state\.offer\.portfolio\?\.existing_home\)/);
});

test('a reviewed file project uses the same card and never the blank creator', () => {
  const offer = {
    id: 'offer-1',
    verdict: 'project',
    status: 'pending',
    folder: 'Album',
    subject: { name: 'Album', shape: 'audio', marker: 'REAPER session' },
    reason: 'One project',
    capability: {
      recognized: 'REAPER',
      workspace: 'REAPER song workspace',
      integration: 'Setup can install the Ori integration plugin.',
      evidence: '1 REAPER session, edited today',
      accept_label: 'Yes, help with my music',
      decline_label: 'No thanks',
      setup_quest_id: 'install_ori_reaper'
    }
  };
  const view = folderOfferView(offer);
  assert.match(headlineText(view), /REAPER project/);
  assert.match(view.question, /REAPER song workspace/);
  assert.match(view.capabilityDetail, /Ori integration plugin/);
  assert.equal(view.reason, offer.capability.evidence);
  assert.deepEqual(
    view.actions.map(action => action.id),
    ['setup', 'no', 'later']
  );
  assert.equal(view.actions[0].journey, true);
  assert.ok(!view.actions.some(action => action.create || action.modal));
  const waiting = folderOfferView({ ...offer, status: 'awaiting_outcome' });
  assert.equal(waiting.resume, true);
  assert.match(waiting.question, /setup has not finished/);
  assert.match(waiting.capabilityDetail, /does not by itself create another workspace/);
  assert.doesNotMatch(waiting.capabilityDetail, /install the Ori integration plugin/);
  assert.equal(waiting.actions[0].id, 'resume');
  assert.equal(waiting.actions[0].journey, true);
});

test('after Adjust… the card keeps Set up beside Continue setup when a plan exists', () => {
  const view = folderOfferView({
    id: 'offer-2',
    verdict: 'project',
    status: 'awaiting_outcome',
    folder: 'Documents',
    subject: { name: 'Session' },
    capability: {
      recognized: 'REAPER',
      workspace: 'REAPER song workspace',
      setup_source: 'plugin'
    },
    plan: { digest: 'a'.repeat(64), lines: [{ name: 'Creates a workspace' }] }
  });
  assert.deepEqual(
    view.actions.map(action => action.id),
    ['setup', 'resume']
  );
  assert.equal(view.actions[0].oneCard, true);
  assert.equal(view.actions[1].journey, true);
  assert.match(view.question, /Set up does what is left in one go/);
  assert.equal(view.plan.lines.length, 1);
});

const oneCardOffer = {
  id: 'offer-1',
  verdict: 'project',
  status: 'pending',
  folder: 'Songs',
  subject: { name: 'My Song', marker: 'REAPER project' },
  reason: 'My Song.rpp is a REAPER project file.',
  capability: {
    recognized: 'REAPER song',
    workspace: 'REAPER Song workspace',
    integration: 'Setup will install the Ori integration plugin.',
    evidence: 'My Song.rpp is a REAPER project file.',
    setup_quest_id: 'install_ori_reaper'
  },
  plan: {
    digest: 'abc',
    lines: [
      { kind: 'integration', name: 'Installs the reviewed REAPER integration 0.9.0' },
      { kind: 'workspace', name: 'Creates a REAPER Song workspace named My Song' },
      { kind: 'task', name: 'Queues a first read-only task', detail: 'Starts when you open it' }
    ]
  }
};

test('a planned project card lists every consequence and offers Set up and Adjust', () => {
  const view = folderOfferView(oneCardOffer);
  assert.deepEqual(
    view.actions.map(action => action.id),
    ['setup', 'adjust', 'no', 'later']
  );
  assert.equal(view.actions[0].oneCard, true);
  assert.ok(!view.actions[0].journey);
  assert.equal(view.actions[1].journey, true);
  assert.deepEqual(
    view.plan.lines.map(line => line.name),
    oneCardOffer.plan.lines.map(line => line.name)
  );
  assert.equal(view.plan.digest, 'abc');
  // The plan already says what installs, so the pre-setup promise is dropped.
  assert.equal(view.capabilityDetail, '');
  assert.match(view.question, /everything Set up will do/);
  assert.equal(view.setup, null);
});

test('a card without a plan keeps the step-by-step journey, a collection card included', () => {
  const noPlan = folderOfferView({ ...oneCardOffer, plan: undefined });
  assert.equal(noPlan.actions[0].journey, true);
  assert.ok(!noPlan.actions[0].oneCard);
  assert.equal(noPlan.plan, null);
  const unplanned = folderOfferView({
    ...oneCardOffer,
    portfolio: { projects: 6 },
    plan: undefined
  });
  assert.equal(unplanned.actions[0].journey, true);
  assert.equal(unplanned.plan, null);
});

const portfolioPlan = {
  digest: 'p1',
  lines: [
    {
      kind: 'provider',
      name: 'Installs and enables the reviewed Music Project Management plugin 0.1.1'
    },
    { kind: 'integration', name: 'Installs and enables the reviewed REAPER integration 0.9.0' },
    { kind: 'home', name: 'Creates your Music Production Home' },
    { kind: 'agents', name: 'Adds the agents the Home requires' },
    { kind: 'library', name: 'Lists the 200 music projects in Songs' },
    { kind: 'songs', name: 'A REAPER song gets its workspace the first time you open it' },
    { kind: 'assistant', name: 'Your project assistant joins each REAPER song you open' }
  ]
};

test('a planned collection card is the one consent: Set up, Adjust…, No thanks, Later', () => {
  const view = folderOfferView({
    ...oneCardOffer,
    subject: { name: 'Songs' },
    portfolio: { projects: 200 },
    capability: {
      ...oneCardOffer.capability,
      question: 'Set up one Music Production Home for these projects?'
    },
    plan: portfolioPlan
  });
  assert.deepEqual(
    view.actions.map(action => action.id),
    ['setup', 'adjust', 'no', 'later']
  );
  assert.equal(view.actions[0].label, 'Set up');
  assert.equal(view.actions[0].oneCard, true);
  // Adjust… is today's step-by-step portfolio path.
  assert.equal(view.actions[1].journey, true);
  assert.equal(view.plan.digest, 'p1');
  assert.deepEqual(
    view.plan.lines.map(line => line.kind),
    ['provider', 'integration', 'home', 'agents', 'library', 'songs', 'assistant']
  );
  assert.match(view.question, /everything Set up will do/);
  assert.deepEqual(view.headline.map(part => part.text).join(''), 'Songs has 200 music projects.');
});

test('a running one-card setup shows each line with its state and no actions', () => {
  const view = folderOfferView({
    ...oneCardOffer,
    status: 'awaiting_outcome',
    plan: undefined,
    setup: {
      status: 'running',
      lines: [
        { kind: 'integration', name: 'Installs the integration', state: 'done' },
        { kind: 'workspace', name: 'Creates a workspace', state: 'working' },
        { kind: 'task', name: 'Queues a task', state: 'waiting' }
      ]
    }
  });
  assert.deepEqual(
    view.setup.lines.map(line => line.state),
    ['done', 'working', 'waiting']
  );
  assert.equal(view.setup.statusLine, 'Working on: Creates a workspace');
  assert.deepEqual(view.actions, []);
  assert.match(view.question, /Setting up My Song/);
  // Not the old "has not finished" wording that offers the journey.
  assert.doesNotMatch(view.question, /has not finished/);
  assert.equal(view.reason, '');
});

test('an unknown line state is shown as a neutral row, never trusted', () => {
  const view = folderOfferView({
    ...oneCardOffer,
    status: 'awaiting_outcome',
    setup: { status: 'running', lines: [{ kind: 'x', name: 'A step', state: '<b>done</b>' }] }
  });
  assert.equal(view.setup.lines[0].state, '');
});

const stoppedOffer = (reason, extra = {}) => ({
  ...oneCardOffer,
  status: 'awaiting_outcome',
  plan: undefined,
  setup: {
    status: 'stopped',
    stop_reason: reason,
    lines: [
      { kind: 'integration', name: 'Installs the integration', state: 'done' },
      { kind: 'workspace', name: 'Creates a workspace', state: 'failed' },
      { kind: 'task', name: 'Queues a task', state: 'waiting' }
    ],
    ...extra
  }
});

test('every stop reason says in one plain sentence what finished and what is needed', () => {
  for (const reason of [
    'plan_changed',
    'needs_pick',
    'needs_choice',
    'needs_model',
    'install_failed',
    'interrupted',
    'consent_stale',
    'assistant_missing',
    'failed',
    'something_unknown'
  ]) {
    const view = folderOfferView(
      stoppedOffer(reason, { entry_candidates: ['My Song.rpp', 'My Song v2.rpp'] })
    );
    assert.match(view.question, /^1 of 3 steps finished\. /, reason);
    // A raw reason code never reaches the screen.
    assert.doesNotMatch(view.question, /[a-z]+_[a-z]+/, reason);
    assert.ok(
      view.actions.some(action => action.journey),
      `${reason} keeps Continue setup`
    );
    assert.equal(view.resume, true);
  }
});

test('a stopped collection says what is true for a collection', () => {
  const view = folderOfferView({
    ...stoppedOffer('needs_model'),
    subject: { name: 'Songs' },
    portfolio: { projects: 30 }
  });
  assert.match(view.question, /The Home needs a model before its agents can be added/);
  assert.doesNotMatch(view.question, /workspace and folder are set up/);
  assert.deepEqual(
    view.actions.map(action => action.id),
    ['model', 'retry', 'resume']
  );
  const modal = setupModalView({
    ...stoppedOffer('needs_model'),
    subject: { name: 'Songs' },
    portfolio: { projects: 30 }
  });
  assert.match(modal.status, /The Home needs a model/);
});

test('a stopped setup offers the action that fixes its stop', () => {
  const ids = reason =>
    folderOfferView(stoppedOffer(reason, { entry_candidates: ['A.rpp', 'B.rpp'] })).actions.map(
      action => action.id
    );
  assert.deepEqual(ids('failed'), ['retry', 'resume']);
  assert.deepEqual(ids('install_failed'), ['retry', 'resume']);
  assert.deepEqual(ids('interrupted'), ['retry', 'resume']);
  assert.deepEqual(ids('needs_model'), ['model', 'retry', 'resume']);
  assert.deepEqual(ids('needs_choice'), ['choose-0', 'choose-1', 'resume']);
  assert.deepEqual(ids('plan_changed'), ['resume']);
  // The shared assistant cannot be added on its own: the user chooses in the journey.
  assert.deepEqual(ids('consent_stale'), ['resume']);
  assert.deepEqual(ids('assistant_missing'), ['resume']);
  const model = folderOfferView(stoppedOffer('needs_model')).actions[0];
  assert.equal(model.href, '/settings#system-model');
  const choice = folderOfferView(stoppedOffer('needs_choice', { entry_candidates: ['A.rpp'] }))
    .actions[0];
  assert.equal(choice.entry, 'A.rpp');
  assert.equal(choice.oneCard, true);
});

test('a finished one-card setup ends on the receipt with Open <workspace>', () => {
  const offer = {
    ...oneCardOffer,
    status: 'resolved',
    plan: undefined,
    outcome: {
      kind: 'project',
      route: '/workspaces/my-song',
      receipt: [
        { kind: 'workspace', name: 'My Song' },
        { kind: 'task', name: 'Read-only first look', detail: 'Starts when you open it' }
      ]
    }
  };
  const receipt = folderReceiptView(offer);
  assert.equal(receipt.visible, true);
  assert.equal(receipt.openLabel, 'Open My Song');
  assert.equal(receipt.route, '/workspaces/my-song');
  assert.equal(folderOfferView(offer).question, "Here's what I set up:");
});

test('a project offer says what it found, asks to confirm the plan, and offers Set up and Adjust', () => {
  const view = folderOfferView(thesisOffer);
  assert.equal(view.visible, true);
  assert.equal(headlineText(view), 'Thesis looks like a LaTeX manuscript.');
  assert.deepEqual(strongText(view), ['Thesis']);
  assert.equal(
    view.question,
    'Set up Thesis as a Writing project workspace? I will also remember that Thesis is a project you are working on.'
  );
  assert.equal(view.reason, '14 LaTeX files, edited yesterday');
  assert.deepEqual(
    view.actions.map(a => [a.label, a.decision, a.choice || '']),
    [
      ['Set up', 'yes', 'project'],
      ['Adjust…', 'yes', 'project'],
      ['Not this one', 'no', ''],
      ['Later', 'later', '']
    ]
  );
  assert.equal(view.actions[0].create, true, 'Set up has the assistant create it');
  assert.equal(view.actions[1].modal, true, 'Adjust… opens the modal');
  assert.deepEqual(
    view.actions.map(a => a.style),
    ['primary', 'outline', 'outline', 'link']
  );
  assert.equal(view.confirming, false);
});

test('without the server-side setup the modal is the one project path', () => {
  const view = folderOfferView({ ...thesisOffer, create_available: false });
  assert.deepEqual(
    view.actions.map(a => [a.id, a.label]),
    [
      ['yes', 'Set up workspace'],
      ['no', 'Not this one'],
      ['later', 'Later']
    ]
  );
  assert.equal(view.actions[0].modal, true);
  assert.equal(view.actions[0].create, undefined);
});

test('a project without a marker or a blueprint is still a plan, and a missing blueprint is explained', () => {
  const plain = folderOfferView({
    status: 'pending',
    verdict: 'project',
    folder: 'Documents',
    subject: { name: 'Stuff' },
    reason: '9 files, edited today',
    remember: false
  });
  assert.equal(headlineText(plain), 'Stuff looks like a project.');
  assert.equal(plain.question, 'Set up a workspace for Stuff?');

  const fallback = folderOfferView({
    ...thesisOffer,
    blueprint: '',
    blueprint_note:
      'The Writing project blueprint is not installed, so this starts as a blank workspace.',
    remember: false
  });
  assert.equal(
    fallback.question,
    'Set up a workspace for Thesis? The Writing project blueprint is not installed, so this starts as a blank workspace.'
  );
});

test('an offer whose folder the server no longer holds asks for it again before any yes', () => {
  const view = folderOfferView({ ...thesisOffer, needs_pick: true });
  assert.equal(headlineText(view), 'Thesis looks like a LaTeX manuscript.');
  assert.match(view.question, /Pick the folder again/);
  assert.deepEqual(
    view.actions.map(a => a.id),
    ['repick', 'no', 'later']
  );
  assert.equal(view.actions[0].repick, true);
  assert.equal(view.confirming, false);
  // A mixed offer's confirm card too; a decided offer is left alone.
  const mixed = folderOfferView(
    { ...thesisOffer, verdict: 'mixed', projects_count: 2, loose_files: 5, needs_pick: true },
    { confirmProject: true }
  );
  assert.equal(mixed.actions[0].id, 'repick');
  const waiting = folderOfferView({ ...thesisOffer, needs_pick: true, status: 'awaiting_outcome' });
  assert.deepEqual(
    waiting.actions.map(a => a.id),
    ['repick']
  );
  const decided = folderOfferView({ ...thesisOffer, needs_pick: true, status: 'later' });
  assert.equal(decided.decided, true);
  assert.notEqual(decided.actions[0]?.id, 'repick');
});

test('the remember sentence is dropped when the server says the fact cannot be saved', () => {
  const view = folderOfferView({
    status: 'pending',
    verdict: 'project',
    folder: 'Documents',
    subject: { name: 'Thesis' },
    reason: '14 LaTeX files, edited yesterday',
    remember: false
  });
  assert.equal(view.question, 'Set up a workspace for Thesis?');
});

test('a dump offer states the counts and offers a tidy', () => {
  const view = folderOfferView({
    status: 'pending',
    verdict: 'dump',
    folder: 'Downloads',
    subject: { name: 'Downloads', is_root: true },
    reason: '63 loose files of 9 kinds',
    loose_files: 63,
    loose_kinds: 9
  });
  assert.equal(headlineText(view), 'Downloads has 63 loose files of 9 kinds.');
  assert.deepEqual(strongText(view), ['Downloads']);
  assert.equal(
    view.question,
    'Want me to tidy it? I will propose moves and you approve each batch.'
  );
  assert.deepEqual(
    view.actions.map(a => [a.label, a.decision || '', a.choice || '']),
    [
      ['Tidy it', '', ''],
      ['Not this one', 'no', ''],
      ['Later', 'later', '']
    ]
  );
  // Tidy it confirms the plan first.
  assert.equal(view.actions[0].confirm, 'tidy');
});

test('a tidy is confirmed as a plan: Set up shows the setup, Adjust… opens the creator', () => {
  const dump = {
    status: 'pending',
    verdict: 'dump',
    folder: 'Downloads',
    subject: { name: 'Downloads', is_root: true },
    reason: '63 loose files of 9 kinds',
    loose_files: 63,
    loose_kinds: 9
  };
  const confirm = folderOfferView(dump, { confirm: 'tidy' });
  assert.equal(confirm.confirming, true);
  assert.equal(headlineText(confirm), 'Set up File Janitor for Downloads?');
  assert.deepEqual(strongText(confirm), ['Downloads']);
  assert.match(confirm.question, /File Curator/);
  assert.match(confirm.question, /nothing moves until you approve a batch/);
  assert.equal(confirm.reason, '63 loose files of 9 kinds');
  assert.deepEqual(
    confirm.actions.map(a => [a.id, a.label]),
    [
      ['setup', 'Set up'],
      ['adjust', 'Adjust…'],
      ['back', 'Back']
    ]
  );
  assert.equal(confirm.actions[0].decision, 'yes');
  assert.equal(confirm.actions[0].choice, 'tidy');
  assert.equal(confirm.actions[0].walkthrough, true);
  assert.equal(confirm.actions[1].manual, 'file-janitor');
  assert.equal(confirm.actions[2].back, true);

  // The same plan from a mixed or ambiguous offer's tidy button.
  const mixed = folderOfferView(
    {
      ...dump,
      verdict: 'mixed',
      folder: 'Documents',
      subject: { name: 'Thesis' },
      projects_count: 3
    },
    { confirm: 'tidy' }
  );
  assert.equal(headlineText(mixed), 'Set up File Janitor for Documents?');
  const ambiguous = folderOfferView({ ...dump, verdict: 'ambiguous' }, { confirm: 'tidy' });
  assert.equal(ambiguous.actions[0].id, 'setup');
  // A project verdict has no tidy plan, and a lost folder comes first.
  const project = folderOfferView({ ...dump, verdict: 'project' }, { confirm: 'tidy' });
  assert.equal(project.actions[0].id, 'yes');
  const lost = folderOfferView({ ...dump, needs_pick: true }, { confirm: 'tidy' });
  assert.equal(lost.actions[0].id, 'repick');
});

test('a mixed offer counts projects and loose files and asks which to start with', () => {
  const view = folderOfferView({
    status: 'pending',
    verdict: 'mixed',
    folder: 'Documents',
    subject: { name: 'Thesis' },
    reason: '3 projects and 40 loose files',
    projects_count: 3,
    loose_files: 40,
    loose_kinds: 6
  });
  assert.equal(headlineText(view), 'I see 3 projects and 40 loose files in Documents.');
  assert.deepEqual(strongText(view), ['3 projects', '40 loose files']);
  assert.equal(view.question, 'Start with a project, or a tidy?');
  assert.deepEqual(
    view.actions.map(a => [a.label, a.decision || '', a.choice || '']),
    [
      ['Start with Thesis', '', ''],
      ['Tidy the loose files', '', ''],
      ['Later', 'later', '']
    ]
  );
  // Each plan is confirmed on its own card first, with a way back.
  assert.equal(view.actions[0].confirm, 'project');
  assert.equal(view.actions[1].confirm, 'tidy');
  const confirm = folderOfferView(
    {
      status: 'pending',
      verdict: 'mixed',
      folder: 'Documents',
      subject: { name: 'Thesis', shape: 'manuscript', marker: 'LaTeX manuscript' },
      reason: '3 projects and 40 loose files',
      projects_count: 3,
      loose_files: 40,
      blueprint: 'writing-project',
      blueprint_label: 'Writing project',
      create_available: true,
      remember: true
    },
    { confirmProject: true }
  );
  assert.equal(confirm.confirming, true);
  assert.equal(headlineText(confirm), 'Thesis looks like a LaTeX manuscript.');
  assert.match(confirm.question, /^Set up Thesis as a Writing project workspace\?/);
  assert.deepEqual(
    confirm.actions.map(a => a.id),
    ['setup', 'adjust', 'back']
  );
  assert.equal(confirm.actions[2].back, true);
  const one = folderOfferView({
    status: 'pending',
    verdict: 'mixed',
    folder: 'D',
    subject: { name: 'X' },
    reason: 'r',
    projects_count: 1,
    loose_files: 1
  });
  assert.equal(headlineText(one), 'I see 1 project and 1 loose file in D.');
});

test('an ambiguous offer asks rather than guesses', () => {
  const view = folderOfferView({
    status: 'pending',
    verdict: 'ambiguous',
    folder: 'Scans',
    subject: { name: 'Scans', is_root: true },
    reason: '31 PDF files, last edited in March'
  });
  assert.equal(headlineText(view), 'I am not sure what Scans is.');
  assert.equal(view.question, 'Is this a project you work in, or a folder to tidy?');
  assert.deepEqual(
    view.actions.map(a => [a.label, a.decision || '', a.choice || '']),
    [
      ["It's a project", '', ''],
      ['Tidy it', '', ''],
      ['Neither', 'no', '']
    ]
  );
  assert.equal(view.actions[0].confirm, 'project');
  assert.equal(view.actions[1].confirm, 'tidy');
  assert.equal(view.reason, '31 PDF files, last edited in March');

  // "It's a project" confirms the plan before anything is decided.
  const confirm = folderOfferView(
    {
      status: 'pending',
      verdict: 'ambiguous',
      folder: 'Scans',
      subject: { name: 'Scans', is_root: true },
      reason: '31 PDF files, last edited in March',
      create_available: true
    },
    { confirmProject: true }
  );
  assert.equal(headlineText(confirm), 'Scans looks like a project.');
  assert.equal(confirm.question, 'Set up a workspace for Scans?');
  assert.deepEqual(
    confirm.actions.map(a => a.id),
    ['setup', 'adjust', 'back']
  );
  // A decided offer never shows the confirm card.
  const decided = folderOfferView(
    { status: 'declined', verdict: 'ambiguous', folder: 'Scans', subject: { name: 'Scans' } },
    { confirmProject: true }
  );
  assert.equal(decided.confirming, false);
  assert.equal(headlineText(decided), 'I am not sure what Scans is.');
});

test('an empty offer is a soft landing with one way forward', () => {
  const view = folderOfferView({
    status: 'closed',
    verdict: 'empty',
    folder: 'Desktop',
    subject: { name: 'Desktop' },
    reason: '2 files'
  });
  assert.equal(headlineText(view), 'Nothing in Desktop needs me yet.');
  assert.equal(view.question, 'Try Downloads, or pick another folder.');
  assert.deepEqual(
    view.actions.map(a => [a.label, a.open === true]),
    [['Show another folder', true]]
  );
  assert.equal(view.decided, false);
});

test('a declined root says so and offers another folder', () => {
  const view = folderOfferView({
    status: 'closed',
    verdict: 'declined',
    folder: 'Downloads',
    subject: { name: 'Downloads' },
    reason: '25 loose files of 6 kinds'
  });
  assert.equal(headlineText(view), 'You asked me not to ask about Downloads.');
  assert.equal(view.actions[0].open, true);
});

test('every verdict carries the reason line', () => {
  for (const verdict of ['project', 'dump', 'mixed', 'ambiguous', 'empty', 'declined']) {
    const view = folderOfferView({
      status: 'pending',
      verdict,
      folder: 'F',
      subject: { name: 'S' },
      reason: 'counts only'
    });
    assert.equal(view.visible, true, verdict);
    assert.equal(view.reason, 'counts only', verdict);
  }
  assert.equal(folderOfferView(null).visible, false);
  assert.equal(folderOfferView({ verdict: 'unknown' }).visible, false);
});

test('a decided offer hides its actions and explains what happens next', () => {
  const later = folderOfferView({
    status: 'later',
    verdict: 'dump',
    folder: 'Downloads',
    subject: { name: 'Downloads' },
    reason: 'r'
  });
  assert.equal(later.decided, true);
  assert.equal(folderOutcomeNote({ status: 'later' }), 'I will ask again in a week.');
  assert.equal(
    folderOutcomeNote({ status: 'declined', subject: { name: 'Thesis' } }),
    'I will not ask about Thesis again.'
  );
  assert.match(
    folderOutcomeNote({
      status: 'awaiting_outcome',
      choice: 'project',
      subject: { name: 'Thesis' }
    }),
    /workspace for Thesis/
  );
  assert.match(
    folderOutcomeNote({
      status: 'awaiting_outcome',
      choice: 'tidy',
      subject: { name: 'Downloads' }
    }),
    /tidy of Downloads/
  );
  assert.equal(folderOutcomeNote({ status: 'pending' }), '');
});

test('a project yes opens the creator pre-filled with the name, blueprint, note, and offer id — never a path', () => {
  const options = folderProjectModalOptions({
    id: 'offer-7',
    subject: { name: 'Thesis', shape: 'manuscript' },
    blueprint: 'writing-project',
    blueprint_note: ''
  });
  assert.deepEqual(options, {
    entryPoint: 'folder_digest',
    name: 'Thesis',
    blueprint: 'writing-project',
    blueprintNote: '',
    folderOfferId: 'offer-7'
  });
  const fallback = folderProjectModalOptions({
    id: 'offer-8',
    subject: { name: 'Album', shape: 'audio' },
    blueprint: '',
    blueprint_note:
      'The REAPER song blueprint is not installed, so this starts as a blank workspace.'
  });
  assert.equal(fallback.blueprint, '');
  assert.match(fallback.blueprintNote, /not installed/);
  assert.equal(Object.keys(fallback).includes('path'), false);
  assert.match(
    folderOutcomeNote({
      status: 'resolved',
      outcome: { kind: 'project' },
      subject: { name: 'Thesis' }
    }),
    /ready/
  );
});

test('Set up sends only the plan digest and a chosen file name, and polls only while running', () => {
  const source = readFileSync(new URL('./personal-assistant-folder.js', import.meta.url), 'utf8');
  assert.match(source, /postOffer\(offer\.id, 'setup', body\)/);
  assert.match(source, /const body = \{ plan_digest: digest \}/);
  assert.match(source, /body\.entry_name = action\.entry/);
  assert.match(source, /offer\.plan\?\.digest \|\| offer\.setup\?\.plan_digest/);
  // Polling starts from a click or an already-running read, never on its own.
  assert.match(source, /state\.offer\?\.setup\?\.status === 'running'/);
  assert.match(source, /SETUP_POLL_MS = 1500/);
  // It polls its own offer by name, so another waiting offer cannot hide the receipt.
  assert.match(source, /\$\{DIGEST_ENDPOINT\}\?offer_id=\$\{encodeURIComponent\(id\)\}/);
  assert.doesNotMatch(source, /folder_card_stub/);
});

test('Home entry points open the one-card chooser, or the HQ card before an HQ exists', () => {
  const source = readFileSync(new URL('./personal-assistant-folder.js', import.meta.url), 'utf8');
  const body = source.slice(
    source.indexOf('function openChooser()'),
    source.indexOf('async function load()')
  );
  // The mission's Start, "Show me a folder" and the first-folder prompt no longer
  // delegate to the chat-context chooser (the composer chip still does, itself).
  assert.doesNotMatch(body, /PersonalAssistantFolderContext\??\.(open|guide)/);
  assert.match(body, /openHQCard\(\)/);
  assert.doesNotMatch(
    source.slice(source.indexOf('async function load()'), source.indexOf('async function scan(')),
    /PersonalAssistantFolderContext/
  );
});

test('the module never sends a folder path to the server', () => {
  const source = readFileSync(new URL('./personal-assistant-folder.js', import.meta.url), 'utf8');
  assert.doesNotMatch(source, /\bpath\s*:/);
  assert.match(source, /JSON\.stringify\(body\)/);
  assert.match(source, /request_id: requestId\(\)/);
});

// A set-up folder's first look on its Home receipt (FR13-FR18).
const lookOffer = look => ({
  id: 'o1',
  status: 'resolved',
  subject: { name: 'Thesis' },
  outcome: {
    kind: 'project',
    route: '/workspaces/thesis',
    receipt: [
      { kind: 'workspace', name: 'Thesis', route: '/workspaces/thesis' },
      {
        kind: 'task',
        name: 'Summarize the draft',
        detail: 'Starts when you press Start first look'
      }
    ]
  },
  first_task: look
});
const seeded = { state: 'seeded', can_start: true, workspace_name: 'Thesis' };

test('a seeded first look leads with Start first look and keeps the workspace secondary', () => {
  const view = folderFirstLookView(seeded, {
    openLabel: 'Open Thesis',
    route: '/workspaces/thesis'
  });
  assert.equal(view.actions[0].label, 'Start first look');
  assert.equal(view.actions[0].style, 'primary');
  assert.equal(view.actions[0].start, true);
  assert.deepEqual(view.actions[1], {
    id: 'open',
    label: 'Open Thesis',
    style: 'outline',
    href: '/workspaces/thesis'
  });
  assert.equal(view.hint, FIRST_LOOK_HINT);
  assert.equal(folderFirstLookView(seeded, { busy: true }).actions[0].label, 'Starting…');
});

test('a first look that cannot start says why and drops the button', () => {
  const blocked = {
    state: 'seeded',
    can_start: false,
    reason: 'setup_wizard_opening',
    workspace_name: 'Thesis'
  };
  const view = folderFirstLookView(blocked, {
    openLabel: 'Open Thesis',
    route: '/workspaces/thesis'
  });
  assert.equal(view.message, 'Open Thesis to finish its setup first.');
  assert.deepEqual(
    view.actions.map(a => a.id),
    ['open']
  );
  assert.equal(view.actions[0].style, 'primary');
  const model = folderFirstLookView({ state: 'seeded', can_start: false, reason: 'no_model' }, {});
  assert.equal(model.message, 'Add a model in Settings to run the first look.');
  assert.equal(model.actions[0].href, '/settings#system-model');
});

test('every refusal has its sentence, and an already-going start has none', () => {
  assert.equal(
    firstLookStartMessage('unassigned', 'Thesis'),
    'The first task has no agent yet. Open Thesis to assign one.'
  );
  assert.equal(
    firstLookStartMessage('local_activation_required', 'Thesis'),
    'Open Thesis and activate it on this computer.'
  );
  assert.equal(
    firstLookStartMessage('start_failed', 'Thesis'),
    'The first look could not start. Try again.'
  );
  assert.equal(firstLookStartMessage('already_consumed', 'Thesis'), '');
  assert.equal(firstLookStartMessage('not_pending', 'Thesis'), '');
});

test('a running, waiting, failed and finished look each read as the server says', () => {
  const ctx = { openLabel: 'Open Thesis', route: '/workspaces/thesis' };
  assert.equal(folderFirstLookView({ state: 'running' }, ctx).status, 'Running…');
  assert.equal(
    folderFirstLookView({ state: 'waiting', message: 'Open Thesis to continue.' }, ctx).message,
    'Open Thesis to continue.'
  );
  const failed = folderFirstLookView({ state: 'failed', task_id: 't' }, ctx);
  assert.equal(failed.actions[0].label, 'Try again');
  assert.equal(failed.actions[0].retry, true);
  const done = folderFirstLookView(
    {
      state: 'finished',
      result_excerpt: 'Three drafts.',
      ticket_route: '/workspaces/thesis?ticket=t-1'
    },
    ctx
  );
  assert.equal(done.excerpt, 'Three drafts.');
  assert.equal(done.actions[0].label, 'Open the full report');
  assert.equal(done.actions[0].href, '/workspaces/thesis?ticket=t-1');
  // Only a ticket in a workspace is ever linked.
  const odd = folderFirstLookView({ state: 'finished', ticket_route: '//evil.example' }, ctx);
  assert.deepEqual(
    odd.actions.map(a => a.id),
    ['open']
  );
  assert.equal(folderFirstLookView({ state: 'bogus' }, ctx).visible, false);
  assert.equal(folderFirstLookView(null, ctx).visible, false);
});

test('the receipt carries the live first look and its row detail', () => {
  const receipt = folderReceiptView(lookOffer({ ...seeded, detail: 'Running…', state: 'running' }));
  assert.equal(receipt.firstLook.status, 'Running…');
  assert.equal(receipt.rows.find(r => r.kind === 'task').detail, 'Running…');
  assert.equal(folderReceiptView(lookOffer(undefined)).firstLook, undefined);
});

test('an open first look is pinned in Needs you, so a reload keeps it', () => {
  for (const state of ['seeded', 'running', 'waiting', 'failed', 'finished']) {
    assert.equal(
      folderPinnedOffer({ inThread: false, pinned: '', offer: lookOffer({ state }) }),
      'o1',
      state
    );
  }
  assert.equal(
    folderPinnedOffer({ inThread: false, pinned: '', offer: lookOffer(undefined) }),
    '',
    'a resolved offer with no first look is not news'
  );
  assert.equal(folderFirstLookOpen(lookOffer({ state: 'running' })), true);
  assert.equal(
    folderFirstLookOpen({ ...lookOffer({ state: 'running' }), status: 'pending' }),
    false
  );
});
