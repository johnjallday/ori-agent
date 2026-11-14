// The assistant's one-confirmation HQ proposal in Today. All durable effects
// reuse the HQ form's request identity, root confirmation, and PAF payload.
import {
  chooseHQWorkspaceRoot,
  defaultHQTimezone,
  hqBuildTarget,
  submitReviewedHQ,
  checkHQSetup,
  hasPendingHQRequest,
  currentHQIntent,
  hqWorkspaceRootView,
  loadHQWorkspaceRoot,
  saveHQWorkspaceRoot,
  skipHQObjective
} from './personal-hq-onboarding.js';
import { JUST_HIRED_FLAG } from './personal-assistant-hire.js';
import { FOLDER_PATH_ENDPOINT, folderDialogOffNote } from './folder-path-entry.js';
import {
  forgetFirstAction,
  hqFirstActionMatches,
  hqFirstActionView,
  hqNotesOutcome,
  hqLiveNotesOutcome,
  hqNotesPayload,
  hqNotesReviewView,
  readFirstAction,
  rememberFirstAction
} from './personal-hq-notes-review.js';

import { hqPresentation } from './personal-hq-reveal.js';

const NOTES_ENDPOINT = '/api/personal-assistant/second-brain';

export function hqCardView(relationship, rootState, plan = {}) {
  const state = String(relationship?.state || '');
  const receipt = Array.isArray(plan.receipt) && plan.receipt.length > 0;
  const visible = ['needs_hq', 'provisioning_hq'].includes(state) || receipt;
  const root = hqWorkspaceRootView(rootState);
  const paused = state === 'provisioning_hq';
  const building = paused && !!plan.busy;
  const collapsed = plan.collapsed === true && !receipt;
  return {
    visible,
    receipt,
    collapsed,
    paused,
    building,
    name: String(relationship?.display_name || '').trim() || 'Your assistant',
    root,
    canBuild: visible && !receipt && !collapsed && !building && (paused || root.confirmed),
    status: building
      ? 'Resuming setup…'
      : paused
        ? 'Your confirmed setup is paused. Resume to continue with the settings you already confirmed.'
        : !root.confirmed && !collapsed && !receipt
          ? root.status
          : plan.failed
            ? 'The build did not finish. Retry the same request.'
            : ''
  };
}

// The headline while the HQ is unbuilt: the HQ is explained in folder terms
// first, since every folder the assistant is shown is remembered there, and the
// Daily Brief is where it lands second.
export function hqHeadline(name) {
  const who = String(name || '').trim() || 'Your assistant';
  return `A home base for ${who}`;
}

export function hqReceiptRows(status, plan, directory) {
  const workspace = status?.workspace;
  if (!status?.valid || !workspace) return [];
  const slug = String(workspace.folder_slug || '').trim();
  return [
    {
      kind: 'workspace',
      name: String(workspace.name || plan.name || 'My HQ'),
      detail: '',
      route: slug ? `/workspaces/${encodeURIComponent(slug)}` : ''
    },
    ...(plan.time
      ? [
          {
            kind: 'schedule',
            name: `Daily Brief at ${plan.time}${plan.schedule_days?.length ? ` · ${plan.schedule_days.join(', ')}` : ''}${plan.timezone ? ` · ${plan.timezone}` : ''}`,
            detail: ''
          }
        ]
      : []),
    ...(plan.scope
      ? [
          {
            kind: 'scope',
            name: `${hqReviewSummary(plan).scope} ${hqReviewSummary(plan).notification}`,
            detail: ''
          }
        ]
      : []),
    ...(directory ? [{ kind: 'directory', name: String(directory), detail: '' }] : [])
  ];
}

export function hqReviewDraft(values = {}) {
  return {
    name: values.name ?? 'My HQ',
    timezone: values.timezone ?? '',
    schedule_days: [...(values.schedule_days ?? ['mon', 'tue', 'wed', 'thu', 'fri'])],
    schedule_time: values.schedule_time ?? '08:00',
    scope: values.scope ?? 'all',
    include_future_workspaces: values.include_future_workspaces ?? true,
    notify_on_ready: values.notify_on_ready ?? false
  };
}

export function hqReviewSummary(draft) {
  return {
    schedule: `${(draft.schedule_days || []).join(', ') || 'No days selected'} · ${draft.schedule_time || draft.time || ''} · ${draft.timezone || ''}`,
    scope: `${draft.scope === 'all' ? 'All eligible workspaces' : 'Selected workspaces (choose in HQ settings)'}${draft.include_future_workspaces ? ', including future ones' : '; future workspaces are not included'}.`,
    notification: draft.notify_on_ready
      ? 'Notify in Action Center when ready.'
      : 'No brief notifications.'
  };
}

const plan = {
  ...hqReviewDraft(),
  time: '08:00',
  collapsed: false,
  reviewOpened: false,
  failed: false,
  receipt: [],
  receiptWorkspaceID: '',
  // The notes choice in this review: included by default, or a folder chosen
  // through the trusted picker. Reset when the review is rebuilt.
  notesInclude: true,
  notesChosen: null,
  // The notes half of the build just made in this tab, or of a recorded
  // setup that has not finished; null when there is nothing to say.
  notesOutcome: null,
  justBuilt: false,
  // The first step after a build (4.1): active while this browser's
  // first-action record names the current HQ. `firstPhase` is 'choices',
  // 'note' (the quick-note composer) or 'saved' (the receipt of a saved note).
  firstAction: false,
  firstPhase: 'choices',
  saved: null,
  noteError: '',
  compact: false,
  compactYielded: false,
  firstActionEnded: false
};

