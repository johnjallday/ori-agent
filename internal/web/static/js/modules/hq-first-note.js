// Shared by Home's result card and the HQ welcome. One draft/request identity
// per tab, retained across navigation, reload and uncertain capture responses.
import { HQ_LABELS as L } from './hq-labels.js';
import { hqLiveNotesOutcome } from './personal-hq-notes-review.js';
const NOTE_DRAFT_KEY = 'ori.hq.firstNote.v1';
let memoryNoteDraft = null;

export function newNoteRequestID() {
  const random = Math.random().toString(36).slice(2, 10);
  return `hq-note-${Date.now().toString(36)}-${random}`;
}

export function readNoteDraft() {
  if (memoryNoteDraft) return { ...memoryNoteDraft };
  try {
    const parsed = JSON.parse(window.sessionStorage.getItem(NOTE_DRAFT_KEY) || 'null');
    if (parsed && typeof parsed.text === 'string' && typeof parsed.requestId === 'string') {
      memoryNoteDraft = parsed;
      return { ...parsed };
    }
  } catch (_) {
    // Storage is optional; the same-page retry remains safe.
  }
  memoryNoteDraft = { text: '', requestId: newNoteRequestID() };
  return { ...memoryNoteDraft };
}

export function writeNoteDraft(draft) {
  memoryNoteDraft = { ...draft };
  try {
    window.sessionStorage.setItem(NOTE_DRAFT_KEY, JSON.stringify(draft));
  } catch (_) {
    // Storage is optional.
  }
}

export async function saveFirstNote(text, { readStatus, fetcher = fetch }) {
  const previous = readNoteDraft();
  text = previous.pendingText || text;
  const draft = { ...previous, text };
  writeNoteDraft(draft);
  if (!text.trim()) throw new Error(L.firstNoteEmpty);
  let uncertain = false;
  try {
    const notes = await readStatus();
    if (
      !hqLiveNotesOutcome(notes).writable ||
      !draft.sourceId ||
      draft.sourceId !== notes?.source?.id
    )
      throw new Error(L.firstNoteChanged);
    writeNoteDraft({ ...draft, pendingText: text });
    let response;
    try {
      response = await fetcher('/api/personal-assistant/second-brain/capture', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
        body: JSON.stringify({ text, request_id: draft.requestId })
      });
    } catch (_) {
      uncertain = true;
      throw new Error(L.firstNoteUncertain);
    }
    const body = await response.json().catch(() => ({}));
    if (!response.ok) {
      uncertain = response.status >= 500 || response.status === 409;
      throw new Error(body.error || L.firstNoteError);
    }
    const capture = body.capture || {};
    if (typeof capture.note !== 'string' || !capture.note.startsWith('Inbox/')) {
      uncertain = true;
      throw new Error(L.firstNoteReceiptError);
    }
    writeNoteDraft({ text: '', requestId: newNoteRequestID() });
    return { text: text.trim(), note: capture.note };
  } catch (error) {
    if (!uncertain) writeNoteDraft(draft);
    throw error;
  }
}
