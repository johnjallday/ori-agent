import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

import {
  followUpReceiptLine,
  newRequestID,
  secondBrainAnsweredLine,
  secondBrainAskView,
  secondBrainAvailable,
  secondBrainChooserView,
  secondBrainConsentView,
  secondBrainError,
  secondBrainItemGroups,
  secondBrainLookView,
  secondBrainManageView,
  secondBrainPlacement,
  secondBrainQuestRequested,
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

  const none = secondBrainManageView({ status: 'not_connected' }, now);
  assert.equal(none.state, 'not_connected');
  assert.deepEqual(
    none.actions.map(action => action.label),
    ['Choose folder…']
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

test('with a detected vault the ask names it and offers four answers', () => {
  const view = secondBrainAskView(detected, true);
  assert.equal(view.show, true);
  assert.equal(
    view.line,
    'Do you keep a second brain? I found your Obsidian vault "Second Brain". If you let me read it, I\'ll tell you what\'s open.'
  );
  assert.deepEqual(
    view.actions.map(action => action.label),
    ['Read Second Brain', 'Another folder…', 'Not now', "I don't keep notes"]
  );
  assert.equal(view.actions[0].vaultId, 'v_1');
});

test('with nothing detected the ask points at a folder', () => {
  const view = secondBrainAskView({ ...detected, detected: [] }, true);
  assert.equal(
    view.line,
    "Do you keep a second brain, notes in Obsidian or a folder of Markdown? Point me at it and I'll tell you what's open."
  );
  assert.deepEqual(
    view.actions.map(action => action.label),
    ['Choose folder…', 'Not now', "I don't keep notes"]
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
        { id: 'a', text: 'Master track 7', due: '2026-10-24', checked: true },
        { id: 'b', text: 'Upload stems', due: '', checked: true }
      ]
    },
    {
      note: 'Reading List.md',
      label: 'Reading List',
      link: '',
      items: [{ id: 'c', text: 'Read Making Music', due: '', checked: false }]
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
  // Server text is only ever set as text.
  assert.doesNotMatch(source, /innerHTML/);
});
