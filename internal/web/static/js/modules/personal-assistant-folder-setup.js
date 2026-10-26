// Presentation of server-authored canonical review references. Model prose never
// supplies a control, offer ID, candidate, route, or confirmation here.
import { folderReceiptView } from './personal-assistant-folder.js';
import {
  proposalView,
  placementProposalView,
  renderProposal
} from './personal-assistant-proposal.js';
import { setupQuestActionURL } from './setup-quest-links.js';
import { createAdjustSheet } from './personal-assistant-folder-adjust.js';

// proposedStructure is the option of a suggestion that proposes a group for a
// folder of several projects, or null. Only the host's closed fields make one.
export function proposedStructure(options) {
  return (
    (options || []).find(
      option =>
        option?.presentation?.version === 1 &&
        option.presentation.kind === 'structure' &&
        option.presentation.state === 'proposed' &&
        option.structure?.version === 1
    ) || null
  );
}

// suggestionChoices sorts a reply's options into what its proposal shows. A
// proposed group is the proposal, and the single folders are reached through
// Adjust. Otherwise the options that can be set up are shown; when none can,
// the unavailable ones are shown as unavailable rather than dropped.
export function suggestionChoices(options) {
  const all = options || [];
  const proposable = all.filter(option => option?.presentation?.state !== 'unavailable');
  const structure = proposedStructure(all);
  if (structure)
    return {
      structure,
      shown: [structure],
      single: proposable.filter(option => option !== structure)
    };
  return { structure: null, shown: proposable.length ? proposable : all, single: proposable };
}

// placementChoiceView is a placement answer as the drawer shows it: the
// server's sentence, the operations it offers, and the guided setups it
// attached. A setup action names a quest by key only; its link is built here,
// and an action without a well-formed key is dropped, never followed.
export function placementChoiceView(choice) {
  const options = (choice?.options || []).filter(option =>
    ['create_project_workspace', 'link_supporting_folder'].includes(option?.operation)
  );
  const setups = [];
  for (const action of choice?.actions || []) {
    const href = setupQuestActionURL(action);
    if (!href) continue;
    setups.push({ label: String(action.label || 'Open Guided Setup'), href });
  }
  return {
    message: String(choice?.message || 'Choose the operation to review.'),
    options,
    setups,
    ...(choice?.readiness ? { readiness: choice.readiness } : {})
  };
}

export function setupCandidates(observation) {
  const projects = observation?.projects || [];
  const labels = projects.map(
    project =>
      `${project.name}${project.root ? ' (whole folder)' : ''}${project.marker ? ` · ${project.marker}` : ''}`
  );
  return projects.map((project, index) => {
    const text = Array.from(labels[index]);
    const long = text.length > 180;
    const duplicate = labels.filter(label => label === labels[index]).length > 1;
    return {
      id: project.id,
      label: `${long ? text.slice(0, 179).join('') + '…' : labels[index]}${long || duplicate ? ` · Choice ${index + 1}` : ''}`
    };
  });
}

export function currentReview(state, offer) {
  return Boolean(
    offer?.id && state?.offerId === offer.id && state.conversationId === offer.conversation_id
  );
}

let elements;
let current;
let reviews = {};
let owner = '';
let candidateObservation = '';
let choiceOptions = [];
let choiceOrigin = null;
let choicesMarker = null;
let reviewLinkMarker = null;
let noticeMarker = null;
let composerOrigin = null;
let uiEpoch = 0;
let suggestion = null;
let choiceTrigger = null;
let choiceSubject = '';
let placementNode = null;
let reviewContext = null;
let reviewElsewhere = null;

// A suggestion may name the workspace the user asked about. It is only a
// reference: Review resolves and checks it again on the server.
export function subjectPlacement(subjectId, option = {}) {
  return subjectId ? { ...option, subject_workspace_id: String(subjectId) } : option;
}

export function choiceIsCurrent(binding, state) {
  return Boolean(
    binding &&
    state &&
    !state.authority &&
    binding.conversationId === state.conversationId &&
    binding.observationId === state.observation?.id &&
    binding.revision === state.revision
  );
}

function focusLocal(control, atStart = false) {
  if (!control?.isConnected || document.getElementById('personalAssistantPanel')?.hidden) return;
  window.PersonalAssistantTranscript?.revealControl?.(control, atStart);
  control.focus({ preventScroll: true });
}
function fallbackOrigin() {
  if (composerOrigin?.isConnected) return composerOrigin;
  composerOrigin = document.createElement('section');
  composerOrigin.dataset.setupComposerOrigin = 'true';
  composerOrigin.className = 'personal-assistant-message__setup';
  composerOrigin.setAttribute('aria-label', 'Setup from attached folder');
  const note = document.createElement('p');
  note.textContent = 'Review setup · no model is needed. Nothing is created until you confirm.';
  composerOrigin.append(note);
  // Idle shared routing targets remain hidden. This is a local UI handoff at
  // the composer's end of the same pane, not a fabricated transcript message.
  document.getElementById('personalAssistantScroll')?.append(composerOrigin);
  return composerOrigin;
}
function originParent() {
  return choiceOrigin?.parent?.isConnected ? choiceOrigin.parent : fallbackOrigin();
}
function parkChoices() {
  if (!elements) return;
  elements.choices.hidden = true;
  if (choicesMarker?.parentNode)
    choicesMarker.parentNode.insertBefore(elements.choices, choicesMarker);
}

const STOPPED_BECAUSE = {
  member_failed: 'one workspace could not be created',
  needs_pick: 'it needs the folder again',
  needs_choice: 'it needs you to choose the project file',
  needs_model: 'it needs a model',
  plan_changed: 'the reviewed plan changed',
  consent_stale: 'its consent is out of date',
  assistant_missing: 'its assistant is missing',
  interrupted: 'it was interrupted'
};

