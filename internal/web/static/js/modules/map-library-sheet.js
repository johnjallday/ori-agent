// map-library-sheet.js — the Home map's "set up projects" sheet.
//
// PRD: tasks/prd-map-library-setup.md FR-1, FR-3, FR-6 to FR-15, FR-19. A
// Home district's library badge ("5 to set up") opens this popover beside the
// district. It lists what the last scan found, lets the person tick the
// projects to set up, choose a file where a project has several, and press one
// button. The reviews and commits are library-setup.js's, one project at a
// time; nothing here creates, changes or deletes anything on its own.
//
// Only the view model is pure (sheetView, confirmLines). openLibrarySheet
// mounts DOM and is covered by the demo.

import {
  SETUP_KINDS,
  createLibraryClient,
  loadSetupRows,
  loadStaffingPlan,
  runSetup,
  setupLabel,
  setupSubtitle,
  setupTotals,
  staffingDisclosure,
  staffingResultLabel
} from './library-setup.js';

export const CONSENT_LINE =
  'Each starts File-only. Source files stay where they are and are never changed.';

const SHEET_CLASS = 'ws-map-library-sheet';

/**
 * The sheet's view model for one render. `rows` come from loadSetupRows,
 * `chosen` maps row id → chosen file (file-choice rows), `ticked` is the set of
 * ticked row ids, `progress` maps row id → { status, message } during and
 * after a run.
 */
export function sheetView(
  rows,
  { chosen = {}, ticked = new Set(), progress = {}, rowStaffing = {}, repair = new Set() } = {}
) {
  // What can be set up as it is comes first, then what needs a file chosen,
  // then what is already on the map, then what needs the dashboard; inside a
  // bucket the library's order is kept.
  const rank = { setup: 0, file_choice: 1, connected: 2, dashboard: 3 };
  const list = (Array.isArray(rows) ? rows : [])
    .filter(Boolean)
    .map((row, index) => ({ row, index }))
    .sort((a, b) => (rank[a.row.kind] ?? 3) - (rank[b.row.kind] ?? 3) || a.index - b.index)
    .map(entry => entry.row);
  const items = list.map(row => {
    const kind = row.kind;
    const file =
      kind === SETUP_KINDS.SETUP
        ? row.files[0]
        : kind === SETUP_KINDS.FILE_CHOICE && row.files.includes(chosen[row.id])
          ? chosen[row.id]
          : '';
    const checkable =
      (kind === SETUP_KINDS.SETUP || kind === SETUP_KINDS.FILE_CHOICE) && !progress[row.id];
    const checked = checkable && ticked.has(row.id) && file !== '';
    return {
      id: row.id,
      name: row.name,
      kind,
      label: setupLabel(kind),
      file,
      files: kind === SETUP_KINDS.FILE_CHOICE ? row.files.slice() : [],
      checkable,
      checked,
      reason: kind === SETUP_KINDS.DASHBOARD ? row.reason || setupLabel(kind) : '',
      progress: progress[row.id] || null,
      assistant: kind === SETUP_KINDS.CONNECTED ? assistantState(rowStaffing[row.id]) : null,
      repairChecked:
        kind === SETUP_KINDS.CONNECTED &&
        repair.has(row.id) &&
        assistantState(rowStaffing[row.id]).repairable &&
        !progress[row.id]
    };
  });
  const selected = items.filter(item => item.checked);
  const repairing = items.filter(item => item.repairChecked);
  return {
    items,
    totals: setupTotals(list),
    subtitle: setupSubtitle(setupTotals(list)),
    selected,
    buttonLabel: `Put ${selected.length} on the map`,
    buttonDisabled: selected.length === 0,
    repairing,
    repairLabel: `Add the assistant to ${repairing.length}`,
    allConnected: list.length > 0 && list.every(row => row.kind === SETUP_KINDS.CONNECTED)
  };
}

/**
 * A connected row's assistant, from its inert status read
 * (GET …/projects/{id}/staffing). Pure. repairable is true only for an empty
 * role the Home's current approval can fill (create or join); any other plan is
 * a reason shown with the row, never an action.
 */
export function assistantState(status) {
  if (!status) return { known: false, repairable: false, line: '', blocked: '' };
  if (status.filled) {
    const who = status.agent_name || 'an assistant';
    return {
      known: true,
      repairable: false,
      line: status.shared ? `has ${who}` : `has its own ${who}`,
      blocked: ''
    };
  }
  const plan = String(status.plan || '');
  if (plan === 'create' || plan === 'join') {
    return { known: true, repairable: true, line: 'no assistant yet', blocked: '' };
  }
  return {
    known: true,
    repairable: false,
    line: 'no assistant',
    blocked: String(status.message || '')
  };
}

/** The confirmation's lines: each ticked project with its exact file (FR-11). */
export function confirmLines(selected) {
  return (Array.isArray(selected) ? selected : []).map(item => `${item.name} — ${item.file}`);
}

/**
 * What confirming does about the assistant, for the confirmation step. Pure.
 * `choice` is 'staff' or 'placement'; staffing is only ever on when the plan
 * allows it and the person left it chosen.
 */
