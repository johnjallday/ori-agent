// note-page.js — bootstrap for workspace and focused note pages.
//
// Loads the active note via the API, populates the textarea + title, and calls
// NoteEditor.mount with a host adapter for the page surface. As of task 4.0
// (slice B), the page also owns multi-tab state via window.NoteTabs — the
// tab strip above the editor shows all open notes in the active pane. The
// editor is a single instance whose content is swapped when the active tab
// changes (no remount, so autosave/history controllers persist across
// tab switches).

import {
  notePath,
  readFocusedNoteRoute,
  readWorkspaceNotesRoute,
  workspaceNotePath,
  workspaceNotesPath,
} from './note-routes.js';

const LEGACY_STATE_KEY = 'note.tabs';
const STATE_KEY_PREFIX = 'note.tabs.workspace.';

function readInitialRoute() {
  if (typeof window === 'undefined') return { mode: 'workspace', workspaceId: '', noteId: '' };
  const workspaceRoute = readWorkspaceNotesRoute(window.location.pathname);
  const focusedRoute = readFocusedNoteRoute(window.location.pathname);
  const page = typeof document !== 'undefined' ? document.getElementById('noteMainContent') : null;
  const mode = page?.dataset?.pageMode || (focusedRoute.noteId ? 'focused' : 'workspace');
  const workspaceId = workspaceRoute.workspaceId || page?.dataset?.workspaceId || '';
  const noteId = workspaceRoute.noteId || focusedRoute.noteId || page?.dataset?.noteId || '';
  return { mode, workspaceId, noteId };
}

const INITIAL_ROUTE = readInitialRoute();
const NOTE_PAGE_MODE = INITIAL_ROUTE.mode === 'focused' ? 'focused' : 'workspace';
const FOCUSED_NOTE_PAGE = NOTE_PAGE_MODE === 'focused';
const WORKSPACE_ID = INITIAL_ROUTE.workspaceId;
const NOTE_ID = INITIAL_ROUTE.noteId;
let currentNote = null;          // the note shown in the active pane
let bundle = null;                // NoteEditor.mount return value (single instance)
let state = null;                 // NoteTabs reducer state
let switching = false;            // re-entrancy guard while swapping content
let pendingGenerateDraft = null;   // last whole-note AI draft waiting to apply
let creatingNoteFromTabStrip = false;
let stateWorkspaceId = WORKSPACE_ID || null; // workspace scope for persisted tab state

// =============================================================================
// State persistence (localStorage)
// =============================================================================

export function noteTabsStateKey(workspaceId) {
  const id = String(workspaceId || '').trim();
  return id ? `${STATE_KEY_PREFIX}${id}` : LEGACY_STATE_KEY;
}

function noteWorkspaceId(note) {
  return note?.workspace_id || note?.folder_id || '';
}

function currentWorkspaceId() {
  return stateWorkspaceId || noteWorkspaceId(currentNote) || WORKSPACE_ID || '';
}

function loadSavedState(fallbackNoteId, workspaceId) {
  if (!window.NoteTabs) return null;
  try {
    const raw = localStorage.getItem(noteTabsStateKey(workspaceId));
    if (!raw) return window.NoteTabs.initialState(fallbackNoteId);
    const parsed = JSON.parse(raw);
    return window.NoteTabs.hydrate(parsed, fallbackNoteId);
  } catch (_) {
    return window.NoteTabs.initialState(fallbackNoteId);
  }
}

function persistState() {
  if (FOCUSED_NOTE_PAGE) return;
  try { localStorage.setItem(noteTabsStateKey(stateWorkspaceId || noteWorkspaceId(currentNote)), JSON.stringify(state)); } catch (_) {}
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

async function fetchWorkspaceNotes(workspaceId) {
  if (!workspaceId) return [];
  try {
    const resp = await fetch(`/api/workspaces/${encodeURIComponent(workspaceId)}/notes`);
    if (!resp.ok) return [];
    const data = await resp.json();
    return Array.isArray(data?.notes) ? data.notes : [];
  } catch (_) {
    return [];
  }
}

export async function createWorkspaceNoteWithContent(workspaceId, name, content, fetchImpl = fetch) {
  if (!workspaceId) throw new Error('No workspace selected');
  const resp = await fetchImpl(`/api/workspaces/${encodeURIComponent(workspaceId)}/notes`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ name: name || 'Untitled', content: content || '' }),
  });
  if (!resp.ok) {
    throw new Error(`Failed to create note: ${resp.status}`);
  }
  const data = await resp.json();
  return data?.note || data;
}