// One sentence for the review state Ori gave the model on this turn, so the
// drawer and the reply describe the same thing. It is a description only: the
// reviewed card stays the place where setup is confirmed or continued.
export function reviewStatusView(context, elsewhere = null) {
  if (!context || context.version !== 1 || !context.status || context.status === 'no_proposal')
    return { visible: false, status: '', text: '', openConversation: '' };
  const subject = String(context.subject || '').trim() || 'this folder';
  const name = String(context.destination_name || '').trim();
  const known = ['existing', 'new'].includes(context.destination_status) && name;
  // A folder set up as a group is not placed "in" anything: the group is what
  // is made of it, and often carries the folder's own name.
  const place = known
    ? context.operation === 'link_supporting_folder'
      ? ` as a supporting folder in ${name}`
      : context.operation === 'create_group_with_workspaces'
        ? ` as the group ${name}`
        : ` in ${context.destination_status === 'new' ? 'a new ' : ''}${name}`
    : '';
  const past = context.historical ? ' This is an earlier review; it cannot be confirmed here.' : '';
  let text;
  switch (context.status) {
    case 'awaiting_confirmation':
      text = `Setup review for ${subject}${place} is waiting for your confirmation. Nothing is set up until you confirm on its card.`;
      break;
    case 'awaiting_outcome':
      text = `Setup for ${subject}${place} was confirmed and has not finished. Continue it on its card.`;
      break;
    case 'setup_running':
      text = `Setting up ${subject}${place}…`;
      break;
    case 'setup_stopped':
      text = `Setup for ${subject}${place} stopped because ${STOPPED_BECAUSE[context.blocker] || 'a step did not finish'}. Use its card to continue.`;
      break;
    case 'reselection_needed':
      text = `The setup review for ${subject} needs the folder again. Pick the folder again for a current review.`;
      break;
    case 'setup_unavailable':
      text =
        context.blocker === 'home_prerequisites'
          ? `Library setup for ${subject} needs its Home provider. Review the package requirements on its card, then choose Check again.`
          : context.blocker === 'destination_changed'
            ? `The reviewed destination for ${subject} has changed. Use Review setup to refresh the review.`
            : context.blocker === 'destination_unavailable'
              ? `The reviewed destination for ${subject} could not be read. Use Review setup to refresh the review.`
              : context.blocker === 'project_exists'
                ? `${subject} already has a workspace. Open it from its card instead of setting up another.`
                : `Setup is not available for ${subject} here. You can keep discussing the folder.`;
      break;
    case 'completed':
      text = `${subject} is set up${place}.`;
      break;
    case 'closed':
      text = `The setup review for ${subject} is closed. It created nothing.`;
      break;
    case 'declined':
      text = `Setup for ${subject} was declined. Nothing was created.`;
      break;
    case 'postponed':
      text = `The setup review for ${subject} is postponed. Nothing was created.`;
      break;
    case 'pending_elsewhere':
      text = `A setup review for ${subject}${place} is waiting in another conversation. Nothing new has been prepared here.`;
      break;
    default:
      text = 'The setup review could not be read right now. That does not mean there is no review.';
  }
  const terminal = ['completed', 'closed', 'declined', 'postponed', 'pending_elsewhere'];
  return {
    visible: true,
    status: String(context.status),
    text: text + (terminal.includes(context.status) ? '' : past),
    openConversation:
      context.status === 'pending_elsewhere' ? String(elsewhere?.conversation_id || '') : ''
  };
}

// The parts of a canonical review that change what its status sentence says.
export function reviewStateKey(offer) {
  return [
    offer?.status,
    offer?.setup?.status,
    offer?.setup?.stop_reason,
    offer?.destination_status,
    offer?.needs_pick === true
  ].join('|');
}

// The folder is already a project: name it and link its page. Only an in-app
// workspace page is linked; nothing here can run or repeat setup.
export function existingProjectView(existing) {
  const subject = String(existing?.subject || 'This folder');
  const workspace = String(existing?.workspace || 'an existing project');
  const route = String(existing?.route || '');
  const tidied = existing?.tidied === true;
  return {
    message: tidied
      ? `File Curator already tidies ${subject}. Nothing new has been prepared.`
      : `${subject} is already set up as the project “${workspace}”. Nothing new has been prepared.`,
    route: /^\/workspaces\/[A-Za-z0-9][A-Za-z0-9._:-]*$/.test(route) ? route : '',
    openLabel: tidied ? 'Open it' : `Open ${workspace}`
  };
}

export function currentSuggestion(state, value, offer = null) {
  if (value?.offer_id) {
    return Boolean(
      value.message_id &&
      value.revision &&
      value.conversation_id === state?.conversationId &&
      value.revision === state.revision &&
      value.observation_id === state.observation?.id &&
      !state.preview &&
      !state.authority &&
      state.offerId === value.offer_id &&
      currentReview(state, offer) &&
      ['pending', 'awaiting_outcome'].includes(offer.status)
    );
  }
  return Boolean(
    value?.message_id &&
    value?.revision &&
    value.conversation_id === state?.conversationId &&
    value.revision === state.revision &&
    value.observation_id === state.observation?.id &&
    !state.preview &&
    !state.authority &&
    !state.offerId &&
    value.options?.length &&
    value.options.every(option =>
      state.observation.projects?.some(project => project.id === option.candidate_id)
    )
  );
}

// The line under a reply's setup proposal. A proposal about one folder has no
// scope left to choose, so it does not ask for one.
export function suggestionNote(value) {
  if (value?.offer_id)
    return 'Focuses the existing canonical review only. Setup still requires its reviewed confirmation.';
  if (value?.subject?.name)
    return `Workspace you named: ${String(value.subject.name)}. Placement and operation are rechecked in Review; setup still requires confirmation.`;
  // A proposed group is one proposal, however many folders it holds.
  return (value?.options || []).length > 1 && !proposedStructure(value.options)
    ? 'Choose scope and review effects before confirming. Nothing is set up here.'
    : 'Review its effects before confirming. Nothing is set up here.';
}

