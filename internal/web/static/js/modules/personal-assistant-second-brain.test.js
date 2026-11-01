import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

import {
  CAPTURE_MAX_CHARACTERS,
  DEFAULT_SECOND_BRAIN_NAME,
  FILL_IN_LINE,
  FOLDER_OFFER_LINE,
  followUpReceiptLine,
  newRequestID,
  secondBrainFillInAnsweredLine,
  secondBrainFillInView,
  secondBrainFolderOffer,
  secondBrainFolderOfferShows,
  PROJECT_QUESTION_ID,
  projectNameFromAnswer,
  saveProjectFact,
  secondBrainProjectAskView,
  setupLearningClosing,
  setupLearningView,
  SETUP_LEARNING_HEADING,
  secondBrainAnsweredLine,
  secondBrainAskView,
  secondBrainAvailable,
  secondBrainCaptureProblem,
  secondBrainCaptureReceipt,
  secondBrainChooserView,
  secondBrainConsentView,
  secondBrainError,
  secondBrainFolderNameProblem,
  secondBrainItemGroups,
  secondBrainLookView,
  secondBrainManageView,
  secondBrainOpenLink,
  secondBrainPlacement,
  secondBrainProjectLink,
  secondBrainProjectsView,
  secondBrainQuestRequested,
  secondBrainSetupConfirmView,
  secondBrainSetupDefaultView,
  secondBrainSetupWhereView,
  secondBrainWrittenLine,
  sinceLabel,
  suggestedLabel
} from './personal-assistant-second-brain.js';

test('the connection card says connected, missing, or not set up', () => {
  const now = Date.parse('2026-10-21T12:00:00Z');
  const connected = {
    status: 'connected',
    source: { name: 'Second Brain', kind: 'obsidian', path: '/Users/sam/Second Brain' },
    last_look: { at: '2026-10-21T10:00:00Z', open_items: 12 }
  };
  const view = secondBrainManageView(connected, now);
  assert.equal(view.title, 'Connected · Second Brain');
  assert.equal(view.detail, 'Obsidian · last look 2 hours ago');
  assert.deepEqual(
    view.actions.map(action => action.label),
    ['Look again', 'Disconnect']
  );

  const missing = secondBrainManageView({ ...connected, status: 'missing' }, now);
  assert.equal(missing.title, "Can't find Second Brain");
  assert.deepEqual(
    missing.actions.map(action => action.label),
    ['Choose again', 'Disconnect']
  );

  const fresh = secondBrainManageView({ ...connected, last_look: null }, now);
  assert.equal(fresh.detail, 'Obsidian · not looked at yet');
  const folder = secondBrainManageView(
    { ...connected, source: { name: 'Thesis Notes', kind: 'markdown' } },
    now
  );
  assert.match(folder.detail, /^Markdown folder · /);
  const setUp = secondBrainManageView(
    { ...connected, source: { name: 'My Brain', kind: 'obsidian', origin: 'created_by_ori' } },
    now
  );
  assert.match(setUp.detail, /^Set up by Ori · /);

  const none = secondBrainManageView({ status: 'not_connected' }, now);
  assert.equal(none.state, 'not_connected');
  assert.deepEqual(
    none.actions.map(action => action.label),
    ['Choose folder…', 'Set one up for me']
  );
});

test('since labels read like a person says them', () => {
  const now = Date.parse('2026-10-21T12:00:00Z');
  assert.equal(sinceLabel('2026-10-21T11:59:50Z', now), 'just now');
  assert.equal(sinceLabel('2026-10-21T11:59:00Z', now), '1 minute ago');
  assert.equal(sinceLabel('2026-10-21T11:15:00Z', now), '45 minutes ago');
  assert.equal(sinceLabel('2026-10-21T11:00:00Z', now), '1 hour ago');
  assert.equal(sinceLabel('2026-10-20T12:00:00Z', now), 'yesterday');
  assert.equal(sinceLabel('2026-10-17T12:00:00Z', now), '4 days ago');
  assert.equal(sinceLabel('', now), '');
});

test('the mission card arrives with ?quest=second-brain', () => {
  assert.equal(secondBrainQuestRequested('?quest=second-brain'), true);
  assert.equal(secondBrainQuestRequested('?quest=show-folder'), false);
  assert.equal(secondBrainQuestRequested(''), false);
});

const detected = {
  status: 'not_connected',
  ask: { state: 'pending' },
  detected: [
    {
      id: 'v_1',
      name: 'Second Brain',
      parent: 'Documents',
      path: '/Users/sam/Documents/Second Brain'
    }
  ]
};

test('the ask belongs to an active relationship only', () => {
  assert.equal(secondBrainAvailable({ state: 'active' }), true);
  assert.equal(secondBrainAvailable({ state: 'paused' }), false);
  assert.equal(secondBrainAvailable({ state: 'needs_hq' }), false);
  assert.equal(secondBrainAvailable(null), false);
});

