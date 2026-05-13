// note-ai-assist.js — selection action bar + AI Assist sidebar.
//
// Coordinates four pieces:
//   1. The floating action bar that appears when text is selected in the note
//      editor (works in both plain-edit and live-preview modes).
//   2. The HTTP dispatcher to /api/notes/assist.
//   3. The AI Assist sidebar that stacks suggestion cards.
//   4. Per-card actions: Stage / Unstage, Copy, Discard, Refine, Quick commit
//      (Quick commit + Stage are wired to the staging engine in task 5.0).
//
// ESM module loaded via <script type="module">. Exposes window.NoteAIAssist
// for the non-module sessions.js to drive.

const ACTIONS = [
  { id: 'expand',    label: 'Expand',    icon: '+',    defaultMode: 'replace'      },
  { id: 'summarize', label: 'Summarize', icon: '≡',    defaultMode: 'replace'      },
  { id: 'rewrite',   label: 'Rewrite',   icon: '✎',    defaultMode: 'replace'      },
  { id: 'counter',   label: 'Counter',   icon: '↹',    defaultMode: 'insert-after' },
  { id: 'cite',      label: 'Cite',      icon: '★',    defaultMode: 'insert-after' },
  { id: 'ask',       label: 'Ask AI…',   icon: '?',    defaultMode: 'insert-after' },
];

const MAX_CARDS_PER_NOTE = 20;

const state = {
  noteId: null,
  workspaceId: null,
  agentId: null,
  cards: [],            // Suggestion[] for the current note
  pendingSelection: null, // { range, text, source: 'textarea'|'preview' }
  bar: null,             // floating action-bar element
  rail: null,            // assist rail element
  cardsContainer: null,  // where cards mount
  emptyEl: null,         // empty-state element
  sessionsApi: null,     // bridge back to sessions.js for save / scroll
  view: 'stack',         // 'stack' | 'review'
};

function escapeHtml(s) {
  return String(s).replace(/[&<>"']/g, (c) => (
    { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]
  ));
}

function uid() {
  return 'sg_' + Math.random().toString(36).slice(2, 10) + Date.now().toString(36);
}

// =============================================================================
// Public API — called from sessions.js
// =============================================================================

// init wires DOM nodes and a small bridge object that exposes helpers from
// sessions.js (current selection, current workspace agent, scrollToRange,
// scheduleAutoSave, etc.).
function init(opts) {
  state.bar = opts.bar;
  state.rail = opts.rail;
  state.sessionsApi = opts.sessionsApi;

  if (state.rail) {
    state.cardsContainer = state.rail.querySelector('[data-role="content"]');
    state.emptyEl = state.rail.querySelector('[data-role="empty"]');
  }
  buildActionBar();
}

// onNoteOpened resets the per-note state.
function onNoteOpened({ noteId, workspaceId, agentId }) {
  state.noteId = noteId || null;
  state.workspaceId = workspaceId || null;
  state.agentId = agentId || null;
  state.cards = [];
  hideBar();
  render();
}

// onAgentChanged updates the working agent (e.g., the workspace's selected agent).
function onAgentChanged(agentId) {
  state.agentId = agentId || null;
  // Disabled-state of the action bar is read at click time — no re-render needed.
}

// onSelectionChanged is called by sessions.js when the user makes a text
// selection in the note editor (textarea or live-preview). selection is
// `{ text, source, anchorRect }` — text is the selected string, source is
// 'textarea' or 'preview', anchorRect is a DOMRect to position against.
function onSelectionChanged(selection) {
  if (!selection || !selection.text || selection.text.trim() === '') {
    // While the user is typing into the Ask-AI input, focus moves out of the
    // preview pane and selectionchange fires with an empty selection. Don't
    // tear the bar down — keep is-asking mode and the pending selection
    // intact. The user closes the input via Esc / Send / submitting.
    if (state.bar?.classList.contains('is-asking')) return;
    hideBar();
    state.pendingSelection = null;
    return;
  }
  state.pendingSelection = selection;
  positionBar(selection.anchorRect);
  showBar();
}

