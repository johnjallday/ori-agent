// First-visit presentation only. Progression owns completion; the server owns
// eligibility/dismissal. Opening the suggestions is ephemeral, never stored.
import { HQ_LABELS as L } from './hq-labels.js';
import { STATE_WORDS } from './hq-stations-grid.js';

export const FIRST_VISIT_MISSIONS = Object.freeze({
  note: 'pa-capture-first-note',
  brief: 'pa-first-brief',
  folder: 'pa-show-folder',
  notes: 'pa-second-brain'
});

function missionState(mission) {
  if (mission?.status === 'completed' || mission?.completed_at) return 'completed';
  if (mission?.status === 'skipped' || mission?.skipped_at) return 'skipped';
  return 'open';
}

export function firstVisitResolved(missions) {
  return ['note', 'brief', 'folder'].every(key =>
    (missions || []).some(m => m.id === FIRST_VISIT_MISSIONS[key] && missionState(m) !== 'open')
  );
}

/**
 * Which Notes the HQ has, from the same source the first visit reads:
 * 'created' (a folder Ori set up and can write), 'own' (the person's own vault,
 * never written) or 'unset' (nothing usable yet). Tips and the suggestions both
 * ask this, so they cannot disagree about which Notes the person has.
 */
export function notesKind(notes) {
  if (notes?.source && notes.source.origin !== 'created_by_ori') return 'own';
  if (notes?.source?.origin === 'created_by_ori' && notes.status !== 'missing') return 'created';
  return 'unset';
}

// The station states a card shows when its part is up: running, not off.
const RUNNING_WORDS = new Set([STATE_WORDS.ready, STATE_WORDS.running, STATE_WORDS.needsYou]);

/**
 * What the assistant's greeting in the drawer says, from the same state the
 * suggestions read. Pure: the page passes what it draws.
 *
 * - `{ show: false }`: nothing to greet (the visit is not pending, its progress
 *   could not be read, or all three suggestions are resolved).
 * - `null`: not known yet (the missions or the Notes have not been read).
 * - `{ show: true, running, off, steps }`: the parts that are up and the parts
 *   that are off, as the page names them and in page order (Notes sits above the
 *   stations), and the three suggestions with their existing titles and hints.
 *   A card still being checked, and one in error, are in neither list.
 *
 * @param {object} input
 * @param {string} input.state first_visit_state from the server
 * @param {object[]|null} input.missions the progression missions, null until read
 * @param {object|null} input.notes the Notes source the first visit reads
 * @param {boolean} [input.resolvedOnLoad]
 * @param {boolean} [input.failed] the progress could not be read
 * @param {{label: string, stateWord: string}[]} [input.cards] the station cards drawn
 * @param {{label: string}[]} [input.folded] the stations folded under "Not in use yet"
 */
export function hqGreetingView({
  state = '',
  missions = null,
  notes = null,
  resolvedOnLoad = false,
  failed = false,
  cards = [],
  folded = []
} = {}) {
  if (state !== 'pending' || failed) return { show: false };
  if (!Array.isArray(missions) || !notes) return null;
  const view = firstVisitView({ state, missions, notes, resolvedOnLoad });
  if (view.allResolved) return { show: false };
  const kind = notesKind(notes);
  const running = kind === 'unset' ? [] : [L.notes];
  const off = kind === 'unset' ? [L.notes] : [];
  for (const card of Array.isArray(cards) ? cards : []) {
    if (RUNNING_WORDS.has(card.stateWord)) running.push(card.label);
  }
  for (const card of Array.isArray(folded) ? folded : []) off.push(card.label);
  return { show: true, running, off, steps: view.steps };
}

export function firstVisitView({
  state = '',
  missions = null,
  notes = null,
  resolvedOnLoad = false
} = {}) {
  const loading = !Array.isArray(missions);
  const steps = [
    { key: 'folder', title: L.firstFolderDocuments, hint: L.firstFolderHint, action: 'folder' },
    { key: 'brief', title: L.firstBriefTitle, hint: L.firstBriefHint, action: 'brief' },
    { key: 'note', title: L.firstNoteTitle, hint: L.firstNoteHint, action: 'note' }
  ].map(step => {
    const status = missionState(missions?.find(m => m.id === FIRST_VISIT_MISSIONS[step.key]));
    return { ...step, state: status, resolved: status !== 'open', disabled: loading };
  });
  const [folder, brief, note] = steps;
  const kind = notesKind(notes);
  const ownVault = kind === 'own';
  const writable = kind === 'created';
  if (!writable) {
    note.title = ownVault ? L.notes : L.firstNotesSetupTitle;
    note.hint = ownVault ? L.firstNotesOwnVault : L.firstNotesSetupHint;
    note.action = 'notes';
    note.disabled ||= !notes;
  } else if (note.state === 'completed') note.hint = L.firstNoteDone;
  if (brief.state === 'completed') brief.hint = L.firstBriefDone;
  if (folder.state === 'completed') {
    folder.title = L.firstFolderTitle;
    folder.hint = L.firstFolderDone;
    folder.action = 'home';
  }
  const count = steps.filter(s => s.resolved).length;
  const allResolved = !loading && count === 3;
  const finishSilently = state === 'pending' && allResolved && resolvedOnLoad;
  return {
    card: state !== 'pending' || finishSilently ? 'none' : allResolved ? 'finished' : 'full',
    steps,
    count,
    loading,
    allResolved,
    finishSilently,
    layout: {
      guided: false,
      notes: true,
      hideEmptyNeeds: false,
      railAllUnderMore: false,
      foldedStations: true,
      stationKeys: null,
      hideNotesChecklist: state === 'pending'
    }
  };
}
