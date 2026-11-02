/*
 * ticket-count-line.js — the one way the product says how much work there is
 * (tasks/prd-760 FR-6, FR-7, FR-10).
 *
 * The server sends `ticket_counts` ({ backlog, ready, in_progress, review,
 * done, cancelled }), computed from each ticket's canonical state. This file
 * turns that object into the single count line every surface shows:
 *
 *   "1 in review · 2 in progress · 3 ready"
 *
 * States at zero are left out. With none of Review, In Progress and Ready the
 * line is "No open work". Backlog, Done and Cancelled are never part of it
 * (FR-8); the board shows those.
 *
 * Missing data is not "No open work". When the server has not sent counts yet
 * (or the request failed) `format` returns '' so a caller can show a
 * placeholder instead of a claim it cannot back.
 *
 * ES modules import `TicketCounts` (or the named functions) directly. The
 * classic scripts that cannot import (workspace-map.js) read the same object
 * from window.OriTicketCounts, which this module sets when it runs. Every
 * module that mounts the map imports this file first, so the global is there
 * before the map renders.
 */

// The three states that make up "open work", in the order they are said:
// what needs a person first, then what is moving, then what is waiting.
const OPEN_STATES = Object.freeze([
  Object.freeze({ key: 'review', phrase: 'in review' }),
  Object.freeze({ key: 'in_progress', phrase: 'in progress' }),
  Object.freeze({ key: 'ready', phrase: 'ready' })
]);

const ALL_STATES = Object.freeze([
  'backlog',
  'ready',
  'in_progress',
  'review',
  'done',
  'cancelled'
]);

export const NO_OPEN_WORK = 'No open work';
const SEPARATOR = ' · ';

function isCountsObject(counts) {
  return counts !== null && typeof counts === 'object' && !Array.isArray(counts);
}

// A count the server could not make sensible (NaN, negative, text) is zero.
function countOf(counts, key) {
  const n = Number(counts[key]);
  return Number.isFinite(n) && n > 0 ? Math.floor(n) : 0;
}

/** True when `counts` is an object the server produced, even an all-zero one. */
export function isKnown(counts) {
  return isCountsObject(counts);
}

/** Review + In Progress + Ready: the work a person should be aware of. */
export function openCount(counts) {
  if (!isCountsObject(counts)) return 0;
  return OPEN_STATES.reduce((total, state) => total + countOf(counts, state.key), 0);
}

/** The open states that are above zero, in saying order: [{ key, count, phrase }]. */
export function openParts(counts) {
  if (!isCountsObject(counts)) return [];
  return OPEN_STATES.map(state => ({
    key: state.key,
    count: countOf(counts, state.key),
    phrase: state.phrase
  })).filter(part => part.count > 0);
}

/**
 * The visible count line. '' when the counts are missing; "No open work" when
 * they are known and Review, In Progress and Ready are all zero.
 */
export function format(counts) {
  if (!isCountsObject(counts)) return '';
  const parts = openParts(counts);
  if (!parts.length) return NO_OPEN_WORK;
  return parts.map(part => `${part.count} ${part.phrase}`).join(SEPARATOR);
}

/**
 * The same facts as a sentence for an accessible name or tooltip, with the
 * noun spelled out and agreeing in number: "1 ticket in review, 2 tickets in
 * progress, 3 tickets ready". '' when the counts are missing.
 */
export function describe(counts) {
  if (!isCountsObject(counts)) return '';
  const parts = openParts(counts);
  if (!parts.length) return NO_OPEN_WORK;
  return parts
    .map(part => `${part.count} ${part.count === 1 ? 'ticket' : 'tickets'} ${part.phrase}`)
    .join(', ');
}

function zeroCounts() {
  const zero = {};
  ALL_STATES.forEach(key => {
    zero[key] = 0;
  });
  return zero;
}

/**
 * Adds up several workspaces' counts (a group, or everything on Home).
 *
 * Returns { counts, known, missing }. `counts` is null when no entry was a
 * counts object: summing absent data into zero would present "No open work"
 * as a fact nobody reported. `missing` is how many entries were not counts,
 * so a caller can say a total is partial instead of passing it off as whole.
 * An empty list is an authoritative zero (nothing to count).
 */
export function sum(list) {
  const entries = Array.isArray(list) ? list : [];
  if (entries.length === 0) return { counts: zeroCounts(), known: 0, missing: 0 };
  const total = zeroCounts();
  let known = 0;
  entries.forEach(entry => {
    if (!isCountsObject(entry)) return;
    known += 1;
    ALL_STATES.forEach(key => {
      total[key] += countOf(entry, key);
    });
  });
  return {
    counts: known === 0 ? null : total,
    known,
    missing: entries.length - known
  };
}

export const TicketCounts = Object.freeze({
  NO_OPEN_WORK,
  isKnown,
  openCount,
  openParts,
  format,
  describe,
  sum
});

if (typeof window !== 'undefined') {
  window.OriTicketCounts = TicketCounts;
}