function hideBar() {
  if (state.bar) state.bar.style.display = 'none';
  if (state.bar) state.bar.classList.remove('is-asking');
  const askInput = state.bar?.querySelector('[data-role="ask-input"]');
  if (askInput) askInput.value = '';
}

function showBar() {
  if (!state.bar) return;
  state.bar.style.display = 'flex';
}

function positionBar(rect) {
  if (!state.bar || !rect) return;
  const barRect = state.bar.getBoundingClientRect();
  const PADDING = 8;
  let top = rect.top - (barRect.height || 36) - PADDING;
  if (top < 8) top = rect.bottom + PADDING;
  let left = rect.left;
  if (left + (barRect.width || 320) > window.innerWidth - 8) {
    left = Math.max(8, window.innerWidth - (barRect.width || 320) - 8);
  }
  state.bar.style.top = `${Math.round(top)}px`;
  state.bar.style.left = `${Math.round(left)}px`;
}

// =============================================================================
// Action bar
// =============================================================================

function buildActionBar() {
  const bar = state.bar;
  if (!bar) return;
  bar.innerHTML = '';
  const isMac = typeof navigator !== 'undefined' && /Mac|iPhone|iPad|iPod/.test(navigator.platform || '');
  const askShortcut = isMac ? '⌘J' : 'Ctrl+J';
  for (const action of ACTIONS) {
    const btn = document.createElement('button');
    btn.type = 'button';
    btn.className = 'note-ai-action-btn';
    btn.dataset.action = action.id;
    btn.title = action.id === 'ask' ? `${action.label} (${askShortcut})` : action.label;
    btn.textContent = action.label;
    btn.addEventListener('click', () => onActionClick(action.id));
    bar.appendChild(btn);
  }

  // Inline Ask-AI input that the bar expands into when "Ask AI…" is clicked.
  // Two rows: a preview of the user's selection (so they still see what
  // they're asking about after focus moves away from the editor), then the
  // input + send button.
  const askWrap = document.createElement('div');
  askWrap.className = 'note-ai-ask-wrap';
  askWrap.innerHTML = `
    <div class="note-ai-ask-selection" data-role="ask-selection" title=""></div>
    <div class="note-ai-ask-row">
      <input type="text" class="note-ai-ask-input" data-role="ask-input"
             placeholder="Ask AI about the selection — Enter to send, Esc to cancel" />
      <button type="button" class="note-ai-ask-send" data-role="ask-send">Send</button>
    </div>
  `;
  const askInput = askWrap.querySelector('[data-role="ask-input"]');
  const askSend = askWrap.querySelector('[data-role="ask-send"]');
  askInput.addEventListener('keydown', (e) => {
    if (e.key === 'Enter') { e.preventDefault(); submitAsk(askInput.value); }
    else if (e.key === 'Escape') { e.preventDefault(); collapseAsk(); }
  });
  askSend.addEventListener('click', () => submitAsk(askInput.value));
  bar.appendChild(askWrap);
}

function onActionClick(actionId) {
  if (!isAgentReady()) {
    notifyAgentMissing();
    return;
  }
  if (actionId === 'ask') {
    expandAsk();
    return;
  }
  dispatch({ action: actionId });
}

function isAgentReady() {
  return Boolean(state.agentId);
}

function notifyAgentMissing() {
  state.sessionsApi?.showToast?.('Select a workspace agent to use inline AI', 'warning');
}

function expandAsk() {
  if (!state.bar) return;
  state.bar.classList.add('is-asking');
  const sel = state.pendingSelection?.text || '';
  const selEl = state.bar.querySelector('[data-role="ask-selection"]');
  if (selEl) {
    // Preserve newlines so multi-paragraph selections still read as
    // structured text; only collapse runs of horizontal whitespace and
    // trim leading/trailing blank space around the whole block.
    const cleaned = sel.replace(/[ \t]+/g, ' ').replace(/^\s+|\s+$/g, '');
    const MAX = 280;
    selEl.textContent = cleaned.length > MAX ? `${cleaned.slice(0, MAX - 1)}…` : cleaned;
    selEl.title = cleaned;
  }
  const input = state.bar.querySelector('[data-role="ask-input"]');
  input?.focus();
}

