// home-library-badges.js — the Home map's project-library badge.
//
// PRD: tasks/prd-library-manager-notifications.md FR 11-12. A Music (or other
// program) Home whose last completed scan found projects ready for setup, or
// whose Manager left suggestions ready for review, shows one small badge on
// its district header. The badge only navigates to the Home's suggestions
// shelf; it never scans, confirms or changes anything.
//
// Reads are event-driven, never a timer: the cockpit asks on its refresh, when
// the tab becomes visible again, and on a back/forward return. Repeated asks
// inside LIBRARY_BADGE_MIN_REFRESH_MS reuse the last answer.

export const LIBRARY_BADGE_MIN_REFRESH_MS = 30_000;
const MAX_HOMES = 20;
const SHELF_ROUTE = /^\/workspaces\/[^/?#\s]+\/assistant#projectLibraryProposals$/;

/** The route if it is exactly a same-origin suggestions-shelf route, else ''. */
export function libraryShelfRoute(value) {
  const route = typeof value === 'string' ? value : '';
  return SHELF_ROUTE.test(route) ? route : '';
}

/** Program Homes on the map: groups whose server-derived template is a managed Home. */
export function libraryHomeIds(workspaces) {
  const ids = [];
  for (const ws of Array.isArray(workspaces) ? workspaces : []) {
    if (ids.length >= MAX_HOMES) break;
    const id = String(ws?.id || '');
    if (id && ws?.group_template?.kind === 'managed_home' && !ids.includes(id)) ids.push(id);
  }
  return ids;
}

function count(value) {
  return Number.isInteger(value) && value > 0 ? value : 0;
}

/**
 * The badge for one Home summary, or null when there is nothing to act on.
 *
 * Shown only when entries are ready for a setup review or suggestions are
 * ready for review (FR 11); "new" alone is not a reason to interrupt.
 */
export function libraryBadgeView(summary, homeName = 'Home') {
  if (!summary || typeof summary !== 'object') return null;
  const digest = summary.digest && typeof summary.digest === 'object' ? summary.digest : null;
  const fresh = digest ? count(digest.new) : 0;
  const ready = digest && !digest.setup_note ? count(digest.activatable) : 0;
  const review = count(summary.ready_proposals);
  const route = libraryShelfRoute(summary.route);
  if ((ready === 0 && review === 0) || !route) return null;
  const text = [];
  const spoken = [];
  if (fresh) {
    text.push(`${fresh} new`);
    spoken.push(`${fresh} new ${fresh === 1 ? 'project' : 'projects'}`);
  }
  if (ready) {
    text.push(`${ready} ready`);
    spoken.push(`${ready} ready to set up`);
  }
  if (review) {
    text.push(`${review} to review`);
    spoken.push(`${review} ${review === 1 ? 'suggestion' : 'suggestions'} to review`);
  }
  const name = String(homeName || 'Home').trim() || 'Home';
  return {
    text: text.join(' · '),
    label: `${name} library: ${spoken.join(', ')}. Open the suggestions shelf.`,
    route
  };
}

/**
 * One loader per page. load() fetches each Home's summary in parallel and
 * resolves to { [homeID]: view }. A call inside the minimum interval returns
 * the previous snapshot unless forced; concurrent calls share one request.
 */
export function createLibraryBadgeLoader({
  fetchImpl = (...args) => globalThis.fetch(...args),
  now = () => Date.now(),
  minRefreshMs = LIBRARY_BADGE_MIN_REFRESH_MS
} = {}) {
  let snapshot = {};
  let loadedAt = null;
  let loadedKey = '';
  let inFlight = null;

  async function summaryFor(id) {
    try {
      const response = await fetchImpl(
        `/api/workspaces/${encodeURIComponent(id)}/assistant-program/library/summary`,
        { headers: { Accept: 'application/json' } }
      );
      if (!response.ok) return null; // Not a library Home, or not yours: no badge.
      return await response.json();
    } catch (_) {
      return null;
    }
  }

  async function load(workspaces, { force = false } = {}) {
    const ids = libraryHomeIds(workspaces);
    const key = ids.join('\n');
    const fresh = loadedAt !== null && now() - loadedAt < minRefreshMs && key === loadedKey;
    if (!force && fresh) return snapshot;
    if (inFlight) return inFlight;
    const names = new Map(
      (Array.isArray(workspaces) ? workspaces : []).map(ws => [String(ws?.id || ''), ws?.name])
    );
    inFlight = (async () => {
      const next = {};
      const summaries = await Promise.all(ids.map(summaryFor));
      ids.forEach((id, index) => {
        const view = libraryBadgeView(summaries[index], names.get(id));
        if (view) next[id] = view;
      });
      snapshot = next;
      loadedAt = now();
      loadedKey = key;
      return snapshot;
    })();
    try {
      return await inFlight;
    } finally {
      inFlight = null;
    }
  }

  return { load, current: () => snapshot };
}
