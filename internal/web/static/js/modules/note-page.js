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
let pendingGenerateDraft = null;   // last whole-note AI draft waiting to apply
let creatingNoteFromTabStrip = false;

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

export async function createWorkspaceNote(workspaceId, fetchImpl = fetch) {
  if (!workspaceId) throw new Error('No workspace selected');
  const resp = await fetchImpl(`/api/workspaces/${encodeURIComponent(workspaceId)}/notes`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ name: 'Untitled', content: '' }),
  });
  if (!resp.ok) {
    throw new Error(`Failed to create note: ${resp.status}`);
  }
  const data = await resp.json();
  return data?.note || data;
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

function setGenerateError(message) {
  window.NoteEditor?.setGenerateError?.(message || '');
}

function setGenerateBusy(isBusy) {
  window.NoteEditor?.setGenerateBusy?.(isBusy);
}

async function generatePageNoteWithAI() {
  const promptInput = document.getElementById('noteAIPromptInput');
  const prompt = promptInput?.value?.trim() || '';
  const agentId = window.NoteEditor?.getSelectedAgentId?.() || '';
  const workspaceId = currentNote?.workspace_id || currentNote?.folder_id || '';

  if (!prompt) {
    setGenerateError('Please enter a prompt describing what you want the note to contain.');
    return;
  }

  setGenerateError('');
  window.NoteEditor?.setGenerateStatus?.('');
  window.NoteEditor?.clearGenerateDraft?.();
  setGenerateBusy(true);

  try {
    const response = await fetch('/api/notes/generate', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        prompt,
        workspace_id: workspaceId,
        agent_id: agentId,
      }),
    });

    if (!response.ok) {
      let payload = null;
      try { payload = await response.json(); } catch (_) {}
      throw new Error(payload?.error || 'Failed to generate note');
    }

    const result = await response.json();
    pendingGenerateDraft = {
      title: result.title || '',
      content: result.content || '',
    };
    window.NoteEditor?.setGenerateDraft?.(pendingGenerateDraft);
    showToast('AI draft generated', 'success');
  } catch (error) {
    console.error('Note AI generation failed:', error);
    setGenerateError(error?.message || 'Failed to generate note content. Please try again.');
  } finally {
    setGenerateBusy(false);
  }
}

function applyPageGenerateDraft(mode = 'replace') {
  const draft = pendingGenerateDraft;
  if (!draft?.content) {
    setGenerateError('Generate a draft before applying it.');
    return;
  }

  const titleInput = document.getElementById('noteNameInput');
  const contentInput = document.getElementById('noteContentInput');
  if (!contentInput) return;

  const currentContent = contentInput.value || '';
  const draftContent = String(draft.content || '').trim();
  const draftTitle = String(draft.title || '').trim();
  let nextContent = draftContent;

  bundle?.history?.push?.(currentContent);

  if (mode === 'append') {
    nextContent = currentContent.trim()
      ? `${currentContent.replace(/\s+$/, '')}\n\n${draftContent}`
      : draftContent;
  } else if (mode === 'insert') {
    const start = Number.isInteger(contentInput.selectionStart) ? contentInput.selectionStart : currentContent.length;
    const end = Number.isInteger(contentInput.selectionEnd) ? contentInput.selectionEnd : start;
    nextContent = `${currentContent.slice(0, start)}${draftContent}${currentContent.slice(end)}`;
    const cursor = start + draftContent.length;
    const restoreCursor = () => {
      try {
        contentInput.focus();
        contentInput.setSelectionRange(cursor, cursor);
      } catch (_) {}
    };
    if (typeof requestAnimationFrame === 'function') requestAnimationFrame(restoreCursor);
    else restoreCursor();
  } else if (draftTitle && titleInput) {
    titleInput.value = draftTitle;
  }

  if (mode !== 'replace' && draftTitle && titleInput && !titleInput.value.trim()) {
    titleInput.value = draftTitle;
  }
  if (titleInput?.value?.trim() && currentNote) {
    const name = titleInput.value.trim();
    currentNote = { ...currentNote, name };
    document.title = `${name} - Ori Agent`;
    _tabLabelCache.set(currentNote.id, name);
    renderTabStrip();
  }

  contentInput.value = nextContent;
  bundle?.render?.();
  bundle?.toc?.scheduleRebuild?.();
  bundle?.autosave?.schedule?.();
  pendingGenerateDraft = null;
  window.NoteEditor?.closeGeneratePanel?.();
  showToast('AI draft applied', 'success');
}

