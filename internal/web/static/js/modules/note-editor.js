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
// TOC navigation helpers — locate / scroll / mark-active
// =============================================================================
// The live-preview pane renders one DOM element per source-Markdown line, with
// each carrying `data-line-index`. These helpers map between source positions
// (byte offsets in the textarea content) and the rendered elements so the TOC
// rail can scroll the preview into view and highlight the active section.

const PREVIEW_PANE_ID = 'notePreviewContent';
const TOC_RAIL_ID = 'noteTocRail';

// lineIndexAtPosition returns the 0-based line number containing source byte
// offset `position` within `content`. Used to translate a heading's source
// position into the line-index attribute that rendered DOM elements carry.
export function lineIndexAtPosition(content, position) {
  let lineIndex = 0;
  let cursor = 0;
  while (cursor < content.length && cursor < position) {
    const nl = content.indexOf('\n', cursor);
    if (nl < 0 || nl >= position) break;
    cursor = nl + 1;
    lineIndex++;
  }
  return lineIndex;
}

// startOfLine returns the byte offset of the first character on line
// `lineIndex` (0-based) within `content`. Used to map back from line index
// to source position when wiring the TOC rail's active-section indicator.
export function startOfLine(content, lineIndex) {
  let cursor = 0;
  let line = 0;
  while (line < Number(lineIndex) && cursor < content.length) {
    const nl = content.indexOf('\n', cursor);
    if (nl < 0) break;
    cursor = nl + 1;
    line++;
  }
  return cursor;
}

// findRenderedHeadingByPosition locates the rendered live-preview element for
// the heading at source byte offset `position`. Returns null when the live
// preview isn't mounted or no rendered line matches.
export function findRenderedHeadingByPosition(content, position) {
  if (typeof document === 'undefined') return null;
  const previewPane = document.getElementById(PREVIEW_PANE_ID);
  if (!previewPane) return null;
  const lineIndex = lineIndexAtPosition(content, position);
  return previewPane.querySelector(`.note-live-line-rendered[data-line-index="${lineIndex}"]`);
}

// scrollToHeadingPosition smooth-scrolls the preview pane so the heading at
// `position` sits at the top of the visible area. No-op when the preview
// isn't mounted or the heading isn't rendered.
export function scrollToHeadingPosition(content, position) {
  const target = findRenderedHeadingByPosition(content, position);
  target?.scrollIntoView({ behavior: 'smooth', block: 'start' });
}

// setActiveTocEntry marks the TOC rail entry corresponding to `lineIndex` as
// the currently-active section (sets `aria-current="location"` and the
// `.is-active` class). Clears the marker on every other entry first.
export function setActiveTocEntry(lineIndex, content) {
  if (typeof document === 'undefined' || lineIndex == null) return;
  const rail = document.getElementById(TOC_RAIL_ID);
  if (!rail) return;
  const cursor = startOfLine(content, lineIndex);
  const target = rail.querySelector(`[data-position="${cursor}"]`);
  rail.querySelectorAll('.note-toc-item').forEach((el) => {
    el.removeAttribute('aria-current');
    el.classList.remove('is-active');
  });
  if (target) {
    target.setAttribute('aria-current', 'location');
    target.classList.add('is-active');
  }
}

// =============================================================================
// NoteTocController — state container for the TOC rail
// =============================================================================
// Owns the IntersectionObserver, the debounce timer, and the drag-source
// position. The host (modal or page) supplies callbacks for content I/O,
// preview-mode probing, undo/save, and re-rendering the live preview after
// drag-reorder mutates the source. Pure rendering helpers (showRail/hideRail,
// scrollToHeadingPosition, setActiveTocEntry) are imported from this same
// module — keeps the controller small and testable.

export class NoteTocController {
  constructor(host = {}) {
    // host: { getContent(), setContent(value), isPreviewMode(), pushUndo(),
    //         scheduleAutoSave(), render() }
    this.host = host;
    this.observer = null;
    this.rebuildTimer = null;
    this.dragSource = null;
  }

  scheduleRebuild() {
    if (this.rebuildTimer) clearTimeout(this.rebuildTimer);
    this.rebuildTimer = setTimeout(() => {
      this.rebuildTimer = null;
      this.rebuild();
    }, 250);
  }

