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
import { folderChooserView } from './personal-assistant-folder-chooser.js';

export const SECOND_BRAIN_ENDPOINT = '/api/personal-assistant/second-brain';
// The usual folders the Add folder sheet offers, and so the offer under a first look.
const FOLDER_CHOICES_ENDPOINT = '/api/home-assistant/folder-context/choices';
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
  // "I don't keep notes" is no longer offered: Set one up for me takes its
  // place. A decline already on record (decision "none") is unaffected.
  const setUp = { id: 'setup', label: 'Set one up for me', style: 'outline' };
  if (vault?.id && vault?.name) {
    return {
      show,
      line: `Do you keep a second brain? I found your Obsidian vault "${vault.name}". Let me read it, or I can set one up for you.`,
      actions: [
        { id: 'read', label: `Read ${vault.name}`, style: 'primary', vaultId: vault.id },
        { id: 'choose', label: 'Another folder…', style: 'outline' },
        setUp,
        { id: 'later', label: 'Not now', style: 'link' }
      ]
    };
  }
  return {
    show,
    line: 'Do you keep a second brain? Point me at it, or I can set one up for you.',
    actions: [
      { id: 'choose', label: 'Choose folder…', style: 'primary' },
      setUp,
      { id: 'later', label: 'Not now', style: 'link' }
    ]
  };
}

// What the consent card says to a second brain Ori set up before it kept People,
// projects and the facts note up to date (FR10). It belongs under Needs you only
// while there is something to write that the answer would let through.
export const FILL_IN_LINE =
  'I can also keep People, a note per project and what I know about you up to date in your second brain. Yours are never touched.';

export function secondBrainFillInView(secondBrain) {
  const show =
    secondBrain?.status === 'connected' &&
    secondBrain?.source?.origin === 'created_by_ori' &&
    secondBrain?.fill_in?.offer === true;
  return {
    show,
    line: FILL_IN_LINE,
    actions: [
      { id: 'allow', label: 'Let Ori write there', style: 'primary' },
      { id: 'later', label: 'Not now', style: 'link' }
    ]
  };
}

// secondBrainFillInAnsweredLine is the one line the card leaves behind.
export function secondBrainFillInAnsweredLine(decision) {
  return decision === 'allow'
    ? "Done. I'll keep People, a note per project and what I know about you up to date."
    : "OK. I'll leave those alone for now.";
}

// The first look at a new second brain offers the first thing to show Ori
// (FR13, FR14). The server's headline carries the sentence when the brain is
// empty; under a headline that counts seeded notes the offer says it itself.
export const FOLDER_OFFER_LINE = "Show me a folder and I'll start filling it.";
// The usual folders, Downloads first. Which of them the offer shows is the
// server's list (the one the Add folder sheet uses), so it never offers a folder
// that is not there; this order only sorts it.
export const FOLDER_OFFER_ORDER = ['downloads', 'music', 'documents', 'desktop'];

// secondBrainFolderOffer builds the offer from the folders the server approved.
// available is that list ({id, label}); null means it is not known yet, and then
// only Choose a folder… is offered rather than a folder that may not exist.
export function secondBrainFolderOffer(headline, available = null) {
  const rank = id => {
    const at = FOLDER_OFFER_ORDER.indexOf(id);
    return at < 0 ? FOLDER_OFFER_ORDER.length : at;
  };
  const chips = (Array.isArray(available) ? available : [])
    .filter(chip => chip && typeof chip.id === 'string' && chip.id)
    .map(chip => ({ id: chip.id, label: String(chip.label || chip.id) }))
    .sort((a, b) => rank(a.id) - rank(b.id));
  return {
    line: String(headline || '').includes(FOLDER_OFFER_LINE) ? '' : FOLDER_OFFER_LINE,
    chips,
    choose: { id: 'choose', label: 'Choose a folder…' }
  };
}

// secondBrainFolderOfferShows: the offer follows the first look of a second brain
// Ori just set up, and also any result whose own headline asks to be shown a
// folder, so a reload does not take it away. Choosing a folder ends it.
export function secondBrainFolderOfferShows({ justSetUp, headline, dismissed } = {}) {
  if (dismissed === true) return false;
  return justSetUp === true || String(headline || '').includes(FOLDER_OFFER_LINE);
}

// The drawer's one project question (first-week FR20 to FR23): the interview's
// first question, asked once, with the folder suggestion when there is one. The
// answer is saved as the reviewed fact the user typed, word for word, and becomes
// a project note they can turn into a workspace. The Profile page's interview is
// a separate surface and is not touched.
export const PROJECT_QUESTION_ID = 'priority';
export const PROJECT_ANSWER_MAX = 500;
const INTERVIEW_ENDPOINT = '/api/personal-assistant/knowledge/interview';
const EXPLICIT_ENDPOINT = '/api/personal-assistant/knowledge/explicit';
const KNOWLEDGE_ENDPOINT = '/api/personal-assistant/knowledge';

// secondBrainProjectAskView: the question belongs to a second brain Ori set up
// whose owner has let it keep project notes, that has no project yet, and where
// the question has not been answered or skipped.
export function secondBrainProjectAskView(secondBrain) {
  const fillIn = secondBrain?.fill_in;
  const projects = Array.isArray(secondBrain?.projects) ? secondBrain.projects : [];
  const show =
    secondBrain?.status === 'connected' &&
    secondBrain?.source?.origin === 'created_by_ori' &&
    fillIn?.state === 'allowed' &&
    !fillIn?.project_ask &&
    projects.length === 0;
  return { show };
}

const NAME_FILLER = new Set(
  (
    'the my our a an on at for of to with working work im i am just currently mainly main thing ' +
    'is are finishing building writing planning launching preparing starting making doing'
  ).split(' ')
);

