// folder-agent-chat.js — the folder agent's report in the Commander's
// conversation on the workspace page (workspace chat sync, Plan B).
//
// The server posts the report as the Commander's message in its thread and
// names it in the view (view.chat: {session_id, message_id, unread}). The
// workspace page publishes its report here, with the card it draws (bound to
// the page's own actions); the drawer draws that card under the message when
// its thread shows it. The buttons stay host actions: the agent recommends,
// the host executes.

export const FOLDER_AGENT_CHAT_EVENT = 'ori:folder-agent-chat';
// Fired when the card appears in or leaves the drawer's thread, so the map's
// presenter can fold the balloon to its line (the chat holds the plan).
export const FOLDER_AGENT_CHAT_SHOWN_EVENT = 'ori:folder-agent-chat-shown';

const CARD_ATTR = 'data-folder-agent-chat-card';
const latest = new Map();

function text(value) {
  return String(value ?? '').trim();
}

// chatRefOf is the report's place in the conversation, or null. lead is the
// words the drawer shows for the message while the card under it asks the
// question ('' when the message is drawn as stored).
export function chatRefOf(raw) {
  const chat = raw?.chat;
  const sessionID = text(chat?.session_id);
  const messageID = text(chat?.message_id);
  if (!sessionID || !messageID) return null;
  return { sessionID, messageID, unread: chat.unread === true, lead: text(chat.lead) };
}

// folderReportReload decides whether the drawer's thread must be loaded again
// so the report's message (and the card under it) is in it: 'reload' when the
// thread shown is the report's (or none is remembered) and no row holds the
// message; 'defer' when that load must wait (a reply is in flight, or a pending
// confirmation or plan would be dropped); 'none' otherwise. A message already
// reloaded for (attempted) is not reloaded again, so a row the transcript cut
// off cannot loop. Whether the report is unread does not matter: a stale thread
// is wrong either way.
export function folderReportReload({
  ref,
  shownSessionID = '',
  rowIDs = [],
  busy = false,
  locked = false,
  attempted = ''
} = {}) {
  if (!ref?.sessionID || !ref?.messageID) return 'none';
  const shown = text(shownSessionID);
  if (shown && shown !== ref.sessionID) return 'none';
  if (Array.from(rowIDs || []).includes(ref.messageID)) return 'none';
  if (attempted === folderReportReloadKey(ref)) return 'none';
  return busy || locked ? 'defer' : 'reload';
}

// syncFolderReport runs that decision with its one effect injected: hydrate
// (force, exact) loads the thread again and resolves true when it did. It never
// changes thread, never touches a draft (a same-thread load leaves the
// composer alone) and loads nothing while a reply is in flight or a pending
// confirmation or plan would be dropped. Resolves the outcome:
//   loaded     the thread holds the message (it did, or it does now)
//   reloaded   a load ran and succeeded
//   deferred   a load is still owed (waiting on a reply, a lock, or a failure)
//   attempted  the message reloaded for, to carry into the next call
export async function syncFolderReport({
  ref,
  shownSessionID = '',
  rowIDs = [],
  busy = false,
  locked = false,
  attempted = '',
  hydrate
}) {
  const verdict = folderReportReload({ ref, shownSessionID, rowIDs, busy, locked, attempted });
  if (verdict === 'none') return { loaded: true, reloaded: false, deferred: false, attempted };
  if (verdict === 'defer' || typeof hydrate !== 'function')
    return { loaded: false, reloaded: false, deferred: true, attempted };
  const shown = text(shownSessionID);
  const hydrated = await hydrate({ force: true, exact: Boolean(shown) && shown === ref.sessionID });
  if (!hydrated) return { loaded: false, reloaded: false, deferred: true, attempted };
  return { loaded: true, reloaded: true, deferred: false, attempted: folderReportReloadKey(ref) };
}

// folderReportReloadKey names one report message in one thread.
export function folderReportReloadKey(ref) {
  return ref?.sessionID && ref?.messageID ? `${ref.sessionID}\n${ref.messageID}` : '';
}

// A row whose words were replaced by the lead-in keeps its stored words here.
const STORED_ATTR = 'data-folder-agent-stored';
const STORED_DATASET = 'folderAgentStored';

// The message's words are the bubble's last text node (an author line may come
// before it).
function messageTextNode(row) {
  const nodes = Array.from(row?.firstElementChild?.childNodes || []);
  for (let i = nodes.length - 1; i >= 0; i -= 1) if (nodes[i].nodeType === 3) return nodes[i];
  return null;
}

