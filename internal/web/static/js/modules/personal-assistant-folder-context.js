// One local folder preview per personal conversation. No paths, observations or
// authority are recovered from browser storage. Only Send shares a reference.
import { folderSelectionFocus, folderTreeView } from './personal-assistant-folder-tree.js';
import { initFolderExplorer } from './personal-assistant-folder-explorer.js';
import { folderChooserView } from './personal-assistant-folder-chooser.js';
import { createFolderPathEntry } from './folder-path-entry.js';
import { collectCurrentWorkspaceContext } from './personal-assistant-workspace-context.js';
import {
  currentFolderDiscussion,
  folderDiscussionOptions,
  folderDiscussionText,
  folderPresentation,
  renderFolderSummary
} from './personal-assistant-folder-presentation.js';
import {
  declinedLine,
  proposalView,
  renderProposal,
  structureOption
} from './personal-assistant-proposal.js';
export { observationSummary, coverageSummary } from './personal-assistant-folder-presentation.js';

const ENDPOINT = '/api/home-assistant/folder-context';
// Ask again: undoes one folder's No thanks by the key its decline carried.
const FORGET_ENDPOINT = '/api/personal-assistant/folder-digest/forget';
export const FOLDER_DISCLOSURE =
  'File contents have not been read. Send shares the recorded file/folder and project names, kinds, counts, project markers, last-changed dates, scan time and coverage with your configured model. Selecting a folder stays local.';

/** Pure state machine with injected I/O: stale picker/network results cannot
 * cross a conversation switch, removal or replacement. */
