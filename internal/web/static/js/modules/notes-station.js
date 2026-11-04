// notes-station.js — what the Notes station's panel shows: which sections, in
// which order, how a look's items group, what the building's badge says, and the
// panel's HTML. Pure helpers only (no DOM, no network), so notes-station.test.js
// can exercise every state under plain Node. workspace-command.js owns fetching
// and clicks.

import {
  OPEN_FOLDER_LABEL,
  previewParagraphs,
  secondBrainChecklistView,
  secondBrainProjectsView,
  secondBrainProjectLink,
  sinceLabel
} from './personal-assistant-second-brain.js';

export const NOTES_ITEM_ACTIONS = ['follow_up', 'done', 'dismiss'];

// A look older than this says so beside Look again.
const STALE_LOOK_MS = 24 * 60 * 60 * 1000;
const MAX_PEOPLE_SHOWN = 10;

function plural(count, one, many) {
  return count + ' ' + (count === 1 ? one : many);
}

function escapeHtml(value) {
  return String(value ?? '')
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;');
}

// safeLink keeps an Obsidian link and nothing else: the panel never puts any
// other scheme in an href.
export function safeLink(link) {
  const value = String(link || '');
  return value.startsWith('obsidian://open?') ? value : '';
}

export function isCreatedByOri(secondBrain) {
  return secondBrain?.source?.origin === 'created_by_ori';
}

// notesPanelAvailable: the panel exists only for a connected folder. Nothing
// connected, or a folder that cannot be found, keeps the drawer card.
export function notesPanelAvailable(secondBrain) {
  return secondBrain?.status === 'connected' && Boolean(secondBrain?.source?.name);
}

// openItemCount is what the look found minus what the user has triaged.
export function openItemCount(lastLook) {
  const open = Number(lastLook?.open_items) || 0;
  const handled = Number(lastLook?.handled) || 0;
  return Math.max(0, open - handled);
}

// notesNudge is the server's word that the notes deserve another look (the same
// rule as Today's Needs you row), or null.
export function notesNudge(secondBrain) {
  const nudge = secondBrain?.nudge;
  if (!nudge) return null;
  const cameDue = Number(nudge.came_due) || 0;
  const stale = nudge.stale === true;
  if (!stale && !cameDue) return null;
  return { stale, days: Number(nudge.days) || 0, cameDue };
}

// nudgeParts are the badge's words for a nudge: what came due, and how old the
// look is once that is the point.
function nudgeParts(nudge) {
  const parts = [];
  if (nudge.cameDue) parts.push(nudge.cameDue + ' came due');
  if (nudge.stale) parts.push('looked ' + nudge.days + 'd ago');
  return parts;
}

// notesBadge is the building's status. A folder Ori set up leads with its Inbox,
// a connected vault with what is still open. When the notes deserve another
// look the building turns to attention and says why: what came due, or how old
// the last look is.
export function notesBadge(secondBrain, now = Date.now()) {
  const name = String(secondBrain?.source?.name || '');
  if (secondBrain?.status === 'missing' && name) {
    return { value: "Can't find " + name, description: 'needs attention', tone: 'attention' };
  }
  if (!notesPanelAvailable(secondBrain)) {
    return { value: 'Not set up', description: 'connect or set one up' };
  }
  const at = secondBrain.last_look?.at;
  const since = at ? shortSince(at, now) : '';
  const nudge = secondBrain.last_look ? notesNudge(secondBrain) : null;
  const tone = nudge ? 'attention' : 'clear';
  if (isCreatedByOri(secondBrain)) {
    const description = [createdDescription(secondBrain, since)]
      .concat(nudge ? nudgeParts(nudge) : [])
      .join(' · ');
    if (!Number.isFinite(secondBrain.inbox_count)) {
      return { value: name, description, tone };
    }
    return { value: secondBrain.inbox_count + ' in Inbox', description, tone };
  }
  if (!secondBrain.last_look) {
    return { value: name, description: 'connected', tone: 'clear' };
  }
  const open = openItemCount(secondBrain.last_look) + ' open';
  if (nudge) {
    // The building's one visible line carries the reason; the full sentence is
    // its description, which the rail row and the building's name read out.
    const reasons = nudgeParts(nudge);
    return { value: reasons[0], description: [open].concat(reasons).join(' · '), tone };
  }
  const description = since ? 'connected · last look ' + since : 'connected';
  return { value: open, description, tone };
}

// createdDescription is the line under a folder Ori set up. Once the user has
// taken a note over it says so plainly, since that is the promise working: "set
// up by Ori · 14 written by Ori · 2 are yours now".
function createdDescription(secondBrain, since) {
  const yours = Number(secondBrain?.fill_in?.user_owned) || 0;
  if (yours > 0) {
    const written = Number(secondBrain?.written_by_ori?.count) || 0;
    const verb = yours === 1 ? 'is' : 'are';
    return `set up by Ori · ${written} written by Ori · ${yours} ${verb} yours now`;
  }
  return since ? 'set up by Ori · last look ' + since : 'set up by Ori';
}

