import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

import {
  BRIEF_KEY_PREFIX,
  createHomeOffer,
  homeOfferCardState,
  homeOfferMessage,
  homeOfferPresentation,
  homePageRoute,
  NOT_NOW_KEY_PREFIX,
  NOTHING_OPENED,
  OFFER_LIMIT,
  readNotNow,
  revealOpenedSong,
  unavailableLine,
  waitsHereLine
} from './personal-assistant-home-offer.js';
import { songSuggestionView } from './home-song-offer.js';

const source = readFileSync(new URL('./personal-assistant-home-offer.js', import.meta.url), 'utf8');

// The plan-line renderer is the browser's; these tests read the card's own parts.
globalThis.OriFolderPlanLines.renderSetupLines = (list, lines, doc) => {
  for (const line of lines)
    list.append(Object.assign(doc.createElement('li'), { textContent: line.name }));
};

class FakeElement {
  constructor(tag) {
    this.tagName = tag.toUpperCase();
    this.children = [];
    this.parent = null;
    this.attributes = {};
    this.dataset = {};
    this.listeners = {};
    this.hidden = false;
    this.disabled = false;
    this.value = '';
    this.ownText = '';
    this.className = '';
    this.innerHTML = '';
  }
  get isConnected() {
    return true;
  }
  get textContent() {
    return this.ownText + this.children.map(child => child.textContent).join('');
  }
  set textContent(value) {
    this.children = [];
    this.ownText = String(value ?? '');
  }
  append(...nodes) {
    for (const node of nodes) {
      const child =
        typeof node === 'string'
          ? Object.assign(new FakeElement('#text'), { ownText: node })
          : node;
      child.parent = this;
      this.children.push(child);
    }
  }
  replaceChildren(...nodes) {
    this.children = [];
    this.ownText = '';
    this.append(...nodes);
  }
  setAttribute(name, value) {
    this.attributes[name] = String(value);
  }
  getAttribute(name) {
    return this.attributes[name] ?? null;
  }
  addEventListener(type, handler) {
    (this.listeners[type] ||= []).push(handler);
  }
  dispatch(type) {
    for (const handler of this.listeners[type] || []) handler({ type, target: this });
  }
  click() {
    if (!this.disabled) this.dispatch('click');
  }
  focus() {
    focused = this;
  }
  all() {
    return this.children.flatMap(child => [child, ...child.all()]);
  }
  matches(selector) {
    if (selector.startsWith('.')) return this.className.split(/\s+/).includes(selector.slice(1));
    const data = /^\[data-([a-z-]+)\]$/.exec(selector);
    if (data) {
      const key = data[1].replace(/-([a-z])/g, (_, c) => c.toUpperCase());
      return key in this.dataset;
    }
    return this.tagName === selector.toUpperCase();
  }
  querySelectorAll(selector) {
    return this.all().filter(node => node.matches(selector));
  }
  querySelector(selector) {
    return this.querySelectorAll(selector)[0] || null;
  }
  contains(node) {
    for (let at = node; at; at = at.parent) if (at === this) return true;
    return false;
  }
  closest(selector) {
    for (let at = this; at; at = at.parent) if (at.matches?.(selector)) return at;
    return null;
  }
}

let focused = null;
const doc = {
  createElement: tag => new FakeElement(tag),
  get activeElement() {
    return focused;
  }
};

function memoryStorage() {
  const map = new Map();
  return {
    getItem: key => (map.has(key) ? map.get(key) : null),
    setItem: (key, value) => map.set(key, String(value)),
    removeItem: key => map.delete(key),
    map
  };
}

const lines = [
  { kind: 'library', name: 'Lists Album-5 in the Home’s library' },
  { kind: 'handoff', name: 'Your Music Portfolio Manager then suggests opening Album-5' },
  { kind: 'workspace', name: 'Creates a REAPER song workspace named Album-5' }
];
const hire = {
  mode: 'create',
  agent_name: 'REAPER Assistant',
  role_label: 'REAPER Assistant',
  model: 'gpt-5-mini'
};