// The quick note's draft and its one request id, kept for this tab so Back,
// a failure or a reload never loses the text, and a retry after a lost
// response replays the same save instead of writing a second note.
const NOTE_DRAFT_KEY = 'ori.hq.firstNote.v1';
const COMPACT_RESULT_KEY = 'ori.hq.compactResult.v1';
let memoryNoteDraft = null;
function compactRecord(value) {
  try {
    if (value !== undefined) {
      if (value) window.sessionStorage.setItem(COMPACT_RESULT_KEY, value);
      else window.sessionStorage.removeItem(COMPACT_RESULT_KEY);
    }
    return window.sessionStorage.getItem(COMPACT_RESULT_KEY) || '';
  } catch (_) {
    return '';
  }
}

function newNoteRequestID() {
  const random = Math.random().toString(36).slice(2, 10);
  return `hq-note-${Date.now().toString(36)}-${random}`;
}

function readNoteDraft() {
  if (memoryNoteDraft) return { ...memoryNoteDraft };
  try {
    const parsed = JSON.parse(window.sessionStorage.getItem(NOTE_DRAFT_KEY) || 'null');
    if (parsed && typeof parsed.text === 'string' && typeof parsed.requestId === 'string') {
      memoryNoteDraft = parsed;
      return { ...parsed };
    }
  } catch (_) {
    // A draft is a convenience; a fresh one is fine.
  }
  memoryNoteDraft = { text: '', requestId: newNoteRequestID() };
  return { ...memoryNoteDraft };
}

function writeNoteDraft(draft) {
  memoryNoteDraft = { ...draft };
  try {
    window.sessionStorage.setItem(NOTE_DRAFT_KEY, JSON.stringify(draft));
  } catch (_) {
    // Storage is optional.
  }
}

function firstActionStorage() {
  try {
    return window.localStorage;
  } catch (_) {
    return null;
  }
}
const state = {
  relationship: null,
  root: null,
  notes: null,
  busy: false,
  sequence: 0,
  justHired: false,
  pendingRoot: null
};

function notesView() {
  return hqNotesReviewView(state.notes, {
    include: plan.notesInclude,
    chosen: plan.notesChosen
  });
}

async function readNotesStatus() {
  try {
    const response = await fetch(NOTES_ENDPOINT, { headers: { Accept: 'application/json' } });
    if (!response.ok) return null;
    const body = await response.json();
    return body?.second_brain || null;
  } catch (_) {
    return null;
  }
}

function renderNotes(view) {
  const section = el('Notes');
  if (!section) return;
  const show = view.visible && !view.collapsed && !view.receipt && !view.paused;
  section.hidden = !show;
  if (show) {
    const notes = notesView();
    const create = notes.mode === 'create';
    // The heading promises notes only when this build will make them.
    el('NotesTitle').textContent = create && notes.include ? 'Notes, ready to use' : 'Notes';
    el('NotesMessage').textContent = notes.message || '';
    el('NotesMessage').hidden = !notes.message;
    el('NotesLocation').hidden = !notes.path || (create && !notes.include);
    el('NotesPath').textContent = notes.path;
    el('NotesScope').textContent =
      create && notes.include ? `${notes.scope} ${notes.seed}`.trim() : '';
    el('NotesScope').hidden = !(create && notes.include);
    el('NotesOptions').hidden = !create;
    el('NotesInclude').checked = create && notes.include;
    el('NotesInclude').disabled = state.busy;
    el('NotesChoose').disabled = state.busy || !notes.include;
  }
  const result = el('NotesResult');
  if (!result) return;
  const outcome = plan.notesOutcome;
  result.hidden =
    !outcome || !outcome.line || !view.receipt || (plan.compact && outcome.status === 'ready');
  if (!result.hidden) {
    el('NotesResultText').textContent = outcome.line;
    el('NotesRetry').hidden = !outcome.canRetry;
    el('NotesRetry').disabled = state.busy;
  }
}

function el(id) {
  return document.getElementById(`personalAssistantHQ${id}`);
}

// Today lifts the card above its routine rows while the result is active, and
// folds them; the notes card keeps its ask out of the way meanwhile. Sent only
// when the answer changes, so status refreshes never move anything twice.
let lastResultActive = null;
function announceResult(active) {
  if (lastResultActive === active) return;
  lastResultActive = active;
  document.dispatchEvent(
    new CustomEvent('personal-assistant:setup-result', { detail: { active } })
  );
  // Now leading (Home has just moved it up): show it from its top, not at the
  // drawer's restored scroll offset. No focus is taken by a load.
  if (active && !currentHQIntent()) el('Card')?.scrollIntoView?.({ block: 'start' });
}

function hqRoute() {
  return plan.receipt.find(row => row.kind === 'workspace')?.route || '';
}

