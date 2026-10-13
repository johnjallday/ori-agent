import test from 'node:test';
import assert from 'node:assert/strict';
import { pillLabel, pillDescription, renderSection, resetTime } from './provider-limits.js';

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
  assert.equal(pillLabel(provider()), 'Codex · Wk 16%');
  const description = pillDescription(provider(), now);
  assert.match(description, /Weekly 16% used, resets in 2d 4h/);
  assert.match(description, /Updated 5m ago · session log/);
});

test('warning is bold, exhausted has an explicit limit signal', () => {
  const data = provider();
  data.level = data.windows[0].level = 'warn';
  data.windows[0].used_percent = 80;
  assert.match(pillLabel(data), /<strong>80%<\/strong>/);
  data.level = 'exhausted';
  assert.match(pillLabel(data), /limit reached/);
  assert.match(renderSection(data, { now }), /Limit reached/);
});

test('inspector exposes accessible meters and source metadata', () => {
  const html = renderSection(provider(), { now });
  assert.match(html, /Usage limits/);
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
