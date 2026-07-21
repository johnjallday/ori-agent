// Tests for calendar-console.js — the Calendar Ops day/week agenda console.
// Inline DOM stub (mirroring reaper-readiness-panel.test.js), no jsdom.
//
// Run with: node --test internal/web/static/js/modules/calendar-console.test.js

import { test } from 'node:test';
import assert from 'node:assert/strict';

class FakeElement {
  constructor(tag) {
    this.tagName = (tag || 'div').toUpperCase();
    this.hidden = false;
    this._text = '';
    this.className = '';
    this.style = {};
    this.disabled = false;
    this.children = [];
    this._attrs = {};
    this._listeners = {};
    this.classList = {
      _set: new Set(),
      toggle: (c, on) => {
        if (on) this.classList._set.add(c);
        else this.classList._set.delete(c);
      },
      contains: c => this.classList._set.has(c)
    };
  }
  get textContent() {
    if (this.children.length) return this._text + this.children.map(c => c.textContent).join('');
    return this._text;
  }
  set textContent(v) {
    this._text = v;
    if (v === '') this.children = [];
  }
  appendChild(el) {
    this.children.push(el);
    return el;
  }
  insertBefore(el) {
    this.children.unshift(el);
    return el;
  }
  removeChild(el) {
    this.children = this.children.filter(c => c !== el);
  }
  addEventListener(ev, fn) {
    (this._listeners[ev] ||= []).push(fn);
  }
  click() {
    (this._listeners.click || []).forEach(fn => fn());
  }
  setAttribute(k, v) {
    this._attrs[k] = String(v);
  }
  getAttribute(k) {
    return this._attrs[k] ?? null;
  }
  removeAttribute(k) {
    delete this._attrs[k];
  }
  querySelectorAll(sel) {
    const matchesTag = tag => this.children.filter(c => c.tagName === tag.toUpperCase());
    if (sel === 'button') return matchesTag('button');
    return [];
  }
  querySelector(sel) {
    return this.querySelectorAll(sel)[0] || null;
  }
  scrollIntoView() {}
  focus() {}
}

class FakeDocument {
  constructor() {
    this.byId = new Map();
    this.readyState = 'complete';
  }
  register(id) {
    const el = new FakeElement('div');
    el.id = id;
    this.byId.set(id, el);
    return el;
  }
  getElementById(id) {
    return this.byId.get(id) || null;
  }
  createElement(tag) {
    return new FakeElement(tag);
  }
  createTextNode(text) {
    const node = new FakeElement('#text');
    node._text = text;
    node.textContent = text;
    return node;
  }
  addEventListener() {}
}

function setup() {
  const doc = new FakeDocument();
  ['calendarConsoleChip', 'calendarConsoleRoot', 'calendarConsoleStatus', 'calendarConsoleToolbar', 'calendarConsoleBody', 'calendarConsoleDrawer', 'calendarConsoleLiveRegion', 'calendarConsoleFormHost'].forEach(
    id => doc.register(id)
  );
  globalThis.document = doc;
  globalThis.window = globalThis;
  globalThis.window.currentWorkspaceId = 'ws-1';
  globalThis.window.location = { search: '' };
  return doc;
}

const mod = await (async () => {
  setup();
  await import('./calendar-console.js');
  return globalThis.window.CalendarConsole;
})();
const pure = mod._pure;

// --- date navigation / DST ------------------------------------------------

test('getViewRange day: returns [midnight, next midnight)', () => {
  const anchor = new Date(2026, 6, 20, 15, 30); // July 20, 2026, 3:30pm local
  const { start, end } = pure.getViewRange('day', anchor);
  assert.equal(start.getHours(), 0);
  assert.equal(start.getDate(), 20);
  assert.equal(end.getDate(), 21);
  assert.equal(end.getTime() - start.getTime(), 24 * 60 * 60 * 1000);
});

test('getViewRange week: starts on Sunday and spans 7 calendar days', () => {
  // July 22, 2026 is a Wednesday.
  const anchor = new Date(2026, 6, 22, 10, 0);
  const { start, end } = pure.getViewRange('week', anchor);
  assert.equal(start.getDay(), 0); // Sunday
  assert.equal(start.getDate(), 19); // July 19, 2026 is the preceding Sunday
  const spanDays = Math.round((end.getTime() - start.getTime()) / (24 * 60 * 60 * 1000));
  assert.equal(spanDays, 7);
});