// shortSince is "2h" or "3d", for the badge's narrow line.
function shortSince(at, now) {
  const then = Date.parse(at || '');
  if (!Number.isFinite(then)) return '';
  const minutes = Math.max(0, Math.round((Number(now) - then) / 60000));
  if (minutes < 1) return 'just now';
  if (minutes < 60) return minutes + 'm';
  const hours = Math.round(minutes / 60);
  if (hours < 24) return hours + 'h';
  return Math.round(hours / 24) + 'd';
}

// isHandled says the user has answered the item: triaged in the panel, or a
// follow-up already made from it some other way (`accepted`, read fresh by the
// server and never stored).
export function isHandled(item) {
  return Boolean(item?.acted_as || item?.accepted);
}

// openLoopGroups files each untouched item once: Dated (soonest first), then
// Decisions, then Stale (its note has sat untouched), then Other. Triaged and
// accepted items go to handled.
export function openLoopGroups(items, stale) {
  const staleNotes = new Set((Array.isArray(stale) ? stale : []).map(note => note?.note));
  const groups = { dated: [], decisions: [], stale: [], other: [], handled: [] };
  for (const item of Array.isArray(items) ? items : []) {
    if (!item?.id || !item?.text) continue;
    if (isHandled(item)) groups.handled.push(item);
    else if (item.due) groups.dated.push(item);
    else if (item.in_decision) groups.decisions.push(item);
    else if (staleNotes.has(item.note)) groups.stale.push(item);
    else groups.other.push(item);
  }
  groups.dated.sort((a, b) => String(a.due).localeCompare(String(b.due)));
  return groups;
}

// itemActions are the triage buttons one item offers. A seeded item already came
// from a follow-up, so it offers no Follow up. A handled item with a follow-up
// can still be finished; anything else handled offers nothing.
export function itemActions(item) {
  if (item?.acted_as === 'follow_up') return ['done'];
  // A follow-up made elsewhere can still be finished from here.
  if (!item?.acted_as && item?.accepted) return ['done'];
  if (item?.acted_as) return [];
  return NOTES_ITEM_ACTIONS.filter(action => !(action === 'follow_up' && item?.seeded));
}

// suggestedCount is how many items the bulk action would still add.
export function suggestedCount(items) {
  return (Array.isArray(items) ? items : []).filter(
    item => item?.suggested === true && !isHandled(item) && !item?.seeded
  ).length;
}

// lookAgeLine nudges only once the look is a day old.
export function lookAgeLine(at, now = Date.now()) {
  const then = Date.parse(at || '');
  if (!Number.isFinite(then) || Number(now) - then <= STALE_LOOK_MS) return '';
  return 'Looked ' + sinceLabel(at, now) + ' — Look again?';
}

export const LOOK_TOO_SOON_LINE = 'Looked less than 10 minutes ago';

// LOOK_AGAIN_AFTER_MS is the server's rate limit on Look again.
const LOOK_AGAIN_AFTER_MS = 10 * 60 * 1000;

// lookTooSoonLine is the answer to a Look again inside the rate limit: when the
// last look was and how long to wait. The wait comes from the server's
// Retry-After when it sent one, else from the last look's time; with neither,
// the plain sentence stands.
export function lookTooSoonLine({ retryAfterSeconds, lastLookAt, now = Date.now() } = {}) {
  let wait = Number(retryAfterSeconds) * 1000;
  const then = Date.parse(lastLookAt || '');
  if (!(wait > 0) && Number.isFinite(then)) wait = LOOK_AGAIN_AFTER_MS - (Number(now) - then);
  if (!(wait > 0)) return LOOK_TOO_SOON_LINE;
  const minutes = Math.max(1, Math.ceil(wait / 60000));
  const looked = Number.isFinite(then) ? 'Looked ' + sinceLabel(lastLookAt, now) + '. ' : '';
  return looked + 'Try again in ' + plural(minutes, 'minute', 'minutes') + '.';
}

// LOOK_STILL_READING_LINE is what the panel says when a look outlasts its poll.
export const LOOK_STILL_READING_LINE = 'Still reading. Check back in a moment.';

// NOTES_PRIVACY_LINE sits under the look: what Ori keeps of the notes.
export const NOTES_PRIVACY_LINE = 'Ori keeps the folder path and these lines, never your notes.';

// The panel's one-liner under its title (Issue 776 FR3): what the folder is and
// who writes there, by origin, and how it differs from Memory.
export const NOTES_ONE_LINER_CREATED =
  "Ori's notes about you, in a folder you own. Ori writes, you edit, and Ori never reads your own notes back.";
