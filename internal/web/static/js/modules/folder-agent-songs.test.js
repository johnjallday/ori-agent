import test from 'node:test';
import assert from 'node:assert/strict';

import {
  folderAgentLive,
  folderAgentReportView,
  folderAgentTileLine,
  followFolderAgent,
  postFolderAgentAction,
  renderFolderAgentReport
} from './folder-agent-card.js';

// A stub DOM just big enough for the card renderers (the same shape the card's
// own tests use): elements with children, dataset, attributes and listeners.
function stubDocument() {
  const make = tag => {
    const node = {
      tagName: String(tag).toUpperCase(),
      className: '',
      textContent: '',
      children: [],
      parentElement: null,
      dataset: {},
      attributes: {},
      listeners: {},
      hidden: false,
      disabled: false,
      value: '',
      setAttribute(name, value) {
        this.attributes[name] = String(value);
      },
      getAttribute(name) {
        return this.attributes[name] ?? null;
      },
      append(...kids) {
        kids.forEach(kid => {
          kid.parentElement = this;
          this.children.push(kid);
        });
      },
      replaceChildren(...kids) {
        this.children = [];
        this.append(...kids);
      },
      addEventListener(type, fn) {
        this.listeners[type] = fn;
      },
      focus() {},
      matches(selector) {
        return selector
          .split(',')
          .some(
            part =>
              part.trim().startsWith('.') &&
              this.className.split(' ').includes(part.trim().slice(1))
          );
      },
      querySelectorAll(selector) {
        const found = [];
        const walk = n => {
          n.children.forEach(child => {
            if (selector === 'button' ? child.tagName === 'BUTTON' : child.matches(selector))
              found.push(child);
            walk(child);
          });
        };
        walk(this);
        return found;
      },
      querySelector(selector) {
        return this.querySelectorAll(selector)[0] ?? null;
      }
    };
    return node;
  };
  return { createElement: make, createTextNode: text => ({ textContent: text, children: [] }) };
}

const text = (node, selector) => node.querySelector(selector)?.textContent ?? null;
const plain = value => JSON.parse(JSON.stringify(value));
const okJSON = body => ({ ok: true, status: 200, json: async () => body });
const settle = () => new Promise(resolve => setTimeout(resolve, 40));

// until waits for a condition that a background poll makes true, up to five
// seconds, and fails loudly instead of hanging when it never does.
async function until(condition) {
  const deadline = Date.now() + 5000;
  while (!condition()) {
    if (Date.now() > deadline) assert.fail('timed out waiting for the poll');
    await new Promise(resolve => setTimeout(resolve, 5));
  }
}

// The server's view of a folder whose Set it up run is done and put it in a Home.
const done = (overrides = {}) => ({
  workspace_id: 'ws-1',
  workspace_name: 'Ori Music Demo',
  folder_name: 'Ori Music Demo',
  agent_name: 'Ori Music Demo Manager',
  state: 'set_up',
  stage: 'home',
  actions: ['look_further'],
  setup: {
    status: 'done',
    finished_at: '2026-11-11T12:00:00Z',
    lines: [{ kind: 'home', step: 'home', name: 'Creates Music Production Home', state: 'done' }]
  },
  ...overrides
});

const songsOffer = {
  count: 7,
  noun: 'songs',
  ask: 'Want me to learn the 7 songs?',
  reads:
    "I'll read tempo, length, track count, track names, markers and regions from each project file. Nothing is changed, and audio is not opened.",
  not_read: "1 Logic Pro song can't be read this way."
};

const goalOffer = { goal: 'Keep these songs moving toward release.', max_runes: 300 };

const learning = (overrides = {}) => ({
  status: 'running',
  line: 'Learning Night Drive… (3 of 7)',
  done: 2,
  total: 7,
  ...overrides
});