function flowHasFocus() {
  return Boolean(choiceOrigin?.parent?.contains(document.activeElement));
}

function retireSuggestion(node) {
  if (!node) return;
  window.PersonalAssistantTranscript?.beforeChange?.();
  if (node.contains(document.activeElement)) {
    // Retire authority, never the reader's focused native control. Its old
    // handlers still validate the original binding; blur retires this shell.
    node.removeAttribute('data-folder-setup-suggestion');
    if (!node.dataset.inactiveSetup) {
      uiEpoch++;
      node.dataset.inactiveSetup = 'true';
      const note = document.createElement('p');
      note.textContent =
        'These setup choices are no longer current. Use the current proposal or pick the folder again; no setup is authorized here.';
      node.append(note);
      node.addEventListener('focusout', () =>
        queueMicrotask(() => {
          if (node.contains(document.activeElement)) return;
          if (choiceOrigin?.parent === node) closeChoices(false);
          node.remove();
        })
      );
    }
    window.PersonalAssistantTranscript?.changed?.();
    return;
  }
  if (node === choiceOrigin?.parent) closeChoices(false);
  node.remove();
  window.PersonalAssistantTranscript?.changed?.();
}

function renderSuggestion() {
  const existing = document.querySelector('[data-folder-setup-suggestion]');
  if (!currentSuggestion(current, suggestion, reviews[suggestion?.offer_id])) {
    retireSuggestion(existing);
    return;
  }
  const row = Array.from(
    document.querySelectorAll('#homeAssistantConversation [data-message-id]')
  ).find(
    row =>
      row.dataset.messageId === suggestion.message_id &&
      row.dataset.conversationId === suggestion.conversation_id &&
      row.dataset.messageRole === 'assistant'
  );
  if (!row) {
    retireSuggestion(existing);
    return;
  }
  const bubble = row.firstElementChild;
  if (!bubble) return;
  if (existing?.parentElement === bubble) {
    const action = existing.querySelector('[data-setup-action]');
    if (action) action.disabled = Boolean(current.pending);
    return;
  }
  retireSuggestion(existing);
  const handoff = document.createElement('div');
  handoff.dataset.folderSetupSuggestion = suggestion.message_id;
  handoff.className = 'personal-assistant-message__setup';
  const { structure, shown: options } = suggestionChoices(suggestion.options);
  const project = current.observation?.projects?.find(
    project => project.id === options[0]?.candidate_id
  );
  let view = proposalView(
    options.length === 1 ? options[0] : {},
    options.length === 1 ? project : { name: current.observation?.folder, root: true }
  );
  if (structure && !suggestion.offer_id) {
    // A proposed group stays under the reply as a card. Its controls are the
    // chips above it (Set up this structure, Adjust…, Just discuss), so the card
    // carries no button of its own.
    handoff.append(renderProposal(view, document, window.OriWorkspaceBuildingArt));
    const note = document.createElement('p');
    note.className = 'personal-assistant-folder-context__note';
    note.textContent = suggestionNote(suggestion);
    handoff.append(note);
    bubble.append(handoff);
    window.PersonalAssistantFolderContext?.refreshDiscussion?.();
    return;
  }
  if (suggestion.offer_id)
    view = {
      ...view,
      state: 'existing',
      label: 'Existing setup review',
      scope: 'Canonical review',
      subject: proposalView(
        {},
        { name: reviews[suggestion.offer_id]?.subject?.name || 'Reviewed folder' }
      ).subject,
      description: 'Show the stored review; do not prepare or repeat setup.',
      destination: ''
    };
  else if (options.length > 1)
    view = {
      ...view,
      label: 'Choose setup scope',
      scope: 'No folder selected',
      description: `Choose one of ${options.length} compatible folders. Discussion checks do not choose setup scope.`
    };
  handoff.append(renderProposal(view, document, window.OriWorkspaceBuildingArt));
  const button = document.createElement('button');
  button.type = 'button';
  button.dataset.setupAction = 'true';
  button.className = suggestion.offer_id
    ? 'personal-assistant-message__action'
    : 'personal-assistant-message__action personal-assistant-message__setup-secondary';
  button.textContent = suggestion.offer_id
    ? 'Show existing setup review'
    : options.length > 1
      ? 'Choose setup scope'
      : view.action;
  button.disabled = Boolean(current.pending);
  button.setAttribute(
    'aria-controls',
    suggestion.offer_id ? 'personalAssistantFolderOffer' : 'personalAssistantFolderSetupChoices'
  );
  if (!suggestion.offer_id) button.setAttribute('aria-expanded', 'false');
  const value = suggestion;
  button.addEventListener('click', () => {
    if (!currentSuggestion(current, value, reviews[value.offer_id])) return;
    if (value.offer_id) {
      const card = document.getElementById('personalAssistantFolderOffer');
      if (card) {
        card.tabIndex = -1;
        focusLocal(card, true);
      }
      return;
    }
    openChoices(options, button, value.subject?.workspace_id || '');
  });
  const note = document.createElement('p');
  note.className = 'personal-assistant-folder-context__note';
  note.textContent = suggestionNote(suggestion);
  handoff.append(button, note);
  bubble.append(handoff);
}