  rebuild() {
    if (typeof document === 'undefined') return;
    if (!this.host.isPreviewMode?.()) {
      hideRail('toc');
      this.detachObserver();
      return;
    }
    if (typeof window === 'undefined' || !window.NoteTOC) return;
    const rail = document.getElementById(TOC_RAIL_ID);
    if (!rail) return;

    const empty = rail.querySelector('[data-role="empty"]');
    const content = rail.querySelector('[data-role="content"]');
    if (!empty || !content) return;

    const outline = window.NoteTOC.buildOutline(this.host.getContent?.() || '');
    const flat = [];
    const flatten = (nodes) => {
      for (const n of nodes) {
        flat.push(n);
        if (n.children?.length) flatten(n.children);
      }
    };
    flatten(outline);

    showRail('toc');
    if (flat.length === 0) {
      empty.style.display = '';
      content.style.display = 'none';
      content.innerHTML = '';
      this.detachObserver();
      return;
    }
    empty.style.display = 'none';
    content.style.display = '';

    const list = document.createElement('ul');
    list.className = 'note-toc-list';
    for (const h of flat) {
      const li = document.createElement('li');
      li.className = 'note-toc-item';
      li.dataset.level = String(h.level);
      li.dataset.position = String(h.position);

      const btn = document.createElement('button');
      btn.type = 'button';
      btn.className = 'note-toc-entry';
      btn.style.paddingLeft = `${8 + (h.level - 1) * 12}px`;
      btn.textContent = h.text;
      btn.title = h.text;
      btn.draggable = true;
      btn.addEventListener('click', () => scrollToHeadingPosition(this.host.getContent?.() || '', h.position));
      btn.addEventListener('dragstart', (e) => this._onDragStart(e, h.position));
      btn.addEventListener('dragend', () => this._onDragEnd());
      btn.addEventListener('dragover', (e) => this._onDragOver(e, h.position));
      btn.addEventListener('drop', (e) => this._onDrop(e, h.position));

      li.appendChild(btn);
      list.appendChild(li);
    }
    content.replaceChildren(list);

    this.attachObserver();
  }

  attachObserver() {
    this.detachObserver();
    if (typeof IntersectionObserver === 'undefined' || typeof document === 'undefined') return;
    const previewPane = document.getElementById(PREVIEW_PANE_ID);
    if (!previewPane) return;
    const headingEls = previewPane.querySelectorAll('.note-live-line-rendered[class*="is-heading-"]');
    if (!headingEls.length) return;

    const visible = new Map();
    const observer = new IntersectionObserver((entries) => {
      for (const entry of entries) {
        if (entry.isIntersecting) visible.set(entry.target, entry.intersectionRatio);
        else visible.delete(entry.target);
      }
      let top = null;
      let topY = Infinity;
      for (const el of visible.keys()) {
        const rect = el.getBoundingClientRect();
        if (rect.top < topY) { topY = rect.top; top = el; }
      }
      if (top) setActiveTocEntry(top.dataset.lineIndex, this.host.getContent?.() || '');
    }, { root: previewPane, rootMargin: '-10% 0px -85% 0px', threshold: [0, 0.1, 0.5] });

    headingEls.forEach((el) => observer.observe(el));
    this.observer = observer;
  }

  detachObserver() {
    if (this.observer) {
      this.observer.disconnect();
      this.observer = null;
    }
  }

  destroy() {
    if (this.rebuildTimer) clearTimeout(this.rebuildTimer);
    this.rebuildTimer = null;
    this.detachObserver();
    this.dragSource = null;
  }

  _onDragStart(e, position) {
    if (!e.dataTransfer) return;
    e.dataTransfer.effectAllowed = 'move';
    e.dataTransfer.setData('text/plain', String(position));
    this.dragSource = position;
    e.currentTarget.closest('.note-toc-item')?.classList.add('is-dragging');
  }

  _onDragOver(e, position) {
    if (this.dragSource == null || this.dragSource === position) return;
    e.preventDefault();
    if (e.dataTransfer) e.dataTransfer.dropEffect = 'move';
    const item = e.currentTarget.closest('.note-toc-item');
    if (!item) return;
    if (typeof document !== 'undefined') {
      document.querySelectorAll('.note-toc-item.is-drop-target')
        .forEach((el) => el.classList.remove('is-drop-target'));
    }
    item.classList.add('is-drop-target');
  }