function suggestion(id, song, extra = {}) {
  return {
    id,
    status: 'pending',
    subject: { name: song },
    plan: {
      digest: `digest-${id}`,
      lines: lines.map(line => ({ ...line, name: line.name.replace('Album-5', song) })),
      hire,
      brief_default: 'Suggest the next step.'
    },
    ...extra
  };
}

const manager = {
  kind: 'workspace_entry',
  key: 'workspace_entry',
  workspaceId: 'home-1',
  workspaceName: 'Music Production Home',
  name: 'Music Portfolio Manager',
  appearance: { color: 'teal' },
  available: true,
  disabled: false
};
const personal = {
  kind: 'personal',
  key: 'personal',
  name: 'Ada',
  available: true,
  disabled: false
};
const onManager = { current: manager, options: [manager, personal], pickerVisible: true };
const onPersonal = { current: personal, options: [manager, personal], pickerVisible: true };

function respond(status, body) {
  return { ok: status >= 200 && status < 300, status, json: async () => body };
}

function harness({
  resolution = onManager,
  suggestions = [suggestion('offer-5', 'Album-5')],
  fetchImpl,
  arrival = false,
  isOpen = () => true
} = {}) {
  const mount = new FakeElement('div');
  const storage = memoryStorage();
  const calls = [];
  let current = resolution;
  let listed = suggestions;
  const offer = createHomeOffer({
    doc,
    mount,
    storage,
    arrival,
    isOpen,
    recipients: () => ({ resolution: () => current }),
    pathname: () => '/workspaces/music-home',
    avatar: who => `<svg data-for="${who?.name}"></svg>`,
    fetchImpl:
      fetchImpl ||
      (async (url, init) => {
        calls.push({ url, method: init?.method || 'GET' });
        return respond(200, { suggestions: listed });
      })
  });
  return {
    offer,
    mount,
    storage,
    calls,
    setResolution: next => (current = next),
    setSuggestions: next => (listed = next)
  };
}

test('the offer is shown only while the Home’s entry agent is the recipient', () => {
  const views = [songSuggestionView(suggestion('offer-5', 'Album-5'))];
  const offers = homeOfferPresentation({ resolution: onManager, views });
  assert.equal(offers.kind, 'offers');
  assert.equal(offers.manager.name, 'Music Portfolio Manager');
  assert.deepEqual(offers.manager.appearance, { color: 'teal' });
  assert.equal(offers.homeName, 'Music Production Home');
  // The plan says "Your Music Portfolio Manager"; the card uses the agent's own name.
  assert.equal(views[0].manager, 'Music Portfolio Manager');
  const renamed = homeOfferPresentation({
    resolution: { ...onManager, current: { ...manager, name: 'Portfolio Manager' } },
    views
  });
  assert.equal(renamed.views[0].manager, 'Portfolio Manager');
  // Under the Personal Assistant there is no card (FR3, FR11).
  assert.equal(homeOfferPresentation({ resolution: onPersonal, views }).kind, 'none');
  // No suggestion, no message.
  assert.equal(homeOfferPresentation({ resolution: onManager, views: [] }).kind, 'none');
  // Not a workspace page: nothing.
  assert.equal(
    homeOfferPresentation({ resolution: { current: personal, options: [] }, views }).kind,
    'none'
  );
});

test('a vacant or missing manager leaves one line to the Home’s page (FR3)', () => {
  const views = [songSuggestionView(suggestion('offer-5', 'Album-5'))];
  for (const entry of [
    { ...manager, name: '', available: false, vacant: true, disabled: true },
    { ...manager, available: false, disabled: true }
  ]) {
    const shown = homeOfferPresentation({
      resolution: { current: personal, options: [entry, personal], pickerVisible: true },
      views
    });
    assert.equal(shown.kind, 'unavailable');
    assert.equal(unavailableLine(shown.views[0]), 'Open Album-5 from the Home’s page');
  }
  assert.equal(homePageRoute('/workspaces/music-home'), '/workspaces/music-home/assistant');
  assert.equal(
    homePageRoute('/workspaces/music-home/assistant'),
    '/workspaces/music-home/assistant'
  );
  assert.equal(homePageRoute('/'), '');
});

