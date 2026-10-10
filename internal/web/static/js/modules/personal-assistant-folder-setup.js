// Presentation of server-authored canonical review references. Model prose never
// supplies a control, offer ID, candidate, route, or confirmation here.
import { folderReceiptView } from './personal-assistant-folder.js';
import {
  proposalView,
  placementProposalView,
  renderProposal
} from './personal-assistant-proposal.js';

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
  note.textContent =
    'Setup from your attached folder — no assistant reply or model is required. Choose scope explicitly; discussion topics are separate.';
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
  const place = known
    ? context.operation === 'link_supporting_folder'
      ? ` as a supporting folder in ${name}`
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
        context.blocker === 'destination_changed'
          ? `The reviewed destination for ${subject} has changed. Use Review setup to refresh the review.`
          : context.blocker === 'destination_unavailable'
            ? `The reviewed destination for ${subject} could not be read. Use Review setup to refresh the review.`
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
  return {
    message: `${subject} is already set up as the project “${workspace}”. Nothing new has been prepared.`,
    route: /^\/workspaces\/[A-Za-z0-9][A-Za-z0-9._:-]*$/.test(route) ? route : '',
    openLabel: `Open ${workspace}`
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
    existing.querySelector('button').disabled = Boolean(current.pending);
    return;
  }
  retireSuggestion(existing);
  const handoff = document.createElement('div');
  handoff.dataset.folderSetupSuggestion = suggestion.message_id;
  handoff.className = 'personal-assistant-message__setup';
  const options = suggestion.options || [];
  const project = current.observation?.projects?.find(
    project => project.id === options[0]?.candidate_id
  );
  let view = proposalView(
    options.length === 1 ? options[0] : {},
    options.length === 1 ? project : { name: current.observation?.folder, root: true }
  );
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
    openChoices(value.options, button, value.subject?.workspace_id || '');
  });
  const note = document.createElement('p');
  note.className = 'personal-assistant-folder-context__note';
  note.textContent = suggestion.offer_id
    ? 'Focuses the existing canonical review only. Setup still requires its reviewed confirmation.'
    : suggestion.subject?.name
      ? `Workspace you named: ${String(suggestion.subject.name)}. Placement and operation are rechecked in Review; setup still requires confirmation.`
      : 'Choose scope and review effects before confirming. Nothing is set up here.';
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
      void window.PersonalAssistantConversation?.resume?.(pending.conversation_id);
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
          label: 'Existing workspace',
          scope: 'Already set up',
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
    placementNode = document.createElement('section');
    placementNode.className = 'personal-assistant-message__setup';
    placementNode.setAttribute('aria-label', 'Choose folder placement');
    const message = document.createElement('p');
    message.textContent = String(choice.message || 'Choose the operation to review.');
    placementNode.append(message);
    for (const option of choice.options || []) {
      if (!['create_project_workspace', 'link_supporting_folder'].includes(option.operation))
        continue;
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
    const cancel = document.createElement('button');
    cancel.type = 'button';
    cancel.className = 'btn btn-sm btn-link';
    cancel.textContent = 'Keep chatting';
    cancel.addEventListener('click', () => {
      closeChoices();
    });
    placementNode.append(cancel);
    showPlacement(placementNode.querySelector('button'), shouldFocus);
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
    closeChoices(shouldFocus);
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
        offer.outcome?.kind === 'home'
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
  if (choiceOrigin && !choiceIsCurrent(choiceOrigin.binding, current)) {
    if (flowHasFocus()) retireSuggestion(choiceOrigin.parent);
    else closeChoices(false);
  }
  const available = setupCandidates(state.observation).length > 0;
  elements.open.hidden = !available;
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
  choicesMarker = document.createComment('single setup chooser parking slot');
  elements.choices.after(choicesMarker);
  open.addEventListener('click', () => openChoices());
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