test('a done run offers to learn the songs: what it reads, what it cannot, and two answers', () => {
  const view = folderAgentReportView(done({ songs_offer: songsOffer }));
  assert.deepEqual(plain(view.songsOffer), {
    count: 7,
    noun: 'songs',
    ask: 'Want me to learn the 7 songs?',
    reads: songsOffer.reads,
    notRead: "1 Logic Pro song can't be read this way.",
    update: ''
  });
  const calls = [];
  const card = renderFolderAgentReport(view, stubDocument(), {
    onAction: (action, _button, extra) => calls.push([action, extra])
  });
  const offer = card.querySelector('.folder-agent-card__songs-offer');
  assert.ok(offer, 'the offer is on the card');
  assert.equal(offer.attributes['aria-label'], 'Learn the songs');
  assert.equal(text(offer, '.folder-agent-card__songs-lead'), 'Want me to learn the 7 songs?');
  assert.equal(text(offer, '.folder-agent-card__songs-reads'), songsOffer.reads);
  assert.equal(
    text(offer, '.folder-agent-card__songs-not-read'),
    "1 Logic Pro song can't be read this way."
  );
  const buttons = offer.querySelectorAll('button');
  assert.deepEqual(
    buttons.map(b => b.textContent),
    ['Learn them', 'Not now']
  );
  assert.deepEqual(
    buttons.map(b => b.dataset.folderAgentAction),
    ['learn_songs', 'dismiss_songs']
  );
  assert.match(buttons[0].className, /btn-primary/, 'Learn them is the primary answer');
  assert.doesNotMatch(buttons[1].className, /btn-primary/);
  buttons[0].listeners.click();
  buttons[1].listeners.click();
  assert.deepEqual(plain(calls), [
    ['learn_songs', {}],
    ['dismiss_songs', {}]
  ]);
});

// FR49: songs an installed app plugin owns but cannot read yet are never a silent
// blank. The offer's place says to update the plugin and offers nothing to answer.
test('songs a plugin cannot read yet are told as an update, with nothing to learn', () => {
  const view = folderAgentReportView(
    done({
      songs_offer: {
        count: 7,
        noun: 'songs',
        ask: 'Update REAPER to read the 7 songs.',
        reads: '',
        update: 'REAPER'
      }
    })
  );
  assert.equal(view.songsOffer.update, 'REAPER');
  const card = renderFolderAgentReport(view, stubDocument(), {});
  const offer = card.querySelector('.folder-agent-card__songs-offer');
  assert.ok(offer, 'the place of the offer is never blank');
  assert.equal(offer.dataset.songsOffer, 'update');
  assert.equal(text(offer, '.folder-agent-card__songs-lead'), 'Update REAPER to read the 7 songs.');
  assert.equal(offer.querySelectorAll('button').length, 0, 'there is nothing to learn or dismiss');
  assert.deepEqual(
    offer
      .querySelector('.folder-agent-card__songs-actions')
      .children.map(node => [node.tagName.toLowerCase(), node.textContent, node.href]),
    [['a', 'Update', '/plugins']]
  );
  assert.equal(offer.querySelector('.folder-agent-card__songs-reads'), null);
});

test('the offer is hidden when the server makes none, and so is the reading', () => {
  const quiet = folderAgentReportView(done());
  assert.equal(quiet.songsOffer, null);
  assert.equal(quiet.learning, null);
  const card = renderFolderAgentReport(quiet, stubDocument(), {});
  assert.equal(card.querySelector('.folder-agent-card__songs-offer'), null);
  assert.equal(card.querySelector('.folder-agent-card__learning'), null);
  // An offer with no question is not drawn, whatever else it carries.
  assert.equal(folderAgentReportView(done({ songs_offer: { count: 7 } })).songsOffer, null);
  // Nothing in the offer's words is drawn when none can be read.
  const bare = folderAgentReportView(done({ songs_offer: { ...songsOffer, not_read: '' } }));
  const bareCard = renderFolderAgentReport(bare, stubDocument(), {});
  assert.equal(bareCard.querySelector('.folder-agent-card__songs-not-read'), null);
});