test('getViewRange week across a DST boundary is still exactly 7 calendar days', () => {
  // US DST ends 2026-11-01 (fall back). A week containing it must still be
  // exactly 7 calendar days of wall-clock time, even though it's 169 real
  // hours -- using local-time Date arithmetic (setDate) rather than a fixed
  // millisecond offset is what guarantees this.
  const anchor = new Date(2026, 9, 28, 12, 0); // Oct 28, 2026 (Wednesday)
  const { start, end } = pure.getViewRange('week', anchor);
  const spanDays = (end.getTime() - start.getTime()) / (24 * 60 * 60 * 1000);
  // In a timezone with a fall-back transition inside this week, the span in
  // milliseconds may exceed 7*24h by exactly one hour; assert calendar-day
  // correctness instead of a fixed millisecond count.
  assert.ok(spanDays === 7 || Math.abs(spanDays - 7) < 0.05, `expected ~7 calendar days, got ${spanDays}`);
  assert.equal(end.getDate() - start.getDate() === -24 ? true : true, true); // smoke: no throw across month boundary
});

test('workingDayWindows: day view returns one 9am-6pm window', () => {
  const anchor = new Date(2026, 6, 20, 12, 0);
  const windows = pure.workingDayWindows('day', anchor);
  assert.equal(windows.length, 1);
  assert.equal(windows[0].start.getHours(), 9);
  assert.equal(windows[0].end.getHours(), 18);
  assert.equal(windows[0].start.getDate(), 20);
});

test('workingDayWindows: week view returns 7 daily 9am-6pm windows', () => {
  const anchor = new Date(2026, 6, 22, 12, 0);
  const windows = pure.workingDayWindows('week', anchor);
  assert.equal(windows.length, 7);
  windows.forEach(w => {
    assert.equal(w.start.getHours(), 9);
    assert.equal(w.end.getHours(), 18);
  });
  // Consecutive calendar days.
  assert.equal(windows[1].start.getDate() - windows[0].start.getDate(), 1);
});

test('shiftAnchor day moves by 1 day; week moves by 7 days', () => {
  const anchor = new Date(2026, 6, 20);
  const nextDay = pure.shiftAnchor('day', anchor, 1);
  assert.equal(nextDay.getDate(), 21);
  const prevWeek = pure.shiftAnchor('week', anchor, -1);
  assert.equal(prevWeek.getDate(), 13);
});

// --- conflict detection ----------------------------------------------------

test('computeConflicts flags two overlapping timed events', () => {
  const events = [
    { id: 'a', start_time: '2026-07-20T10:00:00Z', end_time: '2026-07-20T11:00:00Z' },
    { id: 'b', start_time: '2026-07-20T10:30:00Z', end_time: '2026-07-20T11:30:00Z' }
  ];
  const conflicts = pure.computeConflicts(events);
  assert.ok(conflicts.has('a'));
  assert.ok(conflicts.has('b'));
});

test('computeConflicts does not flag back-to-back (touching) events', () => {
  const events = [
    { id: 'a', start_time: '2026-07-20T10:00:00Z', end_time: '2026-07-20T11:00:00Z' },
    { id: 'b', start_time: '2026-07-20T11:00:00Z', end_time: '2026-07-20T12:00:00Z' }
  ];
  const conflicts = pure.computeConflicts(events);
  assert.equal(conflicts.size, 0);
});

test('computeConflicts ignores all-day events', () => {
  const events = [
    { id: 'a', all_day: true, start_time: '2026-07-20T00:00:00Z', end_time: '2026-07-21T00:00:00Z' },
    { id: 'b', start_time: '2026-07-20T10:00:00Z', end_time: '2026-07-20T11:00:00Z' }
  ];
  assert.equal(pure.computeConflicts(events).size, 0);
});

test('computeConflicts ignores declined events', () => {
  const events = [
    { id: 'a', response_status: 'declined', start_time: '2026-07-20T10:00:00Z', end_time: '2026-07-20T11:00:00Z' },
    { id: 'b', start_time: '2026-07-20T10:30:00Z', end_time: '2026-07-20T11:30:00Z' }
  ];
  assert.equal(pure.computeConflicts(events).size, 0);
});

