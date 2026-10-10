// One local folder preview per personal conversation. No paths, observations or
// authority are recovered from browser storage. Only Send shares a reference.
import { folderFocusView, folderTreeView } from './personal-assistant-folder-tree.js';
import { initFolderExplorer } from './personal-assistant-folder-explorer.js';
import { folderChooserView } from './personal-assistant-folder-chooser.js';
import { collectCurrentWorkspaceContext } from './personal-assistant-workspace-context.js';
import {
  currentFolderDiscussion,
  folderDiscussionOptions,
  folderDiscussionText,
  folderPresentation,
  renderFolderSummary
} from './personal-assistant-folder-presentation.js';
export { observationSummary, coverageSummary } from './personal-assistant-folder-presentation.js';

const ENDPOINT = '/api/home-assistant/folder-context';
export const FOLDER_DISCLOSURE =
  'File contents have not been read. Send shares the recorded file/folder and project names, kinds, counts, project markers, scan time and coverage with your configured model. Selecting a folder stays local.';

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
  async function select(mode, chip = '') {
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
        ...(chip ? { chip } : {})
      });
      if (generation !== state.generation || owner !== currentId()) return false;
      if (result.cancelled || !result.observation) {
        notify('Selection cancelled. Your draft and previous folder are unchanged.');
        return false;
      }
      state.observation = result.observation;
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
  function request() {
    if (!state.observation || state.pending || state.conversationId !== currentId()) return null;
    return {
      selection_id: state.observation.id,
      revision: state.revision,
      ...(state.conversationId ? {} : { draft_id: state.draftId }),
      ...(state.authority ? { historical: true } : {}),
      ...(state.focusIDs.length ? { focus_ids: [...state.focusIDs] } : {})
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
      !folderFocusView(state.observation, ids)
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
                : {})
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
  return { state, select, remove, reset, request, setFocus, accepted, adopt, review, notify };
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
  if (
    existing?.parentElement === row.firstElementChild &&
    existing.dataset.folderGeneration === String(discussionBinding.generation)
  )
    return;
  existing?.remove();
  const value = { ...discussionBinding };
  const strip = node('div', '', 'personal-assistant-folder-discussion');
  strip.dataset.folderDiscussion = value.messageId;
  strip.dataset.folderGeneration = String(value.generation);
  strip.setAttribute('role', 'group');
  strip.setAttribute('aria-label', 'Folder conversation choices');
  const children = folderDiscussionOptions(controller.state.observation);
  const historical = Boolean(controller.state.authority) || value.restored === true;
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
  updateSendHint();
  window.PersonalAssistantPanel?.setFolderBusy?.(state.pending, 'context');
  window.PersonalAssistantConversation?.refresh?.();
  window.PersonalAssistantFolder?.contextProgress?.(state);
  window.PersonalAssistantFolderSetup?.contextChanged?.(state);
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
    const view = folderChooserView(await jsonRequest(`${ENDPOINT}/choices`));
    if (generation !== chooserGeneration) return;
    const choices = view.chips.map(chip => ({ label: chip.label, mode: 'chip', chip: chip.id }));
    if (view.pickerVisible) choices.push({ label: view.pickerLabel, mode: 'picker' });
    elements.choices.replaceChildren();
    for (const choice of choices) {
      const button = node('button', choice.label, 'personal-assistant-panel__chip');
      button.type = 'button';
      button.addEventListener('click', async () => {
        closeChooser();
        const selectedGeneration = chooserGeneration;
        const selected = await controller.select(choice.mode, choice.chip);
        if (selected && folderTreeView(controller.state.observation))
          explorer?.explore({ automatic: true });
        // Disabling Add during selection can move focus to body. Restore it
        // only if the user has not moved elsewhere or left this conversation.
        if (selectedGeneration === chooserGeneration && document.activeElement === document.body)
          elements.add.focus();
      });
      elements.choices.append(button);
    }
    if (view.note) elements.choices.append(node('p', view.note));
    elements.choices.querySelector('button')?.focus();
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
