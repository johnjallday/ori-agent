/*
 * hq-part-tips.js — what each part of the Personal HQ says about itself during
 * a first visit.
 *
 * Pure: no DOM, no storage. The page passes what it draws and what the person
 * has already looked at; this says which parts carry a mark and what each tip
 * says. The words live in HQ_LABELS (hq-labels.js); the Flow model
 * (hq-map-flow.js) stays free of sentences.
 *
 * Only a pending first visit sees any of it (`state === 'pending'`, the server's
 * value). Nothing here decides whether a visit is pending, and "looked at" is
 * only ever read to decide whether a part still has its mark.
 */

import { HQ_LABELS as L } from './hq-labels.js';
import { notesKind } from './hq-first-visit.js';

/** The part key of the Notes panel (the Notes station is `second-brain`). */
export const NOTES_PART = 'notes';

/** The hint on the Map's lead card. It is a tip, not a part of the HQ. */
export const MAP_HINT_PART = 'map-hint';

/**
 * Overview anchors: the `data-cmd-hq-card` value of a station card, mapped to
 * the part key whose sentence it carries. The Notes panel is anchored by its
 * host (`data-cmd-hq-host="notes"`), not by a card, so it is not listed here.
 */
export const OVERVIEW_CARD_PARTS = Object.freeze({
  watchtower: 'watchtower',
  'daily-brief': 'daily-brief',
  memory: 'memory',
  email: 'email',
  'calendar-ops': 'calendar-ops'
});

/** The part key for an Overview station card, or '' when it has no tip. */
export function partKeyForCard(cardKey) {
  return Object.prototype.hasOwnProperty.call(OVERVIEW_CARD_PARTS, cardKey)
    ? OVERVIEW_CARD_PARTS[cardKey]
    : '';
}

/**
 * Map (Flow) anchors. A source node carries its station (`second-brain` is the
 * Notes station, so its part is `notes`); the right-lane nodes carry their own
 * key, and the Daily Brief node is `brief`. The lead card has no part: its hint
 * is drawn by the page. The sentences live here, never on the Flow model, whose
 * tests scan the whole model for tactical words.
 */
export const FLOW_STATION_PARTS = Object.freeze({
  'second-brain': NOTES_PART,
  watchtower: 'watchtower',
  email: 'email',
  'calendar-ops': 'calendar-ops',
  memory: 'memory'
});
export const FLOW_NODE_PARTS = Object.freeze({
  brief: 'daily-brief',
  'needs-you': 'needs-you',
  'follow-ups': 'follow-ups',
  tasks: 'tickets'
});

const has = (table, key) => Object.prototype.hasOwnProperty.call(table, key);

/** The part key for a Flow node (`{key, station}`), or '' when it has no tip. */
export function partKeyForFlowNode(node) {
  const key = String((node && node.key) || '');
  if (has(FLOW_NODE_PARTS, key)) return FLOW_NODE_PARTS[key];
  const station = String((node && node.station) || '');
  return has(FLOW_STATION_PARTS, station) ? FLOW_STATION_PARTS[station] : '';
}

/**
 * The sentence for one part, or '' when that part has nothing to say in its
 * current state: Email and Calendar Ops explain themselves only while they are
 * not set up, because their sentence says they are off.
 *
 * @param {string} key a part key
 * @param {{notes?: 'created'|'own'|'unset', configured?: boolean}} [context]
 */
export function tipSentence(key, { notes = 'unset', configured = true } = {}) {
  switch (key) {
    case 'watchtower':
      return L.tipWatchtower;
    case 'daily-brief':
      return L.tipDailyBrief;
    case 'memory':
      return L.tipMemory;
    case NOTES_PART:
      return notes === 'created'
        ? L.tipNotesCreated
        : notes === 'own'
          ? L.tipNotesOwn
          : L.tipNotesUnset;
    case 'email':
      return configured ? '' : L.tipEmail;
    case 'calendar-ops':
      return configured ? '' : L.tipCalendar;
    case 'needs-you':
      return L.tipNeedsYou;
    case 'follow-ups':
      return L.tipFollowUps;
    case 'tickets':
      return L.tipTickets;
    case MAP_HINT_PART:
      return L.tipMapHint;
    default:
      return '';
  }
}

/** The accessible name of a part's `?` button: "What is <part name>?". */
export function whatIsLabel(partName) {
  return L.tipWhatIs.replace('{part}', String(partName || '').trim());
}

/**
 * Which parts are explained, and which still carry a mark.
 *
 * @param {object} input
 * @param {string} input.state first_visit_state from the server
 * @param {{key: string, configured?: boolean}[]} input.parts the parts the view draws
 * @param {object|null} [input.notes] the Notes source the first visit reads
 * @param {Record<string, boolean>|null} [input.looked] part keys already looked at
 * @returns {{active: boolean, parts: {key: string, sentence: string, marked: boolean}[]}}
 *   `active` is false, with no parts, unless the first visit is pending.
 */
export function partTipsView({ state = '', parts = [], notes = null, looked = null } = {}) {
  if (state !== 'pending') return { active: false, parts: [] };
  const seen = looked && typeof looked === 'object' ? looked : {};
  const kind = notesKind(notes);
  const out = [];
  for (const part of Array.isArray(parts) ? parts : []) {
    const key = String((part && part.key) || '');
    const sentence = tipSentence(key, { notes: kind, configured: part.configured !== false });
    if (sentence) out.push({ key, sentence, marked: seen[key] !== true });
  }
  return { active: true, parts: out };
}
