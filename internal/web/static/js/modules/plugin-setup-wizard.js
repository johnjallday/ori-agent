// A dialog stepper over the folder setup card. It owns no server calls and no
// consent of its own: every package effect goes through the controller from
// createFolderPrerequisites, so each plugin change still needs its own review
// and its own explicit confirmation.

const ACTIONABLE = ['install', 'update', 'enable'];
const VERBS = { install: 'Install', update: 'Update', enable: 'Enable' };

export function pluginSetupPlan(rows, kind = '') {
  const steps = (Array.isArray(rows) ? rows : [])
    .filter(row => ACTIONABLE.includes(row?.operation))
    .map(row => ({
      kind: row.kind,
      operation: row.operation,
      name: row.name,
      version: row.version || '',
      purpose: row.purpose || '',
      status: row.status || ''
    }));
  // The plugin the user pressed comes first. The rest keep the offer's order.
  return [...steps.filter(step => step.kind === kind), ...steps.filter(step => step.kind !== kind)];
}

export function createPluginSetupWizard({ doc, controller, onClose = () => {} }) {
  let dialog = null;
  let parts = null;
  let state = { open: false, plan: [], index: 0, trigger: null };

  const make = (tag, text = '', className = '') => {
    const node = doc.createElement(tag);
    if (text) node.textContent = text;
    if (className) node.className = className;
    return node;
  };

  const current = () => state.plan[state.index] || null;

  function requestCancel() {
    if (!state.open) return;
    controller.cancel();
    close();
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
    header.append(title);
    const body = make('div', '', 'plugin-setup-wizard__body');
    const footer = make('footer', '', 'plugin-setup-wizard__footer');
    dialog.append(header, footer, body);
    parts = { title, body, footer };
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

  function render() {
    if (!state.open || !parts) return;
    const step = current();
    parts.title.textContent = step
      ? `Plugin ${state.index + 1} of ${state.plan.length} · ${step.name}`
      : '';
    parts.body.replaceChildren(make('h3', `${VERBS[step?.operation] || 'Review'} ${step?.name}`));
    parts.footer.replaceChildren(button('Cancel', requestCancel));
  }

  function restoreFocus(kind, trigger) {
    const target = trigger?.isConnected
      ? trigger
      : doc.querySelector?.(
          `[data-requirement-kind="${kind}"] .folder-requirements__actions button`
        );
    target?.focus?.();
  }

  function close() {
    if (!state.open) return;
    const { trigger, plan } = state;
    const kind = plan[0]?.kind;
    state = { open: false, plan: [], index: 0, trigger: null };
    if (dialog?.open && typeof dialog.close === 'function') dialog.close();
    else dialog?.removeAttribute?.('open');
    onClose();
    restoreFocus(kind, trigger);
  }

  return {
    open(rows, { kind = '', trigger = null } = {}) {
      if (state.open) return;
      const plan = pluginSetupPlan(rows, kind);
      if (!plan.length) return;
      build();
      state = { open: true, plan, index: 0, trigger };
      render();
      if (!dialog.open && typeof dialog.showModal === 'function') dialog.showModal();
      // Environments without <dialog> support still get a usable panel.
      else if (!dialog.open) dialog.setAttribute('open', 'open');
      parts.title.focus?.();
    },
    close,
    sync: render,
    view: () => ({
      open: state.open,
      index: state.index,
      total: state.plan.length,
      kind: current()?.kind || '',
      title: parts?.title.textContent || ''
    })
  };
}