// openAskForSelection is the public entry point used by keyboard shortcuts.
// Takes a selection (as returned by readSelection), positions the bar, and
// expands it directly into Ask mode. Returns true on success.
function openAskForSelection(selection) {
  if (!selection || !selection.text || !selection.text.trim()) return false;
  if (!isAgentReady()) { notifyAgentMissing(); return false; }
  onSelectionChanged(selection);
  expandAsk();
  return true;
}

function collapseAsk() {
  if (!state.bar) return;
  state.bar.classList.remove('is-asking');
  const input = state.bar.querySelector('[data-role="ask-input"]');
  if (input) input.value = '';
  const selEl = state.bar.querySelector('[data-role="ask-selection"]');
  if (selEl) { selEl.textContent = ''; selEl.title = ''; }
}

function submitAsk(prompt) {
  prompt = (prompt || '').trim();
  if (!prompt) return;
  if (!isAgentReady()) { notifyAgentMissing(); return; }
  dispatch({ action: 'ask', prompt });
  collapseAsk();
}

// =============================================================================
// Dispatch + cards
// =============================================================================

function dispatch({ action, prompt = null, parentId = null }) {
  const sel = state.pendingSelection;
  if (!sel) return;

  const card = {
    id: uid(),
    action,
    prompt,
    sourceRange: sel.range || null,
    originalText: sel.text,
    output: '',
    mode: ACTIONS.find(a => a.id === action)?.defaultMode || 'replace',
    staged: false,
    status: 'loading',
    parentId,
    paneId: sel.paneId || '',
    createdAt: Date.now(),
  };

  // Bound the stack (no eviction policy yet — just hard cap).
  state.cards.unshift(card);
  if (state.cards.length > MAX_CARDS_PER_NOTE) {
    state.cards = state.cards.slice(0, MAX_CARDS_PER_NOTE);
  }
  hideBar();
  render();

  callAssistEndpoint(card);
}

async function callAssistEndpoint(card) {
  try {
    const noteContent = state.sessionsApi?.getNoteContent?.(card.paneId) || '';
    const context = noteContent === card.originalText
      ? ''
      : noteContent.replace(card.originalText, '⟦SELECTED⟧');
    const history = buildHistoryChain(card);

    const resp = await fetch('/api/notes/assist', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        workspace_id: state.workspaceId || '',
        agent_id: state.agentId || '',
        action: card.action,
        selection: card.originalText,
        context,
        prompt: card.prompt || '',
        history,
      }),
    });

    if (!resp.ok) {
      let message = 'AI assist failed';
      try { const j = await resp.json(); message = j.error || message; } catch (_) { /* ignore */ }
      throw new Error(message);
    }
    const data = await resp.json();
    card.output = (data.content || '').trim();
    card.status = 'ready';
  } catch (err) {
    card.status = 'error';
    card.errorMessage = err?.message || String(err);
  }
  render();
}

function buildHistoryChain(card) {
  const chain = [];
  let cursor = card.parentId;
  while (cursor) {
    const parent = state.cards.find(c => c.id === cursor);
    if (!parent || parent.status !== 'ready') break;
    chain.unshift({
      prompt: parent.prompt || ACTIONS.find(a => a.id === parent.action)?.label || '',
      output: parent.output,
    });
    cursor = parent.parentId;
  }
  return chain;
}

// =============================================================================
// Render
// =============================================================================

function render() {
  if (!state.cardsContainer || !state.emptyEl) return;

  // No cards at all: empty state + hide rail.
  if (state.cards.length === 0) {
    state.view = 'stack';
    state.emptyEl.style.display = '';
    state.cardsContainer.style.display = 'none';
    state.cardsContainer.innerHTML = '';
    removeStatusBar();
    state.sessionsApi?.hideAssistRail?.();
    return;
  }

  state.emptyEl.style.display = 'none';
  state.cardsContainer.style.display = '';
  state.sessionsApi?.showAssistRail?.();
  state.cardsContainer.innerHTML = '';

  if (state.view === 'review') {
    state.cardsContainer.appendChild(renderReviewPane());
  } else {
    for (const card of state.cards) {
      state.cardsContainer.appendChild(renderCard(card));
    }
  }
  updateStatusBar();
}