export function createFolderContextController({
  post,
  changed = () => {},
  uuid,
  currentId,
  isBusy = () => false
}) {
  const state = {
    conversationId: currentId(),
    draftId: uuid(),
    revision: '',
    observation: null,
    // The group Ori proposes for a folder that holds several projects, as the
    // attach response described it. A local preview only: after Send the
    // reply's own suggestion carries the proposal.
    structure: null,
    // The earlier No thanks on that folder, and why no card could be shown.
    declined: null,
    structureUnavailable: '',
    focusIDs: [],
    accepted: null,
    offerId: '',
    authority: '',
    preview: false,
    pending: false,
    selecting: false,
    scanName: '',
    generation: 0,
    notice: ''
  };
  const target = () => ({
    ...(state.conversationId
      ? { conversation_id: state.conversationId }
      : { draft_id: state.draftId }),
    revision: state.revision
  });
  const notify = message => {
    state.notice = message;
    changed(state);
  };
  function reset(id = '', saved = {}) {
    state.generation++;
    Object.assign(state, {
      conversationId: id,
      draftId: uuid(),
      revision: saved.revision || '',
      observation: saved.observation || null,
      structure: null,
      declined: null,
      structureUnavailable: '',
      focusIDs: [],
      accepted: saved.observation || null,
      offerId: saved.offer_id || '',
      authority: saved.authority || (saved.historical ? state.authority || 'historical' : ''),
      preview: false,
      pending: false,
      selecting: false,
      scanName: '',
      notice: ''
    });
    changed(state);
  }
  // selectionToken goes with mode 'selection': the receipt for a typed path
  // the folder-picker endpoint accepted. The path itself is never sent here.
  async function select(mode, chip = '', selectionToken = '') {
    if (isBusy()) {
      notify('Wait for the current reply before changing its folder.');
      return false;
    }
    const generation = ++state.generation;
    const owner = state.conversationId;
    state.pending = true;
    state.selecting = true;
    state.scanName = chip ? chip[0].toUpperCase() + chip.slice(1) : '';
    notify('Looking at folder metadata locally… No file contents are read.');
    try {
      const result = await post(`${ENDPOINT}/select`, {
        ...target(),
        mode,
        ...(chip ? { chip } : {}),
        ...(selectionToken ? { selection_token: selectionToken } : {})
      });
      if (generation !== state.generation || owner !== currentId()) return false;
      if (result.cancelled || !result.observation) {
        notify('Selection cancelled. Your draft and previous folder are unchanged.');
        return false;
      }
      state.observation = result.observation;
      state.structure = result.structure?.version === 1 ? result.structure : null;
      state.declined = state.structure ? folderDecline(result.declined) : null;
      state.structureUnavailable = state.structure
        ? ''
        : String(result.structure_unavailable || '');
      state.focusIDs = [];
      if (typeof result.revision === 'string' && result.revision !== state.revision) {
        state.revision = result.revision;
        state.accepted = null; // server retired the previous binding/reviews
        state.offerId = '';
      }
      state.authority = '';
      state.preview = true;
      notify('Local preview only — not sent to the model yet.');
      return true;
    } catch (error) {
      if (generation === state.generation && owner === currentId())
        notify(
          error.message ||
            'The folder could not be inspected. Try Add folder again; your draft is unchanged.'
        );
      return false;
    } finally {
      if (generation === state.generation) {
        state.pending = false;
        state.selecting = false;
        changed(state);
      }
    }
  }
  async function remove() {
    if (isBusy()) {
      notify('Wait for the current reply before removing its folder.');
      return false;
    }
    const generation = ++state.generation;
    const owner = state.conversationId;
    state.pending = true;
    try {
      // Detaching a purely local preview requires no write. If this conversation
      // has an accepted binding, detach it even when a replacement is previewed.
      if (state.accepted && owner) {
        const result = await post(`${ENDPOINT}/detach`, {
          conversation_id: owner,
          revision: state.revision
        });
        if (generation !== state.generation || owner !== currentId()) return false;
        state.revision = result.revision;
      }
      state.observation = null;
      state.structure = null;
      state.declined = null;
      state.structureUnavailable = '';
      state.focusIDs = [];
      state.accepted = null;
      state.offerId = '';
      state.preview = false;
      state.authority = '';
      notify(
        'Folder removed from future context. Earlier discussion remains history; completed setup is unchanged.'
      );
      return true;
    } catch (error) {
      if (generation === state.generation && owner === currentId())
        notify(
          error.message ||
            'Could not remove the saved folder. Reopen the conversation and try again.'
        );
      return false;
    } finally {
      if (generation === state.generation) {
        state.pending = false;
        changed(state);
      }
    }
  }
  // askAgain undoes the attached folder's earlier No thanks. Only the key its
  // decline carried is sent; the folder is not looked at again, and its card
  // simply gets Set up back.
  async function askAgain() {
    if (!state.declined || state.pending) return false;
    const generation = state.generation;
    state.pending = true;
    changed(state);
    try {
      await post(FORGET_ENDPOINT, { folder_key: state.declined.folder_key });
      if (generation !== state.generation) return false;
      state.declined = null;
      return true;
    } catch (error) {
      if (generation === state.generation)
        notify(error.message || 'Could not ask about this folder again right now. Try again.');
      return false;
    } finally {
      if (generation === state.generation) {
        state.pending = false;
        changed(state);
      }
    }
  }
  function request() {
    if (!state.observation || state.pending || state.conversationId !== currentId()) return null;
    const focus = folderSelectionFocus(state.observation, state.focusIDs);
    if (!focus) return null;
    return {
      selection_id: state.observation.id,
      revision: state.revision,
      ...(state.conversationId ? {} : { draft_id: state.draftId }),
      ...(state.authority ? { historical: true } : {}),
      // All checked entries may resolve to whole-folder discussion. Never send
      // an invalid list or silently truncate the user's selected topics.
      ...(focus.topics.length ? { focus_ids: [...state.focusIDs] } : {})
    };
  }
  function setFocus(ids, binding) {
    if (
      !state.observation ||
      state.pending ||
      state.conversationId !== currentId() ||
      (binding &&
        (binding.observationId !== state.observation.id ||
          binding.generation !== state.generation ||
          binding.conversationId !== state.conversationId)) ||
      !folderSelectionFocus(state.observation, ids)
    )
      return false;
    state.focusIDs = [...ids];
    changed(state);
    return true;
  }
  function accepted(id, saved) {
    if (!saved) return;
    // Checks changed while the model replied apply only to the NEXT turn. Do
    // not replace them with the already-frozen sent turn's focus.
    const focusIDs =
      state.observation?.id === saved.observation?.id &&
      (!state.conversationId || state.conversationId === id)
        ? [...state.focusIDs]
        : [];
    reset(id, saved);
    state.focusIDs = focusIDs;
    changed(state);
  }
  // A turn without a folder can be the one that saves the conversation. The
  // next Add folder must then target that conversation, not the draft it was
  // before; otherwise its result is discarded as belonging to another thread.
  function adopt(id) {
    const next = String(id || '');
    if (!next || state.conversationId === next || state.observation || state.pending) return false;
    state.generation++;
    Object.assign(state, { conversationId: next, revision: '', accepted: null, offerId: '' });
    changed(state);
    return true;
  }
  async function review(candidateId, close = false, offerId = state.offerId, placement = {}) {
    if (
      isBusy() ||
      state.pending ||
      (close ? !state.conversationId || !offerId : !state.observation || state.authority)
    )
      return null;
    const generation = ++state.generation;
    const owner = state.conversationId;
    state.pending = true;
    notify(
      close
        ? 'Closing this review…'
        : 'Preparing the existing setup review… Nothing is created or sent to a model.'
    );
    try {
      const result = await post(`${ENDPOINT}/review${close ? '/close' : ''}`, {
        ...target(),
        ...(close
          ? { offer_id: offerId }
          : {
              selection_id: state.observation.id,
              candidate_id: candidateId,
              ...(typeof window !== 'undefined'
                ? {
                    context: {
                      ...collectCurrentWorkspaceContext(),
                      // The workspace the user named for this suggestion. The host
                      // resolves it again; the page stays the location.
                      ...(placement.subject_workspace_id
                        ? { subject_workspace_id: String(placement.subject_workspace_id) }
                        : {})
                    }
                  }
                : {}),
              ...(placement.operation
                ? { operation: placement.operation, destination_id: placement.destination_id || '' }
                : {}),
              // What the user changed in Adjust on a proposed group.
              ...(placement.structure ? { structure: placement.structure } : {})
            })
      });
      if (generation !== state.generation || currentId() !== owner) return null;
      return result;
    } catch (error) {
      if (generation === state.generation)
        notify(
          error.message ||
            'The review could not be saved. Reopen this conversation before retrying.'
        );
      return null;
    } finally {
      if (generation === state.generation) {
        state.pending = false;
        changed(state);
      }
    }
  }
  return {
    state,
    select,
    remove,
    reset,
    request,
    setFocus,
    accepted,
    adopt,
    review,
    notify,
    askAgain
  };
}