function bindGenerateWithAIControls() {
  document.getElementById('noteGenerateAIToggle')?.addEventListener('click', () => {
    window.NoteEditor?.toggleGeneratePanel?.();
  });
  document.getElementById('noteAIGenerateBtn')?.addEventListener('click', generatePageNoteWithAI);
  document.getElementById('noteAICancelBtn')?.addEventListener('click', () => {
    pendingGenerateDraft = null;
    window.NoteEditor?.closeGeneratePanel?.();
  });
  document.getElementById('noteAIReplaceBtn')?.addEventListener('click', () => applyPageGenerateDraft('replace'));
  document.getElementById('noteAIAppendBtn')?.addEventListener('click', () => applyPageGenerateDraft('append'));
  document.getElementById('noteAIInsertBtn')?.addEventListener('click', () => applyPageGenerateDraft('insert'));
}

// =============================================================================
// Tab strip rendering
// =============================================================================

const _tabLabelCache = new Map(); // noteId → resolved title (best effort cache)

function tabLabel(noteId) {
  if (currentNote?.id === noteId) return currentNote.name || 'Untitled';
  return _tabLabelCache.get(noteId) || 'Loading…';
}

function refreshSecondaryTabLabels() {
  if (!state?.panes?.[1]) return;
  const tabsEl = document.getElementById('notePageSecondaryTabs');
  if (!tabsEl) return;
  tabsEl.querySelectorAll('[data-note-id]').forEach((tab) => {
    const id = tab.dataset.noteId;
    if (!id) return;
    const label = tabLabel(id);
    tab.title = label;
    const labelEl = tab.querySelector('.note-tab-label');
    if (labelEl) labelEl.textContent = label;
  });
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
        refreshSecondaryTabLabels();
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

  list.innerHTML = pane.tabs.map((id, i) => {
    const isActive = id === pane.activeId;
    const label = escapeText(tabLabel(id));
    return `<li class="note-tab${isActive ? ' is-active' : ''}" data-note-id="${escapeAttr(id)}" data-pane="0" data-index="${i}" role="presentation" draggable="true">
      <button type="button" class="note-tab-button" role="tab" aria-selected="${isActive}" title="${label}">
        <span class="note-tab-label">${label}</span>
      </button>
      <button type="button" class="note-tab-close" data-note-id="${escapeAttr(id)}" aria-label="Close tab" title="Close">×</button>
    </li>`;
  }).join('');
}

// Secondary pane's local state. Its note is independent of pane 0's currentNote.
let secondaryNote = null;       // the full note object loaded in pane 1
let secondaryDirty = false;     // unsaved-changes flag for the secondary editor
let secondarySaveTimer = null;  // debounce handle for the secondary autosave

function setSecondaryStatus(text) {
  const el = document.getElementById('notePageSecondaryStatus');
  if (el) el.textContent = text || '';
}

// scheduleSecondarySave debounces secondary-pane PATCHes by 800ms. Calls
// announceNoteSaved on success so other tabs (and the primary pane, if it
// somehow has the same note open) can reconcile.
function scheduleSecondarySave() {
  secondaryDirty = true;
  setSecondaryStatus('Unsaved');
  if (secondarySaveTimer) clearTimeout(secondarySaveTimer);
  secondarySaveTimer = setTimeout(() => { secondarySaveTimer = null; flushSecondarySave(); }, 800);
}

