// search-palette.js — global ⌘K / Ctrl+K palette for jumping to notes and headings.
//
// Mounts itself once on DOMContentLoaded against the markup in
// templates/components/search-palette.tmpl. Hits two endpoints in parallel:
//
//   GET /api/notes/search?q=…         — full-text over note bodies
//   GET /api/notes/search/headings?q=…— FTS over the heading index added in 1.0
//
// Selection of a Note result calls window.sessionManager.openNoteEditor(id);
// selection of a Heading result calls a small wrapper that opens the editor
// in live-preview mode and scrolls to the heading.

const RECENT_KEY = 'note.search.recent';
const RECENT_MAX = 5;
const QUERY_MIN = 2;
const QUERY_DEBOUNCE_MS = 150;

const state = {
  open: false,
  query: '',
  loading: false,
  notes: [],
  headings: [],
  selectedIndex: 0,
  debounceTimer: null,
  inFlightAbort: null,
  previousFocus: null,
};

function $(id) { return document.getElementById(id); }

function escapeHtml(s) {
  return String(s ?? '').replace(/[&<>"']/g, (c) => (
    { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]
  ));
}

// Heading-search snippets come back already wrapped with <mark>. Note-search
// snippets do too. We trust the server's snippet escaping.
function snippetHTML(s) { return s || ''; }

// =============================================================================
// Open / close
// =============================================================================

function open() {
  const palette = $('searchPalette');
  if (!palette || state.open) return;
  state.previousFocus = document.activeElement;
  state.open = true;
  palette.hidden = false;
  const input = $('searchPaletteInput');
  if (input) {
    input.value = state.query;
    input.focus();
    input.select();
  }
  if (!state.query) renderRecent();
  else renderResults();
}

function close() {
  const palette = $('searchPalette');
  if (!palette || !state.open) return;
  state.open = false;
  palette.hidden = true;
  if (state.inFlightAbort) {
    state.inFlightAbort.abort();
    state.inFlightAbort = null;
  }
  if (state.previousFocus && typeof state.previousFocus.focus === 'function') {
    state.previousFocus.focus();
  }
  state.previousFocus = null;
}

function toggle() {
  if (state.open) close();
  else open();
}

// =============================================================================
// Query + fetch
// =============================================================================

function onInput(value) {
  state.query = (value || '').trim();
  state.selectedIndex = 0;
  if (state.debounceTimer) clearTimeout(state.debounceTimer);

  if (state.query.length < QUERY_MIN) {
    state.notes = [];
    state.headings = [];
    state.loading = false;
    if (!state.query) renderRecent();
    else renderResults();
    return;
  }

  state.debounceTimer = setTimeout(() => runQuery(state.query), QUERY_DEBOUNCE_MS);
}

async function runQuery(q) {
  if (state.inFlightAbort) state.inFlightAbort.abort();
  const ac = new AbortController();
  state.inFlightAbort = ac;
  state.loading = true;
  renderResults();

  try {
    const [notesResp, headingsResp] = await Promise.allSettled([
      fetch(`/api/notes/search?q=${encodeURIComponent(q)}`, { signal: ac.signal }),
      fetch(`/api/notes/search/headings?q=${encodeURIComponent(q)}`, { signal: ac.signal }),
    ]);

    let notes = [];
    let headings = [];
    if (notesResp.status === 'fulfilled' && notesResp.value.ok) {
      const j = await notesResp.value.json();
      notes = j.notes || [];
    }
    if (headingsResp.status === 'fulfilled' && headingsResp.value.ok) {
      const j = await headingsResp.value.json();
      headings = j.headings || [];
    }
    if (ac.signal.aborted) return;
    state.notes = notes;
    state.headings = headings;
    state.loading = false;
    state.selectedIndex = 0;
    renderResults();
  } catch (err) {
    if (err?.name === 'AbortError') return;
    state.loading = false;
    state.notes = [];
    state.headings = [];
    renderStatus(`Search failed: ${err?.message || err}`);
  } finally {
    if (state.inFlightAbort === ac) state.inFlightAbort = null;
  }
}

// =============================================================================
// Render
// =============================================================================

function renderStatus(text) {
  const status = document.querySelector('[data-role="status"]');
  if (!status) return;
  status.textContent = text;
  status.hidden = !text;
}

