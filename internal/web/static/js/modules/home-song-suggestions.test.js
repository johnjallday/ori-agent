import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

import { folderDigestOffer, songSuggestionView } from './home-song-suggestions.js';

const source = readFileSync(new URL('./home-song-suggestions.js', import.meta.url), 'utf8');

const lines = [
  { kind: 'library', name: 'Lists Album-5 in the Home’s library' },
  {
    kind: 'handoff',
    name: 'Your Portfolio Manager then suggests opening Album-5',
    detail: 'In the Home, you choose whether to open it and hire its assistant.'
  },
  { kind: 'workspace', name: 'Creates a REAPER song workspace named Album-5' },
  { kind: 'agents', name: 'Adds the agents this blueprint requires' }
];

const hire = {
  mode: 'create',
  agent_name: 'Song Assistant',
  role_label: 'Song',
  model: 'gpt-5-mini'
};

test('a later song’s pending plan is the Home’s one-press open', () => {
  const view = songSuggestionView({
    id: 'offer-5',
    status: 'pending',
    subject: { name: 'Album-5' },
    plan: { digest: 'abc', lines, hire, brief_default: 'Suggest the next step.' }
  });
  assert.equal(view.offerID, 'offer-5');
  assert.equal(view.digest, 'abc');
  assert.equal(view.song, 'Album-5');
  assert.equal(view.manager, 'Portfolio Manager');
  assert.equal(view.label, 'Open Album-5 & hire');
  assert.deepEqual(view.hire, { name: 'Song Assistant', detail: 'Song role · uses gpt-5-mini' });
  assert.equal(view.brief, 'Suggest the next step.');
  assert.equal(view.asksBrief, true);
  assert.equal(view.running, false);
  assert.equal(view.waitsElsewhere, false);
});

test('a song stopped at the hand-off resumes on its run’s digest and brief', () => {
  const view = songSuggestionView({
    id: 'offer-5',
    status: 'awaiting_outcome',
    subject: { name: 'Album-5' },
    setup: {
      plan_digest: 'run-digest',
      status: 'stopped',
      stop_reason: 'handoff_ready',
      brief: 'Mix it.',
      hire,
      lines
    }
  });
  assert.equal(view.digest, 'run-digest');
  assert.equal(view.brief, 'Mix it.');
  assert.equal(view.waitsElsewhere, false);
  assert.equal(view.stopCopy, '');
});

test('a song that stopped for anything else is answered on the assistant’s card', () => {
  const view = songSuggestionView({
    id: 'offer-5',
    status: 'awaiting_outcome',
    setup: { plan_digest: 'd', status: 'stopped', stop_reason: 'needs_model', lines }
  });
  assert.equal(view.waitsElsewhere, true);
  assert.match(view.stopCopy, /needs a model/);
  const running = songSuggestionView({
    id: 'offer-5',
    status: 'awaiting_outcome',
    setup: { plan_digest: 'd', status: 'running', lines }
  });
  assert.equal(running.running, true);
});

test('an offer that is not a song this Home opens is not a suggestion', () => {
  assert.equal(songSuggestionView(null), null);
  assert.equal(
    songSuggestionView({
      id: 'x',
      plan: { digest: 'd', lines: [{ kind: 'workspace', name: 'Creates' }] }
    }),
    null
  );
  assert.equal(songSuggestionView({ id: 'x', plan: { lines } }), null, 'no digest to confirm');
});

test('the press posts the same Set up with open, never a path', () => {
  assert.match(source, /\/offers\/\$\{encodeURIComponent\(view\.offerID\)\}\/setup/);
  assert.match(source, /open: true/);
  assert.doesNotMatch(source, /\b(folder_path|path):/);
});

// The demo's card stayed on "opening…": the read nests the offer under
// folder_digest, as GET /folder-digest answers.
test('following the open reads the offer where the server puts it', () => {
  const offer = { id: 'offer-5', status: 'resolved' };
  assert.equal(folderDigestOffer({ folder_digest: { offer } }), offer);
  assert.equal(folderDigestOffer({ offer }), null);
  assert.equal(folderDigestOffer(null), null);
});

test('a hire whose role is its own name says it once', () => {
  const view = songSuggestionView({
    id: 'offer-5',
    plan: {
      digest: 'd',
      lines,
      hire: {
        mode: 'create',
        agent_name: 'REAPER Assistant',
        role_label: 'REAPER Assistant',
        model: 'm'
      }
    }
  });
  assert.equal(view.hire.detail, 'uses m');
});

test('a redraw while opening keeps the typed brief', () => {
  assert.match(source, /this\.briefs\.get\(view\.offerID\) \?\? view\.brief/);
});
