/*
 * Contextual Help — deterministic app explanations, never work execution.
 *
 * Every search, including unknown questions and slash commands, goes only to
 * the bounded guide endpoint. Relationship readiness affects an explicit
 * assistant transition, never this controller's authority. Fixed walkthroughs
 * reuse the registered coachmarks without calling the guide or a provider.
 * Closing is presentation only: it does not reset page selection or cancel work.
 */
(function () {
  'use strict';

  var ENDPOINT = '/api/ori-guide';
  // The action types this controller will render. Anything else is dropped and
  // never becomes a control.
  var ALLOWED_ACTIONS = {
    navigate: true,
    setup: true,
    coachmark: true,
    handoff: true,
    reset: true,
    dismiss: true
  };

  var state = {
    open: false,
    pending: false,
    // Monotonic request counter; only the newest reply is rendered.
    seq: 0,
    lastTrigger: null,
    coachmarkEl: null,
    coachmarkRoute: '',
    // The decorative animated pointer that bobs at the marked control, plus the
    // listeners that keep it parked there. Presentation only: it never receives
    // focus, is never announced, and never carries meaning the copy lacks.
    pointerEl: null,
    pointerTrack: null,
    // Timer for a walkthrough step waiting on a control that is still mounting.
    coachmarkWait: null,
    // The key the current mark came from, so the mark can re-anchor onto a
    // fresh node when the page re-renders the control under it.
    coachmarkKey: '',
    // Whether the marked control had focus, so a re-render can carry it over.
    coachmarkHeldFocus: false,
    actions: [],
    // The active deterministic walkthrough step, if any: { quest, choices }.
    // Presentation only — the server owns whether the quest is done or deferred.
    quest: null,
    // Compatibility flag only; there is no non-Help execution mode.
    helpOnly: true,
    greeted: false,
    screenRoute: '',
    screenTitle: '',
    contextKey: '',
    response: null,
    responseKey: '',
    assistantState: 'loading',
    assistantAvailable: false,
    // A hired assistant with no Personal HQ yet: distinct from "never hired" so
    // the handoff decline can route to the guided quest instead of Hire.
    needsHQ: false,
    assistantName: 'your personal assistant',
    // Search text survives close/reopen; Help has no transcript.
    draft: '',
    els: null
  };

  /* ---- test-visible events ------------------------------------------------------ */

  // Coarse, local-only signals so browser tests can assert on guide behaviour
  // without scraping the DOM. These are DOM events and nothing more: there is no
  // network call, no storage, and no raw question in the payload — the PRD
  // explicitly rules out building analytics or retaining prompts here
  // (Technical Consideration 7.4).
  function emit(name, detail) {
    if (typeof document === 'undefined' || typeof CustomEvent !== 'function') return;
    try {
      document.dispatchEvent(
        new CustomEvent('ori-guide:' + name, { detail: detail || {}, bubbles: true })
      );
    } catch (err) {
      /* events are diagnostics; never let one break the guide */
    }
  }

  /* ---- escaping -------------------------------------------------------------- */

  function esc(value) {
    return String(value == null ? '' : value)
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;')
      .replace(/'/g, '&#39;');
  }

  /* ---- action validation ------------------------------------------------------ */

  // Space, control characters, and DEL have no place in a path we generated.
  // Written as a code-point scan rather than a regex range so the source stays
  // free of literal control bytes.
  function hasUnsafeChar(value) {
    for (var i = 0; i < value.length; i++) {
      var code = value.charCodeAt(i);
      if (code <= 32 || code === 127) return true;
    }
    return false;
  }

  // A destination must be a same-origin, absolute-path URL. This is a second
  // gate on top of the server's catalog validation: an external or
  // scheme-bearing href never becomes a link, whatever the response said
  // (FR-49). Hyphens stay legal — /action-center is a real route.
  function isSafeHref(href) {
    var h = String(href || '');
    if (!h.startsWith('/')) return false;
    if (h.startsWith('//')) return false;
    if (h.indexOf('://') !== -1) return false;
    if (hasUnsafeChar(h)) return false;
    try {
      var path = decodeURIComponent(h.split(/[?#]/, 1)[0]);
      if (path.indexOf('\\') !== -1 || hasUnsafeChar(path)) return false;
      if (
        path.split('/').some(function (part) {
          return part === '.' || part === '..';
        })
      )
        return false;
      return new URL(h, 'http://ori.local').origin === 'http://ori.local';
    } catch (_) {
      return false;
    }
  }

  function validateAction(action) {
    if (!action || typeof action !== 'object') return null;
    var type = String(action.type || '');
    if (!ALLOWED_ACTIONS[type]) return null;

    if (type === 'navigate' || type === 'setup') {
      if (!isSafeHref(action.href)) return null;
      return {
        type: type,
        label: String(action.label || 'Open'),
        href: String(action.href)
      };
    }

    if (type === 'coachmark') {
      var key = String(action.coachmark || '');
      var registry = window.OriGuideCoachmarks;
      // Only a key the browser itself knows about survives.
      if (!registry || !registry.supports(key, currentRoute())) return null;
      return { type: type, label: String(action.label || 'Show me where'), coachmark: key };
    }

    if (type === 'handoff') {
      var handoffText = String(action.handoff_text || action.handoffText || '').slice(0, 400);
      if (!state.assistantAvailable) {
        var setup = assistantSetupAction();
        if (setup) return setup;
        // Unknown/loading status is not proof of an unhired relationship.
        return { type: type, label: 'Retry assistant status', handoffText: handoffText };
      }
      return {
        type: type,
        label: 'Open ' + state.assistantName,
        handoffText: handoffText
      };
    }

    return { type: type, label: String(action.label || '') };
  }

  function assistantSetupAction() {
    if (state.needsHQ)
      return {
        type: 'navigate',
        label:
          state.assistantState === 'provisioning_hq'
            ? 'Resume Personal HQ setup'
            : 'Build Personal HQ',
        href: '/?quest=build-hq'
      };
    if (state.assistantState === 'repair_needed')
      return {
        type: 'navigate',
        label: 'Repair personal assistant',
        href: '/agents?quest=meet-assistant'
      };
    if (state.assistantState === 'needs_hire' || state.assistantState === 'hiring')
      return {
        type: 'navigate',
        label:
          state.assistantState === 'hiring'
            ? 'Resume meeting your assistant'
            : 'Meet your assistant',
        href: /^\/agents(\/|$)/.test(currentRoute())
          ? '/agents?quest=meet-assistant'
          : '/?quest=meet-assistant'
      };
    return null;
  }

  function currentRoute() {
    try {
      return window.location.pathname || '/';
    } catch (err) {
      return '/';
    }
  }

  /* ---- page context ------------------------------------------------------------ */

  // Context a page module supplies that the URL cannot: Home's Map/Tree
  // selection, an open session, a friendlier label for the current target.
  // It is a hint only — the server still decides what any request is allowed to
  // touch (FR17).
  var pageContext = { workspaceId: '', taskId: '', sessionId: '', label: '' };

  // Derives surface/workspace/task from the path so a page that supplies nothing
  // still gets correct context. Shapes:
  //   /                                  home
  //   /workspaces                        workspace hub
  //   /workspaces/{id}                   workspace detail
  //   /workspaces/{id}/canvas            workspace canvas
  //   /workspaces/{id}/tasks/{taskId}    workspace task
  function contextFromRoute(route) {
    var path = String(route || '/');
    var out = { surface: 'app', workspaceId: '', taskId: '' };

    if (path === '/' || path === '') {
      out.surface = 'home';
      return out;
    }
    if (path === '/workspaces' || path === '/workspaces/') {
      out.surface = 'workspace_hub';
      return out;
    }
    if (path.indexOf('/workspaces/') !== 0) {
      return out;
    }

    var parts = path.slice('/workspaces/'.length).split('/').filter(Boolean);
    if (!parts.length) return out;

    out.workspaceId = decodeURIComponent(parts[0]);
    out.surface = 'workspace_detail';
    if (parts[1] === 'canvas') {
      out.surface = 'workspace_canvas';
    } else if (parts[1] === 'tasks' && parts[2]) {
      out.surface = 'workspace_task';
      out.taskId = decodeURIComponent(parts[2]);
    }
    return out;
  }

  // The normalized context sent with every submission. A page-supplied workspace
  // only fills a gap the URL left; it never overrides the workspace the user is
  // demonstrably looking at (FR18).
  function collectContext() {
    var route = currentRoute();
    var derived = contextFromRoute(route);
    if (window.PersonalAssistantWorkspaceContext?.current) {
      return window.PersonalAssistantWorkspaceContext.current({ origin: 'ask_ori_panel' });
    }
    if (window.PersonalAssistantWorkspaceContext?.collect) {
      return window.PersonalAssistantWorkspaceContext.collect({
        pathname: route,
        workspaceId: document.body?.dataset?.workspaceId || '',
        workspaceSlug: document.body?.dataset?.workspaceSlug || '',
        selectionWorkspaceId:
          window.oriHomeRouteContext?.selection_workspace_id ?? pageContext.workspaceId,
        taskId: pageContext.taskId,
        sessionId: pageContext.sessionId,
        origin: 'ask_ori_panel'
      });
    }
    return {
      surface: derived.surface,
      page_path: route,
      workspace_id: derived.workspaceId || String(pageContext.workspaceId || ''),
      task_id: derived.taskId || String(pageContext.taskId || ''),
      session_id: String(pageContext.sessionId || ''),
      origin: 'ask_ori_panel'
    };
  }

  function contextLabel(ctx) {
    if (pageContext.label) return String(pageContext.label);
    switch (ctx.surface) {
      case 'home':
        return ctx.workspace_id ? 'Workspace: ' + ctx.workspace_id : 'Home';
      case 'workspace_hub':
        return 'All workspaces';
      case 'workspace_detail':
        return 'Workspace: ' + ctx.workspace_id;
      case 'workspace_canvas':
        return 'Canvas: ' + ctx.workspace_id;
      case 'workspace_task':
        return 'Task: ' + (ctx.task_id || ctx.workspace_id);
      default:
        return 'All workspaces';
    }
  }

  // Repaints the visible context. Called before a request is accepted so a stale
  // workspace or task is never submitted invisibly (FR46).
  function refreshContextLabel() {
    var els = state.els;
    if (!els || !els.context) return collectContext();
    var ctx = collectContext();
    els.context.textContent =
      state.screenRoute === currentRoute() ? state.screenTitle || 'This screen' : 'This screen';
    return ctx;
  }

  // The seam page modules use to contribute context they alone know about.
  // Drops an in-flight request whose answer would now be read against a
  // different workspace.
  //
  // Request sequencing alone does not cover this: the sequence only advances
  // when a NEWER request is made, so a reply for workspace A that lands after
  // the user switched to workspace B would render as actionable B content
  // (FR17/FR46). Changing target invalidates the question that was asked about
  // the old one.
  function helpContextKey() {
    var ctx = collectContext();
    return JSON.stringify([
      currentRoute(),
      ctx.surface,
      ctx.workspace_id,
      ctx.selection_workspace_id,
      ctx.task_id,
      ctx.session_id
    ]);
  }

  function invalidateInFlightForContextChange(previousKey) {
    if (previousKey === helpContextKey()) return;
    state.seq += 1;
    setPending(false, false);
    state.actions = [];
    state.response = null;
    if (state.quest) return;
    state.greeted = false;
    if (state.els) {
      state.els.reply.dataset.status = 'context-changed';
      state.els.reply.innerHTML =
        '<p class="ori-guide__answer">The screen or selection changed. Search again for current guidance. Your text is kept.</p>';
    }
    if (state.screenRoute !== currentRoute()) {
      state.screenTitle = '';
      if (state.els?.about)
        state.els.about.textContent = 'Open Help or Search again to load guidance for this screen.';
      if (state.els?.topics) state.els.topics.innerHTML = '';
    }
    emit('context-invalidated', {});
  }

  function setContext(partial) {
    var next = partial || {};
    var previousKey = helpContextKey();

    if ('workspaceId' in next) pageContext.workspaceId = String(next.workspaceId || '');
    if ('taskId' in next) pageContext.taskId = String(next.taskId || '');
    if ('sessionId' in next) pageContext.sessionId = String(next.sessionId || '');
    if ('label' in next) pageContext.label = String(next.label || '');

    if (window.PersonalAssistantWorkspaceContext?.publish) {
      window.PersonalAssistantWorkspaceContext.publish(next);
    } else {
      // Module initialization may follow classic script publication.
      window.oriWorkPageContext = { ...window.oriWorkPageContext, ...next };
    }
    invalidateInFlightForContextChange(previousKey);
    state.contextKey = helpContextKey();
    refreshContextLabel();
    emit('context', { surface: collectContext().surface });
  }

  /* ---- rendering --------------------------------------------------------------- */

  function actionHTML(action, index) {
    var label = esc(action.label);
    if (action.type === 'navigate' || action.type === 'setup') {
      // A real link, so middle-click, open-in-new-tab, before-unload warnings,
      // and route guards all behave exactly as they do anywhere else.
      // Navigation is UI assistance, not authorization.
      return (
        '<a class="ori-guide__action" href="' +
        esc(action.href) +
        '" data-ori-action="' +
        esc(action.type) +
        '" data-ori-index="' +
        index +
        '">' +
        label +
        '</a>'
      );
    }
    return (
      '<button type="button" class="ori-guide__action" data-ori-action="' +
      esc(action.type) +
      '" data-ori-index="' +
      index +
      '">' +
      label +
      '</button>'
    );
  }

  function render(resp) {
    var els = state.els;
    if (!els) return;
    state.response = resp;
    state.responseKey = helpContextKey();

    if (typeof resp.about === 'string' && els.about) els.about.textContent = resp.about;
    state.screenRoute = currentRoute();
    state.screenTitle = String(resp.location || 'This screen');
    refreshContextLabel();
    var actions = [];
    var raw = Array.isArray(resp.actions) ? resp.actions : [];
    for (var i = 0; i < raw.length; i++) {
      var valid = validateAction(raw[i]);
      if (valid) actions.push(valid);
    }
    state.actions = actions;

    var html = '';
    if (resp.location) {
      html +=
        '<p class="ori-guide__location">You are on <strong>' + esc(resp.location) + '</strong></p>';
    }
    html += '<p class="ori-guide__answer">' + esc(resp.answer || '') + '</p>';

    if (actions.length) {
      html += '<div class="ori-guide__actions">';
      for (var j = 0; j < actions.length; j++) html += actionHTML(actions[j], j);
      html += '</div>';
    }

    var suggested = Array.isArray(resp.suggested) ? resp.suggested : [];
    var topicsHTML = '';
    for (var k = 0; k < suggested.length; k++) {
      topicsHTML +=
        '<button type="button" class="ori-guide__topic" data-ori-topic="' +
        esc(suggested[k].label) +
        '">' +
        esc(suggested[k].label) +
        '</button>';
    }
    if (els.topics) els.topics.innerHTML = topicsHTML;
    else if (topicsHTML) html += '<div class="ori-guide__topics">' + topicsHTML + '</div>';

    els.reply.innerHTML = html;
    els.reply.dataset.status = String(resp.status || '');
    els.reply.dataset.topic = String(resp.topic_key || '');
  }

  /* ---- search status ---------------------------------------------------------- */

  // `silent` marks the request the panel fires for itself when it opens.
  //
  // That request must not disable the send control: a browser will not submit a
  // form whose submit button is disabled, so a user who opens the guide and
  // immediately types a question and presses Enter would have it silently
  // swallowed. Only a request the user actually made shows a busy control.
  function setPending(pending, silent) {
    state.pending = pending;
    var els = state.els;
    if (!els) return;
    if (!silent && els.send) {
      els.send.disabled = pending;
    }
    els.panel.dataset.state = pending ? 'pending' : 'ready';
    if (pending && !silent) {
      els.reply.innerHTML = '<p class="ori-guide__answer is-pending">Searching help…</p>';
    }
    // The launcher shows that something is running even while the panel is
    // closed, without becoming a second place to type (FR14).
    if (els.launcherStatus && !silent) {
      els.launcherStatus.hidden = !pending;
      els.launcherStatus.textContent = pending ? 'Working…' : '';
    }
    if (els.launcher) {
      els.launcher.dataset.busy = pending ? 'true' : 'false';
    }
  }

  /* ---- requests ----------------------------------------------------------------- */

  // Latest search wins. There is deliberately no routing endpoint or work
  // controller dependency here, including for unknown text and slash commands.
  function ask(question, options) {
    var silent = !!(options && options.silent);
    if (!silent && state.quest) clearQuestStep({ keepOpen: true });
    var seq = ++state.seq;
    state.response = null;
    var contextKey = helpContextKey();
    state.contextKey = contextKey;
    question = Array.from(String(question || '').trim())
      .slice(0, 400)
      .join('');
    setPending(true, silent);
    state.greeted = true;
    if (!silent) state.draft = String(question || '').slice(0, 400);

    return fetch(ENDPOINT, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ question: question, route: currentRoute() })
    })
      .then(function (r) {
        if (!r.ok) throw new Error('guide ' + r.status);
        return r.json();
      })
      .then(function (resp) {
        if (seq !== state.seq) return;
        if (contextKey !== helpContextKey()) {
          invalidateInFlightForContextChange(contextKey);
          return;
        }

        setPending(false, silent);
        render(resp);
        // The topic *key* only — never the question the user typed.
        emit('answer', {
          status: String(resp.status || ''),
          topic: String(resp.topic_key || ''),
          actions: Array.isArray(resp.actions) ? resp.actions.length : 0
        });
      })
      .catch(function () {
        if (seq !== state.seq) return;
        if (contextKey !== helpContextKey()) {
          invalidateInFlightForContextChange(contextKey);
          return;
        }
        setPending(false, silent);
        // The guide failing must never block the page underneath it (FR-50).
        if (state.els) {
          state.els.reply.dataset.status = 'unavailable';
          state.els.reply.innerHTML =
            '<p class="ori-guide__answer">Help is unavailable right now. ' +
            'Your search text is kept. The page still works — try Search again.</p>';
        }
        emit('fallback', { reason: 'unreachable' });
      });
  }

  /* ---- coachmarks ---------------------------------------------------------------- */

  // The pointer is decoration layered over the page, so it has to be torn down
  // as deliberately as it is built: an orphaned pointer would sit over the UI
  // aiming at a control that is no longer there.
  function clearPointer() {
    if (state.pointerTrack) {
      state.pointerTrack.stopped = true;
      if (typeof window.cancelAnimationFrame === 'function' && state.pointerTrack.frame) {
        window.cancelAnimationFrame(state.pointerTrack.frame);
      }
      state.pointerTrack = null;
    }
    if (state.pointerEl) {
      if (state.pointerEl.parentNode) {
        state.pointerEl.parentNode.removeChild(state.pointerEl);
      }
      state.pointerEl = null;
    }
  }

  // Parks the pointer against the target, in page coordinates, so it survives
  // scrolling without being re-created.
  //
  // Just inside the control's lower edge, near its leading edge rather than its
  // middle: a hand overlapping the thing it points at reads as "this one" the
  // way a hand floating in the margin does not, and staying inside the control's
  // own box keeps it off a modal's backdrop when the target is a dialog button.
  function positionPointer(el) {
    if (!state.pointerEl || !el || typeof el.getBoundingClientRect !== 'function') return;
    var rect = el.getBoundingClientRect();
    var scrollX = window.pageXOffset || 0;
    var scrollY = window.pageYOffset || 0;
    var inset = Math.min(rect.width / 2, 52);
    var x = rect.left + scrollX + inset;
    var y = rect.bottom + scrollY - 12;
    state.pointerEl.style.left = Math.max(0, x) + 'px';
    state.pointerEl.style.top = Math.max(0, y) + 'px';
  }

  // Keeps the pointer parked on its target for as long as it is up.
  //
  // A frame loop rather than scroll/resize listeners: the Map animates its
  // camera into place after mount and pans/zooms under its own transform, none
  // of which fires a scroll or resize event. A pointer positioned once would be
  // left behind at wherever the tile happened to be mid-animation.
  function trackPointer() {
    if (typeof window.requestAnimationFrame !== 'function') return;
    var track = { stopped: false, frame: 0 };
    state.pointerTrack = track;
    var step = function () {
      if (track.stopped || state.pointerTrack !== track) return;
      if (state.coachmarkEl) {
        // The Map re-mounts its tiles when HQ status arrives, which swaps the
        // marked node for an identical new one. Left alone, the mark would hold
        // a detached node whose rect is all zeros and the pointer would sit in
        // the page corner aiming at nothing.
        if (!reanchorCoachmark()) return;
        positionPointer(state.coachmarkEl);
      }
      track.frame = window.requestAnimationFrame(step);
    };
    track.frame = window.requestAnimationFrame(step);
  }

  // A game-style "click here" hand at the marked control. It is aria-hidden and
  // never focusable: the panel copy already names the control in words, so the
  // pointer adds emphasis, never the only signal (FR-118).
  function applyPointer(el) {
    clearPointer();
    if (!el || !document.body) return;
    var pointer = document.createElement('div');
    pointer.className = 'ori-pointer';
    pointer.setAttribute('aria-hidden', 'true');
    pointer.innerHTML =
      '<span class="ori-pointer__hand">' +
      '<svg viewBox="0 0 24 24" width="26" height="26" focusable="false" aria-hidden="true">' +
      '<path fill="currentColor" d="M9 11.5V5.2a1.7 1.7 0 0 1 3.4 0v5.1h.8V7.1a1.6 1.6 0 0 1 3.2 0v3.2h.8V8.6a1.5 1.5 0 0 1 3 0v6.6a5.3 5.3 0 0 1-5.3 5.3h-2.2a5 5 0 0 1-3.8-1.8l-3.3-4a1.5 1.5 0 0 1 2.1-2.1z"/>' +
      '</svg>' +
      '</span>';
    document.body.appendChild(pointer);
    state.pointerEl = pointer;
    positionPointer(el);
    trackPointer();
  }

  function clearCoachmark() {
    cancelCoachmarkWait();
    clearPointer();
    if (state.coachmarkEl) {
      state.coachmarkEl.classList.remove('is-ori-coachmark');
      state.coachmarkEl = null;
      state.coachmarkRoute = '';
      state.coachmarkKey = '';
    }
    state.coachmarkHeldFocus = false;
  }

  // A mark made on one route must not survive onto another. Pages here change
  // the URL without reloading (the Agents collection keeps filters in history),
  // so a mark can outlive the view that justified it (FR-43).
  function clearCoachmarkIfRouteChanged() {
    if (!state.coachmarkEl) return;
    if (state.coachmarkRoute && state.coachmarkRoute !== currentRoute()) {
      clearCoachmark();
    } else if (!document.contains(state.coachmarkEl)) {
      // Or the element itself was re-rendered out from under the mark.
      clearCoachmark();
    }
  }

  function coachmarkMissNote() {
    if (!state.els) return;
    state.els.reply.insertAdjacentHTML(
      'beforeend',
      '<p class="ori-guide__answer ori-guide__answer--note">' +
        'I cannot point at that control from this view. Use the destination above instead.' +
        '</p>'
    );
  }

  function cancelCoachmarkWait() {
    if (state.coachmarkWait) {
      if (typeof window.clearTimeout === 'function') window.clearTimeout(state.coachmarkWait);
      state.coachmarkWait = null;
    }
  }

  // Whether focus is on the marked element, or on a control inside it (a
  // group's coachmark focuses one of its controls).
  function coachmarkHoldsFocus(el) {
    var active = document.activeElement;
    if (!active) return false;
    return active === el || (typeof el.contains === 'function' && el.contains(active));
  }

  // Focuses what a coachmark focuses: the control itself, or the control inside
  // a group that Tab would reach (OriGuideCoachmarks.focusTarget).
  function focusCoachmark(key, el, preventScroll) {
    var registry = window.OriGuideCoachmarks;
    var target =
      registry && typeof registry.focusTarget === 'function' ? registry.focusTarget(key, el) : el;
    if (target && typeof target.focus === 'function')
      target.focus({ preventScroll: preventScroll });
  }

  // Keeps the mark on the live node when the page re-renders the control under
  // it. Returns false when the control is genuinely gone, in which case the
  // mark (and its pointer) are dropped rather than left pointing at a ghost.
  function reanchorCoachmark() {
    var el = state.coachmarkEl;
    if (!el) return false;
    if (typeof document.contains !== 'function' || document.contains(el)) {
      // Remembered every frame, because once the node is swapped out it is too
      // late to ask whether it had focus.
      state.coachmarkHeldFocus = coachmarkHoldsFocus(el);
      return true;
    }

    var registry = window.OriGuideCoachmarks;
    var fresh =
      state.coachmarkKey &&
      registry &&
      registry.resolve(state.coachmarkKey, currentRoute(), document);
    if (!fresh || fresh === el) {
      clearCoachmark();
      return false;
    }
    el.classList.remove('is-ori-coachmark');
    fresh.classList.add('is-ori-coachmark');
    state.coachmarkEl = fresh;
    // Removing the focused node drops focus to <body>, so a keyboard user Ori
    // had placed on the control would lose their place. Focus moves with the
    // mark only when nothing else has it: it never leaves a control the user
    // moved to.
    var active = document.activeElement;
    if (state.coachmarkHeldFocus && (!active || active === document.body)) {
      focusCoachmark(state.coachmarkKey, fresh, true);
    }
    return true;
  }

  // opts.focus === false marks and points without moving focus. A walkthrough
  // passes it for a step the form itself advanced to, while the user is still
  // working in that form: moving focus there would carry their next keystroke
  // somewhere else, and a Space landing on a marked button presses it.
  function markCoachmark(key, el, opts) {
    state.coachmarkKey = key;
    el.classList.add('is-ori-coachmark');
    state.coachmarkEl = el;
    // The route the mark belongs to. If the page's route changes underneath it,
    // the mark is stale and gets cleared rather than left pointing at whatever
    // now occupies that selector (FR-43).
    state.coachmarkRoute = currentRoute();
    applyPointer(el);
    emit('coachmark', { key: key, resolved: true });
    var moveFocus = !(opts && opts.focus === false);
    if (moveFocus) focusCoachmark(key, el, false);
    state.coachmarkHeldFocus = coachmarkHoldsFocus(el);
    if (typeof el.scrollIntoView === 'function') {
      el.scrollIntoView({ block: 'center', behavior: 'smooth' });
    }
  }

  // How long a walkthrough step will wait for a control it knows is about to
  // mount. Bounded: an absent control still degrades to words, just not before
  // the UI it names has had a chance to render.
  var COACHMARK_WAIT_MS = 120;
  var COACHMARK_WAIT_TRIES = 8;

  // Mark and focus. Never click, never submit, never change a value (FR-42).
  //
  // opts.awaitTarget: retry briefly for a control that is not in the document
  // yet. Walkthrough steps set this because the step that names a control is
  // presented in the same tick as the dialog that mounts it — resolving once,
  // immediately, would degrade to "I cannot point at that" for a control that
  // appears a frame later. Guide answers do not set it: there, an absent
  // control really is absent, and the honest answer is the immediate one.
  function applyCoachmark(key, opts) {
    clearCoachmark();
    var registry = window.OriGuideCoachmarks;
    var el = registry && registry.resolve(key, currentRoute(), document);

    if (el) {
      markCoachmark(key, el, opts);
      return true;
    }

    var canWait = opts && opts.awaitTarget && typeof window.setTimeout === 'function';
    if (!canWait) {
      // A stale or absent target degrades to an explanation rather than
      // silently doing nothing (FR-43).
      coachmarkMissNote();
      emit('coachmark', { key: key, resolved: false });
      return false;
    }

    // The route this wait belongs to. Resolving onto a different page would
    // mark whatever now happens to match the selector.
    var waitRoute = currentRoute();
    var tries = 0;
    var attempt = function () {
      state.coachmarkWait = null;
      if (currentRoute() !== waitRoute) {
        emit('coachmark', { key: key, resolved: false });
        return;
      }
      var found = registry && registry.resolve(key, currentRoute(), document);
      if (found) {
        markCoachmark(key, found, opts);
        return;
      }
      tries += 1;
      if (tries >= COACHMARK_WAIT_TRIES) {
        coachmarkMissNote();
        emit('coachmark', { key: key, resolved: false });
        return;
      }
      state.coachmarkWait = window.setTimeout(attempt, COACHMARK_WAIT_MS);
    };
    state.coachmarkWait = window.setTimeout(attempt, COACHMARK_WAIT_MS);
    return false;
  }

  /* ---- work handoff ---------------------------------------------------------------- */

  // Compatibility key: recovery belongs to the assistant. Help never consumes
  // stored work text, searches it, or puts private text into a URL.
  var HANDOFF_KEY = 'ori-guide-handoff';

  // Only a user's explicit action reaches this seam. The assistant's existing
  // suggestion API refuses non-empty drafts, busy replies and loading context.
  async function retryAssistantStatus(panel) {
    // Retry only readiness. A subsequent explicit Open click is still
    // required to move text; retry is never execution approval.
    try {
      await panel?.refresh?.();
    } catch (_) {
      /* Readiness is unavailable. */
    }
    if (!state.open || state.quest) return;
    if (state.els?.reply)
      state.els.reply.insertAdjacentHTML(
        'beforeend',
        '<p class="ori-guide__answer ori-guide__answer--note">Assistant status was checked. Use an available setup or Open action to continue. Your search is kept; nothing was sent.</p>'
      );
  }

  function handoff(text) {
    var panel = window.PersonalAssistantPanel;
    var accepted = false;
    if (!state.assistantAvailable && !assistantSetupAction()) {
      void retryAssistantStatus(panel);
      return false;
    }
    if (state.assistantAvailable && panel?.open && panel?.suggestReply) {
      if (panel.open(state.els?.launcher)) {
        accepted =
          panel.suggestReply(
            Array.from(String(text || '').trim())
              .slice(0, 400)
              .join('')
          ) === true;
        if (!accepted) open(state.els?.launcher, { skipGreeting: true });
      }
    }
    if (!accepted && state.els?.reply) {
      var reason =
        panel?.suggestionNotice?.() ||
        'Finish setup or the current reply, then open your assistant to review it.';
      state.els.reply.insertAdjacentHTML(
        'beforeend',
        '<p class="ori-guide__answer ori-guide__answer--note">The request could not be inserted. Your search text and assistant draft are kept. ' +
          esc(reason) +
          ' Nothing was sent.</p>'
      );
    }
    emit('handoff', { accepted: accepted, submitted: false });
    return accepted;
  }

  /* ---- open / close ------------------------------------------------------------------ */

  function open(trigger, options) {
    if (state.open) return;
    if (
      window.PersonalAssistantPanel &&
      typeof window.PersonalAssistantPanel.close === 'function'
    ) {
      window.PersonalAssistantPanel.close({ restoreFocus: false });
    }
    state.open = true;
    state.lastTrigger = trigger || document.activeElement || null;

    var els = state.els;
    if (!els) return;
    els.panel.hidden = false;
    els.launcher.setAttribute('aria-expanded', 'true');

    // Restore rather than reset: reopening must bring back the draft and the
    // reply that were there, not throw away work in progress (FR45).
    els.input.value = state.draft || '';
    refreshContextLabel();

    // Only greet on a genuinely fresh panel. Re-greeting would wipe a reply the
    // user reopened specifically to read, and would fire a request for a
    // question nobody asked. A caller about to present its own fixed content
    // (the guided HQ quest) opts out via skipGreeting — the async greeting
    // fetch would otherwise land after and silently overwrite that content.
    // A walkthrough step already on the panel is the same fixed content: the
    // user reopening a panel they closed mid-walkthrough must find the step,
    // not a greeting that replaced it.
    if (!state.greeted && !state.pending && !state.quest && !(options && options.skipGreeting)) {
      // Silent: this is the panel greeting itself, not a question the user asked.
      ask('', { silent: true });
    }

    // Focus the input rather than the close button: the user opened this to ask
    // something.
    if (state.quest) els.reply.querySelector?.('[data-ori-quest-choice]')?.focus();
    else if (typeof els.input.focus === 'function') els.input.focus();
    emit('open', { route: currentRoute() });
  }

  function close(options) {
    if (!state.open) return;
    state.open = false;
    clearCoachmark();

    var els = state.els;
    if (!els) return;
    // Keep the draft. Closing is presentation only: it never cancels a submitted
    // request, and it must not silently discard what the user was typing (FR13).
    state.draft = String((els.input && els.input.value) || '');
    els.panel.hidden = true;
    els.launcher.setAttribute('aria-expanded', 'false');

    // Return focus to whatever opened the guide, when it still exists (FR-26).
    var trigger = state.lastTrigger;
    state.lastTrigger = null;
    if (!options || options.restoreFocus !== false) {
      if (trigger && document.contains(trigger) && typeof trigger.focus === 'function') {
        trigger.focus();
      } else if (els.launcher && typeof els.launcher.focus === 'function') {
        els.launcher.focus();
      }
    }
    emit('dismiss', {});
  }

  function toggle(trigger) {
    if (state.open) close();
    else open(trigger);
  }

  /* ---- PAF Help-only mode -------------------------------------------------------------- */

  function setHelpOnly(options) {
    var opts = options || {};
    var previous =
      state.assistantState + ':' + state.assistantName + ':' + state.assistantAvailable;
    state.helpOnly = true;
    // Options without a state retain the established context-only API contract.
    state.assistantState = String(
      opts.state ||
        (opts.available === true ? 'active' : opts.needsHQ === true ? 'needs_hq' : 'needs_hire')
    );
    state.assistantAvailable =
      opts.available === true && ['active', 'paused'].indexOf(state.assistantState) !== -1;
    state.needsHQ = ['needs_hq', 'provisioning_hq'].indexOf(state.assistantState) !== -1;
    state.assistantName = String(opts.assistantName || 'your personal assistant').trim();
    var els = state.els;
    if (!els) return;
    if (els.walkthroughs) {
      var setup = assistantSetupAction();
      if (setup) {
        els.walkthroughs.innerHTML =
          '<a class="ori-guide__walkthrough" href="' +
          esc(setup.href) +
          '">' +
          esc(setup.label) +
          '</a>';
      } else {
        els.walkthroughs.textContent = state.assistantAvailable
          ? 'No setup walkthrough is currently available.'
          : state.assistantState === 'loading'
            ? 'Checking walkthrough availability…'
            : 'Walkthrough availability could not be checked. Retry assistant status from a work-related result.';
      }
    }
    if (
      previous !==
        state.assistantState + ':' + state.assistantName + ':' + state.assistantAvailable &&
      state.response &&
      !state.quest &&
      state.responseKey === helpContextKey()
    )
      render(state.response);
    emit('help-only', { assistantAvailable: state.assistantAvailable });
  }

  /* ---- fixed quest presentation ------------------------------------------------------- */

  /*
   * A fixed quest is a deterministic, host-authored walkthrough presented
   * through Ori's own panel: the Personal HQ setup quest is the first one.
   *
   * It is deliberately its own narrow API rather than a call into ask():
   *   - the user did not ask a question, so pretending they did would put words
   *     in their mouth and record a turn that never happened;
   *   - no request reaches /api/ori-guide and no model is involved, so a quest
   *     step works with no provider configured at all; and
   *   - a step's vocabulary is copy plus one registered coachmark plus labelled
   *     choices. There is no field able to express a click, a submit, a form
   *     open, a navigation, or any other mutation — the caller is told which
   *     choice the user pressed and does the work itself, under its own gates.
   *
   * Copy is escaped on the way in, so a user-controlled assistant name renders
   * as text and can never become markup.
   */
  function presentQuestStep(step) {
    var els = state.els;
    if (!els || !step || typeof step !== 'object') return { rendered: false };

    var quest = String(step.quest || '').trim();
    if (!quest) return { rendered: false };

    var index = Number(step.index);
    var total = Number(step.total);
    var html = '';
    if (isFinite(index) && isFinite(total) && index > 0 && total > 0) {
      html +=
        '<p class="ori-guide__quest-step">Step ' +
        esc(String(index)) +
        ' of ' +
        esc(String(total)) +
        '</p>';
    }
    html += '<p class="ori-guide__answer">' + esc(String(step.answer || '')) + '</p>';
    if (step.note) {
      html +=
        '<p class="ori-guide__answer ori-guide__answer--note">' + esc(String(step.note)) + '</p>';
    }

    var choices = Array.isArray(step.choices) ? step.choices : [];
    var rendered = [];
    var choicesHTML = '';
    for (var i = 0; i < choices.length; i++) {
      var id = String((choices[i] && choices[i].id) || '').trim();
      var label = String((choices[i] && choices[i].label) || '').trim();
      if (!id || !label) continue;
      rendered.push(id);
      choicesHTML +=
        '<button type="button" class="ori-guide__action ori-guide__quest-choice" ' +
        'data-ori-quest="' +
        esc(quest) +
        '" data-ori-quest-choice="' +
        esc(id) +
        '">' +
        esc(label) +
        '</button>';
    }
    if (choicesHTML) {
      html += '<div class="ori-guide__actions">' + choicesHTML + '</div>';
    }

    // Fixed content invalidates pending searches before taking ownership.
    state.seq += 1;
    setPending(false, false);
    if (els.overview) els.overview.hidden = true;
    if (els.form) els.form.hidden = true;
    if (els.hint) els.hint.hidden = true;
    if (els.portrait) els.portrait.hidden = false;
    if (els.title) els.title.textContent = 'Ori walkthrough';
    state.actions = [];
    state.response = null;
    els.reply.innerHTML = html;
    els.reply.dataset.status = 'quest';
    els.reply.dataset.topic = '';
    els.reply.dataset.quest = quest;
    state.quest = { quest: quest, choices: rendered };

    var coachmarkResolved = false;
    if (step.coachmark) {
      // awaitTarget: a step is presented in the same tick as the dialog whose
      // control it names, so the control may be one frame away from existing.
      // step.focus === false keeps focus where the user is working (see
      // markCoachmark).
      coachmarkResolved = applyCoachmark(String(step.coachmark), {
        awaitTarget: true,
        focus: step.focus !== false
      });
    } else {
      clearCoachmark();
    }
    emit('quest-step', { quest: quest, index: index, coachmark: coachmarkResolved });
    return { rendered: true, coachmarkResolved: coachmarkResolved };
  }

  // clearQuestStep ends the presentation: the mark goes away and the panel stops
  // claiming to be mid-walkthrough. It says nothing about the server-side quest,
  // which is only ever completed by a real designation or skipped explicitly.
  function clearQuestStep(options) {
    var hadQuest = Boolean(state.quest);
    clearCoachmark();
    state.quest = null;
    if (state.els) {
      if (state.els.overview) state.els.overview.hidden = false;
      if (state.els.form) state.els.form.hidden = false;
      if (state.els.hint) state.els.hint.hidden = false;
      if (state.els.portrait) state.els.portrait.hidden = true;
      if (state.els.title) state.els.title.textContent = 'Help';
    }
    if (state.els && state.els.reply && state.els.reply.dataset.status === 'quest') {
      state.els.reply.innerHTML = '';
      state.els.reply.dataset.status = '';
      state.els.reply.dataset.quest = '';
    }
    // A finished/deferred fixed sheet must not cover the next real app control.
    // Ordinary Help remains available from its navbar entry; no quest is mutated.
    if (hadQuest && !options?.keepOpen) close();
  }

  /* ---- wiring ------------------------------------------------------------------------- */

  function onQuestChoiceClick(event) {
    var el = event.target.closest('[data-ori-quest-choice]');
    if (!el) return;
    event.preventDefault();
    var id = el.getAttribute('data-ori-quest-choice');
    var quest = el.getAttribute('data-ori-quest');
    // Only a choice this panel actually rendered for the active quest counts, so
    // stale markup cannot fire a step the controller is no longer waiting on.
    if (!state.quest || state.quest.quest !== quest) return;
    if (state.quest.choices.indexOf(id) === -1) return;
    emit('quest-choice', { quest: quest, choice: id });
  }

  function onActionClick(event) {
    var el = event.target.closest('[data-ori-action]');
    if (!el) return;
    var type = el.getAttribute('data-ori-action');
    var index = Number(el.getAttribute('data-ori-index'));
    var action = (state.actions || [])[index];
    if (!action || action.type !== type) return;

    if (type === 'navigate' || type === 'setup') {
      // Let the browser follow the link normally.
      return;
    }
    event.preventDefault();

    if (type === 'coachmark') applyCoachmark(action.coachmark);
    else if (type === 'handoff') handoff(action.handoffText);
    else if (type === 'reset') ask('');
    else if (type === 'dismiss') close();
  }

  function onTopicClick(event) {
    var el = event.target.closest('[data-ori-topic]');
    if (!el) return;
    event.preventDefault();
    var topic = el.getAttribute('data-ori-topic');
    if (state.els) state.els.input.value = topic;
    ask(topic);
  }

  function onKeydown(event) {
    if (event.key !== 'Escape' || !state.open) return;
    // First Escape clears a coachmark, second closes the guide — so dismissing
    // guidance does not also lose the panel the user is reading (FR-24).
    if (state.coachmarkEl) {
      clearCoachmark();
      event.stopPropagation();
      return;
    }
    close();
    event.stopPropagation();
  }

  function init() {
    var panel = document.getElementById('oriGuidePanel');
    var launcher = document.getElementById('oriGuideLauncher');
    if (!panel || !launcher) return;

    state.els = {
      panel: panel,
      launcher: launcher,
      input: document.getElementById('oriGuideInput'),
      send: document.getElementById('oriGuideSend'),
      form: document.getElementById('oriGuideForm'),
      reply: document.getElementById('oriGuideReply'),
      close: document.getElementById('oriGuideClose'),
      context: document.getElementById('oriGuideContext'),
      title: document.getElementById('oriGuideTitle'),
      overview: document.getElementById('oriGuideOverview'),
      about: document.getElementById('oriGuideAbout'),
      topics: document.getElementById('oriGuideTopics'),
      walkthroughs: document.getElementById('oriGuideWalkthroughs'),
      portrait: document.getElementById('oriGuideQuestPortrait'),
      hint: document.getElementById('oriGuideSearchHint')
    };

    refreshContextLabel();
    setHelpOnly({
      available: state.assistantAvailable,
      needsHQ: state.needsHQ,
      assistantName: state.assistantName,
      state: state.assistantState
    });

    launcher.addEventListener('click', function () {
      toggle(launcher);
    });
    if (state.els.close) {
      state.els.close.addEventListener('click', function () {
        close();
      });
    }
    if (state.els.form) {
      state.els.form.addEventListener('submit', function (event) {
        event.preventDefault();
        ask(String(state.els.input.value || '').trim());
      });
    }
    panel.addEventListener('click', onActionClick);
    panel.addEventListener('click', onTopicClick);
    panel.addEventListener('click', onQuestChoiceClick);
    document.addEventListener('keydown', onKeydown);
    window.addEventListener('popstate', clearCoachmarkIfRouteChanged);
    // A route change must repaint the visible context before the next request is
    // accepted, so a stale workspace or task is never submitted invisibly (FR46).
    state.contextKey = helpContextKey();
    function onContextChange() {
      invalidateInFlightForContextChange(state.contextKey);
      state.contextKey = helpContextKey();
      refreshContextLabel();
    }
    window.addEventListener('popstate', onContextChange);
    document.addEventListener('ori:workspace-context', onContextChange);
  }

  var api = {
    init: init,
    open: open,
    close: close,
    toggle: toggle,
    ask: ask,
    // The seam page modules use to contribute context the URL cannot carry.
    setContext: setContext,
    setHelpOnly: setHelpOnly,
    // Deterministic host-authored walkthroughs. See presentQuestStep above for
    // why this is separate from ask() and what it deliberately cannot express.
    presentQuestStep: presentQuestStep,
    clearQuestStep: clearQuestStep,
    // Mark one registered control, for a page that wants to point at its own UI
    // without opening the guide panel — a first-visit hint, for instance.
    //
    // Public rather than another test seam because the alternative is a second
    // coachmark system: this one already re-anchors when the page re-renders
    // beneath the mark, waits a bounded time for a control that is about to
    // mount, clears itself when the route changes, and refuses any key that is
    // not in the hand-written registry. None of that is worth duplicating.
    //
    // It marks and focuses. It never clicks, never submits, never changes a
    // value (FR-42).
    markControl: applyCoachmark,
    clearControlMark: clearCoachmark,
    isOpen: function () {
      return state.open;
    },
    isPending: function () {
      return state.pending;
    },
    // Test seams.
    _collectContext: collectContext,
    _contextFromRoute: contextFromRoute,
    _contextLabel: contextLabel,
    _refreshContextLabel: refreshContextLabel,
    _state: state,
    _validateAction: validateAction,
    _onQuestChoiceClick: onQuestChoiceClick,
    _isSafeHref: isSafeHref,
    _handoff: handoff,
    _applyCoachmark: applyCoachmark,
    _clearCoachmarkIfRouteChanged: clearCoachmarkIfRouteChanged,
    _esc: esc,
    HANDOFF_KEY: HANDOFF_KEY
  };

  if (typeof window !== 'undefined') {
    window.OriGuide = api;
    if (typeof document !== 'undefined') {
      if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', init);
      } else {
        init();
      }
    }
  }
})();
