// Connect your second brain — the assistant's first message after Personal HQ.
//
// The assistant asks for the user's notes (naming the Obsidian vault it found,
// if any), shows what reading them means before anything is read, connects the
// folder read-only to the HQ, and comes back with one headline and one action:
// add the open items as HQ follow-ups. This module owns that one turn in the
// Home assistant drawer's conversation. It never sends a filesystem location:
// a detected vault is named by its id, any other folder by the selection token
// the folder-picker endpoint issued for it.

import {
  createFolderPathEntry,
  folderDialogOffNote,
  FOLDER_PATH_ENDPOINT
} from './folder-path-entry.js';

export const SECOND_BRAIN_ENDPOINT = '/api/personal-assistant/second-brain';
const SYSTEM_MODEL_ENDPOINT = '/api/settings/system-model';
const LOOK_POLL_MS = 1000;
// A look reads within five seconds and its headline model has 45 more; the
// poll gives up after the server's own bound and says so, so the turn never
// hangs.
const LOOK_POLL_LIMIT = 75;

// secondBrainAvailable: the ask belongs to an active relationship only. A
// paused assistant does not start a conversation.
export function secondBrainAvailable(personalAssistant) {
  return String(personalAssistant?.state || '').trim() === 'active';
}

// secondBrainAskView decides whether the assistant opens with the ask and what
// it says (FR5–FR7). It is shown once: only while nothing is connected and the
// ask has never been shown, answered or tombstoned.
export function secondBrainAskView(secondBrain, available) {
  const show =
    available === true &&
    secondBrain?.status === 'not_connected' &&
    secondBrain?.ask?.state === 'pending';
  const vault = Array.isArray(secondBrain?.detected) ? secondBrain.detected[0] : null;
  if (vault?.id && vault?.name) {
    return {
      show,
      line: `Do you keep a second brain? I found your Obsidian vault "${vault.name}". If you let me read it, I'll tell you what's open.`,
      actions: [
        { id: 'read', label: `Read ${vault.name}`, style: 'primary', vaultId: vault.id },
        { id: 'choose', label: 'Another folder…', style: 'outline' },
        { id: 'later', label: 'Not now', style: 'link' },
        { id: 'none', label: "I don't keep notes", style: 'link' }
      ]
    };
  }
  return {
    show,
    line: "Do you keep a second brain, notes in Obsidian or a folder of Markdown? Point me at it and I'll tell you what's open.",
    actions: [
      { id: 'choose', label: 'Choose folder…', style: 'primary' },
      { id: 'later', label: 'Not now', style: 'link' },
      { id: 'none', label: "I don't keep notes", style: 'link' }
    ]
  };
}

// secondBrainAnsweredLine is the assistant's short reply to a decline. It is
// the end of the turn: nothing follows it unprompted (FR8, FR9).
export function secondBrainAnsweredLine(decision) {
  return decision === 'none'
    ? "Got it. I won't ask about notes again."
    : "Okay. Connect your notes from Home whenever you're ready.";
}

// secondBrainChooserView lists the detected vaults (name and the folder they
// sit in; the full path is detail, never a button label) and the other ways to
// name a folder (FR12).
export function secondBrainChooserView(secondBrain, { pickerOff = '', message = '' } = {}) {
  const vaults = (Array.isArray(secondBrain?.detected) ? secondBrain.detected : [])
    .filter(vault => vault?.id && vault?.name)
    .map(vault => ({
      id: vault.id,
      label: vault.name,
      detail: vault.parent ? `in ${vault.parent}` : '',
      path: String(vault.path || '')
    }));
  return {
    title: vaults.length ? 'Which notes should I read?' : 'Where do you keep your notes?',
    vaults,
    pickLabel: 'Pick another folder…',
    pickerOff: String(pickerOff || ''),
    message: String(message || '')
  };
}

function machineName(platform) {
  return /mac/i.test(String(platform || '')) ? 'your Mac' : 'this computer';
}

// secondBrainConsentView says, before anything is read, which folder, that it
// is read-only, what is read, what leaves the machine and what Ori keeps
// (FR14). It names the model the summary goes to, or says there is none.
export function secondBrainConsentView(chosen, model, platform = '') {
  const name = String(chosen?.name || 'this folder');
  const configured = model?.configured === true && model?.provider && model?.model;
  return {
    title: `Read ${name}?`,
    path: String(chosen?.path || ''),
    lines: [
      'Read-only. Nothing is moved, edited, or deleted.',
      'Ori reads the Markdown notes in it: up to 300 of the most recent, none over 256 KB.',
      configured
        ? `Short excerpts go to your model, ${model.provider}/${model.model}, to write the summary. Nothing else leaves ${machineName(platform)}.`
        : 'No model is set, so the summary is counts only.',
      'Ori keeps the folder path and the list of open items in Personal HQ, not your notes.'
    ],
    actions: [
      { id: 'connect', label: 'Connect and look', style: 'primary' },
      { id: 'back', label: 'Back', style: 'outline' },
      { id: 'later', label: 'Not now', style: 'link' }
    ]
  };
}