function removeStatusBar() {
  state.rail?.querySelector('.note-ai-status-bar')?.remove();
}

function renderCard(card) {
  const el = document.createElement('article');
  el.className = 'note-ai-card';
  el.dataset.cardId = card.id;
  el.dataset.status = card.status;
  if (card.staged) el.classList.add('is-staged');

  const label = ACTIONS.find(a => a.id === card.action)?.label || card.action;
  const ts = new Date(card.createdAt).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });

  el.innerHTML = `
    <header class="note-ai-card-header">
      <span class="note-ai-card-label">${escapeHtml(label)}</span>
      <span class="note-ai-card-time">${escapeHtml(ts)}</span>
      <button type="button" class="note-ai-card-discard" title="Discard">×</button>
    </header>
    <div class="note-ai-card-original">
      <span class="note-ai-card-original-label">Original</span>
      <pre class="note-ai-card-original-text">${escapeHtml(card.originalText)}</pre>
    </div>
    <div class="note-ai-card-output" data-role="output"></div>
    <div class="note-ai-card-controls" data-role="controls"></div>
    <div class="note-ai-card-refine" data-role="refine">
      <input type="text" class="note-ai-card-refine-input" placeholder="Refine — e.g. 'shorter', 'more formal'" />
      <button type="button" class="note-ai-card-refine-send" title="Send refinement">Refine</button>
    </div>
  `;

  // Output region
  const outputEl = el.querySelector('[data-role="output"]');
  if (card.status === 'loading') {
    outputEl.innerHTML = '<div class="note-ai-card-loading"><span class="spinner-border spinner-border-sm me-2"></span>Working…</div>';
  } else if (card.status === 'error') {
    outputEl.innerHTML = `<div class="note-ai-card-error">${escapeHtml(card.errorMessage || 'Failed')}<button type="button" class="note-ai-card-retry">Retry</button></div>`;
    outputEl.querySelector('.note-ai-card-retry')?.addEventListener('click', () => retryCard(card.id));
  } else if (card.status === 'committed') {
    outputEl.innerHTML = `<div class="note-ai-card-committed">Committed.</div>`;
  } else {
    outputEl.innerHTML = `<pre class="note-ai-card-output-text">${escapeHtml(card.output)}</pre>`;
  }

  // Controls — only available once the suggestion is ready
  const controls = el.querySelector('[data-role="controls"]');
  if (card.status === 'ready') {
    controls.innerHTML = `
      <label class="note-ai-card-mode">
        Mode:
        <select data-role="mode">
          <option value="replace">Replace</option>
          <option value="insert-before">Insert before</option>
          <option value="insert-after">Insert after</option>
        </select>
      </label>
      <button type="button" class="note-ai-card-stage" data-role="stage">${card.staged ? 'Unstage' : 'Stage'}</button>
      <button type="button" class="note-ai-card-copy" data-role="copy">Copy</button>
      <button type="button" class="note-ai-card-quick" data-role="quick" title="Stage and commit this single suggestion now">Quick commit</button>
    `;
    controls.querySelector('[data-role="mode"]').value = card.mode;
    controls.querySelector('[data-role="mode"]').addEventListener('change', (e) => updateCardMode(card.id, e.target.value));
    controls.querySelector('[data-role="stage"]').addEventListener('click', () => toggleStaged(card.id));
    controls.querySelector('[data-role="copy"]').addEventListener('click', () => copyCard(card.id));
    controls.querySelector('[data-role="quick"]').addEventListener('click', () => quickCommit(card.id));
  } else {
    controls.style.display = 'none';
  }

  // Refine — also gated on ready
  const refine = el.querySelector('[data-role="refine"]');
  if (card.status !== 'ready') {
    refine.style.display = 'none';
  } else {
    const refineInput = refine.querySelector('input');
    const refineBtn = refine.querySelector('button');
    const submitRefine = () => {
      const v = (refineInput.value || '').trim();
      if (!v) return;
      refineInput.value = '';
      addRefinement(card.id, v);
    };
    refineInput.addEventListener('keydown', (e) => {
      if (e.key === 'Enter') { e.preventDefault(); submitRefine(); }
    });
    refineBtn.addEventListener('click', submitRefine);
  }

  el.querySelector('.note-ai-card-discard').addEventListener('click', () => discardCard(card.id));
  return el;
}

