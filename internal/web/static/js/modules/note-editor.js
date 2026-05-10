// note-editor.js — shared note editor surface.
//
// Status: SCAFFOLD. Task 1.0 of the v2 plan extracts the ~80 note-related
// methods on sessionManager into this module so both the modal (today) and
// the upcoming dedicated page (`/notes/<id>`) can mount the same editor
// without code duplication. The full extraction is a multi-round refactor;
// this file grows as each sub-task lands.
//
// What lives here today:
//   - Pure line-level helpers (heading-level detection, task-line parsing,
//     line-kind class assignment) used by the live-preview renderer.
//
// What's still in sessions.js (will migrate in subsequent rounds):
//   - Live-preview rendering pipeline (renderNoteLiveEditor + helpers).
//   - Autosave + history (scheduleNoteAutoSave, undo/redo stack).
//   - AI Assist wiring (selection tracking, agent resolution).
//   - TOC integration.
//   - Generate-with-AI panel.
//   - Modal-specific glue (open/close/show/hide).
//
// ESM module loaded via <script type="module"> from base.tmpl. Exposed as
// `window.NoteEditor` for the non-module sessions.js to consume.

// =============================================================================
// HTML escape — pure string version (replaces sessionManager's DOM-based one)
// =============================================================================
// The original sessionManager.escapeHtml used document.createElement; this
// pure version produces the same output for our use case (escaping `< > & " '`)
// without needing the DOM. Used by render helpers below and shared with
// non-DOM contexts (e.g., tests).