// folderContextChoices is what the Add folder sheet shows for the server's
// choices: one button per approved choice, then the sentence that explains a
// missing dialog. Pure, so the sheet's wording is testable without a page.
export function folderContextChoices(payload) {
  const view = folderChooserView(payload);
  const choices = view.chips.map(chip => ({ label: chip.label, mode: 'chip', chip: chip.id }));
  if (view.pickerVisible) choices.push({ label: view.pickerLabel, mode: 'picker' });
  return { choices, note: view.note };
}

async function jsonRequest(url, body) {
  const response = await fetch(url, {
    method: body ? 'POST' : 'GET',
    headers: {
      Accept: 'application/json',
      ...(body ? { 'Content-Type': 'application/json' } : {})
    },
    ...(body ? { body: JSON.stringify(body) } : {})
  });
  const data = await response.json();
  if (!response.ok)
    throw new Error(
      data.message || 'The folder controls are unavailable. Your draft is unchanged.'
    );
  return data;
}

let controller;
let explorer;
let elements;
let chooserGeneration = 0;
let lastEventKey;
let discussionBinding = null;
let discussionTrigger = null;
let discussionChoiceBinding = null;

function closeDiscussionChooser({ restoreFocus = true } = {}) {
  if (elements?.discussionChooser) elements.discussionChooser.hidden = true;
  discussionTrigger?.setAttribute('aria-expanded', 'false');
  if (restoreFocus && discussionTrigger?.isConnected) discussionTrigger.focus();
  discussionTrigger = null;
  discussionChoiceBinding = null;
}

function discussionRow(value) {
  if (
    !currentFolderDiscussion(
      controller?.state,
      value,
      window.PersonalAssistantConversation?.currentId?.()
    ) ||
    window.PersonalAssistantConversation?.isLoading?.() ||
    window.OriAskRouting?.getState?.().busy ||
    window.PersonalAssistantPanel?._state?.view?.available === false
  )
    return null;
  return (
    Array.from(document.querySelectorAll('#homeAssistantConversation [data-message-id]')).find(
      row =>
        row.dataset.messageId === value.messageId &&
        row.dataset.conversationId === value.conversationId &&
        row.dataset.messageRole === 'assistant'
    ) || null
  );
}

