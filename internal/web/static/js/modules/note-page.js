// note-page.js — bootstrap for the dedicated /notes/<id> page.
//
// Loads the note via the API, populates the textarea + title, and calls
// NoteEditor.mount with a host adapter for the page surface. As of task 4.0
// (slice B), the page also owns multi-tab state via window.NoteTabs — the
// tab strip above the editor shows all open notes in the active pane. The
// editor is a single instance whose content is swapped when the active tab
// changes (no remount, so autosave/history controllers persist across
// tab switches).

const STATE_KEY = 'note.tabs';

function readNoteIdFromPath() {
  if (typeof window === 'undefined') return '';
  const parts = window.location.pathname.split('/').filter(Boolean);
  const idx = parts.indexOf('notes');
  if (idx < 0 || idx + 1 >= parts.length) return '';
  return decodeURIComponent(parts[idx + 1] || '');
}

const NOTE_ID = readNoteIdFromPath();
let currentNote = null;          // the note shown in the active pane
let bundle = null;                // NoteEditor.mount return value (single instance)
let state = null;                 // NoteTabs reducer state
let switching = false;            // re-entrancy guard while swapping content

// =============================================================================
// State persistence (localStorage)
// =============================================================================

function loadSavedState(fallbackNoteId) {
  if (!window.NoteTabs) return null;
  try {
    const raw = localStorage.getItem(STATE_KEY);
    if (!raw) return window.NoteTabs.initialState(fallbackNoteId);
    const parsed = JSON.parse(raw);
    return window.NoteTabs.hydrate(parsed, fallbackNoteId);
  } catch (_) {
    return window.NoteTabs.initialState(fallbackNoteId);
  }
}

function persistState() {
  try { localStorage.setItem(STATE_KEY, JSON.stringify(state)); } catch (_) {}
}

// =============================================================================
// API
// =============================================================================

async function fetchNote(noteId) {
  try {
    const resp = await fetch(`/api/notes/${encodeURIComponent(noteId)}`);
    if (!resp.ok) {
      console.error('Note fetch failed:', resp.status, resp.statusText);
      return null;
    }
    return await resp.json();
  } catch (err) {
    console.error('Note fetch errored:', err);
    return null;
  }
}

async function fetchWorkspaceName(workspaceId) {
  if (!workspaceId) return null;
  try {
    const resp = await fetch(`/api/workspaces/${encodeURIComponent(workspaceId)}`);
    if (!resp.ok) return null;
    const data = await resp.json();
    return data?.name || data?.workspace?.name || null;
  } catch (_) { return null; }
}

// =============================================================================
// DOM helpers
// =============================================================================

function showLoadError(message) {
  const previewContent = document.getElementById('notePreviewContent');
  if (previewContent) {
    previewContent.innerHTML = `<div class="note-page-error">${
      String(message || 'Could not load this note.')
        .replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
    }</div>`;
  }
}

function populateBreadcrumb(note, workspaceName) {
  const link = document.getElementById('notePageWorkspaceLink');
  if (link && note?.workspace_id) {
    link.href = `/workspaces/${encodeURIComponent(note.workspace_id)}`;
    link.textContent = workspaceName || 'Workspace';
  }
}

function showToast(msg, kind) {
  if (typeof window?.showToast === 'function') window.showToast(msg, kind);
  else console.log(`[${kind || 'info'}] ${msg}`);
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

function readPageSelection() {
  if (typeof document === 'undefined' || typeof window === 'undefined') return null;
  if (!window.NoteEditor) return null;
  return window.NoteEditor.readSelection({
    getContent: () => document.getElementById('noteContentInput')?.value || '',
    isPreviewMode: () => true,
  });
}

async function savePageNote() {
  if (!currentNote?.id) return false;
  // Mid-switch saves would write the new content under the old note's ID.
  // Block until the swap completes.
  if (switching) return false;
  const name = document.getElementById('noteNameInput')?.value?.trim() || 'Untitled Note';
  const content = document.getElementById('noteContentInput')?.value || '';
  try {
    const resp = await fetch(`/api/notes/${encodeURIComponent(currentNote.id)}`, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ name, content }),
    });
    if (!resp.ok) return false;
    const data = await resp.json();
    if (data?.note) currentNote = { ...currentNote, ...data.note };
    window.NoteBacklinks?.announceNoteSaved?.(currentNote.id, content.includes('[['));
    // Refresh the tab label in case the title changed.
    renderTabStrip();
    return true;
  } catch (_) { return false; }
}

