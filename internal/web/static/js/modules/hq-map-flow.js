/*
 * hq-map-flow.js — the Personal HQ Map's Flow arrangement as data (PRD 767).
 *
 * Flow lays the HQ's stations out in three lanes, left to right: what Ori
 * reads, the lead agent, and what needs the user. This module turns what the
 * page already has (each station's own state(), the Details header's counts,
 * the agent list and the lead's waiting sentence) into those lanes. It adds no
 * per-station state logic: a source node's words come from stationCard(), so
 * they match the Details stations grid.
 *
 * It also remembers, per workspace in the browser, whether the HQ Map shows
 * Flow or the free-position field (Free).
 *
 * Pure: no DOM. workspace-command.js renders the model.
 */

import { HQ_LABELS } from './hq-labels.js';
import { NOTES_STATION_KEY, STATE_WORDS, stationCard } from './hq-stations-grid.js';

export const WATCHTOWER_STATION_KEY = 'watchtower';
export const DAILY_BRIEF_STATION_KEY = 'daily-brief';

// The left lane, in order. Watchtower joins last, as "Other workspaces", only
// when the Daily Brief reads at least one other workspace (FR5).
export const FLOW_SOURCES = Object.freeze([
  Object.freeze({ key: NOTES_STATION_KEY, label: HQ_LABELS.notes }),
  Object.freeze({ key: 'email', label: HQ_LABELS.email }),
  Object.freeze({ key: 'calendar-ops', label: HQ_LABELS.calendar }),
  Object.freeze({ key: 'memory', label: HQ_LABELS.memory }),
  Object.freeze({ key: WATCHTOWER_STATION_KEY, label: HQ_LABELS.otherWorkspaces })
]);

// The right lane's node keys, in order.
export const FLOW_NEEDS = Object.freeze(['needs-you', 'follow-ups', 'brief', 'tasks']);

const CHECKING = STATE_WORDS.checking;

function clean(text) {
  return String(text === undefined || text === null ? '' : text)
    .replace(/\s+/g, ' ')
    .trim();
}

function plural(count, one, many) {
  return count + ' ' + (count === 1 ? one : many);
}

function same(a, b) {
  return a.toLowerCase() === b.toLowerCase();
}

// The subtitle's words, each said once. A part that says nothing ("—"), one
// the label or state word already says, or one a later part says again is
// dropped: the rule renderMapHQStations() applies to a station's value and
// description.
function subtitleParts(head, rest) {
  const said = head.map(clean).filter(Boolean);
  const all = (Array.isArray(rest) ? rest : [rest]).map(clean);
  const kept = [];
  all.forEach((part, index) => {
    if (!part || !/[\p{L}\p{N}]/u.test(part)) return;
    if (said.some(seen => same(seen, part)) || kept.some(seen => same(seen, part))) return;
    const lower = part.toLowerCase();
    const repeatedLater = all
      .slice(index + 1)
      .some(later => later && later.length > part.length && later.toLowerCase().includes(lower));
    if (!repeatedLater) kept.push(part);
  });
  return kept;
}

// The label, then the state word unless it only repeats the label.
function headParts(label, stateWord) {
  const name = clean(label);
  const state = clean(stateWord);
  return state && !same(state, name) ? [name, state] : [name];
}

/**
 * A node's accessible name: "<label>, <state word>, <subtitle>" with repeats
 * removed (FR10). The label and the state word always stay.
 */
export function flowAccessibleName(label, stateWord, ...subtitle) {
  const head = headParts(label, stateWord);
  return [...head, ...subtitleParts(head, subtitle)].filter(Boolean).join(', ');
}

function toneFor(stateWord) {
  switch (stateWord) {
    case STATE_WORDS.needsYou:
      return 'attention';
    case STATE_WORDS.error:
      return 'error';
    case STATE_WORDS.notSetUp:
      return 'unset';
    case CHECKING:
      return 'muted';
    default:
      return 'ready';
  }
}

