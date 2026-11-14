import test from 'node:test';
import assert from 'node:assert/strict';

import { folderAgentReportView } from './folder-agent-card.js';
import {
  commanderAutoOpen,
  commanderBuildingLine,
  commanderOneLine,
  commanderQueue,
  commanderSpeech,
  folderLookEnded,
  lookLine,
  renderCommanderOneLine
} from './commander-speech.js';

test('the entrance line names the workspace without changing the drawer status', () => {
  assert.equal(commanderBuildingLine('One Song'), 'Building One Song…');
  const view = folderAgentReportView({
    workspace_id: 'new',
    workspace_name: 'One Song',
    folder_name: 'Music',
    agent_name: 'Mira',
    state: 'looking'
  });
  assert.equal(commanderOneLine(view).text, 'Mira is looking around Music');
});

const raw = (overrides = {}) => ({
  workspace_id: 'ws-1',
  workspace_name: 'Albums',
  folder_name: 'Albums',
  agent_name: 'Albums Manager',
  state: 'reported',
  actions: ['set_up', 'keep', 'look_further'],
  ...overrides
});
const view = overrides => folderAgentReportView(raw(overrides));

test('the look narrates the walk, then the turn (FR18–FR19)', () => {
  assert.equal(lookLine(view({ state: 'looking' })), 'Looking around…');
  assert.equal(
    lookLine(view({ state: 'looking', progress: { folder: 'Album-3', opened: 12, entries: 40 } })),
    'Looking in Album-3 · 12 folders so far'
  );
  assert.equal(
    lookLine(view({ state: 'looking', progress: { folder: 'Albums', opened: 1 } })),
    'Looking in Albums · 1 folder so far'
  );
  assert.equal(
    lookLine(view({ state: 'looking', progress: { turn: true } })),
    'Thinking it through…'
  );
  assert.equal(
    lookLine(view({ state: 'looking', progress: { turn: true, folder: 'Album-5' } })),
    'Opening Album-5…'
  );
});

test('the look line moves but the announcement does not tick', () => {
  const a = commanderSpeech(view({ state: 'looking', progress: { folder: 'Album-1', opened: 2 } }));
  const b = commanderSpeech(view({ state: 'looking', progress: { folder: 'Album-4', opened: 9 } }));
  assert.notEqual(a.line, b.line);
  assert.equal(a.announce, b.announce);
  assert.equal(a.announce, 'Looking around Albums');
});

test('a report waiting on its premise asks one question before any plan', () => {
  const asked = view({
    premise: {
      state: 'asked',
      sentence: 'This looks like a music production folder: 6 REAPER songs.',
      confirm: 'Manage them as songs',
      other: "It's something else"
    }
  });
  const speech = commanderSpeech(asked);
  assert.equal(speech.phase, 'premise');
  assert.equal(speech.line, 'One question about Albums · Review');
  assert.equal(speech.review, true);
  assert.deepEqual(commanderOneLine(asked), {
    phase: 'premise',
    text: 'Albums Manager has a question about Albums',
    action: 'Review'
  });
  const confirmed = view({
    premise: { state: 'confirmed', sentence: 'A music production folder.' }
  });
  assert.equal(commanderSpeech(confirmed).phase, 'report');
  // "It's something else" plans nothing; the line must not say it has a plan.
  const withdrawn = view({
    actions: ['keep', 'look_further'],
    premise: {
      state: 'something_else',
      sentence: 'Nothing is installed or created.',
      withdrawn: true
    }
  });
  assert.equal(commanderSpeech(withdrawn).line, 'Nothing planned for Albums · Review');
  assert.equal(commanderOneLine(withdrawn).text, 'Nothing is planned for Albums');
});

