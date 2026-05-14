// note-rail-notes.js — "Notes" tab content for the editor's left rail.
//
// The left rail in both the modal and the workspace notes page now
// hosts two tabs: Outline (existing TOC) and Notes (this module). The Notes
// tab lists every note in the active workspace with a quick-filter input and
// a "+ New note" button.
//
// Host registers a workspace resolver via setWorkspaceContext(fn) so the
// list refreshes when pane focus crosses workspaces. The active note ID is
// highlighted via setActiveNoteId.

import { notePath } from './note-routes.js';

const STORAGE_KEY_TAB = 'note.leftRail.tab';

let _getWorkspaceId = () => null;
let _activeNoteId = null;
let _notesCache = null;
let _cacheWorkspaceId = null;
let _filter = '';

export function setWorkspaceContext(fn) {
  if (typeof fn === 'function') _getWorkspaceId = fn;
}

export function setActiveNoteId(id) {
  _activeNoteId = id || null;
  // Repaint the list so the active-row highlight moves.
  paintList();
}

export function readActiveTab() {
  try {
    const v = localStorage.getItem(STORAGE_KEY_TAB);
    if (v === 'notes' || v === 'outline') return v;
  } catch (_) {}
  return 'notes';
}

export function writeActiveTab(tab) {
  try {
    if (tab === 'notes' || tab === 'outline') localStorage.setItem(STORAGE_KEY_TAB, tab);
  } catch (_) {}
}

// =============================================================================
// Pure helpers (exported for tests).
// =============================================================================

export function filterNotesByQuery(notes, query) {
  const q = String(query || '').trim().toLowerCase();
  if (!q) return notes;
  return notes.filter((n) => String(n.name || '').toLowerCase().includes(q));
}

export function sortNotesForRail(notes) {
  return notes.slice().sort((a, b) => {
    const at = a.updated_at || '';
    const bt = b.updated_at || '';
    if (at < bt) return 1;
    if (at > bt) return -1;
    return 0;
  });
}

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

export function renderListItem(note, isActive = false) {
  const id = escapeAttr(note.id);
  const name = escapeText(note.name || 'Untitled');
  const cls = `note-rail-note-item${isActive ? ' is-active' : ''}`;
  return `<button type="button" class="${cls}" data-note-id="${id}" title="${name}">
    <span class="note-rail-note-name">${name}</span>
  </button>`;
}

// =============================================================================
// DOM wiring.
// =============================================================================

async function fetchNotesForWorkspace(workspaceId, { force = false } = {}) {
  if (!workspaceId) return [];
  if (!force && _notesCache && _cacheWorkspaceId === workspaceId) return _notesCache;
  try {
    const resp = await fetch(`/api/workspaces/${encodeURIComponent(workspaceId)}/notes`);
    if (!resp.ok) return [];
    const data = await resp.json();
    _notesCache = Array.isArray(data?.notes) ? data.notes : [];
    _cacheWorkspaceId = workspaceId;
    return _notesCache;
  } catch (_) {
    return [];
  }
}

function paintList() {
  const list = document.getElementById('noteRailNotesList');
  if (!list) return;
  const notes = _notesCache || [];
  const view = sortNotesForRail(filterNotesByQuery(notes, _filter));
  if (view.length === 0) {
    list.innerHTML = `<div class="note-rail-empty">${
      notes.length === 0 ? 'No notes in this workspace yet.' : 'No notes match the filter.'
    }</div>`;
    return;
  }
  list.innerHTML = view.map((n) => renderListItem(n, n.id === _activeNoteId)).join('');
}

async function refresh({ force = false } = {}) {
  const wsId = _getWorkspaceId();
  if (!wsId) {
    _notesCache = [];
    _cacheWorkspaceId = null;
    paintList();
    return;
  }
  await fetchNotesForWorkspace(wsId, { force });
  paintList();
}

function setTab(tab) {
  const t = tab === 'notes' ? 'notes' : 'outline';
  writeActiveTab(t);
  document.querySelectorAll('.note-rail-tab').forEach((btn) => {
    const isActive = btn.dataset.tab === t;
    btn.classList.toggle('is-active', isActive);
    btn.setAttribute('aria-selected', isActive ? 'true' : 'false');
  });
  document.querySelectorAll('.note-rail-tab-pane').forEach((pane) => {
    pane.hidden = pane.dataset.pane !== t;
  });
  if (t === 'notes') refresh();
}

function openNoteFromRail(noteId) {
  if (!noteId) return;
  if (window.NotePage?.openNoteInTab) {
    window.NotePage.openNoteInTab(noteId);
  } else if (window.sessionManager?.openNote) {
    window.sessionManager.openNote(noteId);
  } else {
    window.location.href = notePath(noteId);
  }
}

async function createNewNote() {
  const wsId = _getWorkspaceId();
  if (!wsId) return;
  try {
    const resp = await fetch(`/api/workspaces/${encodeURIComponent(wsId)}/notes`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ name: 'Untitled', content: '' }),
    });
    if (!resp.ok) return;
    const data = await resp.json();
    const note = data?.note || data;
    // Invalidate cache so the new note appears on next refresh.
    _notesCache = null;
    if (note?.id) openNoteFromRail(note.id);
  } catch (_) {}
}

function bindRail() {
  document.querySelectorAll('.note-rail-tab').forEach((btn) => {
    if (btn.dataset._bound) return;
    btn.dataset._bound = '1';
    btn.addEventListener('click', () => setTab(btn.dataset.tab));
  });

  const search = document.getElementById('noteRailNotesSearch');
  if (search && !search.dataset._bound) {
    search.dataset._bound = '1';
    search.addEventListener('input', (e) => {
      _filter = e.target.value || '';
      paintList();
    });
  }

  const list = document.getElementById('noteRailNotesList');
  if (list && !list.dataset._bound) {
    list.dataset._bound = '1';
    list.addEventListener('click', (e) => {
      const item = e.target.closest('.note-rail-note-item');
      if (!item) return;
      e.preventDefault();
      openNoteFromRail(item.dataset.noteId);
    });
  }

  const newBtn = document.getElementById('noteRailNewNoteBtn');
  if (newBtn && !newBtn.dataset._bound) {
    newBtn.dataset._bound = '1';
    newBtn.addEventListener('click', createNewNote);
  }
}

// initRail is the entry point hosts call when the editor surface mounts. It's
// idempotent: re-binding existing handlers is a no-op.
export function initRail({ workspaceIdResolver, activeNoteId } = {}) {
  if (typeof workspaceIdResolver === 'function') _getWorkspaceId = workspaceIdResolver;
  if (activeNoteId !== undefined) _activeNoteId = activeNoteId;
  bindRail();
  setTab(readActiveTab());
}

// invalidate clears the cache so the next tab switch re-fetches.
export function invalidate() {
  _notesCache = null;
  _cacheWorkspaceId = null;
}

if (typeof window !== 'undefined') {
  window.NoteRailNotes = {
    initRail,
    setActiveNoteId,
    setWorkspaceContext,
    invalidate,
    readActiveTab,
    filterNotesByQuery,
    sortNotesForRail,
    renderListItem,
  };
}

export default {
  initRail,
  setActiveNoteId,
  setWorkspaceContext,
  invalidate,
  readActiveTab,
  filterNotesByQuery,
  sortNotesForRail,
  renderListItem,
};
