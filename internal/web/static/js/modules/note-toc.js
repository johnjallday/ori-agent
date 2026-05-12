// note-toc.js — pure-function helpers for the note Table-of-Contents.
//
// ESM module. Loaded in the browser via <script type="module">; consumed
// from `node --test` via direct `import` (see note-toc.test.js).
// A `window.NoteTOC` global is exposed so the non-module sessions.js can
// reach these helpers without converting itself to a module.
//
// This parser MUST stay in lockstep with the Go parser in
// internal/session/note_headings.go. Both are ATX-only with fenced-code-block
// exclusion (` ``` ` and `~~~`, indented up to 3 spaces). Setext is not
// supported.

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

// buildOutline returns a tree of headings nested by level.
export function buildOutline(markdown) {
  const headings = parseHeadings(markdown);
  const root = { level: 0, children: [] };
  const stack = [root];
  for (const h of headings) {
    while (stack.length > 1 && stack[stack.length - 1].level >= h.level) {
      stack.pop();
    }
    const node = { level: h.level, text: h.text, position: h.position, children: [] };
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
  window.NoteTOC = { parseHeadings, buildOutline, sliceHeadingRange, moveHeadingRange };
}