export const NOTES_ONE_LINER_CONNECTED =
  'Your notes, read-only. Ori keeps the open items it found, never your notes.';

export function notesOneLiner(secondBrain) {
  if (!secondBrain?.source?.name || secondBrain?.status === 'not_connected') return '';
  return isCreatedByOri(secondBrain) ? NOTES_ONE_LINER_CREATED : NOTES_ONE_LINER_CONNECTED;
}

// MAX_ITEMS_SHOWN is how many open items the panel lists before Show all.
export const MAX_ITEMS_SHOWN = 30;

const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];

// localDay is now's calendar day where the user is, as YYYY-MM-DD, the form a
// look writes dates in.
function localDay(now) {
  const date = new Date(Number(now));
  const pad = value => String(value).padStart(2, '0');
  return date.getFullYear() + '-' + pad(date.getMonth() + 1) + '-' + pad(date.getDate());
}

// dueChip is an item's date as a person reads it: "Due Oct 25", "Due today" or
// "Overdue · Oct 3". The date is a calendar day, so it is never shifted by a
// time zone. Anything that is not a date shows nothing.
export function dueChip(due, now = Date.now()) {
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(String(due || ''));
  if (!match) return null;
  const month = MONTHS[Number(match[2]) - 1];
  if (!month) return null;
  const label = month + ' ' + Number(match[3]);
  const today = localDay(now);
  if (due === today) return { label: 'Due today', tone: 'today' };
  if (due < today) return { label: 'Overdue · ' + label, tone: 'overdue' };
  return { label: 'Due ' + label, tone: 'upcoming' };
}

// sinceLookLine compares the look with the one before it.
export function sinceLookLine(result) {
  const added = Number(result?.new_since) || 0;
  const done = Number(result?.done_since) || 0;
  if (!added && !done) return '';
  return 'Since your last look: ' + added + ' new, ' + done + ' done.';
}

// lookCountsLine is the look's numbers in one line: what is open, where, how
// many carry dates, and how many sit in open decisions.
export function lookCountsLine(result) {
  const counts = result?.counts;
  if (!counts) return '';
  const items = Array.isArray(result?.items) ? result.items : [];
  const decisions = items.filter(item => item?.in_decision).length;
  const parts = [
    plural(Number(counts.open_items) || 0, 'open item', 'open items') +
      ' in ' +
      plural(Number(counts.notes_with_open) || 0, 'note', 'notes'),
    (Number(counts.dated) || 0) + ' with dates'
  ];
  if (decisions) parts.push(decisions + ' in decisions');
  parts.push('read ' + plural(Number(counts.notes) || 0, 'note', 'notes'));
  let line = parts.join(' · ');
  if (counts.partial) line += ' (partial look)';
  return line;
}

// noteLabel is a note's path without its extension.
function noteLabel(note) {
  return String(note || '').replace(/\.(md|markdown)$/i, '');
}

// notesSections is the panel's fixed order. Each section says how many things it
// holds; one with nothing shows its one line. Inbox, Today, Capture and Projects
// exist only for a folder Ori set up.
// The People tab's empty line for a folder Ori set up (Issue 776 FR22): People
// fill from the Profile, not from a look.
export const NOTES_PEOPLE_EMPTY_CREATED =
  'No people in your notes yet. Approve a person on your Profile and Ori writes one.';

// mergePeople joins the look's people (with their mentions) and the People
// notes on disk (by file name), one row per name, the look's first.
export function mergePeople(lookPeople, listed) {
  const rows = [];
  const byName = new Map();
  for (const person of Array.isArray(lookPeople) ? lookPeople : []) {
    const name = String(person?.name || '').trim();
    if (!name || byName.has(name.toLowerCase())) continue;
    const row = { ...person, name };
    byName.set(name.toLowerCase(), row);
    rows.push(row);
  }
  for (const person of Array.isArray(listed) ? listed : []) {
    const name = String(person?.name || '').trim();
    if (!name) continue;
    const known = byName.get(name.toLowerCase());
    if (known) {
      known.note = true;
      if (!known.link && person.link) known.link = person.link;
      continue;
    }
    const row = { name, link: person.link || '', note: true, kind: String(person.kind || '') };
    byName.set(name.toLowerCase(), row);
    rows.push(row);
  }
  return rows;
}

// previewKey names the note a row opens inline.
export function previewKey(kind, name) {
  return `${kind}:${name}`;
}

