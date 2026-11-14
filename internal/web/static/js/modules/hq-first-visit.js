// First-visit presentation only. Progression owns completion; the server owns
// offering/finishing the welcome. No DOM, requests or duplicate mission state.
import { HQ_LABELS as L } from './hq-labels.js';
import { JOURNEY_STEPS } from './home-welcome.js';

export const FIRST_VISIT_KEY = 'ori.hq.firstVisit.v1';
export const FIRST_VISIT_MISSIONS = Object.freeze({
  note: 'pa-capture-first-note',
  brief: 'pa-first-brief',
  folder: 'pa-show-folder',
  notes: 'pa-second-brain'
});

export function firstVisitShowAll(storage, workspaceId) {
  try {
    const value = JSON.parse(storage?.getItem(FIRST_VISIT_KEY) || 'null');
    return Boolean(workspaceId && value?.workspaceId === workspaceId && value.showAll === true);
  } catch (_) {
    return false;
  }
}

export function rememberFirstVisitShowAll(storage, workspaceId, showAll) {
  try {
    if (showAll && workspaceId)
      storage?.setItem(FIRST_VISIT_KEY, JSON.stringify({ workspaceId, showAll: true }));
    else storage?.removeItem(FIRST_VISIT_KEY);
  } catch (_) {
    // Optional browser preference: in-memory presentation still works.
  }
}

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

export function firstVisitTicketTitles(tickets = []) {
  return tickets
    .filter(t => t.context?.template_starter_task && !t.context?.template_setup)
    .map(t => String(t.description || '').trim())
    .filter(Boolean)
    .slice(0, 3);
}

export function firstVisitView({
  state = '',
  missions = null,
  notes = null,
  hasBrief = false,
  showAll = false,
  resolvedOnLoad = false,
  savedText = '',
  tickets = []
} = {}) {
  const loading = !Array.isArray(missions);
  const statuses = Object.fromEntries(
    Object.entries(FIRST_VISIT_MISSIONS).map(([key, id]) => [
      key,
      missionState(missions?.find(m => m.id === id))
    ])
  );
  const steps = [
    {
      key: 'note',
      title: L.firstNoteTitle,
      hint: L.firstNoteHint,
      action: 'save',
      button: L.firstNoteSave
    },
    {
      key: 'brief',
      title: L.firstBriefTitle,
      hint: L.firstBriefHint,
      action: 'brief',
      button: hasBrief ? L.firstBriefTitle : L.firstBriefWrite
    },
    {
      key: 'folder',
      title: L.firstFolderTitle,
      hint: L.firstFolderHint,
      action: 'folder',
      button: L.firstFolderChoose
    }
  ].map(step => ({
    ...step,
    state: statuses[step.key],
    resolved: statuses[step.key] !== 'open',
    disabled: loading
  }));
  const [note, brief, folder] = steps;
  const ownVault = notes?.source && notes.source.origin !== 'created_by_ori';
  const writable = notes?.source?.origin === 'created_by_ori' && notes.status !== 'missing';
  if (note.state === 'completed') {
    note.hint = savedText ? L.firstNoteSaved.replace('{text}', savedText) : L.firstNoteDone;
    note.action = '';
  } else if (note.state === 'skipped') {
    note.hint = ownVault ? L.firstNotesOwnVault : L.firstNotesOff;
    note.action = ownVault ? '' : 'notes';
    note.button = L.firstNotesSetup;
  } else if (!writable) {
    note.title = L.firstNotesSetupTitle;
    note.hint = L.firstNotesSetupHint;
    note.action = 'notes';
    note.button = L.firstNotesSetup;
    note.disabled ||= !notes;
  }
  if (brief.state === 'completed') {
    brief.hint = L.firstBriefDone;
    brief.button = L.firstBriefOpen;
  }
  if (folder.state === 'completed') {
    folder.hint = L.firstFolderDone;
    folder.button = L.firstFolderOpen;
    folder.action = 'home';
  }
  const count = steps.filter(s => s.resolved).length;
  const allResolved = !loading && count === 3;
  const card =
    state !== 'pending'
      ? 'none'
      : allResolved
        ? resolvedOnLoad
          ? 'none'
          : 'finished'
        : showAll
          ? 'bar'
          : 'full';
  const guided = card === 'full';
  const ticketTitles = firstVisitTicketTitles(tickets);
  const titles =
    ticketTitles.length > 1
      ? `${ticketTitles.slice(0, -1).join(', ')} and ${ticketTitles.at(-1)}`
      : ticketTitles[0] || '';
  return {
    card,
    steps,
    count,
    loading,
    allResolved,
    finishSilently: state === 'pending' && allResolved && resolvedOnLoad,
    journey: JOURNEY_STEPS,
    progress: L.firstVisitProgress.replace('{count}', count),
    barProgress: L.firstVisitBarProgress.replace('{count}', count),
    ticketTitles,
    footer: ticketTitles.length
      ? (ticketTitles.length === 1 ? L.firstVisitTicket : L.firstVisitTickets)
          .replace('{count}', ticketTitles.length === 2 ? 'two' : 'three')
          .replace('{titles}', titles)
      : '',
    layout: {
      guided,
      notes: !guided || note.state === 'completed',
      hideEmptyNeeds: guided,
      railAllUnderMore: guided,
      foldedStations: !guided,
      // null means the quiet layout's normal station filtering.
      stationKeys: guided
        ? [brief.resolved && 'daily-brief', folder.resolved && 'watchtower'].filter(Boolean)
        : null,
      hideNotesChecklist: card === 'full' || card === 'bar'
    }
  };
}