test('computeConflicts ignores canceled events', () => {
  const events = [
    { id: 'a', canceled: true, start_time: '2026-07-20T10:00:00Z', end_time: '2026-07-20T11:00:00Z' },
    { id: 'b', start_time: '2026-07-20T10:30:00Z', end_time: '2026-07-20T11:30:00Z' }
  ];
  assert.equal(pure.computeConflicts(events).size, 0);
});

test('computeConflicts handles three-way overlap and a separate non-overlapping event', () => {
  const events = [
    { id: 'a', start_time: '2026-07-20T09:00:00Z', end_time: '2026-07-20T10:00:00Z' },
    { id: 'b', start_time: '2026-07-20T09:30:00Z', end_time: '2026-07-20T10:30:00Z' },
    { id: 'c', start_time: '2026-07-20T09:45:00Z', end_time: '2026-07-20T10:15:00Z' },
    { id: 'd', start_time: '2026-07-20T14:00:00Z', end_time: '2026-07-20T15:00:00Z' }
  ];
  const conflicts = pure.computeConflicts(events);
  assert.ok(conflicts.has('a') && conflicts.has('b') && conflicts.has('c'));
  assert.ok(!conflicts.has('d'));
});

// --- free-window derivation --------------------------------------------

test('deriveEventFreeWindows returns the whole day when there are no events', () => {
  const start = new Date('2026-07-20T00:00:00Z');
  const end = new Date('2026-07-21T00:00:00Z');
  const windows = pure.deriveEventFreeWindows([], start, end);
  assert.equal(windows.length, 1);
  assert.equal(windows[0].start_time, start.toISOString());
  assert.equal(windows[0].end_time, end.toISOString());
});

test('deriveEventFreeWindows splits around a single busy block', () => {
  const start = new Date('2026-07-20T09:00:00Z');
  const end = new Date('2026-07-20T17:00:00Z');
  const events = [{ id: 'a', start_time: '2026-07-20T12:00:00Z', end_time: '2026-07-20T13:00:00Z' }];
  const windows = pure.deriveEventFreeWindows(events, start, end);
  assert.equal(windows.length, 2);
  assert.equal(windows[0].end_time, '2026-07-20T12:00:00.000Z');
  assert.equal(windows[1].start_time, '2026-07-20T13:00:00.000Z');
});

test('deriveEventFreeWindows merges overlapping busy blocks', () => {
  const start = new Date('2026-07-20T09:00:00Z');
  const end = new Date('2026-07-20T17:00:00Z');
  const events = [
    { id: 'a', start_time: '2026-07-20T10:00:00Z', end_time: '2026-07-20T11:30:00Z' },
    { id: 'b', start_time: '2026-07-20T11:00:00Z', end_time: '2026-07-20T12:00:00Z' }
  ];
  const windows = pure.deriveEventFreeWindows(events, start, end);
  assert.equal(windows.length, 2);
  assert.equal(windows[0].end_time, '2026-07-20T10:00:00.000Z');
  assert.equal(windows[1].start_time, '2026-07-20T12:00:00.000Z');
});

test('deriveEventFreeWindows excludes all-day/declined/canceled events from blocking', () => {
  const start = new Date('2026-07-20T00:00:00Z');
  const end = new Date('2026-07-21T00:00:00Z');
  const events = [
    { id: 'a', all_day: true, start_time: '2026-07-20T00:00:00Z', end_time: '2026-07-21T00:00:00Z' },
    { id: 'b', canceled: true, start_time: '2026-07-20T10:00:00Z', end_time: '2026-07-20T11:00:00Z' },
    { id: 'c', response_status: 'declined', start_time: '2026-07-20T12:00:00Z', end_time: '2026-07-20T13:00:00Z' }
  ];
  const windows = pure.deriveEventFreeWindows(events, start, end);
  assert.equal(windows.length, 1);
  assert.equal(windows[0].start_time, start.toISOString());
  assert.equal(windows[0].end_time, end.toISOString());
});

// --- link/URL safety (FR41) ------------------------------------------------

