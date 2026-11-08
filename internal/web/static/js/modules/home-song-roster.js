/*
 * home-song-roster.js — the projects-and-assistants section of a program
 * Home's Details (song-card PRD FR15–FR20), titled with the Home's words
 * ("Songs and assistants" under the Music package, "Projects and assistants"
 * without one; PRD #792 FR8).
 *
 * One row per connected project, from
 * `GET /api/workspaces/{home}/assistant-program/library/roster`: the title (a
 * link that opens the project), its keeper's name (text, never a link), its
 * last accepted session and its open work. The server orders the rows by last
 * session, then last save, then name, and sends the Home's `words`. Ten rows
 * show; "Show all (N)" expands in place. Read-only: no chat, hire, pause or
 * move here. Facts are not repeated; the shelf shows them.
 */

import { stageLabel } from './field-labels.js';
import { format as openWorkLine } from './ticket-count-line.js';
import { lowerNoun, projectWords, songFactsLabel } from './library-open.js';
import { safeWorkspaceRoute, untitledText } from './song-card.js';

export const ROSTER_VISIBLE_ROWS = 10;
export const NO_SESSION_TEXT = 'No sessions yet';
export const NO_KEEPER_TEXT = 'No assistant yet';
export const EMPTY_ACTION_TEXT = 'Set up projects from the map’s library badge.';
export const UNSET_CELL_TEXT = '—';
export const NO_STAGE_LABEL = 'Stage not set';
export const NO_NEXT_ACTION_LABEL = 'No next action';
export const NEXT_ACTION_MAX = 80;

/** At most `max` characters, cut on a word with "…" (PRD #792 FR15). */
export function cutOnWord(value, max = NEXT_ACTION_MAX) {
  const line = text(value).replace(/\s+/g, ' ');
  const chars = [...line];
  if (chars.length <= max) return line;
  let cut = chars.slice(0, max - 1).join('');
  const space = cut.lastIndexOf(' ');
  if (space > max / 2) cut = cut.slice(0, space);
  return `${cut.replace(/[\s,;:-]+$/, '')}…`;
}

/** "No songs are linked yet." under the Home's words. */
export function emptyText(words) {
  return `No ${lowerNoun(words, { plural: true })} are linked yet.`;
}

const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];
const text = value => (typeof value === 'string' ? value.trim() : '');

export function rosterURL(homeID) {
  return `/api/workspaces/${encodeURIComponent(String(homeID || ''))}/assistant-program/library/roster`;
}

/**
 * Workspace id → a map tile's facts text, from Home id → roster rows (song-card
 * PRD FR10). Both maps use it: Home's map and a Home's own page map. `words`
 * is Home id → that Home's roster `words`; a Home without them reads neutral.
 */
export function songFactsSnapshot(rosters, words = {}) {
  const snapshot = {};
  for (const [homeID, rows] of Object.entries(rosters || {})) {
    const homeWords = words && typeof words === 'object' ? words[homeID] : null;
    for (const row of Array.isArray(rows) ? rows : []) {
      const id = String(row?.workspace_id || '');
      // The tile shows the declared facts but the track count, and no
      // "varies"; the card and the tooltip keep both. A project with only a
      // track count shows that.
      const facts = row?.facts && typeof row.facts === 'object' ? row.facts : null;
      const label =
        songFactsLabel(facts && { ...facts, track_count: 0, tempo_varies: false }, homeWords) ||
        songFactsLabel(facts, homeWords);
      if (id && label) snapshot[id] = label;
    }
  }
  return snapshot;
}

/** "Nov 5", or "Nov 5, 2025" outside the current year; '' for no date. */
export function sessionDate(isoTime, now = new Date()) {
  if (typeof isoTime !== 'string' || !isoTime) return '';
  const date = new Date(isoTime);
  if (Number.isNaN(date.getTime()) || date.getUTCFullYear() <= 1) return '';
  const day = `${MONTHS[date.getMonth()]} ${date.getDate()}`;
  return date.getFullYear() === now.getFullYear() ? day : `${day}, ${date.getFullYear()}`;
}

function rowView(row, now, words) {
  const session = row?.last_session;
  const sentence = text(session?.first_sentence);
  const date = sessionDate(session?.accepted_at, now);
  const keeper = text(row?.keeper_agent_name);
  const stage = stageLabel(row?.stage);
  const nextAction = cutOnWord(row?.next_action);
  return {
    id: text(row?.workspace_id),
    title: text(row?.title) || untitledText(words),
    // An unset cell is "—" with a label a screen reader says instead.
    stageText: stage || UNSET_CELL_TEXT,
    stageLabel: stage ? '' : NO_STAGE_LABEL,
    nextActionText: nextAction || UNSET_CELL_TEXT,
    nextActionLabel: nextAction ? '' : NO_NEXT_ACTION_LABEL,
    href: safeWorkspaceRoute(row?.workspace_route),
    keeperText: keeper || NO_KEEPER_TEXT,
    hasKeeper: Boolean(keeper),
    sessionText: sentence ? (date ? `${date} — ${sentence}` : sentence) : NO_SESSION_TEXT,
    hasSession: Boolean(sentence),
    // Missing counts are not "No open work": leave the line out instead.
    openWorkText: openWorkLine(row?.open_work_counts)
  };
}