function discussionDraft(value, projectId = '') {
  if (!discussionRow(value)) return false;
  const text = folderDiscussionText(controller.state.observation, projectId, {
    historical: Boolean(controller.state.authority) || value.restored === true
  });
  const accepted = Boolean(text && window.PersonalAssistantPanel?.suggestReply?.(text));
  // Legacy draft shortcuts must not conflict with a different checkbox focus.
  // A rejected suggestion preserves both the exact text AND the next focus.
  if (accepted) controller.setFocus([]);
  return accepted;
}

// discussionChips are the controls under a folder reply. A folder Ori proposed
// a group for gets the proposal's own controls, and choosing one folder moves
// into Adjust; any other folder keeps the two discussion shortcuts.
export function discussionChips({
  structure = false,
  historical = false,
  children = 0,
  markers = false
}) {
  if (structure && !historical)
    return [
      { id: 'setup', label: 'Set up this structure' },
      { id: 'adjust', label: 'Adjust…' },
      { id: 'discuss', label: 'Just discuss' }
    ];
  const chips = [
    {
      id: 'whole',
      label: historical
        ? 'Discuss saved observations'
        : children > 1
          ? 'Discuss the collection'
          : 'Discuss this folder'
    }
  ];
  if (children)
    chips.push({ id: 'choose', label: markers ? 'Choose a project…' : 'Choose a folder…' });
  return chips;
}

// folderDecline is the host's record of an earlier No thanks on a folder, or
// null. Only its closed fields are kept: the folder's key (never a path), its
// name and when.
export function folderDecline(value) {
  const key = String(value?.folder_key || '');
  const at = new Date(String(value?.at || ''));
  if (
    !/^[0-9a-f]{64}$/.test(key) ||
    !String(value?.name || '').trim() ||
    Number.isNaN(at.getTime())
  )
    return null;
  return { folder_key: key, name: String(value.name).trim(), at: value.at };
}

// Why no card could be shown for an attached collection, in one line.
const STRUCTURE_UNAVAILABLE = {
  review_pending:
    'Another setup review is open. Finish or close it to see the proposal for this folder.',
  setup_unavailable: 'Setting up workspaces is not available here.'
};

// structureCardView is the whole decision for the card shown as soon as a
// collection is attached, before Send: the proposal and its three controls, the
// one line that says why there is none, or nothing. parkedId is the folder the
// user said Not now to in this conversation.
export function structureCardView(state, parkedId = '') {
  const live = Boolean(state?.preview && !state.authority && state.observation);
  const id = String(state?.observation?.id || '');
  if (!live) return { kind: 'none', key: '' };
  if (state.structure) {
    if (parkedId && parkedId === id) return { kind: 'none', key: '' };
    const declined = Boolean(state.declined);
    return {
      kind: 'proposal',
      key: `${id}:${declined ? 'declined' : 'open'}`,
      actions: [
        // An earlier No thanks stands where Set up would, until Ask again.
        declined
          ? {
              id: 'ask',
              label: declinedLine(state.declined, state.structure.folder),
              style: 'btn btn-sm btn-outline-secondary personal-assistant-structure__declined',
              disabled: Boolean(state.pending)
            }
          : {
              id: 'setup',
              label: 'Set up',
              style: 'btn btn-sm btn-primary',
              disabled: Boolean(state.pending)
            },
        {
          id: 'adjust',
          label: 'Adjust…',
          style: 'btn btn-sm btn-outline-secondary',
          disabled: Boolean(state.pending)
        },
        { id: 'later', label: 'Not now', style: 'btn btn-sm btn-link', disabled: false }
      ]
    };
  }
  const line = STRUCTURE_UNAVAILABLE[state.structureUnavailable] || '';
  return line ? { kind: 'unavailable', key: `${id}:unavailable`, line } : { kind: 'none', key: '' };
}

// The folder the user said Not now to: its card stays away for this
// conversation, and nothing is written.
let parkedStructure = '';

