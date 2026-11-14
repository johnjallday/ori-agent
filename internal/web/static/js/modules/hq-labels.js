/*
 * hq-labels.js — the plain names the Personal HQ Details page uses.
 *
 * Everything the HQ branch of workspace-command.js renders, in visible text or
 * in an aria-label, takes its words from here. The rest of the Command view
 * still speaks its tactical vocabulary (Outpost, Commander, Garrison, ...);
 * keeping the HQ words in one map means that vocabulary can later be offered
 * back as an optional skin without hunting through the renderer.
 *
 * Pure: no DOM. The work words come from work-labels.js, the one vocabulary
 * every branch of the Command view shares.
 */

import { WORK_LABELS } from './work-labels.js';

export const HQ_LABELS = Object.freeze({
  // People
  lead: 'Lead',
  team: 'Team',
  // The product calls work a ticket, never a task (PRD 760 FR-3), and the
  // agent's action is the existing "Give work" flow; the keys keep the PRD's
  // working names.
  giveTask: WORK_LABELS.giveWork,

  // Work
  tasks: WORK_LABELS.tickets,
  openTasks: WORK_LABELS.openTickets,
  backlog: WORK_LABELS.backlog,
  workspaceNotes: 'Workspace notes',
  sessions: 'Sessions',
  files: 'Files',

  // Page structure
  yourHQ: 'Your HQ',
  setup: 'Setup',
  stations: 'Stations',
  needsYou: 'Needs you',
  ticket: WORK_LABELS.ticket,
  anAgent: 'An agent',
  answer: 'Answer',
  review: 'Review',
  ticketWaiting: '{agent} is waiting on you{title}.',
  ticketReadyForReview: 'Ready for review: {title}.',
  followUps: 'Follow-ups',
  notes: 'Notes',
  goal: 'Goal',
  portability: 'Portability',
  moreViews: 'More views',
  showAll: 'Show all',
  nothingNeedsYou: 'Nothing needs you right now.',

  // Setup rows
  linkedFolders: 'Linked folders',
  tools: 'Tools',
  capabilities: 'Capabilities',
  triggers: 'Triggers and automation',

  // The Map (PRD 767): the Flow arrangement's lanes and nodes, the
  // arrangement switch, and the belt's plain names.
  mapArrangement: 'Map arrangement',
  flow: 'Flow',
  free: 'Free',
  whatOriReads: 'What Ori reads',
  yourLeadAgent: 'Your lead agent',
  whatNeedsYou: 'What needs you',
  email: 'Email',
  calendar: 'Calendar',
  memory: 'Memory',
  otherWorkspaces: 'Other workspaces',
  todaysBrief: "Today's brief",
  waitingOnYou: 'Waiting on you',
  specialists: 'Specialists',
  noSpecialists: 'No specialists yet',
  workspaceGoal: 'Workspace goal',
  inventory: 'Inventory',
  // "New task" in the PRD; work is a ticket on the HQ page.
  newTask: WORK_LABELS.newTicket,

  // First visit. Interpolation stays plain text until the renderer escapes it.
  firstVisitTitle: 'Welcome to your HQ.',
  firstVisitLead:
    'This is where I keep your notes, write your Daily Brief and tell you what needs you. Three things make it yours.',
  firstVisitProgress: '{count} of 3',
  firstVisitBarProgress: '{count} of 3 done',
  firstNoteTitle: 'Save a first note',
  firstNoteHint: 'An idea, a reminder, anything. It lands in your Notes folder.',
  firstNoteField: 'Your first note',
  firstNoteSave: 'Save note',
  firstNoteSaved: 'Saved to your Inbox: “{text}”',
  firstNoteDone: 'Your first note is in your Inbox.',
  firstNotesSetupTitle: 'Set up your notes',
  firstNotesSetupHint: 'Ori keeps what it learns in plain Markdown you own.',
  firstNotesSetup: 'Set up notes',
  firstNotesOff: 'Notes are off. Set them up any time.',
  firstNotesOwnVault: 'Your own vault is connected. Ori does not write there.',
  firstBriefTitle: 'Read your first brief',
  firstBriefHint: 'A short overview of your day across your workspaces.',
  firstBriefWrite: 'Write my first brief',
  firstBriefDone: 'Your first brief is ready.',
  firstBriefOpen: 'Open it',
  firstFolderTitle: 'Show me a folder',
  firstFolderHint:
    'Pick a folder you work in. I look through it and suggest how to set it up. This continues on Home.',
  firstFolderChoose: 'Choose a folder',
  firstFolderDone: 'Your folder is on the map.',
  firstFolderOpen: 'Open Home',
  firstVisitTickets: 'I also added {count} tickets you can run when you like: {titles}.',
  firstVisitTicket: 'I also added a ticket you can run when you like: {titles}.',
  firstVisitSeeTickets: 'See them in Tickets',
  firstVisitShowAll: 'Show everything',
  firstVisitContinue: 'Continue',
  firstVisitHide: 'Hide',
  firstVisitFinishedTitle: 'Your HQ is yours now.',
  firstVisitFinishedText: 'Everything here is set up. Anything that needs you shows up first.',
  firstVisitDone: 'Done',
  firstVisitLoading: 'Loading your first visit…',
  firstVisitLoadError: 'Your progress could not be loaded. Try again.',
  firstVisitRetry: 'Try again',
  firstVisitSaving: 'Saving your note…',
  firstNoteEmpty: 'Write something to save, or go back.',
  firstNoteChanged:
    'Your Notes connection changed or is not writable. Your draft is still here; check Notes before saving.',
  firstNoteUncertain:
    'Ori could not confirm the note was saved. Save again: the same note is never saved twice.',
  firstNoteError: 'Ori could not save the note. Your text is still here.',
  firstNoteReceiptError:
    'Ori could not confirm the saved note. Retry the same save; your draft is still here.',
  firstVisitFinishError: 'Your first visit could not be finished. Try again.',
  more: 'More',
  moreCount: 'and {count} more',
  stationsUnused: 'Not in use yet',
  stationsUnusedHint: 'Set them up when you want them.',

  // Station state words
  notSetUp: 'Not set up',
  ready: 'Ready',
  running: 'Running',
  error: 'Error'
});