function renderChoicePreview() {
  elements?.choices.querySelector('[data-setup-choice-preview]')?.remove();
  if (!elements || !elements.candidate.value || !choiceIsCurrent(choiceOrigin?.binding, current))
    return;
  const project = current.observation.projects.find(
    project => project.id === elements.candidate.value
  );
  if (!project) return;
  const option = choiceOptions.find(option => option.candidate_id === project.id) || {};
  const preview = renderProposal(
    proposalView(option, project),
    document,
    window.OriWorkspaceBuildingArt
  );
  preview.dataset.setupChoicePreview = 'true';
  elements.candidate.after(preview);
  // Explicit choice is local navigation, not an incoming reply or new consent.
  window.PersonalAssistantTranscript?.revealControl?.(elements.candidate, true);
}

function applySuggestion(value) {
  retireSuggestion(document.querySelector('[data-folder-setup-suggestion]'));
  suggestion = currentSuggestion(current, value, reviews[value?.offer_id]) ? value : null;
  renderSuggestion();
  syncReviewLink();
}

// reviewLinkHidden says whether the composer's Review setup link stays away:
// for a folder with nothing to set up, and for one Ori proposed a group for,
// which is set up from its card instead.
export function reviewLinkHidden(observation, structure) {
  return setupCandidates(observation).length === 0 || Boolean(structure);
}

function syncReviewLink() {
  if (!elements || !current) return;
  const structure = currentStructure();
  elements.open.hidden =
    Boolean(current.hireUnavailable) || reviewLinkHidden(current.observation, structure);
  // Move the one existing control beside a local preview; never clone its
  // authority or create a second review owner. Saved context keeps the chip.
  const previewActions = document.getElementById('personalAssistantFolderPreviewActions');
  const exploring = document
    .getElementById('personalAssistantPanel')
    ?.classList?.contains('personal-assistant-panel--exploring');
  const parent =
    current.preview && !structure && !exploring && previewActions
      ? previewActions
      : reviewLinkMarker?.parentNode;
  if (parent && elements.open.parentNode !== parent) {
    if (parent === previewActions) parent.append(elements.open);
    else parent.insertBefore(elements.open, reviewLinkMarker);
  }
}

function renderReviewStatus() {
  const node = document.getElementById('personalAssistantFolderReviewStatus');
  if (!node) return;
  const view = reviewStatusView(reviewContext, reviewElsewhere);
  node.replaceChildren();
  node.hidden = !view.visible;
  node.dataset.reviewStatus = view.status;
  if (!view.visible) return;
  node.append(view.text);
  if (!view.openConversation) return;
  const open = document.createElement('button');
  open.type = 'button';
  open.className = 'btn btn-sm btn-link';
  open.textContent = 'Open existing conversation';
  const id = view.openConversation;
  open.addEventListener('click', () => void window.PersonalAssistantConversation?.resume?.(id));
  node.append(' ', open);
}

// Server-authored on each reply and reload. A status from an earlier turn is
// dropped as soon as the card reports something newer, so the two never differ.
function applyReviewContext(context, elsewhere = null) {
  reviewContext = context || null;
  reviewElsewhere = elsewhere || null;
  renderReviewStatus();
}

function closeChoices(returnFocus = true, forget = true) {
  if (!elements) return;
  window.PersonalAssistantTranscript?.beforeChange?.();
  parkChoices();
  if (noticeMarker?.parentNode) noticeMarker.parentNode.insertBefore(elements.notice, noticeMarker);
  elements.open.setAttribute('aria-expanded', 'false');
  choiceTrigger?.setAttribute('aria-expanded', 'false');
  if (returnFocus)
    focusLocal(
      choiceTrigger?.isConnected ? choiceTrigger : document.getElementById('personalAssistantInput')
    );
  if (forget) {
    uiEpoch++;
    placementNode?.remove();
    composerOrigin?.remove();
    composerOrigin = null;
    choiceOrigin = null;
    choiceTrigger = null;
    choiceSubject = '';
  }
  window.PersonalAssistantTranscript?.changed?.();
}

// Continue beside the originating proposal, not a top-panel results surface.
function showPlacement(focus, shouldFocus) {
  window.PersonalAssistantFolderContext?.notify?.('');
  window.PersonalAssistantTranscript?.beforeChange?.();
  const heading = document.createElement('h3');
  heading.className = 'personal-assistant-setup-heading';
  heading.textContent = 'Setting up';
  placementNode.prepend(heading);
  placementNode.addEventListener('keydown', event => {
    if (event.key !== 'Escape') return;
    event.preventDefault();
    event.stopPropagation();
    closeChoices();
  });
  originParent().append(placementNode);
  window.PersonalAssistantTranscript?.changed?.();
  if (shouldFocus) focusLocal(focus, true);
}