export function confirmStaffing(plan, count, choice = 'staff') {
  const disclosure = staffingDisclosure(plan, count);
  const staff = disclosure.canStaff && choice !== 'placement';
  return {
    ...disclosure,
    // Choosing placement only must not still say the approval is being used.
    note:
      disclosure.canStaff && !staff
        ? 'No assistant is added now. You can add it later from the Home.'
        : disclosure.note,
    staff,
    teamDigest: staff ? String(plan?.teamDigest || '') : '',
    placementLine:
      Number(count) === 1
        ? 'The project is connected with no assistant.'
        : 'The projects are connected with no assistant.',
    buttonLabel: staff ? 'Put on the map and add the assistant' : 'Put on the map'
  };
}

/**
 * The line a row shows once it has run: its connection and, separately, its
 * assistant. Pure.
 */
export function progressLine(progress) {
  if (!progress) return '';
  if (progress.status === 'working') return 'Setting up…';
  if (progress.status === 'done') {
    const assistant = staffingResultLabel(progress.staffing);
    return assistant ? `On the map · ${assistant}` : 'On the map';
  }
  if (progress.stage === 'staffing') {
    return `On the map · assistant not added: ${progress.message || 'it could not be added'}`;
  }
  return `Failed: ${progress.message || 'could not be set up'}`;
}

/**
 * The status line after a run: what was connected, what happened to the
 * assistant, and where it stopped. Pure.
 */
// A sentence ends once: a server message that already has its full stop keeps it.
function sentence(text) {
  const value = String(text || '').trim();
  return /[.!?]$/.test(value) ? value : `${value}.`;
}

export function summarizeRun(result, staffing, plan) {
  const done = Array.isArray(result?.done) ? result.done : [];
  const failed = result?.failed || null;
  const remaining = Array.isArray(result?.remaining) ? result.remaining : [];
  const role = plan?.roleLabel || 'assistant';
  const onMap = done.length + (failed?.workspaceId ? 1 : 0);
  const parts = [];
  if (onMap) parts.push(`${onMap} ${onMap === 1 ? 'project is' : 'projects are'} on the map.`);
  if (staffing && done.length) {
    // "It has" has no subject when only some of the projects got it, so a run
    // that stopped says how many of how many.
    const count = onMap > done.length ? `${done.length} of ${onMap}` : String(done.length);
    parts.push(`${count} ${done.length === 1 ? 'has' : 'have'} your shared ${role}.`);
  }
  if (failed) {
    parts.push(
      failed.stage === 'staffing'
        ? sentence(`${failed.name} is on the map, but its ${role} was not added: ${failed.message}`)
        : sentence(`Stopped at ${failed.name}: ${failed.message}`)
    );
    if (remaining.length) parts.push(`${remaining.length} not reached and left ticked.`);
  }
  if (!parts.length) parts.push('Nothing was set up.');
  return parts.join(' ');
}

// What the person can do about an assistant that was not added, by reason.
// Each points at the existing place that settles it; none decides anything.
const STAFFING_RECOVERY = Object.freeze({
  needs_model: { kind: 'settings', label: 'Set up a model' },
  none: { kind: 'sharing', label: 'Review sharing on the Home' },
  off: { kind: 'sharing', label: 'Review sharing on the Home' },
  consent_stale: { kind: 'sharing', label: 'Review the updated assistant' },
  invalid: { kind: 'sharing', label: 'Review sharing on the Home' },
  unavailable: { kind: 'sharing', label: 'Review sharing on the Home' },
  assistant_missing: { kind: 'sharing', label: 'Add the shared assistant again' },
  reconcile: { kind: 'agents', label: 'Open Agents' },
  sharing_changed: { kind: 'reload', label: 'Review again' },
  request_reused: { kind: 'reload', label: 'Review again' },
  not_shared: { kind: 'none', label: '' },
  read_only: { kind: 'none', label: '' },
  unavailable_target: { kind: 'none', label: '' }
});

/** The recovery for a reason a staffing step stopped; retry when unknown. Pure. */
export function staffingRecovery(reason) {
  return STAFFING_RECOVERY[reason] || { kind: 'retry', label: 'Try again' };
}

/**
 * The results of one run, one row per project the person confirmed, in order.
 * Pure. Connection and assistant are separate facts; a project that was
 * connected before its assistant failed is reported connected, never as
 * fully set up, and a project never reached says so.
 */
export function resultRows(run) {
  const items = Array.isArray(run?.items) ? run.items : [];
  const result = run?.result || {};
  const done = new Map((result.done || []).map(item => [item.id, item]));
  const failed = result.failed || null;
  const unreached = new Set((result.remaining || []).map(item => item.id));
  return items.map(item => {
    const row = {
      id: item.id,
      name: item.name,
      workspaceId: '',
      connection: '',
      assistant: '',
      recovery: null,
      complete: false
    };
    const finished = done.get(item.id);
    if (finished) {
      row.workspaceId = finished.workspaceId || '';
      row.connection = finished.created ? 'Put on the map' : 'Already on the map';
      row.assistant = staffingResultLabel(finished.staffing);
      row.complete = true;
      if (finished.staffing?.state === 'already' && finished.staffing.shared === false) {
        row.assistant = `kept its own ${finished.staffing.agent_name || 'assistant'}`;
      }
      return row;
    }
    if (failed && failed.id === item.id) {
      if (failed.stage === 'staffing') {
        row.workspaceId = failed.workspaceId || '';
        row.connection = failed.created ? 'Put on the map' : 'On the map';
        row.assistant = `assistant not added: ${failed.message || 'it could not be added'}`;
        row.recovery = staffingRecovery(failed.reason);
      } else {
        row.connection = `Not set up: ${failed.message || 'it could not be set up'}`;
        row.recovery = { kind: 'retry', label: 'Try again' };
      }
      return row;
    }
    if (unreached.has(item.id)) {
      row.connection = result.stopped ? 'Not started (closed)' : 'Not reached';
      return row;
    }
    return row;
  });
}