// =============================================================================
// Tab strip rendering
// =============================================================================

const _tabLabelCache = new Map(); // noteId → resolved title (best effort cache)

function tabLabel(noteId) {
  if (currentNote?.id === noteId) return currentNote.name || 'Untitled';
  return _tabLabelCache.get(noteId) || 'Loading…';
}

async function prefetchTabLabels() {
  if (!state) return;
  const ids = window.NoteTabs.allOpenNoteIds(state);
  for (const id of ids) {
    if (_tabLabelCache.has(id) || id === currentNote?.id) continue;
    fetchNote(id).then((n) => {
      if (n?.name) {
        _tabLabelCache.set(n.id, n.name);
        renderTabStrip();
      }
    });
  }
}

function renderTabStrip() {
  const strip = document.getElementById('notePageTabStrip');
  const list = document.getElementById('notePageTabList');
  const splitBtn = document.getElementById('notePageSplitRightBtn');
  const unsplitBtn = document.getElementById('notePageUnsplitBtn');
  if (!strip || !list || !state) return;

  // Always show the strip: the "+" button is the entry point to opening
  // additional notes in tabs, so it needs to be reachable even when the
  // current page has just one open tab.
  strip.hidden = false;

  // Split button visibility — only meaningful when not already split.
  if (splitBtn) splitBtn.hidden = state.splitMode !== 'none' || (state.panes[0]?.tabs?.length ?? 0) === 0;
  if (unsplitBtn) unsplitBtn.hidden = state.splitMode === 'none';

  // The primary tab strip shows pane 0's tabs (the editable pane).
  const pane = state.panes[0];
  if (!pane) { list.innerHTML = ''; return; }

  list.innerHTML = pane.tabs.map((id) => {
    const isActive = id === pane.activeId;
    const label = escapeText(tabLabel(id));
    return `<li class="note-tab${isActive ? ' is-active' : ''}" data-note-id="${escapeAttr(id)}" role="presentation">
      <button type="button" class="note-tab-button" role="tab" aria-selected="${isActive}" title="${label}">
        <span class="note-tab-label">${label}</span>
      </button>
      <button type="button" class="note-tab-close" data-note-id="${escapeAttr(id)}" aria-label="Close tab" title="Close">×</button>
    </li>`;
  }).join('');
}

// renderSecondaryPane paints the second pane's tab strip + rendered
// markdown preview when split mode is on. The secondary pane is read-only
// in this slice — clicking a tab switches its preview content.
async function renderSecondaryPane() {
  const aside = document.getElementById('notePageSecondaryPane');
  const tabsEl = document.getElementById('notePageSecondaryTabs');
  const contentEl = document.getElementById('notePageSecondaryContent');
  const grid = document.getElementById('notePagePaneGrid');
  if (!aside || !tabsEl || !contentEl || !grid || !state) return;

  if (state.splitMode === 'none' || !state.panes[1]) {
    aside.hidden = true;
    grid.classList.remove('is-split');
    return;
  }

  aside.hidden = false;
  grid.classList.add('is-split');

  const pane = state.panes[1];
  tabsEl.innerHTML = pane.tabs.map((id) => {
    const isActive = id === pane.activeId;
    const label = escapeText(tabLabel(id));
    return `<button type="button" class="note-tab${isActive ? ' is-active' : ''}" data-note-id="${escapeAttr(id)}" data-pane="1" title="${label}">
      <span class="note-tab-label">${label}</span>
      <span class="note-tab-close-inline" data-action="close" data-note-id="${escapeAttr(id)}" title="Close">×</span>
    </button>`;
  }).join('');

  if (!pane.activeId) {
    contentEl.innerHTML = '<div class="note-page-secondary-empty">No note selected.</div>';
    return;
  }

  // Fetch + render the active note's markdown into the preview area. Use
  // marked + DOMPurify when available; fall back to escaped text otherwise.
  const note = currentNote?.id === pane.activeId ? currentNote : await fetchNote(pane.activeId);
  if (!note) {
    contentEl.innerHTML = '<div class="note-page-secondary-empty">Could not load this note.</div>';
    return;
  }
  if (note.id !== currentNote?.id) _tabLabelCache.set(note.id, note.name || 'Untitled');

  const md = String(note.content || '');
  let html = '';
  if (typeof window.marked?.parse === 'function') {
    try { html = window.marked.parse(md); } catch (_) { html = escapeText(md); }
  } else {
    html = `<pre>${escapeText(md)}</pre>`;
  }
  if (typeof window.DOMPurify?.sanitize === 'function') {
    html = window.DOMPurify.sanitize(html);
  }
  // Post-process wikilinks so they render as clickable anchors.
  if (typeof window.NoteWikilinks?.applyWikilinksToHtml === 'function') {
    html = window.NoteWikilinks.applyWikilinksToHtml(html, () => 'pending');
  }
  const title = escapeText(note.name || 'Untitled');
  contentEl.innerHTML = `<h2 class="note-page-secondary-title">${title}</h2>${html}`;
}