// suggestedLabel is the primary action's label, naming how many items it adds.
export function suggestedLabel(count) {
  const n = Number(count) || 0;
  return n === 1 ? 'Add 1 as a follow-up' : `Add ${n} as follow-ups`;
}

// secondBrainItemGroups groups a look's items by note, for Show everything.
// Suggested items start checked. For an Obsidian vault each note's heading is
// the obsidian:// link the server built (FR26); a plain folder shows the
// relative path as text. Ori never opens the app itself.
export function secondBrainItemGroups(items) {
  const groups = [];
  const byNote = new Map();
  for (const item of Array.isArray(items) ? items : []) {
    if (!item?.id || !item?.text) continue;
    const note = String(item.note || '');
    if (!byNote.has(note)) {
      const link = String(item.link || '').startsWith('obsidian://open?') ? item.link : '';
      const group = { note, label: note.replace(/\.(md|markdown)$/i, ''), link, items: [] };
      byNote.set(note, group);
      groups.push(group);
    }
    byNote.get(note).items.push({
      id: item.id,
      text: item.text,
      due: String(item.due || ''),
      checked: item.suggested === true
    });
  }
  return groups;
}

// secondBrainLookView is the result card (FR23–FR27): one headline and one
// action, everything else behind Show everything. A failed read is one
// sentence and Choose again. It never asks the user a question.
export function secondBrainLookView(look, { name = '', acted = false } = {}) {
  const state = String(look?.state || '');
  const result = look?.result || null;
  if (state === 'running') {
    return { state, line: `Reading ${name || 'your notes'}…` };
  }
  if (state === 'failed') {
    return {
      state,
      line: String(result?.failure || `Ori couldn't read ${name || 'that folder'}.`),
      actions: [{ id: 'choose', label: 'Choose again', style: 'primary' }]
    };
  }
  if (state !== 'finished' || !result) return { state: 'none' };
  const items = Array.isArray(result.items) ? result.items : [];
  const suggested = items.filter(item => item?.suggested === true).length;
  const changes = [];
  if (Number(result.new_since) > 0) changes.push(`${result.new_since} new`);
  if (Number(result.done_since) > 0) changes.push(`${result.done_since} done`);
  return {
    state,
    lookId: String(look.look_id || result.id || ''),
    headline: String(result.headline || ''),
    since: changes.length ? `Since your last look: ${changes.join(', ')}.` : '',
    alsoNoticed: String(result.also_noticed || ''),
    primary:
      suggested > 0 && !acted
        ? { id: 'add', label: suggestedLabel(suggested), style: 'primary' }
        : null,
    showAll: items.length ? `Show everything (${items.length})` : '',
    groups: secondBrainItemGroups(items),
    acted: acted === true,
    // Look again once this result has been acted on (FR36); the server
    // holds the rate limit.
    again: acted === true ? { id: 'again', label: 'Look again', style: 'link' } : null
  };
}

// followUpReceiptLine says what Add as follow-ups did.
export function followUpReceiptLine(receipt) {
  const created = Number(receipt?.created) || 0;
  const skipped = Number(receipt?.skipped) || 0;
  if (created === 0 && skipped > 0) return 'Those are already in your follow-ups.';
  const added =
    created === 1
      ? 'Added 1 follow-up to Personal HQ.'
      : `Added ${created} follow-ups to Personal HQ.`;
  if (skipped === 0) return added;
  return `${added} ${skipped === 1 ? '1 was' : `${skipped} were`} already there.`;
}

// sinceLabel says how long ago a moment was, the way a person would.
export function sinceLabel(at, now = Date.now()) {
  const then = Date.parse(at || '');
  if (!Number.isFinite(then)) return '';
  const minutes = Math.max(0, Math.round((Number(now) - then) / 60000));
  if (minutes < 1) return 'just now';
  if (minutes < 60) return minutes === 1 ? '1 minute ago' : `${minutes} minutes ago`;
  const hours = Math.round(minutes / 60);
  if (hours < 24) return hours === 1 ? '1 hour ago' : `${hours} hours ago`;
  const days = Math.round(hours / 24);
  return days === 1 ? 'yesterday' : `${days} days ago`;
}