/**
 * A retry's run folded into the run it continues: every row the person
 * confirmed stays listed, earlier successes are kept, and the retried rows take
 * their new outcome. Pure.
 */
export function mergeRuns(previous, next) {
  const retried = new Set((next?.items || []).map(item => item.id));
  const earlier = (previous?.result?.done || []).filter(item => !retried.has(item.id));
  // The retried row was put on the map by the run that stopped at it: its
  // retry only finishes the assistant, so it is still reported as put there.
  const stopped = previous?.result?.failed;
  const finished = (next?.result?.done || []).map(item =>
    stopped && stopped.id === item.id && stopped.created ? { ...item, created: true } : item
  );
  return {
    items: previous?.items || next?.items || [],
    staffing: next?.staffing ?? previous?.staffing ?? null,
    kind: next?.kind || previous?.kind || 'setup',
    result: {
      done: [...earlier, ...finished],
      failed: next?.result?.failed || null,
      remaining: next?.result?.remaining || [],
      stopped: !!next?.result?.stopped
    }
  };
}

/** The status line after adding the assistant to projects on the map. Pure. */
export function summarizeRepair(result, plan) {
  const done = Array.isArray(result?.done) ? result.done : [];
  const failed = result?.failed || null;
  const remaining = Array.isArray(result?.remaining) ? result.remaining : [];
  const role = plan?.roleLabel || 'assistant';
  const parts = [];
  if (done.length) {
    parts.push(
      `${done.length === 1 ? '1 project now has' : `${done.length} projects now have`} your shared ${role}.`
    );
  }
  if (failed) {
    parts.push(sentence(`${failed.name}: ${failed.message}`));
    if (remaining.length) parts.push(`${remaining.length} not reached and left ticked.`);
  }
  if (!parts.length) parts.push('Nothing changed.');
  return parts.join(' ');
}

/**
 * Each connected row's assistant, read only (no reservation, no agent, no
 * ticket). A row whose read fails stays unknown, never repairable.
 */
export async function loadConnectedStaffing(client, rows) {
  const connected = (Array.isArray(rows) ? rows : []).filter(
    row => row && row.kind === SETUP_KINDS.CONNECTED
  );
  const entries = await Promise.all(
    connected.map(async row => {
      try {
        return [row.id, await client.request(`/projects/${encodeURIComponent(row.id)}/staffing`)];
      } catch (_) {
        return [row.id, null];
      }
    })
  );
  return Object.fromEntries(entries.filter(([, status]) => status));
}

/** The rows ticked by default: single-file projects only (FR-7). */
export function defaultTicked(rows) {
  return new Set(
    (Array.isArray(rows) ? rows : [])
      .filter(row => row && row.kind === SETUP_KINDS.SETUP)
      .map(row => row.id)
  );
}

/**
 * Where the sheet goes: to the right of the anchor when there is room, else
 * below it, always inside the viewport with a 16px gutter. Pure.
 */
export function sheetPosition(anchorRect, sheetSize, viewport) {
  const gutter = 16;
  // The app's top bar is fixed and about 60px tall; a sheet clamped under it
  // on a short window would lose its title and Close button.
  const topGutter = 72;
  const gap = 12;
  const width = Math.min(sheetSize.width, viewport.width - gutter * 2);
  let height = Math.min(sheetSize.height, viewport.height - topGutter - gutter);
  let left = anchorRect.right + gap;
  let top = anchorRect.top;
  if (left + width > viewport.width - gutter) {
    left = anchorRect.left;
    top = anchorRect.bottom + 8;
    // Below its control (a wide toolbar has no room beside it): the sheet fits
    // the space under the control, so the control and its counts stay in
    // sight, as long as that leaves a usable sheet; else it moves up as before.
    const room = viewport.height - gutter - top;
    if (room >= 320 && room < height) height = room;
  }
  left = Math.max(gutter, Math.min(left, viewport.width - gutter - width));
  top = Math.max(topGutter, Math.min(top, viewport.height - gutter - height));
  return { left, top, width, maxHeight: height };
}

let current = null;

const MAP_TOKENS = [
  '--ws-bg',
  '--ws-panel',
  '--ws-panel-2',
  '--ws-line',
  '--ws-line-bright',
  '--ws-ink',
  '--ws-ink-dim',
  '--ws-ink-faint',
  '--ws-font-mono'
];

// The map defines its palette on its own root; a sheet mounted on <body> sits
// outside it, so the values are copied over. Missing values keep the CSS
// fallbacks.
function copyMapTokens(target, source) {
  if (!source || typeof globalThis.getComputedStyle !== 'function') return;
  let computed;
  try {
    computed = globalThis.getComputedStyle(source);
  } catch (_) {
    return;
  }
  for (const token of MAP_TOKENS) {
    const value = computed.getPropertyValue(token).trim();
    if (value) target.style.setProperty(token, value);
  }
}

function el(doc, tag, className, text) {
  const node = doc.createElement(tag);
  if (className) node.className = className;
  if (text != null) node.textContent = String(text);
  return node;
}