async function review(candidateId, close = false, offerId, placement) {
  if (!close && choiceOrigin && !choiceIsCurrent(choiceOrigin.binding, current)) return;
  const ticket = ++uiEpoch;
  const actionHadFocus =
    choiceOrigin?.parent?.contains(document.activeElement) ||
    document.getElementById('personalAssistantFolderOffer')?.contains(document.activeElement);
  closeChoices(false, false);
  placementNode?.remove();
  const statusHome = choiceOrigin
    ? originParent()
    : document.getElementById('personalAssistantFolderOffer')?.parentElement;
  if (statusHome && elements.notice) statusHome.append(elements.notice);
  const pendingRequest = window.PersonalAssistantFolderContext?.review?.(
    candidateId,
    close,
    offerId,
    placement
  );
  if (actionHadFocus && elements.notice) {
    elements.notice.tabIndex = -1;
    focusLocal(elements.notice, true);
  }
  const requestFocus = document.activeElement;
  const result = await pendingRequest;
  if (ticket !== uiEpoch) return;
  const shouldFocus = document.activeElement === requestFocus;
  if (result?.folder_pending_elsewhere) {
    const pending = result.folder_pending_elsewhere;
    placementNode = document.createElement('section');
    placementNode.className = 'personal-assistant-message__setup';
    placementNode.setAttribute('aria-label', 'Existing setup review');
    const message = document.createElement('p');
    message.textContent = `${String(pending.subject || 'This folder')} has a review in another conversation. Nothing new has been prepared.`;
    const open = document.createElement('button');
    open.type = 'button';
    open.className = 'btn btn-sm btn-outline-secondary';
    open.textContent = 'Open existing conversation';
    open.addEventListener('click', () => {
      placementNode?.remove();
      void window.PersonalAssistantConversation?.visitFolderReview?.(pending.conversation_id);
    });
    placementNode.append(message, open);
    showPlacement(open, shouldFocus);
    return;
  }
  if (result?.folder_existing_project) {
    const existing = existingProjectView(result.folder_existing_project);
    placementNode = document.createElement('section');
    placementNode.className = 'personal-assistant-message__setup';
    placementNode.setAttribute('aria-label', 'Existing project');
    const message = document.createElement('p');
    message.textContent = existing.message;
    placementNode.append(
      renderProposal(
        {
          ...proposalView({}, { name: result.folder_existing_project.subject }),
          kind: 'workspace',
          art: 'neutral',
          state: 'existing',
          label:
            result.folder_existing_project.tidied === true
              ? 'File Janitor workspace'
              : 'Existing workspace',
          scope:
            result.folder_existing_project.tidied === true ? 'Already tidies it' : 'Already set up',
          description: 'Nothing new has been prepared.',
          destination: ''
        },
        document,
        window.OriWorkspaceBuildingArt
      ),
      message
    );
    if (existing.route) {
      const open = document.createElement('a');
      open.className = 'btn btn-sm btn-outline-secondary';
      open.href = existing.route;
      open.textContent = existing.openLabel;
      placementNode.append(open);
    }
    const cancel = document.createElement('button');
    cancel.type = 'button';
    cancel.className = 'btn btn-sm btn-link';
    cancel.textContent = 'Keep chatting';
    cancel.addEventListener('click', () => {
      closeChoices();
    });
    placementNode.append(cancel);
    showPlacement(placementNode.querySelector('a') || cancel, shouldFocus);
    return;
  }
  if (result?.folder_placement_choice) {
    const choice = result.folder_placement_choice;
    const view = placementChoiceView(choice);
    placementNode = document.createElement('section');
    placementNode.className = 'personal-assistant-message__setup';
    placementNode.setAttribute('aria-label', 'Choose folder placement');
    const requirements = document.createElement('div');
    globalThis.OriFolderSetupVisual.renderRequirements(requirements, { readiness: view.readiness });
    placementNode.append(requirements);
    const message = document.createElement('p');
    message.textContent = view.message;
    placementNode.append(message);
    for (const option of view.options) {
      const button = document.createElement('button');
      button.type = 'button';
      button.className = 'btn btn-sm btn-outline-secondary';
      button.textContent = String(option.label || 'Review');
      button.addEventListener(
        'click',
        () =>
          void review(
            choice.candidate_id,
            false,
            undefined,
            subjectPlacement(placement?.subject_workspace_id, option)
          )
      );
      const preview = renderProposal(
        placementProposalView(option, choice.subject),
        document,
        window.OriWorkspaceBuildingArt
      );
      preview.append(button);
      placementNode.append(preview);
    }
    const actions = document.createElement('div');
    actions.className = 'folder-setup-next';
    // Keep the local folder/draft owner here while its host-guided setup opens.
    for (const setup of view.setups) {
      const open = document.createElement('a');
      open.className = 'btn btn-sm btn-primary';
      open.href = setup.href;
      open.target = '_blank';
      open.rel = 'noopener';
      open.textContent = `${setup.label} (new tab)`;
      actions.append(open);
    }
    const cancel = document.createElement('button');
    cancel.type = 'button';
    cancel.className = 'btn btn-sm btn-link';
    cancel.textContent = 'Keep chatting';
    cancel.addEventListener('click', () => {
      closeChoices();
    });
    actions.append(cancel);
    placementNode.append(actions);
    const start = requirements.querySelector('h4');
    if (start) start.tabIndex = -1;
    showPlacement(start || placementNode.querySelector('button, a'), shouldFocus);
    return;
  }
  if (result?.review_closed) {
    window.PersonalAssistantFolderContext.notify('');
    const id = window.PersonalAssistantConversation.currentId();
    await window.PersonalAssistantFolder.reload();
    if (id === window.PersonalAssistantConversation.currentId())
      window.PersonalAssistantConversation.notify(
        'The old setup review is closed. This conversation is no longer available here; choose New to keep chatting. Your draft is unchanged.'
      );
    return;
  }
  if (!result?.conversation?.stored) {
    // Choices are already closed. Keep the error beside the originating flow
    // instead of parking it above a long planning panel. Reveal it only if the
    // user has not moved focus (for example, to type a newer draft).
    if (shouldFocus && elements.notice?.textContent) {
      elements.notice.tabIndex = -1;
      focusLocal(elements.notice, true);
    }
    return;
  }
  const refreshed = result.folder_reviews?.[current?.offerId];
  if (placement?.recheck && currentReview(current, refreshed)) {
    // The host refreshed the exact canonical review without appending a turn.
    // Do not reload the conversation or move the user's draft/scroll owner.
    reviews = { ...reviews, [refreshed.id]: refreshed };
    window.PersonalAssistantFolder?.updateConversationReview?.(refreshed);
    applyReviewContext(null);
    window.PersonalAssistantFolderContext?.notify?.(
      'Review refreshed. This check did not install plugins or start setup.'
    );
    if (shouldFocus) {
      const model = document.querySelector(
        '#personalAssistantFolderRequirements .folder-requirements__model'
      );
      if (model) {
        model.tabIndex = -1;
        focusLocal(model, true);
      }
    }
    return;
  }
  const focusBeforeHydration = document.activeElement;
  window.PersonalAssistantConversation?.applyReply?.(result);
  // Reload only canonical data. This never scans, calls a model, or confirms.
  if (await window.PersonalAssistantConversation?.resume?.(result.conversation.id)) {
    if (window.PersonalAssistantConversation.currentId() !== result.conversation.id) return;
    const card = document.getElementById('personalAssistantFolderOffer');
    if (
      card?.closest('[data-folder-review-id]')?.dataset.folderReviewId ===
      result.folder_context?.offer_id
    ) {
      if (
        shouldFocus &&
        (document.activeElement === focusBeforeHydration ||
          (!focusBeforeHydration.isConnected && document.activeElement === document.body))
      ) {
        card.tabIndex = -1;
        focusLocal(card, true);
      }
    }
  }
}