// secondBrainManageView is the connection's own card, opened from the HQ's
// Notes station and the Profile (FR33, FR35, FR38): connected with Look again
// and Disconnect, or a folder Ori can no longer find with Choose again.
export function secondBrainManageView(secondBrain, now = Date.now()) {
  const source = secondBrain?.source;
  if (!source?.name || secondBrain?.status === 'not_connected') {
    return {
      state: 'not_connected',
      title: 'Your notes are not connected.',
      detail:
        'Point Ori at an Obsidian vault or a folder of Markdown and it tells you what is open.',
      actions: [{ id: 'choose', label: 'Choose folder…', style: 'primary' }]
    };
  }
  const kind = source.kind === 'obsidian' ? 'Obsidian' : 'Markdown folder';
  const disconnect = { id: 'disconnect', label: 'Disconnect', style: 'link' };
  if (secondBrain.status === 'missing') {
    return {
      state: 'missing',
      title: `Can't find ${source.name}`,
      detail: 'It may have been moved or renamed. Your follow-ups are kept.',
      actions: [{ id: 'choose', label: 'Choose again', style: 'primary' }, disconnect]
    };
  }
  const last = secondBrain.last_look;
  const since = last?.at ? sinceLabel(last.at, now) : '';
  return {
    state: 'connected',
    title: `Connected · ${source.name}`,
    detail: since ? `${kind} · last look ${since}` : `${kind} · not looked at yet`,
    actions: [{ id: 'again', label: 'Look again', style: 'primary' }, disconnect]
  };
}

// secondBrainQuestRequested says the mission card sent the user here.
export function secondBrainQuestRequested(search) {
  try {
    return new URLSearchParams(search || '').get('quest') === 'second-brain';
  } catch (_) {
    return false;
  }
}

// secondBrainError reads the one sentence from either error shape the server
// writes ({error} or {message}).
export function secondBrainError(payload, fallback = 'That did not work. Try again.') {
  return String(payload?.error || payload?.message || '').trim() || fallback;
}

// secondBrainPlacement says where the card shows: in the conversation while
// the user is in the turn, in Needs you when the page loaded onto a result
// still waiting for a decision (FR31), otherwise nowhere.
export function secondBrainPlacement({ available, phase, pinned } = {}) {
  if (available !== true || !phase || phase === 'idle') return 'none';
  return pinned === true ? 'needs' : 'thread';
}

export function newRequestID(prefix = 'sb') {
  const random =
    globalThis.crypto?.randomUUID?.() ||
    `${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}`;
  return `${prefix}-${random}`;
}

// ---------------------------------------------------------------------------
// The drawer turn. Everything below needs a document.

const state = {
  available: false,
  secondBrain: null,
  phase: 'idle', // ask | answered | chooser | consent | reading | result | failed
  pinned: false,
  chosen: null, // { detectedId | selectionToken, name, path }
  look: null,
  model: null,
  busy: false,
  message: '',
  error: '',
  receipt: '',
  decision: '',
  // answer replaces the decline line when the turn ends another way.
  answer: '',
  pickerOff: '',
  replace: false,
  loading: false,
  pollTimer: 0,
  polls: 0,
  focusNext: false,
  // The steps Back returns through: the ask, then the chooser.
  history: [],
  // latest places the card as the conversation's latest reply instead of
  // its first message.
  latest: false
};

let pathEntry = null;

function elements() {
  const root = document.getElementById('personalAssistantSecondBrain');
  if (!root) return null;
  return {
    root,
    body: document.getElementById('personalAssistantSecondBrainBody'),
    status: document.getElementById('personalAssistantSecondBrainStatus'),
    error: document.getElementById('personalAssistantSecondBrainError'),
    thread: document.getElementById('personalAssistantThread'),
    needs: document.getElementById('personalAssistantNeedsYouCards')
  };
}

async function readJSON(response) {
  try {
    return await response.json();
  } catch (_) {
    return null;
  }
}

async function request(path, { method = 'GET', body } = {}) {
  const init = { method, headers: { Accept: 'application/json' } };
  if (body !== undefined) {
    init.headers['Content-Type'] = 'application/json';
    init.body = JSON.stringify(body);
  }
  const response = await fetch(`${SECOND_BRAIN_ENDPOINT}${path}`, init);
  const payload = await readJSON(response);
  return { ok: response.ok, status: response.status, payload: payload || {} };
}

function node(tag, className, text) {
  const element = document.createElement(tag);
  if (className) element.className = className;
  if (text != null) element.textContent = String(text);
  return element;
}

function button(action, onClick) {
  const control = node('button', '', action.label);
  control.type = 'button';
  control.className =
    action.style === 'primary'
      ? 'btn btn-sm btn-primary'
      : action.style === 'outline'
        ? 'btn btn-sm btn-outline-secondary'
        : 'btn btn-sm btn-link';
  control.dataset.secondBrainAction = action.id;
  control.disabled = state.busy;
  control.addEventListener('click', () => onClick(action));
  return control;
}