function totalRows() {
  return state.notes.length + state.headings.length;
}

function rowAt(index) {
  if (index < state.notes.length) return { kind: 'note', item: state.notes[index] };
  return { kind: 'heading', item: state.headings[index - state.notes.length] };
}

function renderRecent() {
  const results = $('searchPaletteResults');
  if (!results) return;
  renderStatus('');
  const recent = readRecent();
  if (recent.length === 0) {
    results.innerHTML = '<div class="search-palette-empty">Type to search notes and headings…</div>';
    return;
  }
  results.innerHTML = `
    <div class="search-palette-section">
      <div class="search-palette-section-label">Recent</div>
      ${recent.map((q, i) => `
        <button type="button" class="search-palette-recent-row" data-index="${i}" data-query="${escapeHtml(q)}">
          <span class="search-palette-recent-icon">↺</span>
          <span class="search-palette-recent-text">${escapeHtml(q)}</span>
        </button>
      `).join('')}
    </div>
  `;
  results.querySelectorAll('.search-palette-recent-row').forEach(el => {
    el.addEventListener('click', () => {
      const q = el.dataset.query;
      const input = $('searchPaletteInput');
      if (input) input.value = q;
      onInput(q);
    });
  });
}

function renderResults() {
  const results = $('searchPaletteResults');
  if (!results) return;

  if (state.loading) {
    results.innerHTML = '<div class="search-palette-empty"><span class="spinner-border spinner-border-sm me-2"></span>Searching…</div>';
    renderStatus('');
    return;
  }

  if (state.query.length < QUERY_MIN) {
    renderRecent();
    return;
  }

  if (totalRows() === 0) {
    results.innerHTML = '<div class="search-palette-empty">No matching notes or headings.</div>';
    renderStatus('');
    return;
  }
  renderStatus('');

  const html = [];
  if (state.notes.length > 0) {
    html.push('<div class="search-palette-section"><div class="search-palette-section-label">Notes</div>');
    state.notes.forEach((n, i) => {
      const idx = i;
      const sel = idx === state.selectedIndex ? ' is-selected' : '';
      const wsName = n.workspace_name ? `<span class="search-palette-row-ws">${escapeHtml(n.workspace_name)}</span>` : '';
      const snippet = (n.snippets && n.snippets[0]) || '';
      html.push(`
        <button type="button" class="search-palette-row${sel}" data-index="${idx}" role="option" aria-selected="${idx === state.selectedIndex}">
          <span class="search-palette-row-icon">📄</span>
          <span class="search-palette-row-body">
            <span class="search-palette-row-title">${escapeHtml(n.name || 'Untitled')}</span>
            <span class="search-palette-row-meta">${wsName}${snippet ? '<span class="search-palette-row-snippet">' + snippetHTML(snippet) + '</span>' : ''}</span>
          </span>
        </button>
      `);
    });
    html.push('</div>');
  }
  if (state.headings.length > 0) {
    html.push('<div class="search-palette-section"><div class="search-palette-section-label">Headings</div>');
    state.headings.forEach((h, i) => {
      const idx = state.notes.length + i;
      const sel = idx === state.selectedIndex ? ' is-selected' : '';
      const wsName = h.workspace_name ? `<span class="search-palette-row-ws">${escapeHtml(h.workspace_name)}</span>` : '';
      const noteName = h.note_name ? escapeHtml(h.note_name) : 'Untitled';
      html.push(`
        <button type="button" class="search-palette-row${sel}" data-index="${idx}" role="option" aria-selected="${idx === state.selectedIndex}">
          <span class="search-palette-row-icon">${'#'.repeat(Math.min(h.level || 1, 6))}</span>
          <span class="search-palette-row-body">
            <span class="search-palette-row-title">${snippetHTML(h.snippet || escapeHtml(h.text || ''))}</span>
            <span class="search-palette-row-meta">${escapeHtml(noteName)} ${wsName}</span>
          </span>
        </button>
      `);
    });
    html.push('</div>');
  }
  results.innerHTML = html.join('');

  results.querySelectorAll('.search-palette-row').forEach(el => {
    const idx = Number(el.dataset.index);
    el.addEventListener('mousemove', () => {
      if (state.selectedIndex !== idx) {
        state.selectedIndex = idx;
        renderResults();
      }
    });
    el.addEventListener('click', () => activateIndex(idx));
  });

  const activeRow = results.querySelector('.search-palette-row.is-selected');
  activeRow?.scrollIntoView({ block: 'nearest' });
}