export function notesSections({ secondBrain, look, inbox, people: listedPeople, missions } = {}) {
  const created = isCreatedByOri(secondBrain);
  const result = look?.result || null;
  const groups = openLoopGroups(result?.items, result?.stale);
  const open =
    groups.dated.length + groups.decisions.length + groups.stale.length + groups.other.length;
  const sections = [];
  if (created) {
    sections.push({ key: 'capture', title: 'Capture', count: 0 });
    sections.push({
      key: 'inbox',
      title: 'Inbox',
      count: Number(inbox?.total) || (Array.isArray(inbox?.notes) ? inbox.notes.length : 0),
      notes: Array.isArray(inbox?.notes) ? inbox.notes : [],
      partial: inbox?.partial === true,
      loading: !inbox,
      empty: 'Nothing in Inbox yet.',
      // An empty Inbox shows the first week's three things to do (Issue 776 FR19).
      checklist: secondBrainChecklistView(secondBrain, missions || [])
    });
    const today = secondBrain?.today_note || null;
    sections.push({
      key: 'today',
      title: 'Today',
      count: today?.state && today.state !== 'none' ? 1 : 0,
      today
    });
  }
  sections.push({
    key: 'loops',
    title: 'Open loops',
    count: open,
    groups,
    suggested: suggestedCount(result?.items),
    lookId: String(look?.look_id || result?.id || ''),
    empty:
      look?.state === 'running' ? 'Reading your notes…' : 'Nothing open in your notes right now.'
  });
  const lookPeople = Array.isArray(result?.people) ? result.people.slice(0, MAX_PEOPLE_SHOWN) : [];
  const people = created ? mergePeople(lookPeople, listedPeople?.people) : lookPeople;
  sections.push({
    key: 'people',
    title: 'People',
    count: people.length,
    people,
    empty: created ? NOTES_PEOPLE_EMPTY_CREATED : 'No people found in your notes yet.'
  });
  if (created) {
    const projects = secondBrainProjectsView(secondBrain);
    sections.push({
      key: 'projects',
      title: 'Projects',
      count: projects.length,
      projects,
      empty: 'No project folders yet.'
    });
  }
  return sections;
}

// firstNonEmptySection is where the panel opens. Capture is a box, not content,
// so it never counts; with nothing anywhere the panel stays at the top.
export function firstNonEmptySection(sections) {
  const found = (Array.isArray(sections) ? sections : []).find(
    section => section.key !== 'capture' && section.count > 0
  );
  return found ? found.key : '';
}

// notesTabs folds the sections into the panel's views. Capture and Today sit with
// Inbox, since they are about what lands there; Look again sits with Open loops.
// A connected vault has no Inbox view and no Projects.
export function notesTabs(sections) {
  const list = Array.isArray(sections) ? sections : [];
  const pick = keys => list.filter(section => keys.includes(section.key));
  const tab = (key, title, keys, count) => {
    const held = pick(keys);
    return { key, title, sections: held, count: count(held) };
  };
  const total = held => held.reduce((sum, section) => sum + section.count, 0);
  const more = pick(['projects']).length ? 'People & Projects' : 'People';
  return [
    tab('inbox', 'Inbox', ['capture', 'inbox', 'today'], held =>
      total(held.filter(section => section.key === 'inbox'))
    ),
    tab('loops', 'Open loops', ['loops'], total),
    tab('more', more, ['people', 'projects'], total)
  ].filter(entry => entry.sections.length > 0);
}

// defaultNotesTab is the view the panel opens on: the one holding the first
// section with something in it, else the first view.
export function defaultNotesTab(sections) {
  const tabs = notesTabs(sections);
  const first = firstNonEmptySection(sections);
  const found = tabs.find(entry => entry.sections.some(section => section.key === first));
  return (found || tabs[0])?.key || '';
}

// activeNotesTab is the view to show. A view this folder does not have falls
// back to the first.
export function activeNotesTab(tabs, wanted) {
  return tabs.find(entry => entry.key === wanted) || tabs[0];
}

// nextNotesTab is the view an arrow key moves to, wrapping at the ends.
export function nextNotesTab(tabs, current, step) {
  const index = tabs.findIndex(entry => entry.key === current);
  if (index < 0 || !tabs.length) return current;
  return tabs[(index + step + tabs.length) % tabs.length].key;
}

// notesPanelCount is the header chip: Inbox for a folder Ori set up, open items
// for a connected vault.
export function notesPanelCount(sections) {
  const list = Array.isArray(sections) ? sections : [];
  const inbox = list.find(section => section.key === 'inbox');
  if (inbox) return inbox.loading ? '—' : inbox.count;
  const loops = list.find(section => section.key === 'loops');
  return loops ? loops.count : 0;
}

const ACTION_LABELS = { follow_up: 'Follow up', done: 'Done', dismiss: 'Dismiss' };
const HANDLED_LABELS = { follow_up: 'Follow-up made', done: 'Done', dismissed: 'Dismissed' };
const ACCEPTED_LABEL = 'In follow-ups';
const KIND_LABELS = { capture: 'Captured', seed: 'Seeded', follow_up: 'Follow-up', user: 'Yours' };

