// Hired-assistant conversations in the Personal Assistant panel.
//
// A conversation is a canonical Session in Personal HQ; the server decides its
// owner on every request. This module only holds the one thing a browser may
// send — an opaque conversation ID — and keeps it per tab, so two tabs never
// share a thread by accident.

import { renderTurnWorkspace, renderTurnSources } from './personal-assistant-workspace-context.js';
import { renderFolderFocus } from './personal-assistant-folder-tree.js';
import { folderDiscussionBinding } from './personal-assistant-folder-presentation.js';

const LIST_ENDPOINT = '/api/home-assistant/conversations';
const STORAGE_KEY = 'ori.personalAssistant.conversation';
const PANEL_ORIGIN = 'personal_assistant_panel';

export const CONVERSATION_ERRORS = {
  conversation_not_found:
    'That conversation no longer exists. Nothing was sent; your message is back in the box.',
  conversation_out_of_scope:
    'That conversation does not belong to your assistant’s Personal HQ. Nothing was sent; your message is back in the box.',
  conversation_unavailable:
    'Conversation history could not be read right now. Nothing was sent; your message is back in the box — try again in a moment.',
  assistant_not_ready: 'Finish personal assistant setup before opening conversations.',
  folder_context_conflict:
    'This conversation’s folder context changed. Reopen it before sending; your draft is kept.',
  folder_context_unavailable:
    'That folder selection is unavailable. Reopen this conversation to discuss its saved observations, or pick again. Your draft is kept.',
  context_save_failed:
    'This reply was not saved with its workspace context. Your draft is kept; reopen the conversation before retrying.',
  folder_context_save_failed:
    'This reply was not saved with its folder context. Your draft and intended folder are kept; check the conversation before retrying.'
};

/** The only conversation input a request carries: an opaque ID, or none. */
export function conversationRequest(currentId) {
  return { id: String(currentId || '').trim() };
}

/** Whether a route context came from the Personal Assistant panel. */
export function isPanelRequest(routeContext) {
  return String(routeContext?.origin || '') === PANEL_ORIGIN;
}

/**
 * The tab's conversation ID after a reply. A conversation that is gone or not
 * the assistant's is dropped, so the next message starts a new one instead of
 * retrying a dead thread. A history read that merely failed keeps the ID: the
 * conversation is still there, and the retry belongs in it.
 */
export function nextConversationId(currentId, reply) {
  if (!reply) return String(currentId || '');
  if (
    reply.error === 'conversation_unavailable' ||
    reply.error === 'context_save_failed' ||
    String(reply.error || '').startsWith('folder_context_')
  )
    return String(currentId || '');
  if (reply.error) return '';
  return String(reply.id || currentId || '');
}

/**
 * Whether the typed text must go back into the composer: the turn was refused,
 * or no answer came back. A stored or shown answer never restores it.
 */
export function shouldRestoreInput(data) {
  return Boolean(data?.model_unavailable || data?.conversation?.error);
}

/** One line telling the user what happened to this turn's history. */
export function conversationNotice(data) {
  const reply = data?.conversation;
  if (!reply) return '';
  if (reply.error)
    return CONVERSATION_ERRORS[reply.error] || CONVERSATION_ERRORS.conversation_unavailable;
  if (data.model_unavailable)
    return 'No answer yet. Your message is back in the box and was not saved.';
  if (!reply.stored) {
    return 'This reply could not be saved to the conversation history. Copy anything you want to keep.';
  }
  if (reply.history_truncated) {
    return 'Saved. Earlier messages are still stored but were left out of this reply.';
  }
  return ''; // Routine saved history needs no permanent status line.
}

/** The label for one conversation in the Continue list. */
export function conversationLabel(summary, now = new Date()) {
  const title = String(summary?.title || '').trim() || 'Conversation';
  const count = Number(summary?.message_count || 0);
  const updated = new Date(summary?.updated_at || '');
  const parts = [`${count} message${count === 1 ? '' : 's'}`];
  if (!Number.isNaN(updated.getTime())) {
    const sameDay = updated.toDateString() === now.toDateString();
    parts.push(
      sameDay
        ? updated.toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' })
        : updated.toLocaleDateString([], { month: 'short', day: 'numeric' })
    );
  }
  return { title, meta: parts.join(' · ') };
}

/** Marks a rendered message row with its canonical IDs. */
export function tagMessageRow(row, conversationId, messageId) {
  if (!row || !row.dataset || !messageId) return false;
  row.dataset.messageId = String(messageId);
  row.dataset.conversationId = String(conversationId || '');
  return true;
}

const state = {
  id: '',
  title: '',
  available: false,
  hydrated: false,
  loading: false,
  generation: 0,
  els: null
};

