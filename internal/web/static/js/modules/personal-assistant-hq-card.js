// The assistant's one-confirmation HQ proposal in Today. All durable effects
// reuse the HQ form's request identity, root confirmation, and PAF payload.
import {
  chooseHQWorkspaceRoot,
  clearHQRequestID,
  hqBuildRequestPayload,
  hqBuildTarget,
  hqRequestID,
  hqWorkspaceRootView,
  loadHQWorkspaceRoot,
  saveHQWorkspaceRoot,
  skipHQObjective
} from './personal-hq-onboarding.js';
import { JUST_HIRED_FLAG } from './personal-assistant-hire.js';
import { FOLDER_PATH_ENDPOINT, folderDialogOffNote } from './folder-path-entry.js';
import {
  forgetFirstAction,
  hqFirstActionView,
  hqNotesOutcome,
  hqNotesPayload,
  hqNotesReviewView,
  readFirstAction,
  rememberFirstAction
} from './personal-hq-notes-review.js';

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
  return `${who} is hired. Give me a home base: My HQ is where I keep what I learn about the folders you show me, and where your Daily Brief lands.`;
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
      ? [{ kind: 'schedule', name: `Daily Brief at ${plan.time} on weekdays`, detail: '' }]
      : []),
    ...(directory ? [{ kind: 'directory', name: String(directory), detail: '' }] : [])
  ];
}

const plan = {
  name: 'My HQ',
  time: '08:00',
  collapsed: false,
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
  noteError: ''
};

// The quick note's draft and its one request id, kept for this tab so Back,
// a failure or a reload never loses the text, and a retry after a lost
// response replays the same save instead of writing a second note.
const NOTE_DRAFT_KEY = 'ori.hq.firstNote.v1';

function newNoteRequestID() {
  const random = Math.random().toString(36).slice(2, 10);
  return `hq-note-${Date.now().toString(36)}-${random}`;
}

function readNoteDraft() {
  try {
    const parsed = JSON.parse(window.sessionStorage.getItem(NOTE_DRAFT_KEY) || 'null');
    if (parsed && typeof parsed.text === 'string' && typeof parsed.requestId === 'string') {
      return parsed;
    }
  } catch (_) {
    // A draft is a convenience; a fresh one is fine.
  }
  return { text: '', requestId: newNoteRequestID() };
}

function writeNoteDraft(draft) {
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
  justHired: false
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
  result.hidden = !outcome || !outcome.line || !view.receipt;
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
  el('FirstLead').textContent = choicesView.lead;
  el('FirstLead').hidden = !choosing;
  el('FirstNote').hidden = !choosing || !choicesView.note;
  el('FirstOpenNotes').hidden = !choosing || !choicesView.openNotes;
  if (choicesView.openNotes) el('FirstOpenNotes').href = choicesView.openNotes;
  el('FirstSetupNotes').hidden = !choosing || !choicesView.setupNotes;
  el('FirstFolder').hidden = !choosing;
  el('FirstExplore').hidden = !choosing;
  for (const id of ['FirstNote', 'FirstSetupNotes', 'FirstFolder', 'FirstExplore']) {
    el(id).disabled = state.busy;
  }
  el('NoteForm').hidden = phase !== 'note';
  el('NoteError').textContent = plan.noteError;
  el('NoteError').hidden = !plan.noteError;
  el('NoteSave').disabled = state.busy;
  el('NoteSave').setAttribute('aria-busy', state.busy && phase === 'note' ? 'true' : 'false');
  el('NoteBack').disabled = state.busy;
  el('NoteSaved').hidden = phase !== 'saved' || !plan.saved;
  if (phase === 'saved' && plan.saved) {
    el('NoteSavedTitle').textContent = `✓ Saved to Notes · ${plan.saved.note}`;
    el('NoteSavedText').textContent = plan.saved.text;
  }
}

// Ends the first step. The HQ and notes stay exactly as they are; only this
// browser's presentation record goes, so the card does not come back.
function endFirstAction() {
  forgetFirstAction(firstActionStorage());
  plan.firstAction = false;
  plan.firstPhase = 'choices';
  plan.justBuilt = false;
  render();
  // The pressed control is gone with the card; hand focus to what leads now:
  // Today's strip, else the composer.
  const strip = document.getElementById('personalAssistantTodayStripToggle');
  const next =
    strip && strip.offsetParent !== null
      ? strip
      : document.getElementById('personalAssistantInput');
  next?.focus?.({ preventScroll: true });
}