function obsidianAnchor(link, name) {
  const href = safeLink(link);
  if (!href) return '';
  return (
    '<a class="ws-notes-link" href="' +
    escapeHtml(href) +
    '" aria-label="Open ' +
    escapeHtml(name) +
    ' in Obsidian">Open in Obsidian</a>'
  );
}

function itemRowHTML(item, lookId, now) {
  const actions = itemActions(item)
    .map(
      action =>
        '<button type="button" class="ws-notes-action" data-cmd-modal-action="notes-item-' +
        action +
        '" data-cmd-id="' +
        escapeHtml(item.id) +
        '" aria-label="' +
        escapeHtml(ACTION_LABELS[action] + ': ' + item.text) +
        '">' +
        ACTION_LABELS[action] +
        '</button>'
    )
    .join('');
  const facts = [noteLabel(item.note)];
  if (item.acted_as) facts.push(HANDLED_LABELS[item.acted_as] || '');
  else if (item.accepted) facts.push(ACCEPTED_LABEL);
  const chip = dueChip(item.due, now);
  const due = chip
    ? '<span class="ws-notes-due is-' + chip.tone + '">' + escapeHtml(chip.label) + '</span>'
    : '';
  return (
    '<li class="ws-notes-item" data-notes-item="' +
    escapeHtml(item.id) +
    '" data-notes-look="' +
    escapeHtml(lookId) +
    '"><div class="ws-notes-item-main"><span class="ws-notes-item-text">' +
    escapeHtml(item.text) +
    '</span><span class="ws-notes-item-meta">' +
    escapeHtml(facts.filter(Boolean).join(' · ')) +
    '</span>' +
    due +
    '</div><div class="ws-notes-item-actions">' +
    actions +
    obsidianAnchor(item.link, noteLabel(item.note)) +
    '</div></li>'
  );
}

function loopsHTML(section, panel, now) {
  const { groups, lookId } = section;
  const named = [
    ['Dated', groups.dated],
    ['Decisions', groups.decisions],
    ['Stale', groups.stale],
    ['Other', groups.other]
  ];
  let html = '';
  if (section.suggested > 0) {
    html +=
      '<button type="button" class="ws-notes-bulk" data-cmd-modal-action="notes-add-suggested">Add ' +
      section.suggested +
      ' suggested as follow-ups</button>';
  }
  // A long look lists its first items and offers the rest, so the panel stays
  // a page rather than a scroll of hundreds of lines.
  let room = panel.showAll ? Infinity : MAX_ITEMS_SHOWN;
  for (const [title, items] of named) {
    if (!items.length || room <= 0) continue;
    const shown = items.slice(0, room);
    room -= shown.length;
    html +=
      '<h5 class="ws-notes-group">' +
      escapeHtml(title) +
      ' <span class="ws-notes-group-count">' +
      items.length +
      '</span></h5><ul class="ws-notes-list">' +
      shown.map(item => itemRowHTML(item, lookId, now)).join('') +
      '</ul>';
  }
  if (!panel.showAll && section.count > MAX_ITEMS_SHOWN) {
    html +=
      '<button type="button" class="ws-notes-action ws-notes-show-all" data-cmd-modal-action="notes-show-all">Show all ' +
      section.count +
      '</button>';
  }
  if (!section.count) html += '<p class="ws-notes-empty">' + escapeHtml(section.empty) + '</p>';
  if (groups.handled.length) {
    html +=
      '<details class="ws-notes-handled"' +
      (panel.handledOpen ? ' open' : '') +
      '><summary>' +
      groups.handled.length +
      ' handled</summary><ul class="ws-notes-list">' +
      groups.handled.map(item => itemRowHTML(item, lookId, now)).join('') +
      '</ul></details>';
  }
  return html;
}

// openButtonHTML is a row's Open: it expands the note Ori wrote inline. Rows of
// the user's own notes get none (Issue 776 FR16).
function openButtonHTML(kind, name, label, panel) {
  const key = previewKey(kind, name);
  const open = panel?.preview?.key === key;
  return (
    '<button type="button" class="ws-notes-action" data-cmd-modal-action="notes-preview" data-cmd-id="' +
    escapeHtml(key) +
    '" aria-expanded="' +
    (open ? 'true' : 'false') +
    '" aria-label="' +
    escapeHtml((open ? 'Close ' : 'Open ') + label) +
    '">' +
    (open ? 'Close' : 'Open') +
    '</button>'
  );
}