export async function createWorkspaceNote(workspaceId, fetchImpl = fetch) {
  return createWorkspaceNoteWithContent(workspaceId, 'Untitled', '', fetchImpl);
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

function populateBreadcrumb(note, workspaceName, workspaceId = currentWorkspaceId()) {
  const link = document.getElementById('notePageWorkspaceLink');
  const id = workspaceId || noteWorkspaceId(note);
  if (link && id) {
    link.href = `/workspaces/${encodeURIComponent(id)}`;
    link.textContent = workspaceName || 'Workspace';
  }
}

function showWorkspaceEmptyState(message = 'No notes in this workspace yet. Create a note to start writing.') {
  const titleInput = document.getElementById('noteNameInput');
  const contentInput = document.getElementById('noteContentInput');
  if (titleInput) {
    titleInput.value = '';
    titleInput.placeholder = 'Create a note to start writing';
    titleInput.disabled = true;
  }
  if (contentInput) contentInput.value = '';
  const previewContent = document.getElementById('notePreviewContent');
  if (previewContent) {
    previewContent.innerHTML = `<div class="note-page-empty">${escapeText(message)}</div>`;
  }
}

function applyFocusedNotePageMode() {
  if (!FOCUSED_NOTE_PAGE || typeof document === 'undefined') return;
  const main = document.getElementById('noteMainContent');
  main?.classList.add('note-page-focused');

  const notesTab = document.getElementById('noteRailTabNotes');
  const outlineTab = document.getElementById('noteRailTabOutline');
  const notesPane = document.getElementById('noteRailPaneNotes');
  const outlinePane = document.getElementById('noteRailPaneOutline');
  if (notesTab) notesTab.hidden = true;
  if (notesPane) notesPane.hidden = true;
  if (outlineTab) {
    outlineTab.classList.add('is-active');
    outlineTab.setAttribute('aria-selected', 'true');
  }
  if (outlinePane) outlinePane.hidden = false;

  document.getElementById('notePageTabStrip')?.setAttribute('hidden', '');
  document.getElementById('notePageSecondaryPane')?.setAttribute('hidden', '');
  document.getElementById('notePageDetachZone')?.setAttribute('hidden', '');
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

function noteTabButtonId(paneIndex, noteId) {
  const safeId = String(noteId || '').replace(/[^a-zA-Z0-9_-]/g, '-');
  return `note-tab-${paneIndex}-${safeId}`;
}

function tabContainerForPane(paneIndex) {
  return document.getElementById(paneIndex === 0 ? 'notePageTabList' : 'notePageSecondaryTabs');
}

function tabButtonForElement(tabEl) {
  if (!tabEl) return null;
  if (tabEl.matches?.('[role="tab"]')) return tabEl;
  return tabEl.querySelector?.('[role="tab"]') || null;
}

function tabButtonsForPane(paneIndex) {
  const container = tabContainerForPane(paneIndex);
  if (!container) return [];
  return Array.from(container.querySelectorAll('[role="tab"]'));
}

function focusActiveTab(paneIndex) {
  if (!state?.panes?.[paneIndex]) return;
  const activeId = state.panes[paneIndex].activeId;
  const focus = () => {
    const buttons = tabButtonsForPane(paneIndex);
    const button = buttons.find((btn) => btn.closest('.note-tab')?.dataset.noteId === activeId);
    button?.focus?.();
  };
  if (typeof requestAnimationFrame === 'function') requestAnimationFrame(focus);
  else focus();
}

function readPageSelection() {
  if (typeof document === 'undefined' || typeof window === 'undefined') return null;
  if (!window.NoteEditor) return null;
  const panes = [{
    paneId: 'primary',
    getContent: () => document.getElementById('noteContentInput')?.value || '',
    isPreviewMode: () => true,
    textareaId: 'noteContentInput',
    previewPaneId: 'notePreviewContent',
  }];
  // Scan the secondary pane too when it's mounted, so selections made there
  // open the AI Assist bar against the secondary content.
  if (secondaryBundle && document.getElementById('notePageSecondaryPreview')) {
    panes.push({
      paneId: 'secondary',
      getContent: () => document.getElementById('notePageSecondaryEditor')?.value || '',
      isPreviewMode: () => true,
      textareaId: 'notePageSecondaryEditor',
      previewPaneId: 'notePageSecondaryPreview',
    });
  }
  return window.NoteEditor.readSelection({ panes });
}

// getPagePaneApi routes AI Assist content I/O to a specific pane. Returning
// null tells initAIAssist to fall back to the primary host callbacks.
function getPagePaneApi(paneId) {
  if (paneId !== 'secondary' || !secondaryBundle || secondaryReadOnly) return null;
  const source = document.getElementById('notePageSecondaryEditor');
  if (!source) return null;
  return {
    getContent: () => source.value || '',
    setContent: (value) => { source.value = String(value || ''); },
    pushUndo: () => secondaryBundle?.history?.push?.(source.value || ''),
    scheduleAutoSave: () => secondaryBundle?.autosave?.schedule?.(),
    render: () => secondaryBundle?.render?.(),
    scheduleTocRebuild: () => {},
  };
}

// createExtractedNote backs the "Extract → note" assist action: it creates a
// note in the current workspace from the selected text and returns the saved
// note ({ id, name }) so the caller can leave an exact wikilink. The wikilinks
// note cache is invalidated so the freshly-created link resolves on click
// instead of being treated as broken.
async function createExtractedNote(title, content) {
  const workspaceId = currentWorkspaceId();
  if (!workspaceId) {
    showToast('Cannot extract without a workspace.', 'error');
    return null;
  }
  try {
    const note = await createWorkspaceNoteWithContent(workspaceId, title, content);
    window.NoteWikilinks?.invalidateNotesCache?.(workspaceId);
    return note;
  } catch (err) {
    console.error('Failed to create extracted note:', err);
    showToast('Failed to create the extracted note', 'error');
    return null;
  }
}

function resetPageAIAssistForCurrentNote(agentId = window.NoteEditor?.getSelectedAgentId?.() || null) {
  const workspaceId = currentWorkspaceId();
  window.NoteAIAssist?.onNoteOpened?.({
    noteId: currentNote?.id || null,
    workspaceId: workspaceId || null,
    agentId,
  });
}

async function syncPageAIAssistForCurrentNote() {
  const workspaceId = currentWorkspaceId();
  if (workspaceId) {
    await window.NoteEditor?.applyAgentDefaultForWorkspace?.(workspaceId);
  }
  resetPageAIAssistForCurrentNote();
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
// Note tags (shared tag input widget; saved independently of content autosave)
// =============================================================================

let noteTagsWidget = null;

async function saveNoteTags(tags) {
  if (!currentNote?.id) return;
  const noteId = currentNote.id;
  try {
    const resp = await fetch(`/api/notes/${encodeURIComponent(noteId)}`, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ tags }),
    });
    if (!resp.ok) {
      showToast('Failed to save tags', 'error');
      return;
    }
    const data = await resp.json();
    if (currentNote?.id === noteId) {
      currentNote = { ...currentNote, tags: data?.note?.tags || tags };
    }
    // New tags should show up in suggestions everywhere right away.
    window.OriTagInput?.clearTagPoolCache?.();
  } catch (_) {
    showToast('Failed to save tags', 'error');
  }
}