  _onDragEnd() {
    if (typeof document !== 'undefined') {
      document.querySelectorAll('.note-toc-item.is-dragging, .note-toc-item.is-drop-target')
        .forEach((el) => el.classList.remove('is-dragging', 'is-drop-target'));
    }
    this.dragSource = null;
  }

  _onDrop(e, targetPosition) {
    e.preventDefault();
    const source = this.dragSource;
    this._onDragEnd();
    if (source == null || source === targetPosition) return;
    if (typeof window === 'undefined' || !window.NoteTOC) return;
    const current = this.host.getContent?.() || '';
    const next = window.NoteTOC.moveHeadingRange(current, source, targetPosition);
    if (next == null || next === current) return;
    this.host.pushUndo?.();
    this.host.setContent?.(next);
    this.host.scheduleAutoSave?.();
    this.host.render?.();
    this.rebuild();
  }
}

// =============================================================================
// Generate-with-AI panel — visibility + rail "generating" mode coordination
// =============================================================================

const GEN_PANEL_ID = 'noteAIGeneratePanel';
const GEN_TOGGLE_ID = 'noteGenerateAIToggle';
const ASSIST_RAIL_ID = 'noteAssistRail';

// Tracks whether the Generate panel is currently the active rail mode so
// closeGeneratePanel knows to delegate to NoteAIAssist.render() afterward.
let _generatePanelActive = false;

function _genPanelEl() {
  return typeof document !== 'undefined' ? document.getElementById(GEN_PANEL_ID) : null;
}
function _genToggleEl() {
  return typeof document !== 'undefined' ? document.getElementById(GEN_TOGGLE_ID) : null;
}

// isGeneratePanelOpen reports whether the panel is currently visible.
export function isGeneratePanelOpen() {
  const panel = _genPanelEl();
  if (!panel) return false;
  return panel.style.display !== 'none' && panel.style.display !== '';
}

// openGeneratePanel reveals the Generate-with-AI form and sets the rail to
// "generating" mode (which hides the suggestion stack so the form is the
// only visible rail content).
export function openGeneratePanel() {
  const panel = _genPanelEl();
  if (!panel) return;
  panel.style.display = 'block';
  _genToggleEl()?.classList.add('ai-active');
  showRail('assist');
  if (typeof document !== 'undefined') {
    const rail = document.getElementById(ASSIST_RAIL_ID);
    if (rail) rail.classList.add('is-generating');
  }
  _generatePanelActive = true;
}

// closeGeneratePanel hides the panel, clears its inputs, and restores the
// rail to whatever the AI Assist module wants (cards, empty state, or
// hidden entirely).
export function closeGeneratePanel() {
  if (typeof document === 'undefined') return;
  const panel = _genPanelEl();
  const toggle = _genToggleEl();
  const promptInput = document.getElementById('noteAIPromptInput');
  const errorDiv = document.getElementById('noteAIError');
  const generatingDiv = document.getElementById('noteAIGenerating');
  const generateBtn = document.getElementById('noteAIGenerateBtn');

  if (panel) panel.style.display = 'none';
  if (toggle) toggle.classList.remove('ai-active');
  if (promptInput) promptInput.value = '';
  if (errorDiv) errorDiv.style.display = 'none';
  if (generatingDiv) generatingDiv.style.display = 'none';
  if (generateBtn) generateBtn.disabled = false;

  const rail = document.getElementById(ASSIST_RAIL_ID);
  if (rail) rail.classList.remove('is-generating');
  if (!_generatePanelActive) return;
  _generatePanelActive = false;
  // Let AI Assist decide whether the rail should now show cards or hide.
  if (typeof window !== 'undefined') window.NoteAIAssist?.render?.();
}

// toggleGeneratePanel flips open ↔ closed.
export function toggleGeneratePanel() {
  if (isGeneratePanelOpen()) closeGeneratePanel();
  else openGeneratePanel();
}

// openGeneratePanelByDefault is the modal-open hook — opens the panel if
// it's currently closed, no-op if already open. Called once per modal open
// so the Generate form is the user's primary entry point for AI work.
export function openGeneratePanelByDefault() {
  if (!isGeneratePanelOpen()) openGeneratePanel();
}