test('the manager’s words name the song, the Home and the hire', () => {
  const view = songSuggestionView(suggestion('offer-5', 'Album-5'));
  assert.equal(
    homeOfferMessage(view, 'Music Production Home'),
    'I listed Album-5 in Music Production Home. Want me to open it and hire the REAPER Assistant? Tell me what it should do with it.'
  );
  assert.equal(
    waitsHereLine(view),
    'Album-5 waits here. Open it from the Home’s page when you’re ready.'
  );
});

test('at most five songs, newest first as the Home’s read lists them (FR6)', () => {
  const views = Array.from({ length: 7 }, (_, i) =>
    songSuggestionView(suggestion(`offer-${i}`, `Song ${i}`))
  );
  const shown = homeOfferPresentation({ resolution: onManager, views });
  assert.equal(OFFER_LIMIT, 5);
  assert.deepEqual(
    shown.views.map(view => view.offerID),
    ['offer-0', 'offer-1', 'offer-2', 'offer-3', 'offer-4']
  );
});

test('card states follow the shared view, with this tab’s press and Not now', () => {
  const view = songSuggestionView(suggestion('offer-5', 'Album-5'));
  assert.equal(homeOfferCardState(view), 'offer');
  assert.equal(homeOfferCardState(view, { pressing: true }), 'running');
  assert.equal(homeOfferCardState({ ...view, running: true }), 'running');
  assert.equal(homeOfferCardState(view, { notNow: true }), 'notNow');
  assert.equal(homeOfferCardState({ ...view, waitsElsewhere: true }), 'elsewhere');
  assert.equal(homeOfferCardState(view, { opened: {} }), 'opened');
  // A run already on is shown running even after Not now in this tab.
  assert.equal(homeOfferCardState({ ...view, running: true }, { notNow: true }), 'running');
});

test('the drawer draws the manager’s message and card, escaped, and saves nothing', async () => {
  const name = 'Song <img src=x onerror=alert(1)>';
  const song = suggestion('offer-5', name);
  song.plan.entry_name = 'Song <img>.rpp';
  song.plan.entry_candidates = ['Song <img>.rpp', 'Alternate.rpp'];
  const { offer, mount, calls } = harness({ suggestions: [song] });
  await offer.refresh();
  assert.equal(mount.hidden, false);
  const article = mount.querySelector('.pa-home-offer');
  assert.equal(article.dataset.offerId, 'offer-5');
  const author = article.querySelector('.personal-assistant-message__author');
  assert.match(author.textContent, /Music Portfolio Manager/);
  assert.equal(
    author.querySelector('.personal-assistant-message__author-avatar').innerHTML,
    '<svg data-for="Music Portfolio Manager"></svg>'
  );
  const words = article.querySelector('.pa-home-offer__text');
  // Song names are text, never markup.
  assert.equal(words.innerHTML, '');
  assert.match(
    words.textContent,
    /^I listed Song <img src=x onerror=alert\(1\)> in Music Production Home\./
  );
  assert.match(
    article.querySelector('.pa-home-offer__hire').textContent,
    /Hires REAPER Assistant · uses gpt-5-mini/
  );
  const file = article.querySelector('.folder-entry-choice__caption');
  assert.equal(file.textContent, 'Opens Song <img>.rpp');
  assert.equal(file.innerHTML, '', 'a filename must remain text, not markup');
  assert.equal(article.querySelector('textarea').value, 'Suggest the next step.');
  const buttons = article.querySelectorAll('button').map(button => button.textContent);
  assert.deepEqual(buttons, [`Open ${name} & hire`, 'Not now']);
  // One read, no session, no turn, no post.
  assert.deepEqual(calls, [
    { url: '/api/personal-assistant/folder-digest/home-suggestions?home_id=home-1', method: 'GET' }
  ]);
  assert.doesNotMatch(source, /\/api\/(sessions|chat)/);
});

