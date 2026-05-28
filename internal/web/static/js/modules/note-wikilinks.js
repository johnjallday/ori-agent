// note-wikilinks.js — pure parser + helpers for `[[Target]]` / `[[Target|Display]]`
// references in note Markdown. Must produce identical output to the Go parser
// in internal/session/note_links.go; the shared test fixtures in
// note-wikilinks.test.js and note_links_test.go enforce parity.
//
// ESM module loaded via <script type="module">. Exposed as window.NoteWikilinks
// so the non-module sessions.js can consume it.

import { notePath } from './note-routes.js';

function detectFence(line) {
  let i = 0;
  while (i < 3 && i < line.length && line[i] === ' ') i++;
  if (i >= line.length) return null;
  const mark = line[i];
  if (mark !== '`' && mark !== '~') return null;
  let count = 0;
  while (i < line.length && line[i] === mark) {
    i++;
    count++;
  }
  if (count < 3) return null;
  return { mark, count };
}

// scanLineForWikilinks walks a single line character-by-character (no regex —
// keeps behavior identical to the Go parser).
function scanLineForWikilinks(line) {
  const out = [];
  let i = 0;
  while (i < line.length - 1) {
    if (line[i] !== '[' || line[i + 1] !== '[') {
      i++;
      continue;
    }
    const open = i;
    i += 2;

    // Read target up to `|` or `]]` or another `[` / `]`.
    const targetStart = i;
    let pipeAt = -1;
    let end = -1;
    for (let j = i; j < line.length - 1; j++) {
      const c = line[j];
      if (c === '[' || c === ']') {
        if (c === ']' && line[j + 1] === ']') {
          end = j;
        }
        break;
      }
      if (c === '|' && pipeAt === -1) {
        pipeAt = j;
      }
    }
    if (end < 0) {
      i = open + 1;
      continue;
    }

    let target = '';
    let display = '';
    if (pipeAt >= 0 && pipeAt < end) {
      target = line.slice(targetStart, pipeAt).trim();
      display = line.slice(pipeAt + 1, end).trim();
    } else {
      target = line.slice(targetStart, end).trim();
    }

    if (target !== '') {
      out.push({ target, display, start: open });
    }
    i = end + 2;
  }
  return out;
}

// parseWikilinks extracts `[[…]]` references from `content`, skipping matches
// inside fenced code blocks (` ``` ` and `~~~`). Returns an array of
// { target, display, position } where position is the byte (char) offset of
// the leading `[` in the source.
export function parseWikilinks(content) {
  if (!content) return [];
  const out = [];
  let inFence = false;
  let fenceMark = '';
  let fenceCount = 0;
  let offset = 0;

  while (true) {
    const nl = content.indexOf('\n', offset);
    const line = nl < 0 ? content.slice(offset) : content.slice(offset, nl);

    const fence = detectFence(line);
    if (fence) {
      if (!inFence) {
        inFence = true;
        fenceMark = fence.mark;
        fenceCount = fence.count;
      } else if (fence.mark === fenceMark && fence.count >= fenceCount) {
        inFence = false;
      }
    } else if (!inFence) {
      for (const m of scanLineForWikilinks(line)) {
        out.push({
          target: m.target,
          display: m.display,
          position: offset + m.start,
        });
      }
    }

    if (nl < 0) break;
    offset = nl + 1;
  }
  return out;
}

// renderWikilinkHTML returns an `<a class="note-wikilink">` element string for
// a parsed wikilink. `isBroken` switches to the broken-link styling and
// disables navigation (the click handler will offer to create the note).
export function renderWikilinkHTML(target, display, isBroken = false) {
  const label = display || target;
  const cls = isBroken ? 'note-wikilink note-wikilink-broken' : 'note-wikilink';
  // Compose the title with raw target, then escape the whole string so the
  // surrounding quotes don't break out of the attribute.
  const title = escapeAttr(isBroken ? `Click to create "${target}"` : target);
  return `<a href="#" class="${cls}" data-wikilink-target="${escapeAttr(target)}" title="${title}">${escapeText(label)}</a>`;
}

function escapeAttr(s) {
  return String(s ?? '').replace(/[&<>"']/g, (c) => (
    { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]
  ));
}
function escapeText(s) {
  return String(s ?? '').replace(/[&<>]/g, (c) => (
    { '&': '&amp;', '<': '&lt;', '>': '&gt;' }[c]
  ));
}

