/*
 * hq-attention.js — the lines of the Personal HQ's "Needs your attention" strip.
 *
 * Pure: workspace-command.js reads each source (the tickets waiting on the
 * user, the setup mounts the HQ hides above its header, the goal's status, the
 * Daily Brief and GitHub proposals) and passes plain values in. This decides
 * which lines show, in what order, and how they are capped. Each action is a
 * descriptor the command view dispatches to the handler the source already
 * has; nothing here acts.
 *
 * Priority (PRD 766 FR11): the lead waiting on input, then setup still needed
 * (wizard, blueprint intake, group requirement, Email connect, first task, no
 * model), then a brief ready today, then GitHub proposals.
 */

export const ATTENTION_LIMIT = 3;
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

function leadLine(waiting) {
  const items = (Array.isArray(waiting) ? waiting : []).filter(item => item && item.taskId);
  if (!items.length) return null;
  // The lead's own ticket first; the rest are counted.
  const first = items.find(item => item.isLead) || items[0];
  const who = String(first.agent || '').trim() || 'An agent';
  const title = String(first.title || '').trim();
  const rest = items.length - 1;
  const sentence =
    who +
    (first.isLead ? ' (Lead)' : '') +
    ' is waiting on you' +
    (title ? ': ' + title : '') +
    (rest > 0 ? ', and ' + plural(rest, 'other ticket', 'other tickets') : '') +
    '.';
  const actions = [{ label: 'Answer', kind: 'ticket', id: String(first.taskId) }];
  if (rest > 0) actions.push({ label: 'See all', kind: 'tickets-waiting' });
  return line('lead', 'Lead', sentence, actions);
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

/**
 * Every line the strip could show, highest priority first.
 *
 * @param {object} state
 * @param {{agent:string,isLead:boolean,title:string,taskId:string}[]} [state.waiting]
 * @param {{key:string,category?:string,sentence:string,actions?:object[]}[]} [state.setup]
 * @param {{reason:string,model?:boolean}|null} [state.goal]
 * @param {{readyToday:boolean,time?:string}|null} [state.brief]
 * @param {{installed:boolean,count:number}|null} [state.github]
 */
export function hqAttentionLines(state = {}) {
  return [
    leadLine(state.waiting),
    ...setupLines(state.setup, state.goal),
    briefLine(state.brief),
    githubLine(state.github)
  ].filter(Boolean);
}

/**
 * The lines to show and how many wait behind "and N more". Expanded shows all.
 */
export function capAttentionLines(lines, { expanded = false, limit = ATTENTION_LIMIT } = {}) {
  const all = Array.isArray(lines) ? lines : [];
  if (expanded || all.length <= limit) return { shown: all, hidden: 0 };
  return { shown: all.slice(0, limit), hidden: all.length - limit };
}

// ── Needs you panel (FR14) ──────────────────────────────────────────────────

export const NEEDS_YOU_LIMIT = 5;

// A station's chip in the Needs you panel; others use their own name.
const STATION_CHIPS = {
  email: 'Email',
  'calendar-ops': 'Calendar',
  'daily-brief': 'Brief',
  'second-brain': 'Notes'
};

/**
 * The Needs you panel's rows: what the inbox says needs the user, then the
 * stations asking for the user (Needs you or Error), then the stale-notes
 * nudge. Each row is a chip, one sentence and one action descriptor.
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
  mail = [],
  stations = [],
  notes = null,
  limit = NEEDS_YOU_LIMIT
} = {}) {
  const rows = [];
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
  return {
    rows: rows.slice(0, limit),
    total: rows.length,
    more: Math.max(0, rows.length - limit)
  };
}