function renderFirst(view, resultActive) {
  const first = el('First');
  if (!first) return;
  first.hidden = !resultActive || !view.receipt || !plan.firstAction;
  if (first.hidden) return;
  const choicesView = hqFirstActionView(plan.notesOutcome, { hqRoute: hqRoute() });
  const phase = plan.firstPhase;
  const choosing = phase === 'choices';
  el('FirstChoices').hidden = !choosing;
  // Keep the same real link/handler, alongside Back while editing so a phone
  // keyboard does not spend an extra row on navigation above the field.
  const explore = el('FirstExplore');
  if (phase === 'note') {
    if (explore.parentNode !== el('NoteActions')) el('NoteActions').append(explore);
  } else if (explore.parentNode !== first) first.insertBefore(explore, el('NoteForm'));
  el('FirstLead').textContent = choicesView.lead;
  el('FirstLead').hidden = !choosing;
  el('FirstNote').hidden = !choosing || !choicesView.note;
  el('FirstOpenNotes').hidden = !choosing || !choicesView.openNotes;
  if (choicesView.openNotes) el('FirstOpenNotes').href = choicesView.openNotes;
  el('FirstSetupNotes').hidden = !choosing || !choicesView.setupNotes;
  el('FirstFolder').hidden = true; // Folder setup remains in its existing entry points.
  el('FirstExplore').hidden = !hqRoute();
  el('FirstExplore').href = hqRoute() || '#';
  el('FirstExplore').setAttribute('aria-disabled', String(state.busy));
  for (const id of ['FirstNote', 'FirstSetupNotes', 'FirstFolder', 'FirstExplore']) {
    el(id).disabled = state.busy;
  }
  el('NoteForm').hidden = phase !== 'note';
  el('NoteError').textContent = plan.noteError;
  el('NoteError').hidden = !plan.noteError;
  const draft = readNoteDraft();
  el('NoteSave').disabled =
    state.busy ||
    !plan.notesOutcome?.writable ||
    Boolean(draft.sourceId && draft.sourceId !== state.notes?.source?.id);
  // An uncertain save must replay the original payload, not edited text under
  // an already-used request ID. Back/reload retain that exact pending note.
  el('NoteText').readOnly = state.busy || Boolean(draft.pendingText);
  el('NoteSave').setAttribute('aria-busy', state.busy && phase === 'note' ? 'true' : 'false');
  el('NoteBack').disabled = state.busy;
  el('NoteSaved').hidden = phase !== 'saved' || !plan.saved;
  if (phase === 'saved' && plan.saved) {
    el('NoteSavedTitle').textContent = `Saved to Inbox · ${plan.saved.note}`;
    el('NoteSavedText').textContent = plan.saved.text;
  }
}

// Ends the first step. The HQ and notes stay exactly as they are; only this
// browser's presentation record goes, so the card does not come back.
function endFirstAction({ focus = true } = {}) {
  const wasCompact = plan.compact;
  plan.firstActionEnded = true;
  compactRecord('');
  forgetFirstAction(firstActionStorage());
  plan.firstAction = false;
  plan.firstPhase = 'choices';
  plan.justBuilt = false;
  if (wasCompact) hqPresentation()?.yield(currentHQIntent());
  render();
  if (!focus) return;
  if (wasCompact) {
    document.querySelector('[data-ws-map-viewport]')?.focus?.({ preventScroll: true });
    return;
  }
  // The pressed control is gone with the card; hand focus to what leads now:
  // Today's strip, else the composer.
  const strip = document.getElementById('personalAssistantTodayStripToggle');
  const next =
    strip && strip.offsetParent !== null
      ? strip
      : document.getElementById('personalAssistantInput');
  next?.focus?.({ preventScroll: true });
}

async function openNoteComposer() {
  const wasCompact = plan.compact;
  state.notes = await readNotesStatus();
  plan.notesOutcome = hqLiveNotesOutcome(state.notes);
  if (!plan.firstAction || (wasCompact && !plan.compact)) return;
  if (!plan.notesOutcome.writable) {
    plan.firstPhase = 'choices';
    error('Notes are not currently writable. Your existing files are unchanged.');
    render();
    return;
  }
  const draft = readNoteDraft();
  if (!draft.sourceId) writeNoteDraft({ ...draft, sourceId: state.notes.source.id });
  plan.firstPhase = 'note';
  plan.noteError =
    draft.sourceId && draft.sourceId !== state.notes.source.id
      ? 'Your Notes connection changed. Your draft is still here; check Notes before saving.'
      : '';
  render();
  const text = el('NoteText');
  text.value = readNoteDraft().text;
  text.focus();
}

