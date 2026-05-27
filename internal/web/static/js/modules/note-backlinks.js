// note-backlinks.js — fetch + render the "Backlinks" section in the AI Assist
// rail. Hits GET /api/notes/{id}/backlinks and produces a clickable list of
// notes that reference the current note via [[wikilinks]].
//
// Hosts (modal sessionManager + page bootstrap) call loadBacklinksFor(noteId)
// after a note loads; nothing renders when there are no backlinks.

import { notePath } from './note-routes.js';

const SECTION_ID = 'noteBacklinksSection';
const LIST_ID = 'noteBacklinksList';
const COUNT_ID = 'noteBacklinksCount';

// Tracks which note the panel is currently displaying so the cross-tab edit
// handler at the bottom of this file knows what to re-fetch.
let _currentNoteId = null;

function escapeText(s) {
  return String(s ?? '').replace(/[&<>]/g, (c) => (
    { '&': '&amp;', '<': '&lt;', '>': '&gt;' }[c]
  ));
}

function escapeAttr(s) {
  return String(s ?? '').replace(/[&<>"']/g, (c) => (
    { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]
  ));
}

// renderBacklinkItem returns one row of HTML for the backlinks list. The
// snippet is bolded around the actual `[[target]]` text so users can spot
// the link in the source.
export function renderBacklinkItem(b) {
  const name = b.source_note_name || 'Untitled note';
  const ws = b.workspace_name ? ` · ${escapeText(b.workspace_name)}` : '';
  const snippet = highlightSnippet(b.context_snippet || '', b.display_text || b.target_text || '');
  const href = b.source_note_id ? notePath(b.source_note_id) : '#';
  return `<a href="${escapeAttr(href)}" class="note-backlink-item" data-note-id="${escapeAttr(b.source_note_id)}">
    <div class="note-backlink-name">${escapeText(name)}<span class="note-backlink-ws">${ws}</span></div>
    ${snippet ? `<div class="note-backlink-snippet">${snippet}</div>` : ''}
  </a>`;
}

// highlightSnippet wraps the link text inside the snippet in <mark>. Falls back
// to a plain escaped string when the link text isn't found (the server might
// have trimmed it out of the snippet).
export function highlightSnippet(snippet, linkText) {
  const text = String(snippet || '');
  if (!text) return '';
  const escaped = escapeText(text);
  const t = String(linkText || '').trim();
  if (!t) return escaped;
  // Highlight a [[...]] form first; fall back to the bare text.
  const wikilinkPattern = `\\[\\[${escapeRegex(t)}(?:\\|[^\\]]+)?\\]\\]`;
  const re = new RegExp(`(${wikilinkPattern}|${escapeRegex(t)})`, 'i');
  const m = escaped.match(re);
  if (!m) return escaped;
  return escaped.replace(re, `<mark class="note-backlink-mark">${m[1]}</mark>`);
}

function escapeRegex(s) {
  return String(s).replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

// renderBacklinksInto paints a list of backlinks into the section host and
// toggles visibility. When the array is empty the section stays hidden.
export function renderBacklinksInto(scopeRoot, backlinks) {
  const root = scopeRoot || document;
  const section = root.querySelector?.(`#${SECTION_ID}`) || document.getElementById(SECTION_ID);
  const list = root.querySelector?.(`#${LIST_ID}`) || document.getElementById(LIST_ID);
  const count = root.querySelector?.(`#${COUNT_ID}`) || document.getElementById(COUNT_ID);
  if (!section || !list) return;

  const items = Array.isArray(backlinks) ? backlinks : [];
  if (items.length === 0) {
    section.hidden = true;
    list.innerHTML = '';
    if (count) count.textContent = '';
    return;
  }

  list.innerHTML = items.map(renderBacklinkItem).join('');
  if (count) count.textContent = String(items.length);
  section.hidden = false;
  // Reveal the rail itself — backlinks alone should be reason enough to show it.
  // (Modal/page hosts may have it hidden if AI Assist hasn't been triggered.)
  const rail = document.getElementById('noteAssistRail');
  if (rail && rail.hidden) rail.hidden = false;
}

// loadBacklinksFor fetches /api/notes/{id}/backlinks and renders the result.
// Returns the array of backlinks for callers that want to do more with it.
export async function loadBacklinksFor(noteId, scopeRoot) {
  _currentNoteId = noteId || null;
  if (!noteId) {
    renderBacklinksInto(scopeRoot, []);
    return [];
  }
  try {
    const resp = await fetch(`/api/notes/${encodeURIComponent(noteId)}/backlinks`);
    if (!resp.ok) {
      console.warn('Backlinks fetch failed', resp.status);
      renderBacklinksInto(scopeRoot, []);
      return [];
    }
    const data = await resp.json();
    const backlinks = Array.isArray(data?.backlinks) ? data.backlinks : [];
    renderBacklinksInto(scopeRoot, backlinks);
    return backlinks;
  } catch (err) {
    console.warn('Backlinks fetch errored', err);
    renderBacklinksInto(scopeRoot, []);
    return [];
  }
}

// clearBacklinks is the modal's "new note" path — there's no note ID yet,
// so the section should be hidden.
export function clearBacklinks(scopeRoot) {
  _currentNoteId = null;
  renderBacklinksInto(scopeRoot, []);
}

// =============================================================================
// Cross-tab live refresh: when another tab saves a note containing wikilinks,
// this tab might be displaying that note's target — re-fetch the backlinks for
// the note we're currently showing.
// =============================================================================

let _channel = null;
try {
  if (typeof BroadcastChannel === 'function') {
    _channel = new BroadcastChannel('note-edits');
  }
} catch (_) { _channel = null; }

if (_channel) {
  _channel.addEventListener('message', (ev) => {
    const msg = ev?.data;
    if (!msg || msg.type !== 'saved' || !msg.hasWikilinks) return;
    if (!_currentNoteId) return;
    // The saved note might newly link to the one we're showing. Re-fetch.
    loadBacklinksFor(_currentNoteId);
  });
}

// announceNoteSaved is what host save paths call after a successful save so
// other tabs can react. hasWikilinks tells subscribers whether the change
// could affect any other note's backlinks.
export function announceNoteSaved(noteId, hasWikilinks) {
  if (!_channel || !noteId) return;
  try {
    _channel.postMessage({ type: 'saved', noteId, hasWikilinks: !!hasWikilinks });
  } catch (_) {}
}

if (typeof window !== 'undefined') {
  window.NoteBacklinks = {
    renderBacklinkItem,
    highlightSnippet,
    renderBacklinksInto,
    loadBacklinksFor,
    clearBacklinks,
    announceNoteSaved,
  };
}

export default {
  renderBacklinkItem,
  highlightSnippet,
  renderBacklinksInto,
  loadBacklinksFor,
  clearBacklinks,
  announceNoteSaved,
};