function syncNoteTagsWidget() {
  const row = document.getElementById('notePageTagsRow');
  const mount = document.getElementById('notePageTagsMount');
  if (!row || !mount) return;
  if (!currentNote?.id) {
    row.hidden = true;
    return;
  }
  if (!noteTagsWidget && window.OriTagInput?.createTagInput) {
    noteTagsWidget = window.OriTagInput.createTagInput({
      container: mount,
      initialTags: currentNote.tags || [],
      onChange: (tags) => { void saveNoteTags(tags); },
    });
  } else if (noteTagsWidget) {
    noteTagsWidget.setTags(currentNote.tags || []);
  }
  row.hidden = !noteTagsWidget;
}

function primaryNoteIsDirty() {
  return Boolean(currentNote?.id && bundle?.autosave?.isDirty?.() && !switching);
}

function sendPrimaryKeepaliveSave() {
  if (!primaryNoteIsDirty()) return;
  const name = document.getElementById('noteNameInput')?.value?.trim() || 'Untitled Note';
  const content = document.getElementById('noteContentInput')?.value || '';
  const body = JSON.stringify({ name, content });
  try {
    bundle?.autosave?.cancel?.();
    fetch(`/api/notes/${encodeURIComponent(currentNote.id)}`, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body,
      keepalive: true,
    });
  } catch (_) {
    // Page is closing; there is no useful recovery surface here.
  }
}

