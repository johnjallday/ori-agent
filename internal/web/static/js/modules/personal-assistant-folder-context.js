// One local folder preview per personal conversation. No paths, observations or
// authority are recovered from browser storage. Only Send shares a reference.
import { folderChooserView } from './personal-assistant-folder-chooser.js';
import { collectWorkspaceContext } from './personal-assistant-workspace-context.js';

const ENDPOINT = '/api/home-assistant/folder-context';
export const FOLDER_DISCLOSURE =
  'File contents have not been read. Send shares the observed folder/project names, kinds, counts, project markers, scan time and coverage with your configured model. Selecting a folder stays local.';

export function observationSummary(observation) {
  if (!observation) return '';
  const kinds = (observation.kinds || []).map(kind => `${kind.count} ${kind.name}`).join(' · ');
  const partial = observation.coverage?.partial ? 'Partial look' : 'Bounded look';
  const files = observation.files || 0;
  return `${partial}: ${files} ${files === 1 ? 'file' : 'files'} observed${kinds ? ` · ${kinds}` : ''}.`;
}

export function coverageSummary(observation) {
  const coverage = observation?.coverage || {};
  const date = new Date(observation?.scanned_at || '');
  const when = Number.isNaN(date.getTime()) ? 'Unknown scan time' : date.toLocaleString();
  const omissions = (coverage.projects_omitted || 0) + (coverage.kinds_omitted || 0);
  return `${when}. Up to ${coverage.max_depth || 3} levels, ${coverage.max_entries || 5000} entries and ${coverage.budget_seconds || 3} seconds. Hidden/tooling folders, links and unreadable entries may be skipped; this is not a complete tree.${omissions ? ` ${omissions} additional summaries omitted.` : ''}`;
}

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
      ...(state.authority ? { historical: true } : {})
    };
  }
  function accepted(id, saved) {
    if (!saved) return;
    reset(id, saved);
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
                      ...(window.OriGuide?._collectContext
                        ? {
                            ...window.OriGuide._collectContext(),
                            origin: 'personal_assistant_panel'
                          }
                        : collectWorkspaceContext({
                            pathname: window.location?.pathname,
                            workspaceId: document.body?.dataset?.workspaceId,
                            workspaceSlug: document.body?.dataset?.workspaceSlug
                          })),
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
  return { state, select, remove, reset, request, accepted, adopt, review, notify };
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
let elements;
let chooserGeneration = 0;
let lastEventKey;

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
    elements.summary.textContent = observationSummary(state.observation);
    elements.coverage.textContent = coverageSummary(state.observation);
    elements.projects.textContent = (state.observation.projects || [])
      .map(
        project =>
          `${project.name}${project.marker ? ` (${project.marker})` : ''}: ${project.files} observed files`
      )
      .join(' · ');
  }
  elements.history.hidden = !state.authority;
  elements.history.textContent = state.authority
    ? `Discussing saved observations only (${state.authority}). Pick again for fresh inspection or setup. File contents have not been read.`
    : '';
  elements.status.textContent = state.notice;
  updateSendHint();
  window.PersonalAssistantPanel?.setFolderBusy?.(state.pending, 'context');
  window.PersonalAssistantConversation?.refresh?.();
  window.PersonalAssistantFolder?.contextProgress?.(state);
  window.PersonalAssistantFolderSetup?.contextChanged?.(state);
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
        await controller.select(choice.mode, choice.chip);
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
  controller?.reset(id, saved);
}

// Only the server's typed event channel reaches here. Ordinary model prose
// never becomes a card or an executable setup control.
function renderEvent(id, event, beforeRow) {
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
    row.append(
      node(
        'p',
        `${observation.folder} · saved observations`,
        'personal-assistant-folder-context__eyebrow'
      )
    );
    row.append(node('p', observationSummary(observation)));
    row.append(
      node(
        'p',
        'File contents have not been read. These are dated observations, not permission to inspect again.'
      )
    );
    const detail = node('details', '');
    detail.append(node('summary', 'Observed projects and coverage'));
    detail.append(
      node(
        'p',
        (observation.projects || [])
          .map(
            project =>
              `${project.name}${project.marker ? ` (${project.marker})` : ''}: ${project.files} observed files`
          )
          .join(' · ')
      )
    );
    detail.append(node('p', coverageSummary(observation)));
    row.append(detail);
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
    projects: document.getElementById('personalAssistantFolderProjects'),
    coverage: document.getElementById('personalAssistantFolderCoverage'),
    history: document.getElementById('personalAssistantFolderHistorical'),
    status: document.getElementById('personalAssistantContextStatus'),
    hint: document.getElementById('personalAssistantFolderSendHint')
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
  document.getElementById('personalAssistantInput')?.addEventListener('input', updateSendHint);
  document.addEventListener('personal-assistant:sent', updateSendHint);
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
  close: () => closeChooser({ restoreFocus: false }),
  reset,
  hydrate: (id, saved) => {
    // Resume has already rendered canonical events; keep their deduplication key.
    closeChooser({ restoreFocus: false });
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
