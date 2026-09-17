import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const source = readFileSync(new URL('./result-card.js', import.meta.url), 'utf8');
const toastSource = readFileSync(new URL('./stage-up-toast.js', import.meta.url), 'utf8');

function load({ reducedMotion = true } = {}) {
  const storage = new Map();
  const window = {
    matchMedia: () => ({ matches: reducedMotion }),
    localStorage: {
      getItem: key => (storage.has(key) ? storage.get(key) : null),
      setItem: (key, value) => storage.set(key, String(value))
    }
  };
  const sandbox = { window, console: { warn() {}, error() {} }, setTimeout, fetch: undefined };
  vm.runInNewContext(toastSource, sandbox, { filename: 'stage-up-toast.js' });
  vm.runInNewContext(source, sandbox, { filename: 'result-card.js' });
  return { card: window.OriResultCard, storage };
}

const payload = (overrides = {}) => ({
  parcel: {
    id: 'p1',
    workspace_id: 'ws-1',
    kind: 'task',
    ref_id: 't1',
    title: 'Compare launch notes',
    agent_name: 'Theo',
    outcome: 'succeeded',
    produced_at: '2026-09-16T10:00:00Z'
  },
  duration_seconds: 102,
  summary: 'Two dates disagree.',
  rewards: {
    xp: {
      awarded: 50,
      level_before: 1,
      level_after: 1,
      progress_before: 0.2,
      progress_after: 0.7,
      stage_before: 'spark',
      stage_after: 'spark'
    },
    craft: 5
  },
  ...overrides
});

test('formatDuration reads like a person would say it', () => {
  const { card } = load();
  assert.equal(card.formatDuration(102), 'took 1m 42s');
  assert.equal(card.formatDuration(42), 'took 42s');
  assert.equal(card.formatDuration(3900), 'took 1h 5m');
  assert.equal(card.formatDuration(undefined), '');
  assert.equal(card.formatDuration(-3), '');
  assert.equal(card.formatDuration(0), '', 'a sub-second run shows no duration');
});

