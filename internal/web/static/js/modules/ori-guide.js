/*
 * Ori Guide controller — the app's setup-and-navigation helper.
 *
 * Ori helps you find things, understand Ori's own concepts, and get set up. It
 * does not do work: a work request comes back as a handoff to Workspace
 * Manager, with the user's text prefilled and *not* submitted (PRD FR-40/FR-84).
 *
 * Everything rendered here is either escaped text or a typed action validated
 * against a client-side allowlist. The server's action union cannot express a
 * mutation, and this controller additionally refuses any action type it does
 * not recognise — so a compromised or buggy response cannot widen what the
 * guide can do (FR-36/FR-49).
 *
 * Opening and closing the guide is presentation only. It never clears Map/Tree
 * selection, checked agents, filters, or form drafts, because it does not touch
 * them (FR-25).
 */
(function () {
  'use strict';

  var ENDPOINT = '/api/ori-guide';
  var ROUTE_ENDPOINT = '/api/home-assistant/route';

  // The guide answers navigation deterministically and without a model. Anything
  // it identifies as work, or honestly cannot answer, escalates to the routing
  // contract instead — which is how one composer serves both without the guide
  // ever gaining a mutation path (FR22/FR27).
  var GUIDE_WORK_TOPIC = 'workspace-manager';

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
    actions: [],
    // Survives close/reopen: closing the panel is presentation only and must
    // never cancel submitted work or lose what the user had typed (FR13/FR45).
    activity: [],
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
    return true;
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
      return {
        type: type,
        label: String(action.label || 'Send this as work'),
        handoffText: String(action.handoff_text || action.handoffText || '')
      };
    }

    return { type: type, label: String(action.label || '') };
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
    els.context.textContent = contextLabel(ctx);
    return ctx;
  }

  // The seam page modules use to contribute context they alone know about.
  function setContext(partial) {
    var next = partial || {};
    if ('workspaceId' in next) pageContext.workspaceId = String(next.workspaceId || '');
    if ('taskId' in next) pageContext.taskId = String(next.taskId || '');
    if ('sessionId' in next) pageContext.sessionId = String(next.sessionId || '');
    if ('label' in next) pageContext.label = String(next.label || '');
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
    if (suggested.length) {
      html +=
        '<p class="ori-guide__suggest-label">I can explain:</p><div class="ori-guide__topics">';
      for (var k = 0; k < suggested.length; k++) {
        html +=
          '<button type="button" class="ori-guide__topic" data-ori-topic="' +
          esc(suggested[k].label) +
          '">' +
          esc(suggested[k].label) +
          '</button>';
      }
      html += '</div>';
    }

    els.reply.innerHTML = html;
    els.reply.dataset.status = String(resp.status || '');
    els.reply.dataset.topic = String(resp.topic_key || '');
  }

  /* ---- activity ---------------------------------------------------------------- */

  // One ordered record of the conversation: what was asked, and what came back
  // (FR40). Deliberately not a live region — the reply below it is what gets
  // announced, so a screen reader is not re-read the whole transcript on every
  // update (FR73).
  function recordActivity(kind, text) {
    // State first, and unconditionally: the panel's own "have we greeted yet"
    // and restore-on-reopen behaviour keys off this, so it must not depend on
    // whether the optional activity element happens to be present.
    state.activity.push({ kind: kind, text: String(text || '') });

    var els = state.els;
    if (!els || !els.activity || typeof document.createElement !== 'function') return;
    var entry = document.createElement('li');
    entry.className = 'ori-guide__activity-entry ori-guide__activity-entry--' + kind;
    entry.textContent = String(text || '');
    els.activity.appendChild(entry);
    els.activity.hidden = false;
    els.activity.scrollTop = els.activity.scrollHeight;
  }

  // Renders a routed (non-navigation) result: a human-readable summary of where
  // the request is going, plus the target, so the user sees the chosen workspace
  // or agent before anything consequential happens (FR34/FR86).
  function renderRouted(routed, guideResp) {
    var els = state.els;
    if (!els) return;

    var label = String(routed.intent_label || 'work request');
    var target = String(routed.matched_agent || routed.suggested_agent_name || '');
    var ctx = collectContext();

    var summary = 'I read that as a ' + label + '.';
    if (routed.workspace_recommended && !ctx.workspace_id) {
      summary += ' It needs a workspace — pick one, or create one, and I will take it from there.';
    } else if (target) {
      summary += ' ' + target + ' is the best fit for it.';
    }

    state.actions = [];
    var html =
      '<p class="ori-guide__routing" data-routing-intent="' +
      esc(String(routed.intent || '')) +
      '">' +
      esc(summary) +
      '</p>';

    if (ctx.workspace_id) {
      html +=
        '<p class="ori-guide__target">Target: <strong>' + esc(contextLabel(ctx)) + '</strong></p>';
    }

    // Routing plans; it does not run. Say so, so nobody reads a routing summary
    // as work already underway (FR35).
    html +=
      '<p class="ori-guide__answer ori-guide__answer--note">' + 'Nothing has run yet.' + '</p>';

    els.reply.innerHTML = html;
    els.reply.dataset.status = 'routed';
    els.reply.dataset.topic = '';
    // The reply below already shows this turn in full. Repeating it in the
    // activity log renders the same sentence twice on screen; the log's job is
    // the record of what was asked, not a second copy of the current answer.

    emit('routed', {
      intent: String(routed.intent || ''),
      policy: String(routed.routing_policy || ''),
      workspace: ctx.workspace_id ? 'present' : 'absent',
      guideStatus: String((guideResp && guideResp.status) || '')
    });
  }

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
    if (!silent) {
      els.send.disabled = pending;
    }
    els.panel.dataset.state = pending ? 'pending' : 'ready';
    if (pending && !silent) {
      els.reply.innerHTML = '<p class="ori-guide__answer is-pending">Working…</p>';
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

  // A newer question always supersedes an older one.
  //
  // Dropping a request while another is in flight looked like duplicate-submit
  // protection, but it silently discarded the user's actual question whenever
  // they typed faster than the panel's own opening request returned. Sequencing
  // instead means the latest question always wins and stale replies are
  // ignored, which is the property that actually matters.
  // Whether the guide's reply means "this was not a navigation question".
  //
  // The guide recognises a work request itself and answers with a handoff, and
  // reports an honest miss as `unknown`. Both are escalation signals, so the
  // client never has to guess at intent with its own keyword list.
  function needsWorkRouting(resp) {
    if (!resp) return false;
    if (String(resp.topic_key || '') === GUIDE_WORK_TOPIC) return true;
    return String(resp.status || '') === 'unknown';
  }

  // Whether the full work controller is on this page.
  //
  // dashboard.js owns the real planning, task-creation, agent-selection,
  // confirmation, and workspace-session flows, and renders them into the
  // activity host inside this panel. Where it is loaded we hand off to it
  // rather than reimplementing any of that here (FR31/FR36).
  function hasWorkController() {
    return Boolean(
      typeof window !== 'undefined' &&
      window.OriAskRouting &&
      typeof window.OriAskRouting.submit === 'function'
    );
  }

  // Hands a work request to the existing controller with this page's context.
  //
  // It renders into the activity host below the composer, so the request and
  // everything it produces stay in one panel (FR39).
  function delegateWork(question, seq) {
    var ctx = collectContext();

    // Acknowledge the handoff immediately, before awaiting the controller.
    //
    // Some fulfilment paths — a specialist handoff, a confirmation the user has
    // to answer — deliberately do not settle for a long time, and one of them
    // may never settle at all if the user walks away. Feedback that waits on
    // completion therefore leaves the panel looking like the request vanished.
    // The activity host below renders the real progress as it arrives.
    showActivityHost(true);
    var els = state.els;
    if (els) {
      els.reply.dataset.status = 'delegated';
      els.reply.innerHTML =
        '<p class="ori-guide__routing">' +
        esc('Working on it' + (ctx.workspace_id ? ' in ' + contextLabel(ctx) : '') + '.') +
        '</p><p class="ori-guide__answer ori-guide__answer--note">' +
        'Anything consequential still asks you to confirm below.</p>';
    }

    emit('routed', {
      intent: 'delegated',
      workspace: ctx.workspace_id ? 'present' : 'absent',
      via: 'work-controller'
    });

    return Promise.resolve(
      window.OriAskRouting.submit(question, {
        routeContext: ctx,
        openThinkingModal: false
      })
    ).then(function () {
      if (seq !== state.seq) return null;
      return true;
    });
  }

  function showActivityHost(visible) {
    var els = state.els;
    if (!els || !els.activityHost) return;
    els.activityHost.hidden = !visible;
  }

  // Escalates a non-navigation request to the routing contract.
  //
  // Routing only classifies and plans; it does not execute. Whatever comes back
  // is rendered as a summary plus the same validated actions, so a classification
  // can never become a state change on its own (FR35).
  function routeWork(question, seq) {
    return fetch(ROUTE_ENDPOINT, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ prompt: question, context: collectContext() })
    })
      .then(function (r) {
        if (!r.ok) throw new Error('route ' + r.status);
        return r.json();
      })
      .then(function (routed) {
        if (seq !== state.seq) return null; // superseded
        return routed;
      });
  }

  function ask(question, options) {
    var silent = !!(options && options.silent);
    var seq = ++state.seq;
    setPending(true, silent);
    if (!silent && question) {
      recordActivity('you', question);
    }

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
        if (seq !== state.seq) return; // superseded

        // A real question the guide did not own goes to routing instead of being
        // rendered as "I don't know" (FR22/FR38).
        if (!silent && question && needsWorkRouting(resp)) {
          // Where the full work controller exists, it does the real thing:
          // planning, agent selection, confirmations, workspace sessions.
          if (hasWorkController()) {
            var handed = delegateWork(question, seq);
            // The panel's own turn is over once the request is handed over; the
            // activity host owns the busy state from here. Holding the composer
            // disabled until a confirmation is answered would lock the user out
            // of the very panel they need to answer it in.
            setPending(false, silent);

            return handed.catch(function () {
              if (seq !== state.seq) return;
              var failedEls = state.els;
              if (failedEls) {
                failedEls.reply.dataset.status = 'unavailable';
                failedEls.reply.innerHTML =
                  '<p class="ori-guide__answer">I could not finish that just now. ' +
                  'Check the activity below — nothing runs without your confirmation.</p>';
              }
              emit('fallback', { reason: 'work-controller-failed' });
            });
          }

          return routeWork(question, seq)
            .then(function (routed) {
              if (routed === null || seq !== state.seq) return;
              setPending(false, silent);
              renderRouted(routed, resp);
            })
            .catch(function () {
              if (seq !== state.seq) return;
              // Routing is what failed, so say so and offer the guide's own
              // answer rather than pretending the request was navigation.
              setPending(false, silent);
              render(resp);
              emit('fallback', { reason: 'route-unavailable' });
            });
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
        if (seq !== state.seq) return; // superseded
        setPending(false, silent);
        // The guide failing must never block the page underneath it (FR-50).
        if (state.els) {
          state.els.reply.dataset.status = 'unavailable';
          state.els.reply.innerHTML =
            '<p class="ori-guide__answer">I could not reach the guide just now. ' +
            'The page behind me still works — try again in a moment.</p>';
        }
        emit('fallback', { reason: 'unreachable' });
      });
  }

  /* ---- coachmarks ---------------------------------------------------------------- */

  function clearCoachmark() {
    if (state.coachmarkEl) {
      state.coachmarkEl.classList.remove('is-ori-coachmark');
      state.coachmarkEl = null;
      state.coachmarkRoute = '';
    }
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

  // Mark and focus. Never click, never submit, never change a value (FR-42).
  function applyCoachmark(key) {
    clearCoachmark();
    var registry = window.OriGuideCoachmarks;
    var el = registry && registry.resolve(key, currentRoute(), document);

    if (!el) {
      // A stale or absent target degrades to an explanation rather than
      // silently doing nothing (FR-43).
      if (state.els) {
        state.els.reply.insertAdjacentHTML(
          'beforeend',
          '<p class="ori-guide__answer ori-guide__answer--note">' +
            'I cannot point at that control from this view. Use the destination above instead.' +
            '</p>'
        );
      }
      emit('coachmark', { key: key, resolved: false });
      return false;
    }

    el.classList.add('is-ori-coachmark');
    state.coachmarkEl = el;
    // The route the mark belongs to. If the page's route changes underneath it,
    // the mark is stale and gets cleared rather than left pointing at whatever
    // now occupies that selector (FR-43).
    state.coachmarkRoute = currentRoute();
    emit('coachmark', { key: key, resolved: true });
    if (typeof el.focus === 'function') el.focus({ preventScroll: false });
    if (typeof el.scrollIntoView === 'function') {
      el.scrollIntoView({ block: 'center', behavior: 'smooth' });
    }
    return true;
  }

  /* ---- work handoff ---------------------------------------------------------------- */

  // Where a pending handoff is parked while the browser navigates to Home.
  //
  // sessionStorage rather than a query parameter: the user's words are their
  // own, and a URL would put them in history, in the address bar, and in any
  // referrer. It is read once and cleared.
  var HANDOFF_KEY = 'ori-guide-handoff';

  function prefillWorkSurface(input, text) {
    input.value = text || '';
    input.focus();
    // An input event so any listener bound to the work surface sees the change
    // exactly as if the user had typed it.
    try {
      input.dispatchEvent(new Event('input', { bubbles: true }));
    } catch (err) {
      /* older browsers: the value is set either way */
    }
  }

  // Prefills the work request without submitting it: the user presses send, so
  // nothing is ever routed or executed on their behalf (FR36).
  //
  // The universal composer is the target now. There is no separate work surface
  // to travel to, so a handoff no longer navigates anywhere — which also means
  // it can no longer strand the request on a page that lacks the old Home input.
  function handoff(text) {
    var els = state.els;
    if (els && els.input) {
      prefillWorkSurface(els.input, text);
      emit('handoff', { onHome: true, inPanel: true });
      return true;
    }

    var input = document.getElementById('homeAssistantInput');
    if (input) {
      close();
      prefillWorkSurface(input, text);
      emit('handoff', { onHome: true });
      return true;
    }

    // Not on Home. Carry the request across the navigation rather than dropping
    // it and making the user retype — still without submitting anything.
    try {
      window.sessionStorage.setItem(HANDOFF_KEY, String(text || ''));
    } catch (err) {
      /* private mode or storage disabled: the user retypes, nothing breaks */
    }
    emit('handoff', { onHome: false });
    window.location.href = '/';
    return false;
  }

  // Consumes a handoff parked by a previous page. Runs once on load; the value
  // is removed immediately so a later reload does not resurrect it.
  function consumePendingHandoff() {
    var text = null;
    try {
      text = window.sessionStorage.getItem(HANDOFF_KEY);
      if (text !== null) window.sessionStorage.removeItem(HANDOFF_KEY);
    } catch (err) {
      return;
    }
    if (!text) return;

    // Parked by an older build that still navigated to Home. Restore it into the
    // universal composer as a draft so the request is not lost mid-upgrade.
    state.draft = String(text);
    if (state.els && state.els.input) {
      prefillWorkSurface(state.els.input, text);
      return;
    }

    var input = document.getElementById('homeAssistantInput');
    if (input) prefillWorkSurface(input, text);
  }

  /* ---- open / close ------------------------------------------------------------------ */

  function open(trigger) {
    if (state.open) return;
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
    // question nobody asked.
    if (!state.activity.length && !state.pending) {
      // Silent: this is the panel greeting itself, not a question the user asked.
      ask('', { silent: true });
    }

    // Focus the input rather than the close button: the user opened this to ask
    // something.
    if (typeof els.input.focus === 'function') els.input.focus();
    emit('open', { route: currentRoute() });
  }

  function close() {
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
    if (trigger && document.contains(trigger) && typeof trigger.focus === 'function') {
      trigger.focus();
    } else if (els.launcher && typeof els.launcher.focus === 'function') {
      els.launcher.focus();
    }
    emit('dismiss', {});
  }

  function toggle(trigger) {
    if (state.open) close();
    else open(trigger);
  }

  /* ---- wiring ------------------------------------------------------------------------- */

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
      activity: document.getElementById('oriGuideActivity'),
      launcherStatus: document.getElementById('oriGuideLauncherStatus'),
      // The work controller's render target, hidden until there is work to show.
      activityHost: document.getElementById('homeAssistantThinkingModal')
    };

    refreshContextLabel();

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
    document.addEventListener('keydown', onKeydown);
    window.addEventListener('popstate', clearCoachmarkIfRouteChanged);
    // A route change must repaint the visible context before the next request is
    // accepted, so a stale workspace or task is never submitted invisibly (FR46).
    window.addEventListener('popstate', refreshContextLabel);

    // A work request made on another page is prefilled here, once, without
    // being submitted.
    consumePendingHandoff();

    // Home also shows Ori on the map. Both entry points drive this one
    // controller and one panel, so they can never disagree about open state
    // (FR-21).
    var mapOri = document.getElementById('oriGuideMapTrigger');
    if (mapOri) {
      mapOri.addEventListener('click', function () {
        toggle(mapOri);
      });
    }
  }

  var api = {
    init: init,
    open: open,
    close: close,
    toggle: toggle,
    ask: ask,
    // The seam page modules use to contribute context the URL cannot carry.
    setContext: setContext,
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
    _needsWorkRouting: needsWorkRouting,
    _hasWorkController: hasWorkController,
    _refreshContextLabel: refreshContextLabel,
    _state: state,
    _validateAction: validateAction,
    _isSafeHref: isSafeHref,
    _handoff: handoff,
    _applyCoachmark: applyCoachmark,
    _clearCoachmarkIfRouteChanged: clearCoachmarkIfRouteChanged,
    _consumePendingHandoff: consumePendingHandoff,
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
