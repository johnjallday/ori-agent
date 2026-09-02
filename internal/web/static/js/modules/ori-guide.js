/*
 * Ask Ori controller — the one assistance surface, on every authenticated page.
 *
 * It answers questions AND takes work. There used to be two surfaces for that,
 * and users had to know which was which before typing; Issue #350 merged them.
 * Intent is inferred rather than chosen:
 *
 *   1. Ask the guide. It answers navigation and setup deterministically, with
 *      no model, and identifies a work request as such itself.
 *   2. Anything it does not own — a work request, or an honest miss — escalates
 *      to the routing contract, or straight to the full work controller where
 *      that is loaded (dashboard.js owns planning, agent selection,
 *      confirmations, and workspace sessions; none of it is reimplemented here).
 *   3. An explicit /task, /ask, or /note bypasses step 1 entirely, so an
 *      override is never reinterpreted as navigation.
 *
 * Automatic routing is not automatic authorization. Everything rendered here is
 * escaped text or a typed action validated against a client-side allowlist; the
 * guide's action union still cannot express a mutation, and every existing
 * confirmation still applies (FR27/FR35/FR36).
 *
 * Opening and closing is presentation only. It never clears Map/Tree selection,
 * checked agents, filters, or form drafts, and never cancels submitted work.
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

  // Explicit power-user overrides. Ordinary requests never need them — intent is
  // inferred — but where one is typed it must win outright (FR24).
  //
  // These have to bypass the guide entirely rather than merely being escalated
  // after it: "/ask what is a workspace" matches the guide's own workspace
  // topic, so asking the guide first would answer it as navigation and silently
  // discard the override.
  var EXPLICIT_COMMAND_RE = /^\/(task|ask|note)\b/i;

  function isExplicitCommand(text) {
    return EXPLICIT_COMMAND_RE.test(String(text || '').trim());
  }

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
    // The active deterministic walkthrough step, if any: { quest, choices }.
    // Presentation only — the server owns whether the quest is done or deferred.
    quest: null,
    // PAF turns this controller into Help-only. Work remains a typed handoff
    // preview and is never delegated from Ori's draft/transcript.
    helpOnly: false,
    assistantAvailable: false,
    assistantName: 'your personal assistant',
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
      var handoffText = String(action.handoff_text || action.handoffText || '').slice(0, 400);
      return {
        type: type,
        label: state.helpOnly
          ? state.assistantAvailable
            ? 'Send to ' + state.assistantName
            : 'Hire your personal assistant'
          : String(action.label || 'Send this as work'),
        handoffText: handoffText
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
  // Drops an in-flight request whose answer would now be read against a
  // different workspace.
  //
  // Request sequencing alone does not cover this: the sequence only advances
  // when a NEWER request is made, so a reply for workspace A that lands after
  // the user switched to workspace B would render as actionable B content
  // (FR17/FR46). Changing target invalidates the question that was asked about
  // the old one.
  function invalidateInFlightForContextChange(previousWorkspaceID) {
    var nextWorkspaceID = collectContext().workspace_id;
    if (previousWorkspaceID === nextWorkspaceID) return;
    if (!state.pending) return;

    state.seq += 1; // any reply already in flight is now stale
    setPending(false, false);
    if (state.els) {
      state.els.reply.dataset.status = 'context-changed';
      state.els.reply.innerHTML =
        '<p class="ori-guide__answer">The workspace changed while I was working, ' +
        'so I stopped rather than answer about a different one. Ask again here.</p>';
    }
    emit('context-invalidated', { from: previousWorkspaceID, to: nextWorkspaceID });
  }

  function setContext(partial) {
    var next = partial || {};
    var previousWorkspaceID = collectContext().workspace_id;

    if ('workspaceId' in next) pageContext.workspaceId = String(next.workspaceId || '');
    if ('taskId' in next) pageContext.taskId = String(next.taskId || '');
    if ('sessionId' in next) pageContext.sessionId = String(next.sessionId || '');
    if ('label' in next) pageContext.label = String(next.label || '');

    invalidateInFlightForContextChange(previousWorkspaceID);
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
  // Builds the choices offered when a request needs a workspace it does not
  // have.
  //
  // These are ordinary validated navigate actions — the same allowlist every
  // other action goes through — so offering a choice cannot widen what the panel
  // is able to do. Picking one takes the user to that workspace, where the
  // request can be made in a context that is visible to them (FR32/FR33).
  function workspaceChoiceActions(routed) {
    var resolution = routed && routed.workspace_resolution;
    var actions = [];

    var candidates = (resolution && resolution.candidates) || [];
    for (var i = 0; i < candidates.length && actions.length < 3; i++) {
      var candidate = candidates[i];
      if (!candidate || !candidate.id || !candidate.slug) continue;
      actions.push({
        type: 'navigate',
        label: 'Open ' + String(candidate.name || candidate.id),
        href: '/workspaces/' + encodeURIComponent(String(candidate.slug))
      });
    }

    // Nothing fit, so the honest next step is making somewhere for it to live.
    if (!actions.length && routed && routed.workspace_recommended) {
      actions.push({
        type: 'navigate',
        label: 'Choose or create a workspace',
        href: '/workspaces'
      });
    }
    return actions;
  }

  function renderRouted(routed, guideResp) {
    var els = state.els;
    if (!els) return;

    var label = String(routed.intent_label || 'work request');
    var target = String(routed.matched_agent || routed.suggested_agent_name || '');
    var ctx = collectContext();
    var resolution = routed.workspace_resolution || null;
    var resolutionState = String((resolution && resolution.state) || '');
    var needsTarget = routed.workspace_recommended && !ctx.workspace_id;

    var summary = 'I read that as a ' + label + '.';
    if (needsTarget && resolutionState === 'ambiguous') {
      summary += ' More than one workspace could be the right home for it, so I have not picked.';
    } else if (needsTarget) {
      summary += ' It needs a workspace — pick one, or create one, and I will take it from there.';
    } else if (target) {
      summary += ' ' + target + ' is the best fit for it.';
    }

    // Validate the offered choices through the same gate as any other action, so
    // a malformed or unexpected candidate never becomes a control.
    var choices = [];
    var raw = workspaceChoiceActions(routed);
    for (var i = 0; i < raw.length; i++) {
      var valid = validateAction(raw[i]);
      if (valid) choices.push(valid);
    }
    state.actions = choices;

    var html =
      '<p class="ori-guide__routing" data-routing-intent="' +
      esc(String(routed.intent || '')) +
      '" data-workspace-state="' +
      esc(resolutionState) +
      '">' +
      esc(summary) +
      '</p>';

    // The chosen target has to be visible before anything consequential runs —
    // whether it came from the page or from resolving a name in the request
    // (FR34/FR86).
    var resolvedName = String((resolution && resolution.selected_workspace_name) || '');
    if (ctx.workspace_id) {
      html +=
        '<p class="ori-guide__target">Target: <strong>' + esc(contextLabel(ctx)) + '</strong></p>';
    } else if (resolvedName && resolutionState === 'confident') {
      html +=
        '<p class="ori-guide__target">Target: <strong>Workspace: ' +
        esc(resolvedName) +
        '</strong></p>';
    }

    // Routing plans; it does not run. Say so, so nobody reads a routing summary
    // as work already underway (FR35).
    html +=
      '<p class="ori-guide__answer ori-guide__answer--note">' + 'Nothing has run yet.' + '</p>';

    if (choices.length) {
      html += '<div class="ori-guide__actions">';
      for (var j = 0; j < choices.length; j++) html += actionHTML(choices[j], j);
      html += '</div>';
    }

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
      workspaceState: resolutionState,
      choices: choices.length,
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

  // Reports a delegated request that failed, without making the caller wait on
  // it.
  //
  // ask() must not return the controller's promise: a fulfilment path can stay
  // unsettled indefinitely (an unanswered confirmation), and awaiting that would
  // hang every caller — including the panel's own request bookkeeping.
  function reportWorkFailure(promise, seq, reason) {
    promise.catch(function () {
      if (seq !== state.seq) return;
      var failedEls = state.els;
      if (failedEls) {
        failedEls.reply.dataset.status = 'unavailable';
        failedEls.reply.innerHTML =
          '<p class="ori-guide__answer">I could not finish that just now. ' +
          'Check the activity below — nothing runs without your confirmation.</p>';
      }
      emit('fallback', { reason: reason });
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

    // An explicit command goes straight to the work controller: the user has
    // already said what they want, so nothing may reinterpret it (FR24).
    if (!state.helpOnly && !silent && question && isExplicitCommand(question)) {
      if (hasWorkController()) {
        reportWorkFailure(delegateWork(question, seq), seq, 'explicit-command-failed');
        setPending(false, silent);
        return Promise.resolve();
      }

      // The commands operate on a workspace, and this page has no work
      // controller — say so rather than silently answering something else.
      setPending(false, silent);
      if (state.els) {
        state.els.reply.dataset.status = 'unavailable';
        state.els.reply.innerHTML =
          '<p class="ori-guide__answer">That command works inside a workspace. ' +
          'Open a workspace and try it there.</p>';
      }
      emit('fallback', { reason: 'explicit-command-unavailable' });
      return Promise.resolve();
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
          // In PAF, Ori is Help-only. Render the guide-owned handoff preview;
          // never route, plan, or submit from this draft/transcript.
          if (state.helpOnly) {
            setPending(false, silent);
            render(resp);
            emit('handoff-preview', {
              available: state.assistantAvailable,
              chars: String(question).length
            });
            return Promise.resolve();
          }
          // Where the full work controller exists, it does the real thing:
          // planning, agent selection, confirmations, workspace sessions.
          if (hasWorkController()) {
            // The panel's own turn is over once the request is handed over; the
            // activity host owns the busy state from here. Holding the composer
            // disabled until a confirmation is answered would lock the user out
            // of the very panel they need to answer it in.
            reportWorkFailure(delegateWork(question, seq), seq, 'work-controller-failed');
            setPending(false, silent);
            return Promise.resolve();
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
    if (state.helpOnly) {
      if (
        state.assistantAvailable &&
        window.PersonalAssistantPanel &&
        typeof window.PersonalAssistantPanel.prefill === 'function'
      ) {
        close();
        window.PersonalAssistantPanel.prefill(String(text || '').slice(0, 400));
        emit('handoff', { assistant: state.assistantName, submitted: false });
        return true;
      }
      if (state.els && state.els.reply) {
        state.els.reply.insertAdjacentHTML(
          'beforeend',
          '<p class="ori-guide__answer ori-guide__answer--note">Hire or repair your personal assistant before sending work. Nothing was submitted.</p>'
        );
      }
      emit('handoff', { available: false, submitted: false });
      return false;
    }

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
    if (
      window.PersonalAssistantPanel &&
      typeof window.PersonalAssistantPanel.close === 'function'
    ) {
      window.PersonalAssistantPanel.close();
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

  /* ---- PAF Help-only mode -------------------------------------------------------------- */

  function setHelpOnly(options) {
    var opts = options || {};
    state.helpOnly = true;
    state.assistantAvailable = opts.available === true;
    state.assistantName = String(opts.assistantName || 'your personal assistant').trim();
    var els = state.els;
    if (!els) return;
    if (els.title) els.title.textContent = 'Ori';
    if (els.role) els.role.hidden = false;
    if (els.launcherName) els.launcherName.textContent = 'Ori Help';
    if (els.input) {
      els.input.placeholder = 'Ask Ori about the app';
      els.input.maxLength = 400;
      els.input.setAttribute('aria-label', 'Ask Ori about the app');
    }
    if (els.launcher) els.launcher.setAttribute('aria-label', 'Open Ori Help — App Guide');
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

    // A fixed step owns the panel body, so any leftover answer or action from an
    // earlier turn cannot be mistaken for part of the walkthrough.
    state.actions = [];
    els.reply.innerHTML = html;
    els.reply.dataset.status = 'quest';
    els.reply.dataset.topic = '';
    els.reply.dataset.quest = quest;
    state.quest = { quest: quest, choices: rendered };

    var coachmarkResolved = false;
    if (step.coachmark) {
      coachmarkResolved = applyCoachmark(String(step.coachmark));
    } else {
      clearCoachmark();
    }
    emit('quest-step', { quest: quest, index: index, coachmark: coachmarkResolved });
    return { rendered: true, coachmarkResolved: coachmarkResolved };
  }

  // clearQuestStep ends the presentation: the mark goes away and the panel stops
  // claiming to be mid-walkthrough. It says nothing about the server-side quest,
  // which is only ever completed by a real designation or skipped explicitly.
  function clearQuestStep() {
    clearCoachmark();
    state.quest = null;
    if (state.els && state.els.reply && state.els.reply.dataset.status === 'quest') {
      state.els.reply.innerHTML = '';
      state.els.reply.dataset.status = '';
      state.els.reply.dataset.quest = '';
    }
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
      role: document.getElementById('oriGuideRole'),
      launcherName:
        typeof launcher.querySelector === 'function'
          ? launcher.querySelector('.ori-guide__launcher-name')
          : null,
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
    panel.addEventListener('click', onQuestChoiceClick);
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
    setHelpOnly: setHelpOnly,
    // Deterministic host-authored walkthroughs. See presentQuestStep above for
    // why this is separate from ask() and what it deliberately cannot express.
    presentQuestStep: presentQuestStep,
    clearQuestStep: clearQuestStep,
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
    _isExplicitCommand: isExplicitCommand,
    _refreshContextLabel: refreshContextLabel,
    _state: state,
    _validateAction: validateAction,
    _onQuestChoiceClick: onQuestChoiceClick,
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
