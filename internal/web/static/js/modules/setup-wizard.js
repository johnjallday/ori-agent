// setup-wizard.js — the shared blueprint Setup Wizard dialog.
//
// One controller serves every wizard-enabled blueprint. It owns the shell:
// identity, progress, navigation, disclosure placement, busy/error states,
// dismissal, and completion. A domain module owns only the *content* of a step
// kind, registered through registerStepRenderer(); it can render controls and
// perform its own domain calls, but it cannot replace the dialog, decide
// readiness, or advance the flow.
//
// Two things are deliberately not done here:
//
//   - Readiness is never decided in the browser. Every action posts to the
//     server and re-renders from the status it returns, so a step reads
//     "complete" only because the server evaluated its requirement, and a
//     workspace that regressed says so on the next look.
//   - Author text is never treated as markup. Titles, descriptions, and
//     disclosures come from a blueprint manifest and are written with
//     textContent, never innerHTML.
(function () {
  'use strict';

  const STATE_READY = 'ready';
  const STATE_NEEDS_ATTENTION = 'needs_attention';
  const STATE_NOT_APPLICABLE = 'not_applicable';

  const STATUS_COMPLETE = 'complete';
  const STATUS_BLOCKED = 'blocked';
  const STATUS_OPTIONAL_SKIPPED = 'optional_skipped';

  // The server names each step's next action; the browser never invents one.
  const ACTION_RECHECK = 'recheck';

  // Step marks double every color cue with a character, so progress is legible
  // in monochrome and to a screen reader.
  const STEP_MARKS = {
    complete: '✓',
    blocked: '!',
    optional_skipped: '–',
    active: '•',
    pending: '•'
  };
  const STEP_WORDS = {
    complete: 'done',
    blocked: 'needs attention',
    optional_skipped: 'skipped',
    active: 'current',
    pending: 'not started'
  };

  // kind -> [renderer]. A list rather than a single entry because step kinds
  // are shared: `account_link` belongs to both Email Ops and GitHub Ops, and
  // `capability_configure` to both Calendar Ops and GitHub Ops. With a single
  // slot the last module to load silently replaced every earlier one, and the
  // blueprint that lost showed the generic controls — a step whose primary
  // button could not do anything.
  const renderers = new Map();

  // rendererFor picks the renderer that claims this step.
  //
  // A renderer may declare `owns(step)` to claim only its own blueprint's
  // steps. One that does not declare it keeps the old behavior exactly --
  // last registered wins -- so existing renderers are unaffected.
  function rendererFor(step) {
    if (!step) return undefined;
    const list = renderers.get(step.kind);
    if (!list || !list.length) return undefined;
    const claimed = list.find(renderer => {
      try {
        return typeof renderer.owns === 'function' && renderer.owns(step);
      } catch {
        return false;
      }
    });
    if (claimed) return claimed;
    const unclaimed = list.filter(renderer => typeof renderer.owns !== 'function');
    return unclaimed.length ? unclaimed[unclaimed.length - 1] : undefined;
  }

  let workspaceId = '';
  let status = null;
  // Fresh generalized runtime status for runtime-aware blueprints. The first
  // project-specific verification remains explicit. Once it has succeeded,
  // reloading the workspace performs a new harmless live recheck rather than
  // reusing a stale Connected result or making the user check manually again.
  let runtimeStatus = null;
  let currentStepId = '';
  let busy = false;
  let openedOnce = false;
  // The last thing said in the live region, so the same sentence is not
  // repeated over a user on every re-render.
  let lastAnnouncement = '';

  function els() {
    return {
      dialog: document.getElementById('setupWizardDialog'),
      banner: document.getElementById('setupWizardBanner'),
      bannerState: document.getElementById('setupWizardBannerState'),
      bannerDetail: document.getElementById('setupWizardBannerDetail'),
      bannerAction: document.getElementById('setupWizardBannerAction'),
      chip: document.getElementById('setupWizardStatusChip'),
      icon: document.getElementById('setupWizardIcon'),
      blueprint: document.getElementById('setupWizardBlueprint'),
      title: document.getElementById('setupWizardTitle'),
      steps: document.getElementById('setupWizardSteps'),
      stepTitle: document.getElementById('setupWizardStepTitle'),
      stepDescription: document.getElementById('setupWizardStepDescription'),
      stepContent: document.getElementById('setupWizardStepContent'),
      disclosure: document.getElementById('setupWizardDisclosure'),
      disclosureBody: document.getElementById('setupWizardDisclosureBody'),
      error: document.getElementById('setupWizardError'),
      live: document.getElementById('setupWizardLive'),
      stepLive: document.getElementById('setupWizardStepLive'),
      back: document.getElementById('setupWizardBack'),
      skip: document.getElementById('setupWizardSkip'),
      primary: document.getElementById('setupWizardPrimary'),
      close: document.getElementById('setupWizardClose')
    };
  }

  // The browser route carries a slug. API identity comes only from the UUID
  // the server publishes before deferred scripts run.
  function resolveWorkspaceId() {
    return String(
      (typeof window !== 'undefined' && window.currentWorkspaceId) ||
        (typeof document !== 'undefined' && document.body?.dataset?.workspaceId) ||
        ''
    );
  }

  function resumeKey() {
    return `oriSetupWizardResume:${workspaceId}`;
  }

  async function api(path, options) {
    const response = await fetch(
      `/api/workspaces/${encodeURIComponent(workspaceId)}/setup-wizard${path}`,
      options || {}
    );
    const payload = await response.json().catch(() => ({}));
    if (!response.ok) {
      const error = new Error(payload?.message || payload?.error || 'Setup request failed');
      error.code = payload?.code || '';
      throw error;
    }
    return payload?.setup || null;
  }

  async function runtimeApi(path = '', options) {
    const response = await fetch(
      `/api/workspaces/${encodeURIComponent(workspaceId)}/runtime-capabilities${path}`,
      options || {}
    );
    const payload = await response.json().catch(() => ({}));
    if (!response.ok) {
      const error = new Error(
        payload?.message || payload?.error || 'Runtime status request failed'
      );
      error.code = payload?.code || '';
      throw error;
    }
    return payload?.runtime || null;
  }

  function hasRuntimeSteps() {
    return (status?.steps || []).some(
      step => step?.kind === 'runtime_mode' || step?.kind === 'runtime_readiness'
    );
  }

  function shouldAutoRecheckRuntime(runtime) {
    if (
      !runtime?.applicable ||
      runtime.selected_mode_id === 'file_only' ||
      runtime.durable_state !== 'configured' ||
      String(runtime.live_state || 'not_checked') !== 'not_checked'
    ) {
      return false;
    }
    const requirement = Array.isArray(runtime.requirements) ? runtime.requirements[0] : null;
    return Boolean(runtime.first_verified_at || requirement?.first_verified_at);
  }

  async function refreshRuntime({ live = false } = {}) {
    if (!workspaceId || !hasRuntimeSteps()) {
      runtimeStatus = null;
      return null;
    }
    runtimeStatus = await runtimeApi(live ? '/recheck' : '', live ? { method: 'POST' } : undefined);
    render();
    return runtimeStatus;
  }

  function setRuntimeStatus(next) {
    runtimeStatus = next && next.applicable ? next : null;
    render();
  }

  function stepById(id) {
    if (!status || !Array.isArray(status.steps)) return null;
    return status.steps.find(step => step.id === id) || null;
  }

  function stepIndex(id) {
    if (!status || !Array.isArray(status.steps)) return -1;
    return status.steps.findIndex(step => step.id === id);
  }

  function currentStep() {
    return stepById(currentStepId) || (status?.steps || [])[0] || null;
  }

  function isResolved(step) {
    return step?.status === STATUS_COMPLETE || step?.status === STATUS_OPTIONAL_SKIPPED;
  }

  // firstUnresolvedId is where the wizard resumes: the step the server named,
  // or the first unresolved one when it named none.
  function firstUnresolvedId() {
    if (status?.current_step_id) return status.current_step_id;
    const pending = (status?.steps || []).find(step => !isResolved(step));
    return pending ? pending.id : (status?.steps || [])[0]?.id || '';
  }

  function setBusy(value, message) {
    busy = Boolean(value);
    const { dialog, live } = els();
    if (dialog?.classList) dialog.classList.toggle('setup-wizard-busy', busy);
    // The progress message is cleared as well as set: a stale "Applying your
    // choice…" left under a finished step is a live region telling the user
    // something is still happening when nothing is.
    if (live && message !== undefined) live.textContent = message || '';
    renderActions();
  }

  function setError(message) {
    const { error } = els();
    if (!error) return;
    error.textContent = message ? String(message) : '';
    error.hidden = !message;
    // Bring the reason into view. On a step with a long body -- a repository
    // list, a folder list -- the error sits below the fold, so a rejected
    // choice looked like nothing happened at all.
    if (message && typeof error.scrollIntoView === 'function') {
      try {
        error.scrollIntoView({ block: 'nearest', behavior: 'smooth' });
      } catch {
        error.scrollIntoView();
      }
    }
    if (message && typeof error.focus === 'function') error.focus();
  }

  function announce(message) {
    const { live } = els();
    if (live) live.textContent = message || '';
  }

  // ---------- rendering ----------

  function render() {
    renderBanner();
    if (!status || !status.applicable) return;
    const { icon, blueprint, title } = els();
    if (icon) icon.textContent = status.blueprint_icon || '🧭';
    if (blueprint) blueprint.textContent = status.blueprint_name || 'Blueprint setup';
    if (title) title.textContent = status.title || 'Set up this workspace';
    renderSteps();
    renderStep();
    renderActions();
  }

  function renderSteps() {
    const { steps } = els();
    if (!steps) return;
    steps.textContent = '';
    (status?.steps || []).forEach((step, index) => {
      const state =
        step.id === currentStepId && !isResolved(step) ? 'active' : step.status || 'pending';
      const item = document.createElement('li');
      item.className = `setup-wizard-step setup-wizard-step-${state}`;
      if (step.id === currentStepId) item.setAttribute('aria-current', 'step');

      const mark = document.createElement('span');
      mark.className = 'setup-wizard-step-mark';
      mark.setAttribute('aria-hidden', 'true');
      mark.textContent = STEP_MARKS[state] || '•';

      const label = document.createElement('span');
      const name = step.title || defaultStepTitle(step);
      // The status word is part of the text, not an aria-label, so it is
      // announced and visible rather than only one or the other.
      label.textContent = `${index + 1}. ${name} (${STEP_WORDS[state] || 'not started'})`;

      item.appendChild(mark);
      item.appendChild(label);
      steps.appendChild(item);
    });
  }

  function defaultStepTitle(step) {
    switch (step?.kind) {
      case 'directory':
        return 'Choose a folder';
      case 'automation_review':
        return 'Review automation';
      case 'capability_connect':
        return 'Connect';
      case 'capability_configure':
        return 'Configure';
      case 'account_link':
        return 'Link an account';
      case 'plugin_readiness':
        return 'Prepare the plugin';
      case 'readiness':
        return 'Readiness check';
      case 'runtime_mode':
        return 'Choose an operating mode';
      case 'runtime_readiness':
        return 'Runtime readiness';
      case 'summary':
        return 'Summary';
      default:
        return 'Setup step';
    }
  }

  function renderStep() {
    const step = currentStep();
    const { stepTitle, stepDescription, stepContent, disclosure, disclosureBody } = els();
    setError('');
    if (!step) return;

    if (stepTitle) stepTitle.textContent = step.title || defaultStepTitle(step);
    if (stepDescription) stepDescription.textContent = step.description || step.summary || '';

    if (stepContent) {
      stepContent.textContent = '';
      const renderer = rendererFor(step);
      if (renderer && typeof renderer.render === 'function') {
        try {
          renderer.render(stepContent, rendererContext(step));
        } catch (error) {
          console.warn('Setup step renderer failed:', error);
          setError('This step could not be displayed. Try Check again.');
        }
      } else {
        renderDefaultStepContent(stepContent, step);
      }
    }

    // The disclosure is rendered last, immediately above the footer's primary
    // action, so what the user is approving is the last thing they read.
    const text = step.disclosure || step.directory_access_disclosure || '';
    if (disclosure) disclosure.hidden = !text;
    if (disclosureBody) disclosureBody.textContent = text;

    announceStep(step);
  }

  // announceStep says where the user is, for someone who cannot see the step
  // list. Without it, confirming a step is silent: the heading changes, the
  // progress marks change, and a screen-reader user hears nothing at all.
  //
  // Only a changed message is written, because re-announcing the same step on
  // every re-render would talk over the user.
  function announceStep(step) {
    const steps = status?.steps || [];
    const position = stepIndex(step.id);
    const title = step.title || defaultStepTitle(step);
    // The same derivation the step list uses, so what is announced matches what
    // is on screen: the current step reads as current even before the server
    // has recorded anything about it.
    const derived = step.id === currentStepId && !isResolved(step) ? 'active' : step.status;
    const state = STEP_WORDS[derived] || '';
    const message =
      position >= 0 && steps.length
        ? `Step ${position + 1} of ${steps.length}: ${title}${state ? ` — ${state}` : ''}`
        : title;
    if (message === lastAnnouncement) return;
    lastAnnouncement = message;
    // The step-position region, not the footer's progress line: that one carries
    // transient "applying…" text a sighted user reads, and permanently parking
    // the step name there would duplicate the heading beside it.
    const { stepLive } = els();
    if (stepLive) stepLive.textContent = message;
    else announce(message);
  }

  function renderDefaultStepContent(container, step) {
    // A step that offers a choice renders it here, so a blueprint gets a real
    // decision — with each option's consequences stated next to it — without
    // any domain code. The IDs come from the server and go straight back to it.
    if (Array.isArray(step.options) && step.options.length) {
      renderStepOptions(container, step);
      return;
    }
    if (step.kind === 'summary') {
      // The server's own sentence comes first. On a summary step it is where
      // the limits of "ready" are stated — what was set up, and what was never
      // checked — and a step list of ticks alone would quietly drop it.
      if (step.summary) {
        const verdict = document.createElement('p');
        verdict.className = 'setup-wizard-step-description';
        verdict.textContent = step.summary;
        container.appendChild(verdict);
      }
      const list = document.createElement('ul');
      list.className = 'setup-wizard-summary';
      (status?.steps || [])
        .filter(other => other.id !== step.id)
        .forEach(other => {
          const item = document.createElement('li');
          item.className = 'setup-wizard-summary-item';
          const mark = document.createElement('span');
          mark.className = 'setup-wizard-summary-mark';
          mark.textContent = STEP_MARKS[other.status] || '•';
          const label = document.createElement('span');
          label.textContent = `${other.title || defaultStepTitle(other)} — ${STEP_WORDS[other.status] || 'not started'}`;
          item.appendChild(mark);
          item.appendChild(label);
          list.appendChild(item);
        });
      container.appendChild(list);
      return;
    }
    if (step.summary) {
      const summary = document.createElement('p');
      summary.className = 'setup-wizard-step-description';
      summary.textContent = step.summary;
      container.appendChild(summary);
    }
  }

  // renderStepOptions draws an adapter-declared choice. Choosing is the action:
  // there is no separate confirm, because picking one *is* the decision the
  // step was asking for.
  function renderStepOptions(container, step) {
    const list = document.createElement('ul');
    list.className = 'setup-wizard-options';
    step.options.forEach(option => {
      const item = document.createElement('li');
      item.className = 'setup-wizard-option' + (option.selected ? ' is-selected' : '');

      const choose = document.createElement('button');
      choose.type = 'button';
      choose.className =
        'modern-btn ' + (option.selected ? 'modern-btn-secondary' : 'modern-btn-primary');
      choose.textContent = option.selected ? `${option.label} (chosen)` : option.label;
      choose.disabled = busy;
      choose.addEventListener('click', () => confirmStep(step.id, option.id));
      item.appendChild(choose);

      if (option.description) {
        const description = document.createElement('p');
        description.className = 'setup-wizard-option-detail';
        description.textContent = option.description;
        item.appendChild(description);
      }
      list.appendChild(item);
    });
    container.appendChild(list);
  }

  function renderActions() {
    const { back, skip, primary } = els();
    const step = currentStep();
    const index = stepIndex(currentStepId);

    if (back) {
      back.hidden = index <= 0;
      back.disabled = busy;
    }
    if (skip) {
      // Only an optional step can be skipped, and only while it is unresolved.
      skip.hidden = !step || step.required || isResolved(step);
      skip.disabled = busy;
    }
    if (primary) {
      primary.textContent = primaryLabel(step);
      // A step whose own control is the action — picking a folder, choosing a
      // connector — says so instead of offering a Continue that can only fail.
      primary.disabled = busy || !step || primaryBlocked(step);
    }
  }

  // primaryBlocked lets a renderer report that the shell's primary control
  // cannot do anything useful yet. The renderer decides, because only it knows
  // whether its own action has been taken.
  function primaryBlocked(step) {
    if (!step || isResolved(step)) return false;
    // An unanswered choice is answered by choosing, not by Continue.
    if (
      Array.isArray(step.options) &&
      step.options.length &&
      !step.options.some(option => option.selected)
    ) {
      return true;
    }
    const renderer = rendererFor(step);
    if (renderer && typeof renderer.disablePrimary === 'function') {
      return Boolean(renderer.disablePrimary(rendererContext(step)));
    }
    return false;
  }

  function primaryLabel(step) {
    if (!step) return 'Continue';
    const last = stepIndex(step.id) === (status?.steps || []).length - 1;
    if (isResolved(step)) {
      return last ? 'Finish' : 'Continue';
    }
    // What the primary control does is the server's call — only it knows
    // whether a step has a requirement to re-check or a decision to record.
    if (
      Array.isArray(step.options) &&
      step.options.length &&
      !step.options.some(option => option.selected)
    ) {
      return 'Choose an option to continue';
    }
    if (step.action === ACTION_RECHECK) return 'Check again';
    const renderer = rendererFor(step);
    if (renderer && typeof renderer.primaryLabel === 'function') {
      const label = renderer.primaryLabel(rendererContext(step));
      if (label) return label;
    }
    if (step.status === STATUS_BLOCKED) return 'Try again';
    return 'Approve and continue';
  }

  function renderBanner() {
    const { banner, bannerState, bannerDetail, bannerAction } = els();
    renderChip();
    if (!banner) return;
    if (!status || !status.applicable || status.state === STATE_NOT_APPLICABLE) {
      banner.hidden = true;
      return;
    }
    banner.hidden = false;
    const presentation = bannerPresentation(status, runtimeStatus);
    if (bannerState) {
      bannerState.textContent = presentation.state;
      bannerState.className = `setup-wizard-banner-state setup-wizard-banner-state-${presentation.tone}`;
    }
    if (bannerDetail) bannerDetail.textContent = presentation.detail;
    if (bannerAction) bannerAction.textContent = presentation.action;
  }

  // The station chip is the same status in the strip the Command/Map view
  // relocates: one persisted state, rendered wherever the user happens to be.
  function renderChip() {
    const { chip } = els();
    if (!chip) return;
    if (!status || !status.applicable || status.state === STATE_NOT_APPLICABLE) {
      chip.hidden = true;
      return;
    }
    const presentation = bannerPresentation(status, runtimeStatus);
    chip.hidden = false;
    chip.textContent = runtimeStatus?.applicable
      ? `Live control: ${presentation.state}`
      : `Setup: ${presentation.state}`;
    chip.setAttribute('aria-label', `${presentation.state} — ${presentation.action}`);
    chip.className = `workspace-detail-config-chip setup-wizard-chip is-${presentation.tone}`;
  }

  function bannerPresentation(current, runtime) {
    if (runtime?.applicable) {
      const selected = String(runtime.selected_mode_id || '');
      if (selected === 'file_only') {
        return {
          state: 'File-only',
          tone: 'ready',
          detail: 'Project-file work is available. Live REAPER was not configured or tested.',
          action: 'Review mode'
        };
      }
      const requirement = Array.isArray(runtime.requirements) ? runtime.requirements[0] : null;
      const live = String(runtime.live_state || requirement?.live_state || 'not_checked');
      const durable = String(runtime.durable_state || requirement?.durable_state || 'in_progress');
      if (durable === 'configured') {
        switch (live) {
          case 'available':
            return {
              state: 'Connected now',
              tone: 'ready',
              detail: requirement?.summary || 'REAPER is connected to this workspace project now.',
              action: 'View live control'
            };
          case 'checking':
            return {
              state: 'Checking connection',
              tone: 'ready',
              detail: 'Refreshing the current REAPER connection for this workspace project…',
              action: 'View live control'
            };
          case 'offline':
            return {
              state: 'Configured · REAPER offline',
              tone: 'ready',
              detail: requirement?.summary || 'Open REAPER, then check the connection.',
              action: 'Check REAPER'
            };
          case 'wrong_target':
            return {
              state: 'Wrong project',
              tone: 'attention',
              detail: requirement?.summary || 'Open this workspace’s project in REAPER.',
              action: 'Fix project'
            };
          default:
            return {
              state: 'Configured',
              tone: 'ready',
              detail: 'Live control is configured. Current connectivity has not been checked.',
              action: 'Check connection'
            };
        }
      }
      if (durable === 'needs_attention' || current.state === STATE_NEEDS_ATTENTION) {
        return {
          state: 'Needs attention',
          tone: 'attention',
          detail:
            runtime.first_blocker?.summary ||
            requirement?.summary ||
            'Live-control setup needs repair.',
          action: 'Repair live control'
        };
      }
      return {
        state: 'Setup required',
        tone: 'required',
        detail:
          runtime.first_blocker?.summary ||
          requirement?.summary ||
          'Finish local REAPER control setup.',
        action: 'Continue setup'
      };
    }
    if (current.state === STATE_READY) {
      return { state: 'Ready', tone: 'ready', detail: 'Setup is complete.', action: 'View setup' };
    }
    if (current.state === STATE_NEEDS_ATTENTION) {
      return {
        state: 'Needs attention',
        tone: 'attention',
        detail: 'Something this workspace depends on stopped working.',
        action: 'Repair setup'
      };
    }
    return {
      state: 'Setup required',
      tone: 'required',
      detail: current.diagnostic
        ? 'This workspace’s recorded setup cannot be run by this version of Ori.'
        : 'Finish setup so this workspace can do its job.',
      action: 'Continue setup'
    };
  }

  // rendererContext is the whole surface a domain renderer gets. It can read
  // the step, act through the shared actions, and report progress — but it
  // cannot navigate, complete, or decide readiness.
  function rendererContext(step) {
    return {
      step,
      status,
      runtimeStatus,
      workspaceId,
      setBusy: (value, message) => setBusy(value, message),
      setError: message => setError(message),
      announce: message => announce(message),
      refresh: () => refresh(),
      refreshRuntime: options => refreshRuntime(options),
      setRuntimeStatus,
      runtimeRequest: (path, options) => runtimeApi(path, options),
      confirm: option => confirmStep(step.id, option),
      recheck: () => recheck(),
      // renderDefault lets a renderer registered on a shared kind (readiness,
      // summary) draw only its own blueprint's steps and hand every other
      // blueprint's back, instead of blanking them.
      renderDefault: container => renderDefaultStepContent(container, step),
      // rememberReturn records where to resume before the browser leaves for an
      // external authorization, so the same workspace, wizard, and step are
      // restored on return.
      rememberReturn: () => rememberResume(step.id)
    };
  }

  // ---------- actions ----------

  function rememberResume(stepId) {
    try {
      window.sessionStorage?.setItem(resumeKey(), stepId || '');
    } catch (error) {
      console.warn('Could not record the setup resume point:', error);
    }
  }

  function consumeResume() {
    try {
      const value = window.sessionStorage?.getItem(resumeKey()) || '';
      if (value) window.sessionStorage?.removeItem(resumeKey());
      return value;
    } catch (error) {
      return '';
    }
  }

  async function refresh() {
    if (!workspaceId) return null;
    try {
      status = await api('', { method: 'GET' });
      if (hasRuntimeSteps()) {
        try {
          const durableRuntime = await runtimeApi();
          runtimeStatus = durableRuntime;
          if (shouldAutoRecheckRuntime(durableRuntime)) {
            runtimeStatus = { ...durableRuntime, live_state: 'checking' };
            render();
            try {
              runtimeStatus = await runtimeApi('/recheck', { method: 'POST' });
            } catch (liveError) {
              console.warn('Runtime live recheck failed:', liveError);
              runtimeStatus = durableRuntime;
            }
          }
        } catch (runtimeError) {
          console.warn('Runtime status request failed:', runtimeError);
          runtimeStatus = null;
        }
      } else {
        runtimeStatus = null;
      }
    } catch (error) {
      console.warn('Setup status request failed:', error);
      return null;
    }
    if (!currentStepId || !stepById(currentStepId)) {
      currentStepId = firstUnresolvedId();
    }
    render();
    publish();
    return status;
  }

  function publish() {
    try {
      document.dispatchEvent(new CustomEvent('ori:setup-status', { detail: status }));
    } catch (error) {
      /* CustomEvent is unavailable in some embedding contexts; the banner has
         already been updated directly, so this is not worth failing over. */
    }
  }

  async function act(fn, pendingMessage) {
    if (busy) return;
    setError('');
    setBusy(true, pendingMessage || 'Working…');
    try {
      status = await fn();
      if (!stepById(currentStepId)) currentStepId = firstUnresolvedId();
      render();
      publish();
      return status;
    } catch (error) {
      setError(friendlyError(error));
      return null;
    } finally {
      setBusy(false, '');
    }
  }

  function friendlyError(error) {
    switch (error?.code) {
      case 'adapter_unavailable':
        return 'This step is unavailable in this build of Ori. Everything else you have set up is kept.';
      case 'unsupported_setup_wizard':
        return 'This workspace’s recorded setup cannot be run by this version of Ori.';
      case 'unknown_step':
        return 'That step is no longer part of this workspace’s setup. Reload to see the current steps.';
      default:
        return error?.message || 'That step could not be completed. Try again.';
    }
  }

  async function confirmStep(stepId, option) {
    const body = option ? JSON.stringify({ option }) : '{}';
    const result = await act(
      () =>
        api(`/steps/${encodeURIComponent(stepId)}/confirm`, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body
        }),
      'Applying your choice…'
    );
    if (result && stepById(stepId)?.kind === 'runtime_mode') {
      try {
        await refreshRuntime();
      } catch (error) {
        setError(friendlyError(error));
      }
    }
    if (result) await advanceIfResolved(stepId);
    return result;
  }

  async function skipStep(stepId) {
    const result = await act(
      () => api(`/steps/${encodeURIComponent(stepId)}/skip`, { method: 'POST' }),
      'Skipping…'
    );
    if (result) await advanceIfResolved(stepId);
    return result;
  }

  async function recheck() {
    return act(() => api('/recheck', { method: 'POST' }), 'Checking…');
  }

  async function complete() {
    const result = await act(() => api('/complete', { method: 'POST' }), 'Finishing setup…');
    if (result && result.state === STATE_READY) {
      closeDialog();
      announceCompletion();
    }
    return result;
  }

  function announceCompletion() {
    const presentation = bannerPresentation(status, runtimeStatus);
    const message = runtimeStatus?.applicable
      ? `${presentation.state} setup complete.`
      : 'Setup complete — this workspace is ready.';
    if (window.Toast?.success) window.Toast.success(message);
    else if (typeof window.notifyToast === 'function') window.notifyToast(message, 'success');
  }

  // advanceIfResolved moves forward only when the server confirmed the step.
  // A step that did not pass keeps the wizard where it is, with the reason
  // visible, rather than sliding past a requirement that is not satisfied.
  async function advanceIfResolved(stepId) {
    const step = stepById(stepId);
    if (!step || !isResolved(step)) {
      renderStep();
      renderActions();
      return;
    }
    const next = (status?.steps || [])[stepIndex(stepId) + 1];
    if (!next && status?.state === STATE_READY) {
      // Resolving the last step finished setup. Asking for one more click on a
      // Finish button that cannot fail would be ceremony, not confirmation.
      await complete();
      return;
    }
    currentStepId = next ? next.id : stepId;
    render();
  }

  function goBack() {
    const index = stepIndex(currentStepId);
    if (index <= 0) return;
    currentStepId = status.steps[index - 1].id;
    render();
    focusHeading();
  }

  async function onPrimary() {
    const step = currentStep();
    if (!step) return;
    const index = stepIndex(step.id);
    const last = index === (status?.steps || []).length - 1;

    if (isResolved(step)) {
      if (!last) {
        currentStepId = status.steps[index + 1].id;
        render();
        focusHeading();
        return;
      }
      if (status.state === STATE_READY) {
        await complete();
        return;
      }
      // The last step is done but a required one earlier is not: send the user
      // back to it rather than offering a Finish that cannot succeed.
      currentStepId = firstUnresolvedId();
      render();
      focusHeading();
      setError('One or more required steps still need attention.');
      return;
    }

    if (step.action === ACTION_RECHECK) {
      await recheck();
      await advanceIfResolved(step.id);
      return;
    }

    const renderer = rendererFor(step);
    if (renderer && typeof renderer.onPrimary === 'function') {
      await renderer.onPrimary(rendererContext(step));
      return;
    }
    await confirmStep(step.id);
  }

  // ---------- dialog lifecycle ----------

  function focusHeading() {
    const { title } = els();
    if (title && typeof title.focus === 'function') title.focus();
  }

  function openDialog(stepId) {
    const { dialog } = els();
    if (!dialog || !status || !status.applicable) return;
    currentStepId = stepId && stepById(stepId) ? stepId : firstUnresolvedId();
    render();
    if (!dialog.open && typeof dialog.showModal === 'function') {
      dialog.showModal();
    } else if (!dialog.open) {
      // Environments without <dialog> support still get a usable panel.
      dialog.setAttribute('open', 'open');
    }
    openedOnce = true;
    focusHeading();
    // Not cleared here: renderStep() has already said which step this is, and
    // blanking it would leave a keyboard user with no idea where they landed.

    // Then re-ask the server. Setup can move outside this dialog — the folder
    // picker, a connector's own panel, a permission granted in Settings, a
    // second tab — so opening on the last render would show a step as
    // outstanding that the user has already satisfied.
    void refreshOnOpen(stepId);
  }

  // refreshOnOpen records the open and re-renders from the server's answer. It
  // runs after the dialog is already on screen, so a slow evaluation delays the
  // content rather than the dialog.
  async function refreshOnOpen(stepId) {
    try {
      status = await api('/open', { method: 'POST' });
      if (hasRuntimeSteps()) runtimeStatus = await runtimeApi();
    } catch (error) {
      console.warn('Could not refresh setup state on open:', error);
      return;
    }
    if (!status || !status.applicable) return;
    currentStepId = stepId && stepById(stepId) ? stepId : firstUnresolvedId();
    render();
    publish();
  }

  function closeDialog() {
    const { dialog } = els();
    if (dialog?.open && typeof dialog.close === 'function') dialog.close();
    else if (dialog) dialog.removeAttribute('open');
  }

  // requestClose is the one path out of the dialog. A finished wizard just
  // closes; an unfinished one records the dismissal server-side and says where
  // to pick it up, so closing never reads as "this workspace is ready".
  async function requestClose() {
    if (busy) {
      announce('Wait for the current step to finish before closing.');
      return false;
    }
    closeDialog();
    if (!status || status.state === STATE_READY) return true;
    try {
      status = await api('/dismiss', { method: 'POST' });
      render();
      publish();
    } catch (error) {
      console.warn('Could not record the setup dismissal:', error);
    }
    const message =
      'Setup is not finished. Pick it up any time from Continue setup on this workspace.';
    if (window.Toast?.info) window.Toast.info(message);
    else if (typeof window.notifyToast === 'function') window.notifyToast(message, 'info');
    return true;
  }

  function bind() {
    const { dialog, back, skip, primary, close, bannerAction } = els();
    if (back) back.addEventListener('click', goBack);
    if (skip)
      skip.addEventListener('click', () => {
        const step = currentStep();
        if (step) skipStep(step.id);
      });
    if (primary) primary.addEventListener('click', () => onPrimary());
    if (close) close.addEventListener('click', () => requestClose());
    if (bannerAction) bannerAction.addEventListener('click', () => openDialog());
    const { chip } = els();
    if (chip) chip.addEventListener('click', () => openDialog());
    if (dialog) {
      // Escape reaches us as a cancelable `cancel` event: refusing it while a
      // step is committing is what keeps a half-finished grant from being left
      // behind by a keystroke.
      dialog.addEventListener('cancel', event => {
        event.preventDefault();
        requestClose();
      });
    }
  }

  // init is idempotent and returns the same promise to every caller: the page
  // script awaits it to sequence its own first-open behavior behind the
  // wizard's, and the module also self-starts on DOM ready. Binding twice would
  // double every click.
  let initPromise = null;

  function init() {
    if (!initPromise) initPromise = initOnce();
    return initPromise;
  }

  async function initOnce() {
    workspaceId = resolveWorkspaceId();
    if (!workspaceId || !document.getElementById('setupWizardDialog')) return null;
    bind();
    const current = await refresh();
    if (!current || !current.applicable) return current;

    const resumeStep = consumeResume();
    const params = new URLSearchParams(window.location?.search || '');
    const runtimeRequested = params.get('runtime_setup') === '1';
    const requested = params.get('setup') === '1' || runtimeRequested;

    if (resumeStep) {
      // Returning from an external authorization: same workspace, same wizard,
      // same step.
      openDialog(resumeStep);
      return status;
    }
    if (requested) {
      const runtimeStep = runtimeRequested
        ? (status.steps || []).find(step => step.kind === 'runtime_readiness')?.id
        : '';
      openDialog(runtimeStep);
      return status;
    }
    if (current.auto_open && !openedOnce) {
      // openDialog records the open server-side, so the one auto-open is spent
      // even if the user closes the tab without touching anything.
      openDialog();
    }
    return status;
  }

  const SetupWizard = {
    init,
    refresh,
    open: openDialog,
    close: requestClose,
    getStatus: () => status,
    getRuntimeStatus: () => runtimeStatus,
    refreshRuntime,
    setRuntimeStatus,
    registerStepRenderer(kind, renderer) {
      if (!kind || !renderer) return;
      const key = String(kind);
      const list = renderers.get(key) || [];
      // Registering the same renderer twice is a no-op rather than a
      // duplicate entry, so a module that re-registers on a retry does not
      // grow the list.
      if (!list.includes(renderer)) list.push(renderer);
      renderers.set(key, list);
      if (status && currentStep()?.kind === kind) renderStep();
    },
    // Exposed for tests and for domain modules that need the same vocabulary.
    _internals: {
      STEP_MARKS,
      STEP_WORDS,
      bannerPresentation,
      shouldAutoRecheckRuntime,
      primaryLabel,
      primaryBlocked,
      friendlyError
    }
  };

  window.SetupWizard = SetupWizard;

  if (typeof document !== 'undefined' && typeof document.addEventListener === 'function') {
    if (document.readyState === 'loading') {
      document.addEventListener('DOMContentLoaded', () => init());
    } else {
      init();
    }
  }
})();