function updateStatusBar() {
  if (!state.rail) return;
  let bar = state.rail.querySelector('.note-ai-status-bar');
  const stagedCount = state.cards.filter(c => c.staged).length;
  if (!bar) {
    bar = document.createElement('div');
    bar.className = 'note-ai-status-bar';
    bar.innerHTML = '<span data-role="count"></span><button type="button" class="note-ai-review-btn" disabled>Review &amp; Commit</button>';
    bar.querySelector('.note-ai-review-btn').addEventListener('click', () => openReviewPane());
    state.rail.appendChild(bar);
  }
  bar.querySelector('[data-role="count"]').textContent =
    stagedCount === 0 ? 'No staged changes' : `${stagedCount} staged`;
  const reviewBtn = bar.querySelector('.note-ai-review-btn');
  reviewBtn.disabled = stagedCount === 0 || state.view === 'review';
  reviewBtn.title = stagedCount === 0
    ? 'Stage a suggestion to enable Review & Commit'
    : 'Review staged changes and commit them to the note';
}

// =============================================================================
// Card actions
// =============================================================================

function findCard(id) { return state.cards.find(c => c.id === id) || null; }

function updateCardMode(id, mode) {
  const card = findCard(id);
  if (!card) return;
  card.mode = mode;
}

function toggleStaged(id) {
  const card = findCard(id);
  if (!card || card.status !== 'ready') return;
  card.staged = !card.staged;
  render();
}

function copyCard(id) {
  const card = findCard(id);
  if (!card || !card.output) return;
  if (navigator.clipboard?.writeText) {
    navigator.clipboard.writeText(card.output).then(
      () => state.sessionsApi?.showToast?.('Suggestion copied', 'success'),
      () => state.sessionsApi?.showToast?.('Copy failed', 'error'),
    );
  }
}

function discardCard(id) {
  state.cards = state.cards.filter(c => c.id !== id);
  render();
}

function retryCard(id) {
  const card = findCard(id);
  if (!card) return;
  card.status = 'loading';
  card.errorMessage = '';
  render();
  callAssistEndpoint(card);
}

// =============================================================================
// Staging / Review & Commit
// =============================================================================

function getStagingApi() {
  return (typeof window !== 'undefined' && window.NoteStaging) || null;
}

function openReviewPane() {
  const stagedCount = state.cards.filter(c => c.staged).length;
  if (stagedCount === 0) return;
  state.view = 'review';
  render();
}

function closeReviewPane() {
  state.view = 'stack';
  render();
}

function lineNumberFor(position, paneId = '') {
  const src = state.sessionsApi?.getNoteContent?.(paneId) || '';
  let line = 1;
  for (let i = 0; i < position && i < src.length; i++) {
    if (src.charCodeAt(i) === 10) line++;
  }
  return line;
}