/**
 * The tactical words the HQ page must never show. The tests and the a11y spec
 * assert against this list, so it is the one place to extend.
 */
export const TACTICAL_TERMS = Object.freeze([
  'Outpost',
  'Commander',
  'Garrison',
  'Quest Board',
  'Loadout',
  'Keeper'
]);

/**
 * The header's one-word portability state, from WorkspaceContinuity's
 * describeStatus view. Never a timestamp: the full status stays in the rail's
 * Portability row (PRD 766 FR5). '' while the status is unknown.
 */
export function portabilityWord(view) {
  if (!view || !view.state) return '';
  if (view.imported && !view.backgroundAllowed) return 'Imported · routines off';
  switch (view.state) {
    case 'ready':
      return view.imported ? 'Imported · ready to move' : 'Ready to move';
    case 'preparing':
      return 'Preparing';
    case 'update_needed':
      return 'Saving latest work';
    default:
      return 'Not ready to move';
  }
}

/** The plain label for `key`, or `fallback` (default: the key) when unknown. */
export function hqLabel(key, fallback) {
  if (Object.prototype.hasOwnProperty.call(HQ_LABELS, key)) return HQ_LABELS[key];
  return fallback === undefined ? String(key) : fallback;
}

/** The tactical terms that appear in `text`, case-insensitively; [] when clean. */
export function tacticalTermsIn(text) {
  const haystack = String(text || '').toLowerCase();
  return TACTICAL_TERMS.filter(term => haystack.includes(term.toLowerCase()));
}