// =============================================================================
// Keyboard
// =============================================================================

function onInputKeyDown(e) {
  if (e.key === 'Escape') {
    e.preventDefault();
    close();
    return;
  }
  if (e.key === 'ArrowDown') {
    e.preventDefault();
    if (totalRows() > 0) {
      state.selectedIndex = Math.min(totalRows() - 1, state.selectedIndex + 1);
      renderResults();
    }
    return;
  }
  if (e.key === 'ArrowUp') {
    e.preventDefault();
    if (totalRows() > 0) {
      state.selectedIndex = Math.max(0, state.selectedIndex - 1);
      renderResults();
    }
    return;
  }
  if (e.key === 'Enter') {
    e.preventDefault();
    if (totalRows() > 0) activateIndex(state.selectedIndex);
    return;
  }
}

function activateIndex(idx) {
  if (idx < 0 || idx >= totalRows()) return;
  const row = rowAt(idx);
  pushRecent(state.query);
  close();
  if (row.kind === 'note') {
    // Route through openNote so the user's notes_open_behavior pref applies.
    window.sessionManager?.openNote?.(row.item.id);
  } else {
    const noteId = row.item.note_id;
    const headingText = row.item.text;
    // Heading results — only the modal flow has the heading-anchor helper.
    // For "page" / "page-new-tab" preference, navigate with a #hash so the
    // page can scroll to it.
    const behavior = window.sessionManager?._readNotesOpenBehavior?.() || 'modal';
    if (behavior === 'modal' && window.sessionManager?.openNoteEditorWithHeading) {
      window.sessionManager.openNoteEditorWithHeading(noteId, headingText);
    } else if (behavior === 'page') {
      window.location.href = `/notes/${encodeURIComponent(noteId)}#${encodeURIComponent(headingText)}`;
    } else if (behavior === 'page-new-tab') {
      window.open(`/notes/${encodeURIComponent(noteId)}#${encodeURIComponent(headingText)}`, '_blank', 'noopener');
    } else if (window.sessionManager?.openNote) {
      window.sessionManager.openNote(noteId);
    }
  }
}

// =============================================================================
// Recent queries (localStorage)
// =============================================================================

function readRecent() {
  try {
    const raw = localStorage.getItem(RECENT_KEY);
    return raw ? JSON.parse(raw) : [];
  } catch (_) {
    return [];
  }
}

function pushRecent(q) {
  q = (q || '').trim();
  if (!q) return;
  let list = readRecent();
  list = [q, ...list.filter(x => x !== q)].slice(0, RECENT_MAX);
  try { localStorage.setItem(RECENT_KEY, JSON.stringify(list)); } catch (_) { /* ignore */ }
}

// =============================================================================
// Mount
// =============================================================================

function mount() {
  const palette = $('searchPalette');
  if (!palette) return;

  const input = $('searchPaletteInput');
  input?.addEventListener('input', () => onInput(input.value));
  input?.addEventListener('keydown', onInputKeyDown);

  palette.querySelector('[data-role="overlay"]')?.addEventListener('click', () => close());

  // Focus trap — keep tab within the palette while it's open.
  palette.addEventListener('keydown', (e) => {
    if (!state.open) return;
    if (e.key === 'Tab') {
      // Only one focusable element (input); just keep it focused.
      e.preventDefault();
      input?.focus();
    }
  });

  // Global shortcut.
  document.addEventListener('keydown', (e) => {
    const isToggle = (e.metaKey || e.ctrlKey) && !e.altKey && !e.shiftKey && e.key.toLowerCase() === 'k';
    if (isToggle) {
      e.preventDefault();
      toggle();
    }
  });
}

if (document.readyState === 'loading') {
  document.addEventListener('DOMContentLoaded', mount);
} else {
  mount();
}

const api = { open, close, toggle };
if (typeof window !== 'undefined') {
  window.SearchPalette = api;
}
export default api;
export { open, close, toggle };
