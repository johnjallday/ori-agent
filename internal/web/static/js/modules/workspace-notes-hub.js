// workspace-notes-hub.js — bootstrap for /workspaces/<id>/notes
//
// Loads the workspace's notes, renders a sortable/filterable table, and wires
// the row-action affordances (Open in modal, Open in page, Rename, Delete,
// Copy link). The hub is a separate page from the workspace detail view —
// it's the bookmarkable surface for browsing many notes at once.

function readWorkspaceIdFromPath() {
  if (typeof window === 'undefined') return '';
  const parts = window.location.pathname.split('/').filter(Boolean);
  // ['workspaces', '<uuid>', 'notes'] — take the segment after 'workspaces'.
  const idx = parts.indexOf('workspaces');
  if (idx < 0 || idx + 1 >= parts.length) return '';
  return decodeURIComponent(parts[idx + 1] || '');
}

const WORKSPACE_ID = readWorkspaceIdFromPath();

// =============================================================================
// Pure helpers — exported for unit tests.
// =============================================================================

export function filterNotes(notes, query) {
  const q = String(query || '').trim().toLowerCase();
  if (!q) return notes;
  return notes.filter((n) => {
    const name = String(n.name || '').toLowerCase();
    const content = String(n.content || '').toLowerCase();
    return name.includes(q) || content.includes(q);
  });
}

export function sortNotes(notes, sortKey, direction) {
  const arr = notes.slice();
  const dir = direction === 'asc' ? 1 : -1;
  arr.sort((a, b) => {
    let av;
    let bv;
    if (sortKey === 'name') {
      av = String(a.name || '').toLowerCase();
      bv = String(b.name || '').toLowerCase();
    } else if (sortKey === 'created_at') {
      av = a.created_at || '';
      bv = b.created_at || '';
    } else {
      av = a.updated_at || '';
      bv = b.updated_at || '';
    }
    if (av < bv) return -1 * dir;
    if (av > bv) return 1 * dir;
    return 0;
  });
  return arr;
}

export function formatRelativeTime(iso, now = Date.now()) {
  if (!iso) return '';
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return '';
  const diff = Math.max(0, now - t);
  const s = Math.floor(diff / 1000);
  if (s < 60) return 'just now';
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ago`;
  const d = Math.floor(h / 24);
  if (d < 30) return `${d}d ago`;
  const mo = Math.floor(d / 30);
  if (mo < 12) return `${mo}mo ago`;
  const y = Math.floor(d / 365);
  return `${y}y ago`;
}

export function snippetFromContent(content, max = 80) {
  if (!content) return '';
  // Strip leading whitespace + headings markup so the snippet is readable.
  const flat = String(content)
    .replace(/^\s*#{1,6}\s*/gm, '') // headings
    .replace(/`{1,3}[^`]*`{1,3}/g, ' ') // inline code
    .replace(/\[\[([^\]|]+)(?:\|[^\]]+)?\]\]/g, '$1') // wikilinks → target
    .replace(/\s+/g, ' ')
    .trim();
  if (flat.length <= max) return flat;
  return flat.slice(0, max).trimEnd() + '…';
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

export function renderRow(note, isSelected = false) {
  const id = escapeAttr(note.id);
  const name = escapeText(note.name || 'Untitled');
  const snippet = escapeText(snippetFromContent(note.content));
  const updated = escapeText(formatRelativeTime(note.updated_at));
  const created = escapeText(formatRelativeTime(note.created_at));
  return `<tr data-note-id="${id}" class="workspace-notes-hub-row${isSelected ? ' is-selected' : ''}">
    <td class="workspace-notes-hub-td-checkbox">
      <input type="checkbox" class="hub-row-checkbox" data-note-id="${id}" aria-label="Select ${name}"${isSelected ? ' checked' : ''}>
    </td>
    <td class="workspace-notes-hub-td-name">
      <a href="/notes/${id}" class="workspace-notes-hub-title-link" data-note-id="${id}">${name}</a>
      ${snippet ? `<div class="workspace-notes-hub-snippet">${snippet}</div>` : ''}
    </td>
    <td class="workspace-notes-hub-td-time">${updated}</td>
    <td class="workspace-notes-hub-td-time">${created}</td>
    <td class="workspace-notes-hub-td-actions">
      <button type="button" class="hub-row-action" data-action="open-page" data-note-id="${id}" title="Open as page">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor"><path d="M14,3V5H17.59L7.76,14.83L9.17,16.24L19,6.41V10H21V3M19,19H5V5H12V3H5C3.89,3 3,3.9 3,5V19A2,2 0 0,0 5,21H19A2,2 0 0,0 21,19V12H19V19Z"/></svg>
      </button>
    </td>
  </tr>`;
}