test('the report and cause lines (FR9, FR7)', () => {
  assert.equal(commanderSpeech(view()).line, 'I have a plan for Albums · Review');
  assert.equal(commanderSpeech(view()).phase, 'report');
  const cause = commanderSpeech(
    view({
      model_sentence:
        'Albums Manager could not look itself: No API key is set for Anthropic. Fix it and press Look further. This is what Ori found.'
    })
  );
  assert.equal(cause.phase, 'cause');
  assert.equal(cause.line, "I couldn't look · Review");
  assert.equal(
    commanderSpeech(view({ state: 'failed' })).line,
    "I couldn't finish looking · Review"
  );
});

const runLines = [
  { kind: 'install', step: 'home', name: 'Installs Music Project Management', state: 'done' },
  {
    kind: 'home',
    step: 'home',
    name: 'Creates Music Production Home',
    state: 'working',
    progress: 'Downloading 0.5.0'
  },
  { kind: 'tools', step: 'tools', name: 'Connect REAPER tools', state: 'waiting' }
];

test('the run line says the step and the live words, never the time (FR6)', () => {
  const s = commanderSpeech(
    view({
      state: 'setting_up',
      setup: { status: 'running', started_at: '2026-11-09T10:00:00Z', lines: runLines }
    })
  );
  assert.equal(s.phase, 'running');
  assert.equal(s.line, 'Setting up Albums · step 1 of 2 · Downloading 0.5.0');
  assert.equal(s.announce, 'Setting up Albums · step 1 of 2');
  assert.doesNotMatch(s.line, /\d:\d\d/);
});

test('the receipt and stopped lines', () => {
  const done = view({ state: 'set_up', setup: { status: 'done', lines: runLines } });
  assert.equal(commanderSpeech(done).line, 'Albums is set up · See what I made');
  const stopped = view({ state: 'set_up', setup: { status: 'stopped', lines: runLines } });
  assert.equal(commanderSpeech(stopped).line, 'A step did not finish · Review');
  assert.equal(commanderSpeech(view({ state: 'kept' })), null);
  assert.equal(commanderSpeech(null), null);
});

test('a done run with a goal offer asks about the goal (folder-setup-ends-in-home FR17)', () => {
  const offering = view({
    state: 'set_up',
    setup: { status: 'done', lines: runLines },
    goal_offer: { goal: 'Finish the album', max_runes: 300 }
  });
  const speech = commanderSpeech(offering);
  assert.equal(speech.phase, 'goal');
  assert.equal(speech.line, 'A goal for Albums? · Review');
  assert.equal(speech.review, true);
  assert.deepEqual(commanderOneLine(offering), {
    phase: 'goal',
    text: 'Albums Manager suggests a goal for Albums',
    action: 'Review'
  });
});

const songsOffer = {
  count: 7,
  noun: 'songs',
  ask: 'Want me to learn the 7 songs?',
  reads:
    "I'll read tempo, length, track count, track names, markers and regions from each project file. Nothing is changed, and audio is not opened.",
  not_read: ''
};

test('a done run asks to learn the songs before it offers the goal (learn-the-songs FR2)', () => {
  const asking = view({
    state: 'set_up',
    setup: { status: 'done', lines: runLines },
    songs_offer: songsOffer
  });
  const speech = commanderSpeech(asking);
  assert.equal(speech.phase, 'songs');
  assert.equal(speech.line, 'Want me to learn the 7 songs? · Review');
  assert.equal(speech.announce, 'Want me to learn the 7 songs?');
  assert.equal(speech.review, true);
  assert.deepEqual(commanderOneLine(asking), {
    phase: 'songs',
    text: 'Albums Manager asks to learn the songs',
    action: 'Review'
  });
  // Given both, the Commander speaks the songs question: one thing at a time.
  const both = view({
    state: 'set_up',
    setup: { status: 'done', lines: runLines },
    songs_offer: songsOffer,
    goal_offer: { goal: 'Finish the album', max_runes: 300 }
  });
  assert.equal(commanderSpeech(both).phase, 'songs');
  // Once the songs are answered the goal is what it asks.
  const answered = view({
    state: 'set_up',
    setup: { status: 'done', lines: runLines },
    goal_offer: { goal: 'Finish the album', max_runes: 300 }
  });
  assert.equal(commanderSpeech(answered).phase, 'goal');
});