// =============================================================================
// Editor content swap
// =============================================================================

async function loadNoteIntoActivePane(noteId) {
  if (!noteId) return false;
  switching = true;

  // 1. Save current note first so the swap doesn't lose unsaved edits.
  try { await bundle?.autosave?.flushImmediate?.(); } catch (_) {}

  // 2. Release presence on the outgoing note, fetch the new one.
  if (currentNote?.id && currentNote.id !== noteId) {
    window.NotePresence?.releaseOpenNote(currentNote.id);
  }
  const next = await fetchNote(noteId);
  if (!next) {
    switching = false;
    showLoadError(`Note not found (id: ${noteId}).`);
    return false;
  }

  // 3. Swap inputs + caches + history.
  const titleInput = document.getElementById('noteNameInput');
  const contentInput = document.getElementById('noteContentInput');
  if (titleInput) titleInput.value = next.name || '';
  if (contentInput) contentInput.value = next.content || '';
  if (next.name) document.title = `${next.name} - Ori Agent`;
  _tabLabelCache.set(next.id, next.name || 'Untitled');

  currentNote = next;

  // 4. Reset history so undo doesn't cross note boundaries.
  bundle?.history?.reset?.();
  bundle?.autosave?.markClean?.();

  // 5. Update URL so refresh / share / browser back work intuitively.
  if (window.location.pathname !== `/notes/${encodeURIComponent(next.id)}`) {
    const url = `/notes/${encodeURIComponent(next.id)}` + window.location.hash;
    window.history.pushState(null, '', url);
  }

  // 6. Re-render preview, refresh side panels.
  bundle?.render?.();
  window.NoteBacklinks?.loadBacklinksFor(next.id);
  window.NoteRailNotes?.setActiveNoteId(next.id);
  window.NotePresence?.claimOpenNote(next.id, 'page');

  switching = false;
  return true;
}

// =============================================================================
// Action wrappers (mutate state + persist + re-render)
// =============================================================================

async function openInTab(noteId) {
  if (!noteId || !window.NoteTabs) return;
  // openTab targets pane 0 (the editor pane) explicitly — splitting routes
  // the editor's main flow through pane 0, with pane 1 being read-only.
  state = window.NoteTabs.openTab(state, noteId, 0);
  persistState();
  renderTabStrip();
  renderSecondaryPane();
  prefetchTabLabels();
  if (currentNote?.id !== noteId) {
    await loadNoteIntoActivePane(noteId);
  }
}

async function switchToTab(noteId, paneIndex) {
  if (!window.NoteTabs) return;
  const next = window.NoteTabs.setActiveTab(state, paneIndex, noteId);
  if (next === state) return;
  state = next;
  persistState();
  renderTabStrip();
  if (paneIndex === 0 && currentNote?.id !== noteId) {
    await loadNoteIntoActivePane(noteId);
  } else if (paneIndex === 1) {
    // Just re-render the secondary preview; editor stays on pane 0.
    await renderSecondaryPane();
  }
}