test('switching to the Personal Assistant hides the card; switching back restores it', async () => {
  const { offer, mount, setResolution } = harness();
  await offer.refresh();
  assert.ok(mount.querySelector('.pa-home-offer'));
  setResolution(onPersonal);
  offer.render();
  assert.equal(mount.querySelector('.pa-home-offer'), null);
  assert.equal(mount.hidden, true);
  setResolution(onManager);
  offer.render();
  assert.ok(mount.querySelector('.pa-home-offer'));
});

test('the composer’s brief and the card’s field write one value; last write wins (FR9)', async () => {
  const { offer, mount, storage } = harness();
  const seen = [];
  offer.onChange(state => seen.push(state.pending.map(view => view.offerID)));
  await offer.refresh();
  assert.deepEqual(seen.at(-1), ['offer-5']);
  // A reply to the manager lands in the card's field.
  offer.setBrief('offer-5', 'clean up the drums and bounce stems');
  const field = mount.querySelector('textarea');
  assert.equal(field.value, 'clean up the drums and bounce stems');
  assert.equal(
    storage.getItem(`${BRIEF_KEY_PREFIX}offer-5`),
    'clean up the drums and bounce stems'
  );
  // Then an edit in the field is the brief the press sends.
  field.value = 'bounce stems only';
  field.dispatch('input');
  const view = offer.state().pending[0];
  assert.equal(offer.brief(view), 'bounce stems only');
  // A redraw (a later read) keeps it.
  await offer.refresh();
  assert.equal(mount.querySelector('textarea').value, 'bounce stems only');
});

test('Not now leaves no song pending, so the composer leaves brief mode (FR10)', async () => {
  const { offer, mount } = harness();
  await offer.refresh();
  assert.equal(offer.state().pending.length, 1);
  mount
    .querySelectorAll('button')
    .find(button => button.textContent === 'Not now')
    .click();
  assert.deepEqual(offer.state().pending, []);
  assert.equal(offer.state().kind, 'offers');
});

test('a song that stops being suggested leaves the conversation (FR5)', async () => {
  const { offer, mount, setSuggestions } = harness({
    suggestions: [suggestion('offer-6', 'Second Song'), suggestion('offer-5', 'Album-5')]
  });
  await offer.refresh();
  assert.deepEqual(
    mount.querySelectorAll('.pa-home-offer').map(node => node.dataset.offerId),
    ['offer-6', 'offer-5']
  );
  setSuggestions([suggestion('offer-6', 'Second Song')]);
  await offer.refresh();
  assert.deepEqual(
    mount.querySelectorAll('.pa-home-offer').map(node => node.dataset.offerId),
    ['offer-6']
  );
});

test('Not now collapses the card to one line, per tab, and tells the server nothing', async () => {
  const { offer, mount, storage, calls } = harness();
  await offer.refresh();
  const later = mount.querySelectorAll('button').find(button => button.textContent === 'Not now');
  later.click();
  const article = mount.querySelector('.pa-home-offer');
  assert.equal(article.dataset.state, 'notNow');
  assert.match(
    article.querySelector('.pa-home-offer__waits').textContent,
    /^Album-5 waits here\. Open it from the Home’s page when you’re ready\./
  );
  assert.equal(article.querySelector('a').href, '/workspaces/music-home/assistant');
  assert.equal(article.querySelector('button'), null);
  assert.deepEqual([...readNotNow('home-1', storage)], ['offer-5']);
  assert.ok(storage.map.has(`${NOT_NOW_KEY_PREFIX}home-1`));
  assert.equal(focused, article);
  assert.ok(calls.every(call => call.method === 'GET'));
  // The next read in this tab keeps it collapsed.
  await offer.refresh();
  assert.equal(mount.querySelector('.pa-home-offer').dataset.state, 'notNow');
});