async function flushSecondarySave() {
  if (!secondaryNote?.id || !secondaryDirty) return;
  const textarea = document.getElementById('notePageSecondaryEditor');
  if (!textarea) return;
  const content = textarea.value;
  setSecondaryStatus('Saving…');
  try {
    const resp = await fetch(`/api/notes/${encodeURIComponent(secondaryNote.id)}`, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ name: secondaryNote.name || 'Untitled Note', content }),
    });
    if (!resp.ok) { setSecondaryStatus('Save failed'); return; }
    const data = await resp.json();
    if (data?.note) {
      secondaryNote = { ...secondaryNote, ...data.note };
      _tabLabelCache.set(secondaryNote.id, secondaryNote.name || 'Untitled');
      refreshSecondaryTabLabels();
    }
    secondaryDirty = false;
    setSecondaryStatus('Saved');
    setTimeout(() => { if (!secondaryDirty) setSecondaryStatus(''); }, 1200);
    window.NoteBacklinks?.announceNoteSaved?.(secondaryNote.id, content.includes('[['));
    // If the primary editor has the SAME note open, refresh its content so
    // the user doesn't lose pane 1's edits the next time pane 0 autosaves.
    if (currentNote?.id === secondaryNote.id) {
      const titleInput = document.getElementById('noteNameInput');
      const contentInput = document.getElementById('noteContentInput');
      if (titleInput && secondaryNote.name) titleInput.value = secondaryNote.name;
      if (contentInput) contentInput.value = content;
      currentNote = { ...currentNote, content };
      bundle?.history?.reset?.();
      bundle?.autosave?.markClean?.();
      bundle?.render?.();
    }
  } catch (_) {
    setSecondaryStatus('Save failed');
  }
}

// renderSecondaryPane paints the secondary pane's tab strip + Markdown
// textarea when split mode is on. The secondary is editable — typing in the
// textarea debounces an autosave PATCH. When pane 1's active note matches
// pane 0's, the textarea is read-only and a banner explains why.
async function renderSecondaryPane() {
  const aside = document.getElementById('notePageSecondaryPane');
  const tabsEl = document.getElementById('notePageSecondaryTabs');
  const grid = document.getElementById('notePagePaneGrid');
  const textarea = document.getElementById('notePageSecondaryEditor');
  const banner = document.getElementById('notePageSecondaryBanner');
  const emptyEl = document.getElementById('notePageSecondaryEmpty');
  if (!aside || !tabsEl || !grid || !textarea || !banner || !emptyEl || !state) return;

  if (state.splitMode === 'none' || !state.panes[1]) {
    // Flush any pending save before tearing down.
    if (secondaryDirty) await flushSecondarySave();
    aside.hidden = true;
    grid.classList.remove('is-split');
    secondaryNote = null;
    return;
  }

  aside.hidden = false;
  grid.classList.add('is-split');

  const pane = state.panes[1];
  tabsEl.innerHTML = pane.tabs.map((id, i) => {
    const isActive = id === pane.activeId;
    const label = escapeText(tabLabel(id));
    return `<button type="button" class="note-tab${isActive ? ' is-active' : ''}" data-note-id="${escapeAttr(id)}" data-pane="1" data-index="${i}" title="${label}" draggable="true">
      <span class="note-tab-label">${label}</span>
      <span class="note-tab-close-inline" data-action="close" data-note-id="${escapeAttr(id)}" title="Close">×</span>
    </button>`;
  }).join('');

  if (!pane.activeId) {
    textarea.hidden = true;
    banner.hidden = true;
    emptyEl.hidden = false;
    secondaryNote = null;
    setSecondaryStatus('');
    return;
  }

  // Flush previous secondary save before loading a different note.
  if (secondaryNote && secondaryNote.id !== pane.activeId && secondaryDirty) {
    await flushSecondarySave();
  }

  const note = await fetchNote(pane.activeId);
  if (!note) {
    textarea.hidden = true;
    banner.hidden = true;
    emptyEl.hidden = false;
    emptyEl.textContent = 'Could not load this note.';
    secondaryNote = null;
    return;
  }
  _tabLabelCache.set(note.id, note.name || 'Untitled');
  refreshSecondaryTabLabels();
  secondaryNote = note;
  secondaryDirty = false;
  emptyEl.hidden = true;
  textarea.value = note.content || '';
  setSecondaryStatus('');

  // If the same note is in pane 0's editor, lock the secondary to avoid
  // diverging edits. Otherwise it's freely editable.
  const sameAsPrimary = currentNote?.id === note.id;
  textarea.readOnly = sameAsPrimary;
  banner.hidden = !sameAsPrimary;
  textarea.hidden = false;
}

