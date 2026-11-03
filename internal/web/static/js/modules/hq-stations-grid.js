/*
 * hq-stations-grid.js — the Personal HQ's stations grid as data.
 *
 * Pure: workspace-command.js passes each registered station (key, label and
 * what its own state() returned) in registry order; this turns them into the
 * grid's cards and orders them. No station gets new state logic here: the
 * state word is read from the station's existing tone and words.
 *
 * Order (PRD 766 FR17): what needs the user first (an Error counts), then
 * Ready / Running, then Not set up; stable by registry order within a group.
 */

import { HQ_LABELS } from './hq-labels.js';

// The Notes station is its own panel in the main column, never a card.
export const NOTES_STATION_KEY = 'second-brain';

export const STATE_WORDS = Object.freeze({
  needsYou: HQ_LABELS.needsYou,
  error: HQ_LABELS.error,
  running: HQ_LABELS.running,
  ready: HQ_LABELS.ready,
  checking: 'Checking',
  notSetUp: HQ_LABELS.notSetUp
});

// "Set up", "Set up Email Ops", "Not set up", "… is not set up yet". A station
// that still needs connecting says so in its own words, whatever its tone
// (Calendar Ops asks to be set up with an attention tone).
const NOT_SET_UP = /^(not )?set up\b|\bnot set up\b/i;
// A station whose own words say work is under way, while its tone is "loading".
const RUNNING = /^(preparing|running|reading|scanning|looking|generating)\b/i;

/** The state word for one station's state() result. */
export function stationStateWord(state) {
  const value = String((state && state.value) || '').trim();
  const description = String((state && state.description) || '').trim();
  const tone = String((state && state.tone) || '').trim();
  if (NOT_SET_UP.test(value) || NOT_SET_UP.test(description)) return STATE_WORDS.notSetUp;
  switch (tone) {
    case 'attention':
      return STATE_WORDS.needsYou;
    case 'degraded':
      return STATE_WORDS.error;
    case 'loading':
      return RUNNING.test(value) && !/^loading\b/i.test(description)
        ? STATE_WORDS.running
        : STATE_WORDS.checking;
    default:
      return STATE_WORDS.ready;
  }
}

function sentenceCase(text) {
  const clean = String(text || '').trim();
  return clean ? clean.charAt(0).toUpperCase() + clean.slice(1) : '';
}

const ACTIONS = {
  [STATE_WORDS.notSetUp]: 'Set up',
  [STATE_WORDS.needsYou]: 'Review',
  [STATE_WORDS.error]: 'Open'
};

const GROUP = {
  [STATE_WORDS.needsYou]: 0,
  [STATE_WORDS.error]: 0,
  [STATE_WORDS.running]: 1,
  [STATE_WORDS.ready]: 1,
  [STATE_WORDS.checking]: 1,
  [STATE_WORDS.notSetUp]: 2
};

/** One card. `summary` is the station's two lines: its value, then its description. */
export function stationCard(station) {
  const state = (station && station.state) || {};
  const stateWord = stationStateWord(state);
  const value = String(state.value || '').trim();
  const description = sentenceCase(state.description);
  const summary = [value, description].filter(
    (line, index, lines) => line && lines.indexOf(line) === index
  );
  return {
    key: String((station && station.key) || ''),
    label: String((station && station.label) || ''),
    stateWord,
    summary: summary.slice(0, 2),
    action: ACTIONS[stateWord] || 'Open',
    configured: stateWord !== STATE_WORDS.notSetUp,
    tone:
      stateWord === STATE_WORDS.error
        ? 'error'
        : stateWord === STATE_WORDS.needsYou
          ? 'attention'
          : ''
  };
}

/**
 * The grid's cards in display order.
 *
 * @param {{key:string,label:string,state:{value?:string,description?:string,tone?:string}}[]} stations
 *   in registry order
 */
export function hqStationCards(stations) {
  return (Array.isArray(stations) ? stations : [])
    .filter(station => station && station.key && station.key !== NOTES_STATION_KEY)
    .map((station, index) => ({ card: stationCard(station), index }))
    .sort((a, b) => GROUP[a.card.stateWord] - GROUP[b.card.stateWord] || a.index - b.index)
    .map(entry => entry.card);
}

/**
 * The cards asking for the user: the Needs you panel's station rows (FR14).
 * An Error is not one: a status that could not be read is shown on its card,
 * not counted as something the user must do.
 */
export function stationsNeedingYou(cards) {
  return (Array.isArray(cards) ? cards : []).filter(
    card => card.stateWord === STATE_WORDS.needsYou
  );
}