// The proposal card shown as soon as a collection is attached, before Send.
// It is drawn from the attach response alone; pressing a control is what
// starts a request, and Not now starts none.
function renderStructure(state) {
  const host = elements?.structure;
  if (!host) return;
  const view = structureCardView(state, parkedStructure);
  if (host.dataset.structureKey === view.key) {
    for (const action of view.actions || []) {
      const button = host.querySelector(`[data-structure-action="${action.id}"]`);
      if (button) button.disabled = action.disabled;
    }
    return;
  }
  host.dataset.structureKey = view.key;
  host.replaceChildren();
  host.hidden = view.kind === 'none';
  if (view.kind === 'unavailable') {
    host.append(node('p', view.line, 'personal-assistant-structure__unavailable'));
    return;
  }
  if (view.kind !== 'proposal') return;
  host.append(
    renderProposal(
      proposalView(structureOption(state.structure)),
      document,
      window.OriWorkspaceBuildingArt
    )
  );
  const actions = node('div', '', 'personal-assistant-structure__actions');
  const runs = {
    setup: button => window.PersonalAssistantFolderSetup?.reviewStructure?.(button),
    ask: async () => {
      if (await controller.askAgain())
        host.querySelector('[data-structure-action="setup"]')?.focus();
    },
    adjust: button =>
      window.PersonalAssistantFolderSetup?.adjustStructure?.(button, controller.state.structure),
    later: () => {
      parkedStructure = String(controller.state.observation?.id || '');
      renderStructure(controller.state);
      document.getElementById('personalAssistantInput')?.focus();
    }
  };
  for (const action of view.actions) {
    const button = node('button', action.label, action.style);
    button.type = 'button';
    button.dataset.structureAction = action.id;
    button.disabled = action.disabled;
    button.addEventListener('click', () => runs[action.id](button));
    actions.append(button);
  }
  host.append(actions);
}

function renderDiscussion() {
  const existing = document.querySelector('[data-folder-discussion]');
  // A cancelled selection may restore the same canonical snapshot. Recapture
  // its new generation, rebuilding the strip; old captured callbacks still fail.
  if (
    discussionBinding &&
    currentFolderDiscussion(
      controller?.state,
      { ...discussionBinding, generation: controller?.state.generation },
      window.PersonalAssistantConversation?.currentId?.()
    )
  ) {
    discussionBinding = { ...discussionBinding, generation: controller.state.generation };
  }
  const row = discussionRow(discussionBinding);
  if (!row) {
    existing?.remove();
    closeDiscussionChooser({ restoreFocus: false });
    return;
  }
  // The reply's own suggestion says whether Ori proposed a group for this folder.
  const proposed = Boolean(window.PersonalAssistantFolderSetup?.structure?.());
  if (
    existing?.parentElement === row.firstElementChild &&
    existing.dataset.folderGeneration === String(discussionBinding.generation) &&
    existing.dataset.folderStructure === String(proposed)
  )
    return;
  existing?.remove();
  const value = { ...discussionBinding };
  const strip = node('div', '', 'personal-assistant-folder-discussion');
  strip.dataset.folderDiscussion = value.messageId;
  strip.dataset.folderGeneration = String(value.generation);
  strip.dataset.folderStructure = String(proposed);
  strip.setAttribute('role', 'group');
  strip.setAttribute('aria-label', 'Folder conversation choices');
  const children = folderDiscussionOptions(controller.state.observation);
  const historical = Boolean(controller.state.authority) || value.restored === true;
  if (proposed && !historical) {
    // A proposed group: its own controls. Each one checks again, when pressed,
    // that this reply's proposal is still the current one.
    const runs = {
      setup: button => window.PersonalAssistantFolderSetup?.reviewStructure?.(button),
      adjust: button => window.PersonalAssistantFolderSetup?.adjustStructure?.(button),
      discuss: () => document.getElementById('personalAssistantInput')?.focus()
    };
    for (const chip of discussionChips({ structure: true })) {
      const button = node('button', chip.label, 'personal-assistant-conversation__button');
      button.type = 'button';
      button.dataset.structureChip = chip.id;
      button.addEventListener('click', () => {
        if (discussionRow(value)) runs[chip.id](button);
      });
      strip.append(button);
    }
    const bubble = row.firstElementChild;
    bubble?.insertBefore(
      strip,
      bubble.querySelector(
        '.personal-assistant-message__setup, .personal-assistant-message__actions'
      )
    );
    return;
  }
  const whole = node(
    'button',
    historical
      ? 'Discuss saved observations'
      : children.length > 1
        ? 'Discuss the collection'
        : 'Discuss this folder',
    'personal-assistant-conversation__button'
  );
  whole.type = 'button';
  whole.addEventListener('click', () => discussionDraft(value));
  strip.append(whole);
  if (children.length) {
    const choose = node(
      'button',
      children.some(
        child =>
          controller.state.observation.projects.find(project => project.id === child.id)?.marker
      )
        ? 'Choose a project…'
        : 'Choose a folder…',
      'personal-assistant-conversation__button'
    );
    choose.type = 'button';
    choose.setAttribute('aria-controls', 'personalAssistantFolderDiscussionChooser');
    choose.setAttribute('aria-expanded', 'false');
    choose.addEventListener('click', () => {
      if (!discussionRow(value)) return;
      discussionTrigger = choose;
      discussionChoiceBinding = value;
      elements.discussionCandidate.replaceChildren(new Option('Choose an observed folder…', ''));
      for (const candidate of folderDiscussionOptions(controller.state.observation)) {
        const option = new Option(
          candidate.label + (candidate.ambiguous ? ' (indistinguishable name)' : ''),
          candidate.id
        );
        option.disabled = candidate.ambiguous;
        elements.discussionCandidate.add(option);
      }
      choose.setAttribute('aria-expanded', 'true');
      elements.discussionChooser.hidden = false;
      elements.discussionCandidate.focus();
    });
    strip.append(choose);
  }
  const bubble = row.firstElementChild;
  bubble?.insertBefore(
    strip,
    bubble.querySelector('.personal-assistant-message__setup, .personal-assistant-message__actions')
  );
}