async function closeTab(noteId, paneIndex) {
  if (!window.NoteTabs) return;
  const before = state;
  state = window.NoteTabs.closeTab(state, noteId, paneIndex);
  if (state === before) return;
  persistState();
  renderTabStrip();
  await renderSecondaryPane();
  // If the user closed every tab in pane 0, navigate away. The reducer
  // may also have collapsed split mode if pane 1 emptied out.
  const pane0 = state.panes[0];
  if (!pane0?.activeId) {
    const wsId = currentNote?.workspace_id;
    window.NotePresence?.releaseOpenNote(currentNote?.id);
    window.location.href = wsId ? `/workspaces/${encodeURIComponent(wsId)}/notes` : '/workspaces';
    return;
  }
  // If the editor pane's active note changed, load it.
  if (paneIndex === 0 && pane0.activeId !== currentNote?.id) {
    await loadNoteIntoActivePane(pane0.activeId);
  }
}

async function splitRight() {
  if (!window.NoteTabs || !currentNote?.id) return;
  // Clone the editor pane's active note into a new pane on the right.
  state = window.NoteTabs.splitRight(state);
  // Keep focus on the editor pane (slice C constraint: pane 0 is the only
  // editable pane). The reducer would otherwise focus the new right pane.
  state = window.NoteTabs.focusPane(state, 0);
  persistState();
  renderTabStrip();
  await renderSecondaryPane();
}

async function unsplit() {
  if (!window.NoteTabs) return;
  state = window.NoteTabs.unsplit(state);
  persistState();
  renderTabStrip();
  await renderSecondaryPane();
}

// =============================================================================
// Public surface for other modules (e.g. note-wikilinks → open as tab)
// =============================================================================

function bindPublicAPI() {
  if (typeof window === 'undefined') return;
  window.NotePage = {
    openNoteInTab: openInTab,
    getActiveNoteId: () => currentNote?.id || null,
  };
}

// =============================================================================
// Bootstrap
// =============================================================================