function readStoredId() {
  try {
    return String(window.sessionStorage.getItem(STORAGE_KEY) || '').trim();
  } catch (_) {
    return '';
  }
}

function writeStoredId(id) {
  try {
    if (id) window.sessionStorage.setItem(STORAGE_KEY, id);
    else window.sessionStorage.removeItem(STORAGE_KEY);
  } catch (_) {
    // A tab without storage still works; it just starts fresh after a reload.
  }
}

function setCurrent(id, title) {
  state.id = String(id || '').trim();
  state.title = state.id ? String(title || state.title || '').trim() : '';
  writeStoredId(state.id);
  render();
}

function setNote(message) {
  if (state.els?.note) state.els.note.textContent = String(message || '');
}

function render() {
  const els = state.els;
  if (!els) return;
  els.bar.hidden = !state.available;
  els.title.textContent = state.id ? state.title || 'Conversation' : 'New conversation';
  els.fresh.disabled =
    !state.id &&
    !hasRenderedMessages() &&
    !window.PersonalAssistantFolderContext?.hasFolder?.() &&
    !window.PersonalAssistantFolderContext?.isPending?.();
}

function hasRenderedMessages() {
  return Boolean(window.OriAskRouting?.getState?.().hasConversation);
}

function closeList() {
  if (!state.els) return;
  state.els.list.hidden = true;
  state.els.list.replaceChildren();
  state.els.resume.setAttribute('aria-expanded', 'false');
}

/**
 * Marks a rendered message with its canonical IDs and gives a stored assistant
 * reply its message actions. An unsaved row gets neither.
 */
function attachMessage(row, conversationId, messageId) {
  if (!tagMessageRow(row, conversationId, messageId)) return;
  window.PersonalAssistantDrafts?.decorate?.(row);
  window.PersonalAssistantMemory?.decorate?.(row);
}

/** A review belongs to the conversation it was opened in; leaving it closes it. */
function closeReviews() {
  window.PersonalAssistantDrafts?.close?.();
  window.PersonalAssistantMemory?.close?.();
}

/** Leaving a conversation also stops working on the draft saved from it. */
function leaveSavedDraft() {
  window.PersonalAssistantDrafts?.clearWorking?.();
}

/** Clears the tab's thread, its rendered log, and any pending confirmation. */
function startNew() {
  if (window.OriAskRouting?.resetConversation && !window.OriAskRouting.resetConversation()) {
    setNote('Wait for the current reply before starting a new conversation.');
    return false;
  }
  state.generation++;
  closeReviews();
  leaveSavedDraft();
  closeList();
  setCurrent('', '');
  window.PersonalAssistantFolderContext?.reset?.();
  setNote('');
  document.getElementById('personalAssistantInput')?.focus();
  return true;
}

async function readJSON(url) {
  const response = await fetch(url, { headers: { Accept: 'application/json' } });
  let body = null;
  try {
    body = await response.json();
  } catch (_) {
    body = null;
  }
  return { ok: response.ok, status: response.status, body };
}