function actionRow(actions, onClick) {
  const row = node('div', 'pa-specialist-offer__actions');
  actions.forEach(action => row.append(button(action, onClick)));
  return row;
}

// go moves forward one step, remembering where Back returns to.
function go(phase) {
  if (
    state.phase !== phase &&
    (state.phase === 'ask' || state.phase === 'chooser' || state.phase === 'manage')
  )
    state.history.push(state.phase);
  setPhase(phase);
}

function back() {
  setPhase(state.history.pop() || 'idle');
}

function setPhase(phase, { focus = true } = {}) {
  if (phase === 'idle' || phase === 'reading') state.history = [];
  state.phase = phase;
  state.focusNext = focus;
  render();
}

function renderAsk(body) {
  const view = secondBrainAskView(state.secondBrain, true);
  body.append(node('p', 'pa-specialist-offer__headline', view.line));
  body.append(actionRow(view.actions, onAskAction));
}

function renderAnswered(body) {
  const line = state.answer || secondBrainAnsweredLine(state.decision);
  body.append(node('p', 'pa-specialist-offer__headline', line));
}

function renderManage(body) {
  const view = secondBrainManageView(state.secondBrain);
  body.append(node('p', 'pa-specialist-offer__headline', view.title));
  if (view.detail) body.append(node('p', 'pa-folder__note', view.detail));
  if (state.secondBrain?.source?.path) {
    body.append(node('p', 'pa-second-brain__path', state.secondBrain.source.path));
  }
  body.append(
    actionRow(view.actions, action => {
      if (action.id === 'again') void lookAgain();
      else if (action.id === 'disconnect') void disconnect();
      else {
        state.message = '';
        go('chooser');
      }
    })
  );
}

function renderChooser(body) {
  const view = secondBrainChooserView(state.secondBrain, {
    pickerOff: state.pickerOff,
    message: state.message
  });
  body.append(node('p', 'pa-specialist-offer__headline', view.title));
  if (view.message) body.append(node('p', 'pa-folder__note', view.message));
  if (view.vaults.length) {
    const list = node('ul', 'pa-second-brain__vaults');
    list.setAttribute('aria-label', 'Obsidian vaults');
    view.vaults.forEach(vault => {
      const item = node('li', 'pa-second-brain__vault');
      const choose = button({ id: 'vault', label: vault.label, style: 'outline' }, () =>
        chooseFolder({ detectedId: vault.id, name: vault.label, path: vault.path })
      );
      item.append(choose);
      if (vault.detail) {
        const detail = node('span', 'pa-second-brain__vault-detail', vault.detail);
        detail.title = vault.path;
        item.append(detail);
      }
      list.append(item);
    });
    body.append(list);
  }
  if (!view.pickerOff) {
    body.append(
      actionRow([{ id: 'pick', label: view.pickLabel, style: 'outline' }], () => void pickFolder())
    );
  }
  if (!pathEntry) {
    pathEntry = createFolderPathEntry({
      title: 'Choose your notes folder',
      onSelect: selection =>
        chooseFolder({
          selectionToken: selection.selectionToken,
          name: baseName(selection.path),
          path: selection.path
        })
    });
  }
  pathEntry.setNote(view.pickerOff);
  pathEntry.setDisabled(state.busy);
  body.append(pathEntry.element);
  body.append(actionRow([{ id: 'back', label: 'Back', style: 'link' }], back));
}

function renderConsent(body) {
  const view = secondBrainConsentView(state.chosen, state.model, globalThis.navigator?.platform);
  body.append(node('p', 'pa-specialist-offer__headline', view.title));
  if (view.path) body.append(node('p', 'pa-second-brain__path', view.path));
  const list = node('ul', 'pa-folder__receipt pa-second-brain__consent');
  list.dataset.layout = 'list';
  view.lines.forEach(line => list.append(node('li', '', line)));
  body.append(list);
  if (state.replace) {
    body.append(
      node(
        'p',
        'pa-folder__note',
        'Another notes folder is connected. Connecting this one replaces it.'
      )
    );
  }
  body.append(
    actionRow(view.actions, action => {
      if (action.id === 'connect') void connect();
      else if (action.id === 'back') back();
      else void decide('later');
    })
  );
}

