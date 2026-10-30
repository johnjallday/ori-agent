import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

import {
  BRIEF_MAX,
  folderDigestOffer,
  followSongOpen,
  pressSongOpen,
  readSongSuggestions,
  songOpenedView,
  songOpeningLabel,
  songSuggestionView,
  STOP_COPY
} from './home-song-offer.js';

const source = readFileSync(new URL('./home-song-offer.js', import.meta.url), 'utf8');

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

const pending = {
  id: 'offer-5',
  status: 'pending',
  subject: { name: 'Album-5' },
  plan: { digest: 'abc', lines, hire, brief_default: 'Suggest the next step.' }
};

function respond(status, body) {
  return { ok: status >= 200 && status < 300, status, json: async () => body };
}

test('a later song’s pending plan is the Home’s one-press open', () => {
  const view = songSuggestionView(pending);
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
  assert.equal(songOpeningLabel(view), 'Portfolio Manager is opening Album-5…');
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
  for (const [reason, copy] of Object.entries(STOP_COPY)) {
    const view = songSuggestionView({
      id: 'offer-5',
      status: 'awaiting_outcome',
      setup: { plan_digest: 'd', status: 'stopped', stop_reason: reason, lines }
    });
    assert.equal(view.waitsElsewhere, true, reason);
    assert.equal(view.stopCopy, copy, reason);
  }
  assert.match(STOP_COPY.needs_model, /needs a model/);
  const unknown = songSuggestionView({
    id: 'offer-5',
    setup: { plan_digest: 'd', status: 'stopped', stop_reason: 'other', lines }
  });
  assert.equal(unknown.stopCopy, 'A step did not finish.');
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

// The demo's card stayed on "opening…": the read nests the offer under
// folder_digest, as GET /folder-digest answers.
test('following the open reads the offer where the server puts it', () => {
  const offer = { id: 'offer-5', status: 'resolved' };
  assert.equal(folderDigestOffer({ folder_digest: { offer } }), offer);
  assert.equal(folderDigestOffer({ offer }), null);
  assert.equal(folderDigestOffer(null), null);
});

test('the suggestions read is the Home’s, and a failed read is null, not empty', async () => {
  const urls = [];
  const views = await readSongSuggestions(async url => {
    urls.push(url);
    return respond(200, { suggestions: [pending, { id: 'not-a-song', plan: { lines: [] } }] });
  }, 'home 1');
  assert.deepEqual(urls, [
    '/api/personal-assistant/folder-digest/home-suggestions?home_id=home%201'
  ]);
  assert.deepEqual(
    views.map(view => view.offerID),
    ['offer-5']
  );
  assert.equal(await readSongSuggestions(async () => respond(500, {}), 'h'), null);
  assert.equal(
    await readSongSuggestions(async () => {
      throw new Error('offline');
    }, 'h'),
    null
  );
  assert.deepEqual(await readSongSuggestions(async () => respond(200, {}), ''), []);
});

test('the press posts the same Set up with open, never a path', async () => {
  assert.match(source, /\/offers\/\$\{encodeURIComponent\(view\.offerID\)\}\/setup/);
  assert.match(source, /open: true/);
  assert.doesNotMatch(source, /\b(folder_path|path):/);
  const view = songSuggestionView(pending);
  const posts = [];
  const pressed = await pressSongOpen(
    async (url, init) => {
      posts.push({ url, init, body: JSON.parse(init.body) });
      return respond(202, {});
    },
    view,
    '  clean up the drums  '
  );
  assert.deepEqual(pressed, { ok: true });
  assert.equal(posts.length, 1);
  assert.equal(posts[0].url, '/api/personal-assistant/folder-digest/offers/offer-5/setup');
  assert.equal(posts[0].init.method, 'POST');
  assert.equal(posts[0].body.plan_digest, 'abc');
  assert.equal(posts[0].body.open, true);
  assert.equal(posts[0].body.brief, 'clean up the drums');
  assert.ok(posts[0].body.request_id);
  // A plan without a brief question sends none.
  const noBrief = { ...view, asksBrief: false };
  await pressSongOpen(
    async (_, init) => {
      assert.equal('brief' in JSON.parse(init.body), false);
      return respond(202, {});
    },
    noBrief,
    'ignored'
  );
});

test('a refused press says why, and an over-limit brief is never sent', async () => {
  const view = songSuggestionView(pending);
  assert.deepEqual(await pressSongOpen(async () => respond(409, { plan_changed: true }), view), {
    ok: false,
    message: 'What opening does has changed. Review it again below.'
  });
  assert.deepEqual(await pressSongOpen(async () => respond(400, { error: 'No.' }), view), {
    ok: false,
    message: 'No.'
  });
  assert.deepEqual(
    await pressSongOpen(async () => {
      throw new Error('offline');
    }, view),
    { ok: false, message: 'Ori could not be reached. Nothing was opened twice; try again.' }
  );
  let sent = false;
  const over = await pressSongOpen(
    async () => {
      sent = true;
      return respond(202, {});
    },
    view,
    'x'.repeat(BRIEF_MAX + 1)
  );
  assert.equal(over.ok, false);
  assert.equal(sent, false);
});

test('following reads until opened or stopped, and never posts', async () => {
  const view = songSuggestionView(pending);
  const reads = [
    respond(503, {}),
    respond(200, { folder_digest: { offer: { id: 'offer-5', setup: { status: 'running' } } } }),
    respond(200, {
      folder_digest: {
        offer: { id: 'offer-5', status: 'resolved', outcome: { route: '/workspaces/album-5' } }
      }
    })
  ];
  const methods = [];
  const opened = await followSongOpen(
    async (url, init) => {
      methods.push(init?.method || 'GET');
      assert.equal(url, '/api/personal-assistant/folder-digest?offer_id=offer-5');
      return reads.shift();
    },
    view,
    { sleep: async () => {} }
  );
  assert.equal(opened.state, 'opened');
  assert.equal(opened.outcome.route, '/workspaces/album-5');
  assert.deepEqual(methods, ['GET', 'GET', 'GET']);
  const stopped = await followSongOpen(
    async () =>
      respond(200, { folder_digest: { offer: { id: 'offer-5', setup: { status: 'stopped' } } } }),
    view,
    { sleep: async () => {} }
  );
  assert.equal(stopped.state, 'stopped');
  const gone = await followSongOpen(async () => respond(200, {}), view, {
    sleep: async () => {},
    limit: 3
  });
  assert.equal(gone.state, 'unsettled');
  let read = false;
  const left = await followSongOpen(
    async () => {
      read = true;
      return respond(200, {});
    },
    view,
    { sleep: async () => {}, live: () => false }
  );
  assert.equal(left.state, 'unsettled');
  assert.equal(read, false, 'a card that left stops reading');
});

test('the opened receipt names the manager, the song link and the hire and first task', () => {
  const view = songSuggestionView(pending);
  const opened = songOpenedView(view, {
    route: '/workspaces/album-5',
    workspace_id: 'song-1',
    receipt: [
      { kind: 'home', name: 'Music Production Home', detail: 'your Home' },
      { kind: 'agent', name: 'Portfolio Manager', detail: 'already here' },
      { kind: 'workspace', name: 'Album-5', detail: 'opened' },
      { kind: 'agent', name: 'Song Assistant', detail: 'added', assigned: true },
      { kind: 'task', name: 'Summarize Album-5', detail: 'ready to start' }
    ]
  });
  assert.equal(opened.kicker, 'Portfolio Manager opened it');
  assert.equal(opened.heading, 'Album-5 is open');
  assert.equal(opened.route, '/workspaces/album-5');
  assert.equal(opened.openLabel, 'Open Album-5');
  assert.equal(opened.workspaceID, 'song-1');
  assert.deepEqual(opened.rows, [
    { kind: 'agent', text: 'Song Assistant — added' },
    { kind: 'task', text: 'Summarize Album-5 — ready to start' }
  ]);
  for (const route of ['//evil', 'https://x/workspaces/a', '/workspaces/a/b', '/workspaces/a?x=1'])
    assert.equal(songOpenedView(view, { route }).route, '', route);
});
