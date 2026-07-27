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

import { emailStatusView, chipStateLabel } from './personal-hq-onboarding.js';

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
    if (!res.ok) throw new Error((data && (data.error || data.message)) || 'Request failed');
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
  async function connectEmail() {
    if (!scope) return;
    let conn;
    try {
      conn = await fetchJSON('/api/connections/google/status');
    } catch (_) {
      toast('Could not check your Google connection.', 'danger');
      return;
    }
    const gmail = conn && Array.isArray(conn.grants) ? conn.grants.find((g) => g.product === 'gmail') : null;
    if (!(conn && conn.subject && gmail && gmail.health === 'healthy')) {
      toast('Connect Google and enable Gmail in Settings → Google Account first.', 'danger');
      window.open('/settings#google-account', '_blank');
      return;
    }
    try {
      const linked = await postJSON(
        '/api/connections/google/gmail/link?workspace_id=' + encodeURIComponent(currentWorkspaceId()),
        {}
      );
      if (!linked || !linked.account_id) throw new Error('no account');
      await postJSON(scope.linkUrl, { account_id: linked.account_id });
      toast('Email connected to ' + scope.connectedTarget + '.', 'success');
      renderBody();
    } catch (_) {
      toast('Could not connect email via your Google account.', 'danger');
    }
  }

  async function renderBody() {
    if (!body || !scope) return;
    body.textContent = 'Loading…';
    const [statusResp, oauth] = await Promise.all([
      fetchJSON(scope.statusUrl),
      fetchJSON('/api/settings/email-oauth')
    ]);
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
    const view = emailStatusView(status, oauth ? !!oauth.configured : true);

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
    action.className = 'modern-btn modern-btn-sm ' + (view.action === 'disconnect' ? 'modern-btn-secondary' : 'modern-btn-primary');
    action.textContent = view.actionLabel;
    action.addEventListener('click', async () => {
      if (view.action === 'settings') {
        window.location.href = '/settings#google-account';
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
    sub.textContent = 'Link an account so this workspace can triage your inbox and draft replies. Nothing is ever sent without your confirmation.';
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

  btn.addEventListener('click', openModal);
  modal.addEventListener('click', (e) => {
    if (e.target && e.target.hasAttribute && e.target.hasAttribute('data-hq-email-close')) closeModal();
  });
  document.addEventListener('keydown', (e) => {
    if (e.key === 'Escape' && modal.classList.contains('is-open')) closeModal();
  });

  maybeShowButton();
})();
