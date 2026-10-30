// Placing a song's building inside its Home from the manager's offer: the
// drawer closes, the Home's own scoped map takes one click (or Enter) on open
// ground inside the Home, and only then does the shared press run. Once the
// run reports the song opened and the browser has verified that workspace is
// new and a child of this Home, the chosen point is saved once through the
// map's layout save. A point is geometry only: it never changes membership,
// and a failed save never re-presses the open.

// The assistant card's reviewed-placement threshold (personal-assistant-folder.js).
export const PLACEMENT_MIN_WIDTH = 980;
export const POSITION_NOT_SAVED = 'Its position was not saved; use Move.';

function text(value) {
  return String(value ?? '').trim();
}

/**
 * Whether a press can enter placement on this page: the Home's own scoped map
 * is mounted and drawn (Map mode), its layout is ready and writable, and the
 * window is wide enough. Anything else takes the automatic path (FR15).
 */
export function homeSongPlacementAvailable({ homeID, map, innerWidth, viewport } = {}) {
  const id = text(homeID);
  if (!id || !map || typeof map.beginPlacement !== 'function') return false;
  if (!(Number(innerWidth) >= PLACEMENT_MIN_WIDTH)) return false;
  if (typeof map.getScopeGroupId !== 'function' || map.getScopeGroupId() !== id) return false;
  const layout = map.getLayoutState?.();
  if (!layout || layout.status !== 'ready' || layout.readOnly === true) return false;
  return Boolean(viewport?.getClientRects?.().length);
}

/** The banner and announcements for one song, in the manager's register. */
export function homeSongPlacementCopy(view, homeName) {
  const home = text(homeName) || 'the Home';
  const instruction = `Open ${view.song} here with a click on open ground inside ${home}, or press Enter. Escape returns to the conversation.`;
  return {
    confirmLabel: `Open ${view.song} here`,
    pendingLabel: `${view.manager} is opening ${view.song}…`,
    instruction,
    pendingInstruction: `${view.manager} is opening ${view.song}. This position is locked.`,
    announcement: `Placing ${view.song} inside ${home}. ${instruction}`,
    backLabel: 'Back to the conversation',
    secondaryLabel: 'Use automatic placement'
  };
}

/**
 * The opened song is the one this run made inside this Home: the outcome is
 * not an existing workspace, its receipt (when it says) created it, and the
 * workspace's own record names this Home as its parent.
 */
export function openedHereInHome(outcome, resource, homeID) {
  const id = text(outcome?.workspace_id);
  if (!id || outcome?.existing === true || text(resource?.id) !== id) return false;
  if (text(resource?.parent_id) !== text(homeID)) return false;
  const row = (Array.isArray(outcome?.receipt) ? outcome.receipt : []).find(
    entry => entry?.kind === 'workspace'
  );
  return !row?.operation || row.operation === 'create';
}

/**
 * The strategy createHomeOffer().usePlacement() takes. `panel` is the drawer
 * (close/open), `map` the page's OriWorkspaceMap, `home()` the Home's id and
 * name, `available()` the check above for this page.
 */
export function createHomeSongPlacement({
  map: mapSource,
  panel,
  home,
  available,
  fetchImpl = (...args) => globalThis.fetch(...args),
  focusCard = () => {}
} = {}) {
  let session = 0;
  // The page's map, or a getter for it: its script may load after this one.
  const current = () => (typeof mapSource === 'function' ? mapSource() : mapSource);

  // The conversation is kept as it was (the echoed brief included); focus goes
  // to the song's card.
  function reopen(view) {
    panel?.open?.(null, { focus: false, preserveConversation: true });
    focusCard(view);
  }

  async function verifyAndSave(view, token, candidate, outcome) {
    const map = current();
    const { id: homeID } = home();
    const id = text(outcome?.workspace_id);
    let resource = null;
    try {
      const response = await fetchImpl(`/api/workspaces/${encodeURIComponent(id)}`, {
        cache: 'no-store',
        headers: { Accept: 'application/json' }
      });
      if (response.ok) resource = await response.json();
    } catch (_) {
      resource = null;
    }
    const workspace = resource?.workspace || resource;
    if (!openedHereInHome(outcome, workspace, homeID)) {
      // Not provably new here: the map keeps its automatic placement.
      map.endPlacement?.(token);
      return POSITION_NOT_SAVED;
    }
    const result = await map.commitPlacement(id, { token, candidate, select: true });
    return result?.saved === true ? '' : POSITION_NOT_SAVED;
  }

  return {
    wants: () => available() === true,
    async begin(view, { run }) {
      const map = current();
      const { id: homeID, name } = home();
      const token = `home-song:${view.offerID}:${++session}`;
      let candidate = null;
      let settled = false;
      const finish = async options => {
        if (settled) return false;
        settled = true;
        try {
          return await run(options);
        } finally {
          map.endPlacement?.(token);
        }
      };
      panel?.close?.({ restoreFocus: false });
      const started = map.beginPlacement({
        token,
        mode: 'reviewed-setup',
        preview: { name: view.song, kind: 'workspace', parentID: homeID, withinParent: true },
        actionCopy: homeSongPlacementCopy(view, name),
        onCandidate: (_token, point) => {
          candidate = point ? { ...point } : candidate;
        },
        onConfirm: (_token, point) => {
          candidate = point ? { ...point } : candidate;
          if (!candidate) return false;
          // Nothing ran before this click or Enter. The drawer comes back on
          // the running card while the run proceeds (FR13).
          reopen(view);
          void finish({
            afterVerified: outcome => verifyAndSave(view, token, candidate, outcome)
          });
          return true;
        },
        // Escape is "back to the conversation": the card and its brief stay.
        onBack: () => reopen(view),
        onSecondary: () => {
          reopen(view);
          void finish({});
        }
      });
      if (started === false) {
        // The map could not take the session after all: the automatic path.
        reopen(view);
        await finish({});
      }
    }
  };
}