function renderReviewPane() {
  const wrap = document.createElement('div');
  wrap.className = 'note-ai-review-pane';

  const staging = getStagingApi();
  // Project per-pane so conflict detection only fires for hunks targeting
  // the same source. Annotate each hunk with the originating paneId so the
  // review UI can label and route correctly.
  const cardsByPane = new Map();
  for (const c of state.cards) {
    if (!c.staged || c.status !== 'ready') continue;
    const key = c.paneId || '';
    if (!cardsByPane.has(key)) cardsByPane.set(key, []);
    cardsByPane.get(key).push(c);
  }
  let hunks = [];
  if (staging) {
    for (const [paneId, cards] of cardsByPane) {
      const paneHunks = staging.projectHunks(cards);
      paneHunks.forEach((h) => { h.paneId = paneId; });
      hunks.push(...paneHunks);
    }
  }
  // Render hunks in document order so the diff scans top-to-bottom of the note.
  hunks.sort((a, b) => a.sourceRange.start - b.sourceRange.start);

  const hasConflict = hunks.some(h => h.conflictsWith.length > 0);

  const header = document.createElement('header');
  header.className = 'note-ai-review-header';
  header.innerHTML = `
    <button type="button" class="note-ai-review-back" data-role="back">← Back to suggestions</button>
    <span class="note-ai-review-count">${hunks.length} staged</span>
  `;
  header.querySelector('[data-role="back"]').addEventListener('click', () => closeReviewPane());
  wrap.appendChild(header);

  for (const hunk of hunks) {
    wrap.appendChild(renderReviewHunk(hunk, hunks));
  }

  const footer = document.createElement('div');
  footer.className = 'note-ai-review-footer';
  footer.innerHTML = `
    <button type="button" class="note-ai-discard-all" data-role="discard">Discard all</button>
    <button type="button" class="note-ai-commit" data-role="commit">Commit</button>
  `;
  footer.querySelector('[data-role="discard"]').addEventListener('click', () => discardAll());
  const commitBtn = footer.querySelector('[data-role="commit"]');
  commitBtn.disabled = hasConflict || hunks.length === 0;
  if (hasConflict) commitBtn.title = 'Resolve conflicts before committing';
  commitBtn.addEventListener('click', () => commit());
  wrap.appendChild(footer);

  return wrap;
}

function renderReviewHunk(hunk, allHunks) {
  const el = document.createElement('article');
  el.className = 'note-ai-review-hunk';
  if (hunk.conflictsWith.length > 0) el.classList.add('has-conflict');

  const label = ACTIONS.find(a => a.id === hunk.action)?.label || hunk.action;
  const lineNo = lineNumberFor(hunk.sourceRange.start, hunk.paneId);
  const modeLabel = hunk.mode === 'insert-before' ? 'before' :
                    hunk.mode === 'insert-after'  ? 'after'  :
                    'replace';
  const paneLabel = hunk.paneId === 'secondary' ? ' · pane 2' : '';

  const head = document.createElement('header');
  head.className = 'note-ai-review-hunk-head';
  head.innerHTML = `
    <span class="note-ai-review-hunk-label">${escapeHtml(label)} — ${escapeHtml(modeLabel)} (line ${lineNo}${escapeHtml(paneLabel)})</span>
    <button type="button" class="note-ai-review-unstage" data-role="unstage">Unstage</button>
  `;
  head.querySelector('[data-role="unstage"]').addEventListener('click', () => unstageHunk(hunk.id));
  el.appendChild(head);

  const staging = getStagingApi();
  const lines = staging ? staging.diffLines(hunk.originalText, hunk.output, hunk.mode) : [];
  const diff = document.createElement('pre');
  diff.className = 'note-ai-review-diff';
  diff.innerHTML = lines.map(l => {
    const cls = `note-ai-diff-${l.kind}`;
    const prefix = l.kind === 'added' ? '+ ' : l.kind === 'removed' ? '- ' : '  ';
    return `<span class="${cls}">${escapeHtml(prefix + l.text)}</span>`;
  }).join('\n');
  el.appendChild(diff);

  if (hunk.conflictsWith.length > 0) {
    const warn = document.createElement('div');
    warn.className = 'note-ai-review-conflict';
    const otherLabels = hunk.conflictsWith
      .map(id => allHunks.find(h => h.id === id))
      .filter(Boolean)
      .map(h => `${ACTIONS.find(a => a.id === h.action)?.label || h.action} (line ${lineNumberFor(h.sourceRange.start, h.paneId)})`)
      .join(', ');
    warn.textContent = `⚠ Conflicts with ${otherLabels}. Unstage one to commit.`;
    el.appendChild(warn);
  }
  return el;
}