/** Loads one validated conversation and renders it as the tab's thread. */
async function resume(id, options = {}) {
  const target = String(id || '').trim();
  if (!target || state.loading) return false;
  state.loading = true;
  window.PersonalAssistantFolderContext?.refreshDiscussion?.();
  const generation = ++state.generation;
  let hydrationBatch = false;
  try {
    const result = await readJSON(`${LIST_ENDPOINT}/${encodeURIComponent(target)}`);
    if (generation !== state.generation) return false;
    if (!result.ok || !result.body?.conversation) {
      const code = String(result.body?.error || 'conversation_unavailable');
      // A thread that is gone or no longer in scope is dropped, never recreated.
      if (code !== 'conversation_unavailable' && state.id === target) setCurrent('', '');
      if (!options.silent || code !== 'conversation_unavailable') {
        setNote(
          code === 'conversation_not_found'
            ? 'That conversation was deleted. Start a new one to continue.'
            : code === 'conversation_out_of_scope'
              ? 'That conversation does not belong to your assistant’s Personal HQ.'
              : 'That conversation could not be opened right now.'
        );
      }
      return false;
    }
    if (window.OriAskRouting?.resetConversation && !window.OriAskRouting.resetConversation()) {
      setNote('Wait for the current reply before opening another conversation.');
      return false;
    }
    closeReviews();
    window.PersonalAssistantFolderContext?.resetEvents?.();
    const conversation = result.body.conversation;
    const messages = result.body.messages || [];
    window.PersonalAssistantTranscript?.beginHydration?.();
    hydrationBatch = true;
    for (const message of messages) {
      if (message.role === 'folder_context' && message.folder_context) {
        window.PersonalAssistantFolderContext?.renderEvent?.(
          message.id,
          message.folder_context,
          null,
          { historical: true }
        );
        continue;
      }
      const row = window.OriAskRouting?.appendMessage?.(message.role, message.content);
      renderTurnWorkspace(row, message.workspace_context, { historical: true });
      if (message.role === 'user' && !message.imported)
        renderFolderFocus(row, message.folder_focus);
      // A saved reply lists what it read then; reloading reads nothing again.
      if (message.role === 'assistant')
        renderTurnSources(row, message.workspace_context, { historical: true });
      attachMessage(row, conversation.id, message.id);
    }
    // The panel keeps a bounded number of rows, so a long conversation shows
    // its latest part even when the server returned all of it.
    const shown = document.querySelectorAll('#homeAssistantConversation [data-message-id]').length;
    const partial =
      result.body.truncated === true ||
      shown < messages.filter(message => message.role !== 'folder_context').length;
    // Which replies were saved, read from Personal HQ's Tickets. When that
    // read failed, nothing is labelled rather than labelled "not saved".
    if (Array.isArray(result.body.saved)) {
      window.PersonalAssistantDrafts?.applySaved?.(result.body.saved);
    } else {
      leaveSavedDraft();
    }
    closeList();
    setCurrent(conversation.id, conversation.title);
    window.PersonalAssistantFolderContext?.hydrate?.(
      conversation.id,
      result.body.folder_context || {}
    );
    window.PersonalAssistantFolderSetup?.hydrate?.(
      conversation.id,
      result.body.folder_reviews || {},
      result.body.folder_setup_suggestion || null,
      result.body.folder_review_context || null,
      result.body.folder_review_elsewhere || null
    );
    const discussion = folderDiscussionBinding(
      messages,
      conversation.id,
      result.body.folder_context
    );
    window.PersonalAssistantFolderContext?.bindDiscussion?.(
      discussion ? { ...discussion, restored: true } : null
    );
    setNote(
      partial
        ? 'Showing the most recent messages of this conversation. Earlier ones are still stored.'
        : ''
    );
    return true;
  } catch (_) {
    if (!options.silent) setNote('That conversation could not be opened right now.');
    return false;
  } finally {
    state.loading = false;
    if (hydrationBatch && generation === state.generation)
      window.PersonalAssistantTranscript?.endHydration?.();
    window.PersonalAssistantFolderContext?.refreshDiscussion?.();
  }
}

async function toggleList() {
  const els = state.els;
  if (!els) return;
  if (!els.list.hidden) {
    closeList();
    return;
  }
  els.resume.setAttribute('aria-expanded', 'true');
  els.list.hidden = false;
  els.list.replaceChildren(listItem('Loading conversations…'));
  let result;
  try {
    result = await readJSON(LIST_ENDPOINT);
  } catch (_) {
    result = { ok: false, body: null };
  }
  if (!result.ok) {
    const code = String(result.body?.error || '');
    els.list.replaceChildren(
      listItem(CONVERSATION_ERRORS[code] || 'Conversations could not be listed right now.')
    );
    return;
  }
  const items = [];
  for (const summary of result.body?.conversations || []) {
    const label = conversationLabel(summary);
    const button = document.createElement('button');
    button.type = 'button';
    button.className = 'personal-assistant-conversation__item';
    button.dataset.conversationId = String(summary.id);
    if (summary.id === state.id) button.setAttribute('aria-current', 'true');
    const title = document.createElement('span');
    title.className = 'personal-assistant-conversation__item-title';
    title.textContent = label.title;
    const meta = document.createElement('span');
    meta.className = 'personal-assistant-conversation__item-meta';
    meta.textContent = summary.id === state.id ? `${label.meta} · open now` : label.meta;
    button.append(title, meta);
    button.addEventListener('click', () => void resume(summary.id));
    const item = document.createElement('li');
    item.append(button);
    items.push(item);
  }
  if (!items.length) items.push(listItem('No earlier conversations yet.'));
  const href = String(result.body?.manage_href || '').trim();
  if (href) {
    const link = document.createElement('a');
    link.href = href;
    link.textContent = 'Rename or delete conversations in Personal HQ';
    const item = document.createElement('li');
    item.className = 'personal-assistant-conversation__manage';
    item.append(link);
    items.push(item);
  }
  els.list.replaceChildren(...items);
}

function listItem(text) {
  const item = document.createElement('li');
  item.className = 'personal-assistant-conversation__empty';
  item.textContent = text;
  return item;
}

/** After a reload in the same tab, show the thread the tab was in. */
function hydrate() {
  if (state.hydrated || !state.available) return;
  state.hydrated = true;
  const stored = readStoredId();
  if (!stored) return;
  state.id = stored;
  render();
  if (!hasRenderedMessages()) void resume(stored, { silent: true });
}