test('while it reads the songs the Commander says where it is, and announces it once', () => {
  const reading = (line, done) =>
    view({
      state: 'set_up',
      setup: { status: 'done', lines: runLines },
      learning: { status: 'running', line, done, total: 7 }
    });
  const a = commanderSpeech(reading('Learning Night Drive… (2 of 7)', 1));
  const b = commanderSpeech(reading('Learning Closer… (6 of 7)', 5));
  assert.equal(a.phase, 'learning');
  assert.equal(a.line, 'Learning Night Drive… (2 of 7)');
  assert.equal(a.review, false);
  // The line moves; the announcement does not tick through every song.
  assert.notEqual(a.line, b.line);
  assert.equal(a.announce, 'Learning the songs');
  assert.equal(b.announce, a.announce);
  assert.deepEqual(commanderOneLine(reading('Learning Closer… (6 of 7)', 5)), {
    phase: 'learning',
    text: 'Learning Closer… (6 of 7)',
    action: 'Review'
  });
  // The reading outranks an offer that is still on the view, and a goal waits.
  const mixed = view({
    state: 'set_up',
    setup: { status: 'done', lines: runLines },
    learning: { status: 'running', line: 'Learning the 7 songs…', done: 0, total: 7 },
    songs_offer: songsOffer,
    goal_offer: { goal: 'Finish the album', max_runes: 300 }
  });
  assert.equal(commanderSpeech(mixed).phase, 'learning');
  // While the agent writes up what it learned the Commander says so, the same way.
  const summarizing = view({
    state: 'set_up',
    setup: { status: 'done', lines: runLines },
    learning: {
      status: 'summarizing',
      line: 'Putting together what I learned…',
      done: 7,
      total: 7
    },
    goal_offer: { goal: 'Finish the album', max_runes: 300 }
  });
  assert.equal(commanderSpeech(summarizing).phase, 'learning');
  assert.equal(commanderSpeech(summarizing).line, 'Putting together what I learned…');
  assert.equal(commanderSpeech(summarizing).review, false);
  // A finished reading says nothing of its own: the receipt or the goal speaks.
  const finished = view({
    state: 'set_up',
    setup: { status: 'done', lines: runLines },
    learning: { status: 'done', line: 'Learned 7 songs', done: 7, total: 7 }
  });
  assert.equal(commanderSpeech(finished).phase, 'done');
});

test('a song name in the line is passed through as text', () => {
  const hostile = '<b>Night</b> "Drive"';
  const speech = commanderSpeech(
    view({
      state: 'set_up',
      setup: { status: 'done', lines: runLines },
      learning: { status: 'running', line: `Learning ${hostile}… (1 of 7)`, done: 0, total: 7 }
    })
  );
  assert.equal(speech.line, `Learning ${hostile}… (1 of 7)`);
});

test('the one-line card words per phase (FR13)', () => {
  assert.deepEqual(commanderOneLine(view()), {
    phase: 'report',
    text: 'Albums Manager has a plan for Albums',
    action: 'Review'
  });
  assert.equal(
    commanderOneLine(view({ state: 'setting_up', setup: { status: 'running', lines: runLines } }))
      .text,
    'Setting up Albums · step 1 of 2'
  );
  assert.deepEqual(
    commanderOneLine(view({ state: 'set_up', setup: { status: 'done', lines: runLines } })),
    {
      phase: 'done',
      text: 'Albums is set up',
      action: 'See what I made'
    }
  );
  assert.equal(
    commanderOneLine(view({ state: 'looking' })).text,
    'Albums Manager is looking around Albums'
  );
  assert.equal(commanderOneLine(view({ state: 'kept' })), null);
});