// previewHTML is the opened note as plain paragraphs (escaped text, never
// Markdown), with Open folder and Open in Obsidian beside it, or the server's
// sentence when it refused.
export function previewHTML(preview, key) {
  if (!preview || preview.key !== key) return '';
  let inner = '';
  if (preview.status === 'loading') inner = '<p class="ws-notes-muted">Opening…</p>';
  else if (preview.status === 'error')
    inner = '<p class="ws-notes-problem" role="alert">' + escapeHtml(preview.message) + '</p>';
  else if (preview.note) {
    inner =
      previewParagraphs(preview.note.body)
        .map(paragraph => '<p class="ws-notes-preview-text">' + escapeHtml(paragraph) + '</p>')
        .join('') +
      (preview.note.truncated
        ? '<p class="ws-notes-muted">Showing the start of this note.</p>'
        : '') +
      '<div class="ws-notes-preview-actions"><button type="button" class="ws-notes-action" data-cmd-modal-action="notes-open-folder">' +
      escapeHtml(OPEN_FOLDER_LABEL) +
      '</button>' +
      obsidianAnchor(preview.note.link, preview.note.title || preview.note.name) +
      '</div>';
  }
  return (
    '<div class="ws-notes-preview" data-notes-preview="' + escapeHtml(key) + '">' + inner + '</div>'
  );
}

// checklistHTML is the empty Inbox of a folder Ori set up: the first week's
// three rows with their check marks. Capture focuses the box above; the project
// question opens in the assistant's drawer.
export function checklistHTML(checklist) {
  if (!checklist?.show || !checklist.rows.length) return '';
  const rows = checklist.rows
    .map(row => {
      const label =
        escapeHtml(row.label) + (row.done ? '<span class="visually-hidden">, done</span>' : '');
      const control =
        row.id === 'capture'
          ? '<button type="button" class="ws-notes-link" data-cmd-modal-action="notes-focus-capture">' +
            label +
            '</button>'
          : '<a class="ws-notes-link" href="' +
            escapeHtml(row.href || '/?quest=second-brain-project') +
            '">' +
            label +
            '</a>';
      return (
        '<li class="ws-notes-checklist-row' +
        (row.done ? ' is-done' : '') +
        '" data-checklist-row="' +
        escapeHtml(row.id) +
        '"><span class="ws-notes-check" aria-hidden="true">' +
        (row.done ? '✓' : '○') +
        '</span>' +
        control +
        '</li>'
      );
    })
    .join('');
  return (
    '<p class="ws-notes-line">Three things to try this week:</p><ul class="ws-notes-checklist" data-notes-checklist>' +
    rows +
    '</ul>'
  );
}

function inboxHTML(section, now, panel) {
  if (section.loading) return '<p class="ws-notes-empty">Reading Inbox…</p>';
  if (!section.notes.length)
    return (
      '<p class="ws-notes-empty">' +
      escapeHtml(section.empty) +
      '</p>' +
      checklistHTML(section.checklist)
    );
  const rows = section.notes
    .map(
      note =>
        '<li class="ws-notes-item"><div class="ws-notes-item-main"><span class="ws-notes-item-text">' +
        escapeHtml(note.title || note.name) +
        '</span><span class="ws-notes-item-meta"><span class="ws-notes-kind">' +
        escapeHtml(KIND_LABELS[note.kind] || KIND_LABELS.user) +
        '</span> · ' +
        escapeHtml(sinceLabel(note.modified, now)) +
        '</span></div><div class="ws-notes-item-actions is-always">' +
        (note.owned_by_ori === true
          ? openButtonHTML('inbox', note.name, note.title || note.name, panel)
          : '') +
        obsidianAnchor(note.link, note.title || note.name) +
        '</div>' +
        previewHTML(panel?.preview, previewKey('inbox', note.name)) +
        '</li>'
    )
    .join('');
  const partial = section.partial
    ? '<p class="ws-notes-muted">Showing the newest ' +
      section.notes.length +
      ' of ' +
      section.count +
      '.</p>'
    : '';
  return '<ul class="ws-notes-list">' + rows + '</ul>' + partial;
}

function todayHTML(section, panel) {
  const today = section.today;
  if (!today) return '<p class="ws-notes-empty">Today\'s note is not available.</p>';
  if (today.state === 'none') {
    return (
      '<p class="ws-notes-line">No note for today yet.</p>' +
      '<a class="ws-notes-bulk" href="/?journal=1" data-notes-journal>Write today\'s Journal</a>'
    );
  }
  const line = today.state === 'ori' ? "Today's note is saved." : "Today's note is yours.";
  // Only today's note as Ori wrote it opens inline.
  const name = today.state === 'ori' && today.date ? `${today.date}.md` : '';
  return (
    '<p class="ws-notes-line">' +
    escapeHtml(line) +
    ' ' +
    (name ? openButtonHTML('daily', name, "today's note", panel) + ' ' : '') +
    obsidianAnchor(today.link, "today's note") +
    '</p>' +
    (name ? previewHTML(panel?.preview, previewKey('daily', name)) : '')
  );
}