test('with a detected vault the ask names it and offers read, set up, or not now', () => {
  const view = secondBrainAskView(detected, true);
  assert.equal(view.show, true);
  assert.equal(
    view.line,
    'Do you keep a second brain? I found your Obsidian vault "Second Brain". Let me read it, or I can set one up for you.'
  );
  assert.deepEqual(
    view.actions.map(action => action.label),
    ['Read Second Brain', 'Another folder…', 'Set one up for me', 'Not now']
  );
  assert.equal(view.actions[0].vaultId, 'v_1');
});

test('with nothing detected the ask points at a folder or offers to set one up', () => {
  const view = secondBrainAskView({ ...detected, detected: [] }, true);
  assert.equal(
    view.line,
    'Do you keep a second brain? Point me at it, or I can set one up for you.'
  );
  assert.deepEqual(
    view.actions.map(action => action.label),
    ['Choose folder…', 'Set one up for me', 'Not now']
  );
  assert.deepEqual(
    view.actions.map(action => action.id),
    ['choose', 'setup', 'later']
  );
});

test('the ask no longer offers "I don\'t keep notes", but a decline on record still reads', () => {
  for (const secondBrain of [detected, { ...detected, detected: [] }]) {
    const ids = secondBrainAskView(secondBrain, true).actions.map(action => action.id);
    assert.ok(!ids.includes('none'), ids.join());
  }
  assert.equal(secondBrainAskView({ ...detected, ask: { state: 'declined' } }, true).show, false);
  assert.equal(secondBrainAnsweredLine('none'), "Got it. I won't ask about notes again.");
});

test('the one-click setup says where it goes and offers somewhere else', () => {
  const view = secondBrainSetupDefaultView({
    parent_name: 'Ori Workspaces',
    path: '/Users/sam/Ori Workspaces/Second Brain'
  });
  assert.equal(view.title, 'Set up your second brain in Ori Workspaces?');
  assert.equal(view.path, '/Users/sam/Ori Workspaces/Second Brain');
  assert.equal(
    view.lines[0],
    "I'll keep Inbox, Daily, People, a note per project and what I know about you up to date; everything else is yours."
  );
  assert.deepEqual(
    view.actions.map(action => action.label),
    ['Set it up', 'Somewhere else…', 'Not now']
  );
});

test('setting one up asks where first, then names the folder and what Ori will write', () => {
  const where = secondBrainSetupWhereView({
    pickerOff: 'The dialog is off.',
    message: 'Pick again.'
  });
  assert.equal(where.title, 'Where should I set up your second brain?');
  assert.equal(where.pickLabel, 'Choose where…');
  assert.equal(where.pickerOff, 'The dialog is off.');
  assert.equal(where.message, 'Pick again.');

  const confirm = secondBrainSetupConfirmView(
    { name: 'Documents', path: '/Users/sam/Documents' },
    ''
  );
  assert.equal(confirm.title, 'Set up "Second Brain" in Documents?');
  assert.equal(confirm.path, '/Users/sam/Documents');
  assert.equal(
    confirm.lines[0],
    "I'll keep Inbox, Daily, People, a note per project and what I know about you up to date; everything else is yours."
  );
  assert.deepEqual(
    confirm.actions.map(action => action.label),
    ['Set it up', 'Back', 'Not now']
  );
  assert.equal(
    secondBrainSetupConfirmView({ name: 'Documents' }, 'My Brain').title,
    'Set up "My Brain" in Documents?'
  );
  assert.equal(DEFAULT_SECOND_BRAIN_NAME, 'Second Brain');
});

test('the card of a folder Ori set up says what Ori wrote and takes a note', () => {
  const now = Date.parse('2026-10-31T12:00:00Z');
  const created = {
    status: 'connected',
    source: { name: 'My Brain', kind: 'obsidian', origin: 'created_by_ori' },
    last_look: { at: '2026-10-31T11:00:00Z' },
    written_by_ori: { count: 5, last_at: '2026-10-31T09:00:00Z' }
  };
  const view = secondBrainManageView(created, now);
  assert.equal(view.written, 'Written by Ori: 5 notes · last write 3 hours ago');
  assert.equal(view.capture, true);

  const connected = secondBrainManageView(
    { ...created, source: { name: 'Vault', kind: 'obsidian', origin: 'connected' } },
    now
  );
  assert.equal(connected.written, '');
  assert.equal(connected.capture, false);
  assert.equal(secondBrainManageView({ ...created, status: 'missing' }, now).capture, undefined);
});

