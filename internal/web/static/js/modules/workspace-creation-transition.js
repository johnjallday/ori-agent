// One transient presentation lifetime for "reviewed setup → Map → result".
//
// This decides only what is on screen: whether the assistant drawer is out of
// the way, and which owner may bring it back. It holds no draft, permission,
// request id or result. Every create, status and retry callback stays with the
// owner that already has it, and nothing here survives a reload.

// placing:     the user is choosing a point; Back returns to the review.
// dispatching: the final gesture ran; a refusal can still return to the review.
// accepted:    work was accepted. Closing a surface does not cancel it, and the
//              editable review never comes back.
// settled:     the owner verified an outcome; only its receipt remains.
const RETURNABLE = new Set(['placing', 'dispatching']);

/**
 * The compact status on the Map. It shows what an owner says, as plain text, and
 * runs that owner's own callbacks. It keeps no result and never takes focus, so
 * background progress cannot pull someone away from what they are doing.
 */
export function createCreationStatusSurface(doc = globalThis.document) {
  let root = null;
  const build = () => {
    root = doc.createElement('section');
    root.className = 'creation-map-status';
    root.dataset.creationMapStatus = '';
    root.setAttribute('aria-label', 'Workspace setup status');
    root.hidden = true;
    const copy = doc.createElement('div');
    copy.className = 'creation-map-status__copy';
    copy.setAttribute('role', 'status');
    copy.setAttribute('aria-live', 'polite');
    const title = doc.createElement('strong');
    title.className = 'creation-map-status__title';
    const detail = doc.createElement('span');
    detail.className = 'creation-map-status__detail';
    copy.append(title, detail);
    const actions = doc.createElement('div');
    actions.className = 'creation-map-status__actions';
    root.append(copy, actions);
    doc.body.append(root);
    return root;
  };
  return {
    show({ tone = 'progress', title = '', detail = '', actions = [] } = {}) {
      const host = root || build();
      host.dataset.tone = tone;
      host.querySelector('.creation-map-status__title').textContent = title;
      const line = host.querySelector('.creation-map-status__detail');
      line.textContent = detail;
      line.hidden = !detail;
      const row = host.querySelector('.creation-map-status__actions');
      const focused = row.contains(doc.activeElement) ? doc.activeElement.textContent : '';
      row.replaceChildren(
        ...actions.map(action => {
          const button = doc.createElement('button');
          button.type = 'button';
          button.className = 'creation-map-status__action';
          if (action.primary) button.classList.add('is-primary');
          button.textContent = action.label;
          button.disabled = action.disabled === true;
          button.addEventListener('click', () => action.run?.());
          return button;
        })
      );
      row.hidden = actions.length === 0;
      host.hidden = false;
      // Redrawing the row must not drop keyboard users back to the page.
      if (focused)
        Array.from(row.children)
          .find(button => button.textContent === focused)
          ?.focus({ preventScroll: true });
    },
    hide() {
      if (root) root.hidden = true;
    },
    element: () => root
  };
}

export function createCreationTransition({ panel, status } = {}) {
  let active = null;
  let suspended = false;
  // An explicit open re-enters the panel through the owner's own Back path.
  let reopening = false;

  const surface = () => (typeof panel === 'function' ? panel() : panel);
  const statusSurface = () => (typeof status === 'function' ? status() : status);
  const current = handle => Boolean(handle) && handle === active;

  function restorePanel(options) {
    if (!suspended) return false;
    suspended = false;
    return surface()?.resume?.(options) === true;
  }

  return {
    /** Only the current lifetime may speak; a late owner's status is dropped. */
    showStatus(handle, view) {
      if (!current(handle)) return false;
      statusSurface()?.show?.(view);
      return true;
    },

    clearStatus(handle) {
      if (!current(handle)) return false;
      statusSurface()?.hide?.();
      return true;
    },

    /**
     * Starts a lifetime for one owner + generation + token. A newer owner
     * supersedes an older one: the older handle goes stale, and the drawer
     * stays suspended for the newer owner rather than flashing back.
     */
    enter({ owner, generation, token, onExplicitReopen } = {}) {
      if (!owner || !token) return null;
      if (active) active.phase = 'superseded';
      statusSurface()?.hide?.();
      if (!suspended) suspended = surface()?.suspend?.() === true;
      active = { owner, generation, token, phase: 'placing', onExplicitReopen };
      return active;
    },

    isCurrent: current,
    phase: () => active?.phase || 'idle',
    owner: () => active?.owner || '',

    /** Back, Escape or a refusal: the same review returns. Nothing else happens. */
    returnToReview(handle, options = {}) {
      if (!current(handle) || !RETURNABLE.has(handle.phase)) return false;
      active = null;
      handle.phase = 'returned';
      statusSurface()?.hide?.();
      restorePanel({ reveal: true, ...options });
      return true;
    },

    dispatch(handle) {
      if (!current(handle) || handle.phase !== 'placing') return false;
      handle.phase = 'dispatching';
      return true;
    },

    /** Work was accepted; this lifetime can no longer return to an editor. */
    accept(handle) {
      if (!current(handle) || !RETURNABLE.has(handle.phase)) return false;
      handle.phase = 'accepted';
      return true;
    },

    settle(handle) {
      if (!current(handle) || handle.phase === 'settled') return false;
      handle.phase = 'settled';
      return true;
    },

    /**
     * Ends the lifetime. `reveal` puts a suspended drawer back; otherwise it
     * stays out of the way as an ordinary closed drawer, so a late result never
     * reopens it. Either way the user's open preference is left as it was.
     */
    release(handle, { reveal = false, ...options } = {}) {
      if (!current(handle)) return false;
      active = null;
      handle.phase = 'released';
      statusSurface()?.hide?.();
      restorePanel({ reveal, ...options });
      return true;
    },

    /** Unsolicited reveals (a late welcome, activity) wait for this to clear. */
    blocksReveal: () => Boolean(active),

    /**
     * The user explicitly asked for the conversation. Before acceptance that
     * leaves placement through the owner's own Back; afterwards the drawer
     * comes back beside the running work, which carries on untouched.
     * Returns whether the panel should go on to finish an ordinary opening.
     */
    requestOpen() {
      if (reopening) return true;
      const handle = active;
      if (handle?.phase === 'placing') {
        reopening = true;
        try {
          handle.onExplicitReopen?.();
        } finally {
          reopening = false;
        }
        // An owner that had nothing to do must not leave the user locked out.
        if (active === handle) {
          active = null;
          handle.phase = 'returned';
        }
      }
      restorePanel({ reveal: true });
      return true;
    },

    isReopening: () => reopening
  };
}
