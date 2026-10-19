// A workspace's read-only Protection fact: whether its linked project folder
// is in a backup Ori observes. The server renders the sentence from stored
// evidence; this module only fetches it and decides what may be shown.

const STATES = new Set(['covered', 'not_covered', 'unknown']);

function sameOriginRoute(value) {
  const route = String(value || '');
  return (
    route.startsWith('/') &&
    !route.startsWith('//') &&
    !route.includes('://') &&
    !route.includes('\\')
  );
}

// null when the workspace has no adopted folder or the read failed: the
// Command view then shows nothing rather than a guess.
export async function fetchWorkspaceProtection(workspaceId, fetchImpl = fetch) {
  if (!workspaceId) return null;
  try {
    const response = await fetchImpl(
      `/api/workspaces/${encodeURIComponent(workspaceId)}/protection`,
      { headers: { Accept: 'application/json' } }
    );
    if (!response?.ok) return null;
    const body = await response.json();
    return body?.protection || null;
  } catch (_) {
    return null;
  }
}

export function protectionFactView(fact) {
  const state = String(fact?.state || '');
  const sentence = String(fact?.sentence || '').trim();
  if (!STATES.has(state) || !sentence) return null;
  const route = sameOriginRoute(fact?.route) ? String(fact.route) : '';
  return {
    state,
    sentence,
    route,
    routeLabel: route ? String(fact?.route_label || '').trim() || 'Open Backup & Recovery' : ''
  };
}