function flushPrimaryWhenBackgrounded() {
  if (document.visibilityState === 'hidden' && primaryNoteIsDirty()) {
    void bundle?.autosave?.flushImmediate?.();
  }
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
  if (!prompt) {
    setGenerateError('Please enter a prompt.');
    return;
  }

  // If the panel was opened with a selection attached, dispatch as an Ask
  // suggestion card instead of generating a whole-note draft. Keeps the
  // selection-scoped flow in the rail with stage/commit semantics.
  const panelSelection = window.NoteEditor?.getPanelSelection?.();
  if (panelSelection && panelSelection.text) {
    const ok = window.NoteAIAssist?.dispatchAsk?.(panelSelection, prompt);
    if (!ok) {
      setGenerateError('Could not dispatch — select a workspace agent first.');
      return;
    }
    window.NoteEditor?.closeGeneratePanel?.();
    return;
  }

  const agentId = window.NoteEditor?.getSelectedAgentId?.() || '';
  const workspaceId = currentWorkspaceId();

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

  if (mode === 'insert') {
    setGenerateError('Insert is unavailable in the live preview. Use Append or Replace.');
    return;
  }

  bundle?.history?.push?.(currentContent);

  if (mode === 'append') {
    nextContent = currentContent.trim()
      ? `${currentContent.replace(/\s+$/, '')}\n\n${draftContent}`
      : draftContent;
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
  window.NoteEditor?.bindGenerateToggleButton?.();
  document.getElementById('noteAIGenerateBtn')?.addEventListener('click', generatePageNoteWithAI);
  document.getElementById('noteAICancelBtn')?.addEventListener('click', () => {
    pendingGenerateDraft = null;
    window.NoteEditor?.closeGeneratePanel?.();
  });
  document.getElementById('noteAIReplaceBtn')?.addEventListener('click', () => applyPageGenerateDraft('replace'));
  document.getElementById('noteAIAppendBtn')?.addEventListener('click', () => applyPageGenerateDraft('append'));
  const insertBtn = document.getElementById('noteAIInsertBtn');
  if (insertBtn) {
    insertBtn.disabled = true;
    insertBtn.title = 'Insert is unavailable in the live preview. Use Append or Replace.';
    insertBtn.addEventListener('click', () => applyPageGenerateDraft('insert'));
  }
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
    const tabButton = tabButtonForElement(tab);
    if (tabButton) tabButton.title = label;
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

function updatePaneFocusState() {
  if (typeof document === 'undefined') return;
  const focusedIndex = state?.focusedPaneIndex ?? 0;
  document.getElementById('notePagePrimaryPane')?.classList.toggle('is-focused', focusedIndex === 0);
  document.getElementById('notePageSecondaryPane')?.classList.toggle('is-focused', focusedIndex === 1);
}

function renderTabStrip() {
  const strip = document.getElementById('notePageTabStrip');
  const list = document.getElementById('notePageTabList');
  const splitBtn = document.getElementById('notePageSplitRightBtn');
  const unsplitBtn = document.getElementById('notePageUnsplitBtn');
  if (FOCUSED_NOTE_PAGE) {
    if (strip) strip.hidden = true;
    return;
  }
  if (!strip || !list || !state) return;

  // Always show the strip: the "+" button is the entry point to opening
  // additional notes in tabs, so it needs to be reachable even when the
  // current page has just one open tab.
  strip.hidden = false;
  updatePaneFocusState();

  // Split button visibility — only meaningful when not already split.
  if (splitBtn) splitBtn.hidden = state.splitMode !== 'none' || (state.panes[0]?.tabs?.length ?? 0) === 0;
  if (unsplitBtn) unsplitBtn.hidden = state.splitMode === 'none';

  // The primary tab strip shows pane 0's tabs (the editable pane).
  const pane = state.panes[0];
  if (!pane) { list.innerHTML = ''; return; }

  list.innerHTML = pane.tabs.map((id, i) => {
    const isActive = id === pane.activeId;
    const label = escapeText(tabLabel(id));
    const buttonId = escapeAttr(noteTabButtonId(0, id));
    return `<li class="note-tab${isActive ? ' is-active' : ''}" data-note-id="${escapeAttr(id)}" data-pane="0" data-index="${i}" role="presentation" draggable="true">
      <button type="button" id="${buttonId}" class="note-tab-button" role="tab" aria-selected="${isActive}" aria-controls="notePagePrimaryPane" tabindex="${isActive ? '0' : '-1'}" title="${label}">
        <span class="note-tab-label">${label}</span>
      </button>
      <button type="button" class="note-tab-close" data-note-id="${escapeAttr(id)}" aria-label="Close tab" title="Close">×</button>
    </li>`;
  }).join('');
}

// Secondary pane's local state. Its note is independent of pane 0's currentNote.
let secondaryNote = null;       // the full note object loaded in pane 1
let secondaryBundle = null;     // NoteEditor.mount return value for pane 1
let secondaryReadOnly = false;  // true when pane 1 mirrors pane 0's note
let secondaryStatusTimer = null;

function setSecondaryStatus(text) {
  const el = document.getElementById('notePageSecondaryStatus');
  if (el) el.textContent = text || '';
}

function setSecondaryAutosaveStatus(status) {
  if (secondaryStatusTimer) {
    clearTimeout(secondaryStatusTimer);
    secondaryStatusTimer = null;
  }
  if (status === 'unsaved') {
    setSecondaryStatus('Unsaved');
  } else if (status === 'saving') {
    setSecondaryStatus('Saving…');
  } else if (status === 'error') {
    setSecondaryStatus('Save failed');
  } else if (status === 'saved') {
    setSecondaryStatus('Saved');
    secondaryStatusTimer = setTimeout(() => {
      secondaryStatusTimer = null;
      if (!secondaryBundle?.autosave?.isDirty?.()) setSecondaryStatus('');
    }, 1200);
  }
}

function ensureSecondaryBundle() {
  if (secondaryBundle || !window.NoteEditor) return secondaryBundle;
  const source = document.getElementById('notePageSecondaryEditor');
  const preview = document.getElementById('notePageSecondaryPreview');
  if (!source || !preview) return null;

  secondaryBundle = window.NoteEditor.mount({
    previewPaneId: 'notePageSecondaryPreview',
    enableToc: false,
    getContent: () => source.value || '',
    setContent: (value) => { source.value = String(value || ''); },
    getContentLines: () => {
      const value = source.value || '';
      return value.length > 0 ? value.split('\n') : [''];
    },
    setContentLines: (lines) => { source.value = (lines || []).join('\n'); },
    isPreviewMode: () => true,
    isReadOnly: () => secondaryReadOnly,
    onAutosaveFlush: flushSecondarySave,
    onAutosaveStatusChange: setSecondaryAutosaveStatus,
    autosaveDelayMs: 800,
  });
  // Register the secondary textarea with the shared selection tracker so
  // text selections in plain-edit mode there also drive the AI Assist bar.
  // (Preview-mode selections come through document selectionchange, which
  // is already wired by the primary mount.)
  window.NoteEditor.wireSelectionTracking?.({ textareaIds: ['notePageSecondaryEditor'] });
  return secondaryBundle;
}

function mirrorPrimaryIntoSecondarySource(source) {
  if (!source || currentNote?.id !== secondaryNote?.id) return;
  const primaryContent = document.getElementById('noteContentInput')?.value ?? currentNote.content ?? '';
  source.value = primaryContent;
  secondaryNote = {
    ...secondaryNote,
    name: currentNote.name || secondaryNote.name,
    content: primaryContent,
  };
}

async function flushSecondarySave() {
  if (!secondaryNote?.id || secondaryReadOnly) return true;
  const source = document.getElementById('notePageSecondaryEditor');
  if (!source) return false;
  const content = source.value;
  try {
    const resp = await fetch(`/api/notes/${encodeURIComponent(secondaryNote.id)}`, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ name: secondaryNote.name || 'Untitled Note', content }),
    });
    if (!resp.ok) return false;
    const data = await resp.json();
    if (data?.note) {
      secondaryNote = { ...secondaryNote, ...data.note };
      _tabLabelCache.set(secondaryNote.id, secondaryNote.name || 'Untitled');
      refreshSecondaryTabLabels();
    }
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
    return true;
  } catch (_) {
    return false;
  }
}