const HTML_ESCAPE_MAP = { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' };

export function escapeHtml(text) {
  return String(text ?? '').replace(/[&<>"']/g, (c) => HTML_ESCAPE_MAP[c]);
}

// =============================================================================
// Markdown renderers — pure functions of input strings
// =============================================================================
// All three depend on browser globals window.marked and window.DOMPurify when
// available. Without them, renderMarkdown falls back to a hand-rolled regex
// pipeline; renderMarkdownLine and renderInlineMarkdown fall back to escaped
// text. Behavior preserved exactly from the original sessionManager methods.

function _marked() {
  return typeof window !== 'undefined' && window.marked;
}
function _domPurify() {
  return typeof window !== 'undefined' && window.DOMPurify;
}

// renderMarkdown converts a multi-paragraph Markdown string to HTML. Used
// by the legacy whole-note preview path (not the live editor).
export function renderMarkdown(text) {
  if (!text) return '<p style="color: var(--text-tertiary);">No content</p>';

  const marked = _marked();
  if (marked && typeof marked.parse === 'function') {
    const dp = _domPurify();
    const canSanitize = dp && typeof dp.sanitize === 'function';
    const normalized = normalizeCompactTaskListMarkdown(text);
    const rendered = marked.parse(canSanitize ? normalized : escapeHtml(normalized), {
      breaks: true,
      gfm: true,
    });
    return canSanitize ? dp.sanitize(rendered) : rendered;
  }

  // Fallback when marked.js isn't loaded — minimal regex pipeline.
  let html = escapeHtml(text);
  html = html.replace(/^### (.+)$/gm, '<h3>$1</h3>');
  html = html.replace(/^## (.+)$/gm, '<h2>$1</h2>');
  html = html.replace(/^# (.+)$/gm, '<h1>$1</h1>');
  html = html.replace(/\*\*(.+?)\*\*/g, '<strong>$1</strong>');
  html = html.replace(/\*(.+?)\*/g, '<em>$1</em>');
  html = html.replace(/```(\w*)\n([\s\S]*?)```/g, '<pre><code>$2</code></pre>');
  html = html.replace(/`([^`]+)`/g, '<code>$1</code>');
  html = html.replace(/^\s*[-*]\s+(.+)$/gm, '<li>$1</li>');
  html = html.replace(/(<li>.*<\/li>\n?)+/g, '<ul>$&</ul>');
  html = html.replace(/^>\s+(.+)$/gm, '<blockquote>$1</blockquote>');
  html = html.replace(/\n\n/g, '</p><p>');
  html = '<p>' + html + '</p>';
  html = html.replace(/<p><\/p>/g, '');
  html = html.replace(/<p>(<h[1-6]>)/g, '$1');
  html = html.replace(/(<\/h[1-6]>)<\/p>/g, '$1');
  html = html.replace(/<p>(<ul>)/g, '$1');
  html = html.replace(/(<\/ul>)<\/p>/g, '$1');
  html = html.replace(/<p>(<pre>)/g, '$1');
  html = html.replace(/(<\/pre>)<\/p>/g, '$1');
  html = html.replace(/<p>(<blockquote>)/g, '$1');
  html = html.replace(/(<\/blockquote>)<\/p>/g, '$1');
  return html;
}

// renderMarkdownLine renders a single line of Markdown. Used by the live
// preview's per-line rendering path. Returns `<br>` for an empty line.
export function renderMarkdownLine(line) {
  if (!line) return '<br>';

  const marked = _marked();
  if (marked && typeof marked.parse === 'function') {
    const dp = _domPurify();
    const canSanitize = dp && typeof dp.sanitize === 'function';
    const normalized = normalizeCompactTaskListMarkdown(line);
    const rendered = marked.parse(canSanitize ? normalized : escapeHtml(normalized), {
      breaks: true,
      gfm: true,
    });
    return canSanitize ? dp.sanitize(rendered) : rendered;
  }
  return renderMarkdown(line);
}

// renderInlineMarkdown renders inline-only Markdown (bold/italic/code, no
// blocks). Used by task-line content where we don't want a `<p>` wrapper.
export function renderInlineMarkdown(text) {
  if (!text) return '';

  const marked = _marked();
  if (marked && typeof marked.parseInline === 'function') {
    const dp = _domPurify();
    const canSanitize = dp && typeof dp.sanitize === 'function';
    const rendered = marked.parseInline(canSanitize ? text : escapeHtml(text), {
      breaks: true,
      gfm: true,
    });
    return canSanitize ? dp.sanitize(rendered) : rendered;
  }
  return escapeHtml(text);
}

// =============================================================================
// Live-preview line renderers — templating for editable / rendered lines
// =============================================================================

// renderEditingLine produces the HTML for a single line being actively edited
// (textarea wrapped in a .note-live-line.is-editing div). The textarea's
// `data-line-index` lets event handlers map back to the source line.
export function renderEditingLine(line, index) {
  const kindClass = lineKindClass(line);
  const className = ['note-live-line-input', kindClass].filter(Boolean).join(' ');
  return `
      <div class="note-live-line is-editing" data-line-index="${index}">
        <textarea class="${className}" data-line-index="${index}" rows="1" spellcheck="true">${escapeHtml(line)}</textarea>
      </div>
    `;
}

// renderEditingRange produces the HTML for an inclusive range of lines being
// edited as one block (e.g., when the user multi-selects and starts typing).
// `markdown` is the joined content of the range; `startIndex`..`endIndex` are
// the source line indices the textarea covers.
export function renderEditingRange(markdown, startIndex, endIndex) {
  return `
      <div class="note-live-line is-editing is-block-editing" data-line-index="${startIndex}" data-line-end="${endIndex}">
        <textarea class="note-live-line-input note-live-block-input" data-line-start="${startIndex}" data-line-end="${endIndex}" spellcheck="true">${escapeHtml(markdown)}</textarea>
      </div>
    `;
}

// renderHeadingLine wraps a heading line with the fold chevron. `isCollapsed`
// is the host's view of whether this section is currently folded — host owns
// that state (a Set of line indices) and passes the bool here.
export function renderHeadingLine(line, index, isCollapsed) {
  const level = parseHeadingLevel(line);
  if (level === 0) return '';
  const expandedValue = isCollapsed ? 'false' : 'true';
  const summary = isCollapsed ? '<span class="note-heading-fold-summary">...</span>' : '';
  return `
      <div class="note-heading-line">
        <button type="button" class="note-heading-fold" data-line-index="${index}" aria-expanded="${expandedValue}" title="${isCollapsed ? 'Expand section' : 'Collapse section'}">
          <span aria-hidden="true">${isCollapsed ? '›' : '⌄'}</span>
        </button>
        <div class="note-heading-content">${renderMarkdownLine(line)}</div>
        ${summary}
      </div>
    `;
}

// renderTaskLine wraps a `- [ ] task` line with a checkbox plus inline-rendered
// content. Returns empty string if `line` isn't a task list item.
export function renderTaskLine(line, index) {
  const task = parseTaskLine(line);
  if (!task) return '';
  const checked = task.checked ? ' checked' : '';
  const content = task.text ? renderInlineMarkdown(task.text) : '';
  return `
      <span class="note-task-line">
        <input type="checkbox" class="note-task-checkbox" data-line-index="${index}"${checked} aria-label="Toggle checkbox">
        <span class="note-task-content">${content}</span>
      </span>
    `;
}

// renderRenderedLine composes the final wrapper for one source line in the
// live preview. Heading lines win over task lines; task lines win over plain
// markdown. An empty line collapses to `<br>` for spacing.
export function renderRenderedLine(line, index, isCollapsed) {
  const kindClass = lineKindClass(line);
  const emptyClass = line ? '' : ' is-empty';
  if (!line) {
    return `
      <div class="note-live-line note-live-line-rendered ${kindClass}${emptyClass}" data-line-index="${index}" tabindex="0">
        <br>
      </div>
    `;
  }
  const inner = renderHeadingLine(line, index, isCollapsed) || renderTaskLine(line, index) || renderMarkdownLine(line);
  return `
      <div class="note-live-line note-live-line-rendered ${kindClass}${emptyClass}" data-line-index="${index}" tabindex="0">
        ${inner}
      </div>
    `;
}

// =============================================================================
// Pure line-level helpers (PRD §4.1, task 1.0 first slice)
// =============================================================================

// parseHeadingLevel returns the ATX heading level (1–6) for `line`, or 0 if
// `line` is not a heading. Single source of truth for the live-preview
// renderer and the TOC outline (the latter goes through NoteTOC.parseHeadings
// which is the more rigorous parser; this one is a fast per-line check).
export function parseHeadingLevel(line) {
  const m = String(line || '').match(/^(#{1,6})\s+/);
  return m ? m[1].length : 0;
}

// parseTaskLine matches `- [x] body` / `- [ ] body` / `- [] body` (compact)
// task list items. Returns the parsed components, or null if the line isn't a
// task line.
export function parseTaskLine(line) {
  const match = String(line || '').match(/^(\s*)([-*+])(\s+)\[( |x|X)?\](\s*)(.*)$/);
  if (!match) return null;
  return {
    indent: match[1] || '',
    bullet: match[2] || '-',
    gap: match[3] || ' ',
    checked: String(match[4] || '').toLowerCase() === 'x',
    compactUnchecked: match[4] === '',
    afterGap: match[5] || '',
    text: match[6] || '',
  };
}

// lineKindClass picks the rendered CSS class for a single source line. Used
// by the live-preview renderer to mark headings, task lists, ordered/
// unordered lists, and blockquotes for styling.
export function lineKindClass(line) {
  const level = parseHeadingLevel(line);
  if (level > 0) return `is-heading-${level}`;
  if (parseTaskLine(line)) return 'is-task-list';
  if (/^\s*[-*+]\s+/.test(line)) return 'is-list';
  if (/^\s*\d+\.\s+/.test(line)) return 'is-list';
  if (/^\s*>\s+/.test(line)) return 'is-quote';
  return '';
}

// normalizeCompactTaskListMarkdown rewrites compact `- []` task markers into
// the canonical `- [ ]` form. Run before saving so other tools that read the
// markdown see standard syntax.
export function normalizeCompactTaskListMarkdown(text) {
  return String(text || '').replace(/^(\s*[-*+]\s+)\[\](?=\s|$)/gm, '$1[ ]');
}

// =============================================================================
// Content I/O — DOM accessors for the note textarea
// =============================================================================
// These wrap `#noteContentInput` reads/writes so callers can stop poking at the
// element directly. No `this`-state; safe to share between modal and page.

const NOTE_CONTENT_ID = 'noteContentInput';

export function getContentValue() {
  const el = typeof document !== 'undefined' ? document.getElementById(NOTE_CONTENT_ID) : null;
  return String(el?.value || '');
}

export function setContentValue(value) {
  const el = typeof document !== 'undefined' ? document.getElementById(NOTE_CONTENT_ID) : null;
  if (!el) return;
  el.value = String(value || '');
}

export function getContentLines() {
  const value = getContentValue();
  return value.length > 0 ? value.split('\n') : [''];
}

export function setContentLines(lines) {
  setContentValue((lines || []).join('\n'));
}

// =============================================================================
// Keyboard shortcuts (pure event-shape checks)
// =============================================================================

// isUndoShortcut returns true for ⌘Z / Ctrl+Z (without Shift).
export function isUndoShortcut(event) {
  if (!event) return false;
  return (event.metaKey || event.ctrlKey)
    && !event.altKey
    && !event.shiftKey
    && String(event.key || '').toLowerCase() === 'z';
}

// isRedoShortcut returns true for ⌘⇧Z / Ctrl+Shift+Z / Ctrl+Y / ⌘Y.
export function isRedoShortcut(event) {
  if (!event) return false;
  const key = String(event.key || '').toLowerCase();
  return (event.metaKey || event.ctrlKey)
    && !event.altKey
    && ((key === 'z' && event.shiftKey) || key === 'y');
}

// isPrintableKey returns true if the key would normally insert a character
// into a textarea (single-character key, no command modifiers).
export function isPrintableKey(event) {
  if (!event) return false;
  return String(event.key || '').length === 1
    && !event.metaKey
    && !event.ctrlKey
    && !event.altKey;
}

// =============================================================================
// Live-preview selection helpers
// =============================================================================
// Pure DOM helpers used by the live-preview pane to decide whether the user
// has highlighted multiple lines, whether a pointer event represents a drag,
// and to clear the browser's text selection on demand.

// selectionContains reports whether `node` (or its parent if it's a text node)
// is contained within `container`. Returns false for missing inputs so it's
// safe to call with `selection.anchorNode` etc.
export function selectionContains(container, node) {
  if (!container || !node) return false;
  const element = node.nodeType === Node.ELEMENT_NODE ? node : node.parentElement;
  return Boolean(element && container.contains(element));
}

// hasTextSelectionInside returns true if the browser's current text selection
// is non-collapsed and both endpoints sit inside `container`.
export function hasTextSelectionInside(container) {
  if (typeof window === 'undefined') return false;
  const selection = window.getSelection?.();
  if (!selection || selection.isCollapsed) return false;
  return selectionContains(container, selection.anchorNode)
    && selectionContains(container, selection.focusNode);
}

// pointerDragged returns true if `event`'s position has moved more than 4
// pixels from `origin` (the pointer-down coordinates). Used by the live-
// preview to distinguish clicks from drags so a double-click doesn't accidentally
// open block-edit mode.
export function pointerDragged(origin, event, threshold = 4) {
  if (!origin || !event) return false;
  const dx = Math.abs((event.clientX ?? 0) - (origin.x ?? 0));
  const dy = Math.abs((event.clientY ?? 0) - (origin.y ?? 0));
  return dx > threshold || dy > threshold;
}

// clearWindowSelection collapses the browser's current text selection if any.
// No-op when there's no selection.
export function clearWindowSelection() {
  if (typeof window === 'undefined') return;
  const selection = window.getSelection?.();
  if (selection && !selection.isCollapsed) selection.removeAllRanges();
}

// =============================================================================
// History — undo/redo stacks for note content edits
// =============================================================================
// State container — instantiated per editor surface. `applying` is set true
// while the editor is programmatically applying a previous state so that the
// resulting input event doesn't push another entry onto the stack.

export class NoteHistory {
  constructor({ limit = 100 } = {}) {
    this.undoStack = [];
    this.redoStack = [];
    this.applying = false;
    this.limit = limit;
  }

  // push records `value` as a new undo entry. Returns true if a new entry was
  // pushed; false if the value matches the top of the stack or applying is
  // currently true (caller should treat both as no-ops).
  push(value) {
    if (this.applying) return false;
    if (this.undoStack[this.undoStack.length - 1] === value) return false;
    this.undoStack.push(value);
    if (this.undoStack.length > this.limit) this.undoStack.shift();
    this.redoStack = [];
    return true;
  }

  // undo pops the most recent entry off the undo stack and returns its value;
  // simultaneously pushes `currentValue` onto the redo stack. Returns null
  // when the undo stack is empty.
  undo(currentValue) {
    if (this.undoStack.length === 0) return null;
    const previous = this.undoStack.pop();
    this.redoStack.push(currentValue);
    return previous;
  }

  // redo is the symmetric pop from the redo stack, pushing `currentValue` onto
  // the undo stack. Returns null when the redo stack is empty.
  redo(currentValue) {
    if (this.redoStack.length === 0) return null;
    const next = this.redoStack.pop();
    this.undoStack.push(currentValue);
    return next;
  }

  reset() {
    this.undoStack = [];
    this.redoStack = [];
    this.applying = false;
  }
}

// =============================================================================
// Auto-save timer — debounces note saves and tracks the dirty flag
// =============================================================================
// The actual save (POST/PUT to the API) lives on the editor host because it
// needs access to currentNote / workspaceId. This class encapsulates the
// timer + dirty state and emits status transitions ('unsaved' / 'saving' /
// 'saved' / 'error') via the onStatusChange callback so the host can keep
// the save-status indicator in sync without owning timer state.

export class NoteAutoSaveTimer {
  constructor({ delayMs = 3000, onFlush, onStatusChange } = {}) {
    this.delayMs = delayMs;
    this.onFlush = onFlush;
    this.onStatusChange = onStatusChange;
    this.timer = null;
    this.dirty = false;
  }

  // schedule arms the timer (replacing any pending one) and marks the editor
  // dirty. After delayMs the onFlush callback runs.
  schedule() {
    this.cancel();
    this.dirty = true;
    this.onStatusChange?.('unsaved');
    this.timer = setTimeout(() => {
      this.timer = null;
      this.onFlush?.();
    }, this.delayMs);
  }

  // flushImmediate cancels the pending timer and runs onFlush right away if
  // dirty. Useful for modal close — don't wait, save now.
  flushImmediate() {
    this.cancel();
    if (this.dirty) this.onFlush?.();
  }

  // cancel stops the pending timer without firing onFlush.
  cancel() {
    if (this.timer) {
      clearTimeout(this.timer);
      this.timer = null;
    }
  }

  // markClean tells the timer the host has successfully saved. Status flips
  // to 'saved' for the indicator.
  markClean() {
    this.dirty = false;
    this.onStatusChange?.('saved');
  }

  // markError tells the timer a save failed. Dirty stays true so a future
  // schedule still fires.
  markError() {
    this.onStatusChange?.('error');
  }

  // markSaving flips the indicator to 'saving' before the host's async work
  // begins. The host calls this from inside its onFlush implementation.
  markSaving() {
    this.onStatusChange?.('saving');
  }

  // reset cancels timers and clears dirty + status. Called when opening a
  // new note so leftover state doesn't bleed across sessions.
  reset() {
    this.cancel();
    this.dirty = false;
    this.onStatusChange?.('saved');
  }

  isDirty() {
    return this.dirty;
  }
}

// =============================================================================
// Vault reference badge — toggles `#noteVaultReferenceBadge` for notes that
// were imported from a private vault.
// =============================================================================

const VAULT_BADGE_ID = 'noteVaultReferenceBadge';
const VAULT_NAME_ID = 'noteVaultReferenceName';

// normalizeVaultReference accepts either snake_case (server shape) or
// camelCase (legacy) inputs and returns a uniform `{ vaultName, recordLabel,
// recordId }` shape. Returns null if `recordId` is missing — callers treat
// that as "no vault reference".
export function normalizeVaultReference(ref) {
  if (!ref || typeof ref !== 'object') return null;
  const normalized = {
    vaultName: String(ref.vault_name || ref.vaultName || '').trim(),
    recordLabel: String(ref.record_label || ref.recordLabel || '').trim(),
    recordId: String(ref.record_id || ref.recordId || '').trim(),
  };
  if (!normalized.recordId) return null;
  return normalized;
}

export function showVaultReferenceBadge(ref) {
  if (typeof document === 'undefined') return;
  const badge = document.getElementById(VAULT_BADGE_ID);
  const nameSpan = document.getElementById(VAULT_NAME_ID);
  if (!badge || !nameSpan) return;

  const normalized = normalizeVaultReference(ref);
  if (!normalized) {
    hideVaultReferenceBadge();
    return;
  }
  const vaultName = normalized.vaultName || 'Private Vault';
  nameSpan.textContent = `From Vault: ${vaultName}`;
  badge.title = normalized.recordLabel
    ? `Vault entry: ${normalized.recordLabel}`
    : 'Imported from a private vault';
  badge.style.display = 'block';
}

export function hideVaultReferenceBadge() {
  if (typeof document === 'undefined') return;
  const badge = document.getElementById(VAULT_BADGE_ID);
  const nameSpan = document.getElementById(VAULT_NAME_ID);
  if (badge) {
    badge.style.display = 'none';
    badge.removeAttribute('title');
  }
  if (nameSpan) nameSpan.textContent = '';
}

// =============================================================================
// Save status — visual indicator in the modal/page footer
// =============================================================================

const SAVE_STATUS_CONTAINER_ID = 'noteSaveStatus';
const SAVE_STATUS_VALUES = new Set(['saved', 'saving', 'unsaved', 'error']);

// updateSaveStatus toggles which `.note-status-{name}` element is visible inside
// `#noteSaveStatus`. Unknown statuses are ignored (caller bug, not crash).
export function updateSaveStatus(status) {
  if (typeof document === 'undefined') return;
  const container = document.getElementById(SAVE_STATUS_CONTAINER_ID);
  if (!container) return;

  container.querySelectorAll('span[class^="note-status-"]').forEach((el) => {
    el.style.display = 'none';
  });
  if (!SAVE_STATUS_VALUES.has(status)) return;
  const target = container.querySelector(`.note-status-${status}`);
  if (target) target.style.display = 'inline-flex';
}

// =============================================================================
// Browser bridge
// =============================================================================

const api = {
  parseHeadingLevel,
  parseTaskLine,
  lineKindClass,
  normalizeCompactTaskListMarkdown,
  getContentValue,
  setContentValue,
  getContentLines,
  setContentLines,
  updateSaveStatus,
  isUndoShortcut,
  isRedoShortcut,
  isPrintableKey,
  normalizeVaultReference,
  showVaultReferenceBadge,
  hideVaultReferenceBadge,
  NoteHistory,
  NoteAutoSaveTimer,
  selectionContains,
  hasTextSelectionInside,
  pointerDragged,
  clearWindowSelection,
  escapeHtml,
  renderEditingLine,
  renderEditingRange,
  renderMarkdown,
  renderMarkdownLine,
  renderInlineMarkdown,
  renderHeadingLine,
  renderTaskLine,
  renderRenderedLine,
};

if (typeof window !== 'undefined') {
  window.NoteEditor = api;
}

export default api;
