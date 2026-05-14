// note-staging.js — pure logic for the AI Assist staging engine.
//
// Inputs: a list of suggestion cards, each carrying a `sourceRange`, `mode`,
// `output`, and `staged` flag. Outputs: a deterministic projection of staged
// hunks (with conflict detection), and an `applyHunks` function that mutates
// source Markdown atomically.
//
// ESM module — used by the browser via window.NoteStaging and tested with
// `node --test`. No DOM access.

// projectHunks turns staged cards into a list of hunks ready for review/commit.
// Conflict detection: two staged hunks conflict iff their sourceRanges overlap
// AND at least one is `replace` mode. Insert-before/after at the same exact
// position do not conflict by themselves.
export function projectHunks(cards) {
  const staged = (cards || []).filter(c => c.staged && c.status === 'ready' && hasValidSourceRange(c.sourceRange));
  const hunks = staged.map(c => ({
    id: c.id,
    suggestionId: c.id,
    sourceRange: { start: c.sourceRange.start, end: c.sourceRange.end },
    mode: c.mode || 'replace',
    output: c.output || '',
    originalText: c.originalText || '',
    action: c.action,
    conflictsWith: [],
  }));

  for (let i = 0; i < hunks.length; i++) {
    for (let j = i + 1; j < hunks.length; j++) {
      if (hunksConflict(hunks[i], hunks[j])) {
        hunks[i].conflictsWith.push(hunks[j].id);
        hunks[j].conflictsWith.push(hunks[i].id);
      }
    }
  }
  return hunks;
}

function hasValidSourceRange(range) {
  return Number.isInteger(range?.start)
    && Number.isInteger(range?.end)
    && range.start <= range.end;
}

function hunksConflict(a, b) {
  const aStart = a.sourceRange.start;
  const aEnd = a.sourceRange.end;
  const bStart = b.sourceRange.start;
  const bEnd = b.sourceRange.end;
  // Overlap test (inclusive at boundaries when both are replace).
  const overlap = Math.max(aStart, bStart) < Math.min(aEnd, bEnd);
  if (!overlap) return false;
  return a.mode === 'replace' || b.mode === 'replace';
}

// applyHunks returns a new source string with all hunks applied. Sorts hunks
// by sourceRange.start descending so earlier offsets stay valid as we splice.
//
// Returns { content, applied: [hunkId], skipped: [{id, reason}] }.
export function applyHunks(source, hunks) {
  const result = { content: source, applied: [], skipped: [] };
  if (!hunks || hunks.length === 0) return result;

  // Sort by start descending; ties broken by mode so insert-before lands first.
  const sorted = [...hunks].sort((a, b) => {
    if (b.sourceRange.start !== a.sourceRange.start) {
      return b.sourceRange.start - a.sourceRange.start;
    }
    // Same start: prefer applying insert-after first (later position) so it
    // doesn't get displaced by an insert-before at the same offset.
    return modeWeight(b.mode) - modeWeight(a.mode);
  });

  let content = source;
  for (const h of sorted) {
    // Stale-range check: if the source no longer matches originalText at the
    // recorded range, fall back to insert-at-original-start.
    const slice = content.slice(h.sourceRange.start, h.sourceRange.end);
    if (h.mode === 'replace' && h.originalText && slice !== h.originalText) {
      const insertAt = clamp(h.sourceRange.start, 0, content.length);
      content = content.slice(0, insertAt) + h.output + '\n\n' + content.slice(insertAt);
      result.applied.push(h.id);
      result.skipped.push({ id: h.id, reason: 'stale-range-fallback-insert' });
      continue;
    }

    if (h.mode === 'replace') {
      content = content.slice(0, h.sourceRange.start) + h.output + content.slice(h.sourceRange.end);
    } else if (h.mode === 'insert-before') {
      const insertAt = clamp(h.sourceRange.start, 0, content.length);
      content = content.slice(0, insertAt) + h.output + '\n\n' + content.slice(insertAt);
    } else if (h.mode === 'insert-after') {
      const insertAt = clamp(h.sourceRange.end, 0, content.length);
      content = content.slice(0, insertAt) + '\n\n' + h.output + content.slice(insertAt);
    } else {
      result.skipped.push({ id: h.id, reason: 'unknown-mode' });
      continue;
    }
    result.applied.push(h.id);
  }
  result.content = content;
  return result;
}

function modeWeight(mode) {
  // Used as a secondary sort key when two hunks share a start offset.
  if (mode === 'insert-before') return 0;
  if (mode === 'replace') return 1;
  if (mode === 'insert-after') return 2;
  return 1;
}

function clamp(n, lo, hi) {
  return Math.max(lo, Math.min(hi, n));
}

// diffLines produces a tiny line-level diff for visual review. Returns an
// array of { kind, text } where kind is 'context' | 'removed' | 'added'.
// Designed to be readable, not optimal — uses naive set-based comparison.
export function diffLines(originalText, replacementText, mode) {
  const out = [];
  const orig = (originalText || '').split('\n');
  const repl = (replacementText || '').split('\n');

  if (mode === 'insert-before' || mode === 'insert-after') {
    for (const line of repl) out.push({ kind: 'added', text: line });
    return out;
  }

  // Replace mode — present as removed-then-added for clarity.
  for (const line of orig) out.push({ kind: 'removed', text: line });
  for (const line of repl) out.push({ kind: 'added', text: line });
  return out;
}

if (typeof window !== 'undefined') {
  window.NoteStaging = { projectHunks, applyHunks, diffLines };
}

export default { projectHunks, applyHunks, diffLines };