// personMeta: how many times a look found the name, or that there is a note.
export function personMeta(person) {
  const mentions = Number(person?.mentions) || 0;
  if (mentions) return plural(mentions, 'mention', 'mentions');
  return person?.note ? 'A note in People' : plural(0, 'mention', 'mentions');
}

function peopleHTML(section) {
  if (!section.people.length)
    return '<p class="ws-notes-empty">' + escapeHtml(section.empty) + '</p>';
  return (
    '<ul class="ws-notes-list">' +
    section.people
      .map(
        person =>
          '<li class="ws-notes-item"><div class="ws-notes-item-main"><span class="ws-notes-item-text">' +
          escapeHtml(person.name) +
          '</span><span class="ws-notes-item-meta">' +
          escapeHtml(personMeta(person)) +
          '</span></div><div class="ws-notes-item-actions is-always">' +
          obsidianAnchor(person.link, person.name) +
          '</div></li>'
      )
      .join('') +
    '</ul>'
  );
}

function projectsHTML(section, panel) {
  if (!section.projects.length)
    return '<p class="ws-notes-empty">' + escapeHtml(section.empty) + '</p>';
  return (
    '<ul class="ws-notes-list">' +
    section.projects
      .map(
        project =>
          '<li class="ws-notes-item"><div class="ws-notes-item-main"><span class="ws-notes-item-text">' +
          escapeHtml(project.name) +
          '</span></div><div class="ws-notes-item-actions is-always">' +
          (project.oriNote ? openButtonHTML('project', project.name, project.name, panel) : '') +
          '<a class="ws-notes-action" href="' +
          escapeHtml(secondBrainProjectLink(project.name)) +
          '" aria-label="' +
          escapeHtml(project.action.ariaLabel) +
          '">' +
          escapeHtml(project.action.label) +
          '</a></div>' +
          (project.oriNote
            ? previewHTML(panel?.preview, previewKey('project', project.name))
            : '') +
          '</li>'
      )
      .join('') +
    '</ul>'
  );
}

function captureHTML(panel) {
  if (panel.captureReceipt) {
    return '<p class="ws-notes-receipt" role="status">' + escapeHtml(panel.captureReceipt) + '</p>';
  }
  const problem = panel.captureProblem
    ? '<p class="ws-notes-problem" role="alert">' + escapeHtml(panel.captureProblem) + '</p>'
    : '';
  return (
    '<div class="ws-notes-capture"><input type="text" class="ws-notes-capture-input" data-notes-capture ' +
    'aria-label="Quick note for Inbox" placeholder="Quick note…" maxlength="4000" value="' +
    escapeHtml(panel.captureDraft || '') +
    '"' +
    (panel.captureBusy ? ' disabled' : '') +
    '><button type="button" class="ws-notes-bulk" data-cmd-modal-action="notes-capture"' +
    (panel.captureBusy ? ' disabled' : '') +
    '>Save to Inbox</button></div>' +
    problem
  );
}

function footerHTML(look, panel, now) {
  const result = look?.result || null;
  const lines = [];
  if (look?.state === 'failed') {
    lines.push('<p class="ws-notes-problem">' + escapeHtml(result?.failure || '') + '</p>');
  }
  if (result?.headline)
    lines.push('<p class="ws-notes-line">' + escapeHtml(result.headline) + '</p>');
  if (result?.also_noticed) {
    lines.push(
      '<p class="ws-notes-muted">Also noticed: ' + escapeHtml(result.also_noticed) + '</p>'
    );
  }
  const since = sinceLookLine(result);
  if (since) lines.push('<p class="ws-notes-muted">' + escapeHtml(since) + '</p>');
  const counts = lookCountsLine(result);
  if (counts) {
    const at = result.finished_at ? sinceLabel(result.finished_at, now) : '';
    lines.push(
      '<p class="ws-notes-muted ws-notes-counts">' +
        escapeHtml(counts + (at ? ' · last look ' + at : '')) +
        '</p>'
    );
  }
  const age = lookAgeLine(result?.finished_at, now);
  if (age) lines.push('<p class="ws-notes-muted">' + escapeHtml(age) + '</p>');
  const message = panel.lookMessage
    ? '<p class="ws-notes-muted" role="status">' + escapeHtml(panel.lookMessage) + '</p>'
    : '';
  const running = look?.state === 'running';
  const privacy = result
    ? '<p class="ws-notes-muted">' + escapeHtml(NOTES_PRIVACY_LINE) + '</p>'
    : '';
  return (
    lines.join('') +
    message +
    '<button type="button" class="ws-notes-action" data-cmd-modal-action="notes-look-again"' +
    (running ? ' disabled' : '') +
    '>' +
    (running ? 'Reading…' : 'Look again') +
    '</button>' +
    privacy
  );
}