test('the brief typed in the card is kept per tab and sent with the press', async () => {
  const posts = [];
  let resolved = false;
  const { offer, mount, storage } = harness({
    fetchImpl: async (url, init) => {
      if (init?.method === 'POST') {
        posts.push(JSON.parse(init.body));
        return respond(202, {});
      }
      if (url.includes('offer_id=')) {
        resolved = true;
        return respond(200, {
          folder_digest: {
            offer: {
              id: 'offer-5',
              status: 'resolved',
              outcome: {
                route: '/workspaces/album-5',
                workspace_id: 'song-1',
                receipt: [
                  { kind: 'agent', name: 'REAPER Assistant', detail: 'added', assigned: true },
                  { kind: 'task', name: 'Summarize Album-5', detail: 'ready to start' }
                ]
              }
            }
          }
        });
      }
      return respond(200, { suggestions: resolved ? [] : [suggestion('offer-5', 'Album-5')] });
    }
  });
  await offer.refresh();
  const field = mount.querySelector('textarea');
  field.value = 'clean up the drums';
  field.dispatch('input');
  assert.equal(storage.getItem(`${BRIEF_KEY_PREFIX}offer-5`), 'clean up the drums');
  const view = songSuggestionView(suggestion('offer-5', 'Album-5'));
  const ran = await offer.run(view, offer.brief(view), {});
  assert.equal(ran, true);
  assert.equal(posts.length, 1);
  assert.equal(posts[0].brief, 'clean up the drums');
  assert.equal(posts[0].open, true);
  // The receipt replaces the card, in the manager's name (FR17).
  const article = mount.querySelector('.pa-home-offer');
  assert.equal(article.dataset.state, 'opened');
  assert.match(article.textContent, /Music Portfolio Manager opened it/);
  assert.match(article.textContent, /Album-5 is open/);
  assert.match(article.textContent, /REAPER Assistant — added/);
  assert.match(article.textContent, /Summarize Album-5 — ready to start/);
  const link = article.querySelector('a');
  assert.equal(link.textContent, 'Open Album-5');
  assert.equal(link.href, '/workspaces/album-5');
});

test('one press per card: a running card cannot be pressed again', async () => {
  let posts = 0;
  let release;
  const gate = new Promise(resolve => (release = resolve));
  const { offer, mount } = harness({
    fetchImpl: async (url, init) => {
      if (init?.method === 'POST') {
        posts += 1;
        await gate;
        return respond(400, { error: 'Stopped.' });
      }
      return respond(200, { suggestions: [suggestion('offer-5', 'Album-5')] });
    }
  });
  await offer.refresh();
  const view = songSuggestionView(suggestion('offer-5', 'Album-5'));
  const first = offer.open(view);
  await Promise.resolve();
  const press = mount.querySelector('[data-home-offer-press]');
  assert.equal(press.disabled, true);
  assert.equal(press.textContent, 'Music Portfolio Manager is opening Album-5…');
  await offer.open(view);
  release();
  await first;
  assert.equal(posts, 1);
  // A refused press says why on the card and leaves it pressable.
  assert.equal(mount.querySelector('.pa-home-offer__status').textContent, 'Stopped.');
  assert.equal(mount.querySelector('[data-home-offer-press]').disabled, false);
});

test('a song stopped elsewhere links to the assistant’s card with the existing copy', async () => {
  const stopped = {
    id: 'offer-5',
    status: 'awaiting_outcome',
    subject: { name: 'Album-5' },
    setup: { plan_digest: 'd', status: 'stopped', stop_reason: 'needs_model', lines, hire }
  };
  const { offer, mount } = harness({ suggestions: [stopped] });
  await offer.refresh();
  const article = mount.querySelector('.pa-home-offer');
  assert.equal(article.dataset.state, 'elsewhere');
  assert.match(article.querySelector('.pa-home-offer__status').textContent, /needs a model first/);
  const link = article.querySelector('a');
  assert.equal(link.textContent, 'Go to your assistant');
  assert.equal(link.href, '/');
});