/**
 * Open the sheet for one Home. Returns { close } or null when one is already
 * open for that Home (it is focused instead). Options:
 *   homeID, homeName: the Home whose library is read
 *   anchor: the element that opened the sheet; focus returns to it
 *   beside: the element the sheet sits next to (the district); anchor by default
 *   tokensFrom: an element whose --ws-* custom properties the sheet copies, so
 *     a sheet mounted on <body> still wears the map's light or dark palette
 *   shelfRoute: the dashboard suggestions-shelf route for "needs the dashboard"
 *   onDone({ workspaceIds }): called after a run that set up at least one project
 *   client: a library client (createLibraryClient by default)
 *   doc: the document (globalThis.document by default)
 */
export function openLibrarySheet({
  homeID,
  homeName = 'this Home',
  anchor = null,
  beside = null,
  tokensFrom = null,
  shelfRoute = '',
  onDone = () => {},
  client = null,
  doc = globalThis.document,
  fetchImpl = undefined
} = {}) {
  if (!doc || !homeID) return null;
  if (current) {
    if (current.homeID === homeID) {
      current.focus();
      return null;
    }
    current.close({ restoreFocus: false });
  }
  const api = client || createLibraryClient(homeID, fetchImpl);
  const state = {
    phase: 'loading', // loading | list | confirm | running | failed
    rows: [],
    chosen: {},
    ticked: new Set(),
    progress: {},
    message: '',
    created: [],
    // The Home's plan for the assistant, read with the rows (inert), and the
    // person's choice on the confirmation.
    staffingPlan: null,
    staffingChoice: 'staff',
    // Connected rows: each one's assistant (inert reads) and the ones the
    // person ticked to get the shared assistant (never ticked by default).
    rowStaffing: {},
    repair: new Set(),
    // The last run, kept so its results stay on screen (and can be retried),
    // and the workspace page each result row links to.
    lastRun: null,
    routes: {}
  };

  const root = el(doc, 'div', SHEET_CLASS);
  root.setAttribute('role', 'dialog');
  root.setAttribute('aria-modal', 'false');
  root.setAttribute('data-library-sheet', String(homeID));
  const titleId = `wsMapLibrarySheetTitle-${Math.random().toString(36).slice(2, 8)}`;
  root.setAttribute('aria-labelledby', titleId);
  root.tabIndex = -1;
  copyMapTokens(root, tokensFrom || (anchor && anchor.closest ? anchor.closest('.ws-map') : null));

  const header = el(doc, 'div', `${SHEET_CLASS}-header`);
  const titleWrap = el(doc, 'div');
  const title = el(doc, 'h2', `${SHEET_CLASS}-title`, `Set up projects from ${homeName}`);
  title.id = titleId;
  const subtitle = el(doc, 'p', `${SHEET_CLASS}-subtitle`, 'Reading the last scan…');
  subtitle.setAttribute('aria-live', 'polite');
  titleWrap.append(title, subtitle);
  const closeButton = el(doc, 'button', `${SHEET_CLASS}-close`, '×');
  closeButton.type = 'button';
  closeButton.setAttribute('aria-label', 'Close');
  header.append(titleWrap, closeButton);

  const body = el(doc, 'div', `${SHEET_CLASS}-body`);
  const consent = el(doc, 'p', `${SHEET_CLASS}-consent`, CONSENT_LINE);
  const status = el(doc, 'p', `${SHEET_CLASS}-status`);
  status.setAttribute('role', 'status');
  status.setAttribute('aria-live', 'polite');
  const footer = el(doc, 'div', `${SHEET_CLASS}-footer`);
  root.append(header, body, consent, status, footer);

  function place() {
    const view = { width: globalThis.innerWidth || 1280, height: globalThis.innerHeight || 800 };
    // Beside the district (so it never covers the district's own header), at
    // the height of the control that opened it.
    // A control the page redrew while the sheet was open (a count that
    // changed after a run) is no longer on the page: it has no place, so the
    // sheet keeps to the one that still is.
    const live = node =>
      Boolean(node) &&
      typeof node.getBoundingClientRect === 'function' &&
      node.isConnected !== false;
    const near = live(beside) ? beside : live(anchor) ? anchor : null;
    const rect = near
      ? near.getBoundingClientRect()
      : { left: view.width / 2 - 220, right: view.width / 2 + 220, top: 80, bottom: 120 };
    const anchorRect = live(anchor) ? anchor.getBoundingClientRect() : rect;
    const size = { width: 440, height: 640 };
    const pos = sheetPosition(
      { left: rect.left, right: rect.right, top: anchorRect.top, bottom: rect.bottom },
      size,
      view
    );
    root.style.left = `${pos.left}px`;
    root.style.top = `${pos.top}px`;
    root.style.width = `${pos.width}px`;
    root.style.maxHeight = `${pos.maxHeight}px`;
  }

  function focus() {
    const first =
      root.querySelector('input:not([disabled])') ||
      root.querySelector('button:not([disabled])') ||
      root;
    first.focus();
  }

  function close({ restoreFocus = true } = {}) {
    if (current !== handle) return;
    doc.removeEventListener('keydown', onKeydown, true);
    root.remove();
    current = null;
    if (restoreFocus && anchor && typeof anchor.focus === 'function' && anchor.isConnected)
      anchor.focus();
  }

  function onKeydown(event) {
    if (event.key !== 'Escape') return;
    if (!root.contains(doc.activeElement) && doc.activeElement !== doc.body) return;
    event.preventDefault();
    event.stopPropagation();
    close();
  }

  function renderRows(view) {
    const list = el(doc, 'ul', `${SHEET_CLASS}-rows`);
    for (const item of view.items) {
      const row = el(doc, 'li', `${SHEET_CLASS}-row is-${item.kind}`);
      if (item.progress) row.classList.add(`is-${item.progress.status}`);
      if (item.kind === SETUP_KINDS.SETUP || item.kind === SETUP_KINDS.FILE_CHOICE) {
        const label = el(doc, 'label', `${SHEET_CLASS}-pick`);
        const box = el(doc, 'input');
        box.type = 'checkbox';
        box.checked = item.checked;
        box.disabled = !item.checkable;
        box.setAttribute('data-sheet-row', item.id);
        box.addEventListener('change', () => {
          if (box.checked) state.ticked.add(item.id);
          else state.ticked.delete(item.id);
          if (box.checked && item.kind === SETUP_KINDS.FILE_CHOICE && !state.chosen[item.id]) {
            // Ticking a file-choice row without a file: keep the tick, ask for
            // the file; the button label only counts rows with a file.
            status.textContent = `Choose the project file for ${item.name} first.`;
          }
          render();
        });
        const name = el(doc, 'span', `${SHEET_CLASS}-name`, item.name);
        label.append(box, name);
        if (item.kind === SETUP_KINDS.SETUP) {
          label.append(el(doc, 'span', `${SHEET_CLASS}-file`, item.file));
        } else {
          label.append(
            el(doc, 'span', `${SHEET_CLASS}-state is-choice`, setupLabel(SETUP_KINDS.FILE_CHOICE))
          );
        }
        row.append(label);
        if (item.kind === SETUP_KINDS.FILE_CHOICE) {
          const choices = el(doc, 'div', `${SHEET_CLASS}-choices`);
          choices.setAttribute('role', 'radiogroup');
          choices.setAttribute('aria-label', `Project file for ${item.name}`);
          for (const file of item.files) {
            const choice = el(doc, 'label', `${SHEET_CLASS}-choice`);
            const radio = el(doc, 'input');
            radio.type = 'radio';
            radio.name = `sheet-file-${item.id}`;
            radio.value = file;
            radio.checked = item.file === file;
            radio.disabled = !item.checkable;
            radio.addEventListener('change', () => {
              state.chosen[item.id] = file;
              state.ticked.add(item.id);
              status.textContent = '';
              render();
            });
            choice.append(radio, el(doc, 'span', null, file));
            choices.append(choice);
          }
          row.append(choices);
        }
        if (item.progress) {
          row.append(el(doc, 'span', `${SHEET_CLASS}-progress`, progressLine(item.progress)));
        }
      } else if (item.kind === SETUP_KINDS.CONNECTED) {
        const assistant = item.assistant || { known: false };
        const where = assistant.line ? `on the map · ${assistant.line}` : 'on the map';
        if (assistant.repairable) {
          // Already on the map without its assistant: offered, never ticked by
          // default, and never connected again.
          const label = el(doc, 'label', `${SHEET_CLASS}-pick`);
          const box = el(doc, 'input');
          box.type = 'checkbox';
          box.checked = item.repairChecked;
          box.disabled = state.phase === 'running' || !!item.progress;
          box.setAttribute('data-sheet-repair', item.id);
          box.setAttribute('aria-label', `Add the shared assistant to ${item.name}`);
          box.addEventListener('change', () => {
            if (box.checked) state.repair.add(item.id);
            else state.repair.delete(item.id);
            render();
            root.querySelector(`[data-sheet-repair="${CSS.escape(item.id)}"]`)?.focus();
          });
          label.append(
            box,
            el(doc, 'span', `${SHEET_CLASS}-name`, item.name),
            el(doc, 'span', `${SHEET_CLASS}-state is-connected`, where)
          );
          row.append(label);
        } else {
          row.append(
            el(doc, 'span', `${SHEET_CLASS}-name`, item.name),
            el(doc, 'span', `${SHEET_CLASS}-state is-connected`, where)
          );
        }
        if (assistant.blocked) {
          const why = el(doc, 'span', `${SHEET_CLASS}-hint`, assistant.blocked);
          row.append(why);
          if (shelfRoute) {
            const link = el(doc, 'a', `${SHEET_CLASS}-link`, 'review sharing');
            link.href = shelfRoute;
            row.append(link);
          }
        }
        if (item.progress) {
          row.append(el(doc, 'span', `${SHEET_CLASS}-progress`, progressLine(item.progress)));
        }
      } else {
        row.append(el(doc, 'span', `${SHEET_CLASS}-name`, item.name));
        const why = el(doc, 'span', `${SHEET_CLASS}-state is-dashboard`, item.reason);
        why.title = item.reason;
        row.append(why);
        if (shelfRoute) {
          const link = el(doc, 'a', `${SHEET_CLASS}-link`, 'open the shelf');
          link.href = shelfRoute;
          row.append(link);
        }
      }
      list.append(row);
    }
    return list;
  }

  // The assistant part of the confirmation: what it does (or why it cannot),
  // and a plain choice to only put the projects on the map.
  function renderStaffingChoice(staffing) {
    const box = el(doc, 'div', `${SHEET_CLASS}-staffing`);
    box.setAttribute('data-sheet-staffing', staffing.canStaff ? 'available' : 'unavailable');
    if (staffing.canStaff) {
      const group = el(doc, 'div', `${SHEET_CLASS}-choices`);
      group.setAttribute('role', 'radiogroup');
      group.setAttribute('aria-label', 'Assistant');
      const options = [
        ['staff', staffing.staffLabel, staffing.staffLine],
        ['placement', staffing.placementLabel, staffing.placementLine]
      ];
      for (const [value, label, hint] of options) {
        const choice = el(doc, 'label', `${SHEET_CLASS}-choice`);
        const radio = el(doc, 'input');
        radio.type = 'radio';
        radio.name = `sheet-staffing-${homeID}`;
        radio.value = value;
        radio.checked = (value === 'staff') === staffing.staff;
        radio.setAttribute('data-sheet-staffing-choice', value);
        radio.addEventListener('change', () => {
          state.staffingChoice = value;
          render();
          root.querySelector(`[data-sheet-staffing-choice="${value}"]`)?.focus();
        });
        const text = el(doc, 'span', null, label);
        choice.append(radio, text);
        if (hint) choice.append(el(doc, 'span', `${SHEET_CLASS}-hint`, hint));
        group.append(choice);
      }
      box.append(group);
    }
    box.append(el(doc, 'p', `${SHEET_CLASS}-hint`, staffing.note));
    return box;
  }

  // The repair of projects already on the map: re-read the plan, then the same
  // finish per project, in order, stopping at the first that fails. Nothing is
  // connected again.
  async function confirmRepair(items, staffing) {
    state.lastRun = null;
    const fresh = await loadStaffingPlan(api);
    const again = confirmStaffing(fresh, items.length, 'staff');
    if (!again.staff || again.teamDigest !== staffing.teamDigest) {
      state.staffingPlan = fresh;
      status.textContent = 'What adding the assistant does changed. Review it and confirm again.';
      render();
      focus();
      return;
    }
    await execute(
      items.map(item => ({ id: item.id, name: item.name, file: '' })),
      { teamDigest: staffing.teamDigest },
      'repair'
    );
  }

  // One run, then its retained results (never an automatic close). The item
  // objects are kept: an explicit retry re-sends the same staffing request ids.
  // Closing the sheet stops what has not started; nothing is rolled back.
  async function execute(items, staffing, kind) {
    state.phase = 'running';
    state.progress = {};
    status.textContent = '';
    render();
    const result = await runSetup({
      client: api,
      items,
      staffing,
      shouldContinue: () => current === handle,
      onProgress: (item, phase, detail) => {
        state.progress[item.id] = {
          status: phase,
          stage: detail?.stage || '',
          staffing: detail?.staffing || null,
          message: detail?.message || ''
        };
        if (current === handle) render();
      }
    });
    // A project connected before its assistant failed is on the map: the map
    // and member lists show it, but it is not reported as fully set up.
    const connected = result.done.map(item => item.workspaceId);
    if (result.failed?.workspaceId) connected.push(result.failed.workspaceId);
    state.created.push(...connected.filter(Boolean));
    for (const item of result.done) {
      state.ticked.delete(item.id);
      state.repair.delete(item.id);
    }
    if (result.failed?.workspaceId) state.ticked.delete(result.failed.id);
    if (state.created.length) onDone({ workspaceIds: state.created.slice() });
    if (current !== handle) return; // closed while running: nothing more to show
    const previous = state.lastRun;
    // A retry continues the same confirmation: keep the rows it already showed.
    const run =
      previous && previous.retrying
        ? mergeRuns(previous, { items, result, staffing, kind })
        : { items, result, staffing, kind };
    state.lastRun = run;
    state.message =
      kind === 'repair'
        ? summarizeRepair(run.result, state.staffingPlan)
        : summarizeRun(run.result, staffing, state.staffingPlan);
    state.progress = {};
    state.phase = 'results';
    status.textContent = state.message;
    render();
    focus();
    void resolveRoutes(resultRows(run));
  }

  // The retry of the row that stopped, and the rows after it, under the same
  // confirmation and request ids.
  async function retry() {
    const run = state.lastRun;
    if (!run?.result?.failed) return;
    const start = run.items.findIndex(item => item.id === run.result.failed.id);
    if (start < 0) return;
    run.retrying = true;
    await execute(run.items.slice(start), run.staffing, run.kind);
  }

  // Each result row links to its workspace page, which is addressed by slug:
  // read it (nothing else) once the results show.
  async function resolveRoutes(rows) {
    const fetcher = fetchImpl || ((...args) => globalThis.fetch(...args));
    for (const row of rows) {
      if (!row.workspaceId || state.routes[row.workspaceId]) continue;
      try {
        const response = await fetcher(`/api/workspaces/${encodeURIComponent(row.workspaceId)}`, {
          headers: { Accept: 'application/json' }
        });
        const ws = response.ok ? await response.json() : null;
        if (ws?.folder_slug)
          state.routes[row.workspaceId] = `/workspaces/${encodeURIComponent(ws.folder_slug)}`;
      } catch (_) {
        // No link; the row still says what happened.
      }
    }
    if (current === handle && state.phase === 'results') render();
  }

  // Where each kind of recovery lives. None of them changes anything by being
  // opened; the person decides there.
  function recoveryHref(kind) {
    if (kind === 'settings') return '/settings';
    if (kind === 'agents') return '/agents';
    if (kind === 'sharing' && shelfRoute)
      return shelfRoute.replace(/#.*$/, '') + '#projectLibraryPanel';
    return '';
  }

  // The retained results: one row per confirmed project with its connection
  // and its assistant as separate facts, a link to the workspace, and the way
  // to settle anything that stopped.
  function renderResults(rows) {
    const list = el(doc, 'ul', `${SHEET_CLASS}-results`);
    list.setAttribute('data-sheet-results', '');
    for (const result of rows) {
      const row = el(doc, 'li', `${SHEET_CLASS}-result`);
      row.setAttribute('data-sheet-result', result.id);
      if (result.complete) row.classList.add('is-complete');
      const head = el(doc, 'div', `${SHEET_CLASS}-result-head`);
      head.append(el(doc, 'span', `${SHEET_CLASS}-name`, result.name));
      const route = result.workspaceId ? state.routes[result.workspaceId] : '';
      if (route) {
        const open = el(doc, 'a', `${SHEET_CLASS}-link`, 'Open');
        open.href = route;
        open.setAttribute('aria-label', `Open ${result.name}`);
        head.append(open);
      }
      row.append(head, el(doc, 'span', `${SHEET_CLASS}-hint`, result.connection));
      if (result.assistant) row.append(el(doc, 'span', `${SHEET_CLASS}-hint`, result.assistant));
      if (result.recovery) {
        const href = recoveryHref(result.recovery.kind);
        if (href && result.recovery.label) {
          const fix = el(doc, 'a', `${SHEET_CLASS}-link`, result.recovery.label);
          fix.href = href;
          row.append(fix);
        } else if (result.recovery.kind === 'reload') {
          const review = el(doc, 'button', `${SHEET_CLASS}-link`, result.recovery.label);
          review.type = 'button';
          review.addEventListener('click', () => {
            state.lastRun = null;
            void load().then(() => focus());
          });
          row.append(review);
        }
      }
      list.append(row);
    }
    return list;
  }

  // Confirmed: re-read the assistant plan first. A plan that changed since the
  // person looked is shown again instead of being run (and a placement-only
  // confirmation never gains an assistant just because one became available).
  async function confirmRun(selected, staffing) {
    if (staffing.staff) {
      const fresh = await loadStaffingPlan(api);
      const again = confirmStaffing(fresh, selected.length, state.staffingChoice);
      if (
        !again.staff ||
        again.teamDigest !== staffing.teamDigest ||
        fresh.plan !== state.staffingPlan?.plan
      ) {
        state.staffingPlan = fresh;
        status.textContent =
          'What setup does about the assistant changed. Review it and confirm again.';
        render();
        focus();
        return;
      }
    }
    await run(selected, staffing.staff ? { teamDigest: staffing.teamDigest } : null);
  }

  function render() {
    const view = sheetView(state.rows, state);
    body.replaceChildren();
    footer.replaceChildren();
    consent.hidden = state.phase === 'loading';
    if (state.phase === 'loading') {
      subtitle.textContent = 'Reading the last scan…';
      body.append(el(doc, 'p', `${SHEET_CLASS}-empty`, 'One moment.'));
    } else if (state.phase === 'confirm') {
      subtitle.textContent = view.subtitle;
      const intro = el(
        doc,
        'p',
        `${SHEET_CLASS}-confirm-intro`,
        `Set up ${view.selected.length} ${
          view.selected.length === 1 ? 'project' : 'projects'
        }${view.selected.length === 1 ? ' as its own workspace' : ', each as its own workspace'} in ${homeName}:`
      );
      const lines = el(doc, 'ul', `${SHEET_CLASS}-confirm`);
      for (const line of confirmLines(view.selected)) lines.append(el(doc, 'li', null, line));
      body.append(intro, lines);
      const staffing = confirmStaffing(
        state.staffingPlan,
        view.selected.length,
        state.staffingChoice
      );
      body.append(renderStaffingChoice(staffing));
      const go = el(doc, 'button', 'modern-btn modern-btn-primary', staffing.buttonLabel);
      go.type = 'button';
      go.addEventListener('click', () => void confirmRun(view.selected, staffing));
      const back = el(doc, 'button', 'modern-btn modern-btn-secondary', 'Back');
      back.type = 'button';
      back.addEventListener('click', () => {
        state.phase = 'list';
        render();
        focus();
      });
      footer.append(go, back);
    } else if (state.phase === 'results' && state.lastRun) {
      subtitle.textContent = 'What setup did';
      body.append(renderResults(resultRows(state.lastRun)));
      const failed = state.lastRun.result.failed;
      const recovery =
        failed && failed.stage === 'staffing'
          ? staffingRecovery(failed.reason)
          : failed
            ? { kind: 'retry' }
            : null;
      if (recovery?.kind === 'retry') {
        const again = el(
          doc,
          'button',
          'modern-btn modern-btn-primary',
          `Try ${failed.name} again`
        );
        again.type = 'button';
        again.setAttribute('data-sheet-retry', '');
        again.addEventListener('click', () => void retry());
        footer.append(again);
      }
      const back = el(doc, 'button', 'modern-btn modern-btn-secondary', 'Back to the list');
      back.type = 'button';
      back.addEventListener('click', () => {
        state.lastRun = null;
        void load().then(() => focus());
      });
      const done = el(doc, 'button', 'modern-btn modern-btn-secondary', 'Close');
      done.type = 'button';
      done.addEventListener('click', () => close());
      footer.append(back, done);
    } else if (state.phase === 'repair') {
      subtitle.textContent = view.subtitle;
      const count = view.repairing.length;
      body.append(
        el(
          doc,
          'p',
          `${SHEET_CLASS}-confirm-intro`,
          `Add the shared assistant to ${count} ${count === 1 ? 'project' : 'projects'} already on the map:`
        )
      );
      const lines = el(doc, 'ul', `${SHEET_CLASS}-confirm`);
      for (const item of view.repairing) lines.append(el(doc, 'li', null, item.name));
      body.append(lines);
      const staffing = confirmStaffing(state.staffingPlan, count, 'staff');
      const box = el(doc, 'div', `${SHEET_CLASS}-staffing`);
      box.setAttribute('data-sheet-staffing', staffing.canStaff ? 'available' : 'unavailable');
      if (staffing.canStaff) box.append(el(doc, 'p', `${SHEET_CLASS}-hint`, staffing.staffLine));
      box.append(
        el(
          doc,
          'p',
          `${SHEET_CLASS}-hint`,
          staffing.canStaff
            ? `${staffing.note} Nothing is connected again and no other project changes.`
            : staffing.note
        )
      );
      body.append(box);
      const go = el(doc, 'button', 'modern-btn modern-btn-primary', 'Add the assistant');
      go.type = 'button';
      go.disabled = !staffing.canStaff || count === 0;
      go.addEventListener('click', () => void confirmRepair(view.repairing, staffing));
      const back = el(doc, 'button', 'modern-btn modern-btn-secondary', 'Back');
      back.type = 'button';
      back.addEventListener('click', () => {
        state.phase = 'list';
        render();
        focus();
      });
      footer.append(go, back);
    } else {
      subtitle.textContent = view.subtitle;
      if (!view.items.length) {
        body.append(
          el(doc, 'p', `${SHEET_CLASS}-empty`, 'The last scan found no projects to set up.')
        );
      } else {
        body.append(renderRows(view));
      }
      const go = el(doc, 'button', 'modern-btn modern-btn-primary', view.buttonLabel);
      go.type = 'button';
      go.disabled = view.buttonDisabled || state.phase === 'running';
      go.addEventListener('click', () => {
        state.phase = 'confirm';
        // The last run's status line is about that run, not this confirmation.
        state.message = '';
        status.textContent = '';
        render();
        focus();
      });
      const cancel = el(doc, 'button', 'modern-btn modern-btn-secondary', 'Cancel');
      cancel.type = 'button';
      cancel.disabled = state.phase === 'running';
      cancel.addEventListener('click', () => close());
      // With only projects already on the map ticked, a disabled "Put 0 on the
      // map" beside the real action is just noise.
      const nothingToPlace =
        (view.repairing.length && view.selected.length === 0) ||
        view.allConnected ||
        state.phase === 'running';
      if (!nothingToPlace) footer.append(go);
      if (view.repairing.length) {
        const repair = el(doc, 'button', 'modern-btn modern-btn-secondary', view.repairLabel);
        repair.type = 'button';
        repair.disabled = state.phase === 'running';
        repair.setAttribute('data-sheet-repair-go', '');
        repair.addEventListener('click', () => {
          state.phase = 'repair';
          state.message = '';
          status.textContent = '';
          render();
          focus();
        });
        footer.append(repair);
      }
      footer.append(cancel);
      if (shelfRoute) {
        const link = el(doc, 'a', `${SHEET_CLASS}-shelf`, 'Open the project shelf');
        link.href = shelfRoute;
        footer.append(link);
      }
    }
    place();
  }

  async function load() {
    state.phase = 'loading';
    render();
    try {
      const [result, plan] = await Promise.all([loadSetupRows(api), loadStaffingPlan(api)]);
      state.rows = result.rows;
      state.staffingPlan = plan;
      state.rowStaffing = await loadConnectedStaffing(api, state.rows);
      for (const id of [...state.repair]) {
        if (!assistantState(state.rowStaffing[id]).repairable) state.repair.delete(id);
      }
      if (result.readOnly) {
        state.message = 'This Home is read-only; projects can only be viewed here.';
      }
      // Keep ticks the person already made (a reload after a run); tick the
      // single-file rows the first time.
      state.ticked = state.ticked.size ? state.ticked : defaultTicked(state.rows);
      for (const id of [...state.ticked]) {
        if (!state.rows.some(row => row.id === id && row.kind !== SETUP_KINDS.CONNECTED))
          state.ticked.delete(id);
      }
      state.phase = 'list';
    } catch (error) {
      state.rows = [];
      state.phase = 'list';
      state.message = error?.message || 'The library could not be read.';
    }
    status.textContent = state.message;
    render();
  }

  async function run(selected, staffing = null) {
    state.lastRun = null;
    await execute(
      selected.map(item => ({ id: item.id, name: item.name, file: item.file })),
      staffing,
      'setup'
    );
  }

  const handle = { homeID, close, focus, root };
  current = handle;
  closeButton.addEventListener('click', () => close());
  doc.addEventListener('keydown', onKeydown, true);
  doc.body.append(root);
  render();
  focus();
  void load().then(() => focus());
  return handle;
}

/** For tests and the cockpit: the sheet currently open, if any. */
export function currentLibrarySheet() {
  return current;
}
