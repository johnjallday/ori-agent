import test from 'node:test';
import assert from 'node:assert/strict';
import {
  pillLabel,
  pillDescription,
  renderSection,
  resetTime,
  startRefreshCadence,
  refreshInterval
} from './provider-limits.js';

const now = new Date('2026-10-13T01:00:00Z');
function provider(overrides = {}) {
  return {
    provider: 'codex',
    display_name: 'Codex',
    state: 'ok',
    level: 'ok',
    plan: 'prolite',
    source: 'codex_session_log',
    fetched_at: '2026-10-13T00:55:00Z',
    windows: [
      {
        id: 'weekly',
        label: 'Weekly',
        window_minutes: 10080,
        used_percent: 16,
        resets_at: now.getTime() / 1000 + 187200,
        level: 'ok'
      }
    ],
    ...overrides
  };
}

test('pill uses duration label and describes reset, age and source', () => {
  assert.equal(pillLabel(provider()), 'Codex · Wk 16% used');
  const description = pillDescription(provider(), now);
  assert.match(description, /Weekly 16% used, resets in 2d 4h/);
  assert.match(description, /Updated 5m ago · session log/);
});

test('warning is bold, exhausted has an explicit limit signal', () => {
  const data = provider();
  data.level = data.windows[0].level = 'warn';
  data.windows[0].used_percent = 80;
  assert.match(pillLabel(data), /<strong>80% used<\/strong>/);
  data.level = 'exhausted';
  assert.match(pillLabel(data), /limit reached/);
  assert.match(renderSection(data, { now }), /Limit reached/);
});

test('a reached provider does not mislabel every window as exhausted', () => {
  const data = provider({ level: 'exhausted' });
  data.windows.unshift({ id: 'five_hour', label: '5-hour', used_percent: 100, level: 'exhausted' });
  assert.equal(pillLabel(data), 'Codex · 5h limit reached · Wk 16% used');
  data.windows.shift();
  assert.equal(pillLabel(data), 'Codex · Wk 16% used · limit reached');
  data.level = 'warn';
  assert.match(pillLabel(data), /<strong>16% used<\/strong>/);
  assert.match(pillDescription(data), /nearing limit/);
});

test('inspector exposes accessible meters and source metadata', () => {
  const html = renderSection(provider(), { now });
  assert.match(html, /Usage limits/);
  assert.match(html, /<strong>16% used<\/strong>/);
  assert.match(html, /role="meter" aria-label="Weekly usage"/);
  assert.match(html, /aria-valuenow="16" aria-valuemin="0" aria-valuemax="100"/);
  assert.match(html, /Updated 5m ago · session log/);
});

test('model-specific windows are detail-only', () => {
  const data = provider();
  data.windows.push({ ...data.windows[0], label: 'Weekly Sonnet', model_specific: true });
  assert.doesNotMatch(pillLabel(data), /Sonnet/);
  assert.doesNotMatch(renderSection(data), /Sonnet/);
  assert.match(renderSection(data, { modelSpecific: true }), /Sonnet/);
});

test('missing provider is empty and non-ok states show only their detail', () => {
  assert.equal(renderSection(null), '');
  const html = renderSection(provider({ state: 'not_signed_in', detail: 'Run `codex login`' }));
  assert.match(html, /Run `codex login`/);
  assert.doesNotMatch(html, /role="meter"/);
});

test('untrusted labels and details are escaped; over-limit values keep valid meter range', () => {
  const data = provider({ plan: '<script>bad</script>', detail: '<img src=x>' });
  data.windows[0].label = '" onfocus="bad';
  data.windows[0].used_percent = 110;
  data.windows[0].level = 'exhausted';
  const html = renderSection(data);
  assert.doesNotMatch(html, /<script>|<img|aria-label="" onfocus/);
  assert.match(html, /aria-valuenow="100"/);
  assert.match(html, /110% used · limit reached/);
});

test('reset times handle hours, unknown and elapsed readings', () => {
  assert.match(resetTime(now.getTime() / 1000 + 7800, now), /resets in 2h 10m/);
  assert.equal(resetTime(0, now), 'Reset time unavailable');
  assert.match(resetTime(now.getTime() / 1000 - 60, now), /reset time passed/);
});

function cadenceFixture(visibility = 'visible') {
  const doc = new EventTarget();
  const win = new EventTarget();
  doc.visibilityState = visibility;
  let time = 0;
  let id = 0;
  const timers = new Map();
  const calls = [];
  const stop = startRefreshCadence(refresh => calls.push({ refresh, time }), {
    doc,
    win,
    now: () => time,
    setTimer: (callback, delay) => {
      timers.set(++id, { callback, at: time + delay });
      return id;
    },
    clearTimer: timer => timers.delete(timer)
  });
  return {
    calls,
    timers,
    stop,
    visibility(value) {
      doc.visibilityState = value;
      doc.dispatchEvent(new Event('visibilitychange'));
    },
    page(event) {
      win.dispatchEvent(new Event(event));
    },
    advance(ms) {
      const target = time + ms;
      for (;;) {
        const next = [...timers.entries()].sort((a, b) => a[1].at - b[1].at)[0];
        if (!next || next[1].at > target) break;
        time = next[1].at;
        timers.delete(next[0]);
        next[1].callback();
      }
      time = target;
    }
  };
}

test('cadence reads live on open and every three minutes, only while visible', () => {
  const clock = cadenceFixture();
  assert.deepEqual(clock.calls, [{ refresh: true, time: 0 }]);
  clock.advance(refreshInterval - 1);
  assert.equal(clock.calls.length, 1);
  clock.advance(1);
  assert.equal(clock.calls.length, 2);
  clock.visibility('hidden');
  assert.equal(clock.timers.size, 0);
  clock.advance(20 * refreshInterval);
  assert.equal(clock.calls.length, 2);
  clock.visibility('visible');
  assert.equal(clock.calls.length, 3);
  assert.equal(clock.calls[2].refresh, true);
  assert.equal(clock.timers.size, 1);
  clock.stop();
});

test('short hidden interval does not fetch immediately; initially hidden pages wait', () => {
  const clock = cadenceFixture('hidden');
  assert.equal(clock.calls.length, 0);
  assert.equal(clock.timers.size, 0);
  clock.visibility('visible');
  assert.equal(clock.calls.length, 1);
  clock.visibility('hidden');
  clock.advance(refreshInterval);
  clock.visibility('visible');
  assert.equal(clock.calls.length, 1);
  clock.advance(refreshInterval);
  assert.equal(clock.calls.length, 2);
  clock.stop();
});

test('back-forward cache pauses polling, resumes once, and remount disposal removes listeners', () => {
  const clock = cadenceFixture();
  clock.page('pageshow'); // Initial page event must not duplicate the live read.
  assert.equal(clock.calls.length, 1);
  clock.page('pagehide');
  assert.equal(clock.timers.size, 0);
  clock.advance(refreshInterval + 1);
  clock.page('pageshow');
  assert.equal(clock.calls.length, 2);
  assert.equal(clock.timers.size, 1);
  clock.stop();
  clock.visibility('hidden');
  clock.advance(refreshInterval * 2);
  clock.visibility('visible');
  clock.page('pageshow');
  assert.equal(clock.calls.length, 2);
  assert.equal(clock.timers.size, 0);
});
