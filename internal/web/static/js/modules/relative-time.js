// relative-time.js — relative-timestamp formatter.
//
// Returns short, human-readable strings for past and future timestamps.
// Used by the home dashboard's cards/rows/events plus the upcoming-task
// "in 6h" / "tomorrow" hints.
//
// Format rules (per PRD §6, extended with future direction):
//   past, ≤60s     → "just now"
//   past, ≤60m     → "{n}m ago"
//   past, ≤24h     → "{n}h ago"
//   past, ≤7d      → "{n}d ago"
//   future, ≤60s   → "soon"
//   future, ≤60m   → "in {n}m"
//   future, ≤24h   → "in {n}h"
//   future, ≤7d    → "in {n}d"
//   otherwise      → absolute "MMM D" (no year)
//   over a year    → "MMM D, YYYY"
//
// Invalid / empty inputs return "".
//
// Exported both as a UMD-style window global (window.RelativeTime) for
// non-module scripts and as a default export for `node --test` consumption.

const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];

function formatAbsolute(d, now) {
  const sameYear = d.getFullYear() === now.getFullYear();
  const base = `${MONTHS[d.getMonth()]} ${d.getDate()}`;
  return sameYear ? base : `${base}, ${d.getFullYear()}`;
}

export function formatRelativeTime(input, opts = {}) {
  if (input == null || input === '') return '';
  const d = input instanceof Date ? input : new Date(input);
  if (Number.isNaN(d.getTime())) return '';

  const now = opts.now instanceof Date ? opts.now : new Date();
  const diffMs = d.getTime() - now.getTime();
  const absMs = Math.abs(diffMs);

  const SEC = 1000;
  const MIN = 60 * SEC;
  const HOUR = 60 * MIN;
  const DAY = 24 * HOUR;
  const WEEK = 7 * DAY;

  // Within the past or future minute → fixed copy.
  if (absMs < MIN) return diffMs < 0 ? 'just now' : 'soon';

  if (absMs < HOUR) {
    const n = Math.round(absMs / MIN);
    return diffMs < 0 ? `${n}m ago` : `in ${n}m`;
  }
  if (absMs < DAY) {
    const n = Math.round(absMs / HOUR);
    return diffMs < 0 ? `${n}h ago` : `in ${n}h`;
  }
  if (absMs < WEEK) {
    const n = Math.round(absMs / DAY);
    return diffMs < 0 ? `${n}d ago` : `in ${n}d`;
  }
  return formatAbsolute(d, now);
}

if (typeof window !== 'undefined') {
  window.RelativeTime = { formatRelativeTime };
}

export default { formatRelativeTime };