async function saveNote(event) {
  event?.preventDefault?.();
  if (state.busy) return;
  const wasCompact = plan.compact;
  const previous = readNoteDraft();
  const text = previous.pendingText || el('NoteText').value;
  const draft = { ...previous, text };
  writeNoteDraft(draft);
  if (!text.trim()) {
    plan.noteError = 'Write something to save, or go back.';
    render();
    el('NoteText').focus();
    return;
  }
  state.busy = true;
  plan.noteError = '';
  render();
  let uncertain = false;
  try {
    state.notes = await readNotesStatus();
    plan.notesOutcome = hqLiveNotesOutcome(state.notes);
    if (
      !plan.notesOutcome.writable ||
      !draft.sourceId ||
      draft.sourceId !== state.notes?.source?.id
    ) {
      throw new Error(
        'Your Notes connection changed or is not writable. Your draft is still here; check Notes before saving.'
      );
    }
    let response;
    writeNoteDraft({ ...draft, pendingText: text });
    try {
      response = await fetch(`${NOTES_ENDPOINT}/capture`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
        body: JSON.stringify({ text, request_id: draft.requestId })
      });
    } catch (_) {
      uncertain = true;
      throw new Error(
        'Ori could not confirm the note was saved. Save again: the same note is never saved twice.'
      );
    }
    const body = await response.json().catch(() => ({}));
    if (!response.ok) {
      // A 5xx may have written the note before failing; the same request id
      // makes a second press replay it rather than write another.
      uncertain = response.status >= 500 || response.status === 409;
      throw new Error(body.error || 'Ori could not save the note. Your text is still here.');
    }
    const capture = body.capture || {};
    if (typeof capture.note !== 'string' || !capture.note.startsWith('Inbox/')) {
      uncertain = true;
      throw new Error(
        'Ori could not confirm the saved note. Retry the same save; your draft is still here.'
      );
    }
    plan.saved = { text: text.trim(), note: capture.note };
    // Saved: the next note is a new one.
    writeNoteDraft({ text: '', requestId: newNoteRequestID() });
    plan.firstPhase = 'saved';
  } catch (cause) {
    plan.noteError = cause?.message || 'Ori could not save the note. Your text is still here.';
    if (!uncertain) writeNoteDraft(draft);
  } finally {
    state.busy = false;
    render();
    if ((!wasCompact || plan.compact) && !document.hidden) {
      const next = plan.firstPhase === 'saved' ? el('NoteSavedTitle') : el('NoteText');
      if (next?.getClientRects?.().length) next.focus();
    }
  }
}

function error(message) {
  const box = el('Error');
  box.textContent = String(message || '');
  box.hidden = !message;
}

function renderReceipt(rows) {
  const list = el('Receipt');
  list.replaceChildren();
  const detailRows = plan.notesOutcome?.path
    ? [...rows, { kind: 'notes', name: plan.notesOutcome.path }]
    : rows;
  for (const row of detailRows) {
    const item = document.createElement('li');
    const label =
      row.kind === 'workspace'
        ? 'Workspace'
        : row.kind === 'schedule'
          ? 'Schedule'
          : row.kind === 'scope'
            ? 'Scope'
            : row.kind === 'notes'
              ? 'Notes'
              : 'Directory';
    item.append(`${label} · `);
    if (row.route) {
      const link = document.createElement('a');
      link.href = row.route;
      link.textContent = row.name;
      item.append(link);
    } else {
      item.append(document.createTextNode(row.name));
    }
    list.append(item);
  }
}