// renderSecondaryPane paints the secondary pane's tab strip + rendered
// Markdown editor when split mode is on. The secondary owns its own
// NoteEditor bundle so it stays editable without stealing the primary
// pane's history/autosave/outline state.
async function renderSecondaryPane() {
  const aside = document.getElementById('notePageSecondaryPane');
  const tabsEl = document.getElementById('notePageSecondaryTabs');
  const grid = document.getElementById('notePagePaneGrid');
  const source = document.getElementById('notePageSecondaryEditor');
  const preview = document.getElementById('notePageSecondaryPreview');
  const banner = document.getElementById('notePageSecondaryBanner');
  const emptyEl = document.getElementById('notePageSecondaryEmpty');
  if (FOCUSED_NOTE_PAGE) {
    if (aside) aside.hidden = true;
    if (grid) grid.classList.remove('is-split');
    return;
  }
  if (!aside || !tabsEl || !grid || !source || !preview || !banner || !emptyEl || !state) return;
  ensureSecondaryBundle();

  if (state.splitMode === 'none' || !state.panes[1]) {
    // Flush any pending save before tearing down.
    await secondaryBundle?.autosave?.flushImmediate?.();
    aside.hidden = true;
    grid.classList.remove('is-split');
    secondaryNote = null;
    secondaryReadOnly = false;
    preview.hidden = true;
    source.value = '';
    updatePaneFocusState();
    return;
  }

  aside.hidden = false;
  grid.classList.add('is-split');
  updatePaneFocusState();

  const pane = state.panes[1];
  tabsEl.innerHTML = pane.tabs.map((id, i) => {
    const isActive = id === pane.activeId;
    const label = escapeText(tabLabel(id));
    const buttonId = escapeAttr(noteTabButtonId(1, id));
    return `<div class="note-tab${isActive ? ' is-active' : ''}" data-note-id="${escapeAttr(id)}" data-pane="1" data-index="${i}" role="presentation" title="${label}" draggable="true">
      <button type="button" id="${buttonId}" class="note-tab-button" role="tab" aria-selected="${isActive}" aria-controls="notePageSecondaryPane" tabindex="${isActive ? '0' : '-1'}" title="${label}">
        <span class="note-tab-label">${label}</span>
      </button>
      <button type="button" class="note-tab-close note-tab-close-secondary" data-action="close" data-note-id="${escapeAttr(id)}" aria-label="Close tab" title="Close">×</button>
    </div>`;
  }).join('');

  if (!pane.activeId) {
    preview.hidden = true;
    banner.hidden = true;
    emptyEl.hidden = false;
    secondaryNote = null;
    secondaryReadOnly = false;
    source.value = '';
    setSecondaryStatus('');
    return;
  }

  // Flush previous secondary save before loading a different note.
  if (secondaryNote && secondaryNote.id !== pane.activeId) {
    await secondaryBundle?.autosave?.flushImmediate?.();
  } else if (secondaryNote?.id === pane.activeId) {
    const sameAsPrimary = currentNote?.id === secondaryNote.id;
    secondaryReadOnly = sameAsPrimary;
    if (sameAsPrimary) {
      secondaryBundle?.live?.state?.reset?.();
      mirrorPrimaryIntoSecondarySource(source);
    }
    banner.hidden = !sameAsPrimary;
    emptyEl.hidden = true;
    preview.hidden = false;
    secondaryBundle?.render?.();
    return;
  }

  const note = await fetchNote(pane.activeId);
  if (!note) {
    preview.hidden = true;
    banner.hidden = true;
    emptyEl.hidden = false;
    emptyEl.textContent = 'Could not load this note.';
    secondaryNote = null;
    secondaryReadOnly = false;
    return;
  }
  _tabLabelCache.set(note.id, note.name || 'Untitled');
  refreshSecondaryTabLabels();
  secondaryNote = note;
  secondaryReadOnly = currentNote?.id === note.id;
  emptyEl.hidden = true;
  emptyEl.textContent = 'No note selected.';
  source.value = note.content || '';
  if (secondaryReadOnly) mirrorPrimaryIntoSecondarySource(source);
  secondaryBundle?.history?.reset?.();
  secondaryBundle?.autosave?.reset?.();
  secondaryBundle?.live?.state?.reset?.();
  setSecondaryStatus('');

  // If the same note is in pane 0's editor, lock the secondary to avoid
  // diverging edits. Otherwise it's freely editable.
  banner.hidden = !secondaryReadOnly;
  preview.hidden = false;
  secondaryBundle?.render?.();
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
  const nextWorkspaceId = noteWorkspaceId(next);
  const expectedWorkspaceId = currentWorkspaceId();
  if (expectedWorkspaceId && nextWorkspaceId && nextWorkspaceId !== expectedWorkspaceId) {
    switching = false;
    showLoadError('This note belongs to a different workspace.');
    showToast('Note belongs to a different workspace', 'error');
    return false;
  }
  stateWorkspaceId = expectedWorkspaceId || nextWorkspaceId || stateWorkspaceId;

  // 3. Swap inputs + caches + history.
  const titleInput = document.getElementById('noteNameInput');
  const contentInput = document.getElementById('noteContentInput');
  if (titleInput) {
    titleInput.disabled = false;
    titleInput.value = next.name || '';
  }
  if (contentInput) contentInput.value = next.content || '';
  if (next.name) document.title = `${next.name} - Ori Agent`;
  _tabLabelCache.set(next.id, next.name || 'Untitled');

  currentNote = next;
  resetPageAIAssistForCurrentNote(null);
  syncNoteTagsWidget();
  fetchWorkspaceName(stateWorkspaceId).then((name) => populateBreadcrumb(currentNote, name, stateWorkspaceId));

  // 4. Reset history so undo doesn't cross note boundaries.
  bundle?.history?.reset?.();
  bundle?.autosave?.markClean?.();

  // 5. Update URL so refresh / share / browser back work intuitively.
  const nextPath = FOCUSED_NOTE_PAGE
    ? notePath(next.id, window.location.hash)
    : workspaceNotePath(stateWorkspaceId || nextWorkspaceId, next.id, window.location.hash);
  if (`${window.location.pathname}${window.location.hash}` !== nextPath) {
    const url = nextPath;
    window.history.pushState(null, '', url);
  }

  // 6. Re-render preview, refresh side panels.
  bundle?.render?.();
  window.NoteBacklinks?.loadBacklinksFor(next.id);
  window.NoteRailNotes?.setActiveNoteId(next.id);
  window.NotePresence?.claimOpenNote(next.id, 'page');
  await syncPageAIAssistForCurrentNote();
  if (state?.splitMode !== 'none') {
    await renderSecondaryPane();
  }

  // 7. If the URL carries a heading hash (e.g., palette → tab), scroll to it.
  scrollToHashHeading();

  switching = false;
  return true;
}