test('renderCommanderOneLine draws the line and its Review button', () => {
  const made = [];
  const doc = {
    createElement(tag) {
      const node = {
        tagName: tag.toUpperCase(),
        className: '',
        textContent: '',
        dataset: {},
        children: [],
        listeners: {},
        append(...kids) {
          node.children.push(...kids);
        },
        addEventListener(type, fn) {
          node.listeners[type] = fn;
        }
      };
      made.push(node);
      return node;
    }
  };
  let reviewed = 0;
  const row = renderCommanderOneLine(view(), doc, { onReview: () => reviewed++ });
  assert.equal(row.children[0].textContent, 'Albums Manager has a plan for Albums');
  assert.equal(row.children[1].textContent, 'Review');
  row.children[1].listeners.click();
  assert.equal(reviewed, 1);
  assert.equal(renderCommanderOneLine(view({ state: 'kept' }), doc), null);
});

test('a look has ended for the person only when it goes from looking to a report or a failure (visual-hire FR22)', () => {
  const state = name => ({ state: name });
  // A plan to review or a question (reported) and a look that failed both count.
  assert.equal(folderLookEnded(state('looking'), state('reported')), true);
  assert.equal(folderLookEnded(state('looking'), state('failed')), true);
  // Still looking, or a report that was already waiting, is not an ending.
  assert.equal(folderLookEnded(state('looking'), state('looking')), false);
  assert.equal(folderLookEnded(null, state('reported')), false);
  assert.equal(folderLookEnded(state('reported'), state('reported')), false);
  // A setup run's finish is not the look's.
  assert.equal(folderLookEnded(state('setting_up'), state('set_up')), false);
  assert.equal(folderLookEnded(state('setting_up'), state('failed')), false);
  assert.equal(folderLookEnded(state('looking'), state('setting_up')), false);
  assert.equal(folderLookEnded(state('looking'), null), false);
  assert.equal(folderLookEnded(), false);
});

test('auto-open: only after the owner acted here, or a report arriving on this map (FR10)', () => {
  const looking = view({ state: 'looking' });
  const reported = view();
  const running = view({ state: 'setting_up', setup: { status: 'running', lines: runLines } });
  const done = view({ state: 'set_up', setup: { status: 'done', lines: runLines } });
  // A report already waiting when the page opened: collapsed line only.
  assert.equal(commanderAutoOpen({ previous: null, next: reported, onMap: true }), false);
  // The report arrives while the owner is on this map.
  assert.equal(commanderAutoOpen({ previous: looking, next: reported, onMap: true }), true);
  // ...but not when the owner is on another tab.
  assert.equal(commanderAutoOpen({ previous: looking, next: reported, onMap: false }), false);
  // The owner pressed Look further / Set it up on this page.
  assert.equal(
    commanderAutoOpen({ previous: reported, next: looking, followed: true, onMap: true }),
    true
  );
  assert.equal(
    commanderAutoOpen({ previous: reported, next: running, followed: true, onMap: true }),
    true
  );
  assert.equal(
    commanderAutoOpen({ previous: running, next: done, followed: true, onMap: true }),
    true
  );
  // Someone else's run (not followed) never opens by itself.
  assert.equal(commanderAutoOpen({ previous: reported, next: running, onMap: true }), false);
  assert.equal(commanderAutoOpen({ previous: running, next: done, onMap: true }), false);
  // No change, no opening.
  assert.equal(
    commanderAutoOpen({ previous: running, next: running, followed: true, onMap: true }),
    false
  );
});

test('the urgency order and "and N more" (FR25)', () => {
  const q = commanderQueue([
    { kind: 'result', id: 'r1' },
    { kind: 'report', id: 'rep' },
    { kind: 'working', id: 'w' },
    { kind: 'ticket_waiting', id: 't1' },
    { kind: 'result', id: 'r2' }
  ]);
  assert.equal(q.first.id, 't1');
  assert.deepEqual(
    q.rest.map(i => i.id),
    ['rep', 'r1', 'r2', 'w']
  );
  assert.equal(q.more, 'and 4 more');
  assert.equal(commanderQueue([{ kind: 'report' }]).more, '');
  assert.equal(commanderQueue([]).first, null);
});