function render() {
  const card = el('Card');
  if (!card) return;
  const view = hqCardView(state.relationship, state.root, plan);
  // Once built, the compact canonical Done item owns the presentation. Keep
  // these server-observed rows for its receipt disclosure, not a second card
  // under Needs you — except for the build just made in this tab, and a notes
  // setup that has not finished: their outcome and retry stay in view.
  const notesNeedsYou =
    !!plan.notesOutcome && ['failed', 'pending'].includes(plan.notesOutcome.status);
  const resultActive = view.receipt && (plan.firstAction || notesNeedsYou);
  const host = document.getElementById('personalHQResultHost');
  const pendingHadFocus = document
    .querySelector('[data-creation-map-status]')
    ?.contains(document.activeElement);
  const intent = currentHQIntent();
  if (intent?.yielded) {
    if (!intent.preserveResult) compactRecord('');
    plan.compactYielded = true;
  }
  const ownsPresentation =
    intent && intent.outcome === 'ready' && hqPresentation()?.current(intent);
  const adoptingResult = ownsPresentation && !intent.externalResult;
  const restoreCompact =
    !intent &&
    !plan.compactYielded &&
    !!plan.receiptWorkspaceID &&
    compactRecord() === plan.receiptWorkspaceID &&
    !window.PersonalAssistantPanel?.isOpen?.();
  plan.compact = !!host && resultActive && (ownsPresentation || restoreCompact);
  if (host) {
    host.hidden = !plan.compact;
    host.dataset.phase = plan.firstPhase;
    if (plan.compact) {
      hqPresentation()?.adoptResult(intent);
      compactRecord(plan.receiptWorkspaceID);
      if (card.parentNode !== host) host.append(card);
    } else if (card.parentNode === host)
      document.getElementById('personalAssistantNeedsYouCards')?.append(card);
  }
  el('Welcome').hidden = !plan.compact;
  el('Eyebrow').hidden = plan.compact;
  el('Details').hidden = !view.receipt;
  el('Skip').hidden = !plan.compact || !intent?.motion;
  if (plan.compact) {
    el('WelcomeCopy').textContent = `${view.name} has a home base. Welcome to your HQ.`;
    const avatar = window.AgentAvatar;
    el('Avatar').innerHTML = avatar?.markup
      ? avatar.markup(
          { name: view.name, appearance: state.relationship?.appearance || {} },
          { size: 40 }
        )
      : '';
  }
  card.hidden = !view.visible || (view.receipt && !resultActive);
  card.dataset.result = resultActive ? 'active' : '';
  renderNotes(view);
  renderFirst(view, resultActive);
  announceResult(resultActive && !card.hidden && !plan.compact && !currentHQIntent());
  if (!view.visible) return;
  el('Eyebrow').textContent = state.justHired ? '✓ Mission 01 complete' : 'Personal HQ';
  el('Headline').textContent = view.receipt
    ? 'Your HQ is ready'
    : view.collapsed
      ? 'Build My HQ'
      : hqHeadline(view.name);
  el('Plan').hidden = view.collapsed || view.receipt || view.paused;
  el('Receipt').hidden = !view.receipt;
  if (view.receipt) renderReceipt(plan.receipt);
  el('Name').value = plan.name;
  el('Time').value = plan.time;
  el('Root').value = state.pendingRoot ?? view.root.path;
  el('RootPath').textContent = state.pendingRoot ?? view.root.path;
  el('RootStatus').textContent =
    state.pendingRoot !== null
      ? 'Directory changed. Confirm it, then review the updated Notes location.'
      : view.root.status;
  el('Change').textContent = state.pendingRoot !== null ? 'Confirm directory' : 'Change';
  const summary = hqReviewSummary(plan);
  el('BriefSummary').textContent = summary.schedule;
  el('Scope').textContent = `${summary.scope} ${summary.notification}`;
  el('Build').textContent = view.building
    ? 'Resuming…'
    : view.paused
      ? 'Resume setup'
      : view.collapsed
        ? 'Build My HQ'
        : state.busy
          ? 'Checking setup…'
          : 'Build my HQ';
  el('Build').disabled =
    state.busy || state.pendingRoot !== null || (!view.collapsed && !view.canBuild);
  if (hasPendingHQRequest() && !view.receipt && !view.paused) {
    el('Build').textContent = 'Check setup status';
    el('Build').disabled = state.busy;
    el('Plan').hidden = true;
    el('Adjust').hidden = true;
  }
  el('Build').setAttribute('aria-busy', String(state.busy));
  for (const id of ['Name', 'Time']) el(id).disabled = state.busy;
  el('Build').hidden = view.receipt;
  el('BuildActions').hidden = view.receipt;
  el('Adjust').hidden = view.collapsed || view.receipt;
  el('NotNow').hidden = view.collapsed || view.receipt;
  el('Change').hidden = view.paused;
  // Build-time controls belong to the review, never to the built result.
  el('Adjust').hidden = hasPendingHQRequest() || view.paused || view.collapsed || view.receipt;
  el('NotNow').hidden = view.paused || view.collapsed || view.receipt;
  el('Change').disabled = state.busy || view.building;
  el('Adjust').disabled = state.busy || view.building;
  el('NotNow').disabled = state.busy || view.building;
  el('Status').textContent = view.status;
  document.dispatchEvent(new CustomEvent('personal-assistant:hq-receipt'));
  if (plan.compact && adoptingResult) {
    // One reframe after the compact surface has its real dimensions. Neither
    // polling, editor changes nor restoring a tab may recenter the map.
    try {
      window.OriWorkspaceMap?.frameHQCreation?.(intent.workspaceId);
    } catch (_) {
      // A missing/broken renderer cannot change the confirmed HQ/Notes result.
    }
  }
  if (plan.compact && pendingHadFocus && !document.hidden) {
    el('Headline').tabIndex = -1;
    el('Headline').focus({ preventScroll: true });
  }
}

async function readJSON(url, options) {
  const response = await fetch(url, options || { headers: { Accept: 'application/json' } });
  const body = await response.json().catch(() => ({}));
  if (!response.ok) throw new Error(body.error || 'Could not finish Personal HQ setup. Retry.');
  return body;
}

async function load(relationship = null) {
  const seq = ++state.sequence;
  try {
    const current =
      relationship || (await readJSON('/api/personal-assistant')).personal_assistant || null;
    const root = await loadHQWorkspaceRoot();
    if (!plan.timezone) plan.timezone = await defaultHQTimezone();
    const hq = await readJSON('/api/personal-hq/status');
    // The notes status is read for the review, never the notes themselves. A
    // failed read leaves notes out of the build rather than guessing.
    const notes = await readNotesStatus();
    if (seq !== state.sequence) return;
    state.relationship = current;
    state.root = root;
    state.notes = notes;
    // The notes half from the record alone: an unfinished setup, else the
    // connection, else nothing. HQ success never stands in for it.
    plan.notesOutcome = hqLiveNotesOutcome(notes);
    // The first step shows only for the HQ this browser just built.
    const record = readFirstAction(firstActionStorage());
    const workspaceId = String(hq?.status?.workspace?.id || hq?.status?.workspace_id || '');
    plan.firstAction =
      current?.state === 'active' &&
      !!hq?.status?.valid &&
      !plan.firstActionEnded &&
      hqFirstActionMatches(workspaceId, record, currentHQIntent()?.workspaceId);
    if (current?.state === 'active' && hq?.status?.valid) {
      // After a reload the HQ and its schedule are still canonical, but the
      // current workspace-root setting is not proof of the original directory.
      // Keep that third row only for the fresh build in this session.
      if (!plan.receipt.length || plan.receiptWorkspaceID !== workspaceId) {
        const time = String(current.daily_brief?.schedule_time || '').trim();
        plan.receipt = hqReceiptRows(hq.status, { ...current.daily_brief, time }, '');
        plan.receiptWorkspaceID = workspaceId;
      }
    } else {
      plan.receipt = [];
      plan.receiptWorkspaceID = '';
    }
    plan.collapsed =
      !plan.reviewOpened && hq?.status?.hq_onboarding_state === 'skipped' && !plan.receipt.length;
    if (['needs_hq', 'provisioning_hq'].includes(current?.state) && !plan.collapsed) {
      try {
        state.justHired = window.sessionStorage.getItem(JUST_HIRED_FLAG) === '1';
        if (state.justHired) window.sessionStorage.removeItem(JUST_HIRED_FLAG);
      } catch (_) {
        // Storage is optional; the card itself still renders.
      }
    }
    render();
  } catch (_) {
    // Never enable a build if its root or relationship cannot be confirmed.
    if (seq !== state.sequence) return;
    state.root = null;
    render();
    if (state.relationship?.state === 'needs_hq')
      error('Could not verify the workspace directory. Retry.');
  }
}