// =============================================================================
// Action wrappers (mutate state + persist + re-render)
// =============================================================================

async function openInTab(noteId) {
  if (!noteId) return;
  if (FOCUSED_NOTE_PAGE) {
    const saved = await bundle?.autosave?.flushImmediate?.();
    if (saved === false) {
      showToast('Save failed. Retry before opening another note.', 'error');
      return;
    }
    window.location.href = notePath(noteId);
    return;
  }
  if (!window.NoteTabs) return;
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

function setFocusedPane(paneIndex) {
  if (!window.NoteTabs || !state) return;
  const next = window.NoteTabs.focusPane(state, paneIndex);
  if (next === state) {
    updatePaneFocusState();
    return;
  }
  state = next;
  persistState();
  updatePaneFocusState();
}

async function createNoteFromTabStrip() {
  if (FOCUSED_NOTE_PAGE) return;
  if (creatingNoteFromTabStrip) return;
  const workspaceId = currentWorkspaceId();
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
  if (FOCUSED_NOTE_PAGE) return;
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
  focusActiveTab(paneIndex);
}

async function closeTab(noteId, paneIndex) {
  if (FOCUSED_NOTE_PAGE) return;
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
    const wsId = currentWorkspaceId();
    window.NotePresence?.releaseOpenNote(currentNote?.id);
    window.location.href = wsId ? workspaceNotesPath(wsId) : '/';
    return;
  }
  // If the editor pane's active note changed, load it.
  if (paneIndex === 0 && pane0.activeId !== currentNote?.id) {
    await loadNoteIntoActivePane(pane0.activeId);
  }
  focusActiveTab(Math.min(paneIndex, state.panes.length - 1));
}

function focusTabByOffset(paneIndex, fromButton, offset) {
  const buttons = tabButtonsForPane(paneIndex);
  if (buttons.length === 0) return;
  const currentIndex = Math.max(0, buttons.indexOf(fromButton));
  const nextIndex = (currentIndex + offset + buttons.length) % buttons.length;
  buttons[nextIndex]?.focus?.();
}

function focusTabAtEdge(paneIndex, edge) {
  const buttons = tabButtonsForPane(paneIndex);
  if (buttons.length === 0) return;
  const index = edge === 'end' ? buttons.length - 1 : 0;
  buttons[index]?.focus?.();
}