test('a finished run shows its title, outcome, duration, summary and rewards', () => {
  const { card } = load();
  const html = card.cardHTML(payload(), {
    workspaceName: 'Research Lab',
    avatarHTML: name => `<i data-avatar="${name}"></i>`
  });
  assert.match(html, /Research Lab/);
  assert.match(html, /data-avatar="Theo"/);
  assert.match(html, /Compare launch notes/);
  assert.match(html, /is-done">Done</);
  assert.match(html, /took 1m 42s/);
  assert.match(html, /Two dates disagree\./);
  assert.match(html, /\+50 XP/);
  assert.match(html, /\+5 Craft/);
  assert.match(html, /data-bar-from="0.2" data-bar-to="0.7"/);
  assert.match(html, /data-result-primary>Open full result</);
  assert.doesNotMatch(html, /data-result-stage/, 'no stage change, no banner');
});

test('a failed run shows its reason, says Needs a look, and offers the task', () => {
  const { card } = load();
  const html = card.cardHTML(
    payload({
      parcel: { ...payload().parcel, outcome: 'failed' },
      summary: '',
      failure_reason: 'The import file is missing.',
      rewards: undefined
    })
  );
  assert.match(html, /is-attention/);
  assert.match(html, />Needs a look</);
  assert.match(html, /The import file is missing\./);
  assert.match(html, /data-result-primary>Open task</);
  assert.doesNotMatch(html, /data-result-rewards/);
});

test('a brief and a janitor scan name where their primary action goes', () => {
  const { card } = load();
  assert.equal(card.primaryLabel({ kind: 'daily_brief', outcome: 'succeeded' }), 'Open brief');
  assert.equal(card.primaryLabel({ kind: 'daily_brief', outcome: 'failed' }), 'Open brief');
  assert.equal(card.primaryLabel({ kind: 'file_janitor', outcome: 'succeeded' }), 'Review files');
  const html = card.cardHTML(
    payload({
      parcel: { ...payload().parcel, kind: 'file_janitor', title: 'File Janitor', agent_name: '' },
      duration_seconds: 4,
      summary: '4 files are ready to review.',
      rewards: undefined
    })
  );
  assert.match(html, /4 files are ready to review\./);
  assert.match(html, /data-result-primary>Review files</);
});

test('rewards that were zero or unknown are left out, and so is an empty block', () => {
  const { card } = load();
  const onlyCraft = card.cardHTML(payload({ rewards: { xp: { awarded: 0 }, craft: 5 } }));
  assert.doesNotMatch(onlyCraft, /XP/);
  assert.match(onlyCraft, /\+5 Craft/);

  const nothing = card.cardHTML(payload({ rewards: { craft: 0 } }));
  assert.doesNotMatch(nothing, /data-result-rewards/);

  const emptySummary = card.cardHTML(payload({ summary: '' }));
  assert.match(emptySummary, /No summary from this run\./);
});

test('text from the run is escaped, never rendered', () => {
  const { card } = load();
  const html = card.cardHTML(
    payload({
      parcel: { ...payload().parcel, title: '<img src=x onerror=alert(1)>' },
      summary: '<script>boom</script>'
    }),
    { workspaceName: '<b>Lab</b>' }
  );
  assert.doesNotMatch(html, /<img|<script|<b>/);
  assert.match(html, /&lt;img src=x onerror=alert\(1\)&gt;/);
});

test('a stage change gets a banner, and the stage-up toast is told it was shown', async () => {
  const { card, storage } = load();
  const staged = payload({
    rewards: {
      xp: {
        awarded: 50,
        level_before: 1,
        level_after: 2,
        progress_before: 0.8,
        progress_after: 0.3,
        stage_before: 'spark',
        stage_after: 'infant'
      }
    }
  });
  assert.match(card.cardHTML(staged), /Theo reached Infant stage/);
  assert.match(card.cardHTML(staged), /Level 2 · level up/);

  const host = fakeHost();
  const shown = await card.open({
    host,
    parcelId: 'p1',
    fetchImpl: async () => ({ ok: true, status: 200, json: async () => staged })
  });
  assert.equal(shown.parcel.id, 'p1');
  assert.equal(storage.get('ori.evolution.lastStage.Theo'), 'infant');
});

// Just enough of an element for open(): the card and its two buttons.
function fakeHost() {
  const listeners = {};
  const make = name => {
    const own = {};
    return {
      name,
      focused: false,
      focus() {
        this.focused = true;
      },
      addEventListener: (type, fn) => (own[type] = fn),
      removeEventListener: type => delete own[type],
      fire: (type, event = {}) => own[type] && own[type](event),
      querySelectorAll: () => []
    };
  };
  const primary = make('primary');
  const closeButton = make('close');
  const card = make('card');
  card.querySelector = sel =>
    sel.includes('data-result-close')
      ? closeButton
      : sel.includes('data-result-primary')
        ? primary
        : null;
  let html = '';
  return {
    listeners,
    primary,
    closeButton,
    card,
    get innerHTML() {
      return html;
    },
    set innerHTML(value) {
      html = value;
    },
    querySelector: sel => (sel.includes('data-result-card') && html ? card : null)
  };
}

test('open posts to the parcel, focuses the primary action, and Esc returns focus', async () => {
  const { card } = load();
  const host = fakeHost();
  const origin = {
    focused: false,
    focus() {
      this.focused = true;
    }
  };
  const calls = [];
  let closed = 0;
  await card.open({
    host,
    parcelId: 'p 1',
    origin,
    onClose: () => closed++,
    fetchImpl: async (url, init) => {
      calls.push({ url, init });
      return { ok: true, status: 200, json: async () => payload() };
    }
  });
  assert.deepEqual(
    calls.map(c => [c.url, c.init.method]),
    [['/api/workspace-map/parcels/p%201/open', 'POST']]
  );
  assert.equal(host.primary.focused, true);
  assert.equal(card.isOpen(), true);

  host.card.fire('keydown', { key: 'Escape' });
  assert.equal(card.isOpen(), false);
  assert.equal(host.innerHTML, '');
  assert.equal(origin.focused, true, 'focus goes back to the pile');
  assert.equal(closed, 1);
});

test('the primary action hands over the payload and closes without stealing focus back', async () => {
  const { card } = load();
  const host = fakeHost();
  const origin = {
    focused: false,
    focus() {
      this.focused = true;
    }
  };
  let opened = null;
  await card.open({
    host,
    parcelId: 'p1',
    origin,
    onPrimary: p => (opened = p),
    fetchImpl: async () => ({ ok: true, status: 200, json: async () => payload() })
  });
  host.primary.fire('click');
  assert.equal(opened.parcel.ref_id, 't1');
  assert.equal(card.isOpen(), false);
  assert.equal(origin.focused, false);
});

test('a count that never gets a timer keeps its true value instead of +0', async () => {
  const storage = new Map();
  const window = {
    matchMedia: () => ({ matches: false }),
    localStorage: { getItem: () => null, setItem: (k, v) => storage.set(k, v) }
  };
  const parked = [];
  // A throttled tab: timers are accepted but never run.
  const sandbox = { window, console: { warn() {} }, setTimeout: fn => parked.push(fn) };
  vm.runInNewContext(source, sandbox, { filename: 'result-card.js' });
  const cards = window.OriResultCard;

  const count = {
    textContent: '+5 Craft',
    attrs: { 'data-count-to': '5', 'data-count-suffix': ' Craft' }
  };
  count.getAttribute = name => count.attrs[name];
  const host = fakeHost();
  host.card.querySelectorAll = sel => (sel.includes('data-count-to') ? [count] : []);
  await cards.open({
    host,
    parcelId: 'p1',
    fetchImpl: async () => ({ ok: true, status: 200, json: async () => payload() })
  });
  assert.equal(count.textContent, '+5 Craft', 'nothing is rewritten before a timer runs');
  assert.ok(parked.length > 0);
});

test('a card that cannot be opened shows nothing and reports it', async () => {
  const { card } = load();
  const host = fakeHost();
  let failed = false;
  const result = await card.open({
    host,
    parcelId: 'gone',
    onError: () => (failed = true),
    fetchImpl: async () => ({ ok: false, status: 404, json: async () => ({}) })
  });
  assert.equal(result, null);
  assert.equal(failed, true);
  assert.equal(host.innerHTML, '');
});
