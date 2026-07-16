// personal-hq-email-setup.js — the "Set up email" button + modal on the
// workspace detail page. It appears ONLY when the current workspace is the
// user's designated Personal HQ, and opens a modal that reuses the loadout-chip
// view-model to route setup correctly: no OAuth client → Settings, configured →
// Connect, connected → Disconnect, stale → Reconnect.
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

  const body = modal.querySelector('.hq-email-setup-body');

  function currentWorkspaceId() {
    if (window.currentWorkspaceId) return String(window.currentWorkspaceId);
    const parts = window.location.pathname.split('/');
    return parts[2] || '';
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

  function openModal() {
    modal.hidden = false;
    modal.classList.add('is-open');
    renderBody();
  }
  function closeModal() {
    modal.hidden = true;
    modal.classList.remove('is-open');
  }

  function connectEmail() {
    const popup = window.open('/api/vault/email/oauth/start?provider=gmail', 'ori-hq-email', 'width=520,height=680');
    if (!popup) {
      toast('Allow pop-ups to connect your email.', 'danger');
      return;
    }
    const onMessage = async (event) => {
      const data = event && event.data;
      if (!data || data.type !== 'ori:vault-email-oauth') return;
      window.removeEventListener('message', onMessage);
      if (!data.success || !data.account || !data.account.id) {
        toast(data && data.error ? data.error : 'Could not connect your email.', 'danger');
        return;
      }
      try {
        await postJSON('/api/personal-hq/email/link', { account_id: data.account.id });
        toast('Email connected to your Personal HQ.', 'success');
        renderBody();
      } catch (_) {
        toast('Connected the account but could not link it to your HQ.', 'danger');
      }
    };
    window.addEventListener('message', onMessage);
  }

  async function renderBody() {
    if (!body) return;
    body.textContent = 'Loading…';
    const [statusResp, oauth] = await Promise.all([
      fetchJSON('/api/personal-hq/email/status'),
      fetchJSON('/api/settings/email-oauth')
    ]);
    const status = statusResp && statusResp.status ? statusResp.status : null;
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
        window.location.href = '/settings#personal-hq-email';
        return;
      }
      if (view.action === 'disconnect') {
        action.disabled = true;
        try {
          await postJSON('/api/personal-hq/email/unlink');
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

  // Show the button only when this workspace is the user's valid, designated HQ.
  async function maybeShowButton() {
    const wsId = currentWorkspaceId();
    if (!wsId) return;
    const data = await fetchJSON('/api/personal-hq/status');
    const status = data && data.status;
    if (status && status.valid && String(status.workspace_id) === wsId) {
      btn.hidden = false;
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