function openChoices(options, trigger = elements.open, subjectId = '') {
  const candidates = setupCandidates(current?.observation).filter(
    candidate =>
      !Array.isArray(options) || options.some(option => option.candidate_id === candidate.id)
  );
  if (!candidates.length || current?.pending || current?.authority) return;
  closeChoices(false);
  choiceTrigger = trigger;
  choiceSubject = String(subjectId || '');
  choiceOrigin = {
    parent: trigger.closest('[data-folder-setup-suggestion]') || fallbackOrigin(),
    trigger,
    binding: {
      conversationId: current.conversationId,
      observationId: current.observation.id,
      revision: current.revision
    }
  };
  if (candidates.length === 1 && setupCandidates(current.observation).length === 1) {
    void review(candidates[0].id, false, undefined, subjectPlacement(choiceSubject));
    return;
  }
  elements.candidate.replaceChildren(new Option('Choose a folder…', ''));
  candidates.forEach(candidate =>
    elements.candidate.add(new Option(candidate.label, candidate.id))
  );
  candidateObservation = current.observation.id;
  choiceOptions = Array.isArray(options) ? options : [];
  renderChoicePreview();
  choiceOrigin.parent.append(elements.choices);
  elements.choices.hidden = false;
  elements.open.setAttribute('aria-expanded', 'true');
  trigger.setAttribute('aria-expanded', 'true');
  if (window.PersonalAssistantTranscript) {
    window.PersonalAssistantTranscript.revealControl(elements.candidate);
    elements.candidate.focus({ preventScroll: true });
  } else elements.candidate.focus();
}

// The group Ori proposes for the attached folder, when it proposes one: from
// the current reply's suggestion once there is a reply, else from the attach
// preview. Null for a single folder, a saved (historical) snapshot, and a
// suggestion that only points at an existing review.
function currentStructure() {
  if (!current?.observation || current.authority) return null;
  if (suggestion && !suggestion.offer_id && currentSuggestion(current, suggestion))
    return proposedStructure(suggestion.options)?.structure || null;
  return current.preview && current.structure?.version === 1 ? current.structure : null;
}

// The root is the candidate a group is reviewed as.
function rootCandidate() {
  return (
    proposedStructure(suggestion?.options)?.candidate_id ||
    current?.observation?.projects?.find(project => project.root)?.id ||
    ''
  );
}

// Where a flow started from a control continues: the proposal under the same
// reply when there is one, else the composer's own end of the pane.
function flowOrigin(trigger) {
  return (
    trigger?.closest?.('[data-folder-setup-suggestion]') ||
    trigger?.closest?.('[data-message-id]')?.querySelector('[data-folder-setup-suggestion]') ||
    fallbackOrigin()
  );
}

let adjusting = null;

function closeAdjust(returnFocus = true) {
  if (!adjusting) return;
  const { sheet, trigger } = adjusting;
  adjusting = null;
  window.PersonalAssistantTranscript?.beforeChange?.();
  sheet.element.remove();
  window.PersonalAssistantTranscript?.changed?.();
  if (returnFocus) focusLocal(trigger?.isConnected ? trigger : elements?.open);
}

// One explicit review of one candidate, started from a control rather than the
// scope chooser. placement may carry what the user changed in Adjust.
function reviewFrom(trigger, candidateId, placement = {}) {
  if (!candidateId || !current?.observation || current.pending || current.authority) return;
  closeChoices(false);
  closeAdjust(false);
  choiceTrigger = trigger;
  choiceOrigin = {
    parent: flowOrigin(trigger),
    trigger,
    binding: {
      conversationId: current.conversationId,
      observationId: current.observation.id,
      revision: current.revision
    }
  };
  void review(candidateId, false, undefined, placement);
}

// reviewCandidate opens the review of the one project an attached folder is,
// as Review setup would: the attach named it (hire_candidate), so the hire card
// answers the attach. false when there is nothing current to review.
export function reviewCandidate(candidateId) {
  if (!elements || !candidateId || !current?.observation || current.pending || current.authority)
    return false;
  if (!setupCandidates(current.observation).some(candidate => candidate.id === candidateId))
    return false;
  reviewFrom(elements.open, candidateId);
  return true;
}

// Recheck follows the exact canonical candidate and its saved placement. The
// context controller owns revision/generation checks and rejects stale replies.
export function recheckReview(offer, trigger) {
  if (
    !elements ||
    !currentReview(current, offer) ||
    offer.status !== 'pending' ||
    offer.setup ||
    !current.observation ||
    !offer.candidate_id ||
    current.pending ||
    current.authority
  )
    return false;
  reviewFrom(trigger, offer.candidate_id, { recheck: true });
  return true;
}

function reviewStructure(trigger) {
  const structure = currentStructure();
  if (structure)
    reviewFrom(trigger, rootCandidate(), {
      structure: {
        group_type:
          structure.recommendation?.operation === 'library' ? 'library' : structure.group.type
      }
    });
}