test('a song with several project files is answered in the card, and its press carries the file', async () => {
  let resolved = false;
  const posts = [];
  const stopped = {
    id: 'offer-5',
    status: 'awaiting_outcome',
    subject: { name: 'Album-5' },
    setup: {
      plan_digest: 'run-digest',
      status: 'stopped',
      stop_reason: 'needs_choice',
      entry_candidates: ['Album-5.rpp', 'Album-5 (mix).rpp'],
      brief: 'Mix it.',
      hire,
      lines
    }
  };
  const { offer, mount } = harness({
    fetchImpl: async (url, init) => {
      if (init?.method === 'POST') {
        posts.push(JSON.parse(init.body));
        return respond(202, {});
      }
      if (url.includes('offer_id=')) {
        resolved = true;
        return respond(200, {
          folder_digest: {
            offer: { id: 'offer-5', status: 'resolved', outcome: { route: '/workspaces/album-5' } }
          }
        });
      }
      return respond(200, { suggestions: resolved ? [] : [stopped] });
    }
  });
  await offer.refresh();
  const article = mount.querySelector('.pa-home-offer');
  assert.equal(article.dataset.state, 'choose');
  assert.match(
    article.querySelector('.pa-home-offer__ask').textContent,
    /^Which project file should I open for Album-5\?$/
  );
  assert.equal(article.querySelector('.pa-home-offer__status').textContent, NOTHING_OPENED);
  // No "Go to your assistant": the choice is made here. The run's brief stays.
  assert.equal(article.querySelector('a'), null);
  assert.equal(article.querySelector('textarea').disabled, true);
  // Brief mode is off while a file is being chosen.
  assert.deepEqual(offer.state().pending, []);
  const choices = mount.querySelectorAll('[data-home-offer-choice]');
  assert.deepEqual(
    choices.map(button => button.textContent),
    ['Album-5.rpp', 'Album-5 (mix).rpp']
  );
  choices[1].click();
  await new Promise(resolve => setImmediate(resolve));
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(posts.length, 1);
  assert.equal(posts[0].entry_name, 'Album-5 (mix).rpp');
  assert.equal(posts[0].plan_digest, 'run-digest');
  assert.equal(posts[0].open, true);
});

test('a press that stops says nothing was opened or placed', async () => {
  let stoppedNow = false;
  const { offer, mount } = harness({
    fetchImpl: async (url, init) => {
      if (init?.method === 'POST') {
        stoppedNow = true;
        return respond(202, {});
      }
      if (url.includes('offer_id='))
        return respond(200, {
          folder_digest: { offer: { id: 'offer-5', setup: { status: 'stopped' } } }
        });
      return respond(200, {
        suggestions: [
          stoppedNow
            ? {
                id: 'offer-5',
                status: 'awaiting_outcome',
                subject: { name: 'Album-5' },
                setup: {
                  plan_digest: 'd',
                  status: 'stopped',
                  stop_reason: 'needs_model',
                  lines,
                  hire
                }
              }
            : suggestion('offer-5', 'Album-5')
        ]
      });
    }
  });
  await offer.refresh();
  const view = offer.state().pending[0];
  assert.equal(await offer.run(view, ''), false);
  const status = mount.querySelector('.pa-home-offer__status').textContent;
  assert.match(status, /needs a model first/);
  assert.match(status, /Nothing was opened or placed yet\.$/);
});

test('a vacant manager renders only the line to the Home’s page', async () => {
  const vacant = { ...manager, name: '', available: false, vacant: true, disabled: true };
  const { offer, mount } = harness({
    resolution: { current: personal, options: [vacant, personal], pickerVisible: true }
  });
  await offer.refresh();
  assert.equal(mount.querySelector('.pa-home-offer'), null);
  const link = mount.querySelector('.pa-home-offer__unavailable').querySelector('a');
  assert.equal(link.textContent, 'Open Album-5 from the Home’s page');
  assert.equal(link.href, '/workspaces/music-home/assistant');
});