test('the songs are asked about before the goal', () => {
  // The server never sends both (the goal waits), but a card that is given both
  // still asks the songs first.
  const both = folderAgentReportView(done({ songs_offer: songsOffer, goal_offer: goalOffer }));
  const card = renderFolderAgentReport(both, stubDocument(), {});
  const order = card.children.map(child => child.className);
  const songs = order.findIndex(name => name.includes('folder-agent-card__songs-offer'));
  const goal = order.findIndex(name => name.includes('folder-agent-card__goal-offer'));
  assert.ok(songs >= 0 && goal >= 0, 'both are drawn');
  assert.ok(songs < goal, 'the songs offer comes before the goal offer');
  // Once the songs are answered the goal offer is the only question.
  const after = folderAgentReportView(done({ goal_offer: goalOffer }));
  const afterCard = renderFolderAgentReport(after, stubDocument(), {});
  assert.equal(afterCard.querySelector('.folder-agent-card__songs-offer'), null);
  assert.ok(afterCard.querySelector('.folder-agent-card__goal-offer'));
});

test('the answers post to the songs endpoint', async () => {
  const view = folderAgentReportView(done({ songs_offer: songsOffer }));
  const posts = [];
  const fetchImpl = async (url, init) => {
    posts.push([url, JSON.parse(init.body)]);
    return okJSON({ folder_agent: null });
  };
  await postFolderAgentAction(view, 'learn_songs', fetchImpl, {});
  await postFolderAgentAction(view, 'dismiss_songs', fetchImpl, {});
  assert.deepEqual(posts, [
    ['/api/personal-assistant/folder-agent/ws-1/songs', { answer: 'learn' }],
    ['/api/personal-assistant/folder-agent/ws-1/songs', { answer: 'dismiss' }]
  ]);
});

// assistant-reads-projects FR33, FR35: the card names who is reading, and every
// way a reading can stop has its sentence and its one action.
test('a reading names the assistant that is reading', () => {
  const view = folderAgentReportView(
    done({
      learning: learning({
        line: 'REAPER Assistant is reading 7 songs…',
        reader: 'REAPER Assistant'
      })
    })
  );
  assert.equal(view.learning.reader, 'REAPER Assistant');
  const card = renderFolderAgentReport(view, stubDocument(), {});
  assert.equal(
    text(card, '.folder-agent-card__learning-line'),
    'REAPER Assistant is reading 7 songs…'
  );
});

test('a stopped reading says why and offers the one action that fits', () => {
  const stopped = (reason, action, line) =>
    folderAgentReportView(
      done({
        setup: {
          status: 'done',
          lines: [],
          receipt: { home: { name: 'Music Production Home', route: '/workspaces/home-1' } }
        },
        learning: learning({ status: 'stopped', line, reason, action, reader: 'REAPER Assistant' })
      })
    );
  const calls = [];
  const draw = view =>
    renderFolderAgentReport(view, stubDocument(), {
      onAction: (action, _button, extra) => calls.push([action, extra])
    });
  const controls = card =>
    card
      .querySelector('.folder-agent-card__learning-actions')
      .children.map(node => [node.tagName, node.textContent, node.href || '']);

  const retry = draw(stopped('plugin_failed', 'retry', 'Reading stopped after 2 of 7 songs.'));
  assert.equal(
    retry.querySelector('.folder-agent-card__learning').dataset.learningReason,
    'plugin_failed'
  );
  assert.deepEqual(controls(retry), [['BUTTON', 'Try again', '']]);
  retry.querySelector('.folder-agent-card__learning-actions').children[0].listeners.click();
  assert.deepEqual(plain(calls), [['retry_songs', {}]]);

  assert.deepEqual(controls(draw(stopped('no_model', 'choose_model', 'x has no model.'))), [
    ['A', 'Choose a model', '/settings'],
    ['BUTTON', 'Try again', '']
  ]);
  assert.deepEqual(controls(draw(stopped('no_assistant', 'add_assistant', 'No assistant.'))), [
    ['A', 'Add it', '/workspaces/home-1'],
    ['BUTTON', 'Try again', '']
  ]);
  assert.deepEqual(controls(draw(stopped('plugin_update', 'update_plugin', 'Update it.'))), [
    ['A', 'Update', '/plugins']
  ]);
  assert.deepEqual(controls(draw(stopped('details_off', 'open_switch', 'Song details are off.'))), [
    ['A', "Open the Home's switch", '/workspaces/home-1']
  ]);
  // A stop with no named reason keeps its line and offers nothing to press.
  const plainStop = draw(
    folderAgentReportView(
      done({ learning: learning({ status: 'stopped', line: 'Stopped before it finished' }) })
    )
  );
  assert.equal(plainStop.querySelector('.folder-agent-card__learning-actions'), null);
  // A reading that is still running never shows an action.
  const running = draw(
    folderAgentReportView(
      done({ learning: learning({ action: 'retry', reason: 'plugin_failed' }) })
    )
  );
  assert.equal(running.querySelector('.folder-agent-card__learning-actions'), null);
});

