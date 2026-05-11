// note-presence.js — cross-tab coordination for "which note is open where".
//
// The dedicated /notes/<id> page claims presence on load and releases on
// unload. Other surfaces (the modal in sessionManager) can call
// `isOpenElsewhere(noteId)` to ask whether any other tab in this browser is
// currently holding the same note. The modal uses this to warn ("already open
// in another tab") before opening a second copy.
//
// Falls back gracefully when BroadcastChannel isn't available — every call
// resolves to "not held elsewhere", so the rest of the UI continues to work.

const CHANNEL_NAME = 'note-presence';
// Unique per-tab id; lets us ignore our own broadcasts.
const TAB_ID = `t-${Math.random().toString(36).slice(2, 10)}-${Date.now().toString(36)}`;

let _channel = null;
let _supported = false;
try {
  if (typeof BroadcastChannel === 'function') {
    _channel = new BroadcastChannel(CHANNEL_NAME);
    _supported = true;
  }
} catch (_) {
  _channel = null;
  _supported = false;
}

// Notes this tab currently holds (page surface keeps these populated).
const _heldNotes = new Set();
// In-flight presence queries: noteId → { resolve, reject, hits, timer }.
const _pendingQueries = new Map();

function postMessage(payload) {
  if (!_channel) return;
  try { _channel.postMessage(payload); } catch (_) {}
}

if (_channel) {
  _channel.addEventListener('message', (ev) => {
    const msg = ev?.data;
    if (!msg || typeof msg !== 'object') return;
    if (msg.tabId === TAB_ID) return; // ignore our own
    handleIncoming(msg);
  });
}

function handleIncoming(msg) {
  switch (msg.type) {
    case 'who-has-note': {
      // Another tab is asking who holds a note. Reply if we do.
      if (msg.noteId && _heldNotes.has(msg.noteId)) {
        postMessage({
          type: 'i-have-note',
          tabId: TAB_ID,
          surface: msg.surface || 'page',
          requesterTabId: msg.tabId,
          noteId: msg.noteId,
        });
      }
      return;
    }
    case 'i-have-note': {
      if (msg.requesterTabId !== TAB_ID) return;
      const pending = _pendingQueries.get(msg.noteId);
      if (!pending) return;
      pending.hits.push({ tabId: msg.tabId, surface: msg.surface || 'page' });
      // First hit is enough for the modal's "warn before opening" check.
      if (!pending.resolved) {
        pending.resolved = true;
        clearTimeout(pending.timer);
        _pendingQueries.delete(msg.noteId);
        pending.resolve({ open: true, surface: msg.surface || 'page', tabId: msg.tabId });
      }
      return;
    }
    case 'note-released': {
      // Currently informational — we don't track other tabs' presence locally.
      return;
    }
    default:
      return;
  }
}

// claimOpenNote announces this tab is now showing noteId on the given surface
// (typically 'page'). Idempotent.
export function claimOpenNote(noteId, surface = 'page') {
  if (!noteId) return;
  _heldNotes.add(noteId);
  postMessage({ type: 'note-claimed', tabId: TAB_ID, noteId, surface });
}

// releaseOpenNote announces this tab has closed noteId. Idempotent.
export function releaseOpenNote(noteId) {
  if (!noteId) return;
  if (!_heldNotes.has(noteId)) return;
  _heldNotes.delete(noteId);
  postMessage({ type: 'note-released', tabId: TAB_ID, noteId });
}

// isOpenElsewhere asks other tabs if anyone is showing noteId. Resolves with
// `{open:true, surface, tabId}` on first hit, or `{open:false}` after the
// timeout. When BroadcastChannel is unsupported, resolves to `{open:false}`.
export function isOpenElsewhere(noteId, timeoutMs = 150) {
  if (!noteId || !_supported) return Promise.resolve({ open: false });
  // If we already have an in-flight query for this noteId, share the promise.
  const existing = _pendingQueries.get(noteId);
  if (existing) return existing.promise;

  const pending = { hits: [], resolved: false };
  pending.promise = new Promise((resolve) => {
    pending.resolve = resolve;
    pending.timer = setTimeout(() => {
      if (pending.resolved) return;
      pending.resolved = true;
      _pendingQueries.delete(noteId);
      resolve({ open: false });
    }, Math.max(50, Math.min(2000, timeoutMs)));
  });
  _pendingQueries.set(noteId, pending);
  postMessage({ type: 'who-has-note', tabId: TAB_ID, noteId });
  return pending.promise;
}

// For tests.
export function _resetForTesting() {
  _heldNotes.clear();
  _pendingQueries.forEach((p) => clearTimeout(p.timer));
  _pendingQueries.clear();
}

if (typeof window !== 'undefined') {
  window.NotePresence = {
    claimOpenNote,
    releaseOpenNote,
    isOpenElsewhere,
    supported: _supported,
    tabId: TAB_ID,
  };
}

export default {
  claimOpenNote,
  releaseOpenNote,
  isOpenElsewhere,
};