function bindDiscussion(value) {
  discussionBinding = value ? { ...value, generation: controller?.state.generation } : null;
  closeDiscussionChooser({ restoreFocus: false });
  renderDiscussion();
}

function updateSendHint() {
  if (!elements?.hint) return;
  elements.hint.hidden =
    !controller?.state.observation ||
    Boolean(document.getElementById('personalAssistantInput')?.value.trim());
}
const node = (tag, text, className = '') => {
  const element = document.createElement(tag);
  element.textContent = text;
  element.className = className;
  return element;
};

function render(state) {
  if (!elements) return;
  elements.active.hidden = !state.observation;
  elements.name.textContent = state.observation?.folder || '';
  elements.remove.disabled = state.pending;
  elements.preview.hidden = !state.observation || !state.preview;
  if (state.observation) {
    renderFolderSummary(elements.summary, state.observation, { local: true });
  }
  elements.history.hidden = !state.authority;
  elements.history.textContent = state.authority
    ? `Discussing saved observations only (${state.authority}). Pick again for fresh inspection or setup. File contents have not been read.`
    : '';
  for (const row of document.querySelectorAll('[data-folder-observation-id]')) {
    const status = row.querySelector('.personal-assistant-folder-context__status');
    if (status)
      status.textContent = folderPresentation(
        { coverage: { partial: row.dataset.folderPartial === 'true' } },
        {
          historical:
            row.dataset.folderHistorical === 'true' ||
            Boolean(state.authority) ||
            state.observation?.id !== row.dataset.folderObservationId
        }
      ).status;
  }
  elements.status.textContent = state.notice;
  renderStructure(state);
  updateSendHint();
  window.PersonalAssistantPanel?.setFolderBusy?.(state.pending, 'context');
  window.PersonalAssistantConversation?.refresh?.();
  window.PersonalAssistantFolder?.contextProgress?.(state);
  window.PersonalAssistantFolderSetup?.contextChanged?.(state);
  window.PersonalAssistantArrangement?.contextChanged?.();
  renderDiscussion();
  explorer?.refresh();
}

function closeChooser({ restoreFocus = true } = {}) {
  chooserGeneration++;
  if (!elements) return;
  elements.chooser.hidden = true;
  elements.add.setAttribute('aria-expanded', 'false');
  if (restoreFocus) elements.add.focus();
}