function renderLook(body) {
  const view = secondBrainLookView(state.look, {
    name: state.chosen?.name || state.secondBrain?.source?.name || '',
    acted: Boolean(state.receipt) || state.secondBrain?.last_look?.acted === true
  });
  if (view.state === 'running') {
    const line = node('p', 'pa-specialist-offer__headline', view.line);
    line.setAttribute('role', 'status');
    body.append(line);
    return;
  }
  if (view.state === 'failed') {
    body.append(node('p', 'pa-specialist-offer__headline', view.line));
    body.append(actionRow(view.actions, () => setPhase('chooser')));
    return;
  }
  if (view.state !== 'finished') return;
  body.append(node('p', 'pa-specialist-offer__headline', view.headline));
  if (view.since) body.append(node('p', 'pa-folder__note', view.since));
  if (view.alsoNoticed)
    body.append(node('p', 'pa-folder__note pa-second-brain__noticed', view.alsoNoticed));
  if (state.receipt) body.append(node('p', 'pa-folder__first-look', state.receipt));
  const actions = [];
  if (view.primary) actions.push(view.primary);
  if (state.pinned && !state.receipt)
    actions.push({ id: 'dismiss', label: 'Not now', style: 'link' });
  if (view.again) actions.push(view.again);
  if (actions.length) {
    body.append(
      actionRow(actions, action => {
        if (action.id === 'add') void addFollowUps('suggested');
        else if (action.id === 'again') void lookAgain();
        else void dismiss();
      })
    );
  }
  if (view.showAll) body.append(renderEverything(view));
}

function renderEverything(view) {
  const details = node('details', 'pa-folder__why pa-second-brain__everything');
  details.append(node('summary', '', view.showAll));
  const form = node('div', 'pa-second-brain__groups');
  view.groups.forEach(group => {
    const fieldset = node('fieldset', 'pa-second-brain__group');
    const legend = node('legend', 'pa-second-brain__note');
    if (group.link) {
      const open = node('a', 'pa-second-brain__open', group.label);
      open.href = group.link;
      open.title = `Open ${group.label} in Obsidian`;
      legend.append(open);
    } else {
      legend.textContent = group.label;
    }
    fieldset.append(legend);
    group.items.forEach(item => {
      const label = node('label', 'pa-second-brain__item');
      const box = node('input');
      box.type = 'checkbox';
      box.value = item.id;
      box.checked = item.checked;
      box.disabled = state.busy || view.acted;
      label.append(box, document.createTextNode(` ${item.text}`));
      if (item.due) label.append(node('span', 'pa-second-brain__due', ` · ${item.due}`));
      fieldset.append(label);
    });
    form.append(fieldset);
  });
  details.append(form);
  if (!view.acted) {
    details.append(
      actionRow([{ id: 'add-selected', label: 'Add selected', style: 'outline' }], () => {
        const ids = Array.from(form.querySelectorAll('input[type="checkbox"]:checked')).map(
          box => box.value
        );
        if (!ids.length) {
          showError('Choose at least one item first.');
          return;
        }
        void addFollowUps(ids);
      })
    );
  }
  return details;
}

function showError(message) {
  state.error = String(message || '');
  const els = elements();
  if (!els?.error) return;
  els.error.textContent = state.error;
  els.error.hidden = !state.error;
}

function render() {
  const els = elements();
  if (!els?.body) return;
  const place = secondBrainPlacement(state);
  const focusWithin = els.root.contains(document.activeElement);
  window.PersonalAssistantTranscript?.beforeChange?.();
  const home = place === 'needs' && els.needs ? els.needs : els.thread;
  // The card is moved, never copied, between the conversation and Needs you,
  // so its handlers stay the same. The ask opens the conversation; a turn the
  // user started later (a folder card, the Notes slot) is its latest reply.
  if (home === els.thread && state.latest) {
    if (els.thread.lastElementChild !== els.root) els.thread.append(els.root);
  } else if (home && els.root.parentElement !== home) {
    if (home === els.thread) home.prepend(els.root);
    else home.append(els.root);
  }
  els.root.hidden = place === 'none';
  els.root.dataset.phase = state.phase;
  els.root.dataset.placement = place;
  els.body.replaceChildren();
  if (place !== 'none') {
    if (state.phase === 'ask') renderAsk(els.body);
    else if (state.phase === 'answered') renderAnswered(els.body);
    else if (state.phase === 'chooser') renderChooser(els.body);
    else if (state.phase === 'consent') renderConsent(els.body);
    else if (state.phase === 'manage') renderManage(els.body);
    else renderLook(els.body);
  }
  if (els.status) {
    els.status.textContent = state.busy && state.phase !== 'reading' ? 'Working…' : '';
    els.status.hidden = !els.status.textContent;
  }
  showError(state.error);
  // A click moves focus into the new step; a turn that appears by itself never
  // takes focus from the composer.
  if (
    (state.focusNext || focusWithin) &&
    place !== 'none' &&
    !document.getElementById('personalAssistantPanel')?.hidden
  ) {
    const target =
      els.body.querySelector('button:not([disabled]), input:not([disabled]), summary') || els.root;
    if (target === els.root) els.root.tabIndex = -1;
    target.focus?.({ preventScroll: true });
  }
  state.focusNext = false;
  window.PersonalAssistantTranscript?.changed?.();
}