test('Try again posts the retry answer to the songs endpoint', async () => {
  const view = folderAgentReportView(done());
  const posts = [];
  await postFolderAgentAction(
    view,
    'retry_songs',
    async (url, init) => {
      posts.push([url, JSON.parse(init.body)]);
      return okJSON({ folder_agent: null });
    },
    {}
  );
  assert.deepEqual(posts, [
    ['/api/personal-assistant/folder-agent/ws-1/songs', { answer: 'retry' }]
  ]);
});

test('a reading shows its line and a progress bar, then how it ended', () => {
  const running = folderAgentReportView(done({ learning: learning() }));
  assert.deepEqual(plain(running.learning), {
    status: 'running',
    line: 'Learning Night Drive… (3 of 7)',
    done: 2,
    total: 7,
    notRead: '',
    summary: '',
    summaryBy: '',
    reader: '',
    reason: '',
    action: ''
  });
  const card = renderFolderAgentReport(running, stubDocument(), {});
  const section = card.querySelector('.folder-agent-card__learning');
  assert.equal(section.dataset.learningStatus, 'running');
  const line = section.querySelector('.folder-agent-card__learning-line');
  assert.equal(line.textContent, 'Learning Night Drive… (3 of 7)');
  assert.equal(line.attributes.role, 'status', 'the line is announced as it changes');
  const bar = section.querySelector('.folder-agent-card__learning-bar');
  assert.equal(bar.max, 7);
  assert.equal(bar.value, 2);

  // The bar never runs past its end, and a reading with no total has no bar.
  const over = renderFolderAgentReport(
    folderAgentReportView(done({ learning: learning({ done: 9 }) })),
    stubDocument(),
    {}
  );
  assert.equal(over.querySelector('.folder-agent-card__learning-bar').value, 7);
  const unknown = renderFolderAgentReport(
    folderAgentReportView(done({ learning: learning({ total: 0, done: 0 }) })),
    stubDocument(),
    {}
  );
  assert.equal(unknown.querySelector('.folder-agent-card__learning-bar'), null);

  // Done: the closing line stays, with the songs that cannot be read; no bar.
  const finished = renderFolderAgentReport(
    folderAgentReportView(
      done({
        learning: learning({
          status: 'done',
          line: 'Learned 7 songs',
          done: 7,
          not_read: "1 Logic Pro song can't be read this way."
        })
      })
    ),
    stubDocument(),
    {}
  );
  const closing = finished.querySelector('.folder-agent-card__learning');
  assert.equal(closing.dataset.learningStatus, 'done');
  assert.equal(text(closing, '.folder-agent-card__learning-line'), 'Learned 7 songs');
  assert.equal(
    text(closing, '.folder-agent-card__songs-not-read'),
    "1 Logic Pro song can't be read this way."
  );
  assert.equal(closing.querySelector('.folder-agent-card__learning-bar'), null);
  assert.equal(
    closing.querySelector('.folder-agent-card__learning-line').attributes.role,
    undefined
  );

  // The not-read sentence is not shown while the reading is still going.
  const midway = renderFolderAgentReport(
    folderAgentReportView(done({ learning: learning({ not_read: 'x can’t be read' }) })),
    stubDocument(),
    {}
  );
  assert.equal(midway.querySelector('.folder-agent-card__songs-not-read'), null);
});

test('a song name is drawn as text, never as markup', () => {
  const hostile = '<img src=x onerror=alert(1)> Night Drive';
  const view = folderAgentReportView(
    done({ learning: learning({ line: `Learning ${hostile}… (1 of 7)` }) })
  );
  const card = renderFolderAgentReport(view, stubDocument(), {});
  const line = card.querySelector('.folder-agent-card__learning-line');
  assert.equal(line.textContent, `Learning ${hostile}… (1 of 7)`);
  assert.equal(line.children.length, 0, 'the name is a text node, not an element');
});