// showLead puts the lead-in in place of a message's stored words (the question
// is then said once, by the card under it) and keeps the stored words on the
// row, or puts them back when lead is ''. The stored message is untouched: the
// agent reads it back on its next turn. Plain text only.
function showLead(row, lead) {
  const node = messageTextNode(row);
  if (!node) return;
  if (lead) {
    if (row.dataset[STORED_DATASET] === undefined) row.dataset[STORED_DATASET] = node.textContent;
    if (node.textContent !== lead) node.textContent = lead;
  } else if (row.dataset[STORED_DATASET] !== undefined) {
    node.textContent = row.dataset[STORED_DATASET];
    delete row.dataset[STORED_DATASET];
  }
}

// publishFolderAgentChat records a workspace's latest report and its card
// factory (card(compact, {presentation}) → Element), then tells the drawer. The
// drawer asks for presentation 'chat'; the Map's balloon asks for compact only.
export function publishFolderAgentChat(
  { workspaceID, raw, card, error = '' },
  { win = globalThis.window } = {}
) {
  const id = text(workspaceID);
  if (!id || typeof card !== 'function') return;
  const previous = latest.get(id);
  const signature = `${JSON.stringify(raw ?? null)}|${error}`;
  const seen = previous && chatRefOf(previous.raw)?.messageID === chatRefOf(raw)?.messageID;
  latest.set(id, {
    workspaceID: id,
    raw,
    card,
    signature,
    // Seen here stays seen until the server says so too (no flicker back).
    seenLocally: Boolean(seen && previous.seenLocally)
  });
  if (previous?.signature === signature) return;
  if (typeof win?.CustomEvent === 'function')
    win.dispatchEvent?.(
      new win.CustomEvent(FOLDER_AGENT_CHAT_EVENT, { detail: { workspaceID: id } })
    );
}

export function folderAgentChat(workspaceID) {
  return latest.get(text(workspaceID)) || null;
}

// markFolderAgentChatSeen remembers that the drawer showed the report.
export function markFolderAgentChatSeen(workspaceID, messageID) {
  const entry = latest.get(text(workspaceID));
  if (entry && chatRefOf(entry.raw)?.messageID === text(messageID)) entry.seenLocally = true;
}

// folderAgentChatUnread is whether the workspace's report waits unseen.
export function folderAgentChatUnread(workspaceID) {
  const entry = latest.get(text(workspaceID));
  const ref = chatRefOf(entry?.raw);
  return Boolean(ref?.unread && !entry.seenLocally);
}

export function resetFolderAgentChat() {
  latest.clear();
}

// postFolderAgentChatSeen tells the server the drawer showed the report.
export async function postFolderAgentChatSeen(
  workspaceID,
  messageID,
  fetchImpl = globalThis.fetch
) {
  const id = text(workspaceID);
  const message = text(messageID);
  if (!id || !message || typeof fetchImpl !== 'function') return false;
  try {
    const response = await fetchImpl(
      `/api/personal-assistant/folder-agent/${encodeURIComponent(id)}/chat/seen`,
      {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
        body: JSON.stringify({ message_id: message })
      }
    );
    return Boolean(response?.ok);
  } catch (_) {
    return false;
  }
}