test('projects get Make a workspace only on a folder Ori set up, by name', () => {
  const created = {
    source: { origin: 'created_by_ori' },
    projects: [
      { name: 'Launch', rel: 'Projects/Launch', path: '/Users/sam/Second Brain/Projects/Launch' },
      { name: 'a/b', rel: 'Projects/a/b', path: '/x' },
      { rel: 'Projects/' }
    ]
  };
  const views = secondBrainProjectsView(created);
  assert.deepEqual(
    views.map(view => [view.name, view.action.label, view.action.ariaLabel]),
    [['Launch', 'Make a workspace', 'Make Launch a workspace']]
  );
  assert.deepEqual(secondBrainProjectsView({ ...created, source: { origin: 'connected' } }), []);
  assert.deepEqual(secondBrainProjectsView({ source: { origin: 'created_by_ori' } }), []);
  assert.deepEqual(secondBrainProjectsView(null), []);
  // The link names the project, never a place on disk.
  assert.equal(
    secondBrainProjectLink('Album 2: the remix'),
    '/?create=1&second_brain_project=Album%202%3A%20the%20remix'
  );
  assert.doesNotMatch(secondBrainProjectLink('Launch'), /Users|path=|%2F/);
});

test('the written line is honest about none, one, and many', () => {
  const now = Date.parse('2026-10-31T12:00:00Z');
  assert.equal(secondBrainWrittenLine(undefined, now), 'Ori has not written to your notes yet.');
  assert.equal(secondBrainWrittenLine({ count: 0 }, now), 'Ori has not written to your notes yet.');
  assert.equal(
    secondBrainWrittenLine({ count: 1, last_at: '2026-10-31T11:59:50Z' }, now),
    'Written by Ori: 1 note · last write just now'
  );
  assert.equal(secondBrainWrittenLine({ count: 3 }, now), 'Written by Ori: 3 notes');
});

test('a note must be there and fit before it is sent', () => {
  assert.equal(secondBrainCaptureProblem(''), 'Write the note first.');
  assert.equal(secondBrainCaptureProblem('  \n '), 'Write the note first.');
  assert.equal(secondBrainCaptureProblem('Call Dana'), '');
  assert.equal(secondBrainCaptureProblem('a'.repeat(CAPTURE_MAX_CHARACTERS)), '');
  assert.equal(
    secondBrainCaptureProblem('a'.repeat(CAPTURE_MAX_CHARACTERS + 1)),
    'A note is at most 4,000 characters.'
  );
  assert.equal(
    secondBrainCaptureReceipt({ name: '20261031-1407-call-dana' }),
    'Saved to Inbox as 20261031-1407-call-dana.'
  );
  assert.equal(secondBrainCaptureReceipt({}), 'Saved to Inbox.');
});

test('a folder name is one plain name', () => {
  for (const good of ['Second Brain', 'My Notes 2026', 'Notes & Ideas', ' padded ']) {
    assert.equal(secondBrainFolderNameProblem(good), '', good);
  }
  assert.equal(secondBrainFolderNameProblem(''), 'Give the folder a name.');
  assert.equal(secondBrainFolderNameProblem('   '), 'Give the folder a name.');
  for (const bad of [
    'a/b',
    'a\\b',
    '..',
    '.',
    '.hidden',
    'ends.',
    'a:b',
    'a\nb',
    'x'.repeat(256)
  ]) {
    assert.equal(
      secondBrainFolderNameProblem(bad),
      'Choose a simple folder name, without slashes.',
      JSON.stringify(bad)
    );
  }
});

test('Open in Obsidian only for a folder Ori set up, and only as an obsidian:// link', () => {
  const created = {
    source: {
      origin: 'created_by_ori',
      open_link: 'obsidian://open?path=%2FUsers%2Fsam%2FSecond%20Brain'
    }
  };
  assert.equal(secondBrainOpenLink(created), created.source.open_link);
  assert.equal(secondBrainOpenLink({ source: { ...created.source, origin: 'connected' } }), '');
  assert.equal(
    secondBrainOpenLink({
      source: { origin: 'created_by_ori', open_link: 'https://evil.example' }
    }),
    ''
  );
  assert.equal(secondBrainOpenLink({ source: { origin: 'created_by_ori' } }), '');
  assert.equal(secondBrainOpenLink(null), '');
});

test('seeded items are shown but never checked', () => {
  const groups = secondBrainItemGroups([
    { id: 'a', text: 'Call Dana', note: 'Inbox/call-dana.md', seeded: true },
    { id: 'b', text: 'Buy strings', note: 'Inbox/mine.md', suggested: true }
  ]);
  assert.deepEqual(
    groups.flatMap(group => group.items.map(item => [item.id, item.checked, item.seeded])),
    [
      ['a', false, true],
      ['b', true, false]
    ]
  );
});

test('the ask is shown once, while nothing is connected', () => {
  assert.equal(secondBrainAskView(detected, false).show, false);
  for (const state of ['shown', 'deferred', 'declined']) {
    assert.equal(secondBrainAskView({ ...detected, ask: { state } }, true).show, false, state);
  }
  assert.equal(secondBrainAskView({ ...detected, status: 'connected' }, true).show, false);
  assert.equal(secondBrainAskView(null, true).show, false);
});