/**
 * The section's view model. `rows` is the server's ordered list; `expanded`
 * shows every row. `sheet` is the library setup sheet's link for the empty
 * line ({ href } or null). `words` is the roster response's `words`.
 */
export function rosterView(
  rows,
  { expanded = false, now = new Date(), sheet = null, words = null } = {}
) {
  const resolvedWords = projectWords(words);
  const all = (Array.isArray(rows) ? rows : []).filter(row => row && text(row.workspace_id));
  const visible = expanded ? all : all.slice(0, ROSTER_VISIBLE_ROWS);
  const view = {
    title: resolvedWords.roster_title,
    words: resolvedWords,
    total: all.length,
    rows: visible.map(row => rowView(row, now, resolvedWords)),
    showAll:
      !expanded && all.length > ROSTER_VISIBLE_ROWS ? { label: `Show all (${all.length})` } : null,
    empty: null
  };
  if (!all.length) {
    const href = sheet && typeof sheet.href === 'string' ? sheet.href : '';
    view.empty = { text: emptyText(resolvedWords), action: { label: EMPTY_ACTION_TEXT, href } };
  }
  return view;
}

function element(doc, tag, className, content) {
  const node = doc.createElement(tag);
  if (className) node.className = className;
  if (content !== undefined) node.textContent = content;
  return node;
}

/**
 * Renders the section into `container`. `onShowAll` is called by the Show
 * all button; `onOpenSheet(event)` runs when the empty line's link is used,
 * so the host can open the sheet in place instead of navigating.
 */
export function renderRoster(container, view, { onShowAll, onOpenSheet } = {}) {
  if (!container) return null;
  const doc = container.ownerDocument || globalThis.document;
  if (!view) {
    container.replaceChildren();
    container.hidden = true;
    return null;
  }
  const section = element(doc, 'section', 'song-roster');
  section.setAttribute('aria-label', view.title);
  const heading = element(doc, 'h3', 'song-roster-title', view.title);
  section.append(heading);

  if (view.empty) {
    const line = element(doc, 'p', 'song-roster-empty');
    line.append(doc.createTextNode(`${view.empty.text} `));
    const action = view.empty.action;
    if (action.href) {
      const link = element(doc, 'a', 'song-roster-empty-link', action.label);
      link.setAttribute('href', action.href);
      if (typeof onOpenSheet === 'function') link.addEventListener('click', onOpenSheet);
      line.append(link);
    } else {
      line.append(doc.createTextNode(action.label));
    }
    section.append(line);
  } else {
    const list = element(doc, 'ul', 'song-roster-list');
    for (const row of view.rows) {
      const item = element(doc, 'li', 'song-roster-row');
      if (row.href) {
        const title = element(doc, 'a', 'song-roster-song', row.title);
        title.setAttribute('href', row.href);
        item.append(title);
      } else {
        item.append(element(doc, 'span', 'song-roster-song', row.title));
      }
      item.append(
        element(
          doc,
          'span',
          `song-roster-keeper${row.hasKeeper ? '' : ' song-roster-muted'}`,
          row.keeperText
        )
      );
      for (const [className, value, label] of [
        ['song-roster-stage', row.stageText, row.stageLabel],
        ['song-roster-next', row.nextActionText, row.nextActionLabel]
      ]) {
        const cell = element(
          doc,
          'span',
          `${className}${label ? ' song-roster-muted' : ''}`,
          value
        );
        if (label) cell.setAttribute('aria-label', label);
        item.append(cell);
      }
      item.append(
        element(
          doc,
          'span',
          `song-roster-session${row.hasSession ? '' : ' song-roster-muted'}`,
          row.sessionText
        )
      );
      if (row.openWorkText) item.append(element(doc, 'span', 'song-roster-work', row.openWorkText));
      list.append(item);
    }
    section.append(list);
    if (view.showAll) {
      const button = element(doc, 'button', 'song-roster-show-all', view.showAll.label);
      button.setAttribute('type', 'button');
      if (typeof onShowAll === 'function') button.addEventListener('click', onShowAll);
      section.append(button);
    }
  }
  container.replaceChildren(section);
  container.hidden = false;
  return section;
}
