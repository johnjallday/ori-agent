/*
 * work-labels.js — the one set of words the UI uses for a unit of work.
 *
 * A unit of work an agent does for you is a **ticket**. "Backlog" is where a
 * ticket waits (a lifecycle state, `workspace.TaskStatusBacklog`), not a second
 * name for the item. Every surface that names a work item — the Map belt and
 * windows, ticket rows, agent sheets, toasts — takes its words from here, so
 * changing a label is a one-line edit (PRD one-vocabulary FR1).
 *
 * The code may keep saying "task" (types, routes, JSON fields, element ids);
 * only what a person reads changes.
 *
 * Pure: no DOM, no imports.
 */

export const WORK_LABELS = Object.freeze({
  // Nouns
  ticket: 'Ticket',
  tickets: 'Tickets',
  newTicket: 'New ticket',
  openTickets: 'Open tickets',
  activeTickets: 'Active tickets',
  recentTickets: 'Recent tickets',
  currentTicket: 'Current ticket',
  noOpenTickets: 'No open tickets',
  nothingAssigned: 'Nothing assigned yet.',

  // The Backlog state and its list
  backlog: 'Backlog',
  backlogSubtitle: 'Saved for later',
  backlogEmpty: 'Backlog clear',

  // Verbs on a ticket
  start: 'Start',
  starting: 'Starting…',
  open: 'Open',
  resolve: 'Resolve',
  track: 'Track',
  giveWork: 'Give work',
  assignFirstTicket: 'Assign a first ticket',

  // Runs stay runs: one execution of a ticket
  runs: 'Runs',
  runHistory: 'Run history',

  // Toasts and errors
  createFailed: 'Could not create the ticket',
  startFailed: 'Could not start the ticket.',
  noLongerPending: 'That ticket is no longer pending.',
  created: 'Ticket created',
  started: 'Ticket started'
});

/**
 * The accessible name of the Backlog window's per-item button: the visible
 * "Start" plus what it does, so a screen reader hears the real action.
 */
export function startFromBacklogLabel(title) {
  return WORK_LABELS.start + ' — move ' + String(title || '') + ' into work';
}

/**
 * Words a work item is never called in visible copy. The guard test renders
 * the work surfaces with fixture data and asserts none of these appear in
 * text, aria-label, title or placeholder. "Task"/"Tasks" are checked as whole
 * words on rendered output only, so identifiers and comments stay free.
 *
 * Setup-checklist framing ("Mission 01", "Quests" as the checklist's name) is
 * not a work item and is deliberately not rendered by the guard (decision 3).
 *
 * The terms are written as regular-expression literals and read back through
 * `.source`: scripts/check-user-facing-task-copy.mjs treats a string literal
 * in a browser script as display copy, and these are the banned words, not copy.
 */
export const RETIRED_WORK_TERMS = Object.freeze(
  [
    /Quest Board/,
    /Accept Quest/,
    /Start Quest/,
    /Resolve Quest/,
    /Track Quest/,
    /Open Quest/,
    /Quest/,
    /Objectives/,
    /Objective/,
    /All Tasks/,
    /Session Tickets/,
    /Tasks/,
    /Task/
  ].map(term => term.source)
);

/**
 * The retired terms that appear in `text` as whole words, case-insensitively;
 * [] when clean. Whole-word matching keeps "Taskbar"-style words and the
 * "quest-" CSS class fragments of raw HTML out of the result.
 */
export function retiredWorkTermsIn(text) {
  const haystack = String(text || '');
  return RETIRED_WORK_TERMS.filter(term => {
    const escaped = term.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
    return new RegExp('(^|[^A-Za-z-])' + escaped + '(?![A-Za-z-])', 'i').test(haystack);
  });
}
