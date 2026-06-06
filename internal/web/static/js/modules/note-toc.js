// note-toc.js — pure-function helpers for the note Table-of-Contents.
//
// ESM module. Loaded in the browser via <script type="module">; consumed
// from `node --test` via direct `import` (see note-toc.test.js).
// A `window.NoteTOC` global is exposed so the non-module sessions.js can
// reach these helpers without converting itself to a module.
//
// `parseHeadings` MUST stay in lockstep with the Go parser in
// internal/session/note_headings.go (which feeds cross-note heading SEARCH).
// Both are ATX-only with fenced-code-block exclusion (` ``` ` and `~~~`,
// indented up to 3 spaces). Setext is not supported.
//
// `parseOutlineEntries` / `buildOutline` are an outline-ONLY superset: on top
// of ATX headings they also surface bold-line "sections" and bold-led list
// "items". These are deliberately JS-only — keeping them out of the Go parser
// avoids polluting the heading search index with bold/list noise.

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

function matchATXHeading(line) {
  let i = 0;
  while (i < line.length && line[i] === '#') i++;
  if (i === 0 || i > 6) return null;
  if (i >= line.length) return null;
  if (line[i] !== ' ' && line[i] !== '\t') return null;
  const text = line.slice(i).replace(/^[ \t]+|[ \t]+$/g, '');
  if (!text) return null;
  return { level: i, text };
}

// parseHeadings extracts headings from a Markdown source. Returns an array
// of { level, text, position } where position is the character offset of
// the first '#' on the heading line.
export function parseHeadings(content) {
  if (!content) return [];
  const headings = [];
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
      const h = matchATXHeading(line);
      if (h) {
        headings.push({ level: h.level, text: h.text, position: offset });
      }
    }

    if (nl < 0) break;
    offset = nl + 1;
  }
  return headings;
}

// matchBoldSection matches a line whose entire (trimmed) content is a single
// bold span — `**text**` or `__text__` — and returns the inner text, or null.
// Many notes use a bold line as a de-facto section title instead of a real
// `##` heading, so the outline surfaces these. A line with prose around the
// bold (or two separate bold spans) is NOT a section.
function matchBoldSection(line) {
  const t = line.replace(/^[ \t]+|[ \t]+$/g, '');
  const m = /^\*\*(.+?)\*\*$/.exec(t) || /^__(.+?)__$/.exec(t);
  if (!m) return null;
  const inner = m[1].replace(/^[ \t]+|[ \t]+$/g, '');
  if (!inner || inner.includes('**') || inner.includes('__')) return null;
  return inner;
}

// matchBoldListItem matches a top-level (≤3 leading spaces) ordered or
// unordered list item whose content LEADS with a bold span, e.g.
// `1. **Welcome Bite**` or `- **Item**`. Returns { ordered, marker, text }.
// Only bold-led items are surfaced — plain list items (groceries, checklists)
// would flood the outline, so they're intentionally ignored.
function matchBoldListItem(line) {
  const m = /^ {0,3}(\d+[.)]|[-*+])[ \t]+(?:\*\*(.+?)\*\*|__(.+?)__)/.exec(line);
  if (!m) return null;
  const inner = (m[2] ?? m[3] ?? '').replace(/^[ \t]+|[ \t]+$/g, '');
  if (!inner) return null;
  return { ordered: /\d/.test(m[1]), marker: m[1], text: inner };
}

// parseOutlineEntries extracts outline entries from a Markdown source. Unlike
// parseHeadings (which mirrors the Go parser and feeds heading SEARCH), this
// is outline-only and additionally surfaces bold-line "sections" and bold-led
// list "items" so notes structured with bold/lists still get a useful outline.
//
// Returns an array of { kind, level, text, position } where kind is one of
// 'heading' | 'section' | 'item' and position is the byte offset of the start
// of the entry's source line. Levels nest: a section sits one level under the
// nearest heading; a bold-led item sits one level under the nearest section.
export function parseOutlineEntries(content) {
  if (!content) return [];
  const entries = [];
  let inFence = false;
  let fenceMark = '';
  let fenceCount = 0;
  let offset = 0;
  let headingDepth = 0; // level of the most recent ATX heading
  let sectionLevel = 0; // list items nest one level under this

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
      const h = matchATXHeading(line);
      if (h) {
        entries.push({ kind: 'heading', level: h.level, text: h.text, position: offset });
        headingDepth = h.level;
        sectionLevel = h.level;
      } else {
        const section = matchBoldSection(line);
        if (section) {
          const level = headingDepth + 1;
          entries.push({ kind: 'section', level, text: section, position: offset });
          sectionLevel = level;
        } else {
          const item = matchBoldListItem(line);
          if (item) {
            const level = sectionLevel + 1;
            const text = item.ordered ? `${item.marker} ${item.text}` : item.text;
            entries.push({ kind: 'item', level, text, position: offset });
          }
        }
      }
    }

    if (nl < 0) break;
    offset = nl + 1;
  }
  return entries;
}

// buildOutline returns a tree of outline entries nested by level. Includes
// real headings plus bold-line sections and bold-led list items (see
// parseOutlineEntries). Each node carries its `kind` so the renderer can
// style and gate drag-reorder (only 'heading' nodes are reorderable).
export function buildOutline(markdown) {
  const entries = parseOutlineEntries(markdown);
  const root = { level: 0, children: [] };
  const stack = [root];
  for (const h of entries) {
    while (stack.length > 1 && stack[stack.length - 1].level >= h.level) {
      stack.pop();
    }
    const node = { kind: h.kind, level: h.level, text: h.text, position: h.position, children: [] };
    stack[stack.length - 1].children.push(node);
    stack.push(node);
  }
  return root.children;
}

// sliceHeadingRange returns {start, end} char offsets covering the heading at
// `headingPosition` and all content beneath it, up to (but not including) the
// next heading at the same level or shallower. Returns null if not found.
export function sliceHeadingRange(source, headingPosition) {
  const headings = parseHeadings(source);
  const idx = headings.findIndex(h => h.position === headingPosition);
  if (idx < 0) return null;
  const myLevel = headings[idx].level;
  let end = source.length;
  for (let j = idx + 1; j < headings.length; j++) {
    if (headings[j].level <= myLevel) {
      end = headings[j].position;
      break;
    }
  }
  return { start: headings[idx].position, end };
}

// moveHeadingRange splices the heading-and-children block at `fromPosition`
// out of `source` and re-inserts it at `toPosition` (an offset into the
// ORIGINAL source — the function adjusts for the splice). Returns the new
// source string, or null if the heading wasn't found.
export function moveHeadingRange(source, fromPosition, toPosition) {
  const range = sliceHeadingRange(source, fromPosition);
  if (!range) return null;

  const block = source.slice(range.start, range.end);
  const before = source.slice(0, range.start) + source.slice(range.end);

  let insertAt;
  if (toPosition <= range.start) {
    insertAt = toPosition;
  } else if (toPosition >= range.end) {
    insertAt = toPosition - block.length;
  } else {
    // Dropping inside the block itself is a no-op.
    return source;
  }
  if (insertAt < 0) insertAt = 0;
  if (insertAt > before.length) insertAt = before.length;

  return before.slice(0, insertAt) + block + before.slice(insertAt);
}

// Bridge for non-module scripts (sessions.js).
if (typeof window !== 'undefined') {
  window.NoteTOC = { parseHeadings, parseOutlineEntries, buildOutline, sliceHeadingRange, moveHeadingRange };
}