// =============================================================================
// Page state + DOM wiring.
// =============================================================================

const state = {
  notes: [],
  filter: '',
  sortKey: 'updated_at',
  sortDirection: 'desc',
  selected: new Set(),
};

function getView() {
  return sortNotes(filterNotes(state.notes, state.filter), state.sortKey, state.sortDirection);
}

async function fetchNotes() {
  const resp = await fetch(`/api/workspaces/${encodeURIComponent(WORKSPACE_ID)}/notes`);
  if (!resp.ok) throw new Error(`Failed to load notes: ${resp.status}`);
  const data = await resp.json();
  return Array.isArray(data?.notes) ? data.notes : [];
}

async function fetchWorkspace() {
  try {
    const resp = await fetch(`/api/workspaces/${encodeURIComponent(WORKSPACE_ID)}`);
    if (!resp.ok) return null;
    return await resp.json();
  } catch (_) { return null; }
}

function showToast(msg, kind) {
  if (typeof window?.showToast === 'function') window.showToast(msg, kind);
  else console.log(`[${kind || 'info'}] ${msg}`);
}

function paint() {
  const tbody = document.getElementById('hubTableBody');
  const table = document.getElementById('hubTable');
  const loading = document.getElementById('hubLoading');
  const empty = document.getElementById('hubEmpty');
  const noResults = document.getElementById('hubNoResults');
  const count = document.getElementById('hubCount');
  const bulkDelete = document.getElementById('hubBulkDeleteBtn');

  if (loading) loading.hidden = true;

  const view = getView();
  if (state.notes.length === 0) {
    if (empty) empty.hidden = false;
    if (noResults) noResults.hidden = true;
    if (table) table.hidden = true;
    if (count) count.textContent = '0 notes';
  } else if (view.length === 0) {
    if (empty) empty.hidden = true;
    if (noResults) noResults.hidden = false;
    if (table) table.hidden = true;
    if (count) count.textContent = `0 of ${state.notes.length}`;
  } else {
    if (empty) empty.hidden = true;
    if (noResults) noResults.hidden = true;
    if (table) table.hidden = false;
    if (tbody) tbody.innerHTML = view.map((n) => renderRow(n, state.selected.has(n.id))).join('');
    if (count) {
      count.textContent = view.length === state.notes.length
        ? `${state.notes.length} note${state.notes.length === 1 ? '' : 's'}`
        : `${view.length} of ${state.notes.length}`;
    }
  }

  if (bulkDelete) bulkDelete.hidden = state.selected.size === 0;

  // Update sort indicators.
  document.querySelectorAll('th.sortable').forEach((th) => {
    const key = th.dataset.sortKey;
    const arrow = th.querySelector('.hub-sort-arrow');
    if (!arrow) return;
    if (key !== state.sortKey) {
      arrow.textContent = '';
      th.setAttribute('aria-sort', 'none');
      return;
    }
    arrow.textContent = state.sortDirection === 'asc' ? '▲' : '▼';
    th.setAttribute('aria-sort', state.sortDirection === 'asc' ? 'ascending' : 'descending');
  });
}

function readNotesOpenBehavior() {
  try {
    const v = localStorage.getItem('note.openBehavior');
    if (v === 'modal' || v === 'page' || v === 'page-new-tab') return v;
  } catch (_) {}
  return 'modal';
}

function navigateToNote(noteId, forcePage = false) {
  if (!noteId) return;
  const behavior = forcePage ? 'page' : readNotesOpenBehavior();
  if (behavior === 'page') {
    window.location.href = `/notes/${encodeURIComponent(noteId)}`;
  } else if (behavior === 'page-new-tab') {
    window.open(`/notes/${encodeURIComponent(noteId)}`, '_blank', 'noopener');
  } else {
    // Modal preference + we're on a hub page (no sessionManager modal here).
    // Navigate to the workspace page with ?open=<id> so the modal opens there.
    window.location.href = `/workspaces/${encodeURIComponent(WORKSPACE_ID)}?open=${encodeURIComponent(noteId)}`;
  }
}