async function open() {
  if (!elements || !controller || controller.state.pending) return;
  if (window.OriAskRouting?.getState?.().busy) {
    controller.notify('Wait for the current reply before adding a folder.');
    return;
  }
  const generation = ++chooserGeneration;
  elements.chooser.hidden = false;
  elements.add.setAttribute('aria-expanded', 'true');
  elements.choices.replaceChildren(node('p', 'Loading approved folder choices…'));
  // Escape belongs to the chooser even before its asynchronous choices arrive.
  document.getElementById('personalAssistantFolderChooserCancel')?.focus();
  try {
    const { choices, note } = folderContextChoices(await jsonRequest(`${ENDPOINT}/choices`));
    if (generation !== chooserGeneration) return;
    elements.choices.replaceChildren();
    const choose = async (mode, chip, selectionToken) => {
      closeChooser();
      const selectedGeneration = chooserGeneration;
      const selected = await controller.select(mode, chip, selectionToken);
      // A folder Ori proposed a group for stays on the chat side, where that
      // proposal is shown; its tree is one press away (Tree + Chat).
      if (selected && !controller.state.structure && folderTreeView(controller.state.observation))
        explorer?.explore({ automatic: true });
      // Disabling Add during selection can move focus to body. Restore it
      // only if the user has not moved elsewhere or left this conversation.
      if (selectedGeneration === chooserGeneration && document.activeElement === document.body)
        elements.add.focus();
    };
    for (const choice of choices) {
      const button = node('button', choice.label, 'personal-assistant-panel__chip');
      button.type = 'button';
      button.addEventListener('click', () => choose(choice.mode, choice.chip));
      elements.choices.append(button);
    }
    if (note) elements.choices.append(node('p', note));
    // Any other folder: a typed path, checked by the folder-picker endpoint.
    const entry = createFolderPathEntry({
      onSelect: selection => choose('selection', '', selection.selectionToken)
    });
    elements.choices.append(entry.element);
    (elements.choices.querySelector('.personal-assistant-panel__chip') || entry.input).focus();
  } catch (error) {
    if (generation === chooserGeneration)
      elements.choices.replaceChildren(node('p', error.message));
  }
}

function reset(id = '', saved = {}) {
  closeChooser({ restoreFocus: false });
  lastEventKey = undefined;
  bindDiscussion(null);
  controller?.reset(id, saved);
}

// Only the server's typed event channel reaches here. Ordinary model prose
// never becomes a card or an executable setup control.
function renderEvent(id, event, beforeRow, { historical = false } = {}) {
  if (!id || !event) return null;
  const observation = event.observation;
  const key = `${observation?.id || 'removed'}:${event.offer_id || ''}`;
  const rendered = Array.from(document.querySelectorAll('[data-folder-context-key]'));
  if (
    (observation || lastEventKey === key) &&
    rendered.some(row => row.dataset.folderContextKey === key)
  ) {
    lastEventKey = key;
    return null;
  }
  const alreadyObserved =
    observation && rendered.some(row => row.dataset.folderObservationId === observation.id);
  lastEventKey = key;
  const row = node('section', '', 'personal-assistant-folder-context');
  row.dataset.folderEventId = id;
  row.dataset.folderContextKey = key;
  row.dataset.messageRole = 'folder_context';
  row.setAttribute(
    'aria-label',
    observation ? 'Saved folder observations' : 'Folder context removed'
  );
  if (alreadyObserved) {
    row.append(
      node(
        'p',
        `${observation.folder} · workspace setup review`,
        'personal-assistant-folder-context__eyebrow'
      )
    );
  } else if (observation) {
    row.dataset.folderObservationId = observation.id;
    row.dataset.folderHistorical = String(historical);
    row.dataset.folderPartial = String(observation.coverage?.partial === true);
    renderFolderSummary(row, observation, { historical });
  } else {
    row.append(
      node(
        'p',
        'Folder context removed. Earlier discussion remains history; completed setup is unchanged.'
      )
    );
  }
  if (event.offer_id) {
    const slot = node('div', 'Loading saved setup review…');
    slot.dataset.folderReviewId = event.offer_id;
    row.append(slot);
  }
  return window.OriAskRouting?.appendContextEvent?.(row, beforeRow) || null;
}

