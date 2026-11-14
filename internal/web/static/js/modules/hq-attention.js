// One pure builder for the HQ's Needs you rows. Sources supply facts and
// action descriptors; workspace-command.js dispatches to their existing owners.
// Priority: waiting tickets, Review, mail, stations, notes, setup, brief, GitHub.
import { HQ_LABELS as L } from './hq-labels.js';

export const NEEDS_YOU_LIMIT = 5;
const SENTENCE_LIMIT = 160;
const MAX_ACTIONS = 2;

// Setup sources in the order the strip lists them.
export const SETUP_ORDER = ['wizard', 'intake', 'group', 'email', 'first-task', 'goal'];

/** One sentence, whitespace collapsed and cut at a word near the limit. */
export function attentionSentence(text) {
  const clean = String(text || '')
    .replace(/\s+/g, ' ')
    .trim();
  if (clean.length <= SENTENCE_LIMIT) return clean;
  const cut = clean.slice(0, SENTENCE_LIMIT - 1);
  const space = cut.lastIndexOf(' ');
  return (space > SENTENCE_LIMIT / 2 ? cut.slice(0, space) : cut) + '…';
}

function line(key, category, sentence, actions) {
  const text = attentionSentence(sentence);
  if (!text) return null;
  return {
    key,
    category,
    sentence: text,
    actions: (Array.isArray(actions) ? actions : [])
      .filter(action => action && String(action.label || '').trim())
      .slice(0, MAX_ACTIONS)
  };
}

function plural(count, one, many) {
  return count + ' ' + (count === 1 ? one : many);
}

function ticketRows(waiting, review) {
  const seen = new Set();
  const rows = [];
  const input = (Array.isArray(waiting) ? waiting : [])
    .filter(Boolean)
    .slice()
    .sort((a, b) => Number(Boolean(b.isLead)) - Number(Boolean(a.isLead)));
  for (const [items, reviewing] of [
    [input, false],
    [Array.isArray(review) ? review : [], true]
  ]) {
    for (const item of items) {
      const id = String(item?.taskId || '').trim();
      if (!id || seen.has(id)) continue;
      seen.add(id);
      const title = String(item.title || '').trim();
      const who =
        (String(item.agent || '').trim() || L.anAgent) + (item.isLead ? ' (' + L.lead + ')' : '');
      rows.push(
        line(
          'ticket:' + id,
          item.isLead ? L.lead : L.ticket,
          reviewing
            ? L.ticketReadyForReview.replace('{title}', title || L.ticket)
            : L.ticketWaiting.replace('{agent}', who).replace('{title}', title ? ': ' + title : ''),
          [{ label: reviewing ? L.review : L.answer, kind: 'ticket', id }]
        )
      );
    }
  }
  return rows;
}

function setupLines(setup, goal) {
  const byKey = new Map();
  for (const signal of Array.isArray(setup) ? setup : []) {
    if (!signal || !SETUP_ORDER.includes(signal.key) || byKey.has(signal.key)) continue;
    byKey.set(signal.key, signal);
  }
  const reason = String((goal && goal.reason) || '').trim();
  if (reason) {
    const model = goal.model === true || /\bmodel\b/i.test(reason);
    byKey.set('goal', {
      key: 'goal',
      category: 'Setup',
      sentence: reason,
      actions: [
        model ? { label: 'Set up a model', kind: 'model' } : { label: 'Open goal', kind: 'goal' }
      ]
    });
  }
  return SETUP_ORDER.map(key => byKey.get(key))
    .filter(Boolean)
    .map(signal =>
      line('setup:' + signal.key, signal.category || 'Setup', signal.sentence, signal.actions)
    );
}

function briefLine(brief) {
  if (!brief || brief.readyToday !== true) return null;
  const time = String(brief.time || '').trim();
  return line(
    'brief',
    'Brief',
    "Today's Daily Brief is ready" + (time ? ' (' + time + ')' : '') + '.',
    [{ label: 'Read', kind: 'brief' }]
  );
}

function githubLine(github) {
  const count = Number(github && github.count) || 0;
  if (!github || github.installed !== true || count <= 0) return null;
  return line(
    'github',
    'GitHub',
    plural(count, 'GitHub change waits', 'GitHub changes wait') + ' for your approval.',
    [{ label: 'Review', kind: 'github' }]
  );
}

// A station's chip in the Needs you panel; others use their own name.
const STATION_CHIPS = {
  email: 'Email',
  'calendar-ops': 'Calendar',
  'daily-brief': 'Brief',
  'second-brain': 'Notes'
};

/**
 * The single Needs you list, capped at five unless expanded. Every actionable
 * row has a chip, a sentence and up to two actions. Non-actionable setup progress
 * is not something that needs the person. The count is the number of rows.
 *
 * @param {object} input
 * @param {{from?:string,subject?:string,why?:string,action?:string,index:number}[]} [input.mail]
 * @param {{key:string,label:string,summary:string[],action:string}[]} [input.stations]
 *   cards that need the user, from hq-stations-grid.js
 * @param {{stale:boolean,days:number,cameDue:number}|null} [input.notes] notesNudge()
 * @param {number} [input.limit] rows shown (five; Infinity for "Show all")
 * @returns {{rows: object[], total: number, more: number}}
 */
export function hqNeedsYouRows({
  waiting = [],
  review = [],
  setup = [],
  goal = null,
  brief = null,
  github = null,
  suppressBrief = false,
  mail = [],
  stations = [],
  notes = null,
  limit = NEEDS_YOU_LIMIT
} = {}) {
  const rows = ticketRows(waiting, review);
  for (const item of Array.isArray(mail) ? mail : []) {
    const subject = String(item.subject || '').trim() || '(no subject)';
    const from = String(item.from || '').trim();
    const row = line(
      'mail:' + item.index,
      'Email',
      (from ? from + ': ' : '') + subject + (item.why ? ' — ' + String(item.why).trim() : ''),
      [{ label: String(item.action || 'Open'), kind: 'mail', index: item.index }]
    );
    if (row) rows.push(row);
  }
  for (const card of Array.isArray(stations) ? stations : []) {
    const summary = (card.summary || []).filter(Boolean).join(' · ');
    const row = line(
      'station:' + card.key,
      STATION_CHIPS[card.key] || card.label,
      card.label + (summary ? ': ' + summary : ''),
      [{ label: card.action || 'Open', kind: 'station', key: card.key }]
    );
    if (row) rows.push(row);
  }
  if (notes && (notes.stale || notes.cameDue)) {
    const parts = [];
    if (notes.cameDue) parts.push(plural(notes.cameDue, 'item', 'items') + ' came due');
    if (notes.stale) parts.push('the last look was ' + plural(notes.days, 'day', 'days') + ' ago');
    const row = line(
      'notes',
      'Notes',
      'Your notes deserve another look: ' + parts.join(', ') + '.',
      [{ label: 'Look again', kind: 'notes' }]
    );
    if (row) rows.push(row);
  }
  rows.push(
    ...setupLines(setup, goal),
    suppressBrief ? null : briefLine(brief),
    githubLine(github)
  );
  const actionable = rows.filter(row => row && row.actions.length);
  return {
    rows: actionable.slice(0, limit),
    total: actionable.length,
    more: Math.max(0, actionable.length - limit)
  };
}