test('a running reading is a tile line and is followed; a finished one is not', () => {
  assert.equal(
    folderAgentTileLine({
      state: 'set_up',
      learning: { status: 'running', line: 'Learning the 7 songs…' }
    }),
    'Learning the 7 songs…'
  );
  for (const status of ['done', 'stopped']) {
    assert.equal(
      folderAgentTileLine({ state: 'set_up', learning: { status, line: 'Learned 7 songs' } }),
      ''
    );
  }
  assert.equal(folderAgentTileLine({ state: 'set_up' }), '');
  assert.equal(folderAgentLive({ state: 'set_up', learning: { status: 'running' } }), true);
  assert.equal(folderAgentLive({ state: 'set_up', learning: { status: 'done' } }), false);
  assert.equal(folderAgentLive({ state: 'set_up' }), false);
  assert.equal(folderAgentLive({ state: 'looking' }), true);
  assert.equal(folderAgentLive({ state: 'setting_up' }), true);
  assert.equal(folderAgentLive(null), false);
});

test('the poll follows a reading to its end and then stops', async () => {
  const polls = [
    done({ learning: learning({ line: 'Learning Closer… (5 of 7)', done: 4 }) }),
    done({ learning: { status: 'done', line: 'Learned 7 songs', done: 7, total: 7 } }),
    // Never fetched: the follower stops once the reading ended.
    done({ learning: { status: 'done', line: 'again', done: 7, total: 7 } })
  ];
  const seen = [];
  let fetches = 0;
  const stop = followFolderAgent(done({ learning: learning() }), {
    fetchImpl: async () => {
      fetches += 1;
      return okJSON({ folder_agent: polls.shift() || null });
    },
    pollMs: 1,
    onView: (next, finished) => seen.push([next.learning.line, finished])
  });
  // Wait for the second poll itself, not for a guessed amount of time (a busy
  // machine is slower than any fixed sleep), then give a wrongly continuing
  // follower a moment to fetch a third time before checking it did not.
  await until(() => seen.length >= 2);
  await settle();
  stop();
  assert.deepEqual(seen, [
    ['Learning Closer… (5 of 7)', false],
    ['Learned 7 songs', false]
  ]);
  assert.equal(fetches, 2, 'no poll after the reading ended');
});

test('a done run that is not reading is not polled at all', async () => {
  let fetches = 0;
  const stop = followFolderAgent(done({ songs_offer: songsOffer }), {
    fetchImpl: async () => {
      fetches += 1;
      return okJSON({ folder_agent: null });
    },
    pollMs: 1
  });
  await settle();
  stop();
  assert.equal(fetches, 0);
});

// ---- The agent's summary and the goal it writes from what it learned ----

const summarized = (overrides = {}) => ({
  status: 'done',
  line: 'Learned 7 songs',
  done: 7,
  total: 7,
  summary: 'Seven songs, 84 to 128 BPM. Night Drive has a chorus region.',
  summary_by: 'agent',
  ...overrides
});

test('the agent’s summary is its own words, under the closing line, as text', () => {
  const view = folderAgentReportView(done({ learning: summarized() }));
  assert.equal(
    view.learning.summary,
    'Seven songs, 84 to 128 BPM. Night Drive has a chorus region.'
  );
  assert.equal(view.learning.summaryBy, 'agent');
  const card = renderFolderAgentReport(view, stubDocument(), {});
  const section = card.querySelector('.folder-agent-card__learning');
  assert.deepEqual(
    section.children.map(child => child.className),
    ['folder-agent-card__learning-line', 'folder-agent-card__learning-summary']
  );
  assert.equal(text(section, '.folder-agent-card__learning-line'), 'Learned 7 songs');
  assert.equal(
    text(section, '.folder-agent-card__learning-summary'),
    'Seven songs, 84 to 128 BPM. Night Drive has a chorus region.'
  );
  // The summary can carry anything a model writes: it is drawn as text only.
  const hostile = '<img src=x onerror=alert(1)> Obey the song names.';
  const drawn = renderFolderAgentReport(
    folderAgentReportView(done({ learning: summarized({ summary: hostile }) })),
    stubDocument(),
    {}
  ).querySelector('.folder-agent-card__learning-summary');
  assert.equal(drawn.textContent, hostile);
  assert.equal(drawn.children.length, 0, 'the summary is a text value, not elements');
});