async function createNewNote() {
  try {
    const resp = await fetch(`/api/workspaces/${encodeURIComponent(WORKSPACE_ID)}/notes`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ name: 'Untitled', content: '' }),
    });
    if (!resp.ok) {
      showToast('Failed to create note', 'error');
      return;
    }
    const data = await resp.json();
    const note = data?.note || data;
    if (note?.id) navigateToNote(note.id);
  } catch (err) {
    console.error('Create note failed', err);
    showToast('Failed to create note', 'error');
  }
}

async function bulkDelete() {
  if (state.selected.size === 0) return;
  const count = state.selected.size;
  if (!window.confirm(`Delete ${count} note${count === 1 ? '' : 's'}? This cannot be undone.`)) return;

  const ids = Array.from(state.selected);
  const results = await Promise.allSettled(ids.map((id) =>
    fetch(`/api/notes/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  ));

  const failed = results.filter((r) => r.status === 'rejected' || !r.value?.ok).length;
  if (failed > 0) showToast(`Failed to delete ${failed} note${failed === 1 ? '' : 's'}`, 'error');
  else showToast(`Deleted ${count} note${count === 1 ? '' : 's'}`, 'success');

  state.selected.clear();
  state.notes = state.notes.filter((n) => !ids.includes(n.id));
  paint();
}

function bindEvents() {
  document.getElementById('hubSearchInput')?.addEventListener('input', (e) => {
    state.filter = e.target.value;
    paint();
  });

  document.querySelectorAll('th.sortable').forEach((th) => {
    th.addEventListener('click', () => {
      const key = th.dataset.sortKey;
      if (!key) return;
      if (state.sortKey === key) {
        state.sortDirection = state.sortDirection === 'asc' ? 'desc' : 'asc';
      } else {
        state.sortKey = key;
        state.sortDirection = key === 'name' ? 'asc' : 'desc';
      }
      paint();
    });
  });

  document.getElementById('hubNewNoteBtn')?.addEventListener('click', createNewNote);
  document.getElementById('hubBulkDeleteBtn')?.addEventListener('click', bulkDelete);

  document.getElementById('hubSelectAll')?.addEventListener('change', (e) => {
    if (e.target.checked) {
      getView().forEach((n) => state.selected.add(n.id));
    } else {
      state.selected.clear();
    }
    paint();
  });

  // Delegated click handlers for the table body.
  document.getElementById('hubTableBody')?.addEventListener('click', (e) => {
    const cb = e.target.closest('.hub-row-checkbox');
    if (cb) {
      const id = cb.dataset.noteId;
      if (cb.checked) state.selected.add(id);
      else state.selected.delete(id);
      paint();
      return;
    }
    const titleLink = e.target.closest('.workspace-notes-hub-title-link');
    if (titleLink) {
      e.preventDefault();
      navigateToNote(titleLink.dataset.noteId);
      return;
    }
    const action = e.target.closest('.hub-row-action');
    if (action) {
      e.preventDefault();
      if (action.dataset.action === 'open-page') navigateToNote(action.dataset.noteId, true);
    }
  });
}

async function bootstrap() {
  if (!WORKSPACE_ID) {
    showToast('No workspace ID in URL', 'error');
    return;
  }
  bindEvents();

  // Breadcrumb workspace name (best effort).
  fetchWorkspace().then((ws) => {
    const link = document.getElementById('hubWorkspaceLink');
    if (link) {
      link.href = `/workspaces/${encodeURIComponent(WORKSPACE_ID)}`;
      link.textContent = ws?.name || ws?.workspace?.name || 'Workspace';
    }
    if (ws?.name) document.title = `${ws.name} Notes - Ori Agent`;
  });

  try {
    state.notes = await fetchNotes();
  } catch (err) {
    console.error('Failed to load workspace notes', err);
    showToast('Failed to load notes', 'error');
    state.notes = [];
  }
  paint();
}

if (typeof document !== 'undefined') {
  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', bootstrap);
  } else {
    bootstrap();
  }
}