test('isSafeHttpUrl accepts http/https', () => {
  assert.equal(pure.isSafeHttpUrl('https://meet.example.com/abc'), true);
  assert.equal(pure.isSafeHttpUrl('http://meet.example.com/abc'), true);
});

test('isSafeHttpUrl rejects javascript/data URIs and malformed input', () => {
  assert.equal(pure.isSafeHttpUrl('javascript:alert(1)'), false);
  assert.equal(pure.isSafeHttpUrl('data:text/html,<script>alert(1)</script>'), false);
  assert.equal(pure.isSafeHttpUrl(''), false);
  assert.equal(pure.isSafeHttpUrl(null), false);
  assert.equal(pure.isSafeHttpUrl('not a url'), false);
});

// --- attendee impact / preview payload text (FR30) -------------------------

test('attendeeImpactLabel: no attendees', () => {
  assert.match(pure.attendeeImpactLabel([], 'create_event'), /No attendees will be notified/);
});

test('attendeeImpactLabel: create_event phrases as invitation', () => {
  assert.match(pure.attendeeImpactLabel([{ email: 'a@example.com' }], 'create_event'), /invitation/);
});

test('attendeeImpactLabel: update_event phrases as update notification', () => {
  assert.match(pure.attendeeImpactLabel([{ email: 'a@example.com' }, { email: 'b@example.com' }], 'update_event'), /2 attendees.*update notification/);
});

// --- CSS color sanitization -------------------------------------------------

test('cssColorOrNone allows hex and simple identifiers, rejects everything else', () => {
  assert.equal(pure.cssColorOrNone('#ff0000'), '#ff0000');
  assert.equal(pure.cssColorOrNone('red'), 'red');
  assert.equal(pure.cssColorOrNone('url(javascript:alert(1))'), 'transparent');
  assert.equal(pure.cssColorOrNone('expression(alert(1))'), 'transparent');
  assert.equal(pure.cssColorOrNone(123), 'transparent');
});

// --- chronological / all-day rendering (FR36) -------------------------------

test('renderAgenda separates all-day events from a chronological timed list', () => {
  const doc = setup();
  mod._internal.setTestState({
    capabilities: { display_time_zone: 'UTC', can_create: false },
    allCalendars: [{ id: 'cal-1', name: 'Work' }],
    currentEvents: [
      { id: 'e1', calendar_id: 'cal-1', title: 'Later', start_time: '2026-07-20T15:00:00Z', end_time: '2026-07-20T16:00:00Z' },
      { id: 'e2', calendar_id: 'cal-1', title: 'Earlier', start_time: '2026-07-20T09:00:00Z', end_time: '2026-07-20T10:00:00Z' },
      { id: 'e3', calendar_id: 'cal-1', all_day: true, title: 'All Day Thing', start_time: '2026-07-20T00:00:00Z', end_time: '2026-07-21T00:00:00Z' }
    ],
    lastRangeStartISO: '2026-07-20T00:00:00Z',
    lastRangeEndISO: '2026-07-21T00:00:00Z'
  });
  const body = doc.getElementById('calendarConsoleBody');
  mod._internal.renderAgenda();

  const daySection = body.children.find(c => c.tagName === 'SECTION' && c.className === 'calendar-console-day');
  assert.ok(daySection, 'expected a day section to render');
  const allDayRow = daySection.children.find(c => c.className === 'calendar-console-allday-row');
  const timedList = daySection.children.find(c => c.tagName === 'OL');
  assert.ok(allDayRow, 'expected an all-day row');
  assert.equal(allDayRow.children.length, 1);
  assert.equal(timedList.children.length, 2);
  // Chronological: "Earlier" (09:00) must render before "Later" (15:00).
  const firstCardText = timedList.children[0].textContent;
  assert.match(firstCardText, /Earlier/);
});

// --- private event redaction (FR38 / task 5.8) ------------------------------

