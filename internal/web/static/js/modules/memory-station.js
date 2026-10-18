// memory-station.js — the facts the Memory station needs about a workspace's
// MEMORY.md: what kind each entry is, what its short status is called on the
// map and in the Stations rail, the per-browser "seen" baseline behind "+K
// new", and what a run's memory_diff artifact says. Pure helpers only (no DOM,
// no network; storage is passed in), so memory-station.test.js can exercise
// every badge row under plain Node, and the panel manager and the run page
// read the same rules.

export const MEMORY_STATION = 'memory';

const SEEN_KEY_PREFIX = 'ori:memory-station-seen:';

export function plural(count, one, many) {
  return count + ' ' + (count === 1 ? one : many);
}

// memoryEntryKind: "managed" is a reviewed global-memory line (read-only in
// the panel), "global_copy" was copied from global memory, everything else is
// "ordinary".
export function memoryEntryKind(entry) {
  const provenance = String(entry?.provenance || '').trim();
  if (entry?.managed || provenance.startsWith('ori-hq:')) return 'managed';
  if (provenance.startsWith('global:')) return 'global_copy';
  return 'ordinary';
}

// droppedEntryCount is how many of the workspace's own entries the prompt
// budget leaves out (`in_prompt: false`, the server's own selection). Reviewed
// global-memory lines are never part of that section, so they never count.
export function droppedEntryCount(entries) {
  return (Array.isArray(entries) ? entries : []).filter(
    entry => memoryEntryKind(entry) !== 'managed' && entry?.in_prompt === false
  ).length;
}

// notShownToAgents is the one sentence the badge and the panel's meter share
// for a dropped count, so the two can never say it differently.
export function notShownToAgents(count) {
  return plural(count, 'entry is', 'entries are') + ' not shown to agents';
}

// memoryDiffCounts reads a run's memory_diff artifact: the lines it added to
// and removed from workspace memory, or empty lists when the run wrote none.
export function memoryDiffCounts(artifacts) {
  const list = Array.isArray(artifacts) ? artifacts : [];
  const diff = list.find(artifact => artifact?.metadata?.role === 'memory_diff');
  return {
    added: Array.isArray(diff?.metadata?.added) ? diff.metadata.added : [],
    removed: Array.isArray(diff?.metadata?.removed) ? diff.metadata.removed : []
  };
}

// memoryStationSummary reduces a GET /api/workspaces/{id}/memory payload to
// what the badge reads. Reviewed HQ learnings are never counted: they are not
// part of the workspace's own prompt section.
export function memoryStationSummary(payload) {
  const entries = Array.isArray(payload?.entries) ? payload.entries : [];
  const own = entries.filter(entry => memoryEntryKind(entry) !== 'managed');
  return {
    count: own.length,
    watching: own.filter(entry => entry?.type === 'watch' || entry?.type === 'thread').length,
    dropped: droppedEntryCount(own),
    overBudget: Boolean(payload?.over_budget)
  };
}

export function memoryStationSeenKey(workspaceID) {
  return SEEN_KEY_PREFIX + String(workspaceID || '');
}

// readMemoryStationBaseline returns the { count } stored when the panel was
// last opened in this browser, or null when there is none or the storage
// cannot be read.
export function readMemoryStationBaseline(storage, workspaceID) {
  if (!workspaceID || !storage) return null;
  try {
    const raw = storage.getItem(memoryStationSeenKey(workspaceID));
    if (!raw) return null;
    const count = Number(JSON.parse(raw)?.count);
    if (!Number.isInteger(count) || count < 0) return null;
    return { count };
  } catch (_) {
    return null;
  }
}

// writeMemoryStationBaseline records what the user has now seen. A storage
// that throws (private window, blocked site data) just keeps no baseline.
export function writeMemoryStationBaseline(storage, workspaceID, summary) {
  if (!workspaceID || !storage || !summary) return false;
  try {
    storage.setItem(memoryStationSeenKey(workspaceID), JSON.stringify({ count: summary.count }));
    return true;
  } catch (_) {
    return false;
  }
}

// memoryNewSinceBaseline is K in "+K new": how many more entries there are
// than when the panel was last opened. With no baseline, or after deletions,
// it is 0.
export function memoryNewSinceBaseline(summary, baseline) {
  if (!summary || !baseline) return 0;
  return Math.max(0, summary.count - baseline.count);
}

function entriesState(summary) {
  if (!summary.count) return { value: 'Empty', description: 'nothing recorded yet', tone: '' };
  const count = plural(summary.count, 'entry', 'entries');
  return {
    value: count,
    description: summary.watching ? count + ', ' + summary.watching + ' watching' : count,
    tone: ''
  };
}

// memoryStationState is the station's short status (PRD FR5, FR29): what the
// map building and the Stations rail print, and what a screen reader hears.
// Precedence: loading, unavailable, over budget, (HQ) to review, new, then
// the plain count.
//   status       — 'idle' | 'loading' | 'ready' | 'error'
//   summary      — memoryStationSummary() of the last read
//   baseline     — readMemoryStationBaseline() for this workspace, or null
//   reviewCount  — proposals waiting on the Profile (Personal HQ only)
//   personalHQ   — the HQ's memory is global memory, and says so
export function memoryStationState({ status, summary, baseline, reviewCount, personalHQ } = {}) {
  let state;
  if (status === 'error') {
    state = { value: 'Memory', description: 'status unavailable', tone: 'degraded' };
  } else if (status !== 'ready' || !summary) {
    state = { value: '—', description: 'loading memory', tone: 'loading' };
  } else if (summary.overBudget) {
    state = {
      value: 'Over budget',
      description: summary.dropped
        ? notShownToAgents(summary.dropped)
        : 'memory is over what agents are shown',
      tone: 'attention'
    };
  } else if (personalHQ && reviewCount > 0) {
    state = {
      value: reviewCount + ' to review',
      description: plural(reviewCount, 'proposal', 'proposals') + ' waiting on Profile',
      tone: 'attention'
    };
  } else {
    const fresh = memoryNewSinceBaseline(summary, baseline);
    state = fresh
      ? {
          value: '+' + fresh + ' new',
          description: plural(fresh, 'entry', 'entries') + ' recorded since you last looked',
          tone: 'attention'
        }
      : entriesState(summary);
  }
  if (!personalHQ) return state;
  // The HQ's memory is global memory: the accessible name leads with that, so
  // the description carries the printed value ("Empty", "+2 new") and the map
  // reads it once, after "global memory". Loading and unavailable states have
  // no value worth reading.
  const quiet = state.tone === 'loading' || state.tone === 'degraded';
  const detail =
    quiet || state.description.includes(state.value)
      ? state.description
      : state.value + ', ' + state.description;
  return { ...state, description: 'global memory, ' + detail };
}