async function bootstrap() {
  if (!NOTE_ID) {
    showLoadError('No note ID in URL.');
    showToast('No note ID in URL', 'error');
    return;
  }
  if (!window.NoteEditor) {
    showLoadError('NoteEditor module failed to load.');
    console.error('NoteEditor module not loaded');
    return;
  }

  // 1. Initialize tab state. If saved state matches the URL note, restore it;
  //    otherwise start fresh with the URL note as the single tab.
  if (window.NoteTabs) {
    state = loadSavedState(NOTE_ID);
    // Make sure the URL's note is open + active in the focused pane.
    if (state) {
      const pane = state.panes[state.focusedPaneIndex];
      if (!pane?.tabs?.includes(NOTE_ID)) {
        state = window.NoteTabs.openTab(state, NOTE_ID, state.focusedPaneIndex);
      } else if (pane.activeId !== NOTE_ID) {
        state = window.NoteTabs.setActiveTab(state, state.focusedPaneIndex, NOTE_ID);
      }
    } else {
      state = window.NoteTabs.initialState(NOTE_ID);
    }
    persistState();
  } else {
    state = { panes: [{ activeId: NOTE_ID, tabs: [NOTE_ID] }], splitMode: 'none', focusedPaneIndex: 0 };
  }

  // 2. Load the initial note.
  currentNote = await fetchNote(NOTE_ID);
  if (!currentNote) {
    showLoadError(`Note not found (id: ${NOTE_ID}). The note may have been deleted or you may not have access.`);
    showToast('Note not found', 'error');
    return;
  }
  _tabLabelCache.set(currentNote.id, currentNote.name || 'Untitled');

  // 3. Populate the editor inputs + document title.
  const titleInput = document.getElementById('noteNameInput');
  const contentInput = document.getElementById('noteContentInput');
  if (titleInput) titleInput.value = currentNote.name || '';
  if (contentInput) contentInput.value = currentNote.content || '';
  if (currentNote.name) document.title = `${currentNote.name} - Ori Agent`;

  // 4. Breadcrumb workspace name (best effort).
  fetchWorkspaceName(currentNote.workspace_id).then((name) => populateBreadcrumb(currentNote, name));

  // 5. Cross-module hooks (wikilinks, backlinks, rail, presence).
  window.NoteWikilinks?.setWorkspaceContext(() => currentNote?.workspace_id || null);
  window.NoteBacklinks?.loadBacklinksFor(currentNote.id);
  window.NoteRailNotes?.initRail({
    workspaceIdResolver: () => currentNote?.workspace_id || null,
    activeNoteId: currentNote.id,
  });
  window.NotePresence?.claimOpenNote(currentNote.id, 'page');
  window.addEventListener('beforeunload', () => {
    if (currentNote?.id) window.NotePresence?.releaseOpenNote(currentNote.id);
  });

  // 6. Mount the editor.
  bundle = window.NoteEditor.mount({
    getContent: () => contentInput?.value || '',
    setContent: (v) => { if (contentInput) contentInput.value = String(v || ''); },
    getContentLines: () => {
      const value = contentInput?.value || '';
      return value.length > 0 ? value.split('\n') : [''];
    },
    setContentLines: (lines) => {
      if (contentInput) contentInput.value = (lines || []).join('\n');
    },
    isPreviewMode: () => true,
    onAutosaveFlush: savePageNote,
    aiAssist: {
      readSelection: readPageSelection,
      showToast,
    },
  });
  bundle.render();
  titleInput?.addEventListener('input', () => bundle.autosave.schedule());

  // 7. Render the tab strip + secondary pane (if split state was restored)
  //    + prefetch labels for non-current tabs.
  renderTabStrip();
  renderSecondaryPane();
  prefetchTabLabels();

  // 8. Delegated handlers for the primary tab strip (pane 0).
  document.getElementById('notePageTabList')?.addEventListener('click', (e) => {
    const closeBtn = e.target.closest('.note-tab-close');
    if (closeBtn) {
      e.preventDefault();
      e.stopPropagation();
      closeTab(closeBtn.dataset.noteId, 0);
      return;
    }
    const tab = e.target.closest('.note-tab');
    if (tab) {
      e.preventDefault();
      switchToTab(tab.dataset.noteId, 0);
    }
  });

  // Delegated handlers for the secondary pane (pane 1).
  document.getElementById('notePageSecondaryTabs')?.addEventListener('click', (e) => {
    const closeAction = e.target.closest('[data-action="close"]');
    if (closeAction) {
      e.preventDefault();
      e.stopPropagation();
      closeTab(closeAction.dataset.noteId, 1);
      return;
    }
    const tab = e.target.closest('.note-tab');
    if (tab) {
      e.preventDefault();
      switchToTab(tab.dataset.noteId, 1);
    }
  });

  // Wire split-right / unsplit buttons.
  document.getElementById('notePageSplitRightBtn')?.addEventListener('click', splitRight);
  document.getElementById('notePageUnsplitBtn')?.addEventListener('click', unsplit);

  // The "+" button opens the global ⌘K search palette so the user can pick
  // any note in any workspace; palette selection then routes through
  // window.NotePage.openNoteInTab (set up in bindPublicAPI below).
  document.getElementById('notePageNewTabBtn')?.addEventListener('click', () => {
    window.SearchPalette?.open?.();
  });

  // 9. Hash-anchor scroll.
  if (location.hash) {
    const headingText = decodeURIComponent(location.hash.slice(1));
    requestAnimationFrame(() => {
      const source = contentInput?.value || '';
      const lines = source.split('\n');
      let cursor = 0;
      for (const line of lines) {
        if (/^#{1,6}\s+/.test(line) && line.includes(headingText)) {
          window.NoteEditor.scrollToHeadingPosition(source, cursor);
          break;
        }
        cursor += line.length + 1;
      }
    });
  }

  // 10. "Open in modal" button.
  document.getElementById('noteOpenInModal')?.addEventListener('click', async () => {
    try { await bundle?.autosave?.flushImmediate?.(); } catch (_) {}
    if (currentNote?.workspace_id && currentNote.id) {
      const wsId = encodeURIComponent(currentNote.workspace_id);
      const noteId = encodeURIComponent(currentNote.id);
      location.href = `/workspaces/${wsId}?open=${noteId}`;
    }
  });

  // 11. Expose the public API for other modules (wikilinks → open in tab).
  bindPublicAPI();
}

if (typeof document !== 'undefined') {
  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', bootstrap);
  } else {
    bootstrap();
  }
}