// =============================================================================
// Agent dropdown — populates `#noteAIAgentSelect` from /api/agents
// =============================================================================

const AGENT_SELECT_ID = 'noteAIAgentSelect';

// loadAgentsIntoDropdown fetches the agent list from /api/agents and rebuilds
// the editor's agent <select>. Logs but does not throw on network failure so
// modal-open paths don't have to handle errors. Returns a promise that
// resolves when the dropdown is ready (or empty).
export async function loadAgentsIntoDropdown() {
  if (typeof document === 'undefined') return;
  const select = document.getElementById(AGENT_SELECT_ID);
  if (!select) return;
  try {
    const response = await fetch('/api/agents');
    if (!response.ok) throw new Error('Failed to load agents');
    const data = await response.json();
    select.innerHTML = '<option value="">Select an agent...</option>';
    const agents = data.agents || [];
    agents.forEach((agent) => {
      const option = document.createElement('option');
      option.value = agent.name;
      option.textContent = agent.name;
      select.appendChild(option);
    });
  } catch (error) {
    // eslint-disable-next-line no-console
    console.error('Failed to load agents for note AI:', error);
  }
}

// getSelectedAgentId returns the currently-selected agent name from
// `#noteAIAgentSelect`, or null if the dropdown is empty / unselected.
export function getSelectedAgentId() {
  if (typeof document === 'undefined') return null;
  const select = document.getElementById(AGENT_SELECT_ID);
  return select?.value || null;
}

// =============================================================================
// Selection tracking — for the AI Assist floating action bar
// =============================================================================
// readSelection inspects the editor surface and returns a structured
// description of the user's current text selection (or null if nothing is
// selected). The result feeds NoteAIAssist.onSelectionChanged so the action
// bar knows when to appear and where.
//
// The function handles both editor modes:
//   - Plain-edit (textarea): selectionStart/selectionEnd directly map to
//     source-markdown offsets.
//   - Live-preview (rendered DOM): uses window.getSelection(), then maps
//     the selected text back to a source range via string.indexOf as a
//     cheap heuristic. Good enough until v2 range-stability lands.
//
// Returns { text, source, range: { start, end } | null, anchorRect } or null.

export function readSelection({ getContent, isPreviewMode, textareaId = 'noteContentInput', previewPaneId = PREVIEW_PANE_ID } = {}) {
  if (typeof document === 'undefined' || typeof window === 'undefined') return null;

  if (!isPreviewMode?.()) {
    const ta = document.getElementById(textareaId);
    if (!ta || document.activeElement !== ta) return null;
    const start = ta.selectionStart;
    const end = ta.selectionEnd;
    if (start === end) return null;
    const text = ta.value.slice(start, end);
    const rect = ta.getBoundingClientRect();
    return {
      text,
      source: 'textarea',
      range: { start, end },
      // Caret position inside a textarea isn't trivially available without
      // canvas measurement, so anchor the bar near the textarea's top-right.
      anchorRect: { top: rect.top, bottom: rect.top + 24, left: rect.right - 320, right: rect.right },
    };
  }

  const sel = window.getSelection();
  if (!sel || sel.rangeCount === 0 || sel.isCollapsed) return null;
  const previewPane = document.getElementById(previewPaneId);
  if (!previewPane) return null;
  const range = sel.getRangeAt(0);
  if (!previewPane.contains(range.commonAncestorContainer)) return null;
  const text = sel.toString();
  if (!text || !text.trim()) return null;
  const anchorRect = range.getBoundingClientRect();

  const content = getContent?.() || '';
  const idx = content.indexOf(text);
  const sourceRange = idx >= 0 ? { start: idx, end: idx + text.length } : null;

  return { text, source: 'preview', range: sourceRange, anchorRect };
}

let _selectionTrackingWired = false;