async function build() {
  if (plan.collapsed) {
    plan.reviewOpened = true;
    plan.collapsed = false;
    render();
    return;
  }
  const view = hqCardView(state.relationship, state.root, plan);
  if (state.busy) return;
  if (hasPendingHQRequest() && !view.paused) {
    const result = await checkHQSetup();
    await load(result.personal_assistant);
    if (result.outcome === 'uncertain')
      error(
        'Setup could not be confirmed. The saved request has not been changed. Check again or reload.'
      );
    return;
  }
  if (state.pendingRoot !== null || !view.canBuild) return;
  const focusOwned = el('Card').contains(document.activeElement);
  if (view.paused) {
    state.busy = true;
    plan.busy = true;
    error('');
    render();
    try {
      const relationship = await window.PersonalHQOnboarding?.resumeHQSetup?.({ focusOwned });
      if (!relationship) throw new Error('Could not resume Personal HQ setup. Try again.');
      await load(relationship);
    } catch (cause) {
      error(cause?.message || 'Could not resume Personal HQ setup. Try again.');
    } finally {
      state.busy = false;
      plan.busy = false;
      render();
    }
    return;
  }
  plan.name = el('Name').value.trim() || 'My HQ';
  plan.time = el('Time').value || '08:00';
  plan.schedule_time = plan.time;
  state.busy = true;
  plan.failed = false;
  error('');
  render();
  try {
    const latest = (await readJSON('/api/personal-assistant')).personal_assistant;
    const target = hqBuildTarget(latest);
    const root = await loadHQWorkspaceRoot();
    if (
      !target.paf ||
      !hqWorkspaceRootView(root).confirmed ||
      hqWorkspaceRootView(root).path !== hqWorkspaceRootView(state.root).path
    ) {
      state.root = root;
      state.relationship = latest;
      throw new Error('The workspace directory or relationship changed. Check the plan and retry.');
    }
    // Notes: re-read right before confirming. If what the review showed is no
    // longer what a build would do — a folder got connected, or the place
    // moved — show the new review instead of silently building something else.
    const reviewed = notesView();
    state.notes = await readNotesStatus();
    const current = notesView();
    if (
      reviewed.mode !== current.mode ||
      reviewed.path !== current.path ||
      reviewed.include !== current.include ||
      reviewed.writable !== current.writable ||
      reviewed.sourceName !== current.sourceName
    ) {
      throw new Error(
        'Your notes setup changed since you reviewed it. Check the plan and build again.'
      );
    }
    const result = await submitReviewedHQ(
      target,
      {
        ...hqReviewDraft(plan),
        notes: hqNotesPayload(current)
      },
      { root: hqWorkspaceRootView(root).path, focusOwned }
    );
    const relationship = result.personal_assistant;
    if (relationship) state.relationship = relationship;
    if (result.outcome !== 'ready') {
      error(
        result.outcome === 'stale'
          ? 'HQ was built; refresh the view. Do not build again.'
          : 'Setup is not yet confirmed. Check setup status or resume the saved setup.'
      );
      return;
    }
    const hq = result.hq;
    const rows = hqReceiptRows(hq, plan, hqWorkspaceRootView(root).path);
    if (!rows.length) throw new Error('HQ was built. Reload to see its workspace.');
    plan.receipt = rows;
    plan.receiptWorkspaceID = hq?.workspace?.id || '';
    // HQ is built whatever the notes half says; its outcome is shown on its own.
    state.notes = await readNotesStatus();
    plan.notesOutcome = hqLiveNotesOutcome(state.notes);
    plan.justBuilt = true;
    // The first step belongs to this build: remembered for this HQ until the
    // user leaves it, so a reload brings it back and a later visit does not.
    rememberFirstAction(firstActionStorage(), hq?.workspace?.id || '');
    plan.firstAction = true;
    plan.firstActionEnded = false;
    plan.firstPhase = 'choices';
    state.relationship = relationship;
    for (const name of [
      'ori:personal-hq-changed',
      'ori:workspaces-changed',
      'ori:progression-refresh'
    ]) {
      window.dispatchEvent(new CustomEvent(name, { detail: { source: 'personal_hq_setup' } }));
    }
    await window.PersonalAssistantPanel?.refresh?.();
    await window.PersonalAssistantToday?.refresh?.();
    await window.PersonalAssistantFolder?.reload?.();
  } catch (cause) {
    plan.failed = true;
    // A refused notes review (the place moved, a folder got connected) shows
    // the current review next to the message, so the user can confirm again.
    state.notes = await readNotesStatus();
    error(cause?.message || 'Could not build Personal HQ. Retry the same request.');
  } finally {
    state.busy = false;
    render();
    if (plan.justBuilt && !currentHQIntent()) focusResult();
  }
}