function baseName(path) {
  const parts = String(path || '')
    .split(/[\\/]/)
    .filter(Boolean);
  return parts[parts.length - 1] || 'that folder';
}

async function loadModel() {
  if (state.model) return state.model;
  try {
    const response = await fetch(SYSTEM_MODEL_ENDPOINT, {
      headers: { Accept: 'application/json' }
    });
    const payload = response.ok ? await readJSON(response) : null;
    const data = payload?.data || payload || {};
    state.model = {
      configured: data.configured === true,
      provider: String(data.provider || ''),
      model: String(data.model || '')
    };
  } catch (_) {
    state.model = { configured: false };
  }
  return state.model;
}

async function chooseFolder(chosen) {
  state.chosen = chosen;
  state.message = '';
  state.replace = false;
  showError('');
  await loadModel();
  go('consent');
}

async function pickFolder() {
  if (state.busy) return;
  state.busy = true;
  render();
  try {
    const response = await fetch(FOLDER_PATH_ENDPOINT, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
      body: JSON.stringify({ title: 'Choose your notes folder' })
    });
    const result = (await readJSON(response)) || {};
    const off = folderDialogOffNote(result);
    if (off) {
      state.pickerOff = off;
    } else if (response.ok && result.selected && result.selection_token) {
      state.busy = false;
      await chooseFolder({
        selectionToken: result.selection_token,
        name: baseName(result.path),
        path: String(result.path || '')
      });
      return;
    } else if (!response.ok) {
      showError(
        secondBrainError(result, 'The folder dialog could not open. Paste the path instead.')
      );
    }
  } catch (_) {
    showError('The folder dialog could not open. Paste the path instead.');
  } finally {
    state.busy = false;
  }
  setPhase('chooser');
}

async function decide(decision) {
  if (state.busy) return;
  state.busy = true;
  render();
  const { ok, payload } = await request('/ask', {
    method: 'POST',
    body: { decision, request_id: newRequestID('ask') }
  }).catch(() => ({ ok: false, payload: {} }));
  state.busy = false;
  if (!ok) {
    showError(secondBrainError(payload));
    render();
    return;
  }
  state.secondBrain = payload.second_brain || state.secondBrain;
  state.decision = decision;
  setPhase('answered');
}

function onAskAction(action) {
  if (action.id === 'read') {
    const vault = (state.secondBrain?.detected || []).find(item => item.id === action.vaultId);
    void chooseFolder({
      detectedId: action.vaultId,
      name: vault?.name || '',
      path: vault?.path || ''
    });
  } else if (action.id === 'choose') {
    state.message = '';
    go('chooser');
  } else {
    void decide(action.id);
  }
}

async function connect() {
  if (state.busy || !state.chosen) return;
  state.busy = true;
  showError('');
  render();
  const body = { request_id: newRequestID('connect') };
  if (state.chosen.detectedId) body.detected_id = state.chosen.detectedId;
  else body.selection_token = state.chosen.selectionToken;
  if (state.replace) body.replace = true;
  const { ok, payload } = await request('/connect', { method: 'POST', body }).catch(() => ({
    ok: false,
    payload: {}
  }));
  state.busy = false;
  if (!ok) {
    if (payload.needs_confirm) {
      state.replace = true;
      render();
      return;
    }
    if (payload.no_notes || payload.needs_pick || payload.repair === 'choose_folder') {
      state.message = secondBrainError(payload);
      if (state.history[state.history.length - 1] === 'chooser') state.history.pop();
      go('chooser');
      return;
    }
    showError(secondBrainError(payload));
    render();
    return;
  }
  state.secondBrain = payload.second_brain || state.secondBrain;
  state.receipt = '';
  state.look = { state: 'running', look_id: payload.look_id };
  state.polls = 0;
  setPhase('reading', { focus: false });
  announce();
  schedulePoll();
}

function schedulePoll() {
  clearTimeout(state.pollTimer);
  state.pollTimer = setTimeout(() => void pollLook(), LOOK_POLL_MS);
}

async function pollLook() {
  const { ok, payload } = await request('/look').catch(() => ({ ok: false, payload: {} }));
  state.polls += 1;
  if (ok && payload.look) {
    state.look = payload.look;
    if (payload.look.state === 'running' && state.polls < LOOK_POLL_LIMIT) {
      schedulePoll();
      return;
    }
  }
  if (!ok || state.look?.state === 'running') {
    state.look = {
      state: 'failed',
      result: { failure: 'The look is taking longer than it should. Look again in a moment.' }
    };
  }
  state.phase = state.look.state === 'failed' ? 'failed' : 'result';
  state.focusNext = elements()?.root?.contains(document.activeElement) || false;
  render();
  announce();
}