// Adjust is one sheet: the group's name and type, the member checklist with
// editable names, and the way to set up a single folder instead. Done opens the
// review with those choices; nothing is created by the sheet.
function adjustStructure(trigger, structure) {
  const record = structure?.version === 1 ? structure : currentStructure();
  const candidateId = rootCandidate();
  if (!record || !candidateId || current.pending) return;
  closeChoices(false);
  closeAdjust(false);
  const binding = {
    conversationId: current.conversationId,
    observationId: current.observation.id,
    revision: current.revision
  };
  // The folders that can be set up on their own: the reply's own options, or
  // before any reply every observed folder but the root.
  const single = suggestion?.options
    ? suggestionChoices(suggestion.options).single
    : (current.observation.projects || [])
        .filter(project => !project.root)
        .map(project => ({ candidate_id: project.id }));
  const sheet = createAdjustSheet({
    structure: record,
    onDone: adjustment => {
      if (!choiceIsCurrent(binding, current)) return closeAdjust(false);
      closeAdjust(false);
      reviewFrom(trigger, candidateId, { structure: adjustment });
    },
    onCancel: () => closeAdjust(),
    onSingle: single.length
      ? () => {
          closeAdjust(false);
          openChoices(single, trigger);
        }
      : undefined
  });
  // The sheet opens beside what it adjusts: the attach preview, the review
  // card, or the proposal under a reply.
  const home =
    trigger?.closest?.('#personalAssistantFolderStructure, #personalAssistantFolderOffer') ||
    flowOrigin(trigger);
  window.PersonalAssistantTranscript?.beforeChange?.();
  home.append(sheet.element);
  window.PersonalAssistantTranscript?.changed?.();
  adjusting = { sheet, trigger, binding };
  sheet.focus();
}

function park(node) {
  if (
    !node ||
    node.contains(elements?.choices) ||
    node.contains(placementNode) ||
    node.contains(choiceOrigin?.trigger)
  )
    closeChoices(false);
  const card = document.getElementById('personalAssistantFolderOffer');
  if (!node || (card && node.contains(card))) window.PersonalAssistantFolder?.parkConversation?.();
}

function renderReferences() {
  if (!current) return;
  const slots = Array.from(
    document.querySelectorAll('#homeAssistantConversation [data-folder-review-id]')
  );
  const active = slots.findLast(slot => current.offerId === slot.dataset.folderReviewId);
  for (const slot of slots) {
    const offer = reviews[slot.dataset.folderReviewId];
    if (slot === active && currentReview(current, offer)) {
      window.PersonalAssistantFolder?.showConversationReview?.(slot, offer);
      continue;
    }
    // Park before replacing children: the one canonical renderer is moved, not
    // cloned, and must survive New, hydration, and bounded-history trimming.
    park(slot);
    slot.replaceChildren();
    const note = document.createElement('p');
    note.className = 'personal-assistant-folder-context__note';
    if (!offer) {
      note.textContent =
        'Saved setup review. Reopen this conversation if the record is unavailable.';
      slot.append(note);
      continue;
    }
    const receipt = folderReceiptView(offer);
    note.textContent = receipt.visible
      ? 'Confirmed workspace setup'
      : 'This review is closed or no longer current. No new setup is authorized by this history.';
    slot.append(note);
    if (offer.status === 'pending') {
      const close = document.createElement('button');
      close.type = 'button';
      close.className = 'btn btn-sm btn-link';
      close.textContent = 'Close outdated review';
      close.addEventListener('click', () => void review('', true, offer.id));
      slot.append(close);
    }
    if (receipt.visible) {
      const kind =
        offer.outcome?.kind === 'home' || offer.outcome?.kind === 'group'
          ? 'group'
          : offer.outcome?.kind === 'project'
            ? 'workspace'
            : 'unknown';
      slot.prepend(
        renderProposal(
          {
            ...proposalView({}, { name: offer.subject?.name }),
            kind,
            art: kind === 'group' ? 'district' : 'neutral',
            state: 'completed',
            label: 'Completed setup',
            scope: 'Verified receipt',
            description: 'The receipt below records what actually finished.',
            destination: ''
          },
          document,
          window.OriWorkspaceBuildingArt
        )
      );
      const rows = document.createElement('ul');
      (receipt.rows || []).forEach(row => {
        const item = document.createElement('li');
        item.textContent = `${row.name}${row.detail ? ` · ${row.detail}` : ''}`;
        rows.append(item);
      });
      slot.append(rows);
      if (receipt.gone) {
        const gone = document.createElement('p');
        gone.className = 'personal-assistant-folder-context__note';
        gone.textContent = receipt.gone.message;
        slot.append(gone);
      }
      if (receipt.route) {
        const link = document.createElement('a');
        link.href = receipt.route;
        link.textContent = receipt.openLabel;
        slot.append(link);
      }
    }
  }
}

function contextChanged(state) {
  if (!elements || !state) return;
  if (owner !== state.conversationId) {
    closeChoices(false);
    placementNode?.remove();
    park();
    reviews = {};
    // A saved first turn keeps its own status; another conversation does not.
    if (owner) applyReviewContext(null);
    owner = state.conversationId;
  }
  if (current?.offerId !== state.offerId || !state.offerId)
    window.PersonalAssistantFolder?.parkConversation?.();
  current = { ...state };
  if (adjusting && !choiceIsCurrent(adjusting.binding, current)) closeAdjust(false);
  if (choiceOrigin && !choiceIsCurrent(choiceOrigin.binding, current)) {
    if (flowHasFocus()) retireSuggestion(choiceOrigin.parent);
    else closeChoices(false);
  }
  const available = setupCandidates(state.observation).length > 0;
  syncReviewLink();
  elements.open.disabled = state.pending || Boolean(state.authority);
  elements.open.title = state.authority ? 'Pick the folder again for a current setup review.' : '';
  if ((!available || candidateObservation !== state.observation?.id) && !flowHasFocus()) {
    parkChoices();
    elements.open.setAttribute('aria-expanded', 'false');
  }
  if (!currentSuggestion(current, suggestion, reviews[suggestion?.offer_id])) suggestion = null;
  renderSuggestion();
  renderReferences();
}

