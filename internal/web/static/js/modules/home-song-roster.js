/*
 * home-song-roster.js — the "Songs and assistants" section of a program
 * Home's Details (song-card PRD FR15–FR20).
 *
 * One row per connected song, from
 * `GET /api/workspaces/{home}/assistant-program/library/roster`: the song
 * title (a link that opens the song), its keeper's name (text, never a link),
 * its last accepted session and its open work. The server orders the rows by
 * last session, then last save, then name. Ten rows show; "Show all (N)"
 * expands in place. Read-only: no chat, hire, pause or move here. Facts are
 * not repeated; the shelf shows them.
 */

import { format as openWorkLine } from './ticket-count-line.js';
import { safeWorkspaceRoute } from './song-card.js';

export const ROSTER_VISIBLE_ROWS = 10;
export const ROSTER_TITLE = 'Songs and assistants';
export const NO_SESSION_TEXT = 'No sessions yet';
export const NO_KEEPER_TEXT = 'No assistant yet';
export const EMPTY_TEXT = 'No songs are linked yet.';
export const EMPTY_ACTION_TEXT = 'Set up projects from the map’s library badge.';

const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];
const text = value => (typeof value === 'string' ? value.trim() : '');

export function rosterURL(homeID) {
  return `/api/workspaces/${encodeURIComponent(String(homeID || ''))}/assistant-program/library/roster`;
}

/** "Nov 5", or "Nov 5, 2025" outside the current year; '' for no date. */
export function sessionDate(isoTime, now = new Date()) {
  if (typeof isoTime !== 'string' || !isoTime) return '';
  const date = new Date(isoTime);
  if (Number.isNaN(date.getTime()) || date.getUTCFullYear() <= 1) return '';
  const day = `${MONTHS[date.getMonth()]} ${date.getDate()}`;
  return date.getFullYear() === now.getFullYear() ? day : `${day}, ${date.getFullYear()}`;
}

function rowView(row, now) {
  const session = row?.last_session;
  const sentence = text(session?.first_sentence);
  const date = sessionDate(session?.accepted_at, now);
  const keeper = text(row?.keeper_agent_name);
  return {
    id: text(row?.workspace_id),
    title: text(row?.title) || 'Untitled song',
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
 * line ({ href } or null).
 */
export function rosterView(rows, { expanded = false, now = new Date(), sheet = null } = {}) {
  const all = (Array.isArray(rows) ? rows : []).filter(row => row && text(row.workspace_id));
  const visible = expanded ? all : all.slice(0, ROSTER_VISIBLE_ROWS);
  const view = {
    title: ROSTER_TITLE,
    total: all.length,
    rows: visible.map(row => rowView(row, now)),
    showAll:
      !expanded && all.length > ROSTER_VISIBLE_ROWS ? { label: `Show all (${all.length})` } : null,
    empty: null
  };
  if (!all.length) {
    const href = sheet && typeof sheet.href === 'string' ? sheet.href : '';
    view.empty = { text: EMPTY_TEXT, action: { label: EMPTY_ACTION_TEXT, href } };
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