// wireSelectionTracking installs the listeners that drive the AI Assist
// action bar's visibility. Idempotent — safe to call multiple times.
// `onChange` is invoked on every selectionchange / keyup / mouseup; the
// caller typically reads readSelection() inside it and forwards the result
// to NoteAIAssist.onSelectionChanged.
export function wireSelectionTracking({ onChange, textareaId = 'noteContentInput' } = {}) {
  if (_selectionTrackingWired || typeof document === 'undefined') return;
  const update = () => { try { onChange?.(); } catch (_) { /* ignore */ } };
  document.addEventListener('selectionchange', update);
  const ta = document.getElementById(textareaId);
  ta?.addEventListener('select', update);
  ta?.addEventListener('keyup', update);
  ta?.addEventListener('mouseup', update);
  // Esc dismisses the action bar without changing the selection.
  document.addEventListener('keydown', (e) => {
    if (e.key === 'Escape' && typeof window !== 'undefined') window.NoteAIAssist?.hideBar();
  });
  _selectionTrackingWired = true;
}

let _aiAssistInitialized = false;

// initAIAssist wires up the NoteAIAssist sidebar against the editor surface.
// One-shot — subsequent calls are no-ops. The host supplies callbacks that
// connect the assist sidebar to the rest of the editor (content I/O, render,
// undo/save, toast, selection-read).
export function initAIAssist(host = {}) {
  if (_aiAssistInitialized) return;
  if (typeof window === 'undefined' || !window.NoteAIAssist || typeof document === 'undefined') return;
  const bar = document.getElementById('noteAIActionBar');
  const rail = document.getElementById(ASSIST_RAIL_ID);
  if (!bar || !rail) return;

  window.NoteAIAssist.init({
    bar,
    rail,
    sessionsApi: {
      getNoteContent: () => host.getContent?.() || '',
      setNoteContent: (value) => {
        host.setContent?.(value);
        if (host.isPreviewMode?.()) host.render?.();
        host.scheduleTocRebuild?.();
      },
      pushUndo: () => host.pushUndo?.(),
      scheduleAutoSave: () => host.scheduleAutoSave?.(),
      showToast: (msg, kind) => host.showToast?.(msg, kind),
      showAssistRail: () => showRail('assist'),
      hideAssistRail: () => hideRail('assist'),
    },
  });

  wireSelectionTracking({
    onChange: () => window.NoteAIAssist?.onSelectionChanged(host.readSelection?.()),
  });
  wireAgentChangeHandler();
  loadAgentsIntoDropdown();
  _aiAssistInitialized = true;
}

let _agentChangeWired = false;

// wireAgentChangeHandler attaches a `change` listener to the agent dropdown
// that forwards the new selection to NoteAIAssist. Idempotent.
export function wireAgentChangeHandler() {
  if (_agentChangeWired || typeof document === 'undefined') return;
  document.getElementById(AGENT_SELECT_ID)?.addEventListener('change', () => {
    if (typeof window !== 'undefined') {
      window.NoteAIAssist?.onAgentChanged(getSelectedAgentId());
    }
  });
  _agentChangeWired = true;
}

// applyAgentDefaultForWorkspace picks the agent that should be preselected
// when a note in `workspaceId` opens. Order of preference:
//   1. The workspace's `entry_agent_name`, if it exists in the dropdown.
//   2. The first non-placeholder option, if no agent is currently selected.
// Either way, NoteAIAssist.onAgentChanged is notified at the end so the
// inline AI surface reflects the choice. Network failures fall back silently
// to the first-available rule.
export async function applyAgentDefaultForWorkspace(workspaceId) {
  await loadAgentsIntoDropdown();
  if (typeof document === 'undefined') return;
  const select = document.getElementById(AGENT_SELECT_ID);
  if (!select) return;

  let entryAgent = null;
  if (workspaceId) {
    try {
      const r = await fetch(`/api/workspaces/${encodeURIComponent(workspaceId)}`);
      if (r.ok) {
        const data = await r.json();
        entryAgent = data?.entry_agent_name || data?.workspace?.entry_agent_name || null;
      }
    } catch (_) {
      // network error — fall through to first-available below
    }
  }

  if (entryAgent) {
    const match = Array.from(select.options).find((o) => o.value === entryAgent);
    if (match) {
      select.value = entryAgent;
      if (typeof window !== 'undefined') window.NoteAIAssist?.onAgentChanged(entryAgent);
      return;
    }
  }

  if (!select.value) {
    const first = Array.from(select.options).find((o) => o.value);
    if (first) select.value = first.value;
  }
  if (typeof window !== 'undefined') {
    window.NoteAIAssist?.onAgentChanged(getSelectedAgentId());
  }
}