// After a build the result is where the user is: its headline takes focus (so
// the next Tab reaches the first choice) and comes into view whole, not at the
// review's old scroll offset.
function focusResult() {
  const headline = el('Headline');
  if (!headline || el('Card')?.hidden) return;
  headline.tabIndex = -1;
  el('Card').scrollIntoView?.({ block: 'start' });
  headline.focus({ preventScroll: true });
}

// Choose another notes location through the trusted folder picker. The
// selection token, not the path, is what the build sends.
async function chooseNotesLocation() {
  if (state.busy) return;
  state.busy = true;
  error('');
  render();
  try {
    const response = await fetch(FOLDER_PATH_ENDPOINT, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
      body: JSON.stringify({ title: 'Choose where to set up your notes' })
    });
    const result = (await response.json().catch(() => ({}))) || {};
    const off = folderDialogOffNote(result);
    if (off) {
      error(off);
    } else if (response.ok && result.selected && result.selection_token) {
      // The same folder name the default place uses, read from the server's
      // own default rather than spelled here.
      const defaultPath = String(state.notes?.setup_default?.path || '');
      const name = defaultPath.split(/[\\/]/).filter(Boolean).pop() || plan.notesChosen?.name || '';
      if (!name) throw new Error('Choose the notes location again.');
      plan.notesChosen = {
        selectionToken: result.selection_token,
        path: String(result.path || ''),
        name
      };
      plan.notesInclude = true;
    } else if (!response.ok) {
      error(result.error || 'The folder dialog could not open.');
    }
  } catch (_) {
    error('The folder dialog could not open.');
  } finally {
    state.busy = false;
    render();
  }
}

// Retry only the notes half of a build, exactly as it was reviewed. HQ, its
// schedule and the assistant are never touched by this.
async function retryNotes() {
  if (state.busy) return;
  const wasCompact = plan.compact;
  state.busy = true;
  render();
  let retried = false;
  try {
    const body = await readJSON(`${NOTES_ENDPOINT}/setup/retry`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
      body: '{}'
    });
    plan.notesOutcome = hqNotesOutcome(body.notes_setup);
    state.notes = await readNotesStatus();
    if (state.notes) plan.notesOutcome = hqLiveNotesOutcome(state.notes);
    await window.PersonalAssistantToday?.refresh?.();
    retried = true;
  } catch (cause) {
    plan.notesOutcome = {
      ...(plan.notesOutcome || {}),
      line: cause?.message || 'Notes could not be set up. Your HQ is ready.',
      canRetry: true
    };
  } finally {
    state.busy = false;
    if (retried && plan.notesOutcome?.status === 'ready') {
      // A success is shown, not just a card that disappears: the result stays
      // with its next step until the user leaves it.
      rememberFirstAction(firstActionStorage(), plan.receiptWorkspaceID);
      plan.firstAction = true;
      plan.firstPhase = 'choices';
    }
    render();
    if (
      retried &&
      (!wasCompact || plan.compact) &&
      !document.hidden &&
      el('NotesResultText')?.getClientRects?.().length
    )
      el('NotesResultText').focus();
  }
}

async function changeRoot() {
  if (state.busy) return;
  state.busy = true;
  error('');
  render();
  try {
    const path = state.pendingRoot ?? (await chooseHQWorkspaceRoot());
    if (state.pendingRoot !== null && !path)
      throw new Error('Choose a workspace directory in Customize setup.');
    if (path) {
      state.root = await saveHQWorkspaceRoot(path);
      state.pendingRoot = null;
      // The default notes place follows the directory; review it again.
      state.notes = await readNotesStatus();
    }
  } catch (cause) {
    error(cause?.message || 'Could not choose the workspace directory.');
  } finally {
    state.busy = false;
    render();
  }
}

async function notNow() {
  if (state.busy) return;
  state.busy = true;
  error('');
  render();
  try {
    await skipHQObjective();
    plan.reviewOpened = false;
    plan.collapsed = true;
    window.dispatchEvent(new CustomEvent('ori:progression-refresh'));
  } catch (cause) {
    error(cause?.message || 'Could not defer Personal HQ. Try again.');
  } finally {
    state.busy = false;
    render();
  }
}

async function adjust() {
  if (state.busy) return;
  plan.name = el('Name').value.trim() || 'My HQ';
  plan.time = el('Time').value || '08:00';
  plan.schedule_time = plan.time;
  if (window.PersonalHQOnboarding?.openBuildModal) {
    await window.PersonalHQOnboarding.openBuildModal({
      draft: hqReviewDraft(plan),
      root: state.pendingRoot ?? hqWorkspaceRootView(state.root).path,
      onReturn: (draft, root) => {
        Object.assign(plan, hqReviewDraft(draft));
        plan.time = draft.schedule_time;
        state.pendingRoot = root !== hqWorkspaceRootView(state.root).path ? root : null;
        render();
        el('Adjust').focus({ preventScroll: true });
      }
    });
  } else {
    error('The HQ form is unavailable. Reload to try again.');
  }
}