function openNoteComposer() {
  plan.firstPhase = 'note';
  plan.noteError = '';
  render();
  const text = el('NoteText');
  text.value = readNoteDraft().text;
  text.focus();
}

async function saveNote(event) {
  event?.preventDefault?.();
  if (state.busy) return;
  const text = el('NoteText').value;
  const draft = { ...readNoteDraft(), text };
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
    let response;
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
      uncertain = response.status >= 500;
      throw new Error(body.error || 'Ori could not save the note. Your text is still here.');
    }
    const capture = body.capture || {};
    plan.saved = { text: text.trim(), note: String(capture.note || capture.name || 'Inbox') };
    // Saved: the next note is a new one.
    writeNoteDraft({ text: '', requestId: newNoteRequestID() });
    plan.firstPhase = 'saved';
  } catch (cause) {
    plan.noteError = cause?.message || 'Ori could not save the note. Your text is still here.';
    if (!uncertain) writeNoteDraft(draft);
  } finally {
    state.busy = false;
    render();
    if (plan.firstPhase === 'saved') el('NoteSavedTitle')?.focus?.();
    else el('NoteText')?.focus?.();
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
  for (const row of rows) {
    const item = document.createElement('li');
    const label =
      row.kind === 'workspace' ? 'Workspace' : row.kind === 'schedule' ? 'Schedule' : 'Directory';
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
  card.hidden = !view.visible || (view.receipt && !resultActive);
  card.dataset.result = resultActive ? 'active' : '';
  renderNotes(view);
  renderFirst(view, resultActive);
  announceResult(resultActive && !card.hidden);
  if (!view.visible) return;
  el('Eyebrow').textContent = state.justHired ? '✓ Mission 01 complete' : 'Personal HQ';
  el('Headline').textContent = view.receipt
    ? `${view.name}’s Personal HQ is ready.`
    : view.collapsed
      ? 'Build My HQ'
      : hqHeadline(view.name);
  el('Plan').hidden = view.collapsed || view.receipt || view.paused;
  el('Receipt').hidden = !view.receipt;
  if (view.receipt) renderReceipt(plan.receipt);
  el('Name').value = plan.name;
  el('Time').value = plan.time;
  el('Root').value = view.root.path;
  el('RootStatus').textContent = view.root.status;
  el('Build').textContent = view.building
    ? 'Resuming…'
    : view.paused
      ? 'Resume setup'
      : view.collapsed
        ? 'Build My HQ'
        : 'Build';
  el('Build').disabled = state.busy || (!view.collapsed && !view.canBuild);
  el('Build').hidden = view.receipt;
  el('Adjust').hidden = view.collapsed || view.receipt;
  el('NotNow').hidden = view.collapsed || view.receipt;
  el('Change').hidden = view.paused;
  // Build-time controls belong to the review, never to the built result.
  el('Adjust').hidden = view.paused || view.collapsed || view.receipt;
  el('NotNow').hidden = view.paused || view.collapsed || view.receipt;
  el('Change').disabled = state.busy || view.building;
  el('Adjust').disabled = state.busy || view.building;
  el('NotNow').disabled = state.busy || view.building;
  el('Status').textContent = view.status;
  document.dispatchEvent(new CustomEvent('personal-assistant:hq-receipt'));
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
    if (notes?.setup) {
      plan.notesOutcome = hqNotesOutcome(notes.setup);
    } else if (notes?.source) {
      plan.notesOutcome = hqNotesOutcome({
        status: 'ready',
        path: notes.source.path,
        writable: notes.source.origin === 'created_by_ori' && notes.status === 'connected'
      });
    } else if (notes) {
      plan.notesOutcome = null;
    }
    // The first step shows only for the HQ this browser just built.
    const record = readFirstAction(firstActionStorage());
    plan.firstAction =
      current?.state === 'active' &&
      !!hq?.status?.valid &&
      !!record &&
      record.workspaceId === String(hq.status.workspace?.id || hq.status.workspace_id || '');
    if (current?.state === 'active' && hq?.status?.valid) {
      // After a reload the HQ and its schedule are still canonical, but the
      // current workspace-root setting is not proof of the original directory.
      // Keep that third row only for the fresh build in this session.
      if (!plan.receipt.length || plan.receiptWorkspaceID !== hq.status.workspace?.id) {
        const time = String(current.daily_brief?.schedule_time || '').trim();
        plan.receipt = hqReceiptRows(hq.status, { time }, '');
        plan.receiptWorkspaceID = hq.status.workspace?.id || '';
      }
    } else {
      plan.receipt = [];
      plan.receiptWorkspaceID = '';
    }
    plan.collapsed = hq?.status?.hq_onboarding_state === 'skipped' && !plan.receipt.length;
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
    plan.collapsed = false;
    render();
    return;
  }
  const view = hqCardView(state.relationship, state.root, plan);
  if (state.busy || !view.canBuild) return;
  if (view.paused) {
    state.busy = true;
    plan.busy = true;
    error('');
    render();
    try {
      const relationship = await window.PersonalHQOnboarding?.resumeHQSetup?.();
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
      root.workspace_root !== state.root?.workspace_root
    ) {
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
      reviewed.include !== current.include
    ) {
      throw new Error(
        'Your notes setup changed since you reviewed it. Check the plan and build again.'
      );
    }
    let timezone = 'UTC';
    try {
      timezone = Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC';
    } catch (_) {
      // The server validates the fallback too.
    }
    const result = await readJSON(target.endpoint, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
      body: JSON.stringify(
        hqBuildRequestPayload(
          target,
          {
            name: plan.name,
            timezone,
            schedule_days: ['mon', 'tue', 'wed', 'thu', 'fri'],
            schedule_time: plan.time,
            scope: 'all',
            include_future_workspaces: true,
            notify_on_ready: false,
            notes: hqNotesPayload(current)
          },
          hqRequestID(window.localStorage)
        )
      )
    });
    const relationship = result.personal_assistant;
    if (relationship?.state !== 'active')
      throw new Error('HQ is still being built. Retry this request.');
    clearHQRequestID(window.localStorage);
    const hq = (await readJSON('/api/personal-hq/status')).status;
    const rows = hqReceiptRows(hq, plan, hqWorkspaceRootView(root).path);
    if (!rows.length) throw new Error('HQ was built. Reload to see its workspace.');
    plan.receipt = rows;
    plan.receiptWorkspaceID = hq?.workspace?.id || '';
    // HQ is built whatever the notes half says; its outcome is shown on its own.
    plan.notesOutcome = result.notes_setup ? hqNotesOutcome(result.notes_setup) : null;
    plan.justBuilt = true;
    // The first step belongs to this build: remembered for this HQ until the
    // user leaves it, so a reload brings it back and a later visit does not.
    rememberFirstAction(firstActionStorage(), hq?.workspace?.id || '');
    plan.firstAction = true;
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
    if (plan.justBuilt) focusResult();
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
    if (retried) el('NotesResultText')?.focus?.();
  }
}

async function changeRoot() {
  if (state.busy) return;
  state.busy = true;
  error('');
  render();
  try {
    const path = await chooseHQWorkspaceRoot();
    if (path) {
      state.root = await saveHQWorkspaceRoot(path);
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
  el('Name').value = plan.name;
  const modalName = document.getElementById('hqBuildName');
  const modalTime = document.getElementById('hqBuildTime');
  if (modalName) modalName.value = plan.name;
  if (modalTime) modalTime.value = plan.time;
  if (window.PersonalHQOnboarding?.openBuildModal) {
    await window.PersonalHQOnboarding.openBuildModal();
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
  el('FirstExplore')?.addEventListener('click', endFirstAction);
  el('FirstOpenNotes')?.addEventListener('click', () => {
    forgetFirstAction(firstActionStorage());
  });
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
    plan.collapsed = false;
    render();
  },
  // expand re-opens the card the user deferred with Not now and brings it into
  // view: the locked folder mission's one action. It never builds anything.
  expand: () => {
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