// =============================================================================
// Editor content swap
// =============================================================================

// scrollToHashHeading: if location.hash is non-empty, scroll the editor to
// the matching heading. Shared by the initial bootstrap and tab swaps so
// palette → tab transitions can still anchor to a heading.
function scrollToHashHeading() {
  if (!location.hash || !window.NoteEditor) return;
  const headingText = decodeURIComponent(location.hash.slice(1));
  if (!headingText) return;
  const contentInput = document.getElementById('noteContentInput');
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

async function loadNoteIntoActivePane(noteId) {
  if (!noteId) return false;

  // 1. Save current note first so the swap doesn't lose unsaved edits.
  const saved = await bundle?.autosave?.flushImmediate?.();
  if (saved === false) {
    showToast('Save failed. Retry before switching notes.', 'error');
    return false;
  }
  pendingGenerateDraft = null;
  window.NoteEditor?.closeGeneratePanel?.();

  switching = true;

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

  // 7. If the URL carries a heading hash (e.g., palette → tab), scroll to it.
  scrollToHashHeading();

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

async function createNoteFromTabStrip() {
  if (creatingNoteFromTabStrip) return;
  const workspaceId = currentNote?.workspace_id || currentNote?.folder_id || '';
  if (!workspaceId) {
    showToast('Cannot create a note without a workspace.', 'error');
    return;
  }

  const saved = await bundle?.autosave?.flushImmediate?.();
  if (saved === false) {
    showToast('Save failed. Retry before creating another note.', 'error');
    return;
  }

  const btn = document.getElementById('notePageNewTabBtn');
  creatingNoteFromTabStrip = true;
  if (btn) {
    btn.disabled = true;
    btn.setAttribute('aria-busy', 'true');
    btn.title = 'Creating note...';
  }

  try {
    const note = await createWorkspaceNote(workspaceId);
    if (!note?.id) throw new Error('Create note response did not include an id');
    _tabLabelCache.set(note.id, note.name || 'Untitled');
    window.NoteRailNotes?.invalidate?.();
    await openInTab(note.id);
    const titleInput = document.getElementById('noteNameInput');
    if (titleInput) {
      titleInput.focus();
      titleInput.select();
    }
    showToast('New note created', 'success');
  } catch (err) {
    console.error('Create note failed', err);
    showToast('Failed to create note', 'error');
  } finally {
    creatingNoteFromTabStrip = false;
    if (btn) {
      btn.disabled = false;
      btn.removeAttribute('aria-busy');
      btn.title = 'Create new note';
    }
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

// Cross-pane drag state — shared between both tab containers so a dragstart
// in pane A and a drop in pane B can resolve the source. Module-scoped
// because separate per-container closures couldn't see each other.
let _dragState = null; // { fromPane: 0|1, fromIdx: number } | null

function clearDragVisuals() {
  document.querySelectorAll('.is-dragging, .is-drop-target').forEach((el) => {
    el.classList.remove('is-dragging', 'is-drop-target');
  });
}

function showDetachZoneIfApplicable() {
  // The zone only makes sense when not already split AND the source pane
  // has more than one tab (detaching the only tab would just be a no-op).
  if (!state || state.splitMode !== 'none') return;
  const sourcePane = state.panes[_dragState?.fromPane ?? 0];
  if (!sourcePane || sourcePane.tabs.length < 2) return;
  const zone = document.getElementById('notePageDetachZone');
  if (zone) zone.hidden = false;
}

function hideDetachZone() {
  const zone = document.getElementById('notePageDetachZone');
  if (zone) {
    zone.hidden = true;
    zone.classList.remove('is-drop-target');
  }
}

function installDetachZone() {
  const zone = document.getElementById('notePageDetachZone');
  if (!zone) return;

  zone.addEventListener('dragover', (e) => {
    if (!_dragState) return;
    e.preventDefault();
    try { e.dataTransfer.dropEffect = 'move'; } catch (_) {}
    zone.classList.add('is-drop-target');
  });

  zone.addEventListener('dragleave', () => {
    zone.classList.remove('is-drop-target');
  });

  zone.addEventListener('drop', async (e) => {
    if (!_dragState || !window.NoteTabs) return;
    e.preventDefault();
    const { fromPane, fromIdx } = _dragState;
    const draggedNoteId = state.panes[fromPane]?.tabs?.[fromIdx];
    if (!draggedNoteId) return;
    if (state.splitMode !== 'none') return; // already split, ignore

    // Split right (clones the source's active tab into pane 1), then move
    // the actual dragged tab into pane 1 at the end. If the dragged tab
    // happens to be the source's active tab, the clone + move-into-self
    // sequence is handled by moveTab's dedupe pass.
    state = window.NoteTabs.splitRight(state);
    state = window.NoteTabs.focusPane(state, 0); // keep editor in pane 0
    // Recompute the source index (splitRight may have changed pane 0's
    // active tab; the source tabs array itself is unchanged though).
    const targetIdx = state.panes[1]?.tabs?.length ?? 0;
    state = window.NoteTabs.moveTab(state, fromPane, 1, fromIdx, targetIdx);
    persistState();
    renderTabStrip();
    await renderSecondaryPane();

    const editorActive = state.panes[0]?.activeId || null;
    if (editorActive && editorActive !== currentNote?.id) {
      await loadNoteIntoActivePane(editorActive);
    }
  });
}

// installDragReorder wires HTML5 drag events on one tab container. Together
// with the module-level _dragState, drag-reorder works within a pane and
// drag-move works across panes (dropping a pane-0 tab onto a pane-1 tab
// moves it via NoteTabs.moveTab).
function installDragReorder(container, paneIndex) {
  if (!container) return;
  const tabSelector = '.note-tab[draggable="true"]';

  container.addEventListener('dragstart', (e) => {
    const tab = e.target.closest(tabSelector);
    if (!tab) return;
    const fromIdx = Number(tab.dataset.index);
    const fromPane = Number(tab.dataset.pane);
    if (Number.isNaN(fromIdx) || fromPane !== paneIndex) return;
    _dragState = { fromPane: paneIndex, fromIdx };
    tab.classList.add('is-dragging');
    try {
      e.dataTransfer.effectAllowed = 'move';
      e.dataTransfer.setData('text/plain', `${paneIndex}:${fromIdx}`);
    } catch (_) {}
    // Reveal the detach drop zone when not already split — dropping there
    // splits the pane right with the dragged tab.
    showDetachZoneIfApplicable();
  });

  container.addEventListener('dragend', () => {
    clearDragVisuals();
    hideDetachZone();
    _dragState = null;
  });

  container.addEventListener('dragover', (e) => {
    if (!_dragState) return;
    // Accept drops anywhere inside the container, not just on a .note-tab —
    // an empty-area drop appends to the end of that pane's tabs.
    e.preventDefault();
    try { e.dataTransfer.dropEffect = 'move'; } catch (_) {}
    document.querySelectorAll('.is-drop-target').forEach((el) => el.classList.remove('is-drop-target'));
    const tab = e.target.closest(tabSelector);
    if (tab) {
      tab.classList.add('is-drop-target');
    } else {
      container.classList.add('is-drop-target');
    }
  });

  container.addEventListener('dragleave', (e) => {
    // Only clear when leaving the container itself (not its children).
    if (e.target === container) container.classList.remove('is-drop-target');
  });

  container.addEventListener('drop', async (e) => {
    if (!_dragState || !window.NoteTabs) return;
    e.preventDefault();
    const { fromPane, fromIdx } = _dragState;

    // Drop target index: the tab we dropped on, or the end of the pane's
    // list if the drop landed on the container's empty area.
    const tab = e.target.closest(tabSelector);
    let toIdx;
    if (tab) {
      toIdx = Number(tab.dataset.index);
      if (Number.isNaN(toIdx)) return;
    } else {
      toIdx = state.panes[paneIndex]?.tabs?.length ?? 0;
    }

    if (fromPane === paneIndex) {
      if (toIdx === fromIdx) return;
      state = window.NoteTabs.reorder(state, paneIndex, fromIdx, toIdx);
    } else {
      state = window.NoteTabs.moveTab(state, fromPane, paneIndex, fromIdx, toIdx);
    }
    persistState();
    renderTabStrip();
    await renderSecondaryPane();

    const editorActive = state.panes[0]?.activeId || null;
    if (editorActive && editorActive !== currentNote?.id) {
      await loadNoteIntoActivePane(editorActive);
    }
  });
}

// promoteSecondary swaps the two panes so the secondary's active note
// becomes the editable one. The editor markup is fixed in pane 0, so
// "promote" really means "swap panes."
async function promoteSecondary() {
  if (!window.NoteTabs || state?.splitMode === 'none') return;
  const newPrimaryId = state.panes[1]?.activeId;
  if (!newPrimaryId) return;
  state = window.NoteTabs.swapPanes(state);
  persistState();
  renderTabStrip();
  await renderSecondaryPane();
  if (currentNote?.id !== newPrimaryId) {
    await loadNoteIntoActivePane(newPrimaryId);
  }
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
  bindGenerateWithAIControls();

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

  // HTML5 drag-to-reorder for the primary tab strip. Drag stays within the
  // same pane in this iteration; cross-pane drag is a follow-up.
  installDragReorder(document.getElementById('notePageTabList'), 0);

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

  // Drag-to-reorder for the secondary pane's tabs too.
  installDragReorder(document.getElementById('notePageSecondaryTabs'), 1);

  // Detach drop zone — appears during drag when not split.
  installDetachZone();

  // Secondary pane editor input → debounced autosave. Bound once on the
  // textarea; the textarea persists across re-renders.
  document.getElementById('notePageSecondaryEditor')?.addEventListener('input', scheduleSecondarySave);

  // Flush the secondary pane's pending save before the page unloads.
  window.addEventListener('beforeunload', () => {
    if (secondaryDirty && secondarySaveTimer) {
      clearTimeout(secondarySaveTimer);
      // beforeunload can't await fetch; fire-and-forget keepalive PATCH.
      const textarea = document.getElementById('notePageSecondaryEditor');
      if (secondaryNote?.id && textarea) {
        try {
          fetch(`/api/notes/${encodeURIComponent(secondaryNote.id)}`, {
            method: 'PATCH',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ name: secondaryNote.name || 'Untitled Note', content: textarea.value }),
            keepalive: true,
          });
        } catch (_) {}
      }
    }
  });

  // Wire split-right / unsplit / promote buttons.
  document.getElementById('notePageSplitRightBtn')?.addEventListener('click', splitRight);
  document.getElementById('notePageUnsplitBtn')?.addEventListener('click', unsplit);
  document.getElementById('notePagePromoteBtn')?.addEventListener('click', promoteSecondary);

  document.getElementById('notePageNewTabBtn')?.addEventListener('click', createNoteFromTabStrip);

  // 9. Hash-anchor scroll (shared helper — also runs on tab swaps).
  scrollToHashHeading();

  // 10. "Open in modal" button.
  document.getElementById('noteOpenInModal')?.addEventListener('click', async () => {
    const saved = await bundle?.autosave?.flushImmediate?.();
    if (saved === false) {
      showToast('Save failed. Retry before opening this note in the modal.', 'error');
      return;
    }
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