function init() {
  if (!el('Card')) return;
  el('Build').addEventListener('click', build);
  el('Change').addEventListener('click', changeRoot);
  el('Adjust').addEventListener('click', adjust);
  el('NotNow').addEventListener('click', notNow);
  el('NotesInclude')?.addEventListener('change', event => {
    plan.notesInclude = event.target.checked;
    render();
  });
  el('NotesChoose')?.addEventListener('click', chooseNotesLocation);
  el('NotesRetry')?.addEventListener('click', retryNotes);
  // The first step after a build. Nothing here reads a folder or starts a
  // setup until a choice is pressed.
  el('FirstNote')?.addEventListener('click', openNoteComposer);
  el('NoteForm')?.addEventListener('submit', saveNote);
  el('NoteText')?.addEventListener('input', event => {
    writeNoteDraft({ ...readNoteDraft(), text: event.target.value });
  });
  el('NoteBack')?.addEventListener('click', () => {
    // The draft stays; Back only closes the composer.
    plan.firstPhase = 'choices';
    plan.noteError = '';
    render();
    el('FirstNote')?.focus?.();
  });
  el('NoteAnother')?.addEventListener('click', () => {
    plan.saved = null;
    openNoteComposer();
  });
  el('NoteDone')?.addEventListener('click', endFirstAction);
  el('FirstExplore')?.addEventListener('click', event => {
    if (state.busy) {
      event.preventDefault();
      return;
    }
    endFirstAction({ focus: false });
  });
  el('Dismiss')?.addEventListener('click', () => endFirstAction());
  el('Skip')?.addEventListener('click', () => hqPresentation()?.skip(currentHQIntent()));
  document.addEventListener('personal-hq:presentation', render);
  const fitResult = () => {
    const host = document.getElementById('personalHQResultHost');
    const viewport = window.visualViewport;
    if (!host || !viewport) return;
    const keyboardOffset = Math.max(0, window.innerHeight - viewport.height - viewport.offsetTop);
    host.style.setProperty('--hq-keyboard-offset', `${keyboardOffset}px`);
    host.style.setProperty('--hq-visible-height', `${viewport.height}px`);
    // Overlay keyboards leave innerHeight unchanged. Keep the editor below
    // the sticky navigation as well as above that smaller visual viewport.
    const navHeight =
      keyboardOffset > 0
        ? document.querySelector('.navbar')?.getBoundingClientRect().height || 0
        : 0;
    host.style.setProperty('--hq-keyboard-nav-height', `${navHeight}px`);
  };
  window.visualViewport?.addEventListener('resize', fitResult);
  window.visualViewport?.addEventListener('scroll', fitResult);
  fitResult();
  document.addEventListener('personal-assistant:opened', () => {
    compactRecord('');
    plan.compactYielded = true;
    render();
  });
  el('FirstOpenNotes')?.addEventListener('click', () => endFirstAction({ focus: false }));
  el('FirstFolder')?.addEventListener('click', () => {
    // The existing guarded folder chooser; it reads nothing until a folder is
    // chosen and reviewed there.
    endFirstAction();
    window.PersonalAssistantFolder?.openChooser?.();
  });
  el('FirstSetupNotes')?.addEventListener('click', () => {
    endFirstAction();
    void window.PersonalAssistantSecondBrain?.openSetup?.();
  });
  for (const [field, key] of [
    ['Name', 'name'],
    ['Time', 'time']
  ]) {
    el(field).addEventListener('input', event => {
      plan[key] = event.target.value;
      if (key === 'time') plan.schedule_time = event.target.value;
      const summary = hqReviewSummary(plan);
      el('BriefSummary').textContent = summary.schedule;
    });
  }
  document.addEventListener('personal-assistant:status', event => {
    const relationship = event.detail?.personalAssistant;
    if (relationship && !state.busy) void load(relationship);
  });
  window.addEventListener('ori:hq-quest-signal', event => {
    if (event.detail?.stage === 'setup-succeeded') void load();
  });
  const current = window.PersonalAssistantPanel?._state?.personalAssistant;
  if (current) void load(current);
}

const api = {
  load,
  receiptRows: () =>
    state.relationship?.state === 'active' ? plan.receipt.map(row => ({ ...row })) : [],
  show: () => {
    plan.reviewOpened = true;
    plan.collapsed = false;
    render();
  },
  // expand re-opens the card the user deferred with Not now and brings it into
  // view: the locked folder mission's one action. It never builds anything.
  expand: () => {
    plan.reviewOpened = true;
    plan.collapsed = false;
    render();
    const card = el('Card');
    if (card && !card.hidden) card.scrollIntoView?.({ block: 'nearest' });
    return Boolean(card) && !card.hidden;
  },
  _state: state
};
if (typeof window !== 'undefined') window.PersonalAssistantHQCard = api;
if (typeof document !== 'undefined') {
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', init);
  else init();
}
