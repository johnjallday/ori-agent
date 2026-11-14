// Transient presentation only. Request payloads, durable IDs, canonical status
// reads and safe retries remain with the HQ controller that invoked this flow.
export function createHQPresentation({
  transition,
  foreground = () => true,
  prepareMap = () => {},
  captureFocus = () => {},
  frame = () => false,
  play = () => null,
  avatar = () => null,
  notify = () => {},
  focusPending = () => {},
  commitFolded = () => {},
  listen = () => () => {}
} = {}) {
  let active = null;
  let generation = 0;
  const owns = intent =>
    Boolean(intent && active === intent && transition?.isCurrent(intent.handle));
  const current = intent => owns(intent) && !intent.yielded && foreground(intent);
  function yieldIntent(intent, { reopen = false, preserveResult = false } = {}) {
    if (!intent || intent.yielded) return;
    intent.yielded = true;
    intent.preserveResult = preserveResult;
    intent.motion?.skip?.();
    intent.cleanup?.();
    if (owns(intent)) transition.release(intent.handle, { reveal: reopen });
    notify();
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
    begin({ token, route = '', resume = false, focusOwned = false } = {}) {
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
        captureFocus(focusOwned);
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
          focusPending(intent);
        } catch (_) {
          /* Focus is optional. */
        }
        intent.cleanup = listen(options => yieldIntent(intent, options));
      } catch (_) {
        // Presentation failure never turns a valid request into a failed build.
        yieldIntent(intent);
      }
      return intent;
    },
    current,
    yield: yieldIntent,
    adoptResult(intent) {
      if (!current(intent) || intent.outcome !== 'ready') return false;
      intent.externalResult = true;
      transition.clearStatus(intent.handle);
      return true;
    },
    skip(intent) {
      if (current(intent)) intent.motion?.skip?.();
    },
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
    ready(intent, { workspaceId, route, stale = false, refresh, explore, assistant = null } = {}) {
      if (!current(intent)) return false;
      intent.outcome = stale ? 'stale' : 'ready';
      intent.workspaceId = workspaceId;
      transition.settle(intent.handle);
      try {
        commitFolded();
      } catch (_) {
        /* Browser preference is optional. */
      }
      const renderResult = () => {
        if (intent.externalResult) {
          if (current(intent)) notify();
          return current(intent);
        }
        return show(intent, {
          title: stale ? 'HQ was built; the view needs refreshing' : 'Your HQ is ready',
          leading: stale ? null : avatar(assistant),
          detail: stale
            ? 'Refresh the view. There is no need to build again.'
            : `${assistant?.display_name || 'Your assistant'} has a home base. Welcome to your HQ.`,
          actions: [
            ...(stale && refresh ? [{ label: 'Refresh view', run: refresh }] : []),
            ...(!stale && route && explore
              ? [{ label: 'Explore HQ', primary: true, run: explore }]
              : []),
            ...(intent.motion
              ? [{ label: 'Skip animation', run: () => intent.motion?.skip?.() }]
              : []),
            { label: 'Dismiss', run: () => yieldIntent(intent) }
          ]
        });
      };
      if (!stale && workspaceId && !intent.framingStarted) {
        intent.framingStarted = true;
        let attempts = 0;
        const tryFrame = () => {
          if (!current(intent)) return;
          try {
            if (frame(workspaceId)) {
              if (intent.eligible !== false) {
                intent.motion = play(workspaceId);
                intent.motion?.done?.then(() => {
                  intent.motion = null;
                  renderResult();
                });
              }
              renderResult();
              return;
            }
          } catch (_) {
            return;
          }
          if (++attempts < 20) setTimeout(tryFrame, 100);
        };
        tryFrame();
      }
      const shown = renderResult();
      notify();
      return shown;
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
    captureFocus: focusOwned => {
      const panel = document.getElementById('personalAssistantPanel');
      moveFocus =
        !!panel?.contains(document.activeElement) ||
        !!document.activeElement?.closest?.('#hqBuildModal') ||
        (focusOwned && document.activeElement === document.body);
    },
    prepareMap: () => {
      const shown = window.OriHomeCockpit?.showHQCreationMap?.();
      // On phones the ordinary Home header is taller than the available map
      // stage. Scroll this explicit foreground gesture to the real canvas;
      // do not change the saved view/camera or scroll on reload/status updates.
      if (
        shown &&
        window.matchMedia('(max-width: 640px), (max-width: 900px) and (max-height: 599px)').matches
      ) {
        document.body.classList.add('hq-reveal-active');
        const canvas = document.querySelector('[data-ws-map-viewport]');
        const navHeight = document.querySelector('.navbar')?.getBoundingClientRect().height || 0;
        if (canvas)
          window.scrollBy({
            top: canvas.getBoundingClientRect().top - navHeight - 12,
            behavior: 'instant'
          });
      }
    },
    frame: id => window.OriWorkspaceMap?.frameHQCreation?.(id),
    play: id => window.OriWorkspaceMap?.playHQAssembly?.(id),
    notify: () => document.dispatchEvent(new CustomEvent('personal-hq:presentation')),
    avatar: assistant => {
      if (!window.AgentAvatar?.markup || !assistant) return null;
      const node = document.createElement('span');
      node.innerHTML = window.AgentAvatar.markup(
        { name: assistant.display_name, appearance: assistant.appearance || {} },
        { size: 40 }
      );
      return node;
    },
    focusPending: intent => {
      if (!moveFocus) return;
      const focus = () => {
        if (!browserPresentation?.current(intent)) return;
        const status = document.querySelector('[data-creation-map-status]');
        if (status && !status.hidden) {
          status.tabIndex = -1;
          status.focus({ preventScroll: true });
        }
      };
      // Several unrelated modal templates declare aria-modal even while hidden.
      // Only an actually open/closing dialog may delay this focus handoff.
      const modal = document.querySelector(
        '.modal.show, .modal[aria-modal="true"]:not([aria-hidden="true"])'
      );
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
        if (document.hidden) yieldPresentation({ preserveResult: true });
      });
      on(window, 'pagehide', () => yieldPresentation({ preserveResult: true }));
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
      return () => {
        document.body.classList.remove('hq-reveal-active');
        disposers.splice(0).forEach(dispose => dispose());
      };
    }
  });
  return browserPresentation;
}