async function addFollowUps(items) {
  const lookId = state.look?.look_id || state.look?.result?.id;
  if (state.busy || !lookId) return;
  state.busy = true;
  showError('');
  render();
  const { ok, payload } = await request(`/look/${encodeURIComponent(lookId)}/follow-ups`, {
    method: 'POST',
    body: { items, request_id: newRequestID('accept') }
  }).catch(() => ({ ok: false, payload: {} }));
  state.busy = false;
  if (!ok) {
    showError(secondBrainError(payload));
    render();
    return;
  }
  state.receipt = followUpReceiptLine(payload.follow_ups);
  state.focusNext = true;
  render();
  announce();
}

// lookAgain re-reads the connected folder without a new consent (FR36). The
// result then says what is new and what was done since the last look.
async function lookAgain() {
  if (state.busy) return;
  state.busy = true;
  showError('');
  render();
  const { ok, payload } = await request('/look', {
    method: 'POST',
    body: { request_id: newRequestID('look') }
  }).catch(() => ({ ok: false, payload: {} }));
  state.busy = false;
  if (!ok) {
    showError(secondBrainError(payload));
    render();
    return;
  }
  state.receipt = '';
  if (state.secondBrain?.last_look) state.secondBrain.last_look.acted = false;
  state.look = payload.look || { state: 'running' };
  state.polls = 0;
  setPhase(state.look.state === 'running' ? 'reading' : 'result', { focus: false });
  if (state.look.state === 'running') schedulePoll();
}

// disconnect removes the connection; the follow-ups it made stay (FR37).
async function disconnect() {
  if (state.busy || !state.secondBrain) return;
  state.busy = true;
  showError('');
  render();
  const name = state.secondBrain.source?.name || 'your notes';
  const { ok, payload } = await request('/disconnect', {
    method: 'POST',
    body: { request_id: newRequestID('disconnect'), if_version: state.secondBrain.version || 0 }
  }).catch(() => ({ ok: false, payload: {} }));
  state.busy = false;
  if (!ok) {
    showError(secondBrainError(payload));
    // A newer version (another tab) is read again so the next try can land.
    await refresh();
    render();
    return;
  }
  state.secondBrain = payload.second_brain || state.secondBrain;
  state.answer = `Disconnected ${name}. Ori no longer reads it; your follow-ups are kept.`;
  setPhase('answered');
  announce();
}

async function refresh() {
  const { ok, payload } = await request('').catch(() => ({ ok: false, payload: {} }));
  if (ok && payload.second_brain) state.secondBrain = payload.second_brain;
  return state.secondBrain;
}

async function dismiss() {
  const lookId = state.look?.look_id || state.look?.result?.id;
  if (state.busy || !lookId) return;
  state.busy = true;
  render();
  const { ok, payload } = await request(`/look/${encodeURIComponent(lookId)}/dismiss`, {
    method: 'POST',
    body: { request_id: newRequestID('dismiss') }
  }).catch(() => ({ ok: false, payload: {} }));
  state.busy = false;
  if (!ok) {
    showError(secondBrainError(payload));
    render();
    return;
  }
  state.pinned = false;
  setPhase('idle', { focus: false });
}

// announce tells Home's Today and the mission card that the turn moved.
function announce() {
  try {
    document.dispatchEvent(
      new CustomEvent('personal-assistant:second-brain', {
        detail: {
          phase: state.phase,
          look: state.look?.state || '',
          secondBrain: state.secondBrain
        }
      })
    );
  } catch (_) {
    // Listeners only refresh their own surface.
  }
}

async function load() {
  if (state.loading) return;
  state.loading = true;
  try {
    const { ok, payload } = await request('');
    if (!ok) return;
    state.secondBrain = payload.second_brain || null;
    const ask = secondBrainAskView(state.secondBrain, state.available);
    if (ask.show && state.phase === 'idle') {
      // The assistant speaks first: the ask is the conversation's first message.
      state.phase = 'ask';
      state.pinned = false;
      render();
      markPromptedWhenSeen();
      return;
    }
    if (state.phase === 'idle' && state.secondBrain?.status !== 'not_connected') {
      await placeWaitingResult();
    }
  } catch (_) {
    // The ask can wait for the next status change; nothing else depends on it.
  } finally {
    state.loading = false;
  }
}

