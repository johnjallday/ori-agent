// notes-station.js — what the Notes station's panel shows: which sections, in
// which order, how a look's items group, what the building's badge says, and the
// panel's HTML. Pure helpers only (no DOM, no network), so notes-station.test.js
// can exercise every state under plain Node. workspace-command.js owns fetching
// and clicks.

import {
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

// notesBadge is the building's status. A folder Ori set up leads with its Inbox,
// a connected vault with what is still open.
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
  if (isCreatedByOri(secondBrain)) {
    const description = since ? 'set up by Ori · last look ' + since : 'set up by Ori';
    if (!Number.isFinite(secondBrain.inbox_count)) {
      return { value: name, description, tone: 'clear' };
    }
    return { value: secondBrain.inbox_count + ' in Inbox', description, tone: 'clear' };
  }
  const description = since ? 'connected · last look ' + since : 'connected';
  if (!secondBrain.last_look) return { value: name, description, tone: 'clear' };
  return { value: openItemCount(secondBrain.last_look) + ' open', description, tone: 'clear' };
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

// openLoopGroups files each untouched item once: Dated (soonest first), then
// Decisions, then Stale (its note has sat untouched), then Other. Triaged items
// go to handled.
export function openLoopGroups(items, stale) {
  const staleNotes = new Set((Array.isArray(stale) ? stale : []).map(note => note?.note));
  const groups = { dated: [], decisions: [], stale: [], other: [], handled: [] };
  for (const item of Array.isArray(items) ? items : []) {
    if (!item?.id || !item?.text) continue;
    if (item.acted_as) groups.handled.push(item);
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
  if (item?.acted_as) return [];
  return NOTES_ITEM_ACTIONS.filter(action => !(action === 'follow_up' && item?.seeded));
}

// suggestedCount is how many items the bulk action would still add.
export function suggestedCount(items) {
  return (Array.isArray(items) ? items : []).filter(
    item => item?.suggested === true && !item?.acted_as && !item?.seeded
  ).length;
}

// lookAgeLine nudges only once the look is a day old.
export function lookAgeLine(at, now = Date.now()) {
  const then = Date.parse(at || '');
  if (!Number.isFinite(then) || Number(now) - then <= STALE_LOOK_MS) return '';
  return 'Looked ' + sinceLabel(at, now) + ' — Look again?';
}

export const LOOK_TOO_SOON_LINE = 'Looked less than 10 minutes ago';

// noteLabel is a note's path without its extension.
function noteLabel(note) {
  return String(note || '').replace(/\.(md|markdown)$/i, '');
}

// notesSections is the panel's fixed order. Each section says how many things it
// holds; one with nothing shows its one line. Inbox, Today, Capture and Projects
// exist only for a folder Ori set up.
export function notesSections({ secondBrain, look, inbox } = {}) {
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
      empty: 'Nothing in Inbox yet.'
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
  const people = Array.isArray(result?.people) ? result.people.slice(0, MAX_PEOPLE_SHOWN) : [];
  sections.push({
    key: 'people',
    title: 'People',
    count: people.length,
    people,
    empty: 'No people found in your notes yet.'
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
const KIND_LABELS = { capture: 'Captured', seed: 'Seeded', user: 'Yours' };

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

function itemRowHTML(item, lookId) {
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
  if (item.due) facts.push('due ' + item.due);
  if (item.acted_as) facts.push(HANDLED_LABELS[item.acted_as] || '');
  return (
    '<li class="ws-notes-item" data-notes-item="' +
    escapeHtml(item.id) +
    '" data-notes-look="' +
    escapeHtml(lookId) +
    '"><div class="ws-notes-item-main"><span class="ws-notes-item-text">' +
    escapeHtml(item.text) +
    '</span><span class="ws-notes-item-meta">' +
    escapeHtml(facts.filter(Boolean).join(' · ')) +
    '</span></div><div class="ws-notes-item-actions">' +
    actions +
    obsidianAnchor(item.link, noteLabel(item.note)) +
    '</div></li>'
  );
}

function loopsHTML(section, panel) {
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
  for (const [title, items] of named) {
    if (!items.length) continue;
    html +=
      '<h5 class="ws-notes-group">' +
      escapeHtml(title) +
      ' <span class="ws-notes-group-count">' +
      items.length +
      '</span></h5><ul class="ws-notes-list">' +
      items.map(item => itemRowHTML(item, lookId)).join('') +
      '</ul>';
  }
  if (!section.count) html += '<p class="ws-notes-empty">' + escapeHtml(section.empty) + '</p>';
  if (groups.handled.length) {
    html +=
      '<details class="ws-notes-handled"' +
      (panel.handledOpen ? ' open' : '') +
      '><summary>' +
      groups.handled.length +
      ' handled</summary><ul class="ws-notes-list">' +
      groups.handled.map(item => itemRowHTML(item, lookId)).join('') +
      '</ul></details>';
  }
  return html;
}

function inboxHTML(section, now) {
  if (section.loading) return '<p class="ws-notes-empty">Reading Inbox…</p>';
  if (!section.notes.length)
    return '<p class="ws-notes-empty">' + escapeHtml(section.empty) + '</p>';
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
        obsidianAnchor(note.link, note.title || note.name) +
        '</div></li>'
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

function todayHTML(section) {
  const today = section.today;
  if (!today) return '<p class="ws-notes-empty">Today\'s note is not available.</p>';
  if (today.state === 'none') {
    return (
      '<p class="ws-notes-line">No note for today yet.</p>' +
      '<a class="ws-notes-bulk" href="/?journal=1" data-notes-journal>Write today\'s Journal</a>'
    );
  }
  const line = today.state === 'ori' ? "Today's note is saved." : "Today's note is yours.";
  return (
    '<p class="ws-notes-line">' +
    escapeHtml(line) +
    ' ' +
    obsidianAnchor(today.link, "today's note") +
    '</p>'
  );
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
          escapeHtml(plural(Number(person.mentions) || 0, 'mention', 'mentions')) +
          '</span></div><div class="ws-notes-item-actions is-always">' +
          obsidianAnchor(person.link, person.name) +
          '</div></li>'
      )
      .join('') +
    '</ul>'
  );
}

function projectsHTML(section) {
  if (!section.projects.length)
    return '<p class="ws-notes-empty">' + escapeHtml(section.empty) + '</p>';
  return (
    '<ul class="ws-notes-list">' +
    section.projects
      .map(
        project =>
          '<li class="ws-notes-item"><div class="ws-notes-item-main"><span class="ws-notes-item-text">' +
          escapeHtml(project.name) +
          '</span></div><div class="ws-notes-item-actions is-always"><a class="ws-notes-action" href="' +
          escapeHtml(secondBrainProjectLink(project.name)) +
          '" aria-label="' +
          escapeHtml(project.action.ariaLabel) +
          '">' +
          escapeHtml(project.action.label) +
          '</a></div></li>'
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
  const counts = result?.counts;
  if (counts) {
    let line =
      plural(Number(counts.notes) || 0, 'note', 'notes') +
      ' · ' +
      plural(Number(counts.open_items) || 0, 'open item', 'open items');
    if (counts.partial) line += ' (partial look)';
    const at = result.finished_at ? sinceLabel(result.finished_at, now) : '';
    if (at) line += ' · last look ' + at;
    lines.push('<p class="ws-notes-muted">' + escapeHtml(line) + '</p>');
  }
  const age = lookAgeLine(result?.finished_at, now);
  if (age) lines.push('<p class="ws-notes-muted">' + escapeHtml(age) + '</p>');
  const message = panel.lookMessage
    ? '<p class="ws-notes-muted" role="status">' + escapeHtml(panel.lookMessage) + '</p>'
    : '';
  const running = look?.state === 'running';
  return (
    lines.join('') +
    message +
    '<button type="button" class="ws-notes-action" data-cmd-modal-action="notes-look-again"' +
    (running ? ' disabled' : '') +
    '>' +
    (running ? 'Reading…' : 'Look again') +
    '</button>'
  );
}

function sectionBodyHTML(section, panel, now) {
  switch (section.key) {
    case 'capture':
      return captureHTML(panel);
    case 'inbox':
      return inboxHTML(section, now);
    case 'today':
      return todayHTML(section);
    case 'loops':
      return loopsHTML(section, panel);
    case 'people':
      return peopleHTML(section);
    case 'projects':
      return projectsHTML(section);
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
export function notesPanelHTML({ secondBrain, panel = {}, now = Date.now() } = {}) {
  const look = panel.look || null;
  const sections = notesSections({ secondBrain, look, inbox: panel.inbox });
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
    '<button type="button" class="ws-notes-action" data-cmd-modal-action="notes-connection">Connection…</button>' +
    '<button type="button" class="ws-cmd-modal-close" data-cmd-modal-action="close" aria-label="Close Notes">×</button>' +
    '</div></header>' +
    (ready ? tabsHTML(tabs, active) : '') +
    '<div class="ws-cmd-modal-body ws-notes-body">' +
    notice +
    body +
    '</div>'
  );
}
