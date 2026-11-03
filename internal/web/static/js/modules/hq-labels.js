/*
 * hq-labels.js — the plain names the Personal HQ Details page uses.
 *
 * Everything the HQ branch of workspace-command.js renders, in visible text or
 * in an aria-label, takes its words from here. The rest of the Command view
 * still speaks its tactical vocabulary (Outpost, Commander, Garrison, ...);
 * keeping the HQ words in one map means that vocabulary can later be offered
 * back as an optional skin without hunting through the renderer.
 *
 * Pure: no DOM, no imports.
 */

export const HQ_LABELS = Object.freeze({
  // People
  lead: 'Lead',
  team: 'Team',
  // The product calls work a ticket, never a task (PRD 760 FR-3), and the
  // agent's action is the existing "Give work" flow; the keys keep the PRD's
  // working names.
  giveTask: 'Give work',

  // Work
  tasks: 'Tickets',
  openTasks: 'Open tickets',
  backlog: 'Backlog',
  workspaceNotes: 'Workspace notes',
  sessions: 'Sessions',
  files: 'Files',

  // Page structure
  yourHQ: 'Your HQ',
  setup: 'Setup',
  stations: 'Stations',
  needsYou: 'Needs you',
  needsYourAttention: 'Needs your attention',
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