test('a decline ends the turn with one line', () => {
  assert.equal(secondBrainAnsweredLine('none'), "Got it. I won't ask about notes again.");
  assert.match(secondBrainAnsweredLine('later'), /whenever you're ready/);
});

test('the chooser names vaults by name and folder, the path only as detail', () => {
  const view = secondBrainChooserView(detected);
  assert.equal(view.title, 'Which notes should I read?');
  assert.deepEqual(view.vaults, [
    {
      id: 'v_1',
      label: 'Second Brain',
      detail: 'in Documents',
      path: '/Users/sam/Documents/Second Brain'
    }
  ]);
  assert.equal(view.pickLabel, 'Pick another folder…');
  assert.equal(view.setUp.label, 'Set one up for me');
  const empty = secondBrainChooserView(
    { detected: [] },
    { pickerOff: 'The folder dialog is off.', message: 'No Markdown notes found in Empty.' }
  );
  assert.equal(empty.title, 'Where do you keep your notes?');
  assert.equal(empty.pickerOff, 'The folder dialog is off.');
  assert.equal(empty.message, 'No Markdown notes found in Empty.');
});

test('the consent card names the model, or says there is none', () => {
  const chosen = { name: 'Second Brain', path: '/Users/sam/Documents/Second Brain' };
  const withModel = secondBrainConsentView(
    chosen,
    { configured: true, provider: 'codex', model: 'gpt-5.6-luna' },
    'MacIntel'
  );
  assert.equal(withModel.title, 'Read Second Brain?');
  assert.equal(withModel.path, '/Users/sam/Documents/Second Brain');
  assert.ok(withModel.lines.includes('Read-only. Nothing is moved, edited, or deleted.'));
  assert.ok(
    withModel.lines.includes(
      'Short excerpts go to your model, codex/gpt-5.6-luna, to write the summary. Nothing else leaves your Mac.'
    )
  );
  assert.ok(
    withModel.lines.some(line => /keeps the folder path and the list of open items/.test(line))
  );
  assert.deepEqual(
    withModel.actions.map(action => action.label),
    ['Connect and look', 'Back', 'Not now']
  );
  const noModel = secondBrainConsentView(chosen, { configured: false });
  assert.ok(noModel.lines.includes('No model is set, so the summary is counts only.'));
  const linux = secondBrainConsentView(
    chosen,
    { configured: true, provider: 'openai', model: 'gpt' },
    'Linux x86_64'
  );
  assert.ok(linux.lines.some(line => line.endsWith('Nothing else leaves this computer.')));
});

test('the suggested label counts the items it adds', () => {
  assert.equal(suggestedLabel(1), 'Add 1 as a follow-up');
  assert.equal(suggestedLabel(8), 'Add 8 as follow-ups');
});

const finished = {
  state: 'finished',
  look_id: 'look-1',
  result: {
    id: 'look-1',
    headline: '12 open items across 6 notes, 4 with dates.',
    items: [
      {
        id: 'a',
        text: 'Master track 7',
        note: 'Projects/Album Release.md',
        due: '2026-10-24',
        suggested: true
      },
      { id: 'b', text: 'Upload stems', note: 'Projects/Album Release.md', suggested: true },
      { id: 'c', text: 'Read Making Music', note: 'Reading List.md' }
    ]
  }
};

test('a finished look leads with one headline and one action', () => {
  const view = secondBrainLookView(finished, { name: 'Second Brain' });
  assert.equal(view.state, 'finished');
  assert.equal(view.headline, '12 open items across 6 notes, 4 with dates.');
  assert.deepEqual(view.primary, { id: 'add', label: 'Add 2 as follow-ups', style: 'primary' });
  assert.equal(view.showAll, 'Show everything (3)');
  assert.equal(view.since, '');
  assert.equal(secondBrainLookView(finished, { acted: true }).primary, null);
});

test('look again says what changed since the last look', () => {
  const view = secondBrainLookView({
    ...finished,
    result: { ...finished.result, new_since: 2, done_since: 1 }
  });
  assert.equal(view.since, 'Since your last look: 2 new, 1 done.');
});

test('running and failed looks never wait on the user', () => {
  assert.deepEqual(secondBrainLookView({ state: 'running' }, { name: 'Second Brain' }), {
    state: 'running',
    line: 'Reading Second Brain…'
  });
  const failed = secondBrainLookView({
    state: 'failed',
    result: { failure: "Can't find Second Brain. It may have been moved or renamed." }
  });
  assert.equal(failed.line, "Can't find Second Brain. It may have been moved or renamed.");
  assert.deepEqual(
    failed.actions.map(action => action.label),
    ['Choose again']
  );
  assert.equal(secondBrainLookView(null).state, 'none');
});

test('show everything groups the items by note, suggested ones checked', () => {
  assert.deepEqual(secondBrainItemGroups(finished.result.items), [
    {
      note: 'Projects/Album Release.md',
      label: 'Projects/Album Release',
      link: '',
      items: [
        { id: 'a', text: 'Master track 7', due: '2026-10-24', checked: true, seeded: false },
        { id: 'b', text: 'Upload stems', due: '', checked: true, seeded: false }
      ]
    },
    {
      note: 'Reading List.md',
      label: 'Reading List',
      link: '',
      items: [{ id: 'c', text: 'Read Making Music', due: '', checked: false, seeded: false }]
    }
  ]);
});

test('an Obsidian note heading carries only an obsidian:// link the server built', () => {
  const link = 'obsidian://open?vault=Second%20Brain&file=Projects%2FAlbum%20Release';
  const [album, other] = secondBrainItemGroups([
    { id: 'a', text: 'Master track 7', note: 'Projects/Album Release.md', link },
    { id: 'b', text: 'Sneaky', note: 'Inbox.md', link: 'javascript:alert(1)' }
  ]);
  assert.equal(album.link, link);
  assert.equal(other.link, '');
});

test('look again is offered once a result has been acted on', () => {
  assert.equal(secondBrainLookView(finished).again, null);
  assert.deepEqual(secondBrainLookView(finished, { acted: true }).again, {
    id: 'again',
    label: 'Look again',
    style: 'link'
  });
});

test('the receipt says what was added and what was already there', () => {
  assert.equal(
    followUpReceiptLine({ created: 8, skipped: 0 }),
    'Added 8 follow-ups to Personal HQ.'
  );
  assert.equal(
    followUpReceiptLine({ created: 1, skipped: 0 }),
    'Added 1 follow-up to Personal HQ.'
  );
  assert.equal(
    followUpReceiptLine({ created: 2, skipped: 1 }),
    'Added 2 follow-ups to Personal HQ. 1 was already there.'
  );
  assert.equal(
    followUpReceiptLine({ created: 0, skipped: 3 }),
    'Those are already in your follow-ups.'
  );
});

test('errors read either shape the server writes', () => {
  assert.equal(
    secondBrainError({ error: 'No Markdown notes found in Empty.' }),
    'No Markdown notes found in Empty.'
  );
  assert.equal(
    secondBrainError({ code: 'bad_request', message: 'The request is not valid' }),
    'The request is not valid'
  );
  assert.equal(secondBrainError({}, 'Fallback.'), 'Fallback.');
});

test('the card is in the conversation, or in Needs you when pinned', () => {
  assert.equal(secondBrainPlacement({ available: true, phase: 'ask' }), 'thread');
  assert.equal(secondBrainPlacement({ available: true, phase: 'result', pinned: true }), 'needs');
  assert.equal(secondBrainPlacement({ available: true, phase: 'idle' }), 'none');
  assert.equal(secondBrainPlacement({ available: false, phase: 'ask' }), 'none');
});

test('request ids fit the server shape', () => {
  assert.match(newRequestID('connect'), /^[A-Za-z0-9._:-]{1,128}$/);
  assert.notEqual(newRequestID(), newRequestID());
});

test('the module sends no path to its own endpoint', () => {
  const source = readFileSync(
    new URL('./personal-assistant-second-brain.js', import.meta.url),
    'utf8'
  );
  assert.doesNotMatch(source, /\bpath:\s*state\.chosen/);
  assert.doesNotMatch(source, /body\.path\s*=/);
  assert.match(source, /body\.detected_id = state\.chosen\.detectedId/);
  assert.match(source, /body\.selection_token = state\.chosen\.selectionToken/);
  // A capture sends the note's text and a request id, nothing that names a place.
  assert.match(
    source,
    /body: \{ text: state\.capture\.text, request_id: state\.capture\.requestId \}/
  );
  // Setting one up sends the parent's token and the folder's name, or only a
  // request id for the default place; never a path.
  assert.match(
    source,
    /\{ selection_token: parent\.selectionToken, name, request_id: state\.setup\.requestId \}/
  );
  assert.match(source, /: \{ request_id: state\.setup\.requestId \}/);
  // Server text is only ever set as text.
  assert.doesNotMatch(source, /innerHTML/);
});

test('the consent card shows only for a second brain Ori set up with something held back', () => {
  const brain = (fillIn, origin = 'created_by_ori', status = 'connected') => ({
    status,
    source: { name: 'Second Brain', origin },
    fill_in: fillIn
  });
  const view = secondBrainFillInView(brain({ state: 'pending', offer: true }));
  assert.equal(view.show, true);
  assert.equal(
    view.line,
    'I can also keep People, a note per project and what I know about you up to date in your second brain. Yours are never touched.'
  );
  assert.equal(view.line, FILL_IN_LINE);
  assert.deepEqual(
    view.actions.map(action => [action.id, action.label]),
    [
      ['allow', 'Let Ori write there'],
      ['later', 'Not now']
    ]
  );
  // No card without something to write, once answered, for a connected vault,
  // or for a folder that is gone.
  for (const hidden of [
    brain({ state: 'pending', offer: false }),
    brain({ state: 'allowed', offer: false }),
    brain({ state: 'later', offer: false }),
    brain({ state: 'pending', offer: true }, 'connected'),
    brain({ state: 'pending', offer: true }, 'created_by_ori', 'missing'),
    brain(undefined),
    null
  ]) {
    assert.equal(secondBrainFillInView(hidden).show, false);
  }
  assert.equal(secondBrainFillInAnsweredLine('allow').startsWith('Done.'), true);
  assert.match(secondBrainFillInAnsweredLine('later'), /leave those alone/);
});

test('the consent card waits in Needs you, where a waiting result waits', () => {
  assert.equal(secondBrainPlacement({ available: true, phase: 'fillin', pinned: true }), 'needs');
  assert.equal(secondBrainPlacement({ available: false, phase: 'fillin', pinned: true }), 'none');
});

test('the first look offers the folders the server approved, Downloads first, and never repeats the empty headline', () => {
  const approved = [
    { id: 'desktop', label: 'Desktop' },
    { id: 'music', label: 'Music' },
    { id: 'downloads', label: 'Downloads' },
    { id: 'documents', label: 'Documents' }
  ];
  const counted = secondBrainFolderOffer(
    'Your second brain is set up: 3 follow-ups in Inbox, 2 people.',
    approved
  );
  assert.equal(counted.line, FOLDER_OFFER_LINE);
  assert.deepEqual(
    counted.chips.map(chip => chip.id),
    ['downloads', 'music', 'documents', 'desktop']
  );
  assert.equal(counted.chips[0].label, 'Downloads');
  assert.equal(counted.choose.label, 'Choose a folder…');

  // A folder the server did not approve is never offered, and one it added comes last.
  const some = secondBrainFolderOffer('', [
    { id: 'documents', label: 'Documents' },
    { id: 'pictures', label: 'Pictures' }
  ]);
  assert.deepEqual(
    some.chips.map(chip => chip.id),
    ['documents', 'pictures']
  );
  // Not known yet, or nothing approved: only Choose a folder… (which always works).
  for (const unknown of [null, undefined, [], 'downloads', [null, {}]]) {
    const offer = secondBrainFolderOffer('', unknown);
    assert.deepEqual(offer.chips, []);
    assert.equal(offer.choose.label, 'Choose a folder…');
  }

  const empty = secondBrainFolderOffer(
    "Your second brain is empty because I don't know you yet. Show me a folder and I'll start filling it.",
    approved
  );
  assert.equal(empty.line, '');
  assert.equal(empty.chips.length, 4);
  assert.equal(secondBrainFolderOffer('', approved).line, FOLDER_OFFER_LINE);
});

test('the folder offer survives a reload when the result itself asks for a folder, and ends when one is chosen', () => {
  const asking =
    "Your second brain is empty because I don't know you yet. Show me a folder and I'll start filling it.";
  const counted = 'Your second brain is set up: 3 follow-ups in Inbox, 2 people.';
  assert.equal(secondBrainFolderOfferShows({ justSetUp: true, headline: counted }), true);
  assert.equal(secondBrainFolderOfferShows({ justSetUp: false, headline: asking }), true);
  // A counted headline after a reload asks nothing, and a look that is not a setup never offers.
  assert.equal(secondBrainFolderOfferShows({ justSetUp: false, headline: counted }), false);
  assert.equal(secondBrainFolderOfferShows({}), false);
  // Once a folder is attached it is done, whichever way it was shown.
  assert.equal(
    secondBrainFolderOfferShows({ justSetUp: true, headline: asking, dismissed: true }),
    false
  );
});

test('the card posts the answer with a fresh request id and leaves one line behind', () => {
  const source = readFileSync(
    new URL('./personal-assistant-second-brain.js', import.meta.url),
    'utf8'
  );
  assert.match(source, /request\('\/fill-in'/);
  assert.match(source, /body: \{ decision, request_id: newRequestID\('fillin'\) \}/);
  // The folder offer hands off to the Show a folder flow; it reads nothing itself.
  assert.match(source, /await context\.selectChip\(chip\)/);
  assert.match(source, /PersonalAssistantFolder\?\.openChooser\?\.\(\)/);
  // The chips come from the same list the Add folder sheet uses.
  assert.match(source, /folder-context\/choices/);
  // A result dealt with frees the one card slot for the consent question.
  assert.match(source, /await showFillInIfOffered\(\)/);
});

test('what a folder taught Ori is listed word for word, with the version and id the approve action takes', () => {
  const view = setupLearningView({
    learning: {
      items: [
        {
          id: 'item-1',
          version: 3,
          text: 'You deal with Chase (seen in 2 files you filed).',
          evidence: [
            'User-approved filing of a file whose name includes Chase',
            ' ',
            'User-approved filing of a file whose name includes Chase',
            'c',
            'd'
          ],
          source: 'files'
        },
        { id: 'item-2', version: 1, text: 'You may prefer filing documents.', source: 'filing' },
        { id: 'item-3', version: 0, text: 'No version, so no action can take it.' },
        { id: '', version: 1, text: 'No id.' },
        { id: 'item-5', version: 1, text: '   ' },
        null
      ]
    }
  });
  assert.equal(view.total, 2);
  assert.equal(view.heading, SETUP_LEARNING_HEADING);
  assert.deepEqual(view.items[0], {
    id: 'item-1',
    version: 3,
    text: 'You deal with Chase (seen in 2 files you filed).',
    evidence: [
      'User-approved filing of a file whose name includes Chase',
      'User-approved filing of a file whose name includes Chase',
      'c'
    ],
    source: 'files'
  });
  assert.equal(view.items[1].source, 'filing');
  for (const nothing of [null, {}, { learning: {} }, { learning: { items: 'x' } }]) {
    const empty = setupLearningView(nothing);
    assert.equal(empty.total, 0);
    assert.equal(empty.heading, 'Nothing from this folder is waiting for an answer right now.');
  }
});

test('the card closes by counting the notes that were written', () => {
  assert.equal(setupLearningClosing(0), 'Nothing new was written to your notes.');
  assert.equal(setupLearningClosing(-3), 'Nothing new was written to your notes.');
  assert.equal(setupLearningClosing('x'), 'Nothing new was written to your notes.');
  assert.equal(setupLearningClosing(1), '1 note was written in your second brain.');
  assert.equal(setupLearningClosing(4), '4 notes were written in your second brain.');
});

test('each answer carries one retry key and the Profile page action, and dismissing posts only an id', () => {
  const source = readFileSync(
    new URL('./personal-assistant-second-brain.js', import.meta.url),
    'utf8'
  );
  assert.match(
    source,
    /api\/personal-assistant\/knowledge\/\$\{encodeURIComponent\(item\.id\)\}\/\$\{action\}/
  );
  assert.match(source, /version: item\.version, request_id: state\.learned\.keys\[keyName\]/);
  // The key is made once per learning and answer, so a retry repeats the request.
  assert.match(
    source,
    /if \(!state\.learned\.keys\[keyName\]\) state\.learned\.keys\[keyName\] = newRequestID\('learn'\)/
  );
  assert.match(source, /SETUP_LEARNING_ENDPOINT\}\/dismiss/);
  assert.match(source, /openLearned,/);
});

test('the project question belongs to a second brain that may keep project notes, once', () => {
  const brain = (over = {}) => ({
    status: 'connected',
    source: { name: 'Second Brain', origin: 'created_by_ori' },
    fill_in: { state: 'allowed' },
    projects: [],
    ...over
  });
  assert.equal(secondBrainProjectAskView(brain()).show, true);
  for (const hidden of [
    brain({ fill_in: { state: 'pending' } }),
    brain({ fill_in: { state: 'later' } }),
    brain({ fill_in: { state: 'allowed', project_ask: 'skipped' } }),
    brain({ fill_in: { state: 'allowed', project_ask: 'answered' } }),
    brain({ projects: [{ name: 'Launch' }] }),
    brain({ source: { name: 'Vault', origin: 'connected' } }),
    brain({ status: 'missing' }),
    brain({ fill_in: undefined }),
    null
  ]) {
    assert.equal(secondBrainProjectAskView(hidden).show, false, JSON.stringify(hidden));
  }
  assert.equal(PROJECT_QUESTION_ID, 'priority');
});

test('a project name is proposed from the first clause, short, and always a valid folder name', () => {
  for (const [answer, want] of Object.entries({
    'Finishing the Launch album': 'Launch Album',
    "I'm working on the Zine, and the website": 'Zine',
    'My podcast relaunch.': 'Podcast Relaunch',
    'widget, a Go module': 'Widget',
    'building a very long named thing today': 'Very Long Named',
    'Q3 planning': 'Q3 Planning',
    the: 'Project',
    '...': 'Project',
    '   ': 'Project',
    '../../etc': 'Project',
    'a/b/c': 'B C',
    'Café Müller': 'Café Müller'
  })) {
    const name = projectNameFromAnswer(answer);
    assert.equal(name, want, answer);
    assert.equal(secondBrainFolderNameProblem(name), '', `${answer} → ${name}`);
  }
});

test('the project question saves the fact and the note with their own retry keys, and skipping posts one id', () => {
  const source = readFileSync(
    new URL('./personal-assistant-second-brain.js', import.meta.url),
    'utf8'
  );
  // The fact is the user's own words, through the canonical explicit save that the
  // interview's rows end in; the Profile page's interview is not completed.
  assert.match(source, /EXPLICIT_ENDPOINT = '\/api\/personal-assistant\/knowledge\/explicit'/);
  assert.match(source, /category: 'projects'/);
  assert.match(source, /project\.keys\.fact = project\.keys\.fact \|\| newRequestID\('projfact'\)/);
  assert.match(source, /project\.keys\.note = project\.keys\.note \|\| newRequestID\('projnote'\)/);
  assert.doesNotMatch(source, /interview\/save/);
  assert.match(source, /request\('\/project\/skip'/);
  assert.match(source, /request\('\/project',/);
  // Make it a workspace opens the existing form with the project chosen; it
  // creates nothing.
  assert.match(source, /makeWorkspace\(name\)/);
  // A freed card slot asks the consent question first, then this one.
  assert.match(
    source,
    /async function showNextCard\(\) \{\s*await showFillInIfOffered\(\);\s*await showProjectAskIfDue\(\);/
  );
});

// The version the save names is the assistant record's, and it moves while the
// user types. The card reads it again and saves once more, under the same key.
function fakeFetch(...responses) {
  const calls = [];
  const impl = async (url, init = {}) => {
    calls.push({ url, body: init.body ? JSON.parse(init.body) : null });
    const next = responses.shift() || { status: 500, body: {} };
    return { ok: next.status < 400, status: next.status, json: async () => next.body };
  };
  impl.calls = calls;
  return impl;
}

const answered = () => ({
  keys: { fact: 'projfact-1' },
  stateVersion: 4,
  answer: ' Finishing the Launch album '
});

test('a save refused because the version moved reads it again and saves once more under the same key', async () => {
  const project = answered();
  const fetchImpl = fakeFetch(
    {
      status: 409,
      body: { error: 'The review changed or needs repair. Refresh before continuing' }
    },
    { status: 200, body: { state_version: 9 } },
    { status: 200, body: { item: { id: 'k1' } } }
  );
  const result = await saveProjectFact(project, fetchImpl);
  assert.equal(result.ok, true);
  assert.equal(project.stateVersion, 9);
  assert.deepEqual(
    fetchImpl.calls.map(call => [call.url.split('/').pop(), call.body?.state_version]),
    [
      ['explicit', 4],
      ['interview', undefined],
      ['explicit', 9]
    ]
  );
  const [first, , second] = fetchImpl.calls;
  assert.equal(first.body.request_id, 'projfact-1');
  assert.equal(
    second.body.request_id,
    'projfact-1',
    'the retry is the same save, not a second one'
  );
  assert.equal(second.body.category, 'projects');
  assert.equal(second.body.text, 'Finishing the Launch album');
});

test('a save is not retried when nothing about the version changed, or for any other refusal', async () => {
  const same = fakeFetch(
    { status: 409, body: { error: 'The review queue is full' } },
    { status: 200, body: { state_version: 4 } },
    {
      status: 200,
      body: { items: [{ state: 'approved', category: 'projects', text: 'Something else' }] }
    }
  );
  const stuck = await saveProjectFact(answered(), same);
  assert.equal(stuck.ok, false);
  assert.equal(stuck.status, 409);
  assert.deepEqual(
    same.calls.map(call => call.url.split('/').pop()),
    ['explicit', 'interview', 'knowledge'],
    'it looked, found the same version, found no such fact, and did not send again'
  );

  const broken = fakeFetch({ status: 503, body: {} });
  assert.equal((await saveProjectFact(answered(), broken)).status, 503);
  assert.equal(broken.calls.length, 1);

  const offline = async () => {
    throw new Error('offline');
  };
  const result = await saveProjectFact(answered(), offline);
  assert.equal(result.ok, false);
});

test('an answer the user already gave is a fact already held, so the note can still be written', async () => {
  const fetchImpl = fakeFetch(
    {
      status: 409,
      body: { error: 'The review changed or needs repair. Refresh before continuing' }
    },
    { status: 200, body: { state_version: 4 } },
    {
      status: 200,
      body: {
        items: [
          { state: 'forgotten', category: 'projects', text: 'Finishing the Launch album' },
          { state: 'approved', category: 'people', text: 'Finishing the Launch album' },
          { state: 'approved', category: 'projects', text: 'Finishing the Launch album' }
        ]
      }
    }
  );
  const result = await saveProjectFact(answered(), fetchImpl);
  assert.equal(result.ok, true);
  assert.equal(result.payload.already_held, true);
  assert.equal(
    fetchImpl.calls.filter(call => call.url.endsWith('/explicit')).length,
    1,
    'it never saved a second copy'
  );

  // Only an approved fact in the same category counts.
  const forgotten = fakeFetch(
    { status: 409, body: {} },
    { status: 200, body: { state_version: 4 } },
    {
      status: 200,
      body: {
        items: [{ state: 'forgotten', category: 'projects', text: 'Finishing the Launch album' }]
      }
    }
  );
  assert.equal((await saveProjectFact(answered(), forgotten)).ok, false);
});

test('a save that works the first time asks nothing else', async () => {
  const fetchImpl = fakeFetch({ status: 200, body: { item: { id: 'k1' } } });
  assert.equal((await saveProjectFact(answered(), fetchImpl)).ok, true);
  assert.equal(fetchImpl.calls.length, 1);
});