function unstageHunk(hunkId) {
  // Hunk id == suggestion id by construction in projectHunks.
  const card = findCard(hunkId);
  if (!card) return;
  card.staged = false;
  render();
}

function discardAll() {
  for (const c of state.cards) c.staged = false;
  state.view = 'stack';
  render();
}

function commit() {
  const staging = getStagingApi();
  if (!staging) return;

  // Group staged cards by paneId so each pane's source is its own
  // projection — hunks across panes can never overlap (different sources),
  // and the apply step writes back to the correct pane.
  const cardsByPane = new Map();
  for (const c of state.cards) {
    if (!c.staged || c.status !== 'ready') continue;
    const key = c.paneId || '';
    if (!cardsByPane.has(key)) cardsByPane.set(key, []);
    cardsByPane.get(key).push(c);
  }
  if (cardsByPane.size === 0) return;

  // Project all panes first so we can check for any conflicts before
  // mutating any source (atomic-ish: refuse the whole commit on conflict).
  const projections = [];
  for (const [paneId, cards] of cardsByPane) {
    const hunks = staging.projectHunks(cards);
    if (hunks.some(h => h.conflictsWith.length > 0)) {
      state.sessionsApi?.showToast?.('Resolve conflicts before committing.', 'warning');
      return;
    }
    projections.push({ paneId, hunks });
  }

  const allApplied = [];
  for (const { paneId, hunks } of projections) {
    const source = state.sessionsApi?.getNoteContent?.(paneId) || '';
    state.sessionsApi?.pushUndo?.(paneId);
    const result = staging.applyHunks(source, hunks);
    state.sessionsApi?.setNoteContent?.(result.content, paneId);
    state.sessionsApi?.scheduleAutoSave?.(paneId);
    allApplied.push(...result.applied);
  }
  state.sessionsApi?.showToast?.(`Committed ${allApplied.length} change${allApplied.length === 1 ? '' : 's'} — ⌘Z to undo.`, 'success');

  // Mark committed cards and remove them from the stack after a short flash.
  const committedIds = new Set(allApplied);
  for (const c of state.cards) {
    if (committedIds.has(c.id)) {
      c.status = 'committed';
      c.staged = false;
    }
  }
  state.view = 'stack';
  render();
  setTimeout(() => {
    state.cards = state.cards.filter(c => c.status !== 'committed');
    render();
  }, 3000);
}

function quickCommit(cardId) {
  const card = findCard(cardId);
  if (!card || card.status !== 'ready') return;

  // Stage just this card temporarily, run commit through the same engine,
  // then ensure no other staged state is mutated.
  const previouslyStaged = state.cards.filter(c => c.staged).map(c => c.id);
  // Unstage everything else so only this card commits.
  for (const c of state.cards) c.staged = (c.id === cardId);
  commit();
  // Restore other staged flags (commit cleared them via committed status,
  // but those didn't apply since they weren't staged when commit ran).
  for (const c of state.cards) {
    if (previouslyStaged.includes(c.id)) c.staged = true;
  }
  render();
}

function addRefinement(parentId, refinementPrompt) {
  const parent = findCard(parentId);
  if (!parent) return;
  // Refinements always carry the parent's selection. Treat as an "ask" so
  // the prompt is verbatim.
  state.pendingSelection = {
    text: parent.originalText,
    range: parent.sourceRange,
  };
  dispatch({ action: 'ask', prompt: refinementPrompt, parentId });
}

// =============================================================================
// Module export + browser bridge
// =============================================================================

const api = {
  ACTIONS,
  init,
  onNoteOpened,
  onAgentChanged,
  onSelectionChanged,
  openAskForSelection,
  hideBar,
  render,
  // Expose for tasks 5.0 / debugging.
  _state: state,
};

if (typeof window !== 'undefined') {
  window.NoteAIAssist = api;
}

export default api;
export { init, onNoteOpened, onAgentChanged, onSelectionChanged, openAskForSelection, hideBar, render };
