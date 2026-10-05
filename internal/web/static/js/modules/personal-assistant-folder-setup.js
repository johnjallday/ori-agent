// Presentation of server-authored canonical review references. Model prose never
// supplies a control, offer ID, candidate, route, or confirmation here.
import { folderReceiptView } from './personal-assistant-folder.js';

export function setupCandidates(observation) {
  return (observation?.projects || []).map(project => ({
    id: project.id,
    label: `${project.name}${project.root ? ' (whole folder)' : ''}${project.marker ? ` · ${project.marker}` : ''}`
  }));
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
let suggestion = null;
let choiceTrigger = null;

export function currentSuggestion(state, value) {
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

function renderSuggestion() {
  const existing = document.querySelector('[data-folder-setup-suggestion]');
  if (!currentSuggestion(current, suggestion)) {
    existing?.remove();
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
    existing?.remove();
    return;
  }
  const bubble = row.firstElementChild;
  if (!bubble) return;
  if (existing?.parentElement === bubble) {
    existing.querySelector('button').disabled = Boolean(current.pending);
    return;
  }
  existing?.remove();
  const handoff = document.createElement('div');
  handoff.dataset.folderSetupSuggestion = suggestion.message_id;
  handoff.className = 'personal-assistant-message__setup';
  const button = document.createElement('button');
  button.type = 'button';
  button.className = 'personal-assistant-message__action';
  button.textContent = 'Review suggested setup';
  button.disabled = Boolean(current.pending);
  button.setAttribute('aria-controls', 'personalAssistantFolderSetupChoices');
  button.setAttribute('aria-expanded', 'false');
  const value = suggestion;
  button.addEventListener('click', () => {
    if (!currentSuggestion(current, value)) return;
    openChoices(value.options, button);
  });
  const note = document.createElement('p');
  note.className = 'personal-assistant-folder-context__note';
  note.textContent =
    'Optional — choose the scope and review the effects. Setup requires your confirmation.';
  handoff.append(button, note);
  bubble.insertBefore(handoff, bubble.querySelector('.personal-assistant-message__actions'));
}

function applySuggestion(value) {
  document.querySelector('[data-folder-setup-suggestion]')?.remove();
  suggestion = currentSuggestion(current, value) ? value : null;
  renderSuggestion();
}

function closeChoices() {
  if (!elements) return;
  elements.choices.hidden = true;
  elements.open.setAttribute('aria-expanded', 'false');
  choiceTrigger?.setAttribute('aria-expanded', 'false');
  (choiceTrigger?.isConnected ? choiceTrigger : elements.open).focus();
  choiceTrigger = null;
}

async function review(candidateId, close = false, offerId) {
  closeChoices();
  const result = await window.PersonalAssistantFolderContext?.review?.(candidateId, close, offerId);
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
  if (!result?.conversation?.stored) return;
  window.PersonalAssistantConversation?.applyReply?.(result);
  // Reload only canonical data. This never scans, calls a model, or confirms.
  if (await window.PersonalAssistantConversation?.resume?.(result.conversation.id)) {
    const slots = Array.from(document.querySelectorAll('[data-folder-review-id]'));
    slots
      .findLast(slot => slot.dataset.folderReviewId === result.folder_context?.offer_id)
      ?.scrollIntoView({ block: 'nearest' });
  }
}

function openChoices(options, trigger = elements.open) {
  const candidates = setupCandidates(current?.observation).filter(
    candidate =>
      !Array.isArray(options) || options.some(option => option.candidate_id === candidate.id)
  );
  if (!candidates.length || current?.pending || current?.authority) return;
  choiceTrigger = trigger;
  if (candidates.length === 1 && setupCandidates(current.observation).length === 1) {
    void review(candidates[0].id);
    return;
  }
  elements.candidate.replaceChildren(new Option('Choose a folder…', ''));
  candidates.forEach(candidate =>
    elements.candidate.add(new Option(candidate.label, candidate.id))
  );
  candidateObservation = current.observation.id;
  elements.choices.hidden = false;
  elements.open.setAttribute('aria-expanded', 'true');
  trigger.setAttribute('aria-expanded', 'true');
  elements.candidate.focus();
}

function park(node) {
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
    park();
    reviews = {};
    owner = state.conversationId;
  }
  if (current?.offerId !== state.offerId || !state.offerId) park();
  current = { ...state };
  const available = setupCandidates(state.observation).length > 0;
  elements.open.hidden = !available;
  elements.open.disabled = state.pending || Boolean(state.authority);
  elements.open.title = state.authority ? 'Pick the folder again for a current setup review.' : '';
  if (!available || candidateObservation !== state.observation?.id) {
    elements.choices.hidden = true;
    elements.open.setAttribute('aria-expanded', 'false');
  }
  if (!currentSuggestion(current, suggestion)) suggestion = null;
  renderSuggestion();
  renderReferences();
}

function hydrate(id, savedReviews = {}, savedSuggestion = null) {
  if (id !== window.PersonalAssistantConversation?.currentId?.()) return;
  owner = id;
  reviews = savedReviews;
  const displayed = window.PersonalAssistantFolder?.current?.();
  if (reviews[displayed?.id])
    window.PersonalAssistantFolder.updateConversationReview(reviews[displayed.id]);
  contextChanged(window.PersonalAssistantFolderContext.current());
  applySuggestion(savedSuggestion);
}

function init() {
  const open = document.getElementById('personalAssistantReviewFolderSetup');
  if (!open || elements) return;
  elements = {
    open,
    choices: document.getElementById('personalAssistantFolderSetupChoices'),
    candidate: document.getElementById('personalAssistantFolderSetupCandidate')
  };
  open.addEventListener('click', openChoices);
  document.getElementById('personalAssistantFolderSetupReview').addEventListener('click', () => {
    if (elements.candidate.reportValidity()) void review(elements.candidate.value);
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
    reviews[offer.id] = offer;
    renderReferences();
  });
  contextChanged(window.PersonalAssistantFolderContext.current());
}

if (typeof window !== 'undefined')
  window.PersonalAssistantFolderSetup = {
    contextChanged,
    applySuggestion,
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
