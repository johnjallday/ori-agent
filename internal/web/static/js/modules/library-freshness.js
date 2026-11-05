// library-freshness.js — how old a Home's library picture is.
//
// PRD: tasks/prd-library-upkeep.md FR1, FR3, FR6. The shelf's freshness line
// and the map district's library badge both read the summary's
// `last_complete_scan_at` (the newest complete scan of a connected root), so a
// partial or failed scan never makes the picture look newer than it is. Small
// on purpose: the map loads it without the shelf module.

const MINUTE_MS = 60_000;
const HOUR_MS = 60 * MINUTE_MS;
const DAY_MS = 24 * HOUR_MS;

/** Past this age the shelf line becomes a nudge and the map badge shows the age. */
export const STALE_SCAN_MS = DAY_MS;
const DATE_AFTER_MS = 30 * DAY_MS;

function plural(count, unit) {
  return `${count} ${unit}${count === 1 ? '' : 's'} ago`;
}

/** "just now", "N minutes/hours/days ago", or "on <date>" past 30 days. */
export function scanAgeText(ageMs, scannedAt) {
  const age = Number.isFinite(ageMs) && ageMs > 0 ? ageMs : 0;
  if (age < MINUTE_MS) return 'just now';
  if (age < HOUR_MS) return plural(Math.floor(age / MINUTE_MS), 'minute');
  if (age < DAY_MS) return plural(Math.floor(age / HOUR_MS), 'hour');
  if (age <= DATE_AFTER_MS) return plural(Math.floor(age / DAY_MS), 'day');
  const date = new Date(scannedAt);
  return Number.isNaN(date.getTime())
    ? plural(Math.floor(age / DAY_MS), 'day')
    : `on ${date.toLocaleDateString(undefined, { year: 'numeric', month: 'short', day: 'numeric' })}`;
}

/**
 * The scan age of one library summary: { text, stale, age_ms }. A summary with
 * no complete scan reads "Not scanned yet" and is never stale (the setup card
 * already asks for the first scan). A clock behind the server reads "just now".
 */
export function libraryFreshness(summary, now = Date.now()) {
  const raw = summary && typeof summary === 'object' ? summary.last_complete_scan_at : null;
  const scannedAt = typeof raw === 'string' ? Date.parse(raw) : NaN;
  if (!Number.isFinite(scannedAt)) return { text: 'Not scanned yet', stale: false, age_ms: null };
  const age = Math.max(0, now - scannedAt);
  return {
    text: `Last scanned ${scanAgeText(age, scannedAt)}`,
    stale: age > STALE_SCAN_MS,
    age_ms: age
  };
}