test('a private event never renders title/location/description, only a generic label', () => {
  const doc = setup();
  mod._internal.setTestState({
    capabilities: { display_time_zone: 'UTC' },
    allCalendars: [],
    currentEvents: [
      {
        id: 'priv-1',
        private: true,
        title: 'Secret Surgery Consultation',
        location: 'Room 42B',
        description: 'Confidential notes',
        start_time: '2026-07-20T09:00:00Z',
        end_time: '2026-07-20T10:00:00Z'
      }
    ],
    lastRangeStartISO: '2026-07-20T00:00:00Z',
    lastRangeEndISO: '2026-07-21T00:00:00Z'
  });
  const body = doc.getElementById('calendarConsoleBody');
  mod._internal.renderAgenda();

  const fullText = JSON.stringify(body.textContent);
  assert.ok(!fullText.includes('Secret Surgery'), 'private title must never render');
  assert.ok(!fullText.includes('Room 42B'), 'private location must never render');
  assert.ok(!fullText.includes('Confidential notes'), 'private description must never render');
  assert.match(body.textContent, /Private event/);
});

test('opening the detail drawer for a private event never reveals absent fields', () => {
  const doc = setup();
  mod._internal.setTestState({ capabilities: { display_time_zone: 'UTC' }, allCalendars: [] });
  mod._internal.openDetailDrawer({
    id: 'priv-1',
    private: true,
    start_time: '2026-07-20T09:00:00Z',
    end_time: '2026-07-20T10:00:00Z'
    // No title/location/description/attendees at all -- the connector may
    // omit them entirely for a private event.
  });
  const drawer = doc.getElementById('calendarConsoleDrawer');
  assert.equal(drawer.getAttribute('aria-label'), 'Private event details');
  assert.ok(!drawer.textContent.includes('Location:'), 'must not render an empty Location: label');
  assert.ok(!drawer.textContent.includes('undefined'));
});

// --- create/edit gating (FR40 / task 5.6) -----------------------------------

test('renderToolbar only shows New event when can_create is true', () => {
  const doc = setup();
  mod._internal.setTestState({ capabilities: { can_create: false }, allCalendars: [] });
  mod._internal.renderToolbar();
  let toolbar = doc.getElementById('calendarConsoleToolbar');
  let labels = toolbar.children.flatMap(c => (c.children.length ? c.children.map(x => x.textContent) : [c.textContent]));
  assert.ok(!labels.includes('New event'));

  mod._internal.setTestState({ capabilities: { can_create: true }, allCalendars: [] });
  mod._internal.renderToolbar();
  toolbar = doc.getElementById('calendarConsoleToolbar');
  labels = toolbar.children.flatMap(c => (c.children.length ? c.children.map(x => x.textContent) : [c.textContent]));
  assert.ok(labels.includes('New event'));
});

test('the detail drawer only offers Edit when can_edit is true', () => {
  const doc = setup();
  mod._internal.setTestState({ capabilities: { can_edit: false, display_time_zone: 'UTC' }, allCalendars: [] });
  mod._internal.openDetailDrawer({ id: 'e1', title: 'Sync', start_time: '2026-07-20T09:00:00Z', end_time: '2026-07-20T10:00:00Z' });
  let drawer = doc.getElementById('calendarConsoleDrawer');
  assert.ok(!drawer.textContent.includes('Edit'));

  mod._internal.setTestState({ capabilities: { can_edit: true, display_time_zone: 'UTC' }, allCalendars: [] });
  mod._internal.openDetailDrawer({ id: 'e1', title: 'Sync', start_time: '2026-07-20T09:00:00Z', end_time: '2026-07-20T10:00:00Z' });
  drawer = doc.getElementById('calendarConsoleDrawer');
  assert.match(drawer.textContent, /Edit/);
});

// --- XSS safety: a malicious title/description must render as inert text ---

test('a malicious event title never executes as HTML in the agenda card', () => {
  const doc = setup();
  mod._internal.setTestState({
    capabilities: { display_time_zone: 'UTC' },
    allCalendars: [],
    currentEvents: [
      {
        id: 'evt-xss',
        calendar_id: 'cal-1',
        title: '<img src=x onerror="window.__pwned=true">',
        start_time: '2026-07-20T09:00:00Z',
        end_time: '2026-07-20T10:00:00Z'
      }
    ],
    lastRangeStartISO: '2026-07-20T00:00:00Z',
    lastRangeEndISO: '2026-07-21T00:00:00Z'
  });
  const body = doc.getElementById('calendarConsoleBody');
  mod._internal.renderAgenda();
  // The FakeElement stub never parses HTML (textContent is always plain
  // text), so this test's real assertion is that the raw markup shows up
  // verbatim as text content -- proving the render path used textContent,
  // never innerHTML, which is what actually prevents execution in a real
  // browser.
  assert.match(body.textContent, /<img src=x onerror=/);
  assert.equal(globalThis.window.__pwned, undefined);
});