function sectionBodyHTML(section, panel, now) {
  switch (section.key) {
    case 'capture':
      return captureHTML(panel);
    case 'inbox':
      return inboxHTML(section, now, panel);
    case 'today':
      return todayHTML(section, panel);
    case 'loops':
      return loopsHTML(section, panel, now);
    case 'people':
      return peopleHTML(section);
    case 'projects':
      return projectsHTML(section, panel);
    default:
      return '';
  }
}

function tabsHTML(tabs, active) {
  return (
    '<div class="ws-cmd-system-tabs ws-notes-tabs" role="tablist" aria-label="Notes views">' +
    tabs
      .map(tab => {
        const selected = tab.key === active.key;
        return (
          '<button type="button" role="tab" id="notes-tab-' +
          tab.key +
          '" class="ws-cmd-system-tab' +
          (selected ? ' is-active' : '') +
          '" data-notes-tab data-cmd-modal-action="notes-tab" data-cmd-id="' +
          tab.key +
          '" aria-selected="' +
          selected +
          '" tabindex="' +
          (selected ? '0' : '-1') +
          '">' +
          escapeHtml(tab.title) +
          ' <span class="ws-notes-tab-count">' +
          tab.count +
          '</span></button>'
        );
      })
      .join('') +
    '</div>'
  );
}

// notesPanelHTML is the whole panel. `panel` is what workspace-command.js holds
// about it: look, inbox, status, message, and the capture box's state.
function oneLinerHTML(secondBrain) {
  const line = notesOneLiner(secondBrain);
  return line
    ? '<p class="ws-panel-one-liner" data-notes-one-liner>' + escapeHtml(line) + '</p>'
    : '';
}

export function notesPanelHTML({ secondBrain, panel = {}, now = Date.now() } = {}) {
  const look = panel.look || null;
  const sections = notesSections({
    secondBrain,
    look,
    inbox: panel.inbox,
    people: panel.people,
    missions: panel.missions
  });
  const tabs = notesTabs(sections);
  const active = activeNotesTab(tabs, panel.tab);
  const ready = panel.status === 'ready';
  const body =
    panel.status === 'error'
      ? '<p class="ws-notes-problem" role="alert">Ori could not read your notes view. Close this and try again.</p>'
      : !ready
        ? '<p class="ws-notes-empty">Reading your notes…</p>'
        : '<div role="tabpanel" class="ws-notes-tabpanel" aria-labelledby="notes-tab-' +
          active.key +
          '">' +
          active.sections
            .map(
              section =>
                '<section class="ws-notes-section" data-notes-section="' +
                section.key +
                '" aria-label="' +
                escapeHtml(section.title + ', ' + plural(section.count, 'item', 'items')) +
                '">' +
                // A section that is the whole tab is already named by the tab.
                (section.title === active.title
                  ? ''
                  : '<h4 class="ws-notes-section-title">' + escapeHtml(section.title) + '</h4>') +
                sectionBodyHTML(section, panel, now) +
                '</section>'
            )
            .join('') +
          (active.key === 'loops'
            ? '<section class="ws-notes-section ws-notes-footer" data-notes-section="footer" aria-label="About this look">' +
              footerHTML(look, panel, now) +
              '</section>'
            : '') +
          '</div>';
  const notice = panel.message
    ? '<p class="ws-notes-receipt" role="status">' + escapeHtml(panel.message) + '</p>'
    : '';
  return (
    '<header class="ws-cmd-modal-head"><h3 class="ws-cmd-modal-title">Notes</h3>' +
    '<span class="ws-cmd-modal-count">' +
    escapeHtml(panel.status === 'ready' ? notesPanelCount(sections) : '—') +
    '</span><div class="ws-cmd-modal-head-actions">' +
    (secondBrain?.source?.name && secondBrain?.status === 'connected'
      ? '<button type="button" class="ws-notes-action" data-cmd-modal-action="notes-open-folder">' +
        escapeHtml(OPEN_FOLDER_LABEL) +
        '</button>'
      : '') +
    '<button type="button" class="ws-notes-action" data-cmd-modal-action="notes-connection">Connection…</button>' +
    '<button type="button" class="ws-cmd-modal-close" data-cmd-modal-action="close" aria-label="Close Notes">×</button>' +
    '</div></header>' +
    oneLinerHTML(secondBrain) +
    (ready ? tabsHTML(tabs, active) : '') +
    '<div class="ws-cmd-modal-body ws-notes-body">' +
    notice +
    body +
    '</div>'
  );
}