// applyWikilinksToHtml takes Markdown-rendered HTML and rewrites any `[[…]]`
// occurrences in text nodes into <a class="note-wikilink"> elements. The
// `resolveTarget(target) → noteId | null` callback decides whether each link
// is broken; callers typically resolve against a workspace's note index.
//
// This works on the rendered HTML (after marked.js) because the rendered HTML
// preserves `[[…]]` in text-content positions (marked doesn't recognize it as
// syntax). Operating on the post-render HTML lets us live inside other
// inline elements (bold, italics, list items) without re-implementing those.
export function applyWikilinksToHtml(html, resolveTarget) {
  if (!html || typeof html !== 'string') return html;
  // Fast bail when there are no `[[` at all.
  if (html.indexOf('[[') < 0) return html;
  // Replace `[[Target|Display]]` and `[[Target]]` outside HTML tags. The
  // regex isn't bulletproof against tag attributes containing `[[`, but in
  // our render pipeline the only place `[[` appears is in text content.
  return html.replace(/\[\[([^[\]|]+?)(?:\|([^[\]]+?))?\]\]/g, (_, rawTarget, rawDisplay) => {
    const target = String(rawTarget || '').trim();
    if (!target) return '';
    const display = String(rawDisplay || '').trim();
    let isBroken = true;
    if (typeof resolveTarget === 'function') {
      const id = resolveTarget(target);
      isBroken = !id;
    }
    return renderWikilinkHTML(target, display, isBroken);
  });
}

// =============================================================================
// Click handling — resolve a clicked wikilink against the workspace and navigate
// =============================================================================
// The host (modal sessionManager + page bootstrap) registers a workspace
// resolver via setWorkspaceContext(getWorkspaceId). The delegated click handler
// reads the clicked link's data-wikilink-target, asks the host for the current
// workspace, and looks the target up against /api/workspaces/{id}/notes (exact
// title, then case-insensitive). In page contexts, the host opens the note
// itself; otherwise we navigate to the focused note URL.

let _getWorkspaceId = () => null;

export function setWorkspaceContext(fn) {
  if (typeof fn === 'function') _getWorkspaceId = fn;
}

let _workspaceNotesCache = new Map(); // workspaceId → notes[]

function resolveTarget(target, notes) {
  const exact = notes.find((n) => n.name === target);
  if (exact) return exact;
  const lc = target.toLowerCase();
  return notes.find((n) => (n.name || '').toLowerCase() === lc) || null;
}

// invalidateNotesCache drops the cached note list for a workspace so the next
// wikilink resolution refetches. Call after creating a note (e.g. the
// "Extract → note" action) so a just-made link isn't mistaken for broken.
function invalidateNotesCache(workspaceId) {
  if (workspaceId == null) _workspaceNotesCache = new Map();
  else _workspaceNotesCache.delete(workspaceId);
}

async function fetchWorkspaceNotes(workspaceId) {
  if (_workspaceNotesCache.has(workspaceId)) return _workspaceNotesCache.get(workspaceId);
  try {
    const r = await fetch(`/api/workspaces/${encodeURIComponent(workspaceId)}/notes`);
    if (!r.ok) return [];
    const data = await r.json();
    const notes = data?.notes || [];
    _workspaceNotesCache.set(workspaceId, notes);
    // Invalidate after a short window so newly-created notes show up.
    setTimeout(() => _workspaceNotesCache.delete(workspaceId), 5000);
    return notes;
  } catch (_) {
    return [];
  }
}

async function createNoteWithName(workspaceId, name) {
  try {
    const r = await fetch(`/api/workspaces/${encodeURIComponent(workspaceId)}/notes`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ name, content: '' }),
    });
    if (!r.ok) return null;
    const data = await r.json();
    _workspaceNotesCache.delete(workspaceId);
    return data?.note || data;
  } catch (_) {
    return null;
  }
}

function installClickHandler() {
  if (typeof document === 'undefined' || installClickHandler._installed) return;
  installClickHandler._installed = true;

  document.addEventListener('click', async (event) => {
    const link = event.target?.closest?.('.note-wikilink');
    if (!link) return;
    event.preventDefault();
    event.stopPropagation();

    const target = link.dataset.wikilinkTarget;
    if (!target) return;

    const workspaceId = _getWorkspaceId();
    if (!workspaceId) {
      // No workspace context yet — quietly do nothing. The host should
      // register a resolver when it knows which workspace is active.
      return;
    }

    const notes = await fetchWorkspaceNotes(workspaceId);
    const match = resolveTarget(target, notes);
    if (match?.id) {
      // Prefer opening as a new tab on the page; fall back to navigation
      // (modal context, or NotePage not yet ready).
      if (typeof window.NotePage?.openNoteInTab === 'function') {
        window.NotePage.openNoteInTab(match.id);
      } else {
        window.location.href = notePath(match.id);
      }
      return;
    }

    // Broken link — offer to create.
    if (!window.confirm(`Create new note "${target}" in this workspace?`)) return;
    const created = await createNoteWithName(workspaceId, target);
    if (created?.id) {
      if (typeof window.NotePage?.openNoteInTab === 'function') {
        window.NotePage.openNoteInTab(created.id);
      } else {
        window.location.href = notePath(created.id);
      }
    }
  });
}

if (typeof document !== 'undefined') {
  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', installClickHandler);
  } else {
    installClickHandler();
  }
}

if (typeof window !== 'undefined') {
  window.NoteWikilinks = {
    parseWikilinks,
    renderWikilinkHTML,
    applyWikilinksToHtml,
    setWorkspaceContext,
    invalidateNotesCache,
  };
}

export default {
  parseWikilinks,
  renderWikilinkHTML,
  applyWikilinksToHtml,
  setWorkspaceContext,
  invalidateNotesCache,
};

export { invalidateNotesCache };