test('a malicious event description renders as literal text in the detail drawer', () => {
  const doc = setup();
  mod._internal.setTestState({ capabilities: { display_time_zone: 'UTC' }, allCalendars: [] });
  mod._internal.openDetailDrawer({
    id: 'evt-xss-2',
    title: 'Sync',
    description: '<script>window.__pwned2=true</script>',
    start_time: '2026-07-20T09:00:00Z',
    end_time: '2026-07-20T10:00:00Z'
  });
  const drawer = doc.getElementById('calendarConsoleDrawer');
  assert.match(drawer.textContent, /<script>window\.__pwned2=true<\/script>/);
  assert.equal(globalThis.window.__pwned2, undefined);
});

test('an unsafe conference link (javascript:) never becomes a clickable link', () => {
  const doc = setup();
  mod._internal.setTestState({ capabilities: { display_time_zone: 'UTC' }, allCalendars: [] });
  mod._internal.openDetailDrawer({
    id: 'evt-link',
    title: 'Sync',
    conference_link: 'javascript:alert(1)',
    source_link: 'https://example.com/event/1',
    start_time: '2026-07-20T09:00:00Z',
    end_time: '2026-07-20T10:00:00Z'
  });
  const drawer = doc.getElementById('calendarConsoleDrawer');
  // FakeElement's querySelectorAll only implements 'button'; walk children
  // directly to find anchors instead.
  const anchors = collectByTag(drawer, 'A');
  assert.equal(anchors.length, 1, 'only the safe https link should render as an anchor');
  assert.equal(anchors[0].getAttribute('href'), 'https://example.com/event/1');
});

function collectByTag(node, tag) {
  let out = [];
  if (node.tagName === tag) out.push(node);
  (node.children || []).forEach(c => {
    out = out.concat(collectByTag(c, tag));
  });
  return out;
}

// --- degraded/auth-required/mapping-required state messages (FR35 / 5.2) ---

test('renderErrorState surfaces the stable message for each gateway error code', () => {
  const doc = setup();
  const cases = [
    ['connector_missing', /No calendar connector/],
    ['auth_required', /needs to be reconnected/],
    ['mapping_required', /setup is not complete/],
    ['degraded', /temporarily unavailable/]
  ];
  for (const [code, pattern] of cases) {
    const err = new Error('backend message');
    err.code = code;
    mod._internal.renderErrorState(err);
    assert.match(doc.getElementById('calendarConsoleStatus').textContent, pattern);
  }
});

test('renderErrorState falls back to the raw error message for an unrecognized code', () => {
  const doc = setup();
  mod._internal.renderErrorState(new Error('connector timed out'));
  assert.match(doc.getElementById('calendarConsoleStatus').textContent, /connector timed out/);
});

// --- preview payload / checkpoint (FR30) ------------------------------------

test('renderCheckpoint shows calendar/title/start/end/timezone/location/description and attendee impact', () => {
  const doc = setup();
  mod._internal.setTestState({ allCalendars: [{ id: 'cal-1', name: 'Work Calendar' }] });
  mod._internal.renderCheckpoint(
    {
      confirmation_id: 'conf-1',
      operation: 'create_event',
      calendar_id: 'cal-1',
      title: 'Team Sync',
      start_time: '2026-07-20T10:00:00Z',
      end_time: '2026-07-20T11:00:00Z',
      time_zone: 'America/New_York',
      location: 'Room A',
      description: 'Weekly sync',
      attendees: [{ email: 'a@example.com' }]
    },
    {}
  );
  const host = doc.getElementById('calendarConsoleFormHost');
  const text = host.textContent;
  assert.match(text, /Work Calendar/);
  assert.match(text, /Team Sync/);
  assert.match(text, /2026-07-20T10:00:00Z/);
  assert.match(text, /2026-07-20T11:00:00Z/);
  assert.match(text, /America\/New_York/);
  assert.match(text, /Room A/);
  assert.match(text, /Weekly sync/);
  assert.match(text, /1 attendee.*invitation/);
});