function escapeAttr(value) {
  const id = String(value || '');
  if (globalThis.CSS?.escape) return globalThis.CSS.escape(id);
  return id.replace(/["\\]/g, '\\$&');
}

// A control's identity: what it does, not what it says ("Not now" is on the
// song question and on the goal).
// A card's heading (or first words) is the "target" focus lands on; there is one
// per card, so it is identified by the card that owns it.
function controlIdentity(node) {
  if (node?.dataset?.chatTarget !== undefined) return 'target';
  return text(node?.dataset?.chatControl || node?.dataset?.folderAgentAction);
}

// owningStep is the follow-up card a control sits in ('' for the receipt or a
// single card).
function owningStep(host, node) {
  for (let at = node; at && at !== host; at = at.parentElement ?? at.parent) {
    const step = at.dataset?.chatStep;
    if (step) return step;
  }
  return '';
}

const LIVE_ATTR = 'data-folder-agent-chat-live';

// drawCard puts the card in its host. The host also keeps one live region that
// is never replaced: a redrawn status node is missed by screen readers, a
// region that already exists and changes is said.
function drawCard(host, card, doc) {
  let live = host.querySelector?.(`[${LIVE_ATTR}]`) || null;
  Array.from(host.children || []).forEach(kid => {
    if (kid !== live) kid.remove();
  });
  if (live && typeof host.insertBefore === 'function') {
    // The card stays first and the live region is never moved.
    host.insertBefore(card, live);
    return;
  }
  host.append(card);
  if (live) {
    host.append(live);
    return;
  }
  live = doc.createElement('div');
  live.className = 'folder-agent-chat-live';
  live.setAttribute(LIVE_ATTR, '');
  live.setAttribute('role', 'status');
  host.append(live);
}

// say announces a status change once: only when the words differ from the ones
// last said, and never for the card a thread opens on (it is not news).
function say(host, card) {
  const live = host.querySelector?.(`[${LIVE_ATTR}]`);
  if (!live) return;
  const words = text(card?.dataset?.announce);
  const first = host.dataset.announced === undefined;
  if (host.dataset.announced === words) return;
  host.dataset.announced = words;
  if (!first || words === '') live.textContent = words;
}

// attachFolderAgentChatCard draws the report's card under its message in the
// drawer's thread (transcript), or takes a stale card away. Returns the
// message row the card sits under, or null when the thread does not hold it.
export function attachFolderAgentChatCard(
  transcript,
  workspaceID,
  { doc = globalThis.document, win = globalThis.window } = {}
) {
  if (!transcript?.querySelectorAll) return null;
  const entry = latest.get(text(workspaceID));
  const ref = chatRefOf(entry?.raw);
  let changed = false;
  transcript.querySelectorAll(`[${CARD_ATTR}]`).forEach(node => {
    if (
      !ref ||
      node.dataset.messageId !== ref.messageID ||
      node.dataset.workspaceId !== entry.workspaceID
    ) {
      node.remove();
      changed = true;
    }
  });
  let row = ref
    ? transcript.querySelector(`[data-lead-message-id="${escapeAttr(ref.messageID)}"]`)
    : null;
  // A row from another thread is not this report's, whatever its message id.
  if (row?.dataset.leadSessionId && row.dataset.leadSessionId !== ref.sessionID) row = null;
  // A row whose words were replaced by a lead-in gets them back once the report
  // is no longer the question (answered, or a newer report came).
  transcript.querySelectorAll(`[${STORED_ATTR}]`).forEach(node => {
    if (!ref?.lead || node.dataset.leadMessageId !== ref.messageID) showLead(node, '');
  });
  if (row && ref.lead) showLead(row, ref.lead);
  if (row) {
    let host = transcript.querySelector(
      `[${CARD_ATTR}][data-message-id="${escapeAttr(ref.messageID)}"]`
    );
    if (!host) {
      host = doc.createElement('div');
      host.className = 'folder-agent-chat-card';
      host.setAttribute(CARD_ATTR, '');
      host.dataset.workspaceId = entry.workspaceID;
      host.dataset.messageId = ref.messageID;
      row.after(host);
      changed = true;
    }
    if (host.dataset.signature !== entry.signature) {
      // Redrawing keeps focus where the owner was when the same control comes back.
      const active = doc?.activeElement;
      const hadFocus = Boolean(active && host.contains?.(active));
      const label = hadFocus ? active.textContent : '';
      const identity = hadFocus ? controlIdentity(active) : '';
      const step = hadFocus ? owningStep(host, active) : '';
      const caret =
        hadFocus && typeof active.selectionStart === 'number' ? active.selectionStart : null;
      const card = entry.card(true, { presentation: 'chat' });
      drawCard(host, card, doc);
      host.dataset.signature = entry.signature;
      // After an explicit answer the card names where focus goes (the failed
      // control again, or the next heading); a background redraw never does.
      const answered = host.querySelector?.('[data-chat-focus]');
      if (answered) {
        delete answered.dataset.chatFocus;
        answered.focus?.({ preventScroll: true });
      } else if (hadFocus) {
        const again = [
          ...Array.from(host.querySelectorAll?.('button') || []),
          ...Array.from(host.querySelectorAll?.('input') || []),
          ...Array.from(host.querySelectorAll?.('[data-chat-target]') || [])
        ].find(control => {
          if (control.disabled) return false;
          // The control itself, in the card that owns it (two cards can both
          // offer "Not now"); older cards without identities match by label.
          if (identity)
            return controlIdentity(control) === identity && owningStep(host, control) === step;
          return control.textContent === label;
        });
        again?.focus?.({ preventScroll: true });
        if (again && caret !== null) again.setSelectionRange?.(caret, caret);
      }
      say(host, card);
    }
  }
  if (changed && typeof win?.CustomEvent === 'function')
    win.dispatchEvent?.(
      new win.CustomEvent(FOLDER_AGENT_CHAT_SHOWN_EVENT, {
        detail: { workspaceID: text(workspaceID) }
      })
    );
  return row || null;
}

// chatShowsFolderAgentReport is whether the open drawer's thread shows the
// workspace's report card (the chat holds the plan).
export function chatShowsFolderAgentReport(
  workspaceID,
  { doc = globalThis.document, win = globalThis.window } = {}
) {
  const panel = win?.PersonalAssistantPanel;
  if (panel?.isOpen?.() !== true || panel?.isSuspended?.() === true) return false;
  return Boolean(
    doc?.querySelector?.(
      `#homeAssistantConversation [${CARD_ATTR}][data-workspace-id="${escapeAttr(text(workspaceID))}"]`
    )
  );
}
