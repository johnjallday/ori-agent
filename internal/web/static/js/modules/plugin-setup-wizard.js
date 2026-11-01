// A dialog stepper over the folder setup card. It owns no server calls and no
// consent of its own: every package effect goes through the controller from
// createFolderPrerequisites, so each plugin change still needs its own review
// and its own explicit confirmation.
//
// Sequencing contract. After a confirmed change the wizard reads the refreshed
// requirements and follows only what the server now offers:
//   - The row is `ready`: this plugin is finished, move on.
//   - The row is `enable`, the change just made was an install or update, and
//     its review said it would not enable: stay on this plugin and ask for the
//     enable review. That is a review, not a consent; it still needs Confirm.
//   - Anything else, including a missing row: stop on this plugin, show the
//     row's status, and offer Back / Finish. Never a second install, and never
//     an action the server did not offer.

const ACTIONABLE = ['install', 'update', 'enable'];
const VERBS = { install: 'Install', update: 'Update', enable: 'Enable' };
const CONFIRM_LABELS = {
  install: 'Confirm installation',
  update: 'Confirm update',
  enable: 'Confirm enablement'
};
const APPLYING = { install: 'Installing', update: 'Updating', enable: 'Enabling' };
const APP_WIDE =
  'Ori plugins are app-wide. Once installed they serve every future music folder, not only this one.';
const NOTHING_CREATED = 'No workspace, Home, agent or model task was created.';

export function pluginSetupPlan(rows, kind = '') {
  const steps = (Array.isArray(rows) ? rows : [])
    .filter(row => ACTIONABLE.includes(row?.operation))
    .map(row => ({
      kind: row.kind,
      operation: row.operation,
      original: row.operation,
      name: row.name,
      version: row.version || '',
      purpose: row.purpose || '',
      progress: 'Waiting',
      applied: false
    }));
  // The plugin the user pressed comes first. The rest keep the offer's order.
  return [...steps.filter(step => step.kind === kind), ...steps.filter(step => step.kind !== kind)];
}

// What the refreshed offer says about a plugin, never what the wizard believes
// it did. A change the server accepted but the offer does not yet show is
// reported as unconfirmed rather than as "Not changed".
export function pluginFinalState(step, row) {
  const operation = row?.operation;
  if (!row) return 'Could not be confirmed';
  if (operation === 'ready')
    return step.original === 'update' ? 'Updated' : 'Installed and enabled';
  if (operation === 'enable')
    return step.original === 'update' && step.applied
      ? 'Updated, not enabled'
      : 'Installed, not enabled';
  if (operation === 'install' || operation === 'update')
    return step.applied ? 'Could not be confirmed' : 'Not changed';
  return row.status || 'Could not be confirmed';
}