async function handleTabListKeydown(event, paneIndex) {
  const tabButton = event.target.closest?.('[role="tab"]');
  if (!tabButton) return;
  const tabEl = tabButton.closest('.note-tab');
  const noteId = tabEl?.dataset.noteId;

  if (event.key === 'ArrowRight') {
    event.preventDefault();
    focusTabByOffset(paneIndex, tabButton, 1);
    return;
  }
  if (event.key === 'ArrowLeft') {
    event.preventDefault();
    focusTabByOffset(paneIndex, tabButton, -1);
    return;
  }
  if (event.key === 'Home') {
    event.preventDefault();
    focusTabAtEdge(paneIndex, 'start');
    return;
  }
  if (event.key === 'End') {
    event.preventDefault();
    focusTabAtEdge(paneIndex, 'end');
    return;
  }
  if (event.key === 'Enter' || event.key === ' ') {
    event.preventDefault();
    if (noteId) await switchToTab(noteId, paneIndex);
    focusActiveTab(paneIndex);
    return;
  }
  if ((event.key === 'Delete' || event.key === 'Backspace') && noteId) {
    event.preventDefault();
    await closeTab(noteId, paneIndex);
  }
}

async function splitRight() {
  if (FOCUSED_NOTE_PAGE) return;
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
  if (FOCUSED_NOTE_PAGE) return;
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
  if (FOCUSED_NOTE_PAGE) return;
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
    getWorkspaceId: () => currentWorkspaceId() || null,
    getPageMode: () => NOTE_PAGE_MODE,
    notePath,
    workspaceNotePath,
  };
}

// =============================================================================
// Bootstrap
// =============================================================================