// =============================================================================
// Rails — left (TOC) and right (AI Assist) sidebar visibility + collapse state
// =============================================================================
// Each rail has two boolean modes the host can toggle independently:
//   1. Hidden vs shown (the rail and its toolbar toggle button) — driven by
//      whether the surrounding feature has anything to display.
//   2. Collapsed vs expanded (when shown) — user preference persisted to
//      localStorage so the choice survives reloads.

const RAIL_CONFIG = {
  toc: { railId: 'noteTocRail', toggleId: 'noteTocToggle', storageKey: 'note.toc.collapsed' },
  assist: { railId: 'noteAssistRail', toggleId: 'noteAssistToggle', storageKey: 'note.aiAssist.collapsed' },
};

function _railCfg(name) { return RAIL_CONFIG[name] || null; }

export function getRailCollapsed(name) {
  const cfg = _railCfg(name);
  if (!cfg || typeof localStorage === 'undefined') return false;
  try {
    return localStorage.getItem(cfg.storageKey) === '1';
  } catch (_) {
    return false;
  }
}

export function setRailCollapsed(name, collapsed) {
  const cfg = _railCfg(name);
  if (!cfg || typeof localStorage === 'undefined') return;
  try {
    localStorage.setItem(cfg.storageKey, collapsed ? '1' : '0');
  } catch (_) {
    // ignore quota / privacy-mode failures
  }
}

// applyRailCollapsed reads the persisted bool and reflects it onto the DOM
// (rail's `data-collapsed` attribute and the toggle button's `aria-pressed`).
export function applyRailCollapsed(name) {
  const cfg = _railCfg(name);
  if (!cfg || typeof document === 'undefined') return;
  const el = document.getElementById(cfg.railId);
  if (!el) return;
  const collapsed = getRailCollapsed(name);
  el.dataset.collapsed = collapsed ? 'true' : 'false';
  const btn = document.getElementById(cfg.toggleId);
  if (btn) btn.setAttribute('aria-pressed', collapsed ? 'true' : 'false');
}

// applyAllRailState refreshes both rails — host calls this when opening a
// note so the layout doesn't flash before persisted state takes effect.
export function applyAllRailState() {
  applyRailCollapsed('toc');
  applyRailCollapsed('assist');
}

// toggleRail flips the collapsed state and re-applies it to the DOM.
export function toggleRail(name) {
  setRailCollapsed(name, !getRailCollapsed(name));
  applyRailCollapsed(name);
}

// showRail unhides the rail + its toolbar toggle, then re-applies the
// persisted collapse state. Hosts call this when the rail's feature has
// content (e.g., TOC has at least one heading; AI Assist has a card).
export function showRail(name) {
  const cfg = _railCfg(name);
  if (!cfg || typeof document === 'undefined') return;
  const el = document.getElementById(cfg.railId);
  const btn = document.getElementById(cfg.toggleId);
  if (el) el.hidden = false;
  if (btn) btn.hidden = false;
  applyRailCollapsed(name);
}

export function hideRail(name) {
  const cfg = _railCfg(name);
  if (!cfg || typeof document === 'undefined') return;
  const el = document.getElementById(cfg.railId);
  const btn = document.getElementById(cfg.toggleId);
  if (el) el.hidden = true;
  if (btn) btn.hidden = true;
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
  getRailCollapsed,
  setRailCollapsed,
  applyRailCollapsed,
  applyAllRailState,
  toggleRail,
  showRail,
  hideRail,
  loadAgentsIntoDropdown,
  getSelectedAgentId,
  applyAgentDefaultForWorkspace,
  readSelection,
  wireSelectionTracking,
  wireAgentChangeHandler,
  initAIAssist,
  isGeneratePanelOpen,
  openGeneratePanel,
  closeGeneratePanel,
  toggleGeneratePanel,
  openGeneratePanelByDefault,
  findRenderedHeadingByPosition,
  scrollToHeadingPosition,
  setActiveTocEntry,
  lineIndexAtPosition,
  startOfLine,
  NoteTocController,
};

if (typeof window !== 'undefined') {
  window.NoteEditor = api;
}

export default api;