// `aliases` are other names the node's words may repeat (a station's registry
// label, "Calendar Ops" for the "Calendar" node).
function node({
  key,
  label,
  stateWord,
  subtitle,
  tone,
  dashed = false,
  station = '',
  aliases = []
}) {
  const head = headParts(label, stateWord);
  const shown = subtitleParts([...head, ...aliases], subtitle);
  return {
    key,
    station,
    label,
    stateWord: clean(stateWord),
    subtitle: shown.join(' · '),
    dashed: Boolean(dashed),
    tone: tone || toneFor(stateWord),
    accessibleName: [...head, ...shown].join(', ')
  };
}

function stationNode(station, label) {
  const card = stationCard(station);
  return node({
    key: card.key,
    station: card.key,
    label,
    aliases: [card.label],
    stateWord: card.stateWord,
    subtitle: card.summary,
    dashed: !card.configured
  });
}

function byKey(stations) {
  const map = new Map();
  for (const station of Array.isArray(stations) ? stations : []) {
    if (station && station.key && !map.has(station.key)) map.set(String(station.key), station);
  }
  return map;
}

function sourceNodes(stations, briefScopeHasOthers) {
  return FLOW_SOURCES.filter(
    source => source.key !== WATCHTOWER_STATION_KEY || briefScopeHasOthers === true
  )
    .filter(source => stations.has(source.key))
    .map(source => stationNode(stations.get(source.key), source.label));
}

function count(value) {
  if (value === null || value === undefined || value === '') return null;
  const number = Number(value);
  return Number.isFinite(number) ? Math.max(0, Math.floor(number)) : null;
}

function unique(values) {
  return Array.from(new Set((Array.isArray(values) ? values : []).map(clean).filter(Boolean)));
}

function needsNodes(stations, counts) {
  const needs = count(counts.needsYou) || 0;
  const followUps = count(counts.followUps);
  const open = count(counts.openTickets);
  const brief = stations.get(DAILY_BRIEF_STATION_KEY);
  const briefCard = brief ? stationCard(brief) : null;
  return [
    node({
      key: 'needs-you',
      label: HQ_LABELS.needsYou,
      stateWord: needs ? String(needs) : 'Clear',
      subtitle: needs ? unique(counts.needsYouFrom).join(', ') : HQ_LABELS.nothingNeedsYou,
      tone: needs ? 'attention' : 'ready'
    }),
    node({
      key: 'follow-ups',
      label: HQ_LABELS.followUps,
      stateWord: followUps === null ? CHECKING : followUps ? followUps + ' open' : 'Clear',
      subtitle:
        followUps === null
          ? 'Reading your follow-ups'
          : followUps
            ? plural(followUps, 'reply or promise', 'replies and promises') + ' to keep'
            : 'Nothing to chase',
      tone: followUps === null ? 'muted' : followUps ? 'attention' : 'ready'
    }),
    briefCard
      ? node({
          key: 'brief',
          station: DAILY_BRIEF_STATION_KEY,
          label: HQ_LABELS.todaysBrief,
          aliases: [briefCard.label],
          stateWord: briefCard.stateWord,
          subtitle: briefCard.summary,
          dashed: !briefCard.configured
        })
      : node({
          key: 'brief',
          label: HQ_LABELS.todaysBrief,
          stateWord: STATE_WORDS.notSetUp,
          subtitle: 'No Daily Brief yet',
          dashed: true
        }),
    node({
      key: 'tasks',
      label: HQ_LABELS.tasks,
      stateWord: open === null ? CHECKING : open ? open + ' open' : 'Clear',
      subtitle:
        open === null
          ? 'Counting tickets'
          : clean(counts.ticketLine) || (open ? '' : 'No open tickets'),
      tone: open === null ? 'muted' : 'ready'
    })
  ];
}