// projectNameFromAnswer proposes a short name for the project an answer describes:
// the first clause with its leading filler words dropped, at most three words.
// It is only a proposal; the user confirms or changes it ("I'll call it Launch.
// OK?"), and the server checks it again.
export function projectNameFromAnswer(answer) {
  const clause = String(answer || '')
    .split(/[,.;:!?\n—–]|\s-\s/)[0]
    .replace(/[^\p{L}\p{N}' -]/gu, ' ');
  const words = clause.split(/\s+/).filter(Boolean);
  while (words.length && NAME_FILLER.has(words[0].toLowerCase().replace(/'/g, ''))) words.shift();
  const name = words
    .slice(0, 3)
    .map(word => word.charAt(0).toUpperCase() + word.slice(1))
    .join(' ')
    .trim();
  return name && !secondBrainFolderNameProblem(name) ? name : 'Project';
}

// What a folder taught Ori, answered on one card (first-week FR16 to FR19). The
// items are the review queue's candidates from the user's approved filing; each
// is approved or turned away with the Profile page's own actions, so the wording
// the user approves is the wording shown here, word for word.
export const SETUP_LEARNING_ENDPOINT = '/api/personal-assistant/setup-learning';
export const SETUP_LEARNING_HEADING =
  'Here is what I learned from your folder. Approve what is right and it goes into your second brain.';

export function setupLearningView(payload) {
  const raw = Array.isArray(payload?.learning?.items) ? payload.learning.items : [];
  const items = raw
    .filter(item => item && typeof item.id === 'string' && item.id && Number(item.version) >= 1)
    .map(item => ({
      id: item.id,
      version: Number(item.version),
      text: String(item.text || '').trim(),
      evidence: (Array.isArray(item.evidence) ? item.evidence : [])
        .map(line => String(line || '').trim())
        .filter(Boolean)
        .slice(0, 3),
      source: item.source === 'filing' ? 'filing' : 'files'
    }))
    .filter(item => item.text);
  return {
    items,
    total: items.length,
    heading: items.length
      ? SETUP_LEARNING_HEADING
      : 'Nothing from this folder is waiting for an answer right now.'
  };
}

// setupLearningClosing is the card's last line: how many notes were written, and
// where to see them. Every number is the folder's own count, never a promise.
export function setupLearningClosing(written) {
  const count = Math.max(0, Number(written) || 0);
  if (count === 0) return 'Nothing new was written to your notes.';
  return count === 1
    ? '1 note was written in your second brain.'
    : `${count} notes were written in your second brain.`;
}

export const DEFAULT_SECOND_BRAIN_NAME = 'Second Brain';

// secondBrainFolderNameProblem is the one sentence for a name that is not one
// plain folder name, or '' when it is fine. The server checks it again.
export function secondBrainFolderNameProblem(name) {
  const text = String(name ?? '').trim();
  if (!text) return 'Give the folder a name.';
  if (
    text.length > 255 ||
    text === '.' ||
    text === '..' ||
    text.startsWith('.') ||
    text.endsWith('.') ||
    /[/\\:]/.test(text) ||
    // eslint-disable-next-line no-control-regex
    /[\u0000-\u001f\u007f]/.test(text)
  ) {
    return 'Choose a simple folder name, without slashes.';
  }
  return '';
}

// What Ori says it will write, before it writes anything.
const SECOND_BRAIN_SETUP_LINES = [
  "I'll keep Inbox, Daily, People, a note per project and what I know about you up to date; everything else is yours.",
  'It starts with your open follow-ups and the people I already know. Plain Markdown that opens in Obsidian or any editor, and stays yours if you stop using Ori.'
];

// secondBrainSetupDefaultView is the one-click setup: the place is already
// decided, so there is nothing to pick or name, and Somewhere else is the way
// to choose another.
export function secondBrainSetupDefaultView(place) {
  return {
    title: `Set up your second brain in ${String(place?.parent_name || 'your workspaces')}?`,
    path: String(place?.path || ''),
    lines: SECOND_BRAIN_SETUP_LINES,
    actions: [
      { id: 'create', label: 'Set it up', style: 'primary' },
      { id: 'elsewhere', label: 'Somewhere else…', style: 'outline' },
      { id: 'later', label: 'Not now', style: 'link' }
    ]
  };
}

// secondBrainSetupWhereView is the step for choosing another place. Only
// a parent is picked here, through the same folder dialog or typed path as
// connecting a folder (FR4).
export function secondBrainSetupWhereView({ pickerOff = '', message = '' } = {}) {
  return {
    title: 'Where should I set up your second brain?',
    note: 'Pick the folder it goes in.',
    pickLabel: 'Choose where…',
    pickerOff: String(pickerOff || ''),
    message: String(message || '')
  };
}

// secondBrainSetupConfirmView is the second step: the name, and one sentence
// saying what Ori will write, before it writes anything (FR4, design).
export function secondBrainSetupConfirmView(parent, name, { message = '' } = {}) {
  const folder = String(parent?.name || 'that folder');
  const named = String(name ?? DEFAULT_SECOND_BRAIN_NAME).trim() || DEFAULT_SECOND_BRAIN_NAME;
  return {
    title: `Set up "${named}" in ${folder}?`,
    path: String(parent?.path || ''),
    nameLabel: 'Folder name',
    name: String(name ?? DEFAULT_SECOND_BRAIN_NAME),
    lines: SECOND_BRAIN_SETUP_LINES,
    message: String(message || ''),
    actions: [
      { id: 'create', label: 'Set it up', style: 'primary' },
      { id: 'back', label: 'Back', style: 'outline' },
      { id: 'later', label: 'Not now', style: 'link' }
    ]
  };
}

// secondBrainOpenLink is the link that opens a folder Ori set up as a vault in
// Obsidian; '' for any other connection. It is a link only: Ori never opens
// the app itself.
export function secondBrainOpenLink(secondBrain) {
  const source = secondBrain?.source;
  const link = String(source?.open_link || '');
  return source?.origin === 'created_by_ori' && link.startsWith('obsidian://open?') ? link : '';
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
    setUp: { id: 'setup', label: 'Set one up for me', style: 'outline' },
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
      checked: item.suggested === true,
      // Copied from a follow-up the user already has: shown, never added again.
      seeded: item.seeded === true
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
        'Point Ori at an Obsidian vault or a folder of Markdown and it tells you what is open, or let it set one up for you.',
      actions: [
        { id: 'choose', label: 'Choose folder…', style: 'primary' },
        { id: 'setup', label: 'Set one up for me', style: 'outline' }
      ]
    };
  }
  const kind =
    source.origin === 'created_by_ori'
      ? 'Set up by Ori'
      : source.kind === 'obsidian'
        ? 'Obsidian'
        : 'Markdown folder';
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
  const created = source.origin === 'created_by_ori';
  return {
    state: 'connected',
    title: `Connected · ${source.name}`,
    detail: since ? `${kind} · last look ${since}` : `${kind} · not looked at yet`,
    // A folder Ori set up says what Ori wrote in it and takes a quick note.
    written: created ? secondBrainWrittenLine(secondBrain.written_by_ori, now) : '',
    capture: created,
    actions: [{ id: 'again', label: 'Look again', style: 'primary' }, disconnect]
  };
}

// secondBrainWrittenLine says what Ori has put in the notes it keeps (FR16).
export function secondBrainWrittenLine(written, now = Date.now()) {
  const count = Number(written?.count) || 0;
  if (count === 0) return 'Ori has not written to your notes yet.';
  const notes = count === 1 ? '1 note' : `${count} notes`;
  const when = written?.last_at ? sinceLabel(written.last_at, now) : '';
  return when ? `Written by Ori: ${notes} · last write ${when}` : `Written by Ori: ${notes}`;
}

// secondBrainProjectsView lists the folders under Projects in a second brain
// Ori set up, each with Make a workspace (FR18). Nothing is made until the
// person presses it and then Creates in the form it opens.
export function secondBrainProjectsView(secondBrain) {
  if (secondBrain?.source?.origin !== 'created_by_ori') return [];
  return (Array.isArray(secondBrain.projects) ? secondBrain.projects : [])
    .filter(project => project?.name && !/[\\/]/.test(project.name))
    .map(project => ({
      name: String(project.name),
      action: {
        id: 'workspace',
        label: 'Make a workspace',
        style: 'outline',
        ariaLabel: `Make ${project.name} a workspace`
      }
    }));
}

// secondBrainProjectLink opens Create Workspace on a project, by its name: the
// page looks the folder up itself, so a link never carries a path.
export function secondBrainProjectLink(name) {
  return `/?create=1&second_brain_project=${encodeURIComponent(String(name || ''))}`;
}

export const CAPTURE_MAX_CHARACTERS = 4000;

// secondBrainCaptureProblem is the one sentence for a note that cannot be
// saved, or '' when it can. The server checks it again.
export function secondBrainCaptureProblem(text) {
  const trimmed = String(text ?? '').trim();
  if (!trimmed) return 'Write the note first.';
  if (Array.from(trimmed).length > CAPTURE_MAX_CHARACTERS) {
    return `A note is at most ${CAPTURE_MAX_CHARACTERS.toLocaleString('en-US')} characters.`;
  }
  return '';
}

// secondBrainCaptureReceipt says where a captured note went.
export function secondBrainCaptureReceipt(capture) {
  const name = String(capture?.name || '').trim();
  return name ? `Saved to Inbox as ${name}.` : 'Saved to Inbox.';
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
  phase: 'idle', // ask | answered | chooser | setup | consent | reading | result | failed | fillin | learned
  pinned: false,
  // The folder offer under the first look of a second brain Ori just set up;
  // offerDismissed ends it once a folder is attached. folderChips are the usual
  // folders the server approved, null until it has answered.
  offerFolder: false,
  offerDismissed: false,
  folderChips: null,
  folderChipsLoading: false,
  // The card of what a folder taught Ori: its items, what the user answered
  // (id → 'approve' | 'reject'), one retry key per answer, the notes counted when
  // it opened, and its closing line once everything is answered.
  learned: { status: 'idle', items: [], answered: {}, keys: {}, baseline: 0, closing: '' },
  // The project question: where it stands (loading | ask | name | done | error),
  // the interview's wording and state version, the folder suggestion, the answer
  // and name being typed, one retry key per write, and the note it wrote.
  project: {
    status: 'idle',
    prompt: '',
    hint: '',
    stateVersion: 0,
    suggestion: null,
    answer: '',
    name: '',
    keys: {},
    note: null,
    message: ''
  },
  panelRoute: '',
  chosen: null, // { detectedId | selectionToken, name, path }
  // Setting one up: the parent the user picked ({ selectionToken, name, path})
  // once there is one, the folder name typed for it, and a refusal to show.
  setup: { parent: null, name: DEFAULT_SECOND_BRAIN_NAME, message: '', requestId: '' },
  // The capture box on a second brain Ori set up.
  capture: { text: '', requestId: '', message: '', link: '' },
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
let setupPathEntry = null;

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
  if (phase === 'idle') state.offerFolder = false;
  state.phase = phase;
  state.focusNext = focus;
  render();
}

function renderAsk(body) {
  const view = secondBrainAskView(state.secondBrain, true);
  body.append(node('p', 'pa-specialist-offer__headline', view.line));
  body.append(actionRow(view.actions, onAskAction));
}

function renderFillIn(body) {
  const view = secondBrainFillInView(state.secondBrain);
  body.append(node('p', 'pa-specialist-offer__headline', view.line));
  body.append(actionRow(view.actions, action => void answerFillIn(action.id)));
}

// answerFillIn sends the answer, then leaves one line behind. Allowing writes
// what was held back before the user leaves the card.
async function answerFillIn(decision) {
  if (state.busy) return;
  state.busy = true;
  showError('');
  render();
  const { ok, payload } = await request('/fill-in', {
    method: 'POST',
    body: { decision, request_id: newRequestID('fillin') }
  }).catch(() => ({ ok: false, payload: {} }));
  state.busy = false;
  if (!ok) {
    showError(secondBrainError(payload));
    render();
    return;
  }
  state.secondBrain = payload.second_brain || state.secondBrain;
  state.answer = secondBrainFillInAnsweredLine(decision);
  state.pinned = false;
  setPhase('answered', { focus: false });
  announce();
}

// renderFolderOffer is the first thing to show Ori: the usual folders that exist
// and Choose a folder…, both starting the Show a folder turn in this conversation.
function renderFolderOffer(body, headline) {
  const offer = secondBrainFolderOffer(headline, state.folderChips);
  if (offer.line) body.append(node('p', 'pa-folder__note', offer.line));
  const row = node('div', 'pa-specialist-offer__actions');
  row.dataset.secondBrainFolderOffer = '';
  offer.chips.forEach(chip =>
    row.append(
      button({ ...chip, label: chip.label, style: 'outline' }, () => void showFolder(chip.id))
    )
  );
  row.append(button({ ...offer.choose, style: 'link' }, () => void showFolder('')));
  body.append(row);
  if (state.folderChips === null) void loadFolderChips();
}

// loadFolderChips asks the server which usual folders exist, the way the Add
// folder sheet does. Until it answers only Choose a folder… is shown. A failure
// leaves that, which still works.
async function loadFolderChips() {
  if (state.folderChips !== null || state.folderChipsLoading) return;
  state.folderChipsLoading = true;
  try {
    const response = await fetch(FOLDER_CHOICES_ENDPOINT, {
      headers: { Accept: 'application/json' }
    });
    const payload = response.ok ? await response.json() : {};
    state.folderChips = folderChooserView(payload).chips;
  } catch (_) {
    state.folderChips = [];
  } finally {
    state.folderChipsLoading = false;
  }
  render();
}

// showFolder hands the folder to the Show a folder flow, which holds it for the
// next message exactly as Add folder does. Nothing is read until the user sends.
// The offer stays until a folder is actually attached, so a chip that could not
// be chosen leaves the others to try.
async function showFolder(chip) {
  const context = window.PersonalAssistantFolderContext;
  if (chip && typeof context?.selectChip === 'function') {
    if ((await context.selectChip(chip)) === true) {
      state.offerDismissed = true;
      render();
    }
    return;
  }
  // Choose a folder… opens the Add folder chooser, where the choice is made.
  state.offerDismissed = true;
  render();
  window.PersonalAssistantFolder?.openChooser?.();
}

// openLearned opens the card of what the user's approved filing taught Ori. It is
// what the first-batch report's Review what Ori learned does: it opens the
// drawer, reads the queue, and counts the notes already there so the closing line
// can say how many this card wrote.
async function openLearned() {
  try {
    const panel = window.PersonalAssistantPanel;
    panel?.open?.(document.getElementById('personalAssistantLauncher'), { focus: false });
  } catch (_) {
    // The card still draws if the drawer was already open.
  }
  if (!state.secondBrain) await load();
  state.pinned = false;
  state.latest = true;
  state.history = [];
  state.learned = {
    status: 'loading',
    items: [],
    answered: {},
    keys: {},
    baseline: 0,
    closing: ''
  };
  setPhase('learned', { focus: false });
  const [{ ok, payload }, status] = await Promise.all([
    fetch(SETUP_LEARNING_ENDPOINT, { headers: { Accept: 'application/json' } })
      .then(async response => ({
        ok: response.ok,
        payload: await response.json().catch(() => ({}))
      }))
      .catch(() => ({ ok: false, payload: {} })),
    refresh()
  ]);
  state.learned.baseline = Number(status?.written_by_ori?.count) || 0;
  if (!ok) {
    state.learned.status = 'error';
  } else {
    state.learned.items = setupLearningView(payload).items;
    state.learned.status = 'ready';
  }
  render();
}

function learnedRemaining() {
  return state.learned.items.filter(item => !state.learned.answered[item.id]);
}

// answerLearning approves or turns away one learning with the Profile page's own
// action. The retry key is made once per learning and answer, so pressing again
// after a failure repeats the same request instead of starting another.
async function answerLearning(item, action) {
  if (state.busy || state.learned.answered[item.id]) return;
  state.busy = true;
  showError('');
  render();
  const keyName = `${item.id}:${action}`;
  if (!state.learned.keys[keyName]) state.learned.keys[keyName] = newRequestID('learn');
  const { ok, payload } = await fetch(
    `/api/personal-assistant/knowledge/${encodeURIComponent(item.id)}/${action}`,
    {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
      body: JSON.stringify({ version: item.version, request_id: state.learned.keys[keyName] })
    }
  )
    .then(async response => ({ ok: response.ok, payload: await response.json().catch(() => ({})) }))
    .catch(() => ({ ok: false, payload: {} }));
  state.busy = false;
  if (!ok) {
    showError(secondBrainError(payload, 'That did not save. Try again.'));
    render();
    return;
  }
  state.learned.answered[item.id] = action;
  tellKnowledgeChanged();
  if (learnedRemaining().length === 0) {
    await settleLearned();
    return;
  }
  render();
}

// settleLearned waits for the sync that each approval started, so the closing line
// counts notes that are on disk, then says how many were written. The sync is a
// background run, so it polls briefly and then reports what it found.
async function settleLearned() {
  state.busy = true;
  render();
  const wanted = state.learned.items.filter(
    item => state.learned.answered[item.id] === 'approve' && item.source === 'files'
  ).length;
  let written = 0;
  for (let attempt = 0; attempt < 8; attempt++) {
    const status = await refresh();
    written = Math.max(0, (Number(status?.written_by_ori?.count) || 0) - state.learned.baseline);
    if (written >= wanted) break;
    await new Promise(resolve => setTimeout(resolve, 400));
  }
  state.busy = false;
  state.learned.closing = setupLearningClosing(written);
  render();
  announce();
}

// dismissLearned turns away what is left so it does not stay in the queue.
async function dismissLearned() {
  if (state.busy) return;
  state.busy = true;
  showError('');
  render();
  const { ok, payload } = await fetch(`${SETUP_LEARNING_ENDPOINT}/dismiss`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
    body: JSON.stringify({ request_id: newRequestID('learn-dismiss') })
  })
    .then(async response => ({ ok: response.ok, payload: await response.json().catch(() => ({})) }))
    .catch(() => ({ ok: false, payload: {} }));
  state.busy = false;
  if (!ok) {
    showError(secondBrainError(payload));
    render();
    return;
  }
  tellKnowledgeChanged();
  state.pinned = false;
  setPhase('idle', { focus: false });
  await showNextCard();
}

// tellKnowledgeChanged lets Home read Today again, so the first-batch report stops
// saying it learned what has just been answered.
function tellKnowledgeChanged() {
  try {
    document.dispatchEvent(new CustomEvent('personal-assistant-knowledge-changed'));
  } catch (_) {
    // Today reloads on its own the next time it is opened.
  }
}

function renderLearned(body) {
  const learned = state.learned;
  if (learned.status === 'loading') {
    const line = node('p', 'pa-specialist-offer__headline', 'Reading what I learned…');
    line.setAttribute('role', 'status');
    body.append(line);
    return;
  }
  if (learned.status === 'error') {
    body.append(
      node('p', 'pa-specialist-offer__headline', 'I could not read what I learned. Try again.')
    );
    body.append(
      actionRow([{ id: 'retry', label: 'Try again', style: 'primary' }], () => void openLearned())
    );
    return;
  }
  const view = setupLearningView({ learning: { items: learned.items } });
  body.append(node('p', 'pa-specialist-offer__headline', view.heading));
  const list = node('ul', 'pa-second-brain__learned');
  view.items.forEach(item => {
    const row = node('li', 'pa-second-brain__learned-item');
    row.dataset.learnedItem = item.id;
    row.append(node('p', 'pa-second-brain__learned-text', item.text));
    item.evidence.forEach(line => row.append(node('p', 'pa-folder__note', line)));
    const answered = learned.answered[item.id];
    if (answered) {
      row.append(
        node('p', 'pa-folder__note', answered === 'approve' ? 'Approved' : 'Not this one')
      );
    } else {
      row.append(
        actionRow(
          [
            { id: 'approve', label: 'Approve', style: 'primary' },
            { id: 'reject', label: 'Not this', style: 'link' }
          ],
          action => void answerLearning(item, action.id)
        )
      );
    }
    list.append(row);
  });
  if (view.items.length) body.append(list);
  if (learned.closing) {
    body.append(node('p', 'pa-folder__first-look', learned.closing));
    const links = node('p', 'pa-folder__note');
    if (state.panelRoute) {
      const notes = node('a', 'pa-second-brain__open', 'See them in Notes');
      notes.href = state.panelRoute;
      links.append(notes);
    }
    const open = secondBrainOpenLink(state.secondBrain);
    if (open) {
      const obsidian = node('a', 'pa-second-brain__open', 'Open in Obsidian');
      obsidian.href = open;
      if (links.childNodes.length) links.append(' · ');
      links.append(obsidian);
    }
    if (links.childNodes.length) body.append(links);
    body.append(
      actionRow([{ id: 'done', label: 'Done', style: 'outline' }], () => {
        state.pinned = false;
        setPhase('idle', { focus: false });
        void showNextCard();
      })
    );
    return;
  }
  const actions = [];
  if (!view.items.length) actions.push({ id: 'done', label: 'Done', style: 'outline' });
  else actions.push({ id: 'later', label: 'Not now', style: 'link' });
  body.append(
    actionRow(actions, action => {
      if (action.id === 'later') void dismissLearned();
      else setPhase('idle', { focus: false });
    })
  );
}

// fetchJSON reads or posts JSON and never throws: {ok, status, payload}.
function fetchJSON(url, body, fetchImpl = fetch) {
  return fetchImpl(url, {
    method: body ? 'POST' : 'GET',
    headers: {
      Accept: 'application/json',
      ...(body ? { 'Content-Type': 'application/json' } : {})
    },
    ...(body ? { body: JSON.stringify(body) } : {})
  })
    .then(async response => ({
      ok: response.ok,
      status: response.status,
      payload: await response.json().catch(() => ({}))
    }))
    .catch(() => ({ ok: false, status: 0, payload: {} }));
}

// saveProjectFact saves the answer as the reviewed fact the user typed. The
// version it names is the assistant record's, and that moves whenever anything in
// the record does (a mission completing, a learning arriving), which can happen
// while the user is still typing. A refusal because it changed is answered by
// reading the version again and saving once more; the retry key is the same, so
// the fact can only ever be saved once.
//
// A refusal that is not about the version can mean the user already said exactly
// this (on the Profile page, or in an earlier answer here): saving a fact twice is
// refused, but the fact is held, and what is left to do is the project's note.
export async function saveProjectFact(project, fetchImpl = fetch) {
  const send = () =>
    fetchJSON(
      EXPLICIT_ENDPOINT,
      {
        request_id: project.keys.fact,
        state_version: project.stateVersion,
        category: 'projects',
        text: project.answer.trim()
      },
      fetchImpl
    );
  let result = await send();
  if (!result.ok && result.status === 409) {
    const fresh = await fetchJSON(INTERVIEW_ENDPOINT, null, fetchImpl);
    const version = Number(fresh.payload?.state_version);
    if (fresh.ok && version >= 1 && version !== project.stateVersion) {
      project.stateVersion = version;
      result = await send();
    }
  }
  if (!result.ok && result.status === 409) {
    const held = await fetchJSON(KNOWLEDGE_ENDPOINT, null, fetchImpl);
    const answer = project.answer.trim();
    const already =
      held.ok &&
      (Array.isArray(held.payload?.items) ? held.payload.items : []).some(
        item => item?.state === 'approved' && item?.category === 'projects' && item?.text === answer
      );
    if (already) return { ok: true, status: 200, payload: { already_held: true } };
  }
  return result;
}

// openProjectAsk reads the interview's first question and the folder suggestion,
// then asks it. A failed read asks nothing: the question can wait.
async function openProjectAsk() {
  state.project = {
    status: 'loading',
    prompt: '',
    hint: '',
    stateVersion: 0,
    suggestion: null,
    answer: '',
    name: '',
    keys: {},
    note: null,
    message: ''
  };
  const { ok, payload } = await fetchJSON(INTERVIEW_ENDPOINT);
  const question = ok && (payload.questions || []).find(item => item.id === PROJECT_QUESTION_ID);
  if (!question || !(Number(payload.state_version) >= 1)) {
    state.project.status = 'idle';
    return false;
  }
  const suggestion = payload.suggestion?.text
    ? { text: String(payload.suggestion.text), folder: String(payload.suggestion.folder || '') }
    : null;
  state.project = {
    ...state.project,
    status: 'ask',
    prompt: String(question.prompt || ''),
    hint: String(question.hint || ''),
    stateVersion: Number(payload.state_version),
    suggestion
  };
  state.pinned = true;
  setPhase('project', { focus: false });
  return true;
}

// showProjectAskIfDue asks the question once the card slot is free and the second
// brain is ready for a project note.
async function showProjectAskIfDue() {
  if (state.phase !== 'idle' || !secondBrainProjectAskView(state.secondBrain).show) return;
  await openProjectAsk();
}

// showNextCard is whatever the one card slot owes next, in order: the consent
// question, then the project question.
async function showNextCard() {
  await showFillInIfOffered();
  await showProjectAskIfDue();
}

function projectFromUsingSuggestion() {
  const project = state.project;
  return project.suggestion && project.answer.trim() === project.suggestion.text.trim();
}

// continueProject moves from the answer to the name, or straight to saving when
// the suggestion already named the project (its folder's own name).
function continueProject() {
  const project = state.project;
  const answer = project.answer.trim();
  if (!answer) {
    project.message = 'Write your answer first, or skip.';
    render();
    return;
  }
  if (Array.from(answer).length > PROJECT_ANSWER_MAX) {
    project.message = `An answer is at most ${PROJECT_ANSWER_MAX} characters.`;
    render();
    return;
  }
  project.message = '';
  if (projectFromUsingSuggestion() && !secondBrainFolderNameProblem(project.suggestion.folder)) {
    project.name = project.suggestion.folder;
    void saveProject();
    return;
  }
  project.name = project.name || projectNameFromAnswer(answer);
  project.status = 'name';
  render();
}

// saveProject saves the answer as the reviewed fact the user typed (the same
// canonical write the interview's rows end in, one retry key for it), then writes
// the project's note. Each step repeats safely, so a retry after a failure picks
// up where it stopped.
async function saveProject() {
  const project = state.project;
  const name = String(project.name).trim();
  const problem = secondBrainFolderNameProblem(name);
  if (problem) {
    project.message = problem;
    render();
    return;
  }
  if (state.busy) return;
  state.busy = true;
  project.message = '';
  showError('');
  render();
  project.keys.fact = project.keys.fact || newRequestID('projfact');
  project.keys.note = project.keys.note || newRequestID('projnote');
  const fact = await saveProjectFact(project);
  if (!fact.ok) {
    state.busy = false;
    project.message = secondBrainError(fact.payload, 'That did not save. Try again.');
    render();
    return;
  }
  const written = await request('/project', {
    method: 'POST',
    body: { name, answer: project.answer.trim(), request_id: project.keys.note }
  }).catch(() => ({ ok: false, payload: {} }));
  state.busy = false;
  if (!written.ok) {
    // The fact is saved either way; only the note is left to do.
    project.status = project.status === 'ask' && projectFromUsingSuggestion() ? 'ask' : 'name';
    project.message = secondBrainError(written.payload, 'The note did not save. Try again.');
    render();
    return;
  }
  project.note = written.payload.project || { name, rel: `Projects/${name}/README.md` };
  project.status = 'done';
  await refresh();
  tellKnowledgeChanged();
  render();
  announce();
}

// skipProject ends the question for good in the drawer. The Profile page keeps it.
async function skipProject() {
  if (state.busy) return;
  state.busy = true;
  showError('');
  render();
  const { ok, payload } = await request('/project/skip', {
    method: 'POST',
    body: { request_id: newRequestID('projskip') }
  }).catch(() => ({ ok: false, payload: {} }));
  state.busy = false;
  if (!ok) {
    showError(secondBrainError(payload));
    render();
    return;
  }
  state.secondBrain = payload.second_brain || state.secondBrain;
  state.pinned = false;
  state.answer = 'OK. I won’t ask again here; you can still answer on your Profile page.';
  setPhase('answered', { focus: false });
}

function renderProject(body) {
  const project = state.project;
  if (project.status === 'loading') {
    const line = node('p', 'pa-specialist-offer__headline', 'One quick question…');
    line.setAttribute('role', 'status');
    body.append(line);
    return;
  }
  if (project.status === 'done') {
    const name = project.note?.name || project.name;
    body.append(node('p', 'pa-specialist-offer__headline', `Added ${name} to your notes.`));
    body.append(
      node('p', 'pa-folder__note', 'It is in Projects as a note you can turn into a workspace.')
    );
    if (project.note?.link) {
      const open = node('a', 'pa-second-brain__open', 'Open in Obsidian');
      open.href = project.note.link;
      const line = node('p', 'pa-folder__note');
      line.append(open);
      body.append(line);
    }
    body.append(
      actionRow(
        [
          { id: 'workspace', label: 'Make it a workspace', style: 'primary' },
          { id: 'done', label: 'Done', style: 'outline' }
        ],
        action => {
          if (action.id === 'workspace') {
            makeWorkspace(name);
            return;
          }
          state.pinned = false;
          setPhase('idle', { focus: false });
        }
      )
    );
    return;
  }
  if (project.status === 'name') {
    body.append(node('p', 'pa-specialist-offer__headline', `I'll call it ${project.name}. OK?`));
    const input = node('input', 'form-control form-control-sm');
    input.type = 'text';
    input.value = project.name;
    input.maxLength = 120;
    input.dataset.projectName = '';
    input.setAttribute('aria-label', 'Project name');
    input.disabled = state.busy;
    input.addEventListener('input', () => {
      project.name = input.value;
    });
    body.append(input);
    if (project.message) body.append(node('p', 'pa-folder__note', project.message));
    body.append(
      actionRow(
        [
          { id: 'confirm', label: 'Save', style: 'primary' },
          { id: 'back', label: 'Back', style: 'link' }
        ],
        action => {
          if (action.id === 'confirm') void saveProject();
          else {
            project.status = 'ask';
            project.message = '';
            render();
          }
        }
      )
    );
    return;
  }
  body.append(node('p', 'pa-specialist-offer__headline', project.prompt));
  if (project.hint) body.append(node('p', 'pa-folder__note', project.hint));
  const area = node('textarea', 'form-control form-control-sm');
  area.rows = 3;
  area.maxLength = PROJECT_ANSWER_MAX;
  area.value = project.answer;
  area.dataset.projectAnswer = '';
  area.setAttribute('aria-label', project.prompt);
  area.disabled = state.busy;
  area.addEventListener('input', () => {
    project.answer = area.value;
  });
  body.append(area);
  if (project.suggestion) {
    body.append(
      actionRow(
        [{ id: 'suggestion', label: `Use “${project.suggestion.text}”`, style: 'outline' }],
        () => {
          project.answer = project.suggestion.text;
          area.value = project.answer;
        }
      )
    );
  }
  if (project.message) body.append(node('p', 'pa-folder__note', project.message));
  body.append(
    actionRow(
      [
        { id: 'save', label: 'Save', style: 'primary' },
        { id: 'skip', label: 'Skip', style: 'link' }
      ],
      action => {
        if (action.id === 'save') continueProject();
        else void skipProject();
      }
    )
  );
}

// showFillInIfOffered is the consent card's turn once a result that was waiting
// has been dealt with: the one card slot is free, so the question follows at once.
async function showFillInIfOffered() {
  await refresh();
  if (state.phase === 'idle' && secondBrainFillInView(state.secondBrain).show) {
    state.pinned = true;
    setPhase('fillin', { focus: false });
  }
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
  if (view.written) body.append(node('p', 'pa-folder__note', view.written));
  const openLink = secondBrainOpenLink(state.secondBrain);
  if (openLink) {
    const open = node('a', 'pa-second-brain__open', 'Open in Obsidian');
    open.href = openLink;
    open.title = 'Open this folder as a vault in Obsidian';
    const line = node('p', 'pa-folder__note');
    line.append(open);
    body.append(line);
  }
  if (view.capture) renderCapture(body);
  renderProjects(body);
  body.append(
    actionRow(view.actions, action => {
      if (action.id === 'again') void lookAgain();
      else if (action.id === 'disconnect') void disconnect();
      else if (action.id === 'setup') startSetup();
      else {
        state.message = '';
        go('chooser');
      }
    })
  );
}

// renderProjects lists the project folders with Make a workspace, which opens
// Create Workspace on that folder (FR18). It creates nothing itself.
function renderProjects(body) {
  const projects = secondBrainProjectsView(state.secondBrain);
  if (!projects.length) return;
  body.append(node('p', 'pa-folder__note', 'Projects'));
  const list = node('ul', 'pa-second-brain__vaults');
  list.setAttribute('aria-label', 'Projects');
  projects.forEach(project => {
    const item = node('li', 'pa-second-brain__vault');
    item.append(node('span', 'pa-second-brain__vault-detail', project.name));
    const make = button(project.action, () => makeWorkspace(project.name));
    make.setAttribute('aria-label', project.action.ariaLabel);
    item.append(make);
    list.append(item);
  });
  body.append(list);
}

// makeWorkspace opens Create Workspace with the project's folder chosen, by
// name; the page that owns the form looks the folder up.
function makeWorkspace(name) {
  const manager = window.sessionManager;
  if (typeof manager?.openSecondBrainProjectWorkspace === 'function') {
    void manager.openSecondBrainProjectWorkspace(name);
    return;
  }
  window.location.assign(secondBrainProjectLink(name));
}

// renderCapture is the small box on the card of a folder Ori set up: a note
// goes to Inbox as its own file, the same place "note this" in chat writes.
function renderCapture(body) {
  const box = node('div', 'pa-second-brain__capture');
  // A block label, so the box can fill the card instead of the label's width.
  const label = node('label', 'pa-folder__note d-block', 'Note something for your Inbox ');
  const field = node('textarea', 'modern-input w-100');
  field.rows = 2;
  field.maxLength = CAPTURE_MAX_CHARACTERS;
  field.value = state.capture.text;
  field.disabled = state.busy;
  field.dataset.secondBrainCapture = '';
  field.addEventListener('input', () => {
    state.capture.text = field.value;
  });
  label.append(field);
  box.append(label);
  if (state.capture.message) {
    const line = node('p', 'pa-folder__note', state.capture.message);
    line.setAttribute('role', 'status');
    if (state.capture.link) {
      const open = node('a', 'pa-second-brain__open', ' Open in Obsidian');
      open.href = state.capture.link;
      line.append(open);
    }
    box.append(line);
  }
  box.append(
    actionRow([{ id: 'capture', label: 'Save to Inbox', style: 'outline' }], () => void capture())
  );
  body.append(box);
}

// capture saves the note in the box. One request id per note, kept until it
// is saved, so a retry after a lost answer replays instead of writing twice.
async function capture() {
  if (state.busy) return;
  const problem = secondBrainCaptureProblem(state.capture.text);
  if (problem) {
    state.capture.message = problem;
    state.capture.link = '';
    render();
    return;
  }
  state.busy = true;
  state.capture.message = '';
  state.capture.link = '';
  if (!state.capture.requestId) state.capture.requestId = newRequestID('capture');
  render();
  const { ok, payload } = await request('/capture', {
    method: 'POST',
    body: { text: state.capture.text, request_id: state.capture.requestId }
  }).catch(() => ({ ok: false, payload: {} }));
  state.busy = false;
  if (!ok) {
    state.capture.message = secondBrainError(payload, 'That note was not saved. Try again.');
    render();
    return;
  }
  state.capture = {
    text: '',
    requestId: '',
    message: secondBrainCaptureReceipt(payload.capture),
    link: String(payload.capture?.link || '').startsWith('obsidian://open?')
      ? payload.capture.link
      : ''
  };
  await refresh();
  render();
  announce();
}

// startSetup opens Set one up for me at its first step, from the ask, the
// chooser, or the Notes station's card.
function startSetup() {
  // One request id for the whole attempt: a retry after a lost answer replays
  // the first call on the server instead of making a second folder.
  state.setup = {
    parent: null,
    name: DEFAULT_SECOND_BRAIN_NAME,
    message: '',
    requestId: newRequestID('create'),
    useDefault: Boolean(state.secondBrain?.setup_default)
  };
  showError('');
  go('setup');
}

function renderSetup(body) {
  if (state.setup.parent) {
    renderSetupConfirm(body);
    return;
  }
  if (state.setup.useDefault && state.secondBrain?.setup_default) {
    renderSetupDefault(body);
    return;
  }
  const view = secondBrainSetupWhereView({
    pickerOff: state.pickerOff,
    message: state.setup.message
  });
  body.append(node('p', 'pa-specialist-offer__headline', view.title));
  body.append(node('p', 'pa-folder__note', view.note));
  if (view.message) body.append(node('p', 'pa-folder__note', view.message));
  if (!view.pickerOff) {
    body.append(
      actionRow(
        [{ id: 'pick', label: view.pickLabel, style: 'primary' }],
        () => void pickFolder('setup')
      )
    );
  }
  if (!setupPathEntry) {
    setupPathEntry = createFolderPathEntry({
      title: 'Choose where to set up your second brain',
      placeholder: '~/Documents',
      onSelect: selection =>
        chooseParent({
          selectionToken: selection.selectionToken,
          name: baseName(selection.path),
          path: selection.path
        })
    });
  }
  setupPathEntry.setNote(view.pickerOff);
  setupPathEntry.setDisabled(state.busy);
  body.append(setupPathEntry.element);
  body.append(
    actionRow([{ id: 'back', label: 'Back', style: 'link' }], () => {
      if (state.secondBrain?.setup_default) {
        state.setup.useDefault = true;
        state.setup.message = '';
        render();
      } else back();
    })
  );
}

function renderSetupDefault(body) {
  const view = secondBrainSetupDefaultView(state.secondBrain.setup_default);
  body.append(node('p', 'pa-specialist-offer__headline', view.title));
  if (view.path) body.append(node('p', 'pa-second-brain__path', view.path));
  const list = node('ul', 'pa-folder__receipt pa-second-brain__consent');
  list.dataset.layout = 'list';
  view.lines.forEach(line => list.append(node('li', '', line)));
  body.append(list);
  body.append(
    actionRow(view.actions, action => {
      if (action.id === 'create') void createSecondBrain();
      else if (action.id === 'elsewhere') {
        state.setup.useDefault = false;
        state.setup.message = '';
        render();
      } else void decide('later');
    })
  );
}

function renderSetupConfirm(body) {
  const view = secondBrainSetupConfirmView(state.setup.parent, state.setup.name, {
    message: state.setup.message
  });
  body.append(node('p', 'pa-specialist-offer__headline', view.title));
  if (view.path) body.append(node('p', 'pa-second-brain__path', view.path));
  const field = node('label', 'pa-folder__note');
  field.append(node('span', '', `${view.nameLabel} `));
  const input = node('input', 'modern-input');
  input.type = 'text';
  input.value = view.name;
  input.maxLength = 255;
  input.disabled = state.busy;
  input.setAttribute('autocomplete', 'off');
  input.setAttribute('spellcheck', 'false');
  input.dataset.secondBrainName = '';
  input.addEventListener('input', () => {
    state.setup.name = input.value;
  });
  input.addEventListener('keydown', event => {
    if (event.key === 'Enter') {
      event.preventDefault();
      void createSecondBrain();
    }
  });
  field.append(input);
  body.append(field);
  if (view.message) body.append(node('p', 'pa-folder__note is-error', view.message));
  const list = node('ul', 'pa-folder__receipt pa-second-brain__consent');
  list.dataset.layout = 'list';
  view.lines.forEach(line => list.append(node('li', '', line)));
  body.append(list);
  body.append(
    actionRow(view.actions, action => {
      if (action.id === 'create') void createSecondBrain();
      else if (action.id === 'back') {
        state.setup.parent = null;
        state.setup.message = '';
        render();
      } else void decide('later');
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
      actionRow(
        [{ id: 'pick', label: view.pickLabel, style: 'outline' }],
        () => void pickFolder('connect')
      )
    );
  }
  body.append(actionRow([view.setUp], startSetup));
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
  const openLink = secondBrainOpenLink(state.secondBrain);
  if (openLink) {
    const open = node('a', 'pa-second-brain__open', 'Open in Obsidian');
    open.href = openLink;
    open.title = 'Open this folder as a vault in Obsidian';
    const line = node('p', 'pa-folder__note');
    line.append(open);
    body.append(line);
  }
  if (
    secondBrainFolderOfferShows({
      justSetUp: state.offerFolder,
      headline: view.headline,
      dismissed: state.offerDismissed
    })
  )
    renderFolderOffer(body, view.headline);
  // A result waiting under Needs you leads to the HQ's Notes panel.
  if (state.pinned && state.panelRoute) {
    const panel = node('a', 'pa-second-brain__open', 'See it all in Notes');
    panel.href = state.panelRoute;
    const line = node('p', 'pa-folder__note');
    line.append(panel);
    body.append(line);
  }
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
      box.disabled = state.busy || view.acted || item.seeded;
      label.append(box, document.createTextNode(` ${item.text}`));
      if (item.due) label.append(node('span', 'pa-second-brain__due', ` · ${item.due}`));
      if (item.seeded)
        label.append(node('span', 'pa-second-brain__due', ' · already in your follow-ups'));
      fieldset.append(label);
    });
    form.append(fieldset);
  });
  details.append(form);
  // Nothing to add when every item is one Ori copied from a follow-up.
  const addable = view.groups.some(group => group.items.some(item => !item.seeded));
  if (!view.acted && addable) {
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
    else if (state.phase === 'setup') renderSetup(els.body);
    else if (state.phase === 'consent') renderConsent(els.body);
    else if (state.phase === 'manage') renderManage(els.body);
    else if (state.phase === 'fillin') renderFillIn(els.body);
    else if (state.phase === 'learned') renderLearned(els.body);
    else if (state.phase === 'project') renderProject(els.body);
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

// chooseParent keeps the folder the user picked to set a second brain up in.
function chooseParent(parent) {
  state.setup.parent = parent;
  state.setup.message = '';
  showError('');
  setPhase('setup');
}

// pickFolder opens the native dialog for connecting a folder ('connect') or
// for the parent of a second brain Ori sets up ('setup').
async function pickFolder(purpose = 'connect') {
  if (state.busy) return;
  const setup = purpose === 'setup';
  state.busy = true;
  render();
  try {
    const response = await fetch(FOLDER_PATH_ENDPOINT, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
      body: JSON.stringify({
        title: setup ? 'Choose where to set up your second brain' : 'Choose your notes folder'
      })
    });
    const result = (await readJSON(response)) || {};
    const off = folderDialogOffNote(result);
    if (off) {
      state.pickerOff = off;
    } else if (response.ok && result.selected && result.selection_token) {
      state.busy = false;
      const picked = {
        selectionToken: result.selection_token,
        name: baseName(result.path),
        path: String(result.path || '')
      };
      if (setup) chooseParent(picked);
      else await chooseFolder(picked);
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
  setPhase(setup ? 'setup' : 'chooser');
}

// createSecondBrain asks the server to make the folder inside the picked
// parent or the default place, seed it, connect it, and start the first look.
// The request carries the parent's selection token and the name, or neither
// for the default place, never a path.
async function createSecondBrain() {
  const parent = state.setup.parent;
  const place = parent ? null : state.setup.useDefault && state.secondBrain?.setup_default;
  if (state.busy || (!parent && !place)) return;
  const problem = secondBrainFolderNameProblem(state.setup.name);
  if (problem) {
    state.setup.message = problem;
    render();
    return;
  }
  const name = String(state.setup.name).trim();
  state.busy = true;
  state.setup.message = '';
  showError('');
  render();
  const { ok, payload } = await request('/create', {
    method: 'POST',
    body: parent
      ? { selection_token: parent.selectionToken, name, request_id: state.setup.requestId }
      : { request_id: state.setup.requestId }
  }).catch(() => ({ ok: false, payload: {} }));
  state.busy = false;
  if (!ok) {
    if (payload.needs_pick) state.setup.parent = null;
    if (payload.needs_pick || payload.repair === 'choose_folder') {
      // The default place is taken or gone: only choosing another can help.
      state.setup.useDefault = false;
      state.setup.message = secondBrainError(payload);
      render();
      return;
    }
    showError(secondBrainError(payload));
    render();
    return;
  }
  state.secondBrain = payload.second_brain || state.secondBrain;
  state.chosen = { name, path: parent ? parent.path : place.path };
  state.receipt = '';
  // The first look ends with the first thing to show Ori (FR13, FR14).
  state.offerFolder = true;
  state.offerDismissed = false;
  void loadFolderChips();
  state.look = { state: 'running', look_id: payload.look_id };
  state.polls = 0;
  setPhase('reading', { focus: false });
  announce();
  schedulePoll();
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
  } else if (action.id === 'setup') {
    startSetup();
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
  await showNextCard();
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
    // One card at a time: a result still waiting goes first, then the consent
    // question (FR10), then the project question (FR20).
    if (state.phase === 'idle' && secondBrainFillInView(state.secondBrain).show) {
      state.pinned = true;
      setPhase('fillin', { focus: false });
    } else {
      await showProjectAskIfDue();
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
    // Set one up for me, straight to its first step, as the conversation's
    // latest reply.
    openSetup: async () => {
      if (!state.secondBrain) await load();
      state.pinned = false;
      state.latest = true;
      state.history = [];
      startSetup();
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
    // The first-batch report's Review what Ori learned: the card of what the
    // user's approved filing taught Ori, answered in place.
    openLearned,
    // The project question, on request (the drawer asks it by itself in order).
    openProject: showProjectAskIfDue,
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
        ['result', 'reading', 'failed', 'manage', 'fillin', 'learned', 'project'].includes(
          state.phase
        )
      ) {
        clearTimeout(state.pollTimer);
        state.pinned = false;
        setPhase('idle', { focus: false });
      }
      announce();
    },
    current: () => state.secondBrain,
    // Today says where a waiting result's card leads: the HQ's Notes panel.
    setPanelRoute: route => {
      if (state.panelRoute === route) return;
      state.panelRoute = route;
      if (state.pinned) setPhase(state.phase, { focus: false });
    }
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
