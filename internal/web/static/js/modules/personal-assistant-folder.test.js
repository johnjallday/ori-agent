import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

import {
  folderActionAvailable,
  folderChooserView,
  folderOfferView,
  folderOutcomeNote,
  folderProjectModalOptions
} from './personal-assistant-folder.js';

const headlineText = view => view.headline.map(part => part.text).join('');
const strongText = view => view.headline.filter(part => part.strong).map(part => part.text);

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
  assert.equal(view.note, 'Pick a folder from the list for now.');

  const withPicker = folderChooserView({
    chips: [{ id: 'desktop', label: 'Desktop' }],
    picker_available: true
  });
  assert.equal(withPicker.pickerVisible, true);
  assert.equal(withPicker.note, '');
  assert.equal(withPicker.pickerLabel, 'Pick another folder…');
  assert.deepEqual(folderChooserView(null).chips, []);
});

test('a project offer names the folder, promises to remember, and offers the three actions', () => {
  const view = folderOfferView({
    id: 'o1',
    status: 'pending',
    verdict: 'project',
    folder: 'Documents',
    subject: { name: 'Thesis', shape: 'manuscript' },
    reason: '14 LaTeX files, edited yesterday',
    remember: true
  });
  assert.equal(view.visible, true);
  assert.equal(headlineText(view), 'You have been working in Thesis.');
  assert.deepEqual(strongText(view), ['Thesis']);
  assert.equal(
    view.question,
    'Want me to set up a workspace for it? I will also remember that Thesis is a project you are working on.'
  );
  assert.equal(view.reason, '14 LaTeX files, edited yesterday');
  assert.deepEqual(
    view.actions.map(a => [a.label, a.decision, a.choice || '']),
    [
      ['Set up workspace', 'yes', 'project'],
      ['Not this one', 'no', ''],
      ['Later', 'later', '']
    ]
  );
  assert.equal(view.actions[0].style, 'primary');
  assert.equal(view.actions[1].style, 'outline');
  assert.equal(view.actions[2].style, 'link');
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
  assert.equal(view.question, 'Want me to set up a workspace for it?');
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
    view.actions.map(a => [a.label, a.decision, a.choice || '']),
    [
      ['Tidy it', 'yes', 'tidy'],
      ['Not this one', 'no', ''],
      ['Later', 'later', '']
    ]
  );
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
    view.actions.map(a => [a.label, a.decision, a.choice || '']),
    [
      ['Start with Thesis', 'yes', 'project'],
      ['Tidy the loose files', 'yes', 'tidy'],
      ['Later', 'later', '']
    ]
  );
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
    view.actions.map(a => [a.label, a.decision, a.choice || '']),
    [
      ["It's a project", 'yes', 'project'],
      ['Tidy it', 'yes', 'tidy'],
      ['Neither', 'no', '']
    ]
  );
  assert.equal(view.reason, '31 PDF files, last edited in March');
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

test('the module never sends a folder path to the server', () => {
  const source = readFileSync(new URL('./personal-assistant-folder.js', import.meta.url), 'utf8');
  assert.doesNotMatch(source, /\bpath\s*:/);
  assert.match(source, /JSON\.stringify\(body\)/);
  assert.match(source, /request_id: requestId\(\)/);
});