test('the opened song is shown on the Home’s map through the workspaces-changed signal', () => {
  const events = [];
  const win = { dispatchEvent: event => events.push(event) };
  assert.equal(revealOpenedSong({ workspaceID: 'song-1' }, win), true);
  assert.equal(events[0].type, 'ori:workspaces-changed');
  assert.deepEqual(events[0].detail, { workspaceId: 'song-1', reason: 'home-song-opened' });
  assert.equal(revealOpenedSong({ workspaceID: '' }, win), false);
  assert.equal(events.length, 1);
});

test('a redraw keeps focus on the same message or control (FR22)', async () => {
  const { offer, mount } = harness();
  await offer.refresh();
  mount.querySelector('.pa-home-offer').focus();
  await offer.refresh();
  assert.equal(focused, mount.querySelector('.pa-home-offer'), 'the new message has focus');
  mount.querySelector('textarea').focus();
  await offer.refresh();
  assert.equal(focused, mount.querySelector('textarea'), 'the new brief field has focus');
  mount.querySelector('[data-home-offer-press]').focus();
  offer.render();
  assert.equal(focused, mount.querySelector('[data-home-offer-press]'));
  // Focus elsewhere on the page is never taken by a redraw.
  const outside = new FakeElement('button');
  outside.focus();
  await offer.refresh();
  assert.equal(focused, outside);
});

test('arrival focus waits for the open drawer, then lands on the manager’s message once', async () => {
  let open = false;
  focused = null;
  const { offer, mount } = harness({ arrival: true, isOpen: () => open });
  await offer.refresh();
  assert.equal(focused, null, 'a closed drawer takes no focus');
  // The drawer opens; its own opening focus came first.
  open = true;
  const header = new FakeElement('button');
  header.focus();
  assert.equal(offer.arrive(), true);
  assert.equal(focused, mount.querySelector('.pa-home-offer'));
  // Only once: a later opening keeps the drawer's own focus.
  header.focus();
  assert.equal(offer.arrive(), false);
  await offer.refresh();
  assert.equal(focused, header);
});

test('an unchanged read leaves the drawn nodes in place; a change redraws', async () => {
  const { offer, mount, setSuggestions } = harness();
  await offer.refresh();
  const press = mount.querySelector('[data-home-offer-press]');
  await offer.refresh();
  offer.render();
  assert.equal(mount.querySelector('[data-home-offer-press]'), press, 'same node, nothing redrawn');
  setSuggestions([suggestion('offer-6', 'Second Song'), suggestion('offer-5', 'Album-5')]);
  await offer.refresh();
  assert.equal(mount.querySelectorAll('.pa-home-offer').length, 2);
});

test('arrival can be learned from the drawer after the address bar lost it', async () => {
  let consumed = '';
  focused = null;
  const { offer, mount } = harness({ arrival: () => consumed === 'assistant' });
  // The getter is read when the offer is first drawn in the open drawer.
  consumed = 'assistant';
  await offer.refresh();
  assert.equal(focused, mount.querySelector('.pa-home-offer'));
});

test('a recipient re-read keeps the drawn offer and its focus', async () => {
  const { offer, mount, setResolution } = harness();
  await offer.refresh();
  const article = mount.querySelector('.pa-home-offer');
  article.focus();
  setResolution({
    current: { ...manager, available: false, loading: true },
    options: [{ ...manager, available: false, loading: true }, personal],
    pickerVisible: true
  });
  offer.render();
  assert.equal(mount.querySelector('.pa-home-offer'), article, 'nothing is redrawn while loading');
  assert.equal(focused, article);
  // An empty redraw (the Personal Assistant, briefly) gives focus back after.
  setResolution(onPersonal);
  offer.render();
  focused = null;
  setResolution(onManager);
  offer.render();
  assert.equal(focused, mount.querySelector('.pa-home-offer'));
});

test('a failed read keeps what this Home showed', async () => {
  let fail = false;
  const { offer, mount } = harness({
    fetchImpl: async () =>
      fail ? respond(500, {}) : respond(200, { suggestions: [suggestion('offer-5', 'Album-5')] })
  });
  await offer.refresh();
  fail = true;
  await offer.refresh();
  assert.ok(mount.querySelector('.pa-home-offer'));
});
