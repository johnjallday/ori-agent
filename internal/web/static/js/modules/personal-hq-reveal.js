// Transient presentation only. Request payloads, durable IDs, canonical status
// reads and safe retries remain with the HQ controller that invoked this flow.
export function createHQPresentation({
  transition,
  foreground = () => true,
  prepareMap = () => {},
  captureFocus = () => {},
  frame = () => false,
  focusPending = () => {},
  commitFolded = () => {},
  listen = () => () => {}
} = {}) {
  let active = null;
  let generation = 0;
  const owns = intent =>
    Boolean(intent && active === intent && transition?.isCurrent(intent.handle));
  const current = intent => owns(intent) && !intent.yielded && foreground(intent);
  function yieldIntent(intent, { reopen = false } = {}) {
    if (!intent || intent.yielded) return;
    intent.yielded = true;
    intent.cleanup?.();
    if (owns(intent)) transition.release(intent.handle, { reveal: reopen });
  }
  function show(intent, view) {
    if (!current(intent)) return false;
    try {
      return transition.showStatus(intent.handle, view);
    } catch (_) {
      yieldIntent(intent);
      return false;
    }
  }
  return {
    begin({ token, route = '', resume = false } = {}) {
      if (!transition || !token || !foreground({ route })) return null;
      if (active) yieldIntent(active);
      const intent = {
        generation: ++generation,
        token,
        route,
        resume,
        yielded: false,
        outcome: 'pending'
      };
      try {
        captureFocus();
        intent.handle = transition.enter({
          owner: 'personal-hq',
          generation: intent.generation,
          token,
          onUserOpen: () => yieldIntent(intent, { reopen: true })
        });
        if (!intent.handle) return null;
        active = intent;
        transition.dispatch(intent.handle);
        // No readiness wait: the caller dispatches its request immediately.
        try {
          prepareMap();
        } catch (_) {
          /* Compact status works without a map. */
        }
        show(intent, {
          title: 'Setting up your HQ…',
          detail: 'You can keep using Ori while setup finishes.',
          actions: [{ label: 'Dismiss', run: () => yieldIntent(intent) }]
        });
        try {
          focusPending();
        } catch (_) {
          /* Focus is optional. */
        }
        intent.cleanup = listen(() => yieldIntent(intent));
      } catch (_) {
        // Presentation failure never turns a valid request into a failed build.
        yieldIntent(intent);
      }
      return intent;
    },
    current,
    yield: yieldIntent,
    refused(intent) {
      if (!current(intent)) return false;
      intent.outcome = 'refused';
      intent.cleanup?.();
      return transition.returnToReview(intent.handle, { restoreFocus: true });
    },
    unresolved(
      intent,
      {
        title = 'Setup needs checking',
        detail = 'The response could not be confirmed. Check setup status before trying again.',
        check,
        retry,
        retryLabel = 'Retry same request'
      } = {}
    ) {
      if (!current(intent)) return false;
      intent.outcome = 'unresolved';
      // This closes the editable-return path; it is NOT evidence of acceptance.
      transition.accept(intent.handle);
      return show(intent, {
        title,
        detail,
        actions: [
          ...(check ? [{ label: 'Check setup status', run: check }] : []),
          ...(retry ? [{ label: retryLabel, run: retry }] : []),
          { label: 'Dismiss', run: () => yieldIntent(intent) }
        ]
      });
    },
    ready(intent, { workspaceId, route, stale = false, refresh, explore } = {}) {
      if (!current(intent)) return false;
      intent.outcome = stale ? 'stale' : 'ready';
      intent.workspaceId = workspaceId;
      transition.settle(intent.handle);
      try {
        commitFolded();
      } catch (_) {
        /* Browser preference is optional. */
      }
      if (!stale && workspaceId && !intent.framingStarted) {
        intent.framingStarted = true;
        let attempts = 0;
        const tryFrame = () => {
          if (!current(intent)) return;
          try {
            if (frame(workspaceId)) return;
          } catch (_) {
            return;
          }
          if (++attempts < 20) setTimeout(tryFrame, 100);
        };
        tryFrame();
      }
      return show(intent, {
        title: stale ? 'HQ was built; the view needs refreshing' : 'Your HQ is ready',
        detail: stale
          ? 'Refresh the view. There is no need to build again.'
          : 'Your assistant has a home base.',
        actions: [
          ...(stale && refresh ? [{ label: 'Refresh view', run: refresh }] : []),
          ...(!stale && route && explore
            ? [{ label: 'Explore HQ', primary: true, run: explore }]
            : []),
          { label: 'Dismiss', run: () => yieldIntent(intent) }
        ]
      });
    }
  };
}

let browserPresentation = null;
export function hqPresentation() {
  if (typeof window === 'undefined' || !window.OriCreationTransition) return null;
  if (browserPresentation) return browserPresentation;
  let moveFocus = false;
  browserPresentation = createHQPresentation({
    transition: window.OriCreationTransition,
    foreground: intent =>
      !document.hidden &&
      (!intent.route || intent.route === window.location.pathname + window.location.search),
    captureFocus: () => {
      const panel = document.getElementById('personalAssistantPanel');
      moveFocus =
        !!panel?.contains(document.activeElement) ||
        !!document.activeElement?.closest?.('#hqBuildModal');
    },
    prepareMap: () => {
      window.OriHomeCockpit?.showHQCreationMap?.();
    },
    frame: id => window.OriWorkspaceMap?.frameHQCreation?.(id),
    focusPending: () => {
      if (!moveFocus) return;
      const focus = () => {
        if (document.hidden || window.OriCreationTransition?.owner() !== 'personal-hq') return;
        const status = document.querySelector('[data-creation-map-status]');
        if (status && !status.hidden) {
          status.tabIndex = -1;
          status.focus({ preventScroll: true });
        }
      };
      const modal = document.querySelector('.modal.show, .modal[aria-modal="true"]');
      if (modal) modal.addEventListener('hidden.bs.modal', focus, { once: true });
      else focus();
    },
    commitFolded: () => window.PersonalAssistantPanel?.commitHQFolded?.(),
    listen: yieldPresentation => {
      const disposers = [];
      const on = (target, name, fn, options) => {
        target.addEventListener(name, fn, options);
        disposers.push(() => target.removeEventListener(name, fn, options));
      };
      on(document, 'ori:home-view-changed', yieldPresentation);
      on(document, 'visibilitychange', () => {
        if (document.hidden) yieldPresentation();
      });
      on(window, 'pagehide', yieldPresentation);
      on(window, 'popstate', yieldPresentation);
      on(document, 'click', event => {
        if (event.target?.closest?.('a[href]:not([href="#"])')) yieldPresentation();
      });
      on(document, 'keydown', event => {
        if (event.key === 'Escape' || event.target?.closest?.('[data-ws-map-viewport]'))
          yieldPresentation();
      });
      for (const name of ['pointerdown', 'wheel'])
        on(
          document,
          name,
          event => {
            if (event.target?.closest?.('[data-ws-map-viewport]')) yieldPresentation();
          },
          { passive: true }
        );
      return () => disposers.splice(0).forEach(dispose => dispose());
    }
  });
  return browserPresentation;
}