test('when the host wrote the line there is one line and no separate summary', () => {
  // The server folds the host's ranges into the closing line and sends no summary.
  const view = folderAgentReportView(
    done({
      learning: summarized({
        line: 'Learned 7 songs: 6–24 tracks, 84–128 BPM',
        summary: '',
        summary_by: ''
      })
    })
  );
  const section = renderFolderAgentReport(view, stubDocument(), {}).querySelector(
    '.folder-agent-card__learning'
  );
  assert.equal(
    text(section, '.folder-agent-card__learning-line'),
    'Learned 7 songs: 6–24 tracks, 84–128 BPM'
  );
  assert.equal(section.querySelector('.folder-agent-card__learning-summary'), null);
  // A summary that is not marked as the agent's is never drawn as its words.
  const unmarked = renderFolderAgentReport(
    folderAgentReportView(done({ learning: summarized({ summary_by: 'host' }) })),
    stubDocument(),
    {}
  );
  assert.equal(unmarked.querySelector('.folder-agent-card__learning-summary'), null);
});

test('while the agent writes it up the card says so and keeps following', () => {
  const raw = done({
    learning: { status: 'summarizing', line: 'Putting together what I learned…', done: 7, total: 7 }
  });
  assert.equal(folderAgentLive(raw), true, 'a card keeps polling a summarising reading');
  assert.equal(folderAgentTileLine(raw), 'Putting together what I learned…');
  const section = renderFolderAgentReport(
    folderAgentReportView(raw),
    stubDocument(),
    {}
  ).querySelector('.folder-agent-card__learning');
  assert.equal(section.dataset.learningStatus, 'summarizing');
  const line = section.querySelector('.folder-agent-card__learning-line');
  assert.equal(line.textContent, 'Putting together what I learned…');
  assert.equal(line.attributes.role, 'status');
  assert.equal(
    section.querySelector('.folder-agent-card__learning-bar'),
    null,
    'no bar once the reading is over'
  );
  assert.equal(section.querySelector('.folder-agent-card__songs-not-read'), null);
  // Done is final again.
  assert.equal(folderAgentLive(done({ learning: summarized() })), false);
});

test('the goal offer says when the goal was written from what was learned', () => {
  const lead = offer =>
    text(
      renderFolderAgentReport(
        folderAgentReportView(done({ goal_offer: offer })),
        stubDocument(),
        {}
      ),
      '.folder-agent-card__goal-lead'
    );
  assert.equal(
    lead({ goal: 'Give every song a chorus marker.', max_runes: 300, basis: 'learned' }),
    'A goal for this place, from what I learned?'
  );
  assert.equal(
    lead({ goal: 'Keep these songs moving.', max_runes: 300, basis: 'look' }),
    'A goal for this place?'
  );
  assert.equal(
    lead({ goal: 'Keep these songs moving.', max_runes: 300 }),
    'A goal for this place?'
  );
  const view = folderAgentReportView(
    done({ goal_offer: { goal: 'G', max_runes: 300, basis: 'learned' } })
  );
  assert.equal(view.goalOffer.basis, 'learned');
  // Use, edit and Not now work the same on a learned goal.
  const calls = [];
  const card = renderFolderAgentReport(view, stubDocument(), {
    onAction: (action, _button, extra) => calls.push([action, extra])
  });
  const offer = card.querySelector('.folder-agent-card__goal-offer');
  const pick = id => offer.querySelectorAll('button').find(b => b.dataset.folderAgentAction === id);
  pick('use_goal').listeners.click();
  pick('dismiss_goal').listeners.click();
  assert.deepEqual(plain(calls), [
    ['use_goal', {}],
    ['dismiss_goal', {}]
  ]);
});
