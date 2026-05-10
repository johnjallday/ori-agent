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
};

if (typeof window !== 'undefined') {
  window.NoteEditor = api;
}

export default api;