function leadCard(agents, waiting) {
  const list = (Array.isArray(agents) ? agents : []).filter(agent => agent && clean(agent.name));
  const lead = list.find(agent => agent.entry) || null;
  if (!lead) return null;
  const name = clean(lead.name);
  const status = clean(lead.status) || 'Idle';
  const sentence = clean(waiting);
  const specialists = list.filter(agent => agent !== lead).map(agent => clean(agent.name));
  const specialistsLine = specialists.length
    ? HQ_LABELS.specialists + ': ' + specialists.join(', ')
    : HQ_LABELS.noSpecialists;
  return {
    key: 'lead',
    name,
    encodedName: clean(lead.encodedName) || name,
    status,
    waiting: sentence,
    specialists,
    specialistsLine,
    accessibleName: [
      name,
      HQ_LABELS.lead,
      ...subtitleParts(
        [name, HQ_LABELS.lead],
        [status, sentence ? HQ_LABELS.waitingOnYou + ': ' + sentence : '', specialistsLine]
      )
    ].join(', ')
  };
}

/**
 * The Flow model.
 *
 * @param {object} input
 * @param {{key:string,label:string,state:{value?:string,description?:string,tone?:string}}[]} input.stations
 *   every map station with what its state() returned, in registry order
 * @param {{needsYou?:number,needsYouFrom?:string[],followUps?:number|null,openTickets?:number|null,ticketLine?:string}} [input.counts]
 *   the values the Details header tiles already show
 * @param {{name:string,encodedName?:string,entry?:boolean,status?:string}[]} [input.agents]
 * @param {string} [input.waiting] the lead's waiting sentence, '' when it waits on nothing
 * @param {boolean} [input.briefScopeHasOthers] the Daily Brief reads another workspace
 * @returns {{sources: object[], lead: object|null, needs: object[]}}
 */
export function flowModel({
  stations = [],
  counts = {},
  agents = [],
  waiting = '',
  briefScopeHasOthers = false
} = {}) {
  const registry = byKey(stations);
  return {
    sources: sourceNodes(registry, briefScopeHasOthers),
    lead: leadCard(agents, waiting),
    needs: needsNodes(registry, counts || {})
  };
}

/**
 * Whether the Daily Brief reads a workspace other than the HQ: a brief over
 * all workspaces does; a selected scope does when it names one besides the HQ.
 * Unknown (config not loaded) is no.
 */
export function briefScopeHasOthers(config, hqWorkspaceId) {
  if (!config || typeof config !== 'object') return false;
  if (config.scope !== 'selected') return config.scope === 'all' || !config.scope;
  const self = clean(hqWorkspaceId);
  return (Array.isArray(config.selected_workspace_ids) ? config.selected_workspace_ids : [])
    .map(clean)
    .some(id => id && id !== self);
}

// ── Arrangement memory (FR3) ────────────────────────────────────────────────

export const MAP_ARRANGEMENT_STORAGE_PREFIX = 'oriWorkspaceMapArrangement:';
export const MAP_ARRANGEMENTS = Object.freeze(['flow', 'free']);
export const DEFAULT_MAP_ARRANGEMENT = 'flow';

export function mapArrangementStorageKey(slug) {
  return MAP_ARRANGEMENT_STORAGE_PREFIX + clean(slug);
}

/** The remembered arrangement for this workspace; Flow when none or unreadable. */
export function readMapArrangement(storage, slug) {
  if (!storage || !clean(slug)) return DEFAULT_MAP_ARRANGEMENT;
  try {
    const value = storage.getItem(mapArrangementStorageKey(slug));
    return MAP_ARRANGEMENTS.includes(value) ? value : DEFAULT_MAP_ARRANGEMENT;
  } catch (_error) {
    return DEFAULT_MAP_ARRANGEMENT;
  }
}

/** Remembers the arrangement. A browser that refuses storage simply forgets. */
export function writeMapArrangement(storage, slug, value) {
  if (!storage || !clean(slug) || !MAP_ARRANGEMENTS.includes(value)) return;
  try {
    storage.setItem(mapArrangementStorageKey(slug), value);
  } catch (_error) {
    /* best effort */
  }
}