// markPromptedWhenSeen records the ask's receipt once the user has the drawer
// open while the ask is showing, not when a page merely loads with the drawer
// closed: the ask is shown once, and a closed drawer has shown nothing.
let promptRecorded = false;
let promptWaiting = false;
function markPromptedWhenSeen() {
  if (promptRecorded) return;
  const record = () => {
    if (promptRecorded || state.phase !== 'ask') return;
    promptRecorded = true;
    void request('/prompted', {
      method: 'POST',
      body: { request_id: newRequestID('prompt') }
    }).then(result => {
      if (result.ok && result.payload.second_brain) state.secondBrain = result.payload.second_brain;
      else promptRecorded = false;
    });
  };
  if (!document.getElementById('personalAssistantPanel')?.hidden) {
    record();
    return;
  }
  if (promptWaiting) return;
  promptWaiting = true;
  document.addEventListener(
    'personal-assistant:opened',
    () => {
      promptWaiting = false;
      record();
    },
    { once: true }
  );
}

// placeWaitingResult shows a result still waiting for a decision once, in
// Needs you, when the page loads onto it (FR31), and resumes a running look.
async function placeWaitingResult() {
  const { ok, payload } = await request('/look');
  if (!ok || !payload.look) return;
  const look = payload.look;
  const waiting = look.state === 'finished' && state.secondBrain?.last_look?.acted === false;
  if (!waiting && look.state !== 'running') return;
  state.look = look;
  state.pinned = true;
  state.phase = look.state === 'running' ? 'reading' : 'result';
  render();
  if (look.state === 'running') {
    state.polls = 0;
    schedulePoll();
  }
}

function onStatus(personalAssistant) {
  const available = secondBrainAvailable(personalAssistant);
  const changed = available !== state.available;
  state.available = available;
  if (!available) {
    render();
    return;
  }
  if (changed || !state.secondBrain) void load();
}

function init() {
  if (!elements()) return;
  document.addEventListener('personal-assistant:status', event =>
    onStatus(event.detail?.personalAssistant)
  );
  const panelState = window.PersonalAssistantPanel?._state;
  if (panelState?.personalAssistant) onStatus(panelState.personalAssistant);
  window.PersonalAssistantSecondBrain = {
    reload: load,
    // The Notes slot and the mission card open the chooser in place.
    openChooser: async () => {
      if (!state.secondBrain) await load();
      state.pinned = false;
      state.latest = true;
      state.message = '';
      state.history = [];
      setPhase('chooser');
    },
    // A folder card in the conversation named an Obsidian vault: show what
    // connecting it means, as the conversation's latest reply (FR10).
    connectSelection: async ({ selectionToken, name, path } = {}) => {
      if (!selectionToken) throw new Error('That folder selection expired. Paste the path again.');
      if (!state.secondBrain) await load();
      state.pinned = false;
      state.latest = true;
      state.history = [];
      await chooseFolder({ selectionToken, name: name || baseName(path), path: path || '' });
    },
    // The Notes station and the Profile open the connection's own card:
    // connected, missing, or not set up (which is the chooser).
    openManage: async () => {
      await refresh();
      state.pinned = false;
      state.latest = true;
      state.message = '';
      state.answer = '';
      state.history = [];
      showError('');
      if (state.secondBrain?.status === 'not_connected') setPhase('chooser');
      else setPhase('manage');
    },
    // Read the connection without changing what the drawer shows.
    status: refresh,
    // Another surface changed the connection (the Profile's Disconnect): read
    // it again, and drop a result or card whose folder is no longer connected.
    changed: async () => {
      await refresh();
      if (
        state.secondBrain?.status === 'not_connected' &&
        ['result', 'reading', 'failed', 'manage'].includes(state.phase)
      ) {
        clearTimeout(state.pollTimer);
        state.pinned = false;
        setPhase('idle', { focus: false });
      }
      announce();
    },
    current: () => state.secondBrain
  };
  if (secondBrainQuestRequested(window.location.search)) startQuestArrival();
}

// startQuestArrival is the mission card's Start (/?quest=second-brain): open
// the drawer on the chooser once the panel knows the assistant, then scrub
// the query so a reload does not open it again.
function startQuestArrival() {
  try {
    const url = new URL(window.location.href);
    url.searchParams.delete('quest');
    window.history?.replaceState?.(window.history.state, '', url.pathname + url.search + url.hash);
  } catch (_) {
    // A cosmetic URL tidy never blocks the chooser.
  }
  let done = false;
  const attempt = () => {
    const panel = window.PersonalAssistantPanel;
    if (done || !panel || typeof panel.open !== 'function') return;
    if (!panel.open(document.getElementById('personalAssistantLauncher'), { focus: false })) return;
    done = true;
    void window.PersonalAssistantSecondBrain.openManage();
  };
  attempt();
  if (done) return;
  const onStatus = () => setTimeout(attempt, 0);
  document.addEventListener('personal-assistant:status', onStatus);
  setTimeout(() => {
    done = true;
    document.removeEventListener('personal-assistant:status', onStatus);
  }, 15000);
}

if (typeof document !== 'undefined') {
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', init);
  else init();
}