export function createPluginSetupWizard({
  doc,
  controller,
  trust = {},
  readRows = () => [],
  blocked = () => false,
  onClose = () => {}
}) {
  let dialog = null;
  let parts = null;
  let signature = '';
  let state = closedState();

  function closedState() {
    return {
      open: false,
      phase: '',
      plan: [],
      index: 0,
      trigger: null,
      launchKind: '',
      changed: false,
      reading: false,
      confirming: false,
      frozen: null,
      notice: '',
      session: 0
    };
  }

  const make = (tag, text = '', className = '') => {
    const node = doc.createElement(tag);
    if (text) node.textContent = text;
    if (className) node.className = className;
    return node;
  };

  const current = () => state.plan[state.index] || null;
  const rowFor = kind => {
    const rows = readRows();
    return (Array.isArray(rows) ? rows : []).find(row => row.kind === kind) || null;
  };

  function leave(next) {
    // A new phase first: controller.cancel() redraws, and the redraw must not
    // read the cleared review as a setup that changed underneath the wizard.
    state.session++;
    state.phase = 'leaving';
    state.reading = false;
    if (!controller.view().pending) controller.cancel();
    next();
  }

  function requestCancel() {
    if (!state.open || state.confirming) return;
    if (state.phase === 'summary') return close();
    const step = current();
    if (step && state.phase !== 'intro') step.progress = 'Stopped here';
    leave(() => (state.changed ? showSummary() : close()));
  }

  function backTarget() {
    if (state.phase === 'intro' || state.phase === 'summary') return null;
    for (let index = state.index - 1; index >= 0; index--) {
      if (ACTIONABLE.includes(rowFor(state.plan[index].kind)?.operation)) return index;
    }
    return state.plan.length > 1 ? 'intro' : null;
  }

  function goBack() {
    const target = backTarget();
    if (target === null || state.confirming || controller.view().pending) return;
    const step = current();
    if (step && step.progress !== 'Done') step.progress = 'Waiting';
    leave(() => {
      if (target === 'intro') {
        state.phase = 'intro';
        render();
      } else void enter(target);
    });
  }

  function showSummary() {
    state.phase = 'summary';
    state.notice = '';
    render();
  }

  function stopHere(step, notice) {
    step.progress = 'Stopped here';
    state.phase = 'stopped';
    state.notice = notice;
    render();
  }

  function advance() {
    if (state.index + 1 < state.plan.length) void enter(state.index + 1);
    else showSummary();
  }

  async function enter(index) {
    const session = ++state.session;
    state.index = index;
    state.phase = 'review';
    state.notice = '';
    const step = current();
    if (controller.view().pending) {
      // An unknown result keeps its exact envelope. Show it, ask for nothing new.
      step.progress = 'Reviewing';
      render();
      return;
    }
    const row = rowFor(step.kind);
    if (row?.operation === 'ready') {
      step.progress = 'Done';
      advance();
      return;
    }
    if (!ACTIONABLE.includes(row?.operation)) {
      stopHere(step, unavailableNotice(step, row));
      return;
    }
    step.operation = row.operation;
    step.progress = 'Reviewing';
    state.reading = true;
    render();
    await controller.review(step.kind);
    if (session !== state.session) return;
    state.reading = false;
    const view = controller.view();
    if (view.review && !ACTIONABLE.includes(controller.operation())) {
      // Nothing left to change, so there is nothing to consent to.
      leave(() => {
        state.phase = 'review';
        step.progress = 'Done';
        advance();
      });
      return;
    }
    if (!view.review && !view.error && !view.pending) {
      stopHere(step, 'This setup changed while the wizard was open. Nothing else was changed.');
      return;
    }
    render();
  }

  function unavailableNotice(step, row) {
    return row
      ? `${step.name}: ${row.status || 'Not available'}. Ori will not offer another change here.`
      : `${step.name} is no longer part of this setup. Ori will not offer another change here.`;
  }

  async function confirm() {
    if (state.confirming) return;
    const step = current();
    const view = controller.view();
    const action = controller.operation() || view.pending?.action || step.operation;
    state.confirming = true;
    state.frozen = view.review;
    step.progress = APPLYING[action] || 'Installing';
    render();
    const session = state.session;
    const result = await controller.confirm();
    if (session !== state.session) return;
    state.confirming = false;
    state.frozen = null;
    if (result?.completed) {
      state.changed = true;
      step.applied = true;
      settle(step, result);
      return;
    }
    const after = controller.view();
    step.progress = 'Reviewing';
    if (!after.review && !after.error && !after.pending) {
      stopHere(step, 'This setup changed while the wizard was open. Nothing else was changed.');
      return;
    }
    render();
  }

  function settle(step, { action, enabledAfter }) {
    const row = rowFor(step.kind);
    if (row?.operation === 'ready') {
      step.progress = 'Done';
      advance();
      return;
    }
    if (row?.operation === 'enable' && action !== 'enable' && !enabledAfter) {
      void enter(state.index);
      return;
    }
    stopHere(step, unavailableNotice(step, row));
  }

  function build() {
    if (dialog) return;
    dialog = make('dialog', '', 'plugin-setup-wizard');
    dialog.id = 'pluginSetupWizard';
    dialog.setAttribute('aria-labelledby', 'pluginSetupWizardTitle');
    const header = make('header', '', 'plugin-setup-wizard__header');
    const title = make('h2', '', 'plugin-setup-wizard__title');
    title.id = 'pluginSetupWizardTitle';
    title.tabIndex = -1;
    const progress = make('p', '', 'plugin-setup-wizard__progress');
    progress.setAttribute('role', 'status');
    header.append(title, progress);
    const decision = make('div', '', 'plugin-setup-wizard__decision');
    const footer = make('footer', '', 'plugin-setup-wizard__footer');
    const body = make('div', '', 'plugin-setup-wizard__body');
    // The choice precedes the long report for keyboard and screen-reader users;
    // the stylesheet keeps the footer at the bottom of the screen.
    dialog.append(header, decision, footer, body);
    parts = { title, progress, decision, footer, body };
    // Escape mirrors Cancel, and only ever through the one cancel path.
    dialog.addEventListener('cancel', event => {
      event.preventDefault();
      requestCancel();
    });
    dialog.addEventListener('keydown', event => {
      if (event.key !== 'Escape') return;
      event.preventDefault();
      event.stopPropagation();
      requestCancel();
    });
    // A browser may force a modal shut. Never lose a change still in flight.
    dialog.addEventListener('close', () => {
      if (!state.open) return;
      if (state.confirming) dialog.showModal?.();
      else requestCancel();
    });
    doc.body.append(dialog);
  }

  function button(label, action, { primary = false, disabled = false } = {}) {
    const node = make(
      'button',
      label,
      primary ? 'btn btn-primary btn-sm' : 'btn btn-outline-secondary btn-sm'
    );
    node.type = 'button';
    node.disabled = disabled;
    node.addEventListener('click', action);
    return node;
  }

  function line(text, className = '', role = '') {
    const node = make('p', text, className);
    if (role) node.setAttribute('role', role);
    return node;
  }

  function titleText() {
    if (state.phase === 'intro') return 'What this setup needs';
    if (state.phase === 'summary') return 'Plugin setup summary';
    const step = current();
    return step ? `Plugin ${state.index + 1} of ${state.plan.length} · ${step.name}` : '';
  }

  function introView() {
    const list = make('ul', '', 'plugin-setup-wizard__list');
    for (const step of state.plan) {
      const row = rowFor(step.kind);
      const item = make('li');
      item.append(make('strong', step.name));
      if (step.version) item.append(line(step.version, 'plugin-setup-wizard__muted'));
      if (step.purpose) item.append(line(step.purpose));
      item.append(line(row?.status || 'Not reported', 'plugin-setup-wizard__muted'));
      list.append(item);
    }
    return {
      decision: [line(APP_WIDE)],
      body: [list],
      footer: [
        button('Continue', () => void enter(0), { primary: true }),
        button('Cancel', requestCancel)
      ]
    };
  }

  function summaryView() {
    const list = make('ul', '', 'plugin-setup-wizard__list');
    for (const step of state.plan) {
      const item = make('li');
      item.append(
        make('strong', step.name),
        line(pluginFinalState(step, rowFor(step.kind)), 'plugin-setup-wizard__final')
      );
      list.append(item);
    }
    return {
      decision: [line(NOTHING_CREATED)],
      body: [list],
      footer: [button('Back to setup', close, { primary: true })]
    };
  }

  function stoppedView() {
    const back = backTarget();
    return {
      decision: [line(state.notice, '', 'status')],
      body: [],
      footer: [
        ...(back === null ? [] : [button('Back', goBack)]),
        button('Finish', showSummary, { primary: true })
      ]
    };
  }

  function reviewView() {
    const step = current();
    const view = controller.view();
    const review = state.frozen || view.review;
    const operation = (review && (review.operation || review.action)) || step.operation;
    const reading = state.reading || (view.busy && !review);
    const decision = [make('h3', `${VERBS[operation] || 'Review'} ${step.name}`)];
    const body = [];
    let confirmable = false;
    if (review) {
      decision.push(
        line(
          `Release ${review.version || 'not reported'} · ${review.source || 'Source not reported'}`
        )
      );
      const report = review.disclosure && trust.render?.(review.disclosure, { summary: false });
      confirmable = operation === 'enable' || Boolean(report);
      if (review.disclosure) decision.push(line(trust.summary?.(review.disclosure) || ''));
      decision.push(
        line(
          `Enabled after this action: ${review.enabled_after ? 'Yes' : 'No — the next step enables it'}`
        )
      );
      body.push(
        report ||
          line(
            operation === 'enable'
              ? 'Enables the installed, reviewed package. No download.'
              : 'Disclosure unavailable. Do not confirm an undisclosed installation.'
          ),
        line(
          'This changes only this Ori plugin. No workspace, Home, agent or model task is created or started.',
          'plugin-setup-wizard__muted'
        )
      );
      if (review.details) {
        const details = make('details', '', 'plugin-setup-wizard__details');
        details.append(
          make('summary', 'Everything Ori reviewed'),
          make('pre', JSON.stringify(review.details, null, 2))
        );
        body.push(details);
      }
    } else if (reading) {
      decision.push(line('Reading the reviewed package…', '', 'status'));
    }
    const pending = Boolean(view.pending) && !state.confirming;
    const inFlight = state.confirming || (view.busy && Boolean(view.pending));
    const back = backTarget();
    const footer = [];
    if (back !== null)
      footer.push(button('Back', goBack, { disabled: inFlight || Boolean(view.pending) }));
    if (inFlight)
      footer.push(button('Applying reviewed change…', () => {}, { primary: true, disabled: true }));
    else if (pending)
      footer.push(
        button('Check the same installation', () => void confirm(), {
          primary: true,
          disabled: blocked()
        })
      );
    else if (review)
      footer.push(
        button(CONFIRM_LABELS[operation] || 'Confirm', () => void confirm(), {
          primary: true,
          disabled: blocked() || !confirmable
        })
      );
    else if (!reading)
      footer.push(button('Review again', () => void enter(state.index), { primary: true }));
    footer.push(button('Cancel', requestCancel, { disabled: inFlight }));
    if (view.error && !state.confirming)
      footer.push(line(view.error, 'plugin-setup-wizard__error', 'alert'));
    return { decision, body, footer };
  }

  function currentView() {
    if (state.phase === 'intro') return introView();
    if (state.phase === 'summary') return summaryView();
    if (state.phase === 'stopped') return stoppedView();
    return reviewView();
  }

  function renderSignature() {
    const view = controller.view();
    const review = state.frozen || view.review;
    return JSON.stringify({
      phase: state.phase,
      index: state.index,
      notice: state.notice,
      reading: state.reading,
      confirming: state.confirming,
      blocked: blocked(),
      busy: view.busy,
      pending: Boolean(view.pending),
      error: view.error,
      // The consent secrets never reach the screen, so a new token alone must
      // not rebuild the report and collapse what the user opened.
      review: review ? { ...review, review_token: undefined, run_id: undefined } : null,
      rows:
        state.phase === 'intro' || state.phase === 'summary'
          ? state.plan.map(step => [step.progress, rowFor(step.kind)?.operation])
          : []
    });
  }

  function render() {
    if (!state.open || !parts || state.phase === 'leaving') return;
    parts.title.textContent = titleText();
    parts.progress.textContent = state.plan
      .map(step => `${step.name}: ${step.progress}`)
      .join(' · ');
    const next = renderSignature();
    if (next === signature) return;
    signature = next;
    const hadFocus = dialog.contains?.(doc.activeElement);
    const focusedLabel = hadFocus ? doc.activeElement?.textContent : '';
    const view = currentView();
    parts.decision.replaceChildren(...view.decision);
    parts.footer.replaceChildren(...view.footer);
    parts.body.replaceChildren(...view.body);
    const keep = view.footer.find(node => node.textContent === focusedLabel && !node.disabled);
    // Rebuilding drops focus. Keep it on the same control when that survives,
    // otherwise the title names where the user has landed.
    if (keep) keep.focus?.();
    else parts.title.focus?.();
  }

  function restoreFocus(kind, trigger) {
    if (trigger?.isConnected) {
      trigger.focus?.();
      return;
    }
    const target =
      doc.querySelector?.(
        `[data-requirement-kind="${kind}"] .folder-requirements__actions button`
      ) ||
      doc.querySelector?.('.folder-requirements__actions button') ||
      doc.getElementById?.('personalAssistantInput');
    target?.focus?.();
  }

  function close() {
    if (!state.open) return;
    const { trigger, launchKind, changed } = state;
    const finished = state.session + 1;
    state = closedState();
    state.session = finished;
    signature = '';
    if (dialog?.open && typeof dialog.close === 'function') dialog.close();
    else dialog?.removeAttribute?.('open');
    onClose({ changed });
    restoreFocus(launchKind, trigger);
  }

  return {
    open(rows, { kind = '', trigger = null } = {}) {
      if (state.open) return;
      // An unknown confirmation result outranks the row that was pressed: its
      // exact envelope has to be settled before anything else is reviewed.
      const pendingKind = controller.view().pending?.kind;
      const launchKind = pendingKind || kind;
      const plan = pluginSetupPlan(rows, launchKind);
      if (!plan.length) return;
      build();
      const session = state.session + 1;
      state = { ...closedState(), open: true, plan, trigger, launchKind, session };
      state.phase = plan.length > 1 && !pendingKind ? 'intro' : 'review';
      signature = '';
      if (state.phase === 'intro') render();
      if (!dialog.open && typeof dialog.showModal === 'function') dialog.showModal();
      // Environments without <dialog> support still get a usable panel.
      else if (!dialog.open) dialog.setAttribute('open', 'open');
      if (state.phase === 'review') void enter(0);
      else parts.title.focus?.();
    },
    close,
    // Called on every card redraw. A review the controller dropped because the
    // offer moved on is reported, never silently asked for again.
    sync() {
      if (!state.open) return;
      if (state.phase === 'review' && !state.reading && !state.confirming) {
        const view = controller.view();
        if (!view.review && !view.pending && !view.error && !view.busy) {
          stopHere(
            current(),
            'This setup changed while the wizard was open. Nothing else was changed.'
          );
          return;
        }
      }
      render();
    },
    view: () => ({
      open: state.open,
      phase: state.phase,
      index: state.index,
      total: state.plan.length,
      kind: current()?.kind || '',
      title: parts?.title.textContent || '',
      progress: state.plan.map(step => step.progress),
      changed: state.changed,
      notice: state.notice
    })
  };
}