function hydrate(
  id,
  savedReviews = {},
  savedSuggestion = null,
  savedContext = null,
  elsewhere = null
) {
  if (id !== window.PersonalAssistantConversation?.currentId?.()) return;
  owner = id;
  reviews = savedReviews;
  const displayed = window.PersonalAssistantFolder?.current?.();
  if (reviews[displayed?.id])
    window.PersonalAssistantFolder.updateConversationReview(reviews[displayed.id]);
  contextChanged(window.PersonalAssistantFolderContext.current());
  applySuggestion(savedSuggestion);
  applyReviewContext(savedContext, elsewhere);
}

function init() {
  const open = document.getElementById('personalAssistantReviewFolderSetup');
  if (!open || elements) return;
  elements = {
    open,
    choices: document.getElementById('personalAssistantFolderSetupChoices'),
    candidate: document.getElementById('personalAssistantFolderSetupCandidate'),
    notice: document.getElementById('personalAssistantContextStatus')
  };
  if (elements.notice) {
    noticeMarker = document.createComment('folder notice parking slot');
    elements.notice.after(noticeMarker);
  }
  reviewLinkMarker = document.createComment('single review link parking slot');
  elements.open.after(reviewLinkMarker);
  choicesMarker = document.createComment('single setup chooser parking slot');
  elements.choices.after(choicesMarker);
  // A folder Ori proposed a group for is reviewed as that group; choosing one
  // of its folders instead is in Adjust. A folder the attach named one project
  // for (hire_candidate) is reviewed as that project, with no chooser: it
  // brings its hire card back after Keep chatting.
  open.addEventListener('click', () => {
    if (currentStructure()) reviewStructure(open);
    else if (!reviewCandidate(current?.hireCandidate)) openChoices();
  });
  elements.candidate.addEventListener('change', renderChoicePreview);
  elements.choices.addEventListener('focusout', () =>
    queueMicrotask(() => {
      if (
        !choiceOrigin ||
        choiceIsCurrent(choiceOrigin.binding, current) ||
        elements.choices.contains(document.activeElement)
      )
        return;
      const stale = choiceOrigin.parent;
      closeChoices(false);
      if (stale?.dataset.inactiveSetup) stale.remove();
    })
  );
  document.getElementById('personalAssistantFolderSetupReview').addEventListener('click', () => {
    if (elements.candidate.reportValidity())
      void review(elements.candidate.value, false, undefined, subjectPlacement(choiceSubject));
  });
  document
    .getElementById('personalAssistantFolderSetupCancel')
    .addEventListener('click', closeChoices);
  elements.choices.addEventListener('keydown', event => {
    if (event.key !== 'Escape') return;
    event.preventDefault();
    event.stopPropagation();
    closeChoices();
  });
  document.addEventListener('personal-assistant:folder-offer', event => {
    const offer = event.detail?.offer;
    if (offer?.conversation_id !== owner || !offer?.id) return;
    // The card just reported a newer canonical state than the last turn's summary.
    if (reviews[offer.id] && reviewStateKey(reviews[offer.id]) !== reviewStateKey(offer))
      applyReviewContext(null);
    reviews[offer.id] = offer;
    renderReferences();
  });
  contextChanged(window.PersonalAssistantFolderContext.current());
}

if (typeof window !== 'undefined')
  window.PersonalAssistantFolderSetup = {
    contextChanged,
    applySuggestion,
    applyReviewContext,
    hydrate,
    park,
    structure: currentStructure,
    reviewStructure,
    reviewCandidate,
    recheckReview,
    refreshReviewLink: syncReviewLink,
    adjustStructure,
    // Just discuss: the reply's proposal leaves this conversation's view. It
    // writes nothing; the folder can be proposed again by attaching it.
    parkSuggestion: () => {
      retireSuggestion(document.querySelector('[data-folder-setup-suggestion]'));
      suggestion = null;
      syncReviewLink();
    },
    closeReview: async offer => {
      if (
        offer?.conversation_id &&
        offer.conversation_id !== window.PersonalAssistantConversation.currentId()
      ) {
        if (!(await window.PersonalAssistantConversation.resume(offer.conversation_id))) {
          // Only the server may classify a deleted/moved review as orphaned.
          // An empty revision cannot close an active live review.
          const generation = window.PersonalAssistantFolderContext.current()?.generation;
          const current = () =>
            generation === window.PersonalAssistantFolderContext.current()?.generation;
          try {
            const response = await fetch('/api/home-assistant/folder-context/review/close', {
              method: 'POST',
              headers: { 'Content-Type': 'application/json' },
              body: JSON.stringify({
                conversation_id: offer.conversation_id,
                revision: '',
                offer_id: offer.id
              })
            });
            const data = await response.json();
            if (!response.ok)
              throw new Error(data.message || 'The old setup review could not be closed.');
            if (current())
              window.PersonalAssistantFolderContext.notify(
                'The outdated setup review is closed. No workspace or preference was created.'
              );
            await window.PersonalAssistantFolder.reload();
          } catch (error) {
            if (current()) window.PersonalAssistantFolderContext.notify(error.message);
          }
          return;
        }
      }
      return review('', true, offer?.id);
    }
  };
if (typeof document !== 'undefined') {
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', init);
  else init();
}