function init() {
  const add = document.getElementById('personalAssistantFolderChip');
  if (!add || controller) return;
  elements = {
    add,
    chooser: document.getElementById('personalAssistantContextChooser'),
    choices: document.getElementById('personalAssistantFolderChoices'),
    active: document.getElementById('personalAssistantActiveFolder'),
    name: document.getElementById('personalAssistantActiveFolderName'),
    remove: document.getElementById('personalAssistantRemoveFolder'),
    preview: document.getElementById('personalAssistantFolderPreview'),
    summary: document.getElementById('personalAssistantFolderSummary'),
    structure: document.getElementById('personalAssistantFolderStructure'),
    history: document.getElementById('personalAssistantFolderHistorical'),
    status: document.getElementById('personalAssistantContextStatus'),
    hint: document.getElementById('personalAssistantFolderSendHint'),
    discussionChooser: document.getElementById('personalAssistantFolderDiscussionChooser'),
    discussionCandidate: document.getElementById('personalAssistantFolderDiscussionCandidate')
  };
  controller = createFolderContextController({
    post: jsonRequest,
    changed: render,
    uuid: () => crypto.randomUUID(),
    currentId: () => window.PersonalAssistantConversation?.currentId?.() || '',
    isBusy: () =>
      window.OriAskRouting?.getState?.().busy === true ||
      window.PersonalAssistantConversation?.isLoading?.() === true
  });
  explorer = initFolderExplorer({
    current: () => controller.state,
    setFocus: (ids, binding) =>
      !window.PersonalAssistantConversation?.isLoading?.() && controller.setFocus(ids, binding),
    notify: message => controller.notify(message)
  });
  document.getElementById('personalAssistantInput')?.addEventListener('input', updateSendHint);
  document.addEventListener('personal-assistant:status', renderDiscussion);
  document.addEventListener('personal-assistant:sent', () => {
    updateSendHint();
    bindDiscussion(null);
  });
  document
    .getElementById('personalAssistantFolderDiscussionDraft')
    ?.addEventListener('click', () => {
      const value = discussionChoiceBinding;
      if (!discussionRow(value) || !elements.discussionCandidate.reportValidity()) return;
      const candidate = elements.discussionCandidate.value;
      closeDiscussionChooser({ restoreFocus: false });
      discussionDraft(value, candidate);
    });
  document
    .getElementById('personalAssistantFolderDiscussionCancel')
    ?.addEventListener('click', () => closeDiscussionChooser());
  document.addEventListener(
    'keydown',
    event => {
      if (event.key !== 'Escape' || elements.discussionChooser.hidden) return;
      event.preventDefault();
      event.stopPropagation();
      closeDiscussionChooser();
    },
    true
  );
  elements.remove.addEventListener('click', async () => {
    const saved = controller.state.accepted;
    if (await controller.remove()) {
      if (saved) renderEvent(controller.state.revision, { version: 1 });
      elements.add.focus();
    }
  });
  document
    .getElementById('personalAssistantFolderChooserCancel')
    .addEventListener('click', closeChooser);
  elements.chooser.addEventListener('keydown', event => {
    if (event.key !== 'Escape') return;
    event.preventDefault();
    event.stopPropagation();
    closeChooser();
  });
  render(controller.state);
}

const api = {
  open,
  close: () => {
    closeChooser({ restoreFocus: false });
    closeDiscussionChooser({ restoreFocus: false });
  },
  bindDiscussion,
  refreshDiscussion: () => {
    renderDiscussion();
    explorer?.refresh();
  },
  explore: () => explorer?.explore() || false,
  showChat: () => explorer?.showChat(),
  reset,
  hydrate: (id, saved) => {
    // Resume has already rendered canonical events; keep their deduplication key.
    closeChooser({ restoreFocus: false });
    explorer?.collapse({ focus: false });
    controller?.reset(id, saved);
  },
  renderEvent,
  guide: () => {
    if (controller && !controller.state.observation && !controller.state.conversationId)
      controller.notify(
        'Add a folder to discuss its structure, or review a workspace setup. Nothing happens automatically.'
      );
  },
  review: (candidateId, close = false, offerId, placement) =>
    controller?.review(candidateId, close, offerId, placement),
  current: () => controller?.state,
  notify: message => controller?.notify(message),
  resetEvents: () => {
    lastEventKey = undefined;
  },
  request: () => controller?.request() || null,
  // Attaches the folder a server-held selection names (a typed path the
  // server checked): the same local preview the chooser makes.
  selectHeld: selectionToken =>
    controller ? controller.select('selection', '', selectionToken) : Promise.resolve(false),
  accepted: (id, saved) => controller?.accepted(id, saved),
  adopt: id => controller?.adopt(id) === true,
  isPending: () => controller?.state.pending === true,
  hasFolder: () => Boolean(controller?.state.observation),
  _controller: () => controller
};
if (typeof window !== 'undefined') window.PersonalAssistantFolderContext = api;
if (typeof document !== 'undefined') {
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', init);
  else init();
}