function applyStatus(detail) {
  state.available = detail?.view?.available === true;
  if (!state.available) closeList();
  render();
  hydrate();
}

/** Called by the Ask controller before a request leaves the panel. */
function request(routeContext) {
  if (!state.available || !isPanelRequest(routeContext)) return null;
  return conversationRequest(state.id);
}

/**
 * Called by the Ask controller with the server's reply. Tags the rendered rows
 * with their canonical message IDs and returns what the controller should do.
 */
function applyReply(data, rows = {}) {
  window.PersonalAssistantFolderContext?.bindDiscussion?.(null);
  if (data?.workspace_context) {
    renderTurnWorkspace(rows.userRow, data.workspace_context);
    renderTurnWorkspace(rows.assistantRow, data.workspace_context);
    renderTurnSources(rows.assistantRow, data.workspace_context);
  }
  const reply = data?.conversation;
  if (!reply) return { notice: '', stored: false, restoreInput: shouldRestoreInput(data) };
  const nextId = nextConversationId(state.id, reply);
  if (reply.stored) {
    attachMessage(rows.userRow, nextId, reply.user_message_id);
    renderFolderFocus(rows.userRow, reply.folder_focus);
    attachMessage(rows.assistantRow, nextId, reply.assistant_message_id);
    if (reply.assistant_message_id) window.PersonalAssistantDrafts?.replyAdded?.();
  }
  setCurrent(nextId, reply.title);
  if (reply.stored && data.folder_context) {
    window.PersonalAssistantFolderContext?.renderEvent?.(
      data.folder_context.revision,
      {
        version: 1,
        observation: data.folder_context.observation,
        offer_id: data.folder_context.offer_id
      },
      rows.userRow,
      {
        historical:
          Boolean(data.folder_context.authority) || data.folder_context.historical === true
      }
    );
    window.PersonalAssistantFolderContext?.accepted?.(nextId, data.folder_context);
  } else if (reply.stored) {
    // A turn without a folder may have just saved this conversation; a folder
    // added next belongs to it.
    window.PersonalAssistantFolderContext?.adopt?.(nextId);
  }
  if (reply.stored)
    window.PersonalAssistantFolderSetup?.applySuggestion?.(data.folder_setup_suggestion || null);
  // The same canonical review summary the model was given for this turn.
  window.PersonalAssistantFolderSetup?.applyReviewContext?.(
    data.folder_review_context || null,
    data.folder_review_elsewhere || null
  );
  if (reply.stored && data.folder_context && rows.assistantRow) {
    window.PersonalAssistantFolderContext?.bindDiscussion?.(
      folderDiscussionBinding(
        [
          {
            id: data.folder_context.revision,
            role: 'folder_context',
            folder_context: { version: 1, observation: data.folder_context.observation }
          },
          { id: reply.user_message_id, role: 'user' },
          { id: reply.assistant_message_id, role: 'assistant', content: data.response }
        ],
        nextId,
        data.folder_context
      )
    );
  }
  const notice = conversationNotice(data);
  setNote(notice);
  return { notice, stored: reply.stored === true, restoreInput: shouldRestoreInput(data) };
}

function init() {
  const bar = document.getElementById('personalAssistantConversationBar');
  if (!bar) return;
  state.els = {
    bar,
    title: document.getElementById('personalAssistantConversationTitle'),
    fresh: document.getElementById('personalAssistantConversationNew'),
    resume: document.getElementById('personalAssistantConversationContinue'),
    list: document.getElementById('personalAssistantConversationList'),
    note: document.getElementById('personalAssistantConversationNote')
  };
  state.els.fresh.addEventListener('click', startNew);
  state.els.resume.addEventListener('click', () => void toggleList());
  // The panel's own Escape handler closes the whole drawer; an open list
  // closes first and keeps the drawer.
  state.els.list.addEventListener('keydown', event => {
    if (event.key !== 'Escape') return;
    event.stopPropagation();
    closeList();
    state.els.resume.focus();
  });
  document.addEventListener('personal-assistant:status', event => applyStatus(event.detail));
  const current = window.PersonalAssistantPanel?._state;
  if (current?.view?.known) applyStatus({ view: current.view });
  setNote('');
  render();
}

const api = {
  init,
  request,
  applyReply,
  startNew,
  resume,
  // Lets a message action report a refusal in the conversation bar.
  notify: setNote,
  currentId: () => state.id,
  refresh: render,
  isLoading: () => state.loading,
  _state: state
};
if (typeof window !== 'undefined') window.PersonalAssistantConversation = api;
if (typeof document !== 'undefined') {
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', init);
  else init();
}
