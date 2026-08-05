// personal-hq-email-setup.js — the "Set up email" button + modal on the
// workspace detail page. It appears when the current workspace can hold an
// email account: the user's designated Personal HQ (legacy in-place email) OR
// the user's Email Ops workspace (Mail spin-off). The modal reuses the
// loadout-chip view-model to route setup correctly: no OAuth client → Settings,
// configured → Connect, connected → Disconnect, stale → Reconnect. The link /
// status / unlink calls are scoped to whichever workspace this is (HQ endpoints
// for the HQ, workspace-scoped endpoints for Email Ops).
//
// Self-contained (no Bootstrap): a lightweight overlay toggled by a class. The
// pure view-model helpers are imported from personal-hq-onboarding.js so the
// state logic has one source of truth.

import { emailSetupView, chipStateLabel } from './personal-hq-onboarding.js';

(function () {
  if (typeof document === 'undefined') return;

  const btn = document.getElementById('hqEmailSetupBtn');
  const modal = document.getElementById('hqEmailSetupModal');
  if (!btn || !modal) return;

  // The modal starts inside #workspace-detail-shared-hosts, which is always
  // [hidden] at rest (workspace-command.js only relocates the Systems/
  // Members panels it explicitly mounts — see mountSharedSurface). Unlike
  // those, this modal is a self-contained overlay with no "restore" need, so
  // move it to <body> once so un-hiding it isn't blocked by a hidden
  // ancestor regardless of which view/mode is active when it's opened.
  if (document.body && modal.parentElement !== document.body) {
    document.body.appendChild(modal);
  }

  const body = modal.querySelector('.hq-email-setup-body');

  function currentWorkspaceId() {
    if (window.currentWorkspaceId) return String(window.currentWorkspaceId);
    const parts = window.location.pathname.split('/');
    return parts[2] || '';
  }

  // scope is set by resolveScope() to the endpoints for whichever email-capable
  // workspace this is (HQ vs Email Ops). All link/status/unlink calls read it.
  let scope = null;

  // resolveScope decides whether this workspace can hold email, and which
  // endpoints to use: the designated Personal HQ (legacy in-place binding) uses
  // the HQ endpoints; the Email Ops workspace uses the workspace-scoped ones.
  // Returns null when this workspace is neither (button stays hidden).
  async function resolveScope() {
    const wsId = currentWorkspaceId();
    if (!wsId) return null;

    const hq = await fetchJSON('/api/personal-hq/status');
    const hqStatus = hq && hq.status;
    if (hqStatus && hqStatus.valid && String(hqStatus.workspace_id) === wsId) {
      return {
        kind: 'hq',
        isHQ: true,
        statusUrl: '/api/personal-hq/email/status',
        linkUrl: '/api/personal-hq/email/link',
        unlinkUrl: '/api/personal-hq/email/unlink',
        connectedTarget: 'your Personal HQ'
      };
    }

    const eo = await fetchJSON('/api/personal-hq/email-ops');
    const eoStatus = eo && eo.status;
    if (eoStatus && eoStatus.exists && String(eoStatus.workspace_id) === wsId) {
      const base = '/api/workspaces/' + encodeURIComponent(wsId) + '/email';
      return {
        kind: 'workspace',
        isHQ: false,
        statusUrl: base + '/status',
        linkUrl: base + '/link',
        unlinkUrl: base + '/unlink',
        connectedTarget: 'Email Ops'
      };
    }
    return null;
  }

  async function fetchJSON(url) {
    try {
      const res = await fetch(url, { headers: { Accept: 'application/json' } });
      return res.ok ? await res.json() : null;
    } catch (_) {
      return null;
    }
  }

  async function postJSON(url, payload) {
    const res = await fetch(url, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: payload ? JSON.stringify(payload) : undefined
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) {
      // Keep the server's machine code and status on the error so callers can
      // tell an actionable conflict (e.g. a credential that no longer exists)
      // from a genuine fault, instead of collapsing both into one message.
      const err = new Error((data && (data.message || data.error)) || 'Request failed');
      err.code = (data && data.error) || '';
      err.status = res.status;
      err.action = (data && data.action) || '';
      throw err;
    }
    return data;
  }

  function toast(message, variant) {
    if (window.Toast && typeof window.Toast[variant || 'success'] === 'function') {
      window.Toast[variant || 'success'](message);
    }
  }

  // Re-renders the command map's Email station after window.OriHQEmailSetup
  // changes, so the station reflects connect/disconnect without a page
  // reload. refresh() is a no-op when the command view isn't the active
  // surface (e.g. Details mode).
  function notifyCommandView() {
    if (
      typeof window !== 'undefined' &&
      window.workspaceCommand &&
      typeof window.workspaceCommand.refresh === 'function'
    ) {
      window.workspaceCommand.refresh();
    }
  }

  function openModal() {
    modal.hidden = false;
    modal.classList.add('is-open');
    renderBody();
  }
  function closeModal() {
    modal.hidden = true;
    modal.classList.remove('is-open');
  }

  // Re-pointed to the shared Google Account connection (FR 46/53): no separate
  // OAuth popup and no user-supplied Web client. If Gmail is enabled on the
  // global Google connection, reuse it to give this workspace its own account
  // with no re-authorization; otherwise send the user to set up Google first.
  async function connectEmail(target) {
    const active = target || scope;
    if (!active) return;
    let conn;
    try {
      conn = await fetchJSON('/api/connections/google/status');
    } catch (_) {
      toast('Could not check your Google connection.', 'danger');
      return;
    }
    const gmail =
      conn && Array.isArray(conn.grants) ? conn.grants.find(g => g.product === 'gmail') : null;
    if (!(conn && conn.subject && gmail && gmail.health === 'healthy')) {
      toast('Connect Google and enable Gmail in Settings → Google Account first.', 'danger');
      window.open('/settings#google-account', '_blank');
      return;
    }
    try {
      const linked = await postJSON(
        '/api/connections/google/gmail/link?workspace_id=' +
          encodeURIComponent(currentWorkspaceId()),
        {}
      );
      if (!linked || !linked.account_id) throw new Error('no account');
      await postJSON(active.linkUrl, { account_id: linked.account_id });
      toast('Email connected to ' + active.connectedTarget + '.', 'success');
      renderBody();
    } catch (err) {
      // The grant can reference a credential the vault no longer holds — after a
      // vault is recreated, or a data directory moves. That is a reconnect, not
      // a fault, and saying so is the difference between a dead button and a
      // next step.
      if (err && err.code === 'credential_missing') {
        toast(
          err.message ||
            'Your Gmail credential is no longer in the vault. Re-enable Gmail to reconnect it.',
          'danger'
        );
        window.open('/settings#google-account', '_blank');
        renderBody();
        return;
      }
      toast('Could not connect email via your Google account.', 'danger');
    }
  }

  async function renderBody() {
    if (!body || !scope) return;
    body.textContent = 'Loading…';
    const statusResp = await fetchJSON(scope.statusUrl);
    const status = statusResp && statusResp.status ? statusResp.status : null;
    if (window.OriHQEmailSetup) {
      window.OriHQEmailSetup.connected = !!(status && status.connected);
      window.OriHQEmailSetup.address = (status && status.email_address) || '';
      notifyCommandView();
    }
    // Keep the Email Ops connect CTA in sync after connect/disconnect.
    if (scope && !scope.isHQ) {
      renderWorkspaceConnectCTA(!!(status && status.connected));
    }
    // Setup state comes from the server's deterministic readiness verdict — the
    // account connection, grant health, vault availability, and this
    // workspace's binding — not from anything an agent claimed it did (FR 32).
    const view = emailSetupView(status);

    body.innerHTML = '';

    const head = document.createElement('div');
    head.className = 'hq-loadout-chip-head';
    const name = document.createElement('span');
    name.className = 'hq-loadout-chip-name';
    name.textContent = view.chip || 'Email';
    const chip = document.createElement('span');
    chip.className = 'hq-loadout-chip-state hq-loadout-chip-state-' + (view.chipState || 'empty');
    chip.textContent = chipStateLabel(view.chipState);
    head.append(name, chip);
    body.appendChild(head);

    if (view.detail) {
      const detail = document.createElement('p');
      detail.className = 'hq-loadout-chip-detail';
      detail.textContent = view.detail;
      body.appendChild(detail);
    }

    const action = document.createElement('button');
    action.type = 'button';
    action.className =
      'modern-btn modern-btn-sm ' +
      (view.action === 'disconnect' ? 'modern-btn-secondary' : 'modern-btn-primary');
    action.textContent = view.actionLabel;
    action.setAttribute('aria-label', view.actionLabel);
    action.addEventListener('click', async () => {
      if (view.action === 'settings') {
        window.location.href = view.actionUrl || '/settings#google-account';
        return;
      }
      if (view.action === 'disconnect') {
        action.disabled = true;
        try {
          await postJSON(scope.unlinkUrl);
          toast('Email disconnected.', 'success');
          renderBody();
        } catch (_) {
          toast('Could not disconnect the email account.', 'danger');
          action.disabled = false;
        }
        return;
      }
      connectEmail();
    });
    body.appendChild(action);
  }

  // renderWorkspaceConnectCTA shows a prominent "Connect email" banner on the
  // Email Ops workspace when no account is linked yet, so setup is one click on
  // the workspace surface rather than only an agent starter task. Hidden once
  // connected. No-op without the mount (only present on the workspace detail
  // page).
  function renderWorkspaceConnectCTA(connected) {
    const mount = document.getElementById('workspaceEmailConnectMount');
    if (!mount) return;
    // When the blueprint's wizard owns setup, its banner already says the same
    // thing in the same place. A second call to action next to it would be two
    // front doors into one flow.
    if (window.SetupWizard?.getStatus?.()?.applicable) {
      mount.hidden = true;
      mount.innerHTML = '';
      return;
    }
    if (connected) {
      mount.hidden = true;
      mount.innerHTML = '';
      return;
    }
    mount.innerHTML = '';
    const card = document.createElement('div');
    card.className = 'workspace-email-connect-card';
    const text = document.createElement('div');
    const title = document.createElement('p');
    title.className = 'workspace-email-connect-title';
    title.textContent = 'Connect your email';
    const sub = document.createElement('p');
    sub.className = 'workspace-email-connect-sub';
    sub.textContent =
      'Link an account so this workspace can triage your inbox and draft replies. Nothing is ever sent without your confirmation.';
    text.append(title, sub);
    const connect = document.createElement('button');
    connect.type = 'button';
    connect.className = 'modern-btn modern-btn-primary modern-btn-sm';
    connect.textContent = 'Connect email';
    connect.addEventListener('click', openModal);
    card.append(text, connect);
    mount.appendChild(card);
    mount.hidden = false;
  }

  // Show the button when this workspace can hold email (HQ or Email Ops), using
  // the resolved scope's endpoints.
  async function maybeShowButton() {
    scope = await resolveScope();
    if (!scope) return;
    btn.hidden = false;
    // Publish a tiny global so the command map's Email station (an HQ station
    // registry entry in workspace-command.js) and the Email Ops connect CTA can
    // read connection state and open this modal without a direct import between
    // independently-loaded module scripts.
    const shared = (window.OriHQEmailSetup = window.OriHQEmailSetup || {});
    shared.isHQ = scope.isHQ;
    shared.open = openModal;
    const emailResp = await fetchJSON(scope.statusUrl);
    const emailStatus = emailResp && emailResp.status;
    shared.connected = !!(emailStatus && emailStatus.connected);
    shared.address = (emailStatus && emailStatus.email_address) || '';
    notifyCommandView();
    // On the Email Ops workspace, surface a prominent connect CTA until linked.
    if (!scope.isHQ) {
      renderWorkspaceConnectCTA(shared.connected);
    }
  }

  // --- setup wizard step ----------------------------------------------------
  //
  // On an Email Ops workspace the blueprint's Setup Wizard owns setup, and this
  // step is where the mailbox gets linked. The module keeps its modal for the
  // legacy Personal HQ workspace, which has no wizard: two surfaces, one for
  // each kind of workspace, rather than two ways to set up the same one.

  function ownsStep(step) {
    return String(step?.adapter || '') === 'email_ops';
  }

  // wizardScope is the endpoint set for the workspace whose wizard is asking.
  //
  // It deliberately does not go through resolveScope(): that answers "is this
  // *the* user's Email Ops workspace?", and a second Email Ops workspace would
  // get null and a blank step. A wizard step already knows which workspace it
  // belongs to, and the workspace-scoped endpoints work for any the user owns.
  function wizardScope() {
    const wsId = currentWorkspaceId();
    if (!wsId) return null;
    const base = '/api/workspaces/' + encodeURIComponent(wsId) + '/email';
    return {
      kind: 'workspace',
      isHQ: false,
      statusUrl: base + '/status',
      linkUrl: base + '/link',
      unlinkUrl: base + '/unlink',
      connectedTarget: 'this workspace'
    };
  }

  // renderLinkStep draws the three states that look alike from outside and are
  // repaired differently: no account connected at all, an account without the
  // mail permission, and an account that is fine but not linked *here*.
  // isDesignatedHQ asks one narrow question: is this workspace the user's
  // Personal HQ? Its email lives on the HQ's own binding, and linking a second
  // workspace-scoped mailbox to the same workspace would give it two.
  async function isDesignatedHQ() {
    const hq = await fetchJSON('/api/personal-hq/status');
    const status = hq && hq.status;
    return Boolean(status && status.valid && String(status.workspace_id) === currentWorkspaceId());
  }

  async function renderLinkStep(container, ctx, stepScope) {
    if (await isDesignatedHQ()) {
      // Personal HQ's email onboarding is not migrated (it predates blueprints
      // and has no wizard); saying so beats silently creating a second link.
      container.textContent = '';
      const note = document.createElement('p');
      note.className = 'hq-loadout-chip-detail';
      note.textContent =
        'This workspace is your Personal HQ, and its email is managed by Personal HQ setup rather than here.';
      container.appendChild(note);
      return;
    }
    const statusResp = await fetchJSON(stepScope.statusUrl);
    const status = statusResp && statusResp.status ? statusResp.status : null;
    const setup = (status && status.setup) || {};
    container.textContent = '';

    const detail = document.createElement('p');
    detail.className = 'hq-loadout-chip-detail';
    detail.textContent =
      setup.message ||
      (status && status.connected
        ? 'A mailbox is linked to this workspace.'
        : 'No mailbox is linked to this workspace yet.');
    container.appendChild(detail);

    if (status && status.connected && status.email_address) {
      const who = document.createElement('p');
      who.className = 'hq-loadout-chip-detail';
      who.textContent = 'Linked mailbox: ' + status.email_address;
      container.appendChild(who);
    }

    // The repair the server named. Connecting an account or granting mail
    // access happens in Settings — a different page, so the wizard records
    // where to come back to before sending anyone there.
    const needsSettings =
      setup.action === 'connect_google' ||
      setup.action === 'enable_gmail' ||
      setup.action === 'repair_vault' ||
      setup.action === 'reconnect_gmail';
    const action = document.createElement('button');
    action.type = 'button';
    action.className = 'modern-btn modern-btn-primary modern-btn-sm';
    action.id = 'setupWizardEmailAction';
    action.textContent = needsSettings
      ? setup.action_label || 'Open account settings'
      : status && status.connected
        ? 'Relink this mailbox'
        : 'Link a mailbox';
    action.addEventListener('click', async () => {
      if (needsSettings) {
        ctx.rememberReturn();
        window.location.href = setup.action_url || '/settings#google-account';
        return;
      }
      ctx.setError('');
      ctx.setBusy(true, 'Linking your mailbox…');
      try {
        await connectEmail(stepScope);
        await ctx.refresh();
      } finally {
        ctx.setBusy(false, '');
      }
    });
    container.appendChild(action);
  }

  const linkStepRenderer = {
    render(container, ctx) {
      if (!ownsStep(ctx.step)) return;
      // The wizard can ask for this step before the module has worked out which
      // endpoints this workspace uses — on a first page load it always does.
      // Resolving on demand is what keeps that from leaving an empty step on
      // screen, a failure nothing reports because every request succeeded.
      const stepScope = wizardScope();
      if (!stepScope) return;
      void renderLinkStep(container, ctx, stepScope);
    },
    primaryLabel(ctx) {
      return ownsStep(ctx.step) ? 'Check again' : '';
    }
  };

  if (window.SetupWizard && typeof window.SetupWizard.registerStepRenderer === 'function') {
    window.SetupWizard.registerStepRenderer('account_link', linkStepRenderer);
  }

  btn.addEventListener('click', openModal);
  modal.addEventListener('click', e => {
    if (e.target && e.target.hasAttribute && e.target.hasAttribute('data-hq-email-close'))
      closeModal();
  });
  document.addEventListener('keydown', e => {
    if (e.key === 'Escape' && modal.classList.contains('is-open')) closeModal();
  });

  maybeShowButton();

  // Exposed so the wizard step's content can be asserted without standing up
  // the dialog.
  window.OriEmailOpsSetupSteps = { link: linkStepRenderer };
})();