async function bootstrap() {
  applyFocusedNotePageMode();

  if (!FOCUSED_NOTE_PAGE && !WORKSPACE_ID) {
    showLoadError('No workspace ID in URL.');
    showToast('No workspace ID in URL', 'error');
    return;
  }
  if (FOCUSED_NOTE_PAGE && !NOTE_ID) {
    showLoadError('No note ID in URL.');
    showToast('No note ID in URL', 'error');
    return;
  }
  if (!window.NoteEditor) {
    showLoadError('NoteEditor module failed to load.');
    console.error('NoteEditor module not loaded');
    return;
  }

  stateWorkspaceId = WORKSPACE_ID || null;

  // 1. Resolve the initial note from the route, saved workspace tab state,
  //    or the workspace's most recently updated note.
  let initialNoteId = NOTE_ID;
  if (FOCUSED_NOTE_PAGE) {
    currentNote = await fetchNote(NOTE_ID);
    if (!currentNote) {
      showLoadError(`Note not found (id: ${NOTE_ID}). The note may have been deleted or you may not have access.`);
      showToast('Note not found', 'error');
      return;
    }
    stateWorkspaceId = noteWorkspaceId(currentNote);
    initialNoteId = currentNote.id;
  } else {
    if (window.NoteTabs) {
      state = loadSavedState(initialNoteId || null, stateWorkspaceId);
      if (!initialNoteId) {
        const pane = state.panes[state.focusedPaneIndex];
        initialNoteId = pane?.activeId || state.panes[0]?.activeId || '';
      }
    }

    if (initialNoteId) {
      currentNote = await fetchNote(initialNoteId);
      if (!currentNote) {
        if (NOTE_ID) {
          showLoadError(`Note not found (id: ${NOTE_ID}). The note may have been deleted or you may not have access.`);
          showToast('Note not found', 'error');
          return;
        }
        initialNoteId = '';
      } else if (noteWorkspaceId(currentNote) && noteWorkspaceId(currentNote) !== stateWorkspaceId) {
        showLoadError('This note belongs to a different workspace.');
        showToast('Note belongs to a different workspace', 'error');
        return;
      }
    }

    if (!currentNote) {
      const notes = await fetchWorkspaceNotes(stateWorkspaceId);
      if (notes[0]?.id) {
        initialNoteId = notes[0].id;
        currentNote = await fetchNote(initialNoteId);
        if (currentNote && noteWorkspaceId(currentNote) && noteWorkspaceId(currentNote) !== stateWorkspaceId) {
          currentNote = null;
          initialNoteId = '';
        }
      }
    }
  }

  if (currentNote?.id) {
    _tabLabelCache.set(currentNote.id, currentNote.name || 'Untitled');
  }

  // 2. Initialize tab state under the route workspace. Focused note pages keep
  // a single internal tab only, with no multi-note UI shown or persisted.
  if (window.NoteTabs) {
    if (FOCUSED_NOTE_PAGE) {
      state = window.NoteTabs.initialState(currentNote.id);
    } else if (NOTE_ID) {
      state = loadSavedState(NOTE_ID, stateWorkspaceId);
      const pane = state.panes[state.focusedPaneIndex];
      if (!pane?.tabs?.includes(NOTE_ID)) {
        state = window.NoteTabs.openTab(state, NOTE_ID, state.focusedPaneIndex);
      } else if (pane.activeId !== NOTE_ID) {
        state = window.NoteTabs.setActiveTab(state, state.focusedPaneIndex, NOTE_ID);
      }
    } else if (currentNote?.id) {
      state = loadSavedState(currentNote.id, stateWorkspaceId);
      const pane = state.panes[state.focusedPaneIndex];
      if (!pane?.tabs?.includes(currentNote.id)) {
        state = window.NoteTabs.initialState(currentNote.id);
      } else if (pane.activeId !== currentNote.id) {
        state = window.NoteTabs.setActiveTab(state, state.focusedPaneIndex, currentNote.id);
      }
    } else {
      state = window.NoteTabs.initialState(null);
    }
    persistState();
  } else {
    state = {
      panes: [{ activeId: currentNote?.id || null, tabs: currentNote?.id ? [currentNote.id] : [] }],
      splitMode: 'none',
      focusedPaneIndex: 0,
    };
  }

  // 3. Populate the editor inputs + document title.
  const titleInput = document.getElementById('noteNameInput');
  const contentInput = document.getElementById('noteContentInput');
  if (currentNote) {
    if (titleInput) {
      titleInput.disabled = false;
      titleInput.value = currentNote.name || '';
    }
    if (contentInput) contentInput.value = currentNote.content || '';
    if (currentNote.name) document.title = `${currentNote.name} - Ori Agent`;
  } else {
    showWorkspaceEmptyState();
    document.title = 'Workspace Notes - Ori Agent';
  }
  syncNoteTagsWidget();

  // 4. Breadcrumb workspace name (best effort).
  fetchWorkspaceName(stateWorkspaceId).then((name) => populateBreadcrumb(currentNote, name, stateWorkspaceId));

  // 5. Cross-module hooks (wikilinks, backlinks, rail, presence).
  window.NoteWikilinks?.setWorkspaceContext(() => currentWorkspaceId() || null);
  if (currentNote?.id) window.NoteBacklinks?.loadBacklinksFor(currentNote.id);
  else window.NoteBacklinks?.clearBacklinks?.();
  if (!FOCUSED_NOTE_PAGE) {
    window.NoteRailNotes?.initRail({
      workspaceIdResolver: () => currentWorkspaceId() || null,
      activeNoteId: currentNote?.id || null,
    });
  }
  // Apply persisted collapse state so the rail doesn't flash expanded on
  // first paint when the user previously collapsed it.
  window.NoteEditor?.applyAllRailState?.();
  if (currentNote?.id) window.NotePresence?.claimOpenNote(currentNote.id, 'page');
  window.addEventListener('beforeunload', () => {
    if (currentNote?.id) window.NotePresence?.releaseOpenNote(currentNote.id);
  });
  document.addEventListener('visibilitychange', flushPrimaryWhenBackgrounded);
  window.addEventListener('pagehide', sendPrimaryKeepaliveSave);
  window.addEventListener('beforeunload', sendPrimaryKeepaliveSave);

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
      getPaneApi: getPagePaneApi,
      createNote: createExtractedNote,
    },
  });
  bundle.render();
  if (!currentNote) showWorkspaceEmptyState();
  // Build the Outline immediately on first paint so users see headings
  // without waiting for the scheduleRebuild debounce inside render().
  bundle.toc?.rebuild?.();
  titleInput?.addEventListener('input', () => bundle.autosave.schedule());
  bindGenerateWithAIControls();

  // Prime the Ask AI agent dropdown with this note's workspace and reset the
  // assist-card stack for the loaded note. Mirrors the modal flow in
  // sessions.js, but the page has to do it explicitly.
  await syncPageAIAssistForCurrentNote();

  // 7. Render the tab strip + secondary pane for the workspace app. Focused
  //    pages keep these hidden.
  renderTabStrip();
  renderSecondaryPane();
  if (!FOCUSED_NOTE_PAGE) prefetchTabLabels();

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
  document.getElementById('notePageTabList')?.addEventListener('keydown', (e) => {
    handleTabListKeydown(e, 0);
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
  document.getElementById('notePageSecondaryTabs')?.addEventListener('keydown', (e) => {
    handleTabListKeydown(e, 1);
  });

  // Drag-to-reorder for the secondary pane's tabs too.
  installDragReorder(document.getElementById('notePageSecondaryTabs'), 1);

  const primaryPane = document.getElementById('notePagePrimaryPane');
  const secondaryPane = document.getElementById('notePageSecondaryPane');
  primaryPane?.addEventListener('pointerdown', () => setFocusedPane(0));
  primaryPane?.addEventListener('focusin', () => setFocusedPane(0));
  secondaryPane?.addEventListener('pointerdown', () => setFocusedPane(1));
  secondaryPane?.addEventListener('focusin', () => setFocusedPane(1));

  // Detach drop zone — appears during drag when not split.
  installDetachZone();

  // Flush the secondary pane's pending save before the page unloads.
  window.addEventListener('beforeunload', () => {
    if (secondaryBundle?.autosave?.isDirty?.()) {
      secondaryBundle.autosave.cancel();
      // beforeunload can't await fetch; fire-and-forget keepalive PATCH.
      const source = document.getElementById('notePageSecondaryEditor');
      if (secondaryNote?.id && source && !secondaryReadOnly) {
        try {
          fetch(`/api/notes/${encodeURIComponent(secondaryNote.id)}`, {
            method: 'PATCH',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ name: secondaryNote.name || 'Untitled Note', content: source.value }),
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

  // Sidebar toggle (Notes/Outline rail). The modal context wires this in
  // sessions.js, but sessions.js isn't loaded on note pages.
  document.getElementById('noteTocToggle')?.addEventListener('click', () => {
    window.NoteEditor?.toggleRail?.('toc');
  });

  // 9. Hash-anchor scroll (shared helper — also runs on tab swaps).
  scrollToHashHeading();

  // 10. Expose the public API for other modules (wikilinks → open in tab).
  bindPublicAPI();
}

if (typeof document !== 'undefined') {
  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', bootstrap);
  } else {
    bootstrap();
  }
}
